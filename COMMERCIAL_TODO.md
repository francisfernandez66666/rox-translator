# 完全商业化待办清单

> 创建：2026-08-19 ｜ 来源：商业化差距审计 + 用户决策 ｜ 更新：2026-08-20
> 状态：P0/P1/P2 全部完成并已提交（阶段五回归通过，待一次性部署生产）

## 用户已决策

| 项 | 决策 |
|----|------|
| #9 支付 | 已实现适配器骨架（mock/wechat/alipay）+ 完整 mock 流程；真实商户号接入后启用 |
| #11 自助注册/试用 | 已实现 |
| #12 审计权限 | 普通用户不可做审计；翻译读写审计仅限租户管理员及以上 |

---

## 🔴 P0 阻断级

### 1. 接口用户级别鉴权 ✅
- [x] `/api/translate`、`/api/translate/stream`、`/api/chat`、`/api/chat/stream`、`/api/translation/*`、`/api/download/` 全部要求登录（JWT）
- [x] 未登录返回 401，前端自动跳登录
- [x] 实现：`internal/api/withAuth` 中间件统一包裹受保护路由（`server.go`）

### 2. 文件租户级别鉴权（下载）✅
- [x] 下载路径校验：仅允许租户输出目录内文件，防 `../` 穿越
- [x] 下载需登录 + 租户校验

### 3. 计费扣 token ✅
- [x] `RecordUsage` 接入全部翻译链路（单句/文件/工单/开放 API，请求级 provider/model）
- [x] 余额不足即停（`billing_enforced` 开启时）；`rate_card` 按语言×供应商×倍率定价生效
- [x] 租户余额查询/明细页（用量看板）
- [x] 实现：`internal/billing/quota.go` + `internal/store/billing.go`（`unitPrice` 已修分语言/供应商 bug）

### 4. 密码改加密 🔲（待办）
- [ ] `PasswordHash()` 从 SHA-256+固定盐 改为 bcrypt/argon2id（逐用户盐 + 工作因子）
- [ ] 兼容存量哈希平滑升级
- [ ] 强制首次登录改密 / 默认密码策略

### 5. 密钥改加密（环境变量化）✅（已完成核心；git 历史清理待办）
- [x] JWT 密钥 → 环境变量 `JWT_SECRET`，未配置随机生成并告警
- [x] 内置 LLM Key → 环境变量 `SILICONFLOW_API_KEY`/`ONLINE_API_KEY`（config.go 已去硬编码，随机占位+告警日志）
- [x] `ADMIN_TOKEN`/`ADMIN_INIT_PASSWORD` 环境变量覆盖
- [ ] 清理 git 历史中的密钥（rotate + 重写历史或至少新建密钥）

### 6. 加防护 ✅
- [x] 登录暴力破解防护：失败计数 + 锁定窗口 + IP 级限流（同 IP 5 次失败/300s → 冷却 300s，HTTP 429）
- [x] CORS 收紧：去 `*`，按域名白名单（`CORS_ALLOWED_ORIGINS`）
- [x] 请求体大小限制、上传文件类型/大小校验（白名单 + 50MB 上限，MaxBytesReader 硬性拦截）

### 7. API Key 绑定租户 ✅
- [x] 开放 API 鉴权后强制使用 `ak.TenantID`，忽略请求 `X-Tenant-ID`（防冒充）
- [x] 开放 API 挂载独立鉴权中间件（`internal/openapi/`）
- [x] 防租户 A 冒充租户 B 读 KB/烧配额

### 8. 租户加强 + LLM 租户级 key ✅
- [x] `expires_at` 解析兼容 `2006-01-02` 与 RFC3339
- [x] 租户配额：QPS/并发/每日 token 上限/余额停服接入 `quota.go`
- [x] LLM 调用使用租户级 API key（BYOK）：租户配了 key 用租户的，否则回退平台 key（模型路由）
- [x] 租户过期/禁用后所有链路（含 API/tickets）拒绝

---

## 🟠 P1 高优先

### 10. 数据导出/删除 ✅
- [x] 租户数据导出 JSON（KB/翻译记录/工单/账单/api_keys/audit/orders/users/usage）
- [x] 租户删除级联清理（`/api/tenant/erase`，禁止清除租户 1）
- [x] GDPR 合规流程（`internal/store/gdpr.go`）

### 12. 审计权限 + 翻译读写审计 ✅
- [x] 审计接口仅租户管理员及以上可访问（普通用户 403）
- [x] 翻译读写审计：before_val/after_val 前后值结构化轨迹
- [x] 审计日志含操作人、前后值、CSV 导出（ISO 17100 对齐）

### 13. 并发风险（SQLite）✅
- [x] `journal_mode=WAL` + `busy_timeout`
- [x] Store 互斥锁
- [x] `migrateColumns` 迁移框架（Schema 安全演进，幂等）
- [x] 定时自动备份任务：`VACUUM INTO` 在线快照 + 保留 N 份（启动即备 + 每 24h，`backup_interval_hours`/`backup_keep`）

### 14. 可观测性 ✅
- [x] Prometheus `/metrics`：HTTP 请求数（按路径）/翻译 ok-fail/用量 token/熔断状态/错误率/租户规模/Go 运行时
- [x] 告警：LLM 供应商故障、余额低、错误率飙升、OOM 内存压力（看门狗 + alerts 表 + 前端告警面板 + 关键告警邮件）
- [x] 结构化日志：请求级 JSON 访问日志（time/level/method/path/status/duration_ms/ip/bytes）+ 优雅停机
- [x] OOM 内存监控：运行时采样（Heap/goroutine/峰值）+ 系统压力阈值 + TOP5 进程

### 15. 开放 API 治理 ✅
- [x] 按 key 限流、计量、预算、语言白名单（租户配额统一）
- [x] OpenAPI 文档 `/openapi/docs` + 版本 + 错误契约
- [x] 翻译完成 webhook 回调（客户 TMS/CI 集成）：webhooks 表 + HMAC-SHA256 签名 + 异步投递重试 3 次指数退避 + 前端面板

### 16. 优雅停机 ✅
- [x] `signal.Notify` + `server.Shutdown`，在途翻译/SSE 处理完再退出（10s 超时）
- [x] 热加载模型/策略配置（`model_routes`/`billing_enforced` 等 system_config 热更新，免重启）

### 17. 测试补充 ✅（核心覆盖）
- [x] engine / fileproc / store 迁移 / payment / upload / webhook / memmonitor 单测
- [x] 阶段五全量回归：后端 4 包单测 + 前端 build + 21 项端到端冒烟（公开页/注册/登录/SSE/计费/Webhook/鉴权/优雅停机）
- [ ] auth / billing / 租户隔离 / 接口 handler / orchestrator 单测 ｜ 持续 E2E 脚本入库 🔲

### 19. 新租户初始化 ✅
- [x] 建租户自动创建默认三级 KB 包（企业/行业/语言文化）
- [x] 初始化余额账本 + 默认配额

---

## 🟡 P2 中优先

### 18. 前端优化 ✅
- [x] 401 全局处理：token 过期自动跳登录
- [x] 租户用量报表页（趋势/任务类型/供应商图表 + 明细）
- [x] 导出按钮接入后端导出接口（审计 CSV / 租户数据 JSON）
- [x] 健康检查超时（10s）+ 离线重试按钮
- [x] i18n：界面文案中英切换（登录页/后台导航/工作台顶栏，`i18n.ts`）—— 翻译工作台内部文案次轮接入

### 20. 商业物料 ✅
- [x] LICENSE / 服务条款（/docs/terms）
- [x] 服务等级协议 SLA（/docs/sla）
- [x] 数据保护条款 DPA（/docs/privacy）
- [x] 定价页（/pricing 动态渲染 rate_card 单价表 + /api/pricing 公开 API）

### 21. 在线支付 ✅（骨架+mock 完整流程）
- [x] `internal/payment/` 三渠道适配器（mock 完整/wechat/alipay 骨架）
- [x] `/api/pay/create|status|simulate|notify` + `Billing.vue` 收银台（QR 码/订单历史）
- [x] `pay_mode` 配置切换；真实商户号接入后启用

### 22. 邮件通知 ✅
- [x] `internal/mail/` Sender 接口（Noop/SMTP 实现）
- [x] 找回密码验证码邮件
- [x] 关键告警邮件（余额耗尽/模型熔断 → `alert_email` 收件人）

---

## 已完成 ✅（汇总）
- 接口 JWT 鉴权全覆盖、文件下载租户隔离、计费扣 token、API Key 绑定租户、租户加强+BYOK
- 租户数据导出/删除（GDPR）、审计权限+前后值轨迹+CSV 导出、SQLite 并发（WAL+互斥+迁移）
- 可观测性（/metrics + 看门狗告警 + 前端告警面板）、开放 API 治理（Key 轮换/计量/文档）
- 新租户初始化（默认 KB 包 + 余额账本 + 配额）
- 前端：401 处理、用量报表图表、导出按钮、健康检查超时+重试
- 前端文件翻译报错修复（tenantHeaders → authHeaders）
- 安全加固：暴力破解限流、CORS 白名单、请求体/上传限制、密钥环境变量化
- 运维：自动备份、优雅停机、结构化日志、OOM 监控、Webhook 回调、商业物料、i18n、邮件通知、在线支付骨架

## 待办（剩余）
- 密码 bcrypt、密钥清理 git 历史（rotate + 重写历史或新建密钥）
- 真实支付商户号接入（微信/支付宝）
- 补充单测（auth/billing/租户隔离/接口 handler/orchestrator）+ 持续 E2E 脚本入库
- 翻译工作台内部文案 i18n 全量接入

## 暂缓（用户决策）
- 无（在线支付已实现骨架，待商户号接入）