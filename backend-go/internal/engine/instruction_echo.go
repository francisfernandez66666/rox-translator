// ============ 本文件职责中文说明 ============
// DEF-01（2026-10-24 云端 UAT）：文件工单「指令回显」判据。
//
// 现网形态：上传 4 行中文 txt，Hunyuan-MT-7B 把 system 里的货币保真/纯度尾注
// （currencyInstructionNote / purityInstructionNote）当成待译内容，输出成两段英文「规范说明」，
// 真实第 2/3 行被替代；IsTranslationUsable 的现有判据（同文/尖括号/回绝/短格爆炸）全部放行，
// 回显被写进 langTranslations，硬闸按「键存在=已译出」跳过，最终 1,037 积分的坏交付。
//
// 本判据三条互补（宁可判缺失走极简指令重译，也不把回显交给客户）：
//  1. 与「本目标语/中英两套提示词」的字符 4-gram 重合度差分（译文像指令、源文不像）；
//  2. 指令特征词（Renminbi/币种/不得复述原文…）在译文出现而源文没有；
//  3. 长源文（>12 rune）的译文长度爆炸（>max(6×源文, 200)）——语言无关的兜底网。
//
// ============================================
package engine

import (
	"context"
	"os"
	"strings"
	"sync"
	"unicode"
)

// instructionCorpusCache 指令语料缓存（key=targetLang|uiLang），只生成一次。
var instructionCorpusCache sync.Map // key: targetLang|uiLang → string

// instructionEchoMarkers 来自本系统自己的两条提示词尾注（中英两套）的特征词。
// 只用于「源文没有、译文却有」的差分判定，不会误杀正常翻译这些词的正文。
var instructionEchoMarkers = []string{
	"renminbi", "currency symbol", "do not repeat the original text",
	"do not annotate original words", "currency rule", "monetary amounts",
	"人民币", "币种符号", "金额币种保真", "不得复述原文", "不得以括号",
}

// instructionCorpus 取本目标语在「中文界面提示词 + 英文界面提示词」两套指令下的全文。
// 现网回显是「中文尾注被翻成英文」——英文那一套正好覆盖该形态；中文那一套覆盖原样回吐。
func instructionCorpus(targetLang, uiLang string) string {
	key := targetLang + "|" + uiLang
	if v, ok := instructionCorpusCache.Load(key); ok {
		return v.(string)
	}
	zh := translateInstruction("", targetLang, "zh") + outputContractNote("zh")
	en := translateInstruction("", targetLang, "en") + outputContractNote("en")
	corpus := zh + "\n" + en
	instructionCorpusCache.Store(key, corpus)
	return corpus
}

// normalizeEchoText 只留字母/数字并转小写：去掉标点/空白/项目符号，避免格式差异影响 4-gram。
func normalizeEchoText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// gramCoverage 计算 text 的 4-gram 有多大比例出现在 corpus 中（0~1）。
// 用「gram 是否为 corpus 子串」而不是集合交集：中文/英文分词差异下更稳，也避免建大 map。
func gramCoverage(text, corpus string) float64 {
	t := []rune(text)
	if len(t) < 4 || corpus == "" {
		return 0
	}
	total, hit := 0, 0
	for i := 0; i+4 <= len(t); i++ {
		total++
		if strings.Contains(corpus, string(t[i:i+4])) {
			hit++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

// longestGramRun 最长连续命中 4-gram 的字符长度（近似「整句照抄」）。
func longestGramRun(text, corpus string) int {
	t := []rune(text)
	best, run := 0, 0
	for i := 0; i+4 <= len(t); i++ {
		if strings.Contains(corpus, string(t[i:i+4])) {
			if run == 0 {
				run = 4
			} else {
				run++
			}
			if run > best {
				best = run
			}
		} else {
			run = 0
		}
	}
	return best
}

// instrEchoRunFloorGrams 「最长连续命中 4-gram」那条分支的字符地板。
//
// ★ 为什么从 12 抬到 20（2026-10-25 管线集成测实测出来的**判据过杀**，不是收紧不够）：
// 归一化会剥掉空格，于是**一个常见领域词**就能凑够 12 字符——现读样本：
// 源文「支持多格式文件翻译」的正常译文 "Multi-format file translation is supported"
// 最长命中 13（"translationi"，来自我们自己英文尾注里的 translation/translated），
// 而它整段与指令语料的 4-gram 重合度只有 0.353，其余字节全是源文带来的实义内容。
// 本判据要抓的是**整句照抄**，代价对比：
//   - 判过杀 ⇒ 这一段的正确译文被丢弃、极简重译腿再丢一次，最终**原文当未译出交付**（现网就是这一形态）；
//   - 判漏一截 ⇒ 重合度那条分支（outCov≥0.55）与特征词差分那条分支仍然在拦。
//
// 真回显样本（尾注整段译成英文，204 归一字符）的最长命中实测 **27**，与 13 有足够间距，
// 故地板取 20（≈ 连续 3~4 个英文词照抄）；两边读数都由
// instruction_echo_overreach_test.go 逐字钉住，改判据必须同时改那两条锁。
const instrEchoRunFloorGrams = 20

// isProseInstructionEcho 判断候选译文是否是「指令正文的翻译/回吐」。
func isProseInstructionEcho(src, out, targetLang, uiLang string) bool {
	srcN := normalizeEchoText(src)
	outN := normalizeEchoText(out)
	if len([]rune(outN)) < 12 {
		return false
	}
	corpus := normalizeEchoText(instructionCorpus(targetLang, uiLang))
	outCov := gramCoverage(outN, corpus)
	srcCov := gramCoverage(srcN, corpus)
	// 源文本身就是讲翻译规范/货币规则：整条豁免（4-gram 差分也允许它高）
	if srcCov >= 0.60 {
		return false
	}
	if outCov >= 0.55 && outCov-srcCov >= 0.30 {
		return true
	}
	if longestGramRun(outN, corpus) >= instrEchoRunFloorGrams && outCov-srcCov >= 0.20 {
		return true
	}
	lowOut, lowSrc := strings.ToLower(out), strings.ToLower(src)
	for _, m := range instructionEchoMarkers {
		if strings.Contains(lowOut, m) && !strings.Contains(lowSrc, m) {
			return true
		}
	}
	return false
}

// hasLongTextExpansion 长源文（>12 rune）的长度爆炸兜底：语言无关地抓「一段 30 字被吐成 350 字说明」。
func hasLongTextExpansion(src, out string) bool {
	sr := len([]rune(strings.TrimSpace(src)))
	or := len([]rune(strings.TrimSpace(out)))
	if sr <= 12 || or == 0 {
		return false // ≤12 由 hasLengthExplosion 管
	}
	limit := 6 * sr
	if limit < 200 {
		limit = 200
	}
	return or > limit
}

// echoGuardOff 指令回显判据是否被关到"仅保留现有可用性"这一应急档。
// 档位＝env `FILE_ECHO_GUARD` ＞ `system_config.file_echo_guard` ＞ 默认开（方案 §10.1.1 那一行）。
// 只有字面 0 关（含脏值在内的一律保持开）：把打错的一个字符读成"关掉回显闸"＝
// 整条文件链把提示词回显当译文写进产物，而管理台看上去"我只是改了个配置"。
// ⚠️ 原先只读 env ⇒ 运营在管理台填 0 完全不生效，必须重启进程才关得掉，
// 与方案里「env ＞ system_config ＞ 代码默认」的三级口径不符（本批对账补齐）。
func (e *Engine) echoGuardOff() bool {
	if v := strings.TrimSpace(os.Getenv("FILE_ECHO_GUARD")); v != "" {
		return v == "0"
	}
	if e != nil && e.St != nil {
		if v, _ := e.St.GetConfig("file_echo_guard"); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v) == "0"
		}
	}
	return false
}

// fileTranslationUsable 文件全链路写入点的统一判据：现有可用性 + 指令回显 + 长文爆炸。
// 应急档（FILE_ECHO_GUARD=0 或 system_config.file_echo_guard=0）仅保留现有可用性判据。
func (e *Engine) fileTranslationUsable(ctx context.Context, src, out, targetLang string) bool {
	if !IsTranslationUsable(src, out) {
		return false
	}
	if e.echoGuardOff() {
		return true
	}
	ui := uiLangFromCtx(ctx)
	if isProseInstructionEcho(src, out, targetLang, ui) {
		return false
	}
	if hasLongTextExpansion(src, out) {
		return false
	}
	return true
}

// TextTranslationUsable 文本链路（逐行翻译/重发）可用的导出判据：
// 与文件链路同一把尺子（IsTranslationUsable + 指令回显 + 长文爆炸），避免文本工单再走一套。
func (e *Engine) TextTranslationUsable(ctx context.Context, src, out, targetLang string) bool {
	return e.fileTranslationUsable(ctx, src, out, targetLang)
}

// TranslateMinimalLine 文本链路逐行补译的导出入口（极简指令，供工单重发只补缺失/错误行）。
func (e *Engine) TranslateMinimalLine(ctx context.Context, src, targetLang string) (string, bool) {
	return e.translateSegmentMinimal(ctx, src, targetLang)
}
