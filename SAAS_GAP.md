# 翻译助手 SaaS 化销售差距分析

> ⚠️ **历史文档**：差距项已全部补齐并上线，现行架构以 `PROGRESS.md` 与 `重构方案*.md` 为准。


> 日期：2026-08-17 ｜ 更新：2026-08-20（**全部补齐并上线**）
> 目的：对照《车企智能翻译平台·产品方案书》与 PLAN.md，找出"能卖钱"缺的能力并补齐。
> 状态：**P0/P1/P2 全部完成**，生产已上线。

---

## 一、已完成补齐 ✅（对照差距逐项落地）

### 🔴 P0（不解决无法收款/开票/上线）— 全部完成

| 能力 | 实现 | 状态 |
|------|------|------|
| **租户体系** | `tenants` 表 + 全部业务表 `tenant_id` + 路由级租户隔离中间件（RBAC 之上叠租户维度） | ✅ |
| **计费与计量** | `balance_accounts` + `usage_ledger`（按租户×任务类型×供应商/模型计量）+ `rate_card`（语言×供应商×倍率） | ✅ |
| **支付与发票** | `orders`/`payments`/`invoices`；MVP 线下转账 + admin 手动充值 + 发票开具 | ✅ |

### 🟠 P1（上线后很快撞墙）— 全部完成

| 能力 | 实现 | 状态 |
|------|------|------|
| **多 LLM 供应商 + 成本核算** | usage_ledger 记录请求级 provider/model；rate_card 供应商维度单价；`model_routes` 权重路由 + 降级链 | ✅ |
| **租户级策略/白名单/隔离** | 租户自服务上传 KB、租户配额（QPS/并发/每日上限/余额停服）、租户 BYOK 模型路由 | ✅ |
| **审计与合规留痕（ISO 17100）** | `audit_logs` 含 before_val/after_val 结构化前后值轨迹 + 操作人/IP + CSV 导出（按租户隔离） | ✅ |
| **配额与限流** | 租户级 QPS/并发/每日 token 上限/余额不足即停 | ✅ |

### 🟡 P2（销售增长与留存）— 全部完成

| 能力 | 实现 | 状态 |
|------|------|------|
| **试用与邀请机制** | 租户自助注册（可开关）+ 邀请码管理 + 试用额度 + 邀请码入口 | ✅ |
| **数据看板与用量报表** | 用量看板（当前余额/累计/供应商数卡片 + 近 7 日趋势柱状图 + 任务类型/供应商横向条形图 + 明细表） | ✅ |
| **API 对外开放 + API Key** | `/openapi/v1/*`（translate/kb/stats/billing/usage/apikey/rotate）+ Key 签发/轮换/状态 + `/openapi/docs` + 调用计量 | ✅ |
| **告警与运维监控** | 看门狗（余额阈值/模型熔断/错误率>40%）→ alerts 表 + 告警面板 + Prometheus `/metrics` | ✅ |

## 二、补齐过程中的附加决策

| 决策 | 说明 |
|------|------|
| 数据库 SQLite | 弃 PG，单文件 WAL + 内置多租户，适配 1.6G 内存服务器 |
| 计费默认不强制 | `system_config billing_enforced=1` 才扣余额停服；未强制仅 `LogUsage` 留痕 |
| 计量口径 | 文本按源 rune 字符数、文件按段数、工单按源字符数；统一 task_type="translate" |
| 模型路由 | `model_routes` JSON（ProviderConfig 权重数组），主模型失败按权重降序降级；admin 热更新 |
| 审计轨迹 | user_update/billing_config_save/tenant_quota_save 等关键操作记录前后值 JSON |
| GDPR | 租户数据导出 JSON（9 类数据）+ 清除（级联置零/删除）；禁止清除租户 1 |
| 开放 API 计量 | 与内部翻译同口径，按请求级 provider/model 落 usage_ledger |

## 三、剩余商业化项（见 COMMERCIAL_TODO.md）

- 二期在线支付（支付宝/微信）
- 密码 bcrypt 加固、JWT 密钥环境变量化
- webhook 回调、优雅停机、i18n
- 商业物料（LICENSE/SLA/定价卡/DPA）

## 四、一句话总结

从"单租户功能清单"到"可上线销售的产品"的三大支柱（**多租户隔离、计费计量、支付/发票**）连同租户自服务、开放 API、用量报表、审计导出、告警监控已全部落地，`https://translator.quant-trading.top` 生产运行中。