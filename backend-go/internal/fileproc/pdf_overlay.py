#!/usr/bin/env python3
"""pdf_overlay.py — PDF「原版式·原字体·原地替换」翻译管线（2026-09-18 新链，替代 pdf2docx 路径）。

原理：不重建版式，直接在**原 PDF**上做文字级手术——
  extract: 按块提取段落文本（带 bbox；与 Go cmd_extract 同口径去重），输出 JSON 段序列
  apply:   add_redact_annot 抹掉原文（只删文字层，图片/矢量/表格线全保留）
           → TextWriter+fitz.Font 在原 bbox 嵌回译文（原字号/原颜色/原字体优先）
  selftest: 合成表格夹具回归（不越界/译文落地/原件保留）

关键工程约束（用户需求 2026-09-18）：
  · 原格式：页面其余内容 100% 原件（对比 pdf2docx→LibreOffice 重建链的全部版式问题）
  · 原字体：优先复用 PDF 内嵌字体子集（可提取出 TTF/OTF/CFF 且覆盖全部译文字形时），
            否则回退内置 DroidSansFallback（覆盖中日韩英）
  · 不溢出：换行宽度被**单元格矢量线**钳制（不越列）；高度不够先缩字号（下限 2pt）
            再压行高；只允许向「下方无表格线」的空白处扩高
  · 自适应：字号 1.0→0.45 逐级、行高 1.32→1.12 两段自适应

子命令（Go 经 subprocess 调用；资源闸/超时由 Go 侧管）：
  extract <in.pdf>                    → stdout JSON {"success":true,"texts":[...]}
  apply   <in.pdf> <out.pdf> <lang>   ← stdin JSON {"translations":{...}}；stdout "OK: ..."
  selftest                            → 退出码 0=通过 1=断言失败 2=依赖缺失(跳过)
"""
# ⚠️ 上面 docstring 的「不溢出 / 自适应」两条写的是链路初版口径（缩字号到 2pt 下限、再压行高、
#    字号 1.0→0.45 逐级），现已被 cmd_apply 的「字号照搬原则」取代：译文字号恒等于原文字号，
#    不缩字、不压行距，溢出只靠换行 + 向下方空白扩容消化，实在排不下才由 _force_place 轻微
#    越出格子下缘（宁越界也不丢文、不变小字号）。改代码以 cmd_apply 的注释为准。
#    另：本文件多处注释提到「行距压到 0.85」，实际代码从未实现（_place/_force_place 的行距
#    恒为 size*1.32，_place 排不下直接返回 False 交给 _force_place）。读注释时按此校正。
#    （docstring 本体不改写：它是 main() 无参数时打到 stderr 的用法说明文本。）
import io
import json
import re
import sys
from pathlib import Path

# 兜底字体：随仓库分发的一份 DroidSansFallbackFull（覆盖中/日/韩/拉丁）。
# 它是最后防线而非常规路径——用上就意味着不再是原件字体，故 _pick_font 会先尽一切可能
# 复用原 PDF 的内嵌子集，只有取不到字形或覆盖不住译文时才落到这个字体上。
FALLBACK_FONT = str(Path(__file__).parent / "assets" / "fonts" / "DroidSansFallbackFull.ttf")

# 键归一化要清的噪声字符 = 普通空白 + 零宽字符（U+200B/200C/200D/2060/FEFF）。
# 零宽字符必须一并删：PDF 文本层常混入 BOM/软连字，extract 与 apply 只要有一侧没删，
# 「原文→译文」的键就对不上，症状是「翻译完成但交付 PDF 一个字都没换」。
_ZW_RE = re.compile(r"[\s\u200b\u200c\u200d\u2060\ufeff]+")

try:
    import pymupdf
except Exception as _e:  # 依赖缺失：extract/apply 由 Go 判错，selftest 判跳过
    pymupdf = None
    _IMPORT_ERR = _e


def _norm(s: str) -> str:
    """键归一化：去零宽/折叠空白（与 extract/apply 两侧共用，保证键对齐）。"""
    return _ZW_RE.sub("", s or "")


def _flat_block(block) -> str:
    """块内 spans 拼接为单串（行间空格），再折叠空白——extract/apply 同口径。"""
    txt = " ".join(s["text"] for l in block.get("lines", []) for s in l.get("spans", []))
    return re.sub(r"\s+", " ", txt).strip()


def _page_cuts(page):
    """收集页面矢量线坐标，作为块切分的栅格线。
    返回 (vxs, hys)：竖线 x 集合、横线 y 集合（只收足够长的线，忽略碎装饰）。"""
    vxs, hys = [], []
    for d in page.get_drawings():
        for item in d["items"]:
            segs = []
            if item[0] == "l":
                p1, p2 = item[1], item[2]
                segs.append((p1.x, p1.y, p2.x, p2.y))
            elif item[0] == "re":
                r = item[1]
                segs += [(r.x0, r.y0, r.x0, r.y1), (r.x1, r.y0, r.x1, r.y1),
                         (r.x0, r.y0, r.x1, r.y0), (r.x0, r.y1, r.x1, r.y1)]
            for x1, y1, x2, y2 in segs:
                # 只认「够长的正交线」当栅格线：长度阈值 12pt 用来滤掉勾选框边、下划线、
                # 装饰短划与图标描边——把它们当分栏线会把一句话劈成无意义的小段。
                if abs(x1 - x2) < 0.5 and abs(y1 - y2) > 12:
                    vxs.append(round(x1, 1))
                elif abs(y1 - y2) < 0.5 and abs(x1 - x2) > 12:
                    hys.append(round(y1, 1))
    # 去重聚簇（±1pt 内视为同一条线）
    def cluster(vals):
        out = []
        for v in sorted(vals):
            if not out or v - out[-1] > 1.0:
                out.append(v)
        return out
    return cluster(vxs), cluster(hys)


def _band_of(pos, cuts):
    """pos 落在 cuts 切出的第几段（cuts 升序切点）。"""
    for i, c in enumerate(cuts):
        if pos < c:
            return i
    return len(cuts)


def _iter_segments(block, vxs, hys):
    """把块按矢量线栅格切成段（表格内=单元格级；无跨界线的普通段落原样一块）。
    产出 (bbox, text, meta)；meta=dict(size,font,color)。extract/apply 必须同口径。"""
    bbox = pymupdf.Rect(block["bbox"])
    # 内缩 2pt 再筛切线：表格格线常正好压在文字 bbox 边缘（线与内容相邻），不排除就会在
    # 边缘处凭空多切出一条 0 宽条带，同一句话在 extract/apply 两侧的切分结果就会不一致。
    xs = [x for x in vxs if bbox.x0 + 2 < x < bbox.x1 - 2]
    ys = [y for y in hys if bbox.y0 + 2 < y < bbox.y1 - 2]
    groups = {}  # (bx,by) -> list of (line_idx, span)
    for li, line in enumerate(block.get("lines", [])):
        for s in line.get("spans", []):
            if not s["text"].strip():
                continue
            mx = (s["bbox"][0] + s["bbox"][2]) / 2.0
            my = (s["bbox"][1] + s["bbox"][3]) / 2.0
            groups.setdefault((_band_of(mx, xs), _band_of(my, ys)), []).append((li, s))
    out = []
    for key in sorted(groups):
        items = groups[key]
        rb = pymupdf.Rect()
        for _li, s in items:
            rb |= pymupdf.Rect(s["bbox"])
        txt = " ".join(s["text"] for _li, s in items)
        txt = re.sub(r"\s+", " ", txt).strip()
        if not txt:
            continue
        # 字号取**中位数**、字体/颜色取**最长 span**：一段里常混进上下标、脚注号、图标字体
        # 这类异常小的 span——取均值会被带偏、取首个 span 可能正好取到图标字体，两种都会
        # 让写回的译文比原文大一号或小一号（同一区域字号忽大忽小的来源之一）。
        sizes = sorted(s["size"] for _li, s in items)
        dom = max(items, key=lambda it: len(it[1]["text"]))[1]
        out.append((rb, txt, {"size": sizes[len(sizes) // 2],
                              "font": dom.get("font", ""),
                              "color": dom.get("color", 0)}))
    return out


def _merged_segments(page, vxs, hys):
    """收集整页段并做两级装配（Word 式：一格/一段一译，译文整段流动排版）：
    ① 行装配：同 y 且 x 重叠/相接的碎片合并为一行——Type3 字体常有交叠重复
       绘制（'研发工'+'发工'）、竖切线把一行打断成多片，必须先收编成行，
       否则各碎片拿不同译文塞进同一区域，视觉上必然相撞；
    ② 段落装配：同一格内（或自由文本同栏）垂直相邻的行合并为一段。
    ★ extract/apply 必须同口径调用（键对齐）。"""
    segs = []
    for b in page.get_text("dict")["blocks"]:
        if b.get("type") != 0:
            continue
        if b.get("lines") and b["lines"][0].get("dir", (1, 0)) != (1, 0):
            continue  # 旋转文本 v1 不处理
        for rb, t, meta in _iter_segments(b, vxs, hys):
            segs.append((rb, t, meta))
    if not segs:
        return []

    # ① 行装配
    segs.sort(key=lambda s: (round(s[0].y0, 1), s[0].x0))
    lines = []
    for rb, t, meta in segs:
        merged = False
        if lines:
            cur = lines[-1]
            if abs(rb.y0 - cur["rect"].y0) <= max(2.0, cur["size"] * 0.5) \
                    and rb.x0 - cur["rect"].x1 < 2.0:
                cur["rect"] |= rb
                cur["parts"].append(t)
                if meta["size"] > cur["size"]:
                    cur["size"] = meta["size"]
                if len(t) > len(cur["dom_text"]):
                    cur["dom_text"] = t
                    cur["font"] = meta["font"]
                merged = True
        if not merged:
            lines.append(dict(rect=pymupdf.Rect(rb), parts=[t], size=meta["size"],
                              font=meta["font"], color=meta["color"], dom_text=t))

    # ② 段落装配
    items = []
    for ln in lines:
        txt = re.sub(r"\s+", " ", " ".join(ln["parts"])).strip()
        if not txt:
            continue
        l, r, bt = _cell_bounds(page, ln["rect"], vxs, hys)
        items.append(dict(rect=ln["rect"], text=txt, size=ln["size"],
                          font=ln["font"], color=ln["color"],
                          cell=(None if l is None else round(l, 1),
                                None if r is None else round(r, 1),
                                None if bt is None else round(bt, 1))))
    buckets = {}
    for it in items:
        # 桶键只按格边界：同一格内的换行行必须合并成一段（否则各自往同一格
        # 塞译文必然相撞）。自由文本（无格线）才进一步要求字号/颜色一致。
        buckets.setdefault(it["cell"], []).append(it)
    groups = []
    for key in sorted(buckets, key=repr):
        arr = sorted(buckets[key], key=lambda it: (it["rect"].y0, it["rect"].x0))
        free = key == (None, None, None)
        used = [False] * len(arr)
        for i, it in enumerate(arr):
            if used[i]:
                continue
            used[i] = True
            grp = [it]
            cur = it
            for j in range(i + 1, len(arr)):
                if used[j]:
                    continue
                nx = arr[j]
                if free and (round(nx["size"] * 2) != round(cur["size"] * 2)
                             or nx["color"] != cur["color"]):
                    continue  # 自由文本：字号/颜色不同=不同段落，不合并
                gap = nx["rect"].y0 - cur["rect"].y1
                if gap > cur["size"] * 3.5 + 2.0 or gap < -3.0:
                    continue  # 间距过大=另一段；明显交叠=并列关系
                if nx["rect"].y0 < cur["rect"].y0 + cur["size"] * 0.5:
                    continue  # 必须在当前行下方起头
                if free:
                    ow = (min(cur["rect"].x1, nx["rect"].x1)
                          - max(cur["rect"].x0, nx["rect"].x0))
                    if ow < 0.4 * min(cur["rect"].width, nx["rect"].width):
                        continue  # 自由文本要求换行对齐（旁栏/缩进不合并）
                grp.append(nx)
                used[j] = True
                cur = nx
            ub = pymupdf.Rect(grp[0]["rect"])
            for g2 in grp[1:]:
                ub |= g2["rect"]
            parts = []
            for k2, g2 in enumerate(grp):
                if (k2 and re.search(r"[\u4e00-\u9fff\u3000-\u303f\uff00-\uffef]$", parts[-1])
                        and re.match(r"[\u4e00-\u9fff\u3000-\u303f\uff00-\uffef]", g2["text"])):
                    parts.append(g2["text"])  # CJK 续行不加空格
                else:
                    parts.append((g2["text"] if k2 == 0 else " " + g2["text"]))
            groups.append(dict(rects=[g2["rect"] for g2 in grp], bbox=ub,
                               text=re.sub(r"\s+", " ", "".join(parts)).strip(),
                               size=max(g2["size"] for g2 in grp),
                               font=grp[0]["font"], color=grp[0]["color"]))
    return groups


# ---------------- extract ----------------

# cmd_extract 输出「送翻键序列」：逐页取段（_merged_segments 的行/段两级装配）后
# 按 _norm 去重，保持首次出现顺序。这里的**顺序与去重口径就是全链的段序真值**：
# Go 侧把它当作 texts[] 送翻、工单再按同一下标写 ticket_segments（见 store/segments.go），
# 所以任何改动（加排序、改去重键、换装配规则）都会同时影响提取键、写回命中与对照段号。
def cmd_extract(pdf_path: str) -> int:
    if pymupdf is None:
        # 依赖缺失不抛栈：Go 侧按 JSON.success=false 判错并回退既有提取键（不阻断交付）
        print(json.dumps({"success": False, "error": f"pymupdf 不可用: {_IMPORT_ERR}"}))
        return 1
    doc = pymupdf.open(pdf_path)
    seen, texts = set(), []
    for page in doc:
        vxs, hys = _page_cuts(page)
        for g in _merged_segments(page, vxs, hys):
            k = _norm(g["text"])
            if k and k not in seen:
                # 相同片段只翻一次（跨页重复的表头、页眉页脚尤其多）：
                # 既省 token，也保证 apply 时同键多处都能被替换掉
                seen.add(k)
                texts.append(g["text"])
    # ensure_ascii=False：键多为中文，转 \uXXXX 会让 stdout 体积翻数倍且不利于排障
    print(json.dumps({"success": True, "texts": texts}, ensure_ascii=False))
    return 0


# ---------------- apply ----------------

def _cell_bounds(page, bbox, vxs=(), hys=()):
    """从矢量线推断块所在单元格边界。
    返回 (left, right, bottom)；无对应线则为 None（用 bbox 自身/默认扩高）。
    竖线：与块垂直方向相交的竖线中取左右最近者；右界额外参考栅格切线 vxs
    （格子宽、文字窄时 bbox.x1 离右格线可达上百 pt，固定窗口会漏）。
    水平线：块底下方 30pt 内的横线（限制向下扩高）。"""
    left = right = bottom = None
    y0, y1 = bbox.y0 + 1, bbox.y1 - 1
    for d in page.get_drawings():
        for item in d["items"]:
            segs = []
            if item[0] == "l":
                p1, p2 = item[1], item[2]
                segs.append((p1.x, p1.y, p2.x, p2.y))
            elif item[0] == "re":
                r = item[1]
                segs += [(r.x0, r.y0, r.x0, r.y1), (r.x1, r.y0, r.x1, r.y1),
                         (r.x0, r.y0, r.x1, r.y0), (r.x0, r.y1, r.x1, r.y1)]
            for x1, yy1, x2, yy2 in segs:
                if abs(x1 - x2) < 0.5 and abs(yy1 - yy2) > 4:
                    top, bot = min(yy1, yy2), max(yy1, yy2)
                    if bot < y0 + 1 or top > y1 - 1:
                        continue  # 与块无垂直交集
                    x = x1
                    if x <= bbox.x0 + 1.5 and x >= bbox.x0 - 60:
                        left = x if left is None else max(left, x)
                    elif x >= bbox.x1 - 1.5 and x <= bbox.x1 + 300:
                        right = x if right is None else min(right, x)
                elif abs(yy1 - yy2) < 0.5 and abs(x1 - x2) > 4:
                    lx, rx = min(x1, x2), max(x1, x2)
                    if rx < bbox.x0 + 1 or lx > bbox.x1 - 1:
                        continue  # 与块无水平交集
                    y = yy1
                    if y >= bbox.y1 - 1.5 and y <= bbox.y1 + 30:
                        bottom = y if bottom is None else min(bottom, y)
    if right is None:
        near = [x for x in vxs if bbox.x1 - 1.5 <= x <= bbox.x1 + 300]
        if near:
            right = min(near)
    if left is None:
        near = [x for x in vxs if bbox.x0 - 300 <= x <= bbox.x0 + 1.5]
        if near:
            left = max(near)
    return left, right, bottom


def _int_to_rgb(c: int):
    """PDF span 的 color 是单个 24bit 整数（0xRRGGBB），TextWriter 要 (r,g,b) 浮点三元组。
    不转就不能照搬原文颜色，译文会一律变成黑色（浅色字/彩色强调字当场失真）。"""
    return ((c >> 16 & 255) / 255.0, (c >> 8 & 255) / 255.0, (c & 255) / 255.0)


def _page_font_cache(doc, page):
    """尝试从 PDF 提取内嵌字体 → pymupdf.Font（按 basefont 名缓存）。
    Type3/提取失败/空 buffer 的字体不进缓存（调用方回退内置字体）。
    ★「Boxes」类字体（如 NimbusBoxes）必须拉黑：其所有字形都是实心方块，
    字形覆盖度检查必然通过，一旦被复用译文整段变黑块（实测缺陷）。"""
    cache = {}
    for info in page.get_fonts(full=True):
        # 兼容不同版本元组长度：xref, ext, ftype, basefont, name, encoding, ...
        if len(info) < 4:
            continue
        xref, ext, ftype, basefont = info[0], info[1], info[2], info[3]
        if ext not in ("ttf", "otf", "cff"):
            continue
        if re.search(r"box", basefont or "", re.I):
            continue
        try:
            name, _ext2, _t, buf = doc.extract_font(xref)
        except Exception:
            continue
        if not buf:
            continue
        if re.search(r"box", name or "", re.I):
            continue
        try:
            cache[name or basefont] = pymupdf.Font(fontbuffer=buf)
        except Exception:
            continue
    return cache


def _pick_font(cache, fontname, text, default):
    """原字体优先：能取到且**覆盖译文全部字形**才用；否则回退内置 CJK 字体。"""
    f = cache.get(fontname)
    if f is not None:
        try:
            # 整体判定、不做逐字混排：内嵌子集只带原文用过的字形，译文中一缺字形若走
            # 「按字回退」，同一句里就会一半原件字体一半兜底字体，字重/基线肉眼可见地打架。
            if all(f.has_glyph(ord(ch)) for ch in text):
                return f
        except Exception:
            pass
    return default


def _wrap_lines(text, font, size, width):
    """贪心换行：拉丁按词、CJK 逐字可断。返回行列表。
    词间空白在拼接时还原为单个空格（宽度计入测量）；CJK 连续字间不加空格。"""
    lines, cur, cur_w = [], [], 0.0
    # 空格宽度也按实测取（拼词时要用它补齐词距）
    sp = font.text_length(" ", size)
    prev_sep = True  # 文首无词距
    # 三类 token：①不可断词（含 URL/带连字符缩写，绝不劈开，否则又会出 "Distributor|s"）
    # ②单个空白（只作分隔标记，不单独成行）③任意单字符（CJK 逐字可断）。
    # 宽度全部走 font.text_length 实测，不用「字符数 × 经验宽」估算：同字号下中/英/德差到
    # 30% 以上，估宽必然让单元格内要么越界要么提前折行。
    tokens = re.findall(r"[A-Za-z0-9][A-Za-z0-9\-'.@/:_]*|\s|.", text)
    for tok in tokens:
        if tok.isspace():
            prev_sep = True
            continue
        w = font.text_length(tok, size)
        sep = " " if (cur and prev_sep) else ""
        add = (sp + w) if sep else w
        if cur and cur_w + add > width:
            lines.append("".join(cur))
            cur, cur_w, sep = [tok], w, ""
        else:
            cur.append(sep + tok)
            cur_w += add
        prev_sep = False
    if cur:
        lines.append("".join(cur))
    return lines or [""]


def _obstacles(page):
    """页面障碍矩形（须在抹原文前采集）：全部文本块 + 图片。
    段落向下扩容时可以侵占下方空白，但不能压到这些内容上。"""
    rects = []
    for b in page.get_text("dict")["blocks"]:
        if b.get("type") == 0:
            r = pymupdf.Rect(b["bbox"])
            if r.width > 2 and r.height > 2:
                rects.append(r)
    try:
        for info in page.get_image_info():
            r = pymupdf.Rect(info["bbox"])
            if r.width > 2 and r.height > 2:
                rects.append(r)
    except Exception:
        pass
    return rects


def _grow_bottom(page, bbox, obstacles):
    """bbox 向下扩容的底界：下一个横向重叠内容块的顶 / 页底。
    （Word 式自适应：文字变长 → 换行 → 向下长，直到撞上别的内容为止）"""
    limit = page.rect.height - 2.0
    for r in obstacles:
        if (r + (-0.5, -0.5, 0.5, 0.5)).contains(bbox):
            continue  # 自身/父块不算障碍
        ow = min(bbox.x1, r.x1) - max(bbox.x0, r.x0)
        if ow <= 12:
            continue  # 横向几乎不重叠（旁栏等）不挡道
        # 相邻行 bbox 因字体上/下伸部天然重叠 1~2pt，容差取 3pt——
        # 否则下一行障碍被漏判，段落会一路扩到页底、压扁下方内容
        if bbox.y1 - 3.0 <= r.y0 < limit:
            limit = r.y0
    return limit


def _box_span(page, bbox, left, right, bottom, obstacles, allow_grow):
    """计算排版盒 (x0, width, max_h)。表格线给出硬底界（格子真实高度，不许溢出）；
    无表格线且 allow_grow 时向下扩容到 _grow_bottom（Word 式自适应）。"""
    x0 = max(bbox.x0 + 1, (left + 1.5) if left is not None else bbox.x0 + 1)
    # 右界：有格子时用整格宽——原中文短、译文长时能吃到格内富余宽度（Word 式换行）
    x1 = (right - 1.5) if right is not None else (bbox.x1 - 1)
    # 水平加宽的封顶：同一水平带内的右侧障碍段（同格兄弟段等）挡住扩张
    for r in obstacles:
        if r.x0 >= bbox.x1 - 1 and r.x0 < x1:
            vy = min(bbox.y1, r.y1) - max(bbox.y0, r.y0)
            if vy >= 3.0:
                x1 = r.x0 - 1.5
    if x1 - x0 < 8:
        x0, x1 = bbox.x0 + 0.5, max(bbox.x1 - 0.5, bbox.x0 + 8)
    if bottom is not None:
        max_h = max(bbox.height, bottom - 1.5 - bbox.y0)
    elif allow_grow:
        max_h = max(bbox.height, _grow_bottom(page, bbox, obstacles) - 1.5 - bbox.y0)
    else:
        max_h = bbox.height
    return x0, x1 - x0, max_h


def _place(page, bbox, text, font, size, left, right, bottom, obstacles, writer,
           allow_grow=True):
    """以指定字号、自然行距（1.32）排出译文。返回 True=排下；False=未写入。
    ★ 字号照搬、行距照搬：不缩字号、不压行距；溢出靠换行与向下扩容消化。
    表格内不越格；普通段落可向下扩容。"""
    x0, width, max_h = _box_span(page, bbox, left, right, bottom, obstacles, allow_grow)
    lines = _wrap_lines(text, font, size, width)
    # 行距 1.32 = 原文排版的自然行距，不随挤压变化（缩行距会让整段看起来比原文密）
    lh = size * 1.32
    if len(lines) * lh > max_h + 0.5:
        return False  # 排不下就一个字都不写：交调用方决定兜底，避免「写半截 + 越界」
    # 首行基线下压约一个字号：PDF 文字定位用的是基线，直接写 bbox.y0 会让译文整体上移、
    # 与同行的图标/相邻格文字对不齐（1.02 是含上伸部的经验偏移）
    y = bbox.y0 + size * 1.02
    for ln in lines:
        writer.append((x0, y), ln, font=font, fontsize=size)
        y += lh
    return True


def _force_place(bbox, text, font, size, width, writer):
    """最后手段：原字号、原行距无条件写出（可能自然外溢出格子下缘）。
    字号行距照搬原则下绝不缩、绝不压、绝不丢文。"""
    lines = _wrap_lines(text, font, size, width)
    lh = size * 1.32
    y = bbox.y0 + size * 1.02
    for ln in lines:
        writer.append((bbox.x0 + 1, y), ln, font=font, fontsize=size)
        y += lh


def _sampled_min_levels(doc, xref):
    """采样图片像素，返回 (每像素各彩色分量最小值列表的采样结果, comps, n)。
    供合成深度计算用；异常返回 None。"""
    try:
        pix = pymupdf.Pixmap(doc, xref)
    except Exception:
        return None
    n, al = pix.n, pix.alpha
    comps = max(1, n - al)
    data = pix.samples
    total = pix.width * pix.height
    step = max(1, total // 20000)
    mins = []
    for i in range(0, total, step):
        b = i * n
        mins.append(min(data[b:b + comps]))
    return mins


def _blank_faint_images(doc):
    """浅色水印图整链清除（直接扫 xref，含嵌在 Form XObject 里的切片）：
    ① 满足去水印诉求（与 docx 链 strip_watermark_fragments 同口径）；
    ② 防止 apply_redactions 重写页面内容流时丢图片 SMask，把透明黑水印
       渲染成实心黑块（实测缺陷）。
    水印形态两种都判：a) 无掩码但底图整体很浅；b) 深色底图 + SMask 掩码，
    按「合成后有效深度」判定（透明黑水印合成到白底后依然很浅）。
    只处理小尺寸图片（水印切片）；大图是真截图/插图，不碰。"""
    n = 0
    for x in range(1, doc.xref_length()):
        try:
            if doc.xref_get_key(x, "Subtype")[1] != "/Image":
                continue
            w, h = doc.xref_get_key(x, "Width")[1], doc.xref_get_key(x, "Height")[1]
            if not (w.isdigit() and h.isdigit()) or int(w) > 640 or int(h) > 640:
                continue
            base = _sampled_min_levels(doc, x)
            if not base:
                continue
            faint = False
            if min(base) >= 190:
                faint = True  # 无掩码浅色图
            else:
                sm = doc.xref_get_key(x, "SMask")
                if sm[0] == "xref":
                    try:
                        sx = int(sm[1].split()[0])
                    except Exception:
                        sx = 0
                    smask = _sampled_min_levels(doc, sx) if sx else None
                    if smask and len(smask) == len(base):
                        # 有效深度 = 255 - smask×(255-base)/255（白底合成）
                        eff = [255 - s * (255 - b) / 255.0
                               for b, s in zip(base, smask)]
                        if eff and min(eff) >= 190:
                            faint = True  # 深底+浅掩码=透明黑水印
            if not faint:
                continue
            # ★ 不改图片内容，挂 1×1 全透明 SMask 使整图完全透明。
            #   千万不能换成「1×1 白色」：图片仍按原位置平铺拉伸（数千个放置位
            #   铺满页面），白色贴片会把画在图片之下的表格边框/底纹全部盖住
            #   （实测表格线消失）；透明 SMask 则彻底退出视觉且与 z-order 无关。
            try:
                sx = doc.get_new_xref()
                doc.update_object(
                    sx, "<< /Type /XObject /Subtype /Image /Width 1 /Height 1 "
                        "/ColorSpace /DeviceGray /BitsPerComponent 8 >>")
                doc.update_stream(sx, b"\x00")
                doc.xref_set_key(x, "SMask", "%d 0 R" % sx)
                doc.xref_set_key(x, "Mask", "null")
                n += 1
            except Exception:
                continue
        except Exception:
            continue
    return n


# cmd_apply 原地替换写回（唯一产出交付 PDF 的入口）。
# 参数：in_pdf=原始 PDF（绝不接受中间 DOCX）；out_pdf=产物路径；lang=目标语言（当前只作
#       字体/换行策略的上下文，不参与键匹配）；payload=stdin 传入的
#       {"translations": {源文段: 译文段}} JSON 字节。
# 流程：① 清水印切片 → ② 逐页用与 extract 完全同源的 _merged_segments 重切一遍，
#       按 _norm 后的键查译文 → ③ add_redact_annot + apply_redactions 只抹文字层
#       （图片/矢量/表格线原样保留）→ ④ TextWriter 按原 bbox / 原字号 / 原颜色嵌回译文。
# 坑点（改动前务必读）：
#   · 切分口径改一处必须两侧同改：apply 是「重新切一遍再对键」，不是「复用 extract 记下的框」，
#     口径不一致就整体不命中，产物看起来像「翻译没生效」。
#   · 障碍矩形（page_obs）必须在 apply_redactions **之前**采集：抹掉原文后文本块不再存在，
#     届时采不到下方相邻段的位置，段落向下扩容就会压到别的内容上。
#   · 零命中不报错：没有 job 的页面直接跳过，进程仍退出 0、产物等于原件——Go 侧只拿得到
#     err==nil，故不能把「成功退出」当成「译文已替换」（命中数看 stdout 的 replaced=）。
def cmd_apply(in_pdf: str, out_pdf: str, lang: str, payload: bytes) -> int:
    if pymupdf is None:
        sys.stderr.write(f"pymupdf 不可用: {_IMPORT_ERR}\n")
        return 1
    data = json.loads(payload or b"{}")
    translations = data.get("translations") or {}
    tmap = {}
    for k, v in translations.items():
        kk = _norm(k)
        # 「首个非空者胜」：上游是 原文→译文 的 map，归一化后可能撞出同一个键（只差空白与
        # 零宽字符），若让后到的覆盖先到的，一条空译文/占位串就能把已翻好的那条抹掉。
        if kk and kk not in tmap and v and v.strip():
            # 块元素字符（█░▒▓ 等）清洗成空格：上游数据可能带这类「假涂黑」字符，
            # TextWriter 对缺失字形自动回退字体会把它画成实心黑块（实测缺陷）
            v = re.sub(r"[\u2580-\u259F\u25A0-\u25A1\u2592\u2591\u2593]+", " ", v)
            tmap[kk] = re.sub(r"\s+", " ", v).strip()

    doc = pymupdf.open(in_pdf)
    wm = _blank_faint_images(doc)  # ★ 抹除前先清水印切片（防 SMask 黑块）
    default_font = pymupdf.Font(fontfile=FALLBACK_FONT)
    replaced = overflow = requested_total = 0
    for page in doc:
        fcache = _page_font_cache(doc, page)
        vxs, hys = _page_cuts(page)
        # ★ 逻辑段落合并（与 extract 同口径）：一格/一段一译，换行段拼接后整段流动
        groups = _merged_segments(page, vxs, hys)
        jobs = []
        for g in groups:
            tr = tmap.get(_norm(g["text"]))
            if not tr:
                continue
            jobs.append((g, tr))
        requested_total += len(jobs)
        if not jobs:
            continue
        # ① 抹原文（只删文字；图片保持）。★ 障碍矩形必须在抹除前采集。
        page_obs = []
        for g in groups:
            page_obs.extend(g["rects"])
        try:
            for info in page.get_image_info():
                r = pymupdf.Rect(info["bbox"])
                if r.width > 2 and r.height > 2:
                    page_obs.append(r)
        except Exception:
            pass
        for g, _tr in jobs:
            for rb in g["rects"]:
                page.add_redact_annot(rb)
        page.apply_redactions(images=pymupdf.PDF_REDACT_IMAGE_NONE)
        # ② 同位嵌回。★ 字号照搬原则（2026-09 定稿）：译文字号 = 原文字号，
        #   全文档绝不缩放；溢出一律自动换行；换行排不下时压行距（最低 0.85）；
        #   仍排不下才允许轻微越出格子下缘（_force_place），绝不缩字、绝不丢文。
        by_color = {}
        placed_at = {}  # 同键且 bbox 交叠的重复绘制文本（伪粗体/图层重复）只排一次
        for g, tr in jobs:
            bbox = g["bbox"]
            left, right, bottom = _cell_bounds(page, bbox, vxs, hys)
            font = _pick_font(fcache, g["font"], tr, default_font)
            # 自身组的成员矩形不算障碍（否则段落永远无法向下扩容）
            own = set(tuple(round(v, 1) for v in rb) for rb in g["rects"])
            obs = [r for r in page_obs
                   if tuple(round(v, 1) for v in r) not in own
                   and not (r + (-0.5, -0.5, 0.5, 0.5)).contains(bbox)]
            # 按颜色分组各起一个 TextWriter：write_text 的 color 是整批生效的，
            # 混在一个 writer 里会让全页译文统一染上第一种颜色（彩色强调字失真）。
            writer = by_color.setdefault(_int_to_rgb(g["color"]),
                                         pymupdf.TextWriter(page.rect))
            rlist = placed_at.setdefault(_norm(g["text"]), [])
            if any(not (r & bbox).is_empty for r in rlist):
                continue  # 重复绘制：译文已在此处排过，跳过（原文仍全部抹除）
            if _place(page, bbox, tr, font, g["size"], left, right, bottom,
                      obs, writer):
                rlist.append(pymupdf.Rect(bbox))
                replaced += 1
            else:
                # _place 返回 False 只代表「按原字号+自然行距排不进这个盒子」，它没写任何东西；
                # 这里改为无条件写出（宁可轻微越出格子下缘），也不缩字号、不删句子。
                # 行距压到 0.85 仍排不下：原字号无条件写出（轻微越出下缘）
                x0 = max(bbox.x0 + 1, (left + 1.5) if left is not None else bbox.x0 + 1)
                width = ((right - 1.5) if right is not None else (bbox.x1 - 1)) - x0
                _force_place(bbox, tr, font, g["size"], width, writer)
                rlist.append(pymupdf.Rect(bbox))
                replaced += 1
                overflow += 1
        for color, writer in by_color.items():
            writer.write_text(page, color=color)
    doc.save(out_pdf, garbage=3, deflate=True)
    # ★ P0-7（2026-09-18）：口径收口——stdout 增加 requested=（收到的译文条数），
    #   Go 侧据此判定「零命中/低命中」，不再只看退出码；旧的 return 后不可达残留打印已删。
    print(f"OK: {out_pdf} replaced={replaced} overflow={overflow} requested={requested_total} wm_blanked={wm}")
    return 0


# ---------------- selftest ----------------

def _run_capture(argv):
    """selftest 辅助：进程内跑子命令并捕获 stdout JSON。"""
    import io as _io
    old = sys.stdout
    sys.stdout = _io.StringIO()
    try:
        cmd_extract(argv[1])
        return sys.stdout.getvalue()
    finally:
        sys.stdout = old


def cmd_selftest() -> int:
    """合成表格夹具回归。退出码 0/1/2（2=依赖缺失，CI 跳过）。"""
    if pymupdf is None:
        sys.stderr.write("SKIP: pymupdf 不可用\n")
        return 2
    fails = []

    def ck(cond, msg):
        # 断言收集器而非即时 assert：跑完全部检查一次性报，一次就能看清「几处回归、
        # 是否同一根因」（改切分规则时尤其省时间，不必改一次跑一次）。
        if not cond:
            fails.append(msg)

    try:
        import tempfile
        tmp = tempfile.mkdtemp(prefix="pdfov_")
        src = str(Path(tmp) / "fix.pdf")
        out = str(Path(tmp) / "out.pdf")

        doc = pymupdf.open()
        page = doc.new_page(width=595, height=842)
        font = pymupdf.Font(fontfile=FALLBACK_FONT)
        # 画一个 2 列 × 2 行表格（矢量矩形 = 单元格边界）。
        # ★ 格子间必须留空隙：相邻格会被 PyMuPDF 合并成一个块（生产 extract/apply
        #   同口径键自会对齐，但夹具需要「一格=一块」才能做逐格越界断言）。
        cells = {
            "r1c1": pymupdf.Rect(60, 100, 290, 160),
            "r1c2": pymupdf.Rect(320, 100, 540, 160),
            "r2c1": pymupdf.Rect(60, 210, 290, 270),
            "r2c2": pymupdf.Rect(320, 210, 540, 270),
        }
        for r in cells.values():
            page.draw_rect(r, color=(0, 0, 0), width=0.8)
        # 格内中文（用内置字体嵌入，模拟真实产物）。
        # r2c1 拆成同格两行：验证「行装配→段落合并」把一格两行并成一段译文。
        zh = {"r1c1": "手机车控与研发工单系统",
              "r1c2": "工单贴中文包勾选语种定稿回写系统",
              "r2c1a": "海外版跟国内同周",
              "r2c1b": "发警示句零错误",
              "r2c2": "门店随查随用不靠邮件发PDF文件"}
        for k in ("r1c1", "r1c2", "r2c2"):
            r = cells[k]
            page.insert_text((r.x0 + 4, r.y0 + 30), zh[k], fontfile=FALLBACK_FONT,
                             fontname="F0", fontsize=11)
        rc1 = cells["r2c1"]
        page.insert_text((rc1.x0 + 4, rc1.y0 + 16), zh["r2c1a"],
                         fontfile=FALLBACK_FONT, fontname="F0", fontsize=11)
        page.insert_text((rc1.x0 + 4, rc1.y0 + 32), zh["r2c1b"],
                         fontfile=FALLBACK_FONT, fontname="F0", fontsize=11)
        # 浅色小图（模拟水印切片，最深像素 245 ≥190）：验证 apply 后整图挂
        # 全透明 SMask 隐身，而不是换成白色贴片盖住下层矢量
        wm_pix = pymupdf.Pixmap(pymupdf.csRGB, pymupdf.IRect(0, 0, 24, 24))
        wm_pix.clear_with(245)
        page.insert_image(pymupdf.Rect(430, 700, 500, 760), pixmap=wm_pix)
        # 第 2 页：自由段落（无表格）——验证「换行 + 向下扩容」保持原字号。
        # 段落要足够宽（模拟真实多行段落），否则窄盒会逼出缩字号、测不到扩容。
        pg2 = doc.new_page(width=595, height=842)
        pg2.insert_text((60, 130), "产品简介与核心功能亮点一览涵盖工单贴中文包勾选语种定稿回写与门店随查随用",
                        fontfile=FALLBACK_FONT, fontname="F0", fontsize=11)
        pg2.insert_text((60, 330), "下方锚点段落占位内容", fontfile=FALLBACK_FONT,
                        fontname="F0", fontsize=11)
        doc.save(src)
        doc.close()

        # 译文键取自模块自己的 extract（与生产同口径：Go 拿 extract 的键喂 LLM）
        ex = json.loads(_run_capture(["extract", src]))
        ck(ex.get("success") and len(ex["texts"]) == 6,
           "extract 应出 6 段，实际 %d" % len(ex.get("texts", [])))
        en_pool = [
            "Vehicle control and R and D work order system integration platform for overseas markets",
            "Paste the Chinese package select languages and write the final version back automatically",
            "Overseas versions ship in the same week as domestic ones with zero translation errors",
            "Stores can search on demand without relying on email attachments at all times",
        ]
        # 中译英典型场景：译文远长于原文 → 只译自由段落为超长英文（须靠扩容而非缩字），
        # 锚点段保持原文不动，作为固定障碍物
        long_en = ("Vehicle control and R and D work order system integration platform "
                   "for overseas markets with one click distribution paste the Chinese "
                   "package select languages and write the final version back to stores "
                   "automatically every week without any email attachments at all") * 2
        tr = {}
        for i, t in enumerate(ex["texts"]):
            if "产品简介" in t:
                tr[t] = long_en
            elif i < 4:
                tr[t] = en_pool[i % len(en_pool)]
        # 给一个格子塞块元素字符：验证 tmap 构建时把 █ 清洗为空格（防止
        # TextWriter 字形回退画出实心黑块）
        tr[ex["texts"][1]] = tr[ex["texts"][1]] + " ███"
        rc = cmd_apply(src, out, "en", json.dumps({"translations": tr}).encode())
        if rc != 0:
            sys.stderr.write("FAIL: apply 退出码 %d\n" % rc)
            return 1

        outdoc = pymupdf.open(out)
        pg = outdoc[0]
        txt = pg.get_text()

        # ① 译文落地（文本层可提取）
        for v in tr.values():
            ck(v.split()[0] in txt, "译文未落地: %s…" % v[:30])
        # ② 原文已抹
        for v in zh.values():
            ck(v not in txt, "原文未抹除: %s" % v)
        # ③ 不越界：所有英文词的 bbox 必须落在各自单元格外扩 2pt 内
        word_boxes = pg.get_text("words")  # (x0,y0,x1,y1,word,...)
        cell_of = {}
        for v in tr.values():
            first = v.split()[0]
            cell_of[first] = v
        for w in word_boxes:
            wr = pymupdf.Rect(w[:4])
            if not re.search(r"[A-Za-z]{2,}", w[4]):
                continue
            # 找到包含该词的格（外扩 2.5pt）
            hit = None
            for k, r in cells.items():
                if (r + 2.5).contains(wr):
                    hit = r
                    break
            if hit is None:
                # 是否属于任一格所在行列的溢出？
                inside_any = any(r.contains(wr) for r in cells.values())
                ck(inside_any, "英文词越界: %r at %s" % (w[4], tuple(round(x) for x in w[:4])))
        # ④ 表格矢量线保留（drawing 数不减少）
        srcdoc = pymupdf.open(src)
        n_src = len(srcdoc[0].get_drawings())
        n_out = len(pg.get_drawings())
        ck(n_out >= n_src, "矢量图形丢失: src=%d out=%d" % (n_src, n_out))
        # ⑤ 字体嵌入（Type0/TTF 存在）
        fonts = pg.get_fonts()
        ck(any(f[2] in ("Type0", "TrueType", "Type1") for f in fonts),
           "无嵌入字体: %s" % fonts[:3])
        # ⑥ 区域字号统一：四格同原字号（11pt 桶），译文字号必须一致（±0.5pt）——
        #    守护「同一区域大大小小」回归（独立缩放会让长句格 4pt、短句格 11pt）
        en_sizes = []
        for bl in pg.get_text("dict")["blocks"]:
            if bl.get("type") != 0:
                continue
            for l in bl.get("lines", []):
                for s in l.get("spans", []):
                    if re.search(r"[A-Za-z]{3,}", s["text"]):
                        en_sizes.append(round(s["size"], 2))
        ck(len(en_sizes) >= 4 and (max(en_sizes) - min(en_sizes)) <= 0.5,
           "同区域译文字号不统一: %s" % sorted(set(en_sizes)))
        # ⑦ Word 式向下扩容：自由段落超长译文保持原字号（靠换行+扩容而非缩字号），
        #    且不压到下方锚点原文
        pg2 = outdoc[1]
        p2_sizes, p2_maxy = set(), 0.0
        for bl in pg2.get_text("dict")["blocks"]:
            if bl.get("type") != 0:
                continue
            for l in bl.get("lines", []):
                for s in l.get("spans", []):
                    if re.search(r"[A-Za-z]{3,}", s["text"]):
                        p2_sizes.add(round(s["size"], 2))
                        p2_maxy = max(p2_maxy, s["bbox"][3])
        anchor_top = None
        for bl in srcdoc[1].get_text("dict")["blocks"]:
            for l in bl.get("lines", []):
                for s in l.get("spans", []):
                    if "下方锚点" in s["text"]:
                        anchor_top = s["bbox"][1]
        ck(p2_sizes == {11.0},
           "自由段落未保持原字号(未向下扩容?): %s" % sorted(p2_sizes))
        ck(anchor_top is not None and p2_maxy <= anchor_top + 0.5,
           "扩容译文压到下方锚点: max_y=%.1f anchor_top=%s" % (p2_maxy, anchor_top))

        if fails:
            for m in fails:
                sys.stderr.write("SELFTEST FAIL: %s\n" % m)
            return 1
        print("pdf_overlay selftest OK")
        return 0
    except Exception as e:
        import traceback
        traceback.print_exc()
        sys.stderr.write("SELFTEST ERROR: %s\n" % e)
        return 1


# main 子命令分派（生产只走 extract / apply 两条，selftest 由 go test 驱动）。
# 参数不足或子命令未知 → 退出码 2 并把模块 docstring 打到 stderr：
#   · Go 侧看到的是非零退出，绝不会把「用法打印」误判成交付成功；
#   · selftest 侧 2 专指「依赖缺失/环境不满足 → 跳过」，两种语义都不落成失败。
# apply 的译文 JSON 从 stdin 读（体积大且含客户原文，不进 argv），isatty 兜住手工直跑。
def main():
    if len(sys.argv) < 2:
        sys.stderr.write(__doc__)
        return 2
    cmd = sys.argv[1]
    if cmd == "extract":
        return cmd_extract(sys.argv[2])
    if cmd == "apply":
        payload = sys.stdin.buffer.read() if not sys.stdin.isatty() else b"{}"
        return cmd_apply(sys.argv[2], sys.argv[3], sys.argv[4] if len(sys.argv) > 4 else "", payload)
    if cmd == "selftest":
        return cmd_selftest()
    sys.stderr.write("未知子命令: %s\n" % cmd)
    return 2


if __name__ == "__main__":
    sys.exit(main())
