#!/usr/bin/env bash
# ============================================================================
# scripts/uat/api_uat.sh — 后端全链路 UAT 断言套件（API 层，含前端所调全部后端面）
# 覆盖：健康/公开接口 / 认证注册 / 防刷协议 / 模型路由 / 估价 / 翻译扣费 / 余额硬闸
#       充值支付(下单-模拟-幂等-发票) / 邀请裂变 / 知识库 / OpenAPI / 权限租户隔离
#       套餐订阅 / 通知反馈 / 文件翻译(docx)
# 依赖：mock_llm.py 已启动、uat 服务已启动且为全新库（run_uat.sh 负责编排）
# 环境变量：
#   BASE_URL    服务地址（默认 http://127.0.0.1:8899）
#   UAT_DB      SQLite 库路径（默认 /tmp/uat/dev.db）
#   ADMIN_USER  超管用户名（默认 admin）
#   ADMIN_PASS  超管密码（默认 Admin@1234）
# ============================================================================
set -u
B="${BASE_URL:-http://127.0.0.1:8899}"
U="${UAT_DB:-/tmp/uat/dev.db}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Admin@1234}"
J='Content-Type: application/json'
PASS=0; FAIL=0; START=$(date +%s)

ck(){ if echo "$3" | grep -qE "$2"; then PASS=$((PASS+1)); echo "PASS|$1"; else FAIL=$((FAIL+1)); echo "FAIL|$1|want[$2]|got[${3:0:220}]"; fi; }
reg(){ local extra="${6:-}"; curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$1\",\"password\":\"$2\",\"code\":\"$3\",\"name\":\"$4\",\"email\":\"$5\",\"agreed\":true${extra:+,$extra}}"; }
tok(){ curl -s $B/api/auth/login -H "$J" -d "{\"username\":\"$1\",\"password\":\"$2\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))'; }
pv(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(d$1)"; }

echo "=== A 阶段：公开接口 / 认证 / 计费 / 翻译 / 交易 ==="
# ---------- A1 健康与公开接口 ----------
ck A1-status '"ok":true' "$(curl -s $B/status)"
ck A1-plans '"success":true' "$(curl -s $B/api/plans)"
ck A1-pricing-page '<' "$(curl -s $B/pricing | head -c 60)"
ck A1-register-config '"success":true' "$(curl -s $B/api/auth/register-config)"
ck A1-langs 'kb_langs' "$(curl -s $B/api/translation/langs)"

# ---------- A2 管理员登录 ----------
AT=$(tok $ADMIN_USER $ADMIN_PASS)
[ ${#AT} -gt 30 ] && { PASS=$((PASS+1)); echo "PASS|A2-admin-login(${#AT}b)"; } || { FAIL=$((FAIL+1)); echo "FAIL|A2-admin-login|AT=[$AT]"; }
AH="Authorization: Bearer $AT"
ck A2-admin-me 'admin|super' "$(curl -s $B/api/auth/me -H "$AH")"

# ---------- A3 注册 / 重名 / 错误密码 / 协议 ----------
ck A3-register-A '"success":true' "$(reg uatuser_a uatpass123 uatcorpA UAT公司A uat_a@test.com)"
T1=$(tok uatuser_a uatpass123); H1="Authorization: Bearer $T1"
ck A3-register-B '"success":true' "$(reg uatuser_b uatpass123 uatcorpB UAT公司B uat_b@test.com)"
T2=$(tok uatuser_b uatpass123); H2="Authorization: Bearer $T2"
ck A3-dup-tenantcode '租户创建失败|UNIQUE|已存在' "$(reg uatuser_a uatpass123 uatcorpA 重复 uat_dup@test.com)"
AID=$(sqlite3 $U "SELECT id FROM users WHERE username='uatuser_a' LIMIT 1" | tr -d '[:space:]')
DUP_PAYLOAD="{\"username\":\"uatuser_a\",\"password\":\"uatpass123\",\"display_name\":\"重复\",\"role\":\"user\",\"tenant_id\":$AID}"
ck A3-dup-username '已存在|占用|exists' "$(curl -s $B/api/admin/users/create -H "$AH" -H "$J" -d "$DUP_PAYLOAD")"
ck A3-bad-login '密码|失败|incorrect|invalid|UNAUTHORIZED' "$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"wrongpass"}')"
ck A3-no-agree '同意|协议' "$(curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_x","password":"uatpass123","code":"uatcorpD","name":"X","email":"uat_x@test.com"}')"

# ---------- A4 注册赠送余额 ----------
BAL1=$(curl -s "$B/api/billing/balance" -H "$H1")
ck A4-balance-shape 'total_available' "$BAL1"
echo "INFO|balance-userA|$BAL1"

# ---------- A5 模型路由指向 mock LLM ----------
MS=$(curl -s $B/api/admin/models/save -H "$AH" -H "$J" -d "{\"api_base\":\"${MOCK_LLM_URL:-http://127.0.0.1:8901}/v1\",\"api_key\":\"sk-mock-uat\",\"model\":\"mock-mt\",\"embed_api_base\":\"${MOCK_LLM_URL:-http://127.0.0.1:8901}/v1\",\"embed_api_key\":\"sk-mock-embed\"}")
ck A5-models-save '"success":true' "$MS"
ck A5-models-set '"set":true' "$(curl -s $B/api/admin/models -H "$AH")"
MG=$(curl -s $B/api/admin/models -H "$AH")
echo "$MG" | grep -qE '\*\*\*\*' && { PASS=$((PASS+1)); echo "PASS|A5-mask"; } || { FAIL=$((FAIL+1)); echo "FAIL|A5-mask|$MG"; }

# ---------- A6 估价 ----------
ck A6-estimate '"success":true' "$(curl -s $B/api/translation/estimate -H "$H1" -H "$J" -d '{"text":"早上好，欢迎使用翻译助手平台进行文本翻译测试。","target_langs":["en"],"mode":"fast"}')"

# ---------- A7 聊天翻译 + 计量入账（usage_ledger 行增） ----------
Q1=$(sqlite3 $U "SELECT left FROM quota_grants WHERE tenant_id=(SELECT tenant_id FROM users WHERE username='uatuser_a') AND kind='trial'")
CH=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"早上好，欢迎使用翻译助手平台。","options":{"target_langs":["en"]}}')
ck A7-chat-ok 'TranslatedEN' "$CH"
sleep 3
Q2=$(sqlite3 $U "SELECT left FROM quota_grants WHERE tenant_id=(SELECT tenant_id FROM users WHERE username='uatuser_a') AND kind='trial'")
[ -n "$Q1" ] && [ -n "$Q2" ] && [ "$Q2" -lt "$Q1" ] && { PASS=$((PASS+1)); echo "PASS|A7-metering($Q1->$Q2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|A7-metering($Q1->$Q2)"; }
ck A7-usage-me '"success":true' "$(curl -s "$B/api/billing/usage/me" -H "$H1")"

# ---------- A8 余额不足硬闸（清零 userB 双台账，billing_enforced=1） ----------
BID=$(sqlite3 $U "SELECT tenant_id FROM users WHERE username='uatuser_b'")
sqlite3 $U "UPDATE balance_accounts SET balance=0 WHERE tenant_id=$BID; UPDATE quota_grants SET left=0 WHERE tenant_id=$BID;"
CH2=$(curl -s $B/api/chat -H "$H2" -H "$J" --max-time 30 -d '{"message":"这是一条余额不足应当被拦截的翻译请求文本内容","options":{"target_langs":["en"]}}')
ck A8-insufficient-block '耗尽|不足|insufficient' "$CH2"
echo "INFO|A8-reply|${CH2:0:200}"

# ---------- A9 支付链路：下单→模拟支付→幂等→发票 ----------
ORD=$(curl -s $B/api/pay/create -H "$H1" -H "$J" -d '{"tokens":1000000,"channel":"mock"}')
ck A9-pay-create '"success":true' "$ORD"
OID=$(echo "$ORD" | pv '.get("order_id") or d.get("order",{}).get("id") or 0')
echo "INFO|order-id|$OID"
SIM_PAYLOAD="{\"order_id\":$OID}"
ck A9-pay-simulate '"success":true' "$(curl -s $B/api/pay/simulate -H "$H1" -H "$J" -d "$SIM_PAYLOAD")"
ck A9-pay-status-paid 'paid|success' "$(curl -s "$B/api/pay/status?order_id=$OID" -H "$H1")"
G1=$(sqlite3 $U "SELECT balance FROM balance_accounts WHERE tenant_id=(SELECT tenant_id FROM users WHERE username='uatuser_a')")
curl -s $B/api/pay/simulate -H "$H1" -H "$J" -d "{\"order_id\":$OID}" >/dev/null
G2=$(sqlite3 $U "SELECT balance FROM balance_accounts WHERE tenant_id=(SELECT tenant_id FROM users WHERE username='uatuser_a')")
[ "$G1" = "$G2" ] && { PASS=$((PASS+1)); echo "PASS|A9-pay-idempotent($G1==$G2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|A9-pay-idempotent($G1->$G2)"; }
ck A9-orders-list '"success":true' "$(curl -s "$B/api/billing/orders" -H "$H1")"
INV_PAYLOAD="{\"order_id\":$OID,\"title\":\"UAT测试发票\",\"tax_no\":\"TAX123456\"}"
ck A9-invoice '"success"' "$(curl -s $B/api/billing/invoices/create -H "$H1" -H "$J" -d "$INV_PAYLOAD")"

# ---------- A10 邀请裂变（个人注册 + ref 码） ----------
REF=$(curl -s "$B/api/referral/my" -H "$H1")
ck A10-referral-my '"success":true' "$REF"
CODE=$(echo "$REF" | pv '.get("ref_code","")')
echo "INFO|ref-code|$CODE"
for i in 1 2 3; do
  R3=$(curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"uatuser_c\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"C受邀\",\"email\":\"uat_c2@test.com\",\"agreed\":true,\"ref\":\"$CODE\"}")
  echo "$R3" | grep -q '"success":true' && break
  sleep 2
done
ck A10-reg-invite '"success":true' "$R3"
RE2=$(curl -s "$B/api/referral/my" -H "$H1")
ck A10-referral-ok '"success":true' "$RE2"
echo "INFO|ref-after|$RE2"

# ---------- A11 知识库：企业包 + 条目 + 检索 ----------
KBP=$(curl -s $B/api/admin/kb-packages/create -H "$H1" -H "$J" -d '{"code":"uat_kb_pkg","name":"UAT企业知识包","pack_type":"tenant"}')
ck A11-kb-package-create '"success":true' "$KBP"
PKGID=$(echo "$KBP" | pv '.get("package",{}).get("id") or d.get("data",{}).get("id") or 0')
KB=$(curl -s $B/api/admin/kb-entries/add -H "$H1" -H "$J" -d "{\"package_id\":$PKGID,\"source_text\":\"登录\",\"target_lang\":\"en\",\"target_text\":\"Log In\",\"module\":\"uat\",\"remark\":\"UAT术语\"}")
ck A11-kb-add '"success":true' "$KB"
ck A11-kb-stats '"success":true' "$(curl -s "$B/api/translation/kb-stats" -H "$H1")"

# ---------- A12 OpenAPI：密钥 + 同步翻译 + 错误密钥 ----------
AK=$(curl -s $B/api/apikeys/create -H "$H1" -H "$J" -d '{"name":"uat-key"}')
ck A12-apikey-create '"success":true' "$AK"
KEY=$(echo "$AK" | pv '.get("api_key","")')
OT=$(curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer $KEY" --max-time 90 -d '{"text":"开放接口同步翻译测试","target_lang":"en"}')
ck A12-openapi-translate '"success":true' "$OT"
echo "INFO|openapi-translate|$OT"
ck A12-openapi-balance '"success":true' "$(curl -s "$B/openapi/v1/balance" -H "Authorization: Bearer $KEY")"
ck A12-openapi-badkey 'invalid|无效' "$(curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer sk-bogus-key" -d '{"text":"x","target_lang":"en"}')"
ck A12-openapi-spec '"openapi"' "$(curl -s $B/openapi/v1.json | head -c 400)"

# ---------- A13 权限与租户隔离 ----------
UADM=$(curl -s "$B/api/admin/users" -H "$H1")
echo "$UADM" | python3 -c 'import sys,json;d=json.load(sys.stdin);us=d.get("users",[]);print("OK" if us and all(u.get("tenant_id")==us[0].get("tenant_id") for u in us) else "BAD")' | grep -q '^OK' && { PASS=$((PASS+1)); echo "PASS|A13-users-scoped"; } || { FAIL=$((FAIL+1)); echo "FAIL|A13-users-scoped|$UADM"; }
ck A13-no-token-401 'success.*false|401|未登录' "$(curl -s $B/api/billing/balance)"
ck A13-cross-tenant-order 'success":false|404|不存在|无权' "$(curl -s "$B/api/pay/status?order_id=$OID" -H "$H2")"
AUD=$(curl -s "$B/api/system/audit" -H "$H1")
echo "$AUD" | python3 -c 'import sys,json;d=json.load(sys.stdin);ls=d.get("logs",[]);print("OK" if ls and all(l.get("tenant_id")==ls[0].get("tenant_id") for l in ls) else "BAD")' | grep -q '^OK' && { PASS=$((PASS+1)); echo "PASS|A13-audit-scoped"; } || { FAIL=$((FAIL+1)); echo "FAIL|A13-audit-scoped|$AUD"; }

# ---------- A14 套餐订阅（包绑定用户A租户，mock 即付即到账） ----------
TAID=$(sqlite3 $U "SELECT tenant_id FROM users WHERE username='uatuser_a' LIMIT 1")
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$TAID,\"code\":\"uat_paid_100k\",\"name\":\"UAT包月10万句\",\"ptype\":\"paid\",\"sentences\":100000,\"price_money\":99,\"duration_days\":30}" >/dev/null
SUB=$(curl -s $B/api/package/subscribe -H "$H1" -H "$J" -d '{"code":"uat_paid_100k"}')
ck A14-subscribe '"success"' "$SUB"
echo "INFO|subscribe|$SUB"
ck A14-me-package '"success":true' "$(curl -s "$B/api/me/package" -H "$H1")"

# ---------- A15 通知 / 反馈 ----------
ck A15-notifications '"success":true' "$(curl -s "$B/api/notifications" -H "$H1")"
ck A15-feedback '"success":true' "$(curl -s $B/api/feedback -H "$H1" -H "$J" -d '{"content":"UAT反馈：界面很赞","contact":"uat@a.com"}')"
ck A15-feedback-list-admin '"success":true' "$(curl -s "$B/api/feedback/list" -H "$AH")"

# ---------- A16 文件翻译（docx 对照表交付） ----------
TMPD=$(mktemp -d)
python3 - "$TMPD/hello.docx" <<'EOF'
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
<w:body><w:p><w:r><w:t>今天天气晴朗，适合外出散步。</w:t></w:r></w:p>
<w:p><w:r><w:t>智能翻译助手的文件翻译功能非常强大。</w:t></w:r></w:p>
<w:sectPr/></w:body></w:document>''')
zf.close()
EOF
FT=$(curl -s $B/api/translate -H "$H1" -F "file=@$TMPD/hello.docx" -F "target_langs=en" --max-time 120)
ck A16-file-translate '文件翻译完成|tokens_used' "$FT"
echo "INFO|file-result|${FT:0:260}"
rm -rf "$TMPD"

echo "==A-PASS=$PASS FAIL=$FAIL=="

# ============================================================================
# B 阶段：运营策略引擎（计费因子流程引擎配置）+ P1/P2 回归（2026-09-05）
# 覆盖：ops policy 读取/保存、fast 免费、limit_chars 闸门、平台包订阅(P2)、
#       套餐月度重置、邀请奖励因子、企业成员邀请加入(P1)、推广时间窗覆盖
# ============================================================================
echo "=== B 阶段：运营策略引擎 / P1 / P2 回归 ==="
TAID=$(sqlite3 $U "SELECT tenant_id FROM users WHERE username='uatuser_a' LIMIT 1")

# ---------- B1 运营策略读取/保存（平台级，超管） ----------
ck B1-ops-policy-get '"success":true' "$(curl -s $B/api/admin/ops/policy -H "$AH")"
OPS_PAYLOAD=$(cat <<'JSON'
{"scope":"platform","policy":{"billing":{"mode_rules":{"fast":{"enabled":true,"charge":false,"limit_chars":10},"pro":{"enabled":true,"charge":true,"limit_chars":0}}},"package":{"monthly_reset_enabled":true,"monthly_reset_limit":1},"invite":{"enabled":true,"reward_tokens":900000}}}
JSON
)
ck B1-ops-policy-save '"success":true' "$(curl -s $B/api/admin/ops/policy/save -H "$AH" -H "$J" -d "$OPS_PAYLOAD")"

# ---------- B2 fast 免费（charge=false）：翻译成功、台账 biz_mode=fast、双桶余额不变 ----------
B2B1=$(curl -s "$B/api/billing/balance" -H "$H1" | pv '.get("total_available") or 0')
CHF=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"测试文本。","options":{"target_langs":["en"],"mode":"fast"}}')
ck B2-fast-chat-free 'TranslatedEN' "$CHF"
B2B2=$(curl -s "$B/api/billing/balance" -H "$H1" | pv '.get("total_available") or 0')
[ -n "$B2B1" ] && [ "$B2B1" = "$B2B2" ] && { PASS=$((PASS+1)); echo "PASS|B2-free-no-deduct($B2B1==$B2B2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B2-free-no-deduct($B2B1->$B2B2)"; }
B2LED=$(sqlite3 $U "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$TAID AND biz_mode='fast'")
[ "$B2LED" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|B2-fast-ledger(biz_mode=fast rows=$B2LED)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B2-fast-ledger|rows=$B2LED"; }
CHP=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"专业模式扣费测试。","options":{"target_langs":["en"],"mode":"pro"}}')
ck B2-pro-still-charge 'TranslatedEN' "$CHP"

# ---------- B3 fast.limit_chars 闸门：超长被拒（MODE_LIMIT_CHARS），pro 不受限 ----------
LONG="这是一段远远超过十个字符的快速模式超长测试文本内容"
CHF2=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 30 -d "{\"message\":\"$LONG\",\"options\":{\"target_langs\":[\"en\"],\"mode\":\"fast\"}}")
ck B3-fast-limit-block '输入上限' "$CHF2"
CHP2=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d "{\"message\":\"$LONG\",\"options\":{\"target_langs\":[\"en\"],\"mode\":\"pro\"}}")
ck B3-pro-unlimited 'TranslatedEN' "$CHP2"

# ---------- B4 P2 回归：平台包（tenant_id=0）可被任意租户订阅 ----------
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d '{"tenant_id":0,"code":"uat_plat_pkg","name":"UAT平台套餐","ptype":"paid","sentences":50000,"price_money":50,"duration_days":30}' >/dev/null
SUB2=$(curl -s $B/api/package/subscribe -H "$H2" -H "$J" -d '{"code":"uat_plat_pkg"}')
ck B4-platform-subscribe '"success"' "$SUB2"
echo "INFO|B4-platform-subscribe|$SUB2"

# ---------- B5 套餐月度重置：扣减→重置恢复→二次重置被上限拦截 ----------
sqlite3 $U "UPDATE quota_grants SET \"left\"=\"left\"-50000 WHERE tenant_id=$TAID AND kind='plan' AND \"left\">50000"
RST=$(curl -s $B/api/admin/billing/package/reset -H "$H1" -H "$J" -d '{}')
ck B5-reset-ok '"success":true' "$RST"
echo "INFO|B5-reset|$RST"
RST2=$(curl -s $B/api/admin/billing/package/reset -H "$H1" -H "$J" -d '{}')
ck B5-reset-limit '上限|已达' "$RST2"

# ---------- B6 邀请奖励因子：invite.reward_tokens=900000 按新值入账 ----------
curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_e","password":"uatpass123","type":"personal","name":"E邀请人","email":"uat_e@test.com","agreed":true}' >/dev/null
TE=$(tok uatuser_e uatpass123); HE="Authorization: Bearer $TE"
ECODE=$(curl -s "$B/api/referral/my" -H "$HE" | pv '.get("ref_code","")')
ETID=$(sqlite3 $U "SELECT tenant_id FROM users WHERE username='uatuser_e'")
E1=$(sqlite3 $U "SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$ETID AND kind='trial'")
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"uatuser_d\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"D受邀\",\"email\":\"uat_d@test.com\",\"agreed\":true,\"ref\":\"$ECODE\"}" >/dev/null
E2=$(sqlite3 $U "SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$ETID AND kind='trial'")
[ -n "$E1" ] && [ -n "$E2" ] && [ $((E2 - E1)) -ge 900000 ] && { PASS=$((PASS+1)); echo "PASS|B6-invite-factor(+$((E2-E1)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|B6-invite-factor($E1->$E2)"; }

# ---------- B7 P1 回归：企业成员凭有效邀请码加入既有租户（role=user） ----------
curl -s $B/api/admin/invite-codes/create -H "$H1" -H "$J" -d '{"code":"JOINUAT001"}' >/dev/null
RF=$(curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_f","password":"uatpass123","type":"enterprise","role_choice":"member","invite":"JOINUAT001","name":"F加入","email":"uat_f@test.com","agreed":true}')
ck B7-invite-join '"success":true' "$RF"
FTID=$(sqlite3 $U "SELECT tenant_id FROM users WHERE username='uatuser_f'" | tr -d '[:space:]')
FROLE=$(sqlite3 $U "SELECT role FROM users WHERE username='uatuser_f'" | tr -d '[:space:]')
FUSED=$(sqlite3 $U "SELECT used FROM invite_codes WHERE code='JOINUAT001'" | tr -d '[:space:]')
[ "$FTID" = "$TAID" ] && { PASS=$((PASS+1)); echo "PASS|B7-joined-tenant($FTID==$TAID)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B7-joined-tenant(f=$FTID a=$TAID)"; }
[ "$FROLE" = "user" ] && { PASS=$((PASS+1)); echo "PASS|B7-joined-role(user)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B7-joined-role($FROLE)"; }
[ "$FUSED" = "1" ] && { PASS=$((PASS+1)); echo "PASS|B7-invite-marked-used"; } || { FAIL=$((FAIL+1)); echo "FAIL|B7-invite-marked-used($FUSED)"; }

# ---------- B8 运营时间窗：base fast 收费 + 窗口覆盖 fast 免费 → 窗口生效 ----------
WPAYLOAD=$(cat <<'JSON'
{"scope":"platform","policy":{"billing":{"mode_rules":{"fast":{"enabled":true,"charge":true,"limit_chars":0},"pro":{"enabled":true,"charge":true,"limit_chars":0}}}}}
JSON
)
curl -s $B/api/admin/ops/policy/save -H "$AH" -H "$J" -d "$WPAYLOAD" >/dev/null
TODAY=$(python3 -c 'from datetime import datetime,timedelta;from zoneinfo import ZoneInfo;n=datetime.now(ZoneInfo("Asia/Shanghai"));print((n-timedelta(days=1)).strftime("%Y-%m-%d"),(n+timedelta(days=1)).strftime("%Y-%m-%d"))')
WS=$(echo "$TODAY" | cut -d' ' -f1); WE=$(echo "$TODAY" | cut -d' ' -f2)
WINDOW_PAYLOAD=$(printf '{"window":{"id":"uat_promo","name":"UAT推广期","start":"%s 00:00","end":"%s 23:59","priority":10,"overrides":{"billing":{"mode_rules":{"fast":{"charge":false}}}}}}' "$WS" "$WE")
curl -s $B/api/admin/ops/policy/window/save -H "$AH" -H "$J" -d "$WINDOW_PAYLOAD" >/dev/null
B8B1=$(curl -s "$B/api/billing/balance" -H "$H1" | pv '.get("total_available") or 0')
CHF3=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"窗口覆盖测试。","options":{"target_langs":["en"],"mode":"fast"}}')
ck B8-window-fast-free 'TranslatedEN' "$CHF3"
B8B2=$(curl -s "$B/api/billing/balance" -H "$H1" | pv '.get("total_available") or 0')
[ -n "$B8B1" ] && [ "$B8B1" = "$B8B2" ] && { PASS=$((PASS+1)); echo "PASS|B8-window-override-no-deduct($B8B1==$B8B2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B8-window-override($B8B1->$B8B2)"; }

echo "==B-PASS=$PASS FAIL=$FAIL=="
echo "==TOTAL-PASS=$PASS FAIL=$FAIL=="
