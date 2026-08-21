package api

// ============ 本文件职责中文说明 ============
// 本文件实现 HTTP 服务核心：服务结构体、路由注册、中间件与基础接口。
//   - Server 结构体：聚合配置/引擎/知识库/租户存储/平台存储/计费服务/指标收集器
//   - NewServer：创建服务、初始化计费与指标、注册路由、启动看门狗
//   - routes：注册全部 REST 路由（指标/翻译/租户/认证/工单/KB/模型/计费/开放 API 等）
//   - 中间件：withMetrics（HTTP 指标）、withTenant（租户解析与隔离）、withCORS（跨域）
//   - 租户解析优先级：登录 JWT > API Key 所属租户 > X-Tenant-ID（仅超管）> 默认租户 1
//   - 基础接口：健康检查 / 技能列表 / 语言列表 / KB 统计
// 安全要点：
//   - 普通用户租户取自 JWT，无法越权指定其他租户（防越权）
//   - 未登录请求仅信任 API Key 或默认租户 1，X-Tenant-ID 头一律忽略（防伪造越权）
//   - 超级管理员（tenant_id=0）可通过 X-Tenant-ID 切换后台生效租户

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/kb"
	"translator/internal/store"
	"translator/internal/tenant"
)

// Server HTTP 服务：聚合平台各子系统并对外提供 HTTP 接口。
type Server struct {
	Cfg    *config.Config   // 全局配置（模型/上传目录/策略参数等）
	Engine *engine.Engine   // 翻译引擎（文本/文件处理、LLM 调用与熔断）
	DB     *kb.KBDatabase   // 知识库数据库（-kb 加载，用于匹配与统计）
	Ten    *tenant.Store    // 租户存储（租户增删改查、权限与模型配置）
	Store  *store.Store     // 平台存储（用户/工单/审计/计费/API Key 等）
	Bill   *billing.Service // 计费服务（QPS/并发限流、每日配额、余额扣减）
	Dist   string           // 前端 dist 目录（SPA 静态资源根目录）
	mux    *http.ServeMux   // 路由分发器
	// 系统级指标收集器（Prometheus /metrics）
	metrics *Metrics
	// 登录失败限流器（暴力破解防护）
	loginLimit *loginLimiter
}

// NewServer 创建 HTTP 服务：初始化计费服务、注册路由并启动看门狗。
// 参数 cfg: 全局配置；eng: 翻译引擎（可 nil）；db: 知识库（可 nil）；dist: 前端目录；
// st: 平台存储（可 nil）；ts: 租户存储（可 nil）。
// 返回: 组装完成的 *Server 实例。
func NewServer(cfg *config.Config, eng *engine.Engine, db *kb.KBDatabase, dist string, st *store.Store, ts *tenant.Store) *Server {
	s := &Server{Cfg: cfg, Engine: eng, DB: db, Ten: ts, Store: st, Dist: dist, metrics: newMetrics(), loginLimit: newLoginLimiter()}
	// 平台存储就绪时初始化计费服务（限流/配额/余额）
	if st != nil {
		s.Bill = billing.NewService(st)
	}
	s.mux = http.NewServeMux()
	s.routes()
	// 启动监控看门狗（后台巡检余额/模型健康）
	s.startWatchdog()
	return s
}

// routes 注册全部 HTTP 路由，按业务域分组调用各分组注册函数。
// 无参数无返回；所有 Handler 均挂载到 s.mux。
func (s *Server) routes() {
	// 指标与基础接口
	s.mux.HandleFunc("/metrics", s.handleMetrics)
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/skills", s.handleSkills)
	// 公开商业页面（无需登录）：定价 / 条款 / SLA / 隐私 / 套餐 / 注册行业
	s.mux.HandleFunc("/api/pricing", s.handlePublicPricingAPI)
	s.mux.HandleFunc("/pricing", s.handlePublicPricing)
	s.mux.HandleFunc("/docs/terms", s.handlePublicTerms)
	s.mux.HandleFunc("/docs/sla", s.handlePublicSLA)
	s.mux.HandleFunc("/docs/privacy", s.handlePublicPrivacy)
	s.mux.HandleFunc("/api/plans", s.handlePlans)
	s.mux.HandleFunc("/api/register/industries", s.handleRegisterIndustries)
	// 翻译核心（聊天/文件/下载/语言/KB 统计）
	s.routesTranslate()
	// ★ SaaS 租户管理（管理后台）
	s.routesTenant()
	// ★ 认证与用户
	s.routesAuth()
	// ★ 工单 + 审批
	s.routesTickets()
	// ★ 管理后台（KB/流程/模型/策略/evals/系统健康/计费/API Key/开放 API）
	s.routesAdmin()
	// ★ 组织层级管理（管理结构展示层）
	s.routesOrgs()
	// 兜底路由：前端 SPA 静态资源
	s.mux.HandleFunc("/", s.handleSPA)
}

// routesTranslate 注册翻译核心路由：聊天（文本/SSE）、文件（翻译/SSE/下载）、语言与 KB 统计。
func (s *Server) routesTranslate() {
	s.mux.HandleFunc("/api/chat/stream", s.handleChatStream)
	s.mux.HandleFunc("/api/translate/stream", s.handleTranslateFileStream)
	s.mux.HandleFunc("/api/chat", s.handleChat)
	s.mux.HandleFunc("/api/translate", s.handleTranslateFile)
	s.mux.HandleFunc("/api/download/", s.handleDownload)
	s.mux.HandleFunc("/api/translation/langs", s.handleTranslationLangs)
	s.mux.HandleFunc("/api/translation/recognize-kb", s.handleRecognizeKB)
	s.mux.HandleFunc("/api/translation/import-kb", s.handleImportKB)
	s.mux.HandleFunc("/api/translation/kb-stats", s.handleKBStats)
}

// routesTenant 注册租户管理路由（管理后台）。
func (s *Server) routesTenant() {
	s.mux.HandleFunc("/api/tenant/list", s.handleTenantList)
	s.mux.HandleFunc("/api/tenant/create", s.handleTenantCreate)
	s.mux.HandleFunc("/api/tenant/update", s.handleTenantUpdate)
	s.mux.HandleFunc("/api/tenant/status", s.handleTenantStatus)
	s.mux.HandleFunc("/api/tenant/delete", s.handleTenantDelete)
	s.mux.HandleFunc("/api/tenant/export", s.handleTenantExport)
	s.mux.HandleFunc("/api/tenant/erase", s.handleTenantErase)
}

// routesAuth 注册认证与用户管理路由。
func (s *Server) routesAuth() {
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/register", s.handleRegister)
	s.mux.HandleFunc("/api/auth/me", s.handleMe)
	s.mux.HandleFunc("/api/auth/change-password", s.handleChangePassword)
	s.mux.HandleFunc("/api/auth/forgot-password", s.handleForgotPassword)
	s.mux.HandleFunc("/api/auth/reset-password", s.handleResetPassword)
	s.mux.HandleFunc("/api/admin/users", s.handleAdminUsers)
	s.mux.HandleFunc("/api/admin/users/create", s.handleAdminUserCreate)
	s.mux.HandleFunc("/api/admin/users/update", s.handleAdminUserUpdate)
	s.mux.HandleFunc("/api/admin/users/reset-password", s.handleAdminUserResetPassword)
	s.mux.HandleFunc("/api/admin/invite-codes", s.handleInviteCodes)
	s.mux.HandleFunc("/api/admin/invite-codes/create", s.handleInviteCodeCreate)
}

// routesTickets 注册工单与审批路由。
func (s *Server) routesTickets() {
	s.mux.HandleFunc("/api/tickets", s.handleTickets)
	s.mux.HandleFunc("/api/tickets/create", s.handleTicketCreate)
	s.mux.HandleFunc("/api/tickets/run", s.handleTicketRun)
	s.mux.HandleFunc("/api/tickets/detail", s.handleTicketDetail)
	s.mux.HandleFunc("/api/approve/list", s.handleApproveList)
	s.mux.HandleFunc("/api/approve/action", s.handleApproveAction)
}

// routesAdmin 注册管理后台全部路由：KB 包/条目/安全句、流程引擎、模型/策略、
// evals 看板、系统健康/审计/告警、计费/充值/用量/发票、开放 API Key、开放 API 与文档。
func (s *Server) routesAdmin() {
	s.routesAdminKB()
	s.routesAdminFlow()
	s.routesAdminModels()
	s.routesAdminSystem()
	s.routesBilling()
	s.routesAPIKeys()
	s.routesOpenAPI()
	s.routesWebhooks()
}

// routesAdminKB 注册知识库包/条目/安全句管理路由。
func (s *Server) routesAdminKB() {
	s.mux.HandleFunc("/api/admin/kb-packages", s.handleKBPackages)
	s.mux.HandleFunc("/api/admin/kb-packages/create", s.handleKBPackageCreate)
	s.mux.HandleFunc("/api/admin/kb-packages/update", s.handleKBPackageUpdate)
	s.mux.HandleFunc("/api/admin/kb-packages/delete", s.handleKBPackageDelete)
	s.mux.HandleFunc("/api/admin/kb-entries", s.handleKBEntries)
	s.mux.HandleFunc("/api/admin/kb-entries/add", s.handleKBEntryAdd)
	s.mux.HandleFunc("/api/admin/kb-entries/import", s.handleKBEntriesImport)
	s.mux.HandleFunc("/api/admin/kb-entries/delete", s.handleKBEntryDelete)
	s.mux.HandleFunc("/api/admin/safety-phrases", s.handleSafetyPhrases)
	s.mux.HandleFunc("/api/admin/safety-phrases/add", s.handleSafetyPhraseAdd)
	s.mux.HandleFunc("/api/admin/safety-phrases/delete", s.handleSafetyPhraseDelete)
}

// routesAdminFlow 注册流程引擎设置路由。
func (s *Server) routesAdminFlow() {
	s.mux.HandleFunc("/api/admin/flow", s.handleFlowConfig)
	s.mux.HandleFunc("/api/admin/flow/save", s.handleFlowSave)
	s.mux.HandleFunc("/api/admin/flow/run", s.handleFlowRunTicket)
}

// routesAdminModels 注册模型/模型路由/策略参数配置路由。
func (s *Server) routesAdminModels() {
	s.mux.HandleFunc("/api/admin/models", s.handleModels)
	s.mux.HandleFunc("/api/admin/models/save", s.handleModelsSave)
	s.mux.HandleFunc("/api/admin/models/routes", s.handleModelRoutes)
	s.mux.HandleFunc("/api/admin/models/routes/save", s.handleModelRoutesSave)
	s.mux.HandleFunc("/api/admin/models/stage", s.handleStageModels)
	s.mux.HandleFunc("/api/admin/models/stage/save", s.handleStageModelsSave)
	s.mux.HandleFunc("/api/admin/policy", s.handlePolicy)
	s.mux.HandleFunc("/api/admin/policy/save", s.handlePolicySave)
}

// routesAdminSystem 注册 evals 看板与系统健康/审计/告警路由。
func (s *Server) routesAdminSystem() {
	s.mux.HandleFunc("/api/evals/list", s.handleEvalsList)
	s.mux.HandleFunc("/api/system/health", s.handleSystemHealth)
	s.mux.HandleFunc("/api/system/audit", s.handleSystemAudit)
	s.mux.HandleFunc("/api/system/alerts", s.handleAlerts)
	s.mux.HandleFunc("/api/system/alerts/resolve", s.handleAlertResolve)
}

// routesBilling 注册计费/充值/用量/配额/发票/商业包路由。
func (s *Server) routesBilling() {
	s.mux.HandleFunc("/api/billing/balance", s.handleBalance)
	s.mux.HandleFunc("/api/billing/usage", s.handleUsage)
	s.mux.HandleFunc("/api/billing/usage/me", s.handleUsageMe)
	s.mux.HandleFunc("/api/billing/usage/org", s.handleUsageOrg)
	s.mux.HandleFunc("/api/billing/usage/cost", s.handleUsageCost)
	s.mux.HandleFunc("/api/billing/orders", s.handleOrders)
	s.mux.HandleFunc("/api/billing/config", s.handleBillingConfig)
	s.mux.HandleFunc("/api/billing/config/save", s.handleBillingConfigSave)
	s.mux.HandleFunc("/api/billing/quota", s.handleTenantQuota)
	s.mux.HandleFunc("/api/billing/quota/save", s.handleTenantQuotaSave)
	s.mux.HandleFunc("/api/admin/orders/create", s.handleOrderCreate)
	s.mux.HandleFunc("/api/admin/orders/pay", s.handleOrderPay)
	s.mux.HandleFunc("/api/admin/orders/refund", s.handleOrderRefund)
	s.mux.HandleFunc("/api/billing/invoices", s.handleInvoices)
	s.mux.HandleFunc("/api/billing/invoices/create", s.handleInvoiceCreate)
	// 商业包：我的包 / 订阅 / 超管管理
	s.mux.HandleFunc("/api/me/package", s.handleMyPackage)
	s.mux.HandleFunc("/api/package/subscribe", s.handlePackageSubscribe)
	s.mux.HandleFunc("/api/admin/packages", s.handleAdminPackages)
	s.mux.HandleFunc("/api/admin/packages/create", s.handleAdminPackageCreate)
	s.mux.HandleFunc("/api/admin/packages/update", s.handleAdminPackageUpdate)
	s.mux.HandleFunc("/api/admin/packages/delete", s.handleAdminPackageDelete)
	s.mux.HandleFunc("/api/admin/packages/settings", s.handleAdminPackageSettings)
	s.mux.HandleFunc("/api/admin/packages/settings/save", s.handleAdminPackageSettingsSave)
	// 在线支付：下单 / 状态轮询 / 模拟支付 / 我已付费（静态码人工确认）/ 渠道回调
	s.mux.HandleFunc("/api/pay/create", s.handlePayCreate)
	s.mux.HandleFunc("/api/pay/status", s.handlePayStatus)
	s.mux.HandleFunc("/api/pay/simulate", s.handlePaySimulate)
	s.mux.HandleFunc("/api/pay/manual-confirm", s.handlePayManualConfirm)
	s.mux.HandleFunc("/api/pay/notify/", s.handlePayNotify)
	// 待人工确认订单（超管审核开通）
	s.mux.HandleFunc("/api/admin/orders/manual", s.handleManualConfirmOrders)
}

// routesAPIKeys 注册租户开放 API Key 管理路由。
func (s *Server) routesAPIKeys() {
	s.mux.HandleFunc("/api/apikeys", s.handleAPIKeys)
	s.mux.HandleFunc("/api/apikeys/create", s.handleAPIKeyCreate)
	s.mux.HandleFunc("/api/apikeys/status", s.handleAPIKeyStatus)
	s.mux.HandleFunc("/api/apikeys/rotate", s.handleAPIKeyRotate)
	s.mux.HandleFunc("/api/apikeys/delete", s.handleAPIKeyDelete)
}

// routesWebhooks 注册租户 webhook 回调配置管理路由。
func (s *Server) routesWebhooks() {
	s.mux.HandleFunc("/api/webhooks", s.handleWebhooks)
	s.mux.HandleFunc("/api/webhooks/save", s.handleWebhookSave)
	s.mux.HandleFunc("/api/webhooks/delete", s.handleWebhookDelete)
	s.mux.HandleFunc("/api/webhooks/test", s.handleWebhookTest)
}

// routesOpenAPI 注册开放 API（API Key 鉴权）与文档路由。
func (s *Server) routesOpenAPI() {
	s.mux.HandleFunc("/openapi/v1/translate", s.handleOpenAPITranslate)
	s.mux.HandleFunc("/openapi/v1/kb/stats", s.handleOpenAPIKBStats)
	s.mux.HandleFunc("/openapi/v1/billing/usage", s.handleOpenAPIUsage)
	s.mux.HandleFunc("/openapi/v1/apikey/rotate", s.handleOpenAPIKeyRotate)
	s.mux.HandleFunc("/openapi/docs", s.handleOpenAPIDocs)
}

// Handler 返回完整的 http.Handler（依次包裹指标/租户/CORS 中间件）。
// 返回: 可交给 http.ListenAndServe 使用的 http.Handler。
func (s *Server) Handler() http.Handler {
	return s.withMetrics(s.withTenant(s.withCORS(s.withBodyLimit(s.withAccessLog(s.mux)))))
}

// maxJSONBody 非 multipart 请求体上限（JSON 接口防超大请求；文件上传走 multipart 不受限）
const maxJSONBody = 16 << 20 // 16MB

// withBodyLimit 限制非 multipart 请求体大小（防滥用超大 JSON）。
// 参数 next: 下一层 Handler。返回: 包装后的 Handler（超出上限时解码方返回错误）。
func (s *Server) withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// multipart 文件上传由各 handler 自行控制大小，此处跳过
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/") {
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
		}
		next.ServeHTTP(w, r)
	})
}

// withMetrics 记录 HTTP 请求指标中间件（按路径标签计数）。
// 参数 next: 下一层 Handler。返回: 包装后的 Handler。
func (s *Server) withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 每个请求按路径计数（供 /metrics 输出）
		s.metrics.countHTTP(r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

// withTenant 解析请求租户并注入 context 的中间件。
// 优先级：登录态（Authorization Bearer JWT 中的 tenant_id）> API Key 所属租户 > X-Tenant-ID 头 > 默认租户 1（rox）。
// 安全约束：
//   - 已登录普通用户：租户取自 JWT，无法越权指定其他租户。
//   - 未登录请求：仅信任 API Key（开放 API）或默认租户 1；X-Tenant-ID 头一律忽略（防伪造越权）。
//   - 超级管理员（平台级 tenant_id=0）：生效租户由 X-Tenant-ID 指定（后台租户切换器），默认 rox。
//
// 参数 next: 下一层 Handler。返回: 包装后的 Handler（已把租户写入 request context）。
func (s *Server) withTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// 1. 登录用户：租户取自 JWT，普通用户无法越权指定其他租户
		if u := s.authUser(r); u != nil {
			if u.TenantID > 0 {
				// 普通登录用户：强制使用 JWT 中的租户（防越权）
				ctx = tenant.WithTenant(ctx, u.TenantID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// 2. 超级管理员（平台级 tenant_id=0）：X-Tenant-ID 切换生效租户（后台租户切换器）
			if v := r.Header.Get("X-Tenant-ID"); v != "" {
				if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
					ctx = tenant.WithTenant(ctx, id)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// 3. 未登录：仅信任 API Key 所属租户（开放 API 路径）
		if ak, ok := s.authenticateAPIKey(r); ok && ak.TenantID > 0 {
			ctx = tenant.WithTenant(ctx, ak.TenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// 4. 其余未登录请求：强制默认租户 1（rox），忽略 X-Tenant-ID 防伪造
		ctx = tenant.WithTenant(ctx, 1)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// effTenant 计算当前请求生效租户。
// 超级管理员（平台级，tenant_id=0）使用请求上下文所选租户（X-Tenant-ID 切换）；其余用户固定自身租户。
// 参数 r: HTTP 请求；u: 当前用户。返回: 生效租户 ID。
func (s *Server) effTenant(r *http.Request, u *store.User) int64 {
	if auth.IsSuperAdmin(u) {
		return s.currentTenant(r)
	}
	if u != nil && u.TenantID > 0 {
		return u.TenantID
	}
	return 1
}

// withCORS 跨域中间件：按白名单校验来源（同源部署天然放行；跨域仅允许配置的来源）。
// 参数 next: 下一层 Handler。返回: 包装后的 Handler（OPTIONS 预检直接返回 200）。
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 请求方法/头（含租户切换与后台 Token 头）
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Tenant-ID, X-Admin-Token")
		// 来源校验：无 Origin 头（同源导航/非浏览器）直接放行
		origin := r.Header.Get("Origin")
		if origin == "" || s.corsAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		// 预检请求直接返回，不进入业务 Handler
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// corsAllowed 判断请求来源是否在 CORS 白名单内。
// 参数 origin: 请求 Origin 头值；返回 true 表示允许该跨域来源。
func (s *Server) corsAllowed(origin string) bool {
	// 无白名单配置时默认放行（保持向后兼容）
	if s.Cfg == nil || len(s.Cfg.CORSOrigins) == 0 {
		return true
	}
	for _, o := range s.Cfg.CORSOrigins {
		if o == origin {
			return true
		}
	}
	return false
}

// authUser 从请求提取当前用户（JWT 校验）。未登录或 Token 无效返回 nil。
// 参数 r: HTTP 请求。返回: 当前登录用户对象（*store.User）或 nil。
func (s *Server) authUser(r *http.Request) *store.User {
	// 平台存储未初始化无法查用户
	if s.Store == nil {
		return nil
	}
	// 提取 Bearer Token
	tok := auth.BearerToken(r)
	if tok == "" {
		return nil
	}
	// JWT 验签与解析
	claims, err := auth.Verify(tok)
	if err != nil {
		return nil
	}
	// 校验用户存在且处于激活状态（按 JWT 中的 user_id + tenant_id）
	u, err := s.Store.GetUser(claims.UserID, claims.TenantID)
	if err != nil || u.Status != store.UserActive {
		return nil
	}
	return u
}

// writeJSON 写入 JSON 响应（统一设置 Content-Type）。
// 参数 w: HTTP 响应写入器；status: HTTP 状态码；v: 待序列化的响应对象。
// 无返回。
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ChatRequest 聊天请求。
type ChatRequest struct {
	Message string                 `json:"message"` // 用户输入消息（待翻译文本）
	Skill   string                 `json:"skill"`   // 技能标识（当前固定 translation）
	Options map[string]interface{} `json:"options"` // 附加选项（如 target_langs 等）
}

// ============ 基础接口 ============

// handleHealth 健康检查接口（/api/health）：返回服务状态与版本。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回 status/version/skills。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"status":  "ok",
		"version": "2.0.0-go",
		"skills":  []string{"translation"},
	})
}

// handleSkills 技能列表接口（/api/skills）：返回平台可用技能。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回 translation 技能的描述与关键词。
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

// handleTranslationLangs 语言列表接口（/api/translation/langs）：返回知识库支持的语言代码/名称/旗帜。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回 kb_langs 数组。
func (s *Server) handleTranslationLangs(w http.ResponseWriter, r *http.Request) {
	langs := make([]map[string]string, 0, len(config.TranslateLangs))
	// 遍历全局语言配置组装语言元信息
	for _, code := range config.TranslateLangs {
		langs = append(langs, map[string]string{
			"code": code,
			"name": config.LangNames[code],
			"flag": config.Flags[code],
		})
	}
	writeJSON(w, 200, map[string]interface{}{"kb_langs": langs})
}

// handleKBStats 知识库统计接口（/api/translation/kb-stats）：返回当前租户的 KB 条目统计。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回 total_tm_entries/lang_stats/total_segments/tenant_id。
func (s *Server) handleKBStats(w http.ResponseWriter, r *http.Request) {
	// 知识库未加载时返回提示
	if s.DB == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "翻译技能未加载"})
		return
	}
	// 按当前租户维度统计（租户隔离：只统计本租户 KB 数据）
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

// currentTenant 从请求 context 取租户；无则默认 1。
// 参数 r: HTTP 请求。返回: 当前请求租户 ID（默认 1 = rox）。
func (s *Server) currentTenant(r *http.Request) int64 {
	if id := tenant.FromContext(r.Context()); id > 0 {
		return id
	}
	return 1
}
