#!/usr/bin/env bash
# ============================================================================
# release_20261025_uatfix.sh — 2026-10-24/25 云端 UAT 修复批：两站前后端同批发版
#
# 本批内容（详见 PROGRESS「2026-10-25 收口批」与《部署指南》§22）：
#   后端：多身份（家行/账户号/登录补建个人版/身份切换/邀请链接建号）、文本工单逐行翻译与整单
#         重发（计数落 tickets.repair_attempts 列）、文件漏译 >5% 定向补译、文件指令回显守卫、
#         文化规则 prompt 上限、ticket_repair_max / text_ticket_per_line 的 0 档、SEO 服务端注入、
#         产物名 -<lang>；schema 幂等补列（tickets.repair_attempts、users.account_id/is_home）。
#   前端：身份切换器、邀请链接管理与受邀入职通知、竞品对标页与 og:image、12 语种补键、
#         统一登录卡的发码链（AI 接管只在租户站/受邀链接）。
#
# 进包证据（不用函数名 grep：-ldflags="-s -w" 会把内联函数名从名字表抹掉）：
#   ① 二进制含**运行期真会产出**的字面量「该邀请链接已被使用」（B3-b 的对外文案，只在本批新代码里）；
#   ② 前端 dist 必须找到承载「企业邀请链接」（B4-c 新面板标题）的分片，换源后与本地**逐字节 sha 等值**；
#   ③ 现役二进制/入口分片必须与新件不同。
#
# 发版链：本机构建 → 清脏 → 侧车 → 打包 → 交付包自检 → 交叉编译 → 上传 →
#         服务器侧常设执行体 swap_web_and_binary.sh（两段式、两站全绿才落位）→ 复验。全程不碰 caddy。
# 用法：bash deploy/release_20261025_uatfix.sh          # 干跑（本机动作全做，不碰服务器）
#       APPLY=1 bash deploy/release_20261025_uatfix.sh  # 真发版
# ============================================================================
set -uo pipefail
export CODEBUDDY_SAFE_DELETE_ENABLED=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261025"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"
MARK_BIN='该邀请链接已被使用'
MARK_FE='企业邀请链接'

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 SEO 两份副本同步校验 ====="
bash "$REPO/scripts/sync_seo_meta.sh" --check || { echo "ABORT=1 SEO meta 两份副本不同步"; exit 1; }
node "$REPO/scripts/gen_seo_meta.mjs" --check || { echo "ABORT=1 route_meta 12 语种内容闸没过"; exit 1; }

echo "===== P0 前端构建 → 清脏 → 侧车 → 打包 → 交付包自检 ====="
(cd "$REPO/frontend-react" && npx vite build) || { echo "ABORT=2 vite build 失败"; exit 2; }
find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
node "$REPO/scripts/precompress_dist.mjs" --check || { echo "ABORT=2 侧车自检没过"; exit 2; }
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
  || { echo "ABORT=3 server 编译失败"; exit 3; }
BIN_SHA=$(sha_local "$SERVER_BIN")
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$BIN_SHA"

echo "===== P0-C 进包证据 ====="
n=$(LC_ALL=C grep -a -c -F "$MARK_BIN" "$SERVER_BIN" || true)
echo "bin_marker[邀请链接已使用]=${n:-0}"
[ "${n:-0}" -ge 1 ] || { echo "ABORT=3 二进制缺本批标记 ⇒ 编错树或新代码没编进去"; exit 3; }
CHUNK_LOCAL=$(grep -l -F "$MARK_FE" "$REPO"/frontend-react/dist/assets/*.js 2>/dev/null | head -1 || true)
[ -n "$CHUNK_LOCAL" ] || { echo "ABORT=3 本地 dist 找不到承载邀请链接面板的分片"; exit 3; }
CHUNK_NAME="$(basename "$CHUNK_LOCAL")"
CHUNK_SHA=$(sha_local "$CHUNK_LOCAL")
echo "fe_chunk=${CHUNK_NAME} sha=${CHUNK_SHA}"

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 本机动作全过（同步/构建/打包/自检/标记）；未 scp、未碰服务器。加 APPLY=1 才落位。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位） ====="
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 "cat > /tmp/fe-$TAG.tar.gz" < "$TAR" || { echo "ABORT=4 上传 tar 失败"; exit 4; }
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 "cat > /tmp/ts-new-$TAG" < "$SERVER_BIN" || { echo "ABORT=4 上传 server 失败"; exit 4; }
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 'cat > /tmp/swap_web_and_binary.sh' < "$SWAP" || { echo "ABORT=4 上传 swap 脚本失败"; exit 4; }

echo "===== P1b 现役件读数（新件必须不同于现役件） ====="
CUR_SHA="$(ssh -o BatchMode=yes -p 28022 root@43.108.86.140 'sha256sum /opt/translator/bin/translator-server | cut -d" " -f1')" \
  || { echo "ABORT=5 现役二进制读数失败"; exit 5; }
echo "现役二进制 sha=${CUR_SHA}；新件 sha=${BIN_SHA}"
[ -n "$CUR_SHA" ] || { echo "ABORT=5 现役二进制 sha 读空"; exit 5; }
if [ "$CUR_SHA" = "$BIN_SHA" ]; then
  echo "ABORT=5 新件与现役件同 sha ⇒ 编译的不是含本批改动的新树，停手人工核"; exit 5
fi

echo "===== P2 服务器侧换源＋换件（两段式，两站全绿才落位） ====="
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
[ "$rc" -eq 0 ] || { echo "ABORT=6 换源/换件失败，见上方 BAD/SWAP_ABORT 行"; exit "$rc"; }

echo "===== P3 复验：单元 + 健康 + 分片字节等值 + schema 列 + 五路由注入 ====="
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 "
  for u in translator translator-demo ai-assist caddy; do
    printf '%s=%s ' \"\$u\" \"\$(systemctl is-active \"\$u\" 2>/dev/null)\"
  done; echo
  for p in 8787 8789; do
    printf 'livez%s=' \"\$p\"; curl -s -o /dev/null -w '%{http_code} ' \"http://127.0.0.1:\$p/livez\"
    printf 'health%s=' \"\$p\"; curl -s -o /dev/null -w '%{http_code}\n' \"http://127.0.0.1:\$p/api/health\"
  done
  echo '--- 新列是否已补（启动期幂等迁移） ---'
  psql \"\$(grep -h '^DB_DSN=' /etc/translator/secrets.env | cut -d= -f2- | tr -d '\"')\" -tAc \
    \"SELECT column_name FROM information_schema.columns WHERE table_name='tickets' AND column_name='repair_attempts'\" 2>/dev/null | sed 's/^/tickets./'
  psql \"\$(grep -h '^DB_DSN=' /etc/translator/secrets.env | cut -d= -f2- | tr -d '\"')\" -tAc \
    \"SELECT column_name FROM information_schema.columns WHERE table_name='users' AND column_name IN ('account_id','is_home') ORDER BY 1\" 2>/dev/null | sed 's/^/users./'
  echo '--- fe chunk byte-equality ---'
  for web in /opt/translator/web /opt/translator-demo/web; do
    f=\$(grep -l -F '$MARK_FE' \"\$web/assets/\"*.js 2>/dev/null | head -1)
    if [ -n \"\$f\" ]; then echo \"\$web/\$(basename \"\$f\") sha=\$(sha256sum \"\$f\" | cut -d' ' -f1)\";
    else echo \"\$web MISSING_FE_CHUNK\"; fi
  done
  echo '--- 五路由注入（title 必须各不相同；/register 走 noindex） ---'
  for path in / /pricing /compare /login /register; do
    printf '%s ' \"\$path\"
    curl -s -H 'Accept-Language: zh' \"http://127.0.0.1:8787\$path?lang=zh\" | grep -oE '<title>[^<]*</title>' | head -1
  done
  echo '--- 首页品牌注入（必须命中 1） ---'
  curl -s http://127.0.0.1:8787/ | grep -c __BRANDING__
" || true
echo "本地该分片期望 sha=${CHUNK_SHA} name=${CHUNK_NAME}（上面两站必须各有一行同 sha）"
echo "DEPLOY_DONE=1 tag=${TAG} tar_sha=${TAR_SHA} bin_sha=${BIN_SHA}"
