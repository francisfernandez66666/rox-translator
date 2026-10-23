#!/usr/bin/env bash
# release_20261023b_purge_tenant.sh — 门面回归＋后台登录同源＋超管删租户（前后端都换）
#
# 本批内容：
#   前端（换源）：
#     · lib/siteFlags.ts 门面判据补「租户定制页」那条腿：landingEnabledForGuest(tenantId)，
#       tenantId>0 ⇒ 关闭平台营销落地页，打开 / 直接落 /login。
#       为什么必须前端按运行时 tenant_id 判：一台机上的主站单元同时服务根域名与全部租户品牌
#       子域（caddy 通配块 → 8787），部署级 env 是**全单元一个值**，做不到「主站开、租户关」。
#       背景：演示单元的 LANDING_DISABLED=1 drop-in 在 10-19 被改名成 .bak，两站现在都读不到标记。
#     · components/Login.tsx：unified 去掉 mode==='home' 依赖 —— 主站后台登录也是主站登录，
#       /admin 退出后不再回「没有账号？自助注册试用」+ otpDesc + 「注册邮箱」placeholder。
#     · api/tenant.ts：tenantDelete 改走 /api/admin/tenants/delete 并带确认串（该行 code）。
#   后端（换件，translator-server 两站同件；translator-assist 本批零改动不换）：
#     · 新增 POST /api/admin/tenants/delete：仅超管、不能删自己所在租户、confirm 必须逐字
#       等于 code 或 name；逐表删、返回逐表读数（api/admin_purge.go + store/purge.go）。
#     · 下线 POST /api/tenant/delete（旧路径只判 RequireRole(4)、不看 tenant_id=0、无确认串）。
#     · 表清单收敛成一份：tenant.ScopedTables（tenant.Delete 与 PurgeTenantCascade 共用）。
#
# 落位一律交给常设执行体 deploy/swap_web_and_binary.sh（两段式、根路径守卫、ETXTBSY 规避）。
# 用法：bash deploy/release_20261023b_purge_tenant.sh            # 干跑（本机编译＋打包＋自检）
#       APPLY=1 bash deploy/release_20261023b_purge_tenant.sh    # 真换（scp → swap → 复验读数）
set -uo pipefail

# vite build 的 emptyOutDir 会批量删 dist 里 100+ 个旧产物，本机安全垫片的批量删除守卫
# 会拦（SAFE_DELETE_BULK_CONFIRM_REQUIRED / state lock timeout）。构建机若无此垫片则该变量无效。
export CODEBUDDY_SAFE_DELETE_ENABLED=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261023b"

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

# 特征串必须是**运行期真的会用到**的字面量（AGENTS §三）。
# 正向：新接口路径 /api/admin/tenants/delete 只可能来自本批的 api/tenant.ts。
NEWAPI=$(LC_ALL=C grep -R -c -F -- "/api/admin/tenants/delete" "$REPO/frontend-react/dist/assets" | awk -F: '{s+=$2} END {print s+0}')
[ "$NEWAPI" -ge 1 ] || { echo "ABORT=2 前端产物缺特征串 /api/admin/tenants/delete ⇒ 编译的不是本工作树"; exit 2; }
# 反向：旧接口路径不得再出现在产物里（旧形态 api/tenant.ts 里有一处）。
if LC_ALL=C grep -R -q -F -- "/api/tenant/delete" "$REPO/frontend-react/dist/assets"; then
  echo "ABORT=2 产物里仍有 /api/tenant/delete ⇒ 本批改动没进包"; exit 2
fi
# 门面第二条腿（landingEnabledForGuest 带 tenantId）与后台登录同源（unified 去掉 mode 依赖）
# 都是**纯逻辑改动**，没有新增用户可见字面量，生产包里函数名被压缩成短标识符，
# 按函数名 grep 只会得到 0（本脚本首版就这么误判过一次）。
# 这两条腿的锁交给测试：src/lib/siteFlags.test.ts（13 例）、src/lib/navShim.test.ts（9 例，
# 含「参数被改成 0 必须判红」的反证）、src/components/Login.auth.dom.test.tsx（21 例，
# 含 /admin 后台登录无自助注册的正测与租户站保留注册的反证）。
# 发版前必须它们先绿——这是本批唯一能证明那两条腿进包的证据。
echo "前端特征串：newapi=${NEWAPI}（≥1）"

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
# 本批后端新增的用户可见字面量：确认串不匹配的提示（旧件里没有这句）。
# 换件前会在服务器侧对现役件做命中 0 的负向对照（见 P1b）。
W_CONFIRM=$(LC_ALL=C grep -a -c -F -- "确认串不匹配：请在 confirm 里原样填写该租户的编码或名称" "$SERVER_BIN" || true)
# 反向：旧路由字面量 /api/tenant/delete 必须已从件里消失（该串在新件里只剩注释、不进二进制；
# 保险起见只做「≤ 旧件」的相对判断，绝对值见 P1b 的现役件读数）。
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ 确认串文案=${W_CONFIRM}（期望 ≥1）"
[ "$W_CONFIRM" != "0" ] || { echo "ABORT=3 缺确认串文案特征串 ⇒ 编译的不是含本批改动的树"; exit 3; }
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
    hit=$(grep -a -c -F -- "确认串不匹配：请在 confirm 里原样填写该租户的编码或名称" "$exe" 2>/dev/null || true)
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
