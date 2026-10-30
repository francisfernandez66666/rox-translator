#!/usr/bin/env bash
# ============================================================================
# deploy/run_kb_industry_rollout_20261030.sh — 本机驱动：把服务器侧脚本送上去再执行
#
# 为什么要这么一层（而不是在本机直接 ssh 一串 SQL）：
#   《AGENTS》§一·9 与 §三那条「现攒命令迟早抄错」——落位/写库的判据必须**在仓库里有一份可复跑的执行体**，
#   本机这一层只负责「上传 + 下令 + 把远端退出码抬到最外层」（§三「跑一个脚本看它报不报错，
#   退出码必须一路 exit 到最外层」）。远端脚本自己默认只读，写档要 APPLY=1。
#
# 用法：
#   bash deploy/run_kb_industry_rollout_20261030.sh                                   # 只读盘点
#   MODE=packs APPLY=1 bash deploy/run_kb_industry_rollout_20261030.sh                # 建包+采集源
#   MODE=terms APPLY=1 bash deploy/run_kb_industry_rollout_20261030.sh                # 录人工术语
#   MODE=gloss APPLY=1 GLOSS_TID=12 bash deploy/run_kb_industry_rollout_20261030.sh   # 按租户开词表
# 环境变量：SSH_HOST（默认 seoul）/ SSH_PORT（默认 28022）/ DB（默认 langcross）
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

SSH_HOST="${SSH_HOST:-seoul}"
SSH_PORT="${SSH_PORT:-28022}"
REMOTE_SCRIPT="/tmp/kb_industry_rollout_20261030.sh"
LOCAL_SCRIPT="deploy/kb_industry_rollout_20261030.sh"

[ -f "$LOCAL_SCRIPT" ] || { echo "ABORT: 找不到 $LOCAL_SCRIPT"; exit 1; }
bash -n "$LOCAL_SCRIPT" || { echo "ABORT: 远端脚本语法不过，不上机"; exit 1; }

TAR_L=$(sha256sum "$LOCAL_SCRIPT" 2>/dev/null | awk '{print $1}' || shasum -a 256 "$LOCAL_SCRIPT" | awk '{print $1}')
echo "[run] 上传 ${LOCAL_SCRIPT}（本机 sha ${TAR_L:0:16}…）→ $SSH_HOST:$REMOTE_SCRIPT"
scp -P "$SSH_PORT" -q "$LOCAL_SCRIPT" "$SSH_HOST:$REMOTE_SCRIPT" || { echo "ABORT: scp 失败"; exit 2; }

echo "[run] 远端 sha 回读（不等值＝跑的不是刚上传那一份，直接停手）"
REMOTE_L=$(ssh -p "$SSH_PORT" "$SSH_HOST" "sha256sum $REMOTE_SCRIPT | cut -d' ' -f1" </dev/null)
if [ "${REMOTE_L:-}" != "${TAR_L:-}" ]; then
  echo "ABORT: 远端脚本指纹不等值（本机 ${TAR_L:0:16}／远端 ${REMOTE_L:0:16}）⇒ 停手，别拿旧脚本写库"
  exit 3
fi
echo "OK   远端脚本与本机同一份"

ssh -p "$SSH_PORT" "$SSH_HOST" \
  "MODE='${MODE:-audit}' APPLY='${APPLY:-0}' GLOSS_TID='${GLOSS_TID:-0}' DB='${DB:-langcross}' \
   CONFIRM_BINARY='${CONFIRM_BINARY:-0}' DETECT_MODEL='${DETECT_MODEL:-}' TEN1_PKG='${TEN1_PKG:-}' \
   bash ${REMOTE_SCRIPT}"
RC=$?
echo "RUN_KB_REMOTE_EXIT=$RC"
exit "$RC"
