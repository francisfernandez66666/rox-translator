#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
关键词矩阵生成器 —— 能言 LangCross 海外 SEO 长尾词库

做什么：
    把「格式 × 语言对 × 场景 × 意图」交叉展开成一批候选关键词，
    按可解释的评分模型排出 P0/P1/P2，产出 CSV 供 build_seo_pages.py 消费。

为什么需要它：
    翻译赛道的自然流量大头不在 "pdf translate" 这类大词（被 DeepL/Google/Adobe 垄断），
    而在 "translate docx to english preserving formatting" 这类三元长尾。
    长尾靠人工想不全面，靠 AI 凭空编又会编出没人搜的词，
    所以做法是「结构化穷举 + 粗估打分 + 真实数据回填」。

★ 关于 volume_tier 的诚实说明：
    本文件里的 volume/competition 是**先验粗估档**，不是真实搜索量。
    正确用法：先用它跑出 P0 铺页面 → 4~8 周后从 Google Search Console
    导出真实 impression/click → 回填本文件顶部的 V*_OVERRIDE → 重跑排序。
    ★ 不要相信任何「AI 估算的月搜索量」，那是编的。

用法：
    python3 scripts/growth/keyword_matrix.py
    python3 scripts/growth/keyword_matrix.py --out out/keywords.csv --top 30
    python3 scripts/growth/keyword_matrix.py --only-p0 --no-format-page
    python3 scripts/growth/keyword_matrix.py --selftest        # 内置自证

零第三方依赖（仅标准库）。
"""

from __future__ import annotations

import argparse
import csv
import os
import re
import sys
from dataclasses import dataclass, fields

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_OUT = os.path.join(HERE, "out", "keywords.csv")

# =============================================================================
# 常量区 ——★ 全部可直接手改，改完重跑即可
# =============================================================================

# 意图层权重：越接近成交越高
INTENT_WEIGHT = {
    "L1": 3.0,  # 交易型：我要现在就翻
    "L2": 3.0,  # 格式保真型：我要翻完版式还在（★ 本产品最强战场）
    "L3": 2.2,  # 场景型：我有一份合同/Listing 要翻
    "L4": 1.0,  # 信息型：我想知道怎么做（引流 + 外链 + GEO 引用）
}

# 需求档 / 竞争档
# ★ high=5 而不是 3：长尾分布是幂律的，头部词的需求量远不止中部的 1.5 倍。
#   用线性档（3/2/1）时头部词会被「中等竞争的中等词」按分数全面压制，
#   排出来的首批全是没人搜的词。
VOLUME_TIER = {"high": 5, "mid": 2, "low": 1}

# ★ 竞争档必须**压缩**（不是 4/2/1 而是 2.0/1.4/1.0）。
#   用线性档时 high/low 相差 4 倍，压过了 volume 的 2.5 倍差距 ⇒
#   「竞争低」主导排序，en→ar / en→pt 这类词以 18 分霸榜，而 zh→en 只有 11.25。
#   现实里高竞争词并非没价值——你有版式保真这个差异化，头部词照样吃得到。
#   压缩后 high/low = 2 < volume 的 2.5 ⇒ 需求量主导，这才是对的。
COMPETITION_TIER = {"high": 2.0, "mid": 1.4, "low": 1.0}

# 你能不能满足这个词（fit）
FIT_WEIGHT = {
    "format": 1.5,       # 原版式交付 —— 能言最强，且用户三秒可自证
    "terminology": 1.3,  # 术语硬闸 / 品牌名保护
    "generic": 1.0,      # 通用翻译能力，没有差异化
}

# 分档：★ 用「名次配额」而不是绝对分数阈值。
#   绝对阈值在这个分数尺度（实测 0.5~27）上失去分辨力——
#   实测阈值 6.0 时 672 词里 P0 高达 283，等于没分档。
#   而真实工作流本来就是「首批铺 N 页」，配额制天然对齐，且调参后体量稳定。
P0_QUOTA = 100     # 首批铺多少页
P1_QUOTA = 200     # 次批

# 绝对下限：分数低于此值，哪怕在配额内也不配 P0（防配额被垃圾词占满）
P0_MIN = 6.0
P1_MIN = 3.0

# ★ 粗估 volume=low 的词硬性封顶 P1，不进 P0。
#   原因：评分里 competition 在分母，「竞争低」会把没人搜的小语种长尾词整片抬进 P0
#   ——首批铺过去就是一批零流量页面。真有量的词请用 V_OVERRIDE 显式抬档，
#   那是数据说话，不是模型猜的。
VOLUME_LOW_CEILING = "P1"

# 自证用的合理区间（防评分模型改坏时不自知）
EXPECT_P0_RANGE = (40, 120)

# ★ 真实数据回填区：拿到 Search Console 数据后，在这里按 slug 覆盖粗估档
#   例：V_OVERRIDE = {"translate-pdf-chinese-to-english-preserving-formatting": "high"}
V_OVERRIDE: dict = {}
C_OVERRIDE: dict = {}

# -----------------------------------------------------------------------------
# 语言对：(src, dst, 需求档, 竞争档)
# 海外优先：以英文搜索市场为主，兼顾中国出海企业（zh→外语）与进入中国市场的海外企业（外语→zh）
# -----------------------------------------------------------------------------
LANG_NAME = {
    "zh": "Chinese", "en": "English", "ja": "Japanese", "ko": "Korean",
    "de": "German", "fr": "French", "es": "Spanish", "ru": "Russian",
    "pt": "Portuguese", "ar": "Arabic", "it": "Italian", "th": "Thai",
}

LANG_PAIRS = [
    # 中国出海：把中文材料翻成外语
    ("zh", "en", "high", "high"),
    ("zh", "ja", "mid",  "mid"),
    ("zh", "ko", "mid",  "mid"),
    ("zh", "de", "mid",  "mid"),
    ("zh", "fr", "mid",  "mid"),
    ("zh", "es", "mid",  "mid"),
    ("zh", "ru", "low",  "low"),
    ("zh", "pt", "low",  "low"),
    ("zh", "ar", "low",  "low"),
    ("zh", "th", "low",  "low"),
    # 进入中国：把外语材料翻成中文
    ("en", "zh", "high", "high"),
    ("ja", "zh", "mid",  "mid"),
    ("ko", "zh", "low",  "mid"),
    ("de", "zh", "low",  "low"),
    ("fr", "zh", "low",  "low"),
    # 海外多语互翻（非中）
    ("en", "es", "high", "high"),
    ("en", "ja", "mid",  "mid"),
    ("en", "de", "mid",  "mid"),
    ("en", "fr", "mid",  "mid"),
    ("en", "pt", "mid",  "mid"),
    ("en", "ar", "mid",  "mid"),
    ("en", "ru", "low",  "low"),
    ("ja", "en", "mid",  "mid"),
    ("ko", "en", "low",  "low"),
]

# -----------------------------------------------------------------------------
# 格式：(code, 搜索用名, 页面上短名, fit, 需求档, 是否走还原文件模式)
# -----------------------------------------------------------------------------
FORMATS = [
    ("pdf",  "pdf",        "PDF",       "format", "high", True),
    ("docx", "word docx",  "Word",      "format", "high", True),
    ("pptx", "powerpoint", "PowerPoint","format", "mid",  True),
    ("xlsx", "excel",      "Excel",     "format", "mid",  True),
    ("srt",  "srt subtitle", "SRT",     "format", "mid",  False),
    ("epub", "epub",       "EPUB",      "generic","low",  False),
    ("md",   "markdown",   "Markdown",  "generic","low",  False),
]

# -----------------------------------------------------------------------------
# 场景：(code, 英文短语, 中文说明, fit, 需求档)
# ★ 每个场景都要有独立文案块（build_seo_pages.py 的 fixtures 里），
#   只换关键词不换内容会被搜索引擎判 doorway page。
# -----------------------------------------------------------------------------
SCENARIOS = [
    ("contract",        "contract",              "合同/协议",        "terminology", "high"),
    ("product-listing", "product listing",       "跨境电商商品详情",  "terminology", "high"),
    ("manual",          "user manual",           "产品说明书/手册",   "format",      "high"),
    ("patent",          "patent",                "专利文件",          "terminology", "mid"),
    ("academic-paper",  "academic paper",        "学术论文",          "generic",     "mid"),
    ("game-localization", "game localization",   "游戏本地化",        "generic",     "mid"),
    ("marketing",       "marketing material",    "市场物料/宣传册",   "format",      "mid"),
    ("subtitle",        "subtitle file",         "字幕文件",          "format",      "low"),
    ("resume",          "resume",                "简历",              "format",      "low"),
    ("financial",       "financial report",      "财报/财务文件",     "terminology", "low"),
]

# 意图修饰词（决定 query 长相与竞争度修正）
MOD_L1 = ""                                   # 交易型
MOD_L2 = "preserving formatting"              # 格式保真型 ★
MOD_L4 = "how to"                             # 信息型

# 竞争度修正：加了修饰后 query 变长尾，竞争显著下降。
# ★ 用连续因子而不是「整档往下挪」——档位触底时（low→low）整档挪会挪不动，
#   导致 L2 与 L1 同分、长尾修饰失去溢价（自证里那条单调性断言就是为它设的）。
COMPETITION_FACTOR = {"L1": 1.0, "L2": 0.5, "L3": 0.7, "L4": 0.7}
# 展示用的档位降级步数（仅影响 CSV 里的 competition_tier 可读性）
COMPETITION_RELAX = {"L1": 0, "L2": 2, "L3": 1, "L4": 1}


# =============================================================================
# 评分与生成
# =============================================================================

@dataclass
class Row:
    slug: str
    keyword: str
    intent: str
    page_type: str     # tool | scenario | guide
    src: str
    dst: str
    fmt: str           # 格式 code，场景型为空
    scenario: str      # 场景 code，格式型为空
    volume_tier: str
    competition_tier: str
    fit: str
    score: float
    priority: str
    zh_hint: str       # 中文说明，供人工复核


def _slugify(s: str) -> str:
    s = s.lower()
    s = re.sub(r"[^a-z0-9]+", "-", s)
    s = re.sub(r"-+", "-", s).strip("-")
    return s


def _tier_down(t: str, steps: int) -> str:
    """把档位往下（竞争更小）挪 n 档：high→mid→low"""
    order = ["low", "mid", "high"]
    i = order.index(t)
    return order[max(0, i - steps)]


def _tier_min(a: str, b: str) -> str:
    """两个维度取更低的档——★ 需求量受限于最短的那块板。

    一个词的需求量 = min(格式需求, 语言对需求)，不是其中任何一个单独决定。
    早期版本写成「格式是 high 就一律 high」，结果 zh→ru / de→zh 这类几乎没人搜的
    组合被判成 high volume，直接霸占了首批 P0。
    """
    order = ["low", "mid", "high"]
    return order[min(order.index(a), order.index(b))]


def _score(intent: str, volume: str, competition: str, fit: str) -> float:
    return (
        INTENT_WEIGHT[intent]
        * VOLUME_TIER[volume]
        * FIT_WEIGHT[fit]
        / (COMPETITION_TIER[competition] * COMPETITION_FACTOR[intent])
    )


def _priority(score: float, volume: str, rank: int) -> str:
    """名次配额为主，绝对下限为兜底，低量词封顶。三者是「与」的关系。"""
    if rank < P0_QUOTA and score >= P0_MIN:
        p = "P0"
    elif rank < P0_QUOTA + P1_QUOTA and score >= P1_MIN:
        p = "P1"
    else:
        p = "P2"
    if p == "P0" and volume == "low":
        p = VOLUME_LOW_CEILING
    return p


def _emit(slug, keyword, intent, page_type, src, dst, fmt, scenario,
          volume, fit, base_comp, zh_hint) -> Row:
    slug = slug or _slugify(keyword)
    volume = V_OVERRIDE.get(slug, volume)
    relax = abs(min(0, COMPETITION_RELAX.get(intent, 0)))
    comp = C_OVERRIDE.get(slug) or _tier_down(base_comp, relax)
    s = _score(intent, volume, comp, fit)
    # priority 依赖全局名次，在 build_rows 排序后统一赋（此处先占位）
    return Row(slug=slug, keyword=keyword, intent=intent, page_type=page_type,
               src=src, dst=dst, fmt=fmt, scenario=scenario,
               volume_tier=volume, competition_tier=comp, fit=fit,
               score=round(s, 3), priority="", zh_hint=zh_hint)


def build_rows(only_formats=None, only_pairs=None, only_scenarios=None) -> list:
    rows: list = []

    pairs = [p for p in LANG_PAIRS
             if not only_pairs or f"{p[0]}-{p[1]}" in only_pairs]
    fmts = [f for f in FORMATS if not only_formats or f[0] in only_formats]
    scen = [s for s in SCENARIOS if not only_scenarios or s[0] in only_scenarios]

    for src, dst, _pvol, pcomp in pairs:
        sname, dname = LANG_NAME[src], LANG_NAME[dst]
        pair_zh = f"{sname}→{dname}"

        # --- L1 交易型 / L2 格式保真型：格式 × 语言对 ---
        for code, qname, short, fit, fvol, restore in fmts:
            vol = _tier_min(fvol, _pvol)

            kw1 = f"translate {qname} from {sname} to {dname}"
            rows.append(_emit(
                f"translate-{code}-{src}-to-{dst}", kw1, "L1", "tool",
                src, dst, code, "", vol, fit, pcomp, f"{pair_zh} · {short}"))

            kw2 = f"{kw1} {MOD_L2}"
            rows.append(_emit(
                f"translate-{code}-{src}-to-{dst}-preserving-formatting", kw2, "L2",
                "tool", src, dst, code, "", vol, fit, pcomp,
                f"{pair_zh} · {short} · 保格式（最强战场）"))

            # L4 只对还原文件模式的格式生成（纯文案模式没有"版式"可谈）
            if restore:
                kw4 = f"{MOD_L4} translate {qname} from {sname} to {dname} without losing layout"
                rows.append(_emit(
                    f"how-to-translate-{code}-{src}-to-{dst}-without-losing-layout",
                    kw4, "L4", "guide", src, dst, code, "",
                    vol, fit, pcomp, f"{pair_zh} · {short} · 教程"))

        # --- L3 场景型：场景 × 语言对 ---
        for code, phrase, zh, fit, svol in scen:
            kw3 = f"{phrase} translation {sname} to {dname}"
            rows.append(_emit(
                f"{code}-translation-{src}-to-{dst}", kw3, "L3", "scenario",
                src, dst, "", code, _tier_min(svol, _pvol), fit, pcomp,
                f"{pair_zh} · {zh}"))

    # 去重（同 slug 保留高分）
    best: dict = {}
    for r in rows:
        if r.slug not in best or r.score > best[r.slug].score:
            best[r.slug] = r
    out = sorted(best.values(), key=lambda r: (-r.score, r.slug))
    for i, r in enumerate(out):
        r.priority = _priority(r.score, r.volume_tier, i)
    return out


def write_csv(rows: list, path: str) -> None:
    os.makedirs(os.path.dirname(os.path.abspath(path)), exist_ok=True)
    cols = [f.name for f in fields(Row)]
    with open(path, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=cols)
        w.writeheader()
        for r in rows:
            w.writerow({c: getattr(r, c) for c in cols})


def render_md(rows: list, top: int) -> str:
    total = len(rows)
    p0 = sum(1 for r in rows if r.priority == "P0")
    p1 = sum(1 for r in rows if r.priority == "P1")
    p2 = sum(1 for r in rows if r.priority == "P2")
    by_intent = {}
    for r in rows:
        by_intent.setdefault(r.intent, 0)
        by_intent[r.intent] += 1

    L = [
        "# 关键词矩阵报告",
        "",
        f"- 总候选：**{total}**",
        f"- 分档：**P0 {p0}** / P1 {p1} / P2 {p2}",
        "- 意图分布：" + "、".join(
            f"{k} {v}" for k, v in sorted(by_intent.items())),
        "",
        f"## Top {min(top, total)}（按分数降序）",
        "",
        "| # | 优先级 | 分数 | 意图 | 形态 | 关键词 | 说明 |",
        "|---|---|---|---|---|---|---|",
    ]
    for i, r in enumerate(rows[:top], 1):
        L.append(f"| {i} | **{r.priority}** | {r.score} | {r.intent} | {r.page_type} "
                 f"| `{r.keyword}` | {r.zh_hint} |")
    L += [
        "",
        "> volume / competition 为先验粗估档。上线 4~8 周后用 Search Console 真实数据",
        "> 回填 `keyword_matrix.py` 顶部的 `V_OVERRIDE` / `C_OVERRIDE` 再重跑。",
    ]
    return "\n".join(L)


# =============================================================================
# 自证
# =============================================================================

def selftest() -> int:
    """内置自证：跑得通不等于跑得对，这些断言是防止「静默产出垃圾词表」"""
    rows = build_rows()
    fail = []

    def ck(cond, msg):
        if not cond:
            fail.append(msg)

    ck(len(rows) > 200, f"候选词太少（{len(rows)}），组合逻辑可能坏了")

    slugs = [r.slug for r in rows]
    ck(len(slugs) == len(set(slugs)), "slug 有重复")

    ck(all(re.fullmatch(r"[a-z0-9-]+", s) for s in slugs), "slug 含非法字符")

    # 单调性：L2（保格式）应当普遍优于同格式同语言对的 L1
    l1 = {(r.fmt, r.src, r.dst): r.score for r in rows if r.intent == "L1"}
    l2 = {(r.fmt, r.src, r.dst): r.score for r in rows if r.intent == "L2"}
    worse = [k for k in l1 if k in l2 and l2[k] <= l1[k]]
    ck(not worse, f"L2 保格式词未优于 L1（{len(worse)} 例，如 {worse[:3]}）——竞争修正失效")

    # 每个意图层都必须有产出（哪层空了说明组合逻辑漏了）
    for it in ("L1", "L2", "L3", "L4"):
        n = sum(1 for r in rows if r.intent == it)
        ck(n > 0, f"意图层 {it} 零产出")

    # 三种页面形态都要有（否则 build_seo_pages 会缺模板分支）
    for pt in ("tool", "scenario", "guide"):
        n = sum(1 for r in rows if r.page_type == pt)
        ck(n > 0, f"页面形态 {pt} 零产出")

    # 分数必须是正有限数
    ck(all(0 < r.score < 1000 for r in rows), "存在异常分数")

    # P0 体量必须落在合理区间：太多说明封顶失效，太少说明组合或阈值被改坏
    n_p0 = sum(1 for r in rows if r.priority == "P0")
    lo, hi = EXPECT_P0_RANGE
    ck(lo <= n_p0 <= hi,
       f"P0 数量 {n_p0} 越界（期望 {lo}~{hi}）——评分模型或 low-volume 封顶被改坏")

    # low-volume 封顶必须真的生效（否则首批全是零流量页）
    ck(all(r.priority != "P0" for r in rows if r.volume_tier == "low"),
       "存在 volume=low 却仍评为 P0 的词，封顶失效")

    # ★★ 防止「低竞争低量词霸榜」——这条断言是本脚本最重要的一条。
    #   实测事故：早期版本 volume 只由格式决定、且档位线性（3/2/1），
    #   结果 Top 12 全是 German→Chinese / English→Arabic 这类几乎没人搜的词
    #   （分数 27），而 zh→en 只有 6.75 排在后面。**脚本不报任何错，
    #   CSV 也正常生成**——只有人把表格打开看一眼才会发现。
    #   所以必须有一条断言能被它打红。
    HEAD_PAIRS = {("zh", "en"), ("en", "zh"), ("en", "es")}
    n_head = sum(1 for r in rows[:20] if (r.src, r.dst) in HEAD_PAIRS)
    ck(n_head >= 3,
       f"Top20 里头部语言对只有 {n_head} 个（期望 ≥3）——低竞争低量词霸榜，"
       f"检查 volume 联合口径（_tier_min）与 VOLUME_TIER 权重")

    # 首批 P0 里必须三种页面形态齐全（否则 build_seo_pages 只跑到一种模板）
    p0_rows = [r for r in rows if r.priority == "P0"]
    for pt in ("tool", "scenario"):
        ck(sum(1 for r in p0_rows if r.page_type == pt) > 0,
           f"P0 里没有 {pt} 形态的页面")

    # 关键词不得出现中文（海外站）
    bad = [r.keyword for r in rows if re.search(r"[一-鿿]", r.keyword)]
    ck(not bad, f"关键词混入中文（{bad[:3]}）")

    if fail:
        print("SELFTEST FAIL")
        for m in fail:
            print("  ✖ " + m)
        return 1
    print(f"SELFTEST PASS  ({len(rows)} 候选词 / P0 "
          f"{sum(1 for r in rows if r.priority == 'P0')} / "
          f"P1 {sum(1 for r in rows if r.priority == 'P1')} / "
          f"P2 {sum(1 for r in rows if r.priority == 'P2')})")
    return 0


# =============================================================================
# CLI
# =============================================================================

def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="能言 SEO 长尾关键词矩阵生成器")
    ap.add_argument("--out", default=DEFAULT_OUT, help="CSV 输出路径")
    ap.add_argument("--md", dest="md", default=None,
                    help="同时输出 Markdown 报告路径（默认与 CSV 同名 .md）")
    ap.add_argument("--top", type=int, default=25, help="报告里展示前 N 条")
    ap.add_argument("--limit", type=int, default=0, help="只保留前 N 条写 CSV（0=全部）")
    ap.add_argument("--only-p0", action="store_true", help="只输出 P0")
    ap.add_argument("--only-p1", action="store_true", help="只输出 P0+P1")
    ap.add_argument("--formats", default="", help="限定格式，逗号分隔，如 pdf,docx")
    ap.add_argument("--pairs", default="", help="限定语言对，逗号分隔，如 zh-en,en-zh")
    ap.add_argument("--scenarios", default="", help="限定场景，逗号分隔")
    ap.add_argument("--quiet", action="store_true")
    ap.add_argument("--selftest", action="store_true", help="跑内置自证后退出")
    a = ap.parse_args(argv)

    if a.selftest:
        return selftest()

    only_formats = [x for x in a.formats.split(",") if x] or None
    only_pairs = set(x for x in a.pairs.split(",") if x) or None
    only_scen = [x for x in a.scenarios.split(",") if x] or None

    rows = build_rows(only_formats, only_pairs, only_scen)
    if a.only_p0:
        rows = [r for r in rows if r.priority == "P0"]
    elif a.only_p1:
        rows = [r for r in rows if r.priority in ("P0", "P1")]
    if a.limit:
        rows = rows[:a.limit]

    write_csv(rows, a.out)

    md_path = a.md or (os.path.splitext(a.out)[0] + ".md")
    with open(md_path, "w", encoding="utf-8") as f:
        f.write(render_md(rows, a.top))

    if not a.quiet:
        p0 = sum(1 for r in rows if r.priority == "P0")
        p1 = sum(1 for r in rows if r.priority == "P1")
        p2 = sum(1 for r in rows if r.priority == "P2")
        print(f"关键词矩阵：{len(rows)} 条  (P0 {p0} / P1 {p1} / P2 {p2})")
        print(f"  CSV  → {a.out}")
        print(f"  报告 → {md_path}")
        print()
        print(render_md(rows, min(a.top, 12)).split("## Top")[1].split("\n", 1)[1])
    return 0


if __name__ == "__main__":
    sys.exit(main())
