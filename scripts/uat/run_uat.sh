#!/usr/bin/env bash
# ============================================================================
# scripts/uat/run_uat.sh — 一键自动化 UAT（后端全链路 + 前端像素级 + 前端↔后端联调）
# 流程：
#   1. 构建 Go 后端二进制
#   2. 启动 mock LLM（OpenAI 兼容，回译文/向量）
#   3. 以全新临时库启动后端（固定超管密码 + 关闭 watchdog 自杀探活）
#   4. 放宽注册防刷 + 开启强制计费 + mock 支付（system_config 直写，等价控制台配置）
#   4b. 构建并启动 assist-server（AI 助手独立服务，T52 代理链路用同源上游）
#   5. 运行后端 API 全链路断言（api_uat.sh）
#   6. 运行前端像素级 UAT（pixel_uat.spec.ts，Playwright 截图）
#   7. 输出汇总与耗时
# 用法：bash scripts/uat/run_uat.sh
#   PG 主矩阵（默认，发布闸门）：bash scripts/uat/run_uat.sh
#   SQLite 本地快跑：    DB_DRIVER=sqlite bash scripts/uat/run_uat.sh
# 环境变量：UAT_PORT（默认8899）、MOCK_LLM_PORT（默认8901）、ASSIST_PORT（默认8898）、KEEP（=1 不清理环境）
#           DB_DRIVER/DB_DSN/UAT_DB —— 断言层 dblib.sh 据此路由（生产方言必须进矩阵）
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1

UAT_PORT="${UAT_PORT:-8899}"
MOCK_LLM_PORT="${MOCK_LLM_PORT:-8901}"
MOCK_CHAIN_PORT="${MOCK_CHAIN_PORT:-8902}"
BASE_URL="http://127.0.0.1:${UAT_PORT}"
WORK=$(mktemp -d)
ADMIN_INIT_PASSWORD=Admin@1234
DB_DRIVER="${DB_DRIVER:-postgres}"   # ★ 批次7：发布闸门主矩阵=PG；本地快跑显式 DB_DRIVER=sqlite

log(){ echo "[run_uat] $*"; }
T0=$(date +%s)

# ---------- 0-. 端口占位前置闸门（★ 2026-09-26 批 I 收尾踩坑后新增） ----------
# 现象：本机 14:26 上一轮遗留下的 mock_chain 仍占着 :8902，本轮自己的 mock 起不来
#       （mockchain.log 里 OSError: Address already in use），但就绪探针 curl /v1/blocks
#       拨到的是**外来实例**⇒ 照样绿；于是断言跑在被陈旧状态污染的 mock 上
#       （T42 的固定 tx_id 'c3'*32 与上一轮同名交易相撞，正确金额那笔被唯一键拒收，
#        订单恒 pending、payments 无凭证 ⇒ 两条 T42 红）。这类红**不是回归**，
#       但比回归更难查；而"探针绿了却对着别人的服务测"本身就是闸门失效。
# 处置：四个端口任一被占即**开局即红**并点名占位方（不静默换端口、不复用外来实例）。
ensure_port_free(){
  local p="$1" name="$2" own=""
  own="$(lsof -nP -iTCP:"$p" -sTCP:LISTEN -t 2>/dev/null | tr '\n' ' ' || true)"
  if [ -n "$own" ]; then
    printf '[run_uat] ❌ %s 端口 %s 已被进程占用（pid=%s）\n' "$name" "$p" "$own"
    lsof -nP -iTCP:"$p" -sTCP:LISTEN 2>/dev/null | sed 's/^/[run_uat]      | /'
    printf '[run_uat]    ⇒ 请先按 pid 清掉遗留实例再跑（禁止换端口绕过：断言与探针都会指错服务方）\n'
    exit 1
  fi
}
ensure_port_free "$UAT_PORT"        "后端"
ensure_port_free "$MOCK_LLM_PORT"   "mock LLM"
ensure_port_free "$MOCK_CHAIN_PORT" "mock chain"
ensure_port_free "${ASSIST_PORT:-8898}" "assist-server"
# mock chain 状态新鲜度自证：初始 tip 必须仍是 1_000_000 且无历史转入。
# （ensure_port_free 已挡住"外来实例"，这一条兜住同一实现被以其他端口提前喂过数据的场景；
#   在下面的 2b 就绪后执行，见该处 MOCK_CHAIN_FRESH 断言。）

# ---------- 0. 方言矩阵：PG 时重建专用测试库 ----------
# ★ 引号闸门前置（2026-09-21）：断言脚本里「双引号内嵌命令替换 + {\"a\":1,\"b\":2}」会被 bash 做
#   大括号展开，把 curl 的 body 截断 → 断言对着「参数格式错误」恒判，闸门静默失真。
#   这类失效不报错、只在长期跑绿后突然翻红，故放在建库/构建之前，几毫秒拦住。
python3 scripts/uat/lint_uat_quotes.py scripts/uat || { echo "UAT 引号闸门未通过，中止"; exit 1; }

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

# ---------- 1.5 ★ G3：竞态检测全量单测（UAT_SKIP_RACE=1 可跳过，本地快速回归用） ----------
if [ "${UAT_SKIP_RACE:-0}" = "1" ]; then
  log "跳过 go test -race（UAT_SKIP_RACE=1）"
else
  log "竞态检测 go test -race ./internal/...（数分钟）..."
  # ★ -timeout 30m（2026-09-19）：store 包高负载下单跑实测 952s，默认 10m/包会假超时；
  #   失败文案同步改中性（旧文案把任何非零退出——含超时——都报成"数据竞争"，误导定位）
  # ★ 预检方言钉死 sqlite（2026-09-20）：单测族一律内存 SQLite（各测试已自钉方言，如
  #   langs_zh/admin_models_cache/h5_chunk），但 PG 模式上方导出的 DB_DRIVER/DB_DSN 仍会
  #   经 config.Default() 副作用泄漏方言——历史上两次造成整包 api 假红。预检职责=竞态
  #   检测（sqlite 语义即可），PG 方言覆盖由后置 UAT 矩阵（连真 PG 库）承担，不在此处。
  (cd backend-go && env DB_DRIVER=sqlite DB_DSN= go test -race -count=1 -timeout 30m ./internal/...) \
    || { echo "❌ 竞态检测未通过（数据竞争或测试失败/超时，见上方 go test 输出），中止 UAT"; exit 1; }
  log "竞态检测通过"
fi
# ★ P1-1 闸门补防（2026-09-15）：dist「存在即跳过」会让 E2E 对着旧包跑（曾实测 dist 比 HEAD 源码旧
#   44 分钟而无人察觉）。改为新鲜度判定：src 下任何 .ts/.tsx 比 dist/index.html 新即重建（vite build ~10s）。
NEED_BUILD=0
[ -f frontend-react/dist/index.html ] || NEED_BUILD=1
if [ "$NEED_BUILD" = "0" ] && [ -n "$(find frontend-react/src frontend-react/e2e -newer frontend-react/dist/index.html \( -name '*.ts' -o -name '*.tsx' \) -print -quit)" ]; then
  log "检测到前端源码比 dist 新，重建 dist..."
  NEED_BUILD=1
fi
if [ "$NEED_BUILD" = "1" ]; then
  (cd frontend-react && npm run build) || { echo "前端构建失败"; exit 1; }
fi

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

# ---------- 2b. mock chain（USDT 收款对账，T42 依赖） ----------
log "启动 mock chain :${MOCK_CHAIN_PORT}..."
nohup python3 scripts/uat/mock_chain.py "$MOCK_CHAIN_PORT" > "$WORK/mockchain.log" 2>&1 < /dev/null &
CHAIN_PID=$!
OK=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "http://127.0.0.1:${MOCK_CHAIN_PORT}/v1/blocks" >/dev/null 2>&1; then OK=1; break; fi
done
[ "${OK:-0}" = "1" ] || { echo "mock chain 启动失败"; exit 1; }
# ★ 新鲜度复核（与上面 ensure_port_free 同族，双保险）：就绪探针只证明"有人应答"，
#   不证明"应答的是本轮刚起的实例"。这里直接读 /state：初始尖 1_000_000 且转入表为空
#   才算真身（外来实例要么已被上一轮 advance/inject 过，要么带着一堆同名 tx）。
FRESH="$(curl -s -m 3 "http://127.0.0.1:${MOCK_CHAIN_PORT}/state" 2>/dev/null || true)"
if ! printf '%s' "$FRESH" | python3 -c '
import json,sys
try: s=json.loads(sys.stdin.read() or "{}")
except Exception: sys.exit(1)
sys.exit(0 if s.get("tip")==1_000_000 and len(s.get("transfers",[]))==0 else 1)
' 2>/dev/null; then
  printf '[run_uat] ❌ mock chain 状态不是"全新实例"（tip 应为 1000000、transfers 应为空），实际：%s\n' "$FRESH"
  printf '[run_uat]    ⇒ 端口 %s 上极可能是上一轮遗留的 mock（脏状态会让 T42 的固定 tx_id 相撞而假红）\n' "$MOCK_CHAIN_PORT"
  kill $CHAIN_PID $MOCK_PID 2>/dev/null
  exit 1
fi
log "mock chain 就绪（${i}s，已确认为全新实例）"

# ---------- 2c. assist-server（AI 助手独立服务，T52 代理链路依赖） ----------
# ★ #34（2026-09-21）：主后台 /api/admin/assist/* 是「同源反代到 assist 管理面」，
#   没有真实上游就只能测到 fail-closed 分支，代理的转发/凭据注入/审计脱敏都测不出来。
#   故 UAT 矩阵把 assist-server 一起拉起来（独立 SQLite，与业务库文件隔离），
#   并把 ASSIST_BASE_URL / ASSIST_ADMIN_TOKEN 同值喂给主服务，模拟生产同源部署。
ASSIST_PORT="${ASSIST_PORT:-8898}"
ASSIST_TOKEN="uat-assist-token-34"
log "构建并启动 assist-server :${ASSIST_PORT}..."
(cd backend-go && go build -o "$WORK/assist-server" ./cmd/assist-server) || { echo "assist-server 构建失败"; exit 1; }
ASSIST_ADDR="127.0.0.1:${ASSIST_PORT}" ASSIST_DB="$WORK/assist.db" ASSIST_ADMIN_TOKEN="$ASSIST_TOKEN" \
  ASSIST_LLM_BASE_URL="http://127.0.0.1:${MOCK_LLM_PORT}/v1" ASSIST_LLM_API_KEY=mock-key ASSIST_LLM_MODEL=mock-llm \
  nohup "$WORK/assist-server" > "$WORK/assist.log" 2>&1 < /dev/null &
ASSIST_PID=$!
OK=0
for i in $(seq 1 15); do
  sleep 1
  if curl -s -m 2 "http://127.0.0.1:${ASSIST_PORT}/health" | grep -q '"ok":true'; then OK=1; break; fi
done
[ "${OK:-0}" = "1" ] || { echo "assist-server 启动失败"; tail -10 "$WORK/assist.log"; kill $ASSIST_PID $MOCK_PID $CHAIN_PID 2>/dev/null; exit 1; }
log "assist-server 就绪（${i}s）"

# ---------- 3. 后端（全新库 + 探活自指向 + 固定超管密码；方言随 DB_DRIVER） ----------
log "启动后端 :${UAT_PORT}（方言 ${DB_DRIVER}）..."
rm -f "$WORK/dev.db"*
# ★ T36（09-14 商业化批次）：固定 ADMIN_TOKEN（S9 收口鉴权断言）与 METRICS_TOKEN（/metrics 401/Bearer），
# 并布 S8 测试词包（SENSITIVE_WORDS_FILE 覆盖默认路径，热加载生效）。
# ★ 2026-09-22（全量 UAT 报告 P0-4）：USER_DATA_DIR 钉到 $WORK——上传目录由它派生（config.go
#   c.UploadDir = UserDataDir/_uploads），不钉则分片上传（T28）写进本机真实应用数据目录，
#   既污染生产数据又随沙箱权限时红时绿（EPERM 假红），钝化对真回归的敏感度。
printf "# UAT T36 测试词包\n紫火核弹T36\n" > "$WORK/sensitive_words.txt"
ADMIN_INIT_PASSWORD=$ADMIN_INIT_PASSWORD SELFCHECK_URL="${BASE_URL}/status" \
  ADMIN_TOKEN=uat-admin-token-36 METRICS_TOKEN=uat-metrics-36 \
  SENSITIVE_WORDS_FILE="$WORK/sensitive_words.txt" \
  MAIL_NOOP_PRINT_BODY=1 \
  USDT_TRON_BASE="http://127.0.0.1:${MOCK_CHAIN_PORT}" USDT_SCAN_INTERVAL_SEC=5 \
  ASSIST_BASE_URL="http://127.0.0.1:${ASSIST_PORT}" ASSIST_ADMIN_TOKEN="$ASSIST_TOKEN" \
  USER_DATA_DIR="$WORK/udata" \
  nohup "$WORK/uat-server" -addr "127.0.0.1:${UAT_PORT}" -frontend frontend-react/dist -kbdb "$WORK/dev.db" \
  > "$WORK/server.log" 2>&1 < /dev/null &
SERVER_PID=$!
OK=0
for i in $(seq 1 20); do
  sleep 1
  if curl -s -m 2 "${BASE_URL}/status" | grep -q '"ok":true'; then OK=1; break; fi
done
[ "${OK:-0}" = "1" ] || { echo "后端启动失败"; tail -5 "$WORK/server.log"; kill $SERVER_PID $MOCK_PID $ASSIST_PID 2>/dev/null; exit 1; }
log "后端就绪（${i}s）"

# ---------- 4. 测试配置（幂等写 system_config，等价超管控制台；双方言经 dbcfg） ----------
log "写入测试配置（防刷放宽/强制计费/mock支付）..."
source scripts/uat/dblib.sh
export UAT_DB="$WORK/dev.db"
dbcfg register_ip_min_interval_sec 0
dbcfg register_ip_daily_limit 1000
dbcfg billing_enforced 1
dbcfg pay_mode mock
# ★ T42 USDT：默认开启收款（trc20 单链，地址占位 base58 合规），对账器 2s 轮询；tx 唯一/尾数依赖后端逻辑本身
dbcfg usdt_enabled 0   # T42 自开自关（避免影响后续前端 E2E 收银台用例）
dbcfg usdt_chains trc20
dbcfg usdt_rate_fen_per_usdt 720
dbcfg usdt_addr_trc20 T4BJRYfnu29GPWdksz7EMUbiqx5CKSZgov
dbcfg usdt_confirmations_trc20 1

# ---------- 5. 后端 API 全链路（A/B 主链路 + T 交易专项，双方言断言层） ----------
log "===== 后端 API 全链路 UAT ====="
export BASE_URL UAT_DB="$WORK/dev.db" UAT_SERVER_LOG="$WORK/server.log" ADMIN_PASS=$ADMIN_INIT_PASSWORD MOCK_LLM_URL="http://127.0.0.1:${MOCK_LLM_PORT}" MOCK_CHAIN_URL="http://127.0.0.1:${MOCK_CHAIN_PORT}" ASSIST_URL="http://127.0.0.1:${ASSIST_PORT}" ASSIST_UAT_TOKEN="$ASSIST_TOKEN"
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
# PW_TARGET 可定向单 spec（缺陷迭代提速；缺省 e2e/ 全量）
# ★ P2（2026-09-16）：①PWTEST_CHILD_PROCESS_TIMEOUT 把 worker teardown 挂起强杀从
#   默认 300s 降到 60s——曾实测高负载下偶发「worker did not exit」拖红闸门 10 分钟；
#   ②首轮失败自动 --last-failed 复跑一轮，区分「真回归」（复跑仍红）与「环境 flaky」
#   （复跑转绿→WARN 不拦截），消除发布闸门随机误红。
(cd frontend-react && PWTEST_CHILD_PROCESS_TIMEOUT=60000 BASE_URL="$BASE_URL" API_URL="$BASE_URL" npx playwright test "${PW_TARGET:-e2e/}" --reporter=line) | tee "$WORK/pixel_uat.log"
# ★ P1-1 闸门补防（2026-09-15）：Playwright 失败必须进退出码——旧实现只 tee 日志不采码，
#   「发布闸门」对前端 E2E 不设防（HEAD 曾带 2 条红用例合入并被声明全绿）。
PW_EXIT=${PIPESTATUS[0]}
PW_FAIL=0
if [ "${PW_EXIT:-1}" -ne 0 ]; then
  log "首轮 E2E 非零（exit=${PW_EXIT}），复跑失败用例甄别 flaky..."
  (cd frontend-react && PWTEST_CHILD_PROCESS_TIMEOUT=60000 BASE_URL="$BASE_URL" API_URL="$BASE_URL" npx playwright test --last-failed --reporter=line) | tee "$WORK/pixel_uat_retry.log"
  PW_RETRY_EXIT=${PIPESTATUS[0]}
  if [ "${PW_RETRY_EXIT:-1}" -eq 0 ]; then
    log "⚠️ 复跑全绿：首轮为环境 flaky（非代码回归），闸门放行并留痕 pixel_uat_retry.log"
  else
    PW_FAIL=1
    echo "❌ 前端 E2E 复跑仍失败（exit=${PW_RETRY_EXIT}），发布闸门拦截"
  fi
fi
[ "$PW_FAIL" = "1" ] && echo "❌ 前端 E2E 失败（exit=${PW_EXIT}），发布闸门拦截"

# ---------- 7. 汇总 ----------
DUR=$(( $(date +%s) - T0 ))
log "=============================="
log "方言：${DB_DRIVER}"
log "后端 API UAT（A/B 主链路）：PASS=${API_PASS:-0} FAIL=${API_FAIL:-0}"
log "后端交易专项 UAT（T 套件）：PASS=${TXN_PASS:-0} FAIL=${TXN_FAIL:-0}"
log "前端 E2E UAT：exit=${PW_EXIT:-?}（FAIL=${PW_FAIL:-1}）；明细见 pixel_uat.log（截图：frontend-react/artifacts/）"
log "日志目录：$WORK"
log "=============================="
[ "${KEEP:-0}" != "1" ] && { kill $SERVER_PID $MOCK_PID $CHAIN_PID $ASSIST_PID 2>/dev/null || true; }
[ "${API_FAIL:-1}${TXN_FAIL:-1}${PW_FAIL:-1}" = "000" ] || exit 1
exit 0
