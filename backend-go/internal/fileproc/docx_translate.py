#!/usr/bin/env python3
"""docx_translate.py — PDF→DOCX→翻译→DOCX→PDF（保留排版+图表+图片OCR翻译）"""
import sys, os, json, subprocess, tempfile, shutil, re
from pathlib import Path
from docx import Document
from docx.opc.constants import RELATIONSHIP_TYPE as RT
from PIL import Image
import pytesseract
import io

TESS_LANG_MAP = {
    "zh": "chi_sim+chi_tra", "en": "eng", "ja": "jpn", "ko": "kor",
    "fr": "fra", "de": "deu", "es": "spa", "ru": "rus", "ar": "ara",
    "pt": "por", "it": "ita", "nl": "nld", "pl": "pol", "tr": "tur",
    "th": "tha", "vi": "vie", "hi": "hin",
}

def tess_lang(code: str) -> str:
    return TESS_LANG_MAP.get(code[:2].lower(), "eng")

def run(cmd: list, timeout=120):
    subprocess.run(cmd, check=True, timeout=timeout, capture_output=True)

def pdf_to_docx(pdf_path: str, docx_path: str):
    from pdf2docx import Converter
    cv = Converter(pdf_path)
    cv.convert(docx_path)
    cv.close()

def docx_to_pdf(docx_path: str, pdf_path: str):
    try:
        run(["libreoffice", "--headless", "--convert-to", "pdf", "--outdir", str(Path(pdf_path).parent), docx_path])
    except:
        pass
    expected = str(Path(pdf_path).parent / (Path(docx_path).stem + ".pdf"))
    if os.path.exists(expected):
        os.rename(expected, pdf_path)

_ZW_RE = re.compile(r"[\s\u200b\u200c\u200d\u2060\ufeff]+")

def _norm(s: str) -> str:
    """归一化文本用于匹配：去除空白与零宽字符"""
    return _ZW_RE.sub("", s or "")

def _build_lookup(translations: dict) -> dict:
    """构建 归一化原文 → 译文 的查找表（长键优先，避免短键误吞长段）"""
    lk = {}
    for orig, trans in translations.items():
        k = _norm(orig)
        if len(k) >= 4:  # 过滤过短的碎片，防误替换
            lk[k] = trans
    return dict(sorted(lk.items(), key=lambda kv: -len(kv[0])))

def _replace_para_text(para, lookup: dict) -> bool:
    """段落级替换：
    A) 整段归一化命中 → 译文写入首 run（保留其格式），清空其余 run；
    B) 段内子串命中（跨 run）→ 在拼接全文上定位区间替换后重写回首 run。
    注意：para.runs 每次访问返回新代理对象，必须取一次列表复用。"""
    runs = list(para.runs)
    if not runs:
        return False
    full = "".join(r.text for r in runs)
    if not full.strip():
        return False
    nf = _norm(full)
    # A 整段命中
    trans = lookup.get(nf)
    if trans is not None:
        for i, r in enumerate(runs):
            r.text = trans if i == 0 else ""
        return True
    # B 子串命中：归一化索引 → 原始索引映射
    idx_map = [i for i, ch in enumerate(full) if not _ZW_RE.match(ch)]
    spans = []
    for k, t in lookup.items():
        start = 0
        while True:
            i = nf.find(k, start)
            if i < 0:
                break
            o_s = idx_map[i]
            o_e = idx_map[i + len(k) - 1] + 1
            spans.append((o_s, o_e, t))
            start = i + len(k)
    if not spans:
        return False
    spans.sort(key=lambda x: x[0], reverse=True)
    # 去重叠（从后往前，跳过与已处理区间相交的）
    last_s = len(full)
    new_full = full
    for o_s, o_e, t in spans:
        if o_e > last_s:
            continue
        new_full = new_full[:o_s] + t + new_full[o_e:]
        last_s = o_s
    first = next((r for r in runs if r.text), None)
    if first is None:
        return False
    for r in runs:
        r.text = new_full if r is first else ""
    return True

def apply_translations_to_text(text: str, translations: dict) -> str:
    for orig, trans in translations.items():
        if orig and orig in text:
            text = text.replace(orig, trans)
    return text

def _translate_paragraphs(paras, lookup: dict, translations: dict):
    for para in paras:
        if _replace_para_text(para, lookup):
            continue
        # 未整段命中：退化为逐 run 子串替换（处理短碎片）
        for run in para.runs:
            run.text = apply_translations_to_text(run.text, translations)

def translate_docx_text(docx_path: str, translations: dict):
    doc = Document(docx_path)
    lookup = _build_lookup(translations)
    _translate_paragraphs(doc.paragraphs, lookup, translations)
    for table in doc.tables:
        for row in table.rows:
            for cell in row.cells:
                _translate_paragraphs(cell.paragraphs, lookup, translations)
    for section in doc.sections:
        _translate_paragraphs(section.header.paragraphs, lookup, translations)
        _translate_paragraphs(section.footer.paragraphs, lookup, translations)
    doc.save(docx_path)

def ocr_and_translate_image(img_bytes: bytes, translations: dict, lang: str) -> bytes:
    img = Image.open(io.BytesIO(img_bytes))
    if img.mode != "RGB":
        img = img.convert("RGB")
    data = pytesseract.image_to_data(img, lang=tess_lang(lang), output_type=pytesseract.Output.DICT)
    from PIL import ImageDraw, ImageFont
    draw = ImageDraw.Draw(img)
    w, h = img.size
    for i in range(len(data["text"])):
        text = data["text"][i].strip()
        if not text:
            continue
        trans = apply_translations_to_text(text, translations)
        if trans == text:
            continue
        x, y, bw, bh = data["left"][i], data["top"][i], data["width"][i], data["height"][i]
        font_size = max(8, int(bh * 0.7))
        try:
            font = ImageFont.truetype("/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", font_size)
        except:
            font = ImageFont.load_default()
        draw.rectangle([x, y, x + bw, y + bh], fill="white")
        draw.text((x, y), trans, fill="black", font=font)
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()

def translate_docx_images(docx_path: str, translations: dict, lang: str):
    doc = Document(docx_path)
    for rel in doc.part.rels.values():
        if "image" in rel.reltype:
            img_bytes = rel.target_part.blob
            if len(img_bytes) < 5000:
                continue
            try:
                new_bytes = ocr_and_translate_image(img_bytes, translations, lang)
                rel.target_part._blob = new_bytes
            except:
                pass
    doc.save(docx_path)

def main():
    if len(sys.argv) < 4:
        print("Usage: docx_translate.py <in_path> <out_path> <lang> < translations.json", file=sys.stderr)
        sys.exit(1)
    in_path = sys.argv[1]
    out_path = sys.argv[2]
    lang = sys.argv[3]
    raw = sys.stdin.read()
    try:
        data = json.loads(raw)
    except:
        data = {}
    translations = data.get("translations", {})

    tmp = tempfile.mkdtemp()
    try:
        ext = Path(in_path).suffix.lower()
        if ext == ".pdf":
            docx_path = os.path.join(tmp, "input.docx")
            pdf_to_docx(in_path, docx_path)
        elif ext in (".docx",):
            docx_path = in_path
        else:
            print(f"Unsupported format: {ext}", file=sys.stderr)
            sys.exit(1)

        translate_docx_text(docx_path, translations)
        if any(len(v) > 0 for v in translations.values()):
            translate_docx_images(docx_path, translations, lang)

        if ext == ".pdf":
            docx_to_pdf(docx_path, out_path)
        else:
            shutil.copy2(docx_path, out_path)
        print(f"OK: {out_path}")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

if __name__ == "__main__":
    main()