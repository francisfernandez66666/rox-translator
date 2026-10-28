#!/usr/bin/env bash
# ============================================================================
# release_20261028_slowchain.sh — 2026-10-28 慢链缓解＋图片版式回退批：两站发版
#
# 本批内容（账见《修改方案_20261028_CFpurge与图片版式.md》与 PROGRESS 同日条目）：
#   图片链三件：① `ocr_recognize.py` 摘掉「同行并框」与「960→1600 双档重跑」（框粒度回逐元素一框），
#                保留 `DET_BOX_THRESH`／`REC_CROP_PAD_RATIO`／`CLS_THRESH`／置信度口径这四件治错字的；
#              ② `engine.RenderSafeTranslations` 把三态占位符（空／`[翻译失败]`／`[已拦截·REDACTED]`）
#                摘成缺席 ⇒ 交付图上不再画占位符字面量（两处调用点：工单图片腿＋即时翻译腿）；
#              ③ 图片件单批段数上限收成 15（其余格式仍是自适应 40）。
#   CF 面：`deploy/swap_web_and_binary.sh` 新增 purge 腿（备料全绿后、落位前），本脚本负责把 token 送进去。
#
# ⚠️ 本批**没有新增运行期字符串字面量**，所以不发「进包特征串」那类判据（AGENTS §三 那条纪律：
#   判据只能吃运行期真会用到的字面串，`-s -w` 会把未引用常量与内联掉的函数名抹掉——
#   拿 `RenderSafeTranslations` 这种函数名 grep 必然假红）。进包证据换成三条**指纹与行为**判据：
#     E1 新件 sha ≠ 现役 sha（P2b，在真落位之前）
#     E2 `ocr_recognize.py` 本机与远端逐文件 sha256 等值（P1 同步自带＋P4 再点一次名）
#        ——识别改动全在这个 .py 里，**换二进制物理上带不上它**，这一条是唯一能证明它落地的读数
#     E3 部署后拿参照图重跑一单，产物三判据（版式回旧版水平／图上零占位符／错字清单不差于旧版）
#        ——由发版后那一轮跑，不在本脚本里判（脚本判不了客户屏幕上长什么样）
#
# 发版链：前端构建/侧车/打包/自检 → 交叉编译 → 能力脚本资产同步（两站）→ 上传 →
#         服务器侧常设执行体 swap_web_and_binary.sh（含 CF purge 腿）→ 复验。全程不碰 caddy。
#
# 用法：
#   bash deploy/release_20261028_slowchain.sh                 # 干跑（本机动作全做，不碰服务器）
#   APPLY=1 bash deploy/release_20261028_slowchain.sh         # 真发版（不带 token＝purge 走 skipped）
#   APPLY=1 CLOUDFLARE_API_TOKEN=cfut_xxx bash deploy/release_20261028_slowchain.sh   # 带 purge
# ⚠️ token 经 **stdin** 送进远端（不进远端 argv、不回显、不落盘）；一次性 token 用完即在 CF 面板删除。
# ============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261028slow"
TAR="/tmp/fe-${TAG}.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-${TAG}}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"
SSH_TARGET="${SSH_TARGET:-root@43.108.86.140}"
SSH_PORT="${SSH_PORT:-28022}"
# 被资产同步搬走、且本批改过的脚本：P4 逐文件点名核 sha（换件不带 .py，这是唯一那条落地证据）。
ASSET_FILES="${ASSET_FILES:-internal/fileproc/ocr_recognize.py}"

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }
sshr() { ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" "$@"; }

echo "===== P0 前端构建 → 清脏 → 侧车 → 打包 → 交付包自检 ====="
# 本批前端零改动，仍走完整链：两站入口同代比对与《部署指南》§五 的判据都吃这份 dist，
# 跳过构建会让「入口名形状／侧车在位」这些判据失去比对对象（历史批同样形态）。
(cd "$REPO/frontend-react" && npx vite build) || { echo "ABORT=2 vite build 失败"; exit 2; }
find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
# ★ 顺序只有一种：build → 生成侧车 → --check（两行，不许合并成「跑一次 --check」）。
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
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
echo "tar=${TAR} size=$(wc -c <"$TAR" | tr -d ' ') sha=$(sha_local "$TAR")"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过"; exit 2; }
TAR_SHA=$(sha_local "$TAR")

echo "===== P0-B 后端件（本工作树，含图片链三件改动） ====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 3; }
BIN_SHA=$(sha_local "$SERVER_BIN")
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=${BIN_SHA}"

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 本机动作全过（构建/侧车/打包/自检/编译）；未 scp、未碰服务器。加 APPLY=1 才落位。"
  exit 0
fi

echo "===== P1 能力脚本资产同步（两站：*.py + assets/，逐文件 sha256） ====="
MODE=remote SSH_HOST="$SSH_TARGET" SSH_PORT="$SSH_PORT" DEST_ROOT=/opt/translator \
  bash "$REPO/scripts/sync_fileproc_assets.sh" || { echo "ABORT=4 主站资产同步失败"; exit 4; }
MODE=remote SSH_HOST="$SSH_TARGET" SSH_PORT="$SSH_PORT" DEST_ROOT=/opt/translator-demo \
  bash "$REPO/scripts/sync_fileproc_assets.sh" || { echo "ABORT=4 演示站资产同步失败"; exit 4; }

echo "===== P2 上传（此时仍未落位） ====="
sshr "cat > /tmp/fe-${TAG}.tar.gz" < "$TAR" || { echo "ABORT=5 上传 tar 失败"; exit 5; }
sshr "cat > /tmp/ts-new-${TAG}" < "$SERVER_BIN" || { echo "ABORT=5 上传 server 失败"; exit 5; }
sshr 'cat > /tmp/swap_web_and_binary.sh' < "$SWAP" || { echo "ABORT=5 上传 swap 脚本失败"; exit 5; }

echo "===== P2b 现役件读数（E1：新件必须不同于现役件） ====="
CUR_SHA="$(sshr 'sha256sum /opt/translator/bin/translator-server | cut -d" " -f1')" \
  || { echo "ABORT=6 现役二进制读数失败"; exit 6; }
echo "现役二进制 sha=${CUR_SHA}；新件 sha=${BIN_SHA}"
[ -n "$CUR_SHA" ] || { echo "ABORT=6 现役二进制 sha 读空"; exit 6; }
if [ "$CUR_SHA" = "$BIN_SHA" ]; then
  echo "ABORT=6 新件与现役件同 sha ⇒ 编译的不是含本批改动的新树，停手人工核"; exit 6
fi

echo "===== P3 服务器侧换源＋换件（两段式；purge 腿在备料全绿后、落位前） ====="
SWAP_CMD="bash /tmp/swap_web_and_binary.sh /tmp/fe-${TAG}.tar.gz ${TAR_SHA} /tmp/ts-new-${TAG} ${BIN_SHA}"
if [ -n "${CLOUDFLARE_API_TOKEN:-}" ]; then
  # token 走 stdin 进远端变量：不出现在远端 argv（`ps` 里看得见参数），也不落任何日志。
  # ⚠️ 远端那条命令里**不能**写 `$CLOUDFLARE_API_TOKEN` 指望本机展开——本机没有这个变量的话
  # 它会被替换成空串，purge 腿于是静默走 skipped，屏幕上还留着一行「带 token」的谎话（首跑自查抓到）。
  echo "P3=带 token（CF purge 会真清边缘缓存；屏上应有「CF purge 已受理」）"
  printf '%s\n' "$CLOUDFLARE_API_TOKEN" | ssh -o BatchMode=yes -p "$SSH_PORT" "$SSH_TARGET" \
    "IFS= read -r CF_TOKEN; CLOUDFLARE_API_TOKEN=\"\$CF_TOKEN\" $SWAP_CMD; rc=\$?; unset CF_TOKEN CLOUDFLARE_API_TOKEN; exit \$rc"
  rc=$?
else
  echo "P3=未配 CLOUDFLARE_API_TOKEN（执行体会出一行 CF_PURGE=skipped；边缘旧 HTML 最多残留 2h，"
  echo "    要消窗口就事后去 CF 面板 Purge Everything，或带 token 重跑本脚本）"
  sshr "$SWAP_CMD"
  rc=$?
fi
echo "P3 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
[ "$rc" -eq 0 ] || { echo "ABORT=7 换源/换件失败（含 purge 未成功那一档），见上方 BAD/SWAP_ABORT 行；此刻源站仍是上一代"; exit "$rc"; }

echo "===== P4 复验：单元 + 两站入口同代（E2 在下面逐文件点名） ====="
sshr '
  for u in translator translator-demo ai-assist caddy; do
    printf "%s=%s " "$u" "$(systemctl is-active "$u" 2>/dev/null)"
  done; echo
  for p in 8787 8789; do
    printf "health%s=" "$p"
    curl -s "http://127.0.0.1:$p/api/health" \
      | python3 -c "import json,sys;d=json.load(sys.stdin);print({k:d.get(k) for k in (\"status\",\"ocr_ready\",\"ocr_fix\",\"lid_engine\")})" 2>/dev/null || echo PARSE_FAIL
  done
  for w in /opt/translator/web /opt/translator-demo/web; do
    printf "entry %s=" "$w"; grep -oE "assets/index-[A-Za-z0-9_-]*\.js" "$w/index.html" | head -1
  done
'
rc=$?
echo "P4 退码=${rc}（非 0 且读数是链路问题 ⇒ 复跑一次再判，别把没测到读成通过）"
[ "$rc" -eq 0 ] || { echo "ABORT=8 复验段链路没走完（≠ 发版失败），复跑一次再判"; exit 8; }

# E2 逐文件点名：本机那份与远端那份必须逐字节等值（同步脚本自己也核，这里是发版侧的第二把尺子）。
for rel in $ASSET_FILES; do
  f="$REPO/backend-go/$rel"
  if [ ! -f "$f" ]; then
    echo "ABORT=9 本机找不到 $rel ⇒ 判据对象不存在（「没核到」与「核过了」在屏幕上必须不同形）"
    exit 9
  fi
  LOCAL_ASSET_SHA="$(sha_local "$f")"
  REMOTE_ASSET_SHA="$(sshr "sha256sum /opt/translator/bin/$(basename "$rel") | cut -d' ' -f1")"
  echo "asset[$(basename "$rel")] local=${LOCAL_ASSET_SHA} remote=${REMOTE_ASSET_SHA}"
  if [ "$LOCAL_ASSET_SHA" != "$REMOTE_ASSET_SHA" ]; then
    echo "ABORT=9 识别脚本两处 sha 不等值 ⇒ 这台还在跑旧识别（换件不带 .py！）"
    exit 9
  fi
done

echo "RELEASE_DONE=${TAG}（E3 参照图回归另跑：见《修改方案_20261028_CFpurge与图片版式.md》§5 断言 E5）"
