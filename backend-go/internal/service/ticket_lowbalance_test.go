// ============================================================================
// ticket_lowbalance_test.go — 低额告警阈值配置接线回归（★ 缺陷核实修复 D2 · 2026-09-16）
// 职责：验证 TicketService.lowBalanceThreshold 实读 system_config
//
//	low_balance_alert_tokens——旧实现硬编码 100000 与注释不符（超管改配置不生效）。
//
// 断言：① 配置正常值按配置返回；② 缺失/空/非法/非正数一律回退默认 100000；
//
//	③ Store 为 nil 的防御路径不 panic。
//
// ============================================================================
package service

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/store"
)

func newLowBalanceSvc(t *testing.T) *TicketService {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	return &TicketService{Store: st}
}

func TestLowBalanceThresholdWired(t *testing.T) {
	s := newLowBalanceSvc(t)
	// ① 配置生效：不再是硬编码
	if err := s.Store.SetConfig("low_balance_alert_tokens", "5000"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	if got := s.lowBalanceThreshold(); got != 5000 {
		t.Fatalf("配置 5000 应返回 5000，实得 %d", got)
	}
	// ② 非法/非正数/空值一律回退默认 100000
	for _, bad := range []string{"abc", "-1", "0", ""} {
		if err := s.Store.SetConfig("low_balance_alert_tokens", bad); err != nil {
			t.Fatalf("写配置 %q 失败: %v", bad, err)
		}
		if got := s.lowBalanceThreshold(); got != 100000 {
			t.Fatalf("非法值 %q 应回退 100000，实得 %d", bad, got)
		}
	}
	// ③ Store 为 nil：防御路径不 panic，返回默认
	nilSvc := &TicketService{}
	if got := nilSvc.lowBalanceThreshold(); got != 100000 {
		t.Fatalf("Store=nil 应返回默认 100000，实得 %d", got)
	}
}
