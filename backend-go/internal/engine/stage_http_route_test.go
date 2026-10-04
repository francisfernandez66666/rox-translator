// ============================================================================
// stage_http_route_test.go — ★ R-1 修法 C 的**真拨一次**断言（A4b，2026-10-04）。
//
// 与 model_for_stage_test.go（A4）的分工：A4 只证明「解析函数返回哪一档」，
// 本文件证明「HTTP 请求真的带着那一档的 base/model/Authorization 发出去了」。
// 这个区分不是形式：R-1 的现网形态正是"解析看着对、拨出去的是另一份凭据"，
// 只看函数返回值抓不到调用侧把 base/key 抄漏的形态。
//
// 判据（同一条假上游记录每次收到的 model 与 Authorization）：
//
//	① stage=kb_match 且库里只配了 ai_initial ⇒ 收到的必须是 ai_initial 那份
//	   （model 名与密钥都是它的），且**一次都不许**收到全局那一份；
//	② 一档都没配 ⇒ 必须回落全局（正向对照；否则"从没打过全局"这条负向判据是空转）；
//	③ 显式配了 kb_match 自己那一档 ⇒ 本阶段优先，初翻档不许越权。
//
// 反证：把 singleLangRaw 里的 resolveModelForStage 换回 resolveStageModel（即删掉初翻回退档）
//
//	⇒ ①红（那一支会拨到全局）；把回退写成"永远拨初翻"⇒ ②红。
//
// 方言口径（AGENTS §一·4）：本测试族固定内存 SQLite。
// ============================================================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/store"
)

// recordedCall 假上游收到的一次调用（只留判据需要的三样）。
type recordedCall struct {
	Model string
	Auth  string
	Path  string
}

// routeProbe 假上游：把每一次调用记进内存，并回一句合法译文（finish_reason=stop）。
// 刻意**不按 base 分服务器**——两条腿的 base 都指向同一个假上游，
// 判据只看"收到的 model/Authorization 是谁那份"，这样任何把 base 抄错但 key 抄对的形态也能被抓到。
type routeProbe struct {
	mu    sync.Mutex
	calls []recordedCall
	srv   *httptest.Server
}

func (p *routeProbe) record(c recordedCall) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, c)
}

func (p *routeProbe) snapshot() []recordedCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedCall, len(p.calls))
	copy(out, p.calls)
	return out
}

// seen 返回指定 model 是否被拨过（判据①的负向、判据②的正向都走它）。
func (p *routeProbe) seen(model string) bool {
	for _, c := range p.snapshot() {
		if c.Model == model {
			return true
		}
	}
	return false
}

func newRouteProbe(t *testing.T) *routeProbe {
	t.Helper()
	p := &routeProbe{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		p.record(recordedCall{Model: req.Model, Auth: r.Header.Get("Authorization"), Path: r.URL.Path})
		w.Header().Set("Content-Type", "application/json")
		// 译文里不含数字与方括号，避免触发截断自修复/重翻分支把调用次数放大
		fmt.Fprint(w, `{"choices":[{"message":{"content":"Hello there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// routeEngine 起一个「假上游 + 内存 SQLite」的引擎：全局档与阶段档共用同一 URL，
// 只靠 model 名与密钥区分是谁那一份。
func routeEngine(t *testing.T, p *routeProbe) (*Engine, *store.Store) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS §一·4）
	// 全局那一份：R-1 期间这里装的是随机占位 Key，调用必 401 —— 本用例用可识别的假值代表它
	cfg.OnlineAPIBase = p.srv.URL + "/v1"
	cfg.OnlineAPIKey = "sk-global-placeholder"
	cfg.OnlineModel = "global/Model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	st := newTestStore(t)
	// NewEngine 而非结构体字面量：熔断器（breaker）等私有字段由它一并初始化——
	// 拿字面量构造会在 e.breaker.IsOpen() 上直接 nil panic（本文件首跑就撞过）。
	e := NewEngine(cfg, nil, nil, nil)
	e.St = st // 阶段配置从 system_config 读，必须有 St
	return e, st
}

// callOnce 走一次真实单语翻译链路（SingleLangTranslate 是客户面入口，不是解析函数）。
func callOnce(t *testing.T, e *Engine, stage string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := e.SingleLangTranslate(ctx, "你好", "en", nil, "zh", stage); err != nil {
		t.Fatalf("stage=%s 单语翻译调用失败: %v", stage, err)
	}
}

// TestKBMatchLegDialsInitialStageOverHTTP = 断言 A4b①。
func TestKBMatchLegDialsInitialStageOverHTTP(t *testing.T) {
	p := newRouteProbe(t)
	e, st := routeEngine(t, p)
	setStageModels(t, st, config.StageModels{
		config.StageAIInitial: {Provider: "stage_ai_initial", APIBase: p.srv.URL + "/v1", APIKey: "sk-initial-key", Model: "initial/Model"},
	})

	for _, stage := range []string{config.StageKBMatch, config.StageReview} {
		before := len(p.snapshot())
		callOnce(t, e, stage)
		calls := p.snapshot()[before:]
		if len(calls) == 0 {
			t.Fatalf("stage=%s 一次上游都没拨到 ⇒ 判据无从谈起", stage)
		}
		for _, c := range calls {
			if c.Model != "initial/Model" {
				t.Fatalf("stage=%s 实际拨出的 model=%s（期望初翻档 initial/Model）⇒ 修法 C 的二级回退没作用到调用侧", stage, c.Model)
			}
			if c.Auth != "Bearer sk-initial-key" {
				t.Fatalf("stage=%s 的 Authorization=%q（期望初翻档那份密钥）⇒ R-1 的机制正是「解析对、凭据错」", stage, c.Auth)
			}
		}
		// 决定性负向：全局那一份一次都不许被拨到
		if p.seen("global/Model") {
			t.Fatalf("stage=%s 仍拨过全局档 ⇒ 客户面会撞上「阶段配了可用端点、全局是占位 Key」那条 401 死腿", stage)
		}
	}
}

// TestUnconfiguredStagesDialGlobal 正向对照（判据②）：一档都没配时才该拨全局。
// 没有这一条，上面那条"从没拨过全局"的负向锁可能是空转（比如 base 拼错、请求压根没发出去）。
func TestUnconfiguredStagesDialGlobal(t *testing.T) {
	p := newRouteProbe(t)
	e, st := routeEngine(t, p)
	setStageModels(t, st, config.StageModels{})

	callOnce(t, e, config.StageKBMatch)
	if !p.seen("global/Model") {
		t.Fatalf("未配任何阶段时应拨全局档，实际收到 %+v ⇒ 假上游压根没被拨到，负向判据全是空转", p.snapshot())
	}
	for _, c := range p.snapshot() {
		if c.Auth != "Bearer sk-global-placeholder" {
			t.Fatalf("回落全局时应带全局密钥，实际 %q", c.Auth)
		}
	}
}

// TestOwnStageWinsOverInitialFallback 优先序（判据③）：本阶段配了就用本阶段的。
func TestOwnStageWinsOverInitialFallback(t *testing.T) {
	p := newRouteProbe(t)
	e, st := routeEngine(t, p)
	setStageModels(t, st, config.StageModels{
		config.StageAIInitial: {APIBase: p.srv.URL + "/v1", APIKey: "sk-initial-key", Model: "initial/Model"},
		config.StageKBMatch:   {APIBase: p.srv.URL + "/v1", APIKey: "sk-kbmatch-key", Model: "kbmatch/Model"},
	})

	callOnce(t, e, config.StageKBMatch)
	if !p.seen("kbmatch/Model") {
		t.Fatalf("kb_match 自己有配置时应优先用它，实际收到 %+v", p.snapshot())
	}
	if p.seen("initial/Model") {
		t.Fatalf("本阶段已配置却被初翻档越权 ⇒ 优先序被写反")
	}
}
