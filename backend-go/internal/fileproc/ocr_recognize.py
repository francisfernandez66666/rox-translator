#!/usr/bin/env python3
"""ocr_recognize.py — PP-OCR ONNX 图片文字识别子命令壳（P0 基础件；P1a 落地识别本体）。

子命令：
  probe            自检依赖与资产；stdout 打状态行 "OK: ..." / "MISSING: ..."（退出码 0 / 3）
  image <path>    识别图片/扫描件 PDF 文字，stdout 打 JSON {"text":..., "boxes":[...], "confidence":...}

依赖（**导入名**，与 fileproc/ocr.go 的 ocrPythonModules 同源维护，改一侧必红在 Go 侧协议断言）：
  onnxruntime / numpy / PIL。
  ★ 刻意不装 paddleocr/paddlepaddle/cv2：PP-OCR 的 ONNX 权重由 onnxruntime 直接驱动，
    预处理（resize/normalize/permute/检测后处理）numpy+Pillow 自足；paddlepaddle/cv2 属 GPU/重依赖，
    纯 CPU 推理一分钱用不上（2026-10-14 选型口径；检测连通域用纯 numpy 二值洪水填充实现，见 _label2d）。

安全边界：纯本地推理、零网络调用；模型权重只从脚本同目录 assets/ocr/ 读，
禁止运行时联网下载（私有化机器可能无外网，且权重随 sync_fileproc_assets.sh 落盘并核对 sha256）。
"""
import importlib.util
import json
import os
import sys
from pathlib import Path

# 与 fileproc/ocr.go 的 ocrPythonModules 同源维护（测试会核对两侧协议，不允许静默漂移）。
MODULES = ["onnxruntime", "numpy", "PIL"]

ASSETS_DIR = Path(__file__).resolve().parent / "assets" / "ocr"
# PP-OCR 三件套权重（det=文字检测 / cls=方向分类 / rec=文字识别）与识别词典，须随包落盘。
DET_MODEL = ASSETS_DIR / "det.onnx"
CLS_MODEL = ASSETS_DIR / "cls.onnx"
REC_MODEL = ASSETS_DIR / "rec.onnx"
REC_VOCAB = ASSETS_DIR / "ppocr_keys_v1.txt"

# 检测预处理上限（长边像素），防超大图 OOM。
DET_MAX_SIDE = 960
# 检测二值化阈值（DB 概率图）。
DET_THRESH = 0.3
# 识别单图高度（PP-OCRv3 约定 48），宽度按宽高比自适应。
REC_H = 48


def _err(variant: str, msg: str) -> int:
    """按统一协议向 stderr 打 ERR:<Variant>:<msg> 并退非零（Go 侧 friendlyOcrError 解析）。"""
    print("ERR:%s:%s" % (variant, msg), file=sys.stderr)
    return 2


def cmd_probe() -> int:
    missing = [m for m in MODULES if importlib.util.find_spec(m) is None]
    weights = sorted(ASSETS_DIR.glob("*.onnx"))
    detail = []
    if missing:
        detail.append("modules=" + ",".join(missing))
    if not weights:
        detail.append("assets/ocr/*.onnx 缺失（取权重步骤见 assets/ocr/README.md）")
    if detail:
        print("MISSING: " + "; ".join(detail))
        return 3
    print("OK: ocr 依赖齐备（%d 个 ONNX 权重）" % len(weights))
    return 0


def _load_vocab():
    if not REC_VOCAB.exists():
        raise FileNotFoundError("词典缺失: %s" % REC_VOCAB)
    with open(REC_VOCAB, "r", encoding="utf-8") as f:
        # PP-OCR 词典每行一个字；末位为 CTC blank（空行）。
        words = [line.rstrip("\n") for line in f]
    # 末位强制为 blank（空串），与训练时 num_classes = len(vocab)+1 对齐。
    if words and words[-1] != "":
        words.append("")
    return words


def _resize_keep_ratio(img, max_side):
    """PIL 图等比缩放到长边 <= max_side，返回 (PIL, scale)。"""
    from PIL import Image
    w, h = img.size
    scale = min(max_side / max(w, h), 1.0)
    if scale < 1.0:
        img = img.resize((int(w * scale), int(h * scale)), Image.BILINEAR)
    return img, scale


def _normalize_numpy(arr, mean, std):
    """(H,W,C) uint8 → (1,C,H,W) float32，减均值除标准差。"""
    import numpy as np
    arr = arr.astype(np.float32) / 255.0
    arr = (arr - np.array(mean, dtype=np.float32)) / np.array(std, dtype=np.float32)
    arr = arr.transpose(2, 0, 1)[None, ...]
    return arr


def _label2d(mask):
    """纯 numpy 四连通域标号（替代 cv2.connectedComponents），返回 (labels, n)。

    mask: bool (H,W)。逐像素 BFS 标号，适配检测后处理的小图（长边<=960）。
    """
    import numpy as np
    h, w = mask.shape
    labels = np.zeros((h, w), dtype=np.int32)
    cur = 0
    stack = []
    for y0 in range(h):
        for x0 in range(w):
            if not mask[y0, x0] or labels[y0, x0] != 0:
                continue
            cur += 1
            labels[y0, x0] = cur
            stack.append((y0, x0))
            while stack:
                y, x = stack.pop()
                for dy, dx in ((-1, 0), (1, 0), (0, -1), (0, 1)):
                    ny, nx = y + dy, x + dx
                    if 0 <= ny < h and 0 <= nx < w and mask[ny, nx] and labels[ny, nx] == 0:
                        labels[ny, nx] = cur
                        stack.append((ny, nx))
    return labels, cur


def _det_boxes(score_map, scale, min_area=16):
    """DB 概率图 → 文本框列表（axis-aligned 包围盒，已还原到原图坐标）。

    score_map: (H,W) float，概率。返回 [[x0,y0,x1,y1], ...] 按阅读顺序（先上后下、同行左到右）。
    """
    import numpy as np
    bin_map = score_map >= DET_THRESH
    if not bin_map.any():
        return []
    labels, n = _label2d(bin_map)
    boxes = []
    for cid in range(1, n + 1):
        ys, xs = np.where(labels == cid)
        if ys.size == 0:
            continue
        if ys.size < min_area:
            continue
        x0, x1 = int(xs.min()), int(xs.max())
        y0, y1 = int(ys.min()), int(ys.max())
        # 还原到原图坐标（检测图是等比缩放到 DET_MAX_SIDE 的）。
        boxes.append([int(x0 / scale), int(y0 / scale), int(x1 / scale), int(y1 / scale)])
    # 阅读顺序：按行聚类（同 y 带容差）后左右排序。
    boxes.sort(key=lambda b: (b[1] // 20, b[0]))
    return boxes


def _run_rec(session, img_crop, words):
    """对单张裁剪图做 CRNN 识别，返回 (text, conf)。"""
    import numpy as np
    from PIL import Image
    crop = img_crop.convert("RGB")
    w, h = crop.size
    if h == 0 or w == 0:
        return "", 0.0
    new_w = max(1, int(REC_H * w / h))
    crop = crop.resize((new_w, REC_H), Image.BILINEAR)
    arr = np.array(crop, dtype=np.float32)
    arr = (arr / 255.0 - 0.5) / 0.5  # rec 用 [-1,1] 归一
    arr = arr.transpose(2, 0, 1)[None, ...]
    out = session.run(None, {session.get_inputs()[0].name: arr})[0]
    # out: (1, T, C) 或 (T, C)；CTC greedy decode。
    logits = out[0] if out.ndim == 3 else out
    if logits.ndim == 3:
        logits = logits[0]
    preds = np.argmax(logits, axis=-1)
    text, conf = "", 0.0
    prev = -1
    for p in preds:
        p = int(p)
        if p != len(words) - 1 and p != prev:  # 跳过 blank（末位）与重复
            text += words[p]
        prev = p
    # 平均最大概率当作置信度（粗略）
    conf = float(np.mean(np.max(logits, axis=-1)))
    return text, conf


def _iter_images(path):
    """把输入（图片或 PDF）展开为一组 PIL 图（PDF 逐页渲染，需 fitz/pdf2image 之一）。"""
    from PIL import Image
    ext = os.path.splitext(path)[1].lower()
    if ext == ".pdf":
        # 扫描件 PDF：优先 PyMuPDF（fitz）逐页渲染；其次 pdf2image（依赖 poppler）。
        if importlib.util.find_spec("fitz") is not None:
            import fitz
            doc = fitz.open(path)
            for page in doc:
                pix = page.get_pixmap()
                yield Image.frombytes("RGB", (pix.width, pix.height), pix.samples)
            return
        if importlib.util.find_spec("pdf2image") is not None:
            from pdf2image import convert_from_path
            for im in convert_from_path(path, dpi=200):
                yield im
            return
        raise RuntimeError("PDF 渲染依赖缺失（需 PyMuPDF 或 pdf2image；图片型 PDF 无法 OCR）")
    yield Image.open(path).convert("RGB")


def cmd_image(path: str) -> int:
    # P1a 实现：PP-OCR ONNX 检测+方向分类+识别，stdout 打 JSON。
    if not os.path.exists(path):
        return _err("ImageOpen", "图片不存在: %s" % path)
    for m in MODULES:
        if importlib.util.find_spec(m) is None:
            return _err("Import", "缺少依赖: %s" % m)
    import numpy as np
    import onnxruntime as ort
    from PIL import Image

    # 资产齐备性检查（权重 + 词典），缺一则明确报错（与 probe 的不齐备态一致）。
    need = {"det.onnx": DET_MODEL, "cls.onnx": CLS_MODEL, "rec.onnx": REC_MODEL,
            "ppocr_keys_v1.txt": REC_VOCAB}
    missing = [name for name, p in need.items() if not p.exists()]
    if missing:
        return _err("Asset", "OCR 模型资产缺失: " + ", ".join(missing) +
                    "（取权重步骤见 assets/ocr/README.md）")

    try:
        words = _load_vocab()
        det_sess = ort.InferenceSession(str(DET_MODEL))
        cls_sess = ort.InferenceSession(str(CLS_MODEL))
        rec_sess = ort.InferenceSession(str(REC_MODEL))
    except Exception as e:  # 模型加载/词典读取失败
        return _err("ModelLoad", "模型加载失败: %s" % e)

    lines = []
    boxes_out = []
    conf_sum = 0.0
    conf_n = 0
    try:
        for page_img in _iter_images(path):
            det_img, scale = _resize_keep_ratio(page_img, DET_MAX_SIDE)
            arr = np.array(det_img, dtype=np.float32)
            blob = _normalize_numpy(arr, [0.485, 0.456, 0.406], [0.229, 0.224, 0.225])
            det_out = det_sess.run(None, {det_sess.get_inputs()[0].name: blob})[0]
            score = det_out[0][0] if det_out[0].ndim == 3 else det_out[0]
            score = np.squeeze(score)
            for (x0, y0, x1, y1) in _det_boxes(score, scale):
                crop = page_img.crop((x0, y0, x1 + 1, y1 + 1))
                if crop.width < 4 or crop.height < 4:
                    continue
                # 方向分类：48x192，归一同 det；argmax>0 表示需旋转 180。
                cc = crop.resize((192, 48), Image.BILINEAR)
                cb = _normalize_numpy(np.array(cc, dtype=np.float32),
                                      [0.485, 0.456, 0.406], [0.229, 0.224, 0.225])
                cl = cls_sess.run(None, {cls_sess.get_inputs()[0].name: cb})[0]
                if int(np.argmax(cl[0])) == 1:
                    crop = crop.rotate(180, expand=True)
                text, conf = _run_rec(rec_sess, crop, words)
                if text.strip():
                    lines.append(text)
                    boxes_out.append([x0, y0, x1, y1])
                    conf_sum += conf
                    conf_n += 1
    except Exception as e:
        return _err("ImageCorrupt", "图片识别过程出错: %s" % e)

    if not lines:
        return _err("Empty", "OCR 未识别出文字（图片可能无文字内容、或为手写/艺术字）")
    result = {
        "text": "\n".join(lines),
        "boxes": boxes_out,
        "confidence": round(conf_sum / conf_n, 4) if conf_n else 0.0,
    }
    print(json.dumps(result, ensure_ascii=False))
    return 0


def main() -> int:
    if len(sys.argv) < 2:
        print("ERR:Usage:probe|image <path>", file=sys.stderr)
        return 2
    cmd = sys.argv[1]
    if cmd == "probe":
        return cmd_probe()
    if cmd == "image":
        if len(sys.argv) < 3:
            print("ERR:Usage:image <path>", file=sys.stderr)
            return 2
        return cmd_image(sys.argv[2])
    print("ERR:Usage:probe|image <path>", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main())
