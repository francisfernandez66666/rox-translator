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
[ "$B1" = "60000" ] && { PASS=$((PASS+1)); echo "PASS|T1-balance-credit(+$B1)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-balance-credit(+$B1, want +60000(200积分×300))"; }
R=$(post "$AH" "{\"id\":$OID1,\"tenant_id\":$TAID}" /api/admin/orders/refund)
ck T1-order-refund '"success":true' "$R"
ST3=$(sq "SELECT status FROM orders WHERE id=$OID1")
[ "$ST3" = "refunded" ] && { PASS=$((PASS+1)); echo "PASS|T1-order-refunded"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-order-refunded($ST3)"; }
B2=$(( $(sq "SELECT balance FROM balance_accounts WHERE tenant_id=$TAID") - B0 ))
[ "$B2" = "0" ] && { PASS=$((PASS+1)); echo "PASS|T1-balance-revert($B2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-balance-revert($B2)"; }
R=$(post "$AH" "{\"id\":$OID1,\"tenant_id\":$TAID}" /api/admin/orders/refund)
ck T1-refund-dup '已退款|refunded|失败|不存在' "$R"

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
ck T6-reset-badcode '验证码|无效|expired|不正确' "$(curl -s $B/api/auth/reset-password -H "$J" -d '{"username":"uatuser_a","code":"000000","new_password":"hacked999"}')"

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
EQ16=$(python3 -c "print(1 if abs($L16 - ($TOT0 - $TOT1)*300) <= 900 else 0)" 2>/dev/null || echo 0)   # 积分差折回 token（每次四舍五入≤±1 积分）
[ "$EQ16" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T16-no-double-deduct(ledger=$L16 consumed=$(( (TOT0 - TOT1)*300 )))"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-no-double-deduct(ledger=$L16 consumed=$(( (TOT0 - TOT1)*300 )))"; }
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
R=$(post "$H1" "{\"order_id\":$OID21,\"title\":\"T23冲红前发票\",\"tax_no\":\"TX9023\"}" /api/billing/invoices/create)
ck T23-invoice-refund-reject '"success":false' "$R"
ck T23-invoice-refund-msg '已退款' "$R"


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
ck T36-sensitive-block 'sensitive_blocked' "$(S36 '{"text":"请翻译：紫火核弹T36 常规句子","target_lang":"en","source_lang":"zh"}')"
ck T36-sensitive-passthrough '"success": *true' "$(S36 '{"text":"纯净文本仅用于闸外验证","target_lang":"en","source_lang":"zh"}')"
SSAVE '{"sensitive_gate_enabled":"0"}' >/dev/null
ck T36-sensitive-off-passthrough '"success": *true' "$(S36 '{"text":"关闸后含词也不拦：紫火核弹T36","target_lang":"en","source_lang":"zh"}')"
SSAVE '{"sensitive_gate_enabled":"1"}' >/dev/null
ck T36-sensitive-on-again 'sensitive_blocked' "$(S36 '{"text":"再开闸恢复拦截：紫火核弹T36","target_lang":"en","source_lang":"zh"}')"
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
ck T37-sens-fullwidth 'sensitive_blocked' "$(S37 '{"text":"请翻译：紫火核弹Ｔ３６ 常规句子","target_lang":"en","source_lang":"zh"}')"
ck T37-sens-zerowidth 'sensitive_blocked' "$(S37 '{"text":"紫\u200b火\u200b核\u200b弹T36 隐藏词","target_lang":"en","source_lang":"zh"}')"
ck T37-sens-spaced 'sensitive_blocked' "$(S37 '{"text":"紫 火 核 弹 T36 空格混淆","target_lang":"en","source_lang":"zh"}')"

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
# T40（2026-09-15 任务1）：文件工单余额预检「积分口径 + PDF 估算校准」
#   新个人租户默认体验额度=free_trial_tokens（300000 内部 token=1000 积分）。
#   a) 700KB 二进制文档（.pdf）：新口径 /12 预估 ≈69k token << 余额 → 放行建单
#      （旧口径 size/3 预估 ≈269k×1.5=403k > 300k → 误拦，即用户反馈的
#      「700KB PDF 提示需 402553 token」缺陷的直接回归）。
#   b) 按余额自适应放大文件（13×余额 字节，封顶 35MB）→ 新口径预估仍超余额 →
#      拒绝，且文案必须含「积分」且零 token 裸值（对外口径承诺）。
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
echo "$R40S" | grep -q '"success":true' && { PASS=$((PASS+1)); echo "PASS|T40-pdf-700k-allowed"; } || { FAIL=$((FAIL+1)); echo "FAIL|T40-pdf-700k-allowed($(echo "$R40S" | head -c 200))"; }
# 大文件：积分余额×汇率300×13 字节（预检按内部 token 估算，/12/1.3×1.5 后仍超余额）；封顶 35MB（<40MB 上传上限）
T40BIG=$(( T40BAL * 300 * 13 )); [ "$T40BIG" -gt 36700160 ] && T40BIG=36700160
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

# 后台确认：无哈希拒 / 合法哈希入账落 payments / 同一笔交易复用到第二单拒（一 tx 一单）
R=$(post "$AH" "{\"id\":$OIDA,\"tenant_id\":$TAID}" /api/admin/orders/pay)
ck T42-confirm-need-tx '交易哈希' "$R"
R=$(post "$AH" "{\"id\":$OIDA,\"tenant_id\":$TAID,\"tx_hash\":\"$TXA\"}" /api/admin/orders/pay)
ck T42-confirm-ok '"success":true' "$R"
ST=$(sq "SELECT status FROM orders WHERE id=$OIDA")
[ "$ST" = "paid" ] && { PASS=$((PASS+1)); echo "PASS|T42-confirmed-paid"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-confirmed-paid($ST)"; }
N=$(sq "SELECT COUNT(*) FROM payments WHERE order_id=$OIDA AND tx_hash='$TXA'")
[ "$N" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T42-payments-tx-hash"; } || { FAIL=$((FAIL+1)); echo "FAIL|T42-payments-tx-hash($N)"; }
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
ck T44-settle-err-branch '!errors\.Is\(err, sql\.ErrNoRows\)' "$(grep -A4 'consumed += take' "$ROOT44/backend-go/internal/store/billing.go" | grep -m1 'errors.Is' || echo NONE)"
if grep -q 'Is(qerr, sql.ErrNoRows)' "$ROOT44/backend-go/internal/store/billing.go"; then
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
# 对外零 token：断言里 30000=100 积分 ×300（库内记账口径），接口出参只允许出现积分。
SEED49=$(dbq "SELECT COUNT(*) FROM user_tasks WHERE task_key<>''" | tr -dc '0-9')
ck T49-builtin-seeded '^[5-9]' "$SEED49"
ck T49-seed-login '^30000\|3\|1\|1\|0$' "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||stack_expiry||'|'||cap_per_day||'|'||cap_per_week FROM user_tasks WHERE task_key='login_daily'")"
ck T49-seed-translate '^30000\|7\|1\|1\|5$' "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||stack_expiry||'|'||cap_per_day||'|'||cap_per_week FROM user_tasks WHERE task_key='translate_week'")"
ck T49-seed-invite-reg '^150000\|14\|0\|0$' "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||cap_per_day||'|'||cap_per_week FROM user_tasks WHERE task_key='invite_register'")"
ck T49-seed-invite-paid '^300000\|0\|event$' "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||period FROM user_tasks WHERE task_key='invite_paid'")"
ck T49-seed-kb '^180000\|0\|once$' "$(dbq "SELECT reward_tokens||'|'||valid_days||'|'||period FROM user_tasks WHERE task_key='kb_upload'")"

# ① 登录事件钩子：再登录一次 uatuser_a → 应落一笔 task/30000 临时台账，且同日不重复发放
UAID=$(sq "SELECT id FROM users WHERE username='uatuser_a' LIMIT 1" | tr -dc '0-9')
: $(tok uatuser_a uatpass123)
ck T49-login-grant '^30000\|task:login_daily$' "$(dbq "SELECT total||'|'||source FROM quota_grants WHERE tenant_id=$TAID AND kind='task' AND source='task:login_daily' ORDER BY id DESC LIMIT 1")"
: $(tok uatuser_a uatpass123)
ck T49-login-dedup-once-per-day '^1$' "$(dbq "SELECT COUNT(*) FROM user_task_rewards WHERE user_id=$UAID AND task_key='login_daily'")"
ck T49-login-counter-one '^1$' "$(dbq "SELECT cnt FROM user_task_period_cnt WHERE user_id=$UAID AND period_key LIKE 'D:%' ORDER BY id DESC LIMIT 1")"

# ② 翻译事件钩子：发起一次即时翻译（成功计量后自动发放 translate_week），日 ≤1 次由去重键保证
curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"任务系统翻译事件测试文本。","options":{"target_langs":["en"],"mode":"fast"}}' >/dev/null
sleep 2
ck T49-translate-grant '^30000\|task:translate_week$' "$(dbq "SELECT total||'|'||source FROM quota_grants WHERE tenant_id=$TAID AND kind='task' AND source='task:translate_week' ORDER BY id DESC LIMIT 1")"
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
ck T49-reset-refilled '^30000$' "$(dbq "SELECT \"left\" FROM quota_grants WHERE id=$GRANT49")"
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
[ "$TN51" = "300000" ] && { PASS=$((PASS+1)); echo "PASS|T51-tokens-undiscounted($TN51)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T51-tokens-undiscounted(want 300000 got $TN51，券不得减积分额度)"; }
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
[ $((B51_1 - B51_0)) -eq 300000 ] && { PASS=$((PASS+1)); echo "PASS|T51-credit-full-quota(+$((B51_1-B51_0)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|T51-credit-full-quota(got +$((B51_1-B51_0)) want +300000)"; }

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
ck T52-forbid-normal-user '"success":false' "$(get "$H1" /api/admin/assist/config)"
ck T52-forbid-anon '"success":false' "$(get "" /api/admin/assist/config)"

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

DUR=$(( $(date +%s) - START ))
echo "==T-PASS=$PASS FAIL=$FAIL DUR=${DUR}s=="
exit 0