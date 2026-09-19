// ============================================================================
// api/persona_api.go — 职业角色（job_role）接口层（2026-09-19 角色功能）
//   - GET  /api/register/personas      公开：启用中的角色字典（注册表单角色下拉）
//   - POST /api/me/job-role            登录用户自助维护自己的职业角色（个人/企业通用；
//                                      角色仅绑用户层级，退出企业仍存在——转岗/转行语义）
//   - GET  /api/admin/personas         角色管理列表（部门管理员以上，与行业字典同口径）
//   - POST /api/admin/personas/create  新建角色包（仅超管；宿主租户0 pack_type='persona'）
//   - POST /api/admin/personas/update  改名（仅超管）
//   - POST /api/admin/personas/status  启停（仅超管）
//   - POST /api/admin/personas/delete  删除（仅超管；仍被 users.job_role 引用时拒绝，停用替代）
// 行业字典（admin_kb.go 行业 CRUD）的镜像实现；job_role 校验一律「启用中的角色包 code」。
// ============================================================================
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"translator/internal/auth"
	"translator/internal/store"
)

// normalizePersonaCode 角色 code 规范化校验：小写字母/数字/下划线（与行业 code 同口径，防注入与展示异常）。
func normalizePersonaCode(raw string) (string, bool) {
	code := strings.ToLower(strings.TrimSpace(raw))
	if code == "" {
		return "", false
	}
	for _, c := range code {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return "", false
		}
	}
	return code, true
}

// handleRegisterPersonas 公开角色字典（注册表单/个人中心下拉共用；仅启用中）。
// 响应：{ success, personas: [{code, name}] }
func (s *Server) handleRegisterPersonas(w http.ResponseWriter, r *http.Request) {
	personas := []map[string]string{}
	if s.Store != nil {
		pkgs, err := s.Store.ListPersonas()
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		for _, p := range pkgs {
			if p.Enabled == 0 {
				continue
			}
			personas = append(personas, map[string]string{"code": p.Code, "name": p.Name})
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "personas": personas})
}

// handleMyJobRole 登录用户自助设置职业角色（POST /api/me/job-role，body {job_role}）。
// 校验：job_role 必须是启用中角色包的 code；空串=清除角色。角色只绑用户层级——
// 企业成员/个人用户同一入口，退出企业后 job_role 仍保留在账号上。
func (s *Server) handleMyJobRole(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		JobRole string `json:"job_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	code := strings.ToLower(strings.TrimSpace(req.JobRole))
	if code != "" {
		if _, err := s.Store.FindEnabledPersonaByCode(code); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "角色不存在或已停用"})
			return
		}
	}
	if err := s.Store.SetJobRole(u.ID, u.TenantID, code); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 角色变更影响检索可见域与 CJK 缓存键 → 统一失效（与包启停同口径）
	s.invKB()
	u.JobRole = code
	writeJSON(w, 200, map[string]interface{}{"success": true, "job_role": code, "user": u})
}

// handleAdminPersonas 角色字典列表（部门管理员以上可见；面板附条目计数）。
// 响应：{ success, personas: [{id,code,name,enabled,entry_count}] }
func (s *Server) handleAdminPersonas(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireDeptAdmin(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	pkgs, err := s.Store.ListPersonas()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	counts, _ := s.Store.CountEntriesByPackages(store.SharedHostTenant)
	for _, p := range pkgs {
		p.EntryCount = counts[p.ID]
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "personas": pkgs})
}

// handlePersonaCreate 新建角色（仅超管）：创建平台角色包（pack_type=persona，宿主租户0）。
// body：{ code, name }
func (s *Server) handlePersonaCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可创建角色"})
		return
	}
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "code 与 name 不能为空"})
		return
	}
	code, ok := normalizePersonaCode(req.Code)
	if !ok {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "角色 code 仅允许小写字母/数字/下划线"})
		return
	}
	exists, _ := s.Store.PersonaCodeExists(code)
	if exists {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "角色 code 已存在"})
		return
	}
	p, err := s.Store.CreateKBPackage(store.SharedHostTenant, 0, code, strings.TrimSpace(req.Name), store.PackPersona, store.PackRoleSource)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(store.SharedHostTenant, u.ID, "persona_create", "kb_packages", code)
	s.invKB()
	writeJSON(w, 200, map[string]interface{}{"success": true, "persona": p})
}

// handlePersonaUpdate 编辑角色显示名（仅超管）。body：{ id, name }
func (s *Server) handlePersonaUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可编辑角色"})
		return
	}
	var req struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.UpdatePersona(req.ID, strings.TrimSpace(req.Name)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(store.SharedHostTenant, u.ID, "persona_update", "kb_packages", fmt.Sprintf("id=%d name=%s", req.ID, req.Name))
	s.invKB()
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handlePersonaStatus 启用/停用角色（仅超管）。body：{ id, enabled }——1 启用 / 0 停用。
func (s *Server) handlePersonaStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可启停角色"})
		return
	}
	var req struct {
		ID      int64 `json:"id"`
		Enabled int   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || (req.Enabled != 0 && req.Enabled != 1) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.TogglePersona(req.ID, req.Enabled); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(store.SharedHostTenant, u.ID, "persona_status", "kb_packages", fmt.Sprintf("id=%d enabled=%d", req.ID, req.Enabled))
	s.invKB()
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handlePersonaDelete 删除角色（仅超管）：删除平台角色包及其下条目/安全句。
// 删除前校验角色未被用户引用（users.job_role 指向该 code 时拒绝，应停用替代）。
// body：{ id }
func (s *Server) handlePersonaDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可删除角色"})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	pkg, gErr := s.Store.GetKBPackage(req.ID, store.SharedHostTenant)
	if gErr != nil || pkg == nil || pkg.PackType != store.PackPersona {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "角色不存在"})
		return
	}
	if used, uErr := s.Store.PersonaReferenced(pkg.Code); uErr == nil && used {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "该角色仍被用户使用，无法删除（可停用替代）"})
		return
	}
	if err := s.Store.DeletePersona(req.ID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(store.SharedHostTenant, u.ID, "persona_delete", "kb_packages", pkg.Code)
	s.invKB()
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
