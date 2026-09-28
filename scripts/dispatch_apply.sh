#!/usr/bin/env bash
# ============ dispatch_apply.sh · 职责说明 ============
# 主站侧**一键开闸**：把「文档转换远程派发」从默认关闭切到开启（改造方案 §7、§11-P5）。
#
# 为什么单独做一个脚本，而不是让人去手改 systemd：
#   这条开关的**回归动作**必须同样是一行命令（dispatch_revert.sh），否则"临时开一下"
#   会变成"忘了关"。到期回归、排障回退、验收完关掉，三种场景都得一键完成，
#   所以开与关必须成对存在、且都不依赖人记得住配置文件的路径。
#
# 它做的四件事（不做第五件）：
#   ① 开闸前置：root + 服务在跑 + **preflight 全过**（环境不一致就不许开，这是硬前置）；
#   ② 写独立 env（0600，属主 translator）+ 独立 drop-in dispatch.conf（prod.conf 零改动）；
#   ③ daemon-reload + restart，等 /livez 通，读 /api/health 的 dispatch 必须 = online；
#   ④ 落一份到期日期，并启用主站侧到期回归 timer（到期自动关，不靠人记）。
#
# ★ 它**不碰** prod.conf、不碰计费/备份/邮件四条链、不改任何业务代码路径。
#   关掉派发只需要 dispatch_revert.sh（删 drop-in + 重启），与"派发从没开过"逐字节等价。
#
# 用法（服务器 **root** 执行）：
#   FPD_SSH=fpd@1.2.3.4 FPD_KEY=/etc/translator/dispatch_ed25519 \
#   FPD_EXPIRE_DATE=2026-10-28 [FPD_MIN_MB=8] [FPD_MIN_PAGES=15] [SKIP_PREFLIGHT=1] \
#   bash scripts/dispatch_apply.sh
#
# 退出码：0=已开闸且实测 online；非 0=**保持关闭**（失败即不改配置，不会留下半开状态）。
# =============================================================================
set -u

FPD_SSH="${FPD_SSH:-}"
FPD_KEY="${FPD_KEY:-/etc/translator/dispatch_ed25519}"
FPD_EXPIRE_DATE="${FPD_EXPIRE_DATE:-}"
FPD_MIN_MB="${FPD_MIN_MB:-8}"
FPD_MIN_PAGES="${FPD_MIN_PAGES:-15}"
SKIP_PREFLIGHT="${SKIP_PREFLIGHT:-0}"

ENV_FILE=/etc/translator/dispatch.env
DROPIN_DIR=/etc/systemd/system/translator.service.d
DROPIN="$DROPIN_DIR/dispatch.conf"
EXPIRY_FILE=/etc/translator/dispatch_expiry
LOCAL_BASE="${LOCAL_BASE:-http://127.0.0.1:8787}"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  ✅ %s\n' "$*"; }
bad()  { printf '\033[1;31m❌\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m⚠️\033[0m %s\n' "$*"; }
die()  { bad "$*"; exit 1; }

# ---------------------------------------------------------------- ① 前置
[ "$(id -u)" = "0" ] || die "必须以 root 执行（要写 /etc/systemd 与 /etc/translator）"
[ -n "$FPD_SSH" ] || die "必须给 FPD_SSH=fpd@<ip>（派发专用账号，不是管理员账号）"
[ -f "$FPD_KEY" ] || die "私钥不存在：$FPD_KEY（先在主站生成并配好体验机的 authorized_keys）"
command -v systemctl >/dev/null 2>&1 || die "本机没有 systemd"
systemctl list-unit-files 'translator.service' >/dev/null 2>&1 || die "translator.service 不存在（本脚本只在主站跑）"

log "[1/5] 前置检查"
if [ -z "$FPD_EXPIRE_DATE" ]; then
  warn "  未给 FPD_EXPIRE_DATE ⇒ 主站侧到期自动回归**不会启用**。"
  warn "  口径：这不是可选项——体验机到期那天若没人手工关闸，派发会一直拨一台已回收的机器。"
else
  ok "到期日 $FPD_EXPIRE_DATE（回归 timer 会在此之前自动关闸）"
fi
if [ "$SKIP_PREFLIGHT" = "1" ]; then
  warn "  SKIP_PREFLIGHT=1 ⇒ 跳过一致性门禁。**只在已跑过 preflight 且环境未变时用**，"
  warn "  否则开着的是一台「环境不一致」的远端：不报错，只是客户拿到的件和主站不一样。"
else
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  if [ -f "$REPO_ROOT/scripts/dispatch_preflight.sh" ]; then
    FPD_SSH="$FPD_SSH" FPD_KEY="$FPD_KEY" bash "$REPO_ROOT/scripts/dispatch_preflight.sh" \
      || die "preflight 未过 ⇒ **不开闸**（改环境后重跑本脚本；确实要强开请显式 SKIP_PREFLIGHT=1）"
    ok "preflight 全过"
  else
    die "找不到 scripts/dispatch_preflight.sh（一致性门禁缺失，拒绝开闸）"
  fi
fi

# ---------------------------------------------------------------- ② 写 env + drop-in
log "[2/5] 写独立配置（prod.conf 零改动）"
mkdir -p /etc/translator && chmod 750 /etc/translator
umask 077
cat >"$ENV_FILE" <<EOF
# /etc/translator/dispatch.env — 远程派发开关（0600，属主 translator）
# 由 scripts/dispatch_apply.sh 生成；回归只需跑 scripts/dispatch_revert.sh。
# ★ 这个文件里**只有主机与密钥路径**，没有任何主站密钥（远端零密钥面是方案底线，见 §8）。
FILEPROC_DISPATCH=1
FILEPROC_DISPATCH_HOST=$FPD_SSH
FILEPROC_DISPATCH_SSH_KEY=$FPD_KEY
FILEPROC_DISPATCH_MIN_MB=$FPD_MIN_MB
FILEPROC_DISPATCH_MIN_PAGES=$FPD_MIN_PAGES
EOF
chmod 600 "$ENV_FILE"
chown translator:translator "$ENV_FILE" 2>/dev/null || warn "  chown translator 失败（确认该账号存在）"
ok "已写 $ENV_FILE（0600）"

mkdir -p "$DROPIN_DIR"
# ★ 只声明 EnvironmentFile，不写任何值：unit 是 world-readable 的，主机/密钥路径不许落在这里。
cat >"$DROPIN" <<'EOF'
# ============================================================================
# dispatch.conf — 远程派发 drop-in（由 scripts/dispatch_apply.sh 生成）
#
# ★ 隔离原则：本文件只引用 env 文件，**不含任何值**（unit world-readable）。
#   prod.conf 零改动 ⇒ 回归 = 删掉本文件 + daemon-reload + restart，
#   也就是 scripts/dispatch_revert.sh 干的事，与"派发从没开过"逐字节等价。
# ============================================================================
[Service]
EnvironmentFile=/etc/translator/dispatch.env
EOF
chmod 644 "$DROPIN"
ok "已写 $DROPIN"

# ---------------------------------------------------------------- ③ 生效
log "[3/5] daemon-reload 并重启 translator"
systemctl daemon-reload || die "daemon-reload 失败"
systemctl restart translator || die "restart 失败（journalctl -u translator -n 50 看原因）"

# ★ 启动慢是常态（要加载知识库向量索引），判"起没起来"必须等 /livez，不能用固定 sleep 猜。
READY=0
for _ in $(seq 1 40); do
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$LOCAL_BASE/livez" 2>/dev/null || echo 000)"
  [ "$code" = "200" ] && { READY=1; break; }
  sleep 1
done
[ "$READY" = "1" ] || die "重启后 /livez 一直不通（服务没起来，派发状态无从谈起）"
ok "服务已起（/livez 200）"

log "[4/5] 实测派发状态（必须 online，off/degraded 都算开闸失败）"
HEALTH="$(curl -s --max-time 8 "$LOCAL_BASE/api/health")"
DISP="$(printf '%s' "$HEALTH" | python3 -c "import sys,json;print(json.load(sys.stdin).get('dispatch',''))" 2>/dev/null)"
case "$DISP" in
  online)
    ok "dispatch=$DISP（派发生效）"
    ;;
  off)
    bad "dispatch=$DISP ⇒ 开关没生效（env 没读到？检查 drop-in 是否被别的 drop-in 覆盖）"
    bad "  回滚：bash scripts/dispatch_revert.sh"
    exit 1
    ;;
  degraded)
    bad "dispatch=$DISP ⇒ 远端未就绪，主站内存一分没省（按 §5.1/§5.2 排障，不要继续观察）"
    bad "  回滚：bash scripts/dispatch_revert.sh"
    exit 1
    ;;
  *)
    bad "dispatch=${DISP:-<字段缺失>} ⇒ 二进制没换或没接线（/api/health 应带 dispatch 字段）"
    exit 1
    ;;
esac

# ---------------------------------------------------------------- ④ 到期回归
log "[5/5] 落到期日 + 启用主站侧到期回归 timer"
if [ -n "$FPD_EXPIRE_DATE" ]; then
  printf '%s\n' "$FPD_EXPIRE_DATE" >"$EXPIRY_FILE"
  chmod 644 "$EXPIRY_FILE"
  ok "已写 $EXPIRY_FILE = $FPD_EXPIRE_DATE"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  if [ -f "$REPO_ROOT/deploy/systemd/translator-dispatch-expiry.timer" ]; then
    # ★ 回归脚本必须先落到服务器本机：timer 跑的是 /opt/translator/bin/dispatch_revert.sh，
    #   而 scripts/ 目录**不随 releases 部署**——只装 timer 不装脚本，到期那天会得到一个
    #   "单元存在、ExecStart 找不到文件"的红灯，看上去像故障，实际是漏装。
    mkdir -p /opt/translator/bin
    cp "$REPO_ROOT/scripts/dispatch_revert.sh" /opt/translator/bin/dispatch_revert.sh 2>/dev/null \
      && chmod 0755 /opt/translator/bin/dispatch_revert.sh \
      && ok "回归脚本已落 /opt/translator/bin/dispatch_revert.sh" \
      || warn "  回归脚本复制失败 ⇒ 到期 timer 会找不到 ExecStart"
    cp "$REPO_ROOT/deploy/systemd/translator-dispatch-expiry.service" /etc/systemd/system/ 2>/dev/null || true
    cp "$REPO_ROOT/deploy/systemd/translator-dispatch-expiry.timer" /etc/systemd/system/ 2>/dev/null || true
    systemctl daemon-reload
    systemctl enable --now translator-dispatch-expiry.timer >/dev/null 2>&1 \
      && ok "到期回归 timer 已启用（每天检查一次，到期自动关闸）" \
      || warn "  timer 启用失败 ⇒ 到期那天需人工跑 dispatch_revert.sh"
  else
    warn "  未找到 timer 单元文件 ⇒ 到期需人工关闸"
  fi
fi

echo ""
echo "✅ 派发已开启：dispatch=online，到期回归已就位。"
echo "   观察口径（§5.4）：24h 内 journalctl -u translator | grep '派发失败 ⇒ 回落本地排队'"
echo "   回落率 >30% 或连续 5 单全回落 ⇒ 视为派发未生效，排障而不是继续观察。"
echo "   一键关闭：bash scripts/dispatch_revert.sh"
