#!/usr/bin/env bash
# ============================================================================
# scripts/uat/multi_instance_e2e.sh — 双实例部署端到端 UAT（2026-09-16 测试盲区补全）
# 对应《核实与修复_测试盲区补全_20260916.md》三.6：此前多实例闭环仅有单测
# （distlock/shadowTTL）与单实例 UAT，「两个 server 进程共享 PG」的部署形态从未实测。
#
# 场景（PG + 双实例 + mock LLM；Redis 未启用 → 影子失效靠 TTL 5s 重播种兜底路径）：
#   M1 双实例健康
#   M2 A 实例开户充值 → B 实例【立刻】可消费（跨实例余额可见性；旧缺陷：B 实例
#      影子 seed 为 0 → 误中止在途翻译）
#   M3 A/B 双实例并发扣费压测（30 路对半）：不透支 / 无误报欠费清零 / ledger 与消耗对账
#   M4 A 实例二次充值 → 5s 内 B 实例恢复可消费（TTL 重播种自愈闭环）
#
# 用法：bash scripts/uat/multi_instance_e2e.sh
#   前置：本机 PG 可达（PG_ADMIN_DSN 可覆盖）、mock LLM 由脚本自起。
# 环境变量：PG_ADMIN_DSN / DB_DSN（缺省 127.0.0.1:5432 当前用户）/ KEEP=1 不清理
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1

A_PORT=8891; B_PORT=8892; MOCK_PORT=8903
A_URL="http://127.0.0.1:${A_PORT}"; B_URL="http://127.0.0.1:${B_PORT}"
JWT_SECRET_VAL="uat-multi-instance-secret"
WORK=$(mktemp -d)
T0=$(date +%s)
PASS=0; FAIL=0
ck(){ if echo "$3" | grep -qE "$2"; then PASS=$((PASS+1)); echo "PASS|$1"; else FAIL=$((FAIL+1)); echo "FAIL|$1|want[$2]|got[${3:0:200}]"; fi; }
log(){ echo "[multi_inst] $*"; }

# ---------- 0. PG 测试库 ----------
PG_ADMIN="${PG_ADMIN_DSN:-postgres://${USER}@127.0.0.1:5432/postgres?sslmode=disable}"
MDB="translator_multi"
DB_DSN="${DB_DSN:-postgres://${USER}@127.0.0.1:5432/${MDB}?sslmode=disable}"
log "重建 PG 测试库 $MDB ..."
psql "$PG_ADMIN" -q -c "DROP DATABASE IF EXISTS $MDB WITH (FORCE)" || true
psql "$PG_ADMIN" -q -c "CREATE DATABASE $MDB" || { echo "PG 建库失败"; exit 1; }
psql "postgres://${USER}@127.0.0.1:5432/${MDB}?sslmode=disable" -q -c "CREATE EXTENSION IF NOT EXISTS vector" \
  || { echo "pgvector 扩展创建失败"; exit 1; }
export DB_DRIVER=postgres DB_DSN

# ---------- 1. 构建 + mock LLM ----------
log "构建后端..."
(cd backend-go && go build -o "$WORK/server" ./cmd/server) || exit 1
nohup python3 scripts/uat/mock_llm.py "$MOCK_PORT" > "$WORK/mockllm.log" 2>&1 < /dev/null &
MOCK_PID=$!
for i in $(seq 1 10); do sleep 1; curl -s -m 2 "http://127.0.0.1:${MOCK_PORT}/v1/chat/completions" -d '{"messages":[{"content":"hi"}]}' >/dev/null 2>&1 && break; done

# ---------- 2. 双实例启动（同库同 JWT_SECRET；探活自指向各自 /status） ----------
log "启动双实例 :${A_PORT} / :${B_PORT}（共享 ${MDB}）..."
COMMON_ENV=(ADMIN_INIT_PASSWORD=Admin@1234 JWT_SECRET="$JWT_SECRET_VAL" DB_DRIVER=postgres DB_DSN="$DB_DSN")
nohup env "${COMMON_ENV[@]}" SELFCHECK_URL="${A_URL}/status" \
  "$WORK/server" -addr "127.0.0.1:${A_PORT}" -kbdb "$WORK/kbA.db" > "$WORK/instA.log" 2>&1 < /dev/null &
A_PID=$!
nohup env "${COMMON_ENV[@]}" SELFCHECK_URL="${B_URL}/status" \
  "$WORK/server" -addr "127.0.0.1:${B_PORT}" -kbdb "$WORK/kbB.db" > "$WORK/instB.log" 2>&1 < /dev/null &
B_PID=$!
for u in "$A_URL" "$B_URL"; do
  OK=0
  for i in $(seq 1 20); do sleep 1; curl -s -m 2 "$u/status" | grep -q '"ok":true' && { OK=1; break; }; done
  [ "$OK" = "1" ] || { echo "实例 $u 启动失败"; tail -5 "$WORK/instA.log" "$WORK/instB.log"; exit 1; }
done
ck M1-both-healthy '"ok":true' "$(curl -s $A_URL/status) | $(curl -s $B_URL/status)"

# ---------- 3. 测试配置（共享库一次写入，双实例即时生效） ----------
source scripts/uat/dblib.sh
dbcfg register_ip_min_interval_sec 0
dbcfg register_ip_daily_limit 1000
dbcfg billing_enforced 1
dbcfg pay_mode mock
# 模型路由指向 mock LLM（models 表在共享库，A 写 B 读）
AJ=$(curl -s $A_URL/api/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"Admin@1234"}' | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))')
AH="Authorization: Bearer $AJ"
curl -s $A_URL/api/admin/models/save -H "$AH" -H 'Content-Type: application/json' \
  -d "{\"api_base\":\"http://127.0.0.1:${MOCK_PORT}/v1\",\"api_key\":\"sk-mock\",\"model\":\"mock-mt\",\"embed_api_base\":\"http://127.0.0.1:${MOCK_PORT}/v1\",\"embed_api_key\":\"sk-mock\"}" >/dev/null

# ---------- M2：A 开户充值 → B 立即可消费 ----------
TS=$(date +%s)
curl -s $A_URL/api/auth/register -H 'Content-Type: application/json' \
  -d "{\"username\":\"multi_u_$TS\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"多实例\",\"email\":\"multi_$TS@test.com\",\"agreed\":true}" >/dev/null
UID_TOKEN=$(curl -s $A_URL/api/auth/login -H 'Content-Type: application/json' -d "{\"username\":\"multi_u_$TS\",\"password\":\"uatpass123\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))')
UH="Authorization: Bearer $UID_TOKEN"
TID=$(dbq "SELECT tenant_id FROM users WHERE username='multi_u_$TS'" | tr -d '[:space:]')
curl -s $A_URL/api/admin/orders/create -H "$AH" -H 'Content-Type: application/json' -d "{\"tenant_id\":$TID,\"tokens\":8000,\"money\":0}" >/dev/null
OID=$(dbq "SELECT id FROM orders WHERE tenant_id=$TID AND status='pending' ORDER BY id DESC LIMIT 1" | tr -d '[:space:]')
curl -s $A_URL/api/admin/orders/pay -H "$AH" -H 'Content-Type: application/json' -d "{\"id\":$OID,\"tenant_id\":$TID}" >/dev/null
sleep 1
# 同一用户在 B 实例登录（JWT_SECRET 一致 → token 互通）+ 开 API Key（共享库）
BT=$(curl -s $B_URL/api/auth/login -H 'Content-Type: application/json' -d "{\"username\":\"multi_u_$TS\",\"password\":\"uatpass123\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))')
[ ${#BT} -gt 30 ] && { PASS=$((PASS+1)); echo "PASS|M2-jwt-cross-instance"; } || { FAIL=$((FAIL+1)); echo "FAIL|M2-jwt-cross-instance"; }
AK=$(curl -s $B_URL/api/apikeys/create -H "Authorization: Bearer $BT" -H 'Content-Type: application/json' -d '{"name":"multi-key"}' | python3 -c 'import sys,json;print(json.load(sys.stdin).get("api_key",""))')
# ★ 核心断言：A 充值后 B 在 TTL 窗口内立刻翻译必须成功（旧缺陷场景：B 影子 seed 0 → 误中止）
M2R=$(curl -s $B_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 60 \
  -d '{"text":"双实例余额可见性验证第一句","target_lang":"en","mode":"pro"}')
ck M2-b-instance-consumes-immediately '"success":true' "$M2R"
sleep 4   # 等 sink 冲刷

# ---------- M3：双实例并发扣费压测（30 路对半，双桶钉死：台账清零 + 永久余额 8000） ----------
# ★ 口径修复（2026-09-16）：DeductWithGrants 先扣台账再扣永久余额——仅钉 balance 会让
#   成功请求消耗落在 30 万试用台账上，ledger 对账公式失真。清空台账后全部消耗走永久余额。
dbq "UPDATE quota_grants SET \"left\"=0 WHERE tenant_id=$TID" >/dev/null
dbq "UPDATE balance_accounts SET balance=8000 WHERE tenant_id=$TID" >/dev/null
sleep 6   # 双实例影子 TTL(5s) 重播种窗口，确保压测从干净影子开始
TOT0=$(python3 -c "
import subprocess
g=subprocess.run(['psql','$DB_DSN','-qAtc','SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$TID AND expires_at>now()'],capture_output=True,text=True).stdout.strip()
b=subprocess.run(['psql','$DB_DSN','-qAtc','SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=$TID'],capture_output=True,text=True).stdout.strip()
print(int(g or 0)+int(b or 0))")
D3=$(mktemp -d)
PIDS3=()   # ★ 脚本修复（2026-09-16）：裸 wait 会连常驻 server/mock 进程一起等 → 永久挂起；
           #   只收集本段 curl 的 PID 定向等待
for i in $(seq 1 15); do
  curl -s $A_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 120 \
    -d "{\"text\":\"双实例并发压测A侧第 $i 句内容\",\"target_lang\":\"en\",\"mode\":\"pro\"}" -o "$D3/a$i.json" &
  PIDS3+=($!)
  curl -s $B_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 120 \
    -d "{\"text\":\"双实例并发压测B侧第 $i 句内容\",\"target_lang\":\"en\",\"mode\":\"pro\"}" -o "$D3/b$i.json" &
  PIDS3+=($!)
done
wait "${PIDS3[@]}"
S3=$(grep -c '"success":true' "$D3"/*.json 2>/dev/null | awk -F: '{s+=$2} END {print s}')
E3=$(grep -l '"success":false' "$D3"/*.json 2>/dev/null | wc -l | tr -d '[:space:]')
[ $((S3 + E3)) -eq 30 ] && [ "$S3" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|M3-all-resolved(ok=$S3 refused=$E3)"; } || { FAIL=$((FAIL+1)); echo "FAIL|M3-all-resolved(ok=$S3 refused=$E3)"; }
# 拒绝样本留痕（诊断用；KEEP=1 时目录保留可细查）
REFSAMPLE=$(grep -l '"success":false' "$D3"/*.json 2>/dev/null | head -1)
[ -n "$REFSAMPLE" ] && echo "INFO|M3-refused-sample|$(head -c 220 "$REFSAMPLE")"
sleep 5
BAL3=$(dbq "SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=$TID" | tr -d '[:space:]')
[ "${BAL3:-0}" -ge 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|M3-no-overdraft($BAL3)"; } || { FAIL=$((FAIL+1)); echo "FAIL|M3-no-overdraft($BAL3)"; }
# 无误报欠费清零（critical 告警仅允许 ≤1 条 settle）
AL3=$(dbq "SELECT COUNT(*) FROM alerts WHERE tenant_id=$TID AND kind='billing_exhausted'" | tr -d '[:space:]')
[ "${AL3:-0}" -le 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|M3-no-false-exhaust(alerts=$AL3)"; } || { FAIL=$((FAIL+1)); echo "FAIL|M3-no-false-exhaust(alerts=$AL3)"; }
# ledger 与消耗对账（双桶合计口径，与 T16 一致）：SUM(实扣 cost) ≈ TOT0 - 剩余双桶
TOT1=$(python3 -c "
import subprocess
g=subprocess.run(['psql','$DB_DSN','-qAtc','SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$TID AND expires_at>now()'],capture_output=True,text=True).stdout.strip()
b=subprocess.run(['psql','$DB_DSN','-qAtc','SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=$TID'],capture_output=True,text=True).stdout.strip()
print(int(g or 0)+int(b or 0))")
L3=$(dbq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='charge'" | tr -d '[:space:]')
EQ3=$(python3 -c "print(1 if abs($L3 - ($TOT0 - $TOT1)) < 1 else 0)" 2>/dev/null || echo 0)
[ "$EQ3" = "1" ] && { PASS=$((PASS+1)); echo "PASS|M3-ledger-reconcile(ledger=$L3 consumed=$((TOT0 - TOT1)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|M3-ledger-reconcile(ledger=$L3 tot0=$TOT0 tot1=$TOT1)"; }
rm -rf "$D3"

# ---------- M4：A 二次充值 → 5s 内 B 恢复可消费（TTL 重播种闭环） ----------
curl -s $A_URL/api/admin/orders/create -H "$AH" -H 'Content-Type: application/json' -d "{\"tenant_id\":$TID,\"tokens\":50000,\"money\":0}" >/dev/null
OID4=$(dbq "SELECT id FROM orders WHERE tenant_id=$TID AND status='pending' ORDER BY id DESC LIMIT 1" | tr -d '[:space:]')
curl -s $A_URL/api/admin/orders/pay -H "$AH" -H 'Content-Type: application/json' -d "{\"id\":$OID4,\"tenant_id\":$TID}" >/dev/null
sleep 5
M4R=$(curl -s $B_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 60 \
  -d '{"text":"充值后跨实例恢复消费验证句","target_lang":"en","mode":"pro"}')
ck M4-topup-propagates-to-b '"success":true' "$M4R"

# ---------- 汇总 ----------
DUR=$(( $(date +%s) - T0 ))
log "=============================="
log "双实例 UAT：PASS=$PASS FAIL=$FAIL DUR=${DUR}s"
log "日志目录：$WORK"
log "=============================="
if [ "${KEEP:-0}" != "1" ]; then
  kill $A_PID $B_PID $MOCK_PID 2>/dev/null
  psql "$PG_ADMIN" -q -c "DROP DATABASE IF EXISTS $MDB WITH (FORCE)" >/dev/null 2>&1
fi
[ "$FAIL" = "0" ] || exit 1
exit 0
