// ============ 本文件职责中文说明 ============
// 四期 W1 收尾：确认动作不得把事实锁出注入集。
//
// 旧实现 ConfirmFact 写 `SET status=?, locked=1`，而事实集准入读口
// ListSalesFactsForInjection 要求 locked=0 ⇒ 卖家一点「确认」，这条事实
// 反而永远不再对外下发（界面零症状）。本锁走真 SQLite + 真译文夹具，
// 证「确认之后这条事实仍带值进集」，并配 M0319 把 locked=1 加回去反证。
// ================================================
package store

import "testing"

// TestConfirmFactKeepsFactEligibleForInjection 确认只改 status，不改 locked。
func TestConfirmFactKeepsFactEligibleForInjection(t *testing.T) {
	st, conn := newFactI18nStore(t)
	// 人工录入、公开、未锁定，但尚未确认；译文先按原文 hash 预生成好。
	id := insertSalesFact(t, conn, 7, "P1", "price", "retail_price", "12 元", "unverified", "public", 0, 5)
	insertFactI18n(t, conn, 7, id, "en", "12 CNY", factSourceHash("12 元"))

	// 确认前不应带值进集（准入要 confirmed）。
	before, _, err := st.ListSalesFactsForInjection(7, "", "en")
	if err != nil {
		t.Fatalf("确认前读事实集失败: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("未确认的事实不该带值进集，实得 %d 条", len(before))
	}

	if err := st.ConfirmFact(7, id, 1); err != nil {
		t.Fatalf("确认事实失败: %v", err)
	}

	// 确认后必须仍可注入：locked 若被写成 1，这里就会是 0 条。
	facts, _, err := st.ListSalesFactsForInjection(7, "", "en")
	if err != nil {
		t.Fatalf("确认后读事实集失败: %v", err)
	}
	if len(facts) != 1 || facts[0].Value != "12 CNY" {
		t.Fatalf("确认过的事实必须仍可注入（确认≠锁定），实得 %+v", facts)
	}
}
