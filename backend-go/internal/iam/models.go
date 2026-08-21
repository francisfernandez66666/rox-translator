// ============ 本文件职责中文说明 ============
// IAM 主数据模型定义：用户（User）与组织（Org）实体、四级角色常量、
// 用户状态常量、组织类型常量。
// 本包是身份与访问管理（Identity and Access Management）子系统的数据结构层，
// 与具体存储/HTTP 逻辑解耦；store.Store 与 auth 相关能力分别见 store.go / auth.go。
// 角色体系：普通用户(user,1) < 部门管理员(dept_admin,2) < 租户管理员(tenant_admin,3) <
// 超级管理员(super_admin,4)；旧值 approver/admin 兼容映射为 3/4 级。
// =============================================
package iam

// User 用户实体（users 表一行）。
// JSON 序列化用于登录响应与后台账户列表；PasswordHash 标记 json:"-" 不对外暴露。
type User struct {
	ID           int64  `json:"id"`            // 用户主键 ID
	TenantID     int64  `json:"tenant_id"`     // 所属租户 ID（0=平台级账号，仅超级管理员）
	Username     string `json:"username"`      // 登录用户名（同租户内唯一）
	PasswordHash string `json:"-"`             // 密码哈希（bcrypt，序列化时隐藏）
	DisplayName  string `json:"display_name"`  // 显示名称（界面展示用）
	Role         string `json:"role"`          // 角色：user/dept_admin/tenant_admin/super_admin（兼容 approver/admin）
	Status       string `json:"status"`        // 账号状态：active（启用）/ disabled（停用）
	CreatedBy    int64  `json:"created_by"`    // 创建者用户 ID（0=系统初始化创建）
	LastLoginAt  string `json:"last_login_at"` // 最近登录时间（RFC3339；空=从未登录）
	OrgID        int64  `json:"org_id"`        // 所属组织 ID（0=未分配，挂在根组织）
	Email        string `json:"email"`         // 联系邮箱（找回密码验证码接收地址）
	CreatedAt    string `json:"created_at"`    // 创建时间（RFC3339 字符串）
	UpdatedAt    string `json:"updated_at"`    // 更新时间（RFC3339 字符串）
}

// 角色常量（四级角色体系 + 旧值兼容）。
// 权限等级由 auth.RoleLevel 换算：super_admin=4 > tenant_admin=3 > dept_admin=2 > user=1。
const (
	RoleUser        = "user"         // 普通用户（等级 1）：使用翻译功能
	RoleDeptAdmin   = "dept_admin"   // 部门管理员（等级 2）：管理本部门子树成员与部门知识库包
	RoleTenantAdmin = "tenant_admin" // 租户管理员（等级 3）：管理本租户组织结构/成员/订阅等
	RoleSuperAdmin  = "super_admin"  // 超级管理员（等级 4）：平台级全量权限
	// —— 旧值兼容（历史数据中出现的角色名，读取时按下方等级映射处理）——
	RoleApprover = "approver" // 旧审批员角色 → 视为租户管理员（等级 3）
	RoleAdmin    = "admin"    // 旧管理员角色 → 视为超级管理员（等级 4）
)

// 用户状态常量。
const (
	UserActive   = "active"   // 启用：可正常登录使用
	UserDisabled = "disabled" // 停用：禁止登录（数据保留）
)

// Org 组织实体（orgs 表一行），构成「根组织 → 组织 → 部门」的树形管理结构。
// 平台根组织（tenant_id=0）是超管的独立管理空间，各租户根组织在视图上挂其下。
type Org struct {
	ID        int64  `json:"id"`         // 组织主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户 ID（0=平台根组织专属）
	ParentID  int64  `json:"parent_id"`  // 父组织 ID（0=挂根组织下；平台视图下租户根指向平台根 ID）
	Name      string `json:"name"`       // 组织名称（根组织可改名并同步租户名）
	Type      string `json:"type"`       // 类型：root(根组织) / org(组织) / dept(部门)
	CreatedAt string `json:"created_at"` // 创建时间（RFC3339 字符串）
	UpdatedAt string `json:"updated_at"` // 更新时间（RFC3339 字符串）
}

// 组织类型常量。
const (
	OrgTypeRoot = "root" // 根组织（每个租户一行；平台根组织 tenant_id=0）
	OrgTypeOrg  = "org"  // 组织（可嵌套任意深度）
	OrgTypeDept = "dept" // 部门（组织下的业务单元）
)