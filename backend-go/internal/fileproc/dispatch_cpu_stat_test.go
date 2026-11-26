// ============ dispatch_cpu_stat_test.go · fpd.slice CPU 采样口径门禁 ============
// 2026-11-26 工单动态派发批（口径 13）：规则 6b 判"广州是否有余量"时，CPU 维度必须读
// fpd.slice 自己的 cgroup v2 cpu.stat 采样窗占用率，而不是整机 load1
// （整机 load 被量化服务长期顶高，读了会让规则 6b 几乎永不触发）。
//
// 本文件把 dispatch_policy_test.go 里的 TestSampleFPDSliceCPU 逻辑复制成门禁版，并补一条
// 决策层断言：整机 load 高、但 fpd.slice cpu.stat 低 ⇒ 仍判余量足（remote_idle）。
//
// 注意：goodCapacity / bigPDF 复用同包既有 helper（dispatch_policy_test.go），
// 不另造同名函数；cpuStatPath 是包级变量，用 defer 恢复，避免污染其它测试。
// ============================================
package fileproc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSampleFPDSliceCPUGate 钉住 cpu.stat 采样窗差值、缺文件返回值与整机/切片口径分离。
func TestSampleFPDSliceCPUGate(t *testing.T) {
	// 清掉可能的 env 覆盖，钉住默认配额 0.3（30% of 2 核，与 gz-install.sh 一致）。
	t.Setenv("FILEPROC_FPD_CPU_QUOTA", "")

	old := cpuStatPath
	defer func() { cpuStatPath = old }()

	// ① 采样窗差值 ≈ 0.5：200ms 窗内 30ms CPU 时间 / 0.3 核配额 = 0.5。
	p := filepath.Join(t.TempDir(), "cpu.stat")
	os.WriteFile(p, []byte("usage_usec 1000000\n"), 0644)
	cpuStatPath = p
	go func() {
		time.Sleep(60 * time.Millisecond)
		os.WriteFile(p, []byte("usage_usec 1030000\n"), 0644)
	}()
	v := sampleFPDSliceCPU(200 * time.Millisecond)
	if v < 0.4 || v > 0.7 {
		t.Errorf("cpu.stat 采样窗占用率 = %v，期望约 0.5（30ms / (200ms × 0.3)）", v)
	}

	// ② 缺文件（读数取不到）必须返回 -1，交给决策器 fail-closed。
	cpuStatPath = filepath.Join(t.TempDir(), "missing.stat")
	if v := sampleFPDSliceCPU(50 * time.Millisecond); v >= 0 {
		t.Errorf("缺文件应返回 -1，实际 %v", v)
	}

	// ③ 整机 load 高但 fpd.slice 低 ⇒ 仍判余量足（口径 13）。
	// 先制造一个低占用读数：6ms / (200ms × 0.3) = 0.1。
	cpuStatPath = p
	os.WriteFile(p, []byte("usage_usec 2000000\n"), 0644)
	go func() {
		time.Sleep(60 * time.Millisecond)
		os.WriteFile(p, []byte("usage_usec 2006000\n"), 0644)
	}()
	low := sampleFPDSliceCPU(200 * time.Millisecond)
	if low < 0 || low > 0.2 {
		t.Fatalf("低占用夹具构造失败：采样得到 %v，期望 ≤0.2", low)
	}

	// 把采样值喂给决策器，同时把整机 load1 顶到 3.5（量化服务常年高位）。
	t.Setenv(envDispatch, "1")
	t.Setenv(envDispatchHost, "fpd@10.0.0.7")
	t.Setenv(envDispatchMaxMB, "")
	in := bigPDF(t, 60)
	rc := goodCapacity()
	rc.CPULoad1 = 3.5            // 整机 load 高
	rc.CPUQuotaUsed = low        // fpd.slice 配额占用低（真实采样值）
	d := DecideTicketDispatch(35, []string{in}, 40, 0, 1, rc)
	if !d.Dispatch || d.Reason != policyRemoteIdle {
		t.Errorf("整机 load 高但 fpd.slice cpu.stat 低仍应判余量足（remote_idle）：got %+v", d)
	}

	// ④ 反向对照：把 fpd.slice 占用抬到 0.9，同一台机器就应判 local（口径 13 的判据确实生效）。
	rc.CPUQuotaUsed = 0.9
	d2 := DecideTicketDispatch(35, []string{in}, 40, 0, 1, rc)
	if d2.Dispatch || d2.Reason != policyBalancedLocal {
		t.Errorf("fpd.slice 占用 0.9 应判 local（balanced_local）：got %+v", d2)
	}
}
