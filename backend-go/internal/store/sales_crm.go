// ============ sales_crm.go · 职责说明 ============
// 三期 W6：CRM 域的数据访问层（handoff_sessions / customers / leads / deals / flow_instances
// + 收件箱列表读口）。全部方法带显式 tid，SELECT/UPDATE 一律带 tenant_id 条件；
// 时间一律 UTC RFC3339；金额一律「分」。
//
// ★ pending_human 的 W6 回填口径（v1.4 裁定，A24/M0263）：
//   RecordReply 的 INSERT 已按 message_id 精确落位（W2）；W6 只做两件事——
//   ① EnsureHandoffSession 补「assist 会话 → conversation_id」映射（幂等，行 id 即会话维度）；
//   ② MarkPendingHumanBySession 按 conversation_id **精确** UPDATE 归因行（pending_human=0 的行才动），
//     不按「最近一条」找（并发下会标错行）、不重复写标记（重复回写只刷新映射不翻标记）。
// ================================================
package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"translator/internal/db"
)

// 线索五阶段（漏斗扩段的段名，与前端 sales.leads.stage* 键一一对应）。
const (
	LeadStageNew     = "new"
	LeadStageEngaged = "engaged"
	LeadStageQuoted  = "quoted"
	LeadStageWon     = "won"
	LeadStageLost    = "lost"
)

// HandoffSessionRow 转人工会话行（会话映射 + 待人工状态）。
type HandoffSessionRow struct {
	ID           int64  `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	SessionID    string `json:"session_id"`
	MessageID    int64  `json:"message_id"`
	PendingHuman int    `json:"pending_human"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	ResolvedAt   string `json:"resolved_at"`
}

// SalesLeadRow 线索行。
type SalesLeadRow struct {
	ID           int64  `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	CustomerID   int64  `json:"customer_id"`
	Email        string `json:"email"`
	Stage        string `json:"stage"`
	Score        int    `json:"score"`
	PendingHuman int    `json:"pending_human"`
	OwnerUserID  int64  `json:"owner_user_id"`
	UpdatedAt    string `json:"updated_at"`
}

// SalesDealRow 商机行（金额单位＝分）。
type SalesDealRow struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	LeadID     int64  `json:"lead_id"`
	TotalCents int64  `json:"total_cents"`
	Currency   string `json:"currency"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

// InboxEventRow 收件箱事件行（ListInboxEvents 的出参；表在 sales_inbox_migrate.go）。
type InboxEventRow struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	EventID     string `json:"event_id"`
	OneID       string `json:"one_id"`
	Topic       string `json:"topic"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	ProcessedAt string `json:"processed_at"`
}

// EnsureHandoffSession 幂等建会话映射：按 (tid, sessionID) 查，无则建；返回映射行 id。
// 参数：tid=租户；sessionID=assist 会话 id；messageID=触发消息行 id（可 0）；返回：conversation 维度 id 与错误。
func (s *Store) EnsureHandoffSession(tid int64, sessionID string, messageID int64) (int64, error) {
	sessionID = strings.TrimSpace(sessionID)
	if tid <= 0 || sessionID == "" {
		return 0, errors.New("会话映射必须带有效租户与会话 id")
	}
	var id int64
	err := s.db.QueryRow(
		`SELECT id FROM handoff_sessions WHERE tenant_id=? AND session_id=?`, tid, sessionID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO handoff_sessions(tenant_id, session_id, message_id, created_at) VALUES(?,?,?,?)`,
		tid, sessionID, messageID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		// 并发建行撞唯一键：回读赢家行（幂等语义，不是错误）。
		if db.IsUniqueViolation(err) {
			var wid int64
			if e2 := s.db.QueryRow(
				`SELECT id FROM handoff_sessions WHERE tenant_id=? AND session_id=?`, tid, sessionID).Scan(&wid); e2 == nil {
				return wid, nil
			}
		}
		return 0, err
	}
	id, _ = res.LastInsertId()
	return id, nil
}

// MarkPendingHumanBySession 按会话精确回填：映射行置 pending_human=1，
// 归因行按 conversation_id 精确 UPDATE（只动 pending_human=0 的行——不重复写标记）。
// ★ 只补映射与标记，绝不按「最近一条」定位（v1.4 裁定：那会标错行）。
// 参数：tid=租户；sessionID=assist 会话 id；返回：是否确有归因行被置位（映射行恒建立）。
func (s *Store) MarkPendingHumanBySession(tid int64, sessionID string) (bool, error) {
	convID, err := s.EnsureHandoffSession(tid, sessionID, 0)
	if err != nil {
		return false, err
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE handoff_sessions SET pending_human=1 WHERE tenant_id=? AND id=? AND pending_human=0`,
		tid, convID); err != nil {
		return false, err
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE reply_attributions SET pending_human=1 WHERE tenant_id=? AND conversation_id=? AND pending_human=0`,
		tid, convID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ResolveHandoffSession 人工处理完成：会话行置 resolved（幂等；重复 resolve 不回翻）。
func (s *Store) ResolveHandoffSession(tid int64, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if tid <= 0 || sessionID == "" {
		return errors.New("会话完结必须带有效租户与会话 id")
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE handoff_sessions SET status='resolved', pending_human=0, resolved_at=?
		 WHERE tenant_id=? AND session_id=? AND status<>'resolved'`,
		time.Now().UTC().Format(time.RFC3339), tid, sessionID)
	return err
}

// ListHandoffSessions 列待人工（onlyPending=true）或全部 open 会话（管理面）。
func (s *Store) ListHandoffSessions(tid int64, onlyPending bool, limit int64) ([]HandoffSessionRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, tenant_id, session_id, message_id, pending_human, status, created_at, resolved_at
	      FROM handoff_sessions WHERE tenant_id=? AND status='open'`
	if onlyPending {
		q += ` AND pending_human=1`
	}
	q += ` ORDER BY id DESC LIMIT ?`
	rows, err := s.db.Query(q, tid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HandoffSessionRow{}
	for rows.Next() {
		var r HandoffSessionRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.SessionID, &r.MessageID, &r.PendingHuman, &r.Status, &r.CreatedAt, &r.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListInboxEvents 收件箱事件分页（W2 只有单条读口；W6 补列表排障面，最新在前）。
func (s *Store) ListInboxEvents(tid int64, status string, limit int64) ([]InboxEventRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, tenant_id, event_id, one_id, topic, status, created_at, processed_at
	      FROM inbox_events WHERE tenant_id=?`
	args := []interface{}{tid}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InboxEventRow{}
	for rows.Next() {
		var r InboxEventRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.EventID, &r.OneID, &r.Topic, &r.Status, &r.CreatedAt, &r.ProcessedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertCustomerFromEmail 幂等建客户行（按 email）；返回客户 id。
func (s *Store) UpsertCustomerFromEmail(tid int64, email, displayName string) (int64, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if tid <= 0 || email == "" {
		return 0, errors.New("客户登记必须带有效租户与邮箱")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var id int64
	err := s.db.QueryRow(`SELECT id FROM sales_customers WHERE tenant_id=? AND email=?`, tid, email).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_customers(tenant_id, email, display_name, created_at) VALUES(?,?,?,?)`,
		tid, email, displayName, now)
	if err != nil {
		if db.IsUniqueViolation(err) {
			var wid int64
			if e2 := s.db.QueryRow(`SELECT id FROM sales_customers WHERE tenant_id=? AND email=?`, tid, email).Scan(&wid); e2 == nil {
				return wid, nil
			}
		}
		return 0, err
	}
	id, _ = res.LastInsertId()
	return id, nil
}

// UpsertLeadFromEmail 幂等建/更新线索：stage 前进只许单向（new→engaged→quoted→won/lost），
// 回退请求被忽略（防止乱序事件把漏斗打回去）；score 取较大值。
// 参数：tid=租户；email=客户邮箱；stage=目标阶段；score=意图分；返回：错误。
func (s *Store) UpsertLeadFromEmail(tid int64, email, stage string, score int) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if tid <= 0 || email == "" {
		return errors.New("线索登记必须带有效租户与邮箱")
	}
	if _, ok := leadStageRank[stage]; !ok {
		return errors.New("线索阶段非法")
	}
	custID, err := s.UpsertCustomerFromEmail(tid, email, "")
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var id int64
	var curStage string
	var curScore int
	err = s.db.QueryRow(
		`SELECT id, stage, score FROM sales_leads WHERE tenant_id=? AND email=?`, tid, email).
		Scan(&id, &curStage, &curScore)
	if err == nil {
		// 单向推进：目标档序 ≤ 当前档序则只刷分（won/lost 是终态，互斥由 rank 判）。
		if leadStageRank[stage] >= leadStageRank[curStage] {
			if _, err := db.Exec(s.db, db.CurrentDialect(),
				`UPDATE sales_leads SET stage=?, score=?, updated_at=? WHERE tenant_id=? AND id=?`,
				stage, maxInt(score, curScore), now, tid, id); err != nil {
				return err
			}
			return nil
		}
		_, err := db.Exec(s.db, db.CurrentDialect(),
			`UPDATE sales_leads SET score=?, updated_at=? WHERE tenant_id=? AND id=?`,
			maxInt(score, curScore), now, tid, id)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_leads(tenant_id, customer_id, email, stage, score, updated_at, created_at) VALUES(?,?,?,?,?,?,?)`,
		tid, custID, email, stage, score, now, now)
	if err != nil && db.IsUniqueViolation(err) {
		return nil // 并发建行：赢家已写，幂等
	}
	return err
}

// leadStageRank 阶段推进序（won/lost 同档终态）。
var leadStageRank = map[string]int{
	LeadStageNew:     0,
	LeadStageEngaged: 1,
	LeadStageQuoted:  2,
	LeadStageWon:     3,
	LeadStageLost:    3,
}

// maxInt 取较大整数（score 只升不降）。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ListLeads 线索分页（stage 空＝全阶段；onlyPendingHuman=true 只看待人工）。
func (s *Store) ListLeads(tid int64, stage string, onlyPendingHuman bool, limit int64) ([]SalesLeadRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, tenant_id, customer_id, email, stage, score, pending_human, owner_user_id, updated_at
	      FROM sales_leads WHERE tenant_id=?`
	args := []interface{}{tid}
	if stage != "" {
		q += ` AND stage=?`
		args = append(args, stage)
	}
	if onlyPendingHuman {
		q += ` AND pending_human=1`
	}
	q += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SalesLeadRow{}
	for rows.Next() {
		var r SalesLeadRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.CustomerID, &r.Email, &r.Stage, &r.Score, &r.PendingHuman, &r.OwnerUserID, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LeadFunnelCounts 漏斗扩段的读数：五阶段各档线索数 + 待人工数。
func (s *Store) LeadFunnelCounts(tid int64) (map[string]int64, error) {
	out := map[string]int64{}
	rows, err := s.db.Query(
		`SELECT stage, COUNT(*) FROM sales_leads WHERE tenant_id=? GROUP BY stage`, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var pending int64
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM sales_leads WHERE tenant_id=? AND pending_human=1`, tid).Scan(&pending)
	out["pending_human"] = pending
	return out, nil
}

// CreateDealFromQuote 由线索开一张商机（金额＝分；status 恒 open 起步）。
func (s *Store) CreateDealFromQuote(tid, leadID, totalCents int64, currency string) (SalesDealRow, error) {
	var row SalesDealRow
	if tid <= 0 || leadID <= 0 || totalCents < 0 {
		return row, errors.New("商机开立必须带有效租户、线索与非负金额")
	}
	if strings.TrimSpace(currency) == "" {
		currency = "CNY"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_deals(tenant_id, lead_id, total_cents, currency, status, created_at, updated_at) VALUES(?,?,?,?,?,?,?)`,
		tid, leadID, totalCents, currency, "open", now, now)
	if err != nil {
		return row, err
	}
	id, _ := res.LastInsertId()
	_ = s.UpsertLeadStageByID(tid, leadID, LeadStageQuoted)
	row = SalesDealRow{ID: id, TenantID: tid, LeadID: leadID, TotalCents: totalCents, Currency: currency, Status: "open", CreatedAt: now}
	return row, nil
}

// UpsertLeadStageByID 按线索 id 直接置阶段（报价开单联动；只许前进的守卫在调用方，
// 本口只做租户内精确 UPDATE，不越租户、不新产行）。
func (s *Store) UpsertLeadStageByID(tid, leadID int64, stage string) error {
	if _, ok := leadStageRank[stage]; !ok {
		return errors.New("线索阶段非法")
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE sales_leads SET stage=?, updated_at=? WHERE tenant_id=? AND id=?`,
		stage, time.Now().UTC().Format(time.RFC3339), tid, leadID)
	return err
}

// TouchFlowInstanceByOneID 断点续跑心跳：收到 flow_drive 事件即刷新该 one_id 的 running 实例心跳
// （与 SweepOnce 的「心跳超时判卡死」闭环：requeue 派发到收件箱消费＝实例还活着）。
// 返回：刷新行数（0＝没有 running 实例，也按正常处理）。
func (s *Store) TouchFlowInstanceByOneID(tid int64, oneID string) (int64, error) {
	if tid <= 0 || strings.TrimSpace(oneID) == "" {
		return 0, nil
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE sales_flow_instances SET heartbeat_ts=?, updated_at=? WHERE tenant_id=? AND one_id=? AND status='running'`,
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339), tid, oneID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
