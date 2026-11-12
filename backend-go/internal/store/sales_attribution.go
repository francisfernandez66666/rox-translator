// ============ sales_attribution.go · 职责说明 ============
// 二期六包平移第三刀（attribution）的数据访问层：reply_attributions 与 pack_stats 两张表的
// 全部读写。★ 表映射已按方案 §2/§8.2 改形：外仓三个旧名统一换成
// package_id（一期产品/物料标识）/ version（版本）/ draft_id（sales_drafts 的 id）。
//
// 幂等形状＝「先 INSERT，撞 (tenant_id, message_id) 唯一键后经 db.IsUniqueViolation
// 双方言判定，再回落成只刷新快照的 UPDATE」；绝不先 COUNT 预检、不认任何冲突字符串，
// 并发下由唯一索引保证同一 message_id 恰好一条。
// 全部方法带显式 tid 或 Row 里的 TenantID，SELECT/UPDATE 一律带 tenant_id 条件，
// 分页与「取最近一条」都用参数化 LIMIT ?，不留写死档位。
// ================================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
)

// evalScoreUnknown 离线评分未知哨兵（-1；0 是合法得分，不能拿来代表未知）。
const evalScoreUnknown = -1

// attributionLatestLimit 「取最近一条」的扫描上限（作为参数传给 LIMIT ?，不写死在 SQL 里）。
const attributionLatestLimit = 1

// attributionDefaultPageSize 分页缺省档位（同理只作参数，不在 SQL 里写死数字）。
const attributionDefaultPageSize = 100

// ReplyAttributionRow 回复归因的持久化行。
// 终态列 ArrivedAt/DealtAt 保留但本层不写：到店/成交属后续自动化链路，一期只认可 inquiry。
type ReplyAttributionRow struct {
	ID             int64 `json:"id"`
	TenantID       int64 `json:"tenant_id"`
	MessageID      int64 `json:"message_id"`
	ConversationID int64 `json:"conversation_id"`
	CustomerID     int64 `json:"customer_id"`
	// ProductID 本轮答复归属的产品主键（四期 W1，§4.1.3-⑥）。
	// ★ 空串＝"未归因到产品"：存量行与"产品已删"同形处理，**不回填猜测**（猜错比空着坏）。
	// 开闸粒度是"按产品"，没有这一列就答不了"哪个产品带来了这一条询盘"，漏斗在库层面断掉。
	ProductID    string  `json:"product_id"`
	PackageID    string  `json:"package_id"`
	Version      string  `json:"version"`
	DraftID      string  `json:"draft_id"`
	AnchorType   int     `json:"anchor_type"`
	RouteResult  string  `json:"route_result"`
	Provider     string  `json:"provider"`
	ModelName    string  `json:"model_name"`
	IntentBefore float64 `json:"intent_before"`
	IntentAfter  float64 `json:"intent_after"`
	Hooked       int     `json:"hooked"`
	LeadCaptured int     `json:"lead_captured"`
	PendingHuman int     `json:"pending_human"`
	EvalScore    int     `json:"eval_score"`
	OutcomeStage string  `json:"outcome_stage"`
	ArrivedAt    string  `json:"arrived_at"`
	DealtAt      string  `json:"dealt_at"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// PackStatRow 素材效果聚合快照行（9 个聚合列由 SyncPackStats 现算）。
type PackStatRow struct {
	TenantID         int64   `json:"tenant_id"`
	PackageID        string  `json:"package_id"`
	Version          string  `json:"version"`
	DraftID          string  `json:"draft_id"`
	SampleCount      int64   `json:"sample_count"`
	HookRate         float64 `json:"hook_rate"`
	LeadRate         float64 `json:"lead_rate"`
	PendingHumanRate float64 `json:"pending_human_rate"`
	ArriveRate       float64 `json:"arrive_rate"`
	DealRate         float64 `json:"deal_rate"`
	AvgIntentDelta   float64 `json:"avg_intent_delta"`
	AvgEvalScore     float64 `json:"avg_eval_score"`
	ComputedAt       string  `json:"computed_at"`
}

// RecordReply 幂等写一条回复归因；同一 (tenant_id, message_id) 重复时只刷新快照、不重复计数，
// 也不动 eval_score、事件标记与终态列。
// ★ 三期 W2：pending_human 随 INSERT 按 message_id 精确落行（assist 回避轮的主服务回写）；
// 它是事件标记，幂等命中分支刻意不刷新——重复回写不许把已清零的标记重新立起。
// 参数：row=归因行（TenantID/MessageID 必须有效）；返回：写库错误。
func (s *Store) RecordReply(row ReplyAttributionRow) error {
	if row.TenantID <= 0 || row.MessageID <= 0 {
		return errors.New("归因记录必须带有效租户与消息 ID")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO reply_attributions
		 (tenant_id, message_id, conversation_id, customer_id, product_id,
		  package_id, version, draft_id, anchor_type, route_result, provider, model,
		  intent_before, intent_after, eval_score, outcome_stage, pending_human, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.TenantID, row.MessageID, row.ConversationID, row.CustomerID, row.ProductID,
		row.PackageID, row.Version, row.DraftID, row.AnchorType, row.RouteResult, row.Provider, row.ModelName,
		row.IntentBefore, row.IntentAfter, evalScoreUnknown, row.OutcomeStage, row.PendingHuman, now, now)
	if err == nil {
		return nil
	}
	if !db.IsUniqueViolation(err) {
		return fmt.Errorf("归因记录落库失败: %w", err)
	}
	// 幂等命中：只刷新快照列与 updated_at；eval_score、事件标记、终态列一律不动。
	// ★ 四期 W1：product_id 用 CASE 保护——同一条 message 的第二次回写可能来自无作用域的路径
	//   （历史回放 / 特征接口），直接覆盖会把漏斗维度静默洗成空串（AGENTS §一·11「读数为失败背书」）；
	//   反过来，第一次没写上的行允许后续补上（`? <> ''` 那一支）。
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`UPDATE reply_attributions
		 SET conversation_id=?, customer_id=?,
		     product_id = CASE WHEN ? = '' THEN product_id ELSE ? END,
		     package_id=?, version=?, draft_id=?,
		     anchor_type=?, route_result=?, provider=?, model=?,
		     intent_before=?, intent_after=?, outcome_stage=?, updated_at=?
		 WHERE tenant_id=? AND message_id=?`,
		row.ConversationID, row.CustomerID, row.ProductID, row.ProductID,
		row.PackageID, row.Version, row.DraftID,
		row.AnchorType, row.RouteResult, row.Provider, row.ModelName,
		row.IntentBefore, row.IntentAfter, row.OutcomeStage, now,
		row.TenantID, row.MessageID)
	if err != nil {
		return fmt.Errorf("归因记录幂等刷新失败: %w", err)
	}
	return nil
}

// MarkHookedBeforeMessage 标记锚点消息之前最近一条已归因回复已接钩。
// 参数：tid=租户；conversationID=会话；customerMessageID=锚点客户消息（>0 时只取其之前的行）；
// 返回：写库错误（无目标行时静默跳过，与外仓语义一致）。
func (s *Store) MarkHookedBeforeMessage(tid, conversationID, customerMessageID int64) error {
	if tid <= 0 || conversationID <= 0 {
		return errors.New("接钩回填必须带有效租户与会话")
	}
	q := `SELECT id FROM reply_attributions WHERE tenant_id=? AND conversation_id=? AND hooked = 0`
	args := []interface{}{tid, conversationID}
	if customerMessageID > 0 {
		q += ` AND message_id < ?`
		args = append(args, customerMessageID)
	}
	q += ` ORDER BY message_id DESC LIMIT ?`
	args = append(args, attributionLatestLimit)
	var id int64
	err := db.QueryRow(s.db, db.CurrentDialect(), q, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("接钩定位失败: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`UPDATE reply_attributions SET hooked=1, updated_at=? WHERE tenant_id=? AND id=?`,
		now, tid, id)
	if err != nil {
		return fmt.Errorf("接钩回填失败: %w", err)
	}
	return nil
}

// MarkLeadCaptured 标记该会话/客户最近一条已归因回复带来留资。
// 参数：tid=租户；conversationID=会话；customerID=客户；返回：写库错误。
func (s *Store) MarkLeadCaptured(tid, conversationID, customerID int64) error {
	return s.markLatestFlag(tid, conversationID, customerID, "lead_captured")
}

// MarkPendingHuman 标记该会话/客户最近一条已归因回复触发待人工接管。
// 参数：tid=租户；conversationID=会话；customerID=客户；返回：写库错误。
func (s *Store) MarkPendingHuman(tid, conversationID, customerID int64) error {
	return s.markLatestFlag(tid, conversationID, customerID, "pending_human")
}

// markLatestFlag 回填最近一条归因记录的指定事件标记列。
// column 只允许两个内部列名（白名单），杜绝拼接注入；值一律参数化。
// 参数：tid=租户；conversationID/customerID=可选定位（至少一个 >0）；column=内部列名。
// 返回：写库错误。
func (s *Store) markLatestFlag(tid, conversationID, customerID int64, column string) error {
	if tid <= 0 || (conversationID <= 0 && customerID <= 0) {
		return errors.New("事件回填必须带有效租户与会话/客户")
	}
	if column != "lead_captured" && column != "pending_human" {
		return errors.New("事件回填列名非法")
	}
	q := `SELECT id FROM reply_attributions WHERE tenant_id=? AND draft_id <> '' AND ` + column + ` = 0`
	args := []interface{}{tid}
	if conversationID > 0 {
		q += ` AND conversation_id = ?`
		args = append(args, conversationID)
	}
	if customerID > 0 {
		q += ` AND customer_id = ?`
		args = append(args, customerID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, attributionLatestLimit)
	var id int64
	err := db.QueryRow(s.db, db.CurrentDialect(), q, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("事件回填定位失败: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`UPDATE reply_attributions SET `+column+` = 1, updated_at=? WHERE tenant_id=? AND id=?`,
		now, tid, id)
	if err != nil {
		return fmt.Errorf("事件回填失败: %w", err)
	}
	return nil
}

// MarkOutcome 回填单条归因记录的终局档（数据层只校验非空；档位白名单在引擎层收口）。
// 参数：tid=租户；messageID=消息；stage=终局档（非空）；返回：写库错误。
func (s *Store) MarkOutcome(tid, messageID int64, stage string) error {
	if tid <= 0 || messageID <= 0 || strings.TrimSpace(stage) == "" {
		return errors.New("终局回填必须带有效租户、消息 ID 与非空终局档")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE reply_attributions SET outcome_stage=?, updated_at=? WHERE tenant_id=? AND message_id=?`,
		stage, now, tid, messageID)
	if err != nil {
		return fmt.Errorf("终局回填失败: %w", err)
	}
	return nil
}

// SyncPackStats 从 reply_attributions 现算该租户的素材效果聚合，幂等物化进 pack_stats。
// 参数：tid=租户；返回：写库错误。
func (s *Store) SyncPackStats(tid int64) error {
	if tid <= 0 {
		return errors.New("素材聚合必须带有效租户")
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT package_id, version, draft_id,
		        COUNT(*),
		        COALESCE(AVG(CASE WHEN hooked = 1 THEN 1.0 ELSE 0.0 END), 0),
		        COALESCE(AVG(CASE WHEN lead_captured = 1 THEN 1.0 ELSE 0.0 END), 0),
		        COALESCE(AVG(CASE WHEN pending_human = 1 THEN 1.0 ELSE 0.0 END), 0),
		        COALESCE(AVG(CASE WHEN arrived_at <> '' THEN 1.0 ELSE 0.0 END), 0),
		        COALESCE(AVG(CASE WHEN dealt_at <> '' THEN 1.0 ELSE 0.0 END), 0),
		        COALESCE(AVG(intent_after - intent_before), 0),
		        COALESCE(AVG(CASE WHEN eval_score >= 0 THEN eval_score ELSE NULL END), 0)
		   FROM reply_attributions
		  WHERE tenant_id = ? AND draft_id <> ''
		  GROUP BY package_id, version, draft_id`, tid)
	if err != nil {
		return fmt.Errorf("素材聚合现算失败: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	var stats []PackStatRow
	for rows.Next() {
		var r PackStatRow
		r.TenantID, r.ComputedAt = tid, now
		if err := rows.Scan(&r.PackageID, &r.Version, &r.DraftID,
			&r.SampleCount, &r.HookRate, &r.LeadRate, &r.PendingHumanRate,
			&r.ArriveRate, &r.DealRate, &r.AvgIntentDelta, &r.AvgEvalScore); err != nil {
			return fmt.Errorf("素材聚合读数失败: %w", err)
		}
		stats = append(stats, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("素材聚合遍历失败: %w", err)
	}
	// 先收干净读游标再对同库发写，避免读事务悬挂时抢写锁。
	if err := rows.Close(); err != nil {
		return fmt.Errorf("素材聚合游标关闭失败: %w", err)
	}
	for _, r := range stats {
		if err := s.upsertPackStat(tid, r); err != nil {
			return err
		}
	}
	return nil
}

// upsertPackStat 幂等写一条素材快照：先 INSERT，撞唯一键经 db.IsUniqueViolation 判定后转 UPDATE。
// 参数：tid=租户；row=聚合快照；返回：写库错误。
func (s *Store) upsertPackStat(tid int64, row PackStatRow) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO pack_stats
		 (tenant_id, package_id, version, draft_id, sample_count, hook_rate, lead_rate,
		  pending_human_rate, arrive_rate, deal_rate, avg_intent_delta, avg_eval_score, computed_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		tid, row.PackageID, row.Version, row.DraftID, row.SampleCount, row.HookRate, row.LeadRate,
		row.PendingHumanRate, row.ArriveRate, row.DealRate, row.AvgIntentDelta, row.AvgEvalScore, row.ComputedAt)
	if err == nil {
		return nil
	}
	if !db.IsUniqueViolation(err) {
		return fmt.Errorf("素材快照落库失败: %w", err)
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`UPDATE pack_stats
		 SET sample_count=?, hook_rate=?, lead_rate=?, pending_human_rate=?,
		     arrive_rate=?, deal_rate=?, avg_intent_delta=?, avg_eval_score=?, computed_at=?
		 WHERE tenant_id=? AND package_id=? AND version=? AND draft_id=?`,
		row.SampleCount, row.HookRate, row.LeadRate, row.PendingHumanRate,
		row.ArriveRate, row.DealRate, row.AvgIntentDelta, row.AvgEvalScore, row.ComputedAt,
		tid, row.PackageID, row.Version, row.DraftID)
	if err != nil {
		return fmt.Errorf("素材快照幂等刷新失败: %w", err)
	}
	return nil
}

// ListReplyAttributions 分页读取某租户的归因记录（LIMIT ? OFFSET ? 全参数化）。
// 参数：tid=租户；limit/offset=分页（limit<=0 用缺省档，offset<0 归零）；返回：归因行与错误。
func (s *Store) ListReplyAttributions(tid, limit, offset int64) ([]ReplyAttributionRow, error) {
	if tid <= 0 {
		return nil, errors.New("归因列表必须带有效租户")
	}
	if limit <= 0 {
		limit = attributionDefaultPageSize
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id, tenant_id, message_id, conversation_id, customer_id, product_id,
		        package_id, version, draft_id, anchor_type, route_result, provider, model,
		        intent_before, intent_after, hooked, lead_captured, pending_human,
		        eval_score, outcome_stage, arrived_at, dealt_at, created_at, updated_at
		   FROM reply_attributions WHERE tenant_id=? ORDER BY id ASC LIMIT ? OFFSET ?`,
		tid, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("归因列表读取失败: %w", err)
	}
	defer rows.Close()
	out := make([]ReplyAttributionRow, 0, 16)
	for rows.Next() {
		var r ReplyAttributionRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.MessageID, &r.ConversationID, &r.CustomerID, &r.ProductID,
			&r.PackageID, &r.Version, &r.DraftID, &r.AnchorType, &r.RouteResult, &r.Provider, &r.ModelName,
			&r.IntentBefore, &r.IntentAfter, &r.Hooked, &r.LeadCaptured, &r.PendingHuman,
			&r.EvalScore, &r.OutcomeStage, &r.ArrivedAt, &r.DealtAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("归因列表读数失败: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetPackStats 按 (tenant_id, package_id, version, draft_id) 读取一条素材快照（不存在返回 nil,nil）。
// 参数：tid=租户；packageID/version/draftID=维度；返回：快照与错误。
func (s *Store) GetPackStats(tid int64, packageID, version, draftID string) (*PackStatRow, error) {
	if tid <= 0 || strings.TrimSpace(draftID) == "" {
		return nil, errors.New("素材快照查询必须带有效租户与素材 ID")
	}
	row := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT tenant_id, package_id, version, draft_id, sample_count, hook_rate, lead_rate,
		        pending_human_rate, arrive_rate, deal_rate, avg_intent_delta, avg_eval_score, computed_at
		   FROM pack_stats WHERE tenant_id=? AND package_id=? AND version=? AND draft_id=?`,
		tid, packageID, version, draftID)
	var r PackStatRow
	err := row.Scan(&r.TenantID, &r.PackageID, &r.Version, &r.DraftID, &r.SampleCount,
		&r.HookRate, &r.LeadRate, &r.PendingHumanRate, &r.ArriveRate, &r.DealRate,
		&r.AvgIntentDelta, &r.AvgEvalScore, &r.ComputedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("素材快照读取失败: %w", err)
	}
	return &r, nil
}
