# ============================================================================
# scripts/xbot/prep_multi.py — 多平台备料入口（2026-10-13）
#
# ★ 边界（用户 2026-10-13 明确）：这个脚本只负责「产出物料」。
#   上传和发帖由人工完成——不接任何平台的发布 API，不做自动上传。
#   原因见 platforms.py 顶部：X 自动发被判 bot 代价是账号级的；
#   Instagram 只收 JPEG、TikTok 只收验证域名 URL 且未审核 App 强制私密。
#
# 做的事：
#   读 out/pending/<日期>.json（AI 按 prompts.md 写的当日文案）
#     → 按平台分别算长度（X 加权 / 其他平台常规字符）
#     → 配图：优先用 AI 场景图 images/s{seq}.*（一条一张，四平台共用）；
#       没有才按平台各出一张排版版式图；同时落 image_prompts.json 供生图步骤取用
#     → 产出一个多平台备料目录：文本文案 + 图片 + 一个审阅页
#
# 用法：
#   python3 scripts/xbot/prep_multi.py --date 2026-10-20
#   python3 scripts/xbot/prep_multi.py --date 2026-10-20 --platforms x,instagram
#   python3 scripts/xbot/prep_multi.py --date 2026-10-20 --open
# ============================================================================
from __future__ import annotations

import argparse
import html as htmlmod
import json
import re
import subprocess
import sys
from datetime import date as Date
from pathlib import Path

BASE = Path(__file__).resolve().parent
sys.path.insert(0, str(BASE))
import platforms  # noqa: E402
import render_multi  # noqa: E402
import layouts  # noqa: E402

OUT = BASE / "out"
PENDING = OUT / "pending"
PREP = OUT / "prep"

# ★ 版式不是纯装饰：每种版式适配一种内容类型，轮着来才不会像机器发的。
#   ⚠ 只推荐**纯排版**类版式（hero/bill/list/quote/story）—— 它们不含人物，
#     程序画得干净。凡是需要画人的（board）都已判定不合格，别再用。
LAYOUT_GUIDE = {
    "hero": "★ 默认。主标 + 副标，干净不费劲，最不容易出错。",
    "bill": "账单。把隐性成本（等待 / 返工 / 术语返修）列成一张单。纯排版，很好看。",
    "list": "清单。把「交稿前还要手工做的事」列出来，每条带勾选框。纯排版。",
    "quote": "金句。一句反直觉的话撑满画面。观点类才用，需要真的想清楚了。",
    "story": "时间线。四个瞬间排成竖轴。适合「等了很久」这类有时间线的内容。",
    "compare": "前后对比。左「人工」右「能言」。适合展示同一份文档的两种结局。",
    "chat": "对话体。把客户 / 同事的一句抱怨画成气泡。像真事，不像广告。",
    "board": "⚠ 已停用。程序画的人物是火柴人，试产一轮判定不合格，别用。",
}

CSS = """
:root{--bg:#000;--panel:#0E1014;--raised:#16181C;--bd:#424956;--text:#E7E9EA;
--dim:#8B939F;--faint:#5A6270;--warn:#D29922;--bad:#F85149}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:'PingFang SC',-apple-system,sans-serif;
padding:36px 20px;line-height:1.65;-webkit-font-smoothing:antialiased}
.wrap{max-width:1080px;margin:0 auto}
h1{font-size:24px;font-weight:700;margin-bottom:6px}
h2{font-size:17px;margin:32px 0 14px;padding-bottom:8px;border-bottom:1px solid var(--bd)}
.sub{color:var(--dim);font-size:13px;margin-bottom:8px}
.boundary{border:1px solid var(--bd);border-left:3px solid var(--warn);border-radius:8px;
padding:12px 14px;font-size:13px;color:var(--dim);margin:18px 0 26px}
.boundary b{color:var(--text)}
.pgrid{display:grid;gap:18px}
@media(min-width:860px){.pgrid{grid-template-columns:1fr 1fr}}
.plat{background:var(--panel);border:1.2px solid var(--bd);border-radius:14px;padding:16px}
.phead{display:flex;align-items:baseline;gap:10px;flex-wrap:wrap;margin-bottom:12px}
.pname{font-size:16px;font-weight:700}
.badge{display:inline-block;font-size:11.5px;padding:2px 10px;border-radius:999px;
border:1px solid var(--bd);color:var(--dim)}
.badge.ok{background:var(--text);color:#000;border-color:var(--text);font-weight:700}
.badge.warn{color:#000;background:var(--warn);border-color:var(--warn);font-weight:700}
.badge.bad{background:var(--bad);color:#fff;border-color:var(--bad);font-weight:700}
.item{border-top:1px solid var(--bd);padding-top:14px;margin-top:14px}
.item:first-of-type{border-top:none;padding-top:0;margin-top:0}
.text{white-space:pre-wrap;font-size:14px;background:var(--raised);border:1px solid var(--bd);
border-radius:8px;padding:11px;margin:9px 0}
.shots{display:flex;gap:10px;overflow-x:auto;padding:4px 0}
.shots img{height:150px;border-radius:6px;background:var(--raised);border:1px solid var(--bd);flex:none}
.tactic{font-size:12.5px;color:var(--dim);margin-top:8px}
.tactic b{color:var(--warn)}
.note{font-size:12px;color:var(--faint);margin-top:6px}
.problems{color:var(--bad);font-size:12.5px;margin-top:8px}
button{font-family:inherit;font-size:12.5px;padding:6px 13px;border-radius:8px;cursor:pointer;
background:var(--text);color:#000;border:1px solid var(--text);font-weight:700}
button.done{background:var(--ok,#E7E9EA)}
"""


def load_items(date: str) -> tuple[list[dict], Path]:
    src = PENDING / f"{date}.json"
    if not src.exists():
        print(f"FAIL  找不到 {src}", file=sys.stderr)
        print("  AI 应先按 prompts.md 生成 out/pending/<日期>.json，本脚本只做「成料」不做「想料」。",
              file=sys.stderr)
        raise SystemExit(1)
    data = json.loads(src.read_text(encoding="utf-8"))
    items = data.get("items") or []
    if not items:
        print(f"FAIL  {src} 里没有 items", file=sys.stderr)
        raise SystemExit(1)
    return items, src


def analyse(platform: str, items: list[dict]) -> list[dict]:
    """按平台算长度并跑平台规则，返回带 platform_checks 的条目。"""
    out = []
    for it in items:
        text = it.get("text", "")
        n = platforms.measure(platform, text)
        problems = platforms.check(platform, text)
        # ★ 反口号判据也接进来：prompts.md 里的故事五要素是给人看的，
        #   真正把关的是 storycheck.py（实测：照着 prompts.md 写的样例自己就超字数）。
        #   只在第一处报（避免每个平台重复报同一件事）。
        if platform == platforms.ORDER[0]:
            problems = problems + _story_problems(it)
        out.append({
            "seq": it.get("seq"),
            "slot": it.get("slot", ""),
            "angle": it.get("angle", ""),
            "topic_id": it.get("topic_id", ""),
            "text": text,
            "image_prompt": it.get("image_prompt", ""),
            "image_text": it.get("image_text") or {},
            "layout": (it.get("image_text") or {}).get("layout", "hero"),
            "len": n,
            "problems": problems,
        })
    return out


# ---------------------------------------------------------------------------
# AI 生图（2026-10-14）：配图的正式来源。
#   pending JSON 里每条写 image_prompt（英文场景提示词），生图步骤产出
#   images/s{seq}.png；这里只负责「找到就用、找不到就兜底排版图」，
#   以及把提示词落成 image_prompts.json 供生图步骤取用。
#   实际调用生图能力的是备料 agent（会话内），脚本自己不连任何生图 API。
AI_IMAGE_EXTS = (".png", ".jpg", ".jpeg", ".webp")


def _ai_image(dest: Path, seq) -> Path | None:
    for e in AI_IMAGE_EXTS:
        p = dest / "images" / f"s{seq}{e}"
        if p.exists():
            return p
    return None


def write_image_prompts(items: list[dict], dest: Path) -> int:
    """把每条的生图提示词落成清单；返回缺提示词的条数。"""
    rows, missing = [], []
    for it in items:
        prompt = (it.get("image_prompt") or "").strip()
        if prompt:
            rows.append({"seq": it.get("seq"), "slot": it.get("slot", ""),
                         "angle": it.get("angle", ""), "prompt": prompt})
        else:
            missing.append(it.get("seq"))
    (dest / "image_prompts.json").write_text(
        json.dumps(rows, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return len(missing)


def _story_problems(it: dict) -> list[str]:
    """跑反口号判据 + 分镜完整性校验，返回问题列表。"""
    import storycheck
    problems = []
    r = storycheck.check(it.get("text", ""), angle=it.get("angle", ""))
    if not r.ok:
        problems.extend(f"故事判据：{x}" for x in r.reasons)
    # 分镜完整性：board 版式必须有 2~4 格，且 scene 合法
    lay = (it.get("image_text") or {}).get("layout")
    if lay == "board":
        panels = (it.get("image_text") or {}).get("panels") or it.get("board") or []
        if len(panels) < 2:
            problems.append(f"分镜只有 {len(panels)} 格，至少 2 格才读得出过程")
        problems.extend(_panel_problems(panels))
    return problems


def _panel_problems(panels) -> list[str]:
    """分镜格的数据问题清单。

    ★ board 版式已停用（程序画的人物不好看），但老数据里可能还有
      board 条目 —— 这里保留校验是为了**明确告诉人"这条要改"**，
      而不是静默出一张丑图。
    """
    import sketch
    out = []
    for i, pan in enumerate(panels or [], 1):
        sc = (pan.get("scene") or "").strip()
        if sc not in sketch.SCENES:
            out.append(f"分镜第 {i} 格 scene={sc!r} 不存在"
                       f"（可用：{'/'.join(sketch.SCENES)}）")
    return out


def _svg_for(layout: str, platform: str, t: dict) -> str:
    """按版式生成 SVG。每个分支都是明确的字段约定，不要在渲染器里猜内容。"""
    main, sub1, sub2 = t.get("main", ""), t.get("sub1", ""), t.get("sub2", "")
    # ★ board（分镜）已停用：程序画的人物是火柴人，出来就是幼儿园水平。
    #   直接回退到 hero，别浪费渲染时间出图。
    if layout in layouts.DEPRECATED_LAYOUTS:
        print(f"    ! 版式 {layout} 已停用，回退 hero", file=sys.stderr)
        m, s1, s2 = _fallback_fields(layout, t)
        return render_multi.build_svg(platform, m, s1, s2)
    if layout == "story":
        return layouts.layout_story(platform, t.get("when", ""), t.get("event", ""),
                                    t.get("detail", ""), t.get("punch", ""))
    if layout == "compare":
        return layouts.layout_compare(platform, t.get("before", ""), t.get("after", ""))
    if layout == "list":
        return layouts.layout_list(platform, main, t.get("items") or [])
    if layout == "bill":
        return layouts.layout_bill(platform, t.get("items") or [], sub2, total=t.get("total"))
    if layout == "chat":
        return layouts.layout_chat(platform, main, t.get("brand") or "能言 LangCross")
    if layout == "quote":
        return layouts.layout_quote(platform, main, sub1 or t.get("brand", ""))
    # hero：未知版式也回退到它，而不是崩掉
    m, s1, s2 = _fallback_fields(layout, t)
    return render_multi.build_svg(platform, m, s1, s2)


def _fallback_fields(layout: str, t: dict) -> tuple[str, str, str]:
    """回退到 hero 时的主标/副标。

    ★ 不能只读 main —— compare 用 before/after、list/bill 用 items，
      它们本来就没有 main，直接取会传空串进 fit_size（已修但仍不该走到那）。
    """
    main = (t.get("main") or "").strip()
    if not main:
        if layout == "compare":
            main = (t.get("before") or "对照").split("\n")[0][:16]
        elif layout in ("list", "bill"):
            first = (t.get("items") or [""])
            main = first[0][0] if isinstance(first[0], (list, tuple)) else str(first[0])
            main = (main or "").split("\n")[0][:16]
        else:
            main = (t.get("event") or t.get("brand") or "")[:16] or "备料"
    return main, (t.get("sub1") or ""), (t.get("sub2") or "")


def _probe_size(p: Path) -> tuple[int, int]:
    """sips 实测像素尺寸。教训：文件大小正常 ≠ 尺寸正确，必须断言。"""
    out = subprocess.run(["sips", "-g", "pixelWidth", "-g", "pixelHeight", str(p)],
                         check=True, capture_output=True, text=True).stdout
    w = re.search(r"pixelWidth: (\d+)", out)
    h = re.search(r"pixelHeight: (\d+)", out)
    return (int(w.group(1)), int(h.group(1))) if w and h else (0, 0)


def _ai_to_platform(src: Path, dst: Path, fmt: str, target: tuple[int, int]) -> None:
    """AI 场景图（一张横图）→ 平台物料。

    规则：
    - JPEG 平台（IG/TikTok 只收 JPEG，PNG 被拒）必须转格式，硬约束；
    - 比例损失 ≤55% 就居中裁到平台尺寸（X 16:9 / IG 4:5 / FB 1.91:1 都在此列）；
      损失过大（TikTok 9:16 要裁掉 62% 宽）保原比例，交给 App 信箱化——
      裁到只剩中缝会毁构图；
    - 产出后实测像素尺寸断言。
    """
    fmt_flag = ["-s", "format", "jpeg"] if fmt.upper() == "JPEG" else ["-s", "format", "png"]
    w, h = _probe_size(src)
    tr = target[0] / target[1]
    sr = (w / h) if h else tr
    loss = 1 - min(sr, tr) / max(sr, tr)
    tmp = dst.with_name(dst.stem + ".tmp" + dst.suffix)
    subprocess.run(["sips"] + fmt_flag + ["-s", "formatOptions", "90",
                                          str(src), "--out", str(tmp)],
                   check=True, capture_output=True)
    if loss <= 0.55:
        if sr > tr:  # 源更宽 → 居中裁宽
            cw, ch = round(h * tr), h
        else:        # 源更高 → 居中裁高
            cw, ch = w, round(w / tr)
        subprocess.run(["sips", "-c", str(ch), str(cw), str(tmp), "--out", str(tmp)],
                       check=True, capture_output=True)
        subprocess.run(["sips", "--resampleHeightWidth", str(target[1]), str(target[0]),
                        str(tmp), "--out", str(tmp)], check=True, capture_output=True)
        ow, oh = _probe_size(tmp)
        if (ow, oh) != (target[0], target[1]):
            tmp.unlink(missing_ok=True)
            raise RuntimeError(f"裁剪后尺寸 {ow}×{oh} ≠ 目标 {target[0]}×{target[1]}")
        how = "居中裁"
    else:
        ow, oh = _probe_size(tmp)
        how = "保原比例（App 内信箱化）"
    tmp.replace(dst)
    print(f"    AI 图 → {dst.name} {ow}×{oh}（{how}）")


def render_platform(platform: str, items: list[dict], dest: Path, date: str) -> list[str]:
    """每个平台一套图。同一份内容在不同平台要不同尺寸。

    ★ 2026-10-14 起优先用 AI 生图（images/s{seq}.*，一条一张），
      按平台转格式/居中裁切后落到平台命名文件；没有 AI 图才回退排版版式 SVG。
    """
    p = platforms.PLATFORMS[platform]
    ext = p.fmt.lower().replace("jpeg", "jpg")
    files = []
    for it in items:
        t = it.get("image_text") or {}
        lay = it.get("layout", "hero")
        stem = f"{date}-s{it['seq']}-{platform}"
        ai = _ai_image(dest, it["seq"])
        if ai:
            out = dest / f"{stem}.{ext}"
            _ai_to_platform(ai, out, p.fmt, p.image)
            it["image"] = out.name
            it["image_ai"] = True
            files.append(out.name)
            continue
        it["image_ai"] = False
        out = dest / f"{stem}.{ext}"
        try:
            svg = _svg_for(lay, platform, t)
            render_multi.render_to_png(svg, out, target_width=p.image[0])
        except Exception as e:  # noqa: BLE001
            # 回退到 hero 版式：出不来图比出不来「有创意」的图重要
            print(f"  ✗ 第 {it['seq']} 条 {lay} 版渲染失败（{e}），回退 hero")
            m, s1, s2 = _fallback_fields(lay, t)
            out = render_multi.render_set(platform, m, s1, s2, dest, stem)
        files.append(out.name)
        it["image"] = out.name
    return files


def build_html(date: str, plats: list[str], per: dict[str, list[dict]],
               total_items: int, dest: Path) -> str:
    sections = []
    for pk in plats:
        p = platforms.PLATFORMS[pk]
        items = per[pk]
        bad = sum(1 for i in items if i["problems"])
        cls = "bad" if bad else "ok"
        cards = []
        for it in items:
            if it.get("image"):
                shots = (f'<img src="{htmlmod.escape(it["image"])}" '
                         f'alt="{p.name}版配图">')
            elif it.get("image_prompt"):
                shots = ('<div class="note">★ 尚未生图，image_prompt：</div>'
                         f'<div class="text">{htmlmod.escape(it["image_prompt"])}</div>')
            else:
                shots = ""
            lay = it.get("layout", "hero")
            src = ("AI 场景图" if it.get("image_ai")
                   else (f"排版兜底 · {lay}" if it.get("image") else "无图"))
            probs = ('<div class="problems">' + "；".join(htmlmod.escape(x) for x in it["problems"])
                     + "</div>") if it["problems"] else ""
            cards.append(f"""<div class="item">
  <div class="phead"><span class="badge">{it['seq']} · {htmlmod.escape(it['slot'])}</span>
  <span class="badge">{htmlmod.escape(it['angle'])}</span><span class="badge">{htmlmod.escape(it['topic_id'])}</span>
  <span class="badge">图源 {htmlmod.escape(src)}</span>
  <span class="badge">{it['len']} / {p.limit}</span></div>
  <div class="text">{htmlmod.escape(it['text'])}</div>
  <div>{shots}</div>{probs}
  <div class="note">版式说明：{htmlmod.escape(LAYOUT_GUIDE.get(lay, '—'))}</div></div>""")
        note = f'<div class="note">★ {htmlmod.escape(p.note)}</div>' if p.note else ""
        fold = (f'<div class="note">首屏折叠位置：约 {p.fold_at} 字符</div>' if p.fold_at else "")
        sections.append(f"""<div class="plat">
  <div class="phead"><span class="pname">{htmlmod.escape(p.name)}</span>
  <span class="badge">{p.aspect}</span><span class="badge">{p.image[0]}×{p.image[1]}</span>
  <span class="badge {cls}">{'有问题' if bad else '通过'}</span></div>
  <div class="tactic"><b>打法</b>：{htmlmod.escape(p.tactic)}</div>{note}{fold}
  {"".join(cards)}
</div>""")

    doc = f"""<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8">
<meta name="robots" content="noindex"><title>能言 LangCross · {htmlmod.escape(date)} 多平台备料</title>
<style>{CSS}</style></head><body><div class="wrap">
<h1>{htmlmod.escape(date)} 多平台备料</h1>
<p class="sub">{total_items} 条内容 × {len(plats)} 个平台　·　<b>全部未发布</b>　·　只出物料，不做上传</p>
<div class="boundary">
  <b>边界</b>：这个页面的产物是「文案 + 配图」，<b>上传和发帖由你手动完成</b>。<br>
  <b>图源</b>：标「AI 场景图」的每条一张生图，已按平台转格式（IG/TikTok 只收 JPEG）
  并居中裁到平台比例；TikTok 9:16 裁损过大，保原比例、App 会自动信箱化。
  标「排版兜底」的才是按平台尺寸现算的版式图。直接拖进对应 App 即可。
</div>
{"".join(sections)}
<div class="boundary" style="border-left-color:#8B939F">
  <b>人工发出去的顺序</b>：先贴文案 → 再拖图。反了会把文案覆盖掉。<br>
  <b>同一句话不必四个平台一字不改</b>：X 是短句打法，Facebook 可以写长。
  页面里的字数是上限内的校验，不是「必须写满」。
</div>
</div></body></html>"""
    (dest / "备料.html").write_text(doc, encoding="utf-8")
    return doc


def write_copy_files(date: str, plats: list[str], per: dict[str, list[dict]], dest: Path) -> None:
    """每个平台一个纯文本文件，方便直接全选复制。"""
    for pk in plats:
        p = platforms.PLATFORMS[pk]
        lines = [f"# {date} · {p.name}（{p.limit} 上限 · {p.aspect}）", ""]
        for it in per[pk]:
            lines += [f"## 第 {it['seq']} 条 · {it['slot']} · {it['angle']}　{it['len']}/{p.limit}"]
            if it["problems"]:
                lines.append("★ " + "；".join(it["problems"]))
            lines += ["", "```", it["text"], "```", ""]
            if it.get("image"):
                lines += [f"配图：{dest / it['image']}", ""]
        (dest / f"文案-{p.key}.md").write_text("\n".join(lines), encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser(description="多平台备料：按平台算长度 + 各出一张对应规格的图。不做任何上传或发布。")
    ap.add_argument("--date", default=Date.today().isoformat())
    ap.add_argument("--platforms", default=",".join(platforms.ORDER),
                    help="逗号分隔，默认全选。可选：" + ",".join(platforms.ORDER))
    ap.add_argument("--open", action="store_true")
    ap.add_argument("--no-render", action="store_true")
    args = ap.parse_args()
    date = args.date

    plats = [x.strip() for x in args.platforms.split(",") if x.strip()]
    unknown = [x for x in plats if x not in platforms.PLATFORMS]
    if unknown:
        print(f"FAIL  未知平台 {unknown}，可选：{platforms.ORDER}", file=sys.stderr)
        return 1

    items, _src = load_items(date)
    dest = PREP / date
    dest.mkdir(parents=True, exist_ok=True)

    missing_prompts = write_image_prompts(items, dest)
    if missing_prompts:
        print(f"! {missing_prompts} 条没有 image_prompt，这些条目将回退排版兜底图", file=sys.stderr)

    per: dict[str, list[dict]] = {}
    for pk in plats:
        per[pk] = analyse(pk, items)
        if not args.no_render:
            render_platform(pk, per[pk], dest, date)
        if pk != "x":
            continue
        # x 是基准：把它的长度写回原条目，兼容旧的 guard 口径
        for orig, a in zip(items, per[pk]):
            orig.setdefault("checks", {})["weighted_len"] = a["len"]

    build_html(date, plats, per, len(items), dest)
    write_copy_files(date, plats, per, dest)

    print(f"\n{'=' * 62}\n  {date} 多平台备料　{len(items)} 条 × {len(plats)} 平台\n{'=' * 62}")
    for pk in plats:
        p = platforms.PLATFORMS[pk]
        bad = sum(1 for i in per[pk] if i["problems"])
        print(f"\n  【{p.name}】{p.aspect} {p.image[0]}×{p.image[1]} · "
              f"{'有问题' if bad else '全部通过'}")
        for it in per[pk]:
            flag = "　".join(it["problems"]) if it["problems"] else ""
            print(f"    第 {it['seq']} 条 {it['len']}/{p.limit}　{flag}")
        if p.tactic:
            print(f"    打法：{p.tactic}")

    print(f"\n审阅页：{dest / '备料.html'}")
    print(f"生图提示词清单：{dest / 'image_prompts.json'}（生图步骤按 seq 取用）")
    ai_n = sum(1 for pk in plats for it in per[pk] if it.get("image_ai"))
    if ai_n:
        print(f"AI 场景图：{ai_n} 处使用 images/s*.png（一条一张，四平台共用）")
    for pk in plats:
        print(f"文案：{dest / f'文案-{pk}.md'}")
    print("\n下一步：你审完 → 自己上传到各平台发帖。回填 X 台账用 xbot.py mark。")
    if args.open:
        subprocess.run(["open", str(dest / "备料.html")], check=False)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())