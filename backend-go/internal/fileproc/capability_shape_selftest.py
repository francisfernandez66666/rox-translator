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

射程（13 条腿，逐条点名）：
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

环境变量（**只给 Go 侧反证用**，把被测对象换成一份变异副本，证明这些腿真的会红）：
  CAPSHAPE_OCR_MODULE=<path>  指定被测的 ocr_recognize.py 副本
  CAPSHAPE_ASR_MODULE=<path>  指定被测的 asr_transcribe.py 副本
"""
import importlib.util
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
