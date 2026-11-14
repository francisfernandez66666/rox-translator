// ============ product_onboard_draft_gate_test.go · 职责说明 ============
// POST /api/product/onboard/draft 的门禁（★ 2026-11-07），全部走**真实路由表＋假上游**。
//
// ★ 靶子各治一病，一条都不许省：
//
//	D1 鉴权/载荷：未登录 401、空描述 400、GET 405、超长 400——**只有卖家自己的问题**才回 4xx（§一·8）。
//	D2 正常抽取：四键齐出、语种被折进站点码表、**卖家自己界面那一语被剔掉**（这一条是需求本体）。
//	D3 占位符：模型写「未提供／暂无／N/A」时出栈必须是空串——占位串一旦被确认就变成对外页正文。
//	D4/D5 抖动：垃圾回执／被截断／空正文 ⇒ **200 ＋ 空草稿**，且报文里一个病因字样都不许出现
//	   （模型抖动绝不许写成卖家的错误；向导手打这条路必须一直开着）。
//	D6 没配阶段档 ⇒ 上游命中数**恒 0**（§21 的 ocr_fix 教训在 HTTP 面的复现：
//	   只测「出了空草稿」就分不清「没拨」与「白拨一次翻译模型再全超时」，后者客户等待与平台成本都真付了）。
//	D7 零写：products / product_facts / product_assets 三个计数前后等值（这一口不许落一行库）。
//	D8 零成本：台账**新增恰好一行** task_type='onboard_draft' 且 charge_kind='log'（留痕照写），
//	   而余额与 usage_daily **一分不动**（AGENTS §一·11：平台承担绝不进客户额度）。
//	   ⚠️ 上游必须真回 usage——零 token 时「没记账」与「记账为 0」同形，那条判据会空转；
//	   ⚠️ 另配一条**正向对照**（translate 必须照常累加日额），否则「日额不动」这句在日额整条腿坏掉时照样绿。
//	D9 防刷封顶：这一口的钱由平台出 ⇒ 没有格子就等于给"注册一批账号轮流打"留免费口子（§一·10）。
//	   判的是**行为**：上限内真拨、越界那一枪上游命中数不再涨、而报文形状一字不变
//	   （200＋空草稿，零"次数用完"字样）——越界写成 4xx 就是把防刷的成本转嫁给卖家的可用性。
//	D10 退格：拨了却没拨成（截断/垃圾回执）⇒ 格子退回，不许让一次上游抖动吃掉卖家当天的次数；
//	   而**没配阶段档那一档压根不占格**（零支出的账记进 rate_limits＝运营翻到一本假账，
//	   §一·11「读数要为事实背书」），这一条按 rate_limits 行数判，不靠注释。
//	D12 窗语义：这一档是**滚动 24h**（`window_start > now-86400`），不是 UTC 日切——
//	   文档与运营面都写"每日"，两者对"深夜用完、明早还能不能用"给不同答案，且没有任何现网症状。
//	   判据＝行为两态成对（窗内达顶必须不拨／同样计数只是窗过期必须恢复并重置为 1），
//	   单测任何一态都会给"窗永不恢复"或"每次都重置"那两种坏法放行（§三 成对判据那条）。
//	W1–W5 源码级腿：路由注册、内联错误响应为零、身份入口唯一、allowance/meta 两处接线、
//	语种清单跨侧等值（前端 LANG_OPTIONS 的每一项都必须落进 store.ProductLangs）。
//	W6 接线位置腿：占格子必须排在真拨之前、退格必须排在拿到 reason 之后（顺序反了＝并发击穿）。
//
// 方言口径（AGENTS §一·4）：自钉 sqlite 并恢复 config.C；内存库用具名 shared-cache DSN
// （裸 `:memory:` 是「每个连接一个库」，测试侧的读点会落到另一个空库上——那是夹具错不是产品缺陷）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run 'OnboardDraft'
// ================================================================
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"
)

// odbFixture 一套「Store＋引擎＋假上游（可换回执、可数命中）＋全量路由」的识别面环境。
type odbFixture struct {
	srv   *Server
	st    *store.Store
	tid   int64
	uid   int64
	tok   string
	hits  *int64
	reply *atomic.Value // 上游回执正文（恒带 usage，见文件头 D8 那条 ⚠️）
	up    *httptest.Server
}

// newOdbFixture 起假上游＋真路由（默认上游回一个空 JSON 对象，各用例再换回执）。
func newOdbFixture(t *testing.T) *odbFixture {
	t.Helper()
	pinSqliteDialect(t)

	var hits int64
	reply := &atomic.Value{}
	reply.Store(`{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}],"usage":{"prompt_tokens":1200,"completion_tokens":300}}`)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply.Load().(string)))
	}))
	t.Cleanup(up.Close)

	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = up.URL + "/v1" // 全局端点刻意也配成**可用**：这样「没拨」才只可能来自阶段档没配
	cfg.OnlineAPIKey = "sk-test"
	cfg.OnlineModel = "global-translation-model"
	config.C = cfg

	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) +
		"?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	// ★ KN11：主库四表 DDL 已摘除，测试库需独立重建（走 kc=nil 主库回滚分支）。
	migrateProductTablesForTest(t, st)
	u, err := st.CreateUser(1, "onboard_draft_user", auth.PasswordHash("pw123456"), "建档卖家", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签 token 失败: %v", err)
	}
	eng := engine.NewEngine(cfg, nil, nil, nil)
	eng.St = st
	srv := &Server{
		Cfg: cfg, Store: st, Engine: eng, Bill: billing.NewService(st),
		mux: newRouteMux(), metrics: newMetrics(),
		loginLimit: newLoginLimiter(st), regGuard: newRegisterGuard(st),
	}
	srv.routes()
	// ★ 这条接线就是 cmd/server/main.go 里那句 `eng.LLM.OnUsage = srv.ChargeUsageRealtime`：
	//   不挂它，本文件测的是「引擎拨了上游」而不是「计费现场→台账」，D8 那行留痕根本不会有人写。
	eng.LLM.OnUsage = func(ctx context.Context, model string, prompt, completion int64) error {
		return srv.ChargeUsageRealtime(ctx, model, prompt, completion)
	}
	return &odbFixture{srv: srv, st: st, tid: u.TenantID, uid: u.ID, tok: tok, hits: &hits, reply: reply, up: up}
}

// setOnboardStage 配/清 onboard_draft 阶段档（清＝写回一份只含 ai_initial 的 stage_models，
// 于是回落链真的存在、而本腿必须**不去够**它——这正是 §21 那条 ocr_fix 判据的形态）。
func (f *odbFixture) setOnboardStage(t *testing.T, configured bool) {
	t.Helper()
	raw := `{"ai_initial":{"api_base":"` + f.up.URL + `/v1","api_key":"sk-test","model":"m"}}`
	if configured {
		raw = `{"onboard_draft":{"api_base":"` + f.up.URL + `/v1","api_key":"sk-test","model":"judge-model"}}`
	}
	if err := f.st.SetConfig("stage_models", raw); err != nil {
		t.Fatalf("写 stage_models 失败: %v", err)
	}
}

// postDraft 打一次识别请求；返回状态码与解析后的响应体。
func (f *odbFixture) postDraft(t *testing.T, tok, body string) (int, map[string]interface{}) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/product/onboard/draft", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	// 没有鉴权中间件的那条腿：租户上下文必须夹具自己钉上，否则 ChargeUsageRealtime
	// 在 `tid<=0` 那一行直接放行，D8 就成了「什么都没记」的空转绿灯。
	r = r.WithContext(tenant.WithTenant(r.Context(), f.tid))
	w := httptest.NewRecorder()
	f.srv.mux.ServeHTTP(w, r)
	return w.Code, odbDecode(w.Body.Bytes())
}

// callDraftGet 打一次 GET（方法语义那一档）。
func (f *odbFixture) callDraftGet(t *testing.T) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/product/onboard/draft", nil)
	r.Header.Set("Authorization", "Bearer "+f.tok)
	w := httptest.NewRecorder()
	f.srv.mux.ServeHTTP(w, r)
	return w.Code
}

// odbDecode 解析响应体（解不出即判红：静默拿到空 map 会让下面所有判据空转）。
func odbDecode(raw []byte) map[string]interface{} {
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		// 用一个不可能通过的形状回吐原文，让调用方的等值判据红并把原文带上。
		return map[string]interface{}{"_unparsed": string(raw)}
	}
	return out
}

// upstreamContent 换上游那一枪 content 字段的正文（外层永远是合法 OpenAI 形状＋带 usage）。
func (f *odbFixture) upstreamContent(content string) {
	f.reply.Store(fmt.Sprintf(`{"choices":[{"finish_reason":"stop","message":{"content":%s}}],`+
		`"usage":{"prompt_tokens":1200,"completion_tokens":300}}`, jsonEncode(content)))
}

// upstreamTruncated 换一条「被 max_tokens 截断」的回执（finish_reason=length ＋ 半截 JSON）。
func (f *odbFixture) upstreamTruncated() {
	f.reply.Store(`{"choices":[{"finish_reason":"length","message":{"content":"{\"name\":\"A9\",\"target_langs\":[\"en\""}}],` +
		`"usage":{"prompt_tokens":1200,"completion_tokens":300}}`)
}

// jsonEncode 把一段正文编成 JSON 字符串字面量（假件里要拼带引号的 JSON，手写转义必错）。
func jsonEncode(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// odbDraft 取出参里的 draft 对象。
func odbDraft(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	d, ok := body["draft"].(map[string]interface{})
	if !ok {
		t.Fatalf("出参缺 draft 对象（或形状不是对象）：%v", body)
	}
	return d
}

// odbLangCSV 把 target_langs 读成逗号串（并要求它是**非 nil 的数组**，null 直接判红）。
func odbLangCSV(t *testing.T, v interface{}) string {
	t.Helper()
	arr, ok := v.([]interface{})
	if !ok {
		t.Fatalf("target_langs 必须是数组（null 会让每个消费方各判一次），实得 %#v", v)
	}
	parts := make([]string, 0, len(arr))
	for _, item := range arr {
		s, _ := item.(string)
		parts = append(parts, s)
	}
	return strings.Join(parts, ",")
}

// TestOnboardDraftAuthAndPayloadGates D1：四档「卖家自己的载荷有问题」。
func TestOnboardDraftAuthAndPayloadGates(t *testing.T) {
	f := newOdbFixture(t)
	f.setOnboardStage(t, true)

	if code := f.callDraftGet(t); code != http.StatusMethodNotAllowed {
		t.Fatalf("D1a：GET 必须 405，实得 %d", code)
	}
	if code, _ := f.postDraft(t, "", `{"text":"随便一句"}`); code != http.StatusUnauthorized {
		t.Fatalf("D1b：未登录必须 401，实得 %d", code)
	}
	for _, c := range []string{`{"text":""}`, `{"text":"   "}`, `{}`} {
		code, body := f.postDraft(t, f.tok, c)
		if code != http.StatusBadRequest {
			t.Fatalf("D1c：空描述必须 400，入参=%s 实得 %d body=%v", c, code, body)
		}
		if msg, _ := body["message"].(string); !strings.Contains(msg, "描述") {
			t.Fatalf("D1c：400 文案必须点名「要贴什么」，实得 %q", msg)
		}
	}
	long := "很长的描述"
	for len([]rune(long)) <= productOnboardDraftTextMaxChars {
		long += "，再补一段"
	}
	if code, _ := f.postDraft(t, f.tok, `{"text":`+jsonEncode(long)+`}`); code != http.StatusBadRequest {
		t.Fatalf("D1d：超上限必须 400（截掉后半段＝让卖家以为系统看漏了），实得 %d", code)
	}
	if n := atomic.LoadInt64(f.hits); n != 0 {
		t.Fatalf("D1：四档拒绝都不该拨上游（拒了还烧一次判定＝把成本记在一件没发生的事上），实得 %d 次", n)
	}
}

// TestOnboardDraftHappyPathFiltersLangs D2：四键齐出＋语种折进码表＋剔界面语＋去重。
func TestOnboardDraftHappyPathFiltersLangs(t *testing.T) {
	f := newOdbFixture(t)
	f.setOnboardStage(t, true)
	f.upstreamContent(`{"name":"整机 A9","tagline":"月产 1200 台的代工线","use_case":"东南亚客户的整线采购","target_langs":["en","it","zh","ja","ja","de","zh-hant","pt-BR"]}`)

	code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机，客户主要在东南亚。","ui_lang":"zh"}`)
	if code != http.StatusOK {
		t.Fatalf("D2：正常识别必须 200，实得 %d body=%v", code, body)
	}
	if ok, _ := body["success"].(bool); !ok {
		t.Fatalf("D2：success 必须为 true，实得 %v", body)
	}
	d := odbDraft(t, body)
	for _, k := range []string{"name", "tagline", "use_case", "target_langs"} {
		if _, present := d[k]; !present {
			t.Fatalf("D2：draft 必须四键齐出（缺一键＝前端那一格连「没内容」都读不出来），实得 %v", d)
		}
	}
	if d["name"] != "整机 A9" || d["tagline"] != "月产 1200 台的代工线" || d["use_case"] != "东南亚客户的整线采购" {
		t.Fatalf("D2：文本候选必须原样透传（不翻译、不改写、不截断），实得 %v", d)
	}
	if got := odbLangCSV(t, d["target_langs"]); got != "en,ja,de,zh_hant" {
		t.Fatalf("D2：语种必须＝站点码表 ∩ 模型输出、去重、并剔掉界面语 zh（脏码 it/pt-BR 不进），实得 %q", got)
	}
	if n := atomic.LoadInt64(f.hits); n != 1 {
		t.Fatalf("D2 正向对照失败：配了阶段档却拨了 %d 次上游（判据在空转）", n)
	}
}

// TestOnboardDraftNeverLeaksPlaceholders D3：模型偷懒写占位串 ⇒ 出栈一律空串。
func TestOnboardDraftNeverLeaksPlaceholders(t *testing.T) {
	f := newOdbFixture(t)
	f.setOnboardStage(t, true)
	f.upstreamContent(`{"name":"未提供","tagline":" 暂无 ","use_case":"N/A","target_langs":[]}`)
	_, body := f.postDraft(t, f.tok, `{"text":"一句话描述","ui_lang":"en"}`)
	d := odbDraft(t, body)
	for _, k := range []string{"name", "tagline", "use_case"} {
		if s, _ := d[k].(string); s != "" {
			t.Fatalf("D3：%s 出栈必须是空串而不是占位符，实得 %q（一旦被确认就变成对外页正文）", k, s)
		}
	}
	// 反向对照：正常值不许被同一把尺子抹掉（否则「剥占位」其实剥的是正文）。
	f.upstreamContent(`{"name":"无螺丝快拆结构","tagline":"无需工具即可更换刀头","use_case":"现场抢修与代工换线","target_langs":[]}`)
	_, body = f.postDraft(t, f.tok, `{"text":"一句话描述","ui_lang":"en"}`)
	d = odbDraft(t, body)
	if d["name"] != "无螺丝快拆结构" || d["tagline"] != "无需工具即可更换刀头" {
		t.Fatalf("D3 正向对照失败：含「无」字的正常正文被占位清洗误伤，实得 %v", d)
	}
}

// TestOnboardDraftFailOpenFaces D4/D5/D6：三种抖动都出「200 ＋ 空草稿」，且报文里零病因字样。
func TestOnboardDraftFailOpenFaces(t *testing.T) {
	f := newOdbFixture(t)
	f.setOnboardStage(t, true)
	cases := []struct {
		name string
		prep func()
	}{
		{"垃圾回执", func() { f.upstreamContent("这段不是 JSON，我只是想聊两句。") }},
		{"被截断", f.upstreamTruncated},
		{"空正文", func() { f.upstreamContent("") }},
	}
	for _, c := range cases {
		c.prep()
		before := atomic.LoadInt64(f.hits)
		code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机。","ui_lang":"zh"}`)
		if code != http.StatusOK {
			t.Fatalf("%s：模型抖动必须 200（向导手打那条路不许被 4xx 挡死），实得 %d body=%v", c.name, code, body)
		}
		if got := atomic.LoadInt64(f.hits); got != before+1 {
			t.Fatalf("%s：这一档配了阶段档，应该真拨一次上游（命中 %d→%d）", c.name, before, got)
		}
		d := odbDraft(t, body)
		for _, k := range []string{"name", "tagline", "use_case"} {
			if s, _ := d[k].(string); s != "" {
				t.Fatalf("%s：抖动时 %s 必须空串，实得 %q", c.name, k, s)
			}
		}
		if got := odbLangCSV(t, d["target_langs"]); got != "" {
			t.Fatalf("%s：抖动时语种必须是空数组（不是 null、不是猜一份），实得 %q", c.name, got)
		}
		// 反向：报文里不许出现任何 reason 档名或技术病因字样（那些只进日志）。
		raw := w_dump(body)
		for _, bad := range []string{"truncated", "unparsable", "empty_output", "no_stage_model", "上游", "模型", "超时"} {
			if strings.Contains(raw, bad) {
				t.Fatalf("%s：reason 档名/技术病因泄进报文：%q ⇒ 卖家看到的就是技术错误", c.name, bad)
			}
		}
	}

	// D6：没配阶段档 ⇒ 200 空草稿，且上游命中数一个都不许涨（§21 的 ocr_fix 教训）。
	f.setOnboardStage(t, false)
	base := atomic.LoadInt64(f.hits)
	code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机。","ui_lang":"zh"}`)
	if code != http.StatusOK {
		t.Fatalf("D6：没配阶段档也必须 200（空草稿），实得 %d", code)
	}
	if d := odbDraft(t, body); d["name"] != "" {
		t.Fatalf("D6：没配阶段档时草稿必须全空，实得 %v", d)
	}
	if got := atomic.LoadInt64(f.hits); got != base {
		t.Fatalf("D6：★ 回落链被接回来了——没配阶段档却白拨了 %d 次上游（每次都是白等满预算再超时）", got-base)
	}
}

// w_dump 把出参重新序列化一次，给「报文里不许出现 X」那类负向判据当一个整包靶子。
func w_dump(body map[string]interface{}) string {
	b, _ := json.Marshal(body)
	return string(b)
}

// TestOnboardDraftZeroWritesAndPlatformCost D7＋D8：零写库 ＋ 留痕行照写而余额与日额一分不动。
func TestOnboardDraftZeroWritesAndPlatformCost(t *testing.T) {
	f := newOdbFixture(t)
	f.setOnboardStage(t, true)
	// 先造一行真产品，让「计数不变」这件事有靶子（空表上 0==0 是空转）。
	if _, err := f.st.CreateProduct(f.tid, store.ProductInput{Name: "既有产品", UserID: f.uid}); err != nil {
		t.Fatalf("造靶子产品失败: %v", err)
	}
	f.upstreamContent(`{"name":"整机 A9","tagline":"月产 1200 台","use_case":"东南亚整线采购","target_langs":["en","ja"]}`)

	p0 := odbCount(t, f.st.DB(), "products")
	f0 := odbCount(t, f.st.DB(), "product_facts")
	a0 := odbCount(t, f.st.DB(), "product_assets")
	_, _, bal0, _ := f.srv.balancePayload(f.tid)
	day0, err := f.st.DailyUsage(f.tid)
	if err != nil {
		t.Fatalf("读日计数失败: %v", err)
	}
	log0 := odbUsageRows(t, f.st.DB(), f.tid, "onboard_draft")

	if code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机，客户在东南亚。","ui_lang":"zh"}`); code != http.StatusOK {
		t.Fatalf("D8：正常识别必须 200，实得 %d body=%v", code, body)
	}
	if n := atomic.LoadInt64(f.hits); n != 1 {
		t.Fatalf("D8 前提自证失败：上游只该被拨一次，实得 %d 次", n)
	}

	p1, f1, a1 := odbCount(t, f.st.DB(), "products"), odbCount(t, f.st.DB(), "product_facts"), odbCount(t, f.st.DB(), "product_assets")
	if p1 != p0 || f1 != f0 || a1 != a0 {
		t.Fatalf("D7：这一口只出候选、绝不写库（products %d→%d，facts %d→%d，assets %d→%d）", p0, p1, f0, f1, a0, a1)
	}
	_, _, bal1, _ := f.srv.balancePayload(f.tid)
	if bal1 != bal0 {
		t.Fatalf("D8：平台承担 ⇒ 客户余额一分不许动（%d→%d）", bal0, bal1)
	}
	day1, _ := f.st.DailyUsage(f.tid)
	if day1 != day0 {
		t.Fatalf("D8：★ 平台标签漏进了 store 的名单 ⇒ 客户的日额被平台自己的成本吃掉（%d→%d）", day0, day1)
	}
	if got := odbUsageRows(t, f.st.DB(), f.tid, "onboard_draft") - log0; got != 1 {
		t.Fatalf("D8：留痕行必须恰好新增一行（「实扣 0」与「压根没记账」要靠它分开），实得新增 %d", got)
	}
	if n := odbUsageLogKind(t, f.st.DB(), f.tid, "onboard_draft"); n != 1 {
		t.Fatalf("D8：那一行留痕的 charge_kind 必须是 log（客户面可见、金钱语义不参与），实得 %d 行", n)
	}
	// 正向对照：日额那条腿真有射程（客户自发消费必须照常累加），否则上面那句「日额不动」是空转。
	if err := f.st.LogUsage(f.tid, f.uid, "translate", "test", "m", "en", 5000, "text", "fast"); err != nil {
		t.Fatalf("正向对照落用量失败: %v", err)
	}
	if day2, _ := f.st.DailyUsage(f.tid); day2 <= day1 {
		t.Fatalf("正向对照失败：translate 必须累加 usage_daily（%d→%d）⇒ 上面那条「日额不动」没被证过", day1, day2)
	}
}

// odbCount 数一张表的全局行数（判「这一口有没有写库」只看总数，最不会漏）。
func odbCount(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow("SELECT COUNT(1) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("数 %s 行数失败: %v", table, err)
	}
	return n
}

// odbUsageRows 数某租户某用途标签的台账行数。
func odbUsageRows(t *testing.T, db *sql.DB, tid int64, taskType string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(`SELECT COUNT(1) FROM usage_ledger WHERE tenant_id=? AND task_type=?`, tid, taskType).Scan(&n); err != nil {
		t.Fatalf("数 usage_ledger 失败: %v", err)
	}
	return n
}

// odbUsageLogKind 数该标签里 charge_kind='log' 的行数（平台承担的落账形态）。
func odbUsageLogKind(t *testing.T, db *sql.DB, tid int64, taskType string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(`SELECT COUNT(1) FROM usage_ledger WHERE tenant_id=? AND task_type=? AND charge_kind='log'`, tid, taskType).Scan(&n); err != nil {
		t.Fatalf("数 usage_ledger 留痕行失败: %v", err)
	}
	return n
}

// odbLangListRe 从前端那份声明里取方括号内的字面量（形状判据，不是第二份清单）。
var odbLangListRe = regexp.MustCompile(`LANG_OPTIONS\s*=\s*\[([^\]]*)\]`)

// odbRateCount 读某一 (scope,key) 此刻窗口内的计数合计（判「这一枪到底占没占格子」只看这一个数）。
// 为什么数 SUM 而不是数行数：同一 (scope,key) 物理上只该有一行，但"退格"是 UPDATE 成 0 而不是删行——
// 按行数判会把「占过又退干净」读成「占了两格」。
func odbRateCount(t *testing.T, db *sql.DB, scope string, uid int64) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow("SELECT COALESCE(SUM(count),0) FROM rate_limits WHERE scope=? AND key=?",
		scope, fmt.Sprintf("%d", uid)).Scan(&n); err != nil {
		t.Fatalf("读 rate_limits 失败: %v", err)
	}
	return n
}

// odbRateRows 数某一 (scope,key) 在 rate_limits 里留下的**行数**（判「这一腿到底占没占过格子」）。
// ★ 与下面那个计数判据分工不同，不许合并：退格是把 count 改成 0 而不是删行，
// 所以「拨了但失败」那一档（D10）看 SUM(count)==0，而「压根没拨」那一档（D11）必须看**行数==0**
// ——按 SUM 判的话 D11 会被"占过又退干净"糊过去，那条锁就成了空转。
func odbRateRows(t *testing.T, db *sql.DB, scope string, uid int64) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow("SELECT COUNT(1) FROM rate_limits WHERE scope=? AND key=?",
		scope, fmt.Sprintf("%d", uid)).Scan(&n); err != nil {
		t.Fatalf("数 rate_limits 行失败: %v", err)
	}
	return n
}

// odbRateSeedWindow 把某一 (scope,key) 摆成"计数已达顶、但窗起点是给定那一刻"的形态（D12 专用）。
// 先删后插：这条腿要的是**窗语义**，不能受前面几枪留下的行影响；
// `lock_until` 显式写 0——库里那一列是 NOT NULL 且有它自己的语义，留 NULL 会让 RateReserve 的回读直接扫失败。
func odbRateSeedWindow(t *testing.T, db *sql.DB, scope string, uid, count, windowStart int64) {
	t.Helper()
	key := fmt.Sprintf("%d", uid)
	if _, err := db.Exec("DELETE FROM rate_limits WHERE scope=? AND key=?", scope, key); err != nil {
		t.Fatalf("清 rate_limits 夹具行失败: %v", err)
	}
	if _, err := db.Exec("INSERT INTO rate_limits(scope,key,count,window_start,lock_until) VALUES(?,?,?, ?,0)",
		scope, key, count, windowStart); err != nil {
		t.Fatalf("铺 rate_limits 夹具行失败: %v", err)
	}
}

// TestOnboardDraftDailyQuota D9：这一口的钱由平台出 ⇒ 必须有格子；越界那一枪只是"不拨"，报文形状一字不变。
//
// 为什么上限 2 而不是照抄代码里的 20：判据要问的是**行为**（第 N+1 枪不拨），不是那个数字本身；
// 数字由 env 现钉，将来改默认档不会把这条锁一起改红（同 §一·5「档位数字只走三级」那条）。
func TestOnboardDraftDailyQuota(t *testing.T) {
	t.Setenv("ONBOARD_DRAFT_DAILY", "2")
	f := newOdbFixture(t)
	f.setOnboardStage(t, true)
	f.upstreamContent(`{"name":"整机 A9","tagline":"月产 1200 台","use_case":"东南亚整线采购","target_langs":["en"]}`)

	for i := 1; i <= 3; i++ {
		before := atomic.LoadInt64(f.hits)
		code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机。","ui_lang":"zh"}`)
		if code != http.StatusOK {
			t.Fatalf("D9 第 %d 枪：越界那一枪也必须 200（把防刷写成 4xx＝拿卖家的可用性交押金，§一·10），实得 %d body=%v", i, code, body)
		}
		d := odbDraft(t, body)
		got := atomic.LoadInt64(f.hits) - before
		if i <= 2 {
			// 上限内的两枪：必须真拨，且真的抽出东西——这是"第 3 枪不拨"那条判据的正向对照。
			if got != 1 {
				t.Fatalf("D9 第 %d 枪：上限之内该拨一次上游，实得 +%d ⇒ 封顶腿把合法请求一起挡了", i, got)
			}
			if s, _ := d["name"].(string); s != "整机 A9" {
				t.Fatalf("D9 第 %d 枪：name 应抽出「整机 A9」，实得 %q ⇒ 后面那句「越界出空草稿」没有对照", i, s)
			}
			continue
		}
		if got != 0 {
			t.Fatalf("D9 第 3 枪：上限 2 已用满，这一枪必须一次都不拨（平台在为这一腿掏钱），实得 +%d", got)
		}
		if s, _ := d["name"].(string); s != "" {
			t.Fatalf("D9 第 3 枪：越界必须出空草稿，实得 name=%q", s)
		}
		if csv := odbLangCSV(t, d["target_langs"]); csv != "" {
			t.Fatalf("D9 第 3 枪：越界时语种必须是空数组，实得 %q", csv)
		}
		// 报文中不许出现档位字样：卖家看到的仍是"这张格子还空着，可以自己填"，不是一句技术失败。
		if raw := w_dump(body); strings.Contains(raw, "capped") || strings.Contains(raw, "上限") || strings.Contains(raw, "次数") {
			t.Fatalf("D9 第 3 枪：防刷档名/中文病因泄进报文：%s", raw)
		}
	}
	if n := odbRateCount(t, f.st.DB(), onboardDraftDailyScope, f.uid); n != 2 {
		t.Fatalf("D9：三枪之后格子计数必须恰好停在 2（越界那一枪不许再多占一格），实得 %d", n)
	}
}

// TestOnboardDraftQuotaReleasesOnFailure D10：拨了却没拨成 ⇒ 退格。
// 方向与 §一·10 同一条：失败原因在平台这一侧（截断/垃圾回执），卖家什么也没得到，
// 不退格就是"上游抖一次、卖家当天少一次"。
func TestOnboardDraftQuotaReleasesOnFailure(t *testing.T) {
	t.Setenv("ONBOARD_DRAFT_DAILY", "2")
	f := newOdbFixture(t)
	f.setOnboardStage(t, true)
	f.upstreamTruncated() // 每一枪都失败

	for i := 1; i <= 3; i++ {
		before := atomic.LoadInt64(f.hits)
		code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机。","ui_lang":"zh"}`)
		if code != http.StatusOK {
			t.Fatalf("D10 第 %d 枪：失败也必须 200 空草稿，实得 %d", i, code)
		}
		if got := atomic.LoadInt64(f.hits) - before; got != 1 {
			t.Fatalf("D10 第 %d 枪：失败的那一枪格子该退回来（否则上限 2 会被两次抖动吃光），实得 +%d body=%v", i, got, body)
		}
	}
	if n := odbRateCount(t, f.st.DB(), onboardDraftDailyScope, f.uid); n != 0 {
		t.Fatalf("D10：三枪全部失败后格子必须退回原处，实得计数 %d", n)
	}
}

// TestOnboardDraftQuotaIdleWhenLegUnconfigured D11：没配第 ⑨ 张卡时**连格子都不占**。
// 这一条防的不是刷，是**假账**：那一档一次上游都不拨、平台一分钱不花，
// 却在 rate_limits 里留下"某人今天用了 20 次"的读数（§一·11「读数要为事实背书」）。
func TestOnboardDraftQuotaIdleWhenLegUnconfigured(t *testing.T) {
	t.Setenv("ONBOARD_DRAFT_DAILY", "2")
	f := newOdbFixture(t)
	f.setOnboardStage(t, false)
	base := atomic.LoadInt64(f.hits)

	for i := 1; i <= 3; i++ {
		if code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机。","ui_lang":"zh"}`); code != http.StatusOK {
			t.Fatalf("D11 第 %d 枪：没配阶段档也必须 200 空草稿，实得 %d body=%v", i, code, body)
		}
	}
	if got := atomic.LoadInt64(f.hits); got != base {
		t.Fatalf("D11：没配阶段档却白拨了 %d 次上游（回落链被接回来了）", got-base)
	}
	if n := odbRateRows(t, f.st.DB(), onboardDraftDailyScope, f.uid); n != 0 {
		t.Fatalf("D11：这一腿压根没拨，却在 rate_limits 里留下了 %d 行 ⇒ 运营翻到的是一本零支出的假账（占格与退格各一次也算占过）", n)
	}
}

// TestOnboardDraftQuotaWindowIsRolling D12：这一档的窗是**滚动 24 小时**，不是 UTC 日切。
//
// 为什么要有这条（不是抠字眼）：运营面与两份文档都写「每日」，而 `rate_limits` 的窗语义是
// 「从这一档第一枪起滚 windowSec」（`store.RateReserve` 的条件是 `window_start > now-windowSec`）——
// 两者对"昨天深夜用完 20 次，今早还能不能用"给的是**不同答案**（日切＝到 00:00 恢复，滚动＝等满 24h）。
// 这个差别没有任何现网症状：界面只会少出一次候选，谁也不知道是窗没滚还是判据坏了。
// ⇒ 写成**行为两态成对**（AGENTS §三「只验一个方向等于什么都没验」）：
//
//	① 窗内且已达顶 ⇒ 一次都不拨；② 计数相同但窗已过期 ⇒ 必须真恢复，且整行重置成「本窗第一笔」（不是 3）。
//	只测② 会在"每次请求都把整行重置"这种坏法下照样绿；只测① 会在"窗永远不过期"下照样绿。
func TestOnboardDraftQuotaWindowIsRolling(t *testing.T) {
	t.Setenv("ONBOARD_DRAFT_DAILY", "2")
	f := newOdbFixture(t)
	f.setOnboardStage(t, true)
	f.upstreamContent(`{"name":"整机 A9","tagline":"月产 1200 台"}`)
	now := time.Now().Unix()

	// ① 窗**内**已达顶：格子还在有效期内，这一枪必须不拨
	odbRateSeedWindow(t, f.st.DB(), onboardDraftDailyScope, f.uid, 2, now-int64((24*time.Hour-time.Minute)/time.Second))
	before := atomic.LoadInt64(f.hits)
	if code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机。","ui_lang":"zh"}`); code != http.StatusOK {
		t.Fatalf("D12①：窗内触顶那一枪也必须 200 空草稿，实得 %d body=%v", code, body)
	}
	if got := atomic.LoadInt64(f.hits) - before; got != 0 {
		t.Fatalf("D12①：窗内且计数已达上限，这一枪必须一次都不拨（平台在为这一腿掏钱），实得 +%d", got)
	}

	// ② 同样的计数，只是窗**过期**：必须恢复成可用，且重置为「本窗第一笔」
	//    window_start 取"恰好比窗老 1 秒"，这样它测的是窗语义本身，而不是"行不存在"那一支
	odbRateSeedWindow(t, f.st.DB(), onboardDraftDailyScope, f.uid, 2, now-int64(24*time.Hour/time.Second)-1)
	before = atomic.LoadInt64(f.hits)
	if code, body := f.postDraft(t, f.tok, `{"text":"我们月产 1200 台整机。","ui_lang":"zh"}`); code != http.StatusOK {
		t.Fatalf("D12②：窗过期那一枪也必须 200，实得 %d body=%v", code, body)
	}
	if got := atomic.LoadInt64(f.hits) - before; got != 1 {
		t.Fatalf("D12②：窗过期后格子必须恢复（这一档是滚动 %d 秒，不是永久占死），实得 +%d", onboardDraftDailyWindow, got)
	}
	if n := odbRateCount(t, f.st.DB(), onboardDraftDailyScope, f.uid); n != 1 {
		t.Fatalf("D12②：开新窗必须整行重置为 1，实得 %d（≥2 ⇒ 把上一窗的计数带进了新窗，上限被悄悄收紧）", n)
	}
}

// TestOnboardDraftSourceWiring W1–W6：路由、错误风格、身份入口、两处接线、语种跨侧等值、防刷接线位置。
func TestOnboardDraftSourceWiring(t *testing.T) {
	src := mustReadOdbFile(t, "product_onboard_draft.go")
	if !strings.Contains(src, `s.mux.HandleFunc("/api/product/onboard/draft", s.handleProductOnboardDraft)`) {
		t.Fatal("W1：路由必须注册在本文件那一个注册函数里")
	}
	routes := mustReadOdbFile(t, "product_api.go")
	if !strings.Contains(routes, "s.registerProductOnboardDraftRoutes()") {
		t.Fatal("W1：routesProduct 没调用 registerProductOnboardDraftRoutes（漏一行＝界面上那颗按钮永远 404）")
	}
	// W2：错误出口只能是 writeError（TestErrorStyleRatchet 基线 0，这里先自查一次）。
	if strings.Contains(src, "writeJSON(w, 4") || strings.Contains(src, "writeJSON(w, 5") {
		t.Fatal("W2：新文件里出现了内联 writeJSON(4xx/5xx) ⇒ 错误风格棘轮当场红")
	}
	if !strings.Contains(src, "s.productActor(r)") {
		t.Fatal("W3：身份必须复用 productActor 那一个入口（不许再造第二套鉴权）")
	}
	// W4：预估与算价两处接线（allowance 单口；meta 明出 0 且不带 omitempty）。
	est := mustReadOdbFile(t, "estimate.go")
	if n := strings.Count(est, "engine.OnboardDraftAllowanceTokens()"); n != 1 {
		t.Fatalf("W4：预估侧必须且只能调一次第 7 支 allowance（单口形态），实得 %d 处", n)
	}
	meta := mustReadOdbFile(t, "pricing_meta.go")
	if !strings.Contains(meta, `"points_onboard_draft": 0`) {
		t.Fatal("W4：对外算价面必须明出 points_onboard_draft: 0（缺席会被读成字段没实现，AGENTS §28）")
	}
	if strings.Contains(meta, "points_onboard_draft,omitempty") {
		t.Fatal("W4：points_onboard_draft 不许带 omitempty——出 0 才是在承诺「不收费」")
	}
	// W4b：健康面这一档只出状态词，本文件与 server.go 都不许把端点/模型/Key 挂上去。
	h := mustReadOdbFile(t, "server.go")
	if !strings.Contains(h, `"onboard_draft": onboardDraftWord`) {
		t.Fatal("W4b：健康面缺 onboard_draft 状态词（没配阶段档这件事必须现读可见）")
	}
	// W5：语种清单跨侧等值——前端向导里的每一项都必须落进后端 store.ProductLangs。
	// ⚠️ 判的是**集合包含**而不是数量相等：前端那份是「对外目标语」（不含卖家自己的 zh），
	// 后端那份是站点全码表；把前者整份塞进后者才是缺陷（两把尺子各有射程）。
	fe := mustReadOdbFrontendFile(t)
	m := odbLangListRe.FindStringSubmatch(fe)
	if m == nil {
		t.Fatal("W5：前端 LANG_OPTIONS 声明没解析到 ⇒ 这条跨侧判据失明（锚点漂了，不是清单变了）")
	}
	checked := 0
	for _, item := range strings.Split(m[1], ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		item = strings.Trim(item, `'"`)
		code := normalizeProductLangCode(item)
		if !productLangSupported(code) {
			t.Fatalf("W5：前端向导里的 %q（归一后 %q）不在后端 store.ProductLangs 里 ⇒ 两侧各一份清单迟早打架", item, code)
		}
		checked++
	}
	if checked < 8 {
		t.Fatalf("W5 射程自证失败：只核到 %d 个语种码（前端那份声明应有 11 个）⇒ 解析器读空", checked)
	}
	// W5b：后端清单里必须同时有「卖家自己那一语」与「对外那 11 语」——否则过滤腿是空转。
	for _, must := range []string{"zh", "zh_hant", "en", "ja"} {
		if !productLangSupported(must) {
			t.Fatalf("W5b：store.ProductLangs 读不到靶子 %q ⇒ 上面那条包含判据在空转", must)
		}
	}

	// W6：防刷那三处接线的**位置**（顺序就是判据本身，D9/D10 只证行为、证不了先后）：
	//   占格 → 真拨 → 退格。占格排后＝并发击穿（两支同时读到"还差一格"）；
	//   退格排在拿到 reason 之前＝拿一个还不存在的病因做决定，或干脆谁都退（把成功那枪的格子也退了）。
	iReserve := strings.Index(src, "s.onboardDraftQuotaReserve(")
	iExtract := strings.Index(src, "s.Engine.ExtractOnboardDraft(")
	iRelease := strings.Index(src, "s.onboardDraftQuotaRelease(")
	if iReserve < 0 || iExtract < 0 || iRelease < 0 {
		t.Fatalf("W6：三处接线缺位（占格 %d／真拨 %d／退格 %d）⇒ 封顶腿不在 handler 里", iReserve, iExtract, iRelease)
	}
	if !(iReserve < iExtract && iExtract < iRelease) {
		t.Fatalf("W6：顺序必须是 占格→真拨→退格，现读 %d/%d/%d", iReserve, iExtract, iRelease)
	}
	if !strings.Contains(src, "s.onboardDraftLegWired(ctx)") {
		t.Fatal("W6：没配阶段档那一档必须先问健康词，否则 rate_limits 会留下一本零支出的假账（D11）")
	}
}

// mustReadOdbFile 读同包源文件（源码级腿的靶子）。
func mustReadOdbFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读不到 %s: %v", name, err)
	}
	return string(raw)
}

// mustReadOdbFrontendFile 读前端那份语种声明（跨侧等值腿的靶子）。
func mustReadOdbFrontendFile(t *testing.T) string {
	t.Helper()
	p := "../../../frontend-react/src/lib/onboardingSchema.ts"
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("W5：读不到 %s: %v", p, err)
	}
	return string(raw)
}
