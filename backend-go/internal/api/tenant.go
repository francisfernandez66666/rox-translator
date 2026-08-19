package api

import (
	"encoding/json"
	"net/http"

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ SaaS 租户管理（管理后台，JWT admin 认证） ============

func (s *Server) handleTenantList(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	list, err := s.Ten.List()
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenants": list})
}

func (s *Server) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		ExpiresAt   string `json:"expires_at"`
		Permissions string `json:"permissions"`
		AdminUser   string `json:"admin_user"`   // 初始租户管理员用户名
		AdminPass   string `json:"admin_pass"`   // 初始租户管理员密码
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	t, err := s.Ten.Create(req.Code, req.Name, req.ExpiresAt, req.Permissions)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
		return
	}
	// 创建该租户的初始租户管理员账号（挂到新租户下）
	if s.Store != nil && req.AdminUser != "" && req.AdminPass != "" {
		if _, err := s.Store.CreateUser(t.ID, req.AdminUser, auth.PasswordHash(req.AdminPass), req.AdminUser+" 管理员", store.RoleTenantAdmin, u.ID); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户已创建，但管理员账号创建失败: " + err.Error()})
			return
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenant": t})
}

func (s *Server) handleTenantUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		ExpiresAt   string `json:"expires_at"`
		Permissions string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Ten.Update(req.ID, req.Name, req.ExpiresAt, req.Permissions); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "更新失败: " + err.Error()})
		return
	}
	t, _ := s.Ten.GetByID(req.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenant": t})
}

func (s *Server) handleTenantStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.Status != tenant.StatusActive && req.Status != tenant.StatusDisabled {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "状态仅支持 active/disabled"})
		return
	}
	if err := s.Ten.SetStatus(req.ID, req.Status); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "操作失败: " + err.Error()})
		return
	}
	t, _ := s.Ten.GetByID(req.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenant": t})
}

func (s *Server) handleTenantDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 不允许删除默认租户
	if req.ID == 1 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "默认租户不可删除"})
		return
	}
	if err := s.Ten.Delete(req.ID); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "删除失败: " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}