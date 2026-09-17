// engine_test.go — 接待引擎单测：检索打分/话术直配/流程推进与让位/动作提取/LLM 兜底
package engine

import (
	"context"
	"strings"
	"testing"

	"translator/internal/assist/llm"
	"translator/internal/assist/store"
)

// newTestEngine 建带固定夹具的引擎（临时库）
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/e.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// 知识库：两条不同优先级
	_, _ = db.Create("kb_entries", map[string]any{
		"key": "kb-epub", "category": "usage", "title": "格式", "priority": 5, "enabled": 1,
		"content": "支持 epub", "keywords": "epub,电子书", "link_keys": "tickets",
	})
	_, _ = db.Create("kb_entries", map[string]any{
		"key": "kb-billing", "category": "billing", "title": "计费", "priority": 9, "enabled": 1,
		"content": "积分计费", "keywords": "积分,价格", "link_keys": "pricing,billing",
	})
	_, _ = db.Create("kb_entries", map[string]any{
		"key": "kb-off", "category": "faq", "title": "停用", "priority": 9, "enabled": 0,
		"content": "不可见", "keywords": "积分", "link_keys": "",
	})
	// 功能入口
	for k, u := range map[string]string{"tickets": "/tickets", "pricing": "/pricing", "billing": "/billing"} {
		_, _ = db.Create("feature_links", map[string]any{
			"key": k, "name": k, "url": u, "ftype": "route", "sort": 10, "enabled": 1,
		})
	}
	// 话术
	_, _ = db.Create("scripts", map[string]any{
		"key": "sc-price", "stype": "keyword", "title": "价格", "priority": 8, "enabled": 1,
		"content": "按积分计费", "keywords": "多少钱,价格", "link_keys": "pricing",
	})
	_, _ = db.Create("scripts", map[string]any{
		"key": "sc-greet", "stype": "greeting", "title": "欢迎", "priority": 9, "enabled": 1,
		"content": "你好呀", "keywords": "", "link_keys": "",
	})
	// 流程：两步
	_, _ = db.Create("flows", map[string]any{
		"key": "fl-onboard", "name": "上手", "priority": 0, "enabled": 1,
		"trigger_keywords": "新手,上手",
		"steps_json":       `[{"ask":"第一步","wait":true,"actions":["tickets"]},{"ask":"第二步","wait":true,"actions":["pricing"]}]`,
	})
	_ = db.SetConfig("welcome", "欢迎词W")
	return New(db, llm.New(nil, 5)) // 无 LLM → 全部走规则/兜底
}

// newSession 测试辅助：先建会话（生产中由 API 层 ensureSession 完成，引擎依赖其存在）
func newSession(t *testing.T, e *Engine, sid string) {
	t.Helper()
	if err := e.db.EnsureSession(sid, "/"); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
}

// TestGreeting 欢迎词优先读 config，回落 greeting 话术
func TestGreeting(t *testing.T) {
	e := newTestEngine(t)
	if e.Greeting() != "你好呀" {
		t.Fatalf("greeting: %s", e.Greeting())
	}
	_ = e.db.SetConfig("welcome", "")
	// config 空时 Greeting() 走话术
	if e.Greeting() != "你好呀" {
		t.Fatalf("greeting fallback: %s", e.Greeting())
	}
}

// TestRetrieveKB 检索排序：命中数×权重，停用不可见
func TestRetrieveKB(t *testing.T) {
	e := newTestEngine(t)
	hits := e.RetrieveKB("积分怎么收费", 3)
	if len(hits) != 1 || hits[0].title != "计费" {
		t.Fatalf("hits: %+v", hits) // kb-off 停用，kb-epub 无命中
	}
	all := e.RetrieveKB("epub", 3)
	if len(all) != 1 || !strings.Contains(all[0].content, "epub") {
		t.Fatalf("epub hit: %+v", all)
	}
}

// TestScriptMatch 话术直配与动作（link_keys → 功能入口）
func TestScriptMatch(t *testing.T) {
	e := newTestEngine(t)
	sc, ok := e.MatchScript("多少钱啊")
	if !ok || asStr(sc["key"]) != "sc-price" {
		t.Fatalf("script match: %v %v", ok, sc)
	}
	rep := e.Respond(context.Background(), "s", "多少钱啊", "/", nil)
	if rep.Source != "rule" || rep.Content != "按积分计费" {
		t.Fatalf("respond: %+v", rep)
	}
	if len(rep.Actions) != 1 || rep.Actions[0].Key != "pricing" {
		t.Fatalf("actions: %+v", rep.Actions)
	}
}

// TestFlowLifecycle 流程：触发→逐步推进→走完退出→让位给话术
func TestFlowLifecycle(t *testing.T) {
	e := newTestEngine(t)
	newSession(t, e, "s1")
	// 触发第一步
	rep := e.Respond(context.Background(), "s1", "我是新手", "/", nil)
	if rep.Source != "flow" || !strings.HasPrefix(rep.Content, "第一步") || len(rep.Actions) != 1 {
		t.Fatalf("step0: %+v", rep)
	}
	// 推进第二步
	rep = e.Respond(context.Background(), "s1", "好", "/", nil)
	if !strings.HasPrefix(rep.Content, "第二步") {
		t.Fatalf("step1: %+v", rep)
	}
	// 走完 → 退出流程
	rep = e.Respond(context.Background(), "s1", "好", "/", nil)
	if rep.Source != "flow" {
		t.Fatalf("done msg: %+v", rep)
	}
	if s, _ := e.db.SessionRow("s1"); asStr(s["in_flow"]) != "" {
		t.Fatal("flow should be cleared")
	}
	// 退出后再问价格 → 话术直配（不再被流程吞掉）
	rep = e.Respond(context.Background(), "s1", "多少钱", "/", nil)
	if rep.Source != "rule" {
		t.Fatalf("after flow: %+v", rep)
	}
}

// TestFlowYield 流程进行中命中其他意图 → 退出让位
func TestFlowYield(t *testing.T) {
	e := newTestEngine(t)
	newSession(t, e, "s2")
	_ = e.Respond(context.Background(), "s2", "我是新手", "/", nil) // 进入流程
	rep := e.Respond(context.Background(), "s2", "多少钱", "/", nil)
	if rep.Source != "rule" {
		t.Fatalf("yield to script: %+v", rep)
	}
	if s, _ := e.db.SessionRow("s2"); asStr(s["in_flow"]) != "" {
		t.Fatal("flow should be cleared on yield")
	}
}

// TestPostProcess LLM 出站标记提取：【go:key】→ 动作按钮并从正文剥离
func TestPostProcess(t *testing.T) {
	e := newTestEngine(t)
	rep := e.postProcess("推荐你去建工单。\n【go:tickets,billing】", "m1")
	if rep.Model != "m1" || rep.Source != "llm" {
		t.Fatalf("meta: %+v", rep)
	}
	if strings.Contains(rep.Content, "go:") {
		t.Fatalf("marker leaked: %s", rep.Content)
	}
	if len(rep.Actions) != 2 {
		t.Fatalf("actions: %+v", rep.Actions)
	}
}

// TestFallbackReply 无 LLM 且无命中时的兜底文案；有命中时直出知识
func TestFallbackReply(t *testing.T) {
	e := newTestEngine(t)
	rep := e.llmReply(context.Background(), "完全无关的问题xyz", nil)
	if rep.Source != "fallback" || rep.Content == "" {
		t.Fatalf("fallback empty: %+v", rep)
	}
	rep = e.llmReply(context.Background(), "epub 支持", nil)
	if !strings.Contains(rep.Content, "支持 epub") || len(rep.Actions) != 1 {
		t.Fatalf("kb fallback: %+v", rep)
	}
}

// TestFlowByKey 流程解析
func TestFlowByKey(t *testing.T) {
	e := newTestEngine(t)
	name, steps, ok := e.FlowByKey("fl-onboard")
	if !ok || len(steps) != 2 || name != "上手" {
		t.Fatalf("flow: %v %v %v", ok, name, steps)
	}
	if _, _, ok := e.FlowByKey("nope"); ok {
		t.Fatal("unknown flow should not match")
	}
}

// ============================================================================
// ★ R0 批次回归（2026-09-16）：同义词归一 / LLM 热加载 / 兜底改造与未答登记
// ============================================================================

// TestSynonymHit R0.1：configs.synonyms 归一表命中（「充钱」→ 关键词「充值」）
func TestSynonymHit(t *testing.T) {
	e := newTestEngine(t)
	_ = e.db.SetConfig("synonyms", "价格=充钱|交钱")
	if n := e.hitScore("怎么充钱", "价格,计费"); n != 1 {
		t.Fatalf("synonym miss: n=%d", n)
	}
	// 无关同义词组不命中
	if n := e.hitScore("怎么充钱", "epub"); n != 0 {
		t.Fatalf("false positive: n=%d", n)
	}
	// 改表后指纹失效重载
	_ = e.db.SetConfig("synonyms", "价格=换汇")
	if e.synonymHit("怎么充钱", "价格") {
		t.Fatal("stale synonyms still matching")
	}
}

// TestSynonymEndToEnd R0.1：口语问句「怎么充钱」经同义词命中 KB（原三层脱靶场景）
func TestSynonymEndToEnd(t *testing.T) {
	e := newTestEngine(t)
	_, _ = e.db.Create("kb_entries", map[string]any{
		"key": "kb-recharge", "title": "怎么充值", "priority": 9, "enabled": 1,
		"content": "去充值与账单页", "keywords": "充值,付款,支付", "link_keys": "billing",
	})
	_ = e.db.SetConfig("synonyms", "充值=充钱|交钱")
	rep := e.Respond(context.Background(), "s-syn", "怎么充钱", "/", nil)
	// R0.2 验收：不再输出空承诺兜底话术，正确命中充值知识并带入口按钮
	if strings.Contains(rep.Content, "先记下来") || !strings.Contains(rep.Content, "充值与账单") || len(rep.Actions) == 0 {
		t.Fatalf("synonym e2e: %+v", rep)
	}
}

// TestLLMHotReload R0.4：configs 写入 LLM 配置 → 无 env 时惰性重建生效
func TestLLMHotReload(t *testing.T) {
	e := newTestEngine(t)
	if e.LLMMode(context.Background()) != "" {
		t.Fatalf("initial mode: %s", e.LLMMode(context.Background()))
	}
	_ = e.db.SetConfig("llm_base_url", "http://127.0.0.1:1/v1") // 不可达端口，仅验证 Enabled 翻转
	_ = e.db.SetConfig("llm_api_key", "sk-test")
	_ = e.db.SetConfig("llm_model", "m1")
	if e.LLMMode(context.Background()) != "db" {
		t.Fatalf("after db cfg: %s", e.LLMMode(context.Background()))
	}
	// 测试连通应报错但 client 已构建
	if _, _, _, err := e.LLMTest(context.Background()); err == nil {
		t.Fatal("unreachable endpoint should error")
	}
}

// TestLLMEnvPriority R0.4：env 显式接入（构造时 providers 非空）不被 configs 覆盖
func TestLLMEnvPriority(t *testing.T) {
	db, _ := store.Open(t.TempDir() + "/e2.db")
	defer db.Close()
	e := New(db, llm.New([]llm.Provider{{Name: "main", BaseURL: "http://env-host", APIKey: "k", Model: "env-m"}}, 5))
	_ = db.SetConfig("llm_base_url", "http://db-host")
	_ = db.SetConfig("llm_api_key", "k2")
	_ = db.SetConfig("llm_model", "db-m")
	if e.LLMMode(context.Background()) != "env" {
		t.Fatalf("env should win: %s", e.LLMMode(context.Background()))
	}
}

// TestUnanswered R0.2：零命中登记未答问题（去重+上限）
func TestUnanswered(t *testing.T) {
	e := newTestEngine(t)
	e.recordUnanswered("怎么充钱")
	e.recordUnanswered("怎么充钱") // 去重
	e.recordUnanswered("量子翻译")
	got := e.UnansweredQuestions()
	if len(got) != 2 || got[0] != "怎么充钱" || got[1] != "量子翻译" {
		t.Fatalf("unanswered: %v", got)
	}
}
