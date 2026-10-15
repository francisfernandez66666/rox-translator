# ============================================================================
# scripts/xbot/storycheck.py — 反口号判据（2026-10-13）
#
# ★ 为什么要有这个模块：
#   `prompts.md` 里写了「故事五要素」和四段骨架，但那些是给人看的判据。
#   实测发现：**照着 prompts.md 写的样例自己就超了字数（338/280）**，
#   说明「文档写了 ≠ 会遵守」—— 判据必须能跑。
#
#   本模块把四条查得出的判据变成硬闸门：
#   ① 有没有具体数字
#   ② 产品名是不是只出现在最后一段
#   ③ 反转段是不是「动作」而不是形容词
#   ④ 最后一段有没有回扣开头（出现开头的关键词）
#
# 查不出的（仍需人工/AI 自检）：故事是否动人、场景是否真实。
# ============================================================================
from __future__ import annotations

import re
from dataclasses import dataclass, field
from pathlib import Path

BASE = Path(__file__).resolve().parent

# 品牌名与产品相关的说法（判定「产品出现」用）
BRAND_RE = re.compile(r"能言|LangCross|langcross")

# ★ URL 不算「产品出现」——但 BRAND_RE 会把 langcross.lexicorn.cn 匹配上，
#   导致「产品句 + 链接」被数成两处，把合格样例判成"产品提前出现"。
#   实测踩过：合格故事段 3、段 4（URL）都被记成产品出现位置。
#   所以判定位置时先剥掉 URL 段。
URL_RE = re.compile(r"https?://\S+")

# ★ 形容词堆砌：这些是「说了等于没说」的词，出现即判不合格
ADJ_BS_RE = re.compile(
    r"原版式直出|专业高效|精准快速|领先|赋能|一站式|全链路|闭环|抓手|降本增效|提效")

# ★ 具体动作：能兑现的承诺，反转段里应该出现这类词
ACTION_RE = re.compile(
    r"当场|当场翻|重翻|对一遍|核对|锁死|拦|改一遍|再看一眼|一稿|当场出|当场给")

# 具体数字：页数/天数/条数/次数/金额/百分比
# ★ 数字判定复用 platforms._has_number（中文数量词也算，如「三种写法」），
#   不能自己写 / \d/ ——两个模块口径不一致会造成同文不同判
#   （实测踩过：「三种写法」在 IG 闸门算有数字、在本模块算没数字）。


@dataclass
class StoryCheck:
    ok: bool = True
    digits: int = 0
    brand_positions: list[int] = field(default_factory=list)   # 出现在第几段
    callback_para: int = -1     # 正文里第一个回扣开头的段号（-1=没回扣）
    reasons: list[str] = field(default_factory=list)

    def fail(self, msg: str) -> None:
        self.ok = False
        self.reasons.append(msg)


def paragraphs(text: str) -> list[str]:
    return [p.strip() for p in re.split(r"\n{2,}", text.strip()) if p.strip()]


def check(text: str, *, angle: str = "") -> StoryCheck:
    """跑反口号判据。任一不过 ⇒ ok=False。"""
    r = StoryCheck()
    paras = paragraphs(text)
    if len(paras) < 3:
        r.fail(f"只有 {len(paras)} 段，故事骨架至少要有暴雷/坐实/反转/点题四段")
        return r

    # ① 具体数字：必须出现在「反转段之前」（末段是产品 + URL，不指望它）
    body_paras = paras[:-1]
    import platforms
    r.digits = sum(1 for p in body_paras if platforms._has_number(p))
    if r.digits == 0:
        r.fail("通篇没有具体数字（页数/天数/条数/次数/金额）—— 这是口号不是故事")

    # ② 产品只能出现在最后一段
    #   ★ 末段的定义不是「最后一个 \n\n 分段」，而是**结尾块**——
    #     产品句和 URL 通常被空行隔开（实测样例就是那样），分成两段就会被
    #     判成「产品出现在第 3、4 段」，把合格样例判不合格（自证抓到过）。
    #   做法：只取含产品的段落序号，要求它落在「尾部一格」内，
    #     并且尾部连续几段（产品/链接）合并算同一处。
    for i, p in enumerate(paras):
        if _mentions_brand(p):
            r.brand_positions.append(i)
    if not r.brand_positions:
        r.fail("最后一段没有产品名与链接")
    elif r.brand_positions[0] < len(paras) - 2:
        r.fail(f"产品名出现在第 {r.brand_positions} 段，应只在结尾出现")

    # ③ 反转段（结尾块）必须是动作，不是形容词
    last = _tail_block(paras)
    if ADJ_BS_RE.search(last):
        r.fail(f"反转段是形容词堆砌（{ADJ_BS_RE.search(last).group()}），必须是具体动作")
    if not ACTION_RE.search(last):
        r.fail("反转段没有可执行的动作（应出现「当场/ 重翻 / 对一遍 / 锁死 / 拦」这类词）")

    # ④ 点题要回扣开头：正文里**任意一段**应与首段有共同的具体名词。
    #   ★ 为什么不是「只看最后一句」：合格样例里回扣发生在第二段
    #     （「目录…正文…」都出现了），最后一句「问题不是翻译质量」是升华不是回扣。
    #     只看最后一句会把合格样例判死（实测踩过）。
    #   ★ 为什么用 2 字滑窗：正则 [一-鿿]{2,4} 非贪婪取最长块，
    #     「这版目录」会整块拿去匹配，永远匹配不上别处的「目录」（实测踩过）。
    brand_i = next((i for i, p in enumerate(paras) if _mentions_brand(p)), None)
    body = paras[:brand_i] if brand_i is not None else paras
    if len(body) >= 3:
        head = body[0]
        grams = {head[i:i + 2] for i in range(len(head) - 1)
                 if re.fullmatch(r"[一-鿿]{2}", head[i:i + 2])}
        # 排除首段自身（首段当然"回扣"了它自己）
        hit = next((i for i, p in enumerate(body[1:], 1)
                    if any(g in p for g in grams)), None)
        if hit is None:
            r.fail("点题没有回扣开头（正文后续各段与首段无共同的具体名词）"
                   "—— 像两条独立的推文")
        else:
            r.callback_para = hit
    return r


def _mentions_brand(p: str) -> bool:
    """这段是否**在正文里**提到产品（纯 URL 段不算）。

    ★ 例外：「列举客户写错品牌名」是故事内容本身，不是打广告。
      「LangCross。Lang Cross。朗克罗斯。」这种把品牌名当**被纠正的对象**
      逐个列出来的句子必须放行，否则术语类故事根本写不出来（实测踩过）。
    ★ 区分方法不是"去掉品牌名还剩多少字"——产品句「能言 LangCross：页码从原文走」
      去掉品牌名后剩得也不多，会被误判成列举（实测踩过）。
      真正的差别是**列举句以品牌名结尾、不带冒号和功能说明**：
      产品句一定写成「能言 X：<做什么>」，列举句写成「<写错的写法>」。
    """
    bare = URL_RE.sub("", p)
    if not BRAND_RE.search(bare):
        return False
    # 产品句形态：品牌名后面紧跟冒号/破折号，接着是功能说明
    if re.search(r"(能言|LangCross|langcross)\s*[：:—]\s*\S", bare):
        return True
    # 其余情况：若整段去掉品牌名与标点后几乎不剩实词 ⇒ 是列举写法
    rest = re.sub(r"[\s，。、：；！？「」『』…—\-\.a-zA-Z]", "", BRAND_RE.sub("", bare))
    return len(rest) >= 4


def _tail_block(paras: list[str]) -> str:
    """结尾块 = 含产品名的那一段及其后的所有段（产品句 + URL 算同一处）。"""
    for i, p in enumerate(paras):
        if _mentions_brand(p):
            return "\n".join(paras[i:])
    return paras[-1]


def self_test() -> int:
    good = (
        "周三下午 4 点，客户发来邮件：「这版目录和正文对不上，我们内部评审要用。」\n\n"
        "附件 87 页 Word。译文没问题，但目录里的页码是从原文件复制过来的，正文重排后全错位了。\n\n"
        "问题不是翻译质量。是交回来的是另一个文件。\n\n"
        "能言 LangCross：当场重翻一遍，页码从原文走。\n\n"
        "https://langcross.lexicorn.cn")
    bad_slogan = (
        "外包译一份要等 3 天，很多人都在吐槽。\n\n"
        "效率低、成本高，交付周期长，是行业的老问题。\n\n"
        "我们致力于用 AI 解决这个痛点。\n\n"
        "能言 LangCross：Word / PPT / Excel / PDF 原版式直出，交付质量更高。\n\n"
        "https://langcross.lexicorn.cn")
    # URL 排在产品句之前（空行隔开）—— 产品与链接算**同一个结尾块**，必须放行
    url_first = (
        "周三下午 4 点，客户发来邮件：「这版目录和正文对不上，我们内部评审要用。」\n\n"
        "附件 87 页 Word。译文没问题，但目录里的页码是从原文件复制过来的。\n\n"
        "问题不是翻译质量。是交回来的是另一个文件。\n\n"
        "https://langcross.lexicorn.cn\n\n"
        "能言 LangCross：当场重翻一遍，页码从原文走。")
    # ★ 这条是本轮补的：判据调宽之后必须证明没调成"什么都放行"。
    #   ⚠ 不要用 good.replace(...) 构造——拼接出来的字符串里换行/引号
    #   极易对不上，replace 静默不生效（实测踩过：自证里样本没变，判据被冤枉）。
    #   反面样本一律写成完整字面量。
    #   ⚠ 判据 ④ 的语义是「正文后续段与首段有共同具体名词」。
    #     所以"不回扣"的样本必须**后一段也不许复述首段的词**，
    #     只把点题句写抽象是测不出来的（实测踩过：细节段的「目录」把闸门放过了）。
    bad_no_callback = (
        "周三下午 4 点，客户发来邮件：「这版目录和正文对不上，我们内部评审要用。」\n\n"
        "附件 87 页 Word。译文没问题，页码是复制过来的，排版全乱了。\n\n"
        "问题出在流程上。需要一套新的方法来解决它。\n\n"
        "能言 LangCross：当场重翻一遍，页码从原文走。\n\n"
        "https://langcross.lexicorn.cn")
    bad_mid_brand = (
        "周三下午 4 点，客户发来邮件：「这版目录和正文对不上。」\n\n"
        "附件 87 页 Word。译文没问题，但目录里的页码是复制过来的。\n\n"
        "能言 LangCross 这套流程当时也没拦住。问题不是翻译质量。\n\n"
        "当场重翻一遍，页码从原文走。\n\n"
        "https://langcross.lexicorn.cn")

    bad = 0
    for name, text, want_ok in [("合格故事", good, True),
                                ("URL 在产品句前(合法)", url_first, True),
                                ("典型口号", bad_slogan, False),
                                ("点题不回扣开头", bad_no_callback, False),
                                ("产品夹在正文里", bad_mid_brand, False)]:
        r = check(text)
        ok = r.ok == want_ok
        print(f"{'OK ' if ok else 'FAIL'} {name}：ok={r.ok}（期望 {want_ok}）"
              f"{'　reason=' + r.reasons[0] if r.reasons else ''}")
        if not ok:
            bad += 1
    # ★ 数字口径必须与 platforms 一致（中文数量词也算）——
    #   「三种写法」在 IG 闸门算有数字，这里也得算有，否则同一段文字两套结论。
    #   同时验证「列举错误写法」能放行（否则术语类故事根本写不出来）。
    cn_num = (
        "周三下午 4 点，客户的德文手册里有三种写法。\n\n"
        "朗克罗斯。朗克罗思。Lang Cross。\n\n"
        "三种里一种是翻译软件自动转的，一种是你手打的。\n\n"
        "能言 LangCross：品牌名进术语库，当场锁死。"
        "\n\nhttps://langcross.lexicorn.cn")
    rn = check(cn_num)
    print(f"{'OK ' if rn.ok else 'FAIL'} 中文数量词/ 列举错误写法：{rn.reasons or '通过'}")
    if not rn.ok:
        bad += 1

    # 列举例外的**反向**自证：一整段都在自夸产品，必须仍被拦住
    brand_para = (
        "周三下午 4 点，客户发来邮件：「这版目录和正文对不上。」\n\n"
        "附件 87 页 Word。译文没问题，但目录里的页码是复制过来的。\n\n"
        "能言 LangCross 用的就是这个。\n\n"
        "能言 LangCross：当场重翻一遍，页码从原文走。"
        "\n\nhttps://langcross.lexicorn.cn")
    rb = check(brand_para)
    blocked = any("结尾" in x for x in rb.reasons)
    print(f"{'OK ' if blocked else 'FAIL'} 正文植入产品仍被拦：{rb.reasons}")
    if not blocked:
        bad += 1

    print("SELF-TEST", "PASS" if bad == 0 else f"FAIL({bad})")
    return bad


if __name__ == "__main__":
    raise SystemExit(1 if self_test() else 0)