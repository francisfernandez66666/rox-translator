#!/usr/bin/env bash
# ============================================================================
# deploy/install_probe_timer_20261010.sh —— 〇-AR 第 9 波 #29 的**装配入口**（把「装」这一步也变成一条命令）
#
# 这条脚本补的是「有脚本无调度」的**第二层**（10-10 现网实测才看清的形状）：
#   · 第一层：探针本体 `scripts/dispatch_probe_daily.sh` 与两份 unit 早在 2026-10-01 就建好了，
#     但四步安装命令只写在**文件头注释**里 ⇒ 10-10 只读实测 `translator-dispatch-probe.timer`
#     = not-found、两份脚本不在位、工作目录不存在 ⇒ 那只闹钟从建好那天起一次都没响过。
#   · 第二层（本脚本要堵的）：10-10 补出的安装器 `scripts/dispatch_probe_install.sh` 解决的是
#     "服务器侧怎么装"，但**"怎么把它送到服务器上"**这一步仍然只存在于人脑子里——
#     换一个人接手就会退化成"仓库里有安装器、现网没闹钟"这一同一形态。
#   ⇒ 所以这里把 上传→逐文件校验→干跑→落位→样张→enable→自检 收成一条幂等命令。
#
# 三段式（默认只走到干跑，任何写动作都要显式下令）：
#   bash deploy/install_probe_timer_20261010.sh              # 上传五件＋逐文件 sha 等值校验＋服务器侧**干跑**（不写 /opt、不碰 systemd）
#   bash deploy/install_probe_timer_20261010.sh --apply      # 再落位：两份脚本＋两份 unit＋工作目录＋样张，齐了才 `enable --now`
#   bash deploy/install_probe_timer_20261010.sh --selftest   # 装配后手工拨一次闹钟（等价 `systemctl start`，见下）
#   两个开关可叠加：`--apply --selftest`。
#
# ★ 三条刻意的设计（不是啰嗦，都是本仓点过名的死法）：
#   ① **逐文件 sha256 等值才算送到位**：只看 scp 退 0 不行——macOS 的 tar 会把 `._*` AppleDouble
#      一起带上公网（AGENTS 与《部署指南》治理段记过两次），而"文件在"与"文件就是仓库那一份"是两件事。
#      打包侧 `COPYFILE_DISABLE=1` ＋ `--exclude='._*'`，校验侧对**六个路径逐个**比 sha，任一不符即停在干跑段。
#   ② **样张在服务器侧现造**，不把 24 MB 从本机推上去：`scripts/make_probe_pdf.py` 就是为此存在的
#      （每页一句真中文＋噪声图抬体积，不经模型 ⇒ 压的是传输与转换两条腿），
#      造完仍要交给安装器的 `--sample` 判据过一遍（体积 ∧ 页数 ∧ **页数必须读得出来**，
#      零值/读不出一律拒绝放行＝§一·12「就绪判据不许吃零值」）。
#   ③ **--selftest 才会真拨一次远端**，默认不拨。理由：探针会向体验机搬一次文件、
#      并可能向 `/api/alerts/alertmanager` 投一条告警（红的时候），那是现网写面；
#      它按设计**绝不动配置、绝不重启任何服务**（静态锁＝internal/fileproc/dispatch_probe_timer_test.go），
#      但"每天自己拨"这件事的证据只有响一次才有 ⇒ 装配当次补一次人工触发，读数只数不贴正文（遮 URL/hex）。
#
# 退出码：0=本段该做的事都做完且校验通过；1=干跑有未闭合的待办（未 --apply）或样张/闹钟判据不成立；
#         2=硬前置不满足（解析不到主机／上传失败／远端 sha 不符／远端脚本退 2）。
# ============================================================================
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$REPO/deploy/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$CONF" | head -1)"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$CONF" | head -1)"
[ -n "$HOST" ] || { echo "ABORT=2 主机没解析出来（读 $CONF，别在命令行里写死地址）"; exit 2; }
[ -n "$PORT" ] || PORT=22

APPLY=0
SELFTEST=0
while [ $# -gt 0 ]; do
  case "$1" in
    --apply) APPLY=1 ;;
    --selftest) SELFTEST=1 ;;
    -h|--help) awk '/^set -uo/{exit} {print}' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "未知参数：$1（--apply／--selftest／--help）" >&2; exit 2 ;;
  esac
  shift
done

# 要送上去的六件：三份脚本＋一份造件器＋两份 unit（少一件安装器就会报「仓库缺 …」）
FILES=(
  scripts/dispatch_probe_install.sh
  scripts/dispatch_probe_daily.sh
  scripts/dispatch_preflight.sh
  scripts/make_probe_pdf.py
  deploy/systemd/translator-dispatch-probe.service
  deploy/systemd/translator-dispatch-probe.timer
)
REMOTE_DIR=/root/wave9_probe
TAR_LOCAL="/tmp/wave9_probe_tree.$$".tar

say() { printf '%s\n' "$*"; }
RC=0

[ -f "$CONF" ] || { echo "ABORT=2 没有 $CONF"; exit 2; }
for f in "${FILES[@]}"; do
  [ -f "$REPO/$f" ] || { say "  FAIL 仓库缺 $f ⇒ 不上传（半套树装上去只会得到半套闹钟）"; exit 2; }
done

say "===== 派发探针闹钟装配 本机时刻=$(date -u +%FT%TZ)（$([ "$APPLY" = 1 ] && echo '含落位' || echo '只到干跑')$([ "$SELFTEST" = 1 ] && echo '＋一次人工触发' || echo '')）====="

# ---------------------------------------------------------------- ① 打包（macOS 三条坑一次堵掉）
# ①a COPYFILE_DISABLE=1 ＋ 三条 --exclude：把 AppleDouble（`._*`）与资源叉目录挡在树外。
#     不写 bsdtar 专有的 --disable-copyfile（那面 flag 在 Linux 的 GNU tar 上会当场退码，
#     把"顺手在 CI 里跑一次"变成一条无关的失败）。
# ①b --no-mac-metadata：**试过**，本机 bsdtar 认这个 flag 但实测仍然把
#     `LIBARCHIVE.xattr.com.apple.provenance` 写进头里 ⇒ 解包侧照旧逐文件吐
#     `tar: Ignoring unknown extended header keyword ...`。磁盘上没多出文件（GNU tar 只是忽略），
#     真正的问题是这些行与 stdout 混流后会把机器判读的 sha 段搅浑（10-10 首跑那次
#     读成"远端清单 12 行 vs 本机 6 行"的假红）。所以**判读侧按行形状过滤**，
#     不靠"让噪声消失"——噪声消不掉时，判据必须自己站得住。
PACK_NOTE=""
if ! ( cd "$REPO" && COPYFILE_DISABLE=1 tar --no-mac-metadata \
        --exclude='._*' --exclude='.DS_Store' --exclude='__MACOSX' \
        -cf "$TAR_LOCAL" "${FILES[@]}" ) 2>/dev/null; then
  rm -f "$TAR_LOCAL"
  ( cd "$REPO" && COPYFILE_DISABLE=1 tar \
      --exclude='._*' --exclude='.DS_Store' --exclude='__MACOSX' \
      -cf "$TAR_LOCAL" "${FILES[@]}" ) || { say "  FAIL 打包失败"; exit 2; }
  PACK_NOTE="（本机 bsdtar 不认 --no-mac-metadata，已退回 COPYFILE_DISABLE 形态：解包侧可能出现 Ignoring 噪声行，不参与判读）"
fi
# 点族判据按**路径段**判（`(^|/)\._`），不能写成不带锚的 `\.`——那会把六个 `.sh`/`.py` 全算成脏条目，
# 于是这条负向锁恒红，下一次就有人把它放宽成"不看了"（负向锁必须配得准，同 AGENTS §一·5 那条）。
DIRTY=$(tar tf "$TAR_LOCAL" | grep -cE '(^|/)(\._|\.DS_Store|__MACOSX)' || true)
say "  打包：$(wc -c <"$TAR_LOCAL" | tr -d ' ') B，条目=$(tar tf "$TAR_LOCAL" | wc -l | tr -d ' ')，dotfile/资源叉条目=$DIRTY（期望 **0**）$PACK_NOTE"
if [ "${DIRTY:-0}" != "0" ]; then
  say "  FAIL 树里混进了点族／资源叉条目 ⇒ 停下（这条链上去的东西会永久留在服务器上）"
  rm -f "$TAR_LOCAL"; exit 2
fi

# 本机逐文件 sha 清单（远端要拿它做等值对照，**只对这一批六件**）
LOCAL_SHA=$(for f in "${FILES[@]}"; do printf '%s  %s\n' "$(shasum -a 256 "$REPO/$f" | awk '{print $1}')" "$f"; done)

# ---------------------------------------------------------------- ② 上传＋远端解包＋逐文件等值
scp -P "$PORT" -o ConnectTimeout=20 -o BatchMode=yes -q "$TAR_LOCAL" "$HOST:$TAR_LOCAL" || { say "  FAIL 上传失败"; rm -f "$TAR_LOCAL"; exit 2; }
rm -f "$TAR_LOCAL"

# ⚠️ 远端输出一律**落临时文件**再读，不写成 `$(ssh … <<'EOF' … EOF)`：
#    把带括号的整段远端脚本塞进命令替换里，本机 bash 3.2 会在 heredoc 内部报语法错
#    （10-10 实测 `syntax error near unexpected token ;;`），而远端 bash 5 是好的——
#    这种「本机解释器扫不过」的红会被误读成脚本写错，白烧一轮。
REMOTE_OUT="/tmp/wave9_probe_remote_out.$$".txt
ssh -p "$PORT" -o ConnectTimeout=20 -o BatchMode=yes "root@$HOST" bash -s -- "$TAR_LOCAL" "$REMOTE_DIR" "$APPLY" "$SELFTEST" > "$REMOTE_OUT" 2>&1 <<'REMOTE_EOF'
set -uo pipefail
TAR="$1"; ROOT="$2"; APPLY="$3"; SELFTEST="$4"
BIN_DIR=/opt/translator/bin
WORK_DIR=/opt/translator/data/_dispatch_probe
SAMP="$WORK_DIR/probe.pdf"
VENVPY=/opt/translator/.venv/bin/python3
RC=0
bad() { echo "  FAIL $1"; RC=1; }
ok()  { echo "  ✔ $1"; }
# sysd <is-enabled|is-active> <unit>：unit 不存在时 systemctl **既**打 "not-found" **又**退非 0，
# 所以 `$(systemctl is-enabled X || echo '<未装>')` 会同时收进两个值，读数行被劈成三行
# （10-10 现网首跑实测形态）。这里统一取一次，读不到就回落 `<未读>`，绝不回落空串。
sysd() {
  local v
  v=$(systemctl "$1" "$2" 2>/dev/null) || true
  [ -n "$v" ] || v='<未读>'
  printf '%s' "$v"
}

# 解包到独立目录（不碰 /opt、不碰 /etc/systemd ⇒ 这一段的写面只有 /root）
rm -rf "$ROOT"; mkdir -p "$ROOT" || { echo "  FAIL 建 $ROOT 失败"; exit 2; }
tar xf "$TAR" -C "$ROOT" || { echo "  FAIL 解包失败"; exit 2; }
rm -f "$TAR"

# 逐文件 sha 等值：本机清单由 heredoc 外的变量比对更麻烦，这里先算远端清单，等值判定交回主流程读
echo "@@REMOTE_SHA_BEGIN"
for f in scripts/dispatch_probe_install.sh scripts/dispatch_probe_daily.sh scripts/dispatch_preflight.sh \
         scripts/make_probe_pdf.py deploy/systemd/translator-dispatch-probe.service deploy/systemd/translator-dispatch-probe.timer; do
  if [ -f "$ROOT/$f" ]; then
    printf '%s  %s\n' "$(sha256sum "$ROOT/$f" | cut -d' ' -f1)" "$f"
  else
    printf 'MISSING  %s\n' "$f"
  fi
done
echo "@@REMOTE_SHA_END"

echo "----- ① 服务器侧干跑（安装器自己那份判据，一行 /opt 都不写）-----"
bash "$ROOT/scripts/dispatch_probe_install.sh"
DRY_RC=$?
echo "  干跑退出码=$DRY_RC（1＝有待办，属正常提示；2＝硬前置不满足）"
[ "$DRY_RC" = 2 ] && bad "干跑退 2：硬前置不满足，--apply 也装不上"

if [ "$APPLY" = 1 ]; then
  echo
  echo "----- ② 样张：服务器侧现造（不经模型，只压传输与转换两条腿）-----"
  mkdir -p "$ROOT/tmp"
  if [ ! -x "$VENVPY" ]; then
    bad "$VENVPY 不可执行 ⇒ 造不出也核不了页数（**不许**拿 grep '/Type /Page' 那种低优先级腿代替，见 §一·12）"
  else
    MADE=$("$VENVPY" "$ROOT/scripts/make_probe_pdf.py" "$ROOT/tmp/probe.pdf" 32 2>&1 | tail -1)
    echo "  造件读数：$MADE"
    case "$MADE" in
      @@PDF_MADE*pages=32*) ok "样张已造好（32 页）" ;;
      *) bad "造件器没出 @@PDF_MADE 读数 ⇒ 这一格判未验证，不拿它当样张" ;;
    esac
    if [ -s "$ROOT/tmp/probe.pdf" ]; then
      echo "----- ③ 落位＋enable（四件齐备才 enable，样张不合档就不会装闹钟）-----"
      bash "$ROOT/scripts/dispatch_probe_install.sh" --apply --sample "$ROOT/tmp/probe.pdf"
      AP_RC=$?
      echo "  执行段退出码=$AP_RC"
      [ "$AP_RC" = 0 ] || bad "--apply 退 $AP_RC：没装成（排障看上面的 FAIL 行；1 有两种含义＝『还没做』或『做了没做成』）"
    else
      bad "$ROOT/tmp/probe.pdf 没生成 ⇒ 不 --apply（没样张的闹钟每天判红，安装器按设计也不 enable）"
    fi
  fi

  # 装配后的三条读数：闹钟状态／两份脚本在位可执行／样张够档
  echo "----- ④ 装配读数（只读复验）-----"
  echo "  probe.timer enabled=$(sysd is-enabled translator-dispatch-probe.timer) active=$(sysd is-active translator-dispatch-probe.timer)"
  for f in "$BIN_DIR/dispatch_preflight.sh" "$BIN_DIR/dispatch_probe_daily.sh"; do
    [ -x "$f" ] && ok "$f 在位可执行" || bad "$f 不在位或不可执行"
  done
  if [ -s "$SAMP" ]; then
    echo "  样张在位：$(wc -c <"$SAMP" | tr -d ' ') B，属主 $(stat -c '%U:%G %a' "$SAMP" 2>/dev/null)"
  else
    bad "样张不在位（$SAMP）"
  fi
  systemctl list-timers --all --no-pager 2>/dev/null | grep -E 'dispatch' | head -3 | sed 's/^/    /'
fi

if [ "$SELFTEST" = 1 ]; then
  echo
  echo "----- ⑤ 人工拨一次（探针只读远端、绝不动配置；红才投告警）-----"
  PT_EN_NOW=$(sysd is-enabled translator-dispatch-probe.timer)
  if [ "$PT_EN_NOW" != enabled ]; then
    bad "闹钟没 enable（现读=$PT_EN_NOW）⇒ 不自检（不自启一个没装好的单元，那只会读出一条无关的失败）"
  else
    # TimeoutStartSec=2400：两条腿各含一次真机往返，这里**同步等它跑完**，
    # 因为"start 退 0"才是这一腿的判据；异步跑会让读数停在"还在跑"那一档。
    # 窗口锚点取「start 之前的那一刻」，不用 `--since "-2 min"` 这种相对写法：
    # 一次跑可能十几分钟，相对窗口会把开头那几条 @@PROBE 削掉（窗口必须盖住整轮）。
    SINCE=$(date '+%Y-%m-%dT%H:%M:%S')
    START_T=$(date +%s)
    systemctl start translator-dispatch-probe.service
    PR=$?
    echo "  systemctl start 退出码=$PR（1＝任一腿判红，这是**故意的诚实读数**：unit failed 是告警通道之外的第二条可见面），用时 $(( $(date +%s) - START_T ))s"
    # 用函数而不是把命令行塞进变量：`--since` 的值带空格时变量展开会被拆成两个参数，
    # journalctl 会把 `04:20:00` 当成**文件名**去打开（10-10 自查抓到，改用带 T 的 ISO 档＋函数）
    jl() { journalctl -u translator-dispatch-probe.service --since "$SINCE" --no-pager 2>/dev/null; }
    echo "  读数行计数（只数不贴正文）：@@PROBE 行=$(jl | grep -ac '@@PROBE' || true)，其中带 reason= 的=$(jl | grep -ac 'reason=' || true)"
    echo "  最近 20 条 @@PROBE（遮 URL／长 hex／地址类键值）："
    jl | grep -a '@@PROBE' | tail -20 \
      | sed -E 's#https?://[^ "]*#<url>#g; s/[0-9a-fA-F]{12,}/<hex>/g; s/(addr|to|from|key)=&?[[:graph:]]*/\1=<redacted>/g' | cut -c1-200 | sed 's/^/    /'
    RES=$(jl | grep -a -oE 'probe_result=[a-z_]+' | tail -1 || true)
    echo "  最后一档=${RES:-<日志里没有 probe_result>}"
    case "$RES" in
      probe_result=ok)     ok "自检：probe_result=ok ⇒ 传输腿与 Go 腿这一轮真的往返过一次" ;;
      probe_result=closed) ok "自检：dispatch 闸关着 ⇒ 探针按设计记 closed 并退 0（这一格属『无事可探』，不是故障）" ;;
      *)                   bad "自检没拿到 ok/closed（${RES:-无读数}）⇒ 闹钟虽在，这一腿的证据仍是零" ;;
    esac
    if [ "$PR" != 0 ]; then
      echo "  ⚠️ 本次手工触发退 $PR：告警通道若可达，会经 /api/alerts/alertmanager 留下一条 kind=prom:DispatchProbeFailure 的 open 行（红才投，绿不投）⇒ 定性去告警中心按这一个 kind 数行数，别按'今天有没有告警'读"
    fi
  fi
fi

# ★ 装配临时树**用完就清**（10-10 现网首跑留下的 /root/wave9_probe 是这份脚本自己造的，
#   里面既有六份脚本的副本、又有一份 24MB 样张）。为什么不停在"留着方便复查"那一档：
#   正身已经落到 /opt/translator/bin 与 /etc/systemd/system，/root 下再留一份**同名不同代**的拷贝，
#   下一次排障的人第一件事就是"这两份哪份是现役"——而本脚本每次都会重传重解（rm -rf ＋ mkdir 在最前面），
#   清掉不损失任何可重复性。没 --apply 时也一样清：干跑的产物本来就没有落位价值。
echo "----- ⑥ 清装配临时树 $ROOT（正身在 /opt/translator/bin，这里只留过路副本）-----"
if rm -rf "$ROOT"; then
  [ -d "$ROOT" ] && bad "$ROOT 没清掉（磁盘或权限异常）" || ok "临时树已清"
else
  warn "rm -rf $ROOT 退非 0 ⇒ 手动核一眼这台机器上的 /root/wave9_probe 再定"
fi
df -h / | tail -1 | sed 's/^/  根分区水位=/'

echo "REMOTE_RC=$RC"
exit "$RC"
REMOTE_EOF
SSH_RC=$?
cat "$REMOTE_OUT"
[ "$SSH_RC" = 0 ] || { say "  FAIL 远端段退 $SSH_RC（远端自己的 REMOTE_RC 见上面那行）"; RC=1; }

# ---------------------------------------------------------------- ③ 逐文件 sha 等值（本机清单 vs 远端清单）
# 先 `tr -d '\r'`（ssh 回传在某些终端配置下带 CR，而 `\r` 会让 64 位 sha 的等值比较**恒假**＝假红）
# 只按**行形状**取清单（`64 位 hex＋两个空格＋路径`，或 MISSING），不靠 BEGIN/END 标记切片：
# stdout 是块缓冲、stderr 不缓冲，两者合流后标记行与 tar 的告警行**顺序会打乱**，
# 按标记切会把告警行算进清单（10-10 首跑就是这条：本机 6 行 vs "远端 12 行"的假红）。
RSHA=$(tr -d '\r' < "$REMOTE_OUT" | grep -aE '^([0-9a-f]{64}|MISSING)  ' || true)
rm -f "$REMOTE_OUT"
# ⚠️ 两份清单必须当**两个输入文件**喂给 awk（`NR==FNR` 那套写法）：把两段字符串拼成一条流喂进去，
#    NR 恒等于 FNR ⇒ 第一支永远成立、第二支一行都不跑，这条锁就变成**恒绿**（空转锁的本形）。
LOCAL_LIST="/tmp/wave9_probe_local_sha.$$".txt
REMOTE_LIST="/tmp/wave9_probe_remote_sha.$$".txt
printf '%s\n' "$LOCAL_SHA" > "$LOCAL_LIST"
printf '%s\n' "${RSHA:-}" > "$REMOTE_LIST"
N_WANT=$(grep -c . "$LOCAL_LIST" || true)
N_GOT=$(grep -c . "$REMOTE_LIST" || true)
MISMATCH=$(awk 'NR==FNR { want[$2]=$1; next }
                $2 != "" { if ($1 != want[$2]) print "  " $2 " 远端=" (length($1) >= 16 ? substr($1,1,16) : $1) " 本机=" substr(want[$2],1,16) }' \
          "$LOCAL_LIST" "$REMOTE_LIST")
rm -f "$LOCAL_LIST" "$REMOTE_LIST"
if [ "${N_GOT:-0}" != "${N_WANT:-0}" ]; then
  say "  FAIL 远端清单行数=$N_GOT 与本机应传件数=$N_WANT 不符 ⇒ 有文件没落上（MISSING 也走这一条）"
  RC=1
elif [ -n "$MISMATCH" ]; then
  say "  FAIL 上传后的文件与仓库不是同一份："
  printf '%s\n' "$MISMATCH"
  RC=1
else
  say "  ✔ 六件逐文件 sha256 与仓库等值（$N_GOT 行）"
fi

echo "INSTALL_EXIT=$RC"
exit "$RC"
