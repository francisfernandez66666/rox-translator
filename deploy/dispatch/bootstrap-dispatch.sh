#!/usr/bin/env bash
# ============ bootstrap-dispatch.sh · 职责说明 ============
# 体验机（文档转换派发远端）一次性初始化脚本，**在那台机器上以 root 执行**。
# 干的事：建账号 fpd → 装 apt/pip 依赖（钉版）→ 铺目录骨架 → 装 systemd 资源帽与两个 timer
#        → 装 sshd 的 ForceCommand 收口 → 自检并打印就绪结论。
#
# 设计红线（改脚本前先读改造方案 §0 / §6 / §8）：
#   ① 这台机器**零密钥**：不配 DB_DSN / JWT_SECRET / API Key / ADMIN_TOKEN，也不装后端二进制。
#      脚本只建目录与依赖，任何"看起来更省事"地把主站 .env 拷过来的做法一律禁止。
#   ② 只暴露被收口的执行入口：账号 fpd 无交互 shell（ForceCommand 固定到 fpd-bridge），
#      不新增任何监听端口。
#   ③ **到期自清**：fpd-expiry.timer 在 FPD_EXPIRE_DATE 那天主动清空工作目录并打 EXPIRED 标记，
#      这样即使主站的回归 timer 没跑成功，远端也不会留着一堆客户文件。
#   ④ 幂等：可重复执行，不删除既有非本脚本产物；对 apt/pip 的重复安装只做确认不做破坏。
#
# 用法（体验机上）：
#   sudo bash bootstrap-dispatch.sh                      # 默认到期日=今天+28 天
#   sudo FPD_EXPIRE_DATE=2026-10-26 bash bootstrap-dispatch.sh
#   sudo FPD_ROOT=/opt/fpdispatch bash bootstrap-dispatch.sh
# 依赖文件（与脚本同目录，先整体 scp/rsync 上来）：
#   fpdexec.py  fpd-bridge  fpd.slice  fpd-sweep.{service,timer}
#   fpd-expiry.{service,timer}  sshd-fpd.conf  requirements.lock.txt
# ==========================================================
set -uo pipefail   # 故意不带 -e：本脚本要在"某一步失败"时打印可执行的修复建议，而不是静默中止

FPD_ROOT="${FPD_ROOT:-/opt/fpdispatch}"
FPD_USER="${FPD_USER:-fpd}"
SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
# 到期日：体验机是"约一个月"的临时资源，默认往后 28 天（早于厂商回收，留 2～3 天余量）。
# ★ 用 date -d 计算；算不出来（非 GNU date）时留空，由下面的告警提示人工填。
#   这台（腾讯云上海 2G/双核）厂商到期 2026-10-28 ⇒ 默认自毁日填 2026-10-26（提前 2 天）。
FPD_EXPIRE_DATE="${FPD_EXPIRE_DATE:-$(date -d "+28 days" +%F 2>/dev/null || true)}"
# 远端资源帽：留 512M 给系统与 sshd，其余给转换进程（2G 机 ⇒ MemoryHigh≈1200M）
FPD_MEM_TOTAL_MB="$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo 2>/dev/null || true)"
FPD_MEM_TOTAL_MB="${FPD_MEM_TOTAL_MB:-0}"
FPD_MEM_MAX_MB=$(( FPD_MEM_TOTAL_MB > 768 ? FPD_MEM_TOTAL_MB - 512 : 384 ))
FPD_MEM_HIGH_MB=$(( FPD_MEM_MAX_MB * 4 / 5 ))

log() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m ⚠️ \033[0m %s\n' "$*"; }
bad() { printf '\033[1;31m ❌ \033[0m %s\n' "$*"; }

# ----------------------------- 0. 前置检查 -----------------------------
log "[0/8] 运行环境检查"
if [ "$(id -u)" != "0" ]; then bad "请用 root 或 sudo 运行（要建账号、装包、写 /etc/systemd）"; exit 1; fi
if ! command -v apt-get >/dev/null 2>&1; then
  bad "未识别到 apt（本脚本只支持 Debian/Ubuntu）。CentOS/Alma 请改用 dnf 安装同名包后手工执行第 2~6 步"
  exit 2
fi
if [ -x /opt/translator/bin/translator-server ]; then
  bad "/opt/translator/bin 下**已存在主站二进制** ⇒ 这台机器可能是主站或被当成第二副本用。"
  bad "派发方案的底线是远端零密钥、只当执行体（改造方案 §8-2）。请确认目标机器身份后再继续。"
  exit 3
fi
for f in fpdexec.py fpd-bridge fpd.slice fpd-sweep.service fpd-sweep.timer \
         fpd-expiry.service fpd-expiry.timer sshd-fpd.conf requirements.lock.txt; do
  [ -f "$SRC_DIR/$f" ] || { bad "缺少随包文件 $f（应把 deploy/dispatch/ 整个目录一起传上来）"; exit 4; }
done

# ----------------------------- 1. 账号 -----------------------------
log "[1/8] 建立专用账号 $FPD_USER（系统账号、只跑 ForceCommand）"
if id -u "$FPD_USER" >/dev/null 2>&1; then
  log "  账号已存在，跳过创建（uid=$(id -u "$FPD_USER")）"
else
  # ★ shell 必须是 /bin/sh 而**不是 nologin**——这是首装真踩出来的：
  #   sshd 执行 ForceCommand 的方式是 `$SHELL -c "<ForceCommand>"`，shell 若是 /usr/sbin/nologin，
  #   派发一上来就得到 "This account is currently not available"、退出码非零，
  #   而现象是"每台机器都装好了、主站却永远判不就绪"，往 sshd/密钥/网络上都排查不到。
  #   "不给交互 shell"这件事由 sshd 侧的两条保证，跟账号 shell 无关：
  #     PermitTTY no（本条与 Match 段一起写）⇒ 分不到伪终端；
  #     ForceCommand ⇒ 任何被传入的命令都被丢弃、固定跑 fpd-bridge。
  useradd -r -m -d "$FPD_ROOT/home" -s /bin/sh "$FPD_USER" || { bad "useradd 失败"; exit 5; }
fi
# 已存在但历史上被建成 nologin 的机器（比如我第一次装的），一律在这里改回来
CUR_SHELL="$(getent passwd "$FPD_USER" | cut -d: -f7 || true)"
if [ "$CUR_SHELL" != "/bin/sh" ] && [ "$CUR_SHELL" != "/bin/bash" ]; then
  warn "账号 shell 是 $CUR_SHELL ⇒ ForceCommand 无法执行，正在改为 /bin/sh"
  usermod -s /bin/sh "$FPD_USER" || { bad "usermod 失败"; exit 5; }
fi

# 主站派发账号的公钥（没有它，底座装得再全也连不上）。
# 用 FPD_PUBKEY=/path/to/id_ed25519.pub 传进来；不传则只建目录并告警，绝不静默"看起来装好了"。
AK="$FPD_ROOT/home/.ssh/authorized_keys"
if [ -n "${FPD_PUBKEY:-}" ] && [ -f "$FPD_PUBKEY" ]; then
  # ⚠️ install 是**整文件覆盖**：这台机器上若已存在别人那把派发公钥（比如主站正式 key），
  #    传新 key 就会把它挤掉——现象是"重跑一次 bootstrap，主站忽然连不上"。
  #    故覆盖前先把现有条数与指纹点名出来，多于一行时要求人工确认（FPD_AK_FORCE=1 才覆盖）。
  EXIST_LINES=0
  [ -f "$AK" ] && EXIST_LINES="$(grep -cE '^(ssh|ecdsa|sk-)' "$AK" 2>/dev/null || true)"
  EXIST_FP="$(sha256sum "$AK" 2>/dev/null | awk '{print substr($1,1,12)}' || true)"
  NEW_FP="$(sha256sum "$FPD_PUBKEY" 2>/dev/null | awk '{print substr($1,1,12)}' || true)"
  if [ "${EXIST_LINES:-0}" -gt 1 ] && [ "${FPD_AK_FORCE:-0}" != "1" ]; then
    bad "  $AK 现有 $EXIST_LINES 行公钥（指纹 ${EXIST_FP:-?}）≠ 单一 key ⇒ 拒绝整文件覆盖（会挤掉别的派发方）"
    bad "  要合并请把新 key **追加**进该文件；确认就是要整体替换再带 FPD_AK_FORCE=1 重跑"
    exit 10
  fi
  if [ "${EXIST_FP:-}" = "${NEW_FP:-}" ]; then
    log "  派发公钥已在且与传入的同一把（$EXIST_LINES 行，指纹 ${EXIST_FP:-?}）⇒ 不动它"
  else
    install -m 600 -o "$FPD_USER" -g "$(id -gn "$FPD_USER")" "$FPD_PUBKEY" "$AK"
    log "  已写入派发公钥（$(grep -cE '^(ssh|ecdsa|sk-)' "$AK" 2>/dev/null || true) 行 / $(wc -c < "$AK") 字节）"
  fi
elif [ -s "$AK" ]; then
  # 没传 FPD_PUBKEY 但文件里已经有 key（本机首装就是这么装的）——**必须报"已在"**，
  # 只按"没传参数"就 warn "为空"会把一台已经接通过的机器报成连不上（读文件比读参数可信）。
  HAVE_LINES="$(grep -cE '^(ssh|ecdsa|sk-)' "$AK" 2>/dev/null || true)"
  HAVE_FP="$(sha256sum "$AK" 2>/dev/null | awk '{print substr($1,1,12)}' || true)"
  log "  未提供 FPD_PUBKEY，但 $AK 已有 ${HAVE_LINES:-0} 行公钥（指纹 ${HAVE_FP:-?}）⇒ 本次不动它"
else
  warn "未提供 FPD_PUBKEY 且 $AK 不存在/为空 ⇒ 主站现在连不上来。补法：把主站派发公钥原样写进该文件并 chmod 600"
fi
chmod 700 "$FPD_ROOT/home/.ssh"
chown -R "$FPD_USER":"$(id -gn "$FPD_USER")" "$FPD_ROOT/home"

# ----------------------------- 2. apt 依赖 -----------------------------
log "[2/8] 安装系统依赖（poppler / 中文字体 / rsync）"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y >/dev/null 2>&1 || warn "apt-get update 失败——离线环境请预先备好 .deb"
# fonts-noto-cjk：主站 14 个中文族全来自这一个包（实测见改造方案 §6.1）
# ★ 注意**普惠体不在 apt 里**——它是主站 /opt/translator/fonts/ 下的单文件，必须由 sync 脚本 rsync 过来
APT_PKGS="poppler-utils fonts-noto-cjk rsync openssh-client python3-venv python3-pip fontconfig"
# LibreOffice 只为**休眠链**（docx_translate.py，全仓当前零调用方）兼容；默认不装，省 400M+ 磁盘与更新麻烦
if [ "${FPD_WITH_LIBREOFFICE:-0}" = "1" ]; then
  APT_PKGS="$APT_PKGS libreoffice-writer libreoffice-impress libreoffice-calc"
  warn "已按需安装 LibreOffice（休眠链用得上；派发主链用不到）"
fi
# shellcheck disable=SC2086
apt-get install -y $APT_PKGS || warn "部分 apt 包安装失败，稍后 preflight 会点名缺哪个"

# ----------------------------- 3. 目录骨架 -----------------------------
log "[3/8] 铺目录 $FPD_ROOT/{bin,w,home,etc}"
mkdir -p "$FPD_ROOT/bin/assets/fonts" "$FPD_ROOT/w" "$FPD_ROOT/home/.ssh" "$FPD_ROOT/etc" \
         /usr/local/share/fonts/fpd-langcross
# 转换脚本与字体资产的**内容**由主站 sync 脚本 rsync（保证与主站逐字同版本），本脚本只建骨架
# ⚠️ 属主一律 root（2026-09-29 改）：fpdexec.py 过去装成 fpd:fpd 0755，等于**派发账号能改自己执行的代码**。
#   ForceCommand 只保证"只能跑这一个入口"，不保证"这个入口的内容不可写"——一旦 fpd 的密钥泄露，
#   对方就能把 fpdexec 换成任意 Python（仍在 w/ 边界内，但拒绝规则与白名单全部归零）。
#   因此这里与 fpd-bridge 同口径：root:root 0755，fpd 只读可执行。
for f in fpdexec.py; do install -m 0755 -o root -g root "$SRC_DIR/$f" "$FPD_ROOT/bin/$f"; done
install -m 0755 -o root -g root "$SRC_DIR/fpd-bridge" "$FPD_ROOT/bin/fpd-bridge"

# ----------------------------- 4. Python venv（钉版） -----------------------------
log "[4/8] 建 venv 并按锁文件安装 Python 依赖"
VENV="$FPD_ROOT/.venv"
[ -x "$VENV/bin/python3" ] || python3 -m venv "$VENV" || { bad "python3 -m venv 失败"; exit 6; }
LOCK="$FPD_ROOT/etc/requirements.lock.txt"
install -m 0644 "$SRC_DIR/requirements.lock.txt" "$LOCK"
# "$VENV/bin/pip" install -r "$LOCK" 的退出码必须显式接住：pip 失败（网络/编译）若被忽略，
# 后面 probe 会以"库版本为空"的形式假装就绪（这类"上游静默失败→下游假绿"是本项目的高频坑）
if ! "$VENV/bin/pip" install --no-input --disable-pip-version-check -r "$LOCK"; then
  bad "pip 安装失败 ⇒ 不要继续。缺 PyMuPDF 的机器能过健康检查却转不出 PDF（改造方案 §12-D5 的成因）"
  exit 7
fi
"$VENV/bin/pip" --no-input install --disable-pip-version-check --upgrade pip >/dev/null 2>&1 || true

# ----------------------------- 5. 运行配置 -----------------------------
log "[5/8] 写 $FPD_ROOT/etc/fpd.env（远端侧配置，不含任何主站密钥）"
{
  echo "# fpd.env · 体验机侧配置（由 bootstrap-dispatch.sh 生成，可重复执行覆盖）"
  echo "# ★ 本文件禁止出现 DB_DSN / JWT_SECRET / 任何 API Key——远端是零密钥执行体。"
  echo "FPD_ROOT=$FPD_ROOT"
  echo "FPD_PYBIN=$VENV/bin/python3"
  echo "FPD_EXPIRE_DATE=${FPD_EXPIRE_DATE:-}"
  echo "FPD_TIMEOUT_SEC=${FPD_TIMEOUT_SEC:-615}"
  echo "FPD_TTL_SEC=${FPD_TTL_SEC:-3600}"
  echo "FPD_MEM_MAX_MB=$FPD_MEM_MAX_MB"
  # ★ 转换子进程真正的内存帽：ssh 会话不落进 fpd.slice，所以靠 fpdexec 里的 setrlimit 兜
  echo "FPD_RLIMIT_AS_MB=$FPD_MEM_MAX_MB"
  # 单个客户文件落盘上限：主站上传闸是 40MB，这里留一倍余量**只用来防"主站那道闸失效"**，
  # 不是第二道业务闸。2G/50G 的体验机上，写满盘比拒绝一单麻烦得多。
  echo "FPD_MAX_PUT_MB=${FPD_MAX_PUT_MB:-80}"
} > "$FPD_ROOT/etc/fpd.env"
chmod 0644 "$FPD_ROOT/etc/fpd.env"
[ -n "$FPD_EXPIRE_DATE" ] || warn "到期日没能自动算出（date -d 不可用？）⇒ 请手工填 FPD_EXPIRE_DATE，"
[ -n "$FPD_EXPIRE_DATE" ] || warn "  否则到期自毁 timer 会因为没有日期而不生效。"

# ----------------------------- 6. systemd：资源帽 + 两个 timer -----------------------------
log "[6/8] 安装 systemd 单元（fpd.slice 资源帽 / 清理 timer / 到期 timer）"
install -m 0644 "$SRC_DIR/fpd.slice"            /etc/systemd/system/fpd.slice
install -m 0644 "$SRC_DIR/fpd-sweep.service"    /etc/systemd/system/fpd-sweep.service
install -m 0644 "$SRC_DIR/fpd-sweep.timer"      /etc/systemd/system/fpd-sweep.timer
install -m 0644 "$SRC_DIR/fpd-expiry.service"   /etc/systemd/system/fpd-expiry.service
install -m 0644 "$SRC_DIR/fpd-expiry.timer"     /etc/systemd/system/fpd-expiry.timer
# 资源帽按机器实测算出的值改写（2G 机 ⇒ 1.2G 上限）；用 sed 改一行，不整文件覆写，便于人工加过的手改被发现
sed -i -E "s/^MemoryHigh=.*/MemoryHigh=${FPD_MEM_HIGH_MB}M/; s/^MemoryMax=.*/MemoryMax=${FPD_MEM_MAX_MB}M/" \
    /etc/systemd/system/fpd.slice
# 到期 timer 的 OnCalendar 用 fpd.env 里的日期；没日期就不启（宁可显式不生效，也不要"看着启了其实没定闹钟"）
systemctl daemon-reload
systemctl enable --now fpd-sweep.timer || warn "fpd-sweep.timer 启动失败"
if [ -n "$FPD_EXPIRE_DATE" ]; then
  sed -i -E "s|^OnCalendar=.*|OnCalendar=${FPD_EXPIRE_DATE} 03:30:00|" /etc/systemd/system/fpd-expiry.timer
  systemctl enable --now fpd-expiry.timer || warn "fpd-expiry.timer 启动失败"
else
  systemctl disable --now fpd-expiry.timer >/dev/null 2>&1 || true
  warn "FPD_EXPIRE_DATE 为空 ⇒ 到期自毁 timer 未启用（必须手工补，否则到期后远端可能还留着客户文件）"
fi

# ----------------------------- 7. sshd 收口 -----------------------------
log "[7/8] 安装 sshd 的 ForceCommand 收口（只允许跑 fpd-bridge）"
install -m 0644 "$SRC_DIR/sshd-fpd.conf" /etc/ssh/sshd_config.d/99-fpd.conf
if sshd -t 2>&1 | grep -Ei 'error|Bad' >/dev/null; then
  bad "sshd 配置校验不过 ⇒ 已回滚，不把 sshd 弄成起不来（那等于把自己锁在机器外面）"
  rm -f /etc/ssh/sshd_config.d/99-fpd.conf
  exit 8
fi
systemctl reload sshd 2>/dev/null || systemctl restart ssh 2>/dev/null || warn "sshd reload/restart 失败，请手工 systemctl restart sshd"

chown -R "$FPD_USER":"$(id -gn "$FPD_USER")" "$FPD_ROOT/w" "$FPD_ROOT/home"
chmod 700 "$FPD_ROOT/home/.ssh"; [ -f "$FPD_ROOT/home/.ssh/authorized_keys" ] && chmod 600 "$FPD_ROOT/home/.ssh/authorized_keys"

# ----------------------------- 8. 自检 -----------------------------
log "[8/8] 自检（apt/pip/字体/目录/systemd 一次跑完，缺什么直接点名）"
MISS=0
need_cmd() { command -v "$1" >/dev/null 2>&1 || { bad "缺命令 $2"; MISS=$((MISS+1)); }; }
need_cmd python3 python3
need_cmd fc-list fontconfig
need_cmd rsync rsync
need_cmd pdfinfo poppler-utils
need_cmd timeout coreutils
[ -x "$FPD_ROOT/bin/fpdexec.py" ] || { bad "缺 fpdexec.py"; MISS=$((MISS+1)); }
# 中文族数量：主站实测 15 族（Noto CJK 14 ＋ 普惠体 1）。这里只验"装了 Noto 那一包"的下限，
# 不写死 15（★ 写死就是"一装一卸立刻假红"的锁）；普惠体由 sync 脚本补，preflight 里再做两侧等值比对。
ZFAM="$(fc-list :lang=zh family 2>/dev/null | tr ',' '\n' | sed 's/^ *//' | sort -u | grep -c 'Noto.*CJK' || true)"
if [ "${ZFAM:-0}" -lt 8 ]; then bad "中文 Noto CJK 族只有 ${ZFAM:-0} 个 ⇒ fonts-noto-cjk 没装好（译文会变方框）"; MISS=$((MISS+1));
else log "  Noto CJK 族 $ZFAM 个（普惠体随后由主站 sync 补齐，两侧等值在 preflight 判）"; fi
[ -f "$FPD_ROOT/bin/assets/fonts/DroidSansFallbackFull.ttf" ] \
  && log "  兜底字体已在（overlay 链必需）" \
  || warn "  兜底字体还没有 ⇒ 必须跑主站 scripts/dispatch_sync.sh 推资产；缺它的机器 apply 必崩（改造方案 §12-D5）"
if systemctl is-active --quiet fpd-sweep.timer; then log "  fpd-sweep.timer 在跑"; else bad "fpd-sweep.timer 未运行"; MISS=$((MISS+1)); fi
if [ -n "$FPD_EXPIRE_DATE" ] && systemctl is-enabled --quiet fpd-expiry.timer; then
  log "  fpd-expiry.timer 已定 $FPD_EXPIRE_DATE 自清"
else
  warn "fpd-expiry.timer 未生效 ⇒ 到期自清这一重保险目前没有"
fi
# ★ 登录面自检：sshd 对 fpd 这一档到底生效了什么。
#   只看 sshd-fpd.conf 装没装上是不够的——ForceCommand 生效与否要问 `sshd -T`（它才是展开后的真值），
#   而账号 shell 必须是可执行的 shell（见 [1/8] 那条 nologin 教训），两项分开点红。
EFF_SHELL="$(getent passwd "$FPD_USER" | cut -d: -f7 || true)"
case "$EFF_SHELL" in
  /bin/sh|/bin/bash) log "  账号 shell=$EFF_SHELL（ForceCommand 可执行）" ;;
  *) bad "账号 shell=$EFF_SHELL ⇒ sshd 会用 \$SHELL -c 拉起 ForceCommand，nologin 系一律报 \"This account is currently not available\""; MISS=$((MISS+1)) ;;
esac
FC="$(sshd -T -C user=$FPD_USER,host=127.0.0.1,addr=127.0.0.1 2>/dev/null | awk '/^forcecommand /{print $2}' || true)"
if [ "$FC" = "$FPD_ROOT/bin/fpd-bridge" ]; then log "  ForceCommand 已收口到 fpd-bridge"; else bad "ForceCommand=$FC（应为 $FPD_ROOT/bin/fpd-bridge）⇒ 派发账号可能被当交互账号用"; MISS=$((MISS+1)); fi
AK_BYTES=0; [ -f "$FPD_ROOT/home/.ssh/authorized_keys" ] && AK_BYTES="$(wc -c < "$FPD_ROOT/home/.ssh/authorized_keys" 2>/dev/null || echo 0)"
if [ "${AK_BYTES:-0}" -ge 40 ]; then log "  派发公钥已在（$AK_BYTES 字节）"; else warn "  派发公钥缺失 ⇒ 主站现在拨不进来（补 FPD_PUBKEY 重跑本脚本即可）"; fi

# ★ 经 fpd-bridge 的通路自检（2026-09-29 加）：只取一次 probe，按"配置真值"逐条断言。
#   为什么非要有这一腿：本脚本其余自检都是 root 视角（包在不在、timer 在不在跑、sshd -T 展开值），
#   它们全绿也不能证明"主站拨进来能干活"。09-29 抓到的缺陷正好落在这个盲区里——
#   bridge 旧写法 `. fpd.env` 加载配置却不 export，直调 fpdexec 时一切正常，
#   走 bridge 时 FPD_EXPIRE_DATE / FPD_RLIMIT_AS_MB 全为空 ⇒ **到期自拒与内存帽两条都不生效，
#   且没有一行日志会喊**。下面五项断言就是把"没人喊"这一条补上。
#   ⚠️ stdin/stdout 都必须脱离 TTY：bridge 第一道拒绝就是 `-t 0 || -t 1`（防交互登录），
#      从交互终端裸跑会把它判成"bridge 拒服务"，那是自检写错，不是机器坏。
BRIDGE_JSON="$(mktemp /tmp/fpd-bridge-probe.XXXXXX.json)"
BRIDGE_ERR="$(mktemp /tmp/fpd-bridge-probe.XXXXXX.err)"
BRIDGE_VERDICT="$(mktemp /tmp/fpd-bridge-verdict.XXXXXX.txt)"
PROBE_HDR="$(printf '%s' '{"mode":"probe"}' | base64)"
printf '%s\n' "$PROBE_HDR" | sudo -u "$FPD_USER" "$FPD_ROOT/bin/fpd-bridge" >"$BRIDGE_JSON" 2>"$BRIDGE_ERR"
BRIDGE_RC=$?
if [ "$BRIDGE_RC" -ne 0 ]; then
  bad "经 fpd-bridge 的 probe 退出码 $BRIDGE_RC ⇒ 派发这条通路根本不通，禁止开闸。stderr: $(head -c 200 "$BRIDGE_ERR" 2>/dev/null || true)"
  MISS=$((MISS+1))
else
  : > "$BRIDGE_VERDICT"
  "$VENV/bin/python3" - "$BRIDGE_JSON" "$BRIDGE_VERDICT" <<'PYV' || bad "bridge probe 解析失败（见上面 stderr）"
import json, sys
bad = []
try:
    d = json.load(open(sys.argv[1]))
except Exception as e:
    bad.append("probe 输出不是 JSON: %s" % e)
    d = {}
if d:
    if not d.get("ok"):
        bad.append("ok=false")
    if d.get("expired"):
        bad.append("expired=true（今天已到达/超过到期日，本机已自拒）")
    if not (d.get("expire_date") or "").strip():
        bad.append("expire_date 为空 ⇒ fpd-bridge 没把 FPD_EXPIRE_DATE 交下来（到期自拒不生效）")
    caps = d.get("caps") or {}
    if not caps.get("rlimit_as_set"):
        bad.append("caps.rlimit_as_mb=%s ⇒ 转换子进程没有内存帽（env 未 export）" % caps.get("rlimit_as_mb"))
    if caps.get("clamped"):
        bad.append("caps.clamped=true ⇒ RLIMIT_AS 被系统硬上限压住，配置值实际吃不到")
    if not caps.get("timeout_bin"):
        bad.append("caps.timeout_bin=false ⇒ 缺 coreutils timeout，墙钟帽加不上")
    if d.get("selftest") != 0:
        bad.append("selftest=%s ⇒ 原版式主链在这台机器上跑不通（缺资产/缺库/脚本是旧版，见改造方案 §12-D5）" % d.get("selftest"))
    if not (d.get("pymupdf") or "").strip():
        bad.append("pymupdf 版本为空 ⇒ venv 里 PyMuPDF 不可导入")
with open(sys.argv[2], "w") as f:
    f.write("; ".join(bad))
PYV
  VERDICT="$(cat "$BRIDGE_VERDICT" 2>/dev/null || true)"
  if [ -n "${VERDICT:-}" ]; then
    bad "经 fpd-bridge 的通路自检不过: $VERDICT"
    MISS=$((MISS+1))
  else
    log "  经 fpd-bridge 的 probe 全绿：到期日/内存帽/timeout/selftest/pymupdf 五项都真吃到"
  fi
fi
rm -f "$BRIDGE_JSON" "$BRIDGE_ERR" "$BRIDGE_VERDICT"

# ★ 搬运三态自检：put → stat → get 在本机走一遍，逐字节比 sha。
#   这一段是"结果必回主站"那条硬口径的**最小可执行证明**：三态里任何一个坏掉，
#   派发出去的单子就永远回不来（远端转成功、主站拿到空文件、工单却显示成功）。
SMOKE_OK=1
FPD_GROUP="$(id -gn "$FPD_USER")"
"$VENV/bin/python3" - "$FPD_ROOT" "$FPD_USER" "$VENV/bin/python3" <<'PY' || SMOKE_OK=0
import base64, hashlib, json, os, subprocess, sys
root, user, pybin = sys.argv[1], sys.argv[2], sys.argv[3]
env = dict(os.environ, FPD_ROOT=root, FPD_PYBIN=pybin)
EX = os.path.join(root, "bin", "fpdexec.py")

def call(hdr, payload=b""):
    """以 fpd 身份**经 fpd-bridge** 跑一次 fpdexec（★ 2026-09-29 改：此前是直调 EX）。

    为什么必须走 bridge：ForceCommand 把生产上唯一的入口钉在 fpd-bridge 上，直调 fpdexec
    等于自证一条主站永远走不到的路径——bridge 里"加载 env / 拒绝规则"那几道缺陷会被完全跳过
    （09-29 那条 env 未 export 就是这么躲过自检的：直调时脚本自带 env，探测全绿）。
    走 bridge 还顺带把 stdin/stdout 的字节归属过了一遍真实实现：run 模式 stdout 必须只有
    被透传脚本的输出，而 bridge 是 exec 直传，不多不少。
    """
    head = base64.b64encode(json.dumps(hdr).encode()).decode() + "\n"
    return subprocess.run(["sudo", "-u", user, os.path.join(root, "bin", "fpd-bridge")],
                          input=head.encode() + payload,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)

blob = os.urandom(200000)
sha = hashlib.sha256(blob).hexdigest()
FAILS = []

def one(mode, payload=b"", rel=None):
    """走一次 bridge 并**如实**回 (rc, stdout, stderr)；不做任何"看起来像就行"的宽容判定。"""
    hdr = {"mode": mode}
    if rel is not None:
        hdr["rel"] = rel
    r = call(hdr, payload)
    return r.returncode, r.stdout, r.stderr.decode(errors="replace")

rc, so, se = one("put", blob, "_smoke/x.bin")
if rc != 0:
    FAILS.append("put rc=%d stderr=%s" % (rc, se[:200]))
else:
    try:
        ack = json.loads(so.decode().strip().splitlines()[-1])
    except Exception:
        FAILS.append("put 成功但回执不是 JSON（bridge 往 stdout 多写了东西？）: %r" % so[:120])
        ack = {}
    if ack.get("sha256") != sha:
        FAILS.append("put 回执 sha256=%s ≠ 本地 %s（写盘被截/被改）" % (ack.get("sha256"), sha[:12]))
    # ★ 必须带 size：只比 sha 的话，"落盘 0 字节但 sha 也算得出来"这类形态会被放过
    if int(ack.get("size") or -1) != len(blob):
        FAILS.append("put 回执 size=%s ≠ %d" % (ack.get("size"), len(blob)))

rc, so, se = one("stat", rel="_smoke/x.bin")
st = {}
if rc != 0:
    FAILS.append("stat rc=%d stderr=%s" % (rc, se[:200]))
else:
    try:
        st = json.loads(so.decode().strip().splitlines()[-1])
    except Exception:
        FAILS.append("stat 回执不是 JSON: %r" % so[:120])
    if st.get("sha256") != sha or int(st.get("size") or -1) != len(blob):
        FAILS.append("stat 读数与源件不等值: size=%s sha=%s" % (st.get("size"), str(st.get("sha256"))[:12]))

rc, got, se = one("get", rel="_smoke/x.bin")
if rc != 0:
    FAILS.append("get rc=%d stderr=%s" % (rc, se[:200]))
elif hashlib.sha256(got).hexdigest() != sha:
    FAILS.append("get 回来的字节与源件不等（%d/%d 字节）⇒ 产物回传这一腿不可用" % (len(got), len(blob)))
elif len(got) != len(blob):
    FAILS.append("get 长度不等: %d ≠ %d" % (len(got), len(blob)))

# 越界必须**拒且只拒得对**：78＝配置/边界拒绝。返回 0 就是守卫失效（能读 /etc/passwd）。
rc, so, se = one("get", rel="../../../../etc/passwd")
if rc != 78:
    FAILS.append("越界 rel 未被正确拒绝（rc=%d，应为 78）⇒ 红线②失效，禁止开闸" % rc)

if FAILS:
    print("❌ 搬运三态 / 边界 / 到期 自检未通过：")
    for f in FAILS:
        print("   - " + f)
    sys.exit(1)
print("✅ 搬运三态（put/stat/get 各自 size+sha 等值）+ 越界拒绝(78) 全部经 fpd-bridge 生效")
PY
if [ "$SMOKE_OK" -ne 1 ]; then bad "搬运三态自检未通过 ⇒ 产物回传这一腿不可用，禁止开闸"; MISS=$((MISS+1)); fi
rm -rf "$FPD_ROOT/w/_smoke"

# ★ 到期自拒的离线自证（2026-09-29 加）：这一腿平时**永远不会被触发**，不主动测就等于没有；
#   而它是"厂商忘了回收机器 / 主站回归 timer 没跑成功"两种失败的唯一双保险（红线③）。
#   做法：把 etc/fpd.env 里的 FPD_EXPIRE_DATE 临时改成过去日子，从 bridge 拨一次 probe ⇒ 必须非 0
#   （bridge 那道日期拒绝早于 fpdexec，所以连 EXPIRED 标记都不会落）；随后**无条件逐字节还原**，
#   还原不等值就点红——一次自检绝不能把机器留在"已到期"态。
#   ⚠️ 为什么在这里改文件而不是传环境变量：bridge 先 source fpd.env（且 set -a 会覆写继承值），
#      传进去的 FPD_EXPIRE_DATE 一定会被文件里的值盖掉——按 env 测会得到"假通过"。
if [ -f "$FPD_ROOT/EXPIRED" ]; then
  warn "  已存在 EXPIRED 标记 ⇒ 跳过到期自证（跳过≠通过，先把标记清掉再重跑本脚本）"
else
  ENVF="$FPD_ROOT/etc/fpd.env"
  ENV_BAK="$(mktemp /tmp/fpd-env.bak.XXXXXX)"
  ENV_SHA_BEFORE="$(sha256sum "$ENVF" 2>/dev/null | awk '{print $1}' || true)"
  if cp -p "$ENVF" "$ENV_BAK" 2>/dev/null && [ -s "$ENV_BAK" ]; then
    sed -i -E 's/^FPD_EXPIRE_DATE=.*/FPD_EXPIRE_DATE=2020-01-01/' "$ENVF"
    EXPIRY_ERR="$(mktemp /tmp/fpd-expiry-check.XXXXXX.err)"
    printf '%s\n' "$(printf '%s' '{"mode":"probe"}' | base64)" \
      | sudo -u "$FPD_USER" "$FPD_ROOT/bin/fpd-bridge" >/dev/null 2>"$EXPIRY_ERR"
    EXPIRY_RC=$?
    cp -p "$ENV_BAK" "$ENVF"           # 先还原，再判定：判红也不许把机器留在到期态
    ENV_SHA_AFTER="$(sha256sum "$ENVF" 2>/dev/null | awk '{print $1}' || true)"
    if [ "$EXPIRY_RC" -eq 0 ]; then
      bad "到期自拒失效：fpd.env 写 2020-01-01 时 bridge 仍放行 ⇒ 机器到期后还会接客户文件"
      MISS=$((MISS+1))
    else
      log "  到期自拒生效（临时把到期日设成 2020-01-01 ⇒ bridge exit=$EXPIRY_RC，文案：$(head -c 120 "$EXPIRY_ERR" | tr '\n' ' ')）"
    fi
    if [ -f "$FPD_ROOT/EXPIRED" ]; then
      bad "到期自证过程中落出了 EXPIRED 标记 ⇒ 拒绝路径写到了标记文件（红线③的实现不该在探测时落盘），请人工确认后删除"
      MISS=$((MISS+1))
    fi
    if [ -n "${ENV_SHA_BEFORE:-}" ] && [ "${ENV_SHA_BEFORE:-}" != "${ENV_SHA_AFTER:-}" ]; then
      bad "fpd.env 未逐字节还原（before=${ENV_SHA_BEFORE:0:12} after=${ENV_SHA_AFTER:0:12}）⇒ 立即人工核对 $ENVF"
      MISS=$((MISS+1))
    fi
    rm -f "$ENV_BAK" "$EXPIRY_ERR"
  else
    bad "备份 $ENVF 失败 ⇒ 到期自证没做（跳过≠通过）"
    MISS=$((MISS+1))
    rm -f "$ENV_BAK"
  fi
fi

"$VENV/bin/python3" - <<'PY' || MISS=$((MISS+1))
# 库导入自检：pymupdf 单独点名——它是 overlay 主链的真实依赖，也是主站清单里漏写的那一个
import importlib, sys
missing = []
for mod in ("pymupdf", "fpdf", "docx", "PIL", "fontTools"):
    try:
        importlib.import_module(mod)
    except Exception:
        missing.append(mod)
if missing:
    sys.stderr.write("❌ venv 里缺库: %s\n" % ", ".join(missing))
    sys.exit(1)
print("✅ venv 库齐（pymupdf/fpdf2/python-docx/Pillow/fonttools）")
PY

echo
if [ "$MISS" -eq 0 ]; then
  log "远端底座就绪。下一步：在主站执行 scripts/dispatch_sync.sh 推脚本与字体资产，再跑 dispatch_preflight.sh 做两侧等值校验。"
  exit 0
fi
bad "有 $MISS 项不就绪 ⇒ 先补齐再来。**派发是增益，不就绪时主站走本地路径，不影响可用性。**"
exit 9
