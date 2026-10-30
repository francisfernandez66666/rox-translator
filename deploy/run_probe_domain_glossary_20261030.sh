#!/usr/bin/env bash
# ============================================================================
# deploy/run_probe_domain_glossary_20261030.sh —— 本机驱动：把探针与 opskey 送上去并跑一次
#
# 判域这一腿用的是全局主模型（现网实测 tencent/Hunyuan-MT-7B，纯翻译模型），而它 fail-open：
# 答不出 JSON 时**零报错、词表恒空**。所以"开闸"之后必须立刻手拨一次把日志读数拿出来，
# 这一支就是那次手拨的执行体（详见 deploy/probe_domain_glossary_20261030.sh 文件头）。
#
# 用法：
#   TID=1 UID_=10001 bash deploy/run_probe_domain_glossary_20261030.sh
# 判绿：末行 PROBE_DONE=1（判域失败也会跑完并出 reason 读数——那一档要不要配
#       domain_detect_model 由人看读数决定）。
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

SSH_HOST="${SSH_HOST:-seoul}"
SSH_PORT="${SSH_PORT:-28022}"
TID="${TID:-1}"
UID_="${UID_:-10001}"
LOCAL_PROBE="deploy/probe_domain_glossary_20261030.sh"

bash -n "$LOCAL_PROBE" || { echo "ABORT: 探针脚本语法不过"; exit 1; }

echo "[probe] 交叉编译 opskey（linux/amd64）"
( cd backend-go && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /tmp/opskey-linux ./cmd/opskey ) \
  || { echo "ABORT: opskey 编译失败"; exit 1; }
ls -l /tmp/opskey-linux

echo "[probe] 上传 opskey + 探针脚本"
scp -P "$SSH_PORT" -q /tmp/opskey-linux "$SSH_HOST:/tmp/opskey-20261030" || { echo "ABORT: scp opskey 失败"; exit 2; }
scp -P "$SSH_PORT" -q "$LOCAL_PROBE" "$SSH_HOST:/tmp/probe_domain_glossary_20261030.sh" || { echo "ABORT: scp 探针失败"; exit 2; }

ssh -p "$SSH_PORT" "$SSH_HOST" \
  "chmod +x /tmp/opskey-20261030 /tmp/probe_domain_glossary_20261030.sh && \
   TID=$TID UID_=$UID_ PROBE_VARIANT=${PROBE_VARIANT:-A} OPSKEY=/tmp/opskey-20261030 bash /tmp/probe_domain_glossary_20261030.sh"
RC=$?
echo "PROBE_REMOTE_EXIT=$RC"
exit "$RC"
