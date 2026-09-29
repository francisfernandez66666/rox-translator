// engine_test.go — 接待引擎单测：检索打分/话术直配/流程推进与让位/动作提取/LLM 兜底
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	// ★ 081x（2026-09-29）：把现值注入拨向一个必然拒绝连接的端口。
	// 默认值是 http://127.0.0.1:8787（生产主服务），本机开发时那个端口上可能真起着服务，
	// 于是「所有引擎单测」会随开发者机器上有没有跑主服务而拿到不同的 system prompt——
	// 需要断言现值的用例自己起桩覆盖这个键，其余用例一律走空现值分支（软路径按设计整段省略）。
	_ = db.SetConfig("main_base_url", "http://127.0.0.1:1")
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
	rep := e.Respond(context.Background(), "s", "多少钱啊", "/", "zh", nil)
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
	rep := e.Respond(context.Background(), "s1", "我是新手", "/", "zh", nil)
	if rep.Source != "flow" || !strings.HasPrefix(rep.Content, "第一步") || len(rep.Actions) != 1 {
		t.Fatalf("step0: %+v", rep)
	}
	// 推进第二步
	rep = e.Respond(context.Background(), "s1", "好", "/", "zh", nil)
	if !strings.HasPrefix(rep.Content, "第二步") {
		t.Fatalf("step1: %+v", rep)
	}
	// 走完 → 退出流程
	rep = e.Respond(context.Background(), "s1", "好", "/", "zh", nil)
	if rep.Source != "flow" {
		t.Fatalf("done msg: %+v", rep)
	}
	if s, _ := e.db.SessionRow("s1"); asStr(s["in_flow"]) != "" {
		t.Fatal("flow should be cleared")
	}
	// 退出后再问价格 → 话术直配（不再被流程吞掉）
	rep = e.Respond(context.Background(), "s1", "多少钱", "/", "zh", nil)
	if rep.Source != "rule" {
		t.Fatalf("after flow: %+v", rep)
	}
}

// TestFlowYield 流程进行中命中其他意图 → 退出让位
func TestFlowYield(t *testing.T) {
	e := newTestEngine(t)
	newSession(t, e, "s2")
	_ = e.Respond(context.Background(), "s2", "我是新手", "/", "zh", nil) // 进入流程
	rep := e.Respond(context.Background(), "s2", "多少钱", "/", "zh", nil)
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

// TestCanonicalizeGoMarkers ★ 082x 增补（2026-09-29 现网复问抓到）：模型把动作标记的
// 括号／分隔符写歪（「[go editor,packages]」）时，extractGoMarkers 只认规范形态
// 「【go:…」，畸形标记就整串英文原样留在用户屏幕上——挂件气泡里出现
// 「You can go to the【…】page. [go editor,packages]」正是这一条。
// 本锁按「畸形必归一、人话一个字不许动」两侧同时钉：
//   - 正向：四种括号×两种分隔符的畸形写法都要变出按钮、正文零残留；
//   - 负向：含 "go" 的正常英文括注必须**逐字节不变**（吃掉用户能读的一句话，
//     比漏一个控制序列严重得多——这条判据写坏的代价是静默删正文，必须反向钉住）。
func TestCanonicalizeGoMarkers(t *testing.T) {
	e := newTestEngine(t) // 夹具里的功能卡：tickets / pricing / billing

	// ① 正向：畸形写法一律归一并出按钮
	type normCase struct {
		in      string
		actions int
		why     string
	}
	norm := []normCase{
		{"可以看看[go tickets,billing]", 2, "半角括号＋空格分隔（现网原样）"},
		{"可以看看【go tickets】", 1, "全角括号＋空格分隔"},
		{"可以看看[go：pricing]", 1, "半角括号＋全角冒号"},
		{"可以看看（go：tickets）", 1, "圆括号＋全角冒号"},
		{"可以看看【go: tickets】", 1, "规范括号＋冒号后带空格"},
		{"混合 [go tickets, 未知东西] 结尾", 1, "夹一段认不出的说明也要归一（未知段不造按钮）"},
	}
	for _, c := range norm {
		rep := e.postProcess(c.in, "m")
		if len(rep.Actions) != c.actions {
			t.Fatalf("%s：按钮 %d 个，期望 %d（正文 %q）", c.why, len(rep.Actions), c.actions, rep.Content)
		}
		low := strings.ToLower(rep.Content)
		if strings.Contains(low, "go ") || strings.Contains(low, "go:") || strings.Contains(low, "go：") {
			t.Fatalf("%s：控制序列漏进正文 %q", c.why, rep.Content)
		}
		if !strings.Contains(rep.Content, "可以看看") && !strings.Contains(rep.Content, "混合") {
			t.Fatalf("%s：归一时把正文删没了 %q", c.why, rep.Content)
		}
	}

	// ② 负向：正常人话必须逐字节不变（这是本函数唯一的"误伤"面，必须反向钉死）
	safe := []string{
		"See [go to the pricing page] for details.",                 // 整段只有一个逗号分段，认不出任何 key
		"[go now](/x) is a link label, not a marker.",               // markdown 链接文字
		"Good pricing here.",                                        // 连括号都没有
		"[go to the store, please]",                                 // 两段都不是功能卡 key
		"（gogogo 冲呀）",                                               // 括号里根本不是 go＋分隔符
		"这是中文括注（说明一下），不含任何标记。",                                      // 无 "go"，走廉价退场
		"[go to the pricing page and buy tickets or billing today]", // 长句：没有一个分段恰好等于 key
	}
	for _, s := range safe {
		if got := e.canonicalizeGoMarkers(s); got != s {
			t.Fatalf("正常人话被吃了：\n 输入 %q\n 输出 %q", s, got)
		}
	}

	// ③ 未闭合（max_tokens 截断）：带冒号或认得出功能卡 → 删到结尾；正常人话 → 原样留
	if got := e.canonicalizeGoMarkers("需要体验的话可以看看[go tickets"); strings.Contains(got, "go") {
		t.Fatalf("未闭合的畸形标记没删净: %q", got)
	}
	if !strings.HasSuffix(e.canonicalizeGoMarkers("需要体验的话可以看看【go:pricing"), "看看") {
		t.Fatal("未闭合分支把正文删多了")
	}
	if s := "这句话被截断了 [go to the pricing"; e.canonicalizeGoMarkers(s) != s {
		t.Fatalf("未闭合的正常人话被误删: %q", e.canonicalizeGoMarkers(s))
	}

	// ④ 取不到功能卡表（库里没配／DB 故障）时**不做**歧义归一：宁可漏剥不可误伤。
	// 空表下任何一段都"认不出来"，若判据写成"找不到不认识的就算通过"，
	// DB 一抖就会把用户正文里的英文括注整段吃掉——所以这里要的是"照原样送出"。
	{
		db2, err := store.Open(t.TempDir() + "/empty.db")
		if err != nil {
			t.Fatalf("open empty: %v", err)
		}
		t.Cleanup(func() { _ = db2.Close() })
		e2 := New(db2, llm.New(nil, 5)) // 库里零张功能卡
		if s := "可以看看[go tickets,billing]"; e2.canonicalizeGoMarkers(s) != s {
			t.Fatalf("功能卡表为空时仍做了歧义归一: %q", e2.canonicalizeGoMarkers(s))
		}
		// 对照腿：带冒号的规范意图**不依赖**卡表，全链路照样剥干净（否则卡表为空的库会漏控制序列）
		if got := e2.postProcess("可以看看【go:whatever】", "m"); strings.Contains(got.Content, "go:") {
			t.Fatalf("带冒号形态在空卡表下没剥净: %q", got.Content)
		}
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

	rep := e.llmReply(context.Background(), "epub 支持吗", nil, "zh")
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
	e.llmReply(context.Background(), "那审计呢", legacy, "zh")
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
	rep := e.llmReply(context.Background(), "完全无关的问题xyz", nil, "zh")
	if rep.Source != "fallback" || rep.Content == "" {
		t.Fatalf("fallback empty: %+v", rep)
	}
	rep = e.llmReply(context.Background(), "epub 支持", nil, "zh")
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
	rep := e.Respond(context.Background(), "s-syn", "怎么充钱", "/", "zh", nil)
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
	rep := e.Respond(context.Background(), "s-ci", "印度语能翻译吗，一个字多少钱", "/", "zh", nil)
	if rep.Source == "rule" {
		t.Fatalf("复合问句不应被价格话术单侧直配抢答，Source=%s content=%s", rep.Source, rep.Content)
	}
	if !strings.Contains(rep.Content, "印度语") || !strings.Contains(rep.Content, "按积分计费") {
		t.Fatalf("应并排呈现语言+价格两侧信息: %s", rep.Content)
	}
	// 纯价格问句：无跨领域命中，维持话术直配快答
	rep2 := e.Respond(context.Background(), "s-ci2", "多少钱", "/", "zh", nil)
	if rep2.Source != "rule" || rep2.Content != "按积分计费" {
		t.Fatalf("纯价格问句应仍直配: source=%s content=%s", rep2.Source, rep2.Content)
	}
}

// TestSystemPromptCarriesToneSpec ★ 080x（2026-09-29「temperature 改到 1 还是冷冰冰」）：
// 音色那段规矩必须真出现在组装后的 system prompt 里。
// 旧口径只限长度（「2-4句话，120字以内，别啰嗦」），既没要求「先接住用户这句话」，
// 也没给收尾方式——模型的最优解就是把知识库那条「企业版能力：①…②…③…⑤」编号清单
// 逐条压缩念一遍（现网回复正是这个形状），用户读到的就是产品参数表，不是人在说话。
// 判据三腿：新结构要求在 / 库里 tone_rules 现值必须压过代码默认（改口气不必发版）/ 旧长度档不许复活。
func TestSystemPromptCarriesToneSpec(t *testing.T) {
	e := newTestEngine(t)
	sys := e.buildSystemPrompt(context.Background(), nil, "zh")
	// ① 新的说话方式五要素：接话 → 只挑最相关（且不许编号列清单）→ 收尾给下一步 → 数字守口径 → 正反对照片
	for _, want := range []string{"先接住", "最相关", "别用「①②③」列清单", "200 字以内", "数字一律照抄知识里的原文", "反面示例", "正面示例"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("说话方式缺 %q：\n%s", want, sys)
		}
	}
	// 人设默认值也在（第一段），且拼在说话方式之前
	if !strings.Contains(sys, "AI翻译平台的销售顾问") || strings.Index(sys, "销售顾问") > strings.Index(sys, "先接住") {
		t.Fatalf("人设没拼在说话方式之前：\n%s", sys)
	}
	// ② 旧口径不得复活——它就是把回复打成参数表的那一条
	for _, gone := range []string{"2-4句话，120字以内", "【语气铁律】"} {
		if strings.Contains(sys, gone) {
			t.Fatalf("旧语气口径复活：%s", gone)
		}
	}
	// ③ 库里现值压过代码默认（与 max_tokens 同一条教训：代码默认会被库里现值盖住，
	//    反过来说库里必须能盖住，运营后台改语气才是真能改）
	_ = e.db.SetConfig("tone_rules", "只说一句话：好的。")
	sys2 := e.buildSystemPrompt(context.Background(), nil, "zh")
	if !strings.Contains(sys2, "只说一句话：好的。") {
		t.Fatalf("tone_rules 库里现值没生效：\n%s", sys2)
	}
	if strings.Contains(sys2, "先接住") || strings.Contains(sys2, "正面示例") {
		t.Fatalf("默认语气没被库值整体替换（会和运营写的叠成两套规矩）：\n%s", sys2)
	}
}

// TestTemperatureDoesNotChangeTone ★ 080x：把「温度不是音色旋钮」钉成机械断言。
// 现场：用户把 temperature 从 0.7 拧到 1，回复照旧冷——同一问题的两条现网回复是
// 「同样的四件事、同样的顺序、同样的长度，只换了词的摆放」。
// 原因就在这条断言的两半上：换温度 ⇒ 出站请求的 temperature 字段确实变了（配置链路是通的，
// 不是"改了没生效"那种故障），但 messages（含 system prompt）逐字节不变 ⇒ 语气一个字没动。
// 以后谁想把语气挂到温度上，这条直接红灯，逼他回到真旋钮（persona / tone_rules）。
func TestTemperatureDoesNotChangeTone(t *testing.T) {
	var bodyMu sync.Mutex
	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodyMu.Lock()
		last = string(b)
		bodyMu.Unlock()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"好的。"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: srv.URL, APIKey: "k", Model: "m"}}, 5)

	// askOnce 以指定温度问一次，返回（出站 temperature 文本, 出站 messages 原文）
	askOnce := func(temp string) (string, string) {
		bodyMu.Lock()
		last = ""
		bodyMu.Unlock()
		if err := e.db.SetConfig("temperature", temp); err != nil {
			t.Fatalf("set temperature: %v", err)
		}
		e.llmReply(context.Background(), "epub 支持吗", nil, "zh")
		bodyMu.Lock()
		body := last
		bodyMu.Unlock()
		if body == "" {
			t.Fatalf("温度 %s 下没打到上游", temp)
		}
		var v struct {
			Temperature float64         `json:"temperature"`
			Messages    json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("解析出站体: %v\n%s", err, body)
		}
		return fmt.Sprintf("%g", v.Temperature), string(v.Messages)
	}

	t1, m1 := askOnce("0.2")
	t2, m2 := askOnce("1")
	if t1 == t2 {
		t.Fatalf("两档温度出站字段相同（%s），配置链路断了", t1)
	}
	if m1 != m2 {
		t.Fatalf("换温度连 messages 都换了——语气不该由温度承担：\n%s\n---\n%s", m1, m2)
	}
}

// TestSystemPromptCarriesPromiseBoundary ★ 081x（2026-09-29 用户指令
// 「涉及到价格、套餐、能力之类的东西，要严格按 RAG、使用系统配置口径」
// →「不光这些，你看一下系统实际能力，严格按系统能力和承诺来，不造额外承诺」）：
// 承诺边界必须真拼进 system prompt，且**必须与语气段各自独立**。
// 现场取证两条编造：消息 132 报出「10 万+高频行业词」（30 条启用知识里 0 出处），
// 又顺嘴答应「PPT 里的动画」（internal/fileproc 下 animation/transition 零命中，pptx 只替换文字节点）。
// 前者是数字编造（语气段第 6 条已拦），后者是**功能清单外的事**——语气段拦不住，必须有事实闸。
// 判据五腿：三档结构在 / 关键禁语在 / 正文零内部实现路径 / 库里 promise_rules 能整体替换默认 /
// 改语气（tone_rules）绝不连带把事实闸擦掉（这条是「为什么单独一个键」的全部理由）。
func TestSystemPromptCarriesPromiseBoundary(t *testing.T) {
	e := newTestEngine(t)
	sys := e.buildSystemPrompt(context.Background(), nil, "zh")
	for _, want := range []string{
		"【承诺边界】",
		// 第一档（确实做到）与第二档（须带前提）的实锚
		"字幕和数据文件 srt、vtt、json、yaml", "交付形态是「原文+译文」两列的 xlsx 对照表",
		"超链接包住的那截文字目前不进翻译", "先扣快过期的",
		// 第三档（明确不支持）——现网就是在这两条上翻过车
		"PPT 里的动画、切换效果", "不做识别提取", "「40+」「上百种」「全球语言都能翻」这类说法一个都不许说",
	} {
		if !strings.Contains(sys, want) {
			t.Fatalf("承诺边界缺 %q：\n%s", want, sys)
		}
	}
	// ★ 正文不许带内部实现路径：这段是要拼进 prompt 的，9B 模型会照抄看见的字符串，
	//   「（依据：internal/engine/file.go）」念到客户屏幕上＝实现细节对外泄漏。
	//   取证位置只准留在 promise.go 的 Go 注释里。
	for _, leak := range []string{"internal/", "file.go", ".go ", "writebackDelivery", "AnydocFormats"} {
		if strings.Contains(defaultPromiseRules, leak) {
			t.Fatalf("承诺边界正文漏出内部实现位置 %q，会被模型念给客户", leak)
		}
	}
	// 段次：人设 → 语气 → 承诺（承诺排在语气前面会让「怎么说」盖住「能说什么」）。
	// ⚠️ 定位承诺段必须用整串段首：语气段第 7 条自己也写着「按【承诺边界】的分档口径说」，
	//    只搜那五个字会把语气段里的提及当成段首，次序判据直接失真（本断言首跑即此假绿形态）。
	if !(strings.Index(sys, "销售顾问") < strings.Index(sys, "先接住") &&
		strings.Index(sys, "先接住") < strings.Index(sys, promiseTestHead) &&
		strings.Index(sys, "积分有效期按【承诺边界】的分档") < strings.Index(sys, promiseTestHead)) {
		t.Fatalf("人设/语气/承诺拼装次序错：\n%s", sys)
	}
	// 库里现值整体替换默认（运营可收紧，不许和默认叠成两套）
	_ = e.db.SetConfig("promise_rules", "只允许回答：不支持。")
	sys2 := e.buildSystemPrompt(context.Background(), nil, "zh")
	if !strings.Contains(sys2, "只允许回答：不支持。") || strings.Contains(sys2, promiseTestHead) {
		t.Fatalf("promise_rules 库值没做到整体替换：\n%s", sys2)
	}
	// ★ 独立性：把语气段改成一句话，事实闸必须原样还在（塞进 tone_rules 的实现会在这一行红灯）
	_ = e.db.SetConfig("tone_rules", "只说一句话：好的。")
	sys3 := e.buildSystemPrompt(context.Background(), nil, "zh")
	if !strings.Contains(sys3, "只说一句话：好的。") {
		t.Fatalf("tone_rules 库值未生效：\n%s", sys3)
	}
	if !strings.Contains(sys3, "只允许回答：不支持。") {
		t.Fatalf("改语气连带擦掉了承诺边界——两段必须分键存放：\n%s", sys3)
	}
}

// promiseTestHead 承诺段段首（测试里定位「这一段真的在」用整串，别只搜「【承诺边界】」：
// 语气段与现值段的正文都会提到这个名字，短串命中位置不唯一）。
const promiseTestHead = "【承诺边界】（这一段管「能说什么」"

// mainServiceStub 起一个「主服务」替身：只答复价口与语种口，并按被调次数记账。
// 参数 pricingJSON/langsJSON 传 "" 表示该口 500（模拟主服务挂或字段脏）。
func mainServiceStub(t *testing.T, pricingJSON, langsJSON string) (string, func() int) {
	t.Helper()
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		switch r.URL.Path {
		case "/api/pricing/meta":
			if pricingJSON == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(pricingJSON))
		case "/api/translation/langs":
			if langsJSON == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(langsJSON))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	count := func() int { mu.Lock(); defer mu.Unlock(); return hits }
	return srv.URL, count
}

// 测试用现值样本：35 种语言 + 两档系数 + 六位小数单价（单价刻意写成 0.09966800000000001 的形态，
// 用来验 trimNum 真把浮点尾巴削掉了——模型照着念的必须是能报出去的价格）
const stubPricingOK = `{"success":true,"modes":[{"code":"fast","points_per_1k_chars":3.2,"points_fixed":3},{"code":"pro","points_per_1k_chars":8.1,"points_fixed":7.5}],"points_price_money":0.099668,"unit":"points"}`

func stubLangsOK(n int) string {
	var sb strings.Builder
	sb.WriteString(`{"kb_langs":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"code":"l%d","name":"语%d","kb":"true"}`, i, i)
	}
	sb.WriteString("]}")
	return sb.String()
}

// TestSystemValuesInjectedFromMainService ★ 081x：价格与语种数改取实时值（用户指令
// 「使用系统配置口径」）。断言的是「客户屏幕上那个数就是从主服务取的」，
// 而不是「知识文案里抄的那个数」——所以样本值 35/3.2/0.099668 全都不是仓库里出现过的常量。
func TestSystemValuesInjectedFromMainService(t *testing.T) {
	base, hits := mainServiceStub(t, stubPricingOK, stubLangsOK(35))
	e := newTestEngine(t)
	_ = e.db.SetConfig("main_base_url", base)

	// 带一条知识素材：既测【相关知识】真在，也测现值段与它的相对次序
	sys := e.buildSystemPrompt(context.Background(), []entry{{key: "kb-epub", title: "格式", content: "支持 epub"}}, "zh")
	for _, want := range []string{
		systemValuesHead, "可选目标语言：35 种",
		"快速模式每 1000 源字符·单语种 3.2 积分，另每次建单固定 3 积分",
		"专业模式每 1000 源字符·单语种 8.1 积分，另每次建单固定 7.5 积分",
		"1 积分 ≈ 0.099668 元",
	} {
		if !strings.Contains(sys, want) {
			t.Fatalf("现值段缺 %q：\n%s", want, sys)
		}
	}
	if strings.Contains(sys, "0.09966800000000001") {
		t.Fatalf("浮点尾巴没削掉，会被当价格念出去：\n%s", sys)
	}
	// 现值段必须排在【相关知识】之前：知识文案里可能还有旧数，先给现值再给素材，
	// 并在段首写明「这里没写的数字就是没取到」。
	// ⚠️ 定位知识段要用「【相关知识】+换行」这个"真的当段首用"的形态：承诺边界正文里
	//    也提到了「【相关知识】里写明的事」，只搜那六个字会先命中承诺段（本断言首跑即此假红）。
	if strings.Index(sys, systemValuesHead) > strings.Index(sys, "【相关知识】\n") {
		t.Fatalf("现值段排在知识段之后：\n%s", sys)
	}
	// 拼装次序还得多一格：承诺边界在前、现值在后（现值是「可引用的数」，边界是「什么数都不许编」）
	if strings.Index(sys, promiseTestHead) > strings.Index(sys, systemValuesHead) {
		t.Fatalf("承诺边界没排在现值之前：\n%s", sys)
	}
	if !strings.Contains(sys, "支持 epub") {
		t.Fatalf("知识素材丢了：\n%s", sys)
	}
	// TTL 缓存：第二次建 prompt 不许再打主服务（否则每条对话两次 HTTP，挂件首响应被拖慢）
	before := hits()
	_ = e.buildSystemPrompt(context.Background(), nil, "zh")
	if got := hits(); got != before {
		t.Fatalf("第二次建 prompt 又打了主服务（%d→%d），60s TTL 缓存没生效", before, got)
	}
}

// TestSystemValuesFailSoftOmitsBlock ★ 081x 第 2 条硬口径：主服务取不到 ⇒ 整段不出现，
// 且**一个数字都不许留下**。兜旧值才是真事故——模型会把旧数当事实念给客户（官网定价页
// 那条「取不到系数就渲染空态，不许兜底旧价」在这里代价更高）。
// 三臂：两个口全挂 / 只挂价口（语种行还在、价格行整行没）/ 单价为 0（主服务侧汇率脏）。
func TestSystemValuesFailSoftOmitsBlock(t *testing.T) {
	// ① 全挂
	base, _ := mainServiceStub(t, "", "")
	e := newTestEngine(t)
	_ = e.db.SetConfig("main_base_url", base)
	sys := e.buildSystemPrompt(context.Background(), nil, "zh")
	if strings.Contains(sys, systemValuesHead) {
		t.Fatalf("主服务两个口都挂了还拼出现值段（承诺段里也提【系统现值】这个名字，判空必须按段首整串）：\n%s", sys)
	}
	// 软路径失败后界面照常（本行即「必须管理台露一次面」的理由，见 api/system_values.go）
	if !strings.Contains(sys, "【承诺边界】") {
		t.Fatalf("现值取不到时承诺边界也跟着没了（两段应互不依赖）：\n%s", sys)
	}

	// ② 只挂价口：语种行照常注入，价格相关的行整行不出现
	base2, _ := mainServiceStub(t, "", stubLangsOK(35))
	e2 := newTestEngine(t)
	_ = e2.db.SetConfig("main_base_url", base2)
	sys2 := e2.buildSystemPrompt(context.Background(), nil, "zh")
	if !strings.Contains(sys2, "可选目标语言：35 种") {
		t.Fatalf("语种现值没注入：\n%s", sys2)
	}
	for _, gone := range []string{"积分单价", "每 1000 源字符", "3.2"} {
		if strings.Contains(sys2, gone) {
			t.Fatalf("价口挂了却留下价格相关的 %q（半截系数比没系数更危险）：\n%s", gone, sys2)
		}
	}

	// ③ 单价为 0＝主服务侧汇率/尺子脏（moneyPerPoint 对脏配置刻意回 0），价格段整体不出现
	base3, _ := mainServiceStub(t, `{"success":true,"modes":[{"code":"fast","points_per_1k_chars":3.2,"points_fixed":3}],"points_price_money":0,"unit":"points"}`, stubLangsOK(35))
	e3 := newTestEngine(t)
	_ = e3.db.SetConfig("main_base_url", base3)
	if sys3 := e3.buildSystemPrompt(context.Background(), nil, "zh"); strings.Contains(sys3, "积分单价") {
		t.Fatalf("单价为 0 还报价：\n%s", sys3)
	}
}

// TestSystemValuesTTLRefreshAndUnknownModeLabel 两小段收尾：
// ① 缓存过期后必须重取（运营在主后台调了档，最迟 60s 反映到挂件，不许"重启才生效"）；
// ② 主服务哪天多出一档新模式（code 不是 fast/pro），标签不能编出一个不存在的模式名。
func TestSystemValuesTTLRefreshAndUnknownModeLabel(t *testing.T) {
	pricing := stubPricingOK
	base, hits := mainServiceStub(t, pricing, stubLangsOK(35))
	e := newTestEngine(t)
	_ = e.db.SetConfig("main_base_url", base)
	if !strings.Contains(e.buildSystemPrompt(context.Background(), nil, "zh"), "3.2") {
		t.Fatalf("首轮未注入现值")
	}
	if hits() != 2 {
		t.Fatalf("首轮应各打一次价口与语种口，实际 %d 次", hits())
	}

	// 换档：另起一个 stub 冒充"运营把系数调了档"（同一进程里改第一个 stub 的返回值不可靠）
	base2, _ := mainServiceStub(t, `{"success":true,"modes":[{"code":"fast","points_per_1k_chars":5.5,"points_fixed":3}],"points_price_money":0.2,"unit":"points"}`, stubLangsOK(40))
	_ = e.db.SetConfig("main_base_url", base2)
	if strings.Contains(e.buildSystemPrompt(context.Background(), nil, "zh"), "5.5") {
		t.Fatalf("TTL 内不该重取（缓存没生效）")
	}
	old := systemValuesTTL
	systemValuesTTL = 0 // 把缓存时长压到 0：下一次建 prompt 必须重取
	t.Cleanup(func() { systemValuesTTL = old })
	sys := e.buildSystemPrompt(context.Background(), nil, "zh")
	if !strings.Contains(sys, "5.5") || !strings.Contains(sys, "1 积分 ≈ 0.2 元") || !strings.Contains(sys, "40 种") {
		t.Fatalf("缓存过期后没按新地址重取现值：\n%s", sys)
	}
	// 缓存时间戳必须落上：管理台「现值」读数拿它算「客户正在用的是几秒前的值」
	if _, at := e.SystemValuesCached(); at.IsZero() || time.Since(at) > time.Minute {
		t.Fatalf("现值缓存时间戳没落上：%v", at)
	}

	if got := modeLabel("ultra"); !strings.Contains(got, "ultra") {
		t.Fatalf("未知模式代码被替换成了猜测的中文名：%s", got)
	}
}
