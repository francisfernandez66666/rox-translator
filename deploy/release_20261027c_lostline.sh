#!/usr/bin/env bash
# ============================================================================
# release_20261027c_lostline.sh — 2026-10-27 工单 T20261027152356CMY 漏译根因批：两站前后端同批发版
#
# 本批内容（账见 PROGRESS §五十四，口径见 AGENTS §一·19 第五族）：
#   后端七处：reviewBatchSafe 空槽必送审校；审校/批量腿 prompt 注入语境与术语口径；
#             RetryLineUntilUsable 加「换校对模型」一档 ＋ retryDiag 诊断读数；
#             IsTranslationUsable 判据 3″（中文回显被洗成符号骨架后不许判可用）；
#             stripTrailingCJKNotes 行尾骨架修剪；retryImageSegments 两处 WARN；
#             extractSegments 非 Office/PDF 回退查 ticket_segments。
#   前端：**本批零改动**（重新构建只为与后端同批落位，dist 应与现役件逐字节等值）。
#
# 进包证据（不用函数名 grep：-ldflags="-s -w" 会抹掉函数名，只认运行期真会产出的字面量）：
#   ① 二进制含本批新增的逐行失败 WARN 文案「图片工单逐行补译仍不合格，按原文回落」；
#   ② 二进制含本批新增的整语种汇总 WARN 文案「图片工单逐行补译判上游断」；
#   ③ 二进制含审校 prompt 新增的空槽口径「尚未译出」；
#   ④ 前端 dist 仍能找到承载 gate 警告块的 `gate-warnings` 分片（前端零改动的对照）。
#
# 发版链：本机构建 → 清脏 → 侧车 → 打包 → 交付包自检 → 交叉编译 → 标记自检 → 上传 →
#         服务器侧常设执行体 swap_web_and_binary.sh（两段式、两站全绿才落位）→ 复验。
# 全程不碰 caddy。
# 用法：bash deploy/release_20261027c_lostline.sh          # 干跑（本机动作全做，不碰服务器）
#       APPLY=1 bash deploy/release_20261027c_lostline.sh  # 真发版
# ============================================================================
set -uo pipefail
export CODEBUDDY_SAFE_DELETE_ENABLED=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261027c"
TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"
SSH_TARGET="${SSH_TARGET:-root@43.108.86.140}"
SSH_PORT="${SSH_PORT:-28022}"
MARK_BIN='图片工单逐行补译仍不合格，按原文回落'
MARK_BIN2='图片工单逐行补译判上游断'
MARK_BIN3='尚未译出'
MARK_FE='gate-warnings'

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
for pair in "per_line_warn=$MARK_BIN" "upstream_dead_warn=$MARK_BIN2" "review_empty_slot=$MARK_BIN3"; do
  name="${pair%%=*}"; lit="${pair#*=}"
  n=$(LC_ALL=C grep -a -c -F "$lit" "$SERVER_BIN" || true)
  echo "bin_marker[$name]=${n:-0}"
  [ "${n:-0}" -ge 1 ] || { echo "ABORT=3 二进制缺本批标记 $name ⇒ 编错树或新代码没编进去"; exit 3; }
done
CHUNK_LOCAL=$(grep -l -F "$MARK_FE" "$REPO"/frontend-react/dist/assets/*.js 2>/dev/null | head -1 || true)
[ -n "$CHUNK_LOCAL" ] || { echo "ABORT=3 本地 dist 找不到承载 gate 警告块的 JS 分片"; exit 3; }
CHUNK_NAME="$(basename "$CHUNK_LOCAL")"
CHUNK_SHA=$(sha_local "$CHUNK_LOCAL")
echo "fe_chunk=${CHUNK_NAME} sha=${CHUNK_SHA}（前端本批零改动，应与现役件同值）"

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 本机动作全过（同步/构建/打包/自检/标记）；未 scp、未碰服务器。加 APPLY=1 才落位。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位） ====="
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" "cat > /tmp/fe-$TAG.tar.gz" < "$TAR" || { echo "ABORT=4 上传 tar 失败"; exit 4; }
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" "cat > /tmp/ts-new-$TAG" < "$SERVER_BIN" || { echo "ABORT=4 上传 server 失败"; exit 4; }
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" 'cat > /tmp/swap_web_and_binary.sh' < "$SWAP" || { echo "ABORT=4 上传 swap 脚本失败"; exit 4; }

echo "===== P1b 现役件读数（新件必须不同于现役件） ====="
CUR_SHA="$(ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" 'sha256sum /opt/translator/bin/translator-server | cut -d" " -f1')" \
  || { echo "ABORT=5 现役二进制读数失败"; exit 5; }
echo "现役二进制 sha=${CUR_SHA}；新件 sha=${BIN_SHA}"
[ -n "$CUR_SHA" ] || { echo "ABORT=5 现役二进制 sha 读空"; exit 5; }
if [ "$CUR_SHA" = "$BIN_SHA" ]; then
  if [ "${ALLOW_SAME_BIN:-0}" = "1" ]; then
    echo "NOTE 新件与现役件同 sha（ALLOW_SAME_BIN=1）：仍走同一条换源/换件路径"
  else
    echo "ABORT=5 新件与现役件同 sha ⇒ 编译的不是含本批改动的新树，停手人工核"; exit 5
  fi
fi

echo "===== P2 服务器侧换源＋换件（两段式，两站全绿才落位） ====="
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
[ "$rc" -eq 0 ] || { echo "ABORT=6 换源/换件失败，见上方 BAD/SWAP_ABORT 行"; exit "$rc"; }

echo "===== P3 复验：单元 + 健康 + 分片字节等值 + 两站入口 ====="
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" "
  for u in translator translator-demo ai-assist caddy; do
    printf '%s=%s ' \"\$u\" \"\$(systemctl is-active \"\$u\" 2>/dev/null)\"
  done; echo
  for p in 8787 8789; do
    printf 'livez%s=' \"\$p\"; curl -s -o /dev/null -w '%{http_code} ' \"http://127.0.0.1:\$p/livez\"
    printf 'health%s=' \"\$p\"; curl -s -o /dev/null -w '%{http_code}\n' \"http://127.0.0.1:\$p/api/health\"
  done
  echo '--- bin markers (新件必须三处都在) ---'
  for lit in '$MARK_BIN' '$MARK_BIN2' '$MARK_BIN3'; do
    printf '%s=' \"\$lit\"; grep -a -c -F \"\$lit\" /opt/translator/bin/translator-server || true
  done
  echo '--- fe chunk byte-equality ---'
  for web in /opt/translator/web /opt/translator-demo/web; do
    f=\$(grep -l -F '$MARK_FE' \"\$web/assets/\"*.js 2>/dev/null | head -1)
    if [ -n \"\$f\" ]; then echo \"\$web/\$(basename \"\$f\") sha=\$(sha256sum \"\$f\" | cut -d' ' -f1)\";
    else echo \"\$web MISSING_FE_CHUNK\"; fi
  done
  echo '--- 入口分片（两站应同代） ---'
  for web in /opt/translator/web /opt/translator-demo/web; do
    grep -o 'index-[A-Za-z0-9_-]*\\.js' \"\$web/index.html\" | head -1 | sed \"s#^#\$web/#\"
  done
"
echo "RELEASE_DONE=1 本机 chunk sha=${CHUNK_SHA}（远端应等值）"
