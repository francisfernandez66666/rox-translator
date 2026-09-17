# 能言 SaaS 系统模块级缺口清单（像素级、全场景、全分支）

> 基于全量代码阅读，逐模块、逐场景、逐分支对照「生产级 SaaS 标准」梳理缺口  
> 标记：✅ 已完备 / ⚠️ 部分实现 / ❌ 缺失 / 🔄 需重构

---

## 1. 翻译引擎

| 场景/分支 | 现状 | 缺口 | 优先级 | 备注 |
|-----------|------|------|--------|------|
| **多模型并发编排** | 单路并发信号量(3)，主模型失败按权重降序逐一降级 | ❌ 缺 **并发多路发射 + 竞速取首个成功**（hedged requests），降低 P99 延迟 | P1 | `engine.go:683-733` `translateLangsConcurrent` |
| **流式输出** | `handleChatStream` SSE 逐 token 推送 | ❌ 文件翻译/批量翻译**无流式进度**（只能轮询 ticket_state） | P1 | `api/stream.go` vs `service/ticket.go` 进度回调 |
| **术语强制注入** | KB 命中直接替译文，模型兜底仅作参考 | ❌ 缺 **术语表强制模式**（prompt-level constrained decoding / logit bias），术语不合规时不落库不交付 | P1 | `engine.go:1035-1065` `singleLang` system prompt 注入 |
| **语言检测** | `DetectSourceLang`（全角修复后） | ❌ 缺 **混合语言段落切分+逐段检测**（代码/公式/表格混排场景） | P2 | `engine.go:1560+` `DetectSourceLang` |
| **模型路由** | 权重选主 + 降级链 | ❌ 缺 **成本感知路由**（按 provider/model 实时单价动态选最优）、**延迟感知路由** | P1 | `engine.go:926-995` `resolveModel`/`pickPrimaryRoute` |
| **评估器** | LLM-as-Judge 抽样评估 | ❌ 缺 **自动化回归基线**（Golden Set + CI 门禁）、**评估模型版本锁定** | P2 | `evals/evals.go` `Evaluate`/`ShouldSample` |
| **硬闸** | 8 项 + 文化闸门 + L2 替换/拦截 | ❌ 缺 **自定义规则 DSL**（正则/词典/语法树）、**闸门规则版本管理与灰度发布** | P2 | `gate/gate.go` `Run`、`culture/culture.go` `Run` |
| **批量翻译** | `BatchTranslate` <sN> 标记解析、动态批大小、统一网关接入 | ⚠️ 缺 **流式批量结果推送**、**部分失败重试单段**、**成本预估回调** | P1 | `engine.go:1320-1500` `BatchTranslate` |
| **截断自修复** | `isTranslationIncomplete` + `autoCompleteTranslation`（续翻+全量重翻） | ✅ 逻辑完整 | - | `engine.go:1210-1265` |
| **熔断冷却** | `Breaker` 连续失败阈值+冷却自动恢复 | ✅ 逻辑完整 | - | `engine.go:240-309` |
| **上下文隔离** | `WithUsageRecorder`/`WithUILang`/`WithUserOrg` context 传递 | ✅ 设计正确，无单例字段串台 | - | `engine.go:130-140` |

---

## 2. 知识库 (KB/TM)

| 场景/分支 | 现状 | 缺口 | 优先级 | 备注 |
|-----------|------|------|--------|------|
| **向量检索** | NPZ 内存索引（1024 维 float32），启动加载、重建全量 | ❌ 缺 **增量索引**（新增/删除条目不重建全量）、**HNSW/IVF 量化**、**混合检索（稀疏+稠密）** | P0 | `kb/npz.go` `LoadNPZ`/`RebuildKBIndex` |
| **组织继承链** | 部门包就近覆盖、跨部门降级开关 | ❌ 缺 **包级权限矩阵**（读/写/管理）、**包继承可视化拓扑**、**循环引用检测** | P1 | `kb/scope.go` `PackScope`、`kb/db.go` `FindExactScoped` |
| **TM 自闭环** | 达阈值生成待审候选，超管人工审核 | ❌ 缺 **自动化审核**（规则+模型预筛）、**审核 SLA/升级**、**冲突合并策略** | P1 | `service/ticket.go` `bumpTmHitsFromTransitions`、`api/tmreview.go` |
| **多租户隔离** | tenant_id + pack_id 三元组唯一键 | ❌ 缺 **跨租户共享包授权流程**（白名单/计费分摊）、**数据脱敏导出** | P2 | `kb/db.go` `ensurePackScopeUnique` |
| **导入/导出** | TMX/平行语料/术语表导入 | ❌ 缺 **大文件断点续传**、**导入预检报告（重复/冲突/格式错误）**、**增量同步** | P1 | `api/upload.go` `handleImportKB`/`handleImportTMX`/`handleImportBitext` |
| **四层统一表** | `kb_entries` layer=1/2/3/4（术语/TM/安全句/碎片） | ✅ 设计完整 | - | `store.go:132-145` `kb_entries` 建表 |
| **语义检索缓存** | 三级查找：管线预取→进程缓存→回源单条 | ✅ 显著降低 Embed 调用 | - | `engine.go:585-601` `EmbedLookupFrom`/`getCachedEmbed` |
| **CJK 缓存** | 按「租户|组织链指纹|跨部门开关」分片、封顶 128 片 | ⚠️ 无 LRU/TTL 统一框架、无命中率指标 | P1 | `engine.go:361-397` `getCJKCache` |

---

## 3. 计费与商业化

| 场景/分支 | 现状 | 缺口 | 优先级 | 备注 |
|-----------|------|------|--------|------|
| **双桶台账** | 额度(近到期先扣) + 永久余额，事务原子 | ✅ 完整 | - | `billing/quota.go` `CheckBalance`、`store.go` `DeductWithGrants` |
| **实时计量** | 内存累积+2s/200条批量落库，影子余额即时中止 | ❌ 缺 **配额预留**（预授权/占用）、**跨租户汇总账单**、**分账报表** | P1 | `billing/sink.go` `UsageSink` |
| **套餐/订单** | free/paid/increment 三类，15min 超时自动关单 | ❌ 缺 **订阅自动续费**、**升降级按比例退差价/补差价**、**发票自动开具/红冲** | P1 | `api/pay.go` `handleOrderCreate`/`handleOrderPay`/`handleOrderRefund` |
| **支付** | 微信 AES-GCM / 支付宝 RSA2 + 模拟/人工确认 | ❌ 缺 **支付宝当面付/分账**、**微信服务商分账**、**退款原路返回异步确认** | P1 | `payment/payment.go` `WechatDecrypt`/`AlipayVerify` |
| **邀请裂变** | 个人用户体验叠加+首单奖励，企业用户不参与 | ❌ 缺 **多级分销**、**邀请海报生成**、**裂变漏斗分析** | P2 | `api/referral.go` `handleReferralCreate`/`handleReferralList` |
| **用量看板** | 租户/用户/组织维度、biz_kind/biz_mode 标注 | ❌ 缺 **成本利润分析**、**供应商对账单**、**异常用量自动告警(阈值/趋势)** | P2 | `api/billing.go` `handleUsage`/`handleUsageOrg`/`handleUsageMe` |
| **Token 迁移** | 句数→Token 一次性迁移，幂等标记 `billing_token_migrated` | ✅ 完整 | - | `billing/migrate.go` `RunTokenMigration` |
| **Markup 统一口径** | 入账=扣费×MarkupMultiplier，R-M1 已修正 | ✅ 完整 | - | `billing/quota.go` `RecordUsage`、`store.go` `RecordUsage` |

---

## 4. 文件翻译管线

| 场景/分支 | 现状 | 缺口 | 优先级 | 备注 |
|-----------|------|------|--------|------|
| **格式支持** | docx/pptx/xlsx/pdf/txt/csv/md + srt/vtt/json/yaml→xlsx | ❌ 缺 **InDesign/FrameMaker**、**CAD/DWG**、**音视频字幕烧录**、**EPUB/Kindle** | P2 | `fileproc/` 目录各格式处理器 |
| **PDF 保真度** | 两阶段 w:t 替换、图片/排版零破坏 | ❌ 缺 **扫描件 OCR+版面还原**（已移除 OCR，但客户有需求）、**表格跨页拆分保真**、**字体子集化嵌入** | P1 | `fileproc/pdf.go` `extract`/`apply`、`fileproc/pdfwrite.go` |
| **大文件** | >15MB/>120页前置拒绝、子进程 OOM 受害者 | ❌ 缺 **分片并行翻译+合并**、**增量翻译（仅译修改段）**、**断点续译** | P1 | `main.go` `FILE_HARDGATE_MAX_SEC`、`fileproc/subprocess.go` |
| **多文件** | 并行 3 个、zip 打包下载 | ❌ 缺 **文件级进度流式推送**、**单文件失败不阻塞整体、支持重试单文件** | P1 | `service/ticket.go` `runFileTicket` 多文件分支 |
| **产物管理** | 14 天留存+到期提醒、核心译文沉淀 TM | ❌ 缺 **产物版本管理**、**产物预览(在线对照)**、**水印/加密/权限控制** | P2 | `store.go` `RegisterArtifact`/`ArtifactsMigrate` |
| **硬闸补漏** | `FILE_HARDGATE_MAX_SEC`(默认600s)+连续2轮零进展熔断 | ✅ 防卡死兜底 | - | `engine.go` `BatchTranslate` 硬闸逻辑 |
| **xlsx 单目标原地替换** | 单目标语言文件翻译改为原地替换单元格 | ✅ 已修复「打开仍是中文原 Sheet」误解 | - | `fileproc/xlsx.go` `TranslateXLSX` |

---

## 5. 多租户与组织架构

| 场景/分支 | 现状 | 缺口 | 优先级 | 备注 |
|-----------|------|------|--------|------|
| **四级树** | 平台根→租户根→组织→部门，拖拽调层级 | ❌ 缺 **组织架构同步（SCIM/AD/LDAP）**、**部门预算多级汇总/钻取** | P1 | `api/orgs.go` `handleOrgsTree`/`handleOrgUpdate` |
| **角色权限** | super_admin/admin/tenant_admin/approver/dept_admin/user | ❌ 缺 **细粒度 RBAC（资源级/字段级）**、**自定义角色**、**权限审计回放** | P2 | `iam/models.go` `Role`、`api/admin.go` 权限校验 |
| **品牌定制** | 子域名解析、Logo/背景/布局拖拽、登录跳转 | ❌ 缺 **白标部署（自定义域名+CNAME 验证）**、**邮件模板品牌化**、**多语言品牌资产** | P1 | `api/tenant.go` `handleTenantBranding`、`frontend-react/src/branding.tsx` |
| **邀请码** | 企业邀请码绑定组织、无效码降级个人 | ❌ 缺 **邀请链接带参数（来源/渠道/UTM）**、**批量邀请/导入** | P2 | `api/auth.go` `handleRegister` 企业注册分支 |
| **租户状态** | active/disabled/expired、deactivate_at 宽限期 | ✅ 完整 | - | `tenant/tenant.go` `Status`、`store.go` `deactivate_at` 列 |

---

## 6. OpenAPI 与集成

| 场景/分支 | 现状 | 缺口 | 优先级 | 备注 |
|-----------|------|------|--------|------|
| **任务模型** | 异步任务+轮询(status/download)、同步短文 | ❌ 缺 **Webhook 重试策略（指数退避/死信队列）**、**回调签名验证 SDK**、**批量任务提交** | P1 | `api/openapi.go` `handleOpenAPITaskCreate`/`handleOpenAPITaskStatus` |
| **SDK** | Python SDK（sdk/python/translator_sdk.py） | ❌ 缺 **TypeScript/Go/Java SDK**、**OpenAPI Spec 自动生成 SDK CI**、**SDK 版本发布流水线** | P1 | `sdk/python/translator_sdk.py` |
| **认证** | API Key (AES-GCM 加密存储、轮换、日限额) | ❌ 缺 **OAuth 2.0 / OIDC**、**JWT 互信**、**Key 作用域细粒度(仅翻译/仅KB/仅计费)** | P2 | `api/apikeys.go` `handleAPIKeyCreate`/`handleAPIKeyRotate` |
| **插件生态** | Word taskpane + 浏览器划词扩展 | ❌ 缺 **Chrome/Edge/Firefox 应用商店上架包**、**VS Code/Cursor 插件**、**Trados/memoQ/QT 插件** | P2 | `extension/`、`office/` |
| **OpenAPI UAT** | 生产端点 7/7 全通过（balance/kb-stats/usage/translate/tasks/apikey-rotate） | ✅ 核心功能验收通过 | - | `PROGRESS.md` §〇-E |
| **同步翻译 tokens_used 回填** | R-L1 已修复，引擎注入用量收集器后由 UsageTokens 汇总 | ✅ 已修复 | - | `engine.go` `UsageTokens`、`api/openapi.go` `handleOpenAPITranslateSync` |

---

## 7. 前端体验

| 场景/分支 | 现状 | 缺口 | 优先级 | 备注 |
|-----------|------|------|--------|------|
| **工作台** | ChatWindow(聊天式) + TicketsPage(工单式) | ❌ 缺 **对照编辑器（源文/译文并排、段落锚点、术语高亮、批注/驳回流）**、**翻译记忆侧边栏检索** | P0 | `ChatWindow.tsx`、`TicketsPage.tsx` |
| **进度可视化** | 气泡进度(步骤+百分比+耗时) | ❌ 缺 **实时流式进度(SSE/WebSocket)**、**文件级/语言级/段落级钻取**、**预估剩余时间** | P1 | `TicketsPage.tsx` `ticketProgress`/`currentStepLabel` |
| **国际化** | 中英双语、localStorage 记忆、面板级 i18n 文件 | ❌ 缺 **语言包热更新**、**复数形式/性别/语序 ICU MessageFormat**、**翻译管理平台集成** | P2 | `frontend-react/src/i18n/` 目录结构 |
| **主题/品牌** | TDesign token、品牌蓝、登录页双布局拖拽 | ❌ 缺 **暗黑模式**、**高对比度/无障碍(WCAG 2.1 AA)**、**自定义 CSS 变量面板** | P2 | `frontend-react/src/styles/theme.css`、`Login.tsx` |
| **离线/弱网** | 离线横幅+重试 | ❌ 缺 **Service Worker 离线缓存**、**乐观 UI + 冲突合并**、**IndexedDB 本地草稿** | P2 | `ChatWindow.tsx` 离线横幅 |
| **双模式持久化** | fast/pro localStorage 记忆、ModeToggle 组件复用 | ✅ 完整 | - | `ChatWindow.tsx` `mode` state、`ModeToggle.tsx` |
| **品牌子域直载** | `/api/branding` 按 host 直接加载、登录后跳转 brand_host | ✅ 完整 | - | `branding.tsx` `BrandingProvider`、`App.tsx` 跨域 token 处理 |

---

## 8. 运维与基建

| 场景/分支 | 现状 | 缺口 | 优先级 | 备注 |
|-----------|------|------|--------|------|
| **部署** | systemd + Caddy、scp 二进制+dist、脚本化 | ❌ 缺 **蓝绿/滚动部署**、**数据库迁移版本化+回滚**、**Canary 发布**、**Terraform/Ansible IaC** | P1 | `deploy/` 目录脚本、`build.sh`、`start.sh` |
| **监控** | /metrics(Prometheus)、看门狗(余额/模型健康) | ❌ 缺 **Grafana 仪表板模板**、**SLO 定义(可用性/延迟/质量)**、**分布式追踪**、**日志聚合** | P1 | `api/watchdog.go` `startWatchdog`、`api/metrics.go` |
| **备份/灾备** | `restore_drill.sh` 演练脚本 | ❌ 缺 **自动化定时备份(全量/增量)**、**异地复制**、**RPO/RTO 量化指标**、**混沌工程演练** | P1 | `deploy/restore_drill.sh` |
| **密钥管理** | EnvironmentFile(0600)、启动强校验 | ❌ 缺 **密钥轮换自动化**、**HSM/KMS 集成**、**密钥泄露扫描** | P1 | `deploy/systemd/prod.conf` `EnvironmentFile` |
| **性能基线** | GOMEMLIMIT=850MiB、MemoryMax=1150M、worker=4（2026-09-04 生产复核值） | ❌ 缺 **持续性能基线对比**、**容量规划模型**、**自动扩缩容(水平/垂直)** | P2 | `main.go` `debug.SetMemoryLimit`、`deploy/systemd/prod.conf` |
| **pprof 诊断** | 仅回环 127.0.0.1:18787、外网不可达 | ✅ 安全 | - | `main.go` pprof 端点 |
| **Caddy 安全头** | HSTS/nosniff/DENY/Referrer-Policy/Permissions-Policy/-Server | ✅ 基线完整 | - | `deploy/caddy/translator.conf` header 块 |

---

## 9. 跨模块横切关注点

| 横切点 | 现状 | 缺口 | 优先级 |
|--------|------|------|--------|
| **错误码体系** | ~~字符串错误散落、无统一 Code/HTTP Status 映射~~ → **已收敛**（2026-09-04）：`internal/errors/codes.go` OpenAPI snake_case 常量 + HTTP 映射 + `openapi.v1.json` 统一 Error Schema + 前端 `ApiError` 透传（提交 309a126） | ⚠️ 剩余：前端错误码国际化、SDK 错误码对齐发布 | P1 |
| **审计日志** | `audit_logs` 表、before_val/after_val JSON | ⚠️ 缺结构化查询、导出、合规报表、实时告警 | P2 |
| **限流** | 登录/注册/验证码限流落库(`rate_limits`表)+内存兜底 | ✅ 基础完备 | - |
| **CORS** | deny-by-default、/openapi/* 无条件反射 | ✅ 安全基线 | - |
| **GDPR 擦除** | `EraseTenantDataFull` 12 表+工单磁盘产物清理 | ✅ 完整 | - |
| **邮件模板** | 6 类模板后台可配、注册自动发产品手册 PDF | ✅ 完整 | - |

---

## 统计汇总

| 优先级 | 缺口数量 | 核心模块分布 |
|--------|----------|--------------|
| **P0** | 3 | 对照编辑器(P0)、向量检索外置化(P0)、SQLite HA(P0) |
| **P1** | 28 | 翻译引擎 4、KB 3、计费 4、文件管线 3、组织 2、OpenAPI 2、运维 4、错误码 1 |
| **P2** | 21 | 翻译引擎 2、KB 2、计费 2、文件管线 2、组织 2、OpenAPI 2、前端 4、运维 1、横切 2 |

> **注**：P0 为「不做会死/资损/合规不达标」；P1 为「不做影响规模化/企业级销售/运维效率」；P2 为「做了更强/体验更好/长期演进」。