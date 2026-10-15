# ============================================================================
# scripts/xbot/prep.py — 备料包（2026-10-13）
#
# ★ 为什么是「备料」而不是「自动发」：
#   手动发 3 条每天只要 5 分钟，边际成本近零；而自动发一旦被 X 判为 bot，
#   代价是账号级的（限流甚至封号），风险收益比完全不对称。
#   ⇒ 自动化的职责退到「决定发不发」之前：AI 想、程序画、程序闸，人来发。
#
# 做的事（一步到位，不碰 X API）：
#   读 out/pending/<日期>.json
#     → 三道闸门（当日三条互检 + 与历史台账比对）
#     → 过闸的渲染配图（程序渲染，AI 生图写字必错）
#     → 回写 checks 到 pending json
#     → 产出 out/prep/<日期>/备料.html  ← 人工审这个
#     → 产出 out/prep/<日期>/文案.md    ← 纯文本，方便直接复制
#
# 用法：
#   python3 scripts/xbot/prep.py --date 2026-10-20
#   python3 scripts/xbot/prep.py --date 2026-10-20 --open     # 顺手打开浏览器
#   python3 scripts/xbot/prep.py --date 2026-10-20 --print    # 只打终端，不出 HTML
#
# 发完之后必须回填台账，否则反重复闸门第二天就断链：
#   python3 scripts/xbot/xbot.py mark --date 2026-10-20 --seq 1 --tweet-id <id>
#   python3 scripts/xbot/xbot.py mark --date 2026-10-20 --seq 2 --skipped   # 审了不发
# ============================================================================
from __future__ import annotations

import argparse
import html as htmlmod
import json
import shutil
import subprocess
import sys
from datetime import date as Date
from pathlib import Path

BASE = Path(__file__).resolve().parent
sys.path.insert(0, str(BASE))
import guard  # noqa: E402
import render  # noqa: E402

OUT = BASE / "out"
PENDING = OUT / "pending"
PREP = OUT / "prep"
SLOTS = {"1": "10:00", "2": "17:00", "3": "22:00"}

# 审阅三问：贴在每条旁边，审的时候照着看，不要凭感觉说「还行」
REVIEW_QUESTIONS = [
    ("① 痛点是不是真的疼", "你自己看完第一句，会不会想「对，就是这样」。不疼就毙掉。"),
    ("② 首句戳不戳得到", "首句里不能有「我 / 我们 / 我们的产品」。有没有对方的损失？"),
    ("③ 解药够不够", "至少两条解药，且必须落在降本增效上，不是「功能很全」。"),
    ("④ 这话像不像人说的", "像不像一个真在踩坑的人写的。AI 味重就毙掉，别指望发出去能改。"),
]

CSS = """
:root{--bg:#000;--panel:#0E1014;--raised:#16181C;--bd:#424956;--bd-strong:#8B939F;
--text:#E7E9EA;--dim:#8B939F;--faint:#5A6270;--ok:#E7E9EA;--bad:#F85149}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:'PingFang SC',-apple-system,sans-serif;
padding:36px 20px;line-height:1.65;-webkit-font-smoothing:antialiased}
.wrap{max-width:1000px;margin:0 auto}
h1{font-size:24px;font-weight:700;margin-bottom:6px}
.sub{color:var(--dim);font-size:13px;margin-bottom:10px}
.warn{border:1px solid var(--bd);border-left:3px solid var(--bd-strong);border-radius:8px;
padding:12px 14px;font-size:13px;color:var(--dim);margin:18px 0 26px}
.warn b{color:var(--text)}
.warn ol{margin:6px 0 0 18px}
.warn li{margin:3px 0}
.sum{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:26px}
.badge{display:inline-block;font-size:11.5px;padding:2px 10px;border-radius:999px;
border:1px solid var(--bd);color:var(--dim)}
.badge.pass{background:var(--text);color:#000;border-color:var(--text);font-weight:700}
.badge.fail{background:var(--bad);color:#fff;border-color:var(--bad);font-weight:700}
.item{display:grid;grid-template-columns:1fr 430px;gap:20px;background:var(--panel);
border:1.2px solid var(--bd);border-radius:14px;padding:18px;margin-bottom:16px}
@media(max-width:900px){.item{grid-template-columns:1fr}}
.item img{width:100%;border-radius:8px;display:block;background:var(--raised)}
.text{white-space:pre-wrap;font-size:14.5px;margin:10px 0 12px;background:var(--raised);
border:1px solid var(--bd);border-radius:8px;padding:12px}
.meta{font-size:11.5px;color:var(--faint);font-family:Menlo,monospace;margin-top:8px}
.q{margin-top:12px;font-size:12.5px;color:var(--dim);border-top:1px solid var(--bd);padding-top:12px}
.q b{color:var(--text)}
.reasons{color:var(--bad);font-size:12.5px;margin-top:8px}
button{font-family:inherit;font-size:12.5px;padding:7px 14px;border-radius:8px;cursor:pointer;
background:var(--text);color:#000;border:1px solid var(--text);font-weight:700}
button:hover{opacity:.82}
button.done{background:var(--ok);color:#000}
.row{display:flex;align-items:center;gap:10px;flex-wrap:wrap}
details{margin-top:22px;font-size:13px;color:var(--dim)}
summary{cursor:pointer}
pre{background:var(--raised);border:1px solid var(--bd);border-radius:8px;padding:12px;
font-size:12px;overflow:auto;margin-top:8px;color:var(--dim);line-height:1.7}
"""


def check_items(items: list[dict], ledger: list[dict]) -> tuple[int, int]:
    """跑闸门并把结论写回每个 item。

    ★ 当日三条要互检：AI 一次生成 3 条，如果不互相比对，可能三条撞车而
      台账里一条历史都没有（第一天必踩）。做法是把当天已检的条目追加进
      临时 ledger，后面的条目就能查到前面的。
    """
    pool = list(ledger)
    passed = 0
    for it in items:
        r = guard.check(it["text"], topic_id=it.get("topic_id"), ledger=pool)
        it["checks"] = {
            "ok": r.ok,
            "compliance": r.compliance,
            "weighted_len": r.weighted_len,
            "first_sim": r.first_sim,
            "whole_sim": r.whole_sim,
            "max_similarity": r.max_similarity,
            "reasons": r.reasons,
        }
        if r.ok:
            passed += 1
        # 无论过没过都进池：被拦下的也要参与后续比对，否则第二条可以放心重复第一条的错误
        pool.append({"text": it["text"], "topic_id": it.get("topic_id"), "ts": "当日互检"})
    return passed, len(items)


def render_images(items: list[dict], date: str, dest: Path) -> None:
    """渲染当日配图到 dest/，并回写 item["image"]。"""
    for it in items:
        it_text = it.get("image_text") or {}
        if not it_text.get("main"):
            continue
        svg = render.build_svg(it_text.get("main", ""), it_text.get("sub1", ""), it_text.get("sub2", ""))
        name = f"{date}-img-{it['seq']}.png"
        # render.render_to_png 内部会断言实际像素尺寸（sharp density 会静默放大 5.33 倍）
        render.render_to_png(svg, dest / name)
        it["image"] = name
        fs = render.fit_size(it_text.get("main", ""))
        print(f"  图 {name}  主标「{it_text.get('main','')}」字号 {fs}")


def build_html(date: str, items: list[dict], passed: int, dest: Path) -> str:
    total = len(items)
    cards = []
    for it in items:
        c = it.get("checks", {})
        ok = bool(c.get("ok"))
        img_name = it.get("image", "")
        img_html = (f'<img src="{htmlmod.escape(img_name)}" alt="">' if img_name
                    else '<div class="meta">未渲染</div>')
        reasons = ""
        if not ok:
            reasons = '<div class="reasons">被闸门拦下：' + htmlmod.escape("；".join(c.get("reasons", []))) + "</div>"
        badge = ('<span class="badge pass">通过闸门</span>' if ok
                 else '<span class="badge fail">被闸门拦下</span>')
        # 一键复制：直接把文案放在 data 属性外的方式不安全（含换行），改用内嵌 script 常量
        copy_js = (
            f'<script>window.__TWEETS = window.__TWEETS || {{}};'
            f'window.__TWEETS[{it["seq"]}] = {json.dumps(it["text"], ensure_ascii=False)};</script>'
        )
        q = "".join(f'<div><b>{t}</b> — {d}</div>' for t, d in REVIEW_QUESTIONS)
        cards.append(f"""<div class="item">
  <div>
    <div class="row">{badge}<span class="badge">第 {it['seq']} 条 · 建议 {it.get('slot', SLOTS.get(str(it['seq']),''))}</span>
    <span class="badge">{htmlmod.escape(it.get('angle',''))}</span><span class="badge">{htmlmod.escape(it.get('topic_id',''))}</span></div>
    <div class="text">{htmlmod.escape(it['text'])}</div>
    <div class="row"><button onclick="cp(this,{it['seq']})">复制文案</button>
      <span class="meta">加权字符 {c.get('weighted_len','?')} / 280　·　首句相似度 {c.get('first_sim','?')}　·　全文 {c.get('whole_sim','?')}</span></div>
    {reasons}
    <div class="q">{q}</div>
  </div>
  <div>{img_html}<div class="meta">图 {htmlmod.escape(img_name)}　主标：{htmlmod.escape((it.get('image_text') or {}).get('main',''))}</div></div>
</div>
{copy_js}""")

    doc = f"""<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8">
<meta name="robots" content="noindex">
<title>能言 LangCross · {htmlmod.escape(date)} 备料审阅</title><style>{CSS}</style></head><body><div class="wrap">
<h1>{htmlmod.escape(date)} 备料审阅</h1>
<p class="sub">通过闸门 <b>{passed}/{total}</b>　·　<b>全部未发布</b>　·　由脚本 prep.py 生成，人工审完手动发</p>
<div class="warn">
  <b>审完怎么发（按这个顺序，X 上才不会出问题）</b>
  <ol>
    <li>逐条看左边文案 + 右边图。<b>不喜欢的直接毙掉</b>——毙掉的也要记一条 <code>--skipped</code>，否则明天 AI 会换个说法再生成一遍。</li>
    <li>先点「复制文案」，进 X 粘贴。</li>
    <li>再把右边那张图拖进去。<b>顺序别反</b>——先图后文案会把上一次编辑的文案覆盖掉。</li>
    <li>发完回填：<code>python3 scripts/xbot/xbot.py mark --date {date} --seq 1 --tweet-id &lt;地址栏里的数字&gt;</code></li>
  </ol>
</div>
<div class="sum"><span class="badge">10:00 返工</span><span class="badge">17:00 术语</span><span class="badge">22:00 成本</span></div>
{"".join(cards)}
<details><summary>生成依据</summary><pre>选题池：X运营执行包_选题库与30天排期_20261013.md（§二 A类 / §三 B类 / §四 C类）
闸门：scripts/xbot/guard.py（合规禁用词 + 自嗨句式 + 加权字数 ≤280 + 相似度）
相似度阈值：首句 0.70 判撞车、全文 0.85 判雷同（两个阈值各管各的，不要合并）
配图：程序渲染（render.py）1200×675，图上的中文一律不走 AI 生图</pre></details>
</div>
<script>
function cp(btn,seq){{
  var t = window.__TWEETS[seq];
  if(!t) return;
  var ta = document.createElement('textarea');
  ta.value = t; ta.style.position='fixed'; ta.style.opacity='0';
  document.body.appendChild(ta); ta.select();
  try {{ document.execCommand('copy'); }} catch(e) {{}}
  document.body.removeChild(ta);
  var old = btn.textContent;
  btn.textContent = '已复制 ' + seq + '　去 X 粘贴';
  btn.classList.add('done');
  setTimeout(function(){{ btn.textContent = old; btn.classList.remove('done'); }}, 2200);
}}
</script>
</div></body></html>"""
    (dest / "备料.html").write_text(doc, encoding="utf-8")
    return doc


def build_md(date: str, items: list[dict], passed: int, dest: Path) -> None:
    """纯文本版：不打开浏览器也能复制（比如在手机上审）。"""
    lines = [f"# {date} 备料（{passed}/{len(items)} 过闸门，未发布）", ""]
    for it in items:
        c = it.get("checks", {})
        flag = "通过" if c.get("ok") else "被拦下"
        lines += [
            f"## 第 {it['seq']} 条 · {it.get('slot','')} · {it.get('angle','')} · {it.get('topic_id','')}　[{flag}]",
            f"加权字符 {c.get('weighted_len','?')}/280　首句相似度 {c.get('first_sim','?')}",
        ]
        if not c.get("ok"):
            lines.append(f"★ 拦下原因：{'；'.join(c.get('reasons', []))}")
        lines += ["", "```", it["text"], "```", ""]
        img = it.get("image")
        if img:
            lines += [f"配图：{dest / img}", ""]
    lines += [
        "---",
        "发完回填台账（不发的话记 --skipped）：",
        f"python3 scripts/xbot/xbot.py mark --date {date} --seq 1 --tweet-id <id>",
        "",
    ]
    (dest / "文案.md").write_text("\n".join(lines), encoding="utf-8")


def print_terminal(date: str, items: list[dict], passed: int) -> None:
    """终端版：不开浏览器也能审。"""
    print(f"\n{'=' * 66}\n  {date} 备料　通过 {passed}/{len(items)}\n{'=' * 66}")
    for it in items:
        c = it.get("checks", {})
        ok = bool(c.get("ok"))
        print(f"\n── 第 {it['seq']} 条 · {it.get('slot','')} · {it.get('angle','')} · {it.get('topic_id','')}"
              f"　[{'通过' if ok else '被拦下'}]　加权 {c.get('weighted_len','?')}/280")
        if not ok:
            for r in c.get("reasons", []):
                print(f"   ★ {r}")
        for line in it["text"].split("\n"):
            print(f"   {line}")
        if it.get("image"):
            print(f"   配图：{PREP / date / it['image']}")
    print(f"\n{'=' * 66}")


def main() -> int:
    ap = argparse.ArgumentParser(description="把 pending 过闸门 + 渲染配图 → 产出人工审阅的备料包")
    ap.add_argument("--date", default=Date.today().isoformat(), help="YYYY-MM-DD，默认今天")
    ap.add_argument("--open", action="store_true", help="生成后用浏览器打开备料页")
    ap.add_argument("--print", dest="only_print", action="store_true", help="只打终端，不出 HTML/MD")
    ap.add_argument("--no-render", action="store_true", help="跳过配图渲染（只过闸门）")
    args = ap.parse_args()
    date = args.date

    src = PENDING / f"{date}.json"
    if not src.exists():
        print(f"FAIL  找不到 {src}", file=sys.stderr)
        print("  pending/<日期>.json 是 AI 按 prompts.md 生成的当日备料，没有它就无从备料。", file=sys.stderr)
        print(f"  生成后重跑：python3 scripts/xbot/prep.py --date {date}", file=sys.stderr)
        return 1
    data = json.loads(src.read_text(encoding="utf-8"))
    items = data.get("items") or []
    if not items:
        print(f"FAIL  {src} 里没有 items", file=sys.stderr)
        return 1

    dest = PREP / date
    dest.mkdir(parents=True, exist_ok=True)

    # ★ 先过闸再渲染：被拦下的条目不该浪费时间出图
    passed, total = check_items(items, guard.load_ledger())
    src.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")

    if not args.no_render:
        for it in items:
            if not it.get("checks", {}).get("ok"):
                print(f"  跳过图 {it['seq']}（被闸门拦下，出图没意义）")
        render_images([i for i in items if i.get("checks", {}).get("ok")], date, dest)

    if args.only_print:
        print_terminal(date, items, passed)
    else:
        build_html(date, items, passed, dest)
        build_md(date, items, passed, dest)
        print(f"\n备料包：{dest / '备料.html'}")
        print(f"纯文本：{dest / '文案.md'}")
        print_terminal(date, items, passed)
        if args.open:
            subprocess.run(["open", str(dest / "备料.html")], check=False)

    print(f"\n下一步：审 → 手动发 → 回填 mark --date {date} --seq <n> --tweet-id <id>")
    return 0 if passed == total else 2


if __name__ == "__main__":
    raise SystemExit(main())