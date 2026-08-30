// ============ server.go · 职责说明 ============
// Package api 提供翻译 SaaS 平台的所有 HTTP 接口层：
// 负责认证与用户管理、翻译任务与流式输出、知识库导入与检索、租户与组织隔离、
// 计费与订阅、工单与反馈、管理后台以及开放 API 等。本包聚合 store/engine/kb/tenant
// 等内部模块，向上为前端与第三方调用方提供 REST 接口，并承担鉴权、限流、审计、
// 指标收集、租户解析与静态资源托管等横切职责。
// =============================================
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
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	"translator/internal/auth/sso"
	"translator/internal/billing"
	"translator/internal/infra/redis"
	"translator/internal/config"
	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/kb"
	"translator/internal/queue"
	"translator/internal/service"
	"translator/internal/store"
	"translator/internal/tenant"
)

// Server HTTP 服务：聚合平台各子系统并对外提供 HTTP 接口。
type Server struct {
	Cfg       *config.Config         // 全局配置（模型/上传目录/策略参数等）
	Engine    *engine.Engine         // 翻译引擎（文本/文件处理、LLM 调用与熔断）
	DB        *kb.KBDatabase         // 知识库数据库（-kb 加载，用于匹配与统计）
	Ten       *tenant.Store          // 租户存储（租户增删改查、权限与模型配置）
	Store     *store.Store           // 平台存储（用户/工单/审计/计费/API Key 等）
	Bill      *billing.Service       // 计费服务（QPS/并发限流、每日配额、余额扣减）
	TicketSvc *service.TicketService // 工单服务（入队/worker/通知）
	Dist      string                 // 前端 dist 目录（SPA 静态资源根目录）
	mux       *http.ServeMux         // 路由分发器
	// 系统级指标收集器（Prometheus /metrics）
	metrics *Metrics
	// 登录失败限流器（暴力破解防护）
	loginLimit *loginLimiter
	// 注册频率护栏（防脚本批量薅试用额度）
	regGuard *registerGuard
	// 服务启动时间（/status 与 uptime 展示）
	startedAt time.Time
	// SSO / OIDC 适配层（阶段六；nil=未启用）
	SSO *sso.Manager
}

// NewServer 创建 HTTP 服务：初始化计费服务、注册路由并启动看门狗。
// 参数 cfg: 全局配置；eng: 翻译引擎（可 nil）；db: 知识库（可 nil）；dist: 前端目录；
// st: 平台存储（可 nil）；ts: 租户存储（可 nil）。
// 返回: 组装完成的 *Server 实例。
func NewServer(cfg *config.Config, eng *engine.Engine, db *kb.KBDatabase, dist string, st *store.Store, ts *tenant.Store) *Server {
	s := &Server{Cfg: cfg, Engine: eng, DB: db, Ten: ts, Store: st, Dist: dist, metrics: newMetrics(), loginLimit: newLoginLimiter(st), regGuard: newRegisterGuard(st), startedAt: time.Now(), SSO: sso.NewManager(cfg.SSOProviders)}
	// 平台存储就绪时初始化计费服务（限流/配额/余额）
	if st != nil {
		s.Bill = billing.NewService(st)
		// ★ 租户限流配额回放（2026-08-26 全仓评审 C2）：QPS/并发此前仅存进程内存，
		//   重启即回默认 10/3——启动时从 system_config(tenant_quota_<tid>) 恢复
		for tid, qc := range st.LoadTenantQuotaConfigs() {
			billing.SetQPS(tid, qc.QPS)
			billing.SetConcurrent(tid, qc.Concurrent)
		}
		// ★ Token 计费一次性迁移（幂等）：存量句数余额按换算率折算充入 token 账户，
		// 完成后置 billing_token_migrated=1，系统自动进入实费计费模式
		billing.RunTokenMigration(st)
	}
	// 工单服务装配：direct 队列（jobs 表）+ worker 池；启动时回收中断任务
	if st != nil && eng != nil {
		q := queue.NewDirect(st.DB())
		if n, err := q.RecoverStale(context.Background()); err == nil && n > 0 {
			log.Printf("[worker] 启动回收中断任务: %d 个已重新入队", n)
		}
		s.TicketSvc = service.NewTicketService(st, eng, ts, db, q, s.Bill)
		// ★ 多 AZ 唤醒（阶段七）：Redis 启用时以 Redis 信号列表唤醒跨实例 worker，降低分发延迟
		if r := redis.Get(); r != nil {
			s.TicketSvc.Notifier = redis.NewQueueNotifier(r)
		}
		s.TicketSvc.Mailer = s.mailer()         // 邮件异步：注入默认发送器
		s.TicketSvc.InfoMailer = s.infoMailer() // 邮件异步：注入专用发送器
		s.TicketSvc.BootResume()                // ★ 断点续传：启动即接管上次中断的 in_progress 工单
		s.TicketSvc.StartStallSweep()           // ★ 卡死工单巡检：>20min 无进展自动重排续跑
		s.startAuditRetention()                 // ★ P2 审计留存：按保留天数定期清理 audit_logs
		// ★ 并发增强：worker 数量从环境变量 WORKER_CONCURRENCY 读取（默认 4）
		workerCount := 4
		if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				workerCount = n
			}
		}
		s.TicketSvc.StartWorkers(workerCount)
	}
	s.mux = http.NewServeMux()
	s.routes()
	s.routesSSO()
	s.routesOpenAPISpec()
	// 启动监控看门狗（后台巡检余额/模型健康）
	s.startWatchdog()
	return s
}

// startAuditRetention 按保留天数（默认 365，system_config.audit_retention_days 可覆盖）
// 每 6 小时清理一次早于截止日的审计日志，避免长期运行表膨胀。
func (s *Server) startAuditRetention() {
	if s.Store == nil {
		return
	}
	// 启动后台协程：每 6 小时触发一次审计日志清理（先立即执行一次）
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		s.pruneAuditOnce()
		for range t.C {
			s.pruneAuditOnce()
		}
	}()
}

// pruneAuditOnce 执行一次审计日志清理：按保留天数计算截止时间，删除早于该时间的记录。
// 无参数无返回；清理条数大于 0 时记日志。失败仅静默返回，不阻断主流程。
func (s *Server) pruneAuditOnce() {
	days := s.Store.AuditRetentionDays()
	cutoff := time.Now().AddDate(0, 0, -days)
	n, err := s.Store.PruneAuditLogs(cutoff)
	if err != nil {
		return
	}
	if n > 0 {
		log.Printf("[audit] 已清理 %d 条超过 %d 天的审计日志", n, days)
	}
}

// routes 注册全部 HTTP 路由，按业务域分组调用各分组注册函数。
// 无参数无返回；所有 Handler 均挂载到 s.mux。
func (s *Server) routes() {
	// 指标与基础接口
	s.mux.HandleFunc("/metrics", s.handleMetrics)
	s.mux.HandleFunc("/status", s.handlePublicStatus)
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/skills", s.handleSkills)
	// 公开商业页面（无需登录）：定价 / 条款 / SLA / 隐私 / 套餐 / 注册行业
	s.mux.HandleFunc("/pricing", s.handlePublicPricing)
	s.mux.HandleFunc("/docs/terms", s.handlePublicTerms)
	s.mux.HandleFunc("/docs/sla", s.handlePublicSLA)
	s.mux.HandleFunc("/docs/privacy", s.handlePublicPrivacy)
	s.mux.HandleFunc("/api/plans", s.handlePlans)
	s.mux.HandleFunc("/api/register/industries", s.handleRegisterIndustries)
	s.mux.HandleFunc("/office/manifest.xml", s.handleOfficeManifest)
	s.mux.HandleFunc("/office/taskpane.html", s.handleOfficeTaskPane)
	s.mux.HandleFunc("/api/admin/memleak/capture", s.handleMemLeakCapture)
	s.mux.HandleFunc("/api/admin/memleak/log", s.handleMemLeakLog)
	// ★ 用户反馈：前台提交 + 超管管理
	s.mux.HandleFunc("/api/feedback", s.handleFeedbackCreate)
	s.mux.HandleFunc("/api/feedback/list", s.handleFeedbackList)
	s.mux.HandleFunc("/api/feedback/reply", s.handleFeedbackReply)
	s.mux.HandleFunc("/api/feedback/get", s.handleFeedbackGet)
	s.mux.HandleFunc("/api/admin/feedbacks/resolve", s.handleAdminFeedbackResolve)
	// ★ 开放 API 文档在线维护（仅超管）
	s.mux.HandleFunc("/api/admin/openapi-docs", s.handleAdminOpenAPIDocsGet)
	s.mux.HandleFunc("/api/admin/openapi-docs/save", s.handleAdminOpenAPIDocsSave)
	s.mux.HandleFunc("/api/admin/openapi-docs/preview", s.handleAdminOpenAPIDocsPreview)
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
	s.mux.HandleFunc("/api/translation/estimate", s.handleTranslationEstimate)
	s.mux.HandleFunc("/api/translation/recognize-kb", s.handleRecognizeKB)
	s.mux.HandleFunc("/api/translation/import-kb", s.handleImportKB)
	s.mux.HandleFunc("/api/translation/import-bitext", s.handleImportBitext)
	s.mux.HandleFunc("/api/translation/import-tmx", s.handleImportTMX)
	s.mux.HandleFunc("/api/translation/kb-stats", s.handleKBStats)
}

// routesTenant 注册租户管理路由（管理后台）。
func (s *Server) routesTenant() {
	s.mux.HandleFunc("/api/tenant/list", s.handleTenantList)
	s.mux.HandleFunc("/api/tenant/create", s.handleTenantCreate)
	s.mux.HandleFunc("/api/tenant/update", s.handleTenantUpdate)
	s.mux.HandleFunc("/api/tenant/invite-enabled", s.handleTenantInviteEnabledGet)
	s.mux.HandleFunc("/api/tenant/status", s.handleTenantStatus)
	s.mux.HandleFunc("/api/tenant/delete", s.handleTenantDelete)
	s.mux.HandleFunc("/api/tenant/export", s.handleTenantExport)
	s.mux.HandleFunc("/api/tenant/erase", s.handleTenantErase)
	s.mux.HandleFunc("/api/tenant/branding", s.handleTenantBranding)
	// 超管为指定租户开通/撤销「品牌定制」权限（免套餐）
	s.mux.HandleFunc("/api/admin/tenant/brand-grant", s.handleAdminBrandGrant)
	// Caddy on_demand_tls 权限回调（localhost 调用，无需鉴权）：放行本平台域名的子域证书自动签发
	s.mux.HandleFunc("/api/caddy/ask", s.handleCaddyOnDemandAsk)
	s.mux.HandleFunc("/api/footer-links", s.handleFooterLinksGet)
	s.mux.HandleFunc("/api/admin/footer-links", s.handleFooterLinksSet)
	s.mux.HandleFunc("/api/admin/tenants/grant-trial", s.handleGrantTrial)
	s.mux.HandleFunc("/api/admin/mail-templates", s.handleAdminMailTemplates)
}

// routesAuth 注册认证与用户管理路由。
func (s *Server) routesAuth() {
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/register", s.handleRegister)
	s.mux.HandleFunc("/api/auth/me", s.handleMe)
	s.mux.HandleFunc("/api/auth/change-password", s.handleChangePassword)
	s.mux.HandleFunc("/api/auth/forgot-password", s.handleForgotPassword)
	s.mux.HandleFunc("/api/auth/reset-password", s.handleResetPassword)
	s.mux.HandleFunc("/api/auth/email-code", s.handleEmailCode)
	s.mux.HandleFunc("/api/auth/register-config", s.handleRegisterConfig)
	s.mux.HandleFunc("/api/admin/users", s.handleAdminUsers)
	s.mux.HandleFunc("/api/admin/users/create", s.handleAdminUserCreate)
	s.mux.HandleFunc("/api/admin/users/update", s.handleAdminUserUpdate)
	s.mux.HandleFunc("/api/admin/users/reset-password", s.handleAdminUserResetPassword)
	s.mux.HandleFunc("/api/admin/users/delete", s.handleAdminUserDelete)
	s.mux.HandleFunc("/api/admin/invite-codes", s.handleInviteCodes)
	s.mux.HandleFunc("/api/admin/invite-codes/create", s.handleInviteCodeCreate)
	// ★ 邀请裂变（白皮书 §五）：我的邀请码/记录 + 二维码
	s.registerReferralRoutes()
	s.routesEditor()
}

// routesTickets 注册工单与审批路由。
func (s *Server) routesTickets() {
	s.mux.HandleFunc("/api/tickets", s.handleTickets)
	s.mux.HandleFunc("/api/tickets/create", s.handleTicketCreate)
	s.mux.HandleFunc("/api/tickets/create-file", s.handleTicketCreateFile)
	s.mux.HandleFunc("/api/tickets/run", s.handleTicketRun)
	s.mux.HandleFunc("/api/tickets/detail", s.handleTicketDetail)
	s.mux.HandleFunc("/api/tickets/download", s.handleTicketDownload)
	s.mux.HandleFunc("/api/tickets/delete", s.handleTicketDelete)
	s.mux.HandleFunc("/api/tickets/cancel", s.handleTicketCancel)
	s.mux.HandleFunc("/api/notifications", s.handleNotifications)
	s.mux.HandleFunc("/api/notifications/unread", s.handleNotificationsUnread)
	s.mux.HandleFunc("/api/notifications/read", s.handleNotificationsRead)
	s.mux.HandleFunc("/api/notifications/read-all", s.handleNotificationsReadAll)
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
	s.mux.HandleFunc("/api/admin/kb-packages/status", s.handleKBPackageStatus)
	s.mux.HandleFunc("/api/admin/kb-packages/share", s.handleKBPackageShare) // ★ 部门包跨部门共享开关（2026-08-26 KB继承链）
	s.mux.HandleFunc("/api/admin/kb-index/rebuild", s.handleKBIndexRebuild)
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
	s.mux.HandleFunc("/api/admin/safety-phrases/bulk-import", s.handleSafetyPhraseBulkImport)
	s.mux.HandleFunc("/api/admin/safety-phrases/status", s.handleSafetyPhraseStatus)
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
	s.mux.HandleFunc("/api/me/context", s.handleMeContext)
	s.mux.HandleFunc("/api/me/update-email", s.handleUpdateEmail)
	s.mux.HandleFunc("/api/me/deactivate", s.handleDeactivateAccount) // ★ 自助注销（2026-08-26 需求）
	s.mux.HandleFunc("/api/me/email-code", s.handleMeEmailCode)
	s.mux.HandleFunc("/api/admin/tm-review/list", s.handleTmReviewList)
	s.mux.HandleFunc("/api/admin/tm-review/approve", s.handleTmReviewApprove)
	s.mux.HandleFunc("/api/admin/tm-review/reject", s.handleTmReviewReject)
	s.mux.HandleFunc("/api/admin/tm-review/adopt", s.handleTmReviewAdopt)
	s.mux.HandleFunc("/api/package/subscribe", s.handlePackageSubscribe)
	s.mux.HandleFunc("/api/admin/packages", s.handleAdminPackages)
	s.mux.HandleFunc("/api/admin/packages/create", s.handleAdminPackageCreate)
	s.mux.HandleFunc("/api/admin/packages/update", s.handleAdminPackageUpdate)
	s.mux.HandleFunc("/api/admin/packages/delete", s.handleAdminPackageDelete)
	s.mux.HandleFunc("/api/admin/packages/settings", s.handleAdminPackageSettings)
	s.mux.HandleFunc("/api/admin/packages/settings/save", s.handleAdminPackageSettingsSave)
	s.mux.HandleFunc("/api/admin/packages/qr-upload", s.handleAdminQRUpload)
	s.mux.HandleFunc("/api/qr-image/", s.handleQRImage)
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
	s.mux.HandleFunc("/api/apikeys/limit", s.handleAPIKeyLimit)
	s.mux.HandleFunc("/api/apikeys/reveal", s.handleAPIKeyReveal)
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
// ★ 翻译统一异步任务模型：创建任务→工单 ID→轮询（文本 15s/文件 60s）→产物下载。
func (s *Server) routesOpenAPI() {
	s.mux.HandleFunc("/openapi/v1/tasks", s.handleOpenAPITaskCreate)            // 创建任务（POST）
	s.mux.HandleFunc("/openapi/v1/tasks/status", s.handleOpenAPITaskStatus)     // 轮询状态（GET ?id=）
	s.mux.HandleFunc("/openapi/v1/tasks/download", s.handleOpenAPITaskDownload) // 产物下载
	s.mux.HandleFunc("/openapi/v1/translate", s.handleOpenAPITranslateSync)     // 同步短文翻译（划译插件/Office taskpane 专用，2026-08-26 断链修复）
	s.mux.HandleFunc("/openapi/v1/balance", s.handleOpenAPIBalance)             // 余额查询
	s.mux.HandleFunc("/openapi/v1/kb/stats", s.handleOpenAPIKBStats)
	s.mux.HandleFunc("/openapi/v1/billing/usage", s.handleOpenAPIUsage)
	s.mux.HandleFunc("/openapi/v1/apikey/rotate", s.handleOpenAPIKeyRotate)
	s.mux.HandleFunc("/openapi/docs", s.handleOpenAPIDocs)
}

// Handler 返回完整的 http.Handler（依次包裹版本/指标/租户/CORS 中间件）。
// 返回: 可交给 http.ListenAndServe 使用的 http.Handler。
func (s *Server) Handler() http.Handler {
	return s.withAPIVersion(s.withTraceID(s.withMetrics(s.withTenant(s.withCORS(s.withBodyLimit(s.withAccessLog(s.mux)))))))
}

// apiVersionKey ctx 存取键：当前请求命中的 API 版本（默认 v1）。
type apiVersionKey struct{}

// WithAPIVersion 写入 ctx（供未来 v2 路由分支使用）。
func WithAPIVersion(ctx context.Context, v string) context.Context {
	return context.WithValue(ctx, apiVersionKey{}, v)
}

// APIVersionFrom 读取 ctx 中的 API 版本（缺省 v1）。
func APIVersionFrom(ctx context.Context) string {
	if v, ok := ctx.Value(apiVersionKey{}).(string); ok && v != "" {
		return v
	}
	return "v1"
}

// withAPIVersion 解析请求 API 版本（优先级：Accept 头 application/vnd.langcross.<v>+json
// > X-API-Version 头 > 默认 v1），写入 ctx 并回显 X-API-Version 响应头。
// 当前仅 v1 生效，v2 路径预留；未知版本不拒绝（向后兼容），仅记录。
func (s *Server) withAPIVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := "v1"
		if h := r.Header.Get("X-API-Version"); h != "" {
			v = normalizeVersion(h)
		} else if acc := r.Header.Get("Accept"); strings.Contains(acc, "vnd.langcross.") {
			// 形如 application/vnd.langcross.v2+json
			if idx := strings.Index(acc, "vnd.langcross."); idx >= 0 {
				rest := acc[idx+len("vnd.langcross."):]
				if end := strings.IndexAny(rest, "+; "); end > 0 {
					v = normalizeVersion(rest[:end])
				} else {
					v = normalizeVersion(rest)
				}
			}
		}
		w.Header().Set("X-API-Version", v)
		next.ServeHTTP(w, r.WithContext(WithAPIVersion(r.Context(), v)))
	})
}

// normalizeVersion 规整版本串（v1 / 1 / V1 -> v1）。
func normalizeVersion(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimPrefix(raw, "v")
	if raw == "" {
		return "v1"
	}
	return "v" + raw
}

// maxJSONBody 非 multipart 请求体上限（JSON 接口防超大请求；文件上传走 multipart 不受限）。
// 背景图等以 base64 经 JSON 上传，故上限设为 5MB——前端上传前需将图片缩放/压缩至该体积内。
const maxJSONBody = 5 << 20 // 5MB

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
		// 3. 未登录：仅信任 API Key 所属租户（开放 API 路径）。
		//    ★ 整改 A3：用 NoTouch 变体——中间件解析不代表一次业务调用，
		//    计数由 openapi handler 内 authenticateAPIKey 唯一执行，消除日配额双扣。
		if ak, authErr := s.authenticateAPIKeyNoTouch(r); authErr == "" && ak != nil && ak.TenantID > 0 {
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
// 超级管理员（平台级，tenant_id=0）：X-Tenant-ID 显式指定（>0）时用该租户；
// 未指定或为 0 时默认平台上下文（tid=0，即「能言」根组织视角）。
// 其余用户固定自身租户。
// 参数 r: HTTP 请求；u: 当前用户。返回: 生效租户 ID。
func (s *Server) effTenant(r *http.Request, u *store.User) int64 {
	if auth.IsSuperAdmin(u) {
		if id := tenant.FromContext(r.Context()); id > 0 {
			return id
		}
		return 0 // 平台上下文
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
		// 来源校验：无 Origin 头（同源导航/非浏览器）直接放行。
		// ★ /openapi/* 前缀无条件反射（评审整改 A2）：开放 API 全部为 Key 鉴权、
		//   无 Cookie 会话面，CSRF 不成立——放行跨域以支持浏览器划词插件等第三方前端
		//   （MV3 content script 即便持有 host_permissions，部分环境仍受页面 CORS 约束）。
		origin := r.Header.Get("Origin")
		if origin == "" || s.corsAllowed(origin) || strings.HasPrefix(r.URL.Path, "/openapi/") {
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
	// ★ 整改 R-M7：未配置白名单时默认拒绝反射（deny-by-default），杜绝「任意 Origin 跨域读取」
	//   的管理/租户接口。需跨域的前端请在 system_config「cors_origins」显式登记。
	//   （/openapi/* 前缀在中间件层无条件反射，属开放 API 设计，不在此函数管辖内）
	if s.Cfg == nil || len(s.Cfg.CORSOrigins) == 0 {
		return false
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
	// 校验用户存在且处于激活状态（按 JWT 中的 user_id + tenant_id）。
	// ★ 注销宽限语义（2026-08-26）：deactivating 当日视为可用（放行），次日起等效停用。
	u, err := s.Store.GetUser(claims.UserID, claims.TenantID)
	if err != nil {
		return nil
	}
	if s := auth.EffectiveUserStatus(u.Status, u.DeactivatedAt); s != store.UserActive && s != store.UserDeactivating {
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

// writeError 写出统一结构化错误（含正确 HTTP 状态码与 trace_id）。
// 参数 w: 响应写入器；r: 请求（提供上下文与 trace_id）；e: 结构化错误。
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, e *apierrors.APIError) {
	apierrors.WriteError(w, r.Context(), e)
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
			"code":    code,
			"name":    config.LangNames[code],
			"name_en": config.LangNamesEn[code],
			"flag":    config.Flags[code],
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
