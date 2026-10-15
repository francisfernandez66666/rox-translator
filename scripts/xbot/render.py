# ============================================================================
# scripts/xbot/render.py — 配图渲染（2026-10-13）
# 痛点主标 + 两行副标 → 1200×675 PNG（纯黑单色品牌体系）
#
# ★ 为什么是程序渲染而不是 AI 生图：
#   AI 生图的中文字形几乎必错、字体不可控、每天风格漂移。图上的中文一律走这里。
# ★ fit_size() 是必须的：固定字号排中文必然溢出被裁
#   （2026-10-13 真踩过：A 版裁掉「干」字、B 版整行没渲染）。
# ============================================================================
from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

BASE = Path(__file__).resolve().parent
W, H = 1200, 675
LEFT = 360
RIGHT_MARGIN = 72
AVAIL = W - LEFT - RIGHT_MARGIN  # 768

# 品牌色值（与 品牌资产/export_banner.js 同一套模板）
BG = "#000000"
FG = "#FFFFFF"
SUB = "#8B939F"
FAINT = "#6E7683"

LOGO_PATHS = """
  <g transform="translate(60,187.5) scale(10)">
    <path d="M9.5 10.5v5.2a4.3 4.3 0 0 0 8.6 0V12" stroke="#FFFFFF" stroke-width="2.4" fill="none" stroke-linecap="round"/>
    <path d="M20.5 19.5v-5.2a4.3 4.3 0 0 0-8.6 0V18" stroke="#FFFFFF" stroke-width="2.4" fill="none" stroke-linecap="round" opacity=".5"/>
  </g>"""


def fit_size(text: str, max_width: float = AVAIL, max_size: int = 82) -> int:
    """按字宽反算字号：全角 1.0em、半角 0.55em。缺了它中文标题必被裁。"""
    em = 0.0
    for ch in text:
        cp = ord(ch)
        em += 1.0 if (0x2E80 <= cp <= 0x9FFF or 0xFF00 <= cp <= 0xFF60 or 0x3000 <= cp <= 0x303F) else 0.55
    return int(min(max_size, max(20, max_width / em)))


def build_svg(main: str, sub1: str, sub2: str) -> str:
    fs_main = fit_size(main, AVAIL, 82)
    fs_sub = fit_size(sub1, AVAIL, 36) if sub1 else 36
    esc = lambda s: s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")  # noqa: E731
    rows = [
        f'<text x="{LEFT}" y="290" font-family="\'PingFang SC\',\'Hiragino Sans GB\',sans-serif" '
        f'font-size="{fs_main}" font-weight="700" fill="{FG}">{esc(main)}</text>'
    ]
    y = 372
    for s in (sub1, sub2):
        if not s:
            continue
        rows.append(
            f'<text x="{LEFT}" y="{y}" font-family="\'PingFang SC\',\'Hiragino Sans GB\',sans-serif" '
            f'font-size="{fit_size(s, AVAIL, 36)}" fill="{SUB}">{esc(s)}</text>'
        )
        y += 64
    rows.append(
        f'<text x="{LEFT}" y="560" font-family="\'PingFang SC\',sans-serif" font-size="26" fill="{FG}">能言 LangCross</text>'
    )
    rows.append(
        f'<text x="640" y="560" font-family="Menlo,monospace" font-size="26" fill="{FAINT}">langcross.lexicorn.cn</text>'
    )
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}">
<rect width="{W}" height="{H}" fill="{BG}"/>
<rect x="1" y="1" width="{W-2}" height="{H-2}" fill="none" stroke="#2A2F3A" stroke-width="2" rx="18"/>
{LOGO_PATHS}
{chr(10).join(rows)}
</svg>"""


def render_to_png(svg: str, out_path: Path) -> None:
    out_path.parent.mkdir(parents=True, exist_ok=True)
    tmp_svg = out_path.with_suffix(".svg")
    tmp_svg.write_text(svg, encoding="utf-8")
    # ★ 传 target_width：sharp 的 density=384 会让输出静默放大 5.33 倍
    #   （声明 1200×675 实际出 6400×3600），文件更大所以体积检查照样过，
    #   平台尺寸上限却被突破。这类 bug 只有断言像素尺寸才抓得到（2026-10-13 实测）。
    cmd = ["node", str(BASE / "_rasterize.js"), str(tmp_svg), str(out_path), str(W)]
    r = subprocess.run(cmd, capture_output=True, text=True)
    tmp_svg.unlink(missing_ok=True)
    if r.returncode != 0:
        raise RuntimeError(f"渲染失败 {out_path.name}: {r.stderr.strip()}")
    if r.stdout.strip():
        got = r.stdout.strip().splitlines()[-1]
        if got != f"{W}x{H}":
            raise RuntimeError(f"尺寸不符：{out_path.name} 实际 {got}，期望 {W}x{H}")


def main() -> int:
    """用法：render.py --date 2026-10-20  读 out/pending/<date>.json 渲染 3 张图"""
    import argparse
    ap = argparse.ArgumentParser()
    ap.add_argument("--date", required=True)
    args = ap.parse_args()
    p = BASE / "out" / "pending" / f"{args.date}.json"
    if not p.exists():
        print(f"找不到 {p}", file=sys.stderr)
        return 1
    data = json.loads(p.read_text(encoding="utf-8"))
    outdir = BASE / "out" / args.date
    for item in data.get("items", []):
        it = item.get("image_text") or {}
        svg = build_svg(it.get("main", ""), it.get("sub1", ""), it.get("sub2", ""))
        name = f"img-{item['seq']}.png"
        render_to_png(svg, outdir / name)
        item["image"] = f"out/{args.date}/{name}"
        print(f"OK  {name}  主标「{it.get('main','')}」字号 {fit_size(it.get('main',''))}")
    p.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"已更新 {p}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())