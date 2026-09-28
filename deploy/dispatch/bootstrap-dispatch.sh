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
  install -m 600 -o "$FPD_USER" -g "$(id -gn "$FPD_USER")" "$FPD_PUBKEY" "$AK"
  log "  已写入派发公钥（$(wc -c < "$AK") 字节）"
else
  warn "未提供 FPD_PUBKEY ⇒ $AK 为空，主站现在连不上来。装完必须补：把主站公钥原样写进该文件并 chmod 600"
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
for f in fpdexec.py; do install -m 0755 -o "$FPD_USER" -g "$(id -gn "$FPD_USER")" "$SRC_DIR/$f" "$FPD_ROOT/bin/$f"; done
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
    """以 fpd 身份跑一次 fpdexec（sudo -u 直接 exec，不经 nologin shell，和 ssh 那条路等价）。"""
    head = base64.b64encode(json.dumps(hdr).encode()).decode() + "\n"
    return subprocess.run(["sudo", "-u", user, pybin, EX], input=head.encode() + payload,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)

blob = os.urandom(200000)
sha = hashlib.sha256(blob).hexdigest()
r = call({"mode": "put", "rel": "_smoke/x.bin"}, blob)
assert r.returncode == 0, "put 失败: %s" % r.stderr.decode()[:200]
assert json.loads(r.stdout)["sha256"] == sha, "put 回来的 sha 不等"
r = call({"mode": "stat", "rel": "_smoke/x.bin"})
assert r.returncode == 0, "stat 失败: %s" % r.stderr.decode()[:200]
r = call({"mode": "get", "rel": "_smoke/x.bin"})
assert r.returncode == 0 and hashlib.sha256(r.stdout).hexdigest() == sha, "get 回来的字节与源件不等"
r = call({"mode": "get", "rel": "../../../../etc/passwd"})
assert r.returncode == 78, "越界 rel 竟然没被拒（rc=%d）" % r.returncode
print("✅ 搬运三态（put/stat/get）逐字节等值 + 越界拒绝生效")
PY
if [ "$SMOKE_OK" -ne 1 ]; then bad "搬运三态自检未通过 ⇒ 产物回传这一腿不可用，禁止开闸"; MISS=$((MISS+1)); fi
rm -rf "$FPD_ROOT/w/_smoke"

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
