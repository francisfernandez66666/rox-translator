package api

// ============ 本文件职责中文说明 ============
// 本文件实现管理后台（Admin Dashboard）的通用能力与共享助手：
//   - 权限鉴权助手：requireAdminUser（super_admin 超管）/ requireTenantAdmin（租户管理员及以上）
//   - 通用错误类型：apiErr / errNotLogin
//   - 通用工具：maskKey（密钥掩码）/ hasMask（掩码识别）/ atol（int64 解析）/ atoiDef（int 解析带默认值）
// 注：各业务域 Handler 已按域拆分到 admin_flow.go / admin_models.go / admin_kb.go /
//     admin_evals.go / admin_billing.go / admin_apikeys.go / admin_openapi.go
// ========================================

import (
	"net/http"
	"strconv"
	"strings"
	"translator/internal/auth"
	"translator/internal/store"
)

// requireAdminUser 超管鉴权（super_admin，等级 4）
func (s *Server) requireAdminUser(r *http.Request) (*store.User, error) {
	u := s.authUser(r)
	if u == nil {
		return nil, errNotLogin
	}
	if err := auth.RequireRole(u, 4); err != nil {
		return nil, err
	}
	return u, nil
}

// requireTenantAdmin 租户管理员鉴权（tenant_admin 及以上，等级 3）
func (s *Server) requireTenantAdmin(r *http.Request) (*store.User, error) {
	u := s.authUser(r)
	if u == nil {
		return nil, errNotLogin
	}
	if err := auth.RequireRole(u, 3); err != nil {
		return nil, err
	}
	return u, nil
}

// requireDeptAdmin 部门管理员鉴权（dept_admin 及以上，等级 2）
func (s *Server) requireDeptAdmin(r *http.Request) (*store.User, error) {
	u := s.authUser(r)
	if u == nil {
		return nil, errNotLogin
	}
	if err := auth.RequireRole(u, 2); err != nil {
		return nil, err
	}
	return u, nil
}

var errNotLogin = &apiErr{"未登录"}

type apiErr struct{ s string } // s: 错误描述信息（用于返回给前端的错误消息）

// Error 实现 error 接口：返回错误描述信息。
func (e *apiErr) Error() string { return e.s }

// ============ 通用工具 ============
// maskKey 对密钥类字符串脱敏展示：保留首尾各 4 位，中间以 **** 遮盖。
// 参数 k: 原始密钥；返回: 脱敏后的字符串（长度不足 8 时整体替换为 ****）。
func maskKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

// hasMask 判断字符串是否已含脱敏标记 ****（用于避免对已脱敏值重复处理）。
// 参数 k: 待检查字符串；返回: true=已脱敏。
func hasMask(k string) bool { return strings.Contains(k, "****") }

// atol 解析 int64（非法返回 0）
func atol(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// atoiDef 解析 int，非法返回默认值
func atoiDef(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
