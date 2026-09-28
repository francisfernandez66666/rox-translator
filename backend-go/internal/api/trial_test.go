// ============ trial_test.go · 职责说明 ============
// 〇-Z「免登录即时翻译试用」后端侧单测（internal/api/trial.go）。
//
// 射程与取舍：本文件钉的是**闸门、账目与成本归属**，不是译文质量——
//
//	A 校验面：方法、设备号格式、原文空/超长、语种白名单，逐条判状态码与稳定码，
//	  并断言「校验失败不吃配额」（访客没拿到译文却少一句是最容易被投诉的账）；
//	B ★ 成功面：真出译文才计数——设备号连续 5 句都 200、left 逐句递减、第 6 句 429
//	  TRIAL_EXHAUSTED＋reason=device，且被拒的那次不得把 trial_dev 顶到 6；
//	C ★ 窗口语义：rate_limits 的读侧不清过期窗口（只有写侧 RateRecord 才重置），
//	  「隔夜是否恢复额度」完全取决于 trialWindowActive 有没有判对窗口起点——
//	  写错的症状是**第一天用完的人永久用完**，且只在生产跑满 24 小时后才显形，
//	  故用「把 window_start 手改成 25 小时前」直接钉；
//	D 另两档：IP 到顶 reason=ip（换浏览器也拦）、平台日预算到顶 reason=global；
//	  触顶告警只留一条（CreateAlert 的 open 去重），刷不出第二遍运维屏；
//	E 优先序：额度三档都走 env > system_config > 代码默认（AGENTS §一·3），非法值逐级回落；
//	F ★ 成本归属：留痕行落 usage_ledger 时 tenant_id=0 且 charge_kind='log'，
//	  客户租户（1）名下**一行都不该多**——这是「试用是平台自己掏的市场费用」的机器证明；
//	G 门面：路由匿名可达（无需凭证即打到闸门），依赖未就绪诚实 503 且不碰配额。
//
// 上游怎么假：起一个 httptest 服务冒充 OpenAI 兼容端点（与 engine 包
// file_writepoints_guard_test.go 的 fwpGateEngine 同一手法），
// 于是成功路径在单测里就真能走通；UAT 矩阵（scripts/uat/api_uat.sh 试用段）再拿
// 整套 mock LLM 复跑一遍端到端，两层判据互补而不是互相替代。
// ⚠️ 引擎用 engine.NewEngine 构造，不用结构体字面量：熔断器/CJK 缓存等私有字段
//
//	缺省会 nil 解引用，而引擎的 recover 兜底会把它伪装成「该语种没有译文」（500），
//	归因成本远高于一次正确的构造。
//
// ⚠️ 假上游的**绝对**调用次数不进断言：pro 模式一句要打「初翻＋AI 校对」两次以上，
//
//	次数属引擎内部编排，会随提示词改造漂移；本文件只锁「被拒的请求增量必须为 0」这条口径。
//
// ★ 单测自钉 sqlite 方言（AGENTS.md §一·4）：config.Default() 副作用写全局 config.C，
//
//	run_uat 的 PG 模式会把方言泄漏给同包内存 SQLite 用例。
//	运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestTrial
//
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/store"
)

// trialDev 固定设备号（格式合规：8~64 位 [A-Za-z0-9_-]），多数用例拿它当"同一台浏览器"。
const trialDev = "uatdevice0000001"

// trialIP 固定来源 IP（用例靠它预置 IP 档计数；clientIP 取 RemoteAddr 的 host 段）。
const trialIP = "203.0.113.9"

// trialHarness 一次试用面测试环境：被测 Server + 底层 DB + 假上游命中次数。
type trialHarness struct {
	s     *Server
	db    *sql.DB
	calls *int64 // 假上游被调用次数（证明请求确实走到了引擎）
}

// trialFixture 建一套「Store 与引擎都就绪、上游是本地假端点」的试用面环境。
// 引擎刻意不接知识库（DB/Index 留 nil）：试用请求的 ctx 租户为 0，
// translateOneInner 对 tid<=0 本来就跳过全部 KB 匹配走纯模型翻译，
// 与真实试用的路径一致，也顺带避免单测依赖知识库数据。
func trialFixture(t *testing.T) *trialHarness {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		// 译文不含数字：源文也不含数字，约束闸门（数字保持等）不会误判，
		// pro 模式的校对与重翻都回同一句话，测试关注的是额度与账目而非文字。
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"The product proposal document"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`)
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言：防 UAT 矩阵的 PG env 泄漏进本包（AGENTS.md §一·4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	db, err := sql.Open("sqlite", "file:oztrial_"+strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	// ⚠️ 不要 SetMaxOpenConns(1)：store.New 的建表/幂等补列链路会在一个未提交事务里
	// 再向同一个 *sql.DB 取第二条连接（PackagesTenantMigrate 等），连接池上限锁成 1 时
	// 第二条永远等不到 ⇒ 整个测试在 fixtures 阶段死锁挂到 -timeout（2026-09-28 首跑实测 300s 挂死）。
	// 内存库用 `file:...?mode=memory&cache=shared` 且按用例名取唯一库名，既让多条连接看到
	// 同一份数据，又不会和同包其它内存库用例串数据（仓内既有写法，见 auth_deptadmin_org_gate_test.go）。
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	s := &Server{
		Store:   st,
		Cfg:     cfg,
		metrics: newMetrics(),
		Engine: func() *engine.Engine {
			// 用 NewEngine 而不是结构体字面量：breaker/cjkCacheByTenant/errRing 等
			// 私有字段由它一并初始化，字面量构造会在翻译链路里 nil 解引用
			// （recoverPipeline 把它兜成「该语种无译文」，测试表现为 500 而不是 panic，更难归因）。
			eng := engine.NewEngine(cfg, nil, nil, nil)
			eng.St = st // 平台存储：阶段模型/术语/配置读取（本用例只走纯模型链路）
			return eng
		}(),
	}
	return &trialHarness{s: s, db: db, calls: &hits}
}

// trialPost 发一次试用请求并返回响应。
// 参数 h: 测试环境；body: 请求体（一般由 trialBody 组装，只写差异项）。
func trialPost(t *testing.T, h *trialHarness, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("构造请求体失败: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/trial/translate", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = trialIP + ":5555"
	w := httptest.NewRecorder()
	h.s.handleTrialTranslate(w, r)
	return w
}

// trialIntervalPass 抹掉同设备最小间隔的记账行，等价于「距上次点击已超过 3 秒」。
// 为什么不清 trial_dev：只放行间隔闸，额度闸仍然照计——连续 5 句里任何一句被间隔闸
// 拦下都会让额度等式变成假数据（这是本文件最容易自伤的一处）。
func trialIntervalPass(t *testing.T, h *trialHarness, device string) {
	t.Helper()
	if _, err := h.db.Exec("DELETE FROM rate_limits WHERE scope='guard_int' AND key=?", "trial:"+device); err != nil {
		t.Fatalf("清理最小间隔记账失败: %v", err)
	}
}

// trialBody 组装一份"除被改字段外全部合法"的请求体，用例只写差异项，读得清。
func trialBody(over map[string]interface{}) map[string]interface{} {
	m := map[string]interface{}{
		"text":        "产品方案书",
		"target_lang": "en",
		"device_id":   trialDev,
	}
	for k, v := range over {
		m[k] = v
	}
	return m
}

// trialErr 解析统一错误体（code＋details.reason＋retry_after）。
type trialErr struct {
	Success    bool `json:"success"`
	Code       string
	Message    string
	RetryAfter int `json:"retry_after"`
	Details    struct {
		Reason string `json:"reason"`
	} `json:"details"`
}

// trialOK 解析成功出参。
type trialOK struct {
	Success     bool   `json:"success"`
	Translation string `json:"translation"`
	SourceLang  string `json:"source_lang"`
	TargetLang  string `json:"target_lang"`
	Mode        string `json:"mode"`
	Left        int    `json:"left"`
	Exhausted   bool   `json:"exhausted"`
}

func trialParseErr(t *testing.T, w *httptest.ResponseRecorder) trialErr {
	t.Helper()
	var e trialErr
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("错误响应体不是合法 JSON: %v / 原文 %q", err, w.Body.String())
	}
	return e
}

func trialParseOK(t *testing.T, w *httptest.ResponseRecorder) trialOK {
	t.Helper()
	var o trialOK
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
		t.Fatalf("成功响应体不是合法 JSON: %v / 原文 %q", err, w.Body.String())
	}
	return o
}

// TestTrialValidationMatrix 校验面逐条：状态码与稳定码都要诚实（AGENTS §一·8 的口径）。
// 每条都断言「没吃掉配额」——校验失败白扣句数是 C 段的同一类账，顺手一起钉。
func TestTrialValidationMatrix(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		body     map[string]interface{}
		badJSON  bool // 单独验「请求体解析不了」这条腿
		wantCode int
		wantEC   string
	}{
		{"非POST", http.MethodGet, nil, false, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
		{"请求体不是JSON", http.MethodPost, nil, true, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"设备号太短", http.MethodPost, trialBody(map[string]interface{}{"device_id": "abc"}), false, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"设备号含非法字符", http.MethodPost, trialBody(map[string]interface{}{"device_id": "bad device!'"}), false, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"设备号超长", http.MethodPost, trialBody(map[string]interface{}{"device_id": strings.Repeat("a", 65)}), false, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"原文为空", http.MethodPost, trialBody(map[string]interface{}{"text": "   "}), false, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"原文超长", http.MethodPost, trialBody(map[string]interface{}{"text": strings.Repeat("汉", trialMaxTextRunes+1)}), false, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"语种不在白名单", http.MethodPost, trialBody(map[string]interface{}{"target_lang": "klingon"}), false, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"语种为空", http.MethodPost, trialBody(map[string]interface{}{"target_lang": ""}), false, http.StatusBadRequest, "VALIDATION_ERROR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := trialFixture(t)
			var r *http.Request
			switch {
			case c.method == http.MethodGet:
				r = httptest.NewRequest(http.MethodGet, "/api/trial/translate", nil)
			case c.badJSON:
				r = httptest.NewRequest(http.MethodPost, "/api/trial/translate", strings.NewReader(`{"text":`))
				r.Header.Set("Content-Type", "application/json")
			default:
				raw, _ := json.Marshal(c.body)
				r = httptest.NewRequest(http.MethodPost, "/api/trial/translate", strings.NewReader(string(raw)))
				r.Header.Set("Content-Type", "application/json")
			}
			r.RemoteAddr = trialIP + ":5555"
			w := httptest.NewRecorder()
			h.s.handleTrialTranslate(w, r)
			if w.Code != c.wantCode {
				t.Fatalf("状态码期望 %d，实得 %d，响应 %q", c.wantCode, w.Code, w.Body.String())
			}
			if w.Code != http.StatusMethodNotAllowed {
				e := trialParseErr(t, w)
				if e.Code != c.wantEC {
					t.Fatalf("稳定码期望 %s，实得 %s（前端按码分支，不能只靠文案）", c.wantEC, e.Code)
				}
				if e.Success {
					t.Fatalf("失败响应必须 success=false，实得 %q", w.Body.String())
				}
			}
			// 配额与上游都没被碰：账上无行、假上游一次没被打
			if st := trialLoadRate(h.db, "trial_dev", trialDev); st.Count != 0 {
				t.Fatalf("校验失败不该计配额，trial_dev 已计 %d 次", st.Count)
			}
			if n := atomic.LoadInt64(h.calls); n != 0 {
				t.Fatalf("校验失败不该走到引擎（假上游被打 %d 次），白烧模型调用费", n)
			}
		})
	}
}

// TestTrialDeviceQuotaFiveSentences ★ B 段：5 句都放行、剩余逐句递减、第 6 句被拒。
// 额度数字用等值锁（5），不是"至少一次"的单向锁——改成 4 或 6 都是产品口径漂移，应当红灯。
func TestTrialDeviceQuotaFiveSentences(t *testing.T) {
	h := trialFixture(t)
	if got := h.s.trialLimit("TRIAL_DEVICE_QUOTA", "trial_device_quota", trialDeviceQuotaDefault); got != 5 {
		t.Fatalf("设备额度默认档漂移：期望 5 句，实得 %d", got)
	}
	for i := 1; i <= 5; i++ {
		trialIntervalPass(t, h, trialDev)
		w := trialPost(t, h, trialBody(nil))
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 句应放行 200，实得 %d / %q", i, w.Code, w.Body.String())
		}
		o := trialParseOK(t, w)
		if !o.Success || strings.TrimSpace(o.Translation) == "" {
			t.Fatalf("第 %d 句必须真出译文，实得 %q", i, w.Body.String())
		}
		if o.Mode != "pro" {
			t.Fatalf("试用必须回显 pro（校对档）模式，实得 %q——第一句就给糙活，额度再宽也是白送", o.Mode)
		}
		if o.TargetLang != "en" {
			t.Fatalf("目标语种回显错误：%q", o.TargetLang)
		}
		if want := 5 - i; o.Left != want {
			t.Fatalf("第 %d 句后剩余句数期望 %d，实得 %d（前端据此显示「剩 N 句」）", i, want, o.Left)
		}
		if o.Exhausted != (o.Left <= 0) {
			t.Fatalf("exhausted 与 left 必须同事实：left=%d exhausted=%v", o.Left, o.Exhausted)
		}
	}
	if st := trialLoadRate(h.db, "trial_dev", trialDev); st.Count != 5 {
		t.Fatalf("5 句成功后 trial_dev 期望计 5，实得 %d", st.Count)
	}

	// 第 6 句：额度见底 → 429 TRIAL_EXHAUSTED＋reason=device（前端据此出注册引导）
	trialIntervalPass(t, h, trialDev)
	w := trialPost(t, h, trialBody(nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("第 6 句应被拒 429，实得 %d / %q", w.Code, w.Body.String())
	}
	e := trialParseErr(t, w)
	if e.Code != "TRIAL_EXHAUSTED" {
		t.Fatalf("稳定码应为 TRIAL_EXHAUSTED（前端据此出注册引导），实得 %s", e.Code)
	}
	if e.Details.Reason != "device" {
		t.Fatalf("reason 应为 device，实得 %q", e.Details.Reason)
	}
	if st := trialLoadRate(h.db, "trial_dev", trialDev); st.Count != 5 {
		t.Fatalf("被拒的请求不得推进配额：期望仍为 5，实得 %d", st.Count)
	}
	// 出「已用完」时模型调用次数**一次都不该涨**——拒绝必须在进引擎之前。
	// 这里刻意不锁绝对次数：pro 模式一句会打「初翻＋AI 校对」两次以上上游，
	// 绝对值属于引擎内部编排，会随提示词改造漂移；真正的口径是「增量必须为 0」。
	before := atomic.LoadInt64(h.calls)
	trialIntervalPass(t, h, trialDev)
	if w := trialPost(t, h, trialBody(nil)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("第 6 句（额度已见底）应再次 429，实得 %d / %q", w.Code, w.Body.String())
	}
	if delta := atomic.LoadInt64(h.calls) - before; delta != 0 {
		t.Fatalf("额度见底的请求不该再花模型调用费，上游多打了 %d 次", delta)
	}
	if st := trialLoadRate(h.db, "trial_dev", trialDev); st.Count != 5 {
		t.Fatalf("重复被拒仍不得推进配额：期望仍为 5，实得 %d", st.Count)
	}
}

// TestTrialCostBelongsToPlatform ★ F 段（本文件最关键的一条账）：
// 试用的成本留在租户 0 的留痕行上，客户租户名下不得多出任何用量行。
// 改坏了会怎样：若进引擎前忘了把 ctx 租户清成 0，匿名请求会被 withTenant 兜底到默认租户 1，
// 每一句试用都从 rox 的余额里扣积分——把市场费用记到客户账上，客户来问账时无法解释。
func TestTrialCostBelongsToPlatform(t *testing.T) {
	h := trialFixture(t)
	trialIntervalPass(t, h, trialDev)
	w := trialPost(t, h, trialBody(nil))
	if w.Code != http.StatusOK {
		t.Fatalf("试用应放行，实得 %d / %q", w.Code, w.Body.String())
	}
	var tid int64
	var kind string
	err := h.db.QueryRow("SELECT tenant_id, charge_kind FROM usage_ledger ORDER BY id DESC LIMIT 1").Scan(&tid, &kind)
	if err != nil {
		t.Fatalf("试用成功后应有成本留痕行: %v", err)
	}
	if tid != 0 {
		t.Fatalf("留痕行必须记在租户 0（平台自付），实得 tenant_id=%d", tid)
	}
	if kind != "log" {
		t.Fatalf("留痕行必须是 charge_kind='log'（不参与退款/消耗核算），实得 %q", kind)
	}
	// 客户账上无痕：默认租户 1 名下不该因为试用多出任何行
	var n int
	if err := h.db.QueryRow("SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=1").Scan(&n); err != nil {
		t.Fatalf("统计租户 1 用量失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("试用不得动客户租户余额/用量，租户 1 名下多出 %d 行", n)
	}
	// 语种与模式口径与扣费路径同构（排查时按这两列圈试用流量）
	if err := h.db.QueryRow("SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=0 AND biz_kind='text' AND biz_mode='pro'").Scan(&n); err != nil {
		t.Fatalf("统计试用留痕失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("试用留痕应为 1 行 text/pro，实得 %d", n)
	}
}

// TestTrialWindowExpiredRestoresQuota ★ C 段：窗口起点拨到 25 小时前 ⇒ 同一设备仍可发起。
// 改坏了会怎样：trialWindowActive 若写成「Count>0 即耗尽」，第一天试完 5 句的访客
// 永远不能再试用（读侧不清窗口，只有写侧才重置），而且本地测不出来——必须满 24 小时才显形。
func TestTrialWindowExpiredRestoresQuota(t *testing.T) {
	h := trialFixture(t)
	for i := 0; i < 5; i++ {
		if _, err := h.s.Store.RateRecord("trial_dev", trialDev, trialWindowSec); err != nil {
			t.Fatalf("预置设备计数失败: %v", err)
		}
	}
	trialAgeRate(t, h.db, "trial_dev", trialDev, trialWindowSec+3600) // 拨到 25 小时前
	trialIntervalPass(t, h, trialDev)
	w := trialPost(t, h, trialBody(nil))
	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("窗口过期后必须恢复额度，实得 429 / %q", w.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("隔夜放行后应正常出译文 200，实得 %d / %q", w.Code, w.Body.String())
	}
	// 窗口重置后计数从头开始：第 1 句 → 剩 4
	if o := trialParseOK(t, w); o.Left != 4 {
		t.Fatalf("过期窗口重新起算，期望 left=4，实得 %d / %q", o.Left, w.Body.String())
	}
}

// TestTrialIPAndGlobalGates 另两档：IP 到顶 reason=ip（与设备号无关，换浏览器也拦）；
// 平台日预算到顶 reason=global，且告警只留一条（open 去重，刷不出第二遍运维屏）。
func TestTrialIPAndGlobalGates(t *testing.T) {
	t.Run("IP档", func(t *testing.T) {
		h := trialFixture(t)
		for i := 0; i < trialIPDailyDefault; i++ {
			if _, err := h.s.Store.RateRecord("trial_ip", trialIP, trialWindowSec); err != nil {
				t.Fatalf("预置 IP 计数失败: %v", err)
			}
		}
		w := trialPost(t, h, trialBody(map[string]interface{}{"device_id": "brandnewdev1"})) // 新设备号也照样拦
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("IP 到顶应 429，实得 %d / %q", w.Code, w.Body.String())
		}
		if r := trialParseErr(t, w).Details.Reason; r != "ip" {
			t.Fatalf("reason 应为 ip，实得 %q", r)
		}
		if n := atomic.LoadInt64(h.calls); n != 0 {
			t.Fatalf("IP 闸必须在模型之前，假上游却被打 %d 次", n)
		}
	})
	t.Run("全局预算档", func(t *testing.T) {
		h := trialFixture(t)
		for i := 0; i < trialGlobalDailyDefault; i++ {
			if _, err := h.s.Store.RateRecord("trial_day", "global", trialWindowSec); err != nil {
				t.Fatalf("预置全局计数失败: %v", err)
			}
		}
		w := trialPost(t, h, trialBody(nil))
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("平台预算到顶应 429，实得 %d / %q", w.Code, w.Body.String())
		}
		if r := trialParseErr(t, w).Details.Reason; r != "global" {
			t.Fatalf("reason 应为 global，实得 %q", r)
		}
		// 触顶即告警；第二次仍触顶不得再刷一条（CreateAlert 的 open 去重）
		if n := trialAlertCount(t, h.s, "trial_budget"); n != 1 {
			t.Fatalf("预算触顶应告警 1 条，实得 %d", n)
		}
		trialPost(t, h, trialBody(nil))
		trialPost(t, h, trialBody(nil))
		if n := trialAlertCount(t, h.s, "trial_budget"); n != 1 {
			t.Fatalf("同一未处理告警不得刷屏，实得 %d 条", n)
		}
	})
}

// TestTrialIntervalGuard 同设备最小间隔：刚记过一次就再来，出 429 RATE_LIMITED 且带 retry_after。
// retry_after 必须是正数——F-47 之后前端/重试器靠它决定等多久，缺字段等于让客户盲等。
func TestTrialIntervalGuard(t *testing.T) {
	h := trialFixture(t)
	if _, err := h.s.Store.RateRecord("guard_int", "trial:"+trialDev, 1); err != nil {
		t.Fatalf("预置间隔记账失败: %v", err)
	}
	w := trialPost(t, h, trialBody(nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("最小间隔内应 429，实得 %d / %q", w.Code, w.Body.String())
	}
	e := trialParseErr(t, w)
	if e.Code != "RATE_LIMITED" {
		t.Fatalf("间隔拦截应复用 RATE_LIMITED，而不是 TRIAL_EXHAUSTED（后者是额度见底，语义不同）：实得 %s", e.Code)
	}
	if e.RetryAfter <= 0 || e.RetryAfter > trialMinIntervalSec {
		t.Fatalf("retry_after 期望 1~%d 秒，实得 %d", trialMinIntervalSec, e.RetryAfter)
	}
	// 间隔拦截同样不吃配额，也不该走到模型
	if st := trialLoadRate(h.db, "trial_dev", trialDev); st.Count != 0 {
		t.Fatalf("间隔内被拒不该计配额，实得 %d", st.Count)
	}
	if n := atomic.LoadInt64(h.calls); n != 0 {
		t.Fatalf("间隔内被拒不该打模型，假上游被打 %d 次", n)
	}
}

// TestTrialLimitPriority 额度三档的优先序（AGENTS §一·3）：env > system_config > 代码默认。
// 非法值（空串、非数字、0、负数）一律回落下一级——把额度配成 0 不当"关闭试用"，
// 静默关掉入口比数字写错更难查（详见 trialLimit 注释）。
func TestTrialLimitPriority(t *testing.T) {
	h := trialFixture(t)
	s := h.s
	const def = 7
	if got := s.trialLimit("TRIAL_DEVICE_QUOTA", "trial_device_quota", def); got != def {
		t.Fatalf("无任何配置时应取代码默认 %d，实得 %d", def, got)
	}
	if err := s.Store.SetConfig("trial_device_quota", "9"); err != nil {
		t.Fatalf("写 system_config 失败: %v", err)
	}
	if got := s.trialLimit("TRIAL_DEVICE_QUOTA", "trial_device_quota", def); got != 9 {
		t.Fatalf("库配置应压过默认，实得 %d", got)
	}
	t.Setenv("TRIAL_DEVICE_QUOTA", "12")
	if got := s.trialLimit("TRIAL_DEVICE_QUOTA", "trial_device_quota", def); got != 12 {
		t.Fatalf("环境变量应压过库配置，实得 %d", got)
	}
	t.Setenv("TRIAL_DEVICE_QUOTA", "0")
	if got := s.trialLimit("TRIAL_DEVICE_QUOTA", "trial_device_quota", def); got != 9 {
		t.Fatalf("env=0 属非法值，应回落到库配置 9，实得 %d", got)
	}
	t.Setenv("TRIAL_DEVICE_QUOTA", "abc")
	if err := s.Store.SetConfig("trial_device_quota", "-3"); err != nil {
		t.Fatalf("写非法库配置失败: %v", err)
	}
	if got := s.trialLimit("TRIAL_DEVICE_QUOTA", "trial_device_quota", def); got != def {
		t.Fatalf("env 与库都非法时应回落代码默认 %d，实得 %d", def, got)
	}
	// ★ 额度压到 1 时端到端也照得准（运营真会配这个数）：第 1 句 left=0/exhausted=true，第 2 句 429
	if err := s.Store.SetConfig("trial_device_quota", "1"); err != nil {
		t.Fatalf("写额度配置失败: %v", err)
	}
	trialIntervalPass(t, h, trialDev)
	w := trialPost(t, h, trialBody(nil))
	if w.Code != http.StatusOK {
		t.Fatalf("配额 1 时第 1 句应放行，实得 %d / %q", w.Code, w.Body.String())
	}
	o := trialParseOK(t, w)
	if o.Left != 0 || !o.Exhausted {
		t.Fatalf("配额 1 用完后应 left=0 且 exhausted=true（前端据此立刻切注册引导），实得 left=%d exhausted=%v", o.Left, o.Exhausted)
	}
	trialIntervalPass(t, h, trialDev)
	if w2 := trialPost(t, h, trialBody(nil)); w2.Code != http.StatusTooManyRequests {
		t.Fatalf("配额 1 时第 2 句应 429，实得 %d / %q", w2.Code, w2.Body.String())
	}
}

// TestTrialLangAllowed 语种白名单与前端语言表同源：KB 语种 + zh，大小写不敏感，其余一律拒。
func TestTrialLangAllowed(t *testing.T) {
	if !trialLangAllowed("zh") {
		t.Fatalf("zh（外语翻回中文）必须在试用白名单里")
	}
	if len(config.TranslateLangs) == 0 {
		t.Fatalf("TranslateLangs 为空，白名单用例失去意义")
	}
	first := config.TranslateLangs[0]
	if !trialLangAllowed(strings.ToLower(first)) {
		t.Fatalf("KB 语种 %s 应放行", first)
	}
	if !trialLangAllowed(strings.ToUpper(first)) {
		t.Fatalf("语种码大小写不该影响放行")
	}
	for _, bad := range []string{"", "klingon", "zh-hant", "e n"} {
		if trialLangAllowed(bad) {
			t.Fatalf("非法语种 %q 不该放行", bad)
		}
	}
}

// TestTrialRouteRegisteredAnonymously 试用面**无需任何凭证**即可打到闸门：
// 引擎未就绪时诚实 503（可重试语义）、不 panic、更不 401/403，且不吃配额。
// 路由是否真挂在 s.mux 上不在这里验——route_auth_gate_test.go 会静态解析全部
// `s.mux.HandleFunc` 注册点并要求公开路由在白名单里写明理由（本文件的路径已登记），
// 「路径通不通、是不是整页 HTML 兜底」由 scripts/uat/api_uat.sh 的试用段以真实 HTTP 打。
func TestTrialRouteRegisteredAnonymously(t *testing.T) {
	h := trialFixture(t)
	s := h.s
	s.Engine = nil // 依赖未就绪：应诚实 503，绝不能 panic，也不能 401（试用是匿名面）
	r := httptest.NewRequest(http.MethodPost, "/api/trial/translate", strings.NewReader(`{"text":"你好","target_lang":"en","device_id":"`+trialDev+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.7:4242"
	w := httptest.NewRecorder()
	s.handleTrialTranslate(w, r)
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Fatalf("试用面不得要求登录，实得 %d", w.Code)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("引擎未就绪应回 503（可重试语义），实得 %d / %q", w.Code, w.Body.String())
	}
	if st := trialLoadRate(h.db, "trial_dev", trialDev); st.Count != 0 {
		t.Fatalf("依赖未就绪不该计配额，实得 %d", st.Count)
	}
	// Store 也在 nil 之列：两个依赖任一没就绪都不能放行到后面的逻辑
	s.Store = nil
	if w2 := func() *httptest.ResponseRecorder {
		r2 := httptest.NewRequest(http.MethodPost, "/api/trial/translate", strings.NewReader(`{"text":"你好","target_lang":"en","device_id":"`+trialDev+`"}`))
		w2 := httptest.NewRecorder()
		s.handleTrialTranslate(w2, r2)
		return w2
	}(); w2.Code != http.StatusServiceUnavailable {
		t.Fatalf("Store 未就绪同样应 503，实得 %d / %q", w2.Code, w2.Body.String())
	}
}

// ============ 测试辅助（直接摸 rate_limits / usage_ledger / alerts 表） ============

// trialLoadRate 直读 rate_limits 某 (scope,key) 的计数。
func trialLoadRate(db *sql.DB, scope, key string) store.RateState {
	var st store.RateState
	row := db.QueryRow("SELECT count, window_start, lock_until FROM rate_limits WHERE scope=? AND key=?", scope, key)
	if err := row.Scan(&st.Count, &st.WindowStart, &st.LockUntil); err != nil {
		if err == sql.ErrNoRows {
			return store.RateState{}
		}
		panic("读取 rate_limits 失败: " + err.Error())
	}
	return st
}

// trialAgeRate 把某 (scope,key) 的窗口起点拨到 secs 秒之前（模拟隔夜）。
func trialAgeRate(t *testing.T, db *sql.DB, scope, key string, secs int64) {
	t.Helper()
	res, err := db.Exec("UPDATE rate_limits SET window_start=? WHERE scope=? AND key=?",
		time.Now().Unix()-secs, scope, key)
	if err != nil {
		t.Fatalf("拨窗口起点失败: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("拨窗口起点应命中 1 行，实得 %d（预置计数根本没写进去，后续断言全是假绿）", n)
	}
}

// trialAlertCount 数某类告警条数（kind 精确匹配；ListAlerts 传 0 即全平台）。
func trialAlertCount(t *testing.T, s *Server, kind string) int {
	t.Helper()
	list, err := s.Store.ListAlerts(0, "", 500)
	if err != nil {
		t.Fatalf("读取告警失败: %v", err)
	}
	var n int
	for _, a := range list {
		if a.Kind == kind {
			n++
		}
	}
	return n
}
