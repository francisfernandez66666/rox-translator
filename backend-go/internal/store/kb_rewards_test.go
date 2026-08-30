// ============ kb_rewards_test.go · 职责说明 ============
// 知识库上传奖励（任务2.3）数据层单元测试：
//   - TestGrantKBRewardPermanent：奖励入永久余额 + 流水落表
//   - TestGrantKBRewardDailyCap：单租户日封顶（超限跳过）
// 环境：临时文件库（kb.Open + store.New 共享连接，DSN 自带 IMMEDIATE/WAL）。
// ========================================
package store

import (
	"testing"
)

// grantRewardEnv 建立一个带租户行的存储环境。
func grantRewardEnv(t *testing.T) *Store {
	t.Helper()
	st, _ := newKBEnv(t)
	if _, err := st.db.Exec("INSERT OR IGNORE INTO tenants (id, code, name, status) VALUES (7,'kbr7','奖励测试租户','active')"); err != nil {
		t.Fatalf("种子租户失败: %v", err)
	}
	return st
}

// TestGrantKBRewardPermanent 奖励入永久余额且流水落表。
func TestGrantKBRewardPermanent(t *testing.T) {
	st := grantRewardEnv(t)
	// 默认每条约额 200（EnsureBillingDefaults 已在 New 落库默认值）
	granted, tokens, used := st.GrantKBReward(7, 1, 0, 3)
	if !granted {
		t.Fatalf("应发放奖励")
	}
	if tokens != 600 {
		t.Fatalf("3 条 × 200 = 600 token，实际 %d", tokens)
	}
	if used != 600 {
		t.Fatalf("发放后当日累计应为 600，实际 %d", used)
	}
	// 永久余额到账
	bal, err := st.GetBalance(7)
	if err != nil || bal.Balance != 600 {
		t.Fatalf("永久余额应为 600，实际 %d (err=%v)", bal.Balance, err)
	}
	// 流水落表一条
	var cnt int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM kb_upload_rewards WHERE tenant_id=7").Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("流水应有 1 条，实际 %d (err=%v)", cnt, err)
	}
}

// TestGrantKBRewardDailyCap 单租户日封顶：累计超过 cap 后续导入不发。
func TestGrantKBRewardDailyCap(t *testing.T) {
	st := grantRewardEnv(t)
	// 压低日封顶：每条约额 200，cap 500 —— 累计 600 触发封顶
	if err := st.SetConfig("kb_upload_reward_daily_cap", "500"); err != nil {
		t.Fatalf("设置日封顶失败: %v", err)
	}
	// 第一笔 3 条 = 600 > 500 → 直接超限（entire batch denied）
	granted, _, used := st.GrantKBReward(7, 1, 0, 3)
	if granted || used != 0 {
		t.Fatalf("首笔即超日封顶应拒发（used=%d）", used)
	}
	// 第二笔 2 条 = 400 ≤ 500 → 发放
	granted, tokens, used := st.GrantKBReward(7, 1, 0, 2)
	if !granted || tokens != 400 || used != 400 {
		t.Fatalf("2条应发放 400，实际 granted=%v tokens=%d used=%d", granted, tokens, used)
	}
	// 第三笔 1 条 = 200 → 400+200=600 > 500 → 拒发
	granted, _, used = st.GrantKBReward(7, 1, 0, 1)
	if granted || used != 400 {
		t.Fatalf("超过日封顶应拒发（used=%d）", used)
	}
}