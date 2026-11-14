// ============ writepoint.go · 职责说明 ============
// W4 同事务写点：业务事务内 INSERT 业务行 + INSERT event_outbox（§4.4.2 硬口径 1）。
//
// ★ 不许「先写业务再补事件」：InsertOutboxEvent 收的是调用方**已开启的事务**（db.Execer），
//
//	业务行与事件行共用同一个 tx，任一失败或回滚 ⇒ 两者都不在（E1）。
//	WritePoint 把这个「业务写 + 事件写 + Commit」的形状封成一把可注入事务的函数，
//	留资写点接它即可，不必自己碰 BeginTx/Commit。
//
// 信封必带 tenant_id（硬口径 3）：Payload 必须是完整 mq.Envelope JSON，写入前经
// mq.UnmarshalEnvelope 走同一套 fail-closed 校验（tid<=0 拒收），且头里的
// topic/event_id/tenant_id 必须与本行逐字相等，杜绝「事件行与信封是两个租户」。
// ================================================
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/mq"
)

// InsertOutboxEvent 在**调用方持有事务**内 INSERT 一条 event_outbox 行。
// tx 由业务写点开启并决定 Commit/Rollback；本函数绝不自行提交、绝不另开连接。
// 参数：tx=业务事务（*sql.Tx 或兼容 db.Execer 的执行器）；d=方言；ev=待写事件；返回：写入错误。
func InsertOutboxEvent(tx db.Execer, d db.Dialect, ev StoredEvent) error {
	if !RegisteredTopics[ev.Topic] {
		return fmt.Errorf("未登记的 outbox 主题 %q（名单外的写点为零，须先登记 RegisteredTopics）", ev.Topic)
	}
	if ev.TenantID <= 0 || strings.TrimSpace(ev.EventID) == "" || strings.TrimSpace(ev.Topic) == "" {
		return errors.New("outbox 事件缺 tenant_id / event_id / topic（tid<=0 拒收）")
	}
	env, err := mq.UnmarshalEnvelope(ev.Payload)
	if err != nil {
		return fmt.Errorf("outbox 事件信封校验失败: %w", err)
	}
	if env.Header.TenantID != ev.TenantID || env.Header.EventID != ev.EventID || env.Topic != ev.Topic {
		return errors.New("outbox 事件行与信封头的 topic/event_id/tenant_id 不一致，拒收")
	}
	_, err = db.Exec(tx, d,
		"INSERT INTO event_outbox(topic,event_id,tenant_id,one_id,trace_id,payload,status,created_at) VALUES(?,?,?,?,?,?,?,?)",
		ev.Topic, ev.EventID, ev.TenantID, ev.OneID, ev.TraceID, string(ev.Payload),
		StatusPending, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("写 event_outbox 失败: %w", err)
	}
	return nil
}

// WritePoint 把「业务写 + outbox 写」绑进同一事务：business 回调先在同一 tx 写业务行，
// 紧接着 InsertOutboxEvent 在同一 tx 写事件行，最后 Commit。任一环节出错/回滚 ⇒ 两者都不在。
// 参数：ctx=上下文；conn=开启事务的连接；d=方言；business=业务写回调（收 *sql.Tx）；ev=事件；返回：错误。
func WritePoint(ctx context.Context, conn *sql.DB, d db.Dialect, business func(*sql.Tx) error, ev StoredEvent) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("outbox 写点开启事务失败: %w", err)
	}
	defer tx.Rollback() // Commit 后为 no-op（ErrTxDone 忽略），保证异常路径必回滚
	if err := business(tx); err != nil {
		return fmt.Errorf("业务写失败（事件行一并回滚）: %w", err)
	}
	// ★ E1 / M0292 靶：事件写必须与业务写共用 tx。把下面的 tx 换成 conn（绕过事务）⇒ 回滚时事件行还在 ⇒ 红。
	if err := InsertOutboxEvent(tx, d, ev); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("outbox 写点提交失败: %w", err)
	}
	return nil
}
