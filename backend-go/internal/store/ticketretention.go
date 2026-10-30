// ============ ticketretention.go · 职责说明 ============
// store 包工单产物保留期（tickets 表 result_expires_at / expire_notify 列）数据访问层。
//   - 完成时打点到期时间（默认完成 +14 天，ticket_retention_days 可配置，0=永久）
//   - 每日扫描：剩余 ≤7/3/1 天分档提醒创建者下载（expire_notify 逗号标记去重）
//   - 到期清理：删除产物文件（主产物 + 多文件表各产物），清空路径字段；
//     核心译文不受影响——文本工单在 final_result、文件工单已回写 tm_segments 长期保留
//
// =============================================
package store

import (
	"strings"
	"time"
	"translator/internal/db"
)

// SetTicketExpiry 打点工单产物到期时间。
func (s *Store) SetTicketExpiry(id int64, expiresAt string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE tickets SET result_expires_at=? WHERE id=?", expiresAt, id)
	return err
}

// TicketRetentionRow 留存扫描所需的最小字段集。
type TicketRetentionRow struct {
	ID              int64  // 工单 ID
	TenantID        int64  // 租户 ID
	CreatedBy       int64  // 创建者（提醒接收人）
	TicketNo        string // 工单号
	Title           string // 标题
	ResultPath      string // 主产物路径（多文件工单为空）
	ResultExpiresAt string // 到期时间 RFC3339
	ExpireNotify    string // 已发送档位标记（逗号分隔："7,3,1"）
}

// retentionCols 工单留存扫描通用查询列清单（Scan 顺序契约；result_path/expires_at/expire_notify 为可空列，COALESCE 兜底）。
const retentionCols = "id, tenant_id, created_by, ticket_no, title, COALESCE(result_path,''), COALESCE(result_expires_at,''), COALESCE(expire_notify,'')"

// scanRetentionRows 通用查询：completed 且设置了到期时间的工单。
func (s *Store) scanRetentionRows(where string) ([]*TicketRetentionRow, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+retentionCols+" FROM tickets WHERE status='completed' AND result_expires_at != '' "+where+" ORDER BY result_expires_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TicketRetentionRow
	for rows.Next() {
		var r TicketRetentionRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.CreatedBy, &r.TicketNo, &r.Title, &r.ResultPath, &r.ResultExpiresAt, &r.ExpireNotify); err != nil {
			continue
		}
		out = append(out, &r)
	}
	return out, nil
}

// ListTicketsForRetention 全部待巡检工单（每日扫描入口）。
func (s *Store) ListTicketsForRetention() ([]*TicketRetentionRow, error) {
	return s.scanRetentionRows("")
}

// TicketExpireMarked 判断某提醒档位是否已发送过。
func (s *Store) TicketExpireMarked(id int64, tier string) bool {
	var m string
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(expire_notify,'') FROM tickets WHERE id=?", id).Scan(&m)
	for _, t := range strings.Split(m, ",") {
		if strings.TrimSpace(t) == tier {
			return true
		}
	}
	return false
}

// MarkTicketExpireNotify 追加提醒档位标记（幂等）。
func (s *Store) MarkTicketExpireNotify(id int64, tier string) error {
	var m string
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(expire_notify,'') FROM tickets WHERE id=?", id).Scan(&m)
	for _, t := range strings.Split(m, ",") {
		if strings.TrimSpace(t) == tier {
			return nil // 已标记
		}
	}
	newVal := strings.TrimSpace(m)
	if newVal == "" {
		newVal = tier
	} else {
		newVal += "," + tier
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE tickets SET expire_notify=?, updated_at=? WHERE id=?", newVal, time.Now().Format(time.RFC3339), id)
	return err
}

// CleanupTicketResults 清理工单全部产物：返回应删除的磁盘文件路径列表，
// 并删除 ticket_artifacts 行、清空 tickets.result_path / text_result_path /
// result_expires_at 与 ticket_files.result_path / text_result_path（核心译文不动）。
//
// ★ 2026-10-30 组5（W25/R-3）两条补全：
//  1. **返回集必须并入 ticket_artifacts.path**——两段式之后「工单交付过什么」的唯一事实源是那张表，
//     只清 result_path 会把 md/原格式产物留在磁盘上、行却被删掉＝永久孤儿（谁的清理都扫不到）。
//     行的删除紧随路径收集之后（顺序不能反：先删行就再也拿不到路径了）。
//  2. **text_result_path 一并清**（tickets 与 ticket_files 两处）——两段式之前这两列就有，
//     但清理一直没管：保留期到了以后「纯文案旁路」的路径还挂在详情面上，点开是 404，
//     而磁盘上那份文件永远没人删。这是一个老缺口，与本批新表同一族（有路径列就必须进清理射程）。
func (s *Store) CleanupTicketResults(id int64) ([]string, error) {
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	var mainPath, mainTextPath string
	_ = db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(result_path,''), COALESCE(text_result_path,'') FROM tickets WHERE id=?", id).
		Scan(&mainPath, &mainTextPath)
	add(mainPath)
	add(mainTextPath)
	tfs, err := s.TicketFiles(id)
	if err == nil {
		for _, f := range tfs {
			add(f.ResultPath)
			add(f.TextResultPath)
		}
		if _, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE ticket_files SET result_path='', text_result_path='', error=CASE WHEN error='' THEN '产物已过保留期清理' ELSE error END WHERE ticket_id=?", id); err != nil {
			return paths, err
		}
	}
	// 产物行：先收集路径（上方已并入），再删行。删行失败就返回错误并保留行——
	// 「行还在、文件没了」下次扫描还能重试；反过来「行没了、文件还在」是永久孤儿。
	artifactPaths, aerr := s.TicketArtifactPaths(id)
	if aerr != nil {
		return paths, aerr
	}
	for _, p := range artifactPaths {
		add(p)
	}
	if derr := s.DeleteTicketArtifactsForTicket(id); derr != nil {
		return paths, derr
	}
	now := time.Now().Format(time.RFC3339)
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tickets SET result_path='', text_result_path='', result_expires_at='', updated_at=? WHERE id=?", now, id); err != nil {
		return paths, err
	}
	return paths, nil
}
