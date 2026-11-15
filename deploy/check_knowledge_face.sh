#!/usr/bin/env bash
# ============================================================================
# deploy/check_knowledge_face.sh — 四期 W3 知识服务**只读面检**（施工图 §6.3 / §8.2）
#
# 服务活性 + 三个消费方各跑一次真件（不是只看 200，AGENTS §一·12「产物能打开不算验收」）：
#   ① /internal/health            ⇒ ok + contract_version（版本不匹配＝degraded）
#   ② i18n/list                   ⇒ translation 域（事实译文现状）
#   ③ corpus/public               ⇒ 公开页 / SSR
#   ④ facts/admitted              ⇒ 销售接待事实集
# 三态不同形：unavailable（连不上）／degraded（连得上但契约版本不匹配）／ok（全绿）。
#
# ★ 2026-11-15 收口（审计 §4.7）：旧形态是**形状探针**——只断言响应里有 `items` 键，
#   空数组照样判绿，证明不了「真件读得出来」。现在三个探针都要求 items 是**非空数组**；
#   部署机若给了 EXPECT_ITEM_KEY（一条已知真件的键/值），还必须能在 items 里命中它。
#   ⚠️ 刻意**不往生产知识库写夹具**：写测试数据会把「验收」变成「改数据」，
#   清理不干净就是脏数据（三期收尾批踩过）。真件由运维以 EXPECT_ITEM_KEY 指定，
#   `--selftest` 用纯本地载荷自证这条判据有判别力，不需要连服务。
#
# 判绿只认末行 `KNOWLEDGE_FACE_ALL_OK=1`；--selftest 判绿只认 `KNOWLEDGE_FACE_SELFTEST_OK=1`。
# 用法（部署机上跑）：
#   KNOWLEDGE_TOKEN=xxx EXPECT_ITEM_KEY=known-fact-key bash deploy/check_knowledge_face.sh
#   bash deploy/check_knowledge_face.sh --selftest
#   可覆盖：KNOWLEDGE_BASE_URL / EXPECT_CONTRACT_VERSION / EXPECT_ITEM_KEY / CURL_MAX_TIME
# 退出码：0＝全绿；1＝有红项（含三态非 ok）。
# ============================================================================
set -uo pipefail

BASE_URL=${KNOWLEDGE_BASE_URL:-http://127.0.0.1:8791}
TOKEN=${KNOWLEDGE_TOKEN:-}
EXPECT_CV=${EXPECT_CONTRACT_VERSION:-knowledge-contract-v2}
EXPECT_ITEM_KEY=${EXPECT_ITEM_KEY:-}
CURL_MAX_TIME=${CURL_MAX_TIME:-10}
FAIL=0

say_bad() { echo "BAD $*"; FAIL=1; }

# payload_ok 真件判别力：items 必须是非空数组；给了 EXPECT_ITEM_KEY 还必须命中。
# ★ 正反两条都在 --selftest 里喂同一个函数，避免「校验器恒真」这种空转锁。
payload_ok() {
  printf '%s' "$1" | EXPECT_ITEM_KEY="$EXPECT_ITEM_KEY" python3 -c '
import sys, json, os
d = json.load(sys.stdin)
items = d.get("items")
if not isinstance(items, list):
    raise SystemExit("items 不是数组")
if len(items) == 0:
    raise SystemExit("items 为空（形状探针不可判绿）")
key = os.environ.get("EXPECT_ITEM_KEY", "").strip()
if key:
    blob = json.dumps(items, ensure_ascii=False)
    if key not in blob:
        raise SystemExit("真件未命中: " + key)
' 2>/dev/null
}

# ---- --selftest：纯本地，不连服务、不写任何数据 ----
if [ "${1:-}" = "--selftest" ]; then
  echo "STATE=selftest（校验器判别力，不连服务）"
  payload_ok '{"items":[]}' && say_bad "selftest：空数组被判绿 ⇒ 校验器失明"
  payload_ok '{"items":{}}' && say_bad "selftest：items 是对象被判绿 ⇒ 校验器失明"
  payload_ok '{"status":"ok"}' && say_bad "selftest：缺 items 被判绿 ⇒ 校验器失明"
  payload_ok '{"items":[{"key":"fixture-1"}]}' || say_bad "selftest：真件数组被判红 ⇒ 校验器过严"
  EXPECT_ITEM_KEY=fixture-1 payload_ok '{"items":[{"key":"fixture-1"}]}' \
    || say_bad "selftest：EXPECT_ITEM_KEY 命中却被判红"
  EXPECT_ITEM_KEY=missing-key payload_ok '{"items":[{"key":"fixture-1"}]}' \
    && say_bad "selftest：EXPECT_ITEM_KEY 未命中却被判绿 ⇒ 真件腿是假的"
  if [ "$FAIL" -ne 0 ]; then
    echo "KNOWLEDGE_FACE_SELFTEST_OK=0"
    exit 1
  fi
  echo "KNOWLEDGE_FACE_SELFTEST_OK=1"
  exit 0
fi

# ---- ① 服务活性（三态）----
health=$(curl -sS -m "$CURL_MAX_TIME" "$BASE_URL/internal/health" 2>/dev/null) || health=""
if [ -z "$health" ]; then
  echo "STATE=unavailable（/internal/health 连不上）"
  echo "KNOWLEDGE_FACE_ALL_OK=0"
  exit 1
fi
cv=$(printf '%s' "$health" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("contract_version",""))' 2>/dev/null) || cv=""
if [ "$cv" != "$EXPECT_CV" ]; then
  echo "STATE=degraded（契约版本不匹配: got=$cv want=${EXPECT_CV}）"
  echo "KNOWLEDGE_FACE_ALL_OK=0"
  exit 1
fi
echo "STATE=ok（health + contract_version 一致）"

if [ -z "$TOKEN" ]; then
  say_bad "KNOWLEDGE_TOKEN 未配置，三个消费方真件无法打"
  echo "KNOWLEDGE_FACE_ALL_OK=0"
  exit 1
fi

# probe：POST 契约端点，必须 200 且 items 为非空数组（给了真件键还必须命中）。
probe() {
  local path="$1" body="$2" label="$3"
  local out code payload
  out=$(curl -sS -m "$CURL_MAX_TIME" -w $'\n%{http_code}' \
    -H "Content-Type: application/json" \
    -H "X-Knowledge-Token: $TOKEN" \
    -X POST --data "$body" "$BASE_URL$path" 2>/dev/null) || { say_bad "$label curl 失败"; return; }
  code=$(printf '%s' "$out" | tail -1)
  payload=$(printf '%s' "$out" | sed '$d')
  if [ "$code" != "200" ]; then
    say_bad "$label HTTP=${code}（契约端点必须 200，不许静默空/假装）"
    return
  fi
  if ! payload_ok "$payload"; then
    say_bad "$label 响应缺真件（items 空/不是数组/EXPECT_ITEM_KEY 未命中，疑似静默空结果）"
    return
  fi
  echo "OK  $label 真件 200+非空 items"
}

probe /internal/knowledge/i18n/list    '{"tenant_id":1,"fact_id":1}'              "i18n/list(translation 域)"
probe /internal/knowledge/corpus/public '{"tenant_id":1,"product_id":"p1"}'        "corpus/public(公开页/SSR)"
probe /internal/knowledge/facts/admitted '{"tenant_id":1,"product_id":"p1"}'       "facts/admitted(销售接待)"

if [ "$FAIL" -eq 0 ]; then
  echo "KNOWLEDGE_FACE_ALL_OK=1"
  exit 0
fi
echo "KNOWLEDGE_FACE_ALL_OK=0"
exit 1
