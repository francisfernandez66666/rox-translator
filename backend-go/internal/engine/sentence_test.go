// ============ sentence_test.go · 职责说明 ============
// ★ D22 句级切分单测：CJK 句界 / 预算聚合 / 超长单句硬切 / 短文本不拆。
// =============================================
package engine

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortTextPassthrough(t *testing.T) {
	segs := splitForChatTranslate("今天天气不错，我们去公园。")
	if len(segs) != 1 || segs[0] != "今天天气不错，我们去公园。" {
		t.Fatalf("短文本不应拆分: %v", segs)
	}
}

func TestCJKSentenceSplit(t *testing.T) {
	long := strings.Repeat("这是一个句子。", 200) // 1600 rune > trigger
	segs := splitForChatTranslate(long)
	if len(segs) < 3 {
		t.Fatalf("应切成多段: %d", len(segs))
	}
	joined := strings.Join(segs, "")
	if joined != long {
		t.Fatal("切分拼接必须无损")
	}
	for _, x := range segs {
		if n := utf8.RuneCountInString(x); n > chatSegmentBudgetRunes {
			t.Fatalf("段超预算: %d", n)
		}
	}
}

func TestHardCutOversizeSentence(t *testing.T) {
	one := strings.Repeat("无句号长文本", 300) // 1800 rune 单句
	segs := splitForChatTranslate(one)
	for _, x := range segs {
		if n := utf8.RuneCountInString(x); n > chatSegmentBudgetRunes {
			t.Fatalf("硬切后仍超预算: %d", n)
		}
	}
	if strings.Join(segs, "") != one {
		t.Fatal("硬切拼接有损")
	}
}

func TestAbbreviationNotSplit(t *testing.T) {
	long := "See Dr. Smith in New York. " + strings.Repeat("更多句子内容。", 120)
	segs := splitForChatTranslate(long)
	joined := strings.Join(segs, "")
	if joined != long {
		t.Fatal("无损性破坏")
	}
	for _, x := range segs {
		if strings.Contains(x, "Dr.") && strings.HasSuffix(x, "Dr.") {
			t.Fatalf("缩写被切断: %q", x)
		}
	}
}
