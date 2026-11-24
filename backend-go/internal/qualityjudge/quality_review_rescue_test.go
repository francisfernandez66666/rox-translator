// ============ quality_review_rescue_test.go · 职责说明（2026-11-24）============
// 复核兜底腿（rescueByReview）的行为锁：主判据不达标时用 review 档复核一次，
// **只捞不杀**、失败保留原判、关思考必须真进请求体。
//
// 为什么这一族必须有行为锁而不是只看源码：三条最容易退化的形态都"不报错"——
//
//	① 变成"两票都通过才通过"（AND）：实测同一批样本上复核模型对 16 条正确译文里的 6 条给低分
//	   （房地产/流量统计/移动应用/合作伙伴关系/博士生/素质教育），做成 veto 就是白丢 6 条好数据；
//	② 复核腿失败时把 err 往上返：实时采集对无法判定是 fail-closed，
//	   于是"复核腿 429"会把原本可救的候选连同主判失败一起丢弃；本腿失败只保留原判不返错。
//	③ 忘了关思考：review 档是推理系，64 tokens 下思维链吃光预算、回执只有半个 JSON，
//	   表现是"复核永远维持不通过"，与"模型认为不合格"完全同形。
//
// 本文件不打外网：用 httptest 假上游按 model 名分派，store 用内存 SQLite（AGENTS §一·4 自钉方言）。
// ============================================================
package qualityjudge

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"translator/internal/config"
	"translator/internal/llm"
	"translator/internal/llmsource"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// pinSQLite 自钉 SQLite 方言（config.Default() 会写全局 config.C，不钉死会在 PG 模式泄漏）。
func pinSQLite(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// fakeJudgeUpstream 假上游：按请求体里的 model 名分派回执。
//
// 三档模型各一条腿：初翻（回译）／主判（initial_evals）／复核（review）。
// 复核腿的原始请求体单独留在 lastReview，用来判"关思考字段真进没进去"。
type fakeJudgeUpstream struct {
	mu         sync.Mutex
	hyBody     string // 回译/正向重译回执的 content
	mainBody   string // 主判回执的 content
	mainFinish string
	mainStatus int // 非 0 时主判腿回这个状态码
	backStatus int // 非 0 时回译腿回这个状态码
	revBody    string
	revFinish  string
	revStatus  int // 非 0 时复核腿回这个状态码（模拟 429/500）
	mainCalls  int
	revCalls   int
	lastReview map[string]interface{}
}

func (f *fakeJudgeUpstream) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string                 `json:"model"`
			Messages []map[string]string    `json:"messages"`
			Thinking map[string]interface{} `json:"thinking"`
			Extra    map[string]interface{} `json:"extra"`
			MaxTok   int                    `json:"max_tokens"`
			Rest     map[string]interface{} `json:"-"`
		}
		raw := map[string]interface{}{}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("假上游解码请求体失败: %v", err)
			w.WriteHeader(500)
			return
		}
		if v, ok := raw["model"].(string); ok {
			req.Model = v
		}

		write := func(content, finish string, status int) {
			if status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"boom"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{{
					"message":       map[string]string{"role": "assistant", "content": content},
					"finish_reason": finish,
				}},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
			})
		}

		f.mu.Lock()
		defer f.mu.Unlock()
		switch req.Model {
		case "HY-TEST": // 回译/正向重译腿
			body := f.hyBody
			if body == "" {
				body = "回译文本"
			}
			write(body, "stop", f.backStatus)
		case "GLM-TEST": // 主判腿
			f.mainCalls++
			write(f.mainBody, f.mainFinish, f.mainStatus)
		case "DS-TEST": // 复核腿
			f.revCalls++
			f.lastReview = raw
			write(f.revBody, f.revFinish, f.revStatus)
		default:
			write(`{"term":0,"semantic":0}`, "stop", 0)
		}
	}
}

var reviewTestDBSeq int64

// newJudgeForTest 组装一个打假上游的 Judge（store 用共享内存 SQLite，三档模型都指向假上游）。
func newJudgeForTest(t *testing.T, f *fakeJudgeUpstream) *Judge {
	t.Helper()
	pinSQLite(t)
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)

	name := "qualityjudge_review_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "_" + strconv.FormatInt(atomic.AddInt64(&reviewTestDBSeq, 1), 10)
	conn, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatalf("打开共享内存数据库失败: %v", err)
	}
	conn.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	stages := map[string]config.StageModel{
		"initial_evals": {APIBase: srv.URL, APIKey: "k", Model: "GLM-TEST"},
		"review":        {APIBase: srv.URL, APIKey: "k", Model: "DS-TEST"},
	}
	raw, _ := json.Marshal(stages)
	for k, v := range map[string]string{
		"stage_models":    string(raw),
		"online_api_base": srv.URL,
		"online_api_key":  "k",
		"online_model":    "HY-TEST",
	} {
		if err := st.SetConfig(k, v); err != nil {
			t.Fatalf("写配置 %s 失败: %v", k, err)
		}
	}
	return &Judge{client: llm.NewClient(config.C), st: st, cfg: config.C, backModel: "HY-TEST", minScore: DefaultMinScore}
}

// TestOnlineKeyResolutionUsesLLMSource 锁住全局配置唯一事实源：env 必须压过库内 online_*，
// stage 缺 Key 时也继承同一份 env 密钥，不能各读各的。
func TestOnlineKeyResolutionUsesLLMSource(t *testing.T) {
	j := newJudgeForTest(t, &fakeJudgeUpstream{})
	llmsource.ResetForTest()
	t.Cleanup(llmsource.ResetForTest)
	cfg := *config.C
	cfg.OnlineAPIBase = "https://env.example/v1"
	cfg.OnlineAPIKey = "env-key"
	cfg.OnlineModel = "ENV-MODEL"
	cfg.OnlineAPIKeyIsPlaceholder = false
	cfg.OnlineAPIKeyOrigin = config.OriginAPIKeyEnv
	j.cfg = &cfg

	base, key, model, err := j.resolveStageKey(context.Background(), "online")
	if err != nil || base != cfg.OnlineAPIBase || key != cfg.OnlineAPIKey || model != "HY-TEST" {
		t.Fatalf("回译腿绕过 llmsource：base=%q key=%q model=%q err=%v", base, key, model, err)
	}
	stages := config.StageModels{"initial_evals": {APIBase: "https://stage.example/v1", Model: "GLM-TEST"}}
	raw, _ := json.Marshal(stages)
	if err := j.st.SetConfig("stage_models", string(raw)); err != nil {
		t.Fatal(err)
	}
	_, key, _, err = j.resolveStageKey(context.Background(), "initial_evals")
	if err != nil || key != "env-key" {
		t.Fatalf("stage 缺 Key 未继承 env 快照：key=%q err=%v", key, err)
	}
}

// TestOnlineKeyResolutionRouteKeepsModelEndpoint 路由档必须取回译模型那条路由的完整三件套，
// 不能拿最高权重但模型不同的路由密钥/端点拼一个看似可用的假配置。
func TestOnlineKeyResolutionRouteKeepsModelEndpoint(t *testing.T) {
	j := newJudgeForTest(t, &fakeJudgeUpstream{})
	llmsource.ResetForTest()
	t.Cleanup(llmsource.ResetForTest)
	if err := j.st.SetConfig("online_api_key", ""); err != nil {
		t.Fatal(err)
	}
	routes := []config.ProviderConfig{
		{Provider: "other", APIBase: "https://other.example/v1", APIKey: "other-key", Model: "OTHER", Weight: 100},
		{Provider: "hy-low", APIBase: "https://hy-low.example/v1", APIKey: "hy-low-key", Model: "HY-TEST", Weight: 1},
		{Provider: "hy", APIBase: "https://hy.example/v1", APIKey: "hy-key", Model: "HY-TEST", Weight: 5},
	}
	raw, _ := json.Marshal(routes)
	if err := j.st.SetConfig("model_routes", string(raw)); err != nil {
		t.Fatal(err)
	}
	base, key, model, err := j.resolveStageKey(context.Background(), "online")
	if err != nil || base != "https://hy.example/v1" || key != "hy-key" || model != "HY-TEST" {
		t.Fatalf("路由回译三件套被拼错：base=%q key=%q model=%q err=%v", base, key, model, err)
	}
	j.backModel = "MISSING"
	if _, _, _, err := j.resolveStageKey(context.Background(), "online"); err == nil {
		t.Fatal("回译模型不在路由时必须显式失败，不能静默换模型")
	}
}

// R1 主判不达标 + 复核达标 ⇒ 改判通过（本腿的唯一目的）。
func TestReviewRescuesMainFailure(t *testing.T) {
	f := &fakeJudgeUpstream{
		mainBody: `{"term":40,"semantic":30}`, mainFinish: "stop",
		revBody: `{"term":85,"semantic":90}`, revFinish: "stop",
	}
	j := newJudgeForTest(t, f)
	r, err := j.Evaluate(context.Background(), "移动应用", "Aplikasi Mudah Alih", "ms")
	if err != nil {
		t.Fatalf("Evaluate 报错: %v", err)
	}
	if !r.Pass || !r.RescuedByReview {
		t.Fatalf("复核达标必须改判通过: pass=%v rescued=%v note=%q", r.Pass, r.RescuedByReview, r.ReviewNote)
	}
	if f.revCalls != 1 {
		t.Fatalf("复核腿应恰好拨一次，实拨 %d 次", f.revCalls)
	}
}

// R2 复核也不达标 ⇒ 维持不通过（不做 AND 之外的第三态）。
func TestReviewKeepsMainFailure(t *testing.T) {
	f := &fakeJudgeUpstream{
		mainBody: `{"term":100,"semantic":0}`, mainFinish: "stop",
		revBody: `{"term":0,"semantic":0}`, revFinish: "stop",
	}
	j := newJudgeForTest(t, f)
	r, err := j.Evaluate(context.Background(), "刹车系统", "מערכת ברק", "he")
	if err != nil {
		t.Fatalf("Evaluate 报错: %v", err)
	}
	if r.Pass || r.RescuedByReview {
		t.Fatalf("复核也不达标不许放行: pass=%v note=%q", r.Pass, r.ReviewNote)
	}
	if r.ReviewNote == "" {
		t.Fatal("维持不通过也必须留读数（否则与'没配复核档'同形）")
	}
}

// R3 主判已通过 ⇒ 复核腿一次都不许拨（只捞不杀的成本边界）。
func TestReviewNotCalledWhenMainPasses(t *testing.T) {
	f := &fakeJudgeUpstream{
		mainBody: `{"term":95,"semantic":90}`, mainFinish: "stop",
		revBody: `{"term":0,"semantic":0}`, revFinish: "stop",
	}
	j := newJudgeForTest(t, f)
	r, err := j.Evaluate(context.Background(), "汽车", "מכונית", "he")
	if err != nil {
		t.Fatalf("Evaluate 报错: %v", err)
	}
	if !r.Pass {
		t.Fatalf("主判达标应放行: %+v", r)
	}
	if f.revCalls != 0 {
		t.Fatalf("主判通过时不许拨复核腿（否则一次判定两个成本），实拨 %d 次", f.revCalls)
	}
}

// R4 复核腿失败（429）⇒ 保留原判不通过，不返错。
//
// ★ 反方向很危险：返 err 会让实时采集把可救候选按 fail-closed 丢弃。
func TestReviewFailureKeepsVerdictAndNoError(t *testing.T) {
	f := &fakeJudgeUpstream{
		mainBody: `{"term":20,"semantic":20}`, mainFinish: "stop",
		revStatus: 429,
	}
	j := newJudgeForTest(t, f)
	r, err := j.Evaluate(context.Background(), "在线支付", "onlineskötering", "sv")
	if err != nil {
		t.Fatalf("复核腿失败不许往上报错（调用方会 fail-closed 丢弃）: %v", err)
	}
	if r.Pass {
		t.Fatalf("复核腿没给出第二意见时必须维持不通过: %+v", r)
	}
	if r.ReviewNote == "" {
		t.Fatal("复核腿失败必须留原因，否则排障只能看到'维持不通过'")
	}
}

// R5 截断 / 非 JSON 两档：保留原判，且读数各自不同形。
func TestReviewTruncatedAndGarbageKeepVerdict(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		finish string
		want   string
	}{
		{"截断", `{"term":80,"sem`, "length", "截断"},
		{"无效JSON", `完全看不懂`, "stop", "无效"},
	}
	for _, tc := range cases {
		f := &fakeJudgeUpstream{
			mainBody: `{"term":10,"semantic":10}`, mainFinish: "stop",
			revBody: tc.body, revFinish: tc.finish,
		}
		j := newJudgeForTest(t, f)
		r, err := j.Evaluate(context.Background(), "数据备份", "Rezerwacja danych", "pl")
		if err != nil {
			t.Fatalf("%s: 不许返错: %v", tc.name, err)
		}
		if r.Pass {
			t.Fatalf("%s: 半个回执不许当成通过: %+v", tc.name, r)
		}
		if !contains(r.ReviewNote, tc.want) {
			t.Fatalf("%s: 读数应含 %q，实得 %q", tc.name, tc.want, r.ReviewNote)
		}
	}
}

// R6 关思考必须真进请求体（review 档是推理系，64 tokens 下不关就只剩半个 JSON）。
//
// ★ 判的是**线上真发出去的那一份 body**，不是源码里有没有那句调用：
// 这一族最典型的退化是"挂了 ctx 但 CallChat 用的是没挂的那个 ctx"（死码，锁看不出来）。
func TestReviewSendsNoThinkingFlag(t *testing.T) {
	f := &fakeJudgeUpstream{
		mainBody: `{"term":30,"semantic":30}`, mainFinish: "stop",
		revBody: `{"term":90,"semantic":90}`, revFinish: "stop",
	}
	j := newJudgeForTest(t, f)
	if _, err := j.Evaluate(context.Background(), "云计算", "cloudteknik", "sv"); err != nil {
		t.Fatalf("Evaluate 报错: %v", err)
	}
	if f.lastReview == nil {
		t.Fatal("复核腿没被调用，无法判关思考字段")
	}
	th, ok := f.lastReview["thinking"].(map[string]interface{})
	if !ok || th["type"] != "disabled" {
		t.Fatalf("复核腿请求体缺 thinking:{type:disabled}（推理系会把 64 tokens 吃光）: %v", f.lastReview["thinking"])
	}
	if mt, ok := f.lastReview["max_tokens"].(float64); !ok || int(mt) != reviewMaxTokens {
		t.Fatalf("复核腿 max_tokens 应为 %d，实得 %v", reviewMaxTokens, f.lastReview["max_tokens"])
	}
}

// R7 主判截断、缺字段、越界都不许当 0 分或直接返错；统一转复核腿裁决。
func TestReviewRescuesInvalidMainOutput(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		finish string
		want   string
	}{
		{"截断", `{"term":80,"semantic":90}`, "length", "截断"},
		{"空对象", `{}`, "stop", "无效"},
		{"越界", `{"term":101,"semantic":90}`, "stop", "无效"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeJudgeUpstream{
				mainBody: tc.body, mainFinish: tc.finish,
				revBody: `{"term":85,"semantic":90}`, revFinish: "stop",
			}
			j := newJudgeForTest(t, f)
			r, err := j.Evaluate(context.Background(), "卖家", "מוכר", "he")
			if err != nil {
				t.Fatalf("坏主判必须转复核，不许返错触发调用方 fail-closed: %v", err)
			}
			if !r.Pass || !r.RescuedByReview {
				t.Fatalf("复核达标应救回坏主判: %+v", r)
			}
			if !contains(r.Reason, tc.want) {
				t.Fatalf("主判坏形态读数应含 %q，实得 %q", tc.want, r.Reason)
			}
		})
	}
}

// R8 复核缺字段、越界、类型错同样不是有效第二意见，必须保留主判不通过。
func TestReviewInvalidScoresKeepVerdict(t *testing.T) {
	for _, body := range []string{`{}`, `{"term":90}`, `{"term":101,"semantic":90}`, `{"term":"90","semantic":90}`} {
		f := &fakeJudgeUpstream{
			mainBody: `{"term":10,"semantic":10}`, mainFinish: "stop",
			revBody: body, revFinish: "stop",
		}
		j := newJudgeForTest(t, f)
		r, err := j.Evaluate(context.Background(), "数据备份", "Rezerwacja danych", "pl")
		if err != nil {
			t.Fatalf("复核坏回执不许返错: %v", err)
		}
		if r.Pass || r.RescuedByReview || !contains(r.ReviewNote, "无效") {
			t.Fatalf("复核坏回执必须保留主判不通过: body=%s result=%+v", body, r)
		}
	}
}

// R9 主判上游报错也先走复核；复核救回后不把错误抛给采集端 fail-closed。
func TestReviewRescuesMainHTTPError(t *testing.T) {
	f := &fakeJudgeUpstream{
		mainStatus: 500,
		revBody:    `{"term":88,"semantic":92}`, revFinish: "stop",
	}
	j := newJudgeForTest(t, f)
	r, err := j.Evaluate(context.Background(), "用户互动", "תגובות מצד המשתמשים", "he")
	if err != nil || !r.Pass || !r.RescuedByReview {
		t.Fatalf("主判报错应被复核救回: result=%+v err=%v", r, err)
	}
	if r.Term != 88 || r.Semantic != 92 {
		t.Fatalf("救回后结果分应取复核分，实得 %v/%v", r.Term, r.Semantic)
	}
}

// R10 主判与复核都不可用时保留原错误，清洗端才会跳过而不是误删。
func TestRepairRejectsNonChineseSource(t *testing.T) {
	j := newJudgeForTest(t, &fakeJudgeUpstream{})
	fixed, ok, err := j.Repair(context.Background(), "manual", "he")
	if err == nil || ok || fixed != "" || !strings.Contains(err.Error(), "仅中文源") {
		t.Fatalf("非中文源不得进入中文重译：fixed=%q ok=%v err=%v", fixed, ok, err)
	}
}

func TestRepairRejectsStructuralDefect(t *testing.T) {
	f := &fakeJudgeUpstream{hyBody: "עברית"}
	j := newJudgeForTest(t, f)
	fixed, ok, err := j.Repair(context.Background(), "数据备份", "fr")
	if err == nil || ok || fixed != "" || !strings.Contains(err.Error(), "结构化闸") {
		t.Fatalf("修复产物书写系统不符必须拒绝：fixed=%q ok=%v err=%v", fixed, ok, err)
	}
}

func TestReviewFailureKeepsMainHTTPError(t *testing.T) {
	f := &fakeJudgeUpstream{mainStatus: 500, revStatus: 429}
	j := newJudgeForTest(t, f)
	r, err := j.Evaluate(context.Background(), "数据备份", "Rezerwacja danych", "pl")
	if err == nil || r.Pass || r.RescuedByReview || r.Conclusive {
		t.Fatalf("两腿都报错应返回主判错误且无有效裁决: result=%+v err=%v", r, err)
	}
}

// R10 坏主判 + 复核不可用 ⇒ 无法判定，清洗端必须跳过而不是按有效低分删除。
func TestInvalidMainWithoutReviewIsNotConclusive(t *testing.T) {
	f := &fakeJudgeUpstream{mainBody: `{}`, mainFinish: "stop", revStatus: 429}
	j := newJudgeForTest(t, f)
	r, err := j.Evaluate(context.Background(), "数据备份", "Rezerwacja danych", "pl")
	if err == nil || r.Pass || r.Conclusive {
		t.Fatalf("坏主判且复核不可用必须返回无法判定: result=%+v err=%v", r, err)
	}
}

// R11 主判上游报错但复核给出有效低分 ⇒ 这是有效否决，可按判错重试/删除。
func TestReviewLowScoreAfterMainHTTPErrorIsConclusive(t *testing.T) {
	f := &fakeJudgeUpstream{mainStatus: 500, revBody: `{"term":0,"semantic":0}`, revFinish: "stop"}
	j := newJudgeForTest(t, f)
	r, err := j.Evaluate(context.Background(), "数据备份", "Rezerwacja danych", "pl")
	if err != nil || r.Pass || !r.Conclusive {
		t.Fatalf("复核有效低分应构成有效否决: result=%+v err=%v", r, err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
