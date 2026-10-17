#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
盲测页生成器 —— 造一个「可分享的翻译 A/B 投票实验」

为什么做这个（作战手册 E4 引擎）：
    翻译质量是永恒的吵架话题，天然可讨论、可参与。一个匿名盲测页——
    「同一份源文，三份译文，你投哪份最好，投完揭晓哪份是能言」——
    ① 有争议性 → 被人转发讨论
    ② 有参与感 → 人们截图自己投完的结果发出去
    ③ 赢了是一份可被反复引用的资产，输了是信任（前提是诚实）

★ 诚实口径（必须，否则反噬）：
    样本量、源文选材、模型版本、评估维度全部公开。
    如果某轮你输了，**照实公布**——认输一轮换来的信任，远超赢三轮。
    本脚本生成的页面已内置这些披露位，且 demo 数据明确标「示例，非真实评测」。

产物：frontend-react/public/blindtest/index.html（纯前端，投票用 localStorage，无需后端）

用法：
    python3 scripts/growth/blindtest_build.py            # 用内置 demo
    python3 scripts/growth/blindtest_build.py --cases path.json
    python3 scripts/growth/blindtest_build.py --selftest

零第三方依赖。
"""

from __future__ import annotations

import argparse
import html
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, os.pardir, os.pardir))
DEFAULT_OUT = os.path.join(ROOT, "frontend-react", "public", "blindtest", "index.html")

CSS = """
:root{--bg:#0A0B0D;--panel:#121417;--line:#2A2F3A;--fg:#E7E9EA;--dim:#8B939F;--accent:#E7E9EA}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);
font:16px/1.7 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"PingFang SC","Microsoft YaHei",sans-serif}
.wrap{max-width:860px;margin:0 auto;padding:0 20px}
header{border-bottom:1px solid var(--line);padding:18px 0;margin-bottom:30px}
header .wrap{display:flex;justify-content:space-between;align-items:center;gap:16px;flex-wrap:wrap}
.brand{font-weight:700}
nav a{color:var(--dim);text-decoration:none;margin-left:16px;font-size:14px}
h1{font-size:30px;letter-spacing:-.01em}
.lede{color:var(--dim);font-size:17px}
.source{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:18px 20px;margin:18px 0;white-space:pre-wrap}
.cards{display:grid;grid-template-columns:1fr 1fr 1fr;gap:14px;margin:20px 0}
@media(max-width:680px){.cards{grid-template-columns:1fr}}
.card{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:16px;cursor:pointer;transition:border-color .15s}
.card:hover{border-color:var(--dim)}
.card.sel{border-color:var(--fg)}
.card h3{margin:0 0 8px;font-size:15px}
.card p{margin:0;white-space:pre-wrap;font-size:14px;color:#cfd3d7}
.btn{background:var(--fg);color:#000;border:0;font-weight:600;padding:14px 22px;border-radius:10px;cursor:pointer;font-size:15px}
.btn:disabled{opacity:.4;cursor:not-allowed}
.reveal{margin:24px 0;padding:18px 20px;border:1px solid var(--line);border-radius:12px;display:none}
.reveal.show{display:block}
.disclose{font-size:12.5px;color:var(--dim);border-top:1px solid var(--line);margin-top:40px;padding-top:16px;line-height:1.6}
footer{border-top:1px solid var(--line);margin-top:40px;padding:24px 0;color:var(--dim);font-size:13px}
.note{color:var(--dim);font-size:13px;margin-top:8px}
"""

DEMO = {
    "title": "Blind translation test — which one reads best?",
    "lede": "Same source. Three anonymous translations (A/B/C). Pick the one that reads "
            "best, then we reveal which was 能言 LangCross. Honest disclosure below.",
    "source_lang": "Chinese (Simplified)",
    "source": "我们的产品已服务超过 120 个国家的客户，支持 12 种界面语言。"
              "我们把术语一致性当作交付质量的第一条：同一个品牌名，绝不在三份文件里出现三种译法。",
    "versions": [
        {"tag": "A", "model": "LangCross (this product)", "text":
         "Our product serves customers in over 120 countries and supports 12 interface languages. "
         "We treat terminology consistency as the first rule of delivery quality: the same brand name "
         "never appears with three different translations across three documents."},
        {"tag": "B", "model": "General-purpose LLM (cloud)", "text":
         "Our product has served customers in more than 120 countries, supporting 12 kinds of "
         "interface languages. We take terminology consistency as the number one quality of delivery: "
         "one brand name will not show up as three different translations in three files."},
        {"tag": "C", "model": "Machine translation (generic)", "text":
         "Our products have served customers in over 120 nations, support 12 interface language. "
         "We put terms consistency as first rule of delivering quality: same brand name not appear "
         "three translation in three documents."},
    ],
    "is_demo": True,
    "disclosure": {
        "sample_size": "n = 1 sample (demo only — not a real benchmark)",
        "source_selection": "Short product-blurb paragraph; chosen for brand-name + number consistency.",
        "model_versions": "Versions at time of build; pin them when you run a real round.",
        "criteria": "Reads best = natural flow + consistent terminology + correct number rendering.",
        "honesty": "If LangCross loses a real round, publish the loss. Trust > a streak.",
    },
}


def render(case: dict) -> str:
    # 把「哪份是能言」的索引抽出来，页面用 data 传入，JS 洗牌展示
    versions = case["versions"]
    langcross_idx = next((i for i, v in enumerate(versions) if "LangCross" in v.get("model", "")), 0)

    cards = []
    for v in versions:
        cards.append({
            "tag": v["tag"],
            "text": v["text"],
            "model": v["model"],
        })

    data = {
        "source": case["source"],
        "sourceLang": case.get("source_lang", ""),
        "cards": cards,
        "langcrossTag": versions[langcross_idx]["tag"],
        "isDemo": bool(case.get("is_demo", False)),
        "disclosure": case.get("disclosure", {}),
    }

    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{html.escape(case['title'])} | 能言 LangCross</title>
<meta name="description" content="A blind A/B/C translation test. Pick the best read, then see which was 能言 LangCross.">
<meta name="robots" content="index,follow">
<style>{CSS}</style>
</head>
<body>
<header><div class="wrap">
  <div class="brand">能言 LangCross</div>
  <nav><a href="https://langcross.lexicorn.cn/login">Try it</a>
  <a href="https://langcross.lexicorn.cn/pricing">Pricing</a></nav>
</div></header>
<main class="wrap">
  <h1>{html.escape(case['title'])}</h1>
  <p class="lede">{html.escape(case.get('lede',''))}</p>

  <div class="source"><strong>Source ({html.escape(data['sourceLang'])}):</strong>
\n{html.escape(data['source'])}</div>

  <div class="cards" id="cards"></div>
  <button class="btn" id="reveal" disabled>Reveal which was 能言</button>
  <p class="note" id="hint">Pick the one that reads best first.</p>

  <div class="reveal" id="revealBox"></div>

  <div class="disclose">
    <strong>Honest disclosure</strong><br>
    Sample size: {html.escape(str(data['disclosure'].get('sample_size','')))}<br>
    Source selection: {html.escape(str(data['disclosure'].get('source_selection','')))}<br>
    Model versions: {html.escape(str(data['disclosure'].get('model_versions','')))}<br>
    Criteria: {html.escape(str(data['disclosure'].get('criteria','')))}<br>
    {html.escape(str(data['disclosure'].get('honesty','')))}
  </div>
</main>

<footer><div class="wrap">能言 LangCross · This is a demonstration page, not a certified benchmark.</div></footer>

<script>
const DATA = {json.dumps(data, ensure_ascii=False)};
const KEY = 'blindtest_vote';
let sel = null;

const cardsEl = document.getElementById('cards');
const order = DATA.cards.slice();
// 洗牌，让 A/B/C 顺序每次不同（同一访客用 localStorage 固定，避免投票中途变）
let saved = null;
try {{ saved = JSON.parse(localStorage.getItem(KEY) || 'null'); }} catch(e) {{}}
if (saved && saved.order) {{
  order.sort((a,b)=> saved.order.indexOf(a.tag)-saved.order.indexOf(b.tag));
}} else {{
  for (let i=order.length-1;i>0;i--){{ const j=Math.floor(Math.random()*(i+1)); [order[i],order[j]]=[order[j],order[i]]; }}
  try {{ localStorage.setItem(KEY, JSON.stringify({{order: order.map(c=>c.tag)}})); }} catch(e){{}}
}}

order.forEach(c=>{{
  const d = document.createElement('div');
  d.className='card'; d.dataset.tag=c.tag;
  d.innerHTML = `<h3>Translation ${{c.tag}}</h3><p>${{c.text}}</p>`;
  d.onclick=()=>{{
    document.querySelectorAll('.card').forEach(x=>x.classList.remove('sel'));
    d.classList.add('sel'); sel=c.tag;
    document.getElementById('reveal').disabled=false;
    document.getElementById('hint').textContent='Now reveal which was 能言 LangCross.';
  }};
  cardsEl.appendChild(d);
}});

document.getElementById('reveal').onclick=()=>{{
  const box=document.getElementById('revealBox');
  const won = sel === DATA.langcrossTag;
  let html = `<h2>${{sel}} was 能言 LangCross — ${{won ? 'you picked ours ✓' : 'you picked a competitor'}}</h2>`;
  html += `<p class="note">Your pick: ${{sel}}. The 能言 LangCross version was ${{DATA.langcrossTag}}.</p>`;
  if (DATA.isDemo) html += `<p class="note">This is a demo sample (n=1), not a real benchmark.</p>`;
  box.innerHTML=html; box.classList.add('show');
}};
</script>
</body>
</html>
"""


def selftest() -> int:
    # 卡片是 JS 运行时注入的，所以 HTML 字符串里没有 "Translation A" 字面量；
    # 判据要查「静态骨架 + 关键标记」而不是运行时产物。
    html_out = render(DEMO)
    fail = []
    if 'id="cards"' not in html_out:
        fail.append("缺卡片容器（JS 注入目标）")
    if "Translation " not in html_out:
        fail.append("JS 卡片模板缺失（'Translation ' 字符串不在）")
    if "langcrossTag" not in html_out:
        fail.append("页面未暴露「哪份是能言」的标记（langcrossTag 缺失）")
    if "Honest disclosure" not in html_out:
        fail.append("缺诚实披露区块")
    # 诚实口径：demo 必须明标非真实评测，否则误导传播
    if "demo" not in json.dumps(DEMO).lower():
        fail.append("demo 数据未标记 is_demo")
    if fail:
        print("SELFTEST FAIL")
        for f in fail:
            print("  ✖ " + f)
        return 1
    print("SELFTEST PASS  (卡片容器 / 能言标记 / 诚实披露 全部到位)")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description="能言 盲测页生成器")
    ap.add_argument("--cases", default=None, help="JSON 评测用例（默认内置 demo）")
    ap.add_argument("--out", default=DEFAULT_OUT)
    ap.add_argument("--selftest", action="store_true")
    a = ap.parse_args()
    if a.selftest:
        return selftest()

    case = DEMO
    if a.cases:
        with open(a.cases, encoding="utf-8") as f:
            case = json.load(f)

    doc = render(case)
    os.makedirs(os.path.dirname(os.path.abspath(a.out)), exist_ok=True)
    with open(a.out, "w", encoding="utf-8") as f:
        f.write(doc)
    print(f"盲测页已生成 → {a.out}")
    print(f"  样本: {case.get('source_lang','')}, "
          f"{'demo' if case.get('is_demo') else '真实'} 用例 {len(case['versions'])} 份译文")
    print("  部署后分享链接即可。★ 输了一轮就照实公布测评结果，别只晒赢的。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
