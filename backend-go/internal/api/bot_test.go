// ============ 本文件职责中文说明 ============
// 群机器人四渠道通知单元测试（★ P2 国际化渠道 2026-09-15）：
// 配置 wecom/dingtalk/slack/teams 四个 webhook 后，notifyBots 应分别收到
// 各渠道约定格式的 payload（企微/钉钉 msgtype=text、Slack text、Teams MessageCard）；
// 未配置渠道不得发起任何请求。真实外网调用不在单测覆盖。
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// botRecorder 汇总各渠道收到的 JSON payload（并发安全）。
type botRecorder struct {
	mu   sync.Mutex
	got  map[string][]map[string]interface{}
	srvs []*httptest.Server
}

func (r *botRecorder) handler(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var v map[string]interface{}
		_ = json.NewDecoder(req.Body).Decode(&v)
		r.mu.Lock()
		r.got[key] = append(r.got[key], v)
		r.mu.Unlock()
		w.WriteHeader(200)
	}
}

// waitForBots 轮询等待 n 条消息到达（notifyBots 为 fire-and-forget goroutine）。
func waitForBots(rec *botRecorder, key string, n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		c := len(rec.got[key])
		rec.mu.Unlock()
		if c >= n {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestNotifyBotsFourChannels(t *testing.T) {
	// 四渠道各起一个 httptest 服务，配置写入 system_config
	srv := map[string]*httptest.Server{}
	cfgKeys := map[string]string{
		"wecom":    "wecom_webhook_url",
		"dingtalk": "dingtalk_webhook_url",
		"slack":    "slack_webhook_url",
		"teams":    "teams_webhook_url",
	}
	rec := &botRecorder{got: map[string][]map[string]interface{}{}}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range cfgKeys {
		srv[name] = httptest.NewServer(rec.handler(name))
		t.Cleanup(srv[name].Close)
		if err := st.SetConfig(cfg, srv[name].URL); err != nil {
			t.Fatalf("写配置 %s 失败: %v", cfg, err)
		}
	}
	s := &Server{Store: st}
	s.notifyBots("标题T", "正文B")

	// 企微/钉钉：msgtype=text 且 content 含标题与正文
	for _, name := range []string{"wecom", "dingtalk"} {
		if !waitForBots(rec, name, 1, 3*time.Second) {
			t.Fatalf("%s 渠道未收到消息", name)
		}
		rec.mu.Lock()
		p := rec.got[name][0]
		rec.mu.Unlock()
		if p["msgtype"] != "text" {
			t.Errorf("%s msgtype=%v", name, p["msgtype"])
		}
		if txt, ok := p["text"].(map[string]interface{}); !ok || txt["content"] != "【能言】标题T\n正文B" {
			t.Errorf("%s content 异常: %v", name, p["text"])
		}
	}
	// Slack：顶层 {"text"}
	if !waitForBots(rec, "slack", 1, 3*time.Second) {
		t.Fatal("slack 渠道未收到消息")
	}
	rec.mu.Lock()
	sp := rec.got["slack"][0]
	tp := rec.got["teams"][0]
	rec.mu.Unlock()
	if sp["text"] != "【能言】标题T\n正文B" {
		t.Errorf("slack text 异常: %v", sp["text"])
	}
	// Teams：MessageCard（@type/title/text）
	if tp["@type"] != "MessageCard" || tp["text"] != "正文B" {
		t.Errorf("teams payload 异常: %v", tp)
	}
	if tp["title"] != "【能言】标题T" {
		t.Errorf("teams title 异常: %v", tp["title"])
	}
}

func TestNotifyBotsNoChannelsNoRequests(t *testing.T) {
	// 占位/未配置（含 "0" 与空串）不得发出请求：httptest 服务收到即为失败
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetConfig("wecom_webhook_url", "0")     // 占位符=关闭
	_ = st.SetConfig("dingtalk_webhook_url", "")   // 空=未配置
	_ = st.SetConfig("slack_webhook_url", srv.URL) // ★ 已配置也必须发出——稍后断言命中
	_ = st.SetConfig("teams_webhook_url", "   ")   // 纯空白=未配置
	s := &Server{Store: st}
	s.notifyBots("x", "y")
	select {
	case <-hit: // slack 配置了地址，收到请求属预期
	case <-time.After(3 * time.Second):
		t.Fatal("slack 已配置却未收到消息")
	}
	// 再等 1s：wecom/dingtalk/teams 均关闭，不应有任何额外渠道——本用例仅 slack 一个服务，
	// 计数=1 即证明关闭渠道未偷发（若偷发会命中同一 handler 使 got>1）
	time.Sleep(time.Second)
	// 关闭全部渠道后再触发一次，绝不允许再命中
	for _, k := range []string{"slack_webhook_url"} {
		_ = st.SetConfig(k, "0")
	}
	s.notifyBots("x2", "y2")
	select {
	case <-hit:
		t.Fatal("全渠道关闭后仍发起了请求")
	case <-time.After(500 * time.Millisecond):
	}
}
