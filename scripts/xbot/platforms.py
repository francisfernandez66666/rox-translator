# ============================================================================
# scripts/xbot/platforms.py — 多平台规格（2026-10-13 核实）
#
# ★ 为什么必须分平台算字数：
#   X 的加权口径（URL 恒 23、CJK 算 2）与 Instagram/Facebook 的常规字符口径
#   完全不同。用同一个 len() 判，会在 X 上漏放超限稿、在 IG 上误拦合格稿。
#
# ★ 为什么不做发布 API：
#   X —— 自动发被判 bot 的代价是账号级的，人工发边际成本仅 5 分钟。
#   Instagram —— API 只收 JPEG，PNG/HEIC 被拒。
#   TikTok —— 图片帖只收「已验证域名的 URL」而非二进制；
#             且未通过审核的 App 发出的帖强制私密，无参数可覆盖。
#   ⇒ 全平台走「AI 备料 + 人工发」，本文件只管算长度和出图。
#
# 规格来源与核实日期见同目录 平台规格_多平台_20261013.md
# ============================================================================
from __future__ import annotations

import re
from dataclasses import dataclass

URL_RE = re.compile(r"https?://\S+")


# ── 字数口径 ──────────────────────────────────────────────────────────────
def weighted_x(text: str) -> int:
    """X 加权口径：URL 整体恒 23，其余 CJK/全角算 2、其余算 1。

    ★ 不能用 len()：200 个汉字的推文 len() 是 200（看着没超），
      但 X 算它是 400，直接被截断。
    """
    n = 0
    idx = 0
    for m in URL_RE.finditer(text):
        n += _plain(text[idx:m.start()])
        n += 23
        idx = m.end()
    return n + _plain(text[idx:])


def plain(text: str) -> int:
    """常规字符口径：URL 按实际长度算（IG/FB/TikTok 都这样）。"""
    return len(text)


def _plain_cjk(s: str) -> int:
    total = 0
    for ch in s:
        cp = ord(ch)
        if (0x2E80 <= cp <= 0x9FFF or 0xAC00 <= cp <= 0xD7AF or 0xF900 <= cp <= 0xFAFF
                or 0xFF00 <= cp <= 0xFF60 or 0xFFE0 <= cp <= 0xFFE6 or 0x3000 <= cp <= 0x303F):
            total += 2
        else:
            total += 1
    return total


def _plain(s: str) -> int:
    return _plain_cjk(s)


# ── 平台定义 ──────────────────────────────────────────────────────────────
@dataclass
class Platform:
    key: str
    name: str
    limit: int
    counter: object            # 计数函数：weighted_x 或 plain
    fold_at: int | None        # 首屏折叠位置；None = 不折叠
    image: tuple[int, int]     # 配图尺寸 (w, h)
    fmt: str                  # 首选格式
    aspect: str
    # 首屏折叠位置之前必须放完的东西（钩子）
    lead_must_fit: bool = False
    note: str = ""
    # 平台特有的打法提示，进备料页让人审时看
    tactic: str = ""


PLATFORMS: dict[str, Platform] = {
    "x": Platform(
        key="x", name="X", limit=280, counter=weighted_x, fold_at=None,
        image=(1200, 675), fmt="PNG", aspect="16:9",
        note="URL 恒算 23 字符，所以 UTM 参数再长也不占额度",
        tactic="短句打头。首句必须是对方的损失，不是我的产品。",
    ),
    "instagram": Platform(
        key="instagram", name="Instagram", limit=2200, counter=plain, fold_at=125,
        image=(1080, 1350), fmt="JPEG", aspect="4:5",
        note="★ 只有前 ~125 字符在首屏可见，后面全是折叠内容",
        tactic="★ 钩子必须写进前 125 字符里，展开没人看就是白写。",
        lead_must_fit=True,
    ),
    "tiktok": Platform(
        key="tiktok", name="TikTok", limit=2200, counter=plain, fold_at=None,
        image=(1080, 1920), fmt="JPEG", aspect="9:16",
        note="★ 流传的 4000 字符是原生 App 上限，官方 Direct Post API 记的是 2200",
        tactic="文案在视频下方，第一句决定要不要看下去。可加 3~5 个精准话题标签。",
    ),
    "facebook": Platform(
        key="facebook", name="Facebook", limit=63206, counter=plain, fold_at=477,
        image=(1200, 630), fmt="PNG", aspect="1.91:1",
        note="上限极大 ⇒ 它适合长文，不是短句打法",
        tactic="★ 与 X 完全相反：可以写长，前 477 字符内要把钩子和结论说完。",
        lead_must_fit=True,
    ),
}

ORDER = ["x", "instagram", "tiktok", "facebook"]


def measure(platform: str, text: str) -> int:
    p = PLATFORMS.get(platform)
    if not p:
        raise KeyError(f"未知平台 {platform}，可选：{list(PLATFORMS)}")
    return p.counter(text)


def check(platform: str, text: str) -> list[str]:
    """返回问题列表，空列表表示通过。"""
    p = PLATFORMS.get(platform)
    if not p:
        return [f"未知平台 {platform}"]
    out: list[str] = []
    n = p.counter(text)
    if n > p.limit:
        out.append(f"{p.name} 长度 {n} > 上限 {p.limit}")
    # ★ 折叠位检查：IG 前 125 字、FB 前 477 字之外的内容等于没写。
    #   但只在「文本确实长到会折叠」时才查——短文案没有折叠内容，
    #   要求它「钩子前置」是闸门误伤（2026-10-13 自证当场抓到过）。
    #
    # ★★ 判定口径改过一次：原来靠「痛点信号词白名单」，
    #   结果把「客户的德文手册里，同一个品牌名有三种写法」这种明显有钩子的
    #   首句判成没钩子（实测当场抓到）。**闸门误伤比漏放更坏**——
    #   AI 会为了过闸把钩子改成信号词表里的陈词滥调。
    #   现在只查一个客观特征：**折叠位以内有没有具体数字**。
    #   数字是可信度的唯一来源（这条与 prompts.md 的第3 条硬约束同源），
    #   不依赖任何主观词表，也不会误伤"三种写法/87 页/第 3 页"这类表达。
    if p.fold_at and p.lead_must_fit and n > p.fold_at:
        head = text[:p.fold_at]
        if not _has_number(head):
            out.append(f"⚠ 正文 {n} 字符会折叠，但前 {p.fold_at} 字符里没有具体数字"
                       f"（钩子写得太软，展开就看不到了）")
    return out


# 中文数字也算数字：「三种写法」「八十七页」和「87 页」一样有可信度。
# ⚠ 但不能逐字扫——「这是一段铺垫」「一下」「一起」里的「一」是语气词不是数量，
#   逐字扫会把软钩子误判成硬钩子（实测踩过：闸门因此对"先说结论"这类开头
#   完全失效，等于没有闸门）。
#   只认**成量的**中文数字：一二三四五六七八九十几百千万 + 后接量词或"分/倍"。
#   "三种/两次/八十七页/三天"过；"一段/一下/一起"不过。
_CN_DIGIT = "一二三四五六七八九"
_CN_UNIT = "十百千万亿"
# 计量词分两类，只收**数量词**：
#   数量词（要）：三种/两版/三批/五章 —— 数字改变信息量；
#   位置词（不要）：一段/一栏/一行/一句 —— 只是说位置，说了等于没说，
#     收进来会让「这是一段铺垫」这种纯口语开头被判成硬钩子（实测踩过两次）。
_CN_MEASURE = ("倍分秒天次页元块条张份个台人步轮点版种套批章节列组"   # 数量
               )
_CN_QTY = "两"


def _has_number(s: str) -> bool:
    """文本里有没有**成量的**具体数字（阿拉伯数字或中文数量词）。

    之所以不逐字扫中文数字：口语里「一」极常见（一段/一下/一起/统一），
    全扫会把软钩子判成硬钩子，闸门直接失效（实测踩过）。
    """
    if any(c.isdigit() for c in s):
        return True
    for i, c in enumerate(s):
        if c not in _CN_DIGIT:
            continue
        # 「三天」「两次」「八十七」：数字后面跟量词/单位 ⇒ 成量
        nxt = s[i + 1] if i + 1 < len(s) else ""
        if nxt in _CN_UNIT or nxt in _CN_MEASURE:
            return True
        # 「三种」「两次」里的量词在数字**前面**？不，汉语是数+量 ⇒ 已在上面。
        # 「一百」这种裸单位已在上面；「十七」是「十」后接「七」，也已在上面。
    if any(c in _CN_QTY for c in s):          # 「两页」「两次」
        i = s.index(_CN_QTY[0])
        nxt = s[i + 1] if i + 1 < len(s) else ""
        if nxt in _CN_MEASURE or nxt in _CN_UNIT:
            return True
    return False


if __name__ == "__main__":  # 自证用例，跑 `python3 platforms.py` 必须全绿
    cases = [
        # (平台, 文本, 期望长度)
        ("x", "中文", 4),
        ("x", "abc", 3),
        ("x", "你好 https://a.cn", 4 + 1 + 23),
        ("x", "https://a.cn", 23),
        ("x", "Word / PPT", 10),
        # ★ 跨平台分档自证：同一条文本两个口径不同
        ("instagram", "中文", 2),        # IG 走常规字符：len("中文")=2
        ("x", "中文", 4),                # X 走加权：4
        ("x", "https://langcross.lexicorn.cn/?utm_source=x&utm_medium=social&utm_campaign=bot_d1_s1", 23),
    ]
    bad = 0
    for plat, text, want in cases:
        got = measure(plat, text)
        flag = "OK " if got == want else "FAIL"
        if got != want:
            bad += 1
        print(f"{flag} {plat}: measure({text[:32]!r}) = {got}（期望 {want}）")

    # 跨平台口径必须真的不同——这是本模块存在的理由，自证一下
    t = "80 页 Word 交回来，目录全要手动重排。"
    xa, ig = measure("x", t), measure("instagram", t)
    print(f"{'OK ' if xa != ig else 'FAIL'} 口径分档生效：同文本 X={xa} / IG={ig}")
    if xa == ig:
        bad += 1

    # 超限拦截
    long_x = "中" * 141          # 加权 282 > 280
    r1 = check("x", long_x)
    print(f"{'OK ' if r1 else 'FAIL'} X 超限被拦：{r1[0] if r1 else '（没拦下）'}")
    if not r1:
        bad += 1
    r2 = check("instagram", "短文案，不超限。")
    print(f"{'OK ' if not r2 else 'FAIL'} IG 短稿不误伤（无折叠内容，不该要求钩子前置）：{r2}")
    if r2:
        bad += 1

    # 折叠位钩子检查：钩子写后面 ⇒ 应给出提示
    tail_hook = "先说结论。" + "这是一段铺垫。" * 20 + "80 页 Word 交回来，目录全要手动重排。"
    r3 = check("instagram", tail_hook)
    print(f"{'OK ' if r3 else 'FAIL'} IG 钩子靠后被提示：{r3[0] if r3 else '（没提示）'}")
    if not r3:
        bad += 1

    # ★★ 折叠位判定必须**双向**自证：好钩子不许误伤、软钩子必须拦。
    #   只测「该拦的拦住了」是不够的——闸门误伤比漏放更坏（AI 会为过闸写更差的文案）。
    #   这三条正是当初把信号词白名单换成数字判定的依据。
    #   ⚠ 样本必须**真的超过折叠位**（IG 125 字符）否则检查根本不会触发，
    #     实测踩过：样本 76 字符，期望被拦却没被拦，一度以为判定写错了。
    soft_lead = ("我们致力于为跨境企业提供更高效的本地化解决方案，"
                 "帮助企业节省时间与成本，让团队专注核心业务。")
    fold_cases = [
        # (文本, 是否应因折叠位被提示)
        (soft_lead + "欢迎垂询。" * 30, True),
        ("客户的德文手册里，同一个品牌名有三种写法。" + "补充说明。" * 40, False),
        ("附件 87 页 Word，目录页码全错位。" + "补充说明。" * 40, False),
    ]
    for text, should in fold_cases:
        n = measure("instagram", text)
        probs = [p for p in check("instagram", text) if "折叠" in p]
        flagged = bool(probs)
        ok = flagged == should and n > PLATFORMS["instagram"].fold_at
        if not ok:
            bad += 1
        print(f"{'OK ' if ok else 'FAIL'} IG 折叠位判定：{text[:20]!r}… "
              f"{n} 字符 提示={flagged}（期望 {should}）")

    # ★★ _has_number 必须双向自证：口语里的「一」不算数量，
    #   真正的数量表达（中文/阿拉伯）都算。这一段是防"有人把量词表改回去"。
    num_cases = [
        ("先说结论。这是一段铺垫。", False), ("统一一下格式", False),
        ("一起看看", False), ("一栏", False), ("一行字", False),
        ("一句", False), ("统一术语", False), ("第一时间", False),
        ("三种写法", True), ("87 页", True), ("八十七页", True),
        ("两天", True), ("两次", True), ("三版", True), ("一批", True),
        ("两个文件", True), ("第五章", True), ("1/8 成本", True),
        ("三倍", True), ("第一次", True),
    ]
    for t, want in num_cases:
        got = _has_number(t)
        if got != want:
            bad += 1
            print(f"FAIL _has_number({t!r}) = {got}（期望 {want}）")
    print(f"{'OK ' if bad == 0 else 'FAIL'} _has_number 数量/口语双向 {len(num_cases)} 例")

    print("SELF-TEST", "PASS" if bad == 0 else f"FAIL({bad})")
    raise SystemExit(1 if bad else 0)