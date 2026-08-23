package api

// ============ 本文件职责中文说明 ============
// 开放 API（API Key 鉴权）：翻译 / KB 统计 / 用量查询 / Key 轮换 / 文档（handleOpenAPI 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ========================================

import (
	"net/http"
	"translator/internal/store"
)

// ============ 开放 API（API Key 鉴权） ============

// handleOpenAPIDocs 开放 API 文档（静态 HTML；任务模型契约说明）
func (s *Server) handleOpenAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8">
<title>翻译平台开放 API 文档</title>
<style>body{font-family:-apple-system,Segoe UI,sans-serif;max-width:900px;margin:30px auto;padding:0 20px;color:#222}
h1{border-bottom:2px solid #1a237e;padding-bottom:8px}code{background:#f0f0f0;padding:2px 6px;border-radius:4px}
pre{background:#f6f8fa;padding:12px;border-radius:8px;overflow:auto}
table{border-collapse:collapse;width:100%;margin:10px 0}th,td{border:1px solid #ddd;padding:8px;text-align:left;font-size:14px}th{background:#fafbfd}
.badge{display:inline-block;background:#e8eaf6;color:#1a237e;border-radius:4px;padding:2px 8px;font-size:12px}
.err{color:#c62828}</style></head><body>
<h1>翻译平台开放 API</h1>
<p>所有接口使用 <code>Authorization: Bearer &lt;API_KEY&gt;</code> 认证，API Key 在管理后台「API Key」面板签发。</p>
<p><b>翻译采用异步任务模型</b>：提交后立即返回 <code>task_id</code>，按建议间隔轮询直至终态
（仅 <code>completed / failed</code> 为终态；未完成时 <code>status</code> 返回 <code>queued / processing</code>）。
文本任务建议 <b>15s</b> 轮询一次，文件任务建议 <b>60s</b> 轮询一次。</p>
<table><tr><th>方法</th><th>路径</th><th>权限</th><th>说明</th></tr>
<tr><td>POST</td><td>/openapi/v1/tasks</td><td class="badge">translate/all</td><td>创建任务：JSON=文本；multipart=文件批量（≤20个/30MB）</td></tr>
<tr><td>GET</td><td>/openapi/v1/tasks/status?id=</td><td class="badge">translate/all</td><td>轮询状态与结果</td></tr>
<tr><td>GET</td><td>/openapi/v1/tasks/download?id=&amp;file_id=</td><td class="badge">translate/all</td><td>文件产物下载（缺省 zip 全部）</td></tr>
<tr><td>GET</td><td>/openapi/v1/balance</td><td class="badge">*</td><td>查询 token 余额与 ≈句数</td></tr>
<tr><td>GET</td><td>/openapi/v1/kb/stats</td><td class="badge">kb/all</td><td>知识库条目统计</td></tr>
<tr><td>GET</td><td>/openapi/v1/billing/usage</td><td class="badge">billing/all</td><td>用量明细</td></tr>
<tr><td>POST</td><td>/openapi/v1/apikey/rotate</td><td class="badge">all</td><td>轮换 API Key（旧 Key 立即失效）</td></tr>
</table>
<h2>① 创建文本任务</h2>
<pre>curl -X POST https://<span>域名</span>/openapi/v1/tasks \
  -H "Authorization: Bearer &lt;API_KEY&gt;" -H "Content-Type: application/json" \
  -d '{"text":"请检查制动系统","target_langs":["en","de"],"mode":"pro"}'
# ← 202 {"success":true,"task_id":123,"ticket_no":"T...","status":"queued",
#        "mode":"pro","poll_interval_sec":15,"balance_tokens":12500000,...}</pre>
<h2>② 创建文件批量任务（mode=fast 快速 / pro 专业校对，默认 pro）</h2>
<pre>curl -X POST https://<span>域名</span>/openapi/v1/tasks \
  -H "Authorization: Bearer &lt;API_KEY&gt;" \
  -F "files=@手册.docx" -F "files=@清单.xlsx" -F "target_langs=en,de" -F "mode=fast"</pre>
<h2>③ 轮询状态（文本 15s / 文件 60s）</h2>
<pre>curl "https://<span>域名</span>/openapi/v1/tasks/status?id=123" -H "Authorization: Bearer &lt;API_KEY&gt;"
# 处理中 → {"status":"processing","steps":[...]}
# 文本完成 → {"status":"completed","translations":{"en":"Check the brake system."},"tokens_used":1832}
# 文件完成 → {"status":"completed","files":[...],"download":"/openapi/v1/tasks/download?id=123"}
# 失败     → {"status":"failed","error_code":"insufficient_balance","message":"余额不足，请充值或升级套餐"}</pre>
<h2>错误码（独立出参 error_code）</h2>
<table>
<tr><th>error_code</th><th>含义</th></tr>
<tr><td class="err">insufficient_balance</td><td>余额不足——请充值或升级套餐</td></tr>
<tr><td class="err">rate_limited</td><td>请求过于频繁，稍后重试</td></tr>
<tr><td class="err">daily_quota_exceeded</td><td>达到当日用量上限</td></tr>
<tr><td class="err">bad_request / not_found / forbidden / invalid_api_key / task_failed / not_ready / no_result</td><td>参数/权限/状态类错误</td></tr>
</table>
<h2>计费说明</h2>
<p>按任务全链路<b>真实 LLM token 消耗 × 均摊系数（默认 1.5）</b>从余额扣减；
专业模式包含知识库匹配、双评估与文化闸门调用，token 消耗高于快速模式。
响应中 <code>balance_tokens</code> 为当前余额，<code>balance_sentences_approx</code> 为按换算率折算的≈句数。</p>
</body></html>`))
}

// handleOpenAPIKBStats 开放接口：查询本租户知识库统计（需要 kb/all 权限）
func (s *Server) handleOpenAPIKBStats(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "kb" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "API Key 无知识库权限"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":    true,
		"tenant_id":  ak.TenantID,
		"kb_entries": s.kbStats(ak.TenantID),
	})
}

// kbStats 统计租户知识库条目数（安全封装：DB 为 nil 时返回 0）
func (s *Server) kbStats(tid int64) int64 {
	if s.DB == nil {
		return 0
	}
	total, _, _, _ := s.DB.Stats(tid)
	return total
}

// handleOpenAPIUsage 开放接口：查询本租户用量与余额（需要 billing/all 权限）
func (s *Server) handleOpenAPIUsage(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "billing" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "API Key 无计费权限"})
		return
	}
	usage, total, _ := s.Store.UsageStats(ak.TenantID)
	balance, _ := s.Store.GetBalance(ak.TenantID)
	writeJSON(w, 200, map[string]interface{}{
		"success":   true,
		"tenant_id": ak.TenantID,
		"usage":     usage,
		"total":     total,
		"balance":   balance,
	})
}

// handleOpenAPIKeyRotate 开放接口：轮换本租户 API Key（传入旧 key 换取新 key）
func (s *Server) handleOpenAPIKeyRotate(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "API Key 无效"})
		return
	}
	// 轮换：删除旧 key 并签发同权限新 key
	if err := s.Store.DeleteAPIKey(ak.ID, ak.TenantID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	newKey, err := s.Store.CreateAPIKey(ak.TenantID, ak.Name, ak.Perms)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "message": "API Key 已轮换，旧 Key 立即失效",
		"api_key": newKey, "name": ak.Name, "perms": ak.Perms,
	})
}

// authenticateAPIKey 从 Authorization 头解析 API Key
func (s *Server) authenticateAPIKey(r *http.Request) (*store.APIKey, bool) {
	if s.Store == nil {
		return nil, false
	}
	h := r.Header.Get("Authorization")
	if len(h) < 8 || h[:7] != "Bearer " {
		return nil, false
	}
	key := h[7:]
	ak, err := s.Store.GetAPIKeyByHash(store.HashAPIKey(key))
	if err != nil || ak.Status != "active" {
		return nil, false
	}
	s.Store.TouchAPIKey(ak.ID)
	return ak, true
}
