#!/usr/bin/env python3
"""ocr_translate_image.py — ★ P4「图片原位翻译」的绘制壳（2026-10-14；★ 2026-10-17 修框判据与字体链）。

子命令：
  render <src> --out <file.png> --lines <lines.json>
      按 lines.json 给出的**原图坐标**把原文抹掉、在原位置重写译文，产出一张译好的图。
      lines.json 形如 [{"text":"原文行","box":[x0,y0,x1,y1],"tran":"译文行"}, …]

设计边界（与 fileproc/ocr_image.go 同一口径）：
  · **不做 OCR、不做翻译**。坐标与译文都由 Go 侧喂进来（OCR 在提取期已跑过一次，
    这里再跑一遍既浪费 CPU，又可能切分成不一样的行——那会让"送去翻译的行"与
    "要覆盖的框"对不上，是原位翻译最容易做坏的地方）。
  · **译不出的行保留原文**：抹掉原文却不写东西＝图片上出现一块空洞，比不翻还糟。
    （Go 侧传进来之前已经把空译文回填成原文，这里再判一次是防脚本被单独调用时出洞。）
  · **字体必须"谁有字形谁上"**（★ 2026-10-17）：随包 assets/fonts/DroidSansFallbackFull.ttf
    只覆盖中日韩与全角标点——实测 `'A'`/`'1'`/`'?'`/`'é'`/`'я'`/`'ش'` 全部落到 `.notdef`
    （与该字体自身的哨兵位图逐字节相同），拿它画英文＝整片豆腐块（用户报障的形态之一）。
    所以按「系统候选（中英双全的 Noto CJK／阿里普惠体 → 退纯拉丁的 DejaVu）→ 随包 CJK」
    组一条**字体链**，绘制时逐字挑第一个真有这个字形的字体——同一条译文里中英混排也能各用各的。
    缺字体时如实报 ERR:Font，绝不用 PIL 默认字体画豆腐块。
  · **字号地板按原行高派生、长译文换行而非缩字**（★ 2026-10-19 G3-a）：旧形态从框高起、一味按宽度
    缩到 8px 兜底 ⇒ 长译文被缩成蚂蚁字（用户报障「译后图看不清」）。现：量原行**墨迹高度**，
    地板＝原行高的 FONT_FLOOR_RATIO（0.6），先尝试向右扩进均匀空白区、再按语种换行
    （CJK 按字、拉丁按词、超长词按字符硬切），实在放不下才降到地板并在 stderr 出声。
    ★ 墨迹高度只认**真笔画**：整列全墨（竖线/表格边）与整行全墨（横线/输入框上下边）都要剔除，
      否则带边框的控件会被读成满框高、短内容（`0`/`4`）被画成整格巨字——`_line_ink_height` 两轴同尺。
  · **空框不造字**（★ 2026-10-19）：OCR 在控件边框/图标/下拉箭头上误切出的**无墨框**，
    若这一行要写的又恰好是原文本身（identity 交付，符号段），直接跳过——画上去就是原文里
    根本不存在的「幽灵内容」（现网单里那批被画成满格巨字的 `0`/`4` 与乱入的 `の`）。
    真译文（≠ 原文）即便墨色很淡也照旧绘制，避免把「字号问题」换成「漏译问题」。
  · **抹字底色与文字色按原图自适应**（★ 2026-10-19 G3-b）：底色取框外 12px 环带的**中位色**（取不到
    则不填底、留轻微残影），文字色按底色亮度反差自动选黑/白——旧形态填框内 1×1 均值色 + 恒黑字，
    在深色底/彩色卡片上要么抹出灰块、要么黑字看不见。
  · **框坐标收两种形态**（★ 2026-10-17 修）：4 值矩形 [x0,y0,x1,y1]（`ocr_recognize.py`
    现役输出，见其 `boxes_out.append([x0, y0, x1, y1])`）与 8 值四点（历史/未来的多边形检测器）。
    其余（少于 4 个坐标、坐标数不成对、非数值）按**坏行跳过**，不打断整张图。
    ★ 旧判据写死 `len(box) < 8`（按"四点八坐标"想当然），与现役四值输出不匹配 ⇒ 每一行都被
      `continue` 跳过 ⇒ 译文图＝重编码的原图（尺寸变了、内容还是原文，看着像"翻过了"）——
      正是用户报障「OCR 翻译成功、译后图没变」的真身。

依赖：PIL + numpy（都在 ocr_recognize.py 的 MODULES 里，属同一条 OCR 依赖链）。
"""
import json
import sys
from pathlib import Path

ASSETS_DIR = Path(__file__).resolve().parent / "assets"
# 随包 CJK 字形资产：与 PDF 覆盖层共用同款（assets/fonts/DroidSansFallbackFull.ttf）。
# ★ 它**只有**中日韩与全角标点，拉丁/西里尔/阿拉伯一个都没有 ⇒ 永远排在字体链**末位**当兜底。
FONT_FILE = ASSETS_DIR / "fonts" / "DroidSansFallbackFull.ttf"
# 系统字体候选（部署机自带，按覆盖面从宽到窄）：先中英双全的，再退纯拉丁/多文的。
# 顺序即偏好：排在前面的先被逐字挑中，所以"中英双全"的档要排在只有拉丁的档之前。
FONT_CANDIDATES = (
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",     # Noto CJK（SIL OFL）：中英 + 西里尔
    "/opt/translator/fonts/AlibabaPuHuiTi-3-55-Regular.ttf",      # 阿里普惠体（免费可商用）
    "/usr/share/fonts/truetype/alibaba/puhui-regular.ttf",        # 同上，另一常见落点
    "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",            # 拉丁/希腊/西里尔/阿拉伯（无 CJK）
)
# 兜底字号下限：再小就用下限，避免长行被缩到不可读（宁可略微超出框，也不画蚂蚁字）。
MIN_FONT_PX = 8
# ★ 2026-10-19 G3-a：字号地板＝原行**墨迹高度**的这一比例。
#   旧形态一律从 8px 兜底 ⇒ 长译文被缩成蚂蚁字（用户报障「译后图看不清」的真身）。
#   地板按原行高派生，等于「最多比原文小到 60%」，是保真而不是拍脑袋的常数。
FONT_FLOOR_RATIO = 0.6
# 多行块高＝LINE_SPACING×(行数-1) + 字号；1.18 与常见正文行距同档。
LINE_SPACING = 1.18
# 墨迹判据：与框内背景亮度差超过此值算作笔画（给 JPEG 压缩噪声留余量）。
INK_LUMA_DELTA = 48.0
# ★ 2026-10-19 缺陷②：细长竖条（标题左侧色条、表格竖线）与文字共框时不是文字。
#   现网实证：`因子战法` 框里那条 6px 宽、44px 高的蓝色色条把该行墨迹高度从 25 抬到 44
#   ⇒ 译文按 44 起算被放大成 32px，而同一行右侧**同级**标题只有 20px——"字号忽大忽小"
#   的最后一条来源。判据两维缺一不可：连续 ≤RULE_MAX_COLS 列、每列覆盖率 ≥RULE_COVER。
#   只按"细"或只按"高覆盖率"都会误剔文字：CJK 粗竖笔覆盖率不低（`の` 左弧连续 13 列
#   都 ≥0.6）却因**宽度超出上限**被放行，正是靠宽度这一维把它们分开。
RULE_MAX_COLS = 8
RULE_COVER = 0.6
# ★ 2026-10-19 G3-b：抹字用底色的采样环带宽度（框外 BG_RING_PX 宽的中位色）。
BG_RING_PX = 12
# ★ 2026-10-19 本批：**框内就是一块纯色徽章**时，底色必须取**那块徽章自己的颜色**，不是框外的环带色。
#   现网实证（同一件）：`の` 落在绿卡上的白徽章里（框内白色纯度 0.97、环带绿色）⇒ 旧口径把白徽章
#   填成一块绿斑；`已同步` 是蓝徽章（框内 (180,198,255) 纯度 0.79、环带白）⇒ 旧口径把蓝徽章填成白块。
#   判据＝框内像素按 BG_INNER_PURE_TOL 量化后**最大同色桶的占比** ≥ BG_INNER_PURE_MIN；
#   达不到就仍走环带（普通文字行的框内是"底+字"混合，占比自然上不去，故不受影响）。
BG_INNER_PURE_TOL = 12
BG_INNER_PURE_MIN = 0.75
# ★ 2026-10-19 本批：**软换行机会**（断词修复）。token 比框宽时先在分隔符后断（`Yuan/`＋`time`），
#   不再一律按字符硬切（旧形态把 `Synchronized` 切成 `Synchronize`＋`d`）。
SOFT_BREAK_AFTER = "/-_·\\|,;：、，；"
# 扩框（长译文放不下时向右扩进均匀空白区）的步长与上限，以及「均匀」的标准差门槛。
EXPAND_STEP = 4
EXPAND_MAX_PX = 24
EXPAND_UNIFORM_STD = 14.0
# `.notdef` 哨兵用的探测码位（私用区，任何正常字体都不会有它的字形）。
_SENTINEL_CH = "\uE000"

_FONT_CACHE = {}    # (path, px) → FreeTypeFont（有界：一张图里字号档位有限，长跑进程不许无界增长）
_CHAIN_CACHE = {}   # px → [font, …]（同上）
_FONT_CACHE_MAX = 128   # ≈ 8 个字号档 × (系统候选 + 随包兜底)，足够单图复用
_CHAIN_CACHE_MAX = 32


def _remember(cache: dict, key, val, cap: int):
    """有界缓存写入（按插入序 FIFO 淘汰最早一批；dict 自 3.7 起保序）。"""
    if len(cache) >= cap:
        for k in list(cache.keys())[: max(1, cap // 4)]:
            cache.pop(k, None)
    cache[key] = val
    return val


def _err(variant: str, msg: str) -> int:
    print("ERR:%s:%s" % (variant, msg), file=sys.stderr)
    return 2


def _load_font(path: str, px: int):
    """按路径+字号装载字体（带缓存）。.ttc 用 index=0 取第一个面（含拉丁的那面）。"""
    from PIL import ImageFont

    key = (path, px)
    hit = _FONT_CACHE.get(key)
    if hit is not None:
        return hit
    f = ImageFont.truetype(path, max(MIN_FONT_PX, int(px)), index=0)
    return _remember(_FONT_CACHE, key, f, _FONT_CACHE_MAX)


def _chain(px: int):
    """组一条**同字号**的字体链：系统候选（存在的）在前，随包 CJK 兜底在后。

    随包字体缺失时按 ERR:Font 如实报错（调用方据此退回"仅译文 .md"）；
    系统候选缺文件/装载失败一律静默跳过——它们是**增强**，不是运行前提。
    """
    px = max(MIN_FONT_PX, int(px))
    hit = _CHAIN_CACHE.get(px)
    if hit is not None:
        return hit
    chain = []
    if not FONT_FILE.exists():
        raise RuntimeError("ERR:Font:缺中文字体 %s（随包资产，见 assets/fonts/）" % FONT_FILE)
    for path in FONT_CANDIDATES:
        if not Path(path).exists():
            continue
        try:
            chain.append(_load_font(path, px))
        except Exception:
            continue  # 单个候选坏掉不影响其余（宁可少一种字体，不可整张图不画）
    chain.append(_load_font(str(FONT_FILE), px))  # 兜底永远在链尾：CJK 一定有字形
    return _remember(_CHAIN_CACHE, px, chain, _CHAIN_CACHE_MAX)


def _glyph_sig(font, ch: str):
    """取一个码位的字形位图签名（numpy 视图），供"这个字体有没有这个字"判定用。"""
    import numpy as np

    return np.asarray(font.getmask(ch))


def _has_glyph(font, ch: str) -> bool:
    """这个字体里**真有** ch 的字形吗？判据＝位图与 `.notdef` 哨兵不同。

    ★ 为什么不能用"宽度 > 0"或"画出来有墨"来判：`DroidSansFallbackFull.ttf` 的 `.notdef`
      本身是有墨的方块（102 个非零像素），宽度也非零——只有跟哨兵**逐字节比**才分得清
      "这个字形存在"与"它是不存在时的兜底方块"。
    """
    try:
        sentinel = _glyph_sig(font, _SENTINEL_CH)
        sig = _glyph_sig(font, ch)
    except Exception:
        return True  # 探测不了就当作有：宁可画出兜底方块，也不把整行字丢掉
    return sig.shape != sentinel.shape or not bool((sig == sentinel).all())


def _pick_font(chain, ch: str):
    """逐字挑字体：空白字符用链首（只为占位宽度），其余取链上第一个有该字形的。"""
    if ch.isspace():
        return chain[0]
    for f in chain:
        if _has_glyph(f, ch):
            return f
    return chain[0]  # 谁都没有：用链首（画出来是兜底方块，但不丢字）


def _measure(chain, text: str) -> float:
    """整段文本在给定字体链下的像素宽度（逐字按各自字体取 advance 再累加）。"""
    total = 0.0
    for ch in text:
        f = _pick_font(chain, ch)
        try:
            total += f.getlength(ch)
        except Exception:
            total += f.size
    return total


def _draw_text(draw, chain, x: float, y: float, text: str, fill) -> None:
    """逐字绘制（同一条串里中英各用各的字体）；返回时 x 已推进。"""
    for ch in text:
        f = _pick_font(chain, ch)
        draw.text((x, y), ch, font=f, fill=fill)
        try:
            x += f.getlength(ch)
        except Exception:
            x += f.size


# 掩膜内的书写原点（掩膜只包住墨迹，留 _MASK_PAD 边距给 AA 与负向 bearing）。
_MASK_PAD = 3


def _mask_extent(chain, lines, px: int, lh: int):
    """把多行文本先画到一张**紧贴墨迹**的灰度掩膜上，返回 (mask, bbox)。

    ★ 为什么要先过这张掩膜，而不是直接画到原图上：
      ① **真实墨迹外框**（含 `g/y` 的下伸部与字体自带的上下留白）只有画出来才知道；
         `px * LINE_SPACING * (n-1) + px` 只是"名义块高"，拿它居中/判溢出会算错；
      ② 贴回原图时按**框**裁剪，墨迹越框的部分不会落到邻行的地盘上——那里随后会被
         邻行的底色抹掉，肉眼就是"字被裁成半截"（现网工单实证）。
    """
    from PIL import Image, ImageDraw

    widths = [_measure(chain, l) for l in lines] or [0]
    w = int(max(widths)) + _MASK_PAD * 2 + 2
    h = lh * (len(lines) - 1) + int(px * 1.8) + _MASK_PAD * 2 + 2
    mask = Image.new("L", (max(1, w), max(1, h)), 0)
    _draw_lines(ImageDraw.Draw(mask), chain, _MASK_PAD, _MASK_PAD, lines, lh, 255)
    return mask, mask.getbbox()


def _block_ink_h(chain, lines, px: int, lh: int) -> int:
    """多行文本的**真实墨迹高度**（含下伸部与 AA）；一行墨都没有时返回 0。"""
    _, bb = _mask_extent(chain, lines, px, lh)
    return 0 if bb is None else int(bb[3] - bb[1])


def _valid_box(box) -> bool:
    """框形态校验：至少 4 个坐标，且坐标数成对（4 值矩形或 8 值四点都收）。

    ★ 2026-10-17 修：旧判据是 `len(box) < 8`——那假设所有检测器都吐"四点八坐标"，
    而现役 `ocr_recognize.py` 吐的是**四值矩形** [x0,y0,x1,y1] ⇒ 每一行都被跳过。
    """
    return isinstance(box, (list, tuple)) and len(box) >= 4 and len(box) % 2 == 0


def _box_rect(box):
    """坐标 → (left, top, right, bottom) 轴对齐矩形（含 1px 边距，压住原文残留笔画）。

    4 值 `[x0,y0,x1,y1]` 与 8 值四点都可用：这里只取"所有 x 的最小/最大"与"y 的最小/最大"。
    """
    xs = [box[i] for i in range(0, len(box), 2)]
    ys = [box[i] for i in range(1, len(box), 2)]
    pad = 1
    return max(0, min(xs) - pad), max(0, min(ys) - pad), max(xs) + pad, max(ys) + pad


def _thin_rule_cols(ink, full_cols):
    """细长竖条（色条/竖线）的列掩膜：连续 ≤RULE_MAX_COLS 列、每列覆盖率 ≥RULE_COVER。

    只返回**列掩膜**，不动行：调用方按它把那些列从墨迹里剔掉，行跨度随其余文字列重算。
    连续段在两端（框把色条切掉一角）同样收，故按"段长≤上限"而不是"整框仅此一段"判。

    ★ `full_cols` 这条参数是**刻意**的：满列（贯穿整框的框线）归满列判据，本判据只管
      "细而不满高"那一族——两条**不重叠**。否则摘掉满列判据这件事在窄框线上照样绿
      （细条判据顺手把它替掉了），那条腿当场变成空转锁、缺陷形态却照旧存在（本批首跑真踩）。
    """
    import numpy as np

    if ink.size == 0:
        return np.zeros(ink.shape[1], dtype=bool)
    cover = ink.mean(axis=0)
    hit = (cover >= RULE_COVER) & (~full_cols)
    out = np.zeros(ink.shape[1], dtype=bool)
    start = None
    for c in range(int(hit.size) + 1):
        if c < hit.size and hit[c]:
            if start is None:
                start = c
            continue
        if start is not None and c - start <= RULE_MAX_COLS:
            out[start:c] = True
        start = None
    return out


def _line_ink_height(patch) -> int:
    """原行在框内的**墨迹高度**（像素行数）：与框内背景亮度差超阈值的像素行跨度。

    ★ 全高墨色列（框线/竖线/表格边框）必须先剔除：它们每一行都有墨，
      不剔除会把「一行 12px 的字」读成「整框 40px 高」，字号地板随之被抬到不该有的高度。
    ★ 2026-10-19：**满宽墨色行（横线/输入框上下边）同样要剔除**——只剔列的那一版在现网
      单里把带边框的控件（`全局` 下拉、参数输入格）读成满框高：横边让「每一列都有墨」，
      `rows[-1]-rows[0]+1` 直接等于框高 ⇒ 字号按框高起算，短内容（`0`/`4`）被画成整格巨字
      （用户报障「译后图字号忽大忽小」的最大单一来源）。两轴同一条判据、同一把尺子。
    ★ 2026-10-19 补：**细长竖条**（`_thin_rule_cols`）也要剔——它只占几个列、够不到
      "整列有墨"，却贯穿整框高；本批现网实证它把同级标题的字号差拉到 32 vs 20（见常量注释）。
    """
    import numpy as np

    arr = np.asarray(patch.convert("RGB"), dtype=np.float32)
    if arr.size == 0 or arr.shape[0] < 3 or arr.shape[1] < 3:
        return 0
    lum = arr[:, :, 0] * 0.299 + arr[:, :, 1] * 0.587 + arr[:, :, 2] * 0.114
    bg = float(np.median(lum))
    ink = np.abs(lum - bg) > INK_LUMA_DELTA
    full_cols = ink.all(axis=0)   # 整列都有墨＝框线/竖线，不是文字
    ink[:, full_cols] = False
    ink[:, _thin_rule_cols(ink, full_cols)] = False   # 细长色条/竖线，同样不是文字
    if ink.size:
        full_rows = ink.all(axis=1)   # 整行都有墨＝横线/上下边，同样不是文字
        ink[full_rows, :] = False
    rows = np.where(ink.any(axis=1))[0]
    if rows.size == 0:
        return 0
    return int(rows[-1] - rows[0] + 1)


def _font_floor(ink_h: int, box_h: int) -> int:
    """字号地板＝原行墨迹高度的 FONT_FLOOR_RATIO 倍（量不到原行时退用框高估一个参考高度）。"""
    ref = ink_h if ink_h > 0 else int(box_h * 0.86)
    return max(MIN_FONT_PX, int(ref * FONT_FLOOR_RATIO))


def _is_lone_nonascii(src: str) -> bool:
    """源文是不是**单个非 ASCII 字符**（去掉空白后 len==1 且码位 > 0x7F）。

    这个形状在本链路上就是「OCR 把**控件形状**读成了字」：现网实证＝一枚开着的绿色开关
    被读成 `の`（开关形状确实像 `の`），回来时旧形态把它整块涂绿再画个白 `の`，圆钮就此消失。
    它是**渲染侧**的判据，只决定"要不要重画"，不决定"翻不翻"（那是引擎侧的事）；
    对照：蓝徽章里的 `已同步` 是**真文字**（三个字符），照旧翻译＋回填，不走本判据。
    """
    s = "".join(src.split())
    return len(s) == 1 and ord(s) > 0x7F


def _is_cjk(ch: str) -> bool:
    """是否 CJK 字符（按字换行的档）：汉字/假名/谚文/扩展区。"""
    o = ord(ch)
    return (0x3400 <= o <= 0x4DBF) or (0x4E00 <= o <= 0x9FFF) or (0xF900 <= o <= 0xFAFF) \
        or (0x3040 <= o <= 0x30FF) or (0xAC00 <= o <= 0xD7A3)


def _tokenize(text: str):
    """切成可换行的最小单位：CJK 单字成 token（按字换行），拉丁/西里尔等连写段成 token（按词换行）。
    空白挂在上一 token 尾部，避免换行时把词间空格丢掉。"""
    out, buf = [], ""
    for ch in text:
        if _is_cjk(ch):
            if buf:
                out.append(buf)
                buf = ""
            out.append(ch)
        elif ch.isspace():
            buf += ch
            out.append(buf)
            buf = ""
        else:
            buf += ch
    if buf:
        out.append(buf)
    return out


def _break_token(chain, token: str, max_w: float):
    """单个 token 自身比框还宽（超长英文词/URL）：**先在分隔符后断**，实在没有分隔符才按字符硬切。

    ★ 2026-10-19：旧形态一律按字符硬切，`Synchronized` 被切成 `Synchronize`＋`d`、
      `Yuan/time` 被切成两截，肉眼就是"词被劈开"。分隔符（`/ - _ ·` 等）本来就是可读断点：
      按它断出来的 `Yuan/`＋`time` 是正常排版，且断完每段都更短、往往不必再降字号。
      每段仍要能装进 max_w：装不下就对该段再按字符硬切（老口径只在"连分隔符都救不了"时生效）。
    """
    chunks, buf = [], ""
    for ch in token:
        buf += ch
        if ch in SOFT_BREAK_AFTER:
            chunks.append(buf)
            buf = ""
    if buf:
        chunks.append(buf)
    pieces = []
    for ck in chunks:
        if pieces and _measure(chain, pieces[-1] + ck) <= max_w:
            pieces[-1] += ck     # 贪心合并：能装就装，尽量少断
            continue
        if _measure(chain, ck) <= max_w:
            pieces.append(ck)
            continue
        cur = ""
        for ch in ck:
            if cur and _measure(chain, cur + ch) > max_w:
                pieces.append(cur)
                cur = ch
            else:
                cur += ch
        if cur:
            pieces.append(cur)
    return pieces or [token]


def _wrap_text_dbg(chain, text: str, max_w: float):
    """`_wrap_text` 的实现体：多返回一位 `hard`＝本轮是否用了**按字符硬切**（词被劈开）。

    返回 (行列表, hard)。`hard` 只作 `_layout` 的档位选择依据，不影响交付形态。
    """
    if max_w <= 0 or _measure(chain, text) <= max_w:
        return [text], False
    lines, cur, hard = [], "", False
    for tok in _tokenize(text):
        if not cur:
            if _measure(chain, tok) > max_w:
                pieces = _break_token(chain, tok, max_w)
                hard = hard or _hard_split(pieces)
                lines.extend(pieces[:-1])
                cur = pieces[-1]
            else:
                cur = tok
            continue
        if _measure(chain, cur + tok) <= max_w:
            cur += tok
        else:
            lines.append(cur)
            if _measure(chain, tok) > max_w:
                pieces = _break_token(chain, tok, max_w)
                hard = hard or _hard_split(pieces)
                lines.extend(pieces[:-1])
                cur = pieces[-1]
            else:
                cur = tok
    if cur:
        lines.append(cur)
    return ([l for l in lines if l] or [text]), hard


def _hard_split(pieces) -> bool:
    """本次 token 拆分里**出现了按字符硬切**吗？判据＝拆出的段数多于"分隔符段数"。

    为什么这么判而不是"看有没有分隔符"：`Yuan/time` 断成 2 段属软断（正常排版），
    而 `Synchronized` 断成 2 段是硬切——两者段数相同、形态完全不同，只能问
    "这些段是不是都恰好落在分隔符结束处"（软断的每段都以 SOFT_BREAK_AFTER 结尾，末段除外）。
    """
    if len(pieces) <= 1:
        return False
    for p in pieces[:-1]:
        if not p or p[-1] not in SOFT_BREAK_AFTER:
            return True
    return False


def _wrap_text(chain, text: str, max_w: float):
    """按语种把 text 折进 max_w 宽度：CJK 按字、拉丁按词、超长词先按分隔符断再按字符硬切。

    返回行列表；单行放得下时就是 [text]（**不主动拆行**——短译文保持一行）。
    """
    return _wrap_text_dbg(chain, text, max_w)[0]


def _layout(text: str, w: int, h: int, floor: int, ink_h: int):
    """定最终字号与折行：优先按原行高起、尽量单行；放不下就**换行**；换行也放不下才降到地板。

    返回 (px, lines, overflow)。overflow=True＝已到地板仍超出框，调用方据此出声（不静默画超框）。

    ★ 2026-10-19（缺陷②）：块高判据＝**真实墨迹高度**（`_block_ink_h`），不是名义块高
      `px*LINE_SPACING*(n-1)+px`——后者漏算下伸部与字体留白，会把文本摆得低到越框。
    ★ 2026-10-19 本批：**能装下但把词按字符劈开了**（`_wrap_text_dbg` 的 hard）时继续降字号，
      1px 一档（成档下探会一下掉 10%，把"断词"换成"字号明显小一截"）；降到能不劈词就停。
      降到地板仍劈词 ⇒ 接受（长词在窄框里除劈开没有别的出路），**不谎报越框**。
      注意与"装不下"那一档分工：装不下仍走既有 0.9 成档下探（那一条的行为一字未动）。
    """
    ref = ink_h if ink_h > 0 else int(h * 0.86)
    px = max(floor, int(ref * 0.92))
    px = min(px, max(floor, int(h * 0.98)))
    while True:
        chain = _chain(px)
        lines, hard = _wrap_text_dbg(chain, text, w)
        block_h = _block_ink_h(chain, lines, px, int(px * LINE_SPACING))
        fits = block_h <= h
        if fits and (not hard or px <= floor):
            return px, lines, False
        if px <= floor:
            return px, lines, True
        px = max(floor, px - 1) if fits else max(floor, int(px * 0.9))


def _pick_bg(img, rect):
    """取「抹字用底色」：先认**框内纯色徽章**，再退框外环带（BG_RING_PX 宽）中位色，最后退框内中位色。

    都取不到（框贴图片边缘、面积过小）返回 None —— 宁可不抹底（留轻微残影）也不填一块灰。
    中位色而非均值：环带里混进邻行文字/噪点时，均值会被拉偏、中位色不会。

    ★ 2026-10-19：**环带优先是错的**——它假设「框内底色 == 框外底色」，而徽章/胶囊/色块上的字
      不满足这条。本批两处现网实证（白徽章填成绿斑、蓝徽章填成白块）都是这个假设的直接后果。
      故第一判据改为「框内是不是一块同色底」（占比见 BG_INNER_PURE_MIN）；普通文字行框内
      底色与字混在一起、占比上不去，仍照旧走环带 ⇒ 既有 82 行的取色一个字节都不动。
    """
    import numpy as np

    arr = np.asarray(img.convert("RGB"), dtype=np.float32)
    H, W = arr.shape[:2]
    x0, y0, x1, y1 = rect
    inside = arr[y0:y1, x0:x1].reshape(-1, 3)
    if inside.shape[0] >= 8:
        # 量化到 BG_INNER_PURE_TOL 的桶里数占比：比"逐像素比中位色"稳（抗 JPEG 噪声与 AA 过渡色）。
        q = np.rint(inside / BG_INNER_PURE_TOL).astype(np.int32)
        keys, counts = np.unique(q, axis=0, return_counts=True)
        k = int(np.argmax(counts))
        if counts[k] / float(inside.shape[0]) >= BG_INNER_PURE_MIN:
            members = (q == keys[k]).all(axis=1)
            return tuple(int(round(v)) for v in np.median(inside[members], axis=0))
    bx0, by0 = max(0, x0 - BG_RING_PX), max(0, y0 - BG_RING_PX)
    bx1, by1 = min(W, x1 + BG_RING_PX), min(H, y1 + BG_RING_PX)
    strips = []
    if by0 < y0:
        strips.append(arr[by0:y0, bx0:bx1])
    if y1 < by1:
        strips.append(arr[y1:by1, bx0:bx1])
    if bx0 < x0:
        strips.append(arr[by0:by1, bx0:x0])
    if x1 < bx1:
        strips.append(arr[by0:by1, x1:bx1])
    ring = np.concatenate([s.reshape(-1, 3) for s in strips], axis=0) if strips else np.empty((0, 3), dtype=np.float32)
    if ring.shape[0] >= 8:
        return tuple(int(round(v)) for v in np.median(ring, axis=0))
    if inside.shape[0] >= 8:
        return tuple(int(round(v)) for v in np.median(inside, axis=0))
    return None


def _text_color(bg):
    """按底色亮度挑文字色：亮底出黑字、暗底出白字（对比度固定，不随主题变量漂）。"""
    if not bg:
        return (0, 0, 0)
    lum = 0.299 * bg[0] + 0.587 * bg[1] + 0.114 * bg[2]
    return (0, 0, 0) if lum >= 128 else (255, 255, 255)


def _hits_any(rect, others) -> bool:
    """矩形是否与任一 blocker 相交（扩框时防压到别的行）。"""
    x0, y0, x1, y1 = rect
    for ox0, oy0, ox1, oy1 in others:
        if x0 < ox1 and ox0 < x1 and y0 < oy1 and oy0 < y1:
            return True
    return False


def _expand_rect(img, rect, blockers, max_px: int = EXPAND_MAX_PX):
    """把框向右**逐步**扩进均匀空白区（每步 EXPAND_STEP），上限 max_px、且不与 blockers 相交。

    「均匀」＝新增条带的像素亮度标准差低于 EXPAND_UNIFORM_STD（有字/有图形的地方不扩）。
    只向右：译文一般比原文长，向右单侧已能容纳，且不会压到左侧/上方的邻行——
    扩不动时由换行与字号地板兜底（_layout），不会因此把字号缩没。
    """
    import numpy as np

    arr = np.asarray(img.convert("RGB"), dtype=np.float32)
    H, W = arr.shape[:2]
    x0, y0, x1, y1 = rect
    grew = 0
    while grew < max_px:
        step = min(EXPAND_STEP, max_px - grew)
        nx1 = min(W, x1 + step)
        if nx1 == x1:
            break
        strip = arr[y0:y1, x1:nx1]
        if strip.size == 0 or float(strip.std()) > EXPAND_UNIFORM_STD:
            break
        if _hits_any((x0, y0, nx1, y1), blockers):
            break
        x1 = nx1
        grew += step
    return (x0, y0, x1, y1)


def _draw_lines(draw, chain, x: float, y: float, lines, lh: int, fill) -> None:
    """多行绘制（逐行、逐字挑字体）；返回时无副作用。"""
    for i, line in enumerate(lines):
        _draw_text(draw, chain, x, y + i * lh, line, fill)


def cmd_render(src: str, out: str, lines_path: str) -> int:
    try:
        from PIL import Image, ImageDraw
    except Exception:
        return _err("Import", "Pillow 未安装（pip install Pillow）")

    src_p = Path(src)
    if not src_p.exists():
        return _err("ImageOpen", "源文件不存在 %s" % src)
    try:
        lines = json.loads(Path(lines_path).read_text(encoding="utf-8"))
    except Exception as exc:
        return _err("Lines", "行数据读取失败 %s" % exc)
    if not isinstance(lines, list) or not lines:
        return _err("Empty", "没有可回填的行")

    try:
        img = Image.open(str(src_p)).convert("RGB")
    except Exception:
        return _err("ImageCorrupt", "图片无法读取或已损坏 %s" % src)
    draw = ImageDraw.Draw(img)

    # 先算好每一行的矩形：坏行留 None（跳过），其余进 blockers 供扩框防重叠。
    rects = []
    for ln in lines:
        r = None
        if isinstance(ln, dict):
            box = ln.get("box") or []
            if _valid_box(box):
                try:
                    r = _box_rect(box)
                except Exception:
                    r = None
                if r is not None and (r[2] <= r[0] or r[3] <= r[1]):
                    r = None
        rects.append(r)
    blockers_all = [r for r in rects if r]

    # ★ 2026-10-19（缺陷②）：量墨迹/取底色/试扩框的**参照系一律是"绘制前的原图"**。
    #   逐行绘制是就地改写同一张画布，而 OCR 的框**本身就互相重叠**（现网单里相邻两行的框
    #   重叠 9px），先前那一行画下的字（含下伸部）会落进下一行的框内——拿画布去量，
    #   下一行的 ink_h 就被读成「上一行的尾巴 + 自己的字」（实测 fac_1：21→44），
    #   字号随之从 19px 涨到 36px，肉眼就是用户报的「字号忽大忽小」。
    ref_img = img.copy()
    painted = 0
    overflowed = 0
    blank_skipped = 0   # 空框（无墨）+ 原文交付 的跳过计数：排障用（见下面的 INFO 行）
    for idx, ln in enumerate(lines):
        if not isinstance(ln, dict):
            continue
        r0 = rects[idx]
        if r0 is None:
            continue  # 坏行（坐标缺失/不成对/非数值）：跳过这一行，不打断整张图
        text = (ln.get("tran") or "").strip() or (ln.get("text") or "")
        text = " ".join(str(text).replace("\r", " ").replace("\n", " ").split())
        if not text:
            continue
        # ① 量原行墨迹高度（字号地板的依据；量不到就退用框高），并取框外环带中位色作抹字底色。
        patch = ref_img.crop(r0)
        ink_h = _line_ink_height(patch)
        # ★ 2026-10-19：两类「本轮要写的就是原文、且原文根本不是文字」的行**不重画**（两个判据分开写，
        #   便于各自被独立的反证钉住——合成一个 if 会让「空框不造字」与「单字噪声不重画」互相掩护）。
        #   ① 框内**一处墨都没有**（OCR 在控件边框/图标/下拉箭头上误切的空框）——旧形态，画上去只会造出
        #      原文里根本不存在的幽灵内容，还把空框当满框算字号（现网单里被画成满格巨字）；
        #   ② 源文是**单个非 ASCII 字符**：OCR 把**控件形状**误识成文字的典型形态。现网实证＝一枚开着的
        #      绿色开关被读成 `の`（开关形状确实像），旧形态把它整块涂绿再画一个白 `の`，开关的白色圆钮
        #      就此消失——客户看到的是「原文根本没有的一个大字盖住了开关」，属③幽灵内容族。
        #      判据与 assist 侧同源（`functionWordNoise` 注释：单假名段在 OCR 里几乎只来自图标/开关误识）。
        #   ⚠️ 两类的放行条件都必须是「要写的内容 == 原文」：真译文（text ≠ 原文）即便墨色很淡、
        #      或源文就是单假名，也照旧绘制——否则淡灰小字会被漏译（把「字号问题」换成更坏的
        #      「漏译问题」），或真收到了 `の` 的译文却不画。
        no_ink_identity = ink_h == 0 and text == (ln.get("text") or "").strip()
        noise_identity = _is_lone_nonascii(ln.get("text") or "") and text == (ln.get("text") or "").strip()
        if no_ink_identity or noise_identity:
            blank_skipped += 1
            continue
        bg = _pick_bg(ref_img, r0)
        if bg is not None:
            # 抹掉原文：填**原有底色**（环带中位色），比一律填白/填灰更耐看
            # （线图/深色底/彩色卡片的截图不会被抹出一块突兀的灰或白）。
            # 取不到底色则**不填**（宁愿留轻微残影，也不填一块与周围不一致的灰）。
            draw.rectangle([r0[0], r0[1], r0[2], r0[3]], fill=bg)
        # ② 长译文先尝试向右扩进均匀空白区（只扩右，不压邻行；扩不动由换行兜底）。
        blockers_wo = [b for b in blockers_all if b != r0]
        left, top, right, bottom = _expand_rect(ref_img, r0, blockers_wo)
        inner_w = max(1, right - left - 2)
        inner_h = max(1, bottom - top - 2)
        floor = _font_floor(ink_h, inner_h)
        # ③ 定字号与折行：尽量保持原字号、放不下换行、实在放不下才降到地板（绝不缩成蚂蚁字）。
        size, draw_lines, overflow = _layout(text, inner_w, inner_h, floor, ink_h)
        if overflow:
            overflowed += 1
        chain = _chain(size)
        # ④ 文字色按底色亮度反差自动选：亮底黑字、暗底白字（暗底上用黑字＝看不见）。
        fill = _text_color(bg)
        lh = int(size * LINE_SPACING)
        # ⑤ 覆盖率掩膜 + **按框裁剪**贴回：墨迹只落在自己的框里（越框部分丢弃），
        #    垂直位置按**真实墨迹**居中。两件事一起做才治得住「字被邻行底色抹掉半截」——
        #    只居中不裁剪：框高不够时墨迹仍会伸进邻行、被邻行底色吃掉；
        #    只裁剪不按墨迹居中：本来放得下的行也会被压到框边。
        mask, bb = _mask_extent(chain, draw_lines, size, lh)
        if bb is not None:
            bw, bh = right - left, bottom - top
            if bw > 0 and bh > 0:
                tile = Image.new("L", (bw, bh), 0)
                # 墨迹横向仍**笔起点**对齐（left+1，与改造前逐字绘制同一位形）；
                # 纵向按墨迹居中，并夹进框内（bh-2 与 _box_rect 的 1px 边距对齐）。
                tile.paste(mask, (1 - bb[0], max(0, (bh - 2 - int(bb[3] - bb[1])) // 2) - bb[1]))
                try:
                    img.paste(fill, (left, top), tile)
                except Exception as exc:
                    return _err("Draw", "绘制失败 %s" % exc)
        painted += 1

    if painted == 0:
        # 一行都没画（框全坏/译文全空/整图都是被跳过的噪声行）：产物与原文无异，如实报错让上游退回
        # "仅译文 .md"，免得交给客户一张"看着像翻过了、其实一个字没换"的图（本批缺陷的形态）。
        # 文案带上 blank_skipped：否则"0 行有可用坐标"会把「坐标全坏」与「整图全是噪声行」
        # 两种成因混成一句（前者要查 OCR，后者是设计内的跳过）——排障读到的成因必须是真成因。
        return _err("NothingPainted", "没有任何一行被回填（%d 行里 %d 行被跳过为「原文交付/噪声行」，其余无可用坐标）"
                    % (len(lines), blank_skipped))

    try:
        img.save(out)
    except Exception as exc:
        return _err("Out", "产物写入失败 %s" % exc)
    print("INFO:painted=%d/%d overflow=%d blank_skipped=%d" % (painted, len(lines), overflowed, blank_skipped), file=sys.stderr)
    return 0


def main() -> int:
    argv = sys.argv[1:]
    if not argv or argv[0] != "render":
        print("ERR:Usage:render <src> --out <file> --lines <lines.json>", file=sys.stderr)
        return 2
    src = argv[1] if len(argv) > 1 else ""
    out, lines_path = "", ""
    i = 2
    while i < len(argv):
        if argv[i] == "--out" and i + 1 < len(argv):
            out = argv[i + 1]
            i += 2
        elif argv[i] == "--lines" and i + 1 < len(argv):
            lines_path = argv[i + 1]
            i += 2
        else:
            i += 1
    if not src or not out or not lines_path:
        print("ERR:Usage:render <src> --out <file> --lines <lines.json>", file=sys.stderr)
        return 2
    return cmd_render(src, out, lines_path)


if __name__ == "__main__":
    sys.exit(main())
