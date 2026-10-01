// ============ greeting_canned_timeout_test.go · 职责说明 ============
// ★ 0AF（2026-10-01）现网缺陷的**HTTP 层**证据：挂件首屏 greet 冷缓存语种（泰文）在上游要跑 >30 秒，
// 主站 `/assist-api` 反代 30 秒先砍连接 ⇒ 访客拿到 HTTP 502 / 30.001890s / `UPSTREAM_UNAVAILABLE`，
// 而缓存行永远写不上，下一个访客还是 502。
//
// 本文件只测一条链：**「接口在自有预算点就 200 出中文，后台补翻随后把译文写上，第二次 greet 命中译文」**。
// engine 侧（../engine/localize_async_test.go）锁的是机制，这一层锁的是"访客真的不会再看到 502"——
// 中间隔着 handler，正是历史上「单测绿、现网红」最常藏的那一层。
//
// ⚠️ 时序口径：不靠 sleep 赌「goroutine 跑到哪一步」。上游夹具第 1 枪挂住、其余枪立刻回正文；
// 「后台到底写没写上」用**有 deadline 的轮询**判——本包拿不到 Engine 的 WaitGroup（它不是导出面），
// 而轮询问的是**结果**（库里那一行在不在），不是调度运气，所以不构成假绿。
// =============================================
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/assist/engine"
	"translator/internal/assist/llm"
	"translator/internal/assist/store"
)

// slowThenOKStub 一条冷语种假上游：第 1 枪挂住（模拟"这一语种就是要几十秒"），其余枪立刻回正文。
type slowThenOKStub struct {
	hits    atomic.Int64
	release chan struct{}
	once    sync.Once
	content string
	block   time.Duration
}

// serve 起这个假上游并返回 base_url。
func (s *slowThenOKStub) serve(t *testing.T) string {
	t.Helper()
	s.release = make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		n := s.hits.Add(1)
		if n == 1 {
			select {
			case <-time.After(s.block):
			case <-s.release:
			case <-r.Context().Done(): // 有界同步腿到点撤了，这一枪没人在等
				return
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": s.content},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"completion_tokens": 10},
		})
		_, _ = w.Write(payload)
	}))
	// 注册顺序＝反序执行：先放行挂住的枪，再关服务，否则 srv.Close 会等在跑的请求上（白等 block 那么久）。
	t.Cleanup(func() { s.once.Do(func() { close(s.release) }) })
	t.Cleanup(srv.Close)
	return srv.URL
}

// releaseAll 主动放行所有挂住的枪（Once 保证与 cleanup 不 double close）。
func (s *slowThenOKStub) releaseAll() { s.once.Do(func() { close(s.release) }) }

// TestGreetingColdLangNeverReturns502 现网那条 502 的正身，在 HTTP 层判三件事：
//  1. 冷语种第 1 次 greet：**200**、欢迎词是中文原文、耗时停在自有预算（远小于反代 30 秒）；
//  2. 请求链之外那一枪把译文写进了缓存；
//  3. 第 2 次 greet 直接拿到泰文，且一次上游都不许多打。
//
// 反证一：把 engine 侧的有界同步腿改回无条件等待 ⇒ 第 1 步会等满 stub 挂起的那一段，
// elapsed 判据红（现网正是这一档被反代打成 502）。
// 反证二：删掉后台腿，或让它复用请求 ctx ⇒ 第 2 步轮询到 deadline 仍拿不到缓存，红。
func TestGreetingColdLangNeverReturns502(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/greeting.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	const welcome = "有什么想了解的？我可以帮你选功能。"
	const thai = "อยากทราบอะไรไหม? ฉันช่วยเลือกให้ได้"
	stub := &slowThenOKStub{content: thai, block: 20 * time.Second} // 明显大于自有预算，又不至于把测试挂死
	base := stub.serve(t)

	// LLM 三项走 configs（管理台同源口径）：engine 侧 ensureLLM 据此建 client。
	// 生产就是这么接的，测试不许为了省事去改构造签名——那会让这条链绕过真正上线的那条腿。
	_ = db.SetConfig("welcome", welcome)
	// chips 留空：本用例只判欢迎词那一枪，别把第二枪混进枪数判据（空串在 LocalizeChips 里整段短路）
	_ = db.SetConfig("quick_chips", "")
	_ = db.SetConfig("llm_base_url", base)
	_ = db.SetConfig("llm_api_key", "k")
	_ = db.SetConfig("llm_model", "m")
	_ = db.SetConfig("canned_translate_timeout_sec", "1")

	srv := httptest.NewServer(NewServer(db, engine.New(db, llm.New(nil, 5)), "test-token", "*").Handler())
	t.Cleanup(srv.Close)

	t0 := time.Now()
	code, body := doJSON(t, srv, "GET", "/api/assist/greeting?lang=th", "", nil)
	elapsed := time.Since(t0)
	if code != http.StatusOK {
		t.Fatalf("冷语种首屏不该再是 5xx（现网读数＝502 / 30.0s / UPSTREAM_UNAVAILABLE），实际 code=%d body=%v", code, body)
	}
	if got, _ := body["greeting"].(string); got != welcome {
		t.Fatalf("超时后必须原样出中文原文（绝不编造译文），实际 %q", got)
	}
	// 自有预算 1 秒；这里给到 8 秒余量仍远小于反代 30 秒——判的是"没有等满上游那一整段"
	if elapsed >= 8*time.Second {
		t.Fatalf("greet 等了 %s ⇒ 有界同步腿没生效，这一条会被 /assist-api 的 30 秒砍成 502", elapsed)
	}

	// 后台腿：有 deadline 的轮询
	deadline := time.Now().Add(15 * time.Second)
	var cached string
	for time.Now().Before(deadline) {
		if cached = db.GetConfig("i18n:welcome:th", ""); cached != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stub.releaseAll()
	if !strings.Contains(cached, thai) {
		t.Fatalf("后台补翻没把译文写进缓存（下一次 greet 还会撞同一次慢上游）：上游被打 %d 次，缓存 %q",
			stub.hits.Load(), firstLineOf(cached))
	}

	// 第二次：命中缓存 ⇒ 直接泰文，且不再多打一枪
	code2, body2 := doJSON(t, srv, "GET", "/api/assist/greeting?lang=th", "", nil)
	got2, _ := body2["greeting"].(string)
	if code2 != http.StatusOK || got2 != thai {
		t.Fatalf("第二次 greet 应命中译文：%d %q", code2, got2)
	}
	if n := stub.hits.Load(); n != 2 {
		t.Fatalf("上游应只被打两枪（超时那一枪 + 后台那一枪），实际 %d 次 ⇒ 缓存没被复用或多起了一条腿", n)
	}
}

// firstLineOf 取缓存值首行（指纹）；报错里只给这一行，别把整段译文糊进测试输出。
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
