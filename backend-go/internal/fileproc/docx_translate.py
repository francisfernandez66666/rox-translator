#!/usr/bin/env python3
"""docx_translate.py — PDF→DOCX→翻译→DOCX→PDF（保留排版/图片）

子命令：
  extract <pdf> <cache_docx>          转换一次并输出段落文本JSON（供LLM翻译的键，与替换目标完全一致）
  apply   <cache_docx> <out_pdf> <lang>  从stdin读translations JSON，在缓存DOCX副本上替换后转PDF
  legacy  <in_path> <out_path> <lang>    单阶段直通模式（回退用）
  selftest                               内置纯函数自检（调度/CI 用；仅需标准库）
"""
import sys, os, io, json, re, subprocess, tempfile, shutil
from pathlib import Path


def run(cmd: list, timeout=600):
    subprocess.run(cmd, check=True, timeout=timeout, capture_output=True)

def pdf_to_docx(pdf_path: str, docx_path: str):
    import contextlib
    with contextlib.redirect_stdout(io.StringIO()):  # 含首次import pymupdf的弃用告警与转换进度，保stdout纯净JSON
        from pdf2docx import Converter
        cv = Converter(pdf_path)
        cv.convert(docx_path)
        cv.close()

def docx_to_pdf(docx_path: str, pdf_path: str):
    # ★ 每个调用使用独立的 LibreOffice 用户配置目录，避免默认 profile 被并发/历史
    #   进程锁住导致 --headless 转换卡死（曾致大文档 PDF 写回超时降级为 xlsx 对照表）。
    prof = tempfile.mkdtemp(prefix="lo_profile_")
    try:
        run(["libreoffice", "--headless",
             f"-env:UserInstallation=file://{prof}",
             "--convert-to", "pdf",
             "--outdir", str(Path(pdf_path).parent), docx_path],
            timeout=600)
    finally:
        shutil.rmtree(prof, ignore_errors=True)
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

# 「字族是否已安装」结果缓存（进程级；normalize_fonts 会按文档内每个 rFonts 元素
# 查询，一次转换可达数千次——不加缓存则每元素 fork 一个 fc-list 子进程，
# 实测 373KB 文档拖慢整条流水线数分钟）
_INSTALLED_CACHE = {}

def _font_installed(family: str) -> bool:
    if family in _INSTALLED_CACHE:
        return _INSTALLED_CACHE[family]
    import subprocess as _sp
    try:
        r = _sp.run(["fc-list", family, "family"], capture_output=True, text=True, timeout=10)
        ok = family.lower() in (r.stdout or "").lower()
    except Exception:
        ok = False
    _INSTALLED_CACHE[family] = ok
    return ok

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

# ---------- 表格版式锚定 ----------
def _emu_to_twips(v) -> int:
    """EMU → twips（python-docx 的 Length 以 EMU 计；1 twip = 1/20 pt = 635 EMU）。"""
    try:
        return int(round(float(v) / 635.0))
    except (TypeError, ValueError):
        return 0

def column_widths_from_samples(samples: list) -> list:
    """由「每列的宽度采样」推导列宽（纯函数，不依赖 python-docx，供 selftest 断言）。

    参数：samples = 长度 ncols 的列表，每项是该列采集到的宽度样本（twips，可含小数）。
    返回：长度 ncols 的整数列宽；**推不出**（samples 空 / 每列都无样本 / Σ≤0）时返回 []。

    规则：每列取样本的**中位数**（抗个别行的跨列/合并单元格干扰），无样本的列用
    「其余列的均值」补位；仍推不出的整体返回 []。
    """
    if not samples:
        return []
    out = []
    for ss in samples:
        vals = sorted(v for v in ss if v and v > 0)
        out.append(int(round(vals[len(vals) // 2])) if vals else 0)
    known = [v for v in out if v > 0]
    if not known:
        return []
    if len(known) < len(out):                      # 个别列无样本：用已知列均值补位
        avg = max(1, int(round(sum(known) / len(known))))
        out = [v if v > 0 else avg for v in out]
    if sum(out) <= 0:
        return []
    return out

# ---------- 最小列宽钳制（治「窄列 + 长词 → 字母竖排」） ----------
# 现象：fixed 布局把列宽锁在原 PDF 量出的值上。该宽度是按**中文**内容设计的，
# 译文（英文）里一个不可断词（如 "Distributors"、"https://…"）若比列还宽，
# 排版器会逐字母断行竖排（E2E 实测 27-28 页整列字母堆叠 + 大片空白）。
# 对策：列宽可被钳制抬高到「该列最长不可断词的估算渲染宽度」，缺口从最宽的
# 若干列按比例匀出——正常列毫发无损，只有被压爆的极端窄列被救起。
# 估算口径：字符宽 ≈ 0.62em（拉丁均值偏保守），em = w:sz/2 pt，1pt=20 twips，
# 即 char_twips ≈ 6.2 × sz(半点)；再加单元格左右边距余量。CJK 可任意断行，不参与。
_UNBREAKABLE_TOKEN_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9\-'&./+:_]*")
_DEFAULT_FONT_HPS = 21          # 10.5pt（w:sz 半点）缺省
_CELL_PAD_TWIPS = 240           # 单元格左右边距合计余量
_MIN_COL_FLOOR = 600            # 列宽下限（约 1cm）
_MAX_TOKEN_CLAMP = 2800         # 单列钳制上限（超长 URL 等极端 token 不许吃掉整行）


def _cell_unbreakable_need(tc):
    """单元格内最长不可断词的估算渲染宽度（twips）。无拉丁词返回 0。"""
    from docx.oxml.ns import qn
    need = 0
    for p in tc.findall(qn('w:p')):
        # 该段字号取段落内最大 w:sz（简化：逐 run 估算，取最大需求）
        for r in p.findall(qn('w:r')):
            rPr = r.find(qn('w:rPr'))
            sz = _DEFAULT_FONT_HPS
            if rPr is not None:
                szEl = rPr.find(qn('w:sz'))
                if szEl is not None:
                    try:
                        v = int(szEl.get(qn('w:val')))
                        if v > 0:
                            sz = v
                    except (TypeError, ValueError):
                        pass
            text = "".join(t.text or '' for t in r.findall(qn('w:t')))
            for tok in _UNBREAKABLE_TOKEN_RE.findall(text):
                need = max(need, len(tok) * 6.2 * sz + _CELL_PAD_TWIPS)
    return need


def _table_column_mins(tbl, ncols):
    """各列最小宽度（twips）。跨列单元格的需求记账到被跨列之和（末列兜底补差）。"""
    from docx.oxml.ns import qn
    mins = [0.0] * max(1, ncols)
    joint = []  # (start, span, need)
    for tr in tbl.findall(qn('w:tr')):
        idx = 0
        for tc in tr.findall(qn('w:tc')):
            tcPr = tc.find(qn('w:tcPr'))
            spanEl = tcPr.find(qn('w:gridSpan')) if tcPr is not None else None
            try:
                span = int(spanEl.get(qn('w:val'))) if spanEl is not None else 1
            except (TypeError, ValueError):
                span = 1
            span = max(1, span)
            need = _cell_unbreakable_need(tc)
            if need > 0:
                if span == 1 and idx < len(mins):
                    mins[idx] = max(mins[idx], need)
                elif idx < len(mins):
                    joint.append((idx, span, need))
            idx += span
    return mins, joint


def clamp_min_widths(widths, mins, joint):
    """纯函数：以最小列宽钳制 widths。
    顺序：① 单列需求抬升（缺口先记账）→ ② 从富余列按比例匀出、尽量保总宽
    （donor 不得低于列宽下限）→ ③ 跨列单元格「和约束」末列补差（此时匀宽已定，
    不会被回抽；无处可借时允许总宽增长——好过字母竖排）。
    返回新列表（int twips）。"""
    w = [float(x) for x in widths]
    n = len(w)
    if not n:
        return w
    # ① 抬升不足列（封顶，防极端 token 吃掉整行）
    for i in range(n):
        cap = min(mins[i], _MAX_TOKEN_CLAMP)
        if w[i] < cap:
            w[i] = cap
    # ② 从有富余的列按比例匀出，尽量保总宽。
    #    富余 = 原始宽 − max(自身钳制下限, 列宽下限)；被抬过的列富余为 0。
    base = list(w)
    grow = sum(w[i] - widths[i] for i in range(n))
    if grow > 0:
        slack = [max(0.0, widths[i] - max(min(mins[i], _MAX_TOKEN_CLAMP),
                                          float(_MIN_COL_FLOOR))) for i in range(n)]
        total_slack = sum(slack)
        if total_slack > 0:
            take = min(grow, total_slack)
            for i in range(n):
                if slack[i] > 0:
                    w[i] -= slack[i] / total_slack * take
    # ③ 跨列单元格的和约束：不足则末列补差（允许总宽增长）
    for start, span, need in joint:
        end = min(n, start + span)
        cur = sum(w[start:end])
        cap = min(need, float(_MAX_TOKEN_CLAMP * span))
        if cur < cap:
            w[end - 1] += cap - cur
    return [int(round(x)) for x in w]


def normalize_tables(docx_path: str):
    """表格版式锚定：**以 w:tcW 为列宽真值**，据此重建 tblGrid，置 tblLayout=fixed，
    行高统一 atLeast（可撑开，治裁剪）。

    ⚠️ 适用前提：只用于 **pdf2docx 重建** 出来的 DOCX（本脚本只由 PDF 三类路径
    extract/apply/legacy 调用；原生 .docx 工单走 Go 侧 fileproc.ApplyDocx，不经过这里）。
    对作者原生的 docx 慎用——那里的 tblLayout=autofit 可能是作者「按内容自适应」的意图，
    强制 fixed 会覆盖它。若将来要复用，务必加参数区分来源。

    ★ 2026-09-18 修复「大量块不匹配」——两个关键实测事实（真实 pdf2docx 产物验证）：
    ① **pdf2docx 写的 tblGrid 是等宽占位符，不是真实几何**：实测全部 2 列表都是
       恰好 50/50、全部 3 列表都是 33.3/33.3/33.3；而 tcW 才是从原 PDF 量出来的真实列宽
       （26.9/73.1、8.8/47.7/43.5、16.9/45.5/37.6 …）。所以**绝不能删 tcW 去信 tblGrid**，
       也不能信 tblGrid 的比例——正确做法是反过来，以 tcW 为真值把 tblGrid 重建对。
    ② 交付版本的 normalize_tables 用 tblLayout=autofit + 删光全部 tcW，等于把列宽交给
       排版器按**译文内容**重新分配。译文（英/德…）比中文长，autofit 把最窄那列压到极限：
       交付 PDF 里 "Distributors × DMS/tablets" 被切成 "Distributor|s×" / "DMS/table|ts"，
       换行碎片（原文「不靠邮件发 PDF」的 "PDF"）落到相邻列下方 → 用户看到的「块不匹配」；
       表格还撑出版心右边界、行高被撑爆（原文 16 页 → 译文 35 页）。
    因此：fixed + tcW 真值重建栅格，行高只放宽、列宽完全不再重排。
    """
    from docx import Document
    from docx.oxml.ns import qn
    from docx.oxml import OxmlElement
    doc = Document(docx_path)
    for tbl in doc.element.body.iter(qn('w:tbl')):
        tblPr = tbl.find(qn('w:tblPr'))
        if tblPr is None:
            tblPr = OxmlElement('w:tblPr')
            tbl.insert(0, tblPr)
        grid = tbl.find(qn('w:tblGrid'))
        gcs = list(grid.findall(qn('w:gridCol'))) if grid is not None else []
        ncols = len(gcs)
        # 采样各列真实宽度（tcW）。跨列单元格（gridSpan>1）按均摊采样，样本弱于整列单元格，
        # 故取中位数而非均值来抗干扰。
        samples = [[] for _ in range(max(1, ncols))]
        span_max = 0
        for tr in tbl.findall(qn('w:tr')):
            idx = 0
            for tc in tr.findall(qn('w:tc')):
                tcPr = tc.find(qn('w:tcPr'))
                tcW = tcPr.find(qn('w:tcW')) if tcPr is not None else None
                spanEl = tcPr.find(qn('w:gridSpan')) if tcPr is not None else None
                try:
                    span = int(spanEl.get(qn('w:val'))) if spanEl is not None else 1
                except (TypeError, ValueError):
                    span = 1
                span = max(1, span)
                try:
                    w = int(tcW.get(qn('w:w')) or 0) if tcW is not None else 0
                except (TypeError, ValueError):
                    w = 0
                if w > 0:
                    if span == 1:
                        if idx < len(samples):
                            samples[idx].append(w)
                    else:
                        per = w / float(span)
                        for j in range(span):
                            if idx + j < len(samples):
                                samples[idx + j].append(per)
                idx += span
            span_max = max(span_max, idx)
        if ncols <= 0:
            # 表里没有 tblGrid（原生/手写 XML 可能省略栅格）：列数退化为「最宽一行的
            # gridSpan 累加值」。这里必须把 samples 重建成同长度的空桶，否则下面的
            # samples[:ncols] 会只剩 1 个桶、算出「1 列」的宽度表，和真实列数对不上。
            # ⚠️ 顺带的既有事实：重建后所有桶都是空的（ncols 未知时采样被 max(1,ncols)
            # 限制进了第 0 个桶、此刻被丢弃），column_widths_from_samples 必然返回 [] →
            # 无栅格表一律走下面的 autofit 兜底。本分支只保证「不崩、不误报列宽」，
            # 并不真的救回这类表的列宽——要救得把采样循环改成先定列数再采样。
            ncols = span_max
            samples = [[] for _ in range(ncols)]
        widths = column_widths_from_samples(samples[:ncols] if ncols else [])
        layout = tblPr.find(qn('w:tblLayout'))
        if layout is None:
            layout = OxmlElement('w:tblLayout')
            tblPr.append(layout)
        tw = tblPr.find(qn('w:tblW'))
        if tw is None:
            tw = OxmlElement('w:tblW')
            tblPr.append(tw)
        if not widths:
            # 完全采不到列宽（非 pdf2docx 来源的表）：保留 autofit 兜底，行为同修复前
            layout.set(qn('w:type'), 'autofit')
            tw.set(qn('w:w'), '5000')
            tw.set(qn('w:type'), 'pct')
            continue
        # ⓪ 最小列宽钳制：原 PDF 列宽按中文设计，译文长词（"Distributors"、URL）可能
        #    比锁定的窄列还宽 → fixed 下被逐字母竖排（E2E 实测）。窄于最长不可断词的
        #    列被抬到该词的估算宽度，缺口从富余列按比例匀出；正常表零改动。
        mins, joint = _table_column_mins(tbl, len(widths))
        widths = clamp_min_widths(widths, mins, joint)
        # ① 用 tcW 真值重建 tblGrid（替换掉 pdf2docx 的等宽占位符）
        if grid is None:
            grid = OxmlElement('w:tblGrid')
            tbl.insert(list(tbl).index(tblPr) + 1, grid)
            gcs = []
        for gc in gcs:
            grid.remove(gc)
        for w in widths:
            gc = OxmlElement('w:gridCol')
            gc.set(qn('w:w'), str(w))
            grid.append(gc)
        # ② 单元格 tcW 与新栅格对齐（跨列单元格取被跨列宽之和），两处口径一致才不会打架
        for tr in tbl.findall(qn('w:tr')):
            idx = 0
            for tc in tr.findall(qn('w:tc')):
                tcPr = tc.find(qn('w:tcPr'))
                if tcPr is None:
                    tcPr = OxmlElement('w:tcPr')
                    tc.insert(0, tcPr)
                spanEl = tcPr.find(qn('w:gridSpan'))
                try:
                    span = int(spanEl.get(qn('w:val'))) if spanEl is not None else 1
                except (TypeError, ValueError):
                    span = 1
                span = max(1, span)
                tcW = tcPr.find(qn('w:tcW'))
                if tcW is None:
                    tcW = OxmlElement('w:tcW')
                    tcPr.append(tcW)
                tcW.set(qn('w:w'), str(sum(widths[idx:idx + span])))
                tcW.set(qn('w:type'), 'dxa')
                idx += span
        # ③ fixed：让排版器按上面的栅格渲染，而不是按译文内容重算列宽
        layout.set(qn('w:type'), 'fixed')
        tw.set(qn('w:w'), str(sum(widths)))
        tw.set(qn('w:type'), 'dxa')
    for trPr in doc.element.body.iter(qn('w:trPr')):
        for h in trPr.findall(qn('w:trHeight')):
            h.set(qn('w:hRule'), 'atLeast')
    doc.save(docx_path)

# ---------- 水印伪影清理（白块/底色拼布的根因修复） ----------
# 现场事实（真实 pdf2docx 产物实测）：原 PDF 的极淡水印被 pdf2docx 切成 42 张小图
# （12~64px，全部像素最暗通道 ≥214，无任何深色像素），嵌在表格单元格里——LibreOffice
# 渲染成灰底上的「白色斑块」。而真正的截图/图标都有深色像素（实测 3 张真图 min=0~35）。
# 判据：图片所有像素的最小通道值 ≥190 → 判为水印切片，整图移除。
# 误伤面：一张图如果最深的像素都浅于 #BEBEBE，它本身就近乎不可见，删除无信息损失。
_WATERMARK_MIN_CHANNEL = 190


def strip_watermark_fragments(docx_path: str) -> int:
    """移除 pdf2docx 切出的水印切片图片（近白、无深色像素的嵌入图）。返回移除数。"""
    import io
    from docx import Document
    from docx.oxml.ns import qn
    from PIL import Image
    # 这两个节点不在 word 命名空间下（分别是 drawingml 的 blip、relationships 的 embed），
    # 故写全 URI 而不是 qn('a:blip')——本函数只在此处用一次，不往模块级引命名空间常量。
    A_BLIP = '{http://schemas.openxmlformats.org/drawingml/2006/main}blip'
    R_EMBED = '{http://schemas.openxmlformats.org/officeDocument/2006/relationships}embed'
    doc = Document(docx_path)
    removed = 0
    # 只遍历 body：页眉/页脚/脚注各自是独立 part，这里覆盖不到（实测水印切片都被 pdf2docx
    # 塞进正文单元格里；若日后遇到页眉漏清，需要再对 header/footer part 各跑一遍同样判据）。
    for blip in list(doc.element.body.iter(A_BLIP)):
        rid = blip.get(R_EMBED)
        if not rid:
            continue
        try:
            part = doc.part.related_parts[rid]
            im = Image.open(io.BytesIO(part.blob)).convert('RGB')
            px = list(im.getdata())
            if not px:
                continue
            # 「全像素、全通道的最小值」= 这张图最深的像素；只要有任一像素够深就说明
            # 图里有真内容（截图/图标/照片），不能删——这是与「整图平均亮度」判据的关键差别，
            # 平均值会把「白底上一个黑色 logo」误判成近白可删。
            darkest = min(min(p) for p in px)
        except Exception:
            continue  # 读不出像素的图不动（宁保留勿误删）
        if darkest >= _WATERMARK_MIN_CHANNEL:
            # 向上找宿主 w:drawing 整块移除：只删 blip 会留下一个没有图片引用的空图框，
            # LibreOffice 仍按原尺寸留出空白（灰底上还是白块），等于没清干净。
            el = blip
            while el is not None and el.tag != qn('w:drawing'):
                el = el.getparent()
            if el is not None and el.getparent() is not None:
                el.getparent().remove(el)
                removed += 1
    if removed:
        doc.save(docx_path)  # 一张都没删时不回写：避免无谓地重排包体、改动上游缓存文件
    return removed


# 近白系底色亮度阈值：dedfe3≈0.874、ffffff=1.0；真正的有色设计底（如品牌色）远低于此。
_NEARWHITE_LUM = 0.84


# 亮度取 Rec.601 加权（0.299R+0.587G+0.114B）而非简单平均：人眼对绿色最敏感，
# 平均法会把「浅绿/浅蓝」这类有色底算成近白而误参与表决。
# 异常（缺字符、非十六进制、位数不足）一律返回 1.0 = 当近白看：宁可少改一格底色，
# 也不能因为解析失败就抛异常把整条还原流水线打断。
def _hex_lum(hexcolor: str) -> float:
    try:
        h = hexcolor.strip('#')
        r, g, b = int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16)
        return (0.299 * r + 0.587 * g + 0.114 * b) / 255.0
    except (ValueError, IndexError):
        return 1.0


def unify_column_shading(docx_path: str) -> int:
    """同一表格同一列的近白底色「列内多数表决」，消除底色拼布。
    根因：pdf2docx 采样单元格背景时把水印像素计入，同一列有的格记 dedfe3、
    有的记 ffffff/无底色——LibreOffice 实心渲染后就是「灰列上的白色斑块」。
    只在近白系（亮度≥0.84，含无底色）内部表决；真正的有色设计底一律不动。"""
    from docx import Document
    from docx.oxml.ns import qn
    from docx.oxml import OxmlElement
    doc = Document(docx_path)
    changed = 0
    for tbl in doc.element.body.iter(qn('w:tbl')):
        # 按栅格列收集 (tc, fill)；跨列单元格记到起始列
        # 这里只用 tblGrid 的**列数**（不用列宽），所以 pdf2docx 那份等宽占位栅格在本函数里
        # 够用——也意味着它跑在 normalize_tables 重建栅格之前或之后都等价。
        grid = tbl.find(qn('w:tblGrid'))
        ncols = len(grid.findall(qn('w:gridCol'))) if grid is not None else 0
        if ncols <= 0:
            # 没有栅格就无从判断「谁和谁同列」，整表跳过：底色表决要求列归属严格，用
            # gridSpan 累加猜出来的列数（normalize_tables 里的兜底做法）在这里只会误统一。
            continue
        cols = [[] for _ in range(ncols)]
        # 跨列格（gridSpan>1，如整行合并的标题）只计入**起始列**的票数：实现最省事，且
        # pdf2docx 产物里这类格极少。已知偏差有两处：① 起始列会因此多拿一票，浅灰表头这类
        # 近白跨列格可能把该列的多数票带偏（有色跨列格则只进 vals、不进 votes，既不改也不
        # 污染结果）；② 被跨的第 2..n 列完全拿不到这一格的样本。真遇到列数少、合并行多的
        # 表时，需要改成按 span 均摊或直接跳过 span>1 的格。
        for tr in tbl.findall(qn('w:tr')):
            idx = 0
            for tc in tr.findall(qn('w:tc')):
                tcPr = tc.find(qn('w:tcPr'))
                fill = None
                if tcPr is not None:
                    shd = tcPr.find(qn('w:shd'))
                    if shd is not None:
                        fill = shd.get(qn('w:fill'))
                spanEl = tcPr.find(qn('w:gridSpan')) if tcPr is not None else None
                try:
                    span = int(spanEl.get(qn('w:val'))) if spanEl is not None else 1
                except (TypeError, ValueError):
                    span = 1
                span = max(1, span)
                if idx < ncols:
                    cols[idx].append((tc, fill))
                idx += span
        for col in cols:
            if len(col) < 2:
                continue  # 该列只有一格，无「列内一致」可言
            # vals 里 None 被归一成字符串 'none'，所以「无底色」是参与表决的一票——
            # 这正是根因所在：pdf2docx 对同一列有的格写 dedfe3、有的干脆不写 shd。
            vals = set(f if f else 'none' for _, f in col)
            nearwhite = {v for v in vals
                         if v == 'none' or (_NEARWHITE_LUM <= _hex_lum(v) <= 1.0)}
            # 两个条件各挡一种误改：vals<2 = 整列一个色，本就一致；nearwhite<2 = 近白侧
            # 只剩唯一候选，没有「少数派」需要统一，表决空转。有色设计底之所以不被改动，
            # 靠的是它压根进不了 nearwhite（后面只在 nearwhite 内部比对/改写）。
            if len(vals) < 2 or len(nearwhite) < 2:
                continue  # 无拼布，或混有真正的有色设计底（不动）
            # 列内近白系多数表决
            # votes 的键集合恒等于 nearwhite（nearwhite ⊆ vals，而 vals 的每个值都来自
            # col 里的某一格），故下面两个「票数不足」判定其实已被上面的 continue 覆盖，
            # 留着只为防御未来改动采样口径时出现空字典（max 对空序列会抛 ValueError）。
            votes = {}
            for _, f in col:
                v = f if f else 'none'
                if v in nearwhite:
                    votes[v] = votes.get(v, 0) + 1
            if not votes:
                continue
            # 同票时取「本列中先出现」的那个色：dict 保持插入序，max 又保留首个最大值，
            # 因此结果稳定、不随哈希顺序抖动（否则同一文件两次还原可能给出不同底色）。
            target = max(votes.items(), key=lambda kv: kv[1])[0]
            if len(votes) < 2:
                continue
            # 落刀范围：只重写「近白侧且非胜出值」的格。有色底进不了 nearwhite，所以本函数
            # 不可能把品牌色表头刷白；target=='none'（多数格压根没写 w:shd）时写
            # val=clear + fill=auto 表达「显式无底色」，比删掉少数派自己的 shd 节点少一次
            # XML 结构变动。
            for tc, f in col:
                v = f if f else 'none'
                if v in nearwhite and v != target:
                    tcPr = tc.find(qn('w:tcPr'))
                    if tcPr is None:
                        tcPr = OxmlElement('w:tcPr')
                        tc.insert(0, tcPr)
                    shd = tcPr.find(qn('w:shd'))
                    if shd is None:
                        shd = OxmlElement('w:shd')
                        # ⚠️ 已知宽松点：新增的 shd 一律 append 到 tcPr 末尾，而 OOXML 的
                        # CT_TcPr 对子元素有固定次序（shd 在 tcBorders 之后、noWrap/vAlign
                        # 之前），严格校验的阅读器可能忽略乱序节点。当前输入都是 pdf2docx
                        # 产物（tcPr 内基本只有 tcW/gridSpan 等前置项）故未出问题；日后若
                        # 要处理原生 docx，应改为按 schema 次序插入。
                        tcPr.append(shd)
                    if target == 'none':
                        shd.set(qn('w:val'), 'clear')
                        shd.set(qn('w:fill'), 'auto')
                    else:
                        shd.set(qn('w:val'), 'clear')
                        shd.set(qn('w:fill'), target)
                    changed += 1
    if changed:
        doc.save(docx_path)
    return changed


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
        # ★ 2026-09-09：单字段落也进翻译键（len<2 → len<1）。pdf2docx 偶发把一个词拆成
        #   多个单字段（如「保险」→「保」+「险」、「无界」→「无」），旧过滤让这些单字从不
        #   进入翻译映射，写回后以中文字残留在译文 PDF（实测工单84残留 无/查/险/贵 等）。
        if len(k) < 1 or k in seen:
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
            # 流水线顺序：先灌译文，再做字体/字号/水印/底色/栅格五道后处理。
            # 硬约束只有最后一步——normalize_tables 的最小列宽钳制要靠**译文文本**量出
            # 「最长不可断词」宽度（见 _cell_unbreakable_need），跑在 translate 之前就会
            # 按中文原文取宽度、窄列依旧被英文长词竖排。其余几步只碰样式/图形，彼此无先后。
            translate_docx_text(work, translations)
            normalize_fonts(work)   # 字体兜底：未装字族→默认CJK（普惠体），汉字不回退
            normalize_font_sizes(work)  # ★ 段内字号统一（治同段忽大忽小）
            strip_watermark_fragments(work)  # ★ 水印切片清除（灰底上的「白色斑块」）
            unify_column_shading(work)       # ★ 底色列内多数表决（水印误采的拼布）
            normalize_tables(work)      # ★ tcW 真值重建栅格+fixed+最小列宽钳制
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

def cmd_selftest() -> int:
    """内置自检：守护 column_widths_from_samples 的「以 tcW 真值还原列几何」契约
    （防「块不匹配」回归）。

    仅用标准库（不 import python-docx），故在无 python-docx 的机器上也能跑——由
    internal/fileproc 的 TestPdfTableLayoutPreserved 经 `python3 docx_translate.py selftest`
    驱动。返回 0=全部通过，1=有断言失败（失败原因打到 stderr）。
    """
    # ⚠️ 上面的 docstring 有一处笔误（它是字符串字面量，本次只补注释故未改）：驱动
    # `docx_translate.py selftest` 的是 TestPdfColumnWidthsPlanner；
    # TestPdfTableLayoutPreserved 驱动的是 docx_table_selftest.py（产物级断言）。
    fails = []

    # 断言失败只记账、不抛异常：一次跑完把所有不符项全列出来，免得 CI 上「改一个跑一次」
    # 地挤牙膏；退出码由下面的 fails 是否为空统一决定。
    def ck(cond, msg):
        if not cond:
            fails.append(msg)

    # ★ 现场形态 A：pdf2docx 的 tblGrid 是等宽占位符（2 列表恒为 50/50），
    #   而 tcW 才是从原 PDF 量出的真实几何（26.9%/73.1%）。
    #   回归意义：若实现误信 tblGrid（或误删 tcW），就会把这张表渲成 50/50 ——
    #   「时间」列凭空宽一倍、「事实」列被压窄一半，正是用户看到的版式错位。
    got = column_widths_from_samples([[2828], [7696]])
    ck(got == [2828, 7696], "两列真值未按 tcW 还原：%s（应为 [2828, 7696]）" % got)
    r = [c / float(sum(got)) for c in got]
    ck(abs(r[0] - 0.269) < 0.01 and abs(r[1] - 0.731) < 0.01,
       "列比例失真：%s（应为约 26.9%% / 73.1%%）" % r)

    # ★ 现场形态 B：三列表真实几何 8.8% / 47.7% / 43.5%（源 PDF 的 3. 现状与痛点表）
    got = column_widths_from_samples([[924], [5024], [4576]])
    r = [c / float(sum(got)) for c in got]
    ck(abs(r[0] - 0.088) < 0.01 and abs(r[1] - 0.477) < 0.01 and abs(r[2] - 0.435) < 0.01,
       "三列真值未还原：%s（应为约 8.8%% / 47.7%% / 43.5%%）" % r)

    # 多行采样取中位数：个别行的跨列均摊值不应带偏整列
    got = column_widths_from_samples([[3000, 2000, 2000, 2000], [5000, 5000], [2000, 2000, 2000]])
    ck(got[0] == 2000, "中位数采样失效：%s（第 1 列应为 2000）" % got)

    # 跨列单元格均摊采样：span=2、宽 6000 → 两列各得 3000
    got = column_widths_from_samples([[6000 / 2.0], [6000 / 2.0]])
    ck(got == [3000, 3000], "跨列均摊采样失效：%s" % got)

    # 个别列完全无样本：用已知列均值补位（不产生 0 宽列）
    got = column_widths_from_samples([[1000], [], [3000]])
    ck(len(got) == 3 and all(v > 0 for v in got), "无样本列未补位：%s" % got)
    ck(abs(got[1] - 2000) <= 1, "补位值应约为已知列均值 2000：%s" % got)

    # 全部无样本 / 空输入 → []（调用方退回 autofit 兜底，行为同修复前）
    ck(column_widths_from_samples([]) == [], "空输入应返回 []")
    ck(column_widths_from_samples([[], [], []]) == [], "全空样本应返回 []")
    ck(column_widths_from_samples(None) == [], "None 应返回 []")
    ck(column_widths_from_samples([[0], [0]]) == [], "全零样本应返回 []")

    # ★ 最小列宽钳制：窄列 + 长不可断词 → 抬到词宽、从富余列匀出、总宽不变。
    #   现场形态：源 PDF 窄列（序号/标签列）按中文设计 924 twips，译文
    #   "Distributors"（12 字符 × 6.2 × 21 + 240 ≈ 1802）比列宽还宽 →
    #   fixed 下逐字母竖排（E2E 27-28 页实测）。钳制后窄列抬到 ~1802，
    #   缺口全部由 7696 的宽列匀出，两列总宽不变。
    base = [924, 7696]
    got2 = clamp_min_widths(base, [1802.0, 0.0], [])
    ck(got2[0] >= 1802 - 1, "窄列未抬到长词宽度：%s（应 >= 1802）" % got2)
    ck(sum(got2) == sum(base), "钳制后总宽必须不变：%s（和应 %d）" % (got2, sum(base)))
    ck(got2[1] < base[1], "宽列应让出宽度：%s" % got2)
    # 正常表（列宽远大于词宽）零改动
    got3 = clamp_min_widths([3000, 4000], [500.0, 800.0], [])
    ck(got3 == [3000, 4000], "无需求时钳制不得改动列宽：%s" % got3)
    # 极端超长 token 封顶：不许吃掉整行
    got4 = clamp_min_widths([924, 7696], [99999.0, 0.0], [])
    ck(got4[0] <= 2800 + 1, "极端 token 未封顶：%s（应 <= 2800）" % got4)
    # 跨列单元格和约束：span=2 需求 5000，两列现和 3000 → 末列补差
    got5 = clamp_min_widths([1500, 1500], [0.0, 0.0], [(0, 2, 5000.0)])
    ck(sum(got5) >= 5000 - 1, "跨列和约束未满足：%s（和应 >= 5000）" % got5)

    if fails:
        for m in fails:
            sys.stderr.write("SELFTEST FAIL: %s\n" % m)
        return 1
    print("selftest OK")
    return 0

def main():
    argv = sys.argv[1:]
    if not argv:
        print(__doc__, file=sys.stderr); sys.exit(1)
    mode = argv[0]
    # selftest 分支放在最前、且不读 stdin：它只跑纯函数断言，不需要 python-docx/PIL/LibreOffice，
    # 因此在没装渲染依赖的 CI runner 上也必须能出结果（装不装得上下游与本断言无关）。
    if mode == "selftest":
        sys.exit(cmd_selftest())
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
