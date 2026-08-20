// ============ 本文件职责中文说明 ============
// Webhook 数据访问层单元测试：CRUD / 事件订阅过滤 / HMAC 签名 / 启用默认值。
// 使用内存 SQLite（:memory:）构建独立 Store 实例，不依赖业务数据。
// ========================================
package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestStore 创建基于内存 SQLite 的 Store（每次测试独立，互不污染）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return s
}

// 新增 webhook：默认启用 + 默认事件。
func TestUpsertWebhookDefaults(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", Secret: "sec"}
	if err := s.UpsertWebhook(w); err != nil {
		t.Fatalf("新增 webhook 失败: %v", err)
	}
	if w.ID <= 0 {
		t.Fatal("新增后应回填 ID")
	}
	if w.Enabled != 1 {
		t.Fatalf("新增应默认启用, got enabled=%d", w.Enabled)
	}
	if w.Events != "translation.completed" {
		t.Fatalf("新增应默认事件, got events=%q", w.Events)
	}
}

// 空 URL 应报错。
func TestUpsertWebhookEmptyURL(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: ""}
	if err := s.UpsertWebhook(w); err == nil {
		t.Fatal("空 URL 应报错")
	}
}

// 更新已有 webhook（不改 events 时保持原值）。
func TestUpsertWebhookUpdate(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://a.com", Events: "translation.completed"}
	if err := s.UpsertWebhook(w); err != nil {
		t.Fatal(err)
	}
	w.URL = "https://b.com"
	w.Enabled = 0
	if err := s.UpsertWebhook(w); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListWebhooks(1)
	if len(list) != 1 {
		t.Fatalf("更新后仍应只有 1 条, got %d", len(list))
	}
	if list[0].URL != "https://b.com" || list[0].Enabled != 0 {
		t.Fatalf("更新未生效: %+v", list[0])
	}
}

// 事件订阅过滤：匹配订阅事件返回，未订阅不返回，空事件返回全部启用项。
func TestGetEnabledWebhooksFilter(t *testing.T) {
	s := newTestStore(t)
	a := &Webhook{TenantID: 1, URL: "https://a.com", Events: "translation.completed"}
	_ = s.UpsertWebhook(a)
	b := &Webhook{TenantID: 1, URL: "https://b.com", Events: "other.event"}
	_ = s.UpsertWebhook(b)
	// 将 b 停用（新增时默认启用，更新关闭）
	b.Enabled = 0
	_ = s.UpsertWebhook(b)
	if len(mustEnabled(t, s, 1, "translation.completed")) != 1 {
		t.Fatal("应命中订阅 translation.completed 的 webhook")
	}
	if len(mustEnabled(t, s, 1, "unsubscribed")) != 0 {
		t.Fatal("未订阅事件不应命中")
	}
	// 空事件 = 不过滤（force ping 场景），停用的仍排除
	all := mustEnabled(t, s, 1, "")
	if len(all) != 1 {
		t.Fatalf("空事件应返回全部启用项(停用排除), got %d", len(all))
	}
}

// HMAC 签名：secret 一致则签名一致；secret 为空返回空串。
func TestSignWebhook(t *testing.T) {
	body := []byte(`{"a":1}`)
	sig1 := SignWebhook(body, "secret123")
	sig2 := SignWebhook(body, "secret123")
	if sig1 == "" || sig1 != sig2 {
		t.Fatalf("相同 secret 签名应一致: %q vs %q", sig1, sig2)
	}
	// 用标准 HMAC 校验签名正确性
	mac := hmac.New(sha256.New, []byte("secret123"))
	mac.Write(body)
	if hex.EncodeToString(mac.Sum(nil)) != sig1 {
		t.Fatal("签名与 HMAC-SHA256 计算结果不一致")
	}
	if SignWebhook(body, "") != "" {
		t.Fatal("空 secret 应返回空签名")
	}
}

// 删除 webhook：跨租户删除应被拒绝（越权防护）。
func TestDeleteWebhookTenantGuard(t *testing.T) {
	s := newTestStore(t)
	_ = s.UpsertWebhook(&Webhook{TenantID: 1, URL: "https://a.com"})
	list, _ := s.ListWebhooks(1)
	if len(list) != 1 {
		t.Fatal("应存在 1 条 webhook")
	}
	// 用错误租户删除 → 不影响
	if err := s.DeleteWebhook(list[0].ID, 999); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ListWebhooks(1)
	if len(after) != 1 {
		t.Fatal("跨租户删除不应生效")
	}
	// 正确租户删除 → 生效
	if err := s.DeleteWebhook(list[0].ID, 1); err != nil {
		t.Fatal(err)
	}
	after2, _ := s.ListWebhooks(1)
	if len(after2) != 0 {
		t.Fatal("正确租户删除应生效")
	}
}

// containsEvent 事件串匹配工具测试。
func TestContainsEvent(t *testing.T) {
	cases := []struct {
		events, target string
		want           bool
	}{
		{"translation.completed", "translation.completed", true},
		{"a.event, translation.completed", "translation.completed", true},
		{"a.event", "translation.completed", false},
		{"", "translation.completed", false},
	}
	for _, c := range cases {
		if got := containsEvent(c.events, c.target); got != c.want {
			t.Errorf("containsEvent(%q,%q)=%v want %v", c.events, c.target, got, c.want)
		}
	}
}

// 辅助：查询启用 webhook 并忽略错误。
func mustEnabled(t *testing.T, s *Store, tid int64, event string) []*Webhook {
	t.Helper()
	hooks, err := s.GetEnabledWebhooks(tid, event)
	if err != nil {
		t.Fatalf("GetEnabledWebhooks 失败: %v", err)
	}
	return hooks
}
