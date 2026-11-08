// ============ 本文件职责中文说明 ============
// S2 地基的行为判据：Scope 的 fail-closed 三态（0／负／合法）、nil ctx 兜底不为空、
// Config 的行业编码／终局档／额度校验，以及词表确实经配置注入而非代码硬编码。
// 本文件刻意不叫 *_gate_test.go：它是行为锁，不进 gateguard C1 的家族级锁集合。
// ========================================
package salesflow

import (
	"context"
	"errors"
	"testing"
)

// 作用域不许回落默认租户：0 与负数都拒绝。
func TestScopeRejectsInvalidTenant(t *testing.T) {
	for _, tid := range []int64{0, -1, -99} {
		if _, err := NewScope(context.Background(), tid); !errors.Is(err, ErrInvalidTenant) {
			t.Fatalf("tid=%d 必须 fail-closed 返回 ErrInvalidTenant，实得 %v", tid, err)
		}
	}
}

// 合法作用域带住 ctx 与显式 tid；nil ctx 也要有非空上下文。
func TestScopeCarriesContextAndTenant(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "trace-x")
	sc, err := NewScope(ctx, 42)
	if err != nil {
		t.Fatalf("合法作用域构建失败: %v", err)
	}
	if sc.TenantID() != 42 {
		t.Fatalf("租户号读回不对: %d", sc.TenantID())
	}
	if sc.Context().Value(ctxKey{}) != "trace-x" {
		t.Fatal("ctx 未透传到引擎作用域")
	}
	nilCtx, err := NewScope(nil, 7)
	if err != nil {
		t.Fatalf("nil ctx 应落 Background，实得错误: %v", err)
	}
	if nilCtx.Context() == nil {
		t.Fatal("Scope.Context() 不许返回 nil")
	}
}

// 终局档一期只许 inquiry；行业编码与额度也要在写侧校验。
func TestConfigValidateOnlyInquiryOutcome(t *testing.T) {
	base := Config{
		IndustryCode:   "cross_border",
		OutcomeStage:   OutcomeStageInquiry,
		DailyFreeLimit: 20,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("合法配置被拒: %v", err)
	}
	cases := map[string]Config{
		"空行业编码":  {IndustryCode: "  ", OutcomeStage: OutcomeStageInquiry},
		"到店终局":   {IndustryCode: "cross_border", OutcomeStage: "arrived"},
		"成交终局":   {IndustryCode: "cross_border", OutcomeStage: "dealt"},
		"负的免费额度": {IndustryCode: "cross_border", OutcomeStage: OutcomeStageInquiry, DailyFreeLimit: -1},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("配置校验漏放: %s", name)
		}
	}
}

// 词表由配置注入：换一份配置就读到另一套锚位词，引擎侧不持有硬编码。
func TestConfigLexiconIsInjected(t *testing.T) {
	c := Config{
		IndustryCode: "cross_border",
		OutcomeStage: OutcomeStageInquiry,
		AnchorLexicon: map[string][]string{
			"price": {"price", "precio", "価格"},
		},
	}
	if got := c.AnchorLexicon["price"]; len(got) != 3 || got[2] != "価格" {
		t.Fatalf("注入的跨语言词表读回不对: %v", got)
	}
}
