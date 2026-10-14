// ============ tts.go · 职责说明 ============
// TTS（译文语音合成）能力件：Chatterbox Multilingual V3 的 ONNX 权重（用户 2026-10-14
// 拍板定为主力，MIT，23 语）经 tts_speak.py 子命令壳把「译文文本 → 可播放音频」产出
// （上游适配器定位，见《改造方案_OCR与ASR接入_20261014_v2.md》§三 3.3）。
//
// 可插拔硬标准（用户 2026-10-14 立）：本文件 + tts_speak.py + assets/tts/ 是 TTS 的全部家当；
// 现有文件只留两处接线（healthcheck.go 探测块、api/server.go 就绪字段）。
// 删掉这三组新文件与接线行 ⇒ 逐字节回退到 2026-10-14 前。
//
// 译文第一（用户令，最高优先级）：TTS 是**择优产出**——语种越界（Chatterbox 23 语之外）或
//
//	合成失败时，调用方的动作是"这一次没有语音、别的照常"，译文与工单状态一个字节都不动。
//	因此本能力不可用**不影响任何主链**：探测为红只代表「没有语音」，不代表故障。
//
// ★ 现役射程（2026-10-15 核正，旧注释这里写错了两处，都是"注释声称有个事实源而它不存在"那一族）：
//   - 合成入口的真名是 **SpeakToWav**（本文件），不叫 SpeakText；
//   - 现在**唯一的调用方**是即时翻译工作台的朗读端点 `internal/api/chat_audio.go` 的 handleChatTTS
//     （POST /api/chat/tts，前端 `src/api/chatVoice.ts` 的 speakAloud）。
//     工单侧的音频交付形态是「译文字幕 .srt ＋译文 .md ＋原音频副本」（见 engine/file.go 的 audio 分支），
//     **不含**合成语音；本文件头此前那句"跳过音频 + GateWarnings 明示"因此不成立——
//     工单链里压根没有这一步，GateWarnings 也就收不到这一条。要不要给工单产物再加一份译文语音 mp3
//     是产品决策（现网模型资产 assets/tts/*.onnx 尚未投放，接了也是恒跳过），已记入待决策账。
//
// 边界（刻意不做）：不建任务类型/队列/状态机；不联网下载模型与音色（私有化机器可能无外网）。
// =============================================
package fileproc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ttsPythonModules TTS 真实依赖的**导入名**清单（与 tts_speak.py 的 MODULES 同源维护，
// 两侧改一侧必红在 capabilities_p0_test.go 的协议断言上）：
//
//	onnxruntime   —— ONNX 推理（Chatterbox Q4 权重，CPU 可跑）
//	transformers  —— T3 Llama 分词器（照 onnx-community 参考驱动，勿轻言换成 tokenizers 裸载）
//	soundfile     —— wav 读写
//	resemble_perth—— PerTh 水印（D11：默认开，产物可溯源）
//	pkuseg        —— 中文仓颉编码前端（onnx 驱动必需，缺了中文音质塌）
//	pykakasi      —— 日文汉字→假名前端（同上）
//	lameenc       —— mp3 编码（D4：产物 mp3 兼容优先）
var ttsPythonModules = []string{"onnxruntime", "transformers", "soundfile", "resemble_perth", "pkuseg", "pykakasi", "lameenc"}

// ttsAssetGlob TTS 模型资产判别串：assets/tts/ 下至少一个 .onnx 权重才算资产落盘。
const ttsAssetGlob = "*.onnx"

// ttsVoiceFile 默认音色（零样本克隆的参考音频，5s+ 干净人声，24kHz 单声道）。
// ★ D9（2026-10-14 用户令「随便找个默认音色先上」）：Chatterbox 纯零样本克隆、无预置发音人，
//
//	每次合成都要这段参考。占位音色源与替换步骤见 assets/tts/voices/README.md——
//	换它只是换一个资产文件，前端/产物契约/计费零改动（可插拔原则的受益面）。
const ttsVoiceFile = "default_reference.wav"

// TtsScriptEnv tts_speak.py 路径覆盖环境变量（开发/测试用，同 ANYDOC_SCRIPT 约定）。
const TtsScriptEnv = "TTS_SCRIPT"

// TtsAvailable TTS 能力是否就绪（healthcheck 缓存）。
// ★ 与 OCR/ASR 不同，这里多查一项**默认音色**：模块和权重齐了却没有参考音频，
//
//	合成必然失败——按 D5 口径这种「半条命」不许报绿。
func TtsAvailable() bool { return CheckHealth().TtsAvail }

// probeTts 在指定 Python 解释器下探测 TTS 依赖。
// 返回 (就绪, 不就绪时的 actionable 告警)——话术在此单源维护，healthcheck.go 只做两行转发。
func probeTts(pythonPath string) (bool, string) {
	for _, m := range ttsPythonModules {
		if !checkPythonModule(pythonPath, m) {
			return false, "Python " + m + " 库未安装，译文语音合成（TTS）不可用（pip install onnxruntime transformers soundfile resemble-perth pkuseg pykakasi lameenc）"
		}
	}
	if !ttsAssetsReady() {
		return false, "TTS 模型资产缺失（assets/tts/*.onnx 未随包落盘），译文语音合成不可用，取权重步骤见 assets/tts/README.md"
	}
	if !ttsVoiceReady() {
		return false, "TTS 默认音色未配置（assets/tts/voices/" + ttsVoiceFile + " 缺失），译文语音合成不可用，投放步骤见 assets/tts/voices/README.md"
	}
	return true, ""
}

// ttsAssetsReady TTS 模型资产是否落盘（脚本同目录 assets/tts/ 至少一个 .onnx）。
func ttsAssetsReady() bool {
	matches, err := filepath.Glob(filepath.Join(ttsAssetsDir(), ttsAssetGlob))
	return err == nil && len(matches) > 0
}

// ttsVoiceReady 默认音色是否已投放（assets/tts/voices/default_reference.wav 存在且非空）。
// 非空判定不能省：零字节文件同样让合成失败，而「文件在」会骗过纯存在性检查。
func ttsVoiceReady() bool {
	info, err := os.Stat(filepath.Join(ttsVoicesDir(), ttsVoiceFile))
	return err == nil && info.Size() > 0
}

// ttsScriptPath 定位 tts_speak.py（与可执行文件同目录，同 anydoc_md.py 部署约定；
// TTS_SCRIPT 环境变量覆盖，供开发/测试环境使用）。
func ttsScriptPath() string {
	if v := strings.TrimSpace(os.Getenv(TtsScriptEnv)); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(os.Args[0]), "tts_speak.py")
}

// ttsAssetsDir 定位 TTS 资产目录（相对**脚本**同目录，随 sync_fileproc_assets.sh 同步）。
func ttsAssetsDir() string {
	return filepath.Join(filepath.Dir(ttsScriptPath()), "assets", "tts")
}

// ttsVoicesDir 定位默认音色目录（assets/tts/voices/，参考音频随资产树同步）。
func ttsVoicesDir() string {
	return filepath.Join(ttsAssetsDir(), "voices")
}

// ===== P3：合成入口（即时翻译「朗读译文」按钮的服务端半边） =====

// ttsTimeout 合成墙钟预算。Chatterbox 0.5B 在 CPU 上约 1~3s/句；
// 60s 够数百字的一段译文，超时的多半是模型加载阶段卡住（那时重试也不会更好）。
func ttsTimeout() time.Duration {
	if v := os.Getenv("TTS_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

// ttsMaxChars 单次合成的文本上限（按 rune 计）。
// 为什么限制：① 长文本合成时间线性增长，HTTP 请求会先超时再出结果；
// ② Chatterbox 是零样本克隆，超长文本的音色稳定性会明显下降（越长越飘）。
// 超限的行为是**拒绝并给出明确文案**，不是截断后半程——半段语音比没有更难解释。
const ttsMaxChars = 2000

// SpeakToWav 把译文文本合成到 outPath（wav，24k 单声道为准，由脚本决定具体格式）。
// ★ 译文第一（最高优先级）：TTS 是**择优产出**，本函数返回错误只代表"这次没有语音"，
//
//	调用方必须据此**跳过音频继续交付译文**，绝不能把翻译结果一起判死。
//
// 出参一律是 *TTLError（分档名见下方五个常量），人话句子由 friendlyTtsError 解析脚本 stderr 的 ERR:<Variant> 行给出。
func SpeakToWav(ctx context.Context, text, lang, outPath string) error {
	if strings.TrimSpace(text) == "" {
		return &TTLError{Reason: TtsReasonEmpty, Message: "没有可朗读的内容"}
	}
	if len([]rune(text)) > ttsMaxChars {
		return &TTLError{Reason: TtsReasonTooLong, Message: fmt.Sprintf("译文过长（%d 字），单次朗读上限 %d 字，请分段后再试", len([]rune(text)), ttsMaxChars)}
	}
	if !TtsAvailable() {
		return &TTLError{Reason: TtsReasonCapabilityMissing, Message: "译文语音合成（TTS）不可用：服务器未安装所需依赖、模型资产或默认音色（assets/tts/），投放步骤见 assets/tts/README.md"}
	}
	if _, err := os.Stat(ttsScriptPath()); err != nil {
		return &TTLError{Reason: TtsReasonCapabilityMissing, Message: fmt.Sprintf("TTS 脚本缺失：tts_speak.py 未随二进制部署到 %s", filepath.Dir(os.Args[0]))}
	}
	if dir := filepath.Dir(outPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	argv := []string{ttsScriptPath(), "speak", text, "--out", outPath}
	if strings.TrimSpace(lang) != "" {
		argv = append(argv, "--lang", strings.TrimSpace(lang))
	}
	bin, args := wrapNice(pyBin(), argv)
	_, stderr, err := runSubprocess(ctx, ttsTimeout(), bin, args, nil)
	if err != nil {
		// 脚本认得的 ERR:<Variant> 行＝有分档名可用（前端按档取 12 语种词典文案）；
		// 认不出的一律 TtsReasonFailed（"可重试"那一档），**不许**升级成"能力缺失"去误导运维投放资产。
		if variant := ttsErrVariant(string(stderr)); variant != "" {
			return &TTLError{Reason: ttsReasonForVariant(variant), Message: friendlyTtsError(string(stderr)), Err: err}
		}
		return &TTLError{Reason: TtsReasonFailed, Message: fmt.Sprintf("TTS 合成失败: %v\n%s", err, truncateTail(stderr)), Err: err}
	}
	if info, serr := os.Stat(outPath); serr != nil || info.Size() == 0 {
		return &TTLError{Reason: TtsReasonFailed, Message: "TTS 产出了空音频（模型未产出有效采样）", Err: serr}
	}
	return nil
}

// friendlyTtsError 解析 tts_speak.py stderr 的 ERR:<Variant>:<msg> 行 → 面向用户话术；
// 无法识别时返回 ""（由调用方给原始错误）。
func friendlyTtsError(stderr string) string {
	variant := ttsErrVariant(stderr)
	if variant == "" {
		return ""
	}
	switch variant {
	case "ModelLoad", "Asset":
		return "TTS 模型资产缺失或加载失败：assets/tts/*.onnx 未随包落盘，取权重步骤见 assets/tts/README.md"
	case "Voice":
		return "TTS 默认音色缺失：assets/tts/voices/ 下没有可用的参考音频，投放步骤见 assets/tts/voices/README.md"
	case "UnsupportedLang":
		return "该语种暂不支持语音播放（超出当前音色可覆盖范围）"
	case "Empty":
		return "这段译文没有可朗读的内容"
	case "Import":
		return "TTS 依赖未就绪：服务器缺少 onnxruntime/transformers/soundfile 等依赖"
	case "Encode":
		return "音频编码失败（mp3 编码器不可用），请改用 wav 或联系管理员"
	default:
		return ""
	}
}

// ttsErrVariant 从子进程 stderr 取脚本写的 `ERR:<Variant>:<细节>` 行的 Variant。
// 与 friendlyTtsError 共用这一条提取腿（同一份判据不写第二遍），取不到回空串。
func ttsErrVariant(stderr string) string {
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "ERR:") {
			return strings.SplitN(strings.TrimPrefix(l, "ERR:"), ":", 2)[0]
		}
	}
	return ""
}

// TTS 朗读失败的五个分档名＝**对外排障契约**（同 AGENTS 里 canned 那组分档的纪律：
// 名字逐字钉住、改名式破坏必须当场红，锁见 tts_reason_test.go）。
//
// 为什么要给机器一份档位而不是只给人一句话：朗读是"择优产出"，界面上那一行提示
// 必须是访客正在看的那门语言；前端只有按档位取 12 语种词典，才能既不把服务端中文
// 投给泰语/俄语访客，也不把"这语种暂时没声音"猜成"服务器坏了"。
const (
	// TtsReasonEmpty 没有可朗读的内容（空文本／纯空白）。
	TtsReasonEmpty = "empty"
	// TtsReasonTooLong 超单次朗读字数上限（客户端动作：分段）。
	TtsReasonTooLong = "too_long"
	// TtsReasonCapabilityMissing 依赖／模型资产／音色未就绪（运维动作：按 assets/tts/README.md 投放）。
	TtsReasonCapabilityMissing = "capability_missing"
	// TtsReasonUnsupportedLang 语种超出音色覆盖范围（客户动作：译文照常看，这一语种没有语音）。
	TtsReasonUnsupportedLang = "unsupported_lang"
	// TtsReasonFailed 其余合成失败（可重试；**不许**把它写成能力缺失去误导运维）。
	TtsReasonFailed = "failed"
)

// ttsReasonForVariant 脚本 Variant → 出栈档位。未知 Variant 一律 TtsReasonFailed：
// 认不出＝不知道是什么失败，按"可重试"处理是最小谎，比猜一个能力档名安全。
func ttsReasonForVariant(variant string) string {
	switch variant {
	case "UnsupportedLang":
		return TtsReasonUnsupportedLang
	case "Empty":
		return TtsReasonEmpty
		// 模型／资产／音色／依赖／编码器：都是"这台机器还没配齐这一档"，运维动作同一件
	case "ModelLoad", "Asset", "Voice", "Import", "Encode":
		return TtsReasonCapabilityMissing
	default:
		return TtsReasonFailed
	}
}

// TTLError 一次"没有语音"的结构化结果：**Reason 给机器，Message 给人**。
//
// Err 保留底层错误（子进程退出等），errors.Is/As 沿这条链继续查得到，
// 换成结构体不丢包裹语义；Error() 回的是**人话句子**，与改造前 fmt.Errorf 的文本逐字一致，
// 因此既有按错误文本断言的用例不会因为它转成结构体而误红。
type TTLError struct {
	Reason  string
	Message string
	Err     error
}

// Error 实现 error 接口。
func (e *TTLError) Error() string { return e.Message }

// Unwrap 暴露底层错误，供 errors.Is/As 继续查。
func (e *TTLError) Unwrap() error { return e.Err }

// TtsReasonOf 从任意错误取朗读档位：nil 回空串；不是 TTLError（本文件之外的失败）回 TtsReasonFailed。
// 调用方（`internal/api/chat_audio.go`）拿它出栈；**禁止**在端点或前端再造一次字符串匹配。
func TtsReasonOf(err error) string {
	if err == nil {
		return ""
	}
	var te *TTLError
	if errors.As(err, &te) && te.Reason != "" {
		return te.Reason
	}
	return TtsReasonFailed
}
