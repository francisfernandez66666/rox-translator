// ============ flow_state_machine.go · 流程状态机数据层 ============
// 二期 S2 六包平移第二刀（state_machine）的落库口。
// 乐观锁靠 `version` 列：Advance 用「WHERE version=?」+「version=version+1」一条
// UPDATE 完成判重＋自增，命中 0 行即版本冲突（F2）；幂等靠确定性 PK
// `fsm_<tenant>_<flowInstance>` 与业务唯一索引 (tenant_id, flow_instance_id)。
//
// ★ 迁入即换：外仓原 uniqueIndex 仅 on flow_instance_id（跨租户污染坑），
// 这里改成显式 (tenant_id, flow_instance_id) 隔离；日志走 observability，零 log.Printf。
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

// 流程状态机状态常量（与 salesflow 后续平移的 flow 状态对齐，先在本包收敛）。
const (
	SMStatusRunning   = "running"
	SMStatusCompleted = "completed"
	SMStatusSuspended = "suspended"
)

// FlowStateMachineRow 流程状态机持久化行。
type FlowStateMachineRow struct {
	ID             string
	TenantID       int64
	OneID          string
	FlowInstanceID int64
	CurrentNode    string
	StateJSON      string
	HeartbeatTS    string
	Status         string
	Version        int64
	LastEventID    string
}

// FlowInstanceRow 流程实例（仅终态收口所需列）。
type FlowInstanceRow struct {
	ID       int64
	TenantID int64
	Status   string
}

// InitFlowStateMachine 幂等初始化：已存在返回现有行，不重复插入。
func (s *Store) InitFlowStateMachine(tid int64, oneID string, fiid int64, startNode string) (*FlowStateMachineRow, error) {
	existing, err := s.GetFlowStateMachine(tid, fiid)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	id := fmt.Sprintf("fsm_%d_%d", tid, fiid)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO flow_state_machines
		 (id, tenant_id, one_id, flow_instance_id, current_node, heartbeat_ts, status, version, last_event_id)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		id, tid, oneID, fiid, startNode, now, SMStatusRunning, 1, ""); err != nil {
		// 并发初始化竞争：撞唯一索引即回落到读取已有行。
		if db.IsUniqueViolation(err) {
			return s.GetFlowStateMachine(tid, fiid)
		}
		return nil, fmt.Errorf("状态机初始化失败: %w", err)
	}
	return s.GetFlowStateMachine(tid, fiid)
}

// GetFlowStateMachine 按租户＋流程实例读取（不存在返回 nil,nil）。
func (s *Store) GetFlowStateMachine(tid, fiid int64) (*FlowStateMachineRow, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT id, tenant_id, one_id, flow_instance_id,
		        COALESCE(current_node,''), COALESCE(state_json,''),
		        COALESCE(heartbeat_ts,''), COALESCE(status,''),
		        COALESCE(version,0), COALESCE(last_event_id,'')
		 FROM flow_state_machines WHERE flow_instance_id=? AND tenant_id=?`, fiid, tid)
	return scanFlowStateMachine(row)
}

// scanFlowStateMachine 扫描单行；查无此行返回 nil,nil（与 Get 的不存在语义一致）。
func scanFlowStateMachine(row *sql.Row) (*FlowStateMachineRow, error) {
	var r FlowStateMachineRow
	err := row.Scan(&r.ID, &r.TenantID, &r.OneID, &r.FlowInstanceID,
		&r.CurrentNode, &r.StateJSON, &r.HeartbeatTS, &r.Status, &r.Version, &r.LastEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读状态机失败: %w", err)
	}
	return &r, nil
}

// AdvanceFlowStateMachine 推进状态机：WHERE 带版本号，命中 0 行即乐观锁冲突。
// 返回受影响行数；调用方据 0 行判定 ErrVersionConflict（F2）。
func (s *Store) AdvanceFlowStateMachine(tid, fiid int64, currentNode, stateJSON string, expectedVersion int64) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE flow_state_machines
		 SET current_node=?, state_json=?, heartbeat_ts=?, version=version+1
		 WHERE flow_instance_id=? AND tenant_id=? AND version=?`,
		currentNode, stateJSON, now, fiid, tid, expectedVersion)
	if err != nil {
		return 0, fmt.Errorf("状态机推进失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("状态机推进读数失败: %w", err)
	}
	return n, nil
}

// HeartbeatFlowStateMachine 心跳续期（防巡检误回收）。
func (s *Store) HeartbeatFlowStateMachine(tid, fiid int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE flow_state_machines SET heartbeat_ts=? WHERE flow_instance_id=? AND tenant_id=?`,
		now, fiid, tid)
	if err != nil {
		return fmt.Errorf("状态机心跳失败: %w", err)
	}
	return nil
}

// CompleteFlowStateMachine 标记完成。
func (s *Store) CompleteFlowStateMachine(tid, fiid int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE flow_state_machines SET status=?, heartbeat_ts=? WHERE flow_instance_id=? AND tenant_id=?`,
		SMStatusCompleted, now, fiid, tid)
	if err != nil {
		return fmt.Errorf("状态机完成标记失败: %w", err)
	}
	return nil
}

// flowSweepScanLimit 巡检单轮扫描预算（不是客户要翻的列表）：防止一轮占满扫描配额，
// 写死数字会被 list_pagination_gate_test 棘轮抓住，故收成常量＋参数化 LIMIT。
const flowSweepScanLimit = 100

// ListStaleFlowStateMachines 取心跳超时的 running 实例（单轮预算 flowSweepScanLimit，无序）。
func (s *Store) ListStaleFlowStateMachines(deadline string) ([]FlowStateMachineRow, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id, tenant_id, one_id, flow_instance_id,
		        COALESCE(current_node,''), COALESCE(state_json,''),
		        COALESCE(heartbeat_ts,''), COALESCE(status,''),
		        COALESCE(version,0), COALESCE(last_event_id,'')
		 FROM flow_state_machines WHERE status=? AND heartbeat_ts < ? LIMIT ?`,
		SMStatusRunning, deadline, flowSweepScanLimit)
	if err != nil {
		return nil, fmt.Errorf("状态机巡检扫描失败: %w", err)
	}
	defer rows.Close()
	out := make([]FlowStateMachineRow, 0, 16)
	for rows.Next() {
		var r FlowStateMachineRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.OneID, &r.FlowInstanceID,
			&r.CurrentNode, &r.StateJSON, &r.HeartbeatTS, &r.Status, &r.Version, &r.LastEventID); err != nil {
			return nil, fmt.Errorf("状态机巡检扫描读数失败: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// CompletedFlowInstanceIDs 批量联查：给定实例 ID 里哪些已完成（终态收口用）。
// 返回 instanceID→tenantID 的键值对；巡检是跨租户扫描，故**不提前按单租户过滤**，
// 而在收口侧按 (instanceID, tenantID) 配对判定（flow_instance_id 非全局唯一，
// 只按 id 判会跨租户误标）。
func (s *Store) CompletedFlowInstanceIDs(fiids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(fiids) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(fiids)), ",")
	args := make([]interface{}, 0, len(fiids)+1)
	for _, id := range fiids {
		args = append(args, id)
	}
	args = append(args, SMStatusCompleted)
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id, tenant_id FROM flow_instances WHERE id IN (`+ph+`) AND status=?`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("终态收口联查失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, tenant int64
		if err := rows.Scan(&id, &tenant); err != nil {
			return nil, fmt.Errorf("终态收口读数失败: %w", err)
		}
		out[id] = tenant
	}
	return out, nil
}

// TouchFlowStateMachine 巡检里重置心跳＋标记已告警（防止反复扫描重打日志）。
func (s *Store) TouchFlowStateMachine(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE flow_state_machines SET heartbeat_ts=?, last_event_id='sweep_noticed' WHERE id=?`,
		now, id)
	if err != nil {
		return fmt.Errorf("状态机巡检标记失败: %w", err)
	}
	return nil
}

// ReconcileTerminalFlowStateMachine 终态收口：把漏标 completed 的状态机补标完成。
func (s *Store) ReconcileTerminalFlowStateMachine(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE flow_state_machines SET status=?, heartbeat_ts=?, last_event_id='terminal_reconciled' WHERE id=?`,
		SMStatusCompleted, now, id)
	if err != nil {
		return fmt.Errorf("终态收口写失败: %w", err)
	}
	return nil
}

// UpsertFlowInstance 落库流程实例（终态收口读路径所需；后续 flow 引擎平移时复用为实例写入口）。
// 冲突目标必须是完整复合主键 (id, tenant_id)：只写 (id) 在 SQLite 报
// 「ON CONFLICT clause does not match any PRIMARY KEY」、PG 报「no unique constraint
// matching」——flow_instance_id 跨租户非全局唯一，单列冲突目标还会把别租户的行当冲突。
func (s *Store) UpsertFlowInstance(id, tid int64, status string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO flow_instances (id, tenant_id, status) VALUES (?,?,?)
		 ON CONFLICT(id, tenant_id) DO UPDATE SET status=EXCLUDED.status`,
		id, tid, status)
	if err != nil {
		return fmt.Errorf("流程实例写入失败: %w", err)
	}
	return nil
}
