// ============ 本文件职责中文说明 ============
// Webhook 管理后台 API：租户配置翻译完成回调 URL / 签名密钥 / 事件订阅。
// 提供列表 / 新增或更新 / 删除 / 测试投递四个接口，供后台「系统集成」面板使用。
// ========================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"translator/internal/store"
)

// nowRFC3339 返回当前时间的 RFC3339 字符串（webhook 测试载荷使用）。
func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

// handleWebhooks 查询租户 webhook 配置列表。
func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	hooks, err := s.Store.ListWebhooks(tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "webhooks": hooks})
}

// handleWebhookSave 新增或更新 webhook 配置（ID<=0 新增，否则更新）。
func (s *Server) handleWebhookSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID      int64  `json:"id"`      // webhook ID（<=0 表示新增）
		URL     string `json:"url"`     // 回调 URL
		Secret  string `json:"secret"`  // 签名密钥
		Events  string `json:"events"`  // 订阅事件（逗号分隔）
		Enabled int    `json:"enabled"` // 1=启用 0=停用
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.URL == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "回调 URL 不能为空"})
		return
	}
	hook := &store.Webhook{
		ID:       req.ID,
		TenantID: s.effTenant(r, u),
		URL:      req.URL,
		Secret:   req.Secret,
		Events:   req.Events,
		Enabled:  req.Enabled,
	}
	if hook.Events == "" {
		hook.Events = "translation.completed"
	}
	if err := s.Store.UpsertWebhook(hook); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "webhook_save", "webhooks", strconv.FormatInt(hook.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "webhook": hook})
}

// handleWebhookDelete 删除指定 webhook 配置。
func (s *Server) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除 webhook ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeleteWebhook(req.ID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "webhook_delete", "webhooks", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleWebhookTest 测试投递：向指定 webhook 发送一条 ping 事件，验证回调可达。
func (s *Server) handleWebhookTest(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待测试 webhook ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 校验 webhook 归属
	hooks, err := s.Store.ListWebhooks(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var target *store.Webhook
	for _, h := range hooks {
		if h.ID == req.ID {
			target = h
			break
		}
	}
	if target == nil {
		writeJSON(w, 404, map[string]interface{}{"success": false, "message": "webhook 不存在"})
		return
	}
	// 投递 ping 事件（异步，忽略事件订阅过滤，验证回调可达）
	s.Store.DispatchWebhookForce(s.effTenant(r, u), "ping", map[string]interface{}{
		"event":     "ping",
		"tenant_id": s.effTenant(r, u),
		"time":      nowRFC3339(),
	})
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "webhook_test", "webhooks", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已发送测试 ping，请检查回调端点日志"})
}
