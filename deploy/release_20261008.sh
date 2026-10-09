#!/usr/bin/env bash
# release_20261008.sh — 〇-AR 第 8 波换件（**只换后端两件，不动前端**）
#
# 本批动了什么（按 AGENTS §一·5「五类渲染面」点名，别默认 dist 绿＝全站绿）：
#   · `internal/engine/review_purity.go`（新增，㊶ 的 ② 审校腿纯度闸门＋④ 出栈尾段剥离）
#     ＋`internal/engine/engine.go`（② 两条落点）＋`internal/engine/postprocess.go`（④ 挂出栈咽喉）
#     ＋`internal/api/metrics.go`（`translator_translation_purity_total` 序列）
#     ⇒ 落在 **translator-server**，两台（主站 `/opt/translator/bin`、演示 `/opt/translator-demo/bin`）都要换；
#   · `internal/payment/usdt.go`（㊾：链头改 `POST /wallet/getnowblock`＋四腿解析＋`jsonInt64` 容错）
#     ⇒ 同样落在 **translator-server**（收款监听只在主单元开，演示单元 `disabled`，两件仍要同版）；
#   · `internal/assist/api/server.go`＋`internal/assist/store/store.go`（(52) 清理读数四档＋`updated_at` 探测缓存拆锁）
#     ⇒ 挂件清理只在 **ai-assist** 进程里跑 ⇒ **translator-assist** 必须换；
#   · 前端／扩展／SDK／派发脚本本轮**一字未动** ⇒ 不换源、不重打包（漂移闸已在本波收尾跑过：✅1.2.2／✅1.0.2）。
#
# ★ 本批**没有抬 `cannedPromptRev`**（仍 `0AR-1`）：㊶ 改的是通用译文出栈、不在挂件 canned 射程，
#   ⇒ 不存在"换件没修"的缓存挡路，换二进制即生效。这一条正面证据要读线上件里的 rev 串**仍是** `0AR-1`（等值判据）。
#
# 两道中止线（都在动手之前，不是事后补救）：
#   ① 本机新件里**特征串缺失** ⇒ 不上传（表现是"编译的是旧工作树"，历史上真踩过一次）；
#   ② 上传件在服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，此时磁盘上还没落位任何新件。
# 落位一律 `cp 到新名` ＋ 同目录 `mv -f` rename（`cp` 直接覆盖运行中二进制会 ETXTBSY）。
#
# 发版判据按用户既定口径：**只验接线，不跑线上像素校验**。
# 用法：bash deploy/release_20261008.sh            # 干跑（编译＋特征串，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261008.sh    # 真换
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261008"

SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
ASSIST_BIN="${ASSIST_BIN:-/tmp/translator-assist-linux-$TAG}"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 $SRC）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 交叉编译（HEAD=$(git -C "$REPO" rev-parse --short HEAD)）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) || { echo "ABORT=1 server 编译失败"; exit 2; }
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$ASSIST_BIN" ./cmd/assist-server) || { echo "ABORT=1 assist 编译失败"; exit 2; }
for f in "$SERVER_BIN" "$ASSIST_BIN"; do
  echo "$f size=$(wc -c <"$f" | tr -d ' ') sha=$(sha_local "$f")"
done

echo "===== P0-B 特征串（中止线①：件里没这几条腿＝本批等于没上线）====="
# ① server：㊶ 的两条动作档名（④ 与 ② 各一，成对出现才算两腿都在）
S_TAIL=$(grep -ac "tail_stripped" "$SERVER_BIN" || true)
S_REVBATCH=$(grep -ac "review_batch_rejected" "$SERVER_BIN" || true)
# ② server：㊾ 的真上游形态（默认路径＋新拆出来的方法覆盖口，缺一条就是"半截修法"）
S_HEADPATH=$(grep -ac "/wallet/getnowblock" "$SERVER_BIN" || true)
S_HEADMETHOD=$(grep -ac "USDT_TRON_HEAD_METHOD" "$SERVER_BIN" || true)
# ③ assist：(52) 的会话侧读数键（旧件里零命中）
A_SESS=$(grep -ac "deleted_sessions" "$ASSIST_BIN" || true)
echo "server: tail_stripped=$S_TAIL review_batch_rejected=$S_REVBATCH getnowblock=$S_HEADPATH USDT_TRON_HEAD_METHOD=$S_HEADMETHOD ；assist: deleted_sessions=$A_SESS"
for pair in "$S_TAIL:㊶④ 尾段剥离档名" "$S_REVBATCH:㊶② 批量拒绝档名" "$S_HEADPATH:㊾ 链头默认路径" "$S_HEADMETHOD:㊾ 方法覆盖口" "$A_SESS:(52) 会话侧条数读数"; do
  n="${pair%%:*}"; label="${pair#*:}"
  [ "$n" != "0" ] || { echo "ABORT=2 缺特征串 ⇒ $label（编译的不是含本批改动的树）"; exit 3; }
done
# ④ 代际等值判据：本批**刻意不抬 rev**，所以线上件里必须**仍是** `0AR-1`。
#    这一条是"不许把'没抬'写成'不需要看'"的落点：若哪天件里冒出第二个 rev 字样，说明有人在别处动了 canned 形态。
S_REV=$(grep -ac "0AR-1" "$ASSIST_BIN" || true)
echo "assist: cannedPromptRev 特征串 0AR-1 命中=$S_REV（期望 **≥1**＝本批沿用同一代际）"
[ "$S_REV" != "0" ] || { echo "ABORT=2 挂件件里找不到 0AR-1（构建树与台账口径不符，先查清再换）"; exit 3; }

SERVER_SHA=$(sha_local "$SERVER_BIN")
ASSIST_SHA=$(sha_local "$ASSIST_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋特征串）；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
  exit 0
fi

echo "===== P1 上传到 /tmp（此时仍未落位）====="
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=3 scp server 失败"; exit 4; }
scp -P "$PORT" "$ASSIST_BIN" "root@$HOST:/tmp/tas-new-$TAG" || { echo "ABORT=3 scp assist 失败"; exit 4; }

echo "===== P2 服务器侧校验＋落位＋重启＋接线读数 ====="
ssh -p "$PORT" "root@$HOST" bash -s -- "$SERVER_SHA" "$ASSIST_SHA" "$TAG" <<'REMOTE_EOF'
set -uo pipefail
EXPECT_TS="$1"; EXPECT_TAS="$2"; TAG="$3"
TS=$(date +%Y%m%d_%H%M%S)
echo "远端时刻=$(date '+%F %T %Z') TS=$TS"

echo "----- P2 校验上传件（中止线②：不等即停，此时一件未落位）-----"
a=$(sha256sum /tmp/ts-new-$TAG | cut -d' ' -f1)
b=$(sha256sum /tmp/tas-new-$TAG | cut -d' ' -f1)
echo "server_sha=$a"; echo "assist_sha=$b"
[ "$a" = "$EXPECT_TS" ]  || { echo "ABORT=4 后端上传件 sha 不等，未落位即停"; exit 4; }
[ "$b" = "$EXPECT_TAS" ] || { echo "ABORT=4 挂件上传件 sha 不等，未落位即停"; exit 4; }

echo "----- P2-B 换件前基线（三条现网判据各自的起点，不贴任何取值内容）-----"
LOGF=/opt/translator/log/translator.log
MT=$(grep -o '^METRICS_TOKEN=.*' /etc/translator/secrets.env 2>/dev/null | head -1 | cut -d= -f2-)
MT_LEN=${#MT}
echo "换件前 METRICS_TOKEN 长度=$MT_LEN（只出长度，不出值；取不到则 /metrics 那两条读数属『没 token』而非『没序列』）"
echo "换件前 /api/health usdt_watch=$(curl -s -m 10 http://127.0.0.1:8787/api/health | grep -o '"usdt_watch":"[^"]*"' || echo '<无该字段>')"
echo "换件前 translator.log 近 10 分钟 usdt-watch 失败行数=$(awk -v lim="$(date -d '10 minutes ago' '+%Y-%m-%dT%H:%M:%S')" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (t>=lim && index($0,"[usdt-watch]") && index($0,"扫描失败")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || echo '<读数失败>')"
echo "换件前 /metrics 纯度序列行数=$( [ -n "$MT" ] && curl -s -m 10 http://127.0.0.1:8787/metrics -H "Authorization: Bearer $MT" | grep -c '^translator_translation_purity_total' || echo '<无 token>' )"
echo "换件前 alerts 未关闭分组=$(sudo -u postgres psql -At -c "select status, count(*) from alerts where status<>'resolved' group by status;" langcross 2>/dev/null || echo '<失败>')"
echo "换件前 挂件 /health anonym 段=$(curl -s -m 10 http://127.0.0.1:8790/health | grep -o '"anonym[^}]*' | head -c 200 || echo '<无该段>')"

echo "----- P2-B2 特征串负向对照（★ 必须在落位之前读线上旧件）-----"
# 本机那五道正锁只证明"新件里有"，不证明"旧件里没有"。若旧件也命中，中止线①就是恒绿的空转判据。
for pair in "/opt/translator/bin/translator-server:tail_stripped" "/opt/translator/bin/translator-server:review_batch_rejected" "/opt/translator/bin/translator-server:/wallet/getnowblock" "/opt/translator/bin/translator-server:USDT_TRON_HEAD_METHOD" "/opt/ai-assist/bin/translator-assist:deleted_sessions"; do
  f="${pair%%:*}"; pat="${pair#*:}"
  # `grep -c` **无命中时退 1** ⇒ 必须 `|| true` 吃退出码，空串才判"文件读不到"（否则会把 0 和失败串在一起）。
  n=$(grep -a -c -F -- "$pat" "$f" 2>/dev/null || true)
  [ -n "$n" ] || n='<文件读不到>'
  echo "旧件 $f 命中『$pat』=$n  size=$(stat -c %s "$f" 2>/dev/null || echo '<无>')  sha16=$(sha256sum "$f" 2>/dev/null | cut -c1-16)"
done
# 正向对照（同一条 grep 得能抓到**旧件本来就有**的东西，否则上面那一串 0 只是"读不到文件"）
echo "同一把尺子的正向对照：旧 translator-server 命中『/v1/blocks』=$(grep -a -c -F -- '/v1/blocks' /opt/translator/bin/translator-server 2>/dev/null || true)（期望 ≥1＝旧端点串确实在旧件里）"

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
echo "----- P3 落位三处（两件件、三单元）-----"
place /tmp/ts-new-$TAG  /opt/translator/bin      translator-server 755 || exit 5
place /tmp/ts-new-$TAG  /opt/translator-demo/bin translator-server 755 || exit 5
place /tmp/tas-new-$TAG /opt/ai-assist/bin       translator-assist 755 || exit 5

echo "----- P4 重启并等健康 -----"
RESTART_SINCE=$(date '+%Y-%m-%d %H:%M:%S')
systemctl restart translator translator-demo ai-assist
for p in 8787 8789 8790; do
  ok=0
  for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if [ "$p" = "8790" ]; then
      code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$p/health" 2>/dev/null || true)
    else
      code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$p/api/health" 2>/dev/null || true)
    fi
    [ "$code" = "200" ] && { ok=1; break; }
    sleep 2
  done
  echo "port=$p healthy=$ok"
done
systemctl is-active translator translator-demo ai-assist
for u in translator translator-demo ai-assist; do
  pid=$(systemctl show -p MainPID --value "$u")
  exe=$(readlink "/proc/$pid/exe" 2>/dev/null || true)
  echo "unit=$u active=$(systemctl is-active "$u") pid=$pid NRestarts=$(systemctl show -p NRestarts --value "$u") exe=$exe size=$(stat -c %s "$exe" 2>/dev/null || echo '<无>') sha16=$(sha256sum "$exe" 2>/dev/null | cut -c1-16)"
done

echo "----- P5 启动后 err 级日志（★ 两条腿都要，只留一条就是结构性空转锁）-----"
# `translator`／`ai-assist` 的 StandardOutput 是 `append:` 落文件 ⇒ journal 恒 0 属**结构性空转**（10-08 已实测坐实）。
# 落点必须从 `systemctl cat` 取：`systemctl show -p StandardOutput --value` 在这台 systemd 上只回模式名 `append`。
for u in translator translator-demo ai-assist; do
  n=$(journalctl -u "$u" --since "$RESTART_SINCE" -p err --no-pager 2>/dev/null | grep -v '^-- No entries --$' | grep -c . || true)
  so=$(systemctl cat "$u" 2>/dev/null | grep -a '^StandardOutput=' | tail -1)
  case "$so" in
    StandardOutput=append:*)
      f="${so#StandardOutput=append:}"
      if [ -f "$f" ]; then
        rs=$(printf '%s' "$RESTART_SINCE" | tr ' ' 'T')
        # 偏移必须是 `i+8`（`"time":"` 长 8 字符），写成 i+7 会把前导引号取进来 ⇒ 字典序恒不等 ⇒ 计数恒 0＝假绿。
        jn=$(awk -v ts="$rs" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (t>=ts && index($0,"\"level\":\"ERROR\"")) c++ } END { print c+0 }' "$f" 2>/dev/null || true)
        [ -n "$jn" ] || jn='<读数失败>'
        echo "unit=$u journal_err行=$n（该 unit 不落 journal，只作对照） 文件落点=$f ERROR行(重启后)=$jn"
      else
        echo "unit=$u journal_err行=$n 文件落点=$f <文件不存在⇒读不到，不等于零错误>"
      fi
      ;;
    *)
      echo "unit=$u journal_err行=$n（StandardOutput=$so）"
      ;;
  esac
done

echo "----- P6 等两轮 usdt 监听（30 s/轮）后读那三条现网判据 -----"
# ★ 判据必须"现读现推"：`usdt_watch` 在重启后第一档是 `unknown`（还没探过），拿它比 `ok` 会当场假红。
sleep 40
echo "第 1 轮后 /api/health usdt_watch=$(curl -s -m 10 http://127.0.0.1:8787/api/health | grep -o '"usdt_watch":"[^"]*"' || echo '<无该字段>')"
sleep 40
echo "第 2 轮后 /api/health usdt_watch=$(curl -s -m 10 http://127.0.0.1:8787/api/health | grep -o '"usdt_watch":"[^"]*"' || echo '<无该字段>')"
rs=$(printf '%s' "$RESTART_SINCE" | tr ' ' 'T')
echo "重启后 [usdt-watch] 失败行数=$(awk -v ts="$rs" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (t>=ts && index($0,"[usdt-watch]") && index($0,"扫描失败")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || echo '<读数失败>')（期望 **0**＝㊾ 现网坐实）"
echo "重启后 [usdt-watch] 成功/扫描行数=$(awk -v ts="$rs" '{ i=index($0,"\"time\":\""); if (i==0) next; t=substr($0,i+8,19); if (t>=ts && index($0,"[usdt-watch]")) c++ } END { print c+0 }' "$LOGF" 2>/dev/null || echo '<读数失败>')（>0 才证明这一轮真的探过，别把"没有失败行"读成"没探"）"
echo "alerts 未关闭分组=$(sudo -u postgres psql -At -c "select status, count(*) from alerts where status<>'resolved' group by status;" langcross 2>/dev/null || echo '<失败>')"
# ★ 收银台对客承诺（auto_settle_live）**没有只读的现网读数面**，别再打一个不存在的接口去"取证"。
#   10-10 复核：本行旧写法打的是 /api/payment/methods —— 全仓 grep 该路由**零命中**，
#   curl 拿到的是 spa.go 的 index.html 兜底（状态码还 200），grep 不到那个字段 ⇒ 输出恒空，
#   于是"取不到"会被读成"承诺没生效"＝**假红**（AGENTS.md §一·6 那条「200 不等于拿到了东西」的同形）。
#   这一档的真实形态是**构造期等值**：pay_usdt.go 里
#   AutoSettleLive: s.usdtWatchAllowsAutoPromise()，与 /api/health 的 usdt_watch=="ok" 同一条判据
#   （开关档 AutoSettleOn 与能力档分开出栈，锁＝pay_usdt_watch_test.go 的 TestUsdtCheckoutPromiseFollowsWatch）；
#   前端 usdtPromise.ts 只认字面 true，否则回落"人工核销"文案。
#   ⇒ 发版后读上面那行的 usdt_watch 状态词就够了。出参面只有 /api/pay/create（写）与
#   /api/pay/status（需订单属主登录），两者都不是匿名可读的取证面。
echo "收银台对客承诺=由构造等值于上面的 usdt_watch 状态词（无只读取证面，故本行不发请求）"

echo "----- P7 /metrics 纯度序列（㊶ 的观测腿接线，只数序列名，不做值判据）-----"
if [ -n "$MT" ]; then
  echo "purity 序列行数=$(curl -s -m 10 http://127.0.0.1:8787/metrics -H "Authorization: Bearer $MT" | grep -c '^translator_translation_purity_total' || true)（≥0；**没有真实退化检出时该序列本来就该是空**，取到 HELP/TYPE 行算接线成立）"
  curl -s -m 10 http://127.0.0.1:8787/metrics -H "Authorization: Bearer $MT" | grep -E '^# (HELP|TYPE) translator_translation_purity_total|^translator_translation_purity_total' | head -5
else
  echo "无 METRICS_TOKEN ⇒ 本段跳过（属"取不到"，不是"没接线"）"
fi

echo "----- P8 挂件 /health 的 anonym 读数段（(52) 接线）-----"
curl -s -m 10 http://127.0.0.1:8790/health | head -c 900; echo
echo "----- P9 /api/health 状态词（两件件的主/演示面）-----"
curl -s -m 10 http://127.0.0.1:8787/api/health | head -c 700; echo
curl -s -m 10 http://127.0.0.1:8789/api/health | head -c 700; echo
echo "----- P10 临时件清理（上传件留在 /tmp 会被下一次排查当成品误用）-----"
rm -f /tmp/ts-new-$TAG /tmp/tas-new-$TAG && echo "已删 /tmp/ts-new-$TAG /tmp/tas-new-$TAG"
REMOTE_EOF
RC=$?
echo "REMOTE_EXIT=$RC"
[ "$RC" = "0" ] || exit "$RC"
echo "完成：两件件三单元已换。两条判据要等时间窗才出账，不在本次即刻读数里——"
echo "  ① ㊶ 的纯度计数只在**真发生一次审校产物被拒／尾段被剥**时才出现在 /metrics（现网样本要等真请求），"
echo "     接线判据＝矩阵 T70 已用同一条序列做过计数增量对账（21/21），线上这一半只需确认 HELP/TYPE 行在位；"
echo "  ② (52) 的会话侧条数要等 ai-assist 的整点清理 tick（从进程启动那一刻起算，不是自然整点）才出读数。"
