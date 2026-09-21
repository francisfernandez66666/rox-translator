// ============================================================================
// openapi_gate_message_test.go — ★ #40 限流文案断言（开放 API 配额闸门）
// ----------------------------------------------------------------------------
// 缺陷原貌：/v1/tasks 与 /v1/translate 在闸门拒绝时无条件拼
// 「如余额不足请充值或升级套餐」，于是 QPS/并发限流（429）也被说成余额问题，
// 接入方拿着「请充值」的文案去充值，实际该做的是退避重试。
// 断言口径：余额/日额类保留充值提示；限流与未知类别只回原文案。
// 纯函数测试，不连库（无需方言自钉）。
// ============================================================================
package api

import (
	"errors"
	"strings"
	"testing"
)

func TestGateUserMessageKeepsRateLimitCopyClean(t *testing.T) {
	cases := []struct {
		name    string
		msg     string
		wantRcn bool // 是否应出现「充值」引导
	}{
		{"QPS 限流", "请求过于频繁，请稍后再试", false},
		{"并发限流", "并发请求过多，请稍后再试", false},
		{"积分耗尽", "组织积分已耗尽，请联系管理员及时充值", true},
		{"日额上限", "今日翻译上限已用完", true},
		{"余额不足", "余额不足", true},
		{"未知类别", "服务暂不可用", false},
	}
	for _, c := range cases {
		got := gateUserMessage(errors.New(c.msg))
		if !strings.HasPrefix(got, c.msg) {
			t.Fatalf("%s：原文被改写 %q", c.name, got)
		}
		if has := strings.Contains(got, "充值"); has != c.wantRcn {
			t.Fatalf("%s：充值引导出现=%v，期望=%v（文案 %q）", c.name, has, c.wantRcn, got)
		}
		// 限流类必须仍归一为 rate_limited，避免分类与文案两套口径漂移
		if c.name == "QPS 限流" && gateErrorCode(errors.New(c.msg)) != "rate_limited" {
			t.Fatalf("QPS 限流的 error_code 归一失准：%s", gateErrorCode(errors.New(c.msg)))
		}
	}
}
