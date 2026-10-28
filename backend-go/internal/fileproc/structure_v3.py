#!/usr/bin/env python3
"""structure_v3.py — 版面/表格结构识别子命令（S2：真推理 + 零文本边界）。

边界（主方案 §6 与《接入方案_结构保真》）：
  **只取结构，不取识别**：输出里**没有任何 text 字段**；文本仍然只听我们自己的 OCR。
  两条子命令：
    probe        自检 paddleocr 与 assets/structure 权重；`OK: version=… models=… layout=… table=…`
    page <img>   单页版面/表格几何；stdout 一行 JSON（形状逐字对齐 Go 侧 structureWire）

输出形状（Go 侧 structure_v3.go 的 structureWire，只增不改）：
  {"blocks":[{"label":"table","box":[x0,y0,x1,y1],"order":0}],
   "tables":[{"box":[…],"rows":2,"cols":3,
              "cells":[{"r":0,"c":0,"rowspan":1,"colspan":1,"box":[…]}],
              "html":"<table>…</table>"}]}
  · `box` 一律四值矩形 [x0,y0,x1,y1]（八值及以上多边形在这里归一到包围盒）；
  · `order` ＝阅读顺序（先 y 后 x，按版面块中心排序后重编号）；
  · `html` 是结构表自身的标记，**不含识别文本**（spike S4 逐模型核过：表格结构模型的 HTML 只带
    `<td rowspan colspan>` 网格骨架）——若哪天某个版本的 HTML 里带文字，Go 侧 hasTextKey 会在
    解析期判 BadInput，这正是我们希望它当场暴露的形态。

依赖与权重解析（缺哪个报哪个）：
  · 版面 = `paddleocr.LayoutDetection(model_dir=…)`
  · 表格 = `paddleocr.TableStructureRecognition(model_dir=…)`
  · 目录优先级：`STRUCTURE_<KIND>_MODEL_DIR` > `<脚本目录>/assets/structure/<kind>` > `assets/structure`（平铺）
  · 表读法：`Result.json` → `{"res": …}`；`res.boxes[]` 带 `label/score/coordinate`；
    表格 `res.logic_points` 逐格 4 个整数 `[row_start, col_start, row_end, col_end]`（含端点）＋
    `res.cell_bboxes` 同序几何；只有 `html` 时按 `<td rowspan colspan>` 网格反解。
    **形状不认识就报 Unavailable:shape**（版本不匹配），绝不猜一个空结构出来。

失败三档（对外排障契约，Go 侧映射成哨兵错误）：
  BadInput    文件不存在／不是可解码的图片
  Timeout     超出内部预算 `STRUCTURE_PAGE_BUDGET_SEC`（缺省 0＝不设；Go 侧墙钟才是真正的闸）
  Unavailable 依赖缺失／权重缺失／形状不认识／推理抛异常
  ⇒ 空结构不许当成功：真的一页没块时由 Go 侧判 ErrStructureEmpty，那也是"看得见的红"。

⚠️ 两段式交付：本文件是"代码 + 协议级自证"那一半——`structure_page_selftest.py` 用**假 paddleocr**
   把六个形状契约逐条钉住（零权重、零 GPU，CI 与本机都跑）；"权重 + S0–S5 六条读数"必须在部署机
   执行（《接入方案》§10/§11），两者都做完才允许 `OCR_STRUCTURE=on`。
"""
import importlib.util
import json
import os
import re
import sys
import tempfile
import time
from pathlib import Path

ASSETS_DIR = Path(__file__).resolve().parent / "assets" / "structure"
# 权重名清单以 spike S4 定稿为准；这里只要求"目录里至少一个 .onnx"（与 healthcheck 同一把尺子）。
MODEL_GLOB = "*.onnx"
# 版面块标签里哪些算表格（PP-StructureV3 的版面模型用 "table"）。
TABLE_LABELS = {"table"}
# logic_points 网格坐标的合理上界：超过它就不像"第几行/第几列"，按形状不认识处理。
GRID_MAX = 500


def _err(variant: str, msg: str) -> int:
    """按统一协议向 stderr 打 ERR:Structure:<variant>:<msg>（Go 侧解析成档位）。"""
    print("ERR:Structure:%s:%s" % (variant, msg), file=sys.stderr)
    return 2


def _budget_sec() -> float:
    """内部预算（秒）。0/缺省＝不设：真实的超时闸是 Go 侧的 context deadline。"""
    try:
        return float(os.environ.get("STRUCTURE_PAGE_BUDGET_SEC", "0") or 0)
    except ValueError:
        return 0.0


def _over_budget(t0: float) -> bool:
    budget = _budget_sec()
    return budget > 0 and (time.time() - t0) > budget


def _assets():
    """权重文件全集：平铺（`*.onnx`）与分档（`layout/`、`table/` 各一份）两种部署形态都算。

    为什么两种都认：分档是 spike S4 定的投放形态，平铺是历史上"先丢一份上去看看"的形态；
    只认一种的后果是 probe 说"没权重"而 page 能跑（或反过来）——两把尺子各说各话。
    """
    try:
        return sorted(list(ASSETS_DIR.glob(MODEL_GLOB)) + list(ASSETS_DIR.glob("*/" + MODEL_GLOB)))
    except OSError:
        return []


def _resolve_model_dir(kind: str):
    """按三级优先级解析某一路模型目录；解析不到回 None（调用方按"缺哪个报哪个"报 Unavailable）。"""
    env = os.environ.get("STRUCTURE_%s_MODEL_DIR" % kind.upper(), "").strip()
    if env:
        return Path(env)
    sub = ASSETS_DIR / kind
    if sub.is_dir() and any(sub.iterdir()):
        return sub
    if _assets():
        return ASSETS_DIR  # 平铺部署（assets/structure/*.onnx 直接放）
    return None


def cmd_probe() -> int:
    """两态 + 三档读数：版本变了但 probe 仍 OK ⇒ 上层按"读数不可比"降级（见 Go 侧）。"""
    if importlib.util.find_spec("paddleocr") is None:
        print("MISSING: paddleocr 未安装（结构腿可选，缺失不影响 OCR/翻译）")
        return 3
    try:
        import paddleocr  # noqa: F401 —— probe 只取版本，不做推理
        ver = str(getattr(paddleocr, "__version__", "") or "-")
    except Exception as e:  # noqa: BLE001 —— 导入失败即"没就绪"，不是崩溃
        print("MISSING: paddleocr 导入失败: %s" % e)
        return 3
    models = _assets()
    if not models:
        print("MISSING: assets/structure/*.onnx 缺失（取权重见 assets/structure/README.md）")
        return 3
    layout_dir = _resolve_model_dir("layout")
    table_dir = _resolve_model_dir("table")
    if layout_dir is None or table_dir is None:
        print("MISSING: 结构模型目录不全（layout=%s table=%s）"
              % ("-" if layout_dir is None else layout_dir.name,
                 "-" if table_dir is None else table_dir.name))
        return 3
    # layout=/table= 是**实际会被推理用到的那个目录名**，不是"我以为叫什么"——
    # 排障时它回答的是「这台机器加载的是哪一份权重」。
    print("OK: version=%s models=%d layout=%s table=%s"
          % (ver, len(models), layout_dir.name, table_dir.name))
    return 0


# ---------------------------------------------------------------- 形状归一（唯一适配层）

def _as_dict(obj):
    """把 paddleocr 3.x 的 Result 对象归一成 dict：只认 `json` 属性与 dict 本身。"""
    cur = obj
    j = getattr(cur, "json", None)
    if callable(j):
        try:
            j = j()
        except Exception:  # noqa: BLE001 —— 形状不认识由调用方统一报
            return None
    if isinstance(j, dict):
        cur = j
    if isinstance(cur, dict) and isinstance(cur.get("res"), dict):
        cur = cur["res"]
    return cur if isinstance(cur, dict) else None


def _norm_box(box):
    """任意框形态 → 四值矩形 [x0,y0,x1,y1]；不足四点回 None（坏行跳过，不猜）。"""
    if not isinstance(box, (list, tuple)) or len(box) < 4:
        return None
    try:
        nums = [float(v) for v in box]
    except (TypeError, ValueError):
        return None
    xs = nums[0::2]
    ys = nums[1::2]
    if not xs or not ys:
        return None
    return [min(xs), min(ys), max(xs), max(ys)]


def _layout_rows(res):
    """版面结果 → [{label, box, score}]；形状不认识抛 ValueError（由 cmd_page 报 Unavailable）。"""
    items = [res] if isinstance(res, dict) else (list(res) if isinstance(res, (list, tuple)) else None)
    if items is None:
        raise ValueError("版面输出既不是 dict 也不是 list")
    rows = []
    for item in items:
        d = _as_dict(item)
        if d is None:
            raise ValueError("版面 Result 形状不认识")
        boxes = d.get("boxes")
        if boxes is None:
            inner = d.get("layout_det_res")
            boxes = inner.get("boxes") if isinstance(inner, dict) else None
        if boxes is None:
            raise ValueError("版面输出里没有 boxes 键")
        for b in boxes:
            if not isinstance(b, dict):
                continue
            box = _norm_box(b.get("coordinate") or b.get("box") or b.get("bbox"))
            if box is None:
                continue
            rows.append({
                "label": str(b.get("label") or b.get("category") or "-"),
                "box": box,
                "score": float(b.get("score") or 0),
            })
    return rows


def _grid(lp):
    """logic_points 单项 → (row_start, col_start, row_end, col_end)；不像网格坐标回 None。"""
    if not isinstance(lp, (list, tuple)) or len(lp) < 4:
        return None
    try:
        vals = [int(v) for v in lp[:4]]
        if [float(v) for v in lp[:4]] != [float(v) for v in vals]:
            return None  # 带小数＝像素坐标，不是网格坐标
    except (TypeError, ValueError):
        return None
    r0, c0, r1, c1 = vals
    if min(vals) < 0 or max(vals) > GRID_MAX:
        return None
    if r1 < r0 or c1 < c0:
        return None
    return r0, c0, r1, c1


_TD_RE = re.compile(r"<t[dh]\b([^>]*)>", re.I)
_TR_RE = re.compile(r"<tr\b[^>]*>(.*?)</tr>", re.I | re.S)


def _span_attr(attrs: str, name: str) -> int:
    m = re.search(r"%s\s*=\s*[\"']?(\d+)" % name, attrs, re.I)
    if not m:
        return 1
    return max(1, int(m.group(1)))


def _parse_html_grid(html: str):
    """只有 HTML 时按 `<td rowspan colspan>` 反解网格 → (rows, cols, cells)；解不出回 None。"""
    rows_html = _TR_RE.findall(html or "")
    if not rows_html:
        return None
    occupied = set()
    cells = []
    ncols = 0
    for r, row in enumerate(rows_html):
        c = 0
        for m in _TD_RE.finditer(row):
            attrs = m.group(1)
            rs = _span_attr(attrs, "rowspan")
            cs = _span_attr(attrs, "colspan")
            while (r, c) in occupied:
                c += 1
            for dr in range(rs):
                for dc in range(cs):
                    occupied.add((r + dr, c + dc))
            # HTML-only 档没有单元格几何：box 全零是**如实**的"不知道坐标"，
            # 上层用 html 重建，不会拿零框去本轮识别（那条路走 OCR 自己的行框）。
            cells.append({"r": r, "c": c, "rowspan": rs, "colspan": cs, "box": [0.0, 0.0, 0.0, 0.0]})
            c += cs
            ncols = max(ncols, c)
    if not cells:
        return None
    nrows = max(c["r"] + c["rowspan"] for c in cells)
    cols = max(ncols, max(c["c"] + c["colspan"] for c in cells))
    return nrows, cols, cells


def _table_from_result(res, table_box):
    """表格结果 → wire 的 table 项；形状不认识回 None（由 cmd_page 报 Unavailable:shape）。"""
    items = [res] if isinstance(res, dict) else (list(res) if isinstance(res, (list, tuple)) else None)
    if not items:
        return None
    d = _as_dict(items[0])
    if d is None:
        return None
    if isinstance(d.get("table_res_list"), list):
        lst = [x for x in d["table_res_list"] if isinstance(x, dict)]
        if not lst:
            return None
        d = lst[0]
    html = str(d.get("html") or d.get("pred_html") or d.get("table_html") or "")
    bboxes = d.get("cell_bboxes") or d.get("cells") or []
    logic = d.get("logic_points") or d.get("logic_pts") or []
    rows = int(d.get("rows") or 0)
    cols = int(d.get("cols") or 0)
    cells = []
    for i, lp in enumerate(logic if isinstance(logic, (list, tuple)) else []):
        g = _grid(lp)
        if g is None:
            cells = []
            break  # logic 不是网格坐标 ⇒ 改走 html（不掺半套读数）
        r0, c0, r1, c1 = g
        box = _norm_box(bboxes[i]) if i < len(bboxes) else None
        cells.append({
            "r": r0, "c": c0,
            "rowspan": r1 - r0 + 1, "colspan": c1 - c0 + 1,
            "box": box or [0.0, 0.0, 0.0, 0.0],
        })
    if cells:
        rows = max(rows, max(c["r"] + c["rowspan"] for c in cells))
        cols = max(cols, max(c["c"] + c["colspan"] for c in cells))
    elif html:
        g = _parse_html_grid(html)
        if not g:
            return None
        rows, cols, cells = g
    if not cells or rows <= 0 or cols <= 0:
        return None
    return {"box": table_box, "rows": rows, "cols": cols, "cells": cells, "html": html}


def _has_text_key(v) -> bool:
    """递归扫描**出栈** JSON 是否出现 text/rec_text/rec_texts 键（与 Go 侧 hasTextKey 同口径）。"""
    if isinstance(v, dict):
        for k, child in v.items():
            if str(k).lower() in ("text", "rec_text", "rec_texts"):
                return True
            if _has_text_key(child):
                return True
    elif isinstance(v, (list, tuple)):
        for child in v:
            if _has_text_key(child):
                return True
    return False


def cmd_page(path: str) -> int:
    """单页结构：版面 → 逐表裁剪 → 表格结构 → 一行 JSON。"""
    t0 = time.time()
    if not os.path.exists(path) or not os.path.isfile(path):
        return _err("BadInput", "文件不存在: %s" % path)
    if importlib.util.find_spec("paddleocr") is None:
        return _err("Unavailable", "paddleocr 未安装")
    layout_dir = _resolve_model_dir("layout")
    if layout_dir is None:
        return _err("Unavailable", "缺 layout 模型目录（%s/layout 或 %s/*.onnx）" % (ASSETS_DIR.parent.name, ASSETS_DIR.name))
    table_dir = _resolve_model_dir("table")
    if table_dir is None:
        return _err("Unavailable", "缺 table 模型目录（%s/layout|table 或 %s/*.onnx）"
                    % (ASSETS_DIR.name, ASSETS_DIR.name))
    try:
        from PIL import Image
        img = Image.open(path)
        img.load()
    except Exception as e:  # noqa: BLE001 —— 坏图就是坏图，不许当"没版面"
        return _err("BadInput", "不是可解码的图片: %s" % e)

    try:
        import paddleocr
        layout = paddleocr.LayoutDetection(model_dir=str(layout_dir))
        rows = _layout_rows(layout.predict(input=str(path)))
    except Exception as e:  # noqa: BLE001 —— 依赖/权重/版本一律 Unavailable（不静默回空结构）
        return _err("Unavailable", "版面推理失败（依赖/权重/版本）: %s" % e)
    if _over_budget(t0):
        return _err("Timeout", "版面阶段超出预算 %.3fs" % _budget_sec())

    # 阅读顺序：先按行（y）再按列（x）——同一行的块不许被 x 打乱行序。
    rows.sort(key=lambda r: (r["box"][1], r["box"][0]))
    blocks = []
    table_boxes = []
    for i, r in enumerate(rows):
        blocks.append({"label": r["label"], "box": r["box"], "order": i})
        if r["label"].lower() in TABLE_LABELS:
            table_boxes.append(r["box"])

    tables = []
    if table_boxes:
        try:
            import paddleocr
            tab = paddleocr.TableStructureRecognition(model_dir=str(table_dir))
        except Exception as e:  # noqa: BLE001
            return _err("Unavailable", "表格模型加载失败: %s" % e)
        for box in table_boxes:
            crop = img.crop(_clamp_box(box, img.size))
            tmp = _save_crop(crop)
            try:
                res = tab.predict(input=tmp)
            except Exception as e:  # noqa: BLE001
                return _err("Unavailable", "表格推理失败（依赖/权重/版本）: %s" % e)
            finally:
                try:
                    os.unlink(tmp)
                except OSError:
                    pass
            if _over_budget(t0):
                return _err("Timeout", "表格阶段超出预算 %.3fs" % _budget_sec())
            t = _table_from_result(res, box)
            if t is None:
                return _err("Unavailable", "shape: 表格输出形状不认识（paddleocr 版本不匹配）")
            tables.append(t)

    out = {"blocks": blocks, "tables": tables}
    if _has_text_key(out):
        # 防御腿：出栈 JSON 里出现识别文本键＝边界被破，宁可 Unavailable 也不把文本带出去。
        return _err("Unavailable", "shape: 结构输出里出现 text 键（零文本边界被破）")
    print(json.dumps(out, ensure_ascii=False, separators=(",", ":")))
    return 0


def _clamp_box(box, size):
    """四值框裁进图片边界（版面框偶尔会略微越界，PIL crop 不接受负坐标）。"""
    w, h = size
    x0, y0, x1, y1 = box
    x0 = max(0, min(int(round(x0)), w))
    y0 = max(0, min(int(round(y0)), h))
    x1 = max(x0 + 1, min(int(round(x1)), w))
    y1 = max(y0 + 1, min(int(round(y1)), h))
    return (x0, y0, x1, y1)


def _save_crop(crop) -> str:
    """裁剪块落到临时 PNG（表格模型吃文件路径，与真机调用形态一致）。"""
    fd, p = tempfile.mkstemp(suffix=".png", prefix="structure_tbl_")
    os.close(fd)
    crop.save(p, format="PNG")
    return p


def main() -> int:
    if len(sys.argv) < 2:
        print("ERR:Structure:Usage:probe|page <img>", file=sys.stderr)
        return 2
    cmd = sys.argv[1]
    if cmd == "probe":
        return cmd_probe()
    if cmd == "page":
        if len(sys.argv) < 3:
            return _err("Usage", "缺少图片路径")
        return cmd_page(sys.argv[2])
    return _err("Usage", "未知子命令: %s" % cmd)


if __name__ == "__main__":
    sys.exit(main())
