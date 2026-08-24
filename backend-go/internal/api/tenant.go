// ============ tenant.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现 SaaS 租户管理接口（管理后台，JWT admin 认证）：
//   - 租户列表/创建/更新/状态切换/删除（handleTenantList / handleTenantCreate / handleTenantUpdate / handleTenantStatus / handleTenantDelete）
//   - 租户数据导出（handleTenantExport）：数据主权，super_admin 导出租户全部业务数据为 JSON 下载
//   - 租户数据清除（handleTenantErase）：GDPR 删除权，super_admin 清除租户全部业务数据
// 安全与约束：
//   - 全部接口 requireAdminUser 鉴权（super_admin）
//   - 默认租户（ID=1）不可删除/不可清除
//   - 创建租户时可同时创建初始租户管理员账号
//   - 清除操作前建议先导出备份（代码注释约定）

import (
	"encoding/json"
	"net/http"
	"strconv"

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ SaaS 租户管理（管理后台，JWT admin 认证） ============

// handleTenantList 租户列表接口（super_admin）：返回平台全部租户。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回: success=true 时携带 tenants 数组。
func (s *Server) handleTenantList(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	// 租户存储未初始化时拒绝
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

// handleTenantCreate 创建租户接口（super_admin）：创建租户并可选同时创建初始租户管理员账号。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 code/name/expires_at/permissions/admin_user/admin_pass）。
// 返回: success=true 时携带新租户对象。
func (s *Server) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
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
		Code        string `json:"code"`        // 租户唯一编码（必填）
		Name        string `json:"name"`        // 租户名称
		ExpiresAt   string `json:"expires_at"`  // 到期时间
		Permissions string `json:"permissions"` // 权限 JSON（含每日字符上限等）
		AdminUser   string `json:"admin_user"`  // 初始租户管理员用户名（可选）
		AdminPass   string `json:"admin_pass"`  // 初始租户管理员密码（可选）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 创建租户
	t, err := s.Ten.Create(req.Code, req.Name, req.ExpiresAt, req.Permissions)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
		return
	}
	// 创建该租户的初始租户管理员账号（挂到新租户下；仅当提供了用户名/密码时）
	if s.Store != nil && req.AdminUser != "" && req.AdminPass != "" {
		if _, err := s.Store.CreateUser(t.ID, req.AdminUser, auth.PasswordHash(req.AdminPass), req.AdminUser+" 管理员", store.RoleTenantAdmin, u.ID, 0); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户已创建，但管理员账号创建失败: " + err.Error()})
			return
		}
	}
	// ★ 默认分配一个开放 API Key（translate 权限、不限量；明文仅此一次随响应返回）
	defaultKey := s.issueDefaultAPIKey(t.ID, "默认 Key")
	resp := map[string]interface{}{"success": true, "tenant": t}
	if defaultKey != "" {
		resp["api_key"] = defaultKey
		resp["message"] = "创建成功（已附带默认 API Key，请立即保存，仅显示一次）"
	}
	writeJSON(w, 200, resp)
}

// handleTenantUpdate 更新租户接口（super_admin）：更新名称/到期时间/权限。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/name/expires_at/permissions）。
// 返回: success=true 时携带更新后的租户对象。
func (s *Server) handleTenantUpdate(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
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
		ID          int64  `json:"id"`          // 目标租户 ID
		Name        string `json:"name"`        // 租户名称
		ExpiresAt   string `json:"expires_at"`  // 到期时间
		Permissions string `json:"permissions"` // 权限 JSON
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 更新租户
	if err := s.Ten.Update(req.ID, req.Name, req.ExpiresAt, req.Permissions); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "更新失败: " + err.Error()})
		return
	}
	t, _ := s.Ten.GetByID(req.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenant": t})
}

// handleTenantStatus 切换租户状态接口（super_admin）：启用/禁用租户。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/status，status 仅支持 active/disabled）。
// 返回: success=true 时携带更新后的租户对象。
func (s *Server) handleTenantStatus(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
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
		ID     int64  `json:"id"`     // 目标租户 ID
		Status string `json:"status"` // 目标状态：active（启用）/ disabled（禁用）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 状态白名单校验
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

// handleTenantDelete 删除租户接口（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 表示删除成功；默认租户（ID=1）不可删除。
func (s *Server) handleTenantDelete(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
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
		ID int64 `json:"id"` // 目标租户 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 安全约束：不允许删除默认租户（平台根基）
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

// handleTenantExport 导出租户全部数据接口（数据主权，super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: JSON 文件下载（Content-Disposition attachment），文件名 tenant_<id>_export.json。
func (s *Server) handleTenantExport(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	_, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 目标租户 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Store == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 导出该租户全部业务数据
	data, err := s.Store.ExportTenantData(req.ID)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 格式化 JSON 并作为附件下载
	b, _ := json.MarshalIndent(data, "", "  ")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename=tenant_`+strconv.FormatInt(req.ID, 10)+`_export.json`)
	_, _ = w.Write(b)
}

// handleTenantErase 清除租户全部业务数据接口（GDPR 删除权，super_admin；删除前建议先导出备份）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 表示租户业务数据已清除；默认租户（ID=1）不可清除。
func (s *Server) handleTenantErase(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 目标租户 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 安全约束：默认租户不可清除
	if req.ID == 1 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "默认租户不可清除"})
		return
	}
	if s.Store == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 清除租户全部业务数据（GDPR 删除权）
	if err := s.Store.EraseTenantData(req.ID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 清除操作审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "tenant_erase", "tenants", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "租户业务数据已清除"})
}
