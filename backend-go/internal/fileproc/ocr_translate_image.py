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


def _fit_size(text: str, w: int, h: int) -> int:
    """按框高起字号，再按框宽收敛（长译文自动缩到刚好放得下）；返回最终字号 px。"""
    px = max(MIN_FONT_PX, int(h * 0.86))
    for _ in range(24):
        tw = _measure(_chain(px), text)
        if tw <= w or px <= MIN_FONT_PX:
            return px
        px = max(MIN_FONT_PX, int(px * max(0.55, w / max(tw, 1))))
    return MIN_FONT_PX


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

    painted = 0
    for ln in lines:
        if not isinstance(ln, dict):
            continue
        box = ln.get("box") or []
        if not _valid_box(box):
            continue  # 坏行（坐标缺失/不成对）：跳过这一行，不打断整张图
        text = (ln.get("tran") or "").strip() or (ln.get("text") or "")
        text = " ".join(str(text).replace("\r", " ").replace("\n", " ").split())
        if not text:
            continue
        try:
            left, top, right, bottom = _box_rect(box)
        except Exception:
            continue  # 坐标不是数值：同样按坏行跳过
        if right <= left or bottom <= top:
            continue
        # ① 抹掉原文：用框内**原有底色**（取框周边像素的中位色）填充，
        #    比一律填白更耐看（线图/深色底的截图不会被抹成一片白块）。
        patch = img.crop((left, top, right, bottom))
        bg = patch.resize((1, 1)).getpixel((0, 0))
        draw.rectangle([left, top, right, bottom], fill=bg)
        # ② 写译文：字号按框高起、按框宽收敛；上下居中、左对齐（与原图排版同手）。
        size = _fit_size(text, max(1, right - left - 2), max(1, bottom - top - 2))
        chain = _chain(size)
        y = top + max(0, ((bottom - top) - size) // 2)
        try:
            _draw_text(draw, chain, left + 1, y, text, (0, 0, 0))
        except Exception as exc:
            return _err("Draw", "绘制失败 %s" % exc)
        painted += 1

    if painted == 0:
        # 一行都没画（框全坏/译文全空）：产物与原文无异，如实报错让上游退回"仅译文 .md"，
        # 免得交给客户一张"看着像翻过了、其实一个字没换"的图（本批缺陷的形态）。
        return _err("NothingPainted", "没有任何一行被回填（%d 行里 0 行有可用坐标）" % len(lines))

    try:
        img.save(out)
    except Exception as exc:
        return _err("Out", "产物写入失败 %s" % exc)
    print("INFO:painted=%d/%d" % (painted, len(lines)), file=sys.stderr)
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
