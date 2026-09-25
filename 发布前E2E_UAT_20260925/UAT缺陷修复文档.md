# 发布前 UAT 缺陷修复文档（九步指令·步骤 3 产物）

- 建立：2026-09-25 晚（步骤 1-2 完成后）。上游＝《缺陷清单.md》终版（F-01~F-43 + 观察项 1-14 + 留证总账）。
- 核实方式：四路**只读核实子代理**分组把缺陷账本 × 当前真实代码逐行对号（A＝注册/计费周边 9 条，B＝计费与配额 11 条，C＝F-42/F-38 深核 + 语种与文案 6 条，D＝管理台/权限/前端 12 条），主代理逐条复读关键落点后定稿。**本文所有行号均为现码实读**，与账本行号有出入处以本文为准（已含根因修正）。
- 决策口径：带「拍板」的条目全部按**核实报告推荐默认**施工，逐项登记在文末「四、决策采纳清单」，用户可随时推翻；凡触对外口径（价格、错误码契约、建单拦截）的默认项已加 ⚠ 标记。
- 施工纪律（全程有效）：AGENTS §一·1 store 冻结（billing.go/kbpackages.go 只减不增，新 helper 落 `billing_refund.go` 等窄域新文件）、§一·4 幂等迁移唯一入口 `db.EnsureColumns`、§一·5 i18n 12 语种全量词典 + 等值锁前置钉 UI-ANNOTATIONS、§一·7 shell 断言 `grep -E`/`mny_norm`、§一·8 错误一律 `s.writeError`+apierrors（棘轮 741 只减不增）、§一·9 文档不外推、§二 提交前闸门全绿（计费改动必须 PG `run_uat.sh`）。

---

## 〇、逐条判定总表（核实结论一览）

| 编号 | 核实判定 | 关键修正/补充 | 处置 |
|---|---|---|---|
| F-01 | 已修复收口 | SMTP 已修，`dkim=pass` 尾验属外部 DNS | 不复评 |
| F-02 | 成立 | 根因＝ref 赋值不触发重渲染（`codeSentRef`） | 批 G |
| F-03 | 成立 | `TurnstileApi` 接口缺 `reset` 声明 | 批 G |
| F-04 | 成立 | 三层「假话」全实；死信零告警 | 批 A（死信告警半）；同步发送改造不做 |
| F-05 | 外部依赖 | — | 尾验项，无码 |
| F-06/F-07 | 已修复收口 | 提交 `b5981b8` | 不复评 |
| F-08 | 成立 | 坑在两枚**错误注释**（kb.go:38/:601 称 24 位，randHex(12) 实产 12 位） | 批 F |
| F-09 | 成立 | 前端恒列 mock 项 + 后端渠道白名单不联动 payMode | 批 A 后端 / 批 G 前端 |
| F-10 | 部分成立（观察项） | stale-closure 存在但 useEffect 兜底正确；复跑未复现 | 批 G 顺手防御修 |
| F-11 | 成立 | 顶栏余额仅 `[user]` 挂载快照 | 批 G |
| F-12 | 成立·**根因修正** | 不存在「0.9 折扣配置」，是**两套计价尺子打架**（面值 ¥299/3,000分 vs 尺子 29900 分/百万 tok→¥269.10） | 批 B（配置改尺子键值）⚠ |
| F-13 | 撤销 | 900ms 早读假空，9 档实为上柜 | 无动作 |
| F-14 | 成立 | 模式列真根因＝`runTextTicket` 漏注入 `tenant.WithMode`（非仅显示问题） | 批 A 后端 / 批 G 前端 |
| F-15 | 成立 | prompt 链确无货币指令，闸门天然放行凭空注入 | 批 D |
| F-16 | 成立 | 账没错、接口选错桶（只读永久桶） | 批 B 出参 / 批 G 前端 |
| F-17 | 成立 | 三处根因逐字确认（签名无 lang / 全局单文件 / 模版无语言维度） | 批 E |
| F-18 | 成立 | `.ws-*` 五档从未钉进 UI-ANNOTATIONS——本批**先钉后改** | 批 G ⚠数字 |
| F-19 | 运营外部依赖 | 代码层已实装，缺钱包地址/汇率/链位（需用户提供） | 无码；措辞已自洽（守卫文案） |
| F-20/F-39 | 成立（同一钮） | Caddy D1 有意注释 /metrics，公网死入口属入口治理 | 批 G 删钮 |
| F-21 | 成立 | 三点全实；`daily_quota_exceeded` 码在 200-错误体路径被整体丢弃 | 批 B ⚠默认墙披露半归产品 |
| F-22 | 成立 | 连带损伤：字符串 org_id 还使角色级联严格比较失配 | 批 G |
| F-23 | 成立·根因精确化 | 模板自杀＝表头 F1 说明**子串匹配后者覆盖前者**劫持列索引到列 5 | 批 F |
| F-24 | 成立 | 双证齐；推荐口径①（改文案+承诺审核） | 批 G 文案 |
| F-25 | 成立 | 删除钮空 children + `type="button"aria-label` 粘连（F-28 族，顺手补空格） | 批 G |
| F-26 | 成立 | 企业租户恒无 locale 包→安全句区死分支 | 批 G（UI 明示+隐藏） |
| F-27 | 成立·渲染面修正 | **纯 React 组件（渲染面①）**，非后端直出；换 dist 即上线 | 批 G+部署 |
| F-28 | 成立 | 一行粘连，`aa2578f` 引入 | 批 G |
| F-29 | 成立 | 唯一体积闸=全局 5MB，28k 字畅通；524 为 CF ~100s 掐断 | 批 D 后端 / 批 G 前端 |
| F-30 | **部分成立** | 代码语义正确（第 5 败即锁）；生产失效＝IPv6 隐私地址 XFF 键漂移 | 批 F 双键；取证列为运维项 |
| F-31 | 成立·重定性吻合 | **目标侧建号也无闸**（:688 403 只挡操作者）；update 提角色同样放行 | 批 F |
| F-32 | 成立 | tid=0 一条 open 吞全世界后续声明 | 批 A ⚠id55 人工核销 |
| F-33 | 成立 | 判定用台阶、投递用字面量两套口径 | 批 A |
| F-34 | 成立 | 补审单不留指向原单的字段，无从防重 | 批 B |
| F-35 | 成立 | 同函数内 org_id 有 nil 保全、role/display_name/status 没有 | 批 F |
| F-36 | 成立（三段链全对号） | carry 行 ref 指向新单，退旧单查询永碰不到 | 批 B |
| F-37 | 成立·措辞订正 | B3 **只清 package_code**；expiry 残留＝观察项 10（watchdog 空码豁免） | 批 B |
| F-38 | 成立 | 全链无长度/来源守卫；`IsTranslationUsable` 四判据 2100 字邮件全过 | 批 D；供应商取证清单另列 |
| F-40 | 成立 | 一行级确认：非裸传 err，须导出 `billing.NewQuotaErr` 保文案补码 | 批 A |
| F-41 | 成立 | 公式低估实测复核 ~62 倍（88 号实烧 1,075,400 tok vs 估 17.3k） | 批 D ⚠K 值 |
| F-42 | 成立 | 四层根因逐环证实 + 三处精化（Abort 即 Execute 同条 ctx；同污租户 UI；下载判据不看产物） | 批 C（专项） |
| F-43 | 成立 | 同函数 errTxt 范式俱在，唯独 :1919/:1926 两处漏 | 批 A |
| 观察 1-14 | 按账本定性 | 2/5 顺批修；9 改注释；3/4/7/8 登记不修或随家族 | 见各批 |

---

## 一、施工批次与顺序（8 批）

> 排批原则：先低风险独立小改，后专项大改；计费批必须 PG 闸门；前端 dist 与后端二进制分开可换源验收。

- **批 A｜后端低风险小改（不动契约）**：F-40 → F-43 → F-32 → F-33 → F-09 后端半 → F-04 死信告警 → F-14 后端半（WithMode）。
- **批 B｜计费与配额域（★PG run_uat 红线）**：F-34 → F-36+F-37（同域联合作业，新文件 `store/billing_refund.go`）→ F-21 → F-16 出参半 → F-12 配置半。
- **批 C｜F-42 专项**：a 中止语义透传 → b reject_source 落列（幂等迁移）+ workflow 判据 → c 认领收窄 → d 对外/UI 产物守卫（`ticketHasDeliverable` 同源）+ 存量订正幂等 UPDATE。
- **批 D｜生成守卫与提示词**：F-38 三点守卫 → F-15 货币指令 → F-41 估算系数 → F-29 后端护栏两件。
- **批 E｜邮件语种跟随（F-17）**：users.preferred_lang 迁移 → 注册载荷 → 手册/模版按语种链路。
- **批 F｜成员与权限**：F-08 → F-23 → F-31 → F-35 → F-30 双键。
- **批 G｜前端批（一次 dist 出）**：F-28 → F-02+F-03 → F-09 前端半 → F-22 → F-20/39 → 观察5 → 观察2 → F-11+F-10 → F-16 前端半 → F-14 前端半 → F-25 → F-26 → F-24 文案 → F-29 前端半 → F-27 → F-18（先钉 UI-ANNOTATIONS）。
- **批 H｜部署与运营动作（步骤 9 随发版）**：12 份最新手册 PDF 铺 `/opt/translator/data/manual/`；`scripts/build_sdk.sh` 产物入 dist `/sdk/`；id55 与存量数据订正执行；F-30 服务器 XFF 取证。

每批完成后即跑该批触及的闸门子集；全部批完成后跑 §二 全量闸门 + 步骤 5 全缺陷复测。

---

## 二、逐条修复方案

### 批 A｜后端低风险小改

**F-40 SSE 余额错误帧缺 error_code**
- 证据：`api/billing_api.go:91-94` —— :92 `CheckBalance` 带码错误（`internal/billing/quota.go:235-244`，:241 `quotaErr{"额度不足，请充值","insufficient_balance"}`）被 :93 裸 apiErr 顶掉；出帧端 `api/stream.go:140 billing.QuotaErrCode` 对 apiErr 恒空（quota.go:251-257）。对照腿 :86-87 日限额原样透传所以有码。
- 改法：`billing/quota.go` 导出 `NewQuotaErr(msg, code string) error`（billing 包非冻结）；:93 改 `return tid, release, billing.NewQuotaErr("组织积分已耗尽，请联系管理员及时充值", "insufficient_balance")`——保文案+补码。工单路径同族由 F-21③ 收口。
- 断言：api 单测断 `billing.QuotaErrCode(gateErr)=="insufficient_balance"`；SSE 集成断 error 帧带码（对齐前端 `translate.ts:89` E11 充值引导分支）。

**F-43 开票错误泄漏驱动原文 `sql: no rows…`**
- 证据：`store/billing.go:1915-1929 CreateInvoice` :1919/:1926 两处 `QueryRow.Scan` 原样回 `sql.ErrNoRows`；同函数 :1922/:1936/:1946 均有 `errTxt` 范式唯独这两处漏；`api/admin_billing.go:422` 拼 `err.Error()` 上外网。
- 改法（最小版）：① store 两处 `errors.Is(err, sql.ErrNoRows)` → `&errTxt{"订单不存在或不可开票（仅已支付订单可开具发票）"}`（跨租户/不存在同文案，不泄露存在性；只改既有函数体不违冻结）；② 删 handler 的 `err.Error()` 拼接。writeError 迁移归入 200-错误体统一批（F-21③ 同向），本条不单独改状态码口径。
- 断言：单测三态（pending/不存在/他租户）同一句且不含 `sql:`；shell 断言 `grep -E 'sql:'` 空命中。

**F-32 pay_manual 告警被幂等去重吞单**
- 证据：`store/alerts.go:43-48` 同 tenant+kind 有 open 即跳过；`api/pay.go:380 CreateAlert(0,…,"pay_manual",含单号)` 固定 tid=0 → 平台级一条 open（现网 id55，2026-09-08）吞掉之后**所有**新声明。告警表无 ref 列。
- 改法（方案 B，零 schema）：alerts.go 加窄方法 `CreateAlertPerOrder`（或 pay_manual 类去重豁免）直接 INSERT——每条订单号一条线索；声明极低频无刷屏风险。
- 断言：store 单测连发两条不同单号 → open 计 2（sqlite 自钉 + PG 整包自检）；UAT 两次「我已付费」后 `/api/system/alerts` 新增 2 条含各自单号。前置人工项：id55 核销（批 H）。

**F-33 站内信按字面 role='admin' 圈人**
- 证据：`pay.go:384 ListUsersByRole(0,"admin")` → `store/users.go:157-159` SQL 精确匹配；`iam/models.go:71-74` 双值 `super_admin`/`admin`，判定侧 RoleLevel>=4 双认——建号/提权实际写 `super_admin`，收不到。
- 改法：`pay.go:384` 合并遍历两角色：`append(ListUsersByRole(0,"admin"), ListUsersByRole(0,"super_admin")...)`（新方法不放 billing.go；若加 `ListUsersByRoleLevel` 则落 users 域文件，本条最小=两行合并即可）。
- 断言：单测建 super_admin 用户→触发声明确认→断其 notifications 有 `pay_manual` 行。

**F-09 后端半（mock 渠道不校验 payMode）**
- 证据：`pay.go:134` 渠道白名单只判字符串合法，无 payMode 交叉校验（前端半见批 G）。
- 改法：`handlePayCreate` 补 `req.Channel=="mock" && payMode!="mock"` → `s.writeError`（错误码：`codes.go` 新增 `ErrPayChannelNotAvailable`→403 或复用 ErrValidation→400，按 §一·8 定码登记映射）。
- 断言：UAT 断 `POST /api/pay/create {channel:"mock"}` 在 static_qr 下必 4xx；payMode=mock 时仍可下单（反向锁）。

**F-04 死信告警（第一步）**
- 证据：`api/mail_tpl.go:263` 入队即成功；`service/ticket.go:202` maxAttempts=5；`queue/direct.go:104-113` 置 dead 仅写 jobs.error，零告警零指标。
- 改法：邮件任务终次失败处（processMailJob 外层）补 `CreateAlert(0,"critical","mail_dead","邮件任务 id=… type=… 5 次重试耗尽")`。**不改同步发送**（异步口径维持，拍板推荐默认）。
- 断言：单测 mock Send 连败 5 次→ jobs=dead 且 alerts 有 mail_dead 行。

**F-14 后端半（biz_mode 数据缺口）**
- 证据：`service/ticket.go:572`（runFileTicket）有 `tenant.WithMode(ctx, mode)`，**runTextTicket（:481-492）从不注入** → usage_ledger.biz_mode='' → UI 显「-」；「快速/实际 pro 对不上」还叠了 `billing_api.go:147` 实时路径取 ctx mode、同步文本端点默认 fast 两套来源。
- 改法：runTextTicket 入口补 `ctx = tenant.WithMode(ctx, t.Mode)` 一行。（billing_api.go:170 硬编码 bizKind="text" 对文件工单不实——登记不修，超出本条射程。）
- 断言：service 单测：文本工单跑完 usage_ledger 行 biz_mode == ticket.Mode。

### 批 B｜计费与配额域（★全部改动必须 PG `run_uat.sh` 全绿；金额断言先过 `mny_norm`）

**F-34 补审单不防重**
- 证据：`store/billing.go:1560-1576 ReopenManualOrder` 仅校验原单 cancelled+manual，每次 INSERT 新 pending，且新单无任何指回原单的字段。
- 改法（推荐变体）：`db.EnsureColumns` 给 orders 补 `reopen_from_order` 列（幂等，SQLite+PG 通用，模板照 `store/tickets.go:296`）；ReopenManualOrder 改既有函数体：INSERT 前先查 `reopen_from_order=原单 AND status='pending'` 命中即复用返回。同时为 F-36 的按单反查留同型范式。
- 断言：单测连调两次 → 仅 1 张 pending；MarkOrderPaid 对同原单双补审单不双入账（:1766-1768 幂等闸在位即锁正向）。

**F-36 升级结转白嫖（退旧单 carry 不回收）**
- 证据：结转 `billing.go:1383-1455`（旧单 order 行置零、转插 `source='order_carry', ref_id=新单`）；退新单收回 UPDATE 限定 `source='order'`（:1739-1742，carry 有意豁免）；退旧单 its order 行 left 已 0→退 0 元，且**全函数无反查 carry 路径**。
- 改法：`RefundOrder(X)` 置 refunded 前追加（新 helper 落**新建 `store/billing_refund.go`**，billing.go 只出不进）：`UPDATE quota_grants SET left=0 WHERE tenant_id=? AND source='order_carry' AND left>0 AND ref_id IN (SELECT id FROM orders WHERE upgrade_from_order=? AND status='refunded')`——「先退新后退旧」白嫖序封死；子单仍 paid 时 carry 保留（对应已付对价，合法）。
- 断言：store 单测：买 basic→升级 pro→退 pro→退 basic ⇒ `TenantRemainTotal` 结转份额归零；反向序 ⇒ carry 保留、退 0 元。PG run_uat 必跑。**存量活体**：租户 3 幽灵 3,000 分列批 H 手工清算（步骤 6）。

**F-37 退款抹错身份**
- 证据：`api/admin_billing.go:373-381` B3 分支**无条件** `perms.PackageCode=""`，不比对被退单是否现役包。**措辞订正**：package_expires/subscribed_at 并未被清（残留＝观察项 10，`watchdog.go:318` 靠空码豁免兜底）。
- 改法（纯 api 包）：① `perms.PackageCode == pkg.Code` 才清；② 清时把 `PackageExpires/SubscribedAt` 一并置空（收掉观察项 10）；③ code 匹配但租户还有其他 `status='paid'`、ptype=paid、未到期、非本单的订单时，身份改挂那张单的包（store helper `LatestActivePaidPackage(tid, excludeOrderID)` 落 `billing_refund.go`）。
- 断言：三连单测：单订阅退款→code+时间键全清；双订阅退旧→新身份在位；退非身份来源单→身份逐字段不动。

**F-21 新租户默认墙不披露 + 两墙不联动 + 200-错误体**
- 证据：`api/register.go:300-302` 硬编码 `MaxDailyChars:20000, MaxDailyTokens:20000`（ops 策略 `policy.go:262` 有旋钮而注册链路没读）；保存侧 `billing_api.go:427-438` 只改 chars 时 tokens 不动，闸门 :75-85 token 墙优先→**改了字符墙等于没改**；chars 无上限钳制；拒绝路径 `api/tickets.go:187-189/:194-196` 回 200+success:false 且 quotaErr 的码被丢弃。
- 改法：① 注册默认改读 `effOpsPolicy.Limits.DefaultMaxDailyChars`（管理台可调）；② 保存联动：`MaxDailyPoints==nil && MaxDailyChars>0` 时按折算率重算 MaxDailyTokens，并补 `max_daily_chars ≤ system_config.quota_max_daily_chars` 钳制；③ 两处拒绝改 `s.writeError(apierrors.New(ErrQuotaExceeded,…))`（码已在 `codes.go:41`；此为**减少**内联，棘轮安全）——**⚠默认按核实推荐采 4xx 语义**；前端 `request()` 封装对 4xx→success:false 归一行为须实测顺验，存量 UAT 脚本断 200 处同步改。**披露半**（定价页/注册提示文案）交产品，登记决策项不擅自上文案。
- 断言：单测：改 chars 后 GET quota points 联动等值；撞墙工单回 4xx+code。PG run_uat 加 `assert_status`。

**F-16 出参半（总览组织余额读错桶）**
- 证据：`api/admin_evals.go:49,61`→`GetBalance` 只读 `balance_accounts.balance`（永久桶）；grants 在 `quota_grants` 故显 0；双桶口径现成＝`billing_api.go:178-189 balancePayload`。
- 改法（推荐①）：admin_evals 出参增 `total_points`（调 `TenantRemainTotal`，聚合 helper 不得放 billing.go——落窄域文件或直接复用既有 payload 函数）；前端改读双桶（批 G）。纯文案案（②）弃。
- 断言：单测建「只有 grants 无 permanent」租户断总览出参=合计；前端 dom 锁双桶渲染。

**F-12 两把计价尺子打架 ⚠（对外价格口径）**
- 证据：定价页读套餐面值（`PricingPage.tsx:86-96` 直出 packages 表）；收银台裸充值走 `price_fen_per_million_tokens=29900`（`store/billing.go:1156-1170`，默认值 :2122）→ 3,000 分×300tok=90 万 tok×¥299/百万=¥269.10；管理台代充同尺子（`admin_billing.go:269-272`）。首月 5 折只作用订阅，与本差无关。
- 改法（默认＝以面值为准，改配置零代码）：`price_fen_per_million_tokens` 29900→**33222**（使 3,000 分=¥299），一次对齐管理台/尺子/套餐三口径。注意该键同时作用于存量 pending 裸充值单回填（`orderMoneyBackfill` :1182）与对账（`admin_reconcile.go:87`）——改后跑对账 UAT 复平。**替代案**：充值包面值改 3,333 分/¥299（动包数据）。
- 断言：`api_uat_txn.sh` 加 pay/create 金额锁（mny_norm 后等值），PG 跑。

### 批 C｜F-42 假 completed 专项（对外契约 + 租户 UI 同源收口）

- 五层链条（全部源码证实）：`orchestrator/flow.go:121-124` 步间返回裸 `ctx.Err()`（cause 被压扁；`store.ErrInsufficientBalance` 只能 `context.Cause` 读回，引擎早有 `abortReasonFrom` engine.go:210-222 但未导出）→ `service/ticket.go:363-364` rejected+RejectReason='context canceled' → `queue/direct.go:104-113` MarkFailed attempts<max 自动回队 → 重试过 `ClaimTicketForRun`（`store/tickets.go:224-227` 无差别放行 rejected）→ `workflow.go:261-273` 把任何非空 reject_reason 当人工驳回意见，译文全空时循环全 continue、**零 LLM 调用 return nil** → :398-400 completed；`store/ticket_finish.go:44,48` 无条件回写旧 reject_reason 留库 → `api/api_openapi_tasks.go:436-440/453-485` completed 分支照发空 translations。Abort 钩子与 Execute 是同一条 WithCancelCause ctx（ticket.go:342→486），cause 注入链现成。UI 污染面：`ListTickets` 不滤 created_by=0、`TicketsPage.tsx:407/690-700` 显已完成+下载钮、`api/tickets.go:612` 下载只看 status。
- **改法四点**：
  - a) `flow.go:122` 改 `return context.Cause(ctx)`（无 cause 时回退 Canceled，用户取消语义不变、:358-361 cancelled 守卫先行拦截）；`service/ticket.go:355-364` 失败收尾加 `errors.Is(runErr, store.ErrInsufficientBalance)` → `t.RejectReason = errInsufficientCode + ": 余额不足，请充值或升级套餐"`（与 :280 预检文案逐字一致，88/90 两型合流）。无需新增机制（哨兵 :234/:280 现成）。
  - b) `tickets` 补列 `reject_source TEXT DEFAULT ''`（走 `db.EnsureColumns`，模板 `store/tickets.go:296 TicketQualityMigrate`）。写入点：人工驳回唯一入口 `api/tickets.go:855/857` 置 `'human'`；三处系统写入（flow.go:196 前缀、ticket.go:280、:364）置 `'system'`。`workflow.go:261` 判据收紧为 `reject_reason 非空 && reject_source=='human'`，另加「载荷存在非空译文」双保险——**全空载荷绝不 return nil 假成功**。
  - c) `ClaimTicketForRun` WHERE 收窄：`status IN ('draft','queued') OR (status='rejected' AND COALESCE(reject_source,'')='human')`——系统错误类 rejected 不再自动重认领，重跑交用户显式入口（本批不加新 UI）。
  - d) 抽 `ticketHasDeliverable(t, files) bool` 一处实现：`api_openapi_tasks.go` completed 映射时文本译文全空/文件无产物 → 降 `failed`+error_code（有 `insufficient_balance:` 前缀用之，否则 task_failed）；`api/tickets.go:612` UI 下载判据同函数复用（鬼 completed 不再露下载钮）。openapi 分支是改既有映射不加新内联错误体，不触棘轮。
- **存量订正（幂等，随批跑一次，仅本地留档）**：`UPDATE tickets SET status='rejected' WHERE status='completed' AND reject_reason<>'' AND <无产物判据>`——现网仅 90 号一例，按 d) 的 helper 语义写 SQL；reject_source 老行留 ''（人工驳回历史单靠「译文非空」双保险仍可重翻，等价损失可接受）。
- **回归断言**：orchestrator：`WithCancelCause`+cancel(ErrInsufficientBalance) 断 Execute 返回 `errors.Is`（修复前红灯）；workflow：RejectSource≠'human'+空载荷断不落重翻分支返回 nil。service：仿 `ticket_lowbalance_test.go`（方言自钉模板）翻译中 sink→Abort→断终态 rejected+`insufficient_balance:` 前缀+ClaimTicketForRun 回 0 行。api：种 completed+空 translations 行断 status 接口回 failed。UAT：任务 90 载荷基准，run_uat 断欠费烧穿终态 failed+码。
- **风险**：(c) 收窄后依赖 rejected 自动重排的存量行为——卡死巡检只重排 in_progress，无冲突；(a) 用户主动取消路径有 cancelled 守卫在前，不变。

### 批 D｜生成守卫与提示词

**F-38 短源文垃圾长文（生成侧污染）**
- 证据：`engine/engine.go:1845 BatchTranslate`→`runChunk:1944-1953` parseBatchOutput **仅判非空即写入**；唯一口径 `engine/translation_guard.go:34-46 IsTranslationUsable` 四判据（空/失败标记/同文/指令残留）2100 字符邮件全过；文本通道 `translateLangsConcurrent`（:826→singleLang）同样无长度比检查；`parseBatchOutput:2049-2090` 收编机制对单槽位超长垃圾照算命中。
- 改法三点：① 第 5 判据并入 `IsTranslationUsable`：`src≤12 rune → out ≤ max(8×src, 80) rune 且绝对上限 300`（阈值 env 可配，默认保守；只对短格生效避免误杀缩写展开/长语系）；命中走既有 `missingSegments`→补漏/逐段兜底重译链（零新代码）；文件链写回点 :553/:595/:679 自动获益。② 文本通道 `singleLangRaw` 成功返回前（:1550 PostProcess 处）同函数复核，不可用→进既有 3 轮重试队列。③ 命中打 `observability.Warn`（provider/model/长度比）聚合「生成侧可疑率」哨兵。**不做 few-shot 泄漏黑名单**（短语黑名单历史踩坑，不可穷举）。命中后策略默认＝**回退重译优先，重译仍爆炸才标 qa_error**，不把上游抖动放大成整单拒交付。
- 断言：`translation_guard_test` 纯函数：6 字源×2100 字邮件=false；engine httptest 假 LLM 回超长 `<s3>` 断该格落 untranslated 而非交付；CI 哨兵产物不含 "Dear "。
- **供应商取证清单（无法代码闭环，交用户向腾讯 Hunyuan-MT 提工单）**：① B4 前缀重组（时间线重合）后服务端 prefix cache 是否可能串 completion；② 提供工单 86/87/任务 91 请求 request-id 的服务端日志与 completion 原文；③ 批解码/推测采样是否可能混入相邻请求 token；④ 书面缓存隔离口径（同租户内/跨租户）。复现样本字节已在账本留证。

**F-15 货币符号漂移（R 兰特）**
- 证据：prompt 三处组装（`translateInstruction:1040-1090`/`outputContractNote:1091-1098`/`cultureRules:2319-2381` 词条级）确无货币口径；`gates.go:97` 数字一致性天然放行凭空注入；「R」最可能为 Hunyuan-MT 英文电商文体先验，非本仓字面量。
- 改法：`translateInstruction` zh/en 两分支尾部各加一句：「原文金额未指明币种时默认人民币，译文须保持人民币口径（¥/CNY/RMB/yuan），不得替换为任何其他货币符号」。一次性刷新 B4 缓存前缀（按语言组合分桶，等效一次冷启动，可接受）。不走 kb_safety_phrases（词条级表达不了）。「双十一→11.11/Singles' Day」交行业包术语层（batchTermLayer 数据驱动），不写死通用指令。
- 断言：单测锁 `translateInstruction("zh","en","en")` 含 currency/yuan 关键词等值；`e2e-manual/` 活体探针（`test.skip(!process.env.LIVE_LLM)`）断译文不含 R300/R50。

**F-41 建单余额预检系统性低估（~62 倍）⚠**
- 证据：`api/tickets.go:47-57 estimateTicketTokens = chars/1.3×langs×markup(2)`；工单 88 估 17.3k tok（≈58 分）实烧 1,075,400——pro＝初译+评审双趟×1,023 段分块×逐次上下文，公式结构性跟不上；:46 注释「宁可多估」名不副实。预检点 :95-108（调用 :194/:354）。
- 改法：`estimateTicketTokens(chars, langs, mode)`：`est = chars × langs × K(mode)`，**K(pro)=160**（96 实测单位成本→20k 外推 155 的 P99 上包络，宁高勿低）、**K(fast)=60**（单趟无评审 ~2.5 折）；K 值放 system_config（`est_tokens_per_char_pro/fast`）管理台可调，上线首周按 usage_ledger 实测 P99 回调。预检拒绝文案带预估积分（:107 范式已有）。**「部分交付/断点续翻」组合拳本批不做**（设计变更面大，登记决策项；F-42 修完后至少不再假 completed）。
- 断言：纯函数单测以 88/89 两案字节数据做基准锁（88：est≥1,075,400）；`ticket_balance_test.go` 随之重钉。
- **⚠默认风险**：K=160 会拦掉「小余额大文档」碰运气建单——按推荐默认「宁可建单被拒」执行，用户可翻。

**F-29 后端半（chat 通道无护栏）**
- 证据：唯一体积闸=全局 `withBodyLimit` 5MB JSON（`api/server.go:608-616`），28k 字≈84KB 畅通；`stream.go:104-131` 对 req.Message 零校验；无请求级 deadline（SSE 故意为之），心跳过闸才启动——524 为 CF ~100s 掐断。
- 改法：① `handleChatStream` 在 SSE 头写出**前**（:118 sseHeaders 之前，符合 :116-117「拒绝必须在头前」纪律）对 `[]rune(req.Message)` 设上限 `chatMaxChars`（运营策略键，**默认 5,000** 字符），超限 `writeError` + code `chat_text_too_long`、文案引导改用翻译工单；② chat 管线包 `context.WithTimeout(ctx, 90s)`（留 CF 余量），到点出 error 帧「处理超时，长文本请走工单」。
- 断言：api 单测超长消息在 SSE 头前收 4xx JSON；UAT `curl` 断 `grep -E '"error_code":"chat_text_too_long"'`。前端半在批 G。

### 批 E｜F-17 邮件语种跟随

- 证据：`api/mail_tpl.go:236 sendManualEmail(to, username)` 签名无 lang、`register.go:408-411` 调用不传；注册载荷（:74-101）无 app_lang；`loadManualPDF`（:284-302）全局单文件序 `manual_pdf_path > env > /opt/translator/data/manual.pdf`，附件名硬编码「产品手册.pdf」；`getMailTpl(code)`（:176）每类型一份无语言维度；users 表（store.go:131-144）无语言列（补列范式＝PersonaMigrate→EnsureColumns）。最新手册在 `产品手册/pdf/LangCross-User-Guide-{12 语种}.pdf`。
- 改法三步：
  1. **注册语言落库**：register 请求体加 `app_lang`（前端 `api/auth.ts` register 调用方随 `getLang()` 带上）；`UserLangMigrate`（EnsureColumns `users.preferred_lang TEXT DEFAULT ''`，挂既有 migrate 编排点）+ Set/Get 薄方法归 users 域文件；12 码白名单校验，非法落 ''（与 job_role 同口径）。
  2. **链路带 lang**：`sendManualEmail(to, username, lang)`；`loadManualPDF(lang)`：`manual_pdf_dir/{lang}.pdf` →回落 en→zh→旧单文件终兜底（兼容既有 `manual_pdf_path`）；附件名按语种。`sendTemplatedMail(to, code, data)` 加 lang，`getMailTpl(code, lang)` 读 system_config 键 `{code}.{lang}`，缺省回退现行无后缀键＝中文（**老配置零迁移**）；首批至少手册+验证码两类；模版管理台 UI 本批不加语种维度，超管直配 system_config（临时口径已在本文披露）。
  3. **兜底**：app_lang 空留 zh（不做 Accept-Language 双判据）。验证码模版**先 zh/en 两份 + 其余回落 en**（推荐默认，误翻风险大于收益）。
- 止血项（原账本「立即可做」）：随本批代码一并闭环——按语种取文件后，线上 `/opt/translator/data/manual.pdf` 旧 6,522B 文件由批 H 铺 12 份新 PDF 替代，无需单独换文件。
- 断言：api 单测 t.TempDir 铺假 PDF 断命中/回落序；register 单测断 app_lang 落库；sendManualEmail mock mailer 断附件名与模版语种；UAT 注册链断入队载荷 JSON 含 lang。

### 批 F｜成员与权限

**F-08 temp_id 必死**
- 证据：`kb.go:543 randHex(12)` 而 `:40 tempIDRe=^[0-9a-f]{24}$`（:604 校验）；`randHex`（:563-573）**串长=n 个 hex 字符**（:561 注释自证），12 位必不匹配；两枚**错误注释** kb.go:38/:601（称 randHex(12) 产 24 位）是历史误导源。`upload_chunk.go:213` 的 randHex(12) 只作文件名不动。
- 改法：`kb.go:543` → `randHex(24)`；订正 :38/:601 两枚注释。全仓 grep 无 12 位长度依赖。
- 断言：`kb_tempid_roundtrip_test.go`——弱锁 `len(randHex(24))==24`+正则过；强锁 httptest 直调 handleRecognizeKB→取 temp_id→handleImportKB 断 200（往返锁能力阻「再改一头」；sqlite 自钉模板）。

**F-23 批量导入三件套**
- 证据（根因精确化）：`user_import.go:102` 白名单含 .csv 而 `readImportRows(:234-237)` 只有 excelize；模板自杀＝说明塞进表头 F1（:74），表头循环 :251-265 **子串匹配后者覆盖前者**，长说明同时命中 username/role/org/email 四个 case 把列索引全劫持到列 5，`cell()`（editor.go:460-466）越界回空→username 全空→:290-292「无有效数据行」——**模板污染所有用它的人的数据行**；:204 回执无条件承诺邮件通知。前端 accept（OrgP.tsx:535）含 .csv。
- 改法：① :251-265 每键**首中即停**（`if _, ok := idx[k]; !ok`）；② 说明挪出表头行（批注/第二区，与①冗余互保）；③ CSV 走标准库：`readImportRows` 按扩展名分支 `encoding/csv`，表头/行组装抽 `buildImportRows(all [][]string)` 纯函数两路共用（推荐「实装 CSV」而非砍白名单——砍功能是删需求）；④ :204 回执按 `nu.Email` 分支两枚中文文案（后端字面量，零 i18n 工作量）。
- 断言：`user_import_test.go`——**模板即回归资产**：抽 `buildUserImportTemplate()`→写临时文件→readImportRows 断示例行原样解析不死；说明劫持负向锁（长文案表头断 username 列=0）；CSV 两列样例行数等值。

**F-31 dept_admin 未绑部门即全量可见（双无闸）**
- 证据：裁剪闸 `auth.go:582` 要求 `RoleLevel==2 && OrgID>0`，org0 落 :608 全租户；建号侧 `:619` 起各闸只约束操作者与组织存在性（:688 403 是**操作者**侧——目标侧建号 org=0 一路绿灯，`validateOrg` 对 <=0 直接放行 :861-863）；update 侧提角色为 dept_admin 不传 org 同样无拦截。
- 改法：① create 在角色白名单（:653）后补 `role==dept_admin && org_id<=0 → 400`；② update 补「变更后终态」判据（finalRole/finalOrg 合并后 `RoleLevel==2 && finalOrg<=0 → 拒绝`），同封「提角色不带部门」与「挪回根」两向；③ 新错误分支一律 `s.writeError(ErrValidation)`（后端中文字面量，零词典）。存量 org0 dept_admin（如 uat_dept_01）闸只拦增量，事后处置归清理决策。
- 断言：`auth_deptadmin_org_gate_test.go` 四向锁（建 400/带部门 200、update 提角色 400/200、org 改 0 400、绑定态子树正锁）。发布前自查 UAT 脚本无 dept_admin+org0 载荷形态。

**F-35 users/update 空字段静默清空**
- 证据：`auth.go:739-741` 注释称「可为空=不修改」，但 :845 原样传 `iam/store.go:186-190` UPDATE 四列；status 空串 :751-753 **默认成 active**（disabled 账号移一次部门就悄悄复活）；org_id 有 `*int64` nil 保全（:839-844）同函数两套语义；审计 :849 after 记空串失真。影响面：前端「移动成员」正是只带 id+org_id 的调用形态。
- 改法（后端指针化，前端零改动）：① `DisplayName/Role/Status` 改 `*string`，删 :751-753 默认块；② 执行前复用 :833-836 已取的 target 合并 `derefOr(req.X, target.X)`（显式空串＝清空语义保留）；③ 等级校验判据随指针微调；④ 审计 after 改记合并后真实写库值；⑤ store 委托签名与 `iam/store.go` 不动（合并在 handler 层，薄委托原则）；:846 失败分支风格不动（§一·8 迁移另批）。发布前 grep `scripts/uat/` 若有刻意传空串表「保持」的旧用法需改载荷。
- 断言：`auth_update_partial_test.go` 五锁：只传 org_id 后三字段逐一不变；disabled 移动后仍 disabled；显式 `display_name:""` 清空成功；审计 after=合并真值；`org_id:0` 根组织语义不破。

**F-30 登录锁定双键（IP∪username）**
- 证据（定性修正）：`ratelimit.go:26`（阈值 5）/:78-79（第 5 败落锁）/:125-140 代码语义**本身正确**；生产「第 6 次未拦」＝IPv6 隐私地址轮换致 XFF 首跳漂移、计数打散。取证（rate_limits 表键分布+访问日志样本）列运维项随批 H。
- 改法：`fail/blocked/clear` 调用点加 username 维度——键 `ip:%s` 与 `user:%s` 双计数，任一达阈即落锁（blocked 里 OR）；user 键不依赖 IP。文案零新增（锁定文案已有）。登录页 Turnstile 联动（失败≥2 次挂验证）与阈值下调**本批不做**，登记决策项（引入用户名枚举面需与 Turnstile 同批权衡）。
- 断言：单测双 IP+同 username 断 user 锁生效；UAT 断同键 5 败第 6 次必 429（api_uat_login.sh；注意账本记忆口径「UAT 探测别撞登录限流预算」，用例自带冷却恢复）。

### 批 G｜前端批（一次 dist；i18n 新键=12 语种文件同步义务，`locales.core.test.ts` 动态键集闸自动兜底）

**F-28**：`AiRegisterFlow.tsx:719` `'ar-otp'+ (c ?'ar-otp--filled':'')` 补空格一行。断言：dom 测试 filled 态 className **精确等于** `'ar-otp ar-otp--filled'`（等值锁）。
**F-02**：`codeSentRef`（:125/:590/:595）改 `useState`，成功 `setCodeSent(true)`、失败回滚 `false`。i18n 零新增（auth.aiOnline 现成）。断言：mock 成功断钮变「在线」、失败断回「发送验证码」。
**F-03**：`turnstile.ts:13-16` 接口补 `reset`；`reexecTurnstile`（:71-74）改 **reset→execute**；发码失败路径按钮 60s 禁用（与传统表单 `useCountdown(60)` 对齐，推荐默认）。断言：dom 测试 mock 断 `ts.reset` 被调；UAT 脚本断连点两次 captcha_token 前 8 位不同。
**F-09 前端半**：`PlansP.tsx:722` mock 项改 `payMode==='mock'` 条件展开（同文件 :717-721 现成范式）。i18n 零新增。断言：dom 锁 static_qr 下无「模拟支付（测试）」option。
**F-22**：`OrgP.tsx:133 onNuOrgChange` 源头 `setNuOrgId(Number(v))`（覆盖 :522 入口与 :120-133 级联两处伤；编辑路径 :262 已有 Number 对照）。后端**不开**字符串宽容（推荐默认：宽容会把同类前端类型 bug 全数静默）。断言：新建 `OrgP.dom.test.tsx` 断提交载荷 `typeof org_id==='number'` + 反向锁 stringify 不含 `"org_id":"`。
**F-20/F-39**：删 `panels_a.tsx:113-116` `openMetrics()` 与 :130 按钮（`API_BASE` import 仍被导出审计用，不动；i18n `overview.prometheus` 键**保留不删**——删键是 12 文件手术零收益，注释标注钮已移除）。断言：dom 两角色态均无 Prometheus 钮；公网 /metrics 探针不进 Playwright（AGENTS §6，归 deploy 冒烟）。
**观察5**：`TaskCenterP.tsx:178-179/:207` 类型标签改按 `row.period || task_type 兜底` 映射现成四键 periodDaily/Weekly/Once/Event（:285 同款三元式抽 helper；i18n 零新增）。断言：`TaskCenterP.dom.test.tsx` 加 `task_type:'daily'+period:'weekly'` 行断「每周任务」。编辑弹窗两值枚举不动（产品面）。
**观察2**：① `panels/auth.ts:169 auth.forgotSent` 与 `dicts/zh.ts:183 login.forgotSent` 删括号句「（未配置邮件时请在服务端日志查看）」（zh/en panels+dicts+十 locale ≈24 处值编辑，零新键；`pwd.codeNoop` 保留——2025 拍板测试模式明示文案）；② `auth.go` handleResetPassword 四处失败族（:425/:452/:461/:472）改 `writeError(ErrValidation)`→400（与 F-21③ 同向收口；forgot 防枚举恒真文案 :365/:370 **不动**）。断言：httptest 错码→400+code 双锁 + forgot 不存在账号 200+success:true 防枚举锁；dom 断 Login 失败分支 message 仍上屏（request 封装 4xx 归一顺验）。
**F-11+F-10**：`App.tsx:158-184` 把 `myPackage()` 提取 `refreshPkgLine` useCallback 经 context 下发，`useChat.tsx` done 帧（:212-217 points_used）后 debounce 2s 调一次（推荐默认）；顺手消 `Bell.tsx:107-111` stale-refresh（toggle 内直接拉列表，不依赖 useEffect 兜底）。断言：dom 断 done 帧后顶栏积分变化；铃铛展开 1s 内 EmptyState 消失。
**F-16 前端半**：总览改读 `total_points` 双桶合计（保留明细行），标签语义随之订正。断言与批 B 合。
**F-14 前端半**：`MyBilling.tsx:169` UTC 裸切片改本地时区（抄 `TicketsPage.tsx:841` 口径）；全站 UTC 家族（`lib/ui.ts:12 fmtTime`、KB 授权列）给 fmtTime 加 `local` 参数一处改多处受益。模式列（:173）随后端 WithMode 修复自然归位。断言：vitest jsdom 钉 `TZ=UTC`/`TZ=Asia/Shanghai` 双跑等值。
**F-25**：`BrandTermsP.tsx`——① :192 删除钮内放现成 `<CloseIcon size={14}/>`（`@/ui/langcross/src` icons.tsx:74）+ 同排 `type="button"aria-label` 粘连顺手补空格；`removeEntry`(:134-140) 前置现成 `confirmDialog`（uiDialogs.tsx:32）；② 三处 `window.prompt`（:111/:113/:125）换站内 Dialog（仿 :162-171 新增弹窗同构，`editDlg` state，提交走既有 kbEntryAdd/Update；语言输入用 BRAND_LANGS select + `langLabel()` 不新造语种名）。i18n：净增约 5 键（bt.delConfirmTitle/Body、bt.editLangTitle、bt.formLangLabel/TextLabel）+ 删 3 枚 prompt 旧键（推荐默认＝删，键集闸容不下死键）×12 语种≈96 处。断言：dom 三锁——删除钮 svg 存在、点删除先出确认后才调 kbEntryDelete、`vi.spyOn(window,'prompt')` 负向清零。
**F-26**：`KbP.tsx:645-720` 安全句 Panel 改 `isSuper` 条件渲染——非 super 只留 Panel 包一行新键 `kb.safetyPlatformManaged`（zh「安全句（语言文化规范）由平台统一维护，无需企业配置。」×12 语种）。断言：dom 锁 isSuper:false 下文案在 DOM、表单控件不在 DOM；super 态反向锁。
**F-24 文案**：`panels/kb.ts` `kb.bitextDone` zh→「已提交，待平台审核」、`kb.bitextImport` 去「写TM」硬承诺（口径①推荐默认），`locales/{十语种}.ts` 同键实测都在 :1218 行机械替换；`KbP.tsx:399/412` 回执追加新键 `kb.bitextPendingNote`（审核通过后自动进入翻译记忆）。后端建议（export-tmx 加 pending_review 字段/租户只读入口）归产品拍板本批不动。断言：i18n 键集闸天然覆盖 + vitest 静态断 zh 下 `kb.bitextDone` 不含「已写入」。
**F-29 前端半**：`useChat.tsx` 错误处理对非 SSE/HTML 响应体兜底——检测 content-type 或体首 `<!DOCTYPE` 即替换为友好文案（「服务处理超时，长文本请改用翻译工单」），**禁止响应体原文塞进气泡**；与后端 90s 超时帧文案对齐。断言：dom mock HTML 响应断气泡内无 `Ray ID`/`<!DOCTYPE`。
**F-27**：`SdkP.tsx`（渲染面①，纯 React）——采账本选项②：① 新脚本 `scripts/build_sdk.sh` 把 `sdk/python/dist/*`、`sdk/typescript` `npm pack` 产物拷入 `frontend-react/public/sdk/`（latest 别名仿 build_extension.sh，`--check` 漂移闸同口径）；② :35/:44 install 命令改本地安装命令 + 每卡下载链接（仿 :97-103 扩展卡结构）；③ i18n 约 3 新键（sdk.srcHint/downloadPython/downloadTs）+ `sdk.changelogHint` 值订正（「npm/PyPI 同步发版」是假话）×12 语种；Java 卡维持源码分发文案。真发 PyPI/npm 属运营（选项①）发布日前不做。断言：dom 反向锁全文不含 `pip install langcross-translator`/`npm install @langcross`；下载 e2e 照 `extension_download.spec.ts` 口径——200+**魔数**（whl/tgz=`PK`）+非 HTML 兜底+体积下限，版本号从盘上现读禁写死。
**F-18（⚠先钉后改）**：`styles/theme.css:286-324` `.ws-*` 五档固定 px、无 clamp/vw/媒体查询，且该组数字**从未钉进 UI-ANNOTATIONS**（源自 09-20 口头两倍档）。施工序：① 在 `UI-ANNOTATIONS.md` 新增 §1.4「WordSwap 尺寸档（〇-Q 批）」钉死两元组（现值 + 窄屏≤640px 值，推荐表：`.ws--lg` 44/52/32/23/3px→26/30/16/14/2px；基础档 16/17/12 不缩或 15/16；`.draft-ws` 15/16/12 **不缩**（贴文本基线）；`.file-segs-ws` 14/15/12 不缩；`.tk-prog-ws` 18/17/14→15/16/12）；② theme.css 按钉死值写 `@media (max-width:640px)` 覆写或 clamp 两端点；③ 等值锁：`readability.test.ts` 增源码段逐档断 clamp 两端点字面值 + `pixel_uat.spec.ts` 钉 360/1440 双视口实测 `.ws--lg .ws-r` computed 等值；**不动既有锁数字**（只加段）。泰/俄/阿长词折行节拍列观察项。

### 批 H｜部署与运营动作（随步骤 9 发版执行，非代码）

1. 手册 PDF 铺服务器：`产品手册/pdf/LangCross-User-Guide-{12 语种}.pdf` → `/opt/translator/data/manual/{lang}.pdf`（配 `manual_pdf_dir` 或 system_config），旧 6,522B 单文件挪走留档。
2. `build_sdk.sh` 产物随前端 dist 换源上线（/sdk/ 静态直出零后端改动）。
3. 存量数据处置（跑前 DB 备份，全部幂等）：**id55 人工核销**（先查 9-08 那笔租户 #1 声明是否漏处理）；F-42 鬼 completed 90 号订正；租户 3 carry 幽灵额度清算（F-36 活体）；TM 待审候选 1005-1007 人工审或驳；订单 5/7 与测试账号处置按步骤 6 与用户确认范围。
4. F-30 取证：服务器上查 `rate_limits` login_fail 键分布 + XFF 首跳样本（验证键漂移定性）。
5. 发版验收按《部署指南》§十清单；前后端都换（本批触后端二进制+dist+扩展无涉）；发版后只验接线不跑线上像素（既定口径）。
6. F-19 USDT：保持未开放（守卫文案已自洽）；开放需用户提供钱包地址后管理台配置——决策项。

---

## 三、AGENTS 约束对照（施工时逐批点名）

- store 冻结：billing.go/kbpackages.go **只改既有函数体**（ReopenManualOrder/RefundOrder/CreateInvoice）；全部新 helper（carry 回收、reopen 复用、LatestActivePaidPackage、CreateAlertPerOrder、UserLangMigrate、ticketHasDeliverable 落 api 层）各归窄域文件（`billing_refund.go`/`alerts.go`/`users.go` 域/`tickets.go`）。
- 迁移唯一入口 `db.EnsureColumns`：本批三列——`tickets.reject_source`、`orders.reopen_from_order`、`users.preferred_lang`；禁一次性 ALTER。
- 错误口径：新增错误分支全部 `s.writeError`+codes.go 定码（本批新码候选：ErrPayChannelNotAvailable、chat_text_too_long、ErrInvoiceNotEligible）；棘轮 741 **只减不增**——F-21/F-43/观察2 属减少，安全。
- i18n 12 语种同步清单（本批全部）：F-25 净增 5 删 3、F-26 增 1、F-24 值改 2+增 1、F-27 增约 3+值改 1、观察2 值改 2（零新键）；其余条目经核实均零词典。
- 等值锁前置：F-18 必须先钉 `UI-ANNOTATIONS.md` 再动 theme.css/测试锁；F-28/F-09/F-22/F-25 的 dom 断言写等值/负向清零，不写单向锁。
- 方言与闸门：新单测一律自钉 `DB_DRIVER=sqlite` 模板 + 整包 PG 自检；批 B/C 触碰计费 → PG `run_uat.sh` 为发布闸门，SQLite 快跑仅自检；金额断言过 `mny_norm`；shell 断言 `grep -E`。
- 注释闸：全部改动随批补中文注释并过 `tools/check_doc_comments.py`（0 缺口基线，docblock 紧贴声明上方；主代理自己做不外包）。
- 渲染面/发版映射：批 A/B/C/D/E/F → 换 `translator-server`；批 G → 换 `frontend-react/dist`（含 ①内联样式面）；无 assist/扩展/后端直出页触及。

---

## 四、决策采纳清单（本批按推荐默认施工，用户可逐项翻案）

| # | 条目 | 采纳的默认 | 翻案代价 |
|---|---|---|---|
| 1 | F-12 | 以套餐面值为准，尺子键 29900→33222（裸充值变贵 10%） | 改回＝动包面值 3,333 分/¥299 + 定价页文案 |
| 2 | F-21 | 默认墙数值维持经 ops 旋钮可读；**披露文案暂不加**（交产品）；拒绝改 429 语义按码分支 | 披露上 /pricing 需一句对外承诺 |
| 3 | F-41 | K(pro)=160/K(fast)=60 宁高勿低；**部分交付不做**，在途烧穿仍会失败（F-42 修后至少如实报 failed） | 断点续翻是大设计 |
| 4 | F-29 | chat 上限 5,000 字符 + 90s 超时 | 上限调低/高均为配置 |
| 5 | F-38 | 命中回退重译优先，仍爆炸才 qa_error；阈值 8×/300rune env 可配 | 改阻断口径需拍 |
| 6 | F-27 | 选项②源码下载+本地安装（不真发 PyPI/npm） | 真发布属运营 |
| 7 | F-24 | 口径①改文案承诺审核（不放开导入即入库） | 口径②=放弃 R7 防污染闸 |
| 8 | F-31 | dept_admin 必须绑部门（create/update 双闸 400）；org0 存量不自动迁移 | 若产品允许 org0 则回到列表裁剪 |
| 9 | F-18 | 窄屏值按 §二·批G 推荐表钉档 | 数字改一处=真值改一处，测试锁随钉 |
| 10 | F-30 | 只做双键；登录页 Turnstile/阈值下调缓做 | 枚举面权衡 |
| 11 | F-04 | 保持异步+死信告警（不改同步发送） | — |
| 12 | F-17 | 验证码模版先 zh/en+其余回落 en；app_lang 空留 zh | 12 份验证码模版全量另做 |
| 13 | F-37 | 退款身份改挂「其他在期订阅」判据（含） | 不含则双订阅退新单误清 |
| 14 | F-42 | reject_source 落列（弃前缀白名单）；系统错误 rejected 不给一键重跑 UI | 落列需一次幂等迁移 |
| 15 | F-32 | 方案 B 必发新告警（弃 ref 列去重） | — |
| 16 | F-23 | CSV 走标准库实装（弃砍白名单） | — |
| 17 | F-22 | 仅前端 Number()（后端不开字符串宽容） | 宽容案会静默同类前端 bug |
| 18 | 观察2 | reset 失败族改 4xx（随 UAT 脚本同批改断言）；forgot 防枚举不动 | 存量脚本红 |
| 19 | F-15 | 货币保真指令照加；双十一词条进行业包 | — |
| 20 | F-19/F-05/F-38取证/F-21披露/F-24租户侧入口/F-26企业承载 | **不施工**，保持决策项挂账 | 见各条 |

---

## 五、施工记录（步骤 4，随批追加）

### 批 A（后端低风险小改）✅ 2026-09-25 晚
- F-40：`billing/quota.go` 导出 `NewQuotaErr(msg,code)`；`billing_api.go` 余额闸改带码出错（保组织墙文案，SSE error 帧恢复 `insufficient_balance` 码）。
- F-43：`store/billing.go CreateInvoice` 两处 `sql.ErrNoRows` → 统一 errTxt「订单不存在或不可开票（仅已支付订单可开具发票）」（不存在/跨租户同文案不泄露存在性）；`admin_billing.go` 开票 handler 去 `err.Error()` 拼接，改走 `publicErrMessage`。
- F-32：`store/alerts.go` 新增窄方法 `CreateAlertPerOrder`（跳静音+open 去重直 INSERT）；`pay.go` 付款声明改走该方法——每条声明在告警中心独立留线索。
- F-33：`pay.go` 站内信收件人改 `admin`+`super_admin` 双角色合并遍历（对齐 RoleLevel 判定口径）。
- F-09 后端半：`pay.go` 渠道白名单后补 `channel==mock && payMode!=mock → writeError(ErrValidation)`（AGENTS §一·8 口径，新增分支零内联）。
- F-04：`service/ticket.go` worker 死信钩子——`mail_send` 且 attempts≥max 时 `CreateAlert(0,critical,mail_dead,…to/subject/err)`（走普通 CreateAlert 去重防 SMTP 全挂刷屏，细节留 jobs 表）。
- F-14 后端半：`runTextTicket` 入口补 `tenant.WithMode(ctx, mode)`，空 mode 按库口径归一 pro（台账「模式」列数据源接通）。
- `go build ./...` 全绿；批 A 断言（invoice 三态文案/mail_dead 告警/per-order 告警/super_admin 站内/mock 渠道 400/biz_mode 落账/QuotaErrCode 等值）随批末统一落测试文件。

### 批 B（计费与配额域）✅ 2026-09-25 深夜
- F-34（补审单防重）：`store/billing_refund.go:17 BillingRefundMigrate` 幂等补 `orders.reopen_from_order` 列，`store.go:77` 挂进 Store.New 迁移链；`store/billing.go:1572` 判据——同一超时取消原单已有在途补审单则复用返回，不再新造（生产 1→2 张死单即此洞）。`api/pay.go:365` 调用点签名不变。
- F-36（升级结转回收闭环）：`billing_refund.go:29 reclaimCarryOfRefundedUpgradesTx` 在 RefundOrder 事务句柄内按 `upgrade_from_order` 反查「已退款的升级子单」，把其 `quota_grants source='order_carry'` 剩余份额清零；`billing.go:1793` 接入退款主流程，`:1828` 把回收 token 数写进退款留痕 summary。语义边界：子单仍 paid 时 carry 保留（对应旧单折抵对价仍在期内）。此前「先退新后退旧」可白嫖 carry 额度（生产实测幽灵 3,000 积分）封死。
- F-37（退款后订阅身份改挂）：`billing_refund.go:61 LatestActivePaidSubscription`（paid_at+duration_days 在期判据，days≤0 视为不过期，到期判定放 Go 侧避方言差）；`api/admin_billing.go:382` 退款处理器退掉身份来源单后改挂「剩余最晚支付的一笔在期订阅」，只在确无在期订阅时才清空 `perms.PackageCode`（旧实现无条件清空，双订阅退错一笔即误杀现役身份）。
- F-21①（余额闸文案与码）：随批 A 的 `billing.NewQuotaErr` 收口，本批补出参侧。
- F-21②（配额双墙打架）：`api/billing_api.go:453` 保存链路——未显式传 `max_daily_points` 时按注册口径 1:1 用字符墙值同步积分墙（`MaxDailyTokens = req.MaxDailyChars`），字符墙置 0 且未传积分时把积分墙一并清零，杜绝「改了字符墙、运行时仍被旧积分墙压住」（现网 tenant 1 即 100000 字符/1000 积分此态）；`:422` 给字符墙补上平台钳制键 `quota_max_daily_chars`（缺省 1,000,000，仅超管可改），与 QPS/并发同为 B6 收敛口径，堵住租户管理员自调字符墙的自我提权。
- F-21③（建单拒绝出参口径）：`api/tickets.go:115` 新增 helper `writeGateError`——按 `billing.QuotaErrCode` 分流：余额耗尽 → 402 `insufficient_balance`，日限额/QPS/预算 → 400 `quota_exceeded`，文案原样透传；5 处闸站点（文本建单 gate+预检 `:204/:211`、文件建单 `:355/:375`、手动 run `:453`）由旧「200 + success:false」改为结构化 4xx（AGENTS §一·8）。兼容核证：前端 `core.ts request()` 对 4xx 抛 ApiError →  toast 走同一 message；UAT 脚本 T40/T17 只 grep 响应体（`"success":false` + 积分数值 + 无 token 子串），`writeError` 的 compact JSON 命中不变，`trace_id`/`QUOTA_EXCEEDED` 均不含 "token" 字样。
- F-16（总览余额读错桶·出参半）：`api/admin_evals.go:64` 在 `balance` 视图补 `total_points`＝`TenantRemainTotal(grants+permanent)` 折算积分，与收银台 `balancePayload` 同口径；旧 `balance_points` 只是永久桶，只持有体验/订阅额度的租户显 0。⚠ 该出参无 httptest 锁（`handleSystemHealth` 需 kb `DB.Stats` 装配），**改挂步骤 5 生产实测**：用只持有 grants 的租户取 `/api/system/health`，断言 `total_points>0` 且 ≠ `balance_points`。
- F-12（充值尺子重锚·代码半）：`store/billing.go:1166` 缺省 29900→33222 分/百万 token，`:2158` 种子值同步；`api/pay.go:22` 注释订正。数学：1 积分=300 token，3,000 积分=90 万 token → (900000×33222+500000)/1e6 = 29900 分 = ¥299.00 恰面值（旧尺子复现 ¥269.10，与套餐面值/管理台代充三口径打架）。⚠ `GetConfig` 优先于代码缺省，**生产 `system_config` 现值仍是 29900，改值动作留批 H**。
- F-12 连锁修（两处既有测试）：`api/coupons_api_test.go:271` 25%/75% 折让容差 0.005→0.011、`:351` 五折由严格相等改为差值 ≤0.011——整数分取整在奇数分原价下天然有 1 分差，旧断言只在偶数分原价下成立（假精确），非语义放宽；中文注释已随批写明。
- 词条登记：批 A 的 `pay.go` 渠道互锁写死中文「当前支付模式下不可选择模拟支付渠道」补进 `scripts/gen_i18n_catalog.py`，`--apply` 重生成 `internal/i18n/catalog_en.go`（exact 386→387），`TestAPICnMessageLiteralsCovered` 复绿。
- 断言落位：`store/billing_uat_batchb_test.go`（F-34 三连幂等 / F-36 退子→carry 保留→退旧归零 + 场景 B / F-37 在期·永不过期·过期·全退四态 / F-43 文案 / F-32 去重对照 / F-12 尺子等值+旧值差复现，6 例全 PASS）；`api/billing_uat_batchb_test.go`（F-21② 四场景含 GET 出参与钳制、F-21③ 四子用例含「禁止回 200」负向，全 PASS）。
- 本批验证：`env DB_DRIVER=sqlite go test -count=1 ./internal/api/ ./internal/store/` 双包 ok；`tools/check_doc_comments.py` go/fe 均 0 缺口；gofmt 无差异。⚠ 计费域按 AGENTS §一·4/§二，PG 方言 `run_uat.sh` 那跑才算发布闸门，随步骤 5 统一执行。

### 批 C（F-42 假 completed 专项·五层链条收口）✅ 2026-09-25 深夜
- F-42-a（cause 不再被压扁）：`orchestrator/flow.go:121` 步间取消由 `return ctx.Err()` 改 `return context.Cause(ctx)`——实时计费的欠费中止走 `engine.go:232/236` 的 `WithCancelCause(store.ErrInsufficientBalance)`，旧写法把它压成 `context.Canceled`，真因留在 cause 里无人读；同函数步骤失败的 `fmt.Errorf("流程步骤 %s 失败: %s")` 改 `%w`（文案逐字不变）保住错误链。`service/ticket.go:374` 失败收尾加 `errors.Is(runErr, store.ErrInsufficientBalance)` 分支，改写为与预检 `:293` **逐字一致**的 `insufficient_balance: 余额不足，请充值或升级套餐`（88/90 两型合流，OpenAPI 侧按前缀取码），站内信正文由裸 `runErr.Error()` 改取该可读文案。
- F-42-b（驳回来源分列）：`store/tickets.go` 新增 `reject_source` 列（`TicketRejectSourceMigrate` 走 `db.EnsureColumns`，挂在 `store.go:54` 迁移链；新库建表 DDL `store.go` 同步补列）+ 常量 `RejectSourceHuman/System`；读写四通道全部带列（GetTicket/GetTicketGlobal/GetTicketByNo/ListTickets 的 SELECT+Scan、`UpdateTicket` 的 SET、`FinishTicket` 的 SET——漏任一条都会让刚标的来源被静默清掉）。写入点：人工驳回唯一入口 `api/tickets.go` 审批 reject 置 `human`；系统三处（flow 步骤失败、service 预检、service 失败收尾）置 `system`。判据侧 `orchestrator/workflow.go:runAIInitial` 由「reject_reason 非空即重翻」收紧为三条件：非空 && `reject_source=='human'` && `payloadHasTranslation(p)`（新纯函数，载荷全空绝不空转 return nil，落回正常翻译分支＝真调用模型）。
- F-42-c（认领收窄）：`store/tickets.go:ClaimTicketForRun` 的 WHERE 由 `status IN ('draft','queued','rejected')` 改为 `status IN ('draft','queued') OR (status='rejected' AND COALESCE(reject_source,'')='human')`——系统类 rejected 不再与队列 MarkFailed 的 attempts<max 自动回队合成无限重跑。用户显式重跑不受影响：`api/tickets.go:handleTicketRun` 入队前已把状态写回 queued。
- F-42-d（交付判据一处实现两侧同源）：`api/tickets.go` 新增纯函数 `ticketDeliverable(t, files)` + 取数包装 `(*Server).ticketHasDeliverable`（文件工单看子文件 result_path/text_result_path 或工单级产物列；文本工单看载荷 translations 至少一种非空；读库失败保守判 false）。三个消费点：① `api_openapi_tasks.go:handleOpenAPITaskStatus` completed 映射——无产物降级 `failed`（欠费前缀取 `insufficient_balance`，否则 `task_failed`），绝不再回空 translations；② 同文件 `handleOpenAPITaskDownload` 加同口径 `not_ready` 拦截（旧实现只判 status，会打零字节 zip）；③ 租户 UI `handleTicketDownload` 在 status 校验后加无产物拒绝（鬼单不再露下载钮）。三处均沿用各站点既有出参形态（openapi 用 `writeTaskError` 的 200+error_code、UI 用同 handler 的 200+success:false），不新增内联 4xx、不触 `TestErrorStyleRatchet`。
- 词条登记：批 C 三句新写死中文（UI 下载拒绝、OpenAPI 详情/下载无产物）补进 `scripts/gen_i18n_catalog.py`，catalog_en.go 重生成 exact 387→390。
- 断言落位：`store/tickets_f42_test.go`（来源往返四通道 / 认领六态含两向负向 / 老库幂等补列后空串行不自动认领）、`orchestrator/f42_flow_test.go`（cause 透传 + 后续步骤零执行 / 步骤失败保链且落 system / `runAIInitial` 四态分支走向 A~D）、`api/ticket_f42_test.go`（`ticketDeliverable` 七形态 / 状态接口降级 failed + 真产物单不被误降 / 下载端点 not_ready）。
- **反向验证（守卫不是假绿）**：把 d) 的降级条件短路、把 c) 的 WHERE 改回旧式、把 b) 的判据退回「非空即重翻」三处分别回滚后实跑，断言全部转红且报出缺陷本体——短路版状态接口原样吐出 `"status":"completed"…"translations":{"en":""}`（与生产任务 90 字节同形），回滚版认领「系统驳回实得 1 行」，判据回退版 A/C/D 三态「译文仍为空」。恢复副本后复绿。
- **存量订正 SQL（批 H 执行，此处留档）**：先核对再改，幂等（判据与 ticketDeliverable 同语义）：
  `SELECT id,tenant_id,status,reject_reason FROM tickets WHERE status='completed' AND reject_reason<>'' AND COALESCE(final_result,'') LIKE '%"en":""%';`
  确认仅 90 号后：`UPDATE tickets SET status='rejected', reject_source='system' WHERE id=90 AND status='completed';`
  （reject_source 老行留空串口径已在测试 ③ 钉死：不再自动认领，需用户点重跑。）
- 本批验证：`go build ./... && go vet ./...` 绿；`env DB_DRIVER=sqlite go test -count=1 ./internal/store/ ./internal/api/ ./internal/orchestrator/ ./internal/service/` 四包 ok（含批 B 用例）；`tools/check_doc_comments.py` go/fe 0 缺口；gofmt 无差异（注：gofmt 会把注释里的 ASCII 空串写法 `''` 变形为 `”`，本批注释一律写「空串」避开）。⚠ 工单流水线是 F-42 主战场，步骤 5 必须以 PG 方言跑 `run_uat.sh` + 生产实测任务 90 订正后不再露下载钮。

### 批 D（生成守卫与提示词）✅ 2026-09-25 深夜

**F-38 短格长度爆炸（生成侧污染）——第 5 判据 + 文本通道复核 + 可疑率哨兵**
- `engine/translation_guard.go`：新增 `hasLengthExplosion`（src≤12 rune ⇒ out ≤ min(max(8×src,80),300) rune，
  阈值 env 可配：`LC_GUARD_SHORT_MAX_SRC_RUNES/RATIO/FLOOR_RUNES/ABS_CAP_RUNES`，非法值回退保守默认）并入
  `IsTranslationUsable` 第 5 判据；`LengthExplosionInfo` 输出 src/out/倍数/上限 一行可聚合字段。
  文件链 5 个写入点（file.go:507/553/595/679/696）+ `collectKBPass` 零改动自动获益——命中走既有
  `missingSegments`→补漏/逐段兜底链（AST 装配锁 `TestFileTranslationWritePointsAllGuarded` 写入点基线仍 5）。
- `engine/engine.go singleLangRaw`（文本通道产物直接交付、不经文件写回点）：返回前同判据复核 ⇒
  带反馈回退重译 ≤2 次（与缩翻硬闸同范式），每次命中打 `observability.Warn`
  （provider/model/lang/detail=长度比，即「生成侧可疑率」哨兵）；重译仍爆炸 ⇒ 返回错误，
  交 `translateLangsConcurrent` 既有 3 轮重试队列，最终置空由漏译率硬闸/漏翻可见性告警兜底（qa_error 落点）。
  **不做 few-shot 黑名单**（按修复文档口径）。
- 断言：`translation_guard_test.go`（6 字源×2506 rune "Dear Valued Customer…" 样本=false、
  80/81 与 96/100 边界等值、>12 rune 源不生效、缩写展开 AWS→亚马逊云科技 反向不误杀、
  env 覆写与 absCap 兜底、collectKBPass 爆炸行进补漏+计入未译）；
  `f38_singlelang_test.go`（httptest 假上游装配级：首爆→重译过闸采纳；稳定爆炸⇒err≠nil 且
  **上游调用恰 3 次**等值锁；正常长文零重试一次通过——交付物不再可能出现 "Dear "）。
- 供应商取证清单（腾讯 Hunyuan-MT 工单素材）按修复文档保持挂账，代码侧无法闭环。

**F-15 货币保真指令**
- `engine.go`：`translateInstruction` 薄委托 = `translateInstructionCore + currencyInstructionNote(uiLang)`，
  调用点零改动；条款点名 ¥/CNY/RMB/yuan 并显式禁止替换为 R/$/€/£、金额数值不变；
  zh/en 两套界面语言、全部目标语言分支统一携带（哈萨克/ja/ko/默认全过）。B4 缓存前缀随内容变更自然刷新一次。
- 断言：`f15_currency_test.go` 逐 (target×uiLang) 组合等值锁 + 关键词锁。
- 活体探针（LIVE_LLM 门控、断译文不含 R300/R50）留到步骤 6 自动化批一并入 `e2e-manual/`。

**F-41 建单余额预检估算重写**
- `api/tickets.go`：`estimateTicketTokens(chars, langs, mode, kPro, kFast)` = chars×langs×K(mode)；
  K(pro)=160、K(fast)=60 走 system_config `est_tokens_per_char_pro/fast`（`estTokensPerChar` 读取，
  缺省/非法一律回退默认——配置坏掉绝不放大放行）；`precheckTicketBalance` 增 mode 形参，
  文本建单传 `normalizeTaskMode(req.Mode)`、文件建单在预检前归一化（与 t.Mode 落库同口径）。
  「部分交付/断点续翻」按决策项 3 本批不做。
- 断言：`ticket_balance_test.go` 重钉（7 类等值：pro/fast/空 mode 归 pro/边界 0/K 非法回退）+
  单调性；`ticket_estimate_f41_test.go` 字节基准锁——88 案 chars=3,748×3×160=**1,799,040**（等值）
  ≥ 实烧 1,075,400；89 案 32,444×3×160 ≥ 外推 930 万；K 配置可调+非法回退专项。
  ⚠ 上线首周按 usage_ledger P99 回调 K（批 H 动作清单）。

**F-29 后端半（chat 通道护栏）**
- `stream.go handleChatStream`：① 体积闸在 **sseHeaders 写出前**——`chatTextOverLimit`（rune 口径、
  TrimSpace 后计数）+ 上限键 `chat_max_chars`（默认 5,000，非法回退），拒绝走
  `s.writeError(apierrors.ErrChatTextTooLong)`；② 管线包 `context.WithTimeout(ctx, 90s)`，
  到点出 error 终帧「处理超时，长文本请改用翻译工单」（error_code=chat_timeout），不发假 done。
- `errors/codes.go`：新码 `ErrChatTextTooLong="chat_text_too_long"`→400 映射登记。
- ⚠️ 口径勘误：统一 `writeError` 出参字段是 **`code`**（非修复文档所写 `error_code`）——
  步骤 6 的 UAT 断言按 `grep -E 'chat_text_too_long'` 命中体即可。
- 断言：`stream_f29_test.go`（5,001 字 ⇒ 400 JSON、Content-Type 非 event-stream、体内无 `data:` 帧、
  code 等值；配置收紧到 10 后 11 字被拒实证接线；边界/rune 口径等值锁；400 映射锁）。

**闸门与本批记录**
- `scripts/gen_i18n_catalog.py` 登记超时帧文案 → catalog_en.go exact 390→**391**。
- 本地全绿：`env DB_DRIVER=sqlite go test -count=1` engine/api/store/orchestrator/service/errors/i18n 全 ok；
  `gofmt -l` 清零、`go build ./... && go vet ./...` 干净；`tools/check_doc_comments.py` go/fe 0 缺口。
- 反向验证（摘锁即红）：摘第 5 判据 ⇒ 爆炸/边界两用例红；摘 singleLangRaw 复核 ⇒ 两装配用例红
  （仍交付 2506 rune 垃圾）；货币条款改回裸 core ⇒ f15 两用例红；摘 chat 体积闸 ⇒
  RejectedBeforeSSE 红（穿透到引擎）。全部回锁复跑绿（cp 备份回滚，未动 git）。

### 批 E（F-17 邮件语种跟随）✅ 2026-09-25 深夜

- **①注册语言落库**：`register.go` 载荷补 `app_lang` 字段；`store.UserLangMigrate()`（`db.EnsureColumns` users.`preferred_lang TEXT NOT NULL DEFAULT ''`）挂 `store.New` 编排点（PersonaMigrate 之后）；`iam.Set/GetPreferredLang` + `store/users.go` 薄委托（与 SetJobRole 同款 WHERE id AND tenant_id）。12 码白名单（`mailUILangNames`，与前端 `Lang` 联合类型逐字对齐：zh/zh_hant/en/ru/fr/ar/es/pt/de/ja/ko/th）+ 连字符归一（zh-hant→zh_hant），白名单外一律落空串=中文链路（job_role 同口径）。
- **②链路带 lang**：`getMailTpl(code, lang)` 五级链（低→高逐字段覆盖）：内置中文母稿 → 自定义 `{code}`（无后缀=中文，**老配置零迁移**）→ 内置英文稿（仅验证码类 `DefaultEn`，**超管只配中文稿时非中文语种吃整套英文、不产中英混血邮件**）→ 自定义 `{code}.en` → 自定义 `{code}.{lang}` 精确键（zh_hant 可专配；中文系不套任何英文级）。`sendTemplatedMail/sendManualEmail/loadManualPDF/sendEmailCode` 全部穿透 lang；`loadManualPDF` 新键 `manual_pdf_dir`：`{lang}.pdf`（另试连字符变体文件名，对齐交付包 `LangCross-User-Guide-zh-hant.pdf` 命名）→ en → zh → **旧单文件链终兜底**（manual_pdf_path/env/默认路径），空语种按 zh 处理（不先吃 en.pdf）。附件名 12 语种本地化（`manualPDFNames`）；正文问候语中英两稿（zh 系中文、其余回落 en）。
- **调用点口径**：register（manual + enterprise_reg）用注册语种；`handleEmailCode` 载荷 app_lang→X-App-Lang 头；`handleMeEmailCode` 头→账号存量 preferred_lang；forgot-password（reset_code）读账号 **preferred_lang**（邮件语种不信任请求头，账号存量为准）；tenant_notify/user_import/alert **本批传空=中文**（正文由触发方中文字面量传入/收件人为运营侧，随模版配置成熟再放开）。模版管理台 UI 本批不加语种维度（临时口径：超管直配 system_config，PUT 校验只认无后缀码——点分键经此通道写入会被拒，直改 system_config 可行；老 PUT 零改动且 merge 保留点分键）。
- **顺带修复**：`sendManualEmail` 历史直挂未渲染的 `tpl.Subject`（{brand} 占位符原样进邮件主题）——改走 `renderMailTpl`，由 `buildManualMailMsg` 纯函数锁定。
- **前端**：`api/auth.ts` 单点收口——`authRegister` 载荷自动注入 `app_lang: currentUiLang()`（Login 传统表单与 AI 接管流共用同一函数，两分支零改动全覆盖）；`sendEmailCode` 体补 app_lang（该请求不走 authHeaders，无头可兜）。X-App-Lang 头链路（〇-S #12）已存在，后端 requestMailLang 双判据（**载荷优先、头兜底**）。
- **断言（全部实测红绿互换）**：`store/user_lang_test.go`（迁移幂等+读写回路+跨租户守卫+清除+查无报错）；`api/mail_f17_test.go`（白名单归一表、载荷/头提取序、**五级链等值锁含「验证码只配中文稿→th 整套英文、主题不得残留中文」**、t.TempDir 假 PDF 命中序含连字符/空语种 zh 头位/旧单文件回落/全空报错文案、buildManualMailMsg 附件名+问候语+{brand} 渲染）；`api/register_lang_f17_test.go`（**直调 handleRegister 端到端**：落库/头兜底/非法载荷由头顶上/双空落空串/zh-hant 归一）。
- **测试装配两坑（新库形态首踩，口径记录）**：①注册链路有并发 goroutine 查库——裸 `:memory:` 每连接独立空库（no such table 随机红），钉单连接又在持 Rows 嵌套查询处池死锁（整轮挂死），**必须用命名共享缓存 `file:<名>?mode=memory&cache=shared`**；②同 IP 连发注册默认被护栏拦（日 3 次+间隔 60s），harness 里 `register_ip_min_interval_sec=0` + `register_ip_daily_limit=50`，护栏本体另有专测。
- **反向验证**：摘 `DefaultEn` overlay ⇒ GetMailTplFallbackChain 红；摘 register `SetPreferredLang` ⇒ RegisterAppLangPersists 红；摘 loadManualPDF 空语种→zh 头位 ⇒ LangChain 红（实测抓到「空语种先吃 en.pdf」）。三处回锁后复跑全绿。
- **验证**：`go build/vet ./...` 干净；api/store/iam 三包整包 sqlite 快跑全 ok（9.0/10.0/0.5s）；`check_doc_comments` go/fe 0 缺口；gofmt 清零；前端 `tsc --noEmit` 0 错 + `src/api` vitest 48/48 绿。
- **止血项闭环**：处方「旧 6,522B 线上 manual.pdf 由批 H 铺 12 份新 PDF 替代」不变——批 H 现在只需铺 `manual_pdf_dir` + `SetConfig manual_pdf_dir` 一步，代码侧命中/回落序已由本批锁定。

---

### 批 F（F-08 / F-23 / F-31 / F-35）✅ 2026-09-25 深夜

**F-08（KB temp_id 生成侧与判据侧串长不同源）**
- `kb.go:543` `TempID: randHex(12)` → `randHex(24)`，与 `tempIDRe=^[0-9a-f]{24}$`（:40）严格同源；
  同步修正 :38 / :601 两处注释口径（旧注释写 12 位与正则矛盾）。`upload_chunk.go:213` 的
  `randHex(12)` 是分片合并**文件名 token**（`mergedNameRe` 按 12 位配死），非 temp_id，有意不动。
- 断言 `kb_tempid_roundtrip_test.go` 五枚：①弱锁格式同源（randHex(24) 命中白名单、randHex(12) 必不命中，
  防反向偷宽正则）；②强锁 handler 全链往返（multipart 传 CSV→recognize→import 断 200 且 added=2）；
  ③④⑤三条负向（`../../etc/passwd` 穿越载荷 400「temp_id 无效」、12 位旧串 400、
  同 ID 二次导入 400「已过期」=导入成功必须清元信息防重放）。
- 反向验证：生成侧回改 randHex(12) → 强锁第一步即红「识别返回的 temp_id 必须命中白名单，实际 12 位串」。

**F-23（批量导入成员四改，user_import.go）**
- ①表头列索引**首中即停**（`setIfFirst`）：旧 last-wins 实现下，表头行任何含「用户名称/管理员/邮箱」
  关键词的长句会把五列索引一路劫持到说明列，整单静默错位；
- ②填写说明从表头行 F1 挪到示例行 F2（与①构成双保险）；
- ③模板构建抽纯函数 `buildUserImportTemplate()`（模板即回归资产）；解析改扩展名分发：
  `.csv` 走 encoding/csv（FieldsPerRecord=-1 容忍稀疏行），xlsx/xls 走 excelize，
  两路共用 `buildImportRows([][]string)` 纯函数——白名单里有 .csv 而旧解析必拒的历史假象收口；
- ④回执诚实化：`importSuccessMessage(mailSent)` 两版中文文案 + `mailLive()` 通道判据
  （队列可用或 MAIL_ENABLED+SMTP_HOST+SMTP_USER 齐全才算活；**Noop 兜底 Send 返回 nil 但永不外发，
  判为不可用**——处方只说按 nu.Email 分支，实做时把「通道假成功」一并堵上）；
  离线态不再调 send（回执走「线下转告」版）。
- 断言 `user_import_f23_test.go` 五枚：模板往返逐字段等值、旧版说明挂表头行负向锁、
  CSV/xlsx 两路 4 行等值+角色归一+邮箱小写+空名回退、回执两版文案+mailLive 三态、
  handler 全链离线态回执（两行用户都得「线下转告」版）。
- 反向验证三处：撤首中即停→劫持锁红；`.csv`→`.csvx` 判据失效→CSV 锁红；撤 mailLive→离线回执锁红。各自精确红一条。

**F-31（dept_admin 必绑部门，双闸，auth.go）**
- 闸1 create：角色白名单/权限级校验后补 `role==dept_admin && org_id<=0 → 400`——旧实现放行造出
  「未绑定部门的死角色」，当事人所有成员操作反被「未绑定部门」守卫锁死；
- 闸2 update：**合并后终态判据**（finalRole 等级 2 且 finalOrg<=0 → 拒绝），只查请求字段会漏
  「存量无部门账号升角成 dept_admin（org 缺席=沿用 0）」与「改角色同时摘部门」两条降级路径。
- 两个新错误分支一律 `s.writeError(ErrValidation)`（AGENTS §一·8）；两条中文提示已补录
  `scripts/gen_i18n_catalog.py` 并重生成 catalog（exact 391→393）。
- 断言 `auth_deptadmin_org_gate_test.go` 三组：create 无部门 400+不落库/带部门等值落库、
  update 终态两路降级各 400+不改库+对照合法路径、反向验证撤 create 闸/撤终态闸各自翻红。

**F-35（users/update 空字段静默清空，auth.go）**
- `display_name/role/status` 改 `*string` 指针入参，删「status 缺席默认 active」块；
  目标现值前置加载（不存在→404 结构化错误，旧实现 0 行受影响仍回 success）；
  缺席字段取目标现值合并后写库（store 委托签名不动），审计 after 记**合并后真实写库值**；
  越权判据统一改用合并前的 reqRole 字符串，全部原语义保留。
- 锁：只改名→role/status/org 原样保留；先停用→只改角色→status 仍 disabled、姓名未被洗掉
  （旧实现两坑同炸处）。反向验证 finalStatus 回默认 active → 合并锁翻红。

**批 F 验证账**：`TestUATBatchF` 10 用例全绿；`env DB_DRIVER=sqlite go test -count=1 ./...` 全仓 EXIT=0
（api 32.3s / store 29.0s / i18n / iam 全 ok）；build/vet/gofmt 干净；中文注释闸门 go/fe 0 缺口；
反向变异共 7 处，每处恰好只红对应锁。**遗留**：F-31 闸1 会改变后台「建 dept_admin 未选部门」的旧手感
（现 400+中文提示，前端 toast 读 message 链路零改动）；存量「org_id=0 的 dept_admin」若在库里存在，
任何 update 都会被终态闸拦下要求补部门——归批H 数据体检项（见部署批）。

### 批 G（前端批 17 项，一次 dist）✅ 2026-09-25 深夜

施工形态：波A 六组并行子代理（互斥文件区）+ 波B i18n 串行独占序列 + 主代理收尾（独立复跑全部定向测试/键集闸/棘轮、观察2 dom 欠账补锁、chat.timeoutTicket 预插键）。每组均做变异回滚验证；主代理复跑证据：A1+A2+B1 合跑 93/93、A3 17/17、A4 双 TZ 各 7/7、A5 Go 2/2+dom 4/4、B2 73/73、B4 75/75 + build_sdk --check 绿。

- **F-28**：OTP filled 类名三元直出 `'ar-otp ar-otp--filled'`；dom 等值锁（未填格='ar-otp'）。
- **F-02**：codeSentRef→useState；成功翻「在线」/失败回滚，判别性回归锁（旧 ref 实现必超时）。
- **F-03**：TurnstileApi 补 reset，reexec 改 reset→execute（调用序锁 invocationCallOrder）；发码失败复用 lib/useCountdown(60) 冷却禁用（假定时器推进 60s 恢复锁）。⚠️ **处方偏离（步骤6 收口时定）**：原处方另一半「UAT 脚本断连点两次 captcha_token 前 8 位不同」**不入 scripts/uat**——真 Cloudflare Turnstile 无法在 shell/curl 里完成人机挑战，改由 Playwright `e2e/register_captcha_token.spec.ts` 承担（真浏览器过挑战、连点两次取 token 比对），已随 run_uat e2e 段进发布闸门。
- **F-09 前端半**：PlansP mock 支付项改 payMode==='mock' 条件展开；dom 锁 static_qr 下「模拟支付（测试）」精确计数 0 + mock 态恰 1（含正向控制防空跑）。
- **F-22**：OrgP onNuOrgChange 源头 Number()；新建 OrgP.dom.test.tsx 断 typeof org_id==='number' + 反向锁 stringify 不含 `"org_id":"`（拼接构造避自伤）。后端未开宽容。
- **F-20/F-39**：panels_a 删 openMetrics() 与 Prometheus 钮（API_BASE import 保留）；overview.prometheus 键保留+注释标注入口已移除；dom 两角色态负向清零（先等健康卡渲染防假绿）。/metrics 公网探针按 AGENTS §6 归 deploy 冒烟，不进 Playwright。
- **F-16 前端半**：总览余额卡改读 total_points 双桶合计（缺失回落 balance_points 兼容旧后端）；明细行保留；overview.balance 改「组织余额合计 (积分)」×12 份同步；dom 合计 800/明细 300 等值 + 回落锁。
- **观察5**：TaskCenterP 类型标签按 row.period || task_type 兜底（periodKeyOf/periodLabel helper 收敛 :285 同款三元式）；dom 锁 daily+weekly 行「每周」恰 1、「每日任务」旧错标 null。编辑弹窗两值枚举未动。
- **观察2①**：forgotSent 族 24 处值手术（panels/auth.ts zh/en + dicts.zh/en + 10 locale ×2 键）删「（未配置邮件时请在服务端日志查看）」，零新键；pwd.codeNoop 按裁定保留。
- **观察2②**：auth.go handleResetPassword 四失败族 writeError(ErrValidation)→400+code（文案逐字未动，catalog 无新增）；Go 锁=四族 400+VALIDATION_ERROR+文案逐字 + forgot 不存在账号 200+success:true 防枚举锁；dom 锁（主代理补）=Login.reset_4xx 走真 request() 桩 fetch 收 400，断上屏文本精确等于后端 message（非「请求失败 (400)」兜底串）+ 出网载荷等值。错误样式棘轮基线下调 741→740、auth.go 54→53（总数+逐文件同改，sum==total 自洽）。⚠️ 观察项登记：找回第二步失败消息落在 `forgotSent ? 'auth-ok' : 'auth-err'` 槽（Login.tsx :435），会以成功绿样式显示错误文案——提示槽复用的历史遗留，非本批面，挂产品拍板。
- **F-11+F-10**：myPackage() 提 refreshPkgLine 经 PkgRefreshCtx 旁挂可变句柄下发（避开 ChatProvider 时序倒挂），useChat done 帧后 2s 重置式 debounce 调 1 次（1999ms 恰 0/满 2s 恰 1/双帧合并恰 1/卸载作废 四把计数等值锁）；Bell toggle 直接拉列表（stale 判别锁：请求早于 panel commit，旧实现必红）。
- **F-14 前端半**：lib/ui.ts fmtTime 加 local 参数（默认 false 旧行为零影响）；MyBilling :169 + 同文件 :133/:210/:244 裸切片改本地口径（TrendCard 的 UTC 日聚合取数键**不是**显示层，未动）；KbP :753 授权时间列接线（A4 移交）；双 TZ（UTC/Asia/Shanghai）各 7/7 等值锁，期望值由独立 Date 取值器算出（非同义反复），钉数字序列防 ICU 分隔符漂移。
- **F-25**：BrandTermsP 删除钮内 CloseIcon(14)+同排粘连补空格、removeEntry 前置 confirmDialog（时序锁：点钮调用 0→确认才 1）、三处 window.prompt→站内 editDlg（BRAND_LANGS select+langLabel，零新造语种名）；i18n 净 +2 键（bt.delConfirmTitle/Body、bt.editLangTitle、bt.formLangLabel/TextLabel −bt.promptLang/promptText/editPrompt）×12 份；window.prompt 负向清零锁。⚠️ 施工期 ar.ts 译文内嵌双引号未转义炸过一次全仓解析（当场自修），后续组任务书已转成硬约束。
- **F-26**：KbP 安全句 Panel 拆两态——非 super 仅一行 kb.safetyPlatformManaged、表单控件文档级 0 枚；super 反向锁（select 恰 6/input 恰 3/button 恰 3 等值）。
- **F-24**：kb.bitextDone→「已提交，待平台审核」、bitextImport 去「写TM」（zh/en+10 locale 12 份一致）；新键 kb.bitextPendingNote 拼进双语与 TMX 两处回执（整串等值锁）；「已写入/写TM」负向清零。export-tmx pending_review 字段/租户只读入口归产品拍板未动。
- **F-29 前端半**：useChat h6HandleErr 前插 isHtmlErrorBody 拦截（体首 200B 内 `<!doctype` 判据），气泡与 errorMessage 整条替换 chat.timeoutTicket；dom 锁=整串等值 + Ray ID/<!DOCTYPE/Cloudflare 三负向。新键 chat.timeoutTicket 由主代理预插 12 份。
- **F-27**：新 scripts/build_sdk.sh（版本源 pyproject/package.json、latest 别名、内容指纹 .sha256、--check 漂移闸、manifest.json 唯一文件名事实源）；SdkP 假命令删除、安装命令/下载链接运行时现读 manifest（源码零版本字面量负向锁）；dom 反向锁不含 pip install langcross-translator / npm install @langcross；e2e sdk_download.spec.ts 6/6 绿（whl=PK、tgz/sdist=1f8b 按实测魔数、体积下限、非 HTML 兜底判据、版本盘上现读）；Java 卡维持源码分发。`bash scripts/build_sdk.sh --check` 建议并入 §二 闸门清单（步骤8 文档批落）。→ **已落**：AGENTS.md §二 闸门清单与 §一·5 交付链（SDK 与扩展同为「随源发布」）均已收录，《部署指南》§五 步 0-B／§十 验证清单同步补 `/sdk/*` 可达性探针。
- **F-18**：先钉后改三步齐全——UI-ANNOTATIONS.md 新增 §1.5「WordSwap 尺寸档（〇-Q）」（§1.4 已被浮层规格占用，编号顺延）钉两元组：.ws--lg 44/52/32/23/3px→26/30/16/14/2px、.tk-prog-ws 18/17/14→15/16/12；基础档/.draft-ws/.file-segs-ws 不缩（理由入档）；theme.css @media(≤640px) 按钉值实现（宽屏档一字未动，211 行纯增量）；readability J 段 31 测源码级等值+窄屏选择器白名单负向；pixel P2e 360/1440 双视口 computed 等值实跑 1 passed（font-size computed 实测不回舍，13.5px 原样回读；亚像素教训已注）。泰/俄/阿长词折行节拍列 §1.5 观察项。

批H 新增联动项：public/sdk/ 产物随前端 dist 换源即上线（spa.go 静态直出），发版时需确认线上 `/sdk/manifest.json` 与四产物可达（并入部署冒烟）。

## 收尾账（步骤 6/7 收口，2026-09-26 跨零点）

- **闸门终局全绿（推送前复跑）**：`run_uat.sh`（PG）A 95/0 · B 113/0 · T 515/0 · 前端 E2E 70 passed/0 failed（flaky 经 `--last-failed` 复跑甄别，exit=0）；`go test -race` 40 包全 ok；`assist_uat` 48/0；`multi_instance` 8/0；`build_extension.sh --check` ✅（1.2.2）；`build_sdk.sh --check` ✅（python 1.0.0 / ts 1.0.1）；tsc 0、vitest 65 文件 480 用例全绿、vite build 0；注释闸门 `check_doc_comments.py` go 0 / fe 0。
- **闸门自身三修**（本轮抓出的非产品回归，全细节见 PROGRESS「〇-T」节第 3 条）：①多实例就绪预算 20s→45s＋钉 `USER_DATA_DIR=$WORK/udata`（测试备份/堆快照不再写进本机真实应用数据目录）；②T42 假红根因＝假链端口 8902 被外来进程抢占，排障口径与 `MOCK_CHAIN_PORT` 让位法已写进《部署指南》§十四 FAQ；③`dblib.sh` dbcfg SQLite 分支补 `.timeout 5000`、写失败即 FATAL。
- **测试数据清理**：本地开发库备份后删除本轮 UAT 账号/工单/测试租户及关联行（超管测试号 `uat_super_01` 按用户令**保留**）；/tmp 草稿、临时二进制、调试实例全清；`data/tm_embeddings.npz` 被外来临时进程改写两次，均 `git checkout --` 还原提交版（13,457,516 B，sha256 `91c699a4…`）。
- **提交与推送**：代码提交 `5e914c9`（122 文件，+7052/−549，零 `.md`、零 UI 交付包）→ `push_code_only.sh` 干跑判定后 `--apply` 推出 `d892de3`＋并轨 `4a1e20b`；本批全部文档仅本地。
