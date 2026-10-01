#!/usr/bin/env python3
# ============ scripts/assist_residue_report.py · 职责说明 ============
# 挂件（assist 服务）「中文残片」的**例行读数器**：把现网 assist.log 里的
# 补翻/出栈分档分布，与日文确定性正字表的**覆盖率**，一次跑成一份 stdout 读数。
#
# ★ 10-01 批次（〇-AF 收尾）为什么要造它：这一条链上原有的指标只有「补翻拒绝率」，
#   而它的读法是"手工复问几轮 + 人眼 grep reason="。两个后果都真实发生过：
#     ① **拒绝率不指向"该修哪一边"**。10-01 当天的实测分布是
#        upstream_error 4 / line_count 1 / not_improved 2 / canned_han_residue 4，
#        这四档的运维动作完全不同（等上游自愈 / 抬行数契约 / 加本地正字兜底 / 查模型产物），
#        揉进一个"拒绝率 X%"里就谁也不会去分档，下一批该动哪里全靠猜（094x 那笔账的原话）。
#     ② **真正的漏点当时只留下一句话**。那条被拒的 ja 正文有五个 leaks 形态
#        （料金额／文件／术语库／定价页面／プロfessional），正字表只兜住「文件」一个
#        ——日志里 `fixed=文件`、`before=5 after=4` 就是这一句的字面证据。
#        ⇒ 该盯的是**表的覆盖率（1/5）**，不是拒绝率。这句结论此前只写在 PROGRESS.md 里，
#        本脚本把它变成一个每次都能重跑、能贴进交接文档的读数（`@@RES coverage_x=`）。
#   所以指标改造后的口径是**「覆盖率 ＋ 分档分布」这一对**，拒绝率不再是唯一指标。
#
# 三条硬口径（都是刻意的，改脚本前先读）：
#   - **只读**：只 open() 两个文件（日志 ＋ han_residue.go）读，
#     **绝不写文件、不建目录、不重启服务、不打网络、不碰数据库**；
#     刻意**没有 --out**——读数一律出 stdout，要存档是调用方重定向的事。
#   - **分档名单必须与 Go 侧常量逐字对齐**（下面 REASON_TIERS 那 18 档：
#     han_residue.go 七个 reject* ＋ canned_guard.go 三个 cannedReject*
#     ＋ localize_async.go 八个 canned_sync_*／canned_bg_*）。
#     这一份名单由 backend-go/internal/assist/engine/residue_report_gate_test.go
#     **双向**锁住（Go 有而脚本没有 ⇒ 红；脚本有而 Go 没有 ⇒ 红）——
#     加一个新档名却忘了进例行指标，当场红，而不是"日志里多出一个谁也不认识的档名"。
#     线上若出现名单外的档名（二进制比仓库新），也不静默丢弃：单列 `unknown_reason` 并报 alert。
#   - **正字表不在这里抄一份**：键集合从 backend-go/internal/assist/engine/han_residue.go
#     的 `jaResidueFixups` 现读（AGENTS §一·11「单一事实源」同一条口径：
#     抄一份＝表改了覆盖率还按旧表算，正是本仓反复点名的"两处各写一份"形态）。
#     读不到那张表（变量被改名／文件被挪走）就**退 2 并点名**，
#     绝不退化成"覆盖率恒为 0"那种看起来像结论的假读数。
#
# 语种口径（为什么非日文不进覆盖率）：正字表**只服务日文**
#   ——applyJaResidueFixups 第一道判据就是 `canonicalLang(answerLang) != "ja"` 直接原样返回
#   （AGENTS §一·13 写的"确定性正字表"那一段射程也只有日文：其余语种界面里一个汉字都不该出现，
#   「正字」这个概念不成立，判据是"见汉字即残留"，唯一出路是补翻）。
#   ⇒ 非日文的残片照样进词频表（那是"哪个语种在漏"的读数），但**绝不进覆盖率分母**，
#     并把这条政策原样打在输出里（`nonja_policy=`），免得后人拿全站残片去除以 ja 的表。
#
# 数值口径（AGENTS §一·7 那几条在这里同样成立）：
#   - 正则交替一律 ERE／Python 原生交替，不写 BRE 的 `\|`（BSD grep 之外某些精简环境会当字面量，静默 0 命中）；
#   - `coverage_x` 按"命中片段数／去重片段数"现算并固定两位小数，比对侧只需字符串等值，无需再归一；
#     窗口内**没有日文残片时报 `n/a` 而不是 `0.00`**——0.00 会被读成"表一个都没兜住"，
#     而实际是"今天压根没样本"，这正是 §一·7 那条"静默 0 命中"陷阱的 Python 版本；
#   - 计数不依赖外部命令，全部在本进程内完成，所以没有 `set -e` 下 grep 计数需要 `|| true` 那一类坑。
#
# 用法（默认读生产路径；本机与 CI 用 --log 指到仓库内的夹具）：
#   python3 scripts/assist_residue_report.py                                    # 现网全量
#   python3 scripts/assist_residue_report.py --since 2026-10-01T04              # 只算当天 04:00 起
#   python3 scripts/assist_residue_report.py --log scripts/fixtures/assist_residue_fixture.jsonl
#   （--log / --repo-root 留口的唯一目的就是让本机与 CI 能指到仓库内的夹具，**不是为了写什么东西**）
#
# 退出码：0＝读数已出；2＝输入不可用（日志读不到／正字表解析不出／窗口内一行有效记录都没有）。
#   刻意不给"零读数也算成功"这条路：全零会被误读成"今天没有残留"，那是最难发现的假绿。
# =============================================
import argparse
import json
import os
import re
import sys

PREFIX = "@@RES"

# 默认日志路径（现网 assist 服务的 slog 落盘位置；留 --log 口给本机与 CI 的夹具）。
DEFAULT_LOG = "/opt/ai-assist/data/assist.log"

# 脚本所在目录的上一级＝仓库根（照 scripts/assist_kb_sync.py 的同一条口径）。
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
HAN_RESIDUE_GO = os.path.join(
    REPO_ROOT, "backend-go", "internal", "assist", "engine", "han_residue.go"
)

# REASON_TIERS ＝ 挂件出栈这条链的全部失败分档名（**对外排障契约，逐字照 Go 常量**）。
#
# ⚠️ 右侧那个名字是 Go 侧常量名：日志里看得见的值是**左边的字面量**，
#    两边等值由 residue_report_gate_test.go 双向锁死。新增档名时必须同步往这里加一行，
#    忘了加不是"少一个读数"，而是那一档的失败在例行指标里永久隐身（档名会落进 unknown_reason，
#    脚本会报 alert，但 alert 只覆盖"已经在跑的日志"，指标本身仍然缺腿）。
REASON_TIERS = [
    # —— han_residue.go：补翻底座的七个出口（reject*）——
    "no_upstream",            # rejectNoUpstream    没有可用上游（这一路本来就没资格打补翻那一枪）
    "no_leaks",               # rejectNoLeaks       残片清单为空（出现即说明两边尺子分叉了）
    "upstream_error",         # rejectUpstreamError 上游报错（现网同窗 provider 失败即此类）
    "truncated",              # rejectTruncated     新稿被 max_tokens 截断（半份稿子绝不采用）
    "empty_output",           # rejectEmptyOutput   上游回空／清洗后为空
    "line_count",             # rejectLineCount     行数与上一稿不一致（少了行＝删内容）
    "not_improved",           # rejectNotImproved   残片没严格变少（模型没把点名的每一处都改掉）
    # —— canned_guard.go：canned 出栈闸的三个分档（cannedReject*）——
    "canned_empty",           # cannedRejectEmpty      清洗后为空
    "canned_han_residue",     # cannedRejectResidue    还带着没翻的中文词（现网 en 的 credits充值）
    "canned_brand_injected",  # cannedRejectBrandAdded 原文没提品牌名，译文里却凭空多出品牌名
    # —— localize_async.go：有界同步腿＋后台补翻腿的八个分档（cannedSync*／cannedBg*）——
    "canned_sync_timeout",    # cannedSyncTimeout  同步腿超出自有预算（访客那一屏为什么还是中文）
    "canned_sync_canceled",   # cannedSyncCanceled 访客/反代先走了（跟上游挂了是两种病）
    "canned_sync_upstream",   # cannedSyncUpstream 上游在预算内明确报错
    "canned_bg_timeout",      # cannedBgTimeout    后台腿也踩到自己的预算（冷语种／预算给小了）
    "canned_bg_upstream",     # cannedBgUpstream   后台腿拿到的仍是上游错误
    "canned_bg_gated",        # cannedBgGated      后台腿的产物没过出栈闸（翻成功但不合格）
    "canned_bg_stale",        # cannedBgStale      后台腿在途期间库里那一行变了
    "canned_bg_panic",        # cannedBgPanic      后台腿 panic（被 recover 收住，出现即必须查栈）
]

# 携带档名的三个日志字段。为什么三个都数（不只数 reason）：
#   - reason           ＝ 本条日志自己的档名（全部路径都有）；
#   - gate_reason      ＝ canned_bg_gated 那一行带的**出栈闸**档名（后台腿不合格时到底是哪条判据拒的）；
#   - sync_reason      ＝ 后台腿那几行带的**同步腿**档名（同一条链上两次的病因要成对读，
#                        只数 reason 会让"同步腿超时了多少次"这个运维读数直接缺失）。
REASON_FIELDS = ("reason", "gate_reason", "sync_reason")

# 残片清单字段。leaks ＝ 补翻/对话正文那条路（strings.Join(leaks, ",")）；
# detail ＝ canned 出栈闸那一条，且**只在档名是 canned_han_residue 时**才是逗号拼的残片清单
# （canned_brand_injected 时它是品牌名，不是汉字残片，混进词频表就是污染）。
LEAKS_FIELD = "leaks"
DETAIL_FIELD = "detail"
RESIDUE_GATE_TIERS = {"canned_han_residue"}

# 正字表生效那一条日志的字段：fixed ＝ 实际改掉的键（逗号拼），before/after ＝ 替换前后残片数。
FIXED_FIELD = "fixed"

# jaLatinIntrusionPat 的 Python 同构体（片假名直接嵌小写拉丁＝词被劈开，见 han_residue.go）。
# 只用来**给片段打标签**（class=latin_intrusion），不参与"进不进表"的判断：
# 那一档在 Go 侧是刻意只送补翻、绝不就地替换的，覆盖率里它必然算 missed——
# 打标签是为了让读数一眼能分出"表管得着的汉字词形"与"表按口径就不该管的混排"。
LATIN_INTRUSION_RE = re.compile(r"[ァ-ヴー]+[a-z]{2,}")

# 汉字段（U+4E00–U+9FFF），与 hanRunsOf 同口径。同样只用于打标签。
HAN_RE = re.compile(r"[一-鿿]+")

# 只读声明：这行字符串本身也被 Go 侧锁（residue_report_gate_test.go 的"脚本还在／还是只读"那一条），
# 删掉它就等于把"这个脚本会不会写东西"重新变成读代码才知道的事。
READ_ONLY_MARK = "只读"


def emit(key, value):
    """输出一行读数（统一 @@RES 前缀，便于 grep 与直接贴进交接文档）。"""
    print("%s %s=%s" % (PREFIX, key, value))


def sanitize(name):
    """把语种名收敛成安全的键片段（日志里的 lang 理论上可以是任意串，键里不能带空格/逗号/换行）。"""
    return re.sub(r"[^A-Za-z0-9_-]", "_", str(name or "unknown"))


def load_ja_fixup_keys(go_path):
    """从 han_residue.go 现读确定性正字表的**键集合**（不抄第二份）。

    返回 (按表序的键列表, 键→值的映射)。解析不出来就返回空——调用方据此退 2，
    绝不带着空表算出"覆盖率 0/0"那种假读数（空表算出来的 missed=0／covered=0，
    看上去像"今天全兜住了"，比报错危险得多）。
    """
    try:
        with open(go_path, "r", encoding="utf-8") as fh:
            src = fh.read()
    except OSError as exc:
        sys.stderr.write("正字表源文件读不到：%s（%s）\n" % (go_path, exc))
        return [], {}
    start = src.find("var jaResidueFixups = []jaFixupPair{")
    if start < 0:
        sys.stderr.write("在 %s 里找不到 jaResidueFixups（变量被改名或文件被挪走？）\n" % go_path)
        return [], {}
    end = src.find("\n}", start)
    block = src[start:] if end < 0 else src[start:end]
    pairs = re.findall(r'\{from:\s*"([^"]*)",\s*to:\s*"([^"]*)"\}', block)
    if not pairs:
        sys.stderr.write("jaResidueFixups 块里解析不出任何 {from,to} 行（表结构变了，正则要跟改）\n")
        return [], {}
    keys = []
    mapping = {}
    for frm, to in pairs:
        if frm not in mapping:  # 表内重复键在 Go 侧另有测试拦，这里只保证读数不被重复计数
            keys.append(frm)
        mapping[frm] = to
    return keys, mapping


def iter_records(path, since):
    """逐行读日志，交还「解析成功的记录」；坏行与缺 time 的行分别计数，**任何一行解析失败都不崩**。

    现网 assist.log 可能被别的进程追加过非 JSON 行（脚本重定向、老版本纯文本行、日志轮转残段），
    崩掉＝这份例行读数当天彻底没有，正是本脚本要消灭的形态。
    """
    total = 0
    bad = 0
    no_time = 0
    kept = 0
    records = []
    with open(path, "r", encoding="utf-8", errors="replace") as fh:
        for raw in fh:
            line = raw.strip()
            if not line:
                continue  # 空行不是"坏行"，也不计入读数总量
            total += 1
            try:
                obj = json.loads(line)
            except ValueError:
                bad += 1
                continue
            if not isinstance(obj, dict):
                bad += 1
                continue
            ts = obj.get("time")
            if not isinstance(ts, str) or not ts:
                no_time += 1
                # ★ --since 开启时：没有时间戳的记录无法判断在不在窗口里，宁可信它不在——
                #   让它在输出里留一个 "bad_lines/no_time" 计数即可，不要让它污染 readings。
                if since:
                    continue
            elif since and ts < since:
                # ISO-8601 且同一时区偏移下，字典序＝时间序（现网 slog 全程 +08:00）。
                # 跨偏移比较不保证正确——所以 --since 建议传完整到小时的前缀，读数以 window_first 自证。
                continue
            kept += 1
            records.append(obj)
    return records, {"total": total, "bad": bad, "no_time": no_time, "kept": kept}


def split_fragments(value):
    """把 Go 侧 strings.Join(x, ",") 的字段拆回片段列表（空串／纯空白不产出片段）。"""
    if not isinstance(value, str):
        return []
    return [p.strip() for p in value.split(",") if p.strip()]


def main():
    parser = argparse.ArgumentParser(
        description="挂件中文残片的例行读数（只读，全部结果出 stdout）",
        allow_abbrev=False,
    )
    parser.add_argument("--log", default=DEFAULT_LOG, help="assist.log 路径（CI 指到仓库内夹具）")
    parser.add_argument("--since", default="", help="只统计 time >= 该前缀的行（如 2026-10-01T04）")
    parser.add_argument("--repo-root", default=REPO_ROOT, help="仓库根（用于现读 jaResidueFixups）")
    args = parser.parse_args()

    go_path = os.path.join(args.repo_root, "backend-go", "internal", "assist", "engine", "han_residue.go")

    emit("script", "assist_residue_report.py")
    emit("mode", READ_ONLY_MARK)
    emit("log", args.log)
    emit("since", args.since or "none")

    # ① 正字表先读：读不到就直接退 2——覆盖率那三条读数全部依赖它，
    #    带着空表继续跑就是"报错换成一屏假数"。
    ja_keys, ja_map = load_ja_fixup_keys(go_path)
    emit("ja_table_source", go_path)
    if not ja_keys:
        emit("error", "ja_table_unparsed")
        emit("alert", "正字表解析失败，覆盖率三条读数不可用（详见 stderr）")
        return 2
    emit("ja_table_keys", len(ja_keys))

    try:
        records, stats = iter_records(args.log, args.since)
    except OSError as exc:
        emit("error", "log_unreadable")
        emit("alert", "日志读不到：%s" % exc)
        return 2

    emit("lines_total", stats["total"])
    emit("lines_kept", stats["kept"])
    emit("bad_lines", stats["bad"])
    emit("lines_without_time", stats["no_time"])
    if records:
        times = [r.get("time") for r in records if isinstance(r.get("time"), str) and r.get("time")]
        if times:
            emit("window_first", min(times))
            emit("window_last", max(times))
    if not records:
        # 全零读数**不许当结论**：要么路径错了，要么窗口切空了，两种都不是"今天干净"。
        emit("error", "no_records")
        emit("alert", "窗口内没有一条可解析记录（这不代表「今天没有残留」）")
        return 2

    # ② 分档计数：名单固定 18 行（含零），这样"某一档今天没出现"与"这一档不在指标里"
    #    在 grep 出来的结果上是两件事，不会被混成一件事。
    tier_counts = {t: 0 for t in REASON_TIERS}
    unknown = {}
    for rec in records:
        for field in REASON_FIELDS:
            val = rec.get(field)
            if not isinstance(val, str) or not val:
                continue
            if val in tier_counts:
                tier_counts[val] += 1
            else:
                unknown[val] = unknown.get(val, 0) + 1
    emit("tiers_known", len(REASON_TIERS))
    emit("reason_total", sum(tier_counts.values()))
    for tier in REASON_TIERS:
        emit("reason_" + tier, tier_counts[tier])
    if unknown:
        emit("unknown_reason_count", len(unknown))
        emit("unknown_reason", ",".join(sorted(unknown)))
        for val in sorted(unknown):
            emit("unknown_reason_" + sanitize(val), unknown[val])
        emit("alert", "日志里出现指标名单外的档名（Go 侧新增档却忘了进 REASON_TIERS）")
    else:
        emit("unknown_reason_count", 0)

    # ③ 按语种分组的残片词频（leaks ＋ 出栈闸那条的 detail，后者只在档名是汉字残留时才算）。
    freq = {}
    leak_lines = 0
    for rec in records:
        frags = split_fragments(rec.get(LEAKS_FIELD))
        gate_vals = [rec.get(f) for f in REASON_FIELDS if isinstance(rec.get(f), str)]
        if any(v in RESIDUE_GATE_TIERS for v in gate_vals):
            frags = frags + split_fragments(rec.get(DETAIL_FIELD))
        if not frags:
            continue
        leak_lines += 1
        lang = sanitize(rec.get("lang", "unknown"))
        bucket = freq.setdefault(lang, {})
        for frag in frags:
            bucket[frag] = bucket.get(frag, 0) + 1
    emit("lines_with_leaks", leak_lines)
    emit("leaks_total", sum(sum(v.values()) for v in freq.values()))
    emit("lang_total", len(freq))
    for lang in sorted(freq, key=lambda k: (-sum(freq[k].values()), k)):
        occ = sum(freq[lang].values())
        emit("lang_" + lang, occ)
    for lang in sorted(freq):
        items = sorted(freq[lang].items(), key=lambda kv: (-kv[1], kv[0]))
        emit("leak_freq_" + lang, ",".join("%s:%d" % (f, c) for f, c in items))

    # ④ 覆盖率：**只有日文**进分母（正字表的射程只有 ja，见文件头那条语种口径）。
    ja_frags = freq.get("ja", {})
    covered, missed = [], []
    for idx, frag in enumerate(sorted(ja_frags, key=lambda f: (-ja_frags[f], f)), start=1):
        hits = [k for k in ja_keys if k in frag]
        cls = "latin_intrusion" if LATIN_INTRUSION_RE.search(frag) else (
            "han_form" if HAN_RE.search(frag) else "other")
        verdict = "covered" if hits else "missed"
        matched = "->".join(ja_map[k] for k in hits) if hits else "-"
        # 一个片段一行（而不是四行）：grep '@@RES ja_frag' 就能拿到整张判词表，
        # 贴进交接文档时也不用再手工对齐多行。
        emit("ja_frag_%d" % idx,
             "fragment=%s verdict=%s class=%s matched=%s" % (frag, verdict, cls, matched))
        (covered if hits else missed).append(frag)
    distinct = len(ja_frags)
    emit("ja_distinct_fragments", distinct)
    emit("ja_table_covered", len(covered))
    emit("ja_table_missed", len(missed))
    # ⚠️ 窗口里没有日文残片时**不许**报 coverage_x=0.00：
    #   那会被读成"表一个都没兜住"，而实际是"今天压根没有 ja 样本"——
    #   报 n/a 并单列一条 alert，读的人必须自己去确认窗口切对了没有（同 §一·7"静默 0 命中"那一类坑）。
    emit("coverage_x", ("%.2f" % (len(covered) / float(distinct))) if distinct else "n/a")
    if not distinct:
        emit("alert", "窗口内没有日文残片，coverage_x 无从计算（这不是 0，是没样本）")
    emit("ja_missed_list", ",".join(missed) if missed else "-")
    emit("ja_covered_list", ",".join(covered) if covered else "-")

    # ⑤ 非日文语种：明确标成"不进正字表、只走补翻"，并把它们从覆盖率里彻底隔开。
    nonja = sorted(k for k in freq if k != "ja")
    emit("nonja_langs", ",".join(nonja) if nonja else "-")
    emit("nonja_fragments", sum(len(freq[k]) for k in nonja))
    emit("nonja_policy", "不进正字表_只走补翻")

    # ⑥ 正字表实际生效的那几行（fixed 字段）：覆盖率是"理论上兜不兜得住"，
    #    这一条是"今天真兜住了哪些"，两个读数分开才能看出"表没用上"和"表不够用"是两种病。
    applied = {}
    fixup_lines = 0
    for rec in records:
        keys = split_fragments(rec.get(FIXED_FIELD))
        if not keys:
            continue
        fixup_lines += 1
        for frag in keys:
            applied[frag] = applied.get(frag, 0) + 1
    emit("fixup_lines", fixup_lines)
    emit("fixup_applied_keys", ",".join("%s:%d" % (k, applied[k]) for k in sorted(applied)) if applied else "-")
    return 0


if __name__ == "__main__":
    sys.exit(main())
