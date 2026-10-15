# ============================================================================
# scripts/xbot/sketch.py — 简笔画面元件（2026-10-13）
#
# ★ 为什么需要这个：
#   短剧脚本的力气在「画面」那一列，不是台词。人看的是分镜，不是文字。
#   所以配图不该是「一段文字 + 一个主标」，而是「几格画面 + 每格一句注脚」。
#
# ★ 画法约束：纯白线稿 + 黑底（沿用品牌纯黑单色体系）。
#   只用几何图形，不画照片感的东西——程序画的写实必然难看，
#   但线稿/简笔风格反而成立且稳定（AI 生图会漂移且写不对中文）。
# ============================================================================
from __future__ import annotations

FG = "#FFFFFF"
SUB = "#8B939F"
FAINT = "#6E7683"
AMBER = "#D29922"
RED = "#F85149"


def head(cx: float, cy: float, r: float, *, brow: bool = False,
         shoulder: bool = True) -> str:
    """一个头+肩的轮廓。加 brow=True 画皱眉（表情绪）。
    ★ 三条踩过的坑：
      ① 头不能太大——r超过格子短边的 22% 就像幼儿园简笔画（实测 0.20 仍偏大，
         收到 0.15 才是"大人"的比例）；
      ② 肩要**连成一条**闭合曲线，不能只画左右两段短线：短线会被读成
         "两个浮空的括号"（实测踩过）；
      ③ 肩宽≈3.4r、落点在 cy+2.0r，扁圆弧而不是深圆弧，深圆弧读成气泡。
    """
    sw = max(r * 0.13, 1.5)
    s = f'<circle cx="{cx:.0f}" cy="{cy:.0f}" r="{r:.0f}" fill="none" ' \
        f'stroke="{FG}" stroke-width="{sw:.1f}"/>'
    if shoulder:
        s += (f'<path d="M{cx-r*1.75:.0f} {cy+r*1.92:.0f} '
              f'Q{cx-r*1.30:.0f} {cy+r*0.92:.0f} {cx:.0f} {cy+r*0.86:.0f} '
              f'Q{cx+r*1.30:.0f} {cy+r*0.92:.0f} {cx+r*1.75:.0f} {cy+r*1.92:.0f}" '
              f'fill="none" stroke="{FG}" stroke-width="{sw:.1f}" '
              f'stroke-linecap="round"/>')
    if brow:
        s += (f'<path d="M{cx-r*0.62:.0f} {cy-r*0.34:.0f} L{cx-r*0.16:.0f} {cy-r*0.12:.0f}" '
              f'stroke="{RED}" stroke-width="{max(r*0.13, 1.4):.1f}" stroke-linecap="round"/>'
              f'<path d="M{cx+r*0.62:.0f} {cy-r*0.34:.0f} L{cx+r*0.16:.0f} {cy-r*0.12:.0f}" '
              f'stroke="{RED}" stroke-width="{max(r*0.13, 1.4):.1f}" stroke-linecap="round"/>')
    return s


def doc(x: float, y: float, w: float, h: float, *, lines: int = 5,
        fold: bool = True, mark: str = "") -> str:
    """一份文件（纸 + 文字线）。mark 可传 "✕" 或 "!" 标出问题。"""
    s = (f'<rect x="{x:.0f}" y="{y:.0f}" width="{w:.0f}" height="{h:.0f}" rx="3" '
         f'fill="none" stroke="{FG}" stroke-width="2"/>')
    lh = h / (lines + 1.6)
    for i in range(lines):
        yy = y + lh * (i + 1.15)
        ww = w * (0.72 if i % 2 == 0 else 0.55)
        s += (f'<line x1="{x+w*0.13:.0f}" y1="{yy:.0f}" x2="{x+w*0.13+ww:.0f}" '
              f'y2="{yy:.0f}" stroke="{FAINT}" stroke-width="{h*0.028:.1f}" '
              f'stroke-linecap="round"/>')
    if fold:
        s += (f'<path d="M{x+w*0.70:.0f} {y+h:.0f} l{w*0.30:.0f} {-h*0.30:.0f}" '
              f'fill="none" stroke="{FG}" stroke-width="1.6" opacity=".5"/>')
    if mark:
        s += (f'<circle cx="{x+w*0.86:.0f}" cy="{y+h*0.20:.0f}" r="{h*0.20:.0f}" '
              f'fill="{RED}"/>'
              f'<text x="{x+w*0.86:.0f}" y="{y+h*0.20:.0f}" text-anchor="middle" '
              f'dominant-baseline="central" font-family="Menlo,monospace" '
              f'font-size="{h*0.26:.0f}" font-weight="700" fill="#000">{mark}</text>')
    return s


def monitor(x: float, y: float, w: float, h: float, *, rows: int = 4,
            warn: int = -1) -> str:
    """电脑屏幕（含支架）。warn 指定第几行标红（0 起，-1 不标）。"""
    s = (f'<rect x="{x:.0f}" y="{y:.0f}" width="{w:.0f}" height="{h:.0f}" rx="4" '
         f'fill="none" stroke="{FG}" stroke-width="2"/>'
         f'<line x1="{x+w*0.42:.0f}" y1="{y+h:.0f}" x2="{x+w*0.42:.0f}" '
         f'y2="{y+h*1.22:.0f}" stroke="{FG}" stroke-width="2"/>'
         f'<line x1="{x+w*0.26:.0f}" y1="{y+h*1.22:.0f}" x2="{x+w*0.74:.0f}" '
         f'y2="{y+h*1.22:.0f}" stroke="{FG}" stroke-width="2" stroke-linecap="round"/>')
    rh = h / (rows + 1.4)
    for i in range(rows):
        yy = y + rh * (i + 1.0)
        ww = w * (0.66 if i % 2 == 0 else 0.46)
        col = RED if i == warn else FAINT
        s += (f'<line x1="{x+w*0.11:.0f}" y1="{yy:.0f}" x2="{x+w*0.11+ww:.0f}" '
              f'y2="{yy:.0f}" stroke="{col}" stroke-width="{h*0.045:.0f}" '
              f'stroke-linecap="round"/>')
    return s


def clock(cx: float, cy: float, r: float, *, hour: int = 10) -> str:
    """钟面。hour 只画到小时位，用于表示「深夜 / 上午」。"""
    hx = cx + r * 0.42 * (0.5 if hour < 7 or hour > 22 else -0.4)
    hy = cy - r * 0.42 * (0.72 if hour < 7 or hour > 22 else 0.42)
    return (f'<circle cx="{cx:.0f}" cy="{cy:.0f}" r="{r:.0f}" fill="none" stroke="{AMBER}" '
            f'stroke-width="{r*0.10:.0f}"/>'
            f'<line x1="{cx:.0f}" y1="{cy:.0f}" x2="{hx:.0f}" y2="{hy:.0f}" '
            f'stroke="{AMBER}" stroke-width="{r*0.09:.0f}" stroke-linecap="round"/>'
            f'<line x1="{cx:.0f}" y1="{cy:.0f}" x2="{cx+r*0.56:.0f}" y2="{cy+r*0.20:.0f}" '
            f'stroke="{AMBER}" stroke-width="{r*0.07:.0f}" stroke-linecap="round" opacity=".7"/>')


def bubble(x: float, y: float, w: float, h: float, *, tail: str = "bl") -> str:
    """空白对话气泡框（文字由外面画）。"""
    s = (f'<rect x="{x:.0f}" y="{y:.0f}" width="{w:.0f}" height="{h:.0f}" rx="{h*0.34:.0f}" '
         f'fill="none" stroke="{FG}" stroke-width="2"/>')
    tw, th = w * 0.14, h * 0.30
    if tail == "bl":
        s += (f'<path d="M{x+w*0.20:.0f} {y+h:.0f} l{tw*0.6:.0f} {th:.0f} '
              f'l{-tw*0.15:.0f} {-th:.0f}" fill="none" stroke="{FG}" stroke-width="2"/>')
    return s


def table(y: float, w: float) -> str:
    """桌面横线。"""
    return (f'<line x1="0" y1="{y:.0f}" x2="{w:.0f}" y2="{y:.0f}" stroke="{FG}" '
            f'stroke-width="3" stroke-linecap="round"/>')


def arrow_down(cx: float, y1: float, y2: float) -> str:
    return (f'<line x1="{cx:.0f}" y1="{y1:.0f}" x2="{cx:.0f}" y2="{y2:.0f}" '
            f'stroke="{AMBER}" stroke-width="2.5" stroke-dasharray="6 5"/>'
            f'<path d="M{cx-6:.0f} {y2-9:.0f} L{cx:.0f} {y2:.0f} L{cx+6:.0f} {y2-9:.0f}" '
            f'fill="none" stroke="{AMBER}" stroke-width="2.5" '
            f'stroke-linecap="round" stroke-linejoin="round"/>')


def check_mark(cx: float, cy: float, r: float) -> str:
    return (f'<path d="M{cx-r*0.8:.0f} {cy:.0f} L{cx-r*0.2:.0f} {cy+r*0.6:.0f} '
            f'L{cx+r*0.85:.0f} {cy-r*0.55:.0f}" fill="none" stroke="{AMBER}" '
            f'stroke-width="{r*0.34:.0f}" stroke-linecap="round" stroke-linejoin="round"/>')


# ── 场景 ────────────────────────────────────────────────────────────────
# ★ 三条通用纪律：
#   ① 每个场景四周留8%~10% 内边距，元素贴边会被 clipPath 切掉、看着像缺笔画；
#   ② 一个场景只讲一个动作，元素不超过 4 个——多了就读不出重点；
#   ③ 情绪靠红色（皱眉/叹号/震动线）点，不靠形容词。
#   ⚠️ 场景函数收到的是格子尺寸，且坐标系原点在格子左上角（0,0），
#      调用方负责整体平移（见 layouts.layout_board）。

def scene_mail_discover(w: int, h: int) -> str:
    """① 打开邮件，发现问题。屏幕 + 皱眉的人。"""
    m = min(w, h) * 0.10
    bw, bh = w - m * 2, h - m * 2
    return (monitor(m, m + bh * 0.04, bw * 0.58, bh * 0.66, rows=4, warn=1)
            + head(m + bw * 0.86, m + bh * 0.38, bh * 0.13, brow=True))


def scene_two_docs(w: int, h: int) -> str:
    """② 两份文件并排，对不上。"""
    m = min(w, h) * 0.10
    bw, bh = w - m * 2, h - m * 2
    dw = bw * 0.32
    return (doc(m, m + bh * 0.06, dw, bh * 0.74, lines=5, mark="!")
            + doc(m + dw + bw * 0.04, m + bh * 0.06, dw, bh * 0.74, lines=5, mark="!")
            + head(m + bw * 0.90, m + bh * 0.60, bh * 0.12, brow=True))


def scene_wait(w: int, h: int) -> str:
    """④ 等。深夜的钟 + 亮着的屏幕 + 一个人。
    ★ 三个元素横向排开，钟要**收小**——之前 r=0.28*bh 时钟面直接压住
      屏幕、把人物挤到右下角（实测踩过），画面读不出"在等"。
    """
    m = min(w, h) * 0.10
    bw, bh = w - m * 2, h - m * 2
    return (clock(m + bw * 0.22, m + bh * 0.40, bh * 0.20, hour=23)
            + monitor(m + bw * 0.50, m + bh * 0.20, bw * 0.34, bh * 0.38, rows=3)
            + head(m + bw * 0.82, m + bh * 0.76, bh * 0.10))


def scene_check(w: int, h: int) -> str:
    """⑤ 对一遍。两份**错开叠放**（不是完全重合）+ 勾。
    ★ 两份文档要错位摆放：完全重合会糊成一坨，看不出是两份。
      勾也不能画太大——占了小半个格子就变成"打钩海报"而不是画面元素。
    """
    m = min(w, h) * 0.10
    bw, bh = w - m * 2, h - m * 2
    return (doc(m, m + bh * 0.06, bw * 0.32, bh * 0.76, lines=5)
            + doc(m + bw * 0.18, m + bh * 0.16, bw * 0.32, bh * 0.76,
                  lines=5, fold=False)
            + check_mark(m + bw * 0.82, m + bh * 0.54, bh * 0.11))


def scene_slam(w: int, h: int) -> str:
    """③ 拍桌。桌子要**看得出来是桌子**（有厚度 + 桌腿），
    不然只有一个横线，读成"两个人站着"，完全不知道在拍。
    ★ 震动线必须从"拍下去的手"向外上方**扇形发散**且互不相交：
      之前左右各画一条同起点的斜线，交叉成了一个巨大的红 X（实测踩过），
      读起来像"禁止"符号，完全跑偏。
    ★ 两个人头要拉开距离 + 一高一低，否则两个圆叠在一起像双胞胎。
    """
    m = min(w, h) * 0.10
    bw, bh = w - m * 2, h - m * 2
    ty = m + bh * 0.70# 桌面高度
    r = bh * 0.13
    # ★ 两个人都摆在**左侧 60%** 内，右侧留出空区给拍击动作和震动线。
    #   之前人摆到 0.70/0.76，震动线无论往哪边发散都会扫到人身上
    #   （实测连踩两次：往左上扫左边人的脸、往右上扫右边人的脸）。
    hit_x = m + bw * 0.78          # 拍点在桌面右侧空区
    return (head(m + bw * 0.14, m + bh * 0.28, r)
            + head(m + bw * 0.42, m + bh * 0.32, r, brow=True)
            # 拍下去的手：从右肩斜向右侧空区的拍点
            + f'<line x1="{m+bw*0.48:.0f}" y1="{m+bh*0.46:.0f}" '
              f'x2="{hit_x-bh*0.06:.0f}" y2="{ty-bh*0.03:.0f}" '
              f'stroke="{FG}" stroke-width="{max(r*0.16,1.6):.1f}" stroke-linecap="round"/>'
            # 桌面（双线 = 有厚度）
            + f'<line x1="{m:.0f}" y1="{ty:.0f}" x2="{m+bw:.0f}" y2="{ty:.0f}" '
              f'stroke="{FG}" stroke-width="3" stroke-linecap="round"/>'
            + f'<line x1="{m:.0f}" y1="{ty+bh*0.07:.0f}" x2="{m+bw:.0f}" '
              f'y2="{ty+bh*0.07:.0f}" stroke="{FAINT}" stroke-width="1.6"/>'
            # 桌腿
            + f'<line x1="{m+bw*0.14:.0f}" y1="{ty+bh*0.07:.0f}" '
              f'x2="{m+bw*0.14:.0f}" y2="{m+bh:.0f}" stroke="{FAINT}" stroke-width="1.6"/>'
            + f'<line x1="{m+bw*0.86:.0f}" y1="{ty+bh*0.07:.0f}" '
              f'x2="{m+bw*0.86:.0f}" y2="{m+bh:.0f}" stroke="{FAINT}" stroke-width="1.6"/>'
            # 震动线：从拍点向右上方**扇形**发散，三道互不相交
            + "".join(f'<line x1="{hit_x:.0f}" y1="{ty-bh*0.03:.0f}" '
                      f'x2="{hit_x+bh*(0.14+0.11*i):.0f}" '
                      f'y2="{ty-bh*(0.28+0.12*i):.0f}" '
                      f'stroke="{RED}" stroke-width="2.4" stroke-linecap="round"/>'
                      for i in range(3)))


SCENES = {
    "mail_discover": scene_mail_discover,
    "two_docs": scene_two_docs,
    "slam": scene_slam,
    "wait": scene_wait,
    "check": scene_check,
}