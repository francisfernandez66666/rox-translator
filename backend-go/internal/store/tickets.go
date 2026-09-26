// ============ tickets.go · 职责说明 ============
// store 包工单（tickets / ticket_state 表）数据访问层。
// 工单 CRUD、租户隔离查询、待审批列表，以及工单状态轨迹（每步运行快照，版本递增，供前端流程进度展示）。
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
// 时间字段（created_at/updated_at/result_expires_at 等）一律存 RFC3339 文本而非原生日期类型：
// 同一份 SQL 要在 SQLite（本地/CI）与 PostgreSQL（生产）两侧跑，文本列可免去方言日期函数与
// 驱动时区差异；全链路读写都用 time.Now().Format(time.RFC3339) 同一格式，比较口径一致。
type Ticket struct {
	ID             int64  `json:"id"`                         // 工单主键 ID
	TenantID       int64  `json:"tenant_id"`                  // 所属租户 ID
	TicketNo       string `json:"ticket_no"`                  // 工单号（T + 时间戳 + 随机后缀）
	Title          string `json:"title"`                      // 工单标题
	Status         string `json:"status"`                     // 状态：draft/in_progress/pending_approval/approved/rejected/completed
	SourceText     string `json:"source_text"`                // 待翻译源文本
	FilePath       string `json:"file_path"`                  // 关联上传文件路径（空表示纯文本）
	TargetLangs    string `json:"target_langs"`               // 目标语言列表（逗号分隔）
	CreatedBy      int64  `json:"created_by"`                 // 创建者用户 ID
	Mode           string `json:"mode"`                       // 翻译模式：fast 快速 / pro 专业校对（空=pro）
	TokensBilled   int64  `json:"tokens_billed"`              // 本单实收计费 token（扣费现场累计，逐笔等于 usage_ledger.quantity；★ F-49 起该口径才与实际扣费一致，历史行为裸用量）
	APIUserID      int64  `json:"api_user_id"`                // ★ OpenAPI 归属用户 ID（0=非 API 创建/历史数据，回读校验用）
	MaxLength      int64  `json:"max_length"`                 // ★ 缩翻最长字符限制（0=未启用缩翻；>0=译文总长不得超过该值）
	Delivery       string `json:"delivery"`                   // ★ 文件工单交付方式：restore 还原文件模式（默认）/ text 纯文案模式
	TextResultPath string `json:"text_result_path,omitempty"` // ★ 纯文案 .md 产物路径（还原模式兜底附加物 / 纯文案模式主产物）
	ApproverID     int64  `json:"approver_id"`                // 审批人用户 ID（0 表示未分配）
	ReviewerID     int64  `json:"reviewer_id"`                // 审校人用户 ID（0 表示未分配）
	RejectReason   string `json:"reject_reason"`              // 驳回原因（被驳回时填写，重翻时使用）
	// ★ F-42-b（2026-09-25 UAT 修复批）：驳回原因的来源方。此前只有 reject_reason 一列，
	//   「人工驳回意见」与「系统失败原因」混写同一列，重翻分支（workflow.runAIInitial）
	//   按「非空即人工意见」处理——系统写的 'context canceled' 被当成驳回意见喂给
	//   重翻循环，载荷全空时循环整轮 continue、零 LLM 调用 return nil，假 completed 就是这么来的。
	//   human=人工驳回（唯一入口 api/tickets.go 审批驳回），system=流程/计费/预检写入，
	//   空串=老数据（列补列前的历史行，判据按「非 human」处理，配合双保险见 d)）。
	RejectSource string `json:"reject_source"`         // 驳回来源：human 人工 / system 系统 / 空=历史行
	FinalResult  string `json:"final_result"`          // 最终结果（JSON：含各语言译文及中间轨迹）
	ResultPath   string `json:"result_path,omitempty"` // 结果文件路径（原格式回写产物/xlsx 对照表；空=未生成）
	// ★ 改造 4（2026-09-17）：评估不达标标记（1=有语言评估总分低于 evals_fail_threshold）。
	//   独立列而非运行时解析 payload——列表接口可零成本透出并支持后续按标筛选/统计。
	QualityFlagged int `json:"quality_flagged"` // 0=正常 / 1=质检存疑（待人工复核）
	// ★ 改造 5（2026-09-17）：确定性质检摘要（error/warning 计数），列表行徽标数据源。
	//   同理由 runQA 落列，避免列表接口为渲染徽标解析整份 final_result payload。
	QAErrors   int    `json:"qa_errors"`   // error 级问题数（已自动重译后仍存在）
	QAWarnings int    `json:"qa_warnings"` // warning 级提示数
	CreatedAt  string `json:"created_at"`  // 创建时间（RFC3339 字符串）
	UpdatedAt  string `json:"updated_at"`  // 更新时间（RFC3339 字符串）
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
// 工单号形态 T+14 位秒级时间戳+3 位随机后缀（randSuffix 走 crypto/rand）：同一秒内靠随机后缀区分。
// 建表时 ticket_no 上没有唯一约束，所以它是给人看、对外引用的编号，不承担并发唯一性保证。
// 初始状态一律 draft（是否入队由调用方决定），主键由 db.InsertID 双方言回填。
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
// SELECT 里逐个 COALESCE 是给「补列之前的老行」兜底：后加的列在旧数据上是 NULL，
// 直接 Scan 进 string/int 会报类型错误。COALESCE 是 SQLite 与 PG 共同支持的写法，
// 默认值与业务默认一致（delivery 缺省 restore、模式缺省即 pro、质检计数缺省 0）。
// WHERE 带 tenant_id：跨租户取单在这里就等价于「不存在」，不给调用方区分二者的机会。
func (s *Store) GetTicket(id, tid int64) (*Ticket, error) {
	var t Ticket
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, COALESCE(reject_source,'') AS reject_source, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), COALESCE(max_length,0), COALESCE(delivery,'restore') AS delivery, COALESCE(text_result_path,'') AS text_result_path, COALESCE(quality_flagged,0) AS quality_flagged, COALESCE(qa_errors,0) AS qa_errors, COALESCE(qa_warnings,0) AS qa_warnings, created_at, updated_at FROM tickets WHERE id=? AND tenant_id=?", id, tid).
		Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.RejectSource, &t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.MaxLength, &t.Delivery, &t.TextResultPath, &t.QualityFlagged, &t.QAErrors, &t.QAWarnings, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTicketGlobal 按 ID 查询工单（不带租户过滤，worker 异步上下文用）。
// 只允许在「已凭 ID 定位、且要做跨租户运维动作」的后台路径使用（入队/认领/收尾）；
// 任何面向用户的接口都必须走带 tenant_id 的 GetTicket，否则会跨租户读单。
func (s *Store) GetTicketGlobal(id int64) (*Ticket, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, COALESCE(reject_source,'') AS reject_source, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), COALESCE(max_length,0), COALESCE(delivery,'restore') AS delivery, COALESCE(text_result_path,'') AS text_result_path, COALESCE(quality_flagged,0) AS quality_flagged, COALESCE(qa_errors,0) AS qa_errors, COALESCE(qa_warnings,0) AS qa_warnings, created_at, updated_at FROM tickets WHERE id=?", id)
	return scanTicketFull(row)
}

// GetTicketByNo 按工单号查询工单（对应用户粘贴「工单号 T20260902...」而非数字 ID 的场景）。
// 同样不带租户过滤（只按 ticket_no 定位，查询时无法预知归属），
// 调用方拿到结果后必须自行比对 tenant_id 再对外返回。
func (s *Store) GetTicketByNo(no string) (*Ticket, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, COALESCE(reject_source,'') AS reject_source, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), COALESCE(max_length,0), COALESCE(delivery,'restore') AS delivery, COALESCE(text_result_path,'') AS text_result_path, COALESCE(quality_flagged,0) AS quality_flagged, COALESCE(qa_errors,0) AS qa_errors, COALESCE(qa_warnings,0) AS qa_warnings, created_at, updated_at FROM tickets WHERE ticket_no=?", no)
	return scanTicketFull(row)
}

// SetTicketResultPath 写入结果文件路径。
// WHERE 只有 id、不带 tenant_id：本方法按主键定位（worker 侧已用 GetTicketGlobal 拿到归属），
// 新增面向 HTTP 的调用点请改用带租户条件的写法，别靠调用方自觉。
func (s *Store) SetTicketResultPath(id int64, path string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE tickets SET result_path=?, updated_at=? WHERE id=?", path, time.Now().Format(time.RFC3339), id)
	return err
}

// SetTicketTextResultPath 写入纯文案 .md 产物路径（还原模式兜底附加物 / 纯文案模式主产物）。
// 与 SetTicketResultPath 的差别：本方法不刷 updated_at（updated_at 同时是卡死巡检的陈旧判据）。
func (s *Store) SetTicketTextResultPath(id int64, path string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE tickets SET text_result_path=? WHERE id=?", path, id)
	return err
}

// ListTickets 工单列表（租户隔离；onlyMine=true 时只返回当前用户创建的）。
// 参数：tid=租户 ID，userID=用户 ID，onlyMine=是否仅我的工单。
// 返回：工单列表（最多 200 条，按 ID 倒序）。
// 固定 200 条上限：工单列表页无分页，靠倒序保证「最近的可操作单」一定在结果里，
// 同时给租户隔离查询一个天然的响应体上界。
// 单行 Scan 失败只跳过该行（整表不因一行坏数据而查询失败），但错误也不上报。
func (s *Store) ListTickets(tid, userID int64, onlyMine bool) ([]*Ticket, error) {
	q := "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, COALESCE(reject_source,'') AS reject_source, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), COALESCE(max_length,0), COALESCE(delivery,'restore') AS delivery, COALESCE(text_result_path,'') AS text_result_path, COALESCE(quality_flagged,0) AS quality_flagged, COALESCE(qa_errors,0) AS qa_errors, COALESCE(qa_warnings,0) AS qa_warnings, created_at, updated_at FROM tickets WHERE tenant_id=?"
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
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.RejectSource, &t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.MaxLength, &t.Delivery, &t.TextResultPath, &t.QualityFlagged, &t.QAErrors, &t.QAWarnings, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &t)
	}
	return out, nil
}

// ListPendingApproval 待审批工单列表（供 approver/admin 审批台使用）。
// 参数：tid=租户 ID；返回状态为 pending_approval/approved/rejected 的工单。
func (s *Store) ListPendingApproval(tid int64) ([]*Ticket, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, COALESCE(reject_source,'') AS reject_source, final_result, COALESCE(result_path,''), COALESCE(mode,'') AS mode, COALESCE(tokens_billed,0) AS tokens_billed, COALESCE(api_user_id,0), COALESCE(max_length,0), COALESCE(delivery,'restore') AS delivery, COALESCE(text_result_path,'') AS text_result_path, COALESCE(quality_flagged,0) AS quality_flagged, COALESCE(qa_errors,0) AS qa_errors, COALESCE(qa_warnings,0) AS qa_warnings, created_at, updated_at FROM tickets WHERE tenant_id=? AND status IN ('pending_approval','approved','rejected') ORDER BY id DESC LIMIT 200", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.RejectSource, &t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.MaxLength, &t.Delivery, &t.TextResultPath, &t.QualityFlagged, &t.QAErrors, &t.QAWarnings, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &t)
	}
	return out, nil
}

// UpdateTicket 更新工单状态与字段。
// 参数：t=待更新工单对象（以 ID+TenantID 定位）；返回错误。
// ⚠️ 这是「整对象覆盖写」：SET 里列出的每个字段都会被 t 的值写回，
// 调用方必须先 Get 到完整对象再改需要变的字段，否则 mode/delivery/final_result 等
// 会被零值清空。源文本与文件路径不出现在 SET 列表中（本方法不写这两列）。
// 状态推进另有原子口径（ClaimTicketForRun / FinishTicket），需要 CAS 语义时不要用本方法。
func (s *Store) UpdateTicket(t *Ticket) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tickets SET title=?, status=?, target_langs=?, approver_id=?, reviewer_id=?, reject_reason=?, reject_source=?, final_result=?, mode=?, tokens_billed=?, max_length=?, delivery=?, updated_at=? WHERE id=? AND tenant_id=?",
		t.Title, t.Status, t.TargetLangs, t.ApproverID, t.ReviewerID, t.RejectReason, t.RejectSource, t.FinalResult, t.Mode, t.TokensBilled, t.MaxLength, t.Delivery,
		time.Now().Format(time.RFC3339), t.ID, t.TenantID)
	return err
}

// 驳回来源（★ F-42-b 2026-09-25 UAT 修复批）：reject_source 列取值。
// 判据侧只承认 'human' 作重翻意见；'system' 与空串一律不走人工重翻分支。
const (
	RejectSourceHuman  = "human"  // 审批台人工驳回（唯一入口 api/tickets.go 驳回 handler）
	RejectSourceSystem = "system" // 系统写入：流程步骤失败/余额预检不足/翻译中欠费中止
)

// ClaimTicketForRun CAS 认领工单执行权（★ P1-5 修复 2026-09-14）：
// 仅当工单仍处于可执行态（draft/queued，或「人工驳回」的 rejected）时原子翻到 in_progress。
// 旧实现 runTicket 只挡 completed，同一工单可被两个 worker 并发执行
// （双份翻译、FinalResult 互相覆盖、实时计费双倍）。返回受影响行数：
// 0 = 已被其他 worker 认领或已进入终态/不可自动执行态，调用方必须放弃执行。
// ★ F-42-c（2026-09-25 UAT 修复批）：rejected 收窄为「仅人工驳回」——
//
//	系统错误类 rejected（余额耗尽/步骤失败）此前与 direct 队列 MarkFailed 的
//	attempts<max 自动回队合成无限重跑：每次重跑都在 runAIInitial 里被当成人工驳回意见，
//	载荷全空时循环整轮 continue、零 LLM 调用 return nil ⇒ 假 completed。
//	用户显式重跑不受影响：handleTicketRun 在入队前已把状态写回 queued（api/tickets.go:461）。
//	reject_source 为空串（补列前的历史 rejected 行）同样不再自动认领——按「非人工即不自动重跑」
//	的保守口径，需要重跑由用户在工单页点运行。
//
// 参数：id=工单 ID。返回：受影响行数与错误。
func (s *Store) ClaimTicketForRun(id int64) (int64, error) {
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tickets SET status='in_progress', updated_at=? WHERE id=? AND (status IN ('draft','queued') OR (status='rejected' AND COALESCE(reject_source,'')='human'))",
		time.Now().Format(time.RFC3339), id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// SetTicketState 写入/更新某工单某步骤的最新轨迹（每步骤只留一行，version 每次自增）。
// ★ 整改：SetTicketState 改为同步骤 UPSERT（每步骤仅保留一行最新轨迹），
// 避免细粒度进度（每批初翻/校对）反复 INSERT 撑爆 ticket_state。
// 同时结算每步执行耗时：首次 running 记录 started_at；running→终态时计算 duration_ms。
// 注意：本方法是「先 SELECT 再 INSERT/UPDATE」两步，未包事务——同一步骤被并发写时
// 可能丢一次更新或重复自增 version。轨迹只用于前端流程展示，不做资金/状态判定依据，
// 因此按可容忍丢失处理；需要强一致的收尾写入请用 FinishTicket。
func (s *Store) SetTicketState(ticketID int64, step, status, payload string) error {
	now := time.Now().Format(time.RFC3339)
	// 终态集合含 warning：降级交付（版式还原失败改出 .md）同样要结算耗时、不再被当作进行中
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
// 历史写法：靠「列已存在时 ALTER 直接报错、错误被丢弃」达成幂等。新增列请不要再照此写，
// 一律走 db.EnsureColumns 的双方言幂等补列（见 TicketQualityMigrate）。
func (s *Store) TicketStateTimingMigrate() {
	db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE ticket_state ADD COLUMN started_at TEXT")
	db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE ticket_state ADD COLUMN duration_ms INTEGER")
}

// TicketQualityMigrate 为 tickets 增加质检透出列（★ 改造 4/5，2026-09-17，幂等）：
//   - quality_flagged：1=存在语言评估总分低于 evals_fail_threshold，需人工复核；
//   - qa_errors / qa_warnings：确定性质检（qa.Check）error/warning 计数。
//
// 三者均落列而非运行时解析 final_result——工单列表接口据此零成本渲染质检徽标，
// 避免为 UI 展示解析整份 payload。复用 db.EnsureColumns 双方言幂等补列。
// 注：存量历史工单不回溯（保持 0），新跑工单由 runQA / applyEvalDisposition 写入。
func (s *Store) TicketQualityMigrate() {
	_ = db.EnsureColumns(s.db, db.CurrentDialect(), "tickets", map[string]string{
		"quality_flagged": "INTEGER NOT NULL DEFAULT 0",
		"qa_errors":       "INTEGER NOT NULL DEFAULT 0",
		"qa_warnings":     "INTEGER NOT NULL DEFAULT 0",
	})
}

// TicketRejectSourceMigrate ★ F-42-b（2026-09-25 UAT 修复批）：为 tickets 补
// reject_source 列（幂等，走 db.EnsureColumns，AGENTS §一·1 第 4 条）。
// 老行留空串（未知来源）——判据侧按「非 'human' 即不作重翻意见」处理，
// 历史人工驳回单因空串不再自动走重翻分支，由用户显式重跑，等价损失可接受
// （配套双保险见 workflow.runAIInitial：载荷存在非空译文时仍可按人工意见重翻）。
func (s *Store) TicketRejectSourceMigrate() {
	_ = db.EnsureColumns(s.db, db.CurrentDialect(), "tickets", map[string]string{
		"reject_source": "TEXT NOT NULL DEFAULT ''",
	})
}

// SetTicketQualityFlagged 标记工单「质检存疑」（★ 改造 4）。
// 单向置 1，不回置 0——评估不达标是既成事实，不因同单其他语言达标而撤销人工复核提示。
// 不改 updated_at：本字段为质检元数据，不参与保留期/排序口径。
// 参数：id=工单 ID。返回错误。
func (s *Store) SetTicketQualityFlagged(id int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tickets SET quality_flagged=1 WHERE id=?", id)
	return err
}

// SetTicketQASummary 写入工单质检摘要（★ 改造 5：error/warning 计数，列表行徽标）。
// 参数：id=工单 ID；errors=error 级问题数；warnings=warning 级提示数。返回错误。
func (s *Store) SetTicketQASummary(id int64, errors, warnings int) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tickets SET qa_errors=?, qa_warnings=? WHERE id=?", errors, warnings, id)
	return err
}

// TicketStates 查询工单状态轨迹（按版本升序）。
// 参数：ticketID=工单 ID；返回该工单全部步骤轨迹。
// 按 version 升序返回，前端据此画流程时间线；本查询刻意不取 started_at/duration_ms，
// 返回结构的这两个字段保持零值（耗时只在需要时另取，列表路径不消费）。
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
	err := row.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.RejectSource, &t.FinalResult, &t.ResultPath, &t.Mode, &t.TokensBilled, &t.APIUserID, &t.MaxLength, &t.Delivery, &t.TextResultPath, &t.QualityFlagged, &t.QAErrors, &t.QAWarnings, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTicketWithFiles 删除工单及其关联数据（文件记录/状态轨迹/产物文件）。
// 物理删除磁盘上的产物文件和上传文件（如果存在）。
// 参数：id=工单 ID，tid=租户 ID。返回错误。
// 实现要点：
//   - 先按 (id, tid) 取单：跨租户删除在这里直接返回错误，磁盘上一个字节都不动；
//   - 磁盘路径必须在删 DB 行**之前**收集完（行删掉就再也查不到 result_path 等了）；
//   - DB 按 ticket_files → ticket_state → tickets 逐条删且不包事务：任一步失败即返回，
//     再调一次本方法可继续收敛（已删的部分重删无副作用）；
//   - 文件删除放到 goroutine：一次工单可能挂多个大产物，同步删会拖住 HTTP 响应；
//     os.Remove 的失败在此不回报（目录树已被 DB 侧解引用，最多留些孤儿文件在盘上）。
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
	if t.TextResultPath != "" {
		diskPaths = append(diskPaths, t.TextResultPath)
	}
	tfs, _ := s.TicketFiles(id)
	for _, tf := range tfs {
		if tf.FilePath != "" {
			diskPaths = append(diskPaths, tf.FilePath)
		}
		if tf.ResultPath != "" {
			diskPaths = append(diskPaths, tf.ResultPath)
		}
		if tf.TextResultPath != "" { // ★ 双模式：纯文案兜底产物随工单一并清理
			diskPaths = append(diskPaths, tf.TextResultPath)
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
			RemoveEmptyArtifactDir(p) // ★ #65：产物子目录已空则回收（非空目录 os.Remove 必失败，无副作用）
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
// 状态条件直接写进 UPDATE，靠数据库完成「检查 + 修改」的原子化：
// 并发双取消时只有一个能拿到受影响行 1，另一个得 0 并收到「不在可取消状态」的业务错误，
// 不会出现后到者把已取消单再改一遍（进而重复投通知）。
// completed/approved 等终态不在允许集合内：结果已交付的单不允许靠取消抹掉计费记录。
// ⚠️ 只按 id 定位、不带 tenant_id：调用方必须先用自己的租户查询确认这张单属于当前租户。
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
//
// stale=0 的语义是「cut 取当前时刻」，即把所有仍是 in_progress 的工单一律重排——
// 这是 service.BootResume 的用法（上一进程必然已死，不存在误判活任务的风险），
// 周期巡检必须传真实陈旧阈值（20 分钟），否则会把正在跑的任务全打断成双跑。
func (s *Store) RequeueStalledTickets(stale time.Duration) (int64, error) {
	cut := time.Now().Add(-stale).Format(time.RFC3339)
	// ★ 2026-09-12 PG 方言修复：payload 取 ticket_id 的 JSON 表达式按方言生成
	// （原内联 json_extract 为 SQLite JSON1 专属，PG 下整条重排 SQL 报错，卡死工单永不自动重排）。
	d := db.CurrentDialect()
	q := `UPDATE tickets SET status='queued', updated_at=?
		 WHERE status='in_progress' AND updated_at < ?
		   AND NOT EXISTS (
		       SELECT 1 FROM jobs j
		       WHERE j.type='ticket_run' AND j.status='running'
		         AND ` + db.JSONTicketIDExpr(d, "j.payload") + ` = tickets.id)`
	res, err := db.Exec(s.db, d, q, time.Now().Format(time.RFC3339), cut)
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
