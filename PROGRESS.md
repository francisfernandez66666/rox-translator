# 改造进度跟踪（PROGRESS）

> 更新：2026-08-20 ｜ 状态：**全部完成并上线生产** ｜ 关联文档：PLAN.md / SAAS_GAP.md / COMMERCIAL_TODO.md

## 当前状态总览

- **阶段**：P0 MVP + SaaS 基础层全部完成，生产已上线（`https://translator.quant-trading.top`）
- **代码**：`backend-go/`（Go 单二进制）+ `frontend/`（Vue3 + TS），已全部加中文注释
- **数据库**：SQLite（单文件 WAL，19 张业务表 + 幂等迁移）
- **部署**：阿里云 43.108.86.140 `/opt/translator/`，systemd `translator.service`，Caddy 反代
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
- [x] 阶段四·邮件通知：余额耗尽/模型熔断 critical 告警 → `alert_email` 收件人（Noop/SMTP）
- [x] 阶段五·全量回归：后端 4 包单测 + 前端 build + 21 项端到端冒烟（公开页/注册/登录/SSE/计费/Webhook/鉴权/优雅停机）全通过

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

## 待办（剩余，见 COMMERCIAL_TODO.md）

- [x] 二期在线支付（支付宝/微信）—— 已接入适配器骨架 + mock 完整流程；真实商户号接入后启用
- [x] 翻译完成 webhook 回调（客户 TMS/CI 集成）—— 已上线，支持 HMAC 签名校验
- [x] 优雅停机（signal.Notify + server.Shutdown）—— 已上线
- [x] i18n 界面中英切换 —— 已上线（登录/后台导航/工作台顶栏；翻译工作台内部文案次轮接入）
- [x] 商业物料（LICENSE/SLA/定价卡/DPA）—— 已上线（/docs/terms、sla、privacy + /pricing）
- [ ] 生产管理员/超管密钥轮换与审计 —— 需在部署时设置随机 JWT_SECRET/ADMIN_TOKEN 并轮换

## 问题与风险记录

| 日期 | 问题/风险 | 状态 | 应对 |
|------|-----------|------|------|
| 2026-08-15 | 服务器仅 1.6G 内存 | 已关闭 | 弃 PG 改 SQLite 单文件 |
| 2026-08-19 | rate_card 迁移 DDL 引号缺失 | 已修复 | `DEFAULT '*` → `DEFAULT '*'`，迁移单测通过 |
| 2026-08-19 | 本地错误率=1（假路由残留） | 已修复 | 清空测试库 model_routes，重测为 0 |
| 2026-08-19 | 登录审计归错租户（超管 tenant 0） | 已修复 | 改用 JWT 用户 tenant_id |
| 2026-08-19 | user_update 审计归错租户 | 已修复 | 改用有效租户 tid + before/after 轨迹 |