// ============ notifications.go · 职责说明 ============
// store 包站内信（notifications 表）数据访问层。
// 通用通知中心的存储层。
// 投递源：工单完成/失败（本期）；告警等事件后续统一接入。
// 读取按用户隔离；未读数供前台铃铛轮询。
// =============================================
package store

import (
	"time"
	"translator/internal/db"
)

// Notification 站内信实体。
type Notification struct {
	ID        int64  `json:"id"`         // 主键 ID
	UserID    int64  `json:"user_id"`    // 接收用户 ID
	Title     string `json:"title"`      // 标题
	Body      string `json:"body"`       // 正文
	RefType   string `json:"ref_type"`   // 关联类型（ticket/alert...）
	RefID     int64  `json:"ref_id"`     // 关联 ID（如工单 ID）
	ReadAt    string `json:"read_at"`    // 已读时间（空=未读）
	CreatedAt string `json:"created_at"` // 创建时间（RFC3339）
}

// CreateNotification 写入一条站内信。
func (s *Store) CreateNotification(userID int64, title, body, refType string, refID int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO notifications (user_id, title, body, ref_type, ref_id, read_at, created_at) VALUES (?,?,?,?,?,'',?)",
		userID, title, body, refType, refID, time.Now().Format(time.RFC3339))
	return err
}

// ListNotifications 列出某用户的站内信（最新在前，最多 100 条）。
func (s *Store) ListNotifications(userID int64) ([]*Notification, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, user_id, title, body, ref_type, ref_id, read_at, created_at FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT 100", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Title, &n.Body, &n.RefType, &n.RefID, &n.ReadAt, &n.CreatedAt); err == nil {
			out = append(out, &n)
		}
	}
	return out, nil
}

// UnreadCount 未读站内信数量。
func (s *Store) UnreadCount(userID int64) (int64, error) {
	var n int64
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM notifications WHERE user_id=? AND read_at=''", userID).Scan(&n)
	return n, err
}

// MarkNotificationRead 标记单条已读（校验归属）。
func (s *Store) MarkNotificationRead(id, userID int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE notifications SET read_at=? WHERE id=? AND user_id=?", time.Now().Format(time.RFC3339), id, userID)
	return err
}

// MarkAllNotificationsRead 全部标记已读。
func (s *Store) MarkAllNotificationsRead(userID int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE notifications SET read_at=? WHERE user_id=? AND read_at=''", time.Now().Format(time.RFC3339), userID)
	return err
}
