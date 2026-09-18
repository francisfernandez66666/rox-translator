package engine

import "testing"

// TestSuspectOutputMetric ★ P0-4（2026-09-18）断言：退化输出检出必须进入
// /metrics 计数器（旧实现只有一条 log.Printf，运营侧完全不可观测）。
func TestSuspectOutputMetric(t *testing.T) {
	before := SuspectOutputSnapshot()["de"]
	// 伪标签回显：清洗后会交付干净结果，但检出计数必须 +1
	PostProcessTranslation("<target>Deutsch</target><only>返回结果</only>", "de")
	after := SuspectOutputSnapshot()["de"]
	if after != before+1 {
		t.Fatalf("suspect 计数应 +1：%d → %d", before, after)
	}
	// 正常译文不计数
	PostProcessTranslation("ein gutes Produkt", "de")
	if got := SuspectOutputSnapshot()["de"]; got != after {
		t.Fatalf("正常译文不应计入 suspect：%d → %d", after, got)
	}
	// 快照必须是拷贝：调用方改不动内部状态
	snap := SuspectOutputSnapshot()
	snap["de"] = 999999
	if SuspectOutputSnapshot()["de"] == 999999 {
		t.Fatal("SuspectOutputSnapshot 返回了内部 map 引用")
	}
}
