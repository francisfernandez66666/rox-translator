# ============================================================================
# scripts/xbot/_make_preview.py — P3 预览页生成（2026-10-13）
# 读 out/pending/*.json 与 out/<date>/*.png，产出 品牌资产/xbot-模拟7天/预览.html
# 按天分组，一屏看完一周；每条带闸门结论与审阅三问。
# ============================================================================
from __future__ import annotations

import html
import json
import shutil
from pathlib import Path

BASE = Path(__file__).resolve().parent
DEST = BASE.parents[1] / "品牌资产" / "xbot-模拟7天"

QUESTIONS = [
    ("① 痛点是不是真的疼", "你自己看完第一句会不会想「对，就是这样」"),
    ("② 首句戳不戳得到", "首句里不能有「我 / 我们 / 我们的产品」"),
    ("③ 解药够不够", "至少两条解药，且落在降本增效上"),
]

CSS = """
:root{--bg:#000;--panel:#0E1014;--raised:#16181C;--bd:#424956;--bd-strong:#8B939F;
--text:#E7E9EA;--dim:#8B939F;--faint:#5A6270;--ok:#E7E9EA}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:'PingFang SC',-apple-system,sans-serif;padding:40px 24px;line-height:1.65}
.wrap{max-width:980px;margin:0 auto}
h1{font-size:26px;margin-bottom:6px}
.sub{color:var(--dim);font-size:13.5px;margin-bottom:32px}
.day{margin-bottom:44px}
.day h2{font-size:17px;padding-bottom:8px;border-bottom:1px solid var(--bd);margin-bottom:18px;display:flex;justify-content:space-between;align-items:baseline}
.day h2 span{color:var(--faint);font-size:12.5px;font-family:Menlo,monospace;font-weight:400}
.item{display:grid;grid-template-columns:1fr 460px;gap:22px;background:var(--panel);
border:1.2px solid var(--bd);border-radius:14px;padding:18px;margin-bottom:14px}
@media(max-width:900px){.item{grid-template-columns:1fr}}
.item img{width:100%;border-radius:8px;display:block}
.meta{font-size:11.5px;color:var(--faint);font-family:Menlo,monospace;margin-top:8px}
.text{white-space:pre-wrap;font-size:14.5px;margin:10px 0 12px}
.badge{display:inline-block;font-size:11px;padding:2px 9px;border-radius:999px;border:1px solid var(--bd);color:var(--dim);margin-right:8px}
.badge.pass{background:var(--text);color:#000;border-color:var(--text);font-weight:700}
.badge.fail{background:#F85149;color:#fff;border-color:#F85149;font-weight:700}
.q{margin-top:12px;padding-top:12px;border-top:1px solid var(--bd);font-size:12.5px;color:var(--dim)}
.q b{color:var(--text)}
summary{cursor:pointer;color:var(--dim);font-size:13px;margin-top:10px}
pre{background:var(--raised);border:1px solid var(--bd);border-radius:8px;padding:12px;font-size:12px;overflow:auto;margin-top:8px;color:var(--dim)}
"""


def main() -> int:
    DEST.mkdir(parents=True, exist_ok=True)
    (DEST / "图").mkdir(exist_ok=True)
    files = sorted((BASE / "out/pending").glob("*.json"))
    if not files:
        print("没有 pending 数据，先跑 _run_sim7d.sh")
        return 1

    total = passed = 0
    blocks = []
    for f in files:
        data = json.loads(f.read_text(encoding="utf-8"))
        date, day = data["date"], data.get("day", "?")
        rows = []
        for it in data["items"]:
            total += 1
            c = it.get("checks", {})
            ok = bool(c.get("ok"))
            passed += 1 if ok else 0
            src = BASE / it.get("image", "")
            # ★ 图必须带日期前缀再拷：7 天各有 img-1/2/3.png，同名会互相覆盖，
            #   结果 21 张图只剩 3 张（预览页还照样渲染，看起来一切正常）。
            img_rel = ""
            if src.exists():
                dst_name = f"{date}_{src.name}"
                shutil.copy2(src, DEST / "图" / dst_name)
                img_rel = f"图/{dst_name}"
            badge = ('<span class="badge pass">通过闸门</span>' if ok
                     else '<span class="badge fail">被闸门拦下</span>')
            body = html.escape(it["text"])
            rows.append(f"""    <div class="item">
      <div>
        {badge}<span class="badge">{html.escape(it['topic_id'])}</span><span class="badge">{html.escape(it['slot'])} {html.escape(it['angle'])}</span>
        <div class="text">{body}</div>
        <div class="meta">加权字符 {c.get('weighted_len','?')} / 280　·　与历史最大相似度 {c.get('max_similarity','?')}</div>
        <details><summary>审阅三问（点击展开）</summary><div class="q">
          {'<br>'.join(f'<b>{t}</b> — {d}' for t, d in QUESTIONS)}
        </div></details>
      </div>
      <div>{f'<img src="{img_rel}" alt="">' if img_rel else '<div class="meta">未渲染</div>'}
        <div class="meta">配图主标：{html.escape((it.get('image_text') or {}).get('main',''))}</div></div>
    </div>""")
        blocks.append(f"""  <div class="day">
    <h2>第 {day} 天 · {date}<span>3 条 / {' '.join(i['slot'] for i in data['items'])}</span></h2>
{chr(10).join(rows)}
  </div>""")

    doc = f"""<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8">
<title>能言 LangCross · 7 天模拟审阅</title><style>{CSS}</style></head><body><div class="wrap">
<h1>能言 LangCross · 7 天模拟审阅</h1>
<p class="sub">P3 模拟产物 · {total} 条文案 + {total} 张配图 · <b>全部未真实发布</b> ·
通过闸门 <b>{passed}/{total}</b> · 验收线：≥{int(total*0.81)} 条一次通过才准上线</p>
{chr(10).join(blocks)}
<details><summary>生成依据</summary><pre>选题池：X运营执行包_选题库与30天排期_20261013.md（§二 A类 / §三 B类 / §四 C类）
闸门：scripts/xbot/guard.py（合规禁用词 + 自嗨句式 + 加权字数 + 相似度）
选题 ID 不复用、相似度阈值 0.70、字数上限 280</pre></details>
</div></body></html>"""
    (DEST / "预览.html").write_text(doc, encoding="utf-8")
    print(f"预览页：{DEST/'预览.html'}")
    print(f"合计 {total} 条，通过 {passed}，拦下 {total-passed}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())