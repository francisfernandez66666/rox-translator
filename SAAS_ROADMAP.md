# 彻底 SaaS 化改造路线图（10 项）

> 创建：2026-08-20 ｜ 来源：用户十项需求评审 + 代码库现状调研 + 四项关键决策
> 状态：**阶段一（互译+i18n）与阶段二（模型分阶段+超管维护）已完成，阶段三起待实施** ｜ 关联文档：PLAN.md / SAAS_GAP.md / COMMERCIAL_TODO.md / PROGRESS.md / 部署指南.md

---

## 〇、背景

现有系统已完成 P0 MVP + SaaS 基础层（多租户 / 三级角色 / 计费 / 支付骨架 / 管理后台 / 开放 API / 可观测性）并上线生产（`https://translator.quant-trading.top`）。
用户提出以下 10 项，是「彻底 SaaS 化」仍缺失的能力。

## 一、需求清单

| # | 需求 | 对应阶段 |
|---|------|---------|
| 1 | 流程引擎里知识库/初翻/evals/校对可分别选择不同模型与 API | 阶段二 |
| 2 | 商业包：新用户免费体验句数、付费包（包月 X 句）、翻译增量包（超管自定义） | 阶段三 |
| 3 | 超级管理员负责新建/维护模型与 API 接口 | 阶段二 |
| 4 | 分级消耗量看板：普通用户个人级 / 管理员组织-子组织-用户级 / 超管模型 token 成本 | 阶段四 |
| 5 | 付费支持 SDK 支付 + 弹窗扫静态二维码支付（超管后台开关+配码）；扫码后点「我已付费」通知超管审核开通 | 阶段六 |
| 6 | 前台上传知识库移入后台；仅租户管理员/超管可上传并分类（租户管理员：企业包/部门包；超管：行业包/企业包/文化习惯包/部门包） | 阶段五 |
| 7 | 租户管理员可给自己租户开通账号并开通新租户管理员；新超级管理员仅超管可开通 | 阶段五（后端已满足） |
| 8 | 新用户注册校验：完全新租户注册需选择行业（开通对应行业包） | 阶段三 |
| 9 | 全量 i18n：中英文切换后页面内容全部跟随切换 | 阶段一 |
| 10 | 支持语言互相翻译（英语→中文、英语→法语等任意方向） | 阶段一 |

## 二、已确认决策（2026-08-20 用户拍板）

| 决策项 | 结论 |
|--------|------|
| 商业包「句」如何计数 | **每源句 × 每个目标语言 = 消耗句数**（与 usage_ledger 逐语言计量一致，成本核算精确） |
| 模型/API 由谁维护 | **模型全部归超管**：平台路由 + 各流程阶段模型均由超管配置；收掉租户管理员的模型配置权限 |
| 注册行业来源 | **超管创建行业包即成为注册行业选项**；新租户选行业后自动开通对应行业包，零冗余配置 |
| 实施顺序 | 按本路线图 6 个阶段顺序推进，每阶段独立提交上线 |

## 三、现状与差距（代码调研结论）

| 现状（文件:行） | 差距 |
|----------------|------|
| 引擎所有阶段共用 `resolveModel`（`engine/engine.go:625`），先租户 `model_config` → 全局 `ModelRoutes` 权重 → `Online*` | 无法按阶段选模型（Req1） |
| evals 硬编码 `Cfg.OnlineModel`（`evals/evals.go:82`），忽略租户/路由配置 | evals 阶段模型不可配（Req1） |
| 计费为 token 制（`balance_accounts`），`trial_tokens` 默认 50000；无句包概念 | 需商业包/体验句/增量包（Req2） |
| 支付适配器 mock/wechat/alipay + 下单/轮询/模拟/回调（`payment/payment.go`、`api/pay.go`） | 无静态二维码 + 「我已付费」人工确认（Req5） |
| KB 上传在 ChatWindow 弹窗（`ChatWindow.vue:241-347`），接口 `recognize-kb`/`import-kb` 无角色校验且写入平台 KB | 需移入后台 + 按包隔离 + 角色限制（Req6） |
| 包类型仅 `tenant/industry/locale`（`store/kbpackages.go:27`） | 需 `department` 部门包（Req6） |
| `handleAdminUserCreate`（`api/auth.go:350`）已限制「仅超管可建超管」，租户管理员可建 user/tenant_admin | Req7 后端已满足，仅补前端交互 |
| 注册 `handleRegister`（`api/register.go`）无行业选择，试用按 token 发放 | 需行业必填 + 行业包 + 体验句（Req8） |
| i18n 仅覆盖登录页 + 后台导航（`i18n.ts`，约 196 行）；12 个 admin 面板 + ChatWindow 全中文硬编码 | 需全量覆盖（Req9） |
| `singleLang` 源语言硬编码 `"zh"`（`engine/engine.go:691`）；KB 匹配固定 `srcLang="zh"`（`orchestrator/workflow.go:128`） | 非中文源翻译指令与 KB 匹配错误（Req10） |

---

## 四、阶段一：语言互译 + 全量 i18n（Req 10 + Req 9）

### 后端 · 语言互译
- `engine/engine.go`
  - `TranslateOne` / `translateLangsConcurrent` / `singleLang` / `TranslateOtherLang` 透传真实源语言（`DetectSourceLang` 结果），去掉 `sourceLang` 硬编码 `"zh"`
  - `translateInstruction(source, target)` 按实际源语言名生成指令（源语言名从 `config.LangNames` 取，缺则回退代码）
- `orchestrator/workflow.go`
  - `runKBMatch` 用 `DetectSourceLang(t.SourceText)` 作为 `FindEntriesBySource` 的 `srcLang`
  - `runAIInitial` / `runReview` / 驳回重翻传入源语言
- `api/kb.go`：`isSourceCol` 支持英文 source 列（`source`/`english` 等）与任意语言源列

### 前端
- ChatWindow 增加**源语言选择器**（默认中文）；目标语言可自由组合（如 英→中、英→法）
- 非中文源提示「纯模型翻译」（KB 语义兜底仅对中文源生效）

### 前端 · 全量 i18n
- i18n 字典拆分多文件：`i18n.ts`（通用+登录+导航）+ `i18n/admin.ts`（12 个面板）+ `i18n/workspace.ts`（ChatWindow/App）
- 所有 `.vue` 模板硬编码中文 → `{{ t('...') }}`：标题/按钮/占位符/提示/hint/confirm/alert 全量覆盖
- 语言切换（`lang` ref 已响应式）后全页面跟随刷新

### 验证
- 英→中、英→法端到端冒烟；切换语言后全页面无残留中文

---

## 五、阶段二：模型分阶段配置 + 超管统一维护（Req 1 + Req 3）✅ 已完成

### 后端
- 新增 `system_config stage_models`（JSON）：
  ```json
  {
    "kb_match":   {"provider":"..","api_base":"..","api_key":"..","model":".."},
    "ai_initial": {"provider":"..","api_base":"..","api_key":"..","model":".."},
    "evals":      {"provider":"..","api_base":"..","api_key":"..","model":".."},
    "review":     {"provider":"..","api_base":"..","api_key":"..","model":".."}
  }
  ```
- `engine` 新增 `resolveStageModel(ctx, stage)`；各 LLM 调用点传阶段（初翻=`ai_initial`、审校=`review`、evals=`evals`、KB 兜底翻译=`kb_match`）；未配置该阶段回退现有 `resolveModel`
- `evals/evals.go`：Evaluator 增加租户 + 阶段模型解析（不再硬编码 `Cfg.OnlineModel`）
- 接口：`GET/POST /api/admin/stage-models`（`requireAdminUser`）
- `handleModelsSave`（`api/admin_models.go`）改为仅超管可写；租户管理员读取返回只读

### 前端
- `admin/Models.vue` 重构：超管可见「模型路由」（现有）+「阶段模型」卡片（kb/初翻/evals/校对各选 provider+model+api）；租户管理员隐藏模型配置
- `admin/Workflow.vue` 保持步骤启停（与阶段模型配置并列）

### DB
- 无新表（`system_config` 存 JSON），迁移幂等

### 验证
- 各阶段配置不同 model，`usage_ledger` 确认各阶段实际使用不同 provider/model

---

## 六、阶段三：商业包 + 注册行业（Req 2 + Req 8）

### 后端
- **新表 `packages`**：`id / code / name / ptype(free|paid|increment) / sentences / price_money / duration_days / enabled / sort / created_at / updated_at`
- **`tenants.permissions` 扩展**（JSON）：`package_code` / `subscribed_at` / `sentence_balance`（剩余句数）
- **句子计量**：`api/stream.go` / `api/upload.go` 按源句（换行/句号切分）× 目标语言数计句，写入 `usage_ledger`（新增 `sentences` 或沿用 quantity 语义）
- **额度校验**：免费体验（`trial_sentences` 默认 500 句，`system_config` 可配）；付费包（包月 X 句）；增量包（购买 X 句追加 `sentence_balance`）；超限返回「额度耗尽」提示
- 接口：
  - `GET/POST /api/admin/packages`（超管 CRUD + 启停）
  - `GET /api/plans`（公开定价页）
  - `GET /api/me/package`（当前包与剩余句数）
  - `POST /api/package/subscribe`（订阅/兑换包）
  - `GET /api/register/industries`（行业列表，来自 `pack_type=industry` 的行业包）
- 注册 `handleRegister`：无邀请码新建租户时 **industry 必填** → 自动创建/关联对应行业包 + 发放免费体验句

### 前端
- 登录注册表单：无邀请码时显示行业下拉（数据来自 `/api/register/industries`）
- 定价页 `/pricing`：展示免费体验/付费包/增量包（数据来自 `/api/plans`）
- 超管新增「商业包管理」面板：包 CRUD、句数、价格、启停
- `admin/Billing.vue` 显示当前包 + 剩余句数

### DB
- 新建 `packages` 表；`tenants.permissions` 扩展（幂等迁移）

### 验证
- 新租户注册选行业 → 拿到行业包 + 体验句；免费句用完被拦；超管建付费包/增量包 → 订阅 → 句数增加

---

## 七、阶段四：分级用量看板（Req 4）

### 后端
- `GET /api/billing/usage/me`（普通用户个人：按 `user_id` 过滤 ledger + 汇总）
- `GET /api/billing/usage/org`（租户管理员：`usage_ledger JOIN users.org_id`，支持 `org_id` 下钻出该组织用户明细，组织→子组织→用户）
- `GET /api/billing/usage/cost`（超管：全平台 `provider`/`model` 维度 SUM(cost) + token/句数聚合，模型成本核算）
- `store/billing.go` 新增查询：`UsageByUser` / `UsageByOrg` / `CostByModel`

### 前端
- `admin/Usage.vue` 改造：按角色显示三个层级
  - 超管：模型成本卡片（provider/model/token/成本）
  - 租户管理员：组织树 → 子组织 → 用户下钻
  - 普通用户（工作台）：个人消耗小卡片（本月句数/剩余/余额）

### 验证
- 三种角色登录看到对应粒度看板；超管成本与各模型 route 用量一致

---

## 八、阶段五：KB 后台化 + 角色权限（Req 6 + Req 7）

### 后端
- 新增 `PackDepartment`（部门包）常量（`store/kbpackages.go`）；`EnsureDefaultPackages` 增加部门包
- `api/kb.go`
  - `recognize-kb` / `import-kb` 加 `requireTenantAdmin`
  - `import-kb` 增加 `package_id` 参数，写入指定包（`SaveEntry`，按包隔离）而非平台 KB
  - 包类型权限校验：`tenant_admin` 仅可操作 `tenant`(企业)/`department`(部门)；`super_admin` 可操作 `industry`/`tenant`/`locale`/`department`。create/delete/import 均校验
- Req7：`handleAdminUserCreate` 已满足（租户管理员建 user/tenant_admin；仅超管建 super_admin），补充测试与前端交互完善

### 前端
- 删除 `ChatWindow.vue` 的 KB 上传弹窗（`showKbModal`/`kbFile`/`kbStep`/`startImport` 等）与相关样式
- `admin/Kb.vue` 重构：文件上传区（识别 → 预览 → 导入到所选包）+ 分类创建/删除；包类型按角色过滤显示
- `admin/Org.vue` / `admin/Users.vue` 完善账号开通交互（租户管理员开通账号/新租户管理员；仅超管建超管）

### 验证
- 普通用户不再见 KB 上传；租户管理员仅能建企业/部门包；超管可建行业/文化/部门包；导入数据进入指定包

---

## 九、阶段六：支付改造（Req 5）

### 后端
- `system_config pay_mode` 扩展为 `sdk` / `static_qr` / `mock`；新增 `static_qr_image`（图片 URL 或 base64）与可选 `static_qr_amount`
- SDK 模式：走现有 `wechat`/`alipay` 适配器（`payment/payment.go` 已有协议骨架，配置商户号后启用）
- 静态码模式：`pay create` 返回配置的静态二维码图片；订单 `pay_method=manual`、状态 `pending`（待人工确认）
- 新增 `POST /api/pay/manual-confirm`：用户扫码后点「我已付费」→ 标记订单待人工确认 + 写 `system_alerts` 告警 + 邮件通知超管（复用 `watchdog.go` 的 `notifyAlert` / `alert_email` 机制）
- 超管在 Billing 面板确认到账（复用 `handleOrderPay`）→ 发放句包/token

### 前端
- `admin/Billing.vue` 收银台：按 `pay_mode` 切换形态——SDK 码 / 静态二维码；静态码模式加「我已付费」按钮 → 提交后提示「已通知管理员审核开通」
- 超管 Billing 面板：待人工确认订单列表（`manual` 类型）快速确认

### 验证
- 切 `pay_mode=static_qr` + 配置二维码图 → 下单显示静态码 → 点「我已付费」 → 超管收到告警+邮件 → 确认后句数/余额到账

---

## 十、交付节奏

每阶段：
1. 后端（幂等迁移 + 接口 + 单测）
2. 前端（组件 + i18n 词条）
3. 验证：`go build / go vet / go test` + `gofmt -l` + `npm run build` + 端到端冒烟
4. commit（中文信息）+ push origin main
5. 更新 PROGRESS.md / 部署指南.md（提交历史 + 配置项 + 排查项）
6. 部署生产（跨编译 Linux 二进制 → 备份 → 替换 → systemd 重启 → 全链路验证）

## 十一、涉及文件清单（实施参考）

| 层 | 文件 |
|----|------|
| 后端 | `internal/engine/engine.go`、`internal/evals/evals.go`、`internal/orchestrator/workflow.go`、`internal/tenant/tenant.go`、`internal/config/config.go`、`internal/billing/quota.go`、`internal/store/billing.go`、`internal/store/kbpackages.go`、`internal/store/store.go`、`internal/store/systemconfig.go`、`internal/api/{admin_models,kb,register,stream,pay,admin_billing,billing_api,admin}.go`、`internal/payment/payment.go`、`internal/mail/*`、`internal/watchdog.go` |
| 前端 | `src/i18n.ts`（拆分为多文件）、`src/components/ChatWindow.vue`、`src/components/Login.vue`、`src/components/AdminDashboard.vue`、`src/components/admin/{Models,Usage,Billing,Kb,Org,Users,Workflow,Overview,Alerts}.vue`、`src/api/{models,kb,billing,tenant,auth}.ts` |
| 文档 | `PROGRESS.md`、`部署指南.md`、本文件 `SAAS_ROADMAP.md` |