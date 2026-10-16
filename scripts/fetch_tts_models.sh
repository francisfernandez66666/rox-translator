#!/usr/bin/env bash
# ============ fetch_tts_models.sh · 职责说明 ============
# 取 TTS **多语档**（Supertonic-3 int8，八档 ja/ko/fr/de/es/pt/ru/ar）权重并逐档核对 sha256。
#
# 为什么单独有一个脚本（不是并进 sync_fileproc_assets.sh）：
#   权重的口径是「**不进 git**」（几百 MB，且许可各异），由部署负责人在部署机上一次取好。
#   sync 脚本同步的是"仓库里有的东西"（*.py + assets/ 树 = 脚本与 README），它物理上搬不动权重。
#   而"记得手动 wget 再解包再核 sha"是撑不住的——同一族事故（D5：整个 assets/ 从没进过部署清单）
#   就是这个形态。故取权重这件事要有**一条可重复执行、执行完自己核对**的命令。
#
# 用法：
#   # ① 部署机上跑（仓库在那台机器上）：落到 backend-go/internal/fileproc/assets/tts/supertonic/
#   bash scripts/fetch_tts_models.sh
#   # ② 开发机推到主站（走 ssh）
#   SSH_HOST=<别名或 user@host> bash scripts/fetch_tts_models.sh --remote
#   # ③ 只核对不下载（tar 已在缓存里、或只想确认目标机文件齐且 sha 对）
#   bash scripts/fetch_tts_models.sh --check
#   SSH_HOST=<...> bash scripts/fetch_tts_models.sh --remote --check
#
# 可选环境变量：
#   SSH_HOST      目标机（可为 ~/.ssh/config 里的别名，如 SSH_HOST=seoul；remote 模式必给）
#   SSH_KEY       identity 文件（不给＝按 ~/.ssh/config 走；给别名时**别**默认成 id_rsa，会顶掉别名的 IdentityFile）
#   SSH_PORT      ssh 端口（不给＝按 ~/.ssh/config，别名自带端口时尤其不要给）
#   DEST          目标目录（默认仓库里的 assets/tts/supertonic；remote 默认 /opt/translator/bin/assets/tts/supertonic，
#                 演示站要显式给 DEST=/opt/translator-demo/bin/assets/tts/supertonic）
#   CACHE_DIR     tarball 缓存目录（默认 ~/.cache/langcross-tts）
# ================================================================
set -u

# Supertonic-3 int8：源与指纹（改动这两处＝换权重版本，必须同步 assets/tts/supertonic/README.md）
TARBALL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/sherpa-onnx-supertonic-3-tts-int8-2026-05-11.tar.bz2"
TARBALL_SHA="82fa96f91c4ef8abaae3a14a3f4153facf88bed821d1f7331cec2700f432c427"
TARBALL_NAME="$(basename "$TARBALL_URL")"
# 七件套（与 tts_speak.py 的 SUPERTONIC_FILES / tts.go 的 ttsSupertonicFiles 逐项等值，锁 L5）
TTS_FILES=(
  duration_predictor.int8.onnx
  text_encoder.int8.onnx
  vector_estimator.int8.onnx
  vocoder.int8.onnx
  tts.json
  unicode_indexer.bin
  voice.bin
)

MODE="local"
CHECK_ONLY=0
for arg in "$@"; do
  case "$arg" in
    --remote) MODE="remote" ;;
    --check)  CHECK_ONLY=1 ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "未知参数: ${arg}（--remote / --check）" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SSH_HOST="${SSH_HOST:-}"
# ★ 刻意**不给** SSH_KEY/SSH_PORT 默认值：给 ssh 别名（~/.ssh/config 里的 Host）时，
#   默认的 -i id_rsa / -p 22 会**顶掉**别名里的 IdentityFile 与 Port，连不上还看起来像"密钥不对"。
#   要覆盖再显式 SSH_KEY=... / SSH_PORT=... 传。
SSH_KEY="${SSH_KEY:-}"
SSH_PORT="${SSH_PORT:-}"
CACHE_DIR="${CACHE_DIR:-$HOME/.cache/langcross-tts}"
REMOTE_DEST="/opt/translator/bin/assets/tts/supertonic"
if [ "$MODE" = "remote" ]; then
  DEST="${DEST:-$REMOTE_DEST}"
else
  DEST="${DEST:-$REPO_ROOT/backend-go/internal/fileproc/assets/tts/supertonic}"
fi

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  ✅ %s\n' "$*"; }
bad()  { printf '\033[1;31m❌\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m⚠️\033[0m %s\n' "$*"; }

DIGEST() { if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; else sha256sum "$1" | awk '{print $1}'; fi; }

# ---------------------------------------------------------------- 目标机侧核对函数
# 返回目标机上某个文件是否「存在、非空、sha 等于本地同名件」。
remote_or_local_sha() { # $1=文件名  $2=本地基准文件（remote 时的 sha 期望来源）
  local name="$1" base="$2"
  if [ "$MODE" = "local" ]; then
    [ -s "$DEST/$name" ] && DIGEST "$DEST/$name" || printf ''
  else
    # ★ 必须 < /dev/null：本函数在 while read 循环里被调用，ssh 会读走循环的 stdin（同 sync 脚本那条坑）
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" \
      "test -s '$DEST/$name' && sha256sum '$DEST/$name' 2>/dev/null | awk '{print \$1}' || true" </dev/null
  fi
}

SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=15)
[ -n "$SSH_KEY" ] && SSH_OPTS+=(-i "$SSH_KEY")
[ -n "$SSH_PORT" ] && SSH_OPTS+=(-p "$SSH_PORT")
SCP_OPTS=(-o BatchMode=yes)
[ -n "$SSH_KEY" ] && SCP_OPTS+=(-i "$SSH_KEY")
[ -n "$SSH_PORT" ] && SCP_OPTS+=(-P "$SSH_PORT")

# ---------------------------------------------------------------- --check：只核对
if [ "$CHECK_ONLY" = "1" ]; then
  if [ "$MODE" = "remote" ] && [ -z "$SSH_HOST" ]; then
    bad "--remote --check 必须给 SSH_HOST=user@host"; exit 2
  fi
  log "[check] 核对 $DEST 下七件套（存在 + 非空 [+ 与 tar 原文逐字节等值]）"
  TGZ="$CACHE_DIR/$TARBALL_NAME"
  STAGE=""
  if [ -s "$TGZ" ] && [ "$(DIGEST "$TGZ")" = "$TARBALL_SHA" ]; then
    STAGE="$(mktemp -d "${TMPDIR:-/tmp}/ttscheck.XXXXXX")"
    tar -xjf "$TGZ" -C "$STAGE" >/dev/null 2>&1 || true
    BASE="$(find "$STAGE" -maxdepth 3 -type f -name duration_predictor.int8.onnx -exec dirname {} \; | head -1)"
    [ -n "$BASE" ] || BASE=""
  else
    warn "缓存里没有可信 tarball（$TGZ 缺失或 sha 不符）⇒ 只做存在性/非空核对（拿不到原文，无法逐字节等值）"
    BASE=""
  fi
  FAIL=0
  for f in "${TTS_FILES[@]}"; do
    got="$(remote_or_local_sha "$f" "")"
    if [ -z "$got" ]; then
      bad "$f 缺失或为空"; FAIL=1; continue
    fi
    if [ -n "$BASE" ] && [ -s "$BASE/$f" ]; then
      want="$(DIGEST "$BASE/$f")"
      if [ "$want" = "$got" ]; then ok "$f 等值 ${want:0:12}…"; else bad "$f 不等值  期望=${want:0:12}… 实得=${got:0:12}…"; FAIL=1; fi
    else
      ok "$f 在位（${got:0:12}…，无原文可比）"
    fi
  done
  [ -n "$STAGE" ] && rm -rf "$STAGE"
  if [ "$FAIL" -eq 0 ]; then log "核对通过。"; exit 0; fi
  bad "核对未通过。"; exit 6
fi

# ---------------------------------------------------------------- 下载 + 校验 tarball
mkdir -p "$CACHE_DIR"
TGZ="$CACHE_DIR/$TARBALL_NAME"
if [ -s "$TGZ" ] && [ "$(DIGEST "$TGZ")" = "$TARBALL_SHA" ]; then
  ok "复用缓存 tarball：$TGZ"
else
  log "[1/4] 下载 $TARBALL_NAME"
  # ★ --http1.1：本机→GitHub 的 h2 这一跳常年抖（实测 curl rc=16「HTTP2 framing layer」，
  #   与 AGENTS §一·5「重拨第二枪换 --http1.1」同一族）；--retry-all-errors 让 rc=16 也进重试。
  curl -fL --http1.1 --retry 4 --retry-all-errors --retry-delay 3 --connect-timeout 20 \
    -o "$TGZ.part" "$TARBALL_URL" || { bad "下载失败（网络或 URL 变了？）"; rm -f "$TGZ.part"; exit 3; }
  mv "$TGZ.part" "$TGZ"
fi
GOT_SHA="$(DIGEST "$TGZ")"
if [ "$GOT_SHA" != "$TARBALL_SHA" ]; then
  bad "tarball sha256 不符：期望 $TARBALL_SHA 实得 $GOT_SHA ⇒ 停手（可能是上游换了包或下载被截断）"
  exit 4
fi
ok "tarball sha256 等值 ${TARBALL_SHA:0:12}…"

log "[2/4] 解包并定位七件套"
EXTRACT="$CACHE_DIR/extract"
rm -rf "$EXTRACT"
mkdir -p "$EXTRACT"
tar -xjf "$TGZ" -C "$EXTRACT" || { bad "解包失败（缓存文件损坏？删掉 $TGZ 重跑）"; exit 3; }
SRC_DIR="$(find "$EXTRACT" -maxdepth 2 -type f -name duration_predictor.int8.onnx -exec dirname {} \; | head -1)"
[ -n "$SRC_DIR" ] || SRC_DIR="$(find "$EXTRACT" -maxdepth 1 -mindepth 1 -type d | head -1)"
[ -n "$SRC_DIR" ] || { bad "解包后找不到模型目录"; exit 3; }
for f in "${TTS_FILES[@]}"; do
  [ -s "$SRC_DIR/$f" ] || { bad "解包内容缺件：$SRC_DIR/${f}（上游包结构可能变了）"; exit 4; }
done
ok "七件套齐备于 $SRC_DIR"

# ---------------------------------------------------------------- 安装
if [ "$MODE" = "local" ]; then
  log "[3/4] 安装到 $DEST"
  mkdir -p "$DEST"
  for f in "${TTS_FILES[@]}"; do cp "$SRC_DIR/$f" "$DEST/$f" || exit 5; chmod 0644 "$DEST/$f"; done
else
  [ -n "$SSH_HOST" ] || { bad "--remote 必须给 SSH_HOST=user@host"; exit 2; }
  log "[3/4] 推送到 ${SSH_HOST}:${DEST}（经 /tmp 中转 + sudo 落位）"
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" 'rm -rf /tmp/tts-supertonic && mkdir -p /tmp/tts-supertonic' </dev/null || { bad "ssh 不通"; exit 5; }
  SCP_ARGS=()
  for f in "${TTS_FILES[@]}"; do SCP_ARGS+=("$SRC_DIR/$f"); done
  scp "${SCP_OPTS[@]}" "${SCP_ARGS[@]}" "$SSH_HOST:/tmp/tts-supertonic/" || { bad "scp 失败"; exit 5; }
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" "sudo mkdir -p '$DEST' && sudo cp /tmp/tts-supertonic/* '$DEST/' && sudo chmod 0644 '$DEST'/* && rm -rf /tmp/tts-supertonic" </dev/null || { bad "远端落位失败"; exit 5; }
fi

# ---------------------------------------------------------------- 逐档核对
log "[4/4] 逐档 sha256 核对（tar 原文 vs ${DEST}）"
FAIL=0
for f in "${TTS_FILES[@]}"; do
  want="$(DIGEST "$SRC_DIR/$f")"
  got="$(remote_or_local_sha "$f" "$SRC_DIR/$f")"
  if [ "$want" = "$got" ]; then ok "$f 等值 ${want:0:12}…"; else bad "$f 不等值/缺失  期望=${want:0:12}… 实得=${got:0:12}…"; FAIL=1; fi
done
if [ "$FAIL" -ne 0 ]; then
  bad "有文件没落到位 ⇒ 八档配音会整段降级。修好再往下走。"
  exit 6
fi

log "多语档权重落位完成且逐档等值。"
if [ "$MODE" = "remote" ]; then
  warn "★ 记住：权重刚落盘不会让就绪位自己翻——必须 ssh $SSH_HOST 'sudo systemctl restart translator'"
  warn "  （演示站是 translator-demo）才重探一次；两站各投一次、各重启一次。"
  warn "  ⚠️ 主站在 monospace 之外还有演示站 /opt/translator-demo（其 bin 目录由 DEST 指定）。"
fi
log "下一步：核对 /api/health 的 tts_langs 是否含 ja/ko/fr/de/es/pt/ru/ar，再跑一单真件配音。"
