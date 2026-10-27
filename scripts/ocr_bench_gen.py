#!/usr/bin/env python3
"""ocr_bench_gen.py — 合成 OCR 基准集生成器（主方案 §15 测量门）。

为什么要有它：`assets/ocr/` 的权重要在部署机才能跑，而"识别率有没有变好"不能靠人看图。
本脚本用 PIL 把**已知文本**渲染成图并同步产出真值清单（manifest.json），于是 CER 与
噪声行精度都可以由 `cmd/ocrbench` 全自动算出。

刻意包含的三类样本（对应本批要治的现网形态）：
  · 表格两列（label / value）——检验阅读顺序与列聚类；
  · 重复列（manual / manual / manual）——检验同列众数纠正（column_mode）；
  · 一枚"开关/图标"噪声块（图上没有文字，但会诱导检测腿出框）——检验 box_thresh 与几何腿。

用法（部署机，需 PIL；字体缺省用随包 DroidSansFallbackFull.ttf）：
  python3 scripts/ocr_bench_gen.py --out /tmp/ocrbench --rows 12 --lang en
  python3 scripts/ocr_bench_gen.py --out /tmp/ocrbench --scale 0.5 --quality 40 --blur 0.6
产物：<out>/bench-XX.png（或 .jpg）＋ <out>/manifest.json
"""
import argparse
import json
import random
from pathlib import Path

try:
    from PIL import Image, ImageDraw, ImageFilter, ImageFont
except ImportError as e:  # 部署机没装 PIL 时明确报错，不静默
    raise SystemExit("ERR:Bench:缺少 Pillow（pip install Pillow）: %s" % e)

EN_WORDS = ["Formative Tactics", "manual", "manual", "manual", "dragon_return",
            "Floating profit and loss", "Yuan/time", "double_bump", "value", "enabled"]
ZH_WORDS = ["产品说明书", "手动", "手动", "手动", "浮动盈亏", "元/次",
            "双凸起", "数值", "已启用", "测试工位"]
VALUES = ["12.5", "0.00", "4716", "on", "off", "¥1003.00", "09-17 09:40:33"]


def pick_font(size: int, font_path: str | None):
    """首选随包 CJK 字体；找不到就退回 PIL 默认字体（英文样本仍可用，中文会成方块——manifest 会标注）。"""
    cands = []
    if font_path:
        cands.append(Path(font_path))
    cands += [
        Path(__file__).resolve().parent.parent / "backend-go/internal/fileproc/assets/fonts/DroidSansFallbackFull.ttf",
        Path("/opt/translator/bin/assets/fonts/DroidSansFallbackFull.ttf"),
    ]
    for p in cands:
        if p.is_file():
            return ImageFont.truetype(str(p), size), str(p)
    return ImageFont.load_default(), ""


def build_case(out: Path, k: int, words, rows: int, font, cjk: bool,
               scale: float, quality: int, blur: float):
    """渲染一张合成图并返回 (文件名, expected, noise)。

    版式：两列表格（label / value）＋ 中行一枚绿色"开关"块（图上没有文字）。
    """
    W, H = 900, 80 + rows * 46
    img = Image.new("RGB", (W, H), "white")
    d = ImageDraw.Draw(img)
    d.text((30, 20), "OCR 基准（合成）" if cjk else "OCR bench (synthetic)", fill="black", font=font)
    expected, noise = [], []
    for i in range(rows):
        y = 70 + i * 46
        label = words[i % len(words)]
        val = VALUES[i % len(VALUES)]
        d.text((30, y), label, fill="black", font=font)
        d.text((520, y), val, fill="black", font=font)
        expected += [label, val]
        if i == rows // 2:
            d.rounded_rectangle([700, y - 4, 760, y + 28], radius=14, fill=(30, 160, 80))
            noise.append("の")  # 现网实证的误识形态（换别的字形同样成立）
    if 0 < scale < 1.0:
        img = img.resize((max(1, int(W * scale)), max(1, int(H * scale))), Image.BILINEAR)
    if blur > 0:
        img = img.filter(ImageFilter.GaussianBlur(blur))
    name = "bench-%02d%s" % (k, ".jpg" if quality < 95 else ".png")
    p = out / name
    if name.endswith(".jpg"):
        img.save(p, quality=quality)
    else:
        img.save(p)
    return name, expected, noise


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True, help="输出目录（图与 manifest.json）")
    ap.add_argument("--images", type=int, default=3, help="生成张数（默认 3）")
    ap.add_argument("--rows", type=int, default=12, help="每张表的行数（默认 12）")
    ap.add_argument("--lang", choices=["en", "zh"], default="en")
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--font", default="", help="显式字体路径（缺省用随包 CJK 字体）")
    ap.add_argument("--scale", type=float, default=1.0, help="额外缩放（<1 模拟低分辨率）")
    ap.add_argument("--quality", type=int, default=95, help="JPEG 质量（<95 会转 jpg）")
    ap.add_argument("--blur", type=float, default=0.0, help="高斯模糊半径（0=不模糊）")
    args = ap.parse_args()

    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    _ = random.Random(args.seed)  # 预留：将来加随机排版扰动时用它，保持 --seed 可复现
    font, font_used = pick_font(26, args.font or None)
    words = EN_WORDS if args.lang == "en" else ZH_WORDS
    entries = []
    for k in range(args.images):
        name, expected, noise = build_case(out, k, words, args.rows, font, args.lang == "zh",
                                           args.scale, args.quality, args.blur)
        entries.append({"image": name, "expected": expected, "noise": noise})
        print("WROTE %s rows=%d noise=%s" % (out / name, len(expected), ",".join(noise) or "-"))
    manifest = {"lang": args.lang, "rows": args.rows, "font": font_used or "default",
                "scale": args.scale, "quality": args.quality, "blur": args.blur,
                "images": entries}
    (out / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")
    print("BENCH_MANIFEST=%s images=%d" % (out / "manifest.json", len(entries)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
