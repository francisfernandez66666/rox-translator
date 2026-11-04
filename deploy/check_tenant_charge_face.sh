#!/usr/bin/env bash
# ============================================================================
# deploy/check_tenant_charge_face.sh —— 租户充值到账的**现网只读面检**（★ 2026-11-04 体验批 §4.5）
#
# 它回答的问题只有一个：**超管代客置付说「成功」之后，钱真的进账、库侧真有那一行、
# 新路径真留了痕吗**（AGENTS §一·8：接口说成功不算验收，库侧事实＋留痕才算）。
# 为什么不能只看仓库：仓库侧单测判的是「出参带 balance_points/order_points」，而现网
# Caddy 指向哪一版二进制、库里那行 quota_grants/orders 在不在、order_pay 留痕在不在，
# 任何仓库判据都看不见；坏掉的形状是静默的——toast 说充好了、租户余额一动不动。
#
# 判据三件套（全只读：不建单、不写库、不造夹具、不重启单元、不碰 caddy）：
#   C1 登录态余额**前后差值等值**：现读 /api/me/package 的 points_balance 当「后」，
#      与 §1 试验记录的「注资前余额」+「本次注入积分」逐字比对；
#   C2 库里那行 `quota_grants` 或 `orders.status='paid'` **真存在**（夹具租户维度现读）；
#   C3 `audit_logs` 里 `order_pay` **留痕在位**（夹具租户维度现读）。
#   ⚠️ 余额为 0 的账号**没有判别力**：C1 必须自己报「未测到」而不是判绿
#      （AGENTS §24 那条 check_assist_profile_face 的同款口径）。
#
# 三态（同仓内其它 face 脚本口径）：exit 0 全绿／1 有红项／**2 有 N 项没测到**（⏳）。
# 拿到 2 既不是通过也不是产品缺陷：读数链路抖动或夹具读数没给全，复跑一次再判。
# 判绿只认末行 `TENANT_CHARGE_FACE_ALL_OK=1`。
#
# 只读与凭据纪律：
#   · 库侧读数一律 `sudo -u postgres psql -d "$DB" -Atc "SELECT …"`（零 INSERT/UPDATE/DELETE，
#     peer 认证不带口令、不硬编码 DSN；沿用 deploy/release_20261016b_apikey_scope.sh:97 的口径）；
#   · 登录态凭据只经 env `PROBE_USER/PROBE_PASS`，不落文件、不打印 token；
#   · 夹具（一次性虚拟租户/订单）由 §1 受控试验另做，本脚本**不造夹具**，只按
#     FACE_TENANT_ID 读现成的行；FACE_TENANT_ID 校验纯数字后才拼进 SQL（防注入）。
#
# 跨机读数形态（AGENTS §25-c 硬约定）：库侧多点读数只有 `ssh 'bash -s'` ＋ **单引号 heredoc**
# 这一种形态，脚本正文不与引号打架；**未命中档一律写 ASCII 哨兵 NOT_SET**（禁止裸 `<…>`）。
# 注意 bash -n 对单引号 heredoc 内的远端正文退 0，远端脚本须按文本核对（同 §25-c）。
#
# 用法（凭据/夹具读数全部经 env，本文件与任何文档都不写死账号密码/数字）：
#   PROBE_USER=<探针账号> PROBE_PASS=<口令> \
#   FACE_TENANT_ID=<§1 夹具租户号> FACE_BALANCE_BEFORE=<§1 注资前余额读数> \
#   FACE_CREDIT_POINTS=<§1 本次注入积分读数> \
#   FACE_SSH_HOST=root@<部署机或库机> FACE_SSH_PORT=22 FACE_DBNAME=langcross \
#   bash deploy/check_tenant_charge_face.sh
#   可选：MAIN_BASE（默认 http://127.0.0.1:8787）／CURL_MAX_TIME（默认 20s）
# ============================================================================
set -uo pipefail

MAIN_BASE=${MAIN_BASE:-http://127.0.0.1:8787}
PROBE_USER=${PROBE_USER:-}
PROBE_PASS=${PROBE_PASS:-}
FACE_TENANT_ID=${FACE_TENANT_ID:-}
FACE_BALANCE_BEFORE=${FACE_BALANCE_BEFORE:-}
FACE_CREDIT_POINTS=${FACE_CREDIT_POINTS:-}
FACE_SSH_HOST=${FACE_SSH_HOST:-}
FACE_SSH_PORT=${FACE_SSH_PORT:-22}
FACE_DBNAME=${FACE_DBNAME:-langcross}
CURL_MAX_TIME=${CURL_MAX_TIME:-20}

FAIL=0
UNMEASURED=0
ok()   { echo "OK   $*"; }
bad()  { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
skip() { echo "⏳   $*"; UNMEASURED=$((UNMEASURED + 1)); }

# ------------------------------------------------------------------ P0 前置
# 凭据/夹具号缺失 ⇒ 停在最前面说明「为什么没跑」，而不是拿一条匿名请求把后面判成红。
if [ -z "${PROBE_USER}" ] || [ -z "${PROBE_PASS}" ]; then
  echo "P0 需要探针账号：PROBE_USER/PROBE_PASS 经 env 传入（本脚本不落凭据、不读配置文件、不打印 token）"
  echo "TENANT_CHARGE_FACE_UNMEASURED=1"
  exit 2
fi
if [ -z "${FACE_TENANT_ID}" ] || ! [[ "${FACE_TENANT_ID}" =~ ^[0-9]+$ ]]; then
  echo "P0 需要夹具租户号 FACE_TENANT_ID（纯数字，§1 试验给出；脚本不造夹具、不把脏参拼进 SQL）"
  echo "TENANT_CHARGE_FACE_UNMEASURED=1"
  exit 2
fi
for c in curl python3; do
  if ! command -v "${c}" >/dev/null 2>&1; then
    echo "P0 本机缺 ${c} ⇒ 一条都测不到"
    echo "TENANT_CHARGE_FACE_UNMEASURED=1"
    exit 2
  fi
done

# jget <json> <key>：取一个顶层字段（键不存在退 3、整体解析失败退 2）。
# 判「取不到」必须比判「取到空值」更严：解析失败常被读成「这个字段没配」（同族假红见 AGENTS §一·5 ⑤）。
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

# ------------------------------------------------------------------ 登录态余额（本地单点读数）
# 读不到一律按「没测到」交人复跑，不重拨摇绿（4xx/5xx/半截体都不许被重拨洗成通过）。
LOGIN_BODY=$(python3 -c 'import json,sys;print(json.dumps({"username":sys.argv[1],"password":sys.argv[2]}))' \
  "${PROBE_USER}" "${PROBE_PASS}")
LOGIN_JSON=$(curl -sS --max-time "${CURL_MAX_TIME}" -X POST "${MAIN_BASE}/api/auth/login" \
  -H 'Content-Type: application/json' -d "${LOGIN_BODY}" 2>/dev/null || echo NOT_SET)
TOKEN=NOT_SET
if [ "${LOGIN_JSON}" != "NOT_SET" ] && [ -n "${LOGIN_JSON}" ]; then
  TOKEN=$(jget "${LOGIN_JSON}" token 2>/dev/null || echo NOT_SET)
fi
BALANCE_NOW=NOT_SET
if [ "${TOKEN}" != "NOT_SET" ] && [ -n "${TOKEN}" ]; then
  PKG_JSON=$(curl -sS --max-time "${CURL_MAX_TIME}" "${MAIN_BASE}/api/me/package" \
    -H "Authorization: Bearer ${TOKEN}" 2>/dev/null || echo NOT_SET)
  if [ "${PKG_JSON}" != "NOT_SET" ] && [ -n "${PKG_JSON}" ]; then
    BALANCE_NOW=$(jget "${PKG_JSON}" points_balance 2>/dev/null || echo NOT_SET)
  fi
fi

# ------------------------------------------------------------------ 库侧多点读数（唯一跨机形态）
# 三件套的库侧两条各读多个点，一次 ssh 'bash -s' + 单引号 heredoc 收齐；
# 远端只读 SELECT，未命中档写 ASCII 哨兵 NOT_SET（禁止裸 <…> 被 bash 当重定向）。
remote_readings() {
  ssh -p "${FACE_SSH_PORT}" "${FACE_SSH_HOST}" 'bash -s' -- "${FACE_TENANT_ID}" "${FACE_DBNAME}" 2>&1 <<'REMOTE_READINGS'
set -uo pipefail
TID="$1"; DB="$2"
# 库侧只读：函数封装保证带空格的 SQL 作为**一个**参数传给 psql（变量展开不会重解析引号）
psql_ro() { sudo -u postgres psql -d "$DB" -Atc "$1" 2>/dev/null; }
GRANT=$(psql_ro "SELECT count(*)||'|'||COALESCE(SUM(total),0) FROM quota_grants WHERE tenant_id=$TID" || echo NOT_SET)
ORDER_PAID=$(psql_ro "SELECT count(*) FROM orders WHERE tenant_id=$TID AND status='paid'" || echo NOT_SET)
AUDIT_PAY=$(psql_ro "SELECT count(*) FROM audit_logs WHERE tenant_id=$TID AND action='order_pay'" || echo NOT_SET)
printf 'GRANT=%s\n' "${GRANT:-NOT_SET}"
printf 'ORDER_PAID=%s\n' "${ORDER_PAID:-NOT_SET}"
printf 'AUDIT_PAY=%s\n' "${AUDIT_PAY:-NOT_SET}"
REMOTE_READINGS
}

GRANT=NOT_SET
ORDER_PAID=NOT_SET
AUDIT_PAY=NOT_SET
if [ -z "${FACE_SSH_HOST}" ] || ! command -v ssh >/dev/null 2>&1; then
  echo "P0 库侧读数需 FACE_SSH_HOST 且本机有 ssh（跨机多点读数只有 ssh 'bash -s' 一种形态）⇒ 库侧两腿未测到"
else
  REMOTE_OUT=$(remote_readings) || true
  while IFS='=' read -r k v; do
    case "${k}" in
      GRANT)      GRANT="${v}" ;;
      ORDER_PAID) ORDER_PAID="${v}" ;;
      AUDIT_PAY)  AUDIT_PAY="${v}" ;;
    esac
  done <<< "${REMOTE_OUT}"
fi
GRANT_ROWS=${GRANT%%|*}
GRANT_TOTAL=${GRANT#*|}

# ------------------------------------------------------------------ C1 登录态余额前后差值等值
if [ "${BALANCE_NOW}" = "NOT_SET" ] || [ -z "${BALANCE_NOW}" ] || ! [[ "${BALANCE_NOW}" =~ ^[0-9]+$ ]]; then
  skip "C1 登录态余额读数没取到/不合格（${BALANCE_NOW}）⇒ 没测到，不判产品缺陷（复跑再判）"
elif [ "${BALANCE_NOW}" = "0" ]; then
  # ★ 余额为 0 没有判别力：前后差值与任何数都分不开，判绿等于把「没账」读成「到账对得上」。
  skip "C1 登录态余额为 0 ⇒ 没有判别力，报未测到而不是判绿；请换有余额的探针账号"
elif [ -z "${FACE_BALANCE_BEFORE}" ] || [ -z "${FACE_CREDIT_POINTS}" ] || \
     ! [[ "${FACE_BALANCE_BEFORE}" =~ ^[0-9]+$ ]] || ! [[ "${FACE_CREDIT_POINTS}" =~ ^[0-9]+$ ]]; then
  skip "C1 缺 §1 试验原始读数（FACE_BALANCE_BEFORE / FACE_CREDIT_POINTS，须为纯数字，现读现推不写死）⇒ 未测到"
elif [ "${FACE_CREDIT_POINTS}" = "0" ]; then
  skip "C1 本次注入积分为 0 ⇒ 没有判别力，报未测到而不判绿"
else
  DELTA=$(( BALANCE_NOW - FACE_BALANCE_BEFORE ))
  if [ "${DELTA}" -eq "${FACE_CREDIT_POINTS}" ] 2>/dev/null; then
    ok "C1 登录态余额前后差值等值（${FACE_BALANCE_BEFORE} → ${BALANCE_NOW}，差值=${DELTA}＝本次注入积分 ${FACE_CREDIT_POINTS}）"
  else
    bad "C1 登录态余额前后差值不等值（${FACE_BALANCE_BEFORE} → ${BALANCE_NOW}，实得差值=${DELTA}，期望=${FACE_CREDIT_POINTS}）⇒ 钱没按这一单到账"
  fi
fi

# ------------------------------------------------------------------ C2 库里那行真存在
if [ "${GRANT}" = "NOT_SET" ] && [ "${ORDER_PAID}" = "NOT_SET" ]; then
  skip "C2 库侧两条读数都没取到 ⇒ 未测到，不判缺陷"
elif { [ "${GRANT_ROWS}" != "NOT_SET" ] && [ "${GRANT_ROWS}" -gt 0 ] 2>/dev/null; } || \
     { [ "${ORDER_PAID}" != "NOT_SET" ] && [ "${ORDER_PAID}" -gt 0 ] 2>/dev/null; }; then
  ok "C2 库侧那行在位（quota_grants 行数=${GRANT_ROWS} 合计 token=${GRANT_TOTAL}；orders.status='paid' 行数=${ORDER_PAID}）"
else
  bad "C2 库里既没有 quota_grants 行、也没有 orders.status='paid' 行 ⇒ 接口说成功但库侧事实不在位"
fi

# ------------------------------------------------------------------ C3 audit_logs 留痕在位
if [ "${AUDIT_PAY}" = "NOT_SET" ]; then
  skip "C3 audit_logs 读数没取到 ⇒ 未测到，不判缺陷"
elif [ "${AUDIT_PAY}" -gt 0 ] 2>/dev/null; then
  ok "C3 audit_logs 里 order_pay 留痕在位（${AUDIT_PAY} 行）"
else
  bad "C3 audit_logs 里没有 order_pay 留痕 ⇒ 新路径没留痕（接口说成功不算验收）"
fi

# ------------------------------------------------------------------ 三态收口
echo "TENANT_CHARGE_FACE_FAIL=${FAIL} UNMEASURED=${UNMEASURED}"
if [ "${FAIL}" -gt 0 ]; then echo "TENANT_CHARGE_FACE_ALL_OK=0"; exit 1; fi
if [ "${UNMEASURED}" -gt 0 ]; then echo "TENANT_CHARGE_FACE_UNMEASURED=1"; exit 2; fi
echo "TENANT_CHARGE_FACE_ALL_OK=1"
exit 0
