#!/usr/bin/env bash
# release_20261022a_iam_finish.sh — IAM 重做收口与一致性核查批次（前后端都换）
#
# 本批内容：
#   前端（换源）：
#     · components/PwdEye.tsx 新立——登录/注册/找回/强制改密共用的密码可见切换抽成唯一实现。
#       旧形态该钮只写在 Login.tsx 里，ForcePasswordGate（强制改密窗）三个密码框全盲输，
#       而它是批量导入账号的必经之路、初始口令还是随机串；三处显隐同进同退（状态外提）。
#     · AiRegisterFlow 补卡内红字错误位：fail() 两个出口同源（toast＋卡内 .auth-err），
#       提交前先清上一轮红字（错误不再只走 toast，一消失就成"点了没反应"）。
#   后端（换件，translator-server 两站同件；translator-assist 本批零改动不换）：
#     · ValidatePassword 补「去空白后仍非空」判据（旧实现 p=="" 放过 6 空格/全角空格/制表符）；
#     · 管理台建号补强度闸（四个口令写口之一此前只判非空）；
#     · 资料补全设密码成功后清 must_change_pwd；finishLogin 对齐 brand_host/sso_code；
#     · 邮箱验证码腿补账号级失败锁定（单码 5 次错误作废）；
#     · 内联 writeJSON 分批收口到 s.writeError + apierrors。
#
# 落位一律交给常设执行体 deploy/swap_web_and_binary.sh（两段式、根路径守卫、ETXTBSY 规避）。
# 用法：bash deploy/release_20261022a_iam_finish.sh            # 干跑（本机编译＋打包＋自检）
#       APPLY=1 bash deploy/release_20261022a_iam_finish.sh    # 真换（scp → swap → 复验读数）
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261022a"

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
# 本批的特征串必须是**运行期真的会用到**的字面量（AGENTS §三）。
# 诚实说明：本批前端改动是**结构型**的（PwdEye 抽离、卡内错误位接线），没有新增用户可见
# 字面量——PwdEye 复用的 auth.showPwd/hidePwd 两键与 .auth-err 类名在修复前就已存在于
# 产物里。故前端正向判据取 PwdEye 的契约面类名 auth-pwd-eye（三处共用实现的编译痕迹），
# 配 agreed:!0 负向（上批 P1-4 的硬编码 agreed:true 不许复活），结构完整性交给
# check_frontend_tar.sh 四腿（侧车/脏文件/入口体积/语种 chunk）。
PWDEYE=$(LC_ALL=C grep -R -c -F -- "auth-pwd-eye" "$REPO/frontend-react/dist/assets" | awk -F: '{s+=$2} END {print s+0}')
[ "$PWDEYE" -ge 1 ] || { echo "ABORT=2 前端产物缺特征串 auth-pwd-eye ⇒ 编译的不是本工作树"; exit 2; }
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

echo "===== P0-B 后端件（本工作树 HEAD）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 本批后端新增的用户可见字面量：邮箱验证码腿的账号级失败锁定提示（旧件里没有这句）。
# 换件前会在服务器侧对现役件做命中 0 的负向对照（见 P2 前预检）。
W_LOCK=$(LC_ALL=C grep -a -c -F -- "登录尝试过于频繁，请稍后再试" "$SERVER_BIN" || true)
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ 账号锁定文案=${W_LOCK}（期望 ≥1）"
[ "$W_LOCK" != "0" ] || { echo "ABORT=3 缺账号锁定文案特征串 ⇒ 编译的不是含本批改动的树"; exit 3; }
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

echo "===== P1b 服务器侧负向对照：现役件里不该有本批新串（命中必须为 0）====="
ssh -p "$PORT" "root@$HOST" '
  set -uo pipefail
  OLD=0
  for u in translator translator-demo; do
    pid=$(systemctl show -p MainPID --value "$u")
    exe=$(readlink -f "/proc/$pid/exe" 2>/dev/null || echo "<未读>")
    hit=$(grep -a -c -F -- "登录尝试过于频繁，请稍后再试" "$exe" 2>/dev/null || true)
    echo "unit=$u pid=$pid exe=$exe 新串命中=${hit}"
    [ "$hit" != "0" ] && OLD=1
  done
  [ "$OLD" = "0" ] || { echo "ABORT=5 现役件里已有本批新串 ⇒ 件已被换过或读数错了，停手人工核"; exit 5; }
' || { echo "ABORT=5 负向对照失败（见上）"; exit 5; }

echo "===== P2 服务器侧换源＋换件 ====="
ssh -p "$PORT" "root@$HOST" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
exit "$rc"
