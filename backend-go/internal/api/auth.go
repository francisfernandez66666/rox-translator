package api

import (
	"encoding/json"
	"net/http"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// ============ 认证 ============

// handleLogin 登录：返回 JWT + 用户信息
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.currentTenant(r)
	u, err := s.Store.GetUserByUsername(tid, req.Username)
	if err != nil {
		// 未在默认租户命中：跨租户匹配
		matches, gerr := s.Store.GetUserByUsernameGlobal(req.Username)
		if gerr != nil || len(matches) == 0 {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "用户名或密码错误"})
			return
		}
		// 优先超管（平台级账号）
		u = nil
		for _, m := range matches {
			if m.TenantID == 0 {
				u = m
				break
			}
		}
		// 唯一租户用户可直接登录
		if u == nil && len(matches) == 1 {
			u = matches[0]
		}
		if u == nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "用户名在多个租户重复，请通过租户入口登录"})
			return
		}
	}
	if u.Status != store.UserActive {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "账号已停用"})
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "用户名或密码错误"})
		return
	}
	tok, err := auth.Sign(u, 24*time.Hour)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "签发失败"})
		return
	}
	s.Store.TouchLogin(u.ID)
	// 登录审计归属实际登录租户（平台超管 tenant_id=0 归默认租户 1）
	auditTID := u.TenantID
	if auditTID <= 0 {
		auditTID = 1
	}
	s.Store.LogAudit(auditTID, u.ID, "login", "auth", "用户登录")
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "token": tok,
		"user": map[string]interface{}{
			"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
			"role": u.Role, "tenant_id": u.TenantID,
		},
	})
}

// handleMe 当前用户信息
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "user": u})
}

// handleChangePassword 修改密码
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewPassword == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "原密码错误"})
		return
	}
	if err := s.Store.ResetPassword(u.ID, u.TenantID, auth.PasswordHash(req.NewPassword)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(u.TenantID, u.ID, "change_password", "auth", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 用户管理（tenant_admin + super_admin） ============

// handleAdminUsers 用户列表（生效租户下）
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	users, err := s.Store.ListUsers(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "users": users})
}

// handleAdminUserCreate 创建用户（超管可指定租户；租户管理员限本租户）
func (s *Server) handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		TenantID    int64  `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.Role == "" {
		req.Role = store.RoleUser
	}
	switch req.Role {
	case store.RoleUser, store.RoleTenantAdmin, store.RoleSuperAdmin, store.RoleApprover, store.RoleAdmin:
	default:
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "角色无效"})
		return
	}
	// 仅超级管理员可创建超级管理员
	if auth.RoleLevel(req.Role) >= 3 && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅超级管理员可分配该角色"})
		return
	}
	// 归属租户：超管创建超管时平台级(0)，否则租户管理员限本租户、超管用所选/指定租户
	tid := s.effTenant(r, u)
	if req.Role == store.RoleSuperAdmin || req.Role == store.RoleAdmin {
		tid = 0
	} else if auth.IsSuperAdmin(u) && req.TenantID > 0 {
		tid = req.TenantID
	}
	nu, err := s.Store.CreateUser(tid, req.Username, auth.PasswordHash(req.Password), req.DisplayName, req.Role, u.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
		return
	}
	nu.PasswordHash = ""
	s.Store.LogAudit(u.TenantID, u.ID, "user_create", "users", req.Username)
	writeJSON(w, 200, map[string]interface{}{"success": true, "user": nu})
}

// handleAdminUserUpdate 更新用户（名称/角色/状态）
func (s *Server) handleAdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID          int64  `json:"id"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.Status == "" {
		req.Status = store.UserActive
	}
	// 目标用户角色校验
	if target, err := s.Store.GetUser(req.ID, s.effTenant(r, u)); err == nil {
		// 防止越权：租户管理员不能操作超管，也不能提升为超管
		if auth.IsSuperAdmin(target) && !auth.IsSuperAdmin(u) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：不能操作超级管理员"})
			return
		}
		if req.Role != "" && auth.RoleLevel(req.Role) >= 3 && !auth.IsSuperAdmin(u) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：仅超级管理员可分配该角色"})
			return
		}
	}
	tid := s.effTenant(r, u)
	// 结构化变更轨迹：记录角色/状态前后值（先取更新前值）
	before := map[string]string{}
	if target, err := s.Store.GetUser(req.ID, tid); err == nil {
		before = map[string]string{"role": target.Role, "status": target.Status, "display_name": target.DisplayName}
	}
	beforeJSON, _ := json.Marshal(before)
	if err := s.Store.UpdateUser(req.ID, tid, req.DisplayName, req.Role, req.Status); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	afterJSON, _ := json.Marshal(map[string]string{"role": req.Role, "status": req.Status, "display_name": req.DisplayName})
	s.Store.LogAuditDiff(tid, u.ID, "user_update", "users", before["display_name"], string(beforeJSON), string(afterJSON))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAdminUserResetPassword 重置密码（本租户）
func (s *Server) handleAdminUserResetPassword(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID       int64  `json:"id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || req.Password == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 租户管理员不能重置超管密码
	if target, err := s.Store.GetUser(req.ID, s.effTenant(r, u)); err == nil && auth.IsSuperAdmin(target) && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：不能操作超级管理员"})
		return
	}
	if err := s.Store.ResetPassword(req.ID, s.effTenant(r, u), auth.PasswordHash(req.Password)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(u.TenantID, u.ID, "user_reset_pwd", "users", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}