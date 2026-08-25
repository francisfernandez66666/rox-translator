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

# ---------- 字体归一化 ----------
# OCR 识别语言：源文档可能中英混排，固定多语组合；提取与应用两侧必须一致
# 字体解析结果缓存（进程级）
_FONT_CACHE = None

def resolve_cjk_font() -> str:
    """解析输出用 CJK 字体（结果缓存）：
    ① 环境变量 CJK_FONT_NAME 显式指定；
    ② 候选链按序取 fontconfig 已安装且覆盖汉字的第一个：
       阿里巴巴普惠体(Alibaba PuHuiTi，默认) → 苹方 → Noto Sans CJK SC → 任一 :lang=zh；
    ③ 兜底 Noto Sans CJK SC。
    只输出「确实已安装」的字族名，杜绝 LibreOffice 回退到无汉字字体。"""
    global _FONT_CACHE
    if _FONT_CACHE:
        return _FONT_CACHE
    import os as _os, subprocess as _sp
    name = _os.environ.get("CJK_FONT_NAME", "").strip()
    if name and _font_installed(name):
        _FONT_CACHE = name
        return name
    for cand in ("Alibaba PuHuiTi", "PingFang SC", "Noto Sans CJK SC", "Noto Sans CJK JP"):
        if _font_installed(cand):
            _FONT_CACHE = cand
            return cand
    try:
        out = _sp.run(["fc-list", ":lang=zh", "family"], capture_output=True, text=True, timeout=10)
        for ln in out.stdout.splitlines():
            fam = ln.split(",")[0].strip()
            if fam:
                _FONT_CACHE = fam
                return fam
    except Exception:
        pass
    _FONT_CACHE = "Noto Sans CJK SC"
    return _FONT_CACHE

def _font_installed(family: str) -> bool:
    import subprocess as _sp
    try:
        r = _sp.run(["fc-list", family, "family"], capture_output=True, text=True, timeout=10)
        return family.lower() in (r.stdout or "").lower()
    except Exception:
        return False

def normalize_fonts(docx_path: str):
    """还原文件时的字体兜底规则：
    原文声明的字族若服务器未安装且文件未内嵌该字体（pdf2docx 重建的 DOCX
    一律不携带内嵌字体，故判定条件即「未安装」），则统一改写为 resolve_cjk_font()
    解析出的默认 CJK 字体（苹方优先，其次 Noto Sans CJK SC）。
    已安装的字体原样保留；确保任何汉字都不会落入无字形回退路径（方框）。"""
    from docx import Document
    font = resolve_cjk_font()
    doc = Document(docx_path)
    def rewrite(el):
        n = 0
        for rf in el.iter(W_NS + 'rFonts'):
            for attr in ('ascii', 'hAnsi', 'eastAsia', 'cs'):
                q = W_NS + attr
                cur = rf.get(q)
                if not cur:
                    continue
                if _font_installed(cur):
                    continue  # 已安装：保留原文体
                rf.set(q, font)
                n += 1
        return n
    n = rewrite(doc.element.body)
    try:
        n += rewrite(doc.styles.element)
    except Exception:
        pass
    doc.save(docx_path)

def _pil_font(size: int):
    """为 PIL 绘制解析 CJK 字体文件路径（跟随 resolve_cjk_font 的结果）。"""
    from PIL import ImageFont
    import subprocess as _sp
    fam = resolve_cjk_font()
    try:
        r = _sp.run(["fc-match", "-f", "%{file}", fam],
                    capture_output=True, text=True, timeout=10)
        path = (r.stdout or "").strip()
        if path and os.path.exists(path):
            return ImageFont.truetype(path, size)
    except Exception:
        pass
    for cand in ("/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",):
        if os.path.exists(cand):
            return ImageFont.truetype(cand, size)
    return ImageFont.load_default()


# ---------- 段内字号统一 ----------
def normalize_font_sizes(docx_path: str) -> int:
    """每段以出现最多的字号为准对齐 w:sz/w:szCs，治同段忽大忽小。"""
    from docx import Document
    from collections import Counter
    doc = Document(docx_path)
    fixed = 0
    for para in iter_all_paragraphs(doc):
        szs = [el.get(W_NS + 'val') for el in para._p.iter(W_NS + 'sz')]
        szs = [v for v in szs if v and v.isdigit()]
        if len(set(szs)) <= 1:
            continue
        dominant = Counter(szs).most_common(1)[0][0]
        for tag in ('sz', 'szCs'):
            for el in para._p.iter(W_NS + tag):
                v = el.get(W_NS + 'val')
                if v and v != dominant:
                    el.set(W_NS + 'val', dominant)
                    fixed += 1
    doc.save(docx_path)
    return fixed

# ---------- 整页 OCR 模式（图形化/扫描版 PDF 兜底） ----------
PAGE_TEXT_MIN = 200  # 平均每页文本层字符低于此值视为图形化文档


# ---------- 表格自适应 ----------
def normalize_tables(docx_path: str):
    """表格自适应：总宽100% + layout=autofit；行高统一 atLeast（可撑开）；
    删除单元格固定宽 tcW（交由 autofit 依内容分配），治译文裁剪/溢出。"""
    from docx import Document
    from docx.oxml.ns import qn
    from docx.oxml import OxmlElement
    doc = Document(docx_path)
    for tbl in doc.element.body.iter(qn('w:tbl')):
        tblPr = tbl.find(qn('w:tblPr'))
        if tblPr is None:
            tblPr = OxmlElement('w:tblPr')
            tbl.insert(0, tblPr)
        layout = tblPr.find(qn('w:tblLayout'))
        if layout is None:
            layout = OxmlElement('w:tblLayout')
            tblPr.append(layout)
        layout.set(qn('w:type'), 'autofit')
        tw = tblPr.find(qn('w:tblW'))
        if tw is None:
            tw = OxmlElement('w:tblW')
            tblPr.append(tw)
        tw.set(qn('w:w'), '5000')
        tw.set(qn('w:type'), 'pct')
    for trPr in doc.element.body.iter(qn('w:trPr')):
        for h in trPr.findall(qn('w:trHeight')):
            h.set(qn('w:hRule'), 'atLeast')
    for tcW in list(doc.element.body.iter(qn('w:tcW'))):
        tcW.getparent().remove(tcW)
    doc.save(docx_path)

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
    print(json.dumps({"success": True, "mode": "docx", "texts": texts}, ensure_ascii=False))

def cmd_apply(cache_docx: str, out_path: str, lang: str, translations: dict):
    tmp = tempfile.mkdtemp()
    try:
        work = os.path.join(tmp, "work.docx")
        shutil.copy2(cache_docx, work)
        if translations:
            translate_docx_text(work, translations)
            normalize_fonts(work)   # 字体兜底：未装字族→默认CJK（普惠体），汉字不回退
            normalize_font_sizes(work)  # ★ 段内字号统一（治同段忽大忽小）
            normalize_tables(work)      # ★ 表格自适应：列宽自动布局+表宽100%
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
