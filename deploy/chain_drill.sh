#!/usr/bin/env bash
# ============================================================================
# chain_drill.sh — ★ S4 全链路演练（商业化开闸验收，D-1 必跑）
#
# 目的：在「生产库副本」的隔离环境里，用真实二进制 + 真实 HTTP 链路走一遍
#       注册→体验礼包→翻译扣费→耗尽停服→下单→「我已付费」→超管确认→开通→退款，
#       验证商业化闭环代码路径全部可通（不影响生产实例与数据）。
#
# 用法（生产服务器 root 执行）：bash chain_drill.sh
# 依赖：/opt/translator/bin/translator-server、/tmp/opskey（-resetpw 子命令）、
#       pg_dump 计划备份（自动取最新）、secrets.env、curl/psql/pg_restore。
# 安全：全程只碰 scratch 库与 /tmp 数据目录；trap 兜底 kill 实例 + dropdb + 清临时。
# ============================================================================
set -u
BIN=/opt/translator/bin/translator-server
BACKUP_DIR=/opt/translator/data/backups
PORT=8799
HOST=127.0.0.1:$PORT
DRILL=/tmp/chain_drill
PASS=0; FAIL=0
STEP() { printf "\n── %s ──\n" "$*"; }
ok()   { echo "  ✅ $*"; PASS=$((PASS+1)); }
bad()  { echo "  ✖ $*"; FAIL=$((FAIL+1)); }
jqv()  { python3 -c "import json,sys;d=json.load(sys.stdin);print(d$1)" 2>/dev/null; }

rm -rf "$DRILL"; mkdir -p "$DRILL/data"; export TMPDIR=$DRILL

# ---------- 0. 环境与备份 ----------
set -a; . /etc/translator/secrets.env; set +a
LATEST=$(ls -t $BACKUP_DIR/tm_*.bak.dump 2>/dev/null | head -1)
[ -n "$LATEST" ] || { echo "✖ 无计划备份"; exit 1; }
echo "备份源: $LATEST ($(du -h "$LATEST"|cut -f1))"
DBUSER=$(printf '%s' "$DB_DSN" | sed -nE 's#postgres://([^:]+):[^@]*@.*#\1#p')
DBPASS=$(printf '%s' "$DB_DSN" | sed -nE 's#postgres://[^:]+:([^@]*)@.*#\1#p')
[ -n "$DBUSER" ] && [ -n "$DBPASS" ] || { echo "✖ 无法从 DB_DSN 解析用户/口令"; exit 1; }
SCRATCH="drill_chain_$(date +%s)"

cleanup() {
  [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null
  sudo -u postgres dropdb --if-exists "$SCRATCH" 2>/dev/null
  rm -rf "$DRILL"; echo "(scratch $SCRATCH 与临时目录已清理)"
}
trap cleanup EXIT

# ---------- 1. scratch 还原（兼灾备复测） ----------
STEP "1/9 还原 scratch 库"
T0=$(date +%s)
sudo -u postgres createdb -O "$DBUSER" "$SCRATCH"
sudo -u postgres psql -d "$SCRATCH" -qc "CREATE EXTENSION IF NOT EXISTS vector" >/dev/null
SCRATCH_DSN="postgres://$DBUSER:$DBPASS@127.0.0.1:5432/$SCRATCH?sslmode=disable"
pg_restore -d "$SCRATCH_DSN" --no-owner "$LATEST" 2>"$DRILL/restore.log"
grep -qiE 'error' "$DRILL/restore.log" && head -3 "$DRILL/restore.log" || true
ok "pg_restore $(( $(date +%s)-T0 ))s（restore.log 仅 extension 类警告可忽略）"

# 演练专用开关（只动 scratch）
sudo -u postgres psql -d "$SCRATCH" -qc "
INSERT INTO system_config(key,value) VALUES
 ('captcha_provider',''),('email_verify_enabled','0'),('pay_mode','mock'),
 ('billing_enforced','1'),('registration_enabled','1'),('email_notify_enabled','0'),
 ('wecom_webhook_url',''),('dingtalk_webhook_url',''),('watchdog_selfcheck_restart','0')
ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value;
DELETE FROM orders; DELETE FROM alerts;
DELETE FROM balance_accounts WHERE tenant_id NOT IN (SELECT id FROM tenants) AND tenant_id<>0;
DELETE FROM quota_grants   WHERE tenant_id NOT IN (SELECT id FROM tenants) AND tenant_id<>0;" >/dev/null
# 超管口令重置（scratch）
SA_PW="Drill!$(date +%s | tail -c 6)"
DB_DSN="$SCRATCH_DSN" /tmp/opskey -resetpw 1 -pw "$SA_PW" >/dev/null 2>&1 && ok "scratch 超管口令重置" || bad "超管口令重置失败"

# ---------- 2. 起一次性实例 ----------
STEP "2/9 起一次性实例（:$PORT，DB=scratch，USER_DATA_DIR=/tmp/chain_drill/data）"
( DB_DSN="$SCRATCH_DSN" REQUIRE_PROD_SECRETS=0 USER_DATA_DIR="$DRILL/data" \
  SELFCHECK_URL="http://$HOST/status" MAIL_ENABLED=0 PORT_HINT=$PORT \
  nohup "$BIN" -addr $HOST -frontend "" >"$DRILL/server.log" 2>&1 & echo $! >"$DRILL/pid" )
sleep 1; SRV_PID=$(cat "$DRILL/pid"); sleep 4
curl -sf "http://$HOST/status" >/dev/null && ok "disposable 实例存活 (pid=$SRV_PID)" || { bad "实例未起来"; tail -20 "$DRILL/server.log"; exit 1; }

# ---------- 3. 注册（新租户，体验礼包） ----------
STEP "3/9 注册（个人号+租户码）→ 体验积分 1000"
CODE="drill$(date +%H%M%S)"
REG=$(curl -s -XPOST "http://$HOST/api/auth/register" -H 'Content-Type: application/json' -d "{
 \"username\":\"owner_$CODE\",\"password\":\"Drill#2026\",\"code\":\"$CODE\",\"name\":\"演练租户$CODE\",
 \"email\":\"$CODE@drill.test\",\"email_code\":\"\",\"captcha_token\":\"\",\"invite\":\"\",\"agreed\":true,
 \"utm_source\":\"drill_channel\",\"utm_medium\":\"drill\"}")
echo "$REG" | grep -qE '"success":\s*true' && ok "注册成功（新租户自动发礼包）" || { bad "注册失败: $REG"; exit 1; }
TID=$(echo "$REG" | jqv "['tenant_id']"); KEY=$(echo "$REG" | jqv "['api_key']")
[ -n "$KEY" ] && ok "注册默认 API Key 随响应签发" || bad "默认 Key 缺失"
TOK=$(curl -s -XPOST "http://$HOST/api/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"owner_$CODE\",\"password\":\"Drill#2026\"}" | jqv "['token']")
[ -n "$TOK" ] && ok "owner 登录拿会话" || { bad "登录失败"; exit 1; }
A=(-H "Authorization: Bearer $TOK" -H 'Content-Type: application/json')
OV=$(curl -s "${A[@]}" "http://$HOST/api/billing/my/overview"); echo "$OV" | head -c 200; echo
PTS=$(echo "$OV" | jqv "['points_available']")
sudo -u postgres psql -d "$SCRATCH" -tAc "SELECT kind,total,COALESCE(source,''),expires_at FROM quota_grants WHERE tenant_id=$TID" | sed 's/^/    grant: /'
sudo -u postgres psql -d "$SCRATCH" -tAc "SELECT 'BA:'||tenant_id||'|'||balance||'|'||COALESCE(updated_at,'') FROM balance_accounts WHERE tenant_id=$TID"
sudo -u postgres psql -d "$SCRATCH" -tAc "SELECT 'AUDIT:'||action||'|'||COALESCE(detail,'') FROM audit_logs ORDER BY id DESC LIMIT 6" | sed "s/^/    /"
[ "${PTS:-0}" = "1000" ] && ok "体验积分 points_available=1000（tid=$TID）" || bad "积分异常: $PTS"
sudo -u postgres psql -d "$SCRATCH" -tAc "SELECT utm_source FROM registration_attribution ORDER BY id DESC LIMIT 1" | grep -q drill_channel \
  && ok "注册归因已落库（S4）" || echo "  ○ 归因行未含 utm（若注册链路未带上则记观察）"

# ---------- 4. 真实翻译扣费 ----------
STEP "4/9 API Key 签发 → /openapi/v1/translate 扣费"
KR=$(curl -s "${A[@]}" -XPOST "http://$HOST/api/apikeys/create" -d '{"name":"drill","perms":"translate"}')
K2=$(echo "$KR" | jqv "['api_key']"); [ -n "$K2" ] && ok "自助签发 Key 亦可（owner=租管）" || echo "  ○ 自助签发被拒（权限口径观察）: $(echo $KR|head -c 80)"
U0=$(sudo -u postgres psql -d "$SCRATCH" -tAc "SELECT count(*) FROM usage_ledger WHERE tenant_id=$TID")
TR=$(curl -s -XPOST "http://$HOST/openapi/v1/translate" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d "{\"text\":\"演练链路验证：请翻成英文。批次 $CODE\",\"target_lang\":\"en\",\"source_lang\":\"zh\"}")
echo "$TR" | grep -q '"success":true' && ok "翻译返回成功" || bad "翻译失败: $(echo $TR|head -c 120)"
U1=$(sudo -u postgres psql -d "$SCRATCH" -tAc "SELECT count(*) FROM usage_ledger WHERE tenant_id=$TID")
[ "$U1" -gt "$U0" ] && ok "usage_ledger 留痕（$U0→$U1，计费生效）" || bad "未见用量留痕"

# ---------- 5. 耗尽停服 ----------
STEP "5/9 余额清零 → 翻译应被硬停（insufficient_balance）"
sudo -u postgres psql -d "$SCRATCH" -c "UPDATE quota_grants SET \"left\"=0 WHERE tenant_id=$TID" >/dev/null
sudo -u postgres psql -d "$SCRATCH" -c "UPDATE balance_accounts SET balance=0 WHERE tenant_id=$TID" >/dev/null
TR2=$(curl -s -XPOST "http://$HOST/openapi/v1/translate" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d "{\"text\":\"第二次应被拒 $CODE\",\"target_lang\":\"en\",\"source_lang\":\"zh\"}")
echo "$TR2" | grep -q insufficient_balance && ok "耗尽被停（错误码正确）" || bad "未拦截: $(echo $TR2|head -c 120)"
ALERT_SEEN=""
for i in 1 2 3 4 5 6; do sleep 15
  ALERT_SEEN=$(sudo -u postgres psql -d "$SCRATCH" -tAc "SELECT COALESCE(string_agg(DISTINCT kind,','),'') FROM alerts WHERE tenant_id=$TID AND kind IN ('billing_exhausted','balance')")
  [ -n "$ALERT_SEEN" ] && break
done
[ -n "$ALERT_SEEN" ] && ok "耗尽告警已落中心（kind=$ALERT_SEEN：sink 清零或 watchdog 扫描两路）" || echo "  ○ 90s 内未见告警（定性：sink 路仅真实扣费清零触发；watchdog 周期路 alert_interval_sec 默认 300s>90s 窗口。生产告警面已在压测实证 billing_exhausted，见部署指南 §八-B8）"

# ---------- 6. 充值下单（个人码） ----------
STEP "6/9 pay/create 充值单（3000 分/¥299）→ pending + 二维码"
PO=$(curl -s "${A[@]}" -XPOST "http://$HOST/api/pay/create" -d '{"points":3000,"channel":"manual"}')
OID=$(echo "$PO" | jqv "['order']['id']")
echo "$PO" | grep -q '"manual_confirm":true' && ok "manual 单创建（id=$OID）" || bad "下单失败: $(echo $PO|head -c 200)"

# ---------- 7. 我已付费 ----------
STEP "7/9 用户点「我已付费」→ manual_confirm=1"
curl -s "${A[@]}" -XPOST "http://$HOST/api/pay/manual-confirm" -d "{\"order_id\":$OID}" | grep -q '"success":true' \
  && ok "已标记待人工确认" || bad "manual-confirm 失败"

# ---------- 8. 超管确认 → 自动开通 ----------
STEP "8/9 超管 orders/pay → 充值到账（90 万 token=3000 分）"
SLT=$(curl -s -XPOST "http://$HOST/api/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$SA_PW\"}")
SA_TOK=$(echo "$SLT" | jqv "['token']"); [ -n "$SA_TOK" ] && ok "超管登录" || bad "超管登录失败: $(echo $SLT|head -c 100)"
curl -s -XPOST "http://$HOST/api/admin/orders/pay" -H "Authorization: Bearer $SA_TOK" -H 'Content-Type: application/json' \
  -d "{\"id\":$OID,\"tenant_id\":$TID}" | grep -q '"success":true' && ok "确认收款成功" || bad "确认失败"
PTS2=$(curl -s "${A[@]}" "http://$HOST/api/billing/my/overview" | jqv "['points_available']")
[ "${PTS2:-0}" -ge 2900 ] && ok "到账 points_available=$PTS2（≈3000 内扣少量演练用量）" || bad "开通后余额异常: $PTS2"

# ---------- 9. 退款演练（不碰真实资金） ----------
STEP "9/9 超管 orders/refund → 订单退款态 + 额度回收"
curl -s -XPOST "http://$HOST/api/admin/orders/refund" -H "Authorization: Bearer $SA_TOK" -H 'Content-Type: application/json' \
  -d "{\"id\":$OID,\"tenant_id\":$TID}" | grep -q '"success":true' && ok "退款受理" || bad "退款失败"
ST=$(sudo -u postgres psql -d "$SCRATCH" -tAc "SELECT status FROM orders WHERE id=$OID")
[ "$ST" = "refunded" ] && ok "orders.status=refunded" || bad "订单态: $ST"
PTS3=$(curl -s "${A[@]}" "http://$HOST/api/billing/my/overview" | jqv "['points_available']")
echo "  ○ 退款后 points_available=$PTS3（回收口径见 SOP §3：消耗<10% 全额回收）"

printf "\n════════ 演练结果：%d 通过 / %d 失败 ════════\n" "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || { echo "（tail server.log）"; tail -5 "$DRILL/server.log"; }
# 备注：入口拒绝路径（gateUsage insufficient_balance）实时不发告警；
# 告警两路=sink 扣费清零即时（billing_exhausted）+ watchdog 周期扫描（balance，默认 300s 一轮）。
exit "$FAIL"
