// ============ ticket_list_page.go · 职责说明 ============
// store 包工单列表的**分页与批量进度**数据访问层（★ 2026-11-04 体验批 A5）。
//
// 这一族解决的是三件事，都来自用户报的「列表加载很慢」：
//
//	① 分页：`ListTickets` 原先固定 `ORDER BY id DESC LIMIT 200`（写死、无 offset），
//	   第 201 条以后的单在 UI 上**物理不存在**，而界面上没有任何一句话这么说；
//	   现收成常量对（缺省档＝200，与改造前逐字等值 ⇒ 老客户端零行为变化），
//	   并给 count 让 api 层能如实回 `total`/`has_more`。
//	② 大字段：列表 SELECT 仍带 `source_text`/`final_result` 两坨 TEXT——
//	   **本批刻意没动**（理由见 ticketListSelectCols 上方那段，是实读消费面之后定的）。
//	③ N+1：`handleTickets` 出参循环里对每张运行中单逐条查 `TicketStates`＋`TicketFiles`，
//	   10 张运行中单＝20 次往返；现给 `...ForIDs` 两个批量口，一次取完在内存里按单分组。
//
// 档位口径与 `store/notifications.go` 那对 `NotificationPageDefault/Max` 同形
// （AGENTS §一·5「列表类公开接口的分页与空值形态」）。
// =============================================
package store

import (
	"database/sql"

	"translator/internal/db"
)

// 工单列表档位（唯一写点）：
//   - 缺省档 200 与改造前那条写死的 `LIMIT 200` **逐字等值**，所以不带分页参数的老客户端
//     （前端 TicketsPage、SDK、监控脚本）看到的行集一字不变；
//   - 上限 500 是给"审批台/批量排障"留的口子，超过一律折回缺省档（不报错，脏参折档口径）。
const (
	TicketPageDefault = 200
	TicketPageMax     = 500
)

// EffectiveTicketPage 把脏 limit/offset 折回档位：非数字（api 层解出 0）／0／负数／超上限 ⇒ 缺省档；
// offset 负数 ⇒ 0。判据只在这里做一次，api 层不再各判一遍。
func EffectiveTicketPage(limit, offset int) (int, int) {
	if limit <= 0 || limit > TicketPageMax {
		limit = TicketPageDefault
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ticketListSelectCols 工单列表与审批台的**同一份**列清单（此前两处各抄一份长串，
// 加一列要记得改两处——那种"迟早漏一处"正是本仓反复踩的那一族）。
// ⚠️ 本批刻意**保留** `source_text`/`final_result` 两个大 TEXT：施工方案 A5-1 推荐"列表不取大字段"，
//
//	实读消费面后**判为不做**（不是漏做），三条证据：
//	① 前端 TicketsPage 的列表列直接渲染原文摘要（去掉后那一列变空＝把功能砍了，不是提速）；
//	② 审批台卡片同样读这两坨（ListPendingApproval 与列表共用本清单）；
//	③ 分页之后单页行数由常量封顶，"整表搬运"那一档本来就不存在。
//	⇒ 真正的慢来自**无索引排序**（idx_tickets_tenant_id／idx_tickets_status_id 已治）
//	与 **N+1**（本文件的批量口治）。要摘大字段必须同时改前端两处取数，另批做。
const ticketListSelectCols = "id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, COALESCE(reject_source,'') AS reject_source, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), COALESCE(max_length,0), COALESCE(delivery,'restore') AS delivery, COALESCE(text_result_path,'') AS text_result_path, COALESCE(quality_flagged,0) AS quality_flagged, COALESCE(qa_errors,0) AS qa_errors, COALESCE(qa_warnings,0) AS qa_warnings, COALESCE(degraded_langs,'') AS degraded_langs, COALESCE(gate_warnings,'') AS gate_warnings, created_at, updated_at"

// scanTicketRows 把一份结果集摊成 []*Ticket（列顺序必须与 ticketListSelectCols 逐字同序）。
// 单行 Scan 失败只跳过该行——与改造前同口径：整表不因一行坏数据而失败。
func scanTicketRows(rows *sql.Rows) []*Ticket {
	var out []*Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath,
			&t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.RejectSource,
			&t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.MaxLength, &t.Delivery,
			&t.TextResultPath, &t.QualityFlagged, &t.QAErrors, &t.QAWarnings, &t.DegradedLangsJSON,
			&t.GateWarningsJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &t)
	}
	return out
}

// ListTicketsPage 分页版工单列表。返回 (本页行, 同谓词下的总行数, 错误)。
// 参数：tid/userID/onlyMine 与 ListTickets 同义；limit/offset 在本函数内折档（调用方可直接透传脏值）。
// 两条纪律：
//
//	① `total` 必须与本页用**同一个谓词**（含 onlyMine），否则分页器显示的是"别人的总数"；
//	② `has_more` 由调用方按**本页行数**判，不许拿 total 算——count 与取页是两个时刻，
//	  中间有新单落库时 total 会偏大，拿它算页码会把"其实还有"读成"到底了"。
func (s *Store) ListTicketsPage(tid, userID int64, onlyMine bool, limit, offset int) ([]*Ticket, int, error) {
	limit, offset = EffectiveTicketPage(limit, offset)
	where := "WHERE tenant_id=?"
	args := []interface{}{tid}
	if onlyMine {
		where += " AND created_by=?"
		args = append(args, userID)
	}
	var total int
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM tickets "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := "SELECT " + ticketListSelectCols + " FROM tickets " + where + " ORDER BY id DESC LIMIT ? OFFSET ?"
	listArgs := append(append([]interface{}{}, args...), limit, offset)
	rows, err := db.Query(s.db, db.CurrentDialect(), q, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return scanTicketRows(rows), total, nil
}

// ListPendingApprovalPage 分页版审批台列表（与工单列表共用同一份列清单与同一把档位尺子）。
// 状态集合与改造前逐字一致（pending_approval/approved/rejected），本批只加档位与 offset。
func (s *Store) ListPendingApprovalPage(tid int64, limit, offset int) ([]*Ticket, int, error) {
	limit, offset = EffectiveTicketPage(limit, offset)
	const where = "WHERE tenant_id=? AND status IN ('pending_approval','approved','rejected')"
	var total int
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM tickets "+where, tid).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := "SELECT " + ticketListSelectCols + " FROM tickets " + where + " ORDER BY id DESC LIMIT ? OFFSET ?"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, append([]interface{}{tid}, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return scanTicketRows(rows), total, nil
}

// TicketStatesForIDs 批量取多张单的步进状态（★ 治 N+1：原先每张单一条查询）。
// 返回 map[ticketID][]*TicketState；组内顺序与 TicketStates 一致（ORDER BY ticket_id, version）。
// 空入参回**空 map**（不是 nil）——消费方按"这张单没有状态行"分支，不该被迫再判一次 nil。
func (s *Store) TicketStatesForIDs(ids []int64) (map[int64][]*TicketState, error) {
	out := map[int64][]*TicketState{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	q := "SELECT id, ticket_id, step, status, payload, version, updated_at FROM ticket_state WHERE ticket_id IN (" + placeholders(len(ids)) + ") ORDER BY ticket_id, version"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st TicketState
		if err := rows.Scan(&st.ID, &st.TicketID, &st.Step, &st.Status, &st.Payload, &st.Version, &st.UpdatedAt); err != nil {
			continue // 单行坏数据跳过（与 TicketStates 同口径）
		}
		out[st.TicketID] = append(out[st.TicketID], &st)
	}
	return out, nil
}

// TicketFilesForIDs 批量取多张单的文件行（与 TicketStatesForIDs 同一族、同一形状）。
func (s *Store) TicketFilesForIDs(ids []int64) (map[int64][]*TicketFile, error) {
	out := map[int64][]*TicketFile{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	q := "SELECT " + ticketFileCols + " FROM ticket_files WHERE ticket_id IN (" + placeholders(len(ids)) + ") ORDER BY ticket_id, id"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f TicketFile
		// Scan 顺序与 ticketFileCols 逐字同序（那张清单是 TicketFiles 的既有契约，这里必须薄引用它，
		// 不许再抄第二份列串——抄了就会漂移）
		if err := rows.Scan(&f.ID, &f.TenantID, &f.TicketID, &f.FileName, &f.FilePath,
			&f.ResultPath, &f.Error, &f.CreatedAt, &f.TextResultPath); err != nil {
			continue
		}
		out[f.TicketID] = append(out[f.TicketID], &f)
	}
	return out, nil
}
