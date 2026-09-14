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
#   T37 今日修复回归（2026-09-14）：P0-2 org_id=0 越权（子树内放行+未分配 403）/
#       P0-4 auto_charge 仅超管即时入账（租户管理员 pending）/ P0-3 品牌子域 sso_code 登录链
#       （无裸 token + 兑换 + 单次消费）/ P0-5 敏感词 Unicode 归一化（全角/零宽/空格拦截）/
#       P1-15 points 溢出 400 / P1-2 charge_kind 语义枚举守恒
# 注意：所有带复杂引号 body 的 curl 必须「先存变量再断言」，禁止在 ck 内嵌嵌套引号
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
R=$(post "$AH" "{\"tenant_id\":$TAID,\"tokens\":50000,\"money\":0}" /api/admin/orders/create)
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
[ "$B1" = "50000" ] && { PASS=$((PASS+1)); echo "PASS|T1-balance-credit(+$B1)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T1-balance-credit(+$B1, want +50000)"; }
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
R=$(post "$H1" '{"tokens":8888,"channel":"manual"}' /api/pay/create)
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
R=$(post "$H1" '{"tokens":123456,"channel":"mock"}' /api/pay/create)
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
R=$(post "$AH" '{"title":"UAT任务-每日打卡","reward_tokens":5000,"kind":"daily","enabled":1,"sort_order":1}' /api/admin/tasks/save)
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
ck T10-webhook-test '"success"' "$(post "$H1" "{\"id\":$WHID}" /api/webhooks/test)"
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
R=$(post "$H1" '{"tokens":777,"channel":"mock"}' /api/pay/create)
ONO=$(echo "$R" | pv '.get("order",{}).get("order_no","")')
R=$(curl -s $B/api/pay/notify/mock -H "X-Admin-Token: $AT" -H "$J" -d "{\"order_no\":\"$ONO\",\"amount\":1}")
ck T13-notify-wrong-amount '金额不符|验签|拒绝' "$R"

# ---------- T14 权限边界 ----------
R=$(post "$H1" "{\"tenant_id\":$TBID,\"tokens\":100}" /api/admin/orders/create)
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
TID15=$(post "$AH" '{"task_type":"once","title":"T15任务","description":"d","reward_tokens":100,"enabled":1,"sort_order":9}' /api/admin/tasks/save | pv '.get("id") or 0')
[ "${TID15:-0}" -gt 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T15-task-id($TID15)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T15-task-id(got $TID15)"; }

# ---------- T16（G1）并发扣费压力：30 路并发翻译耗尽余额 —— 不透支、不双扣 ----------
T16U="uatuser_t16$(date +%s)"
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$T16U\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"T16压力\",\"email\":\"$T16U@test.com\",\"agreed\":true}" >/dev/null
T16T=$(tok $T16U uatpass123); H16="Authorization: Bearer $T16T"
T16D=$(sq "SELECT tenant_id FROM users WHERE username='$T16U' LIMIT 1" | tr -d '[:space:]')
R=$(post "$AH" "{\"tenant_id\":$T16D,\"tokens\":8000,\"money\":0}" /api/admin/orders/create)
OID16=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
post "$AH" "{\"id\":$OID16,\"tenant_id\":$T16D}" /api/admin/orders/pay >/dev/null
# 个人租户注册自带 30 万试用余额，压测前直接钉到 8000 tokens，逼出真实「抢余额」竞争
dbq "UPDATE balance_accounts SET balance=8000 WHERE tenant_id=$T16D"
AK16=$(post "$H16" '{"name":"t16-key"}' /api/apikeys/create | pv '.get("api_key","")')
TOT0=$(get "$H16" /api/billing/balance | pv '.get("total_available",0)')
D16=$(mktemp -d)
for i in $(seq 1 30); do
  curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer $AK16" --max-time 120 \
    -d "{\"text\":\"并发压力句 $i 翻译测试内容\",\"target_lang\":\"en\",\"mode\":\"pro\"}" -o "$D16/r$i.json" &
done
wait
S16=$(cat "$D16"/r*.json 2>/dev/null | grep -c '"success":true')
E16=$(cat "$D16"/r*.json 2>/dev/null | grep -c 'insufficient\|余额不足\|耗尽\|"success":false')
[ $((S16 + E16)) -eq 30 ] && [ "$S16" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|T16-all-resolved(ok=$S16 refused=$E16)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-all-resolved(ok=$S16 refused=$E16)"; }
sleep 4
TOT1=$(get "$H16" /api/billing/balance | pv '.get("total_available",0)')
[ "${TOT1:-0}" -ge 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T16-no-negative-balance($TOT1)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-no-negative-balance($TOT1)"; }
AL16=$(sq "SELECT COUNT(*) FROM alerts WHERE tenant_id=$T16D AND kind='balance' AND level='critical'" | tr -d '[:space:]')
[ "${AL16:-0}" -le 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T16-no-false-settle-alert(critical=$AL16)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-no-false-settle-alert(critical=$AL16)"; }
L16=$(sq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$T16D" | tr -d '[:space:]')
EQ16=$(python3 -c "print(1 if abs($L16 - ($TOT0 - $TOT1)) < 1 else 0)" 2>/dev/null || echo 0)
[ "$EQ16" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T16-no-double-deduct(ledger=$L16 consumed=$((TOT0 - TOT1)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|T16-no-double-deduct(ledger=$L16 consumed=$((TOT0 - TOT1)))"; }
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
R=$(post "$H1" '{"tokens":40000,"channel":"mock"}' /api/pay/create)
OID21=$(echo "$R" | pv '.get("order",{}).get("id") or 0')
post "$H1" "{\"order_id\":$OID21}" /api/pay/simulate >/dev/null
sleep 4
T21A0=$(get "$H1" /api/billing/balance | pv '.get("total_available",0)')
curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer $AK21" --max-time 60 \
  -d '{"text":"退款窗口计量句 one sentence for consumption window check","target_lang":"en","mode":"pro"}' >/dev/null
sleep 4
T21A1=$(get "$H1" /api/billing/balance | pv '.get("total_available",0)')
CONSUMED21=$((T21A0 - T21A1))
[ "$CONSUMED21" -gt 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|T21-consumption-window(+$CONSUMED21)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T21-consumption-window($CONSUMED21)"; }
R=$(post "$AH" "{\"id\":$OID21,\"tenant_id\":$TAID}" /api/admin/orders/refund)
ck T21-refund-ok '"success":true' "$R"
ST21=$(sq "SELECT status FROM orders WHERE id=$OID21" | tr -d '[:space:]')
[ "$ST21" = "refunded" ] && { PASS=$((PASS+1)); echo "PASS|T21-refunded"; } || { FAIL=$((FAIL+1)); echo "FAIL|T21-refunded($ST21)"; }
RM21=$(sq "SELECT COALESCE(refund_money,0) FROM orders WHERE id=$OID21" | tr -d '[:space:]')
AM21=$(sq "SELECT amount_money FROM orders WHERE id=$OID21" | tr -d '[:space:]')
LT21=$(python3 -c "print(1 if 0 < $RM21 < $AM21 else 0)" 2>/dev/null || echo 0)
[ "$LT21" = "1" ] && { PASS=$((PASS+1)); echo "PASS|T21-partial-refund(实退 $RM21 < 全额 ${AM21} ，已消耗 $CONSUMED21 tokens)"; } || { FAIL=$((FAIL+1)); echo "FAIL|T21-partial-refund(refund=$RM21 amount=$AM21 consumed=$CONSUMED21)"; }

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

# ---------- T26（C26 token 真账）usage/me 展示口径字段 ----------
R=$(get "$H1" /api/billing/usage/me)
ck T26-usage-me-tokens-field '"tokens_available"' "$R"
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
ck T36-me-rate '"points_tokens_rate":300' "$(curl -s $B/api/auth/me -H "$H1")"
ck T36-settings-show '"points_tokens_rate":300' "$(SSET)"
ck T36-settings-rate-zero-reject '"success": *false' "$(SSAVE '{"points_tokens_rate":0}')"
ck T36-settings-rate-400 '"success": *true' "$(SSAVE '{"points_tokens_rate":400}')"
ck T36-settings-rate-echo '"points_tokens_rate":400' "$(SSET)"
SSAVE '{"points_tokens_rate":300}' >/dev/null   # 还原

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
ck T37-subtree-still-ok '"success": *true' "$(post "$HD37" "{\"id\":$MEM37ID,\"password\":\"DeptOk@37x\"}" /api/admin/users/reset-password)"
# 核心断言：org_id=0 的租户管理员，部门管理员重置/停用必须 403
TA37ID=$(sq "SELECT id FROM users WHERE username='t37_ta'" | tr -d '[:space:]')
R37=$(post "$HD37" "{\"id\":$TA37ID,\"password\":\"Hijack@37x\"}" /api/admin/users/reset-password)
ck T37-p0x-reset-org0-403 '未分配部门|无权|权限不足' "$R37"
R37b=$(post "$HD37" "{\"id\":$TA37ID,\"status\":\"disabled\"}" /api/admin/users/update)
ck T37-p0x-update-org0-403 '未分配部门|无权|权限不足' "$R37b"

# ② T37-2（P0-4）：auto_charge=1 时租户管理员订单保持 pending（仅超管可即时入账）
dbcfg auto_charge 1
O37=$(post "$H1" '{"tokens":3000,"money":0}' /api/admin/orders/create)
ck T37-ta-order-pending '"status":"pending"' "$O37"
OA37=$(post "$AH" "{\"tenant_id\":$TAID,\"tokens\":3000,\"money\":0}" /api/admin/orders/create)
ck T37-sa-order-paid '"status":"paid"' "$OA37"
dbcfg auto_charge 0
O37c=$(post "$H1" '{"tokens":3000,"money":0}' /api/admin/orders/create)
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

DUR=$(( $(date +%s) - START ))
echo "==T-PASS=$PASS FAIL=$FAIL DUR=${DUR}s=="
exit 0