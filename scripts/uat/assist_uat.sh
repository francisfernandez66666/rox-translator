#!/usr/bin/env bash
# ============================================================================
# scripts/uat/assist_uat.sh — 主站 AI 助手（assist）自动化 UAT
# 覆盖（2026-09-16 R0 批次 + 既有链路回归 + 2026-09-17 改造 1A 融合项）：
#   C 端：greeting/chips、话术直配(rule)、流程触发与推进(flow)、知识兜底(fallback)、
#         history 回显、features、超长截断、缺参 400、错方法 405
#   R0.1 同义词归一：口语「怎么充钱」命中充值知识（原三层脱靶场景）
#   R0.2 兜底改造：零命中不再空承诺 + 未答问题登记（管理台清单可见）
#   R0.4 管理台：config key 白名单闸、api_key 掩码回显与回写 skip、
#         LLM 配置热加载（llm_mode rule→db）、测试连通端点（不可达可读报错）
#   管理端：401 鉴权、四表 CRUD、sessions 统计载荷
#   ★ 改造 1A（2026-09-17）：内嵌 seed 生效（不依赖外置文件）、内嵌管理页可达、
#         管理台 Token 主库桥接（MAIN_DB → system_config.assist_admin_token）与 env 优先级契约
#   ★ 〇-LK（2026-09-22）G 段：管理 Token 保存即生效（旧值即刻失效）、该键掩码与空值拒绝、
#         同会话重复 greet 不再堆重复欢迎语、换 Token 重启后老访客 tok 仍可用（sess_key 已持久化）
# 依赖：无（自起 assist mock 模式，临时 SQLite，端口默认 8793/8794）
# 用法：bash scripts/uat/assist_uat.sh
# ============================================================================
set -u
cd "$(dirname "$0")/../.." || exit 1

PORT="${ASSIST_UAT_PORT:-8793}"
PORT2="${ASSIST_UAT_PORT2:-8794}"
B="http://127.0.0.1:${PORT}"
B2="http://127.0.0.1:${PORT2}"
J='Content-Type: application/json'
TOK="uat-assist-$(date +%s)"
WORK=$(mktemp -d)
BIN="$WORK/assist-server"
PASS=0; FAIL=0; START=$(date +%s)

ck(){ if echo "$3" | grep -qE "$2"; then PASS=$((PASS+1)); echo "PASS|$1"; else FAIL=$((FAIL+1)); echo "FAIL|$1|want[$2]|got[${3:0:200}]"; fi; }

log(){ echo "[assist_uat] $*"; }

# ---------- 0. 构建（★ 改造 1A：源码已并入主 module，入口改为 cmd/assist-server）----------
log "构建 assist-server（主仓单 module）..."
(cd backend-go && go build -o "$BIN" ./cmd/assist-server) || { echo "构建失败"; exit 1; }

# ---------- 1. 启动（mock 规则模式 + 全新临时库；★ 不配 ASSIST_SEED/ASSIST_WEB）----------
# ★ 改造 1A：显式不传 ASSIST_SEED / ASSIST_WEB —— 验证 seed 与管理页均为二进制内嵌，
#   部署不再需要投放 web/ 与 seed/ 目录；内嵌若失效，下方 A1/A2/E1/E2 会直接红。
log "启动 assist :${PORT}（mock 规则模式，内嵌 seed + 内嵌管理页）..."
ASSIST_MOCK=1 ASSIST_ADMIN_TOKEN="$TOK" ASSIST_ADDR="127.0.0.1:${PORT}" \
  ASSIST_DB="$WORK/assist.db" \
  nohup "$BIN" > "$WORK/assist.log" 2>&1 < /dev/null &
PID=$!
OK=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$B/health" | grep -q '"ok":true'; then OK=1; break; fi
done
[ "${OK:-0}" = "1" ] || { echo "assist 启动失败"; tail -5 "$WORK/assist.log"; kill $PID 2>/dev/null; exit 1; }
log "就绪（${i}s）"

AH="X-Assist-Admin: $TOK"

# ---------- 2. C 端基础链路 ----------
R=$(curl -s "$B/api/assist/greeting?page=/")
ck A1-greeting '"greeting"' "$R"
ck A1-chips '积分怎么收费' "$R"
SID=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
# ★ P0-1（2026-09-18）：greeting 同时下发会话能力令牌 tok，chat/history 必须随带
TOK=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
if [ -z "$TOK" ]; then echo "FAIL|A1-tok-missing"; FAIL=$((FAIL+1)); fi

R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID\",\"tok\":\"$TOK\",\"message\":\"怎么收费？价格多少\",\"page\":\"/\"}")
ck A2-chat-rule '"source":"rule"' "$R"

curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID\",\"tok\":\"$TOK\",\"message\":\"我是新手不会用\"}" >/dev/null
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID\",\"tok\":\"$TOK\",\"message\":\"个人版\"}")
ck A3-flow-advance '"source":"flow"' "$R"

R=$(curl -s "$B/api/assist/history?session=$SID&tok=$TOK&limit=20")
ck A4-history 'assistant' "$R"
ck A4-history-user 'user' "$R"
# ★ P0-1 反向断言：无/伪令牌读历史必须 401
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/history?session=$SID")
ck A4-history-no-tok-401 '^401$' "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/history?session=$SID&tok=deadbeef")
ck A4-history-bad-tok-401 '^401$' "$C"

R=$(curl -s "$B/api/assist/features")
ck A5-features '"features"' "$R"

R=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/chat" -H "$J" -d '{"session":"","message":"x"}')
ck A6-chat-400 '^400$' "$R"
R=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$B/api/assist/chat")
ck A6-chat-405 '^405$' "$R"

# ---------- 2b. ★ 2026-09-20 复合意图让位（生产漏接修复：「印度语能翻译吗，一个字多少钱」
# 旧逻辑被价格话术直配抢答，语言侧信息全程不参与；现话术降为素材融合应答）----------
RC=$(curl -s "$B/api/assist/greeting?page=/")
SID_CI=$(echo "$RC" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TOK_CI=$(echo "$RC" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID_CI\",\"tok\":\"$TOK_CI\",\"message\":\"印度语能翻译吗，一个字多少钱\",\"page\":\"/\"}")
ck CI1-not-price-only '"source":"(llm|fallback)"' "$R"
ck CI1-lang-info '印地语|印度语' "$R"
ck CI1-price-info '积分|预充值' "$R"
# 纯价格问句仍走毫秒级话术直配（让位逻辑不得误伤单意图快答）
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID_CI\",\"tok\":\"$TOK_CI\",\"message\":\"多少钱\",\"page\":\"/\"}")
ck CI2-pure-price-rule '"source":"rule"' "$R"

# ---------- 3. R0.1 同义词归一：怎么充钱 ----------
# ★ 修复（2026-09-18 闸门回归）：B1/B2 各用全新会话，不再复用 $SID——
#   复用会让 B1「怎么充钱」进入的 recharge-guide 流程把 B2 的无意义输入当作流程答案吞掉，
#   兜底话术与未答登记都不再发生（假失败）。tok 是与 sid 绑定的 HMAC 能力令牌，
#   新会话必须重新 greeting 取 sid+tok，不能自造 sid。
RB=$(curl -s "$B/api/assist/greeting?page=/")
SID_SYN=$(echo "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TOK_SYN=$(echo "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID_SYN\",\"tok\":\"$TOK_SYN\",\"message\":\"怎么充钱\",\"page\":\"/\"}")
ck B1-synonym-recharge '充值|余额|套餐|积分' "$R"
ck B1-synonym-not-fallback-empty '"source":"' "$R"
if echo "$R" | grep -qE '这个问题我记下了|这个问题我还没学到'; then
  FAIL=$((FAIL+1)); echo "FAIL|B1-synonym-hit|got fallback text"
else
  PASS=$((PASS+1)); echo "PASS|B1-synonym-hit"
fi

# ---------- 4. R0.2 兜底改造 + 未答登记 ----------
RB=$(curl -s "$B/api/assist/greeting?page=/")
SID_UN=$(echo "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TOK_UN=$(echo "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID_UN\",\"tok\":\"$TOK_UN\",\"message\":\" xyzzy量子波动速翻布拉布拉 \"}")
ck B2-fallback-guide '先记下来|换个说法|入口' "$R"
ck B2-fallback-actions '"actions":\[{' "$R"
R=$(curl -s "$B/api/assist/admin/sessions" -H "$AH")
ck B2-unanswered 'xyzzy量子波动速翻布拉布拉' "$R"

# ---------- 5. R0.4 管理台：白名单 / 掩码 / 热加载 / 测试连通 ----------
# 5a. 白名单闸：未登记 key 拒绝
C=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"arbitrary_hack","value":"x"}')
ck C1-whitelist-400 '^400$' "$C"
# 5b. LLM 键可写 + 明文不回显（掩码形态）+ 掩码回写 skip
C=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"llm_api_key","value":"sk-abcdef123456"}')
ck C2-llmkey-write '^200$' "$C"
R=$(curl -s "$B/api/assist/admin/config" -H "$AH")
if echo "$R" | grep -q 'sk-abcdef123456'; then
  FAIL=$((FAIL+1)); echo "FAIL|C3-mask-no-leak|明文泄露"
else
  PASS=$((PASS+1)); echo "PASS|C3-mask-no-leak"
fi
ck C4-mask-shape '\*\*\*' "$R"
R=$(curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"llm_api_key","value":"sk-***56"}')
ck C5-masked-write-skip '"skipped":true' "$R"
# 5c. 热加载：写 base_url/model 后 llm_mode 翻转为 db（mock 下 client 由 configs 构建）
curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"llm_base_url","value":"http://127.0.0.1:9/v1"}' >/dev/null
curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"llm_model","value":"uat-model"}' >/dev/null
R=$(curl -s "$B/api/assist/admin/sessions" -H "$AH")
ck C6-hotreload-llmmode '"llm_mode":"db"' "$R"
# 5d. 测试连通：不可达端点返回可读错误而非挂死
R=$(curl -s -m 15 -X POST "$B/api/assist/admin/llm/test" -H "$AH")
ck C7-llmtest 'ok":false|error' "$R"
# 5e. 管理端 401
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/admin/kb")
ck C8-admin-401 '^401$' "$C"

# ---------- 6. 管理端 CRUD 回归 ----------
NID=$(curl -s -X POST "$B/api/assist/admin/scripts" -H "$AH" -H "$J" \
  -d "{\"key\":\"uat$(date +%s)\",\"stype\":\"keyword\",\"title\":\"UAT\",\"keywords\":\"uattest魔法词\",\"content\":\"UAT话术命中\",\"priority\":9,\"enabled\":1}" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id","0"))' 2>/dev/null)
[ -n "$NID" ] && [ "$NID" != "0" ] && { PASS=$((PASS+1)); echo "PASS|D1-crud-create($NID)"; } || { FAIL=$((FAIL+1)); echo "FAIL|D1-crud-create"; }
R=$(curl -s -X PUT "$B/api/assist/admin/scripts?id=$NID" -H "$AH" -H "$J" -d '{"content":"UAT改后"}')
ck D2-crud-update '"ok":true' "$R"
R=$(curl -s -X DELETE "$B/api/assist/admin/scripts?id=$NID" -H "$AH")
ck D3-crud-delete '"ok":true' "$R"

# ---------- 7. 管理页托管 ----------
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/assist/admin")
ck E1-admin-page '^200$' "$C"
# ★ 改造 1A：管理页为二进制内嵌（未配 ASSIST_WEB）——内容必须是真实页面而非 404 占位
R=$(curl -s "$B/assist/admin")
ck E2-admin-embedded 'AI 助手管理台' "$R"

# ---------- 8. ★ 改造 1A：管理台 Token 主库桥接 + env 优先级 ----------
# 构造最小主库（仅 system_config 表）：验证 assist 以只读方式读到该 Token。
# 注：写入值为明文——DecryptSecret 对无 enc:v1: 前缀的历史明文原样返回（兼容路径），
#     主后台经 /api/assist 写入时是 enc:v1: 密文，两条路径共用同一读取函数。
MAINDB="$WORK/main.db"
DBTOK="uat-dbtoken-$(date +%s)"
python3 - "$MAINDB" "$DBTOK" <<'PY'
import sqlite3, sys
conn = sqlite3.connect(sys.argv[1])
conn.execute("CREATE TABLE system_config(key TEXT PRIMARY KEY, value TEXT, updated_at TEXT)")
conn.execute("INSERT INTO system_config(key,value,updated_at) VALUES('assist_admin_token',?,datetime('now'))", (sys.argv[2],))
conn.commit(); conn.close()
PY

log "启动 assist :${PORT2}（无 ASSIST_ADMIN_TOKEN，改由主库桥接取 Token）..."
ASSIST_MOCK=1 ASSIST_ADDR="127.0.0.1:${PORT2}" ASSIST_DB="$WORK/assist2.db" \
  MAIN_DB="$MAINDB" \
  nohup "$BIN" > "$WORK/assist2.log" 2>&1 < /dev/null &
PID2=$!
OK2=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$B2/health" | grep -q '"ok":true'; then OK2=1; break; fi
done
[ "${OK2:-0}" = "1" ] || { echo "assist2 启动失败"; tail -5 "$WORK/assist2.log"; kill $PID2 2>/dev/null; exit 1; }

# 8a. 主库 Token 生效（env 未配 → 回落到 DB）
C=$(curl -s -o /dev/null -w '%{http_code}' "$B2/api/assist/admin/kb" -H "X-Assist-Admin: $DBTOK")
ck F1-dbtoken-works '^200$' "$C"
# 8b. 非法 Token 仍被拒（桥接不放松鉴权）
C=$(curl -s -o /dev/null -w '%{http_code}' "$B2/api/assist/admin/kb" -H "X-Assist-Admin: wrong-token")
ck F2-dbtoken-reject-wrong '^401$' "$C"
{ kill $PID2 2>/dev/null; wait $PID2 2>/dev/null; } 2>/dev/null || true

# 8c. env 优先级高于主库（同库同 Token，env 显式配置应压过 DB 值）
ENVTOK="uat-envtoken-$(date +%s)"
ASSIST_MOCK=1 ASSIST_ADMIN_TOKEN="$ENVTOK" ASSIST_ADDR="127.0.0.1:${PORT2}" \
  ASSIST_DB="$WORK/assist3.db" MAIN_DB="$MAINDB" \
  nohup "$BIN" > "$WORK/assist3.log" 2>&1 < /dev/null &
PID3=$!
OK3=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$B2/health" | grep -q '"ok":true'; then OK3=1; break; fi
done
[ "${OK3:-0}" = "1" ] || { echo "assist3 启动失败"; tail -5 "$WORK/assist3.log"; kill $PID3 2>/dev/null; exit 1; }
C=$(curl -s -o /dev/null -w '%{http_code}' "$B2/api/assist/admin/kb" -H "X-Assist-Admin: $ENVTOK")
ck F3-env-priority-works '^200$' "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B2/api/assist/admin/kb" -H "X-Assist-Admin: $DBTOK")
ck F4-env-priority-overrides-db '^401$' "$C"
{ kill $PID3 2>/dev/null; wait $PID3 2>/dev/null; } 2>/dev/null || true

# ---------- 9. ★ 〇-LK（2026-09-22）：Token 热生效 / 访客密钥解耦 / 欢迎语去重 ----------
# 这一段专门锁用户反馈的两件事：「ai 助手要带缓存，不然刷新一次页面就没了」与
# 「配置请参考我其他 llm 配置的方式重新做」。三件都是改坏就直接影响使用：
#   G1-G3 管理 Token 保存即生效（configs.admin_token 优先于启动快照，且不留双凭据窗口）；
#   G4-G5 该键的读取掩码 / 空值拒绝（清除只能走主后台显式 clear）；
#   G6 同一会话重复 greet 不再堆重复欢迎语（台账与 history 恢复都受影响）；
#   G7 换管理 Token 并重启后，老访客的会话令牌仍然有效（sess_key 已持久化、与 Token 解耦）。
# 放在最后：轮换会让前面用的 $AH 失效，temp 库随即销毁，不影响其它断言。
OLDTOK="${AH#X-Assist-Admin: }"
NEWTOK="uat-rotated-$(date +%s)"
R=$(curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d "{\"key\":\"admin_token\",\"value\":\"$NEWTOK\"}")
ck G1-rotate-write '"ok":true' "$R"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/admin/kb" -H "X-Assist-Admin: $NEWTOK")
ck G2-new-token-live '^200$' "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/admin/kb" -H "X-Assist-Admin: $OLDTOK")
ck G3-old-token-dead '^401$' "$C"
# G4：管理面列配置时 admin_token 只能以掩码出现（明文既不进响应也不进日志）
R=$(curl -s "$B/api/assist/admin/config" -H "X-Assist-Admin: $NEWTOK")
if echo "$R" | grep -q "$NEWTOK"; then
  FAIL=$((FAIL+1)); echo "FAIL|G4-admin-token-masked|明文泄露"
else
  PASS=$((PASS+1)); echo "PASS|G4-admin-token-masked"
fi
ck G4b-token-listed 'admin_token' "$R"
ck G4c-token-masked '\*{3,}' "$R"# G5：空值写入拒绝（空串清除会把管理面一把关掉，属显式 clear 流程的职责）
C=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$B/api/assist/admin/config" -H "X-Assist-Admin: $NEWTOK" -H "$J" -d '{"key":"admin_token","value":""}')
ck G5-empty-token-400 '^400$' "$C"

# G6：欢迎语去重（同一 sid 连续 greet 三次，台账里只留一条开场白）
RG=$(curl -s "$B/api/assist/greeting?page=/")
SID_G=$(echo "$RG" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TOK_G=$(echo "$RG" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
for i in 1 2 3; do curl -s "$B/api/assist/greeting?session=$SID_G&tok=$TOK_G&page=/" >/dev/null; done
NA=$(curl -s "$B/api/assist/history?session=$SID_G&tok=$TOK_G&limit=50" \
  | python3 -c 'import sys,json;print(sum(1 for m in json.load(sys.stdin).get("messages",[]) if m.get("role")=="assistant"))')
if [ "$NA" = "1" ]; then PASS=$((PASS+1)); echo "PASS|G6-greet-dedup"; else FAIL=$((FAIL+1)); echo "FAIL|G6-greet-dedup|want 1 got $NA"; fi

# G7：sess_key 与 Token 解耦——换 Token 重启后，老访客的 tok 仍可续用（刷新页面不再丢对话）
{ kill $PID 2>/dev/null; wait $PID 2>/dev/null; } 2>/dev/null || true
log "重启 assist :${PORT}（新 ASSIST_ADMIN_TOKEN，同一 ASSIST_DB）验证会话密钥不随 Token 变化..."
ASSIST_MOCK=1 ASSIST_ADMIN_TOKEN="uat-restarted-$(date +%s)" ASSIST_ADDR="127.0.0.1:${PORT}" \
  ASSIST_DB="$WORK/assist.db" \
  nohup "$BIN" > "$WORK/assist-restart.log" 2>&1 < /dev/null &
PID=$!
OKR=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$B/health" | grep -q '"ok":true'; then OKR=1; break; fi
done
[ "${OKR:-0}" = "1" ] || { echo "assist 重启失败"; tail -5 "$WORK/assist-restart.log"; exit 1; }
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/history?session=$SID_G&tok=$TOK_G&limit=50")
ck G7-sess-key-survives-restart '^200$' "$C"
# 反向：老会话的 tok 不是任何人都能续——伪令牌仍须 401（否则 G7 就成了「不设防」）
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/history?session=$SID_G&tok=deadbeef")
ck G8-fake-tok-still-401 '^401$' "$C"

# ---------- 汇总 ----------
DUR=$(( $(date +%s) - START ))
log "=============================="
log "assist UAT：PASS=$PASS FAIL=$FAIL DUR=${DUR}s"
log "日志目录：$WORK"
log "=============================="
{ kill $PID 2>/dev/null; wait $PID 2>/dev/null; } 2>/dev/null || true
rm -rf "$WORK"
[ "$FAIL" = "0" ] || exit 1
exit 0
