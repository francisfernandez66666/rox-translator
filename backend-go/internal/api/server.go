package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/kb"
	"translator/internal/store"
	"translator/internal/tenant"
)

// Server HTTP 服务
type Server struct {
	Cfg    *config.Config
	Engine *engine.Engine
	DB     *kb.KBDatabase
	Ten    *tenant.Store
	Store  *store.Store
	Dist   string
	mux    *http.ServeMux
}

// NewServer 创建服务
func NewServer(cfg *config.Config, eng *engine.Engine, db *kb.KBDatabase, dist string, st *store.Store, ts *tenant.Store) *Server {
	s := &Server{Cfg: cfg, Engine: eng, DB: db, Ten: ts, Store: st, Dist: dist}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/skills", s.handleSkills)
	s.mux.HandleFunc("/api/chat/stream", s.handleChatStream)
	s.mux.HandleFunc("/api/translate/stream", s.handleTranslateFileStream)
	s.mux.HandleFunc("/api/chat", s.handleChat)
	s.mux.HandleFunc("/api/translate", s.handleTranslateFile)
	s.mux.HandleFunc("/api/download/", s.handleDownload)
	s.mux.HandleFunc("/api/translation/langs", s.handleTranslationLangs)
	s.mux.HandleFunc("/api/translation/recognize-kb", s.handleRecognizeKB)
	s.mux.HandleFunc("/api/translation/import-kb", s.handleImportKB)
	s.mux.HandleFunc("/api/translation/kb-stats", s.handleKBStats)
	// ★ SaaS 租户管理（管理后台）
	s.mux.HandleFunc("/api/tenant/list", s.handleTenantList)
	s.mux.HandleFunc("/api/tenant/create", s.handleTenantCreate)
	s.mux.HandleFunc("/api/tenant/update", s.handleTenantUpdate)
	s.mux.HandleFunc("/api/tenant/status", s.handleTenantStatus)
	s.mux.HandleFunc("/api/tenant/delete", s.handleTenantDelete)

	// ★ 认证与用户
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/me", s.handleMe)
	s.mux.HandleFunc("/api/auth/change-password", s.handleChangePassword)
	s.mux.HandleFunc("/api/admin/users", s.handleAdminUsers)
	s.mux.HandleFunc("/api/admin/users/create", s.handleAdminUserCreate)
	s.mux.HandleFunc("/api/admin/users/update", s.handleAdminUserUpdate)
	s.mux.HandleFunc("/api/admin/users/reset-password", s.handleAdminUserResetPassword)

	// ★ 工单 + 审批
	s.mux.HandleFunc("/api/tickets", s.handleTickets)
	s.mux.HandleFunc("/api/tickets/create", s.handleTicketCreate)
	s.mux.HandleFunc("/api/tickets/run", s.handleTicketRun)
	s.mux.HandleFunc("/api/tickets/detail", s.handleTicketDetail)
	s.mux.HandleFunc("/api/approve/list", s.handleApproveList)
	s.mux.HandleFunc("/api/approve/action", s.handleApproveAction)

	// ★ KB 包管理（行业包）
	s.mux.HandleFunc("/api/admin/kb-packages", s.handleKBPackages)
	s.mux.HandleFunc("/api/admin/kb-packages/create", s.handleKBPackageCreate)
	s.mux.HandleFunc("/api/admin/kb-packages/update", s.handleKBPackageUpdate)
	s.mux.HandleFunc("/api/admin/kb-packages/delete", s.handleKBPackageDelete)
	s.mux.HandleFunc("/api/admin/kb-entries", s.handleKBEntries)
	s.mux.HandleFunc("/api/admin/kb-entries/add", s.handleKBEntryAdd)
	s.mux.HandleFunc("/api/admin/kb-entries/delete", s.handleKBEntryDelete)
	s.mux.HandleFunc("/api/admin/safety-phrases", s.handleSafetyPhrases)
	s.mux.HandleFunc("/api/admin/safety-phrases/add", s.handleSafetyPhraseAdd)
	s.mux.HandleFunc("/api/admin/safety-phrases/delete", s.handleSafetyPhraseDelete)

	// ★ 流程引擎设置
	s.mux.HandleFunc("/api/admin/flow", s.handleFlowConfig)
	s.mux.HandleFunc("/api/admin/flow/save", s.handleFlowSave)
	s.mux.HandleFunc("/api/admin/flow/run", s.handleFlowRunTicket)

	// ★ 模型/策略配置
	s.mux.HandleFunc("/api/admin/models", s.handleModels)
	s.mux.HandleFunc("/api/admin/models/save", s.handleModelsSave)
	s.mux.HandleFunc("/api/admin/policy", s.handlePolicy)
	s.mux.HandleFunc("/api/admin/policy/save", s.handlePolicySave)

	// ★ evals 看板
	s.mux.HandleFunc("/api/evals/list", s.handleEvalsList)

	// ★ 系统健康
	s.mux.HandleFunc("/api/system/health", s.handleSystemHealth)
	s.mux.HandleFunc("/api/system/audit", s.handleSystemAudit)

	// ★ 计费/充值/用量
	s.mux.HandleFunc("/api/billing/balance", s.handleBalance)
	s.mux.HandleFunc("/api/billing/usage", s.handleUsage)
	s.mux.HandleFunc("/api/billing/orders", s.handleOrders)
	s.mux.HandleFunc("/api/admin/orders/create", s.handleOrderCreate)
	s.mux.HandleFunc("/api/admin/orders/pay", s.handleOrderPay)
	s.mux.HandleFunc("/api/admin/orders/refund", s.handleOrderRefund)

	// ★ 租户开放 API Key
	s.mux.HandleFunc("/api/apikeys", s.handleAPIKeys)
	s.mux.HandleFunc("/api/apikeys/create", s.handleAPIKeyCreate)
	s.mux.HandleFunc("/api/apikeys/status", s.handleAPIKeyStatus)
	s.mux.HandleFunc("/api/apikeys/delete", s.handleAPIKeyDelete)

	// ★ 开放 API（API Key 鉴权）
	s.mux.HandleFunc("/openapi/v1/translate", s.handleOpenAPITranslate)

	s.mux.HandleFunc("/", s.handleSPA)
}

// Handler 返回 http.Handler
func (s *Server) Handler() http.Handler {
	return s.withTenant(withCORS(s.mux))
}

// withTenant 解析请求租户并注入 context。
// 优先级：登录态（Authorization Bearer JWT 中的 tenant_id）> X-Tenant-ID 头 > 默认租户 1（rox）。
// 超级管理员为平台级（tenant_id=0），其生效租户由 X-Tenant-ID 指定（后台租户切换器），默认 rox。
func (s *Server) withTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// 1. 登录用户：租户取自 JWT，普通用户无法越权指定其他租户
		if u := s.authUser(r); u != nil && u.TenantID > 0 {
			ctx = tenant.WithTenant(ctx, u.TenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// 2. 未登录或超管：兼容 X-Tenant-ID 头（开放 API / 超管后台切换租户）
		if v := r.Header.Get("X-Tenant-ID"); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
				ctx = tenant.WithTenant(ctx, id)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// effTenant 当前请求生效租户。
// 超级管理员（平台级，tenant_id=0）使用请求上下文所选租户；其余用户固定自身租户。
func (s *Server) effTenant(r *http.Request, u *store.User) int64 {
	if auth.IsSuperAdmin(u) {
		return s.currentTenant(r)
	}
	if u != nil && u.TenantID > 0 {
		return u.TenantID
	}
	return 1
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Tenant-ID, X-Admin-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authUser 从请求提取当前用户（JWT）。未登录返回 nil。
func (s *Server) authUser(r *http.Request) *store.User {
	if s.Store == nil {
		return nil
	}
	tok := auth.BearerToken(r)
	if tok == "" {
		return nil
	}
	claims, err := auth.Verify(tok)
	if err != nil {
		return nil
	}
	u, err := s.Store.GetUser(claims.UserID, claims.TenantID)
	if err != nil || u.Status != store.UserActive {
		return nil
	}
	return u
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ChatRequest 聊天请求
type ChatRequest struct {
	Message string                 `json:"message"`
	Skill   string                 `json:"skill"`
	Options map[string]interface{} `json:"options"`
}

// ============ 基础接口 ============

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"status":  "ok",
		"version": "2.0.0-go",
		"skills":  []string{"translation"},
	})
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"skills": []map[string]interface{}{
			{
				"name":        "translation",
				"description": "多语言翻译：支持9种知识库语言+任意其他语言AI翻译，支持文本和文件翻译",
				"keywords":    []string{"翻译", "译成", "翻成", "translate", "多语言", "九语", "9语", "本地化"},
			},
		},
	})
}

func (s *Server) handleTranslationLangs(w http.ResponseWriter, r *http.Request) {
	langs := make([]map[string]string, 0, len(config.TranslateLangs))
	for _, code := range config.TranslateLangs {
		langs = append(langs, map[string]string{
			"code": code,
			"name": config.LangNames[code],
			"flag": config.Flags[code],
		})
	}
	writeJSON(w, 200, map[string]interface{}{"kb_langs": langs})
}

func (s *Server) handleKBStats(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "翻译技能未加载"})
		return
	}
	tid := s.currentTenant(r)
	total, perLang, seg, err := s.DB.Stats(tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "total_tm_entries": total, "lang_stats": perLang, "total_segments": seg,
		"tenant_id": tid,
	})
}

// currentTenant 从请求 context 取租户；无则默认 1
func (s *Server) currentTenant(r *http.Request) int64 {
	if id := tenant.FromContext(r.Context()); id > 0 {
		return id
	}
	return 1
}