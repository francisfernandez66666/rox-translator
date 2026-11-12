// ============ marketing_consent.go · 职责说明 ============
// 三期 W5：外联合规四表的数据访问层（marketing_consents / suppression_list /
// outbound_sequences / outbound_sends）。全部方法带显式 tid，SELECT/UPDATE 一律带
// tenant_id 条件；幂等形状＝「先 INSERT，撞唯一键后经 db.IsUniqueViolation 双方言判定」，
// 绝不先 COUNT 预检、绝不按冲突字符串分支（与 sales_attribution.go 同一口径）。
//
// ★ 合规四件套的读口在这里、判序在 sendOne（api 层唯一出口）：
//  ① ConsentStatus（opt-in/opt-out）② InSuppression ③ DailySendCount（freq_cap_per_day）
//  ④ 序列节点由调用方按 delay_hours 选——store 只存取，不做业务判。
// ================================================
package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"translator/internal/db"
)

// 外联发送行的四个状态（claimed=已占位待发送；sent/failed/skipped 为终态）。
const (
	OutboundStatusClaimed = "claimed"
	OutboundStatusSent    = "sent"
	OutboundStatusFailed  = "failed"
	OutboundStatusSkipped = "skipped"
)

// 订阅两态（无行＝从未订阅，按未授权处理）。
const (
	ConsentOptedIn  = "opted_in"
	ConsentOptedOut = "opted_out"
)

// OutboundSequenceNode 邮件序列节点行。
type OutboundSequenceNode struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	NodeKey    string `json:"node_key"`
	Name       string `json:"name"`
	DelayHours int64  `json:"delay_hours"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	Enabled    int    `json:"enabled"`
	UpdatedAt  string `json:"updated_at"`
}

// OutboundSendRow 发送占位/结果行（管理面只读展示 + 排障）。
type OutboundSendRow struct {
	ID            int64  `json:"id"`
	TenantID      int64  `json:"tenant_id"`
	CustomerEmail string `json:"customer_email"`
	NodeKey       string `json:"node_key"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	ClaimedAt     string `json:"claimed_at"`
	SentAt        string `json:"sent_at"`
	CreatedAt     string `json:"created_at"`
}

// SetConsentStatus 登记订阅状态（唯一键 (tid,email)，重复写＝改态并留 source 痕迹）。
// 参数：tid=租户；email=客户邮箱；status=opted_in/opted_out；source=来源标记；返回：错误。
func (s *Store) SetConsentStatus(tid int64, email, status, source string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if tid <= 0 || email == "" {
		return errors.New("订阅登记必须带有效租户与邮箱")
	}
	if status != ConsentOptedIn && status != ConsentOptedOut {
		return errors.New("订阅状态只能是 opted_in 或 opted_out")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// 先 UPDATE：命中行＝改态（覆盖旧态并刷新 source 痕迹）；0 行＝首登记，再 INSERT。
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE marketing_consents SET status=?, source=?, updated_at=? WHERE tenant_id=? AND customer_email=?`,
		status, source, now, tid, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO marketing_consents(tenant_id, customer_email, status, source, created_at, updated_at) VALUES(?,?,?,?,?,?)`,
		tid, email, status, source, now, now)
	return err
}

// ConsentStatus 读订阅状态；无行返回 (\"\", false)＝从未订阅（按未授权处理）。
func (s *Store) ConsentStatus(tid int64, email string) (string, bool, error) {
	var status string
	err := s.db.QueryRow(
		`SELECT status FROM marketing_consents WHERE tenant_id=? AND customer_email=?`,
		tid, strings.ToLower(strings.TrimSpace(email))).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return status, true, nil
}

// AddSuppression 加入抑制名单（email 或 domain 形态的匹配值，重复加幂等）。
func (s *Store) AddSuppression(tid int64, matchValue, kind, reason string) error {
	matchValue = strings.ToLower(strings.TrimSpace(matchValue))
	if tid <= 0 || matchValue == "" {
		return errors.New("抑制名单必须带有效租户与匹配值")
	}
	if kind != "email" && kind != "domain" {
		return errors.New("抑制名单类型只能是 email 或 domain")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO suppression_list(tenant_id, match_value, kind, reason, created_at) VALUES(?,?,?,?,?)`,
		tid, matchValue, kind, reason, now)
	if err != nil && db.IsUniqueViolation(err) {
		return nil // 重复加入幂等：名单里已经有了，不是错误
	}
	return err
}

// InSuppression 判邮箱（或其域名）是否在抑制名单——命中即跳过外联。
func (s *Store) InSuppression(tid int64, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false, nil
	}
	domain := email
	if at := strings.LastIndex(email, "@"); at >= 0 {
		domain = email[at+1:]
	}
	var cnt int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM suppression_list WHERE tenant_id=? AND match_value IN (?, ?)`,
		tid, email, domain).Scan(&cnt)
	return cnt > 0, err
}

// SaveSequenceNode 幂等写一个序列节点（node_key 唯一，重复写＝整行覆盖）。
func (s *Store) SaveSequenceNode(tid int64, n OutboundSequenceNode) error {
	if tid <= 0 || strings.TrimSpace(n.NodeKey) == "" {
		return errors.New("序列节点必须带有效租户与 node_key")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	n.UpdatedAt = now
	// 先 UPDATE：命中行＝覆盖编辑；0 行＝新建。
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE outbound_sequences SET name=?, delay_hours=?, subject=?, body=?, enabled=?, updated_at=?
		 WHERE tenant_id=? AND node_key=?`,
		n.Name, n.DelayHours, n.Subject, n.Body, n.Enabled, now, tid, n.NodeKey)
	if err != nil {
		return err
	}
	if aff, _ := res.RowsAffected(); aff > 0 {
		return nil
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO outbound_sequences(tenant_id, node_key, name, delay_hours, subject, body, enabled, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		tid, n.NodeKey, n.Name, n.DelayHours, n.Subject, n.Body, n.Enabled, now, now)
	return err
}

// ListSequenceNodes 列本租户全部序列节点（管理面编辑用，按 delay_hours 升序）。
func (s *Store) ListSequenceNodes(tid int64) ([]OutboundSequenceNode, error) {
	rows, err := s.db.Query(
		`SELECT id, tenant_id, node_key, name, delay_hours, subject, body, enabled, updated_at
		 FROM outbound_sequences WHERE tenant_id=? ORDER BY delay_hours ASC, id ASC`, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboundSequenceNode{}
	for rows.Next() {
		var n OutboundSequenceNode
		if err := rows.Scan(&n.ID, &n.TenantID, &n.NodeKey, &n.Name, &n.DelayHours, &n.Subject, &n.Body, &n.Enabled, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ListOutboundSends 分页列发送记录（管理面排障用，最新在前）。
func (s *Store) ListOutboundSends(tid, limit int64) ([]OutboundSendRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, tenant_id, customer_email, node_key, status, reason, claimed_at, sent_at, created_at
		 FROM outbound_sends WHERE tenant_id=? ORDER BY id DESC LIMIT ?`, tid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboundSendRow{}
	for rows.Next() {
		var r OutboundSendRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.CustomerEmail, &r.NodeKey, &r.Status, &r.Reason, &r.ClaimedAt, &r.SentAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimOutboundSend 幂等占位：同 (tid,email,node) 首次返回 (行ID,true)；重复/并发败方 (0,false,nil)。
// ★ 这是「多实例只发一次」的唯一结构保证（ux_outbound_once）；调用方必须在占位成功后才发信。
func (s *Store) ClaimOutboundSend(tid int64, email, nodeKey string) (int64, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if tid <= 0 || email == "" || nodeKey == "" {
		return 0, false, errors.New("外联占位必须带有效租户、邮箱与节点键")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO outbound_sends(tenant_id, customer_email, node_key, status, claimed_at, created_at) VALUES(?,?,?,?,?,?)`,
		tid, email, nodeKey, OutboundStatusClaimed, now, now)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return 0, false, nil // 已占位过：幂等语义，不是错误
		}
		return 0, false, err
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

// MarkOutboundSent 终态：已发送（只能从 claimed 推进，防止败方覆盖赢家结果）。
func (s *Store) MarkOutboundSent(tid, id int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE outbound_sends SET status=?, sent_at=? WHERE tenant_id=? AND id=? AND status=?`,
		OutboundStatusSent, time.Now().UTC().Format(time.RFC3339), tid, id, OutboundStatusClaimed)
	return err
}

// MarkOutboundFailed 终态：发送失败（带原因，供下一轮重试判据/排障）。
func (s *Store) MarkOutboundFailed(tid, id int64, reason string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE outbound_sends SET status=?, reason=? WHERE tenant_id=? AND id=? AND status=?`,
		OutboundStatusFailed, reason, tid, id, OutboundStatusClaimed)
	return err
}

// MarkOutboundSkipped 终态：合规跳过（原因码进 reason；占位已立 ⇒ 该节点永不重发）。
func (s *Store) MarkOutboundSkipped(tid, id int64, reason string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE outbound_sends SET status=?, reason=? WHERE tenant_id=? AND id=? AND status=?`,
		OutboundStatusSkipped, reason, tid, id, OutboundStatusClaimed)
	return err
}

// DailySendCount 客户当日已发送条数（freq_cap_per_day 的计数口径，UTC 日界）。
func (s *Store) DailySendCount(tid int64, email string) (int64, error) {
	day := time.Now().UTC().Format("2006-01-02")
	var cnt int64
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM outbound_sends
		 WHERE tenant_id=? AND customer_email=? AND status IN (?, ?) AND claimed_at LIKE ?`,
		tid, strings.ToLower(strings.TrimSpace(email)), OutboundStatusSent, OutboundStatusClaimed, day+"%").Scan(&cnt)
	return cnt, err
}

// GuardOutboundConfigKey 读取频限配置键的默认值（freq_cap_per_day，独立于 AI 免费额度——A2 裁定）。
func GuardOutboundConfigKey() string { return "freq_cap_per_day" }

// ListOptedInEmails 列本租户当前 opted_in 的客户邮箱（外联批任务的候选集，按 id 升序分页）。
func (s *Store) ListOptedInEmails(tid, limit int64) ([]string, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT customer_email FROM marketing_consents WHERE tenant_id=? AND status=? ORDER BY id ASC LIMIT ?`,
		tid, ConsentOptedIn, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PurgeCustomerOutboundTrail GDPR 删除请求权的最小实现（施工图 §8 C4：建表即产生个人数据，
// 擦除策略随 W5 同批落，不拖到法务）：删该客户在租户内的全部外联留痕——
// outbound_sends（发送占位/结果，含邮箱明文）与 marketing_consents（订阅状态）。
// ★ 只擦「这个客户、这个租户」的行；suppression_list 的抑制记录**保留**（它恰恰是
//   「别再联系此人」的证据，删了反而会让下游重新外联）；控制列名不经拼接。
// 参数：tid=租户；email=客户邮箱；返回：删除行数与错误。
func (s *Store) PurgeCustomerOutboundTrail(tid int64, email string) (int64, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if tid <= 0 || email == "" {
		return 0, errors.New("擦除必须带有效租户与邮箱")
	}
	var total int64
	for _, q := range []string{
		`DELETE FROM outbound_sends WHERE tenant_id=? AND customer_email=?`,
		`DELETE FROM marketing_consents WHERE tenant_id=? AND customer_email=?`,
	} {
		res, err := db.Exec(s.db, db.CurrentDialect(), q, tid, email)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
