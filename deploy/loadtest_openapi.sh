#!/usr/bin/env bash
# ============================================================================
# deploy/loadtest_openapi.sh — OpenAPI 翻译压测对照脚本（并发优化 R1/R2 验收）
#
# 用途：以 N 路并发提交 M 个文本任务并轮询至终态，输出 P50/P95 时延与成功率，
#       用于「改造前后」吞吐对照（配合 PROGRESS 记录基线）。
#
# 前置：服务器上执行；需一个有效 API Key（管理后台「API Key」面板签发，translate 权限）
# 用法：./deploy/loadtest_openapi.sh <base_url> <api_key> [并发数=4] [任务数=8]
# 依赖：curl + python3（百分位计算）
# ============================================================================
set -euo pipefail

BASE="${1:?用法: $0 <base_url> <api_key> [并发数] [任务数] [file_mode=text|file] [pdf_path]}"
KEY="${2:?缺少 api_key}"
CONC="${3:-4}"
TOTAL="${4:-8}"
MODE="${5:-text}"
PDF="${6:-}"

[ "$TOTAL" -ge "$CONC" ] || { echo "任务数应 ≥ 并发数"; exit 1; }
if [ "$MODE" = "file" ]; then
  [ -n "$PDF" ] && [ -f "$PDF" ] || { echo "file 模式需提供第 6 个参数 pdf_path 且文件存在"; exit 1; }
fi

TEXT="智能驾驶域控制器固件升级已完成，蓝牙钥匙绑定状态同步成功，请提醒用户在下次上车前重新校准迎宾灯语与座椅记忆位置。"
OUT=/tmp/loadtest_$$; mkdir -p "$OUT"; trap 'rm -rf "$OUT"' EXIT

if [ "$MODE" = "file" ]; then
  echo "==> 压测开始(文件模式) base=$BASE 并发=$CONC 任务数=$TOTAL pdf=$PDF"
else
  echo "==> 压测开始(文本模式) base=$BASE 并发=$CONC 任务数=$TOTAL"
fi

submit() { # 提交单任务，输出 task_id
  if [ "$MODE" = "file" ]; then
    curl -s -X POST "$BASE/openapi/v1/tasks" \
      -H "Authorization: Bearer $KEY" -F "files=@$PDF" -F "target_langs=en" -F "mode=pro" \
      | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('task_id') or '')"
  else
    curl -s -X POST "$BASE/openapi/v1/tasks" \
      -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
      -d "{\"text\":\"$TEXT\",\"target_langs\":[\"en\"],\"mode\":\"pro\"}" \
      | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('task_id') or '')"
  fi
}
wait_one() { # 轮询单任务至终态，输出耗时秒（failed 输出 FAIL:<code>）
  local id="$1" t0=$SECONDS
  while :; do
    st=$(curl -s "$BASE/openapi/v1/tasks/status?id=$id" -H "Authorization: Bearer $KEY" \
         | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('status'),d.get('error_code',''))")
    set -- $st
    case "$1" in
      completed) echo $((SECONDS - t0)); return ;;
      failed) echo "FAIL:$2"; return ;;
    esac
    [ $((SECONDS - t0)) -ge 1800 ] && { echo "FAIL:timeout"; return; }
    sleep 5
  done
}

# —— 受控并发提交+等待 ——
i=0
while [ $i -lt $TOTAL ]; do
  batch=""
  for _ in $(seq 1 $CONC); do
    [ $i -ge $TOTAL ] && break
    id=$(submit); i=$((i+1))
    [ -n "$id" ] && batch="$batch $id"
  done
  for id in $batch; do
    ( r=$(wait_one "$id"); echo "$r" >>"$OUT/results" ) &
  done
  wait
done

# —— 汇总 ——
python3 - "$OUT/results" <<'PY'
import sys, statistics as st
rows = [l.strip() for l in open(sys.argv[1]) if l.strip()]
ok = sorted(int(r) for r in rows if not r.startswith('FAIL'))
fail = [r for r in rows if r.startswith('FAIL')]
def pct(p): 
    k = max(0, round(len(ok)*p) - 1)
    return ok[k] if ok else -1
print(f"完成={len(ok)} 失败={len(fail)} 成功率={len(ok)/max(1,len(rows))*100:.0f}%")
if ok:
    print(f"时延秒: min={ok[0]} P50={pct(.5)} P95={pct(.95)} max={ok[-1]} 均值={st.mean(ok):.1f}")
for f in fail[:5]:
    print("失败样例:", f)
busy = [f for f in fail if 'busy' in f.lower() or 'database' in f.lower()]
if busy:
    print("⚠ 出现数据库忙/SQLITE_BUSY 类失败：", len(busy), "条 —— 并发写优化未达预期")
else:
    print("✓ 无数据库忙类失败（并发写优化生效）")
PY
echo "==> 压测结束（建议：改造前后各跑一次，同参数对照；file 模式可验证大文件并发下的 SQLITE_BUSY 是否归零）"