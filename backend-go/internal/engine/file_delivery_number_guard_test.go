// ============ file_delivery_number_guard_test.go · 职责说明（2026-11-23）============
// 交付物数字守恒兜底锁：写回后长句格里丢掉的数字必须回落源文并点名；
// 已正确换算的金额格、无数字格、非表格行不得被误改。
// ================================================
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyDeliveredNumbersFallsBackMissingDigitCell(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.md")
	out := filepath.Join(dir, "out.md")
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(src, "# 标题\n|指标|值|\n|投放|线索量 \\+150%|\n|金额|65–120 万|\n")
	// 第二格丢了 150，第三格金额已正确换算（650,000–1,200,000）。
	write(out, "# כותרת\n|מדד|ערך|\n|פרסום|כמות לידים זהה|\n|סכום|650,000–1,200,000|\n")

	notes, warnings, err := (&Engine{}).verifyDeliveredNumbers(context.Background(), src, out, "he")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("数字就地补齐后不该产生未译点名，实得 %+v", notes)
	}
	if len(warnings) != 1 {
		t.Fatalf("应有一条补齐告警，实得 %+v", warnings)
	}
	b, _ := os.ReadFile(out)
	got := string(b)
	if !strings.Contains(got, "+150%") || !strings.Contains(got, "כמות לידים") {
		t.Fatalf("缺数格未就地补齐或译文被覆盖：%s", got)
	}
	if !strings.Contains(got, "650,000–1,200,000") {
		t.Fatalf("正确换算的金额格被误改：%s", got)
	}
	if !strings.Contains(got, "# כותרת") {
		t.Fatalf("非表格行被误改：%s", got)
	}
}

func TestVerifyDeliveredNumbersNoFalsePositive(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.md")
	out := filepath.Join(dir, "out.md")
	os.WriteFile(src, []byte("|金额|150–250 万|\n"), 0o644)
	os.WriteFile(out, []byte("|סכום|1,500,000–2,500,000|\n"), 0o644)
	notes, warnings, err := (&Engine{}).verifyDeliveredNumbers(context.Background(), src, out, "he")
	if err != nil || len(notes) != 0 || len(warnings) != 0 {
		t.Fatalf("正确换算不该触发兜底：notes=%+v warnings=%+v err=%v", notes, warnings, err)
	}
}
