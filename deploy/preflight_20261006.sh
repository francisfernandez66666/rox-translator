#!/usr/bin/env bash
# preflight_20261006.sh — 发版前【只读】现网读数（〇-AR 第 3＋4 波换件前的基线）
#
# 为什么单独一个脚本、且刻意不含任何写操作：本批要验的三条腿（canned 启动期清理、
# 旧 sessions/messages 搬迁、USDT 监听状态词）都是**换件那一刻才跑一次**的形态，
# 没有"换件前"的同刻读数就永远说不出"这一轮真的动了"——只看换件后"库里现在有多少行"
# 是把"本来就有"当成"清理生效"，属本仓反复踩过的假绿形态。
#
# 写法说明（都是本仓真踩过的）：
#  ① 远端脚本走 **heredoc（带引号定界符，本地零展开）→ stdin → `bash -s`**，
#     不在命令行里做三层引号嵌套——上一版把一条 SQL 拆成两行写，第二行就成了"命令未找到"；
#  ② 主机与端口按既有口径从 deploy_to_production.sh 的默认值**现读**，本文件不写字面 IP；
#  ③ 本地 shell 不开 `set -e`（本脚本有预期会失败的探测子句），远端同理，计数一律 `|| true`。
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
if [ -z "$HOST" ] || [ -z "$PORT" ]; then
  echo "PREFLIGHT_ABORT=1 主机/端口没能从 deploy_to_production.sh 现读出来（那两行的写法变了）"
  exit 2
fi
echo "host=<现读，长度 ${#HOST}>  port=$PORT"

ssh -p "$PORT" -o ConnectTimeout=20 "root@$HOST" bash -s <<'REMOTE_EOF'
set -uo pipefail
echo "===== A0 机器时刻与磁盘 ====="
date -u +%FT%TZ
df -h / | tail -1

echo "===== A1 四个单元在跑哪一件（判哪一件在跑只看 /proc/<pid>/exe ＋体积＋特征串）====="
for u in translator translator-demo ai-assist translator-server-tenant; do
  act=$(systemctl is-active "$u" 2>/dev/null || true)
  ts=$(systemctl show -p ActiveEnterTimestamp --value "$u" 2>/dev/null || true)
  pid=$(systemctl show -p MainPID --value "$u" 2>/dev/null || true)
  exe=""
  if [ -n "${pid:-}" ] && [ "$pid" != "0" ]; then exe=$(readlink "/proc/$pid/exe" 2>/dev/null || true); fi
  sz="<无>"; sh="<无>"
  if [ -n "${exe:-}" ] && [ -f "$exe" ]; then
    sz=$(stat -c %s "$exe" 2>/dev/null || echo "<取不到>")
    sh=$(sha256sum "$exe" 2>/dev/null | cut -c1-16)
  fi
  echo "unit=$u active=$act since=$ts pid=$pid exe=${exe:-<无>} size=$sz sha16=${sh:-<无>}"
done

echo "===== A2 三件文件指纹与体积（与本机新建件对照用）====="
for f in /opt/translator/bin/translator-server /opt/translator-demo/bin/translator-server /opt/ai-assist/bin/translator-assist; do
  if [ -f "$f" ]; then echo "$f size=$(stat -c %s "$f") sha16=$(sha256sum "$f" | cut -c1-16)"; else echo "$f <不存在>"; fi
done
echo "----- 特征串：判第 3／4 波在不在件里 -----"
for f in /opt/translator/bin/translator-server /opt/translator-demo/bin/translator-server; do
  if [ -f "$f" ]; then
    echo "$f usdt_watch_dead=$(grep -ac usdt_watch_dead "$f" 2>/dev/null || true) usdt_watch=$(grep -ac usdt_watch "$f" 2>/dev/null || true)"
  fi
done
if [ -f /opt/ai-assist/bin/translator-assist ]; then
  echo "/opt/ai-assist/bin/translator-assist canned_feature_key_unsafe=$(grep -ac canned_feature_key_unsafe /opt/ai-assist/bin/translator-assist 2>/dev/null || true) canned_bg_backoff=$(grep -ac canned_bg_backoff /opt/ai-assist/bin/translator-assist 2>/dev/null || true) fabr_count_claim=$(grep -ac fabr_count_claim /opt/ai-assist/bin/translator-assist 2>/dev/null || true)"
fi

echo "===== A3 两站 web 根：入口 asset 名＋dotfile 计数（换源前基线）====="
for w in /opt/translator/web /opt/translator-demo/web; do
  if [ -d "$w" ]; then
    a=$(grep -oE 'index-[A-Za-z0-9_-]+\.js' "$w/index.html" 2>/dev/null | head -1)
    d=$(find "$w" \( -name .DS_Store -o -name '._*' \) 2>/dev/null | wc -l)
    echo "$w entry=${a:-<取不到>} dirty=$d index_bytes=$(stat -c %s "$w/index.html" 2>/dev/null || echo '<无>')"
  else
    echo "$w <目录不存在>"
  fi
done
echo "----- 归档份数（发版按文件名时间戳裁到 4）-----"
for w in /opt/translator /opt/translator-demo; do
  wo=$(ls -d "$w"/web_old.* 2>/dev/null | wc -l)
  bb=$(ls "$w"/bin/ 2>/dev/null | grep -cE 'translator-server\.(bak|pre)' || true)
  echo "$w web_old=$wo bin_归档=$bb"
done
ab=$(ls /opt/ai-assist/bin/ 2>/dev/null | grep -c '\.bak' || true)
echo "/opt/ai-assist bin_归档=$ab"

echo "===== A4 挂件库：canned 行数与行首 12 位指纹（只取键＋前 12 字符，禁止整值 dump）====="
DB=/opt/ai-assist/data/assist.db
if [ -f "$DB" ]; then
  echo "db_size=$(stat -c %s "$DB")"
  if command -v sqlite3 >/dev/null 2>&1; then
    echo "canned_rows=$(sqlite3 -readonly "$DB" "select count(*) from configs where key like 'i18n:%';" 2>/dev/null || echo '<查询失败>')"
    sqlite3 -readonly "$DB" "select key, substr(value,1,12) from configs where key like 'i18n:%' order by key;" 2>/dev/null || echo '<读指纹失败>'
    echo "----- 旧代表（本批迁移腿要搬的那两张）-----"
    for t in sessions_base messages_base sessions messages; do
      echo "table=$t rows=$(sqlite3 -readonly "$DB" "select count(*) from $t;" 2>/dev/null || echo '<无此表>')"
    done
    echo "----- 表清单（核对迁移腿到底认识哪几张）-----"
    sqlite3 -readonly "$DB" "select name from sqlite_master where type='table' order by name;" 2>/dev/null || true
  else
    echo "sqlite3_not_on_server：这一腿改用本地副本读（scp 只读取 .backup 出来的那份）"
  fi
else
  echo "$DB <不存在>"
fi

echo "===== A5 主库档位现值（只读数，确认没被管理台外改过）====="
sudo -u postgres psql -d langcross -Atc "SELECT key,value FROM system_config WHERE key IN ('register_ip_min_interval_sec','register_device_daily_limit','trial_device_quota','billing_enforced') ORDER BY key" 2>/dev/null || echo "<主库读档失败>"
echo "online_api_key_len=$(sudo -u postgres psql -d langcross -Atc "SELECT COALESCE(length(value),-1) FROM system_config WHERE key='online_api_key'" 2>/dev/null || echo '<查询失败>')"
echo "model_routes_len=$(sudo -u postgres psql -d langcross -Atc "SELECT COALESCE(length(value),-1) FROM system_config WHERE key='model_routes'" 2>/dev/null || echo '<查询失败>')"
echo "rate_limit_cells_today=$(sudo -u postgres psql -d langcross -Atc "SELECT count(*) FROM rate_limits WHERE scope IN ('reg_day','trial_day')" 2>/dev/null || echo '<查询失败>')"

echo "===== A6 健康面（只读 GET）====="
for p in 8787 8789; do
  echo "----- 127.0.0.1:$p/api/health -----"
  curl -s -m 10 "http://127.0.0.1:$p/api/health" | head -c 900
  echo
done
echo "----- assist /health -----"
curl -s -m 10 http://127.0.0.1:8790/health | head -c 400
echo

echo "===== A7 定时器（本批不动它们，只登记现值）====="
echo "dispatch_expiry_timer=$(systemctl is-enabled translator-dispatch-expiry.timer 2>/dev/null || echo '<无>')"
systemctl list-timers --all --no-pager 2>/dev/null | grep -E 'dispatch|translator|assist|logrotate' | head -10 || echo "<无相关定时器>"

echo "===== A8 三份日志的量级与错误计数（只数条数，不贴正文）====="
for L in /opt/translator/log/translator.log /opt/translator-demo/log/translator.log /opt/ai-assist/data/assist.log; do
  if [ -f "$L" ]; then
    echo "$L size=$(stat -c %s "$L") panic=$(grep -ci panic "$L" 2>/dev/null || true) ERROR=$(grep -c '"level":"ERROR"' "$L" 2>/dev/null || true) WARN=$(grep -c '"level":"WARN"' "$L" 2>/dev/null || true)"
  else
    echo "$L <不存在>"
  fi
done
echo "PREFLIGHT_DONE=1"
REMOTE_EOF
echo "PREFLIGHT_EXIT=$?"
