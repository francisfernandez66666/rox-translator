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
# =============================================================================
set -u

MODE="${1:-}"
ENV_FILE=/etc/translator/dispatch.env
DROPIN=/etc/systemd/system/translator.service.d/dispatch.conf
EXPIRY_FILE=/etc/translator/dispatch_expiry
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

if [ "$MODE" = "--purge" ]; then
  rm -f "$ENV_FILE" "$EXPIRY_FILE"
  systemctl disable --now translator-dispatch-expiry.timer >/dev/null 2>&1 || true
  ok "已清 env / 到期日 / 到期 timer（私钥文件本身保留，按 §8-3 手工 rm）"
  warn "  密钥处置：到期后顺手 rm 掉私钥并删 known_hosts 那行（见 §8-3）。"
fi

echo ""
echo "✅ 远程派发已关闭，能力完全回本地（本地队列 + 资源帽照旧）。"
echo "   ⚠️ 关闸后主站内存压力回到改造前水平：大 PDF 仍受本地并发闸=1 的保护，"
echo "      但尖峰回来了。若要再次开启，先跑 scripts/dispatch_preflight.sh 再 dispatch_apply.sh。"
