#!/usr/bin/env bash
# release_20261021e_ai_agree.sh — IAM P1-4 AI 注册流协议勾选同形（仅前端）
#
# 本批内容：
#   · AiRegisterFlow 不再硬编码 agreed:true —— 账号表单渲染与传统登录/注册同款
#     Checkbox＋auth.userAgreement/auth.privacyPolicy 链接（.ar-link），提交前置
#     守卫 !agreed → toast(auth.needAgree)＋return，提交体 agreed 随勾选态传。
#   · 后端零改动（二进制同树重编自证字节等值）。
#
# 落位一律交给常设执行体 deploy/swap_web_and_binary.sh（两段式、根路径守卫、ETXTBSY 规避）。
# 用法：bash deploy/release_20261021e_ai_agree.sh            # 干跑（本机编译＋打包＋自检）
#       APPLY=1 bash deploy/release_20261021e_ai_agree.sh    # 真换（scp → swap → 复验读数）
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261021e"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 前端构建 → 侧车 → 打包 → 自检 ====="
(cd "$REPO/frontend-react" && npx vite build) || { echo "ABORT=2 vite build 失败"; exit 2; }
# macOS Finder 会在 dist 里落 .DS_Store（构建后仍可能出现）：预压缩与打包前各清一次。
find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
# 本批的特征串必须是**运行期真的会用到**的字面量（AGENTS §三）：登录/注册/AI 流的
# 协议链接类名 .ar-link 只在本批的 AiRegisterFlow 里出现（Login chunk 含本组件）。
grep -R -q -- "ar-link" "$REPO/frontend-react/dist/assets" \
  || { echo "ABORT=2 前端产物缺特征串 ar-link ⇒ 编译的不是本工作树"; exit 2; }
# 反向自证：硬编码 agreed:true 不得再出现在产物里（旧形态在 Login chunk 里有一处）。
if LC_ALL=C grep -R -q -- "agreed:!0" "$REPO/frontend-react/dist/assets"; then
  echo "ABORT=2 产物里仍有 agreed:true 的压缩残留（agreed:!0）⇒ 本批改动没进包"; exit 2
fi
rm -f "$TAR"
(
  cd "$REPO/frontend-react" &&
    COPYFILE_DISABLE=1 tar --exclude='dist/.DS_Store' --exclude='dist/._*' --exclude='dist/wasm/sherpa-onnx/*.data' -czf "$TAR" dist
) || { echo "ABORT=2 打包失败"; exit 2; }
echo "tar=$TAR size=$(wc -c <"$TAR" | tr -d ' ') sha=$(sha_local "$TAR")"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过"; exit 2; }

echo "===== P0-B 后端件（本工作树 HEAD，本批零后端改动）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
W_GONE=$(LC_ALL=C grep -a -c -F -- "密码找回已下线" "$SERVER_BIN" || true)
W_PWD=$(LC_ALL=C grep -a -c -F -- "请先获取邮箱验证码" "$SERVER_BIN" || true)
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ GONE文案=${W_GONE} 改密无码文案=${W_PWD}（期望都 ≥1）"
[ "$W_GONE" != "0" ] || { echo "ABORT=3 缺 GONE 文案特征串 ⇒ 编译的不是含上批改动的树"; exit 3; }
[ "$W_PWD" != "0" ] || { echo "ABORT=3 缺改密无码文案特征串"; exit 3; }
TAR_SHA=$(sha_local "$TAR")
BIN_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋打包＋自检）；未 scp、未碰服务器。加 APPLY=1 才落位。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位）====="
scp -P "$PORT" "$TAR" "root@$HOST:/tmp/fe-$TAG.tar.gz" || { echo "ABORT=4 scp tar 失败"; exit 4; }
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=4 scp server 失败"; exit 4; }
scp -P "$PORT" "$SWAP" "root@$HOST:/tmp/swap_web_and_binary.sh" || { echo "ABORT=4 scp swap 脚本失败"; exit 4; }

echo "===== P2 服务器侧换源＋换件 ====="
ssh -p "$PORT" "root@$HOST" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
exit "$rc"