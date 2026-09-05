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
# 环境变量：UAT_PORT（默认8899）、MOCK_LLM_PORT（默认8901）、KEEP（=1 不清理环境）
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1

UAT_PORT="${UAT_PORT:-8899}"
MOCK_LLM_PORT="${MOCK_LLM_PORT:-8901}"
BASE_URL="http://127.0.0.1:${UAT_PORT}"
WORK=$(mktemp -d)
ADMIN_INIT_PASSWORD=Admin@1234

log(){ echo "[run_uat] $*"; }
T0=$(date +%s)

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

# ---------- 3. 后端（全新临时库 + 探活自指向 + 固定超管密码） ----------
log "启动后端 :${UAT_PORT}（全新临时库 $WORK/dev.db）..."
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

# ---------- 4. 测试配置（直写 system_config，等价超管控制台） ----------
log "写入测试配置（防刷放宽/强制计费/mock支付）..."
sqlite3 "$WORK/dev.db" "INSERT OR REPLACE INTO system_config (key,value,updated_at) VALUES
 ('register_ip_min_interval_sec','0',datetime('now')),
 ('register_ip_daily_limit','1000',datetime('now')),
 ('billing_enforced','1',datetime('now')),
 ('pay_mode','mock',datetime('now'));" 2>/dev/null

# ---------- 5. 后端 API 全链路 ----------
log "===== 后端 API 全链路 UAT ====="
export BASE_URL UAT_DB="$WORK/dev.db" ADMIN_PASS=$ADMIN_INIT_PASSWORD MOCK_LLM_URL="http://127.0.0.1:${MOCK_LLM_PORT}"
bash scripts/uat/api_uat.sh | tee "$WORK/api_uat.log"
API_TAIL=$(tail -1 "$WORK/api_uat.log")
API_PASS=$(echo "$API_TAIL" | grep -oE 'PASS=[0-9]+' | cut -d= -f2)
API_FAIL=$(echo "$API_TAIL" | grep -oE 'FAIL=[0-9]+' | cut -d= -f2)

# ---------- 6. 前端像素级 UAT ----------
log "===== 前端 E2E UAT（像素级 + 运行时健康 + 冒烟）====="
mkdir -p frontend-react/artifacts
(cd frontend-react && BASE_URL="$BASE_URL" API_URL="$BASE_URL" npx playwright test e2e/ --reporter=line) | tee "$WORK/pixel_uat.log"
PIX_PASS=$(grep -cE '✓|passed' "$WORK/pixel_uat.log" || true)

# ---------- 7. 汇总 ----------
DUR=$(( $(date +%s) - T0 ))
log "=============================="
log "后端 API UAT：PASS=$API_PASS FAIL=$API_FAIL"
log "前端像素 UAT：见 pixel_uat.log（截图：frontend-react/artifacts/）"
log "日志目录：$WORK"
log "=============================="
[ "${KEEP:-0}" != "1" ] && { kill $SERVER_PID $MOCK_PID 2>/dev/null || true; }
exit 0
