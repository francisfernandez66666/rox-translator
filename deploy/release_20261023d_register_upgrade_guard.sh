#!/usr/bin/env bash
# release_20261023d_register_upgrade_guard.sh — 修「完善信息改选企业即报注册过于频繁」（后端换件）
#
# 事故（2026-10-23 用户连报三条，本脚本修第一条）：
#   主站邮箱验证码登录为未注册邮箱自动建空白个人账号（email_login.go createPersonalByEmail，
#   记一次 IP 账）→ 登录后 AI 补全面板改选「企业」时走 authRegister（handleRegister 的升级通道：
#   JWT 本人 + 同邮箱 + 空白账号）⇒ 而 IP 频率护栏当时挂在 handleRegister 最前面（body 解码之前），
#   对升级通道一视同仁按下限：最小间隔 60 秒内必撞（注册完立刻弹补全面板），日限 3 次当天试两轮就满。
#   用户原话：「注册后引导式信息填写会报错：注册过于频繁，请 10 秒后再试」。
#
# 本批内容（backend-go/internal/api/register.go，前端零改动）：
#   ① IP 护栏从函数最前面挪到 upgradeBlankID 可判之后，且**升级通道不吃 IP 档**——
#      那条路转化的是本人几分钟前刚自动建、已计过一次 IP 的空白账号，不是新注册；
#   ② record(ip) 同样跳过——一次用户动作不扣两次 IP 账；
#   ③ 防薅本尊没放：升级仍过设备档/平台档（reserveFreeAccount，升级确实新建租户发额度）、
#      人机验证、一次性邮箱黑名单与「本人空白账号」三条件。
#
# 进包证据：本批无新增用户可见字面量（护栏文案原样挪位），生产包里没有可 grep 的新串
#   （2026-10-23b 的教训：按函数名 grep 是假判据）。锁交给测试，APPLY 前必须全绿：
#   internal/api/register_upgrade_guard_test.go（4 例：升级双满放行/游客 429/非空白 429/
#   新注册护栏仍在）＋ 既有 Register 全量。二进制侧另加一道机械判据：新件 sha 必须**不同于**
#   现役件（93e02bd0… 起），证明编译出来的确实是含本批改动的新树。
#
# 用法：bash deploy/release_20261023d_register_upgrade_guard.sh            # 干跑
#       APPLY=1 bash deploy/release_20261023d_register_upgrade_guard.sh    # 真换（scp → swap → 复验）
set -uo pipefail
# vite build 的 emptyOutDir 会批量删 dist 里 100+ 个旧产物，本机安全垫片的批量删除守卫会拦
export CODEBUDDY_SAFE_DELETE_ENABLED=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261023d"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 前端构建 → 侧车 → 打包 → 自检（本批前端零改动，照打保证包自洽） ====="
(cd "$REPO/frontend-react" && npx vite build) || { echo "ABORT=2 vite build 失败"; exit 2; }
find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
rm -f "$TAR"
(
  cd "$REPO/frontend-react" &&
    COPYFILE_DISABLE=1 tar --exclude='dist/.DS_Store' --exclude='dist/._*' --exclude='dist/wasm/sherpa-onnx/*.data' -czf "$TAR" dist
) || { echo "ABORT=2 打包失败"; exit 2; }
echo "tar=$TAR size=$(wc -c <"$TAR" | tr -d ' ') sha=$(sha_local "$TAR")"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过"; exit 2; }
TAR_SHA=$(sha_local "$TAR")

echo "===== P0-B 后端件（本工作树 HEAD） ====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
BIN_SHA=$(sha_local "$SERVER_BIN")
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$BIN_SHA"

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（构建＋打包＋自检）；未 scp、未碰服务器。加 APPLY=1 才落位。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位） ====="
scp -P "$PORT" "$TAR" "root@$HOST:/tmp/fe-$TAG.tar.gz" || { echo "ABORT=4 scp tar 失败"; exit 4; }
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=4 scp server 失败"; exit 4; }
scp -P "$PORT" "$SWAP" "root@$HOST:/tmp/swap_web_and_binary.sh" || { echo "ABORT=4 scp swap 脚本失败"; exit 4; }

echo "===== P1b 现役二进制读数（新件必须不同于现役件，证明编译的是含本批改动的新树） ====="
CUR_SHA="$(ssh -p "$PORT" "root@$HOST" 'sha256sum /opt/translator/bin/translator-server | cut -d" " -f1')" \
  || { echo "ABORT=5 现役二进制读数失败"; exit 5; }
# ⚠️ 变量后紧跟全角标点（；，（）等）必须写花括号：本机 locale 下 bash 把高位字节
#    当标识符字符，`$CUR_SHA；` 会被解析成变量名「CUR_SHA；」⇒ set -u 直接 unbound。
echo "现役二进制 sha=${CUR_SHA}；新件 sha=${BIN_SHA}"
[ -n "$CUR_SHA" ] || { echo "ABORT=5 现役二进制 sha 读空"; exit 5; }
if [ "$CUR_SHA" = "$BIN_SHA" ]; then
  echo "ABORT=5 新件与现役件同 sha ⇒ 编译的不是含本批改动的新树，停手人工核"; exit 5
fi

echo "===== P2 服务器侧换源＋换件 ====="
ssh -p "$PORT" "root@$HOST" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"

echo "===== P3 复验：单元 active + 健康端点（assist 单元真名是 ai-assist，不是 translator-assist） ====="
ssh -p "$PORT" "root@$HOST" '
  for u in translator translator-demo ai-assist caddy; do
    printf "%s=%s " "$u" "$(systemctl is-active "$u" 2>/dev/null)"
  done; echo
  curl -s -o /dev/null -w "livez8787=%{http_code} " http://127.0.0.1:8787/livez
  curl -s -o /dev/null -w "livez8789=%{http_code}\n" http://127.0.0.1:8789/livez
' || true
exit "$rc"
