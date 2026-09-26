// f63_audit_detail_test.go —— F-63 资金审计 detail 行为锁（批 I-3 补断言，2026-09-27）。
//
// 缺陷形态（〇-U 实测）：order_pay 小票渠道分支 detail 是空串、order_refund 是固定串
// 「权益已回收」——审计行只能证明「谁确认/退过款」，回答不了「哪一单、多少钱、什么渠道」，
// 而这两类恰是资金纠纷与合规回查的唯一轨迹来源。批 I-3 已统一口径
// （auditOrder：`订单 <单号>｜金额 <分>｜渠道 <渠道>［｜尾注］`，见 audit_detail.go 头注）。
//
// 本测按修复文档 §3.3「补断言」的规格落：动作发生 → 最新审计行 detail 含订单号且含金额分，
// 替代旧「有审计行即绿」。走真实 handler（handleOrderPay / handleOrderRefund），
// 不直接调 auditOrder——纯函数单测锁不住「调用点把 detail 传成空串」的回归形态。
//
// 方言：自钉内存 SQLite（AGENTS §一·4，pinSqliteDialect）。
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// latestAuditDetail 取指定动作的最新审计行 detail（无行返回空串＋false）。
func latestAuditDetail(t *testing.T, s *Server, action string) (string, bool) {
	t.Helper()
	rows, err := s.Store.ListAuditFilter(0, action, "orders", 0, "", "", 10)
	if err != nil {
		t.Fatalf("读取审计行失败: %v", err)
	}
	if len(rows) == 0 {
		return "", false
	}
	return rows[0].Detail, true // ListAuditFilter 按时间倒序，[0] 即最新
}

// TestF63OrderPayRefundAuditDetail F-63：确认支付/退款的最新审计 detail 必须含单号与金额分。
func TestF63OrderPayRefundAuditDetail(t *testing.T) {
	s, sqlDB, tok := newF44Probe(t) // 复用 F-44 闸门的最小服务栈（内存库＋平台超管）
	// 超管审计视图（ListAuditFilter tid=0）会 LEFT JOIN tenants 取租户名；
	// store.New 的迁移不建 tenants（租户域由 Ten 承担），这里补一张最小表让视图跑得通。
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT DEFAULT '')`); err != nil {
		t.Fatalf("建最小 tenants 表失败: %v", err)
	}
	// 种一张 9.99 元微信小票订单（无包，纯充值单，pkgID=0 不触发订阅身份清理分支）
	o, err := s.Store.CreateOrderChannel(1, 200000, 9.99, 1, "wechat", "")
	if err != nil {
		t.Fatalf("创建订单失败: %v", err)
	}
	post := func(handler func(http.ResponseWriter, *http.Request), body map[string]interface{}) *httptest.ResponseRecorder {
		blob, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/orders/x", bytes.NewReader(blob))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	// ① 确认支付 → 200，最新 order_pay detail 含单号＋金额分＋渠道
	rec := post(s.handleOrderPay, map[string]interface{}{"id": o.ID, "tenant_id": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("handleOrderPay 应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	detail, ok := latestAuditDetail(t, s, "order_pay")
	if !ok {
		t.Fatal("★ F-63：order_pay 没有留下审计行")
	}
	if !regexp.MustCompile(`订单 ` + regexp.QuoteMeta(o.OrderNo) + `｜金额 999分｜渠道 wechat`).MatchString(detail) {
		t.Fatalf("★ F-63：order_pay detail 口径不符（应含单号+金额分+渠道），实得 %q", detail)
	}
	// ② 退款 → 200，最新 order_refund detail 含单号＋「实退 N分」尾注（旧固定串「权益已回收」单独出现即红）
	rec = post(s.handleOrderRefund, map[string]interface{}{"id": o.ID, "tenant_id": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("handleOrderRefund 应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	detail, ok = latestAuditDetail(t, s, "order_refund")
	if !ok {
		t.Fatal("★ F-63：order_refund 没有留下审计行")
	}
	// 负向钉：旧固定串「权益已回收」单独出现＝detail 退化回「记了等于没记」
	if detail == "权益已回收" {
		t.Fatal("★ F-63 复现：order_refund detail 退化回固定串「权益已回收」")
	}
	if !regexp.MustCompile(`订单 ` + regexp.QuoteMeta(o.OrderNo) + `｜金额 \d+分｜渠道 wechat｜权益已回收｜实退 \d+分`).MatchString(detail) {
		t.Fatalf("★ F-63：order_refund detail 口径不符（应含单号+金额+渠道+实退分），实得 %q", detail)
	}
}
