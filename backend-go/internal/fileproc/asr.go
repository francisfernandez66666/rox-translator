// ============ asr.go · 职责说明 ============
// ASR（音频语音识别）能力件：sherpa-onnx（SenseVoice 等 ONNX 模型）经 asr_transcribe.py
// 子命令壳把「音频 → 带时间戳的文本」投喂给既有翻译引擎（上游适配器定位，见
// 《改造方案_OCR与ASR接入_20261014_v2.md》§二）。
//
// 可插拔硬标准（用户 2026-10-14 立）：本文件 + asr_transcribe.py + assets/asr/ 是 ASR 的全部家当；
// 现有文件只留两处接线（healthcheck.go 探测块、api/server.go 就绪字段）。
// 删掉这三组新文件与接线行 ⇒ 逐字节回退到 2026-10-14 前。
//
// 入口分工（用户令）：即时翻译只收**麦克风录音**（文字/录音切换，永不上传文件，试用态不开放）；
// 工单收**音频文件**（产物 md+srt+mp3）。两端共用本适配器一份实现（D8）。
// 边界（刻意不做）：不建任务类型/队列/状态机；不联网下载模型（权重随 sync_fileproc_assets.sh 落盘）；
// P0 只交付探测与脚本壳，转写入口（TranscribeAudio）在 P2 落地，届时只追加本文件。
// =============================================
package fileproc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// asrPythonModules ASR 真实依赖的**导入名**清单（与 asr_transcribe.py 的 MODULES 同源维护，
// 两侧改一侧必红在 capabilities_p0_test.go 的协议断言上）。
// wav 读取走 sherpa_onnx.read_wave（内置，无第三方音频库依赖）；
// mp3/m4a 等容器先由部署机 ffmpeg 转 16k 单声道 wav 再进壳（见 部署指南 §一 apt 行）。
var asrPythonModules = []string{"sherpa_onnx"}

// asrAssetGlob ASR 模型资产判别串：assets/asr/ 下至少一个 .onnx 权重才算资产落盘。
const asrAssetGlob = "*.onnx"

// AsrNoSpeechReason 是"这段音频没有有效语音"这一档的**对外排障契约名**（前端按它取
// chat.micNoSpeech 词典；与 asr_transcribe.py 的 REASON_NO_SPEECH 逐字同源）。
// ★ 为什么单列一档而不是并进 failed：静音/没授权麦克风与"服务端坏了"给用户的动作完全不同
//
//	（前者让他靠近麦克风重说，后者是报障），混成一档等于把可行动提示换成一句"失败"。
const AsrNoSpeechReason = "no_speech"

// AsrNoSpeechMessage 是 NoSpeech 档面向用户的人话句子（api/asrReason 按它前缀判档，
// 与 friendlyAsrError 的 NoSpeech 分支**同一常量**，避免两处各写一份字符串后漂移）。
const AsrNoSpeechMessage = "ASR 未检测到有效语音：录音可能过于安静或麦克风未授权，请靠近麦克风再说一次"

// AsrScriptEnv asr_transcribe.py 路径覆盖环境变量（开发/测试用，同 ANYDOC_SCRIPT 约定）。
const AsrScriptEnv = "ASR_SCRIPT"

// AsrAvailable ASR 能力是否就绪（healthcheck 缓存）。
// ★ D5 口径：Python 模块与模型资产两者齐备才为真（同 ocr.go，不重复论证）。
func AsrAvailable() bool { return CheckHealth().AsrAvail }

// probeAsr 在指定 Python 解释器下探测 ASR 依赖。
// 返回 (就绪, 不就绪时的 actionable 告警)——话术在此单源维护，healthcheck.go 只做两行转发。
func probeAsr(pythonPath string) (bool, string) {
	for _, m := range asrPythonModules {
		if !checkPythonModule(pythonPath, m) {
			return false, "Python " + m + " 库未安装，音频语音识别（ASR）不可用（pip install sherpa-onnx）"
		}
	}
	if !asrAssetsReady() {
		return false, "ASR 模型资产缺失（assets/asr/*.onnx 未随包落盘），音频语音识别不可用，取权重步骤见 assets/asr/README.md"
	}
	return true, ""
}

// asrAssetsReady ASR 模型资产是否落盘（脚本同目录 assets/asr/ 至少一个 .onnx）。
func asrAssetsReady() bool {
	matches, err := filepath.Glob(filepath.Join(asrAssetsDir(), asrAssetGlob))
	return err == nil && len(matches) > 0
}

// AsrAvailable ASR 就绪时，本机**实际会路由到**哪一档识别引擎（供 /api/health 的 asr_engine 出栈）。
//
// ★ 为什么出这一档：`asr_ready=true` 只说"这台机器能识别"，读者无从知道它是 SenseVoice（带 ITN 标点/多语热切）
//
//	还是 Whisper 兜底档——两者对同一段音频的产出形态不同（D12 排障就卡在"这台到底跑的是哪个"）。
//	取值口径与 asr_transcribe.py 的 `_build_recognizer` **同一顺序**（SenseVoice ＞ Transducer ＞ Whisper ＞ 单权重兜底），
//	所以它是"真会用到的那一档"而不是"声明档"；资产认不出型号或不就绪时回空串（前端/运维按"未知"处理）。
//	同 #42 口径：只出档位词（sense_voice / transducer / whisper），绝不出权重文件名或绝对路径。
func AsrEngineName() string {
	if !AsrAvailable() {
		return ""
	}
	return asrEngineFromAssets(asrAssetsDir())
}

// asrEngineFromAssets 判据本体（**纯函数**：喂一个目录即可覆盖四种路由组合，不必装 sherpa-onnx）。
func asrEngineFromAssets(dir string) string {
	onnx, _ := filepath.Glob(filepath.Join(dir, asrAssetGlob))
	sort.Strings(onnx)
	if len(onnx) == 0 {
		return ""
	}
	tokens := filepath.Join(dir, "tokens.txt")
	hasTokens := false
	if _, err := os.Stat(tokens); err == nil {
		hasTokens = true
	}
	anyTxt, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	// find 与脚本同序：全部关键词都命中文件名才算（"model" 命中即 SenseVoice 主力档）。
	find := func(keywords ...string) string {
		for _, p := range onnx {
			name := strings.ToLower(filepath.Base(p))
			ok := true
			for _, k := range keywords {
				if !strings.Contains(name, k) {
					ok = false
					break
				}
			}
			if ok {
				return p
			}
		}
		return ""
	}
	if (find("model") != "" || find("sense") != "") && hasTokens {
		return "sense_voice"
	}
	if find("encoder") != "" && find("decoder") != "" && find("joiner") != "" && hasTokens {
		return "transducer"
	}
	wenc := find("whisper", "encoder")
	if wenc == "" {
		wenc = find("encoder")
	}
	wdec := find("whisper", "decoder")
	if wdec == "" {
		wdec = find("decoder")
	}
	if wenc != "" && wdec != "" && hasTokens {
		return "whisper"
	}
	if len(anyTxt) > 0 && len(onnx) == 1 {
		return "transducer"
	}
	return ""
}

// asrScriptPath 定位 asr_transcribe.py（与可执行文件同目录，同 anydoc_md.py 部署约定；
// ASR_SCRIPT 环境变量覆盖，供开发/测试环境使用）。
func asrScriptPath() string {
	if v := strings.TrimSpace(os.Getenv(AsrScriptEnv)); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(os.Args[0]), "asr_transcribe.py")
}

// asrAssetsDir 定位 ASR 资产目录（相对**脚本**同目录，随 sync_fileproc_assets.sh 同步）。
func asrAssetsDir() string {
	return filepath.Join(filepath.Dir(asrScriptPath()), "assets", "asr")
}

// asrTimeout ASR 转写墙钟预算。音频按时长线性增长且与精简 concurrency 无关：
// 默认 900s（15 分钟）覆盖常见 1 小时以内素材在 CPU 上的 SenseVoice（约 15~30 倍实时）估算。
func asrTimeout() time.Duration {
	if v := os.Getenv("ASR_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 900 * time.Second
}

// asrSegment ASR 转写出的一段（带时间戳）。
type asrSegment struct {
	Start float64 `json:"start"` // 起始秒
	End   float64 `json:"end"`   // 结束秒
	Text  string  `json:"text"`  // 该段转写文本（源语言）
}

// asrResult ASR 转写结果 JSON 形状（与 asr_transcribe.py transcribe 子命令 stdout 一致）。
type asrResult struct {
	Text     string       `json:"text"`     // 全文拼接（\n 分隔；退化形态，无 segments 时用）
	Segments []asrSegment `json:"segments"` // 带时间戳的段（优先用，产出 srt 需它）
	Language string       `json:"language"` // 识别出的语种（可选）
}

// TranscribeAudio 音频 → 带时间轴的字幕 cue（P2 转写本体）。
// 转写结果由 rememberAsrCues 记一份，供写回阶段 AudioCues 取回
// （★ 不为同一份音频重复跑一次几十秒的 ASR，见 asrCueCache 的说明）。
//
// ★ 译文第一：ASR 不可用（AsrAvailable()==false）或脚本缺失时返回**友好错误**，
//
//	由调用方决定降级或提示；识别出来为空也如实报出，不静默交空产物。
func TranscribeAudio(ctx context.Context, path string) ([]SubtitleCue, error) {
	if !AsrAvailable() {
		return nil, fmt.Errorf("音频语音识别（ASR）不可用：服务器未安装 sherpa-onnx 或未部署 ASR 模型资产（assets/asr/*.onnx），取权重步骤见 assets/asr/README.md")
	}
	if _, err := os.Stat(asrScriptPath()); err != nil {
		return nil, fmt.Errorf("ASR 脚本缺失：asr_transcribe.py 未随二进制部署到 %s", filepath.Dir(os.Args[0]))
	}
	bin, argv := wrapNice(pyBin(), []string{asrScriptPath(), "transcribe", path})
	stdout, stderr, err := runSubprocess(ctx, asrTimeout(), bin, argv, nil)
	if err != nil {
		if msg := friendlyAsrError(string(stderr)); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, fmt.Errorf("ASR 转写失败: %w\n%s", err, truncateTail(stderr))
	}
	cues, perr := parseAsrJSON(string(stdout))
	if perr != nil {
		return nil, perr
	}
	rememberAsrCues(path, cues)
	return cues, nil
}

// ExtractAudioASR 音频 → 文本提取入口（extract.go 唯一需要的一行委托）：
// 转写出的每段文本按序喂给提取器 e（去重/trim 由 Extractor.add 负责），进入既有翻译管线。
// 时间轴不在这里返回——它由 TranscribeAudio 缓存，写回阶段经 AudioCues 取回。
func ExtractAudioASR(ctx context.Context, path string, e *Extractor) error {
	cues, err := TranscribeAudio(ctx, path)
	if err != nil {
		return err
	}
	if len(cues) == 0 {
		return fmt.Errorf("ASR 未识别出语音内容（音频可能无人声、纯音乐或已损坏）")
	}
	for _, c := range cues {
		e.add(c.Text)
	}
	return nil
}

// parseAsrJSON 把 asr_transcribe.py 的 stdout JSON 解析为字幕 cue 序列。
// 脚本可能在前缀打印非 JSON 日志（进度/告警），故取第一个 { 起的整段做反序列化。
func parseAsrJSON(out string) ([]SubtitleCue, error) {
	out = strings.TrimSpace(out)
	start := strings.Index(out, "{")
	if start < 0 {
		return nil, fmt.Errorf("ASR 输出格式异常（未找到 JSON）: %s", truncateTail([]byte(out)))
	}
	var res asrResult
	if err := json.Unmarshal([]byte(out[start:]), &res); err != nil {
		return nil, fmt.Errorf("ASR 输出 JSON 解析失败: %w", err)
	}
	cues := make([]SubtitleCue, 0, len(res.Segments))
	for i, s := range res.Segments {
		t := strings.TrimSpace(s.Text)
		if t == "" {
			continue // 空段丢弃；丢弃后 Index 仍按 cue 序号重排，不影响 srt 编号连续
		}
		cues = append(cues, SubtitleCue{Index: i + 1, Start: s.Start, End: s.End, Text: t})
	}
	if len(cues) == 0 {
		// 退化形态：脚本没给 segments（如仅支持整段文本的模型），按 \n 切成伪段。
		for i, line := range strings.Split(res.Text, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			cues = append(cues, SubtitleCue{Index: i + 1, Text: strings.TrimSpace(line)})
		}
	}
	if len(cues) == 0 {
		return nil, fmt.Errorf("ASR 未识别出语音内容（音频可能无人声、纯音乐或已损坏）")
	}
	return cues, nil
}

// friendlyAsrError 解析 asr_transcribe.py stderr 的 ERR:<Variant>:<msg> 行 → 面向用户话术；
// 无法识别时返回 ""（由调用方给原始错误）。
func friendlyAsrError(stderr string) string {
	line := ""
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "ERR:") {
			line = l
			break
		}
	}
	if line == "" {
		return ""
	}
	variant := strings.SplitN(strings.TrimPrefix(line, "ERR:"), ":", 2)[0]
	switch variant {
	case "ModelLoad", "Asset":
		return "ASR 模型资产缺失或加载失败：assets/asr/*.onnx 未随包落盘，取权重步骤见 assets/asr/README.md"
	case "AudioOpen", "AudioCorrupt", "Decode":
		return "音频无法读取或已损坏，请更换文件后重试"
	case "Unsupported", "Container":
		return "不支持的音频格式或编码，请使用 mp3/m4a/wav/ogg/flac/aac/opus"
	case "Empty":
		return "ASR 未识别出语音内容（音频可能无人声、纯音乐或已损坏）"
	case "NoSpeech":
		// 静音门在喂模型**之前**拦下（见 asr_transcribe.py detect_no_speech），
		// 与 Empty 的区别：Empty 是模型跑完了没吐字，NoSpeech 是压根没进模型。
		return AsrNoSpeechMessage
	case "Import":
		return "音频语音识别（ASR）依赖未就绪：服务器未安装 sherpa-onnx"
	case "FFmpeg":
		return "音频转码失败：服务器缺少 ffmpeg，无法解码该音频容器（mp3/m4a 等需先转 16kHz 单声道 wav）"
	default:
		return ""
	}
}

// ===== 时间轴传递：为什么要有这份缓存 =====
// 工单管线是两段式的：第 1 步 `ExtractTexts(音频)` 拿 ASR 文本去做翻译；
// 第 3 步写回产物时才需要那条**时间轴**生成 .srt。两者隔着一整轮 LLM 调用，
// 中间没有天然的载体能把它带过去（Extractor.Texts 是 []string，改它的形状要动
// 全格式的公共签名，代价远大于这里的收益）。
// 重跑一次 ASR 只为拿时间戳是不可接受的——同一份音频几十秒到十几分钟的 CPU 开销，
// 而且两次推理结果可能不完全一致（VAD 切分有抖动），反而会让 .srt 与已翻译的文本错位。
// 故缓存一条 ASR 的结果：**键＝源音频绝对路径**，命中即复用，进程内共享。
//
// 边界：① 只做一个 FIFO 队列按上限淘汰（不做 LRU），长跑进程内存有界；
//
//	② 缓存未命中（进程重启/并发另一实例）时 AudioCues 返回 nil，写回自动退化为
//	   「无时间轴」顺序编号 srt（WriteTranslationSrt 的既有兜底），不会失败。
const asrCueCacheMax = 32

// asrCueCache 就是上面那段说明所指的「最近 asrCueCacheMax 条 FIFO 队列」本体，
// asrCueMu 只保护这片 slice（读写都在进程内，不落库、不跨实例共享）。
var (
	asrCueMu    sync.Mutex
	asrCueCache []asrCueEntry
)

// asrCueEntry 一条缓存：音频在本机的绝对路径（键）→ 那一次 ASR 的时间轴。
type asrCueEntry struct {
	path string
	cues []SubtitleCue
}

// rememberAsrCues 记一份某音频的转写时间轴（覆盖同 key 旧值，超上限淘汰最早的一条）。
func rememberAsrCues(path string, cues []SubtitleCue) {
	asrCueMu.Lock()
	defer asrCueMu.Unlock()
	for i := range asrCueCache {
		if asrCueCache[i].path == path {
			asrCueCache[i].cues = cues
			return
		}
	}
	asrCueCache = append(asrCueCache, asrCueEntry{path: path, cues: cues})
	if len(asrCueCache) > asrCueCacheMax {
		asrCueCache = asrCueCache[1:]
	}
}

// AudioCues 取回某音频已转写的时间轴（写回阶段用）；未命中返回 nil。
func AudioCues(path string) []SubtitleCue {
	asrCueMu.Lock()
	defer asrCueMu.Unlock()
	for i := len(asrCueCache) - 1; i >= 0; i-- {
		if asrCueCache[i].path == path {
			return asrCueCache[i].cues
		}
	}
	return nil
}
