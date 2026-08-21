package api

// ============ 本文件职责中文说明 ============
// 开放 API（API Key 鉴权）：翻译 / KB 统计 / 用量查询 / Key 轮换 / 文档（handleOpenAPI 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ========================================

import (
	"encoding/json"
	"net/http"
	"translator/internal/store"
)

// ============ 开放 API（API Key 鉴权） ============

// handleOpenAPIDocs 开放 API 文档（静态 HTML）
func (s *Server) handleOpenAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8">
<title>翻译平台开放 API 文档</title>
<style>body{font-family:-apple-system,Segoe UI,sans-serif;max-width:900px;margin:30px auto;padding:0 20px;color:#222}
h1{border-bottom:2px solid #1a237e;padding-bottom:8px}code{background:#f0f0f0;padding:2px 6px;border-radius:4px}
pre{background:#f6f8fa;padding:12px;border-radius:8px;overflow:auto}
table{border-collapse:collapse;width:100%;margin:10px 0}th,td{border:1px solid #ddd;padding:8px;text-align:left;font-size:14px}th{background:#fafbfd}
.badge{display:inline-block;background:#e8eaf6;color:#1a237e;border-radius:4px;padding:2px 8px;font-size:12px}</style></head><body>
<h1>翻译平台开放 API</h1>
<p>所有接口使用 <code>Authorization: Bearer &lt;API_KEY&gt;</code> 认证，API Key 在管理后台「API Key」面板签发。</p>
<table><tr><th>方法</th><th>路径</th><th>权限</th><th>说明</th></tr>
<tr><td>POST</td><td>/openapi/v1/translate</td><td class="badge">translate/all</td><td>文本翻译</td></tr>
<tr><td>GET</td><td>/openapi/v1/kb/stats</td><td class="badge">kb/all</td><td>知识库条目统计</td></tr>
<tr><td>GET</td><td>/openapi/v1/billing/usage</td><td class="badge">billing/all</td><td>用量与余额</td></tr>
<tr><td>POST</td><td>/openapi/v1/apikey/rotate</td><td class="badge">all</td><td>轮换 API Key（旧 Key 立即失效）</td></tr>
</table>
<h2>翻译请求示例</h2>
<pre>curl -X POST https://<span>域名</span>/openapi/v1/translate \
  -H "Authorization: Bearer &lt;API_KEY&gt;" \
  -H "Content-Type: application/json" \
  -d '{"text":"请检查制动系统","target_langs":["en","de"]}'</pre>
<p>响应：<code>translations</code> 为目标语言→译文映射，<code>sources</code> 标记来源（kb/ai）。</p>
</body></html>`))
}

// handleOpenAPITranslate 开放翻译接口
func (s *Server) handleOpenAPITranslate(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "translate" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "API Key 无翻译权限"})
		return
	}
	var req struct {
		Text        string   `json:"text"`         // 待翻译源文本（必填）
		TargetLangs []string `json:"target_langs"` // 目标语言列表（默认 ["en"]）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "text 不能为空"})
		return
	}
	if len(req.TargetLangs) == 0 {
		req.TargetLangs = []string{"en"}
	}
	// ★ 强制计费前置校验（句数模式）：余额不足直接拒绝，错误码 sentence_exhausted
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "code": gateErr.Error(), "message": gateErr.Error()})
		return
	}
	enforced := false
	var balanceBefore int64
	if v, _ := s.Store.GetConfig("sentence_enforced"); v == "1" {
		enforced = true
		balanceBefore, _ = s.Store.GetSentenceBalance(tid)
		if balanceBefore <= 0 {
			writeJSON(w, 200, map[string]interface{}{"success": false, "code": "sentence_exhausted",
				"message": "句数余额不足，请购买套餐后重试"})
			return
		}
	}
	res := s.Engine.HandleText(r.Context(), req.Text, map[string]interface{}{"target_langs": req.TargetLangs}, nil)
	var balanceAfter int64 = -1
	if res.Error == "" {
		// ★ 计量：开放 API 翻译按源文本字符数计量
		s.meterUsage(r, tid, "translate", int64(len([]rune(req.Text))))
		// ★ 句数扣减（与前台同链路：源句数 × 目标语言数，受强制计费门控）
		s.meterSentences(r, tid, req.Text, map[string]interface{}{"target_langs": toAnySlice(req.TargetLangs)})
		s.metrics.countTranslate("openapi", true)
	} else {
		s.metrics.countTranslate("openapi", false)
	}
	if enforced {
		balanceAfter, _ = s.Store.GetSentenceBalance(tid)
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":      true,
		"translations": res.Data.Translations,
		"sources":      res.Data.TranslationsSource,
		"mode":         res.Data.Mode,
		"reply":        res.Reply,
		"sentence_balance": balanceAfter,
	})
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

// toAnySlice 字符串切片转 interface 切片（meterSentences options 兼容）。
func toAnySlice(in []string) []interface{} {
	out := make([]interface{}, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}
