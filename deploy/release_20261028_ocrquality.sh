#!/usr/bin/env bash
# ============================================================================
# release_20261028_ocrquality.sh — 2026-10-28 OCR 质量链批：两站前后端同批发版
#
# 本批内容（账见 PROGRESS「2026-10-28 OCR 质量链批」）：
#   后端：textscript 新包；识别层 P0-B/C/P4；清洗层；引擎侧 AI 判定（StageOcrFix）；
#         PendingOcrDoc 交接与三处接线；结构几何列聚类 + AssignBlocks；PP-StructureV3 骨架（默认关）；
#         LID 内置后端；identity 死锁修复；meta 闸门（scripts/gate_mutants.*）。
#   前端：**零改动**（重新构建只为与后端同批落位）。
#
# 进包证据（只认运行期真会产出的字面量；-ldflags="-s -w" 会抹掉函数名）：
#   ① 交付诚实闸新增文案「OCR 清洗结果与提取文本不同源，未重建回填映射」
#   ② gateWarnings 新增文案「图片识别结果与提取文本不同源」
#   ③ 健康面新增键「lid_engine」
#
# 发版链：SEO 校验 → 前端构建/侧车/打包/自检 → 交叉编译 → 标记自检 →
#         资产同步（两站 .py + assets/，逐文件 sha256）→ 上传 →
#         服务器侧常设执行体 swap_web_and_binary.sh（两段式、两站全绿才落位）→ 复验。
# 全程不碰 caddy。
# 用法：bash deploy/release_20261028_ocrquality.sh          # 干跑（本机动作全做，不碰服务器）
#       APPLY=1 bash deploy/release_20261028_ocrquality.sh  # 真发版
# ============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261028ocrq"
TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"
SSH_TARGET="${SSH_TARGET:-root@43.108.86.140}"
SSH_PORT="${SSH_PORT:-28022}"
MARK_BIN='OCR 清洗结果与提取文本不同源，未重建回填映射'
MARK_BIN2='图片识别结果与提取文本不同源'
MARK_BIN3='lid_engine'
MARK_FE='gate-warnings'

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 SEO 两份副本同步校验 ====="
bash "$REPO/scripts/sync_seo_meta.sh" --check || { echo "ABORT=1 SEO meta 两份副本不同步"; exit 1; }
node "$REPO/scripts/gen_seo_meta.mjs" --check || { echo "ABORT=1 route_meta 12 语种内容闸没过"; exit 1; }

echo "===== P0 前端构建 → 清脏 → 侧车 → 打包 → 交付包自检 ====="
(cd "$REPO/frontend-react" && npx vite build) || { echo "ABORT=2 vite build 失败"; exit 2; }
find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
# ★ 本机 Finder/并行进程会在构建窗口里回写 .DS_Store（实测：删完一会儿又出现）。
#   清脏→自检最多重试 3 次；三次仍脏即停手（判据不降级，见 AGENTS「换源只走 tar」三条）。
ok=0
for i in 1 2 3; do
  find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
  if node "$REPO/scripts/precompress_dist.mjs" --check; then ok=1; break; fi
  sleep 1
done
[ "$ok" = "1" ] || { echo "ABORT=2 侧车自检没过（重试 3 次）"; exit 2; }
rm -f "$TAR"
find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
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
for pair in "stale_warn=$MARK_BIN" "gate_warn=$MARK_BIN2" "lid_engine=$MARK_BIN3"; do
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

echo "===== P1 能力脚本资产同步（两站：*.py + assets/，逐文件 sha256） ====="
MODE=remote SSH_HOST="$SSH_TARGET" SSH_PORT="$SSH_PORT" DEST_ROOT=/opt/translator \
  bash "$REPO/scripts/sync_fileproc_assets.sh" || { echo "ABORT=4 主站资产同步失败"; exit 4; }
MODE=remote SSH_HOST="$SSH_TARGET" SSH_PORT="$SSH_PORT" DEST_ROOT=/opt/translator-demo \
  bash "$REPO/scripts/sync_fileproc_assets.sh" || { echo "ABORT=4 演示站资产同步失败"; exit 4; }

echo "===== P2 上传（此时仍未落位） ====="
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" "cat > /tmp/fe-$TAG.tar.gz" < "$TAR" || { echo "ABORT=5 上传 tar 失败"; exit 5; }
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" "cat > /tmp/ts-new-$TAG" < "$SERVER_BIN" || { echo "ABORT=5 上传 server 失败"; exit 5; }
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" 'cat > /tmp/swap_web_and_binary.sh' < "$SWAP" || { echo "ABORT=5 上传 swap 脚本失败"; exit 5; }

echo "===== P2b 现役件读数（新件必须不同于现役件） ====="
CUR_SHA="$(ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" 'sha256sum /opt/translator/bin/translator-server | cut -d" " -f1')" \
  || { echo "ABORT=6 现役二进制读数失败"; exit 6; }
echo "现役二进制 sha=${CUR_SHA}；新件 sha=${BIN_SHA}"
[ -n "$CUR_SHA" ] || { echo "ABORT=6 现役二进制 sha 读空"; exit 6; }
if [ "$CUR_SHA" = "$BIN_SHA" ]; then
  echo "ABORT=6 新件与现役件同 sha ⇒ 编译的不是含本批改动的新树，停手人工核"; exit 6
fi

echo "===== P3 服务器侧换源＋换件（两段式，两站全绿才落位） ====="
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P3 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
[ "$rc" -eq 0 ] || { echo "ABORT=7 换源/换件失败，见上方 BAD/SWAP_ABORT 行"; exit "$rc"; }

echo "===== P4 复验：单元 + 健康能力位 + 资产指纹 + 两站入口 ====="
ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" "
  for u in translator translator-demo ai-assist caddy; do
    printf '%s=%s ' \"\$u\" \"\$(systemctl is-active \"\$u\" 2>/dev/null)\"
  done; echo
  for p in 8787 8789; do
    printf 'health%s=' \"\$p\"; curl -s \"http://127.0.0.1:\$p/api/health\" | python3 -c 'import json,sys;d=json.load(sys.stdin);print({k:d.get(k) for k in (\"status\",\"ocr_ready\",\"ocr_fix\",\"lid_ready\",\"lid_engine\",\"structure\",\"structure_engine\")})' 2>/dev/null || echo PARSE_FAIL
  done
  echo '--- newly synced scripts sha (must exist on both roots) ---'
  for root in /opt/translator /opt/translator-demo; do
    for f in ocr_recognize.py structure_v3.py; do
      if [ -f \"\$root/bin/\$f\" ]; then echo \"\$root/bin/\$f sha=\$(sha256sum \"\$root/bin/\$f\" | cut -d' ' -f1)\";
      else echo \"\$root/bin/\$f MISSING\"; fi
    done
  done
  echo '--- fe chunk byte-equality ---'
  for web in /opt/translator/web /opt/translator-demo/web; do
    f=\$(grep -l -F '$MARK_FE' \"\$web/assets/\"*.js 2>/dev/null | head -1)
    if [ -n \"\$f\" ]; then echo \"\$web/\$(basename \"\$f\") sha=\$(sha256sum \"\$f\" | cut -d' ' -f1)\";
    else echo \"\$web MISSING_FE_CHUNK\"; fi
  done
"
echo "RELEASE_DONE=1 本机 chunk sha=${CHUNK_SHA}（远端应等值）"
