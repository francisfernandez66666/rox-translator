// ============ admin_assist_proxy.go · 职责说明 ============
// 主后台 ↔ AI 助手（assist）服务的**同源受管代理**（★ #34 后台 AI 助手前端重做，2026-09-21）。
//
// 为什么要代理（旧做法的三个死穴）：
//  1. 旧 AssistP 用 iframe 内嵌 assist 自带管理台，并要求把主后台取到的管理 Token
//     写进 localStorage('assist_tok') 让 iframe 自读——**凭据落到浏览器可读区**，
//     且一旦部署为跨域 iframe 就静默失效（历史上真踩过）。
//  2. iframe 里的页面不是本仓 React 组件：不吃全站主题/暗色、不进 i18n 12 语种词典、
//     不能用统一 Button/Table/Dialog，用户看到的是「两套系统拼在一起」。
//  3. 发布闸门的 Playwright 跑在主站端口上，跨到 /assist-api 需要另起反代，
//     所以「助手管理台」从来没有真正的端到端断言。
//
// 现方案：面板改为原生 React，所有读写经本文件的 /api/admin/assist/* 走主后台鉴权
// （仅超管，requireAdminUser），由**服务端**注入 X-Assist-Admin 头再转发 assist 服务；
// 管理 Token 全程不出后端进程，浏览器只见一次「来源」标记（env/db/none，不含明文）。
//
// 口径边界：
//   - 上游地址单一事实源：env ASSIST_BASE_URL > system_config.assist_base_url > 默认 127.0.0.1:8790；
//     仅允许 http/https，避免把 file:// 之类的怪值注入。
//   - fail-closed：Token 未配置或 assist 服务不可达 → 直接回**诚实状态码**的结构化错误体
//     （503 未配置 / 502 上游不可达或读中断 / 500 构造失败，★ F-64③ 批 I-10 起），
//     绝不静默回退假数据；细节进 slog 不进响应体（#37 脱敏口径）。
//   - 审计：写操作（POST/PUT/DELETE）记 assist_admin_write，**只记区域/方法/目标行 ID**，
//     请求体一律不落审计（配置项可能是 llm_api_key 明文）。
//
// 路由白名单（不做通配前缀代理，避免把主后台变成任意内网 HTTP 中继）：
//
//	GET  /api/admin/assist/status     → 探活 + Token 来源（面板顶部状态条）
//	GET/PUT  /api/admin/assist/config → assist /api/assist/admin/config
//	GET/POST/PUT/DELETE /api/admin/assist/{kb|scripts|flows|features}
//	                                → assist /api/assist/admin/{table}
//	GET  /api/admin/assist/sessions → assist /api/assist/admin/sessions
//	POST /api/admin/assist/llm-test → assist /api/assist/admin/llm/test
//
// =============================================
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"translator/internal/observability"

	apierrors "translator/internal/errors"
)

// assistBaseURLKey system_config 中存放 assist 服务基址的键（环境变量优先，便于部署侧保底）。
const assistBaseURLKey = "assist_base_url"

// assistDefaultBaseURL 缺省上游：assist-server 默认监听端口（见 cmd/assist-server 与 internal/assist/config）。
const assistDefaultBaseURL = "http://127.0.0.1:8790"

// assistProxyRoutes 白名单：主后台路径 → assist 服务路径。未登记的组合一律 404。
var assistProxyRoutes = map[string]string{
	"/api/admin/assist/config":   "/api/assist/admin/config",
	"/api/admin/assist/kb":       "/api/assist/admin/kb",
	"/api/admin/assist/scripts":  "/api/assist/admin/scripts",
	"/api/admin/assist/flows":    "/api/assist/admin/flows",
	"/api/admin/assist/features": "/api/assist/admin/features",
	"/api/admin/assist/sessions": "/api/assist/admin/sessions",
	"/api/admin/assist/llm-test": "/api/assist/admin/llm/test",
}

// assistProxyAreas 主后台路径 → 区域名（审计 detail 用，避免把上游内部路径写进审计噪声）。
var assistProxyAreas = map[string]string{
	"/api/admin/assist/config":   "config",
	"/api/admin/assist/kb":       "kb",
	"/api/admin/assist/scripts":  "scripts",
	"/api/admin/assist/flows":    "flows",
	"/api/admin/assist/features": "features",
	"/api/admin/assist/sessions": "sessions",
	"/api/admin/assist/llm-test": "llm_test",
}

// assistBaseURL 解析生效的上游基址：env > 库内配置 > 默认；非法 scheme 或空值回落默认。
// 返回去掉尾部斜杠的绝对地址（后续直接拼路径）。
func (s *Server) assistBaseURL() string {
	cand := strings.TrimSpace(os.Getenv("ASSIST_BASE_URL"))
	if cand == "" {
		if v, err := s.Store.GetConfig(assistBaseURLKey); err == nil {
			cand = strings.TrimSpace(v)
		}
	}
	if cand == "" {
		return assistDefaultBaseURL
	}
	u, err := url.Parse(cand)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return assistDefaultBaseURL
	}
	return strings.TrimRight(cand, "/")
}

// assistClient 共享 HTTP 客户端：30s 超时覆盖「测试连通」这类要打真实 LLM 的慢调用，
// 同时给连接池设上限，避免面板刷新时打爆上游。
var assistClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{MaxIdleConnsPerHost: 8},
}

// handleAdminAssistProxy 白名单转发（除 status 外的全部 assist 管理面读写都走这里）。
//
// ★ F-64③（批 I-10）状态码口径：本函数有两类失败，只有一类「透传」豁免。
//   - 上游（assist 服务）的业务/系统失败：文件末尾 `w.WriteHeader(resp.StatusCode)` 原样透传，
//     那是上游的状态码，本层不重新造错误体，符合 AGENTS §一·8 的白名单口径；
//   - 本层自己的失败（越权 403、路由未登记 404、Token 未配置 503、请求构造失败 500、
//     上游不可达/读中断 502）：一律走 s.writeError 带 code/trace_id。以前这几支回
//     「HTTP 200 + success:false」，面板靠读 body 才看得出来，监控与 SDK 按状态码分支一律判成成功
//     ——assist 全挂时全站 5xx 率仍是 0%，比界面更难发现。文案一字未改
//     （AssistP 的 bizFail 取 e.message，仍显示同一句处置指引）。
func (s *Server) handleAdminAssistProxy(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 鉴权失败按「未登录 401 / 等级不足 403」分流（★ F-64③ 批 I-10：同 writeAuthzError 口径，
		// 旧写法两种情况都压成 403，token 过期的超管会被留在页面上反复撞「无权限」而看不见「请重登」）
		s.writeAuthzError(w, r, err)
		return
	}
	upstream, ok := assistProxyRoutes[r.URL.Path]
	if !ok {
		// 路由不在白名单＝该路径本层没有对应上游（404），不是「查无数据」
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "接口不存在"))
		return
	}
	tok, src := s.effectiveAssistToken()
	if tok == "" {
		// fail-closed：没有管理凭据时不转发，也不给「空 Token 试试看」的机会。
		// 取 503：这是**依赖未就绪**（保存 Token 或配 env 后即恢复），不是客户端请求有误，
		// 给 400 会让调用点误以为改请求就能通。
		s.writeError(w, r, apierrors.New(apierrors.ErrServiceUnavailable, "AI 助手管理 Token 未配置（当前来源："+src+"）：请在主后台「AI 助手 · 设置」中保存 Token，或为 assist-server 配置环境变量 ASSIST_ADMIN_TOKEN 后重启"))
		return
	}
	// 转发查询串：剥掉 admin_token（assist 侧支持 query 传凭据，但我们只走请求头，
	// 否则凭据会出现在浏览器历史、访问日志与 Referer 里）
	q := r.URL.Query()
	q.Del("admin_token")
	target := s.assistBaseURL() + upstream
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	var body io.Reader
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		// 流式透传：assist 管理面 payload 都很小（一条记录/一组配置），不必先读进内存
		body = r.Body
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		// 构造请求失败＝本进程编程/配置错误（URL 非法等），与客户端无关 ⇒ 500；细节只进 slog（#37 脱敏口径）
		observability.Error(r.Context(), "assist 代理构造请求失败", "path", r.URL.Path, "err", err)
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "请求无法转发到 AI 助手服务"))
		return
	}
	req.Header.Set("X-Assist-Admin", tok)
	if r.Method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := assistClient.Do(req)
	if err != nil {
		// 上游不可达最常见的原因是 assist-server 没起 / 基址配错——把可自助的处置写进提示。
		// 取 502（ErrUpstreamUnavailable）：本层是网关方，故障在上游连接环节，
		// 给 503 会把告警口径指错方向（503 在本仓表示「本服务依赖未就绪」）。
		observability.Error(r.Context(), "assist 代理转发失败", "path", r.URL.Path, "target", target, "err", err)
		s.writeError(w, r, apierrors.New(apierrors.ErrUpstreamUnavailable,
			"AI 助手服务不可达（基址 "+s.assistBaseURL()+"）：请确认 assist-server 已启动，或在部署侧调整 ASSIST_BASE_URL"))
		return
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MB 上限：管理面不可能更大，防上游异常撑爆内存
	if err != nil {
		// 连接已建立但读响应中断＝上游侧故障，同样 502；「请重试」是原话，不改文案
		observability.Error(r.Context(), "assist 代理读取响应失败", "path", r.URL.Path, "err", err)
		s.writeError(w, r, apierrors.New(apierrors.ErrUpstreamUnavailable, "AI 助手服务响应中断，请重试"))
		return
	}
	if resp.StatusCode >= 400 {
		observability.Error(r.Context(), "assist 代理上游返回错误", "path", r.URL.Path, "status", resp.StatusCode)
	}
	// 写操作留审计（不含请求体：配置项可能是 llm_api_key）
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
		detail := r.Method + " " + assistProxyAreas[r.URL.Path]
		if id := q.Get("id"); id != "" {
			detail += " id=" + id
		}
		s.Store.LogAudit(0, u.ID, "assist_admin_write", "assist", detail)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(payload)
}

// handleAdminAssistStatus 面板状态条数据源：上游是否可达 + 管理 Token 来源 + 生效基址。
// 只回「来源」不回 Token 明文（连掩码都不需要，面板无展示价值）。
func (s *Server) handleAdminAssistStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		// 与代理入口同一分流（★ F-64③ 批 I-10）：未登录 401、等级不足 403，面板据此决定是弹重登还是报无权限
		s.writeAuthzError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		// 方法不支持＝405（ErrMethodNotAllowed），本口径见 AGENTS §一·8
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	base := s.assistBaseURL()
	_, tokSrc := s.effectiveAssistToken()
	reachable, healthErr := s.probeAssist(base)
	msg := ""
	switch {
	case !reachable:
		msg = "AI 助手服务不可达：" + healthErr
	case tokSrc == "none":
		msg = "AI 助手服务在线，但管理 Token 未配置，管理面读写暂不可用"
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":   true,
		"base_url":  base,
		"reachable": reachable,
		"token_src": tokSrc, // env / db / none
		"message":   msg,
	})
}

// assistAdminPutConfig 服务端→assist 的内部配置写入（★ 〇-LK：管理 Token 保存后同步）。
// 与面板代理转发的区别：调用方是主后台自己（不经浏览器、不带主站鉴权），
// 凭据由参数给出而非读生效链——因为 Token 刚被改掉， assist 侧认的还是旧值。
// 参数：cred=assist 当前接受的 Token；body=assist /admin/config 的 PUT 报文。
// 返回：是否写入成功；失败原因只进日志不进响应（Token 相关细节不外泄）。
func (s *Server) assistAdminPutConfig(ctx context.Context, cred, body string) (bool, error) {
	if strings.TrimSpace(cred) == "" {
		return false, nil // 无凭据可推：等同于「需要重启 assist」，不是错误
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.assistBaseURL()+"/api/assist/admin/config", strings.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Assist-Admin", cred)
	req.Header.Set("Content-Type", "application/json")
	resp, err := assistClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return false, errors.New("assist 返回 " + strconv.Itoa(resp.StatusCode))
	}
	// 上游明确 skipped（掩码回写拦截）不算同步成功，前端据此提示「助手侧尚未生效」
	var out map[string]any
	_ = json.Unmarshal(payload, &out)
	if skipped, _ := out["skipped"].(bool); skipped {
		return false, nil
	}
	return true, nil
}

// probeAssist 探活上游 /health（2s 超时，面板挂载即调用，不能拖慢首屏）。
// 返回 (是否可达, 可直接展示的原因)——原因只描述「没起/超时/状态码」，不外泄内部错误细节。
func (s *Server) probeAssist(base string) (bool, string) {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(base + "/health")
	if err != nil {
		return false, "连接失败或服务未启动"
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return false, "健康检查返回 " + strconv.Itoa(resp.StatusCode)
	}
	return true, ""
}
