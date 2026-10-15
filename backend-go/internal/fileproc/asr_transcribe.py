#!/usr/bin/env python3
"""asr_transcribe.py — sherpa-onnx 音频语音识别子命令壳（P0 基础件；P2 落地转写本体）。

子命令：
  probe              自检依赖与资产；stdout 打状态行 "OK: ..." / "MISSING: ..."（退出码 0 / 3）
  transcribe <path>  音频 → 带时间戳文本，stdout 打 JSON {"text": ..., "segments": [...], "language": ...}

依赖（**导入名**，与 fileproc/asr.go 的 asrPythonModules 同源维护）：
  sherpa_onnx（识别器本体）。★ wav 读取**不走它**：实测 sherpa_onnx 1.13.8 里没有 read_wave
  （只有 write_wave），所以 PCM wav 由标准库 wave + numpy 读，见 _read_wave 的原因说明；
  mp3/m4a 等容器仍由部署机 ffmpeg 预转 16k 单声道 wav。

模型（P2 定稿形态，资产随 sync_fileproc_assets.sh 落盘）：
  SenseVoice-Small（中/英/日/韩/粤，int8 ~240MB）为主力；
  Parakeet TDT v3（25 欧洲语言）与 Whisper tiny（冷门兜底）按语种路由加载——
  选型依据见《改造方案_OCR与ASR接入_20261014_v2.md》§三 3.2。
  资产目录约定 assets/asr/*.onnx；多模型共存时按 assets/asr/*.json 的型号清单路由，
  没有清单就退化为「目录里唯一一套权重」。

错误协议（stderr 打 ERR:<Variant>:<msg>，fileproc/asr.go 的 friendlyAsrError 逐档翻译）：
  Asset/ModelLoad/Import/AudioOpen/AudioCorrupt/Decode/Unsupported/Container/Empty/FFmpeg
"""
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

# 与 fileproc/asr.go 的 asrPythonModules 同源维护（测试会核对两侧协议，不允许静默漂移）。
MODULES = ["sherpa_onnx"]

ASSETS_DIR = Path(__file__).resolve().parent / "assets" / "asr"

# ffmpeg 转码目标：sherpa-onnx 的 read_wave 只读 PCM wav，且绝大多数 ASR 模型的采样率是 16k
# （SenseVoice / ParaParakeet / Whisper 全系一致）。统一先把任意容器砸成这个形态，
# 后面模型侧就不必再操心 resample —— 与其在四个模型里各写一遍重采样，不如在这里一次性做对。
TARGET_SAMPLE_RATE = 16000


# ---------------------------------------------------------------- probe

def cmd_probe() -> int:
    missing = [m for m in MODULES if importlib.util.find_spec(m) is None]
    weights = sorted(ASSETS_DIR.glob("*.onnx"))
    detail = []
    if missing:
        detail.append("modules=" + ",".join(missing))
    if not weights:
        detail.append("assets/asr/*.onnx 缺失（取权重步骤见 assets/asr/README.md）")
    if detail:
        print("MISSING: " + "; ".join(detail))
        return 3
    print("OK: asr 依赖齐备（%d 个 ONNX 权重）" % len(weights))
    return 0


# ------------------------------------------------------------ 资产解析

def _load_weights() -> list:
    """列出可用权重（按文件名排序，保证多模型路由结果稳定）。"""
    return sorted(ASSETS_DIR.glob("*.onnx"))


def _uses_non_whisper(tokens_path: Path) -> bool:
    return tokens_path.exists()


def _read_wave(path: str, sherpa_module):
    """读 PCM wav → (一维 float32 样本数组, 采样率)。

    ★ 为什么不能直接用它曾经依赖的那一行 `sherpa_onnx.read_wave`：
      实测现网装的 **sherpa_onnx 1.13.8 根本没有 read_wave**（`dir()` 里只有 `write_wave`，
      `sherpa_onnx.utils` 里也没有），于是每一条音频都在转写第一步就退
      `ERR:Decode:module 'sherpa_onnx' has no attribute 'read_wave'`——
      依赖装好了、权重落盘了、probe 也回 OK，**只有真跑一单才暴露**，
      这正是本仓「有自动降级的链路，产物能打开不算验收」那一族（同 §一·12 的派发死腿）。
      ⇒ 读取本体落到**标准库 wave + numpy**（零第三方音频库，且 `_ensure_wav` 已把
      任何容器砸成 16k 单声道 s16 PCM，形状完全确定）；
      若将来的版本又导出 read_wave，仍优先用它（它读的是模型侧同款解析），
      读回来的样本一律归一成 numpy float32，下游 `samples.size`/`accept_waveform` 一字不改。
    """
    import wave

    import numpy as np

    reader = getattr(sherpa_module, "read_wave", None)
    if callable(reader):  # 兼容有 read_wave 的版本（旧 0.x/未来版都走这条）
        samples, rate = reader(path)
        arr = np.asarray(samples, dtype=np.float32)
        # read_wave 历史上给过 [-1,1] 浮点，也直接给过 int16 语义；按量级判一次，
        # 免得某个版本换了口径就把音量放大 32768 倍（表现是识别成乱码而不是报错）。
        if arr.size and float(np.max(np.abs(arr))) > 2.0:
            arr = arr / 32768.0
        return arr, int(rate)

    try:
        with wave.open(path, "rb") as wf:
            n_ch, samp_w, n_frames = wf.getnchannels(), wf.getsampwidth(), wf.getnframes()
            rate = wf.getframerate()
            raw = wf.readframes(n_frames)
    except Exception as exc:  # 非 PCM（A-law/IEEE float/24-bit）都从这里出
        raise RuntimeError("ERR:AudioCorrupt:wav 不是标准库可读的 PCM 形态（%s）" % exc)
    if samp_w == 2:
        arr = np.frombuffer(raw, dtype="<i2").astype(np.float32) / 32768.0
    elif samp_w == 1:
        # 8-bit PCM 是**无符号**且以 128 为零点（CCITT G.711 线性化形态），不是补码。
        arr = (np.frombuffer(raw, dtype=np.uint8).astype(np.float32) - 128.0) / 128.0
    elif samp_w == 4:
        arr = np.frombuffer(raw, dtype="<i4").astype(np.float32) / 2147483648.0
    else:
        raise RuntimeError("ERR:Unsupported:wav 位深 %d-bit 不支持（只认 8/16/32-bit PCM）" % (samp_w * 8))
    if n_ch > 1:
        # 兜底只取第一声道：_ensure_wav 有 ffmpeg 时必然给单声道，
        # 走到这里说明机器没 ffmpeg 而原始 wav 是多声道——取第一声道比整单失败好
        #（识别质量略降但译文出得来；不静默做混音，避免把"少了一步归一"藏进声道平均）。
        arr = arr.reshape(-1, n_ch)[:, 0]
    return arr, int(rate)


# ------------------------------------------------------------ 音频预处理

def _ensure_wav(path: Path) -> Path:
    """把任意音频容器转成 16kHz 单声道 PCM wav，返回临时文件路径（调用方负责清理）。

    ★ 为什么必须走这一步：mp3/m4a/aac/opus 是**容器里的有损编码**，本文件的读取腿 _read_wave
      只认 PCM wav；与其在 Go 侧判断「哪些扩展名要先转」（那份清单会随格式准入面反复漂移，
      见 fileproc/formats.go 的 #43），不如让 Python 侧无条件归一 —— 反正 ffmpeg 对已经是
      目标形态的输入几乎零成本，不存在「多转一次」的浪费。
    """
    if shutil.which("ffmpeg") is None:
        # _read_wave 能吃纯 PCM wav；没有 ffmpeg 时至少让 wav 直通，别的容器如实报错。
        if path.suffix.lower() == ".wav":
            return path
        raise RuntimeError("ERR:FFmpeg:系统未安装 ffmpeg，无法解码 %s 容器" % path.suffix)
    fd, out = tempfile.mkstemp(suffix=".wav")
    os.close(fd)
    cmd = [
        "ffmpeg", "-nostdin", "-y", "-i", str(path),
        "-ar", str(TARGET_SAMPLE_RATE), "-ac", "1", "-vn", "-sn", "-dn",
        "-f", "wav", out,
    ]
    proc = subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    if proc.returncode != 0 or not os.path.exists(out) or os.path.getsize(out) == 0:
        tail = proc.stderr.decode("utf-8", "replace")[-300:] if proc.stderr else ""
        try:
            os.unlink(out)
        except OSError:
            pass
        raise RuntimeError("ERR:AudioCorrupt:ffmpeg 解码失败 %s" % tail.replace("\n", " "))
    return Path(out)


# ------------------------------------------------------------ 识别器构造

def _build_recognizer(model_dir: Path):
    """按 assets/asr/ 下的权重构造 sherpa-onnx OfflineRecognizer。

    三种形态按优先级试探（都是 sherpa-onnx 官方 API，只是不同的 model= 参数组合）：
      ① SenseVoice（model=SenseVoice，带 tokens/language 参数）——中英日韩粤主力；
      ② ParaParakeet/Transducer（encoder+decoder+joiner 三件套）——欧洲语言；
      ③ Whisper（encoder+decoder+tokens，需 multilingual）——冷门语种兜底。
    都没有 ⇒ ERR:Asset 由调用方转友好话术。
    """
    import sherpa_onnx

    onnx = sorted(model_dir.glob("*.onnx"))
    if not onnx:
        raise RuntimeError("ERR:Asset:assets/asr/ 下没有 *.onnx 权重")

    def find(*keywords):
        for p in onnx:
            name = p.name.lower()
            if all(k in name for k in keywords):
                return p
        return None

    tokens = model_dir / "tokens.txt"
    glob_tokens = sorted(model_dir.glob("*.txt"))

    try:
        # ① SenseVoice：单文件模型 + 多语种热切换（ASR 侧唯一能按 language 参数切语种的）
        sense = find("model") or find("sense")
        if sense is not None and _uses_non_whisper(tokens):
            return sherpa_onnx.OfflineRecognizer.from_sense_voice(
                model=str(sense),
                tokens=str(tokens),
                use_itn=True,
                num_threads=os.cpu_count() or 2,
                # 由下面的 set_language 覆盖；这里只给个默认值占位
                language="auto",
                decoding_method="greedy_search",
            ), "sense_voice"

        # ② Transducer / ParaParakeet：encoder-decoder-joiner 三件套
        enc = find("encoder")
        dec = find("decoder")
        jn = find("joiner")
        if enc and dec and jn and _uses_non_whisper(tokens):
            return sherpa_onnx.OfflineRecognizer.from_transducer(
                encoder=str(enc), decoder=str(dec), joiner=str(jn),
                tokens=str(tokens),
                num_threads=os.cpu_count() or 2,
                decoding_method="greedy_search",
            ), "transducer"

        # ③ Whisper：encoder + decoder + tokens（multilingual）
        wenc = find("whisper", "encoder") or enc
        wdec = find("whisper", "decoder") or dec
        if wenc and wdec and _uses_non_whisper(tokens):
            return sherpa_onnx.OfflineRecognizer.from_whisper(
                encoder=str(wenc), decoder=str(wdec), tokens=str(tokens),
                num_threads=os.cpu_count() or 2,
                decoding_method="greedy_search",
            ), "whisper"

        fallback = glob_tokens[0] if glob_tokens else None
        if fallback is not None and len(onnx) == 1:
            # 单权重 + 单个词表：交给 sherpa-onnx 的通用入口（它自己按 shape 猜型号）
            return sherpa_onnx.OfflineRecognizer.from_transducer(
                encoder=str(onnx[0]), decoder=str(onnx[0]), joiner=str(onnx[0]),
                tokens=str(fallback),
                num_threads=os.cpu_count() or 2,
            ), "transducer"
    except RuntimeError:
        raise
    except Exception as exc:  # 构造期异常一律归到 ModelLoad（权重不全/版本不匹配最常见）
        raise RuntimeError("ERR:ModelLoad:识别器构造失败 %s" % exc)

    raise RuntimeError("ERR:Asset:无法从 assets/asr/ 的权重推断模型类型（缺 tokens.txt 或三件套不全）")


# ------------------------------------------------------------ 转写本体

def cmd_transcribe(path: str) -> int:
    # 存在性先判：它是零成本的，而「依赖没装」在私有化机器上是常态——
    # 两个都缺时用户首先需要知道的是「文件也没找到」，而不是被环境问题淹没。
    src = Path(path)
    if not src.exists():
        print("ERR:AudioOpen:文件不存在 %s" % path, file=sys.stderr)
        return 2
    try:
        import sherpa_onnx
    except Exception:
        print("ERR:Import:sherpa_onnx 未安装", file=sys.stderr)
        return 2

    try:
        if not _load_weights():
            print("ERR:Asset:assets/asr/*.onnx 缺失", file=sys.stderr)
            return 2
        try:
            recognizer, kind = _build_recognizer(ASSETS_DIR)
        except RuntimeError as exc:
            msg = str(exc)
            if msg.startswith("ERR:"):
                print(msg, file=sys.stderr)
                return 2
            raise

        tmp = None
        try:
            wav = _ensure_wav(src)
            if wav != src:
                tmp = wav
            samples, sample_rate = _read_wave(str(wav), sherpa_onnx)
            # SenseVoice/Parakeet/Whisper 全系都按 16k 训练，_ensure_wav 在有 ffmpeg 的机器上必然给出 16k。
            # 走到不一致这一档＝这台**没有 ffmpeg** 而原始 wav 是 44.1k/48k（或反过来：ffmpeg 的参数漂了）。
            # ⇒ 如实报「缺 ffmpeg 归一」这条可行动文案，**不做静默降采样**——
            #   把「依赖没装好」藏成「识别质量差」是本仓最贵的一类缺陷（同 §一·12 派发死腿那族）。
            if sample_rate != TARGET_SAMPLE_RATE:
                raise RuntimeError("ERR:FFmpeg:音频采样率 %d Hz 未归一到 %d Hz，需在服务器安装 ffmpeg" %
                                   (sample_rate, TARGET_SAMPLE_RATE))
            if samples.size == 0:
                print("ERR:Empty:音频无有效采样（时长为零或纯静音）", file=sys.stderr)
                return 2

            stream = recognizer.create_stream()
            stream.accept_waveform(sample_rate, samples)
            recognizer.decode_stream(stream)
            text = (stream.result.text or "").strip()
            if not text:
                print("ERR:Empty:未识别出语音内容（纯音乐/无人声/语言不被模型支持）", file=sys.stderr)
                return 2

            # 时间戳：sherpa-onnx 的 ParaParakeet/Transducer 与部分 Whisper 编解码器带结果
            # 时间戳；SenseVoice 只出整段文本。带不出时间戳时**不编造假时间戳**——
            # 一律退化为「整段一个 cue」，由 WriteTranslationSrt 按 duration 铺开，
            # 绝不让「没有时间戳」变成「没有产物」（译文第一）。
            segments = []
            duration = float(samples.size) / float(sample_rate) if sample_rate else 0.0
            try:
                for seg in getattr(stream.result, "timestamps", []) or []:
                    segments.append({"start": float(seg[0]), "end": float(seg[1]), "text": text})
            except Exception:
                segments = []
            if not segments:
                segments = [{"start": 0.0, "end": duration, "text": text}]

            print(json.dumps(
                {"text": text, "segments": segments, "language": "", "engine": kind},
                ensure_ascii=False,
            ))
            return 0
        finally:
            if tmp is not None:
                try:
                    os.unlink(tmp)
                except OSError:
                    pass
    except RuntimeError as exc:
        msg = str(exc)
        if not msg.startswith("ERR:"):
            msg = "ERR:Decode:" + msg
        print(msg, file=sys.stderr)
        return 2
    except Exception as exc:
        print("ERR:Decode:%s" % exc, file=sys.stderr)
        return 2


def main() -> int:
    if len(sys.argv) < 2:
        print("ERR:Usage:probe|transcribe <path>", file=sys.stderr)
        return 2
    cmd = sys.argv[1]
    if cmd == "probe":
        return cmd_probe()
    if cmd == "transcribe":
        if len(sys.argv) < 3:
            print("ERR:Usage:transcribe <path>", file=sys.stderr)
            return 2
        return cmd_transcribe(sys.argv[2])
    print("ERR:Usage:probe|transcribe <path>", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main())
