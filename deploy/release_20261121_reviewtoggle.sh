#!/usr/bin/env bash
# release_20261121_reviewtoggle.sh — 复核链路超管开关 + 同步文件通道写超时豁免 上线
#
# 本批内容（2026-11-21）：
#   ① 复核链路运营开关：`ops_policy` 新增 `quality` 段（平台级、仅超管可写），
#      两个独立布尔 `instant_review_enabled` / `ticket_review_enabled`，默认 true。
#      作用域只有一处：入口在 ctx 打标（工单链 `engine.ReviewScopeTicket`），引擎内各复核腿按 ctx 取档。
#   ② 同步文件翻译通道 `/api/translate` 清写超时：真值 tv6 实测 199 行 / 15.6 KB 跑了 2,150 s，
#      而全站 `http.Server.WriteTimeout` 是 30 分钟 ⇒ 产物写完、回执写不出去（curl 000）。
#   ③ 存量红两处（都不是本批引入，但都在本批闸门里现形，顺手收口）：
#      · `salesOps.opsAllowOn` 把批次当互斥档位 ⇒ 抬到 W6 后 W5 三项永远显示「半套」；
#      · `langcrossUiConformance` 的目录枚举把 `.dom.test.tsx` 当生产页面 ⇒ 锁恒红。
#
# 中止线（都在动手之前）：
#   ① 本机 tar 过不了 `scripts/check_frontend_tar.sh` ⇒ 不上传；
#   ② 本机新件里特征串缺失 ⇒ 不上传（用**运行期真的会引用的字符串字面量**判，
#      不用函数名：`-ldflags="-s -w"` 下内联掉的函数名不进名字表，那会给假红）；
#   ③ 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何东西。
# 落位一律交给常设执行体 `swap_web_and_binary.sh`。
#
# 用法：bash deploy/release_20261121_reviewtoggle.sh            # 干跑（编译＋打包＋全部自检，不碰服务器）
#       APPLY=1 bash deploy/release_20261121_reviewtoggle.sh    # 真换（scp → swap → 复验）
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261121review"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 前端构建 → 侧车 → 打包（顺序只有一种：build → precompress → tar → 自检）====="
(cd "$REPO/frontend-react" && npx vite build) || { echo "ABORT=2 vite build 失败"; exit 2; }
# ★ 构建会把 `public/.DS_Store` 原样拷进 dist（Vite 复制 public 全目录）。
#   不在这里清掉，预压缩脚本会直接 ABORT=3（本批首跑真踩），而"删一次 dist"只是治了这一次。
find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
rm -f "$TAR"
(
  cd "$REPO/frontend-react/dist" &&
    COPYFILE_DISABLE=1 tar --exclude='.DS_Store' --exclude='._*' --exclude='./wasm/sherpa-onnx/*.data' -czf "$TAR" .
) || { echo "ABORT=2 打包失败"; exit 2; }
echo "tar=$TAR size=$(wc -c <"$TAR" | tr -d ' ') sha=$(sha_local "$TAR" | cut -c1-16)"

echo "----- P0-T 交付包自检（与 deploy_to_production.sh、手工链共用同一把尺子）-----"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过 ⇒ 不许换源"; exit 2; }

echo "===== P0-B 后端件 ====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 特征串一律取**运行期真会引用的字符串字面量**（JSON 标签 / 日志前缀 / 配置键），不取函数名。
W_INST=$(grep -a -c -F -- "instant_review_enabled" "$SERVER_BIN" || true)
W_TICK=$(grep -a -c -F -- "ticket_review_enabled" "$SERVER_BIN" || true)
W_CTX=$(grep -a -c -F -- "[file-ctx-retry]" "$SERVER_BIN" || true)
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ instant_review_enabled=${W_INST} ticket_review_enabled=${W_TICK} file-ctx-retry=${W_CTX}（期望都 ≥1）"
[ "$W_INST" != "0" ] || { echo "ABORT=3 缺特征串 instant_review_enabled ⇒ 编译的不是含本批复核开关的树"; exit 3; }
[ "$W_TICK" != "0" ] || { echo "ABORT=3 缺特征串 ticket_review_enabled ⇒ 同上"; exit 3; }
[ "$W_CTX" != "0" ] || { echo "ABORT=3 缺特征串 [file-ctx-retry]（上下文重译腿）"; exit 3; }
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
