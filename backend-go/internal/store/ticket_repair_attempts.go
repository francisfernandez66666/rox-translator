// ============ ticket_repair_attempts.go · 职责说明 ============
// store 包「文本工单整单重发计数」窄域文件（★ 2026-10-24 云端 UAT 修复批 DEF-02，§10.1.2）。
//
// 方案点名的是**列**而不是载荷字段：`tickets.repair_attempts INTEGER NOT NULL DEFAULT 0`。
// 本批对账时发现实现把计数写进了 `final_result` JSON（载荷里的 structure_repair_attempts），
// 行为上一样能限住 3 次，但代价是运维面看不见——现网问「这一单被自动重发了几次」时，
// 只能整段拉出 final_result 再解析 JSON，而 SQL 里一句 `WHERE repair_attempts>=3` 就够；
// 更要紧的是计数与译文挤在同一段 blob 里：任何一次「整段重写载荷」的路径（人工重跑、
// 驳回重翻、后续补译）都可能顺手把它抹回 0，于是上限变软、坏单无限重发真扣钱。
// ⇒ 计数落列，**列是唯一事实源**（载荷里那份字段随之删除，不留第二把尺子）。
//
// 幂等：走 db.EnsureColumns（AGENTS §一·4：迁移唯一入口），老库启动自动补 0，
// 新库随 tickets 建表就有；本文件不做一次性 ALTER。
// 方言：只写 UPDATE/SELECT，不带尾随 LIMIT（AGENTS §一·4），SQLite 与 PG 同一条 SQL。
// ============================================================
package store

import (
	"time"

	"translator/internal/db"
)

// TicketRepairAttemptsMigrate tickets 补 repair_attempts 列（幂等；DEF-02 §10.1.2）。
func (s *Store) TicketRepairAttemptsMigrate() {
	_ = db.EnsureColumns(s.db, db.CurrentDialect(), "tickets", map[string]string{
		"repair_attempts": "INTEGER NOT NULL DEFAULT 0",
	})
}

// TicketRepairAttempts 读某一单已自动整单重发的次数（缺行/读失败回 0＋错误上抛）。
// 参数：id=工单 ID。
func (s *Store) TicketRepairAttempts(id int64) (int, error) {
	var n int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT repair_attempts FROM tickets WHERE id=?", id).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// IncTicketRepairAttempts 计数 +1 并返回**加完之后**的读数（DEF-02 判定放行重发时调用）。
// 先 UPDATE 再 SELECT：两种方言都认 `UPDATE … RETURNING` 的时机不同（SQLite 需编译选项），
// 而这里的正确性要求只有「同一次重发只加一格」——UPDATE 走的是**服务端算术**
// （`repair_attempts=repair_attempts+1`，不是 Go 侧读-改-写），并发各加一格不会吞。
// ⚠️ 返回的那个读数**不保证唯一**：紧跟 UPDATE 的回读与别人的 UPDATE 之间没有锁，
// 两枪完全可能读到同一个中间值。它只作"这一发真记上了"的日志/返回值，
// **上限判定必须走下一次现读**（TicketRepairAttempts）——调用方 StructureRetryNeeded 正是这个顺序。
// updated_at 与 RequeueTicketForRepair 同口径刷新，避免"重发了但工单看起来没动过"。
func (s *Store) IncTicketRepairAttempts(id int64) (int, error) {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tickets SET repair_attempts=repair_attempts+1, updated_at=? WHERE id=?",
		time.Now().Format(time.RFC3339), id)
	if err != nil {
		return 0, err
	}
	return s.TicketRepairAttempts(id)
}
