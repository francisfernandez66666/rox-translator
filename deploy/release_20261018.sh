#!/usr/bin/env bash
# release_20261018.sh — 2026-10-18 批次（#4 工单文件元评论污染修复 ＋ #1 国内首屏瘦身）
#
# 本批内容：
#   · 后端（internal/engine）：**文件翻译链的译文可用性判定加固**——translation_guard.go 新增三条结构判据
#     （非语言段必须原样交付／实义词退化成孤立功能词／译文引号自述解释源词）＋「无需翻译段」不再进上游
#     （identitySegments 预填），file.go 的三个派发点统一改查 preFilled 并集。
#     现网症状＝工单交付 md 里整排数字段被换成 `and`、`<` 被翻成一整句方法论、`配件` 后面挂英文自述句。
#   · 后端（internal/api）：spa.go 给入口 HTML 加 `Link: rel=preload` 关键资源头（#1 慢链路）。
#   · 前端：产物 index.html 精简（删注释／压内联哨兵脚本／折叠标签间空白），
#     线上 22,656 B → 9,456 B（br 7,609 → 3,087 B）。
#   · Caddy：本批**零改动**（Link 头由后端发、Caddy 原样透传；F-46 的 HTML no-cache 决策不动）。
#   · internal/fileproc 的 *.py 与 assets：本批零改动 ⇒ **不需要 SYNC_ASSETS**。
#
# 中止线（都在动手之前）：
#   ① 本机 tar 过不了 `scripts/check_frontend_tar.sh`（含 T8 入口体积哨兵）⇒ 不上传；
#   ② 产物 index.html 没吃到精简（还带注释／体积没掉）⇒ 不上传（本批前端改动就在这一个文件里）；
#   ③ 本机新件缺特征串（证明编译的是本工作树 HEAD）⇒ 不上传；
#   ④ 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何东西。
#
# ⚠️ 与上一批 release_20261017c.sh 的**唯一形态差异**：那批的"吃到改动"证据是**入口 JS 换名**，
#   本批前端改动只在 index.html（入口 JS 内容一字未变、哈希不变）⇒ 换成
#   「产物 HTML 无注释 ＋ 体积跌破源码的一半」这条证据。**别把 17c 的入口换名判据照抄过来**：
#   它会因为 index-PpbXDXsp.js 与线上同名而直接 ABORT（假红）。
#
# 用法：bash deploy/release_20261018.sh            # 干跑（本机编译＋打包＋全部自检，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261018.sh    # 真换（scp → swap → 复验）
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261018"

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
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
rm -f "$TAR"
(
  cd "$REPO/frontend-react/dist" &&
    COPYFILE_DISABLE=1 tar --exclude='.DS_Store' --exclude='._*' --exclude='./wasm/sherpa-onnx/*.data' -czf "$TAR" .
) || { echo "ABORT=2 打包失败"; exit 2; }
echo "tar=$TAR size=$(wc -c <"$TAR" | tr -d ' ') sha=$(sha_local "$TAR" | cut -c1-16)"

echo "----- P0-E 产物 HTML 必须吃到精简（本批前端改动就在这一个文件里）-----"
SRC_HTML="$REPO/frontend-react/index.html"
OUT_HTML="$REPO/frontend-react/dist/index.html"
SRC_BYTES=$(wc -c <"$SRC_HTML" | tr -d ' ')
OUT_BYTES=$(wc -c <"$OUT_HTML" | tr -d ' ')
COMMENTS=$(LC_ALL=C grep -c -- '<!--' "$OUT_HTML" || true)
echo "源码 index.html=${SRC_BYTES} B ｜ 产物=${OUT_BYTES} B ｜ 产物内注释块=${COMMENTS}（期望 0）"
[ "$COMMENTS" = "0" ] || { echo "ABORT=2 产物 HTML 还带注释 ⇒ 精简插件没生效（检查 vite.config.ts 的 artifact-html-minify）"; exit 2; }
[ "$SRC_BYTES" -gt 15000 ] || { echo "ABORT=2 源码 index.html 只有 ${SRC_BYTES} B（判据前提变了：注释本就很少？）"; exit 2; }
[ "$OUT_BYTES" -lt 12000 ] || { echo "ABORT=2 产物 HTML=${OUT_BYTES} B 未跌破 12,000 ⇒ 精简没吃满"; exit 2; }
# 语义标记一条都不许丢（丢一个首屏就坏）
for anchor in 'id="__boot_skeleton"' 'class="lc-boot-cover"' 'id="root"' '<noscript>' 'assets/index-'; do
  n=$(LC_ALL=C grep -c -F -- "$anchor" "$OUT_HTML" || true)
  echo "  产物锚点 $anchor=${n}"
  [ "$n" != "0" ] || { echo "ABORT=2 产物 HTML 丢了标记 $anchor ⇒ 精简器咬坏了页"; exit 2; }
done

echo "----- P0-T 交付包自检（与 deploy_to_production.sh、手工链**共用同一把尺子**）-----"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过（含 T8 入口体积哨兵）⇒ 不许换源"; exit 2; }

echo "===== P0-B 后端件（本批 engine 判定加固 ＋ spa.go 预加载头，必须换）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 特征串证明"编译的是本工作树 HEAD"：本批两处形状必须都在件里。
# ⚠️ 必须带 LC_ALL=C：macOS/BSD grep 多字节 locale 下拿中文模式**恒 0 命中**（17c 首跑真踩）。
# ⚠️ #4 那一侧不许拿**函数名**当特征串（首跑真踩）：`-ldflags="-s -w"` 之下内联掉的函数不进名字表，
#   同一棵树同一个件里 degenerateToFunctionWord 命中 1、identityDelivery 命中 0 ⇒ 会把编好的件判成"编错树"。
#   用的是 engine 侧**被真实引用**的常量 identityPrefillMarker 与其日志字面量，与这里**逐字等值**
#   （等值锁＝backend-go/internal/engine/release_marker_gate_test.go 的
#    TestIdentityPrefillMarkerMatchesReleaseScript，含三枚反证）。
W_LINK=$(LC_ALL=C grep -a -c -F -- "rel=preload; as=script; crossorigin" "$SERVER_BIN" || true)
W_PREFILL=$(LC_ALL=C grep -a -c -F -- "PREFILL_IDENTITY_SEGMENTS_V1" "$SERVER_BIN" || true)
W_PRELOG=$(LC_ALL=C grep -a -c -F -- "文件翻译：无需翻译段按原文预填交付" "$SERVER_BIN" || true)
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ preload=${W_LINK} prefill=${W_PREFILL} prefill_log=${W_PRELOG}（期望都 ≥1）"
[ "$W_LINK" != "0" ] || { echo "ABORT=3 缺特征串「rel=preload; as=script; crossorigin」⇒ 编译的不是含本批改动的树"; exit 3; }
[ "$W_PREFILL" != "0" ] || { echo "ABORT=3 缺特征串 PREFILL_IDENTITY_SEGMENTS_V1（#4 原文预填）"; exit 3; }
[ "$W_PRELOG" != "0" ] || { echo "ABORT=3 缺预填日志字面量（#4 预填没走到运行期引用点）"; exit 3; }

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
[ "$rc" = "0" ] || exit "$rc"

echo "⚠️ 本批 internal/fileproc 的 *.py 与 assets 零改动 ⇒ 不跑 sync_fileproc_assets.sh（跑了也只是指纹等值）"
echo "RELEASE_ALL_OK=1 tag=$TAG"
