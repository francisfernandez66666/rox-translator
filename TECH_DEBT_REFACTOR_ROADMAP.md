# 能言 SaaS 技术债与重构路线图

> 基于全量代码阅读，梳理存量技术债、架构演进路径、重构策略与里程碑

---

## 一、技术债分类清单

### 1.1 架构层面债务（阻碍横向扩展）

| 债务项 | 当前实现 | 痛点 | 重构目标 | 工作量估算 |
|--------|----------|------|----------|------------|
| **SQLite 单点写** | `modernc.org/sqlite` + `_txlock=immediate` + WAL | 单机写上限 ~200 QPS，无 HA，备份锁库 | **PostgreSQL 兼容层**或 **SQLite 集群** | 3-4 人周 |
| **进程内组件耦合** | Engine/Queue/Cache/VectorIndex 同进程 | 单点故障域大、无法独立扩缩容、升级需全量重启 | **Sidecar 化**：LLM Gateway、Vector Search、File Converter、Task Queue | 4-6 人周 |
| **Python 子进程强耦合** | `pdf2docx`/`LibreOffice`/`python-docx` 直调 | 环境脆弱、版本锁定、内存不可控、难容器化、安全面大 | **gRPC 微服务化**文件转换器，或接入商业转换 API | 3-4 人周 |
| **无统一配置中心** | `system_config` 表 + 环境变量 + config.json 三源 | 热更新不一致、无版本审计、无 Schema 校验、灰度发布困难 | **统一配置服务**（etcd/Consul/Apollo）+ Schema 校验 | 2-3 人周 |

### 1.2 数据层面债务

| 债务项 | 当前实现 | 痛点 | 重构目标 | 工作量估算 |
|--------|----------|------|----------|------------|
| **向量索引内存全量** | NPZ 启动加载、重建全量、无增量 | >50万条 OOM、重建耗时、无混合检索 | **pgvector / Milvus / Qdrant** 外置化、增量索引、稀疏+稠密混合检索 | 3-4 人周 |
| **缓存无统一框架** | CJK/文化/Embedding/影子余额各自 map+mutex+封顶 | 无 LRU/TTL、无命中率指标、无分布式、内存泄漏风险 | **Redis Cluster L2 + 本地 L1**，统一 `Cache` 接口、指标暴露 | 2-3 人周 |
| **队列仅 Direct** | `jobs` 表 + 租约超时回收 | 无优先级、无延迟队列、无保序、无死信可视化、单点 | **Redis Streams / NATS JetStream** L2 队列，Direct 保留为本地执行器 | 2-3 人周 |
| **审计日志仅入库** | `audit_logs` 表、JSON 存前后值 | 无结构化查询、无导出、无合规报表、无实时告警 | **结构化审计流**（Kafka/ClickHouse）+ 合规仪表板 | 2-3 人周 |

### 1.3 代码层面债务

| 债务项 | 典型表现 | 影响文件数 | 重构策略 |
|--------|----------|------------|----------|
| **魔数/硬编码阈值** | `100000`、`50000`、`15MB`、`120页`、`650Mi`、`950M` 散落 | 30+ | 迁移 `system_config` 热更新 + 启动校验 + 单位测试 |
| **错误码体系缺失**（OpenAPI 层已收敛 2026-09-04，见 `internal/errors/codes.go`+提交 309a126；以下策略适用于剩余内部路径） | `fmt.Errorf` 字符串、`quotaErr`、HTTP 状态码映射随意 | 50+ | 统一 `ErrorCode` 枚举 + `APIError` 结构 + OpenAPI Schema + 前端映射表 |
| **国际化键硬编码** | `t('lang.xxx')`、`tk.stQueued` 散落 TSX/Go，无提取校验 | 200+ | 接入 **Lingui / i18n-ally**，CI 校验键完整性、类型安全 |
| **前端状态分散** | `useChat`/`useAuth`/`useBranding`/`AdminProvider` 互不相知 | 15+ | 引入 **Zustand** 统一 Store，Provider 扁平化、Selector 粒度化 |
| **测试覆盖极低** | 仅 `*_test.go` 单元测试，无集成/E2E/Contract | 全仓 | **测试金字塔**：Contract(Pact) → Integration(Testcontainers) → E2E(Playwright) → CI Gate |
| **OpenAPI 手写维护** | 后台在线编辑 `openapi-docs`，无 Spec 生成、无 SDK 流水线 | 1 | **Goa / oapi-codegen / swag** 生成 Spec → **openapi-generator** 多语言 SDK → 发布流水线 |

---

## 二、架构演进路径（三阶段）

### Phase 1：存算分离、组件服务化（6-8 周）
**目标**：消除单点写瓶颈、向量检索外置、文件转换解耦、配置统一

```
┌─────────────────────────────────────────────────────────────────┐
│                      Phase 1 目标架构                            │
├─────────────────────────────────────────────────────────────────┤
│  Go API Gateway (Stateless)                                     │
│  ├── /api/*          → 业务逻辑（保留单体，但无状态）              │
│  ├── /openapi/*      → 同业务逻辑                                │
│  └── /metrics        → Prometheus 指标                          │
├─────────────────────────────────────────────────────────────────┤
│  PostgreSQL (主库)                                              │
│  ├── 业务表（users/tickets/orders/billing/...）                  │
│  ├── pgvector 扩展（向量检索）                                   │
│  └── 逻辑复制/流复制（HA）                                       │
├─────────────────────────────────────────────────────────────────┤
│  Redis Cluster                                                  │
│  ├── L2 缓存（CJK/文化/Embedding/影子余额/限流计数）             │
│  ├── Streams 队列（任务/通知/Webhook/邮件）                      │
│  └── 分布式锁/幂等键                                             │
├─────────────────────────────────────────────────────────────────┤
│  Sidecar Services (gRPC)                                        │
│  ├── LLM Gateway    : ModelRoutes/熔断/成本路由/竞速/流式       │
│  ├── Vector Search  : 增量索引/混合检索/过滤/重排序              │
│  ├── File Converter : PDF/DOCX/PPTX/XLSX/OCR/版面还原           │
│  └── Task Executor  : 从 Redis Streams 消费，执行翻译流水线      │
├─────────────────────────────────────────────────────────────────┤
│  Config Service (etcd/Consul)                                   │
│  ├── 统一 Schema 校验                                            │
│  ├── 版本审计/回滚                                               │
│  └── 灰度发布/特性开关                                           │
└─────────────────────────────────────────────────────────────────┘
```

**关键任务拆解**：

| 任务 | 交付物 | 验收标准 |
|------|--------|----------|
| PostgreSQL 迁移 | 迁移脚本、双写验证、回滚预案 | 读写分离、主从切换 <30s、零数据丢失 |
| pgvector 接入 | 向量表、增量索引 API、混合检索 API | 100万条 <100ms P99、增量写入 <10ms |
| Redis Cluster 接入 | 缓存/队列/锁客户端、迁移脚本 | 命中率 >95%、队列吞吐 >10k/s |
| LLM Gateway 服务化 | gRPC 接口、ModelRoutes 配置热更、熔断指标 | 竞速模式 P99 降低 30%、成本感知路由生效 |
| File Converter 服务化 | gRPC 接口、容器镜像、健康检查 | 并发 10+、内存稳定、支持 OCR 可选 |
| Config Service | etcd 集群、Go 客户端、Admin 面板 | 配置变更 <1s 生效、历史版本可回滚 |

### Phase 2：规范化工程化、可观测性、测试体系（4-6 周）
**目标**：建立工程规范、全链路可观测、自动化质量门禁

```
┌─────────────────────────────────────────────────────────────────┐
│                      Phase 2 目标能力                            │
├─────────────────────────────────────────────────────────────────┤
│  统一错误码体系          │ ErrorCode 枚举 + APIError + 多语言映射  │
│  结构化日志 + TraceID   │ Zap + OTel SDK、全链路透传、Loki 接入   │
│  分布式追踪             │ Tempo/Jaeger、服务拓扑、延迟火焰图      │
│  SLO/SLI 定义           │ 可用性/延迟/质量/成本 四维、Burn Rate 告警│
│  Contract Testing       │ Pact (Provider/Consumer)、CI Gate       │
│  Integration Testing    │ Testcontainers (PG/Redis/Kafka)、并行   │
│  E2E Testing            │ Playwright、关键用例覆盖、视频回放       │
│  OpenAPI Spec 生成      │ oapi-codegen → Spec → openapi-generator │
│  多语言 SDK 流水线      │ Python/TS/Go/Java、语义版本、自动发布   │
│  国际化工程化           │ Lingui 提取/校验/类型、CI 阻断缺失键     │
│  前端状态统一           │ Zustand Store、Provider 扁平化、SSR 就绪 │
└─────────────────────────────────────────────────────────────────┘
```

### Phase 3：企业级特性、生态扩展、智能化（持续演进）
**目标**：支撑大客户交付、插件生态、AI 原生能力

```
┌─────────────────────────────────────────────────────────────────┐
│                      Phase 3 重点方向                            │
├─────────────────────────────────────────────────────────────────┤
│  对照编辑器              │ 段落锚点/术语高亮/批注驳回/版本对比/协同 │
│  流式进度                │ SSE/WebSocket、文件/语言/段落级钻取     │
│  白标部署                │ 自定义域名/CNAME 验证/品牌资产隔离       │
│  组织架构同步            │ SCIM 2.0 / LDAP / AD、实时增量同步       │
│  细粒度 RBAC             │ 资源级/字段级权限、自定义角色、审计回放  │
│  插件生态商店化          │ VS Code/Trados/memoQ/Chrome Store 上架   │
│  暗黑模式/无障碍         │ WCAG 2.1 AA、CSS 变量体系、主题引擎      │
│  术语强制模式            │ Constrained Decoding / Logit Bias       │
│  多级分销/裂变分析       │ 分销树/漏斗/归因/自动结算                │
│  智能化运维              │ 容量规划模型/自动扩缩容/混沌工程/自愈     │
└─────────────────────────────────────────────────────────────────┘
```

---

## 三、重构策略与原则

### 3.1 核心原则

| 原则 | 说明 | 执行方式 |
|------|------|----------|
| **主干始终可发布** | 任何重构不阻塞业务迭代 | Feature Flag 守护、分支寿命 <3 天、小步提交 |
| **数据不重写** | 存量数据迁移而非重建 | 双写验证 → 读切换 → 停写旧表 → 归档 |
| **接口先行** | 定义契约再实现 | Protobuf/gRPC 定义 → Mock Server → Consumer 先接入 |
| **可观测优先** | 重构前先有基线指标 | 每个组件必须有：RED 指标、日志结构化、TraceID 透传 |
| **回滚预案** | 每次发布必须有回滚脚本 | 数据库迁移可逆、配置可热回滚、蓝绿部署就绪 |

### 3.2 重构模式对照表

| 重构对象 | 模式 | 关键技术点 |
|----------|------|------------|
| **数据库** | Strangler Fig + 双写 | `sqlc` 生成 PG/SQLite 双 dialect、同步校验、逐表切流 |
| **缓存** | Cache-Aside + Write-Through | 统一 `Cache` 接口、L1/L2 分层、失效广播、预热策略 |
| **队列** | Outbox Pattern + Relay | `jobs` 表作 Outbox、Relay 进程发到 Redis Streams、幂等消费 |
| **向量检索** | Shadow Index + 逐步切流 | 新旧索引并行写、读端对比一致性、流量渐进切换 |
| **LLM 网关** | Sidecar + Envoy Filter | 现有 `ModelRoutes` 配置迁移、熔断指标暴露、竞速路由插件 |
| **文件转换** | gRPC + 容器化 | 现有 `fileproc/*` 封装为 gRPC 服务、健康检查、优雅降级 |
| **前端状态** | 逐模块迁移到 Zustand | 先迁移 `auth`/`chat`/`branding`，再清理 Context |

---

## 四、里程碑与交付物

### Milestone 1：数据库与向量外置（第 1-4 周）
| 周次 | 交付物 | 验收 |
|------|--------|------|
| W1 | PG Schema 迁移脚本、双写中间件、Testcontainers 集成测试 | 单表双写一致性 100% |
| W2 | 全表迁移完成、读流量 10% 切 PG、监控告警就绪 | P99 延迟 <50ms、零错误 |
| W3 | pgvector 扩展建表、向量数据迁移脚本、增量索引 gRPC 接口 | 10万条增量索引 <30s |
| W4 | 混合检索上线、Shadow 流量 100% 对比、全量切流 | 召回率 ≥旧版、P99 <100ms |

### Milestone 2：Redis + 队列 + 配置（第 5-8 周）
| 周次 | 交付物 | 验收 |
|------|--------|------|
| W5 | Redis Cluster 部署、缓存客户端统一接口、CJK/文化/Embedding 迁移 | 命中率 >95%、内存下降 60% |
| W6 | Redis Streams 队列、Outbox Relay、任务消费器重构 | 吞吐 >5k/s、零丢单 |
| W7 | etcd Config Service、system_config 迁移、Admin 面板热更 | 配置变更 <1s 生效 |
| W8 | 端到端压测、混沌演练（杀节点/断网/慢查询）、性能基线报告 | 通过 P0 SLA |

### Milestone 3：Sidecar 服务化（第 9-14 周）
| 周次 | 交付物 | 验收 |
|------|--------|------|
| W9-10 | LLM Gateway gRPC 服务、ModelRoutes 热更、竞速路由、流式 SSE | 竞速 P99 降 30%、成本降 15% |
| W11-12 | File Converter gRPC 服务、容器镜像、OCR 可选、健康检查 | 并发 10+、内存稳定、零 OOM |
| W13 | Task Executor 从 Streams 消费、翻译流水线解耦、断点续跑 | 单任务重试 <3min、零卡死 |
| W14 | 全链路联调、生产灰度 10% → 50% → 100%、文档归档 | 核心流程零回归 |

### Milestone 4：工程规范与可观测（第 15-20 周）
| 周次 | 交付物 | 验收 |
|------|--------|------|
| W15 | 统一错误码枚举、APIError 结构、OpenAPI Schema、前端映射表 | 100% 接口返回标准错误 |
| W16 | Zap+OTel 结构化日志、TraceID 透传、Loki/Tempo 接入、Grafana 仪表板 | 故障定位 <10min |
| W17 | SLO 定义（4 维）、Burn Rate 告警、错误预算看板 | 告警噪音 <20% |
| W18 | Pact Contract Test、Testcontainers Integration、Playwright E2E、CI Gate | 覆盖率 >80%、Gate 阻断 |
| W19 | oapi-codegen Spec 生成、openapi-generator 多语言 SDK、发布流水线 | Spec 与实现 100% 一致 |
| W20 | Lingui 国际化提取/校验/类型、Zustand 状态统一、SSR 就绪评估 | 0 缺失键、类型安全 |

---

## 五、风险与对策

| 风险 | 可能性 | 影响 | 对策 |
|------|--------|------|------|
| **PG 迁移数据不一致** | 中 | P0 | 双写校验脚本全量跑、不一致自动修复、回滚预案演练 3 次 |
| **向量检索召回率下降** | 中 | P1 | Shadow 流量 100% 对比 2 周、人工抽样评估、阈值可调 |
| **Sidecar 网络延迟增加** | 高 | P1 | 同机房部署、连接池复用、gRPC keepalive、本地缓存热数据 |
| **重构周期过长阻塞业务** | 高 | P0 | Feature Flag 守护、双轨并行（旧链路维护、新链路建设）、每周 Demo |
| **团队认知负荷过大** | 中 | P2 | 架构决策记录 (ADR)、代码走查、结对编程、内部分享会 |
| **第三方依赖版本地狱** | 中 | P1 | 依赖锁文件、Dependabot 自动升级、最小版本策略、供应链扫描 |

---

## 六、资源需求估算

| 角色 | Phase 1 | Phase 2 | Phase 3 | 备注 |
|------|---------|---------|---------|------|
| 后端工程师 | 3 | 2 | 2 | 含 1 Tech Lead |
| DevOps/SRE | 1 | 1 | 1 | K8s/监控/CI/CD |
| 前端工程师 | 0.5 | 1 | 2 | Phase 2/3 前端重构主力 |
| QA/测试 | 0.5 | 1 | 1 | 自动化测试建设 |
| **合计** | **5** | **5** | **6** | 峰值 6 人 |

> **预算建议**：Phase 1-2 为「必须做」，预算锁定；Phase 3 为「持续投入」，按季度规划。

---

## 七、决策记录模板 (ADR)

每个重构决策须产出 ADR，存放 `docs/adr/`：

```markdown
# ADR-XXX: <标题>

## Status
Proposed / Accepted / Superseded

## Context
<背景、痛点、约束>

## Decision
<决策内容、技术选型、关键权衡>

## Consequences
### Positive
- 正向收益
### Negative
- 负向代价（技术债、复杂度、成本）
### Risks
- 风险点及缓解措施

## Implementation Plan
- 里程碑、Owner、验收标准

## Rollback Plan
- 回滚触发条件、步骤、预估时间
```

---

## 八、起步行动项（本周即可开始）

1. **建立 ADR 目录**，记录本文档所有重构决策
2. **搭建 Testcontainers CI**，跑通 PG/Redis 集成测试骨架
3. **接入 oapi-codegen**，生成首版 OpenAPI Spec，对照现有接口扫描差异
4. **统一错误码枚举**草案，评审后纳入 `internal/errors` 包
5. **pgvector PoC**：10 万条真实数据、增量索引、混合检索、压测报告
6. **Redis Cluster 部署脚本**（Ansible/Terraform）、缓存接口定义
7. **前端 Zustand Store 骨架**，迁移 `auth` 模块为试点
8. **Lingui 配置**接入前端、提取现有键、补齐缺失、CI 校验脚本

---

> **核心信条**：不做大爆炸重写，只做「可验证、可回滚、可度量」的小步演进。每个 Phase 结束必须有 **生产可用的交付物** 和 **性能基线报告**。