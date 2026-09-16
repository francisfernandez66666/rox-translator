// engine_test.go — 接待引擎单测：检索打分/话术直配/流程推进与让位/动作提取/LLM 兜底
package engine

import (
	"strings"
	"testing"

	"ai-assist/internal/llm"
	"ai-assist/internal/store"
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
	rep := e.Respond("s", "多少钱啊", "/", nil)
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
	rep := e.Respond("s1", "我是新手", "/", nil)
	if rep.Source != "flow" || !strings.HasPrefix(rep.Content, "第一步") || len(rep.Actions) != 1 {
		t.Fatalf("step0: %+v", rep)
	}
	// 推进第二步
	rep = e.Respond("s1", "好", "/", nil)
	if !strings.HasPrefix(rep.Content, "第二步") {
		t.Fatalf("step1: %+v", rep)
	}
	// 走完 → 退出流程
	rep = e.Respond("s1", "好", "/", nil)
	if rep.Source != "flow" {
		t.Fatalf("done msg: %+v", rep)
	}
	if s, _ := e.db.SessionRow("s1"); asStr(s["in_flow"]) != "" {
		t.Fatal("flow should be cleared")
	}
	// 退出后再问价格 → 话术直配（不再被流程吞掉）
	rep = e.Respond("s1", "多少钱", "/", nil)
	if rep.Source != "rule" {
		t.Fatalf("after flow: %+v", rep)
	}
}

// TestFlowYield 流程进行中命中其他意图 → 退出让位
func TestFlowYield(t *testing.T) {
	e := newTestEngine(t)
	newSession(t, e, "s2")
	_ = e.Respond("s2", "我是新手", "/", nil) // 进入流程
	rep := e.Respond("s2", "多少钱", "/", nil)
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
	rep := e.llmReply("完全无关的问题xyz", nil)
	if rep.Source != "fallback" || rep.Content == "" {
		t.Fatalf("fallback empty: %+v", rep)
	}
	rep = e.llmReply("epub 支持", nil)
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
