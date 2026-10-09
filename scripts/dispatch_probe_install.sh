#!/usr/bin/env bash
# ============ dispatch_probe_install.sh · 职责说明 ============
# 把「每日派发探针」那一整套**一次装齐**（#29 的落账批，2026-10-10）。
#
# 为什么要专门做一个安装脚本，而不是继续让人照着注释敲（这不是洁癖，是现网读数）：
#   探针本体 scripts/dispatch_probe_daily.sh 早在 2026-10-01 就建好了，四个安装步骤一直写在
#   它自己的文件头注释里。10-10 现网只读实测：
#     systemctl is-enabled translator-dispatch-probe.timer ⇒ **not-found**
#     /opt/translator/bin/dispatch_probe_daily.sh ／ dispatch_preflight.sh ⇒ **都不在位**
#     /opt/translator/data/_dispatch_probe ⇒ **目录不存在**
#   ⇒ 那只闹钟**从建好那天起一次都没响过**：〇-AF 那批写下的"每天自己拨一次传输腿"，
#     实际形态是"有一份没人调度的脚本"。这正是本仓反复点名的「有脚本无调度」——
#     证据链看着齐（脚本在、unit 在、文档在），运行面是零。
#   所以修法不是"记得装"，而是**把装这一步变成一条幂等命令 + 一份缺件就拒绝 enable 的判据**。
#
# ★ 判据取向（本脚本唯一"可能被人嫌麻烦"的设计，理由写在这）：
#   四件齐备（两脚本落位／两 unit 落位／工作目录可写／**样张够档**）才 `enable --now`。
#   样张缺失时**不装闹钟**——因为探针的设计就是"无样张判红"（那是设计不是故障，见
#   dispatch_probe_daily.sh 判读口径第 2 条），装上去等于每天给告警中心送一条假故障，
#   而一条天天红的闹钟会在两周内把真正 reason=probe_red 那次静默降级淹掉。
#   想要"先装闹钟、接受每天判红"的排障形态，显式加 --allow-no-sample（不推荐）。
#
# 用法：
#   主站 root 本机：bash dispatch_probe_install.sh            # 只读干跑：打四件现状＋待办
#   主站 root 本机：bash dispatch_probe_install.sh --apply     # 真正落位并 enable
#   带样张：        bash dispatch_probe_install.sh --apply --sample /path/to/big.pdf
#   反证/调试口：   --root <目录> 把 /etc／/opt 的写点整体换到临时目录（**只用于测试**，
#                  见下面 ENV 段说明；换过 root 后 enable 不会真动系统 systemd）
#
# 退出码：0=已就位（或干跑无待办）；
#         1=缺件且未 --apply（提示该做什么）——**以及**执行段某一步没落上（拷贝/样张落位
#            走的是 bad() 那条腿，它只把 RC 置 1，不直接 exit），所以 1 有两种含义：
#            "还没做"和"做了但没做成"。排障时看输出里的 `FAIL` 行区分，别只看码。
#         2=硬前置不满足（未知参数／非 root 落位／无 systemd／三个写点目录建不起来／
#            daemon-reload 失败／enable 失败）。
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPLY=0
SAMPLE_SRC=""
ALLOW_NO_SAMPLE=0
ROOT_OVERRIDE=""

BIN_DIR_DEFAULT=/opt/translator/bin
UNIT_DIR_DEFAULT=/etc/systemd/system
WORK_DIR_DEFAULT=/opt/translator/data/_dispatch_probe
SECRETS_DEFAULT=/etc/translator/secrets.env

PROBE_SCRIPT=dispatch_probe_daily.sh
PREFLIGHT_SCRIPT=dispatch_preflight.sh
UNITS=(translator-dispatch-probe.service translator-dispatch-probe.timer)
TIMER_NAME=translator-dispatch-probe.timer
# fpdprobe 二进制在位只是**提示项**：探针自己会把它判成 reason=probe_binary_missing 并出声，
# 所以安装器不在这里代编译（跨平台编译属发版链，写进本脚本会变成"装闹钟顺手改了产物"）。
FPDPROBE_PATH_DEFAULT=/opt/translator/bin/fpdprobe

while [ $# -gt 0 ]; do
  case "$1" in
    --apply) APPLY=1 ;;
    --sample) SAMPLE_SRC="${2:-}"; shift ;;
    --allow-no-sample) ALLOW_NO_SAMPLE=1 ;;
    --root) ROOT_OVERRIDE="${2:-}"; shift ;;
    -h|--help) awk '/^set -uo/{exit} {print}' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "未知参数：$1（--apply/--sample <路径>/--allow-no-sample/--root <目录>）" >&2; exit 2 ;;
  esac
  shift
done

BIN_DIR="$BIN_DIR_DEFAULT"; UNIT_DIR="$UNIT_DIR_DEFAULT"; WORK_DIR="$WORK_DIR_DEFAULT"; SECRETS_FILE="$SECRETS_DEFAULT"
if [ -n "$ROOT_OVERRIDE" ]; then
  # ★ 测试口：把四个绝对写点整体挪到临时目录树下，好让单测**真跑**拷贝与判据分支。
  #   为什么需要它而不是静态 grep 锁：本脚本的判据是"位置与条件"（缺样张时不许 enable），
  #   grep 只能证明某行存在，证不了"这一档走的是那一条分支"（AGENTS §一·11 同族教训）。
  BIN_DIR="$ROOT_OVERRIDE/opt/translator/bin"
  UNIT_DIR="$ROOT_OVERRIDE/etc/systemd/system"
  WORK_DIR="$ROOT_OVERRIDE/opt/translator/data/_dispatch_probe"
  SECRETS_FILE="$ROOT_OVERRIDE/etc/translator/secrets.env"
fi

RC=0
say()  { printf '%s\n' "$*"; }
ok()   { printf '  ✔ %s\n' "$1"; }
warn() { printf '  ⚠️ %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1"; RC=1; }

# ---------------------------------------------------------------- 档位现读
# 样张的两个门槛必须跟**现役配置**一致，否则"装了个不够档的件"＝闹钟天天判红。
# 口径：env 文件里显式配了就用它，没配才用代码默认（20MiB／30 页）——
# 代码默认来自 internal/fileproc/fileproc_remote.go 的 dispatchMinBytes/dispatchMinPages，
# 这里是**抄来的第二份数字**，所以必须打出来让人核，并且改动那两侧常量时同步这一行。
MIN_MB=20
MIN_PAGES=30
MIN_MB_SRC="代码默认"
MIN_PAGES_SRC="代码默认"
if [ -f "$SECRETS_FILE" ]; then
  v=$(grep -E '^\s*(export\s+)?FILEPROC_DISPATCH_MIN_MB=' "$SECRETS_FILE" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d "\"' " || true)
  [ -n "$v" ] && { MIN_MB="$v"; MIN_MB_SRC="env 文件"; }
  p=$(grep -E '^\s*(export\s+)?FILEPROC_DISPATCH_MIN_PAGES=' "$SECRETS_FILE" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d "\"' " || true)
  [ -n "$p" ] && { MIN_PAGES="$p"; MIN_PAGES_SRC="env 文件"; }
fi
case "$MIN_MB" in *[!0-9]* | "") MIN_MB=20; MIN_MB_SRC="env 值不可解析⇒回落代码默认" ;; esac
case "$MIN_PAGES" in *[!0-9]* | "") MIN_PAGES=30; MIN_PAGES_SRC="env 值不可解析⇒回落代码默认" ;; esac
MIN_BYTES=$((MIN_MB * 1024 * 1024))

# 页数读数：优先真解析器（pymupdf），没有就**拒绝判**而不是猜。
# 为什么不退回 grep '/Type /Page'：那正是 internal/fileproc 里 PdfPageCount 被列为**最低优先级**
# 的那条腿（流式页标记），拿它当唯一判据会把"实际 5 页但标记多"的件放进来——
# AGENTS §一·12 那条「页数只有一份事实源」说的就是这类件：判据必须同源，不许各数一遍。
pdf_pages() {
  local f="$1" py=""
  # PDF_PYTHON 是**显式指认口**（测试与"venv 不在默认路径"的机器用）：判据不许靠猜解释器，
  # 猜不到就返回空 ⇒ 上层**拒绝判**而不是拿粗判放行（同 §一·12「页数只有一份事实源」）。
  for c in "${PDF_PYTHON:-}" /opt/translator/.venv/bin/python3 "$(command -v python3 || true)" \
           "$HOME/.venvs/langcross-fileproc/bin/python3"; do
    [ -n "$c" ] && [ -x "$c" ] || continue
    "$c" -c 'import pymupdf' >/dev/null 2>&1 && { py="$c"; break; }
  done
  [ -n "$py" ] || { echo ""; return 1; }
  "$py" -c 'import sys, pymupdf; d=pymupdf.open(sys.argv[1]); print(d.page_count); d.close()' "$f" 2>/dev/null
}

MODE="干跑"
[ "$APPLY" = 1 ] && MODE="执行"
say "===== 每日派发探针安装器（$MODE）====="
say "档位：体积 ≥ ${MIN_MB}MiB（来源：${MIN_MB_SRC}） 页数 ≥ ${MIN_PAGES}（来源：${MIN_PAGES_SRC}）"
say "写点：bin=$BIN_DIR unit=$UNIT_DIR work=$WORK_DIR"

# ---------------------------------------------------------------- ① 两份脚本
need_apply=0
for pair in "$PROBE_SCRIPT:$REPO_ROOT/scripts/$PROBE_SCRIPT" "$PREFLIGHT_SCRIPT:$REPO_ROOT/scripts/$PREFLIGHT_SCRIPT"; do
  name="${pair%%:*}"; src="${pair#*:}"
  dst="$BIN_DIR/$name"
  if [ ! -f "$src" ]; then bad "仓库里找不到 $src ⇒ 无法安装（先核 scripts/ 是否完整）"; continue; fi
  if [ -f "$dst" ] && cmp -s "$src" "$dst"; then
    ok "$name 已在位且与仓库逐字节一致（$dst）"
  else
    say "  待办：$name $( [ -f "$dst" ] && echo '在位但与仓库不一致（漂移）' || echo '<不在位>' ) ⇒ 从 $src 落位并 chmod 0755"
    need_apply=1
  fi
done

# ---------------------------------------------------------------- ② 两份 unit
unit_src_dir="$REPO_ROOT/deploy/systemd"
for u in "${UNITS[@]}"; do
  src="$unit_src_dir/$u"
  [ -f "$src" ] || { bad "仓库缺 $src"; continue; }
  if [ -f "$UNIT_DIR/$u" ] && cmp -s "$src" "$UNIT_DIR/$u"; then
    ok "$u 已在位且与仓库一致"
  else
    say "  待办：$u $( [ -f "$UNIT_DIR/$u" ] && echo '在位但不一致' || echo '<不在位>' ) ⇒ 复制到 $UNIT_DIR"
    need_apply=1
  fi
done

# ---------------------------------------------------------------- ③ 工作目录
if [ -d "$WORK_DIR" ] && [ -w "$WORK_DIR" ]; then
  ok "工作目录可写：$WORK_DIR"
else
  say "  待办：$WORK_DIR $( [ -d "$WORK_DIR" ] && echo '存在但当前身份不可写' || echo '<不存在>' ) ⇒ mkdir -p 并 chown translator:translator"
  need_apply=1
fi

# ---------------------------------------------------------------- ④ 样张
SAMPLE="$WORK_DIR/probe.pdf"
sample_ok=0
if [ -n "$SAMPLE_SRC" ]; then
  if [ ! -f "$SAMPLE_SRC" ]; then
    bad "--sample 指的文件不存在：$SAMPLE_SRC"
  else
    sz=$(wc -c <"$SAMPLE_SRC" 2>/dev/null | tr -d ' ')
    pg=$(pdf_pages "$SAMPLE_SRC")
    say "  样张候选：$SAMPLE_SRC size=${sz:-?}B pages=${pg:-<取不到页数，本机无带 pymupdf 的 python>}"
    if [ -z "$pg" ]; then
      bad "页数读不出来 ⇒ **不许**放行（零值放行是本仓点名的死法）。装 pymupdf 或换一台有解析器的机器再跑。"
    elif [ "${sz:-0}" -lt "$MIN_BYTES" ]; then
      bad "样张不够档：${sz:-0}B < ${MIN_BYTES}B（${MIN_MB}MiB）⇒ 这份件根本不会被派发，探针会判红"
    elif [ "$pg" -lt "$MIN_PAGES" ]; then
      bad "样张页数不够档：$pg < $MIN_PAGES ⇒ 同上（派发资格＝体积 ∧ 页数，两条同时成立才派）"
    else
      sample_ok=1
      say "  待办：把这份够档的样张放到 $SAMPLE（属主 translator，权限 0644）"
      need_apply=1
    fi
  fi
else
  if [ -f "$SAMPLE" ]; then
    sz=$(wc -c <"$SAMPLE" 2>/dev/null | tr -d ' ')
    pg=$(pdf_pages "$SAMPLE")
    if [ -n "$pg" ] && [ "${sz:-0}" -ge "$MIN_BYTES" ] && [ "$pg" -ge "$MIN_PAGES" ]; then
      ok "样张已在位且够档：$SAMPLE size=${sz}B pages=${pg}"
      sample_ok=1
    else
      say "  待办：$SAMPLE 在位但**不够档或页数读不出**（size=${sz:-?} pages=${pg:-<读不到>}）⇒ 换一份 ≥${MIN_MB}MiB ∧ ≥${MIN_PAGES}页 的件"
      need_apply=1
    fi
  else
    say "  待办：样张缺失（$SAMPLE）⇒ 没有样张这只闹钟**每天判红**，所以本安装器默认不 enable"
    say "        造一份够档的合成件（不经模型、只压传输与转换两条腿）：仓库 1001_08AF 那批的"
    say "        make_probe_pdf.py，或用现网任意一份够档真实件复制到 $SAMPLE"
    need_apply=1
  fi
fi

# ---------------------------------------------------------------- ⑤ 现状读数（提示项，不拦）
if command -v systemctl >/dev/null 2>&1 && [ -z "$ROOT_OVERRIDE" ]; then
  en=$(systemctl is-enabled "$TIMER_NAME" 2>/dev/null || echo '<未装>')
  ac=$(systemctl is-active "$TIMER_NAME" 2>/dev/null || echo '<未装>')
  say "闹钟现状：is-enabled=$en is-active=$ac"
  [ -x "$FPDPROBE_PATH_DEFAULT" ] && ok "fpdprobe 在位：$FPDPROBE_PATH_DEFAULT" \
    || say "  待办：$FPDPROBE_PATH_DEFAULT <不在位> ⇒ 探针那一腿会记 reason=probe_binary_missing（它属**发版链**产物，本脚本不代编译：本机 GOOS=linux go build -o … ./cmd/fpdprobe 再随二进制一同落位）"
else
  say "闹钟现状：本机无 systemctl 或走了 --root（测试形态）⇒ 不读系统 unit 状态"
fi

if [ "$APPLY" != 1 ]; then
  say ""
  if [ "$need_apply" = 1 ]; then
    # 待办文案必须**分别**指到那一条缺的件上：旧写法只问 sample_ok，于是"样张够档但两份脚本漂移"
    # 这种形态会被提示成"还需 --sample 指一份够档的件"——照提示做完全做不对。
    hint=""
    [ "$sample_ok" = 1 ] || hint="--sample 指一份够档的件"
    [ -z "$SAMPLE_SRC" ] && [ -f "$SAMPLE" ] && hint="换一份够档的样张（现件 size/页数不合档）"
    say "干跑结论：**有上面列出的待办**。带 --apply 重跑即落位。还需：${hint:-无额外条件}"
    exit 1
  fi
  say "干跑结论：四件齐备，无需改动。"
  exit "$RC"
fi

# ---------------------------------------------------------------- 执行
# ★ --root 是**测试形态**：整段执行都不要求 root、不碰 systemd。
#   为什么：这一档存在的理由就是"在临时目录里真跑一遍拷贝与判据"（见上面 ENV 段），
#   而旧写法把 root/systemd 两道硬检查放在它前面 ⇒ 单测里**唯一能进到的分支只有开头那句 return**，
#   落位与 enable 判据那几条腿一行都没跑过，测试却报"跑过了"。现在按 root 的用途放行。
if [ -z "$ROOT_OVERRIDE" ]; then
  [ "$(id -u)" = "0" ] || { bad "落位需要 root（要写 $UNIT_DIR 与 systemctl）"; exit 2; }
  command -v systemctl >/dev/null 2>&1 || { bad "本机没有 systemd，装不了闹钟"; exit 2; }
fi

# ★ 三个写点目录**先建齐再拷**：旧形态只建 BIN_DIR 与 WORK_DIR，$UNIT_DIR 留给"反正生产上存在"
#   这一假设 ⇒ --root 测试形态里那个假想根目录根本没有 etc/systemd/system，
#   两份 unit 的 cp 各报一次 "No such file or directory"、RC 翻 1（cp 失败走 bad 那条腿），
#   而 dry-run 早把"复制到 $UNIT_DIR"写成待办放行——测试形态跑不到落位判据，
#   就等于这条安装链从没被跑过（现网实测：10-10 首跑）。
mkdir -p "$BIN_DIR" "$UNIT_DIR" "$WORK_DIR" \
  || { bad "建写点目录失败（$BIN_DIR / $UNIT_DIR / $WORK_DIR）"; exit 2; }
for pair in "$PROBE_SCRIPT:$REPO_ROOT/scripts/$PROBE_SCRIPT" "$PREFLIGHT_SCRIPT:$REPO_ROOT/scripts/$PREFLIGHT_SCRIPT"; do
  name="${pair%%:*}"; src="${pair#*:}"
  [ -f "$src" ] || continue
  install -m 0755 "$src" "$BIN_DIR/$name" && ok "已落 $BIN_DIR/$name" || bad "落 $name 失败"
done
for u in "${UNITS[@]}"; do
  src="$unit_src_dir/$u"
  [ -f "$src" ] || continue
  cp "$src" "$UNIT_DIR/$u" && ok "已落 $UNIT_DIR/$u" || bad "落 $u 失败"
done
if id translator >/dev/null 2>&1; then
  chown -R translator:translator "$WORK_DIR" "$BIN_DIR" 2>/dev/null || warn "chown 没全成（目录属主保持现状）"
fi
if [ -n "$SAMPLE_SRC" ] && [ "$sample_ok" = 1 ]; then
  # ★ 不用 `install -o translator`：GNU install 在账号不存在时（本机／CI／无该用户的机器）
  #   **直接失败**，会让样张落位整步红掉。先无条件 cp，再"能 chown 就 chown、不能就出声"，
  #   样张在不在位这一判据本身不受属主影响（探针以 translator 跑时才要求可读）。
  if cp "$SAMPLE_SRC" "$SAMPLE"; then
    ok "样张已落 $SAMPLE"
    if id translator >/dev/null 2>&1; then
      chown translator:translator "$SAMPLE" 2>/dev/null || warn "  样张属主没改成功（以 translator 跑探针时可能读不到）"
    fi
    chmod 0644 "$SAMPLE" 2>/dev/null || true
  else
    bad "样张落位失败：$SAMPLE_SRC ⇒ $SAMPLE"
  fi
fi

if [ -n "$ROOT_OVERRIDE" ]; then
  # 测试形态（--root）⇒ **一行 systemctl 都不碰**：假想根目录只用来验"拷贝与判据"那两条腿，
  # 让它在真机上 daemon-reload／enable 会把一个临时树当现役 unit 装进系统，那是自伤不是测试。
  say ""
  say "（--root 测试形态：跳过 daemon-reload 与 enable，只验落位与判据；系统 systemd 一字未动）"
  exit "$RC"
fi

systemctl daemon-reload || { bad "daemon-reload 失败"; exit 2; }

# ★ enable 的**前置**：四件必须齐。这一步刻意放在 daemon-reload 之后、且单独复算一次，
#   因为"刚才拷成功了"不等于"现在可跑"（磁盘满／权限错都在这中间发生）。
can_enable=1
for name in "$PROBE_SCRIPT" "$PREFLIGHT_SCRIPT"; do
  [ -x "$BIN_DIR/$name" ] || { warn "$BIN_DIR/$name 不可执行 ⇒ 不 enable"; can_enable=0; }
done
for u in "${UNITS[@]}"; do
  [ -f "$UNIT_DIR/$u" ] || { warn "缺 $UNIT_DIR/$u ⇒ 不 enable"; can_enable=0; }
done
if [ ! -f "$SAMPLE" ] && [ "$sample_ok" != 1 ] && [ "$ALLOW_NO_SAMPLE" != 1 ]; then
  warn "样张不在位 ⇒ **不 enable**（每天判红的闹钟会把真故障淹掉）。要接受请显式 --allow-no-sample。"
  can_enable=0
fi

if [ "$can_enable" = 1 ]; then
  # enable **且** --now：旧账里这条特意点过"别只 enable 不 --now"——只 enable 的单元
  # 在下一个日历点前不装填，读数面上看像"装好了"，实际 still 一次没跑。
  systemctl enable --now "$TIMER_NAME" && ok "闹钟已启用：$(systemctl is-enabled "$TIMER_NAME" 2>/dev/null)/$(systemctl is-active "$TIMER_NAME" 2>/dev/null)" \
    || { bad "systemctl enable --now $TIMER_NAME 失败"; RC=2; }
  say ""
  say "自检（别等明天 05:20）：systemctl start translator-dispatch-probe.service"
  say "  systemctl start translator-dispatch-probe.service && journalctl -u translator-dispatch-probe.service -n 50"
  say "  读数行前缀 @@PROBE；probe_result=ok 才算这一腿真的通，reason= 点明卡在哪一档。"
fi

exit "$RC"
