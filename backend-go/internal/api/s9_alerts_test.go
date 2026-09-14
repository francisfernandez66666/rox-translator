// S9 Alertmanager 收口单测：鉴权 403、firing 落告警中心、resolved 忽略。
package api

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newS9Server 构造带 SQLite 存储与 ADMIN_TOKEN 的临时服务实例（S9 收口端点测试用）。
func newS9Server(t *testing.T) *Server {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st, Cfg: &config.Config{AdminToken: "tok-123"}}
}

// TestAlertmanagerWebhook 验证收口端点：无/错 token 拒绝、firing 落库、resolved 忽略。
func TestAlertmanagerWebhook(t *testing.T) {
	s := newS9Server(t)
	body := `{"alerts":[{"status":"firing","labels":{"alertname":"TranslatorDown","severity":"critical"},"annotations":{"summary":"翻译服务 up=0 超过 2 分钟"},"startsAt":"2026-09-14T00:00:00Z"},
		{"status":"resolved","labels":{"alertname":"DiskFull"},"startsAt":"x"}]}`
	// 无凭证 → 403
	r := httptest.NewRequest("POST", "/api/alerts/alertmanager", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleAlertmanagerWebhook(w, r)
	if w.Code != 403 {
		t.Fatalf("缺凭证应 403，实际 %d", w.Code)
	}
	// 正确凭证 → 200，firing 1 条落库（critical），resolved 忽略
	r = httptest.NewRequest("POST", "/api/alerts/alertmanager", strings.NewReader(body))
	r.Header.Set("X-Admin-Token", "tok-123")
	w = httptest.NewRecorder()
	s.handleAlertmanagerWebhook(w, r)
	if w.Code != 200 {
		t.Fatalf("应 200，实际 %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["accepted"].(float64) != 1 {
		t.Fatalf("firing 应受理 1 条: %v", resp)
	}
	alerts, _ := s.Store.ListAlerts(0, "", 10)
	if len(alerts) != 1 || alerts[0].Level != "critical" || !strings.HasPrefix(alerts[0].Kind, "prom:") {
		t.Fatalf("告警中心落库异常: %+v", alerts)
	}
}
