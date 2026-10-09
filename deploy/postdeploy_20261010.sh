#!/usr/bin/env bash
# deploy/postdeploy_20261010.sh —— 〇-AR 第 9 波换件（`release_20261010.sh`，两台 translator-server）后的现网接线复查（**全程只读，不重启、不写库、不落位任何文件**）
#   ⚠️ 本文件是从第 8 波那一份复查脚本扩出来的，所以 B/D/E 三段读的仍是第 8 波那三条（㊾／㊶／(52)）
#      ——它们**换了件但还没到出读数的时间窗**，不是过时段落；本波新增的是 **C 段的 (53)** 与 **F 段的 #29**。
#      ★ (54) 在这里**没有对应判据**，这是刻意的：它那两条（匹配查询 err 腿／按链各开窗口）在现网
#      没有可复现的故障形态（查询不失败、孤儿没积压），拿"现网读数正常"当"已验证"就是假绿
#      ⇒ 它的账只有单测＋反证，台账按**已上线·现网无读数**记，别在这份脚本里补一条恒绿的锁。
#
# 它补的是 release_20261010.sh（以及它复用的第 8 波那批改动）当场**取不到**或**要等时间窗**的那几条读数，一条都不重复写数字：
#   ① ㊾（TRON 链头）：**三条腿**一起读——状态词 ＋ 失败行数归 0 ＋ **扫描成功才会写的库侧游标**
#      （`system_config.usdt_cursor_<链>` 的 `updated_at` 晚于本单元启动）。
#      只看"没有失败行"是不够的——监听压根没跑时失败行也是 0（假绿）。
#      ⚠️ 第三条原先写的是"日志里带 [usdt-watch] 的行数 >0"，那是**结构性必红**：
#      那个前缀只在失败三处落日志，健康轮次一行都不写（2026-10-10 复查实测抓到，细则见 B 段头部）。
#   ② 告警归宿：`usdt_watch_dead` 那条 open 行有没有随健康轮次自动收敛，
#      以及当时 `open=20` 那一堆到底是什么 kind（不分组就永远不知道 20 条里有几条是本批的）。
#   ③ ㊶（回译拼尾）：`/metrics` 的 purity 序列接线（HELP/TYPE 在位＝计数器接了线）。
#      ★ 现网**没有真退化样本时该序列本来就是空**，所以本段只判接线，不判计数值——
#      把"没抓到退化"写成"修法生效"是假绿，写反了也一样。
#   ④ (52)（清理零读数）：挂件 `/health` 的 `anonym_cleanup` 四档（never/idle/ok/failing）。
#   ⑤ 代际等值：挂件件里 `cannedPromptRev` 仍是 `0AR-1`（本批刻意没抬 ⇒ 不存在"换件没修"）。
#   ⑥ #29（有脚本无调度）：派发探针闹钟**在不在位**＋工作目录属主。装了没有是这条的唯一判据。
#   ⑦ 前端本轮**没换源**，所以这里按用户口径做一次**字节级对账**替"我判断不用换"背书：
#      本机 dist 入口名/asset sha256 ＝＝ 两站 index.html 引用 ＝＝ 公网抓回的那一个 asset。
#      ⚠️ 抓 asset 的判据必须是 sha256 等值，不能只看 200——`spa.go` 对不存在的路径兜底回
#      index.html 且状态码仍是 200（AGENTS §一·6），只看状态码会把"路径写错"读成"前端一致"。
#   ⑧ ★ 本波**没换** `cmd/fpdprobe`（原计划"随批刷新"的三条实测读数把它推翻了，见
#      `release_20261010.sh` 头部那段更正）⇒ F 段对探针只做**只读核验**：现役件里 `fpdexec` 特征串
#      必须命中 ≥1（它就是补丁五那一枚）＋ `--help` 起得来。装配闹钟（F 段）之后，
#      探针第一次真跑要等 05:20 那个日历点或手工 `systemctl start`，**这次复查里不代替它跑**。
#   ⑨ (54) **没有现网判据，这是刻意安排**：它那两条（匹配查询 err 腿／未匹配池按链各开窗口）
#      在现网都没有可复现的故障形态（查询不失败、孤儿远未积压到 200），
#      所以账只能记成**已上线·现网无读数**，靠单测＋三条反证支撑。
#      ⚠️ 别为了"这一条也要有绿灯"在脚本里补一条恒真判据——那是给未验证发证（§一·12 桩只认协议同族）。
#
# ★ 三条现成口径（都在这条链上真踩出来过，别再写回去）：
#   · 挂件健康面是 `/health`，**不是** `/api/health`；`translator`／`ai-assist` 的日志**不落 journal**
#     （`StandardOutput=append:` 落文件），`journalctl -u translator` 恒 0 行属结构性空转；
#   · `demo.lexicorn.cn` 没有自己的站点块，走通配块打到 **8787（主站）**；演示单元（8789）的真域名是
#     `rox-test.lexicorn.cn` ⇒ 读演示单元必须走 8789 或 rox-test，拿 demo 域读到的是主站；
#   · 库侧一律 `sudo -u postgres psql`，**不读 secrets.env 的 DSN、不把口令带进环境、不打印**；
#     `/metrics` 的 Bearer token 只出**长度**（`METRICS_TOKEN` 一旦在别处出现过就地轮换）。
#
# 用法：bash deploy/postdeploy_20261010.sh
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$REPO/deploy/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$CONF" | head -1)"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$CONF" | head -1)"
[ -n "$HOST" ] || { echo "ABORT=1 主机没解析出来（读 $CONF）"; exit 2; }
[ -n "$PORT" ] || PORT=22

# ── 本机侧：前端 dist 事实（远端拿它做等值对照）──
DIST="$REPO/frontend-react/dist"
[ -d "$DIST" ] || { echo "ABORT=1 本机没有 dist（$DIST）——前端对账这一段没法做"; exit 2; }
L_ENTRY=$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$DIST/index.html" | head -1)
[ -n "$L_ENTRY" ] || { echo "ABORT=1 dist/index.html 里取不到入口 asset 名"; exit 2; }
L_ASSET="$DIST/$L_ENTRY"
L_SHA=$(shasum -a 256 "$L_ASSET" | awk '{print $1}')
L_SIZE=$(wc -c <"$L_ASSET" | tr -d ' ')
L_HOME_SHA=$(shasum -a 256 "$DIST/index.html" | awk '{print $1}')
L_HOME_SIZE=$(wc -c <"$DIST/index.html" | tr -d ' ')
# dist 是否**不早于**前端源码最后一改（换了源代码却没重构建＝对账无效，先在这里拦住）
L_SRC_NEWEST=$(git -C "$REPO" log -1 --format='%ct' -- frontend-react/src frontend-react/public extension sdk 2>/dev/null || echo 0)
L_DIST_MTIME=$(stat -f %m "$DIST/index.html" 2>/dev/null || stat -c %Y "$DIST/index.html" 2>/dev/null || echo 0)

echo "===== 发版后接线复查 本机时刻=$(date -u +%FT%TZ)（只读，不动现网一个字节）====="
echo "本机 dist: entry=$L_ENTRY asset_sha256=$L_SHA asset_size=$L_SIZE index_sha256=$L_HOME_SHA index_size=$L_HOME_SIZE"
echo "本机 dist 新鲜度: dist/index.html mtime=$L_DIST_MTIME 前端源码最后提交=$L_SRC_NEWEST 落后源码秒=$((L_SRC_NEWEST - L_DIST_MTIME))（>0＝dist 比源码旧，下面的前端对账判『无效』不是『通过』）"

ssh -p "$PORT" -o ConnectTimeout=20 -o BatchMode=yes "root@$HOST" bash -s -- "$L_ENTRY" "$L_SHA" "$L_SIZE" "$L_HOME_SIZE" <<'REMOTE_EOF'
set -uo pipefail
ENTRY="$1"; WANT_SHA="$2"; WANT_SIZE="$3"; WANT_HOME="$4"
LOGF=/opt/translator/log/translator.log
ALOG=/opt/ai-assist/data/assist.log
RC=0
bad() { echo "  FAIL $1"; RC=1; }
ok()  { echo "  ✔ $1"; }
# ★ systemd 状态读数必须走这一个函数，不许再写 `$(systemctl is-enabled X || echo '<未装>')`：
#   unit 不存在时 systemctl **既**往 stdout 打 "not-found"**又**退非 0，于是命令替换把两个值都收进来，
#   读数行变成 `enabled=not-found\n<未装>` 两行；更坏的是 `PT_EN` 里带换行，判据永远不等于
#   `enabled`（方向倒是对的），但那条 FAIL 文案被劈成三行，看日志的人会先怀疑脚本坏了。
#   （10-10 复跑实测抓到；空值回落 `<未读>`，绝不回落成"看着像正常"的词。）
sysd() {  # sysd <is-enabled|is-active> <unit>
  local v
  v=$(systemctl "$1" "$2" 2>/dev/null) || true
  [ -n "$v" ] || v='<未读>'
  printf '%s' "$v"
}

# ★ 时间窗锚点＝**该单元本次启动的那一刻**，不是「近 N 分钟」的墙上钟（10-10 实测自伤后改）。
# 为什么：本脚本跑在换件之后，而旧件那几轮的失败行还在同一个日志文件里。按墙上 10 分钟数，
# 窗口会横跨重启点，把旧件的账算到新件头上——现网第一次跑就是这样报的 `fails=4 scans=4`，
# 而同一时刻 /api/health 回的是 usdt_watch=ok、ERROR=0（假红）。
# 口径：/proc/<pid>/stat 第 22 字段＝进程启动时刻（**自开机**的时钟滴答），加 /proc/stat 的 btime
# 才是 epoch；再按本机时区（CST）格式化成日志里那套 ISO 前缀，才能和字符串比较对上。
unit_start_iso() {
  local pid line st btime hz epoch
  pid=$(systemctl show -p MainPID --value "$1" 2>/dev/null)
  [ -n "$pid" ] && [ "$pid" != "0" ] || { echo ""; return 0; }
  # comm 字段本身带括号且可能含空格 ⇒ 先把「…)」之前的一律削掉，剩下的第 20 段才是 starttime
  #（原第 22 段减去 state＋comm 两段）。直接 awk '{print $22}' 遇到带空格的进程名会错位。
  line=$(sed 's/.*) //' "/proc/$pid/stat" 2>/dev/null)
  st=$(printf '%s' "$line" | awk '{print $20}')
  btime=$(awk '/^btime /{print $2}' /proc/stat 2>/dev/null)
  hz=$(getconf CLK_TCK 2>/dev/null)
  [ -n "$st" ] && [ -n "$btime" ] && [ -n "$hz" ] || { echo ""; return 0; }
  epoch=$((btime + st / hz))
  date -d "@$epoch" '+%Y-%m-%dT%H:%M:%S'
}

echo
echo "----- A 三单元存活＋现役件（『哪一件在跑』只认 /proc/<pid>/exe，不认文件时间戳）-----"
for pair in "translator:/opt/translator/bin/translator-server" "translator-demo:/opt/translator-demo/bin/translator-server" "ai-assist:/opt/ai-assist/bin/translator-assist"; do
  u="${pair%%:*}"; f="${pair#*:}"
  pid=$(systemctl show -p MainPID --value "$u" 2>/dev/null)
  exe=$(readlink "/proc/$pid/exe" 2>/dev/null || echo '<读不到>')
  es=$(sha256sum "$exe" 2>/dev/null | cut -d' ' -f1)
  ds=$(sha256sum "$f" 2>/dev/null | cut -d' ' -f1)
  st=$(systemctl is-active "$u" 2>/dev/null)
  nr=$(systemctl show -p NRestarts --value "$u" 2>/dev/null)
  if [ "$st" = "active" ] && [ -n "$es" ] && [ "$es" = "$ds" ]; then
    ok "$u active pid=$pid NRestarts=$nr 现役件 sha256＝磁盘件（${es:0:16}…）"
  else
    bad "$u active=$st exe_sha=${es:0:16} 磁盘 sha=${ds:0:16}（不等＝跑的不是落位那件，或 NRestarts 在涨：$nr）"
  fi
done
for p in 8787 8789 8790; do
  face=/api/health; [ "$p" = "8790" ] && face=/health
  echo "  port=$p $face=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://127.0.0.1:$p$face")"
done

echo
echo "----- B ㊾ 三条腿一起读：状态词＋失败行数＋**成功轮次在库里留没留写入**（缺第三条＝'压根没跑'读成'没问题'）-----"
# ★★ 第三条正腿为什么从日志搬到库里（2026-10-10 现网复查第一次跑就是被这条判据红的）：
#   `[usdt-watch]` 这个前缀在代码里**只出现在失败三处**（扫描失败／入账落库失败／自动入账失败），
#   成功那一轮走的是 `MarkUSDTDepositSeenBlock`，一行带该前缀的日志都不写
#   ⇒ "健康态 [usdt-watch] 全部行 > 0" 是一条**结构性必红**的判据：监听越健康它越红，
#     把它当"真的跑过"的证据，等于选错了账本（同族第二条假绿＝拿日志行数判副作用真发生）。
#   能证明"这一轮真的拨过上游并且拨通了"的读面只有一个：扫描成功才会写的那一行链游标
#   `system_config.usdt_cursor_<链>`——它由 `SetConfig` 每次写入把 `updated_at` 刷成当下时刻，
#   而 `usdtScanChain` 只有在 `FetchDeposits` 成功且链头读数 >0 时才走到这一句。
#   ⚠️ 链名一律从库里 `usdt_chains` 现读再拼键（禁止写死 trc20：运营加一条链这条腿就空转）。
# 腿①／3：主站状态词（进程态、重启即清 ⇒ 它说的就是"这一代件"；演示单元按运营意图是 disabled，只作读数）
WORD=$(curl -s --max-time 8 http://127.0.0.1:8787/api/health | sed -n 's/.*"usdt_watch":"\([a-z_]*\)".*/\1/p' || true)
[ -n "$WORD" ] || WORD="<取不到>"
echo "  主站 usdt_watch 状态词=$WORD（ok＝本进程至少跑完一轮且清单内全链无失败／unknown＝压根没跑过一轮／disabled＝开关关着）"
if [ "$WORD" = "ok" ]; then
  ok "B 腿①：主站监听状态词＝ok"
else
  bad "B 腿①：主站 usdt_watch=$WORD 不是 ok——unknown 是'还没探过'，别跟'探过且没问题'混读"
fi
for p in 8787 8789; do
  echo "  port=$p $(curl -s --max-time 8 http://127.0.0.1:$p/api/health | grep -o '"usdt_watch":"[a-z]*"' || echo '<无该字段>') $(curl -s --max-time 8 http://127.0.0.1:$p/api/health | grep -o '"dispatch":"[a-z]*"' || echo '')"
done
# 窗口锚点＝translator 本次启动时刻；字典序比较必须用 ISO 前缀；awk 偏移必须 i+8
#（"time":" 长 8 字符，写成 i+7 会把引号取进来⇒恒不等⇒计数恒 0＝假绿）
LIM=$(unit_start_iso translator)
if [ -z "$LIM" ]; then
  bad "㊾ 时间窗锚点取不到（translator MainPID 读不到）——此时**不许**把窗口判据当通过，先看 A 段"
else
# 腿②／3：失败行数（负向判据，期望 0；同段的"全部行"降级成**只作读数**，理由见上面那段）
  FAILS=$(awk -v lim="$LIM" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (t>=lim && index($0,"[usdt-watch]") && index($0,"扫描失败")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || true)
  SCANS=$(awk -v lim="$LIM" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (t>=lim && index($0,"[usdt-watch]")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || true)
  : "${FAILS:=<读数失败>}" "${SCANS:=<读数失败>}"
  echo "  自 $LIM（本单元启动）以来：[usdt-watch] 扫描失败行=$FAILS（期望 **0**）  [usdt-watch] 全部行=$SCANS（**只作读数不作判据**：成功轮不带这个前缀，健康态它恒 0）"
  if [ "$FAILS" = "0" ]; then
    ok "B 腿②：本件启动以来零失败行"
  else
    bad "B 腿②：有 $FAILS 行扫描失败（⑮／㊾ 那一族回潮，去看下面那两条样本）"
  fi

# 腿③／3：成功轮次的库侧证据——清单里每条链都必须有一行「本件启动之后写过的游标」
  CHAIN_LIST=$(sudo -u postgres psql -d langcross -Atc "select value from system_config where key='usdt_chains';" 2>/dev/null || true)
  CUR_ROWS=$(sudo -u postgres psql -d langcross -Atc "select key||'@'||substr(updated_at,1,19) from system_config where key like 'usdt_cursor_%' order by key;" 2>/dev/null || true)
  NOW_E=$(date +%s)
  LIM_E=$(date -d "$LIM" +%s 2>/dev/null || true)
  if [ -z "$CHAIN_LIST" ]; then
    bad "B 腿③：库里 usdt_chains 现值读不到 ⇒ 不知道该核哪几条游标（这一格判『未验证』，不等于通过）"
  elif [ -z "$LIM_E" ]; then
    bad "B 腿③：启动锚点转不成时刻（LIM=$LIM）⇒ 游标新鲜度无从比较（同样判『未验证』）"
  else
    DIAL_BAD=0
    DIAL_DETAIL=""
    for ch in $(printf '%s' "$CHAIN_LIST" | tr ',' ' '); do
      ch=$(printf '%s' "$ch" | tr -d ' \r')
      [ -n "$ch" ] || continue
      TS=$(printf '%s\n' "$CUR_ROWS" | sed -n "s/^usdt_cursor_$ch@//p")
      if [ -z "$TS" ]; then
        bad "B 腿③：链 $ch 在库里**没有游标行** ⇒ 这台从没成功拨通过上游（旧件那代写的行也会被下面那条『早于启动』抓到，别当没看见）"
        DIAL_BAD=1
        continue
      fi
      TS_E=$(date -d "$TS" +%s 2>/dev/null || true)
      if [ -z "$TS_E" ]; then
        bad "B 腿③：链 $ch 游标时刻解析不了（原值 $TS）⇒ 判未验证"
        DIAL_BAD=1
        continue
      fi
      DIAL_DETAIL="$DIAL_DETAIL $ch=$((${NOW_E} - TS_E))s前"
      if [ "$TS_E" -lt "$LIM_E" ]; then
        bad "B 腿③：链 $ch 最后一次成功扫描写在 $TS，**早于本件启动** $LIM ⇒ 这一代件还没拨通过上游（别拿旧件的游标当新件的健康）"
        DIAL_BAD=1
      fi
    done
    if [ "$DIAL_BAD" = "0" ]; then
      ok "B 腿③：清单内每条链都有『本件启动之后』的游标写入（距今${DIAL_DETAIL}）＝监听真的拨过上游并且拨通了"
    fi
  fi
fi
echo "  最近 2 条 usdt-watch 行（遮 URL 与 hex，只作定位不作判据）："
grep 'usdt-watch' "$LOGF" 2>/dev/null | tail -2 | sed -E 's#https?://[^ "]*#<url>#g; s/[0-9a-fA-F]{16,}/<hex>/g' | cut -c1-190 | sed 's/^/    /'

echo
echo "----- C 告警归宿：按 kind 分组看 open=20 的构成＋usdt_watch_dead 有没有随健康轮次收敛-----"
echo "  未关闭告警按 kind："
sudo -u postgres psql -d langcross -Atc "select '    '||kind||' | '||count(*)||' | 最近 '||max(created_at) from alerts where status<>'resolved' group by kind order by count(*) desc, kind;" 2>/dev/null || echo "    <主库读档失败>"
echo "  usdt_watch_dead 全量归宿（open 应为 0；有 resolved 行才证明恢复腿真跑过）："
sudo -u postgres psql -d langcross -Atc "select '    status='||status||' 行数='||count(*)||' 最近创建 '||max(created_at)||' 最近收敛 '||COALESCE(max(resolved_at),'-') from alerts where kind='usdt_watch_dead' group by status order by status;" 2>/dev/null || echo "    <告警查不到>"
echo "  监听开关三行（运营意图，与状态词不矛盾）："
sudo -u postgres psql -d langcross -Atc "select '    '||key||'='||value from system_config where key in ('usdt_enabled','usdt_auto_settle','usdt_chains') order by key;" 2>/dev/null || echo "    <读不到>"

# ★ (53) 那条补腿的**唯一**现网证据在日志里，不在 /health：状态词只说"现在好不好"，
#   而这一格要证的是"重启之后有没有真的回头把遗留告警关掉、并且是以哪一档理由关的"。
#   三档 why（in_process_recovery／post_restart_reconcile／chain_unlisted）只有日志出栈，
#   数日志次数按 §一·8 那条只能抓"压根不走到这一行"，所以**上面那行 status 读数才是判据**，
#   这里两行合起来才是完整一条链：库里的 open 归 0 ＋ 日志里出现对应那一档。
echo "  恢复腿日志（窗口同上，只数不贴正文）："
REC=$(awk -v lim="${LIM:-}" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (lim!="" && t<lim) next; if (index($0,"USDT 到账监听已恢复")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || true)
UNLISTED=$(awk -v lim="${LIM:-}" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (lim!="" && t<lim) next; if (index($0,"因链清单收窄而收敛")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || true)
: "${REC:=<读数失败>}" "${UNLISTED:=<读数失败>}"
WHY=$(grep -o '"why":"[a-z_]*"' "$LOGF" 2>/dev/null | tail -1 || true)
echo "    恢复行=$REC（本单元启动以来，期望 **≥1**＝遗留 open 行被这一腿收敛掉）" \
  "清单收窄行=$UNLISTED（**只作读数不作判据**：现网没收窄过就不该有；有它就必须去核 usdt_chains 现值）" \
  "最后一档=${WHY:-<日志里没有 why 字段>}"
if [ "${REC:-0}" != "0" ] && [ "${REC:-}" != "<读数失败>" ]; then
  ok "(53) 补腿现网坐实：恢复行存在且带档名（$WHY）"
else
  bad "(53) 补腿没跑出恢复行（rec=$REC）——两种形态别混：①监听被闸关着/一条都没探过（看 B 段腿①状态词与腿③游标）；②库里那条 open 本来就不归这一腿管（看上面 status 行）。open 还在＝这一格没闭环，别记成已修"
fi
OPENW=$(sudo -u postgres psql -d langcross -Atc "select count(*) from alerts where kind='usdt_watch_dead' and status<>'resolved';" 2>/dev/null || echo "")
if [ -n "$OPENW" ]; then
  [ "$OPENW" = "0" ] && ok "usdt_watch_dead 遗留 open=0（库侧对账，与日志那行互证）" \
    || bad "usdt_watch_dead 仍有 open=$OPENW 行 ⇒ 恢复腿没领到资格（全链无失败那一档没成立），别把'件换了'当'告警关了'"
else
  echo "    <库侧读不到 open 数——这一格判『未验证』，不等于通过>"
fi

echo
echo "----- D ㊶ 观测腿接线：/metrics 的 purity 序列（只判 HELP/TYPE 在位，不判计数）-----"
MT=$(grep -o '^METRICS_TOKEN=.*' /etc/translator/secrets.env 2>/dev/null | head -1 | cut -d= -f2-)
MT_LEN=${#MT}
echo "  METRICS_TOKEN 长度=$MT_LEN（只出长度，不出值）"
if [ "$MT_LEN" -gt 0 ]; then
  BODY=$(curl -s -m 10 http://127.0.0.1:8787/metrics -H "Authorization: Bearer $MT")
  HL=$(printf '%s' "$BODY" | grep -cE '^# (HELP|TYPE) translator_translation_purity_total' || true)
  SER=$(printf '%s' "$BODY" | grep -c '^translator_translation_purity_total' || true)
  echo "  HELP/TYPE 行=$HL（期望 ≥2＝计数器注册上了）  样本行=$SER（**现网无退化样本时 0 属正常**，不为判据）"
  [ "${HL:-0}" -ge 2 ] && ok "purity 序列已注册到 /metrics" || bad "purity HELP/TYPE 行=$HL（<2＝计量没接线，不是'没发生退化'）"
  printf '%s' "$BODY" | grep -E '^# (HELP|TYPE) translator_translation_purity_total|^translator_translation_purity_total' | head -5 | sed 's/^/    /'
else
  echo "  没取到 token ⇒ 本段属『读不到』，**不等于**『没接线』（两种形态别混，见 AGENTS §一·3 那条读数口径）"
fi

echo
echo "----- E (52)＋代际等值：挂件 /health 的 anonym_cleanup 四档＋canned 段-----"
curl -s -m 10 http://127.0.0.1:8790/health > /tmp/_pd_health || echo "  <挂件 /health 拨不通>"
python3 - <<'PY'
import json
try:
    d = json.load(open('/tmp/_pd_health'))
except Exception as e:
    print('  SKIP=1 /health 解析失败', type(e).__name__); raise SystemExit(0)
an = d.get('anonym_cleanup')
print('  anonym_cleanup=', json.dumps(an, ensure_ascii=False) if an is not None else '<无该段（旧件形态⇒(52) 没上线）>')
cd = d.get('canned') or {}
print('  canned.status=', cd.get('status'), 'fail_total=', cd.get('fail_total'), 'prompt_rev=', cd.get('prompt_rev'), 'backoff_active=', cd.get('backoff_active'))
PY
rm -f /tmp/_pd_health

echo
echo "----- F #29 有脚本无调度：派发探针闹钟在不在位（装了才算补上）-----"
# ★ 这一段现在**带判据**了（10-10 首跑时只出读数，于是"闹钟没装"这件事要靠人看输出看出来）：
#   #29 的缺陷本体就是"脚本在、文档在、运行面是零"，所以这里三件事必须逐条判红：
#   ① timer enabled 且 active（只 enable 不 --now＝下一个日历点前不装填，读数面看着像装好了）；
#   ② 两份脚本在位且可执行（ExecStart 指的就是它们）；
#   ③ 样张在位且够档（缺它探针每天判红，安装器按设计就不该 enable 闹钟）。
PT_EN=$(sysd is-enabled translator-dispatch-probe.timer)
PT_AC=$(sysd is-active translator-dispatch-probe.timer)
echo "  probe.timer enabled=$PT_EN active=$PT_AC"
if [ "$PT_EN" = enabled ] && [ "$PT_AC" = active ]; then
  ok "#29 闹钟已装配并已装填（enabled/active）"
else
  bad "#29 闹钟没装上（enabled=$PT_EN active=$PT_AC）——『有脚本无调度』这一格仍未闭，别把 scripts/ 里那份文件当成已生效"
fi
echo "  expiry.timer enabled=$(sysd is-enabled translator-dispatch-expiry.timer) active=$(sysd is-active translator-dispatch-expiry.timer)"
systemctl list-timers --all --no-pager 2>/dev/null | grep -E 'dispatch' | head -3 | sed 's/^/    /'
for f in /opt/translator/bin/dispatch_preflight.sh /opt/translator/bin/dispatch_probe_daily.sh; do
  if [ -x "$f" ]; then
    echo "  $f 在位可执行 mtime=$(stat -c %y "$f" | cut -c1-16)"
  else
    bad "$f $( [ -f "$f" ] && echo '在位但不可执行' || echo '<不在位>' ) ⇒ 闹钟 ExecStart 直接跑不起来（#29 那一条没补上）"
  fi
done
ls -1d /opt/translator/data/_dispatch_probe 2>/dev/null | sed 's/^/  工作目录=/' || echo "  工作目录 <不存在>"
stat -c '  属主=%U:%G 权限=%a' /opt/translator/data/_dispatch_probe 2>/dev/null || true
# ★ 探针本体与样张的**只读**核验（本波没换 fpdprobe，所以这里只证明"闹钟拨得动"这两件事）：
echo "  fpdprobe size=$(stat -c %s /opt/translator/bin/fpdprobe 2>/dev/null || echo '<无>') 协议串 fpdexec=$(grep -a -c -F -- fpdexec /opt/translator/bin/fpdprobe 2>/dev/null || echo '<读不到>')（期望 ≥1＝它就是补丁五那一枚）"
[ -x /opt/translator/bin/fpdprobe ] && ok "fpdprobe 可执行" || bad "fpdprobe 不在位或不可执行 ⇒ 闹钟每天会记 reason=probe_binary_missing"
if [ -s /opt/translator/data/_dispatch_probe/probe.pdf ]; then
  psz=$(wc -c </opt/translator/data/_dispatch_probe/probe.pdf | tr -d ' ')
  ppg=$(/opt/translator/.venv/bin/python3 -c 'import sys,pymupdf;d=pymupdf.open(sys.argv[1]);print(d.page_count);d.close()' /opt/translator/data/_dispatch_probe/probe.pdf 2>/dev/null || echo '')
  [ -n "$ppg" ] || ppg='<页数读不到>'
  echo "  样张 size=${psz}B 页数=${ppg}"
  # 够档判据现读现推（env > 代码默认 20MiB/30 页），别在这里写死第二把尺子
  min_mb=$(grep -E '^\s*(export\s+)?FILEPROC_DISPATCH_MIN_MB=' /etc/translator/secrets.env 2>/dev/null | tail -1 | cut -d= -f2- | tr -d "\"' " || true)
  min_pg=$(grep -E '^\s*(export\s+)?FILEPROC_DISPATCH_MIN_PAGES=' /etc/translator/secrets.env 2>/dev/null | tail -1 | cut -d= -f2- | tr -d "\"' " || true)
  : "${min_mb:=20}"; : "${min_pg:=30}"
  # ★ 三处非数字都要先钉死再比大小：本脚本跑在 `set -u` 下，且 `[ 文案 -ge 30 ]` 会报错被 if 吞掉
  #   ⇒ 不清洗就是"读不到＝够档/读不到＝不够档"的随机档（同 §一·12「就绪判据不许吃零值」同族）。
  case "$min_mb" in *[!0-9]* | "") min_mb=20 ;; esac
  case "$min_pg" in *[!0-9]* | "") min_pg=30 ;; esac
  pp_ok=1
  case "$ppg" in *[!0-9]* | "") pp_ok=0 ;; esac
  if [ "$pp_ok" = 1 ] && [ "$psz" -ge $((min_mb * 1024 * 1024)) ] && [ "$ppg" -ge "$min_pg" ]; then
    ok "样张够档（${psz}B／${ppg} 页 ≥ ${min_mb}MiB ∧ ${min_pg}页，档位现读自 secrets.env／代码默认）"
  else
    bad "样张不合档或页数读不出（现读 ${psz}B／${ppg} 页 对 档 ${min_mb}MiB／${min_pg}页）⇒ 探针会判 reason=no_sample 或派发资格不成立，闹钟每天送一条红"
  fi
else
  bad "样张不在位（/opt/translator/data/_dispatch_probe/probe.pdf）⇒ 探针 reason=no_sample；安装器在这种形态下**不会** enable 闹钟"
fi

echo
echo "----- G 前端未换源的字节级对账（本机 dist ＝ 两站磁盘 ＝ 公网抓回）-----"
for w in /opt/translator/web /opt/translator-demo/web; do
  en=$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$w/index.html" 2>/dev/null | head -1)
  as=$(sha256sum "$w/$en" 2>/dev/null | cut -d' ' -f1)
  sz=$(wc -c <"$w/$en" 2>/dev/null | tr -d ' ')
  if [ "$en" = "$ENTRY" ] && [ "$as" = "$WANT_SHA" ] && [ "$sz" = "$WANT_SIZE" ]; then
    ok "$w 入口与 asset 逐字节等于本机 dist（$en ${sz} B ${as:0:16}…）"
  else
    bad "$w entry=$en sha=${as:0:16} size=$sz —— 与本机 dist（$ENTRY ${WANT_SIZE} B ${WANT_SHA:0:16}）不等 ⇒ 该站前端落后或需换源"
  fi
done
for u in https://langcross.lexicorn.cn https://rox-test.lexicorn.cn; do
  r=$(curl -s -o /tmp/_pd_asset -w '%{http_code}|%{content_type}|%{size_download}' --max-time 30 "$u/$ENTRY" || echo CURL_FAIL)
  as=$(sha256sum /tmp/_pd_asset 2>/dev/null | cut -d' ' -f1)
  # ★ content_type 本身含空格（text/html; charset=utf-8），所以判据按 | 分隔取字段，不拿 %{http_code} 后面的裸词序
  ct=$(echo "$r" | awk -F'|' '{print $2}'); code=$(echo "$r" | awk -F'|' '{print $1}')
  if [ "$as" = "$WANT_SHA" ]; then ok "$u/$ENTRY 公网抓回 sha 与本机一致（code=$code ct=$ct）"; else bad "$u/$ENTRY 公网 sha=${as:0:16} ≠ 本机（code=$code ct=$ct $r）⇒ 公网那份不是这个构建（HTML 兜底/CDN 缓存/路径写错）"; fi
  rm -f /tmp/_pd_asset
done

echo
echo "----- H 磁盘与公开面负向（dotfile／归档族／首页品牌直出）-----"
for w in /opt/translator/web /opt/translator-demo/web; do
  d=$(find "$w" \( -name .DS_Store -o -name '._*' \) 2>/dev/null | wc -l)
  [ "$d" = "0" ] && ok "$w dotfile=0" || bad "$w dotfile=$d（必须 0）"
  echo "  $w 下划线归档=$(ls -1d "${w}"_old.* 2>/dev/null | grep -c . || true) 份 点族=$(ls -1d "$w".old.* 2>/dev/null | grep -c . || true) 入口大小=$(wc -c <"$w/index.html" 2>/dev/null | tr -d ' ')"
done
for u in https://langcross.lexicorn.cn https://rox-test.lexicorn.cn; do
  r=$(curl -s -o /tmp/_pd_ds -w '%{http_code}|%{content_type}|%{size_download}' --max-time 25 "$u/.DS_Store" || echo CURL_FAIL)
  home=$(curl -s -o /dev/null -w '%{size_download}' --max-time 25 "$u/" || echo 0)
  ct=$(echo "$r" | awk -F'|' '{print $2}'); sz=$(echo "$r" | awk -F'|' '{print $3}')
  if [ "$ct" = "text/html; charset=utf-8" ] && [ "$sz" = "$home" ]; then ok "$u/.DS_Store 是 SPA 兜底（字节=$sz＝首页 $home）"; else bad "$u/.DS_Store =$r 首页=$home（磁盘上还有真文件）"; fi
  rm -f /tmp/_pd_ds
  b=$(curl -s --max-time 25 "$u/" | grep -c '__BRANDING__' || true)
  echo "  $u 首页 branding 命中=$b 字节=$(curl -s -o /dev/null -w '%{size_download}' --max-time 25 "$u/")（demo 域走通配块打到 8787，别拿它读演示单元）"
done

echo
echo "----- I 日志水位与错误行（只数不贴正文；这两个 unit 不落 journal）-----"
echo "  $LOGF size=$(stat -c %s "$LOGF" 2>/dev/null) panic=$(grep -c panic "$LOGF" 2>/dev/null || true) ERROR=$(grep -c '"level":"ERROR"' "$LOGF" 2>/dev/null || true)"
# 同 B 段口径：按「本件启动以来」数，不按墙上 10 分钟——整个文件的 ERROR 计数里混着旧件的历史账，
# 只想看"这二进制有没有在报错"就必须用启动锚点截断。
ERRW=$(awk -v lim="$LIM" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (lim!="" && t<lim) next; if (index($0,"\"level\":\"ERROR\"")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || true)
: "${ERRW:=<读数失败>}"
echo "  ERROR 行（窗口起点=${LIM:-未取到锚点，此时为**整份文件**计数}）=$ERRW"
echo "  $ALOG size=$(stat -c %s "$ALOG" 2>/dev/null) WARN=$(grep -c '"level":"WARN"' "$ALOG" 2>/dev/null || true) ERROR=$(grep -c '"level":"ERROR"' "$ALOG" 2>/dev/null || true)"
echo "  挂件清理归宿（sessions_base 壳行应归 0，#27／㊿ 的另一半）："
sqlite3 /opt/ai-assist/data/assist.db "select '    sessions_base='||count(*) from sessions_base;" 2>/dev/null || echo "    <读不到>"
sqlite3 /opt/ai-assist/data/assist.db "select '    messages_base='||COALESCE(count(*),0) from messages_base;" 2>/dev/null || echo "    <读不到>"
sqlite3 /opt/ai-assist/data/assist.db "select '    壳行(msg_count>0 但已无消息)='||COALESCE(count(*),0) from sessions_base where msg_count>0 and id not in (select session_id from messages_base);" 2>/dev/null || echo "    <读不到>"
for t in sessions messages; do
  n=$(sqlite3 /opt/ai-assist/data/assist.db "select count(*) from $t;" 2>/dev/null || echo '<无此表>')
  echo "    旧表 $t=$n（应仍是 <无此表>，回潮＝迁移判据又跑了一次）"
done

echo
echo "----- J 待核实的现网写面（**只数不写**，说明为什么 auto_settle_live 这条没有生产读数）-----"
echo "  收银台那句承诺的取数面只有两个：/api/pay/create（下单＝现网写操作）与 /api/pay/status（要订单归属方登录）。"
echo "  它与上面 B 段的 usdt_watch **同一个函数**（usdtWatchAllowsAutoPromise ⇒ word()==ok），所以 B 的 ok 就是它的等价读数；"
echo "  现网未拨一次的原因＝不在生产造单。库里 pending usdt 单数（只数，确认没留测试残骸）："
sudo -u postgres psql -d langcross -Atc "select '    pending_usdt_orders='||count(*) from usdt_orders o join orders r on r.id=o.order_id and r.status='pending';" 2>/dev/null || echo "    <读不到>"

echo
if [ "$RC" = "0" ]; then echo "POSTDEPLOY_ALL_OK=1"; else echo "POSTDEPLOY_FAIL=1（上面有 bad 行）"; fi
echo POSTDEPLOY_EXIT=$RC
exit "$RC"
REMOTE_EOF
SSH_RC=$?
echo "POSTDEPLOY_LOCAL_EXIT=$SSH_RC"
# ★ 这一行是 10-10 复跑时才发现的**本脚本自己的**一条「有判据没接线」：
#   旧形态远端段跑完只 `echo POSTDEPLOY_EXIT=$RC` 就结束了（最后一句 echo 恒退 0），
#   本机这一层又把 `$?` 打进一行读数就到此为止 ⇒ **同一份输出里写着 POSTDEPLOY_FAIL=1，
#   脚本退出码仍然是 0**。表现完全就是本波点名的那一族：证据链看着齐（判据在、红也出了），
#   运行面是零——因为下一个把它挂进定时器或 CI 的人，第一个读的就是退出码。
#   现在把远端 RC 一路带到本机：1＝有 FAIL 行；2＝硬前置不满足；
#   255＝ssh 链路压根没通（这**不是**现网健康，必须照样红）。
if [ "$SSH_RC" = "0" ]; then
  echo "本机结论：复查全绿（现网接线一条不缺）"
else
  echo "本机结论：复查**未通过**，退出码 $SSH_RC（255＝链路没通，别读成『现网没问题』）"
fi
exit "$SSH_RC"
