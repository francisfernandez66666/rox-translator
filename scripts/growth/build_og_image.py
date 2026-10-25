#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
社交分享卡（og:image）生成器 —— 1200×630 PNG

为什么要有这个脚本而不只留一张图：
    这张图上的**文案**和 106 页 SEO 页一样受同一套红线约束（零「100% 准确」、零上游供应商名、
    零 token 裸值与汇率、不贬低对手）。图一旦画出来就没人再读它写的字，
    下一批改品牌口径时它是唯一没有闸门的那一面。所以文案常量收在本文件里，
    生成前逐条过 `build_seo_pages.lint_text()`——**红线没过就不出图**，而不是出一张违规的 PNG。

产物：
    frontend-react/public/og-image.png（随 vite build 进 dist，由 Caddy/`spa.go` 静态直出）
    被三处引用：`public/g/*.html` 的 og:image、`index.html` 的 og:image、
    服务端 route meta 注入（`internal/api/seo_meta.go`）。三处都指向这一个路径。

用法：
    python3 scripts/growth/build_og_image.py            # 生成
    python3 scripts/growth/build_og_image.py --check    # 只校验已生成的产物（闸门用）

依赖 Pillow（仅生成时需要；`--check` 与产物本身不需要，所以 CI 不装 Pillow 也不会假红）。
字体：默认取 macOS 自带的 Hiragino Sans GB，可用环境变量覆盖——
    OG_FONT_REGULAR / OG_FONT_BOLD / OG_FONT_FAMILY_HINT
非 macOS 机器重生成时先装一款含 CJK 的字面，否则脚本会点名"缺字面"而不是静默出豆腐块。
"""

from __future__ import annotations

import argparse
import os
import struct
import sys
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, os.pardir, os.pardir))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

OUT_PATH = os.path.join(ROOT, "frontend-react", "public", "og-image.png")
# ★ 唯一对外路径：三处引用面（静态页 / index.html / 服务端注入）都拼这一个常量。
#   写成第二个字面量就是「改图忘改链接」的开始（同 AGENTS §一·5 报价单一事实源那一族）。
OG_IMAGE_PATH = "/og-image.png"

W, H = 1200, 630

# —— 面色/字色取 UI 交付令牌（〇-P 现行档），不新造一套颜色 ——
BG = (10, 11, 13)        # --lc-inset #0A0B0D
PANEL = (22, 24, 28)     # --lc-raised #16181C
LINE = (58, 64, 76)      # --lc-border-faint #3A404C
FG = (231, 233, 234)     # --lc-text-1 #E7E9EA
DIM = (139, 147, 159)    # --lc-border-strong #8B939F（次级文字档）
WHITE = (255, 255, 255)  # --lc-fill-white #FFFFFF（实心白：徽标块/主 CTA 档）

# —— 卡面文案（★ 改这里等于改对外承诺，必须先过 lint，见 validate_copy）——
BRAND = "能言 LangCross"
TAGLINE = "术语翻得准，版式还得原样"
FEATURES = [
    ("行业术语硬闸", "词库匹配上的译法，交付前强制统一"),
    ("品牌名不被音译", "企业名、产品名按固定译名保护，不漂移"),
    ("原格式写回", "DOCX / PPTX / PDF / Excel 回来的还是那个文件"),
]
FOOTLINE = "12 语种界面 · 40+ 翻译语种 · 积分透明计费，建单前先给预估"

FONT_CANDIDATES_REGULAR = [
    "/System/Library/Fonts/Hiragino Sans GB.ttc:0",
    "/System/Library/Fonts/Supplemental/Songti.ttc:0",
    "/System/Library/Fonts/Supplemental/Arial Unicode.ttf:0",
]
FONT_CANDIDATES_BOLD = [
    "/System/Library/Fonts/Hiragino Sans GB.ttc:2",
    "/System/Library/Fonts/Supplemental/Songti.ttc:1",
    "/System/Library/Fonts/Supplemental/Arial Unicode.ttf:0",
]


def validate_copy() -> list:
    """红线闸门：卡面每一句都过 SEO 页同一把尺子（禁用词／贬低词／成本口径／占位符）。

    ★ 复用 `build_seo_pages.lint_text` 而不是在这里再抄一份名单——两份名单迟早打架，
      而打架的结果是"图照出、文案违规"，因为 PNG 里没有文本可 grep，闸门天然看不见它。
    """
    from build_seo_pages import lint_text  # type: ignore  # noqa: PLC0415

    bad = []
    for label, text in (
        [("BRAND", BRAND), ("TAGLINE", TAGLINE), ("FOOTLINE", FOOTLINE)]
        + [(f"FEATURES[{i}].{k}", v) for i, (k, v) in enumerate(FEATURES) for k in (0, 1)]
    ):
        for hit in lint_text(text):
            bad.append(f"{label}：{hit}")
    return bad


def load_font(specs: list, size: int):
    """按候选顺序取第一款能加载且**真画得出汉字**的字面，返回 (path, index)。

    ★ 判"画得出"必须看落墨像素而不是 getbbox：缺字时 PIL 会回一个非零的 .notdef 盒子，
      只看 bbox 的脚本会把豆腐块当成成功产物交出去（这一族在本仓反复出现——
      "能打开／能画"不等于"画对了"）。
    ★ 返回的是**来源**（路径＋索引）而不是某个字号的字体对象：一张卡要七种字号，
      每次现取同一路径比在每个字号上调 `font_variant` 可靠（后者在部分 Pillow 版本上不存在）。
    """
    from PIL import Image, ImageDraw, ImageFont  # type: ignore  # noqa: PLC0415

    for raw in specs:
        # 候选写成「/路径/字体.ttc:索引」；索引段必须是纯数字，否则整串当路径
        # （Windows 的「C:\fonts\x.ttf」带冒号，按冒号切会把盘符切成路径）
        path, _, idx = raw.rpartition(":")
        if not (path and idx.isdigit()):
            path, idx = raw, "0"
        index = int(idx)
        try:
            f = ImageFont.truetype(path, size, index=index)
        except Exception:  # noqa: BLE001 - 候选试错，任何一款加载失败都该继续往下找
            continue
        probe = Image.new("L", (size * 6, size * 2), 0)
        ImageDraw.Draw(probe).text((4, 4), "能言术语", font=f, fill=255)
        # histogram()[255]＝纯白像素个数（跨 Pillow 版本稳定；getdata() 在新版已弃用）
        if probe.histogram()[255] < size * 4:
            continue
        return path, index
    raise SystemExit(
        "缺少可画汉字的字体。请装一款 CJK 字面并用 "
        "OG_FONT_REGULAR / OG_FONT_BOLD 指到它（格式：/路径/字体.ttc:索引）")


def render() -> bytes:
    from PIL import Image, ImageDraw, ImageFont  # type: ignore  # noqa: PLC0415

    reg_env, bold_env = os.environ.get("OG_FONT_REGULAR"), os.environ.get("OG_FONT_BOLD")
    reg_src = load_font([reg_env] if reg_env else FONT_CANDIDATES_REGULAR, 28)
    bold_src = load_font([bold_env] if bold_env else FONT_CANDIDATES_BOLD, 28)

    def face(spec: tuple, size: int):
        path, index = spec
        return ImageFont.truetype(path, size, index=index)

    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)

    # 内容卡（圆角 + 描边取交付灰阶档，不引入新色值）
    d.rounded_rectangle((60, 60, W - 60, H - 60), radius=22, fill=PANEL, outline=LINE, width=2)

    # 左上：反相白底徽标（实心白 = 主 CTA 档，与 --lc-fill-white 同源；白底上用面色字）
    badge = (100, 96, 100 + 152, 96 + 74)
    d.rounded_rectangle(badge, radius=14, fill=WHITE)
    d.text((badge[0] + 26, badge[1] + 12), "能言", font=face(bold_src, 40), fill=BG)
    d.text((badge[2] + 24, badge[1] + 14), "LangCross", font=face(bold_src, 40), fill=FG)

    d.text((100, 202), TAGLINE, font=face(bold_src, 46), fill=FG)

    # 三条差异点：白色小方块作项目符（不用 emoji／图标字体，避免缺字面出豆腐块）
    # ★ 纵向档位（徽标 96 / 主张 202 / 三行 292+56 / 分隔线 486 / 页脚 502）是**按 630 高排出来的**，
    #   改任何一档要连着看最后一行会不会压到分隔线上——压线在预览里是"有点挤"，
    #   在分享卡片上是"页脚被切成两截"，而这张图没有任何闸门会替人看见它。
    y = 292
    inner_right = W - 60 - 40      # 卡片右缘留 40px 内垫
    for head, body in FEATURES:
        d.rectangle((100, y + 11, 112, y + 23), fill=WHITE)
        hf = face(bold_src, 30)
        d.text((130, y), head, font=hf, fill=FG)
        hw = d.textlength(head, font=hf)
        bf = face(reg_src, 25)
        d.text((130 + hw + 18, y + 4), body, font=bf, fill=DIM)
        # ★ 溢出必须当场判死，不许"生成一张被裁掉半句的图"：PNG 里没有文本可 grep，
        #   这一维只有画的人知道（分享卡片上表现为最后一行少几个字，零报错）。
        right = 130 + hw + 18 + d.textlength(body, font=bf)
        if right > inner_right:
            raise SystemExit(f"✖ 差异点一行超出卡片右缘：{right:.0f} > {inner_right}（{head}）")
        y += 56

    # 分隔线 + 页脚一行（覆盖语种与计费口径；虚数口径见项目记忆「营销数字」）
    d.line((100, 486, W - 100, 486), fill=LINE, width=2)
    ff = face(reg_src, 24)
    d.text((100, 502), FOOTLINE, font=ff, fill=DIM)
    foot_right = 100 + d.textlength(FOOTLINE, font=ff)
    if foot_right > inner_right:
        raise SystemExit(f"✖ 页脚一行超出卡片右缘：{foot_right:.0f} > {inner_right}")
    if y - 56 + 40 > 486:
        raise SystemExit("✖ 差异点最后一行压到分隔线（分隔线在 y=486，行高要一起看）")

    return img.tobytes()


def encode_png(rgb_bytes: bytes, w: int = W, h: int = H) -> bytes:
    """标准库写 PNG（无 Pillow 时 --check 也要能读产物，所以解码路径同样不依赖它）。"""
    raw = bytearray()
    stride = w * 3
    for y in range(h):
        raw.append(0)                      # filter type 0 = None
        raw += rgb_bytes[y * stride:(y + 1) * stride]

    def chunk(tag: bytes, data: bytes) -> bytes:
        body = tag + data
        return (struct.pack(">I", len(data)) + body
                + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF))

    ihdr = struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(bytes(raw), 9)) + chunk(b"IEND", b""))


def png_size(path: str) -> tuple:
    """只读 IHDR 取宽高——闸门不需要完整解码，也就不需要 Pillow。"""
    with open(path, "rb") as f:
        head = f.read(33)
    if head[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError(f"{path} 不是 PNG")
    return struct.unpack(">II", head[16:24])


def check(path: str) -> int:
    """产物闸门：尺寸必须是 1200×630，且必须真是 PNG（分享爬虫按字节数取图，
    一个 0 字节的占位文件在页面上没有任何症状）。"""
    if not os.path.exists(path):
        print(f"✖ 产物缺失：{path}")
        return 1
    bad = validate_copy()
    if bad:
        print("✖ 卡面文案没过红线闸门：")
        for b in bad:
            print("   ✖ " + b)
        return 1
    try:
        w, h = png_size(path)
    except ValueError as e:
        print(f"✖ {e}")
        return 1
    if (w, h) != (W, H):
        print(f"✖ 尺寸 {w}×{h}，应为 {W}×{H}（社交卡片按 1.91:1 取图）")
        return 1
    size = os.path.getsize(path)
    if size < 2000:
        print(f"✖ 产物只有 {size} B，不像一张真图（空白/截断产物在分享卡片上零症状）")
        return 1
    print(f"OG_IMAGE_OK=1 path={os.path.relpath(path, ROOT)} size={w}x{h} bytes={size} 文案红线=0")
    return 0


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="能言社交分享卡（og:image）生成器")
    ap.add_argument("--check", action="store_true", help="只校验已生成的产物")
    ap.add_argument("--out", default=OUT_PATH)
    a = ap.parse_args(argv)

    if a.check:
        return check(a.out)

    bad = validate_copy()
    if bad:
        print("✖ 卡面文案没过红线闸门，未生成任何文件：")
        for b in bad:
            print("   ✖ " + b)
        return 2

    data = encode_png(render())
    os.makedirs(os.path.dirname(a.out), exist_ok=True)
    with open(a.out, "wb") as f:
        f.write(data)
    print(f"OG_IMAGE_WRITTEN=1 path={os.path.relpath(a.out, ROOT)} bytes={len(data)}")
    return check(a.out)


if __name__ == "__main__":
    sys.exit(main())
