#!/bin/bash
# ============ fpd-expiry.sh · 职责说明 ============
# 体验机侧的**到期自毁正腿**（红线③「产物必须回主站、到期本机主动清干净并拒绝服务」的执行体）。
#
# 它替代旧 `fpd-expiry.service` 里那三段 ExecStart/ExecStartPost。旧写法在本机上有两处硬伤，
# 而且都是「平时看不见、到期那天才要命」的形态：
#
#   ① **退役那条腿永远是失败的**。旧 unit 是 `User=fpd`，而 `systemctl disable --now` 需要特权，
#      fpd 账号会拿到 `Failed to disable unit: Interactive authentication required.`。
#      现网实证：2026-09-29 07:44:56 与 09-30 09:44:13 两次手工拉起，journal 里 sweep 的 JSON 回执
#      正常打出（`{"removed": [], "kept": [], "ttl_sec": 1, "expired": false}`），
#      紧跟的那行鉴权失败把整个 unit 打成 `active (exited)` 之外的 **failed** 态——
#      也就是说「闹钟到期那天会不会真的自灭」这件事，**从来没被证明过一次**，
#      而 unit 一直挂着 failed，看起来就像"已经在坏了"。
#   ② **三段是顺序无关的**。旧写法把 `disable` 放在 ExecStartPost，等于「今天跑过一次脚本」
#      就等于「到期已处理」。这与主站 2026-10-01 〇-AF/〇-AG 抓到并修掉的那只 timer 是**同一个缺陷家族**
#      （那只在 unit 里带无条件 `ExecStartPost` 的 timer，把「跑过一次」当成「到期已处理」，
#        而它实际在未到期那天正常退 0、什么都没关 ⇒ 到期日反而不会再有自动回滚）。
#
# ⇒ 本脚本把顺序钉死成四步，一步不成就**不碰 systemctl**：
#      判未到期 ⇒ 什么都不做，正常退 0（闹钟必须还在）；
#      判已到期 ⇒ 先清（sweep + rm）→ 再**坐实**（EXPIRED 标记在、工作目录真空）→ 才退役两只 timer。
#   这一档的失败语义是「**还会再来**」（timer 保持 enabled，明天 03:30 之后每次开机/触发都再试一次，
#   `Persistent=true` 还会把漏掉的那次补上），不是「今天试过一次就再也不试」。
#
# ★ 为什么退役这一步只能由脚本做、不能留在 unit 里：
#   unit 层没有任何判定能力（ExecStartPost 在 ServiceExit 之后**无条件**执行，
#   脚本的三条提前退出——非 root 退 2／未到期退 0／没坐实退 5——它一概不看）。
#   把 `disable` 写回 unit ＝ 把这个家族缺陷原样装回来。静态锁见
#   backend-go/internal/fileproc/ 里对 `fpd-expiry.service` 的「unit 内不许出现无条件 ExecStartPost」判据。
#
# ★ 凭据面：本脚本零密钥、零外呼（不 ssh、不 curl、不读主站任何东西），只做本机清理与自我退役。
#
# 用法（正常由 systemd 拉起；手工执行需 root）：
#   bash /opt/fpdispatch/bin/fpd-expiry.sh
# 排障可覆盖的入口（**只为可测性留，生产不要在命令行传**）：
#   ENV_FILE      默认 $FPD_ROOT/etc/fpd.env
#   WORK_DIR      默认 $FPD_ROOT/w          （会话目录）
#   MARKER        默认 $FPD_ROOT/EXPIRED    （到期标记）
#   EXP_DATE      默认空＝从 ENV_FILE 读；给了就以此为准（自证到期支用）
#   SWEEP_CMD     默认以 fpd 身份跑 fpdexec.py --mode sweep（传 'true' 可做零副作用自证）
#   SYSTEMCTL_BIN 默认 /bin/systemctl       （自证用假 systemctl，看它到底被不被调用）
#   FPD_USER      默认 fpd
# 退出码：0=已处理（未到期跳过 或 到期已清并退役）；2=非 root；4=到期日配置缺失/非法且判不出；
#         5=清理没坐实（**闹钟必须还在**）；6=退役指令本身失败（**闹钟还在**，可重跑）。
set -u

FPD_ROOT="${FPD_ROOT:-/opt/fpdispatch}"
ENV_FILE="${ENV_FILE:-$FPD_ROOT/etc/fpd.env}"
WORK_DIR="${WORK_DIR:-$FPD_ROOT/w}"
MARKER="${MARKER:-$FPD_ROOT/EXPIRED}"
SWEEP_CMD="${SWEEP_CMD:-}"
SYSTEMCTL_BIN="${SYSTEMCTL_BIN:-/bin/systemctl}"
FPD_USER="${FPD_USER:-fpd}"

log()  { echo "[fpd-expiry] $*"; }
err()  { echo "[fpd-expiry] ERROR $*" >&2; }

# ⚠️ 本脚本要在**赋值语句**里跑 grep 计数与 find 探测：bash 在 `set -u` 之外还常被外层 systemd 以
#   `set -e` 语义看待（本仓在 .sh 上真踩过「grep 无命中把整个脚本静默带走」），
#   所以这里**刻意不用 set -e**，每条外呼都自己收码。
if [ "$(id -u)" != "0" ]; then
  err "必须 root 运行（退役 timer 是特权操作）；当前 uid=$(id -u)"
  err "★ 别再把它交回非特权账号——旧 unit 以 User=fpd 跑，disable 那条腿 100% 鉴权失败。"
  exit 2
fi

# ---------------- 1. 读到期日（读不到就判不出，宁可让闹钟继续挂着） ----------------
EXP="${EXP_DATE:-}"
if [ -z "$EXP" ] && [ -f "$ENV_FILE" ]; then
  # 只取那一行赋值，别把整个 env 文件 source 进来：这文件里将来可能有别的键，
  # source 会把脚本的行为绑到"配置里恰好有什么"上（本仓 §一·3 那条 env>库>默认 的教训同族）。
  EXP="$(grep -E '^FPD_EXPIRE_DATE=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '"' | tr -d "'" || true)"
fi
EXP="$(printf '%s' "$EXP" | tr -d ' \t\r')"

if [ -z "$EXP" ]; then
  err "到期日取不到（ENV_FILE=$ENV_FILE 里没有 FPD_EXPIRE_DATE）⇒ 无法判定，本次不动任何东西"
  err "★ 这一档必须响亮：主站 bootstrap 在日期为空时是**显式不启用** expiry timer 的，"
  err "  所以真走到这里说明配置与 timer 的假设已经不一致，人工核对 $ENV_FILE"
  exit 4
fi
# 日期合法性：非法一律按**已到期**处理（与 fpdexec._expired() / fpd-bridge 同一口径，三处不许各写一份）。
# ★★ 为什么这里必须走 `date -d` 日历归一，而不是只上正则：
#   2026-10-01 自证时我自己写的正则 `^[0-9]{4}-[0-9]{2}-[0-9]{2}$` 把 `2026-13-45`（13 月 45 日）
#   判成**合法**，于是走了字典序比较 ⇒ "2026-10-01" ＜ "2026-13-45" ⇒ 判未到期、什么都不做。
#   而 fpdexec 用 `date.fromisoformat()`，month=13 直接抛 ⇒ 判**已到期**。
#   同一串坏配置在两侧得出相反结论，正是本脚本头注释要消灭的那类"三处各写一份"的缺陷。
#   现在两侧都**归一到真实日历**再比：`date -d` 解析失败＝非法＝按已到期（宁可关闸）。
TODAY="$(date -u +%Y-%m-%d)"
EXP_NORM="$(date -u -d "$EXP" +%Y-%m-%d 2>/dev/null || true)"

EXPIRED=0
if [ -z "$EXP_NORM" ]; then
  EXPIRED=1
  err "到期日 $EXP 不是真实存在的日期（date -d 解析失败）⇒ 按已到期处理（配置坏了就该停，与 fpdexec/bridge 同口径）"
elif [ "$TODAY" \> "$EXP_NORM" ] || [ "$TODAY" = "$EXP_NORM" ]; then
  EXPIRED=1
fi

# ---------------- 2. 未到期 ⇒ 什么都不做，闹钟必须还在 ----------------
if [ "$EXPIRED" != "1" ]; then
  log "未到期（今天 $TODAY ＜ 到期日 $EXP）⇒ 一次 systemctl 都不碰，清理与退役都不做"
  # ★ 这条腿过去是**永远走不到**的：旧 unit 无条件跑 sweep(ttl=1) ＋ 无条件 rm ＋ 无条件 disable，
  #   任何人手工 `systemctl start fpd-expiry.service` 排障，都会当场把当天所有在途会话目录清空，
  #   并把"跑过一次"当成"到期已处理"。现在手工拉起是安全操作（只读判定），这是本次改造的主要收益之一。
  exit 0
fi

# ---------------- 3. 已到期 ⇒ 先清 ----------------
log "已到期（今天 $TODAY ≥ 到期日 $EXP，或日期非法按到期）⇒ 开始自清"
if [ -z "$SWEEP_CMD" ]; then
  PYBIN=""
  if [ -f "$ENV_FILE" ]; then
    PYBIN="$(grep -E '^FPD_PYBIN=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d ' \t\r"' || true)"
  fi
  [ -n "$PYBIN" ] || PYBIN="$FPD_ROOT/.venv/bin/python3"
  # ★ 为什么这里还要再跑一次 sweep（而不是只靠下面那条 rm）：
  #   fpdexec 的 sweep 才是「打 EXPIRED 标记」的**唯一正写点**（_mark_expired），
  #   rm 只删文件不落标记；只跑 rm 的话 bridge 那道「标记文件存在即拒」就没有第二条证据，
  #   到期那天拒服务就只剩「日期已过」这一道——两侧都认日期、不依赖单点，是红线③的原设计。
  SWEEP_CMD="runuser -u $FPD_USER -- $PYBIN $FPD_ROOT/bin/fpdexec.py --mode sweep"
fi
# ★ 这里刻意用 `bash -c` 执行 SWEEP_CMD：覆盖口是给自证用的**命令串**，
#   直接对变量做无引号展开会按空格劈开参数，带引号的假 sweep（sh -c '…'）会碎成非法 argv——
#   那种碎法在测试里表现为"sweep 失败退 5"，看起来像脚本判据在起作用，其实是 harness 自己写错了。
SWEEP_OUT="$(FPD_TTL_SEC=1 bash -c "$SWEEP_CMD" 2>&1)" || {
  rc=$?
  err "sweep 失败（exit=$rc）：$SWEEP_OUT"
  err "★ 不许把「远端没清成」写成「已处理」——闹钟保持 enabled，明天再试"
  exit 5
}
log "sweep 回执：$(printf '%s' "$SWEEP_OUT" | tr '\n' ' ' | head -c 300)"

# 兜一次硬清（会话目录里可能有 sweep 按 mtime 判"还没闲置"的在途件；到期那天一律视为垃圾）
if [ -d "$WORK_DIR" ]; then
  find "$WORK_DIR" -mindepth 1 -maxdepth 1 -exec rm -rf {} + 2>/dev/null || true
fi

# ---------------- 4. 坐实 ⇒ 才允许退役 ----------------
if [ ! -f "$MARKER" ]; then
  err "清理后没有 $MARKER ⇒ 判**未坐实**，不退役闹钟（失败语义＝还会再来）"
  err "★ 这条判据存在的原因：sweep 正常退 0 只证明「脚本被跑过」，不证明「这台机器已拒绝服务」"
  exit 5
fi
LEFTOVER="$(find "$WORK_DIR" -mindepth 1 2>/dev/null | head -3 || true)"
if [ -n "$LEFTOVER" ]; then
  err "工作目录仍残留内容 ⇒ 判未坐实，不退役闹钟：$LEFTOVER"
  exit 5
fi
log "坐实完成（$MARKER 在、$WORK_DIR 空）⇒ 退役两只 timer，避免到期后每天报红被误当成故障"
DISABLE_OUT="$("$SYSTEMCTL_BIN" disable --now fpd-sweep.timer fpd-expiry.timer 2>&1)"
disable_rc=$?
if [ "$disable_rc" != "0" ]; then
  err "systemctl disable 退 $disable_rc：$DISABLE_OUT"
  err "★ 清理已经生效（客户文件不在本机了），但闹钟还在 ⇒ 下次触发会重跑本脚本，属预期"
  exit 6
fi
log "已退役：$(printf '%s' "$DISABLE_OUT" | tr '\n' ' ' | head -c 200)"
log "expiry_result=retired expired=1 work_dir=$WORK_DIR marker=$MARKER"
exit 0
