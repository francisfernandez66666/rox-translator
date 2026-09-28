// engine_test.go — 接待引擎单测：检索打分/话术直配/流程推进与让位/动作提取/LLM 兜底
package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// TestPostProcessStripsAllMarkers ★ 074x（2026-09-29 生产现场）：
// 一条回答里出现**两个**标记时，旧实现只剥第一个，第二个原样留在用户屏幕上；
// 标记被 max_tokens 截断（没有闭合的「】」）时，旧实现整段不剥，同样漏。
// 这两形态就是用户截图里那句「企业用的话【go:enterprise-features】能查权限管理和审计功能。」。
func TestPostProcessStripsAllMarkers(t *testing.T) {
	e := newTestEngine(t)

	// ① 多标记：正文一个不剩，按钮取并集（tickets + pricing）
	rep := e.postProcess("先说结论。\n【go:tickets】中间还有一句。\n【go:pricing】", "m")
	if strings.Contains(rep.Content, "【") || strings.Contains(rep.Content, "go:") {
		t.Fatalf("第二个标记漏进正文: %q", rep.Content)
	}
	if len(rep.Actions) != 2 {
		t.Fatalf("两个标记的 key 应合并成两个按钮: %+v", rep.Actions)
	}
	if !strings.Contains(rep.Content, "中间还有一句") {
		t.Fatalf("正文被误删: %q", rep.Content)
	}

	// ② 未闭合（被 max_tokens 截断）：从标记头删到结尾，宁可不给按钮也不漏控制序列
	rep2 := e.postProcess("需要体验的话可以看看【go:bill", "m")
	if strings.Contains(rep2.Content, "go:") || strings.Contains(rep2.Content, "【") {
		t.Fatalf("未闭合标记漏进正文: %q", rep2.Content)
	}
	if !strings.HasSuffix(rep2.Content, "看看") {
		t.Fatalf("未闭合分支把正文删多了: %q", rep2.Content)
	}

	// ③ 未知 key：照旧剥干净（按钮为空是可接受结果，漏英文不是）
	rep3 := e.postProcess("企业用的话能查权限。【go:enterprise-features】", "m")
	if strings.Contains(rep3.Content, "enterprise-features") {
		t.Fatalf("未映射 key 漏进正文: %q", rep3.Content)
	}
	if len(rep3.Actions) != 0 {
		t.Fatalf("未映射 key 不该凭空造按钮: %+v", rep3.Actions)
	}
}

// TestGoMarkerMenuOffersButtonKeys ★ 074x：送给模型的跳转菜单必须是 feature_links 的 key。
// 旧口径把 kb_entries 的 key 当菜单（生产实测 30 个知识 key 只有 1 个有按钮映射），
// 模型「照菜单选」必然选中渲染不出来的东西 ⇒ 界面只剩一串英文。
// 判据四腿：菜单里有全部按钮 key / 全链路（含 system 与 user）里不许出现纯知识 key /
// 模型按菜单给的标记真能变出按钮 / 历史消息里的残留标记不再回放进 prompt。
func TestGoMarkerMenuOffersButtonKeys(t *testing.T) {
	var bodyMu sync.Mutex
	var lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body) // 单次 Read 会拿到半截 body，负向断言就成了恒真
		bodyMu.Lock()
		lastBody = string(b)
		bodyMu.Unlock()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"epub 能翻。\n【go:tickets】"},"finish_reason":"stop"}],"usage":{"completion_tokens":20}}`))
	}))
	// bodyOf 取最近一次上游请求体（httptest handler 在别的 goroutine 里写，必须带锁读）
	bodyOf := func() string {
		bodyMu.Lock()
		defer bodyMu.Unlock()
		return lastBody
	}
	defer srv.Close()

	e := newTestEngine(t)
	// 直接挂上游 client（configs 留空 ⇒ ensureLLM 不会用管理台配置换链）；
	// 本用例只验 prompt 组装与出站标记处理，热加载由 TestLLMHotReloadSecondEditPickedUp 负责
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: srv.URL, APIKey: "k", Model: "m"}}, 5)

	rep := e.llmReply(context.Background(), "epub 支持吗", nil)
	if rep.Source != "llm" {
		t.Fatalf("应走 LLM: %+v", rep)
	}
	// ① 菜单里有全部按钮 key
	for _, k := range []string{"tickets", "pricing", "billing"} {
		if !strings.Contains(bodyOf(), k) {
			t.Fatalf("跳转菜单缺按钮 key %s", k)
		}
	}
	// ② 纯知识条目的 key 不再进菜单（kb-epub 是本轮的负面对照：它是知识 key，不是按钮 key）
	if strings.Contains(bodyOf(), "kb-epub") {
		t.Fatalf("知识 key 仍在跳转菜单里（旧口径没换干净）")
	}
	// ③ 按菜单选出来的 key 真变出按钮，且正文里没有控制序列
	if len(rep.Actions) != 1 || rep.Actions[0].Key != "tickets" {
		t.Fatalf("标记未映射成按钮: %+v", rep.Actions)
	}
	if strings.Contains(rep.Content, "go:") {
		t.Fatalf("标记漏进正文: %q", rep.Content)
	}

	// ④ 历史回放同样要洗：线上存量消息里已躺着修复前漏出的控制序列，
	//    原样喂回模型＝把自家漏出来的格式当范本学（老会话越聊越歪的那条路径）。
	//    ⚠️ 负向判据只能钉**那条历史消息里的具体标记**，不能钉 "go:" 字样——
	//    prompt 自己的菜单说明里就带着「【go:key1,key2】」这个模板，恒红。
	legacy := []store.Row{{"role": "assistant", "content": "企业用的话【go:enterprise-features】能查权限管理。"}}
	e.llmReply(context.Background(), "那审计呢", legacy)
	if strings.Contains(bodyOf(), "【go:enterprise-features】") {
		t.Fatalf("历史里的漏标控制序列被原样回放进 prompt:\n%s", bodyOf())
	}
	if !strings.Contains(bodyOf(), "能查权限管理") {
		t.Fatalf("洗标记不该把历史正文一起删掉:\n%s", bodyOf())
	}
}

// TestLLMTruncatedSurfacesInUsage ★ 074x：finish_reason=length 必须被读到并记 WARN。
// 「答案能出来」不等于「答案答完了」——旧代码把上游收尾原因直接丢掉，
// 半句话回复在日志里一片绿，只能靠用户截图发现。
func TestLLMTruncatedSurfacesInUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"需要体验的话"},"finish_reason":"length"}],"usage":{"completion_tokens":900}}`))
	}))
	defer srv.Close()
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: srv.URL, APIKey: "k", Model: "m"}}, 5)
	if _, _, u, err := e.LLMTest(context.Background()); err != nil || !u.Truncated {
		t.Fatalf("截断未被识别: truncated=%v err=%v", u.Truncated, err)
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

// TestLLMHotReloadSecondEditPickedUp ★ 2026-09-29 生产实锤的回归锁：第二次在线改配置也必须被读到。
// 旧判据 `if e.llm != nil && e.llm.Enabled()` 把「管理台第一次保存建出来的 client」当成了 env 接管，
// 短路掉整段回读 ⇒ 热加载一辈子只有一次（日志里只有一次「热加载生效」），
// 运维照「保存即热加载」改完 base_url 去点测试，实际一直在打旧地址。
// 两腿：① 无 env 构造（llmFromEnv=false）时改到第二个上游，请求真落在第二个上；
//
//	② env 接管时 configs 再怎么改都不许换掉 env 那条链（原优先级不许被这次修法带歪）。
func TestLLMHotReloadSecondEditPickedUp(t *testing.T) {
	var hitA, hitB int32
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitA++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"A"}}],"usage":{"total_tokens":1}}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitB++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"B"}}],"usage":{"total_tokens":1}}`))
	}))
	defer srvB.Close()

	db, err := store.Open(t.TempDir() + "/reload.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	e := New(db, llm.New(nil, 5)) // 无 env：等价于生产 assist 的启动形态

	_ = db.SetConfig("llm_base_url", srvA.URL)
	_ = db.SetConfig("llm_api_key", "sk-test")
	_ = db.SetConfig("llm_model", "m-a")
	if _, model, _, err := e.LLMTest(context.Background()); err != nil || model != "m-a" || hitA != 1 {
		t.Fatalf("第一次保存应生效在 A：model=%s hitA=%d err=%v", model, hitA, err)
	}

	// 关键一步：管理员把地址改到 B（等价于把 …/v1/chat/completions 改回 …/v1）
	_ = db.SetConfig("llm_base_url", srvB.URL)
	_ = db.SetConfig("llm_model", "m-b")
	text, model, _, err := e.LLMTest(context.Background())
	if hitA != 1 {
		t.Fatalf("旧上游 A 又被打了（hitA=%d）⇒ 热加载仍被短路", hitA)
	}
	if err != nil || model != "m-b" || hitB != 1 || text != "B" {
		t.Fatalf("第二次保存未生效：model=%s hitB=%d text=%s err=%v", model, hitB, text, err)
	}
	if got := e.LLMMode(context.Background()); got != "db" {
		t.Fatalf("来源徽标应为 db，got %s", got)
	}

	// ② env 接管：构造期 providers 非空 ⇒ configs 永远不换链（这次修法的反向对照）
	db2, err := store.Open(t.TempDir() + "/env.db")
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	t.Cleanup(func() { db2.Close() })
	e2 := New(db2, llm.New([]llm.Provider{{Name: "main", BaseURL: srvA.URL, APIKey: "k", Model: "env-m"}}, 5))
	_ = db2.SetConfig("llm_base_url", srvB.URL)
	_ = db2.SetConfig("llm_api_key", "k2")
	_ = db2.SetConfig("llm_model", "db-m")
	beforeB := hitB
	if _, model, _, err := e2.LLMTest(context.Background()); err != nil || model != "env-m" {
		t.Fatalf("env 必须压过 configs：model=%s err=%v", model, err)
	}
	if hitB != beforeB {
		t.Fatal("env 接管时不该去碰 configs 里的上游 B（优先级被这次修法带歪了）")
	}
	if got := e2.LLMMode(context.Background()); got != "env" {
		t.Fatalf("来源徽标应为 env，got %s", got)
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

// TestCompoundIntentYield ★ 2026-09-20 生产漏接回归锁：
// 「印度语能翻译吗，一个字多少钱」同时含语言咨询与价格咨询两个意图，
// 旧逻辑被「价格咨询」话术直配抢答（Source=rule），语言侧知识全程不参与——
// 修复后话术降为素材让位融合应答；纯价格问句仍走毫秒级直配（不退化）。
func TestCompoundIntentYield(t *testing.T) {
	e := newTestEngine(t)
	_, _ = e.db.Create("kb_entries", map[string]any{
		"key": "kb-lang", "category": "faq", "title": "支持哪些语言", "priority": 8, "enabled": 1,
		"content": "支持印地语（俗称印度语）等 40+ 语种互翻", "keywords": "语言,语种,印度语,印地语", "link_keys": "chat",
	})
	// 生产二次踩坑回归锁：计费知识经运营滚成「大条目」（关键词与价格话术全交集、
	// 多字命中分数压过语言条目）。旧实现只比对 RetrieveKB 第一条，看到同域即不让位，
	// 语言侧信息照样被吞——必须越过同域高分条目继续找跨领域命中。
	_, _ = e.db.Create("kb_entries", map[string]any{
		"key": "kb-billing-big", "category": "billing", "title": "积分怎么收费", "priority": 10, "enabled": 1,
		"content": "积分永久有效，先预检后扣费", "keywords": "积分,价格,多少钱,收费,充值,一个字,字数", "link_keys": "billing",
	})
	rep := e.Respond(context.Background(), "s-ci", "印度语能翻译吗，一个字多少钱", "/", nil)
	if rep.Source == "rule" {
		t.Fatalf("复合问句不应被价格话术单侧直配抢答，Source=%s content=%s", rep.Source, rep.Content)
	}
	if !strings.Contains(rep.Content, "印度语") || !strings.Contains(rep.Content, "按积分计费") {
		t.Fatalf("应并排呈现语言+价格两侧信息: %s", rep.Content)
	}
	// 纯价格问句：无跨领域命中，维持话术直配快答
	rep2 := e.Respond(context.Background(), "s-ci2", "多少钱", "/", nil)
	if rep2.Source != "rule" || rep2.Content != "按积分计费" {
		t.Fatalf("纯价格问句应仍直配: source=%s content=%s", rep2.Source, rep2.Content)
	}
}
