#!/usr/bin/env bash
# ============ dispatch_sync.sh · 职责说明 ============
# 把主站（= 本仓库）的 PDF 转换脚本与字体资产**逐字**同步到体验机的派发工作区。
#
# 为什么必须有这个脚本，而不是"手工 rsync 一次就行"：
#   就绪判据里最硬的一条是**脚本内容指纹等值**（改造方案 §5.1 G1）。主站改了 pdf_overlay.py
#   而远端没跟上，两侧就会产出**不同的 PDF**——这种差异不会报错，只会让客户拿到一份
#   "看起来没问题但版式不一样"的件。所以同步必须是可重复执行、且执行完自己核对指纹的命令。
#
# 射程（只动体验机，**绝不写主站任何东西**）：
#   远端 /opt/fpdispatch/bin/ 下的脚本与 assets/fonts/ 字体；普惠体落到 /opt/fpdispatch/bin/fonts/。
#
# 用法：
#   FPD_SSH=ubuntu@1.2.3.4 FPD_KEY=~/.ssh/dispatch_ed25519 bash scripts/dispatch_sync.sh
#   （另可 FPD_FONTS_DIR=/path/to/fonts 指定普惠体所在目录；不给就跳过并点名提醒）
#   ★★ FPD_SSH 必须是「有 shell 且能 sudo」的账号（如 ubuntu@/root@）——不能给 fpd@：
#     fpd 被 sshd ForceCommand 收口，任何命令行都被顶成 fpd-bridge、只认 stdin 协议，
#     mkdir/rsync/sha256sum 全部失真（2026-11-26 广州装机实测；前置自检见 SHELL_OK）。
#
# ★★ 2026-09-28 重写说明（这次不是改小 bug，是把它从"文档齐全但一行跑不通"救回来）：
#   旧版合进仓时连 `bash -n` 都没过——第 87/91 行命令替换少一个收尾引号，跑到 91 行直接
#   `unexpected EOF looking for matching ")"`；另有 ① `"$FPD_SSH "sudo bash -s""` 把主机与
#   远端命令粘成**一个 argv**，ssh 目标必然解析失败；② `$FPD_REMOTE_ROOT` 落在单引号里靠远端
#   展开（为空），同一个命令的兜底分支却是本地展开，两支口径不一致；③ 一处韩文字符混进注释。
#   ⇒ 由此立三条：**变量名紧跟中文等多字节字符时一律写 `${VAR}`**（`$VAR）` 会被 bash 把后面的
#     字节吞进变量名，报 unbound variable）、命令替换里不再嵌一层需要手工转义的引号、
#     合仓前必跑 `bash -n`。
# ================================================================
set -u

FPD_SSH="${FPD_SSH:-}"
FPD_KEY="${FPD_KEY:-$HOME/.ssh/dispatch_ed25519}"
FPD_REMOTE_ROOT="${FPD_REMOTE_ROOT:-/opt/fpdispatch}"
FPD_FONTS_DIR="${FPD_FONTS_DIR:-}"
FPD_PORT="${FPD_PORT:-22}"
STAGING="${STAGING:-/tmp/fpd-staging}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$REPO_ROOT/backend-go/internal/fileproc"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  ✅ %s\n' "$*"; }
bad()  { printf '\033[1;31m❌\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m⚠️\033[0m %s\n' "$*"; }

[ -n "$FPD_SSH" ] || { bad "必须给 FPD_SSH=user@host（本机 ssh 的目标就是体验机，不是主站）"; exit 2; }
[ -d "$SRC" ] || { bad "找不到源目录 ${SRC}（请在仓库根目录下执行本脚本）"; exit 2; }

SSH_OPTS=(-i "$FPD_KEY" -n -o BatchMode=yes -o ConnectTimeout=15 -p "$FPD_PORT")
# scp 与 ssh 的 opts 分家：scp 不认 ssh 的 -n（stdin /dev/null），混用必报 invalid option
SCP_OPTS=(-i "$FPD_KEY" -o BatchMode=yes -o ConnectTimeout=15 -P "$FPD_PORT")
RSH_STR="ssh -i $FPD_KEY -o BatchMode=yes -o ConnectTimeout=15 -p $FPD_PORT"

# ★ 2026-11-26 工单分块派发：chunk_translate.py（协议胶水，走 SCRIPTS 同步）
# chunkworker 是 Go 构建产物（不在源码真源目录），由下方「worker 同步」段单独构建并 scp。
SCRIPTS=(pdf_overlay.py pdfwrite.py anydoc_md.py docx_translate.py docx_table_selftest.py chunk_translate.py)
ASSETS=(assets/fonts/DroidSansFallbackFull.ttf assets/fonts/LICENSE-NOTE.txt)

log "同步对象：${#SCRIPTS[@]} 个转换脚本 + ${#ASSETS[@]} 个随包资产 + 普惠体（可选）"
log "目标：${FPD_SSH}:${FPD_REMOTE_ROOT}/bin"

# ---------------------------------------------------------------- 前置：源侧必须完整
# 半套同步会让远端跑旧逻辑 ⇒ 症状与「脚本缺失」混在一起，比不同步更难查，故一律停手。
for f in "${SCRIPTS[@]}"; do
  [ -s "$SRC/$f" ] || { bad "仓库里缺 $f ⇒ 立即停手"; exit 3; }
done
for rel in "${ASSETS[@]}"; do
  [ -s "$SRC/$rel" ] || { bad "仓库里缺 $rel ⇒ 立即停手（这一项正是现网 D5 的同款成因）"; exit 3; }
done

# ---------------------------------------------------------------- 暂存档
LOCAL="$(mktemp -d "${TMPDIR:-/tmp}/fpd-sync.XXXXXX")"
cleanup() { rm -rf "$LOCAL"; }
trap cleanup EXIT
mkdir -p "$LOCAL/payload"
for f in "${SCRIPTS[@]}"; do cp "$SRC/$f" "$LOCAL/payload/$f" || exit 4; done
for rel in "${ASSETS[@]}"; do
  mkdir -p "$LOCAL/payload/$(dirname "$rel")"
  cp "$SRC/$rel" "$LOCAL/payload/$rel" || exit 4
done

if command -v shasum >/dev/null 2>&1; then DIGEST() { shasum -a 256 "$1" | awk '{print $1}'; }
else DIGEST() { sha256sum "$1" | awk '{print $1}'; }; fi

# 本地指纹清单（先落文件再逐行比对，避免在 double-quote 里嵌套命令替换——旧版就是在这里出事的）
LIST="$LOCAL/list.txt"
: >"$LIST"
for f in "${SCRIPTS[@]}"; do printf '%s  %s\n' "$(DIGEST "$LOCAL/payload/$f")" "$f" >>"$LIST"; done
for rel in "${ASSETS[@]}"; do printf '%s  %s\n' "$(DIGEST "$LOCAL/payload/$rel")" "$rel" >>"$LIST"; done

# ---------------------------------------------------------------- 安装脚本（本地生成，远端执行）
# 为什么不用「一堆命令管道喂给远端 bash」：中间只要有一条要拼变量就得手工转义，
# 正是旧版粘 argv、引号错位的根源。生成本地文件 → 一次 rsync → 一次 ssh 执行，稳得多。
INST="$LOCAL/install.sh"
{
  printf 'set -e\n'
  printf 'B="%s/bin"\n' "$FPD_REMOTE_ROOT"
  printf 'mkdir -p "$B/assets/fonts" "$B/fonts"\n'
  for f in "${SCRIPTS[@]}"; do
    printf 'cp "%s/scripts/%s" "$B/%s" && chmod 0755 "$B/%s"\n' "$STAGING" "$f" "$f" "$f"
  done
  for rel in "${ASSETS[@]}"; do
    printf 'mkdir -p "$(dirname "$B/%s")" && cp "%s/scripts/%s" "$B/%s" && chmod 0644 "$B/%s"\n' \
      "$rel" "$STAGING" "$rel" "$rel" "$rel"
  done
  printf 'chown -R fpd:fpd "$B" || true\n'
  printf 'echo INSTALL_DONE\n'
} >"$INST"

log "[0/4] 通道自检（FPD_SSH 必须是有 shell 且能 sudo 的账号；fpd@ 会被 ForceCommand 顶成 bridge）"
SHELL_PROBE="$(ssh "${SSH_OPTS[@]}" "$FPD_SSH" "echo SHELL_OK" 2>&1 || true)"
case "$SHELL_PROBE" in
  *SHELL_OK*) ok "命令行通道可用（echo 真回显）" ;;
  *"[fpd-bridge]"*|*"stdin 缺 header"*)
    bad "FPD_SSH=$FPD_SSH 被 ForceCommand 收口（命令被顶成 fpd-bridge）⇒ 本脚本全部命令行形态在这台机器上结构性失效"
    bad "改用可 sudo 的账号（ubuntu@/root@）；或把本脚本全协议化后再拨 fpd@"
    exit 4 ;;
  *)
    bad "ssh 命令行不通（BatchMode=yes 下多为私钥不可读/主机键未钉/端口不对）——probe 输出：${SHELL_PROBE:0:120}"
    exit 4 ;;
esac

log "[1/4] 传到体验机暂存区 ${STAGING}"
ssh "${SSH_OPTS[@]}" "$FPD_SSH" "mkdir -p ${STAGING}/scripts ${STAGING}/fonts" || { bad "ssh 不通（BatchMode=yes 下多为私钥不可读/无口令不匹配，先手工 ssh -o BatchMode=yes 探一次）"; exit 4; }
rsync -az -e "$RSH_STR" "$LOCAL/payload/" "$FPD_SSH:${STAGING}/scripts/" || { bad "脚本 rsync 失败"; exit 4; }
rsync -az -e "$RSH_STR" "$INST" "$FPD_SSH:${STAGING}/install.sh" || { bad "安装脚本 rsync 失败"; exit 4; }

log "[2/4] 普惠体（主站 /usr/share/fonts/truetype/alibaba/ 下的单文件，apt 装不出来）"
if [ -n "$FPD_FONTS_DIR" ] && [ -d "$FPD_FONTS_DIR" ]; then
  # ★ 实测教训：_zh_families() 用 fc-list（系统 fontconfig），不是扫 /opt/fpdispatch/bin/fonts。
  #   普惠体必须落到 fontconfig 会扫的目录（bootstrap 预留的 /usr/local/share/fonts/fpd-langcross），
  #   再 fc-cache，否则 family 进不了 zh_families，G3「远端 ⊇ 主站」永远差这一族。
  #   另外普惠体是两支文件（bold/regular），旧版 head -1 只取一支会丢字重，这里改为全部同步。
  #   ★ 不用 mapfile（macOS 默认 /bin/bash 是 3.2，无 mapfile）；改用临时清单 + while read。
  FONTLIST="$LOCAL/fonts.txt"
  : >"$FONTLIST"
  find "$FPD_FONTS_DIR" -maxdepth 1 \( -iname 'puhui-*.ttf' -o -iname 'AlibabaPuHuiTi*' \) 2>/dev/null | sort >"$FONTLIST"
  # ★ 先读进数组（不用 mapfile，macOS bash 3.2 无此内建）；再 for 遍历。
  #   绝不能在 while read <FONTLIST 里直接调 ssh——ssh 会吞掉 FONTLIST 剩余行，循环只跑一次。
  FONTS=()
  while IFS= read -r line; do
    [ -n "$line" ] && FONTS[${#FONTS[@]}]="$line"
  done <"$FONTLIST"
  if [ "${#FONTS[@]}" -gt 0 ]; then
    RMT_FONT_DIR="/usr/local/share/fonts/fpd-langcross"
    ssh "${SSH_OPTS[@]}" "$FPD_SSH" "sudo mkdir -p '${RMT_FONT_DIR}'" || { bad "普惠体目录建不成"; exit 4; }
    for f in "${FONTS[@]}"; do
      bn="$(basename "$f")"
      rsync -az -e "$RSH_STR" "$f" "$FPD_SSH:${STAGING}/fonts/${bn}" || { bad "普惠体 rsync 失败: ${bn}"; exit 4; }
      ssh "${SSH_OPTS[@]}" "$FPD_SSH" "sudo install -m 0644 '${STAGING}/fonts/${bn}' '${RMT_FONT_DIR}/${bn}' && sudo fc-cache -f '${RMT_FONT_DIR}' >/dev/null 2>&1 || true" || { bad "普惠体安装失败: ${bn}"; exit 5; }
      ok "已同步 ${bn}"
    done
    # 兼容旧版：派发工作区也留一份（运行时不依赖它，仅供本地核对）
    ssh "${SSH_OPTS[@]}" "$FPD_SSH" "sudo mkdir -p '${FPD_REMOTE_ROOT}/bin/fonts' && sudo cp '${RMT_FONT_DIR}'/*.ttf '${FPD_REMOTE_ROOT}/bin/fonts/' 2>/dev/null; sudo chown -R fpd:fpd '${FPD_REMOTE_ROOT}/bin/fonts' 2>/dev/null || true"
  else
    warn "  ${FPD_FONTS_DIR} 下没找到 puhui-*.ttf / AlibabaPuHuiTi* ⇒ 跳过（两侧字体族数差一个，preflight 会点红）"
  fi
else
  warn "  未给 FPD_FONTS_DIR ⇒ 跳过普惠体。**注意**：主站有普惠体而远端没有时，"
  warn "  §5.1 的「字体族 ⊇」判据会判不就绪，派发自动回本地——这是**安全侧**，不是故障。"
fi

# ★ 2026-11-26 Go worker 构建与同步（不进 SCRIPTS/ASSETS：它是构建产物，不是源码真源）
# 与主站 translator-server 同源 commit 构建；失败即退出，绝不允许"远端跑旧 worker"的半套同步。
log "[2.5/4] 构建并同步 chunkworker（Go 同源 worker）"
WORKER_BIN="${REPO_ROOT}/backend-go/chunkworker.linux"
(
  cd "${REPO_ROOT}/backend-go" || exit 1
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "${WORKER_BIN}" ./cmd/chunkworker
) || { bad "chunkworker 构建失败"; exit 2; }
scp "${SCP_OPTS[@]}" "${WORKER_BIN}" "${FPD_SSH}:${STAGING}/chunkworker" || { bad "chunkworker 上传失败"; exit 5; }
ssh "${SSH_OPTS[@]}" "$FPD_SSH" "sudo install -m 0755 '${STAGING}/chunkworker' '${FPD_REMOTE_ROOT}/bin/chunkworker'" || { bad "chunkworker 落位失败"; exit 5; }
ok "chunkworker 已落位（${WORKER_BIN##*/}）"

log "[3/4] 落到 ${FPD_REMOTE_ROOT}/bin（属主给派发账号，脚本 0755 / 资产 0644）"
ssh "${SSH_OPTS[@]}" "$FPD_SSH" "sudo bash '${STAGING}/install.sh'" || { bad "远端安装失败"; exit 5; }
ok "安装完成"

log "[4/4] 指纹核对（同步完不核对＝没同步）"
FAIL=0
while read -r want rel; do
  got="$(ssh "${SSH_OPTS[@]}" "$FPD_SSH" "command -v sha256sum >/dev/null 2>&1 && sha256sum '${FPD_REMOTE_ROOT}/bin/${rel}' 2>/dev/null | head -1 | cut -d' ' -f1 || true")"
  if [ "$want" = "$got" ]; then
    ok "$rel 等值 ${want:0:12}…"
  else
    bad "$rel 不等值/缺失  本地=${want:0:12}  远端=${got:0:12}"
    FAIL=1
  fi
done <"$LIST"

# ★ 兜底字体单独再验一次：它就是现网 D5 那颗雷，远端缺它 ⇒ 派出去的 apply 一进去就崩在水印扫描之后。
TTF_SIZE="$(ssh "${SSH_OPTS[@]}" "$FPD_SSH" "wc -c < '${FPD_REMOTE_ROOT}/bin/assets/fonts/DroidSansFallbackFull.ttf' 2>/dev/null || echo 0" | tr -d '[:space:]')"
if [ "${TTF_SIZE:-0}" -gt 1000000 ]; then
  ok "兜底字体在位且非空（${TTF_SIZE} 字节）"
else
  bad "远端兜底字体缺失或过小（size=${TTF_SIZE:-0}）⇒ 派出去的 apply 必死，别开闸"
  FAIL=1
fi

[ "$FAIL" -eq 0 ] || { bad "指纹不等值 ⇒ 主站会判不就绪并走本地路径（派发面自动失效，产品不受影响）。修好再开闸。"; exit 6; }
log "同步完成且逐文件指纹等值。下一步：主站跑 scripts/dispatch_preflight.sh（G1~G4 一致性门禁），全过再 dispatch_apply.sh 开闸。"
