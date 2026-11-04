// ============ notifications_batch.go · 职责说明 ============
// 站内信**批量写入**（★ 2026-11-04 体验批 §2.12 A12）。
//
// 改造前 api/notify_recipients.go 的平台超管投递是「逐收件人各打一次 CreateNotification」，
// N 个超管＝N 次写往返，跑在与客户请求同一个进程里。本文件把那一圈收成**一次多值 INSERT**。
// ⚠️ 判定口径不动：收件人名单由 api 侧 superAdminRecipients() 圈定、谁该收／ref_type／n
// （成功送达人数）全在调用方按原样算，本方法只负责「把这一批一次写进去」并回报落库条数。
//
// 双方言（AGENTS §一·4）：SQL 以 SQLite 的 `?` 占位符为唯一真源，PostgreSQL 由 db.Exec
// 的占位符改写统一翻译；不在本方法里做方言分支。空入参**提前返回、不发 SQL**，
// 与逐条版「空名单一次都不写」等值（等值锁见 notifications_batch_test.go）。
// =============================================
package store

import (
	"strings"
	"time"

	"translator/internal/db"
)

// NotificationDraft 一条待投递站内信：收件人 ID ＋ 四个业务字段（read_at 恒为空串＝未读）。
// 字段与 CreateNotification 的入参一一对应，避免调用方再造一份「批量专用」的字段口径。
type NotificationDraft struct {
	UserID  int64  // 接收用户 ID
	Title   string // 标题
	Body    string // 正文
	RefType string // 关联类型（取自 NotifyRef*，名单外的值照投不拦）
	RefID   int64  // 关联 ID
}

// CreateNotificationsBatch 一次 INSERT 写入多条站内信，返回成功落库条数。
// 空 items 直接返回 (0, nil) 且**不发 SQL**；全批共用一个 created_at 时刻
// （与 CreateNotification 的 time.Now().Format(time.RFC3339) 同一口径，含本地时区）。
// 整条 SQL 失败返回 (0, err)：与逐条版「某个写失败只出声不中断」在故障态不完全同形，
// 但正常路径下两者落库行逐行等值、n 相等，由 notifications_batch_test.go 锁住。
func (s *Store) CreateNotificationsBatch(items []NotificationDraft) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	now := time.Now().Format(time.RFC3339)
	var b strings.Builder
	b.WriteString("INSERT INTO notifications (user_id, title, body, ref_type, ref_id, read_at, created_at) VALUES ")
	args := make([]interface{}, 0, len(items)*6)
	for i, it := range items {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?,?,?,?,?,'',?)")
		args = append(args, it.UserID, it.Title, it.Body, it.RefType, it.RefID, now)
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(), b.String(), args...); err != nil {
		return 0, err
	}
	return len(items), nil
}
