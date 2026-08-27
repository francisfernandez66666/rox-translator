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
	"fmt"
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
	Industry    string `json:"industry"`    // 注册行业编码（关联共享行业包的载入过滤）
	BrandName    string `json:"brand_name"`    // 自定义品牌展示名（租户级品牌定制）
	BrandLogo    string `json:"brand_logo"`    // 自定义品牌 Logo URL（空=用默认）
	Domain       string `json:"domain"`        // 自定义访问域名（用于按域名解析租户品牌）
	BrandHomeBg  string `json:"brand_home_bg"`  // 未登录首页背景图（base64 dataURL 或外链 URL，空=用默认）
	BrandHomeBgStyle string `json:"brand_home_bg_style"` // 首页背景图样式 JSON：{scale,x,y,mode}（mode: tile/cover/contain）
	BrandLoginCardPos string `json:"brand_login_card_pos"` // 登录/注册卡片位置 JSON：{x,y} 百分比（卡片中心相对于视口）
	BrandLoginLayout string `json:"brand_login_layout"` // 登录页布局 JSON：{mode:'full'|'split', side:'left'|'right'}
	BrandLinks   string `json:"brand_links"`   // 自定义页脚链接 JSON 数组（[{label,label_en,url}]）
	InviteEnabled bool `json:"invite_enabled"` // 是否开通「邀请好友」功能（超管按租户开关；false=对该租户关闭邀请裂变）
	IsPersonal   bool `json:"is_personal"`    // 是否个人用户租户（true=个人用户；false=企业用户，企业用户默认不参与邀请好友奖励）
	CreatedAt    string `json:"created_at"`    // 创建时间（RFC3339 字符串）
	UpdatedAt    string `json:"updated_at"`    // 更新时间（RFC3339 字符串）
}

// BrandLink 自定义品牌链接项（页脚展示）
type BrandLink struct {
	Label   string `json:"label"`             // 中文标签
	LabelEn string `json:"label_en"`          // 英文标签
	URL     string `json:"url"`               // 跳转地址
}

// Perms 租户权限（Permissions JSON 的解码结构）
type Perms struct {
	Langs         []string `json:"langs,omitempty"`           // 允许翻译的语言代码；空=全部
	MaxDailyChars int64    `json:"max_daily_chars,omitempty"` // 每日字符上限；0=不限（旧口径，兼容保留）
	// ★ MaxDailyTokens 每日 token 上限（2026-08-26 评审整改 D4）：
	//   与 DailyUsage（usage_ledger 成本 token 合计）同口径比较；>0 时优先生效，
	//   修正旧「字符限额 vs token 计量」的语义错位；0=未配置（gateUsage 回退旧口径）
	MaxDailyTokens int64 `json:"max_daily_tokens,omitempty"`
	// 商业包（句数制）字段：
	SentenceBalance int64  `json:"sentence_balance,omitempty"`   // 剩余翻译句数（源句×目标语言数累计）
	PackageCode     string `json:"package_code,omitempty"`       // 当前订阅的付费包编码（空=无订阅）
	SubscribedAt    string `json:"subscribed_at,omitempty"`      // 最近订阅时间（RFC3339）
	PackageExpires  string `json:"package_expires_at,omitempty"` // 订阅到期时间（RFC3339，空=不限期；到期由后台扫描摘除）
	NotifiedExp7    bool   `json:"notified_exp7,omitempty"`      // 到期提醒 7 天档已发送（去重标记）
	NotifiedExp1    bool   `json:"notified_exp1,omitempty"`      // 到期提醒 1 天档已发送（去重标记）
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
	if !have["industry"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN industry TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["brand_name"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN brand_name TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["brand_logo"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN brand_logo TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["domain"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN domain TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["brand_links"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN brand_links TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["brand_home_bg"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN brand_home_bg TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["brand_home_bg_style"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN brand_home_bg_style TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["brand_login_card_pos"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN brand_login_card_pos TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["brand_login_layout"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN brand_login_layout TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !have["invite_enabled"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN invite_enabled INTEGER NOT NULL DEFAULT 1"); err != nil {
			return err
		}
	}
	if !have["is_personal"] {
		if _, err := s.db.Exec("ALTER TABLE tenants ADD COLUMN is_personal INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	return nil
}

// tenantColumns 租户查询统一列清单（与 scanTenant 顺序一致）。
const tenantColumns = `id, code, name, status, expires_at, permissions, COALESCE(industry,''), COALESCE(brand_name,''), COALESCE(brand_logo,''), COALESCE(domain,''), COALESCE(brand_home_bg,''), COALESCE(brand_home_bg_style,''), COALESCE(brand_login_card_pos,''), COALESCE(brand_login_layout,''), COALESCE(brand_links,''), COALESCE(invite_enabled,1), COALESCE(is_personal,0), created_at, updated_at`

// scanner 同时兼容 *sql.Row 与 *sql.Rows 的 Scan 方法。
type scanner interface{ Scan(dest ...interface{}) error }

// scanTenant 从一行中解析租户对象（字段顺序须与 tenantColumns 一致）。
func scanTenant(s scanner) (*Tenant, error) {
	var t Tenant
	if err := s.Scan(&t.ID, &t.Code, &t.Name, &t.Status, &t.ExpiresAt, &t.Permissions, &t.Industry, &t.BrandName, &t.BrandLogo, &t.Domain, &t.BrandHomeBg, &t.BrandHomeBgStyle, &t.BrandLoginCardPos, &t.BrandLoginLayout, &t.BrandLinks, &t.InviteEnabled, &t.IsPersonal, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Status = effectiveStatus(t.Status, t.ExpiresAt)
	return &t, nil
}

// SetIndustry 设置租户注册行业编码（行业包共享载入的过滤依据）。
func (s *Store) SetIndustry(id int64, code string) error {
	_, err := s.db.Exec("UPDATE tenants SET industry=?, updated_at=? WHERE id=?", code, nowStr(), id)
	return err
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
		"INSERT INTO tenants (code, name, status, expires_at, permissions, created_at, updated_at) VALUES ('rox','langcross 默认租户','active','','{}',?,?)",
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
	rows, err := s.db.Query("SELECT " + tenantColumns + " FROM tenants ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, t)
	}
	return out, nil
}

// GetByID 按 id 查询租户。
// 参数：id=租户主键 ID；返回租户对象（含计算后的有效状态）。
func (s *Store) GetByID(id int64) (*Tenant, error) {
	row := s.db.QueryRow("SELECT "+tenantColumns+" FROM tenants WHERE id=?", id)
	return scanTenant(row)
}

// GetByCode 按租户编码查询。
// 参数：code=租户编码；返回租户对象（含计算后的有效状态）。
func (s *Store) GetByCode(code string) (*Tenant, error) {
	row := s.db.QueryRow("SELECT "+tenantColumns+" FROM tenants WHERE code=?", code)
	return scanTenant(row)
}

// GetByDomain 按自定义域名查询租户（用于按访问域名解析品牌/租户）。
// 参数：domain=访问域名（不含端口）；返回租户对象（含计算后的有效状态）。
func (s *Store) GetByDomain(domain string) (*Tenant, error) {
	row := s.db.QueryRow("SELECT "+tenantColumns+" FROM tenants WHERE domain=?", domain)
	return scanTenant(row)
}

// SetBranding 保存租户级品牌定制（展示名/Logo/自定义域名/页脚链接）。
// 参数：id=租户 ID；brandName/brandLogo/domain 为展示字段；brandLinks 为 JSON 数组字符串（需合法 JSON 数组，空串=清空）。
func (s *Store) SetBranding(id int64, brandName, brandLogo, domain, brandLinks string) error {
	if brandLinks != "" {
		var arr []interface{}
		if err := json.Unmarshal([]byte(brandLinks), &arr); err != nil {
			return fmt.Errorf("brand_links 需为合法 JSON 数组")
		}
	}
	_, err := s.db.Exec(
		"UPDATE tenants SET brand_name=?, brand_logo=?, domain=?, brand_links=?, updated_at=? WHERE id=?",
		brandName, brandLogo, domain, brandLinks, nowStr(), id)
	return err
}

// SetBrandHomeBg 保存租户级「未登录首页背景图」及其样式（缩放/位置/平铺模式）。
// 参数：id=租户 ID；bg=背景图数据（dataURL 或 URL，空串=清空）；style=样式 JSON 字符串。
func (s *Store) SetBrandHomeBg(id int64, bg, style string) error {
	_, err := s.db.Exec("UPDATE tenants SET brand_home_bg=?, brand_home_bg_style=?, updated_at=? WHERE id=?", bg, style, nowStr(), id)
	return err
}

// SetBrandLoginCardPos 保存租户级「登录/注册卡片位置」（百分比 JSON：{x,y}）。
// 参数：id=租户 ID；pos=位置 JSON 字符串（空串=重置默认居中）。
func (s *Store) SetBrandLoginCardPos(id int64, pos string) error {
	_, err := s.db.Exec("UPDATE tenants SET brand_login_card_pos=?, updated_at=? WHERE id=?", pos, nowStr(), id)
	return err
}

// SetBrandLoginLayout 保存租户级「登录页布局」（JSON：{mode:'full'|'split', side:'left'|'right'}）。
// 参数：id=租户 ID；layout=布局 JSON 字符串（空串=重置默认全屏背景）。
func (s *Store) SetBrandLoginLayout(id int64, layout string) error {
	_, err := s.db.Exec("UPDATE tenants SET brand_login_layout=?, updated_at=? WHERE id=?", layout, nowStr(), id)
	return err
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

// SetInviteEnabled 设置租户「邀请好友」功能开关（超管按租户控制是否对租户开放邀请裂变）。
// 参数：id=租户 ID；enabled=true 开通 / false 关闭。
func (s *Store) SetInviteEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(
		"UPDATE tenants SET invite_enabled=?, updated_at=? WHERE id=?",
		enabled, nowStr(), id)
	return err
}

// SetPersonal 设置租户是否为「个人用户」租户（true=个人用户；false=企业用户）。
// 个人用户默认可参与邀请好友奖励；企业用户默认不参与。
// 参数：id=租户 ID；personal=true 标记为个人用户。
func (s *Store) SetPersonal(id int64, personal bool) error {
	_, err := s.db.Exec(
		"UPDATE tenants SET is_personal=?, updated_at=? WHERE id=?",
		personal, nowStr(), id)
	return err
}

// ★ BYOK 下线说明（2026-08-26，决策人定稿「所有 LLM 一律走平台网关」）：
//   原租户级模型配置能力（ModelConfig/Route/GetModelConfig/SetModelConfig）已整体删除，
//   引擎解析优先级收敛为「全局 ModelRoutes → 全局默认」。数据库 tenants.model_config 列
//   保留不删（避免迁移风险），代码不再读写；如需清理历史数据可执行：
//   UPDATE tenants SET model_config='{}';

// PolicyConfig 租户级策略参数
type PolicyConfig struct {
	HighSim            float64 `json:"high_sim,omitempty"`             // 高相似度阈值（TM 命中判断）
	MedSim             float64 `json:"med_sim,omitempty"`              // 中等相似度阈值
	EvalsPassThreshold float64 `json:"evals_pass_threshold,omitempty"` // 评估通过阈值
	// ★ 跨部门降级检索开关（2026-08-26 KB继承链改造）：指针三态——
	//   nil=旧配置未显式设置（默认开启）/ 0=显式关闭 / 1=显式开启。
	//   开启时：链内精确零命中可回退到其他部门「愿意共享」的部门包（share_cross_dept=1）。
	CrossDeptFallback *int `json:"cross_dept_fallback,omitempty"`
	// ★ 数据回流开关（2026-08-26 评审整改 D7，白皮书 §七.4）：
	//   nil=默认参与行业记忆共建 / 1=该租户关闭回流——达标候选与导入候选不进入平台审核池，
	//   私有 TM 与审批直入不受影响。指针三态语义与 CrossDeptFallback 一致。
	DataFeedbackOptOut *int `json:"data_feedback_opt_out,omitempty"`
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

// Name 查询租户名称（按 ID）。
// 参数：id=租户主键 ID；返回租户名称（未找到返回空串）。
func (s *Store) Name(id int64) (string, error) {
	var name string
	err := s.db.QueryRow("SELECT name FROM tenants WHERE id=?", id).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}

// Delete 删除租户并级联清理其全部主数据与业务数据（组织树/用户/知识库/工单/订单等）。
// 参数：id=租户 ID；默认租户（ID=1）由调用方拦截。
func (s *Store) Delete(id int64) error {
	if id <= 0 {
		return fmt.Errorf("无效租户")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 主数据：组织树 + 用户
	for _, tbl := range []string{"orgs", "users"} {
		if _, e := tx.Exec("DELETE FROM "+tbl+" WHERE tenant_id=?", id); e != nil {
			return e
		}
	}
	// 业务数据：知识库/工单/计费/开放能力/审计
	for _, tbl := range []string{
		"kb_safety_phrases", "kb_entries", "kb_packages",
		"tickets", "ticket_state", "balance_accounts", "usage_ledger", "orders", "payments",
		"api_keys", "eval_records", "invite_codes", "invoices", "webhooks",
	} {
		if _, e := tx.Exec("DELETE FROM "+tbl+" WHERE tenant_id=?", id); e != nil {
			return e // 表可能不存在于旧库，忽略不存在的表错误
		}
	}
	if _, e := tx.Exec("DELETE FROM tenants WHERE id=?", id); e != nil {
		return e
	}
	return tx.Commit()
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
