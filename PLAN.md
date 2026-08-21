# 车企智能翻译平台 · 实施计划（P0 MVP + SaaS 化）

> 文档版本：v2.1 ｜ 更新：2026-08-21
> 状态：**P0 MVP + SaaS 基础层已全部完成并上线**（阶段 1-9 + SaaS 补充项全部完成，见 PROGRESS.md）；**彻底 SaaS 化 10 项需求已全部完成**（见 SAAS_ROADMAP.md）
> 下一阶段：真实支付商户号接入 + 商业化运营
> 代码基础：`backend-go/`（Go 后端，单二进制）＋ `frontend/`（Vue3 + TS）
> 数据库：**SQLite**（单文件，WAL，内置多租户），知识库 `tm.sqlite3` + 向量 `tm_embeddings.npz`
> 部署目标：阿里云首尔服务器 43.108.86.140（`/opt/translator/`，与旧项目 quant 完全隔离）
> 访问地址：`https://translator.quant-trading.top`（Caddy 反代 → 127.0.0.1:8787）

---

## 一、项目目标

在现有 Go 翻译后端基础上，改造为**车企智能翻译 SaaS 平台**，已完成：

- 中文文本一键翻译 16+ 语言（Hunyuan-MT-7B 主模型，多供应商路由降级）
- 汽车翻译记忆库命中（3300+ 条）+ 术语全语种统一 + 来源/相似度标注
- Word/PPT/Excel 批量翻译并下载（含 PPT 自动缩放、docx 带属性写回）
- **多租户体系（Tenant，单实例 shared SQLite + 行级 tenant_id 隔离）** ✅
- **三级角色 user / approver / admin + JWT + RBAC** ✅
- 知识库三级包分层（企业包 / 行业包 / 语言文化习惯包，树形继承）✅
- 知识库四层（L1 术语 / L2 TM / L3 安全句 / L4 AI 碎片）✅
- AI 初翻 + 审校 Agent + ConstraintGate + 审批台 + 自迭代 ✅
- **evals 评估**（LLM-as-Judge 5 维加权评分，可配置抽样率）✅
- **管理后台**（数据治理 / 系统健康 / evals / 模型路由 / 策略 / 流程引擎 / 用户 / 计费 / 配额 / 发票 / 邀请码 / 审计 / 告警 / 用量看板）✅
- **计费与计量**（token 余额 / usage_ledger / rate_card 供应商×语言定价 / 配额限流 / 余额停服）✅
- **支付与发票**（MVP 线下转账 + admin 充值）✅
- **租户自服务 + 开放 API**（自助注册/邀请码/试用、API Key 签发/轮换、开放 API、用量报表、审计导出、GDPR 导出/清除）✅
- **系统可观测性**（Prometheus `/metrics`、看门狗告警、熔断/错误率监控）✅

## 二、最终决策（已落地）

| 决策项 | 选择（已实现） |
|--------|------|
| 改造范围 | 完整 P0 MVP + **SaaS 化基础层**（SAAS_GAP.md 全部补齐） |
| 代码基础 | Go 后端（backend-go/），单二进制，启动参数 `-addr/-frontend/-kb/-kbdb` |
| 数据库 | **SQLite**（单文件 + WAL + busy_timeout；自动建表 + 幂等列迁移 `migrateColumns`） |
| LLM | 多供应商路由（默认 SiliconFlow Hunyuan-MT-7B 主模型，GLM-4-9B 兜底），权重路由 `model_routes` 热更新 |
| 知识库 | `tm.sqlite3`（SQLite）+ `tm_embeddings.npz`（1024 维向量，L2 归一化余弦检索） |
| evals | LLM-as-Judge 5 维加权评分；不合格自动重翻→送校正；无 Judge Key 跳过；可配置抽样率 |
| 角色层级 | user / approver / admin 三级 |
| 多租户 | 单实例多租户：shared schema + 全部业务表 `tenant_id` + 路由级租户隔离中间件（RBAC 之上叠租户维度） |
| 计费模式 | 充值 token 余额不过期；按租户×任务类型计量（usage_ledger）；rate_card 单价表（语言×供应商×倍率） |
| 支付 | MVP 线下转账 + admin 手动充值；二期接在线支付 |
| 配额 | 租户级 QPS / 并发 / 每日 token 上限 / 余额不足即停 |
| 计量口径 | 文本按源 rune 字符数；文件按段数；工单按源字符数；请求级记录 provider/model 用于成本核算 |
| 开放 API | `/openapi/v1/*`：translate / kb/stats / billing/usage / apikey/rotate；文档 `/openapi/docs` |
| 审计 | `audit_logs` 含 before_val/after_val 结构化轨迹；CSV 导出（ISO 17100 对齐话术） |
| 监控 | Prometheus `/metrics` + 看门狗告警（余额阈值/模型熔断/错误率>40%） |
| 合规 | 租户数据导出 JSON + 清除（GDPR） |
| 隔离 | 独立目录 `/opt/translator/`、独立端口、独立服务，不动旧项目 quant |

## 三、架构总览（实际落地）

```
阿里云服务器 43.108.86.140（独立于旧项目 quant）
┌────────────────────────────────────────────────────────┐
│ Caddy → https://translator.quant-trading.top           │
│    ↓ reverse_proxy → 127.0.0.1:8787                    │
│ Go 单体（单二进制 backend-go/cmd/server）              │
│    ├─ 编排层: internal/orchestrator（FlowDef 流程引擎） │
│    ├─ 业务层: engine（翻译） / kb / gate / culture /   │
│    │          fileproc（docx/pptx/xlsx） / evals       │
│    ├─ 平台层: api（HTTP+SSE） / auth(JWT+RBAC) /       │
│    │          tenant / billing(计量+余额+配额+发票) /  │
│    │          openapi(API Key) / store / watchdog      │
│    └─ 数据层: SQLite（单文件）                         │
│       users/tenants/tickets/kb_packages/kb_entries/   │
│       balance_accounts/usage_ledger/rate_card/orders/  │
│       invoices/payments/api_keys/invite_codes/         │
│       eval_records/audit_logs/system_config/alerts     │
└────────────────────────────────────────────────────────┘
```

**流程链路**：
```
用户建工单/发文本 → KB 匹配（企业包→行业包，包内 L1术语/L2TM/L3安全句/L4碎片）
  → AI 初翻（术语注入、缩句双稿）→ evals 评估 → 不合格重翻/送校正
  → 审校 Agent → evals → ConstraintGate 硬校验
  → 语言文化习惯包输出闸门（反查译文）→ 人工审批台（批准/编辑/驳回）
       → 批准：写 KB（按包写入）→ 自迭代
       → 驳回：驳回意见拼入提示词自动重启初翻+校对（workflow 内部循环，非人工重翻）
每次 LLM 调用按请求级记录 provider/model → usage_ledger → 扣余额
```

## 四、实施阶段（全部完成 ✅）

| 阶段 | 内容 | 状态 |
|------|------|------|
| 阶段 1 | 数据层（SQLite + tm.sqlite3 + npz 向量加载） | ✅ |
| 阶段 2 | Schema（19 张表，含 SaaS 化表，幂等迁移） | ✅ |
| 阶段 3 | Go 数据层（store 包：建表/迁移/CRUD/事务） | ✅ |
| 阶段 4 | 平台层（auth / tenant / billing / audit / llmproxy / openapi / systemconfig） | ✅ |
| 阶段 5 | 业务层（kb / culture / engine / gate / evals） | ✅ |
| 阶段 6 | 编排层（orchestrator FlowDef + workflow + 步骤启停） | ✅ |
| 阶段 7 | API + 前端（登录/工单/审批/KB/evals/系统/管理后台/租户自服务） | ✅ |
| 阶段 8 | evals 评估器（5 维评分 + 抽样 + 重翻/校正） | ✅ |
| 阶段 9 | 部署上线 + 端到端验证（多租户隔离 + 计费闭环 + KB 包 + 开放 API） | ✅ |
| SaaS 补充 | 全部 A/B 组（见 SAAS_GAP.md 与 PROGRESS.md） | ✅ |

## 五、需要用户提供的（现状）

- [ ] 二期在线支付（支付宝/微信）Key —— 当前 MVP 线下转账 + admin 充值
- [ ] 语言文化习惯包内容（政治文化避雷词清单）持续补充
- [ ] 生产管理员初始密码是否修改（生产 taadmin/taadmin123）

## 六、风险与应对（当前状态）

| 风险 | 应对 | 状态 |
|------|------|------|
| SQLite 并发写 | WAL + busy_timeout + Store 互斥 | ✅ 已处理 |
| 多租户越权 | 全部查询强制 tenant_id + 租户隔离中间件 + 越权测试 | ✅ 已处理 |
| LLM 供应商故障 | 多供应商权重路由 + 熔断（5 次/1800s 冷却）+ 看门狗告警 | ✅ 已处理 |
| 计费争议 | 请求级 provider/model 计量，驳回重翻计入租户用量 | ✅ 已处理 |
| 自助注册滥用 | 试用额度 + 注册开关 + 配额限流（QPS/并发/每日上限/余额停服） | ✅ 已处理 |
| 数据安全 | 租户数据导出 + GDPR 清除；审计 CSV 导出（ISO 17100 对齐） | ✅ 已处理 |
| 可观测性 | `/metrics`（HTTP/翻译/熔断/错误率/租户规模）+ 告警面板 | ✅ 已处理 |

## 七、里程碑（实际产出）

| 阶段 | 产出 |
|------|------|
| 阶段 1-2 | SQLite 就绪 + 知识库迁移完成 + 19 张表 Schema（含 SaaS 化表） |
| 阶段 3-6 | Go 后端全量改造（RBAC + 多租户隔离 + KB 包分层 + 计量计费 + 配额 + 流程引擎 + evals） |
| 阶段 7 | API + 前端改造（登录/工单/审批/租户/余额用量/API Key/管理后台/用量看板图表） |
| 阶段 8 | evals 评估器可用（含抽样率） |
| 阶段 9 | 部署上线 + 端到端验证（三级角色 + 多租户隔离 + 计费闭环 + 开放 API + 监控） |

---
## 八、SaaS 化补充说明（来源 SAAS_GAP.md）

完整差距分析见 `SAAS_GAP.md`。已全部补齐：
- **P0**：多租户隔离、计费计量、支付发票（MVP 线下充值）
- **P1**：租户配额、租户自服务 KB、审计导出、供应商成本核算 + 模型路由
- **P2**：自助注册试用、开放 API+Key、用量报表、告警监控、GDPR 导出/清除

## 九、相关文档

| 文档 | 说明 |
|------|------|
| `PROGRESS.md` | 全量实施进度与关键决策记录 |
| `SAAS_GAP.md` | SaaS 销售差距分析与补齐对照 |
| `COMMERCIAL_TODO.md` | 商业化待办清单（已勾选项 = 已完成） |
| `CLOUD_VALUE.md` | 云端系统能力价值一览表 |
| `部署指南.md` | 生产部署/运维指南 |