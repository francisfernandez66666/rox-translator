// ============ assist_open_proxy.go · 职责说明 ============
// C 端 AI 助手挂件（访客面）的**同源转发**：`/assist-api/api/assist/*` → assist 服务。
//
// 为什么主服务要自己转一遍（★ 2026-09-22 由 e2e 用例 assist_widget_cache.spec.ts W3 暴露）：
//
//	挂件写死同源前缀 `/assist-api`（见 frontend-react/src/api/assist.ts 的 ASSIST_API），
//	此前这条前缀只存在于两处——vite dev 的 proxy 与生产 Caddy 的 `handle /assist-api/*`。
//	于是「主服务直出 dist」的形态（本地单二进制跑前端、以及发布闸门 run_uat 的
//	`uat-server -frontend frontend-react/dist`）下，一次 /assist-api 请求都落进 SPA 兜底、
//	返回一整个 index.html：挂件全程「助手暂时联系不上」，历史/令牌链路根本没有被跑过。
//	现在由本文件补齐第三条路径，三种部署形态口径一致，闸门也就真能验挂件。
//
// 安全边界（刻意做窄，避免主服务变成内网任意 HTTP 中继）：
//  1. 精确路径白名单：只有 greeting / chat / history / features 四个访客端点转发，
//     `/assist-api/api/assist/admin/*` 一律 404——管理面必须走带主站鉴权的
//     /api/admin/assist/*（见 admin_assist_proxy.go），这里绝不给旁路。
//  2. 不注入任何凭据：访客端点靠 sid+tok 自证，本层只是搬运工。
//  3. 方法只放 GET/POST，请求体限长，响应体限长（上游异常不回吐无限流）。
//  4. 上游不可达回 502 + 统一错误体，前端挂件据此走离线兜底。
//  5. 本层自己的错误分支一律走 s.writeError + apierrors 统一出口（#42 错误写法棘轮：
//     新写的文件不该再留一处内联）；上游自身返回的 4xx（例如令牌失效 401）不套壳，
//     原状态码原响应体透传——否则挂件「401 → 清本地会话 → 重新 greet」的自愈链路会被钝掉。
//
// 生产口径不变：Caddy 仍会先截走 /assist-api 直转 assist（deploy/caddy/translator.conf），
// 本文件对线上是「多一条兜底」，不改变现有链路。
// =============================================
package api

import (
	"io"
	"net/http"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/observability"
)

// assistOpenPaths 访客面转发白名单：剥掉 /assist-api 前缀后的路径 → 上游路径（值与键同形，
// 单独列出来是为了让「上游路径」这件事在一处可读、可审）。
var assistOpenPaths = map[string]bool{
	"/api/assist/greeting": true,
	"/api/assist/chat":     true,
	"/api/assist/history":  true,
	"/api/assist/features": true,
}

// assistOpenPrefix 挂件同源前缀（与前端 ASSIST_API 默认值、Caddy strip_prefix 同口径）。
const assistOpenPrefix = "/assist-api"

// assistOpenMaxBody 访客请求体上限：挂件一句话不会超过几十 KB，1MB 足够宽松又能挡住刷包。
const assistOpenMaxBody = 1 << 20

// handleAssistOpenProxy 把白名单内的访客请求原样转发到 assist 服务并回传响应。
func (s *Server) handleAssistOpenProxy(w http.ResponseWriter, r *http.Request) {
	up := strings.TrimPrefix(r.URL.Path, assistOpenPrefix)
	if up == "" || up[0] != '/' {
		up = "/" + up // 容错：/assist-apiapi/... 这类畸形路径不该被拼成合法上游
	}
	if !assistOpenPaths[up] {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "接口不存在"))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body io.Reader
	if r.Method == http.MethodPost {
		// 限长后再转发：不让浏览器侧的超大 body 直通内网上游
		body = http.MaxBytesReader(w, r.Body, assistOpenMaxBody)
	}
	target := s.assistBaseURL() + up
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		observability.Error(r.Context(), "assist 访客转发构造请求失败", "path", up, "err", err)
		s.writeError(w, r, apierrors.New(apierrors.ErrUpstreamUnavailable, "AI 助手服务暂不可用"))
		return
	}
	if r.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := assistClient.Do(req)
	if err != nil {
		// 最常见原因是 assist-server 没起或基址配错：细节进日志，响应只给可自助的口径
		observability.Error(r.Context(), "assist 访客转发失败", "path", up, "err", err)
		s.writeError(w, r, apierrors.New(apierrors.ErrUpstreamUnavailable, "AI 助手服务不可达，请稍后再试"))
		return
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		observability.Error(r.Context(), "assist 访客转发读取响应失败", "path", up, "err", err)
		s.writeError(w, r, apierrors.New(apierrors.ErrUpstreamUnavailable, "AI 助手服务响应中断，请重试"))
		return
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(payload)
}
