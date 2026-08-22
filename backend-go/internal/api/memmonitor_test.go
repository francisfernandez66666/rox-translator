package api

// ============ 本文件职责中文说明 ============
// OOM 内存监控单元测试：验证 /proc/meminfo 解析与内存压力告警触发逻辑。
// 通过注入模拟 meminfo 内容，覆盖正常/压力/解析失败三种场景。
// ========================================

import (
	"os"
	"strings"
	"testing"
)

// writeMeminfo 写入模拟 /proc/meminfo 到指定路径（返回清理函数）。
func writeMeminfo(t *testing.T, content string) {
	t.Helper()
	orig := meminfoPath
	meminfoPath = t.TempDir() + "/meminfo"
	if err := os.WriteFile(meminfoPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写入模拟 meminfo 失败: %v", err)
	}
	t.Cleanup(func() { meminfoPath = orig })
}

// 模拟压力场景：可用内存仅剩 10%，应触发 critical memory 告警。
func TestMemoryPressureAlert(t *testing.T) {
	writeMeminfo(t, `MemTotal:        1000000 kB
MemAvailable:     100000 kB
`)
	total, avail, ok := systemMemInfo()
	if !ok {
		t.Fatal("systemMemInfo 应成功读取模拟 meminfo")
	}
	if total != 1000000 || avail != 100000 {
		t.Fatalf("解析结果错误: total=%d avail=%d", total, avail)
	}
	usedPct := float64(total-avail) / float64(total) * 100
	if usedPct < 85 {
		t.Fatalf("压力场景使用率 %.1f%% 应 ≥85%%", usedPct)
	}
	// 验证告警触发
	triggered := false
	runMemorySample(85, func(tid int64, level, kind, message string) error {
		if kind == "memory" && level == "critical" {
			triggered = true
		}
		return nil
	})
	if !triggered {
		t.Fatal("内存压力告警未触发")
	}
}

// 模拟正常场景：可用内存充足，不应触发告警。
func TestMemoryNormalNoAlert(t *testing.T) {
	writeMeminfo(t, `MemTotal:        1000000 kB
MemAvailable:     900000 kB
`)
	triggered := false
	runMemorySample(85, func(tid int64, level, kind, message string) error {
		if kind == "memory" {
			triggered = true
		}
		return nil
	})
	if triggered {
		t.Fatal("正常内存场景不应触发告警")
	}
}

// 模拟 /proc 不可用：systemMemInfo 应返回失败，且不 panic。
func TestMemoryInfoUnavailable(t *testing.T) {
	writeMeminfo(t, "not a valid meminfo format")
	total, avail, ok := systemMemInfo()
	if ok {
		t.Fatalf("非法 meminfo 不应成功解析: total=%d avail=%d", total, avail)
	}
	if total != 0 || avail != 0 {
		t.Fatalf("失败场景应返回 0: total=%d avail=%d", total, avail)
	}
}

// 验证峰值堆内存只增不减。
func TestPeakHeapMonotonic(t *testing.T) {
	prev := peakHeapBytes
	s1 := sampleSelfMemory()
	s2 := sampleSelfMemory()
	// 两次采样之间测试进程自身可能产生新分配，峰值允许增长但不允许下降
	if s2.PeakHeapMB < s1.PeakHeapMB {
		t.Fatalf("第二次采样峰值不应低于第一次: %.3f vs %.3f", s1.PeakHeapMB, s2.PeakHeapMB)
	}
	if peakHeapBytes < prev {
		t.Fatal("峰值堆内存不应回退")
	}
}

// 验证 topMemoryProcesses 空 /proc 时优雅降级（返回占位文案而非 panic）。
func TestTopProcessesDegrade(t *testing.T) {
	out := topMemoryProcesses(5)
	if out == "" {
		t.Fatal("topMemoryProcesses 不应返回空串")
	}
	if !strings.Contains(out, "（") {
		t.Fatalf("异常降级文案异常: %s", out)
	}
}
