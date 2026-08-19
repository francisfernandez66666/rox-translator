package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// SysConfig 系统配置项（DB 持久化 + 内存缓存）
type SysConfig struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// GetConfig 读取配置项；不存在返回空字符串
func (s *Store) GetConfig(key string) (string, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM system_config WHERE key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetConfig 写入配置项
func (s *Store) SetConfig(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO system_config (key, value, updated_at) VALUES (?,?,?) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
		key, value, time.Now().Format(time.RFC3339))
	return err
}

// AllConfigs 返回全部配置
func (s *Store) AllConfigs() ([]SysConfig, error) {
	rows, err := s.db.Query("SELECT key, value, COALESCE(updated_at,'') FROM system_config ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SysConfig
	for rows.Next() {
		var c SysConfig
		if err := rows.Scan(&c.Key, &c.Value, &c.UpdatedAt); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// FlowStep 流程步骤配置（流程引擎步骤启停）
type FlowStep struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Enable bool   `json:"enable"`
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
	{Key: "approval", Name: "人工审批", Enable: true},
	{Key: "feedback", Name: "自迭代写库", Enable: true},
}

// GetFlowSteps 读取流程步骤启停配置（合并默认定义）
func (s *Store) GetFlowSteps() ([]FlowStep, error) {
	raw, err := s.GetConfig("flow_steps")
	var saved []FlowStep
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &saved)
	}
	byKey := map[string]bool{}
	for _, st := range saved {
		byKey[st.Key] = st.Enable
	}
	out := make([]FlowStep, 0, len(DefaultFlowSteps))
	for _, d := range DefaultFlowSteps {
		if en, ok := byKey[d.Key]; ok {
			d.Enable = en
		}
		out = append(out, d)
	}
	return out, nil
}

// SaveFlowSteps 保存流程步骤启停配置
func (s *Store) SaveFlowSteps(steps []FlowStep) error {
	data, _ := json.Marshal(steps)
	return s.SetConfig("flow_steps", string(data))
}

// StepEnabled 快捷判断某步骤是否启用（admin 可关）
func (s *Store) StepEnabled(key string) bool {
	steps, err := s.GetFlowSteps()
	if err != nil {
		return true
	}
	for _, st := range steps {
		if st.Key == key {
			return st.Enable
		}
	}
	return true
}