package api

// ============ 本文件职责中文说明 ============
// 开放 API 文档（/openapi/docs）+ 文档在线维护接口：
//   - 公开页：Markdown 源 → goldmark 渲染 HTML（GFM 表格/删除线，默认安全模式转义原始标签）
//   - 内容来源：system_config.openapi_docs_md（超管在线编辑）优先；空则回退内置 defaultDocsMD
//   - 维护接口（仅超管 requireAdminUser，写操作记审计）：
//       GET  /api/admin/openapi-docs         读当前生效 MD 源码
//       POST /api/admin/openapi-docs         保存 MD 源码（空串=恢复内置默认）
//       POST /api/admin/openapi-docs/preview 预览渲染结果（不落库）
// 安全要点：MD 渲染走 goldmark 默认安全配置——调用方注入的原始 HTML 标签被转义，
// 杜绝公开页脚本注入；内容上限 256KB。
// ========================================

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"translator/internal/store"
)

// openAPIDocsConfigKey 文档 MD 源码在 system_config 中的存储键
const openAPIDocsConfigKey = "openapi_docs_md"

// openAPIDocsMaxBytes 文档源码上限（256KB，防超大内容写库）
const openAPIDocsMaxBytes = 256 << 10

// docsRenderer 共享渲染器：GFM 表格 + 删除线；不启用 Unsafe（原始 HTML 转义，安全优先）
var docsRenderer = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough),
	goldmark.WithRendererOptions(html.WithXHTML()),
)

// renderDocsHTML 把 Markdown 渲染为完整文档页 HTML（内嵌样式壳，与品牌配色一致）。
// 参数 md: Markdown 源码。返回: 完整 HTML 字符串（渲染失败时回退纯文本转义展示）。
func renderDocsHTML(md string) string {
	var buf bytes.Buffer
	if err := docsRenderer.Convert([]byte(md), &buf); err != nil {
		// 渲染异常兜底：按纯文本输出，绝不因文档问题打断服务
		return `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8"><title>翻译平台开放 API 文档</title></head><body><pre>` +
			htmlEscapeText(md) + `</pre></body></html>`
	}
	return `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>翻译平台开放 API 文档</title>
<style>
body{font-family:-apple-system,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;max-width:900px;margin:30px auto;padding:0 20px;color:#222;line-height:1.7}
h1{border-bottom:2px solid #1a237e;padding-bottom:8px;font-size:24px}
h2{font-size:19px;margin-top:26px;border-bottom:1px solid #e0e0e0;padding-bottom:4px}
code{background:#f0f0f0;padding:2px 6px;border-radius:4px;font-size:13px}
pre{background:#f6f8fa;padding:12px;border-radius:8px;overflow:auto;font-size:13px}
pre code{background:transparent;padding:0}
table{border-collapse:collapse;width:100%;margin:10px 0}
th,td{border:1px solid #ddd;padding:8px 10px;text-align:left;font-size:14px}
th{background:#fafbfd}
.badge{display:inline-block;background:#e8eaf6;color:#1a237e;border-radius:4px;padding:2px 8px;font-size:12px}
.err{color:#c62828}
blockquote{border-left:4px solid #1a73e8;margin:10px 0;padding:4px 14px;background:#f5f9ff;color:#455}
a{color:#1a73e8}
</style></head><body>` + buf.String() + `</body></html>`
}

// htmlEscapeText 纯文本 HTML 转义（渲染兜底路径用）。
func htmlEscapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// currentDocsMD 返回当前生效的文档 MD 源码：system_config 优先，空则内置默认。
func (s *Server) currentDocsMD() string {
	if v, _ := s.Store.GetConfig(openAPIDocsConfigKey); strings.TrimSpace(v) != "" {
		return v
	}
	return defaultDocsMD
}

// handleOpenAPIDocs 开放 API 文档公开页：当前生效 MD 渲染为 HTML。
func (s *Server) handleOpenAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderDocsHTML(s.currentDocsMD())))
}

// handleAdminOpenAPIDocsGet 超管读取当前生效的文档 MD 源码。
func (s *Server) handleAdminOpenAPIDocsGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	md := s.currentDocsMD()
	s.Store.LogAudit(0, u.ID, "openapi_docs_view", "system_config", openAPIDocsConfigKey)
	writeJSON(w, 200, map[string]interface{}{"success": true, "md": md, "is_default": md == defaultDocsMD})
}

// handleAdminOpenAPIDocsSave 超管保存文档 MD 源码（空串=恢复内置默认）。
func (s *Server) handleAdminOpenAPIDocsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		MD string `json:"md"` // Markdown 源码（空串=恢复内置默认）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if len(req.MD) > openAPIDocsMaxBytes {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文档内容超过 256KB 上限"})
		return
	}
	if err := s.Store.SetConfig(openAPIDocsConfigKey, req.MD); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	note := "自定义文档已发布"
	if strings.TrimSpace(req.MD) == "" {
		note = "已恢复内置默认文档"
	}
	s.Store.LogAuditDiff(0, u.ID, "openapi_docs_save", "system_config", openAPIDocsConfigKey,
		`{"action":"save"}`, `{"bytes":`+itoaApi(len(req.MD))+`,"note":"`+note+`"}`)
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": note})
}

// handleAdminOpenAPIDocsPreview 超管预览渲染结果（不落库）。
func (s *Server) handleAdminOpenAPIDocsPreview(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		MD string `json:"md"` // 待预览的 Markdown 源码
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if len(req.MD) > openAPIDocsMaxBytes {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文档内容超过 256KB 上限"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "html": renderDocsHTML(req.MD)})
}

// itoaApi 整数转字符串（审计明细用）。
func itoaApi(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// defaultDocsMD 内置默认文档（Markdown 源码；openapi_docs_md 为空时使用）。
const defaultDocsMD = `# 翻译平台开放 API

所有接口使用 \Authorization: Bearer <API_KEY>\ 认证，API Key 在管理后台「API Key」面板签发。

**翻译采用异步任务模型**：提交后立即返回 task_id，按建议间隔轮询直至终态
（仅 completed / failed 为终态；未完成时 status 返回 queued / processing）。
文本任务建议 **15s** 轮询一次，文件任务建议 **60s** 轮询一次。

## 接口一览

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| POST | /openapi/v1/tasks | translate/all | 创建任务：JSON=文本；multipart=文件批量（≤20个/30MB） |
| GET | /openapi/v1/tasks/status?id= | translate/all | 轮询状态与结果 |
| GET | /openapi/v1/tasks/download?id=&file_id= | translate/all | 文件产物下载（缺省 zip 全部） |
| GET | /openapi/v1/balance | * | 查询 token 余额与 ≈句数 |
| GET | /openapi/v1/kb/stats | kb/all | 知识库条目统计 |
| GET | /openapi/v1/billing/usage | billing/all | 用量明细 |
| POST | /openapi/v1/apikey/rotate | all | 轮换 API Key（旧 Key 立即失效） |

## ① 创建文本任务

~~~
curl -X POST https://域名/openapi/v1/tasks \
  -H "Authorization: Bearer <API_KEY>" -H "Content-Type: application/json" \
  -d '{"text":"请检查制动系统","target_langs":["en","de"],"mode":"pro"}'
# ← 202 {"success":true,"task_id":123,"ticket_no":"T...","status":"queued",
#        "mode":"pro","poll_interval_sec":15,"balance_tokens":12500000,...}
~~~

## ② 创建文件批量任务（mode=fast 快速 / pro 专业校对，默认 pro）

~~~
curl -X POST https://域名/openapi/v1/tasks \
  -H "Authorization: Bearer <API_KEY>" \
  -F "files=@手册.docx" -F "files=@清单.xlsx" -F "target_langs=en,de" -F "mode=fast"
~~~

## ③ 轮询状态（文本 15s / 文件 60s）

~~~
curl "https://域名/openapi/v1/tasks/status?id=123" -H "Authorization: Bearer <API_KEY>"
# 处理中 → {"status":"processing","steps":[...]}
# 文本完成 → {"status":"completed","translations":{"en":"Check the brake system."},"tokens_used":1832}
# 文件完成 → {"status":"completed","files":[...],"download":"/openapi/v1/tasks/download?id=123"}
# 失败     → {"status":"failed","error_code":"insufficient_balance","message":"余额不足，请充值或升级套餐"}
~~~

## 错误码（独立出参 error_code）

| error_code | 含义 |
|------------|------|
| insufficient_balance | **余额不足——请充值或升级套餐** |
| rate_limited | 请求过于频繁，稍后重试 |
| daily_quota_exceeded | 达到当日用量上限 |
| bad_request / not_found / forbidden / invalid_api_key / task_failed / not_ready / no_result | 参数/权限/状态类错误 |

## 余额与计费

翻译按实际用量从账户余额扣减；每次响应携带 balance_tokens（当前余额）与
balance_sentences_approx（≈句数）。额度不足将返回错误码 insufficient_balance，
请充值或升级套餐。具体计费规则由平台管理员配置。
`

// ============ 开放 API 辅助接口（KB 统计 / 用量 / Key 轮换） ============

// handleOpenAPIKBStats 开放接口：查询本租户知识库统计（需要 kb/all 权限）。
func (s *Server) handleOpenAPIKBStats(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "error_code": "invalid_api_key", "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "kb" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "error_code": "forbidden", "message": "API Key 无知识库权限"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":    true,
		"tenant_id":  ak.TenantID,
		"kb_entries": s.kbStats(ak.TenantID),
	})
}

// kbStats 统计租户知识库条目数（安全封装：DB 为 nil 时返回 0）。
func (s *Server) kbStats(tid int64) int64 {
	if s.DB == nil {
		return 0
	}
	total, _, _, _ := s.DB.Stats(tid)
	return total
}

// handleOpenAPIUsage 开放接口：查询本租户用量与余额（需要 billing/all 权限）。
func (s *Server) handleOpenAPIUsage(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "error_code": "invalid_api_key", "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "billing" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "error_code": "forbidden", "message": "API Key 无计费权限"})
		return
	}
	usage, total, _ := s.Store.UsageStats(ak.TenantID)
	balance, _ := s.Store.GetBalance(ak.TenantID)
	resp := map[string]interface{}{
		"success":   true,
		"tenant_id": ak.TenantID,
		"usage":     usage,
		"total":     total,
	}
	if balance != nil {
		resp["balance"] = balance.Balance
		resp["balance_tokens"] = balance.Balance
		resp["balance_sentences_approx"] = balance.Balance / s.Store.TokenSentenceRate()
	}
	writeJSON(w, 200, resp)
}

// handleOpenAPIKeyRotate 开放接口：轮换本租户 API Key（传入旧 key 换取新 key）。
func (s *Server) handleOpenAPIKeyRotate(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "error_code": "invalid_api_key", "message": "API Key 无效"})
		return
	}
	// 轮换：删除旧 key 并签发同权限新 key（密钥仅明文返回一次）
	if err := s.Store.DeleteAPIKey(ak.ID, ak.TenantID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "error_code": "internal", "message": err.Error()})
		return
	}
	newKey, err := s.Store.CreateAPIKey(ak.TenantID, ak.Name, ak.Perms)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "error_code": "internal", "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "message": "API Key 已轮换，旧 Key 立即失效",
		"api_key": newKey, "name": ak.Name, "perms": ak.Perms,
	})
}

// authenticateAPIKey 从 Authorization 头解析并校验 API Key（Bearer tk_xxx）。
// 校验通过时顺带刷新最近调用时间。返回: Key 对象与是否有效。
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
