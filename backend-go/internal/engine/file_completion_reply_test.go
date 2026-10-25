// ============ file_completion_reply_test.go · 职责说明 ============
// 2026-10-25 修法 A/C 的判据锁（用户在 PROGRESS「待批复」那条上批复「A 和 C 都做了」）：
//
//	A：漏译提示里的数字必须是**段数总和**，不是「有漏译的语种个数」。
//	   旧写法 `len(parts)` 把语种数套进「有 %d 段」：en 缺 3 + ja 缺 1 ⇒ 文案说"2 段"（真值 4）；
//	   只缺一个语种时印出「有 1 段未能译出（en×5）」——同一句自相矛盾。
//	C：一个可用交付物都没有时不许报「✅ 文件翻译完成」。
//	   引擎出口必须用 fileNoDeliverables 判零交付并返回 Error（service 两条路径随之按失败收尾）。
//
// 三腿：
//
//	① 文案口径（A）：多语种/单语种/全零三档逐字判数字与分解；
//	② 零交付判据（C）：主产物与旁路两边都空才为真（单边有产物＝仍有交付，不许判死）；
//	③ 接线（守卫级）：HandleFile 里 fileNoDeliverables 必须**先于** fileCompletionReply，
//	   且旧的 `len(parts)` 写法不许回潮（判据级断言代替不了守卫级，见 AGENTS §三）。
//
// =============================================
package engine

import (
	"os"
	"strings"
	"testing"
)

// TestFileCompletionReplyCountsSegments 修法 A：数字＝段数总和，括号保留分语种分解。
func TestFileCompletionReplyCountsSegments(t *testing.T) {
	cases := []struct {
		name       string
		un         map[string]int
		wantIn     []string // 必须出现
		wantAbsent []string // 必须不出现（固定住"语种个数冒充段数"这一形态）
	}{
		{
			name:   "多语种：3+1 段必须报 4 段",
			un:     map[string]int{"en": 3, "ja": 1},
			wantIn: []string{"有 4 段未能译出", "en×3", "ja×1"},
			// 旧写法会印「有 2 段」（＝两个语种）；这一条就是那次的回退锁。
			wantAbsent: []string{"有 2 段未能译出"},
		},
		{
			name:       "单语种：en 缺 5 段必须报 5 段（旧写法会印 1 段）",
			un:         map[string]int{"en": 5},
			wantIn:     []string{"有 5 段未能译出", "en×5"},
			wantAbsent: []string{"有 1 段未能译出"},
		},
		{
			name:       "全零不出口（否则印出「有 0 段未能译出（）」这种噪声）",
			un:         map[string]int{"en": 0},
			wantAbsent: []string{"未能译出"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fileCompletionReply(2, []string{"/tmp/a-en.md"}, []string{"en", "ja"}, c.un, nil, nil)
			for _, w := range c.wantIn {
				if !strings.Contains(got, w) {
					t.Fatalf("完成话术缺 %q，实得：%s", w, got)
				}
			}
			for _, w := range c.wantAbsent {
				if strings.Contains(got, w) {
					t.Fatalf("完成话术出现了不该有的 %q（单位错配已回潮），实得：%s", w, got)
				}
			}
		})
	}
}

// TestFileCompletionReplyKeepsOtherSections 文案的其它三段（基础句/质量提示/降级说明）不许被这次搬家弄丢。
func TestFileCompletionReplyKeepsOtherSections(t *testing.T) {
	got := fileCompletionReply(7, []string{"/tmp/a-en.md", "/tmp/a-ja.md"}, []string{"en", "ja"},
		map[string]int{"en": 1}, []string{"质量A", "质量B"}, []string{"ja"})
	for _, w := range []string{"共 7 段文本", "输出 2 个文件", "en/ja", "有 1 段未能译出", "质量校验提示 2 条", "版式还原失败"} {
		if !strings.Contains(got, w) {
			t.Fatalf("完成话术缺 %q，实得：%s", w, got)
		}
	}
}

// TestFileNoDeliverables 修法 C 的判据：两边都空才算零交付。
func TestFileNoDeliverables(t *testing.T) {
	cases := []struct {
		name     string
		files    []string
		text     []string
		wantTrue bool
	}{
		{"主产物与旁路都空＝零交付", nil, nil, true},
		{"空切片同样算零交付", []string{}, []string{}, true},
		{"只有主产物＝仍有交付", []string{"/a-en.md"}, nil, false},
		{"只有纯文案旁路＝仍有交付", nil, []string{"/a-en-text.md"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fileNoDeliverables(c.files, c.text); got != c.wantTrue {
				t.Fatalf("fileNoDeliverables(%v,%v)=%v，期望 %v", c.files, c.text, got, c.wantTrue)
			}
		})
	}
}

// TestFileCompletionGuardWired 守卫级：判据存在不等于接上了——零交付判定必须先于完成话术。
// 反证思路：两个锚点任一缺失（被挪走/被删）都判红；旧 `len(parts)` 回潮也判红。
func TestFileCompletionGuardWired(t *testing.T) {
	src, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读不到 file.go: %v", err)
	}
	body := string(src)
	iGuard := strings.Index(body, "if fileNoDeliverables(filesOut, textOut) {")
	iReply := strings.Index(body, "reply := fileCompletionReply(")
	if iGuard < 0 {
		t.Fatal("HandleFile 里找不到零交付判定（修法 C 的接线被摘掉）")
	}
	if iReply < 0 {
		t.Fatal("HandleFile 里找不到完成话术调用（修法 A 的出口被摘掉）")
	}
	if iGuard > iReply {
		t.Fatal("零交付判定排在完成话术之后 ⇒ 仍会先拼出「✅ 完成」再报错，修法 C 形同虚设")
	}
	// 旧形态（语种个数冒充段数）不许回潮：出现 `len(parts), strings.Join(parts` 即判红。
	if strings.Contains(body, "len(parts), strings.Join(parts") {
		t.Fatal("完成话术又用 len(parts)（语种个数）当段数 ⇒ 修法 A 已回退")
	}
}
