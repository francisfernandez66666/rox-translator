# 能言 SaaS 全量代码与架构审计报告

> 日期：2026-09-04
> 范围：backend-go / frontend-react / sdk / extension / office / deploy / scripts / archive / 顶层文档，端到端、全量、像素级、全场景全流程分支
> 基线：本报告在 README / PROGRESS / ARCHITECTURE_REVIEW_REPORT / MODULE_GAP_ANALYSIS / GREPPABLE_TECH_DEBT_CHECKLIST / 计费链路审计与修复方案 / 部署指南 之上做**增量确认与差异修正**，不重复旧结论。

---

## 一、审计范围与方法

### 1.1 已精读（主进程直读，未依赖子代理）

| 端 | 已读内容 |
|----|----------|
| 后端 | `cmd/server/main.go`、`internal/api/`（server.go/auth.go/billing_api.go/pay.go/upload.go/openapi_v1.go/sso.go/tickets.go/watchdog.go/feedback.go/editor.go/admin.go 等全部 handler）、`internal/store/`（表结构/迁移/quota_grants/apikeys/billing/webhooks/crypto 等）、`internal/billing/`（sink/quota/migrate）、`internal/engine/engine.go`（2082 行）、`internal/llm/client.go`、`internal/gate/gate.go`、`internal/culture/culture.go`、`internal/queue/direct.go`、`internal/service/ticket.go`、`internal/orchestrator/workflow.go`、`internal/kb/`、`internal/crawler/crawler.go`、`internal/fileproc/`、`internal/db/rewrite.go`、`internal/config/config.go`、`internal/mail/mail.go`、`internal/infra/`（redis/distlock/ratelimit/concurrency） |
| 前端 | `src/App.tsx`、`src/stores/`、`src/hooks/useChat.tsx`、`src/api/`（core/index/tickets/kb/translate 等 18 模块）、`src/components/`（ChatWindow/TicketsPage/EditorPage/Login/selfservice/MessageBubble 等）、`src/components/admin/`（AdminDashboard/TaskCenterP/panels_a-d 等）、`src/i18n/`（index + dicts.zh/en + 20 个 panels）、`src/lib/`、`src/types/` |
| SDK | `sdk/python/translator_sdk.py`、`sdk/typescript/src/index.ts`、`sdk/js/translator-sdk.mjs`、`sdk/java/TranslatorClient.java`（239 行完整） |
| 扩展/办公 | `extension/`（manifest/content/popup/SDKName/SDKHost）、`office/` |
| 部署 | `deploy/systemd/`、`deploy/caddy/`、`deploy/observability/`（grafana/prometheus/alertmanager/promtail）、`deploy/multi-az/README.md`、`deploy/cloud/`、`deploy/deploy_check.sh`、`deploy/restore_drill.sh`、`deploy/smoke_kb_scope.sh`、`deploy/loadtest/`、`scripts/`（bootstrap-demo/bootstrap-seoul/cutover-to-pg） |
| 文档 | README / PROGRESS / 部署指南 / 计费链路审计与修复方案 / 权限关系 / archive/*（未完成项目/待解决问题） |

### 1.2 交叉核对结论

- 路由清单（`grep 'HandleFunc('` 全量）与 handler 一一对应，未见死代码。
- 表结构清单（`store.go` 全部建表/迁移/索引）与前端契约字段对齐。
- i18n 键不对称已实测（见 §2.P2）。数据见下：

```
dicts.zh.ts keys: 553    dicts.en.ts keys: 551
zh-only: login.roleAdmin / pwd.emailBound / pwd.emailBoundEn
en-only: tk.fileReady
```

- 前端 `api/index.ts` 未 re-export `tasks/scrape/branding` 三模块，但组件经 `@/api/tasks`、`@/api/scrape`、`@/api/branding` 路径直连——功能可用，风格不一致（技术债非 bug）。

---

## 二、系统还缺什么（结论）

> 标记：🔴 P0 必做（资损/合规/会挂）｜🟠 P1 应做（规模化/企业级销售/运维效率）｜🟡 P2 可做（体验/长期演进）
> 每项标注【代码缺口】还是【资源/配置缺口】。

### 2.1 🔴 P0

| # | 缺口 | 证据 | 类型 |
|---|------|------|------|
| 1 | **多实例计费一致性无锁**：`billing.quotaByTenant`、QPS/并发/日额窗口为进程内存态；multi-az 文档宣称"跨实例合计不超过 LLM_MAX_CONCURRENT"，但 Redis 仅做 SETNX 令牌桶，双桶扣减（`DeductWithGrants`）走 DB 无分布式原子 | `internal/billing/quota.go`、`deploy/multi-az/README.md:19-20`、`internal/store/quota_grants.go` | 代码缺口 |
| 2 | **队列双跑竞态**：`Reserve`=UPDATE 后按 `leased_by` SELECT；`RecoverStale` 在任务处理>租约 30m 时可能把 running 重置为 queued → 双跑。单机靠 SQLite IMMEDIATE 锁缓解，PG 多实例未验证。**缓解已落地（2026-09-04）**：`Queue.Heartbeat` 60s 租约续期 + `RecoverStale` 两步回收（先清持有者再回队，防跨实例双跑） | `internal/queue/direct.go`、`internal/service/ticket.go` | 代码缺口（已加固，PG 多实例仍需分布式验证） |
| 3 | **对照编辑器缺失**：现仅 ChatWindow(TicketsPage 轮询式，无源/译并排、段落锚点、术语高亮、批注驳回流 | `frontend-react/src/components/ChatWindow.tsx`、`TicketsPage.tsx` | 代码缺口 |
| 4 | **生产单机无 HA**：PG 同机自建非 RDS、Redis 单节点自研客户端、1G 内存无扩容空间。~~`watchdog_selfcheck_restart=0` 仍未开启~~（**已于 2026-09-04 改为默认开启**，显式 "0" 才关） | 部署指南 §十、`archive/待解决问题.md:9-13`、`deploy/multi-az/README.md:35` | 资源/配置缺口 |
| 5 | **文档过时误导排期**：MODULE_GAP_ANALYSIS 仍把「SQLite HA」列为 P0，但生产已切 PG | 部署指南 §十 | 文档缺口 |

### 2.2 🟠 P1

#### 计费与商业化
- 订阅**自动续费**、升降级差价结算、**发票自动开具/红冲**全缺；真实支付渠道需**商户号**（archive 未完成项目挂账）
- **配额预留（预授权/占用）缺失**：`UsageSink` 内存累积 + 2s/200 条批量落库，中间态无占用 | `internal/billing/sink.go`
- 跨租户汇总账单、分账、供应商对账、成本/利润分析缺失 | `internal/api/billing.go`
- 异常用量阈值/趋势自动告警缺失（现有告警仅余额/熔断/错误率） | `internal/api/watchdog.go`

#### 开放 API 与集成
- **Webhook 无重试/死信/签名 SDK**：`dispatchCompletedWebhook` 一次性投递 | `internal/service/ticket.go:379`
- ~~错误码体系缺失~~：审计时字符串错误散落、无统一 Code/HTTP 映射（GREPPABLE §2）；**已于 2026-09-04 修复**──`internal/errors/codes.go` 新增 OpenAPI snake_case 常量，`api_openapi_tasks.go`/`admin_openapi.go` 全部字面量收敛，前端 `api/core.ts` 透传 code/error_code（提交 309a126）
- SDK 发布流水线 / OpenAPI Spec 自动生成 CI 缺（Java/Python/TS 已就绪但无仓库发布）
- API Key 无细粒度作用域（仅翻译/仅KB/仅计费） | `internal/api/apikeys.go`

#### 文件管线
- 大文件仍前置拒绝（>15MB/>120 页），无分片并行、断点续译、增量译修改段；子进程是 OOM 受害者（`FILEPROC_MAX_CONCURRENT=1`）
- PDF 扫描件 OCR 已移除但客户有需求；表格跨页保真、字体子集化缺 | `internal/fileproc/pdf.go`

#### KB / 引擎
- 向量检索已迁 pgvector，但**增量索引 / HNSW / 混合检索缺**；TM 审核无自动化预筛与 SLA 升级
- 术语**强制注入**（constrained decoding / logit bias）缺，仅 prompt 提示；术语不合规不落库不交付未闭环 | `internal/engine/engine.go:1035-1065`
- 成本/延迟感知路由缺（现仅权重选主 + 顺序降级） | `internal/engine/engine.go:926-995`
- 无自动回归基线（Golden Set + CI 门禁）、评估模型版本未锁定 | `internal/evals/`

#### 运维
- 部署仍手工 scp + systemd，**无 IaC / 蓝绿 / Canary / DB 迁移版本化回滚**（启动幂等迁移不可回滚）
- **异地备份未配**：`backup_remote_cmd` 默认空（有槽位无实配）；RPO/RTO 未量化
- 密钥轮换自动化 / KMS 缺（仅明文 0600 EnvironmentFile）

#### 邮件
- ~~SMTP 默认 Noop~~：审计时生产 `MAIL_ENABLED` 未启（`internal/mail/mail.go:63`）；**已于 2026-09-04 复核确认为已实配生效**（mail.conf/主 unit 含完整 SMTP 配置，日志可见真实发信成功 from=noreply@lexicorn.cn）。剩余触达待办：`email_notify_enabled`/`alert_email` 等开关仍为 0

#### 前端体验
- Service Worker 离线缓存、IndexedDB 草稿、乐观 UI + 冲突合并、暗黑模式、WCAG AA 全缺

### 2.3 🟡 P2

- 多级分销 / 邀请海报 / 漏斗分析；OAuth2/OIDC/JWT 互信；细粒度 RBAC + 权限审计回放；白标自定义域名 + CNAME 验证；SCIM/AD/LDAP 组织同步
- Chrome/Firefox/Edge 商店上架包（extension 已就绪未上架）、VS Code/Cursor/Trados/memoQ/QT 插件
- ICU MessageFormat、语言包热更新；InDesign/CAD/DWG/EPUB/Kindle 格式
- Grafana 仪表板模板加载验证、SLO 定义、分布式追踪、日志聚合落地
- 流式批量结果推送、文件级/语言级/段落级进度钻取、预估剩余时间

---

## 三、架构设计评价

### 3.1 亮点（真实成立）

1. **双层扣费 + 影子余额**设计正确：内存即时中止 + 批量落库；`DeductWithGrants` 双桶（近到期台账先扣）原子事务；Markup 口径统一（R-M1 已修） | `internal/billing/quota.go`、`internal/store/quota_grants.go`、`billing/migrate.go`
2. **方言抽象让 SQLite→PG 平滑切流**：SQLite DDL 为唯一真源，`internal/db/rewrite.go` 运行时改写；一次性迁移工具 + `cutover-to-pg.sh` 全程带备份与回滚路径，实测行数一致
3. **安全基线扎实**：CORS deny-by-default + /openapi 反射、pprof 回环绑定、/metrics Bearer 鉴权（METRICS_TOKEN）、支付回调 X-Admin-Token 注入（Caddy 内联头）、API Key AES-GCM 加密存储与轮换、systemd User 沙箱 + EnvironmentFile 0600、TRUST_PROXY_XFF 防伪造头、登录/注册 IP 限流落库 + 内存兜底
4. **自愈体系完备**：Breaker 熔断冷却、模型降级链、gate 8 项硬校验 + `gate_retry_max=8` 防死循环、文件管线 `FILE_HARDGATE_MAX_SEC` 墙钟兜底、watchdog 卡死巡检（含 running 租约不重排、工单心跳、metrics 读锁竞态已修）
5. **单实例架构红线被显式文档化**（部署指南 §八-D 逐项列出进程内存态组件），边界诚实清晰
6. **可观测性模板齐全**：prometheus/grafana/alertmanager/promtail 全套；/metrics 收敛内网
7. **契约一致性强**：三语言 SDK 端点/错误码/异步模型统一；i18n base+panels 结构化、useSyncExternalStore 零框架耦合；`selfservice.tsx` 已修普通用户访问余额 403（改用 /api/me/package）
8. **GDPR 擦除完整**：12 表 + 工单磁盘产物清理 | `EraseTenantDataFull`

### 3.2 劣势与风险（重点）

1. **「无状态」是多实例的唯一假设，但现实是半状态**：限流、验证码、重置码、采集调度、影子余额全是进程级。multi-az README 蓝图完整，但 Redis 客户端仍是自研**单节点**，哨兵/集群需重写；且生产 1G 内存根本跑不起多实例。**文档领先、代码半就绪、硬件不支撑**——最大架构风险。
2. **队列正确性依赖单机 SQLite 锁**，切 PG 后精确到分布式的租约竞争仍需验证（**已补救**：2026-09-04 加 Heartbeat 续租 + RecoverStale 两步回收，单实例/未来多实例的误回收窗口大幅收敛）。
3. ~~扩展是安全弱点~~：审计时 host_permissions http/https 全站、API Key 明文存 `chrome.storage.sync`、content.js 无 XSS 清理即注入气泡。**已于 2026-09-04 修复**：optional_host_permissions + activeTab/storage、storage.local + 密钥掩码、textContent 消毒（提交 309a126）；仍待办：应用商店上架。
4. ~~错误码非结构化~~：审计时前端 `MessagePlugin.error(e.message)` 直接抛中文文案。**已于 2026-09-04 修复**：统一 OpenAPI 错误码常量 + 前端 `ApiError`/`bizErrorCode` 透传（提交 309a126）。
5. **低配单机是产品天花板**：worker=4、chat 并发 2、内存 850MiB——所有"规模化"能力被硬件锁死，但商业卖点恰恰是企业级。

---

## 四、历史文档修正建议

| 文档 | 过时结论 | 建议 |
|------|---------|------|
| MODULE_GAP_ANALYSIS §8 | 「SQLite HA」列为 P0 | 已切 PG，改为「PG 单机无 HA / Redis 单节点」为 P0 |
| MODULE_GAP_ANALYSIS §6 | 「缺 TypeScript/Java SDK」 | 已实现（`sdk/typescript`、`sdk/java/TranslatorClient.java`），改为缺发布流水线 |
| 部署指南 §六 | 明文密钥 systemd 示例 | 文档已自注废弃，建议整节删除防误抄 |
| PROGRESS.md | 以"完成"为主线 | 建议补充"未完成/挂账"独立章节（与 archive 合并） |

---

## 五、建议下一步（按收益/成本排序）

> 状态标记：~~已修复~~ / 仍待办。

1. ~~上真邮件~~：**已完成**（2026-09-04 复核 mail.conf/主 unit 已实配，日志真实发信成功）；`watchdog_selfcheck_restart` 亦已改**默认开启**（两沉默风险均已消除）
2. **补生产 `backup_remote_cmd` 异地推送 + 每月实测 `restore_drill.sh`**（数据安全底线）——⏳ 仍待办
3. ~~修扩展安全三件套~~：**已完成**（host_permissions 收敛为 optional、API Key 改 storage.local + 掩码、content.js 改 textContent 消毒，扩展代码已提交 309a126）；上架商店包仍待办
4. ~~统一错误码枚举 + OpenAPI 错误 Schema~~：**已完成**（`internal/errors/codes.go` 常量 + openapi.v1.json 重写含 Error schema + 前端透传，提交 309a126）
5. **多实例若要投产**：先给 Redis 换哨兵客户端、给 `quotaByTenant` 加分布式扣减、给 jobs 加租约心跳续期——否则保持单实例并收敛"多 AZ"对外承诺。⏳ 其中**队列租约心跳续期已落地**（`Queue.Heartbeat` + worker 60s 续租），Redis 哨兵/分布式扣减仍待办

---

## 六、统计汇总

| 优先级 | 缺口数 | 核心分布 |
|--------|--------|----------|
| P0 | 5 | HA/切流一致性 2、对照编辑器 1、单机资源 1、文档过时 1 |
| P1 | ~22 | 计费 5、OpenAPI 5、文件管线 3、KB/引擎 4、运维 3、邮件 1、前端 4 |
| P2 | 10+ | 商业化、插件生态、前端体验、可观测 |

> 注：以上为历史文档之外的增量；累计总账以 MODULE_GAP_ANALYSIS 统计（P0 3/P1 28/P2 21）合并本报告修正后为准。