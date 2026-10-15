# ============================================================================
# scripts/xbot/layouts.py — 场景化图文版式（2026-10-13）
#
# ★ 为什么需要多种版式：
#   只做「logo + slogan + 痛点主标」一种版式，读者滑三次就免疫了——
#   每天三个格子长得一模一样，看起来就像机器发的。
#   场景化图文把内容画出来（对话、前后对比、清单、账单、对话体金句），
#   比讲道理更容易被停下来。
#
# ★ 铁律不变：
#   图上的中文一律程序渲染，不走 AI 生图（字形必错、每天漂移）。
#   所有版式共用 fit_size()，缺了它中文必然溢出被裁（踩过两次）。
# ============================================================================
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import platforms  # noqa: E402

BASE = Path(__file__).resolve().parent

BG = "#000000"
FG = "#FFFFFF"
SUB = "#8B939F"
FAINT = "#6E7683"
LINE = "#2A2F3A"
RED = "#F85149"
AMBER = "#D29922"

LOGO_G = (
    '<path d="M9.5 10.5v5.2a4.3 4.3 0 0 0 8.6 0V12" stroke="{fg}" stroke-width="2.4" '
    'fill="none" stroke-linecap="round"/>'
    '<path d="M20.5 19.5v-5.2a4.3 4.3 0 0 0-8.6 0V18" stroke="{fg}" stroke-width="2.4" '
    'fill="none" stroke-linecap="round" opacity=".5"/>'
)


def _esc(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def fit_size(text: str, max_width: float, max_size: int) -> int:
    """按字宽反算字号：全角 1.0em、半角 0.55em。缺了它中文标题必被裁。"""
    em = 0.0
    for ch in text:
        cp = ord(ch)
        em += 1.0 if (0x2E80 <= cp <= 0x9FFF or 0xFF00 <= cp <= 0xFF60
                      or 0x3000 <= cp <= 0x303F) else 0.55
    return int(min(max_size, max(18, max_width / em)))


def _font(size: int, weight: int = 400, color: str = FG) -> str:
    return (f'font-family="\'PingFang SC\',\'Hiragino Sans GB\',sans-serif" '
            f'font-size="{size}" font-weight="{weight}" fill="{color}"')


def _frame(w: int, h: int, body: str) -> str:
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" '
            f'viewBox="0 0 {w} {h}">\n<rect width="{w}" height="{h}" fill="{BG}"/>\n'
            f'<rect x="1" y="1" width="{w-2}" height="{h-2}" fill="none" stroke="{LINE}" '
            f'stroke-width="2" rx="18"/>\n{body}\n</svg>')


def _logo(x: float, y: float, scale: float) -> str:
    return (f'<g transform="translate({round(x,1)},{round(y,1)}) scale({round(scale,2)})">'
            + LOGO_G.format(fg=FG) + "</g>")


# logo 图形实际占高 ≈ 30 单位（scale=1 时），用它来算需要让开多少空间
LOGO_BOX_H = 30.0

# logo path 的真实纵向范围只有 y=6.2~23.8。分镜格里logo 只是角标，
# 用真实高度算占位，否则横版 675px 高的画面区会被logo 白吃掉四成。
LOGO_TIGHT_H = 25.0


def _logo_bottom(y: float, scale: float) -> float:
    return y + LOGO_BOX_H * scale


def _footer(w: int, h: int, y_ratio: float = 0.86) -> str:
    """页脚。★ y_ratio 从 0.90 一路降到 0.86：0.90 时网址那条正好压在画布底边被裁掉。"""
    base = int(h * (0.030 if h > w else 0.038))
    x = round(w * (0.0625 if h > w else 0.05))
    y = round(h * y_ratio)
    return (
        f'<text x="{x}" y="{y}" {_font(base, 400, FG)}>能言 LangCross</text>'
        f'<text x="{x}" y="{y + int(base*1.5)}" '
        f'font-family="Menlo,monospace" font-size="{int(base*0.88)}" fill="{FAINT}" '
        f'>langcross.lexicorn.cn</text>'
    )


def label_w(label: str, size: int) -> float:
    """估算文字渲染宽度（用于把圆点摆到标题旁边）。"""
    return sum(size if (0x2E80 <= ord(c) <= 0x9FFF or 0xFF00 <= ord(c) <= 0xFF60)
               else size * 0.55 for c in label)


def _wrap(text: str, size: int, limit_px: float) -> list[str]:
    """按字符宽度折行；先尊重显式换行符 \\n。

    ★ 不处理 \\n 的话，「交回来目录页码全错\\n客户当场退回」会被当一整行塞进
      有限的框里，两栏文字挤成一坨（实测踩过）。
    """
    out: list[str] = []
    for seg in str(text).split("\n"):
        if not seg.strip():
            out.append("")
            continue
        cur, curw = "", 0.0
        for ch in seg:
            cw = size if (0x2E80 <= ord(ch) <= 0x9FFF or 0xFF00 <= ord(ch) <= 0xFF60) else size * 0.55
            if curw + cw > limit_px and cur:
                out.append(cur); cur, curw = ch, cw
            else:
                cur += ch; curw += cw
        if cur:
            out.append(cur)
    return out or [""]


# ── 版式 1：对话体 ────────────────────────────────────────────────────────
def layout_chat(platform: str, msg: str, brand: str = "能言 LangCross") -> str:
    """把一条真实抱怨画成聊天气泡。左侧红点标记说话人，右侧下方是我们的回应。

    ★ 版式纪律：文字基线要从气泡左边距 + 圆点直径 + 间距起算，
      否则第一行会压在红点上（实测踩过）。
    """
    p = platforms.PLATFORMS[platform]
    w, h = p.image
    v = h > w
    pad = round(w * (0.075 if v else 0.055))
    maxw = w - pad * 2
    fs = fit_size(msg, maxw * 0.72, int(h * (0.048 if v else 0.062)))
    dot = int(fs * 0.26)
    text_left = pad + dot * 2 + int(fs * 0.5)
    text_w = maxw - (text_left - pad)

    bubble_lines = _wrap(msg, fs, text_w)[:4]
    line_h = int(fs * 1.55)
    pad_in = int(fs * 0.7)
    # ★ logo 在最上方，内容必须从 logo 视觉底部之下开始 —— 直接按比例给 top
    #   会让 logo 压在气泡左上角（实测踩过）
    lscale = w / 120 * 0.75
    ly = int(h * (0.10 if v else 0.13))
    bubble_h = line_h * len(bubble_lines) + pad_in * 2
    brand_h = int(fs * 0.66) * 4.2          # 品牌回应预留高度
    # 竖版按比例给 top 会让内容全挤在上半、下半一大片空 ⇒ 整块居中
    top = int(_logo_bottom(ly, lscale) + h * (0.06 if not v else 0.03))
    if v:
        block_h = bubble_h + int(h * 0.10) + brand_h
        foot_top = int(h * 0.84)
        top = max(top, ly + (foot_top - ly - block_h) // 2)

    rows = [
        # rect 先画、圆点后画，否则圆点会被气泡盖住（实测踩过：红点完全看不见）
        f'<rect x="{pad}" y="{top}" width="{maxw}" height="{bubble_h}" rx="{int(fs*0.55)}" '
        f'fill="#0E1014" stroke="{LINE}" stroke-width="1.2"/>',
        f'<circle cx="{pad + dot}" cy="{top + pad_in + int(fs*0.35)}" r="{dot}" fill="{RED}"/>',
    ]
    ty = top + pad_in + int(fs * 0.35)
    for ln in bubble_lines:
        rows.append(f'<text x="{text_left}" y="{ty}" {_font(fs)}>{_esc(ln)}</text>')
        ty += line_h

    # 品牌回应：单行，不要重复渲染
    ry = top + bubble_h + int(h * 0.10)
    rfs = int(fs * 0.66)
    rows.append(f'<text x="{pad}" y="{ry}" {_font(rfs, 700, FG)}>{_esc(brand)}</text>')
    rows.append(f'<text x="{pad}" y="{ry + int(rfs*1.9)}" {_font(int(rfs*0.92), 400, SUB)}>'
                f'原版式直出，目录页码图注都不动</text>')
    return _frame(w, h, _logo(pad, ly, lscale)
                  + "\n".join(rows) + "\n" + _footer(w, h, 0.90 if v else 0.86))


# ── 版式 2：前后对比 ──────────────────────────────────────────────────────
def layout_compare(platform: str, before: str, after: str) -> str:
    """左「人工」右「能言」：同一份文档两种结局。

    ★ 版式纪律：① \\n 必须当换行处理；
      ② 圆点要与标题文字同一基线，别各画各的；
      ③ 两栏高度按内容自适应，不要留一大片空。
    """
    p = platforms.PLATFORMS[platform]
    w, h = p.image
    pad = round(w * 0.05)
    gap = round(w * 0.035)
    colw = (w - pad * 2 - gap) // 2
    v = h > w

    fs = fit_size(max(before, after), colw * 0.80, int(h * 0.052))
    lh = int(fs * 1.62)
    left = _wrap(before, fs, colw * 0.80)[:4]
    right = _wrap(after, fs, colw * 0.80)[:4]
    rows_n = max(len(left), len(right))
    box_h = lh * rows_n + int(fs * 1.0)

    # ★ 整块垂直居中：固定按比例给 head_y 会让短内容全挤在上半、下半一片空
    foot_top = int(h * (0.86 if v else 0.80))
    block_h = int(fs * 0.76) + int(h * (0.09 if v else 0.075)) + box_h
    head_y = (foot_top - block_h) // 2 + int(fs * 0.38)
    head_y = max(head_y, int(h * (0.14 if v else 0.10)))
    top = head_y + int(h * (0.09 if v else 0.075))

    rows = []
    for idx, (label, lines, color, dotc) in enumerate([
        ("人工", left, FAINT, RED), ("能言 LangCross", right, FG, FG)
    ]):
        x = pad + idx * (colw + gap)
        cx = x + colw // 2
        dot = max(int(fs * 0.20), 6)
        rows.append(f'<rect x="{x}" y="{top}" width="{colw}" height="{box_h}" rx="10" '
                    f'fill="#0E1014" stroke="{LINE}" stroke-width="1.2"/>')
        # 圆点与标题同一基线（circle 的 cy 就是文字视觉中线）
        rows.append(f'<circle cx="{x + int(colw*0.5) - int(label_w(label, fs)*0.62) - dot*1.4}" '
                    f'cy="{head_y}" r="{dot}" fill="{dotc}"/>')
        rows.append(f'<text x="{cx}" y="{head_y}" font-family="\'PingFang SC\',sans-serif" '
                    f'font-size="{int(fs*0.76)}" font-weight="700" fill="{color}" '
                    f'text-anchor="middle" dominant-baseline="central">{_esc(label)}</text>')
        ty = top + int(fs * 0.92)
        for ln in lines:
            rows.append(f'<text x="{x + int(colw*0.10)}" y="{ty}" {_font(fs, 400, color)}>'
                        f'{_esc(ln)}</text>')
            ty += lh
    return _frame(w, h, "\n".join(rows) + "\n" + _footer(w, h, 0.86))


# ── 版式 3：清单 ──────────────────────────────────────────────────────────
def layout_list(platform: str, title: str, items: list[str]) -> str:
    """清单型：把「交稿前要手工做的事」一条条列出来，每条前画一个勾选框。

    ★ 版式纪律：① logo 与标题不能同高（曾重叠成一团）；
      ② 行距按实际条数算，别写死——5 条时最后一条会撞上页脚；
      ③ 勾选框**画空的**。曾同时画空框 + 琥珀勾，语义自相矛盾，
         读起来像"这几件事已经做完了"，跟"还要手工做"正好相反。
    """
    p = platforms.PLATFORMS[platform]
    w, h = p.image
    v = h > w
    pad = round(w * (0.075 if v else 0.055))
    maxw = w - pad * 2
    items = [x for x in (items or []) if x.strip()][:5]

    fs_title = fit_size(title, maxw, int(h * (0.070 if v else 0.082)))
    longest = max(items, key=len) if items else "字"
    fs_item = fit_size(longest, maxw * 0.80, int(h * (0.042 if v else 0.048)))
    box = max(int(fs_item * 0.42), 14)

    # 顶部：logo 在上，标题在下 —— 标题基线必须让开 logo 的视觉底部
    lscale = w / 120 * (0.6 if v else 0.62)
    logo_y = int(h * (0.06 if v else 0.075))
    title_y = int(_logo_bottom(logo_y, lscale) + fs_title * 0.42)

    # 行距：保底 + 放不下就少画几条。
    # ★ 不能用 min(理想, 可用/n) —— 那会把行距压到全糊在一起（实测踩过）；
    #   也不能让 fit_n 过于保守 —— 横版明明放得下 5 条却只画出 2 条（同样踩过）。
    foot_y = round(h * (0.88 if v else 0.84))
    min_step = int(fs_item * 1.75)
    want_step = int(fs_item * 2.3)
    avail = foot_y - (title_y + int(fs_title * 1.1))
    fit_n = max(1, avail // max(min_step, 1))
    items = items[:fit_n]
    step = min(want_step, avail // max(len(items), 1))
    step = max(step, min_step)

    rows = [
        _logo(pad, logo_y, lscale),
        f'<text x="{pad}" y="{title_y}" {_font(fs_title, 700)}>{_esc(title)}</text>',
    ]
    ty = title_y + int(fs_title * 1.5)
    for it in items:
        # 空心方框＝还没做。这是"待办清单"，不是"已完成清单"。
        rows.append(f'<rect x="{pad}" y="{ty - int(box * 0.82)}" width="{box}" '
                    f'height="{box}" rx="4" fill="none" stroke="{FAINT}" '
                    f'stroke-width="1.6"/>')
        rows.append(f'<text x="{pad + box*1.8}" y="{ty}" {_font(fs_item)}>{_esc(it)}</text>')
        ty += step
    return _frame(w, h, "\n".join(rows) + "\n" + _footer(w, h, 0.90 if v else 0.86))


# ── 版式 4：账单 ──────────────────────────────────────────────────────────
def layout_bill(platform: str, lines: list[tuple[str, str]], note: str = "",
                total: str | None = None) -> str:
    """账单型：把隐性成本列成一张单。

    lines = [(项目, 金额), ...]
    ★ total 由调用方给，不在渲染器里猜金额——字符串没法可靠求和，
      渲染器擅自算出来的数一定是错的（曾写成 sum() 拼字符串直接崩）。
    """
    p = platforms.PLATFORMS[platform]
    w, h = p.image
    pad = round(w * 0.09)
    maxw = w - pad * 2
    rows = [f'<text x="{pad}" y="{round(h*0.20)}" {_font(fit_size("翻译账单", maxw, int(h*0.075)), 700)}>'
            f'翻译账单</text>']
    rows.append(f'<line x1="{pad}" y1="{round(h*0.235)}" x2="{w-pad}" y2="{round(h*0.235)}" '
                f'stroke="{LINE}" stroke-width="1.2"/>')
    rows_l = list(lines or [])[:5]
    fs = fit_size(max((a for a, _ in rows_l), default="字"), maxw * 0.6, int(h * 0.05))
    ty = round(h * 0.32)
    step = int(fs * 2.0)
    for name, amount in rows_l:
        rows.append(f'<text x="{pad}" y="{ty}" {_font(fs)}>{_esc(name)}</text>')
        rows.append(f'<text x="{w-pad}" y="{ty}" font-family="Menlo,monospace" '
                    f'font-size="{fs}" fill="{AMBER}" text-anchor="end">{_esc(amount)}</text>')
        ty += step
    rows.append(f'<line x1="{pad}" y1="{ty - int(fs*0.8)}" x2="{w-pad}" y2="{ty - int(fs*0.8)}" '
                f'stroke="{LINE}" stroke-width="1.2"/>')
    rows.append(f'<text x="{pad}" y="{ty + int(fs*0.3)}" {_font(int(fs*0.9), 700)}>合计</text>')
    if total:
        rows.append(f'<text x="{w-pad}" y="{ty + int(fs*0.3)}" font-family="Menlo,monospace" '
                    f'font-size="{int(fs*0.9)}" font-weight="700" fill="{RED}" '
                    f'text-anchor="end">{_esc(total)}</text>')
    if note:
        nf = fit_size(note, maxw, int(h * 0.042))
        # ★ note 必须**跟在合计下面**，不能写死 0.83——横版页脚线在 0.86，
        #   两者几乎重合，note 直接压住"能言 LangCross"（实测踩过）。
        #   同时给页脚留 0.05 的余量，最坏情况也不重叠。
        ny = min(ty + int(fs * 1.9), round(h * 0.81))
        rows.append(f'<text x="{pad}" y="{ny}" {_font(nf, 400, SUB)}>{_esc(note)}</text>')
    return _frame(w, h, "\n".join(rows) + "\n" + _footer(w, h, 0.86))


# ── 版式 5：金句 ──────────────────────────────────────────────────────────
def layout_quote(platform: str, quote: str, attrib: str = "") -> str:
    """纯金句版：大字居中，一句话说完。适合反直觉观点。"""
    p = platforms.PLATFORMS[platform]
    w, h = p.image
    maxw = w * 0.82
    fs = fit_size(quote, maxw, int(h * (0.105 if h > w else 0.115)))
    # 竖排最多 4 行，横向最多 3 行
    def wrap(text: str, limit_px: float) -> list[str]:
        out, cur, curw = [], "", 0.0
        for ch in text:
            cw = fs if 0x2E80 <= ord(ch) <= 0x9FFF or 0xFF00 <= ord(ch) <= 0xFF60 else fs * 0.55
            if curw + cw > limit_px:
                out.append(cur); cur, curw = ch, cw
            else:
                cur += ch; curw += cw
        if cur:
            out.append(cur)
        return out[:4 if h > w else 3] or [""]
    lines = wrap(quote, maxw)
    lh = int(fs * 1.42)
    total = lh * len(lines)
    start_y = int(h / 2 - total / 2 + fs * 0.5)
    rows = [f'<line x1="{round(w*0.09)}" y1="{int(h*0.14)}" x2="{round(w*0.09)}" '
            f'y2="{int(h*0.14)+int(h*0.06)}" stroke="{AMBER}" stroke-width="5" '
            f'stroke-linecap="round"/>']
    for i, ln in enumerate(lines):
        rows.append(f'<text x="{round(w*0.09)}" y="{start_y + i*lh}" {_font(fs, 700)}>{_esc(ln)}</text>')
    if attrib:
        rows.append(f'<text x="{round(w*0.09)}" y="{start_y + total + int(fs*0.95)}" '
                    f'{_font(int(fs*0.52), 400, SUB)}>{_esc(attrib)}</text>')
    return _frame(w, h, "\n".join(rows) + "\n" + _footer(w, h, 0.90))


LAYOUTS = {
    # ★ 默认首选是 hero 不是 board——board（程序画分镜人物）试产一轮后
    #   判定不合格：火柴人形态怎么调都不好看。代码留着但不要再默认用。
    "hero": "主标（★ 默认：干净、不费劲、最不丑）",
    "bill": "账单（把等待与返工的账摊开，纯排版所以好看）",
    "list": "清单（交稿前要做的事）",
    "quote": "金句（反直觉观点，少用）",
    "story": "时间线（故事的四个瞬间）",
    "compare": "前后对比（人工 vs 能言）",
    "chat": "对话体（像真实抱怨）",
    "board": "⚠ 已停用（2026-10-13）：程序画的人物是火柴人，出来就是幼儿园水平",
}

#★ 明确不该再用的版式。prep_multi 见到会直接回退，不浪费渲染时间。
DEPRECATED_LAYOUTS = {"board"}


# ── 版式 0：分镜（★ 人看的是画面，不是文字）────────────────────────────
def layout_board(platform: str, panels: list[dict], punch: str = "") -> str:
    """分镜格：每格 = 一个简笔画面 + 一句极短注脚。

    panels = [{"scene": <SCENES 的键>, "cap": "注脚（≤14 字）"}, ...]

    ★ 为什么这是首选：短剧脚本的力量在「画面」那一列，台词只是点缀。
      推文同理——人先被画面留住，才愿意看字。
    ★ 版式纪律：
      ① 注脚**必须单行**：折行就变成正文了，观众会跳读；
        放不下就自动缩字号，绝不折行（实测踩过：竖版 2×2 折成两行，
        第二行直接画到格框外面，"小时""上"孤零零挂在那儿）。
      ② 画面在前、注脚在下，视线自上而下；
      ③ 格与格之间用细线分隔，像分镜表；
      ④ 落点（punch）压在底部琥珀带，字号大于注脚，且必须让开页脚。
    """
    p = platforms.PLATFORMS[platform]
    w, h = p.image
    v = h > w
    import sketch

    panels = [x for x in (panels or []) if x.get("scene") in sketch.SCENES][:4]
    if not panels:
        panels = [{"scene": "mail_discover", "cap": ""}]

    foot_ratio = 0.90 if v else 0.86
    pad = round(w * (0.035 if v else 0.022))
    n = len(panels)

    # ── 底部落点带 ────────────────────────────────────────────────────
    # ★ punch 带必须整体坐在页脚线之上（曾压住"能言 LangCross"和网址）。
    #   做法：先定页脚线 foot_y，再从foot_y 往上倒推带子的顶和底。
    punch_fs = fit_size(punch, w - pad * 2, int(h * (0.052 if v else 0.062))) if punch else 0
    punch_h = int(punch_fs * 1.5) if punch else 0
    punch_gap = int(h * 0.022)          # 带子与网格的间距
    punch_bottom_gap = int(h * 0.018)  # 带子与页脚线的间距
    foot_y = round(h * foot_ratio)

    # ── 网格 ─────────────────────────────────────────────────────────
    # ★ logo 占位用 LOGO_TIGHT_H 而不是 LOGO_BOX_H：后者是 30 单位的保守框
    #   （实际图形 y 只占 6~24），在只有 675px 高的横版里会白吃掉 40% 的高度。
    lscale = w / 120 * (0.46 if v else 0.40)
    logo_y = int(h * (0.028 if v else 0.022))
    grid_top = int(logo_y + LOGO_TIGHT_H * lscale + h * 0.018)

    rows_n = 2 if (v and n > 2) else 1
    cols_n = 2 if rows_n == 2 else n
    grid_bottom = (foot_y - punch_bottom_gap - punch_h - punch_gap if punch
                   else foot_y - int(h * 0.02))
    cell_h = (grid_bottom - grid_top) // rows_n
    cell_w = (w - pad * 2) // cols_n

    # ★ 注脚单行：按**最长那条**反算字号，保证四条都不折行。
    #   一旦折行就不是注脚而是正文了（竖版实测折成两行，第二行掉到格外）。
    cap_txt = {i: ((pan.get("cap") or "").strip()) for i, pan in enumerate(panels)}
    cap_fs = max(int(h * (0.030 if v else 0.038)), 12)
    for t in cap_txt.values():
        cap_fs = min(cap_fs, fit_size(t or "字", (cell_w - pad) * 0.86, cap_fs))
    cap_h = int(cap_fs * 1.5)

    parts = [_logo(pad, logo_y, lscale)]
    for idx, pan in enumerate(panels):
        r_i, c_i = (idx // cols_n, idx % cols_n) if rows_n == 2 else (0, idx)
        x0 = pad + c_i * cell_w
        y0 = grid_top + r_i * cell_h
        iw = cell_w - (pad if cols_n > 1 else 0)
        ih = cell_h - (pad if rows_n > 1 else 0)
        # 画面区（上方），注脚在下
        art_h = ih - cap_h
        parts.append(f'<clipPath id="cp{platform}{idx}">'
                     f'<rect x="{x0}" y="{y0}" width="{iw}" height="{art_h}" rx="8"/>'
                     f'</clipPath>')
        # ★ 场景元件按自己的 w/h 画**相对坐标**（0,0 在左上），
        #   必须整块平移到格子原点。漏了这步（实测踩过）：元件画在 y=49 而
        #   clipPath 在 y=153，整块画面被裁到格子上沿之外 —— 图看起来"空白"，
        #   但 SVG 合法、尺寸正确、XML 解析通过，只能靠看图发现。
        parts.append(f'<g clip-path="url(#cp{platform}{idx})">'
                     f'<g transform="translate({x0},{y0})">'
                     + sketch.SCENES[pan["scene"]](iw, art_h) + "</g></g>")
        # 格框：极淡，只做分格感不做卡片感
        parts.append(f'<rect x="{x0}" y="{y0}" width="{iw}" height="{ih}" rx="8" '
                     f'fill="none" stroke="{LINE}" stroke-width="1.2"/>')
        # 注脚：单行，垂直居中在下方留白里
        cap = cap_txt[idx]
        if cap:
            parts.append(f'<text x="{x0 + int(iw*0.05)}" '
                         f'y="{y0 + ih - int(cap_fs * 0.42)}" {_font(cap_fs, 500, FG)}>'
                         f'{_esc(cap)}</text>')
        # 镜号角标，像分镜表
        badge_r = int(min(iw, art_h) * 0.075)
        badge_cx = x0 + iw - badge_r - int(w * 0.012)
        badge_cy = y0 + badge_r + int(w * 0.012)
        parts.append(f'<circle cx="{badge_cx}" cy="{badge_cy}" r="{badge_r}" fill="{AMBER}"/>')
        parts.append(f'<text x="{badge_cx}" y="{badge_cy}" text-anchor="middle" '
                     f'dominant-baseline="central" font-family="Menlo,monospace" '
                     f'font-size="{int(badge_r * 1.15)}" font-weight="700" '
                     f'fill="#000">{idx + 1}</text>')

    # 底部落点带
    if punch:
        by = grid_bottom + punch_gap
        parts.append(f'<rect x="{pad}" y="{by}" width="{w - pad*2}" height="{punch_h}" '
                     f'rx="9" fill="#1A1206" stroke="{AMBER}" stroke-width="1.4"/>')
        parts.append(f'<text x="{pad + int(w*0.022)}" y="{by + int(punch_h*0.5)}" '
                     f'dominant-baseline="central" {_font(punch_fs, 700, AMBER)}>'
                     f'{_esc(punch)}</text>')
    return _frame(w, h, "\n".join(parts) + "\n" + _footer(w, h, foot_ratio))


# ── 版式 0：时间线（★ 故事的视觉形态）────────────────────────────────────
def layout_story(platform: str, when: str, event: str, detail: str, punch: str) -> str:
    """把一件事的四个瞬间排成竖向时间线。

    ★ 为什么这是首选版式：它逼着内容必须有时间、有事件、有细节、有落点。
      前三个格填不满就是故事没讲清楚，会肉眼可见地看出来——比抽象排版更诚实。
    ★ 版式纪律：
      ① 落点（punch）必须有视觉重量（琥珀色、放大），它是故事的转折不是标语；
      ② 时间戳左对齐成列，不然后面三点看不出是同一件事；
      ③ 竖向空间不够就裁 detail，绝不压缩到不可读。
    """
    p = platforms.PLATFORMS[platform]
    w, h = p.image
    v = h > w
    pad = round(w * (0.075 if v else 0.055))
    right = w - pad
    rail_x = pad + int(w * 0.035)          # 时间轴竖线
    text_x = rail_x + int(w * 0.055)

    # 落点是重心：底部一条强调带
    # ★ 落点框底部必须让开页脚——曾压住「能言 LangCross」和网址（看图才发现）
    # ★ 框本身要紧凑：给它 1.85 倍字高太奢侈，会把上方内容挤到字号被压小
    #   （实测踩过：正文被迫缩到 0.5 倍）。1.45 倍既能呼吸又留得下空间。
    foot_top_ratio = 0.90 if v else 0.86
    punch_fs = fit_size(punch, w - pad * 2, int(h * (0.070 if v else 0.078)))
    punch_h = int(punch_fs * 1.45)
    foot_y = round(h * (0.90 if v else 0.86))
    punch_y = foot_y - int(h * 0.055) - punch_h

    # ★ logo 必须完整落在画布内：y 走 top_pad，但 scale 要保证 30 单位图形不超出
    lscale = w / 120 * 0.8
    logo_y = int(h * (0.055 if v else 0.065))
    top_pad = int(_logo_bottom(logo_y, lscale) + h * (0.035 if not v else 0.03))
    avail = punch_y - top_pad - int(h * 0.03)

    steps = [(when, event), ("", detail)]
    fs_when = fit_size(when or "…", w * 0.30, int(h * (0.042 if v else 0.048)))
    fs_body = fit_size(max(event, detail), (right - text_x) * 0.94, int(h * (0.048 if v else 0.055)))
    # ★ 每个节点的完整占位 = 时间戳 + 正文行数 × 行高。step_h 必须按这个算，
    #   只按比例给会把最后一段压到落点框底下（实测踩过）。
    def node_h(ts: str, body: str) -> int:
        lines = len(_wrap(body, fs_body, (right - text_x) * 0.94)[:3])
        head = int(fs_when * 1.5) if ts else int(fs_body * 0.9)
        return head + lines * int(fs_body * 1.5)

    need = sum(node_h(ts, bd) for ts, bd in steps) + int(fs_body * 0.8)
    gap = int(h * 0.02)
    slack = avail - need - gap * (len(steps) - 1)
    extra = 0
    if slack < 0:                        # 不够就轻微缩字号，保底可读
        # ★ 不要开平方衰减：实测 shrink=1.4 时正文被压到 0.84 倍以下、
        #   整段看起来发虚。只做一次线性微调，宁可字略小也不要发虚。
        k = min(0.94, max(0.72, avail / max(need, 1)))
        fs_body = max(int(fs_body * k), int(h * 0.034))
        fs_when = max(int(fs_when * k), int(h * 0.026))
    else:                                # 有余量就均匀拉开
        extra = slack // len(steps)

    # 竖线：从第一个节点圆心画到最后一段正文底部
    first_cy = top_pad + int(fs_when * 0.4)
    last_bottom = top_pad + sum(node_h(ts, bd) for ts, bd in steps[:-1]) \
        + (extra + gap) * (len(steps) - 1) + node_h(*steps[-1])
    rows = [_logo(pad, logo_y, lscale)]
    rows.append(f'<line x1="{rail_x}" y1="{first_cy}" x2="{rail_x}" '
                f'y2="{min(last_bottom - int(fs_body*0.5), punch_y - int(h*0.02))}" '
                f'stroke="{LINE}" stroke-width="2"/>')

    dot = max(int(fs_when * 0.22), 4)
    y = top_pad
    for i, (ts, body) in enumerate(steps):
        cy = y + int(fs_when * 0.4)
        rows.append(f'<circle cx="{rail_x}" cy="{cy}" r="{dot}" fill="{AMBER}"/>')
        if ts:
            rows.append(f'<text x="{text_x}" y="{cy}" '
                        f'font-family="Menlo,monospace" font-size="{fs_when}" '
                        f'fill="{AMBER}">{_esc(ts)}</text>')
        ty = y + int(fs_when * 1.5) if ts else y + int(fs_body * 0.9)
        for ln in _wrap(body, fs_body, (right - text_x) * 0.94)[:3]:
            rows.append(f'<text x="{text_x}" y="{ty}" {_font(fs_body, 400, FG)}>{_esc(ln)}</text>')
            ty += int(fs_body * 1.5)
        y += node_h(ts, body) + gap + extra

    # 落点：琥珀色强调带
    rows.append(f'<rect x="{pad}" y="{punch_y}" width="{right - pad}" height="{punch_h}" '
                f'rx="10" fill="#1A1206" stroke="{AMBER}" stroke-width="1.4"/>')
    rows.append(f'<text x="{pad + int(w*0.03)}" y="{punch_y + int(punch_h*0.5)}" '
                f'dominant-baseline="central" {_font(punch_fs, 700, AMBER)}>{_esc(punch)}</text>')
    return _frame(w, h, "\n".join(rows) + "\n" + _footer(w, h, foot_top_ratio))

# ── 自证 ────────────────────────────────────────────────────────────────
SELF_TEST_CASES = [
    ("board", ([{"scene": "mail_discover", "cap": "交完第 8 小时"},
                {"scene": "two_docs", "cap": "两份对不上"},
                {"scene": "slam", "cap": "「你重做」"},
                {"scene": "check", "cap": "当场翻一遍"}],
               "错的不是判断，是没人告诉你的格式")),
    ("story", ("周四 22:00", "「这版明天上午要」", "次日 9:20，附件是昨天的版本", "11 小时，只在等")),
    ("hero", None),
    ("chat", ("这一版目录全乱了，甲方说周一要交", "重排 + 原版式直出")),
    ("compare", ("交回来目录页码全错\n客户当场退回", "版式不动\n拿到就能发")),
    ("list", ("交稿前你还要手工做这些", ["重排目录与页码", "核对图表编号", "统一品牌名", "补图注", "查页眉页脚"])),
    ("bill", ([("等译 3 天 ×2", "6 天"), ("返工排版", "2 人天"), ("术语返修", "1 次")], "以上都没算进报价单")),
    ("quote", ("客户不看译文，他只看译文像不像原件", "—— 一位做过 5 年本地化的朋友")),
]


def _build(key: str, payload, platform: str) -> str:
    if key == "board":
        return layout_board(platform, payload[0], payload[1])
    if key == "story":
        return layout_story(platform, payload[0], payload[1], payload[2], payload[3])
    if key == "hero":
        import render_multi
        return render_multi.build_svg(platform, "返工一次，这单白干",
                                      "Word / PPT 原版式直出", "拿到手就能发")
    if key == "chat":
        return layout_chat(platform, payload[0], payload[1])
    if key == "compare":
        return layout_compare(platform, payload[0], payload[1])
    if key == "list":
        return layout_list(platform, payload[0], payload[1])
    if key == "bill":
        return layout_bill(platform, payload[0], payload[1], total="见上")
    return layout_quote(platform, payload[0], payload[1])


if __name__ == "__main__":
    import xml.dom.minidom
    import tempfile
    import render_multi
    from render_multi import png_size

    tmp = Path(tempfile.mkdtemp(prefix="layouts_"))
    bad = 0
    for key, payload in SELF_TEST_CASES:
        for plat in ["x", "tiktok"]:
            p = platforms.PLATFORMS[plat]
            try:
                svg = _build(key, payload, plat)
            except Exception as e:  # noqa: BLE001
                print(f"FAIL {key:<8} {plat:<7} 构建异常 {e}")
                bad += 1
                continue
            # ★ XML 合法性必须先查：踩过 _footer 里重复 font-size 属性导致
            #   sharp 报 "Input buffer has corrupt header"，看图看不出、只有解析能抓。
            try:
                xml.dom.minidom.parseString(svg)
            except Exception as e:  # noqa: BLE001
                print(f"FAIL {key:<8} {plat:<7} SVG 非法 XML：{e}")
                bad += 1
                continue
            ext = p.fmt.lower().replace("jpeg", "jpg")
            out = tmp / f"{key}-{plat}.{ext}"
            try:
                render_multi.render_to_png(svg, out, target_width=p.image[0])
                got = png_size(out)
                ok = got == p.image
            except Exception as e:  # noqa: BLE001
                ok, got = False, str(e)[:60]
            print(f"{'OK ' if ok else 'FAIL'} {key:<8} {plat:<7} {got}")
            if not ok:
                bad += 1
    print(f"\n自证产物：{tmp}")
    print("SELF-TEST", "PASS" if bad == 0 else f"FAIL({bad})")
    raise SystemExit(1 if bad else 0)
