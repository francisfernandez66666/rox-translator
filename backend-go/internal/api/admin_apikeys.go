// ============ admin_apikeys.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 开放 API Key：签发 / 启停 / 轮换 / 删除（handleAPIKeys 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ========================================

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// ============ 开放 API Key ============

// handleAPIKeys 查询当前租户下的开放 API Key 列表（密钥已脱敏展示，不可复原明文）。参数 w/r：标准 HTTP；鉴权：租户管理员及以上；按 effTenant 租户隔离；返回 keys 数组。
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

// handleAPIKeyCreate 为当前租户签发新的开放 API Key（明文仅本次返回，前端须立即保存）。参数 w/r：body 含 name/perms/daily_call_limit；鉴权：租户管理员及以上；副作用：写入 api_keys 表并记审计 apikey_create；平台上下文自动落到首个活跃租户避免任务归属悬空。
func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Name       string `json:"name"`             // Key 名称（便于管理识别）
		Perms      string `json:"perms"`            // 权限范围（all/translate/kb/billing）
		DailyLimit int64  `json:"daily_call_limit"` // 每日调用上限（0=不限，R4 Key 级配额）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "name 不能为空"})
		return
	}
	tidForKey := s.effTenant(r, u)
	if tidForKey <= 0 {
		// ★ 平台上下文签发的 Key 必须落到具体租户：取首个活跃租户（否则任务归属悬空导致回读404）
		tidForKey = s.Store.FirstActiveTenantID()
	}
	plain, err := s.Store.CreateAPIKey(tidForKey, u.ID, req.Name, req.Perms, req.DailyLimit)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "apikey_create", "api_keys", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain, "note": "请立即保存，仅显示一次"})
}

// handleAPIKeyStatus 启用/停用指定 API Key（status=active/disabled）。参数 w/r：body 含 id 与 status；鉴权：租户管理员及以上；副作用：更新状态并写审计；按租户隔离。
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
	plain, err := s.Store.CreateAPIKey(tid, old.UserID, old.Name, old.Perms, old.DailyCallLimit) // 轮换继承旧限额与归属用户
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "apikey_rotate", "api_keys", old.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain, "note": "旧 Key 已失效，新 Key 仅显示一次"})
}

// handleAPIKeyDelete 删除指定 API Key（按租户隔离）。参数 w/r：body 含 id；鉴权：租户管理员及以上；副作用：删除记录并写审计。
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

// handleAPIKeyLimit 设置 API Key 每日调用上限（R4 Key 级配额；0=不限）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id 与 daily_call_limit，租户管理员及以上）。
func (s *Server) handleAPIKeyLimit(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID    int64 `json:"id"`               // Key 主键 ID
		Limit int64 `json:"daily_call_limit"` // 每日上限（0=不限）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || req.Limit < 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	if err := s.Store.SetAPIKeyDailyLimit(req.ID, tid, req.Limit); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "Key 不存在"})
		return
	}
	s.Store.LogAudit(tid, u.ID, "apikey_set_limit", "api_keys",
		fmt.Sprintf("%d limit=%d", req.ID, req.Limit))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// issueDefaultAPIKey 为新建租户签发默认开放 API Key（translate 权限、不限量）。
// 最佳努力语义：签发失败仅记日志，不阻断租户创建/注册主流程。
// 返回: 明文 Key（仅此一次返回；空串表示签发失败或存储未就绪）。
func (s *Server) issueDefaultAPIKey(tid int64, name string) string {
	return s.issueDefaultAPIKeyFor(tid, 0, name)
}

// issueDefaultAPIKeyFor 指定归属用户签发租户默认 Key（uid>0 即时强绑定，免等重启回填）。
func (s *Server) issueDefaultAPIKeyFor(tid, userID int64, name string) string {
	if s.Store == nil || tid <= 0 {
		return ""
	}
	plain, err := s.Store.CreateAPIKey(tid, userID, name, "translate", 0)
	if err != nil {
		log.Printf("[tenant-default-key] 租户 %d 默认 API Key 签发失败: %v", tid, err)
		return ""
	}
	s.Store.LogAudit(tid, 0, "apikey_issue_default", "api_keys", name)
	return plain
}

// handleAPIKeyReveal 解密返回 Key 明文（固定复制能力：前端只复制不展示）。
// 鉴权：租户管理员及以上 + 租户隔离。
func (s *Server) handleAPIKeyReveal(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // Key 主键 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	plain, err := s.Store.GetAPIKeyPlain(req.ID, s.effTenant(r, u))
	if err != nil || plain == "" {
		writeJSON(w, 404, map[string]interface{}{"success": false, "message": "Key 不存在或未存储可复制密文"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain})
}
