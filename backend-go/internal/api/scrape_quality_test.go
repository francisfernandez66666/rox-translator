// ============ scrape_quality_test.go · 职责说明 ============
// api 语义闸适配层的纯函数反证：JSON 解析、阈值边界。
// 判据本体（HY 回译 + evals 打分）的测试在 internal/qualityjudge 包。
// 不依赖网络/DB。
// ============================================================
package api

import (
	"strings"
	"testing"
)

func TestExtractQualityJSON(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		hasObj bool
	}{
		{`{"term":80,"semantic":90}`, `{"term":80,"semantic":90}`, true},
		{"```json\n{\"term\":0,\"semantic\":30}\n```", `{"term":0,"semantic":30}`, true},
		{"打分：{\"term\":50,\"semantic\":60} 以上", `{"term":50,"semantic":60}`, true},
		{"我不确定", "我不确定", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got := extractQualityJSON(tc.in)
		if !tc.hasObj && strings.Contains(got, "{") {
			t.Fatalf("输入 %q 不该有 JSON 对象，却得到 %q", tc.in, got)
		}
		if tc.hasObj && got != tc.want {
			t.Fatalf("输入 %q: got=%q want=%q", tc.in, got, tc.want)
		}
	}
}

func TestScoreThreshold(t *testing.T) {
	const min = 70.0
	cases := []struct {
		term, sem float64
		pass      bool
	}{
		{100, 100, true},
		{70, 70, true}, // 边界：= 阈值放行
		{69, 90, false},
		{90, 69, false},
		{0, 0, false},
	}
	for _, tc := range cases {
		if got := scoreThresholdPass(tc.term, tc.sem, min); got != tc.pass {
			t.Fatalf("term=%.0f sem=%.0f: got=%v want=%v", tc.term, tc.sem, got, tc.pass)
		}
	}
}
