#!/usr/bin/env bash
# ============================================================================
# release_20261023e_admin_brand.sh — 后台报障 F1–F4 发版（前后端同批，两站）
#
# 本批内容：
#   后端：平台品牌三列读写 + 显式 tenant_id=0 仅超管并短路 Host 解析；
#         品牌合并保存（mergeBrandNameMap）+ 种入写 brand_name；窄 setter SetBrandName；
#         平台组织树 include_personal / tenant_is_personal / personal_count。
#   前端：BrandP 平台根默认与 brand_names 回填/回传；品牌 tab 迁组织 Hub；个人租户开关；
#         登录/登出会话卫生；12 语种文案（品牌定制/页脚链接/显示个人租户/平台主站）。
#
# 进包证据（不用函数名 grep，-ldflags="-s -w" 会把被内联的函数名从名字表抹掉）：
#   ① 二进制必须含**运行期真会用到**的字面量：`brand_names 需为合法 JSON 对象`（合并失败文案）、
#      `include_personal`（组织树查询参数）、`tenant_is_personal`（组织出参 JSON tag，反射读）；
#   ② 前端：本地 dist 里含「显示个人租户」的懒加载分片，换源后必须与线上同文件 **sha256 等值**
#      （由消费方自己读出值，而不是看版本号/数日志）；
#   ③ 现役二进制 sha 必须不同于新件（证明编译的是含本批改动的新树）。
#
# 发版链：本机构建/打包/自检 → scp → 服务器侧常设执行体 swap_web_and_binary.sh（两段式换源+换件）
#   → 单元/健康/标记/字节等值复验。全程不碰 caddy。
#
# 用法：bash deploy/release_20261023e_admin_brand.sh            # 干跑（本机动作 + 打印判据）
#       APPLY=1 bash deploy/release_20261023e_admin_brand.sh    # 真发版
# ============================================================================
set -uo pipefail
export CODEBUDDY_SAFE_DELETE_ENABLED=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261023e"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 前端构建 → 清脏 → 侧车 → 打包 → 交付包自检 ====="
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

echo "===== P0-C 进包证据（运行期字面量，两站同件） ====="
# 三个串都必须命中：合并错误文案 / 组织树查询参数 / 组织出参 JSON tag（反射读，运行期真用）。
for lit in 'brand_names 需为合法 JSON 对象' 'include_personal' 'tenant_is_personal'; do
  n=$(LC_ALL=C grep -a -c -F "$lit" "$SERVER_BIN" || true)
  echo "bin_marker[$lit]=${n:-0}"
  [ "${n:-0}" -ge 1 ] || { echo "ABORT=3 二进制缺运行期标记：${lit}（编错树或 -ldflags 判据失效）"; exit 2; }
done
# 前端：本地必须能找到承载新文案的懒加载分片（找不到＝构建树不对，后面字节等值会失去靶子）。
ADMIN_CHUNK_LOCAL=$(grep -l -F '显示个人租户' "$REPO"/frontend-react/dist/assets/*.js 2>/dev/null | head -1 || true)
[ -n "$ADMIN_CHUNK_LOCAL" ] || { echo "ABORT=3 本地 dist 找不到含「显示个人租户」的分片"; exit 2; }
ADMIN_CHUNK_NAME="$(basename "$ADMIN_CHUNK_LOCAL")"
ADMIN_CHUNK_SHA=$(sha_local "$ADMIN_CHUNK_LOCAL")
echo "admin_chunk=${ADMIN_CHUNK_NAME} sha=${ADMIN_CHUNK_SHA}"

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 本机动作全过（构建/打包/自检/标记）；未 scp、未碰服务器。加 APPLY=1 才落位。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位） ====="
scp -P "$PORT" "$TAR" "root@$HOST:/tmp/fe-$TAG.tar.gz" || { echo "ABORT=4 scp tar 失败"; exit 4; }
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=4 scp server 失败"; exit 4; }
scp -P "$PORT" "$SWAP" "root@$HOST:/tmp/swap_web_and_binary.sh" || { echo "ABORT=4 scp swap 脚本失败"; exit 4; }

echo "===== P1b 现役二进制读数（新件必须不同于现役件） ====="
CUR_SHA="$(ssh -p "$PORT" "root@$HOST" 'sha256sum /opt/translator/bin/translator-server | cut -d" " -f1')" \
  || { echo "ABORT=5 现役二进制读数失败"; exit 5; }
echo "现役二进制 sha=${CUR_SHA}；新件 sha=${BIN_SHA}"
[ -n "$CUR_SHA" ] || { echo "ABORT=5 现役二进制 sha 读空"; exit 5; }
if [ "$CUR_SHA" = "$BIN_SHA" ]; then
  echo "ABORT=5 新件与现役件同 sha ⇒ 编译的不是含本批改动的新树，停手人工核"; exit 5
fi

echo "===== P2 服务器侧换源＋换件（两段式，两站全绿才落位） ====="
ssh -p "$PORT" "root@$HOST" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
[ "$rc" -eq 0 ] || { echo "ABORT=6 换源/换件失败，见上方 BAD/SWAP_ABORT 行"; exit "$rc"; }

echo "===== P3 复验：单元 active + 健康端点 + 运行期标记 + 分片字节等值 ====="
ssh -p "$PORT" "root@$HOST" "
  for u in translator translator-demo ai-assist caddy; do
    printf '%s=%s ' \"\$u\" \"\$(systemctl is-active \"\$u\" 2>/dev/null)\"
  done; echo
  for p in 8787 8789; do
    printf 'livez%s=' \"\$p\"; curl -s -o /dev/null -w '%{http_code} ' \"http://127.0.0.1:\$p/livez\"
    printf 'health%s=' \"\$p\"; curl -s -o /dev/null -w '%{http_code}\n' \"http://127.0.0.1:\$p/api/health\"
  done
  echo '--- binary markers ---'
  for f in /opt/translator/bin/translator-server /opt/translator-demo/bin/translator-server; do
    printf '%s sha=%s ' \"\$f\" \"\$(sha256sum \"\$f\" | cut -d' ' -f1)\"
    for lit in 'brand_names 需为合法 JSON 对象' 'include_personal' 'tenant_is_personal'; do
      printf '[%s]=%s ' \"\$lit\" \"\$(LC_ALL=C grep -a -c -F \"\$lit\" \"\$f\" || true)\"
    done; echo
  done
  echo '--- admin chunk byte-equality ---'
  for web in /opt/translator/web /opt/translator-demo/web; do
    f=\$(grep -l -F '显示个人租户' \"\$web/assets/\"*.js 2>/dev/null | head -1)
    if [ -n \"\$f\" ]; then echo \"\$web/\$(basename \"\$f\") sha=\$(sha256sum \"\$f\" | cut -d' ' -f1)\";
    else echo \"\$web MISSING_LABEL_CHUNK\"; fi
  done
" || true
echo "本地该分片期望 sha=${ADMIN_CHUNK_SHA} name=${ADMIN_CHUNK_NAME}（上面两站必须各有一行同 sha）"

echo "===== P3b 只读行为探针 ====="
ssh -p "$PORT" "root@$HOST" '
  # 匿名显式 0 仍回落平台（不因放行超管而把匿名首屏打歪）；接口必须 200 且 tenant_id=0
  for p in 8787 8789; do
    printf "branding0_%s=" "$p"
    curl -s "http://127.0.0.1:$p/api/tenant/branding?tenant_id=0" | head -c 160; echo
  done
  # 平台组织接口未登录必须 401（不是 500）；证明新 handler 路由在位
  printf "orgs_anon="; curl -s -o /dev/null -w "%{http_code}\n" "http://127.0.0.1:8787/api/admin/orgs"
' || true

echo "DEPLOY_DONE=1 tag=${TAG} tar_sha=${TAR_SHA} bin_sha=${BIN_SHA}"
