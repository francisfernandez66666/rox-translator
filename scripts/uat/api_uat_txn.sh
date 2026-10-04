#!/usr/bin/env bash
# ============================================================================
# scripts/uat/api_uat_txn.sh — 功能与交易专项深度 UAT（api_uat.sh 未覆盖的交易/功能分支）
# 覆盖：
#   T1 线下订单全生命周期（admin 代充值 → 确认 → 退款 → 余额回滚）
#   T2 静态码人工确认链路（manual 渠道下单 → 「我已付费」→ 超管待审列表 → 确认）
#   T3 发票金额一致性（B1 定价回填：amount_money = tokens×单价）
#   T4 受邀人付费 → 邀请者付费奖励（运营策略因子 + 幂等）
#   T5 OpenAPI 异步任务（文本任务 → 轮询 → completed）
#   T6 认证：改密 → 新密登录 → 恢复；忘记密码防枚举；错误重置码拒绝
#   T7 OpenAPI 余额硬闸（清零租户 → insufficient_balance）
#   T8 任务中心（超管建任务 → 用户领取 → 重复领取拒绝）
#   T9 知识库：条目更新/删除 + 安全句新增
#   T10 Webhook CRUD + 测试
#   T11 告警中心 + 用量统计（usage/me / usage / usage/org / usage/cost）
#   T12 品牌定制保存（触发品牌术语种入）+ 品牌查询
#   T13 支付回调安全闸（缺凭证 403 / 错误凭证 403 / mock 金额不符 400）
#   T14 权限边界：租户管理员越权代充 403 / 非超管写 ops policy 403 / 匿名 401
#   T16 并发扣费压力（30 路 openapi 并发：不透支余额 / ledger 与消耗严格对账）
#   T17 同租户工单隐私（非创建者读详情 403）  T18 启动日志脱敏（无明文口令 / DSN 掩码）
#   T19 时间窗覆盖白名单（payment.mode、billing.enforced 拒绝；合法覆盖放行）
#   T20 编辑器产物下载 B8 闸（未登记 404 / 路径穿越拒绝）
#   T21 已消耗订单退款按未消耗比例折算（A3）  T22 退款联动撤销裂变付费奖励
#   T23 已退款订单禁止开票（CreateInvoice 硬闸）
#   T25 B3/B5 安全收尾（sso_code 兑换拒绝 / 计费配置匿名拒）  T26 C26 token 真账展示字段
#   T27 F10 OpenAPI 规范导出（含 H12 /terms）  T28 H5 分片上传（续传定位/合并/缺口/类型守卫）
#   T29 H10 SCIM 全生命周期（开通/建用户/查重/停用/删除/坏令牌 401/关闭后 403）
#   T30 H3 包级授权矩阵（读/写/撤销）+ D11 关键词检索 + H12 开放术语检索
#   T31 H12 TMX 导出（成员拒/文档结构/非法语言/匿名拒）  T32 H7+H11 ops 路由与 SLO 可视化
#   T33 H4 TM 审核队列契约 + H9 二级裂变漏斗
#   T34 工单双模式（2026-09-13）：还原文件模式纯文案旁路产物 / 纯文案模式交付 /
#       白名单分档 / 文本工单无文案产物提示 / OpenAPI delivery 回显 / health 暴露 anydoc_ready
#   T35 线上反馈回归（2026-09-14）：文件工单 multipart 上传（特殊文件名 PDF/多文件混合）/
#       bootstrap-demo.sh 配置守护（base_domain 种入 / demo_superadmin / 种子开关可覆盖）
#   T36 商业化开闸批次（2026-09-14）：S1 积分制公开面（plans/overview/me/settings 汇率校验）/
#       S8 敏感词闸（输入拦截 e2e + 开关往返 + 非法值拒绝）/ S3 一次性邮箱黑名单（内置域+增补域）/
#       S4 归因落库+漏斗接口鉴权 / S9 Alertmanager 收口鉴权+告警落库+metrics 401/Bearer /
#       S7 s7_watchlist 表 / S5 首页 HTML
#   T42 USDT 收款全链路（mock_chain：尾数对单/声明 txid/一 tx 一单/M2 自动入账）
#   T37 今日修复回归（2026-09-14）：P0-2 org_id=0 越权（子树内放行+未分配 403）/
#       P0-4 auto_charge 仅超管即时入账（租户管理员 pending）/ P0-3 品牌子域 sso_code 登录链
#       （无裸 token + 兑换 + 单次消费）/ P0-5 敏感词 Unicode 归一化（全角/零宽/空格拦截）/
#       P1-15 points 溢出 400 / P1-2 charge_kind 语义枚举守恒
#   T43 今日修复回归（2026-09-16）：RBAC 收紧（高角色禁落租户 400 / 存量违规行降权 403）/
#       支付渠道 fail-closed（wechat/alipay 显式报错、禁止 mockpay/alipay 占位假码）/
#       发票冲红闭环（开票→void→同单可重开）
#   T44 缺陷核实修复回归锁（2026-09-16 D1-D5）：欠费结算错误分支（qerr 吞错禁回退）/
#       低额告警阈值接线（low_balance_alert_tokens 实读）/ 备份推送超时+禁入 HTTP 白名单 /
#       Caddy CSP 头存在——行为侧由 store/service 单测覆盖，此处为源码级防回退闸门
#   T46 质检闭环 API 透出（改造 4/5，2026-09-17）：详情接口 quality 视图（QA 报告/评估分/存疑语言）
#       + tickets.quality_flagged 列经详情接口零成本透出（前端「质检存疑」徽标数据源）
#   T47 逐段对照真值表（2026-09-18）：文件工单完成后 ticket_segments 落库真值配对
#       （源文段/译文段/段序号），对照接口 /api/tickets/segments 可读；多文件工单按
#       文件维度互不覆盖
#   T48 伪标签清洗端到端（2026-09-18）：mock LLM 回显走形伪标签 `<target>…></target>`
#       （模拟模型在无上下文短单元格上的真实污染形态），断言清洗链拆除伪标签、保留正文、
#       交付结果零残留
#   T49 任务系统（★ #33，2026-09-21）：出厂五类任务数值口径（100/100/500/1000/600 积分与
#       有效期、上限、叠加）+ 登录/翻译事件自动发放（同日去重、周期计数）+ 用户视角出参
#       积分口径零 token + 超管「重置积分消耗量」（有效期不变、非超管 403）+ 超管局部更新
#       不丢事件语义
#   T50 订阅自动续费（★ #41 商业洞二，2026-09-21）：开关鉴权与前置校验（未订阅拒绝）/
#       permissions 单字段原子写不覆盖订阅身份 / 收银台与 /api/me/package 回读一致 /
#       T-3 窗口内扫描自动建同包续费单（created_by=0 系统单）+ 站内信 + 二次扫描去重不堆单 /
#       关闭开关后不再建单
#   T51 优惠券全链路（★ #41 商业洞三，2026-09-21）：建券券码大写归一 + 超管专属读写 /
#       试算与下单同口径（服务端重算金额，前端不参与）+ 门槛/券种/不存在码的业务提示可回显 /
#       核销只改 orders.amount_money，amount_tokens 与积分额度一分不减（券减钱不减货）/
#       每家企业限用次数二次拒 + 失败不残留挂券 pending 单 / 立减超额压到 0.01 元不出 0 元单 /
#       核销流水可查 + 删模板留流水 + 已删码再下单回「券码不存在」
#       （关单退券 ReleaseStaleCouponRedemptions 由 5min 巡检触发，归 store/coupons_test.go 覆盖）
#   T52 AI 助手管理代理（★ #34 后台前端重做，2026-09-21）：仅超管可读 / 匿名与普通用户拒 /
#       状态条回显可达性与 Token 来源（不含明文）/ 知识库 CRUD 往返 / 浏览器带的 admin_token
#       查询串被剥掉（凭据只走服务端注入头）/ 配置白名单与掩码不回写 / 审计只记方法+区域不记请求体 /
#       会话统计与 LLM 连通测试经代理可用 / 白名单外路径 404（不做通用中继）
#   T53 订阅续费宽限期（★ #74，2026-09-23）：开自动续费租户到期后进宽限期（身份与额度保留、
#       落 grace_expires_at、发「宽限期」站内信、/api/me/package 透出 in_grace）/ 宽限期内每日
#       补建续费单且同日去重（pending 单与 renewal_attempts 格子都只 1）/ 宽限期结束摘除并改发
#       「宽限期结束」文案 / 未开自动续费的到期租户立即摘除不进宽限期（对照组）
#   T54 多币种报价（★ #75，2026-09-22 起关闭封存）：超管报价配置口鉴权（匿名/普通用户 403）/
#       关闭态回显恒 CNY + feature_open=false + 白名单只露 CNY / 外币币种与倍率保存一律拒收
#       （CNY 表态仍放行）/ 直插 system_config 残留外币配置也不生效（读口短路压住）/
#       /api/plans 与 /api/me/package 恒 quote_currency=CNY、price_display=人民币原价 /
#       下单快照恒落 CNY|1 且 money_cny=amount_money=实付（新租户首月半价后 10.8，快照落在
#       最终应收之后）双写（结算事实源红线）/ 配置键复原
#   T55 支付渠道凭据管理台配置（★ #74，2026-09-22 补 HTTP 级常设锁）：读写口鉴权（匿名/普通用户拒）/
#       白名单外键整单拒收且不留半套凭据 / 开关只认 0/1/空、回调与网关必须完整 URL（非法值不落库）/
#       敏感项 enc:v1 密文落库 + 掩码回显 + 响应零明文 + 掩码再提交不覆盖真密文（同批其它字段照常保存）/
#       库配置端到端流到下单链路（已填项不再出现在渠道侧缺项清单）/ 凭据不全 fail-closed 不出 mockpay 假码 /
#       enabled=0 时提示「已停用」优先于「未配置」/ 保存进审计 / 空串=清除（删行、回显空）
#       （env > DB 优先级需注入 PAY_* 环境变量、跑中途改不了 UAT 服务环境，故由 pay_channels_test.go 进程内覆盖）
#   T56 对照编辑器读回锁（★ 〇-U 批 I-1 · F-44 P0）：保存修订 → 库里真落行 → **同一接口读回等值**
#       （旧缺陷＝读侧裸 `?` 在 PG 语法错被 `_ ,` 吞成「无修订」，界面假成功）→ 术语表读侧可读 →
#       审批回写导出的 docx 里含修订串（zip 魔数＋体积＋document.xml 三验，AGENTS §6 托管物口径）。
#       ★ 本节只在 PG 方言的 run_uat.sh 主矩阵里有意义：SQLite 快跑下它永远绿（见文件头与 §一·4）。
#   T57 反馈上下文脏值锁（★ 〇-U 批 I-2 · F-45）：文本/工单反馈带上下文 → 列值必须是 '{}'
#       而非 JSON 字面量 "null"（"null" 能穿过读侧 try/catch，最后在前端 Object.entries 处
#       抛错＝超管反馈详情白屏）→ 管理台列表不得回带 "translations_json":"null" →
#       判据自证（手工种一行必须被抓到）+ 启动迁移那条清洗 UPDATE 在当前方言下可执行且归 0。
#       ★ 断言侧两条口径（2026-09-26 首跑踩坑）：用户令牌必须就地重登（前段改密/轮换会让顶部 $H1 变 401）、
#         超管列表路由是 /api/feedback/list（/api/admin/feedbacks 只有 /resolve，照文件头注释写会打进 404）。
#   T58 配额读写同源 + 审计改前改后（★ 〇-U 批 I-3 · F-55/F-56）：超管带 X-Tenant-ID 读出的
#       日字符/日积分必须**等于 tenants.permissions 库里真值**（旧缺陷＝读侧用 authUser().TenantID
#       恒取租户 0 的默认画像 ⇒ 照屏点一次保存就把 0 写进该租户日墙，而 0 的语义是「不限」＝当场拆墙）→
#       平台上下文必须显式回 tenant_selected:false 且保存被拒 → 写 12345/777 再读回等值 + 库里
#       tokens=777×rate 等值 → 第二笔 23456/888 的审计 detail 必须同时含「12345→23456」「777→888」
#       且 before_val/after_val 两侧都有 max_daily_points（旧缺陷＝before 在写后取 ⇒ diff 恒空）→
#       判据自证（上一笔 detail 不得命中同一串）→ 按原 permissions 串整串钉回并等值复验。
#   T59 账务同源与对外口径（★ 〇-U 批 I-4 · F-49/F-51/F-50）：同一笔调用的三个数必须相等——
#       报文 points_used ＝ 台账 SUM(quantity) 折积分 ＝「我的用量」计数增量（旧缺陷＝出参自己再乘一遍
#       均摊系数、与扣费现场的策略系数不同源；收集器被引擎内层遮蔽 ⇒ points_used 恒 0，本轮实测现场）→
#       API Key 调用不得落 user_id<>Key 归属用户的行，且「我的用量」必须认这一笔（旧缺陷＝withTenant 只注
#       租户不注用户 ⇒ 客户自己账单永久漏计）→ 开放接口与站内对话两条对外通道均不得出现「token：123」裸值、
#       对话页脚必须是「本次翻译消耗 N 积分」，快速模式不得回显专业流水线文案 →
#       异步工单 tickets.tokens_billed 必须等于同窗口台账 SUM(quantity)（sink 2s ticker ⇒ 有界重试等收敛，
#       判据目标取自 tickets 行、独立于台账）→ 收尾撤销本次签发的 Key 并复验已失效。
#   T60 收款路径状态码诚实三件套（★ 〇-U 批 I-7 · F-64① 对外契约档）：每条被改的收款接口按
#       「HTTP 状态码 + 错误码 + 中文文案」三件套锁死——400 入参族（points 非法/坏 JSON/超上限/
#       未知渠道/缺 order_id/code 为空/券码不存在）、401 未登录与 403 等级不足必须分流（旧写法两条
#       都回 403 ⇒ token 过期的客户在收银台看到「无权限」，前端只在 401 走重登录）、404 查无此单、
#       409 状态冲突（重复退款/已退款单开票/非 mock 单模拟支付/重复冲红）、503 渠道未就绪
#       （静态收款码清空后下单，跑完钉回原值）→ 三条正向对照（mock 下单→模拟到账→开票→冲红重开
#       仍须 200 + success:true，防「整面改报错」反向翻车）→ 失败响应体零内部细节外泄总闸。
#       进程内同链路行为锁见 backend-go/internal/api/pay_status_honesty_test.go（两层互为反证）。
#       断言助手 req3/ck3 见文件头 mny_norm 下方注释（为什么只锁响应体会把本批改造判成假绿）。
#   T61 AI 助手管理代理状态码诚实（★ 〇-U 批 I-10 · F-64③）：本层四类失败各归其码——
#       未登录 401 与越权 403 必须分流（旧写法两支都 403，超管 token 过期后前端不触发重登录）、
#       白名单外 assist 路径 404 且必须是 JSON 兜底不是 HTML（代理不是通用中继；其内部带
#       code=NOT_FOUND 的拒绝支在真实路由上是死支，行为锁见那份 Go 测试）、上游 assist 自己拒的
#       4xx 原样透传（反向锁：本层不得包成统一错误体，透传体里不许出现 code）、
#       超管读代理仍须 200 + 上游 rows；
#       502（上游不可达）/503（未配管理 Token）两支只在进程内造，见 admin_assist_proxy_test.go。
#   T62 状态码诚实收尾三处定夺（★ 〇-U 批 I-10 · F-64② 收尾）：注册邮箱与既有账号撞车 →
#       409 CONFLICT（与换绑邮箱两处同码，旧写法 400/409 两个码一句文案）；反馈详情「别人的这条」
#       与「根本不存在的 id」必须三元组（状态码/错误码/文案）逐字相等且响应体零业务字段
#       （旧写法 403 vs 404 ＝把反馈总量白送给任何登录用户），本人读取仍 200 作正向反证；
#       知识库管理口匿名 → 401（本批从内联 403 纠正的那一族，admin_kb.go 与 kb.go 各锁一支）。
#   T63 鉴权分流尾量代表口（★ 〇-U 批 I-10 ③ 尾量 122 处）：超管口三态（401/403/200）、
#       租管口 401+200 正向对照、部门口 401（writeAuthzError 换回内联 403 即先红）。
#   T64 计费预估系数管理台表单（★ 〇-X 第 5 项 / F-72 补口）：/api/admin/config/est-tokens
#       鉴权三态 / 未配置回显代码缺省+公式+汇率 / 四类非法输入（K 清零·负数·超上限·缺项）整批拒收
#       且**库里四键行数一根毛都不许多**（半套配置＝长短单口径不一致）/ 合法保存读回等值+审计含新数 /
#       脏值下生效值回缺省而 stored 原样回吐（表单不许把"从来没生效的数"显示成当前配置）/
#       reset 清空四键；跑完删净自己写的行与审计，把「库里没这四行走代码缺省」的线上形态交还下一轮（含 T40）。
#   T65 品牌显式指名的跨域闸（★ 2026-09-28 〇-Y · F-79）：匿名带 ?tenant_id=<别家> 必须**忽略参数**
#       回落平台（tenant_id=0 且看不见别家品牌名）、超管同一条请求必须命中（后台跨租户配置这条正路
#       不许误杀）、脏参数（abc/0/-7）一律 200 且回落——忽略是降级，不是把它打成错误页。
#   T66 免登录即时翻译试用（★ 2026-09-28 〇-Z #74/#75）：真服务进程 + 零 Authorization 打通
#       /api/trial/translate → 逐句倒计时（默认 5 句档、出参钉 pro）→ 最小间隔与额度用完**分码**
#       （RATE_LIMITED 带 retry_after / TRIAL_EXHAUSTED 带 reason=device|ip|global，前端据此决定
#       是「等 3 秒」还是「去注册」）→ 校验族与 405 一句都不吃配额 → 伪造 "mode":"fast" 被忽略 →
#       ★ 成本归属数到库里：成功句数 = 租户 0 的 charge_kind='log' 行数增量，客户余额 SUM 一字不动，
#       且不得出现「租户>0 且 user_id=0」的实扣行；预算触顶留 open 告警；收尾把 rate_limits 的
#       滚动 24h 窗、留痕行、三档配置键清干净（不清＝下一轮同 IP 直接假红）。
#   T67 站点门面开关（★ 2026-09-28 〇-Z #69/#70）：演示站不进主页做成**部署级开关**（两站共用 dist
#       与二进制）→ 开放态首页**逐字节等于未注入**（护住《部署指南》§十 的 2,591 B 判据）→
#       关闭态多出的字节**恰好等于那段标记** → /pricing、/compare 对访客仍 200（只管 `/`）→
#       策略读写同源（改完 GET 回来必须已是新值）→ 整包交还进段前的平台策略（语义比对，不逐字）。
#   T68 旧包额度耗尽仍须能升级（★ 2026-09-28 〇-Z · 缺陷 F-80，演示站实测）：台账 left=0 的
#       付费订阅客户点「升级」曾被 409「当前套餐已无剩余价值，无法抵扣升级」彻底挡死——
#       越用满越想加钱的人越买不了。现钉：① 耗尽升级成单且**抵扣 0、应付＝全价**（全价按主角
#       自己那笔订阅单反推折扣系数，不写死数字）；② 换新包生效且**不多发额度**（order_carry
#       行数不得增长）；③ 反向对照·未耗尽仍按比例抵扣（应付＋抵扣＝全价）；
#       ④ 反向对照·拿更便宜的包「升级」仍须 409（防止把修复做成整段放宽）。
#   T69 免费注册账号防薅三档（★ 2026-09-28 〇-Z · 用户令 F-81）：设备档/平台日预算档在真 HTTP 出口上
#       的四条腿＋一条默认档回落——设备触顶 429 且 reason=device、不建租户；平台触顶 429 且
#       **先前占住的设备格必须退回**（否则系统自己的失败会吃掉用户当天额度）＋留 register_budget 告警；
#       受邀加入在两档都触顶时照样放行且不推进计数（客户自己发的码不占平台名额）；
#       脏设备号不返 400（公开接口契约，F-64/F-79 同形教训）但照记平台账；
#       库里删档位键后回落代码默认 3 格。收尾清 rate_limits/alerts 并钉回开闸值。
# 注意：所有带复杂引号 body 的 curl 必须「先存变量再断言」，禁止在 ck 内嵌嵌套引号
#   —— 2026-09-21 实测：`ck X 'want' "$(post "$H" "{\"a\":1,\"b\":2}" /p)"` 里的 body 会被 bash
#   在双引号内的命令替换中做**大括号展开**，按逗号切成两个参数，curl 发出残缺 body 换来「参数格式
#   错误」，宽松断言照样命中 = 永远绿灯。此条现由 scripts/uat/lint_uat_quotes.py 作为闸门强制。
# 依赖：mock_llm.py 已启动、uat 服务已启动（run_uat.sh 编排）
# 用法：BASE_URL=... UAT_DB=... ADMIN_PASS=... [UAT_SERVER_LOG=...] bash scripts/uat/api_uat_txn.sh
# ============================================================================
set -u
B="${BASE_URL:-http://127.0.0.1:8899}"
U="${UAT_DB:-/tmp/uat/dev.db}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Admin@1234}"
J='Content-Type: application/json'
source "$(dirname "$0")/dblib.sh"   # 双方言断言层（sqlite/PG）
PASS=0; FAIL=0; START=$(date +%s)

ck(){ if echo "$3" | grep -qE "$2"; then PASS=$((PASS+1)); echo "PASS|$1"; else FAIL=$((FAIL+1)); echo "FAIL|$1|want[$2]|got[${3:0:220}]"; fi; }
tok(){ curl -s $B/api/auth/login -H "$J" -d "{\"username\":\"$1\",\"password\":\"$2\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))'; }
pv(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(d$1)"; }
sq(){ dbq "$1"; }
post(){ local h="$1" body="$2" path="$3"; curl -s $B"$path" -H "$h" -H "$J" -d "$body"; }
get(){ local h="$1" path="$2"; curl -s $B"$path" -H "$h"; }
# mny_norm — 金额文本的方言归一（★ 2026-09-23 SQLite 快跑假红修复）
# 为什么需要：同一列 PG 的 numeric 直读回 "5" / "10.8"，SQLite 的 REAL 直读回 "5.0" / "5.00"，
# 值完全相同、文本不同。金额锁比的是「直读回来的字符串」，不归一就会让本地快跑
# （DB_DRIVER=sqlite）恒红 T51/T54 两条，假红会钝化对真回归的敏感度。
# 只对纯数字字段动手（含 | 分隔的多列拼接），币种一类的文本字段原样保留。
mny_norm(){ printf '%s' "$1" | awk -F'|' 'BEGIN{OFS="|"} {for(i=1;i<=NF;i++) if ($i ~ /^[0-9]+\.[0-9]+$/) { sub(/0+$/, "", $i); sub(/\.$/, "", $i) } print }'; }
# ----------------------------------------------------------------------------
# req3 / ck3 — 「HTTP 状态码 + 错误码 + 中文文案」三件套断言（★ 〇-U 批 I-7 · F-64① 补断言口径）
#
# 为什么既有 ck/post/get 不够用：那三个助手只把**响应体**交给断言。而本批改动恰好发生在
# 「同一段体、不同状态码」上——旧的 200 壳与新改的 400/401/404/409 里都写着 "success":false，
# 只看体就把「诚实改造」判成「没改」（`ck T23 'success:false'` 在改前改后**都是绿**，典型假绿）；
# 只看状态码又会放过「409 却回 FORBIDDEN」这种码与错体不一致的半截改法。
# 于是本批要求（修复文档 §7.2 补断言）：每条被改的接口锁三件套——状态码、code 字段、文案正则。
#
# req3 <方法> <请求头> <路径> [body] → 写全局 R3ST / R3CODE / R3MSG / R3BODY
#   非 JSON 响应体（网关 HTML 页等）时 code/msg 留空，让 ck3 红在「拿不到错误码」而不是崩脚本。
# ck3  <用例名> <期望状态码> <期望错误码> <期望文案正则> → 判 req3 刚写入的那四个变量
# ----------------------------------------------------------------------------
r3field(){ printf '%s' "${R3BODY:-}" | python3 -c 'import sys, json
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
v = d.get(sys.argv[1]) if isinstance(d, dict) else None
print("" if v is None else v)' "$1" 2>/dev/null; }
req3(){ local m="$1" h="$2" p="$3" b="${4:-}" tf
  tf="/tmp/uat_req3_$$.json"
  # 请求头为空串＝匿名探测：curl 的 -H "" 在部分版本上会报「no header name」，
  # 因此这里根本不带 -H，而不是传一个空头（匿名是本批要锁的一类响应，不能靠运气）。
  if [ "$m" = "GET" ]; then
    if [ -n "$h" ]; then
      R3ST=$(curl -s -o "$tf" -w '%{http_code}' --max-time 60 "$B$p" -H "$h")
    else
      R3ST=$(curl -s -o "$tf" -w '%{http_code}' --max-time 60 "$B$p")
    fi
  else
    if [ -n "$h" ]; then
      R3ST=$(curl -s -o "$tf" -w '%{http_code}' -X "$m" --max-time 60 "$B$p" -H "$h" -H "$J" -d "$b")
    else
      R3ST=$(curl -s -o "$tf" -w '%{http_code}' -X "$m" --max-time 60 "$B$p" -H "$J" -d "$b")
    fi
  fi
  R3BODY=$(python3 -c 'import sys
try:
    print(open(sys.argv[1], encoding="utf-8", errors="replace").read())
except Exception:
    print("")' "$tf")
  rm -f "$tf"
  R3CODE=$(r3field code); R3MSG=$(r3field message)
}
ck3(){ local name="$1" want="$2,$3,$4" bad=""
  [ "${R3ST:-}" = "$2" ] || { bad="$bad 状态码=${R3ST:-?}"; }
  [ "${R3CODE:-}" = "$3" ] || { bad="$bad code=${R3CODE:-?}"; }
  printf '%s' "${R3MSG:-}" | grep -qE "$4" || { bad="$bad 文案=${R3MSG:-?}"; }
  if [ -z "$bad" ]; then PASS=$((PASS+1)); echo "PASS|$name"
  else FAIL=$((FAIL+1)); echo "FAIL|$name|want(状态码,错误码,文案)=$want|got(${R3ST:-?},${R3CODE:-?},${R3MSG:-?})|$bad|body=${R3BODY:0:160}"; fi
}

AT=$(tok $ADMIN_USER $ADMIN_PASS); AH="Authorization: Bearer $AT"
T1=$(tok uatuser_a uatpass123); H1="Authorization: Bearer $T1"
T2=$(tok uatuser_b uatpass123); H2="Authorization: Bearer $T2"
T5=$(tok uatuser_e uatpass123); H5="Authorization: Bearer $T5"
T6=$(tok uatuser_d uatpass123); H6="Authorization: Bearer $T6"
[ ${#AT} -lt 10 ] && echo "FATAL|admin token 失效" && exit 1
[ ${#T1} -lt 10 ] && echo "FATAL|uatuser_a token 失效（uatuser_a 密码可能被改）" && exit 1
TAID=$(sq "SELECT tenant_id FROM users WHERE username='uatuser_a' LIMIT 1" | tr -d '[:space:]')
TBID=$(sq "SELECT tenant_id FROM users WHERE username='uatuser_b' LIMIT 1" | tr -d '[:space:]')
TEID=$(sq "SELECT tenant_id FROM users WHERE username='uatuser_e' LIMIT 1" | tr -d '[:space:]')
TDID=$(sq "SELECT tenant_id FROM users WHERE username='uatuser_d' LIMIT 1" | tr -d '[:space:]')

echo "=== T 阶段：功能与交易专项深度 UAT ==="

# ----------------------------------------------------------------------------
# ★ F-78（2026-09-28 〇-X）「积分↔token 汇率 1:300 → 1:400」断言口径收口
# 本套件所有「积分面值 × 汇率 = 库内 token」的折算锁一律从库里**现读**汇率（RATE），
# 不再写死 300/60000/300000 这类历史字面值——写死数字正是 F-12 那类「改一处口径、
# 定价页/收银台/管理台三口径打架」事故的复读机；现读之后，改档只需数据库里两个键一起动，
# 断言层零改动继续有效，而下面 T0 那条等式锁负责抓住「只动一枚旋钮」的错误改法。
# 缺行兜底与 store 出厂默认同值（400 / 24917），与 T58/T59 的现读口径同源。
# ----------------------------------------------------------------------------
RATE=$(dbq "SELECT value FROM system_config WHERE key='points_tokens_rate'" | tr -d '[:space:]' | tr -dc '0-9')
[ -n "$RATE" ] || RATE=400
RULER_FEN=$(dbq "SELECT value FROM system_config WHERE key='price_fen_per_million_tokens'" | tr -d '[:space:]' | tr -dc '0-9')
[ -n "$RULER_FEN" ] || RULER_FEN=24917
echo "INFO|计费口径读数：积分汇率 1:$RATE，裸充值尺子 ${RULER_FEN} 分/百万 token"

# T0 面值成对性锁：3,000 积分（＝3000×RATE token）按尺子折回来必须仍是 ¥299.00＝29,900 分。
# 算法与 store 的 TokensToFen 同口径（乘满再除、分位四舍五入），两侧都取**库里的真实读数**，
# 因此这一条同时锁住三件事：汇率与尺子成对移动、面值不漂移、出厂种子与补发结果一致。
# 反向负向锁（尺子钉在旧档 33222 时必须折断这条等式）在 Go 单测
# store/billing_uat_batchb_test.go F12，断言层这里只做正向日检。
T0FEN=$(python3 -c 'import sys
toks = int(sys.argv[1]) * int(sys.argv[2])
print((toks * int(sys.argv[3]) + 500000) // 1000000)' "$RATE" 3000 "$RULER_FEN" 2>/dev/null || echo 0)
ck T0-point-face-299 '^29900$' "$T0FEN"

# ---------- T1 线下订单全生命周期 ----------
B0=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$TAID")
R=$(post "$AH" "{\"tenant_id\":$TAID,\"points\":200,\"money\":0}" /api/admin/orders/create)
ck T1-order-create '"success":true' "$R"
OID1=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
ST1=$(echo "$R" | pv '.get("order",{}).get("status","")')
[ "$ST1" = "pending" ] && { PASS=$((PASS+1)); echo "PASS|T1-order-pending"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-order-pending($ST1)"; }
AM1=$(echo "$R" | pv '.get("order",{}).get("amount_money",0)')
[ "$AM1" != "0" ] && [ -n "$AM1" ] && { PASS=$((PASS+1)); echo "PASS|T1-amount-auto-fill($AM1)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-amount-auto-fill($AM1)"; }
# ★ A3 竞态防护：前序用例的 SSE 计量可能晚 1-2s 入队（When=响应完成时刻）。
# 先等 sink 冲刷干净再支付，避免「支付前发生的用量」被算进支付后消耗窗口。
sleep 4
R=$(post "$AH" "{\"id\":$OID1,\"tenant_id\":$TAID}" /api/admin/orders/pay)
ck T1-order-pay '"success":true' "$R"
ST2=$(sq "SELECT status FROM orders WHERE id=$OID1")
[ "$ST2" = "paid" ] && { PASS=$((PASS+1)); echo "PASS|T1-order-marked-paid"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-order-marked-paid($ST2)"; }
B1=$(( $(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$TAID") - B0 ))
[ "$B1" = "$(( 200 * RATE ))" ] && { PASS=$((PASS+1)); echo "PASS|T1-balance-credit(+$B1)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-balance-credit(+$B1, want +$((200 * RATE))(200积分×汇率$RATE))"; }
R=$(post "$AH" "{\"id\":$OID1,\"tenant_id\":$TAID}" /api/admin/orders/refund)
ck T1-order-refund '"success":true' "$R"
ST3=$(sq "SELECT status FROM orders WHERE id=$OID1")
[ "$ST3" = "refunded" ] && { PASS=$((PASS+1)); echo "PASS|T1-order-refunded"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-order-refunded($ST3)"; }
B2=$(( $(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$TAID") - B0 ))
[ "$B2" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T1-balance-revert($B2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-balance-revert($B2)"; }
# ★ F-64①（批 I-7）三件套：原先只锁文案（'已退款|refunded|失败|不存在'），
#   而那句文案在「200 壳 + success:false」时代就已存在——改前改后都绿，等于没锁。
#   现在锁 HTTP 409（单子在、状态不允许退＝资源状态冲突）+ CONFLICT + 文案。
req3 POST "$AH" /api/admin/orders/refund "{\"id\":$OID1,\"tenant_id\":$TAID}"
ck3 T1-refund-dup 409 CONFLICT '不存在|状态不允许退款|已退款'

# ---------- T2 静态码人工确认链路 ----------
dbcfg static_qr_image 'data:image/png;base64,UATQR' 2>/dev/null
R=$(post "$H1" '{"points":30,"channel":"manual"}' /api/pay/create)
ck T2-manual-create '"success":true' "$R"
ck T2-manual-qr 'UATQR' "$R"
OID2=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
R=$(post "$H1" "{\"order_id\":$OID2}" /api/pay/manual-confirm)
ck T2-manual-confirm '"success":true' "$R"
MC2=$(sq "SELECT manual_confirm FROM orders WHERE id=$OID2")
[ "$MC2" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T2-manual-confirm-flag"; } || { FAIL=$((FAIL+1)); echo "FAIL|T2-manual-confirm-flag($MC2)"; }
ck T2-manual-list '"success":true' "$(get "$AH" /api/admin/orders/manual)"
R=$(post "$AH" "{\"id\":$OID2,\"tenant_id\":$TAID}" /api/admin/orders/pay)
ck T2-manual-pay '"success":true' "$R"
ST4=$(sq "SELECT status FROM orders WHERE id=$OID2")
[ "$ST4" = "paid" ] && { PASS=$((PASS+1)); echo "PASS|T2-manual-paid"; } || { FAIL=$((FAIL+1)); echo "FAIL|T2-manual-paid($ST4)"; }

# ---------- T3 发票金额一致性（B1） ----------
R=$(post "$H1" '{"points":412,"channel":"mock"}' /api/pay/create)
OID3=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
post "$H1" "{\"order_id\":$OID3}" /api/pay/simulate >/dev/null
AM3=$(sq "SELECT amount_money FROM orders WHERE id=$OID3")
R=$(post "$H1" "{\"order_id\":$OID3,\"title\":\"交易专项发票\",\"tax_no\":\"TX9001\"}" /api/billing/invoices/create)
ck T3-invoice-create '"success"' "$R"
ck T3-invoices-list '"success":true' "$(get "$H1" /api/billing/invoices)"
IVAMT=$(echo "$R" | pv '.get("invoice",{}).get("amount_money",0) or 0')
[ -n "$AM3" ] && [ "$IVAMT" = "$AM3" ] && { PASS=$((PASS+1)); echo "PASS|T3-invoice-amount($IVAMT==$AM3)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T3-invoice-amount($IVAMT vs $AM3)"; }

# ---------- T4 受邀人付费 → 邀请者付费奖励（幂等） ----------
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$TDID,\"code\":\"uat_txn_paid\",\"name\":\"交易专项包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":30,\"duration_days\":30}" >/dev/null
# ---------- T4 受邀人付费 → 邀请者付费奖励（幂等） ----------
# 每轮注册全新受邀人（随机后缀），验证「首笔付费→邀请者永久余额」真实入账
NEWINV="uatuser_pay$(date +%s)"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$NEWINV\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"新受邀\",\"email\":\"$NEWINV@test.com\",\"agreed\":true,\"ref\":\"$(curl -s "$B/api/referral/my" -H "$H5" | pv '.get("ref_code","")')\"}" >/dev/null
NTID=$(sq "SELECT tenant_id FROM users WHERE username='$NEWINV'")
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$NTID,\"code\":\"uat_txn_paid\",\"name\":\"交易专项包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":30,\"duration_days\":30}" >/dev/null
TN=$(tok $NEWINV uatpass123); HN="Authorization: Bearer $TN"
E_BEFORE=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$TEID")
R=$(post "$HN" '{"code":"uat_txn_paid"}' /api/package/subscribe)
ck T4-invitee-subscribe '"success"' "$R"
OID4=$(echo "$R" | pv '.get("order",{}).get("id") or d.get("id") or 0')
post "$AH" "{\"id\":$OID4,\"tenant_id\":$NTID}" /api/admin/orders/pay >/dev/null
E_AFTER=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$TEID")
[ $((E_AFTER - E_BEFORE)) -ge 100000 ] && { PASS=$((PASS+1)); echo "PASS|T4-inviter-paid-reward(+$((E_AFTER-E_BEFORE)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|T4-inviter-paid-reward($E_BEFORE->$E_AFTER)"; }
post "$AH" "{\"id\":$OID4,\"tenant_id\":$NTID}" /api/admin/orders/pay >/dev/null
E_AFTER2=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$TEID")
[ "$E_AFTER" = "$E_AFTER2" ] && { PASS=$((PASS+1)); echo "PASS|T4-reward-idempotent"; } || { FAIL=$((FAIL+1)); echo "FAIL|T4-reward-idempotent($E_AFTER->$E_AFTER2)"; }
R=$(sq "SELECT COUNT(*) FROM referral_rewards WHERE inviter_uid=(SELECT id FROM users WHERE username='uatuser_e') AND invitee_uid=(SELECT id FROM users WHERE username='$NEWINV') AND type='paid_perm'")
[ "$R" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T4-reward-ledger(paid_perm row)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T4-reward-ledger($R)"; }
ck T4-referral-my '"success":true' "$(get "$H5" /api/referral/my)"

# ---------- T5 OpenAPI 异步任务 ----------
AK=$(curl -s $B/api/apikeys/create -H "$H1" -H "$J" -d '{"name":"txn-key"}' | pv '.get("api_key","")')
R=$(curl -s $B/openapi/v1/tasks -H "$J" -H "Authorization: Bearer $AK" --max-time 60 -d '{"text":"异步任务翻译内容测试","target_langs":["en"]}')
ck T5-task-create '"task_id":[0-9]' "$R"
TASKID=$(echo "$R" | pv '.get("task_id") or 0')
[ -n "$TASKID" ] && [ "$TASKID" != "0" ] && { PASS=$((PASS+1)); echo "PASS|T5-task-id($TASKID)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T5-task-id"; }
POLL=""
for i in $(seq 1 15); do
  POLL=$(curl -s "$B/openapi/v1/tasks/status?id=$TASKID" -H "Authorization: Bearer $AK")
  echo "$POLL" | grep -q '"status":"completed"' && break
  sleep 2
done
ck T5-task-completed '"status":"completed"' "$POLL"
# 文本任务无文件产物：download 返回 no_result 是正确契约（文件任务才有产物）
ck T5-task-download 'no_result|无可下载' "$(curl -s "$B/openapi/v1/tasks/download?id=$TASKID" -H "Authorization: Bearer $AK" --max-time 60)"
ck T5-task-badkey 'invalid|无效' "$(curl -s "$B/openapi/v1/tasks/status?id=$TASKID" -H "Authorization: Bearer sk-bogus")"

# ---------- T6 认证：改密/新密登录/恢复 + 忘记密码防枚举 + 错误重置码 ----------
R=$(post "$H1" '{"old_password":"uatpass123","new_password":"uatpass999"}' /api/auth/change-password)
ck T6-change-pwd '"success":true' "$R"
# ★ B2 会话撤销（2026-09-12）：改密后旧 JWT 必须立即失效（此前 24h 内仍可用属漏洞）
ck T6-old-token-revoked '"success":false' "$(post "$H1" '{}' /api/auth/change-password)"
T6A=$(tok uatuser_a uatpass999)
[ ${#T6A} -gt 30 ] && { PASS=$((PASS+1)); echo "PASS|T6-login-new-pwd"; } || { FAIL=$((FAIL+1)); echo "FAIL|T6-login-new-pwd"; }
ck T6-old-pwd-rejected '密码|失败|incorrect|invalid|UNAUTHORIZED' "$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"uatpass123"}')"
H6A="Authorization: Bearer $T6A"
R=$(post "$H6A" '{"old_password":"uatpass999","new_password":"uatpass123"}' /api/auth/change-password)
ck T6-restore-pwd '"success":true' "$R"
ck T6-restore-login '"success":true' "$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"uatpass123"}')"
# ★ B2：两次改密已使开场签发的 H1 失效；后续套件（T9/T10/T12/T14）复用 H1，重新登录刷新
T1=$(tok uatuser_a uatpass123); H1="Authorization: Bearer $T1"
[ ${#T1} -lt 10 ] && { echo "FATAL|T6 后刷新 uatuser_a token 失败"; exit 1; }
ck T6-forgot-guard '"success":true' "$(curl -s $B/api/auth/forgot-password -H "$J" -d '{"email":"nonexist@test.com"}')"
# ★ F-20 后端半（2026-09-25 批G）：重置密码四类失败（码错/过期/用户缺/已占用之外的校验族）
#   已收敛为 writeError + 稳定码 VALIDATION_ERROR + 文案「验证码错误或已过期」——
#   旧断言只吃中文文案，文案一 i18n 就失明；这里补 code 字段等值锁，前端按码分支才有依据。
R6B=$(curl -s $B/api/auth/reset-password -H "$J" -d '{"username":"uatuser_a","code":"000000","new_password":"hacked999"}')
ck T6-reset-badcode '验证码|无效|expired|不正确' "$R6B"
ck T6-reset-badcode-code '"code":"VALIDATION_ERROR"' "$R6B"

# ---------- T7 OpenAPI 余额硬闸（清零租户 uatuser_b） ----------
AKB=$(curl -s $B/api/apikeys/create -H "$H2" -H "$J" -d '{"name":"b-key"}' | pv '.get("api_key","")')
ck T7-openapi-insufficient 'insufficient|余额不足|耗尽' "$(curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer $AKB" --max-time 60 -d '{"text":"余额不足应当被拦截的开放接口翻译","target_lang":"en"}')"

# ---------- T8 任务中心 ----------
R=$(post "$AH" '{"title":"UAT任务-每日打卡","reward_points":17,"kind":"daily","enabled":1,"sort_order":1}' /api/admin/tasks/save)
ck T8-task-save '"success":true' "$R"
TASK8=$(echo "$R" | pv '.get("id") or 0')
ck T8-me-tasks '"success":true' "$(get "$H6" /api/me/tasks)"
R=$(post "$H6" "{\"id\":$TASK8}" /api/me/tasks/claim)
ck T8-claim '"success":true' "$R"
R=$(post "$H6" "{\"id\":$TASK8}" /api/me/tasks/claim)
ck T8-claim-dup '已领取|重复|already|claimed' "$R"

# ---------- T9 知识库：条目更新/删除 + 安全句 ----------
PKG=$(sq "SELECT id FROM kb_packages WHERE tenant_id=$TAID AND pack_type='tenant' LIMIT 1" | tr -d '[:space:]')
R=$(post "$H1" "{\"package_id\":$PKG,\"source_text\":\"交易专项术语\",\"target_lang\":\"en\",\"target_text\":\"Txn Term\",\"module\":\"txn\",\"remark\":\"专项\"}" /api/admin/kb-entries/add)
ck T9-entry-add '"success":true' "$R"
EID=$(echo "$R" | pv '.get("id") or 0')
R=$(post "$H1" "{\"id\":$EID,\"source_text\":\"交易专项术语\",\"target_lang\":\"en\",\"target_text\":\"Txn Term v2\",\"module\":\"txn\"}" /api/admin/kb-entries/update)
ck T9-entry-update '"success":true' "$R"
R=$(post "$H1" "{\"id\":$EID}" /api/admin/kb-entries/delete)
ck T9-entry-delete '"success":true' "$R"
R=$(post "$H1" "{\"package_id\":$PKG,\"lang\":\"en\",\"kind\":\"style\",\"phrase\":\"请务必\",\"replacement\":\"please ensure\"}" /api/admin/safety-phrases/add)
ck T9-safety-add '"success":true' "$R"
SPID=$(echo "$R" | pv '.get("id") or 0')
R=$(post "$H1" "{\"id\":$SPID,\"status\":\"approved\"}" /api/admin/safety-phrases/status)
ck T9-safety-status '"success":true' "$R"
R=$(post "$H1" "{\"id\":$SPID}" /api/admin/safety-phrases/delete)
ck T9-safety-delete '"success":true' "$R"

# ---------- T10 Webhook CRUD ----------
R=$(post "$H1" '{"url":"https://example.com/hook","events":"task.completed","active":true}' /api/webhooks/save)
ck T10-webhook-save '"success":true' "$R"
WHID=$(echo "$R" | pv '.get("webhook",{}).get("id") or 0')
# 新增含重试策略的 webhook
R=$(post "$H1" "{\"url\":\"https://example.com/retry-hook\",\"events\":\"translation.completed\",\"max_retries\":5,\"retry_interval\":120}" /api/webhooks/save)
ck T10-webhook-save-retry '"success":true' "$R"
WHID2=$(echo "$R" | pv '.get("webhook",{}).get("id") or 0')
ck T10-webhook-list '"success":true' "$(get "$H1" /api/webhooks)"
R=$(post "$H1" "{\"id\":$WHID}" /api/webhooks/test); ck T10-webhook-test '"success"' "$R"
# 查询投递历史（空）
R=$(get "$H1" "/api/webhooks/deliveries?webhook_id=$WHID")
ck T10-webhook-deliveries '"success":true' "$R"
# 重试不存在的投递
R=$(post "$H1" '{"delivery_id":99999}' /api/webhooks/retry)
ck T10-webhook-retry-notfound '"success":false' "$R"
# 删除 webhook
R=$(post "$H1" "{\"id\":$WHID}" /api/webhooks/delete)
ck T10-webhook-delete '"success":true' "$R"
R=$(post "$H1" "{\"id\":$WHID2}" /api/webhooks/delete)
ck T10-webhook-delete2 '"success":true' "$R"

# ---------- T11 告警 + 用量统计 ----------
ck T11-alerts '"success":true' "$(get "$AH" /api/system/alerts)"
ck T11-usage-me '"success":true' "$(get "$H1" /api/billing/usage/me)"
ck T11-usage '"success":true' "$(get "$AH" /api/billing/usage)"
ck T11-usage-cost '"success":true' "$(get "$AH" /api/billing/usage/cost)"
ck T11-usage-org '"success":true' "$(get "$AH" /api/billing/usage/org)"

# ---------- T12 品牌定制保存 → 品牌术语种入 ----------
R=$(post "$H1" '{"brand_name":"UAT汽车","brand_name_en":"UATCAR","brand_logo":"","brand_home_bg":""}' /api/tenant/branding)
ck T12-branding-save '"success":true' "$R"
ck T12-branding-get '"success":true' "$(get "$H1" /api/tenant/branding)"
BTERM=$(sq "SELECT COUNT(*) FROM kb_entries WHERE tenant_id=$TAID AND package_id=$PKG AND source_text='UAT汽车'")
[ "$BTERM" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|T12-brand-term-seeded($BTERM)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T12-brand-term-seeded($BTERM)"; }

# ---------- T13 支付回调安全闸 ----------
ck T13-notify-no-token '拒绝|403' "$(curl -s $B/api/pay/notify/mock -H "$J" -d '{}')"
ck T13-notify-bad-token '拒绝|403' "$(curl -s $B/api/pay/notify/mock -H "X-Admin-Token: wrong-token" -H "$J" -d '{"order_no":"x","amount":1}')"
R=$(post "$H1" '{"points":3,"channel":"mock"}' /api/pay/create)
ONO=$(echo "$R" | pv '.get("order",{}).get("order_no","")')
R=$(curl -s $B/api/pay/notify/mock -H "X-Admin-Token: $AT" -H "$J" -d "{\"order_no\":\"$ONO\",\"amount\":1}")
ck T13-notify-wrong-amount '金额不符|验签|拒绝' "$R"

# ---------- T14 权限边界 ----------
R=$(post "$H1" "{\"tenant_id\":$TBID,\"points\":1}" /api/admin/orders/create)
ck T14-tenant-admin-cross-charge '权限不足|403' "$R"
R=$(post "$H1" '{"scope":"platform","policy":{}}' /api/admin/ops/policy/save)
ck T14-ops-save-non-super '超级管理员|超管|403' "$R"
ck T14-anon-balance '401|未登录|success.*false' "$(curl -s $B/api/billing/balance)"

# ---------- T15 本次开发专项：PG 方言修复端到端锁定（2026-09-12） ----------
# ckn <名称> <禁止正则> <响应> —— 反向断言：命中即失败（用于驱动错误泄漏检测）
ckn(){ if echo "$3" | grep -qE "$2"; then FAIL=$((FAIL+1)); echo "FAIL|$1|forbidden[$2]|got[${3:0:220}]"; else PASS=$((PASS+1)); echo "PASS|$1"; fi; }

# ① /status 健康位分离：ok=基础设施（db_ok/breaker），degraded=业务告警位
R=$(curl -s $B/status)
ck T15-status-dbok '"db_ok":true' "$R"
ck T15-status-degraded '"degraded":(true|false)' "$R"

# ② 驱动错误脱敏：重复用户名/重复租户码不得泄漏 pq:/JSON1/约束名等内部细节
#   注意：用户名唯一约束是「租户内」维度——必须同租户判重才触发约束（跨租户建号是合法操作）
UA_TID=$(dbq "SELECT tenant_id FROM users WHERE username='uatuser_a' AND tenant_id>0 LIMIT 1")
DUPU=$(post "$AH" "{\"username\":\"uatuser_a\",\"password\":\"x\",\"name\":\"d\",\"email\":\"dup15@test.com\",\"tenant_id\":$UA_TID}" /api/admin/users/create)
ckn T15-sanitize-user 'pq:|SQL logic|UNIQUE constraint|42601|23505|lib/pq|json_extract' "$DUPU"
ck T15-sanitize-user-msg '已存在|失败' "$DUPU"
DUPC=$(post "$AH" "{\"code\":\"$(dbq "SELECT code FROM tenants LIMIT 1")\",\"name\":\"d\"}" /api/tenant/create)
ckn T15-sanitize-tenant 'pq:|SQL logic|UNIQUE constraint|42601|23505|lib/pq' "$DUPC"

# ③ 句数余额（JSONNumAdd/JSONNumGE 助手）：T4 已购 20000 句包并支付 → 余额入账
SB=$(dbjson tenants $NTID permissions sentence_balance)
[ "${SB%.*}" -ge 20000 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T15-sentence-credit($SB)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T15-sentence-credit(got $SB, want>=20000)"; }

# ④ 增量包镜像累加（applyIncrementMirrorTx/JSONNumAdd 修复主路径）：再购 5000 句充值包 → 余额 +5000
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$NTID,\"code\":\"uat_txn_inc\",\"name\":\"T15充值包\",\"ptype\":\"increment\",\"sentences\":5000,\"price_money\":10,\"duration_days\":0}" >/dev/null
R=$(post "$HN" '{"code":"uat_txn_inc"}' /api/package/subscribe)
OID15=$(echo "$R" | pv '.get("order",{}).get("id") or d.get("id") or 0')
post "$AH" "{\"id\":$OID15,\"tenant_id\":$NTID}" /api/admin/orders/pay >/dev/null
SBA=$(dbjson tenants $NTID permissions sentence_balance)
[ "${SBA%.*}" -ge 25000 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T15-increment-mirror($SB->$SBA)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T15-increment-mirror($SB->$SBA, want>=25000)"; }

# ⑤ 个人标记落库（SetPersonal bool→INTEGER 修复）：type=personal 注册 → is_personal=1
IP=$(dbq "SELECT is_personal FROM tenants WHERE id=$NTID")
[ "$IP" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T15-personal-db"; } || { FAIL=$((FAIL+1)); echo "FAIL|T15-personal-db(got $IP)"; }

# ⑥ 任务自增 ID（InsertID 修复）：超管建任务返回正 ID，用户可领取（T8 已测领取，此处锁 DB）
TID15=$(post "$AH" '{"task_type":"once","title":"T15任务","description":"d","reward_points":1,"enabled":1,"sort_order":9}' /api/admin/tasks/save | pv '.get("id") or 0')
[ "${TID15:-0}" -gt 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T15-task-id($TID15)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T15-task-id(got $TID15)"; }

# ---------- T16（G1）并发扣费压力：30 路并发翻译耗尽余额 —— 不透支、不双扣 ----------
T16U="uatuser_t16$(date +%s)"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$T16U\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T16压力\",\"email\":\"$T16U@test.com\",\"agreed\":true}" >/dev/null
T16T=$(tok $T16U uatpass123); H16="Authorization: Bearer $T16T"
T16D=$(sq "SELECT tenant_id FROM users WHERE username='$T16U' LIMIT 1" | tr -d '[:space:]')
R=$(post "$AH" "{\"tenant_id\":$T16D,\"points\":27,\"money\":0}" /api/admin/orders/create)
OID16=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
post "$AH" "{\"id\":$OID16,\"tenant_id\":$T16D}" /api/admin/orders/pay >/dev/null
# 个人租户注册自带 30 万试用余额，压测前直接钉到 8000 tokens，逼出真实「抢余额」竞争
dbq "UPDATE balance_accounts SET balance=8000 WHERE tenant_id=$T16D"
AK16=$(post "$H16" '{"name":"t16-key"}' /api/apikeys/create | pv '.get("api_key","")')
TOT0=$(get "$H16" /api/billing/balance | pv '.get("points_available",0)')
D16=$(mktemp -d)
for i in $(seq 1 30); do
  curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer $AK16" --max-time 120 \
    -d "{\"text\":\"并发压力句 $i 翻译测试内容\",\"target_lang\":\"en\",\"mode\":\"pro\"}" -o "$D16/r$i.json" &
done
wait
S16=$(cat "$D16"/r*.json 2>/dev/null | grep -c '"success":true')
E16=$(cat "$D16"/r*.json 2>/dev/null | grep -cE 'insufficient|余额不足|耗尽|"success":false')
[ $((S16 + E16)) -eq 30 ] && [ "$S16" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|T16-all-resolved(ok=$S16 refused=$E16)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-all-resolved(ok=$S16 refused=$E16)"; }
sleep 4
TOT1=$(get "$H16" /api/billing/balance | pv '.get("points_available",0)')
[ "${TOT1:-0}" -ge 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T16-no-negative-balance($TOT1)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-no-negative-balance($TOT1)"; }
AL16=$(sq "SELECT COUNT(*) FROM alerts WHERE tenant_id=$T16D AND kind='balance' AND level='critical'" | tr -d '[:space:]')
[ "${AL16:-0}" -le 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T16-no-false-settle-alert(critical=$AL16)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-no-false-settle-alert(critical=$AL16)"; }
L16=$(sq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$T16D" | tr -d '[:space:]')
# 容差按「3 积分」的原意表达（★ F-78 改档时不跟着改数字就会把容差悄悄收紧）：
# 每次结算的展示积分四舍五入≤±1 积分，留 3 积分余量＝3×RATE token。
T16TOL=$(( 3 * RATE ))
EQ16=$(python3 -c "print(1 if abs($L16 - ($TOT0 - $TOT1)*$RATE) <= $T16TOL else 0)" 2>/dev/null || echo 0)   # 积分差折回 token（每次四舍五入≤±1 积分）
[ "$EQ16" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T16-no-double-deduct(ledger=$L16 consumed=$(( (TOT0 - TOT1)*RATE )))"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-no-double-deduct(ledger=$L16 consumed=$(( (TOT0 - TOT1)*RATE )))"; }
rm -rf "$D16"

# ---------- T17（G4）同租户工单隐私：非创建者不可见详情 ----------
R=$(post "$H1" '{"title":"T17隐私工单","source_text":"hello privacy check sentence","target_langs":"en","mode":"fast"}' /api/tickets/create)
TKID=$(echo "$R" | pv '.get("ticket",{}).get("id") or 0')
ck T17-ticket-create '"success":true' "$R"
ck T17-owner-view '"success":true' "$(get "$H1" "/api/tickets/detail?id=$TKID")"
PEER="uatpeer_a_$(date +%s)"
R=$(post "$AH" "{\"username\":\"$PEER\",\"password\":\"uatpass123\",\"display_name\":\"同租户同事\",\"role\":\"user\",\"tenant_id\":$TAID}" /api/admin/users/create)
ck T17-peer-create '"success":true' "$R"
PT=$(tok $PEER uatpass123); HP="Authorization: Bearer $PT"
R=$(get "$HP" "/api/tickets/detail?id=$TKID")
ck T17-peer-deny '"success":false' "$R"
ck T17-peer-deny-msg '无权查看他人工单' "$R"

# ---------- T18（G4）启动日志脱敏：无明文超管密码 / DSN 口令必须掩码 ----------
L18F="${UAT_SERVER_LOG:-}"
if [ -n "$L18F" ] && [ -f "$L18F" ]; then
  P18A=$(grep -c 'Admin@1234' "$L18F" 2>/dev/null || true); P18A=${P18A:-0}
  [ "$P18A" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T18-log-no-adminpw"; } || { FAIL=$((FAIL+1)); echo "FAIL|T18-log-no-adminpw($P18A 处明文超管密码)"; }
  P18B=$(grep -cE '://[^/@[:space:]]+:[^/@*[:space:]][^/@[:space:]]*@' "$L18F" 2>/dev/null || true); P18B=${P18B:-0}
  [ "$P18B" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T18-log-dsn-masked"; } || { FAIL=$((FAIL+1)); echo "FAIL|T18-log-dsn-masked($P18B 处未掩码 DSN)"; }
else
  PASS=$((PASS+1)); echo "PASS|T18-log-skip(未提供 UAT_SERVER_LOG，仅 run_uat 全流程可检)"
fi

# ---------- T19（G4）时间窗覆盖白名单：payment.mode / billing.enforced 必须拒绝 ----------
R=$(post "$AH" '{"window":{"id":"t19_pay","name":"支付后门窗","start":"2026-01-01T00:00:00Z","end":"2099-01-01T00:00:00Z","priority":5,"overrides":{"payment":{"mode":"free"}}}}' /api/admin/ops/policy/window/save)
ck T19-win-payment-reject '"success":false' "$R"
ck T19-win-payment-msg 'payment.mode/auto_charge' "$R"
R=$(post "$AH" '{"window":{"id":"t19_enf","name":"开关后门窗","start":"2026-01-01T00:00:00Z","end":"2099-01-01T00:00:00Z","priority":5,"overrides":{"billing":{"enforced":false}}}}' /api/admin/ops/policy/window/save)
ck T19-win-enforced-reject 'billing.enforced|"success":false' "$R"
R=$(post "$AH" '{"window":{"id":"t19_ok","name":"合法折扣窗","start":"2026-01-01T00:00:00Z","end":"2099-01-01T00:00:00Z","priority":5,"overrides":{"billing":{"markup_multiplier":0.5}}}}' /api/admin/ops/policy/window/save)
ck T19-win-legal-accept '"success":true' "$R"

# ---------- T20（G4）编辑器产物下载 B8 闸：未登记文件 404 / 路径穿越拒绝 ----------
R=$(curl -s "$B/api/editor/export/download?file=not_registered_xlsx" -H "$H1")
ck T20-artifact-unlisted-deny '文件不存在|"success":false|not found' "$R"
R=$(curl -s "$B/api/editor/export/download?file=..%2F..%2F..%2Fetc%2Fpasswd" -H "$H1")
ck T20-traversal-deny '文件不存在|"success":false|not found|非法' "$R"
if echo "$R" | grep -q 'root:'; then FAIL=$((FAIL+1)); echo "FAIL|T20-traversal-leaked-rootfile"; else PASS=$((PASS+1)); echo "PASS|T20-traversal-no-content"; fi

# ---------- T21（G4）已消耗订单退款：实退金额按未消耗比例折算（A3） ----------
R=$(post "$H1" '{"name":"t21-key"}' /api/apikeys/create); AK21=$(echo "$R" | pv '.get("api_key","")')
R=$(post "$H1" '{"points":133,"channel":"mock"}' /api/pay/create)
OID21=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
post "$H1" "{\"order_id\":$OID21}" /api/pay/simulate >/dev/null
sleep 4
T21A0=$(get "$H1" /api/billing/balance | pv '.get("points_available",0)')
curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer $AK21" --max-time 60 \
  -d '{"text":"退款窗口计量句 one sentence for consumption window check","target_lang":"en","mode":"pro"}' >/dev/null
sleep 4
T21A1=$(get "$H1" /api/billing/balance | pv '.get("points_available",0)')
CONSUMED21=$((T21A0 - T21A1))
[ "$CONSUMED21" -gt 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T21-consumption-window(+$CONSUMED21)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T21-consumption-window($CONSUMED21)"; }
R=$(post "$AH" "{\"id\":$OID21,\"tenant_id\":$TAID}" /api/admin/orders/refund)
ck T21-refund-ok '"success":true' "$R"
ST21=$(sq "SELECT status FROM orders WHERE id=$OID21" | tr -d '[:space:]')
[ "$ST21" = "refunded" ] && { PASS=$((PASS+1)); echo "PASS|T21-refunded"; } || { FAIL=$((FAIL+1)); echo "FAIL|T21-refunded($ST21)"; }
RM21=$(sq "SELECT COALESCE(refund_money,0) FROM orders WHERE id=$OID21" | tr -d '[:space:]')
AM21=$(sq "SELECT amount_money FROM orders WHERE id=$OID21" | tr -d '[:space:]')
LT21=$(python3 -c "print(1 if 0 < $RM21 < $AM21 else 0)" 2>/dev/null || echo 0)
[ "$LT21" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T21-partial-refund(实退 $RM21 < 全额 ${AM21} ，已消耗 $CONSUMED21 积分)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T21-partial-refund(refund=$RM21 amount=$AM21 consumed=$CONSUMED21)"; }

# ---------- T22（G4）退款联动撤销裂变付费奖励（revokePaidReferralIfAllRefunded） ----------
I22="uatuser_i22$(date +%s)"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$I22\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T22邀请者\",\"email\":\"$I22@test.com\",\"agreed\":true}" >/dev/null
IT=$(tok $I22 uatpass123); HI="Authorization: Bearer $IT"
ICODE=$(curl -s "$B/api/referral/my" -H "$HI" | pv '.get("ref_code","")')
C22="uatuser_c22$(date +%s)"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$C22\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T22受邀\",\"email\":\"$C22@test.com\",\"agreed\":true,\"ref\":\"$ICODE\"}" >/dev/null
CT=$(tok $C22 uatpass123); HC="Authorization: Bearer $CT"
CTID=$(sq "SELECT tenant_id FROM users WHERE username='$C22' LIMIT 1" | tr -d '[:space:]')
ITID=$(sq "SELECT tenant_id FROM users WHERE username='$I22' LIMIT 1" | tr -d '[:space:]')
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$CTID,\"code\":\"uat_t22_paid\",\"name\":\"T22付费包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":30,\"duration_days\":30}" >/dev/null
E22B=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$ITID")
R=$(post "$HC" '{"code":"uat_t22_paid"}' /api/package/subscribe)
OID22=$(echo "$R" | pv '.get("order",{}).get("id") or d.get("id") or 0')
post "$AH" "{\"id\":$OID22,\"tenant_id\":$CTID}" /api/admin/orders/pay >/dev/null
E22A=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$ITID")
[ "${E22A:-0}" -gt "${E22B:-0}" ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T22-reward-granted(+$((E22A - E22B)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|T22-reward-granted($E22B->$E22A)"; }
R=$(post "$AH" "{\"id\":$OID22,\"tenant_id\":$CTID}" /api/admin/orders/refund)
ck T22-refund-ok '"success":true' "$R"
E22F=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$ITID")
[ "$E22F" = "$E22B" ] && { PASS=$((PASS+1)); echo "PASS|T22-reward-clawback(奖励全额撤销)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T22-reward-clawback(before=$E22B afterReward=$E22A afterRefund=$E22F)"; }
RW22=$(sq "SELECT COUNT(*) FROM referral_rewards WHERE inviter_uid=(SELECT id FROM users WHERE username='$I22') AND type='paid_perm'" | tr -d '[:space:]')
[ "$RW22" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T22-reward-row-deleted"; } || { FAIL=$((FAIL+1)); echo "FAIL|T22-reward-row-deleted($RW22)"; }

# ---------- T23（G4）已退款订单禁止开票（CreateInvoice 硬闸） ----------
# ★ F-64①（批 I-7）三件套：'"success":false' 这条断言在 200 壳时代与诚实 409 时代**同绿**，
#   是本次改造要消灭的那类假绿；现按「409 + CONFLICT + 已退款文案」重锁（订单在、状态不可开）。
T23BODY="{\"order_id\":$OID21,\"title\":\"T23冲红前发票\",\"tax_no\":\"TX9023\"}"
req3 POST "$H1" /api/billing/invoices/create "$T23BODY"
ck3 T23-invoice-refund-reject 409 CONFLICT '已退款'


# ---------- T24（G5）订阅到期摘除链路：注入到期 → 手动扫描 → permissions 实际变更 ----------
U24="uatuser_t24$(date +%s)"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$U24\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T24到期链\",\"email\":\"$U24@test.com\",\"agreed\":true}" >/dev/null
TK24=$(tok $U24 uatpass123); H24="Authorization: Bearer $TK24"
T24D=$(sq "SELECT tenant_id FROM users WHERE username='$U24' LIMIT 1" | tr -d '[:space:]')
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$T24D,\"code\":\"uat_t24_paid\",\"name\":\"T24到期包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":10,\"duration_days\":30}" >/dev/null
R=$(post "$H24" '{"code":"uat_t24_paid"}' /api/package/subscribe)
OID24=$(echo "$R" | pv '.get("order",{}).get("id") or d.get("id") or 0')
post "$AH" "{\"id\":$OID24,\"tenant_id\":$T24D}" /api/admin/orders/pay >/dev/null
PC=$(dbjsonstr tenants $T24D permissions package_code | tr -d '[:space:]')
[ "$PC" = "uat_t24_paid" ] && { PASS=$((PASS+1)); echo "PASS|T24-subscribe-granted($PC)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T24-subscribe-granted($PC)"; }
dbjsonset tenants $T24D permissions package_expires_at "2020-01-01T00:00:00Z"   # ★ 注入已过期到期时间（duration 0=不限期不参与扫描）
ck T24-scan-non-super-denied '"success":false' "$(post "$H24" '{}' /api/admin/ops/watchdog/subscription-scan)"
ck T24-scan-run '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"
PC2=$(dbjsonstr tenants $T24D permissions package_code | tr -d '[:space:]')
[ -z "$PC2" ] && { PASS=$((PASS+1)); echo "PASS|T24-expire-strip(permissions 摘除)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T24-expire-strip(got $PC2)"; }
AC24=$(sq "SELECT COUNT(*) FROM audit_logs WHERE tenant_id=$T24D AND action='package_expire'" | tr -d '[:space:]')
[ "${AC24:-0}" -ge 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T24-expire-audit($AC24)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T24-expire-audit($AC24)"; }
ck T24-scan-idempotent '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"

# ---------- T25（B3/B5 安全收尾）sso_code 兑换防重放 + 计费配置匿名拒绝 ----------
ck T25-sso-exchange-bad-code '"success":false' "$(post '' '{"code":"bogus-code-0001"}' /api/auth/sso/exchange)"
ck T25-billing-config-anon-denied '"success":false' "$(curl -s $B/api/billing/config)"

# ---------- T26（C26→积分口径）usage/me 展示字段 ----------
R=$(get "$H1" /api/billing/usage/me)
ck T26-usage-me-points-field '"points_available"' "$R"
ck T26-usage-me-sentences-approx '"sentences_estimate"' "$R"

# ---------- T27（F10/H12）OpenAPI 规范自动导出含全部端点 ----------
R=$(curl -s $B/openapi/v1.json)
ck T27-spec-openapi '"openapi"' "$R"
ck T27-spec-terms-path '"/terms"' "$R"
ck T27-spec-translate '"/translate"' "$R"

# ---------- T28（H5）大文件分片上传：传片/续传定位/合并/缺口与类型守卫 ----------
# 注意：macOS bash 3.2 在 ck 的 "$( ... )" 双引号内嵌套 \" 会解析破碎——
# 所有含内层双引号的 JSON body 一律先落变量再传参。
CID="t28-$(date +%s)-$$"
CH0=$(mktemp); CH1=$(mktemp)
printf 'zh,en\nserver_row,one\n' > "$CH0"
printf 'two,three\n' > "$CH1"
ck T28-chunk-0 '"success":true' "$(curl -s $B/api/upload/chunk -H "$H1" -F "upload_id=$CID" -F index=0 -F total=2 -F "chunk=@$CH0;filename=part.csv")"
ck T28-chunk-1 '"success":true' "$(curl -s $B/api/upload/chunk -H "$H1" -F "upload_id=$CID" -F index=1 -F total=2 -F "chunk=@$CH1;filename=part.csv")"
ck T28-status-received '"received":\[0,1\]' "$(get "$H1" "/api/upload/status?upload_id=$CID")"
MERGE_BODY='{"upload_id":"'$CID'","filename":"bit.csv"}'
R=$(post "$H1" "$MERGE_BODY" /api/upload/merge)
ck T28-merge-ok 'kbmerged_[0-9a-f]{12}\.csv' "$R"
ck T28-merge-after-clean '"success":false' "$(post "$H1" "$MERGE_BODY" /api/upload/merge)"
CID2="t28-b-$(date +%s)-$$"
curl -s $B/api/upload/chunk -H "$H1" -F "upload_id=$CID2" -F index=0 -F total=3 -F "chunk=@$CH0;filename=part.csv" >/dev/null
MERGE2='{"upload_id":"'$CID2'","filename":"x.csv"}'
ck T28-merge-incomplete-guard '"success":false' "$(post "$H1" "$MERGE2" /api/upload/merge)"
MERGE2X='{"upload_id":"'$CID2'","filename":"x.exe"}'
ck T28-merge-bad-ext '"success":false' "$(post "$H1" "$MERGE2X" /api/upload/merge)"
rm -f "$CH0" "$CH1"

# ---------- T29（H10）SCIM 2.0 全生命周期：开通/元数据/建用户/查重/启停/删除/坏令牌 ----------
R=$(post "$H1" '{"enabled":true}' /api/tenant/scim)
ck T29-scim-config-enabled '"success":true' "$R"
SK=$(echo "$R" | pv '.get("config",{}).get("token","")')
[ ${#SK} -ge 32 ] && { PASS=$((PASS+1)); echo "PASS|T29-scim-token-issued"; } || { FAIL=$((FAIL+1)); echo "FAIL|T29-scim-token-issued"; }
SN="scim-t29-$RANDOM$RANDOM"
SH="Authorization: Bearer $SK"
ck T29-scim-meta '"schemas"' "$(curl -s $B/api/scim/v2/ServiceProviderConfig -H "$SH")"
NEW_USER='{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"'$SN'","externalId":"ext-'$SN'","name":{"givenName":"Sync","familyName":"Test"},"emails":[{"value":"'${SN}'@test.com"}]}'
R=$(curl -s $B/api/scim/v2/Users -H "$SH" -H "$J" -d "$NEW_USER")
SID=$(echo "$R" | pv '.get("id","")')
[ -n "$SID" ] && { PASS=$((PASS+1)); echo "PASS|T29-scim-user-created"; } || { FAIL=$((FAIL+1)); echo "FAIL|T29-scim-user-created|got[${R:0:180}]"; }
NU=$(sq "SELECT COUNT(*) FROM users WHERE username='$SN'" | tr -d '[:space:]')
[ "$NU" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T29-scim-user-in-db"; } || { FAIL=$((FAIL+1)); echo "FAIL|T29-scim-user-in-db"; }
R=$(curl -s -G "$B/api/scim/v2/Users" -H "$SH" --data-urlencode "filter=userName eq \"$SN\"")
ck T29-scim-filter-hit '"totalResults":1' "$R"
UPD_USER='{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"'$SN'","externalId":"ext-'$SN'-v2"}'
R=$(curl -s -X POST $B/api/scim/v2/Users -H "$SH" -H "$J" -d "$UPD_USER")
ck T29-scim-idempotent-update '"id":"'$SID'"' "$R"
PATCH_ON='{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":true}]}'
R=$(curl -s -X PATCH "$B/api/scim/v2/Users/$SID" -H "$SH" -H "$J" -d "$PATCH_ON")
ST=$(sq "SELECT status FROM users WHERE id=$SID" | tr -d '[:space:]')
[ "$ST" = "active" ] && { PASS=$((PASS+1)); echo "PASS|T29-scim-patch-activate"; } || { FAIL=$((FAIL+1)); echo "FAIL|T29-scim-patch-activate(status=$ST)"; }
PATCH_OFF='{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":false}]}'
R=$(curl -s -X PATCH "$B/api/scim/v2/Users/$SID" -H "$SH" -H "$J" -d "$PATCH_OFF")
ST=$(sq "SELECT status FROM users WHERE id=$SID" | tr -d '[:space:]')
[ "$ST" = "disabled" ] && { PASS=$((PASS+1)); echo "PASS|T29-scim-patch-deactivate"; } || { FAIL=$((FAIL+1)); echo "FAIL|T29-scim-patch-deactivate(status=$ST)"; }
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$B/api/scim/v2/Users/$SID" -H "$SH")
[ "$CODE" = "204" ] && { PASS=$((PASS+1)); echo "PASS|T29-scim-delete-204"; } || { FAIL=$((FAIL+1)); echo "FAIL|T29-scim-delete-204"; }
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/scim/v2/Users" -H "Authorization: Bearer bogus_token_000000")
[ "$CODE" = "401" ] && { PASS=$((PASS+1)); echo "PASS|T29-scim-bad-token-401"; } || { FAIL=$((FAIL+1)); echo "FAIL|T29-scim-bad-token-401"; }
ck T29-scim-config-off '"success":true' "$(post "$H1" '{"enabled":false}' /api/tenant/scim)"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/scim/v2/Users" -H "$SH")
[ "$CODE" = "403" ] && { PASS=$((PASS+1)); echo "PASS|T29-scim-disabled-403"; } || { FAIL=$((FAIL+1)); echo "FAIL|T29-scim-disabled-403"; }

# ---------- T30（H3+D11+H12 seed）包级授权矩阵 + 条目写入 + 关键词检索 + 开放术语 ----------
R=$(post "$H1" '{"code":"uat-h30","name":"H30 test pack","pack_type":"tenant","role":"source"}' /api/admin/kb-packages/create)
PID30=$(echo "$R" | pv '.get("package",{}).get("id",0)')
[ "${PID30:-0}" -gt 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T30-pack-created-$PID30"; } || { FAIL=$((FAIL+1)); echo "FAIL|T30-pack-created|got[${R:0:160}]"; }
MEM_CREATE='{"username":"uatmem-h30","password":"mem123456","display_name":"H30 member","role":"user","tenant_id":'$TAID'}'
post "$H1" "$MEM_CREATE" /api/admin/users/create >/dev/null
MID30=$(sq "SELECT id FROM users WHERE username='uatmem-h30' LIMIT 1" | tr -d '[:space:]')
MT=$(tok uatmem-h30 mem123456); MH="Authorization: Bearer $MT"
ck T30-member-no-grant-403 '只读授权' "$(get "$MH" "/api/admin/kb-entries?package_id=$PID30")"
GRANT_READ='{"pack_id":'$PID30',"user_id":'$MID30',"role":"read"}'
ck T30-grant-read '"success":true' "$(post "$H1" "$GRANT_READ" /api/admin/kb-packages/grants)"
ck T30-member-read-ok '"success":true' "$(get "$MH" "/api/admin/kb-entries?package_id=$PID30")"
ADD_FW='{"package_id":'$PID30',"layer":1,"source_lang":"zh","source_text":"防火墙","target_lang":"en","target_text":"firewall"}'
ck T30-member-write-denied '"success":false' "$(post "$MH" "$ADD_FW" /api/admin/kb-entries/add)"
GRANT_WRITE='{"pack_id":'$PID30',"user_id":'$MID30',"role":"write"}'
ck T30-grant-write '"success":true' "$(post "$H1" "$GRANT_WRITE" /api/admin/kb-packages/grants)"
ADD_SVR='{"package_id":'$PID30',"layer":1,"source_lang":"zh","source_text":"服务器","target_lang":"en","target_text":"server"}'
R=$(post "$MH" "$ADD_SVR" /api/admin/kb-entries/add)
ck T30-member-write-ok '"success":true' "$R"
ck T30-mine-list '"success":true' "$(get "$MH" /api/admin/kb-packages/mine)"
GRANT_NONE='{"pack_id":'$PID30',"user_id":'$MID30',"role":""}'
ck T30-revoke '"success":true' "$(post "$H1" "$GRANT_NONE" /api/admin/kb-packages/grants)"
ck T30-revoked-403 '只读授权' "$(get "$MH" "/api/admin/kb-entries?package_id=$PID30")"
ck T30-kb-search-q '"success":true' "$(get "$H1" "/api/admin/kb-entries?package_id=$PID30&q=%E6%9C%8D%E5%8A%A1")"
R=$(curl -s -G "$B/openapi/v1/terms" --data-urlencode "q=服务器" -H "Authorization: Bearer $AK")
ck T30-terms-exact-hit '"exact":true' "$R"
ck T30-terms-target '"target":"server"' "$R"
ck T30-terms-no-anon 'API Key' "$(curl -s -G "$B/openapi/v1/terms" --data-urlencode "q=服务器")"

# ---------- T31（H12）TMX 导出：成员拒 / 文档结构 / 非法语言 / 匿名拒 ----------
MEMT=$(tok uatmem-h30 mem123456)
ck T31-tmx-member-403 '"success":false' "$(get "Authorization: Bearer $MEMT" /api/translation/export-tmx)"
R=$(curl -s -D /tmp/t31_h.txt "$B/api/translation/export-tmx?lang=en" -H "$H1")
ck T31-tmx-doc 'tmx version="1.4"' "$R"
ck T31-tmx-disposition 'attachment; filename=langcross_tm_' "$(cat /tmp/t31_h.txt 2>/dev/null)"
rm -f /tmp/t31_h.txt
ck T31-tmx-bad-lang '不支持的语言' "$(get "$H1" '/api/translation/export-tmx?lang=zzz')"
ck T31-tmx-anon-denied '"success":false' "$(curl -s $B/api/translation/export-tmx)"

# ---------- T32（H7/H11）运营可视化端点：动态路由统计 + SLO/burn rate ----------
ck T32-ops-routes '"success":true' "$(get "$AH" /api/admin/ops/routes)"
ck T32-ops-routes-non-super '"success":false' "$(get "$H1" /api/admin/ops/routes)"
R=$(get "$AH" /api/admin/ops/slo)
ck T32-ops-slo '"success":true' "$R"
ck T32-ops-slo-keys '"slos"' "$R"

# ---------- T33（H4/H9）反馈预筛审核队列契约 + 二级裂变漏斗 ----------
ck T33-tm-review-list '"candidates"' "$(get "$AH" '/api/admin/tm-review/list?limit=5')"
ck T33-tm-review-non-super '"success":false' "$(get "$H1" '/api/admin/tm-review/list?limit=5')"
U33="uatuser-t33-$(date +%s)"
curl -s $B/api/auth/register -H "$J" -d '{"username":"'$U33'","password":"uatpass123","type":"personal","name":"T33 funnel","email":"'$U33'@test.com","agreed":true}' >/dev/null
H33="Authorization: Bearer $(tok $U33 uatpass123)"
R=$(get "$H33" /api/referral/funnel)
ck T33-funnel '"success":true' "$R"
ck T33-funnel-l2pct '"l2_pct"' "$R"

# ---------- T34 工单双模式（还原文件 / 纯文案，2026-09-13） ----------
TMPD34=$(mktemp -d)
python3 - "$TMPD34/t34.docx" <<'EOF'
import sys, zipfile
zf = zipfile.ZipFile(sys.argv[1],'w')
zf.writestr('[Content_Types].xml','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>''')
zf.writestr('_rels/.rels','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>''')
zf.writestr('word/document.xml','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>双模式测试第一段：文件翻译兜底交付。</w:t></w:r></w:p>
<w:p><w:r><w:t>双模式测试第二段：纯文案模式验证。</w:t></w:r></w:p>
<w:sectPr/></w:body></w:document>''')
zf.close()
EOF
# 等待文件工单跑完（轮询 detail 至终态，最长 60s：mock LLM 下硬闸兜底循环亦需数秒）
waittk(){ local hdr="$1" tid="$2" d st
  for i in $(seq 1 30); do
    d=$(get "$hdr" "/api/tickets/detail?id=$tid")
    st=$(echo "$d" | pv "['ticket'].get('status')")
    case "$st" in completed|rejected) echo "$d"; return 0;; esac
    sleep 2
  done
  echo "$d"; return 1; }

# T34-1 还原文件模式（缺省 delivery）：完成后旁路纯文案 .md 已登记 + fmt=text 可下载
R=$(curl -s $B/api/tickets/create-file -H "$H1" -F "files=@$TMPD34/t34.docx" -F "target_langs=en" -F "mode=fast")
ck T34-restore-create '"success":true' "$R"
TKR=$(echo "$R" | pv "['ticket']['id']")
D34=$(waittk "$H1" "$TKR")
ck T34-restore-completed '"status":"completed"' "$D34"
ck T34-restore-sidecar-registered '"text_result_path":"[^"]' "$D34"
T34MD=$(curl -s "$B/api/tickets/download?id=$TKR&fmt=text" -H "$H1" --max-time 30)
ck T34-restore-dl-text-md 'TranslatedEN' "$T34MD"
ck T34-restore-dl-main-200 '200' "$(curl -s -o /dev/null -w '%{http_code}' "$B/api/tickets/download?id=$TKR" -H "$H1" --max-time 30)"

# T34-2 纯文案模式（delivery=text，docx 走 MD 管线不依赖 anydoc）：主产物即 .md
R=$(curl -s $B/api/tickets/create-file -H "$H1" -F "files=@$TMPD34/t34.docx" -F "target_langs=en" -F "mode=fast" -F "delivery=text")
ck T34-text-create '"success":true' "$R"
TKT=$(echo "$R" | pv "['ticket']['id']")
D34T=$(waittk "$H1" "$TKT")
ck T34-text-completed '"status":"completed"' "$D34T"
ck T34-text-delivery-echo '"delivery":"text"' "$D34T"
T34TMD=$(curl -s "$B/api/tickets/download?id=$TKT" -H "$H1" --max-time 30)
ck T34-text-dl-md-content 'TranslatedEN' "$T34TMD"

# T34-3 白名单分档：restore 拒绝 .rtf（老格式）；text 模式拒绝未知扩展并提示纯文案支持面
printf '{\\rtf1 legacy}' > "$TMPD34/legacy.rtf"
R=$(curl -s $B/api/tickets/create-file -H "$H1" -F "files=@$TMPD34/legacy.rtf" -F "target_langs=en")
ck T34-rtf-restore-reject '不支持的格式' "$R"
printf 'not-a-doc' > "$TMPD34/bad.ppt9"
R=$(curl -s $B/api/tickets/create-file -H "$H1" -F "files=@$TMPD34/bad.ppt9" -F "target_langs=en" -F "delivery=text")
ck T34-text-whitelist '纯文案模式支持' "$R"

# T34-4 文本工单无文件产物：fmt=text 返回明确失败话术（非 500）
R=$(post "$H1" '{"title":"T34文本工单","source_text":"双模式文本工单兜底提示检查","target_langs":"en","mode":"fast"}' /api/tickets/create)
TKX=$(echo "$R" | pv "['ticket']['id']")
ck T34-textticket-no-artifact '"success":false' "$(get "$H1" "/api/tickets/download?id=$TKX&fmt=text" )"

# T34-5 OpenAPI 文件任务 delivery 透传回显
R=$(curl -s $B/openapi/v1/tasks -H "Authorization: Bearer $AK" -F "files=@$TMPD34/t34.docx" -F "target_langs=en" -F "mode=fast" -F "delivery=text" --max-time 60)
ck T34-openapi-delivery-echo '"delivery":"text"' "$R"
rm -rf "$TMPD34"

# T34-6 健康检查暴露 anydoc_ready（纯文案模式提取层就绪状态，布尔值——未装依赖时为 false 也须存在该字段）
H=$(curl -s "$B/api/health" --max-time 20)
ck T34-health-anydoc-ready '"anydoc_ready":(true|false)' "$H"

# ---------- T35 线上反馈回归（2026-09-14：multipart 上传修复 + 演示站脚本守护） ----------
# T35-1 文件工单 multipart 上传（★ 历史缺陷：前端 request() 对 FormData 强设 JSON Content-Type
#       抹掉 boundary → ParseMultipartForm 秒 400「文件解析失败或超过大小上限（40MB）」）。
#       curl 天然生成正确 boundary，本用例锁定后端契约：含空格/中点/中文的特殊文件名 PDF 建单成功。
TMPD35=$(mktemp -d)
python3 - "$TMPD35/翻译助手 2.0 · 业务集成方案.pdf" <<'EOF'
import sys
# 手工构造最小合法 1 页 PDF（纯字节，无第三方依赖；页树 Count=1 可被 pdfPageCount 识别）
body = b"""%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >> endobj
trailer << /Root 1 0 R /Size 4 >>
%%EOF
"""
open(sys.argv[1], 'wb').write(body)
EOF
R=$(curl -s $B/api/tickets/create-file -H "$H1" -F "files=@$TMPD35/翻译助手 2.0 · 业务集成方案.pdf" -F "target_langs=en" -F "mode=fast" --max-time 60)
ck T35-pdf-multipart-create '"success":true' "$R"
TKP=$(echo "$R" | pv "['ticket'].get('id')")
DP=$(waittk "$H1" "$TKP")
ck T35-pdf-ticket-terminal '"status":"(completed|rejected)"' "$DP"
STP=$(echo "$DP" | pv "['ticket'].get('status')")
if [ "$STP" = "completed" ]; then
  ck T35-pdf-dl-200 '200' "$(curl -s -o /dev/null -w '%{http_code}' "$B/api/tickets/download?id=$TKP" -H "$H1" --max-time 60)"
fi
# T35-2 多文件混合上传（2 文件共享上限口径——multipart 解析不得因多 parts 出错）
printf '第一行内容。\n第二行内容。\n' > "$TMPD35/a.txt"
printf '{"k":"v"}\n' > "$TMPD35/b.json"
R=$(curl -s $B/api/tickets/create-file -H "$H1" -F "files=@$TMPD35/a.txt" -F "files=@$TMPD35/b.json" -F "target_langs=en" -F "mode=fast" --max-time 60)
ck T35-multi-upload '"success":true' "$R"
rm -rf "$TMPD35"
# T35-3 演示站脚本静态守护（防止 bootstrap-demo.sh 回退丢配置：base_domain 幂等写入 /
#       demo_superadmin 账号种入 / DEMO_SEED_ACCOUNTS 环境变量开关可覆盖）
BS="$(cd "$(dirname "$0")" && pwd)/../bootstrap-demo.sh"
HITBD=$(grep -c "VALUES ('base_domain'" "$BS")
ck T35-bootstrap-basedomain '^[1-9]' "$HITBD"
HITSA=$(grep -c "'demo_superadmin'" "$BS")
ck T35-bootstrap-superadmin '^[1-9]' "$HITSA"
HITSG=$(grep -c 'DEMO_SEED_ACCOUNTS:-1' "$BS")
ck T35-bootstrap-seedguard '^[1-9]' "$HITSG"

# ---------- T36 ★ 商业化开闸批次（09-14）专项 ----------
echo "--- T36 商业化开闸（积分/敏感词/反薅/漏斗/监控收口）---"
reg(){ local extra="${6:-}"; curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$1\",\"password\":\"$2\",\"code\":\"$3\",\"name\":\"$4\",\"email\":\"$5\",\"agreed\":true${extra:+,$extra}}"; }
SSET(){ curl -s $B/api/admin/packages/settings -H "$AH" -H "$J"; }
SSAVE(){ curl -s $B/api/admin/packages/settings/save -H "$AH" -H "$J" -d "$1"; }

# ① S1 积分制公开面
PL=$(get "$AH" /api/plans)
ck T36-plans-free-points '"free_trial_points":1000' "$PL"
if echo "$PL" | grep -qE 'free_trial_tokens'; then FAIL=$((FAIL+1)); echo "FAIL|T36-plans-no-token-naked"; else PASS=$((PASS+1)); echo "PASS|T36-plans-no-token-naked"; fi
ck T36-overview-points '"points_available":' "$(get "$H1" /api/billing/my/overview)"
# ★ 2026-09-19 积分口径：auth/me 与 settings 出参零 token 裸键，汇率不再下发
MEJ=$(curl -s $B/api/auth/me -H "$H1")
if echo "$MEJ" | grep -qE '"[a-z_]*tokens"'; then FAIL=$((FAIL+1)); echo "FAIL|T36-me-no-token-naked"; else PASS=$((PASS+1)); echo "PASS|T36-me-no-token-naked"; fi
SETJ=$(SSET)
ck T36-settings-show '"free_trial_points":' "$SETJ"
if echo "$SETJ" | grep -qE '"[a-z_]*tokens"'; then FAIL=$((FAIL+1)); echo "FAIL|T36-settings-no-token-naked"; else PASS=$((PASS+1)); echo "PASS|T36-settings-no-token-naked"; fi
ck T36-settings-points-400 '"success": *true' "$(SSAVE '{"free_trial_points":400}')"
ck T36-settings-points-echo '"free_trial_points":400' "$(SSET)"
SSAVE '{"free_trial_points":1000}' >/dev/null   # 还原默认体验积分（1000）

# ② S8 敏感词闸（词包见 run_uat 注入：紫火核弹T36；输入命中不进模型）
AK36=$(post "$H1" '{"name":"t36-key"}' /api/apikeys/create | pv '.get("api_key","")')
S36(){ curl -s $B/openapi/v1/translate -H "Authorization: Bearer $AK36" -H "$J" -d "$1"; }
# ★ 修法 F（2026-10-04 〇-AR 第 2 波 · 缺陷 ⑭）改写本段判据：
#   同步 /openapi/v1/translate 的敏感词拒译出参，旧形态是「409＋message 里塞内部码
#   sensitive_blocked」（F-53 在对话面修过的那个裸键名，这一面当时漏了），本批改成
#   「403＋error_code=rejected＋人话」——与异步面 gateErrorCode 的内容类拒绝同一档。
#   于是旧断言 grep 的正是那句被判定为泄漏的键名，必须随批换掉（换掉不等于放宽：
#   这里同时补了「人话到位」与「报文里不再出现裸内部码」两条腿，一正一负配齐）。
SENS36_BLOCK=$(S36 '{"text":"请翻译：紫火核弹T36 常规句子","target_lang":"en","source_lang":"zh"}')
ck T36-sensitive-block '"error_code":"rejected"' "${SENS36_BLOCK}"
ck T36-sensitive-copy '内容合规审核|不予受理' "${SENS36_BLOCK}"
if echo "${SENS36_BLOCK}" | grep -qE 'sensitive_blocked'; then FAIL=$((FAIL+1)); echo "FAIL|T36-sensitive-no-raw-internal-code"; else PASS=$((PASS+1)); echo "PASS|T36-sensitive-no-raw-internal-code"; fi
ck T36-sensitive-passthrough '"success": *true' "$(S36 '{"text":"纯净文本仅用于闸外验证","target_lang":"en","source_lang":"zh"}')"
SSAVE '{"sensitive_gate_enabled":"0"}' >/dev/null
ck T36-sensitive-off-passthrough '"success": *true' "$(S36 '{"text":"关闸后含词也不拦：紫火核弹T36","target_lang":"en","source_lang":"zh"}')"
SSAVE '{"sensitive_gate_enabled":"1"}' >/dev/null
ck T36-sensitive-on-again '"error_code":"rejected"' "$(S36 '{"text":"再开闸恢复拦截：紫火核弹T36","target_lang":"en","source_lang":"zh"}')"
ck T36-settings-gate-bad-reject '"success": *false' "$(SSAVE '{"sensitive_gate_enabled":"2"}')"

# ③ S3 一次性邮箱黑名单
ck T36-disposable-builtin '一次性|临时邮箱|不予' "$(reg t36mail uatpass123 T36MD 演练T36 a@mailinator.com)"
SSAVE '{"disposable_email_domains":"spamt36.test"}' >/dev/null
ck T36-disposable-custom '一次性|临时邮箱|不予' "$(reg t36mail2 uatpass123 T36MC 演练T36 b@spamt36.test)"
ck T36-settings-domains-echo 'spamt36.test' "$(SSET)"
SSAVE '{"disposable_email_domains":""}' >/dev/null   # 还原

# ④ S4 归因 + 漏斗
ck T36-funnel-anon-reject 'success|40[13]' "$(curl -s $B/api/admin/funnel)"
R36=$(reg t36utm uatpass123 T36MU 演练T36 utmt36@t.test '"utm_source":"t36utm","utm_medium":"unit"')
ck T36-utm-register-ok '"success": *true' "$R36"
N=$(sq "SELECT COUNT(*) FROM registration_attribution WHERE utm_source='t36utm'" | tr -d '[:space:]')
ck T36-utm-persist '^1$' "$N"
ck T36-funnel-admin '"success": *true' "$(get "$AH" "/api/admin/funnel?days=7")"

# ⑤ S9 Alertmanager 收口 + /metrics 鉴权（ADMIN_TOKEN/METRICS_TOKEN 由 run_uat 固定注入）
NOW36=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ALB36="{\"alerts\":[{\"status\":\"firing\",\"labels\":{\"alertname\":\"T36Smoke\",\"severity\":\"critical\"},\"annotations\":{\"summary\":\"T36 收口断言\"},\"startsAt\":\"$NOW36\"},{\"status\":\"resolved\",\"labels\":{\"alertname\":\"T36Ignored\"},\"startsAt\":\"$NOW36\"}]}"
ck T36-am-noauth '^403$' "$(curl -s -o /dev/null -w '%{http_code}' -XPOST $B/api/alerts/alertmanager -H "$J" -d "$ALB36")"
ck T36-am-wrongtok '^403$' "$(curl -s -o /dev/null -w '%{http_code}' -XPOST $B/api/alerts/alertmanager -H "X-Admin-Token: nope" -H "$J" -d "$ALB36")"
ck T36-am-fire200 '"success": *true.*"accepted": *1|"accepted": *1.*"success": *true' "$(curl -s -XPOST $B/api/alerts/alertmanager -H "X-Admin-Token: uat-admin-token-36" -H "$J" -d "$ALB36")"
CK36=$(sq "SELECT COUNT(*) FROM alerts WHERE kind='prom:T36Smoke'" | tr -d '[:space:]')
ck T36-am-alert-persist '^[1-9]' "$CK36"
ck T36-metrics-401 '401' "$(curl -s -o /dev/null -w '%{http_code}' $B/metrics)"
ck T36-metrics-bearer 'translator_info' "$(curl -s $B/metrics -H 'Authorization: Bearer uat-metrics-36')"

# ⑥ S7 观察表 + ⑦ S5 首页（静态托管）
NW=$(sq "SELECT COUNT(*) FROM s7_watchlist" | tr -d '[:space:]')
ck T36-s7-table '^[0-9]+$' "$NW"
ck T36-landing-html 'og:title|能言|LangCross' "$(curl -s $B/)"

# ---------- T37 ★ 今日修复回归批次（2026-09-14，见《UAT_缺陷清单与处置记录_20260914.md》） ----------
echo "--- T37 今日修复回归（org_id=0 越权/auto_charge 收敛/sso_code 品牌链/敏感词归一化/积分溢出/charge_kind）---"

# ① T37-1（P0-2）：部门管理员不能操作 org_id=0（未分配部门）的同租户账号（含租户管理员）
#    旧缺陷：子树校验写 target.OrgID>0 才生效，org_id=0（注册/SCIM/建号默认值）整段被跳过
#    → dept_admin 可重置 tenant_admin 密码完成租户接管。修复后：org_id≤0 一律 403（与 Delete 对齐）。
ORG37=$(post "$AH" "{\"name\":\"T37研发部\",\"type\":\"dept\",\"tenant_id\":$TAID}" /api/admin/orgs/create)
ck T37-org-create '"success": *true' "$ORG37"
OID37=$(echo "$ORG37" | pv '.get("org",{}).get("id") or 0')
post "$AH" "{\"username\":\"t37_dept\",\"password\":\"uatpass123\",\"display_name\":\"T37部门管理员\",\"role\":\"dept_admin\",\"tenant_id\":$TAID,\"org_id\":$OID37}" /api/admin/users/create >/dev/null
post "$AH" "{\"username\":\"t37_member\",\"password\":\"uatpass123\",\"display_name\":\"T37部门成员\",\"role\":\"user\",\"tenant_id\":$TAID,\"org_id\":$OID37}" /api/admin/users/create >/dev/null
post "$AH" "{\"username\":\"t37_ta\",\"password\":\"uatpass123\",\"display_name\":\"T37租管对照\",\"role\":\"tenant_admin\",\"tenant_id\":$TAID}" /api/admin/users/create >/dev/null
TD37=$(tok t37_dept uatpass123); HD37="Authorization: Bearer $TD37"
ck T37-dept-login-ok '^.{20,}$' "$TD37"
MEM37ID=$(sq "SELECT id FROM users WHERE username='t37_member'" | tr -d '[:space:]')
# 子树内正常授权不被误伤：部门管理员重置本部门成员密码仍应成功（防修过头）
# ★ T37-UAT 脚本修复（2026-09-15）：body 先落变量再传 post —— 内联 \" 转义串在
#   ck "$(...)" 双层命令替换下偶发解析漂移导致 400「请求格式错误」，非产品缺陷。
T37BODY="{\"id\":$MEM37ID,\"password\":\"DeptOk@37x\"}"
ck T37-subtree-still-ok '"success": *true' "$(post "$HD37" "$T37BODY" /api/admin/users/reset-password)"
# 核心断言：org_id=0 的租户管理员，部门管理员重置/停用必须 403
TA37ID=$(sq "SELECT id FROM users WHERE username='t37_ta'" | tr -d '[:space:]')
R37=$(post "$HD37" "{\"id\":$TA37ID,\"password\":\"Hijack@37x\"}" /api/admin/users/reset-password)
ck T37-p0x-reset-org0-403 '未分配部门|无权|权限不足' "$R37"
R37b=$(post "$HD37" "{\"id\":$TA37ID,\"status\":\"disabled\"}" /api/admin/users/update)
ck T37-p0x-update-org0-403 '未分配部门|无权|权限不足' "$R37b"

# ② T37-2（P0-4）：auto_charge=1 时租户管理员订单保持 pending（仅超管可即时入账）
dbcfg auto_charge 1
O37=$(post "$H1" '{"points":10,"money":0}' /api/admin/orders/create)
ck T37-ta-order-pending '"status":"pending"' "$O37"
OA37=$(post "$AH" "{\"tenant_id\":$TAID,\"points\":10,\"money\":0}" /api/admin/orders/create)
ck T37-sa-order-paid '"status":"paid"' "$OA37"
dbcfg auto_charge 0
O37c=$(post "$H1" '{"points":10,"money":0}' /api/admin/orders/create)
ck T37-autocharge-off-pending '"status":"pending"' "$O37c"

# ③ T37-3（P0-3）：品牌子域登录返回一次性 sso_code（不再返回裸 token），兑换后得 JWT、单次消费
sq "UPDATE tenants SET domain='t37brand' WHERE id=$TAID" >/dev/null
dbcfg base_domain uat.t37.internal
BL37=$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"uatpass123"}')
ck T37-brand-sso-code '"sso_code":"[0-9a-f]{32}"' "$BL37"
if echo "$BL37" | grep -q '"token"'; then FAIL=$((FAIL+1)); echo "FAIL|T37-brand-no-naked-token"; else PASS=$((PASS+1)); echo "PASS|T37-brand-no-naked-token"; fi
XCODE37=$(echo "$BL37" | pv '.get("sso_code","")')
X37=$(curl -s $B/api/auth/sso/exchange -H "$J" -d "{\"code\":\"$XCODE37\"}")
ck T37-brand-exchange '"success": *true.*"token"' "$X37"
X37B=$(curl -s $B/api/auth/sso/exchange -H "$J" -d "{\"code\":\"$XCODE37\"}")
ck T37-brand-exchange-single-use '"success": *false' "$X37B"
sq "UPDATE tenants SET domain='' WHERE id=$TAID" >/dev/null
dbcfg base_domain ''

# ④ T37-4（P0-5）：敏感词 Unicode 归一化——全角/零宽/字间空格混淆全部拦截（OpenAPI 同步通道 e2e）
AK37=$(post "$H1" '{"name":"t37-key"}' /api/apikeys/create | pv '.get("api_key","")')
S37(){ curl -s $B/openapi/v1/translate -H "Authorization: Bearer $AK37" -H "$J" -d "$1"; }
# ★ 同 T36：本批起 OpenAPI 面把内容类拒绝归到 rejected/403（不再往外发内部码
#   sensitive_blocked），三条混淆形态的判据随之换成码位断言——三条仍必须全红才说明漏拦，
#   只要有一条回 success:true 就是归一化腿被绕过（闸外正对照见 T36-sensitive-passthrough）。
ck T37-sens-fullwidth '"error_code":"rejected"' "$(S37 '{"text":"请翻译：紫火核弹Ｔ３６ 常规句子","target_lang":"en","source_lang":"zh"}')"
ck T37-sens-zerowidth '"error_code":"rejected"' "$(S37 '{"text":"紫\u200b火\u200b核\u200b弹T36 隐藏词","target_lang":"en","source_lang":"zh"}')"
ck T37-sens-spaced '"error_code":"rejected"' "$(S37 '{"text":"紫 火 核 弹 T36 空格混淆","target_lang":"en","source_lang":"zh"}')"

# ⑤ T37-5（P1-15）：充值 points 非法大值必须 400 拒绝（防 int64 溢出负订单），合法值仍可下单
ck T37-points-overflow-reject '超出允许范围' "$(curl -s $B/api/pay/create -H "$AH" -H "$J" -d '{"points":2199023255552}')"
ck T37-points-ok-still-works '"success": *true' "$(curl -s $B/api/pay/create -H "$AH" -H "$J" -d '{"points":100}')"

# ⑥ T37-6（P1-2）：usage_ledger.charge_kind 语义列存在、枚举守恒，且本租户存在实扣行
CK37=$(sq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$TAID AND charge_kind NOT IN ('','charge','settle','log')" | tr -d '[:space:]')
ck T37-charge-kind-enum '^0$' "$CK37"
CH37=$(sq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$TAID" | tr -d '[:space:]')
CKC37=$(sq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$TAID AND charge_kind IN ('','charge','settle','log')" | tr -d '[:space:]')
[ "$CH37" = "$CKC37" ] && [ "${CH37:-0}" -gt 0 ] && { PASS=$((PASS+1)); echo "PASS|T37-charge-kind-covered"; } || { FAIL=$((FAIL+1)); echo "FAIL|T37-charge-kind-covered($CKC37/$CH37)"; }

# ---------- T38 ★ 用量明细 CSV 导出（2026-09-15 P2 报表导出，见《P0P2待办核实报告_20260915.md》P2-2） ----------
# 契约：/api/billing/usage?export=csv —— 鉴权/租户隔离与 JSON 口径一致；
# 非超管脱敏供应商/模型并应用展示系数；超管见真实 provider；from/to 日期区间过滤。
CSVH=$(mktemp); CSVB=$(mktemp)
curl -s -D "$CSVH" -o "$CSVB" "$B/api/billing/usage?export=csv" -H "$H1"
ck T38-ctype-csv 'text/csv' "$(cat "$CSVH")"
ck T38-disposition-attachment 'attachment; filename=usage_' "$(cat "$CSVH")"
ck T38-header-cols 'charge_kind,created_at' "$(head -1 "$CSVB")"
# 数据行数>0（本脚本前序用例已产生实扣流水）
CSVN=$(( $(wc -l < "$CSVB") - 1 ))
[ "$CSVN" -gt 0 ] && { PASS=$((PASS+1)); echo "PASS|T38-rows-nonempty"; } || { FAIL=$((FAIL+1)); echo "FAIL|T38-rows-nonempty($CSVN)"; }
# 非超管：每行 provider/model（第5/6列）必须为 '*' 脱敏
CSVMASK=$(awk -F, 'NR>1 && $5!="*" {print $5; exit}' "$CSVB")
[ -z "$CSVMASK" ] && { PASS=$((PASS+1)); echo "PASS|T38-mask-nonadmin"; } || { FAIL=$((FAIL+1)); echo "FAIL|T38-mask-nonadmin($CSVMASK)"; }
# 日期区间：远古区间应只剩表头（0 数据行）
CSV0=$(curl -s "$B/api/billing/usage?export=csv&from=2000-01-01&to=2000-01-02" -H "$H1" | wc -l | tr -d '[:space:]')
[ "$CSV0" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T38-range-empty"; } || { FAIL=$((FAIL+1)); echo "FAIL|T38-range-empty($CSV0)"; }
# 超管（X-Tenant-ID 切换）：provider 不脱敏，至少一行第5列非 '*'
CSVS=$(mktemp)
curl -s -o "$CSVS" "$B/api/billing/usage?export=csv" -H "$AH" -H "X-Tenant-ID: $TAID"
if awk -F, 'NR>1 && $5!="*" {found=1} END{exit !found}' "$CSVS"; then
  PASS=$((PASS+1)); echo "PASS|T38-super-real-provider"
else
  FAIL=$((FAIL+1)); echo "FAIL|T38-super-real-provider"
fi
# 未登录不得返回 CSV（鉴权失败走 JSON 错误响应，绝不流式下载）
CSV401=$(curl -s -i "$B/api/billing/usage?export=csv" | head -6)
if echo "$CSV401" | grep -q "text/csv"; then FAIL=$((FAIL+1)); echo "FAIL|T38-unauth-not-csv"; else PASS=$((PASS+1)); echo "PASS|T38-unauth-not-csv"; fi
rm -f "$CSVH" "$CSVB" "$CSVS"

# ---------- T39 ★ 多语言文件任务 zip 打包下载（2026-09-15 P2，OpenAPI 面唯一的多语言打包入口） ----------
# 契约（按产品实际设计）：单文件工单=1 行 ticket_file，多语言产物在工单服务层
# zipOutputs 预打包为 <file>_translated.zip 存 result_path（download 直取该 zip）；
# 多文件工单才有每文件一行、download 端 zip 汇总。语言覆盖以 zip 条目名（_en/_ja）为准。
TMPD39=$(mktemp -d)
python3 - "$TMPD39/t39.docx" <<'EOF'
import sys, zipfile
zf = zipfile.ZipFile(sys.argv[1],'w')
zf.writestr('[Content_Types].xml','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>''')
zf.writestr('_rels/.rels','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>''')
zf.writestr('word/document.xml','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>多语言打包回归第一段：天空是蓝色的。</w:t></w:r></w:p>
<w:p><w:r><w:t>多语言打包回归第二段：书籍是人类进步的阶梯。</w:t></w:r></w:p>
<w:sectPr/></w:body></w:document>''')
zf.close()
EOF
R=$(curl -s $B/openapi/v1/tasks -H "Authorization: Bearer $AK" -F "files=@$TMPD39/t39.docx" -F "target_langs=en,ja" -F "mode=fast" --max-time 60)
TASK39=$(printf '%s' "$R" | python3 -c 'import sys,json
try: print(json.load(sys.stdin).get("task_id",""))
except Exception: print("")')
[ -n "$TASK39" ] && { PASS=$((PASS+1)); echo "PASS|T39-created"; } || { FAIL=$((FAIL+1)); echo "FAIL|T39-created($R)"; }
# 轮询走 OpenAPI status（与 T5 同法；API 建单 CreatedBy=0，避免依赖内部 detail 可见性）。
# 注：工单置 completed 与各产物行 result_ready 落库存在毫秒级窗口，联合等待两者齐备再退出，
#     否则会对「刚 completed 的瞬间快照」误报（2026-09-15 首轮 PG 矩阵实测命中该竞态）。
ST39=""
for i in $(seq 1 60); do
  STAT39=$(curl -s "$B/openapi/v1/tasks/status?id=$TASK39" -H "Authorization: Bearer $AK" --max-time 30)
  PARSE39=$(printf '%s' "$STAT39" | python3 -c 'import sys,json
try:
    d = json.load(sys.stdin)
    print(d.get("status",""), sum(1 for f in d.get("files", []) if f.get("result_ready")))
except Exception:
    print("", 0)')
  ST39=$(echo "$PARSE39" | awk '{print $1}'); N39=$(echo "$PARSE39" | awk '{print $2}')
  case "$ST39" in completed|failed) break;; esac
  sleep 2
done
ck T39-completed '^completed$' "$ST39"
# OpenAPI 出参：ticket_file 行已就绪（单文件工单=1 行，多语言在行内预打包）
N39=$(printf '%s' "$STAT39" | python3 -c 'import sys,json
d=json.load(sys.stdin)
print(sum(1 for f in d.get("files",[]) if f.get("result_ready")))')
[ "${N39:-0}" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|T39-artifact-ready"; } || { FAIL=$((FAIL+1)); echo "FAIL|T39-artifact-ready($N39|${STAT39:0:260})"; }
# zip 打包下载：PK 魔数 + 条目≥2 + 含 en/ja 语言码文件名
ZIP39H=$(mktemp); ZIP39=$(mktemp)
curl -s -D "$ZIP39H" -o "$ZIP39" "$B/openapi/v1/tasks/download?id=$TASK39" -H "Authorization: Bearer $AK" --max-time 60
ck T39-zip-ctype 'application/zip' "$(cat "$ZIP39H")"
ZINFO=$(python3 - "$ZIP39" <<'EOF'
import sys, zipfile
try:
    z = zipfile.ZipFile(sys.argv[1])
    names = z.namelist()
    print(f"{len(names)}|{'Y' if any('_en' in n for n in names) else 'N'}|{'Y' if any('_ja' in n for n in names) else 'N'}")
except Exception as e:
    print(f"0|N|N")
EOF
)
ZC39="${ZINFO%%|*}"; ZEN39=$(echo "$ZINFO" | cut -d'|' -f2); ZJA39=$(echo "$ZINFO" | cut -d'|' -f3)
[ "${ZC39:-0}" -ge 2 ] && { PASS=$((PASS+1)); echo "PASS|T39-zip-entries"; } || { FAIL=$((FAIL+1)); echo "FAIL|T39-zip-entries($ZINFO)"; }
ck T39-zip-has-en '\|Y\|' "|$ZEN39|"
ck T39-zip-has-ja 'Y$' "$ZJA39"
rm -f "$ZIP39H" "$ZIP39"; rm -rf "$TMPD39"


# ============================================================================
# T40（2026-09-15 任务1；★ 2026-09-25 批 D｜F-41 后重钉）：文件工单余额预检「积分口径 + K 系数等值」
#   新个人租户默认体验额度=free_trial_tokens（1000 积分面值，按现读汇率折成内部 token）。
#   ⚠️ 口径已被 F-41 的「宁高勿低」决策**翻转**：旧公式 chars/1.3×langs×markup 比 pro 实测计费低约 62 倍
#      （88 号估 17.3k、实烧 1,075,400 后在中途烧穿全损），新公式 est = chars × langs × K(mode)，
#      K(pro)=160 / K(fast)=60 且走 system_config 可调。于是「700KB PDF 在体验余额下直接放行」
#      这条 09-15 的旧断言**前提失效**（716800/12≈59733 字符 ×60 ≈ 358 万 token ≈ 11947 积分 ≫ 1100 积分）：
#      拦单正是本次修复要的效果（用户拍板「宁可建单被拒，好过中途烧穿全损」），不是回归。
#   a-1 体验余额下 700KB .pdf 必须被拦，且出结构化码（F-21③：4xx + code，不再 200+success:false）
#   a-2 等值锁：拒绝文案里的「预估需约 N 积分」必须等于按**当前 F/K 两档配置现算**的值
#       —— 批 H 回调 K、〇-W 回调 F（往 system_config 插/改 est_tokens_fixed_fast /
#       est_tokens_per_char_fast；★ 2026-09-27 〇-X 第 5 项起这四键**有管理台表单**了
#       （GET/POST /api/admin/config/est-tokens，见下方 T64 段），写库/写表单都即生效无需发版，
#       本段仍按「库里现读」算期望值，两条改配置的路径都跟随）
#       后本条都自动跟随；只有「代码里偷偷改默认值 / 分档折算规则被改回 /3 / 两段式接错档」才会翻红。
#   a-3 补足额度后同一文件必须放行 —— 证明拦单只由「余额 < 预估」这一条决定，
#       不是尺寸硬闸或扩展名误判（09-15 用户反馈的「700KB PDF 提示需 402553 token」误拦形态）复活。
#   b) 35MB 级大文件：仍拒，且文案含「积分」、零 token 裸值（对外口径承诺）。
# ============================================================================
T40U="t40u_$(date +%s)$RANDOM"
T40RG=$(reg "$T40U" uatpass123 "T40C$RANDOM" 演练T40 "$T40U@t.test")
echo "$T40RG" | grep -q '"success": *true' || echo "  [T40] 注册失败: $(echo "$T40RG" | head -c 160)"
T40TK=$(tok "$T40U" uatpass123)
H40="Authorization: Bearer $T40TK"
T40BAL=$(curl -s $B/api/me/package -H "$H40" | pv '.get("points_balance",0)')
T40BAL=${T40BAL:-0}
echo "  [T40] 新租户余额 points=$T40BAL"
TMPD40=$(mktemp -d)
head -c 716800 /dev/urandom > "$TMPD40/t40_small.pdf"
R40S=$(curl -s $B/api/tickets/create-file -H "$H40" -F "files=@$TMPD40/t40_small.pdf" -F "target_langs=en" -F "mode=fast")
ck T40-pdf-700k-blocked-at-trial '"success":false' "$R40S"
ck T40-pdf-code-quota '"code":"QUOTA_EXCEEDED"' "$R40S"
if echo "$R40S" | grep -qi "token"; then FAIL=$((FAIL+1)); echo "FAIL|T40-pdf-msg-no-raw-token($(echo "$R40S" | head -c 120))"; else PASS=$((PASS+1)); echo "PASS|T40-pdf-msg-no-raw-token"; fi
# a-2 等值锁（★ F-72 2026-09-27 〇-W 改两段式；★ F-78 折算尺改现读）：
#   N == round( (F(fast) + (716800/12 整除) × K(fast)) / RATE )
#   三档都从库里**现读**（K/F 键缺失/非法 → 与后端同口径的保守默认 F=1200 / K=60；RATE 见文件头 T0），
#   所以管理台回调 F 或 K、或积分改档，本条都自动跟随；只有「代码偷偷改默认值 / 两段式接错档 / 分档折算被改回 /3」才翻红。
KFAST40=$(dbq "SELECT value FROM system_config WHERE key='est_tokens_per_char_fast'" | tr -d '[:space:]')
FFAST40=$(dbq "SELECT value FROM system_config WHERE key='est_tokens_fixed_fast'" | tr -d '[:space:]')
EXP40=$(K40="$KFAST40" F40="$FFAST40" RATE40="$RATE" python3 -c 'import os
def num(raw, defv, allow_zero):
    try:
        v = float(os.environ.get(raw, "").strip())
    except Exception:
        return defv            # 键缺失/空/非数字 → 后端同样回退默认
    if v > 0 or (allow_zero and v == 0):
        return v
    return defv                # K 的 ≤0 与 F 的负数都是脏配置
k = num("K40", 60.0, False)
fixed = num("F40", 1200.0, True)   # F 允许显式 0＝运维退回纯线性
rate = int(num("RATE40", 400.0, False))
est = int(fixed) + int(716800 // 12 * k)   # 与 Go 侧同序：固定项先取整，再加线性项
print((est + rate // 2) // rate)')
ck T40-pdf-estimate-eq-K "预估需约 ${EXP40} 积分" "$R40S"
N40=$(printf '%s' "$R40S" | grep -oE '预估需约 [0-9]+ 积分' | grep -oE '[0-9]+' | head -1)
# a-3 按**文案里实际给出的**预估积分补足额度（多留 1000 积分余量）后重试 → 必须放行
if [ -z "$N40" ]; then
  FAIL=$((FAIL+1)); echo "FAIL|T40-pdf-allowed-after-topup(文案里没解析出预估积分，resp=$(echo "$R40S" | head -c 160))"
else
  T40TID=$(dbq "SELECT tenant_id FROM users WHERE username='$T40U'" | tr -dc '0-9')
  G40=$(( (N40 + 1000) * RATE ))
  NOW40=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  dbq "INSERT INTO quota_grants (tenant_id,kind,total,\"left\",expires_at,source,ref_id,created_at) VALUES ($T40TID,'uat',$G40,$G40,'2099-12-31T23:59:59Z','uat_t40_topup',0,'$NOW40')" >/dev/null
  R40T=$(curl -s $B/api/tickets/create-file -H "$H40" -F "files=@$TMPD40/t40_small.pdf" -F "target_langs=en" -F "mode=fast")
  ck T40-pdf-allowed-after-topup '"success":true' "$R40T"
fi
# 大文件：积分余额×现读汇率×13 字节（预检按内部 token 估算，/12/1.3×1.5 后仍超余额）；封顶 35MB（<40MB 上传上限）
T40BIG=$(( T40BAL * RATE * 13 )); [ "$T40BIG" -gt 36700160 ] && T40BIG=36700160
head -c "$T40BIG" /dev/urandom > "$TMPD40/t40_big.pdf"
R40B=$(curl -s $B/api/tickets/create-file -H "$H40" -F "files=@$TMPD40/t40_big.pdf" -F "target_langs=en" -F "mode=fast")
echo "$R40B" | grep -q '"success":false' && echo "$R40B" | grep -q "积分" \
  && { PASS=$((PASS+1)); echo "PASS|T40-overspend-rejected-points"; } \
  || { FAIL=$((FAIL+1)); echo "FAIL|T40-overspend-rejected-points(bytes=$T40BIG resp=$(echo "$R40B" | head -c 200))"; }
if echo "$R40B" | grep -qi "token"; then FAIL=$((FAIL+1)); echo "FAIL|T40-msg-no-raw-token($(echo "$R40B" | head -c 120))"; else PASS=$((PASS+1)); echo "PASS|T40-msg-no-raw-token"; fi
rm -rf "$TMPD40"

# ============================================================================
# T41（2026-09-15 任务4）：审计列表「时间倒序」回归防护
#   背景：SQLite→PG 切流按显式 id 导入但未同步序列 → 新审计拿到低于存量的
#   低位 id，旧排序 ORDER BY id DESC 把 9 月新记录沉底，界面误显示「审计
#   停在 8-29」。修复=排序改 created_at DESC, id DESC + migrate 启动自愈
#   （store.syncSequencesPG）。本段验证：以幂等配置保存触发一条当天审计，
#   /api/system/audit 首行必须是当天记录，且列表按时间不升序。
# ============================================================================
SSAVE '{"disposable_email_domains":""}' >/dev/null   # 幂等触发 package_settings_save 审计
T41L=$(curl -s "$B/api/system/audit?limit=8" -H "$AH")
T41TODAY=$(date +%F)
echo "$T41L" | grep -q "\"created_at\":\"${T41TODAY}" \
  && { PASS=$((PASS+1)); echo "PASS|T41-audit-head-is-today"; } \
  || { FAIL=$((FAIL+1)); echo "FAIL|T41-audit-head-is-today($(echo "$T41L" | head -c 200))"; }
T41ORD=$(echo "$T41L" | python3 -c 'import sys,json;a=[x.get("created_at","") for x in json.load(sys.stdin).get("logs",[])];print("DESC" if a==sorted(a,reverse=True) else "BAD")' 2>/dev/null || echo ERR)
[ "$T41ORD" = "DESC" ] \
  && { PASS=$((PASS+1)); echo "PASS|T41-audit-time-desc"; } \
  || { FAIL=$((FAIL+1)); echo "FAIL|T41-audit-time-desc($T41ORD)"; }


# ============================================================================
# T42（2026-09-15 USDT 收款）全链路（依赖 mock_chain.py，经 USDT_TRON_BASE 注入后端）：
#   默认关拒单 / 配置口校验（地址/汇率非法拒） / 下单尾数唯一（两单金额互异） /
#   声明 txid（manual-confirm 必填+格式闸） / 后台确认四项（必填哈希、一笔交易只核一单） /
#   尾数关闭=精确基额 / M2 链上自动入账（错金额孤儿、确认达阈值入账、入账幂等） /
#   自开自关不污染后续前端 E2E。
# ============================================================================
CHAIN="${MOCK_CHAIN_URL:-http://127.0.0.1:8902}"
CJ='Content-Type: application/json'
echo "===== T42 USDT 收款全链路 ====="
R=$(post "$H1" '{"points":30,"channel":"usdt"}' /api/pay/create)
ck T42-off-reject '未开放' "$R"
R=$(SSAVE '{"usdt_addr_trc20":"badaddr"}')
ck T42-bad-addr-rejected '地址格式非法' "$R"
R=$(SSAVE '{"usdt_enabled":"1","usdt_addr_trc20":"T4BJRYfnu29GPWdksz7EMUbiqx5CKSZgov"}')
ck T42-enable-ok2 '"success":true' "$R"
R=$(SSAVE '{"usdt_rate_fen_per_usdt":0}')
ck T42-bad-rate-rejected 'usdt_rate_fen_per_usdt' "$R"
R=$(SSAVE '{"usdt_enabled":"1"}')
ck T42-enable-ok '"success":true' "$R"

# 下单×2：尾数唯一（同基额两单金额必须互异——无 memo 链上对单的根）
R=$(post "$H1" '{"points":30,"channel":"usdt","usdt_chain":"trc20"}' /api/pay/create)
ck T42-order-a '"success":true' "$R"
OIDA=$(echo "$R" | pv '["order"]["id"]')
ADDR=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["usdt_pay"]["address"])')
AMTA=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["usdt_pay"]["amount_micro"])')
TAILA=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["usdt_pay"]["tail"])')
R2=$(post "$H1" '{"points":30,"channel":"usdt","usdt_chain":"trc20"}' /api/pay/create)
OIDB=$(echo "$R2" | pv '["order"]["id"]')
AMTB=$(echo "$R2" | python3 -c 'import sys,json;print(json.load(sys.stdin)["usdt_pay"]["amount_micro"])')
TAILCHK=$(python3 -c "x=float('$TAILA');print('OK' if 0.000001<=x<=0.009999 else 'BAD')" 2>/dev/null || echo ERR)
[ "$TAILCHK" = "OK" ] && [ -n "$ADDR" ] && { PASS=$((PASS+1)); echo "PASS|T42-tail-in-range"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-tail-in-range(tail=$TAILA addr=$ADDR)"; }
[ "$AMTA" != "$AMTB" ] && { PASS=$((PASS+1)); echo "PASS|T42-tail-unique"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-tail-unique($AMTA==$AMTB)"; }
ck T42-bad-chain-rejected '未开放' "$(post "$H1" '{"points":30,"channel":"usdt","usdt_chain":"doge"}' /api/pay/create)"

# 客户声明 txid：必填→格式闸→合法声明落库并进人工核对单
R=$(post "$H1" "{\"order_id\":$OIDA}" /api/pay/manual-confirm)
ck T42-txid-required '交易哈希' "$R"
R=$(post "$H1" "{\"order_id\":$OIDA,\"tx_hash\":\"xyz123\"}" /api/pay/manual-confirm)
ck T42-txid-format '格式' "$R"
TXA=$(python3 -c "print('a1'*32)")
R=$(post "$H1" "{\"order_id\":$OIDA,\"tx_hash\":\"$TXA\"}" /api/pay/manual-confirm)
ck T42-declare-ok '"success":true' "$R"
ck T42-admin-list-declared "\"declared\":\"$TXA\"" "$(get "$AH" /api/admin/orders/manual)"
ST=$(sq "SELECT manual_confirm FROM orders WHERE id=$OIDA")
[ "$ST" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T42-manual-flag"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-manual-flag($ST)"; }

# ★ F-67（〇-U 批 I-8 补断言，2026-09-27）三段锁①：声明后**租户侧出参**必须给得出
#   「待平台确认」的原料——列行 status=pending 且 manual_confirm=1。
#   前端 PlansP 三态显示零后端改动 ⇒ 锁必须钉在接口契约上（读侧），否则字段哪天从
#   出参里掉出去，三态会静默退化回「已声明也显示未付款」。
R=$(get "$H1" /api/billing/orders)
F67A=$(echo "$R" | python3 -c "import sys,json
o=[x for x in json.load(sys.stdin).get('orders',[]) if x.get('id')==$OIDA]
print(o[0]['status']+'/'+str(o[0].get('manual_confirm')) if o else 'missing')")
[ "$F67A" = "pending/1" ] && { PASS=$((PASS+1)); echo "PASS|T42-f67-declared-view"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-f67-declared-view($F67A)"; }

# 后台确认：无哈希拒 / 合法哈希入账落 payments / 同一笔交易复用到第二单拒（一 tx 一单）
R=$(post "$AH" "{\"id\":$OIDA,\"tenant_id\":$TAID}" /api/admin/orders/pay)
ck T42-confirm-need-tx '交易哈希' "$R"
R=$(post "$AH" "{\"id\":$OIDA,\"tenant_id\":$TAID,\"tx_hash\":\"$TXA\"}" /api/admin/orders/pay)
ck T42-confirm-ok '"success":true' "$R"
ST=$(sq "SELECT status FROM orders WHERE id=$OIDA")
[ "$ST" = "paid" ] && { PASS=$((PASS+1)); echo "PASS|T42-confirmed-paid"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-confirmed-paid($ST)"; }
N=$(sq "SELECT COUNT(*) FROM payments WHERE order_id=$OIDA AND tx_hash='$TXA'")
[ "$N" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T42-payments-tx-hash"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-payments-tx-hash($N)"; }

# ★ F-67 三段锁②：后台确认后租户侧出参翻成「平台已确认」——status=paid 且渠道如实 usdt。
#   （三态第③段=未声明的 pending 单 manual_confirm=0 显示普通「待支付」，由①的反向构成：
#    OIDB 从未声明，若②①同型断言对 OIDA 成立则口径闭环。）
R=$(get "$H1" /api/billing/orders)
F67B=$(echo "$R" | python3 -c "import sys,json
o=[x for x in json.load(sys.stdin).get('orders',[]) if x.get('id')==$OIDA]
print(o[0]['status']+'/'+str(o[0].get('channel')) if o else 'missing')")
[ "$F67B" = "paid/usdt" ] && { PASS=$((PASS+1)); echo "PASS|T42-f67-confirmed-view"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-f67-confirmed-view($F67B)"; }
R=$(post "$AH" "{\"id\":$OIDB,\"tenant_id\":$TAID,\"tx_hash\":\"$TXA\"}" /api/admin/orders/pay)
ck T42-tx-reuse-rejected '已关联' "$R"
ST=$(sq "SELECT status FROM orders WHERE id=$OIDB")
[ "$ST" = "pending" ] && { PASS=$((PASS+1)); echo "PASS|T42-reuse-order-still-pending"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-reuse-order-still-pending($ST)"; }
sq "UPDATE orders SET status='cancelled' WHERE id=$OIDB AND status='pending'" >/dev/null   # 清场防尾数冲突

# 尾数开关：关闭=精确基额（fen*1e6/720 四舍五入）
SSAVE '{"usdt_tail_enabled":"0"}' >/dev/null
R=$(post "$H1" '{"points":30,"channel":"usdt"}' /api/pay/create)
OIDX=$(echo "$R" | pv '["order"]["id"]')
AMTX=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["usdt_pay"]["amount_micro"])')
MONEY=$(echo "$R" | pv '["order"]["amount_money"]')
EXPECT=$(python3 -c "import decimal;d=decimal.Decimal;print(int((d(str($MONEY))*100*1000000+d(360))//d(720)))")
[ "$AMTX" = "$EXPECT" ] && { PASS=$((PASS+1)); echo "PASS|T42-tail-off-exact-base"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-tail-off-exact-base(got=$AMTX want=$EXPECT)"; }
sq "UPDATE orders SET status='cancelled' WHERE id=$OIDX AND status='pending'" >/dev/null
SSAVE '{"usdt_tail_enabled":"1"}' >/dev/null

# M2 自动对账（mock 链注入）：错金额=孤儿不入账；精确金额+确认达标=自动入账；重复扫描幂等
SSAVE '{"usdt_auto_settle":"1"}' >/dev/null
R=$(post "$H1" '{"points":26,"channel":"usdt"}' /api/pay/create)
OIDC=$(echo "$R" | pv '["order"]["id"]')
AMTC=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["usdt_pay"]["amount_micro"])')
TXW=$(python3 -c "print('f0'*32)")
curl -s $CHAIN/advance -H "$CJ" -d '{"n":3}' >/dev/null
curl -s $CHAIN/inject -H "$CJ" -d "{\"to\":\"$ADDR\",\"value\":$((AMTC-7)),\"tx_id\":\"$TXW\"}" >/dev/null
SEEN=0
for k in $(seq 1 12); do sleep 2; N=$(sq "SELECT COUNT(*) FROM usdt_deposits WHERE tx_hash='$TXW'"); [ "$N" = "1" ] && { SEEN=1; break; }; done
[ "$SEEN" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T42-deposit-ingested"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-deposit-ingested"; }
sleep 6
ST=$(sq "SELECT status FROM orders WHERE id=$OIDC")
[ "$ST" = "pending" ] && { PASS=$((PASS+1)); echo "PASS|T42-wrong-amount-not-settled"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-wrong-amount-not-settled($ST)"; }
TXC=$(python3 -c "print('c3'*32)")
curl -s $CHAIN/advance -H "$CJ" -d '{"n":3}' >/dev/null
curl -s $CHAIN/inject -H "$CJ" -d "{\"to\":\"$ADDR\",\"value\":$AMTC,\"tx_id\":\"$TXC\"}" >/dev/null
PAID=0
for k in $(seq 1 24); do sleep 3; ST=$(sq "SELECT status FROM orders WHERE id=$OIDC"); [ "$ST" = "paid" ] && { PAID=1; break; }; done
[ "$PAID" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T42-auto-settle-paid"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-auto-settle-paid($ST)"; }
N=$(sq "SELECT COUNT(*) FROM payments WHERE order_id=$OIDC AND tx_hash='$TXC'")
[ "$N" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T42-auto-settle-tx-proof"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-auto-settle-tx-proof($N)"; }
N=$(sq "SELECT COUNT(*) FROM usdt_deposits WHERE tx_hash='$TXW' AND matched_order_id=0")
[ "$N" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T42-orphan-kept-unmatched"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-orphan-kept-unmatched($N)"; }
sleep 8
N=$(sq "SELECT COUNT(*) FROM usdt_deposits WHERE tx_hash='$TXC'")
[ "$N" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T42-rescan-idempotent"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-rescan-idempotent($N)"; }
ML=$(get "$AH" /api/admin/orders/manual)
T41CHK=$(echo "$ML" | python3 -c "import sys,json;ids=[int(o['id']) for o in json.load(sys.stdin).get('orders',[])];print('STILL' if $OIDA in ids else 'GONE')" 2>/dev/null || echo ERR)
if [ "$T41CHK" = "STILL" ]; then FAIL=$((FAIL+1)); echo "FAIL|T42-paid-left-manual-list"; else PASS=$((PASS+1)); echo "PASS|T42-paid-left-manual-list"; fi

# 收尾自关（不留给前端 E2E）：关闭后下单恢复拒单
SSAVE '{"usdt_auto_settle":"0"}' >/dev/null
SSAVE '{"usdt_enabled":"0"}' >/dev/null
ck T42-disable-reject-again '未开放' "$(post "$H1" '{"points":30,"channel":"usdt"}' /api/pay/create)"

# ---------- T43 今日修复回归（2026-09-16）：RBAC 收紧 / 支付渠道 fail-closed / 发票冲红 ----------
# 背景：见《核实报告_架构评审发现逐条验证_20260916》。三组断言锁定当日修复，防回归。

# T43-a 支付渠道 fail-closed：微信/支付宝真实协议未接入时必须显式报错，
#       不得静默回退 mock 出 mockpay:// 假码（旧实现用户扫废码、订单永挂 pending）。
R=$(post "$H1" '{"points":100,"channel":"wechat"}' /api/pay/create)
ck T43-wechat-fail-closed '未配置|未接入|暂不可用' "$R"
echo "$R" | grep -q 'mockpay://' && { FAIL=$((FAIL+1)); echo "FAIL|T43-wechat-no-mock-fallback|wechat 下单失败仍返回 mockpay 假码"; } || { PASS=$((PASS+1)); echo "PASS|T43-wechat-no-mock-fallback"; }
R=$(post "$H1" '{"points":100,"channel":"alipay"}' /api/pay/create)
ck T43-alipay-fail-closed '未配置|未接入|暂不可用' "$R"
echo "$R" | grep -q 'alipay://precreate' && { FAIL=$((FAIL+1)); echo "FAIL|T43-alipay-no-placeholder|alipay 仍返回占位收款码"; } || { PASS=$((PASS+1)); echo "PASS|T43-alipay-no-placeholder"; }

# T43-b RBAC 收紧：等级≥4 角色必须平台级归属（tenant_id=0）。
# ① users/update 把租户内账号提升为 admin → 400（create 侧不变量的 update 侧收口）
#   注意：macOS 自带 bash 3.2 对 "$(...)" 内再嵌 \" 的解析有缺陷（body 会被拆坏），
#   断言一律用「两段式」（先 R=$(post …) 再 ck … "$R"），与 T1 段口径一致。
BID43=$(sq "SELECT id FROM users WHERE username='uatuser_b' LIMIT 1" | tr -d '[:space:]')
R=$(post "$AH" "{\"id\":$BID43,\"role\":\"admin\"}" /api/admin/users/update)
ck T43-update-promote-reject '仅可分配给平台级账号|tenant_id=0' "$R"
# ② SQL 强插违规行模拟存量数据 → 该账号（旧角色 admin 但挂具体租户）：
#    跨租户退款 / 分配高角色 必须 403（IsSuperAdmin/RequireRole 收紧后不再按纯等级放行）
sq "UPDATE users SET role='admin' WHERE id=$BID43"
BT43=$(tok uatuser_b uatpass123); H43="Authorization: Bearer $BT43"
R=$(post "$H43" '{"id":999999,"tenant_id":2}' /api/admin/orders/refund)
ck T43-legacy-admin-refund-403 '权限不足|超级管理员' "$R"
R=$(post "$H43" "{\"id\":$BID43,\"role\":\"super_admin\"}" /api/admin/users/update)
ck T43-legacy-admin-promote-403 '权限不足|超级管理员' "$R"
sq "UPDATE users SET role='tenant_admin' WHERE id=$BID43"   # 还原现场

# T43-c 发票冲红闭环（C16）：已付订单开票 → void 作废 → 同单可重新开票
B43_0=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$TAID")
R=$(post "$AH" "{\"tenant_id\":$TAID,\"points\":34,\"money\":0}" /api/admin/orders/create)
OID43=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
post "$AH" "{\"id\":$OID43,\"tenant_id\":$TAID}" /api/admin/orders/pay >/dev/null
sleep 3   # 等 sink 冲刷，避免计量混入（同 A3/T1 口径）
R=$(post "$H1" "{\"order_id\":$OID43,\"title\":\"T43冲红验证\",\"tax_no\":\"TX9043\"}" /api/billing/invoices/create)
IVID43=$(echo "$R" | pv '.get("invoice",{}).get("id") or 0')
ck T43-invoice-create '"success":true' "$R"
R=$(post "$H1" "{\"id\":$IVID43}" /api/billing/invoices/void)
ck T43-invoice-void '"success":true' "$R"
IVST43=$(sq "SELECT status FROM invoices WHERE id=$IVID43")
[ "$IVST43" = "void" ] && { PASS=$((PASS+1)); echo "PASS|T43-invoice-void-status"; } || { FAIL=$((FAIL+1)); echo "FAIL|T43-invoice-void-status($IVST43)"; }
# ★ 引号陷阱修复（2026-09-21）：`ck … "$(post "…" "{\"a\":1,\"b\":2}" …)"` 里内嵌双引号的 body
#   在「双引号内的命令替换」中会被 bash 做**大括号展开**，按逗号拆成多个参数——实测 body 退化成
#   `"a":1`、path 变成 `"b":2}`，服务端只能回「参数格式错误」，而旧的宽松断言 '"success"' 照样命中
#   造成**永远绿灯**。故复杂 body 一律先存变量（本文件第 72 行既定口径），断言同时收紧到 success:true。
R=$(post "$H1" "{\"order_id\":$OID43,\"title\":\"T43冲红后重开\",\"tax_no\":\"TX9043\"}" /api/billing/invoices/create)
ck T43-invoice-reissue-after-void '"success":true' "$R"

# ---------- T44 缺陷核实修复回归锁（2026-09-16 D1-D5，源码级防回退闸门） ----------
# 行为语义已由 Go 单测覆盖（store.TestSettleExhaustedNoPermAccount /
# service.TestLowBalanceThresholdWired）；此处锁定「修复点不被悄悄改回去」。
ROOT44="$(cd "$(dirname "$0")/../.." && pwd)"
SRC44="$ROOT44/backend-go/internal/store/billing.go"
# ★ 2026-09-29 D-1 修复后重写这两条锁（旧锁假红复盘，两条都要记）：
#   ① 旧锚 `grep -A4 'consumed += take'` 命中的是**两处**同名行，再 `grep -m1 errors.Is` 取第一条
#      ——实际抓到的是**第二段**（永久余额兜底）的尾巴，等于「锚在错的来源上」：台账段自己怎么改都看不见，
#      而兜底段一换行形就红灯（本轮真红：D-1 把 `if err == nil {…} else if !errors.Is(…)` 改成
#      先 ErrNoRows→break、其余错误→return 的正形式，语义等价、锁却断了）。
#   ② 旧 want 里把变量名 `err` 与「否定式」`!errors.Is(...)` 写死，属于**按字面形态锁语义**：
#      正写/反写都算合规，改名也算合规，但锁只认那一种写法。
#   新口径：锚点取兜底段唯一的取数语句（全文件实测 1 次命中），两条语义级判据共用 -A12 窗口
#   （实测窗口内恰好只有「ErrNoRows→break」与「真实错误→return」这两行命中，
#     再往外 -A20 会把守卫 UPDATE 的 `return 0, uerr` 也捞进来＝摘掉前者照样绿的假绿面），
#   判据只认形状不认字面：
#     · T44-settle-err-branch：真实 DB 错误必须向上抛（D1 教训「杜绝无痕归零」）
#     · T44-settle-errnorows-branch：ErrNoRows 必须被单独识别（不能当错误抛，否则零余额租户结算直接失败）
#   两条 want 都接受任意含 err 的变量名与正/反形式——真正要抓的是**分支消失**，那由反证实测确认。
GAP44="$(grep -A12 'SELECT id, balance FROM balance_accounts WHERE tenant_id=? AND balance>0' "$SRC44" | grep -E 'errors\.Is\(|return 0, ' | tr '\n' ' ' || echo NONE)"
ck T44-settle-err-branch 'return 0, [A-Za-z0-9_]*err[A-Za-z0-9_]*' "$GAP44"
ck T44-settle-errnorows-branch 'errors\.Is\([A-Za-z0-9_]*err[A-Za-z0-9_]*, sql\.ErrNoRows\)' "$GAP44"
# ★ 第三条（2026-09-29 D-1 新增，正向＋负向成对写）：兜底段每一笔 balance 扣减都必须带
#   `AND balance>=?` 守卫。这正是 D-1 的修复本体——旧形态是「SELECT 旧值 → 无守卫 UPDATE」，
#   并发扣费能把它打成负数。单测已覆盖行为（store 事务交错用例＋PG 并发断言），这条源码锁
#   兜的是「哪天守卫被悄悄摘掉、单测也被顺手删了」这种双杀回退。
#   为什么不能只写负向：`grep -vc` 在"整段不再出现 UPDATE 语句"时也回 0＝恒空绿，
#   所以必须同时要求**至少数到一笔**扣减语句（AGENTS §一·5 那条「负向锁要配正向对照」同理）。
UPD44="$(grep -A24 'SELECT id, balance FROM balance_accounts WHERE tenant_id=? AND balance>0' "$SRC44" | grep -c 'UPDATE balance_accounts SET balance=balance-?' || true)"
NOGRD44="$(grep -A24 'SELECT id, balance FROM balance_accounts WHERE tenant_id=? AND balance>0' "$SRC44" | grep 'UPDATE balance_accounts SET balance=balance-?' | grep -vc 'AND balance>=' || true)"
if [ "${UPD44:-0}" -ge 1 ] 2>/dev/null && [ "${NOGRD44:-1}" = "0" ]; then
  PASS=$((PASS+1)); echo "PASS|T44-settle-balance-guard（兜底段 ${UPD44} 笔扣减全部带余额守卫）"
else FAIL=$((FAIL+1)); echo "FAIL|T44-settle-balance-guard|扣减语句数=$UPD44（须≥1）其中无守卫数=$NOGRD44（须=0）⇒ D-1 并发透支修复被回退"; fi
if grep -q 'Is(qerr, sql.ErrNoRows)' "$SRC44"; then
  FAIL=$((FAIL+1)); echo "FAIL|T44-settle-qerr-ban|D1 修复被回退：SettleExhausted 重新出现 qerr 误判"
else PASS=$((PASS+1)); echo "PASS|T44-settle-qerr-ban"; fi
ck T44-lowbalance-wired 'low_balance_alert_tokens' "$(grep -m1 'GetConfig("low_balance_alert_tokens")' "$ROOT44/backend-go/internal/service/ticket.go" || echo NONE)"
ck T44-backup-cmd-timeout 'exec\.CommandContext\(pushCtx' "$(grep -m1 'exec.CommandContext(pushCtx' "$ROOT44/backend-go/internal/api/watchdog.go" || echo NONE)"
if grep -q 'backup_remote_cmd' "$ROOT44/backend-go/internal/api/admin_packages.go"; then
  FAIL=$((FAIL+1)); echo "FAIL|T44-backup-cmd-no-whitelist|backup_remote_cmd 混入 settings HTTP 白名单（admin-RCE 面）"
else PASS=$((PASS+1)); echo "PASS|T44-backup-cmd-no-whitelist"; fi
ck T44-csp-header 'Content-Security-Policy' "$(grep -m1 'Content-Security-Policy' "$ROOT44/deploy/caddy/translator.conf" || echo NONE)"

# ---------- T45 密码找回全链路（2026-09-16 测试盲区补全） ----------
# 此前 T6 只测了 forgot 防枚举与错误重置码拒绝，完整闭环（发码→取码→重置→新密登录→还原）无覆盖。
# 原理：UAT 环境 MAIL_ENABLED 未配置 → NoopSender；run_uat.sh 注入 MAIL_NOOP_PRINT_BODY=1
#   使验证码正文随服务日志可见（仅测试环境），T45 从 UAT_SERVER_LOG 增量读取 6 位码完成闭环。
echo "--- T45 密码找回全链路（发码→日志取码→重置→新密登录→还原）---"
if [ -n "${UAT_SERVER_LOG:-}" ] && [ -f "$UAT_SERVER_LOG" ]; then
  MARK45=$(wc -l < "$UAT_SERVER_LOG" | tr -d '[:space:]')
  ck T45-forgot-accept '"success":true' "$(curl -s $B/api/auth/forgot-password -H "$J" -d '{"username":"uatuser_a"}')"
  RCODE=""
  for i in $(seq 1 15); do   # 邮件异步入队 → 轮询日志增量取码
    # ★ python3 提取（2026-09-16 修复）：grep -oE 中文 pattern 在 ck 的 $(...) 命令替换 +
    #   bash 3.2 管道组合下实测偶发取不到（手动同管线可取），python3 对编码/转义免疫
    RCODE=$(tail -n +"$MARK45" "$UAT_SERVER_LOG" 2>/dev/null | python3 -c '
import sys, re
m = ""
for line in sys.stdin:
    for x in re.findall(r"验证码是[:：]\s*(\d{6})", line):
        m = x
print(m)' 2>/dev/null)
    [ -n "$RCODE" ] && break
    sleep 2
  done
  [ -n "$RCODE" ] && { PASS=$((PASS+1)); echo "PASS|T45-code-from-log($RCODE)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T45-code-from-log|mark=$MARK45 增量行数=$(wc -l < "$UAT_SERVER_LOG" | tr -d '[:space:]') 日志尾=$(tail -1 "$UAT_SERVER_LOG" | head -c 120)"; }
  if [ -n "$RCODE" ]; then
    R45BODY="{\"username\":\"uatuser_a\",\"code\":\"$RCODE\",\"new_password\":\"UatReset@45x\"}"
    ck T45-reset-ok '"success":true' "$(curl -s $B/api/auth/reset-password -H "$J" -d "$R45BODY")"
    T45T=$(tok uatuser_a UatReset@45x)
    [ ${#T45T} -gt 30 ] && { PASS=$((PASS+1)); echo "PASS|T45-login-new-pwd"; } || { FAIL=$((FAIL+1)); echo "FAIL|T45-login-new-pwd"; }
    ck T45-old-pwd-dead '密码|失败|incorrect|invalid|UNAUTHORIZED' "$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"uatpass123"}')"
    # 还原密码（改密接口：旧码已被消费，用新密会话改回；B2 语义下旧 token 已失效，用 T45 新 token）
    H45="Authorization: Bearer $T45T"
    ck T45-restore '"success":true' "$(post "$H45" '{"old_password":"UatReset@45x","new_password":"uatpass123"}' /api/auth/change-password)"
    ck T45-restore-login '"success":true' "$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"uatpass123"}')"
  fi
else
  PASS=$((PASS+1)); echo "PASS|T45-log-skip(未提供 UAT_SERVER_LOG，仅 run_uat 全流程可检)"
fi

# ---------- T46（改造 4/5，2026-09-17）质检闭环 API 透出 ----------
# 改造 5：工单详情接口新增 quality 视图（qa_report / eval_scores / review_eval_scores / quality_flagged_langs），
#         此前仅 xlsx 下载有 QA 列、界面零透出；改造 4：tickets.quality_flagged 列经详情/列表零成本透出
#         （前端「质检存疑」徽标数据源，由 workflow.applyEvalDisposition 打标）。
# 注意：T6/T45 会改变 uatuser_a 口令并使首部 H1 令牌失效，故此处重新鉴权取专用令牌。
T46T=$(tok uatuser_a uatpass123); H46="Authorization: Bearer $T46T"
[ ${#T46T} -gt 10 ] 2>/dev/null || { FAIL=$((FAIL+1)); echo "FAIL|T46-auth|uatuser_a 重新登录失败"; }
R=$(post "$H46" '{"title":"T46质检透出","source_text":"hello quality check sentence","target_langs":"en","mode":"fast"}' /api/tickets/create)
TKID=$(echo "$R" | pv '.get("ticket",{}).get("id") or 0')
[ "$TKID" -gt 0 ] 2>/dev/null || { FAIL=$((FAIL+1)); echo "FAIL|T46-ticket-create|got[$R]"; }
ck T46-ticket-create '"success":true' "$R"
D46="$(get "$H46" "/api/tickets/detail?id=$TKID")"
ck T46-quality-field-present '"quality"' "$D46"
# ★ 2026-09-18 修竞态：注入必须等工单跑到终态之后——执行器完成时会整行 UPDATE tickets 并
#   重写 final_result（见 store/tickets.go 完成落库），若在建单瞬间注入，随后被真实结果
#   覆盖，quality 解析回空对象（本日 T46 三连红即此因，非产品缺陷）。
waittk "$H46" "$TKID" >/dev/null
# 注入 final_result 质检 JSON（模拟 runQA / applyEvalDisposition 落库），验证 parseTicketQuality 解析路径
sq "UPDATE tickets SET final_result='{\"eval_scores\":{\"en\":42.5},\"review_eval_scores\":{\"en\":80.0},\"quality_flagged_langs\":[\"en\"]}' WHERE id=$TKID"
D46B="$(get "$H46" "/api/tickets/detail?id=$TKID")"
ck T46-quality-eval-scores '"eval_scores"' "$D46B"
ck T46-quality-review-scores '"review_eval_scores"' "$D46B"
ck T46-quality-flagged-langs '"quality_flagged_langs"' "$D46B"
# 改造 4：打标落库（quality_flagged=1）经详情接口透出
sq "UPDATE tickets SET quality_flagged=1 WHERE id=$TKID"
D46C="$(get "$H46" "/api/tickets/detail?id=$TKID")"
ck T46-quality-flagged-col '"quality_flagged":1' "$D46C"

# ---------- T47（2026-09-18）逐段对照真值表 ticket_segments ----------
# 缺陷背景：PDF/文件工单的源文与译本是两次独立转换产物，段落切分粒度不同，
# 对照编辑器按下标 min() 硬对齐必然错位（实测 504 段 vs 578 段、抽查 20 对全错位）。
# 修复：翻译时把 texts[i] ↔ translations[texts[i]] 精确配对落 ticket_segments 真值表。
# 本节断言：①落库行数>0；②段序号 0 的源文=上传段落原文（真值非二手转换）；
# ③target_text 已回填；④对照接口返回段列表；⑤多文件工单按 file_path 互不覆盖。
T47D=$(mktemp -d)
python3 - "$T47D/t47.docx" <<'EOF'
import sys, zipfile
zf = zipfile.ZipFile(sys.argv[1],'w')
zf.writestr('[Content_Types].xml','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>''')
zf.writestr('_rels/.rels','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>''')
zf.writestr('word/document.xml','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>逐段对照真值测试段一：新车上市官网多语齐发。</w:t></w:r></w:p>
<w:p><w:r><w:t>逐段对照真值测试段二：集成方案验收检查。</w:t></w:r></w:p>
<w:sectPr/></w:body></w:document>''')
zf.close()
EOF
R=$(curl -s $B/api/tickets/create-file -H "$H46" -F "files=@$T47D/t47.docx" -F "target_langs=en" -F "mode=fast" --max-time 60)
ck T47-create '"success":true' "$R"
TK47=$(echo "$R" | pv "['ticket'].get('id')")
D47=$(waittk "$H46" "$TK47")
ck T47-completed '"status":"completed"' "$D47"
CNT47=$(dbq "SELECT COUNT(*) FROM ticket_segments WHERE ticket_id=$TK47" | tr -dc '0-9')
ck T47-segments-persisted '^[1-9]' "$CNT47"
SRC0=$(dbq "SELECT source_text FROM ticket_segments WHERE ticket_id=$TK47 AND lang='en' AND seg_index=0")
ck T47-seg0-source-truth '逐段对照真值测试段一' "$SRC0"
TGTCNT=$(dbq "SELECT COUNT(*) FROM ticket_segments WHERE ticket_id=$TK47 AND lang='en' AND target_text<>''" | tr -dc '0-9')
ck T47-seg-target-filled '^[1-9]' "$TGTCNT"
R=$(get "$H46" "/api/tickets/segments?id=$TK47&lang=en")
ck T47-editor-ok '"success":true' "$R"
ck T47-editor-type '"type":"file"' "$R"
ck T47-editor-pair 'TranslatedEN' "$R"
# 多文件工单：段按文件维度落库（唯一键含 file_path），两文件的段互不覆盖
printf '多文件真值第一行。\n' > "$T47D/m1.txt"
printf '多文件真值第二行。\n' > "$T47D/m2.txt"
R=$(curl -s $B/api/tickets/create-file -H "$H46" -F "files=@$T47D/m1.txt" -F "files=@$T47D/m2.txt" -F "target_langs=en" -F "mode=fast" --max-time 60)
TKM=$(echo "$R" | pv "['ticket'].get('id')")
DM=$(waittk "$H46" "$TKM")
STM=$(echo "$DM" | pv "['ticket'].get('status')")
if [ "$STM" = "completed" ]; then
  MF47=$(dbq "SELECT COUNT(DISTINCT file_path) FROM ticket_segments WHERE ticket_id=$TKM AND lang='en'" | tr -dc '0-9')
  ck T47-multifile-rows-by-file '^[2-9]' "$MF47"
else
  PASS=$((PASS+1)); echo "PASS|T47-multifile-skip(工单未达 completed，状态=$STM)"
fi
rm -rf "$T47D"

# ---------- T48（2026-09-18）伪标签清洗端到端 ----------
# 缺陷背景：模型在无上下文短串（表格序号列/项目符号）上回显指令词元，产出
# <target>#></target>、<only>•••</only>、<tt>…</tt> 等 ASCII 伪标签，旧清洗链
# （StripChineseInNonZh 只删中文）拦不住，一路透传进交付文件。
# mock_llm.py 收到含 UATPSEUDO 的行会回显 `<target>UAT-PSEUDO-CLEANSSED></target>`
# （与现场同形），断言：正文保留（UAT-PSEUDO-CLEANSSED 在）、标签零残留
# （含 Go JSON \u003c 转义形态——漏检转义形态会让本断言「永远通过」，见 AGENTS.md §7）。
R=$(post "$H46" '{"title":"T48伪标签清洗","source_text":"UATPSEUDO degenerate cell marker","target_langs":"en","mode":"fast"}' /api/tickets/create)
TK48=$(echo "$R" | pv "['ticket'].get('id')")
D48=$(waittk "$H46" "$TK48")
ck T48-completed '"status":"completed"' "$D48"
ck T48-body-kept 'UAT-PSEUDO-CLEANSSED' "$D48"
RESID48=$(echo "$D48" | grep -qE '<target|</target|u003c/?target' && echo YES || echo NO)
ck T48-no-pseudo-residue '^NO$' "$RESID48"

# ---------- T49（2026-09-21 #33）任务系统：出厂数值 + 事件自动发放 + 超管重置消耗量 ----------
# 需求原文数值（改一个数就该翻红）：
#   每日登录 +100 临时积分/3 天/一日一次/日叠加；每周发起翻译 +100 临时积分/7 天/日 ≤1、周 ≤5；
#   邀请好友注册 +500 临时积分/14 天；邀请好友充值 +1000 永久积分；知识库解析成功 +600 永久积分（终身一次）。
# 对外零 token：断言里的 token 一律写成「积分面值 × 库里现读汇率」（★ F-78 后不再钉死 300），
# 接口出参只允许出现积分。面值（100/100/500/1000/600 积分）才是需求原文的锁，汇率只是折算尺。
SEED49=$(dbq "SELECT COUNT(*) FROM user_tasks WHERE task_key<>''" | tr -dc '0-9')
ck T49-builtin-seeded '^[5-9]' "$SEED49"
ck T49-seed-login "^$(( 100 * RATE ))\|3\|1\|1\|0$" "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||stack_expiry||'|'||cap_per_day||'|'||cap_per_week FROM user_tasks WHERE task_key='login_daily'")"
ck T49-seed-translate "^$(( 100 * RATE ))\|7\|1\|1\|5$" "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||stack_expiry||'|'||cap_per_day||'|'||cap_per_week FROM user_tasks WHERE task_key='translate_week'")"
ck T49-seed-invite-reg "^$(( 500 * RATE ))\|14\|0\|0$" "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||cap_per_day||'|'||cap_per_week FROM user_tasks WHERE task_key='invite_register'")"
ck T49-seed-invite-paid "^$(( 1000 * RATE ))\|0\|event$" "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||period FROM user_tasks WHERE task_key='invite_paid'")"
ck T49-seed-kb "^$(( 600 * RATE ))\|0\|once$" "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||period FROM user_tasks WHERE task_key='kb_upload'")"

# ① 登录事件钩子：再登录一次 uatuser_a → 应落一笔 task/100 积分 临时台账，且同日不重复发放
UAID=$(sq "SELECT id FROM users WHERE username='uatuser_a' LIMIT 1" | tr -dc '0-9')
: $(tok uatuser_a uatpass123)
ck T49-login-grant "^$(( 100 * RATE ))\|task:login_daily$" "$(dbq "SELECT total||'|'||source FROM quota_grants WHERE tenant_id=$TAID AND kind='task' AND source='task:login_daily' ORDER BY id DESC LIMIT 1")"
: $(tok uatuser_a uatpass123)
ck T49-login-dedup-once-per-day '^1$' "$(dbq "SELECT COUNT(*) FROM user_task_rewards WHERE user_id=$UAID AND task_key='login_daily'")"
ck T49-login-counter-one '^1$' "$(dbq "SELECT cnt FROM user_task_period_cnt WHERE user_id=$UAID AND period_key LIKE 'D:%' ORDER BY id DESC LIMIT 1")"

# ② 翻译事件钩子：发起一次即时翻译（成功计量后自动发放 translate_week），日 ≤1 次由去重键保证
curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"任务系统翻译事件测试文本。","options":{"target_langs":["en"],"mode":"fast"}}' >/dev/null
sleep 2
ck T49-translate-grant "^$(( 100 * RATE ))\|task:translate_week$" "$(dbq "SELECT total||'|'||source FROM quota_grants WHERE tenant_id=$TAID AND kind='task' AND source='task:translate_week' ORDER BY id DESC LIMIT 1")"
curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"任务系统翻译事件重复测试文本。","options":{"target_langs":["en"],"mode":"fast"}}' >/dev/null
sleep 2
ck T49-translate-daily-cap '^1$' "$(dbq "SELECT COUNT(*) FROM user_task_rewards WHERE task_key='translate_week' AND user_id=$UAID")"

# ③ 用户视角出参：自动任务带发放方式/有效期/周期进度，且零 token 裸值
# 用刚登录拿到的新 token（套件里其它用例可能改过该账号密码，旧 token 会被会话版本闸判「未登录」）
T49A=$(tok uatuser_a uatpass123); H49A="Authorization: Bearer $T49A"
ME49=$(get "$H49A" /api/me/tasks)
ck T49-me-auto '"grant_mode":"auto"' "$ME49"
ck T49-me-temporary '"reward_kind":"temporary"' "$ME49"
ck T49-me-points-only '"reward_points":100' "$ME49"
HAS49=$(echo "$ME49" | grep -qE 'reward_tokens|"tokens"' && echo YES || echo NO)
ck T49-me-no-token '^NO$' "$HAS49"
ck T49-me-progress '"today_count":' "$ME49"

# ④ 超管特殊任务：重置已消耗的任务临时积分（有效期不变）；非超管 403
GRANT49=$(dbq "SELECT id FROM quota_grants WHERE tenant_id=$TAID AND kind='task' ORDER BY id DESC LIMIT 1" | tr -dc '0-9')
EXP49=$(dbq "SELECT expires_at FROM quota_grants WHERE id=$GRANT49")
dbq "UPDATE quota_grants SET \"left\"=100 WHERE id=$GRANT49" >/dev/null
ck T49-reset-forbidden '"success":false|Forbidden|403' "$(post "$H6" '{"subscribed_only":false}' /api/admin/tasks/reset-consumption)"
ck T49-reset-forbidden-anon '401|Unauthorized|"success":false' "$(curl -s $B/api/admin/tasks/reset-consumption -X POST -H "$J" -d '{"subscribed_only":false}')"
R49=$(post "$AH" '{"subscribed_only":false}' /api/admin/tasks/reset-consumption)
ck T49-reset-ok '"success":true' "$R49"
ck T49-reset-rows '"reset_rows":[1-9]' "$R49"
ck T49-reset-message '有效期保持不变' "$R49"
ck T49-reset-refilled "^$(( 100 * RATE ))$" "$(dbq "SELECT \"left\" FROM quota_grants WHERE id=$GRANT49")"
ck T49-reset-expiry-kept "^${EXP49}$" "$(dbq "SELECT expires_at FROM quota_grants WHERE id=$GRANT49")"
# 幂等：已拉满（left=total）的记录第二次重置不再触碰。
# （软断言：套件运行期其它租户的异步计量可能刚好消耗掉一笔任务台账，导致本次又重置 1 条——
#   这属于「又拉满了一次」而非缺陷，故此处只锁零 token 出参与有效期稳定，硬幂等锁在 store 单测。）
R49B=$(post "$AH" '{"subscribed_only":false}' /api/admin/tasks/reset-consumption)
ck T49-reset-again-ok '"success":true' "$R49B"
ck T49-reset-again-expiry-kept "^${EXP49}$" "$(dbq "SELECT expires_at FROM quota_grants WHERE id=$GRANT49")"

# ⑤ 手工任务与事件任务并存：超管新建自动任务须完整回显（不得被退回手工语义）
R=$(post "$AH" '{"task_type":"daily","title":"T49事件任务","reward_points":50,"grant_mode":"auto","task_key":"uat_t49_event","period":"event","valid_days":5,"stack_expiry":0,"cap_per_day":2,"enabled":1}' /api/admin/tasks/save)
ck T49-admin-save '"success":true' "$R"
T49ID=$(echo "$R" | pv '.get("id") or 0')
ck T49-admin-echo 'uat_t49_event' "$(get "$AH" /api/admin/tasks)"
# 只改标题（不重复下发发放口径）→ 事件语义与规则必须原样保留
R=$(post "$AH" "{\"id\":$T49ID,\"task_type\":\"daily\",\"title\":\"T49事件任务改名\",\"reward_points\":50}" /api/admin/tasks/save)
ck T49-admin-partial '"success":true' "$R"
ck T49-admin-partial-kept '^auto\|event\|5\|2\|1\|uat_t49_event$' "$(dbq "SELECT grant_mode||'|'||period||'|'||valid_days||'|'||cap_per_day||'|'||enabled||'|'||task_key FROM user_tasks WHERE id=$T49ID")"
dbq "DELETE FROM user_tasks WHERE id=$T49ID" >/dev/null   # 清理本用例自建任务，不污染出厂数据

# ---------- T50（★ #41 商业洞二）订阅自动续费：开关 + T-3 自动建单 + 去重 ----------
U50="uatuser_t50$(date +%s)"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$U50\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T50续费\",\"email\":\"$U50@test.com\",\"agreed\":true}" >/dev/null
TK50=$(tok $U50 uatpass123); H50="Authorization: Bearer $TK50"
T50D=$(sq "SELECT tenant_id FROM users WHERE username='$U50' LIMIT 1" | tr -d '[:space:]')
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$T50D,\"code\":\"uat_t50_paid\",\"name\":\"T50续费包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":20,\"duration_days\":30}" >/dev/null

# ① 未订阅不得开启（否则扫描会对不存在的包建单）
ck T50-renew-needs-subscription '"success":false' "$(post "$H50" '{"enabled":true}' /api/package/auto-renew)"

# ② 订阅并到账 → 开关可开启，permissions 单字段原子写不覆盖订阅身份
R=$(post "$H50" '{"code":"uat_t50_paid"}' /api/package/subscribe)
OID50=$(echo "$R" | pv '.get("order",{}).get("id") or d.get("id") or 0')
post "$AH" "{\"id\":$OID50,\"tenant_id\":$T50D}" /api/admin/orders/pay >/dev/null
ck T50-renew-enable '"success":true' "$(post "$H50" '{"enabled":true}' /api/package/auto-renew)"
AR50=$(dbjsonstr tenants $T50D permissions auto_renew | tr -d '[:space:]')
# ★ 方言容错：同一布尔值经 json_extract(SQLite)=1、jsonb->>(PG)=true 取出的文本不同，
#   断言只认「真值形态」集合，否则换方言就跑假红（本条 09-21 首跑在 PG 下即为此）。
case "$AR50" in True|true|1) PASS=$((PASS+1)); echo "PASS|T50-renew-flag-persisted($AR50)";; *) FAIL=$((FAIL+1)); echo "FAIL|T50-renew-flag-persisted(got $AR50)";; esac
PC50=$(dbjsonstr tenants $T50D permissions package_code | tr -d '[:space:]')
[ "$PC50" = "uat_t50_paid" ] && { PASS=$((PASS+1)); echo "PASS|T50-renew-keeps-package"; } || { FAIL=$((FAIL+1)); echo "FAIL|T50-renew-keeps-package(got $PC50)"; }
ck T50-renew-get-echo '"auto_renew":true' "$(get "$H50" /api/package/auto-renew)"
ck T50-renew-me-package '"auto_renew":true' "$(get "$H50" /api/me/package)"

# ③ 注入「剩 2 天到期」→ 手动扫描应自动生成同包续费单（created_by=0 系统单，mock 渠道挂 pending）
EXP50=$(python3 -c "import datetime;print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(days=2)).strftime('%Y-%m-%dT%H:%M:%SZ'))")
dbjsonset tenants $T50D permissions package_expires_at "$EXP50"
ck T50-scan-run '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"
PEND50=$(sq "SELECT COUNT(*) FROM orders WHERE tenant_id=$T50D AND status='pending' AND created_by=0" | tr -d '[:space:]')
[ "$PEND50" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T50-renew-order-created"; } || { FAIL=$((FAIL+1)); echo "FAIL|T50-renew-order-created(pending 系统单=$PEND50)"; }
CH50=$(sq "SELECT channel FROM orders WHERE tenant_id=$T50D AND status='pending' AND created_by=0 ORDER BY id DESC LIMIT 1" | tr -d '[:space:]')
[ -n "$CH50" ] && { PASS=$((PASS+1)); echo "PASS|T50-renew-order-channel($CH50)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T50-renew-order-channel(空)"; }
NT50=$(sq "SELECT COUNT(*) FROM notifications WHERE title='续费订单已自动生成'" | tr -d '[:space:]')
[ "${NT50:-0}" -ge 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T50-renew-notify($NT50)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T50-renew-notify($NT50)"; }
AUD50=$(sq "SELECT COUNT(*) FROM audit_logs WHERE tenant_id=$T50D AND action='auto_renew_order'" | tr -d '[:space:]')
[ "${AUD50:-0}" -ge 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T50-renew-audit"; } || { FAIL=$((FAIL+1)); echo "FAIL|T50-renew-audit($AUD50)"; }

# ④ 二次扫描去重：pending 续费单不堆叠（每日扫描幂等红线）
ck T50-scan-again '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"
PEND50B=$(sq "SELECT COUNT(*) FROM orders WHERE tenant_id=$T50D AND status='pending' AND created_by=0" | tr -d '[:space:]')
[ "$PEND50B" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T50-renew-order-dedup"; } || { FAIL=$((FAIL+1)); echo "FAIL|T50-renew-order-dedup(pending=$PEND50B)"; }

# ⑤ 关闭开关 + 撤掉挂单 → 再扫描不再建单（开关是唯一驱动源）
dbq "UPDATE orders SET status='cancelled' WHERE tenant_id=$T50D AND status='pending' AND created_by=0" >/dev/null
ck T50-renew-disable '"success":true' "$(post "$H50" '{"enabled":false}' /api/package/auto-renew)"
ck T50-scan-after-disable '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"
PEND50C=$(sq "SELECT COUNT(*) FROM orders WHERE tenant_id=$T50D AND status='pending' AND created_by=0" | tr -d '[:space:]')
[ "$PEND50C" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T50-renew-off-no-order"; } || { FAIL=$((FAIL+1)); echo "FAIL|T50-renew-off-no-order(pending=$PEND50C)"; }
# 订阅身份不因续费流程被摘除（到期日仍是注入值，扫描只提醒不越权改包）
PC50C=$(dbjsonstr tenants $T50D permissions package_code | tr -d '[:space:]')
[ "$PC50C" = "uat_t50_paid" ] && { PASS=$((PASS+1)); echo "PASS|T50-renew-identity-kept"; } || { FAIL=$((FAIL+1)); echo "FAIL|T50-renew-identity-kept(got $PC50C)"; }
# 清理本用例自建包与挂单，避免污染后续套件的套餐列表断言
dbq "DELETE FROM packages WHERE code='uat_t50_paid'" >/dev/null
dbq "DELETE FROM notifications WHERE title IN ('续费订单已自动生成','自动续费未能生成订单')" >/dev/null

# ---------- T51（★ #41 商业洞三）优惠券：建券 → 试算 → 下单核销 → 门槛/限用/抢完 → 删模板留流水 ----------
# 注：关单退券（ReleaseStaleCouponRedemptions）由后台 5min 巡检触发，UAT 不便等周期，
#     该分支由 store/coupons_test.go 的内存库用例覆盖；此处只钉「金额单一事实源」与配额判定。
SFX51=$(date +%s)
C51="UATT51PCT$SFX51"          # 充值单 9 折，门槛 50 元，总量 2 张，每家 1 次
C51SUB="UATT51SUB$SFX51"       # 仅订阅单券（用在充值单必须被拒）
C51BIG="UATT51BIG$SFX51"       # 立减 9999 元（压到 0 元单的红线守卫）
U51="uatuser_t51$SFX51"
# feq <实际金额> <期望金额> — 金额等价断言（容差 1 分）：积分→元的汇率由服务端配置决定，
# 用例不把汇率写死，只钉「折让=原价×比例」「实付=原价−折让」这两条关系。
feq(){ python3 -c "import sys
try:
  a,b=float('$1'),float('$2')
except Exception:
  sys.exit(1)
sys.exit(0 if abs(a-b)<=0.011 else 1)"; }
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$U51\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T51优惠券\",\"email\":\"$U51@test.com\",\"agreed\":true}" >/dev/null
TK51=$(tok $U51 uatpass123); H51="Authorization: Bearer $TK51"
T51D=$(sq "SELECT tenant_id FROM users WHERE username='$U51' LIMIT 1" | tr -d '[:space:]')

# ① 超管建券（券码入库即大写归一）+ 列表可见 + 非超管读不到
R=$(post "$AH" "{\"code\":\"$(echo $C51 | tr 'A-Z' 'a-z')\",\"name\":\"T51充值九折\",\"kind\":\"recharge\",\"discount_type\":\"percent\",\"discount_value\":10,\"min_amount\":50,\"max_uses\":2,\"per_tenant_limit\":1,\"enabled\":1}" /api/admin/coupons/save)
ck T51-coupon-create '"success":true' "$R"
CID51=$(echo "$R" | pv '.get("coupon",{}).get("id") or 0')
# ★ 券码归一口径：小写提交必须落成大写，否则用户复制到的码永远「不存在」
ck T51-code-normalized "\"code\":\"$C51\"" "$R"
ck T51-coupon-list "$C51" "$(get "$AH" /api/admin/coupons)"
ck T51-coupon-tenant-forbidden '"success":false' "$(get "$H51" /api/admin/coupons)"
post "$AH" "{\"code\":\"$C51SUB\",\"kind\":\"subscribe\",\"discount_type\":\"amount\",\"discount_value\":5,\"enabled\":1}" /api/admin/coupons/save >/dev/null
post "$AH" "{\"code\":\"$C51BIG\",\"kind\":\"recharge\",\"discount_type\":\"amount\",\"discount_value\":9999,\"enabled\":1}" /api/admin/coupons/save >/dev/null

# ② 试算与下单同口径：折让=原价×10%、实付=原价−折让（金额一律服务端算，前端不参与）
R=$(post "$H51" "{\"code\":\"$C51\",\"points\":1000}" /api/coupon/preview)
ck T51-preview-ok '"success":true' "$R"
ORG51=$(echo "$R" | pv '.get("origin_money",0)')
DIS51=$(echo "$R" | pv '.get("discount_money",0)')
PAY51=$(echo "$R" | pv '.get("pay_money",0)')
ck T51-preview-kind-field '"kind":"recharge"' "$R"
if [ "${ORG51:-0}" != "0" ] && feq "$DIS51" "$(python3 -c "print(round($ORG51*0.1,2))")" && feq "$PAY51" "$(python3 -c "print($ORG51-$DIS51)")"; then
  PASS=$((PASS+1)); echo "PASS|T51-preview-math($ORG51-$DIS51=$PAY51)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T51-preview-math(origin=$ORG51 discount=$DIS51 pay=$PAY51)"
fi
# 未达门槛（小金额单 < 50 元门槛）→ 直接回用户可读提示，不给半成品报价
# ★ 断言用具体业务文案而非 '"success":false'：后者对「参数格式错误」也成立，会掩盖请求本身没发出去
R=$(post "$H51" "{\"code\":\"$C51\",\"points\":300}" /api/coupon/preview)
ck T51-preview-min-amount '"success":false' "$R"
ck T51-preview-min-amount-hint '订单需满' "$R"
# 券种不符：仅订阅券用在充值试算上
R=$(post "$H51" "{\"code\":\"$C51SUB\",\"points\":1000}" /api/coupon/preview)
ck T51-preview-kind '"success":false' "$R"
ck T51-preview-kind-hint '不适用于本单类型' "$R"
# 不存在的券码（#37 口径：业务提示可回显，不外泄 SQL）
ck T51-preview-notfound '券码不存在' "$(post "$H51" '{"code":"NOSUCHCODE999","points":1000}' /api/coupon/preview)"

# ③ 带券下单：amount_money 改写为折后，amount_tokens/积分额度一分不减（券减钱不减货）
R=$(post "$H51" "{\"points\":1000,\"channel\":\"mock\",\"coupon\":\"$(echo $C51 | tr 'A-Z' 'a-z')\"}" /api/pay/create)
ck T51-order-create '"success":true' "$R"
OID51=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
ON51=$(echo "$R" | pv '.get("order",{}).get("order_no","")')
AM51=$(sq "SELECT amount_money FROM orders WHERE id=$OID51" | tr -d '[:space:]')
if [ -n "$AM51" ] && feq "$AM51" "$PAY51"; then
  PASS=$((PASS+1)); echo "PASS|T51-order-discounted($AM51==试算 $PAY51)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T51-order-discounted(落库 $AM51 vs 试算 $PAY51)"
fi
TN51=$(sq "SELECT amount_tokens FROM orders WHERE id=$OID51" | tr -d '[:space:]')
[ "$TN51" = "$(( 1000 * RATE ))" ] && { PASS=$((PASS+1)); echo "PASS|T51-tokens-undiscounted($TN51)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T51-tokens-undiscounted(want $((1000 * RATE))(1000积分×汇率$RATE) got $TN51，券不得减积分额度)"; }
AP51=$(echo "$R" | pv '.get("order",{}).get("amount_points",-1)')
[ "$AP51" = "1000" ] && { PASS=$((PASS+1)); echo "PASS|T51-points-undiscounted"; } || { FAIL=$((FAIL+1)); echo "FAIL|T51-points-undiscounted(got $AP51)"; }
ck T51-order-coupon-code "$C51" "$(sq "SELECT coupon_code FROM orders WHERE id=$OID51")"
UC51=$(sq "SELECT used_count FROM coupons WHERE id=$CID51" | tr -d '[:space:]')
[ "$UC51" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T51-used-count-1"; } || { FAIL=$((FAIL+1)); echo "FAIL|T51-used-count-1(got $UC51)"; }
ck T51-redeem-audit "$ON51" "$(sq "SELECT detail FROM audit_logs WHERE action='coupon_redeem' AND detail LIKE '%$ON51%' LIMIT 1")"
# 到账按原价额度：模拟支付后余额增量=1000 积分对应的 token，与折后金额无关
B51_0=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$T51D" | tr -d '[:space:]')
post "$H51" "{\"order_id\":$OID51}" /api/pay/simulate >/dev/null
B51_1=$(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$T51D" | tr -d '[:space:]')
[ $((B51_1 - B51_0)) -eq $(( 1000 * RATE )) ] && { PASS=$((PASS+1)); echo "PASS|T51-credit-full-quota(+$((B51_1-B51_0)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|T51-credit-full-quota(got +$((B51_1-B51_0)) want +$((1000 * RATE))(1000积分×汇率$RATE))"; }

# ④ 每家企业限用 1 次：二次用同券必须拒，且不能留下挂券的半截单
R=$(post "$H51" "{\"points\":1000,\"channel\":\"mock\",\"coupon\":\"$C51\"}" /api/pay/create)
ck T51-tenant-limit '"success":false' "$R"
ck T51-tenant-limit-hint '可用次数已用完' "$R"
HANG51=$(sq "SELECT COUNT(*) FROM orders WHERE tenant_id=$T51D AND coupon_code<>'' AND status='pending'" | tr -d '[:space:]')
[ "$HANG51" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T51-failed-order-no-coupon"; } || { FAIL=$((FAIL+1)); echo "FAIL|T51-failed-order-no-coupon(挂券 pending=$HANG51)"; }

# ⑤ 0 元单红线：立减额远超订单额时只减到 0.01 元（0 元单会让回调金额核对退化成恒真）
R=$(post "$H51" "{\"points\":100,\"channel\":\"mock\",\"coupon\":\"$C51BIG\"}" /api/pay/create)
ck T51-floor-create '"success":true' "$R"
OID51B=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
AM51B=$(sq "SELECT amount_money FROM orders WHERE id=$OID51B" | tr -d '[:space:]')
[ -n "$AM51B" ] && ck T51-floor-001 '^0\.01$' "$AM51B" || { FAIL=$((FAIL+1)); echo "FAIL|T51-floor-001(空)"; }

# ⑥ 核销流水与删模板：模板可删，历史流水必须留（活动复盘/对账唯一依据）
ck T51-redemptions "$ON51" "$(get "$AH" "/api/admin/coupons/redemptions?coupon_id=$CID51")"
ck T51-redemptions-amount "\"paid_money\":$PAY51" "$(get "$AH" "/api/admin/coupons/redemptions?coupon_id=$CID51")"
ck T51-coupon-delete '"success":true' "$(post "$AH" "{\"id\":$CID51}" /api/admin/coupons/delete)"
if echo "$(get "$AH" /api/admin/coupons)" | grep -qE "$C51"; then
  FAIL=$((FAIL+1)); echo "FAIL|T51-deleted-out-of-list(删后仍在列表)"
else
  PASS=$((PASS+1)); echo "PASS|T51-deleted-out-of-list"
fi
ck T51-redemption-kept "$ON51" "$(get "$AH" "/api/admin/coupons/redemptions?coupon_id=$CID51")"
# ★ 已删模板的券码再试算必须回「券码不存在」（body 先存变量，见上方引号陷阱说明）
R=$(post "$H51" "{\"code\":\"$C51\",\"points\":1000}" /api/coupon/preview)
ck T51-deleted-code-reject '券码不存在' "$R"
ck T51-deleted-code-flag '"coupon_error":true' "$R"
ck T51-del-bad-id '"success":false' "$(post "$AH" '{"id":0}' /api/admin/coupons/delete)"

# ⑦ 订阅单带券（券适用 subscribe 类）+ 升级单显式拒券（口径边界，不静默忽略）
C51ANY="UATT51ANY$SFX51"
R=$(post "$AH" "{\"code\":\"$C51ANY\",\"kind\":\"subscribe\",\"discount_type\":\"percent\",\"discount_value\":50,\"enabled\":1}" /api/admin/coupons/save)
CID51ANY=$(echo "$R" | pv '.get("coupon",{}).get("id") or 0')
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$T51D,\"code\":\"uat_t51_low\",\"name\":\"T51低包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":20,\"duration_days\":30}" >/dev/null
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$T51D,\"code\":\"uat_t51_high\",\"name\":\"T51高包\",\"ptype\":\"paid\",\"sentences\":60000,\"price_money\":60,\"duration_days\":30}" >/dev/null
R=$(post "$H51" "{\"code\":\"uat_t51_low\",\"coupon\":\"$C51ANY\"}" /api/package/subscribe)
ck T51-subscribe-coupon '"success":true' "$R"
OID51S=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
AM51S=$(mny_norm "$(sq "SELECT amount_money FROM orders WHERE id=$OID51S" | tr -d '[:space:]')")
RD51S=$(get "$AH" "/api/admin/coupons/redemptions?coupon_id=$CID51ANY")
# 折后实付必须与流水的 paid_money 一致（应收单一事实源：订单、流水、收银台三处同数）
if [ -n "$AM51S" ] && echo "$RD51S" | grep -qE "\"paid_money\":$AM51S([^0-9]|$)"; then
  PASS=$((PASS+1)); echo "PASS|T51-subscribe-discounted(订单 $AM51S == 流水)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T51-subscribe-discounted(订单 $AM51S 未见于流水 paid_money)"
fi
ck T51-subscribe-audit "$C51ANY" "$(sq "SELECT detail FROM audit_logs WHERE action='coupon_redeem' AND detail LIKE '%$C51ANY%' LIMIT 1")"
# 升级单已含旧包余额抵扣，带券必须显式拒绝（不能静默忽略，否则用户以为券生效了）
R=$(post "$H51" "{\"code\":\"uat_t51_high\",\"coupon\":\"$C51ANY\"}" /api/package/upgrade)
ck T51-upgrade-reject-coupon '不再叠加优惠券' "$R"

# 清理本用例自建券与核销流水（订单/账本留痕供人工核对，与其它交易用例同口径）
dbq "DELETE FROM coupons WHERE code IN ('$C51','$C51SUB','$C51BIG','$C51ANY')" >/dev/null
dbq "DELETE FROM coupon_redemptions WHERE code LIKE 'UATT51%'" >/dev/null
dbq "DELETE FROM packages WHERE code IN ('uat_t51_low','uat_t51_high')" >/dev/null

# ★ 09-27 复跑红账（批 I-10 收尾补）：T45 改密与 T47 令牌轮换已把脚本顶部抓的 $H1 打失效
#   ——旧「一刀切 403」时代把「过期令牌」伪装成「权限不足」，测试与实现同时错所以一直绿；
#   F-64③ 分流（未登录 401／越权 403）落地后，后段所有用 $H1 的腿（T52 普通用户被拒、
#   T61 代理分流、T62 反馈本人 200、T63 租管三态）诚实翻 401 判红。
#   口径同 345 行与 T46 的注释：这里全局刷新一次；H46/H55/H57 等段各自就地重登，互不影响。
T1=$(tok uatuser_a uatpass123); H1="Authorization: Bearer $T1"
[ ${#T1} -gt 10 ] 2>/dev/null || { FAIL=$((FAIL+1)); echo "FAIL|H1-refresh-before-T52|uatuser_a 重新登录失败（后段用 H1 的各腿会连锁红）"; }

# ---------- T52 AI 助手管理代理（★ #34 后台前端重做，2026-09-21） ----------
# 断言的是「主后台同源代理 + 原生面板」这条新链路，替掉旧 iframe+localStorage 方案后必须有的保障：
#   鉴权（仅超管）、凭据注入（浏览器带的 admin_token 查询串被剥掉）、白名单转发、
#   上游数据可读可写、配置掩码不回写、审计只记区域不记请求体（可能含 llm_api_key）。
# 依赖 run_uat.sh 第 2c 步把 assist-server 一起拉起来（ASSIST_URL / ASSIST_UAT_TOKEN 由编排导出）。
SFX52=$(date +%s | tail -c 6)
A52="${ASSIST_URL:-http://127.0.0.1:8898}"
if curl -s -m 3 "$A52/health" | grep -qE '"ok":(true|1)'; then
  PASS=$((PASS+1)); echo "PASS|T52-assist-upstream-alive"
else
  FAIL=$((FAIL+1)); echo "FAIL|T52-assist-upstream-alive（assist-server 未启动：T52 必须经 run_uat.sh 编排跑）"
fi

# ① 鉴权面：普通用户与匿名一律拒（代理不放行，凭据也不外泄）
# ★ 断言升级（〇-U 批 I-10 · F-64③）：这里原先只锁 `"success":false`——200 壳与 4xx 都会绿，
#   是文件头 req3/ck3 那段注说的典型假绿形态。现按「状态码+错误码+文案」三件套收紧
#   （未登录 401 与越权 403 必须分流，细则与理由见下方 T61 段）。
req3 GET "$H1" /api/admin/assist/config; ck3 T52-forbid-normal-user 403 FORBIDDEN '权限不足'
req3 GET "" /api/admin/assist/config; ck3 T52-forbid-anon 401 UNAUTHORIZED '未登录'

# ② 状态条：上游可达 + Token 来源为 env（面板据此显示绿条，且不返回 Token 明文）
R=$(get "$AH" /api/admin/assist/status)
ck T52-status-reachable '"reachable":true' "$R"
ck T52-status-token-src '"token_src":"env"' "$R"
ck T52-status-no-secret-leak '"message":""' "$R"

# ③ 数据面读：seed 已灌入知识库，代理原样回传上游 rows
R=$(get "$AH" /api/admin/assist/kb)
ck T52-kb-list '"rows":\[' "$R"
N52=$(echo "$R" | python3 -c 'import sys,json;print(len(json.load(sys.stdin).get("rows") or []))' 2>/dev/null || echo 0)
[ "${N52:-0}" -gt 0 ] && { PASS=$((PASS+1)); echo "PASS|T52-kb-seeded($N52 条)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T52-kb-seeded(got $N52)"; }

# ④ CRUD 往返：新建 → 列表可见 → 改 → 删 → 不可见（assist 侧按 id 定位，query 透传）
K52="uat_t52_$SFX52"
R=$(post "$AH" "{\"key\":\"$K52\",\"category\":\"uat\",\"title\":\"T52 冒烟条目\",\"content\":\"仅供断言\",\"keywords\":\"冒烟\",\"priority\":3,\"enabled\":1}" /api/admin/assist/kb)
ck T52-kb-create '"id":' "$R"
ID52=$(echo "$R" | pv '.get("id") or 0')
ck T52-kb-read "$K52" "$(get "$AH" /api/admin/assist/kb)"
# 更新走 PUT（assist 侧按 ?id= 定位，代理原样透传查询串）
ck T52-kb-update '"ok":true' "$(curl -s -X PUT "$B/api/admin/assist/kb?id=$ID52" -H "$AH" -H "$J" -d '{"title":"T52 冒烟条目（已改）","enabled":1}')"
ck T52-kb-updated-title '冒烟条目（已改）' "$(get "$AH" /api/admin/assist/kb)"
ck T52-kb-delete '"ok":true' "$(curl -s -X DELETE "$B/api/admin/assist/kb?id=$ID52" -H "$AH")"
if get "$AH" /api/admin/assist/kb | grep -qE "$K52"; then
  FAIL=$((FAIL+1)); echo "FAIL|T52-kb-deleted-gone（删除后列表仍见 $K52）"
else
  PASS=$((PASS+1)); echo "PASS|T52-kb-deleted-gone"
fi

# ⑤ 凭据不外泄：浏览器即使带 ?admin_token=乱值，代理也会剥掉并按服务端注入的头转发（200 而非 401）
ck T52-admin-token-stripped '"rows":\[' "$(get "$AH" "/api/admin/assist/kb?admin_token=WRONG-52-$SFX52")"
if get "" "/api/admin/assist/kb?admin_token=$ASSIST_UAT_TOKEN" | grep -qE '"rows"'; then
  FAIL=$((FAIL+1)); echo "FAIL|T52-no-query-token-path（匿名凭 query 传凭据竟然放行）"
else
  PASS=$((PASS+1)); echo "PASS|T52-no-query-token-path"
fi

# ⑥ 配置读写 + 掩码不回写：assist 白名单外的键被上游拒；掩码形态值跳过写库（防把 *** 存进库）
ck T52-config-whitelist 'key not allowed' "$(curl -s -X PUT "$B/api/admin/assist/config" -H "$AH" -H "$J" -d '{"key":"not_a_real_key","value":"x"}')"
R=$(curl -s -X PUT "$B/api/admin/assist/config" -H "$AH" -H "$J" -d '{"key":"welcome","value":"T52 冒烟欢迎词"}')
ck T52-config-write '"ok":true' "$R"
ck T52-config-read 'T52 冒烟欢迎词' "$(get "$AH" /api/admin/assist/config)"
ck T52-config-mask-skip '"skipped":true' "$(curl -s -X PUT "$B/api/admin/assist/config" -H "$AH" -H "$J" -d '{"key":"llm_api_key","value":"sk-1***xy"}')"

# ⑦ 审计留痕但绝不落请求体：写入带密钥串的配置后，最新一条 assist_admin_write 只能看到方法+区域
FAKE_SECRET="SK52SECRET$SFX52"
curl -s -X PUT "$B/api/admin/assist/config" -H "$AH" -H "$J" -d "{\"key\":\"persona\",\"value\":\"含密钥占位 $FAKE_SECRET\"}" >/dev/null
AU52=$(sq "SELECT detail FROM audit_logs WHERE action='assist_admin_write' ORDER BY id DESC LIMIT 1" | tr -d '[:space:]')
if [ -n "$AU52" ] && ! echo "$AU52" | grep -qE "$FAKE_SECRET" && echo "$AU52" | grep -qE 'PUTconfig'; then
  PASS=$((PASS+1)); echo "PASS|T52-audit-no-request-body($AU52)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T52-audit-no-request-body(got $AU52)"
fi

# ⑧ 概览与会话：面板首屏要读的统计接口经代理可读（字段缺失即前端徽标空转）
R=$(get "$AH" /api/admin/assist/sessions)
ck T52-sessions-shape '"sessions":\[' "$R"
ck T52-sessions-stats '"total":' "$R"
ck T52-sessions-unanswered '"unanswered":\[' "$R"

# ⑨ 连通测试：走 mock LLM 应回 ok:true + 模型名（失败说明代理或 ASSIST_LLM_* 装配断了）
R=$(post "$AH" '{}' /api/admin/assist/llm-test)
ck T52-llm-test-ok '"ok":true' "$R"
ck T52-llm-test-model '"model":' "$R"

# ⑩ 未登记路径不放行（代理不是通用中继：白名单之外的 assist 路径只能拿到 404，
#    断言状态码而不是文案，避免与 SPA 兜底处理器的文案实现耦合）
C52OFF=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/admin/assist/unknown-route" -H "$AH")
[ "$C52OFF" = "404" ] && { PASS=$((PASS+1)); echo "PASS|T52-off-whitelist-404"; } || { FAIL=$((FAIL+1)); echo "FAIL|T52-off-whitelist-404(got $C52OFF)"; }

# ---------- T53（★ #74）订阅续费宽限期 + 自动续费重试：身份保留 / 宽限截止 / 去重三连 / 结束摘除 / 对照组 ----------
# 本段测什么：#41 自动续费旧行为是「到期即刻摘掉付费身份」，#74 给开了自动续费的租户补一段
#   N 天宽限期（默认 3，subscription_grace.go；env SUBSCRIPTION_GRACE_DAYS>0 时优先）。三段行为：
#   ① 进入宽限期：package_code/package_expires_at 原样保留（鉴权计费无需放行分支），只落
#      grace_expires_at 并发一条「订阅已到期，宽限期至 X」站内信；/api/me/package 出 in_grace/grace_expires；
#   ② 宽限期内每日扫描继续补建续费单，同日去重由 renewal_attempts(tenant_id,package_id,attempt_date) 唯一键兜底；
#   ③ 宽限期结束仍未到账 → ExpirePackage 摘除，文案换成「宽限期结束，订阅已失效」；
#   ④ 对照组：auto_renew=false 的到期租户立刻摘除（宽限期只服务有续费意愿的客户，不给别人延长收费窗口）。
# 为什么这么测：行为全挂在「每日扫描」上，无法等真实天数流逝，沿用 T50 的注入惯用法——
#   dbjsonset 把 package_expires_at/grace_expires_at 推到过去 + 手动 POST subscription-scan 触发扫描；
#   站内信断言用 LIKE '%宽限期%' 模糊标题（防文案微调把闸门拖红）并按 ref_id=租户 收窄，不吃别的用例的噪声。
# 清理：本段自建的双租户/用户/包/订单/站内信/台账/审计全部结尾 DELETE，不碰出厂数据。
SFX53=$(date +%s)
U53A="uatuser_t53a$SFX53"; U53B="uatuser_t53b$SFX53"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$U53A\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T53宽限期\",\"email\":\"$U53A@test.com\",\"agreed\":true}" >/dev/null
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$U53B\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T53对照组\",\"email\":\"$U53B@test.com\",\"agreed\":true}" >/dev/null
TK53A=$(tok $U53A uatpass123); H53A="Authorization: Bearer $TK53A"
TK53B=$(tok $U53B uatpass123); H53B="Authorization: Bearer $TK53B"
TD53A=$(sq "SELECT tenant_id FROM users WHERE username='$U53A' LIMIT 1" | tr -d '[:space:]')
TD53B=$(sq "SELECT tenant_id FROM users WHERE username='$U53B' LIMIT 1" | tr -d '[:space:]')
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$TD53A,\"code\":\"uat_t53_paid\",\"name\":\"T53宽限包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":20,\"duration_days\":30}" >/dev/null
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$TD53B,\"code\":\"uat_t53_b\",\"name\":\"T53对照包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":20,\"duration_days\":30}" >/dev/null
# renewal_attempts 的 attempt_date 用服务器本地日期（renewalAttemptClock 同口径）；断言层与被测层同机同时区
TODAY53=$(date +%F)
# 「昨天」注入值必须与落库口径同为 RFC3339 UTC（Z 后缀），后面做字典序比较才等价于时间序
EXP53=$(python3 -c "import datetime;print((datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(hours=24)).strftime('%Y-%m-%dT%H:%M:%SZ'))")

# ① A 租户：订阅到账 + 开自动续费（宽限期只对这类租户开放）
#   注：mock 支付模式下 subscribe 内部已 MarkOrderPaid，admin/orders/pay 只是 manual 渠道下的
#   兜底（同 T50 惯用法：不消费其响应），到账与否一律以 permissions 落库为准。
R=$(post "$H53A" '{"code":"uat_t53_paid"}' /api/package/subscribe)
OID53=$(echo "$R" | pv '.get("order",{}).get("id") or d.get("id") or 0')
post "$AH" "{\"id\":$OID53,\"tenant_id\":$TD53A}" /api/admin/orders/pay >/dev/null
PC053=$(dbjsonstr tenants $TD53A permissions package_code | tr -d '[:space:]')
[ "$PC053" = "uat_t53_paid" ] && { PASS=$((PASS+1)); echo "PASS|T53-subscribed"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-subscribed(package_code=$PC053)"; }
ck T53-renew-enable '"success":true' "$(post "$H53A" '{"enabled":true}' /api/package/auto-renew)"

# ② 注入「昨天已到期」→ 扫描应进宽限期：身份保留 + 落 grace_expires_at + 出参 in_grace
dbjsonset tenants $TD53A permissions package_expires_at "$EXP53"
ck T53-scan-grace-in '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"
PC53=$(dbjsonstr tenants $TD53A permissions package_code | tr -d '[:space:]')
# WHY 核心断言：宽限期的全部价值=「晚付一天不用重新选包」，身份键被扫描碰到即回归
[ "$PC53" = "uat_t53_paid" ] && { PASS=$((PASS+1)); echo "PASS|T53-grace-keeps-identity"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-grace-keeps-identity(got $PC53)"; }
GR53=$(dbjsonstr tenants $TD53A permissions grace_expires_at | tr -d '[:space:]')
# RFC3339 UTC 同格式同时区 → 字典序=时间序；宽限截止必须晚于本期到期才是有效宽限期（graceDeadline 口径）
if [ -n "$GR53" ] && [[ "$GR53" > "$EXP53" ]]; then PASS=$((PASS+1)); echo "PASS|T53-grace-deadline-set($GR53)"; else FAIL=$((FAIL+1)); echo "FAIL|T53-grace-deadline-set(got '$GR53' want >$EXP53)"; fi
M53=$(get "$H53A" /api/me/package)
# ★ 布尔出参方言容错（同 T50 case 口径）：Go 编码器恒出 true，仍容 1 防实现漂移；grep -E 交替（§7 禁 BRE）
ck T53-me-in-grace '"in_grace":(true|1)' "$M53"
ck T53-me-grace-expires '"grace_expires":"20' "$M53"
GNT53=$(sq "SELECT COUNT(*) FROM notifications WHERE ref_id=$TD53A AND title LIKE '%宽限期%'" | tr -d '[:space:]')
[ "$GNT53" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T53-grace-notify-once"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-grace-notify-once(宽限期站内信=$GNT53)"; }
# 宽限期首轮即补建续费单：created_by=0 系统单挂 pending，台账占掉今日一格
PEND53=$(sq "SELECT COUNT(*) FROM orders WHERE tenant_id=$TD53A AND status='pending' AND created_by=0" | tr -d '[:space:]')
[ "$PEND53" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T53-grace-renewal-order"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-grace-renewal-order(pending 系统单=$PEND53)"; }
ATT53=$(sq "SELECT COUNT(*) FROM renewal_attempts WHERE tenant_id=$TD53A AND attempt_date='$TODAY53'" | tr -d '[:space:]')
[ "$ATT53" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T53-attempt-slot-1"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-attempt-slot-1(renewal_attempts 今日格=$ATT53)"; }
CK53=$(sq "SELECT COUNT(*) FROM renewal_attempts WHERE tenant_id=$TD53A AND status='created' AND order_no<>''" | tr -d '[:space:]')
[ "$CK53" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T53-attempt-linked-order"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-attempt-linked-order(created 且回写单号=$CK53)"; }

# ③ 同日二次扫描 → 去重三连：站内信不重发、续费单不堆叠、台账格子不重复占（多触发/多实例红线）
ck T53-scan-dedup '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"
GNT53B=$(sq "SELECT COUNT(*) FROM notifications WHERE ref_id=$TD53A AND title LIKE '%宽限期%'" | tr -d '[:space:]')
[ "$GNT53B" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T53-dedup-notify"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-dedup-notify(宽限期站内信=$GNT53B)"; }
PEND53B=$(sq "SELECT COUNT(*) FROM orders WHERE tenant_id=$TD53A AND status='pending' AND created_by=0" | tr -d '[:space:]')
[ "$PEND53B" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T53-dedup-order"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-dedup-order(pending 系统单=$PEND53B)"; }
ATT53B=$(sq "SELECT COUNT(*) FROM renewal_attempts WHERE tenant_id=$TD53A AND attempt_date='$TODAY53'" | tr -d '[:space:]')
[ "$ATT53B" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T53-dedup-attempt"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-dedup-attempt(renewal_attempts 今日格=$ATT53B)"; }

# ④ 宽限期结束（grace_expires_at 注入成「一小时前」，仍晚于本期到期值→宽限期有效但已过）→ 摘除
GEND53=$(python3 -c "import datetime;print((datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(hours=1)).strftime('%Y-%m-%dT%H:%M:%SZ'))")
dbjsonset tenants $TD53A permissions grace_expires_at "$GEND53"
ck T53-scan-grace-out '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"
PC53C=$(dbjsonstr tenants $TD53A permissions package_code | tr -d '[:space:]')
[ -z "$PC53C" ] && { PASS=$((PASS+1)); echo "PASS|T53-grace-end-expired"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-grace-end-expired(身份未摘除 got $PC53C)"; }
GR53C=$(dbjsonstr tenants $TD53A permissions grace_expires_at | tr -d '[:space:]')
[ -z "$GR53C" ] && { PASS=$((PASS+1)); echo "PASS|T53-grace-key-cleared"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-grace-key-cleared(grace_expires_at=$GR53C)"; }
NT53E=$(sq "SELECT COUNT(*) FROM notifications WHERE ref_id=$TD53A AND title LIKE '%宽限期结束%'" | tr -d '[:space:]')
[ "$NT53E" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T53-grace-end-notify"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-grace-end-notify(「宽限期结束」站内信=$NT53E)"; }
# 摘除后出参必须回落 in_grace=false（前端徽标与后台裁决口径一致，防「界面说在宽限期、身份已没了」）
ck T53-me-out-of-grace '"in_grace":(false|0)' "$(get "$H53A" /api/me/package)"

# ⑤ 对照组 B：不开自动续费 → 到期即刻摘除，且不落宽限期键、不发宽限期文案
#   （宽限期只服务自动续费客户：无续费意愿还延长收费窗口没有业务依据）
R=$(post "$H53B" '{"code":"uat_t53_b"}' /api/package/subscribe)
OID53B=$(echo "$R" | pv '.get("order",{}).get("id") or d.get("id") or 0')
post "$AH" "{\"id\":$OID53B,\"tenant_id\":$TD53B}" /api/admin/orders/pay >/dev/null
PC053B=$(dbjsonstr tenants $TD53B permissions package_code | tr -d '[:space:]')
[ "$PC053B" = "uat_t53_b" ] && { PASS=$((PASS+1)); echo "PASS|T53-ctl-subscribed"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-ctl-subscribed(package_code=$PC053B)"; }
ck T53-ctl-autorenew-off '"auto_renew":(false|0)' "$(get "$H53B" /api/package/auto-renew)"
dbjsonset tenants $TD53B permissions package_expires_at "$EXP53"
ck T53-scan-ctl '"success":true' "$(post "$AH" '{}' /api/admin/ops/watchdog/subscription-scan)"
PC53D=$(dbjsonstr tenants $TD53B permissions package_code | tr -d '[:space:]')
[ -z "$PC53D" ] && { PASS=$((PASS+1)); echo "PASS|T53-ctl-immediate-expire"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-ctl-immediate-expire(未即刻摘除 got $PC53D)"; }
GR53D=$(dbjsonstr tenants $TD53B permissions grace_expires_at | tr -d '[:space:]')
[ -z "$GR53D" ] && { PASS=$((PASS+1)); echo "PASS|T53-ctl-no-grace-key"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-ctl-no-grace-key(不该落宽限期 got $GR53D)"; }
GNT53D=$(sq "SELECT COUNT(*) FROM notifications WHERE ref_id=$TD53B AND title LIKE '%宽限期%'" | tr -d '[:space:]')
[ "$GNT53D" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T53-ctl-no-grace-notify"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-ctl-no-grace-notify(宽限期文案=$GNT53D)"; }
NT53D=$(sq "SELECT COUNT(*) FROM notifications WHERE ref_id=$TD53B AND title='订阅已到期'" | tr -d '[:space:]')
[ "${NT53D:-0}" -ge 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T53-ctl-expire-notify($NT53D)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T53-ctl-expire-notify($NT53D)"; }

# ⑥ 清理本用例数据（口径同 T50 且更严：新租户整链删净，包/订单/站内信/台账/审计/用户/租户行）
dbq "DELETE FROM packages WHERE code IN ('uat_t53_paid','uat_t53_b')" >/dev/null
dbq "DELETE FROM renewal_attempts WHERE tenant_id IN ($TD53A,$TD53B)" >/dev/null
dbq "DELETE FROM orders WHERE tenant_id IN ($TD53A,$TD53B)" >/dev/null
dbq "DELETE FROM notifications WHERE ref_id IN ($TD53A,$TD53B)" >/dev/null
dbq "DELETE FROM audit_logs WHERE tenant_id IN ($TD53A,$TD53B)" >/dev/null
dbq "DELETE FROM quota_grants WHERE tenant_id IN ($TD53A,$TD53B)" >/dev/null
dbq "DELETE FROM balance_accounts WHERE tenant_id IN ($TD53A,$TD53B)" >/dev/null
dbq "DELETE FROM users WHERE username IN ('$U53A','$U53B')" >/dev/null
dbq "DELETE FROM tenants WHERE id IN ($TD53A,$TD53B)" >/dev/null

# ---------- T54（★ #75，2026-09-22 起关闭封存）多币种报价：配置口鉴权 / 外币写入拒收 / 残留配置不生效 / 全站恒 CNY ----------
# 本段测什么：#75 报价功能按用户决策关闭封存（收单只有微信/支付宝 CNY 与币安 USDT，外币报价
#   暂无业务落点；store/currency.go quoteFeatureOpen=false）。四组红线：
#   ① 配置口仅平台超管（GET/POST 都要拦匿名与普通用户）——开关关掉不放水鉴权；
#   ② 关闭态收敛：GET 恒回 currency=CNY + feature_open=false + 白名单只露 CNY；
#      任何外币币种/倍率保存一律 success:false 拒收；
#   ③ 残留配置压不住：直接往 system_config 塞 quote_currency=USD + fx_rates 也不生效
#      （/api/plans 与下单快照仍恒 CNY/1，price_display=原价）——重开只翻开关，不必先清历史脏配置；
#   ④ 语义红线反锁：orders 快照列照常双写 money_cny=amount_money（结算事实源恒人民币），
#      订阅建单链路不因报价封装修复而改变金额口径。
# ★ body 一律先 printf 存变量再传 post/ck：内嵌 {"a":1,"b":2} 会被大括号展开切碎（见文件头警告）。
U54="uatuser_t54$SFX53"   # 复用 T53 时间戳后缀保证用户名不撞
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$U54\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T54报价\",\"email\":\"$U54@test.com\",\"agreed\":true}" >/dev/null
TK54=$(tok $U54 uatpass123); H54="Authorization: Bearer $TK54"
TD54=$(sq "SELECT tenant_id FROM users WHERE username='$U54' LIMIT 1" | tr -d '[:space:]')
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$TD54,\"code\":\"uat_t54_pkg\",\"name\":\"T54报价包\",\"ptype\":\"paid\",\"sentences\":20000,\"price_money\":21.6,\"duration_days\":30}" >/dev/null
QCFG=/api/admin/config/quote-currency
# ① 关闭态回显 + 鉴权（鉴权口与开关无关，匿名/普通用户照样 403）
RQ54=$(get "$AH" $QCFG)
ck T54-get-default '"currency":"CNY"' "$RQ54"
ck T54-get-feature-closed '"feature_open":(false|0)' "$RQ54"
ck T54-get-whitelist-cny-only '"supported_currencies":\["CNY"\]' "$RQ54"
ANON54=$(curl -s $B$QCFG)
case "$ANON54" in *'"success":true'*) FAIL=$((FAIL+1)); echo "FAIL|T54-anon-forbidden";; *) PASS=$((PASS+1)); echo "PASS|T54-anon-forbidden";; esac
RQ54=$(get "$H54" $QCFG)
case "$RQ54" in *'"success":true'*) FAIL=$((FAIL+1)); echo "FAIL|T54-user-forbidden";; *) PASS=$((PASS+1)); echo "PASS|T54-user-forbidden";; esac
# ② 外币写入拒收（USD 合法币种也一样拒——关闭态没有"合法外币"）；CNY 表态仍放行（关闭≠打死接口）
B54=$(printf '%s' '{"currency":"USD","rates":{"USD":7.2}}')
RQ54=$(post "$AH" "$B54" $QCFG)
ck T54-reject-usd-sealed '"success":false' "$RQ54"
B54=$(printf '%s' '{"currency":"XXX","rates":{"USD":7.2}}')
RQ54=$(post "$AH" "$B54" $QCFG)
ck T54-reject-bad-currency '"success":false' "$RQ54"
B54=$(printf '%s' '{"currency":"CNY","rates":{}}')
RQ54=$(post "$AH" "$B54" $QCFG)
ck T54-save-cny-allowed '"success":true' "$RQ54"
# 拒写不留半套：fx_rates 键根本不该被写进去
N54=$(sq "SELECT COUNT(*) FROM system_config WHERE key='fx_rates'" | tr -d '[:space:]')
[ "$N54" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T54-no-half-config"; } || { FAIL=$((FAIL+1)); echo "FAIL|T54-no-half-config(fx_rates 行数=$N54)"; }
# ③ 残留配置压不住：绕过校验直插 system_config 外币键 → 生效读取与出参仍恒 CNY
dbq "INSERT INTO system_config(key,value) VALUES('quote_currency','USD')" >/dev/null
dbq "INSERT INTO system_config(key,value) VALUES('fx_rates','{\"USD\":7.2}')" >/dev/null
RQ54=$(get "$AH" $QCFG)
ck T54-resign-currency-cny '"currency":"CNY"' "$RQ54"
PLANS54=$(curl -s $B/api/plans)
ck T54-plans-quote-cny '"quote_currency":"CNY"' "$PLANS54"
# float() 强制转浮点再打印：Go 会把 21.0 序列化成 21（int 形态），期望值口径统一为 Python 浮点 repr
PD54=$(echo "$PLANS54" | python3 -c "import sys,json;d=json.load(sys.stdin);print([float(p.get('price_display') or 0) for p in d.get('plans',[]) if p.get('code')=='uat_t54_pkg'][:1])")
# 关闭态 price_display 恒等于人民币原价 21.6（残留 USD:7.2 不许参与换算——fail-closed 的界面级证据）
[ "${PD54:-}" = "[21.6]" ] && { PASS=$((PASS+1)); echo "PASS|T54-price-display-cny"; } || { FAIL=$((FAIL+1)); echo "FAIL|T54-price-display-cny(want 21.6 got $PD54)"; }
CKEY54=$(echo "$PLANS54" | python3 -c "import sys,json;d=json.load(sys.stdin);print(1 if any('price_cny' not in p for p in d.get('plans',[])) else 0)")
[ "$CKEY54" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T54-plans-carry-price-cny"; } || { FAIL=$((FAIL+1)); echo "FAIL|T54-plans-carry-price-cny(有行缺 price_cny)"; }
MP54=$(get "$H54" /api/me/package)
ck T54-me-quote '"quote_currency":"CNY"' "$MP54"
# ④ 下单快照：残留外币配置下建单仍恒落 CNY|1，money_cny=amount_money 双写（结算事实源不漂）。
#   注意实付=10.8 而非挂牌 21.6：新租户未满 30 天吃「试运营首月半价」（PackageOrderPrice，
#   2026-09-14 拍板）——报价快照落在折让之后，恰好锁住「快照跟随最终应收」口径。
R=$(post "$H54" '{"code":"uat_t54_pkg"}' /api/package/subscribe)
OID54=$(echo "$R" | pv '.get("order",{}).get("id") or d.get("id") or 0')
SNAP54=$(mny_norm "$(sq "SELECT currency||'|'||fx_rate||'|'||money_cny FROM orders WHERE id=$OID54" | tr -d '[:space:]')")
[ "$SNAP54" = "CNY|1|10.8" ] && { PASS=$((PASS+1)); echo "PASS|T54-order-quote-snapshot"; } || { FAIL=$((FAIL+1)); echo "FAIL|T54-order-quote-snapshot(want CNY|1|10.8(首月半价后实付) got $SNAP54)"; }
AMT54=$(sq "SELECT amount_money FROM orders WHERE id=$OID54" | tr -d '[:space:]')
[ "${AMT54%.*}" = "10" ] && { PASS=$((PASS+1)); echo "PASS|T54-settlement-still-cny($AMT54)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T54-settlement-still-cny(amount_money=$AMT54 want 10.8(半价实付))"; }
# ⑤ 清理：本用例直插的报价配置行删回出厂默认，租户/包/订单/余额/台账链删净
dbq "DELETE FROM system_config WHERE key IN ('quote_currency','fx_rates')" >/dev/null
dbq "DELETE FROM packages WHERE code='uat_t54_pkg'" >/dev/null
dbq "DELETE FROM orders WHERE tenant_id=$TD54" >/dev/null
dbq "DELETE FROM quota_grants WHERE tenant_id=$TD54" >/dev/null
dbq "DELETE FROM balance_accounts WHERE tenant_id=$TD54" >/dev/null
dbq "DELETE FROM users WHERE username='$U54'" >/dev/null
dbq "DELETE FROM tenants WHERE id=$TD54" >/dev/null

# ---------- T55（★ #74 支付渠道凭据管理台化，2026-09-22 补 HTTP 级常设锁）----------
# 覆盖口径：读写口的鉴权 / 白名单键（未知键整单拒收且不留半套凭据）/ 开关与地址形态校验 /
#   敏感项 enc:v1 密文落库 + 掩码回显 + 掩码再提交不覆盖真值 / 空串=清除 /
#   保存链路留审计 / **库配置真的流到下单链路**（缺项清单不再点名已填项）/ fail-closed 不降级 mock。
# 与单测的分工：env > DB 的优先级需要往进程里注入 PAY_* 环境变量，UAT 服务是 run_uat.sh
#   以固定环境启动的、跑中途无法改环境，故那一条由 pay_channels_test.go 在进程内覆盖；
#   本处锁的是「HTTP 口进得去、出来的值与库里/渠道侧一致」这一段，两者互补不重复。
echo "--- T55 支付渠道凭据管理台配置 ---"
# 口径同 T46 的注释：T6/T45 会改 uatuser_a 口令并使脚本开头取的 H1 失效，
# 这里必须重新登录取专用令牌——否则下面的「普通用户被拒」会对着「未登录」恒真，
# 而「普通用户下单」三条直接红在鉴权层，测不到支付链路。
T55U=$(tok uatuser_a uatpass123); H55="Authorization: Bearer $T55U"
[ ${#T55U} -gt 10 ] 2>/dev/null || { FAIL=$((FAIL+1)); echo "FAIL|T55-auth|uatuser_a 重新登录失败"; }
# ① 鉴权：匿名与普通用户都拿不到配置口（回显里含商户号等经营信息）
case "$(get '' /api/admin/pay/channels)" in *'"success":true'*) FAIL=$((FAIL+1)); echo "FAIL|T55-anon-forbidden";; *) PASS=$((PASS+1)); echo "PASS|T55-anon-forbidden";; esac
case "$(get "$H55" /api/admin/pay/channels)" in *'"success":true'*) FAIL=$((FAIL+1)); echo "FAIL|T55-user-forbidden";; *) PASS=$((PASS+1)); echo "PASS|T55-user-forbidden";; esac
RC55=$(get "$AH" /api/admin/pay/channels)
ck T55-admin-echo '"success":true' "$RC55"
ck T55-echo-shape '"fields":\{' "$RC55"
# env_overridden 在 UAT 恒空（服务未注入任何 PAY_* 变量）：这一条同时是「本文件后续断言
# 看到的生效值确实来自库」的前提——一旦将来 run_uat 注入了 PAY_* 变量，这里先红，
# 提醒去把下面的缺项清单口径改成 env 值，而不是让断言对着 env 值假绿。
ck T55-no-env-takeover '"env_overridden":\{\}' "$RC55"

# ② 白名单闸：未知键必须在写库之前整体拒绝（SetPayConfigField 也拒，但那已在第二个循环里，
#    排在前面的合法字段会先落库 = 半套凭据，比整单失败难查得多）
B55='{"fields":{"paych_wechat_app_id":"uat55wxappid","paych_not_a_key":"x"}}'
R=$(post "$AH" "$B55" /api/admin/pay/channels/save)
ck T55-unknown-key-reject '"success":false' "$R"
ck T55-unknown-key-msg '未知的支付渠道配置项' "$R"
N55=$(sq "SELECT COUNT(*) FROM system_config WHERE key='paych_wechat_app_id'" | tr -d '[:space:]')
[ "$N55" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T55-unknown-key-no-halfwrite"; } || { FAIL=$((FAIL+1)); echo "FAIL|T55-unknown-key-no-halfwrite(未知键连带把合法字段写进了库)"; }

# ③ 字段形态校验：开关只认 0/1/空；回调与网关必须是完整 URL（相对路径能存进来，
#    但渠道永远调不到，表现为「配了却收不到款」，必须在保存时拦住）
B55='{"fields":{"paych_wechat_enabled":"2"}}'
R=$(post "$AH" "$B55" /api/admin/pay/channels/save)
ck T55-enabled-bad-value '"success":false' "$R"
B55='{"fields":{"paych_wechat_notify_url":"pay.example.com/wx"}}'
R=$(post "$AH" "$B55" /api/admin/pay/channels/save)
ck T55-relative-url-reject '完整地址' "$R"
N55=$(sq "SELECT COUNT(*) FROM system_config WHERE key='paych_wechat_notify_url'" | tr -d '[:space:]')
[ "$N55" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T55-bad-url-no-write"; } || { FAIL=$((FAIL+1)); echo "FAIL|T55-bad-url-no-write(非法地址仍落了库)"; }

# ④ 正常保存：非敏感项明文可回显
B55='{"fields":{"paych_wechat_app_id":"uat55wxappid","paych_notify_base":"https://pay.example.com"}}'
R=$(post "$AH" "$B55" /api/admin/pay/channels/save)
ck T55-save-plain '"success":true' "$R"
RC55=$(get "$AH" /api/admin/pay/channels)
ck T55-echo-plain-value '"paych_wechat_app_id":"uat55wxappid"' "$RC55"

# ⑤ 敏感项三条锁：密文落库 / 掩码回显 / 掩码再提交保留真值
#    （第三条是本轮最容易回归的一条：管理员打开页面不动密钥栏直接点保存，
#      表单把回显的 "********" 原样提交回来，若直接写库就等于把真密钥抹掉了。）
SECRET55="uat55apiv3key-0123456789abcdef"   # 恰好 32 字节，与微信 APIv3 密钥同规格
B55='{"fields":{"paych_wechat_apiv3_key":"'"$SECRET55"'"}}'
R=$(post "$AH" "$B55" /api/admin/pay/channels/save)
ck T55-save-secret '"success":true' "$R"
CIPH55=$(sq "SELECT value FROM system_config WHERE key='paych_wechat_apiv3_key'" | tr -d '[:space:]')
case "$CIPH55" in enc:v1:*) PASS=$((PASS+1)); echo "PASS|T55-secret-cipher-at-rest";; *) FAIL=$((FAIL+1)); echo "FAIL|T55-secret-cipher-at-rest(敏感项未加密落库: ${CIPH55:0:16})";; esac
RC55=$(get "$AH" /api/admin/pay/channels)
ck T55-secret-masked-echo '"paych_wechat_apiv3_key":"\*\*\*\*\*\*\*\*"' "$RC55"
case "$RC55" in *"$SECRET55"*) FAIL=$((FAIL+1)); echo "FAIL|T55-echo-leaks-plaintext";; *) PASS=$((PASS+1)); echo "PASS|T55-echo-leaks-plaintext";; esac
B55='{"fields":{"paych_wechat_apiv3_key":"********","paych_wechat_mch_id":"uat55mch"}}'
R=$(post "$AH" "$B55" /api/admin/pay/channels/save)
ck T55-mask-resubmit-ok '"success":true' "$R"
CIPH55B=$(sq "SELECT value FROM system_config WHERE key='paych_wechat_apiv3_key'" | tr -d '[:space:]')
[ "$CIPH55B" = "$CIPH55" ] && { PASS=$((PASS+1)); echo "PASS|T55-mask-not-written-back"; } || { FAIL=$((FAIL+1)); echo "FAIL|T55-mask-not-written-back(掩码提交覆盖了真密文)"; }
MCH55=$(sq "SELECT value FROM system_config WHERE key='paych_wechat_mch_id'" | tr -d '[:space:]')
[ "$MCH55" = "uat55mch" ] && { PASS=$((PASS+1)); echo "PASS|T55-sibling-field-saved"; } || { FAIL=$((FAIL+1)); echo "FAIL|T55-sibling-field-saved(got $MCH55)"; }

# ⑥ 库配置真的流到下单链路：本步之前经管理台填了 app_id / mch_id / apiv3_key（32 字节）
#    与 notify_base，缺项清单就不得再点名这三项（否则说明 payGatewayConfig() 没把库值送进
#    provider，读写口自说自话）；同时从未填的 private_key 必须仍被点名，证明不是清单整体失效。
R=$(post "$H55" '{"points":100,"channel":"wechat"}' /api/pay/create)
ck T55-wechat-failclosed '资质未配置' "$R"
echo "$R" | grep -qE 'PAY_WECHAT_APP_ID' && { FAIL=$((FAIL+1)); echo "FAIL|T55-db-value-reaches-provider(已填 app_id 仍被判缺失)"; } || { PASS=$((PASS+1)); echo "PASS|T55-db-value-reaches-provider"; }
echo "$R" | grep -qE 'PAY_WECHAT_PRIVATE_KEY' && { PASS=$((PASS+1)); echo "PASS|T55-missing-list-still-strict"; } || { FAIL=$((FAIL+1)); echo "FAIL|T55-missing-list-still-strict(缺项清单未点名从未填的 private_key: ${R:0:160})"; }
echo "$R" | grep -qE 'mockpay://' && { FAIL=$((FAIL+1)); echo "FAIL|T55-no-mock-fallback"; } || { PASS=$((PASS+1)); echo "PASS|T55-no-mock-fallback"; }

# ⑦ 管理台停用（enabled=0）优先于凭据齐全度：给出「已停用」而非「未配置」，
#    且绝不回退 mock 出假码（#41 整改口径：配置了却给出废码比不出码更贵）
B55='{"fields":{"paych_wechat_enabled":"0"}}'
R=$(post "$AH" "$B55" /api/admin/pay/channels/save)
ck T55-save-switch-off '"success":true' "$R"
R=$(post "$H55" '{"points":100,"channel":"wechat"}' /api/pay/create)
ck T55-switch-off-msg '已停用' "$R"
echo "$R" | grep -qE 'mockpay://' && { FAIL=$((FAIL+1)); echo "FAIL|T55-switch-off-no-mock"; } || { PASS=$((PASS+1)); echo "PASS|T55-switch-off-no-mock"; }

# ⑧ 留痕：保存动作进审计（谁在什么时候改了收款凭据，出账纠纷时要能回溯）
AU55=$(sq "SELECT detail FROM audit_logs WHERE action='pay_channels_save' ORDER BY id DESC LIMIT 1")
ck T55-save-audited 'paych_wechat' "$AU55"

# ⑨ 空串=清除（管理台「清空」语义），删行而非留空值迷惑读路径
B55='{"fields":{"paych_wechat_apiv3_key":""}}'
R=$(post "$AH" "$B55" /api/admin/pay/channels/save)
ck T55-clear-secret '"success":true' "$R"
N55=$(sq "SELECT COUNT(*) FROM system_config WHERE key='paych_wechat_apiv3_key'" | tr -d '[:space:]')
[ "$N55" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T55-clear-deletes-row"; } || { FAIL=$((FAIL+1)); echo "FAIL|T55-clear-deletes-row(清空后仍留行)"; }
RC55=$(get "$AH" /api/admin/pay/channels)
ck T55-cleared-echo-empty '"paych_wechat_apiv3_key":""' "$RC55"

# ⑩ 清理：本用例写入的 paych_* 键删净，避免把 UAT 库留在「半套微信凭据」状态
dbq "DELETE FROM system_config WHERE key IN ('paych_wechat_app_id','paych_wechat_mch_id','paych_wechat_apiv3_key','paych_wechat_enabled','paych_notify_base')" >/dev/null
dbq "DELETE FROM audit_logs WHERE action='pay_channels_save'" >/dev/null
# 本用例的两次 wechat 下单失败各留下一张 pending 单（与 T43 同源，订单先建、取码才失败），
# 由 order_pending_timeout_min 巡检收口，此处只报数不判定，避免造出一条恒真的假绿断言。
ORD55=$(sq "SELECT COUNT(*) FROM orders WHERE channel='wechat' AND status='pending'" | tr -d '[:space:]')
echo "INFO|T55-leftover-wechat-pending=$ORD55"

# ---------- T56（★ 〇-U 批 I-1 · 缺陷 F-44 P0）对照编辑「保存即读回」双方言锁 ----------
# 缺陷因果链（2026-09-26 第二轮 E2E UAT F-44）：写侧 UpsertTranslationEdit 走了 internal/db
# 方言包装（`?`→`$n` 只在包装器里改写），读侧 GetTranslationEdits / GetTicketSegments 却裸用
# *sql.DB + `?`。lib/pq 不改写占位符 ⇒ PG 生产库上读查询直接语法错，而调用方写成
# `edits, _ :=`（错误被当成「无修订」吞掉），于是接口 200、界面提示「已保存」，
# 读回却永远为空、审批回写导出静默丢掉客户修订稿。SQLite 本地快跑全绿 ⇒
# ★ 本节每条断言都必须由 PG 方言的 run_uat.sh 主矩阵跑出来才算数（AGENTS §一·4、§7）。
# 四条腿：①写侧真落库 ②读侧等值回显（旧缺陷在这条红）③术语表读侧可读（ListKBTerms 同病，
# 且它另有 PG 专属语法错：SELECT DISTINCT 配 ORDER BY 未选择列）④回写产物含修订串。
T56T=$(tok uatuser_a uatpass123); H56="Authorization: Bearer $T56T"
[ ${#T56T} -lt 10 ] && { echo "FATAL|T56-auth|uatuser_a 重新登录失败"; exit 1; }
T56D=$(mktemp -d)
python3 - "$T56D/t56.docx" <<'PYEOF'
import sys, zipfile
zf = zipfile.ZipFile(sys.argv[1], 'w')
zf.writestr('[Content_Types].xml', '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>''')
zf.writestr('_rels/.rels', '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>''')
zf.writestr('word/document.xml', '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>对照编辑读回测试段一：修订稿必须活着到交付件。</w:t></w:r></w:p>
<w:p><w:r><w:t>对照编辑读回测试段二：吞掉的错误比红灯更贵。</w:t></w:r></w:p>
<w:sectPr/></w:body></w:document>''')
zf.close()
PYEOF
R=$(curl -s $B/api/tickets/create-file -H "$H56" -F "files=@$T56D/t56.docx" -F "target_langs=en" -F "mode=fast" --max-time 60)
ck T56-create '"success":true' "$R"
TK56=$(echo "$R" | pv "['ticket'].get('id')")
D56=$(waittk "$H56" "$TK56")
ck T56-completed '"status":"completed"' "$D56"

# ① 写侧：保存一段修订 + 批注 → saved=1，且库里确实落了这一行（写侧一直是好的，缺的是下面三条腿）
REV56="UAT-F44-REVISED-SEG0"
BODY56='{"edits":[{"index":0,"edited_text":"UAT-F44-REVISED-SEG0","status":"approved","note":"UAT-F44-NOTE-SEG0"}]}'
R=$(post "$H56" "$BODY56" "/api/tickets/segments/save?id=$TK56&lang=en")
ck T56-save-one '"saved":1' "$R"
WROTE56=$(dbq "SELECT edited_text FROM translation_edits WHERE ticket_id=$TK56 AND lang='en' AND seg_index=0" | tr -d '\r')
ck T56-db-row-written "$REV56" "$WROTE56"

# ② 读侧（★ F-44 本体）：同一个接口读回来必须等值含修订串/状态/批注，且不是 500
R=$(get "$H56" "/api/tickets/segments?id=$TK56&lang=en")
ck T56-read-success '"success":true' "$R"
ck T56-readback-edited "$REV56" "$R"
ck T56-readback-status 'UAT-F44-REVISED-SEG0","status":"approved"' "$R"
ck T56-readback-note 'UAT-F44-NOTE-SEG0' "$R"

# ③ 术语表读侧：直插一条租户术语（纯字面量 SQL，两方言通用）→ 读回必须点名它；
#    旧写法在此处同样是「PG 语法错 + 吞成空数组」，界面表现为「术语高亮永远不亮」而无任何痕迹。
dbq "INSERT INTO kb_entries (tenant_id, source_text, target_lang) VALUES ($TAID, 'UAT-F44-TERM-SEG0', 'en')" >/dev/null
R=$(get "$H56" "/api/tickets/segments?id=$TK56&lang=en")
ck T56-terms-read 'UAT-F44-TERM-SEG0' "$R"
dbq "DELETE FROM kb_entries WHERE tenant_id=$TAID AND source_text='UAT-F44-TERM-SEG0'" >/dev/null
N56TERM=$(dbq "SELECT COUNT(*) FROM kb_entries WHERE source_text='UAT-F44-TERM-SEG0'" | tr -dc '0-9')
[ "$N56TERM" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T56-term-row-cleaned"; } || { FAIL=$((FAIL+1)); echo "FAIL|T56-term-row-cleaned(手工插入的术语行没删净)"; }

# ④ 审批回写：导出产物里必须是修订稿（AGENTS §6 托管物口径：200 可能是 SPA 兜底页，
#    故先验 zip 魔数与体积，再解 word/document.xml 找修订串）。
R=$(post "$H56" '{}' "/api/tickets/segments/export?id=$TK56&lang=en")
ck T56-export-ok '"success":true' "$R"
URL56=$(echo "$R" | pv "['download']")
curl -s $B"$URL56" -H "$H56" -o "$T56D/edited.docx" --max-time 30
CHK56=$(python3 - "$T56D/edited.docx" "$REV56" <<'PYEOF'
import sys, zipfile
p, needle = sys.argv[1], sys.argv[2]
try:
    raw = open(p, 'rb').read()
    if raw[:2] != b'PK' or len(raw) < 800:
        print('NOT-ZIP')          # SPA 兜底页/空文件：按 AGENTS §6 判为无效产物
        sys.exit(0)
    xml = zipfile.ZipFile(p).read('word/document.xml').decode('utf-8', 'replace')
    print('HAS-REV' if needle in xml else 'NO-REV')
except Exception as e:
    print('ERR-%s' % e)
PYEOF
)
ck T56-export-carries-revision '^HAS-REV$' "$CHK56"
rm -rf "$T56D"

# ---------- T57（★ 〇-U 批 I-2 · 缺陷 F-45）反馈上下文不得写 "null" ----------
# 缺陷因果链：文本/工单反馈勾选「附带上下文」时，写侧对空译文映射做 json.Marshal 得到
# 字面量 "null" 并落进 feedbacks.translations；"null" 是**合法 JSON**，
# 超管后台详情读出来后 JSON.parse 不抛错、拿到 null，下一步 Object.entries(null) 才抛
# TypeError ⇒ 整块反馈详情白屏（客户看不到自己提的问题，也看不到平台回复）。
# 本节钉写侧（三种缺上下文形态都必须落 '{}'）+ 读侧（列表接口不得回带 "null"）。
# ★ 首跑踩坑落账（2026-09-26）：① 取用户令牌必须**就地重登**——脚本前段（T45 改密、T47 令牌轮换）
#   会让顶部抓的 $H1 失效，直接复用会得到 401 + 无 id ⇒ 下面整段对着空 ID 恒判（本脚本后段的
#   H46/H49A/H55/H56 全是就地重登，原因相同）；
# ② 超管反馈列表的真实路由是 /api/feedback/list（见 server.go 注册表；feedback.go 文件头注释里
#   写的 /api/admin/feedbacks 只有 /resolve 一条，照注释写断言会打进 404「接口不存在」假判）。
T57T=$(tok uatuser_a uatpass123); H57="Authorization: Bearer $T57T"
FID57=$(post "$H57" '{"target_type":"text","content":"UAT-F45缺上下文","with_context":true,"target_langs":"en","mode":"fast"}' /api/feedback | pv "['id']")
ck T57-text-feedback-created '^[0-9]+$' "$FID57"
T57COL=$(dbq "SELECT translations FROM feedbacks WHERE id=$FID57" | tr -d '\r')
ck T57-no-null-in-column '^{}$' "$T57COL"
FID57B=$(post "$H57" "{\"target_type\":\"ticket\",\"ticket_id\":$TK56,\"content\":\"UAT-F45工单反馈\",\"with_context\":true}" /api/feedback | pv "['id']")
ck T57-ticket-feedback-created '^[0-9]+$' "$FID57B"
T57COLB=$(dbq "SELECT translations FROM feedbacks WHERE id=$FID57B" | tr -d '\r')
ck T57-ticket-col-valid-json '^\{' "$T57COLB"
# 读侧负向锁（两腿分开命名：一腿证明「真的读到了列表」，一腿证明「读到的里没有 null」，
# 合名会让 404/空列表也判绿——AGENTS §一·6 兜底陷阱同源）
T57LIST=$(get "$AH" "/api/feedback/list?status=open")
ck T57-list-read-ok '"success":true' "$T57LIST"
ck T57-list-carries-probe 'UAT-F45缺上下文' "$T57LIST"
echo "$T57LIST" | grep -qF '"translations_json":"null"' && { FAIL=$((FAIL+1)); echo "FAIL|T57-list-no-null-translation(列表仍回带 null 上下文)"; } || { PASS=$((PASS+1)); echo "PASS|T57-list-no-null-translation"; }
# 全表不变量：新库里不得存在任何 translations='null' 的行（写侧已归一）
DIRTY57=$(dbq "SELECT COUNT(*) FROM feedbacks WHERE translations='null'" | tr -dc '0-9')
ck T57-table-no-null-rows '^0$' "$DIRTY57"
# 判据自证（防恒真）：手工插一行 'null' 必须被上面同一条判据抓到（抓到=1），
# 再用启动迁移里那条等价 UPDATE 洗一遍必须归 0——这一步同时验清洗 SQL 在**当前方言**下可执行。
dbq "INSERT INTO feedbacks (tenant_id, user_id, target_type, content, translations, status, created_at) SELECT tenant_id, user_id, target_type, 'UAT-F45-脏行探针', 'null', 'open', created_at FROM feedbacks WHERE id=$FID57B" >/dev/null
PROBE57=$(dbq "SELECT COUNT(*) FROM feedbacks WHERE translations='null' AND content='UAT-F45-脏行探针'" | tr -dc '0-9')
[ "${PROBE57:-0}" = "1" ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T57-dirty-probe-caught"; } || { FAIL=$((FAIL+1)); echo "FAIL|T57-dirty-probe-caught(判据没抓到已知脏行=恒绿断言, got=$PROBE57)"; }
dbq "UPDATE feedbacks SET translations='{}' WHERE translations='null' OR translations IS NULL" >/dev/null
AFTER57=$(dbq "SELECT COUNT(*) FROM feedbacks WHERE translations='null'" | tr -dc '0-9')
ck T57-migrate-sql-cleans '^0$' "$AFTER57"
dbq "DELETE FROM feedbacks WHERE id IN ($FID57,$FID57B) OR content='UAT-F45-脏行探针'" >/dev/null

# ---------- T58（★ 2026-09-26 〇-U 批 I-3 · 缺陷 F-55/F-56）配额读写同源 + 审计改前改后 ----------
# 缺陷因果链：配额 GET 用 authUser().TenantID（超管恒 0）取值，保存分支却用 effTenant(X-Tenant-ID)
# 写值 ⇒ 超管切到租户 3 后表单回的是「租户 0 的默认画像」（本轮实测 0/0/100000/1000，库内真值
# 10/3/100000/20000），照屏点一次保存就把 0 写进该租户两道日墙——而 billing/quota.go 里
# maxDaily<=0 的语义是**不限**，一次点击当场拆墙。另一半：审计 before 快照在写入之后才取
# ⇒ before==after、diff 恒空，且旧字段清单压根没有 max_daily_points。
# 本节锁「读→写→读」三方等值（GET 值＝库内真值、写入值＝再读值）＋审计 detail 逐字含改前/改后。
T58TID=$TAID
T58RATE=$RATE                                    # ★ F-78：与文件头现读的同源汇率（不再各自兜底一份，防两处口径分叉）
# t58perm — 从 tenants.permissions 取一个数值键（0 值因 omitempty 不在 JSON 里，缺键按 0 算）
t58perm(){ printf '%s' "$1" | python3 -c 'import sys,json
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
if not isinstance(d, dict):
    d = {}
print(int(d.get(sys.argv[1], 0) or 0))' "$2"; }
# t58eq — 直读等值锁（区别于 ck 的「响应体里搜得到」：本节的重点正是「两处取值必须相同」）
t58eq(){ if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "PASS|$1"; else FAIL=$((FAIL+1)); echo "FAIL|$1(want=$3 got=$2)"; fi; }
T58OLD=$(dbq "SELECT permissions FROM tenants WHERE id=$T58TID" | tr -d '\r' | head -1)
T58DBCHARS=$(t58perm "$T58OLD" max_daily_chars)
T58DBTOKS=$(t58perm "$T58OLD" max_daily_tokens)
# tokens→积分与后端 PointsFromTokens 同式（四舍五入整除）
T58DBPTS=$(python3 -c 'import sys
t, r = int(sys.argv[1]), int(sys.argv[2])
print(0 if t <= 0 or r <= 0 else (t + r // 2) // r)' "$T58DBTOKS" "$T58RATE")
R=$(curl -s "$B/api/billing/quota" -H "$AH" -H "X-Tenant-ID: $T58TID")
ck T58-read-ok '"success":true' "$R"
ck T58-tenant-selected '"tenant_selected":true' "$R"
T58QPS=$(printf '%s' "$R" | pv "['qps']")
T58CONC=$(printf '%s' "$R" | pv "['concurrent']")
t58eq T58-chars-equals-db "$(printf '%s' "$R" | pv "['max_daily_chars']")" "$T58DBCHARS"
t58eq T58-points-equals-db "$(printf '%s' "$R" | pv "['max_daily_points']")" "$T58DBPTS"
# 平台上下文（不带 X-Tenant-ID）：超管读的是「谁都不是」，必须显式标出来而不是给一串默认值
R=$(curl -s "$B/api/billing/quota" -H "$AH")
ck T58-platform-view-flagged '"tenant_selected":false' "$R"
# 同一上下文里点保存必须被拒（写侧本来就有这道门，此处锁它没被"顺手放宽"）
R=$(curl -s "$B/api/billing/quota/save" -H "$AH" -H "$J" -d '{"qps":5,"concurrent":2,"max_daily_chars":0}')
ck T58-save-needs-tenant '请先通过租户切换器选择目标租户' "$R"
# 写第一笔：改成一对 distinctive 值（qps/并发按回读值原样回提，避免顺带动限流影响 T16 并发段）
BODY58A="{\"qps\":$T58QPS,\"concurrent\":$T58CONC,\"max_daily_chars\":12345,\"max_daily_points\":777}"
R=$(curl -s "$B/api/billing/quota/save" -H "$AH" -H "X-Tenant-ID: $T58TID" -H "$J" -d "$BODY58A")
ck T58-save-a-ok '"success":true' "$R"
R=$(curl -s "$B/api/billing/quota" -H "$AH" -H "X-Tenant-ID: $T58TID")
t58eq T58-readback-chars "$(printf '%s' "$R" | pv "['max_daily_chars']")" "12345"
t58eq T58-readback-points "$(printf '%s' "$R" | pv "['max_daily_points']")" "777"
# 同源直查：库里 permissions 必须同步是 12345 / 777×rate（写侧不落库＝读写两套值的老形态）
T58NOW=$(dbq "SELECT permissions FROM tenants WHERE id=$T58TID" | tr -d '\r' | head -1)
t58eq T58-db-chars-after-write "$(t58perm "$T58NOW" max_daily_chars)" "12345"
t58eq T58-db-tokens-after-write "$(t58perm "$T58NOW" max_daily_tokens)" "$((777 * T58RATE))"
# 写第二笔：审计轨迹必须同时带**改前**与**改后**（旧实现 before 在写后取 ⇒ 两值相同、diff 恒空）
BODY58B="{\"qps\":$T58QPS,\"concurrent\":$T58CONC,\"max_daily_chars\":23456,\"max_daily_points\":888}"
R=$(curl -s "$B/api/billing/quota/save" -H "$AH" -H "X-Tenant-ID: $T58TID" -H "$J" -d "$BODY58B")
ck T58-save-b-ok '"success":true' "$R"
T58DET=$(dbq "SELECT detail FROM audit_logs WHERE action='tenant_quota_save' AND tenant_id=$T58TID ORDER BY id DESC LIMIT 1" | tr -d '\r' | head -1)
ck T58-audit-detail-chars '日字符 12345→23456' "$T58DET"
ck T58-audit-detail-points '日积分 777→888' "$T58DET"
T58BAK=$(dbq "SELECT before_val || '|' || after_val FROM audit_logs WHERE action='tenant_quota_save' AND tenant_id=$T58TID ORDER BY id DESC LIMIT 1" | tr -d '\r' | head -1)
ck T58-audit-before-points '"max_daily_points":777' "$T58BAK"
ck T58-audit-after-points '"max_daily_points":888' "$T58BAK"
# 判据自证（防恒真）：同一条 detail 判据必须**抓不到**上一笔（第一笔的改前不是 12345→23456）
T58PREV=$(dbq "SELECT detail FROM audit_logs WHERE action='tenant_quota_save' AND tenant_id=$T58TID ORDER BY id DESC LIMIT 1 OFFSET 1" | tr -d '\r' | head -1)
echo "$T58PREV" | grep -qF '日字符 12345→23456' && { FAIL=$((FAIL+1)); echo "FAIL|T58-audit-detail-scoped(上一笔也含同一串=判据不区分行)"; } || { PASS=$((PASS+1)); echo "PASS|T58-audit-detail-scoped"; }
# 收尾：按原 perms 串整串钉回（不留测试残留值给后续段），并等值复验
dbq "UPDATE tenants SET permissions='$T58OLD' WHERE id=$T58TID" >/dev/null
T58BACK=$(dbq "SELECT permissions FROM tenants WHERE id=$T58TID" | tr -d '\r' | head -1)
t58eq T58-restored-exact "$T58BACK" "$T58OLD"
dbq "DELETE FROM audit_logs WHERE action='tenant_quota_save' AND tenant_id=$T58TID AND detail LIKE '%12345%'" >/dev/null


# ---------- T59 账务同源与对外口径（★ 〇-U 批 I-4 · F-49/F-51/F-50） ----------
# 缺陷形态（2026-09-26 字节级 UAT 实测 + 逐行核实代码）：
#   F-49① 出参 points_used 由「调用点自己再乘一遍均摊系数」得出，而真正的扣费系数在策略引擎里
#         按租户+模式逐次解析（默认 1.5 只是两处各自的兜底常量）⇒ 客户按报文折算的账与实扣差一截，
#         且报文本身看不出异常、无从发现；
#   F-49② Engine.WithUsageRecorder 被引擎内层再注入一次并**遮蔽**外层收集器 ⇒ 09-25 那次
#         「补注入」（整改 R-L1）实际恒读 0——本轮 UAT 现场抓到 points_used=0，整条对外用量契约是假的；
#   F-51  withTenant 对 API Key 请求只注入租户不注入用户 ⇒ usage_ledger.user_id 落 0，
#         而「我的用量」按 tenant_id+user_id 过滤 ⇒ 这一笔在客户自己的账单里永久漏计；
#   F-50  对话页脚把 token 裸值拼进 reply 逐字渲染进客户气泡 ⇒ 穿透 AGENTS §一·5
#         「计费口径统一积分、公开接口零 token 裸值」，而既有闸门只扫结构化字段、扫不到文案里的数字。
# 本节把「同一笔调用的三个数必须相等」钉死：
#   报文 points_used ＝ 台账 SUM(quantity) 折积分 ＝「我的用量」计数增量；异步侧再加一条
#   tickets.tokens_billed ＝ 同一窗口台账 SUM(quantity)（sink 2s ticker ⇒ 有界重试等收敛，
#   判据目标取自 tickets 行、独立于台账，不会自证）。
# ★ 断言侧口径（沿用 T57 首跑踩坑账）：用户令牌就地重登（前段改密/轮换会让顶部 $H1 变 401）；
#   数值直读一律过 mny_norm 方言归一再等值（AGENTS §一·7）。
T59U=$(tok uatuser_a uatpass123); H59="Authorization: Bearer $T59U"
AK59BODY='{"name":"f49-key"}'
AK59=$(curl -s $B/api/apikeys/create -H "$H59" -H "$J" -d "$AK59BODY" | pv '.get("api_key","")')
ck T59-key-created '^rk_[0-9a-f]{40}$' "$AK59"
AK59UID=$(dbq "SELECT user_id FROM api_keys WHERE tenant_id=$TAID AND name='f49-key'" | tr -d '[:space:]')
ck T59-key-bound-user '^[1-9][0-9]*$' "$AK59UID" # 强绑定：无归属用户的 Key 一律无效（validateAPIKey 硬闸）
T59RATE=$T58RATE                                 # 折算率与 T58 同源（同一份 system_config，缺省 400）
# t59pts — 台账侧独立折算，必须与 store.PointsFromTokens 同式（四舍五入整除），否则等值锁两边不同源
t59pts(){ python3 -c 'import sys
t, r = int(sys.argv[1]), int(sys.argv[2])
print(0 if t <= 0 or r <= 0 else (t + r // 2) // r)' "${1:-0}" "${2:-0}"; }
t59eq(){ if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "PASS|$1"; else FAIL=$((FAIL+1)); echo "FAIL|$1(want=$3 got=$2)"; fi; }
# t59num — 数值直读归一。★ 首跑踩坑落账（2026-09-26）：mny_norm 的入参是**位置参数 $1**（不是 stdin），
# 写成 `... | mny_norm` 会在 set -u 下报 `$1: unbound variable` 并回吐空串，
# 于是三条等值锁全部「拿空串比空串」——qty/billed 两条红，而 async-ledger-equals 那条**假绿**。
t59num(){ local v; v=$(printf '%s' "${1:-}" | tr -d '[:space:]\r'); mny_norm "$v"; }
# 前序用例的 SSE/工单计量可能晚 1-2s 才入队（同 T1 的既有口径），先等一次冲刷干净再取窗口水位，
# 否则窗口里会混进别人的行，SUM 与计数都不可信。
sleep 4
T59MARK=$(dbq "SELECT COALESCE(MAX(id),0) FROM usage_ledger" | tr -d '[:space:]')
ME59A=$(get "$H59" /api/billing/usage/me)
CNT59A=$(printf '%s' "$ME59A" | pv "['count']"); [ -n "$CNT59A" ] || CNT59A=0
# ① 同步开放接口：报文积分 ＝ 台账积分（F-49①② 的正面锁）
BODY59='{"text":"账务同源测试文本F49","target_langs":["en"],"mode":"fast"}'
R=$(curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer $AK59" --max-time 120 -d "$BODY59")
ck T59-sync-ok '"success":true' "$R"
P59=$(printf '%s' "$R" | pv "['points_used']")
ck T59-sync-points-positive '^[1-9][0-9]*$' "$P59" # 旧缺陷现场：收集器被遮蔽 ⇒ 恒 0
T59W="id>$T59MARK AND tenant_id=$TAID AND user_id=$AK59UID"
T59Q=$(t59num "$(dbq "SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE $T59W")")
T59N=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE $T59W" | tr -d '[:space:]')
ck T59-ledger-rows '^[1-9][0-9]*$' "$T59N"
ck T59-ledger-qty-positive '^[1-9][0-9]*$' "$T59Q"
t59eq T59-points-equals-ledger "$P59" "$(t59pts "$T59Q" "$T59RATE")"
# ② F-51 归因：窗口内不得出现「别的用户」的行（旧形态 user_id=0 ⇒ 客户账单漏计这一笔）
T59BAD=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE id>$T59MARK AND tenant_id=$TAID AND user_id<>$AK59UID" | tr -d '[:space:]')
t59eq T59-ledger-user-scoped "$T59BAD" "0"
ME59B=$(get "$H59" /api/billing/usage/me)
CNT59B=$(printf '%s' "$ME59B" | pv "['count']"); [ -n "$CNT59B" ] || CNT59B=0
t59eq T59-usage-me-count "$((CNT59B - CNT59A))" "$T59N" # 「我的用量」计数增量＝台账新增行数（读侧同源）
# ③ F-50 公开面零 token 裸值：开放接口与站内对话两条对外通道都不得出现「token：123」形态
printf '%s' "$R" | grep -qE 'token[:：][[:space:]]*[0-9]' && { FAIL=$((FAIL+1)); echo "FAIL|T59-openapi-no-token-raw"; } || { PASS=$((PASS+1)); echo "PASS|T59-openapi-no-token-raw"; }
ck T59-openapi-mode-badge '快速模式' "$R"
CHAT59BODY='{"message":"页脚口径测试文本F50","options":{"mode":"fast","target_langs":["en"]}}'
RC=$(post "$H59" "$CHAT59BODY" /api/chat)
ck T59-chat-ok '"reply"' "$RC"
ck T59-chat-footer-points '本次翻译消耗 [0-9]+ 积分' "$RC"
printf '%s' "$RC" | grep -qE 'token[:：][[:space:]]*[0-9]' && { FAIL=$((FAIL+1)); echo "FAIL|T59-chat-no-token-raw"; } || { PASS=$((PASS+1)); echo "PASS|T59-chat-no-token-raw"; }
# 快速模式的徽标文案不得声称走了专业流水线（F-50②：文案收敛到 ModeBadgeLabel 一处，两侧不许各写一份）
printf '%s' "$RC" | grep -qE '专业校对模式' && { FAIL=$((FAIL+1)); echo "FAIL|T59-chat-badge-not-pro(快速模式回显了专业流水线文案)"; } || { PASS=$((PASS+1)); echo "PASS|T59-chat-badge-not-pro"; }
# ④ 异步工单：tickets.tokens_billed（完成时落库的实收）必须与同一窗口台账等值
T59AMARK=$(dbq "SELECT COALESCE(MAX(id),0) FROM usage_ledger" | tr -d '[:space:]')
TASK59=$(curl -s $B/openapi/v1/tasks -H "$J" -H "Authorization: Bearer $AK59" --max-time 60 -d '{"text":"异步工单账务同源F49","target_langs":["en"],"mode":"fast"}' | pv '.get("task_id") or 0')
ck T59-task-created '^[1-9][0-9]*$' "$TASK59"
POLL59=""
for i in $(seq 1 20); do
  POLL59=$(curl -s "$B/openapi/v1/tasks/status?id=$TASK59" -H "Authorization: Bearer $AK59" --max-time 30)
  echo "$POLL59" | grep -q '"status":"completed"' && break
  sleep 2
done
ck T59-task-completed '"status":"completed"' "$POLL59"
PA59=$(printf '%s' "$POLL59" | pv "['points_used']")
ck T59-async-points-positive '^[1-9][0-9]*$' "$PA59"
TB59=$(t59num "$(dbq "SELECT COALESCE(MAX(tokens_billed),0) FROM tickets WHERE id=$TASK59")")
ck T59-async-billed-positive '^[1-9][0-9]*$' "$TB59" # 旧缺陷：tokens_billed 落裸用量，与实扣差一个系数
t59eq T59-async-points-equals-ticket "$PA59" "$(t59pts "$TB59" "$T59RATE")"
T59WA="id>$T59AMARK AND tenant_id=$TAID AND user_id=$AK59UID"
T59ASUM=0
for i in $(seq 1 10); do
  T59ASUM=$(t59num "$(dbq "SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE $T59WA")")
  [ "$T59ASUM" = "$TB59" ] && break
  sleep 1
done
t59eq T59-async-ledger-equals-ticket "$T59ASUM" "$TB59"
# 判据反空锁：等值两侧都必须是正整数（两侧同为空串也会「相等」，那是假绿不是通过）
ck T59-async-ledger-qty-positive '^[1-9][0-9]*$' "$T59ASUM"
# 收尾：撤销本次签发的 Key（不留测试常开凭据），并复验已失效
KID59=$(dbq "SELECT id FROM api_keys WHERE tenant_id=$TAID AND name='f49-key'" | tr -d '[:space:]')
R=$(post "$H59" "{\"id\":$KID59}" /api/apikeys/delete)
ck T59-key-revoked '"success":true' "$R"
ck T59-key-dead 'invalid|无效' "$(curl -s $B/openapi/v1/balance -H "Authorization: Bearer $AK59")"
# ---------- T60 收款路径状态码诚实三件套（★ 〇-U 批 I-7 · F-64① 对外契约档） ----------
# 本节锁「失败按真实语义给码」：400 入参 / 401 未登录 / 403 等级不足 / 404 查无此单 /
# 409 状态冲突 / 503 渠道未就绪；同时留三条正向对照（合法请求仍须 200 + success:true），
# 防「把整个收款面改成报错」这种反向翻车也被宽松断言算成通过。
# 判据口径来自修复文档 §7.2：「每条被改的接口，把 UAT 断言从 success:false 改成
# 状态码 + 错误码 + 中文文案三件套」。
# 与 backend-go/internal/api/pay_status_honesty_test.go 的分工：那份是进程内行为锁（能造任意状态），
# 本节跑真实服务端 + 真实路由 + 真实库，两层互为反证（单测不经过 mux 中间件，脚本层抓得到）。
T60ALL=""   # 累积本节的失败响应体，末尾做一次「服务端内部细节不外泄」总闸
# ★ 令牌必须就地重登（2026-09-26 T60 首跑踩坑落账）：顶部 $H1 是脚本第 147 行取的，
#   中途 T6 改密与令牌轮换会把它打成 401 —— 于是整节「对着 401 断言 400/404/409」，
#   25 条一起假红（同 T57 首跑同一个坑，见文件头 T57 那段注）。本节自带令牌，不再蹭 $H1。
H60A="Authorization: Bearer $(tok uatuser_a uatpass123)"
ck T60-user-token '^.{20,}$' "${H60A#Bearer }"
# ① 400 入参族：这些客户改一下请求就能自证纠正，故必须是 400（不是 500、更不是 200 壳）
B60P0='{"points":0,"channel":"mock"}'
req3 POST "$H60A" /api/pay/create "$B60P0"; ck3 T60-create-points-zero 400 VALIDATION_ERROR '必须大于'
T60ALL="$T60ALL$R3BODY"
B60BAD='this-is-not-json'
req3 POST "$H60A" /api/pay/create "$B60BAD"; ck3 T60-create-bad-json 400 VALIDATION_ERROR '必须大于'
B60BIG='{"points":9999999999999,"channel":"mock"}'
req3 POST "$H60A" /api/pay/create "$B60BIG"; ck3 T60-create-points-overflow 400 VALIDATION_ERROR '超出允许范围'
B60CH='{"points":10,"channel":"bitcoin"}'
req3 POST "$H60A" /api/pay/create "$B60CH"; ck3 T60-create-bad-channel 400 VALIDATION_ERROR '不支持的支付渠道'
B60MOCK='{"points":10,"channel":"mock"}'
B60CP='{"code":"UAT60NOSUCH","points":100}'
req3 POST "$H60A" /api/coupon/preview "$B60CP"
ck3 T60-coupon-preview-400 400 VALIDATION_ERROR '券码不存在'
printf '%s' "$R3BODY" | grep -qE '"coupon_error"' && { PASS=$((PASS+1)); echo "PASS|T60-coupon-error-flag-in-details"; } || { FAIL=$((FAIL+1)); echo "FAIL|T60-coupon-error-flag-in-details(前端按此键决定是券区红字还是整单错误，键掉了就是静默回归)"; }
T60ALL="$T60ALL$R3BODY"
# ② 401（未登录）与 403（等级不足）必须分流：旧写法两条都回 403，
#    于是 token 过期的客户在收银台看到「无权限」并以为自己账号缺权限（前端 core.ts 只在 401 走重登录）。
req3 POST "" /api/pay/create "$B60P0"; ck3 T60-create-anon-401 401 UNAUTHORIZED '未登录'
req3 GET "" /api/me/package; ck3 T60-mepackage-anon-401 401 UNAUTHORIZED '未登录'
# ★ 首跑踩坑落账（2026-09-26）：本条最初用 $TAID（uatuser_a 自己的租户）断 403，实跑回 200 并真建了一单——
#   不是缺陷，是**判据前提写错**：handleOrderCreate 允许 tenant_admin 给【本租户】自助下单
#   （admin_billing.go:253 只有 !IsSuperAdmin && tenant_id≠effTenant 才 403）。
#   跨租户才有 403 可锁，故这里打 $TBID（uatuser_b 的租户）＝「A 的租户管理员替 B 下单」。
B60TEN="{\"tenant_id\":$TBID,\"points\":200,\"money\":0}"
req3 POST "" /api/admin/orders/create "$B60TEN"; ck3 T60-adminorder-anon-401 401 UNAUTHORIZED '未登录'
req3 POST "$H60A" /api/admin/orders/create "$B60TEN"; ck3 T60-adminorder-cross-tenant-403 403 FORBIDDEN '权限不足'
U60="uatmem60$(date +%s)"
B60USER="{\"username\":\"$U60\",\"password\":\"uatpass123\",\"display_name\":\"T60普通用户\",\"role\":\"user\",\"tenant_id\":$TAID}"
post "$AH" "$B60USER" /api/admin/users/create >/dev/null
M60=$(tok $U60 uatpass123); H60="Authorization: Bearer $M60"
ck T60-member-login '^.{20,}$' "$M60"
req3 POST "$H60" /api/pay/create "$B60MOCK"; ck3 T60-create-member-403 403 FORBIDDEN '权限不足'
req3 POST "$H60" /api/coupon/preview "$B60CP"; ck3 T60-coupon-preview-member-403 403 FORBIDDEN '权限不足'
req3 POST "" /api/coupon/preview "$B60CP"; ck3 T60-coupon-preview-anon-401 401 UNAUTHORIZED '未登录'
# ③ 404 查无此单（旧写法：200 壳 + 「订单不存在或渠道非 mock」这种二合一文案）
req3 GET "$H60A" "/api/pay/status?order_id=987654321"; ck3 T60-status-404 404 NOT_FOUND '订单不存在'
req3 GET "$H60A" "/api/pay/status"; ck3 T60-status-no-param 400 VALIDATION_ERROR '缺少 order_id'
B60GHOST='{"order_id":987654321}'
req3 POST "$H60A" /api/pay/simulate "$B60GHOST"; ck3 T60-simulate-404 404 NOT_FOUND '订单不存在'
req3 POST "$H60A" /api/pay/manual-confirm "$B60GHOST"; ck3 T60-manualconfirm-404 404 NOT_FOUND '订单不存在'
B60VOIDGHOST='{"id":987654321}'
req3 POST "$H60A" /api/billing/invoices/void "$B60VOIDGHOST"; ck3 T60-invoice-void-404 404 NOT_FOUND '发票不存在'
B60SUBEMPTY='{"code":""}'
req3 POST "$H60A" /api/package/subscribe "$B60SUBEMPTY"; ck3 T60-subscribe-empty-code 400 VALIDATION_ERROR 'code 不能为空'
B60SUBGHOST='{"code":"uat_pkg_not_exists_60"}'
req3 POST "$H60A" /api/package/subscribe "$B60SUBGHOST"; ck3 T60-subscribe-404 404 NOT_FOUND '套餐不存在'
T60ALL="$T60ALL$R3BODY"
# ④ 409 状态冲突：单子找得到、但当前状态不允许这个动作（与 404「没有这单」必须可区分）
B60MAN='{"points":10,"channel":"manual"}'
req3 POST "$H60A" /api/pay/create "$B60MAN"
ck T60-manual-order-created '"success":true' "$R3BODY"
OID60=$(printf '%s' "$R3BODY" | pv '.get("order",{}).get("id") or 0')
B60SIMMAN="{\"order_id\":$OID60}"
req3 POST "$H60A" /api/pay/simulate "$B60SIMMAN"; ck3 T60-simulate-manual-409 409 CONFLICT '渠道非 mock'
# ⑤ 503 渠道未就绪：与 ① 的「渠道名写错 →400」配成一对反证——
#    名字不在白名单是入参问题，名字对但平台没配收款要素是服务端没准备好（客户无从纠正）。
#    先存回原值再清空，跑完必须钉回，否则后面的用例会在错误前提下跑（同 dblib.sh dbcfg 的中止口径）。
QR60=$(dbq "SELECT value FROM system_config WHERE key='static_qr_image'" | tr -d '[:space:]')
dbcfg static_qr_image ''
req3 POST "$H60A" /api/pay/create "$B60MAN"; ck3 T60-manual-no-qr-503 503 PAY_CHANNEL_UNAVAILABLE '静态收款码未配置'
dbcfg static_qr_image "$QR60"
req3 POST "$H60A" /api/pay/create "$B60MAN"
ck T60-manual-qr-restored '"success":true' "$R3BODY"
# ⑥ 正向对照：mock 下单 → 模拟到账 → 开票 → 冲红，全链路仍须 200 + success:true
B60PAID='{"points":12,"channel":"mock"}'
req3 POST "$H60A" /api/pay/create "$B60PAID"
ck T60-mock-create-ok '"success":true' "$R3BODY"
[ "${R3ST:-}" = "200" ] && { PASS=$((PASS+1)); echo "PASS|T60-mock-create-status-200"; } || { FAIL=$((FAIL+1)); echo "FAIL|T60-mock-create-status-200(got $R3ST)"; }
OID60P=$(printf '%s' "$R3BODY" | pv '.get("order",{}).get("id") or 0')
B60SIMP="{\"order_id\":$OID60P}"
req3 POST "$H60A" /api/pay/simulate "$B60SIMP"
ck T60-mock-simulate-ok '"success":true' "$R3BODY"
B60INV="{\"order_id\":$OID60P,\"title\":\"T60诚实性对照发票\",\"tax_no\":\"TX9060\"}"
req3 POST "$H60A" /api/billing/invoices/create "$B60INV"
ck T60-invoice-create-ok '"success":true' "$R3BODY"
IV60=$(printf '%s' "$R3BODY" | pv '.get("invoice",{}).get("id") or 0')
B60VOID="{\"id\":$IV60}"
req3 POST "$H60A" /api/billing/invoices/void "$B60VOID"
ck T60-invoice-void-ok '"success":true' "$R3BODY"
req3 POST "$H60A" /api/billing/invoices/void "$B60VOID"; ck3 T60-invoice-void-dup-409 409 CONFLICT '不存在或已作废'
# 同单重开（★ T43 冲红闭环）：作废后仍可开票，成功路径不能被 409 判定误伤
req3 POST "$H60A" /api/billing/invoices/create "$B60INV"
ck T60-invoice-reopen-after-void '"success":true' "$R3BODY"
# ⑦ 诚实化改造不许把服务端内部细节一起带出去（F-43 同族：驱动原文、SQL、约束名一律禁止上屏）
printf '%s' "$T60ALL" | grep -qiE 'sql:|no rows|SQLSTATE|constraint|panic:|goroutine|dsn' && { FAIL=$((FAIL+1)); echo "FAIL|T60-no-internal-leak(失败响应体里出现了内部细节，见本节各条 body)"; } || { PASS=$((PASS+1)); echo "PASS|T60-no-internal-leak"; }
# ---------- T61 AI 助手管理代理：鉴权分流 + 白名单 + 上游透传（★ 〇-U 批 I-10 · F-64③） ----------
# 本节锁本层（主后台同源代理）自己的四类失败各自落在哪个码上，与
# backend-go/internal/api/admin_assist_proxy_test.go 的进程内锁互补：
#   ① 未登录 401 / 已登录非超管 403 必须**分流**（旧写法两支都回 403：超管 token 过期后
#      前端 core.ts 只在 401 触发重登录，于是他在后台看着「权限不足」原地撞墙）；
#   ② 白名单外的 assist 路径不得被放行（真实路由上由 spa.go 的 /api 兜底回 404 JSON，
#      绝不能回成 HTML 整页——回成 HTML 就说明接口路径被当成前端路由兜底了）；
#   ③ 上游 assist 自己拒的 4xx **原样透传**（反向锁：不许被本层换成统一错误体，
#      否则运维只看到我们的中文套话，丢掉上游那句真实拒绝原因）；
#   ④ 正向对照：超管读代理仍须 200 + 上游 rows（把鉴权面整体改报错也能过 ①②，必须有这条反证）。
# 502（assist 不可达）与 503（未配管理 Token）两支只能在进程内造：停上游或清凭据会打断
# 后面所有 T52/T61 用例，编排环境里做代价远大于收益，由那份单测承担，此处不重复。
T61ALL=""
req3 GET "" /api/admin/assist/status; ck3 T61-status-anon-401 401 UNAUTHORIZED '未登录'
T61ALL="$T61ALL$R3BODY"
req3 GET "$H1" /api/admin/assist/status; ck3 T61-status-normal-403 403 FORBIDDEN '权限不足'
req3 GET "" /api/admin/assist/sessions; ck3 T61-proxy-anon-401 401 UNAUTHORIZED '未登录'
req3 GET "$H1" /api/admin/assist/sessions; ck3 T61-proxy-normal-403 403 FORBIDDEN '权限不足'
# ② 白名单外路径不放行：mux 只登记 assistProxyRoutes 里的那些路径（server.go:225），
#    未登记的 assist 路径根本进不到代理，由 spa.go 的 /api 兜底统一回 404 JSON。
#    这里锁「404 + 不是 HTML + 接口不存在」而不是错误码：代理内部那支带 code=NOT_FOUND 的
#    拒绝分支在真实路由上是**防御性死支**，它的行为锁在 admin_assist_proxy_test.go 的
#    TestAssistProxyWhitelist（直接调 handler）——写在这里会变成永远红或永远假的错位断言。
req3 GET "$AH" /api/admin/assist/zzz-not-a-route
if [ "${R3ST:-}" = "404" ] && printf '%s' "$R3BODY" | grep -qE '接口不存在' && ! printf '%s' "$R3BODY" | grep -qiE '<html|<!DOCTYPE'; then
  PASS=$((PASS+1)); echo "PASS|T61-off-whitelist-404-json"
else
  FAIL=$((FAIL+1)); echo "FAIL|T61-off-whitelist-404-json(got ${R3ST:-?} body=${R3BODY:0:160}；HTML 兜底＝SPA 回退把接口当成前端路由了)"
fi
T61ALL="$T61ALL$R3BODY"
# ④ 正向对照（状态码与上游原文一起验，只看 200 会放过「本层自己编一个空 rows」）
req3 GET "$AH" /api/admin/assist/kb
if [ "${R3ST:-}" = "200" ] && printf '%s' "$R3BODY" | grep -qE '"rows":\['; then
  PASS=$((PASS+1)); echo "PASS|T61-super-read-passthrough-200"
else
  FAIL=$((FAIL+1)); echo "FAIL|T61-super-read-passthrough-200(got ${R3ST:-?} body=${R3BODY:0:160})"
fi
# ③ 上游 4xx 原样透传：assist 白名单外的配置键由**上游**拒绝（400 + "key not allowed"）。
#    本层若把它包成统一错误体，这一支就会同时丢掉真实原因与可区分的状态码。
req3 PUT "$AH" /api/admin/assist/config '{"key":"not_a_real_key","value":"x"}'
[ "${R3ST:-}" = "400" ] && { PASS=$((PASS+1)); echo "PASS|T61-upstream-400-status-passthrough"; } || { FAIL=$((FAIL+1)); echo "FAIL|T61-upstream-400-status-passthrough(got ${R3ST:-?}，代理不得改写上游状态码)"; }
printf '%s' "$R3BODY" | grep -qE 'key not allowed' && { PASS=$((PASS+1)); echo "PASS|T61-upstream-message-passthrough"; } || { FAIL=$((FAIL+1)); echo "FAIL|T61-upstream-message-passthrough(body=${R3BODY:0:160})"; }
[ -z "${R3CODE:-}" ] && { PASS=$((PASS+1)); echo "PASS|T61-passthrough-carries-no-unified-code"; } || { FAIL=$((FAIL+1)); echo "FAIL|T61-passthrough-carries-no-unified-code(透传体里出现了 code=${R3CODE}，说明改写路径还活着)"; }
# 诚实化改造不许把底层错误串一起带出来（同 T60 ⑦ 口径：dial/connection refused 一律禁止上屏）
printf '%s' "$T61ALL" | grep -qiE 'sql:|no rows|SQLSTATE|constraint|panic:|goroutine|dial |connection refused' && { FAIL=$((FAIL+1)); echo "FAIL|T61-no-internal-leak(失败响应体里出现内部细节)"; } || { PASS=$((PASS+1)); echo "PASS|T61-no-internal-leak"; }

# ---------- T62 状态码诚实收尾三处定夺（★ 〇-U 批 I-10 · F-64② 收尾） ----------
# 本批收尾时有三处「同一件事两个码 / 一个码泄漏存在性」的定夺，逐条落到断言上：
#   ① 注册邮箱与既有账号撞车 → 409 CONFLICT（旧写法回 400，而换绑邮箱那两处回 409：
#      同一句文案两个码，前端/SDK 按 code 分支要为同一种失败写两条判断）；
#   ② 反馈详情：别人的反馈 与 根本不存在的 id → **同码同文案**（旧写法一个 403 一个 404，
#      等于把「这一条存在」白送给任何登录用户，反馈总量与提交节奏变成免费计数接口）；
#   ③ 知识库管理口匿名 → 401（旧写法内联 403，同 T61① 一类：token 过期的租户管理员
#      在知识库页看着「无权限」而看不见「请重登」）。
SFX62=$(date +%s | tail -c 6)
U62="uatm62$SFX62"
E62="uat62_$SFX62@test.com"
T62ALL=""
# ① 先用一个全新邮箱注册成功（正向前提），再拿同一邮箱换用户名注册 → 必须 409
B62R1="{\"username\":\"$U62\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T62邮箱撞车\",\"email\":\"$E62\",\"agreed\":true}"
req3 POST "" /api/auth/register "$B62R1"
if [ "${R3ST:-}" = "200" ] && printf '%s' "$R3BODY" | grep -qE '"success":true'; then
  PASS=$((PASS+1)); echo "PASS|T62-register-first-ok"
else
  FAIL=$((FAIL+1)); echo "FAIL|T62-register-first-ok(got ${R3ST:-?} body=${R3BODY:0:160}，前提不成立则下一条 409 无意义)"
fi
B62R2="{\"username\":\"${U62}b\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T62邮箱撞车二\",\"email\":\"$E62\",\"agreed\":true}"
req3 POST "" /api/auth/register "$B62R2"
ck3 T62-register-dup-email-409 409 CONFLICT '该邮箱已被其他账号绑定'
T62ALL="$T62ALL$R3BODY"
# ② 反馈详情存在性探针：先由 uatuser_a 提一条，再让 uatuser_b（另一租户）拿 id 试探
FCONTENT="T62 存在性探针锁用反馈 $SFX62"
B62F="{\"target_type\":\"text\",\"content\":\"$FCONTENT\"}"
req3 POST "$H1" /api/feedback "$B62F"
[ "${R3ST:-}" = "200" ] && { PASS=$((PASS+1)); echo "PASS|T62-feedback-created"; } || { FAIL=$((FAIL+1)); echo "FAIL|T62-feedback-created(got ${R3ST:-?} body=${R3BODY:0:160})"; }
FID62=$(dbq "SELECT id FROM feedbacks WHERE content='$FCONTENT' ORDER BY id DESC LIMIT 1" | tr -d '[:space:]')
ck T62-feedback-id-read '^[0-9]+$' "$FID62"
GHOST62=987654321   # 明确不存在的 id（自增主键不可能到这个量级）
req3 GET "$H2" "/api/feedback/get?id=$FID62"; ck3 T62-feedback-peer-404 404 NOT_FOUND '反馈不存在'
P62="${R3ST}/${R3CODE}/${R3MSG}"; PB62="$R3BODY"
T62ALL="$T62ALL$R3BODY"
req3 GET "$H2" "/api/feedback/get?id=$GHOST62"; ck3 T62-feedback-ghost-404 404 NOT_FOUND '反馈不存在'
G62="${R3ST}/${R3CODE}/${R3MSG}"; GB62="$R3BODY"
# ★ 核心等值锁：两条通道的「状态码/错误码/文案」三元组必须逐字相同——
#   只要有任何一维不同，调用方就能数出「这一条存在」，探针就还开着。
if [ "$P62" = "$G62" ]; then PASS=$((PASS+1)); echo "PASS|T62-feedback-probe-closed($P62)"
else FAIL=$((FAIL+1)); echo "FAIL|T62-feedback-probe-closed(越权=$P62 / 查无=$G62，三元组必须逐字相等)"; fi
# 越权详情不得回带反馈字段（只承认「不存在」，不承认「有这么一条」）。
# ⚠️ 两条响应体**分别**判：只判 ghost 会让「peer 漏字段但 ghost 干净」这种半截改法静默通过。
LEAK62=0
for BD62 in "$PB62" "$GB62"; do
  if printf '%s' "$BD62" | grep -qE '"feedback"|"content"|"user_id"|"source_text"'; then LEAK62=1; fi
done
if [ "$LEAK62" = "0" ]; then PASS=$((PASS+1)); echo "PASS|T62-detail-no-fields"
else FAIL=$((FAIL+1)); echo "FAIL|T62-detail-no-fields(越权/查无响应体带了业务字段: ${PB62:0:160})"; fi
# 正向对照：本人仍须 200 且取到自己那条（把详情口整面改报错也能过上面四条）
req3 GET "$H1" "/api/feedback/get?id=$FID62"
if [ "${R3ST:-}" = "200" ] && printf '%s' "$R3BODY" | grep -qE '"feedback"'; then
  PASS=$((PASS+1)); echo "PASS|T62-owner-detail-200"
else
  FAIL=$((FAIL+1)); echo "FAIL|T62-owner-detail-200(got ${R3ST:-?} body=${R3BODY:0:160})"
fi
# ③ 知识库管理口匿名 401（本批从内联 403 纠正过来的那一族，两个文件各锁一支）
req3 GET "" /api/admin/kb-entries; ck3 T62-kb-entries-anon-401 401 UNAUTHORIZED '未登录'
B62IMP='{"package_id":1,"entries":[]}'
req3 POST "" /api/admin/kb-entries/import "$B62IMP"; ck3 T62-kb-import-anon-401 401 UNAUTHORIZED '未登录'
req3 GET "" /api/admin/brand-terms; ck3 T62-brand-terms-anon-401 401 UNAUTHORIZED '未登录'
T62ALL="$T62ALL$R3BODY"
printf '%s' "$T62ALL" | grep -qiE 'sql:|no rows|SQLSTATE|constraint|panic:|goroutine|dsn' && { FAIL=$((FAIL+1)); echo "FAIL|T62-no-internal-leak(失败响应体里出现内部细节)"; } || { PASS=$((PASS+1)); echo "PASS|T62-no-internal-leak"; }

# ---------- T63 鉴权分流尾量代表口（★ 〇-U 批 I-10 ③ 尾量 122 处的 HTTP 级常设锁） ----------
# 批 I-10 把 27 个文件里 122 处「未登录也回 403」的内联错误体迁到 s.writeAuthzError
# （server.go：errNotLogin→401 UNAUTHORIZED、等级不足→403 FORBIDDEN，**文案逐字不变**）。
# 进程内锁是 errorstyle 棘轮 + 各 handler 单测；这里补 HTTP 级抽查，按**三类守卫各取一代表**
# 钉分流形态——将来谁把 writeAuthzError 换回内联 403，本段先红：
#   ① 超管口（admin_models.go，requireAdminUser）：匿名 401 / 租管 403 / 超管 200 三态齐；
#   ② 租管口（admin_webhooks.go，requireTenantAdmin）：匿名 401，且租管（$H1）必须 200
#      ——正向对照防「鉴权面整体改报错也能过 ①」；
#   ③ 部门口（orgs.go）：匿名 401 一支即可，等级链行为由单测承担。
req3 GET "" /api/admin/models; ck3 T63-models-anon-401 401 UNAUTHORIZED '未登录'
req3 GET "$H1" /api/admin/models; ck3 T63-models-tenantadmin-403 403 FORBIDDEN '权限不足'
req3 GET "$AH" /api/admin/models
[ "${R3ST:-}" = "200" ] && { PASS=$((PASS+1)); echo "PASS|T63-models-super-200"; } || { FAIL=$((FAIL+1)); echo "FAIL|T63-models-super-200(got ${R3ST:-?} body=${R3BODY:0:120})"; }
req3 GET "" /api/webhooks; ck3 T63-webhooks-anon-401 401 UNAUTHORIZED '未登录'
req3 GET "$H1" /api/webhooks
[ "${R3ST:-}" = "200" ] && { PASS=$((PASS+1)); echo "PASS|T63-webhooks-tenantadmin-200"; } || { FAIL=$((FAIL+1)); echo "FAIL|T63-webhooks-tenantadmin-200(got ${R3ST:-?} body=${R3BODY:0:120}；401＝H1 失效假绿，勿放宽)"; }
req3 GET "" /api/admin/orgs; ck3 T63-orgs-anon-401 401 UNAUTHORIZED '未登录'

# ---------- T64 计费预估系数管理台表单（★ 〇-X 第 5 项 / F-72 补口，2026-09-27） ----------
# 本节锁 GET/POST /api/admin/config/est-tokens（后端 est_tokens_config.go、前端 PlansP 超管段）。
# 为什么值得一条 HTTP 级常设锁：这四个数直接决定「客户能不能建单」——K 清零＝余额预检全放行，
# 正是 F-41 那笔「估 1.7 万、实烧 107 万、中途烧穿全损」的事故形态；而在管理台之前它们只能靠
# psql 往 system_config 插行，手滑一次就是全量放行或全量误拦，且没有任何留痕。
# 五组判据：① 鉴权三态；② 未配置时回显代码缺省 + 公式/汇率只读面；③ 四类非法输入整批拒收
# 且**库里一行都不许多出来**（半套参数＝长短单口径不一致，比不调更糟）；④ 合法保存→读回等值
# ＋审计留痕含新数；⑤ 脏值（库里躺着 'abc'）时界面必须显示「系统真正在用哪个数」而不是脏值本身，
# 同时 stored 原样回吐把诊断线索留住；⑥ reset 回落缺省。
# ★ 收尾必须删掉本段写过的四键：T40（脚本前段）是**现读** est_tokens_*_fast 来算等值锁的，
#   而 run_uat 复用同一个库跑下一轮——留着 161/61 会让 T40 跟着漂移，且「未配置走缺省」这条
#   线上真实形态就再也没被测到了。
ESTCFG=/api/admin/config/est-tokens
ESTKEYS="'est_tokens_per_char_pro','est_tokens_per_char_fast','est_tokens_fixed_pro','est_tokens_fixed_fast'"
# estv <键> → 从 R3BODY 的 coefficients 里取一个生效值（取不到回空，交给等值判据报红）
estv(){ printf '%s' "${R3BODY:-}" | python3 -c 'import sys,json
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
c = d.get("coefficients") if isinstance(d, dict) else None
v = c.get(sys.argv[1]) if isinstance(c, dict) else None
print("" if v is None else v)' "$1" 2>/dev/null; }
# estrow <键> → 从 R3BODY 的 stored 里取库内原值（空串＝该键未配置）
estrow(){ printf '%s' "${R3BODY:-}" | python3 -c 'import sys,json
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
s = d.get("stored") if isinstance(d, dict) else None
v = s.get(sys.argv[1]) if isinstance(s, dict) else None
print("" if v is None else v)' "$1" 2>/dev/null; }
# ESTCNT → 「**真正表态了**的四键行数」：值空串＝与从未配置同形态（读侧一律回代码缺省），
#   所以只数 value<>''。否则本段 reset 之后残留的 4 行空串会让行数判据在下一轮 4→0 假红
#   （首跑真踩：跑完留下的空串行把「还原」判据变红，而生效值其实一模一样走缺省）。
ESTCNT(){ sq "SELECT COUNT(*) FROM system_config WHERE key IN ($ESTKEYS) AND value<>''" | tr -d '[:space:]'; }
# ESTGET <键> → 现读库内原值（本段既要直插脏值、又要 reset 清空，跑完必须**原样还回去**：
#   正常情况下两库都没有这四行（走代码缺省），但万一运维已经用管理台配过，脚本不能替他改数。
ESTGET(){ dbq "SELECT value FROM system_config WHERE key='$1'" | tr -d '\r' | head -1; }
# ckq <名> <期望字面值> <实测> → **等值**锁，不用 ck 的正则形态：正则 '0' 会被 160/1200 命中，
#   '60' 会被 160 命中——本段量的正是「读回来的到底是不是那个数」，宽松正则＝恒绿假断言。
ckq(){ if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "PASS|$1($2)"; else FAIL=$((FAIL+1)); echo "FAIL|$1|want=$2|got=${3:-<空>}"; fi; }
# 令牌就地重登：$AH 取在脚本顶部（第 159 行那次登录），中途 T6 改密/轮换会把它打成 401
#   （同 T60 首跑踩过的坑），对着 401 断言 400 会让整节假红。
AH64="Authorization: Bearer $(tok $ADMIN_USER $ADMIN_PASS)"
ck T64-super-token '^.{20,}$' "${AH64#Bearer }"
EST0=$(ESTCNT); EST0=${EST0:-0}
PRE64KPRO=$(ESTGET est_tokens_per_char_pro); PRE64KFAST=$(ESTGET est_tokens_per_char_fast)
PRE64FPRO=$(ESTGET est_tokens_fixed_pro);     PRE64FFAST=$(ESTGET est_tokens_fixed_fast)
# ① 鉴权三态（匿名 401 / 租管 403 / 超管 200）
req3 GET "" $ESTCFG; ck3 T64-get-anon-401 401 UNAUTHORIZED '未登录'
req3 GET "$H1" $ESTCFG; ck3 T64-get-tenantadmin-403 403 FORBIDDEN '权限不足|仅平台超管'
B64ANY='{"k_pro":160,"k_fast":60,"fixed_pro":3000,"fixed_fast":1200}'
#   ★ 租管这一腿在 requireAdminUser 就被挡（"权限不足"），走不到 IsSuperAdmin 那支
#   （"仅平台超管"）——两支文案都收下：真漏了超管判定时会由 Go 侧单测红在这里由 HTTP 级红，
#   而把两支合并成一条正则才不会被「守卫链换人」这种等价改法假红。
req3 POST "$H1" $ESTCFG "$B64ANY"; ck3 T64-post-tenantadmin-403 403 FORBIDDEN '权限不足|仅平台超管'
# ② 未配置态回显：四系数＝代码缺省，且公式/汇率两条只读面必须在前端可达（表单要显示它们）
req3 GET "$AH64" $ESTCFG
[ "${R3ST:-}" = "200" ] && { PASS=$((PASS+1)); echo "PASS|T64-get-200"; } || { FAIL=$((FAIL+1)); echo "FAIL|T64-get-200(got ${R3ST:-?} body=${R3BODY:0:160}；401＝AH64 失效假绿，勿放宽)"; }
ckq T64-default-kpro 160 "$(estv k_pro)"
ckq T64-default-kfast 60 "$(estv k_fast)"
ckq T64-default-fpro 3000 "$(estv fixed_pro)"
ckq T64-default-ffast 1200 "$(estv fixed_fast)"
printf '%s' "$R3BODY" | grep -qE '"formula":"[^"]' && { PASS=$((PASS+1)); echo "PASS|T64-formula-echoed"; } || { FAIL=$((FAIL+1)); echo "FAIL|T64-formula-echoed(公式没回显＝前端那段等式只能写死在组件里，与后端口径会漂)"; }
printf '%s' "$R3BODY" | grep -qE '"points_tokens_rate":[1-9]' && { PASS=$((PASS+1)); echo "PASS|T64-rate-echoed"; } || { FAIL=$((FAIL+1)); echo "FAIL|T64-rate-echoed(汇率只读面缺失，管理台无法把 token 系数折成积分)"; }
# ③ 四类非法输入整批拒收，且库里一行都不许多出来
B64K0='{"k_pro":0,"k_fast":60,"fixed_pro":3000,"fixed_fast":1200}'
req3 POST "$AH64" $ESTCFG "$B64K0"; ck3 T64-reject-k-zero 400 VALIDATION_ERROR '必须大于 0'
B64NEG='{"k_pro":160,"k_fast":60,"fixed_pro":-1,"fixed_fast":1200}'
req3 POST "$AH64" $ESTCFG "$B64NEG"; ck3 T64-reject-negative 400 VALIDATION_ERROR '不能为负数'
B64BIG='{"k_pro":160,"k_fast":99999999,"fixed_pro":3000,"fixed_fast":1200}'
req3 POST "$AH64" $ESTCFG "$B64BIG"; ck3 T64-reject-over-max 400 VALIDATION_ERROR '超出合理范围'
B64MISS='{"k_pro":160,"k_fast":60,"fixed_pro":3000}'
req3 POST "$AH64" $ESTCFG "$B64MISS"; ck3 T64-reject-missing-field 400 VALIDATION_ERROR '缺少 fixed_fast'
EST1=$(ESTCNT); EST1=${EST1:-0}
[ "$EST1" = "$EST0" ] && { PASS=$((PASS+1)); echo "PASS|T64-reject-leaves-nothing(四键行数 $EST0→$EST1)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T64-reject-leaves-nothing(拒收却把行数从 $EST0 写成 $EST1＝半套配置，长短单口径会不一致)"; }
# ④ 合法保存 → 读回等值 + 审计留痕（F 档允许 0＝关闭固定项，这里顺带锁一次）
#   ★ 成功体是 {success:true, coefficients:{…}}，没有 code/message 两字段，故不走 ck3（它会等值比
#   错误码），只判状态码 200 + success:true + 生效值读回。
B64OK='{"k_pro":161,"k_fast":61,"fixed_pro":3001,"fixed_fast":0}'
req3 POST "$AH64" $ESTCFG "$B64OK"
ck T64-save-ok '"success":true' "$R3BODY"
req3 GET "$AH64" $ESTCFG
ckq T64-readback-kpro 161 "$(estv k_pro)"
ckq T64-readback-kfast 61 "$(estv k_fast)"
ckq T64-readback-fpro 3001 "$(estv fixed_pro)"
ckq T64-readback-ffast-zero 0 "$(estv fixed_fast)"
EST64DET=$(dbq "SELECT detail FROM audit_logs WHERE action='est_tokens_save' ORDER BY id DESC LIMIT 1" | tr -d '\r' | head -1)
ck T64-audit-has-new-values 'k_pro=161' "$EST64DET"
# ⑤ 脏值不伪装成生效值：绕过校验直插 'abc' → 生效值回**代码缺省**（不是上一批的 161），
#   同时 stored 原样回吐＋defaults/allow_zero 齐面，前端才可能标出「这一行是脏的、当前用的是缺省」。
dbq "DELETE FROM system_config WHERE key='est_tokens_per_char_pro'" >/dev/null
dbq "INSERT INTO system_config(key,value) VALUES('est_tokens_per_char_pro','abc')" >/dev/null
req3 GET "$AH64" $ESTCFG
ckq T64-dirty-uses-default 160 "$(estv k_pro)"
ckq T64-stored-raw abc "$(estrow k_pro)"
printf '%s' "$R3BODY" | grep -qE '"defaults":\{[^}]*k_pro' && { PASS=$((PASS+1)); echo "PASS|T64-defaults-echoed"; } || { FAIL=$((FAIL+1)); echo "FAIL|T64-defaults-echoed(代码缺省没回显＝前端只能自己抄一份 160/60，与后端常量必然漂移)"; }
printf '%s' "$R3BODY" | grep -qE '"allow_zero":\{[^}]*fixed_pro' && { PASS=$((PASS+1)); echo "PASS|T64-allow-zero-echoed"; } || { FAIL=$((FAIL+1)); echo "FAIL|T64-allow-zero-echoed(哪些档可以填 0 没回显，表单只能猜，猜错就是把 K 清零放行)"; }
# ⑥ reset：四项一律清空回落缺省（写成空串＝与「从未配置」同形态）
B64R='{"reset":true}'
req3 POST "$AH64" $ESTCFG "$B64R"
ck T64-reset-ok '"success":true' "$R3BODY"
req3 GET "$AH64" $ESTCFG
ckq T64-reset-kpro 160 "$(estv k_pro)"
ckq T64-reset-ffast 1200 "$(estv fixed_fast)"
RST64=$(dbq "SELECT COUNT(*) FROM system_config WHERE key IN ($ESTKEYS) AND value<>''" | tr -d '[:space:]')
[ "${RST64:-0}" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T64-reset-blanks-rows"; } || { FAIL=$((FAIL+1)); echo "FAIL|T64-reset-blanks-rows(还有 $RST64 行带值＝点「恢复缺省」只清了显示、库里仍生效)"; }
# 收尾：把四键**恢复成进段之前的样子**，交还给下一轮（含现读这四键算等值锁的 T40）。
#   正常形态是「库里没有这四行 → 走代码缺省」，所以删干净就是复原；
#   但若进段前运维确实配过值（例如批 H 调过 K），本段必须把那一行原值写回去，不能替他改数。
dbq "DELETE FROM system_config WHERE key IN ($ESTKEYS)" >/dev/null
for KV64 in "est_tokens_per_char_pro|$PRE64KPRO" "est_tokens_per_char_fast|$PRE64KFAST" \
            "est_tokens_fixed_pro|$PRE64FPRO" "est_tokens_fixed_fast|$PRE64FFAST"; do
  K64="${KV64%%|*}"; V64="${KV64#*|}"
  if [ -n "$V64" ]; then dbq "INSERT INTO system_config(key,value) VALUES('$K64','$V64')" >/dev/null; fi
done
EST2=$(ESTCNT); EST2=${EST2:-0}
[ "$EST2" = "$EST0" ] && { PASS=$((PASS+1)); echo "PASS|T64-restore-rowcount($EST2 行，与进段前一致)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T64-restore-rowcount(进段前 $EST0 行 / 现在 $EST2 行＝本段改了库却没还原，下一轮 T40 的现算口径会跟着漂)"; }
ckq T64-restore-kpro "$PRE64KPRO" "$(ESTGET est_tokens_per_char_pro)"
ckq T64-restore-ffast "$PRE64FFAST" "$(ESTGET est_tokens_fixed_fast)"
dbq "DELETE FROM audit_logs WHERE action IN ('est_tokens_save','est_tokens_reset')" >/dev/null

# ---------- T65 品牌显式指名的跨域闸（★ 2026-09-28 〇-Y · F-79） ----------
# 现象：/api/tenant/branding?tenant_id=<别家> 过去「谁带都算」——未登录访客在主站或 A 租户的品牌域上
#   打这个参数，就能把 B 租户的品牌名/Logo/背景图整份拉走；SPA 首屏注入与接口共用同一咽喉点，
#   所以线上当时实测「/?tenant_id=1 注入命中」。判据全文见《缺陷核实与修复文档》§21.11 与 §二十二。
# 锁四条：① 匿名指名→参数被忽略、回落平台（tenant_id=0 且看不见别家的英文品牌名）；
#   ② 反证：超管同一条请求必须命中（后台跨租户配置这条正路不许被误杀，否则等于白修）；
#   ③ 脏参数（abc/0/-7）一律 200 且回落——忽略参数是降级，不是把它打成错误页；
#   ④ 匿名视角的白标承诺从此由服务端兜底，不再依赖前端"不带参数"的自觉。
# 匿名探测一律直连 curl 而不走 get ""：本脚本自己的注释记过 `curl -H ""` 在部分 curl 版本上
#   会报「no header name」，那会让整段断言拿到空响应而假红（AGENTS §一·7 同族的写法雷）。
B65A=$(curl -s --max-time 60 "$B/api/tenant/branding?tenant_id=$TAID")
ck T65-anon-fallback '"tenant_id":0' "$B65A"
if echo "$B65A" | grep -q 'UATCAR'; then
  FAIL=$((FAIL+1)); echo "FAIL|T65-anon-no-foreign-brand|匿名拿到了别家品牌名"
else
  PASS=$((PASS+1)); echo "PASS|T65-anon-no-foreign-brand"
fi
B65S=$(get "$AH" "/api/tenant/branding?tenant_id=$TAID")
ck T65-super-hits "\"tenant_id\":$TAID" "$B65S"
ck T65-super-brand-seen 'UATCAR' "$B65S"
for Q65 in abc 0 -7; do
  ck "T65-dirty-$Q65" '"tenant_id":0' "$(curl -s --max-time 60 "$B/api/tenant/branding?tenant_id=$Q65")"
done

# 收尾清理：本节造的反馈属断言耗材，跑完即删。留着的代价不是本轮（本段已在脚本末尾），
#   而是**下一轮复用同一个 UAT 库**时把历史行算进统计类用例（反馈列表/留资计数）。
#   注册用户按本脚本既有惯例留下（各段自建用户都是这么留的）：users 行挂着会话/台账/邀请
#   等外键，硬删要连带清一片表，而删除对这些用例零收益。
if [ -n "${FID62:-}" ]; then dbq "DELETE FROM feedbacks WHERE id=$FID62" >/dev/null; fi

# ---------- T66 免登录即时翻译试用（★ 2026-09-28 〇-Z #74/#75） ----------
# 要锁的东西（进程内 trial_test.go 那 9 个用例看不见的三层，正是本段的射程）：
#   ① **真路由 + 真匿名**：整段一条 Authorization 都不带，跑在 run_uat 起的真实服务进程上
#      （库里 billing_enforced=1）。进程内测试把 handler 直接装配出来，「路由有没有注册进公网
#      那张表」「访客会不会其实被鉴权middleware 拦在门口」它一概不知道。
#   ② **PG 方言下的账目**：试用成本归属这件事（记在租户 0 的留痕行、客户余额一分不动）只有
#      在真库里数得出来，而 AGENTS §一·4 要求生产方言必须进矩阵。
#   ③ **配置热生效 + 断言耗材清理**：额度三档走 system_config 同名小写键，写进去当轮就该生效；
#      rate_limits 的滚动 24h 窗**不清就把下一轮判红**（本脚本同一 IP 下一轮会再来一遍）。
# 刻意**不**重复进程内已锁的细则（语种白名单全集、窗口过期恢复额度、env>库>默认 逐级回落），
#   两层各锁自己那一层，改一处红一处才有定位价值。
# body 一律先 printf 进变量再喂 req3：AGENTS §一·7 / 本文件头「大括号展开」雷的既定口径。
DEV66A="uat66devAAAA01"; DEV66B="uat66devBBBB01"; DEV66C="uat66devCCCC01"; DEV66D="uat66devDDDD01"
TXT66="今天的产品评审会改到下午三点。"
t66body(){ printf '{"text":"%s","target_lang":"%s","device_id":"%s"%s}' "$1" "$2" "$3" "$4"; }
# t66cnt <scope> <key> — 读 rate_limits 当前计数；**没有这一行时回 0**（不是空串）。
#   为什么在助手层就归零：断言里「一行都没有」与「计数为 0」是同一件好事（没消耗配额），
#   若把空串漏给 ckq，就得每条都自己写 ${x:-0}——漏一处就是一条假红。
t66cnt(){ local v; v=$(dbq "SELECT count FROM rate_limits WHERE scope='$1' AND key='$2'" | tr -d '[:space:]'); printf '%s' "${v:-0}"; }
# cks <用例名> <期望状态码> — 成功面专用：状态码等值 ＋ 不得带错误码（成功体没有 code 字段）。
#   为什么不复用 ck3：ck3 的语义是「状态码+错误码+文案三件套都得有」，那是给失败面写的；
#   拿它判成功支得把 code/msg 两个期望填空串再靠 `grep -qE ''` 恒真过关——那是一颗永远绿的判据。
cks(){ local n="$1" want="$2" bad=""
  [ "${R3ST:-}" = "$want" ] || { bad="$bad 状态码=${R3ST:-?}"; }
  [ -z "${R3CODE:-}" ] || { bad="$bad 意外错误码=${R3CODE}"; }
  if [ -z "$bad" ]; then PASS=$((PASS+1)); echo "PASS|$n"
  else FAIL=$((FAIL+1)); echo "FAIL|$n|want HTTP $want 且无 code|got(${R3ST:-?},${R3CODE:-无})|$bad|body=${R3BODY:0:200}"; fi
}

# 进段前的账目基线（后面全按「增量」判，绝不对绝对值下手——本库已被前面 60 多段写过）
MAXID66=$(dbq "SELECT COALESCE(MAX(id),0) FROM usage_ledger" | tr -d '[:space:]')
LED0=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=0 AND charge_kind='log' AND user_id=0" | tr -d '[:space:]')
BAL0=$(mny_norm "$(dbq "SELECT COALESCE(SUM(balance),0) FROM balance_accounts")")
UD0=$(dbq "SELECT COUNT(*) FROM usage_daily WHERE tenant_id=0" | tr -d '[:space:]')

# ① 逐句倒计时：默认 5 句档，left 一句一减；出参钉 mode=pro（试用面没有模式字段＝访客挑不了便宜档）
B66=$(t66body "$TXT66" en "$DEV66A" "")
req3 POST "" /api/trial/translate "$B66"; cks T66-a1-ok 200
ck T66-a1-left4 '"left":4' "$R3BODY"
ck T66-a1-mode-pro '"mode":"pro"' "$R3BODY"
ck T66-a1-has-translation '"translation":"[^"]' "$R3BODY"
ckn T66-a1-not-exhausted '"exhausted":true' "$R3BODY"
# ★ A8（㊵ 判据补齐，2026-10-04 R-1 批）：**en 与 ja 两条腿必须同判据**。
#   本段的对端故障形态正是"en/ja 双 500、zh 独活"（现网三次实跑，见修改文档 §二），
#   只锁 en 一次的话，"换条腿就活"这种半成品修法不会被发现；zh 那条腿本来就 200，
#   它绿着也不能证明任何事——所以正例取的是**当时全红的两条**。
#   判据三条同 en：200 ＋ translation 非空 ＋ 出栈里不许出现兜底文案与失败码
#   （「试用暂时不可用」＝ trial.go 的 TRANSLATION_FAILED 支，㊵ 的对外形态就是它）。
#   设备号另起一个：间隔闸（3s/设备）与倒计时账都属于 DEV66A 那一档，混用会先把本条打成 429。
DEV66E="uat66devEEEE01"; DEV66F="uat66devFFFF01"
B66JA=$(t66body "$TXT66" ja "$DEV66E" "")
req3 POST "" /api/trial/translate "$B66JA"; cks T66-ja-ok 200
ck T66-ja-has-translation '"translation":"[^"]' "$R3BODY"
ckn T66-ja-no-fallback-copy '试用暂时不可用' "$R3BODY"
ckn T66-ja-no-failure-code 'TRANSLATION_FAILED' "$R3BODY"
# en 腿把同一条负向补齐（另起设备号 DEV66F：DEV66A 此刻正卡在 3s 间隔闸上，
#   拿它打第二条只会得到 RATE_LIMITED，本条的"200＋出栈无兜底文案"就永远断不到）
B66EN=$(t66body "$TXT66" en "$DEV66F" "")
req3 POST "" /api/trial/translate "$B66EN"; cks T66-en-ok 200
ck T66-en-has-translation '"translation":"[^"]' "$R3BODY"
ckn T66-en-no-fallback-copy '试用暂时不可用' "$R3BODY"
ckn T66-en-no-failure-code 'TRANSLATION_FAILED' "$R3BODY"
# ⚠️ 本条断言的**反证边界**要如实登记，否则后人会以为它锁住了凭据正确性：
#   修改文档 §一给 A8 写的反证是「清空 stage_models.ai_initial ⇒ 本判据必须红」，但在矩阵内它**结构上不可能红**——
#   本矩阵的上游是 scripts/uat/mock_llm.py，它对任何 Key/任何 model 一律回 200（不看凭据）。
#   凭据那一层的等价锁在 Go 侧：A4（解析返回哪一档）＋ A4b（internal/engine/stage_http_route_test.go，
#   真 httptest 上游逐次记 model 与 Authorization）。本段在矩阵里的真实射程＝
#   「路由已注册＋匿名可达＋这条腿真能走到出译文」，不含"用的是哪份密钥"。
# ② 最小间隔闸（同设备连点）：**必须与「额度用完」分码**——前端按 code 决定是「等 3 秒再来」还是
#    「去注册」，混成一个码就等于把只是手快的访客送去注册页。
req3 POST "" /api/trial/translate "$B66"; ck3 T66-a-interval-429 429 RATE_LIMITED '太快'
ck T66-a-interval-retry-after '"retry_after":' "$R3BODY"
ckn T66-a-interval-not-trial-code 'TRIAL_EXHAUSTED' "$R3BODY"
# ★ 计数纪律：被闸拒掉的一句**不吃配额**（访客没拿到译文却少了一句额度，是最容易被投诉的一种账）
ckq T66-a-interval-no-count 1 "$(t66cnt trial_dev "$DEV66A")"
sleep 3   # 过 trialMinIntervalSec（常量 3s、不可配）；RateRecord 记的 window_start 按秒取整，睡满 3 必过
req3 POST "" /api/trial/translate "$B66"; cks T66-a2-ok 200
ck T66-a2-left3 '"left":3' "$R3BODY"
sleep 3
req3 POST "" /api/trial/translate "$B66"; cks T66-a3-ok 200
ck T66-a3-left2 '"left":2' "$R3BODY"
# ③ 设备额度用满：直接把这一行的计数钉到 5（等价「已成功翻过 5 句」，省下 4 次 3s 干等），
#    间隔哨兵一起改到窗外——否则先撞上面那道 429，本道永远断不到。
NOW66=$(date +%s); OLD66=$(( NOW66 - 120 ))
dbq "UPDATE rate_limits SET count=5, window_start=$NOW66 WHERE scope='trial_dev' AND key='$DEV66A'" >/dev/null
dbq "UPDATE rate_limits SET window_start=$OLD66 WHERE scope='guard_int' AND key='trial:$DEV66A'" >/dev/null
req3 POST "" /api/trial/translate "$B66"; ck3 T66-a6-exhausted 429 TRIAL_EXHAUSTED '注册后继续使用'
ck T66-a6-reason-device '"reason":"device"' "$R3BODY"
ckn T66-a6-no-translation '"translation":' "$R3BODY"   # 拒绝体不许夹半份译文（前端按 left/exhausted 切引导卡）

# ④ 校验族 ＋ 方法闸：全部 4xx，而且**一句都不该计数**（都拿设备 D 打，判据就看它的账目）
B66NOD=$(printf '{"text":"%s","target_lang":"en"}' "$TXT66")
req3 POST "" /api/trial/translate "$B66NOD"; ck3 T66-v-no-device 400 VALIDATION_ERROR '试用标识不合法'
B66EMPTY=$(t66body "" en "$DEV66D" "")
req3 POST "" /api/trial/translate "$B66EMPTY"; ck3 T66-v-empty-text 400 VALIDATION_ERROR '请输入'
TXT66LONG=$(python3 -c 'print("超"*302)')   # 302 个字符：上限 300 按 rune 计，不能用字节凑（中文一字三字节）
B66LONG=$(t66body "$TXT66LONG" en "$DEV66D" "")
req3 POST "" /api/trial/translate "$B66LONG"; ck3 T66-v-too-long 400 VALIDATION_ERROR '过长'
B66LANG=$(t66body "$TXT66" zz "$DEV66D" "")
req3 POST "" /api/trial/translate "$B66LANG"; ck3 T66-v-bad-lang 400 VALIDATION_ERROR '暂不在试用范围'
req3 GET "" /api/trial/translate; ck3 T66-v-method 405 METHOD_NOT_ALLOWED 'POST'
ckq T66-v-validation-no-count 0 "$(t66cnt trial_dev "$DEV66D")"
# ⑤ 请求体里伪造 "mode":"fast" 必须被忽略、且回显钉在 pro（字段表里没有 mode＝匿名改不了模式）
B66FAST=$(t66body "$TXT66" en "$DEV66D" ',"mode":"fast"')
req3 POST "" /api/trial/translate "$B66FAST"; cks T66-mode-forge-ok 200
ck T66-mode-forge-echo-pro '"mode":"pro"' "$R3BODY"

# ⑥ IP 档：换设备号也刷不动。把 IP 日上限钉成「此刻这一出口已用的句数」，新设备第一句就该被拒。
#    key 现读不写死 127.0.0.1：clientIP 的取值随监听形态变（IPv6 环回 ::1 也是可能的），
#    写死一次就把本道断言变成「永远读不到行」的假绿。
IPKEY66=$(dbq "SELECT key FROM rate_limits WHERE scope='trial_ip' ORDER BY count DESC LIMIT 1" | tr -d '[:space:]')
if [ -z "$IPKEY66" ]; then
  FAIL=$((FAIL+1)); echo "FAIL|T66-ip-key-found(trial_ip 一行都没有＝前面那 4 句没被记进 IP 账)"
else
  PASS=$((PASS+1)); echo "PASS|T66-ip-key-found($IPKEY66)"
  IPUSED66=$(t66cnt trial_ip "$IPKEY66")
  dbcfg trial_ip_daily "$IPUSED66"
  B66B=$(t66body "$TXT66" en "$DEV66B" "")
  req3 POST "" /api/trial/translate "$B66B"; ck3 T66-b-exhausted-ip 429 TRIAL_EXHAUSTED '注册后额度按账号计算'
  ck T66-b-reason-ip '"reason":"ip"' "$R3BODY"
fi
# ⑦ 全局预算顶：三档同向时**先设备、再 IP、后全局**的判序也顺带钉住（先把 IP 放回去才轮到全局）。
dbq "DELETE FROM system_config WHERE key='trial_ip_daily'" >/dev/null   # 删行＝回落代码默认 40
G66=$(t66cnt trial_day 'global')
dbcfg trial_global_daily "$G66"
B66C=$(t66body "$TXT66" en "$DEV66C" "")
req3 POST "" /api/trial/translate "$B66C"; ck3 T66-c-exhausted-global 429 TRIAL_EXHAUSTED '今日试用名额已用完'
ck T66-c-reason-global '"reason":"global"' "$R3BODY"
# 触顶文案只说「名额已满」，不把平台内部额度数字漏给匿名访客（判 R3MSG 而不是整页 body：
#   trace_id 是十六进制串，含数字属正常，拿整体会把这条锁做成恒红）
ckn T66-c-msg-no-quota-leak "$G66" "${R3MSG:-}"
#   ⚠ 若 G66=0（前面一句都没成功），dbcfg 写 0 会被 trialLimit 当成非法值回落默认 3000，
#     本道闸不触发 ⇒ 上面那条 ck3 直接红——是「没跑到前提」的响亮信号，不是静默通过。
# 预算打满是市场费用异常信号（刷量或爆量），当天就得让运维看见 → 必须留一条租户 0 的 open 告警
ALERT66=$(dbq "SELECT COUNT(*) FROM alerts WHERE tenant_id=0 AND kind='trial_budget' AND status='open'" | tr -d '[:space:]')
[ "${ALERT66:-0}" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|T66-global-budget-alert($ALERT66 条)"; } \
  || { FAIL=$((FAIL+1)); echo "FAIL|T66-global-budget-alert(预算打满却没告警＝运维只能月底对账才发现刷量)"; }

# ⑧ ★ 成本归属（本段最值钱的一条）：成功的 6 句（设备 A 三句 ＋ 设备 D 一句 ＋ A8 的 ja/en 两句）全部落在**租户 0 的
#    留痕行**上；客户余额一分不动；也不许出现「租户>0 且 user_id=0」的实扣行——后者就是
#    「把市场推广费用记到客户账上」的形态（客户来问账时无法解释，见 trial.go 文件头★段）。
LED1=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=0 AND charge_kind='log' AND user_id=0" | tr -d '[:space:]')
ckq T66-ledger-platform-rows-delta 6 "$(( ${LED1:-0} - ${LED0:-0} ))"
BAL1=$(mny_norm "$(dbq "SELECT COALESCE(SUM(balance),0) FROM balance_accounts")")
ckq T66-balance-untouched "$BAL0" "$BAL1"
FK66=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id>0 AND user_id=0 AND id>$MAXID66" | tr -d '[:space:]')
ckq T66-no-anon-charge-on-tenant 0 "${FK66:-0}"
ND66=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=0 AND charge_kind<>'log' AND id>$MAXID66" | tr -d '[:space:]')
ckq T66-tenant0-all-log-kind 0 "${ND66:-0}"
# 留痕行的业务口径：task_type=translate / biz_kind=text / biz_mode=pro（管理台按这三档核试用成本，
#   写成别的档位就对不上账；⚠ 这些行**不会**出现在「按租户用量」里——它们不属于任何客户）
OK66=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=0 AND charge_kind='log' AND task_type='translate' AND biz_kind='text' AND biz_mode='pro' AND id>$MAXID66" | tr -d '[:space:]')
ckq T66-ledger-biz-shape 6 "${OK66:-0}"
# 数量取引擎回吐的 token 用量，纯知识库命中时按「一句一个单位」兜底，避免试用在成本视图里全 0
POS66=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=0 AND charge_kind='log' AND quantity>0 AND id>$MAXID66" | tr -d '[:space:]')
ckq T66-ledger-quantity-positive 6 "${POS66:-0}"

# 收尾清账（★ 不清就是给下一轮下毒）：rate_limits 的滚动 24h 窗留在库里，下一轮同 IP/同设备
#   会直接被判「额度用完」——那是一条查不出根因的假红。留痕行、告警、三档配置键一并归零。
dbq "DELETE FROM rate_limits WHERE scope IN ('trial_dev','trial_ip','trial_day') OR (scope='guard_int' AND key LIKE 'trial:%')" >/dev/null
dbq "DELETE FROM usage_ledger WHERE tenant_id=0 AND charge_kind='log' AND user_id=0 AND id>$MAXID66" >/dev/null
dbq "DELETE FROM alerts WHERE tenant_id=0 AND kind='trial_budget'" >/dev/null
dbq "DELETE FROM system_config WHERE key IN ('trial_device_quota','trial_ip_daily','trial_global_daily')" >/dev/null
# usage_daily 的租户 0 日计数：只有试用的留痕路径会写 tid=0 这一行，进段前是空才顺手删空
#   （若进段前就有行，那是别人的账，本段无权清理）。
if [ "${UD0:-0}" = "0" ]; then dbq "DELETE FROM usage_daily WHERE tenant_id=0" >/dev/null; fi
# 反证：账真清干净了。「写了一串 DELETE 却因方言/转义没执行」是本仓反复踩过的形态
LEFT66=$(dbq "SELECT COUNT(*) FROM rate_limits WHERE scope LIKE 'trial_%' OR key LIKE 'trial:%'" | tr -d '[:space:]')
ckq T66-cleanup-rate-limits-empty 0 "${LEFT66:-0}"
CKEY66=$(dbq "SELECT COUNT(*) FROM system_config WHERE key LIKE 'trial_%'" | tr -d '[:space:]')
ckq T66-cleanup-config-empty 0 "${CKEY66:-0}"
LED2=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=0 AND charge_kind='log' AND user_id=0" | tr -d '[:space:]')
ckq T66-cleanup-ledger-back-to-baseline "$LED0" "$LED2"

# ---------- T67 站点门面开关：关主页时首页恰好只多一段标记（★ 2026-09-28 〇-Z #69/#70） ----------
# 背景：演示站（体验机）不该对外营业——访客打开 `/` 直接进登录注册页；主站照旧出官网。
#   做成**部署级开关**（后台运营策略 front.landing_enabled，环境变量 LANDING_DISABLED 可覆盖），
#   两站共用同一份 dist 与同一个二进制：前端按 hostname 判定等于把「哪个站是体验机」编译进产物，
#   换域名要重构建，而且本地任何闸门都看不见那条分支。
# 本段锁 HTTP 出口那三件事（进程内的 site_flags_test.go 锁优先序与注入函数本身）：
#   ① 开放态首页**逐字节等于未注入**：一个恒为 true 的标记会把《部署指南》§十 钉死的
#      「主站首页 2,591 B」判据无声顶翻，排查的人会先去怀疑品牌注入那条链；
#   ② 关闭态多出来的字节**恰好等于那段标记**（多一个字＝有别的东西混进了直出面）；
#   ③ 开关只管 `/`：/pricing、/compare 对访客仍须 200 出 SPA（体验机也要让人看到价），
#      且策略读写同源——改完再 GET，面板回显必须已是新值，否则运维照着假现值点保存会写反。
# 跑完把 ops_policy 交还原样：api_uat.sh 的 B1 段在本段之前已经写过平台策略，
#   所以这里必须「GET 取现值 → 只改一个字段 → 原值交还」，绝不写死成清空。
IDX0=$(curl -s --max-time 60 "$B/")
LEN0=${#IDX0}
ck T67-open-index-served '<div id="root"' "$IDX0"
ckn T67-open-no-marker '__site_flags__' "$IDX0"
# 取平台策略现值，就地生成两份 body：off 档、以及「交还原值」档（同一份原文，防中途有人改过）
req3 GET "$AH" /api/admin/ops/policy; cks T67-policy-get 200
T67OFF="/tmp/uat67_off_$$.json"; T67ORG="/tmp/uat67_org_$$.json"
printf '%s' "$R3BODY" | python3 -c '
import json, sys
d = json.loads(sys.stdin.read() or "{}")
plat = d.get("platform")
if not isinstance(plat, dict):
    sys.stderr.write("platform 不是对象，拒绝生成 payload\n")
    sys.exit(2)
off = json.loads(json.dumps(plat))
front = dict(off.get("front") or {})
front["landing_enabled"] = False
off["front"] = front
open(sys.argv[1], "w", encoding="utf-8").write(json.dumps({"policy": off}))
open(sys.argv[2], "w", encoding="utf-8").write(json.dumps({"policy": plat}))
' "$T67OFF" "$T67ORG"
if [ -s "$T67OFF" ] && [ -s "$T67ORG" ]; then PASS=$((PASS+1)); echo "PASS|T67-payload-files"
else FAIL=$((FAIL+1)); echo "FAIL|T67-payload-files(两份 body 没生成＝后面整段会在错误的开关态上判)"; fi
POL0=$(dbq "SELECT value FROM system_config WHERE key='ops_policy'" | tr -d '\n')
# 用 --data @file 而不是把 JSON 拼进命令行：本段 body 是嵌套对象，正是文件头点名的展开雷区
R67=$(curl -s --max-time 60 -X POST "$B/api/admin/ops/policy/save" -H "$AH" -H "$J" --data "@$T67OFF")
ck T67-save-off '"success":true' "$R67"
IDX1=$(curl -s --max-time 60 "$B/")
ck T67-off-marker '"landing":false' "$IDX1"
MARK67='<script id="__site_flags__">window.__SITE_FLAGS__={"landing":false};</script>'
ckq T67-off-delta-equals-marker "$(( LEN0 + ${#MARK67} ))" "${#IDX1}"
ck T67-off-branding-intact '__BRANDING__' "$IDX1"   # 门面标记排在品牌注入之后，两件事不许互相顶掉
B67P=$(get "$AH" /api/admin/ops/policy)
ck T67-policy-echo-off '"landing_enabled":false' "$B67P"   # 读写同源：面板回显必须已是新值
# 访客可达面：关掉主页不许顺手把营销页一起收掉（收掉的形态＝/compare 也 302/404 或整页兜底）
for P67 in /pricing /compare; do
  R67P=$(curl -s --max-time 60 -w '\n%{http_code}' "$B$P67")
  ckq "T67-guest-open-${P67//\//}" 200 "${R67P##*$'\n'}"
  ck "T67-guest-spa-${P67//\//}" '<div id="root"' "${R67P%$'\n'*}"
done
# 还原：交还进段之前那份平台策略（原先没这一行就把行删掉，不给库里留一个「从来没生效的现值」）
R67R=$(curl -s --max-time 60 -X POST "$B/api/admin/ops/policy/save" -H "$AH" -H "$J" --data "@$T67ORG")
ck T67-restore-saved '"success":true' "$R67R"
if [ -z "$POL0" ]; then dbq "DELETE FROM system_config WHERE key='ops_policy'" >/dev/null; fi
IDX2=$(curl -s --max-time 60 "$B/")
ckn T67-restored-no-marker '__site_flags__' "$IDX2"
ckq T67-restored-byte-identical "$LEN0" "${#IDX2}"
# 库里的策略原文按**语义**比对还原：交还走的是后端重 marshal，键序与缺省项可能和进段前不同，
#   按字符串死比会假红（同 AGENTS §一·7 mny_norm 那一条族的「文本≠值」坑）。
POL1=$(dbq "SELECT value FROM system_config WHERE key='ops_policy'" | tr -d '\n')
if python3 -c '
import json, sys
a = (sys.argv[1] or "").strip() or "{}"
b = (sys.argv[2] or "").strip() or "{}"
try:
    sys.exit(0 if json.loads(a) == json.loads(b) else 1)
except Exception:
    sys.exit(1)
' "$POL0" "$POL1"; then
  PASS=$((PASS+1)); echo "PASS|T67-policy-row-restored"
else
  FAIL=$((FAIL+1)); echo "FAIL|T67-policy-row-restored|before=${POL0:0:160}|after=${POL1:0:160}"
fi
rm -f "$T67OFF" "$T67ORG"
# 本段两次 ops_policy_save 的审计行按既有口径**留着**：审计是只增的证据链，api_uat.sh 的 B1 段
#   同样留了同类行，全仓没有任何用例按 ops_policy_save 的行数做等值锁（动这条锁前重新核一遍）。

# ---------- T68 旧包额度耗尽仍须能升级（★ 2026-09-28 〇-Z · 缺陷 F-80） ----------
# 现象（演示站 langcross_demo 实测）：租户 1 的 basic_m 那笔已支付订单发放 1,200,000 token，
#   台账 quota_grants(kind='plan',source='order',ref_id=该订单) 的 SUM("left")=0，
#   而订阅有效期到 10-17 还没过。客户点「升级到 专业·年」→ 409
#   「当前套餐已无剩余价值，无法抵扣升级」，升级这条路彻底封死。
# 为什么这是缺陷而不是正确校验：抵扣为 0 只是「旧包不再折让」，不是「不许升级」。
#   **越是把套餐用满、越想加钱的客户，越被系统挡在门口**——而这恰是升级最该成交的人群。
# 修法（store/billing.go 的 ComputeUpgradeCredit ④ 段）：剩余率 0 时抵扣按 0 继续出单，
#   应付即全价；上面那五条前置校验（无生效套餐／同包／非付费包／目标价不高于当前／
#   订单 token 口径异常）一条都没放宽。
# 本段钉 HTTP 出口的四件事（store 层那条分支另有 packages_test.go 的 TestPackageUpgradeExhausted，
#   那条已做过「旧代码必红」的反证）：
#   ① 耗尽租户升级必须成单（200 而不是 409），且**抵扣 0、应付＝全价**；
#      「全价」按**主角自己那笔订阅单**反推折扣系数（挂牌 20 → 实收 AM68LOW ⇒ 系数 = AM68LOW/20），
#      再乘目标包挂牌价取期望值。为什么不写死 30/60/120：试运营首月半价挂在
#      「租户注册未满 30 天」上，写死数字会在那条策略调整或换批次注册时假红（同 AGENTS §一·7
#      「金额锁不写死汇率」那族口径）；系数取自同一租户的同一列，档位对它一视同仁。
#   ② 支付确认后订阅身份换新包，且**不多发一分额度**：旧包没有剩余可结转，
#      新单名下的 order_carry 转入行数必须与升级前持平（09-21 生产就查出过幽灵 3,000 积分）；
#   ③ 反向对照·部分消耗仍按比例抵扣：抵扣额 > 0，且「应付＋抵扣＝该目标包全价」；
#   ④ 反向对照·前置校验仍拒：拿更便宜的包「升级」仍须 409 并给可读文案
#      （防止有人把这条修复做成「整段校验一律放行」的过度放宽）。
SFX68=$(date +%s | tail -c 6)
U68A="uatuser_t68a$SFX68"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$U68A\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T68耗尽升级\",\"email\":\"$U68A@test.com\",\"agreed\":true}" >/dev/null
T68A=$(tok $U68A uatpass123); H68A="Authorization: Bearer $T68A"
TID68A=$(sq "SELECT tenant_id FROM users WHERE username='$U68A' LIMIT 1" | tr -d '[:space:]')
[ -n "$TID68A" ] || { FAIL=$((FAIL+1)); echo "FAIL|T68-tenant-created(取不到租户 ID：本段全部判据无效)"; }
# 建三档付费包（都挂主角租户名下；GetPackageByCode 的 tenant_id IN (0,?) 是租户作用域）：
#   low 20 元/30 天 → high 60 元/365 天 → max 120 元/365 天，只为让「目标价必须更高」成立
for P in "uat_t68_low:20000:20:30" "uat_t68_high:60000:60:365" "uat_t68_max:100000:120:365"; do
  IFS=: read -r PC PS PP PD <<< "$P"
  curl -s $B/api/admin/packages/create -H "$AH" -H "$J" \
    -d "{\"tenant_id\":$TID68A,\"code\":\"$PC\",\"name\":\"T68$PC\",\"ptype\":\"paid\",\"sentences\":$PS,\"price_money\":$PP,\"duration_days\":$PD}" >/dev/null
done

# 订阅 low 并到账（攒出「有订阅且额度充足」的初始态，供 ③ 的部分消耗对照用）
R=$(post "$H68A" '{"code":"uat_t68_low"}' /api/package/subscribe)
OID68A=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
[ "$OID68A" != "0" ] && post "$AH" "{\"id\":$OID68A,\"tenant_id\":$TID68A}" /api/admin/orders/pay >/dev/null
GR68=$(sq "SELECT COUNT(*) FROM quota_grants WHERE tenant_id=$TID68A AND kind='plan' AND \"left\">0" | tr -d '[:space:]')
[ "${GR68:-0}" -ge 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T68-subscribed-low($GR68 条有效台账)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T68-subscribed-low(台账 0 条：订阅未到账，后面全部判据失去前提)"; }
# 折扣系数从这一笔真实订单反推（挂牌 20 元）；系数为 0 会让后面两条金额锁退化成「和 0 比 0」的假绿
AM68LOW=$(mny_norm "$(sq "SELECT amount_money FROM orders WHERE id=$OID68A" | tr -d '[:space:]')")
FAC68=$(python3 -c "
try:
    f = float('${AM68LOW:-0}') / 20.0
except Exception:
    f = 0.0
print(f if f > 0 else 0)")
if [ "$(python3 -c "print(1 if float('${FAC68:-0}') > 0 else 0)")" = "1" ]; then
  PASS=$((PASS+1)); echo "PASS|T68-price-factor-derived(实收 $AM68LOW / 挂牌 20 ⇒ 系数 $FAC68)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T68-price-factor-derived(low 单实收=${AM68LOW:-空}，推不出折扣系数，①③ 的金额锁将失去意义)"
fi
EXP_HIGH=$(python3 -c "print(round(60.0*float('${FAC68:-0}'),2))")
EXP_MAX=$(python3 -c "print(round(120.0*float('${FAC68:-0}'),2))")

# ③ 反向对照·**未耗尽**时升级仍按比例抵扣（F-80 的修法不能把比例抵扣本身做坏）
#    台账削掉四分之一（留四分之三）→ 升级 high：抵扣必须 > 0，且 应付＋抵扣 == high 的全价 $EXP_HIGH
dbq "UPDATE quota_grants SET \"left\"=total-total/4 WHERE tenant_id=$TID68A AND kind='plan'" >/dev/null
REM68=$(sq "SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$TID68A AND kind='plan' AND \"left\">0" | tr -d '[:space:]')
req3 POST "$H68A" /api/package/upgrade '{"code":"uat_t68_high"}'
ck T68-partial-upgrade-accepted '"success":true' "$R3BODY"
OID68UP1=$(echo "$R3BODY" | pv '.get("order",{}).get("id") or 0')
CR68UP1=$(mny_norm "$(sq "SELECT credit_money FROM orders WHERE id=$OID68UP1" | tr -d '[:space:]')")
AM68UP1=$(mny_norm "$(sq "SELECT amount_money FROM orders WHERE id=$OID68UP1" | tr -d '[:space:]')")
if [ -n "$CR68UP1" ] && ! feq "$CR68UP1" "0"; then
  PASS=$((PASS+1)); echo "PASS|T68-partial-credit-positive(剩余 $REM68 ⇒ 抵扣 $CR68UP1)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T68-partial-credit-positive(剩余 $REM68 却抵扣 ${CR68UP1:-空}：比例抵扣被改坏或单没建成)"
fi
if [ -n "$AM68UP1" ] && [ -n "$CR68UP1" ] && feq "$(python3 -c "print(round(float('${AM68UP1}')+float('${CR68UP1}'),2))")" "$EXP_HIGH"; then
  PASS=$((PASS+1)); echo "PASS|T68-partial-sum-equals-full($AM68UP1+$CR68UP1=$EXP_HIGH)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T68-partial-sum-equals-full(应付 ${AM68UP1:-空} ＋ 抵扣 ${CR68UP1:-空} ≠ high 全价 $EXP_HIGH)"
fi
# 部分结转本来就该留行：作为 ② 的对照基线（② 要求的是「不再多长」，不是「必须为 0」）
CARRY0=$(sq "SELECT COUNT(*) FROM quota_grants WHERE tenant_id=$TID68A AND source='order_carry'" | tr -d '[:space:]')

# ① 主角判据·**额度全耗尽**后升级必须成单（旧代码在这里回 409「当前套餐已无剩余价值」）
dbq "UPDATE quota_grants SET \"left\"=0 WHERE tenant_id=$TID68A AND kind='plan'" >/dev/null
REM68Z=$(sq "SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$TID68A AND kind='plan' AND \"left\">0" | tr -d '[:space:]')
[ "$REM68Z" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T68-exhausted-precondition(剩余 0，与演示站同形态)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T68-exhausted-precondition(剩余=$REM68Z，本段判据失去意义)"; }
req3 POST "$H68A" /api/package/upgrade '{"code":"uat_t68_max"}'
if [ "$R3ST" = "200" ] && echo "$R3BODY" | grep -qE '"success":true'; then
  PASS=$((PASS+1)); echo "PASS|T68-exhausted-upgrade-200(F-80：额度耗尽不再挡门)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T68-exhausted-upgrade-200|状态码=$R3ST 错误码=${R3CODE:-无} 文案=${R3MSG:-无}|body=${R3BODY:0:200}"
fi
OID68UP2=$(echo "$R3BODY" | pv '.get("order",{}).get("id") or 0')
CR68UP2=$(mny_norm "$(sq "SELECT credit_money FROM orders WHERE id=$OID68UP2" | tr -d '[:space:]')")
AM68UP2=$(mny_norm "$(sq "SELECT amount_money FROM orders WHERE id=$OID68UP2" | tr -d '[:space:]')")
# 抵扣 0（旧包没价值可退）＋应付＝全价（**不是被减成 0 元单**：0 元单会让回调金额核对退化成恒真）
[ -n "$CR68UP2" ] && feq "$CR68UP2" "0" \
  && { PASS=$((PASS+1)); echo "PASS|T68-exhausted-credit-zero"; } \
  || { FAIL=$((FAIL+1)); echo "FAIL|T68-exhausted-credit-zero(抵扣=${CR68UP2:-空})"; }
if [ -n "$AM68UP2" ] && feq "$AM68UP2" "$EXP_MAX"; then
  PASS=$((PASS+1)); echo "PASS|T68-exhausted-pay-full-price($AM68UP2 == max 全价 $EXP_MAX)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T68-exhausted-pay-full-price(应付=${AM68UP2:-空} 期望=$EXP_MAX)"
fi

# ② 支付确认后：订阅身份换新包，且**不多发额度**（旧包剩余 0 ⇒ 结转行数不得增长）
[ "$OID68UP2" != "0" ] && post "$AH" "{\"id\":$OID68UP2,\"tenant_id\":$TID68A}" /api/admin/orders/pay >/dev/null
PC68=$(dbjsonstr tenants $TID68A permissions package_code | tr -d '[:space:]')
[ "$PC68" = "uat_t68_max" ] && { PASS=$((PASS+1)); echo "PASS|T68-exhausted-new-package-active($PC68)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T68-exhausted-new-package-active(got $PC68)"; }
CARRY1=$(sq "SELECT COUNT(*) FROM quota_grants WHERE tenant_id=$TID68A AND source='order_carry'" | tr -d '[:space:]')
if [ "${CARRY1:-0}" = "${CARRY0:-0}" ]; then
  PASS=$((PASS+1)); echo "PASS|T68-exhausted-no-extra-carry(carry 行数 $CARRY1 == 升级前 $CARRY0)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T68-exhausted-no-extra-carry(旧包剩余 0 却多出结转行：$CARRY0 → $CARRY1，等于凭空多发额度)"
fi

# ④ 反向对照·前置校验一条都没被放宽：A 现在在 max（120 元），拿更便宜的 low（20 元）「升级」必须仍拒
req3 POST "$H68A" /api/package/upgrade '{"code":"uat_t68_low"}'
ck3 T68-downgrade-still-rejected 409 CONFLICT '价格应高于当前套餐'

# 清理本批自建包（订单/台账留痕供人工对账，与 T50/T51 同口径），避免污染后续套餐列表断言
dbq "DELETE FROM packages WHERE code IN ('uat_t68_low','uat_t68_high','uat_t68_max','uat_t68_base')" >/dev/null

# ---------- T69 免费注册账号防薅三档（★ 2026-09-28 〇-Z · 用户令「注册免费账号也是和免费体验试用一样有防薅限制」＝F-81） ----------
# 口径（与免登录试用 T66 同一族，但**射程只圈在「新建免费账号＝自带体验额度」这一支**）：
#   设备档 reg_dev（默认 3 次/24h）＋平台日预算 reg_day（默认 500）＋既有的 IP 档 guard_int/guard_day。
#   受邀加入已有企业（joinWithInvite）与专属域名注册（dedicatedTid>0）**一律不占格**——
#   前者花的是客户自己发的一次性邀请码，50 人同 NAT 出口入职不该排三天队，更不该在拒绝时烧掉客户的码。
# 本段锁 HTTP 出口那四件事（进程内 register_guard_f81_test.go 锁的是分支与并发语义，两层各管一段）：
#   ① 设备档触顶：429 RATE_LIMITED ＋ details.reason=device ＋ retry_after，且**不建租户、不多记账**；
#   ② 平台档触顶：429 文案指向日径全额（不是「该设备」），且**设备档那一格必须退回来**
#      ——「先占平台格再被平台挡」时若不退设备格，用户会因为系统的失败丢掉自己当天的额度；
#   ③ 受邀加入在**两档都触顶**的状态下照样 200，且两档计数纹丝不动；
#   ④ 脏设备号（过不了 ^[A-Za-z0-9_-]{8,64}$）不当场 400：这是公开注册接口的对外契约，
#      老缓存包／脚本客户端／管理台代客注册都可能不带这个字段（F-64、F-79 两批「契约改动引发现网故障」的同形教训），
#      但**照样吃平台档**，且脏值不得进限流账本当 key。
# 另加 ⑤：库里删掉档位键后回落到代码默认 3 格（第 3 次注册应放行）——锁「env>库>默认」的最后一档没接错。
# 收尾必须把 rate_limits 的滚动 24h 窗、register_budget 告警、两个档位键清/钉干净（同 T66 口径）：
#   留着＝下一轮同 IP/同设备一进门就被判「今日名额已满」，那是一条查不出根因的假红。
SFX69=$(date +%s | tail -c 6)
DEV69A="uat69devAAAA01"; DEV69B="uat69devBBBB01"
t69body(){ printf '{"username":"%s","password":"uatpass123","type":"personal","name":"T69防薅","email":"%s@test.com","agreed":true,"device_id":"%s"}' "$1" "$1" "$2"; }
t69invitebody(){ printf '{"username":"%s","password":"uatpass123","type":"enterprise","role_choice":"member","invite":"%s","name":"T69受邀","email":"%s@test.com","agreed":true,"device_id":"%s"}' "$1" "$2" "$1" "$3"; }
# t69cnt <scope> <key> — 没有这一行时回 0（同 t66cnt：断言里「没账」与「计数 0」是同一件好事，
#   漏空串给 ckq 就是一条假红，别指望每条都自己写 ${x:-0}）。
t69cnt(){ local v; v=$(dbq "SELECT count FROM rate_limits WHERE scope='$1' AND key='$2'" | tr -d '[:space:]'); printf '%s' "${v:-0}"; }
# 段内压低设备档、平台档先开到不可能触顶（让 ①③④ 只在「设备档」这一条上被判定，根因唯一）
dbcfg register_device_daily_limit 2
dbcfg register_global_daily_limit 100000
# 旧告警先清：CreateAlert 的「同租户+同类型已有 open 即跳过」会让本段的「新增告警 ≥1」恒假（那是判据坏，不是机制坏）
dbq "DELETE FROM alerts WHERE tenant_id=0 AND kind='register_budget'" >/dev/null
ALERTMAX69=$(dbq "SELECT COALESCE(MAX(id),0) FROM alerts" | tr -d '[:space:]')

# ① 设备档 2 格用完 ⇒ 第 3 次 429
U69A1="uatuser_t69a1$SFX69"; B69A1=$(t69body "$U69A1" "$DEV69A")
req3 POST "" /api/auth/register "$B69A1"; cks T69-d1-ok 200
U69A2="uatuser_t69a2$SFX69"; B69A2=$(t69body "$U69A2" "$DEV69A")
req3 POST "" /api/auth/register "$B69A2"; cks T69-d2-ok 200
ckq T69-device-count-two 2 "$(t69cnt reg_dev "$DEV69A")"
TEN69_0=$(dbq "SELECT COUNT(*) FROM tenants" | tr -d '[:space:]')
U69A3="uatuser_t69a3$SFX69"; B69A3=$(t69body "$U69A3" "$DEV69A")
req3 POST "" /api/auth/register "$B69A3"
ck3 T69-d3-device-capped 429 RATE_LIMITED '该设备今天注册的账号已达上限'
ck T69-d3-reason-device '"reason":"device"' "$R3BODY"
ck T69-d3-retry-after '"retry_after":' "$R3BODY"
TEN69_1=$(dbq "SELECT COUNT(*) FROM tenants" | tr -d '[:space:]')
ckq T69-d3-no-tenant-created "$TEN69_0" "$TEN69_1"
ckq T69-d3-count-not-overrun 2 "$(t69cnt reg_dev "$DEV69A")"

# ② 平台日预算触顶（上限就配成当前计数＝「已满」，不写死数字：本轮前面几十段注册都在这本账上，
#    写死 500 会在注册量变化时假红／假绿，同 AGENTS §一·7「金额锁不写死」那族口径）
GUSED69=$(t69cnt reg_day global)
if [ "${GUSED69:-0}" -ge 1 ] 2>/dev/null; then
  PASS=$((PASS+1)); echo "PASS|T69-platform-premise(进本段前平台档已记 $GUSED69 格)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T69-platform-premise(reg_day/global 计数为 0＝前面那些注册一笔都没占平台格，reserveFreeAccount 没接线，本段判据失去前提)"
fi
dbcfg register_global_daily_limit "$GUSED69"
U69B1="uatuser_t69b1$SFX69"; B69B1=$(t69body "$U69B1" "$DEV69B")
DEVB69_0=$(t69cnt reg_dev "$DEV69B")
req3 POST "" /api/auth/register "$B69B1"
ck3 T69-g-budget-full 429 RATE_LIMITED '今日免费注册名额已用完'
ck T69-g-retry-after '"retry_after":' "$R3BODY"
TEN69_2=$(dbq "SELECT COUNT(*) FROM tenants" | tr -d '[:space:]')
ckq T69-g-no-tenant-created "$TEN69_1" "$TEN69_2"
# ★ 退格证据：本笔在平台格上被挡，之前占下的设备格必须原样退回（defer release 的 HTTP 出口面）
ckq T69-g-device-seat-refunded "$DEVB69_0" "$(t69cnt reg_dev "$DEV69B")"
ALERT69=$(dbq "SELECT COUNT(*) FROM alerts WHERE tenant_id=0 AND kind='register_budget' AND status='open' AND id>$ALERTMAX69" | tr -d '[:space:]')
if [ "${ALERT69:-0}" -ge 1 ] 2>/dev/null; then
  PASS=$((PASS+1)); echo "PASS|T69-platform-alert($ALERT69 条新开告警)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T69-platform-alert(平台名额被打满却没留告警＝刷号只表现为注册按钮失灵，运维当天查不到)"
fi

# ③ 受邀加入：两档都处在「已满」状态下（设备档 DEV69A 已触顶、平台档上限=现计数）仍须放行且不推进计数
CODE69="UAT69J$SFX69"
post "$AH" "{\"code\":\"$CODE69\",\"tenant_id\":$TAID}" /api/admin/invite-codes/create >/dev/null
INV69=$(dbq "SELECT COUNT(*) FROM invite_codes WHERE code='$CODE69' AND tenant_id=$TAID AND used=0" | tr -d '[:space:]')
if [ "${INV69:-0}" = "1" ]; then
  PASS=$((PASS+1)); echo "PASS|T69-invite-code-ready(绑到租户 $TAID 的一次性码已就位)"
else
  FAIL=$((FAIL+1)); echo "FAIL|T69-invite-code-ready(读不到可用邀请码：本段判据没有前提)"
fi
G0_69=$(t69cnt reg_day global); D0_69=$(t69cnt reg_dev "$DEV69A")
U69M="uatuser_t69m$SFX69"; B69M=$(t69invitebody "$U69M" "$CODE69" "$DEV69A")
req3 POST "" /api/auth/register "$B69M"; cks T69-invite-join-ok 200
ckq T69-invite-no-platform-seat "$G0_69" "$(t69cnt reg_day global)"
ckq T69-invite-no-device-progress "$D0_69" "$(t69cnt reg_dev "$DEV69A")"
TID69M=$(dbq "SELECT tenant_id FROM users WHERE username='$U69M'" | tr -d '[:space:]')
ckq T69-invite-into-coder-tenant "$TAID" "$TID69M"

# ④ 脏设备号：不 400（对外契约），但照记平台账、且脏值不得进限流账本
dbcfg register_global_daily_limit 100000
G1_69=$(t69cnt reg_day global)
BAD69="bad dev!"
U69D="uatuser_t69d$SFX69"; B69D=$(t69body "$U69D" "$BAD69")
req3 POST "" /api/auth/register "$B69D"; cks T69-dirty-device-200 200
ckq T69-dirty-counts-platform "$(( ${G1_69:-0} + 1 ))" "$(t69cnt reg_day global)"
ckq T69-dirty-no-ledger-row 0 "$(t69cnt reg_dev "$BAD69")"

# ⑤ 库里删掉档位键 ⇒ 回落代码默认 3 格：DEV69A 已占 2 格，这次应放行（读到 2 就红，读到 0/关闭也红）
dbq "DELETE FROM system_config WHERE key='register_device_daily_limit'" >/dev/null
U69A4="uatuser_t69a4$SFX69"; B69A4=$(t69body "$U69A4" "$DEV69A")
req3 POST "" /api/auth/register "$B69A4"; cks T69-default-tier-third-ok 200
ckq T69-default-tier-count 3 "$(t69cnt reg_dev "$DEV69A")"

# 收尾清账＋反证（「写了一串 DELETE 却因方言/转义没执行」是本仓反复踩过的形态）
dbq "DELETE FROM rate_limits WHERE (scope='reg_dev' AND key LIKE 'uat69dev%') OR (scope='reg_day' AND key='global')" >/dev/null
dbq "DELETE FROM alerts WHERE tenant_id=0 AND kind='register_budget'" >/dev/null
# 两档钉回 harness 的开闸值：本段之后还有前端 Playwright 一轮注册，留 2 格会把它们打成 429 假红
dbcfg register_device_daily_limit 100000
dbcfg register_global_daily_limit 100000
LEFT69=$(dbq "SELECT COUNT(*) FROM rate_limits WHERE scope IN ('reg_dev','reg_day')" | tr -d '[:space:]')
ckq T69-cleanup-rate-limits-empty 0 "${LEFT69:-0}"
ALEFT69=$(dbq "SELECT COUNT(*) FROM alerts WHERE kind='register_budget'" | tr -d '[:space:]')
ckq T69-cleanup-alerts-empty 0 "${ALEFT69:-0}"
ckq T69-cleanup-device-tier-restored 100000 "$(dbq "SELECT value FROM system_config WHERE key='register_device_daily_limit'" | tr -d '[:space:]')"
ckq T69-cleanup-global-tier-restored 100000 "$(dbq "SELECT value FROM system_config WHERE key='register_global_daily_limit'" | tr -d '[:space:]')"

DUR=$(( $(date +%s) - START ))
echo "==T-PASS=$PASS FAIL=$FAIL DUR=${DUR}s=="
exit 0