// ============ 本文件职责中文说明 ============
// 图片件「单批段数上限＝15」闸门（★ 2026-10-28 图片版式回退批，方案 §5 断言 C1）。
//
// 背景：计价/成本批把文件链的自适应分批从固定 15 抬到 `adaptiveBatchSize(…, 40, 6000)`，
// 那一档是为**长文档/表格省固定指令开销**设计的。图片件的交付面不一样：它是「按 OCR 框原位回填的
// 那张图」，段↔框一一对应，批量里漏一行／串行一行就会把译文画到**错误的框**上——客户看到的不是
// "少一句"而是"字跑到别处"。而图片件段数本就少（几十段量级），收紧上限多出的指令开销远小于文档件。
// ⇒ 图片件走 15、其余格式仍走 40，两档分开，且**不许再被顺手合并回一个数**。
//
// 三条腿：
//
//	K1 形态腿：`HandleFile` 里 `imgSegCap` 的默认档是 40、图片档被 `IsImageFormat(ext)` 收成 15；
//	K2 调用点腿（守卫级）：三处 `adaptiveBatchSize(` 调用一律吃 `imgSegCap`，代码行上不许再出现
//	   写死的 `40`（写死＝有人把图片件又抬回 40，而这条链的坏在界面上要等一单真图才现形）；
//	K3 射程自证：两枚变异各改一处（图片档改回 40／调用点写回字面量 40），必须分别让 K1／K2 判红。
//
// ============================================
package engine

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// imageBatchCapJudge 出两类读数：图片档是否收成 15、调用点是否还写着死 40。
// 纯函数——K3 的变异喂副本进来复用同一把尺子。
func imageBatchCapJudge(code string) (imgCapMissing, literal40Hits []string) {
	if !strings.Contains(code, "imgSegCap := 40") {
		imgCapMissing = append(imgCapMissing, "默认档 `imgSegCap := 40` 不在位")
	}
	if !strings.Contains(code, "imgSegCap = 15") {
		imgCapMissing = append(imgCapMissing, "图片件上限 `imgSegCap = 15` 不在位（图片件被抬回大批）")
	}
	if !strings.Contains(code, "fileproc.IsImageFormat(ext)") {
		imgCapMissing = append(imgCapMissing, "分档判据 `IsImageFormat(ext)` 不在位（15 那一档没人抬得起）")
	}
	calls := 0
	for _, line := range strings.Split(code, "\n") {
		t0 := strings.TrimSpace(line)
		if !strings.Contains(t0, "adaptiveBatchSize(") || strings.HasPrefix(t0, "func ") {
			continue
		}
		calls++
		if strings.Contains(t0, ", 40,") {
			literal40Hits = append(literal40Hits, t0)
		}
	}
	if calls < 3 {
		imgCapMissing = append(imgCapMissing, "adaptiveBatchSize 调用点只数到 "+strconv.Itoa(calls)+" 处（应 ≥3，判据已空转）")
	}
	return imgCapMissing, literal40Hits
}

// goCodeLines 剥掉整行 `//` 注释（说明文字里出现 40 不算抄本，同 §一·5 那族纪律）。
func goCodeLines(src string) string {
	out := make([]string, 0, 64)
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestImageBatchCapIsFifteen 形态腿：图片件上限 15、调用点不许写死 40（文本链仍 40 作正向对照）。
func TestImageBatchCapIsFifteen(t *testing.T) {
	src, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读不到 file.go（被移动？这条锁的射程就没了）: %v", err)
	}
	missing, literal := imageBatchCapJudge(goCodeLines(string(src)))
	if len(missing) > 0 {
		t.Fatalf("图片件批量上限形态被改动：%s", strings.Join(missing, " ｜ "))
	}
	if len(literal) > 0 {
		t.Fatalf("调用点写死 40（图片件会被抬回大批，段↔框错位风险回来）：%s", strings.Join(literal, " ｜ "))
	}
}

// TestImageBatchCapMutationsBreakTheirLegs 射程自证：两枚变异必须各让一条腿红。
func TestImageBatchCapMutationsBreakTheirLegs(t *testing.T) {
	src, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读不到 file.go: %v", err)
	}
	code := goCodeLines(string(src))

	// 变异 1：图片档改回 40 ⇒ K1 必须点名。
	m1 := strings.Replace(code, "imgSegCap = 15", "imgSegCap = 40", 1)
	missing1, _ := imageBatchCapJudge(m1)
	if len(missing1) == 0 {
		t.Fatalf("反证 1 失效：图片档改回 40 仍判绿（K1 空转）")
	}

	// 变异 2：调用点写回字面量 40 ⇒ K2 必须点名。
	m2 := strings.Replace(code, "adaptiveBatchSize(protSrc, imgSegCap, 6000)",
		"adaptiveBatchSize(protSrc, 40, 6000)", 1)
	_, literal2 := imageBatchCapJudge(m2)
	if len(literal2) == 0 {
		t.Fatalf("反证 2 失效：调用点写回字面量 40 仍判绿（K2 空转）")
	}
}
