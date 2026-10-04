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
#   M5 ★ 第三台实例 C 启动时库里没配 Key → A 侧保存后 C **不重启**自行热加载
#      （〇-AR 第 5 波「每台热加载」的跨进程端到端锁：健康面／译文／日志／第二次翻转四条腿）
#   M5-D ★ 第四台实例 D 起来时库里**已经有**可用配置（＝现网演示单元那一台的形态）
#      → 启动水合到的值不许被当成"env 给的"，否则这台被永久钉死在启动那一刻、
#        运营之后再怎么改都不跟（〇-AR 第 5 波补腿：前置两半［健康＋快照仍是库里旧值］／
#        第三次改动跟得上／日志档位不是 env）
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
# ★ snap_model <实例 URL> <鉴权头> ＝ 取"这台**此刻生效**的模型名"，只问快照那一半（响应里的 model.model）。
#   为什么不能用整段响应体做子串匹配（本批实测踩出来的假绿，D 实例第一次跑就是这样绿的）：
#   同一个接口里的 `routes` 数组是 loadRoutesDecrypted() 从库里**原文**读的编辑面数据
#   （管理台要显示"库里存着什么"，连被快照停用的坏路由都要显示，否则运营下一次整表保存会把它删掉），
#   所以**运营一保存，routes 数组里立刻就有新模型名，而这台的快照还停在旧值上**——
#   拿整段匹配等于把"库里存了"误判成"这台在用"，热加载一条没跑也能过。
#   取值失败（非 JSON／字段缺）一律回空串，让上层等值比较自然判红，不兜底成"看起来像的值"。
snap_model(){ curl -s -m 3 -H "$2" "$1/api/admin/models" 2>/dev/null | python3 -c '
import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("")
    sys.exit(0)
m = d.get("model") if isinstance(d, dict) else None
print(m.get("model", "") if isinstance(m, dict) else "")'; }

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
#   ★ 2026-10-05（〇-AR 第 5 波）：当年登记在这条注释末尾的「产品侧跨实例配置刷新仍缺口」**已经修掉了**，
#     修法＝「每台热加载」（internal/llmsource：每个读点前按 TTL＋指纹惰性重探共享库），
#     它的跨进程端到端锁就是本文件下面的 **M5 段**（第三台实例 C 启动时刻意不给 LLM 三项 env）。
#     上面①那句描述的是**改造前**的形态：models/save 只碰写入进程自己的内存快照；现在保存链路
#     先落库、落成了才发布快照，其余实例在 TTL 内自己跟上。
#     A/B 两侧继续用 env 档，是为了让 M2–M4 的射程干净落在「跨实例余额可见性」上，
#     **不再是**因为「刷新做不到所以只能靠 env 兜」。
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
# ★ 口径校正（2026-10-05 〇-AR 第 5 波）：旧注释在这里写的是「这一句**不会**让 B 的运行期配置变新」，
#    那是改造前的事实，现在已经不成立——保存链路先落库、再发布快照，B 会在 TTL（默认 5s）内
#    惰性重探并换上库里这份（M5 段就是把这条锁端到端钉住的）。
#    本段之所以仍看不出差别，是因为 A/B 都带着 env 档，而 **env 优先级压过库**（AGENTS §一·3），
#    换上后拿到的还是同一个 mock ⇒ 两侧都能出译文。
#    于是 M2–M4 的射程依旧干净地落在「跨实例余额可见性」上；配置刷新这一层单独由 M5 覆盖，
#    两段互不顶替——别把这里当成"跨实例配置没生效"的证据（那是旧文，会误导排障）。
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

# ---------- M5：第三台实例「每台热加载」（★ 2026-10-05 〇-AR 第 5 波，㊻ 的端到端锁） ----------
# 现场复现（现网 R-1/㊻ 的同一形态，双实例只是它的放大器）：
#   C 实例**启动时**共享库里刻意不给全局 Key（env 档也不给它），于是它拿到随机占位 Key；
#   随后运营在 **A 实例**的管理台保存可用 Key，C 必须在**不重启**的前提下、TTL 内
#   （默认 5 秒）自己换上库里那一份 —— 这就是「每台热加载」。
# 为什么单测不够：internal/llmsource 与 internal/api 的锁测的是"一个进程里的解析与读点"，
#   而本批缺陷的完整链路跨**两个进程＋一座共享库**（A 写、C 读），只有这一层能证明它真通。
# 四条判据各守一类失败，缺一类都可能留下静默：
#   ① 前置负向对照：换 Key **之前** C 必须确实不行（健康面报 placeholder 且出不了非空译文）。
#      没这一句，后面几条可能是恒真的空转锁——C 若从启动就拿到了 Key，测的就不是热加载。
#   ② 内容腿：C 的译文**非空**。只看 "success":true 会被"空壳报成功"糊过去（第 2 波⑭ 的教训）。
#   ③ 观测腿：C 自己的日志里「上游模型配置已热加载」必须**新增至少一条**
#      ——界面上翻绿而日志里没有这一条＝某个读点没接上这把尺子（各自读开机值的老形态）。
#   ④ 第二次翻转：运营**再**改一次模型名，C 的管理台读面必须跟着变。
#      只验第一次＝一份 sync.Once 形态的假实现也能过（"每台热加载"里"持续"那一半没人管）。
dbq "UPDATE balance_accounts SET balance=balance+500 WHERE tenant_id=$TID" >/dev/null
sleep 6   # C 没见过的租户：等它按影子 TTL 从库里播种，别把"没额度"当成"没 Key"
# 清掉共享库里的 LLM 三项＋路由表（只影响本段之后起的 C：A/B 走 env 档，一条都不覆盖）
dbq "DELETE FROM system_config WHERE \"key\" IN ('online_api_key','online_api_base','online_model','model_routes')" >/dev/null
C_PORT=8893; C_URL="http://127.0.0.1:${C_PORT}"
# ★ 与 COMMON_ENV 唯一的差别就是**不给** SILICONFLOW_API_KEY / ONLINE_API_BASE / ONLINE_MODEL：
#   这正是"另一台实例在运营配置之前就已经起来了"的形态。其余档位（JWT／库／数据目录）保持一致。
nohup env ADMIN_INIT_PASSWORD=Admin@1234 JWT_SECRET="$JWT_SECRET_VAL" DB_DRIVER=postgres DB_DSN="$DB_DSN" \
  USER_DATA_DIR="$WORK/udata" EMBED_API_KEY=sk-mock EMBED_API_BASE="http://127.0.0.1:${MOCK_PORT}/v1" \
  SELFCHECK_URL="${C_URL}/status" \
  "$WORK/server" -addr "127.0.0.1:${C_PORT}" -kbdb "$WORK/kbC.db" > "$WORK/instC.log" 2>&1 < /dev/null &
C_PID=$!
C_OK=0
for i in $(seq 1 45); do sleep 1; curl -s -m 2 "$C_URL/status" | grep -q '"ok":true' && { C_OK=1; break; }; done
if [ "$C_OK" != "1" ]; then
  FAIL=$((FAIL+1)); echo "FAIL|M5-instance-c-started(等待 ${i}s 未就绪 ⇒ 本段判据一条都没跑)"
  tail -5 "$WORK/instC.log" 2>/dev/null
else
  PASS=$((PASS+1)); echo "PASS|M5-instance-c-started"
  # ① 前置负向对照
  ck M5-c-health-is-placeholder '"llm_global_key":"placeholder"' "$(curl -s $C_URL/api/health)"
  M5PRE=$(curl -s $C_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 60 \
    -d '{"text":"热加载前置对照句：此刻库里还没有 Key","target_lang":"en","mode":"pro"}')
  if echo "$M5PRE" | grep -qE '"translations":\{"en":"[^"]'; then
    FAIL=$((FAIL+1)); echo "FAIL|M5-c-cannot-translate-before-config(换 Key 前就出了译文 ⇒ 前置没搭对，本段其余判据不成立)"
  else
    PASS=$((PASS+1)); echo "PASS|M5-c-cannot-translate-before-config"
  fi
  HOT0=$(grep -cE '上游模型配置已热加载' "$WORK/instC.log" 2>/dev/null || true)
  # 运营在 A 实例上保存可用 Key（写共享库；C 自己不接这次请求）
  curl -s $A_URL/api/admin/models/save -H "$AH" -H 'Content-Type: application/json' \
    -d "{\"api_base\":\"http://127.0.0.1:${MOCK_PORT}/v1\",\"api_key\":\"sk-mock\",\"model\":\"mock-mt\",\"embed_api_base\":\"http://127.0.0.1:${MOCK_PORT}/v1\",\"embed_api_key\":\"sk-mock\"}" >/dev/null
  # 等 C 自己翻（TTL 默认 5s，这里给 24s 预算；到点仍不翻即判红，不靠 sleep 猜）
  M5WAIT=0
  until curl -s -m 3 "$C_URL/api/health" | grep -q '"llm_global_key":"ok"'; do
    sleep 2; M5WAIT=$((M5WAIT + 2))
    [ "$M5WAIT" -ge 24 ] && break
  done
  ck M5-c-health-flips-after-a-saves '"llm_global_key":"ok"' "$(curl -s $C_URL/api/health)"
  # ② 内容腿：C 不重启即可出真译文
  M5R=$(curl -s $C_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 60 \
    -d '{"text":"第二台实例热加载后翻译验证句","target_lang":"en","mode":"pro"}')
  ck M5-c-translates-after-hot-reload '"success":true' "$M5R"
  ck M5-c-translation-nonempty '"translations":\{"en":"[^"]' "$M5R"
  # ③ 观测腿：C 自己记过热加载这一跳
  HOT1=$(grep -cE '上游模型配置已热加载' "$WORK/instC.log" 2>/dev/null || true)
  [ $(( ${HOT1:-0} - ${HOT0:-0} )) -ge 1 ] 2>/dev/null \
    && { PASS=$((PASS+1)); echo "PASS|M5-c-logged-hot-reload(新增 $(( ${HOT1:-0} - ${HOT0:-0} )) 条，等待 ${M5WAIT}s)"; } \
    || { FAIL=$((FAIL+1)); echo "FAIL|M5-c-logged-hot-reload(HOT0=${HOT0:-?} HOT1=${HOT1:-?} 等待 ${M5WAIT}s)⇒ 界面翻绿而日志无这一跳＝该读点没走同一把尺子"; }
  # ④ 第二次翻转：运营再改模型名，C **此刻生效的快照**必须跟着变（只验一次＝sync.Once 也能过）
  #    ★ 判据按快照字段等值，不按响应体子串（见文件头 snap_model 那段：库里存了 ≠ 这台在用）。
  #    ★ 前置负向对照（补腿第二轮加的，缺它这条就是半空转）：再改**之前** C 的快照必须还停在第一次那份
  #      `mock-mt`。本段在 A 上连保过 mock-mt／mock-mt-second，而 routes 数组读的是库里原文，
  #      所以"响应体里出现 mock-mt-second"在保存那一瞬间就成立——先钉住"这台此刻还是第一份"，
  #      后面那句"变成第二份"才只可能来自**这台自己的重探**。
  ck M5-c-snapshot-is-first-value-before-2nd-change '^mock-mt$' "$(snap_model "$C_URL" "$AH")"
  curl -s $A_URL/api/admin/models/save -H "$AH" -H 'Content-Type: application/json' \
    -d "{\"api_base\":\"http://127.0.0.1:${MOCK_PORT}/v1\",\"api_key\":\"sk-mock\",\"model\":\"mock-mt-second\",\"embed_api_base\":\"http://127.0.0.1:${MOCK_PORT}/v1\",\"embed_api_key\":\"sk-mock\"}" >/dev/null
  M5WAIT2=0
  until [ "$(snap_model "$C_URL" "$AH")" = "mock-mt-second" ]; do
    sleep 2; M5WAIT2=$((M5WAIT2 + 2))
    [ "$M5WAIT2" -ge 24 ] && break
  done
  ck M5-c-reads-second-change '^mock-mt-second$' "$(snap_model "$C_URL" "$AH")"

  # ---------- D 实例：现网演示单元那一台（★ 第 5 波补腿：启动即水合到的值**不许冒充 env**） ----------
  # 上面 C 的形态是「起来时库里没 Key」；现网真实形态恰好相反：**库里已经有可用配置，实例先起、
  # 后有人改**。旧写法在水合之后把 cfg 的派生状态（值非空＋占位标记翻假）留在原地，下一轮解析
  # 按「env 已给可用 Key ⇒ 库值一条都不覆盖」短路 ⇒ 这台被钉死在启动那一刻水合到的值上，
  # 以后运营再怎么改它都不跟，而热加载日志还写着 from=env（现网演示单元的实证读数）。
  # D 实例就是照着这个形态起的：**不给 LLM env**，但库里此刻已经有 A 刚保存的那一份。
  # 四条判据（编号用 D1／D1b／D2／D3，与下面「反证 ⑤-A…⑤-F」那批区分开）：
  #   D1 前置（一半）：D 起来就该是可用的（水合腿真的跑到了），否则后面测的是"没配"而不是"不跟"；
  #   D1b 前置（另一半）：这台此刻生效的**快照**必须还是库里那份旧值（mock-mt-second）。
  #        没有它，D2 有可能测的是"D 起得晚、库里本来就已是第三次改动"——那种形态"跟得上"恒真＝空转锁；
  #   D2 内容腿：A 再保存第三次改动（模型名 mock-mt-third），D **不重启**必须读到；
  #   D3 档位腿：D **新长出来的**那条热加载日志**不许**写 from=env（它的 env 压根没配 Key）——
  #        这一条锁的是"排障抓手本身诚实"，档位假了，下一位排查的人会顺着 env 那条线找一整晚。
  D_PORT=8894; D_URL="http://127.0.0.1:${D_PORT}"
  nohup env ADMIN_INIT_PASSWORD=Admin@1234 JWT_SECRET="$JWT_SECRET_VAL" DB_DRIVER=postgres DB_DSN="$DB_DSN" \
    USER_DATA_DIR="$WORK/udata" EMBED_API_KEY=sk-mock EMBED_API_BASE="http://127.0.0.1:${MOCK_PORT}/v1" \
    SELFCHECK_URL="${D_URL}/status" \
    "$WORK/server" -addr "127.0.0.1:${D_PORT}" -kbdb "$WORK/kbD.db" > "$WORK/instD.log" 2>&1 < /dev/null &
  D_PID=$!
  D_OK=0
  for i in $(seq 1 45); do sleep 1; curl -s -m 2 "$D_URL/status" | grep -q '"ok":true' && { D_OK=1; break; }; done
  if [ "$D_OK" != "1" ]; then
    FAIL=$((FAIL+1)); echo "FAIL|M5-instance-d-started(等待 ${i}s 未就绪 ⇒ D1/D1b/D2/D3 四条判据一条都没跑)"
    tail -5 "$WORK/instD.log" 2>/dev/null
  else
    # 正读数也要记一条（与上面 C 段 `M5-instance-c-started` 同口径）：只记失败不记成功的话，
    # 汇总里的 PASS 数就不含"D 起得来"这一件事，读账的人会以为四条判据都在计数内。
    PASS=$((PASS+1)); echo "PASS|M5-instance-d-started(库里已有配置的那台，等待 ${i}s 就绪)"
    # D1 前置：库里有可用配置的那台，启动就该是 ok（水合腿真跑到了；这一句同时排掉"D 其实没 Key"的空转）
    ck M5-d-healthy-at-boot '"llm_global_key":"ok"' "$(curl -s -m 5 $D_URL/api/health)"
    # D1b 前置的另一半：这台**此刻生效的快照**必须是库里那一份旧值（mock-mt-second）。
    #   没有这一句，D2 可能测的是"本来就已经是新值"（D 起得晚、库里已是第三次改动），
    #   那种形态下"跟得上"恒真＝空转锁。先钉"旧值在这台上"，后面的"变成新值"才有意义。
    ck M5-d-boot-snapshot-is-old-value '^mock-mt-second$' "$(snap_model "$D_URL" "$AH")"
    DHO0=$(grep -cE '上游模型配置已热加载' "$WORK/instD.log" 2>/dev/null || true)
    curl -s $A_URL/api/admin/models/save -H "$AH" -H 'Content-Type: application/json' \
      -d "{\"api_base\":\"http://127.0.0.1:${MOCK_PORT}/v1\",\"api_key\":\"sk-mock\",\"model\":\"mock-mt-third\",\"embed_api_base\":\"http://127.0.0.1:${MOCK_PORT}/v1\",\"embed_api_key\":\"sk-mock\"}" >/dev/null
    DWAIT=0
    # ★ 等的是**快照字段**变成新值，不是响应体里出现新名字（后者由库内 routes 原文提供，一保存就有，
    #   等于"库里存了"就当"这台用了"——D 段第一次跑就是这样假绿的，见文件头 snap_model 注释）。
    until [ "$(snap_model "$D_URL" "$AH")" = "mock-mt-third" ]; do
      sleep 2; DWAIT=$((DWAIT + 2))
      [ "$DWAIT" -ge 24 ] && break
    done
    ck M5-d-follows-change-after-hydration '^mock-mt-third$' "$(snap_model "$D_URL" "$AH")"
    # D3 档位腿：**这一轮新长出来的**那条热加载日志里，来源必须**不是** env
    #   （判据按增量数，不按"日志里有没有这一行"：D 从启动到就绪这一段本来就可能自己记过一条，
    #     拿存量当增量＝把"根本没追上新配置"读成"追上了且档位合规"，正是本批要锁的那类假绿。）
    DHO1=$(grep -cE '上游模型配置已热加载' "$WORK/instD.log" 2>/dev/null || true)
    DLINE=$(grep -E '上游模型配置已热加载' "$WORK/instD.log" 2>/dev/null | tail -1)
    if [ $(( ${DHO1:-0} - ${DHO0:-0} )) -lt 1 ] 2>/dev/null; then
      FAIL=$((FAIL+1)); echo "FAIL|M5-d-hotload-not-env-origin(D 侧没有新增热加载日志：HO0=${DHO0:-?} HO1=${DHO1:-?} 等待 ${DWAIT}s)⇒ D2 若同时绿就是别的路径把值带过去的，档位无从可查"
    elif printf '%s' "$DLINE" | grep -qE '"from":"env"'; then
      FAIL=$((FAIL+1)); echo "FAIL|M5-d-hotload-not-env-origin(水合值冒充 env：${DLINE:0:200})⇒ D 的 env 没配过 SILICONFLOW_API_KEY，这一档是派生状态骗出来的短路"
    else
      PASS=$((PASS+1)); echo "PASS|M5-d-hotload-not-env-origin(新增 $(( ${DHO1:-0} - ${DHO0:-0} )) 条，最后一行档位读数合规：${DLINE:0:160})"
    fi
  fi
  # ---------- E 段：档位必须与「库里此刻真有什么」同源（★ 2026-10-05 第 6 波，现网演示单元抓到的第二条谎言） ----------
  # 现网读数（同刻取的两条）：演示单元的热加载行写着 `"from":"db"`，而 langcross_demo 的 system_config
  # 里**只有 model_routes 一行**（len=268），一行 online_api_* 都没有 ⇒ 那个 "db" 不是库里来的，
  # 是这台上一次把值写回 cfg、下一轮解析把**自己的产物**当成了一种来源（第 5 波只修了冒充 env 那一侧）。
  # 危害不止日志撒谎：运营把那把 Key 撤掉之后，这台仍拿着"库里已不存在"的凭据继续打上游，
  # 而健康面与日志都说"配置来自库"——正是「每台热加载」要消灭的那类"配置在库里改、读的人不在库里读"。
  # 本段就照那一台摆：只撤库里 online_api_key 那一行的值（路由表留着），D **不重启**，问三件事：
  #   E1 撤**之前**：档位必须等于"按库里内容现算出来的那一档"（此刻应是 db）——A/B 对照的 A 面，
  #      没有这一句，E2 可能测的是"本来就对"（空转锁）；
  #   E2 撤**之后**：新长出来的那条热加载行必须等于**当场重算**的那一档（应是 route）；
  #   E3 内容腿：撤掉的是"这台没在用的那一把"，路由腿必须接住 ⇒ 仍出**非空**译文
  #      （AGENTS §三 那条：只判 "success":true 会被"空壳报成功"糊过去）。
  # ★ 两档期望都是**从库里现读现推**（db／route／none 三档），不写死字符串：
  #   写死 "route" 的话，将来库内形态一变这条就成结构性假红或假绿（§一·3「派生态不是来源」同族）。
  llm_expected_from(){
    dbq "SELECT CASE
           WHEN COALESCE((SELECT value FROM system_config WHERE \"key\"='online_api_key'),'') <> '' THEN 'db'
           WHEN COALESCE((SELECT value FROM system_config WHERE \"key\"='model_routes'),'') NOT IN ('','[]') THEN 'route'
           ELSE 'none' END" | tr -d '[:space:]'
  }
  # 档位取值也只问**那一行自己的字段**（整行子串匹配＝routes 数组里出现的任何字样都能冒充命中）
  snap_from(){ printf '%s' "$1" | sed -n 's/.*"from":"\([^"]*\)".*/\1/p'; }
  EXP0=$(llm_expected_from)
  ELINE0=$(grep -E '上游模型配置已热加载' "$WORK/instD.log" 2>/dev/null | tail -1)
  EFROM0=$(snap_from "$ELINE0")
  if [ -n "$EFROM0" ] && [ "$EFROM0" = "$EXP0" ]; then
    PASS=$((PASS+1)); echo "PASS|M5-e1-origin-equals-store-before-withdraw(from=$EFROM0＝库里读数 $EXP0)"
  else
    FAIL=$((FAIL+1)); echo "FAIL|M5-e1-origin-equals-store-before-withdraw(from=${EFROM0:-无行}，库里读数=${EXP0:-读库失败})⇒ 前置没搭对（此刻库里正有 online_api_key，D 的档位应与之一致），E2 不成立"
  fi
  dbq "UPDATE system_config SET value='' WHERE \"key\"='online_api_key'" >/dev/null
  EHO0=$(grep -cE '上游模型配置已热加载' "$WORK/instD.log" 2>/dev/null || true)
  EWAIT=0
  # ★ 循环体里必须**有一个读者**（snap_model 打的是 /api/admin/models，走 llmsource.Current）：
  #   重探是惰性的——没有读点，这台就不会自己醒来探库，日志里永远等新行出来不了。
  #   第一次跑这条腿就是只 grep 日志不读接口，等满 24 s HO0=1 HO1=1，把"没人读"误判成"没重探"。
  #   判据用"新增行"而不是"档位变了"：变了才会打行，行里的 from 才是这台此刻的档位。
  while : ; do
    snap_model "$D_URL" "$AH" >/dev/null 2>&1
    EN=$(grep -cE '上游模型配置已热加载' "$WORK/instD.log" 2>/dev/null || true)
    [ "${EN:-0}" != "${EHO0:-0}" ] && break
    sleep 2; EWAIT=$((EWAIT + 2))
    [ "$EWAIT" -ge 24 ] && break
  done
  ELINE1=$(grep -E '上游模型配置已热加载' "$WORK/instD.log" 2>/dev/null | tail -1)
  EFROM1=$(snap_from "$ELINE1")
  EXP1=$(llm_expected_from)
  if [ "${EN:-0}" = "${EHO0:-0}" ]; then
    FAIL=$((FAIL+1)); echo "FAIL|M5-e2-origin-follows-store-after-withdraw(等待 ${EWAIT}s，D 侧热加载行没新增：HO0=${EHO0:-?} HO1=${EN:-?})⇒ 库里改了而这台没重探（循环里已真打过读点，不是「没人来读」）"
  elif [ -z "$EFROM1" ]; then
    FAIL=$((FAIL+1)); echo "FAIL|M5-e2-origin-follows-store-after-withdraw(有新行但取不到档位读数)⇒ 日志字段名或格式变了：${ELINE1:0:200}"
  elif [ "$EFROM1" = "$EFROM0" ]; then
    FAIL=$((FAIL+1)); echo "FAIL|M5-e2-origin-follows-store-after-withdraw(撤库里那行 Key 后档位纹丝不动＝from=$EFROM1，而此刻库里读数是 $EXP1)⇒ 这一档是上一次写回 cfg 的产物冒充来源，且被撤的凭据洗不掉（现网那句 from=db 就是这条）；等待 ${EWAIT}s HO0=${EHO0:-?} HO1=$(grep -cE '上游模型配置已热加载' "$WORK/instD.log" 2>/dev/null || true)"
  elif [ "$EFROM1" != "$EXP1" ]; then
    FAIL=$((FAIL+1)); echo "FAIL|M5-e2-origin-follows-store-after-withdraw(from=$EFROM1，库里读数=$EXP1)⇒ 换了档但换错了，档位与库内容不同源"
  else
    PASS=$((PASS+1)); echo "PASS|M5-e2-origin-follows-store-after-withdraw(from=$EFROM1＝库里读数，等待 ${EWAIT}s)"
  fi
  ER=$(curl -s $D_URL/openapi/v1/translate -H 'Content-Type: application/json' -H "Authorization: Bearer $AK" --max-time 60 \
    -d '{"text":"撤掉库里那把 Key 之后这台仍要能翻","target_lang":"en","mode":"pro"}')
  ck M5-e3-translates-after-key-withdraw '"translations":\{"en":"[^"]' "$ER"
  # 反证口径（★ 本段每一条都是**逐条装回真跑过的**，红哪几条按实跑读数写，不按推断写；
  #   上一波这里记的"②③④ 红／②④ 红"是**没实跑过的推断**，本轮两条都被实测推翻并改正）：
  #   ⑤-A 把 llmsource.Refresh 的换指针一步（Publish(next)）摘掉 ⇒ 整段 PASS=24 FAIL=6，
  #        红的是 c-health-flips／c-translates／c-translation-nonempty／c-snapshot-is-first-value(前置)／
  #        c-reads-second-change／d-follows-change 六条；
  #        ★ **两条日志腿照旧绿**（c-logged-hot-reload 新增 4 条、d-hotload-not-env-origin 出 from=db）——
  #        那行日志在 Publish 之后无条件打，所以它锁的是"档位读数诚实"，**不是**"指针真换了"；
  #        换指针这一层只能靠内容腿（健康面／译文／快照字段）抓，别把日志腿当证据。
  #   ⑤-B 把健康面读点（health_probes.go 的 llmGlobalKeyState）改回直接读 s.Cfg ⇒ PASS=29 FAIL=1，
  #        **只有 c-health-flips 这一条红**：六个读点各有自己的腿，管理台面／引擎腿都不在它射程里。
  #        （上一波那句"②④ 红"就是把"任一读点"当成了"所有读点"，实测不成立。）
  #   ⑤-D 把 ApplyTo 里的「来源同步回写」那一句（cfg.OnlineAPIKeyOrigin = s.From）摘掉 ⇒
  #        **本段测不出来**（实测整脚本 PASS=30 FAIL=0，D 段五条读数全绿；档位停在 none，
  #        而 nobody 读那个档位去拦库里腿），红的是两条单测的 ①：
  #        cmd/server 的 TestHydratedKeyIsNotRecordedAsEnv ①（"档位=none，期望 route"）与
  #        llmsource 的 TestHydratedValueNeverMasqueradesAsEnv ①（同因，①用 Fatalf 所以 ②不再执行）。
  #        别拿"D 段还绿"当作这一句可以删；
  #   ⑤-E 把 Resolve 里 envUsable 的**档位判据**（origin == OriginAPIKeyEnv）摘掉，只留
  #        「非占位且非空」⇒ 实测 PASS=28 FAIL=2：D2 红（want ^mock-mt-third$ 实得 mock-mt-second，
  #        等满 24 s 也不跟＝被钉死在启动水合那一刻）＋ D3 红（新出的那行热加载日志写着 from=env，
  #        **与现网演示单元那条一字同形**）。
  #        ★ 注意 C 段九条在这条反证下**依旧全绿**（实测）：C 起来时库里没 Key，占位标记是真的，
  #        短路条件够不着它——"库里本来就有配置的那台会不会被钉死"只有 D 这一段管。
  #        单测侧同因红两条 ②（got=sk-from-route from=env），读数见证据目录 RP1 两份日志。
  #   ⑤-F 把 snap_model 换回"整段响应体做子串匹配"（＝本段第一次跑时的写法）⇒
  #        D2 与 C 段④ **双双假绿**（本批实测读数：D 启动后 0.8 秒即跑完这两条，正落在默认 5 秒
  #        TTL 之内、日志里热加载那行一条都还没有，快照仍是旧模型名，可响应体里已经出现新名字）。
  #        为什么会假绿：/api/admin/models 的 model 对象读快照、routes 数组读**库里原文**
  #        （编辑面要显示"存着什么"，含被快照停用的坏路由），一保存 routes 就带新值 ⇒
  #        整段子串＝把"库里存了"读成"这台用了"。同段的 D3（按日志增量数）就是这条假绿的现形器：
  #        它当场报 HO0=0 HO1=0，才把 D2 那条绿灯揪出来。
  #   ⑤-C 跑法：bash scripts/uat/multi_instance_e2e.sh（PG 方言，整段跑，本段单独跑不起来）。
fi

# ---------- 汇总 ----------
DUR=$(( $(date +%s) - T0 ))
log "=============================="
log "双实例 UAT：PASS=$PASS FAIL=$FAIL DUR=${DUR}s"
log "日志目录：$WORK"
log "=============================="
if [ "${KEEP:-0}" != "1" ]; then
  # ★ M5 起的第三台 C 与第四台 D 必须一并收掉（${C_PID:-}／${D_PID:-}：前置段失败时该变量没赋值，set -u 下裸写会中止清理）
  kill $A_PID $B_PID ${C_PID:-} ${D_PID:-} $MOCK_PID 2>/dev/null
  psql "$PG_ADMIN" -q -c "DROP DATABASE IF EXISTS $MDB WITH (FORCE)" >/dev/null 2>&1
fi
[ "$FAIL" = "0" ] || exit 1
exit 0
