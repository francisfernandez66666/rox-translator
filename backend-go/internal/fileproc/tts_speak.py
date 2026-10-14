#!/usr/bin/env python3
"""tts_speak.py — 译文语音合成子命令壳（P0 基础件；P3 落地 voices/speak 本体）。

子命令：
  probe                      自检依赖与资产；stdout 打状态行 "OK: ..." / "MISSING: ..."（退出码 0 / 3）
  voices                     列出 assets/tts/voices/ 下可用参考音色（JSON 数组打 stdout）
  speak <text> [--lang xx] [--out file]   译文 → 音频文件（无 stdout；失败见 stderr）

依赖（**导入名**，与 fileproc/tts.go 的 ttsPythonModules 同源维护）：
  onnxruntime / transformers / soundfile / resemble_perth / pkuseg / pykakasi / lameenc。
  ★ 中文缺 pkuseg（仓颉前端）、日文缺 pykakasi，这两个语种的音质会塌——
    D5 口径：真实依赖必须进探测，不许「装了一半还报绿」。

当前引擎：sherpa_onnx.OfflineTts（通用加载器；Kokoro / MeloTTS / VITS / Matcha 权重都吃）。
★ 与已拍板主力方案的关系（务必如实理解，别照着注释改坏）：
  Chatterbox Multilingual V3（Resemble AI，MIT，23 语）是 2026-10-14 **用户拍板的主力**，
  但它需要自实现的 T3→S3Gen→声码器解码循环，不是 sherpa-onnx 能直接 from_xxx 的形态
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
MODULES = [
    "onnxruntime",
    "transformers",
    "soundfile",
    "resemble_perth",
    "pkuseg",
    "pykakasi",
    "lameenc",
]

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


def _try_chatterbox(_text: str, _lang: str, _voice: Path, _out: Path):  # pragma: no cover
    """主力引擎钩位（尚未落地，见文件头说明）。返回 None 表示"本壳不支持，退通用加载器"。"""
    return None


def _build_tts(model_dir: Path):
    """构造 sherpa-onnx OfflineTts。按资产形状试探三种形态，都不中即 ERR:Asset。"""
    import sherpa_onnx

    def find(*keywords):
        for p in sorted(model_dir.glob("*.onnx")):
            name = p.name.lower()
            if all(k in name for k in keywords):
                return p
        return None

    model_xml = None
    for p in model_dir.glob("*.onnx"):
        model_xml = p
        break
    for cand in ("model.onnx", "kokoro.onnx", "melotts.onnx", "vits.onnx"):
        q = model_dir / cand
        if q.exists():
            model_xml = q
            break

    try:
        # ① Kokoro / MeloTTS（多语/中英混和交流 ）：repository 形态较常见，优先试
        lexicon = None
        for cand in ("lexicon.txt", "lexicon-us-en.txt", "lexicon-zh.txt"):
            q = model_dir / cand
            if q.exists():
                lexicon = q
                break
        tokens = None
        for cand in ("tokens.txt", "voices.txt"):
            q = model_dir / cand
            if q.exists():
                tokens = q
                break
        data_dir = None
        for cand in ("espeak-ng-data", "voices", "data"):
            q = model_dir / cand
            if q.is_dir():
                data_dir = q
                break
        if model_xml and tokens and lexicon:
            kwargs = dict(
                model=str(model_xml), tokens=str(tokens), lexicon=str(lexicon),
                data_dir=str(data_dir) if data_dir else "",
                num_threads=os.cpu_count() or 2,
            )
            try:
                return sherpa_onnx.OfflineTts.from_kokoro(**kwargs), "kokoro"
            except Exception:
                return sherpa_onnx.OfflineTts.from_vits(
                    model=str(model_xml), tokens=str(tokens), lexicon=str(lexicon),
                    data_dir=str(data_dir) if data_dir else "",
                    num_threads=os.cpu_count() or 2,
                ), "vits"
        # ② Matcha：acoustic model + vocoder
        acoustic = find("acoustic") or find("matcha")
        vocoder = find("vocoder") or find("hifigan") or find("hifi")
        if acoustic and vocoder and tokens:
            return sherpa_onnx.OfflineTts.from_matcha(
                acoustic_model=str(acoustic), vocoder=str(vocoder), tokens=str(tokens),
                lexicon=str(lexicon) if lexicon else "",
                num_threads=os.cpu_count() or 2,
            ), "matcha"
    except Exception as exc:
        raise RuntimeError("ERR:ModelLoad:合成器构造失败 %s" % exc)

    raise RuntimeError(
        "ERR:Asset:无法从 assets/tts/ 的权重与 tokens/lexicon 推断模型形态"
        "（需要 model.onnx + tokens.txt + lexicon.txt，或 acoustic+vocoder 两件套）"
    )


# ------------------------------------------------------------ 合成本体

def _write_wav(samples, sample_rate: int, out: Path) -> None:
    import numpy as np
    import soundfile as sf

    arr = np.asarray(samples, dtype="float32")
    if arr.ndim > 1:
        arr = arr.mean(axis=1)
    peak = float(np.max(np.abs(arr))) if arr.size else 0.0
    # 归一化到 0.89 满量程：留一点headroom  给 mp3 编码器的 overshoot（削顶失真很难听）。
    if peak > 0.89:
        arr = arr * (0.89 / peak)
    wav_out = out.with_suffix(".wav") if out.suffix.lower() != ".wav" else out
    sf.write(str(wav_out), arr, sample_rate, subtype="PCM_16")
    _to_mp3_if_needed(wav_out, out)


def _to_mp3_if_needed(wav_path: Path, out: Path) -> None:
    """--out 指向 .mp3 时转一次（D4：产物 mp3 兼容优先）。转不动就保留 wav 并如实报 Encode。"""
    if out.suffix.lower() != ".mp3":
        return
    import subprocess

    import lameenc

    if wav_path.resolve() != out.resolve():
        cmd = ["ffmpeg", "-nostdin", "-y", "-i", str(wav_path), "-codec:a", "libmp3lame", "-q:a", "4", str(out)]
        try:
            proc = subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if proc.returncode == 0 and out.exists() and out.stat().st_size > 0:
                return
        except Exception:
            pass
    # 没有 ffmpeg 时用 lameenc 直压 PCM
    try:
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
        return
    except Exception as exc:
        raise RuntimeError("ERR:Encode:mp3 编码失败 %s（已生成 wav：%s）" % (exc, wav_path))


def cmd_speak(text: str, lang: str, out_path: str, voice_arg: str | None) -> int:
    if not text.strip():
        print("ERR:Empty:文本为空", file=sys.stderr)
        return 2
    if not ASSETS_DIR.is_dir() or not sorted(ASSETS_DIR.glob("*.onnx")):
        print("ERR:Asset:assets/tts/*.onnx 缺失", file=sys.stderr)
        return 2

    voice = _pick_voice(voice_arg)
    out = Path(out_path)

    # 主力钩位：Chatterbox 到位后在这里接管，产出成功即返回
    try:
        if _try_chatterbox(text, lang, voice, out) is True:
            if out.exists() and out.stat().st_size > 0:
                return 0
    except RuntimeError as exc:
        msg = str(exc)
        if msg.startswith("ERR:"):
            print(msg, file=sys.stderr)
            return 2

    import sherpa_onnx  # noqa: F401  导入失败由下面的通用异常处理包住

    tts, kind = _build_tts(ASSETS_DIR)
    try:
        audio = tts.generate(text, sid=0, speed=1.0)
    except Exception as exc:
        raise RuntimeError("ERR:ModelLoad:合成失败 %s" % exc)
    if audio is None or len(audio.samples) == 0:
        print("ERR:Empty:模型未产出有效采样", file=sys.stderr)
        return 2
    _write_wav(audio.samples, audio.sample_rate, out)
    return 0


def main() -> int:
    argv = sys.argv[1:]
    if not argv:
        print("ERR:Usage:probe|voices|speak <text> [--lang xx] [--out file] [--voice id.wav]", file=sys.stderr)
        return 2
    cmd = argv[0]
    rest = argv[1:]

    if cmd == "probe":
        return cmd_probe()
    if cmd == "voices":
        return cmd_voices()
    if cmd == "speak":
        if not rest:
            print("ERR:Usage:speak <text> [--lang xx] [--out file]", file=sys.stderr)
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
            print("ERR:Usage:speak 必须带 --out <file>", file=sys.stderr)
            return 2
        try:
            return cmd_speak(text, lang, out_path, voice)
        except RuntimeError as exc:
            msg = str(exc)
            if not msg.startswith("ERR:"):
                msg = "ERR:ModelLoad:" + msg
            print(msg, file=sys.stderr)
            return 2
        except Exception as exc:
            print("ERR:ModelLoad:%s" % exc, file=sys.stderr)
            return 2
    print("ERR:Usage:probe|voices|speak <text> [--lang xx] [--out file]", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main())
