// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》§9.1 出站链顺序锁：新守卫必须在 guardReplyBrand 之后、
// applyRechargeIntent 之前（first/last 各一枚），且 observeVisitorAsides 仍在
// localizeActionNames 之后。★ 同时断言「默认零改动」的放行口仍在（ctx 未注入即 return）。
//
// 为什么用源码级顺序判据：链路顺序错误不会让任何单测变红（每条腿单独都自洽），
// 但会把「先剥句再判充值」变成「先判充值再剥句」，那是全租户语义漂移。
// ========================================
package engine

import (
	"os"
	"strings"
	"testing"
)

func readOutboundChain(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatalf("读不到 engine.go: %v", err)
	}
	return string(raw)
}

// first：守卫必须在 brand 之后。
func TestKnowledgeGuardAfterBrand(t *testing.T) {
	src := readOutboundChain(t)
	brand := strings.Index(src, "rep = e.guardReplyBrand(ctx, answer, rep)")
	guard := strings.Index(src, "rep = e.guardKnowledgeAnswer(ctx, answer, rep)")
	if brand < 0 || guard < 0 {
		t.Fatal("出站链缺少 brand 或知识层守卫的调用行")
	}
	if guard <= brand {
		t.Fatal("知识层守卫必须在 guardReplyBrand 之后（先过反编造/报价/品牌三道）")
	}
}

// last：守卫必须在 recharge 之前，且不跨过 observeVisitorAsides。
func TestKnowledgeGuardBeforeRecharge(t *testing.T) {
	src := readOutboundChain(t)
	guard := strings.Index(src, "rep = e.guardKnowledgeAnswer(ctx, answer, rep)")
	recharge := strings.Index(src, "rep = e.applyRechargeIntent(ctx, sessionID, input, rep)")
	localize := strings.Index(src, "rep.Actions = e.localizeActionNames(ctx, rep.Actions, answer)")
	observe := strings.Index(src, "e.observeVisitorAsides(ctx, answer, rep)")
	if guard < 0 || recharge < 0 || localize < 0 || observe < 0 {
		t.Fatal("出站链四腿有一处缺席（锚点写漂）")
	}
	if guard >= recharge {
		t.Fatal("知识层守卫必须在 applyRechargeIntent 之前")
	}
	if !(localize < observe) {
		t.Fatal("observeVisitorAsides 必须仍在 localizeActionNames 之后")
	}
	if !(recharge < localize && localize < observe) {
		t.Fatal("recharge → localize → observe 的既有顺序不得被跨过")
	}
}
