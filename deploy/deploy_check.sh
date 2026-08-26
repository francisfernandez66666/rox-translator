#!/usr/bin/env bash
# ============================================================================
# deploy/deploy_check.sh — 部署后验收一键检查（2026-08-26 评审整改配套）
# 用法：./deploy/deploy_check.sh <base_url> [ADMIN_TOKEN] [METRICS_TOKEN]
# 退出码 0 = 全部通过；任何一项失败即非 0，便于部署窗口快速定位。
# ============================================================================
set -uo pipefail
BASE="${1:?用法: $0 <base_url> [ADMIN_TOKEN] [METRICS_TOKEN]}"
ADMTOK="${2:-}"
MTRTOK="${3:-}"
# 内网直连地址：默认生产 8787；本地冒烟时自动跟随 base_url 端口
LOCAL_BASE="http://127.0.0.1:8787"
case "$BASE" in
  *127.0.0.1:*|*localhost:*) LOCAL_BASE="$BASE" ;;
esac
PASS=0; FAIL=0
ok()   { echo "  ✔ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✖ $1"; FAIL=$((FAIL+1)); }
check(){ local desc="$1" want="$2" got="$3"; [ "$got" = "$want" ] && ok "$desc ($got)" || bad "$desc 期望$want 实际$got"; }

echo "==> [1/7] 基础探活"
check "/api/health"        200 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$BASE/api/health")"
check "/status"            200 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$BASE/status")"

echo "==> [2/7] D1 metrics 收敛（公网响应体不得出现指标特征；SPA 兜底页/401 均视为安全）"
body=$(curl -s --max-time 8 "$BASE/metrics" | head -c 2000)
if echo "$body" | grep -q "translator_"; then
  bad "公网 /metrics 泄露指标特征"
else
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$BASE/metrics")
  ok "公网 /metrics 无指标泄露 (http=$code 兜底页或401)"
fi
if [ -n "$MTRTOK" ]; then
  check "/metrics(内网+token)" 200 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 -H "Authorization: Bearer $MTRTOK" "$LOCAL_BASE/metrics")"
fi

echo "==> [3/7] A2 插件 CORS（Origin 反射）"
hdr=$(curl -s -o /dev/null -D - --max-time 8 -H "Origin: https://example.com" "$BASE/openapi/v1/balance" | grep -i "^access-control-allow-origin:" | tr -d '\r' | awk '{print $2}')
[ "$hdr" = "https://example.com" ] && ok "ACAO 反射生效" || bad "ACAO 未反射（got: ${hdr:-空}）"

echo "==> [4/7] P0-2 支付回调三道闸"
pncode=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 -X POST "$BASE/api/pay/notify/mock" -d '{"order_no":"x","amount":1}')
case "$pncode" in
  403) ok "匿名回调 403（直连口径）" ;;
  400) ok "400=订单不存在（生产拓扑：Caddy 已注入凭证，渠道/金额闸门生效）" ;;
  *)   bad "匿名回调 $pncode 异常" ;;
esac

echo "==> [5/7] 注册→双桶余额→OpenAPI（A1 核心口径）"
EV=$(curl -s --max-time 8 "$BASE/api/auth/register-config" | python3 -c "import sys,json;print(json.load(sys.stdin).get('email_verify_enabled',False))" 2>/dev/null)
if [ "$EV" = "True" ] || [ "$EV" = "true" ]; then
  echo "  ↳ 生产已启用注册邮箱验证（防薅生效），第5项改为仅验证双桶出参通道开放性"
  bal=$(curl -s --max-time 8 "$BASE/openapi/v1/balance" -H "Authorization: Bearer invalid-probe")
  echo "$bal" | grep -q "invalid_api_key" && ok "balance 端点鉴权正常（跳过注册实测）" || bad "balance 端点异常"
  SKIP_REGISTER=1
fi
if [ "${SKIP_REGISTER:-0}" != "1" ]; then
IND=$(curl -s --max-time 8 "$BASE/api/register/industries" | python3 -c "import sys,json;print(json.load(sys.stdin)['industries'][0]['code'])" 2>/dev/null)
REG=$(curl -s --max-time 15 -X POST "$BASE/api/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"chk$(date +%s)\",\"password\":\"chk123456\",\"code\":\"chk$(date +%s)\",\"email\":\"chk$(date +%s)@t.com\",\"industry\":\"$IND\",\"role_choice\":\"admin\"}")
KEY=$(echo "$REG" | python3 -c "import sys,json;print(json.load(sys.stdin).get('api_key',''))" 2>/dev/null)
if [ -n "$KEY" ]; then
  bal=$(curl -s "$BASE/openapi/v1/balance" -H "Authorization: Bearer $KEY")
  total=$(echo "$bal" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('balance_tokens',-1))" 2>/dev/null)
  grants=$(echo "$bal" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('sub_grants_left',-1))" 2>/dev/null)
  perm=$(echo "$bal" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('permanent_balance',-1))" 2>/dev/null)
  [ "${total:-0}" -gt 0 ] && [ "${grants:--1}" -ge 0 ] && [ "${perm:--1}" -ge 0 ] \
    && ok "双桶出参 total=$total grants=$grants perm=$perm" || bad "双桶出参异常: $bal"
else
  bad "注册未返回 api_key"
fi
fi

echo "==> [6/7] 自助注销端点存在性（匿名 401 即可）"
check "/api/me/deactivate 匿名" 401 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 -X POST "$BASE/api/me/deactivate")"

echo "==> [7/7] 同步划译端点存在性（匿名 401 即可）"
check "/openapi/v1/translate 匿名" 401 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 -X POST "$BASE/openapi/v1/translate" -d '{}')"

echo ""
[ "$FAIL" = "0" ] && echo "✅ 验收全部通过（$PASS 项）" || { echo "❌ 通过 $PASS 项 / 失败 $FAIL 项"; exit 1; }