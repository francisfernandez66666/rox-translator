#!/usr/bin/env bash
# ============ dispatch_revert.sh · 职责说明 ============
# 主站侧**一键关闸**：把远程派发退回"从没开过"的状态（改造方案 §14 回归动作，§11-P5）。
#
# 三种场景共用这一条命令，这也是它必须存在的原因：
#   ① 排障：远端出问题 → 立刻回本地，不等定位（派发是增益，不是可用性依赖）；
#   ② 验收完关掉：按 §14 的回归清单，能力回本地后再做别的动作；
#   ③ **到期**（timer 每天调一次 `--if-expired`）：体验机到期当天自动关，不靠人记。
#
# 判据：删 drop-in → daemon-reload → restart → 读 /api/health 的 dispatch **必须 = off**。
#   ★ 只删 drop-in 不重启是"看起来关了其实还开着"的常见假动作，所以重启与实测是必需步，不是可选步。
#
# 用法（服务器 root 执行）：
#   bash scripts/dispatch_revert.sh              # 立即关闸
#   bash scripts/dispatch_revert.sh --if-expired # 到期才关（未到期即退出 0，供 timer 每天调）
#   bash scripts/dispatch_revert.sh --purge      # 关闸 + 一并清掉 env/私钥路径记录与到期日
#
# 退出码：0=已关闭或本就关闭；非 0=关闭动作失败（**此时派发可能仍在生效，必须人工介入**）。
#   ⚠️ 非 0 的另一层含义：到期回归 timer **不会被停用**（停用只在"实测 dispatch=off"之后那一步），
#      所以失败第二天还会自动再试——这是刻意的，别把它改成"跑过一次就退役"。
# =============================================================================
set -u

MODE="${1:-}"
# 三个路径都留 env 覆盖口（默认值＝现网真值，一字未改）。
# 为什么留这个口：本脚本的"未到期不许自灭／关闸坐实才自灭"是**行为**判据，静态 grep 锁不住
# （unit 里那行无条件 ExecStartPost 就是"看着没问题但每天把自己关掉"的形态，10-01 现网实证）。
# 留了口，Go 侧就能用假 systemctl／假 curl 在临时目录里**真跑一遍**三条分支（见
# internal/fileproc/dispatch_expiry_timer_test.go）。生产 timer 的环境里没有这三个变量，走的仍是默认值。
ENV_FILE="${ENV_FILE:-/etc/translator/dispatch.env}"
DROPIN="${DROPIN:-/etc/systemd/system/translator.service.d/dispatch.conf}"
EXPIRY_FILE="${EXPIRY_FILE:-/etc/translator/dispatch_expiry}"
LOCAL_BASE="${LOCAL_BASE:-http://127.0.0.1:8787}"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  ✅ %s\n' "$*"; }
bad()  { printf '\033[1;31m❌\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m⚠️\033[0m %s\n' "$*"; }

# --if-expired：给 timer 每天调。**未到期直接退出 0**，否则 timer 会因为"每天关一次"刷出一堆无意义日志。
if [ "$MODE" = "--if-expired" ]; then
  if [ ! -f "$EXPIRY_FILE" ]; then
    # 没落到期日 ⇒ 视为未到期（保守：不擅自关，避免把正常运行的派发误关）
    exit 0
  fi
  EXPIRE="$(head -1 "$EXPIRY_FILE" | tr -d '[:space:]')"
  TODAY="$(date +%F)"
  if [ -z "$EXPIRE" ] || [ "$TODAY" \< "$EXPIRE" ]; then
    exit 0
  fi
  log "已到期（到期日 $EXPIRE，今天 $TODAY）⇒ 自动关闸"
fi

[ "$(id -u)" = "0" ] || { bad "必须以 root 执行"; exit 2; }

log "[1/3] 摘掉 drop-in（prod.conf 零改动 ⇒ 与派发从没开过等价）"
if [ -f "$DROPIN" ]; then
  rm -f "$DROPIN" || { bad "删 drop-in 失败：$DROPIN"; exit 3; }
  ok "已删 $DROPIN"
else
  ok "drop-in 本就不存在（已关闭或从未开启）"
fi

log "[2/3] daemon-reload 并重启（不重启＝看起来关了其实还开着）"
systemctl daemon-reload || { bad "daemon-reload 失败"; exit 3; }
systemctl restart translator || { bad "restart 失败（**派发可能仍在生效**，人工看 journalctl -u translator）"; exit 3; }
READY=0
for _ in $(seq 1 40); do
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$LOCAL_BASE/livez" 2>/dev/null || echo 000)"
  [ "$code" = "200" ] && { READY=1; break; }
  sleep 1
done
[ "$READY" = "1" ] || { bad "重启后 /livez 不通（服务没起来）"; exit 3; }
ok "服务已起（/livez 200）"

log "[3/3] 实测必须 = off（off 才是唯一可接受的终态）"
HEALTH="$(curl -s --max-time 8 "$LOCAL_BASE/api/health")"
DISP="$(printf '%s' "$HEALTH" | python3 -c "import sys,json;print(json.load(sys.stdin).get('dispatch',''))" 2>/dev/null)"
if [ "$DISP" = "off" ]; then
  ok "dispatch=off（已完全退回本地）"
else
  bad "dispatch=${DISP:-<字段缺失>} ≠ off ⇒ **派发可能仍在生效**，人工检查 drop-in 目录是否被别的 drop-in 覆盖"
  exit 4
fi

# ★ 10-01（〇-AF 待决策 ①，现网实证）：**摘自己这只闹钟的位置必须在上面那道"实测 = off"之后**。
# 旧形态是单元文件里一行无条件 `ExecStartPost=systemctl disable --now <本 timer>`，
# 它把"今天跑过一次"当成"到期已处理"——而 --if-expired 在未到期那天是**正常退 0**、什么都没关。
# 现网读数（10-01 04:10:05）：journal 实读 Removed "/etc/systemd/system/timers.target.wants/…"，
# 随后 is-enabled=disabled、is-active=inactive，而到期日还差 25 天 ⇒ 10-26 那天不会有自动回滚。
# 现在的语义：只有"闸确实关上了"这条事实成立，闹钟才退役；上面任何一条 exit 2/3/4 都留在
# **闹钟还在**的状态，明天 04:10 会再试一次（这一档的失败必须是"还会再来"，不是"今天试过一次就再也不试"）。
if systemctl disable --now translator-dispatch-expiry.timer >/dev/null 2>&1; then
  ok "到期回归 timer 已停用（闸已实测关闭，不再需要每天自探）"
else
  warn "  timer 停用失败 ⇒ 闹钟还在，明天会再跑一次本脚本（幂等，无害）；"
  warn "  想彻底停：systemctl disable --now translator-dispatch-expiry.timer"
fi

if [ "$MODE" = "--purge" ]; then
  # timer 已在上一步"关闸坐实"后停用，这里不再重复 disable（重复一次＝多一条无意义日志，
  # 而且会让人以为"只有 --purge 才停闹钟"，回到本段开头那条错误语义）。
  rm -f "$ENV_FILE" "$EXPIRY_FILE"
  ok "已清 env / 到期日（私钥文件本身保留，按 §8-3 手工 rm；timer 已随关闸停用）"
  warn "  密钥处置：到期后顺手 rm 掉私钥并删 known_hosts 那行（见 §8-3）。"
fi

echo ""
echo "✅ 远程派发已关闭，能力完全回本地（本地队列 + 资源帽照旧）。"
echo "   ⚠️ 关闸后主站内存压力回到改造前水平：大 PDF 仍受本地并发闸=1 的保护，"
echo "      但尖峰回来了。若要再次开启，先跑 scripts/dispatch_preflight.sh 再 dispatch_apply.sh。"
