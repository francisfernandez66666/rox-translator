// ============ 本文件职责中文说明 ============
// 工单（tickets / ticket_state 表）数据访问层：工单 CRUD、租户隔离查询、
// 待审批列表，以及工单状态轨迹（每步运行快照，版本递增，供前端流程进度展示）。
// 工单状态机：draft → in_progress → pending_approval → approved / rejected → completed。
// =============================================
package store

import (
	"database/sql"
	"fmt"
	"os"
	"time"
	"translator/internal/db"
)

// Ticket 工单
type Ticket struct {
	ID           int64  `json:"id"`                    // 工单主键 ID
	TenantID     int64  `json:"tenant_id"`             // 所属租户 ID
	TicketNo     string `json:"ticket_no"`             // 工单号（T + 时间戳 + 随机后缀）
	Title        string `json:"title"`                 // 工单标题
	Status       string `json:"status"`                // 状态：draft/in_progress/pending_approval/approved/rejected/completed
	SourceText   string `json:"source_text"`           // 待翻译源文本
	FilePath     string `json:"file_path"`             // 关联上传文件路径（空表示纯文本）
	TargetLangs  string `json:"target_langs"`          // 目标语言列表（逗号分隔）
	CreatedBy    int64  `json:"created_by"`            // 创建者用户 ID
	Mode         string `json:"mode"`                  // 翻译模式：fast 快速 / pro 专业校对（空=pro）
	TokensBilled int64  `json:"tokens_billed"`         // 本单实费计费 token 数（真实用量×均摊系数）
	APIUserID    int64  `json:"api_user_id"`           // ★ OpenAPI 归属用户 ID（0=非 API 创建/历史数据，回读校验用）
	ApproverID   int64  `json:"approver_id"`           // 审批人用户 ID（0 表示未分配）
	ReviewerID   int64  `json:"reviewer_id"`           // 审校人用户 ID（0 表示未分配）
	RejectReason string `json:"reject_reason"`         // 驳回原因（被驳回时填写，重翻时使用）
	FinalResult  string `json:"final_result"`          // 最终结果（JSON：含各语言译文及中间轨迹）
	ResultPath   string `json:"result_path,omitempty"` // 结果文件路径（原格式回写产物/xlsx 对照表；空=未生成）
	CreatedAt    string `json:"created_at"`            // 创建时间（RFC3339 字符串）
	UpdatedAt    string `json:"updated_at"`            // 更新时间（RFC3339 字符串）
}

// TicketState 工单状态轨迹（Projector 物化）
type TicketState struct {
	ID         int64  `json:"id"`          // 轨迹记录主键 ID
	TicketID   int64  `json:"ticket_id"`   // 关联工单 ID
	Step       string `json:"step"`        // 流程步骤标识（如 kb_match / gate）
	Status     string `json:"status"`      // 该步骤状态：pending/running/success/failed/skipped
	Payload    string `json:"payload"`     // 步骤轨迹快照（JSON，如命中记录 / 初翻校对进度）
	Version    int    `json:"version"`     // 步骤版本号（同步骤递增）
	UpdatedAt  string `json:"updated_at"`  // 更新时间（RFC3339 字符串）
	StartedAt  string `json:"started_at"`  // 步骤开始时间（首次 running 时记录）
	DurationMs int64  `json:"duration_ms"` // 步骤执行耗时（毫秒，running→终态时结算）
}

// 工单状态
const (
	TicketDraft       = "draft"            // 草稿
	TicketQueued      = "queued"           // 已入队待执行（异步队列）
	TicketInProgress  = "in_progress"      // 处理中
	TicketPendingAppr = "pending_approval" // 待审批
	TicketApproved    = "approved"         // 已批准
	TicketRejected    = "rejected"         // 已驳回
	TicketCompleted   = "completed"
	TicketCancelled   = "cancelled" // 用户取消（翻译中/排队中可取消）
)

// CreateTicket 创建工单（初始状态为草稿）。
// 参数：tid=租户 ID，userID=创建者 ID，title=标题，sourceText=源文本，
// filePath=文件路径，targetLangs=目标语言列表（逗号分隔）。
// 返回：新工单对象。
func (s *Store) CreateTicket(tid, userID int64, title, sourceText, filePath, targetLangs string) (*Ticket, error) {
	now := time.Now()
	t := &Ticket{
		TenantID:    tid,
		TicketNo:    "T" + now.Format("20060102150405") + randSuffix(3), // 生成唯一工单号
		Title:       title,
		Status:      TicketDraft,
		SourceText:  sourceText,
		FilePath:    filePath,
		TargetLangs: targetLangs,
		CreatedBy:   userID,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
	}
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO tickets (tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		t.TenantID, t.TicketNo, t.Title, t.Status, t.SourceText, t.FilePath, t.TargetLangs, t.CreatedBy, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.ID = id // 回填自增主键
	return t, nil
}

// GetTicket 按 id 查询工单（租户隔离校验）。
// 参数：id=工单主键 ID，tid=租户 ID；返回工单对象。
func (s *Store) GetTicket(id, tid int64) (*Ticket, error) {
	var t Ticket
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), created_at, updated_at FROM tickets WHERE id=? AND tenant_id=?", id, tid).
		Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTicketGlobal 按 ID 查询工单（不带租户过滤，worker 异步上下文用）。
func (s *Store) GetTicketGlobal(id int64) (*Ticket, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), created_at, updated_at FROM tickets WHERE id=?", id)
	return scanTicketFull(row)
}

// SetTicketResultPath 写入结果文件路径。
func (s *Store) SetTicketResultPath(id int64, path string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE tickets SET result_path=?, updated_at=? WHERE id=?", path, time.Now().Format(time.RFC3339), id)
	return err
}

// ListTickets 工单列表（租户隔离；onlyMine=true 时只返回当前用户创建的）。
// 参数：tid=租户 ID，userID=用户 ID，onlyMine=是否仅我的工单。
// 返回：工单列表（最多 200 条，按 ID 倒序）。
func (s *Store) ListTickets(tid, userID int64, onlyMine bool) ([]*Ticket, error) {
	q := "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), created_at, updated_at FROM tickets WHERE tenant_id=?"
	args := []interface{}{tid}
	if onlyMine {
		q += " AND created_by=?" // 只看自己创建的
		args = append(args, userID)
	}
	q += " ORDER BY id DESC LIMIT 200"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &t)
	}
	return out, nil
}

// ListPendingApproval 待审批工单列表（供 approver/admin 审批台使用）。
// 参数：tid=租户 ID；返回状态为 pending_approval/approved/rejected 的工单。
func (s *Store) ListPendingApproval(tid int64) ([]*Ticket, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), created_at, updated_at FROM tickets WHERE tenant_id=? AND status IN ('pending_approval','approved','rejected') ORDER BY id DESC LIMIT 200", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &t)
	}
	return out, nil
}

// UpdateTicket 更新工单状态与字段。
// 参数：t=待更新工单对象（以 ID+TenantID 定位）；返回错误。
func (s *Store) UpdateTicket(t *Ticket) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tickets SET title=?, status=?, target_langs=?, approver_id=?, reviewer_id=?, reject_reason=?, final_result=?, mode=?, tokens_billed=?, updated_at=? WHERE id=? AND tenant_id=?",
		t.Title, t.Status, t.TargetLangs, t.ApproverID, t.ReviewerID, t.RejectReason, t.FinalResult, t.Mode, t.TokensBilled,
		time.Now().Format(time.RFC3339), t.ID, t.TenantID)
	return err
}

// ★ 整改：SetTicketState 改为同步骤 UPSERT（每步骤仅保留一行最新轨迹），
// 避免细粒度进度（每批初翻/校对）反复 INSERT 撑爆 ticket_state。
// 同时结算每步执行耗时：首次 running 记录 started_at；running→终态时计算 duration_ms。
func (s *Store) SetTicketState(ticketID int64, step, status, payload string) error {
	now := time.Now().Format(time.RFC3339)
	isTerminal := status == "success" || status == "failed" || status == "warning" || status == "skipped"

	var id int64
	var oldStatus, oldStarted string
	var oldDur int64
	qerr := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id, status, COALESCE(started_at,''), COALESCE(duration_ms,0) FROM ticket_state WHERE ticket_id=? AND step=? ORDER BY version DESC LIMIT 1",
		ticketID, step).Scan(&id, &oldStatus, &oldStarted, &oldDur)
	if qerr != nil {
		// 不存在：新建；首次 running 记录开始时间
		started := ""
		if status == "running" {
			started = now
		}
		_, err := db.Exec(s.db, db.CurrentDialect(),
			"INSERT INTO ticket_state (ticket_id, step, status, payload, version, updated_at, started_at, duration_ms) VALUES (?,?,?,?,1,?,?,0)",
			ticketID, step, status, payload, now, started)
		return err
	}
	// 已存在：更新同一行
	started := oldStarted
	dur := oldDur
	if oldStatus != "running" && status == "running" && started == "" {
		started = now
	}
	if oldStatus == "running" && isTerminal && started != "" {
		if t0, perr := time.Parse(time.RFC3339, started); perr == nil {
			dur = int64(time.Since(t0) / time.Millisecond)
		}
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE ticket_state SET status=?, payload=?, version=version+1, updated_at=?, started_at=?, duration_ms=? WHERE id=?",
		status, payload, now, started, dur, id)
	return err
}

// TicketStateTimingMigrate 为 ticket_state 增加 started_at / duration_ms 列（幂等，列已存在则忽略）。
func (s *Store) TicketStateTimingMigrate() {
	db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE ticket_state ADD COLUMN started_at TEXT")
	db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE ticket_state ADD COLUMN duration_ms INTEGER")
}

// TicketStates 查询工单状态轨迹（按版本升序）。
// 参数：ticketID=工单 ID；返回该工单全部步骤轨迹。
func (s *Store) TicketStates(ticketID int64) ([]*TicketState, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT id, ticket_id, step, status, payload, version, updated_at FROM ticket_state WHERE ticket_id=? ORDER BY version", ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TicketState
	for rows.Next() {
		var st TicketState
		if err := rows.Scan(&st.ID, &st.TicketID, &st.Step, &st.Status, &st.Payload, &st.Version, &st.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &st)
	}
	return out, nil
}

// scanTicketFull 扫描全列工单行（GetTicketGlobal 专用，含 result_path）。
func scanTicketFull(row *sql.Row) (*Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTicketWithFiles 删除工单及其关联数据（文件记录/状态轨迹/产物文件）。
// 物理删除磁盘上的产物文件和上传文件（如果存在）。
// 参数：id=工单 ID，tid=租户 ID。返回错误。
func (s *Store) DeleteTicketWithFiles(id, tid int64) error {
	t, err := s.GetTicket(id, tid)
	if err != nil {
		return err
	}
	// 收集需清理的磁盘文件路径
	var diskPaths []string
	if t.FilePath != "" {
		diskPaths = append(diskPaths, t.FilePath)
	}
	if t.ResultPath != "" {
		diskPaths = append(diskPaths, t.ResultPath)
	}
	tfs, _ := s.TicketFiles(id)
	for _, tf := range tfs {
		if tf.FilePath != "" {
			diskPaths = append(diskPaths, tf.FilePath)
		}
		if tf.ResultPath != "" {
			diskPaths = append(diskPaths, tf.ResultPath)
		}
	}
	// 删除 DB 记录（ticket_files → ticket_state → tickets）
	if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM ticket_files WHERE ticket_id=?", id); err != nil {
		return err
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM ticket_state WHERE ticket_id=?", id); err != nil {
		return err
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM tickets WHERE id=? AND tenant_id=?", id, tid); err != nil {
		return err
	}
	// 异步清理磁盘文件（不阻塞主流程）
	go func() {
		for _, p := range diskPaths {
			os.Remove(p)
		}
	}()
	return nil
}

// StampTicketAPIUser 给 API 创建的任务盖印归属用户 ID（OpenAPI 安全绑定）。
func (s *Store) StampTicketAPIUser(id, userID int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE tickets SET api_user_id=? WHERE id=?", userID, id)
	return err
}

// CancelTicket 用户取消：仅排队中/翻译中可置为 cancelled（幂等安全）。
func (s *Store) CancelTicket(id int64) error {
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tickets SET status='cancelled', updated_at=? WHERE id=? AND status IN ('queued','in_progress')",
		time.Now().Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("工单不在可取消状态")
	}
	return nil
}

// TouchTicket 工单心跳：仅刷新 updated_at（评审整改 R3）。
// 长翻译阶段内业务状态不变，若无心跳，20 分钟卡死巡检会把仍在运行的工单误判重排，
// 造成同工单双副本并发执行（双份 LLM 消耗 + 双份计费）。worker 每 60s 调用一次保活。
func (s *Store) TouchTicket(id int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE tickets SET updated_at=? WHERE id=?", time.Now().Format(time.RFC3339), id)
	return err
}

// RequeueStalledTickets 将超时无进展的 in_progress 工单重置为 queued（断点续传）。
// 返回受影响行数。updated_at 由 worker 心跳持续刷新，「20 分钟未动」即视为真卡死。
//
// ★ 重复执行防线（2026-08-26 评审整改 R3）：仅当该工单的 jobs 行不在
// 「running（租约未过期）」状态时才允许重排——running 即代表本进程仍有活跃 goroutine
// 在处理它；否则会双副本并发跑同一工单（双扣费/双通知）。租约过期的 running 由
// direct 队列 Reserve 自行回收，无需此处越权释放。
func (s *Store) RequeueStalledTickets(stale time.Duration) (int64, error) {
	cut := time.Now().Add(-stale).Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE tickets SET status='queued', updated_at=?
		 WHERE status='in_progress' AND updated_at < ?
		   AND NOT EXISTS (
		       SELECT 1 FROM jobs j
		       WHERE j.type='ticket_run' AND j.status='running'
		         AND CAST(json_extract(j.payload,'$.ticket_id') AS INTEGER) = tickets.id)`,
		time.Now().Format(time.RFC3339), cut)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, nil
	}
	// ★ 兜底释放已删除（2026-08-26 全仓评审 B2）：原实现对「工单已 queued/cancelled」的
	//   running 租约无条件清空、不看租约年龄——而入队顺序是「置工单 queued → 建 job →
	//   worker 认领(running) → 才翻 in_progress」，认领窗口内 sweep 触发会把活跃租约
	//   直接释放，第二个 worker 立即可再领取同一 job，造成同工单双副本执行（双扣费）。
	//   职责边界澄清：
	//   ① 活跃 worker 保护 = 第一段 NOT EXISTS(jobs running)；
	//   ② 过期租约回收 = DirectQueue.Reserve 的 leased_at<=? 判定（唯一合法回收点）；
	//   ③ 进程重启的全量释放 = service.BootResume 显式执行；
	//   ④ 取消场景的重复执行防护 = runTicket 收尾守卫（cancelled 即放弃计费/完成态）。
	return n, nil
}
