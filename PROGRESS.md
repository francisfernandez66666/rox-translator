# 改造进度跟踪（PROGRESS）

> 更新：2026-08-22 ｜ 状态：**一期重构 + 二期（工单队列/Gate/OpenAPI/通知中心）全部落地并上线** ｜ 关联文档：重构方案.md / 重构方案二期.md ｜ 关联文档：PLAN.md / 重构方案.md / SAAS_GAP.md / COMMERCIAL_TODO.md

## 当前状态总览

- **阶段**：P0 MVP + SaaS 基础层全部完成，生产已上线（`https://translator.quant-trading.top`）；SaaS 化 6 阶段路线图**全部完成**：阶段一（语言互译 + 全量 i18n）、阶段二（模型分阶段 + 超管统一维护）、阶段三（商业包 + 注册行业）、阶段四（分级用量看板）、阶段五（KB 后台化 + 组织架构）、阶段六（静态码/SDK 支付 + 人工确认）
- **规划中**：真实支付商户号接入（微信/支付宝 SDK 需商户资质）；商业包定价策略
- **代码**：`backend-go/`（Go 单二进制）+ `frontend/`（Vue3 + TS），已全部加中文注释
- **数据库**：SQLite（单文件 WAL，20 张业务表 + 幂等迁移）
- **部署**：阿里云 43.108.86.140 `/opt/translator/`，systemd `translator.service`，Caddy 反代
- **重构落地**（详见 `重构方案.md`）：四级角色体系 + IAM 子系统拆分（`internal/iam/`）+ 角色化工作台；知识库包优先级链（部门>组织>行业>语言文化，tm_segments.pack_id）；向量索引 BAAI/bge-m3 全量重建+增量入库；行业包单轨制（tenants.industry，堵组织包跨租户泄漏）；审计日志分级；句数计费门控；平台上下文语义（tid≤0 聚合视图/平台直翻无知识库）
- **Git**：`github.com:francisfernandez66666/rox-translator.git`（main 分支）

## Git 提交历史（里程碑）

| Commit | 内容 |
|--------|------|
| `a7fbdf2` | 初始版本：翻译助手 v2 — ROX 车机多语翻译知识库 |
| `558f92f` | 添加 .gitignore |
| `7a47719` | 全量中文注释 + App Translocation 修复 + 免安装构建 |
| `6a12015` | 重构为 SaaS 多租户平台：三级账户体系 + 租户级配置 |
| `22904a0` | 修复文件翻译：更多语言解析 + docx 带属性段落写回 + PPT 自动缩放 |
| `b163f22` | 补齐 SaaS 销售能力：多供应商成本核算+路由、Excel批量导入KB、审计前后值轨迹、开放API扩展+Key轮换、告警监控、租户GDPR导出/清除、前端UI全面接入 |
| `0c9cdce` | 数据看板可视化（趋势/任务类型/供应商图表）+ 系统级 /metrics Prometheus 指标导出 |
| `526573d` | 前后端源码补齐全量中文注释（Go 60 文件 + 前端 10 文件），gofmt 归一化 |
| `8adfe5a` | 全量更新项目文档：PLAN（已完成态）/ PROGRESS / SAAS_GAP / COMMERCIAL_TODO / CLOUD_VALUE / 部署指南 |
| `18c59d1` | 新增 REFACTOR_PLAN：后台结构调整 + 商业化补齐 6 阶段执行计划 |
| `0666433` | 后端重构：admin.go 按域拆分 7 文件，server.go 路由按域分组（纯重构） |
| `a7bccaa` | 前端后台重构：AdminDashboard 拆为壳+12 面板，api/index.ts 按域拆分 13 文件（纯重构） |
| `ce2961d` | 组织层级+租户改名+P0 安全+串号修复：orgs 树表+用户 org_id、组织 CRUD/下钻 API、前端「租户」→「组织」+组织结构面板+切换器下钻、注册返回完整租户信息、登录暴力破解限流、CORS 白名单+请求体限制、登出重置聊天 store |
| `a596df4` | 文档：PROGRESS 更新提交历史与关键决策（组织层级/暴力破解限流已上线，移除已完成待办） |
| `6555d5b` | 注释补全：ratelimit_test.go 文件头职责说明、Users.vue 账户管理各函数中文注释 |
| `60ea8c9` | 阶段一：在线支付（pay 三渠道适配器+下单/状态/模拟/通知 API+Billing 收银台）+ 找回密码（邮件验证码）+ 密钥安全加固（env 全覆盖+随机占位） |
| `73b3160` | 阶段一：组织管理「开通用户」：在选中组织下创建账号（支持组织归属/角色/重置密码/启停用） |
| `72d06ff` | 阶段二：上传安全加固：统一扩展名白名单+大小上限 |
| `7ca48dc` | 阶段二：数据库定时自动备份（VACUUM INTO 在线快照+旧备份清理） |
| `e6c611a` | 阶段二：优雅停机（signal.Notify+server.Shutdown）+ 结构化访问日志（每请求 JSON） |
| `4d66e8b` | 阶段二：前端健康检查超时（10s）+ 离线重试按钮 |
| `0943311` | 阶段二：OOM 内存监控（运行时采样+系统压力阈值告警+可疑进程 TOP5） |
| `a9f7492` | 阶段三：Webhook 回调（webhooks 表+管理 API+HMAC 签名+重试指数退避+翻译完成事件投递+UI 面板） |
| `dd8e597` | 阶段三：补充测试（支付网关+上传校验单测，修复 parseAmount/parseUpload 两处真实缺陷） |
| `81fad7e` | 阶段四：商业物料+定价页（/pricing + /docs/terms、sla、privacy + /api/pricing）+ i18n 中英（登录/后台导航/工作台顶栏） |
| `31058cc` | 阶段四：关键告警邮件通知（余额耗尽/模型熔断 → alert_email 收件人） |
| `48afb94` | 修复生产级竞态：metrics 计数映射并发读写崩溃（concurrent map writes），RWMutex + 并发压测验证 |
| `6c0ae58` | 全量补齐中文注释（前后端）：后端 5 文件 + 前端 22 文件（Vue/TS），0 缺注释函数/定义 |
| `f767444` | **SaaS 阶段一**：语言互译（Req10）+ 全量 i18n（Req9）——后端全链路 sourceLang 透传（translateInstruction 支持 en→zh、workflow KB 按实际源语言匹配）+ 前端 i18n 重构为 i18n/ 分面板字典（530+ 中英文案键，全量覆盖工作台+12 管理面板）+ ChatWindow 源语言选择器 |
| `dd6d76f` | **SaaS 阶段二**：模型分阶段配置（Req1）+ 超管统一维护模型（Req3）——`system_config.stage_models` 支持知识库/初翻/evals/审校各自独立模型；引擎 `resolveStageModel(ctx,stage)` 按阶段解析（未配置回退全局/路由）；evals 去硬编码 `Cfg.OnlineModel` 改 `resolveJudge`；租户模型配置收归超管（`handleModels/Save` 仅超管）；新增 `GET/POST /api/admin/models/stage`；前端 Models.vue 增加分阶段模型卡片 + i18n |
| `1f7ab0c` | **SaaS 阶段五·Req6（KB 后台化）**：前台 ChatWindow 移除 KB 导入弹窗；后台 Kb.vue 增加文件上传（识别→选包→导入）；`recognize-kb`/`import-kb` 加 `requireTenantAdmin` + `package_id` 按包写入（SaveEntry）；包类型权限按角色过滤（租管仅企业/部门包，超管全类型）；新增 `department` 部门包 + 单测 |
| `e2fbc72` | **组织架构**：根组织/组织/部门三级自定义名称 + 部门层级——`orgs` 表新增 `type` 字段（root/org/dept）迁移；根组织独立建行可重命名（EnsureRootOrg/GetRootOrg/ListOrgs 根优先）；CreateOrg 支持类型（组织=根组织下，部门=组织下）；根组织删除保护；前端 Org.vue 支持根组织重命名+组织/部门图标+按父级推断类型 + 单测 |
| `040e17c` | 翻译提示词跟随界面语言 + 组织拖拽层级管理——前端传 lang 给后端，translateInstruction 按界面语言生成中/英提示词；组织放开任意深度嵌套，新增 orgs/move 改父级接口（成环校验），Org.vue 支持拖拽调整层级+清晰显示组织/部门 |
| `ed87c91` | 部署：兼容 Caddyfile（独立 conf + import 行）——translator 站点配置独立于 /etc/caddy/translator.conf，主 Caddyfile 用 import 引入；不受 quant-trading-v2 部署脚本 mv 覆盖影响；部署指南记录兼容方案与日志权限注意事项 |

## 已完成 · 全部阶段 ✅

### 阶段 1-2：数据层 + Schema
- [x] SQLite 单文件（WAL + busy_timeout + 互斥），自动建表 + `migrateColumns` 幂等列迁移
- [x] 知识库 `tm.sqlite3`（3300+ 条）+ `tm_embeddings.npz`（1024 维向量，L2 归一化）加载验证
- [x] 19 张业务表：users/tenants/tickets/ticket_state/kb_packages/kb_entries/kb_safety_phrases/balance_accounts/usage_ledger/rate_card/orders/payments/invoices/api_keys/invite_codes/eval_records/audit_logs/system_config/alerts
- [x] `usage_ledger` 增加 provider/model 列（供应商成本核算维度）；`rate_card` 增加 provider 列

### 阶段 3-4：Go 数据层 + 平台层
- [x] `internal/store/`：全量 CRUD + 事务 + 迁移（billing/audit/alerts/gdpr/apikeys/invite/kbpackages/systemconfig/tickets/users/evals）
- [x] `internal/auth/`：JWT + RBAC 三级角色 + 登录/改密/自助注册
- [x] `internal/tenant/`：多租户隔离（行级 tenant_id）+ 租户 CRUD/状态/试用/邀请码 + GDPR 导出/清除
- [x] `internal/billing/`：计量（usage_ledger）+ 余额账本 + rate_card 单价 + 配额限流（QPS/并发/每日上限/余额停服）+ 订单/发票
- [x] `internal/openapi/`：开放 API + 租户 API Key 签发/轮换/状态 + `/openapi/docs` 文档
- [x] `internal/api/watchdog.go`：看门狗（余额阈值/模型熔断/错误率>40% → alerts 表，幂等告警）
- [x] `internal/api/metrics.go`：Prometheus `/metrics`（HTTP/翻译 ok-fail/用量 token/熔断/错误率/租户规模/Go 运行时）

### 阶段 5：业务层
- [x] `internal/engine/`：初翻 Agent（术语注入、缩句双稿）、审校、多供应商路由降级、熔断
- [x] `internal/kb/`：四层查找 + 三级包分层 + 向量检索 + Excel 批量导入
- [x] `internal/culture/`：语言文化习惯包输出闸门
- [x] `internal/gate/`：ConstraintGate 硬校验
- [x] `internal/evals/`：LLM-as-Judge 5 维评分 + 抽样率 + 重翻/校正

### 阶段 6：编排层
- [x] `internal/orchestrator/`：FlowDef 流程引擎 + workflow + 驳回意见自动重启循环 + 步骤启停（admin）
- [x] 兼容现有同步 API（`/api/chat`、`/api/translate`、`/api/translation/*`）

### 阶段 7：API + 前端
- [x] 全部 API 路由（见 PLAN.md 路由清单）：auth/tenant/billing/apikeys/tickets/approve/translate/kb/evals/system/admin/metrics/openapi
- [x] 前端：登录+自助注册、聊天翻译窗口、工单、审批台、KB 管理、evals 看板、API Key、租户/计费/配额/发票/邀请码/审计 CSV/告警面板、**用量看板图表**（趋势柱状/任务类型/供应商横向条形）
- [x] 前端路由守卫 + 角色判断 + 401 处理

### 阶段 8：evals
- [x] LLM-as-Judge 5 维加权评分（术语30/语法20/语义30/数字单位10/风格10）
- [x] 不合格自动重翻（≤2 次）→ 仍低送校正模型 → 再评估；无 Key 跳过；抽样率可配

### 阶段 9：部署 + 验证（生产已上线）
- [x] 前端构建 → 交叉编译 Linux 二进制（`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`）
- [x] systemd `translator.service`（`-addr 127.0.0.1:8787 -frontend /opt/translator/web -kb /opt/translator/data/tm_embeddings.npz -kbdb /opt/translator/data/tm.sqlite3`）
- [x] Caddy 反代 `translator.quant-trading.top`，SSL 已签发
- [x] 端到端验证通过：注册→登录→翻译→计量扣余额→用量报表→发票→审计导出→开放 API 全链路→API Key 轮换→旧 Key 失效→GDPR 导出/清除→看门狗告警→/metrics
- [x] 租户隔离验证：租户 A 无法访问租户 B 的数据
- [x] 生产健康验证：0 panic，错误率 0%，翻译/用量/metrics 正常

### 阶段 10：商业化补齐（REFACTOR_PLAN 六阶段，全部完成）
- [x] 阶段一·在线支付：`internal/payment/` 三渠道适配器（mock/wechat/alipay 骨架）+ `/api/pay/create|status|simulate|notify` + `Billing.vue` 收银台（QR 码/订单历史/对账）；`pay_mode` 配置切换
- [x] 阶段一·找回密码：`internal/mail/`（Sender 接口 + Noop/SMTP 实现）+ `/api/auth/forgot|reset` + 登录页两步面板；验证码 10 分钟一次性
- [x] 阶段一·密钥安全加固：config 去硬编码，`SILICONFLOW_API_KEY`/`ONLINE_API_KEY`/`ADMIN_TOKEN`/`ADMIN_INIT_PASSWORD`/`JWT_SECRET` env 全覆盖 + 随机占位 + 告警日志
- [x] 阶段一·组织开通用户：Org.vue 开通用户表单（org_id 归属/角色/初始密码/重置密码/启停用）
- [x] 阶段二·上传限制：`parseUpload` 扩展名白名单 + 50MB 上限（MaxBytesReader 硬性拦截）
- [x] 阶段二·自动备份：`Store.Backup`（SQLite `VACUUM INTO` 在线快照）+ `PruneBackups` 保留 N 份；启动即备份 + 每 24h（`backup_interval_hours`，0=关）
- [x] 阶段二·优雅停机+结构化日志：signal.Notify + `http.Server.Shutdown`（10s 超时）+ `withAccessLog` 每请求 JSON
- [x] 阶段二·健康检查：前端 10s 超时 + 离线重试按钮
- [x] 阶段二·OOM 监控：`startMemoryMonitor` 运行时采样（Heap/goroutine/峰值）+ `/proc/meminfo` 压力阈值告警 + TOP5 进程（`mem_monitor_interval_sec`/`mem_pressure_pct`）
- [x] 阶段三·Webhook：webhooks 表 + 管理 API + HMAC-SHA256 签名 + 异步投递重试 3 次指数退避 + 翻译完成事件（text/file 四路径）+ 前端面板
- [x] 阶段三·补充测试：payment 9 单测 + upload 5 单测；修复 parseAmount（元/分启发式）与 parseUpload（大文件未硬性拦截）两处真实缺陷
- [x] 阶段四·商业物料：/pricing 定价页 + /docs/terms、/docs/sla、/docs/privacy（中英双语内嵌页）+ /api/pricing 公开单价 API
- [x] 阶段四·i18n：`frontend/src/i18n.ts` 中英双字典（登录/后台导航/工作台顶栏）+ 语言切换按钮
- [x] SaaS 阶段一·语言互译：后端 sourceLang 自动检测/显式指定全链路透传，任意语言互译（en→zh/zh→fr/…）；`translateInstruction` 支持全部方向
- [x] SaaS 阶段一·全量 i18n：`i18n.ts` 重构为 `frontend/src/i18n/`（index.ts 合并 + panels/*.ts 分面板字典 + `tpl()` 插值）；覆盖登录/导航/工作台/MessageBubble/App + 12 个 admin 面板 + 共享工具函数，共 530+ 文案键（中英成对）；ChatWindow 新增源语言选择器
- [x] SaaS 阶段二·模型分阶段：`system_config.stage_models` JSON（kb_match/ai_initial/evals/review 各 {provider,api_base,api_key,model}）；引擎 `resolveStageModel(ctx,stage)` 按阶段解析、未配置回退 `resolveModel`、api_key 留空继承全局；`TranslateOne`(kb_match)/`TranslateLangsInto`/`TranslateOtherLang`/`TranslateWithFeedback`/批量兜底(ai_initial)/`ReviewTranslation`(review)/evals(evals) 全链路传阶段；阶段模型独立时不走路由降级链
- [x] SaaS 阶段二·超管统一维护：`handleModels`/`handleModelsSave` 改 `requireAdminUser`（收掉租户管理员模型配置权限，策略参数仍租管可配）；新增 `handleStageModels`/`handleStageModelsSave`（`GET/POST /api/admin/models/stage`，掩码密钥）；前端 Models.vue 增加「分阶段模型」卡片（超管可见）+ i18n 词条；单测 `TestResolveStageModel`/`TestResolveStageModelKeyInherit`
- [x] SaaS 阶段五·KB 后台化（Req6）：前台 ChatWindow 移除 KB 导入弹窗（模板/脚本/样式）；后台 Kb.vue 增加文件上传区（识别→预览→选包→导入）；`recognize-kb`/`import-kb` 加 `requireTenantAdmin` 且 `import-kb` 按 `package_id` 写入指定包（`SaveEntry`，租户隔离）；包类型权限按角色过滤（`canManagePackType`：租管仅 tenant/department，超管全类型）；新增 `department` 部门包常量 + `EnsureDefaultPackages`；单测 `TestCanManagePackType`/`TestEnsureDefaultPackagesIncludesDepartment`
- [x] 组织架构三级自定义名称：`orgs` 表新增 `type` 字段（root/org/dept，幂等迁移）；根组织独立建行（`EnsureRootOrg`/`GetRootOrg`/`ListOrgs` 根优先）可重命名；`CreateOrg` 支持类型（组织=根组织下，部门=组织下）；根组织删除保护；`handleOrgList` 返回 `root` 行；前端 Org.vue 支持根组织重命名 + 组织/部门图标 + 按父级推断类型；单测 `TestOrgHierarchy`
- [x] 阶段四·邮件通知：余额耗尽/模型熔断 critical 告警 → `alert_email` 收件人（Noop/SMTP）
- [x] 阶段五·全量回归：后端 4 包单测 + 前端 build + 21 项端到端冒烟（公开页/注册/登录/SSE/计费/Webhook/鉴权/优雅停机）全通过
- [x] SaaS 阶段三·商业包（Req2）+ 注册行业（Req8）：`packages` 表（free/paid/increment + 句数/价格/有效期/启停）；`tenants.permissions` 扩展 sentence_balance/package_code/subscribed_at；句数计量（源句×目标语言数）`meterSentences`/`meterFileSentences` + `sentence_enforced` 闸门；超管包 CRUD + 全局设置（sentence_enforced/trial_sentences/pay_mode/static_qr_image）；公开定价 `/api/plans` + `/api/me/package` + `/api/package/subscribe`（mock 自动到账 / 静态码人工确认）；注册行业必填 + 开通行业包（FindIndustryByCode/EnsureIndustryPackage）+ 试用句数；订单 `package_id` 列 + `MarkOrderPaid` 商业包发句数；前端注册行业下拉、定价页套餐卡、超管 Packages.vue 面板、Billing 订阅 + 个人句数卡（ChatWindow）；单测 TestPackageCRUD/TestSentenceBalance/TestIndustryPackage/TestPackageOrderManualConfirm
- [x] SaaS 阶段四·分级用量看板（Req4）：`/api/billing/usage/me`（个人累计/当日/剩余句数）、`/api/billing/usage/org`（组织→子组织→用户下钻）、`/api/billing/usage/cost`（超管 provider/model 成本核算）；store UsageByUser/UsageByOrg/CostByModel；前端 Usage.vue 按角色三档 + ChatWindow 顶栏个人剩余句数
- [x] SaaS 阶段六·支付改造（Req5）：`pay_mode` 扩展 sdk/static_qr/mock + `static_qr_image`；静态码订单 channel=manual + 收款码回填；`/api/pay/manual-confirm`（「我已付费」→ manual_confirm=1 + critical 告警 + 邮件通知超管）；`/api/admin/orders/manual` 待确认订单 + 超管确认发放；前端收银台按模式切换（图片/文本二维码 + 我已付费按钮）+ 超管待确认列表 + Packages.vue 支付模式配置

## 关键决策记录

| 日期 | 决策 | 说明 |
|------|------|------|
| 2026-08-15 | 完整 P0 MVP | 按方案书 22 人天范围 |
| 2026-08-15 | Go 后端为基础 | 单二进制，与现有部署衔接 |
| 2026-08-15 | 数据库 SQLite | 单文件 WAL，内置多租户，避免 1.6G 内存跑 PG 的压力 |
| 2026-08-15 | 沿用现有 LLM | 硅基流动/智谱 Key，多供应商路由可扩展 |
| 2026-08-15 | evals LLM-as-Judge | 5 维加权评分，可抽样 |
| 2026-08-15 | 三级角色权限体系 | user/approver/admin |
| 2026-08-15 | KB 包分层 | 企业→行业为匹配来源；语言文化习惯包为输出闸门 |
| 2026-08-15 | 驳回自动重启 | 意见拼入提示词自动重启初翻+校对 |
| 2026-08-17 | SaaS 化补充（SAAS_GAP.md） | P0/P1/P2 全做 |
| 2026-08-19 | 计费默认不强制 | `system_config billing_enforced=1` 才扣余额停服；未强制仅计量留痕 |
| 2026-08-19 | 计量口径 | 文本按源 rune 字符数、文件按段数、工单按源字符数；请求级 provider/model |
| 2026-08-19 | 告警看门狗 | 5 分钟周期，余额阈值/熔断/错误率>40% 幂等告警 |
| 2026-08-20 | /metrics 无鉴权 | 仅聚合指标，不涉租户数据 |
| 2026-08-20 | 组织=管理结构展示层 | 根组织=租户本身（parent_id=0 不建行），子组织/部门树展示与归集；数据隔离仍以租户为边界 |
| 2026-08-20 | 暴力破解限流 | 同一 IP 5 次失败/300s 窗口 → 冷却 300s（HTTP 429）；登录成功清零 |
| 2026-08-20 | 支付渠道 | `pay_mode` 配置（默认 mock）；`/api/pay/simulate` 仅 mock 可用；真实渠道需商户号 |
| 2026-08-20 | 备份策略 | `VACUUM INTO` 在线快照，启动即备 + 每 24h；保留最近 7 份（可配） |
| 2026-08-20 | 告警通知 | 余额耗尽/模型熔断 → 邮件（alert_email 收件人）+ 全部写 alerts 表 |
| 2026-08-20 | Webhook 安全 | HMAC-SHA256 签名（X-Signature 头）+ 异步投递重试 3 次指数退避 |
| 2026-08-21 | 阶段模型 | 各流程阶段（kb_match/ai_initial/evals/review）经 `stage_models` 独立配置；api_key 留空继承全局；未配置阶段回退全局/路由；租户模型配置收归超管 |
| 2026-08-21 | 四级角色体系 | user(1)<dept_admin(2)<tenant_admin(3)<super_admin(4)；部门管理员限本部门子树管成员与部门包；结构操作仅超管+租管 |
| 2026-08-21 | IAM 子系统 | 组织与账户拆分 internal/iam/（models/store/auth），旧包薄委托；代码内聚不物理分库 |
| 2026-08-21 | 知识库优先级链 | 部门包(0)>组织包(1)>行业包(2)>语言文化包(3)；tm_segments.pack_id 归属；共享宿主=租户1，行业按 tenants.industry 过滤 |
| 2026-08-21 | 向量模型 | BAAI/bge-m3（SiliconFlow）替代智谱 embedding-2；stage_models.kb_embed 配置；导入后自动增量重建 |
| 2026-08-21 | 平台上下文语义 | tid≤0：管理面板=跨租户聚合；前台翻译=平台直翻无知识库（超管不挂任何租户包） |
| 2026-08-21 | 审计分级 | 超管全量带租户名/操作者；租管工作台独立审计面板（本租户+过滤+CSV） |
| 2026-08-21 | Caddy 缓存策略 | index.html no-cache（部署即生效）；/assets/* immutable 一年 |
| 2026-08-21 | 队列选型 | direct 单实现（goroutine池+jobs表持久化），Kafka 仅文档登记触发条件不写死代码；Queue 接口留单文件接入位 |
| 2026-08-21 | 工单隐私 | 非超管一律只看自己创建的工单（涉及用户隐私，租管亦然） |
| 2026-08-21 | Gate 架构 | 安全词=知识库 Agent 的参考知识（注入上下文为主路径）；「闸门」专指译后机械硬过滤 |
| 2026-08-21 | 安全句来源 | LLM 挖掘批量投喂→pending→人工审核→approved 生效；结构化 kind/replacement/status/source 字段 |
| 2026-08-21 | PDF 工单降级 | 本期输出 xlsx 对照表；PDF 原格式排版回写列后续迭代 |

| `cad0e4d…cd2bd17` | **IAM/权限/知识库/计费六批次重构**（2026-08-21 晚，约 20 个提交）：IAM 子系统拆分 internal/iam/ + 四级角色（user<dept_admin<tenant_admin<super_admin）+ 部门管理员部门子树范围；前端角色化工作台（平台运营/企业管理/部门管理三视图）+ 开户「先部门后角色」级联；知识库包体系（pack_id 归属、启停、行业单轨制 tenants.industry、跨租户泄漏修复）；向量索引 BAAI/bge-m3 全量重建(3348行)+导入自动增量入库+ScopedSearch 包过滤；模型面板按业务五阶段重构（初翻/Embed/初翻Evals/校对/校对Evals，超管移除单模型冗余）；句数扣减受强制计费门控；审计日志分级（超管全量带租户名/租管本租户独立面板）；平台视角跨租户聚合（订单/APIKey/Webhook/用量）；删除部门自动降级其管理员；前台身份栏（账号·组织·部门·包类型）+ 超管平台直翻隔离（修复误命中租户KB）；Caddy 缓存策略（HTML no-cache/assets immutable）；构建脚本 iCloud 目录签名修复
| `42eacbb` | **二期重构**（2026-08-21 深夜）：三层解耦（API/编排器/存储）+ internal/queue 接缝（direct 执行器=goroutine池+jobs账本+租约超时回收+启动自愈，Kafka 列迭代补充）；工单异步化（入队即返 ticket_no，隐私隔离非超管仅自己）+ 下载 docx/xlsx/pptx 原格式回写、文本/PDF 降级 xlsx；Gate 两层闸门（approved 安全句按目标语言注入 agent 上下文 + L2 硬过滤默认关 locale_gate_enforced）+ LLM 批量投喂审核流；OpenAPI 句数闭环（sentence_exhausted 错误码+余额回传）；通用通知中心（notifications+铃铛+已读流转）；批次零修复 /metrics 路由与 Evals 占位 Key 空转 |
| `90c1de2…4fcd024` | 工单升级为前台独立 Tab 全屏页：头部双 Tab（即时翻译/翻译工单，URL /tickets 同步）；大文本域+文件上传建单（docx/xlsx/pptx/pdf/txt/csv ≤10MB 自动入队）；LangMultiSelect 多选语言选择器组件化；安全句结构化管理界面（录入/审核/LLM 导入）；英文术语统一 Translation Job；i18n 缺失 key 补齐与引导示例中文化 |

## 待办（剩余，见 COMMERCIAL_TODO.md）

- [x] 二期在线支付（支付宝/微信）—— 已接入适配器骨架 + mock 完整流程 + 静态码人工确认；真实商户号接入后启用 SDK
- [x] 翻译完成 webhook 回调（客户 TMS/CI 集成）—— 已上线，支持 HMAC 签名校验
- [x] 优雅停机（signal.Notify + server.Shutdown）—— 已上线
- [x] i18n 界面中英切换 —— 已上线（登录/后台导航/工作台顶栏；翻译工作台内部文案次轮接入）→ **全量 i18n 已在 SaaS 阶段一完成（含工作台与全部管理面板）**
- [x] 商业物料（LICENSE/SLA/定价卡/DPA）—— 已上线（/docs/terms、sla、privacy + /pricing）
- [x] 商业包 + 静态码支付 —— 已上线（阶段三 + 阶段六）
- [ ] 真实支付商户号接入（微信/支付宝 SDK）
- [ ] 生产管理员/超管密钥轮换与审计 —— 需在部署时设置随机 JWT_SECRET/ADMIN_TOKEN 并轮换
- [x] 语言文化包 gate 闸门逻辑 —— 已上线（approved 安全句注入 agent 上下文 + L2 硬过滤 locale_gate_enforced 默认关）
- [x] Evals 占位 Key 空转处理 —— 已修复（占位标记+主路由 Key 水合+动态可用性判定）

## 问题与风险记录

| 日期 | 问题/风险 | 状态 | 应对 |
|------|-----------|------|------|
| 2026-08-15 | 服务器仅 1.6G 内存 | 已关闭 | 弃 PG 改 SQLite 单文件 |
| 2026-08-19 | rate_card 迁移 DDL 引号缺失 | 已修复 | `DEFAULT '*` → `DEFAULT '*'`，迁移单测通过 |
| 2026-08-19 | 本地错误率=1（假路由残留） | 已修复 | 清空测试库 model_routes，重测为 0 |
| 2026-08-19 | 登录审计归错租户（超管 tenant 0） | 已修复 | 改用 JWT 用户 tenant_id |
| 2026-08-19 | user_update 审计归错租户 | 已修复 | 改用有效租户 tid + before/after 轨迹 |
| 2026-08-21 | 超管前台误命中租户知识库 | 已修复 | engine.tenantID 兜底返回1所致；TranslateOne 直读 context，tid≤0 平台直翻 |
| 2026-08-21 | 组织包数据跨租户泄漏 | 已修复 | 共享检索 JOIN kb_packages 校验类型+注册行业，租户1仅行业/语言文化包可共享 |
| 2026-08-21 | Models/Org 面板白板崩溃 | 已修复 | v-for 变量名反转 + orgIcon 未定义；用户控制台报错直击根因 |
| 2026-08-21 | execW 自递归死锁 | 已修复 | 批量替换误改函数体调用自身；单测抓出并修复 |
| 2026-08-21 | 删租户残留孤儿组织 | 已修复 | 删除级联清理主/业务数据；平台树 INNER JOIN 防御 |
| 2026-08-21 | 部署后浏览器旧资源 | 已修复 | Caddy 增加 HTML no-cache / assets immutable 缓存头 |
| 2026-08-21 | /metrics 点击打开翻译页 | 已修复 | Caddy 未代理 /metrics 落入 SPA 兜底；新增 handle /metrics 反代 |
| 2026-08-21 | Evals 占位 Key 空转 | 已修复 | 占位标记+主路由 Key 启动水合+Evaluate 动态可用性判定 |
| 2026-08-21 | 批量替换引入 execW 自递归 | 已修复 | 函数体被误改调用自身致死锁，测试超时抓出 |