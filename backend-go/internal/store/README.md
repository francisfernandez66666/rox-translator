# internal/store — 域索引与维护约定

> ★ 改造 2（2026-09-17）新增。本包是 SaaS 平台数据访问层（`store.Store` 单一入口），
> 当前 **74 个文件 / 约 16.8k 行（含测试）**，被 `internal/api/` 90+ 文件直接引用。
>
> 结论（务实路线）：**不做一次性物理拆包**（改动面 >3000 行且无行为收益），
> 改为「文件归组 + 冻结规则 + 通用能力下沉」控制复杂度增长；
> 域拆包按引用面从小到大试点推进（见文末「第三步」）。

---

## 一、域索引（按业务域归组）

新增文件时请先在下表找到归属域，用现有前缀命名；**不要往 `store.go` 继续塞业务方法**。

### 平台核心 / 租户 / 组织 / 用户
| 文件 | 职责 |
|---|---|
| `store.go` (900) | Store 核心：连接装配、迁移编排（`New()` 里逐项幂等 migrate）、通用 Row/扫描辅助 |
| `tenants.go` / `orgs.go` / `users.go` | 租户 / 组织 / 用户 **薄委托层**（历史拆分残留，方法体直通 store 内实现） |
| `orgmigrate.go` | 组织迁移（部门树重建、成员归属迁移） |
| `scim.go` | H10 SCIM 2.0 组织/用户同步存储层 |
| `gdpr.go` | GDPR 数据主权（导出 / 删除请求权） |

### 认证与访问控制
| 文件 | 职责 |
|---|---|
| `apikeys.go` | 租户开放 API Key（`api_keys`，签发即加密落 `key_enc`） |
| `invite.go` | 自助注册邀请码（`invite_codes`） |
| `ratelimit.go` | 频率护栏（`rate_limits`） |
| `audit.go` | 审计日志（`audit_logs`） |
| `crypto.go` | 静态加密**薄委托** → 实现在 `internal/secret`（见第三节） |

### 计费 / 商业（★ 冻结域）
| 文件 | 职责 |
|---|---|
| `billing.go` (**2201**) | 计费域：订单 / 支付 / 退款 / 双桶台账（额度桶 + 永久余额）/ 订阅首月半价 |
| `my_billing.go` | F8 自服务账单（客户侧积分口径） |
| `reconcile.go` | F9 三表勾稽（orders ↔ payments ↔ 余额/退款流水） |
| `packages.go` | 商业包（`packages`）与租户套餐归属、过期处理 |
| `quota_grants.go` | 额度发放台账（幂等发放、并发防重） |
| `quota_org.go` | 部门预算（四期增强）与双预算墙判定 |
| `usdt.go` | USDT 收款（TRC20/ERC20/BEP20，M1 人工核销 + M2 链上轮询） |
| `balancehook.go` | 余额变更钩子（P1 多实例闭环） |
| `webhooks.go` (547) | Webhook 回调与投递逻辑（HMAC 签名、重试策略、死信队列） |
| `referral.go` (549) | 邀请裂变（二级分销、返佣归因） |

### 知识库 / 翻译记忆（★ 冻结域）
| 文件 | 职责 |
|---|---|
| `kbpackages.go` (**1371**) | 知识库包与条目（五档可见范围继承链、包级权限矩阵入口） |
| `kbgrants.go` | H3 包级权限矩阵：按用户授权（读/写/管理三级） |
| `kbscrape.go` (1105) | 行业包 / 语言文化包自动采集与审批 |
| `kb_rewards.go` | 知识库上传奖励 |
| `tmreview.go` | TM 自闭环待审池 |

### 工单 / 翻译产物
| 文件 | 职责 |
|---|---|
| `tickets.go` (435) | 工单主体（`tickets` / `ticket_state`）：★ 改造 4/5 新增 `quality_flagged` / `qa_errors` / `qa_warnings` 列 |
| `ticketfiles.go` | 工单多文件（`ticket_files`） |
| `ticketretention.go` | 产物保留期（`result_expires_at` / `expire_notify`） |
| `artifacts.go` | 产物 / 上传件归属登记（评审整改 C1） |
| `edits.go` | 对照编辑器数据层 |
| `evals.go` | 翻译质量评估记录（`eval_records`） |
| `tasks.go` | 任务中心 |

### 运营 / 增长 / 通知
| 文件 | 职责 |
|---|---|
| `alerts.go` | S9 监控告警中心（`alerts`） |
| `notifications.go` | 站内信（`notifications`） |
| `feedback.go` | 用户反馈（`feedbacks`） |
| `funnel.go` | S4 增长归因漏斗（注册→激活→耗尽→首购→续费） |
| `s7_growth.go` | S7 商业化触达序列数据层 |
| `systemconfig.go` | 系统配置（`system_config`，键值 + 密文 key） |
| `backup.go` | 数据库备份实现 |
| `dberr.go` | 驱动层错误脱敏（sqlite/postgres 原始错误归一） |

---

## 二、冻结规则（★ 强制）

1. **`billing.go`（2206 行）与 `kbpackages.go`（1371 行）只减不增**（行数口径＝含注释的非测试行数，规则本身指「不追加新方法」；2026-09-22 实测校准）。**
   新方法一律按域新建文件（如 `billing_refund.go` / `kbpackages_acl.go`），
   或优先归入上表已存在的窄域文件。**禁止**在这两个文件里追加新方法。
2. **`store.go` 不承接业务方法。** 它只保留连接/迁移编排与真正的通用工具；
   任何业务语义的方法都应落到对应域文件。
3. **通用能力下沉，不反向依赖。** 与业务表无关的工具（加密、错误脱敏、Row 动态扫描等）
   放 `internal/secret`、`internal/db` 等基础包，store 通过薄委托引用——
   **禁止**让基础包 import `internal/store`。
4. **迁移统一走幂等补列。** 新增列用 `db.EnsureColumns`（双方言幂等，老库启动自动升级），
   仿 `store.TicketQualityFlaggedMigrate()`；禁止写一次性 `ALTER TABLE` 直执行脚本。
5. **改动必须过闸门**：`go test -race ./...` 全绿 + `bash scripts/uat/run_uat.sh` 全绿
   （对账类断言最敏感，天然验证无行为回归）。

---

## 三、通用能力下沉记录

| 能力 | 现位置 | 说明 |
|---|---|---|
| 静态加密（AES-256-GCM、`enc:v1:` 前缀） | `internal/secret` | ★ 改造 2 自 `store/crypto.go` 下沉。原实现寄生 store 包，导致「只读一个密文配置」也要依赖整个业务存储层（如 assist-server 需解密 `assist_admin_token`）。现 store 保留同名薄委托，**既有 90+ 调用点零改动**；`internal/assist/*` 直接复用基础包，不反向依赖业务存储层 |

---

## 四、第三步·域拆包（评估后执行，本次未做）

试点顺序按**引用面从小到大**：

1. `webhooks`（547 行）→ `internal/biz/webhooks`
2. `referral`（549 行）→ `internal/biz/referral`

方式：拆独立包 + store 保留薄委托（同 `crypto.go` 手法，调用点零改动）。
跑通后再评估 billing 拆分——届时需同步 `internal/api/billing_api.go`、`admin_billing.go` 引用面。

验收口径同第二节第 5 条。
