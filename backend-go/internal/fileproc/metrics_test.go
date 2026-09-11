package fileproc

import (
	"testing"
	"time"
)

// TestRecordStart 验证 RecordStart 正确递增启动计数
func TestRecordStart(t *testing.T) {
	before := globalProcMetrics.starts
	RecordStart()
	after := globalProcMetrics.starts
	if after != before+1 {
		t.Errorf("RecordStart: expected %d, got %d", before+1, after)
	}
}

// TestRecordSuccess 验证 RecordSuccess 正确递增成功计数和耗时
func TestRecordSuccess(t *testing.T) {
	before := globalProcMetrics.success
	RecordSuccess(100 * time.Millisecond)
	after := globalProcMetrics.success
	if after != before+1 {
		t.Errorf("RecordSuccess: expected %d, got %d", before+1, after)
	}
}

// TestRecordFailure 验证 RecordFailure 正确递增失败计数
func TestRecordFailure(t *testing.T) {
	before := globalProcMetrics.failures
	RecordFailure(50 * time.Millisecond)
	after := globalProcMetrics.failures
	if after != before+1 {
		t.Errorf("RecordFailure: expected %d, got %d", before+1, after)
	}
}

// TestRecordTimeout 验证 RecordTimeout 正确递增超时计数
func TestRecordTimeout(t *testing.T) {
	before := globalProcMetrics.timeouts
	RecordTimeout(2 * time.Second)
	after := globalProcMetrics.timeouts
	if after != before+1 {
		t.Errorf("RecordTimeout: expected %d, got %d", before+1, after)
	}
}

// TestRecordSigkill 验证 RecordSigkill 正确递增 SIGKILL 计数
func TestRecordSigkill(t *testing.T) {
	before := globalProcMetrics.sigkills
	RecordSigkill()
	after := globalProcMetrics.sigkills
	if after != before+1 {
		t.Errorf("RecordSigkill: expected %d, got %d", before+1, after)
	}
}

// TestRecordQueueWait 验证 RecordQueueWait 正确记录排队等待时间
func TestRecordQueueWait(t *testing.T) {
	before := globalProcMetrics.queueWaits
	RecordQueueWait(50 * time.Millisecond)
	after := globalProcMetrics.queueWaits
	if after <= before {
		t.Errorf("RecordQueueWait: expected > %d, got %d", before, after)
	}
}

// TestMetricsSnapshot 验证 Snapshot 返回正确的指标快照
func TestMetricsSnapshot(t *testing.T) {
	// 重置指标
	globalProcMetrics = ProcMetrics{}

	// 记录一些指标
	RecordStart()
	RecordSuccess(100 * time.Millisecond)

	snapshot := GetProcMetrics().Snapshot()
	if snapshot.Starts != 1 {
		t.Errorf("Snapshot.Starts: expected 1, got %d", snapshot.Starts)
	}
	if snapshot.Success != 1 {
		t.Errorf("Snapshot.Success: expected 1, got %d", snapshot.Success)
	}
}

// TestMetricsConcurrency 验证并发安全性
func TestMetricsConcurrency(t *testing.T) {
	// 重置指标
	globalProcMetrics = ProcMetrics{}

	// 并发执行 RecordStart
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			RecordStart()
			done <- true
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}

	snapshot := GetProcMetrics().Snapshot()
	if snapshot.Starts != 100 {
		t.Errorf("Concurrent RecordStart: expected 100, got %d", snapshot.Starts)
	}
}
