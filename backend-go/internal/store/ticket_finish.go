// ============ ticket_finish.go · 职责说明 ============
// store 包工单收尾原子写入（★ #40② 2026-09-21 评审缺陷）。
//
// 旧路径把一次收尾拆成三条各自 `_ =` 忽略错误的写：UpdateTicket（状态/计费回填）、
// SetTicketExpiry（产物到期打点）、CreateNotification（站内信）。任一条失败都会留下
// 「半收尾」状态——工单显示已完成却无到期时间（留存扫描永远漏掉它）、或已完成却没通知，
// 而积分已在实时计量钩子里真扣走，事后无法从库内状态复原。
//
// 本文件把三者收进一个事务，并把「先查后改」的收尾守卫（service/ticket.go R3）下沉为
// 条件 UPDATE：多副本接管（queued）/ 用户取消（cancelled）时原子放弃，杜绝 TOCTOU 窗口。
// =============================================
package store

import (
	"fmt"
	"strings"
	"time"
	"translator/internal/db"
)

// TicketFinishInput 工单收尾事务入参。
type TicketFinishInput struct {
	// Ticket 已按目标终态回填的工单（Status=completed/rejected，含 TokensBilled/RejectReason 等）。
	Ticket *Ticket
	// ExpiresAt 产物到期时间（RFC3339）；空串=不更新 result_expires_at（永久保留口径）。
	ExpiresAt string
	// Notify 完成/失败站内信；nil=本次不发。
	Notify *Notification
	// ExcludeStatuses 状态守卫：工单当前状态命中任一值即整体放弃（0 行更新 → applied=false）。
	// 由调用方按语义给出，如完成路径传 cancelled/queued、失败路径传 cancelled。
	ExcludeStatuses []string
}

// FinishTicket 在单个事务内完成工单收尾（状态落库 + 到期打点 + 站内信）。
// 返回 applied=false 表示状态守卫命中（已被取消或被其他副本接管），本次未写任何一行。
// 参数 in: 收尾入参（Ticket 必填）。返回 applied: 是否真正写入；err: 事务执行错误。
func (s *Store) FinishTicket(in *TicketFinishInput) (bool, error) {
	if in == nil || in.Ticket == nil {
		return false, fmt.Errorf("FinishTicket: 入参或 Ticket 为空")
	}
	t := in.Ticket
	d := db.CurrentDialect()
	args := []interface{}{
		t.Title, t.Status, t.TargetLangs, t.ApproverID, t.ReviewerID, t.RejectReason,
		t.FinalResult, t.Mode, t.TokensBilled, t.MaxLength, t.Delivery,
		time.Now().Format(time.RFC3339),
	}
	set := "SET title=?, status=?, target_langs=?, approver_id=?, reviewer_id=?, reject_reason=?, final_result=?, mode=?, tokens_billed=?, max_length=?, delivery=?, updated_at=?"
	// 到期打点并入同一条 UPDATE（旧实现是第二条写，失败即留下无到期时间的已完成工单）
	if in.ExpiresAt != "" {
		set += ", result_expires_at=?"
		args = append(args, in.ExpiresAt)
	}
	where := "WHERE id=? AND tenant_id=?"
	args = append(args, t.ID, t.TenantID)
	if len(in.ExcludeStatuses) > 0 {
		holders := make([]string, 0, len(in.ExcludeStatuses))
		for _, st := range in.ExcludeStatuses {
			holders = append(holders, "?")
			args = append(args, st)
		}
		where += " AND status NOT IN (" + strings.Join(holders, ",") + ")"
	}

	tx, err := s.db.Begin() // SQLite DSN 带 _txlock=immediate；PG 下由行锁承担
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := db.Exec(tx, d, "UPDATE tickets "+set+" "+where, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		// 守卫命中：既不落状态也不发通知，调用方据此放弃后续副作用（webhook 等）
		return false, nil
	}
	if in.Notify != nil {
		if _, err := db.Exec(tx, d,
			"INSERT INTO notifications (user_id, title, body, ref_type, ref_id, read_at, created_at) VALUES (?,?,?,?,?,'',?)",
			in.Notify.UserID, in.Notify.Title, in.Notify.Body, in.Notify.RefType, in.Notify.RefID,
			time.Now().Format(time.RFC3339)); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
