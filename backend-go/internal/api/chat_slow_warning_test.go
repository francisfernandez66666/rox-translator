// ============================================================================
// chat_slow_warning_test.go — ★ 决策⑪② 慢预警帧断言（2026-10-10）。
//
// 钉住的形态：handleChatStream 在请求级 deadline 到点前 N 秒先发一帧
// warning(reason=slow, seconds_left=N)，让前端给出「继续等待 / 转工单」两按钮；
// 到点行为不变——仍按 F-29 批 D 出 chat_timeout error 帧（本用例同时锁住这一点，
// 防止有人把「预警」误改成「豁免超时」）。
//
// 测试手法：chatStreamTimeout / chatStreamWarnBefore 是包级变量（仅为可测性提级，
// 生产路径恒走 90s/15s 默认），此处注入毫秒级短 deadline 并在 Cleanup 恢复。
// 方言口径（AGENTS §一·4）：自钉 sqlite 并恢复 config.C。
// ============================================================================
package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/iam"
	"translator/internal/store"
)

// slowUpstreamChatHarness 同 failingChatHarness（Store＋引擎＋JWT 全套就绪），
// 差异只在假上游：先睡足一段再回 500——保证引擎卡到 deadline 被 runCtx 掐停，
// 预警帧有机会在超时前发出（若上游秒回，error 帧先于预警，用例就测不到目标形态）。
func slowUpstreamChatHarness(t *testing.T) (*Server, string) {
	t.Helper()
	pinSqliteDialect(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // 远超下面的短 deadline：强制走超时路径
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "sk-test"
	cfg.OnlineModel = "test-model"
	cfg.HunyuanFallbackModel = "fallback/Model"
	config.C = cfg

	db, err := sql.Open("sqlite", "file:slow_warn_mem?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	u, err := st.CreateUser(1, "slow_warn_user", auth.PasswordHash("pw123456"), "慢预警探针", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发测试令牌失败: %v", err)
	}
	eng := engine.NewEngine(cfg, nil, nil, nil)
	eng.St = st
	s := &Server{Store: st, Cfg: cfg, metrics: newMetrics(), Engine: eng}
	return s, tok
}

// TestChatStreamWarnsBeforeDeadline deadline 前 N 秒必须先出 warning 慢预警帧，
// 随后到点仍出 chat_timeout error 帧（到点语义不变）。
func TestChatStreamWarnsBeforeDeadline(t *testing.T) {
	oldTimeout, oldWarn := chatStreamTimeout, chatStreamWarnBefore
	chatStreamTimeout = 300 * time.Millisecond
	chatStreamWarnBefore = 100 * time.Millisecond // 200ms 处发预警，留 100ms 余量
	t.Cleanup(func() { chatStreamTimeout, chatStreamWarnBefore = oldTimeout, oldWarn })

	s, tok := slowUpstreamChatHarness(t)
	w := postChatStream(t, s, tok, "把这段话翻译成英文：这是一段用于触发超时预警的较长文本。")
	body := w.Body.String()

	if !strings.Contains(body, `"type":"warning"`) {
		t.Fatalf("deadline 前 15s（此处为注入的提前量）应先发 warning 慢预警帧，实际响应体：\n%s", body)
	}
	if !strings.Contains(body, `"reason":"slow"`) {
		t.Fatalf("warning 帧必须带 reason=slow（前端按它渲染「继续等待/转工单」）：\n%s", body)
	}
	if !strings.Contains(body, "chat_timeout") {
		t.Fatalf("到点行为不许变：仍须按 F-29 出 chat_timeout error 帧，实际响应体：\n%s", body)
	}
	// 顺序锁：预警帧必须先于超时 error 帧出现（先拒后警＝预警形同虚设）。
	if strings.Index(body, `"type":"warning"`) > strings.Index(body, "chat_timeout") {
		t.Fatalf("warning 帧必须先于 chat_timeout error 帧：\n%s", body)
	}
}
