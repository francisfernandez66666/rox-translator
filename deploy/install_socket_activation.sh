#!/usr/bin/env bash
# ============================================================================
# deploy/install_socket_activation.sh — #41 零停机第一阶段 S1 的**装配入口**
# （把"仓库里有 unit 文件"变成"现网真在用它监听"，同 10-10 那只闹钟的第二层教训）
#
# 为什么要有这一份（AGENTS §三「有脚本无调度要一直问到第二层」）：
#   本仓已经栽过两次同一形态：unit 与脚本都写好了、四步安装命令只写在**文件头注释**里，
#   于是 10-10 实测 `translator-dispatch-probe.timer` = not-found、**从写下那天起一次没响过**；
#   同一批只读复核又抓到 `translator-restore-drill.timer` 也是 not-found（演练从未自动跑过）。
#   socket 单元如果只落在注释里，就会长出第三种同形：仓库里有 `.socket`、
#   现网 8787 的持有者还是 `translator-server` —— 而这件事**没有任何症状**，
#   直到下一次 restart 才露出那串 502。⇒ 装配这一步必须是一条命令，不是一段注释。
#
# ★★ 本脚本的首要职责不是"装上"，是**拦住装错的顺序**：
#   先 enable socket、后上线认识 fd 的二进制 ⇒ 旧二进制自己 bind 同一地址撞 EADDRINUSE
#   ⇒ 单元进崩溃重启循环＝**停站**。所以任何写动作之前先核两道：
#     G1 两站的 `translator-server` 制品里都存在接管标记（BINARY_MARKER 那一条常量）；
#     G2 上传的两份 unit 与仓库**逐文件 sha 等值**（"送过去了"与"送的就是那一份"是两件事）。
#   任一不合即停在干跑段，一个字节都不写进 /etc。
#
# 四档（默认只读，任何写动作都要显式下令）：
#   bash deploy/install_socket_activation.sh              # 只读预检：上传到 /tmp＋逐文件 sha＋标记＋当前属主读数
#   bash deploy/install_socket_activation.sh --apply      # 落位：写 /etc/systemd/system → daemon-reload → enable --now → restart → 四条验收
#   bash deploy/install_socket_activation.sh --status     # 只读读数：谁持有 8787/8789、socket 单元状态、两站 health
#   bash deploy/install_socket_activation.sh --rollback   # 回滚：disable --now 两份 socket → restart → 回今天形态（不需要回滚代码）
#
# ★ 四条验收（方案文档 §六 里机器可查的那四条；其余要人工看延迟分位，不在本脚本射程）：
#   V1 属主列里出现 `systemd`（结构性判据：接管之后 `ss -lntp` 会同时列出 systemd 那份监听 socket
#      与继承它的业务进程；只有业务进程＝压根没接管，V2/V3/V4 照样能绿）；
#   V2 两站 `/api/health` 各 200（接管之后服务照常）；
#   V3 主站日志里出现接管那一行（观测腿，**不参与裁决**，只用来抓"日志压根没落盘"那一族观测失效）；
#   V4 **发版窗口内**的 5xx 与 P99（★ 2026-10-14 补，见下面 probe 三函数头注释）。
#   ⇒ 判绿只认末行 `SOCKET_APPLY_OK=1`；V1／V2／V4（非 2xx 那条）任一缺即 `SOCKET_APPLY_OK=0` 并退 1。
#   ⚠️ V4 的**两条腿分开判**：`non2xx=0` 是"502 归零"（socket activation 的本职），
#      `p99<=1s` 是"亚秒"（要靠 S3 启动路径瘦身才达标）——**S3-a 只把 Python 探测挪出关键路径，
#      其余段还没按读数瘦身**，所以今晚 V4 很可能出"5xx 归零、延迟仍秒级"这一形态，
#      那是**预期的诚实读数**，不是装配失败；两腿混成一条就会把"该继续做 S3-b"读成"整套方案没用"。
#
# ⚠️ 本脚本**绝不碰 caddy**（同 AGENTS §一·5：那台机器上 `caddy stop/start/restart` 打的是共享 2019 管理面，
#    会把全站停掉）。restart 只作用于 `translator`／`translator-demo` 两个 service 单元。
#
# 退出码：0＝该档该做的事做完且判据成立；1＝干跑有待办／验收不成立；2＝硬前置不满足。
# ============================================================================
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$REPO/deploy/deploy_to_production.sh"

# 主机／端口／用户一律从**现役发版脚本**现读，禁止在本文件写死地址（10-10 装配入口同口径）。
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$CONF" | head -1)"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$CONF" | head -1)"
RUSER="$(sed -n 's/^REMOTE_USER="${DEPLOY_USER:-\(.*\)}"/\1/p' "$CONF" | head -1)"
[ -n "$HOST" ] || { echo "ABORT=2 主机没解析出来（读 ${CONF}，别在命令行里写死地址）"; exit 2; }
[ -n "$PORT" ] || PORT=22
[ -n "$RUSER" ] || RUSER=root

UNITS=(deploy/systemd/translator.socket deploy/systemd/translator-demo.socket)
# 两站部署根：web 根之外的**部署根**，二进制就在 <root>/bin/translator-server。
BINS="/opt/translator/bin/translator-server /opt/translator-demo/bin/translator-server"

# BINARY_MARKER＝"这一支二进制认得 systemd 交来的 fd"的字面证据。
# ⚠️ 必须与 backend-go/cmd/server/listen_systemd.go 里那条常量**逐字一致**；
#    那边改名而这边没跟 ⇒ 本闸恒红（停在干跑，绝不 enable）——方向是宁可不装，不可停站。
BINARY_MARKER="LISTEN_FDS_TAKEOVER_V1"
# 接管那一行日志的锚（V3 观测腿用；与 listen_systemd.go 里的文案同源）
LOG_ANCHOR="已接管 systemd 交来的监听 fd"

MODE="dryrun"
while [ $# -gt 0 ]; do
  case "$1" in
    --apply) MODE="apply" ;;
    --rollback) MODE="rollback" ;;
    --status) MODE="status" ;;
    -h | --help) awk '/^set -uo/{exit} {print}' "${BASH_SOURCE[0]}" ; exit 0 ;;
    *) echo "未知参数：${1}（--apply／--rollback／--status／--help）" >&2 ; exit 2 ;;
  esac
  shift
done

SSH="ssh -o ConnectTimeout=20 -p $PORT $RUSER@$HOST"

# 远端只读读数一段（--status／验收／回滚三处共用，避免同一判据各读各的）。
# ⚠️ 属主读数**原样吐 ss 的第 6 列**（2026-10-14 现网实读校准：表头把 "Local Address:Port  Peer Address:PortProcess" 挤成一行看着像 7 列，真数据行里 Process 在 $6；按 $7 取会恒读 <无读数>），不在远端做截断：
#    `ss -lntp` 那一列形如 `users:(("translator-serve",pid=1234,fd=7))`，进程名本身被 ss 截过，
#    拿 sed 去剥引号/pid 会把读数削成"看着像 systemd"或"看着什么都不像"——
#    判据只需要问一件事：**这一列里有没有 systemd**（见下面的 case）。
#    ⇒ 这条读数腿本身要先在现网实读一次再写进判据（本批就是先写成 $7、--status 恒回 <无读数>才发现的）。
remote_status() {
  $SSH 'for p in 8787 8789; do
  owner=$(ss -lntp 2>/dev/null | awk -v a="127.0.0.1:$p" "\$4==a {print \$6}" | head -1)
  echo "PORT$p owner=${owner:-<无读数>}"
done
echo "socket_main=$(systemctl is-enabled translator.socket 2>&1 | head -1)/$(systemctl is-active translator.socket 2>&1 | head -1)"
echo "socket_demo=$(systemctl is-enabled translator-demo.socket 2>&1 | head -1)/$(systemctl is-active translator-demo.socket 2>&1 | head -1)"
echo "health_main=$(curl -s -o /dev/null -w "%{http_code}" -m 10 http://127.0.0.1:8787/api/health)"
echo "health_demo=$(curl -s -o /dev/null -w "%{http_code}" -m 10 http://127.0.0.1:8789/api/health)"'
}

if [ "$MODE" = "status" ]; then
  remote_status
  exit 0
fi

# ---------- 1 · 上传两份 unit，逐文件 sha 等值（G2） ----------
for f in "${UNITS[@]}"; do
  [ -f "$REPO/$f" ] || { echo "ABORT=2 仓库缺 $f"; exit 2; }
done
TAR=/tmp/socket_units.tgz
# COPYFILE_DISABLE＋--exclude：macOS 会把 ._* AppleDouble 一起塞进 tar（本仓在公网出过两次）
COPYFILE_DISABLE=1 tar --exclude='._*' --exclude='.DS_Store' -czf "$TAR" -C "$REPO" "${UNITS[@]}" \
  || { echo "ABORT=2 打包失败"; exit 2; }
$SSH 'mkdir -p /tmp/socket_units_stage && rm -rf /tmp/socket_units_stage/*' >/dev/null 2>&1
scp -o ConnectTimeout=20 -P "$PORT" "$TAR" "$RUSER@$HOST:/tmp/socket_units.tgz" >/dev/null \
  || { echo "ABORT=2 上传失败"; exit 2; }

# 逐文件比 sha（不是整包）：整包等值只证明"传全了"，不证明"每一份都对"，
# 而落位是按文件 cp 的——一份漂了，另一份照样装上去。
SHA_FAIL=0
for f in "${UNITS[@]}"; do
  l=$( (cd "$REPO" && shasum -a 256 "$f") | awk '{print $1}')
  r=$($SSH "sha256sum /tmp/socket_units.tgz >/dev/null 2>&1 && tar -xOf /tmp/socket_units.tgz '$f' | sha256sum | cut -d' ' -f1" | tr -d '\r')
  [ -n "$r" ] || { echo "G2=bad $f 远端读不到内容（链路问题，不等于通过）"; SHA_FAIL=1; continue; }
  if [ "$l" = "$r" ]; then echo "G2=ok $f"; else echo "G2=bad $f local=$l remote=$r"; SHA_FAIL=1; fi
done
[ "$SHA_FAIL" = "0" ] || { echo "ABORT=2 上传件与仓库不一致，停在写动作之前"; exit 2; }

# ---------- 2 · 远端只读预检（G1 标记＋当前属主＋systemd 认不认这两份文件） ----------
PRE=$($SSH "bash -s" <<REMOTE
set -uo pipefail
mkdir -p /tmp/socket_units_pre && tar -xzf /tmp/socket_units.tgz -C /tmp/socket_units_pre
for b in $BINS; do
  if [ ! -f "\$b" ]; then echo "G1=\$b missing"; continue; fi
  hits=\$(grep -ac "$BINARY_MARKER" "\$b" 2>/dev/null || true)
  echo "G1=\$b hits=\${hits:-0}"
done
for u in translator.socket translator-demo.socket; do
  p=/tmp/socket_units_pre/deploy/systemd/\$u
  if systemd-analyze verify "\$p" >/tmp/sa.\$u.log 2>&1; then echo "VERIFY=\$u ok"; else echo "VERIFY=\$u bad"; sed -n '1,3p' /tmp/sa.\$u.log; fi
done
systemd-analyze verify /tmp/socket_units_pre/deploy/systemd/translator.socket 2>&1 | grep -c "ListenStream" >/dev/null || true
REMOTE
)
echo "$PRE"
# G1：两台制品都必须命中标记。缺＝那一台还没换 S2 的件 ⇒ enable socket 会把它打进重启循环。
G1_BAD=0
for b in $BINS; do
  line=$(echo "$PRE" | grep "^G1=$b " || true)
  [ -n "$line" ] || { echo "ABORT=2 $b 没有预检读数（远段没跑到位，不等于通过）"; exit 2; }
  case "$line" in
    *missing*|*hits=0*) G1_BAD=1 ;;
  esac
done
if [ "$G1_BAD" = "1" ]; then
  echo "ABORT=2 现网二进制里没有 socket 接管标记（${BINARY_MARKER}）⇒ 顺序不成立，停在干跑。"
  echo "        先按正常发版把带接管逻辑的二进制换上两站（S2），再回来跑本脚本 --apply。"
  exit 2
fi
case "$PRE" in *"VERIFY="*bad*) echo "ABORT=2 systemd 不认这两份 unit 文件"; exit 2 ;; esac
echo "$PRE" | grep -q "G1=.*hits=" || { echo "ABORT=2 标记预检那一腿没有读数"; exit 2; }

if [ "$MODE" = "dryrun" ]; then
  echo "---- 当前属主读数 ----"
  remote_status
  echo "DRYRUN_ALL_OK=1 预检全过（未写 /etc、未碰 systemd；--status 档位可随时复看）"
  echo "下一步：bash deploy/install_socket_activation.sh --apply"
  exit 0
fi

# ---------- 3 · 回滚档（disable --now ＋ restart，不需要回滚代码） ----------
if [ "$MODE" = "rollback" ]; then
  $SSH 'systemctl disable --now translator.socket translator-demo.socket 2>&1 | tail -4
        systemctl restart translator translator-demo' || { echo "ROLLBACK=1 命令没走完"; exit 1; }
  sleep 8
  OUT=$(remote_status)
  echo "$OUT"
  # 回滚成立的判据＝属主回到业务进程（不是 systemd）且两站 health 200
  if echo "$OUT" | grep -q "PORT8787 owner=systemd" || echo "$OUT" | grep -q "PORT8789 owner=systemd"; then
    echo "ROLLBACK_OK=0 socket 单元还在持有端口，回滚没落到位"
    exit 1
  fi
  echo "$OUT" | grep -q "health_main=200" && echo "$OUT" | grep -q "health_demo=200" \
    || { echo "ROLLBACK_OK=0 回滚后 health 不是 200，按 --status 排障"; exit 1; }
  echo "ROLLBACK_OK=1 已回到今天形态（进程自己 bind、两站 health 200）"
  exit 0
fi

# ---------- 4 · 落位（只有 --apply 走到这里，且 G1/G2 已全过） ----------
TS=$(date +%Y%m%d_%H%M%S)
# ★ V4 窗口探针（2026-10-14 补）：**--apply 里那次 restart 就是本机第一次零停机窗口**，
#   不在这一次采样的话，方案 §八 第 3 条那条锁整晚没有读数——而"装上但没验过"正是本仓
#   「有脚本无调度」那一族的第三种形态（unit 装上了、零停机从没被数过）。
#   采样腿与落位**在同一段远端脚本里**（跨两次 ssh 起后台进程会把 pid 交给没人认领的会话，
#   而且两次往返之间就是窗口，读数会漏）；判据的 awk 与 swap_web_and_binary.sh 的
#   probe_status 逐字同源，由 window_probe_ruler_test.go 机械锁住那份等值。
PROBE_URLS="${WINDOW_PROBE_URLS:-http://127.0.0.1:8787/livez http://127.0.0.1:8789/livez}"
PROBE_P99="${WINDOW_PROBE_P99_SEC:-1}"
APPLY_OUT=$($SSH "bash -s" <<REMOTE
set -uo pipefail
PDIR=/tmp/window_probe.$TS
mkdir -p "\$PDIR"; : > "\$PDIR/log"; rm -f "\$PDIR/stop"
# 采样器单独写成一份临时脚本再用 nohup 起：内层 heredoc 用**引号定界符**，
# 于是下面这些 \$ 全部原样落到远端，避免"外层本地展开一次、内层远端再展开一次"的两层引号地狱。
cat > "\$PDIR/sampler.sh" <<'SAMPLER'
#!/usr/bin/env bash
# 每 0.2s 一轮，直到 stop 文件落下（另有 400 轮硬上限，防止主流程中途失败留下孤儿采样器）。
dir="\$1"; urls="\$2"
i=0
while [ ! -f "\$dir/stop" ] && [ "\$i" -lt 400 ]; do
  i=\$((i + 1))
  for u in \$urls; do
    r=\$(curl -s -o /dev/null -w '%{http_code} %{time_total}' --max-time 15 "\$u" 2>/dev/null || echo "000 15.000")
    echo "\$u \$r" >> "\$dir/log"
  done
  sleep 0.2
done
SAMPLER
nohup bash "\$PDIR/sampler.sh" "\$PDIR" "$PROBE_URLS" >/dev/null 2>&1 &
PROBE_PID=\$!
tar -xzf /tmp/socket_units.tgz -C /tmp/socket_units_pre
cd /tmp/socket_units_pre/deploy/systemd || { echo 'APPLY=2 解包目录形状不对'; exit 2; }
for u in translator.socket translator-demo.socket; do
  dst=/etc/systemd/system/\$u
  if [ -f "\$dst" ] && ! cmp -s "\$u" "\$dst"; then
    cp -a "\$dst" "\$dst.bak.$TS" && echo "BAK=\$u"
  fi
  install -m 0644 "\$u" "\$dst" || { echo 'APPLY=2 落位失败 '\$u; exit 2; }
  # 落位后逐字节回验：写进去了不等于写对了
  cmp -s "\$u" "\$dst" && echo "LANDED=\$u byte_equal" || { echo 'APPLY=2 内容与仓库不一致'; exit 2; }
done
systemctl daemon-reload
# ★★ 2026-10-16 现网实测补上的一步：**首次装配必须先停服务，才能启用 socket**。
#   旧形态（enable --now → restart）在真机首跑直接失败，journal 原文：
#     `translator.socket: Socket service translator.service already active, refusing.`
#   机理：systemd 只在**服务被启动**的那一瞬交接 fd；关联 service 还在跑时它拒绝让 socket
#   去绑同一地址（否则两个持有者并存，它无从决定谁 accept）。所以首次装配的硬顺序是
#     stop（让出 127.0.0.1:8787） → enable --now（socket 立刻 bind，此后恒在 listen）→ 服务启动/重启。
#   ⚠️ 这一停一启是**首次装配自身的一次性窗口**（今天的 `restart` 每跳都有窗口，装上之后不再有）；
#     它会计进本次 V4_STATS 的 non2xx —— 判绿的口径因此是"**装上之后再跑一次 --apply**"：
#     第二次进来 socket 已 active ⇒ 不 stop（见下面的 FIRST 分支）⇒ restart 由 socket 顶着 ⇒ non2xx=0。
FIRST=0
systemctl is-active --quiet translator.socket || FIRST=1
if [ "\$FIRST" = "1" ]; then
  echo "FIRST_INSTALL=1（socket 尚未接管 ⇒ 本次要先停服务让出端口，这一次窗口是一次性的）"
  systemctl stop translator translator-demo
fi
systemctl enable --now translator.socket translator-demo.socket
# 启用之后必须**当场**问一句"真在 listen 吗"：旧形态把 `Job failed` 淹在几十行输出中间，
# 靠末尾的 V1 兜底——那是"半装"形态（socket 单元 enabled 却 inactive），排障得倒回去找原因。
# ★ 失败一律**回位**（disable socket ＋ 把服务拉回自绑形态）再停手：这一步刚刚 stop 过两个服务，
#   只管判死不管回位＝把"没装上"升级成"停站"，方向反了（同文件头「宁可不装，不可停站」）。
SOCK_OK=1
for s in translator.socket translator-demo.socket; do
  systemctl is-active "\$s" >/dev/null 2>&1 || SOCK_OK=0
done
if [ "\$SOCK_OK" = "0" ]; then
  systemctl disable --now translator.socket translator-demo.socket >/dev/null 2>&1
  systemctl start translator translator-demo >/dev/null 2>&1
  echo "APPLY=2 socket 启用后不是 active（enable 没生效／端口被别的持有者占着）⇒ 已回位到现状形态（disable socket ＋ start 服务）；根因看 journalctl -u translator.socket"
  exit 2
fi
systemctl restart translator translator-demo
sleep 8
# 服务必须真起来：接管分支与"自己 bind"同时发生＝EADDRINUSE＝崩溃重启循环，这是那条族的拦口
for s in translator translator-demo; do
  systemctl is-active "\$s" >/dev/null 2>&1 || {
    systemctl disable --now translator.socket translator-demo.socket >/dev/null 2>&1
    systemctl restart "\$s" >/dev/null 2>&1
    echo "APPLY=2 \$s 在 socket 接管后起不来（疑似 EADDRINUSE：接管没生效又自绑）⇒ 已回位；根因看 journalctl -u \$s"
    exit 2
  }
done
# 窗口到此结束：先收摊采样腿，再读三份统计（顺序错了就把"收摊之后"的空档算进窗口）。
: > "\$PDIR/stop"
wait "\$PROBE_PID" 2>/dev/null || true
STATS=\$(awk 'NF>=2 {n++; if (\$2 !~ /^2/) bad++; if (\$2 == "000") zero++; t[n]=\$3+0}
       END {
         if (n==0) { print "0 0 0 none"; exit }
         for (i=1;i<=n;i++) for (j=i+1;j<=n;j++) if (t[j]<t[i]) { x=t[i]; t[i]=t[j]; t[j]=x }
         idx=int(n*0.99); if (idx<1) idx=1; if (idx>n) idx=n
         printf "%d %d %d %.3f\\n", n, bad, zero, t[idx]
       }' "\$PDIR/log" 2>/dev/null)
echo "V4_STATS=\$STATS"
echo "APPLY=done"
rm -rf "\$PDIR"
REMOTE
)
echo "$APPLY_OUT"
echo "$APPLY_OUT" | grep -q "APPLY=done" || { echo "SOCKET_APPLY_OK=0 落位段没走完，按 --status 核读数"; exit 2; }

# ---------- 5 · 四条验收（V1／V2 裁决，V3 观测，V4 窗口） ----------
sleep 8
V=$(remote_status)
echo "$V"
BAD=""
for p in 8787 8789; do
  line=$(echo "$V" | grep "^PORT$p " || true)
  # ★ 2026-10-16 现网实读校准：接管之后 `ss -lntp` 那一列**同时**列出两个持有者
  #   （systemd 那份监听 socket 与继承了它的业务进程），形如
  #     owner=users:(("translator-serv",pid=1187492,fd=13),("systemd",pid=1,fd=79))
  #   于是旧判据 `*owner=systemd*` 恒不命中——那样会把一次**成功**的装配判成 V1 不合格
  #   （本批首跑就踩到：socket 已 active、V3 接管行也在，只有 V1 红）。
  #   判据的射程不变：**属主列里必须出现 systemd**；没接管的形态那一列只有业务进程。
  case "$line" in
    *owner=systemd*|*'"systemd"'*) : ;;
    *) BAD="$BAD V1:$p=${line:-<无读数>}" ;;
  esac
done
for k in main demo; do
  code=$(echo "$V" | sed -n "s/^health_$k=//p")
  [ "$code" = "200" ] || BAD="$BAD V2:$k=${code:-<取不到>}"
done
# V3 观测腿：主站日志**不在 journald**（StandardOutput=append: 落文件），所以落点要现取；
# ⚠️ 别用 `systemctl show -p StandardOutput --value`——它会把路径截掉只剩 "append"（本仓 10-08 实测过）。
LOG_HIT=$($SSH 'f=$(systemctl cat translator 2>/dev/null | sed -n "s/^StandardOutput=append://p" | head -1)
if [ -n "$f" ] && [ -f "$f" ]; then grep -c "'"$LOG_ANCHOR"'" "$f" 2>/dev/null || true; else journalctl -u translator --no-pager 2>/dev/null | grep -c "'"$LOG_ANCHOR"'" || true; fi' | tr -d '\r')
echo "V3_takeover_log_lines=${LOG_HIT:-<无读数>}（观测腿，不参与裁决；0＝落点读错了地方）"

# V4 窗口腿：从落位段那份输出里现读统计（**不重新采样**——窗口已经过去了，重采样只能读到平稳态）。
# ⚠️ 三条读数的裁决地位不同，这是刻意分的：
#   · 样本数＝0 ⇒ 判红。配了 URL 却没采到＝这条锁空转（同 swap 脚本那条纪律）。
#   · 非 2xx≠0 ⇒ 判红。这是 socket activation 的**本职**（502 归零），没做到就是没装上。
#   · P99 ≥ 阈值 ⇒ **只出名不裁决**：亚秒要靠 S3 的启动路径瘦身，而瘦身幅度必须按真数决定
#     （方案 §四·S3 与 §十 第五批都写明"达标即收工、不达标才评估 S3-b"）。
#     把它写成硬失败，等于让装配脚本替人做一个还没有读数的决定。
V4=$(echo "$APPLY_OUT" | sed -n 's/^V4_STATS=//p' | head -1 | tr -d '\r')
V4_N=$(echo "$V4" | awk '{print $1}')
V4_BAD=$(echo "$V4" | awk '{print $2}')
V4_ZERO=$(echo "$V4" | awk '{print $3}')
V4_P99=$(echo "$V4" | awk '{print $4}')
if [ -z "$V4" ]; then
  BAD="$BAD V4:<无读数（落位段没出统计）>"
elif [ -z "$V4_N" ] || [ "$V4_N" = "0" ]; then
  BAD="$BAD V4:零样本"
else
  echo "V4_window samples=$V4_N non2xx=$V4_BAD timeout000=$V4_ZERO p99_s=$V4_P99 urls=$PROBE_URLS"
  [ "${V4_BAD:-1}" = "0" ] || BAD="$BAD V4:窗口内非2xx=$V4_BAD"
  # 首次装配那一次窗口是**一次性的**（上面 FIRST_INSTALL 那段讲了机理）：把提示写在紧挨判据的地方，
  # 否则下一个人看到 non2xx=4 会先去怀疑 socket activation 没用——而它恰恰刚被证明接管住了。
  if [ "${V4_BAD:-0}" != "0" ] && echo "$APPLY_OUT" | grep -q "^FIRST_INSTALL=1"; then
    echo "V4_FIRST_INSTALL_NOTE 本次是首次装配（服务让位→socket 绑定那一跳），non2xx=${V4_BAD} 属一次性窗口；"
    echo "                       验收请**再跑一次 --apply**（第二次不 stop，restart 由 socket 顶着）判 non2xx=0。"
  fi
  LATE=$(awk -v v="${V4_P99:-99}" -v w="$PROBE_P99" 'BEGIN{print (v+0>w+0)?"1":"0"}')
  if [ "$LATE" = "1" ]; then
    echo "V4_DELAY_NOTE P99=${V4_P99}s ≥ ${PROBE_P99}s ⇒ 5xx 归零已达成、延迟还没进亚秒（本条**不参与裁决**，是 S3 下一步的输入读数）"
  else
    echo "V4_DELAY_NOTE P99=${V4_P99}s < ${PROBE_P99}s（亚秒达标）"
  fi
fi

if [ -n "$BAD" ]; then
  echo "SOCKET_APPLY_OK=0 不合格项：$BAD"
  echo "  ⇒ 回滚一条命令：bash deploy/install_socket_activation.sh --rollback"
  exit 1
fi
echo "SOCKET_APPLY_OK=1 8787/8789 属主＝systemd、两站 health 200、发版窗口内非 2xx＝0"
echo "剩下的事不在本脚本射程里：启动各阶段耗时看 journal／服务日志里的「启动阶段计时」行（#41 S3），"
echo "  V4_DELAY_NOTE 那条 P99 读数就是它的窗口侧对照；两数一起才决定 S3-b/S3-c 做不做。"
exit 0
