// ============ scrape_quality_test.go · 职责说明 ============
// api 语义闸适配层的纯函数反证：JSON 解析、阈值边界。
// 判据本体（HY 回译 + evals 打分）的测试在 internal/qualityjudge 包。
// 不依赖网络/DB。
// ============================================================
package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"translator/internal/qualityjudge"
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

type fakeQualityJudgeAPI struct {
	result qualityjudge.Result
	err    error
}

func (f fakeQualityJudgeAPI) Evaluate(context.Context, string, string, string) (qualityjudge.Result, error) {
	return f.result, f.err
}

func (f fakeQualityJudgeAPI) Repair(context.Context, string, string) (string, bool, error) {
	return "", false, f.err
}

func TestAPIQualityJudgeKeepsRescueMetadata(t *testing.T) {
	a := &apiQualityJudge{j: fakeQualityJudgeAPI{result: qualityjudge.Result{
		Pass: true, Conclusive: true, RescuedByReview: true, ReviewNote: "复核改判通过",
	}}}
	pass, reason, err := a.JudgeTerm(context.Background(), "用户互动", "תגובות", "he")
	if err != nil || !pass || reason != "复核改判通过" {
		t.Fatalf("救回元数据在适配层丢失: pass=%v reason=%q err=%v", pass, reason, err)
	}
}

func TestAPIQualityJudgeErrorIsFailClosed(t *testing.T) {
	a := &apiQualityJudge{j: fakeQualityJudgeAPI{err: errors.New("judge down")}}
	pass, reason, err := a.JudgeTerm(context.Background(), "数据备份", "Rezerwacja danych", "pl")
	if err == nil || pass || !strings.Contains(reason, "无法判定") {
		t.Fatalf("无法判定必须 fail-closed：pass=%v reason=%q err=%v", pass, reason, err)
	}
}

func TestAPIQualityJudgeInconclusiveIsFailClosed(t *testing.T) {
	a := &apiQualityJudge{j: fakeQualityJudgeAPI{result: qualityjudge.Result{Pass: false, Reason: "两腿都不可用"}}}
	pass, reason, err := a.JudgeTerm(context.Background(), "数据备份", "Rezerwacja danych", "pl")
	if err == nil || pass || !strings.Contains(reason, "无法判定") {
		t.Fatalf("Conclusive=false 即使 err=nil 也必须 fail-closed：pass=%v reason=%q err=%v", pass, reason, err)
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
