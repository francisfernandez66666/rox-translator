// Package api HTTP 服务：C 端接待接口 + 超管管理接口 + 管理页静态托管。
package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"translator/internal/assist/engine"
	"translator/internal/assist/store"
	"translator/internal/assist/web"
	"translator/internal/observability"
)

// Server HTTP 服务
type Server struct {
	db   *store.DB
	eng  *engine.Engine
	adm  string // 启动期解析到的管理 Token（env 或主库桥接），是候选之一而非唯一
	cors string
	// sessKey 会话能力令牌（tok）的 HMAC 密钥。
	// ★ 〇-LK（2026-09-22）口径变更：密钥**不再由管理 Token 派生**，而是首次启动随机生成
	//   并持久化到自身 configs.sess_key。老做法把两件事绑在一起，导致「换管理 Token /
	//   重启服务 = 所有访客的 tok 同时作废」，挂件历史随之读不出来（用户反馈的
	//   「刷新一次页面就没了」的后端根因）。会话密钥与管理员凭据本就互不相干，分开存放后
	//   轮换 Token 只影响管理面，访客会话不受牵连。
	sessKey []byte
	// ★ 〇-LK 管理 Token 热生效：生效值 = configs.admin_token（主后台面板托管）优先，
	//   其次启动快照（env ASSIST_ADMIN_TOKEN / 主库桥接）。带 TTL 缓存，
	//   面板推送写入 configs 后立即失效 → 改完即用，不必重启服务。
	tokMu sync.RWMutex
	tok   string
	tokAt time.Time
	// ★ 〇-AM 匿名会话过期清理：定期删除已过期匿名用户及其孤儿消息。
	anonymCleanupInterval time.Duration
}

// adminTokenTTL 管理 Token 的缓存时长。取 60s 与 engine 的 LLM 配置、store 的词表缓存同量级：
// 既让「面板保存后最迟一分钟自然生效」（推送不到的兜底路径），也不给每个管理请求都加一次 SQLite 读。
const adminTokenTTL = 60 * time.Second

// NewServer 构建
func NewServer(db *store.DB, eng *engine.Engine, adminToken, cors string) *Server {
	s := &Server{db: db, eng: eng, adm: adminToken, cors: cors}
	// 会话密钥：读已持久化的 configs.sess_key；缺失则随机生成并落库（幂等，重启后老访客仍可用）
	s.sessKey = loadOrGenSessKey(db)
	s.tok = s.readAdminToken()
	s.anonymCleanupInterval = time.Hour // ★ 〇-AM：每小时清理一次过期匿名会话
	go s.startAnonymCleanup()            // 启动后台清理 goroutine
	return s
}

// startAnonymCleanup 定期清除过期的匿名用户及其孤儿消息。
// ★ 〇-AM：匿名用户超过 3 天未活跃 → expires_at ≤ NOW() → 自动清理。每 1h 扫一次，每次 100 行批量删除。
func (s *Server) startAnonymCleanup() {
	ticker := time.NewTicker(s.anonymCleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		total := 0
		for total == 0 {
			var err error
			total, err = s.db.CleanupExpiredAnonymous(100)
			if err != nil {
				slog.Error("assist.api 过期匿名会话清理失败", "err", err)
				break
			}
		}
		if total > 0 {
			slog.Info("assist.api 过期匿名会话清理完成", "deleted_msgs", total)
		}
	}
}

// loadOrGenSessKey 取会话 HMAC 密钥（64 位 hex 存储，32 字节裸钥）。
// 参数 db=assist 存储。返回：32 字节密钥；库里没有/格式损坏时生成新的并写回。
func loadOrGenSessKey(db *store.DB) []byte {
	if v := strings.TrimSpace(db.GetConfig("sess_key", "")); v != "" {
		if b, err := hex.DecodeString(v); err == nil && len(b) == 32 {
			return b
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("assist.api 会话密钥初始化失败: " + err.Error())
	}
	if err := db.SetConfig("sess_key", hex.EncodeToString(b)); err != nil {
		// 落库失败只影响「重启后老会话要重新 greet」，不影响本次运行，记日志不阻断启动
		slog.Warn("assist 会话密钥持久化失败（本次运行仍可用，重启后访客需重新开场）", "err", err)
	}
	return b
}

// readAdminToken 直读当前生效的管理 Token：configs.admin_token > 启动快照。
// 为什么库内值压过 env：这是「面板可管理」的前提——env 是部署侧保底，
// 若它一直压着面板保存的值，改 Token 就永远要重启（与主后台模型配置「后台优先于环境变量」同口径）。
func (s *Server) readAdminToken() string {
	if v := strings.TrimSpace(s.db.GetConfig("admin_token", "")); v != "" {
		return v
	}
	return strings.TrimSpace(s.adm)
}

// adminToken 带 TTL 的生效 Token（可能为空 = 管理面关闭）。
func (s *Server) adminToken() string {
	s.tokMu.RLock()
	cached, fresh := s.tok, time.Since(s.tokAt) < adminTokenTTL
	s.tokMu.RUnlock()
	if fresh {
		return cached
	}
	v := s.readAdminToken()
	s.tokMu.Lock()
	s.tok, s.tokAt = v, time.Now()
	s.tokMu.Unlock()
	return v
}

// invalidateAdminToken 让缓存立即失效（configs.admin_token 刚被改过）。
func (s *Server) invalidateAdminToken() {
	s.tokMu.Lock()
	s.tokAt = time.Time{}
	s.tokMu.Unlock()
}

// sessTok 计算会话能力令牌：HMAC-SHA256(sessKey, sid) 的 hex。
// 参数 sid: 会话 ID。返回: 64 位 hex 令牌；仅持有者（服务端签发）可用该 sid 收发消息。
func (s *Server) sessTok(sid string) string {
	m := hmac.New(sha256.New, s.sessKey)
	m.Write([]byte(sid))
	return hex.EncodeToString(m.Sum(nil))
}

// validSess 常数时间校验 sid 对应的能力令牌。
// 参数 sid/tok: 会话 ID 与待验令牌。返回: 是否有效。
func (s *Server) validSess(sid, tok string) bool {
	want := s.sessTok(sid)
	return subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1
}

// Handler 汇总路由
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// C 端（挂件调用，免登录；会话以 sid+tok 能力令牌自证，★ P0-1）
	mux.HandleFunc("/api/assist/greeting", s.handleGreeting)
	mux.HandleFunc("/api/assist/chat", s.handleChat)
	mux.HandleFunc("/api/assist/history", s.handleHistory)
	mux.HandleFunc("/api/assist/features", s.handleFeatures)

	// 管理端（token 鉴权）
	mux.HandleFunc("/api/assist/admin/config", s.guard(s.handleConfig))
	mux.HandleFunc("/api/assist/admin/kb", s.guard(s.handleTable("kb_entries")))
	mux.HandleFunc("/api/assist/admin/scripts", s.guard(s.handleTable("scripts")))
	mux.HandleFunc("/api/assist/admin/flows", s.guard(s.handleTable("flows")))
	mux.HandleFunc("/api/assist/admin/features", s.guard(s.handleTable("feature_links")))
	mux.HandleFunc("/api/assist/admin/sessions", s.guard(s.handleSessions))
	mux.HandleFunc("/api/assist/admin/llm/test", s.guard(s.handleLLMTest)) // ★ R0.4c 测试连通
	// ★ 081x（2026-09-29）：系统现值接线体检（只读）。价格/语种数改成了「取不到就不说」的软路径，
	// 软路径失败界面上一模一样，必须有个地方能看见拨的是哪个地址、到底取到没有。
	mux.HandleFunc("/api/assist/admin/system-values", s.guard(s.handleSystemValues))

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "time": time.Now().Format(time.RFC3339)})
	})

	// 管理页静态托管
	mux.HandleFunc("/assist/admin", s.adminPage)

	return s.corsMW(mux)
}

// ============================================================
// 中间件
// ============================================================

// corsMW 跨域中间件：按白名单回写 Origin 并透传预检
func (s *Server) corsMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if s.cors == "*" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else {
				for _, o := range strings.Split(s.cors, ",") {
					if strings.TrimSpace(o) == origin {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						break
					}
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-Assist-Admin")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// guard 管理端鉴权（X-Assist-Admin 头，常数时间比较）。
// ★ 〇-LK：生效值走 adminToken()（configs 优先、带 TTL），因此主后台面板保存新 Token 后
// 这里即刻放行，不再「改一次 Token 重启一次服务」。
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ★ P0-2 纵深防御：服务端未配置 Token 时管理面一律 401（防止空生效值与空请求头
		// 在 ConstantTimeCompare 下相等而误放行）
		want := s.adminToken()
		if want == "" {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		tok := r.Header.Get("X-Assist-Admin")
		if tok == "" {
			tok = r.URL.Query().Get("admin_token")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(want)) != 1 {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// ============================================================
// C 端接口
// ============================================================

// newSessionID 生成会话 ID：时间前缀（便于排查）+ crypto/rand 8 字节 hex。
// ★ P0-1（2026-09-18）：旧实现用「时间^pid」做种子的 LCG 伪随机，同窗口 sid 可预测，
// 已换密码学随机源。
func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("s%d", time.Now().UnixNano()) // 理论不可达；退化仍保证唯一
	}
	return fmt.Sprintf("s%d%s", time.Now().UnixMilli(), hex.EncodeToString(b))
}

// authCtx 请求中解析出的认证上下文（零值 = 匿名用户）。
type authCtx struct {
	tenantID int64
	userID   int64
	token    string
}

// extractAuthFromRequest 从请求中提取认证信息。
// ★ 〇-AM：登录态优先（Authorization Bearer token → 主后台解析 tenant+user），
// 未登录则返回空值，由调用方走匿名指纹流程。
func (s *Server) extractAuthFromRequest(r *http.Request) authCtx {
	// ① Authorization: Bearer <token> → 调主后台 API 获取 user info
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if userID, tenantID := s.resolveTokenUser(token); userID > 0 {
			return authCtx{tenantID: tenantID, userID: userID, token: token}
		}
	}
	// ② X-Tenant-ID header（前端已登录时附带）
	if tidStr := r.Header.Get("X-Tenant-ID"); tidStr != "" {
		var tid int64
		if tid == 0 {
			fmt.Sscanf(tidStr, "%d", &tid)
		}
		// 没有 token 但带了 tenant_id → 仍需验证有效性，此处暂不处理
	}
	return authCtx{}
}

// resolveTokenUser 通过主后台 API 解析 JWT token → (userID, tenantID)。
// 失败返回 (0,0)，调用方视为匿名用户。
func (s *Server) resolveTokenUser(token string) (int64, int64) {
	// TODO: 这里需要调用主后台 /api/auth/me 或类似端点解析 token。
	// 在 assist-server 独立部署的场景下，暂时无法直接校验主站 token。
	// 实际方案：main backend 的 /api/assist/chat 代理转发时携带用户信息到 assist-server。
	return 0, 0
}

// clientIP 提取客户端真实 IP（考虑 X-Forwarded-For / X-Real-IP / Proxy-Client-IP）。
func clientIP(r *http.Request) string {
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP", "Proxy-Client-IP", "WL-Client-IP"} {
		if ip := r.Header.Get(h); ip != "" {
			// X-Forwarded-For 可能逗号分隔多个 IP，取第一个
			if idx := strings.Index(ip, ","); idx > 0 {
				ip = ip[:idx]
			}
			return strings.TrimSpace(ip)
		}
	}
	// fallback: RemoteAddr 格式为 "host:port"
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// sessMu session upsert 竞态保护（低并发足够；mutex 护 CreateSession 读-建窗口）
var sessMu sync.Mutex // session upsert 竞态保护（低并发足够）

// ensureSession 读取或创建会话（★ 〇-AM：auth-aware，支持登录态挂钩 + 匿名指纹）。
func (s *Server) ensureSession(ctx context.Context, id, pageURL string, tenantID, userID int64, anonymHash string) (store.Row, bool) {
	sessMu.Lock()
	defer sessMu.Unlock()
	// ① 先查是否已有同名会话（兼容旧 sid）
	if rows, err := s.db.SessionRow(id); err == nil && rows != nil {
		return rows, false
	}
	// ② 登录态 → 按 tenant+user 查是否有未绑定到该 sid 的会话
	if tenantID > 0 && userID > 0 {
		if resolved, err := s.db.ResolveSessionByAuth(tenantID, userID, ""); err == nil && resolved != nil {
			// 把已存在的老会话更新到这个新 sid 上（保留历史数据）
			_ = s.db.MergeSessions(resolved["id"].(string), id)
			if rows, _ := s.db.SessionRow(id); rows != nil {
				return rows, true
			}
		}
	}
	// ③ 插入新会话（auth-aware）
	if err := s.db.CreateSession(id, pageURL, tenantID, userID, anonymHash); err != nil {
		observability.Error(ctx, "assist.api 会话创建失败", "err", err)
	}
	if rows, _ := s.db.SessionRow(id); rows != nil {
		return rows, true
	}
	return nil, false
}

// resolveOldSessionToNew 将旧 sid 的数据合并到新 sid（★ 〇-AM：通过 store.DB.MergeSessions 调用）。
func (s *Server) resolveOldSessionToNew(oldSID, newSID string, pageURL string, tenantID, userID int64) {
	_ = s.db.MergeSessions(oldSID, newSID)
}

// generateAnonHash 用请求特征生成匿名指纹（SHA256 前 16 hex）。
// ★ 〇-AM：匿名用户通过 (UA+IP+page_url) 做指纹，实现同设备同浏览器跨租户复用会话。
func generateAnonHash(userAgent, ipAddr, pageURL string) string {
	hash := sha256.Sum256([]byte(userAgent + "|" + ipAddr + "|" + pageURL))
	return hex.EncodeToString(hash[:8]) // 取前 8 字节 = 16 hex 字符，碰撞概率极低
}

// handleGreeting GET/POST /api/assist/greeting?session=&tok=&page= → 欢迎词 + 会话 id + 能力令牌 + 快捷提问
// ★ P0-1：入参 session 仅在 tok 校验通过时复用（老访客续会话）；否则一律新开
// （防伪造 sid 蹭他人上下文）。响应新增 tok，前端与 sid 同存。
// ★ 〇-AM：从请求头提取 Authorization / X-Tenant-ID，已登录用户自动挂钩 tenant_id/user_id。
func (s *Server) handleGreeting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "method"})
		return
	}
	sid := strings.TrimSpace(r.URL.Query().Get("session"))
	page := strings.TrimSpace(r.URL.Query().Get("page"))
	if sid == "" || !s.validSess(sid, strings.TrimSpace(r.URL.Query().Get("tok"))) {
		sid = newSessionID()
	}
	// ★ 〇-AM：解析认证信息（登录态挂 tenant+user，未登录用 anon_hash）
	authInfo := s.extractAuthFromRequest(r)
	anonymHash := ""
	if authInfo.tenantID == 0 && authInfo.userID == 0 {
		anonymHash = generateAnonHash(r.UserAgent(), clientIP(r), page)
	}
	s.ensureSession(r.Context(), sid, page, authInfo.tenantID, authInfo.userID, anonymHash)
	// ★ 082x（2026-09-29，用户指令「不能根据用户的前台语言和使用语言来回复，一律用中文」）：
	// greet 新增 lang（访客界面语言）。欢迎词与 chips 是**不走对话模型**的直出文本，
	// 英文站访客打开挂件看到的第一口气就是这两行中文，所以它们要单独过一层按需翻译
	// （见 engine/localize.go；中文界面零开销、翻不到就原样出中文）。
	uiLang := normalizeUILang(r.URL.Query().Get("lang"))
	text := s.db.GetConfig("welcome", "")
	if text == "" {
		text = s.eng.Greeting()
	}
	// ★ 0AF（2026-10-01 现网 greet 502）：欢迎词与 chips 是**两次串行**翻译，各有各的有界预算的话
	// 最坏是 2×预算（8+8=16 秒）——离反代那 30 秒是不远了，但访客白等的那 8 秒没有任何收益。
	// 这里让整段 greet 共用**一个**截止：第二枪只花剩余预算，拿不到就照常出中文并在后台补
	// （见 engine/localize_async.go）。engine 侧那条有界 ctx 取两者里更早的 deadline，所以不会放宽。
	greetCtx, cancelGreet := context.WithTimeout(r.Context(), s.eng.CannedSyncBudget())
	defer cancelGreet()
	text = s.eng.LocalizeGreeting(greetCtx, text, uiLang)
	// ★ 〇-LK（2026-09-22）欢迎语去重：旧实现每次 greeting 都无条件 AddMessage，
	// 而挂件在同一 sid 上重复 greet 是常态（令牌失效自愈、跨页复用会话），
	// 于是台账里堆出一串重复欢迎语：既让管理台「消息总数」虚高，也让
	// history 恢复时看到好几条一模一样的开场白。已有消息的会话只回文本、不再落库。
	if rows, _ := s.db.History(sid, 1); len(rows) == 0 {
		// ★ 〇-AM：欢迎语落库时携带 auth/anon 上下文
		_ = s.db.AddMessageWithAuth(sid, "assistant", text, nil, authInfo.tenantID, authInfo.userID, anonymHash, "")
	}
	writeJSON(w, 200, map[string]any{
		"session":  sid,
		"tok":      s.sessTok(sid),
		"greeting": text,
		"chips":    chipsOf(s.eng.LocalizeChips(greetCtx, s.db.GetConfig("quick_chips", ""), uiLang)),
	})
}

// chipsOf 逗号分隔配置转快捷提问切片
func chipsOf(s string) []string {
	parts := strings.Split(s, ",")
	out := []string{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// handleChat POST /api/assist/chat {session, tok, message, page}
// ★ P0-1：tok 校验失败按 401 拒绝（前端走「重新 greet」自愈路径），不再允许任意写他人会话。
// ★ 〇-AM：auth-aware，已登录用户自动挂钩 tenant_id/user_id，匿名用户使用 anon_hash + expires_at。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "method"})
		return
	}
	var req struct {
		Session string `json:"session"`
		Tok     string `json:"tok"`
		Message string `json:"message"`
		Page    string `json:"page"`
		// Lang 访客界面语言（★ 082x 新增，见 handleGreeting 同处注释）。
		// 缺省/未知一律按中文处理，**不返 400**：这是公开挂件面，
		// 老缓存包和 082x 之前的前端就是不带这个字段（同 F-79「新增防护不拿可用性交押金」口径）。
		Lang string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad json"})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Session == "" || req.Message == "" {
		writeJSON(w, 400, map[string]any{"error": "session and message required"})
		return
	}
	if !s.validSess(req.Session, strings.TrimSpace(req.Tok)) {
		writeJSON(w, 401, map[string]any{"error": "invalid session"})
		return
	}
	if len(req.Message) > 2000 {
		req.Message = req.Message[:2000]
	}
	sid := req.Session
	// ★ 〇-AM：解析认证信息
	authInfo := s.extractAuthFromRequest(r)
	anonymHash := ""
	if authInfo.tenantID == 0 && authInfo.userID == 0 {
		anonymHash = generateAnonHash(r.UserAgent(), clientIP(r), req.Page)
	}
	s.ensureSession(r.Context(), sid, req.Page, authInfo.tenantID, authInfo.userID, anonymHash)

	uiLang := normalizeUILang(req.Lang)
	// 写入用户消息时携带 auth/anon 上下文
	_ = s.db.AddMessageWithAuth(sid, "user", req.Message, nil, authInfo.tenantID, authInfo.userID, anonymHash, "")
	history, _ := s.db.History(sid, 12)

	rep := s.eng.Respond(r.Context(), sid, req.Message, req.Page, uiLang, history)
	_ = s.db.AddMessageWithAuth(sid, "assistant", rep.Content, actionMaps(rep.Actions), authInfo.tenantID, authInfo.userID, anonymHash, "")
	_ = s.db.TouchSession(sid)

	writeJSON(w, 200, map[string]any{
		"reply":   rep.Content,
		"actions": rep.Actions,
		"model":   rep.Model,
		"source":  rep.Source,
		// ★ 082x：这一条答案是模型按访客语言写的，还是我们出站补翻的。
		// 挂件不渲染它，但排障时必须有（没有它，「语言问题修好了」和「全靠补翻兜着」在界面上长一样）。
		"lang_localized": rep.LangLocalized,
	})
}

// actionMaps 动作按钮转 JSON 落库结构
func actionMaps(as []engine.Action) []map[string]string {
	if len(as) == 0 {
		return nil
	}
	out := make([]map[string]string, 0, len(as))
	for _, a := range as {
		out = append(out, map[string]string{"key": a.Key, "name": a.Name, "url": a.URL, "ftype": a.FType, "icon": a.Icon})
	}
	return out
}

// handleHistory GET /api/assist/history?session=&tok=&limit=20
// ★ P0-1（2026-09-18）：旧实现无任何归属校验，拿到/猜到 sid 即可读全部对话；
// 现要求 greet 下发的能力令牌 tok（HMAC(sid)）匹配才返回。
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimSpace(r.URL.Query().Get("session"))
	if sid == "" {
		writeJSON(w, 400, map[string]any{"error": "session required"})
		return
	}
	if !s.validSess(sid, strings.TrimSpace(r.URL.Query().Get("tok"))) {
		writeJSON(w, 401, map[string]any{"error": "invalid session"})
		return
	}
	limit := 20
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	msgs, err := s.db.History(sid, limit)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "db"})
		return
	}
	if msgs == nil {
		msgs = []store.Row{}
	}
	writeJSON(w, 200, map[string]any{"messages": msgs})
}

// handleFeatures GET /api/assist/features → enabled 功能入口
func (s *Server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.List("feature_links", true)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "db"})
		return
	}
	if rows == nil {
		rows = []store.Row{}
	}
	writeJSON(w, 200, map[string]any{"features": rows})
}

// ============================================================
// 管理端
// ============================================================

// configKeyWhitelist 管理端可写的配置键白名单：
//   - UI 五项：welcome/persona/temperature/max_tokens/quick_chips
//     ★ 080x（2026-09-29）新增 tone_rules「说话方式」：现网实证 temperature 不是音色旋钮
//     （0.7 与 1.0 两条回复同四件事、同顺序、同长度，只换词的摆放），真正的旋钮是系统提示词里
//     那段语气规范。它此前硬编码在 engine.go，运营想改个口气必须发一次版——挪进配置后改完即生效。
//   - ★ LLM 四项（R0.4）：base_url/api_key/model/model_backup 允许后台在线配置，
//     engine 侧配合惰性重建实现热加载；api_key_backup 复用主 Key 故不单列。
//     env（ASSIST_LLM_*）显式配置优先于 configs 表（见 engine.llmClient）。
var configKeyWhitelist = map[string]bool{
	"welcome": true, "persona": true, "tone_rules": true, "temperature": true, "max_tokens": true, "quick_chips": true,
	// ★ 081x（2026-09-29，用户指令「严格按系统能力和承诺来，不造额外承诺」）：
	//   promise_rules 与 tone_rules **刻意分成两个键**——管理台改语气是整段替换，
	//   事实闸要是住在语气段里就会被一次改口一起擦掉（见 engine/promise.go 文件头）。
	//   main_base_url 是现值注入拨的主服务地址（价格系数/语种数取实时值，不落在知识文案里）。
	"promise_rules": true, "main_base_url": true,
	"llm_base_url": true, "llm_api_key": true, "llm_model": true, "llm_model_backup": true,
	// R0.1 同义词归一表（逗号分隔：词=同义词1|同义词2，多组换行）
	"synonyms": true,
	// ★ 分级召回第 3 级（默认关闭）：embed_recall=on 启用向量召回，
	// embed_model 为 OpenAI 兼容嵌入模型名（凭证复用 llm_base_url/llm_api_key）。
	"embed_recall": true, "embed_model": true,
	// ★ 〇-LK（2026-09-22）：管理 Token 也纳入可后台配置项，与 llm_api_key 同一套做法
	//（掩码回显 + 掩码不回写 + 写完立即失效缓存）。写入需先通过 guard，
	// 所以这条白名单只对「已经持有旧 Token 的调用方（主后台代理）」开放，不会降低门槛。
	"admin_token": true,
}

// maskedCfgKeys 读取时按掩码回显的配置键（明文绝不进管理面响应体）。
var maskedCfgKeys = map[string]bool{"llm_api_key": true, "admin_token": true}

// hiddenCfgKeys 完全不在管理面列出的内部键：sess_key 是访客会话令牌的 HMAC 密钥，
// 展示它对运维没有价值，却会把「凭据列表」变长一格。
var hiddenCfgKeys = map[string]bool{"sess_key": true}

// handleConfig GET 读取 / PUT 写入单项配置
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := s.db.AllConfigs()
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "db"})
			return
		}
		if rows == nil {
			rows = []store.Row{}
		}
		// 敏感项掩码回显（防管理台/日志泄露明文），与主站 models 掩码口径一致：
		// 内部键（sess_key）整行剔除，密钥类只回掩码。
		out := make([]store.Row, 0, len(rows))
		for _, raw := range rows {
			row := store.Row(raw)
			k, _ := row["key"].(string)
			if hiddenCfgKeys[k] {
				continue
			}
			if maskedCfgKeys[k] {
				if v, _ := row["value"].(string); v != "" {
					row["value"] = maskSecret(v)
				}
			}
			out = append(out, row)
		}
		writeJSON(w, 200, map[string]any{"configs": out})
	case http.MethodPut:
		var req struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
			writeJSON(w, 400, map[string]any{"error": "key required"})
			return
		}
		// 白名单闸：未登记 key 一律 400（防任意 upsert 静默无效的陷阱）
		if !configKeyWhitelist[req.Key] {
			writeJSON(w, 400, map[string]any{"error": "key not allowed: " + req.Key})
			return
		}
		// 空值清除必须走主后台的显式 clear 流程：configs.admin_token 优先于启动快照，
		// 若允许这里写空串等于给了一个「一把关掉管理面」的半吊子口子。
		if req.Key == "admin_token" && strings.TrimSpace(req.Value) == "" {
			writeJSON(w, 400, map[string]any{"error": "value not allowed for key: " + req.Key})
			return
		}
		// 掩码值回写拦截：管理台保存时若 value 仍是掩码形态，视为未修改，跳过写库
		if maskedCfgKeys[req.Key] && isMaskedSecret(req.Value) {
			writeJSON(w, 200, map[string]any{"ok": true, "skipped": true})
			return
		}
		if err := s.db.SetConfig(req.Key, req.Value); err != nil {
			observability.Error(r.Context(), "assist.api 配置写入失败", "key", req.Key, "err", err)
			writeJSON(w, 500, map[string]any{"error": "db"})
			return
		}
		// ★ 〇-LK：管理 Token 刚变，立即失效缓存，让新值即刻生效、旧值即刻失效（不必重启）
		if req.Key == "admin_token" {
			s.invalidateAdminToken()
			observability.Info(r.Context(), "assist 管理 Token 已更新（配置热生效）")
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 405, map[string]any{"error": "method"})
	}
}

// maskSecret 敏感值掩码：保留前 3 后 2，中间 ***（主站 maskKey 同思路）
func maskSecret(s string) string {
	if len(s) <= 6 {
		return "***"
	}
	return s[:3] + "***" + s[len(s)-2:]
}

// isMaskedSecret 判断是否为掩码形态（含 "***" 且非空）——掩码值不回写库
func isMaskedSecret(s string) bool {
	return s != "" && strings.Contains(s, "***")
}

// handleTable 通用表 CRUD（管理端）
func (s *Server) handleTable(table string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rows, err := s.db.List(table, false)
			if err != nil {
				writeJSON(w, 500, map[string]any{"error": "db"})
				return
			}
			if rows == nil {
				rows = []store.Row{}
			}
			writeJSON(w, 200, map[string]any{"rows": rows})
		case http.MethodPost:
			var data map[string]any
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				writeJSON(w, 400, map[string]any{"error": "bad json"})
				return
			}
			normalizeRow(table, data)
			id, err := s.db.Create(table, data)
			if err != nil {
				observability.Error(r.Context(), "assist.api 记录创建失败", "table", table, "err", err)
				writeJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"id": id})
		case http.MethodPut:
			id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
			if id <= 0 {
				writeJSON(w, 400, map[string]any{"error": "id required"})
				return
			}
			var data map[string]any
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				writeJSON(w, 400, map[string]any{"error": "bad json"})
				return
			}
			delete(data, "id")
			normalizeRow(table, data)
			if err := s.db.Update(table, id, data); err != nil {
				writeJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		case http.MethodDelete:
			id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
			if id <= 0 {
				writeJSON(w, 400, map[string]any{"error": "id required"})
				return
			}
			if err := s.db.Delete(table, id); err != nil {
				writeJSON(w, 500, map[string]any{"error": "db"})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			writeJSON(w, 405, map[string]any{"error": "method"})
		}
	}
}

// normalizeRow 数字/布尔类型归一（SQLite 动态类型）
func normalizeRow(table string, data map[string]any) {
	if v, ok := data["priority"]; ok {
		data["priority"] = toAnyInt(v)
	}
	if v, ok := data["sort"]; ok {
		data["sort"] = toAnyInt(v)
	}
	if v, ok := data["enabled"]; ok {
		data["enabled"] = toAnyInt(v)
	}
	if table == "flows" {
		if v, ok := data["steps_json"].(string); ok && v == "" {
			data["steps_json"] = "[]"
		}
	}
}

// toAnyInt JSON 动态数字转 int（float64/int/数字串均兼容）
func toAnyInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		var x int
		if _, err := fmt.Sscanf(n, "%d", &x); err == nil {
			return x
		}
	}
	return 0
}

// handleSessions GET /api/assist/admin/sessions → 会话列表 + 统计 + 未答问题清单（R0.2）
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.ListSessions(100)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "db"})
		return
	}
	if rows == nil {
		rows = []store.Row{}
	}
	writeJSON(w, 200, map[string]any{
		"sessions":   rows,
		"total":      s.db.SessionCount(),
		"messages":   s.db.MessageCount(),
		"unanswered": s.eng.UnansweredQuestions(), // R0.2 运营补料清单
		"llm_mode":   s.llmMode(r.Context()),      // R0.3 生效状态徽标
	})
}

// llmMode LLM 接入状态（管理台徽标）：env / db / rule
func (s *Server) llmMode(ctx context.Context) string {
	if s.eng.LLMMode(ctx) != "" {
		return s.eng.LLMMode(ctx)
	}
	return "rule"
}

// handleLLMTest POST /api/assist/admin/llm/test → 测试连通（R0.4c）
// 用当前生效配置发一条 1-token 请求，回显模型名/耗时/错误，供管理台「测试连通」按钮。
func (s *Server) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "method"})
		return
	}
	start := time.Now()
	text, model, _, err := s.eng.LLMTest(r.Context())
	dur := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error(), "ms": dur})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "model": model, "ms": dur, "sample": truncate(text, 60)})
}

// truncate 截断字符串（rune 安全）
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// adminPage 管理页（单文件 HTML）
// ★ 改造 1A（2026-09-17）：默认吐内嵌资源（web.AdminHTML，随二进制编译，零外部文件依赖）；
// ASSIST_WEB 显式指向的目录内存在 admin.html 时优先用外置文件（运维临时改页面用）。
func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	// 外置覆盖优先（存在才生效，避免 env 残留指向不存在路径导致 404）
	if v := os.Getenv("ASSIST_WEB"); v != "" {
		if b, err := os.ReadFile(filepath.Join(v, "admin.html")); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
	}
	if len(web.AdminHTML) > 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.AdminHTML)
		return
	}
	w.WriteHeader(404)
	_, _ = w.Write([]byte("admin page missing"))
}

// ============================================================
// 工具
// ============================================================

// writeJSON 统一 JSON 响应出口
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// randHex 随机 hex（LCG 伪随机）已于 2026-09-18 P0-1 整改中删除：
// 它曾被 newSessionID 用于会话 ID，但种子含时间/pid、可被预测，安全场景一律用 crypto/rand。
