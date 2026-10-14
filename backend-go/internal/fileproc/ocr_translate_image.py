#!/usr/bin/env python3
"""ocr_translate_image.py — ★ P4「图片原位翻译」的绘制壳（2026-10-14）。

子命令：
  render <src> --out <file.png> --lines <lines.json>
      按 lines.json 给出的**原图坐标**把原文抹掉、在原位置重写译文，产出一张译好的图。
      lines.json 形如 [{"text":"原文行","box":[x1,y1,x2,y2,x3,y3,x4,y4],"tran":"译文行"}, …]

设计边界（与 fileproc/ocr_image.go 同一口径）：
  · **不做 OCR、不做翻译**。坐标与译文都由 Go 侧喂进来（OCR 在提取期已跑过一次，
    这里再跑一遍既浪费 CPU，又可能切分成不一样的行——那会让"送去翻译的行"与
    "要覆盖的框"对不上，是原位翻译最容易做坏的地方）。
  · **译不出的行保留原文**：抹掉原文却不写东西＝图片上出现一块空洞，比不翻还糟。
    （Go 侧传进来之前已经把空译文回填成原文，这里再判一次是防脚本被单独调用时出洞。）
  · **字体必带中文**：用随包的 assets/fonts/DroidSansFallbackFull.ttf（Droid Sans Fallback，
    Apache-2.0，PDF 覆盖层同款资产）。缺字体时如实报 ERR:Font，绝不用 PIL 默认字体画豆腐块。

依赖：PIL + numpy（都在 ocr_recognize.py 的 MODULES 里，属同一条 OCR 依赖链）。
"""
import json
import sys
from pathlib import Path

ASSETS_DIR = Path(__file__).resolve().parent / "assets"
# 中文/多语字形资产：与 PDF 覆盖层共用同款（assets/fonts/DroidSansFallbackFull.ttf）。
FONT_FILE = ASSETS_DIR / "fonts" / "DroidSansFallbackFull.ttf"
# 兜底字号下限：再小就用下限，避免长行被缩到不可读（宁可略微超出框，也不画蚂蚁字）。
MIN_FONT_PX = 8


def _err(variant: str, msg: str) -> int:
    print("ERR:%s:%s" % (variant, msg), file=sys.stderr)
    return 2


def _font(px: int):
    from PIL import ImageFont

    if not FONT_FILE.exists():
        raise RuntimeError("ERR:Font:缺中文字体 %s（随包资产，见 assets/fonts/）" % FONT_FILE)
    return ImageFont.truetype(str(FONT_FILE), max(MIN_FONT_PX, int(px)))


def _box_rect(box):
    """四点坐标 → (left, top, right, bottom) 轴对齐矩形（含 1px 边距，压住原文残留笔画）。"""
    xs = [box[i] for i in range(0, len(box), 2)]
    ys = [box[i] for i in range(1, len(box), 2)]
    pad = 1
    return max(0, min(xs) - pad), max(0, min(ys) - pad), max(xs) + pad, max(ys) + pad


def _fit_font(draw, text, w, h):
    """按框高起字号，再按框宽收敛（长译文自动缩到刚好放得下）。"""
    from PIL import ImageFont

    px = max(MIN_FONT_PX, int(h * 0.86))
    for _ in range(24):
        f = _font(px)
        try:
            tw = draw.textlength(text, font=f)
        except Exception:
            f = ImageFont.truetype(str(FONT_FILE), max(MIN_FONT_PX, px))
            tw = draw.textlength(text, font=f)
        if tw <= w or px <= MIN_FONT_PX:
            return f
        px = max(MIN_FONT_PX, int(px * max(0.55, w / max(tw, 1))))
    return _font(MIN_FONT_PX)


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

    for ln in lines:
        box = ln.get("box") or []
        if len(box) < 8:
            continue
        text = (ln.get("tran") or "").strip() or (ln.get("text") or "")
        if not text:
            continue
        left, top, right, bottom = _box_rect(box)
        # ① 抹掉原文：用框内**原有底色**（取框周边像素的中位色）填充，
        #    比一律填白更耐看（线图/深色底的截图不会被抹成一片白块）。
        patch = img.crop((left, top, right, bottom))
        bg = patch.resize((1, 1)).getpixel((0, 0))
        draw.rectangle([left, top, right, bottom], fill=bg)
        # ② 写译文：字号按框高起、按框宽收敛；上下居中、左对齐（与原图排版同手）。
        f = _fit_font(draw, text, max(1, right - left - 2), max(1, bottom - top - 2))
        try:
            draw.text((left + 1, top + max(0, ((bottom - top) - f.size) // 2)), text, font=f, fill=(0, 0, 0))
        except Exception as exc:
            return _err("Draw", "绘制失败 %s" % exc)

    try:
        img.save(out)
    except Exception as exc:
        return _err("Out", "产物写入失败 %s" % exc)
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
