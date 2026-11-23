// ============ md_adjacent_bold_test.go · 职责说明（2026-11-23）============
// 源片段自带 `****：` 加粗骨架时，译文标记必须按源 token 数守恒；
// 只补标记、正文逐字节不变；正常形态不误伤。
// ================================================
package fileproc

import "testing"

func TestRestoreAdjacentBoldSkeleton(t *testing.T) {
	src := `**3\.3 AI 搭建****：**一个"角色智能体群 \+ 共享底座"。`
	tr := `****3.3 הקמת AI: "קבוצת סוכנים לפי תפקידים + תשתית משותפת".**`
	got := mdRestoreAdjacentBoldSkeleton(src, tr)
	if stringCount(got, "**") != stringCount(src, "**") {
		t.Fatalf("标记数没守恒：src=%d got=%d (%q)", stringCount(src, "**"), stringCount(got, "**"), got)
	}
	for _, keep := range []string{"3.3 הקמת AI", "קבוצת סוכנים", "תשתית משותפת"} {
		if !contains(got, keep) {
			t.Fatalf("正文被改动，缺 %q：%s", keep, got)
		}
	}
}

func TestRestoreAdjacentBoldSkeletonNoFalsePositive(t *testing.T) {
	// 正常单对加粗：源/译文 token 相等，不得被改写。
	if got := mdRestoreAdjacentBoldSkeleton(`**指标**`, `**מדד**`); got != `**מדד**` {
		t.Fatalf("正常形态被误改：%q", got)
	}
	// 源无 **** 骨架：即使译文标记少也不在本函数射程。
	if got := mdRestoreAdjacentBoldSkeleton(`**A**`, `A`); got != `A` {
		t.Fatalf("无骨架形态被误改：%q", got)
	}
}

func stringCount(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub) - 1
		}
	}
	return n
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
