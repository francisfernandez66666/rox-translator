// ============ systemconfig.go · 职责说明 ============
// store 包系统配置（system_config 表）数据访问层。
// KV 型配置的读写与流程步骤启停配置。
// 支持热更新：admin 可通过 API 保存 flow_steps（JSON）来控制流程引擎各步骤开关；
// GetFlowSteps 会把已保存配置与默认定义合并，StepEnabled 提供快捷判断。
// =============================================
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"translator/internal/db"
)

// SysConfig 系统配置项（DB 持久化 + 内存缓存）
type SysConfig struct {
	Key       string `json:"key"`        // 配置键名
	Value     string `json:"value"`      // 配置值
	UpdatedAt string `json:"updated_at"` // 最近更新时间（RFC3339 字符串）
}

// GetConfig 读取配置项；不存在返回空字符串。
// 参数：key=配置键名；返回配置值（无记录时返回 "" 且无错误）。
func (s *Store) GetConfig(key string) (string, error) {
	var v string
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT value FROM system_config WHERE key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil // 键不存在按空串处理
	}
	return v, err
}

// SetConfig 写入配置项（存在则更新，不存在则插入，upsert）。
// 参数：key=配置键名，value=配置值；返回错误。
func (s *Store) SetConfig(key, value string) error {
	// ON CONFLICT(key) 实现 upsert，保证键唯一
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO system_config (key, value, updated_at) VALUES (?,?,?) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
		key, value, time.Now().Format(time.RFC3339))
	return err
}

// AllConfigs 返回全部配置项（按键名排序）。
// 返回：配置项切片。
func (s *Store) AllConfigs() ([]SysConfig, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT key, value, COALESCE(updated_at,'') FROM system_config ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SysConfig
	for rows.Next() {
		var c SysConfig
		if err := rows.Scan(&c.Key, &c.Value, &c.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, c)
	}
	return out, nil
}

// FlowStep 流程步骤配置（流程引擎步骤启停）
type FlowStep struct {
	Key    string `json:"key"`    // 步骤标识（如 kb_match / ai_initial / gate）
	Name   string `json:"name"`   // 步骤中文名
	Enable bool   `json:"enable"` // 是否启用
}

// DefaultFlowSteps 默认流程步骤定义（key → 中文名）
var DefaultFlowSteps = []FlowStep{
	{Key: "kb_match", Name: "知识库匹配", Enable: true},
	{Key: "ai_initial", Name: "AI 初翻", Enable: true},
	{Key: "evals_initial", Name: "初翻评估", Enable: true},
	{Key: "review", Name: "审校 Agent", Enable: true},
	{Key: "evals_review", Name: "审校评估", Enable: true},
	{Key: "gate", Name: "ConstraintGate 硬校验", Enable: true},
	{Key: "culture_gate", Name: "语言文化包输出闸门", Enable: true},
	{Key: "qa", Name: "QA 确定性检查", Enable: true},
	{Key: "approval", Name: "人工审批", Enable: true},
	{Key: "feedback", Name: "自迭代写库", Enable: true},
}

// GetFlowSteps 读取流程步骤启停配置（合并默认定义：仅覆盖已保存步骤的 Enable）。
// 返回：完整步骤列表（按默认定义顺序）。
func (s *Store) GetFlowSteps() ([]FlowStep, error) {
	raw, err := s.GetConfig("flow_steps")
	var saved []FlowStep
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &saved) // 解析已保存的 JSON 配置
	}
	byKey := map[string]bool{}
	for _, st := range saved {
		byKey[st.Key] = st.Enable // 建索引：key → 是否启用
	}
	out := make([]FlowStep, 0, len(DefaultFlowSteps))
	for _, d := range DefaultFlowSteps {
		// 以默认定义为主干，命中已保存配置则覆盖 Enable
		if en, ok := byKey[d.Key]; ok {
			d.Enable = en
		}
		out = append(out, d)
	}
	return out, nil
}

// SaveFlowSteps 保存流程步骤启停配置（整体覆盖写入 flow_steps 配置项）。
// 参数：steps=完整步骤列表；返回错误。
func (s *Store) SaveFlowSteps(steps []FlowStep) error {
	data, _ := json.Marshal(steps)
	return s.SetConfig("flow_steps", string(data))
}

// StepEnabled 快捷判断某步骤是否启用（admin 可关）。
// 参数：key=步骤标识；返回是否启用（读取失败默认视为启用，保证容错）。
func (s *Store) StepEnabled(key string) bool {
	steps, err := s.GetFlowSteps()
	if err != nil {
		return true // 读取异常时默认启用，避免误停流程
	}
	for _, st := range steps {
		if st.Key == key {
			return st.Enable
		}
	}
	return true // 未知步骤默认启用
}

// ============ 租户 QPS/并发配额持久化（2026-08-26 全仓评审 C2） ============

// TenantQuotaCfg 租户限流配额（QPS / 并发上限）。
// 此前仅写进程内存（重启即回默认 10/3），现落 system_config 持久化。
type TenantQuotaCfg struct {
	QPS        int `json:"qps"`        // 每秒请求数上限
	Concurrent int `json:"concurrent"` // 并发请求数上限
}

// tenantQuotaKey 生成租户配额的配置键。
func tenantQuotaKey(tid int64) string { return fmt.Sprintf("tenant_quota_%d", tid) }

// SaveTenantQuotaCfg 持久化租户限流配额（upsert）。
func (s *Store) SaveTenantQuotaCfg(tid int64, qps, concurrent int) error {
	b, _ := json.Marshal(TenantQuotaCfg{QPS: qps, Concurrent: concurrent})
	return s.SetConfig(tenantQuotaKey(tid), string(b))
}

// LoadTenantQuotaConfigs 加载全部租户限流配额（服务启动时回放用）。
// 返回：map[租户ID]=配额；解析失败的行跳过。
func (s *Store) LoadTenantQuotaConfigs() map[int64]TenantQuotaCfg {
	out := map[int64]TenantQuotaCfg{}
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT key, value FROM system_config WHERE key LIKE 'tenant_quota_%'")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		tidStr := strings.TrimPrefix(k, "tenant_quota_")
		tid, perr := strconv.ParseInt(tidStr, 10, 64)
		if perr != nil || tid <= 0 {
			continue
		}
		var cfg TenantQuotaCfg
		if json.Unmarshal([]byte(v), &cfg) == nil && cfg.QPS > 0 {
			out[tid] = cfg
		}
	}
	return out
}
