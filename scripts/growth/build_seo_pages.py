#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
SEO / GEO 页面工厂 —— 批量生成长尾关键词静态页

输入：keyword_matrix.py 产出的 keywords.csv
输出：
    frontend-react/public/g/<slug>.html   静态页（随 vite build 进 dist）
    frontend-react/public/sitemap.xml
    frontend-react/public/robots.txt
    scripts/growth/out/build_report.md

为什么要程序化生成而不是手写：
    首批 100 页 × 每页 7 个必选区块，手写必然出现「某个区块漏了」且没人发现。
    程序化生成 + --check 校验，能把「结构完整性」变成可回归的断言。

★ 三个不能违反的口径（都来自本仓已踩过的坑）：
    1. 每个页面必须有**自己的内容**（不同场景写不同痛点与 FAQ）。
       只换关键词不换内容会被搜索引擎判 doorway page，整站降权。
       所以 SCENARIO_COPY 里每个场景都有独立的 pain/bullets/faq，
       新增场景必须补写，不许留占位（--check 会拦占位符）。
    2. 首屏必须有可用 CTA。SEO 页最常见的死法是一千字介绍 + 转化入口在页脚。
    3. 合规红线以《品牌一页纸》为准：零「100% 准确」「永不泄密」「有限公司」、
       零上游供应商名、零 token 裸值与汇率（全站积分口径）。

用法：
    python3 scripts/growth/build_seo_pages.py --limit 100
    python3 scripts/growth/build_seo_pages.py --check            # 只校验已生成的
    python3 scripts/growth/build_seo_pages.py --dry-run --limit 10
    python3 scripts/growth/build_seo_pages.py --selftest

零第三方依赖（仅标准库）。
"""

from __future__ import annotations

import argparse
import csv
import html
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, os.pardir, os.pardir))

DEFAULT_CSV = os.path.join(HERE, "out", "keywords.csv")
DEFAULT_OUT_DIR = os.path.join(ROOT, "frontend-react", "public")
DEFAULT_REPORT = os.path.join(HERE, "out", "build_report.md")

SITE = "https://langcross.lexicorn.cn"
BRAND = "能言 LangCross"
BRAND_EN = "LangCross"
PAGE_DIR = "g"

UI_LANGS = ["en", "zh", "zh_hant", "ru", "fr", "ar", "es", "pt", "de", "ja", "ko", "th"]

# ★ 占位符检测：新增场景必须补写真实文案，不许留 TODO/占位
PLACEHOLDER_RE = re.compile(r"\b(TODO|TBD|FIXME|占位|待补|XXX)\b", re.I)

# 合规禁用词（《品牌一页纸》红线）
BANNED_WORDS = [
    "100% accurate", "100% accuracy", "never leak", "no leak", "free unlimited",
    "unlimited free", "有限公司", "智能翻译平台", "翻译助手",
]
# 上游供应商与成本口径（一律不得出现）
BANNED_SUPPLIERS: list = []
COST_RE = re.compile(r"\b(tokens?|API key|per.token|汇率)\b", re.I)

LANG_NAME = {
    "zh": "Chinese", "en": "English", "ja": "Japanese", "ko": "Korean",
    "de": "German", "fr": "French", "es": "Spanish", "ru": "Russian",
    "pt": "Portuguese", "ar": "Arabic", "it": "Italian", "th": "Thai",
}
FMT_LABEL = {
    "pdf": "PDF", "docx": "Word", "pptx": "PowerPoint", "xlsx": "Excel",
    "srt": "SRT", "epub": "EPUB", "md": "Markdown",
}

# =============================================================================
# 场景文案 ——★ 每个场景独立撰写，禁止复用（doorway page 红线）
# 新增场景必须在这里补 pain / bullets(3) / faq(4)，且不得含占位符
# =============================================================================
SCENARIO_COPY = {
    "contract": {
        "pain": "A mistranslated clause is not a typo. It is a signed obligation you did not agree to.",
        "bullets": [
            "Defined terms stay defined — one term, one translation, across every page.",
            "Brand and entity names are protected from transliteration, so parties stay identifiable.",
            "You get the contract back as a contract: tables, numbering, signatures, annexes.",
        ],
        "faq": [
            ("Will the clause numbering survive translation?",
             "Yes. Structure is preserved during write-back, so numbering, tables and annex "
             "references stay where they were."),
            ("Can I force our own wording for key terms?",
             "Yes. Load your termbase as a knowledge package; when a source term matches, the "
             "required wording is enforced before delivery."),
            ("Is my contract text used to train anything?",
             "Your content is processed for your translation task and handled under the platform's "
             "data policy. Review the privacy terms for the current detail."),
            ("What if a clause still reads wrong?",
             "Quality signals are surfaced per ticket, and you can edit any segment in the "
             "bilingual editor, then re-export the file with your edits applied."),
        ],
    },
    "product-listing": {
        "pain": "Three marketplaces, three listings, one product name spelled three different ways.",
        "bullets": [
            "Product names and specs stay consistent across every language and every channel.",
            "Bulk translate the whole catalogue and download files in original format.",
            "Attribute tables and spec grids come back intact — no re-pasting into templates.",
        ],
        "faq": [
            ("Can I translate a whole catalogue at once?",
             "Yes. Upload the file or batch, choose target languages, and download per-language "
             "deliverables."),
            ("Will my product names be transliterated?",
             "Brand names are protected by the brand-name module, so they stay as you wrote them "
             "instead of being phonetically mangled."),
            ("Do spec tables survive?",
             "Table structure is preserved in write-back for the restore-file mode."),
            ("How is this priced?",
             "Points-based and estimable before you commit — the published estimator on the "
             "compare page uses the same formula as billing."),
        ],
    },
    "manual": {
        "pain": "The translated manual was fine. Rebuilding 80 pages of screenshots and callouts was not.",
        "bullets": [
            "Layout, headings, tables and image captions are written back into the original format.",
            "Recurring part names and warnings are translated the same way every time.",
            "Update one chapter later without re-doing the whole document.",
        ],
        "faq": [
            ("Do images and captions stay in place?",
             "Image positions and captions are preserved; caption text is translated."),
            ("Can I keep terminology consistent across a product line?",
             "Yes — a shared knowledge package enforces the same wording across every manual."),
            ("What formats are supported?",
             "DOCX, PPTX, XLSX and PDF support original-format write-back. Other formats are "
             "delivered as translated text."),
            ("Is there a quality check?",
             "Per-ticket quality signals are shown in the interface, with issues listed for review."),
        ],
    },
    "patent": {
        "pain": "In a patent translation, an inconsistent term can narrow a claim you did not mean to narrow.",
        "bullets": [
            "Enforce one translation per technical term across the whole specification.",
            "Structure preserved: claims numbering, references, and tables stay intact.",
            "Your terminology is yours — loaded as a knowledge package, not a public dictionary.",
        ],
        "faq": [
            ("How do you keep claim terms consistent?",
             "Terms in your knowledge package are enforced during translation; a mismatch is "
             "caught before delivery."),
            ("Is the claim structure preserved?",
             "Yes, numbering and cross-references are preserved through write-back."),
            ("Can I review before accepting?",
             "Yes — the bilingual editor lets you edit segments and re-export."),
            ("Do you replace legal review?",
             "No. This produces a working translation for review, not a certified legal filing."),
        ],
    },
    "academic-paper": {
        "pain": "Your references, equations and figure captions did not survive the round trip.",
        "bullets": [
            "Sections, citations and figure captions keep their places.",
            "Domain terms are translated consistently across the whole paper.",
            "Get a working draft you can edit, not a wall of text to reassemble.",
        ],
        "faq": [
            ("Are citations preserved?",
             "Citation markers and reference structure are preserved during write-back."),
            ("Can I control field-specific terminology?",
             "Yes, via a knowledge package or glossary you upload."),
            ("Is this journal-ready?",
             "It is a working translation for your own revision. Language polishing and "
             "journal formatting remain your step."),
            ("What file types work best?",
             "DOCX is the most reliable for papers; PDF write-back is supported with layout "
             "preservation."),
        ],
    },
    "game-localization": {
        "pain": "The UI strings came back translated. Then you spent two days re-inserting them into the table.",
        "bullets": [
            "Structured files keep their keys and columns — no manual re-insertion.",
            "Character and item names stay consistent across every string table.",
            "Placeholder and format markers are respected.",
        ],
        "faq": [
            ("Do you handle string tables?",
             "Structured formats are delivered as bilingual tables that preserve keys and columns."),
            ("Will placeholders like {0} survive?",
             "Yes, placeholders are respected rather than translated."),
            ("Can I enforce a style guide?",
             "Yes — style instructions can be applied and glossaries enforced."),
            ("What about very large batches?",
             "Batch file tasks are supported; watch the estimator for cost before committing."),
        ],
    },
    "marketing": {
        "pain": "The campaign launched in six countries. The slogan landed in three of them.",
        "bullets": [
            "Taglines and brand terms stay under your control instead of being re-coined per language.",
            "Brochure and deck layout preserved — slides stay slides.",
            "Reuse the same approved wording across every market.",
        ],
        "faq": [
            ("Can I protect our slogan from being translated?",
             "Yes. Brand and protected terms can be pinned so they are not re-coined."),
            ("Do slide layouts survive?",
             "PPTX write-back preserves layout."),
            ("Can different markets share one glossary?",
             "Yes, knowledge packages can be shared across departments and teams."),
            ("Is there human review?",
             "You can review and edit every segment; quality signals are shown per ticket."),
        ],
    },
    "subtitle": {
        "pain": "Timing was perfect until the translation came back and every line had to be re-pasted.",
        "bullets": [
            "Subtitle files are delivered as bilingual tables that keep cue order and timecodes.",
            "Speaker-consistent wording across a whole episode.",
            "Edit a line and re-export, instead of rebuilding the file.",
        ],
        "faq": [
            ("Are timecodes preserved?",
             "Yes — subtitle tasks deliver a bilingual table that preserves cue order and timecodes."),
            ("Can I edit before exporting?",
             "Yes, edit in the bilingual editor and re-export."),
            ("Do you translate speaker names?",
             "Names can be protected via the brand-name module."),
            ("What formats are supported?",
             "SRT and VTT are handled; structured outputs come back as bilingual tables."),
        ],
    },
    "resume": {
        "pain": "The translation was accurate. The one-column CV you got back was not your CV.",
        "bullets": [
            "Your CV comes back as a document with its layout intact.",
            "Job titles and company names handled consistently.",
            "Edit any line, then re-export the file.",
        ],
        "faq": [
            ("Will the layout survive?",
             "DOCX write-back preserves layout."),
            ("Should I translate a CV word for word?",
             "Usually not — role names often need market-appropriate equivalents. Edit after "
             "translation in the bilingual editor."),
            ("Can I keep company names untranslated?",
             "Yes, brand protection keeps them as written."),
            ("Is it free?",
             "New accounts can try before subscribing; ongoing use is points-based and "
             "estimable up front."),
        ],
    },
    "financial": {
        "pain": "Numbers were right. The note references and table headers were not.",
        "bullets": [
            "Tables, headers and note references keep their structure.",
            "Reporting terminology stays consistent across periods and languages.",
            "Delivered in the original format, ready for review.",
        ],
        "faq": [
            ("Are tables preserved?",
             "Yes, table structure is preserved in write-back."),
            ("Can I standardise reporting terms?",
             "Yes, via a shared knowledge package."),
            ("Is this suitable for filing?",
             "It is a working translation for internal review, not a certified filing."),
            ("How is cost estimated?",
             "The published estimator uses the same formula as billing; check the compare page."),
        ],
    },
}

# 格式型页面的文案（按格式区分，避免同质）
FORMAT_COPY = {
    "pdf": (
        "Most tools give you the text and leave you to rebuild the document.",
        [
            "You get a PDF back — tables, headers, footnotes and columns where they were.",
            "If layout write-back cannot be guaranteed, you still get the full translated text "
            "instead of a failed job.",
            "Terminology is enforced before delivery, not after.",
        ],
    ),
    "docx": (
        "A DOCX is not just text — it is styles, tables, headers and footnotes.",
        [
            "Styles, headings, tables and footnotes are preserved in write-back.",
            "Your glossary is enforced, so repeated terms come out identical.",
            "Edit any paragraph afterwards and re-export the file with edits applied.",
        ],
    ),
    "pptx": (
        "Slides are layout. Translating only the words gets you a deck you have to rebuild.",
        [
            "Slide layout, placeholders and speaker notes are preserved.",
            "Consistent wording across every slide and every deck in the series.",
            "Download the translated deck, not a text dump.",
        ],
    ),
    "xlsx": (
        "Spreadsheets break when translation ignores columns, sheets and formulas.",
        [
            "Sheets, columns and cell structure are preserved.",
            "Terms translated consistently across every sheet.",
            "Formulas and numeric cells are not scrambled by translation.",
        ],
    ),
    "srt": (
        "Subtitle translation fails at the last mile: re-inserting text into the cue file.",
        [
            "Delivered as a bilingual table that preserves cue order and timecodes.",
            "Consistent wording across an entire episode or series.",
            "Edit a line and re-export, rather than rebuilding the file.",
        ],
    ),
    "epub": (
        "E-books are structured. Flattening them to plain text loses chapters and TOC.",
        [
            "Text is extracted and delivered as translated Markdown or text.",
            "Chapter-level structure is respected during extraction.",
            "Good fit when you want the content, not a reflowed book.",
        ],
    ),
    "md": (
        "Markdown keeps its meaning through translation — or it should.",
        [
            "Headings, lists and code blocks keep their structure.",
            "Code and inline markers are not mangled.",
            "Delivered as a translated Markdown file.",
        ],
    ),
}

GENERIC_FAQ = [
    ("Is it free to try?",
     "You can translate a short sample before registering. Ongoing use is points-based and "
     "estimable before you commit."),
    ("How is the cost calculated?",
     "Points, estimated up front with the same formula used for billing. The compare page "
     "publishes that formula."),
    ("Do you keep my terminology?",
     "Yes. Upload a glossary or termbase as a knowledge package and the required wording is "
     "enforced before delivery."),
    ("Is my data used for training?",
     "Your content is processed for your translation task and handled under the platform's "
     "data policy. See the privacy terms for the current detail."),
]


# =============================================================================
# 文案闸门
# =============================================================================

def lint_text(t: str) -> list:
    """返回违规列表；空列表＝过闸。"""
    bad = []
    low = t.lower()
    for w in BANNED_WORDS:
        if w.lower() in low:
            bad.append(f"禁用词：{w}")
    for s in BANNED_SUPPLIERS:
        if s.lower() in low:
            bad.append(f"供应商名：{s}")
    m = COST_RE.search(t)
    if m:
        bad.append(f"成本口径泄漏：{m.group(0)}")
    if PLACEHOLDER_RE.search(t):
        bad.append("占位符未替换")
    return bad


def fit_len(s: str, n: int) -> str:
    """限长。★ 两个原则：
       ① 先丢品牌后缀，再砍正文——截断成「… | LangC…」比没有品牌后缀难看得多；
       ② 必须断在词边界，不能把单词劈开（实测出现过 "tables, he…" 这种产物）。
    """
    if len(s) <= n:
        return s
    for sep in (" | ", " — ", " - ", " · "):
        if sep in s:
            head = s.rsplit(sep, 1)[0]
            if len(head) <= n:
                return head
    cut = s[:n - 1]
    sp = cut.rfind(" ")
    if sp > n * 0.6:
        cut = cut[:sp]
    return cut.rstrip(" ,;—-|·") + "…"


# =============================================================================
# 页面渲染
# =============================================================================

CSS = """
:root{--bg:#0A0B0D;--panel:#121417;--raised:#1A1D21;--line:#2A2F3A;--line2:#3A404C;
--fg:#E7E9EA;--dim:#8B939F;--accent:#E7E9EA}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);
font:16px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,
"PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif}
a{color:var(--fg)}
.wrap{max-width:820px;margin:0 auto;padding:0 20px}
header{border-bottom:1px solid var(--line);padding:18px 0;margin-bottom:40px}
header .wrap{display:flex;align-items:center;justify-content:space-between;gap:16px;flex-wrap:wrap}
.brand{font-weight:700;letter-spacing:.02em}
nav a{margin-left:18px;color:var(--dim);text-decoration:none;font-size:14px}
nav a:hover{color:var(--fg)}
h1{font-size:34px;line-height:1.25;margin:0 0 14px;letter-spacing:-.01em}
h2{font-size:21px;margin:44px 0 12px}
.lede{font-size:18px;color:var(--dim);margin:0 0 26px}
.cta{display:block;background:var(--fg);color:#000;text-decoration:none;font-weight:600;
text-align:center;padding:15px 22px;border-radius:10px;margin:22px 0 10px}
.cta:hover{opacity:.9}
.sub{color:var(--dim);font-size:14px;text-align:center;margin:0 0 30px}
.panel{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:22px 24px}
ul{padding-left:20px}li{margin:7px 0}
.faq{border-top:1px solid var(--line);margin-top:12px}
.faq details{border-bottom:1px solid var(--line);padding:14px 0}
.faq summary{cursor:pointer;font-weight:600}
.faq p{color:var(--dim);margin:10px 0 0}
.note{font-size:13px;color:var(--dim);border-left:2px solid var(--line2);padding-left:14px;margin:26px 0}
footer{border-top:1px solid var(--line);margin-top:60px;padding:26px 0;color:var(--dim);font-size:13px}
@media(max-width:600px){h1{font-size:27px}}
"""

JSONLD_FAQ_TMPL = {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    "mainEntity": [],
}


def render_page(r: dict, hreflang_langs: list) -> str:
    slug = r["slug"]
    kw = r["keyword"]
    url = f"{SITE}/{PAGE_DIR}/{slug}.html"
    src = LANG_NAME.get(r["src"], r["src"])
    dst = LANG_NAME.get(r["dst"], r["dst"])
    utm = (f"utm_source=seo&utm_medium=organic_page&utm_campaign={slug}"
           f"&utm_content={r['page_type']}")

    ptype = r["page_type"]
    if ptype == "scenario":
        copy = SCENARIO_COPY.get(r["scenario"])
        if not copy:
            raise SystemExit(f"场景 {r['scenario']} 缺少文案块，请在 SCENARIO_COPY 补写")
        title = f"{kw.split(' translation ')[0].title()} Translation {src} to {dst} | {BRAND_EN}"
        lede = copy["pain"]
        bullets = copy["bullets"]
        faq = copy["faq"]
        h1 = f"{kw.split(' translation ')[0].title()} translation, {src} to {dst}"
    elif ptype == "guide":
        fmt = FMT_LABEL.get(r["fmt"], r["fmt"].upper())
        title = f"How to Translate {fmt} {src} to {dst} Without Losing Layout | {BRAND_EN}"
        lede = (f"Translating a {fmt} file from {src} to {dst} is easy. "
                f"Getting the layout back is the part that eats an afternoon.")
        bullets = [
            "Extract and translate with structure in mind, not as a wall of text.",
            "Write the translation back into the original format so tables, headers and "
            "footnotes stay put.",
            "Check terminology before delivery — one term, one translation, everywhere.",
        ]
        faq = GENERIC_FAQ
        h1 = f"How to translate {fmt} from {src} to {dst} without losing the layout"
    else:  # tool
        fmt = FMT_LABEL.get(r["fmt"], r["fmt"].upper())
        base = FORMAT_COPY.get(r["fmt"], FORMAT_COPY["docx"])
        title = f"Translate {fmt} {src} to {dst}"
        if r["intent"] == "L2":
            title += " — Layout Preserved"
        title += f" | {BRAND_EN}"
        lede = base[0]
        bullets = base[1]
        faq = GENERIC_FAQ
        h1 = f"Translate {fmt} from {src} to {dst}"
        if r["intent"] == "L2":
            h1 += ", layout preserved"

    title = fit_len(title, 60)
    # description 里不再拼 lede（拼上就必然超 155 被截断），只写「做什么 + 拿回什么 + 怎么试」
    desc = fit_len(
        f"Translate {src} to {dst} and get the file back in its original format — "
        f"tables, headings and footnotes intact. Try a sample before registering.", 155)

    # JSON-LD：FAQPage 是 AI 搜索（GEO）抓取的主要形态，必须有
    faq_ld = dict(JSONLD_FAQ_TMPL)
    faq_ld["mainEntity"] = [
        {"@type": "Question", "name": q,
         "acceptedAnswer": {"@type": "Answer", "text": a}}
        for q, a in faq
    ]
    app_ld = {
        "@context": "https://schema.org",
        "@type": "SoftwareApplication",
        "name": BRAND,
        "applicationCategory": "BusinessApplication",
        "operatingSystem": "Web",
        "description": "AI document translation with terminology enforcement and "
                       "original-format delivery.",
    }

    alt = []
    for lg in hreflang_langs:
        alt.append(f'  <link rel="alternate" hreflang="{lg}" href="{url}">')
    alt.append(f'  <link rel="alternate" hreflang="x-default" href="{url}">')

    faq_html = "\n".join(
        f"      <details><summary>{html.escape(q)}</summary><p>{html.escape(a)}</p></details>"
        for q, a in faq)

    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{html.escape(title)}</title>
<meta name="description" content="{html.escape(desc)}">
<link rel="canonical" href="{url}">
<meta property="og:type" content="website">
<meta property="og:title" content="{html.escape(title)}">
<meta property="og:description" content="{html.escape(desc)}">
<meta property="og:url" content="{url}">
<meta property="og:site_name" content="{BRAND}">
<meta name="twitter:card" content="summary_large_image">
<meta name="robots" content="index,follow">
{chr(10).join(alt)}
<script type="application/ld+json">
{json.dumps(faq_ld, ensure_ascii=False, indent=2)}
</script>
<script type="application/ld+json">
{json.dumps(app_ld, ensure_ascii=False, indent=2)}
</script>
<style>{CSS}</style>
</head>
<body>
<header><div class="wrap">
  <div class="brand">{BRAND}</div>
  <nav><a href="{SITE}/pricing?{utm}&utm_content=nav">Pricing</a>
  <a href="{SITE}/compare?{utm}&utm_content=nav">Compare</a>
  <a href="{SITE}/login?{utm}&utm_content=nav">Sign in</a></nav>
</div></header>

<main class="wrap">
  <h1>{html.escape(h1)}</h1>
  <p class="lede">{html.escape(lede)}</p>

  <a class="cta" href="{SITE}/login?{utm}&utm_content=hero&s={r['src']}&t={r['dst']}&fmt={r['fmt'] or r['scenario']}">
    Translate a {FMT_LABEL.get(r['fmt'], 'file') if r['fmt'] else 'file'} now — no signup to try</a>
  <p class="sub">Upload your file, get it back in the original format.</p>

  <div class="panel">
    <h2 style="margin-top:0">What you get back</h2>
    <ul>
{chr(10).join(f'      <li>{html.escape(b)}</li>' for b in bullets)}
    </ul>
  </div>

  <h2>Why this is different</h2>
  <p>Most translation tools hand you text and leave the reassembly to you. That is where the
  afternoon goes. {BRAND_EN} writes the translation back into your file, and enforces your
  terminology before delivery rather than after.</p>

  <p class="note">Cost is published, not hidden: the estimator on our compare page uses the
  same formula as billing, and you see the estimate before you commit.</p>

  <h2>Questions</h2>
  <div class="faq">
{faq_html}
  </div>

  <a class="cta" href="{SITE}/pricing?{utm}&utm_content=footer">See pricing</a>
</main>

<footer><div class="wrap">
  {BRAND} · <a href="{SITE}/pricing?{utm}&utm_content=foot">Pricing</a> ·
  <a href="{SITE}/compare?{utm}&utm_content=foot">Compare</a>
</div></footer>
</body>
</html>
"""


# =============================================================================
# sitemap / robots
# =============================================================================

def render_sitemap(slugs: list) -> str:
    urls = [f"{SITE}/", f"{SITE}/pricing", f"{SITE}/compare"]
    urls += [f"{SITE}/{PAGE_DIR}/{s}.html" for s in slugs]
    body = "\n".join(
        f"  <url><loc>{html.escape(u)}</loc></url>" for u in urls)
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
            f"{body}\n</urlset>\n")


def render_robots() -> str:
    return f"""User-agent: *
Allow: /
Disallow: /api/
Disallow: /admin
Disallow: /openapi/

Sitemap: {SITE}/sitemap.xml
"""


# =============================================================================
# 生成与校验
# =============================================================================

def read_rows(csv_path: str) -> list:
    if not os.path.exists(csv_path):
        raise SystemExit(
            f"找不到关键词表 {csv_path}\n先跑：python3 scripts/growth/keyword_matrix.py")
    with open(csv_path, encoding="utf-8") as f:
        return [r for r in csv.DictReader(f)]


def build(rows: list, out_dir: str, report_path: str, dry_run: bool,
          hreflang_langs: list) -> tuple:
    page_dir = os.path.join(out_dir, PAGE_DIR)
    problems: list = []
    written = []

    if not dry_run:
        os.makedirs(page_dir, exist_ok=True)

    for r in rows:
        slug = r["slug"]
        try:
            doc = render_page(r, hreflang_langs)
        except SystemExit:
            raise
        except Exception as e:                       # noqa: BLE001
            problems.append(f"{slug}: 渲染失败 {e}")
            continue

        # 闸门
        bad = lint_text(doc)
        if bad:
            problems.append(f"{slug}: " + "；".join(bad))
            continue

        # 结构断言（★ 首屏 CTA + canonical 自指 + title 长度）
        if f'<link rel="canonical" href="{SITE}/{PAGE_DIR}/{slug}.html">' not in doc:
            problems.append(f"{slug}: canonical 未自指")
            continue
        head, _, rest = doc.partition("</head>")
        if 'class="cta"' not in rest.split("<h2>")[0]:
            problems.append(f"{slug}: CTA 不在首屏（在第一个 h2 之后）")
            continue
        m = re.search(r"<title>(.*?)</title>", doc, re.S)
        if not m or len(m.group(1)) > 60:
            problems.append(f"{slug}: title 超长或缺失")
            continue
        m = re.search(r'name="description" content="(.*?)"', doc, re.S)
        if not m or len(m.group(1)) > 155:
            problems.append(f"{slug}: description 超长或缺失")
            continue

        if not dry_run:
            with open(os.path.join(page_dir, slug + ".html"), "w", encoding="utf-8") as f:
                f.write(doc)
        written.append(slug)

    if not dry_run:
        with open(os.path.join(out_dir, "sitemap.xml"), "w", encoding="utf-8") as f:
            f.write(render_sitemap(written))
        with open(os.path.join(out_dir, "robots.txt"), "w", encoding="utf-8") as f:
            f.write(render_robots())

    # 报告
    os.makedirs(os.path.dirname(os.path.abspath(report_path)), exist_ok=True)
    with open(report_path, "w", encoding="utf-8") as f:
        f.write("# SEO 页面生成报告\n\n")
        f.write(f"- 输入词条：{len(rows)}\n- 生成成功：**{len(written)}**\n")
        f.write(f"- 被拦下：**{len(problems)}**\n")
        f.write(f"- 输出目录：`{os.path.join(out_dir, PAGE_DIR)}`\n\n")
        if problems:
            f.write("## 被拦下的条目\n\n")
            for p in problems:
                f.write(f"- ✖ {p}\n")
        f.write("\n## 上线后必跑\n\n```\n"
                "bash scripts/growth/check_seo_live.sh --base " + SITE + "\n```\n")

    return written, problems


def check(out_dir: str) -> int:
    """校验已生成的页面（防「生成时报绿、上线后才发现结构缺失」）"""
    page_dir = os.path.join(out_dir, PAGE_DIR)
    if not os.path.isdir(page_dir):
        print(f"✖ 目录不存在：{page_dir}")
        return 1
    files = sorted(f for f in os.listdir(page_dir) if f.endswith(".html"))
    if not files:
        print(f"✖ {page_dir} 里没有 html")
        return 1

    fail = []
    for fn in files:
        p = os.path.join(page_dir, fn)
        doc = open(p, encoding="utf-8").read()
        slug = fn[:-5]
        if f'<link rel="canonical" href="{SITE}/{PAGE_DIR}/{slug}.html">' not in doc:
            fail.append(f"{fn}: canonical 未自指")
        if 'class="cta"' not in doc:
            fail.append(f"{fn}: 缺 CTA")
        if "FAQPage" not in doc:
            fail.append(f"{fn}: 缺 FAQPage 结构化数据（GEO 引用主力）")
        bad = lint_text(doc)
        if bad:
            fail.append(f"{fn}: " + "；".join(bad))
        m = re.search(r"<title>(.*?)</title>", doc, re.S)
        if not m or not (10 <= len(m.group(1)) <= 60):
            fail.append(f"{fn}: title 长度异常")

    # sitemap 与页面数必须一致
    sm = os.path.join(out_dir, "sitemap.xml")
    if not os.path.exists(sm):
        fail.append("缺 sitemap.xml")
    else:
        n = open(sm, encoding="utf-8").read().count(f"/{PAGE_DIR}/")
        if n != len(files):
            fail.append(f"sitemap 里 {n} 个 /{PAGE_DIR}/ URL，实际 {len(files)} 个页面")
    if not os.path.exists(os.path.join(out_dir, "robots.txt")):
        fail.append("缺 robots.txt")

    if fail:
        print(f"CHECK FAIL  ({len(files)} 页面，{len(fail)} 问题)")
        for f in fail[:25]:
            print("  ✖ " + f)
        return 1
    print(f"CHECK PASS  ({len(files)} 页面 / sitemap / robots 全部过闸)")
    return 0


def selftest() -> int:
    """自证：用一条真实词条跑完整链路，确认渲染与闸门都活着"""
    sys.path.insert(0, HERE)
    probe = {"slug": "translate-pdf-zh-to-en-preserving-formatting",
             "keyword": "translate pdf from Chinese to English preserving formatting",
             "intent": "L2", "page_type": "tool", "src": "zh", "dst": "en",
             "fmt": "pdf", "scenario": "", "volume_tier": "high",
             "competition_tier": "high", "fit": "format", "score": "22.5",
             "priority": "P0", "zh_hint": "x"}
    doc = render_page(probe, ["en"])
    fail = []
    if "<title>" not in doc:
        fail.append("无 title")
    if "FAQPage" not in doc:
        fail.append("无 FAQPage")
    if 'class="cta"' not in doc:
        fail.append("无 CTA")
    if lint_text(doc):
        fail.append("探针页自身没过合规闸门：" + "；".join(lint_text(doc)))

    # 三种形态都要能渲染
    for pt, extra in (("scenario", {"scenario": "contract", "fmt": ""}),
                      ("guide", {"fmt": "docx", "scenario": ""})):
        p2 = dict(probe, page_type=pt, **extra)
        try:
            d2 = render_page(p2, ["en"])
            if 'class="cta"' not in d2:
                fail.append(f"{pt} 形态缺 CTA")
        except Exception as e:                        # noqa: BLE001
            fail.append(f"{pt} 形态渲染失败：{e}")

    # 场景文案完整性：每个场景必须三件齐全且无占位
    for k, v in SCENARIO_COPY.items():
        if len(v.get("bullets", [])) < 3:
            fail.append(f"场景 {k} bullets 不足 3 条")
        if len(v.get("faq", [])) < 4:
            fail.append(f"场景 {k} FAQ 不足 4 条")
        if PLACEHOLDER_RE.search(json.dumps(v, ensure_ascii=False)):
            fail.append(f"场景 {k} 含占位符")

    # 闸门必须真的会响（拿一条脏文本自证，★ 否则「零违规」可能只是没跑）
    if not lint_text("We are 100% accurate and never leak your data."):
        fail.append("合规闸门对已知脏文本不报警——闸门没在工作")
    if not lint_text("costs 3000 tokens per run"):
        fail.append("成本口径闸门对已知脏文本不报警——闸门没在工作")
    if not lint_text("TODO: fill in"):
        fail.append("占位符闸门不报警")

    # 所有场景 code 都要在 SCENARIO_COPY 里有文案（否则渲染期才炸）
    from keyword_matrix import SCENARIOS  # type: ignore
    for code, *_ in SCENARIOS:
        if code not in SCENARIO_COPY:
            fail.append(f"场景 {code} 在矩阵里但 SCENARIO_COPY 没写文案")

    if fail:
        print("SELFTEST FAIL")
        for f in fail:
            print("  ✖ " + f)
        return 1
    print(f"SELFTEST PASS  (形态 3/3，场景文案 {len(SCENARIO_COPY)} 套，闸门 3/3 会响)")
    return 0


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="能言 SEO 页面工厂")
    ap.add_argument("--csv", default=DEFAULT_CSV)
    ap.add_argument("--out-dir", default=DEFAULT_OUT_DIR)
    ap.add_argument("--report", default=DEFAULT_REPORT)
    ap.add_argument("--limit", type=int, default=100)
    ap.add_argument("--priority", default="", help="只生成指定优先级，逗号分隔，如 P0")
    ap.add_argument("--hreflang", default="en",
                    help="★ 只填你真的生成了对应语种页面的语言。默认 en —— "
                         "乱写 hreflang 指向不存在的页面比不写更糟")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--check", action="store_true", help="只校验已生成的产物")
    ap.add_argument("--selftest", action="store_true")
    a = ap.parse_args(argv)

    if a.selftest:
        return selftest()
    if a.check:
        return check(a.out_dir)

    rows = read_rows(a.csv)
    if a.priority:
        want = set(x.strip() for x in a.priority.split(",") if x.strip())
        rows = [r for r in rows if r["priority"] in want]
    rows = rows[:a.limit]

    langs = [x.strip() for x in a.hreflang.split(",") if x.strip()]
    written, problems = build(rows, a.out_dir, a.report, a.dry_run, langs)

    print(f"页面工厂：{len(written)} 页生成，{len(problems)} 条被拦下"
          + ("（dry-run，未写盘）" if a.dry_run else ""))
    print(f"  目录   → {os.path.join(a.out_dir, PAGE_DIR)}")
    print(f"  报告   → {a.report}")
    for p in problems[:10]:
        print("  ✖ " + p)
    if not a.dry_run:
        print()
        print("★ 推上线后必须跑：bash scripts/growth/check_seo_live.sh "
              f"--base {SITE}")
    return 1 if (problems and not written) else 0


if __name__ == "__main__":
    sys.exit(main())
