// ============ admin_webhooks.go · 职责说明 ============
// api 包内部实现文件。
// =============================================

// ============ 本文件职责中文说明 ============
// Webhook 管理后台 API：租户配置翻译完成回调 URL / 签名密钥 / 事件订阅。
// 提供列表 / 新增或更新 / 删除 / 测试投递四个接口，供后台「系统集成」面板使用。
// ★ F-64②（批 I-10）口径：本文件失败响应已统一走 s.writeError + apierrors 出口——
//
//	列表/历史查询失败 500、回调地址被 SSRF 闸门拒绝 400、重试按「记录不存在 404 / 状态不可重试 409」分流。
//	注意测试投递是异步 fire-and-forget，端点打不通不会反映在本接口状态码上（结果看 deliveries 历史）。
//
// ========================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	apierrors "translator/internal/errors"
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
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	tid := s.effTenant(r, u)
	hooks, err := s.Store.ListWebhooks(tid)
	if err != nil {
		// F-64②：webhook 配置列表读的是本进程存储层，失败＝服务端出错（500）；
		// 旧写法回 200 会让「系统集成」面板把查询失败渲染成「还没配过回调」。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "webhooks": hooks})
}

// handleWebhookSave 新增或更新 webhook 配置（ID<=0 新增，否则更新）。
func (s *Server) handleWebhookSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID            int64  `json:"id"`             // webhook ID（<=0 表示新增）
		URL           string `json:"url"`            // 回调 URL
		Secret        string `json:"secret"`         // 签名密钥
		Events        string `json:"events"`         // 订阅事件（逗号分隔）
		Enabled       int    `json:"enabled"`        // 1=启用 0=停用
		MaxRetries    int    `json:"max_retries"`    // 最大重试次数
		RetryInterval int    `json:"retry_interval"` // 重试间隔秒数
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
		ID:            req.ID,
		TenantID:      s.effTenant(r, u),
		URL:           req.URL,
		Secret:        req.Secret,
		Events:        req.Events,
		Enabled:       req.Enabled,
		MaxRetries:    req.MaxRetries,
		RetryInterval: req.RetryInterval,
	}
	// 事件缺省订阅翻译完成事件
	if hook.Events == "" {
		hook.Events = "translation.completed"
	}
	if err := s.Store.UpsertWebhook(hook); err != nil {
		// F-64②：保存失败的绝大多数成因是 store 侧回调地址校验（仅 http/https、格式不合法、
		// 指向内网/保留地址被 SSRF 闸门拒绝）——载荷本身不合法，改好 URL 再发才有意义 ⇒ 400。
		// 「改好载荷即可能成功」正是 400 的语义；数据库写入失败同走此支但概率极低，
		// 文案仍是 store 原话逐字透出，排障按 message 而不是按状态码定因。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "webhook_save", "webhooks", strconv.FormatInt(hook.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "webhook": hook})
}

// handleWebhookDelete 删除指定 webhook 配置。
func (s *Server) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
		// F-64②：删除走 DELETE ... WHERE id=? AND tenant_id=?，删不到只是 0 行影响、不报错，
		// 进到这条分支＝数据库执行失败＝服务端出错（500）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "webhook_delete", "webhooks", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleWebhookTest 测试投递：向指定 webhook 发送一条 ping 事件，验证回调可达。
func (s *Server) handleWebhookTest(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
		// F-64②：这一处读的是本层数据库（取租户 webhook 列表以校验 ID 归属），不是回调端点，
		// 失败＝服务端出错（500）。真正的「对方回调地址打不通」发生在下面 DispatchWebhookForce
		// 的异步 goroutine 里——它不返回 error，投递结果只落 webhook_deliveries 供
		// /api/webhooks/deliveries 回看，所以本 handler 物理上不存在 502/503 这条分支。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
	// 注：DispatchWebhookForce 按租户全量启用 webhook 下发，未按 target.ID 精确投递；
	//     此处遍历列表仅为校验该 ID 归属本租户且存在。
	s.Store.DispatchWebhookForce(s.effTenant(r, u), "ping", map[string]interface{}{
		"event":     "ping",
		"tenant_id": s.effTenant(r, u),
		"time":      nowRFC3339(),
	})
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "webhook_test", "webhooks", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已发送测试 ping，请检查回调端点日志"})
}

// handleWebhookDeliveries 查询指定 webhook 的投递历史（分页，按时间倒序）。
func (s *Server) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	webhookIDStr := r.URL.Query().Get("webhook_id")
	webhookID, _ := strconv.ParseInt(webhookIDStr, 10, 64)
	if webhookID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "webhook_id 参数缺失"})
		return
	}
	limitStr := r.URL.Query().Get("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 {
		limit = 50
	}
	tid := s.effTenant(r, u)
	deliveries, err := s.Store.ListDeliveries(webhookID, tid, limit)
	if err != nil {
		// F-64②：投递历史读的是本进程存储层，失败＝服务端出错（500）；
		// 旧写法回 200 会让面板把「查历史失败」显示成「这条回调还没投递过」。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// 统计
	total, success, failed, dead, _ := s.Store.GetDeliveryStats(webhookID, tid)
	writeJSON(w, 200, map[string]interface{}{
		"success":    true,
		"deliveries": deliveries,
		"stats":      map[string]int{"total": total, "success": success, "failed": failed, "dead": dead},
	})
}

// handleWebhookRetry 重试一条失败/死信投递。
func (s *Server) handleWebhookRetry(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		DeliveryID int64 `json:"delivery_id"` // 待重试投递 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeliveryID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.RetryDelivery(req.DeliveryID, s.effTenant(r, u)); err != nil {
		// F-64②：原 200 承载失败 → 按真实语义分流（文案仍走 publicErrMessage 逐字透出）。
		// store 侧把三类失败混在同一个 error 里：①「投递记录不存在」（含跨租户）②「该投递已成功，
		// 无需重试」/「关联 webhook 配置不存在」（记录在、状态不允许重试）③ 数据库读写故障。
		// 判据＝失败路径回读一次投递记录（成功链路不多查）：读不到 → 404；读得到 → 409 状态冲突；
		// 回读本身也报错时同样落 404 一侧（与 ① 不可区分，属本层无结构化错误的既有偏差）。
		msg := publicErrMessage(r.Context(), err)
		tid := s.effTenant(r, u)
		if _, gerr := s.Store.GetDelivery(req.DeliveryID, tid); gerr != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, msg))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, msg))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "webhook_retry", "webhook_deliveries", strconv.FormatInt(req.DeliveryID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已重新投递"})
}
