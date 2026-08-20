package api

// ============ 本文件职责中文说明 ============
// 开放 API Key：签发 / 启停 / 轮换 / 删除（handleAPIKeys 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ========================================

import (
	"encoding/json"
	"net/http"
)

// ============ 开放 API Key ============

// handleAPIKeys 列表
func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	keys, err := s.Store.ListAPIKeys(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "keys": keys})
}

// handleAPIKeyCreate 签发
func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Name  string `json:"name"`  // Key 名称（便于管理识别）
		Perms string `json:"perms"` // 权限范围（all/translate/kb/billing）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "name 不能为空"})
		return
	}
	plain, err := s.Store.CreateAPIKey(s.effTenant(r, u), req.Name, req.Perms)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "apikey_create", "api_keys", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain, "note": "请立即保存，仅显示一次"})
}

// handleAPIKeyStatus 启停
func (s *Server) handleAPIKeyStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID     int64  `json:"id"`     // 目标 API Key ID
		Status string `json:"status"` // 目标状态：active（启用）/ disabled（停用）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.SetAPIKeyStatus(req.ID, s.effTenant(r, u), req.Status); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAPIKeyRotate 轮换 API Key（本租户，旧 Key 立即失效）
func (s *Server) handleAPIKeyRotate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待轮换 API Key ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	old, err := s.Store.GetAPIKey(req.ID, tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "Key 不存在"})
		return
	}
	if err := s.Store.DeleteAPIKey(req.ID, tid); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	plain, err := s.Store.CreateAPIKey(tid, old.Name, old.Perms)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "apikey_rotate", "api_keys", old.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain, "note": "旧 Key 已失效，新 Key 仅显示一次"})
}

// handleAPIKeyDelete 删除
func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除 API Key ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeleteAPIKey(req.ID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
