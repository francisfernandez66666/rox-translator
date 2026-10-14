package api

// 本文件钉 countSentences 的切分判据（★ 2026-10-13 切分重做的机械锁）。
//
// 三条写法说明（为什么长这样）：
// ① 每条正测都配**同一句上的旧口径读数**做反向对照——"新判据给出 2 句"本身没有信息量，
//    只有"旧口径在这里给 6 句、新口径给 2 句"才同时证明「缺陷修了」与「锁有射程」
//    （AGENTS §三「正向判据不许落在只有失败分支才会写的字面上」同口径）。
// ② 另配一条**非退化锁**：往文本里真加一句必须涨读数。否则"把上下文判定写成永远不切"
//    这种退化形态对本文件全部等值判据毫无感觉（那才是真出事）。
// ③ 全角标点、`! ? ;` 与换行各钉一条"与旧口径读数相等"的等值锁——本批改的只有 ASCII 句点
//    那一维，这几维要是也跟着动了，就是超出必要性的改动，当场判红。

import (
	"strings"
	"testing"
)

// legacyCountSentences 是 2026-10-13 改造**之前**的字面实现（照抄，只用于反向对照）。
// 参数 text: 源文本。返回: 旧口径句数（把 ASCII 句点无差别当句末）。
func legacyCountSentences(text string) int64 {
	repl := strings.NewReplacer("\n", "。", "\r", "。", "。", "。", "！", "。", "？", "。",
		".", "。", "!", "。", "?", "。", ";", "。", "；", "。")
	segs := strings.Split(repl.Replace(text), "。")
	n := int64(0)
	for _, seg := range segs {
		if strings.TrimSpace(seg) != "" {
			n++
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

// scCase 一条切分用例：文本、期望的新口径读数。
type scCase struct {
	name string
	text string
	want int64
}

// TestCountSentencesAbbreviationAndCode 缺陷本体：缩写／股票代码／属性名里的点不是句末。
// 样本是现网截图那条真实输入（与预估批那条 estScreenshotText 同源），不是凑出来的语法标本。
func TestCountSentencesAbbreviationAndCode(t *testing.T) {
	real := "Root cause visible in the log: the ledger is keyed by req.Code (suffixed, e.g. 600912.SH), my test read the bare code. Fix the test keys to use the same withSuffix derivation"
	if got := countSentences(real); got != 2 {
		t.Fatalf("截图那条真件应切 2 句（唯一真句末在 \"bare code.\"），实得 %d", got)
	}
	if old := legacyCountSentences(real); old <= 2 {
		t.Fatalf("反向对照失效：旧口径必须在这条上显著多切（实得 %d），否则上面的等值判据证明不了任何东西", old)
	} else {
		t.Logf("同一条输入：改造前 %d 句／改造后 2 句（旧口径把 req.Code／e.g.／600912.SH 各切一刀）", old)
	}

	cases := []scCase{
		{"小数", "The rate is 6.5 and the build is 1.2.3 today", 1},
		{"域名与文件名", "Open https://a.com/x and read report.pdf now", 1},
		{"首字母缩写", "See U.S.A. policy and J. Smith notes", 1},
		{"省略号中段不刷屏", "Hmm... sure. Next", 3},
		{"真句末照常切", "First sentence. Second one. Third", 3},
		{"编号式引用不切", "Row No. 5 and Fig. 3 are fine. Done", 2},
		{"整句结尾的 No. 要切", "Are you sure? No. I disagree.", 3},
	}
	for _, c := range cases {
		if got := countSentences(c.text); got != c.want {
			t.Fatalf("%s: 期望 %d 句实得 %d 句（文本 %q）", c.name, c.want, got, c.text)
		}
	}
}

// TestCountSentencesKeepsOtherTerminators 改动面红线：除 ASCII 句点之外的边界行为一字未变。
// 判据＝与旧口径读数**相等**（这几维本批刻意不动；一旦有人顺手"优化"了分号或全角标点，这里判红）。
func TestCountSentencesKeepsOtherTerminators(t *testing.T) {
	samples := []string{
		"第一行换行第二行\n第三行",
		"中文全角。第二句！第三句？第四句；结束",
		"Latin bang! Question? Semicolon; tail",
		"混合 Mixed 一句。Two sentences. 三句！",
	}
	for _, s := range samples {
		if got, want := countSentences(s), legacyCountSentences(s); got != want {
			t.Fatalf("非句点维度的切分行为被改动了（旧 %d／新 %d）: %q", want, got, s)
		}
	}
}

// TestCountSentencesMonotonic 非退化锁：真加一句必须涨读数——
// 否则"上下文判定"退化成"永远不切"时，上面的等值判据照样全绿。
func TestCountSentencesMonotonic(t *testing.T) {
	base := "One sentence here"
	if countSentences(base) != 1 {
		t.Fatalf("单句基准读数应为 1，实得 %d", countSentences(base))
	}
	withTwo := base + ". And a second one"
	if got := countSentences(withTwo); got != 2 {
		t.Fatalf("补一个真句末后读数必须 2，实得 %d", got)
	}
	// 反证：把「点后紧跟拉丁字母就不切」这一条摘掉（退化回无差别切），截图案必须变响
	degenerate := legacyCountSentences(base + ". Second. Third. Fourth")
	if degenerate != 4 {
		t.Fatalf("旧口径对照体自检失败（应 4 句，实得 %d）——反向对照不可信", degenerate)
	}
	if got := countSentences(base + ". Second. Third. Fourth"); got != 4 {
		t.Fatalf("四个真句末必须数成 4 句，实得 %d", got)
	}
}

// TestCountSentencesEmptyFallback 空文本与**纯标点**文本兜底为 1（与改造前一致，且它保证计量读数永不为零）。
// ⚠️ 这一条不是凑数的边缘用例：它就是抓到"判序写反"那一版实现的那把锁——
// 先记内容再判边界时，'。' 自己会把 dirty 抬起来，"。。。" 被数成 3 句（旧口径 1 句），
// 而"全角标点这一维行为不许变"是本批的红线。
func TestCountSentencesEmptyFallback(t *testing.T) {
	for _, s := range []string{"", "   ", "\n\n", "。。。", "...", "！！！", "；；；"} {
		if got := countSentences(s); got != 1 {
			t.Fatalf("空/纯标点文本 %q 应兜底 1 句，实得 %d", s, got)
		}
		if legacy := legacyCountSentences(s); legacy != 1 {
			t.Fatalf("反向对照体自检失败：%q 旧口径应 1 句、实得 %d", s, legacy)
		}
	}
	// 标点串后面才来正文：旧口径给 1 句（前面那些空段一律忽略），判序写反的新实现会给 4 句。
	if got, want := countSentences("。。。第二句"), legacyCountSentences("。。。第二句"); got != want {
		t.Fatalf("纯标点前导的读数与旧口径不等（新 %d／旧 %d）＝边界字符被当成内容了", got, want)
	}
}

// TestCountSentencesEstimateOutParamIsDisplayOnly 接线锁：预估出参的 sentences 走的是这一个函数，
// 且它是纯读数——同一文本换目标语种，读数不许变（参与计算就是 2026-10-13 之前那个缺陷本体）。
func TestCountSentencesEstimateOutParamIsDisplayOnly(t *testing.T) {
	req := estimateReq{Text: "Alpha beta. Gamma delta, e.g. 600912.SH", SegmentCount: 0}
	one := estimateUnitCount(req)
	req.TargetLangs = []string{"en", "ja", "ko"}
	if got := estimateUnitCount(req); got != one {
		t.Fatalf("句数是展示读数，不该随语种数变（%d vs %d）", one, got)
	}
	if want := countSentences(req.Text); one != want {
		t.Fatalf("出参读数与切分实现脱钩（estimateUnitCount=%d／countSentences=%d）——冒出了第二把尺子", one, want)
	}
}
