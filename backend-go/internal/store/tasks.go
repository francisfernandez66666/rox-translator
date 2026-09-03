// ============ tasks.go · 职责说明 ============
// store 包「任务中心」数据层实现（功能③：个人中心 → 任务中心）。
// 规则（2026-09-03 新增）：
//   - user_tasks 任务定义表：task_type=daily(每日)/once(一次性)，reward_tokens=永久 token 奖励
//   - user_task_claims 领取记录表：daily 按「用户+任务+日期」去重，once 按「用户+任务」去重
//   - 奖励入永久余额（balance_accounts，与充值包同桶，永不过期），领取时事务内原子累加
//   - 超管后台可增删改任务与奖励，用户在个人中心查看并一键领取
//
// =============================================
package store

import (
	"time"

	"translator/internal/db"
)

// UserTask 任务中心任务定义。
type UserTask struct {
	ID           int64  `json:"id"`            // 任务 ID
	TaskType     string `json:"task_type"`     // daily=每日任务 / once=一次性任务
	Title        string `json:"title"`         // 任务标题
	Description  string `json:"description"`   // 任务说明（可空）
	RewardTokens int64  `json:"reward_tokens"` // 奖励 token 数（永久余额）
	Enabled      int    `json:"enabled"`       // 1=启用（用户可见可领取）0=停用（隐藏）
	SortOrder    int    `json:"sort_order"`    // 排序（越小越靠前）
	CreatedAt    string `json:"created_at"`    // 创建时间 RFC3339
	UpdatedAt    string `json:"updated_at"`    // 更新时间 RFC3339
}

// UserTaskClaim 用户领取记录。
type UserTaskClaim struct {
	ID           int64  `json:"id"`            // 流水 ID
	TaskID       int64  `json:"task_id"`       // 任务 ID
	UserID       int64  `json:"user_id"`       // 领取用户 ID
	TenantID     int64  `json:"tenant_id"`     // 领取用户所属租户（奖励入该租户永久余额）
	RewardTokens int64  `json:"reward_tokens"` // 本次领取奖励 token
	ClaimDate    string `json:"claim_date"`    // 领取日期 YYYY-MM-DD（daily 按此去重）
	CreatedAt    string `json:"created_at"`    // 领取时间 RFC3339
}

// UserTaskView 用户视角任务视图（含本人领取状态）。
type UserTaskView struct {
	UserTask
	Claimed   bool   `json:"claimed"`    // 是否已领取（daily=今日；once=曾领取）
	ClaimedAt string `json:"claimed_at"` // 最近领取时间（空=未领取）
}

// TasksMigrate 建表与索引（幂等，随 Store.New 迁移链调用）。
func (s *Store) TasksMigrate() {
	d := db.CurrentDialect()
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS user_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_type TEXT NOT NULL DEFAULT 'daily',
		title TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		reward_tokens INTEGER NOT NULL DEFAULT 0,
		enabled INTEGER NOT NULL DEFAULT 1,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL)`)
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS user_task_claims (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		reward_tokens INTEGER NOT NULL DEFAULT 0,
		claim_date TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL)`)
	// daily 去重：同用户同任务同日仅一次；once 去重：claim_date 固定存 once 标记（同用户同任务仅一次）
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_claim_user ON user_task_claims(user_id, task_id, claim_date)`)
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_task_enabled ON user_tasks(enabled, sort_order, id)`)
}

// ListUserTasks 列出全部任务定义（超管后台，含停用项，按 sort_order 排序）。
func (s *Store) ListUserTasks() []*UserTask {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, task_type, title, COALESCE(description,''), reward_tokens, enabled, sort_order, COALESCE(created_at,''), COALESCE(updated_at,'') FROM user_tasks ORDER BY sort_order, id")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*UserTask
	for rows.Next() {
		var t UserTask
		if err := rows.Scan(&t.ID, &t.TaskType, &t.Title, &t.Description, &t.RewardTokens, &t.Enabled, &t.SortOrder, &t.CreatedAt, &t.UpdatedAt); err == nil {
			out = append(out, &t)
		}
	}
	return out
}

// SaveUserTask 新增或更新任务定义（id=0 新增，>0 更新）。
func (s *Store) SaveUserTask(t *UserTask) (int64, error) {
	now := time.Now().Format(time.RFC3339)
	if t.TaskType != "once" {
		t.TaskType = "daily"
	}
	if t.RewardTokens < 0 {
		t.RewardTokens = 0
	}
	if t.SortOrder < 0 {
		t.SortOrder = 0
	}
	d := db.CurrentDialect()
	if t.ID > 0 {
		_, err := db.Exec(s.db, d,
			"UPDATE user_tasks SET task_type=?, title=?, description=?, reward_tokens=?, enabled=?, sort_order=?, updated_at=? WHERE id=?",
			t.TaskType, t.Title, t.Description, t.RewardTokens, t.Enabled, t.SortOrder, now, t.ID)
		return t.ID, err
	}
	res, err := db.Exec(s.db, d,
		"INSERT INTO user_tasks (task_type, title, description, reward_tokens, enabled, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)",
		t.TaskType, t.Title, t.Description, t.RewardTokens, t.Enabled, t.SortOrder, now, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// DeleteUserTask 删除任务定义（同时清理其领取记录）。
func (s *Store) DeleteUserTask(id int64) error {
	d := db.CurrentDialect()
	if _, err := db.Exec(s.db, d, "DELETE FROM user_task_claims WHERE task_id=?", id); err != nil {
		return err
	}
	_, err := db.Exec(s.db, d, "DELETE FROM user_tasks WHERE id=?", id)
	return err
}

// ListUserTaskViews 用户视角任务列表：启用任务 + 本人领取状态。
// daily：今日已领置 claimed；once：曾领取置 claimed。
func (s *Store) ListUserTaskViews(uid int64) []*UserTaskView {
	tasks := s.ListUserTasks()
	if len(tasks) == 0 {
		return nil
	}
	today := time.Now().Format("2006-01-02")
	var out []*UserTaskView
	for _, t := range tasks {
		if t.Enabled != 1 {
			continue // 停用任务用户不可见
		}
		v := &UserTaskView{UserTask: *t}
		// 查询领取状态：daily 查今日，once 查 once 标记
		var claimedAt string
		_ = db.QueryRow(s.db, db.CurrentDialect(),
			"SELECT COALESCE(created_at,'') FROM user_task_claims WHERE user_id=? AND task_id=? AND claim_date=? ORDER BY id DESC LIMIT 1",
			uid, t.ID, t.claimDateKey(today)).Scan(&claimedAt)
		v.Claimed = claimedAt != ""
		v.ClaimedAt = claimedAt
		out = append(out, v)
	}
	return out
}

// claimDateKey 领取去重键：daily=日期，once=固定 once 标记。
func (t *UserTask) claimDateKey(today string) string {
	if t.TaskType == "once" {
		return "once"
	}
	return today
}

// ClaimUserTask 用户领取任务奖励（事务原子：去重校验 + 永久余额累加 + 领取流水）。
// 返回：ok=是否领取成功（false=已领过/停用/奖励为 0），tokens=实际发放 token 数。
func (s *Store) ClaimUserTask(uid, tid, taskID int64) (ok bool, tokens int64) {
	d := db.CurrentDialect()
	now := time.Now().Format(time.RFC3339)
	today := time.Now().Format("2006-01-02")
	tx, err := s.db.Begin()
	if err != nil {
		return false, 0
	}
	defer tx.Rollback()
	// 读取任务定义并校验启用
	var t UserTask
	if err := db.QueryRow(tx, d, "SELECT id, task_type, title, COALESCE(description,''), reward_tokens, enabled, sort_order, COALESCE(created_at,''), COALESCE(updated_at,'') FROM user_tasks WHERE id=?", taskID).
		Scan(&t.ID, &t.TaskType, &t.Title, &t.Description, &t.RewardTokens, &t.Enabled, &t.SortOrder, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return false, 0
	}
	if t.Enabled != 1 || t.RewardTokens <= 0 {
		return false, 0
	}
	key := t.claimDateKey(today)
	// 去重：同用户同任务同 key 已存在则跳过
	var exists int
	_ = db.QueryRow(tx, d, "SELECT COUNT(*) FROM user_task_claims WHERE user_id=? AND task_id=? AND claim_date=?", uid, taskID, key).Scan(&exists)
	if exists > 0 {
		return false, 0
	}
	// 领取用户租户（奖励入其租户永久余额）
	var tenantID int64
	_ = db.QueryRow(tx, d, "SELECT COALESCE(tenant_id,0) FROM users WHERE id=?", uid).Scan(&tenantID)
	// 事务内确保余额账户行存在后原子累加（与 GrantKBRewardByChars 同款并发安全）
	if _, err := db.Exec(tx, d, "INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tenantID, now); err != nil {
		return false, 0
	}
	if _, err := db.Exec(tx, d, "UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
		t.RewardTokens, now, tenantID); err != nil {
		return false, 0
	}
	// 领取流水（防刷留痕与审计）
	if _, err := db.Exec(tx, d, "INSERT INTO user_task_claims (task_id, user_id, tenant_id, reward_tokens, claim_date, created_at) VALUES (?,?,?,?,?,?)",
		taskID, uid, tenantID, t.RewardTokens, key, now); err != nil {
		return false, 0
	}
	if err := tx.Commit(); err != nil {
		return false, 0
	}
	return true, t.RewardTokens
}
