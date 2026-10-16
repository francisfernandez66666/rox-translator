// ============ tts.go · 职责说明 ============
// TTS（译文语音合成）能力件：**经 tts_speak.py 子命令壳把「译文文本 → 可播放音频」产出**
// （上游适配器定位，见《改造方案_OCR与ASR接入_20261014_v2.md》§三 3.3）。
//
// ★ 引擎现状（2026-10-16 校正，别照旧注释理解）：
//   - 主力模型的**拍板没变**：Chatterbox Multilingual V3（用户 2026-10-14 定，MIT，23 语），
//     但它的 T3→S3Gen→声码器解码循环**还没实现**（`tts_speak.py` 里只有个空钩位），
//     所以现在投 Chatterbox 权重会得到一个"点亮了就绪位、每次合成都失败"的假绿形态。
//   - 现役是**两条 sherpa-onnx 路线，按目标语种路由**（见 tts_speak.py 的 model_route）：
//     ① 中英档 MeloTTS zh_en（assets/tts/ 根目录，方案 §3.3 的「zh/en → 轻量档」）；
//     ② 多语档 Supertonic-3 int8（assets/tts/supertonic/，2026-10-16 用户钉「免费可商用」后选定，
//     单模型覆盖 ja/ko/fr/de/es/pt/ru/ar 八档）。覆盖语种见 ttsCoveredLangs（th 是唯一边界）。
//   - 两条路的切换**只动** assets/tts/ + 脚本内部驱动；本文件的接线（探测块 + 就绪位 + 覆盖清单）不变。
//
// ★ 许可如实登记（不许美化）：Supertonic-3 权重＝**OpenRAIL-M**（不是 MIT/CC0），
//
//	免费可商用但带使用限制（禁违法/歧视/误导/医疗建议等，下游需传递同样限制），见
//	assets/tts/supertonic/README.md。法人已确认这条口径可接受（2026-10-16 用户令「不花钱、可商用」）。
//
// 可插拔硬标准（用户 2026-10-14 立）：本文件 + tts_speak.py + assets/tts/ 是 TTS 的全部家当；
// 现有文件只留三处接线（healthcheck.go 探测块、api/server.go 的 tts_ready/tts_langs 两个字段、
// 前端按 tts_ready 与覆盖语种收门）。删掉这三组新文件与接线行 ⇒ 逐字节回退到 2026-10-14 前。
//
// 译文第一（用户令，最高优先级）：TTS 是**择优产出**——语种越界（覆盖清单之外）或
//
//	合成失败时，调用方的动作是"这一次没有语音、别的照常"，译文与工单状态一个字节都不动。
//	因此本能力不可用**不影响任何主链**：探测为红只代表「没有语音」，不代表故障。
//
// ★ 现役射程（2026-10-15 核正）：
//   - 合成入口两个，都在本文件：**SpeakToWav**（单段，即时翻译朗读用）与
//     **SpeakLines**（多段批量缝合，工单音频产物用）。
//   - 调用方：`internal/api/chat_audio.go` 的 handleChatTTS（POST /api/chat/tts，前端
//     `src/api/chatVoice.ts` 的 speakAloud）走 SpeakToWav；`internal/engine/file.go` 的
//     audio 分支走 SpeakLines，产出 `{base}_{lang}.mp3`（择优：语种覆盖才产）。
//     计费口径见 v2 方案 §3.5——识别与合成都**不单独立项**，TTS 算力视为获客成本。
//   - ⚠️ 现网资产状态（2026-10-16 起已投产）：`assets/tts/` 下是 **sherpa-onnx 中英档**
//     （MeloTTS zh_en，见 ttsCoveredLangs），已随部署落到两站 bin 目录；Chatterbox 驱动**尚未落地**，
//     所以非 zh/en 语种是**明确的能力边界**（unsupported_lang），不是"暂时坏了"。
//     换/补权重仍只动本目录 + 脚本内部驱动，Go 侧只会因**清单变化**（模块、覆盖语种）而改。
//
// 边界（刻意不做）：不建任务类型/队列/状态机；不联网下载模型与音色（私有化机器可能无外网）。
// =============================================
package fileproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ttsPythonModules TTS 真实依赖的**导入名**清单（与 tts_speak.py 的 MODULES 同源维护，
// 两侧改一侧必红在 capabilities_p0_test.go 的协议断言上）。
//
// ★ 2026-10-16 校正（本批真踩）：这里必须是**装机引擎真正 import 的东西**。
//
//	现役引擎＝sherpa-onnx 中英档（MeloTTS zh_en），只需 sherpa_onnx + soundfile + lameenc + numpy。
//	旧清单里的 onnxruntime / transformers / resemble_perth / pkuseg / pykakasi 属于**尚未落地的
//	Chatterbox 驱动**：留在探测里会让就绪位**恒 false**（pkuseg 这个 2018 年的包在 Python 3.12
//	上连 wheel 都构建不出来，本批实测）——探测与引擎各说一套，就是"永远点不亮"的形态。
//	Chatterbox 驱动落地时必须把那份清单**同时**加回本处与脚本侧（同源锁会拦只改一侧），
//	届时 PerTh 水印（D11）随之生效；现役中英档无水印，见 assets/tts/README.md。
var ttsPythonModules = []string{"sherpa_onnx", "soundfile", "lameenc", "numpy"}

// ttsCoveredLangs 现役语音模型覆盖的目标语种（与 tts_speak.py 的 LANGS 同源维护，逐项等值）。
//
// ★ 2026-10-16 扩档：中英档（MeloTTS zh_en）＋ 多语档（Supertonic-3 int8，八档 ja/ko/fr/de/es/pt/ru/ar）。
// 方案 §3.3 的语种路由是「zh/en → 轻量档；其余 → 多语模型」，所以这份清单是**能力边界**不是"临时数字"：
// 越界语种（th 是唯一一条：Piper 48 语与 Supertonic 31 语均无泰语）由脚本回 ERR:UnsupportedLang
// → api 层映射 unsupported_lang → 前端按 12 语种词典说一句「该语种暂不支持语音播放，译文照常」。
// 分档（谁走根目录、谁走 supertonic 子目录）见 ttsMeloLangs / ttsSupertonicLangs。
var ttsCoveredLangs = []string{"zh", "en", "ja", "ko", "fr", "de", "es", "pt", "ru", "ar"}

// ttsMeloLangs 走 `assets/tts/` **根目录**（MeloTTS zh_en）的语种，与 tts_speak.py 的 MELO_LANGS 同源（锁 L5）。
var ttsMeloLangs = []string{"zh", "en"}

// ttsSupertonicFiles Supertonic-3 多语档的**七件套文件名**（与脚本 SUPERTONIC_FILES 逐项等值，锁 L5）。
//
// ★ 为什么 Go 侧也要有一份：就绪/覆盖判据要能**现读资产**（/api/health 的 tts_langs 按语种如实收门），
// 而探测不能为了看一眼文件就起一个 Python 子进程（healthcheck 是每进程一次，但 server 侧还要按语种算）。
// 两份清单由锁 L5 钉住——「名字抄了一份在 Go」这件事由机械闸兜底，不靠记得同步。
var ttsSupertonicFiles = []string{
	"duration_predictor.int8.onnx",
	"text_encoder.int8.onnx",
	"vector_estimator.int8.onnx",
	"vocoder.int8.onnx",
	"tts.json",
	"unicode_indexer.bin",
	"voice.bin",
}

// ttsMinWeightBytes 权重体量下限（与脚本 MIN_WEIGHT_BYTES 同值）：低于它的 *.onnx 一律不当模型看
// （HF 仓的 git-lfs 指针文件是 133 字节纯文本，纯存在性检查会被它骗过）。
const ttsMinWeightBytes = 1_000_000

// ttsInList 线性判成员（清单都在十项以内，不值得引 map）。
func ttsInList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ttsSupertonicLangs 多语档语种＝声明清单 − MeloTTS 档（**派生**，不新抄一份字面清单：
// 新增语种只动 ttsCoveredLangs 一处，忘改派生集这种事不可能发生）。
func ttsSupertonicLangs() []string {
	out := make([]string, 0, len(ttsCoveredLangs))
	for _, lc := range ttsCoveredLangs {
		if !ttsInList(ttsMeloLangs, lc) {
			out = append(out, lc)
		}
	}
	return out
}

// TtsCoveredLangs **这台机器真正能合成**的语种清单（供 /api/health 的 tts_langs 出栈，资产现读现算）。
//
// ★ 为什么出"有效档"而不是"声明档"：语音是**按语种**的能力，前端据这份清单逐语种收门
// （AGENTS §一·17：false ⇒ 不渲染朗读键）。声明覆盖十档而机器只投了中英档时，
// 界面会给日/韩访客一颗**点下去必然失败**的朗读键——那正是该条判为缺陷的形态。
// 缺 supertonic 七件套 ⇒ 只回 zh/en；两台都齐 ⇒ 十档。
// 返回**副本**：调用方改不了这份事实源（同 §一·3「单一事实源」的写法）。
func TtsCoveredLangs() []string { return ttsEffectiveLangs() }

// ttsEffectiveLangs 按两条路线各自的资产在场情况，把声明清单收敛成有效清单。
func ttsEffectiveLangs() []string {
	return ttsEffectiveLangsFor(ttsAssetsReady(), ttsSupertonicAssetsReady())
}

// ttsEffectiveLangsFor 判据本体（**纯函数**：测试喂两档真假即可覆盖四种组合，不必动磁盘）。
// 路由口径与脚本 model_route 同源：MeloTTS 档要 ttsAssetsReady，其余要 supertonic 资产齐。
func ttsEffectiveLangsFor(meloReady, supertonicReady bool) []string {
	out := make([]string, 0, len(ttsCoveredLangs))
	for _, lc := range ttsCoveredLangs {
		if ttsInList(ttsMeloLangs, lc) {
			if meloReady {
				out = append(out, lc)
			}
			continue
		}
		if supertonicReady {
			out = append(out, lc)
		}
	}
	return out
}

// ttsLangCovered 判一个目标语种有没有语音。
// 归一化口径与脚本的 norm_lang **同一把尺子**：小写、剥掉地域段（zh-hant / zh_Hant → zh）。
//
//	▲ 两侧各写一套归一化必然漂（历史形态：前端送 zh_hant、后端按 zh-hant 判，边界永远差一档），
//	  所以这条函数的**行为等值**也由 tts_lang_coverage_gate_test.go 拿同一张表去比对脚本实现。
//
// 空串放行：调用方没送语种（老客户端、脚本直调）时按默认音色试；把"没送参数"判成"不支持"
// 等于给可用性交押金（同 §一·10 公开接口新增防护一律软档）。
func ttsLangCovered(lang string) bool {
	lc := strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lc, "-_"); i >= 0 {
		lc = lc[:i]
	}
	if lc == "" {
		return true
	}
	for _, c := range ttsCoveredLangs {
		if c == lc {
			return true
		}
	}
	return false
}

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
			return false, "Python " + m + " 库未安装，译文语音合成（TTS）不可用（pip install sherpa-onnx soundfile lameenc numpy）"
		}
	}
	if !ttsRoutesReady() {
		return false, "TTS 模型资产缺失（assets/tts/ 或 assets/tts/supertonic/ 未随包落盘），译文语音合成不可用，取权重步骤见 assets/tts/README.md"
	}
	if !ttsVoiceReady() {
		return false, "TTS 默认音色未配置（assets/tts/voices/" + ttsVoiceFile + " 缺失），译文语音合成不可用，投放步骤见 assets/tts/voices/README.md"
	}
	return true, ""
}

// ttsAssetsReady **中英档**（MeloTTS，assets/tts/ 根目录）权重是否落盘。
func ttsAssetsReady() bool {
	matches, err := filepath.Glob(filepath.Join(ttsAssetsDir(), ttsAssetGlob))
	return err == nil && len(matches) > 0
}

// ttsRoutesReady 任一语音路线就绪（中英档或**多语档**）即可让 TtsAvailable 为真。
// ★ 为什么不是"两条都齐才算就绪"：中英档可用而多语档缺件时，"服务器有 TTS" 这件事不假，
// 缺的只是那八档——按语种收门由 tts_langs 承担（见 TtsCoveredLangs）。
// 反过来只看中英档也有缺陷：某天只投了多语档，就绪位会谎报 false 把可用的 TTS 摘掉。
func ttsRoutesReady() bool { return ttsAssetsReady() || ttsSupertonicAssetsReady() }

// ttsSupertonicAssetsReady 多语档七件套是否齐（逐件存在且**非空**，onnx 另加体量闸）。
// 非空/体量判定不能省：LFS 指针或零字节文件同样让合成失败，纯存在性检查会被它们骗过（D5 同族）。
func ttsSupertonicAssetsReady() bool {
	dir := ttsSupertonicDir()
	for _, n := range ttsSupertonicFiles {
		info, err := os.Stat(filepath.Join(dir, n))
		if err != nil || info.Size() == 0 {
			return false
		}
		if strings.HasSuffix(n, ".onnx") && info.Size() < ttsMinWeightBytes {
			return false
		}
	}
	return true
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

// ttsSupertonicDir 定位多语档模型目录（assets/tts/supertonic/，七件套随资产树同步）。
func ttsSupertonicDir() string {
	return filepath.Join(ttsAssetsDir(), "supertonic")
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

// ===== P2 工单侧：多段批量合成（音频工单的「译文配音」产物） =====

// ttsBatchTimeout 批量合成的墙钟预算。与单段 60s 分开设：
// 单段那条线按"HTTP 请求等得起"定，批量这条线跑在工单后台任务里，没有浏览器在等，
// 但 Chatterbox 0.5B 在 CPU 上 1~3s/句，一段 10 分钟的音轨可能有上百段 ⇒ 默认放到 900s
// （与 asrTimeout 同档，两条腿都是"识别/合成一整条音轨"的量级）。
func ttsBatchTimeout() time.Duration {
	if v := os.Getenv("TTS_BATCH_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 900 * time.Second
}

// SpeakLines 把多段译文按顺序合成并缝合到 outPath（wav 或 .mp3，由后缀决定）。
// ★ 译文第一（最高优先级）：与 SpeakToWav 同一纪律——返回错误只代表"这次没有语音"，
//
//	调用方必须据此**跳过音频继续交付译文**，绝不能把翻译结果一起判死。
//
// ★ 为什么不复用 SpeakToWav 循环 N 次：模型加载才是耗时大头（ONNX 权重 + 分词器），
//
//	N 次调用就付 N 次加载；而且 N 个临时 wav 还要在 Go 侧拼音频——那是把音频工序
//	搬进 Go。改为脚本侧一次加载、逐段合成、静音缝合，Go 只负责把段列表喂过去。
//
// ★ 段列表走临时文件而不是命令行：行译文可很长（命令行有长度上限），
//
//	且 argv 会出现在 `ps` 里把客户内容漏给同机其他进程（同 RenderTranslatedImage 的理由）。
func SpeakLines(ctx context.Context, lines []string, lang, outPath string) error {
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			kept = append(kept, ln)
		}
	}
	if len(kept) == 0 {
		return &TTLError{Reason: TtsReasonEmpty, Message: "没有可朗读的内容"}
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
	tmp, err := os.CreateTemp("", "tts-lines-*.json")
	if err != nil {
		return &TTLError{Reason: TtsReasonFailed, Message: "TTS 临时文件创建失败: " + err.Error(), Err: err}
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if encErr := json.NewEncoder(tmp).Encode(kept); encErr != nil {
		tmp.Close()
		return &TTLError{Reason: TtsReasonFailed, Message: "TTS 段列表写出失败: " + encErr.Error(), Err: encErr}
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return &TTLError{Reason: TtsReasonFailed, Message: "TTS 临时文件关闭失败: " + closeErr.Error(), Err: closeErr}
	}
	argv := []string{ttsScriptPath(), "speakfile", tmpPath, "--out", outPath}
	if strings.TrimSpace(lang) != "" {
		argv = append(argv, "--lang", strings.TrimSpace(lang))
	}
	bin, args := wrapNice(pyBin(), argv)
	_, stderr, rerr := runSubprocess(ctx, ttsBatchTimeout(), bin, args, nil)
	if rerr != nil {
		if variant := ttsErrVariant(string(stderr)); variant != "" {
			return &TTLError{Reason: ttsReasonForVariant(variant), Message: friendlyTtsError(string(stderr)), Err: rerr}
		}
		return &TTLError{Reason: TtsReasonFailed, Message: fmt.Sprintf("TTS 合成失败: %v\n%s", rerr, truncateTail(stderr)), Err: rerr}
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

// DubContainerFor 配音产物的**容器**后缀（"译后音频回到原文件格式"口径，2026-10-16 用户令）。
//
// 输入什么容器就回什么容器：wav 由脚本原生落盘，mp3/ogg/opus/m4a/aac/flac 由脚本侧
// ffmpeg 转码（参数表在 tts_speak.py 的 CONTAINER_ARGS，两侧逐项等值由
// tts_lang_coverage_gate_test.go 锁住——**不许**在这里再抄一份"支持哪些后缀"的独立判断，
// 两份尺子迟早打架，见 AGENTS §一·5 报价单一事实源那族）。
//
// 表外容器一律回 .mp3：宁可产物容器与原件不同，也不许不出声
// （音频入口白名单里只有这 7 种后缀，走到 default 说明上游形态变了，此时"有声音"优先）。
func DubContainerFor(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".wav", ".mp3", ".ogg", ".opus", ".m4a", ".aac", ".flac":
		return strings.ToLower(strings.TrimSpace(ext))
	default:
		return ".mp3"
	}
}

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
