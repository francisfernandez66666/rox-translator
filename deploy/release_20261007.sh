#!/usr/bin/env bash
# release_20261007.sh — 〇-AR 第 7 波换件（**只换后端两件，不动前端**）
#
# 本批动了什么（按 AGENTS §一·5「五类渲染面」点名，别默认 dist 绿＝全站绿）：
#   · `internal/store/kb_scrape_ledger.go`（新增，采集进度账回收腿）＋`internal/crawler/crawler.go`（挂到 RunDaily 入口）
#     ⇒ 落在 **translator-server**，两台（主站 `/opt/translator/bin`、演示 `/opt/translator-demo/bin`）都要换；
#   · `internal/assist/store/store.go`（`CleanupExpiredAnonymous` 加"按消息表真值归一 msg_count"那条腿）
#     ⇒ 挂件库的清理只在 **ai-assist** 进程里跑 ⇒ **translator-assist** 必须换；
#   · 前端／扩展／SDK／派发脚本本轮**一字未动** ⇒ 不换源、不重打包（漂移闸按 10-06 那批的口径不重复跑）。
#
# 两道中止线（都在动手之前，不是事后补救）：
#   ① 本机新件里**特征串缺失** ⇒ 不上传（表现是"编译的是旧工作树"，历史上真踩过一次）；
#   ② 上传件在服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，此时磁盘上还没落位任何新件。
# 落位一律 `cp 到新名` ＋ 同目录 `mv -f` rename（`cp` 直接覆盖运行中二进制会 ETXTBSY）。
#
# 发版判据按用户既定口径：**只验接线，不跑线上像素校验**。
# 用法：bash deploy/release_20261007.sh            # 干跑（编译＋特征串，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261007.sh    # 真换
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261007"

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

echo "===== P0-B 特征串（中止线①：件里没这两条腿＝本批等于没上线）====="
# ① server：回收腿的日志文案与可配键名（`kb_scrape_ledger.go` 独有）
S_LEDGER=$(grep -ac "采集进度账已回收" "$SERVER_BIN" || true)
S_CONFKEY=$(grep -ac "scrape_ledger_retention_days" "$SERVER_BIN" || true)
# ② assist：归一那条 UPDATE 的字面片段（旧件里零命中）
A_NORM=$(grep -ac "msg_count=(SELECT COUNT(\*)" "$ASSIST_BIN" || true)
echo "server: 回收日志串=$S_LEDGER 保留天数键=$S_CONFKEY ；assist: 计数归一 SQL 串=$A_NORM"
# （判据只有一条：正锁命中即继续，缺失即中止。曾经这里有一行引用未定义变量的残留，已删。）
if [ "$S_LEDGER" = "0" ] || [ "$S_CONFKEY" = "0" ]; then
  echo "ABORT=2 translator-server 里缺回收腿特征串（编译的不是含本批改动的树）"; exit 3
fi
if [ "$A_NORM" = "0" ]; then
  echo "ABORT=2 translator-assist 里缺 msg_count 归一腿特征串（㊿ 的修法没进件）"; exit 3
fi

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
echo "远端时刻=$(date -u +%FT%TZ) TS=$TS"

echo "----- P2 校验上传件（中止线②：不等即停，此时一件未落位）-----"
a=$(sha256sum /tmp/ts-new-$TAG | cut -d' ' -f1)
b=$(sha256sum /tmp/tas-new-$TAG | cut -d' ' -f1)
echo "server_sha=$a"; echo "assist_sha=$b"
[ "$a" = "$EXPECT_TS" ]  || { echo "ABORT=4 后端上传件 sha 不等，未落位即停"; exit 4; }
[ "$b" = "$EXPECT_TAS" ] || { echo "ABORT=4 挂件上传件 sha 不等，未落位即停"; exit 4; }

echo "----- P2-B 换件前基线（两条现网判据各自的起点，不贴任何取值内容）-----"
DB=/opt/ai-assist/data/assist.db
echo "换件前 挂件库：sessions_base=$(sqlite3 -readonly "$DB" "select count(*) from sessions_base;" 2>/dev/null || echo '<失败>') 壳行(msg_count>0 且已无消息)=$(sqlite3 -readonly "$DB" "select count(*) from sessions_base where msg_count>0 and id not in (select session_id from messages_base);" 2>/dev/null || echo '<失败>') messages_base=$(sqlite3 -readonly "$DB" "select count(*) from messages_base;" 2>/dev/null || echo '<失败>')"
for d in langcross langcross_demo; do
  echo "换件前 PG 库 $d：system_config 总行=$(sudo -u postgres psql -At -c "select count(*) from system_config;" "$d" 2>/dev/null || echo '<失败>') kb_scrape_% 行=$(sudo -u postgres psql -At -c "select count(*) from system_config where key like 'kb_scrape_%';" "$d" 2>/dev/null || echo '<失败>') 当天断点键=$(sudo -u postgres psql -At -c "select count(*) from system_config where key like 'kb_scrape_checkpoint_'||to_char(current_date,'YYYY-MM-DD')||'%';" "$d" 2>/dev/null || echo '<失败>')"
done

echo "----- P2-B2 特征串负向对照（★ 必须在落位之前读线上旧件）-----"
# 本机那两道正锁（S_LEDGER/S_CONFKEY/A_NORM）只证明"新件里有"，不证明"旧件里没有"。
# 若旧件也命中，中止线①就是恒绿的空转判据——本段把这条负向对照落成读数，
# 期望三个 0；任一非 0 ⇒ 该特征串不唯一，换件后"日志里出现这行"不能再当"本批已上线"的证据。
for pair in "/opt/translator/bin/translator-server:采集进度账已回收" "/opt/translator/bin/translator-server:scrape_ledger_retention_days" "/opt/ai-assist/bin/translator-assist:msg_count=(SELECT COUNT(*)"; do
  f="${pair%%:*}"; pat="${pair#*:}"
  echo "旧件 $f 命中『$pat』=$(grep -a -c -F -- "$pat" "$f" 2>/dev/null || echo '<失败>')  size=$(stat -c %s "$f" 2>/dev/null || echo '<无>')  sha16=$(sha256sum "$f" 2>/dev/null | cut -c1-16)"
done

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

echo "----- P5 启动后 err 级日志（★ 必须先滤掉 journalctl 的「-- No entries --」横幅，否则 grep -c . 把它也数成 1 条）-----"
for u in translator translator-demo ai-assist; do
  n=$(journalctl -u "$u" --since "$RESTART_SINCE" -p err --no-pager 2>/dev/null | grep -v '^-- No entries --$' | grep -c . || true)
  echo "unit=$u err行数=$n"
done

echo "----- P6 回收腿接线读数：采集器跑一轮才会出账（scrape_poll_sec 默认 300s）-----"
echo "  现在（重启即刻）："
for d in langcross langcross_demo; do
  echo "    PG $d kb_scrape_% 行=$(sudo -u postgres psql -At -c "select count(*) from system_config where key like 'kb_scrape_%';" "$d" 2>/dev/null || echo '<失败>')"
done
echo "  挂件库壳行判据（清理腿每小时整点跑一次，重启后第一次 tick 内应归 0）："
echo "    壳行=$(sqlite3 -readonly "$DB" "select count(*) from sessions_base where msg_count>0 and id not in (select session_id from messages_base);" 2>/dev/null || echo '<失败>') sessions_base=$(sqlite3 -readonly "$DB" "select count(*) from sessions_base;" 2>/dev/null || echo '<失败>') messages_base=$(sqlite3 -readonly "$DB" "select count(*) from messages_base;" 2>/dev/null || echo '<失败>')"
echo "----- P7 /api/health 状态词（两件件的主/演示面＋挂件面）-----"
curl -s -m 10 http://127.0.0.1:8787/api/health | head -c 700; echo
curl -s -m 10 http://127.0.0.1:8789/api/health | head -c 700; echo
curl -s -m 10 http://127.0.0.1:8790/health | head -c 300; echo
echo "----- P8 临时件清理（上传件留在 /tmp 会被下一次排查当成品误用）-----"
rm -f /tmp/ts-new-$TAG /tmp/tas-new-$TAG && echo "已删 /tmp/ts-new-$TAG /tmp/tas-new-$TAG"
REMOTE_EOF
RC=$?
echo "REMOTE_EXIT=$RC"
[ "$RC" = "0" ] || exit "$RC"
echo "完成：两件件三单元已换。⚠️ 两条现网判据不在本次读数里收尾——"
echo "  ① 采集账回收：等下一次采集轮（scrape_poll_sec 默认 300s）跑过，看日志一行「采集进度账已回收 deleted_keys=<N>」，"
echo "     并复读 kb_scrape_% 行数应 ≤ 启用来源数×14×2＋非日期族几把，且**当天断点键仍取到值**；"
echo "  ② 会话壳行：等 ai-assist 的整点清理 tick，壳行读数归 0。"
