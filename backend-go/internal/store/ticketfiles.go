// ============ ticketfiles.go · 职责说明 ============
// store 包工单多文件（ticket_files 表）数据访问层。
// 一个文件工单可挂多个源文件，
// 每个文件独立记录产物路径与失败原因；下载时按文件打包或单独取回。
// 兼容性：旧工单无 ticket_files 行，走 tickets.file_path 单文件路径。
// =============================================
package store

import (
	"database/sql"
	"time"
	"translator/internal/db"
)

// TicketFile 工单源文件
type TicketFile struct {
	ID         int64  `json:"id"`          // 主键 ID
	TenantID   int64  `json:"tenant_id"`   // 所属租户 ID
	TicketID   int64  `json:"ticket_id"`   // 关联工单 ID
	FileName   string `json:"file_name"`   // 原始文件名
	FilePath   string `json:"file_path"`   // 上传保存路径
	ResultPath string `json:"result_path"` // 翻译产物路径（空=未生成）
	Error      string `json:"error"`       // 单文件处理失败原因（空=成功/未处理）
	CreatedAt  string `json:"created_at"`
}

// ticketFileCols 工单源文件表通用查询列清单（Scan 顺序契约；result_path/error 为可空列，COALESCE 兜底）。
const ticketFileCols = "id, tenant_id, ticket_id, file_name, file_path, COALESCE(result_path,''), COALESCE(error,''), created_at"

// AddTicketFile 为工单登记一个源文件。
func (s *Store) AddTicketFile(tf *TicketFile) (*TicketFile, error) {
	tf.CreatedAt = time.Now().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO ticket_files (tenant_id, ticket_id, file_name, file_path, created_at) VALUES (?,?,?,?,?)",
		tf.TenantID, tf.TicketID, tf.FileName, tf.FilePath, tf.CreatedAt)
	if err != nil {
		return nil, err
	}
	tf.ID = id
	return tf, nil
}

// TicketFiles 列出工单的全部源文件（按 ID 升序）。
func (s *Store) TicketFiles(ticketID int64) ([]*TicketFile, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+ticketFileCols+" FROM ticket_files WHERE ticket_id=? ORDER BY id", ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TicketFile
	for rows.Next() {
		var f TicketFile
		if err := rows.Scan(&f.ID, &f.TenantID, &f.TicketID, &f.FileName, &f.FilePath, &f.ResultPath, &f.Error, &f.CreatedAt); err != nil {
			continue
		}
		out = append(out, &f)
	}
	return out, nil
}

// GetTicketFile 按 ID + 租户取单个文件行（强制租户隔离，杜绝跨租户读取上传/产物路径）。
func (s *Store) GetTicketFile(id, tenantID int64) (*TicketFile, error) {
	var f TicketFile
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+ticketFileCols+" FROM ticket_files WHERE id=? AND tenant_id=?", id, tenantID).
		Scan(&f.ID, &f.TenantID, &f.TicketID, &f.FileName, &f.FilePath, &f.ResultPath, &f.Error, &f.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// SetTicketFileResult 写入单个文件的产物路径（error 清空）。
func (s *Store) SetTicketFileResult(id int64, resultPath string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE ticket_files SET result_path=?, error='' WHERE id=?", resultPath, id)
	return err
}

// SetTicketFileError 写入单个文件的失败原因。
func (s *Store) SetTicketFileError(id int64, errMsg string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE ticket_files SET error=? WHERE id=?", errMsg)
	return err
}

// CountTicketFiles 统计工单挂载的文件数（判断是否多文件工单）。
func (s *Store) CountTicketFiles(ticketID int64) (int64, error) {
	var n int64
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM ticket_files WHERE ticket_id=?", ticketID).Scan(&n)
	return n, err
}
