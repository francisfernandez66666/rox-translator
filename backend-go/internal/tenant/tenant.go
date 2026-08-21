// ============ 本文件职责中文说明 ============
// 多租户支持核心：租户实体与管理（tenants 表）、租户级配置（模型/策略/流程步骤）、
// 租户上下文注入（WithTenant / FromContext）、有效期与手动停用状态计算。
// 还负责默认租户 rox 的初始化与老库兼容（model_config/policy_config/flow_config 补列）。
// =============================================

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
	ID          int64  `json:"id"`          // 租户主键 ID
	Code        string `json:"code"`        // 租户编码（唯一，如 rox）
	Name        string `json:"name"`        // 租户名称
	Status      string `json:"status"`      // 状态：active / disabled / expired（disabled 与 expired 由 effectiveStatus 计算）
	ExpiresAt   string `json:"expires_at"`  // 有效期（空=永久），格式 2006-01-02 或 RFC3339
	Permissions string `json:"permissions"` // JSON 权限字符串（解码见 Perms）
	CreatedAt   string `json:"created_at"`  // 创建时间（RFC3339 字符串）
	UpdatedAt   string `json:"updated_at"`  // 更新时间（RFC3339 字符串）
}

// Perms 租户权限（Permissions JSON 的解码结构）
type Perms struct {
	Langs         []string `json:"langs,omitempty"`           // 允许翻译的语言代码；空=全部
	MaxDailyChars int64    `json:"max_daily_chars,omitempty"` // 每日字符上限；0=不限
	// 商业包（句数制）字段：
	SentenceBalance int64  `json:"sentence_balance,omitempty"` // 剩余翻译句数（源句×目标语言数累计）
	PackageCode     string `json:"package_code,omitempty"`     // 当前订阅的付费包编码（空=无订阅）
	SubscribedAt    string `json:"subscribed_at,omitempty"`    // 最近订阅时间（RFC3339）
}

// 租户状态常量
const (
	StatusActive   = "active"   // 启用
	StatusDisabled = "disabled" // 手动停用
	StatusExpired  = "expired"  // 已到期（由有效期自动计算）
)

// Store 租户存储（基于 SQLite）
type Store struct {
	db *sql.DB // 底层 SQLite 连接（与 store/kb 共享）
}

// NewStore 创建租户存储并建表。
// 参数：db=已打开的 SQLite 连接；返回租户 Store 实例。
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.ensureTable(); err != nil {
		return nil, err
	}
	return s, nil
}

// ensureTable 幂等建表并补齐老库缺失的租户级配置列。
// 实现：CREATE TABLE IF NOT EXISTS 建表，再用 PRAGMA table_info 检查并 ALTER 补列。
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
	// 遍历现有列，构建存在性集合
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
	// 缺失的列逐一 ALTER 补上（SQLite 无 ADD COLUMN IF NOT EXISTS）
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

// nowStr 生成当前 RFC3339 格式时间字符串。
func nowStr() string {
	return time.Now().Format(time.RFC3339)
}

// EnsureDefault 确保默认租户 rox 存在；不存在则创建并返回其 id。
// 返回：默认租户 ID。
func (s *Store) EnsureDefault() (int64, error) {
	row := s.db.QueryRow("SELECT id FROM tenants WHERE code='rox'")
	var id int64
	if err := row.Scan(&id); err == nil {
		return id, nil // 已存在直接返回
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

// Create 创建租户（初始状态 active）。
// 参数：code=租户编码（必填），name=名称，expiresAt=有效期，permissions=权限 JSON。
// 返回：新租户对象。
func (s *Store) Create(code, name, expiresAt, permissions string) (*Tenant, error) {
	if code == "" {
		return nil, errCodeEmpty // 编码不能为空
	}
	now := nowStr()
	if permissions == "" {
		permissions = "{}" // 默认空权限
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

// List 列出所有租户（含计算后的有效状态）。
// 返回：租户列表（按 ID 排序）。
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
			continue // 单行解析失败跳过
		}
		t.Status = effectiveStatus(t.Status, t.ExpiresAt) // 结合有效期刷新实际状态
		out = append(out, &t)
	}
	return out, nil
}

// GetByID 按 id 查询租户。
// 参数：id=租户主键 ID；返回租户对象（含计算后的有效状态）。
func (s *Store) GetByID(id int64) (*Tenant, error) {
	row := s.db.QueryRow("SELECT id, code, name, status, expires_at, permissions, created_at, updated_at FROM tenants WHERE id=?", id)
	var t Tenant
	if err := row.Scan(&t.ID, &t.Code, &t.Name, &t.Status, &t.ExpiresAt, &t.Permissions, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Status = effectiveStatus(t.Status, t.ExpiresAt)
	return &t, nil
}

// GetByCode 按租户编码查询。
// 参数：code=租户编码；返回租户对象（含计算后的有效状态）。
func (s *Store) GetByCode(code string) (*Tenant, error) {
	row := s.db.QueryRow("SELECT id, code, name, status, expires_at, permissions, created_at, updated_at FROM tenants WHERE code=?", code)
	var t Tenant
	if err := row.Scan(&t.ID, &t.Code, &t.Name, &t.Status, &t.ExpiresAt, &t.Permissions, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Status = effectiveStatus(t.Status, t.ExpiresAt)
	return &t, nil
}

// Update 更新租户（名称、有效期、权限；状态单独由 SetStatus 控制）。
// 参数：id=租户主键 ID，name=名称，expiresAt=有效期，permissions=权限 JSON。
func (s *Store) Update(id int64, name, expiresAt, permissions string) error {
	if permissions == "" {
		permissions = "{}" // 默认空权限
	}
	_, err := s.db.Exec(
		"UPDATE tenants SET name=?, expires_at=?, permissions=?, updated_at=? WHERE id=?",
		name, expiresAt, permissions, nowStr(), id)
	return err
}

// SetStatus 设置租户状态（active / disabled）。
// 参数：id=租户主键 ID，status=新状态。
func (s *Store) SetStatus(id int64, status string) error {
	_, err := s.db.Exec(
		"UPDATE tenants SET status=?, updated_at=? WHERE id=?",
		status, nowStr(), id)
	return err
}

// ModelConfig 租户级模型配置
type ModelConfig struct {
	APIBase string `json:"api_base"` // 模型 API 基础地址
	APIKey  string `json:"api_key"`  // 模型 API Key
	Model   string `json:"model"`    // 模型名
}

// GetModelConfig 读取租户模型配置；未配置返回空值（调用方回退全局默认）。
// 参数：tid=租户 ID；返回租户级模型配置结构体。
func (s *Store) GetModelConfig(tid int64) (ModelConfig, error) {
	var raw string
	err := s.db.QueryRow("SELECT model_config FROM tenants WHERE id=?", tid).Scan(&raw)
	// 未配置或空 JSON 视为无覆盖，返回零值结构体
	if err != nil || raw == "" || raw == "{}" {
		return ModelConfig{}, nil
	}
	var mc ModelConfig
	if err := json.Unmarshal([]byte(raw), &mc); err != nil {
		return ModelConfig{}, nil // 解析失败按未配置处理
	}
	return mc, nil
}

// SetModelConfig 保存租户模型配置（整体覆盖写入 model_config JSON 列）。
// 参数：tid=租户 ID，mc=模型配置结构体。
func (s *Store) SetModelConfig(tid int64, mc ModelConfig) error {
	b, _ := json.Marshal(mc)
	_, err := s.db.Exec(
		"UPDATE tenants SET model_config=?, updated_at=? WHERE id=?",
		string(b), nowStr(), tid)
	return err
}

// PolicyConfig 租户级策略参数
type PolicyConfig struct {
	HighSim            float64 `json:"high_sim,omitempty"`             // 高相似度阈值（TM 命中判断）
	MedSim             float64 `json:"med_sim,omitempty"`              // 中等相似度阈值
	EvalsPassThreshold float64 `json:"evals_pass_threshold,omitempty"` // 评估通过阈值
}

// GetPolicyConfig 读取租户策略参数。
// 参数：tid=租户 ID；返回策略配置结构体（未配置返回零值）。
func (s *Store) GetPolicyConfig(tid int64) (PolicyConfig, error) {
	var raw string
	err := s.db.QueryRow("SELECT policy_config FROM tenants WHERE id=?", tid).Scan(&raw)
	if err != nil || raw == "" || raw == "{}" {
		return PolicyConfig{}, nil // 未配置按零值返回
	}
	var pc PolicyConfig
	if err := json.Unmarshal([]byte(raw), &pc); err != nil {
		return PolicyConfig{}, nil // 解析失败按未配置处理
	}
	return pc, nil
}

// SetPolicyConfig 保存租户策略参数（整体覆盖写入 policy_config JSON 列）。
// 参数：tid=租户 ID，pc=策略配置结构体。
func (s *Store) SetPolicyConfig(tid int64, pc PolicyConfig) error {
	b, _ := json.Marshal(pc)
	_, err := s.db.Exec(
		"UPDATE tenants SET policy_config=?, updated_at=? WHERE id=?",
		string(b), nowStr(), tid)
	return err
}

// FlowConfig 租户级流程步骤启停（key → enable）
type FlowConfig struct {
	Steps map[string]bool `json:"steps,omitempty"` // 步骤标识 → 是否启用
}

// GetFlowConfig 读取租户流程步骤启停配置。
// 参数：tid=租户 ID；返回流程步骤配置结构体。
func (s *Store) GetFlowConfig(tid int64) (FlowConfig, error) {
	var raw string
	err := s.db.QueryRow("SELECT flow_config FROM tenants WHERE id=?", tid).Scan(&raw)
	if err != nil || raw == "" || raw == "{}" {
		return FlowConfig{}, nil // 未配置按空 map 返回
	}
	var fc FlowConfig
	if err := json.Unmarshal([]byte(raw), &fc); err != nil {
		return FlowConfig{}, nil // 解析失败按未配置处理
	}
	return fc, nil
}

// SetFlowConfig 保存租户流程步骤启停配置（整体覆盖写入 flow_config JSON 列）。
// 参数：tid=租户 ID，fc=流程步骤配置结构体。
func (s *Store) SetFlowConfig(tid int64, fc FlowConfig) error {
	b, _ := json.Marshal(fc)
	_, err := s.db.Exec(
		"UPDATE tenants SET flow_config=?, updated_at=? WHERE id=?",
		string(b), nowStr(), tid)
	return err
}

// Delete 删除租户。
// 参数：id=租户主键 ID；返回错误。
func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec("DELETE FROM tenants WHERE id=?", id)
	return err
}

// effectiveStatus 结合有效期计算实际状态：手动 disabled 优先；到期则 expired。
// 参数：status=数据库存储的状态，expiresAt=有效期（RFC3339）；返回计算后的有效状态。
func effectiveStatus(status, expiresAt string) string {
	if status == StatusDisabled {
		return StatusDisabled // 手动停用优先级最高
	}
	if expiresAt != "" {
		// 到期时间已过则判定为 expired
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			if time.Now().After(t) {
				return StatusExpired
			}
		}
	}
	return status
}

// ParsePerms 解析权限 JSON 字符串为 Perms 结构体。
// 参数：permissions=权限 JSON；返回解析后的权限结构体（空串返回空结构体）。
func ParsePerms(permissions string) *Perms {
	p := &Perms{}
	if permissions == "" {
		return p
	}
	_ = json.Unmarshal([]byte(permissions), p) // 解析失败时保留零值
	return p
}

// 校验错误
var errCodeEmpty = &errText{"租户编码不能为空"}

// errText 自定义错误类型：仅保存一条错误消息文本。
type errText struct{ s string }

// Error 实现 error 接口，返回错误消息文本。
func (e *errText) Error() string { return e.s }

// ============ context 注入 ============

// ctxKey 私有 context 键类型（防止与其他包冲突）。
type ctxKey struct{}

// WithTenant 将租户 ID 注入 context。
// 参数：ctx=父 context，id=租户 ID；返回携带租户 ID 的新 context。
func WithTenant(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext 从 context 取租户 ID；无则返回 0（调用方需回退默认租户）。
// 参数：ctx=携带租户 ID 的 context；返回租户 ID（未注入时为 0）。
func FromContext(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxKey{}).(int64)
	return v
}
