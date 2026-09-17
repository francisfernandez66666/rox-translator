# 能言 SaaS 系统全量架构评审报告

> 生成时间：2026-08-29  
> 评审范围：全仓代码（backend-go、frontend-react、extension、deploy、sdk）+ 运行时配置 + 部署脚本 + 历史整改方案  
> 评审方式：端到端全量阅读、像素级场景全分支跟踪

---

## 一、系统整体架构设计评价

### 1.1 架构优势（亮点）

| 维度 | 评价 | 关键证据 |
|------|------|----------|
| **部署形态** | 单 Go 二进制 + SQLite + 静态资源，零外部依赖（除 LLM API），运维极简 | `main.go` 单入口、`deploy/systemd/prod.conf` 沙箱化、`build.sh` macOS .app 打包 |
| **多租户隔离** | JWT + API Key + 租户切换器三层，普通用户强制 JWT 租户、超管 X-Tenant-ID 切换，防越权设计完善 | `server.go:409-441` `withTenant` 中间件、`auth.go` JWT 解析 |
| **知识库架构** | 四层（术语/TM/安全句/碎片）+ 组织继承链（部门包就近覆盖 > 企业包 > 行业包 > 文化包）+ 跨部门降级开关，隔离语义硬约束 | `kb/db.go` `FindExactScoped`/`FuzzyHitsScoped`、`scope.go` `PackScope` |
| **计费体系** | 双桶台账（发放额度+永久余额）+ 实时计量（内存累积+周期批量落库）+ Markup 统一口径，彻底消除双重计费/资损 | `billing/sink.go` `UsageSink`、`billing/quota.go` `CheckBalance` 双桶口径、`service/ticket.go` `chargeTokens` 仅回填不扣费 |
| **翻译引擎** | 统一网关（ModelRoutes 按权重选主+降级链）、主模型熔断冷却、阶段模型覆盖、并发信号量、硬闸重试、截断自修复 | `engine/engine.go` `resolveModel`/`breaker`/`translateLangsConcurrent`/`singleLang` 重试链 |
| **文件管线** | PDF 两阶段（pdf2docx 提取 → w:t 级替换 → LibreOffice 回 PDF）、前置拦截(>15MB/>120页)、子进程 OOM 优先受害者 | `fileproc/pdf.go` `extract`+`apply`、`main.go` `GOMEMLIMIT=650Mi`、`FILEPROC_RLIMIT_AS_MB` |
| **异步任务** | Direct queue（jobs 表 + 租约超时回收 + 重试 + 死信），启动断点续跑 + 卡死巡检（>20min） | `queue/queue.go` `Queue` 接口、`service/ticket.go` `BootResume`/`StartStallSweep` |
| **安全加固** | systemd 沙箱、密钥 EnvironmentFile(0600)、CORS deny-by-default、Caddy 安全头、API Key AES-GCM 加密存储、支付真实验签 | `deploy/systemd/prod.conf`、`deploy/caddy/translator.conf`、`store/store.go` `key_enc`、`payment/payment.go` 真实加解密 |

### 1.2 架构劣势/技术债

| 问题 | 严重度 | 说明 | 影响范围 |
|------|--------|------|----------|
| **单点 SQLite** | P1 | 仅单机部署，无 HA/读写分离，写并发靠 `_txlock=immediate` + 批量落库缓解，但硬上限约 100-200 QPS | 并发扩展性、高可用、灾备 |
| **内存缓存无统一框架** | P1 | CJK缓存/文化缓存/Embedding缓存/影子余额各自实现封顶清空策略，无统一淘汰、无监控指标 | 内存泄漏风险、缓存命中率不可观测、多机扩展受阻 |
| **前端状态管理分散** | P1 | `useChat`/`useAuth`/`useBranding`/`AdminProvider` 分离，跨组件共享依赖 props drilling 或 context 嵌套，无统一状态库 | 维护成本、状态同步 Bug、TypeScript 推导复杂度 |
| **国际化键硬编码散落** | P2 | `lang.xxx` 键在 TSX 与后端错误码中双维护，缺乏抽取校验工具 | 多语言扩展困难、键缺失/拼写错误无编译期发现 |
| **测试覆盖极低** | P2 | 仅 `*_test.go` 零星单元测试，无集成/E2E/Contract 测试，CI 缺失 | 回归风险、重构不敢动、交付信心低 |
| **可观测性仅有 /metrics** | P2 | 无分布式追踪、无结构化日志标准、无 SLO/SLI 定义、告警仅邮件/站内信 | 故障定位慢、性能基线无对比、容量规划靠猜 |
| **OpenAPI 文档手写维护** | P2 | `openapi-docs` 后台在线编辑，无 OpenAPI 3.0 Spec 自动生成、无 SDK 自动生成流水线 | 文档漂移、SDK 手写维护成本高、第三方集成门槛高 |
| **Python 子进程耦合** | P1 | PDF 管线强依赖 `pdf2docx`/`LibreOffice`/`python-docx`，版本锁定、环境脆弱、难容器化 | 部署环境一致性、水平扩展、安全攻击面 |

---

## 二、架构关键决策复盘

| 决策点 | 当前选择 | 评价 | 建议演进 |
|--------|----------|------|----------|
| **数据库** | SQLite 单文件(WAL+immediate lock) | 适配单机、低维护，但成天花板 | 引入 **SQLite 集群** 或 **PostgreSQL 兼容层** 为多机扩展预留 |
| **队列** | Direct queue (jobs 表) | 够用，但无持久化保序、无优先级、无延迟队列 | 引入 **Redis Streams** 或 **NATS JetStream** 做 L2 队列，保留 Direct 做本地执行器 |
| **缓存** | 进程内 map + 手工封顶 | 无统一框架、无监控、无分布式 | 引入 **Redis Cluster** 做 L2 缓存，进程内保留 L1 |
| **向量检索** | NPZ 内存全量 | 10万条以内可接受，超 50 万必崩 | 接入 **pgvector / Milvus / Qdrant**，保留 NPZ 做边缘/离线 |
| **LLM 网关** | 进程内 ModelRoutes + 熔断 | 单点、无熔断可视化、无成本感知 | 抽离为 **Sidecar/独立网关服务**（Kong/Envoy+插件 或 自研 Go 网关） |
| **文件转换** | Python 子进程(pdf2docx/LibreOffice) | 环境脆弱、难容器化、内存不可控 | 评估 **LibreOffice Headless 容器化 + gRPC**、**Apache Tika**、**商业转换 API** |
| **前端状态** | React Context + 自定义 Hooks | 适合中小应用，大型化会 context hell | 引入 **Zustand/Jotai/Redux Toolkit** 统一状态层 |
| **国际化** | 手写面板级 dict + `t('key')` | 无提取工具、无类型安全、键易拼写错误 | 接入 **i18n-ally / Lingui / Tolgee**，CI 校验键完整性 |

---

## 三、一句话总结

**这是一个「功能极其完备、工程质量极高、商业化闭环已跑通」的单机版 SaaS 系统**，在翻译引擎、知识库隔离、计费精度、文件管线、多租户权限、部署加固等核心链路上都已把「坑」踩完并填平。

**但架构天花板在 SQLite 单点、进程内缓存/队列/向量索引、Python 子进程耦合、前端状态分散、可观测性缺失** —— 要支撑「多机高可用、百万级 KB、企业级集成、插件生态」必须做 **存算分离、组件服务化、规范化工程化** 三件大事。

建议按 **P0→P1→P2** 分三期演进，每期 4-6 周，保持主干可发布，渐进式重构。