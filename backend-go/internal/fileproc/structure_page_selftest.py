#!/usr/bin/env python3
"""structure_page_selftest.py — S2「结构页」形状契约的行为级自证（★ 2026-10-28 计价与成本批）。

为什么必须行为级：S2 的缺陷**全是运行期形状错**——框/标签映射错、阅读顺序按返回序编号、
合并单元格丢 span、HTML-only 档解不出网格、形状不认识却静默回空结构。这些在源码里
grep 得再像也判不出对错，只有「喂进去、看吐出来」能钉住。

零权重、零 GPU、零网络：`structure_v3.py` 的 `paddleocr` 依赖由 `testdata/structure_fake/paddleocr.py`
顶替（只实现 `LayoutDetection`/`TableStructureRecognition` 的调用协议与返回形状）；
PNG 用 Pillow 现造。Go 侧 `structure_page_wire_gate_test.go` 会：
  ① 用 `PYTHONPATH=<testdata/structure_fake>` 跑本文件，判绿只认末行 `STRUCTSELFTEST_ALL_OK=1`；
  ② 用同一份假件跑**真子进程**（`python3 structure_v3.py page <png>`），把 stdout 喂给 Go 的
     `parseStructureJSON` —— 那才是"脚本吐的字节真的被 Go 认"这条接线；
  ③ 跑反向腿：让假件在版面框里塞 `text` 键，Go 侧必须判 `ErrStructureBadInput`。

判绿口径：每条腿各打一行 `LEG <id> OK`，全过再打 `STRUCTSELFTEST_ALL_OK=1`；任一失败打
`LEG <id> FAIL <原因>` 并退 1。**注册表就是腿的全集**，删一条腿 Go 侧的集合等值锁当场红。

射程（逐条点名）：
  L01 版面映射：label/box 逐字落到 blocks，八值/多边形归一到包围盒，且不落 score 键
  L02 阅读顺序：按 (y, x) 重排后重编号——照模型返回顺序编号是错的（假件刻意乱序）
  L03 零文本边界：假件塞 `text` 键时出栈 JSON 里一个都没有（块本身不许被连坐丢掉）
  L04 表格网格：`logic_points` 的 [row_start,col_start,row_end,col_end] 解成 r/c/rowspan/colspan
  L05 HTML-only：只有 html 时按 `<td rowspan colspan>` 反解网格（含跨行占位）
  L06 三档失败：坏图 BadInput / 缺权重 Unavailable / 超预算 Timeout，各报各的 variant
  L07 版本形状：版面或表格返回不认识的对象 ⇒ Unavailable:shape（绝不静默回空结构）
  L08 probe 读数：`layout=`/`table=` 与 `models=` 现读现出；缺权重时 MISSING 而非 OK

环境变量（自证期间会临时覆盖并复原，进程结束不留痕）：
  STRUCT_FAKE_*           假件的形态开关（见 testdata/structure_fake/paddleocr.py）
  STRUCTURE_*_MODEL_DIR   本文件每条腿自己设/清，用来钉"缺哪个报哪个"
"""
import contextlib
import importlib.util
import io
import json
import os
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
FAKE_DIR = HERE / "testdata" / "structure_fake"

FAILURES = []
PASSED = []
LEGS = []  # [(腿号, 执行器)]；Go 侧核腿的**全集**，删一条即红。


def leg(lid):
    """装饰器工厂：登记成第 `lid` 号腿（对齐 capability_shape_selftest.py 的口径）。"""

    def decorator(fn):
        def runner():
            try:
                fn()
                PASSED.append(lid)
                print("LEG %s OK" % lid)
            except Exception as exc:  # noqa: BLE001 —— 腿内任何异常都是这条腿红，绝不吞
                FAILURES.append(lid)
                print("LEG %s FAIL %s" % (lid, exc))

        LEGS.append((lid, runner))
        return runner

    return decorator


def _load_structure_module():
    """按路径导入被测模块（`page` 是这条链的唯一实现）。"""
    path = HERE / "structure_v3.py"
    if not path.is_file():
        raise RuntimeError("structure_v3.py 不在 %s ⇒ 自证没有靶子" % HERE)
    spec = importlib.util.spec_from_file_location("structure_v3_under_test", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("无法导入 %s" % path)
    mod = importlib.util.module_from_spec(spec)
    sys.modules["structure_v3_under_test"] = mod
    spec.loader.exec_module(mod)
    return mod


def _ensure_paddleocr_on_path():
    """假 paddleocr：真装了就用真的（部署机），否则把 testdata 假件放进 sys.path。"""
    if importlib.util.find_spec("paddleocr") is not None:
        return
    if not FAKE_DIR.is_dir():
        raise RuntimeError("既没有 paddleocr，也找不到 %s ⇒ 自证跑不起来" % FAKE_DIR)
    sys.path.insert(0, str(FAKE_DIR))


SV = _load_structure_module()
_ensure_paddleocr_on_path()

# 每条腿都会改这几个 env；进入前统一清掉，避免上一条腿的形态泄漏到下一条。
_ENV_KEYS = (
    "STRUCT_FAKE_TEXT_KEY", "STRUCT_FAKE_BAD_SHAPE", "STRUCT_FAKE_SLEEP_MS",
    "STRUCT_FAKE_TABLE_MODE", "STRUCTURE_LAYOUT_MODEL_DIR", "STRUCTURE_TABLE_MODEL_DIR",
    "STRUCTURE_PAGE_BUDGET_SEC",
)


def _clear_env():
    for k in _ENV_KEYS:
        os.environ.pop(k, None)


@contextlib.contextmanager
def _env(**kv):
    """临时设置/清除 env（None＝清除），退出时逐键复原。"""
    saved = {k: os.environ.get(k) for k in _ENV_KEYS}
    try:
        for k in _ENV_KEYS:
            os.environ.pop(k, None)
        for k, v in kv.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = str(v)
        yield
    finally:
        for k, v in saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v


@contextlib.contextmanager
def _assets_dir(path):
    """临时把模块的 ASSETS_DIR 指向指定目录（权重投放形态的唯一变量）。"""
    saved = SV.ASSETS_DIR
    SV.ASSETS_DIR = Path(path)
    try:
        yield
    finally:
        SV.ASSETS_DIR = saved


def _weights_tree(root: Path) -> Path:
    """造分档权重树（layout/ 与 table/ 各一份 0 字节 .onnx）——只要求"目录非空"。"""
    for kind in ("layout", "table"):
        d = root / kind
        d.mkdir(parents=True, exist_ok=True)
        (d / "inference.onnx").write_bytes(b"")
    return root


def _png(root: Path, size=(500, 400)) -> Path:
    """现造一张纯白 PNG（结构腿只看几何，图片内容不参与判定）。"""
    from PIL import Image

    p = root / "page.png"
    Image.new("RGB", size, "white").save(p, format="PNG")
    return p


def _run_page(img, **env):
    """跑一次 `cmd_page`，回 (exit, stdout, stderr)。"""
    out, err = io.StringIO(), io.StringIO()
    with _env(**env), contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        code = SV.cmd_page(str(img))
    return code, out.getvalue(), err.getvalue()


def _page_json(img, **env):
    code, out, err = _run_page(img, **env)
    if code != 0:
        raise AssertionError("page 应成功，实际 exit=%d stderr=%s" % (code, err.strip()))
    try:
        return json.loads(out.strip())
    except json.JSONDecodeError as exc:
        raise AssertionError("page 输出不是一行合法 JSON：%r（%s）" % (out, exc))


def _all_keys(v):
    """递归列出 JSON 里的全部键名（判"零文本键"用；只问键不问值）。"""
    if isinstance(v, dict):
        for k, child in v.items():
            yield k
            yield from _all_keys(child)
    elif isinstance(v, (list, tuple)):
        for child in v:
            yield from _all_keys(child)


def _labels(doc):
    return [b.get("label") for b in doc.get("blocks") or []]


# ------------------------------------------------------------------ 腿

@leg("L01")
def leg_l01_layout_blocks_map():
    """label/box 逐字落盘；八值多边形归一到包围盒；出栈不许有 score 键。"""
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        _weights_tree(root)
        img = _png(root)
        with _assets_dir(root):
            doc = _page_json(img)
        blocks = doc.get("blocks") or []
        assert len(blocks) == 4, "版面块数应为 4，实得 %d（%s）" % (len(blocks), _labels(doc))
        by_label = {b["label"]: b for b in blocks}
        assert set(by_label) == {"text", "table", "title", "figure"}, "标签集合不对：%s" % list(by_label)
        assert by_label["table"]["box"] == [10.0, 120.0, 400.0, 220.0], \
            "table 的框应为 [10,120,400,220]，实得 %r" % (by_label["table"]["box"],)
        for b in blocks:
            assert isinstance(b["box"], list) and len(b["box"]) == 4, "每个框必须归一成四值：%r" % (b,)
            assert "score" not in b, "出栈不许带 score（那是读数不是结构，Go 侧也不认）"
            assert isinstance(b["order"], int), "order 必须是整数"


@leg("L02")
def leg_l02_reading_order_is_y_then_x():
    """假件刻意乱序返回；order 必须按 (y, x) 重排后从 0 连续编号。"""
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        _weights_tree(root)
        img = _png(root)
        with _assets_dir(root):
            doc = _page_json(img)
        ordered = sorted(doc["blocks"], key=lambda b: b["order"])
        want = ["title", "figure", "table", "text"]
        got = [b["label"] for b in ordered]
        assert got == want, "阅读顺序应为 %s（先 y 后 x），实得 %s ⇒ 照返回顺序编号了" % (want, got)
        assert [b["order"] for b in ordered] == [0, 1, 2, 3], "order 必须连续编号：%s" % [b["order"] for b in ordered]


@leg("L03")
def leg_l03_no_text_keys():
    """假件往版面框里塞 text 键 ⇒ 出栈 JSON 里一个文本键都不许有，且块不许被连坐丢掉。"""
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        _weights_tree(root)
        img = _png(root)
        with _assets_dir(root):
            doc = _page_json(img, STRUCT_FAKE_TEXT_KEY="1")
        assert len(doc["blocks"]) == 4, "带 text 键时块本身不许被丢掉（丢块＝另一条腿要抓的形态）"
        # ⚠️ 只判**键名**：版面块里有 `"label":"text"` 这种值，按整段 JSON 做子串匹配会把它误判成
        #    "文本键"（本文件首跑真踩）——判据必须问键，不问值。
        bad = {"text", "rec_text", "rec_texts"}
        hits = [k for k in _all_keys(doc) if str(k).lower() in bad]
        assert not hits, "出栈结构里出现了文本键 %s（零文本边界被破）" % hits


@leg("L04")
def leg_l04_table_grid_from_logic_points():
    """logic_points 的 [row_start,col_start,row_end,col_end] 解成 r/c/rowspan/colspan。"""
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        _weights_tree(root)
        img = _png(root)
        with _assets_dir(root):
            doc = _page_json(img, STRUCT_FAKE_TABLE_MODE="merged")
        tables = doc.get("tables") or []
        assert len(tables) == 1, "应恰好一张表，实得 %d" % len(tables)
        t = tables[0]
        assert (t["rows"], t["cols"]) == (2, 2), "网格应为 2x2，实得 %sx%s" % (t["rows"], t["cols"])
        assert t["box"] == [10.0, 120.0, 400.0, 220.0], "表格框应沿用版面那块，实得 %r" % (t["box"],)
        cells = t["cells"]
        assert len(cells) == 3, "合并档应有 3 个单元格，实得 %d" % len(cells)
        first = cells[0]
        assert (first["r"], first["c"], first["rowspan"], first["colspan"]) == (0, 0, 1, 2), \
            "首行跨两列必须解成 colspan=2，实得 %r" % (first,)
        assert first["box"] == [0.0, 0.0, 200.0, 20.0], "单元格框应同序映射，实得 %r" % (first["box"],)
        assert cells[2]["c"] == 1 and cells[2]["rowspan"] == 1, "右下单元格映射不对：%r" % (cells[2],)


@leg("L05")
def leg_l05_table_grid_from_html_only():
    """只有 html 时按 `<td rowspan colspan>` 反解（含跨行占位，第二行不许把格子排到 rowspan 位上）。"""
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        _weights_tree(root)
        img = _png(root)
        with _assets_dir(root):
            doc = _page_json(img, STRUCT_FAKE_TABLE_MODE="html")
        t = (doc.get("tables") or [{}])[0]
        assert (t["rows"], t["cols"]) == (2, 2), "HTML 反解网格应为 2x2，实得 %sx%s" % (t["rows"], t["cols"])
        spans = sorted((c["r"], c["c"], c["rowspan"], c["colspan"]) for c in t["cells"])
        assert spans == [(0, 0, 1, 1), (0, 1, 2, 1), (1, 0, 1, 1)], \
            "跨行单元格与第二行占位解错：%s" % (spans,)


@leg("L06")
def leg_l06_three_failure_variants():
    """坏图 BadInput / 缺权重 Unavailable / 超预算 Timeout —— 三档各报各的，不许混。"""
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        _weights_tree(root)
        img = _png(root)
        # ① 坏图（文件不存在）
        code, out, err = _run_page(root / "nope.png")
        assert code != 0 and "ERR:Structure:BadInput:" in err, "坏图应报 BadInput，实得 exit=%d err=%r" % (code, err)
        # ② 缺权重（空资产目录 + 无 env 覆盖）
        with tempfile.TemporaryDirectory() as empty:
            with _assets_dir(empty):
                code, out, err = _run_page(img)
        assert code != 0 and "ERR:Structure:Unavailable:" in err, "缺权重应报 Unavailable，实得 exit=%d err=%r" % (code, err)
        assert "layout" in err, "缺权重必须点名缺的是哪一路（layout/table）：%r" % err
        # ③ 超预算（内部预算 0.1ms + 假件睡 20ms）
        with _assets_dir(root):
            code, out, err = _run_page(img, STRUCTURE_PAGE_BUDGET_SEC="0.0001", STRUCT_FAKE_SLEEP_MS="20")
        assert code != 0 and "ERR:Structure:Timeout:" in err, "超预算应报 Timeout，实得 exit=%d err=%r" % (code, err)


@leg("L07")
def leg_l07_version_shape_mismatch_is_unavailable():
    """版面/表格返回不认识的对象 ⇒ Unavailable:shape（空结构会让上层当成"这页没版面"）。"""
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        _weights_tree(root)
        img = _png(root)
        with _assets_dir(root):
            code, out, err = _run_page(img, STRUCT_FAKE_BAD_SHAPE="1")
        assert code != 0 and "ERR:Structure:Unavailable:" in err, \
            "版面形状不认识应报 Unavailable，实得 exit=%d err=%r" % (code, err)
        assert out.strip() == "", "失败时不许往 stdout 吐半截 JSON，实得 %r" % out
        with _assets_dir(root):
            code, out, err = _run_page(img, STRUCT_FAKE_TABLE_MODE="bad")
        assert code != 0 and "ERR:Structure:Unavailable:shape" in err, \
            "表格形状不认识应报 Unavailable:shape，实得 exit=%d err=%r" % (code, err)


@leg("L08")
def leg_l08_probe_readings():
    """probe 出 `layout=`/`table=` 与 `models=`；缺权重时 MISSING 而不是 OK。"""
    out = io.StringIO()
    with tempfile.TemporaryDirectory() as td:
        root = _weights_tree(Path(td))
        with _assets_dir(root), contextlib.redirect_stdout(out):
            code = SV.cmd_probe()
        text = out.getvalue()
        assert code == 0, "有权重时 probe 应回 0，实得 %d（%r）" % (code, text)
        assert text.startswith("OK: "), "有权重时首行应为 OK：%r" % text
        for key in ("version=", "models=", "layout=", "table="):
            assert key in text, "probe 读数缺 %s：%r" % (key, text)
        assert "models=2" in text, "分档权重应数到 2 份，实得 %r" % text
    out2 = io.StringIO()
    with tempfile.TemporaryDirectory() as empty:
        with _assets_dir(empty), contextlib.redirect_stdout(out2):
            code = SV.cmd_probe()
        assert code == 3 and out2.getvalue().startswith("MISSING: "), \
            "空资产目录必须回 MISSING（不是 OK），实得 exit=%d %r" % (code, out2.getvalue())


# ------------------------------------------------------------------ 入口

def main() -> int:
    _clear_env()
    for lid, runner in LEGS:
        runner()
    print("LEGS total=%d ok=%d fail=%d" % (len(LEGS), len(PASSED), len(FAILURES)))
    if FAILURES:
        print("STRUCTSELFTEST_LEGS_FAILED=%s" % ",".join(FAILURES))
        return 1
    print("STRUCTSELFTEST_ALL_OK=1")
    return 0


if __name__ == "__main__":
    sys.exit(main())
