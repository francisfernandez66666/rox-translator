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

射程（逐条点名；**条数以 @leg 注册与 Go 侧 capShapeLegs 的双向等值锁为准**，本文不写死数字）：
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
   L33 图片回填：抹字底色**先认框内纯色块**（徽章/胶囊），杂色时仍退环带（★ 2026-10-19 缺陷②尾项）
   L34 图片回填：超长 token **先在分隔符后断**（`Yuan/`+`time`），无分隔符才按字符硬切、不丢字符（同批）
   L35 图片回填：有"不劈词也放得下"的字号就先降字号（1px 档），不把词劈开当第一选择（同批）
   L36 图片回填：非语言行（单字噪声/符号 `<`/图形 `⇅`）交付原文时不重画，原件像素逐字节保住；
       图形行剔除计数进 INFO（同批；★ 2026-10-21 M9 由「单字非 ASCII」扩到「整行无字母」）
   L37 图片回填：译后图沿用**原字体色**——浅蓝徽章的白字仍白、白底黑字仍黑（★ 2026-10-19 用户令）
   L38 图片回填：字色取**墨迹核心**（前 FG_CORE_PCT%），抗锯齿过渡色不把估计拉向底色（同批）
   L39 图片回填：估不准（无墨/墨少/无底色）回落底色反差档，且渲染真调用了估计函数（同批）
   L40 图片回填：**同一区域字号归一**——行高相近(±20%)+同表格行(y 重叠) ⇒ 归到众数（并列取较小），
       且 region_px 真被 `_layout` 采纳/装不下时落回自己的档（★ 2026-10-28 用户令）
   L41 图片回填：归一判据的**保守腿**——行高差>20% 或垂直远隔的行**不许**被并成同字号（含并组正对照）
   L42 图片回填：`-1` 哨兵档不参与分组、不进众数、不从邻居借字号（原样保留）

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


@leg("L43")
def leg_l43_box_thresh_drops_low_probability_boxes():
    """框内概率门：均值 0.45 的候选（开关/图标）被丢，0.85 的真字留下；传 0 即关闭（回滚位）。"""
    import numpy as np

    sh, sw = 128, 128
    score = np.zeros((sh, sw), dtype=np.float32)
    score[20:40, 20:60] = 0.45   # 开关/图标那一档
    score[60:80, 20:60] = 0.85   # 真字
    boxes = OCR._det_boxes(score, (sw, sh))
    assert len(boxes) == 1, "低概率框应被 box_thresh 挡住，实读 %d 个框：%r" % (len(boxes), boxes)
    # 框会被 unclip 外扩，所以按**中心**判它属于下面那一格，而不是按 y0。
    assert (boxes[0][1] + boxes[0][3]) / 2 > 50, "剩下的必须是真字那一格，实读 %r" % (boxes[0],)
    # 0 档＝关（这也是本件的回滚位：DET_BOX_THRESH=0 等价旧行为）。
    boxes0 = OCR._det_boxes(score, (sw, sh), box_thresh=0.0)
    assert len(boxes0) == 2, "关掉概率门后两个框都该在，实读 %d" % len(boxes0)


@leg("L44")
def leg_l44_min_area_is_relative_to_map_area():
    """面积下限按概率图面积相对计算：同一 10px 小块，小图留、大图丢。"""
    import numpy as np

    def blob(sh, sw):
        score = np.zeros((sh, sw), dtype=np.float32)
        score[10:12, 10:15] = 1.0  # 恰好 10 个像素
        return score

    small = OCR._det_boxes(blob(64, 64), (64, 64))
    assert len(small) == 1, "小图上 10px 小块应保留（相对阈值≈4），实读 %r" % (small,)
    big = OCR._det_boxes(blob(960, 960), (960, 960))
    assert big == [], "大图上同一 10px 小块应被相对阈值（≈37）丢掉，实读 %r" % (big,)


@leg("L45")
def leg_l45_reading_order_adapts_to_line_height():
    """50px 行高的两行必须被切成两行（旧 20px 固定行带会把它们并成一行）。"""
    boxes = [[10, 0, 60, 49], [100, 60, 160, 109], [10, 60, 60, 109]]
    out = OCR._order_boxes(boxes)
    assert out[0][1] == 0, "第一框必须是上面那一行，实读 %r" % (out[0],)
    assert out[1][0] < out[2][0], "同一行必须左到右，实读 %r" % (out[1:],)


@leg("L46")
def leg_l46_conf_excludes_blank_frames():
    """置信度只对被采纳的字符帧取均值：含 blank 帧的旧口径会把长框系统性拉低。"""
    from PIL import Image
    import numpy as np

    # (1,T,C)：帧 1,0,1,0,1（0＝blank）；被采纳帧的最高 logit 是 5.0，空帧是 1.0。
    logits = np.full((1, 5, 4), -1.0, dtype=np.float32)
    for t, cls in enumerate([1, 0, 1, 0, 1]):
        logits[0, t, cls] = 5.0 if cls != 0 else 1.0
    sess = FakeSession([None, 3, 48, 4], [logits])
    words = ["", "a", "b", "c"]
    text, conf = OCR._run_rec(sess, Image.new("RGB", (40, 20), "white"), words)
    assert text == "aaa", "解码结果不对：%r" % (text,)
    assert abs(conf - 5.0) < 1e-6, "conf 应只数被采纳字符帧（=5.0），实读 %r（含 blank 的旧口径≈3.4）" % (conf,)


@leg("L47")
def leg_l47_crop_padding_is_bounded_by_neighbor_midline():
    """裁剪白边：**默认值必须是 0.0（回滚档）**；机制腿在显式 0.15 下验「与邻框中线取小」的夹逼。

    ★ 2026-10-28 图片版式回退批的**二次翻转**：白边默认 0.15 在密表件上是净负收益——
    同一张 2408px 参照图实测 `double_bump`→`double_bum`、`600000.SH`→`60000.SH`、
    时间两格被吃成一行 `09-1509:57:20`（水平白边吃进邻格；竖直白边把字在 rec 输入里压矮）。
    归零后这三处全恢复、`の` 仍被 `DET_BOX_THRESH` 治住、框数不变（白边只影响 crop、不影响出栈框坐标，
    版式不受牵连）。`manua` 那一族（rec 丢尾字母）改由 AI 修正腿治（待定项①），不由白边兜。
    机制本身仍要有人看着：所以把 0.15 显式塞进去验夹逼还在——翻转理由写在这里，不删断言。
    """
    from PIL import Image

    assert OCR.REC_CROP_PAD_RATIO == 0.0, "白边默认值不是回滚档 0.0（密表件实测净负收益，见腿注释）"

    img = Image.new("RGB", (200, 100), "white")
    boxes = [[50, 40, 80, 60], [100, 40, 130, 60]]
    orig = OCR.REC_CROP_PAD_RATIO
    OCR.REC_CROP_PAD_RATIO = 0.15
    try:
        crop = OCR._crop_with_pad(img, boxes, 0)
        # pad=max(2,round(0.15*21))=3；右边界应被邻框中线 (80+100)//2=90 夹住。
        assert crop.size == (37, 27), "白边裁剪尺寸不符（期望 37x27），实读 %r" % (crop.size,)
        assert crop.size[0] <= 90 - 47 + 1, "白边越过了邻框中线"
    finally:
        OCR.REC_CROP_PAD_RATIO = orig


@leg("L48")
def leg_l48_result_confs_align_with_boxes():
    """输出契约：confs 与 boxes 等长同序，且 confidence 是逐行 conf 的均值。"""
    out = OCR._image_result(["a", "b"], [[0, 0, 1, 1], [0, 2, 1, 3]], [0.9, 0.5], 0)
    assert list(out.keys()) == ["text", "boxes", "confs", "confidence", "skipped_pages"], "输出字段集合变了：%r" % (out,)
    assert len(out["confs"]) == len(out["boxes"]) == 2, "confs 与 boxes 必须等长"
    assert abs(out["confidence"] - 0.7) < 1e-6, "confidence 应为逐行均值，实读 %r" % (out["confidence"],)


@leg("L49")
def leg_l49_pipeline_calls_new_stages():
    """接线腿（★ 2026-10-28 图片版式回退批**按新口径翻转**）：裁剪腿必须走 `_crop_all`；
    检测腿必须**不并框**——同一行两个分离色块必须出**两个**框。

    旧期望是「检测腿必须调用 `_merge_fragments`」（今天凌晨质量链批写的）。本批把它**翻转而不是删掉**：
    并框正是用户报「字体大大小小、版式完全不还原」的回归源——版式还原完全以「框」为输入
    （每框独立按原墨迹高定字号），多元素并进单框 ⇒ 字号被压扁。
    留着旧期望会让下一个人按旧口径把合并写回来，所以翻转理由写在这里（AGENTS §一·5 那条纪律）。
    合并件本身也已从脚本里摘除（`hasattr` 那条），形状锁＝`ocr_recognize_shape_gate_test.go`。
    """
    from PIL import Image
    import numpy as np

    img = Image.new("RGB", (100, 60), "white")
    boxes = [[10, 10, 40, 30], [50, 10, 80, 30]]
    calls = []
    orig_crop = OCR._crop_with_pad

    def spy_crop(im, bs, i):
        calls.append(i)
        return orig_crop(im, bs, i)

    OCR._crop_with_pad = spy_crop
    try:
        crops = OCR._crop_all(img, boxes)
    finally:
        OCR._crop_with_pad = orig_crop
    assert len(crops) == 2 and calls == [0, 1], "裁剪腿没走 _crop_with_pad（函数写对但没被调用＝白修）"

    assert not hasattr(OCR, "_merge_fragments"), "碎片合并函数还挂在模块上（并框形态没摘干净）"
    assert not hasattr(OCR, "_unclip_merge"), "同行膨胀合并函数还挂在模块上（并框形态没摘干净）"

    # 同一行、左右分离的两个色块：并框会把它们收成一块（字号压扁的起点），逐元素必须出两框。
    score = np.zeros((64, 64), dtype=np.float32)
    score[10:20, 10:25] = 1.0
    score[10:20, 30:40] = 1.0
    got = OCR._det_boxes(score, (64, 64))
    assert len(got) == 2, "同一行两个分离色块被并成一框（实得 %d 框）⇒ 框粒度回归源又长回来了" % len(got)


@leg("L50")
def leg_l50_hard_break_marks_latin_word_end():
    """★ 2026-10-29 用户令：拉丁词**不得不**按字符拆开时，前段末尾挂连字符。

    现网实证：表格里 `momentum` 被画成 `momentu`＋`m`。拉丁文字里"两段并排的完整词"
    读起来是一个短语（`home lander`＝两个词），换行处不挂符号就是歧义本体；
    印刷排版对拆词本来有加软连字符的通行做法（`home-`／`lander`），这里取同一条。

    四条腿各治一病，缺一条这条锁就不完整：
      ① 词内硬切的每一段（末段除外）都以符号收尾，剥掉符号逐字等于源词（**只加不减字符**）；
      ② **符号宽度必须算进"装得下"的判据**：把 max_w 钉在恰好装得下 `Synchron` 那一档，
         不预留就会产出 `Synchron-`（比框还宽＝字被裁掉，比劈词更坏）；
      ③ 分隔符软断**不挂符号**（`Yuan/`＋`time` 是正常排版，加符号＝凭空造出原文没有的字符）；
      ④ CJK 逐字可断**不挂符号**（中文没有拆词概念，挂上去是脏字符）。
    """
    chain = IMG._chain(19)
    mark = IMG.HARD_BREAK_MARK
    # ①＋② 词内硬切：取"恰好装得下 Synchron"这一档——不预留符号宽就会在这一档超框。
    src = "Synchronized"
    max_w = IMG._measure(chain, "Synchron")
    lines, hard = IMG._wrap_text_dbg(chain, src, max_w)
    assert hard is True and len(lines) >= 2, "夹具自证不过：这一档没走到词内硬切（%r hard=%r）" % (lines, hard)
    assert all(l.endswith(mark) for l in lines[:-1]), \
        "词内硬切没挂连字符（%r）——客户读到的是两个词，不是被换行拆开的一个词" % (lines,)
    stripped = "".join(l[:-1] if l.endswith(mark) else l for l in lines)
    assert stripped == src, "挂符号把字符弄丢了（%r → %r）" % (lines, stripped)
    over = [l for l in lines if IMG._measure(chain, l) > max_w + 1e-6]
    assert not over, "符号宽度没算进判据，行宽超框（%r）⇒ 加完连字符反而把字裁掉" % (over,)
    # ③ 分隔符软断不挂符号。
    soft_w = IMG._measure(chain, "Yuan/time") - 4
    assert IMG._measure(chain, "Yuan/") <= soft_w < IMG._measure(chain, "Yuan/time"), \
        "夹具自证不过：这一档要么装得下整个 token、要么连 `Yuan/` 都装不下"
    soft, soft_hard = IMG._wrap_text_dbg(chain, "Yuan/time", soft_w)
    assert soft == ["Yuan/", "time"] and soft_hard is False, \
        "软断形态变了（%r hard=%r）——在可读断点断词不需要符号" % (soft, soft_hard)
    assert all(not l.endswith(mark) for l in soft[:-1]), "软断也挂了符号（%r）" % (soft,)
    # ④ CJK 逐字断行不挂符号。
    cjk_w = IMG._measure(chain, "同步")
    cjk, _ = IMG._wrap_text_dbg(chain, "同步协调机制", cjk_w)
    assert "".join(cjk) == "同步协调机制", "中文断行把字符弄丢了（%r）" % (cjk,)
    assert all(mark not in l for l in cjk), "中文行被挂了连字符（%r）＝原文里没有的脏字符" % (cjk,)


@leg("L51")
def leg_l51_overlapping_boxes_split_by_midline():
    """★ 2026-10-29 用户复跑实证：两个框**共用像素** ⇒ 后画的把先画的抹掉半截（字叠字）。

    现网形态＝卡片里「0胜 / 0负」的框与下面「卖出胜负」的框垂直重叠十几个像素，
    两框各自抹底＋画字，客户看到两行字叠在一起（"字跑到别处"那一族）。
    五条腿各治一病：
      ① 上下叠放 ⇒ 沿 **y** 夹、各让重叠带的一半，夹完两框不再共像素；
      ② 左右并排 ⇒ 沿 **x** 夹（夹错轴会把整行推出框外）；
      ③ **包含关系不动**（大框是容器，小框本来就该在里面画——夹它等于把正文夹没）；
      ④ 不相交的／**只共 1px 边距的相邻两行**原样返回（两条负向对照：证明判据不是恒真，
         否则 ① 的红可能只是"全都改了"；贴边那一档还防住"白扣两行各 1px 行高 ⇒ 全站白降字号"）；
      ⑤ 接线腿：`cmd_render` 真调了它（函数写对但没被调用＝白修）。
    """
    import inspect

    R = IMG._resolve_box_overlap
    # ① 上下叠放：A=[0,100,200,140]、B=[20,130,180,170] ⇒ y 向重叠带 130..140，中线 135
    out, n = R([(0, 100, 200, 140), (20, 130, 180, 170)])
    assert n == 1, "上下叠放的两个框没被夹（实读 trimmed=%d）" % n
    a, b = out
    assert a[3] == b[1] == 135, "没按重叠带中线各让一半（实读 %r %r）" % (a, b)
    assert a[0] == 0 and a[1] == 100 and a[2] == 200, "夹错了不该动的维度（%r）" % (a,)
    assert b[0] == 20 and b[2] == 180 and b[3] == 170, "夹错了不该动的维度（%r）" % (b,)
    # ② 左右并排（x 向重叠 10、y 向重叠 40）⇒ 必须夹 x
    out2, n2 = R([(0, 0, 100, 40), (90, 5, 190, 45)])
    assert n2 == 1, "左右并排的两个框没被夹（trimmed=%d）" % n2
    assert out2[0][2] == out2[1][0] == 95, "没沿 x 夹（实读 %r %r）——夹错轴会把整行推出框外" % (out2[0], out2[1])
    assert out2[0][3] == 40 and out2[1][3] == 45, "沿 x 夹时动了 y（%r）" % (out2,)
    # ③ 包含关系原样
    big, small = (0, 0, 400, 300), (100, 100, 160, 120)
    out3, n3 = R([big, small])
    assert n3 == 0 and out3 == [big, small], "包含关系被夹了（trimmed=%d，%r）⇒ 小框会被大框的抹底吃掉" % (n3, out3)
    # ④ 不相交原样（负向对照）
    far = [(0, 0, 50, 20), (60, 0, 110, 20)]
    out4, n4 = R(far)
    assert n4 == 0 and out4 == far, "不相交的框被动了（trimmed=%d）⇒ 这条判据恒真，① 的红不作数" % n4
    # ④b **贴边不算碰撞**：`_box_rect` 每边自带 1px 边距 ⇒ 相邻两行天然重叠 2px。
    #     夹它等于白扣两行各 1px 行高，而字号地板按行高算 ⇒ 全站文字白降一档（比"字叠字"更坏）。
    near = [(10, 19, 190, 41), (10, 39, 190, 61)]
    out4b, n4b = R(near)
    assert n4b == 0 and out4b == near, "只共 1px 边距的相邻两行被夹了（trimmed=%d）⇒ 白降字号" % n4b
    # ⑤ 接线：cmd_render 必须真调（且只调一次——两处各夹一遍就是两把尺子）
    src = inspect.getsource(IMG.cmd_render)
    assert src.count("_resolve_box_overlap(rects)") == 1, "重叠消解没接进 cmd_render（或接了两次）"


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
    ⚠️ 框内**刻意是"字色混杂"形态**（墨块占约四成面积，普通文字行就是这样）——框内一旦是纯色块，
    第一判据（`BG_INNER_PURE_MIN`，L33 守它）会先命中并返回框内底色，这条路就走不到环带了：
    2026-10-19 尾项加框内判据时，这枚反证正因夹具"框内也是一整块纯色"而**失去射程**
    （L22 的变异体不再让 L22 红），故夹具按"真文字行的框内形态"改成混杂。
    """
    import numpy as np
    arr = np.full((120, 200, 3), (30, 90, 200), dtype=np.uint8)   # 一块蓝色底
    arr[56:64, 86:114, :] = 0                                     # 框内字色墨块（约四成面积）
    bg = IMG._pick_bg(_rgb_image(arr), (80, 50, 120, 70))
    assert bg == (30, 90, 200), "彩色底的抹字底色被改成了别的东西（实读 %r）" % (bg,)
    # 白底文档：环带中位色＝白
    white = np.full((120, 200, 3), 255, dtype=np.uint8)
    white[56:64, 86:114, :] = 0
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

    ★ 2026-10-21 M9 夹具调整：源文改用**真单词**（`sync`）而不是 `0`。
      `0` 现在会先被「无字母行」那一档（symbol_identity）跳过，于是本腿在 M9 反证里
      被别人的判据掩护成恒绿（首跑实测：把 no_ink_identity 摘掉后 L27 仍然 OK）。
      用带字母的词，「无墨框」这一条才有独立的射程；数字/符号那一档由 L36 单独守。
    """
    with tempfile.TemporaryDirectory() as d:
        src, out = Path(d) / "s.png", Path(d) / "o.png"
        _blank_png(src)
        rc = _render(src, out, [{"text": "sync", "box": [10, 10, 200, 80], "tran": "sync"}])
        assert rc != 0 and not out.exists(), "无墨框 + 原文交付不许造出幽灵内容（rc=%d，产物=%s）" % (rc, out.exists())
        rc2 = _render(src, out, [{"text": "sync", "box": [10, 10, 200, 80], "tran": "同步"}])
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
    ★ 2026-10-28（区域归一批把量测/绘制拆成两遍后）**语义腿对那枚变异不再敏感**——所有量测
      都发生在任何绘制之前，"看活画布"在结构上就够不着污染了。拆两遍**是更强的修法**，
      但闸门不能因此失明：变异锚点走**源码形态**那条（`patch = ref_img.crop(r0)` 逐字钉住，
      同 L30/L39「写对却没被调用＝白修」的源码级口径），语义腿照旧跑真渲染当正向对照。
    """
    import inspect
    import numpy as np
    assert "patch = ref_img.crop(r0)" in inspect.getsource(IMG.cmd_render), \
        "量墨迹的参照系锚点漂了（不再是逐字 ref_img.crop）——Go 侧那枚 img.crop 变异会失去射程"
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
        # ★ 2026-10-29：期望值必须按**管线真正用的那份几何**算——本批在 `cmd_render` 里加了一步
        #   框间重叠消解（L51），这条夹具自己重算几何却手抄 `_box_rect`，会量到一条已不存在的管线。
        #   所以这里改为复用同一个 `_resolve_box_overlap`（不是把期望值调宽，判据一字未动）。
        rects, _tn = IMG._resolve_box_overlap([IMG._box_rect(box_a), IMG._box_rect(box_b)])
        r0 = rects[1]
        left, top, right, bottom = IMG._expand_rect(ref, r0, [rects[0]])
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


# ---------------------------------------------- 图片：缺陷②尾项（2026-10-19 现网图片单·外观）

@leg("L33")
def leg_l33_bg_prefers_pure_inner_block_over_ring():
    """★ 2026-10-19 尾项：抹字底色**先认框内纯色块**（徽章/胶囊），环带只在框内不是纯色块时才用。

    现网实证两处：蓝徽章的框内底色 `(180,198,255)`、环带却是白（蓝底被填成白块）；绿开关同理。
    根因＝「框内底色 == 框外底色」这条假设对**徽章族**不成立（见 `_pick_bg` 文档）。
    反证腿（同一条腿内的第二半）：框内是**杂色**（不是纯色块）时必须退回环带——否则会把随机一个
    框内像素当成底色，普通文字行会被抹出一块字色。
    """
    import numpy as np
    # ① 蓝徽章：框内一整块浅蓝（含两成字色像素），框外一圈深蓝"控件边框"、再外是白底。
    arr = np.full((140, 220, 3), 255, dtype=np.uint8)
    arr[28:92, 48:152, :] = (30, 90, 200)          # 控件边框（恰好落在 BG_RING_PX 环带里）
    arr[40:80, 60:140, :] = (180, 198, 255)        # 框内浅蓝底
    arr[52:68, 84:116, :] = (40, 40, 40)           # 框内字色像素（16% 面积 ⇒ 纯色占比 0.84）
    bg = IMG._pick_bg(_rgb_image(arr), (60, 40, 140, 80))
    assert bg == (180, 198, 255), "徽章的框内浅蓝底没被认出（实读 %r）⇒ 蓝徽章会被填成环带的白/深蓝" % (bg,)
    # ② 框内杂色：不是纯色块 ⇒ 必须退回环带中位色（这里是深蓝边框），不许拿框内某个像素当底色。
    arr2 = np.full((140, 220, 3), 255, dtype=np.uint8)
    arr2[28:92, 48:152, :] = (30, 90, 200)
    noise = np.random.default_rng(7).integers(0, 256, size=(40, 80, 3), dtype=np.uint8)
    arr2[40:80, 60:140, :] = noise
    bg2 = IMG._pick_bg(_rgb_image(arr2), (60, 40, 140, 80))
    assert bg2 == (30, 90, 200), "框内杂色时没退回环带（实读 %r）⇒ 会把随机字色当底色抹开" % (bg2,)


@leg("L34")
def leg_l34_break_token_prefers_separator_break():
    """★ 2026-10-19 尾项：超长 token 断行**先在分隔符后断**，实在没有分隔符才按字符硬切。

    现网实证：`Yuan/time` 被切成 `Yuan/t`+`ime`、`Synchronized` 被切成 `Synchronize`+`d`——
    词被劈开（用户报的"词断得莫名其妙"）。分隔符 `/ - _ ·` 本来就是可读断点。
    两半一起判：软断（hard=False）与硬切（hard=True）必须分得开，否则 `_layout` 的
    「能不劈词就不劈」（L35 守它）无从判断。软断/硬切都不许丢字符。
    """
    chain = IMG._chain(19)
    max_w = IMG._measure(chain, "Yuan/time") - 4
    assert IMG._measure(chain, "Yuan/") <= max_w < IMG._measure(chain, "Yuan/time"), \
        "夹具自证不过：这个宽度要么装得下整个 token、要么连 `Yuan/` 都装不下"
    lines, hard = IMG._wrap_text_dbg(chain, "Yuan/time", max_w)
    assert lines == ["Yuan/", "time"], "没在分隔符后断（实读 %r）——词又被劈开了" % (lines,)
    assert hard is False, "分隔符软断被报成硬切（`_layout` 会据此白降字号）"
    assert "".join(lines) == "Yuan/time", "软断把字符弄丢了（%r）" % (lines,)
    assert IMG._wrap_text(chain, "Yuan/time", max_w) == ["Yuan/", "time"], \
        "`_wrap_text` 的旧签名/返回值变样了（既有调用点会跟着坏）"
    # 没有分隔符的超长词：只能按字符硬切，同样不许丢字符。
    # ★ 2026-10-29 口径翻转（用户令）：硬切出来的**前段必须挂连字符**——`home`＋`lander` 在拉丁文字里
    # 读起来是"两个词"，而 `home-`＋`lander` 才说得出"这是一个被换行拆开词"。所以这里剥掉行尾符号后
    # 逐字等于源词（字符一个不少），且非末段一律以符号收尾（见 L50 专测这条形态）。
    w2 = IMG._measure(chain, "Synchroniz")
    lines2, hard2 = IMG._wrap_text_dbg(chain, "Synchronized", w2)
    assert hard2 is True, "无分隔符超长词没被认出是硬切（实读 hard=%r）" % (hard2,)
    assert len(lines2) >= 2, "硬切只出一段（%r）" % (lines2,)
    joined = "".join(l[:-1] if l.endswith(IMG.HARD_BREAK_MARK) else l for l in lines2)
    assert joined == "Synchronized", "硬切把字符丢了（%r → %r）" % (lines2, joined)


@leg("L35")
def leg_l35_layout_shrinks_before_splitting_word():
    """★ 2026-10-19 尾项：只要"不劈词也放得下"的字号存在，`_layout` 就先降字号（1px 一档），不劈词。

    现网实证：`Synchronized` 在徽章里被切成两行（`Synchronize`+`d`），而同排同级文字本该 20px——
    真因是布局把"硬切"当成了首选。本腿两半：
      ① 存在装得下单行的字号 ⇒ 必须返回 hard=False 且**真降到了那个字号**（不是原地裁切）；
      ② 字号到了地板仍装不下单行（窄框）⇒ 允许 hard=True，但**不许谎报越框**（盒够高时报 overflow=False）。
    """
    chain19 = IMG._chain(19)
    target_px = 14
    max_w = IMG._measure(IMG._chain(target_px), "Synchronized") + 4
    assert IMG._measure(chain19, "Synchronized") > max_w, \
        "夹具自证不过：19px 档就装得下 ⇒ 这条腿测不到「降字号 vs 硬切」"
    px, lines, overflow = IMG._layout("Synchronized", int(max_w), 60, 8, 24)
    assert px <= target_px, "没降字号就断词（实读 px=%d，期望 ≤%d）" % (px, target_px)
    chain = IMG._chain(px)
    assert all(IMG._measure(chain, l) <= max_w + 1 for l in lines), \
        "报「放得下」但行宽仍超框（%r）" % (lines,)
    lines_big, hard_big = IMG._wrap_text_dbg(chain, "Synchronized", max_w)
    assert hard_big is False, "降字号之后仍在劈词（%r）⇒ 白降" % (lines_big,)
    # ② 窄框（任何字号都装不下单行）：允许硬切，盒够高时不谎报缘框。
    px2, lines2, ovf2 = IMG._layout("Synchronized", 6, 220, 8, 24)
    assert px2 >= 8 and ovf2 is False, "窄框长词被谎报越框（px=%d overflow=%r）" % (px2, ovf2)


@leg("L36")
def leg_l36_symbol_line_not_repainted():
    """★ 2026-10-19 尾项＋2026-10-21 M9 扩形：源文**根本不是文字**（单字噪声/符号/箭头）且本轮交付原文 ⇒ 不重画。

    现网实证两代：
      · 一枚开着的绿色开关被 OCR 读成 `の`：旧形态把它整块涂绿再画一个白 `の`，白色圆钮就此消失；
      · 上下箭头图标被 OCR 读成 `1`/`<`：M9 之前只有「单字非 ASCII」那一档，`<` 被照原样画回图标上
        （客户看到的仍是"原文里根本没有的一个字"）。
    四条腿一起判（只判"不画"会让"整段漏绘"一路绿）：
      ① 单字噪声（`の`）交付原文 ⇒ 那个框的像素**逐字节不动**（开关得保住）；
      ② 同一框换成**真译文** ⇒ 必须照旧绘制（否则"不造字"变成"漏译"）；
      ③ 符号行（`<`）交付原文 ⇒ 不重画；图形行（`⇅`）**不管译文是什么都不画**；
         并且要先给这两个框**画上墨**——否则它们会走「空框不造字」那条腿，
         本腿测的就成了别人的判据（绿得毫无射程）。
      ④ 判据射程：`<`/`1`/`¥1003.00`/`⇅` 属「无字母行」，`D`/`Hello` 不是；
         `⇅` 属图形档、`<` 不属；`¥1003.00` 属内容档；脚本自报的 `dropped` 必须数到 1。
    """
    import contextlib
    import io
    import numpy as np
    from PIL import Image
    with tempfile.TemporaryDirectory() as d:
        src, out, out2 = Path(d) / "s.png", Path(d) / "o.png", Path(d) / "o2.png"
        arr = np.full((250, 260, 3), 255, dtype=np.uint8)
        arr[30:70, 40:140, :] = (3, 167, 111)      # "开关"：绿色实心块（原图本来就有墨）
        arr[90:120, 50:120, :] = (20, 20, 20)      # `<` 图标：先给墨，避免走"空框不造字"那条腿
        arr[150:180, 50:120, :] = (20, 20, 20)     # `⇅` 图标：同上
        _rgb_image(arr).save(src)
        box, box_b = [40, 30, 140, 70], [150, 80, 250, 115]
        box_c, box_d = [40, 90, 140, 120], [40, 150, 140, 180]
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = _render(src, out, [
                {"text": "の", "box": box, "tran": "の"},
                {"text": "Hello", "box": box_b, "tran": "你好"},
                {"text": "<", "box": box_c, "tran": "<"},
                {"text": "⇅", "box": box_d, "tran": "⇅"},
            ])
        assert rc == 0 and out.exists(), "整图渲染失败（rc=%d）" % rc
        got = np.asarray(Image.open(str(out)).convert("RGB"))
        before = arr[30:70, 40:140]
        assert np.array_equal(before, got[30:70, 40:140]), "单字噪声行的框被重画了（开关被涂改）"
        assert np.array_equal(arr[90:120, 50:120], got[90:120, 50:120]), "符号行 `<` 交付原文却被重画（图标被盖）"
        assert np.array_equal(arr[150:180, 50:120], got[150:180, 50:120]), "图形行 `⇅` 被重画（箭头被文字盖住）"
        err = buf.getvalue()
        assert "dropped=1" in err, "图形行剔除计数没进 INFO 行（排障读数丢了）：%r" % err
        assert "blank_skipped=2" in err, "两条「原文交付不重画」没计进 blank_skipped：%r" % err
        # ② 真译文必须照旧绘制
        rc2 = _render(src, out2, [{"text": "の", "box": box, "tran": "of"}])
        assert rc2 == 0, "真译文渲染失败（rc=%d）" % rc2
        got2 = np.asarray(Image.open(str(out2)).convert("RGB"))
        assert not np.array_equal(before, got2[30:70, 40:140]), "单字噪声行收到真译文却没画（漏译）"
        # ③ 判据射程（缺任何一条，上面两条"不重画"都可能只是别人的判据在挡）
        assert IMG._is_lone_nonascii("の") is True
        assert IMG._is_lone_nonascii(" の ") is True, "空白没被先剥掉"
        assert IMG._is_lone_nonascii("はの") is False, "两个字符＝可能是真词，不许跳"
        assert IMG._is_lone_nonascii("D") is False, "ASCII 短源文不许被单字噪声腿跳"
        assert IMG._is_lone_nonascii("") is False
        assert IMG._is_symbol_only_line("<") is True, "OCR 折出来的 `<` 必须算无字母行"
        assert IMG._is_symbol_only_line("1") is True, "OCR 折出来的 `1` 必须算无字母行"
        assert IMG._is_symbol_only_line("¥1003.00") is True, "金额行必须算无字母行"
        assert IMG._is_symbol_only_line("⇅") is True
        assert IMG._is_symbol_only_line("D") is False, "真文字（单字母）不许被判成符号"
        assert IMG._is_symbol_only_line("Hello") is False
        assert IMG._is_symbol_only_line("") is False
        assert IMG._is_graphic_only_line("⇅") is True and IMG._is_graphic_only_line("<") is False, "图形档射程错"
        assert IMG._is_content_only_line("¥1003.00") is True, "内容档射程错（¥ 必须在白名单里）"
        assert IMG._is_content_only_line("Hello") is False


# ---------------------------------------------- 图片：原字体色还原（⭐ 2026-10-19 用户令）

def _ink_median(arr, bg):
    """arr 里**笔画本色**的读数：与底色亮度差最大那一档（前 FG_CORE_PCT%）墨迹像素的中位色。

    用与实现同一把尺子取墨迹（`_ink_mask`）。为什么还要再取一次核心：暗字的**抗锯齿边缘**
    与底色差常常仍 > INK_LUMA_DELTA（黑字 50% 过渡＝中灰，差 119），它们会把整块墨迹的中位色
    拉到中灰（实测 (17,17,17) 被读成 (83,83,83)）——那是**读数手法**的偏差，不是画错了色。
    取核心即"笔画最实的那部分"，天线两侧一致：实现估色取核心，判据也只在核心上读。
    """
    import numpy as np
    lum = IMG._luma_plane(arr)
    bg_lum = IMG._luma_of(bg)
    ink = IMG._ink_mask(lum, bg_lum)
    assert int(ink.sum()) > 0, "框内一个墨点都没有（夹具没画上字，或实现把这一行漏绘了）"
    delta = np.abs(lum - bg_lum)
    core = ink & (delta >= float(np.percentile(delta[ink], IMG.FG_CORE_PCT)))
    return tuple(int(round(v)) for v in np.median(arr[core], axis=0))


def _dist(a, b) -> float:
    """两色的欧氏距离（judge "落笔色更接近谁"用；抗锯齿会把落笔色往底色带一点，
    等值判据在换机器/换字体时会假红，比"离哪个候选色更近"才是这条腿真问的事）。"""
    return sum((float(a[i]) - float(b[i])) ** 2 for i in range(3)) ** 0.5


@leg("L37")
def leg_l37_render_keeps_original_font_color():
    """★ 2026-10-19 用户令：译后图必须沿用**原字体色**（不只是原背景色）。

    旧形态只按底色亮度二选一 ⇒ 浅蓝徽章（底 (180,198,255)，亮度 199≥128）上的**白字**被画成黑字，
    红/绿/品牌色文字一律被拍成黑或白——用户报的就是「译文把原来的配色改掉了」。
    判据取"落笔色**更接近原字体色**、且离旧口径那个色足够远"（不判逐字节等值：抗锯齿会把落笔色
    往底色方向带一点，等值判据在换机器/换字体时假红——这条腿问的是"用了哪一档色"，两档相距 >400）。
    两半一起判（只判"蓝徽章出白字"会让"恒白字"也一路绿）：
      ① 浅蓝徽章 + 白字 ⇒ 落笔色必须接近白（旧口径会是黑）；
      ② 白底 + 黑字 ⇒ 必须接近黑（不许一律改成"更亮的那一档"）。
    """
    import numpy as np
    from PIL import Image
    with tempfile.TemporaryDirectory() as d:
        box = [80, 60, 240, 160]
        # ① 浅蓝徽章：框＝徽章（字块占 15% ⇒ 纯色占比 0.85 ≥ BG_INNER_PURE_MIN，底色取徽章蓝）
        #    每档配一个"竞争色" alt：徽章档的竞争色＝旧口径会画的黑，文档档＝反相的白。
        for tag, base, orig, alt, out_src, out_dst in (
            ("badge", (180, 198, 255), (255, 255, 255), (0, 0, 0), "s1.png", "o1.png"),
            ("doc", (255, 255, 255), (17, 17, 17), (255, 255, 255), "s2.png", "o2.png"),
        ):
            arr = np.full((200, 320, 3), 255, dtype=np.uint8)
            arr[60:160, 80:240, :] = base       # 控件/卡片底色
            arr[100:140, 140:200, :] = orig     # 原字体色（大字号 ⇒ 笔画够实，读数不被抗锯齿主导）
            src, dst = Path(d) / out_src, Path(d) / out_dst
            _rgb_image(arr).save(src)
            rc = _render(src, dst, [{"text": "已同步", "box": box, "tran": "Synced"}])
            assert rc == 0, "%s 图渲染失败（rc=%d）" % (tag, rc)
            got = np.asarray(Image.open(str(dst)).convert("RGB"), dtype=np.float32)
            sub = got[60:160, 80:240]
            bg = IMG._pick_bg(_rgb_image(sub), (0, 0, sub.shape[1], sub.shape[0]))
            med = _ink_median(sub, bg)
            assert bg == base, "%s 的底色没被保住（实读 %r，期望 %r）" % (tag, bg, base)
            d_orig, d_alt = _dist(med, orig), _dist(med, alt)
            assert d_orig <= 60, "%s 档没沿用原字体色（落笔 %r，原字体色 %r）" % (tag, med, orig)
            assert d_alt >= d_orig + 150, \
                "%s 档落笔色（%r）离竞争色 %r（%.0f）不够远，说明没在用原字体色（离原色仅 %.0f）" \
                % (tag, med, alt, d_alt, d_orig)
        # 估计器侧的硬读数（渲染腿之外再钉一次"认出了什么色"）
        arr = np.full((200, 320, 3), 255, dtype=np.uint8)
        arr[60:160, 80:240, :] = (180, 198, 255)
        arr[100:140, 140:200, :] = (255, 255, 255)
        est = IMG._pick_fg(_rgb_image(arr), (180, 198, 255))
        assert est is not None and _dist(est, (255, 255, 255)) <= 40, \
            "字色估计器没认出徽章上的白字（实读 %r）" % (est,)


@leg("L38")
def leg_l38_font_color_uses_ink_core():
    """★ 2026-10-19 用户令：估字色取**墨迹核心**（按 |Δ亮度| 前 FG_CORE_PCT%），不是整块墨迹。

    现网的字都带抗锯齿/压缩过渡色：整块取中位会被过渡色拉向底色——白字估成浅灰、深红估成粉，
    「沿用原字体色」于是变成「沿用一个比原文淡一档的色」。两半：
      ① 本色 (200,0,0) 只占墨迹约 1/4、其余是过渡色 (235,170,170) ⇒ 估计必须≈本色；
      ② 夹具自证：同一块墨迹按**全部**像素取中位确实明显更淡——否则这条腿是空转的
         （"核心百分位"这一维根本没被测到）。
    """
    import numpy as np
    from PIL import Image
    arr = np.full((40, 120, 3), 255, dtype=np.uint8)
    arr[10:30, 20:60, :] = (235, 170, 170)     # 过渡色（抗锯齿外圈）：800 px
    arr[14:26, 70:94, :] = (200, 0, 0)         # 本色：288 px（约 26%）
    patch = Image.fromarray(arr, "RGB")
    fg = IMG._pick_fg(patch, (255, 255, 255))
    assert fg is not None, "白底深红字居然估不出字色"
    assert abs(fg[0] - 200) <= 12 and fg[1] <= 12 and fg[2] <= 12, \
        "字色没取墨迹核心（实读 %r，期望≈(200,0,0)；过渡色把估计拉亮了）" % (fg,)
    lum = IMG._luma_plane(np.asarray(patch.convert("RGB"), dtype=np.float32))
    ink = IMG._ink_mask(lum, 255.0)
    all_med = tuple(int(round(v)) for v in np.median(np.asarray(patch.convert("RGB"), dtype=np.float32)[ink], axis=0))
    assert all_med[0] - all_med[2] < fg[0] - fg[2] - 40, \
        "夹具自证不过：全部墨迹的中位色 %r 没明显比核心色 %r 淡 ⇒ 这条腿测不到「核心百分位」" % (all_med, fg)


@leg("L39")
def leg_l39_font_color_falls_back_and_is_wired():
    """★ 2026-10-19 用户令：估不准时**必须回落**底色反差档，且 `cmd_render` 真的用了 `_pick_fg`。

    边界三档（都要回 None，由 `_text_color` 兜底——取向是**可见性 > 配色还原**）：
      ① 无墨 / ② 墨太少（样本不足）/ ③ 底色取不到（不猜）。
    ★ 接线腿与 L18/L30 同一口径：函数写对而没被调用＝白修——这里做**源码级**断言
      （`cmd_render` 必须出现 `_pick_fg(`）；"估计出的色真被用上"由 L37 的渲染腿承担。
    """
    import inspect
    import numpy as np
    from PIL import Image
    blank = Image.fromarray(np.full((30, 80, 3), 255, dtype=np.uint8), "RGB")
    assert IMG._pick_fg(blank, (255, 255, 255)) is None, "无墨框不许估出字色"
    few = np.full((30, 80, 3), 255, dtype=np.uint8)
    few[10:13, 10:12, :] = 0                    # 6 px < FG_MIN_INK_PX：样本不足
    assert IMG._pick_fg(Image.fromarray(few, "RGB"), (255, 255, 255)) is None, \
        "墨太少时估出的色不可信，必须回落底色反差档"
    solid = np.full((30, 80, 3), 255, dtype=np.uint8)
    solid[10:22, 10:70, :] = 0
    assert IMG._pick_fg(Image.fromarray(solid, "RGB"), None) is None, "底色取不到时不许猜字色"
    assert "_pick_fg(" in inspect.getsource(IMG.cmd_render), \
        "字色估计函数写对却没被渲染调用＝白修（旧形态恒 `_text_color`）"


# ---------------------------------------------- 图片：同一区域字号归一（⭐ 2026-10-28 用户令）

@leg("L40")
def leg_l40_region_sizes_unify_to_mode():
    """★ 2026-10-28 用户令「同一区域的字号归一要做。按框/块/表格/行高相近来判断同一区域。」
    现网形态＝同一张表格的行里 11px 与 30px 并存（字号按每行自己的框独立算）。三半：
      ① 行高相近（±20%）**且**同表格行（y 区间重叠达标）的三行，字号 11/28/12 ⇒ 全部归到
         众数；三档各出一一次时**并列取较小**（用户令：宁可多排字，不许把邻行的字挤出去）；
      ② 众数真正"多数"的那一档必须被采纳（12/28/12 ⇒ 12，不是并列兜底）；
      ③ 接线腿（同 L18/L30/L39「函数写对却没人调用＝白修」口径）：
         `cmd_render` 必须把区域字号喂进 `_layout` 的 region_px 那一个调用点，
         且 `_layout` 收到装不下的 region_px 时**落回自己的档**（用户硬约束：归一不许把字裁掉，
         复用 `_block_ink_h` 旧尺判溢出，不许为归一造第二把尺子）。
    """
    import inspect
    rows = [(20, 10, 30, 11), (22, 12, 34, 28), (21, 11, 32, 12)]
    assert IMG._region_groups(rows) == [0, 0, 0], "行高相近+同表格行没并成一组：%r" % (IMG._region_groups(rows),)
    assert IMG._region_sizes(rows) == [11, 11, 11], \
        "同组没归到众数（并列应取较小=11），实读 %r" % (IMG._region_sizes(rows),)
    rows2 = [(20, 10, 30, 12), (21, 12, 32, 28), (20, 11, 33, 12)]
    assert IMG._region_sizes(rows2) == [12, 12, 12], "众数档（12 出两次）没被采纳：%r" % (IMG._region_sizes(rows2),)
    # ③ 接线：区域算出的字号必须真进 `_layout` 的 region_px 实参（锚点取 cmd_render 里那一行调用）。
    cr_src = inspect.getsource(IMG.cmd_render)
    assert "_region_sizes(" in cr_src, "分组函数写对却没被渲染调用＝白修（旧形态照旧逐行独立算）"
    assert 'p["inner_w"], p["inner_h"], p["floor"], p["ink_h"], p["region_px"])' in cr_src, \
        "绘制腿的 _layout 没把 region_px 传进去＝归一算完没人用（白修）"
    # _layout 侧语义：众数装得下 ⇒ 用它；装不下 ⇒ 落回自己的档（不许裁字/超框交出去）。
    px_fit = IMG._layout("Ok", 300, 40, 8, 10, 18)[0]
    assert px_fit == 18, "region_px 装得下却没采用（实读 %d）⇒ 归一腿没生效" % px_fit
    got_px, _, got_ovf = IMG._layout("Some longer translated text here", 300, 12, 8, 10, 18)
    assert got_px < 18 and got_ovf is False, \
        "region_px 装不下时没落回自己的档（px=%d overflow=%r）——为统一把字裁掉正是用户禁止的形态" % (got_px, got_ovf)
    assert "region_px >= floor" in inspect.getsource(IMG._layout), \
        "_layout 的归一腿必须仍以 floor 为下限（region_px 低于地板时不许硬用，否则缩成蚂蚁字）"


@leg("L41")
def leg_l41_region_criteria_keep_unrelated_rows():
    """★ 2026-10-28 用户硬约束「任一不满足就不许并成一组——把不相干的行并为同字号，
    比现在『大小不一』更坏」。两半（各断一条判据的射程）：
      ① 行高差 >20%（20 vs 30，差 50%）⇒ 即使 y 区间几乎全重叠（同一条水平带）也不许并组，
         字号 28/11 一个都不许被对方改掉；
      ② 行高相近但垂直不邻近（相隔 470px ≫ 较矮框的 30%）⇒ 同样不许并组；
      ③ 夹具自证：把上面两行按"该并组"的形态重排（行高相近+紧贴）必须**真的**并了组——
         否则①②可能只是分组函数整个失灵（红得没有归属）。
    """
    far = [(20, 10, 30, 28), (30, 11, 41, 11)]
    assert IMG._region_sizes(far) == [28, 11], \
        "行高差 50% 的两行被并成同字号了：%r（不相干的行钉同字号＝比大小不一更坏的回归）" % (IMG._region_sizes(far),)
    sep = [(20, 10, 30, 28), (20, 500, 520, 11)]
    assert IMG._region_sizes(sep) == [28, 11], "垂直远隔的两行被并成同字号了：%r" % (IMG._region_sizes(sep),)
    near = [(20, 10, 30, 28), (20, 31, 51, 11)]   # 行高相同、间隔 1px ≤ 30% ⇒ 必须并组
    assert IMG._region_sizes(near) == [11, 11], \
        "夹具自证不过：该并组的形态也没并（分组函数整体失灵，上面两条绿灯是空转）"


@leg("L42")
def leg_l42_sentinel_tier_not_grouped():
    """★ 2026-10-28 用户令「`-1` 那档不参与分组、也不从邻居借字号」。
    读码口径：现役 `_layout` 的返回值恒 ≥ MIN_FONT_PX（8），仓库里**没有**会产出 -1 字号的腿——
    `-1` 只可能来自哨兵/特殊档（或将来新增的档位），判据因此收在**分组入口**：
    字号 ≤0 的行直接回组号 -1（不并组、不进众数计数），且原样把 -1 还回去。
    ⚠️ 不许把 -1 塞进众数计数——{11,28,-1} 的"并列较小"会把整组钉成 -1（负字号进 _chain
    会被 max(MIN_FONT_PX) 悄悄抬成 8px 蚂蚁字，这是"读数为哨兵背书"的形态）。
    """
    rows = [(20, 10, 30, 11), (21, 12, 32, 28), (20, 11, 31, -1)]
    g = IMG._region_groups(rows)
    assert g[2] == -1, "-1 档混进了分组表（组号 %r）——用户明令它不参与分组" % (g,)
    assert g[0] == g[1] >= 0, "夹具自证不过：两个正常行没并组，下面那条断言测不到「-1 不进众数」"
    out = IMG._region_sizes(rows)
    assert out[2] == -1, "-1 档从邻居借了字号（实读 %d）——哨兵档必须原样保留" % out[2]
    assert out[0] == out[1] == 11, "同组正常行没归一（实读 %r）或 -1 被算进了众数" % (out,)


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
