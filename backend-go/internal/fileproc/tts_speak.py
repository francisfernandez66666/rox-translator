#!/usr/bin/env python3
"""tts_speak.py — 译文语音合成子命令壳（P0 基础件；P3 落地 voices/speak 本体）。

子命令：
  probe                      自检依赖与资产；stdout 打状态行 "OK: ..." / "MISSING: ..."（退出码 0 / 3）
  voices                     列出 assets/tts/voices/ 下可用参考音色（JSON 数组打 stdout）
  speak <text> [--lang xx] [--out file]   译文 → 音频文件（无 stdout；失败见 stderr）
                            产物容器由 --out 后缀决定：wav 原生落盘；mp3/ogg/opus/m4a/aac/flac 走
                            ffmpeg 转码（见 CONTAINER_ARGS，即"译后音频回到原文件格式"）；其余后缀
                            回 ERR:Encode，由调用方回落 .mp3。
  speakfile <lines.json> [--lang xx] [--out file]
                            **多段批量合成**（P2 工单音频产物用）。lines.json 是 JSON 字符串数组，
                            逐段合成后用 0.25s 静音缝成一段，只加载一次模型（加载才是耗时大头）。
                            为什么不用 argv 传多段：行译文可很长，命令行有长度上限，且 `ps` 会泄露客户内容。

依赖（**导入名**，与 fileproc/tts.go 的 ttsPythonModules 同源维护）：
  现役：sherpa_onnx / soundfile / lameenc / numpy（= 装机引擎真正 import 的全部）。
  待 Chatterbox 驱动落地时**加回**：onnxruntime / transformers / resemble_perth（PerTh 水印）/
  pkuseg（中文仓颉前端）/ pykakasi（日文假名前端）——D5 口径：真实依赖必须进探测，
  不许「装了一半还报绿」；反过来，尚未落地的引擎的依赖留在探测里同样是缺陷（会让就绪位恒 false）。

当前引擎：sherpa_onnx.OfflineTts 中英档（**MeloTTS zh_en**，44.1kHz 单女声，zh/en 同模型），
  经「配置对象」形态构造（`OfflineTtsConfig` + `OfflineTts(cfg)`，见 `_build_tts` 的核正说明）。
  覆盖语种＝`LANGS`，越界一律 ERR:UnsupportedLang。
★ 与已拍板主力方案的关系（务必如实理解，别照着注释改坏）：
  Chatterbox Multilingual V3（Resemble AI，MIT，23 语）是 2026-10-14 **用户拍板的主力**，
  但它需要自实现的 T3→S3Gen→声码器解码循环，不是 sherpa-onnx 能直接加载的形态
  （repetition_penalty 必须 =1.2，=2.0 会死循环，社区实测坑）。
  本壳因此留出 `_try_chatterbox()` 钩位：**缺了一律如实报 ERR:ModelLoad，绝不用别的模型冒充它**。
  届时以"新增一个分支"的方式接入，而不是替换本壳的运行逻辑（可插拔原则：加，不改）。

安全边界：纯本地推理、零网络调用；模型权重与参考音色只从脚本同目录 assets/tts/ 读，
禁止运行时联网下载（私有化机器可能无外网）。
"""
import importlib.util
import json
import os
import sys
from pathlib import Path

# 与 fileproc/tts.go 的 ttsPythonModules 同源维护（测试会核对两侧协议，不允许静默漂移）。
# ★ 2026-10-16 校正：这里列的是**当前装机引擎真正要 import 的东西**。
#   现役引擎＝sherpa-onnx 中英档（见下方 LANGS），它只需要 sherpa_onnx + soundfile + lameenc + numpy。
#   旧清单（onnxruntime / transformers / resemble_perth / pkuseg / pykakasi）是**尚未落地的
#   Chatterbox 驱动**的依赖：把它们留在探测里会让 `tts_ready` 恒 false——
#   而 pkuseg 这个 2018 年的包在 Python 3.12 上连 wheel 都构建不出来（本批实测），
#   等价于"永远点不亮"。Chatterbox 驱动落地时必须**同时**把那份清单加回本处与 Go 侧
#   （同源锁会拦只改一侧），届时 PerTh 水印（D11）随之生效——现役中英档无水印，见 assets/tts/README.md。
MODULES = [
    "sherpa_onnx",
    "soundfile",
    "lameenc",
    "numpy",
]

# 覆盖语种（与 fileproc/tts.go 的 ttsCoveredLangs 同源维护，两侧逐项等值由 Go 单测锁住）。
# ★ 现役引擎是 sherpa-onnx 中英档（MeloTTS zh_en，2026-10-14 方案 §3.3「zh/en → 轻量档」那一条，
#   2026-10-16 用户拍板「先上中英档」）；Chatterbox 驱动落地后这里扩到它覆盖的 11 个语种。
# 非本清单语种一律回 ERR:UnsupportedLang（**不许**拿中英模型去念日/韩/俄文：
# 那不会报错，只会吐出一段听不懂的音频——比"没有语音"更坏）。
LANGS = ["zh", "en"]

ASSETS_DIR = Path(__file__).resolve().parent / "assets" / "tts"
VOICES_DIR = ASSETS_DIR / "voices"

# 出 此处 的默认抽样率；具体值以模型为准，这里只作为"没拿到 config"时的兜底。
DEFAULT_SAMPLE_RATE = 24000


def cmd_probe() -> int:
    missing = [m for m in MODULES if importlib.util.find_spec(m) is None]
    weights = sorted(ASSETS_DIR.glob("*.onnx"))
    voices = sorted(VOICES_DIR.glob("*.wav")) if VOICES_DIR.is_dir() else []
    detail = []
    if missing:
        detail.append("modules=" + ",".join(missing))
    if not weights:
        detail.append("assets/tts/*.onnx 缺失（取权重步骤见 assets/tts/README.md）")
    if not voices:
        detail.append("assets/tts/voices/*.wav 缺失（默认音色投放步骤见 assets/tts/voices/README.md）")
    if detail:
        print("MISSING: " + "; ".join(detail))
        return 3
    print("OK: tts 依赖齐备（%d 个 ONNX 权重，%d 个参考音色）" % (len(weights), len(voices)))
    return 0


def cmd_voices() -> int:
    """列出可用参考音色。没有任何音色时返回空数组（不报错——list 类接口缺值不该是故障）。"""
    voices = []
    if VOICES_DIR.is_dir():
        for p in sorted(VOICES_DIR.glob("*.wav")):
            voices.append({"id": p.stem, "file": str(p), "bytes": p.stat().st_size})
    print(json.dumps(voices, ensure_ascii=False))
    return 0


# ------------------------------------------------------------ 引擎构造

def norm_lang(lang: str) -> str:
    """归一化语种码：小写、剥掉地域段（`zh-hant` / `zh_Hant` → `zh`）。

    ★ 与 Go 侧 `ttsLangCovered` 是**同一把尺子**（前端 `normTtsLang` 亦然）：
      两侧各判一次必然漂（历史形态＝前端按 `zh_hant`、后端按 `zh-hant`，边界判定永远差一档）。
      归一化只做这一件事，多一步都不做（别顺手把 `th` 之类也"映射"成别的码）。
    """
    s = (lang or "").strip().lower().replace("_", "-")
    return s.split("-")[0]


def lang_covered(lang: str) -> bool:
    """这个目标语种有没有语音。

    ★ 空串放行：调用方没送 `--lang`（老客户端、脚本直调）时按默认音色试，
      拒绝它等于把「没送参数」误判成「这个语种不支持」——同 §一·10「新增防护不拿可用性交押金」。
    """
    lc = norm_lang(lang)
    return (not lc) or (lc in LANGS)


def _pick_voice(explicit: str | None) -> Path:
    """挑一个参考音色：显式指定 > default_reference.wav > voices/ 下任意一个。"""
    if explicit:
        p = VOICES_DIR / explicit
        if not p.exists():
            raise RuntimeError("ERR:Voice:指定音色不存在 %s" % explicit)
        return p
    default = VOICES_DIR / "default_reference.wav"
    if default.exists() and default.stat().st_size > 0:
        return default
    if VOICES_DIR.is_dir():
        for p in sorted(VOICES_DIR.glob("*.wav")):
            if p.stat().st_size > 0:
                return p
    raise RuntimeError("ERR:Voice:assets/tts/voices/ 下没有可用的参考音频")


def _try_chatterbox(_text: str, _lang: str, _voice: Path):
    """主力引擎钩位（尚未落地，见文件头说明）。

    返回 None 表示"本壳不支持，退通用加载器"；落地后返回 (samples, sample_rate)。
    ★ 签名刻意**返回采样而不是写文件**：`speak` 与 `speakfile` 两条路都要用它，
      写文件的形态下批量路要么给每段起临时文件再读回，要么悄悄绕过主力引擎——
      前者难看，后者是"主力上线了批量路却不用它"的静默分歧。
    """
    return None


# 权重文件的体量下限：低于它的 *.onnx 一律不当模型看。
# ★ 为什么需要这条：HF 仓里常带 `model.int8.onnx` 这种 **git-lfs 指针文件**（133 字节纯文本，
#   内容是 `version https://git-lfs.github.com/spec/v1`），旧的"取第一个 .onnx"实现会挑中它，
#   于是构造器抛一个语焉不详的异常 —— 与下面那条 API 形状坑叠在一起时，根因完全指不出来。
MIN_WEIGHT_BYTES = 1_000_000


def _first_asset(model_dir: Path, names: "tuple[str, ...]", min_bytes: int = MIN_WEIGHT_BYTES) -> "Path | None":
    """按名字优先级取第一个存在且体量合理的文件；都不中再退"目录里任意够大的 *.onnx"。"""
    for n in names:
        q = model_dir / n
        if q.exists() and q.stat().st_size >= min_bytes:
            return q
    for p in sorted(model_dir.glob("*.onnx")):
        if p.stat().st_size >= min_bytes:
            return p
    return None


def _find_dir(model_dir: Path, names: "tuple[str, ...]") -> "Path | None":
    """取第一个存在的子目录（data_dir / dict_dir 这类按目录传的资产）。"""
    for n in names:
        q = model_dir / n
        if q.is_dir():
            return q
    return None


def _build_tts(model_dir: Path):
    """构造 sherpa-onnx OfflineTts。按资产形状试探三种形态，都不中即 ERR:Asset。

    ★ 2026-10-16 核正（本批真踩）：现网装的 **sherpa-onnx 1.13.8** 的 Python API 是
      「配置对象」形态——`cfg = sherpa_onnx.OfflineTtsConfig()` 填好 `cfg.model.vits.*`
      / `.kokoro.*` / `.matcha.*` 之后 `sherpa_onnx.OfflineTts(cfg)`；
      **根本没有** `OfflineTts.from_vits / from_kokoro / from_matcha` 这些类方法。
      旧实现照一套不存在的类方法写 ⇒ 探测（只看依赖与文件在不在）全绿、`tts_ready=true`，
      而**每一次合成**都抛 AttributeError 被收成 ERR —— 与 `asr_transcribe.py` 那条
      「1.13.8 里没有 `read_wave`」是同族缺陷（形状错，源码 grep 得到字样也判不出对错）。
      故这里一律走配置对象，并由 `tts_lang_coverage_gate_test.go` 机械钉住"不许再出现 from_ 调用"。
    """
    import sherpa_onnx

    tokens = _first_asset(model_dir, ("tokens.txt", "voices.txt"), 1)
    lexicon = _first_asset(model_dir, ("lexicon.txt", "lexicon-us-en.txt", "lexicon-zh.txt"), 1)
    data_dir = _find_dir(model_dir, ("espeak-ng-data", "data"))
    dict_dir = _find_dir(model_dir, ("dict",))
    model = _first_asset(model_dir, ("model.onnx", "kokoro.onnx", "melotts.onnx", "vits.onnx"))
    acoustic = _first_asset(model_dir, ("acoustic.onnx", "matcha.onnx"))
    vocoder = _first_asset(model_dir, ("vocoder.onnx", "hifigan.onnx", "vocos.onnx"))

    def base_cfg():
        cfg = sherpa_onnx.OfflineTtsConfig()
        cfg.model.num_threads = os.cpu_count() or 2
        return cfg

    attempts = []
    if model is not None and tokens is not None:
        # ① VITS 系（MeloTTS zh_en 即此形态：model.onnx + tokens.txt + lexicon.txt + dict/）
        def _vits():
            cfg = base_cfg()
            cfg.model.vits.model = str(model)
            cfg.model.vits.tokens = str(tokens)
            if lexicon is not None:
                cfg.model.vits.lexicon = str(lexicon)
            if data_dir is not None:
                cfg.model.vits.data_dir = str(data_dir)
            if dict_dir is not None:
                cfg.model.vits.dict_dir = str(dict_dir)
            return cfg
        attempts.append(("vits", _vits))

        # ② Kokoro（多语、zh/en 混说；只有它带 lexicon 时才有意义）
        if lexicon is not None:
            def _kokoro():
                cfg = base_cfg()
                cfg.model.kokoro.model = str(model)
                cfg.model.kokoro.tokens = str(tokens)
                cfg.model.kokoro.lexicon = str(lexicon)
                if data_dir is not None:
                    cfg.model.kokoro.data_dir = str(data_dir)
                if dict_dir is not None:
                    cfg.model.kokoro.dict_dir = str(dict_dir)
                return cfg
            attempts.append(("kokoro", _kokoro))

    # ③ Matcha：acoustic + vocoder 两件套
    if acoustic is not None and vocoder is not None and tokens is not None:
        def _matcha():
            cfg = base_cfg()
            cfg.model.matcha.acoustic_model = str(acoustic)
            cfg.model.matcha.vocoder = str(vocoder)
            cfg.model.matcha.tokens = str(tokens)
            if lexicon is not None:
                cfg.model.matcha.lexicon = str(lexicon)
            if data_dir is not None:
                cfg.model.matcha.data_dir = str(data_dir)
            if dict_dir is not None:
                cfg.model.matcha.dict_dir = str(dict_dir)
            return cfg
        attempts.append(("matcha", _matcha))

    if not attempts:
        raise RuntimeError(
            "ERR:Asset:无法从 assets/tts/ 的权重与 tokens/lexicon 推断模型形态"
            "（需要 model.onnx + tokens.txt + lexicon.txt，或 acoustic+vocoder 两件套）"
        )

    last_exc = None
    for kind, build in attempts:
        try:
            return sherpa_onnx.OfflineTts(build()), kind
        except Exception as exc:  # 形状不匹配就试下一种；全失败才报 ModelLoad
            last_exc = exc
    raise RuntimeError("ERR:ModelLoad:合成器构造失败（已试 %s）%s" % (",".join(k for k, _ in attempts), last_exc))


# ------------------------------------------------------------ 合成本体

# 目标容器 → ffmpeg 编码参数表。口径＝「译后音频回到**原文件格式**」（用户令 2026-10-16）：
# 输入 mp3 就得回 mp3、输入 m4a 就回 m4a，用户拿回的东西必须能直接替换原件。
# wav 是引擎原生输出（无需转码），单独处理；表里没有的容器 ⇒ ERR:Encode，
# 由调用方（engine/file.go）回落 .mp3 并在 GateWarnings 明示——配音是增值产物，绝不判死整单。
CONTAINER_ARGS = {
    ".mp3": ["-codec:a", "libmp3lame", "-q:a", "4"],
    ".ogg": ["-codec:a", "libvorbis", "-q:a", "5"],
    ".opus": ["-codec:a", "libopus", "-b:a", "48k"],
    ".m4a": ["-codec:a", "aac", "-b:a", "96k"],
    ".aac": ["-codec:a", "aac", "-b:a", "96k"],
    ".flac": ["-codec:a", "flac"],
}


def _ffmpeg_encode(wav_path: Path, out: Path, args: list) -> bool:
    """ffmpeg 转码一次；成功（退出码 0 且产物非空）返回 True。缺 ffmpeg 或失败返回 False（不抛）。"""
    import subprocess

    cmd = ["ffmpeg", "-nostdin", "-y", "-i", str(wav_path), *args, str(out)]
    try:
        proc = subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except Exception:
        return False
    return proc.returncode == 0 and out.exists() and out.stat().st_size > 0


def _encode_out(wav_path: Path, out: Path) -> None:
    """把中间 wav 编码成 --out 指定的容器。转不动 ⇒ RuntimeError(ERR:Encode:<原因>)。"""
    suffix = out.suffix.lower()
    if suffix == ".mp3":
        # D4：mp3 兼容优先——ffmpeg 先行，没有 ffmpeg 时用 pure-python 的 lameenc 直压 PCM。
        if wav_path.resolve() != out.resolve() and _ffmpeg_encode(wav_path, out, CONTAINER_ARGS[".mp3"]):
            return
        try:
            import lameenc
            import soundfile as sf

            pcm, sr = sf.read(str(wav_path), dtype="int16")
            enc = lameenc.Encoder()
            enc.set_bit_rate(96)
            enc.set_in_sample_rate(sr)
            enc.set_channels(1)
            enc.set_quality(2)
            mp3 = enc.encode(pcm.tobytes())
            mp3 += enc.flush()
            out.write_bytes(mp3)
            if out.stat().st_size > 0:
                return
        except Exception as exc:
            raise RuntimeError("ERR:Encode:mp3 编码失败 %s（已生成 wav：%s）" % (exc, wav_path))
        raise RuntimeError("ERR:Encode:mp3 编码产物为空（已生成 wav：%s）" % wav_path)
    args = CONTAINER_ARGS.get(suffix)
    if not args:
        raise RuntimeError(
            "ERR:Encode:不支持的产物容器 %s（可选 %s 或 wav）" % (suffix or "(无后缀)", ",".join(sorted(CONTAINER_ARGS)))
        )
    if _ffmpeg_encode(wav_path, out, args):
        return
    raise RuntimeError("ERR:Encode:%s 编码失败（需 ffmpeg 且其带该容器编码器；已生成 wav：%s）" % (suffix, wav_path))


def _write_wav(samples, sample_rate: int, out: Path) -> None:
    """合成结果落盘到 --out：.wav 直接写；其余容器先写临时 wav、编码成功后再删中间件。
    ★ 中间 wav 用 **临时名**（`<out>.tmp.wav`）而不是 `out.with_suffix('.wav')`——
    否则每次请求都会在产物目录里留一个与产物同名的孤儿 wav（旧形态实测遗留 zh.wav 30 万字节）。"""
    import numpy as np
    import soundfile as sf

    arr = np.asarray(samples, dtype="float32")
    if arr.ndim > 1:
        arr = arr.mean(axis=1)
    peak = float(np.max(np.abs(arr))) if arr.size else 0.0
    # 归一化到 0.89 满量程：留一点 headroom 给有损编码器的 overshoot（削顶失真很难听）。
    if peak > 0.89:
        arr = arr * (0.89 / peak)
    if out.suffix.lower() == ".wav":
        sf.write(str(out), arr, sample_rate, subtype="PCM_16")
        return
    tmp = out.with_name(out.name + ".tmp.wav")
    sf.write(str(tmp), arr, sample_rate, subtype="PCM_16")
    try:
        _encode_out(tmp, out)
    finally:
        try:
            tmp.unlink()
        except OSError:
            pass


def _synth_one(tts, text: str):
    """合成单段 → (samples, sample_rate)。失败抛 RuntimeError(ERR:<Variant>)。"""
    try:
        audio = tts.generate(text, sid=0, speed=1.0)
    except Exception as exc:
        raise RuntimeError("ERR:ModelLoad:合成失败 %s" % exc)
    if audio is None or len(audio.samples) == 0:
        raise RuntimeError("ERR:Empty:模型未产出有效采样")
    return audio.samples, audio.sample_rate


def cmd_speak(text: str, lang: str, out_path: str, voice_arg: str | None) -> int:
    if not text.strip():
        print("ERR:Empty:文本为空", file=sys.stderr)
        return 2
    if not ASSETS_DIR.is_dir() or not sorted(ASSETS_DIR.glob("*.onnx")):
        print("ERR:Asset:assets/tts/*.onnx 缺失", file=sys.stderr)
        return 2
    # ★ 语种边界先于任何加载动作：拿中英模型去念别的语言**不会报错**，只会吐一段听不懂的音频
    #   （客户以为"翻译坏了"）——所以宁可在这里明确拒，并让 Go 侧映射成 unsupported_lang 档，
    #   前端按 12 语种词典说一句「该语种暂不支持语音播放，译文照常」。
    if not lang_covered(lang):
        print("ERR:UnsupportedLang:语种 %s 超出当前语音模型覆盖范围（%s）" % (lang, ",".join(LANGS)), file=sys.stderr)
        return 2

    voice = _pick_voice(voice_arg)
    out = Path(out_path)

    # 主力钩位：Chatterbox 到位后在这里接管，产出成功即返回
    try:
        got = _try_chatterbox(text, lang, voice)
        if got is not None:
            samples, sr = got
            _write_wav(samples, sr, out)
            return 0
    except RuntimeError as exc:
        msg = str(exc)
        if msg.startswith("ERR:"):
            print(msg, file=sys.stderr)
            return 2

    import sherpa_onnx  # noqa: F401  导入失败由下面的通用异常处理包住

    tts, kind = _build_tts(ASSETS_DIR)
    try:
        samples, sr = _synth_one(tts, text)
    except RuntimeError as exc:
        print(str(exc), file=sys.stderr)
        return 2
    _write_wav(samples, sr, out)
    return 0


# 批量缝合时两段之间的静音时长（秒）。给 0 而不是留白：相邻字幕的译文会首尾粘成一个词，
# 听起来像模型漏了半句。0.25s 是"听得出一段结束"的最小值，再长整篇节奏会拖。
BATCH_GAP_SEC = 0.25


def cmd_speakfile(lines_path: str, lang: str, out_path: str, voice_arg: str | None) -> int:
    """多段合成 → 缝合 → 单一音频文件（P2 工单音频的译文配音产物）。

    ★ 择优产出：某一段合成失败只跳过那一段（stderr 记一行），其余段照常缝。
      全段都失败才判错——与"这一整片没有语音"是同一件事。
    """
    if not ASSETS_DIR.is_dir() or not sorted(ASSETS_DIR.glob("*.onnx")):
        print("ERR:Asset:assets/tts/*.onnx 缺失", file=sys.stderr)
        return 2
    # 语种门与单段路同源同档（见 cmd_speak 那段说明）：批量路也要在加载模型之前先判边界。
    if not lang_covered(lang):
        print("ERR:UnsupportedLang:语种 %s 超出当前语音模型覆盖范围（%s）" % (lang, ",".join(LANGS)), file=sys.stderr)
        return 2
    try:
        raw = json.loads(Path(lines_path).read_text(encoding="utf-8"))
    except Exception as exc:
        print("ERR:Usage:lines.json 读取或解析失败 %s" % exc, file=sys.stderr)
        return 2
    if not isinstance(raw, list) or not raw:
        print("ERR:Empty:lines.json 不是非空数组", file=sys.stderr)
        return 2
    lines = [str(x) for x in raw if str(x).strip()]
    if not lines:
        print("ERR:Empty:lines.json 里没有非空行", file=sys.stderr)
        return 2

    voice = _pick_voice(voice_arg)
    out = Path(out_path)

    import numpy as np
    import sherpa_onnx  # noqa: F401  导入失败由下面的通用异常处理包住

    tts, kind = _build_tts(ASSETS_DIR)
    chunks: list = []
    sample_rate = 0
    skipped = 0
    for idx, line in enumerate(lines):
        try:
            samples, sr = _synth_one(tts, line)
        except RuntimeError as exc:
            skipped += 1
            print("[speakfile] 第 %d 段跳过：%s" % (idx + 1, exc), file=sys.stderr)
            continue
        if sample_rate == 0:
            sample_rate = sr
        elif sr != sample_rate:
            # 同一模型不会变采样率；真变了就重采样对齐，不让缝合结果变调。
            samples = np.asarray(samples, dtype="float32")
            n = int(round(len(samples) * sample_rate / sr))
            if n > 0 and len(samples) > 1:
                samples = np.interp(
                    np.linspace(0, len(samples) - 1, n),
                    np.arange(len(samples)),
                    samples,
                ).astype("float32")
        if chunks:
            chunks.append(np.zeros(int(sample_rate * BATCH_GAP_SEC), dtype="float32"))
        chunks.append(np.asarray(samples, dtype="float32"))

    if not chunks:
        print("ERR:Empty:所有段都合成失败", file=sys.stderr)
        return 2
    if skipped:
        print("[speakfile] 共跳过 %d/%d 段（译文文本照常在 .md/.srt 里）" % (skipped, len(lines)), file=sys.stderr)
    _write_wav(np.concatenate(chunks), sample_rate, out)
    return 0


def main() -> int:
    argv = sys.argv[1:]
    if not argv:
        print("ERR:Usage:probe|voices|speak <text>|speakfile <lines.json> [--lang xx] [--out file] [--voice id.wav]", file=sys.stderr)
        return 2
    cmd = argv[0]
    rest = argv[1:]

    if cmd == "probe":
        return cmd_probe()
    if cmd == "voices":
        return cmd_voices()
    if cmd in ("speak", "speakfile"):
        if not rest:
            print("ERR:Usage:%s <%s> [--lang xx] [--out file]" % (cmd, "text" if cmd == "speak" else "lines.json"), file=sys.stderr)
            return 2
        text = rest[0]
        lang, out_path, voice = "", "", None
        i = 1
        while i < len(rest):
            if rest[i] == "--lang" and i + 1 < len(rest):
                lang = rest[i + 1]
                i += 2
            elif rest[i] == "--out" and i + 1 < len(rest):
                out_path = rest[i + 1]
                i += 2
            elif rest[i] == "--voice" and i + 1 < len(rest):
                voice = rest[i + 1]
                i += 2
            else:
                i += 1
        if not out_path:
            print("ERR:Usage:%s 必须带 --out <file>" % cmd, file=sys.stderr)
            return 2
        try:
            if cmd == "speak":
                return cmd_speak(text, lang, out_path, voice)
            return cmd_speakfile(text, lang, out_path, voice)
        except RuntimeError as exc:
            msg = str(exc)
            if not msg.startswith("ERR:"):
                msg = "ERR:ModelLoad:" + msg
            print(msg, file=sys.stderr)
            return 2
        except Exception as exc:
            print("ERR:ModelLoad:%s" % exc, file=sys.stderr)
            return 2
    print("ERR:Usage:probe|voices|speak <text>|speakfile <lines.json> [--lang xx] [--out file]", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main())
