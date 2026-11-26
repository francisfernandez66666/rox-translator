#!/usr/bin/env bash
# release_20261126_dispatch.sh — 工单动态派发（广州执行机）＋上传闸抬升双站发版。
#
# 本脚本只负责构建、交付包自检、上传与常设 swap；模型/采集源写库与上线回读由 runbook 单独执行。
# 用法：bash deploy/release_20261126_dispatch.sh           # 干跑
#       APPLY=1 bash deploy/release_20261126_dispatch.sh   # 真换
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261126dispatch"
TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"
SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | cut -d' ' -f1; }

feat_count() {
  python3 - "$1" "$2" <<'PY'
import sys
print(open(sys.argv[1], 'rb').read().count(sys.argv[2].encode('utf-8')))
PY
}

echo "===== P0 前端构建与交付包 ====="
(cd "$REPO/frontend-react" && npm run typecheck && npx vite build) || { echo "ABORT=2 前端类型检查或构建失败"; exit 2; }
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩侧车生成失败"; exit 2; }
node "$REPO/scripts/precompress_dist.mjs" --check || { echo "ABORT=2 预压缩侧车校验失败"; exit 2; }
(cd "$REPO/frontend-react" && tar -czf "$TAR" --exclude='dist/wasm/sherpa-onnx/*.data' --exclude='._*' --exclude='.DS_Store' dist) \
  || { echo "ABORT=2 前端打包失败"; exit 2; }
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过，禁止换源"; exit 2; }

echo "===== P0-B 后端二进制 ====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 3; }
W_DECIDE=$(feat_count "$SERVER_BIN" "工单块级派发开始")
W_RECEIPT=$(feat_count "$SERVER_BIN" "工单块级派发回执")
W_TAKEOVER=$(feat_count "$SERVER_BIN" "dispatch_policy")
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN")"
echo "特征串 派发开始=${W_DECIDE} 派发回执=${W_RECEIPT} 超限接手=${W_TAKEOVER}"
[ "$W_DECIDE" != "0" ] || { echo "ABORT=3 缺派发决策运行期特征串"; exit 3; }
[ "$W_RECEIPT" != "0" ] || { echo "ABORT=3 缺派发回执运行期特征串"; exit 3; }
[ "$W_TAKEOVER" != "0" ] || { echo "ABORT=3 缺健康面派发状态词特征串"; exit 3; }
TAR_SHA=$(sha_local "$TAR")
BIN_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机构建与自检；未 scp、未碰服务器。加 APPLY=1 才执行 P1。"
  exit 0
fi

echo "===== P1 上传（仍未落位）====="
scp -P "$PORT" "$TAR" "root@$HOST:/tmp/fe-$TAG.tar.gz" || { echo "ABORT=4 scp tar 失败"; exit 4; }
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=4 scp server 失败"; exit 4; }
scp -P "$PORT" "$SWAP" "root@$HOST:/tmp/swap_web_and_binary.sh" || { echo "ABORT=4 scp swap 脚本失败"; exit 4; }

echo "===== P2 两站换源＋换件（常设两段式执行体）====="
ssh -p "$PORT" "root@$HOST" "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算成功）"
exit "$rc"
