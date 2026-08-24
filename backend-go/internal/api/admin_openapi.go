// ============ admin_openapi.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
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
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"translator/internal/store"
)

// 开放 API 文档双语存储键（四期体验增强：中英两份独立维护）
const (
	openAPIDocsConfigKeyZh = "openapi_docs_md_zh"
	openAPIDocsConfigKeyEn = "openapi_docs_md_en"
)

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

// getDocsMD 取指定语言的生效源码（lang: "zh"|"en"）：对应 config 非空用之，否则回退该语言内置默认。
func (s *Server) getDocsMD(lang string) string {
	key := openAPIDocsConfigKeyZh
	def := defaultDocsMDZh
	if lang == "en" {
		key = openAPIDocsConfigKeyEn
		def = defaultDocsMDEn
	}
	if v, _ := s.Store.GetConfig(key); strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

// extractBodyInner 提取 HTML 文档 <body> 内部内容（双语容器嵌入复用）。
func extractBodyInner(htmlDoc string) string {
	low := strings.ToLower(htmlDoc)
	i := strings.Index(low, "<body>")
	j := strings.LastIndex(low, "</body>")
	if i == -1 || j == -1 || j <= i {
		return htmlDoc
	}
	return htmlDoc[i+len("<body>") : j]
}

// handleOpenAPIDocs 开放 API 文档公开页：中英双容器渲染 + 右上角语言切换
// （默认语言按浏览器 navigator.language 自动选择；切换结果记忆到 localStorage）。
func (s *Server) handleOpenAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	zhBody := extractBodyInner(renderDocsHTML(s.getDocsMD("zh")))
	enBody := extractBodyInner(renderDocsHTML(s.getDocsMD("en")))
	page := `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8">
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
.lang-switch{position:fixed;top:14px;right:18px;display:flex;gap:6px}
.lang-btn{border:1px solid #1a73e8;background:#fff;color:#1a73e8;border-radius:14px;padding:3px 12px;font-size:12.5px;cursor:pointer}
.lang-btn.on{background:#1a73e8;color:#fff}
.doc-lang{display:none}.doc-lang.show{display:block}
</style></head><body>
<div class="lang-switch">
  <button class="lang-btn on" data-l="zh" onclick="setLang('zh')">中文</button>
  <button class="lang-btn" data-l="en" onclick="setLang('en')">English</button>
</div>
<div class="doc-lang show" data-l="zh">` + zhBody + `</div>
<div class="doc-lang" data-l="en">` + enBody + `</div>
<script>
// 为每个 pre 块添加复制按钮（零依赖，直接写入剪贴板避免引号转换）
document.querySelectorAll('pre').forEach(function(pre){
  var btn=document.createElement('button');
  btn.textContent='📋 复制';
  btn.style.cssText='position:absolute;right:8px;top:6px;background:#e8eaf6;color:#1a237e;border:none;border-radius:4px;padding:3px 10px;font-size:12px;cursor:pointer';
  pre.style.position='relative';
  btn.onclick=function(){
    var text=pre.querySelector('code')?pre.querySelector('code').textContent:pre.textContent;
    navigator.clipboard.writeText(text).then(function(){btn.textContent='✅ 已复制';setTimeout(function(){btn.textContent='📋 复制'},1500)});
  };
  pre.parentNode.insertBefore(btn,pre);
});
function setLang(l){
  document.querySelectorAll('.doc-lang').forEach(function(e){e.classList.toggle('show', e.getAttribute('data-l')===l)});
  document.querySelectorAll('.lang-btn').forEach(function(b){b.classList.toggle('on', b.getAttribute('data-l')===l)});
  try{localStorage.setItem('docs_lang',l)}catch(e){}
}
(function(){
  var l=null;try{l=localStorage.getItem('docs_lang')}catch(e){}
  if(!l){l=(navigator.language||'zh').toLowerCase().indexOf('zh')===0?'zh':'en'}
  setLang(l);
})();
</script>
</body></html>`
	_, _ = w.Write([]byte(page))
}

// handleAdminOpenAPIDocsGet 超管读取中英两份文档源码与默认标记。
func (s *Server) handleAdminOpenAPIDocsGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	mdZh := s.getDocsMD("zh")
	mdEn := s.getDocsMD("en")
	s.Store.LogAudit(0, u.ID, "openapi_docs_view", "system_config", openAPIDocsConfigKeyZh+"/"+openAPIDocsConfigKeyEn)
	writeJSON(w, 200, map[string]interface{}{
		"success":    true,
		"md_zh":      mdZh,
		"md_en":      mdEn,
		"default_zh": mdZh == defaultDocsMDZh,
		"default_en": mdEn == defaultDocsMDEn,
	})
}

// handleAdminOpenAPIDocsSave 超管保存指定语言的文档源码（空串=恢复该语言内置默认）。
func (s *Server) handleAdminOpenAPIDocsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Lang string `json:"lang"` // zh | en
		MD   string `json:"md"`   // Markdown 源码（空串=恢复该语言内置默认）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	lang := strings.ToLower(strings.TrimSpace(req.Lang))
	if lang != "zh" && lang != "en" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "lang 必须为 zh 或 en"})
		return
	}
	if len(req.MD) > openAPIDocsMaxBytes {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文档内容超过 256KB 上限"})
		return
	}
	key := openAPIDocsConfigKeyZh
	if lang == "en" {
		key = openAPIDocsConfigKeyEn
	}
	if err := s.Store.SetConfig(key, req.MD); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	note := "自定义文档已发布"
	if strings.TrimSpace(req.MD) == "" {
		note = "已恢复内置默认文档"
	}
	s.Store.LogAuditDiff(0, u.ID, "openapi_docs_save", "system_config", key+"("+lang+")",
		`{"action":"save"}`, `{"bytes":`+itoaApi(len(req.MD))+`,"note":"`+note+`"}`)
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": note})
}

// handleAdminOpenAPIDocsPreview 超管预览渲染结果（不落库；lang 缺省 zh）。
func (s *Server) handleAdminOpenAPIDocsPreview(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Lang string `json:"lang"` // zh | en（缺省 zh）
		MD   string `json:"md"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if len(req.MD) > openAPIDocsMaxBytes {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文档内容超过 256KB 上限"})
		return
	}
	lang := strings.ToLower(strings.TrimSpace(req.Lang))
	if lang == "" {
		lang = "zh"
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "html": renderDocsHTML(req.MD), "lang": lang})
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
const defaultDocsMDZh = `# 翻译平台开放 API

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

## 支持的语言与计费规则

target_langs 传语言代码数组；缺省 ["en"]。

## 支持的语言与计费规则

target_langs 传语言代码数组；缺省 ["en"]。支持以下 34 种语言（Hunyuan-MT 全量）：

| 代码 | 语言 | 翻译范围 |
|------|------|----------|
| en | 英语 | 知识库匹配+AI |
| ru | 俄语 | 知识库匹配+AI |
| ar | 阿拉伯语 | 知识库匹配+AI |
| es | 西班牙语 | 知识库匹配+AI |
| pt | 葡萄牙语 | 知识库匹配+AI |
| fr | 法语 | 知识库匹配+AI |
| kk | 哈萨克语 | 知识库匹配+AI |
| de | 德语 | 知识库匹配+AI |
| zh_hant | 繁体中文 | 知识库匹配+AI |
| ja | 日语 | AI 直翻 |
| ko | 韩语 | AI 直翻 |
| th | 泰语 | AI 直翻 |
| tr | 土耳其语 | AI 直翻 |
| it | 意大利语 | AI 直翻 |
| pl | 波兰语 | AI 直翻 |
| sv | 瑞典语 | AI 直翻 |
| ms | 马来语 | AI 直翻 |
| id_lang | 印尼语 | AI 直翻 |
| vi | 越南语 | AI 直翻 |
| mn | 蒙古语 | AI 直翻 |
| nl | 荷兰语 | AI 直翻 |
| uk | 乌克兰语 | AI 直翻 |
| hi | 印地语 | AI 直翻 |
| fa | 波斯语 | AI 直翻 |
| he | 希伯来语 | AI 直翻 |
| el | 希腊语 | AI 直翻 |
| my | 缅甸语 | AI 直翻 |
| km | 高棉语 | AI 直翻 |
| lo | 老挝语 | AI 直翻 |
| tl | 菲律宾语 | AI 直翻 |
| gu | 古吉拉特语 | AI 直翻 |
| ur | 乌尔都语 | AI 直翻 |
| te | 泰卢固语 | AI 直翻 |
| mr | 马拉地语 | AI 直翻 |

**计费口径**：消耗句数 = 源句数 × 目标语言数（多语言按倍数累增）；token ≈ 句 × 500 × 均摊系数。
mode=pro 含知识库匹配与双评估审校全流水线，消耗高于 fast。

**计费口径**：消耗句数 = 源句数 × 目标语言数（多语言按倍数累增）；token ≈ 句 × 500 × 均摊系数。
mode=pro 含知识库匹配与双评估审校全流水线，消耗高于 fast。

## 文件批量任务要点

- multipart 字段名必须为 **files**，本地路径前**必须加 @**：
  curl -F "files=@/path/手册.docx" -F "files=@/path/清单.xlsx"
- 单次 ≤20 个文件、总量 ≤30MB
- 支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml
- 完成后经 download 接口下载；产物保留 14 天，请及时下载

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

// defaultDocsMDEn 英文默认文档。
const defaultDocsMDEn = `# Translation Platform Open API

All endpoints authenticate with **Authorization: Bearer YOUR_API_KEY**. Issue keys in the admin console "API Key" panel.

**Async task model**: submitting returns a task_id immediately; poll until a terminal state (only completed / failed are terminal; while pending, status is queued / processing). Poll text tasks every **15s**, file tasks every **60s**.

## Endpoints

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| POST | /openapi/v1/tasks | translate/all | Create task: JSON = text; multipart = batch files (up to 20 files / 30MB) |
| GET | /openapi/v1/tasks/status?id= | translate/all | Poll status & result |
| GET | /openapi/v1/tasks/download?id=&file_id= | translate/all | Download artifacts (zip when omitted) |
| GET | /openapi/v1/balance | * | Token balance & sentence conversion |
| GET | /openapi/v1/kb/stats | kb/all | Knowledge base statistics |
| GET | /openapi/v1/billing/usage | billing/all | Usage details |
| POST | /openapi/v1/apikey/rotate | all | Rotate API Key (old key invalidates immediately) |

## Create a text task

~~~json
POST https://host/openapi/v1/tasks
{"text":"Check the brake system.","target_langs":["en","de"],"mode":"pro"}
~~~

Response (202): {"task_id":123,"status":"queued","poll_interval_sec":15,"balance_tokens":12500000}

## Create a batch file task (mode=fast / pro, default pro)

~~~bash
curl -X POST https://host/openapi/v1/tasks -H "Authorization: Bearer YOUR_API_KEY" -F "files=@manual.docx" -F "files=@list.xlsx" -F "target_langs=en,de" -F "mode=fast"
~~~

## Poll status

~~~json
GET /openapi/v1/tasks/status?id=123
pending    -> {"status":"processing","steps":[...]}
text done  -> {"status":"completed","translations":{"en":"..."},"tokens_used":1832}
files done -> {"status":"completed","files":[...],"download":"/openapi/v1/tasks/download?id=123"}
failed     -> {"status":"failed","error_code":"insufficient_balance"}
~~~

## Supported languages & billing rules

target_langs takes an array of language codes; defaults to ["en"].

## Supported languages & billing rules

target_langs takes an array of language codes; defaults to ["en"]. Supported: 34 languages (Hunyuan-MT full set):

| Code | Language | Scope |
|------|----------|-------|
| en | English | Knowledge base + AI |
| ru | Russian | Knowledge base + AI |
| ar | Arabic | Knowledge base + AI |
| es | Spanish | Knowledge base + AI |
| pt | Portuguese | Knowledge base + AI |
| fr | French | Knowledge base + AI |
| kk | Kazakh | Knowledge base + AI |
| de | German | Knowledge base + AI |
| zh_hant | Traditional Chinese | Knowledge base + AI |
| ja | Japanese | AI direct |
| ko | Korean | AI direct |
| th | Thai | AI direct |
| tr | Turkish | AI direct |
| it | Italian | AI direct |
| pl | Polish | AI direct |
| sv | Swedish | AI direct |
| ms | Malay | AI direct |
| id_lang | Indonesian | AI direct |
| vi | Vietnamese | AI direct |
| mn | Mongolian | AI direct |
| nl | Dutch | AI direct |
| uk | Ukrainian | AI direct |
| hi | Hindi | AI direct |
| fa | Persian | AI direct |
| he | Hebrew | AI direct |
| el | Greek | AI direct |
| my | Burmese | AI direct |
| km | Khmer | AI direct |
| lo | Lao | AI direct |
| tl | Filipino | AI direct |
| gu | Gujarati | AI direct |
| ur | Urdu | AI direct |
| te | Telugu | AI direct |
| mr | Marathi | AI direct |

**Billing**: sentences consumed = source sentences × target language count (multi-target multiplies). tokens ≈ sentences × 500 × markup factor. mode=pro includes KB matching and full review pipeline, consuming more than fast.

**Billing**: sentences consumed = source sentences × target language count (multi-target multiplies). tokens ≈ sentences × 500 × markup factor. mode=pro includes KB matching and full review pipeline, consuming more than fast.

## Batch file task notes

- The multipart field must be named **files**, and local paths **must be prefixed with @**:
  curl -F "files=@/path/manual.docx" -F "files=@/path/list.xlsx"
- Up to 20 files, 30MB total per request
- Accepted: docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml
- Download via the download endpoint when completed; artifacts are kept for 14 days

## Error codes (dedicated error_code field)

| error_code | Meaning |
|------------|---------|
| insufficient_balance | Balance exhausted — top up or upgrade your plan |
| rate_limited | Too many requests, retry later |
| daily_quota_exceeded | Daily usage cap reached |
| bad_request / not_found / forbidden / invalid_api_key / task_failed / not_ready / no_result | Parameter / permission / state errors |

## Balance & Billing

Usage is deducted from your account balance based on actual consumption; every response carries balance_tokens and balance_sentences_approx. Billing rules are configured by platform administrators.
`

// ============ 开放 API 辅助接口（KB 统计 / 用量 / Key 轮换） ============

// handleOpenAPIKBStats 开放接口：查询本租户知识库统计（需要 kb/all 权限）。
func (s *Server) handleOpenAPIKBStats(w http.ResponseWriter, r *http.Request) {
	ak, authErr := s.authenticateAPIKey(r)
	if authErr != "" {
		if authErr == "key_quota_exceeded" {
			writeJSON(w, 429, map[string]interface{}{"success": false, "error_code": authErr,
				"message": "该 API Key 今日调用次数已达上限，请调整限额或明日再试"})
			return
		}
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
	ak, authErr := s.authenticateAPIKey(r)
	if authErr != "" {
		if authErr == "key_quota_exceeded" {
			writeJSON(w, 429, map[string]interface{}{"success": false, "error_code": authErr,
				"message": "该 API Key 今日调用次数已达上限，请调整限额或明日再试"})
			return
		}
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
	ak, authErr := s.authenticateAPIKey(r)
	if authErr != "" {
		if authErr == "key_quota_exceeded" {
			writeJSON(w, 429, map[string]interface{}{"success": false, "error_code": authErr,
				"message": "该 API Key 今日调用次数已达上限，请调整限额或明日再试"})
			return
		}
		writeJSON(w, 401, map[string]interface{}{"success": false, "error_code": "invalid_api_key", "message": "API Key 无效"})
		return
	}
	// 轮换：删除旧 key 并签发同权限新 key（密钥仅明文返回一次）
	if err := s.Store.DeleteAPIKey(ak.ID, ak.TenantID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "error_code": "internal", "message": err.Error()})
		return
	}
	newKey, err := s.Store.CreateAPIKey(ak.TenantID, ak.Name, ak.Perms, ak.DailyCallLimit)
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
// 校验通过时刷新最近使用时间与今日计数，并执行 R4 Key 级每日配额判定。
// 返回: (Key 对象, 错误码)。""=通过；"invalid_api_key"；"key_quota_exceeded"=当日限额已满。
func (s *Server) authenticateAPIKey(r *http.Request) (*store.APIKey, string) {
	if s.Store == nil {
		return nil, "invalid_api_key"
	}
	h := r.Header.Get("Authorization")
	if len(h) < 8 || h[:7] != "Bearer " {
		return nil, "invalid_api_key"
	}
	key := h[7:]
	ak, err := s.Store.GetAPIKeyByHash(store.HashAPIKey(key))
	if err != nil || ak.Status != "active" {
		return nil, "invalid_api_key"
	}
	// ★ R4 Key 级每日配额：limit>0 且跨日计数已重置后的今日次数 ≥ 上限 → 拒绝
	today := time.Now().Format("2006-01-02")
	if ak.DailyCallLimit > 0 {
		used := ak.CallsToday
		if ak.CallsTodayDate != today {
			used = 0 // 跨日自动清零
		}
		if used >= ak.DailyCallLimit {
			return ak, "key_quota_exceeded"
		}
	}
	s.Store.TouchAPIKey(ak.ID)
	return ak, ""
}
