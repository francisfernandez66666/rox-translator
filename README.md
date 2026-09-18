# 能言 SaaS

> 面向企业与团队的 AI 翻译协作平台，文件翻译、对话翻译、知识库一体化，积分预充值计费（术语硬闸 + 原版式交付 + Unicode 归一化敏感词合规闸）。

---

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 单二进制，PostgreSQL + pgvector，REST API |
| 前端 | React 18 + LangCross 自研组件库（src/ui/langcross，纯黑设计系统；TDesign 仅存量过渡共存）+ React Router + Zustand（Vite 构建，React.lazy 代码分割） |
| 桌面端 | `./build.sh` 构建 macOS `.app` |
| 生产部署 | systemd + Caddy（反向代理 + 自动 TLS） |
| SDK | Python / TypeScript / Java |

---

## 快速开始

```bash
git clone <仓库地址>
cd 能言
./start.sh        # 编译 Go 后端并启动 http://127.0.0.1:8787
# ./start.sh -f   # 同时启动 vite 热更新前端（:5174）
```

首次运行会自动初始化数据库。启动后请访问控制台并按提示修改默认管理员密码。

手动构建示例：

```bash
cd frontend-react && npm install && npm run build
cd ../backend-go && go build -o translator-server ./cmd/server

# 本地开发（SQLite，零依赖）
./translator-server -addr 127.0.0.1:8787 -frontend ../frontend-react/dist -kbdb data/dev.db

# 生产形态（PostgreSQL + pgvector，业务库与知识库同库）
DB_DRIVER=postgres DB_DSN='postgres://user:pass@127.0.0.1:5432/translator?sslmode=disable' \
  ./translator-server -addr 127.0.0.1:8787 -frontend ../frontend-react/dist
```

---

## 目录结构

```
能言/
├── backend-go/        # Go 后端源码（单二进制；含 AI 顾问子服务 cmd/assist-server）
│   ├── cmd/server/          # 主服务（翻译 SaaS 全部 API + 前端托管）
│   ├── cmd/assist-server/   # AI 销售/客服助手（★ 2026-09-17 自 ai-assist/ 并入同 module）
│   └── internal/assist/     # 助手引擎/LLM 降级链/独立 SQLite 存储/管理台（数据隔离保留）
├── frontend-react/    # React 前端（自研组件库 src/ui/langcross）
├── deploy/            # systemd、Caddy、冒烟/压测/演练脚本
├── scripts/           # 数据库初始化、切流与 UAT 自动化脚本（scripts/uat）
├── extension/         # Chrome 自托管扩展（划词翻译，开发者模式加载）
├── vscode-extension/  # VS Code 插件（侧边栏翻译 + 术语检索，零构建）
├── sdk/               # OpenAPI SDK（Python/TypeScript/Java）
├── data/              # 运行时数据（本地 SQLite 开发库 / 向量缓存文件；生产业务数据在 PostgreSQL）
├── AGENTS.md          # 工程硬约定（store 冻结规则、观测/密钥/方言约定、闸门口径）
├── start.sh           # 本地开发一键启动
└── build.sh           # macOS .app 构建
```

> ★ 2026-09-17 架构收敛：原独立 module `ai-assist/`（独立 go.mod/go.sum）已并入 `backend-go`
> 同一 module，统一构建/观测/CI/鉴权链路；**数据隔离保留**（助手仍用独立 SQLite，
> 不并入业务库）。管理台 Token 由主后台 `/api/admin/assist/token` 下发，免手填。

---

## 核心能力

- **多格式文件翻译**：docx/pptx/xlsx/pdf/txt/csv/md 输出译文文件；srt/vtt/json/yaml 等以对照表（xlsx）形式交付；支持多目标语言打包下载
- **工单文件双模式**：还原文件模式（默认，原格式交付 + 纯文案 .md 兜底附加物，版式回写失败自动降级为 .md 交付不整单失败）/ 纯文案模式（anydoc 本地提取，额外准入 doc/xls/ppt 老格式、odt/ods/odp、rtf/epub，交付译文 .md）
- **对话式翻译**：聊天交互、上下文审校、风格指令
- **知识库与翻译记忆（KB/TM）**：pgvector 向量检索；五档可见范围（部门 > 跨部门 > 企业 > 行业 > 通用语言习惯包）按优先级/相似度排序；组织继承链与部门级隔离
- **行业字典**：超管在后台「知识库 → 🏭 行业管理」面板在线创建/维护行业（code 唯一、启停、删除带引用保护）；全站行业下拉动态拉取（注册/租户表单/数据采集）
- **RAG 术语遵循硬闸**：源文命中 KB L1 术语时，译文必须体现规定译法，否则硬闸拦截并附 KB 提示自动重译；支持约束解码的模型走 Constrained Decoding 双轨（事前注入约束，不支持的自动降级事后闭环）
- **品牌名统一译法**：知识库可独立配置品牌名（module=brand）；对话与文件翻译在翻译前做品牌保护并翻后归一化
- **租户与组织隔离**：多租户、角色权限、部门预算
- **品牌定制与登录页布局**：按子域名解析租户品牌（名称/Logo/背景图）；登录页支持全屏背景或左右分栏
- **积分计费**：客户侧统一积分口径（1 积分=300 内部计量 token，内部双桶台账不变：发放额度+永久余额）；预充值永久有效、订阅首月半价（注册 30 天内）、余额不足即时中止、建单前按源规模预检；公开接口零 token 裸值（`balance_points` 系）；★ 2026-09-15 起前端全界面统一积分口径（余额/预算/奖励/账单/对账零 token 裸值），建单预检文案同口径；发票口径见运营 SOP
- **邀请裂变**：推荐注册、邀请奖励；二级分销漏斗（限 2 级、付费永久包触发上级返佣、归因看板）
- **企业集成**：SCIM 2.0 用户/组织同步（Entra/Okta 兼容，租户自助配置）、SSO/OIDC（飞书/钉钉）、TMX 双向翻译记忆交换（Trados/memoQ 对接）、VS Code 插件与 Chrome 划词扩展
- **包级权限矩阵**：知识库包 读/写/管理 三级授权（部门管理员按范围、普通成员按授权硬闸）
- **大文件导入**：KB/TM 批量导入分片上传 + 断点续传（>4MB 前端自动分片）
- **可观测性**：SLO/SLI 多窗口 burn-rate 告警（可用性/翻译成功率/P99）、路由级 P50/P95/单位成本可视化、Prometheus /metrics
- **智能路由**：供应商实时延迟/成本动态权重路由与慢分位对冲请求（Hedged Requests，默认关，环境变量启用）
- **注册与邮件模板**：自助注册分个人/企业两类；超管可配置多用途邮件模板
- **OpenAPI**：API Key、异步任务模型（创建/轮询/下载）、同步短文翻译、术语检索（`GET /openapi/v1/terms`）；规范内置 `/openapi/v1.json`
- **Webhook 回调**：翻译完成自动 POST 通知（HMAC-SHA256 签名），支持可配置重试策略（最大重试次数/间隔），投递历史记录与死信队列，管理员可手动重试
- **品牌官网首页**：未登录 `/` 为营销 Landing（价值主张/积分价目卡动态拉取/FAQ/信任条/CTA + **销售留资表单** LeadForm→`POST /api/lead`：IP 限流落库/蜜罐假成功/可选 Turnstile，线索进反馈中心并通知运营），UTM 五参捕获进注册归因；SEO（description/og/canonical/JSON-LD）；`/pricing` 公开页为前端 PricingPage 路由（★2026-09-18 双实现归一，Go 服务端渲染版已删除）积分口径 + 首月半价
- **增长归因漏斗**：注册随 UTM/ref/landing_path/UA 落 `registration_attribution`；超管 `GET /api/admin/funnel` 五环节（注册→激活→耗尽→首购→续费）聚合 + 后台漏斗面板（7/30/90 天）
- **反薅护栏**：一次性邮箱黑名单（注册/发码/换绑）+ Cloudflare Turnstile 人机验证；顺序「格式→黑名单→人机」
- **触达序列（S7）**：余额清零满 48h 未付费→一次性挽回礼包；到期 T-3 续费提醒、T+3 老客回归回访；站内+邮件+运营群，去重幂等
- **敏感词兜底闸（S8）**：词包热加载、双向检测（Unicode 归一化口径：小写+NFKC 全角折叠+零宽字符剥离+空白剥离，全角/零宽/字间空格混淆不可绕过）、输入命中不进模型（文本整单拒译/文件段级占位）、审计+告警+超管开关
- **S9 最小监控**：同机 Prometheus + Alertmanager + node_exporter（全回环、内存帽、METRICS_TOKEN 鉴权抓取）；6 条告警规则经 `/api/alerts/alertmanager` 收口进平台告警中心与运营群
- **USDT 收款（海外线）**：TRC20/ERC20/BEP20 三链双轨——M1 人工核销（收银台精确金额+尾数对单、客户声明 txid、超管确认强制回填链上哈希且一笔交易只核一单）；M2 链上轮询自动对账（默认关闭，确认数阈值+精确金额唯一命中才入账，错金额留孤儿池人工裁决）。超管「计费与套餐→运营」USDT 分区配置钱包地址/汇率/链/确认数；境内主体不得开通
- **运营通知渠道**：企业微信/钉钉/Slack/Teams 四路群机器人（system_config 配置即用，未配置静默跳过）；审计写失败计数经 `/metrics` 暴露（`translator_audit_write_failures_total`）
- **运营报表**：后台「设置→计费」支持用量明细 CSV 导出（`/api/billing/usage?export=csv`，日期区间/分页上限保护；非超管自动脱敏供应商与展示系数，与页面口径一致）
- **运营 SOP**：《试运营收款·发票·退款 SOP》（到账三对/SLA≤2h/每周盘 pending/普票补开口径/退款消耗门槛制 + 话术模板 + 报价单·意向单最小模板）
- **管理后台**：一级菜单 9 项（2026-09-15 Tab 精简 20→8；2026-09-16 autosales 新增「🤖 AI 助手」成 9 项：知识库/话术/流程/功能入口/LLM 配置/会话记录可视化维护），审计日志、系统配置、模型路由、告警收纳于对应 Hub；旧深链 key 兼容跳转
- **AI 销售/客服助手（★ autosales，2026-09-16 上线）**：常驻挂件（除登录/注册页）——访客自动接待、产品使用指导、功能入口深链一键直达；LLM 多模型降级（未配 Key 时话术/知识库规则兜底）；★ R0 批次（2026-09-16）：管理台在线配置 LLM（Base URL/Key 掩码/模型，保存即生效免重启，带测试连通与生效徽标）、同义词归一检索（口语问法对齐知识库，如「充钱」→「充值」）、未答问题自动登记进管理台「待补料问题」清单（运营补料闭环）；★ 2026-09-17 架构收敛：并入主 module `internal/assist/*` + `cmd/assist-server`，管理台 Token 经主后台 `/api/admin/assist/token` 免手填下发，日志接统一 slog，CI 纳入单测与 UAT
- **质量闭环（★ 2026-09-17）**：① evals 不合格处置——各语言评估总分低于 `evals_fail_threshold`（默认 60，0=关闭）时判 `failed`、工单打「质检存疑」标（`quality_flagged` 列）、告警中心 + 群机器人限频提醒（`evals_alert_enabled` 可单独关提醒保留打标），**不做自动重译**（Judge 主观分重译易震荡，仅人工决策）；② 用户侧 QA 报告透出——工单列表行徽标（N 项错误/N 项提示）+ 详情抽屉「质检报告」区块（Pass/错误/提示汇总 + Issues 明细表 + 各语言五维评估分），付费用户在界面内即可感知质检投入（此前仅下载 xlsx 可见）


---

## 测试

```bash
cd backend-go && go test -race ./...           # 单元测试（PG 方言助手/迁移锁/nil 防线回归，无 PG 实例自动跳过）
bash scripts/uat/run_uat.sh                    # 全链路 UAT 主矩阵（PostgreSQL 方言=生产同构，发布闸门：API A/B 主链路 77（含 A1b 留资 8 断言、/pricing 归一 SPA 壳断言） + 功能/交易专项 326（含 T42 USDT 全链 mock_chain 驱动、T43/T44 修复回归、T45 密码找回全链路） + Playwright 33（mobile_uat/a11y 超时已根治，0 flaky）；首轮非零自动 --last-failed 复跑甄别 flaky）
bash scripts/uat/assist_uat.sh                 # AI 顾问 UAT（34 断言：C端链路/同义词/兜底改造/config 白名单与掩码/LLM 热加载/测试连通/CRUD/内嵌管理页/主库 Token 桥接与 env 优先级）
bash scripts/uat/multi_instance_e2e.sh         # 双实例 e2e（JWT 互通/USDT 对账锁/双桶并发勾稽/优雅停机，验证多实例红线）
PW_TARGET=e2e/xxx.spec.ts bash scripts/uat/run_uat.sh  # 迭代调试：只跑指定 e2e（缺省全量）
DB_DRIVER=sqlite UAT_SKIP_RACE=1 bash scripts/uat/run_uat.sh  # SQLite 方言本地快跑（兼容参考）
cd frontend-react && npx vitest run            # 前端单测（118 用例，含工单质检徽标/AI 助手面板/留资表单/登录链路 jsdom 测试）
cd sdk/typescript && npm test                  # TS SDK 行为级测试（8 用例）
cd sdk/python && python3 -m unittest test_translator_sdk  # Python SDK 测试（13 用例）
```

> 手工探针（生产站 CSP / 管理台 Token 验证等，不在发布闸门内）见 `frontend-react/e2e-manual/`，
> 运行方式写在各文件头注释；红线约定见 [AGENTS.md](AGENTS.md)。

UAT 断言层双方言（`scripts/uat/dblib.sh`），同一套用例覆盖两种方言；生产方言（PG）必须进矩阵（CI `uat-pg` job 同口径）。

---

## 部署要点

- 生产形态为单个 Go 二进制 + `frontend-react/dist` 静态资源
- 数据库：PostgreSQL 16 + pgvector（同机自建）
- Go 服务通过 `-frontend` 参数托管前端 dist
- `deploy/` 目录提供：
  - systemd 服务模板与 drop-in 配置
  - Caddy 反向代理与安全头配置
  - 部署检查、冒烟、负载测试、灾难恢复演练脚本
- 详细步骤见《部署指南.md》

---

## 文档索引

| 文档 | 说明 |
|---|---|
| [AGENTS.md](AGENTS.md) | 工程硬约定（store 冻结规则、观测/密钥/方言约定、提交前闸门、e2e 红线） |
| [部署指南.md](部署指南.md) | 生产环境安装、systemd/Caddy 配置、依赖与目录 |
| [PROGRESS.md](PROGRESS.md) | 项目进度、当前生产状态 |
| [权限关系.md](权限关系.md) | 租户-组织-部门-角色权限模型 |
| [改造完成情况_架构融合与质量闭环_20260917.md](改造完成情况_架构融合与质量闭环_20260917.md) | 架构融合与质量闭环五项改造的落地记录与闸门实测 |
| [白皮书-翻译助手.md](商业化白皮书-翻译助手.md) | 商业化白皮书 v1.1（积分制/价目/成本模型/渠道） |
| [品牌一页纸.md](品牌一页纸.md) | 品牌名称/定位/slogan/卖点/禁用词唯一口径 |
| [商业化试运营_差距与行动计划_20260914.md](商业化试运营_差距与行动计划_20260914.md) | S0-S9 决策与执行清单 |
| [试运营收款发票退款SOP_20260914.md](试运营收款发票退款SOP_20260914.md) | 收款确认/发票/退款运营 SOP 与话术 |
| [生产配置清单_20260904.md](生产配置清单_20260904.md) | 生产必配项逐项核对（含 S9 监控与积分制配置） |
