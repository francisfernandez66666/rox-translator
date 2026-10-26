// ============ 本文件职责中文说明 ============
// ★ 2026-10-26 用户令「回显闸误杀，改成『源文含特征词即豁免』，并补一条以这三段为靶的等值锁」。
//
// 靶子取自当日云端 UAT BUG-01（工单 T20261026141842NO7，mode=fast，源文 3 段 → 译文 2 段）：
// 丢的恰是第三段「金额与币种必须准确呈现，默认按人民币计」——它在现网被判成指令回显而整段不写入。
// 现读读数（本机同一把尺子复算）：源文与指令语料的 4-gram 重合度 **0.0**，
// 于是旧那条「srcCov ≥ 0.60 整段豁免」结构性够不到；而它的**正确**英译里必然出现
// RMB／monetary amounts／currency 这些**我们尾注自己的用词**，于是被特征词差分腿与重合度腿轮流打死
// （同一句源文投六条候选译文，四条判回显）。
//
// 本文件按用户口径钉三件事，一条都不许省：
//
//	T1 三段源文的**正确译文逐条判可用**（等值锁，逐段点名，不做「只要有一条过」的糊弄判据）；
//	T2 第三段的豁免**必须由新腿给出**：现读 sourceHasEchoTopic(src3)=真、srcCov<0.60（旧腿够不到），
//	   并逐字钉住「摘掉豁免这条腿它就判回显」的反证形态（变异体在同一文件里现算，不靠注释声称）；
//	T3 真回显仍判不可用（源文没讲这些事、译文却把尾注译成英文）——证明豁免没有把整条闸变成摆设。
//
// 方言：本文件不碰库、不打上游（纯判据）。
// 运行：go test ./internal/engine/ -run TestInstructionEchoTopicExempt
// ============================================
package engine

import (
	"context"
	"strings"
	"testing"
)

// 三段源文逐字取自 UAT evidence/D_ticket/probe_D2_segments.json 的 create 请求体。
const (
	uatSeg1Src = "第一段：这是一段用于端到端验收的中文原文，请翻译成英文。"
	uatSeg2Src = "第二段：本平台的差异化不在单句翻译，而在于把整件事办完。"
	// uatSeg3Src 就是现网整段丢失的那一句——它既在讲「金额与币种」，
	// 又落在我们货币尾注的特征词族里，是本轮豁免的唯一真靶。
	uatSeg3Src = "第三段：金额与币种必须准确呈现，默认按人民币计。"
)

// 三段各自**正确**的译文形态（现网模型实际给出的那几条同族写法，逐条都必须留下）。
var uatSegGoodOuts = []struct {
	src string
	out string
}{
	{uatSeg1Src, "This is a Chinese text intended for end-to-end acceptance testing. Please translate it into English."},
	{uatSeg1Src, "Paragraph 1: This is a Chinese source text used for E2E acceptance; please translate it into English."},
	{uatSeg2Src, "The differentiation of this platform lies not in single-sentence translation but in getting the whole job done."},
	{uatSeg2Src, "Paragraph 2: What sets this platform apart is not translating individual sentences, but completing the entire task."},
	{uatSeg3Src, "The third paragraph: amounts and currencies must be presented accurately, by default in RMB."},
	{uatSeg3Src, "Paragraph 3: Monetary amounts and currency must be accurately presented, defaulting to RMB."},
	{uatSeg3Src, "Third paragraph: monetary amounts and currencies must be accurately presented, and the default currency is RMB."},
	{uatSeg3Src, "The third paragraph: amounts must keep their original currency and default to RMB."},
	{uatSeg3Src, "Paragraph three: figures and currency symbols must be faithfully presented, in RMB by default."},
	// 中文界面下的英文尾注形态同一条源文也不能被杀（品牌名/说明文本里出现 Renminbi 属如实翻译）。
	{uatSeg3Src, "Paragraph 3: Monetary amounts must be faithfully rendered in Renminbi unless the source states another currency."},
}

// TestInstructionEchoTopicExemptKeepsUatSegments T1：三段逐条判可用（fileTranslationUsable 同一把尺子）。
func TestInstructionEchoTopicExemptKeepsUatSegments(t *testing.T) {
	e := &Engine{}
	for _, uiLang := range []string{"zh", "en"} {
		ctx := WithUILang(context.Background(), uiLang)
		for i, c := range uatSegGoodOuts {
			// 三条判据腿各自单独判一遍：统一判据绿不能证明是「哪一条腿放行的」。
			if isProseInstructionEcho(c.src, c.out, "en", uiLang) {
				t.Fatalf("T1[%d/%s] 正确译文被判指令回显：src=%q out=%q", i, uiLang, c.src, c.out)
			}
			if hasLongTextExpansion(c.src, c.out) {
				t.Fatalf("T1[%d/%s] 正确译文被判长文爆炸：src=%q out=%q", i, uiLang, c.src, c.out)
			}
			if !e.fileTranslationUsable(ctx, c.src, c.out, "en") {
				t.Fatalf("T1[%d/%s] 统一判据把正确译文挡在写入点之外：out=%q", i, uiLang, c.out)
			}
		}
	}
}

// TestInstructionEchoTopicExemptIsTheLegThatFired T2：豁免的**归属**与射程。
//  1. 第三段命中新腿（sourceHasEchoTopic 真）、旧腿（srcCov≥0.60）不够——否则本文件测的是旧豁免；
//  2. 现读现算反证：把「源文含特征词」这一档摘掉（只留旧 srcCov 腿）后，六条候选里必须**至少一条**
//     翻成判回显。这条反证是豁免的射程自证：若摘掉腿仍然全绿，说明 T1 靠的是别的腿，
//     本批改动压根没生效（本仓老坑：正向判据落在只有失败分支才会写的字面上）。
func TestInstructionEchoTopicExemptIsTheLegThatFired(t *testing.T) {
	corpus := normalizeEchoText(instructionCorpus("en", "zh"))
	srcCov := gramCoverage(normalizeEchoText(uatSeg3Src), corpus)
	if !sourceHasEchoTopic(uatSeg3Src) {
		t.Fatalf("T2 第三段没命中特征词组——豁免腿压根不覆盖现网这一形态（srcCov=%.3f）", srcCov)
	}
	if srcCov >= 0.60 {
		t.Fatalf("T2 第三段 srcCov=%.3f 已在旧整段豁免射程内，本文件测不到新腿", srcCov)
	}
	// 反证：只按旧腿判（等价于把 sourceHasEchoTopic 那一档摘掉）。
	killed := 0
	for _, c := range uatSegGoodOuts {
		if c.src != uatSeg3Src {
			continue
		}
		if legacyEchoWithoutTopicExempt(c.src, c.out, corpus) {
			killed++
		}
	}
	if killed == 0 {
		t.Fatal("T2 反证空转：摘掉豁免腿后第三段的六条正确译文仍全判假——豁免没有射程，T1 绿灯不是本批改动给的")
	}
	t.Logf("T2 读数：srcCov=%.3f，摘掉豁免腿后被杀的第三段正确译文=%d/%d", srcCov, killed, len(uatSeg3Candidates()))
}

// legacyEchoWithoutTopicExempt 是「本批改动之前」的那把尺子（没有 sourceHasEchoTopic 那一档）。
// 只在反证里用，判据其余部分与 isProseInstructionEcho 逐字同形——这里复制是有意的：
// 引真实函数就没法证明「摘掉的是哪一条腿」。
func legacyEchoWithoutTopicExempt(src, out, corpusN string) bool {
	outN := normalizeEchoText(out)
	if len([]rune(outN)) < 12 {
		return false
	}
	outCov := gramCoverage(outN, corpusN)
	srcCov := gramCoverage(normalizeEchoText(src), corpusN)
	if srcCov >= 0.60 {
		return false
	}
	if outCov >= 0.55 && outCov-srcCov >= 0.30 {
		return true
	}
	if longestGramRun(outN, corpusN) >= instrEchoRunFloorGrams && outCov-srcCov >= 0.20 {
		return true
	}
	lowOut, lowSrc := strings.ToLower(out), strings.ToLower(src)
	for _, m := range instructionEchoMarkers {
		if strings.Contains(lowOut, strings.ToLower(m)) && !strings.Contains(lowSrc, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

func uatSeg3Candidates() []string {
	var s []string
	for _, c := range uatSegGoodOuts {
		if c.src == uatSeg3Src {
			s = append(s, c.out)
		}
	}
	return s
}

// TestInstructionEchoTopicExemptStillCatchesRealEcho T3：真回显（源文没讲这些事）必须仍判不可用。
// 靶子逐字取 DEF-01 现网样本（instruction_echo_test.go 的 echoSampleSrc／echoSampleOut），
// 再加两条「同一段非货币源文 + 尾注英译」的变体——防止豁免按译文形状反向放行。
func TestInstructionEchoTopicExemptStillCatchesRealEcho(t *testing.T) {
	e := &Engine{}
	if sourceHasEchoTopic(echoSampleSrc) {
		t.Fatalf("T3 靶子源文被判定为「本来就在讲这些事」，它就不再是真回显样本了：%q", echoSampleSrc)
	}
	if !isProseInstructionEcho(echoSampleSrc, echoSampleOut, "en", "zh") {
		t.Fatal("T3 DEF-01 真回显样本经豁免后判假——整条闸被豁免变成摆设")
	}
	if e.fileTranslationUsable(context.Background(), echoSampleSrc, echoSampleOut, "en") {
		t.Fatal("T3 真回显被统一判据放行，会像 2026-10-24 那样写进 langTranslations 交付给客户")
	}
	// 另一段与货币／复述／附注都无关的源文，配上整段尾注英译，同样必须判回显。
	offTopicSrc := "本次验收样例只包含产线巡检记录与设备编号两列内容。"
	if sourceHasEchoTopic(offTopicSrc) {
		t.Fatalf("T3 反例源文意外命中特征词组：%q", offTopicSrc)
	}
	if !isProseInstructionEcho(offTopicSrc, echoSampleOut, "en", "zh") {
		t.Fatal("T3 无关源文＋尾注英译没被判回显——重合度/最长命中腿失去射程")
	}
}
