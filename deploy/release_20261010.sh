#!/usr/bin/env bash
# release_20261010.sh — 〇-AR 第 9 波换件（**只换主站＋演示站两台 translator-server，不动 fpdprobe、不动前端、不动挂件**）
#
# 本批动了什么（按 AGENTS §一·5「五类渲染面」点名，别默认 dist 绿＝全站绿）：
#   · `internal/api/pay_usdt_watch.go`（(53) 重启后遗留告警收敛腿＋(54) chain_unlisted 档与逐链窗口）
#     ＋`internal/store/usdt.go`（(54) 匹配查询第三返回值 err、Scan 失败直判歧义、ListUnmatchedDepositsForChain）
#     ⇒ 两处都落在 **translator-server**，主站 `/opt/translator/bin` 与演示 `/opt/translator-demo/bin` **都要换**
#       （收款监听只在主单元跑，但两台必须同版——旧版差一台就是"排障时那台的行为不在预期里"）；
#   · `cmd/fpdprobe` **本波不换件**（★ 这一条原先写成「现网那份早于〇-AF 补丁五」，实测三条读数证明**说反了**；
#     按错理由换件＝把闹钟每天要拨的那枚件换成一份只是构建工件不同的件，行为零差异却留下一笔「改过现役探针」的假账）：
#       ① `git log -- backend-go/cmd/fpdprobe/main.go` 只有一个提交＝93e1bae（补丁五，10-01 03:18:36 +0800，
#          且是**新增** 283 行＝探针出生就带补丁五），此后到现在**零改动**（93e1bae..HEAD 计数＝0）；
#       ② 现网那份 mtime＝10-01 03:19:55（在那次提交之后 79 秒落位），件内特征串实读 `fpdexec`=2、`页数`=1
#          ⇒ 现役件**正是**补丁五那一枚，不是它的前代；
#       ③ 本机重编件与现网件**体积逐字节相等**（6,262,946）而 sha 不同——它只 import `internal/fileproc`
#          （再往下只有 config/observability），本波改的 `internal/api`＋`internal/store` **一行都不在这枚件里**，
#          而 fileproc 自 10-01 之后只多出两个**测试文件**（非测试源码零改动）
#          ⇒ 那点差异只能是 Go 构建工件（build id / 版本戳），不是行为差异。
#     ⇒ 本波只对现役探针做**只读核验**（P2-B3 起得来＋件内协议串在）；真要换它请另起一批并写清真理由。
#   · 挂件（translator-assist）**本波零改动**：`git diff 63f3601..HEAD -- backend-go/internal/assist backend-go/cmd/assist-server`
#     实跑为**空**，且现势件正是 10-10 00:38:45 CST 那次换上去的第 8 波版（`deleted_sessions` 命中 1）⇒ 不换、不重启。
#   · 前端／扩展／SDK 一字未动 ⇒ 不换源、不重打包（漂移闸本机实跑：✅1.2.2／✅1.0.2）。
#
# ★ 本批**没有抬 `cannedPromptRev`**（仍 `0AR-1`）：改的是收款侧监听与匹配查询，不在挂件 canned 射程
#   ⇒ 不存在"换件没修"的缓存挡路，换二进制即生效。
#
# 两道中止线（都在动手之前，不是事后补救）：
#   ① 本机新件里**特征串缺失** ⇒ 不上传（表现是"编译的是旧工作树"，历史上真踩过一次）；
#   ② 上传件在服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，此时磁盘上还没落位任何新件。
# 落位一律 `cp 到新名` ＋ 同目录 `mv -f` rename（`cp` 直接覆盖运行中二进制会 ETXTBSY）。
#
# ★ 换件之后本波**必须现读的三条判据**（写进 deploy/postdeploy_20261010.sh 的 C 段，这里只交代为什么）：
#   ① 库里 `alerts` 的 `usdt_watch_dead` **open 从 1 归 0**（基线 2026-10-10 02:18 CST 实测：id=90、
#      created_at=2026-10-06T12:17:13+08:00、status=open，而同一时刻 `/api/health` 的 `usdt_watch` 已是 `ok`
#      —— 这正是 (53) 的现网本体：事实好了、告警不认识这次"好了"）；
#   ② 日志里出现一行 `USDT 到账监听已恢复` 且带 `"why":"post_restart_reconcile"`（这一档只有日志出栈，
#      `/health` 不出 why ⇒ 只数状态词证不了那条腿跑过）；
#   ③ 匹配查询那条 err 腿没有现网故障可复现（现网查询不失败），所以它只有单测与反证，**没有现网读数**
#      ⇒ 台账里要按"已上线·现网无读数"记，别写成"已验证"。
#
# 发版判据按用户既定口径：**只验接线，不跑线上像素校验**。
# 用法：bash deploy/release_20261010.sh            # 干跑（编译＋特征串，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261010.sh    # 真换
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261010"

SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 $SRC）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 交叉编译（HEAD=$(git -C "$REPO" rev-parse --short HEAD)，工作树含本波未提交改动=$([ -n "$(git -C "$REPO" status --porcelain -- backend-go)" ] && echo 是 || echo 否)）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) || { echo "ABORT=1 server 编译失败"; exit 2; }
for f in "$SERVER_BIN"; do
  echo "$f size=$(wc -c <"$f" | tr -d ' ') sha=$(sha_local "$f")"
done

echo "===== P0-B 特征串（中止线①：件里没这几条腿＝本批等于没上线）====="
# ① server：(53) 那条腿的**档名**与 (54) 的**文案档**——两条都是"只有日志出栈"的契约，缺一条就是那条腿没进件
W_RECONCILE=$(grep -ac "post_restart_reconcile" "$SERVER_BIN" || true)
W_UNLISTED=$(grep -ac "chain_unlisted" "$SERVER_BIN" || true)
W_RESOLVEMSG=$(grep -ac "因链清单收窄而收敛" "$SERVER_BIN" || true)
# ② server：(54) 的匹配查询诚实化（Scan 失败按歧义出声那句；旧件里零命中）
W_SCANAMBIG=$(grep -ac "USDT 匹配行解析失败" "$SERVER_BIN" || true)
# ②b server：(54) 最危险那一条腿——块高缺失顶穿确认闸（缺块高的转账旧形态把"未确认"算成已确认，
#     当场置 paid 且全程零告警；新件必须带这条出声，缺它＝那条 continue 没进件）
W_BLOCKNO=$(grep -ac "USDT 入账缺块高读数" "$SERVER_BIN" || true)
# ③ 第 8 波那两条**必须还在**（本波是叠加，不是替换；掉了就是构建树退回旧提交）
CARRY_HEAD=$(grep -ac "/wallet/getnowblock" "$SERVER_BIN" || true)
CARRY_PURITY=$(grep -ac "tail_stripped" "$SERVER_BIN" || true)
echo "server: post_restart_reconcile=$W_RECONCILE chain_unlisted=$W_UNLISTED 因链清单收窄=$W_RESOLVEMSG 匹配行解析失败=$W_SCANAMBIG 缺块高=$W_BLOCKNO ； carry: getnowblock=$CARRY_HEAD tail_stripped=$CARRY_PURITY"
for pair in "$W_RECONCILE:(53) 重启后收敛腿的档名" "$W_UNLISTED:(54) 撤链档名" "$W_RESOLVEMSG:(54) 撤链文案（不许写成已恢复）" "$W_SCANAMBIG:(54) 匹配行解析失败按歧义" "$W_BLOCKNO:(54) 缺块高不评确认数" "$CARRY_HEAD:第 8 波㊾链头默认路径" "$CARRY_PURITY:第 8 波㊶尾段剥离档名"; do
  n="${pair%%:*}"; label="${pair#*:}"
  [ "$n" != "0" ] || { echo "ABORT=2 缺特征串 ⇒ $label（编译的不是含本批改动的树）"; exit 3; }
done
SERVER_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋特征串）；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
  exit 0
fi

echo "===== P1 上传到 /tmp（此时仍未落位）====="
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=3 scp server 失败"; exit 4; }

echo "===== P2 服务器侧校验＋落位＋重启＋接线读数 ====="
ssh -p "$PORT" "root@$HOST" bash -s -- "$SERVER_SHA" "$TAG" <<'REMOTE_EOF'
set -uo pipefail
EXPECT_TS="$1"; TAG="$2"
TS=$(date +%Y%m%d_%H%M%S)
echo "远端时刻=$(date '+%F %T %Z') TS=$TS"

echo "----- P2 校验上传件（中止线②：不等即停，此时一件未落位）-----"
a=$(sha256sum /tmp/ts-new-$TAG | cut -d' ' -f1)
echo "server_sha=$a"
[ "$a" = "$EXPECT_TS" ] || { echo "ABORT=4 后端上传件 sha 不等，未落位即停"; exit 4; }

echo "----- P2-B 换件前基线（三条现网判据各自的起点，不贴任何取值内容）-----"
LOGF=/opt/translator/log/translator.log
echo "换件前 /api/health usdt_watch=$(curl -s -m 10 http://127.0.0.1:8787/api/health | grep -o '"usdt_watch":"[^"]*"' || echo '<无该字段>')"
echo "换件前 usdt_watch_dead open 行数=$(sudo -u postgres psql -d langcross -Atc "select count(*) from alerts where kind=\$\$usdt_watch_dead\$\$ and status<>\$\$resolved\$\$;" 2>/dev/null || echo '<读不到>')"
echo "换件前 alerts 未关闭按 kind=$(sudo -u postgres psql -d langcross -Atc "select kind||'='||count(*) from alerts where status<>\$\$resolved\$\$ group by kind;" 2>/dev/null | tr '\n' ' ' || echo '<读不到>')"
echo "换件前 日志「USDT 到账监听已恢复」累计行数=$(grep -ac 'USDT 到账监听已恢复' "$LOGF" 2>/dev/null || true)"
echo "换件前 日志「扫描失败」近 60 分钟行数=$(awk -v lim="$(date -d '60 minutes ago' '+%Y-%m-%dT%H:%M:%S')" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (t>=lim && index($0,"[usdt-watch]") && index($0,"扫描失败")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || echo '<读数失败>')"

echo "----- P2-B2 特征串负向对照（★ 必须在落位之前读线上旧件）-----"
# 本机那七道正锁只证明"新件里有"，不证明"旧件里没有"。若旧件也命中，中止线①就是恒绿的空转判据。
for pat in post_restart_reconcile chain_unlisted 因链清单收窄而收敛 "USDT 匹配行解析失败" "USDT 入账缺块高读数"; do
  n=$(grep -a -c -F -- "$pat" /opt/translator/bin/translator-server 2>/dev/null || true)
  [ -n "$n" ] || n='<文件读不到>'
  echo "旧件命中『$pat』=$n（期望 **0**）"
done
# 正向对照（同一条 grep 得能抓到**旧件本来就有**的东西，否则上面那一串 0 只是"读不到文件"）
echo "同一把尺子的正向对照：旧件命中『/wallet/getnowblock』=$(grep -a -c -F -- '/wallet/getnowblock' /opt/translator/bin/translator-server 2>/dev/null || true)（期望 ≥1＝第 8 波那条腿确实在旧件里）"
echo "旧件 size=$(stat -c %s /opt/translator/bin/translator-server) sha16=$(sha256sum /opt/translator/bin/translator-server | cut -c1-16)"
echo "现役 fpdprobe（本波**不换**，这里只留基线读数）size=$(stat -c %s /opt/translator/bin/fpdprobe 2>/dev/null || echo '<无>') mtime=$(stat -c %y /opt/translator/bin/fpdprobe 2>/dev/null || echo '<无>') 协议串 fpdexec=$(grep -a -c -F -- fpdexec /opt/translator/bin/fpdprobe 2>/dev/null || true)（期望 ≥1＝它就是补丁五那一枚）"

place() { # $1 源 $2 目标目录 $3 目标文件名 $4 模式
  local src="$1" dir="$2" name="$3" mode="$4"
  if [ ! -f "$dir/$name" ]; then echo "FAIL $dir/$name <不存在>"; return 1; fi
  cp -a "$dir/$name" "$dir/$name.bak.$TS" || { echo "FAIL 备份 $dir/$name"; return 1; }
  cp "$src" "$dir/$name.new.$TS" || { echo "FAIL 复制到新名"; return 1; }
  chmod "$mode" "$dir/$name.new.$TS"
  chown root:root "$dir/$name.new.$TS"
  mv -f "$dir/$name.new.$TS" "$dir/$name" || { echo "FAIL rename 落位 $dir/$name"; return 1; }
  echo "PLACED $dir/$name size=$(stat -c %s "$dir/$name") sha16=$(sha256sum "$dir/$name" | cut -c1-16) 备份=$dir/$name.bak.$TS"
}
echo "----- P3 落位（只有两台后端；探针、挂件与前端本波都不动）-----"
place /tmp/ts-new-$TAG /opt/translator/bin      translator-server 755 || exit 5
place /tmp/ts-new-$TAG /opt/translator-demo/bin translator-server 755 || exit 5

echo "----- P4 重启两台后端并等健康（挂件不重启：它本波零改动）-----"
RESTART_SINCE=$(date '+%Y-%m-%d %H:%M:%S')
# ★ 窗口锚点必须与日志写入口径**同形**（日志里是 %Y-%m-%dT%H:%M:%S，第 11 位是 T 不是空格）：
#   拿带空格的本地时刻去做字典序比较，"T"(0x54) 恒大于空格 ⇒ 判据恒真，会把换件**之前**的行也数进来。
RESTART_T=$(date -d "$RESTART_SINCE" '+%Y-%m-%dT%H:%M:%S')
systemctl restart translator translator-demo
for p in 8787 8789; do
  ok=0
  for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
    code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$p/api/health" || true)
    if [ "$code" = "200" ]; then ok=1; break; fi
    sleep 2
  done
  [ "$ok" = 1 ] && echo "OK 端口 $p 健康（第 $i 次）" || { echo "ABORT=5 端口 $p 重启后没起来，请回滚件：$p"; exit 6; }
done
echo "单元读数：translator=$(systemctl is-active translator)/$(systemctl show -p NRestarts --value translator) ；translator-demo=$(systemctl is-active translator-demo)/$(systemctl show -p NRestarts --value translator-demo) ；ai-assist=$(systemctl is-active ai-assist)（本波没碰）"

echo "----- P4-B 探针可跑性（闹钟每天拨的就是它；只验能起、不验真派一单）-----"
/opt/translator/bin/fpdprobe --help >/tmp/_fp_help 2>&1 || true
head -3 /tmp/_fp_help | sed 's/^/    fpdprobe--help: /'
echo "    fpdprobe 件里协议串命中=$(grep -a -c -F -- fpdexec /opt/translator/bin/fpdprobe || true)（期望 ≥1）"

echo "----- P5 落位后件内自检（新件必须带本波那五条，且旧串不复活）-----"
for pat in post_restart_reconcile chain_unlisted 因链清单收窄而收敛 "USDT 匹配行解析失败" "USDT 入账缺块高读数"; do
  echo "    新件命中『$pat』=$(grep -a -c -F -- "$pat" /opt/translator/bin/translator-server 2>/dev/null || true)"
done

echo "----- P6 恢复腿的现网读数（(53) 的收口判据；这一刻起最多两轮 30s 内应出现）-----"
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18; do
  OPENW=$(sudo -u postgres psql -d langcross -Atc "select count(*) from alerts where kind=\$\$usdt_watch_dead\$\$ and status<>\$\$resolved\$\$;" 2>/dev/null || echo "")
  if [ "$OPENW" = "0" ]; then echo "    第 $((i*10)) 秒：open=0 ⇒ 收敛腿已跑"; break; fi
  sleep 10
done
REC=$(awk -v lim="$RESTART_T" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (t>=lim && index($0,"USDT 到账监听已恢复")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || echo '<读数失败>')
echo "    重启以来「USDT 到账监听已恢复」行数=$REC（期望 ≥1）"
echo "    最后一行 why 档=$(grep -o '"why":"[a-z_]*"' "$LOGF" 2>/dev/null | tail -1 || echo '<日志里没有 why>')"
echo "    换件后 open 行数=$(sudo -u postgres psql -d langcross -Atc "select count(*) from alerts where kind=\$\$usdt_watch_dead\$\$ and status<>\$\$resolved\$\$;" 2>/dev/null || echo '<读不到>')（基线=1）"
echo "    换件后 /api/health usdt_watch=$(curl -s -m 10 http://127.0.0.1:8787/api/health | grep -o '"usdt_watch":"[^"]*"' || echo '<无该字段>')"
echo "REMINDER=1 完整接线验收（含告警归宿分档、/metrics 纯度序列、闹钟在位性、清理腿首 tick）跑 bash deploy/postdeploy_20261010.sh"
REMOTE_EOF
rc=$?
echo "P2 远端退码=$rc"
exit "$rc"
