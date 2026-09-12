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
# 注意：所有带复杂引号 body 的 curl 必须「先存变量再断言」，禁止在 ck 内嵌嵌套引号
# 依赖：mock_llm.py 已启动、uat 服务已启动（run_uat.sh 编排）
# 用法：BASE_URL=... UAT_DB=... ADMIN_PASS=... bash scripts/uat/api_uat_txn.sh
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
T6A=$(tok uatuser_a uatpass999)
[ ${#T6A} -gt 30 ] && { PASS=$((PASS+1)); echo "PASS|T6-login-new-pwd"; } || { FAIL=$((FAIL+1)); echo "FAIL|T6-login-new-pwd"; }
ck T6-old-pwd-rejected '密码|失败|incorrect|invalid|UNAUTHORIZED' "$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"uatpass123"}')"
H6A="Authorization: Bearer $T6A"
R=$(post "$H6A" '{"old_password":"uatpass999","new_password":"uatpass123"}' /api/auth/change-password)
ck T6-restore-pwd '"success":true' "$R"
ck T6-restore-login '"success":true' "$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"uatpass123"}')"
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

DUR=$(( $(date +%s) - START ))
echo "==T-PASS=$PASS FAIL=$FAIL DUR=${DUR}s=="
exit 0
