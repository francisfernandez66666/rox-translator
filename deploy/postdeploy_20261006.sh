#!/usr/bin/env bash
# deploy/postdeploy_20261006.sh —— 10-06 发版后的现网接线复查（**全程只读**）
#
# 这条脚本要回答的不是"服务起没起"（那一层 release_20261006.sh 的 P5/P6 已经钉死），
# 而是本批**只在运行期才出真值**的四族腿，加上第一轮复查里自己被纠正掉的判据：
#
#   ① usdt_watch 状态词：重启后 9 秒取只会是 unknown（第一 tick 未到），真值要等一轮对账；
#      现网实测 = failing（TronGrid 的 /v1/blocks 整族已 404，见《缺陷与缺失清单》⑮ 补腿条目），
#      而 failing 正是 ⑮ 要的产出——收银台那句"达到确认数后自动入账"当场停用，告警落 open 行。
#   ② canned 冷启动三腿：启动期清理（canned_purge_stale）删掉旧代字节 → 首访同步腿超时出中文
#      （canned_sync_timeout）→ 后台补翻生效落缓存 → 第二次同语种直接命中。**四步都得有读数**。
#   ③ 挂件旧表搬迁：sessions/messages 迁进 *_base 后旧表 DROP（只在启动那一刻跑一次）。
#   ④ 换源后的磁盘事实：dotfile=0、归档按**下划线族**数到 4、公网 /.DS_Store 只回 SPA 兜底 HTML、
#      托管物（扩展 zip / SDK whl·tgz）非 HTML 兜底且魔数对。
#
# ★ 三条判据口径是这一批复跑时**改过来的**，别再写回旧形态：
#   · 挂件健康面是 `/health`，不是 `/api/health`（打错得 404 page not found，那不是缺陷是打错路）；
#   · 归档族名一律 `web_old.<TS>`（与 deploy_to_production.sh:57 同族）；按 `web.old.*` 数恒回 0，
#     会把"没裁"读成"裁干净了"；
#   · pprof 走公网回 200 **不等于**泄漏：`spa.go` 对未知路径兜底回 index.html 且状态码 200，
#     判据必须是 content-type 与字节数（真 pprof 不是 text/html）。
#
# 库侧读数一律走 `sudo -u postgres psql`（同 preflight_20261006.sh 的口径），
# **不读 secrets.env 里的 DSN、不把口令带进任何进程环境**——这一批最初的三版复查脚本就是在这里
# 既写复杂了、又把"读现网凭据"这条腿引进临时脚本，属于自找的暴露面。
#
# 用法：bash deploy/postdeploy_20261006.sh
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$REPO/deploy/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$CONF" | head -1)"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$CONF" | head -1)"
[ -n "$HOST" ] || { echo "ABORT=1 主机没解析出来"; exit 2; }
[ -n "$PORT" ] || PORT=22

echo "===== 发版后接线复查 本机时刻=$(date -u +%FT%TZ) ====="
ssh -p "$PORT" -o ConnectTimeout=20 -o BatchMode=yes "root@$HOST" bash -s <<'REMOTE_EOF'
set -uo pipefail
LOGF=/opt/translator/log/translator.log
RC=0
bad() { echo "  FAIL $1"; RC=1; }
ok()  { echo "  ✔ $1"; }

echo "----- A 三单元存活与现役件（哪一件在跑只认 /proc/<pid>/exe）-----"
for unit in translator translator-demo ai-assist; do
  pid=$(systemctl show -p MainPID --value "$unit" 2>/dev/null)
  exe=$(readlink "/proc/$pid/exe" 2>/dev/null || echo "<读不到>")
  sha=$(sha256sum "$exe" 2>/dev/null | cut -c1-16)
  echo "  unit=$unit active=$(systemctl is-active "$unit" 2>/dev/null) nrestarts=$(systemctl show -p NRestarts --value "$unit" 2>/dev/null) exe=$exe sha16=$sha"
done
for p in 8787 8789; do
  echo "  port=$p livez=$(curl -s -o /dev/null -w '%{http_code}' --max-time 6 http://127.0.0.1:$p/livez) readyz=$(curl -s -o /dev/null -w '%{http_code}' --max-time 6 http://127.0.0.1:$p/readyz)"
done

echo
echo "----- B usdt_watch：状态词＋每轮失败原因＋告警 open 行（⑮ 的三腿）-----"
for p in 8787 8789; do
  echo "  port=$p $(curl -s --max-time 8 http://127.0.0.1:$p/api/health | grep -o '"usdt_watch":"[a-z]*"') $(curl -s --max-time 8 http://127.0.0.1:$p/api/health | grep -o '"dispatch":"[a-z]*"')"
done
echo "  主单元 usdt-watch 行总数=$(grep -c 'usdt-watch' "$LOGF" 2>/dev/null || echo 0) 最近 2 条（遮 URL/hex）："
grep 'usdt-watch' "$LOGF" 2>/dev/null | tail -2 | sed -E 's#https?://[^ "]*#<url>#g; s/[0-9a-fA-F]{16,}/<hex>/g' | cut -c1-190 | sed 's/^/    /'
echo "  收银台联动面：USDT 开关现值与告警行（只读，sudo -u postgres）："
sudo -u postgres psql -d langcross -Atc "select key||'='||value from system_config where key in ('usdt_enabled','usdt_auto_settle','usdt_chains') order by key;" 2>/dev/null | sed 's/^/    /' || echo "    <主库读档失败>"
sudo -u postgres psql -d langcross -Atc "select 'alerts usdt_watch_dead: '||status||' '||count(*)||' 最近 '||max(created_at) from alerts where kind='usdt_watch_dead' group by status;" 2>/dev/null | sed 's/^/    /' || echo "    <告警查不到>"

echo
echo "----- C 挂件 canned 四步读数：清理→同步超时→后台补翻生效→二次命中缓存-----"
echo "  /health: $(curl -s --max-time 8 http://127.0.0.1:8790/health)"
python3 - <<'PY'
import json, collections
c = collections.Counter()
try:
    fh = open('/opt/ai-assist/data/assist.log', 'r', errors='replace')
except Exception as e:
    print('  SKIP=1 assist.log 读不到', type(e).__name__); raise SystemExit(0)
for line in fh:
    if 'canned' not in line:
        continue
    try:
        d = json.loads(line)
    except Exception:
        continue
    c[(d.get('level', '?'), str(d.get('msg', ''))[:44], str(d.get('reason', ''))[:26])] += 1
for (lvl, msg, reason), n in sorted(c.items(), key=lambda x: -x[1])[:10]:
    print('   ', n, '|', lvl, '|', msg, '|', reason)
PY
sqlite3 /opt/ai-assist/data/assist.db "select count(*) from configs where key like 'i18n:%';" | sed 's/^/  canned_rows=/'
sqlite3 /opt/ai-assist/data/assist.db "select substr(key,6,99) from configs where key like 'i18n:%' order by key;" | sed 's/^/    /'

echo
echo "----- D 挂件旧表搬迁：新表接住行数、旧表已 DROP、来源标记在位-----"
for t in sessions_base messages_base sessions messages; do
  n=$(sqlite3 /opt/ai-assist/data/assist.db "select count(*) from $t;" 2>/dev/null || echo "<无此表>")
  echo "  table=$t rows=$n"
done
sqlite3 /opt/ai-assist/data/assist.db "select count(*) from sessions_base where anonym_hash='legacy_migrated';" 2>/dev/null | sed 's/^/  迁进行标记 legacy_migrated 数=/'
echo "  ★ 迁移行的**清理归宿**（这是 #27 的另一半：搬过去不等于收得住）——"
echo "    清理腿只删 msg_count=0 的匿名会话，而迁进来的行 msg_count 带着旧计数，"
echo "    消息被按批删走后会话壳就再也删不掉。读数（只数不贴正文）："
echo "    ⚠️ 聚合必须套 COALESCE：空表上 sum(...) 退 NULL，而「'文本'||NULL」整行变 NULL，"
echo "       （这一行本身也是一条口径：echo 的双引号里**不许用反引号做强调**——"
echo "        反引号会被当命令替换执行，远端退「文本: command not found」，强调那段文字直接从输出里消失。）"
echo "       sqlite3 就**一行都不吐**——读起来像'查询没跑'，实际是'表是空的'（本批复跑真踩）。"
sqlite3 /opt/ai-assist/data/assist.db "select 'messages_base 总行='||count(*)||' 其中 legacy='||COALESCE(sum(anonym_hash='legacy_migrated'),0) from messages_base;" 2>/dev/null | sed 's/^/    /'
sqlite3 /opt/ai-assist/data/assist.db "select 'sessions_base 壳行（msg_count>0 但已无消息）='||count(*) from sessions_base where msg_count>0 and id not in (select session_id from messages_base);" 2>/dev/null | sed 's/^/    /'
sqlite3 /opt/ai-assist/data/assist.db "select 'sessions_base 已过期未删='||count(*) from sessions_base where anonym_hash!='' and (expires_at is null or expires_at<=CURRENT_TIMESTAMP);" 2>/dev/null | sed 's/^/    /'

echo
echo "----- E 磁盘事实：web 根 dotfile／归档族／权限／公网负向-----"
for w in /opt/translator/web /opt/translator-demo/web; do
  d=$(find "$w" \( -name .DS_Store -o -name '._*' \) 2>/dev/null | wc -l)
  [ "$d" = "0" ] && ok "$w dotfile=0" || bad "$w dotfile=$d（必须 0）"
  echo "  $w 下划线归档=$(ls -1d "${w}"_old.* 2>/dev/null | grep -c .) 份 点族=$(ls -1d "$w".old.* 2>/dev/null | grep -c .) 份 入口=$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$w/index.html" | head -1)"
done
for u in https://langcross.lexicorn.cn https://rox-test.lexicorn.cn; do
  r=$(curl -s -o /tmp/_ds -w '%{http_code}|%{content_type}|%{size_download}' --max-time 20 "$u/.DS_Store" || echo CURL_FAIL)
  home=$(curl -s -o /tmp/_hm -w '%{size_download}' --max-time 20 "$u/" || echo 0)
  sz=$(echo "$r" | awk -F'|' '{print $3}')
  ct=$(echo "$r" | awk -F'|' '{print $2}')
  if [ "$ct" = "text/html; charset=utf-8" ] && [ "$sz" = "$home" ]; then ok "$u/.DS_Store 是 SPA 兜底（字节=$sz＝首页 $home）"; else bad "$u/.DS_Store code|ct|sz=$r 首页=$home（磁盘上还有真文件）"; fi
done
for path in /extensions/langcross-extension-latest.zip /sdk/langcross_translator-latest-py3-none-any.whl /sdk/langcross-translator-sdk-latest.tgz; do
  r=$(curl -s -o /tmp/_mg -w '%{http_code}|%{content_type}|%{size_download}' --max-time 20 "https://langcross.lexicorn.cn$path" || echo CURL_FAIL)
  magic=$(head -c 2 /tmp/_mg | xxd -p | tr -d '\n')
  echo "  $path = $r magic=$magic"
done
echo "  pprof 公网必须不是真端点（真 pprof 的 content-type 不是 text/html）："
echo "    $(curl -s -o /dev/null -w 'code=%{http_code} ct=%{content_type} sz=%{size_download}' --max-time 20 https://langcross.lexicorn.cn/debug/pprof/)"

echo
echo "----- F 三域首页直出与 __BRANDING__（谁直出决定品牌在不在）-----"
for u in https://langcross.lexicorn.cn https://demo.lexicorn.cn https://rox-test.lexicorn.cn; do
  b=$(curl -s --max-time 20 "$u/" | grep -c '__BRANDING__' || true)
  echo "  $u branding=$b bytes=$(curl -s -o /dev/null -w '%{size_download}' --max-time 20 "$u/")"
done
echo "  ⚠️ 口径备忘：demo.lexicorn.cn 在 Caddy 里**没有自己的站点块**，走 *.lexicorn.cn 通配块打到 8787；"
echo "     演示单元（8789）的真域名是 rox-test.lexicorn.cn。别拿 demo 域当演示单元读演示站。"

echo
echo "----- G 派发闹钟必须还在（第 3 波那只判'失败也退役'的闹钟）-----"
echo "  translator-dispatch-expiry.timer is-enabled=$(systemctl is-enabled translator-dispatch-expiry.timer 2>/dev/null) is-active=$(systemctl is-active translator-dispatch-expiry.timer 2>/dev/null)"
systemctl list-timers --all --no-pager 2>/dev/null | grep dispatch | head -2 | sed 's/^/    /'

echo
echo "----- H 日志水位（只数不贴正文）-----"
echo "  $LOGF size=$(stat -c %s "$LOGF" 2>/dev/null) panic=$(grep -c panic "$LOGF" 2>/dev/null || echo 0) ERROR=$(grep -c '"level":"ERROR"' "$LOGF" 2>/dev/null || echo 0)"
echo "  /opt/ai-assist/data/assist.log size=$(stat -c %s /opt/ai-assist/data/assist.log 2>/dev/null) WARN=$(grep -c '"level":"WARN"' /opt/ai-assist/data/assist.log 2>/dev/null || echo 0)"
echo "  ★ 这两个 unit 的日志都不在 journal：translator StandardOutput/Error=append（落文件），"
echo "    translator-demo 才是 journal（StandardOutput=journal）——journalctl -u ai-assist 恒回 0。"

echo
if [ "$RC" = "0" ]; then echo "POSTDEPLOY_ALL_OK=1"; else echo "POSTDEPLOY_FAIL=1（上面有 bad 行）"; fi
echo POSTDEPLOY_EXIT=$RC
exit "$RC"
REMOTE_EOF
SSH_RC=$?
echo "POSTDEPLOY_LOCAL_EXIT=$SSH_RC"
# ★ 10-10 补的**这一族脚本自己的**「有判据没接线」：旧形态远端段最后一句是 echo（恒退 0），
#   本机层又把 `$?` 打成一行读数就结束 ⇒ **整份脚本永远退 0**，哪怕同一屏写着
#   POSTDEPLOY_FAIL=1。第 9 波复跑实测抓到的（`tail` 那层看到 exit 0，日志里却是 FAIL=1）。
#   这一族会被下一波照着抄，所以两份一起改，并配了静态锁
#   （internal/fileproc/postdeploy_exit_gate_test.go：远端 RC 必须一路带到本机退出码）。
if [ "$SSH_RC" = "0" ]; then
  echo "本机结论：复查全绿（现网接线一条不缺）"
else
  echo "本机结论：复查**未通过**，退出码 $SSH_RC（255＝链路没通，别读成『现网没问题』）"
fi
exit "$SSH_RC"
