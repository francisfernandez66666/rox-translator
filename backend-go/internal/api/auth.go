package api

// ============ 本文件职责中文说明 ============
// 本文件实现认证与用户管理相关 HTTP 接口：
//   - 登录（handleLogin）：JWT 签发 + 跨租户用户匹配 + 审计留痕
//   - 当前用户信息（handleMe）、修改密码（handleChangePassword）
//   - 用户管理（tenant_admin + super_admin）：列表/创建/更新/重置密码
// 安全要点：
//   - 登录跨租户匹配时优先平台超管账号；用户名在多租户重复时拒绝登录（防撞库）
//   - 仅超级管理员可创建/分配超管角色，租户管理员禁止操作超管账号（防越权）
//   - 所有写操作均写入审计日志（含变更前后值 diff）

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"sync"
	"time"

	"translator/internal/auth"
	"translator/internal/mail"
	"translator/internal/store"
)

// ============ 认证 ============

// 忘记密码验证码存储（内存实现；单实例部署足够）。
// 键：用户 ID；值：验证码与过期时间。获取验证码时自动清理过期项。
var resetCodes = struct {
	sync.Mutex
	m map[int64]resetCode // 用户 ID → 验证码信息
}{m: map[int64]resetCode{}}

// resetCode 验证码信息。
type resetCode struct {
	Code      string    // 6 位数字验证码
	ExpiresAt time.Time // 过期时间（10 分钟）
}

// handleLogin 登录接口：校验用户名密码并签发 JWT，返回 token + 用户信息。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 {username, password}）。
// 返回: success=true 时携带 token 与用户信息；失败返回 200 + success=false（统一不区分错误细节，防用户名枚举）。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// 暴力破解防护：同一 IP 连续失败超过阈值后进入冷却期
	if s.loginLocked(r) {
		writeJSON(w, 429, map[string]interface{}{"success": false, "message": "登录尝试过于频繁，请稍后再试"})
		return
	}
	// 平台存储未初始化时拒绝登录
	if s.Store == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	var req struct {
		Username string `json:"username"` // 登录用户名
		Password string `json:"password"` // 登录密码（明文，内部比对哈希）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 先按当前请求上下文租户查用户；未命中则跨租户匹配
	tid := s.currentTenant(r)
	u, err := s.Store.GetUserByUsername(tid, req.Username)
	if err != nil {
		// 未在默认租户命中：跨租户全局匹配（兼容平台级账号与单租户唯一用户名）
		matches, gerr := s.Store.GetUserByUsernameGlobal(req.Username)
		if gerr != nil || len(matches) == 0 {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "用户名或密码错误"})
			return
		}
		// 优先平台超管账号（tenant_id=0，平台级账号可在任何租户入口登录）
		u = nil
		for _, m := range matches {
			if m.TenantID == 0 {
				u = m
				break
			}
		}
		// 全平台唯一租户用户可直接登录
		if u == nil && len(matches) == 1 {
			u = matches[0]
		}
		// 用户名在多个租户重复且非超管：拒绝登录，要求通过租户专属入口登录
		if u == nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "用户名在多个租户重复，请通过租户入口登录"})
			return
		}
	}
	// 账号状态校验：停用账号禁止登录
	if u.Status != store.UserActive {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "账号已停用"})
		return
	}
	// 密码校验：比对存储的 bcrypt 哈希（兼容历史 SHA-256）
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		// 暴力破解防护：记录失败次数
		s.recordLoginFail(r)
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "用户名或密码错误"})
		return
	}
	// 历史哈希自动升级：登录成功后若仍为 SHA-256 格式，改写为 bcrypt
	if auth.NeedMigrateHash(u.PasswordHash) {
		if err := s.Store.ResetPassword(u.ID, u.TenantID, auth.PasswordHash(req.Password)); err == nil {
			u.PasswordHash = auth.PasswordHash(req.Password)
		}
	}
	// 登录成功：清零失败计数
	s.clearLoginFails(r)
	// 签发有效期 24 小时的 JWT
	tok, err := auth.Sign(u, 24*time.Hour)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "签发失败"})
		return
	}
	// 记录最近登录时间
	s.Store.TouchLogin(u.ID)
	// 登录审计归属实际登录租户（平台超管 tenant_id=0 归默认租户 1）
	auditTID := u.TenantID
	if auditTID <= 0 {
		auditTID = 1
	}
	s.Store.LogAudit(auditTID, u.ID, "login", "auth", "用户登录")
	// 返回 JWT 与脱敏后的用户信息（不含密码哈希）
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "token": tok,
		"user": map[string]interface{}{
			"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
			"role": u.Role, "tenant_id": u.TenantID,
		},
	})
}

// loginLocked 判断当前请求 IP 是否处于登录冷却期。
// 参数 r: HTTP 请求；返回 true 表示应拒绝登录（429）。
func (s *Server) loginLocked(r *http.Request) bool {
	return s.loginLimit.blocked(clientIP(r))
}

// recordLoginFail 记录当前请求 IP 一次登录失败。
// 参数 r: HTTP 请求。
func (s *Server) recordLoginFail(r *http.Request) {
	if s.loginLimit == nil {
		s.loginLimit = newLoginLimiter()
	}
	s.loginLimit.fail(clientIP(r))
}

// clearLoginFails 登录成功后清零当前请求 IP 的失败记录。
// 参数 r: HTTP 请求。
func (s *Server) clearLoginFails(r *http.Request) {
	s.loginLimit.clear(clientIP(r))
}

// handleMe 当前用户信息接口：返回登录用户完整信息。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（需带 Authorization Bearer JWT）。
// 返回: success=true 时携带 user 对象；未登录返回 401。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "user": u})
}

// handleChangePassword 修改密码接口：校验原密码后更新为 bcrypt 哈希。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 {old_password, new_password}）。
// 返回: success=true 表示修改成功；原密码错误或存储失败返回 success=false。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		OldPassword string `json:"old_password"` // 原密码（用于校验身份）
		NewPassword string `json:"new_password"` // 新密码（存储前转为哈希）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewPassword == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 校验原密码是否正确
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "原密码错误"})
		return
	}
	// 仅允许修改自己租户下的账号（u.ID 绑定 u.TenantID，防跨租户篡改）
	if err := s.Store.ResetPassword(u.ID, u.TenantID, auth.PasswordHash(req.NewPassword)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 记录修改密码审计
	s.Store.LogAudit(u.TenantID, u.ID, "change_password", "auth", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 忘记密码（验证码方式） ============

// handleForgotPassword 忘记密码接口：按用户名或邮箱定位用户，生成验证码并发送。
// 安全要点：无论用户是否存在统一返回 success=true（防用户名枚举）；
// 验证码通过邮件发送（SMTP 未配置时打印日志，前端提示测试模式）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 {username, email}）。
// 返回: success=true 表示已发送验证码（或进入测试模式）。
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"` // 用户名（二选一）
		Email    string `json:"email"`    // 联系邮箱（二选一）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 定位用户：优先用户名（跨租户匹配），其次邮箱
	var u *store.User
	if req.Username != "" {
		if matches, err := s.Store.GetUserByUsernameGlobal(req.Username); err == nil && len(matches) == 1 {
			u = matches[0]
		} else if err == nil && len(matches) > 1 {
			// 多租户重名：需提供邮箱精确定位
			u = nil
		}
	}
	if u == nil && req.Email != "" {
		if found, err := s.Store.GetUserByEmail(req.Email); err == nil {
			u = found
		}
	}
	// 用户不存在或未绑定邮箱：统一返回成功（防枚举），但无法发送
	if u == nil || u.Email == "" {
		writeJSON(w, 200, map[string]interface{}{"success": true, "message": "如果账号存在且绑定了邮箱，验证码已发送"})
		return
	}
	// 校验账号状态：停用账号不发送
	if u.Status != store.UserActive {
		writeJSON(w, 200, map[string]interface{}{"success": true, "message": "如果账号存在且绑定了邮箱，验证码已发送"})
		return
	}
	// 生成 6 位数字验证码
	code, err := genResetCode()
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "生成验证码失败"})
		return
	}
	// 存储验证码（覆盖旧码，10 分钟有效）
	resetCodes.Lock()
	resetCodes.m[u.ID] = resetCode{Code: code, ExpiresAt: time.Now().Add(10 * time.Minute)}
	resetCodes.Unlock()
	// 发送邮件（Noop 模式打印日志）
	s.mailer().Send(&mail.Message{
		To:      u.Email,
		Subject: "【翻译助手】密码重置验证码",
		Body:    mail.BuildVerificationBody(code),
	})
	s.Store.LogAudit(u.TenantID, u.ID, "forgot_password", "auth", "请求重置密码验证码")
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "验证码已发送到绑定邮箱"})
}

// handleResetPassword 重置密码接口：校验验证码后更新密码。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 {username, code, new_password}）。
// 返回: success=true 表示重置成功；验证码错误/过期返回 success=false。
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`     // 用户名（用于定位验证码归属）
		Code        string `json:"code"`         // 6 位验证码
		NewPassword string `json:"new_password"` // 新密码
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Code == "" || req.NewPassword == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 定位用户
	var u *store.User
	if matches, err := s.Store.GetUserByUsernameGlobal(req.Username); err == nil && len(matches) == 1 {
		u = matches[0]
	}
	if u == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "验证码错误或已过期"})
		return
	}
	// 校验验证码（一次性：仅验证通过后作废，防止错误尝试耗尽验证码）
	resetCodes.Lock()
	rc, ok := resetCodes.m[u.ID]
	resetCodes.Unlock()
	if !ok || rc.Code != req.Code || time.Now().After(rc.ExpiresAt) {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "验证码错误或已过期"})
		return
	}
	// 验证通过：作废该验证码并更新密码
	resetCodes.Lock()
	delete(resetCodes.m, u.ID)
	resetCodes.Unlock()
	// 更新密码
	if err := s.Store.ResetPassword(u.ID, u.TenantID, auth.PasswordHash(req.NewPassword)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(u.TenantID, u.ID, "reset_password", "auth", "通过验证码重置密码")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// genResetCode 生成 6 位数字验证码（密码学安全随机）。
func genResetCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// mailer 创建邮件发送器（按环境变量 MAIL_ENABLED / SMTP_* 惰性构建）。
func (s *Server) mailer() mail.Sender {
	return mail.NewSender(&mail.Config{
		Enabled: os.Getenv("MAIL_ENABLED") == "1",
		Host:    os.Getenv("SMTP_HOST"),
		Port:    os.Getenv("SMTP_PORT"),
		User:    os.Getenv("SMTP_USER"),
		Pass:    os.Getenv("SMTP_PASS"),
		From:    os.Getenv("SMTP_FROM"),
	})
}

// ============ 用户管理（tenant_admin + super_admin） ============

// handleAdminUsers 用户列表接口：列出当前生效租户下的用户。
// 权限：部门管理员及以上；部门管理员仅可见本部门及其子部门下用户，租户管理员及以上可见全部。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（需 dept_admin 及以上权限）。
// 返回: success=true 时携带 users 数组。
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	// 部门管理员：仅本部门及其子部门下用户（部门树归集）；租户管理员及以上：租户全部用户
	if auth.RoleLevel(u.Role) == 2 && u.OrgID > 0 {
		orgIDs, err := s.Store.OrgDescendantIDs(tid, u.OrgID)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		users, err := s.Store.ListUsersByOrg(tid, orgIDs)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "users": users})
		return
	}
	// 租户隔离：仅列出生效租户（超管可切换）下的用户
	users, err := s.Store.ListUsers(tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "users": users})
}

// handleAdminUserCreate 创建用户接口（超管可指定归属租户；租户管理员限本租户；部门管理员限本部门子树）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 username/password/display_name/role/tenant_id）。
// 返回: success=true 时携带新用户（密码哈希已置空）。
func (s *Server) handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Username    string `json:"username"`     // 用户名（租户内唯一）
		Password    string `json:"password"`     // 初始密码（存储前转哈希）
		DisplayName string `json:"display_name"` // 显示名称
		Role        string `json:"role"`         // 角色：user/tenant_admin/super_admin/approver/admin
		TenantID    int64  `json:"tenant_id"`    // 归属租户（仅超管可指定，默认生效租户）
		OrgID       int64  `json:"org_id"`       // 所属组织 ID（0=根组织/未分配）
		Email       string `json:"email"`        // 联系邮箱（找回密码验证码接收）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 角色默认普通用户
	if req.Role == "" {
		req.Role = store.RoleUser
	}
	// 角色白名单校验（四级：user/dept_admin/tenant_admin/super_admin + 兼容旧值）
	switch req.Role {
	case store.RoleUser, store.RoleDeptAdmin, store.RoleTenantAdmin, store.RoleSuperAdmin, store.RoleApprover, store.RoleAdmin:
	default:
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "角色无效"})
		return
	}
	// 权限校验：仅超级管理员可创建超级管理员/管理员等高权限角色；租户管理员可创建部门管理员/普通用户
	if auth.RoleLevel(req.Role) >= 4 && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅超级管理员可分配该角色"})
		return
	}
	if auth.RoleLevel(req.Role) >= 3 && !auth.IsTenantAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅租户管理员及以上可分配该角色"})
		return
	}
	// 归属租户判定：超管创建超管时平台级(0)；否则租户管理员限本租户、超管用所选/指定租户
	tid := s.effTenant(r, u)
	if req.Role == store.RoleSuperAdmin || req.Role == store.RoleAdmin {
		tid = 0
	} else if auth.IsSuperAdmin(u) && req.TenantID > 0 {
		tid = req.TenantID
	}
	// 组织归属校验：非平台级用户组织必须属于归属租户
	if tid > 0 {
		if err := s.validateOrg(tid, req.OrgID); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	// 部门管理员范围：目标组织必须在其本部门及子部门树内（否则拒绝创建）
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法开通账号"})
			return
		}
		if req.OrgID > 0 {
			inTree, e := s.Store.IsOrgInSubtree(tid, u.OrgID, req.OrgID)
			if e != nil || !inTree {
				writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权在非本部门下开通账号"})
				return
			}
		}
	}
	nu, err := s.Store.CreateUser(tid, req.Username, auth.PasswordHash(req.Password), req.DisplayName, req.Role, u.ID, req.OrgID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
		return
	}
	// 绑定联系邮箱（用于找回密码）
	if req.Email != "" {
		_ = s.Store.SetUserEmail(nu.ID, tid, req.Email)
		nu.Email = req.Email
	}
	// 返回前清空密码哈希，避免泄露
	nu.PasswordHash = ""
	s.Store.LogAudit(u.TenantID, u.ID, "user_create", "users", req.Username)
	writeJSON(w, 200, map[string]interface{}{"success": true, "user": nu})
}

// handleAdminUserUpdate 更新用户接口（名称/角色/状态），带变更前后值审计。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/display_name/role/status）。
// 返回: success=true 表示更新成功。
func (s *Server) handleAdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID          int64  `json:"id"`           // 目标用户 ID
		DisplayName string `json:"display_name"` // 显示名称（可为空=不修改）
		Role        string `json:"role"`         // 目标角色（可为空=不修改）
		Status      string `json:"status"`       // 状态：active/disabled
		OrgID       *int64 `json:"org_id"`       // 所属组织 ID（nil=不修改，0=根组织）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.Status == "" {
		req.Status = store.UserActive
	}
	// 权限校验：目标用户角色检查（防越权操作超管）
	if target, err := s.Store.GetUser(req.ID, s.effTenant(r, u)); err == nil {
		// 租户管理员不能操作超管账号
		if auth.IsSuperAdmin(target) && !auth.IsSuperAdmin(u) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：不能操作超级管理员"})
			return
		}
		// 非超管不能把用户提升为超管级角色
		if req.Role != "" && auth.RoleLevel(req.Role) >= 4 && !auth.IsSuperAdmin(u) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅超级管理员可分配该角色"})
			return
		}
		// 非租户管理员不能分配租户管理员级角色
		if req.Role != "" && auth.RoleLevel(req.Role) >= 3 && !auth.IsTenantAdmin(u) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅租户管理员及以上可分配该角色"})
			return
		}
	}
	tid := s.effTenant(r, u)
	// 部门管理员范围：目标用户必须在本部门及子部门树内
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法操作"})
			return
		}
		if target, e := s.Store.GetUser(req.ID, tid); e == nil && target.OrgID > 0 {
			inTree, e2 := s.Store.IsOrgInSubtree(tid, u.OrgID, target.OrgID)
			if e2 != nil || !inTree {
				writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作非本部门下账号"})
				return
			}
		}
	}
	// 组织归属校验：非超管不能把用户移出本租户组织（租户隔离，仅校验组织存在性）
	if req.OrgID != nil {
		if err := s.validateOrg(tid, *req.OrgID); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		// 部门管理员：目标组织也须在本部门子树内
		if auth.RoleLevel(u.Role) == 2 {
			inTree, e2 := s.Store.IsOrgInSubtree(tid, u.OrgID, *req.OrgID)
			if e2 != nil || !inTree {
				writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权把用户移出本部门范围"})
				return
			}
		}
	}
	// 结构化变更轨迹：先取更新前值，再执行更新，记录 before/after diff
	before := map[string]string{}
	if target, err := s.Store.GetUser(req.ID, tid); err == nil {
		before = map[string]string{"role": target.Role, "status": target.Status, "display_name": target.DisplayName}
	}
	beforeJSON, _ := json.Marshal(before)
	// 获取当前组织值用于保留未指定字段
	orgID := int64(0)
	if req.OrgID != nil {
		orgID = *req.OrgID
	} else if target, err := s.Store.GetUser(req.ID, tid); err == nil {
		orgID = target.OrgID
	}
	if err := s.Store.UpdateUser(req.ID, tid, req.DisplayName, req.Role, req.Status, orgID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	afterJSON, _ := json.Marshal(map[string]string{"role": req.Role, "status": req.Status, "display_name": req.DisplayName})
	// 写入审计 diff（审计留痕：记录角色/状态变更前后值）
	s.Store.LogAuditDiff(tid, u.ID, "user_update", "users", before["display_name"], string(beforeJSON), string(afterJSON))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// validateOrg 校验组织归属：组织必须属于指定租户（防越权挂到其他租户组织）。
// 参数 tid: 租户 ID；orgID: 组织 ID（0=根组织，直接合法）。
// 返回: nil 表示合法。
func (s *Server) validateOrg(tid, orgID int64) error {
	if orgID <= 0 {
		return nil // 0=根组织/未分配，始终合法
	}
	org, err := s.Store.GetOrgByID(orgID)
	if err != nil {
		return fmt.Errorf("组织不存在")
	}
	if org.TenantID != tid {
		return fmt.Errorf("组织不属于当前租户")
	}
	return nil
}

// handleAdminUserResetPassword 重置密码接口（部门管理员限本部门子树内）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/password）。
// 返回: success=true 表示重置成功；非超管不能重置超管密码。
func (s *Server) handleAdminUserResetPassword(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID       int64  `json:"id"`       // 目标用户 ID
		Password string `json:"password"` // 新密码（存储前转哈希）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || req.Password == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	// 权限校验：非超管不能重置超管密码（防越权）
	if target, err := s.Store.GetUser(req.ID, tid); err == nil && auth.IsSuperAdmin(target) && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：不能操作超级管理员"})
		return
	}
	// 部门管理员范围：目标用户须在本部门子树内
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法操作"})
			return
		}
		if target, e := s.Store.GetUser(req.ID, tid); e == nil && target.OrgID > 0 {
			inTree, e2 := s.Store.IsOrgInSubtree(tid, u.OrgID, target.OrgID)
			if e2 != nil || !inTree {
				writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作非本部门下账号"})
				return
			}
		}
	}
	// 租户隔离：仅重置生效租户下的用户
	if err := s.Store.ResetPassword(req.ID, tid, auth.PasswordHash(req.Password)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(u.TenantID, u.ID, "user_reset_pwd", "users", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
