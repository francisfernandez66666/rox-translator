// ============ sales_inbox_lead_gate_test.go · 职责说明 ============
// 四期 W2 · R4（施工图 §5 / §4.2.3）：留资后卖家收件箱可见同一条（含 lead_source 前缀）。
//
// 反证由变异体 M0279 提供：把 bridgeLeadCapturedToInbox 写入的 tenant_id 写死/摘掉
// ⇒ 事件落到别的租户 ⇒ 卖家按自己的 tenant_id 在收件箱读口查不到 ⇒ 本锁判红。
// 内置反证腿：另一租户读口不许看到同一条（证明按 tenant_id 隔离，不是全局可见）。
//
// 夹具自钉 SQLite 方言（AGENTS §一·4 模板），不依赖同包其他测试文件的 helper；
// store.New 内部已跑 SalesInboxMigrate（store.go:69），inbox_events 表在位。
// ================================================
package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/store"
)

// pinLeadInboxDialect 自钉 sqlite 方言并在用例结束后复原全局 config.C（AGENTS §一·4 模板）。
func pinLeadInboxDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// leadInboxHarness 建一套内存库 + Store + Server，供收件箱可见性用例复用。
func leadInboxHarness(t *testing.T) *Server {
	t.Helper()
	pinLeadInboxDialect(t)
	db, err := sql.Open("sqlite", fmt.Sprintf("file:leadinbox_%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())))
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("建 Store 失败: %v", err)
	}
	return &Server{Store: st}
}

// TestLeadCapturedVisibleInSellerInbox R4：留资以 lead.captured 补进收件箱后，
// 卖家收件箱读口能看见同一条，event_id 含 lead-product: 前缀，且按 tenant_id 隔离。
func TestLeadCapturedVisibleInSellerInbox(t *testing.T) {
	s := leadInboxHarness(t)
	const sellerTid = int64(7)
	const otherTid = int64(8)
	src := "lead-product:acme:9f3a"
	eventID := LeadCapturedInboxEventID(src, 42)

	if err := s.bridgeLeadCapturedToInbox(context.Background(), sellerTid, eventID, "42"); err != nil {
		t.Fatalf("桥接写入失败: %v", err)
	}

	// 卖家收件箱读口（ListInboxEvents 与 sales_crm_api.go 同一条按 tenant_id 的查询）。
	rows, err := s.Store.ListInboxEvents(sellerTid, "", 100)
	if err != nil {
		t.Fatalf("收件箱查询失败: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.EventID == eventID && r.Topic == leadCapturedTopic && strings.HasPrefix(r.EventID, leadSourceProductPrefix) {
			found = true
		}
	}
	if !found {
		t.Fatalf("留资事件在卖家租户收件箱不可见（tenant_id 隔离失效或被摘掉）：tid=%d event=%s", sellerTid, eventID)
	}

	// 内置反证腿：另一租户读口不许看到同一条（证明这条查询真的按 tenant_id 收敛）。
	cross, err := s.Store.ListInboxEvents(otherTid, "", 100)
	if err != nil {
		t.Fatalf("跨租户收件箱查询失败: %v", err)
	}
	for _, r := range cross {
		if r.EventID == eventID {
			t.Fatalf("留资事件串到了别的租户收件箱：tenant_id 过滤被摘掉（tid=%d）", otherTid)
		}
	}
}

// TestLeadCapturedInboxWrongTenantNotVisible R4 判别力自证：写入时挂错 tenant_id，
// 卖家读口必须查不到——否则上一条「可见」判据就是对着全集恒真的空转锁。
func TestLeadCapturedInboxWrongTenantNotVisible(t *testing.T) {
	s := leadInboxHarness(t)
	const sellerTid = int64(7)
	eventID := LeadCapturedInboxEventID("lead-product:acme:9f3a", 43)
	// 模拟变异 M0279：写入时不带正确 tenant_id（挂到默认租户 1）。
	if err := s.bridgeLeadCapturedToInbox(context.Background(), 1, eventID, "43"); err != nil {
		t.Fatalf("桥接写入失败: %v", err)
	}
	rows, err := s.Store.ListInboxEvents(sellerTid, "", 100)
	if err != nil {
		t.Fatalf("收件箱查询失败: %v", err)
	}
	for _, r := range rows {
		if r.EventID == eventID {
			t.Fatalf("tenant_id 写错却仍被卖家收件箱读到 ⇒ 隔离判据空转")
		}
	}
}
