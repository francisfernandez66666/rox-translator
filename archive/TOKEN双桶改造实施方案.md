# 技术方案 · Token 双桶台账改造（Commit A–D 实施依据）

> 版本 v1.1 ｜ 2026-08-25 ｜ 上游口径文档：《商业化白皮书-翻译助手.md》
> **状态：✅ 已全部落地**（Commit A `614fe8f` / B `bcdf7b6` / C `765d21d` / D 邀请裂变 `194211c`+`7225bfa`；本文转为设计存档）
>
> 落地时相对 v1.0 的三处修正：
> 1. 套餐订单 `orders.amount_tokens` 恒为 0，paid/increment 分流额度按「包内句数 × estimate_tokens_per_sentence(默认500)」折算，不可直接取订单金额字段；
> 2. `QuotaGrantMigrate`/`ReferralMigrate` 必须挂载于 `Store.New`（初版漏挂导致新库缺表，已修复）；
> 3. 邀请付费奖励金额优先级：system_config `inviter_paid_reward_tokens` → env `INVITER_PAID_REWARD_TOKENS` → 默认 500000。
> 本文为开发实施依据：数据模型、扣减算法、写入路径、定时任务、参数与验收。

---

## 一、目标

1. 额度模型由「单桶余额」升级为「**发放台账 + 永久余额**」双部分结构，支持：
   - 带到期日的体验包 / 订阅额度（可多行叠加、各自独立过期）
   - 永久有效的充值余额
2. 扣减顺序：**未过期额度行（按到期日升序）→ 永久余额**，同事务原子完成，绝不扣负。
3. 写入路径全部收敛到白皮书规则：
   - 注册礼包：300,000 token / **14 天**（入台账）
   - paid 订阅订单确认 → 台账发放（t+30 天）
   - increment 订单确认 / 邀请永久奖励 → `balance += n`（永久桶）
4. 运营参数全部 system_config 化，后台可视化可调。

## 二、数据模型变更

```sql
-- 新增：额度发放台账（幂等建表）
CREATE TABLE IF NOT EXISTS quota_grants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    kind TEXT NOT NULL DEFAULT 'trial',     -- trial(体验/邀请叠加) | plan(t+30订阅)
    total INTEGER NOT NULL,                 -- 发放总额
    left INTEGER NOT NULL,                  -- 剩余（扣减在此核销）
    expires_at TEXT NOT NULL,               -- 到期时间 RFC3339；过期行跳过不删
    source TEXT DEFAULT '',                 -- register | order | invite | admin
    ref_id INTEGER DEFAULT 0,               -- 关联 order_id / feedback_id 等
    created_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_qg_tid_exp ON quota_grants(tenant_id, expires_at);

-- 语义收窄（不改数值、不加列）：
balance_accounts.balance  -- 从此仅表示「永久余额」；历史存量即永久余额，无需迁移

-- 后续 Commit C 预留（本阶段不建）：
-- users.ref_code / users.referred_by / referral_rewards
```

## 三、扣减算法（store 层新方法）

```go
// DeductWithGrants 双部分顺序扣减（事务）：
//   ① 未过期 grants 按 expires_at ASC 逐行核销（可拆分多行）
//   ② 不足部分从 balance_accounts.balance 原子扣减（balance>=remain 条件更新）
// 任一环节不足 → 整体回滚并返回 ErrInsufficientBalance
func (s *Store) DeductWithGrants(tid int64, tokens int64) error
```

要点：
- 全程 `BEGIN IMMEDIATE` 事务，规避 SQLITE_BUSY 下的并发双花
- 过期行不删除（保留对账痕迹），仅在查询时按 `expires_at > now` 过滤
- 旧 `Deduct` 保留但标记 Deprecated；运行时唯一调用点（RecordUsage 内部）切换至新方法

## 四、写入路径改造

| 场景 | 现状 | 改造后 |
|---|---|---|
| 注册礼包 | `Charge(tid, trialTokens)` 入永久桶（trialTokens 取自旧配置） | 读 `free_trial_tokens` / `free_trial_days`（默认 300000 / 14）→ `CreateGrant(kind='trial')` |
| paid 订单确认 | 确认后统一 Charge 入永久桶 | 按 package.ptype 分流：`paid` → CreateGrant(kind='plan', expires=now+30d)；`increment` → 保持 Charge |
| 邀请奖励（后续 Commit C/D） | — | 预留 `CreateGrant(source='invite')` 与 `AddPermanent(n)` 两入口 |

## 五、定时任务（并入现有巡检 tick）

```go
// 每 tick 追加执行：
CloseStalePendingOrders() // UPDATE orders SET status='cancelled'
                          // WHERE status='pending' AND created_at < now-15min（键 order_pending_timeout_min）
LowBalanceSweep()         // 对 enforced 租户：剩余合计 < 低额阈值时发一次去重告警
```

- 关闭阈值键：`order_pending_timeout_min`（默认 15）
- 低额阈值键：`low_balance_alert_tokens`（默认 100000）；去重策略：alerts 表存在同租户同类型且 created_at 在 24h 内的记录则跳过

## 六、参数键清单（system_config，全部已落库默认值）

| 键 | 默认 | 用途 |
|---|---|---|
| free_trial_tokens | 300000 | 新客体验 token 数 |
| free_trial_days | 14 | 体验有效天数 |
| order_pending_timeout_min | 15 | pending 订单自动关闭 |
| low_balance_alert_tokens | 100000 | 低额告警绝对阈值 |
| invite_reward_tokens | 300000 | 每邀 1 人·邀请者体验增量 |
| invite_extend_days | 14 | 每邀 1 人·邀请者时长叠加天数 |
| inviter_paid_reward_tokens | 500000 | 受邀者首笔付费套餐→邀请者永久 token（env INVITER_PAID_REWARD_TOKENS 可兜底） |
| billing_enforced | false | 强制计费总开关（既有）|
| tm_review_threshold | 100 | TM 自闭环候选阈值（既有）|

## 七、API 兼容性

- `/openapi/v1/balance`、管理端余额接口返回体**新增**字段（向后兼容）：`sub_grants_left`（未过期台账合计）、`permanent_balance`
- 原 `balance` 字段语义 = 永久余额；前端商业化页据此渲染双视图

## 八、验收清单

1. 新注册租户：quota_grants 出现 300000/14d 行；balance=0
2. 消耗顺序：构造「快过期行 + 大额行 + 永久余额」三段，验证先扣近到期行、拆行核销、最后动永久
3. 过期 grant 自动跳过且不报错
4. 余额/额度不足：enforced=false 仅告警放行；true 返回 ErrInsufficientBalance
5. pending 订单 16 分钟后被自动置 cancelled
6. paid 订单确认 → 台账出现 t+30 的 plan 行；increment 订单确认 → balance 增加
7. 回归：既有文本/文件工单全链路正常（扣减入口切换无感）

## 九、实施与回滚（已完成）

- Commit A（614fe8f）：§二+§三+§四（注册礼包切换 + DeductWithGrants 切换）——核心原子提交
- Commit B（bcdf7b6）：§五 定时任务 + §六 参数默认值落库
- Commit C（765d21d）：订单确认 ptype 分流（paid→台账 / increment→永久）
- Commit D（194211c + 7225bfa）：邀请裂变存储层/API/钩子/前端（含三处存量缺陷修复，见文件头说明）
- 回滚：revert 提交即可；quota_grants/referral_rewards 均为增量新增表，不影响旧路径数据
