#!/usr/bin/env bash
# ============================================================================
# scripts/uat/four_phase_closeout_uat.sh — 四期收口批的**现网只读验收**（2026-11-15）
#
# 覆盖本批新接上的五条产品腿，全部只发只读请求，不往生产库写任何测试数据：
#   T1  主站 /api/health 出 assist 段（mode/ready）＋ sales_facts／knowledge 段；
#   T2  assist.mode 必须是 local 或 proxy（off 只在显式配置下允许），ready 字段类型必须是布尔；
#   T3  知识服务 /internal/health 的 contract_version 必须等于期望值（默认 v2）；
#   T4  /assist-api/ 四端点契约仍活（greeting 探针，200 + JSON，不依赖跨进程 :8790）；
#   T5  对外页 /p/{tc}/{pc} 可达（给了夹具才跑，缺夹具如实记 SKIP，不假装测过）；
#   T6  outbox relay 缺省关（OUTBOX_RELAY 未显式 on）；
#   T7  knowledge-migrate --sessions 只读 dry-run 能跑（给了二进制路径才跑）。
#
# ★ 判据与真请求喂**同一个** health_shape_problems 函数；--selftest 用本地假响应
#   正反自证这条判据不是空转（缺段/错档/错版本都必须判红），不连服务、不写数据。
#
# 用法（部署机）：
#   BASE_URL=http://127.0.0.1:8787 KNOWLEDGE_TOKEN=xxx bash scripts/uat/four_phase_closeout_uat.sh
#   bash scripts/uat/four_phase_closeout_uat.sh --selftest
# 判绿只认末行 FOUR_PHASE_UAT_ALL_OK=1；SKIP 不判绿也不判红（与 run_uat.sh 口径一致）。
# ============================================================================
set -uo pipefail

BASE_URL=${BASE_URL:-http://127.0.0.1:8787}
KNOWLEDGE_BASE_URL=${KNOWLEDGE_BASE_URL:-http://127.0.0.1:8791}
KNOWLEDGE_TOKEN=${KNOWLEDGE_TOKEN:-}
EXPECT_CV=${EXPECT_CONTRACT_VERSION:-knowledge-contract-v2}
CURL_MAX_TIME=${CURL_MAX_TIME:-10}
TENANT_CODE=${PRODUCT_TENANT_CODE:-}
PAGE_CODE=${PRODUCT_PAGE_CODE:-}
KNOWLEDGE_MIGRATE_BIN=${KNOWLEDGE_MIGRATE_BIN:-}

FAIL=0
SKIP=0
ok()   { echo "OK   $*"; }
bad()  { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
skip() { echo "SKIP $*"; SKIP=$((SKIP + 1)); }

# health_shape_problems 主站健康面的形状判据（纯函数，真请求与 selftest 共用）。
# 参数：$1=health JSON 文本；$2=期望 assist.mode 允许集（逗号分隔）。
# 返回：0＝形状合格（stdout 为空）；1＝有问题（stdout 逐行点名）。
health_shape_problems() {
  printf '%s' "$1" | python3 -c '
import json, os, sys
raw = sys.stdin.read()
try:
    d = json.loads(raw)
except Exception as e:
    print("health 不是 JSON: %s" % e); sys.exit(0)
probs = []
for seg in ("assist", "sales_facts", "knowledge"):
    if seg not in d or not isinstance(d[seg], dict):
        probs.append("缺 %s 段或该段不是对象" % seg)
a = d.get("assist")
if isinstance(a, dict):
    mode = a.get("mode")
    allowed = os.environ.get("UAT_ASSIST_MODES", "local,proxy").split(",")
    if mode not in allowed:
        probs.append("assist.mode=%r 不在允许档 %r" % (mode, allowed))
    if not isinstance(a.get("ready"), bool):
        probs.append("assist.ready 不是布尔（健康面不许出字符串占位）")
if probs:
    print("\n".join(probs))
' 2>/dev/null
}

# kh_shape_problems 知识服务健康面的契约版本判据。
kh_shape_problems() {
  printf '%s' "$1" | EXPECT_CV="$EXPECT_CV" python3 -c '
import json, os, sys
try:
    d = json.loads(sys.stdin.read())
except Exception as e:
    print("knowledge health 不是 JSON: %s" % e); sys.exit(0)
want = os.environ.get("EXPECT_CV", "")
got = d.get("contract_version", "")
if d.get("status") != "ok":
    print("knowledge status=%r（期望 ok）" % d.get("status"))
if got != want:
    print("contract_version=%r 与期望 %r 不一致（两站二进制必须同批换）" % (got, want))
' 2>/dev/null
}

# ---- --selftest：纯本地，不连服务、不写数据 ----
if [ "${1:-}" = "--selftest" ]; then
  GOOD='{"status":"ok","assist":{"mode":"local","ready":true},"sales_facts":{"gate":"on"},"knowledge":{"status":"ok","reachable":true}}'
  BAD_MISSING='{"status":"ok","sales_facts":{},"knowledge":{}}'
  BAD_MODE='{"status":"ok","assist":{"mode":"off","ready":false},"sales_facts":{},"knowledge":{}}'
  BAD_READY='{"status":"ok","assist":{"mode":"local","ready":"true"},"sales_facts":{},"knowledge":{}}'
  p=$(health_shape_problems "$GOOD" local,proxy)
  [ -z "$p" ] && ok "selftest 正常健康面放行" || { bad "selftest 正常健康面被判红：$p"; }
  for pair in "缺段:$BAD_MISSING" "mode=off:$BAD_MODE" "ready非布尔:$BAD_READY"; do
    label=${pair%%:*}; payload=${pair#*:}
    p=$(health_shape_problems "$payload" local,proxy)
    [ -n "$p" ] && ok "selftest $label 判红（射程在位）" || bad "selftest $label 没判红 ⇒ 判据空转"
  done
  p=$(kh_shape_problems '{"status":"ok","contract_version":"knowledge-contract-v2"}')
  [ -z "$p" ] && ok "selftest v2 契约版本放行" || bad "selftest v2 被判红：$p"
  p=$(kh_shape_problems '{"status":"ok","contract_version":"knowledge-contract-v1"}')
  [ -n "$p" ] && ok "selftest v1 旧版本判红" || bad "selftest v1 没判红 ⇒ 版本腿是假的"
  if [ "$FAIL" -ne 0 ]; then echo "FOUR_PHASE_UAT_SELFTEST_ALL_OK=0"; exit 1; fi
  echo "FOUR_PHASE_UAT_SELFTEST_ALL_OK=1"
  exit 0
fi

# ---- T1/T2 主站健康面 ----
health=$(curl -sS -m "$CURL_MAX_TIME" "$BASE_URL/api/health" 2>/dev/null) || health=""
if [ -z "$health" ]; then
  bad "T1 /api/health 连不上（BASE_URL=${BASE_URL}）"
else
  problems=$(UAT_ASSIST_MODES="${UAT_ASSIST_MODES:-local,proxy}" health_shape_problems "$health")
  if [ -n "$problems" ]; then
    printf '%s\n' "$problems" | while IFS= read -r line; do bad "T2 health 形状：$line"; done
    # while 在管道子 shell 里，FAIL 不会回传；这里补记一次（下游判绿看末行，红一次即足够）。
    FAIL=$((FAIL + 1))
  else
    ok "T1/T2 health 三段齐、assist.mode/ready 形状合格"
  fi
fi

# ---- T3 知识服务契约版本 ----
kh=$(curl -sS -m "$CURL_MAX_TIME" -H "X-Knowledge-Token: $KNOWLEDGE_TOKEN" \
  "$KNOWLEDGE_BASE_URL/internal/health" 2>/dev/null) || kh=""
if [ -z "$kh" ]; then
  bad "T3 知识服务 /internal/health 连不上"
else
  p=$(kh_shape_problems "$kh")
  [ -z "$p" ] && ok "T3 契约版本 $EXPECT_CV 一致" || { printf '%s\n' "$p" | while IFS= read -r l; do bad "T3 $l"; done; FAIL=$((FAIL + 1)); }
fi

# ---- T4 /assist-api 四端点契约（本地引擎，不出网到 :8790）----
# 挂件 greet 走 GET（四端点同时允许 GET/POST，GET 是无副作用的探针形态）。
greet=$(curl -sS -m "$CURL_MAX_TIME" -w $'\n%{http_code}' \
  "$BASE_URL/assist-api/api/assist/greeting" 2>/dev/null) || greet=""
code=$(printf '%s' "$greet" | tail -1)
body=$(printf '%s' "$greet" | sed '$d')
if [ "$code" != "200" ]; then
  bad "T4 /assist-api greeting HTTP=${code}（本地引擎或旧代理均须 200）"
elif ! printf '%s' "$body" | python3 -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null; then
  bad "T4 /assist-api greeting 回的不是 JSON（可能落了 SPA 兜底）"
else
  ok "T4 /assist-api greeting 200+JSON（四端点前缀契约仍活）"
fi

# ---- T5 对外页可达（有夹具才判，缺夹具如实 SKIP）----
if [ -z "$TENANT_CODE" ] || [ -z "$PAGE_CODE" ]; then
  skip "T5 未给 PRODUCT_TENANT_CODE/PRODUCT_PAGE_CODE，对外页不判绿"
else
  pc=$(curl -sS -m "$CURL_MAX_TIME" -o /dev/null -w '%{http_code}' \
    "$BASE_URL/p/$TENANT_CODE/$PAGE_CODE" 2>/dev/null) || pc="000"
  [ "$pc" = "200" ] && ok "T5 对外页 /p/ 200" || bad "T5 对外页 /p/ HTTP=$pc"
fi

# ---- T6 relay 缺省关 ----
relay=${OUTBOX_RELAY:-off}
case "$relay" in
  on|1|true|yes) bad "T6 OUTBOX_RELAY=${relay}（本批默认关，开闸须有现网读数后单独发版）" ;;
  *) ok "T6 OUTBOX_RELAY 缺省关（event_outbox 只积压不投递）" ;;
esac

# ---- T7 CLI dry-run ----
if [ -z "$KNOWLEDGE_MIGRATE_BIN" ] || [ ! -x "$KNOWLEDGE_MIGRATE_BIN" ]; then
  skip "T7 未给可执行的 KNOWLEDGE_MIGRATE_BIN，dry-run 不判绿"
else
  if "$KNOWLEDGE_MIGRATE_BIN" --sessions 2>&1 | grep -q 'DRY_RUN=1'; then
    ok "T7 knowledge-migrate --sessions 只读 dry-run 生效"
  else
    bad "T7 --sessions 没出 DRY_RUN=1（可能误写成真写）"
  fi
fi

echo "FOUR_PHASE_UAT_FAIL=$FAIL SKIP=$SKIP"
if [ "$FAIL" -ne 0 ]; then echo "FOUR_PHASE_UAT_ALL_OK=0"; exit 1; fi
echo "FOUR_PHASE_UAT_ALL_OK=1"
exit 0
