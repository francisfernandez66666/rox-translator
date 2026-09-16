// Package api HTTP 服务：C 端接待接口 + 超管管理接口 + 管理页静态托管。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-assist/internal/engine"
	"ai-assist/internal/store"
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

var sessMu sync.Mutex // session upsert 竞态保护（低并发足够）

// ensureSession 读取或创建会话
func (s *Server) ensureSession(id, pageURL string) (store.Row, bool) {
	sessMu.Lock()
	defer sessMu.Unlock()
	sess, _ := s.db.SessionRow(id)
	if sess != nil {
		return sess, false
	}
	if err := s.db.EnsureSession(id, pageURL); err != nil {
		log.Printf("[api] session create err: %v", err)
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
	s.ensureSession(sid, page)
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
	s.ensureSession(sid, req.Page)

	_ = s.db.AddMessage(sid, "user", req.Message, nil)
	history, _ := s.db.History(sid, 12)

	rep := s.eng.Respond(sid, req.Message, req.Page, history)
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
		if err := s.db.SetConfig(req.Key, req.Value); err != nil {
			log.Printf("[api] config set %s err: %v", req.Key, err)
			writeJSON(w, 500, map[string]any{"error": "db"})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 405, map[string]any{"error": "method"})
	}
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
				log.Printf("[api] create %s err: %v", table, err)
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

// handleSessions GET /api/assist/admin/sessions → 会话列表 + 统计
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
		"sessions": rows,
		"total":    s.db.SessionCount(),
		"messages": s.db.MessageCount(),
	})
}

// adminPage 管理页（单文件 HTML）
func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	p := filepath.Join(s.webDir(), "admin.html")
	b, err := os.ReadFile(p)
	if err != nil {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("admin page missing: " + p))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// webDir 管理页静态目录（env 覆盖，默认 web）
func (s *Server) webDir() string {
	if v := os.Getenv("ASSIST_WEB"); v != "" {
		return v
	}
	return "web"
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
