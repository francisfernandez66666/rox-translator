#!/usr/bin/env python3
"""capability_shape_selftest.py — ★ OCR/ASR 两条能力腿「形状契约」的行为级自证（2026-10-15）。

为什么是**行为级**而不是 grep 级（本仓纪律，AGENTS §三 那条「判据级断言代替不了守卫级」）：
  本批改掉的五个缺陷全都是**运行时形状错**，源码里 grep 得到字样也判不出对错——
    · CTC blank 记在**末位**（正确＝类 0）⇒ 检测框照常出、字数对、置信度 0.94，只有每个字都错开一格；
    · 检测输入没对齐 32 的倍数 ⇒ onnxruntime 在内核执行期抛 Shape mismatch，被兜底 except 收成
      「图片识别过程出错」，根因指不到尺寸；
    · 缺 unclip 膨胀 ⇒ 40px 行高被裁成 20px，识别成「秘圣作台测订」；
    · cls 档位写死 48x192 ⇒ 换上 v5 的 80x160 权重整单 OCR 失败；
    · 归一化没做 BGR 翻转 ⇒ 把红通道当蓝通道，掉哪几个框不可预测；
    · ASR 侧 `sherpa_onnx.read_wave` 在现网装的 **1.13.8 里根本不存在**（只有 write_wave），
      而 probe 回 OK、`asr_ready=true`、健康面全绿，只有真转一单才暴露。
  ⇒ 这六条只能靠「喂进去、看吐出来」钉死，所以本文件用**假 session＋合成 score map＋
    手搓 PCM wav** 把纯函数当被测对象，零模型权重、零网络、零 GPU。

由 Go 侧 `capability_shape_gate_test.go` 调用（`go test ./internal/fileproc/` 即进发布闸门）。
本文件也可单跑：`python3 capability_shape_selftest.py`。

判绿口径：**每条腿各打一行** `LEG <id> OK`，全过再打 `CAPSHAPE_ALL_OK=1`；
任一失败打 `LEG <id> FAIL <原因>` 并退 1。Go 侧同时核「腿的**全集**都在位」——
有人删掉一条腿（或让它恒过）时，那一行 LEG 消失即红，这是本锁的射程自证。

射程（24 条腿，逐条点名）：
  L01 词典装载：blank 恒占类 0（文件自带空首行时不重复前插）
  L02 词典补齐：类别数比表长时逐位补空格
  L03 CTC 解码：跳类 0＋连续重复去重＋就地补齐到类别数（表长于类别数不许截断）
  L04 检测缩放：长边上限＋宽高各自对齐 32 的倍数
  L05 检测后处理：按 unclip 口径向外膨胀
  L06 检测后处理：坐标按**概率图自身尺寸**逐轴反算（不拿单一缩放比乘回去）
  L07 检测后处理：阅读顺序＋面积下限＋空概率图返回空表
  L08 归一化：RGB→BGR 翻转后再减均值除标准差，输出 (1,C,H,W)
  L09 方向分类档位：从模型输入形状读，非静态则回落旧档
  L10 ASR 读 wav：16-bit 单声道 PCM
  L11 ASR 读 wav：8-bit（无符号、零点 128）＋多声道取第一声道
  L12 ASR 读 wav：不认识的形态如实报错（24-bit⇒Unsupported／非 wav⇒AudioCorrupt）
  L13 ASR 读 wav：将来若 sherpa_onnx 又导出 read_wave 仍优先用它，并把 int16 量级归一
  L14 图片回填：四值矩形框 [x0,y0,x1,y1] 必须被采纳并绘制（2026-10-17 真缺陷：旧判据按八值跳过全部行）
  L15 图片回填：八值四点框（历史/多边形检测器形态）同样被采纳
  L16 图片回填：坏行（坐标不成对/缺失）跳过且如实报 NothingPainted，不产出"与原文无异"的图
  L17 图片回填：逐字挑字体——拉丁字落到有拉丁字形的字体上（本批"英文画成豆腐块"的根治腿）
  L18 图片回填：绘制真的走逐字挑选（挑字函数写对但没被调用＝白修）
  L19 图片回填：字号地板按原行高派生、长译文换行而不缩成蚂蚁字（★ 2026-10-19 G3-a）
  L20 图片回填：全高墨色列（框线/竖线）剔除，原行高不被读成满框高（同批 G3-a）
  L21 图片回填：文字色按底色亮度反差自动选黑/白（★ 同批 G3-b）
  L22 图片回填：抹字底色取框外环带中位色，彩色底不被抹成灰（同批 G3-b）
  L23 图片回填：长译文优先向右扩进均匀空白区，遇墨/别的框即停（同批 G3-a）
  L24 ASR：无语音门（纯静音判 no_speech，小声真人语音不误伤）（★ 2026-10-19 G11）
  L26 图片回填：满宽墨色行（输入框上下边）剔除，短内容不被画成整格巨字（★ 同批）
  L27 图片回填：空框 + 原文交付跳过绘制，不造原文里没有的「幽灵内容」（同批）
  L28 图片回填：细长竖条（标题色条/表格竖线）剔除，字号不被色条抬高（★ 2026-10-19 缺陷②）
  L29 图片回填：**反证腿**——宽墨块/粗笔画不许被当色条剔掉（CJK 左弧 13 列覆盖率 ≥0.6）
  L30 图片回填：块高按**真实墨迹**算，凡报 overflow=0 就不许真实越框（越框那半行会被邻行抹掉）
  L31 图片回填：量墨迹/取底色/试扩框一律看**绘制前的原图**，不被上一行画下的字污染（缺陷②真因）
  L32 图片回填：越框墨迹一律丢弃（掩膜按框裁剪），不落进邻行地盘被邻行底色抹成半截字

环境变量（**只给 Go 侧反证用**，把被测对象换成一份变异副本，证明这些腿真的会红）：
  CAPSHAPE_OCR_MODULE=<path>  指定被测的 ocr_recognize.py 副本
  CAPSHAPE_ASR_MODULE=<path>  指定被测的 asr_transcribe.py 副本
  CAPSHAPE_IMG_MODULE=<path>  指定被测的 ocr_translate_image.py 副本
"""
import importlib.util
import json
import os
import struct
import sys
import tempfile
import wave
from pathlib import Path

HERE = Path(__file__).resolve().parent

FAILURES = []
PASSED = []
LEGS = []  # [(腿号, 执行器)]，按定义顺序登记；Go 侧核腿的**全集**，删一条即红。


def leg(lid):
    """装饰器工厂：把函数登记成第 `lid` 号腿（如 `@leg("L01")`）。
    执行器成功打 `LEG L01 OK`、失败打 `LEG L01 FAIL <原因>`，
    Go 侧按这些 LEG 行判「每条腿真跑过且真会红」；**注册表就是腿的全集**，删一条即红。
    """

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


def _load_module(alias, env_name, default_path):
    """按路径导入被测模块（反证时指向变异副本；正常态用仓库里那一份）。"""
    path = os.environ.get(env_name) or str(default_path)
    spec = importlib.util.spec_from_file_location(alias, path)
    if spec is None or spec.loader is None:
        raise RuntimeError("无法导入被测模块 %s" % path)
    mod = importlib.util.module_from_spec(spec)
    sys.modules[alias] = mod
    spec.loader.exec_module(mod)
    return mod


# ------------------------------------------------------------------ 假件（零模型、零权重）

class FakeSession:
    """最小 onnxruntime session 替身：只回答 get_inputs()[0].name/.shape 与 run()。"""

    def __init__(self, shape, outputs):
        self._in = FakeInput(shape)
        self._outs = outputs

    def get_inputs(self):
        return [self._in]

    def run(self, _wanted, feeds):
        return self._outs


class FakeInput:
    def __init__(self, shape):
        self.name = "x"
        self.shape = shape


def _mk_logits(rows):
    """(1,T,C) 的 one-hot 风格 logits：每行只有 argmax 位是正数，别的类都是负数。"""
    import numpy as np
    t, c = len(rows), max(rows) + 1
    arr = np.full((1, t, c), -1.0, dtype=np.float32)
    for i, cls in enumerate(rows):
        arr[0, i, cls] = 5.0
    return arr


# ------------------------------------------------------------------ OCR 腿

OCR = _load_module("ocr_under_test", "CAPSHAPE_OCR_MODULE", HERE / "ocr_recognize.py")


@leg("L01")
def leg_l01_vocab_blank_is_class_zero():
    """官方词典文件**没有**空首行 ⇒ 装载时必须前插一格占住类 0（blank）。"""
    with tempfile.TemporaryDirectory() as d:
        p = Path(d) / "dict.txt"
        p.write_text("a\nb\nc\n", encoding="utf-8")
        OCR.REC_VOCAB = p
        words = OCR._load_vocab()
        assert words[0] == "", "类 0 必须是 blank（空串），实读 %r" % (words[0],)
        assert words[1:4] == ["a", "b", "c"], "词典条目必须整体后移一格，实读 %r" % (words,)
        # 自带空首行的社区包：不许再插一次（否则每个字都错开一格，正是本批修的形态）。
        p.write_text("\na\nb\nc\n", encoding="utf-8")
        words2 = OCR._load_vocab()
        assert words2 == ["", "a", "b", "c"], "已含空首行还前插＝整体位移，实读 %r" % (words2,)


@leg("L02")
def leg_l02_pad_vocab_to_n_classes():
    """类别数比表长（use_space_char 追加的那一格）⇒ 补空格；比表短⇒原样，由越界守卫兜住。"""
    words = ["", "a", "b"]
    out = OCR._pad_vocab(list(words), 5)
    assert out == ["", "a", "b", " ", " "], "补齐结果不符：%r" % (out,)
    keep = OCR._pad_vocab(list(words), 2)
    assert keep == words, "表长够用时不许截断：%r" % (keep,)


@leg("L03")
def leg_l03_ctc_decode_skips_class_zero_and_repeats():
    """CTC 贪心解码：跳类 0、连续重复只取一次、表长于类别数时按表取值不截断。"""
    from PIL import Image

    # 表 5 格、模型 6 类 ⇒ _run_rec 必须就地补出第 6 格（use_space_char 追加的半角空格）。
    vocab = ["", "a", "b", "c", "d"]
    # argmax 序列：blank b b blank d 空格 blank —— 三件事（跳 0／去重／补齐）各占一段。
    sess = FakeSession([None, 3, 48, 6], [_mk_logits([0, 2, 2, 0, 4, 5, 0])])
    crop = Image.new("RGB", (40, 12), "white")
    text, conf = OCR._run_rec(sess, crop, vocab)
    assert text == "bd ", "解码结果不符（blank/去重/补齐三件事任一没生效）：%r" % (text,)
    assert conf > 0, "置信度必须出数（出栈 JSON 的 confidence 字段靠它）"
    assert len(vocab) == 6, "类别数比表长时不许就地补齐（实读长度 %d）" % len(vocab)
    # 表比类别数**长**（投放的词典多写了几行）：补齐腿不许截断，多出来的格子按表取值。
    # ★ 这里测不到「下标越界」那一档，因为 _pad_vocab 保证表长 ≥ 类别数——
    #   那条守卫是防御性的（防将来有人把补齐腿挪出 _run_rec），构造上打不到，别当成有射程。
    long_vocab = ["", "a", "b", "c", "d", "e", "f"]
    sess2 = FakeSession([None, 3, 48, 6], [_mk_logits([0, 5, 0])])
    t2, _ = OCR._run_rec(sess2, crop, long_vocab)
    assert t2 == "e", "表长于类别数时必须按表取值（不许截断），实读 %r" % (t2,)
    assert len(long_vocab) == 7, "补齐腿把多余的词典格截掉了：%r" % (long_vocab,)


@leg("L04")
def leg_l04_det_input_is_multiple_of_stride():
    """检测输入两轴必须都是 32 的整数倍，且长边不超过上限。"""
    from PIL import Image

    for (w, h) in [(150, 760), (1654, 2339), (965, 300), (31, 31), (17, 9000)]:
        img = Image.new("RGB", (w, h), "white")
        out = OCR._resize_for_det(img, OCR.DET_MAX_SIDE)
        ow, oh = out.size
        assert ow % OCR.DET_STRIDE == 0 and oh % OCR.DET_STRIDE == 0, \
            "%dx%d 缩放后 %dx%d 不是 %d 的倍数（官方导出的 Resize 节点会直接拒收）" % (
                w, h, ow, oh, OCR.DET_STRIDE)
        assert max(ow, oh) <= OCR.DET_MAX_SIDE + OCR.DET_STRIDE, \
            "%dx%d 缩放后长边 %d 超出上限档" % (w, h, max(ow, oh))
        assert ow > 0 and oh > 0


@leg("L05")
def leg_l05_boxes_are_dilated_by_unclip_ratio():
    """检测框必须按 unclip 口径外扩——不外扩就是把一行字裁成只穿过字腰的窄带。"""
    import numpy as np

    sh, sw = 128, 256
    score = np.zeros((sh, sw), dtype=np.float32)
    # 一条 20x200 的窄带（概率图坐标）。★ 切片右端是开区间，包围盒取到的是 229/69，
    #   _det_boxes 里 bw=x1-x0+1，所以期望值必须按**闭区间**算，否则这条腿自己就是假的。
    score[50:70, 30:230] = 1.0
    x0, x1, y0, y1 = 30, 229, 50, 69
    boxes = OCR._det_boxes(score, (sw, sh))
    assert len(boxes) == 1, "应当恰好一个框，实读 %r" % (boxes,)
    bx0, by0, bx1, by1 = boxes[0]
    # d = area*ratio/perimeter，包围盒近似 A=w*h、P=2(w+h)；orig=(sw,sh) 时两轴比例皆 1。
    bw, bh = x1 - x0 + 1, y1 - y0 + 1
    d = (bw * bh) * OCR.DET_UNCLIP_RATIO / float(2 * (bw + bh))
    dx = int(round(d))
    assert dx > 0, "膨胀量算出 0 说明 unclip 那条腿没进公式"
    assert (bx0, by0, bx1, by1) == (x0 - dx, y0 - dx, x1 + dx, y1 + dx), \
        "框未按 unclip 外扩（期望四边各外扩 %d px），实读 %r" % (dx, boxes[0])


@leg("L06")
def leg_l06_maps_back_per_axis_not_by_single_scale():
    """还原到原图必须**逐轴**除：缩放后对齐过 32，两轴比例已不相等。"""
    import numpy as np

    sh, sw = 64, 96          # 概率图
    ow, oh = 200, 100        # 原图（宽高比与概率图明显不同）
    score = np.zeros((sh, sw), dtype=np.float32)
    score[10:20, 20:40] = 1.0
    # 关掉膨胀，只看映射腿（膨胀由 L05 单独判，两件事分开的代价是这里要手动兜住比例）。
    ratio_backup = OCR.DET_UNCLIP_RATIO
    try:
        OCR.DET_UNCLIP_RATIO = 0.0
        bx0, by0, bx1, by1 = OCR._det_boxes(score, (ow, oh))[0]
    finally:
        OCR.DET_UNCLIP_RATIO = ratio_backup
    sx, sy = ow / float(sw), oh / float(sh)
    assert bx0 == int(20 * sx) and bx1 == int(39 * sx), "横轴映射不符：%r（期望按 %f 反算）" % ((bx0, bx1), sx)
    assert by0 == int(10 * sy) and by1 == int(19 * sy), "纵轴映射不符：%r（期望按 %f 反算）" % ((by0, by1), sy)
    assert bx1 < ow and by1 < oh, "框掉到图外了（单一 scale 反算的典型症状）"


@leg("L07")
def leg_l07_reading_order_area_floor_and_empty_map():
    """阅读顺序（先上后下、同行左到右）＋面积下限滤噪点＋空概率图返回空表。"""
    import numpy as np

    sh, sw = 200, 200
    score = np.zeros((sh, sw), dtype=np.float32)
    score[120:140, 100:160] = 1.0   # 下面一行的右半
    score[120:140, 10:60] = 1.0     # 下面一行的左半
    score[20:40, 80:140] = 1.0      # 上面一行
    score[180, 190] = 1.0           # 单个像素噪点（面积下限该滤掉）
    ratio_backup = OCR.DET_UNCLIP_RATIO
    try:
        OCR.DET_UNCLIP_RATIO = 0.0
        boxes = OCR._det_boxes(score, (sw, sh))
    finally:
        OCR.DET_UNCLIP_RATIO = ratio_backup
    assert len(boxes) == 3, "噪点应当被面积下限滤掉，实读 %d 框" % len(boxes)
    assert boxes[0][1] < 100, "第一框必须是最上面那一行，实读 %r" % (boxes[0],)
    assert boxes[1][0] < boxes[2][0], "同一行必须左到右，实读 %r" % (boxes[1:],)
    assert OCR._det_boxes(np.zeros((64, 64), dtype=np.float32), (64, 64)) == [], "空概率图必须返回空表"


@leg("L08")
def leg_l08_normalize_flips_rgb_to_bgr():
    """归一化必须先把 RGB 翻成 BGR（官方 yml 的 DecodeImage 写 img_mode: BGR）。"""
    import numpy as np

    arr = np.zeros((2, 2, 3), dtype=np.float32)
    arr[:, :, 0] = 255.0  # PIL 给的通道 0 是**红**
    out = OCR._normalize_numpy(arr, [0.485, 0.456, 0.406], [0.229, 0.224, 0.225])
    assert out.shape == (1, 3, 2, 2), "输出必须是 (1,C,H,W)，实读 %r" % (out.shape,)
    ch0 = float(out[0, 0, 0, 0])
    # 翻 BGR 后通道 0 是蓝＝0/255 ⇒ (0-0.485)/0.229；没翻就会拿到红的 (1-0.485)/0.229。
    assert abs(ch0 - (0.0 - 0.485) / 0.229) < 1e-4, "通道 0 没按蓝通道减均值（BGR 翻转丢了），实读 %f" % ch0
    ch2 = float(out[0, 2, 0, 0])
    assert abs(ch2 - (1.0 - 0.406) / 0.225) < 1e-4, "通道 2 没按红通道归一，实读 %f" % ch2
    # 显式关掉翻转的那一档（调用方另有口径时）：必须落回 RGB 序
    out_rgb = OCR._normalize_numpy(arr, [0.485, 0.456, 0.406], [0.229, 0.224, 0.225], bgr=False)
    assert abs(float(out_rgb[0, 0, 0, 0]) - (1.0 - 0.485) / 0.229) < 1e-4


@leg("L09")
def leg_l09_cls_hw_comes_from_model_shape():
    """方向分类档位从模型自身形状读；动态维回落旧档 48x192。"""
    modern = FakeSession([None, 3, 80, 160], [np_arr_zero()])
    assert OCR._static_hw(modern) == (80, 160), "v5 静态档读不出来"
    dynamic = FakeSession([None, 3, 80, "width"], [np_arr_zero()])
    assert OCR._static_hw(dynamic) == OCR.CLS_HW_LEGACY, "非静态维必须回落旧档"
    broken = object()  # 压根没有 get_inputs ⇒ 不许抛，只能回落
    assert OCR._static_hw(broken) == OCR.CLS_HW_LEGACY, "读不到形状时必须回落而不是抛"


def np_arr_zero():
    import numpy as np
    return np.zeros((1, 2), dtype=np.float32)


# ------------------------------------------------------------------ ASR 腿

ASR = _load_module("asr_under_test", "CAPSHAPE_ASR_MODULE", HERE / "asr_transcribe.py")


class FakeSherpaNoReader:
    """现网 sherpa_onnx 1.13.8 的形态：只有 write_wave，**没有 read_wave**。"""
    write_wave = staticmethod(lambda *a, **k: None)


class FakeSherpaWithReader:
    """将来某个版本又导出 read_wave 的形态：返回 int16 量级的样本（考验归一腿）。"""

    @staticmethod
    def read_wave(path):
        import numpy as np
        return np.array([0, 16384, -16384], dtype=np.float32), 16000


def _write_pcm(path, samples_i16, rate=16000, channels=1):
    import numpy as np
    with wave.open(str(path), "wb") as wf:
        wf.setnchannels(channels)
        wf.setsampwidth(2)
        wf.setframerate(rate)
        wf.writeframes(np.asarray(samples_i16, dtype="<i2").tobytes())


@leg("L10")
def leg_l10_read_wave_int16_mono():
    """16-bit 单声道 PCM：读回 (float32[-1,1], 采样率)。"""
    with tempfile.TemporaryDirectory() as d:
        p = Path(d) / "a.wav"
        _write_pcm(p, [0, 32767, -32768, 16384])
        samples, rate = ASR._read_wave(str(p), FakeSherpaNoReader)
        assert rate == 16000, "采样率读数不符：%d" % rate
        assert abs(samples[1] - (32767 / 32768.0)) < 1e-5, "16-bit 归一不符：%r" % (samples,)
        assert abs(samples[2] - (-32768 / 32768.0)) < 1e-5
        assert str(samples.dtype) == "float32", "样本必须是 float32，实读 %s" % samples.dtype


@leg("L11")
def leg_l11_read_wave_int8_and_multichannel():
    """8-bit（无符号、零点 128）与多声道（取第一声道）都要能出可用样本。"""
    import numpy as np
    with tempfile.TemporaryDirectory() as d:
        p8 = Path(d) / "b.wav"
        with wave.open(str(p8), "wb") as wf:
            wf.setnchannels(1)
            wf.setsampwidth(1)
            wf.setframerate(16000)
            wf.writeframes(bytes([128, 255, 0]))
        s8, _ = ASR._read_wave(str(p8), FakeSherpaNoReader)
        assert abs(float(s8[0])) < 1e-6, "8-bit 的零点必须按 128 算，实读 %r" % (s8,)
        assert abs(float(s8[1]) - (127.0 / 128.0)) < 1e-6
        # 双声道：左声道是 [0, 32767]、右声道是 [-32768, -32768] ⇒ 只许取左
        pst = Path(d) / "c.wav"
        interleave = np.array([0, -32768, 32767, -32768], dtype="<i2")
        _write_pcm(pst, interleave, channels=2)
        st, rate = ASR._read_wave(str(pst), FakeSherpaNoReader)
        assert rate == 16000 and st.size == 2, "多声道必须摊平到单声道，实读 size=%d" % st.size
        assert abs(float(st[1]) - (32767 / 32768.0)) < 1e-5, "取错声道（要第一路），实读 %r" % (st,)


@leg("L12")
def leg_l12_read_wave_rejects_unknown_shapes():
    """认不出的形态**如实报错**，不许硬解出半截样本（下游 accept_waveform 会静默吃坏数据）。"""
    with tempfile.TemporaryDirectory() as d:
        # 24-bit PCM：标准库能打开，但本函数只认 8/16/32 ⇒ Unsupported
        p24 = Path(d) / "d.wav"
        _riff_wav(p24, bits_per_sample=24, body=bytes([0, 0, 0] * 8))
        try:
            ASR._read_wave(str(p24), FakeSherpaNoReader)
            raise AssertionError("24-bit 必须报 Unsupported，竟然解过去了")
        except RuntimeError as exc:
            assert "ERR:Unsupported" in str(exc), "报错档位不对：%s" % exc
        # 不是 wav：wave 打开即抛 ⇒ AudioCorrupt
        bogus = Path(d) / "e.wav"
        bogus.write_bytes(b"this is definitely not a wav file")
        try:
            ASR._read_wave(str(bogus), FakeSherpaNoReader)
            raise AssertionError("非 wav 必须报 AudioCorrupt")
        except RuntimeError as exc:
            assert "ERR:AudioCorrupt" in str(exc), "报错档位不对：%s" % exc


def _riff_wav(path, bits_per_sample, body):
    """手搓 RIFF/WAVE 头（标准库 wave 写不出 24-bit，这一档只能自己拼）。"""
    channels, rate = 1, 16000
    block_align = channels * bits_per_sample // 8
    fmt = struct.pack("<HHIIHH", 1, channels, rate, rate * block_align, block_align, bits_per_sample)
    data = b"data" + struct.pack("<I", len(body)) + body
    blob = b"RIFF" + struct.pack("<I", 4 + 8 + len(fmt) + len(data)) + b"WAVE" + b"fmt " + struct.pack("<I", 16) + fmt + data
    Path(path).write_bytes(blob)


@leg("L13")
def leg_l13_prefers_sherpa_reader_when_present():
    """将来版本又导出 read_wave 时优先用它，并把 int16 量级归一到 [-1,1]。"""
    with tempfile.TemporaryDirectory() as d:
        p = Path(d) / "f.wav"
        _write_pcm(p, [0, 1, 2, 3])  # 内容无所谓：这一档走 read_wave 那条腿
        samples, rate = ASR._read_wave(str(p), FakeSherpaWithReader)
        assert rate == 16000
        assert abs(float(samples[1]) - 0.5) < 1e-6, "read_wave 给出 int16 量级时必须除以 32768，实读 %r" % (samples,)
        assert abs(float(samples[2]) - (-0.5)) < 1e-6
        # 已经是浮点量级的版本不许再除一遍
        class AlreadyFloat:
            @staticmethod
            def read_wave(_path):
                import numpy as np
                return np.array([0.25, -0.5], dtype=np.float32), 16000

        s2, _ = ASR._read_wave(str(p), AlreadyFloat)
        assert abs(float(s2[0]) - 0.25) < 1e-6, "浮点量级被二次缩放：%r" % (s2,)


# ------------------------------------------------------------------ 图片原位回填腿（⭐ 本批）

IMG = _load_module("img_under_test", "CAPSHAPE_IMG_MODULE", HERE / "ocr_translate_image.py")


def _blank_png(path, w=320, h=120):
    """造一张**纯白**原图：空白重编码后仍是 0 个墨点，于是"有墨"＝真有绘制发生。"""
    from PIL import Image
    Image.new("RGB", (w, h), "white").save(str(path))


def _ink_count(path):
    """数非白像素（判「有没有真画上去」最干净的一把尺子：重编码的原图恒为 0）。"""
    import numpy as np
    from PIL import Image
    arr = np.asarray(Image.open(str(path)).convert("RGB"))
    return int((arr < 250).any(axis=2).sum())


def _render(src, out, lines):
    """跑一次 render 子命令（行数据落临时 json，与 Go 侧同一调用形态）。"""
    lp = Path(str(out) + ".json")
    lp.write_text(json.dumps(lines, ensure_ascii=False), encoding="utf-8")
    return IMG.cmd_render(str(src), str(out), str(lp))


class _FakeFont:
    """假字体：只回答「这个码位有没有字形」（真字体的覆盖情况随机器而异，判据必须确定）。

    位图刻意用两个**不同**的数组表示「有/无」：`_has_glyph` 靠与 `.notdef` 哨兵逐字节比，
    这里 absent 恒为全零（＝哨兵），present 恒为全 255。
    """

    def __init__(self, glyphs, size=20):
        self.glyphs = set(glyphs)
        self.size = size

    def getmask(self, ch):
        import numpy as np
        if ch in self.glyphs:
            return np.full((4, 4), 255, dtype=np.uint8)
        return np.zeros((4, 4), dtype=np.uint8)

    def getlength(self, ch):
        return 10.0


@leg("L14")
def leg_l14_four_value_box_is_painted():
    """★ 2026-10-17 真缺陷：`ocr_recognize.py` 吐的是**四值矩形** `[x0,y0,x1,y1]`，
    而旧 guard 写的是 `len(box) < 8`（想当然按「四点八坐标」）⇒ 每一行都被 `continue` 跳过
    ⇒ 译文图＝重编码的原图（尺寸变了、内容还是原文，看着像「翻过了」）。
    判据用空白图：只要有墨，就证明这一行被采纳并绘制了（纯白重编码恒 0 墨点）。
    """
    with tempfile.TemporaryDirectory() as d:
        src, out = Path(d) / "s.png", Path(d) / "o.png"
        _blank_png(src)
        rc = _render(src, out, [{"text": "原文", "box": [10, 10, 300, 100], "tran": "译文测试"}])
        assert rc == 0, "render 退出码 %d（四值框被当坏行跳过了）" % rc
        assert _ink_count(out) > 0, "四值矩形框没被采纳 ⇒ 译文图与重编码的原图无异（本批缺陷形态）"


@leg("L15")
def leg_l15_eight_value_quad_box_still_painted():
    """八值四点框（历史/未来多边形检测器的形态）必须同样被采纳——两种形态都收，不许二选一。"""
    with tempfile.TemporaryDirectory() as d:
        src, out = Path(d) / "s.png", Path(d) / "o.png"
        _blank_png(src)
        quad = [40, 20, 300, 20, 300, 100, 40, 100]
        rc = _render(src, out, [{"text": "原文", "box": quad, "tran": "译文测试"}])
        assert rc == 0, "render 退出码 %d（八值四点框被当坏行跳过了）" % rc
        assert _ink_count(out) > 0, "八值四点框没被采纳"


@leg("L16")
def leg_l16_malformed_box_is_skipped_not_painted():
    """坏行（坐标不成对）必须**跳过**并如实报 NothingPainted——
    直接拿 [1,2,3] 去算矩形会画出一个歪框；画出来还不如不画（客户看到的是一张「像翻过了」的坏图）。"""
    with tempfile.TemporaryDirectory() as d:
        src, out = Path(d) / "s.png", Path(d) / "o.png"
        _blank_png(src)
        rc = _render(src, out, [{"text": "原文", "box": [1, 2, 3], "tran": "译文"}])
        assert rc != 0, "坏行必须让整张图判失败（否则上游会把它当成功产物交给客户）"
        assert not out.exists(), "NothingPainted 档不许落盘产物"


@leg("L17")
def leg_l17_per_char_font_fallback():
    """逐字挑字体：拉丁字必须落到**有拉丁字形**的那一档上。

    随包 `DroidSansFallbackFull.ttf` 只覆盖中日韩（实测 `'A'/'1'/'?'` 全落到 `.notdef`），
    只拿它画英文 ⇒ 译文图整片豆腐块——这是本批另一条真缺陷，根治腿就是这条逐字回落。
    """
    cjk, latin = _FakeFont("中文，"), _FakeFont("ABC1?")
    assert IMG._has_glyph(latin, "A") and not IMG._has_glyph(cjk, "A"), \
        "哨兵比对法失效（分不出「有字形」与「不存在时的兜底方块」）"
    chain = [cjk, latin]
    assert IMG._pick_font(chain, "A") is latin, "拉丁字没被挑到有拉丁字形的字体上（英文会画成豆腐块）"
    assert IMG._pick_font(chain, "中") is cjk, "汉字应落到链首的 CJK 字体"
    assert IMG._pick_font(chain, " ") is cjk, "空白字符走链首（只为占位宽度）"
    assert IMG._pick_font(chain, "ش") is cjk, "谁都没这个字形时回落链首，不许抛"


@leg("L18")
def leg_l18_draw_uses_per_char_picker():
    """挑字函数写对但**没被绘制调用**＝白修（L17 会一路绿）。

    隔离手法：把量宽那条路径（`_measure`）换成桩，于是绘制期间唯一会碰 `_pick_font`
    的就是 `_draw_text` 本身——它必须逐字调用，而不是「整行一个字体」。
    """
    seen = []
    orig_pick, orig_measure = IMG._pick_font, IMG._measure
    IMG._measure = lambda chain, text: 10.0 * len(text)

    def spy(chain, ch):
        seen.append(ch)
        return orig_pick(chain, ch)

    IMG._pick_font = spy
    try:
        with tempfile.TemporaryDirectory() as d:
            src, out = Path(d) / "s.png", Path(d) / "o.png"
            _blank_png(src)
            rc = _render(src, out, [{"text": "原文", "box": [10, 10, 300, 100], "tran": "A中"}])
            assert rc == 0, "render 退出码 %d" % rc
    finally:
        IMG._pick_font, IMG._measure = orig_pick, orig_measure
    assert "A" in seen and "中" in seen, "绘制没走逐字挑字体（只见到 %r）⇒ 挑字腿形同虚设" % (seen,)


# ---------------------------------------------------- 图片保真腿（⭐ 2026-10-19 G3-a/G3-b）

def _rgb_image(arr):
    """numpy (H,W,3) → PIL Image（供捏造带墨/带底色的样张）。"""
    from PIL import Image
    import numpy as np
    return Image.fromarray(np.asarray(arr, dtype=np.uint8), "RGB")


@leg("L19")
def leg_l19_font_floor_is_derived_and_wraps():
    """★ 2026-10-19 G3-a：字号地板必须按原行高派生，且长译文要**换行**而不是缩到地板以下。

    旧形态一律从 8px 兜底 ⇒ 长译文缩成蚂蚁字。判据两段：
      ① 地板 = max(8, int(原行墨迹高度 × FONT_FLOOR_RATIO))；原行 16px ⇒ 地板 9px
         （0.6×16=9.6→9）。这里刻意用**字面 9** 而不是读 IMG.FONT_FLOOR_RATIO 现算：
         读现算值会在「比例被改成 0」时一起变成 0，断言恒真＝空转锁。
      ② 一段长译文放进窄框，`_layout` 出来的字号不低于地板（换行吸收宽度、不缩字）。
    """
    assert IMG._font_floor(16, 20) == 9, "字号地板没按原行高 0.6 派生（实读 %d）" % IMG._font_floor(16, 20)
    assert IMG._font_floor(0, 20) >= IMG.MIN_FONT_PX, "量不到原行高时必须回落到 MIN_FONT_PX 地板"
    long_text = "这是一段特意写得很长的译文用来强制换行并检验字号不会缩到不可读"
    floor = IMG._font_floor(16, 20)
    px, lines, _ = IMG._layout(long_text, 60, 20, floor, 16)
    assert px >= floor, "长译文把字号缩到地板以下了（%d < %d）" % (px, floor)
    assert len(lines) > 1, "窄框里的长译文没有换行（%r）" % (lines,)


@leg("L20")
def leg_l20_full_height_ink_columns_dropped():
    """★ 2026-10-19 G3-a：全高墨色列（框线/竖线/表格边）必须被剔除，否则原行高被读成满框高。

    造一张 20×60 白底样张：第 0 列整列有墨（框线），第 6~12 行第 20~40 列有一块文字墨。
    正确读数＝文字的 7 行；不剔除列则整列墨让「每一行都有墨」⇒ 读数 20（地板随之被抬错）。
    ★ 2026-10-19 缺陷②批：这条框线刻意取**满列**（贯穿整框）——细而不满高的色条归 L28。
      两条判据在实现里刻意**不重叠**：重叠过一次，本腿的变异体当场退化成"摘掉判据也不红"的空转锁。
    """
    import numpy as np
    arr = np.full((20, 60, 3), 255, dtype=np.uint8)
    arr[:, 0, :] = 0            # 全高竖线（框线）
    arr[6:13, 20:41, :] = 0     # 文字墨块：行 6..12
    h = IMG._line_ink_height(_rgb_image(arr))
    assert h == 7, "全高墨色列没被剔除（读数 %d，期望文字本身的 7 行）" % h


@leg("L21")
def leg_l21_text_color_by_background_luminance():
    """★ 2026-10-19 G3-b：文字色按底色亮度反差自动选——亮底黑字、暗底白字。

    旧形态恒黑字：深色底/彩色卡片上字看不见（用户报障的另一形态）。
    """
    assert IMG._text_color((255, 255, 255)) == (0, 0, 0), "白底必须出黑字"
    assert IMG._text_color((245, 245, 245)) == (0, 0, 0), "近白底也必须出黑字"
    assert IMG._text_color((16, 16, 16)) == (255, 255, 255), "深色底必须出白字（旧形态黑字看不见）"
    assert IMG._text_color((40, 60, 90)) == (255, 255, 255), "暗蓝底必须出白字"
    assert IMG._text_color(None) == (0, 0, 0), "底色取不到时按黑字"


@leg("L22")
def leg_l22_bg_from_ring_median_keeps_color():
    """★ 2026-10-19 G3-b：抹字底色取**框外环带的中位色**，彩色底不被抹成灰/白。

    造一张纯色底图，框四周环带就是该底色 ⇒ `_pick_bg` 必须回该色（不是灰 128）。
    """
    import numpy as np
    arr = np.full((120, 200, 3), (30, 90, 200), dtype=np.uint8)   # 一块蓝色底
    bg = IMG._pick_bg(_rgb_image(arr), (80, 50, 120, 70))
    assert bg == (30, 90, 200), "彩色底的抹字底色被改成了别的东西（实读 %r）" % (bg,)
    # 白底文档：环带中位色＝白
    white = np.full((120, 200, 3), 255, dtype=np.uint8)
    assert IMG._pick_bg(_rgb_image(white), (80, 50, 120, 70)) == (255, 255, 255), "白底取样不为白"


@leg("L23")
def leg_l23_expand_rect_into_uniform_margin_only():
    """★ 2026-10-19 G3-a：长译文优先向右扩进**均匀空白区**，遇到墨/别的框就地停下。

    白底图右侧留 24px 空白 ⇒ 框应扩满上限；把右侧改成有墨（高方差）⇒ 一步都不扩。
    """
    import numpy as np
    arr = np.full((80, 200, 3), 255, dtype=np.uint8)
    rect = (20, 20, 100, 60)
    grown = IMG._expand_rect(_rgb_image(arr), rect, [])
    assert grown == (20, 20, 100 + IMG.EXPAND_MAX_PX, 60), "白底右侧空白必须扩满上限，实读 %r" % (grown,)
    arr2 = np.full((80, 200, 3), 255, dtype=np.uint8)
    arr2[20:60, 102:150, :] = 0            # 紧邻框右侧就是墨块 ⇒ 不许扩
    assert IMG._expand_rect(_rgb_image(arr2), rect, []) == rect, "右侧有墨时不许扩（会压到别人）"
    # 有别的框挡路：右侧空白但 blocker 贴着 ⇒ 不许扩进 blocker
    blocked = IMG._expand_rect(_rgb_image(arr), rect, [(101, 20, 160, 60)])
    assert blocked == rect, "与相邻框相交时不许扩，实读 %r" % (blocked,)


@leg("L26")
def leg_l26_full_width_ink_rows_dropped():
    """★ 2026-10-19：满宽墨色行（横线/输入框上下边）必须与满高墨色列**同尺**剔除。

    造一张 20×60 白底样张：第 0 行整行有墨（输入框上边），第 6~12 行第 20~40 列一块文字墨。
    正确读数＝文字本身的 7 行；只剔列的那一版因为横线让「每一列都有墨」之外还让**行连续有墨**，
    读数变成 13 ⇒ 字号按框高起算，短内容（`0`/`4`）被画成整格巨字（现网单字号忽大忽小的真身）。
    """
    import numpy as np
    arr = np.full((20, 60, 3), 255, dtype=np.uint8)
    arr[0, :, :] = 0            # 满宽横线（框的上边）
    arr[6:13, 20:41, :] = 0     # 文字墨块：行 6..12
    h = IMG._line_ink_height(_rgb_image(arr))
    assert h == 7, "满宽墨色行没被剔除（读数 %d，期望文字本身的 7 行）" % h


@leg("L27")
def leg_l27_blank_box_identity_not_painted():
    """★ 2026-10-19：**无墨框 + 要写的就是原文**（identity 交付）⇒ 跳过绘制，不造「幽灵内容」。

    现网单里 OCR 在控件边框/下拉箭头上误切出无墨框，被画成满格巨字的 `0`/`4` 与乱入的 `の`
    ——原图没字而我们造了字。两条腿一起判（只判"跳过"会让"整段漏绘"也一路绿）：
    同一无墨框换成**真译文**（≠ 原文）必须照旧绘制，否则「字号问题」会变成「漏译问题」。
    """
    with tempfile.TemporaryDirectory() as d:
        src, out = Path(d) / "s.png", Path(d) / "o.png"
        _blank_png(src)
        rc = _render(src, out, [{"text": "0", "box": [10, 10, 200, 80], "tran": "0"}])
        assert rc != 0 and not out.exists(), "无墨框 + 原文交付不许造出幽灵内容（rc=%d，产物=%s）" % (rc, out.exists())
        rc2 = _render(src, out, [{"text": "0", "box": [10, 10, 200, 80], "tran": "Zero"}])
        assert rc2 == 0 and _ink_count(out) > 0, "真译文（≠ 原文）即便框内无墨也必须照旧绘制（rc=%d）" % rc2


# ------------------------------------------------------------------ 图片：缺陷②（2026-10-19 现网图片单）

def _ink_rows(path, rect):
    """rect 内**有墨的行数**（与 `_line_ink_height` 同一把尺子：非白即可）。

    抹底之后框内只剩我们画的那一行字 ⇒ 这个读数就是**实际落笔的墨迹高度**。
    """
    import numpy as np
    from PIL import Image
    arr = np.asarray(Image.open(str(path)).convert("RGB"))
    x0, y0, x1, y1 = rect
    sub = arr[y0:y1, x0:x1]
    return int((sub < 250).any(axis=2).any(axis=1).sum())


@leg("L28")
def leg_l28_thin_tall_rule_dropped():
    """★ 2026-10-19 缺陷②：细长竖条（标题左侧色条、表格竖线）不是文字，必须与框线同尺剔除。

    现网实证：`因子战法` 框里那条 6px 宽、44px 高的蓝色色条把 ink_h 从 25 抬到 44 ⇒
    译文按 44 起算被放大成 32px，而同排**同级**标题只有 20px（用户报的「字号忽大忽小」）。
    造 60×160 白底样张：第 4~9 列、第 2~57 行一条**细而不满高**的色条（满高列已被 L20 判据
    吃掉，本腿守的是"细"这一维），另加 20~46 行 × 20~140 列的文字墨。正确读数＝文字的 27 行。
    """
    import numpy as np
    arr = np.full((60, 160, 3), 255, dtype=np.uint8)
    arr[2:58, 4:10, :] = 0      # 细长色条：6 列 × 56 行（刻意不满高，绕开 L20 的满列判据）
    arr[20:47, 20:141, :] = 0   # 文字墨块：行 20..46
    h = IMG._line_ink_height(_rgb_image(arr))
    assert h == 27, "细长色条没被剔除（读数 %d，期望文字本身的 27 行）" % h


@leg("L29")
def leg_l29_wide_ink_run_is_kept():
    """★ 同批**反证腿**：宽墨块/粗笔画不许被当色条剔掉——判据两维缺一不可，只按覆盖率一味剔会把字切没。

    现网字里 `の` 左弧连续 **13 列**覆盖率 ≥0.6（实测值，见 `RULE_MAX_COLS` 常量注释）；
    这里就用 13 列 × 42 行（覆盖率 0.70）的实心块当最接近的形态：读数必须是块本身的 42 行。
    宽度上限一旦放到 ≥13 就会把它剔空、读数变 0（字号随即退到"框高 ×0.86"的兜底档）。
    """
    import numpy as np
    arr = np.full((60, 160, 3), 255, dtype=np.uint8)
    arr[8:50, 40:53, :] = 0     # 13 列 × 42 行、覆盖率 0.70：宽度超上限 ⇒ 必须保留
    h = IMG._line_ink_height(_rgb_image(arr))
    assert h == 42, "宽墨块被误当色条剔除（读数 %d，期望 42）——宽度上限把文字切没了" % h


@leg("L30")
def leg_l30_layout_fits_by_real_ink_block_height():
    """★ 2026-10-19 缺陷②：`_layout` 的"放得下"必须按**真实墨迹块高**判，不是名义块高。

    名义式 `px*LINE_SPACING*(n-1)+px` 漏算下伸部与字体自带留白 ⇒ 判"放得下"而实际越框，
    越出去的那半行随后被**邻行的底色**抹掉（现网 `Strategy` 只剩半截的真身）。两段判据：
      ① 源码级——`_layout` 必须拿 `_block_ink_h` 现算块高（换回名义式即红）。这一处只有源码问得出
         "用的是哪一把尺子"，与 L18「挑字函数写对但没被绘制调用＝白修」同一口径；
      ② 语义级——任何盒高下凡报 `overflow=0`，真实墨迹高度必须 ≤ 盒高（不许虚报放得下）。
    """
    import inspect
    src = inspect.getsource(IMG._layout)
    assert "_block_ink_h(" in src, "布局没走真实墨迹块高（名义块高漏算下伸部 ⇒ 越框被邻行底色抹半截）"
    flat = "".join(src.split())
    assert "LINE_SPACING)*(len(lines)-1)" not in flat, "块高又变回名义式了（漏算下伸部与字体留白）"
    for h in (18, 22, 26, 30, 40, 63):
        for floor in (8, 12):
            px, lines, overflow = IMG._layout("Volatility Breakout Strategy", 120, h, floor, 25)
            ink = IMG._block_ink_h(IMG._chain(px), lines, px, int(px * IMG.LINE_SPACING))
            if not overflow:
                assert ink <= h, "报 overflow=0 但真实墨迹 %d > 盒高 %d（越框那半行会被邻行抹掉）" % (ink, h)


@leg("L31")
def leg_l31_ink_measured_on_frozen_reference():
    """★ 2026-10-19 缺陷②：量墨迹/取底色/试扩框必须看**绘制前的原图**，不许看就地改写的画布。

    机制：OCR 的框**本身就互相重叠**（现网单相邻两行重叠 9px）。逐行就地绘制时，上一行画下的字
    （含下伸部）落进下一行的框内；拿画布去量，下一行的 ink_h 就被读成「上一行的尾巴 + 自己的字」
    （现网 fac_1 实证 21→44 ⇒ 字号 19px 涨成 36px）。判据：下框里**实际落笔的墨迹高度**必须等于
    按原图墨迹算出来的那一档，而不是按"上一行尾巴"算出来的那一档。
    ⚠️ 夹具自证（原地留档）：两档字号必须真拉开——哪天 `_layout` 不再按墨迹起算，两档会相等，
      这条腿就退化成空转锁，故先断言拉开了再比读数。反证在 Go 侧：把量测改回 `img.crop` 必红。
    """
    import numpy as np
    with tempfile.TemporaryDirectory() as d:
        src, out = Path(d) / "s.png", Path(d) / "o.png"
        arr = np.full((150, 200, 3), 255, dtype=np.uint8)
        arr[66:82, 30:90, :] = 0     # 下框**自己的**原墨迹：行 66..81 ⇒ ink_h=16
        _rgb_image(arr).save(src)
        box_a, box_b = [20, 20, 180, 70], [10, 56, 190, 110]   # 两框行向重叠 56..70
        rc = _render(src, out, [
            {"text": "上", "box": box_a, "tran": "Neighbor Overlap Text Spills Downwards"},
            {"text": "下", "box": box_b, "tran": "Bottom"},
        ])
        assert rc == 0, "两行重叠框渲染失败（rc=%d）" % rc
        ref = _rgb_image(arr)
        r0 = IMG._box_rect(box_b)
        left, top, right, bottom = IMG._expand_rect(ref, r0, [IMG._box_rect(box_a)])
        iw, ih = max(1, right - left - 2), max(1, bottom - top - 2)
        want_px = IMG._layout("Bottom", iw, ih, IMG._font_floor(16, ih), 16)[0]
        bad_px = IMG._layout("Bottom", iw, ih, IMG._font_floor(27, ih), 27)[0]
        assert bad_px > want_px + 3, \
            "夹具自证不过：污染档与参照档字号没拉开（%d vs %d）⇒ 这条腿是空转的" % (bad_px, want_px)
        got = _ink_rows(out, r0)
        chain = IMG._chain(want_px)
        exp = IMG._block_ink_h(chain, IMG._wrap_text(chain, "Bottom", iw), want_px, int(want_px * IMG.LINE_SPACING))
        assert abs(got - exp) <= 2, \
            "下框字号按**被污染的画布**算了（实读墨高 %d，按原图应 %d；污染档字号 %d）" % (got, exp, bad_px)


@leg("L32")
def leg_l32_drawn_ink_never_leaves_its_box():
    """★ 缺陷②：越框的墨迹一律丢弃（掩膜按框裁剪）——落进邻行地盘就会被邻行底色抹掉半截。

    造一张**纯白**原图（原图无墨 ⇒ 框外任何墨点都只能是我们画的）：上框放一段放不下的长译文
    （到字号地板仍越框），下框故意是坏框（跳过、永不被重绘）⇒ 上框**之外**必须一个墨点都没有。
    反证在 Go 侧：贴回改成"整张掩膜直接贴"（不按框裁剪）必红。
    """
    import numpy as np
    from PIL import Image
    with tempfile.TemporaryDirectory() as d:
        src, out = Path(d) / "s.png", Path(d) / "o.png"
        _blank_png(src)
        rc = _render(src, out, [
            {"text": "上", "box": [20, 20, 200, 50],
             "tran": "A very long translated sentence that cannot fit in this tiny box"},
            {"text": "下", "box": [], "tran": "bad box"},
        ])
        assert rc == 0, "渲染失败（rc=%d）" % rc
        arr = np.asarray(Image.open(str(out)).convert("RGB"))
        below = arr[51:120, :, :]     # 上框 (20,20,200,50) 加 1px 边距后的下沿＝行 51
        leaked = int((below < 250).any(axis=2).sum())
        assert leaked == 0, "墨迹越过了自己的框（框下还有 %d 个墨点）——会被邻行底色抹成半截字" % leaked


# ------------------------------------------------------------------ ASR：无语音门（⭐ 2026-10-19 G11）

@leg("L24")
def leg_l24_no_speech_gate():
    """★ 2026-10-19 G11：纯静音/极低能量必须判无语音（否则模型在静音上幻觉出一句话）。

    判据：全零/空采样 → no_speech；**小声真人语音不许被误伤**（宁可放过静音不可误伤真人）；
    且 `cmd_transcribe` 真的调用了这道门（函数写对却没人调用＝白修）。
    """
    import inspect
    import numpy as np
    assert ASR.detect_no_speech(np.zeros(16000, dtype=np.float32)) == ASR.REASON_NO_SPEECH, "全零采样必须判无语音"
    assert ASR.detect_no_speech(np.zeros(0, dtype=np.float32)) == ASR.REASON_NO_SPEECH, "空采样必须判无语音"
    t = np.arange(16000, dtype=np.float32)
    quiet = (0.01 * np.sin(2 * np.pi * 220.0 * t / 16000.0)).astype(np.float32)  # rms≈0.007 的小声语音
    assert ASR.detect_no_speech(quiet) == "", "小声真人语音被误判为无语音（宁可放过静音，不可误伤真人）"
    assert "detect_no_speech" in inspect.getsource(ASR.cmd_transcribe), "无语音门没被 cmd_transcribe 调用＝白修"


def main() -> int:
    # 遍历注册表而不是手写函数名清单：这样「删掉一条腿」必然让那一行 LEG 消失（Go 侧核全集即红），
    # 而手写清单会同时删掉注册与调用，看起来像「重构」，实际是把射程摘掉了。
    for _, runner in LEGS:
        runner()
    if FAILURES:
        print("CAPSHAPE_ALL_OK=0 failed=%s" % ",".join(FAILURES))
        return 1
    print("CAPSHAPE_ALL_OK=1 legs=%d" % len(PASSED))
    return 0


if __name__ == "__main__":
    sys.exit(main())
