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
# 四个探测都要 200 + JSON 带 items；拿到空结果/503/解析失败一律判红，不许静默降级成「通过」。
#
# 判绿只认末行 `KNOWLEDGE_FACE_ALL_OK=1`。
# 用法（部署机上跑）：
#   KNOWLEDGE_TOKEN=xxx bash deploy/check_knowledge_face.sh
#   可覆盖：KNOWLEDGE_BASE_URL / EXPECT_CONTRACT_VERSION / CURL_MAX_TIME
# 退出码：0＝全绿；1＝有红项（含三态非 ok）。
# ============================================================================
set -uo pipefail

BASE_URL=${KNOWLEDGE_BASE_URL:-http://127.0.0.1:8791}
TOKEN=${KNOWLEDGE_TOKEN:-}
EXPECT_CV=${EXPECT_CONTRACT_VERSION:-knowledge-contract-v1}
CURL_MAX_TIME=${CURL_MAX_TIME:-10}
FAIL=0

say_bad() { echo "BAD $*"; FAIL=1; }

# ---- ① 服务活性（三态）----
health=$(curl -sS -m "$CURL_MAX_TIME" "$BASE_URL/internal/health" 2>/dev/null) || health=""
if [ -z "$health" ]; then
  echo "STATE=unavailable（/internal/health 连不上）"
  echo "KNOWLEDGE_FACE_ALL_OK=0"
  exit 1
fi
cv=$(printf '%s' "$health" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("contract_version",""))' 2>/dev/null) || cv=""
if [ "$cv" != "$EXPECT_CV" ]; then
  echo "STATE=degraded（契约版本不匹配: got=$cv want=$EXPECT_CV）"
  echo "KNOWLEDGE_FACE_ALL_OK=0"
  exit 1
fi
echo "STATE=ok（health + contract_version 一致）"

if [ -z "$TOKEN" ]; then
  say_bad "KNOWLEDGE_TOKEN 未配置，三个消费方真件无法打"
  echo "KNOWLEDGE_FACE_ALL_OK=0"
  exit 1
fi

# probe：POST 契约端点，必须 200 且 JSON 带 items（用 python3 完整解析，失败即红）。
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
    say_bad "$label HTTP=$code（契约端点必须 200，不许静默空/假装）"
    return
  fi
  if ! printf '%s' "$payload" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert "items" in d, "missing items"' 2>/dev/null; then
    say_bad "$label 响应缺 items（疑似静默空结果）"
    return
  fi
  echo "OK  $label 真件 200+items"
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
