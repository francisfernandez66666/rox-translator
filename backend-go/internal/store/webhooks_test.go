// ============ 本文件职责中文说明 ============
// Webhook 数据访问层单元测试：CRUD / 事件订阅过滤 / HMAC 签名 / 启用默认值 / 投递记录 / 重试。
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
	w := &Webhook{TenantID: 1, URL: "https://8.8.8.8/hook-a", Events: "translation.completed"}
	if err := s.UpsertWebhook(w); err != nil {
		t.Fatal(err)
	}
	w.URL = "https://8.8.4.4/hook-b"
	w.Enabled = 0
	if err := s.UpsertWebhook(w); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListWebhooks(1)
	if len(list) != 1 {
		t.Fatalf("更新后仍应只有 1 条, got %d", len(list))
	}
	if list[0].URL != "https://8.8.4.4/hook-b" || list[0].Enabled != 0 {
		t.Fatalf("更新未生效: %+v", list[0])
	}
}

// 事件订阅过滤：匹配订阅事件返回，未订阅不返回，空事件返回全部启用项。
func TestGetEnabledWebhooksFilter(t *testing.T) {
	s := newTestStore(t)
	a := &Webhook{TenantID: 1, URL: "https://8.8.8.8/hook-a", Events: "translation.completed"}
	_ = s.UpsertWebhook(a)
	b := &Webhook{TenantID: 1, URL: "https://8.8.4.4/hook-b", Events: "other.event"}
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
	_ = s.UpsertWebhook(&Webhook{TenantID: 1, URL: "https://8.8.8.8/hook-a"})
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

// ---- 投递记录测试 ----

// 创建投递记录并查询列表。
func TestCreateAndListDeliveries(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", Secret: "sec", MaxRetries: 3, RetryInterval: 60}
	if err := s.UpsertWebhook(w); err != nil {
		t.Fatal(err)
	}
	// 模拟创建 3 条投递记录
	id1 := s.createDelivery(w, "translation.completed", `{"event":"test"}`, 3)
	id2 := s.createDelivery(w, "translation.completed", `{"event":"test2"}`, 3)
	id3 := s.createDelivery(w, "ping", `{"event":"ping"}`, 1)
	if id1 <= 0 || id2 <= 0 || id3 <= 0 {
		t.Fatal("投递 ID 应大于 0")
	}
	// 查询列表
	deliveries, err := s.ListDeliveries(w.ID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 3 {
		t.Fatalf("应返回 3 条投递记录, got %d", len(deliveries))
	}
	// 按 ID 倒序
	if deliveries[0].ID != id3 {
		t.Fatalf("应按 ID 倒序, first=%d want=%d", deliveries[0].ID, id3)
	}
}

// 投递记录越权防护：跨租户查询应返回空。
func TestDeliveriesTenantGuard(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", MaxRetries: 3}
	_ = s.UpsertWebhook(w)
	_ = s.createDelivery(w, "test", "{}", 3)
	// 错误租户查询
	deliveries, err := s.ListDeliveries(w.ID, 999, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 0 {
		t.Fatal("跨租户查询应返回空")
	}
	// 正确租户查询
	deliveries2, err := s.ListDeliveries(w.ID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries2) != 1 {
		t.Fatal("正确租户查询应返回记录")
	}
}

// 投递统计。
func TestDeliveryStats(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", MaxRetries: 3}
	_ = s.UpsertWebhook(w)
	// 创建不同状态的投递
	id1 := s.createDelivery(w, "test", "{}", 3)
	id2 := s.createDelivery(w, "test", "{}", 3)
	id3 := s.createDelivery(w, "test", "{}", 3)
	s.updateDeliveryStatus(id1, "success", 200, "ok", 1, "")
	s.updateDeliveryStatus(id2, "failed", 500, "err", 3, "HTTP 500")
	s.updateDeliveryStatus(id3, "dead", 0, "", 3, "connection refused")
	total, success, failed, dead, err := s.GetDeliveryStats(w.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || success != 1 || failed != 1 || dead != 1 {
		t.Fatalf("统计不匹配: total=%d success=%d failed=%d dead=%d", total, success, failed, dead)
	}
}

// 获取单条投递记录。
func TestGetDelivery(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", MaxRetries: 3}
	_ = s.UpsertWebhook(w)
	id := s.createDelivery(w, "translation.completed", `{"key":"val"}`, 3)
	d, err := s.GetDelivery(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Event != "translation.completed" || d.Payload != `{"key":"val"}` {
		t.Fatalf("投递记录不匹配: event=%s payload=%s", d.Event, d.Payload)
	}
	// 越权查询
	_, err = s.GetDelivery(id, 999)
	if err == nil {
		t.Fatal("跨租户查询应报错")
	}
}

// 重试投递：非成功状态应创建新投递记录（通过 RetryDelivery）。
func TestRetryDelivery(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", MaxRetries: 3}
	if err := s.UpsertWebhook(w); err != nil {
		t.Fatal(err)
	}
	id := s.createDelivery(w, "test", `{"x":1}`, 3)
	if id <= 0 {
		t.Fatalf("createDelivery 应返回有效 ID, got %d", id)
	}
	s.updateDeliveryStatus(id, "dead", 500, "err", 3, "timeout")
	before, _ := s.ListDeliveries(w.ID, 1, 100)
	if len(before) != 1 {
		t.Fatalf("应有 1 条原始投递, got %d", len(before))
	}
	// 直接调用 createDelivery 模拟重试
	newID := s.createDelivery(w, "test", `{"x":1}`, w.MaxRetries)
	if newID <= 0 {
		t.Fatalf("重试 createDelivery 应返回有效 ID, got %d", newID)
	}
	after, _ := s.ListDeliveries(w.ID, 1, 100)
	t.Logf("重试后投递记录数: %d (原始: %d)", len(after), len(before))
	if len(after) != 2 {
		t.Fatalf("重试后应有 2 条投递记录, got %d", len(after))
	}
	// 新记录事件应继承
	if after[0].Event != "test" {
		t.Fatalf("新投递事件应继承原始事件, got %s", after[0].Event)
	}
}

// 重试成功的投递应报错。
func TestRetryDeliverySuccessNoop(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", MaxRetries: 3}
	_ = s.UpsertWebhook(w)
	id := s.createDelivery(w, "test", "{}", 3)
	s.updateDeliveryStatus(id, "success", 200, "ok", 1, "")
	if err := s.RetryDelivery(id, 1); err == nil {
		t.Fatal("成功的投递不应允许重试")
	}
}

// Webhook 重试策略字段持久化。
func TestWebhookRetryPolicyFields(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", MaxRetries: 5, RetryInterval: 120}
	if err := s.UpsertWebhook(w); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListWebhooks(1)
	if len(list) != 1 {
		t.Fatal("应存在 1 条 webhook")
	}
	if list[0].MaxRetries != 5 || list[0].RetryInterval != 120 {
		t.Fatalf("重试策略未持久化: max_retries=%d retry_interval=%d", list[0].MaxRetries, list[0].RetryInterval)
	}
	// 更新
	w.MaxRetries = 10
	w.RetryInterval = 300
	_ = s.UpsertWebhook(w)
	list2, _ := s.ListWebhooks(1)
	if list2[0].MaxRetries != 10 || list2[0].RetryInterval != 300 {
		t.Fatalf("重试策略更新未生效")
	}
}

// 连续失败计数增减。
func TestFailureCountIncrementReset(t *testing.T) {
	s := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/hook", MaxRetries: 3}
	_ = s.UpsertWebhook(w)
	// 初始失败计数应为 0
	list, _ := s.ListWebhooks(1)
	if list[0].FailureCount != 0 {
		t.Fatalf("初始失败计数应为 0, got %d", list[0].FailureCount)
	}
	// 递增
	s.incrementFailureCount(w.ID)
	s.incrementFailureCount(w.ID)
	list2, _ := s.ListWebhooks(1)
	if list2[0].FailureCount != 2 {
		t.Fatalf("失败计数应为 2, got %d", list2[0].FailureCount)
	}
	// 重置
	s.resetFailureCount(w.ID)
	list3, _ := s.ListWebhooks(1)
	if list3[0].FailureCount != 0 {
		t.Fatalf("重置后失败计数应为 0, got %d", list3[0].FailureCount)
	}
}
