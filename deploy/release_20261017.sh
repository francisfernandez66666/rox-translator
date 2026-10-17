#!/usr/bin/env bash
# release_20261017.sh — 首屏轻量化批 C＋A＋B 上线（前端换源为主，后端同批同换一台件）
#
# 本批内容（用户令：官网/公开面首屏太慢，慢链路 6–20 KB/s 也要尽快打开）：
#   C 首屏骨架：index.html 里一块纯黑占位（`.lc-boot-cover` + `.lc-boot-spin`），入口分片到手即撤；
#   A i18n 面板懒化：30 个后台面板词条从入口包拆成 appPanels-*.js（入口 617,170 → 470,280 B）；
#   B 公开面拆入口：未登录且落在公开五路径（/、/pricing、/compare、/login、/register）走 PublicRoot
#     （**不挂 react-router、不挂工作台/后台静态依赖**），其余（含 /admin 未登录）仍走 App。
#     现读数：入口 **339,296 B raw / 93,016 B br**（改前 617,170／163,165）。
#
# 本批改的都是前端产物：**后端一个字节没动**（HEAD 的 backend-go 与线上同代）。
# 那道「后端也换一台件」的意义不是"修了什么"，而是：
#   ① 让 `swap_web_and_binary.sh` 的既有契约（四参数、两段式）原样成立，不为前端批现攒一次性命令；
#   ② 换上去的是**本工作树 HEAD** 的后端（下方 P0-B 用特征串证明它含最近一批后端改动），
#      避免"web 是新代、后端是更早一代"这种无人看得见的混合态。
#
# 中止线（都在动手之前）：
#   ① 本机 tar 过不了 `scripts/check_frontend_tar.sh`（含 **T8 入口 raw ≤420,000 B** 这条本批新加的拆分层哨兵）⇒ 不上传；
#   ② 本机新件里特征串缺失 ⇒ 不上传；
#   ③ 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何东西。
# 落位一律交给常设执行体 `swap_web_and_binary.sh`（它自带根路径两道守卫、两段式、ETXTBSY 规避）。
#
# 用法：bash deploy/release_20261017.sh            # 干跑（本机编译＋打包＋全部自检，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261017.sh    # 真换（scp → swap → 复验）
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261017"

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
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过（含 T8 入口拆分层哨兵）⇒ 不许换源"; exit 2; }

echo "===== P0-B 后端件（本批零改动，仍按契约换一台同代件）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 特征串证明"编译的是本工作树 HEAD"：最近一批后端改动（API Key 平台作用域）必须在件里。
W_SCOPE=$(grep -a -c -F -- "apiKeyIDScope" "$SERVER_BIN" || true)
W_MEDIA=$(grep -a -c -F -- "ocr_recognize" "$SERVER_BIN" || true)
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ apiKeyIDScope=${W_SCOPE} ocr_recognize=${W_MEDIA}（期望都 ≥1）"
[ "$W_SCOPE" != "0" ] || { echo "ABORT=3 缺特征串 apiKeyIDScope ⇒ 编译的不是含最近后端改动的树"; exit 3; }
[ "$W_MEDIA" != "0" ] || { echo "ABORT=3 缺特征串 ocr_recognize（承重·能力资产那条腿）"; exit 3; }
TAR_SHA=$(sha_local "$TAR")
BIN_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋打包＋自检）；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
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
