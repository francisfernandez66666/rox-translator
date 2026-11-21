// ============ review_toggle_workflow_test.go · 职责说明 ============
// 翻译工单的「复核链路运营开关」（★ 2026-11-21 用户令：工单也要可配）的机械锁。
//
// 为什么要在 orchestrator 这一层单独测：工单的两个复核腿（runReview / runEvalsReview）
// **不在** Engine.HandleText / HandleFile 的调用链上，它们由流程执行器直接调用。
// 只在 engine 包里测「关闭时不调模型」，对这两条腿**完全没有射程**——
// 运营在后台把工单档关掉，工单仍会逐语种跑一遍审校与复核评估，而屏幕上零读数。
//
// 两条腿各判两态（关闭＝零模型调用且流程照常；开启＝真拨一枪，正向对照）：
//   - 只测关闭侧等于给"闸门被整个摘掉"发绿灯；
//   - 只测开启侧则证明不了开关存在。
//
// 另外钉住一条取向：关闭时**不改**载荷里的译文与评分（"没评估"不许伪装成"评估通过"，
// 也不许顺手把 Sources 标成任何"已复核"形态）。
// ============================================
package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/evals"
	"translator/internal/store"
)

// rtPayloadTicket 建一张带载荷的工单（单行源文 ⇒ 走整篇审校那一支，与现网短文本工单同形）。
func rtPayloadTicket(t *testing.T, h *pwHarness, source, translation string) *store.Ticket {
	t.Helper()
	tk := pwTicket(t, h, source, "en")
	p := ticketPayload{
		SourceText:   source,
		TargetLangs:  []string{"en"},
		Translations: map[string]string{"en": translation},
		Sources:      map[string]string{"en": "model"},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("载荷序列化失败: %v", err)
	}
	tk.FinalResult = string(b)
	if err := h.St.UpdateTicket(tk); err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	return tk
}

// TestWorkflowReviewToggleOffSkipsBothLegs 关闭档：审校与复核 evals 一次模型都不拨。
func TestWorkflowReviewToggleOffSkipsBothLegs(t *testing.T) {
	// ★ 计数而不是在闭包里 t.Fatalf：假上游可能被引擎从别的 goroutine 调用，
	//   而 t.Fatalf 在非测试 goroutine 里只会结束那一个 goroutine、**测试照常判绿**——
	//   那是本仓反复踩过的"判据自伤"形态。计数在测试 goroutine 里断言才可靠。
	var mu sync.Mutex
	calls := 0
	var firstPrompt string
	h := pwSetup(t, func(prompt string) string {
		mu.Lock()
		calls++
		if firstPrompt == "" {
			firstPrompt = prompt
		}
		mu.Unlock()
		return ""
	})
	h.W.Engine.Evals = evals.New(config.C, h.W.Engine.LLM, h.St, "sk-fake-judge")
	if err := h.St.SetConfig("ops_policy", `{"quality":{"ticket_review_enabled":false}}`); err != nil {
		t.Fatalf("写 ops_policy 失败: %v", err)
	}

	const src = "本系统支持 45 种多格式文件翻译能力"
	const tr = "This system supports multi-format file translation."
	tk := rtPayloadTicket(t, h, src, tr)

	ctx := engine.WithReviewScope(context.Background(), engine.ReviewScopeTicket)
	if err := h.W.runReview(ctx, tk); err != nil {
		t.Fatalf("关闭审校腿后 runReview 不应报错（流程要照常走完），实得 %v", err)
	}
	if err := h.W.runEvalsReview(ctx, tk); err != nil {
		t.Fatalf("关闭复核评估后 runEvalsReview 不应报错，实得 %v", err)
	}

	p := pwPayload(t, h, tk.ID)
	mu.Lock()
	n, fp := calls, firstPrompt
	mu.Unlock()
	if n != 0 {
		t.Fatalf("复核链路已关闭，工单却仍打了 %d 次上游（prompt=%.60s）——开关没落在两条腿的咽喉上", n, fp)
	}
	if p.Translations["en"] != tr {
		t.Errorf("复核链路关闭时译文不得被改动，实得 %q", p.Translations["en"])
	}
	if p.Sources["en"] != "model" {
		t.Errorf("复核链路关闭时来源标记不得被改写（不许伪装成已复核），实得 %q", p.Sources["en"])
	}
	if len(p.ReviewEvalScores) != 0 {
		t.Errorf("复核链路关闭时不得落复核评分（没评估≠评估通过），实得 %+v", p.ReviewEvalScores)
	}
}

// TestWorkflowReviewToggleOnRunsBothLegs 开启档（正向对照）：审校真改译文、复核 evals 真落分。
func TestWorkflowReviewToggleOnRunsBothLegs(t *testing.T) {
	const revised = "This system supports 45 multi-format file translation formats."
	h := pwSetup(t, func(prompt string) string {
		if strings.Contains(prompt, "翻译质量评估员") {
			return `{"term":20,"grammar":20,"semantic":20,"numunit":20,"style":20}`
		}
		return revised
	})
	h.W.Engine.Evals = evals.New(config.C, h.W.Engine.LLM, h.St, "sk-fake-judge")
	// 显式声明开启（与"没配过"那一档区分开：本用例要证明的是开关的**正**向形态）
	if err := h.St.SetConfig("ops_policy", `{"quality":{"ticket_review_enabled":true}}`); err != nil {
		t.Fatalf("写 ops_policy 失败: %v", err)
	}

	const src = "本系统支持 45 种多格式文件翻译能力"
	const tr = "This system supports multi-format file translation."
	tk := rtPayloadTicket(t, h, src, tr)

	ctx := engine.WithReviewScope(context.Background(), engine.ReviewScopeTicket)
	if err := h.W.runReview(ctx, tk); err != nil {
		t.Fatalf("runReview: %v", err)
	}
	p := pwPayload(t, h, tk.ID)
	if p.Translations["en"] == tr {
		t.Fatalf("复核链路开启时审校必须真跑（译文一字未变 ⇒ 关闭侧的绿灯无从对照，判据失明）")
	}

	if err := h.W.runEvalsReview(ctx, tk); err != nil {
		t.Fatalf("runEvalsReview: %v", err)
	}
	p2 := pwPayload(t, h, tk.ID)
	if p2.ReviewEvalScores["en"] == 0 {
		t.Errorf("复核链路开启时复核评分必须落载荷，实得 %+v", p2.ReviewEvalScores)
	}
}
