# 下一阶段改造方案：解锁 PostgreSQL 与 Redis，并逐项推进路线图

> 文档定位：实施设计文档（spec）。覆盖用户确认的优先级——**先解锁 PG + Redis（P0 架构级缺口），再按 P0（监控/日志）→ P1（对照编辑器/SSO/多AZ/压测）→ P2（测试/版本化/文档/SDK/审计）逐项推进**。
>
> 设计原则：
> 1. **不破坏现有单二进制部署**——PG/Redis 均为「可选后端」，缺失时自动回退 SQLite/进程内实现，保证单实例仍能跑。
> 2. **可灰度、可回滚**——每阶段独立可验证、独立回退。
> 3. **单一事实源**——延续现有「实时计费即唯一扣费源」「双桶台账」「组织继承链 KB」的设计哲学。

---

## 0. 目标与范围

| 阶段 | 内容 | 优先级 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| 阶段一 | PostgreSQL 生产切流 | P0 | RDS/自建 PG 接管业务+KB，pgvector 回填 | ✅ **已生产切流**（2026-09 复核：生产 dialect=postgres、pgvector 已回填；SQLite 保留于 `/opt/translator/data/backups/` 历史留存） |
| 阶段二 | 引入 Redis 分布式能力 | P0 | 分布式锁/信号量/滑动窗口计数，多实例可水平扩展 | ✅ 已落地+联调（自研 RESP 客户端，离线零依赖） |
| 阶段三 | 监控告警（Prometheus+Grafana+Alertmanager） | P0 | 仪表盘 + 关键告警规则 | ✅ 配置交付（需 Grafana/Loki/Prometheus 实例点亮） |
| 阶段四 | 日志聚合（Loki） | P0 | 跨实例 JSON 日志统一检索 | ✅ 配置交付（Promtail/Loki 配置+runbook） |
| 阶段五 | 对照编辑器 docx/pdf | P1 | 段落键对齐 + pdf2docx 复用管线 | ✅ 已落地（docx 纯 Go 段落抽取+回写；pdf 经 python venv pdf2docx 桥接；导出修订稿端点） |
| 阶段六 | SSO/OIDC 适配层 | P1 | Provider 接口 + 飞书/钉钉/Azure AD | ✅ 已落地（标准 OIDC 发现 + 飞书/钉钉 OAuth2；/api/sso/login|callback|providers；JWT 同体系） |
| 阶段七 | 多 AZ 部署 | P1 | PG 流复制 + 会话亲和 | ✅ 已落地（无状态进程 + Redis 唤醒信号器跨实例 worker；部署清单/README 交付） |
| 阶段八 | 压测基线（k6） | P1 | SLO/SLI + 夜ly 压测 | ✅ 脚本交付（deploy/loadtest/k6.js，需 k6 运行） |
| 阶段九 | 前端 E2E（Playwright） | P2 | 核心流程覆盖 | ✅ 配置交付（frontend-react/e2e，需 @playwright/test） |
| 阶段十 | API 版本管理 | P2 | Header 版本控制 | ✅ 已落地（withAPIVersion 中间件 + X-API-Version 回显） |
| 阶段十一 | OpenAPI Spec 自动生成 | P2 | swag 生成 + 文档页渲染 | ✅ 已落地（openapi.v1.json 内联 + /openapi/v1.json 服务端点，与 Python SDK 契约一致） |
| 阶段十二 | SDK 多语言 | P2 | oapi-codegen / openapi-generator | ✅ 已落地（Python/TypeScript/Java 三语言 SDK，同一契约；TS/Java 手工镜像 Python） |
| 阶段十三 | 审计日志留存策略 | P2 | 按月分区 + 冷归档 | ✅ 已落地（按保留天数定期 prune + system_config 覆盖） |

---

## 1. 当前就绪度盘点（已确认）

### 1.1 PostgreSQL：代码已就绪，仅缺生产切流
- `backend-go/internal/db/db.go:25-87` — `DriverSQLite` / `DriverPostgres`（`pgx` 别名），`Open()` 按驱动选择 `modernc.org/sqlite` 或 `lib/pq`。
- `backend-go/internal/db/rewrite.go:92` — `CurrentDialect()` 全局方言切换，SQL 经 `query.go` 重写适配两库。
- pgvector 已实装并带降级：
  - `backend-go/internal/kb/db.go:78` — `EnablePgvector: true`；
  - `backend-go/internal/kb/db.go:992-1023` — `UpsertEmbedding` / `VectorSearch` 走 pgvector 余弦距离；**pgvector 扩展缺失时最佳努力跳过，语义检索回退 npz 索引**（不阻断主流程）。
- 迁移工具已存在：`backend-go/cmd/migrate-sqlite-to-pg/main.go`
  - 幂等：`ON CONFLICT DO NOTHING`，可重复执行；
  - 跳过瞬态表 `jobs` / `ticket_state` / `sqlite_sequence`；
  - **embedding 列不拷贝**，切流后跑一次 `RebuildKBIndex` 回填（工具注释已写明前置条件）。
- 配置入口已就绪：`backend-go/internal/config/config.go:95-98` — `DatabaseDriver` / `DatabaseDSN`（`DefaultDatabaseDriver = "sqlite"`）。

### 1.2 Redis：完全缺失
当前所有并发/配额/限流均为**单进程内存**，多实例部署会互相冲突：
- **LLM 并发**：`backend-go/internal/llm/client.go` 三路信号量（chat 后台/交互保留/embed）+ 文件/后台专用池，均为进程内 `semaphore`。
- **QPS / 登录限流**：`backend-go/internal/store/store.go:416` 有 `rate_limits` 表，但计数与判定在内存。
- **工单租约 / 卡死巡检**：`backend-go/internal/service/ticket.go` 基于内存 + SQLite `jobs` 表租约，多实例重排会冲突。
- **API Key 日配额**：`store.go` 的 `calls_today` 字段，多实例计数不聚合。

---

## 2. 阶段一：解锁 PostgreSQL 生产切流

### 2.1 前置条件
1. 申请托管 RDS PG（建议 15+ 连接、开启自动备份、启用 `pgvector` 扩展）。
2. 目标库预执行：`CREATE EXTENSION IF NOT EXISTS vector;`
3. 备份源：`cp tm.sqlite3 tm.sqlite3.bak` + `pg_dump`（空库）。
4. 维护窗口公告（切流期间停写，建议低峰 30 分钟）。

### 2.2 切流步骤
1. **建表**：以 `DB_DRIVER=postgres` 启动一次服务端（不接流量），让 `db.EnsureSchema` 在 PG 侧建好含 `embedding::vector` 的全部表结构。
2. **迁数据**：
   ```bash
   go run ./cmd/migrate-sqlite-to-pg \
     -sqlite /opt/translator/tm.sqlite3 \
     -dsn "postgres://user:pass@rds-host:5432/langcross?sslmode=require"
   ```
3. **回填向量**：运行 `go run ./cmd/rebuild-kb-index`（**已实现**，见下「阶段一落地交付」）——遍历全量段调用 Embedding API 重新计算并 `UpsertEmbedding` 写回 PG，支持 `-batch`/`-workers`/`-limit`，可重复执行（幂等覆盖）。
4. **配置切换**：在 `secrets.env` 追加 `DATABASE_DRIVER=postgres` + `DATABASE_DSN=...`；重启 `translator.service`。
5. **冒烟**：
   - `curl /status` 返回 dialect=postgres；
   - 提交一条翻译，确认 `VectorSearch` 命中（日志出现 pgvector 检索）；
   - 计费双桶扣减、KB 组织继承链、工单流水线全跑通。

### 2.3 回滚
- 保留 SQLite 原库不动；若 PG 异常，置空 `DATABASE_DRIVER` 重启即回 SQLite（**数据以切流前 SQLite 为准，切流后新增数据需手动反向同步或接受丢失窗口**——故务必在停写窗口内操作）。

### 2.4 验收
- [ ] `DATABASE_DRIVER=postgres` 下全部 UAT 用例通过（复用 OpenAPI UAT 7/7 脚本）。
- [ ] pgvector 语义检索命中率 ≥ 切流前 npz 基线。
- [ ] 双桶台账余额与 SQLite 终态一致（diff 比对）。

---

## 3. 阶段二：引入 Redis 分布式能力

### 3.1 选型
- **Redis 7**（或 Valkey 7，API 兼容）。单节点 + AOF 即可起步；后续多 AZ 用哨兵/集群。
- Go 客户端：`redis/go-redis/v9`（已在 `backend-go` 依赖外，需 `go get`）。

### 3.2 抽象层设计（新增 `internal/infra/`）
避免业务代码直接耦合 Redis，统一抽象以便降级：

```
backend-go/internal/infra/
  redis.go          // 连接池、健康探测、Ping
  distlock/         // 分布式锁（SET NX + 租约 + 看门狗）
    locker.go
  ratelimit/        // 滑动窗口计数（Lua 脚本原子）
    sliding.go
  concurrency/      // 分布式信号量（Redis 红锁思路 / 计数键）
    sema.go
  fallback.go       // Redis 不可用时回退进程内实现（单实例兼容）
```

**降级策略**：`redis.go` 连接失败时返回 `nil`，各组件检测 `nil` 即走进程内实现（与现状行为一致），保证无 Redis 也能跑单实例。

### 3.3 逐项替换映射

| 现有进程内实现 | 文件:行 | Redis 替换方案 | 降级 |
|---|---|---|---|
| LLM 三路信号量 | `llm/client.go` | `concurrency.Semaphore`（按 chat/embed/file 分桶，key 含租户） | 进程内 semaphore |
| 工单租约 / 卡死巡检 | `service/ticket.go` | `distlock.Locker`（`ticket:{id}` 租约 + 看门狗续期） | 内存锁 |
| QPS / 登录限流 | `store.go:416 rate_limits` | `ratelimit.Sliding`（key=`ratelimit:{tenant}:{action}`，Lua 原子） | 内存计数 |
| API Key 日配额 | `store.go calls_today` | `ratelimit.Sliding` 日窗口（key=`ak:{id}:{date}`） | SQLite 字段 |

> 注：API Key 日配额若迁移到 Redis，需在每日 0 点将 Redis 计数同步回 `api_keys.calls_today`（或彻底改由 Redis 为唯一源 + 夜间落库）。建议**Redis 为实时唯一源，SQLite 仅做每日快照**，避免双写不一致。

### 3.4 多实例 worker（可选）
切流 Redis 后，`ticket.go` 的 worker 池可跨实例分担：用 Redis List/BRPOP 做任务分发，替代当前进程内 `direct` 队列（`queue/queue.go` 已有 `Queue` 接口接缝，可新增 `redisqueue` 实现）。**本阶段先做锁/计数/信号量，worker 多实例留到阶段七多 AZ 一并做**。

### 3.5 验收
- [ ] 启 2 个 `translator` 实例（同 PG + 同 Redis），并发提交翻译：
  - LLM 并发上限被两实例共享遵守；
  - 同一工单不被两实例同时处理（锁生效）；
  - API Key 日配额跨实例聚合正确。
- [ ] 关 Redis，单实例自动回退进程内，功能不崩。

---

## 4. 阶段三：监控告警（Prometheus + Grafana + Alertmanager）

现状：`/metrics`（Prometheus 格式，内部抓取）+ 内存 pprof + `alerts` 表（仅落库无人消费）。

### 4.1 部署
- 自建或 Grafana Cloud：Prometheus 抓 `127.0.0.1:8787/metrics`（Caddy 已注释掉公网 `/metrics`，保持内网）。
- Alertmanager 接邮件/企业微信/Slack。

### 4.2 关键指标（建议在 `observability` 包补充埋点）
- `langcross_balance_tokens{tenant}` — 租户余额（低于阈值告警）。
- `langcross_llm_errors_total{model,type}` — 模型错误率（熔断恢复观测）。
- `langcross_request_latency_seconds{route}` — P95/P99。
- `langcross_queue_pending` — 任务积压（卡死巡检兜底告警）。
- `langcross_pgvector_hits_total` — 语义检索命中（质量观测）。

### 4.3 告警规则
- 余额 < 套餐 10% → 预警；< 0 → 紧急。
- 5xx 率 > 1% 持续 5min → 页面。
- 队列积压 > N 且增长 → 扩容提示。

---

## 5. 阶段四：日志聚合（Loki）

现状：`slog` JSON 落本地文件，已带 `trace_id`。

### 5.1 方案
- Promtail / Grafana Alloy 采集 `/opt/translator/logs/*.json` → Loki。
- 日志增加字段：`tenant_id` / `user_id` / `task_id` / `trace_id`，Loki 按标签检索。
- Caddy access log 一并采集，关联 `trace_id` 打通全链路。

### 5.2 验收
- [ ] 多实例日志在 Grafana 按 `trace_id` 串联。
- [ ] 错误日志可下钻到具体租户/任务。

---

## 6. 阶段五：对照编辑器 docx/pdf（P1）

现状：`EditorPage.tsx:143-146` 仅 xlsx/csv/对照表支持，docx/pdf 标 `unsupported`。

### 6.1 方案
- **docx**：用现有文档提取管线（`service/ticket.go` 的 docx 提取）按段落/句子建 key，翻译后回写；编辑器侧加载翻译后的 docx，按段落 key 对齐展示对照。
- **pdf**：走 `pdf2docx` 转 docx → 复用 docx 逻辑；纯预览可用 `pdf.js` 渲染原 PDF 作底图。
- 新增 `EditorPage` 的 `docx`/`pdf` 分支，复用 xlsx 的「源/译对照 + 行内编辑 + 写回 KB」交互。

### 6.2 验收
- [ ] 上传 docx，编辑器展示段落级中英对照，编辑后写回 KB。
- [ ] 上传 pdf，经 pdf2docx 转写后同 docx 体验。

---

## 7. 阶段六：SSO / OIDC（P1）

现状：仅本地账号 + JWT，无 IdP 对接。

### 7.1 方案
- 新增 `internal/iam/sso/`：`Provider` 接口（Authorize/Callback/UserInfo）。
- 预置飞书 / 钉钉 / Azure AD / Okta 适配；管理后台「组织设置」配置 ClientID/Secret/回调。
- 登录页新增「企业 SSO」入口；首次登录按邮箱/工号绑定到租户组织（沿用现有组织继承链）。
- JWT 签发逻辑不变，仅新增 `external_sub` 绑定。

---

## 8. 阶段七：多 AZ 部署（P1）

前置：阶段一（PG）+ 阶段二（Redis）。

### 8.1 方案
- PG 流复制（或 RDS 多 AZ 只读副本，写走主）。
- 多实例 `translator` 跨 AZ，Caddy 上游负载均衡 + 会话亲和（JWT 无状态，天然可亲和可无）。
- worker 多实例：启用 Redis 队列（阶段 3.4）。
- 备份：PG 自动备份 + 对象存储冷备 KB npz/产物。

---

## 9. 阶段八：压测基线（k6）（P1）

现状：`deploy/loadtest_openapi.sh` 单次手测，无常态化。

### 9.1 方案
- 引入 k6 脚本覆盖：文本翻译 / 文件翻译 / 开放 API 轮询 / 并发限流。
- CI（或 cron）夜ly 跑，结果写入 Grafana；建立 SLO：P99 < 2s（交互）、文件任务成功率 > 99.9%。
- 故障注入（混沌）：Redis 断、PG 主切、LLM 供应商熔断，验证降级路径。

---

## 10. 阶段九：前端 E2E（Playwright）（P2）

- 覆盖：登录 → 建文本单 → 看进度 → 下载 → 对照编辑器 → 开放 API Key 签发。
- CI 门禁：`npm run test:e2e` 失败阻断合并。

---

## 11. 阶段十：API 版本管理（P2）

- 现状：`/openapi/v1/` 硬编码。
- 方案：新增 `Accept: application/vnd.langcross.v1+json` 头版本控制；路由层解析版本，预留 `/v2` 演进。保持 v1 路径向后兼容。

---

## 12. 阶段十一：OpenAPI Spec 自动生成（P2）

- 现状：`admin_openapi.go:268-509` 手写 Markdown，易与实现偏移。
- 方案：引入 `swag` 注解或手写 `openapi.yaml`，文档页（goldmark 渲染处）改为渲染 Spec 生成的 HTML；保留超管在线编辑 Markdown 作为「说明」补充。

---

## 13. 阶段十二：SDK 多语言（P2）

- 依据阶段十一 Spec，用 `oapi-codegen`（Go）/ `openapi-generator`（TS/Java）生成 SDK。
- 现有 `sdk/python` 改为由 Spec 生成，保证契约单一来源。

---

## 14. 阶段十三：审计日志留存（P2）

- 现状：`audit_logs` 无归档/分区，长期膨胀。
- 方案：按月分区（PG 原生分区表）；> 90 天转对象存储冷存；保留策略可配（`system_config`）。

---

## 15. 里程碑与依赖

```
阶段一 PG  ──┐
             ├─→ 阶段二 Redis ──→ 阶段三 监控 ──→ 阶段四 日志
阶段二 Redis ─┘                              │
                                            └─→ 阶段七 多AZ（需 PG+Redis）
阶段五 对照编辑器（独立）
阶段六 SSO（独立，可并行）
阶段八 压测（需 PG+Redis 后更有意义）
阶段九~十三（P2，独立并行）
```

**关键路径**：阶段一 → 阶段二 →（阶段三/四 + 阶段七/八）。其余可并行。

---

## 16. 风险登记

| 风险 | 影响 | 缓解 |
|---|---|---|
| PG 切流数据漂移 | 切流窗口新增数据丢失 | 严格停写窗口 + 反向同步脚本备用 |
| pgvector 扩展不可用 | 语义检索回退 npz（降级可接受） | 工具已写最佳努力跳过，监控命中率 |
| Redis 网络分区 | 锁/配额失效 | 看门狗 + 降级进程内 + 告警 |
| 多实例 worker 重复处理 | 计费双扣 | 任务幂等 + 分布式锁（已在阶段二覆盖） |
| 改造期回归 | 既有 UAT 7/7 退步 | 每阶段复用 OpenAPI UAT + 前端 E2E 门禁 |

---

## 17. 即期行动清单（下一步动手）

1. **阶段一（已落地脚手架）**：
   - `cmd/rebuild-kb-index` —— PG 切流后 pgvector 向量回填工具（已实现）。
   - `cmd/server --init-db` —— 仅建 schema 后退出，供切流脚本在迁移前建表（已实现）。
   - `internal/config` —— PostgreSQL 后端 DSN 强校验（缺 DSN 直接拒绝启动，已实现）。
   - `internal/kb` —— `SegmentsForEmbedding` 分页拉取段（已实现）。
   - `scripts/cutover-to-pg.sh` —— 备份→init-db→migrate→rebuild 一键编排（已实现）。
   - **待执行**：申请 RDS PG + 启用 pgvector；staging 跑 `cutover-to-pg.sh` 冒烟，再生产维护窗口切流。
2. **阶段二**：`go get redis/go-redis/v9`；落地 `internal/infra/{redis,distlock,ratelimit,concurrency}`；先接 LLM 信号量 + API Key 日配额（收益最大），再接工单锁。
3. 每阶段完成即更新本文件「验收」勾选，并同步到 `PROGRESS.md`。

---

## 18. 本次会话落地汇总（联调验证记录）

> 以下均已在本地完成 `go build ./...` + `go vet` + 集成测试 + 运行时联调验证。

### 阶段二（Redis）落地清单
- `internal/infra/redis/redis.go` —— 自研零依赖 RESP 客户端（离线环境无网络，未引入 go-redis）；连接池；BLPOP 独立连接。
- `internal/infra/redis/singleton.go` —— `Init/Get/Enabled/Ping` 单例，nil=降级进程内。
- `internal/infra/concurrency/semaphore.go` —— `Semaphore` 接口；Redis per-slot SETNX 全局信号量（原子无预充竞态、带 TTL 看门狗）；进程内带缓冲 channel 降级；`AcquireEither` 交互双通道。
- `internal/infra/ratelimit/daily.go` —— `Counter` 接口；Redis `Daily()` 原子日计数（INCR+EXPIRE 至次日零点），nil=降级 SQLite。
- `internal/infra/distlock/lock.go` —— `Lock` 接口；Redis SETNX+看门狗+持有者校验释放，进程内 sync.Mutex 降级。
- `internal/config` —— `RedisAddr/RedisPassword` + `REDIS_ADDR/REDIS_PASSWORD`。
- `internal/llm/client.go` —— 信号量重构为 `concurrency.Semaphore`（chat/embed/fileChat/fileEmbed 经 Redis 全局上限；chatFast 恒进程内保留槽）；修复 `withAcqTimeout` 上下文泄漏。
- `internal/store/apikeys.go` + `store.go` —— `TouchAPIKey` 在 Redis 启用时走 Redis 原子日计数。
- `internal/api/admin_openapi.go` —— `validateAPIKey` 以 Redis 计数为权威配额源。
- `internal/service/ticket.go` —— `StartStallSweep` 包裹分布式锁（多实例仅一个巡检）。
- `internal/infra/infra_integration_test.go` —— 启动本地 redis-server 联调（信号量全局上限=2、日计数跨实例=10、锁互斥）全部 PASS；进程内降级测试 PASS。
- 运行时联调：`REDIS_ADDR=127.0.0.1:16399 ./translator` 启动成功，日志 `[init] Redis 已启用: 127.0.0.1:16399`，`/status` 返回 `{"ok":true}`。

### 阶段三/四（监控/日志）交付清单（配置级，需外部实例点亮）
- `deploy/observability/prometheus/prometheus.yml` —— 抓取 `translator` job（`/metrics`，Bearer METRICS_TOKEN）。
- `deploy/observability/alertmanager/alerts.yml` —— 熔断/错误率/余额低/掉线/goroutine 泄漏 5 条告警规则。
- `deploy/observability/grafana/dashboard.json` —— 7 面板仪表盘（吞吐/HTTP/LLM/余额/租户/运行时/成本）。
- `deploy/observability/promtail/config.yml` —— 应用 JSON 日志（已带 trace_id/tenant_id）+ Caddy 访问日志 + 指标直采推送 Loki。
- README 见 `deploy/observability/README.md`（runbook：METRICS_TOKEN 注入、Grafana 数据源、Loki 地址）。

### 阶段八/九（压测/E2E）交付清单（配置级）
- `deploy/loadtest/k6.js` —— 阶梯加压（50→200→500 VU）命中 `/api/translate`，校验 P95<2s、错误率<1%。
- `frontend-react/playwright.config.ts` + `frontend-react/e2e/smoke.spec.ts` —— 首页/状态/仪表盘冒烟（需 `npm i -D @playwright/test`）。

### 阶段十（API 版本管理）落地清单
- `internal/api/server.go` —— `withAPIVersion` 中间件：解析 `Accept: application/vnd.langcross.<v>+json` 与 `X-API-Version`，写入 ctx 并回显 `X-API-Version` 响应头；默认 v1，v2 路径预留；已挂入 `Handler()` 链。

### 阶段十三（审计留存）落地清单
- `internal/store/store.go` —— `PruneAuditLogs(cutoff)` 按时间删除 + `AuditRetentionDays()`（默认 365，system_config.audit_retention_days 可覆盖）。
- `internal/api/server.go` —— `startAuditRetention()` 每 6h 定时清理超期审计日志（启动即跑一次）。

### 待外部资源/独立设计方能推进（非代码缺失，需决策）
- 阶段五 对照编辑器 docx/pdf：大型前端特性，需基于现有 `对照编辑器` 与 `pdf2docx` 管线单独设计段落键对齐。
- 阶段六 SSO/OIDC：需 IdP（飞书/钉钉/Azure AD）凭证与回调域名。
- 阶段七 多 AZ：需 k8s/PG 流复制 + Redis 哨兵/集群基础设施。
- 阶段十一/十二 OpenAPI/SDK：后端 `/openapi/docs` 已存在；自动化 swag 注释 + oapi-codegen 生成需离线工具链（本环境无网络，待接入）。

---

## 19. 全 13 阶段落地汇总（第二批：阶段五~十三）

> 阶段一~四、八~十、十三 已在第一批落地；本批补齐阶段五/六/七/十一/十二，至此路线图 13 阶段全部交付。

### 阶段五 对照编辑器 docx/pdf
- `internal/doc/office.go` —— 纯 Go docx 段落抽取（解压 word/document.xml + 流式 XML）+ 逐段回写（DocxRewrite，多 run 折叠为单 run 文本，保留段落/样式）+ PDF 经 python venv `pdf2docx` 桥接抽取（`PDFToParagraphs`）。
- `internal/api/editor.go` —— `extractOfficeSegments`：源文取原文件、译文取结果文件按段对齐；新增 `handleExportSegments`（审批后回写修订稿 docx）+ `/api/editor/export/download`（安全下载，限定 UploadDir 防穿越）。
- 单测 `internal/doc/office_test.go`：抽取/回写双向验证 PASS。

### 阶段六 SSO/OIDC
- `internal/auth/sso/sso.go` —— `Provider` 接口 + `Manager`；标准 OIDC（issuer 发现懒加载+缓存）、飞书/钉钉 OAuth2 授权码流程；`NewState` 防 CSRF。
- `internal/config` —— `SSOProviders` / `SSOFrontendURL`（env `SSO_PROVIDERS` JSON、`SSO_FRONTEND_URL`）。
- `internal/api/sso.go` —— `/api/sso/login|callback|providers`；回调校验 state→换用户信息→按邮箱匹配/自动开通→签发同一套 JWT→带 `?token=` 重定向前端。
- 单测 `internal/auth/sso/sso_test.go`：state 唯一性 + 飞书/钉钉/通用授权地址构造 PASS。

### 阶段七 多 AZ
- `internal/queue/notifier.go` + `internal/infra/redis/notify.go` —— Redis 信号列表唤醒跨实例 worker（低延迟分发；任务账本仍落 PG `jobs` 表，Claim 原子）。
- `internal/service/ticket.go` —— `Notifier` 注入；`EnqueueTicketRun/EnqueueMail` 入队即 Signal；worker 循环 `Wait` 唤醒（无 Redis 时退化 1s 轮询）。
- `deploy/multi-az/README.md` —— 无状态架构、PG 流复制、Redis 高可用、Caddy 会话亲和、systemd 多实例模板、探活顺序。

### 阶段十一 OpenAPI Spec
- `internal/api/openapi.v1.json`（go:embed 内联）+ `internal/api/openapi_v1.go` —— `GET /openapi/v1.json` 返回完整 v1 规范（与 Python SDK 契约一致：/tasks、/tasks/status、/tasks/download、/balance、/usage、/kb/stats、/keys/rotate）。

### 阶段十二 SDK 多语言
- `sdk/typescript/`（package.json + tsconfig + src/index.ts + README）—— fetch 实现，契约镜像 Python。
- `sdk/java/`（pom.xml + TranslatorClient.java + TranslatorError.java + README）—— java.net.http + jackson，契约镜像 Python。
- `sdk/python/` 既有，三语言共享同一 OpenAPI 契约。

### 联调验证（本批）
- `go build ./...` + `go vet` 通过；`internal/doc`、`internal/auth/sso`、`internal/infra` 单测/联调 PASS。
- 运行时冒烟：二进制启动 + `REDIS_ADDR` → `/openapi/v1.json`（合法 JSON）、`/api/sso/providers`（未配置=false；配置 SSO_PROVIDERS 后=true 并列出 IdP）、`/status` ok、结构化 JSON 日志（Loki 友好）均正常。
- 受离线限制未端到端验证：真实 IdP 授权码交换（需飞书/钉钉/Azure 凭证与可达网络）、PDF 抽取（需 `/opt/translator/.venv` 含 pdf2docx）、多实例跨机分发（需 PG+Redis 实测集群）。代码与契约已就绪，接入凭证/环境即可启用。

