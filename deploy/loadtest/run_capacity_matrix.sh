#!/usr/bin/env bash
# ============================================================================
# deploy/loadtest/run_capacity_matrix.sh — 容量台阶矩阵（★ P1 压测基线量化 2026-09-15）
# 背景：单次 k6 探针结果原只落 /tmp 不入库，无 P99/吞吐量化验收阈值（见
#   《P0P2待办核实报告_20260915.md》P1-3）。本脚本逐档跑 VU 台阶、按预算判
#   PASS/FAIL，结果归档 deploy/loadtest/results/<时间戳>/ 供基线对比与容量预告引用。
# 阈值预算（对齐《容量预告与超卖预案_20260914.md》09-14 实测定案）：
#   P99 < 15000ms（安全档 VU≤8 实测 P99≈10s 的 1.5 倍余量；可 P99_MS 覆盖）
#   系统错误率 < 1%（实测恒 0）
# 用法：
#   BASE=https://<host> TOKEN=<api-key> bash deploy/loadtest/run_capacity_matrix.sh
#   VUS_LIST="2 4 8 16 24" DURATION=75s P99_MS=15000 ...（均可 env 覆盖）
# 退出码：全部台阶 PASS=0；任一台阶 FAIL=1（超预算台阶 FAIL 属预期——用于找安全线，
#   判定线以上 FAIL 才需告警；总表 printed 供人工/CI 读取）。
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1

BASE="${BASE:-http://127.0.0.1:8787}"
TOKEN="${TOKEN:?需要 TOKEN（平台 API Key，测前确认租户并发闸已按预案放宽）}"
VUS_LIST="${VUS_LIST:-2 4 8 16 24}"
DURATION="${DURATION:-75s}"
P99_MS="${P99_MS:-15000}"
ERR_RATE_MAX="${ERR_RATE_MAX:-0.01}"
COOLDOWN="${COOLDOWN:-6}"   # 档间冷却秒（对齐 09-14 定案方法）
TS=$(date +%Y%m%d_%H%M%S)
RESULTS_DIR="deploy/loadtest/results/${TS}"
mkdir -p "$RESULTS_DIR"

echo "[capacity] BASE=$BASE VUS=[$VUS_LIST] DURATION=$DURATION P99预算=${P99_MS}ms 归档=$RESULTS_DIR"
command -v k6 >/dev/null || { echo "❌ 未安装 k6（brew install k6）"; exit 1; }

SUMMARY="$RESULTS_DIR/matrix_summary.csv"
printf 'vus,requests,success_rate,p95_ms,p99_ms,limited,err_rate,verdict\n' > "$SUMMARY"
FAILS=0
for V in $VUS_LIST; do
  echo "===== 台阶 VU=$V（${DURATION}，冷却 ${COOLDOWN}s）====="
  if k6 run -e BASE="$BASE" -e TOKEN="$TOKEN" -e VUS="$V" -e DURATION="$DURATION" \
       -e P99_MS="$P99_MS" -e ERR_RATE_MAX="$ERR_RATE_MAX" -e RESULTS_DIR="$RESULTS_DIR" \
       deploy/loadtest/k6.js > "$RESULTS_DIR/k6_vu${V}.log" 2>&1; then
    VERDICT=PASS
  else
    VERDICT=FAIL
  fi
  # 从单台阶 CSV 提取指标行合入总表（跳过表头）
  ROW=$(ls "$RESULTS_DIR"/k6_capacity_${V}vu_*.csv 2>/dev/null | head -1)
  if [ -n "$ROW" ]; then
    tail -n +2 "$ROW" | awk -F, -v v="$V" -v verdict="$VERDICT" \
      'NR==1{printf "%s,%s,%s,%s,%s,%s,%s,%s\n", v, $4, $5, $6, $7, $8, $9, verdict}' >> "$SUMMARY"
  else
    echo "$V,-,-,-,-,-,-,$VERDICT" >> "$SUMMARY"
  fi
  [ "$VERDICT" = "FAIL" ] && FAILS=$((FAILS+1))
  echo "[capacity] VU=$V → $VERDICT"
  sleep "$COOLDOWN"
done

echo "===== 容量矩阵总表 ====="
column -s, -t "$SUMMARY" 2>/dev/null || cat "$SUMMARY"
echo "[capacity] 归档：$RESULTS_DIR（JSON+CSV+日志）"
if [ "$FAILS" -gt 0 ]; then
  echo "[capacity] 存在 $FAILS 个超预算台阶（高位台阶 FAIL 属预期，用于标定安全并发线；"
  echo "           若低位档（≤默认并发闸×租户数）FAIL，请更新《容量预告与超卖预案》并排查）"
  exit 1
fi
exit 0
