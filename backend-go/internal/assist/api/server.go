// Package api HTTP 服务：C 端接待接口 + 超管管理接口 + 管理页静态托管。
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
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
	adm  string // admin token
	cors string
}

// NewServer 构建
func NewServer(db *store.DB, eng *engine.Engine, adminToken, cors string) *Server {
	return &Server{db: db, eng: eng, adm: adminToken, cors: cors}
}

// Handler 汇总路由
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// C 端（挂件调用，免登录，session 自证）
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

// guard 管理端鉴权（X-Assist-Admin 头，常数时间比较）
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Assist-Admin")
		if tok == "" {
			tok = r.URL.Query().Get("admin_token")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.adm)) != 1 {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// ============================================================
// C 端接口
// ============================================================

// newSessionID 生成会话 ID
func newSessionID() string {
	return fmt.Sprintf("s%d%s", time.Now().UnixMilli(), randHex(4))
}

// sessMu session upsert 竞态保护（低并发足够；mutex 护 EnsureSession 读-建窗口）
var sessMu sync.Mutex // session upsert 竞态保护（低并发足够）

// ensureSession 读取或创建会话
func (s *Server) ensureSession(ctx context.Context, id, pageURL string) (store.Row, bool) {
	sessMu.Lock()
	defer sessMu.Unlock()
	sess, _ := s.db.SessionRow(id)
	if sess != nil {
		return sess, false
	}
	if err := s.db.EnsureSession(id, pageURL); err != nil {
		observability.Error(ctx, "assist.api 会话创建失败", "err", err)
	}
	sess, _ = s.db.SessionRow(id)
	return sess, sess != nil
}

// handleGreeting GET /api/assist/greeting?session=&page= → 欢迎词 + 会话 id + 快捷提问 + 功能入口
func (s *Server) handleGreeting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "method"})
		return
	}
	sid := strings.TrimSpace(r.URL.Query().Get("session"))
	page := strings.TrimSpace(r.URL.Query().Get("page"))
	if sid == "" {
		sid = newSessionID()
	}
	s.ensureSession(r.Context(), sid, page)
	text := s.db.GetConfig("welcome", "")
	if text == "" {
		text = s.eng.Greeting()
	}
	_ = s.db.AddMessage(sid, "assistant", text, nil)
	writeJSON(w, 200, map[string]any{
		"session":  sid,
		"greeting": text,
		"chips":    chipsOf(s.db.GetConfig("quick_chips", "")),
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

// handleChat POST /api/assist/chat {session, message, page}
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "method"})
		return
	}
	var req struct {
		Session string `json:"session"`
		Message string `json:"message"`
		Page    string `json:"page"`
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
	if len(req.Message) > 2000 {
		req.Message = req.Message[:2000]
	}
	sid := req.Session
	s.ensureSession(r.Context(), sid, req.Page)

	_ = s.db.AddMessage(sid, "user", req.Message, nil)
	history, _ := s.db.History(sid, 12)

	rep := s.eng.Respond(r.Context(), sid, req.Message, req.Page, history)
	_ = s.db.AddMessage(sid, "assistant", rep.Content, actionMaps(rep.Actions))
	_ = s.db.TouchSession(sid)

	writeJSON(w, 200, map[string]any{
		"reply":   rep.Content,
		"actions": rep.Actions,
		"model":   rep.Model,
		"source":  rep.Source,
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

// handleHistory GET /api/assist/history?session=&limit=20
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimSpace(r.URL.Query().Get("session"))
	if sid == "" {
		writeJSON(w, 400, map[string]any{"error": "session required"})
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
//   - ★ LLM 四项（R0.4）：base_url/api_key/model/model_backup 允许后台在线配置，
//     engine 侧配合惰性重建实现热加载；api_key_backup 复用主 Key 故不单列。
//     env（ASSIST_LLM_*）显式配置优先于 configs 表（见 engine.llmClient）。
var configKeyWhitelist = map[string]bool{
	"welcome": true, "persona": true, "temperature": true, "max_tokens": true, "quick_chips": true,
	"llm_base_url": true, "llm_api_key": true, "llm_model": true, "llm_model_backup": true,
	// R0.1 同义词归一表（逗号分隔：词=同义词1|同义词2，多组换行）
	"synonyms": true,
}

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
		// api_key 掩码回显（防管理台/日志泄露明文），与主站 models 掩码口径一致
		for i := range rows {
			if store.Row(rows[i])["key"] == "llm_api_key" {
				if v, _ := store.Row(rows[i])["value"].(string); v != "" {
					store.Row(rows[i])["value"] = maskSecret(v)
				}
			}
		}
		writeJSON(w, 200, map[string]any{"configs": rows})
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
		// 掩码值回写拦截：管理台保存时若 value 仍是掩码形态，视为未修改，跳过写库
		if req.Key == "llm_api_key" && isMaskedSecret(req.Value) {
			writeJSON(w, 200, map[string]any{"ok": true, "skipped": true})
			return
		}
		if err := s.db.SetConfig(req.Key, req.Value); err != nil {
			observability.Error(r.Context(), "assist.api 配置写入失败", "key", req.Key, "err", err)
			writeJSON(w, 500, map[string]any{"error": "db"})
			return
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

// randHex 随机 hex（无外部依赖，uint64 环形序列避免溢出为负）
func randHex(n int) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, n*2)
	var x uint64 = uint64(time.Now().UnixNano()) ^ uint64(os.Getpid())<<20
	for i := range b {
		x = x*6364136223846793005 + 1442695040888963407
		b[i] = hexd[(x>>33)%16]
	}
	return string(b)
}
