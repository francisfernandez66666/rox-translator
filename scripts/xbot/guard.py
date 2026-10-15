# ============================================================================
# scripts/xbot/guard.py — 三道闸门（2026-10-13）
# 合规 / 加权字数 / 重复度。任一不过 ⇒ 该条不发。
#
# ★ 设计前提：校验必须由代码算，不能信 AI 自评。pending.json 里的 checks 字段
#   是本模块写的，不是 AI 说的。
# ============================================================================
from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from difflib import SequenceMatcher
from pathlib import Path

BASE = Path(__file__).resolve().parent

# ── 闸门 A｜合规禁用词（《品牌一页纸.md》红线 + 执行包反例库）────────────────
# 品牌名禁用写法｜禁用承诺｜主体口径｜禁用诱饵｜黑话
BANNED_WORDS = [
    "翻译助手", "智能翻译平台", "Lexicorn 能言",
    "100%", "百分百", "百分之百", "永不泄密", "绝对准确", "零错误", "100%准确",
    "有限公司", "我们公司",
    "限时免费", "免费领取", "免费下载",
    "赋能", "抓手", "闭环", "一站式", "全链路",
]
# 制造者视角自嗨句式：首句命中即判不合格
# ★ 必须只拦「时长型」表述，不能拦一切「做了」——
#   「做了大半年」（自嗨）要拦，「做了双语版」（在讲客户场景）不能拦。
#   闸门误伤比漏放更坏：好文案被拦掉，AI 会为了过闸去写更差的文案。
SELF_PITY_RE = re.compile(
    r"^(?:"
    r"做了.{0,3}(?:个月|年|周|天|晚上|凌晨|白天)"
    r"|我做了很久|我们做了.{0,3}(?:个月|年)"
    r"|耗时|历时|第一个产品|终于上线|我们的产品|我们致力于"
    r"|很高兴宣布|宣布|重磅|隆重"
    r")"
)
# 正文必须含的域名
REQUIRED_HOST = "langcross.lexicorn.cn"


# ── 闸门 B｜加权字数 ───────────────────────────────────────────────────────
# ★ X 的计数口径：CJK/全角算 2、其余算 1、任何 URL 恒算 23。
#   用 len() 数中文会把「200 汉字」的稿子当成「100 字符」放行，超限稿就是这么漏过去的。
_URL_RE = re.compile(r"https?://\S+")


def weighted_len(text: str) -> int:
    """按 X 加权口径计算长度。URL 整体恒 23，其余 CJK 算 2、ASCII 算 1。"""
    n = 0
    idx = 0
    for m in _URL_RE.finditer(text):
        n += _plain_len(text[idx:m.start()])
        n += 23
        idx = m.end()
    n += _plain_len(text[idx:])
    return n


def _plain_len(s: str) -> int:
    total = 0
    for ch in s:
        # CJK 统一表意文字（含扩展区）、假名、韩文、全角标点/符号 → 2
        cp = ord(ch)
        if (
            0x2E80 <= cp <= 0x9FFF
            or 0xAC00 <= cp <= 0xD7AF
            or 0xF900 <= cp <= 0xFAFF
            or 0xFF00 <= cp <= 0xFF60
            or 0xFFE0 <= cp <= 0xFFE6
            or 0x3000 <= cp <= 0x303F
        ):
            total += 2
        else:
            total += 1
    return total


# ── 闸门 C｜重复度 ─────────────────────────────────────────────────────────
FIRST_SENTENCE_SIM_MAX = 0.70   # 首句相似度上限
WHOLE_SIM_MAX = 0.85            # 全文相似度上限
LOOKBACK_DAYS = 30
SAME_TOPIC_DAYS = 60            # 同一选题 ID 天数内不复用


@dataclass
class CheckResult:
    ok: bool
    compliance: str = "pass"
    weighted_len: int = 0
    first_sim: float = 0.0     # 首句相似度，阈值 0.70
    whole_sim: float = 0.0     # 全文相似度，阈值 0.85
    max_similarity: float = 0.0 # = max(first_sim, whole_sim)，仅供报表
    reasons: list[str] = field(default_factory=list)


def _first_sentence(text: str) -> str:
    """取首句：按中文句号/问号/叹号/换行切，取最前一段非空。"""
    body = _URL_RE.sub("URL", text)
    parts = re.split(r"[。！？\n]", body)
    for p in parts:
        if p.strip():
            return p.strip()
    return body.strip()


def _sim(a: str, b: str) -> float:
    if not a or not b:
        return 0.0
    return SequenceMatcher(None, a, b).ratio()


def load_ledger(path: Path | None = None) -> list[dict]:
    """读发布台账；不存在或坏行一律跳过（台账坏掉不该让整个机器人停摆）。"""
    p = path or (BASE / "ledger.jsonl")
    if not p.exists():
        return []
    rows = []
    for line in p.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            rows.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return rows


def check(text: str, *, topic_id: str | None = None, ledger: list[dict] | None = None) -> CheckResult:
    """跑三道闸门。任一不过 ⇒ ok=False，reasons 写明原因。"""
    r = CheckResult(ok=True)
    r.weighted_len = weighted_len(text)
    low = text.lower()

    # 闸门 A：合规
    hit = [w for w in BANNED_WORDS if w.lower() in low]
    if hit:
        r.compliance = "fail"
        r.ok = False
        r.reasons.append(f"命中禁用词：{ '、'.join(hit) }")

    head = _first_sentence(text)
    if SELF_PITY_RE.match(head):
        r.compliance = "fail"
        r.ok = False
        r.reasons.append(f"首句自嗨句式：{head[:12]}")

    if REQUIRED_HOST not in text:
        r.compliance = "fail"
        r.ok = False
        r.reasons.append(f"缺少官网链接（{REQUIRED_HOST}）")

    # 闸门 B：字数
    if r.weighted_len > 280:
        r.ok = False
        r.reasons.append(f"加权字符 {r.weighted_len} > 280")

    # 闸门 C：重复
    # ★ 两个阈值各管各的，不能合并——2026-10-13 实测踩过：
    #   21 条同框架文案全文相似度天然 0.70~0.79（品牌句式 + URL 结构都一样），
    #   拿 0.70 去判全文会把 15/21 条全误拦。首句 0.70 判撞车，全文 0.85 判雷同。
    led = ledger if ledger is not None else load_ledger()
    for row in led:
        old = row.get("text", "")
        r.first_sim = max(r.first_sim, _sim(head, _first_sentence(old)))
        r.whole_sim = max(r.whole_sim, _sim(text, old))
        if topic_id and row.get("topic_id") == topic_id:
            r.ok = False
            r.reasons.append(f"选题 {topic_id} 已于 {row.get('ts', '?')} 发过（{SAME_TOPIC_DAYS} 天内不复用）")
    r.first_sim = round(r.first_sim, 3)
    r.whole_sim = round(r.whole_sim, 3)
    r.max_similarity = max(r.first_sim, r.whole_sim)

    if r.first_sim >= FIRST_SENTENCE_SIM_MAX:
        r.ok = False
        r.reasons.append(f"首句相似度 {r.first_sim:.2f} ≥ {FIRST_SENTENCE_SIM_MAX}（撞车了）")
    elif r.whole_sim >= WHOLE_SIM_MAX:
        r.ok = False
        r.reasons.append(f"全文相似度 {r.whole_sim:.2f} ≥ {WHOLE_SIM_MAX}（雷同）")

    return r


if __name__ == "__main__":  # 自证用例，跑 `python3 guard.py` 必须全绿
    cases = [
        ("中文", 4),
        ("abc", 3),
        ("你好 https://a.cn", 4 + 1 + 23),
        ("https://a.cn", 23),
        ("Word / PPT", 10),
    ]
    bad = 0
    for text, want in cases:
        got = weighted_len(text)
        flag = "OK " if got == want else "FAIL"
        if got != want:
            bad += 1
        print(f"{flag} weighted_len({text!r}) = {got}（期望 {want}）")
    # 闸门自测
    t_bad = "做了大半年，我们的第一个产品上线了 langcross.lexicorn.cn"
    t_ok = "外包译一份要等 3 天。https://langcross.lexicorn.cn"
    if check(t_bad).ok:
        print("FAIL 自嗨句未被拦下"); bad += 1
    else:
        print(f"OK  自嗨句被拦：{check(t_bad).reasons[0]}")
    if not check(t_ok).ok:
        print(f"FAIL 正常文案被误拦：{check(t_ok).reasons}"); bad += 1
    else:
        print(f"OK  正常文案通过（加权 {check(t_ok).weighted_len}）")
    if check("永久免费领取 langcross.lexicorn.cn").ok:
        print("FAIL 禁用词未被拦下"); bad += 1
    else:
        print("OK  禁用词被拦")

    # 回归 1（2026-10-13 踩过）：自嗨检测不得误伤「做了 + 非时长宾语」
    t_pain = "做了双语版，还得手工把两份文档的段落一段段对齐。\n\n能言 LangCross：原版式直出。\n\nhttps://langcross.lexicorn.cn"
    if not check(t_pain).ok:
        print(f"FAIL 「做了双语版」被误判为自嗨：{check(t_pain).reasons}"); bad += 1
    else:
        print("OK  「做了双语版」未被误伤（只有时长型才算自嗨）")
    # 同时确认时长型仍被拦
    if check("做了大半年才做出来的 langcross.lexicorn.cn").ok:
        print("FAIL 「做了大半年」未被拦"); bad += 1
    else:
        print("OK  「做了大半年」被拦")

    # 回归 2（2026-10-13 踩过）：全文相似度高 ≠ 撞车
    # 同框架两条（品牌句式与 URL 结构相同 ⇒ 全文天然 0.7~0.8），首句不同就不该拦
    a = "80 页 Word 交回来，目录和页码全要手动重排。\n\n能言 LangCross：原版式直出。\n\nhttps://langcross.lexicorn.cn"
    b = "图表标题翻译错了，整张图的意思就反了。\n\n能言 LangCross：术语硬闸。\n\nhttps://langcross.lexicorn.cn"
    rb = check(b, ledger=[{"text": a, "topic_id": "A02"}])
    if not rb.ok:
        print(f"FAIL 同框架不同首句被误拦（全文阈值用错）：{rb.reasons}"); bad += 1
    else:
        print(f"OK  同框架不同首句通过（首句 {rb.first_sim} / 全文 {rb.whole_sim}，全文不按 0.70 判）")
    # 但首句真的撞了必须拦
    c = "80 页 Word 交回来，目录和页码全要手动重排。\n\n能言 LangCross：原版式直出。\n\nhttps://langcross.lexicorn.cn"
    rc = check(c, ledger=[{"text": a, "topic_id": "A02"}])
    if rc.ok:
        print("FAIL 首句完全撞车未被拦"); bad += 1
    else:
        print(f"OK  首句撞车被拦（{rc.reasons[0]}）")

    print("SELF-TEST", "PASS" if bad == 0 else f"FAIL({bad})")
    raise SystemExit(1 if bad else 0)