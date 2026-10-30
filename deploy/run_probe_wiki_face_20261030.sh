#!/usr/bin/env bash
# ============================================================================
# deploy/run_probe_wiki_face_20261030.sh —— 本机驱动：把只读维基探针送上去跑一次
# 用法：bash deploy/run_probe_wiki_face_20261030.sh
# 判绿：末行 WIKIFACE_ALL_OK=1（只表示「量到了」，命中好坏看 W2_HITS/W2_MISS）。
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

SSH_HOST="${SSH_HOST:-seoul}"
SSH_PORT="${SSH_PORT:-28022}"
REMOTE_SCRIPT="/tmp/probe_wiki_face_20261030.sh"
LOCAL_SCRIPT="deploy/probe_wiki_face_20261030.sh"

bash -n "$LOCAL_SCRIPT" || { echo "ABORT: 探针语法不过"; exit 1; }
scp -P "$SSH_PORT" -q "$LOCAL_SCRIPT" "$SSH_HOST:$REMOTE_SCRIPT" || { echo "ABORT: scp 失败"; exit 2; }

L_SHA=$(shasum -a 256 "$LOCAL_SCRIPT" | awk '{print $1}')
R_SHA=$(ssh -p "$SSH_PORT" "$SSH_HOST" "sha256sum $REMOTE_SCRIPT | awk '{print \$1}'")
if [ "${L_SHA}" != "$R_SHA" ]; then
  echo "ABORT: 远端脚本与本机不是同一份（远端 $R_SHA / 本机 ${L_SHA}）"
  exit 3
fi
echo "OK   远端脚本与本机同一份"

ssh -p "$SSH_PORT" "$SSH_HOST" "bash $REMOTE_SCRIPT"
RC=$?
echo "RUN_WIKIFACE_REMOTE_EXIT=$RC"
exit "$RC"
