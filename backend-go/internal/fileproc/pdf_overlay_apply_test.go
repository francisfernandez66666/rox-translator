package fileproc

import "testing"

// TestParseOverlayApplyOutput ★ P0-7（2026-09-18）断言：零命中/低命中判错所依赖的
// stdout 计数解析必须准确（旧实现直接丢弃 stdout，未翻译原样件被当成功交付）。
func TestParseOverlayApplyOutput(t *testing.T) {
	st := parseOverlayApplyOutput("OK: /tmp/out.pdf replaced=0 overflow=0 requested=6 wm_blanked=1\n")
	if st.Replaced != 0 || st.Requested != 6 || st.Overflow != 0 {
		t.Fatalf("零命中行解析错误: %+v", st)
	}
	st = parseOverlayApplyOutput("warn: something\nOK: /tmp/out.pdf replaced=42 overflow=3 requested=45 wm_blanked=0")
	if st.Replaced != 42 || st.Requested != 45 || st.Overflow != 3 {
		t.Fatalf("正常行解析错误: %+v", st)
	}
	// 兼容旧版脚本输出（无 requested=）：解析为 0 → 调用方不判错，保持成功口径
	st = parseOverlayApplyOutput("OK: /tmp/out.pdf replaced=10 overflow=0 wm_blanked=0")
	if st.Replaced != 10 || st.Requested != 0 {
		t.Fatalf("旧版输出兼容解析错误: %+v", st)
	}
}

// TestOverlayHitVerdict ★ P0-7 判定规则（与 ApplyTranslatedPdfOverlay 内联逻辑同口径）：
// requested>0 时 replaced==0 或命中率 <60% 必须判错。
func TestOverlayHitVerdict(t *testing.T) {
	verdict := func(replaced, requested int) bool { // true=应判错
		if requested <= 0 {
			return false
		}
		return replaced == 0 || replaced*10 < requested*6
	}
	cases := []struct {
		r, q int
		want bool
	}{
		{0, 6, true},     // 零命中：提取键口径漂移的典型形态
		{5, 6, false},    // 83% 命中：正常（重复段合并等会造成少量差额）
		{3, 6, true},     // 50% 命中：疑似半翻译件，判错走重建兜底
		{60, 100, false}, /* 60% 恰线不判错 */
		{0, 0, false},    // 无可判信息：兼容旧脚本，不拦
	}
	for _, c := range cases {
		if got := verdict(c.r, c.q); got != c.want {
			t.Errorf("verdict(%d,%d)=%v want %v", c.r, c.q, got, c.want)
		}
	}
}
