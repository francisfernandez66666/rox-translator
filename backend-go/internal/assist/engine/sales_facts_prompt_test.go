package engine

import (
	"context"
	"strings"
	"testing"

	mainengine "translator/internal/engine"
)

func TestSalesFactsPromptBlockAndWiring(t *testing.T) {
	set := mainengine.SalesFactsSet{Facts: []mainengine.SalesFact{
		{Kind: "spec", Key: "lead_time", Value: "E2E公开交期 9 天", Lang: "zh"},
		{Kind: "spec", Key: "note", Value: "第一行\n第二行", Lang: "zh"},
	}}
	ctx := InjectSalesFacts(context.Background(), set)
	block := salesFactsPromptBlock(ctx)
	if !strings.Contains(block, "【产品事实】") || !strings.Contains(block, "E2E公开交期 9 天") {
		t.Fatalf("产品事实段没带上准入值: %q", block)
	}
	if strings.Contains(block, "第一行\n第二行") {
		t.Fatalf("事实值里的换行必须折叠，避免注入提示词结构: %q", block)
	}
	e := newTestEngine(t)
	if sys := e.buildSystemPrompt(ctx, nil, "zh"); !strings.Contains(sys, "E2E公开交期 9 天") {
		t.Fatal("产品事实没接进 buildSystemPrompt")
	}
	if sys := e.buildSystemPrompt(context.Background(), nil, "zh"); strings.Contains(sys, "E2E公开交期 9 天") {
		t.Fatal("无事实 ctx 不许把别轮产品事实带进提示词")
	}
}
