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
# ★ 文本框膨胀系数：与权重自带的 inference.yml 里 PostProcess.unclip_ratio 同值
#   （PP-OCRv5_mobile_det 官方导出＝thresh 0.3 / box_thresh 0.6 / unclip_ratio 1.5）。
#   **换权重必须回来核这一档**：阈值与膨胀量都是模型自带的训练口径，
#   拿别的档去配会出现「框在、字却被切半」的静默降质（见 _det_boxes 的实测反例）。
DET_UNCLIP_RATIO = 1.5
# 识别单图高度（PP-OCRv3 约定 48），宽度按宽高比自适应。
REC_H = 48
# ★ 方向分类模型的两个输入档（**只作兜底，真值优先从模型自身的输入形状读**）：
#   · CLS_HW_MODERN＝PP-LCNet_x0_25/x1_0_textline_ori（PP-OCRv5/v6 官方 ONNX 导出）＝高 80、宽 160，
#     且该导出是**静态** H/W（onnxruntime 会直接拒收别的尺寸）；
#   · CLS_HW_LEGACY＝v2/v3/v4 时代的 ch_ppocr_mobile_v2.0_cls＝高 48、宽 192。
#     旧实现把 48x192 写死，于是换上 v5 权重后 cls 这一腿在模型加载期就抛形状错，
#     被 cmd_image 的兜底 except 收成 ERR:ImageCorrupt——表现是「OCR 识别过程出错」，
#     看不出根因是尺寸档位，正是本仓「有自动降级的链路，产物能打开不算验收」那一族。
CLS_HW_MODERN = (80, 160)
CLS_HW_LEGACY = (48, 192)


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
        lines = [line.rstrip("\n") for line in f]
    # ★ 索引＝模型输出类别号，**类 0 恒为 CTC blank**。
    #   PaddleOCR 的 CTCLabelDecode 是在代码里前插 "blank"，官方词典文件本身没有空首行
    #   （v3/v4 的 ppocr_keys_v1.txt 6623 行 ↔ 模型 6625 类；PP-OCRv5 官方 ONNX 的
    #   character_dict 18383 条 ↔ 模型 18385 类）。旧实现把 blank 记在**末位**，
    #   于是换上任何一个官方权重后每个字都错开一格——检测框照常出、文本全是移位乱码，
    #   属「产物能打开但内容是坏的」那一族，只能靠真件往返抓（见 assets/ocr/README.md 的投放口径）。
    #   若投放的词典文件已自带空首行（少数社区包这么打），这里不再重复前插。
    if lines and lines[0] == "":
        words = lines
    else:
        words = [""] + lines
    return words


def _pad_vocab(words, n_classes: int):
    """按模型类别数补齐字符表：多出来的那一类是 use_space_char 追加的半角空格。

    类别数比表长 ⇒ 逐位补空格；比表长短（表里多写了行）⇒ 原样返回，
    越界那一格由 _run_rec 的下标守卫兜住，绝不让一张图把整个 job 抛掉。
    """
    while len(words) < n_classes:
        words.append(" ")
    return words


# 检测输入的宽高对齐粒度（PP-OCR 的 DB 骨干是 32 倍下采样再接上采样）。
DET_STRIDE = 32


def _resize_for_det(img, max_side):
    """检测专用缩放：等比缩到长边 <= max_side，**再把宽高各自对齐到 DET_STRIDE 的整数倍**。

    ★ 为什么必须对齐：PP-OCRv5 官方 ONNX 导出里那个 Resize 节点的输出缓冲按
      「输入高宽 = 32 的整数倍」写死，喂 150x760 这种不对齐的图会在**内核执行期**抛
      `Shape mismatch attempting to re-use buffer {1,96,19,95} != {1,96,20,96}`，
      而 cmd_image 的兜底 except 把它收成 ERR:ImageCorrupt:「图片识别过程出错」——
      一张普通的手机截图就整单失败，且报错文案指不到「输入尺寸」这个真根因（PaddleOCR
      自己的 DetResizeForTest 就是先等比缩、再把边取整到 32 的倍数，这里同口径）。
    """
    from PIL import Image
    w, h = img.size
    scale = min(max_side / float(max(w, h)), 1.0)
    nw = max(DET_STRIDE, int(round(w * scale / DET_STRIDE)) * DET_STRIDE)
    nh = max(DET_STRIDE, int(round(h * scale / DET_STRIDE)) * DET_STRIDE)
    if (nw, nh) != (w, h):
        img = img.resize((nw, nh), Image.BILINEAR)
    return img


def _normalize_numpy(arr, mean, std, bgr=True):
    """(H,W,C) uint8 → (1,C,H,W) float32，减均值除标准差。

    ★ bgr 默认 True＝先把 RGB 翻成 BGR 再归一。PaddleOCR 的 det/cls 预处理是在 **cv2 的 BGR 数组**
      上按 mean=[0.485,0.456,0.406] 逐通道相减的（官方 yml 的 DecodeImage 明确写 img_mode: BGR），
      第一个减数落在蓝通道上。PIL 给的是 RGB，直接套同一串 mean 就是「把红通道当蓝通道」，
      检测置信度整体下滑且掉哪几个框不可预测——这类形状错在界面上只看得到「字少了」，
      日志一行都不会红，只能靠真件往返对文案。
    """
    import numpy as np
    arr = arr.astype(np.float32) / 255.0
    if bgr:
        arr = arr[:, :, ::-1]
    arr = (arr - np.array(mean, dtype=np.float32)) / np.array(std, dtype=np.float32)
    arr = arr.transpose(2, 0, 1)[None, ...]
    return arr


def _static_hw(session):
    """读方向分类模型声明的 (高,宽)；任一根不是常量则回落到旧档 48x192。

    PP-OCRv5/v6 官方导出的 textline_ori 是**静态** 3x80x160，onnxruntime 对别的尺寸直接拒收；
    v2/v3/v4 时代的 cls 导出是 48x192（宽动态或固定）。档位按模型自身形状读，
    换权重就不许再回来改代码（旧实现把 48x192 写死，v5 权重一放上去整单 OCR 报
    ERR:ImageCorrupt，根因藏在「图片识别过程出错」这句话里）。
    """
    try:
        dims = session.get_inputs()[0].shape
        h, w = dims[-2], dims[-1]
        if isinstance(h, int) and isinstance(w, int) and h > 0 and w > 0:
            return h, w
    except Exception:
        pass
    return CLS_HW_LEGACY


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


def _det_boxes(score_map, orig_size, min_area=16):
    """DB 概率图 → 文本框列表（axis-aligned 包围盒，已还原到**原图**坐标）。

    score_map: (H,W) float，概率；orig_size: (原图宽, 原图高)。
    返回 [[x0,y0,x1,y1], ...] 按阅读顺序（先上后下、同行左到右）。
    ★ 坐标还原按**概率图自身的尺寸**算，不拿缩放比去乘：检测输入既等比缩过、
      又对齐过 32 的倍数（见 _resize_for_det），两轴比例已不再严格相等，
      用单一 scale 反算会把框整体拉偏（图越大偏得越多，末行还会掉到图外）。
    ★ 每个框必须**按 unclip 口径向外膨胀**（DET_UNCLIP_RATIO，随权重 yml 走）：
      DB 的二值化区域是字形的**腐蚀核**（概率图在笔画边缘就掉到阈值以下），
      直接拿包围盒去裁剪会得到一条只穿过字腰的窄带——实测 40px 的行高被切成 20px，
      「翻译工作台 2026 测试」识别成「秘圣作台测订」：检测腿没报错、置信度 0.94、
      字数都对，只有文案是错的（本仓最难抓的那一类静默坏，只能靠真件对文案）。
      膨胀量沿用 PaddleOCR 的公式 d = area * ratio / perimeter，
      这里用包围盒近似 A=w*h、P=2(w+h)（无 cv2 的多边形 offset 可用）。
    """
    import numpy as np
    bin_map = score_map >= DET_THRESH
    if not bin_map.any():
        return []
    labels, n = _label2d(bin_map)
    sh, sw = bin_map.shape
    ow, oh = orig_size
    sx = ow / float(sw)
    sy = oh / float(sh)
    boxes = []
    for cid in range(1, n + 1):
        ys, xs = np.where(labels == cid)
        if ys.size == 0:
            continue
        if ys.size < min_area:
            continue
        x0, x1 = int(xs.min()), int(xs.max())
        y0, y1 = int(ys.min()), int(ys.max())
        bw = x1 - x0 + 1
        bh = y1 - y0 + 1
        # unclip 膨胀（概率图坐标系），再还原到原图坐标并夹回图内。
        d = (bw * bh) * DET_UNCLIP_RATIO / float(2 * (bw + bh))
        dx = int(round(d))
        ex0, ex1 = x0 - dx, x1 + dx
        ey0, ey1 = y0 - dx, y1 + dx
        bx0 = max(0, min(ow - 1, int(ex0 * sx)))
        bx1 = max(0, min(ow - 1, int(ex1 * sx)))
        by0 = max(0, min(oh - 1, int(ey0 * sy)))
        by1 = max(0, min(oh - 1, int(ey1 * sy)))
        boxes.append([bx0, by0, bx1, by1])
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
    # 按模型的类别数补齐字符表（use_space_char 追加的那一格），同一份表在多张裁剪图间复用。
    _pad_vocab(words, int(logits.shape[-1]))
    preds = np.argmax(logits, axis=-1)
    text, conf = "", 0.0
    prev = -1
    for p in preds:
        p = int(p)
        # ★ 跳过 blank（**类 0**）与连续重复；下标越界一律不取，
        #   防止投放的词典行数比模型类别数多时整张图抛错（宁可少一个字，不许丢一单）。
        if p != 0 and p != prev and p < len(words):
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
    # 方向分类的输入档位整轮读一次（同一份权重所有页共用）。
    cls_h, cls_w = _static_hw(cls_sess)
    try:
        for page_img in _iter_images(path):
            det_img = _resize_for_det(page_img, DET_MAX_SIDE)
            arr = np.array(det_img, dtype=np.float32)
            blob = _normalize_numpy(arr, [0.485, 0.456, 0.406], [0.229, 0.224, 0.225])
            det_out = det_sess.run(None, {det_sess.get_inputs()[0].name: blob})[0]
            score = det_out[0][0] if det_out[0].ndim == 3 else det_out[0]
            score = np.squeeze(score)
            for (x0, y0, x1, y1) in _det_boxes(score, page_img.size):
                crop = page_img.crop((x0, y0, x1 + 1, y1 + 1))
                if crop.width < 4 or crop.height < 4:
                    continue
                # 方向分类：输入档位按模型自身形状读（v5 是 80x160，旧档 48x192），
                # 归一与 det 同口径（BGR＋ImageNet mean/std）；argmax==1 表示需旋转 180。
                cc = crop.resize((cls_w, cls_h), Image.BILINEAR)
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
