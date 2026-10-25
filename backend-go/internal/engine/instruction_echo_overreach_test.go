// ============ 本文件职责中文说明 ============
// DEF-01 判据的**反向**锁：指令回显判据不得误杀正常译文。
//
// 现网形态（2026-10-25 文件管线集成测抓到）：上传「支持多格式文件翻译」这类含「翻译」二字的
// 短标签，模型给出的正确译文 "Multi-format file translation is supported" 被
// isProseInstructionEcho 判成回显 ⇒ 不写入 langTranslations ⇒ 极简重译腿**同一把尺子**再丢一次
// ⇒ 交付物里这一段是中文原文（Untranslated=1）。
// 根因：归一化剥掉空格后，**一个常见领域词**就能凑够 12 字符的「最长连续命中」
// （"translation is" 归一化成 translationis，而我们自己的英文尾注里满是 translation/translated）；
// 而本产品的源文里有相当比例正是在讲「翻译/文件翻译/术语翻译」。
// 判过杀的代价是把**真译文**丢掉，判漏的代价是把回显交给客户——两边都要钉，所以这个文件同时锁：
//
//	O1 现网被误杀的那一条：判假（且写入点统一判据 fileTranslationUsable 为真）；
//	O2 四条正常译文逐条判假（含被误杀那条）；
//	O3 抬高地板后「整句照抄」腿**仍然有射程**：构造一段重合度不足 0.55、且不含任何特征词的
//	   逐字抄写样本，必须仍判回显（否则就等于把这一腿摘了）；
//	O4 间距自证：O1 的最长命中实测值必须**严格小于**地板、O3 的必须**严格大于等于**地板，
//	   两边现读现算。缺这一条，将来改归一化/改语料把间距抹平也无人报警（判据恒真是本仓老坑）。
//
// 方言：本文件不碰库、不打上游。
// 运行：go test ./internal/engine/ -run 'TestInstructionEcho(Overreach|RunLeg|RunFloor)'
// ============================================
package engine

import (
	"context"
	"strings"
	"testing"
)

// echoFP_* 现网被误杀的样本（源文含「翻译」，译文里必然出现 translation）。
const (
	echoFPSrc = "支持多格式文件翻译"
	echoFPOut = "Multi-format file translation is supported"
)

// echoRunOnlyOut 逐字照抄指令语料的一段，**刻意**做两件事把另两条分支引开：
//  1. 前面那截取自英文界面提示词「without appending any back-translation of the result,
//     without extra explanations」（连续命中远长于地板）；
//  2. 后面接一大段与语料无关的实义内容，把整条 4-gram 重合度压到 0.55 以下，
//     且全句不含 instructionEchoMarkers 里任何一个特征词。
//
// ⇒ 这一条只可能由「最长连续命中」腿抓住，是对抬地板最直接的射程自证。
const echoRunOnlyOut = "without appending any back-translation of the result, without extra explanations. " +
	"The maintenance log records every calibration event with the operator badge identifier " +
	"and the torque reading noted on line seven of the workshop sheet filed last Thursday."

// echoGoodOuts 极简腿给出的正常译文形态（与 file_echo_pipeline_test.go 的 fixture 同源）。
var echoGoodOuts = []string{
	"The system automatically matches the built-in automotive database",
	echoFPOut,
	"Current: all allowed; position left blank/0 = use the global single-ticket amount",
	"Please calibrate the device within the maintenance cycle",
}

func echoCorpusForTest() string {
	return normalizeEchoText(instructionCorpus("en", ""))
}

// TestInstructionEchoOverreachKeepsLegitTranslation O1：被误杀的那一条必须判假。
func TestInstructionEchoOverreachKeepsLegitTranslation(t *testing.T) {
	if isProseInstructionEcho(echoFPSrc, echoFPOut, "en", "") {
		t.Fatalf("正常译文被当成指令回显（判过杀 ⇒ 这一段会以原文交付）：%q", echoFPOut)
	}
	// 写入点用的统一判据也必须放行（这是六处写入点真正调用的那一个函数）。
	if !(&Engine{}).fileTranslationUsable(context.Background(), echoFPSrc, echoFPOut, "en") {
		t.Fatalf("fileTranslationUsable 仍拒绝正常译文：%q", echoFPOut)
	}
}

// TestInstructionEchoOverreachKeepsAllNormalTranslations O2：逐条判假，缺一即红。
func TestInstructionEchoOverreachKeepsAllNormalTranslations(t *testing.T) {
	for i, src := range []string{
		"系统自动匹配内置汽车数据库",
		echoFPSrc,
		"当前：全部允许·仓位留空/0 =使用全局单票金额",
		"请在维护周期内校准设备",
	} {
		out := echoGoodOuts[i]
		if isProseInstructionEcho(src, out, "en", "") {
			t.Errorf("第 %d 条正常译文被误判为回显 src=%q out=%q", i+1, src, out)
		}
		if !IsTranslationUsable(src, out) {
			t.Errorf("第 %d 条连基础可用性判据都过不了（那是另一条腿的问题，本文件只钉回显）：%q", i+1, out)
		}
	}
}

// TestInstructionEchoRunLegStillHasRange O3：抬地板后「整句照抄」腿仍必须抓住回显。
func TestInstructionEchoRunLegStillHasRange(t *testing.T) {
	src := "系统自动匹配内置汽车数据库"
	if !isProseInstructionEcho(src, echoRunOnlyOut, "en", "") {
		t.Fatalf("逐字抄指令的样本没被判回显 ⇒ 「最长连续命中」腿已被抬地板抬成死腿：%q", echoRunOnlyOut)
	}
	// 反向自证：这一条**不能**是靠重合度或特征词过的，否则 O3 测的不是 run 腿。
	outN := normalizeEchoText(echoRunOnlyOut)
	corpus := echoCorpusForTest()
	if gramCoverage(outN, corpus) >= 0.55 {
		t.Fatalf("样本的重合度已到 %.3f，重合度那条分支会先命中 ⇒ O3 判不到 run 腿，需加长非语料那一段",
			gramCoverage(outN, corpus))
	}
	low := strings.ToLower(echoRunOnlyOut)
	for _, m := range instructionEchoMarkers {
		if strings.Contains(low, m) {
			t.Fatalf("样本里含特征词 %q ⇒ 特征词那条分支会先命中，O3 判不到 run 腿", m)
		}
	}
}

// TestInstructionEchoRunFloorGapIsReal O4：地板两侧的间距必须真实存在（现读现算）。
func TestInstructionEchoRunFloorGapIsReal(t *testing.T) {
	corpus := echoCorpusForTest()
	fpRun := longestGramRun(normalizeEchoText(echoFPOut), corpus)
	tpRun := longestGramRun(normalizeEchoText(echoRunOnlyOut), corpus)
	if fpRun >= instrEchoRunFloorGrams {
		t.Fatalf("被误杀样本的最长命中 %d 已 ≥ 地板 %d ⇒ 判据两侧没有间距，本文件的正向锁测不到任何东西",
			fpRun, instrEchoRunFloorGrams)
	}
	if tpRun < instrEchoRunFloorGrams {
		t.Fatalf("照抄样本的最长命中 %d < 地板 %d ⇒ O3 的绿灯来自别的分支，run 腿射程存疑", tpRun, instrEchoRunFloorGrams)
	}
	// 间距下限：只留 1 个字符的间距时，改一次归一化就会翻面（判据会静默换边）。
	if instrEchoRunFloorGrams-tpRun > 0 || tpRun-fpRun < 8 {
		t.Fatalf("地板 %d 与照抄样本 %d / 正常译文 %d 的相对位置不理想（间距 %d）",
			instrEchoRunFloorGrams, tpRun, fpRun, tpRun-fpRun)
	}
}
