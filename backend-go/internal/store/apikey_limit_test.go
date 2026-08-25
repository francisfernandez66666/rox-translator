// ============ 本文件职责中文说明 ============
// R4 Key 级配额单元测试：带上限签发、今日计数跨日清零、限额调整。
package store

import (
	"testing"
	"time"
)

// TestAPIKeyDailyLimitFlow 带每日上限签发 → 触摸计数跨日归集 → 调整限额。
func TestAPIKeyDailyLimitFlow(t *testing.T) {
	s := newTestStoreWithTenants(t)
	plain, err := s.CreateAPIKey(1, 0, "配额测试", "translate", 3)
	if err != nil {
		t.Fatal(err)
	}
	if plain[:3] != "rk_" {
		t.Fatalf("明文 Key 应以 rk_ 开头")
	}
	k, err := s.GetAPIKeyByHash(HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}
	if k.DailyCallLimit != 3 {
		t.Fatalf("每日上限应为 3，实得 %d", k.DailyCallLimit)
	}
	// 模拟两次调用（同日累计）
	for i := 0; i < 2; i++ {
		s.TouchAPIKey(k.ID)
	}
	k2, _ := s.GetAPIKeyByHash(HashAPIKey(plain))
	today := time.Now().Format("2006-01-02")
	if k2.CallsTodayDate != today || k2.CallsToday != 2 {
		t.Fatalf("今日计数应为 2（%s），实得 %d/%s", today, k2.CallsToday, k2.CallsTodayDate)
	}
	// 模拟跨日：直接把日期改写为昨天再触摸 → 计数应重置为 1
	if _, err := s.db.Exec("UPDATE api_keys SET calls_today_date='2000-01-01' WHERE id=?", k.ID); err != nil {
		t.Fatal(err)
	}
	s.TouchAPIKey(k.ID)
	k3, _ := s.GetAPIKeyByHash(HashAPIKey(plain))
	if k3.CallsToday != 1 {
		t.Fatalf("跨日后计数应重置为 1，实得 %d", k3.CallsToday)
	}
	// 调整限额为 10
	if err := s.SetAPIKeyDailyLimit(k.ID, 1, 10); err != nil {
		t.Fatal(err)
	}
	k4, _ := s.GetAPIKeyByHash(HashAPIKey(plain))
	if k4.DailyCallLimit != 10 {
		t.Fatalf("限额调整未生效")
	}
}
