# 后台结构调整 + 商业化补齐 · 执行计划（REFACTOR_PLAN）

> 创建：2026-08-20 ｜ 状态：**已完成并归档** ｜ 关联：PLAN.md / COMMERCIAL_TODO.md / PROGRESS.md
>
> ⚠️ **已归档（2026-08-22）**：本计划已全部执行完成。后续演进见 `重构方案.md`（一期）与 `重构方案二期.md`（二期）。

## 一、背景与目标

用户反馈：**后台结构一塌糊涂**，需要调整；同时**对外卖还缺硬能力**。本计划分 6 阶段落地：

1. 后台结构重构（前端 1281 行单文件 → 分组面板；后端 admin.go 1250 行/47 handler → 按域拆文件）——**纯重构，零行为变更**
2. P0 安全硬伤（收钱前提）：密码 bcrypt、密钥环境变量化、CORS 白名单、暴力破解防护、请求体限制
3. P1 运维能力：优雅停机、结构化日志、webhook 回调、定时自动备份
4. P2 商业化：定价页、条款页（SLA/服务条款/DPA）、i18n 中英
5. 全量验证 + 部署生产 + 更新文档

## 二、现状问题清单

### 前端后台（AdminDashboard.vue，1281 行）
- 19 个平铺 tab 挤在一个单文件里，无分组、无子组件、无路由
- `<template>` 19 个 `v-if="active === 'xxx'"` section，全部平铺
- 所有 ref/computed/function 集中在 `<script setup>`，超过 1200 行
- `api/index.ts` 635 行全部函数堆一起，无按域分层

### 后端 admin.go（1250 行 / 47 handler）
- 8 大业务域混排：flow / models / routes / policy / KB / evals / system / billing / apikeys / openapi
- `server.go routes()` 100+ 行平铺注册，仅靠注释分区

### 对外卖缺口（对照 COMMERCIAL_TODO.md）
- 密码仍是 SHA-256+固定盐（`auth.go PasswordHash`）
- JWT 密钥硬编码（`auth.go Secret = "trans-platform-jwt-secret-2026"`）；内置 LLM Key 编译进二进制（`config.go Default()`）
- CORS `Access-Control-Allow-Origin: *`（`server.go withCORS`）
- 无暴力破解防护、无请求体大小限制
- 无 webhook、无优雅停机、无结构化日志、无定时备份
- 无定价页 / 条款页 / i18n

## 三、分阶段执行

### 阶段 1：后端按域拆分（纯重构，零行为变更）
- `internal/api/admin.go` → 拆 7 文件：
  - `admin_flow.go`（handleFlowConfig/Save/RunTicket）
  - `admin_models.go`（handleModels/Save、handleModelRoutes/Save、handlePolicy/Save）
  - `admin_kb.go`（handleKBPackages/Create/Update/Delete、handleKBEntries/Add/Import/Delete、handleSafetyPhrases/Add/Delete）
  - `admin_evals.go`（handleEvalsList、handleSystemHealth/Audit、handleAlerts/Resolve）
  - `admin_billing.go`（handleBalance/Usage/Orders、handleOrderCreate/Pay/Refund、handleInvoices/Create、handleBillingConfig/Save、handleTenantQuota/Save）
  - `admin_apikeys.go`（handleAPIKeys/Create/Status/Rotate/Delete）
  - `admin_openapi.go`（handleOpenAPIDocs/Translate/KBStats/Usage/KeyRotate）
- `server.go routes()` 按域分组函数：`routesCore()` / `routesAuth()` / `routesTenant()` / `routesTickets()` / `routesAdmin()` / `routesBilling()` / `routesOpenAPI()`
- 验证：`go build` + `go vet` + `go test` + 本地启动全路由回归

### 阶段 2：前端后台重构（顶部一级分类 + 左侧面板）
- 新建 `frontend/src/components/admin/`，拆 11 个子面板：
  - **经营**：`Overview.vue`（系统看板+审计）、`Alerts.vue`（监控告警）、`Usage.vue`（用量）、`Billing.vue`（计费配置+配额+充值+发票）
  - **组织**：`Users.vue`（账户）、`Tenants.vue`（租户）、`Invites.vue`（邀请码）
  - **内容**：`Kb.vue`（行业/KB/安全句）
  - **引擎**：`Models.vue`（模型+路由+策略）、`Workflow.vue`（流程引擎+evals）
  - **开放**：`ApiKeys.vue`（API Key）、`Tickets.vue`（工单+审批）
- `AdminDashboard.vue` 瘦身为壳：顶部 5 个一级分类页签 + 组内左侧面板列表 + `<component :is>` 动态渲染
- 抽取共享 UI 原子：`admin/ui.ts`（卡片/表格/按钮样式 + 通用工具函数 fmtTime/shortJSON/prettyJSON/statusLabel）
- `api/index.ts` 按域拆：`api/index.ts`（保留公共）→ 新增 `api/admin.ts` / `api/billing.ts` / `api/tenant.ts` / `api/openapi.ts`
- 验证：`npm run build` + 手动回归 19 个面板

### 阶段 3：P0 安全硬伤
1. **密码 bcrypt**：`PasswordHash/CheckPassword` 改 bcrypt（`golang.org/x/crypto/bcrypt`），登录时旧 SHA-256 哈希自动升级（`$sha256$` 前缀识别）
2. **密钥环境变量化**：
   - `JWT_SECRET`：未配置随机生成 + 启动日志告警
   - 内置 LLM Key：改为环境变量优先，无则启动告警提示（不再无条件回填硬编码 Key）
   - `ADMIN_TOKEN` 已有
3. **CORS 白名单**：新增 `CORS_ALLOW_ORIGINS` 配置（默认放行本地 + 生产域名），去掉 `*`
4. **暴力破解防护**：登录失败计数（内存 map + 锁），5 次失败锁定 15 分钟 + IP 级限流
5. **请求体/上传大小限制**：`http.MaxBytesReader` 包装（如 10MB）

### 阶段 4：P1 运维能力
- **优雅停机**：`signal.Notify(SIGINT/SIGTERM)` + `http.Server.Shutdown`（带超时，在途翻译/SSE 处理完）
- **结构化日志**：新增 `internal/log/`，请求级（method/path/status/tenant_id/耗时）+ LLM 调用级（provider/model/err），JSON 输出
- **webhook 回调**：`webhooks` 表 + 租户配置回调 URL + 翻译完成触发 POST（失败重试 N 次）
- **定时自动备份**：SQLite 快照 + 保留 N 份（复用看门狗周期）

### 阶段 5：P2 商业化
- **定价页**：公开 `/pricing` 页（读 rate_card 展示 token 单价表）
- **条款页**：`/docs/terms`（服务条款）、`/docs/sla`、`/docs/privacy`（DPA）
- **i18n 中英**：前端文案抽 `i18n.ts`（本轮覆盖后台与登录关键页，翻译工作台次轮）

### 阶段 6：全量验证 + 部署
- `go build/vet/test` + `npm run build` + 本地端到端（注册→登录→翻译→计费→审批→开放 API→webhook→优雅停机）
- 交叉编译部署生产（备份 `translator-server.bak.<ts>`），验证 `/metrics`、告警、后台新导航
- 更新 `COMMERCIAL_TODO.md` / `PROGRESS.md` / `PLAN.md`

## 四、风险与注意

| 风险 | 应对 |
|------|------|
| 拆分改动破坏行为 | 阶段 1-2 纯重构，改后立即 go build/vet/test + npm build + 本地回归 |
| JWT/LLM 密钥环境变量化暴露存量密钥 | 部署前在服务器 systemd 配 `JWT_SECRET` + 新 Key；旧 Key 在供应商后台吊销 |
| 生产账号密码策略 | 需确认是否强制改密 |
| bcrypt 存量兼容 | `$sha256$` 前缀识别旧哈希，登录时自动升级 |
| 前端拆分回归 | 每个面板逐个验证 19 个模块 |

## 五、验收标准
- 后台 19 个面板在新导航下全部可用，功能与改造前一致
- 后端 `go build/vet/test` 全绿，`gofmt -l` 无输出
- 前端 `npm run build` 成功
- 生产部署后 `/metrics` 正常、0 panic、错误率正常
- 密码 bcrypt、CORS 白名单、暴力破解防护、webhook、优雅停机生效