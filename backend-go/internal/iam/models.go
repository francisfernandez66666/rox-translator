package iam

// User 用户实体
type User struct {
	ID           int64  `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	CreatedBy    int64  `json:"created_by"`
	LastLoginAt  string `json:"last_login_at"`
	OrgID        int64  `json:"org_id"`
	Email        string `json:"email"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

const (
	RoleUser        = "user"
	RoleDeptAdmin   = "dept_admin"
	RoleTenantAdmin = "tenant_admin"
	RoleSuperAdmin  = "super_admin"
	RoleApprover    = "approver"
	RoleAdmin       = "admin"
)

const (
	UserActive   = "active"
	UserDisabled = "disabled"
)

// Org 组织实体
type Org struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	ParentID  int64  `json:"parent_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const (
	OrgTypeRoot = "root"
	OrgTypeOrg  = "org"
	OrgTypeDept = "dept"
)