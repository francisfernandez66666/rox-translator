#!/usr/bin/env bash
# release_20261017b.sh — 2026-10-17 用户八项报障修复批（前端换源 + 后端换件，两站同批）
#
# 本批内容（详见 PROGRESS §三十二）：
#   · Bug1/2 前端：官网 FAQ 列表 key/language 触发、忘记密码支持邮箱找回（后端 auth.go 同批）；
#   · Bug6/7 后端：快速模式改为「知识库直配+初翻+质检、不做校对」（engine/text.go + orchestrator/flow.go）；
#   · Bug8 前后端：即时翻译新增图片上传（后端 POST /api/chat/image，前端工具条图片钮＋结果卡）；
#   · Bug3/4/5 Caddy：安全头三条（microphone/wasm-unsafe-eval/media-src blob:）——**不由本脚本落地**，
#     由同批 `deploy/patch_caddy_security_headers.sh` 单独做（本脚本全程不碰 caddy，同既有契约）。
#
# 后果：前端 dist 与 translator-server **两件都变了**（不像 release_20261017.sh 那样后端零改动）。
#   translator-assist 零改动（本批无 internal/assist/* 变更）⇒ 不换；extension/、sdk/ 零改动 ⇒ 不重打。
#
# 中止线（都在动手之前）：
#   ① 本机 tar 过不了 `scripts/check_frontend_tar.sh`（含 T8 入口体积哨兵）⇒ 不上传；
#   ② 本机新件缺特征串（证明编译的不是本工作树 HEAD）⇒ 不上传；
#   ③ 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何东西。
# 落位一律交给常设执行体 `swap_web_and_binary.sh`（自带根路径两道守卫、两段式、ETXTBSY 规避）。
#
# 用法：bash deploy/release_20261017b.sh            # 干跑（本机编译＋打包＋全部自检，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261017b.sh    # 真换（scp → swap → 复验）
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261017b"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 前端构建 → 侧车 → 打包（顺序只有一种：build → precompress → tar → 自检）====="
# ⚠️ build 会整目录重写 dist（把侧车抹掉），所以预压缩必须在 build 之后、打包之前。
(cd "$REPO/frontend-react" && npx vite build) || { echo "ABORT=2 vite build 失败"; exit 2; }
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
rm -f "$TAR"
# 排除两族：macOS 脏文件（落进 web 根即公网可下载）与部署期补拉的模型权重（199 MB，部署机自己拉）。
(
  cd "$REPO/frontend-react/dist" &&
    COPYFILE_DISABLE=1 tar --exclude='.DS_Store' --exclude='._*' --exclude='./wasm/sherpa-onnx/*.data' -czf "$TAR" .
) || { echo "ABORT=2 打包失败"; exit 2; }
echo "tar=$TAR size=$(wc -c <"$TAR" | tr -d ' ') sha=$(sha_local "$TAR" | cut -c1-16)"

echo "----- P0-T 交付包自检（与 deploy_to_production.sh、手工链**共用同一把尺子**）-----"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过（含 T8 入口体积哨兵）⇒ 不许换源"; exit 2; }

echo "===== P0-B 后端件（本批含 engine/orchestrator/api 改动，必须换）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 特征串证明"编译的是本工作树 HEAD"：本批三处形状必须在件里（少一条就是编了别的树）。
W_IMG=$(grep -a -c -F -- "/api/chat/image" "$SERVER_BIN" || true)
W_REASON=$(grep -a -c -F -- "capability_missing" "$SERVER_BIN" || true)
W_FAST=$(grep -a -c -F -- "kb_match" "$SERVER_BIN" || true)
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ chat/image=${W_IMG} capability_missing=${W_REASON} kb_match=${W_FAST}（期望都 ≥1）"
[ "$W_IMG" != "0" ] || { echo "ABORT=3 缺特征串 /api/chat/image（bug8 路由）⇒ 编译的不是含本批改动的树"; exit 3; }
[ "$W_REASON" != "0" ] || { echo "ABORT=3 缺特征串 capability_missing（bug8 降级分档）"; exit 3; }
[ "$W_FAST" != "0" ] || { echo "ABORT=3 缺特征串 kb_match（bug6/7 流水线步骤名）"; exit 3; }
TAR_SHA=$(sha_local "$TAR")
BIN_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋打包＋自检）；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
  echo "提示：本批还需单独跑 deploy/patch_caddy_security_headers.sh 落 Caddy 安全头（bug3/4/5）。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位）====="
scp -P "$PORT" "$TAR" "root@$HOST:/tmp/fe-$TAG.tar.gz" || { echo "ABORT=4 scp tar 失败"; exit 4; }
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=4 scp server 失败"; exit 4; }
scp -P "$PORT" "$SWAP" "root@$HOST:/tmp/swap_web_and_binary.sh" || { echo "ABORT=4 scp swap 脚本失败"; exit 4; }

echo "===== P2 服务器侧换源＋换件（常设执行体：两段式，两站全绿才落位）====="
ssh -p "$PORT" "root@$HOST" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
exit "$rc"
