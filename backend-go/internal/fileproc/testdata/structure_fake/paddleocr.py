"""假 paddleocr —— 只实现 `structure_v3.py` 依赖的两个类与返回形状（★ 2026-10-28 S2 自证件）。

为什么必须有它：S2 的真实推理要权重要 GPU，本机与 CI 都跑不了；而这条链的缺陷形态
（形状不认识却静默回空结构、框/标签映射错、合并单元格丢 span、HTML-only 档解不出网格）
全部是**运行期形状错**——源码 grep 得再像也判不出对错。所以用一份只认协议的桩把
`structure_v3.py` 的 `page` 真跑起来（零权重、零 GPU、毫秒级）。

协议口径（改这里等于改契约，Go 侧 `structure_page_wire_gate_test.go` 与
`structure_page_selftest.py` 同时盯着）：
  · 构造：`LayoutDetection(model_dir=<非空>)` / `TableStructureRecognition(model_dir=<非空>)`
    —— `model_dir=None` 直接抛（"没权重也敢跑"要在自证里现形，不许被默认值掩盖）；
  · 推理：`predict(input=<文件路径字符串>)` —— 传非字符串直接抛（真机就是吃路径）；
  · 返回：`[Result]`，`Result.json` 是 `{"res": {...}}`（paddleocr 3.x 的形态）。

可切换形态（只给自证/反证用，生产不设这些 env）：
  STRUCT_FAKE_TEXT_KEY=1     版面框里塞一个 `text` 键（零文本边界的反向腿）
  STRUCT_FAKE_BAD_SHAPE=1    版面返回不认识的对象（版本不匹配档）
  STRUCT_FAKE_SLEEP_MS=N     版面推理睡 N 毫秒（Timeout 档）
  STRUCT_FAKE_TABLE_MODE=logic|merged|html|bad
                             logic=2x2 全单格；merged=首行 col 跨 2；html=只有 HTML；bad=形状不认识
"""

import os
import time

__version__ = "3.2.0"


class Result:
    """paddleocr 3.x 的 Result：`.json` 是 `{"res": {...}}`。"""

    def __init__(self, res):
        self._res = res

    @property
    def json(self):
        return {"res": self._res}


def _require_model_dir(model_dir):
    if not model_dir:
        raise RuntimeError("model_dir 必须显式传入（结构腿不吃默认权重目录）")
    if not os.path.isdir(str(model_dir)):
        raise RuntimeError("model_dir 不存在: %s" % model_dir)


def _require_path_input(value):
    if not isinstance(value, str):
        raise RuntimeError("predict(input=...) 必须是文件路径字符串，实得 %r" % (type(value),))
    if not os.path.isfile(value):
        raise RuntimeError("input 文件不存在: %s" % value)


class LayoutDetection:
    def __init__(self, model_dir=None, **kwargs):
        _require_model_dir(model_dir)
        self.model_dir = str(model_dir)

    def predict(self, input=None, **kwargs):
        _require_path_input(input)
        time.sleep(float(os.environ.get("STRUCT_FAKE_SLEEP_MS", "0") or 0) / 1000.0)
        if os.environ.get("STRUCT_FAKE_BAD_SHAPE") == "1":
            return [object()]  # 既不是 dict 也没有 .json ⇒ 脚本必须报 Unavailable:shape
        # 刻意乱序 + 同行两块：阅读顺序必须由脚本按 (y, x) 重排（不是照返回顺序编号）。
        boxes = [
            {"label": "text", "score": 0.99, "coordinate": [10, 300, 400, 340]},
            {"label": "table", "score": 0.95, "coordinate": [10, 120, 400, 220]},
            {"label": "title", "score": 0.90, "coordinate": [10, 20, 300, 60]},
            {"label": "figure", "score": 0.88, "coordinate": [300, 20, 500, 60]},
        ]
        if os.environ.get("STRUCT_FAKE_TEXT_KEY") == "1":
            boxes[0]["text"] = "识别文本不许出现在结构输出里"
        return [Result({"boxes": boxes})]


class TableStructureRecognition:
    def __init__(self, model_dir=None, **kwargs):
        _require_model_dir(model_dir)
        self.model_dir = str(model_dir)

    def predict(self, input=None, **kwargs):
        _require_path_input(input)
        mode = (os.environ.get("STRUCT_FAKE_TABLE_MODE") or "logic").strip().lower()
        if mode == "bad":
            return [Result({"foo": 1})]  # 既无 cells 也无 html ⇒ 必须报 Unavailable:shape
        if mode == "html":
            html = ('<table><tr><td></td><td rowspan="2"></td></tr>'
                    '<tr><td></td></tr></table>')
            return [Result({"html": html})]
        if mode == "merged":
            bboxes = [[0, 0, 200, 20], [0, 20, 100, 40], [100, 20, 200, 40]]
            logic = [[0, 0, 0, 1], [1, 0, 1, 0], [1, 1, 1, 1]]
        else:
            bboxes = [[0, 0, 100, 20], [100, 0, 200, 20], [0, 20, 100, 40], [100, 20, 200, 40]]
            logic = [[0, 0, 0, 0], [0, 1, 0, 1], [1, 0, 1, 0], [1, 1, 1, 1]]
        return [Result({
            "cell_bboxes": bboxes,
            "logic_points": logic,
            "html": "<table></table>",
        })]
