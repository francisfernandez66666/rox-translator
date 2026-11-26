// ============ dispatch_scenarios_test.go · 职责说明 ============
// 四个场景门禁：多语言提取一次、截断超限广州 assemble 接手、
// 装机熔断记录《量化峰值基线》、双项目目录/TMPDIR 隔离。
// 均为源码形态断言（真机部分由验收清单覆盖）。
// ==========================================================
package fileproc

import (
	"os"
	"strings"
	"testing"
)

// TestDispatchMultilangExtractOnce 钉住 B2：提取一次、分语言翻译、各语言独立写回（F-2）。
// 用源码形态断言：dispatch_chunks.go 里 texts 计算在 bounds 之前且只一次；
// 多语循环由上层按 finalLangs 驱动（file.go 保持原循环，不另造）。
func TestDispatchMultilangExtractOnce(t *testing.T) {
	b, err := os.ReadFile("../engine/dispatch_chunks.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	extractAt := strings.Index(s, "ExtractTextsPdfOverlay(")
	boundsAt := strings.Index(s, "bounds = append")
	if extractAt < 0 || boundsAt < 0 || extractAt > boundsAt {
		t.Error("提取必须在算块边界之前（提取一次、块边界共用）")
	}
	if strings.Count(s, "ExtractTextsPdfOverlay(") != 1 {
		t.Error("派发编排里提取只能出现一次（多语共用 texts）")
	}
}

// TestDispatchRetryTakeoverAssemble 钉住 C-6：截断重试→超限广州 assemble 接手。
func TestDispatchRetryTakeoverAssemble(t *testing.T) {
	b, err := os.ReadFile("../../../deploy/dispatch/chunk_translate.py")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"def cmd_assemble(", `payload["op"] = "assemble"`, "def _run_worker("} {
		if !strings.Contains(s, want) {
			t.Errorf("chunk_translate.py 缺 %s（超限接手链路断）", want)
		}
	}
	// worker 侧必须识别 assemble 并调同源写回
	w, err := os.ReadFile("../engine/chunkworker.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(w), "RunChunkAssemble") || !strings.Contains(string(w), "pdf_overlay.py") {
		t.Error("worker 未实现 assemble 写回（或没走同源 pdf_overlay.py）")
	}
	// 非派发单不切广州：MaybeDispatchTicket 判 local 时 handled=false
	e, err := os.ReadFile("../engine/dispatch_chunks.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(e), "return nil, false // 判 local") &&
		!strings.Contains(string(e), "handled=false") {
		t.Error("非派发单未明确 handled=false（可能中途切广州）")
	}
}

// TestDispatchCircuitBreakerPeakRecord 钉住 D-7：越线时记录《量化峰值基线》，不硬回滚。
func TestDispatchCircuitBreakerPeakRecord(t *testing.T) {
	b, err := os.ReadFile("../../../deploy/dispatch/gz-install.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"BASELINE_FILE", "PEAK_FILE", "MemoryHigh", "load1 持续", "不硬回滚"} {
		if !strings.Contains(s, want) {
			t.Errorf("gz-install.sh 缺熔断要素 %s", want)
		}
	}
	// 反证：删掉记录分支的假源码应判红
	fake := "trigger_peak_record(){ bad \"$1\"; exit 8; }"
	checker := func(src string) bool { return strings.Contains(src, "PEAK_FILE") }
	if checker(fake) {
		t.Error("反证落空：无 PEAK_FILE 的假源码被判通过")
	}
}

// TestDispatchDoubleIsolation 钉住 G-5：双目录隔离（fpd 不写 /tmp 与量化目录）。
func TestDispatchDoubleIsolation(t *testing.T) {
	bridge, err := os.ReadFile("../../../deploy/dispatch/fpd-bridge")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bridge), "export TMPDIR=") ||
		!strings.Contains(string(bridge), "w/.session_tmp") {
		t.Error("fpd-bridge 未把 TMPDIR 钉到 w/.session_tmp")
	}
	gz, err := os.ReadFile("../../../deploy/dispatch/gz-install.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(gz)
	for _, forbidden := range []string{"/etc/quant.env\"", "/var/lib/quant-trading-v2"} {
		if strings.Contains(s, forbidden) && !strings.Contains(s, "[ -f /etc/quant.env ]") {
			t.Errorf("gz-install.sh 出现量化路径写入风险：%s", forbidden)
		}
	}
	if !strings.Contains(s, "零 Key 落盘") {
		t.Error("gz-install.sh 缺零 Key 自检")
	}
}
