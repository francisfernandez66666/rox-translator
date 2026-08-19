// Package tenant 提供多租户支持：租户管理、租户注入、租户有效性校验。
package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Tenant 租户实体
type Tenant struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Status      string `json:"status"`       // active / disabled / expired
	ExpiresAt   string `json:"expires_at"`   // 有效期(空=永久), 格式 2006-01-02 或 RFC3339
	Permissions string `json:"permissions"`  // JSON 权限字符串
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Perms 租户权限（Permissions JSON 的解码结构）
type Perms struct {
	Langs          []string `json:"langs,omitempty"`           // 允许翻译的语言代码；空=全部
	MaxDailyChars  int64    `json:"max_daily_chars,omitempty"` // 每日字符上限；0=不限
}

// 租户状态常量
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusExpired  = "expired"
)

// Store 租户存储（基于 SQLite）
type Store struct {
	db *sql.DB
}

// NewStore 创建租户存储并建表
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.ensureTable(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ensureTable() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		"code" TEXT UNIQUE NOT NULL,
		"name" TEXT NOT NULL DEFAULT '',
		"status" TEXT NOT NULL DEFAULT 'active',
		"expires_at" TEXT NOT NULL DEFAULT '',
		"permissions" TEXT NOT NULL DEFAULT '{}',
		"created_at" TEXT,
		"updated_at" TEXT
	)`); err != nil {
		return err
	}
	// 兼容旧库：补充租户级配置列（model/policy/flow，均为 JSON）
	rows, err := s.db.Query("PRAGMA table_info(tenants)")
	if err != nil {
		return err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt *string
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		have[name] = true
	}
	if !have["model_config"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN model_config TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
	}
	if !have["policy_config"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN policy_config TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
	}
	if !have["flow_config"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN flow_config TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
	}
	return nil
}

func nowStr() string {
	return time.Now().Format(time.RFC3339)
}

// EnsureDefault 确保默认租户 rox 存在；不存在则创建并返回其 id
func (s *Store) EnsureDefault() (int64, error) {
	row := s.db.QueryRow("SELECT id FROM tenants WHERE code='rox'")
	var id int64
	if err := row.Scan(&id); err == nil {
		return id, nil
	}
	now := nowStr()
	res, err := s.db.Exec(
		"INSERT INTO tenants (code, name, status, expires_at, permissions, created_at, updated_at) VALUES ('rox','ROX 默认租户','active','','{}',?,?)",
		now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Create 创建租户
func (s *Store) Create(code, name, expiresAt, permissions string) (*Tenant, error) {
	if code == "" {
		return nil, errCodeEmpty
	}
	now := nowStr()
	if permissions == "" {
		permissions = "{}"
	}
	res, err := s.db.Exec(
		"INSERT INTO tenants (code, name, status, expires_at, permissions, created_at, updated_at) VALUES (?,?,'active',?,?,?,?)",
		code, name, expiresAt, permissions, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetByID(id)
}

// List 列出所有租户
func (s *Store) List() ([]*Tenant, error) {
	rows, err := s.db.Query("SELECT id, code, name, status, expires_at, permissions, created_at, updated_at FROM tenants ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Code, &t.Name, &t.Status, &t.ExpiresAt, &t.Permissions, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue
		}
		t.Status = effectiveStatus(t.Status, t.ExpiresAt)
		out = append(out, &t)
	}
	return out, nil
}

// GetByID 按 id 查询
func (s *Store) GetByID(id int64) (*Tenant, error) {
	row := s.db.QueryRow("SELECT id, code, name, status, expires_at, permissions, created_at, updated_at FROM tenants WHERE id=?", id)
	var t Tenant
	if err := row.Scan(&t.ID, &t.Code, &t.Name, &t.Status, &t.ExpiresAt, &t.Permissions, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Status = effectiveStatus(t.Status, t.ExpiresAt)
	return &t, nil
}

// GetByCode 按租户编码查询
func (s *Store) GetByCode(code string) (*Tenant, error) {
	row := s.db.QueryRow("SELECT id, code, name, status, expires_at, permissions, created_at, updated_at FROM tenants WHERE code=?", code)
	var t Tenant
	if err := row.Scan(&t.ID, &t.Code, &t.Name, &t.Status, &t.ExpiresAt, &t.Permissions, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Status = effectiveStatus(t.Status, t.ExpiresAt)
	return &t, nil
}

// Update 更新租户（名称、有效期、权限；状态单独由 SetStatus 控制）
func (s *Store) Update(id int64, name, expiresAt, permissions string) error {
	if permissions == "" {
		permissions = "{}"
	}
	_, err := s.db.Exec(
		"UPDATE tenants SET name=?, expires_at=?, permissions=?, updated_at=? WHERE id=?",
		name, expiresAt, permissions, nowStr(), id)
	return err
}

// SetStatus 设置租户状态（active / disabled）
func (s *Store) SetStatus(id int64, status string) error {
	_, err := s.db.Exec(
		"UPDATE tenants SET status=?, updated_at=? WHERE id=?",
		status, nowStr(), id)
	return err
}

// ModelConfig 租户级模型配置
type ModelConfig struct {
	APIBase string `json:"api_base"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// GetModelConfig 读取租户模型配置；未配置返回空值（调用方回退全局默认）
func (s *Store) GetModelConfig(tid int64) (ModelConfig, error) {
	var raw string
	err := s.db.QueryRow("SELECT model_config FROM tenants WHERE id=?", tid).Scan(&raw)
	if err != nil || raw == "" || raw == "{}" {
		return ModelConfig{}, nil
	}
	var mc ModelConfig
	if err := json.Unmarshal([]byte(raw), &mc); err != nil {
		return ModelConfig{}, nil
	}
	return mc, nil
}

// SetModelConfig 保存租户模型配置
func (s *Store) SetModelConfig(tid int64, mc ModelConfig) error {
	b, _ := json.Marshal(mc)
	_, err := s.db.Exec(
		"UPDATE tenants SET model_config=?, updated_at=? WHERE id=?",
		string(b), nowStr(), tid)
	return err
}

// PolicyConfig 租户级策略参数
type PolicyConfig struct {
	HighSim           float64 `json:"high_sim,omitempty"`
	MedSim            float64 `json:"med_sim,omitempty"`
	EvalsPassThreshold float64 `json:"evals_pass_threshold,omitempty"`
}

// GetPolicyConfig 读取租户策略参数
func (s *Store) GetPolicyConfig(tid int64) (PolicyConfig, error) {
	var raw string
	err := s.db.QueryRow("SELECT policy_config FROM tenants WHERE id=?", tid).Scan(&raw)
	if err != nil || raw == "" || raw == "{}" {
		return PolicyConfig{}, nil
	}
	var pc PolicyConfig
	if err := json.Unmarshal([]byte(raw), &pc); err != nil {
		return PolicyConfig{}, nil
	}
	return pc, nil
}

// SetPolicyConfig 保存租户策略参数
func (s *Store) SetPolicyConfig(tid int64, pc PolicyConfig) error {
	b, _ := json.Marshal(pc)
	_, err := s.db.Exec(
		"UPDATE tenants SET policy_config=?, updated_at=? WHERE id=?",
		string(b), nowStr(), tid)
	return err
}

// FlowConfig 租户级流程步骤启停（key → enable）
type FlowConfig struct {
	Steps map[string]bool `json:"steps,omitempty"`
}

// GetFlowConfig 读取租户流程步骤启停配置
func (s *Store) GetFlowConfig(tid int64) (FlowConfig, error) {
	var raw string
	err := s.db.QueryRow("SELECT flow_config FROM tenants WHERE id=?", tid).Scan(&raw)
	if err != nil || raw == "" || raw == "{}" {
		return FlowConfig{}, nil
	}
	var fc FlowConfig
	if err := json.Unmarshal([]byte(raw), &fc); err != nil {
		return FlowConfig{}, nil
	}
	return fc, nil
}

// SetFlowConfig 保存租户流程步骤启停配置
func (s *Store) SetFlowConfig(tid int64, fc FlowConfig) error {
	b, _ := json.Marshal(fc)
	_, err := s.db.Exec(
		"UPDATE tenants SET flow_config=?, updated_at=? WHERE id=?",
		string(b), nowStr(), tid)
	return err
}

// Delete 删除租户
func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec("DELETE FROM tenants WHERE id=?", id)
	return err
}

// effectiveStatus 结合有效期计算实际状态：手动 disabled 优先；到期则 expired
func effectiveStatus(status, expiresAt string) string {
	if status == StatusDisabled {
		return StatusDisabled
	}
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			if time.Now().After(t) {
				return StatusExpired
			}
		}
	}
	return status
}

// ParsePerms 解析权限 JSON
func ParsePerms(permissions string) *Perms {
	p := &Perms{}
	if permissions == "" {
		return p
	}
	_ = json.Unmarshal([]byte(permissions), p)
	return p
}

// 校验错误
var errCodeEmpty = &errText{"租户编码不能为空"}

type errText struct{ s string }

func (e *errText) Error() string { return e.s }

// ============ context 注入 ============

type ctxKey struct{}

// WithTenant 将租户 ID 注入 context
func WithTenant(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext 从 context 取租户 ID；无则返回 0（调用方需回退默认租户）
func FromContext(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxKey{}).(int64)
	return v
}