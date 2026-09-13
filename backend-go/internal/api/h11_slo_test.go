// ============================================================================
// H11 SLO 计算层单测：窗口错误率/燃烧率、P99 环形分位、告警级别判定（纯函数）。
// ============================================================================
package api

import (
	"testing"
	"time"
)

func h511State(samples []sloSample) *sloState {
	st := &sloState{}
	for i, s := range samples {
		st.ring[i%sloRing] = s
	}
	st.filled = len(samples)
	if st.filled > sloRing {
		st.filled = sloRing
	}
	return st
}

func TestH11BurnRateWindows(t *testing.T) {
	now := time.Now()
	// 60 分钟，每分钟一条；后 6 条 down（可用性错误率 10%）
	var ss []sloSample
	for i := 0; i < 60; i++ {
		up := i < 54
		ss = append(ss, sloSample{T: now.Add(-time.Duration(60-i) * time.Minute), Up: up, Totals: 10})
	}
	st := h511State(ss)
	def := sloDefs[0] // availability 99.9 → budget 0.1%
	w := st.sloRateAt(def, time.Hour, now)
	if w.ErrRate < 0.099 || w.ErrRate > 0.101 {
		t.Fatalf("1h 错误率应≈10%%，实际 %.4f", w.ErrRate)
	}
	if w.Burn < 99 || w.Burn > 101 { // 10% / 0.1% = 100 倍燃烧率
		t.Fatalf("burn rate 应≈100，实际 %.1f", w.Burn)
	}
	w10m := st.sloRateAt(def, 10*time.Minute, now)
	if w10m.Samples != 10 {
		t.Fatalf("10 分钟窗口样本数错误: %d", w10m.Samples)
	}
	if w10m.ErrRate < 0.39 || w10m.ErrRate > 0.61 { // 6/10
		t.Fatalf("快窗错误率错误: %.2f", w10m.ErrRate)
	}
}

func TestH11TranslateAndP99SLOs(t *testing.T) {
	now := time.Now()
	defs := map[string]sloDef{"translate_success": sloDefs[1], "latency_p99": sloDefs[2]}
	// 成功率 SLO：1000 请求 50 失败 = 5% > 1% 预算 → burn 5
	var ss []sloSample
	for i := 0; i < 60; i++ {
		ss = append(ss, sloSample{T: now.Add(-time.Duration(60-i) * time.Minute), Up: true, Totals: 100, Fails: 5})
	}
	st := h511State(ss)
	w := st.sloRateAt(defs["translate_success"], time.Hour, now)
	if w.ErrRate < 0.049 || w.ErrRate > 0.051 || w.Burn < 4.9 || w.Burn > 5.1 {
		t.Fatalf("成功率窗口错误: %+v", w)
	}
	// P99 SLO：一半分钟超 8000ms → 阈值型预算 5% → burn 10
	var ss2 []sloSample
	for i := 0; i < 60; i++ {
		p99 := 5000.0
		if i%2 == 0 {
			p99 = 9000.0
		}
		ss2 = append(ss2, sloSample{T: now.Add(-time.Duration(60-i) * time.Minute), Up: true, P99: p99})
	}
	st2 := h511State(ss2)
	w2 := st2.sloRateAt(defs["latency_p99"], time.Hour, now)
	if w2.ErrRate < 0.49 || w2.ErrRate > 0.51 {
		t.Fatalf("P99 超线占比错误: %+v", w2)
	}
}

func TestH11MetricsP99Ring(t *testing.T) {
	m := newMetrics()
	for i := 1; i <= 100; i++ {
		m.observeHTTP("/api/translate", float64(i*10))
	}
	p99 := m.P99()
	if p99 < 980 || p99 > 1010 {
		t.Fatalf("P99 应≈1000ms，实际 %.1f", p99)
	}
	ok, fail := m.TranslationsTotals()
	m.countTranslate("text", true)
	m.countTranslate("text", false)
	ok2, fail2 := m.TranslationsTotals()
	if ok2-ok != 1 || fail2-fail != 1 {
		t.Fatalf("TranslationsTotals 增量错误: %d/%d → %d/%d", ok, fail, ok2, fail2)
	}
}
