#!/usr/bin/env bash
# ============ dispatch_apply.sh · 职责说明 ============
# 主站侧**一键开闸**：把「文档转换远程派发」从默认关闭切到开启（改造方案 §7、§11-P5）。
#
# 为什么单独做一个脚本，而不是让人去手改 systemd：
#   这条开关的**回归动作**必须同样是一行命令（dispatch_revert.sh），否则"临时开一下"
#   会变成"忘了关"。到期回归、排障回退、验收完关掉，三种场景都得一键完成，
#   所以开与关必须成对存在、且都不依赖人记得住配置文件的路径。
#
# 它做的事（★ 2026-09-30 开闸当天从"四件"长成"六件"，两条都是现网踩出来的）：
#   ① 开闸前置：root + 服务在跑 + **preflight 全过**（环境不一致就不许开，这是硬前置）；
#   ①b 凭据落位：**服务账号必须真读得到那把私钥**。首开实测——钥匙在 /etc/translator 里，
#       该目录 750 root:root，而 translator 是 95:986 的服务账号，连目录都进不去；
#       systemd 的 EnvironmentFile 由 PID 1(root) 读，所以开关值读到了、健康面只报 degraded，
#       现象是"配置全对但永远拨不通"。判据一律问"服务账号 test -r"，不问"文件在不在"。
#   ①c 主机指纹：给 ssh 一个 known_hosts 并预置远端主机键。服务账号**没有家目录**
#       （passwd 里是 /home/translator，实际不存在）且 unit 开着 ProtectHome=yes ⇒
#       accept-new 记不下任何键 ⇒ 每次拨都等于"谁给的主机键都认"。客户文件要在公路上走，
#       这一腿不会报错、也不会进健康面，只能在这里显式铺好（键值先打指纹交人核对）。
#   ② 写独立 env（0600，属主 translator）+ 独立 drop-in dispatch.conf（prod.conf 零改动）；
#   ③ daemon-reload + restart，等 /livez 通，读 /api/health 的 dispatch 必须 = online；
#   ④ 落一份到期日期，并启用主站侧到期回归 timer（到期自动关，不靠人记）。
#
# ★ 它**不碰** prod.conf、不碰计费/备份/邮件四条链、不改任何业务代码路径。
#   关掉派发只需要 dispatch_revert.sh（删 drop-in + 重启），与"派发从没开过"逐字节等价。
#
# 用法（服务器 **root** 执行）：
#   FPD_SSH=fpd@1.2.3.4 [FPD_KEY=/etc/translator-dispatch/dispatch_ed25519] \
#   [FPD_KNOWN_HOSTS=/etc/translator-dispatch/known_hosts] \
#   FPD_EXPIRE_DATE=2026-10-26 [FPD_MIN_MB=20] [FPD_MIN_PAGES=30] [SKIP_PREFLIGHT=1] \
#   [FPD_SAMPLE=/opt/translator/data/_uploads/one.pdf] \
#   bash scripts/dispatch_apply.sh
#   ★ FPD_SAMPLE 不是可选项的装饰：预检的 G5（真机全往返）**无样张即判红**，本脚本会把它透传给
#   preflight。开闸前随手放一份真件（≥定档的体积与页数才有意义）在那儿，别用 SKIP_PREFLIGHT 绕。
#   （★ 两个凭据路径的默认值与 dispatch_preflight.sh **逐字同值**，由
#     fileproc/dispatch_scripts_gate_test.go 的 TestPreflightAndApplyShareCredentialPaths 钉住：
#     两份脚本各写一个路径，就会出现"挪完钥匙、预检却说私钥不存在、于是有人跳过预检"这条链。）
#
# ★ 默认档位是**量出来的**（2026-09-30 定档，别再改回 8/15 那对占位数）：
#   同一份 1.69MB/14 页真单据走原版式链，产物胀到 6.01MB（3.4 倍，本地同脚本同 venv 完全复现），
#   公网回传实测 0.49MB/s ⇒ 派一单的代价按**产物**算而不是按输入算。
#   输入 8MB 的 PDF 在这条链上产物可到 ≈27MB、回传 ≈55s，而远端转换本身只快几秒——
#   盖不回来。粗算盈亏平衡点在输入 ≈15–20MB 以上，故体积腿取 20、页数腿取 30
#   （两腿必须同时成立才派，见 Go 侧 DispatchEligible）。
#   等值锁＝fileproc_remote_test.go 的 TestDispatchTierDefaultsMatchMeasuredBreakEven：
#   本脚本的默认值与 Go 的默认值必须一致，任何一侧漂回 8/15 都会让"没装 drop-in 的环境"开始派小件。
#
# 退出码：0=已开闸且实测 online；非 0=**保持关闭**（失败即不改配置，不会留下半开状态）。
# =============================================================================
set -u

FPD_SSH="${FPD_SSH:-}"
# ★ 私钥默认落点＝/etc/translator-dispatch（**不是** /etc/translator）。
#   原因见上面 ①b：/etc/translator 是 750 root:root，服务账号进不去，钥匙放那儿等于派发永远拨不通。
FPD_KEY="${FPD_KEY:-/etc/translator-dispatch/dispatch_ed25519}"
# 远端主机指纹文件（Go 侧按 FILEPROC_DISPATCH_KNOWN_HOSTS 取用，空则不加 -o UserKnownHostsFile）
FPD_KNOWN_HOSTS="${FPD_KNOWN_HOSTS:-/etc/translator-dispatch/known_hosts}"
FPD_PORT="${FPD_PORT:-22}"
FPD_EXPIRE_DATE="${FPD_EXPIRE_DATE:-}"
FPD_MIN_MB="${FPD_MIN_MB:-20}"      # ★ 2026-09-30 定档（产物 3.4× 膨胀＋0.49MB/s 回传实测），别改回 8
FPD_MIN_PAGES="${FPD_MIN_PAGES:-30}" # ★ 与体积腿同时成立才派；15 页那档在 14 页就胀 3.4 倍的实测面前偏低
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

# ---------- ①b 凭据落位：问"服务账号读得到吗"，不问"文件在不在" ----------
SVC_USER="$(systemctl show -p User --value translator.service 2>/dev/null || true)"
SVC_USER="${SVC_USER:-root}"
as_svc() {
  # runuser 优先（util-linux 标配），没有就退回 sudo -u；两者都要 -n 语义（不口令）
  if command -v runuser >/dev/null 2>&1; then runuser -u "$SVC_USER" -- "$@" 2>/dev/null
  else sudo -u "$SVC_USER" "$@" 2>/dev/null; fi
}
[ -f "$FPD_KEY" ] || die "私钥不存在：$FPD_KEY
  生成一把（**私钥永不离开本机**，只把 .pub 送去体验机）：
    install -d -m 700 -o translator -g translator $(dirname "$FPD_KEY")
    sudo -u translator ssh-keygen -t ed25519 -N '' -C langcross-dispatch-$(date +%Y) -f $FPD_KEY
    sudo FPD_PUBKEY=$FPD_KEY.pub FPD_EXPIRE_DATE=<YYYY-MM-DD> bash deploy/dispatch/bootstrap-dispatch.sh"
if [ "$SVC_USER" != "root" ] && ! as_svc test -r "$FPD_KEY"; then
  bad "私钥在位但**服务账号 $SVC_USER 读不到**：$FPD_KEY"
  bad "  目录口径：$(stat -c '%a %U:%G' "$(dirname "$FPD_KEY")" 2>/dev/null || echo 取不到)"
  bad '  这就是「配置全对、健康面永远 degraded」的形态——EnvironmentFile 由 root(PID 1) 读，'
  bad '  而 ssh 是服务进程自己起的：钥匙读不到只会退 Permission denied，不会有人告诉你为什么。'
  bad "  修法（本脚本能自动做的只在 KEY_DIR=/etc/translator 这一种历史落位上）："
  bad "    install -d -m 700 -o translator -g translator /etc/translator-dispatch"
  bad "    mv /etc/translator/dispatch_ed25519* /etc/translator-dispatch/ && chown translator:translator /etc/translator-dispatch/*"
  if [ "$(dirname "$FPD_KEY")" = "/etc/translator" ]; then
    NEW_DIR=/etc/translator-dispatch
    log "  检测到历史落位 /etc/translator（服务账号进不去）⇒ 自动把钥匙挪到 $NEW_DIR"
    install -d -m 700 -o translator -g translator "$NEW_DIR" || die "建 $NEW_DIR 失败"
    mv -f "$FPD_KEY" "$NEW_DIR/" 2>/dev/null || die "挪私钥失败"
    mv -f "$FPD_KEY.pub" "$NEW_DIR/" 2>/dev/null || true
    KEY_BASE="$(basename "$FPD_KEY")"
    chmod 600 "$NEW_DIR/$KEY_BASE"
    chown translator:translator "$NEW_DIR/$KEY_BASE" 2>/dev/null || true
    [ -f "$NEW_DIR/$KEY_BASE.pub" ] && chmod 644 "$NEW_DIR/$KEY_BASE.pub" && chown translator:translator "$NEW_DIR/$KEY_BASE.pub" 2>/dev/null
    FPD_KEY="$NEW_DIR/$KEY_BASE"
    as_svc test -r "$FPD_KEY" || die "挪完仍读不到 ⇒ 不是目录权限问题，停下人工查（别让闸半开）"
    ok "私钥已落位 $FPD_KEY（服务账号 $SVC_USER 实读通过）"
  else
    die "私钥对服务账号不可读，且落点不是已知的历史位置 ⇒ 不猜、不改，按上面两条手工修完再开闸"
  fi
else
  ok "私钥 $FPD_KEY 服务账号（$SVC_USER）实读通过"
fi

# ---------- ①c 主机指纹：给 ssh 一个能用的 known_hosts，并把远端主机键钉进去 ----------
FPD_HOST="${FPD_SSH#*@}"
[ -n "$FPD_HOST" ] && [ "$FPD_HOST" != "$FPD_SSH" ] || die "FPD_SSH 要写成 fpd@<主机>（现在解析不出主机段）"
KH_DIR="$(dirname "$FPD_KNOWN_HOSTS")"
install -d -m 700 -o translator -g translator "$KH_DIR" 2>/dev/null || true
if [ -s "$FPD_KNOWN_HOSTS" ] && grep -qF "$FPD_HOST " "$FPD_KNOWN_HOSTS"; then
  ok "主机指纹已在 $FPD_KNOWN_HOSTS（$FPD_HOST）"
else
  TMPKH="$(mktemp)"
  if ! ssh-keyscan -p "$FPD_PORT" -T 10 "$FPD_HOST" >"$TMPKH" 2>/dev/null; then
    rm -f "$TMPKH"; die "ssh-keyscan 取不到 $FPD_HOST 的主机键 ⇒ 网络/端口不对，先排障再开闸"
  fi
  grep -vE '^\s*$|^\s*#' "$TMPKH" >"${TMPKH}.clean" || true
  if [ ! -s "${TMPKH}.clean" ]; then
    rm -f "$TMPKH" "${TMPKH}.clean"; die "ssh-keyscan 只回注释 ⇒ 该端口上没有 ssh 服务"
  fi
  cat "${TMPKH}.clean" >>"$FPD_KNOWN_HOSTS"
  rm -f "$TMPKH" "${TMPKH}.clean"
  chmod 600 "$FPD_KNOWN_HOSTS" 2>/dev/null || true
  chown translator:translator "$FPD_KNOWN_HOSTS" 2>/dev/null || true
  ok "已预置主机指纹到 $FPD_KNOWN_HOSTS（$FPD_HOST）"
  warn "  ⚠️ 这是**首见即信**（TOFU）：开闸前请带外核对一次下面这几个指纹。"
  warn "     对不上就不要开闸——把闸开在一条被人换过主机键的链上，客户文件就送给别人了。"
  ssh-keygen -lf "$FPD_KNOWN_HOSTS" 2>/dev/null | sed 's/^/      /' || true
fi
# Go 侧只认这个开关；没它就不加 -o（本地快跑与单测维持原形态），所以这里必须显式配上。
if ! as_svc test -r "$FPD_KNOWN_HOSTS"; then
  die "known_hosts 对服务账号 $SVC_USER 不可读 ⇒ 钉不住主机键，先修属主/权限（chmod 600 + chown $SVC_USER）"
fi

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
    FPD_SSH="$FPD_SSH" FPD_KEY="$FPD_KEY" FPD_KNOWN_HOSTS="$FPD_KNOWN_HOSTS" FPD_SAMPLE="${FPD_SAMPLE:-}" \
      bash "$REPO_ROOT/scripts/dispatch_preflight.sh" \
      || die "preflight 未过 ⇒ **不开闸**（改环境后重跑本脚本；确实要强开请显式 SKIP_PREFLIGHT=1）
      ⚠️ 若红的是 G5：那是**真机全往返**没做成，不是格式问题。G5 无样张时刻意判红不判跳过
      （09-29 那次事故就是「G1~G4 全绿、传输腿却是死路」），给它一份过档的样张再来：
        FPD_SAMPLE=/opt/translator/data/_uploads/<一份 ≥20MiB 或 ≥30 页的 PDF>（本脚本按同一档位量吞吐）"
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
FILEPROC_DISPATCH_KNOWN_HOSTS=$FPD_KNOWN_HOSTS
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
