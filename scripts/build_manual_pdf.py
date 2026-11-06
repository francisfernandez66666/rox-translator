#!/usr/bin/env python3
"""
md -> PDF converter for the LangCross user guides (产品手册/manual.*.md).

Why this exists
---------------
The 12-language user guides live as Markdown (manual.<lang>.md). The compiled
PDFs under 产品手册/pdf/ are re-exported from those sources whenever the docs
change. There is no external/markdown-service dependency: this script is the
single, reproducible generator ("md+PDF 重出").

Approach
--------
- One Unicode font (Arial Unicode MS) embedded via PyMuPDF Archive covers every
  UI script: Latin, Cyrillic, CJK (zh/ja/ko), Thai, Arabic (RTL). No tofu boxes.
- Markdown is parsed into atomic blocks (heading / para / table / list /
  blockquote / code / hr). Each block is measured on a scratch page, then placed
  with page.insert_htmlbox and paginated so blocks never split across pages.
- Arabic gets `direction: rtl`.

Run:  python3 scripts/build_manual_pdf.py [lang ...]
With no args, all 12 languages are built.
"""
import glob
import io
import os
import re
import sys

import pymupdf
from fontTools.subset import Options, Subsetter
from fontTools.ttLib import TTFont

# --------------------------------------------------------------------------- #
# Font discovery (macOS Arial Unicode; override via env ARIAL_UNICODE_TTF)
# --------------------------------------------------------------------------- #
def find_arial_unicode():
    cand = os.environ.get("ARIAL_UNICODE_TTF")
    if cand and os.path.exists(cand):
        return cand
    for pat in (
        "/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
        "/Library/Fonts/Arial Unicode.ttf",
        "/System/Library/Fonts/Arial Unicode.ttf",
    ):
        if os.path.exists(pat):
            return pat
    raise SystemExit("Arial Unicode.ttf not found; set ARIAL_UNICODE_TTF")


# --------------------------------------------------------------------------- #
# Inline markdown -> html (escape first, then code/bold/italic/link/strike)
# --------------------------------------------------------------------------- #
def inline_md(text: str) -> str:
    # escape
    text = text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
    # code spans first (protect content)
    codes = []

    def stash(m):
        codes.append(m.group(1))
        return f"\x00CODE{len(codes) - 1}\x00"

    text = re.sub(r"`([^`]+)`", stash, text)
    # links [text](url) — internal #anchors can't resolve to heading ids, drop them
    def link_sub(m):
        txt, url = m.group(1), m.group(2)
        if url.startswith("#"):
            return txt
        return f'<a href="{url}">{txt}</a>'

    text = re.sub(r"\[([^\]]+)\]\(([^)]+)\)", link_sub, text)
    # bold **x** or __x__
    text = re.sub(r"\*\*(.+?)\*\*", r"<b>\1</b>", text)
    text = re.sub(r"__(.+?)__", r"<b>\1</b>", text)
    # italic *x* or _x_
    text = re.sub(r"(?<!\*)\*(?!\*)(.+?)\*(?!\*)", r"<i>\1</i>", text)
    text = re.sub(r"(?<!_)_(?!_)(.+?)_(?!_)", r"<i>\1</i>", text)
    # strike ~~x~~
    text = re.sub(r"~~(.+?)~~", r"<s>\1</s>", text)
    # restore code
    for i, c in enumerate(codes):
        text = text.replace(f"\x00CODE{i}\x00", f"<code>{c}</code>")
    return text


# --------------------------------------------------------------------------- #
# Block parser
# --------------------------------------------------------------------------- #
def parse_blocks(md: str):
    lines = md.split("\n")
    blocks = []  # each: (kind, html)
    i = 0
    n = len(lines)

    def is_table_sep(s):
        return bool(re.match(r"^\s*\|?[\s:|-]+\|?\s*$", s)) and "-" in s

    while i < n:
        line = lines[i]
        stripped = line.strip()

        # blank
        if stripped == "":
            i += 1
            continue

        # code fence
        if stripped.startswith("```") or stripped.startswith("~~~"):
            fence = stripped[:3]
            i += 1
            buf = []
            while i < n and not lines[i].strip().startswith(fence):
                buf.append(lines[i])
                i += 1
            i += 1  # skip closing fence
            code = "\n".join(buf)
            blocks.append(("code", f"<pre>{inline_md(code)}</pre>"))
            continue

        # 图片（★ E 组 2026-11-06：手册每章配一张**夹具**功能截图）
        # 独占一行的 `![alt](name)` 才当图；段中间的 `![](...)` 仍按普通文字走，
        # 否则一句正文里出现括号就会被切成两块（排版事故比漏一张图难查）。
        m = re.match(r"^!\[([^\]]*)\]\(([^)\s]+)\)\s*$", line)
        if m:
            alt = m.group(1).replace('"', "")
            blocks.append(("img", f'<div class="shotbox"><img class="shot" src="{m.group(2)}" alt="{alt}"/></div>'))
            i += 1
            continue

        # heading
        m = re.match(r"^(#{1,6})\s+(.*)$", line)
        if m:
            level = len(m.group(1))
            blocks.append((f"h{level}", f"<h{level}>{inline_md(m.group(2).strip())}</h{level}>"))
            i += 1
            continue

        # horizontal rule
        if re.match(r"^(-{3,}|\*{3,}|={3,})\s*$", stripped):
            blocks.append(("hr", "<hr>"))
            i += 1
            continue

        # blockquote
        if stripped.startswith(">"):
            buf = []
            while i < n and lines[i].strip().startswith(">"):
                buf.append(re.sub(r"^\s*>\s?", "", lines[i]))
                i += 1
            inner = parse_blocks("\n".join(buf))
            inner_html = "".join(h for _, h in inner)
            blocks.append(("blockquote", f"<blockquote>{inner_html}</blockquote>"))
            continue

        # table
        if "|" in line and i + 1 < n and is_table_sep(lines[i + 1]):
            header = [c.strip() for c in line.strip().strip("|").split("|")]
            i += 2  # skip header + separator
            rows = []
            while i < n and "|" in lines[i] and lines[i].strip() != "":
                rows.append([c.strip() for c in lines[i].strip().strip("|").split("|")])
                i += 1
            thead = "<tr>" + "".join(f"<th>{inline_md(c)}</th>" for c in header) + "</tr>"
            tbody = ""
            for r in rows:
                tbody += "<tr>" + "".join(f"<td>{inline_md(c)}</td>" for c in r) + "</tr>"
            blocks.append(("table", f"<table>{thead}{tbody}</table>"))
            continue

        # list (ordered / unordered, possibly nested)
        if re.match(r"^\s*([-*+]|\d+[.)])\s+", line):
            buf = []
            while i < n and (
                re.match(r"^\s*([-*+]|\d+[.)])\s+", lines[i])
                or (lines[i].strip() != "" and re.match(r"^\s+\S", lines[i]) and not re.match(r"^\s*([#>`]|#{1,6})", lines[i]))
            ):
                # only continue list if indented continuation or another marker
                if lines[i].strip() == "":
                    break
                if re.match(r"^\s*([-*+]|\d+[.)])\s+", lines[i]):
                    buf.append(lines[i])
                elif re.match(r"^\s+\S", lines[i]):
                    buf.append(lines[i])  # nested continuation
                else:
                    break
                i += 1
            blocks.append(("list", parse_list(buf)))
            continue

        # paragraph
        buf = []
        while i < n and lines[i].strip() != "" and not re.match(
            r"^(#{1,6}\s|>\s|```|~~~|---|\*\*\*|===)", lines[i]
        ) and not re.match(r"^\s*([-*+]|\d+[.)])\s+", lines[i]):
            buf.append(lines[i])
            i += 1
        para = " ".join(s.strip() for s in buf)
        blocks.append(("p", f"<p>{inline_md(para)}</p>"))

    return blocks


def parse_list(raw_lines):
    # raw_lines: list of raw markdown list lines (may be nested by indent)
    items = []
    for ln in raw_lines:
        indent = len(ln) - len(ln.lstrip(" "))
        m = re.match(r"^(\s*)([-*+]|\d+[.)])\s+(.*)$", ln)
        marker = m.group(2)
        text = m.group(3)
        ordered = bool(re.match(r"\d+[.)]", marker))
        items.append((indent, ordered, text))

    def rec(idx, indent):
        ordered = items[idx][1]
        tag = "ol" if ordered else "ul"
        parts = []
        while idx < len(items):
            ind, ordr, txt = items[idx]
            if ind < indent:
                break
            if ind > indent:
                # shouldn't happen at this level; treat as same
                pass
            # this item
            item_html = inline_md(txt)
            idx += 1
            if idx < len(items) and items[idx][0] > indent:
                child, idx = rec(idx, items[idx][0])
                item_html += child
            parts.append(f"<li>{item_html}</li>")
        return f"<{tag}>" + "".join(parts) + f"</{tag}>", idx

    out, _ = rec(0, items[0][0])
    return out


# --------------------------------------------------------------------------- #
# PDF build
# --------------------------------------------------------------------------- #
PAGE = pymupdf.paper_rect("A4")
MARGIN = 54  # pt ~ 19mm
CONTENT_TOP = MARGIN
CONTENT_BOTTOM = PAGE.height - MARGIN
CONTENT_LEFT = MARGIN
CONTENT_RIGHT = PAGE.width - MARGIN
CONTENT_W = CONTENT_RIGHT - CONTENT_LEFT
CONTENT_H = CONTENT_BOTTOM - CONTENT_TOP
GAP = 4  # pt between blocks


def build_css(rtl: bool) -> str:
    direction = "rtl" if rtl else "ltr"
    return f"""
@font-face {{ font-family: ArialUnicode; src: url(ArialUnicode.ttf); }}
* {{ font-family: ArialUnicode; }}
body {{ font-size: 10.5px; line-height: 1.5; color: #1a1a1a; margin: 0; direction: {direction}; }}
h1 {{ font-size: 22px; font-weight: 700; margin: 0 0 6px; }}
h2 {{ font-size: 17px; font-weight: 700; margin: 14px 0 5px; }}
h3 {{ font-size: 13.5px; font-weight: 700; margin: 10px 0 4px; }}
h4 {{ font-size: 12px; font-weight: 700; margin: 8px 0 3px; }}
h5, h6 {{ font-size: 11px; font-weight: 700; margin: 6px 0 3px; }}
p {{ margin: 0 0 6px; }}
ul, ol {{ margin: 0 0 6px; padding-left: 18px; }}
li {{ margin: 2px 0; }}
blockquote {{ margin: 0 0 8px; padding: 6px 12px; border-left: 3px solid #ccc; color: #555; background: #f5f5f5; }}
table {{ border-collapse: collapse; margin: 0 0 8px; }}
td, th {{ border: 1px solid #bbb; padding: 3px 7px; font-size: 9.5px; vertical-align: top; }}
th {{ background: #eee; font-weight: 700; }}
code {{ background: #f0f0f0; padding: 1px 3px; font-size: 9px; }}
pre {{ background: #f0f0f0; padding: 8px; margin: 0 0 8px; font-size: 9px; white-space: pre-wrap; }}
hr {{ border: none; border-top: 1px solid #ccc; margin: 8px 0; }}
a {{ color: #1a1a1a; }}
/* 手册配图（E 组 2026-11-06）：宽度跟着版心走，高度按比例——截图是 1440×900，
   不写 max-width 会按原始像素撑破内容区；边框是灰线，不是把截图当正文色块。 */
div.shotbox {{ margin: 0 0 8px; }}
img.shot {{ max-width: 100%; height: auto; border: 1px solid #bbb; }}
"""


def subset_font(font_path, chars):
    """Subset Arial Unicode to only the glyphs a manual uses (keeps GSUB/GPOS
    so Arabic/Thai shaping survives). Returns ttf bytes."""
    font = TTFont(font_path)
    opts = Options()
    opts.layout_features = ["*"]  # keep all shaping features (Arabic connect, Thai)
    opts.glyph_names = True
    opts.notdef_outline = True
    opts.recalc_bounds = True
    opts.hinting = False  # screen PDFs don't need hinting; ~2.4x smaller
    opts.desubroutinize = True
    ss = Subsetter(options=opts)
    text = "".join(sorted(set(chars)))
    ss.populate(text=text)
    ss.subset(font)
    buf = io.BytesIO()
    font.save(buf)
    return buf.getvalue()


def measure_block(scratch_doc, html, css, arch):
    page = scratch_doc.new_page()
    rect = pymupdf.Rect(CONTENT_LEFT, CONTENT_TOP, CONTENT_RIGHT, CONTENT_BOTTOM)
    spare, scale = page.insert_htmlbox(rect, html, css=css, archive=arch)
    scratch_doc.delete_page(0)  # keep scratch doc small
    if scale < 1.0:
        return CONTENT_H  # taller than a page: cap, will be scaled on final
    return CONTENT_H - spare


def resolve_shots(md: str, manual_dir: str, lang: str):
    """把 md 里的 `![alt](img/<章节>.png)` 换成**本语种那一张**截图并读进内存。

    md 里刻意只写章节名、不写语种（同 AGENTS §一·14「清单只有一份」的同一手法）：
    12 份手册的正文因此不需要各改各的路径，语种由生成器按当前 lang 现拼
    `img/<章节>/<lang>.png`。文件不在 ⇒ **整行撤下**（正文照常出图位以外的内容）并计入 missing，
    由调用方出声——**不许**留一个指向空气的 `<img>`（那是把"缺图"藏成"图坏了"）。
    """
    out, images, missing = [], {}, []
    n = 0
    for ln in md.split("\n"):
        m = re.match(r"^!\[[^\]]*\]\(img/([^)\s]+)\.png\)\s*$", ln.strip())
        if not m:
            out.append(ln)
            continue
        cand = os.path.join(manual_dir, "img", m.group(1), f"{lang}.png")
        if not os.path.exists(cand):
            missing.append(os.path.relpath(cand, manual_dir))
            continue
        name = f"shot{n}.png"
        with open(cand, "rb") as fh:
            images[name] = fh.read()
        out.append(re.sub(r"\(img/[^)\s]+\.png\)", f"({name})", ln.strip()))
        n += 1
    return "\n".join(out), images, missing


def build_pdf(md_path, out_path, font_path):
    md_raw = open(md_path, encoding="utf-8").read()
    lang = re.search(r"manual\.([\w-]+)\.md$", md_path).group(1)
    rtl = lang == "ar"
    manual_dir = os.path.dirname(os.path.abspath(md_path))
    md, shots, missing = resolve_shots(md_raw, manual_dir, lang)
    if missing:
        print(f"WARN {lang} 缺截图 {len(missing)} 张（正文照常出，图位撤下）: {', '.join(missing[:3])}")

    # Subset the font to this manual's glyphs -> small, no per-call bloat.
    chars = set(md)
    # safety: keep full Arabic + Thai blocks so shaping forms are present
    chars |= set(chr(c) for c in range(0x0600, 0x06FF + 1))
    chars |= set(chr(c) for c in range(0x0E00, 0x0E7F + 1))
    subset = subset_font(font_path, chars)

    arch = pymupdf.Archive()
    arch.add((subset, "ArialUnicode.ttf"))
    # 截图走 archive 而不是 file:// ——实测 insert_htmlbox 对 file:// 一律 0 张图（静默丢图），
    # 只有 archive 里的名字会被解析；所以这里必须在 measure 之前就把字节灌进去。
    for name, data in shots.items():
        arch.add((data, name))
    css = build_css(rtl)
    blocks = parse_blocks(md)

    # measure each block's natural height (subset font -> accurate, small mem)
    scratch = pymupdf.open()
    heights = [measure_block(scratch, h, css, arch) for _, h in blocks]
    scratch.close()

    # paginate: accumulate blocks that fit one page, render per page
    doc = pymupdf.open()
    page = doc.new_page()
    buf, buf_h = [], 0.0
    SAFE = CONTENT_H - 24  # safety margin to avoid scale-down on flush

    def render_current():
        if buf:
            rect = pymupdf.Rect(CONTENT_LEFT, CONTENT_TOP, CONTENT_RIGHT, CONTENT_BOTTOM)
            page.insert_htmlbox(rect, "".join(buf), css=css, archive=arch)

    for (kind, html), h in zip(blocks, heights):
        if kind.startswith("h"):
            h += 6
        if buf and buf_h + h + GAP > SAFE:
            render_current()
            page = doc.new_page()
            buf, buf_h = [], 0.0
        buf.append(html)
        buf_h += h + GAP
    render_current()

    # ★ 2026-10-28：insert_htmlbox 是**逐页**渲染的，每页都会各嵌一份字体子集 ⇒
    #   产出体积 ≈ 页数 × 字体大小（实测 en 5.7 MB／zh 18.4 MB／ja 19.4 MB，而注册欢迎邮件
    #   就是把这 12 份当附件发出去的，ja/zh-hant 已顶到常见 20 MB 附件上限）。
    #   保存前做一次**文档级**字体合并与子集化，再带压缩存盘：实测 en 5.7 MB → 170 KB、
    #   zh 18.4 MB → 1.4 MB，页数与正文一字不动（抽文本核验见构建后的自检）。
    try:
        doc.subset_fonts()
    except Exception as e:  # 合并失败不该让手册生不出来：退回原样保存（体积大但内容对）
        print(f"WARN subset_fonts({lang}) 失败，按原样保存: {e}")
    if os.path.exists(out_path):
        os.remove(out_path)
    doc.save(out_path, garbage=4, deflate=True, deflate_fonts=True)
    return doc.page_count


def main():
    font_path = find_arial_unicode()
    base = os.path.dirname(os.path.abspath(__file__))
    manual_dir = os.path.join(os.path.dirname(base), "产品手册")
    pdf_dir = os.path.join(manual_dir, "pdf")
    os.makedirs(pdf_dir, exist_ok=True)

    if len(sys.argv) > 1:
        langs = sys.argv[1:]
    else:
        langs = [
            re.search(r"manual\.([\w-]+)\.md$", os.path.basename(p)).group(1)
            for p in glob.glob(os.path.join(manual_dir, "manual.*.md"))
        ]

    for lang in langs:
        md_path = os.path.join(manual_dir, f"manual.{lang}.md")
        if not os.path.exists(md_path):
            print("SKIP (no md):", lang)
            continue
        out_path = os.path.join(pdf_dir, f"LangCross-User-Guide-{lang}.pdf")
        pages = build_pdf(md_path, out_path, font_path)
        print(f"BUILD {lang:8s} -> {os.path.basename(out_path)}  pages={pages}")


if __name__ == "__main__":
    main()
