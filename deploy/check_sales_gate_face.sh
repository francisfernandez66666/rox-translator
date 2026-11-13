#!/usr/bin/env bash
# ============================================================================
# deploy/check_sales_gate_face.sh —— 销售事实注入的**现网探针**（★ 四期 W1，施工图 §4.1.8）
#
# 它回答两个问题，而且只有把这两个问题分开问才答得准：
#   ① 开闸之后，**匿名访客的产品页**真拿到了事实集吗（不是"代码里有这条路"）；
#   ② 拿到的**只是这个产品**的事实吗（不是"该租户全部产品"，那是三期串台那一行）。
#
# 为什么不能只看仓库：W1 改的是"作用域能不能注入"，而这条链坏掉的形态是**静默**的——
#   作用域解析回 denied ⇒ 事实集是空集 ⇒ 出站头不签名 ⇒ assist 侧 fail-closed 回避 ⇒
#   访客照常收到一条**看起来正常**的回答（只是没有产品事实支撑）。仓库单测全绿、日志只有一行 WARN，
#   界面上零症状。同理"两把开关只开了一把"也是静默半开档（half）。
#
# 判据（施工图 §4.1.8 的 D1–D5）：
#   D1  两开关现值 + `/api/health.sales_facts.gate` 两处对读（on/half/off 三态如实出）；
#   D1b 取一个 live 产品页，用 python3 **镜像准入读口**算出它此刻应注入几条
#       （status='confirmed' AND visibility='public' AND locked=0 AND product_id=…，
#       译文存在且 source_hash == sha256(原文)、非空，上限 200 条）；
#   D2  匿名打该产品页的挂件 chat ⇒ `sales_facts.injected` 增量 == D1b 的期望值
#       （期望 ≤20 时判等值；更大时判落在 1..期望 并记一项没测到——见下方理由），
#        同时 `scope_anon` 增量必须 == 1（作用域确实被判成匿名产品页）；
#   D3  串台负向腿：同租户**另一个**产品的可注入事实数 N₂>0 时，本次增量必须仍等于本品期望
#       （等于 期望+N₂ 就是"读口退回整租户"那一行的现网指纹）；
#   D4  反向对照：把 `sales_facts_injection_enabled` 临时置 0（→ gate 变 half），
#       再打同一页 ⇒ `injected` 与 `scope_anon` 增量都必须是 0，最后**成对恢复原值**并复验 gate 回原档；
#   D5  归因行带产品维度：那次匿名 chat 之后 `reply_attributions` 里该 `message_id` 的行
#       `tenant_id=产品租户 AND product_id=该产品` 且 `conversation_id=0`，
#       并且 `handoff_sessions` 里**没有**该会话的行（匿名不许建孤儿会话，F11③）。
#       ⚠️ 只判"有一行"不够——三期也有一行，差别只在 product_id 与那个孤儿 handoff 行。
#
# ★ D4 是本文件**唯一的写动作**——施工图 §4.1.8 把这条探针写成"只读"，但 D4 的语义
#   （"关掉一把开关 ⇒ 事实集回空"）物理上只能靠改一次配置来制造。四条纪律：
#   · 只改一件、只改到 `half`（另一把开关不动），跑完在 EXIT 陷阱里恢复**原值**；
#   · 原行不存在时恢复成"删掉这一行"（"行不存在"与"值是空串"是两种形态，混了会把灰度读成已配）；
#   · 原 `updated_at` 为 NULL 时也恢复成 NULL（不写成空串：`COALESCE(updated_at,'')` 的读点会把两者看成一样，
#     但下一个拿 `IS NULL` 判新鲜度的人会拿到相反结论）；
#   · 恢复后**复验 gate 回到原档**（配置缓存 TTL 默认 5s）；恢复失败或复验不过都打 BAD 并要求手工恢复
#     ——绝不能让它悄悄留成 0（那等于这次探针把事实注入静默关掉了）。
#   · 回写前先过**白名单形状守卫**（`sql_lit_ok`：只放行 [A-Za-z0-9_.:+-]）。原值/原时间戳含单引号、
#     反斜杠、空白等 ⇒ **整条 D4 跳过**（出 skip 行）。理由：恢复语句被值里的引号拆开时，
#     库里会停在"开关=0"而屏幕上看不见——恢复失败比"这次没测到"坏得多。
#     同理 `CFG_EXISTED` 只认 0/1、`updated_at IS NULL` 只认 t/f；读不到预期形态一律不写。
#   · `TENANT_CODE`/`PAGE_CODE`（可选手工钉靶）同样过形状守卫，不合就**忽略并落自动挑靶**——
#     带着一个带引号的值去撞 SQL 语法错，红字会指向"库里没这个产品"。
#
# ★ 为什么 D2 大集合只判区间：`engine.LimitSalesFacts` 还会按 32 KiB 载重二次截断，而那个字节数
#   取决于事实译文长度（每台机器不同）。>20 条时脚本只判"落在 1..期望"并把**等值腿记成没测到**，
#   而不是把一次正常的字节截断判成产品缺陷（AGENTS §一·5 ⑥「读数不许为失败背书」，同理不许为诚实截断报红）。
#
# ★ `injected` / `scope_anon` 是**进程级累计计数**：判据问的是"这一枪前后差了多少"。
#   现网有并发访客时，别人那一枪也会加进这个差里 ⇒ 增量为正且偏大时，红字会点名这个可能，
#   但不自动重拨（重拨到"安静窗口"就是把真泄漏摇成绿灯）。要复判请挑低峰跑，或先看日志里那一行
#   `销售事实集已下发` 的 `reason`/`product_id`。
#
# ★ 语种：本脚本一律用 `X-App-Lang`（默认 en）指定，并**只**认这一条通道——
#   chat 的 body 里虽然有 `lang`，但 `seoLangFromRequest` 只读 query/头，body 那份不参与事实取数
#   （三期既有形态，四期 W1 未扩范围；已登记为 W2 候选）。探针若跟着前端只送 body，
#   会拿到 zh 译文而期望值按 en 算，红字会指错方向。
#
# 三态（同仓其它 face 脚本口径）：exit 0 全绿／1 有红项／**2 有 N 项没测到**（⏳）。
# 拿到 2 **既不是通过也不是产品缺陷**：现场没有 live 产品、没有准入门槛内的事实、
# 闸没全开、或这一跳没走完时，都如实记"没测到"，交人复跑或先建档再判。
#
# 用法（在**部署机**上跑；本脚本不落凭据、不打印任何事实值/产品名/租户名）：
#   bash deploy/check_sales_gate_face.sh
#   可选 env：
#     BASE_URL=http://127.0.0.1:8787   主服务基址（默认回环，必须在部署机上跑）
#     DB=langcross                     库名
#     PSQL_BIN=psql / PSQL_SUDO=0      以 postgres 身份连接（默认 sudo -u postgres；PSQL_SUDO=0 走当前用户）
#     PROBE_LANG=en                    事实取数语种（同时走 X-App-Lang 头）
#     TENANT_CODE=/PAGE_CODE=          显式钉住某一个产品页（不传则自动挑第一个有准入事实的 live 产品）
#     CURL_MAX_TIME=60 / CONFIG_TTL_WAIT=20
#   判绿只认末行 `SALES_GATE_FACE_ALL_OK=1`。
# ============================================================================
set -uo pipefail

BASE_URL=${BASE_URL:-http://127.0.0.1:8787}
DB=${DB:-langcross}
PSQL_BIN=${PSQL_BIN:-psql}
PSQL_SUDO=${PSQL_SUDO:-1}
PROBE_LANG=${PROBE_LANG:-en}
CURL_MAX_TIME=${CURL_MAX_TIME:-60}
CONFIG_TTL_WAIT=${CONFIG_TTL_WAIT:-20}
TENANT_CODE=${TENANT_CODE:-}
PAGE_CODE=${PAGE_CODE:-}

FAIL=0
UNMEASURED=0
ok()   { echo "OK   $*"; }
bad()  { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
note() { echo "NOTE $*"; }
skip() { echo "⏳   $*"; UNMEASURED=$((UNMEASURED + 1)); }
finish() {
  echo "SALES_GATE_FACE_FAIL=${FAIL} UNMEASURED=${UNMEASURED}"
  if [ "${FAIL}" -gt 0 ]; then echo "SALES_GATE_FACE_ALL_OK=0"; return 1; fi
  if [ "${UNMEASURED}" -gt 0 ]; then echo "SALES_GATE_FACE_UNMEASURED=1"; return 2; fi
  echo "SALES_GATE_FACE_ALL_OK=1"
  return 0
}

# db_q <SQL>：单值/单行查询（-A -t）。**失败退非零、成功打结果**——
# 不用"全局 DB_RC"那种写法：`X=$(db_q …)` 里的赋值发生在子 shell，父 shell 永远读到旧值，
# 于是"查失败"会被读成"查到了空"。空结果与查失败必须分得开（AGENTS §三 那条）。
DB_ERR=""
db_q() {
  local out rc
  if [ "${PSQL_SUDO}" = "1" ]; then
    out=$(sudo -u postgres "${PSQL_BIN}" -d "${DB}" -Atc "$1" 2>&1); rc=$?
  else
    out=$("${PSQL_BIN}" -d "${DB}" -Atc "$1" 2>&1); rc=$?
  fi
  if [ "${rc}" -ne 0 ]; then
    DB_ERR=${out}
    return 1
  fi
  printf '%s' "${out}"
  return 0
}

# is_num <串>：非负整数（增量算术前的守卫——把 "NOT_SET" 喂给 $((…)) 会让脚本自己崩在判据之外）。
is_num() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
    *) return 0 ;;
  esac
}

# code_shape_ok <串>：租户码/页面码/产品主键（TEXT）的形状守卫——它们会被拼进 SQL，
# 所以宁可跳过也不能让一个带引号的值把后面的判据打成语法错。
code_shape_ok() {
  case "$1" in
    ''|*[!A-Za-z0-9_-]*) return 1 ;;
    *) return 0 ;;
  esac
}

# sql_lit_ok <串>：拼进 SQL 字面量前的**白名单**形状守卫。
# 只放行 [A-Za-z0-9_.:+-]（够 '0'/'1'、UTC 时间戳、点号版本号）；单引号/反斜杠/换行/空格一律拒。
# 为什么是白名单而不是"转义一下"：D4 用它回写**原值**，一旦回写语句被值里的引号拆开，
# 库里就停在"开关=0"而我方以为已恢复——恢复失败比不写更坏，所以形状不合就整条 D4 跳过（出 skip 行）。
sql_lit_ok() {
  case "$1" in
    ''|*[!A-Za-z0-9_.:+-]*) return 1 ;;
    *) return 0 ;;
  esac
}

# face_curl <方法> <url> [curl 参数...]：一次请求，rc / http_code / body 落到 FACE_*。
# 完整性判据（AGENTS §一·5 ⑤）：rc≠0 或 code=000 ＝「这一跳没走完」⇒ 换 --http1.1 再拨一次；
# 4xx/5xx 与"内容不对"**一律不重拨**——摇绿灯就是把缺陷藏起来。
FACE_RC=""; FACE_CODE=""; FACE_TEXT=""; FACE_RETRIED=0
face_curl() {
  local method="$1" url="$2"; shift 2
  local tmp code rc
  tmp=$(mktemp) || { FACE_RC=1; FACE_CODE=""; FACE_TEXT=""; return; }
  code=$(curl -sS -o "${tmp}" -w '%{http_code}' -X "${method}" --max-time "${CURL_MAX_TIME}" "$@" "${url}" 2>/dev/null)
  rc=$?
  FACE_RETRIED=0
  if [ "${rc}" -ne 0 ] || [ "${code}" = "000" ]; then
    code=$(curl --http1.1 -sS -o "${tmp}" -w '%{http_code}' -X "${method}" --max-time "${CURL_MAX_TIME}" "$@" "${url}" 2>/dev/null)
    rc=$?
    FACE_RETRIED=1
  fi
  FACE_RC="${rc}"
  FACE_CODE="${code}"
  FACE_TEXT=$(tr '\n' ' ' < "${tmp}")
  rm -f "${tmp}"
}

# jget <json> <键>：取顶层字段（解析失败退 2、键缺失退 3、"取到"退 0）。
jget() {
  printf '%s' "$1" | python3 -c '
import json, sys
raw = sys.stdin.read()
key = sys.argv[1]
try:
    d = json.loads(raw)
except Exception:
    sys.exit(2)
if not isinstance(d, dict) or key not in d:
    sys.exit(3)
v = d[key]
print("" if v is None else v)
' "$2"
}

# jget_path <json> <父键> <子键>：取一层嵌套字段（同上退出码约定）。
jget_path() {
  printf '%s' "$1" | python3 -c '
import json, sys
raw = sys.stdin.read()
parent, key = sys.argv[1], sys.argv[2]
try:
    d = json.loads(raw)
except Exception:
    sys.exit(2)
if not isinstance(d, dict):
    sys.exit(2)
p = d.get(parent)
if not isinstance(p, dict) or key not in p:
    sys.exit(3)
v = p[key]
print("" if v is None else v)
' "$2" "$3"
}

# parse_bool 与 strconv.ParseBool 同口径（杯子之外的值一律 err ⇒ 上游按 off 处理）。
parse_bool() {
  local v
  v=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
  case "${v}" in
    1|t|true) echo on ;;
    0|f|false) echo off ;;
    *) echo err ;;
  esac
}

# gate_word 与 sales_facts_scope.go 的 salesFactsGateWord 逐行对应：
# 任一不可解析 / 两把都没开 ⇒ off；全开 ⇒ on；只开一把 ⇒ half。
gate_word() {
  local inj guard
  inj=$(parse_bool "$1"); guard=$(parse_bool "$2")
  if [ "${inj}" = "err" ] || [ "${guard}" = "err" ]; then echo off; return; fi
  if [ "${inj}" = "off" ] && [ "${guard}" = "off" ]; then echo off; return; fi
  if [ "${inj}" = "on" ] && [ "${guard}" = "on" ]; then echo on; return; fi
  echo half
}

health_gate() {
  face_curl GET "${BASE_URL}/api/health"
  if [ "${FACE_RC}" != "0" ] || [ "${FACE_CODE}" != "200" ]; then
    echo ""
    return 1
  fi
  jget_path "${FACE_TEXT}" sales_facts gate 2>/dev/null || echo ""
}

# ------------------------------------------------------------------ P0 前置
if ! command -v curl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  echo "P0 本机缺 curl 或 python3 ⇒ 一条都测不到"
  skip "P0 缺 curl/python3"
  finish
  exit 2
fi
if ! command -v "${PSQL_BIN}" >/dev/null 2>&1; then
  echo "P0 缺 ${PSQL_BIN} ⇒ D1/D1b/D3/D5 的期望值都算不出来（它们必须以那一刻的库为准，不许写死）"
  skip "P0 缺 ${PSQL_BIN}"
  finish
  exit 2
fi

# ------------------------------------------------------------------ D1 开关现值
INJ=""
if ! INJ=$(db_q "SELECT value FROM system_config WHERE key='sales_facts_injection_enabled'"); then
  echo "P0 读不到 system_config（库连不上或权限不足）：${DB_ERR}"
  skip "P0 库读失败 ⇒ 五条判据的基准全缺"
  finish
  exit 2
fi
GUARD=""
if ! GUARD=$(db_q "SELECT value FROM system_config WHERE key='sales_claim_guard_enabled'"); then
  echo "P0 读 sales_claim_guard_enabled 失败：${DB_ERR}"
  skip "P0 库读失败"
  finish
  exit 2
fi
INJ_SHOW=${INJ:-NOT_SET}
GUARD_SHOW=${GUARD:-NOT_SET}
GATE=$(gate_word "${INJ}" "${GUARD}")

face_curl GET "${BASE_URL}/api/health"
if [ "${FACE_RC}" != "0" ] || [ "${FACE_CODE}" != "200" ]; then
  bad "D1 打不开 ${BASE_URL}/api/health（rc=${FACE_RC} code=${FACE_CODE:-NOT_SET}）⇒ 后面几条都没有读数基准"
  HEALTH_GATE=""
else
  HEALTH_GATE=$(jget_path "${FACE_TEXT}" sales_facts gate 2>/dev/null) || HEALTH_GATE=""
  if [ -z "${HEALTH_GATE}" ]; then
    bad "D1 /api/health 缺 sales_facts.gate 段（F9 的接线腿在现网不成立）"
  elif [ "${HEALTH_GATE}" = "${GATE}" ]; then
    ok "D1 开关现值 inj=${INJ_SHOW} guard=${GUARD_SHOW} ⇒ gate=${GATE}，health 同值"
  else
    # TTL 窗口内（默认 5s）两处可能各读一次；等过 TTL 再读一次，仍不一致才是真不一致
    sleep 6
    if ! INJ2=$(db_q "SELECT value FROM system_config WHERE key='sales_facts_injection_enabled'"); then INJ2=${INJ}; fi
    if ! GUARD2=$(db_q "SELECT value FROM system_config WHERE key='sales_claim_guard_enabled'"); then GUARD2=${GUARD}; fi
    GATE2=$(gate_word "${INJ2}" "${GUARD2}")
    HEALTH_GATE=$(health_gate)
    if [ "${HEALTH_GATE}" = "${GATE2}" ]; then
      ok "D1 gate=${GATE2}（首次相差是配置缓存 TTL 窗口，过窗后同值）"
      GATE=${GATE2}
    else
      bad "D1 库内两开关推出 gate=${GATE2}，而 /api/health 报 gate=${HEALTH_GATE:-NOT_SET} ⇒ 进程读的不是这份库（多实例/连错库/水合未跟上）"
    fi
  fi
fi

# health 计数快照：SNAP_INJ / SNAP_ANON
SNAP_INJ=""; SNAP_ANON=""
health_snapshot() {
  SNAP_INJ=""; SNAP_ANON=""
  face_curl GET "${BASE_URL}/api/health"
  if [ "${FACE_RC}" != "0" ] || [ "${FACE_CODE}" != "200" ]; then return; fi
  SNAP_INJ=$(jget_path "${FACE_TEXT}" sales_facts injected 2>/dev/null) || SNAP_INJ=""
  SNAP_ANON=$(jget_path "${FACE_TEXT}" sales_facts scope_anon 2>/dev/null) || SNAP_ANON=""
}

# ------------------------------------------------------------------ D1b 目标产品与期望条数
# 优先挑"有准入事实"的 live 产品（否则 D2 的增量判据没有判别力）；找不到时退回任一个 live 产品，
# 让 D5（归因维度）仍能跑——归因写回与事实条数无关。
PROD_ROW=""
# 显式钉靶的形状守卫：TENANT_CODE/PAGE_CODE 会被拼进 SQL。形状不合 ⇒ 忽略（落自动挑靶），
# 而不是带着一个带引号的值去撞语法错——那会红在"产品不存在"上，把病因指向库里没这个产品。
if [ -n "${TENANT_CODE}" ] && ! code_shape_ok "${TENANT_CODE}"; then
  note "TENANT_CODE 形状不合（只许 [A-Za-z0-9_-]）⇒ 忽略显式钉靶，改为自动挑靶"
  TENANT_CODE=""
fi
if [ -n "${PAGE_CODE}" ] && ! code_shape_ok "${PAGE_CODE}"; then
  note "PAGE_CODE 形状不合（只许 [A-Za-z0-9_-]）⇒ 忽略显式钉靶，改为自动挑靶"
  PAGE_CODE=""
fi
if [ -n "${TENANT_CODE}" ] && [ -n "${PAGE_CODE}" ]; then
  PROD_ROW=$(db_q "SELECT tenant_code||'|'||page_code||'|'||tenant_id||'|'||id FROM products
    WHERE status='live' AND tenant_code='${TENANT_CODE}' AND page_code='${PAGE_CODE}' LIMIT 1") || PROD_ROW=""
else
  PROD_ROW=$(db_q "SELECT p.tenant_code||'|'||p.page_code||'|'||p.tenant_id||'|'||p.id FROM products p
    WHERE p.status='live' AND p.tenant_code<>'' AND p.page_code<>''
      AND EXISTS (SELECT 1 FROM product_facts f WHERE f.tenant_id=p.tenant_id AND f.product_id=p.id
                  AND f.status='confirmed' AND f.visibility='public' AND f.locked=0)
    ORDER BY p.id LIMIT 1") || PROD_ROW=""
  if [ -z "${PROD_ROW}" ]; then
    PROD_ROW=$(db_q "SELECT p.tenant_code||'|'||p.page_code||'|'||p.tenant_id||'|'||p.id FROM products p
      WHERE p.status='live' AND p.tenant_code<>'' AND p.page_code<>'' ORDER BY p.id LIMIT 1") || PROD_ROW=""
  fi
fi

PTENANT=""; PCODE=""; PTID=""; PID=""
PAGE_PATH=""
LANG_NORM=$(printf '%s' "${PROBE_LANG}" | tr '[:upper:]' '[:lower:]' | tr '-' '_')
if [ -z "${LANG_NORM}" ]; then LANG_NORM="zh"; fi

# compute_expected <tid> <pid> <lang>：镜像 store.ListSalesFactsForInjection 的准入判据。
# 只算"条数"（受控键不下发，按 0 算——它们物理上只有一个 kind+key，不进 facts）。
compute_expected() {
  local tid="$1" pid="$2" lang="$3" facts trans
  if ! facts=$(db_q "SELECT COALESCE(json_agg(json_build_object('id',id,'value',value) ORDER BY priority DESC, id), '[]'::json)::text
      FROM product_facts
      WHERE tenant_id=${tid} AND product_id='${pid}'
        AND status='confirmed' AND visibility='public' AND locked=0"); then
    echo "ERR"; return
  fi
  if ! trans=$(db_q "SELECT COALESCE(json_agg(json_build_object('fid',fact_id,'hash',source_hash,'value',value)), '[]'::json)::text
      FROM product_fact_i18n WHERE tenant_id=${tid} AND lang='${lang}'"); then
    echo "ERR"; return
  fi
  printf '%s\n%s\n' "${facts}" "${trans}" | python3 -c '
import hashlib, json, sys
lines = sys.stdin.read().split("\n")
try:
    facts = json.loads(lines[0] or "[]")
    trans = json.loads(lines[1] or "[]")
except Exception:
    print("ERR"); sys.exit(0)
by = {}
for t in trans:
    by[int(t["fid"])] = t
n = 0
for f in facts:
    t = by.get(int(f["id"]))
    if not t:
        continue
    if t["hash"] != hashlib.sha256(str(f["value"]).encode()).hexdigest():
        continue
    if not str(t["value"]).strip():
        continue
    n += 1
print(min(n, 200))   # 镜像 engine.MaxSalesFacts（200）；32 KiB 载重截断由 D2 的区间腿兜
'
}

EXPECTED=""
if [ -z "${PROD_ROW}" ]; then
  skip "D1b 库里没有可用的 live 产品页（products.status='live' 且两段码非空）⇒ D2/D3/D5 失去靶子；先在管理台建一个产品并转 live"
else
  PTENANT=${PROD_ROW%%|*}
  rest=${PROD_ROW#*|}
  PCODE=${rest%%|*}
  rest=${rest#*|}
  PTID=${rest%%|*}
  PID=${rest#*|}
  if ! code_shape_ok "${PTENANT}" || ! code_shape_ok "${PCODE}" || ! code_shape_ok "${PID}" || ! is_num "${PTID}" || [ "${PTID}" = "0" ]; then
    skip "D1b 产品行形状不合（tenant_code/page_code/id 含预期外字符，或 tenant_id 非正整数）⇒ 不拿它当靶子"
  else
    PAGE_PATH="/p/${PTENANT}/${PCODE}"
    EXPECTED=$(compute_expected "${PTID}" "${PID}" "${LANG_NORM}")
    case "${EXPECTED}" in
      ''|ERR)
        EXPECTED=""
        skip "D1b 期望条数算不出来（库读失败）⇒ D2 的等值腿没有基准"
        ;;
      *)
        ok "D1b 靶子=${PAGE_PATH}（id=${PID}, tid=${PTID}, lang=${LANG_NORM}）此刻应注入 ${EXPECTED} 条"
        ;;
    esac
  fi
fi

# ------------------------------------------------------------------ D2 匿名产品页真注入
SID=""; TOK=""; MID=""
DELTA_INJ=""; DELTA_ANON=""
chat_body() { # $1=message
  python3 -c '
import json, sys
print(json.dumps({"session": sys.argv[1], "tok": sys.argv[2], "message": sys.argv[3],
                  "page": sys.argv[4], "lang": sys.argv[5]}, ensure_ascii=False))
' "${SID}" "${TOK}" "$1" "${PAGE_PATH}" "${PROBE_LANG}"
}

if [ -z "${PAGE_PATH}" ]; then
  skip "D2 缺靶子，未测"
elif [ "${GATE}" != "on" ]; then
  skip "D2 gate=${GATE}（两把开关没全开）⇒ 现在打这一页本来就该是空集；这不是缺陷，是灰度未开"
else
  # greet 不带页面头（page=/ 走无作用域档），只用来拿会话；事实注入的唯一一轮放在 chat 上。
  face_curl GET "${BASE_URL}/assist-api/api/assist/greeting?page=%2F&lang=${PROBE_LANG}" \
    -H "X-App-Lang: ${PROBE_LANG}"
  if [ "${FACE_RC}" != "0" ] || [ "${FACE_CODE}" != "200" ]; then
    skip "D2 greeting 这一跳没走完（rc=${FACE_RC} code=${FACE_CODE:-NOT_SET}）⇒ 链路问题，不是产品缺陷"
  else
    SID=$(jget "${FACE_TEXT}" session 2>/dev/null) || SID=""
    TOK=$(jget "${FACE_TEXT}" tok 2>/dev/null) || TOK=""
    if [ -z "${SID}" ] || [ -z "${TOK}" ]; then
      bad "D2 greeting 响应缺 session/tok（code=${FACE_CODE}）⇒ 挂件链路的会话建立在现网不成立"
    fi
  fi
  if [ -n "${SID}" ] && [ -n "${TOK}" ]; then
    health_snapshot
    if ! is_num "${SNAP_INJ}" || ! is_num "${SNAP_ANON}"; then
      skip "D2 health 缺 sales_facts 计数（injected/scope_anon）⇒ 增量判据没有读数"
    else
      face_curl POST "${BASE_URL}/assist-api/api/assist/chat" \
        -H 'Content-Type: application/json' -H "X-App-Lang: ${PROBE_LANG}" -H "X-LangCross-Page: ${PAGE_PATH}" \
        -d "$(chat_body '请用一句话介绍这个产品。')"
      if [ "${FACE_RC}" != "0" ] || [ "${FACE_CODE}" != "200" ]; then
        skip "D2 chat 这一跳没走完（rc=${FACE_RC} code=${FACE_CODE:-NOT_SET}）⇒ 链路问题，不是产品缺陷"
      else
        MID=$(jget_path "${FACE_TEXT}" attribution message_id 2>/dev/null) || MID=""
        face_curl GET "${BASE_URL}/api/health"
        AFTER_INJ=$(jget_path "${FACE_TEXT}" sales_facts injected 2>/dev/null) || AFTER_INJ=""
        AFTER_ANON=$(jget_path "${FACE_TEXT}" sales_facts scope_anon 2>/dev/null) || AFTER_ANON=""
        if ! is_num "${AFTER_INJ}" || ! is_num "${AFTER_ANON}"; then
          skip "D2 事后 health 缺计数 ⇒ 增量判据没有读数"
        else
          DELTA_INJ=$((AFTER_INJ - SNAP_INJ))
          DELTA_ANON=$((AFTER_ANON - SNAP_ANON))
          if [ "${DELTA_ANON}" != "1" ]; then
            bad "D2 scope_anon 增量=${DELTA_ANON}（期望 1）⇒ 这次匿名请求没有被判成「匿名产品页」；作用域解析或页面头都没生效"
          else
            ok "D2 scope_anon 增量=1（匿名产品页作用域成立）"
          fi
          if [ -z "${EXPECTED}" ]; then
            skip "D2 期望条数未知，未判等值（injected 增量=${DELTA_INJ}）"
          elif [ "${EXPECTED}" = "0" ]; then
            skip "D2 该产品此刻 0 条准入事实（或译文缺失/hash 失配）⇒ 注入为空的读数没有判别力；先备一条公开事实再复跑"
          elif [ "${DELTA_INJ}" = "${EXPECTED}" ]; then
            ok "D2 injected 增量=${DELTA_INJ} == 期望 ${EXPECTED}（匿名产品页真拿到了这份事实集）"
          elif [ "${EXPECTED}" -gt 20 ] && [ "${DELTA_INJ}" -ge 1 ] && [ "${DELTA_INJ}" -le "${EXPECTED}" ]; then
            ok "D2 injected 增量=${DELTA_INJ} 落在 1..${EXPECTED}（大集合受 32 KiB 载重截断，方向正确）"
            skip "D2 大集合（>20 条）不判字节级等值 ⇒ 等值腿记没测到（这是诚实的截断，不是缺陷）"
          else
            bad "D2 injected 增量=${DELTA_INJ}，期望 ${EXPECTED} ⇒ 事实集没有按这个产品下发。
     排查顺序：① D1 的 gate 是否真 on；② ASSIST_PROFILE_KEY 是否配在**主服务**（未配密钥 ⇒ attachSalesFacts 静默不发，这是本链最静默的一档）；
     ③ 该产品事实的译文是否落在 lang=${LANG_NORM} 且 hash 与原文一致（D1b 的期望值就是按这个口径算的）；
     ④ 增量偏大时先排除并发访客（这是进程级累计计数，别人的一枪也算在这个差里）。"
          fi
        fi
      fi
    fi
  fi
fi

# ------------------------------------------------------------------ D3 串台负向腿
if [ -z "${PAGE_PATH}" ] || [ -z "${EXPECTED}" ] || [ "${GATE}" != "on" ] || [ -z "${DELTA_INJ}" ]; then
  skip "D3 缺靶子/缺期望/闸未全开 ⇒ 串台腿没有基准"
else
  SIB_ROW=$(db_q "SELECT p.id FROM products p
    WHERE p.tenant_id=${PTID} AND p.id<>'${PID}' AND p.status='live'
      AND EXISTS (SELECT 1 FROM product_facts f WHERE f.tenant_id=p.tenant_id AND f.product_id=p.id
                  AND f.status='confirmed' AND f.visibility='public' AND f.locked=0)
    ORDER BY p.id LIMIT 1") || SIB_ROW=""
  if [ -z "${SIB_ROW}" ] || ! code_shape_ok "${SIB_ROW}"; then
    skip "D3 同租户没有第二个带准入事实的 live 产品 ⇒ 串台负向腿没有对照物（单产品租户本来就测不了这一条）"
  else
    SIB_EXPECTED=$(compute_expected "${PTID}" "${SIB_ROW}" "${LANG_NORM}")
    case "${SIB_EXPECTED}" in
      ''|ERR|0)
        skip "D3 同租户第二个产品的期望条数不可用 ⇒ 对照物不成立"
        ;;
      *)
        if [ "${DELTA_INJ}" = "${EXPECTED}" ]; then
          ok "D3 同租户另有 ${SIB_EXPECTED} 条可注入事实，本次增量仍是 ${DELTA_INJ}（== 本品期望）⇒ 没有串台"
        else
          bad "D3 同租户第二个产品有 ${SIB_EXPECTED} 条可注入事实，本次增量=${DELTA_INJ} 与本品期望=${EXPECTED} 不符 ⇒ 先看 D2 的红；若 D2 绿而这里恰好等于 期望+${SIB_EXPECTED}，就是读口退回整租户（三期串台那一行）"
        fi
        ;;
    esac
  fi
fi

# ------------------------------------------------------------------ D4 反向对照（唯一写动作）
CFG_KEY="sales_facts_injection_enabled"
CFG_WROTE=0
CFG_EXISTED=0
CFG_ORIG=""
CFG_ORIG_UPD=""
CFG_ORIG_UPD_NULL="f"
restore_switch() {
  [ "${CFG_WROTE}" = "1" ] || return 0
  if [ "${CFG_EXISTED}" = "1" ]; then
    if [ "${CFG_ORIG_UPD_NULL}" = "t" ]; then
      if ! db_q "UPDATE system_config SET value='${CFG_ORIG}', updated_at=NULL WHERE key='${CFG_KEY}'" >/dev/null; then
        return 1
      fi
    else
      if ! db_q "UPDATE system_config SET value='${CFG_ORIG}', updated_at='${CFG_ORIG_UPD}' WHERE key='${CFG_KEY}'" >/dev/null; then
        return 1
      fi
    fi
  else
    if ! db_q "DELETE FROM system_config WHERE key='${CFG_KEY}'" >/dev/null; then
      return 1
    fi
  fi
  local now
  if ! now=$(db_q "SELECT COALESCE(value,'')||'|'||(CASE WHEN updated_at IS NULL THEN 'NULL' ELSE updated_at END) FROM system_config WHERE key='${CFG_KEY}'"); then
    return 1
  fi
  if [ "${CFG_EXISTED}" = "1" ]; then
    local want="${CFG_ORIG}|"
    if [ "${CFG_ORIG_UPD_NULL}" = "t" ]; then want="${want}NULL"; else want="${want}${CFG_ORIG_UPD}"; fi
    [ "${now}" = "${want}" ] || return 1
  else
    [ -z "${now}" ] || return 1
  fi
  CFG_WROTE=0
  return 0
}
trap 'restore_switch || echo "BAD  D4 临时改档恢复失败：请手工把 ${CFG_KEY} 恢复成 ${CFG_ORIG:-<该行原本不存在>}"' EXIT

if [ "${GATE}" != "on" ] || [ -z "${PAGE_PATH}" ]; then
  skip "D4 闸未全开（gate=${GATE}）或缺靶子 ⇒ 反向对照没有判别力（现在本来就该是空集）"
elif [ -z "${DELTA_INJ}" ]; then
  skip "D4 D2 没拿到读数 ⇒ 反向对照无从比较"
else
  if ! CFG_EXISTED=$(db_q "SELECT count(1) FROM system_config WHERE key='${CFG_KEY}'"); then
    skip "D4 读不到该开关的行数 ⇒ 不敢写（写下去可能留成 0）"
  elif ! is_num "${CFG_EXISTED}" || { [ "${CFG_EXISTED}" != "0" ] && [ "${CFG_EXISTED}" != "1" ]; }; then
    # 主键唯一 ⇒ 正常只会是 0/1；读到别的值说明查询没照预期落（SQL 被改过/连错库）。
    skip "D4 该开关行数读数不是 0/1（读到 '${CFG_EXISTED}'）⇒ 不敢写"
  elif [ "${CFG_EXISTED}" = "1" ]; then
    if ! CFG_ORIG=$(db_q "SELECT value FROM system_config WHERE key='${CFG_KEY}'"); then
      skip "D4 读不到原值 ⇒ 不敢写"
    elif ! sql_lit_ok "${CFG_ORIG}"; then
      # 含单引号/反斜杠/空格等 ⇒ 回写语句可能被拆开。宁可不写：恢复失败会把库停在"开关=0"。
      skip "D4 原值形状无法安全回写（含引号/反斜杠/空白等）⇒ 不写，避免恢复失败"
    elif ! CFG_ORIG_UPD_NULL=$(db_q "SELECT (updated_at IS NULL)::text FROM system_config WHERE key='${CFG_KEY}'"); then
      skip "D4 读不到原时间戳形态 ⇒ 不敢写"
    elif [ "${CFG_ORIG_UPD_NULL}" != "t" ] && [ "${CFG_ORIG_UPD_NULL}" != "f" ]; then
      skip "D4 原时间戳形态读数不是 t/f（读到 '${CFG_ORIG_UPD_NULL}'）⇒ 不敢写"
    else
      if [ "${CFG_ORIG_UPD_NULL}" = "t" ]; then CFG_ORIG_UPD=""; else
        CFG_ORIG_UPD=$(db_q "SELECT updated_at FROM system_config WHERE key='${CFG_KEY}'") || CFG_ORIG_UPD=""
      fi
      if [ "${CFG_ORIG_UPD_NULL}" = "f" ] && ! sql_lit_ok "${CFG_ORIG_UPD}"; then
        skip "D4 原时间戳形状无法安全回写（含引号/空白等）⇒ 不写，避免恢复失败"
      else
        D4_WRITE=1
      fi
    fi
  else
    D4_WRITE=1
  fi

  if [ "${D4_WRITE:-0}" = "1" ]; then
    note "D4 即将临时把 ${CFG_KEY} 置 0（跑完成对恢复；这是本脚本唯一的写动作）"
    if ! db_q "INSERT INTO system_config (key, value, updated_at) VALUES ('${CFG_KEY}', '0', to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'))
      ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at" >/dev/null; then
      bad "D4 没能把 ${CFG_KEY} 临时置 0（写库失败：${DB_ERR}）⇒ 反向对照没跑；库里值未变，继续"
    else
      CFG_WROTE=1
      NEW_GATE=""
      tries=0
      while [ "${tries}" -lt "${CONFIG_TTL_WAIT}" ]; do
        NEW_GATE=$(health_gate)
        [ "${NEW_GATE}" = "half" ] && break
        tries=$((tries + 1)); sleep 1
      done
      if [ "${NEW_GATE}" != "half" ]; then
        bad "D4 置 0 后 ${CONFIG_TTL_WAIT}s 内 health 的 gate 仍是 ${NEW_GATE:-NOT_SET}（期望 half）⇒ 进程没有按库内配置翻档"
      else
        health_snapshot
        if ! is_num "${SNAP_INJ}" || ! is_num "${SNAP_ANON}"; then
          skip "D4 事后 health 缺计数 ⇒ 反向对照没有读数"
        else
          face_curl POST "${BASE_URL}/assist-api/api/assist/chat" \
            -H 'Content-Type: application/json' -H "X-App-Lang: ${PROBE_LANG}" -H "X-LangCross-Page: ${PAGE_PATH}" \
            -d "$(chat_body '再介绍一下这个产品。')"
          if [ "${FACE_RC}" != "0" ] || [ "${FACE_CODE}" != "200" ]; then
            skip "D4 反向对照这一跳没走完（rc=${FACE_RC} code=${FACE_CODE:-NOT_SET}）"
          else
            face_curl GET "${BASE_URL}/api/health"
            A_INJ=$(jget_path "${FACE_TEXT}" sales_facts injected 2>/dev/null) || A_INJ=""
            A_ANON=$(jget_path "${FACE_TEXT}" sales_facts scope_anon 2>/dev/null) || A_ANON=""
            if ! is_num "${A_INJ}" || ! is_num "${A_ANON}"; then
              skip "D4 事后 health 缺计数 ⇒ 反向对照没有读数"
            else
              D_INJ=$((A_INJ - SNAP_INJ))
              D_ANON=$((A_ANON - SNAP_ANON))
              if [ "${D_INJ}" = "0" ] && [ "${D_ANON}" = "0" ]; then
                ok "D4 关一把开关后 injected/scope_anon 增量都是 0 ⇒ 事实集回空是**开关**在起作用，不是恒空"
              else
                bad "D4 关一把开关后 injected 增量=${D_INJ}、scope_anon 增量=${D_ANON}（都期望 0）⇒ 半开档没有短路；灰度开关失效"
              fi
            fi
          fi
        fi
      fi
      if restore_switch; then
        ok "D4 临时改档已恢复原值（${CFG_KEY}=${CFG_ORIG:-<删除该行>}）"
        BACK_GATE=""
        tries=0
        while [ "${tries}" -lt "${CONFIG_TTL_WAIT}" ]; do
          BACK_GATE=$(health_gate)
          [ "${BACK_GATE}" = "${GATE}" ] && break
          tries=$((tries + 1)); sleep 1
        done
        if [ "${BACK_GATE}" = "${GATE}" ]; then
          ok "D4 恢复后 gate 回到 ${GATE}（进程已按库内原值重新生效）"
        else
          bad "D4 恢复后 ${CONFIG_TTL_WAIT}s 内 gate 仍是 ${BACK_GATE:-NOT_SET}（期望 ${GATE}）⇒ 库已恢复但进程没跟上，请核查配置缓存与实例"
        fi
      else
        bad "D4 恢复 ${CFG_KEY} 失败 ⇒ **立刻手工恢复**（否则事实注入被静默关掉，而屏幕上这次探针像个成功）"
      fi
    fi
  fi
fi

# ------------------------------------------------------------------ D5 归因行带产品维度
if [ -z "${PAGE_PATH}" ] || ! is_num "${MID}" || [ "${MID}" = "0" ] || [ -z "${SID}" ]; then
  skip "D5 缺靶子/message_id/会话 id（D2 没走完）⇒ 归因维度没有读数"
else
  ROW=""
  tries=0
  while [ "${tries}" -lt 10 ]; do
    ROW=$(db_q "SELECT tenant_id||'|'||COALESCE(product_id,'')||'|'||conversation_id
      FROM reply_attributions WHERE message_id=${MID} ORDER BY id DESC LIMIT 1") || ROW=""
    [ -n "${ROW}" ] && break
    tries=$((tries + 1)); sleep 1
  done
  if [ -z "${ROW}" ]; then
    skip "D5 10s 内 reply_attributions 里没有 message_id=${MID} 的行 ⇒ 回写可能没走（也可能 assist 没回吐 attribution 段）"
  else
    R_TID=${ROW%%|*}
    rest=${ROW#*|}
    R_PID=${rest%%|*}
    R_CID=${rest#*|}
    if [ "${R_TID}" != "${PTID}" ]; then
      bad "D5 归因行 tenant_id=${R_TID}，产品租户=${PTID} ⇒ 匿名档写错了租户（串租户比不写更坏）"
    elif [ "${R_PID}" != "${PID}" ]; then
      bad "D5 归因行 product_id=${R_PID:-<空>}，产品主键=${PID} ⇒ 产品维度没落库（F10 那一行）；W1 之后这条询盘归不了因"
    elif [ "${R_CID}" != "0" ]; then
      bad "D5 归因行 conversation_id=${R_CID}（期望 0）⇒ 匿名会话不该有会话映射"
    else
      ok "D5 归因行 tenant_id=${R_TID}、product_id=${R_PID}、conversation_id=0 ⇒ 产品维度成立"
    fi
    HOFF=$(db_q "SELECT count(1) FROM handoff_sessions WHERE tenant_id=${PTID} AND session_id='${SID}'") || HOFF=""
    if ! is_num "${HOFF}"; then
      skip "D5 读不到 handoff_sessions ⇒ 孤儿行判据没测到"
    elif [ "${HOFF}" = "0" ]; then
      ok "D5 该匿名会话在 handoff_sessions 里 0 行（匿名不建孤儿会话，F11③ 成立）"
    else
      bad "D5 该匿名会话在 handoff_sessions 里有 ${HOFF} 行 ⇒ 匿名档不该建会话映射（谁来接管？）"
    fi
  fi
fi

finish
exit $?
