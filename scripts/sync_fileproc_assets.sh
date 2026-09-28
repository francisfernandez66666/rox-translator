#!/usr/bin/env bash
# ============ sync_fileproc_assets.sh · 职责说明 ============
# 把仓库里的文件转换脚本（*.py）与随包资产树（assets/）**原子同步**到主站 /opt/translator/bin/，
# 同步完逐文件核对 sha256，指纹不等值即判失败。
#
# 为什么必须有这个脚本（★★ 这是 §12-D5 事故的成因，不是锦上添花）：
#   `pdf_overlay.py` 的兜底字体按**脚本同目录**定位（`<脚本目录>/assets/fonts/DroidSansFallbackFull.ttf`），
#   必须是随发版一起落盘的资产。但历史上：
#     · `deploy/` 下没有任何脚本会同步 `.py` 或 `assets/`（全 grep 实证实为零）；
#     · 《部署指南》§五的清单只列了三个 `.py`，**整个 `assets/` 从来没进过部署清单**；
#   ⇒ 现网 `/opt/translator/bin/assets/` 连目录都不存在，PDF「原版式写回」**100% 失败**
#     并被 engine 的降级链完全兜住（工单成功、产物可打开），寂寞死了一年多。
#   口头要求「记得把字体一起拷贝」是撑不住的——**必须有一个可重复执行、执行完自己核对的命令**。
#
# 为什么不枚举脚本清单而是同步所有 *.py：清单会腐化。新脚本漏列 ⇒ 症状与本次事故一模一样
#   （本地跑得好好的、线上 Keyword-NotFound）。「忘记更新清单」这类缺陷要靠机制消掉，不靠自觉。
#
# 用法：
#   # ① 部署机上跑（仓库本身就在那台机器上）
#   MODE=local bash scripts/sync_fileproc_assets.sh
#   # ② 从开发机推到生产（走 ssh；远端需要 sudo）
#   MODE=remote SSH_HOST=ubuntu@<主站IP> SSH_KEY=~/.ssh/id_rsa bash scripts/sync_fileproc_assets.sh
#   # ③ 演练（落到临时根，不碰 /opt，用来验证脚本本身通不通）
#   MODE=local DEST_ROOT=/tmp/fpd-drill SKIP_CHOWN=1 bash scripts/sync_fileproc_assets.sh
#
# 可选环境变量：
#   MODE            local | remote（默认 local）
#   DEST_ROOT       目标根（默认 /opt/translator），实际落点是 $DEST_ROOT/bin
#   SSH_HOST/SSH_KEY/SSH_PORT   remote 模式用（SSH_KEY 默认 ~/.ssh/id_rsa）
#   TRANSLATOR_USER 属主（默认 translator；SKIP_CHOWN=1 时跳过 chown）
# ================================================================
set -u

MODE="${MODE:-local}"
DEST_ROOT="${DEST_ROOT:-/opt/translator}"
SSH_HOST="${SSH_HOST:-}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_rsa}"
SSH_PORT="${SSH_PORT:-22}"
TRANSLATOR_USER="${TRANSLATOR_USER:-translator}"
SKIP_CHOWN="${SKIP_CHOWN:-0}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$REPO_ROOT/backend-go/internal/fileproc"
DEST_BIN="$DEST_ROOT/bin"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  ✅ %s\n' "$*"; }
bad()  { printf '\033[1;31m❌\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m⚠️\033[0m %s\n' "$*"; }

# ---------------------------------------------------------------- 前置校验
[ -d "$SRC" ] || { bad "找不到源目录 ${SRC}（请在仓库根目录下执行本脚本）"; exit 2; }

# 必须存在的资产：缺任何一个都必须停手——半套同步会让线上跑一半的逻辑，**比不同步更危险**。
MUST_HAVE=(assets/fonts/DroidSansFallbackFull.ttf assets/fonts/LICENSE-NOTE.txt)
for rel in "${MUST_HAVE[@]}"; do
  if [ ! -s "$SRC/$rel" ]; then
    bad "仓库缺少或为空：$SRC/$rel ⇒ 停手（D5 的根因就是这个部件没落过地，绝不放半成品上机）"
    exit 3
  fi
done

# 待同步清单：顶层 *.py + 整个 assets/ 树
# ⚠️ 不用 mapfile：macOS 自带 bash 是 3.2（README 就是这么写好的），为了「本机也能演练」一律用 read 回填。
PY_SCRIPTS=()
while IFS= read -r name; do PY_SCRIPTS+=("$name"); done < <(cd "$SRC" && ls -1 *.py 2>/dev/null | LC_ALL=C sort)
[ "${#PY_SCRIPTS[@]}" -gt 0 ] || { bad "$SRC 下没有 .py，清单异常 ⇒ 停手"; exit 3; }
ASSETS=()
while IFS= read -r rel; do ASSETS+=("$rel"); done < <(cd "$SRC" && find assets -type f | LC_ALL=C sort)
[ "${#ASSETS[@]}" -gt 0 ] || { bad "$SRC 下 assets/ 为空 ⇒ 停手"; exit 3; }

log "同步对象：${#PY_SCRIPTS[@]} 个脚本 + ${#ASSETS[@]} 个资产文件 → $DEST_BIN"
log "  ${PY_SCRIPTS[*]}"
log "  ${ASSETS[*]}"

# ---------------------------------------------------------------- 暂存档
STAGE="$(mktemp -d "${TMPDIR:-/tmp}/fpsync.XXXXXX")"
cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT

for f in "${PY_SCRIPTS[@]}"; do cp "$SRC/$f" "$STAGE/$f" || exit 4; done
for rel in "${ASSETS[@]}"; do
  mkdir -p "$STAGE/$(dirname "$rel")"
  cp "$SRC/$rel" "$STAGE/$rel" || exit 4
done

SHA_LOCAL_LIST="$STAGE/.local_sha"
: >"$SHA_LOCAL_LIST"
if command -v shasum >/dev/null 2>&1; then DIGEST() { shasum -a 256 "$1" | awk '{print $1}'; }
else DIGEST() { sha256sum "$1" | awk '{print $1}'; }; fi
for f in "${PY_SCRIPTS[@]}"; do printf '%s  %s\n' "$(DIGEST "$STAGE/$f")" "$f" >>"$SHA_LOCAL_LIST"; done
for rel in "${ASSETS[@]}"; do printf '%s  %s\n' "$(DIGEST "$STAGE/$rel")" "$rel" >>"$SHA_LOCAL_LIST"; done

# ---------------------------------------------------------------- 安装动作
# 统一在目标机上跑一段安装脚本：local 直执行，remote 经 ssh 投过去执行。
CHOOWN_LINE="chown -R $TRANSLATOR_USER:$TRANSLATOR_USER $DEST_BIN/assets $DEST_BIN"
[ "$SKIP_CHOWN" = "1" ] && CHOOWN_LINE="echo '  (SKIP_CHOWN=1，跳过属主修改)'"
gen_install_script() {
  cat <<EOS
set -e
mkdir -p $DEST_BIN/assets/fonts
EOS
  for f in "${PY_SCRIPTS[@]}"; do
    printf 'install -m 0755 "$STAGE/%s" "%s/%s"\n' "$f" "$DEST_BIN" "$f"
  done
  # 不用 `install -D`（BSD 版 install 没这个选项）：手写 mkdir + cp + chmod，两侧都可跑。
  for rel in "${ASSETS[@]}"; do
    printf 'mkdir -p "$(dirname "%s/%s")" && cp "$STAGE/%s" "%s/%s" && chmod 0644 "%s/%s"\n' \
      "$DEST_BIN" "$rel" "$rel" "$DEST_BIN" "$rel" "$DEST_BIN" "$rel"
  done
  printf '%s\n' "$CHOOWN_LINE"
  for f in "${PY_SCRIPTS[@]}"; do
    printf 'echo "INSTALLED %s/%s"\n' "$DEST_BIN" "$f"
  done
  for rel in "${ASSETS[@]}"; do
    printf 'echo "INSTALLED %s/%s"\n' "$DEST_BIN" "$rel"
  done
}
gen_install_script >"$STAGE/install.sh"

if [ "$MODE" = "local" ]; then
  log "[1/2] 本地安装到 $DEST_BIN"
  STAGE="$STAGE" bash "$STAGE/install.sh" || { bad "本地安装失败（需要 root/属主权限 ⇒ 试试 sudo -E，或先 SKIP_CHOWN=1 演练）"; exit 5; }
else
  [ -n "$SSH_HOST" ] || { bad "MODE=remote 必须给 SSH_HOST=user@host"; exit 2; }
  SSH_OPTS=(-i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=15 -p "$SSH_PORT")
  log "[1/2] 推送并安装到 ${SSH_HOST}:${DEST_BIN}"
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" 'mkdir -p /tmp/fpsync-staging' || { bad "ssh 不通（BatchMode=yes 下多为密钥不可读，见排障说明）"; exit 4; }
  rsync -az -e "ssh -i $SSH_KEY -o BatchMode=yes -p $SSH_PORT" --delete "$STAGE/" "$SSH_HOST:/tmp/fpsync-staging/" || { bad "rsync 失败"; exit 4; }
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" "sudo STAGE=/tmp/fpsync-staging bash /tmp/fpsync-staging/install.sh" || { bad "远端安装失败"; exit 5; }
fi
ok "安装完成"

# ---------------------------------------------------------------- 指纹核对
# ★ 同步完不核对＝没同步。这是 G1「脚本指纹等值」在主站这侧的同一条口径。
log "[2/2] 逐文件 sha256 核对（本地 vs ${DEST_BIN}）"
read_dest_digest() {
  # 参数：$1=相对文件名（也可能是 assets/... 这类带路径的）
  local rel="$1"
  local dest="$DEST_BIN/$rel"
  if [ "$MODE" = "local" ]; then
    [ -s "$dest" ] && DIGEST "$dest" || printf ''
  else
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" "command -v sha256sum >/dev/null 2>&1 && sha256sum '$dest' 2>/dev/null | awk '{print \$1}' || true"
  fi
}
FAIL=0
while read -r want rel; do
  got="$(read_dest_digest "$rel")"
  if [ "$want" = "$got" ]; then
    ok "$rel 等值 ${want:0:12}…"
  else
    bad "$rel 不等值/缺失  仓库=${want:0:12}  目标=${got:0:12}"
    FAIL=1
  fi
done <"$SHA_LOCAL_LIST"

# ★ 独立再验一次兜底字体：它就是 D5 的那颗雷，既要比 sha 也要比「非空」。
MUST_TTF="$DEST_BIN/assets/fonts/DroidSansFallbackFull.ttf"
if [ "$MODE" = "local" ]; then
  [ -f "$MUST_TTF" ] && TTF_SIZE="$(wc -c <"$MUST_TTF" | tr -d ' ')" || TTF_SIZE=0
else
  TTF_SIZE="$(ssh "${SSH_OPTS[@]}" "$SSH_HOST" "wc -c < '$MUST_TTF' 2>/dev/null || echo 0" | tr -d ' ')"
fi
if [ "${TTF_SIZE:-0}" -gt 1000000 ]; then
  ok "兜底字体在位且非空（${TTF_SIZE} 字节）"
else
  bad "兜底字体缺失或过小（size=${TTF_SIZE:-0}）⇒ PDF 原版式写回仍会立刻失败，别急着上线验证"
  FAIL=1
fi

if [ "$FAIL" -ne 0 ]; then
  bad "指纹核对未通过：这批资产没真正落到位。修好再往下走（此刻若触发一单 PDF，仍会降级交付）。"
  exit 6
fi
log "同步完成且逐文件指纹等值。"
log "下一步：跑 deploy_check.sh，并起一单真实大 PDF —— 验收判据是日志里出现「PDF 原地替换完成」，不是只出现「转换成功」。"
