#!/usr/bin/env bash
# ============================================================================
# deploy/retire_assist_unit.sh —— 四期 W5 步骤 5：停用 translator-assist unit
#
# 装配四档一条命令（§8.3 口径，同 install_capsvc.sh 形态）：
#   --status    只读：unit 状态 / 是否开机自启 / 旧二进制是否在位 / ASSIST_LINK 现值
#   干跑        默认（无参数）或 --dry-run：只打印将要执行的命令，不碰 systemd
#   --apply     停用：systemctl disable --now <unit>；**不删旧二进制**
#   --rollback  恢复：systemctl enable --now <unit>（回滚件就是那个旧二进制）
#
# ★ 保留旧二进制一个发版周期（§4.5.2 步骤 5 / §8.3 回滚）：
#   unit 文件名是 ai-assist.service，产物名才是 translator-assist。
#   --apply 只停 unit，绝不 `rm` / 覆盖 /opt/ai-assist/bin/translator-assist；
#   --rollback 直接 enable --now 把它拉回来，不需要重新发版。
#   旧二进制不在位时 --apply 直接拒绝（没有回滚源的停机不可接受）。
#   一个发版周期之后再清理由人工单独执行，不写进本脚本的自动档。
#
# ★ 回滚的另一档是「代码切换」而非本脚本：本地链路缺省开，
#   显式 ASSIST_LINK=remote 即回落到旧跨进程代理（见
#   internal/api/assist_crossproc_retire.go 的 assistLinkIsLocal）。
#   两档叠加：ASSIST_LINK=remote + 本脚本 --rollback ⇒ 完整回到 W5 之前。
# ============================================================================
set -uo pipefail

UNIT="${ASSIST_RETIRE_UNIT:-ai-assist.service}"
BIN="${ASSIST_RETIRE_BIN:-/opt/ai-assist/bin/translator-assist}"

say() { echo "$*"; }
die() { echo "ABORT $*" >&2; exit 2; }

MODE=""
case "${1:-}" in
  --status|--dry-run|--apply|--rollback) MODE="$1" ;;
  ""|-h|--help) MODE="--dry-run" ;;
  *) die "未知参数：${1}（--status／--dry-run／--apply／--rollback）" ;;
esac

# 只读探针：命令不存在时一律回落成可读状态词，不把一次查询判成崩溃。
unit_active() { systemctl is-active "$UNIT" 2>/dev/null || echo "unknown"; }
unit_enabled() { systemctl is-enabled "$UNIT" 2>/dev/null || echo "unknown"; }
bin_present() { [ -f "$BIN" ] && echo "present" || echo "missing"; }

if [ "$MODE" = "--status" ]; then
  say "UNIT=$UNIT"
  say "BIN=$BIN"
  if command -v systemctl >/dev/null 2>&1; then
    say "active=$(unit_active)"
    say "enabled=$(unit_enabled)"
  else
    say "active=unknown (no systemctl)"
    say "enabled=unknown (no systemctl)"
  fi
  say "binary=$(bin_present)"
  say "ASSIST_LINK=${ASSIST_LINK:-<unset>} (unset/任意非 remote 值＝本地链路已收敛)"
  say "ASSIST_RETIRE_STATUS_OK=1"
  exit 0
fi

if [ "$MODE" = "--dry-run" ]; then
  say "[dry-run] 将停用 unit：$UNIT"
  say "[dry-run]   systemctl disable --now $UNIT"
  say "[dry-run] 保留回滚件（不删、不覆盖）：${BIN}（当前 $(bin_present)）"
  say "[dry-run] 回滚：bash $0 --rollback；代码档回落：ASSIST_LINK=remote"
  say "[dry-run] 未对 systemd 做任何改动"
  say "ASSIST_RETIRE_DRYRUN_OK=1"
  exit 0
fi

command -v systemctl >/dev/null 2>&1 || die "缺 systemctl，--apply/--rollback 不可用"
if [ "$(id -u)" != "0" ]; then
  die "需要 root 执行 systemctl（当前 uid=$(id -u)）；请用 sudo 重跑"
fi
if [ "$(bin_present)" != "present" ]; then
  die "旧二进制 $BIN 不在位：没有回滚源，拒绝 --apply/--rollback"
fi

if [ "$MODE" = "--apply" ]; then
  say "停用 ${UNIT}（旧二进制原样保留一个发版周期）"
  systemctl disable --now "$UNIT" || die "systemctl disable --now 失败"
  st=$(unit_active)
  if [ "$st" = "active" ]; then
    die "disable --now 之后 $UNIT 仍 active：$st"
  fi
  say "active=$st"
  say "binary=$(bin_present)（保留，未删除）"
  say "ASSIST_RETIRE_APPLY_OK=1"
  exit 0
fi

if [ "$MODE" = "--rollback" ]; then
  say "恢复 ${UNIT}（复用保留的旧二进制 ${BIN}）"
  systemctl enable --now "$UNIT" || die "systemctl enable --now 失败"
  st=$(unit_active)
  if [ "$st" != "active" ]; then
    die "enable --now 之后 $UNIT 不是 active：$st"
  fi
  say "active=$st"
  say "提示：若需同时回落代码链路，配 ASSIST_LINK=remote 后重启主服务"
  say "ASSIST_RETIRE_ROLLBACK_OK=1"
  exit 0
fi
