# 能言 SaaS · 项目进度总览

> 最后更新：2026-08-30（PG + Redis 已落地 Seoul 生产部署；监控/日志/Loki/Prometheus/Grafana 配置交付；k6 压测 + Playwright E2E 配置交付；API 版本化 Header 控制落地；审计日志按保留天数定期 prune 落地）｜ 与生产一致（main 分支）

## 〇、全仓端到端评审整改 + 黑盒 UAT（第四批）

| 块 | 内容 |
|---|---|
| **P0 安全** | 跨租户安全句审核越权（补 tenant_id 条件）；KB 条目 target_lang 白名单（tm_segments 列名拼接位防标识符注入）；KB 导入元信息 temp_id 格式校验 + FilePath 落 UploadDir 白名单双闸；API Key 换 crypto/rand(160bit)；wordBoundaryCache 并发写加锁 |
| **资损/双跑** | OpenAPI 建任务余额预检改双桶合计（消除 A1 口径回潮误拒台账租户）；RequeueStalledTickets 删除「不看租约年龄」的第二段释放（认领窗口双跑双扣费根因）；句数镜像 json_set 单语句原子增减 + 发放流 IMMEDIATE 事务化（含 token 入账同事务）；legacy Deduct 改守卫式条件更新 |
| **引擎** | BatchTranslate 接入统一网关（resolveModel→stage_models.ai_initial），文件管线不再绕过 model_routes；硬闸补漏循环加墙钟预算(FILE_HARDGATE_MAX_SEC 默认600s)+连续2轮零进展熔断（「译出为止」语义不变） |
| **静默失效** | ListUsersByRole 补 deactivate_at 列（13列Scan14目标恒空→超管通知链复活）；QPS/并发配额落 system_config(tenant_quota_<tid>) 且启动回放；billing_config 审计 before 值先读后写；clientIP 支持 TRUST_PROXY_XFF 取真实IP（反代限流不再全员连坐）；GDPR 擦除补 12 表+工单磁盘产物清理(EraseTenantDataFull) |
| **UAT 实测追加修复** | 强制计费余额拒绝 error_code 映射 insufficient_balance（原 rejected 违约）；refund_revoke 告警移出 IMMEDIATE 事务（跨连接写被锁吞）；退款裸 no rows 友好化；取消与认领竞态（认领前查态防覆盖 + runTicket 3s 取消监视器联动 ctx）——详见《archive/全仓端到端评审·P0缺陷与交付收口方案.md》§六 |
| **新需求** | 邀请好友前台记录：ListReferrals 补 invitee_email/paid 标记，面板新增邮箱/邀请状态/是否已付费列（中英 i18n）；行业注册通用兜底（general 包幂等创建，缺选/错选不再拒绝注册） |
| **交付物** | Python SDK success 字段 P0 修复（对现网契约必失败→可用）+ JS 错误消息对齐 + 默认轮询按 type 15s/60s；前端五修（审批台 v-for 遮蔽 t 崩溃/Login roleLevel 归一四级/core.ts abort 监听泄漏/PlansPanel NaN+style 双开标签/Audit CSV 导出带鉴权头）；systemd 沙箱(User=translator+ProtectSystem 等)+密钥 EnvironmentFile(0600)；Caddy 安全头基线+回调凭证改环境变量引用；.gitignore 废除 /*.md（交付文档回归版本库） |

## 〇-B、前端重写与租户级唯一/KB 计费（2026-08-27）

| 块 | 内容 |
|---|---|
| **React + TDesign 重写** | 删除 Vue 旧栈（frontend/），新建 frontend-react/，start.sh/build.sh 指向 frontend-react |
| **中文注释补齐** | 全量中文注释补齐（React 新栈 + backend） |
| **租户级唯一约束** | output_artifacts.path / packages.code / orders.order_no / users.ref_code 改为租户级唯一 |
| **KB 嵌入计费** | 向量索引重建按包类型分摊 token 费用，行业/语言文化等全局包免费，租户/部门包按字符比例计费到对应租户 |

## 〇-C、最新需求交付（2026-08-28，提交 d9ea334）

| 块 | 内容 |
|---|---|
| **品牌子域直载 + 登录跳转** | 品牌信息由前端按访问 host 调 `/api/branding` 直接加载（无「根域配置再覆盖」）；登录成功后后端返回 `brand_host`，若用户所属租户配置了独立子域且与当前域不一致，前端带 `?token=` 跳转该子域（需求 1） |
| **企业注册角色区分** | 企业注册拆分为「我是管理员（新建企业）/我是普通成员（受邀加入）」；成员须凭有效企业邀请码加入，无效或非企业邀请码自动降级为个人用户（需求 2、7） |
| **邀请裂变个人限定** | 邀请付费奖励（多邀得多）仅个人用户（is_personal=1）可得，企业租户后端跳过发放；邀请面板按 is_personal 区分企业/个人，企业用户隐藏多邀得多奖励与后台配置（需求 5） |
| **公开文档优化** | `/docs/sla` 增加中/英切换（localStorage 记忆）；移除页脚 STATUS 按钮；定价页改为品牌蓝主题并卡片化（需求 6） |
| **主题与组件统一** | 统一 TDesign 品牌令牌（选中态加深、主色更饱和 `#2f47f5`）；修正 ChatWindow/TicketsPage/ModeToggle/AdminDashboard 等硬编码谷歌蓝，圆角对齐 TDesign（需求 3、4） |

## 〇-D、最新需求交付（2026-08-28，提交 6a868e6）

| 块 | 内容 |
|---|---|
| **实时计费（边工作边计费）** | llm.Client.OnUsage 每次 LLM 调用（对话/嵌入）即时上报用量；Bill.Meter 始终计量（billing_enforced=0 时仅记台账不计费），余额扣除仍受 billing_enforced 控制；余额不足经 ctx 中止整次翻译任务，避免供应商被免费翻译（白嫖）。移除原有的「任务结束后统一扣费」（chargeTaskTokens），改为逐调用计量 |
| **工单进度细粒度落库** | SetTicketState 改为同步骤 UPSERT（每步骤仅一行轨迹，避免每批进度撑爆 ticket_state）；新增 started_at/duration_ms 记录每步执行耗时；初翻/校对逐段进度经引擎回调归集为 file_translate 轨迹的 init/review done/total，前端可展示精确百分比与每步耗时 |
| **全量中文注释** | 前后端代码全量补充中文注释（文件职责说明 + 函数注释），无逻辑变更 |

---

## 〇-E、OpenAPI 全功能 UAT（2026-08-28，生产验收）

生产端点 `https://langcross.lexicorn.cn`，scope=all 测试 Key（测后已轮换，旧 Key 作废）。**7/7 全通过**：

| 端点 | 结果 |
|------|------|
| `GET /openapi/v1/balance` | 200，返回 token / ≈句数余额 |
| `GET /openapi/v1/kb/stats` | 200（`kb_entries:4013`） |
| `GET /openapi/v1/billing/usage` | 200，usage 随调用持续增长（**真实计量生效**） |
| `POST /openapi/v1/translate`（同步短文） | 200，en/ja 译文正确 |
| `POST /openapi/v1/tasks`（文本） | 202 入队 → `completed`；`status` 含完整译文，`tokens_used` 已计费 |
| `POST /openapi/v1/tasks`（文件 .txt） | 202 入队 → `completed`；`download` 返回正确译文内容 |
| `POST /openapi/v1/apikey/rotate` | 200，旧 Key 立即失效（balance 复测 401）/ 新 Key 可用（200） |

说明（非缺陷，已交叉验证不影响计费）：
- 文本任务结果在 `status.translations`；其 `download` 返回 `no_result` 为预期（download 仅用于文件产物）。
- 文件任务单文件 `download` 直接回传译文内容（多文件才打包 zip，与文档「缺省 zip」措辞略有出入，功能正常）。
- 同步 `translate` 响应体 `tokens_used` 现回填真实用量（引擎注入用量收集器后由 `UsageTokens` 汇总，与 balance/usage 一致）；此前回填 0 的展示字段不一致已修复（提交 91dc8c5，R-L1）。

## 〇-F、中低优整改 + 文件翻译修复（2026-08-28，提交 91dc8c5）

| 块 | 内容 |
|---|---|
| **文件翻译质量闸** | 文件交付物（含快速模式）强制硬约束闸重翻（`gates.go` / `applySegmentGates` 传 `retry=true`）：数字/格式/非源语言/乱码不过则带反馈重翻一次，避免错误直接落入成品 xlsx |
| **源语言全角误判（成本表漏译根因）** | `DetectSourceLang` 全角数字/标点不再稀释中文判定——成本表单元格「单价￥１２３．４５」原误判为 `en`→`en` 回显、段未译出；新增 `TestDetectSourceLangFullWidth` 回归测试 |
| **xlsx 单目标原地替换** | 单目标语言文件翻译改为原地替换单元格（产物即译文），多目标仍多 Sheet；修复「打开仍是中文原 Sheet」的误解 |
| **R-M1 入账口径** | 套餐 token 入账统一 × `MarkupMultiplier`（与扣费同单位）；修正 `phase4_test` 旧断言（50000→75000） |
| **R-M2 计费防丢** | `billing/sink.go` 非余额不足瞬时错误重入队（fail-open，带 50k 上限） |
| **R-M3 支付验真** | 微信 AES-256-GCM / 支付宝 RSA2 真实加解密；明文回调拒绝；仅 `mock` 渠道需 `X-Admin-Token` |
| **R-M4~M5** | 源语言识别补日/韩/阿/俄；阶段模型（校对/Judge/文化闸门）纳入多供应商 failover |
| **R-M6~M8** | Caddy on-demand 枚举 oracle 封禁（回环 + CIDR 白名单）；CORS 默认拒绝；登录/注册限流落库（`rate_limits` 表 + 内存兜底） |
| **R-M9 / R-L1~L4** | 前端品牌平台根哨兵 0→1；OpenAPI 同步翻译 `tokens_used` 回填；SDK 下载探测 JSON 错误体改抛错；扩展可配 fast/pro；`kb_entries` 四层（术语/TM/安全句/碎片）可达 |
| **全量中文注释** | 前后端代码（go/ts/tsx/js/py）全量补/对齐中文注释（本次新增 `gates.go`、`ratelimit.go` 包注释与若干前端 i18n 注释） |

## 〇-G、优化方案修订与落地决策（2026-08-30）

> 评审《系统优化方案.md》v1.0 后纠偏：文档方向（为规模化做准备）成立，但将**已实现的 PG 双方言层、pgvector 双写、jobs 表队列**误列为"待从零开发"，导致 P0 工时/优先级失真。落地口径改为"激活既有能力 + 补齐真实缺口"，详见《系统优化方案.md》§〇。

| 工作流 | 内容 | 状态 |
|---|---|---|
| **A. PostgreSQL 切换** | 连接池配置 env（`DB_MAX_OPEN_CONNS` 等）+ 一次性迁移工具 `cmd/migrate-sqlite-to-pg` 已就绪；PG 驱动此前已 blank-import。`DB_DRIVER=postgres`+`DB_DSN` 部署切换与切流后 `RebuildKBIndex` 回填 pgvector 已落地 ✅（Seoul 服务器同机自建 PG 16 + pgvector 0.6.0，非托管 RDS） | ✅ 已落地（Seoul 生产部署，2026-08-30） |
| **B. 邮件异步** | 复用 `internal/queue` 把同步 `mail.Sender.Send` 改为入队 + worker 发送 + 重试/死信；不引 Redis | ✅ 已落地（commit 76f4410） |
| **C. 统一错误码+结构化日志** | 新增 `internal/errors` 枚举 + `log/slog` + `X-Trace-ID` 中间件；auth 关键路径已迁移，其余渐进 | ✅ 已落地（commit 76f4410） |
| **D. 对照编辑器（新 feature）** | `translation_edits` 表 + `GET/POST /api/tickets/segments` + 前端双栏编辑器（术语高亮+逐段通过/驳回批注）；文本+文件（MVP 先 xlsx/csv/对照表，docx/pdf 二期） | ✅ 已落地（commit 76f4410，见 〇-H） |

**明确不做（当前过度设计）**：Redis Cluster / etcd / gRPC Sidecar / K8s 微服务拆分 / 多区域；SSO/SCIM/白标/CAT 插件/混沌工程/SDK 自动发布流水线——等具体企业客户或规模化运维诉求出现再做。

## 〇-A、历史批次索引（详情见对应方案文档）

  - **第三批（并发优化+商业化收口）**：LLM 三路信号量/Embed 批处理缓存/卡死巡检正确性/FILEPROC 子进程闸；双桶余额贯通/定价单一事实源/payments 实收/退款权益回收/download 归属/metrics 死锁修复/模型Key加密/oneid 邮箱唯一+自助注销 → 《archive/评审整改·余额贯通与商业化收口方案.md》《archive/翻译引擎并发瓶颈诊断与优化方案.md》
  - **第二批（KB 组织继承链）**：祖先链就近覆盖/兄弟隔离/跨部门降级检索/tm_segments 三元组唯一键 → 《archive/KB组织继承链与部门隔离改造方案.md》
  - **首批（架构决策与止血）**：BYOK 移除统一网关/P0 八项止血/P1 七项/Python 栈退役/git 历史清洗 → 《archive/LLM统一网关与BYOK移除方案.md》《archive/P0安全止血与并发原子性修复方案.md》《archive/旧Python后端下线与构建链收敛方案.md》
  - **更早（商业化四连等）**：双桶台账/参数化巡检/订单分流/邀请裂变/TM 自闭环/OCR 移除/PDF 两阶段管线 → 《archive/TOKEN双桶改造实施方案.md》《archive/TM自闭环与OCR移除方案.md》

## 一、当前生产状态

| 项 | 值 |
|----|-----|
| 生产域名 | **https://langcross.lexicorn.cn**（2026-08-24 起，旧域名已下线） |
| 服务器 | 43.108.86.140（阿里云；内存紧张，按 **≤1G 有效可用** 调优：GOMEMLIMIT=650Mi、MemoryMax=950M、worker=2，详见下文「运维护栏」） |
| 服务 | `translator.service`（Go 单二进制，/status 返回 v3, ok:true）；前端已切换为 React + TDesign（frontend/ 旧 Vue 栈已下线） |
| 反代 | Caddy（自动 HTTPS），配置片段 `/etc/caddy/translator.conf` |
| 数据库 | SQLite 单文件 `/opt/translator/data/tm.sqlite3`（业务+TM 共用） |
| 计费 | Token 实时计量（每次 LLM 调用即上报用量；余额扣除受 billing_enforced 控制，billing_enforced=0 暂未启用扣费，超管随时开启；余额不足中止整次任务避免白嫖） |
| 部署脚本 | 后端交叉编译（GOOS=linux GOARCH=amd64）→ scp 二进制；前端 `npm run build` → scp dist 静态资源；ssh 重启 `translator.service`（详见 README 快速开始） |

## 二、核心能力（全部已上线）

- **多格式文件翻译**：docx/pptx/xlsx/pdf/txt/csv/md 输出译文文件；srt/vtt/json/yaml 等以对照表（xlsx）形式交付；多文件混合工单、多目标语言打包 zip
- **PDF 保真翻译管线（两阶段，a1a5aad）**：
   1. `extract`：pdf2docx 转 DOCX 并提取段落键（含表格/嵌套/文本框/页眉脚）
   2. LLM 翻译段落键 → `apply`：在缓存 DOCX 上 w:t 级替换（图片/排版零破坏）+ LibreOffice 转回 PDF（★ 图片内容按产品策略不翻译，OCR 已移除）
    - ⚠️ 已知限制与缓解：超大 PDF（`pdf2docx + LibreOffice` 转 PDF 易超时/卡死）已由上传前置拦截兜底——**PDF 体积 >15MB 或页数 >120 页直接友好拒绝并提示转 docx**；转换子进程标记为 OOM 优先受害者，超限快速失败而非挂死整机。常规 PDF 可稳定翻译，超大/扫描件仍建议优先上传 `.docx` 源文件
- **双模式**：⚡快速（AI 初翻+校对）/ 🎓专业校对（知识库+评估+硬闸全流水线）
- **计费体系（Token 实费 + 双桶台账）**：额度=发放台账（quota_grants，带到期可叠加）+ 永久余额（balance_accounts）；扣减顺序「台账近到期行→永久余额」事务原子；部门预算墙、套餐/订单/发票
- **邀请裂变**：个人邀请码+专属链接+二维码；被邀人注册→邀请者体验叠加(+30万，有效期默认14天、后台可调)；首笔付费套餐→邀请者+50万（token 数与有效期均后台可调：默认永久余额，可改为限时台账）；同对每种奖励仅一次。**仅个人用户（is_personal=1）可获得邀请奖励（含多邀得多付费奖励），企业租户后端跳过发放；邀请面板按 is_personal 区分企业/个人，企业用户隐藏多邀得多奖励与后台配置**。
- **注册与邮件体系**：自助注册拆分为「个人 / 企业」两类（个人注册自动生成租户编码与名称并标记 is_personal；企业注册进一步区分「我是管理员（新建企业）/我是普通成员（凭有效企业邀请码加入）」，无效或非企业邀请码自动降级为个人用户）。企业注册发送欢迎邮件并抄送管理员邮箱。超管可在后台「邮件模板」面板配置多用途模板（注册验证码 / 密码重置验证码 / 企业注册成功提醒 / 租户管理员通知 / 系统告警 / 产品手册）；注册成功自动向用户发送《产品手册》PDF 邮件，附件读取外部 PDF 文件（默认 `/opt/translator/data/manual.pdf`，可用 `system_config.manual_pdf_path` 或环境变量 `MANUAL_PDF_PATH` 指定），经专用邮箱 `info@lexicorn.cn` 发送；中文邮件主题用 RFC2047 编码、正文 base64 编码。
- **TM 自闭环**：tm_review 待审池唯一入库通道（超管人工审核通过才落正式 TM）；bitext/tmx/反馈修正/命中达标四来源候选
- **开放 API**：`POST /openapi/v1/tasks` 异步任务 + 轮询 status/download + balance；AES-GCM 密钥加密与一次性明文展示
- **组织架构**：平台根→租户根→组织→部门四级树、拖拽调层级、部门预算徽标弹窗、邀请码绑定组织
  - **管理后台**：三工作台（超管/租管/部门管）、租户切换器、OpenAPI 文档在线编辑（双语）、审计日志、告警中心、记忆审核台
   - **品牌定制与子域名**：按子域名前缀解析租户品牌（名称/Logo/子域）；Caddy on-demand TLS 自动签发证书（需 DNS 通配符 A 记录 `*.lexicorn.cn → 服务器 IP`）；品牌信息前端按 host 直接调 `/api/branding` 加载（无根域覆盖）；登录成功后自动跳转至所属品牌子域（后端返回 `brand_host`）。品牌定制为付费套餐功能（有效付费套餐或超管授权方可编辑，未满足仅可查看）；登录页支持两种布局——① 全屏背景（登录卡片浮于其上，无遮罩）② 左右分栏（容器可在左/右，另一侧为图片）；登录卡片与背景图位置均可在品牌管理页拖拽定位并保存；语言切换（中文/EN）为全局设计，内嵌于登录容器右上角
- **Office 划译插件**：Word 侧加载 taskpane，选区翻译插回文档
- **运维护栏（1G 内存红线，2026-08-28 优化）**：`GOMEMLIMIT=650Mi`、`MemoryMax=950M`、worker=2、LLM 并发 8（禁 HTTP/2 治流挂起）；**文件翻译防卡死**：PDF 体积>15MB 或页数>120 前置拦截 + 友好提示；转换子进程 OOM 优先受害者 + 可选 `FILEPROC_RLIMIT_AS_MB` 硬上限；**并发写零 SQLITE_BUSY**：实时用量计量改为内存累积 + 周期(2s/200条)按租户单事务批量落库（写事务从每秒 N 个降到每周期每租户 1 个），并用 `usage_daily` 计数器表替代每次请求的 ledger `LIKE` 全扫；产物留存 14 天+到期提醒、pending 订单 15min 自动关闭、低额提醒巡检

## 三、近期关键修复（2026-08-24~27）

| 提交 | 内容 |
|------|------|
| 0ea5dac | KB 五档可见范围模型 + 跨部门包(cross_dept)独立类型（cross_orgs/cross_all 部门集合，维护与使用权限按涵盖部门收窄，导入/写条目/删除均经 deptKBScope）；embedding 供应商切 SiliconFlow BAAI/bge-m3(1024维) 移除硬编码 embedding-2；pgvector 后端(UpsertEmbedding/VectorSearch/RebuildKBIndex 双写，语义检索优先向量、回退 ScopedSearchScope)；前后端全量中文注释随本次提交补齐（覆盖整个代码库） |
| c5bbdc0 | 后端全量中文注释（39 文件头 + 31 函数文档）+ gofmt |
| a1a5aad | PDF 两阶段翻译重构：修表格不译/图片丢失/图后内容丢失三大缺陷（w:t 级替换、含图 run 保护、lxml id 去重陷阱） |
| e435a5f | python-docx runs 代理对象复用修复；零宽字符归一化；域名切换 |
| 7a7d459..17422d7 | 工单删除按钮+后端级联删除；气泡式进度面板（智能上下定位）；i18n 补齐 |
| 3b7047d..e883d8a | TM 自闭环全量落地（待审池/审核台/计数钩子）；OCR 全量移除；OpenAPI 文档口径统一；文本任务单引号 JSON 宽松解析 |
| d4b9d6e..9b4dd59 | 文件管线回显检测+硬闸重试（每段独立重翻最多 2 轮）；弹窗 Teleport 兼容加固；前台菜单受控下拉修复；LLM 客户端禁 HTTP/2 治 siliconflow 流挂起 |
| 1200dce..0ae3694 | 前台汉堡菜单双事件保险；改密/改邮弹窗与后台对齐、双验证码、邮箱必填全局唯一 |
| 614fe8f | CommitA 双桶台账：quota_grants + DeductWithGrants 顺序扣减；注册礼包 30w/14d 入台账 |
| bcdf7b6 | CommitB 商业化参数默认值落库（面板可调）；pending 订单 15min 自动关闭；低额提醒巡检（24h 去重） |
| 765d21d | CommitC 订单确认按 ptype 分流：paid→t+30 台账 / increment→永久余额 / free 维持旧句数通道 |
| 194211c+7225bfa | CommitD 邀请裂变全量：存储层首绑闸门/叠加发放/付费去重 + my/qrcode 接口 + 注册绑定与付费奖励钩子 + 前端邀请面板；修复 RewardPaidPermanent 租户取错、套餐订单 amount_tokens=0 致入账 0、QuotaGrantMigrate/ReferralMigrate 未挂载三处存量缺陷 |
| 1e37128 | 今日改动范围全量中文注释补全（后端 32 文件 + 前端 10 文件，无逻辑变更） |
| 6d85e1b | React + TDesign 前端重写（frontend/ Vue 旧栈下线，frontend-react/ 新建，start.sh/build.sh 切到新栈）；React 新栈与 backend 全量中文注释补齐；租户级唯一约束（output_artifacts.path / packages.code / orders.order_no / users.ref_code）；KB 嵌入向量索引重建按包类型分摊 token 费用，全局包免费、租户/部门包按字符比例计费 |
| 9fa17af | 品牌定制按子域名前缀解析租户；Caddy on-demand TLS 自动签发证书（配合 DNS 通配符 A 记录）；品牌定制改为套餐付费功能（有效付费套餐或超管授权方可编辑，未满足仅可查看并提示）；新增超管为指定租户开通品牌定制接口 POST /api/admin/tenant/brand-grant；前后端代码补充全量中文注释 |
| 709a0e9 | 登录页双布局（全屏背景 / 左右分栏，容器左右可切换）；背景图样式（缩放/位置/充满-适应）与登录卡片位置均可在品牌管理页拖拽保存；登录/注册/忘记密码三视图互斥；语言切换按钮内嵌登录容器；前后端补充全量中文注释 |
| ffe9312 | 注册拆分为个人/企业用户（个人 is_personal 可获邀请奖励，企业注册发欢迎邮件并抄送）；新增超管可配邮件模板（6 类）与后台「邮件模板」面板；注册成功自动发送产品手册 PDF 邮件（附件读取外部 PDF，info 专用邮箱发送）；修复中文邮件编码（RFC2047 主题 + base64 正文），mail 支持 CC 与 multipart/mixed 附件；前后端补充全量中文注释 |
| d9ea334 | 品牌子域登录跳转（brand_host 跨域带 token）；企业注册区分管理员/普通成员，成员须凭有效企业邀请码、无效码降级个人；邀请付费奖励仅个人用户可得；/docs/sla 中英切换 + 移除 STATUS 按钮 + 定价页品牌化；主题统一品牌蓝、选中态加深、修正硬编码谷歌蓝 |
| 6a868e6 | 实时计费（边工作边计费）：OnUsage 逐调用计量、余额不足中止任务；工单进度细粒度落库（UPSERT + started_at/duration_ms + 初翻/校对逐段进度）；前后端全量中文注释 |
| bfc982b | **不换库性能优化**：根治大 PDF 卡死（15MB/120页前置拦截 + 子进程 OOM 优先受害者 + GOMEMLIMIT=650Mi + FreeOSMemory）与多人并发 SQLITE_BUSY（实时计量批量落库 + usage_daily 计数器 + 进度/TM 批量写 + 缓存容量上限）；**修双重计费资损**（移除 chargeTokens 二次扣费，实时钩子为唯一扣费源）与用量看板 user_id 归属失真（ctx 透传） |
| 91dc8c5 | **中低优整改 + 文件翻译修复**：文件交付物强制硬闸重翻；DetectSourceLang 全角误判修复（成本表漏译根因）；xlsx 单目标原地替换；R-M1 入账×markup / R-M2 sink 重入队 / R-M3 支付真实验签解密 / R-M4 日韩阿俄识别 / R-M5 阶段模型 failover / R-M6 Caddy 枚举封禁 / R-M7 CORS 默认拒绝 / R-M8 限流落库 / R-M9 品牌根哨兵 / R-L1 tokens_used 回填 / R-L2 SDK 下载探测 / R-L3 扩展可配 fast·pro / R-L4 kb 四层可达；前后端全量中文注释 |

## 四、技术要点备忘

- **PDF 字体**：服务器装 `fonts-noto-cjk`（NotoSansCJK-Regular.ttc，拉丁+CJK 全覆盖）；`PDF_FONT_PATH` 指向它。旧 DroidSansFallbackFull.ttf 无拉丁字形（渲染为框框），勿再使用
- **Python 依赖**：`/opt/translator/.venv` 内 fpdf2/pdf2docx/python-docx/Pillow/fonttools；系统需 poppler-utils、libreoffice-writer/impress/calc。★ tesseract/pytesseract 已随 OCR 移除卸载，勿再装回
- **双桶台账**：额度唯一扣减入口 DeductWithGrants；paid 订单按「包内句数×estimate_tokens_per_sentence(默认500)」折算入台账（订单 amount_tokens 恒为 0，不可直接用）
- **前端弹窗规范**：应用内 Teleport 遮罩弹窗（fb-mask/fb-modal 样式需组件内自带，scoped 不跨组件共享）；禁用浏览器 alert/confirm 于关键交互
- **lxml 陷阱**：元素代理对象回收后 id() 复用，严禁按 id() 去重节点
- **python-docx 陷阱**：`para.runs` 每次访问返回新代理列表；`run.text=` 会删除该 run 的 drawing/pict 子元素
- **SQLite 并发红线**：DSN `_txlock=immediate` 全局生效；事务内严禁经独立连接再写库（会撞 busy_timeout 静默失败——UAT-2 教训）；句数镜像一律 json_set 原子语句或 IMMEDIATE 事务
- **新增环境变量（第四批）**：`TRUST_PROXY_XFF=1`（反代取真实IP，直连勿开）；`FILE_HARDGATE_MAX_SEC`（硬闸补漏墙钟预算，默认600s）
- **邮件相关环境变量**：`MAIL_ENABLED` / `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS`（默认发信箱 `noreply@lexicorn.cn`，SMTP 端口 465）；`INFO_SMTP_ENABLED` / `INFO_SMTP_USER` / `INFO_SMTP_PASS`（产品手册等专用发信箱 `info@lexicorn.cn`，默认 `smtp.mxhichina.com:465`）。均在 systemd `translator.service` 的 `Environment` 中配置。
- **注册行业口径**：缺选/错选行业→通用行业(general)兜底不再拒绝；通用包由 EnsureDefaultPackages 在租户1幂等创建

## 〇-H、对照编辑器（工作流 D，2026-08-30 新 feature 落地）
- **后端**：`translation_edits` 表（store.go 迁移 + store/edits.go 读写方法，按 ticket_id+lang+seg_index 唯一）；新增 `internal/api/editor.go`：GET/POST `/api/tickets/segments`（?id=&lang=），租户隔离（超管可跨租户）。
- **段落提取**：文本工单解析 `FinalResult.translations` 按行对齐；文件工单解析 xlsx/csv 对照表产物（docx/pdf 等二进制为 `unsupported`，二期）。
- **术语高亮**：GET 响应带回租户术语表 `terms`，前端 `<mark>` 高亮命中串。
- **写入语义**：逐段 upsert edited_text/status(pending/approved/rejected)/note；approve→TM 回写默认关闭（MVP 仅落库，二期可配置）。
- **前端**：`src/components/EditorPage.tsx`（TDesign 双栏：源文只读+术语高亮 / 译文可编辑+状态+批注），`App.tsx` 新增「✍️ 对照编辑」Tab；`api/tickets.ts` 加 `getSegments`/`saveSegments`。
- **验证**：`go build ./...` 通过；`internal/api` 单测（splitLines/locateColumns/extractTextSegments）通过；`npm run typecheck` 与 `vite build` 通过。
- **状态**：已于 commit `76f4410` 落地并 push 至 `origin/main`（不含文档/流程图）。

## 六、解锁 PG + Redis 及路线图落地（2026-08-30）

> 详见《改造方案_解锁PG与Redis及路线图.md》§18/§19 联调验证记录。**路线图 13 阶段已全部交付**，PG + Redis 已在 Seoul 服务器部署落地。

- **阶段一 PostgreSQL 切流**：✅ 已完成。Seoul 服务器 PG 16 + pgvector 0.6.0（同机自建），`migrate-sqlite-to-pg` 迁移 + `backfill-embeddings` 写入 3,347 条向量，`/status` 返回 `dialect:"postgres"`。
- **阶段二 Redis**：✅ 已完成。`internal/infra/{redis,distlock,ratelimit,concurrency}` 自研落地；LLM 信号量/API Key 日配额/工单巡检锁接 Redis；`infra_integration_test.go` 联调全 PASS；运行时 `[init] Redis 已启用` + `/status` ok。
- **阶段三/四 监控/日志**：`deploy/observability/{prometheus,alertmanager,grafana,promtail}` 配置交付；需 Grafana/Loki/Prometheus 实例点亮（runbook 见该目录）。
- **阶段五 对照编辑器 docx/pdf**：`internal/doc/office.go` 纯 Go docx 段落抽取+回写；pdf 经 python venv pdf2docx 桥接；`editor.go` 抽取+`/api/tickets/segments/export` 回写修订稿。单测 PASS。
- **阶段六 SSO/OIDC**：`internal/auth/sso`（OIDC 发现 + 飞书/钉钉 OAuth2）+ `internal/api/sso.go`（`/api/sso/login|callback|providers`）+ config `SSO_PROVIDERS`/`SSO_FRONTEND_URL`。单测 PASS；运行时 providers 列表验证通过。
- **阶段七 多 AZ**：`internal/queue/notifier.go` + `internal/infra/redis/notify.go` Redis 唤醒跨实例 worker + `deploy/multi-az/README.md`（PG 流复制/Redis 高可用/Caddy 亲和/systemd 多实例）。
- **阶段八/九 压测/E2E**：`deploy/loadtest/k6.js` + `frontend-react/{playwright.config.ts,e2e/smoke.spec.ts}` 配置交付。
- **阶段十 API 版本化**：`withAPIVersion` 中间件（`Accept: application/vnd.langcross.v1+json` / `X-API-Version`，默认 v1，v2 预留）。
- **阶段十一 OpenAPI Spec**：`internal/api/openapi.v1.json`（go:embed）+ `/openapi/v1.json` 端点（与 Python SDK 契约一致）。
- **阶段十二 SDK 多语言**：`sdk/{python,typescript,java}` 三语言客户端，同一 OpenAPI 契约。
- **阶段十三 审计留存**：`Store.PruneAuditLogs` + `AuditRetentionDays`（默认 365，system_config 覆盖）+ 每 6h 定时 prune。
- **离线未端到端验证项**（代码/契约已就绪，接入环境即启用）：真实 IdP 授权码交换、PDF 抽取（需 venv pdf2docx）、多实例跨机分发实测、Grafana/Loki 实例点亮、k6/Playwright 实跑。

## 五、文档索引

- [部署指南.md](部署指南.md) — 构建/部署/systemd/Caddy/依赖清单
  - [未完成项目.md](archive/未完成项目.md) — 待办与外部依赖项
  - [待解决问题.md](archive/待解决问题.md) — 问题跟踪（含已解决归档）
  - [权限关系.md](权限关系.md) — 角色层级与数据可见性矩阵
  - [全仓端到端评审·P0缺陷与交付收口方案.md](archive/全仓端到端评审·P0缺陷与交付收口方案.md) — 第四批整改设计+UAT 实测记录（含 4 个 UAT 缺陷修复）
  - [TOKEN双桶改造实施方案.md](archive/TOKEN双桶改造实施方案.md) — 双桶台账数据模型/扣减算法/参数（已全部落地）
  - [TM自闭环与OCR移除方案.md](archive/TM自闭环与OCR移除方案.md) — TM 唯一入库通道与 OCR 移除决策记录
  - [评审整改·余额贯通与商业化收口方案.md](archive/评审整改·余额贯通与商业化收口方案.md) — 双桶余额贯通/插件CORS/财务口径/产物归属/安全加固二期（含硬闸重试特性确认）
   - [翻译引擎并发瓶颈诊断与优化方案.md](archive/翻译引擎并发瓶颈诊断与优化方案.md) — LLM 三路信号量/Embed 批处理缓存/卡死巡检正确性/子进程资源闸/QoS 车道

## 九、Bug 修复记录（2026-08-30）

### 9.1 Admin 账号无邮箱导致绑定弹窗死循环
- **根因**：`EnsureAdmin` 调用 `CreateUser` 时未传入邮箱，admin 账号 `email` 字段为空
- **现象**：admin 登录后前端检测到空邮箱，弹出不可关闭的 `EmailBindModal`，无论是否输入邮箱都无法正常使用
- **修复**：
  - `EnsureAdmin` 新增 `email` 参数，创建/更新时同步设置邮箱
  - `main.go` 新增 `ADMIN_EMAIL` 环境变量，传入 `EnsureAdmin`
  - 登录响应增加 `email` 字段，前端可直接检测
- **文件**：`backend-go/internal/iam/store.go`、`backend-go/cmd/server/main.go`、`backend-go/internal/api/auth.go`、`backend-go/internal/store/users.go`

### 9.2 验证码收不到（NoopSender 模式）
- **根因**：Seoul 服务器未配置 `MAIL_ENABLED=1` 和 SMTP 凭据，`mailer()` 返回 `NoopSender`，验证码仅打印到服务端日志，不会真正发送到邮箱
- **修复**：
  - `sendEmailCode` 在 `noop=true` 时返回明确提示："验证码已生成（测试模式，请查看服务端日志）"
  - 清理 `sendEmailCode` 中未使用的死代码 `sender` 变量
  - 修复 `pwd.codeNoop` 翻译（之前错误显示"请先发送验证码"，现在正确显示测试模式提示）
- **真正收信需配置**：`MAIL_ENABLED=1` + `SMTP_HOST/PORT/USER/PASS`（Seoul 已配置 SMTP，代码已更新并部署）
- **文件**：`backend-go/internal/api/email_verify.go`、`frontend-react/src/i18n/dicts.zh.ts`、`frontend-react/src/i18n/dicts.en.ts`

### 9.3 平台根 admin 账号无法发放试用
- **根因**：`handleGrantTrial` 拒绝 `tenant_id <= 0`，且 `main.go` 仅初始化 `tid=1` 不初始化 `tid=0`（平台根账号）。平台根 admin 无余额账户、无试用额度，且后台无法发放
- **修复**：
  - `main.go`：增加 `EnsureBalance(0)` 和 `EnsureDefaultPackages(0)` 初始化平台根账号
  - `handleGrantTrial`：`req.TenantID <= 0` 改为 `< 0`，新增 `tenant_id=0` 特殊分支直接发放试用额度（无需 tenants 表记录）
- **文件**：`backend-go/cmd/server/main.go`、`backend-go/internal/api/register.go`

### 9.4 超管被 `registration_review` 闸住无法翻译
- **根因**：`billing_api.go` 的 `gateUsage` 在 `registration_review=1` 时要求当前租户存在有效套餐/试用，但超管 `currentTenant` 返回 `1`，同样被闸
- **修复**：`gateUsage` 中 `registration_review` 检查增加超管豁免：`auth.IsSuperAdmin(u)` 为真时直接跳过
- **文件**：`backend-go/internal/api/billing_api.go`

### 9.5 验证码邮件仍无法送达（排查中）
- **当前状态**：Seoul 服务器 `MAIL_ENABLED=1`、SMTP 凭据已配置；Python 直连 `smtp.mxhichina.com:465` 发信成功（认证+发送均 OK）。但用户真实邮箱仍收不到验证码。
- **已做**：
  - 已部署新增邮件流程日志（`enqueueMail`/`syncSendMail`/`SMTPSender.Send` 均加 `[mail]`/`[smtp]` 日志）
  - 已确认进程环境变量 `MAIL_ENABLED=1`、SMTP 参数正确
- **待验证**：需要用户在部署后触发一次「发送验证码」，根据日志判断是入队未消费、SMTP 被拒、还是投递到垃圾箱/异步退回
- **可能方向**：发件域 `lexicorn.cn`  reputation/SPF/DKIM/DMARC 导致收件方拒收或进垃圾箱；需用户配合查看垃圾箱或提供具体邮箱做定向测试
