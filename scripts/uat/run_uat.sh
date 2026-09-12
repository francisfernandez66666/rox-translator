#!/usr/bin/env bash
# ============================================================================
# scripts/uat/run_uat.sh — 一键自动化 UAT（后端全链路 + 前端像素级 + 前端↔后端联调）
# 流程：
#   1. 构建 Go 后端二进制
#   2. 启动 mock LLM（OpenAI 兼容，回译文/向量）
#   3. 以全新临时库启动后端（固定超管密码 + 关闭 watchdog 自杀探活）
#   4. 放宽注册防刷 + 开启强制计费 + mock 支付（system_config 直写，等价控制台配置）
#   5. 运行后端 API 全链路断言（api_uat.sh）
#   6. 运行前端像素级 UAT（pixel_uat.spec.ts，Playwright 截图）
#   7. 输出汇总与耗时
# 用法：bash scripts/uat/run_uat.sh
#   SQLite 矩阵（默认）：bash scripts/uat/run_uat.sh
#   PostgreSQL 矩阵：    DB_DRIVER=postgres [UAT_PG_DB=translator_uat] [PG_ADMIN_DSN=...] bash scripts/uat/run_uat.sh
# 环境变量：UAT_PORT（默认8899）、MOCK_LLM_PORT（默认8901）、KEEP（=1 不清理环境）
#           DB_DRIVER/DB_DSN/UAT_DB —— 断言层 dblib.sh 据此路由（生产方言必须进矩阵）
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1

UAT_PORT="${UAT_PORT:-8899}"
MOCK_LLM_PORT="${MOCK_LLM_PORT:-8901}"
BASE_URL="http://127.0.0.1:${UAT_PORT}"
WORK=$(mktemp -d)
ADMIN_INIT_PASSWORD=Admin@1234
DB_DRIVER="${DB_DRIVER:-sqlite}"

log(){ echo "[run_uat] $*"; }
T0=$(date +%s)

# ---------- 0. 方言矩阵：PG 时重建专用测试库 ----------
if [ "$DB_DRIVER" = "postgres" ]; then
  UAT_PG_DB="${UAT_PG_DB:-translator_uat}"
  PG_ADMIN="${PG_ADMIN_DSN:-postgres://${USER}@127.0.0.1:5432/postgres?sslmode=disable}"
  log "重建 PG 测试库 $UAT_PG_DB ..."
  psql "$PG_ADMIN" -q -c "DROP DATABASE IF EXISTS $UAT_PG_DB WITH (FORCE)" || true
  psql "$PG_ADMIN" -q -c "CREATE DATABASE $UAT_PG_DB" || { echo "PG 建库失败（检查 PG_ADMIN_DSN）"; exit 1; }
  # pgvector：KB 语义检索依赖（生产库同款前置；扩展是库级对象，须在新库内建）
  psql "postgres://${USER}@127.0.0.1:5432/$UAT_PG_DB?sslmode=disable" -q -c "CREATE EXTENSION IF NOT EXISTS vector" \
    || { echo "pgvector 扩展创建失败（brew install pgvector 或检查 shared_preload_libraries）"; exit 1; }
  export DB_DRIVER
  export DB_DSN="${DB_DSN:-postgres://${USER}@127.0.0.1:5432/$UAT_PG_DB?sslmode=disable}"
fi

# ---------- 1. 构建 ----------
log "构建后端..."
(cd backend-go && go build -o "$WORK/uat-server" ./cmd/server) || { echo "构建失败"; exit 1; }
log "构建前端 dist（若缺失）..."
[ -f frontend-react/dist/index.html ] || (cd frontend-react && npm run build) || { echo "前端构建失败"; exit 1; }

# ---------- 2. mock LLM ----------
log "启动 mock LLM :${MOCK_LLM_PORT}..."
nohup python3 scripts/uat/mock_llm.py "$MOCK_LLM_PORT" > "$WORK/mockllm.log" 2>&1 < /dev/null &
MOCK_PID=$!
OK=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "http://127.0.0.1:${MOCK_LLM_PORT}/v1/chat/completions" -H 'Content-Type: application/json' -d '{"messages":[{"content":"hi"}]}' >/dev/null 2>&1; then OK=1; break; fi
done
[ "${OK:-0}" = "1" ] || { echo "mock LLM 启动失败"; exit 1; }
log "mock LLM 就绪（${i}s）"

# ---------- 3. 后端（全新库 + 探活自指向 + 固定超管密码；方言随 DB_DRIVER） ----------
log "启动后端 :${UAT_PORT}（方言 ${DB_DRIVER}）..."
rm -f "$WORK/dev.db"*
ADMIN_INIT_PASSWORD=$ADMIN_INIT_PASSWORD SELFCHECK_URL="${BASE_URL}/status" \
  nohup "$WORK/uat-server" -addr "127.0.0.1:${UAT_PORT}" -frontend frontend-react/dist -kbdb "$WORK/dev.db" \
  > "$WORK/server.log" 2>&1 < /dev/null &
SERVER_PID=$!
OK=0
for i in $(seq 1 20); do
  sleep 1
  if curl -s -m 2 "${BASE_URL}/status" | grep -q '"ok":true'; then OK=1; break; fi
done
[ "${OK:-0}" = "1" ] || { echo "后端启动失败"; tail -5 "$WORK/server.log"; kill $SERVER_PID $MOCK_PID 2>/dev/null; exit 1; }
log "后端就绪（${i}s）"

# ---------- 4. 测试配置（幂等写 system_config，等价超管控制台；双方言经 dbcfg） ----------
log "写入测试配置（防刷放宽/强制计费/mock支付）..."
source scripts/uat/dblib.sh
export UAT_DB="$WORK/dev.db"
dbcfg register_ip_min_interval_sec 0
dbcfg register_ip_daily_limit 1000
dbcfg billing_enforced 1
dbcfg pay_mode mock

# ---------- 5. 后端 API 全链路（A/B 主链路 + T 交易专项，双方言断言层） ----------
log "===== 后端 API 全链路 UAT ====="
export BASE_URL UAT_DB="$WORK/dev.db" ADMIN_PASS=$ADMIN_INIT_PASSWORD MOCK_LLM_URL="http://127.0.0.1:${MOCK_LLM_PORT}"
bash scripts/uat/api_uat.sh | tee "$WORK/api_uat.log"
API_TAIL=$(tail -1 "$WORK/api_uat.log")
API_PASS=$(echo "$API_TAIL" | grep -oE 'PASS=[0-9]+' | cut -d= -f2)
API_FAIL=$(echo "$API_TAIL" | grep -oE 'FAIL=[0-9]+' | cut -d= -f2)

log "===== 后端交易/功能专项 UAT（api_uat_txn）====="
bash scripts/uat/api_uat_txn.sh | tee "$WORK/api_uat_txn.log"
TXN_TAIL=$(grep -oE 'T-PASS=[0-9]+ FAIL=[0-9]+' "$WORK/api_uat_txn.log" | tail -1)
TXN_PASS=$(echo "$TXN_TAIL" | grep -oE 'PASS=[0-9]+' | cut -d= -f2)
TXN_FAIL=$(echo "$TXN_TAIL" | grep -oE 'FAIL=[0-9]+' | cut -d= -f2)

# ---------- 6. 前端像素级 UAT ----------
log "===== 前端 E2E UAT（像素级 + 运行时健康 + 冒烟）====="
mkdir -p frontend-react/artifacts
(cd frontend-react && BASE_URL="$BASE_URL" API_URL="$BASE_URL" npx playwright test e2e/ --reporter=line) | tee "$WORK/pixel_uat.log"
PIX_PASS=$(grep -cE '✓|passed' "$WORK/pixel_uat.log" || true)

# ---------- 7. 汇总 ----------
DUR=$(( $(date +%s) - T0 ))
log "=============================="
log "方言：${DB_DRIVER}"
log "后端 API UAT（A/B 主链路）：PASS=${API_PASS:-0} FAIL=${API_FAIL:-0}"
log "后端交易专项 UAT（T 套件）：PASS=${TXN_PASS:-0} FAIL=${TXN_FAIL:-0}"
log "前端像素 UAT：见 pixel_uat.log（截图：frontend-react/artifacts/）"
log "日志目录：$WORK"
log "=============================="
[ "${KEEP:-0}" != "1" ] && { kill $SERVER_PID $MOCK_PID 2>/dev/null || true; }
[ "${API_FAIL:-1}${TXN_FAIL:-1}" = "00" ] || exit 1
exit 0
