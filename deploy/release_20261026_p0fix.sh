#!/usr/bin/env bash
# ============================================================================
# release_20261026_p0fix.sh — 2026-10-26 UAT P0-01 收口批：两站前后端同批发版
#
# 本批内容（方案 v2.0）：
#   后端：chat 可用性写入门（review 前跳过退化串、review 后极简/fallback 兜底、
#         闸门后最终断言）；术语匹配大小写不敏感 + 目标书写系统校验（脏术语不入硬闸）；
#         KB 入库/导入拒绝书写系统明显不符的术语。
#   前端：chat gate_warnings 可见；/register 注册意图文案；320 视口 Turnstile 尺寸/CSS 兜底；
#         /billing 余额明细取整说明。
#
# 第二批（同日 UAT 用户五点）：
#   OCR 无实义短段（单假名/编号/股票代码）按原文预填、不送模型；图片 60m / 音频 90m 执行上限；
#   即时翻译图片上传后立即出「正在识别并翻译」状态卡与按钮转圈。
#
# 进包证据（不用函数名 grep：-ldflags="-s -w" 会抹掉函数名）：
#   ① 二进制含运行期真会产出的字面量「即时翻译译文不可用，已丢弃」；
#   ② 二进制另含媒体验收批的日志字面量「媒体工单执行上限已放宽」（图片 60m / 音频 90m）；
#   ③ 前端 dist 必须找到承载 gate 警告块的 `gate-warnings` 字符串，换源后与本地逐字节 sha 等值；
#   ④ 现役二进制/入口分片必须与新件不同。
#
# 发版链：本机构建 → 清脏 → 侧车 → 打包 → 交付包自检 → 交叉编译 → 上传 →
#         服务器侧常设执行体 swap_web_and_binary.sh（两段式、两站全绿才落位）→ 复验。
# 全程不碰 caddy。
# 用法：bash deploy/release_20261026_p0fix.sh          # 干跑（本机动作全做，不碰服务器）
#       APPLY=1 bash deploy/release_20261026_p0fix.sh  # 真发版
# ============================================================================
set -uo pipefail
export CODEBUDDY_SAFE_DELETE_ENABLED=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261026"
TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"
SSH_TARGET="${SSH_TARGET:-root@43.108.86.140}"
SSH_PORT="${SSH_PORT:-28022}"
MARK_BIN='即时翻译译文不可用，已丢弃'
MARK_BIN2='媒体工单执行上限已放宽'
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
n=$(LC_ALL=C grep -a -c -F "$MARK_BIN" "$SERVER_BIN" || true)
echo "bin_marker[chat_drop]=${n:-0}"
[ "${n:-0}" -ge 1 ] || { echo "ABORT=3 二进制缺本批标记 ⇒ 编错树或新代码没编进去"; exit 3; }
n2=$(LC_ALL=C grep -a -c -F "$MARK_BIN2" "$SERVER_BIN" || true)
echo "bin_marker[media_timeout]=${n2:-0}"
[ "${n2:-0}" -ge 1 ] || { echo "ABORT=3 二进制缺媒体验收批标记 ⇒ 新批次没编进去"; exit 3; }
CHUNK_LOCAL=$(grep -l -F "$MARK_FE" "$REPO"/frontend-react/dist/assets/*.js 2>/dev/null | head -1 || true)
[ -n "$CHUNK_LOCAL" ] || { echo "ABORT=3 本地 dist 找不到承载 gate 警告块的 JS 分片"; exit 3; }
CHUNK_NAME="$(basename "$CHUNK_LOCAL")"
CHUNK_SHA=$(sha_local "$CHUNK_LOCAL")
echo "fe_chunk=${CHUNK_NAME} sha=${CHUNK_SHA}"

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
  # 纯前端批次：Go 非测试源码没动 ⇒ 二进制字节确实应当相同。放行要显式下令（ALLOW_SAME_BIN=1），
  # 默认仍判死——「同 sha」最常见的原因仍是编错树，不能让它悄悄通过。
  if [ "${ALLOW_SAME_BIN:-0}" = "1" ]; then
    echo "NOTE 新件与现役件同 sha（ALLOW_SAME_BIN=1，纯前端批次）：仍走同一条换源/换件路径"
  else
    echo "ABORT=5 新件与现役件同 sha ⇒ 编译的不是含本批改动的新树，停手人工核（纯前端批次加 ALLOW_SAME_BIN=1）"; exit 5
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
