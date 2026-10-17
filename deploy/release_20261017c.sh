#!/usr/bin/env bash
# release_20261017c.sh — 2026-10-17 深夜第二批（图片原位回填修复 + 语音引擎诚实探测，两站同批）
#
# 本批内容（承接 release_20261017b 的八项报障批，同一晚的第二轮报障）：
#   · 后端+脚本：**图片原位回填**（工单图片件与即时翻译图片共用的 RenderTranslatedImage 那一腿）
#     ——ocr_translate_image.py 全量重写：框判据收四值矩形（现役 ocr_recognize.py 的产出形态）与
#       八值四点两种、逐字选字体（随包 CJK 字体画英文字母＝豆腐块，改走「系统字体链 + 逐字回落」）、
#       一行都没画上时判失败（ERR:NothingPainted）而不是把重编码的原图当成功产物交出去；
#       ocr.go/ocr_image.go 同步：失败档位翻人话、原位回填缓存键与提取器同形（trim）。
#   · 前端：sherpa-onnx 端侧 ASR 的**可用性探测诚实化**——旧形态只看 workflow 能不能 200，
#       而 199 MB 的 `.data` 权重不在仓库里 ⇒ 探测报"可用"、真点下去失败；现按**资产体积+类型**判，
#       缺权重即回落服务端引擎（麦克风仍可用，相对现状是修复而非回归）。
#   · Caddy：登录前页面 CSP 放开三条（'unsafe-eval' / connect-src data: / worker-src blob:）——
#     **不由本脚本落地**，由同批 `deploy/patch_caddy_security_headers.sh` 单独做（本脚本全程不碰 caddy）。
#
# 后果：前端 dist 与 translator-server **两件都变了**，且 `internal/fileproc/*.py` 必须同步到
#   两站的 `/opt/translator[-demo]/bin/`（Python 脚本不在二进制里，换件不带它）。
#   translator-assist 零改动 ⇒ 不换；extension/、sdk/ 零改动 ⇒ 不重打。
#
# 中止线（都在动手之前）：
#   ① 本机 tar 过不了 `scripts/check_frontend_tar.sh`（含 T8 入口体积哨兵）⇒ 不上传；
#   ② 本机新件缺特征串（证明编译的是本工作树 HEAD）⇒ 不上传；
#   ③ 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何东西。
# 落位一律交给常设执行体 `swap_web_and_binary.sh`（自带根路径两道守卫、两段式、ETXTBSY 规避）。
#
# 用法：bash deploy/release_20261017c.sh            # 干跑（本机编译＋打包＋全部自检，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261017c.sh    # 真换（scp → swap → 复验）
#       APPLY=1 SYNC_ASSETS=1 bash deploy/release_20261017c.sh   # 追加：把 fileproc 的 *.py/assets 同步到两站
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
SYNC_ASSETS="${SYNC_ASSETS:-0}"
TAG="20261017c"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

# 上一批（release_20261017b）落在两站的入口名——本批必须换名，否则说明构建没吃到源码改动。
PREV_ENTRY="assets/index-C7_FzA-r.js"

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

echo "----- P0-E 入口名必须换（证 dist 吃到了本批前端改动；同 §一·5「通知浮层只改分片时入口哈希会变」）-----"
NEW_ENTRY="$(cd "$REPO/frontend-react/dist" && ls -1 assets/index-*.js 2>/dev/null | head -1)"
[ -n "$NEW_ENTRY" ] || { echo "ABORT=2 dist 里没有入口包 assets/index-*.js ⇒ 构建产物形状不对"; exit 2; }
echo "入口：旧=$PREV_ENTRY 新=$NEW_ENTRY"
[ "$NEW_ENTRY" != "$PREV_ENTRY" ] || {
  echo "ABORT=2 入口名与上一批**逐字相同** ⇒ 这次构建没吃到前端改动（改的是测试/文档？）"
  exit 2
}

echo "----- P0-T 交付包自检（与 deploy_to_production.sh、手工链**共用同一把尺子**）-----"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过（含 T8 入口体积哨兵）⇒ 不许换源"; exit 2; }

echo "===== P0-B 后端件（本批含 internal/fileproc 的 Go 侧改动，必须换）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 特征串证明"编译的是本工作树 HEAD"：本批两处形状必须在件里（少一条就是编了别的树）。
# ⚠️ 必须带 LC_ALL=C：macOS/BSD grep 在多字节 locale 下拿中文模式**恒 0 命中**
#   （本批首跑真踩：同一个二进制用 python 数得到 1、用 grep 数得到 0 ⇒ 会把自己编译好的件判成"编错了树"）。
W_NP=$(LC_ALL=C grep -a -c -F -- "图片原位回填未产出" "$SERVER_BIN" || true)
W_FONT=$(LC_ALL=C grep -a -c -F -- "系统字体候选也取不到" "$SERVER_BIN" || true)
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ nothing_painted=${W_NP} font_chain=${W_FONT}（期望都 ≥1）"
[ "$W_NP" != "0" ] || { echo "ABORT=3 缺特征串「图片原位回填未产出」（NothingPainted 档位）⇒ 编译的不是含本批改动的树"; exit 3; }
[ "$W_FONT" != "0" ] || { echo "ABORT=3 缺特征串「系统字体候选也取不到」（字体链档位）"; exit 3; }

echo "----- P0-P 图片回填脚本必须带本批三处形状（换二进制不带 .py，脚本另经 sync_fileproc_assets.sh 落盘）-----"
IPY="$REPO/backend-go/internal/fileproc/ocr_translate_image.py"
for pat in '_valid_box' '_pick_font' 'NothingPainted'; do
  n=$(LC_ALL=C grep -a -c -F -- "$pat" "$IPY" || true)
  echo "  $pat=${n}"
  [ "$n" != "0" ] || { echo "ABORT=3 脚本缺 $pat ⇒ 不是本批重写后的版本"; exit 3; }
done

TAR_SHA=$(sha_local "$TAR")
BIN_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋打包＋自检）；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
  echo "提示：本批还需 ① deploy/patch_caddy_security_headers.sh（CSP 三条）② SYNC_ASSETS=1（fileproc 脚本落两站）。"
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

echo "===== P3 fileproc 脚本与资产同步（两站各一次；SYNC_ASSETS=1 才做）====="
if [ "$SYNC_ASSETS" != "1" ]; then
  echo "SKIP P3：未开 SYNC_ASSETS=1。⚠️ 只换二进制**不带** .py ⇒ 图片回填仍是旧脚本，验收时会看不到本批修复。"
  echo "RELEASE_DONE_NO_ASSETS=1 tag=$TAG"
  exit 0
fi
KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519}"
for root in /opt/translator /opt/translator-demo; do
  echo "----- 同步 → $root -----"
  MODE=remote SSH_HOST="root@$HOST" SSH_KEY="$KEY" SSH_PORT="$PORT" DEST_ROOT="$root" \
    bash "$REPO/scripts/sync_fileproc_assets.sh" || { echo "ABORT=5 $root 同步失败（指纹没等值就是没落地）"; exit 5; }
done
echo "⚠️ 权重/脚本刚落盘不会让就绪位自己翻：CheckHealth 是 sync.Once，需 restart 两站单元（P2 已重启过一次，"
echo "   若同步在重启之后，就绪位读数可能仍是旧的——本批脚本不 restart，避免与 P2 的停站窗口叠加）。"
echo "RELEASE_ALL_OK=1 tag=$TAG"
