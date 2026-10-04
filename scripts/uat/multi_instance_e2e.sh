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
#   M3b 近零双桶（余额刚够 2 笔）并发压测：★ D-1 欠费结算与扣减交错下的资金守恒
#       （charge+settle 流水 == 双桶移走量、两桶均不为负、结算路径必须真被触发）
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
# ★ 2026-10-04（〇-AR 第 2 波）：LLM 来源必须在**启动前**用 env 给两台实例，不能只靠下面
#   第 3 段那次 A 侧 models/save。原因（都有实证，见 发布前E2E_UAT_20261003/证据/1004_08AR_第2波_诚实性/）：
#   ① models/save 只刷新**写入进程自己的**内存快照（admin_models.go 的 s.Cfg.ModelRoutes=merged），
#      B 实例早就起来了 ⇒ 永远拿启动时的随机占位 Key ⇒ 恒 401×3；
#   ② 这个「B 从来没真成功过」在旧形态下**看不出来**：修法 F 之前引擎零可用译文仍回
#      success:true（实测旧二进制响应 257 B：success:true / points_used:0 / translations.en=""），
#      于是 M2、M4 两条 `"success":true` 判据绿的是「服务没崩」而不是「客户拿到了译文」；
#   ③ 第 2 波把空壳收敛成 409 task_failed 之后，这两条腿第一次露出真值——**红是暴露，不是引入**。
#   这里只补脚手架自己的前置条件，**判据一字未放宽**：env 档在 hydrateLLMKeys 里「一条都不覆盖」
#   （cmd/server/llmkeys.go 的 llmKeyFromEnv），所以两台进程启动即拿到同一个可用 mock 来源。
#   ⚠️ 产品侧「跨实例配置刷新」仍缺口（现网单实例，无客户面影响），已作为待决缺陷登记，不在此处顺手修。
COMMON_ENV=(ADMIN_INIT_PASSWORD=Admin@1234 JWT_SECRET="$JWT_SECRET_VAL" DB_DRIVER=postgres DB_DSN="$DB_DSN" USER_DATA_DIR="$WORK/udata"
  SILICONFLOW_API_KEY=sk-mock ONLINE_API_BASE="http://127.0.0.1:${MOCK_PORT}/v1" ONLINE_MODEL=mock-mt
  EMBED_API_KEY=sk-mock EMBED_API_BASE="http://127.0.0.1:${MOCK_PORT}/v1")
# ★ 2026-09-26（步骤 6 清理测试数据时踩出）：USER_DATA_DIR 必须钉进 $WORK，与 run_uat.sh 的
#   2026-09-22 修复同口径。此前本脚本没钉，双实例把「上传目录 / 启动备份 / memleak 堆快照」
#   全写进了本机真实应用数据目录（~/Library/Application Support/能言/{backups,memleak} 里
#   实测躺着今天 00:03–00:04 的 .dump/.pb）——闸门跑一次就在客户数据目录里留一层测试垃圾，
#   既污染排查现场，又让「备份目录」这类断言读到的不再是真实状态。
mkdir -p "$WORK/udata"
nohup env "${COMMON_ENV[@]}" SELFCHECK_URL="${A_URL}/status" \
  "$WORK/server" -addr "127.0.0.1:${A_PORT}" -kbdb "$WORK/kbA.db" > "$WORK/instA.log" 2>&1 < /dev/null &
A_PID=$!
nohup env "${COMMON_ENV[@]}" SELFCHECK_URL="${B_URL}/status" \
  "$WORK/server" -addr "127.0.0.1:${B_PORT}" -kbdb "$WORK/kbB.db" > "$WORK/instB.log" 2>&1 < /dev/null &
B_PID=$!
for u in "$A_URL" "$B_URL"; do
  OK=0
  # ★ 2026-09-26 真踩后扩容：就绪等待 20s→45s。
  #   为什么不是 20s：双实例共享同一个 PG 库，迁移走「单飞锁」——后起的实例必须等
  #   前一个把建表跑完才能进入监听；冷启全新库 + 同机有别的负载时，A 实测 00:02:59 起、
  #   00:03:24 才 listen（25s），20s 预算直接把「慢启动」判成「启动失败」并 exit 1，
  #   于是 M1–M8 八条红线**一条都没跑**就整段红（这类红不是回归，却最容易被误读成回归）。
  #   只放宽等待、不放宽任何断言：就绪后 M1 仍各自实测 /status 必须 "ok":true。
  for i in $(seq 1 45); do sleep 1; curl -s -m 2 "$u/status" | grep -q '"ok":true' && { OK=1; break; }; done
  [ "$OK" = "1" ] || { echo "实例 $u 启动失败（等待 ${i}s 未就绪）"; tail -5 "$WORK/instA.log" "$WORK/instB.log"; exit 1; }
done
ck M1-both-healthy '"ok":true' "$(curl -s $A_URL/status) | $(curl -s $B_URL/status)"

# ---------- 3. 测试配置（共享库一次写入，双实例即时生效） ----------
source scripts/uat/dblib.sh
dbcfg register_ip_min_interval_sec 0
dbcfg register_ip_daily_limit 1000
# ★ F-81：设备档／平台日预算档开大，否则「A 开户 → B 立即可消费」这类多实例链路会被防薅挡住
#   （两实例共享同一本 rate_limits 账，跨实例的注册也各占一格）。T69 段内自行压低并钉回。
dbcfg register_device_daily_limit 100000
dbcfg register_global_daily_limit 100000
dbcfg billing_enforced 1
dbcfg pay_mode mock
# 模型路由指向 mock LLM（models 表在共享库，A 写 B 读）
# ⚠️ 这一句测的是「共享库写读」，但它**不会**让 B 实例的运行期配置变新（见上面 COMMON_ENV 那段①）：
#    保存之后 A 用的是库值快照、B 用的仍是启动时的 env 快照。两侧都指向同一个 mock ⇒ 都能出译文，
#    本段的判据射程因此干净地落在「跨实例余额可见性」上，不再被配置陈旧污染。
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
curl -s $A_URL/api/admin/orders/create -H "$AH" -H 'Content-Type: application/json' -d "{\"tenant_id\":$TID,\"points\":27,\"money\":0}" >/dev/null
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
# ★ 收紧（2026-10-04 〇-AR 第 2 波）：除信封还须**译文非空**。
#   旧形态下引擎零可用译文照样回 success:true（实测旧二进制回 257 B：success:true／points_used:0／
#   translations.en=""），只看信封＝把「上游挂了」判成「跨实例余额没传过去」的反面——绿灯掩盖缺陷。
#   这条非空锁就是当年能当场抓住 ⑭ 的那一句，此后长期保留。
ck M2-b-instance-translation-nonempty '"translations":\{"en":"[^"]' "$M2R"
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
# ★ 口径修复（2026-10-04 〇-AR 第 2 波）：流水读数必须与 TOT0 同时刻取基线，
#   对账比的是**本窗口增量**，不是「窗口消耗」对「全生命周期流水合计」。
#   为什么以前没红：旧形态下 M2 那次翻译是 ⑭ 空壳（success:true 但 points_used:0），
#   窗口之前没有任何 charge 流水，累计值恰好等于窗口增量，两个口径数值相同⇒错公式被掩盖。
#   M2 真出译文之后累计值多出 M2 那一笔（实测 302），当场判红。这条红同样是**暴露不是引入**。
#   （M3b 段一直用的是 CHARGE_BASE/CHARGE_AFTER_PROBE 增量口径，本段与它对齐。）
CHG0=$(dbq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='charge'" | tr -d '[:space:]')
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
# ledger 与消耗对账（双桶合计口径，与 T16 一致）：本窗口实扣流水增量 ≈ TOT0 - 剩余双桶
# ⚠️ 期末两把尺子放在同一次连接里读（原来分两次 psql，异步 sink 的尾行若恰好在两次之间落库，
#    就会出现「钱还没扣到位、流水已经到了」的窗口外增量，判成假红）。
TOT1_L3=$(python3 -c "
import subprocess
def q(sql):
    return subprocess.run(['psql','$DB_DSN','-qAtc',sql],capture_output=True,text=True).stdout.strip()
g=int(float(q('SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$TID AND expires_at>now()') or 0))
b=int(float(q('SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=$TID') or 0))
l=int(float(q(\"SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='charge'\") or 0))
print(f'{g+b} {l}')")
TOT1=$(printf '%s' "$TOT1_L3" | awk '{print $1}')
L3=$(printf '%s' "$TOT1_L3" | awk '{print $2}')
D3L=$(python3 -c "print(int(round(abs($L3 - $CHG0))))" 2>/dev/null || echo -1)
EQ3=$(python3 -c "print(1 if abs($D3L - ($TOT0 - $TOT1)) < 1 else 0)" 2>/dev/null || echo 0)
[ "$EQ3" = "1" ] && { PASS=$((PASS+1)); echo "PASS|M3-ledger-reconcile(窗口流水=$D3L 消耗=$((TOT0 - TOT1)) 基线流水=$CHG0)"; } \
  || { FAIL=$((FAIL+1)); echo "FAIL|M3-ledger-reconcile(窗口流水=$D3L 消耗=$((TOT0 - TOT1)) 基线=$CHG0 累计=$L3 tot0=$TOT0 tot1=$TOT1)⇒ 只对增量，累计口径差属基线问题"; }
rm -rf "$D3"

# ---------- M3b：近零双桶下「欠费结算 × 并发扣减」资金守恒（★ D-1，2026-09-29） ----------
# 为什么 M3 不够：M3 把余额钉到 8000、30 路怎么打都余量充足，
# SettleExhausted（欠费有界清零）**在这条路径上一次都不会被触发**。而 D-1 的缺陷恰在
# 「结算与正常扣减在同一租户行上交错」：结算原来把事务内读到的旧快照写回——
# 台账段是绝对赋值 `SET "left"=快照-take`（并发已扣的量被**送回去**，凭空长出可用额度），
# 永久余额段是无守卫相对扣减（可扣成**负数**透支，平台替客户买单）。两处已改回主链范式
# （相对扣减 + `>=take` 守卫 + RowsAffected + 重读重试一次），本段是端到端那一层的锁。
# 判据设计（不是「余额≥0」一句就完事）：
#   ① 标定单句实扣均价 C，把余额钉成「刚够 2×C」——保证既有成功（在扣）又有失败（触发结算），
#      两侧真撞车；否则整段空转。
#   ② 守恒等式 TOT前 - TOT后 == Δ(charge) + Δ(settle)：抓「流水记了、钱没动」与反之
#      （结算按旧快照写回时，期末余量会大于应得值，等式即红）。
#      ⚠️ 只算 charge+settle 两种：欠费批次按 C20 走 LogUsageBatch（charge_kind='log'，留痕不扣费），
#         把它计入就等于把「未扣的钱」当成扣掉的，恒差整批。
#   ③ Δsettle > 0：结算必须**真的跑过**，否则 ② 是在没有结算的前提下绿的（假绿）。
#   ④ 至少 1 笔成功：余额够 2 笔却全被拒＝另一类真缺陷（误报欠费），不能被 ② 掩盖。
# 方言：本脚本整段只在 PG 上跑（SQLite 有 _txlock=immediate 全库写锁，交错不发生，跑了不算覆盖）。
# 金额口径：比较前统一取整再判 |差|<1（PG numeric 回 `5`、SQLite 回 `5.0`，AGENTS §一·7）。
dbq "UPDATE quota_grants SET \"left\"=0 WHERE tenant_id=$TID" >/dev/null
dbq "UPDATE balance_accounts SET balance=8000 WHERE tenant_id=$TID" >/dev/null
sleep 6
CHARGE_BASE=$(dbq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='charge'" | tr -d '[:space:]')
# ① 标定：单发一句（与并发段同文案形态），用 ledger 增量取真实均价
curl -s $A_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 60 \
  -d '{"text":"并发结算压测标定句第 1 句内容","target_lang":"en","mode":"pro"}' >/dev/null
sleep 6
CHARGE_AFTER_PROBE=$(dbq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='charge'" | tr -d '[:space:]')
UNIT_COST=$(python3 -c "print(int(round(abs($CHARGE_AFTER_PROBE - $CHARGE_BASE))))" 2>/dev/null || echo 0)
if [ "${UNIT_COST:-0}" -lt 1 ] 2>/dev/null; then
  FAIL=$((FAIL+1)); echo "FAIL|M3b-cost-calibration(标定增量=$UNIT_COST，无法钉小额余额，本段未跑)"
else
  dbq "UPDATE balance_accounts SET balance=$((UNIT_COST * 2)) WHERE tenant_id=$TID" >/dev/null
  sleep 6   # 双实例影子 TTL 重播种，确保两侧从同一个「刚够两笔」的真值开始
  TOT_B=$(python3 -c "
import subprocess
g=subprocess.run(['psql','$DB_DSN','-qAtc','SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$TID AND \"left\">0 AND expires_at>now()'],capture_output=True,text=True).stdout.strip()
b=subprocess.run(['psql','$DB_DSN','-qAtc','SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=$TID'],capture_output=True,text=True).stdout.strip()
print(int(g or 0)+int(b or 0))")
  CHG_B=$(dbq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='charge'" | tr -d '[:space:]')
  SET_B=$(dbq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='settle'" | tr -d '[:space:]')
  D3B=$(mktemp -d); PIDS3B=()
  for i in $(seq 1 4); do
    curl -s $A_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 120 \
      -d "{\"text\":\"并发结算压测A侧第 $i 句内容\",\"target_lang\":\"en\",\"mode\":\"pro\"}" -o "$D3B/a$i.json" &
    PIDS3B+=($!)
    curl -s $B_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 120 \
      -d "{\"text\":\"并发结算压测B侧第 $i 句内容\",\"target_lang\":\"en\",\"mode\":\"pro\"}" -o "$D3B/b$i.json" &
    PIDS3B+=($!)
  done
  wait "${PIDS3B[@]}"
  # 响应按四类分开数：成功 / 欠费类拒绝 / 并发闸拒绝（rate_limited）/ 空响应。
  # ⚠️ 为什么必须分开：本仓 OpenAPI 另有两道**与余额无关**的闸（每 Key 频次、每实例并发上限），
  #    M3 段实测 30 路里 ok=6、其余 24 路全是这两道闸拦下的 rate_limited。
  #    ④「至少 1 笔成功」要证的是「余额刚够两笔时不该误报欠费」——把并发闸的拒绝也计入失败判据，
  #    这条锁就成结构性恒红；反过来若干脆不判④，真·误报欠费又没人管。
  #    故：并发闸拒绝 → 改成串行补发（一次一笔，与并发闸无关），只在补发后仍零成功才判红，
  #    并把「拒绝来自哪一类」原样打进消息里，不给后人留猜的空间。
  # ⚠️ 计数写法照抄 M3 的既有形态 `{s+=$2}`：曾误写成 `{s=$s+$2}`，
  #    macOS nawk 会把 `$s` 当非法字段直接报错、管道输出空串，于是 S3B 恒为空、④恒红
  #    ——这是「断言自伤」形态，改判据前先单独跑一次计数器自证能吐出数字。
  S3B=0; E3B=0; R3B=0; Z3B=0
  for f in "$D3B"/*.json; do
    [ -f "$f" ] || continue
    body=$(cat "$f" 2>/dev/null || true)
    case "$body" in
      *'"success":true'*) S3B=$((S3B + 1)) ;;
      *'"error_code":"rate_limited"'*) R3B=$((R3B + 1)) ;;
      *'"success":false'*) E3B=$((E3B + 1)) ;;
      *) Z3B=$((Z3B + 1)) ;;
    esac
  done
  sleep 8   # 等 sink 冲刷（结算与留痕流水在这一步才落库）
  RETRY3B=0
  if [ "$S3B" = "0" ] && [ "$R3B" != "0" ]; then
    # 并发闸把 8 路全挡了 ⇒ 串行补发（最多 4 笔，每笔间隔 6s 让并发槽位释放）
    for i in $(seq 1 4); do
      sleep 6
      RB=$(curl -s $A_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" \
        --max-time 120 -d "{\"text\":\"并发结算压测串行补发第 $i 句\",\"target_lang\":\"en\",\"mode\":\"pro\"}")
      RETRY3B=$((RETRY3B + 1))
      case "$RB" in
        *'"success":true'*) S3B=$((S3B + 1)) ;;
        *'"error_code":"rate_limited"'*) R3B=$((R3B + 1)) ;;
        *'"success":false'*) E3B=$((E3B + 1)); printf '%s' "$RB" > "$D3B/retry_refused.json" ;;
      esac
    done
  fi
  RS3B=$(grep -lE '"success":false' "$D3B"/*.json 2>/dev/null | head -1)
  SAMPLE3B=""; [ -n "$RS3B" ] && SAMPLE3B=$(head -c 160 "$RS3B")
  [ "${S3B:-0}" -ge 1 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|M3b-some-succeeded(ok=$S3B 欠费拒=$E3B 并发闸拒=$R3B 空响应=$Z3B 串行补发=$RETRY3B unit=$UNIT_COST)"; } \
    || { FAIL=$((FAIL+1)); echo "FAIL|M3b-some-succeeded(ok=0 欠费拒=$E3B 并发闸拒=$R3B 空响应=$Z3B 串行补发=$RETRY3B unit=$UNIT_COST)⇒ 补发后仍无一笔成功：欠费拒>0 即误报欠费（D-1 侧缺陷）；全为并发闸拒/空响应则属链路或限流配置问题。首条拒绝样本=${SAMPLE3B:-（无拒绝体，说明响应全是空体⇒链路没通）}"; }
  # 不透支：两桶逐列取极值/合计（绝对赋值丢失更新的直接后果是余额为负）
  MINBAL=$(dbq "SELECT COALESCE(MIN(balance),0) FROM balance_accounts WHERE tenant_id=$TID" | tr -d '[:space:]')
  [ "${MINBAL:-0}" -ge 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|M3b-no-negative-balance(min=$MINBAL)"; } \
    || { FAIL=$((FAIL+1)); echo "FAIL|M3b-no-negative-balance(min=$MINBAL，D-1 透支)"; }
  NEGG=$(dbq "SELECT COUNT(*) FROM quota_grants WHERE tenant_id=$TID AND \"left\"<0" | tr -d '[:space:]')
  [ "${NEGG:-0}" = "0" ] && { PASS=$((PASS+1)); echo "PASS|M3b-no-negative-grant"; } \
    || { FAIL=$((FAIL+1)); echo "FAIL|M3b-no-negative-grant(负库存台账 $NEGG 行)"; }
  # 结算必须真跑过（否则守恒判据是在没有结算的前提下绿的）
  CHG_E=$(dbq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='charge'" | tr -d '[:space:]')
  SET_E=$(dbq "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=$TID AND charge_kind='settle'" | tr -d '[:space:]')
  DSET=$(python3 -c "print(int(round(abs($SET_E - $SET_B))))" 2>/dev/null || echo -1)
  [ "${DSET:--1}" -ge 0 ] 2>/dev/null && { PASS=$((PASS+1)); echo "PASS|M3b-settle-reading-ok(settle 流水增量=$DSET)"; } \
    || { FAIL=$((FAIL+1)); echo "FAIL|M3b-settle-reading-ok(settle 增量读数非法：$SET_E - $SET_B)"; }
  if [ "${DSET:-0}" -gt 0 ] 2>/dev/null; then PASS=$((PASS+1)); echo "PASS|M3b-settle-actually-ran($DSET)"; else FAIL=$((FAIL+1)); echo "FAIL|M3b-settle-actually-ran(欠费清零未触发，守恒判据空转)"; fi
  # 守恒等式
  TOT_E=$(python3 -c "
import subprocess
g=subprocess.run(['psql','$DB_DSN','-qAtc','SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$TID AND \"left\">0 AND expires_at>now()'],capture_output=True,text=True).stdout.strip()
b=subprocess.run(['psql','$DB_DSN','-qAtc','SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=$TID'],capture_output=True,text=True).stdout.strip()
print(int(g or 0)+int(b or 0))")
  DCHG=$(python3 -c "print(int(round(abs($CHG_E - $CHG_B))))" 2>/dev/null || echo -1)
  EQ3B=$(python3 -c "print(1 if abs(($TOT_B - $TOT_E) - ($DCHG + $DSET)) < 1 else 0)" 2>/dev/null || echo 0)
  [ "$EQ3B" = "1" ] && { PASS=$((PASS+1)); echo "PASS|M3b-conservation(tot $TOT_B->$TOT_E, charge=$DCHG settle=$DSET)"; } \
    || { FAIL=$((FAIL+1)); echo "FAIL|M3b-conservation(tot $TOT_B->$TOT_E 移走 $((TOT_B - TOT_E))，流水 charge=$DCHG settle=$DSET 对不上)"; }
  RS3B=$(grep -lE '"success":false' "$D3B"/*.json 2>/dev/null | head -1)
  [ -n "$RS3B" ] && echo "INFO|M3b-refused-sample|$(head -c 200 "$RS3B")"
  [ "${KEEP:-0}" = "1" ] || rm -rf "$D3B"
fi

# ---------- M4：A 二次充值 → 5s 内 B 恢复可消费（TTL 重播种闭环） ----------
curl -s $A_URL/api/admin/orders/create -H "$AH" -H 'Content-Type: application/json' -d "{\"tenant_id\":$TID,\"points\":167,\"money\":0}" >/dev/null
OID4=$(dbq "SELECT id FROM orders WHERE tenant_id=$TID AND status='pending' ORDER BY id DESC LIMIT 1" | tr -d '[:space:]')
curl -s $A_URL/api/admin/orders/pay -H "$AH" -H 'Content-Type: application/json' -d "{\"id\":$OID4,\"tenant_id\":$TID}" >/dev/null
sleep 5
M4R=$(curl -s $B_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 60 \
  -d '{"text":"充值后跨实例恢复消费验证句","target_lang":"en","mode":"pro"}')
ck M4-topup-propagates-to-b '"success":true' "$M4R"
# 同 M2 的非空锁（充值传播这条腿也一样不许拿信封当成功）
ck M4-topup-translation-nonempty '"translations":\{"en":"[^"]' "$M4R"

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
