#!/usr/bin/env bash
# release_20261019b.sh — 2026-10-19 第二批（工单 107 图片原位翻译四处缺陷）
#
# 本批内容（权威源＝本轮实测探针读数；PROGRESS §三十五 续）：
#   · ① 提示词回声入图：`gates.go` 重译腿采纳条件补 `IsTranslationUsable`（指令回显过闸也不许采纳）。
#   · ② 字号忽大忽小：`ocr_translate_image.py` 的 `_line_ink_height` 在剔列之后再剔**整行全墨**
#     （行内分隔线/图框被当成字形高度 ⇒ 字阶按它算）。
#   · ③ 幽灵内容：渲染循环里"无墨且文本等于源文本"的行跳过（不再输出看不见的占位），
#     出 INFO 行 `painted=/overflow=/blank_skipped=`；一行都没画上仍按 `NothingPainted` 判失败。
#   · ④ 中文泄露（12 段未译出）：`engine.go` 新增极简指令档（WithMinimalInstruction）
#     ＋`file.go` 的 `translateSegmentMinimal` 作为硬闸**逐段兜底末段腿**；
#     `translation_guard.go` 判据 3′＝无中文源回吐视为原文交付（去掉无中文碎片的假告警）。
#   · **internal/fileproc 的 *.py 本批有改动**（ocr_translate_image.py、capability_shape_selftest.py 自证腿）
#     ⇒ **必须**跑 scripts/sync_fileproc_assets.sh（P1.5），否则线上二进制有新 Go 腿、磁盘上却是旧 py。
#   · 前端：**零改动**（本批只动后端与 fileproc 资产）；Caddy：**零改动**。
#
# 中止线（都在动手之前）：
#   ① 本机 tar 过不了 scripts/check_frontend_tar.sh（含 T8 入口体积哨兵）⇒ 不上传；
#   ② 本机新件缺特征串（证明编译的是本工作树 HEAD）⇒ 不上传；
#   ③ 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何东西；
#   ④ fileproc 资产同步指纹不等值 ⇒ 不换件（P1.5 在 P2 之前）。
#
# ⚠️ 特征串一律取**运行期真会被引用**的字面量（AGENTS §三：`-ldflags="-s -w"` 之下函数名被内联掉不进名字表）：
#   本批＝file.go 的 `MINIMAL_INSTR_SEG_RESCUE_V1`（极简腿救回一段时随观测日志真被引用）
#   与其日志字面量 `文件翻译：极简指令兜底腿救回未译段`；另保 10-19 批三串作形态对照。
#   等值锁＝internal/engine/release_marker_gate_test.go 的 TestMinimalInstrRescueMarkerMatchesReleaseScript（三枚反证）。
#
# 用法：bash deploy/release_20261019b.sh            # 干跑（本机编译＋打包＋自检，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261019b.sh    # 真换（sync 资产 → scp → swap → 复验）
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261019b"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"
SYNC="$REPO/scripts/sync_fileproc_assets.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
USER="$(sed -n 's/^REMOTE_USER="${DEPLOY_USER:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] && [ -n "$USER" ] || { echo "ABORT=1 主机/端口/用户现读失败（读 ${SRC}）"; exit 2; }

# ★《部署指南》§五：生产 sshd 在 28022、密钥是 ~/.ssh/id_ed25519。
#   裸 `ssh root@<IP>` 靠 ~/.ssh/config 的 Host 档能通，但**脚本自己显式传 -i/-P/-p 会覆盖 config**
#   ⇒ 凡是显式传端口的调用都必须同时显式给密钥（`~/` 在词位不展开，写 $HOME）。
#   sync_fileproc_assets.sh 的 SSH_KEY 默认是 ~/.ssh/id_rsa，不对 ⇒ 下面两处显式传 $HOME/.ssh/id_ed25519。
SSH_KEY_PATH="$HOME/.ssh/id_ed25519"
[ -r "$SSH_KEY_PATH" ] || { echo "ABORT=1 读不到密钥 ${SSH_KEY_PATH}（《部署指南》§五）"; exit 2; }

# ★ 演示单元的**真域名是 rox-test.lexicorn.cn**；`demo.lexicorn.cn` 没有独立站点块，
#   落进 *.lexicorn.cn 通配块被反代到 8787（主站）——拿它验"演示站"会读到主站（《部署指南》§十 备忘④）。
MAIN_DOMAIN="${MAIN_DOMAIN:-langcross.lexicorn.cn}"
DEMO_DOMAIN="${DEMO_DOMAIN:-rox-test.lexicorn.cn}"

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

echo "----- P0-E 产物 HTML 形态（本批 index.html 源未改，精简器仍须在位）-----"
OUT_HTML="$REPO/frontend-react/dist/index.html"
OUT_BYTES=$(wc -c <"$OUT_HTML" | tr -d ' ')
COMMENTS=$(LC_ALL=C grep -c -- '<!--' "$OUT_HTML" || true)
ENTRY=$(LC_ALL=C grep -oE 'assets/index-[A-Za-z0-9_-]{8,}\.js' "$OUT_HTML" | head -1)
echo "产物 index.html=${OUT_BYTES} B ｜ 注释块=${COMMENTS}（期望 0）｜ 入口=${ENTRY}"
[ "$COMMENTS" = "0" ] || { echo "ABORT=2 产物 HTML 还带注释 ⇒ 精简插件没生效"; exit 2; }
[ -n "$ENTRY" ] || { echo "ABORT=2 产物 HTML 找不到形如 assets/index-<hash>.js 的入口"; exit 2; }
for anchor in 'id="__boot_skeleton"' 'class="lc-boot-cover"' 'id="root"' '<noscript>'; do
  n=$(LC_ALL=C grep -c -F -- "$anchor" "$OUT_HTML" || true)
  echo "  产物锚点 $anchor=${n}"
  [ "$n" != "0" ] || { echo "ABORT=2 产物 HTML 丢了标记 $anchor ⇒ 精简器咬坏了页"; exit 2; }
done

echo "----- P0-T 交付包自检（与 deploy_to_production.sh、手工链共用同一把尺子）-----"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过（含 T8 入口体积哨兵）⇒ 不许换源"; exit 2; }

echo "===== P0-B 后端件（本批 engine ＋ fileproc 资产，必须换）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 特征串证明"编译的是本工作树 HEAD"。本批主串＝极简腿常量与其日志字面量（运行期真被引用）；
# ⚠️ 一律带 LC_ALL=C：macOS/BSD grep 多字节 locale 下拿中文模式可能恒 0 命中「假红」（17c 首跑真踩）。
# ⚠️ 不许拿**函数名**当特征串（10-18 首跑真踩）：`-ldflags="-s -w"` 之下内联掉的函数不进名字表。
#    等值锁＝internal/engine/release_marker_gate_test.go 的 TestMinimalInstrRescueMarkerMatchesReleaseScript。
W_MIN=$(LC_ALL=C grep -a -c -F -- "MINIMAL_INSTR_SEG_RESCUE_V1" "$SERVER_BIN" || true)
W_MINLOG=$(LC_ALL=C grep -a -c -F -- "文件翻译：极简指令兜底腿救回未译段" "$SERVER_BIN" || true)
W_PREFILL=$(LC_ALL=C grep -a -c -F -- "PREFILL_IDENTITY_SEGMENTS_V1" "$SERVER_BIN" || true)
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ minimal=${W_MIN} minimal_log=${W_MINLOG} prefill=${W_PREFILL}（期望都 ≥1）"
[ "$W_MIN" != "0" ] || { echo "ABORT=3 缺特征串 MINIMAL_INSTR_SEG_RESCUE_V1（极简指令兜底腿）⇒ 编译的不是含本批改动的树"; exit 3; }
[ "$W_MINLOG" != "0" ] || { echo "ABORT=3 缺特征串「文件翻译：极简指令兜底腿救回未译段」⇒ 常量失去运行期引用点"; exit 3; }
[ "$W_PREFILL" != "0" ] || { echo "ABORT=3 缺特征串 PREFILL_IDENTITY_SEGMENTS_V1（形态对照腿）"; exit 3; }

TAR_SHA=$(sha_local "$TAR")
BIN_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋打包＋自检）；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位）====="
scp -i "$SSH_KEY_PATH" -P "$PORT" "$TAR" "$USER@$HOST:/tmp/fe-$TAG.tar.gz" || { echo "ABORT=4 scp tar 失败"; exit 4; }
scp -i "$SSH_KEY_PATH" -P "$PORT" "$SERVER_BIN" "$USER@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=4 scp server 失败"; exit 4; }
scp -i "$SSH_KEY_PATH" -P "$PORT" "$SWAP" "$USER@$HOST:/tmp/swap_web_and_binary.sh" || { echo "ABORT=4 scp swap 脚本失败"; exit 4; }

echo "===== P1.5 fileproc 资产同步（本批 *.py 有改动；在换件之前，指纹不等值即停手）====="
SSH_HOST="$USER@$HOST" SSH_PORT="$PORT" SSH_KEY="$SSH_KEY_PATH" MODE=remote DEST_ROOT=/opt/translator bash "$SYNC" \
  || { echo "ABORT=4 主站 fileproc 资产同步失败（指纹不等值）⇒ 不换件"; exit 4; }
SSH_HOST="$USER@$HOST" SSH_PORT="$PORT" SSH_KEY="$SSH_KEY_PATH" MODE=remote DEST_ROOT=/opt/translator-demo bash "$SYNC" \
  || { echo "ABORT=4 演示站 fileproc 资产同步失败 ⇒ 不换件"; exit 4; }

echo "===== P2 服务器侧换源＋换件（常设执行体：两段式，两站全绿才落位；换完 restart 两个单元）====="
ssh -i "$SSH_KEY_PATH" -p "$PORT" "$USER@$HOST" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
[ "$rc" = "0" ] || exit "$rc"

echo "===== P3 换后复验（只读；由消费方自己读出值，不数日志）====="
sha_out() { shasum -a 256 "$1" | awk '{print $1}'; }
fails=0
bump() { echo "FAIL $*"; fails=$((fails + 1)); }

for u in translator translator-demo; do
  st=$(ssh -i "$SSH_KEY_PATH" -p "$PORT" "$USER@$HOST" "systemctl is-active $u" 2>/dev/null | tr -d '\r')
  echo "unit $u is-active=${st}"
  [ "$st" = "active" ] || bump "$u 不是 active（is-active=${st}）"
done

H_MAIN=$(curl -s -m 20 -k "https://$MAIN_DOMAIN/api/health" || true)
H_DEMO=$(curl -s -m 20 -k "https://$DEMO_DOMAIN/api/health" || true)
echo "主站 health($MAIN_DOMAIN): $(echo "$H_MAIN" | cut -c1-220)"
echo "演示 health($DEMO_DOMAIN): $(echo "$H_DEMO" | cut -c1-160)"
echo "$H_MAIN" | LC_ALL=C grep -q '"asr_engine"' || bump "$MAIN_DOMAIN /api/health 缺 asr_engine（G12）"
echo "$H_DEMO" | LC_ALL=C grep -q '"asr_engine"' || bump "$DEMO_DOMAIN /api/health 缺 asr_engine（G12）"

# 首页入口名必须等于本机 dist 里那个（结构上证明换了源）
LOCAL_ENTRY=$(LC_ALL=C grep -oE 'assets/index-[A-Za-z0-9_-]{8,}\.js' "$OUT_HTML" | head -1)
for d in "$MAIN_DOMAIN" "$DEMO_DOMAIN"; do
  body=$(curl -s -m 20 -k "https://$d/" || true)
  n=$(printf '%s' "$body" | LC_ALL=C grep -c -F -- "id=\"root\"" || true)
  e=$(printf '%s' "$body" | LC_ALL=C grep -oE 'assets/index-[A-Za-z0-9_-]{8,}\.js' | head -1)
  brand=$(printf '%s' "$body" | LC_ALL=C grep -c -F -- "__BRANDING__" || true)
  echo "$d: root=${n} 入口=${e} __BRANDING__=${brand}（本机入口=${LOCAL_ENTRY}）"
  [ "$n" != "0" ] || bump "$d 首页没有 #root（首屏坏了）"
  [ "$e" = "$LOCAL_ENTRY" ] || bump "$d 首页入口=${e} ≠ 本机 dist ${LOCAL_ENTRY}（源没换成）"
  [ "$brand" != "0" ] || bump "$d 首页缺 __BRANDING__ 注入（走了静态直出？查 Caddy 兜底段）"
done

# G6 判据：不存在的路径必须真 404（不是 SPA 兜底 200）
code=$(curl -s -m 20 -k -o /dev/null -w '%{http_code}' "https://$MAIN_DOMAIN/__no_such_path_$TAG" || true)
echo "/__no_such_path 状态码=${code}（期望 404）"
[ "$code" = "404" ] || bump "不存在路径状态码=${code} ≠ 404（G6 未生效）"

# 磁盘 dotfile 复验（零 .DS_Store）
ds=$(ssh -i "$SSH_KEY_PATH" -p "$PORT" "$USER@$HOST" "find /opt/translator/web /opt/translator-demo/web -name '.DS_Store' | wc -l" 2>/dev/null | tr -d ' \r')
echo "两站 web 根 .DS_Store 计数=${ds}（期望 0）"
[ "$ds" = "0" ] || bump ".DS_Store 计数=${ds} ≠ 0"

echo "RELEASE_FAIL=$fails"
[ "$fails" = "0" ] || { echo "RELEASE_ABORT=1 复验有 FAIL ⇒ 见上面"; exit 5; }
echo "RELEASE_ALL_OK=1 tag=$TAG entry=$LOCAL_ENTRY"
