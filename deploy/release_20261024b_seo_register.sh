#!/usr/bin/env bash
# ============================================================================
# release_20261024b_seo_register.sh — /register 路由 SEO 文案修正发版（前后端同批，两站）
#
# 本批内容（2026-10-24 发版后复验抓到）：
#   frontend-react/src/seo/route_meta.json 的 /register 十条…不，十二语种 title
#   直接抄了 /login 的「Sign in · LangCross」，注册页对搜索引擎与浏览器标签都写成"登录"；
#   现按 12 语种各补一句「注册」文案，并随 scripts/sync_seo_meta.sh 水合到后端 //go:embed 副本。
#   新增锁＝internal/api/seo_meta_test.go 的同语种 title 去重腿 + 抄回反证腿。
#
# 进包证据（不用函数名 grep，-ldflags="-s -w" 会把内联函数名从名字表抹掉）：
#   ① 二进制含**运行期真会读**的字面量：泰语 /register 标题「สมัครสมาชิก · LangCross」
#      （它只存在于 //go:embed 的 seo_meta.json 里，命中即证明嵌的是本批新副本）；
#   ② 前端 dist 必须有承载该标题的分片，换源后与本地**逐字节 sha 等值**（由消费方自己读出值）；
#   ③ 现役二进制 sha 必须不同于新件。
#
# 发版链：本机构建/打包/自检 → scp → 服务器侧常设执行体 swap_web_and_binary.sh → 复验。全程不碰 caddy。
# 用法：bash deploy/release_20261024b_seo_register.sh         # 干跑
#       APPLY=1 bash deploy/release_20261024b_seo_register.sh # 真发版
# ============================================================================
set -uo pipefail
export CODEBUDDY_SAFE_DELETE_ENABLED=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261024b"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"
# 泰语注册标题：只在本批新副本里存在，且含非 ASCII 故两側一律按字面 UTF-8 匹配
MARK_TITLE='สมัครสมาชิก · LangCross'

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0-SEO 前后端 meta 副本同步校验 ====="
bash "$REPO/scripts/sync_seo_meta.sh" --check || { echo "ABORT=1 SEO meta 两份副本不同步"; exit 1; }

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
  || { echo "ABORT=3 server 编译失败"; exit 3; }
BIN_SHA=$(sha_local "$SERVER_BIN")
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$BIN_SHA"

echo "===== P0-C 进包证据 ====="
n=$(LC_ALL=C grep -a -c -F "$MARK_TITLE" "$SERVER_BIN" || true)
echo "bin_marker[泰语注册标题]=${n:-0}"
[ "${n:-0}" -ge 1 ] || { echo "ABORT=3 二进制缺 SEO 新副本标记 ⇒ 编错树或 embed 没带上"; exit 3; }
CHUNK_LOCAL=$(grep -l -F "$MARK_TITLE" "$REPO"/frontend-react/dist/assets/*.js 2>/dev/null | head -1 || true)
[ -n "$CHUNK_LOCAL" ] || { echo "ABORT=3 本地 dist 找不到承载注册标题的分片"; exit 3; }
CHUNK_NAME="$(basename "$CHUNK_LOCAL")"
CHUNK_SHA=$(sha_local "$CHUNK_LOCAL")
echo "seo_chunk=${CHUNK_NAME} sha=${CHUNK_SHA}"

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 本机动作全过（同步校验/构建/打包/自检/标记）；未 scp、未碰服务器。加 APPLY=1 才落位。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位） ====="
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 "cat > /tmp/fe-$TAG.tar.gz" < "$TAR" || { echo "ABORT=4 上传 tar 失败"; exit 4; }
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 "cat > /tmp/ts-new-$TAG" < "$SERVER_BIN" || { echo "ABORT=4 上传 server 失败"; exit 4; }
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 'cat > /tmp/swap_web_and_binary.sh' < "$SWAP" || { echo "ABORT=4 上传 swap 脚本失败"; exit 4; }

echo "===== P1b 现役二进制读数（新件必须不同于现役件） ====="
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

echo "===== P3 复验：单元 active + 健康 + 标记 + 分片字节等值 + 五路由注入 ====="
ssh -o BatchMode=yes -p 28022 root@43.108.86.140 "
  for u in translator translator-demo ai-assist caddy; do
    printf '%s=%s ' \"\$u\" \"\$(systemctl is-active \"\$u\" 2>/dev/null)\"
  done; echo
  for p in 8787 8789; do
    printf 'livez%s=' \"\$p\"; curl -s -o /dev/null -w '%{http_code} ' \"http://127.0.0.1:\$p/livez\"
    printf 'health%s=' \"\$p\"; curl -s -o /dev/null -w '%{http_code}\n' \"http://127.0.0.1:\$p/api/health\"
  done
  echo '--- seo chunk byte-equality ---'
  for web in /opt/translator/web /opt/translator-demo/web; do
    f=\$(grep -l -F '$MARK_TITLE' \"\$web/assets/\"*.js 2>/dev/null | head -1)
    if [ -n \"\$f\" ]; then echo \"\$web/\$(basename \"\$f\") sha=\$(sha256sum \"\$f\" | cut -d' ' -f1)\";
    else echo \"\$web MISSING_SEO_CHUNK\"; fi
  done
  echo '--- 五路由注入（title 必须各不相同） ---'
  for path in / /pricing /compare /login /register; do
    for lg in en ja; do
      printf '%s?lang=%s ' \"\$path\" \"\$lg\"
      curl -s -H \"Accept-Language: \$lg\" \"http://127.0.0.1:8787\$path?lang=\$lg\" | grep -oE '<title>[^<]*</title>' | head -1
    done
  done
" || true
echo "本地该分片期望 sha=${CHUNK_SHA} name=${CHUNK_NAME}（上面两站必须各有一行同 sha）"
echo "DEPLOY_DONE=1 tag=${TAG} tar_sha=${TAR_SHA} bin_sha=${BIN_SHA}"
