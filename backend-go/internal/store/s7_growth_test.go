// S7 增长触达数据层单测：清零计时、挽回候选（48h+未付费+终身一次）、T+3 回访。
package store

import (
	"testing"
	"time"

	"translator/internal/db"
)

// TestS7RescueAndLapseFlow S7 观察闭环：救回与流失状态迁移、重复扫描幂等。
func TestS7RescueAndLapseFlow(t *testing.T) {
	s := newTestStoreWithTenants(t)
	// 1) 未清零不计时
	if z := s.S7MarkZero(1, false); z != "" {
		t.Fatal("非清零不应记录起点")
	}
	// 2) 清零 → 记录起点且幂等
	z := s.S7MarkZero(1, true)
	if z == "" {
		t.Fatal("清零应记录起点")
	}
	if z2 := s.S7MarkZero(1, true); z2 != z {
		t.Fatal("重复清零不应刷新起点")
	}
	// 3) 未满 48h 不是候选；回拨起点后是候选
	if got := s.S7RescueCandidates(48 * time.Hour); len(got) != 0 {
		t.Fatalf("48h 内不应有候选: %v", got)
	}
	db.Exec(s.db, db.CurrentDialect(), `UPDATE s7_watchlist SET zero_since=? WHERE tenant_id=1`,
		time.Now().UTC().Add(-49*time.Hour).Format(time.RFC3339))
	if got := s.S7RescueCandidates(48 * time.Hour); len(got) != 1 || got[0] != 1 {
		t.Fatalf("48h 后应为候选: %v", got)
	}
	// 4) 已付费租户剔除
	paid, err := s.CreatePackage(&Package{Code: "p1", Name: "包", PType: PackagePaid, Points: 100, PriceMoney: 9})
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.CreatePackageOrder(1, paid, 1, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOrderPaid(o.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := s.S7RescueCandidates(48 * time.Hour); len(got) != 0 {
		t.Fatalf("已付费租户不应进挽回候选: %v", got)
	}
	// 5) 发放一次后终身不再候选（清掉付费单回到"从未付费"态）
	db.Exec(s.db, db.CurrentDialect(), `DELETE FROM orders WHERE tenant_id=1`)
	db.Exec(s.db, db.CurrentDialect(), `UPDATE s7_watchlist SET zero_since=? WHERE tenant_id=1`,
		time.Now().UTC().Add(-49*time.Hour).Format(time.RFC3339))
	if got := s.S7RescueCandidates(48 * time.Hour); len(got) != 1 {
		t.Fatalf("重置已付费场景后应再次候选: %v", got)
	}
	s.S7MarkRescued(1)
	if got := s.S7RescueCandidates(48 * time.Hour); len(got) != 0 {
		t.Fatalf("已发挽回不应重复: %v", got)
	}
	// 6) 到期回访：登记→72h→候选→置位后不再
	s.S7MarkLapsed(1, time.Now().UTC())
	if got := s.S7LapsedFollowups(72 * time.Hour); len(got) != 0 {
		t.Fatal("刚到期不应回访")
	}
	db.Exec(s.db, db.CurrentDialect(), `UPDATE s7_watchlist SET lapsed_at=? WHERE tenant_id=1`,
		time.Now().UTC().Add(-73*time.Hour).Format(time.RFC3339))
	if got := s.S7LapsedFollowups(72 * time.Hour); len(got) != 1 {
		t.Fatalf("72h 后应回访: %v", got)
	}
	s.S7MarkLapsed3Sent(1)
	if got := s.S7LapsedFollowups(72 * time.Hour); len(got) != 0 {
		t.Fatalf("回访去重失效: %v", got)
	}
	// 7) 新一期到期复位标记
	s.S7MarkLapsed(1, time.Now().UTC())
	if got := s.S7LapsedFollowups(0); len(got) != 1 {
		t.Fatalf("新到期应重新进入回访窗口: %v", got)
	}
}
