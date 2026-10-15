# ============================================================================
# scripts/xbot/render_multi.py — 多平台配图渲染（2026-10-13）
#
# ★ 同一套内容，各平台要的图规格完全不同：
#     X           1200×675   16:9   横向横幅
#     Instagram   1080×1350  4:5   竖版（信息流占比最大）
#     TikTok      1080×1920  9:16  全屏竖版
#     Facebook    1200×630   1.91:1 横向
#   一稿一图在 X 上有效，在 TikTok 上会被黑边吃掉一半。
#
# ★ 文字一律程序渲染，不走 AI 生图（字形必错、每天漂移）。
# ★ fit_size() 是必须的：固定字号排中文必然溢出被裁（踩过两次）。
# ============================================================================
from __future__ import annotations

import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import platforms  # noqa: E402

BASE = Path(__file__).resolve().parent

# 品牌色值（纯黑单色体系，与 export_banner.js / render.py 同源）
BG = "#000000"
FG = "#FFFFFF"
SUB = "#8B939F"
FAINT = "#6E7683"

# 品牌 logo 图形：两条错位的钩形笔画构成一个整体（第二条半透明）
# ★ 只画这一个 <g>。曾用 translate 复刻路径，结果 logo 出现两个
#   且一个压住页脚网址——文件能正常渲染，不报任何错，只能靠肉眼看图发现。
LOGO_G = (
    '<path d="M9.5 10.5v5.2a4.3 4.3 0 0 0 8.6 0V12" stroke="{fg}" stroke-width="2.4" '
    'fill="none" stroke-linecap="round"/>'
    '<path d="M20.5 19.5v-5.2a4.3 4.3 0 0 0-8.6 0V18" stroke="{fg}" stroke-width="2.4" '
    'fill="none" stroke-linecap="round" opacity=".5"/>'
)


def _logo(x: float, y: float, scale: float) -> str:
    return (f'<g transform="translate({round(x,1)},{round(y,1)}) scale({round(scale,2)})">'
            + LOGO_G.format(fg=FG) + "</g>")


def fit_size(text: str, max_width: float, max_size: int) -> int:
    """按字宽反算字号：全角 1.0em、半角 0.55em。缺了它中文标题必被裁。

    ★ 空文本不能除零（AI 字段填错是常态，脚本不该崩）——
      实测「compare 版式没有 main 字段」回退到 hero 时传进空串，直接 ZeroDivisionError。
    """
    text = text or ""
    if not text.strip():
        return max(18, max_size // 2)
    em = 0.0
    for ch in text:
        cp = ord(ch)
        em += 1.0 if (0x2E80 <= cp <= 0x9FFF or 0xFF00 <= cp <= 0xFF60
                      or 0x3000 <= cp <= 0x303F) else 0.55
    return int(min(max_size, max(18, max_width / em)))


def _esc(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def build_svg(platform: str, main: str, sub1: str = "", sub2: str = "") -> str:
    """按平台规格生成 SVG。竖版平台（IG/TikTok）的排版是另一套逻辑。"""
    p = platforms.PLATFORMS[platform]
    w, h = p.image
    vertical = h > w

    if vertical:
        # 竖版：左边距小一点（留 1/12），标题占满宽度，副标在下方
        left = round(w / 12)
        avail = w - left * 2
        logo_g = _logo(left, round(h * 0.10), w / 120 * 0.85)
        main_y = round(h * 0.42)
        fs_main = fit_size(main, avail, int(h * 0.115))
        sub_step = int(fs_main * 1.35)
        rows = [
            f'<text x="{left}" y="{main_y}" font-family="\'PingFang SC\',\'Hiragino Sans GB\',sans-serif" '
            f'font-size="{fs_main}" font-weight="700" fill="{FG}">{_esc(main)}</text>'
        ]
        y = main_y + sub_step
        for s in (sub1, sub2):
            if not s:
                continue
            rows.append(
                f'<text x="{left}" y="{y}" font-family="\'PingFang SC\',\'Hiragino Sans GB\',sans-serif" '
                f'font-size="{fit_size(s, avail, int(h*0.038))}" fill="{SUB}">{_esc(s)}</text>'
            )
            y += int(h * 0.055)
        rows.append(
            f'<text x="{left}" y="{round(h*0.90)}" font-family="\'PingFang SC\',sans-serif" '
            f'font-size="{int(h*0.030)}" fill="{FG}">能言 LangCross</text>'
        )
        rows.append(
            f'<text x="{left}" y="{round(h*0.945)}" font-family="Menlo,monospace" '
            f'font-size="{int(h*0.026)}" fill="{FAINT}">langcross.lexicorn.cn</text>'
        )
        scale = w / 120
    else:
        # 横版：与单平台版一致，左侧 logo、右侧文字
        left = round(w * 0.30)
        right_margin = round(w * 0.06)
        avail = w - left - right_margin
        logo_g = _logo(round(w * 0.05), round(h * 0.278), w / 120)
        rows = [
            f'<text x="{left}" y="{round(h*0.43)}" font-family="\'PingFang SC\',\'Hiragino Sans GB\',sans-serif" '
            f'font-size="{fit_size(main, avail, int(h*0.122))}" font-weight="700" fill="{FG}">{_esc(main)}</text>'
        ]
        y = round(h * 0.55)
        for s in (sub1, sub2):
            if not s:
                continue
            rows.append(
                f'<text x="{left}" y="{y}" font-family="\'PingFang SC\',\'Hiragino Sans GB\',sans-serif" '
                f'font-size="{fit_size(s, avail, int(h*0.053))}" fill="{SUB}">{_esc(s)}</text>'
            )
            y += int(h * 0.095)
        rows.append(f'<text x="{left}" y="{round(h*0.83)}" font-family="\'PingFang SC\',sans-serif" '
                    f'font-size="{int(h*0.039)}" fill="{FG}">能言 LangCross</text>')
        rows.append(f'<text x="{left}" y="{round(h*0.90)}" font-family="Menlo,monospace" '
                    f'font-size="{int(h*0.039)}" fill="{FAINT}">langcross.lexicorn.cn</text>')

    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}">\n'
            f'<rect width="{w}" height="{h}" fill="{BG}"/>\n'
            f'<rect x="1" y="1" width="{w-2}" height="{h-2}" fill="none" stroke="#2A2F3A" stroke-width="2" rx="18"/>\n'
            f'{logo_g}\n' + "\n".join(rows) + "\n</svg>")


def render_to_png(svg: str, out_path: Path, target_width: int | None = None) -> None:
    out_path.parent.mkdir(parents=True, exist_ok=True)
    tmp_svg = out_path.with_suffix(".svg")
    tmp_svg.write_text(svg, encoding="utf-8")
    cmd = ["node", str(BASE / "_rasterize.js"), str(tmp_svg), str(out_path)]
    if target_width:
        cmd.append(str(target_width))
    r = subprocess.run(cmd, capture_output=True, text=True)
    tmp_svg.unlink(missing_ok=True)
    if r.returncode != 0:
        raise RuntimeError(f"渲染失败 {out_path.name}: {r.stderr.strip()}")
    # ★ 必须核对实际尺寸：sharp 的 density 会让输出悄悄放大 5.33 倍，
    #   文件更大、体积检查能过、平台上限却已突破 —— 这类静默失败只能靠断言尺寸防住。
    if target_width and r.stdout.strip():
        got = r.stdout.strip().splitlines()[-1]
        if not got.startswith(f"{target_width}x"):
            raise RuntimeError(f"尺寸不符：{out_path.name} 实际 {got}，期望宽 {target_width}")


def render_set(platform: str, main: str, sub1: str, sub2: str,
               out_dir: Path, stem: str) -> Path:
    """渲染一个平台版本的图，返回路径。"""
    p = platforms.PLATFORMS[platform]
    svg = build_svg(platform, main, sub1, sub2)
    ext = p.fmt.lower().replace("jpeg", "jpg")
    out = out_dir / f"{stem}.{ext}"
    render_to_png(svg, out, target_width=p.image[0])
    return out


def png_size(f: Path) -> tuple[int, int]:
    """读文件头拿实际像素尺寸。不依赖 PIL。"""
    import struct
    d = f.open("rb").read(32)
    if d[:8] == b"\x89PNG\r\n\x1a\n":
        return struct.unpack(">II", d[16:24])
    if d[:2] == b"\xff\xd8":  # JPEG 要走段扫描
        raw = f.open("rb").read()
        i = 2
        while i < len(raw) - 9:
            if raw[i] != 0xFF:
                i += 1
                continue
            m = raw[i + 1]
            if m in (0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF):
                h, w = struct.unpack(">HH", raw[i + 5:i + 9])
                return w, h
            if m in (0xD8, 0xD9) or 0xD0 <= m <= 0xD7:
                i += 2
                continue
            ln = struct.unpack(">H", raw[i + 2:i + 4])[0]
            i += 2 + ln
    return (0, 0)


if __name__ == "__main__":  # 自证：跑 `python3 render_multi.py` 必须全绿
    import tempfile
    tmp = Path(tempfile.mkdtemp(prefix="render_multi_"))
    bad = 0
    for key in platforms.ORDER:
        p = platforms.PLATFORMS[key]
        out = render_set(key, "返工一次，这单白干", "Word / PPT / Excel / PDF 原版式直出",
                         "拿到手就能发，不用再排一遍", tmp, f"t-{key}")
        # ★ 必须断言像素尺寸，不能只查文件大小：2026-10-13 踩过——
        #   sharp density=384 让 1200×675 实际出成 6400×3600（5.33 倍），
        #   文件更大所以体积检查照样通过，平台尺寸上限却被静默突破。
        got = png_size(out) if out.exists() else (0, 0)
        ok = got == p.image
        print(f"{'OK ' if ok else 'FAIL'} {p.name:<10} {p.aspect:<7} 期望 {p.image} 实际 {got}  "
              f"{out.stat().st_size // 1024 if out.exists() else 0}KB  {out.name}")
        if not ok:
            bad += 1
    # 标题极长不能溢出：靠 fit_size 缩字号兜住
    out = render_set("x", "一" * 40, "", "", tmp, "t-long")
    ok = out.exists() and png_size(out) == platforms.PLATFORMS["x"].image
    print(f"{'OK ' if ok else 'FAIL'} 超长标题（40 字）仍渲染成功且尺寸正确 {png_size(out)}")
    if not ok:
        bad += 1

    # ★ logo 结构自证（2026-10-13 踩过）：曾用 translate 复刻路径画 logo，
    #   结果图上出现两个 logo 还压住页脚网址。渲染不报错、尺寸也对，
    #   纯靠肉眼看图发现 ⇒ 只能断言 SVG 里 logo 组只有一个。
    for key in platforms.ORDER:
        svg = build_svg(key, "主标", "副一", "副二")
        n_groups = svg.count("<g transform=")
        n_paths = svg.count("<path")
        ok = n_groups == 1 and n_paths == 2   # 一个 <g>，两条笔画
        print(f"{'OK ' if ok else 'FAIL'} {platforms.PLATFORMS[key].name:<10} "
              f"logo 组 {n_groups} 个 / path {n_paths} 条（期望 1 / 2）")
        if not ok:
            bad += 1

    print(f"自证产物在 {tmp}")
    print("SELF-TEST", "PASS" if bad == 0 else f"FAIL({bad})")
    raise SystemExit(1 if bad else 0)