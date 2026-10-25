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
# ★ 品牌英文名唯一事实源＝keyword_matrix.py（矩阵生成的 "{c} vs LangCross" 措辞
#   与这里 page title 的后缀必须同源；两处各写一份迟早长出「vs 一个不存在的名字」）。
#   被其它目录的测试 import 时 sys.path 里不一定有本目录，先把 HERE 放进去再取。
if HERE not in sys.path:
    sys.path.insert(0, HERE)
from keyword_matrix import BRAND_EN  # type: ignore # noqa: E402
PAGE_DIR = "g"

# ★ 社交分享卡（og:image，方案 §2.2 承诺项，2026-10-25 收口）
#   图由 `scripts/growth/build_og_image.py` 生成并**随仓库落盘**（PNG 里没有文本，
#   文案红线在那一个脚本里过，不在这里）。这里只负责两件事：
#   ① URL 必须是**绝对地址**——相对路径在抓取器眼里等于没有图；
#   ② 磁盘上必须真有这个文件——`check()` 会查，「meta 指着 404」是这一族最坏的静默形态
#      （分享出去没卡片，而页面本身零症状零报错）。
#   ★ 路径常量从生成脚本 import，不在这再写一遍字面量：两处各写一份迟早长出
#     「图改名叫 og/brand.png、106 页还指向 /og-image.png」那一形态。
from build_og_image import OG_IMAGE_PATH  # type: ignore # noqa: E402

OG_IMAGE_URL = SITE + OG_IMAGE_PATH
OG_IMAGE_W, OG_IMAGE_H = 1200, 630

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

# ★ 贬低对手的口径（2026-10-24 竞品页族立）：竞品名可以出现，踩竞品的一句话不许出现。
#   三条理由，任一成立都够：① 公开页里的贬低句是法务与口碑的长期负债；
#   ② Google 的 helpful-content / reviews 判定会把「踩对手抬自己」的对比页整族降权，
#      也就是**产品口径对了、排名照样没有**；③ 这一族的差异点本来就够硬，不需要靠对方难看。
#   ★ 匹配按**词边界**（英文条目）而不是子串：子串匹配下「the cheaper answer」——
#     一句说自家定价的中性英语——会被 "cheap" 误杀，误伤的代价是整族 100 页长期红灯、
#     最后被人把闸门摘掉，比没有闸门更坏。中文条目没有词边界可用，仍按子串。
#   名单宁窄不宽；落地前已实测现存 100 页零命中（新增页族不许把闸门读成历史缺陷）。
DISPARAGE_WORDS = [
    "worse", "worst", "inferior", "inaccurate", "unreliable", "terrible", "awful",
    "useless", "sucks", "bad at", "poor quality", "low quality", "fails to",
    "can't handle", "cannot handle", "cheap", "scam", "rip off",
    "不如", "太差", "不准确", "垃圾",
]
_HAS_ASCII = re.compile(r"[A-Za-z]")

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
# ★ 竞品对标页文案（2026-10-24 用户令「和竞品做对标，作出差异性来，突出我们」）
#
# 键 = f"{竞品 code}-{角度 code}"，与 keyword_matrix.py 的 L5 行一一对应
# （code 来自 COMPETITORS / VS_ANGLES，不在这里再拼一次字符串）。
#
# 三条文案纪律，改这一族之前必读：
#   ① **不贬低对手**：每页必须先给一句诚实句（「只翻一段通用短文它就够了，我们不保证单句赢」），
#      差异点只写「整件事办完」那一层。这条不是客气——公开页里的贬低句是法务与口碑的
#      长期负债，而且 Google 的 helpful-content 判定会把「踩对手抬自己」的 doorway 文整族降权。
#      lint_text 里配了 DISPARAGE_WORDS 机械拦截（首跑真拦下过一句「is worse at」）。
#   ② **不编事实**：不写对手的价格、额度、隐私条款、市占率——本站没有可查证的来源。
#      能写的只有我们自己的交付形态（原格式写回、术语硬闸、积分预估、SSO/预算/审计/OpenAPI+SDK）。
#   ③ **六页不许同质**：每套 pain/bullets/faq 各自成文；selftest 里有一条 pairwise
#      唯一性腿，两页共用同一句 bullet 或同一条 FAQ 即判红——
#      「换个竞品名重跑一遍」正是 doorway page manual action 的教科书写法。
#
# 语种口径：**只有英文**。搜 "deepl alternative" 的人打在英文 SERP 上，页面也只在英文面出；
#   十二语种的竞品口径走的是 route_meta.json 的 description（另一处、另一条链）。
# =============================================================================

VS_COPY = {
    "deepl-alternative": {
        "title": "DeepL Alternative That Returns Your File | LangCross",
        "desc": "DeepL returns translated text; LangCross returns your file with the translation written back — layout intact, glossary enforced, cost estimated first.",
        "h1": "A DeepL alternative for teams whose deliverable is a file, not a paragraph",
        "lede": "DeepL gives you excellent sentences. The question nobody asks is what you "
                "do with the ten thousand sentences — and with the layout they arrived in.",
        "honest": "DeepL writes fluent prose — for a single paragraph of general foreign "
                  "text it is enough, and we do not claim to beat it on that. The difference "
                  "shows up on a document: what you download, and whether your terminology "
                  "survived the trip.",
        "bullets": [
            "The deliverable is the original file with the translation written back into it — "
            "tables, headers, footnotes and columns stay where they were.",
            "A glossary is a gate, not a suggestion: required wording is enforced before "
            "delivery and the same term never comes out two ways.",
            "Cost is points-based and estimated before the job runs, with the same formula "
            "billing uses.",
        ],
        "faq": [
            ("What does an alternative need to do that a text translator does not?",
             "Take the file apart and put it back together. Translating a DOCX into a text "
             "box is the easy half; returning a DOCX is the part that saves the afternoon."),
            ("Can I bring my own terminology?",
             "Yes — upload a glossary or termbase as a knowledge package. Entries are applied "
             "before delivery, so a term is decided once instead of per paragraph."),
            ("Is there something to compare before committing?",
             "Translate a sample document. Layout write-back and terminology either show up in "
             "the downloaded file or they do not; you do not have to take a word for it."),
            ("What does it cost?",
             "Points, estimated up front. The public estimator uses the same formula as "
             "billing, so the number you see before the job is the number that gets charged."),
        ],
    },
    "google-translate-alternative": {
        "title": "Google Translate Alternative: Layout Stays Put | LangCross",
        "desc": "Fast and free for a sentence. For a deliverable: your PDF or Office file comes back translated in its original layout, with terminology applied up front.",
        "h1": "A Google Translate alternative when the output has to be a deliverable",
        "lede": "Free, instant, everywhere — and it stops at the sentence. Once your work "
                "starts as a file and ends as a file, the translated words are only the first part.",
        "honest": "For a quick read of general text Google Translate is enough, and we do not "
                  "claim to beat it on a single sentence. Where the two products part is after "
                  "the sentence: the file, the glossary, and who is accountable for the output.",
        "bullets": [
            "Files come back as files: PDF, DOCX, PPTX and XLSX re-assembled in the original "
            "layout rather than pasted back by hand.",
            "Domain dictionaries and translation memory keep repeated wording identical across "
            "documents and across months.",
            "Team controls on the same account: single sign-on, per-department budgets, "
            "an audit trail, and an OpenAPI plus SDK when you want it inside your own system.",
        ],
        "faq": [
            ("Why not paste the text into a free translator?",
             "Because the paste is the small part. Rebuilding a slide deck, a spec sheet or a "
             "contract after translation is the labor nobody puts in the price."),
            ("Does terminology carry across documents?",
             "Yes. A knowledge package plus translation memory means a term decided in March is "
             "still the same term in September, on every file your team uploads."),
            ("Can it be called from our own systems?",
             "There is an OpenAPI and SDK for that, with the same points accounting as the "
             "interface — so usage stays in one budget instead of a second one."),
            ("How do we try it on a real file?",
             "Upload one document you would actually ship. Compare the download against the "
             "upload; layout and terminology are visible in the file itself."),
        ],
    },
    "deepl-vs": {
        "title": "DeepL vs LangCross: Where the Difference Shows | LangCross",
        "desc": "DeepL vs LangCross, judged on what you download: translated text, or your own document with the translation written back into its original layout.",
        "h1": "DeepL vs LangCross — decided by what you download, not by one sentence",
        "lede": "Comparing sentence quality is the least useful thing you can do with two "
                "tools that deliver different artifacts. Here is the split we would use.",
        "honest": "DeepL is strong at prose and we will not pretend a single general sentence "
                  "is where this contest is won. The honest comparison is scope: one tool "
                  "returns translated text, the other returns your file with the text in it.",
        "bullets": [
            "Delivery: translated text versus your original document rewritten in place — "
            "styles, tables and footnotes included.",
            "Control: glossary as an enforced gate before delivery, plus per-department "
            "budgets, SSO and an audit trail on the same account.",
            "Money: points per job on our side, estimated before the job runs, with the "
            "formula published on the compare page.",
        ],
        "faq": [
            ("Which one is better for documents?",
             "Ask a different question: which one gives you back a document. That is the "
             "branch this comparison actually turns on."),
            ("Is the difference visible in a test?",
             "Yes, in about two minutes: upload a table-heavy page to both and open the "
             "downloads side by side. Nothing here requires believing a claim."),
            ("Do you handle terminology differently?",
             "Terminology is applied before delivery as a constraint, not offered as a "
             "post-edit suggestion, so a term does not drift between paragraphs."),
            ("Should a team pay for both?",
             "Some do. If most of your work is a paragraph you are reading, not shipping, "
             "a general translator is the cheaper answer; if most of it is a file, it is not."),
        ],
    },
    "google-translate-vs": {
        "title": "Google Translate vs LangCross: Where It Differs | LangCross",
        "desc": "These are different jobs. One gives you a sentence to read; the other gives you a file to ship, with terminology enforced and the cost shown beforehand.",
        "h1": "Google Translate vs LangCross — free sentence, or finished file",
        "lede": "These are not two versions of the same product, and a feature grid that "
                "pretends otherwise helps nobody. Here is what each one is actually for.",
        "honest": "Google Translate wins on cost, speed and availability, and for getting the "
                  "gist of general text it is the right tool. The comparison only becomes "
                  "useful once the work involves your own file, your own terms and someone "
                  "accountable for the deliverable.",
        "bullets": [
            "Scope: an ad-hoc translator in a browser versus a pipeline that takes a file in "
            "and hands the same file back translated.",
            "Consistency: domain dictionaries and translation memory make repeated wording "
            "identical across every upload and every quarter.",
            "Operations: SSO, department budgets, audit trail and an OpenAPI plus SDK — "
            "the parts that let a team run it rather than an individual.",
        ],
        "faq": [
            ("Is this a replacement for a free translator?",
             "Not for reading something quickly. It is for producing something you can send, "
             "where the reassembly cost is inside the product instead of inside your evening."),
            ("What happens to the layout?",
             "It is the deliverable: PDF and Office files are rebuilt with the translated text "
             "in place, and when write-back cannot be guaranteed you still get the full text."),
            ("How is billing different?",
             "Points per job, estimated before the job starts, with the estimator and the "
             "invoice using the same formula — so no line appears after the fact."),
            ("When is the free option the right answer?",
             "When nobody downstream has to open the file. The cost of a free translation is "
             "the hour of reformatting that follows it, and that hour is not free."),
        ],
    },
    "deepl-document": {
        "title": "DeepL Document Translation: Layout Written Back | LangCross",
        "desc": "Document translation is extract, translate, reassemble. LangCross writes the translation back into your file — tables, headers and footnotes included.",
        "h1": "DeepL document translation, and the layout step that follows it",
        "lede": "Document translation is three jobs wearing one coat: extract the text, "
                "translate it, then put it back. The third one is where afternoons disappear.",
        "honest": "Uploading a whole document to DeepL is a real feature and its output is "
                  "readable. We are not comparing sentence quality here; we are comparing what "
                  "the file looks like when you open it afterwards.",
        "bullets": [
            "Original-format write-back for PDF, DOCX, PPTX, XLSX: headings, tables, "
            "footnotes and columns rebuilt rather than flattened into text.",
            "Terminology enforced before the file is reassembled, so a term that appears "
            "in forty places is decided once.",
            "If layout write-back cannot be guaranteed, the job still delivers the full "
            "translated text with the gap stated — never a silent failure.",
        ],
        "faq": [
            ("Why does a translated document need reassembly?",
             "Because a DOCX is styles, numbering, headers and anchored tables. Translating "
             "the characters without touching that structure leaves you with a text file."),
            ("What decides whether layout survives?",
             "How the pipeline treats the file: parsed as structure, or flattened into a text "
             "block and re-poured. Only the first one can keep a table where it was."),
            ("Can long documents keep terminology consistent?",
             "Yes — a knowledge package applied before delivery plus translation memory, so a "
             "term does not change its mind halfway through a manual."),
            ("Is there a way to check without committing?",
             "Send one real document through and open the result. Layout and terminology are "
             "both visible in the download; nothing here needs a demo script."),
        ],
    },
    "google-translate-document": {
        "title": "Google Translate Documents: Layout Is Your Job | LangCross",
        "desc": "Translate a document for free and the reassembly is yours. LangCross hands back the same file, translated, with terminology applied before delivery.",
        "h1": "Google Translate for documents — what you still have to rebuild afterwards",
        "lede": "You can get the words out of a document with a free translator. The work "
                "starts when the words have to go back into the file, in the same places.",
        "honest": "For reading a document quickly, a free tool is genuinely enough, and we "
                  "will not oversell that case. The moment the file has to be sent out, the "
                  "reassembly and the terminology become the actual job.",
        "bullets": [
            "Files are translated as files: the structure is parsed, the text replaced, and "
            "the original layout re-emitted on the way out.",
            "Industry dictionaries plus translation memory keep repeated wording stable across "
            "a catalogue, a manual series or a set of contracts.",
            "Points-based cost with the estimate shown before the job, so the alternative to "
            "free is a number you can plan, not a subscription you guess.",
        ],
        "faq": [
            ("What is the hidden cost of free document translation?",
             "The hour after it: pasting sections back, fixing table widths, re-checking that "
             "one term means the same thing on page 3 and page 40."),
            ("Which formats come back as themselves?",
             "PDF, DOCX, PPTX, XLSX, TXT, CSV and Markdown are rebuilt in place; subtitle and "
             "JSON-style files are delivered as an aligned bilingual table instead."),
            ("How do you keep a term identical across a whole set?",
             "Knowledge package plus translation memory, applied before delivery. The decision "
             "is stored once, not renegotiated per file."),
            ("Does a failed layout write-back lose the content?",
             "No. When write-back cannot be guaranteed the job still returns the complete "
             "translated text and flags the gap rather than reporting success."),
        ],
    },
}


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
    for d in DISPARAGE_WORDS:
        if _HAS_ASCII.search(d):
            # 英文条目走词边界，"cheaper"/"quality"这类正常词不许被误杀
            if re.search(r"\b" + re.escape(d.lower()) + r"\b", low):
                bad.append(f"贬低对手：{d}")
        elif d in t:
            bad.append(f"贬低对手：{d}")
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
# 门面 12 语种竞品口径的四条自检（方案 §八 B/C 批；★ 2026-10-25 对账批从仓外一次性脚本收进仓内）
#
# 为什么这条必须在仓里：那三十六条 description 是**对外承诺竞品差异**的那一句，
# 四条判据当初只在 `~/i18n_persist/apply_seo_vs_20261024.py` 里跑过一次。
# 跑过一次＝下一批改词典时没有任何东西会拦——「把 ja 那句的 DeepL 删掉」这种改动
# 在界面上看不出问题（句子照样通顺、长度照样合规），只有这四条能判死。
# 同族的病本仓记过很多次：闸门只活在施工脚本里，产物却长期在线。
#
# ★ 判据 ②（不贬低／红线词）**复用上面那把 `lint_text`**，不在这再抄一份名单：
#   两份名单迟早打架，而打架的输法是"页面照发、词照违规"。
#   后端注入面（`internal/api/seo_meta.go`）读的是 embed 的同一份 JSON，
#   Go 侧另有逐字节等值锁（`TestSEOMetaJSONMatchesFrontendSource`），
#   所以这里判**源文件**就等于判了服务端出栈那一份——一把尺子，两个读点。
# =============================================================================
ROUTE_META_JSON = os.path.join(ROOT, "frontend-react", "src", "seo", "route_meta.json")
# 必须点名两家竞品的三句（首页／定价／比价＝对外说差异的那三面）
COMPETITOR_ROUTES = ["/", "/pricing", "/compare"]
COMPETITOR_NAMES = ["DeepL", "Google"]
# 逐键非空的射程：五个公开路由 × 12 语种，title 与 description 都要有自己的一句
ROUTE_META_PUBLIC_ROUTES = ["/", "/pricing", "/compare", "/login", "/register"]
# ★ 长度分档：CJK 语种按 120、其余按 220（Google 的 description 截断按**显示宽度**，
#   汉字一档、拉丁一档；超档不是"报错"而是搜索结果里被切成半句，页面侧零症状）。
ROUTE_META_CJK_MAX = 120
ROUTE_META_LATIN_MAX = 220
_CJK_RE = re.compile(r"[\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fff\uac00-\ud7af]")


def route_meta_len_limit(s: str) -> int:
    """按语种脚本取长度上限（含任一日韩汉字／假名／谚文即走 CJK 档）。"""
    return ROUTE_META_CJK_MAX if _CJK_RE.search(s) else ROUTE_META_LATIN_MAX


def route_meta_copy_failures(doc: dict, where: str = "route_meta.json") -> list:
    """四条自检：① 两竞品都点名 ② 红线词（复用 lint_text）
    ③ 长度分档 ④ 五路由 × 12 语种 title/description 逐键非空。
    回违规清单，空＝过闸。"""
    bad = []
    langs = doc.get("languages") or []
    if len(langs) != 12:
        bad.append(f"{where}：languages 应 12 个，实得 {len(langs)}")
    routes = doc.get("routes") or {}

    # ④ 逐键非空（先判这一条：①②③ 对空串没有意义，会把一条缺失报成三条）
    for path in ROUTE_META_PUBLIC_ROUTES:
        r = routes.get(path) or {}
        for field in ("title", "description"):
            m = r.get(field) or {}
            for lang in langs:
                v = (m.get(lang) or "").strip()
                if not v:
                    bad.append(f"{where}：{path} 的 {field}[{lang}] 是空的")

    # ①③ 竞品三面
    for path in COMPETITOR_ROUTES:
        r = routes.get(path) or {}
        m = r.get("description") or {}
        for lang in langs:
            v = (m.get(lang) or "").strip()
            if not v:
                continue  # ④ 已经点过名
            miss = [n for n in COMPETITOR_NAMES if n not in v]
            if miss:
                bad.append(f"{where}：{path}[{lang}] 没点名竞品 {'、'.join(miss)}"
                           f"（差异句退化成通用句）")
            for hit in lint_text(v):
                bad.append(f"{where}：{path}[{lang}] {hit}")
            limit = route_meta_len_limit(v)
            if len(v) > limit:
                bad.append(f"{where}：{path}[{lang}] 长度 {len(v)} 超档 {limit}"
                           f"（搜索结果会被截成半句）")
    return bad


def check_route_meta_json(path: str = ROUTE_META_JSON) -> list:
    """读源文件跑四条自检；文件读不到／JSON 坏掉都算违规（不许静默跳过）。"""
    if not os.path.exists(path):
        return [f"route_meta 源文件不存在：{path}"]
    try:
        with open(path, encoding="utf-8") as f:
            doc = json.load(f)
    except Exception as e:                             # noqa: BLE001
        return [f"route_meta.json 解析失败：{e}"]
    return route_meta_copy_failures(doc)


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

    # ★ 下面五件是「页型可覆写」的槽位，默认值与重构前逐字一致——
    #   既有 100 页（tool/scenario/guide）不许因为这次加分支而长出一字节差异，
    #   否则「只新增 6 页、不动存量」这条承诺在 diff 里根本看不出来。
    desc = (f"Translate {src} to {dst} and get the file back in its original format — "
            f"tables, headings and footnotes intact. Try a sample before registering.")
    why_para = ("Most translation tools hand you text and leave the reassembly to you. That is where the\n"
                f"  afternoon goes. {BRAND_EN} writes the translation back into your file, and enforces your\n"
                "  terminology before delivery rather than after.")
    cta_href = (f"{SITE}/login?{utm}&utm_content=hero&s={r['src']}&t={r['dst']}"
                f"&fmt={r['fmt'] or r['scenario']}")
    cta_label = ("Translate a "
                 f"{FMT_LABEL.get(r['fmt'], 'file') if r['fmt'] else 'file'} now — no signup to try")
    sub_line = "Upload your file, get it back in the original format."

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
    # ★ 竞品对标型（L5，2026-10-24 用户令「和竞品做对标，作出差异性来」）：
    #   文案七件（title/h1/lede/bullets/faq/honest/desc）全在 VS_COPY 里逐页独立成文，
    #   这里只负责取用与「CTA 不带语向参数」——竞品页的 src/dst 是空的，
    #   带上 `&t=` 会拼出一个 `t=` 空参数的登录链接，落进落地页还得再判一次空值。
    elif ptype == "vs":
        key = f"{r['scenario']}-{r.get('angle', '')}"
        copy = VS_COPY.get(key)
        if not copy:
            raise SystemExit(
                f"竞品对标页 {key} 缺文案块：请在 VS_COPY 补写一套独立文案"
                "（title/h1/lede/bullets/faq/honest/desc，不许复用其他页的句子）")
        title = copy["title"]
        h1 = copy["h1"]
        lede = copy["lede"]
        bullets = copy["bullets"]
        faq = copy["faq"]
        desc = copy["desc"]
        why_para = copy["honest"]
        cta_href = f"{SITE}/login?{utm}&utm_content=hero"
        cta_label = "Run one of your own documents — no signup to try"
        sub_line = "Upload the file, download the same file translated."
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
    desc = fit_len(desc, 155)

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
<meta property="og:image" content="{OG_IMAGE_URL}">
<meta property="og:image:width" content="{OG_IMAGE_W}">
<meta property="og:image:height" content="{OG_IMAGE_H}">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:image" content="{OG_IMAGE_URL}">
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

  <a class="cta" href="{cta_href}">
    {html.escape(cta_label)}</a>
  <p class="sub">{html.escape(sub_line)}</p>

  <div class="panel">
    <h2 style="margin-top:0">What you get back</h2>
    <ul>
{chr(10).join(f'      <li>{html.escape(b)}</li>' for b in bullets)}
    </ul>
  </div>

  <h2>Why this is different</h2>
  <p>{html.escape(why_para)}</p>

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
        # ★ sitemap 必须由**磁盘并集**派生，只写本轮生成的那几条是缺陷：
        #   竞品族这 6 页是按 `--intent L5` 单独一批生成的，若照旧写 `written`，
        #   这一次跑完存量的 100 页就从 sitemap 上整体消失（check() 那条
        #   「sitemap 计数＝磁盘页面数」当场判红，而线上的表现只是存量页面慢慢掉收录——
        #   零症状、零报错，是最难发现的那一类）。
        on_disk = sorted(f[:-5] for f in os.listdir(page_dir) if f.endswith(".html"))
        with open(os.path.join(out_dir, "sitemap.xml"), "w", encoding="utf-8") as f:
            f.write(render_sitemap(on_disk))
        with open(os.path.join(out_dir, "robots.txt"), "w", encoding="utf-8") as f:
            f.write(render_robots())

    # 报告
    os.makedirs(os.path.dirname(os.path.abspath(report_path)), exist_ok=True)
    with open(report_path, "w", encoding="utf-8") as f:
        f.write("# SEO 页面生成报告\n\n")
        f.write(f"- 输入词条：{len(rows)}\n- 生成成功：**{len(written)}**\n")
        f.write(f"- 被拦下：**{len(problems)}**\n")
        f.write(f"- 输出目录：`{os.path.join(out_dir, PAGE_DIR)}`\n\n")
        if not dry_run:
            f.write(f"- sitemap 收录：{len(on_disk)} 页（磁盘并集，含本轮之外的存量）\n\n")
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
    # ★ og:image 的「文件在不在」按**产物目录**判一次，不逐页判：106 页指向同一个路径，
    #   缺图是一条问题而不是 106 条（把同一件事报 106 遍，读的人只会把闸门关掉）。
    og_file = os.path.join(out_dir, OG_IMAGE_PATH.lstrip("/"))
    if not os.path.isfile(og_file):
        fail.append(f"分享卡文件没落盘：{og_file}（106 页的 og:image 全指向 404）")
    elif os.path.getsize(og_file) < 2000:
        fail.append(f"分享卡只有 {os.path.getsize(og_file)} B，不像一张真图：{og_file}")

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
        # ★ 方案 §2.2 那一行「og:image，分享时不裸奔」的落点：绝对 URL + twitter 那一侧同步
        if f'<meta property="og:image" content="{OG_IMAGE_URL}">' not in doc:
            fail.append(f"{fn}: 缺 og:image 或不是绝对 URL（应为 {OG_IMAGE_URL}）")
        if f'<meta name="twitter:image" content="{OG_IMAGE_URL}">' not in doc:
            fail.append(f"{fn}: 缺 twitter:image（X/Telegram 只认这一条）")
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

    # ★ 门面 12 语种竞品口径的四条自检（2026-10-25 收进仓内；判的是源文件，
    #   服务端 embed 那一份由 Go 侧的逐字节等值锁带过来——见 route_meta_copy_failures 的注释）
    fail.extend(check_route_meta_json())

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

    # ★ L5 竞品对标族（2026-10-24 用户令「和竞品做对标，作出差异性来」）——五件自证：
    #   ① 矩阵里 竞品 × 角度 的**每个组合**都在 VS_COPY 有文案（缺了不是渲染期才炸：
    #      build() 对缺文案是 raise SystemExit＝整批中止，所以必须提前到这里判）；
    #      反向也要判：VS_COPY 里躺着一条矩阵没有的键＝文案成了死码，下一个人会以为那页已存在。
    #   ② 每套七件齐全、bullets≥3、faq≥4、title≤60、desc≤155、零占位；
    #   ③ **六页两两不许同句**——「换个竞品名把同一套话重跑一遍」正是 doorway page
    #      被人工处置的教科书写法，这条是它唯一的机械防线（也是"差异性"三个字的落地处）；
    #   ④ 竞品页真渲染一次：CTA／FAQPage 在位，且 CTA **不许带空语向参数**
    #      （竞品页没有 src→dst，带上 `&t=` 会把一个空参数写进落地链接）；
    #   ⑤ 贬低闸门两头都要验：喂脏文本必须响（否则"零违规"只是没跑），
    #      喂自家六套文案必须不响（否则整族长期红灯，最后被人把闸门摘掉）。
    from keyword_matrix import COMPETITORS, VS_ANGLES  # type: ignore
    want_keys = {f"{c[0]}-{a[0]}" for c in COMPETITORS for a in VS_ANGLES}
    for missing in sorted(want_keys - set(VS_COPY)):
        fail.append(f"竞品页 {missing} 在矩阵里但 VS_COPY 没有文案")
    for orphan in sorted(set(VS_COPY) - want_keys):
        fail.append(f"VS_COPY 里的 {orphan} 在矩阵里没有词条（文案成了死码）")

    for k, v in VS_COPY.items():
        for field in ("title", "h1", "lede", "honest", "desc"):
            if not v.get(field):
                fail.append(f"竞品页 {k} 缺 {field}")
        if len(v.get("bullets", [])) < 3:
            fail.append(f"竞品页 {k} bullets 不足 3 条")
        if len(v.get("faq", [])) < 4:
            fail.append(f"竞品页 {k} FAQ 不足 4 条")
        if len(v.get("title", "")) > 60:
            fail.append(f"竞品页 {k} title 超长（{len(v['title'])}）")
        if len(v.get("desc", "")) > 155:
            fail.append(f"竞品页 {k} desc 超长（{len(v['desc'])}）")
        if PLACEHOLDER_RE.search(json.dumps(v, ensure_ascii=False)):
            fail.append(f"竞品页 {k} 含占位符")
        hit = lint_text(json.dumps(v, ensure_ascii=False))
        if hit:
            fail.append(f"竞品页 {k} 文案过不了自家闸门：" + "；".join(hit))

    seen_line = {}
    for k, v in VS_COPY.items():
        lines = ([v.get("title", ""), v.get("h1", ""), v.get("lede", ""),
                  v.get("honest", ""), v.get("desc", "")]
                 + list(v.get("bullets", [])) + [q for q, _a in v.get("faq", [])])
        for line in lines:
            if line in seen_line and seen_line[line] != k:
                fail.append(f"竞品页 {k} 与 {seen_line[line]} 复用同一句：{line[:48]}…")
            seen_line[line] = k

    vprobe = dict(probe, slug="deepl-alternative", keyword="deepl alternative",
                  intent="L5", page_type="vs", src="en", dst="", fmt="",
                  scenario="deepl", angle="alternative")
    try:
        vdoc = render_page(vprobe, ["en"])
        if 'class="cta"' not in vdoc:
            fail.append("竞品页缺 CTA")
        if "FAQPage" not in vdoc:
            fail.append("竞品页缺 FAQPage 结构化数据")
        if html.escape(VS_COPY["deepl-alternative"]["honest"]) not in vdoc:
            fail.append("竞品页没把诚实句渲染进正文（差异性段被吞了）")
        if "&t=" in vdoc:
            fail.append("竞品页 CTA 带着空语向参数 &t=（竞品页没有 src→dst）")
    except Exception as e:                             # noqa: BLE001
        fail.append(f"竞品页渲染失败：{e}")

    if not lint_text("DeepL is worse at documents and gives poor quality output."):
        fail.append("贬低闸门对已知脏文本不报警——闸门没在工作")

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

    # ★ og:image 两腿：渲染必须带上绝对 URL；check() 的判据必须有射程（摘掉就点名）
    if f'<meta property="og:image" content="{OG_IMAGE_URL}">' not in doc:
        fail.append("探针页没有 og:image（方案 §2.2 那一行没落地）")
    if f'<meta name="twitter:image" content="{OG_IMAGE_URL}">' not in doc:
        fail.append("探针页没有 twitter:image（X/Telegram 只认这一条）")
    stripped = doc.replace(f'<meta property="og:image" content="{OG_IMAGE_URL}">', "")
    if f'<meta property="og:image" content="{OG_IMAGE_URL}">' in stripped:
        fail.append("反证失效：摘掉 og:image 的变异体里它还在（这枚判据是空转的）")

    # ★ 门面竞品口径四条自检的射程自证：真文件必须过闸，四枚变异各点名一条
    real = check_route_meta_json()
    if real:
        fail.append("route_meta 竞品口径没过自家四条自检：" + "；".join(real[:5]))
    import copy as _copy  # noqa: PLC0415
    with open(ROUTE_META_JSON, encoding="utf-8") as f:
        base_doc = json.load(f)
    mutants = [
        ("① 删掉一句里的 DeepL",
         lambda d: d["routes"]["/"]["description"].__setitem__("ja", "Google 翻訳が一文で終わるところから"),
         "没点名竞品"),
        ("② 把贬低词写进首页描述",
         lambda d: d["routes"]["/pricing"]["description"].__setitem__(
             "en", "DeepL is worse at documents — LangCross is the accurate choice."),
         "贬低对手"),
        ("③ 超长度档",
         lambda d: d["routes"]["/compare"]["description"].__setitem__("zh", "长" * (ROUTE_META_CJK_MAX + 1)),
         "超档"),
        ("④ 掏空一个语种键",
         lambda d: d["routes"]["/register"]["title"].__setitem__("th", "  "),
         "是空的"),
    ]
    for label, mutate, expect in mutants:
        doc2 = _copy.deepcopy(base_doc)
        before = json.dumps(doc2, ensure_ascii=False)
        mutate(doc2)
        if json.dumps(doc2, ensure_ascii=False) == before:
            fail.append(f"反证 {label} 没改动任何字节（锚漂了，这条自证是空转的）")
            continue
        hits = route_meta_copy_failures(doc2, where="变异体")
        if not hits:
            fail.append(f"反证失效：{label} 四条自检却全绿")
        elif not any(expect in h for h in hits):
            fail.append(f"反证 {label} 没点名预期那一条（应含「{expect}」，实得：{hits[:2]}）")

    if fail:
        print("SELFTEST FAIL")
        for f in fail:
            print("  ✖ " + f)
        return 1
    print(f"SELFTEST PASS  (形态 4/4，场景文案 {len(SCENARIO_COPY)} 套，"
          f"竞品文案 {len(VS_COPY)} 套且两两不同句，闸门 4/4 会响，"
          f"og:image 2/2，门面竞品四条自检 4/4 有射程)")
    return 0


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="能言 SEO 页面工厂")
    ap.add_argument("--csv", default=DEFAULT_CSV)
    ap.add_argument("--out-dir", default=DEFAULT_OUT_DIR)
    ap.add_argument("--report", default=DEFAULT_REPORT)
    ap.add_argument("--limit", type=int, default=100)
    ap.add_argument("--priority", default="", help="只生成指定优先级，逗号分隔，如 P0")
    # ★ --intent（2026-10-24 竞品页族）：竞品那一族是**单独一批**生成的，
    #   不能靠 `--priority` 筛——六条 L5 里两条落 P0、四条落 P2（高分竞争档把它们压住了），
    #   按优先级筛就只出两页，剩下四页永远生成不出来，而它的文案是**已写好**的事实。
    #   意图档位与配额无关，所以这里按意图筛、并按 limit 放开门。
    ap.add_argument("--intent", default="", help="只生成指定意图层，逗号分隔，如 L5")
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
    if a.intent:
        want_i = set(x.strip() for x in a.intent.split(",") if x.strip())
        rows = [r for r in rows if r["intent"] in want_i]
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
