#!/usr/bin/env python3
"""docx_translate.py — PDF→DOCX→翻译→DOCX→PDF（保留排版/图片；图片内文字OCR翻译）

子命令：
  extract <pdf> <cache_docx>          转换一次并输出段落文本JSON（供LLM翻译的键，与替换目标完全一致）
  apply   <cache_docx> <out_pdf> <lang>  从stdin读translations JSON，在缓存DOCX副本上替换（含图片OCR）后转PDF
  legacy  <in_path> <out_path> <lang>    单阶段直通模式（回退用）
"""
import sys, os, io, json, re, subprocess, tempfile, shutil
from pathlib import Path

TESS_LANG_MAP = {
    "zh": "chi_sim+chi_tra", "en": "eng", "ja": "jpn", "ko": "kor",
    "fr": "fra", "de": "deu", "es": "spa", "ru": "rus", "ar": "ara",
    "pt": "por", "it": "ita", "nl": "nld", "pl": "pol", "tr": "tur",
    "th": "tha", "vi": "vie", "hi": "hin",
}

def tess_lang(code: str) -> str:
    return TESS_LANG_MAP.get((code or "")[:2].lower(), "eng")

def run(cmd: list, timeout=180):
    subprocess.run(cmd, check=True, timeout=timeout, capture_output=True)

def pdf_to_docx(pdf_path: str, docx_path: str):
    import contextlib
    with contextlib.redirect_stdout(io.StringIO()):  # 含首次import pymupdf的弃用告警与转换进度，保stdout纯净JSON
        from pdf2docx import Converter
        cv = Converter(pdf_path)
        cv.convert(docx_path)
        cv.close()

def docx_to_pdf(docx_path: str, pdf_path: str):
    run(["libreoffice", "--headless", "--convert-to", "pdf",
         "--outdir", str(Path(pdf_path).parent), docx_path])
    expected = str(Path(pdf_path).parent / (Path(docx_path).stem + ".pdf"))
    if os.path.exists(expected):
        os.replace(expected, pdf_path)

# ---------- 文本归一化与查找表 ----------
_ZW_RE = re.compile(r"[\s\u200b\u200c\u200d\u2060\ufeff]+")

def _norm(s: str) -> str:
    return _ZW_RE.sub("", s or "")

def _build_lookup(translations: dict):
    """返回 (full_lookup, partial_lookup)：短键只允许整段命中，防误吞"""
    full_lk, part_lk = {}, {}
    for orig, trans in translations.items():
        k = _norm(orig)
        if not k:
            continue
        if len(k) >= 4:
            part_lk[k] = trans
        full_lk[k] = trans
    sort_desc = lambda d: dict(sorted(d.items(), key=lambda kv: -len(kv[0])))
    return sort_desc(full_lk), sort_desc(part_lk)

# ---------- w:t 级替换（绝不触碰 drawing，图片不丢） ----------
W_NS = '{http://schemas.openxmlformats.org/wordprocessingml/2006/main}'
XML_SPACE = '{http://www.w3.org/XML/1998/namespace}space'

def _para_t_nodes(para):
    return para._p.findall('.//' + W_NS + 't')

def _replace_para_text(para, lookup_full: dict, lookup_part: dict) -> bool:
    ts = _para_t_nodes(para)
    if not ts:
        return False
    full = "".join(t.text or "" for t in ts)
    if not full.strip():
        return False
    nf = _norm(full)

    spans = []
    trans = lookup_full.get(nf)          # A 整段命中（允许短键）
    if trans is not None:
        spans.append((0, len(full), trans))
    else:                                 # B 子串命中（仅长键）
        idx_map = [i for i, ch in enumerate(full) if not _ZW_RE.match(ch)]
        if len(idx_map) == len(nf):
            for k, t in lookup_part.items():
                start = 0
                while True:
                    i = nf.find(k, start)
                    if i < 0:
                        break
                    spans.append((idx_map[i], idx_map[i + len(k) - 1] + 1, t))
                    start = i + len(k)
    if not spans:
        return False

    spans.sort(key=lambda x: x[0])
    merged = []
    for s_, e_, t_ in spans:
        if merged and s_ < merged[-1][1]:
            continue
        merged.append((s_, e_, t_))

    out = []
    off = 0
    si = 0
    for t in ts:
        txt = t.text or ""
        L = len(txt)
        s0, s1 = off, off + L
        buf = []
        cur = s0
        j = si
        while j < len(merged) and merged[j][1] <= cur:
            j += 1
        si = j
        while j < len(merged) and merged[j][0] < s1:
            ms, me, mt = merged[j]
            a = max(ms, cur)
            b = min(me, s1)
            if a > cur:
                buf.append(txt[cur - s0:a - s0])
            if a == ms:
                buf.append(mt)
            cur = b
            j += 1
        if cur < s1:
            buf.append(txt[cur - s0:])
        out.append("".join(buf))
        off = s1

    changed = False
    for t, nt in zip(ts, out):
        if (t.text or "") != nt:
            t.text = nt
            t.set(XML_SPACE, 'preserve')
            changed = True
    return changed

def apply_translations_to_text(text: str, translations: dict) -> str:
    for orig, trans in translations.items():
        if orig and orig in text:
            text = text.replace(orig, trans)
    return text

def iter_all_paragraphs(doc):
    """正文 XML 内全部段落（含表格/嵌套表格/文本框 txbx）+ 页眉页脚。
    注意：lxml 元素代理会被回收且 id() 复用，严禁按 id 去重。"""
    from docx.text.paragraph import Paragraph
    for p_el in doc.element.body.iter(W_NS + 'p'):
        yield Paragraph(p_el, doc)
    for sec in doc.sections:
        for part in (sec.header, sec.footer):
            try:
                el = part._element
            except Exception:
                continue
            if el is None:
                continue
            for p_el in el.iter(W_NS + 'p'):
                yield Paragraph(p_el, doc)

def _has_drawing(r) -> bool:
    """run 内是否含图片/图形（w:drawing 或 w:pict）"""
    return (r._r.find('.//' + W_NS + 'drawing') is not None
            or r._r.find('.//' + W_NS + 'pict') is not None)

def translate_docx_text(docx_path: str, translations: dict):
    from docx import Document
    doc = Document(docx_path)
    lk_full, lk_part = _build_lookup(translations)
    for para in iter_all_paragraphs(doc):
        if _replace_para_text(para, lk_full, lk_part):
            continue
        # 兜底：逐 run 子串替换。★ 含图 run 必须跳过（run.text= 会删掉 drawing）
        for run in para.runs:
            if _has_drawing(run):
                continue
            new = apply_translations_to_text(run.text, translations)
            if new != run.text:
                run.text = new
    doc.save(docx_path)

# ---------- 图片 OCR 翻译 ----------
def ocr_and_translate_image(img_bytes: bytes, translations: dict, lang: str) -> bytes | None:
    import pytesseract
    from PIL import Image, ImageDraw, ImageFont
    img = Image.open(io.BytesIO(img_bytes))
    if img.mode != "RGB":
        img = img.convert("RGB")
    w, h = img.size
    if w < 40 or h < 20:
        return None
    data = pytesseract.image_to_data(img, lang=tess_lang(lang),
                                     output_type=pytesseract.Output.DICT)
    draw = ImageDraw.Draw(img)
    hit = False
    for i in range(len(data["text"])):
        word = (data["text"][i] or "").strip()
        if not word:
            continue
        trans = apply_translations_to_text(word, translations)
        if trans == word:
            # 词级未命中：试整行合并由调用方段落级处理，这里跳过
            continue
        hit = True
        x, y, bw, bh = data["left"][i], data["top"][i], data["width"][i], data["height"][i]
        fs = max(8, int(bh * 0.72))
        try:
            font = ImageFont.truetype(
                "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", fs)
        except Exception:
            font = ImageFont.load_default()
        draw.rectangle([x, y, x + bw, y + bh], fill="white")
        draw.text((x, y), trans[:64], fill=(20, 20, 20), font=font)
    if not hit:
        return None
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()

def translate_docx_images(docx_path: str, translations: dict, lang: str):
    from docx import Document
    try:
        doc = Document(docx_path)
    except Exception:
        return
    n = 0
    for rel in list(doc.part.rels.values()):
        if "image" not in rel.reltype:
            continue
        blob = rel.target_part.blob
        if len(blob) < 3000:  # 忽略图标/装饰小图
            continue
        try:
            nb = ocr_and_translate_image(blob, translations, lang)
            if nb:
                rel.target_part._blob = nb
                n += 1
        except Exception:
            continue
    if n:
        try:
            doc.save(docx_path)
        except Exception:
            pass

# ---------- extract / apply ----------
def cmd_extract(pdf_path: str, cache_docx: str):
    pdf_to_docx(pdf_path, cache_docx)
    from docx import Document
    doc = Document(cache_docx)
    seen, texts = set(), []
    for p in iter_all_paragraphs(doc):
        txt = "".join(t.text or "" for t in _para_t_nodes(p)).strip()
        if not txt:
            continue
        k = _norm(txt)
        if len(k) < 2 or k in seen:
            continue
        seen.add(k)
        texts.append(txt)
    print(json.dumps({"success": True, "texts": texts}, ensure_ascii=False))

def cmd_apply(cache_docx: str, out_path: str, lang: str, translations: dict):
    tmp = tempfile.mkdtemp()
    try:
        work = os.path.join(tmp, "work.docx")
        shutil.copy2(cache_docx, work)
        if translations:
            translate_docx_text(work, translations)
            translate_docx_images(work, translations, lang)
        docx_to_pdf(work, out_path)
        print(f"OK: {out_path}")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

def cmd_legacy(in_path: str, out_path: str, lang: str, translations: dict):
    ext = Path(in_path).suffix.lower()
    tmp = tempfile.mkdtemp()
    try:
        if ext == ".pdf":
            cache = os.path.join(tmp, "in.docx")
            pdf_to_docx(in_path, cache)
        elif ext == ".docx":
            cache = in_path
        else:
            print(f"Unsupported format: {ext}", file=sys.stderr)
            sys.exit(1)
        cmd_apply(cache if ext == ".pdf" else cache, out_path, lang, translations)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

def main():
    argv = sys.argv[1:]
    if not argv:
        print(__doc__, file=sys.stderr); sys.exit(1)
    mode = argv[0]
    if mode == "extract" and len(argv) >= 3:
        cmd_extract(argv[1], argv[2]); return
    if mode == "apply" and len(argv) >= 4:
        raw = sys.stdin.read()
        try:
            data = json.loads(raw)
        except Exception:
            data = {}
        cmd_apply(argv[1], argv[2], argv[3], data.get("translations", {})); return
    if mode == "legacy" and len(argv) >= 4:
        raw = sys.stdin.read()
        try:
            data = json.loads(raw)
        except Exception:
            data = {}
        cmd_legacy(argv[1], argv[2], argv[3], data.get("translations", {})); return
    # 兼容旧调用：<in> <out> <lang>
    if len(argv) >= 3:
        raw = sys.stdin.read()
        try:
            data = json.loads(raw)
        except Exception:
            data = {}
        cmd_legacy(argv[0], argv[1], argv[2], data.get("translations", {})); return
    print(__doc__, file=sys.stderr); sys.exit(1)

if __name__ == "__main__":
    main()
