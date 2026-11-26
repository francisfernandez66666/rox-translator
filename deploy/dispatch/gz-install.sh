#!/usr/bin/env bash
# ============ gz-install.sh · 广州量化机上的 fpd 执行体装机（2026-11-26） ============
# 用法（在 gz 上，ubuntu 账号 sudo）：
#   sudo FPD_EXPIRE_DATE=2026-12-03 FPD_WORKDIR_MAX_MB=4096 bash gz-install.sh
#
# 设计原则（方案 §3.8 双项目隔离）：
#   1. **复用 bootstrap-dispatch.sh 的账号/目录/ForceCommand 逻辑**，不重复造轮子；
#   2. 追加 gz 专属三件：① w/ 总配额与 TMPDIR 隔离 ② 量化四服务基线快照
#      ③ 施工期熔断（越线则暂停并写《量化峰值基线》，不硬回滚）。
#   3. **不写任何 Key**：LLM Key 由主站随 payload 临时传入，广州侧零落盘。
set -euo pipefail

FPD_ROOT="${FPD_ROOT:-/opt/fpdispatch}"
FPD_USER="${FPD_USER:-fpd}"
FPD_WORKDIR_MAX_MB="${FPD_WORKDIR_MAX_MB:-4096}"
FPD_EXPIRE_DATE="${FPD_EXPIRE_DATE:-}"
SRC_DIR="${SRC_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
BASELINE_FILE="${BASELINE_FILE:-/var/backups/quant/fpd_quant_baseline_$(date +%Y%m%d_%H%M%S).txt}"
PEAK_FILE="${PEAK_FILE:-/var/backups/quant/fpd_quant_peak_$(date +%Y%m%d_%H%M%S).txt}"

log(){ printf '[gz-install] %s\n' "$1"; }
ok(){ printf '[gz-install] ✔ %s\n' "$1"; }
bad(){ printf '[gz-install] ✖ %s\n' "$1" >&2; }

# ---- 前置：只在这台是量化机时装（用 /etc/quant.env 是否存在判） ----
[ -f /etc/quant.env ] || { bad "这台机没有 /etc/quant.env ⇒ 不是预期的广州量化机，拒绝安装"; exit 9; }
command -v systemctl >/dev/null 2>&1 || { bad "缺 systemctl"; exit 9; }

# ---- 1) 量化四服务基线（安装前快照，供熔断对比） ----
log "[1/5] 采集量化四服务基线 → ${BASELINE_FILE}"
{
  echo "# fpd install baseline $(date -Is)"
  systemctl is-active quant quant-research pydata caddy 2>/dev/null || true
  systemctl show quant quant-research pydata -p Id -p MemoryCurrent -p CPUQuota 2>/dev/null || true
  free -m
  df -h /
  uptime
} > "$BASELINE_FILE" 2>&1
chmod 0600 "$BASELINE_FILE"

# ---- 2) 复用 bootstrap 装机（账号/目录/ForceCommand/venv/slice/timer） ----
log "[2/5] 调用 bootstrap-dispatch.sh 完成基础装机"
FPD_ROOT="$FPD_ROOT" FPD_USER="$FPD_USER" FPD_EXPIRE_DATE="$FPD_EXPIRE_DATE" \
  bash "${SRC_DIR}/bootstrap-dispatch.sh"

# ---- 3) gz 专属：w/ 配额 + TMPDIR 隔离（§3.8） ----
log "[3/5] 落 w/ 配额与 TMPDIR 隔离参数"
ENV_FILE="$FPD_ROOT/etc/fpd.env"
# 幂等覆盖：只动这两个键，不碰 bootstrap 写的其它档
if grep -q '^FPD_WORKDIR_MAX_MB=' "$ENV_FILE"; then
  sed -i.bak "s/^FPD_WORKDIR_MAX_MB=.*/FPD_WORKDIR_MAX_MB=${FPD_WORKDIR_MAX_MB}/" "$ENV_FILE"
else
  printf 'FPD_WORKDIR_MAX_MB=%s\n' "$FPD_WORKDIR_MAX_MB" >> "$ENV_FILE"
fi
# 显式声明 TMPDIR 只用会话目录（bridge/worker 启动时由脚本自身 set，这里留档口径）
if ! grep -q '^FPD_TMPDIR_POLICY=' "$ENV_FILE"; then
  printf 'FPD_TMPDIR_POLICY=session_only\n' >> "$ENV_FILE"
fi
chmod 0644 "$ENV_FILE"

# ★ D-1 资源帽（方案 §2.1）：广州机专用，覆盖 bootstrap 按总内存算出的大帽。
# 不改 bootstrap 的模板值——旧体验机仍按它自己的内存自适应，互不影响。
SLICE=/etc/systemd/system/fpd.slice
log "  覆盖 fpd.slice 为广州机专用帽：CPUQuota 30% / MemoryHigh 512M / MemoryMax 768M"
sed -i.bak \
  -e 's/^MemoryHigh=.*/MemoryHigh=512M/' \
  -e 's/^MemoryMax=.*/MemoryMax=768M/' \
  -e '/^CPUQuota=/d' \
  "$SLICE"
if ! grep -q '^CPUQuota=' "$SLICE"; then
  sed -i '/^CPUWeight=/i CPUQuota=30%' "$SLICE"
fi
# Nice 由 fpdexec._caps() 对转换子进程 nice(10) 落实；slice 里不放非法的 Nice= 指令。
systemctl daemon-reload

mkdir -p "$FPD_ROOT/w" "$FPD_ROOT/w/.session_tmp"
chown -R "$FPD_USER":"$(id -gn "$FPD_USER")" "$FPD_ROOT/w"

# ---- 4) 施工期熔断（口径 15：记录峰值，不硬回滚） ----
log "[4/5] 装机后复核量化服务；越线则记录峰值并暂停"
CURRENT_LOAD="$(awk '{print $1}' /proc/loadavg)"
QUANT_ACTIVE="$(systemctl is-active quant quant-research pydata caddy 2>/dev/null | tr '\n' ' ')"
if [ "$QUANT_ACTIVE" != "active active active active " ]; then
  bad "量化四服务非全 active（现读：$QUANT_ACTIVE）⇒ 暂停后续步骤，记录峰值后交 owner"
  printf 'quant_active=%s\n' "$QUANT_ACTIVE" > "$PEAK_FILE"
  exit 7
fi
# 方案 §3.0 熔断双判据：① load>2.0 持续 60s ② 任一量化服务 MemoryCurrent>MemoryHigh×1.1
trigger_peak_record(){
  bad "$1"
  {
    echo "# fpd install peak $(date -Is) — 后续资源降级依据"
    printf '%s\n' "$1"
    printf 'load1=%s\n' "$CURRENT_LOAD"
    systemctl show quant quant-research pydata -p Id -p MemoryCurrent -p MemoryHigh -p CPUQuota 2>/dev/null
    free -m
    df -h /
  } > "$PEAK_FILE" 2>&1
  exit 8
}
if awk -v l="$CURRENT_LOAD" 'BEGIN{exit !(l>2.0)}'; then
  # 持续窗：60 秒内每 10s 复采一次，任一次回落到阈值内即视为瞬时抖动
  sustained=1
  for _i in 1 2 3 4 5 6; do
    sleep 10
    _l="$(awk '{print $1}' /proc/loadavg)"
    if awk -v l="$_l" 'BEGIN{exit !(l<=2.0)}'; then sustained=0; break; fi
    CURRENT_LOAD="$_l"
  done
  [ "$sustained" -eq 1 ] && trigger_peak_record "load1 持续 60s > 2.0（末值 $CURRENT_LOAD）"
fi
# 内存判据：MemoryCurrent 超 MemoryHigh 1.1 倍
for _svc in quant quant-research pydata; do
  _cur="$(systemctl show "$_svc" -p MemoryCurrent --value 2>/dev/null || echo 0)"
  _high="$(systemctl show "$_svc" -p MemoryHigh --value 2>/dev/null || echo 0)"
  if [ "${_high}" != "infinity" ] && [ "${_high:-0}" -gt 0 ] && \
     awk -v c="${_cur:-0}" -v h="$_high" 'BEGIN{exit !(c > h*1.1)}'; then
    trigger_peak_record "${_svc} MemoryCurrent=${_cur} > MemoryHigh=${_high}×1.1"
  fi
done
ok "量化四服务全 active、load1=${CURRENT_LOAD}、内存未越线（在阈值内）"

# ---- 5) 零 Key 落盘自检（D-4 机械门禁的装机侧对应） ----
log "[5/5] 自检：广州侧零 Key 落盘"
if grep -qE '^(SILICONFLOW_API_KEY|DB_DSN|JWT_SECRET|ADMIN_TOKEN|METRICS_TOKEN|FPD_LLM_KEY)=' \
    "$FPD_ROOT/etc/fpd.env" 2>/dev/null; then
  bad "fpd.env 里出现了 Key 赋值 ⇒ 违反口径 3（广州零 Key 落盘），立即停"
  exit 10
fi
ok "广州侧零 Key 落盘确认"

log "完成。基线 ${BASELINE_FILE}；峰值记录路径 ${PEAK_FILE}（仅越线时生成）；expiry=${FPD_EXPIRE_DATE:-未设}。"
log "下一步：主站 dispatch_sync.sh 同步脚本与 worker → dispatch_preflight.sh 门禁 → dispatch_apply.sh 开闸。"
