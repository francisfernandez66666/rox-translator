#!/usr/bin/env bash
# ============================================================================
# deploy/probe_d01_and_uat_debris_20261027.sh — D-01 闭环探针 ＋ UAT 残骸盘点（**全程只读**）
#
# 为什么写成脚本而不是逐条现攒命令（AGENTS §一·5 同一纪律）：
#   「今天哪些行是测试打出来的」这个集合，将来要**逐字**当清理集合用；
#   两处各圈一次迟早不一致（本仓真踩过「备份集合≠删除集合」）。
#
# 只读口径：psql 只跑 SELECT、curl 只打 GET、磁盘只 stat；零写入。
# 打印内容：id／计数／basename／字节数——不打印正文、不打印凭证、不打印绝对目录。
#
# ★ 时间边界（AGENTS「TEXT 时间戳的窗口边界」同族，这里必须点名）：
#   `tickets.created_at` 是 **UTC 文本**。本批 UAT 跑在本地（+08:00）2026-10-27 05:00~08:00，
#   库里那一列写的是 2026-10-26 21:xx~2026-10-27 00:xx UTC ⇒
#   默认档取 `2026-10-26 20:00`（UTC）＝本地 10-27 04:00，整段都在窗口内；
#   谁把这里改成 `>= '2026-10-27'`（按日界）就会**结构性漏掉当晚那一批**，
#   于是"清干净了"其实是"没数到"。脚本会把库里现读的 max(created_at) 一起打出来做对照。
#
# 用法（在部署机上跑）：
#   bash deploy/probe_d01_and_uat_debris_20261027.sh
#   DB=langcross_demo PORT=8789 bash deploy/probe_d01_and_uat_debris_20261027.sh   # 演示站同尺再读一遍
# ============================================================================
set -uo pipefail

DB="${DB:-langcross}"
PORT="${PORT:-8787}"
# ★★ 边界写法必须与**库里那一列的实际形态同形**（2026-10-27 现网实测）：
#   `tickets.created_at` 存的是 `2026-10-26T00:04:32+08:00` 这种带 **T** 的 ISO 文本，
#   若把界写成带**空格**的 `'2026-10-26 20:00'`，同一天里 `'T'(0x54) > ' '(0x20)` ⇒
#   当天**不分时刻**全部落进窗口（本批就是这么把 10-26 的行一起圈进来的；事后逐条核对确认
#   被摘的 12 张工单全是 UAT 命名件、身份只有 uid=10001/1，没有客户数据，且 281 行用量已整行备份）。
#   ⇒ 现在默认档改成同形带 T 的 ISO；改这一档必须先看 `SELECT max(created_at)` 的真实形状。
SINCE="${SINCE:-2026-10-26T00:00:00+08:00}"
PSQL=(sudo -u postgres psql -d "$DB" -Atc)

echo "== 0) 库与窗口（现读对照，别拿日历当事实）=="
echo "  DB=$DB  PORT=$PORT  窗口(UTC) >= '$SINCE'"
# ⚠️ 这条 SQL 里**不许再出现任何内层单引号**：本行外层已是 `$( … 'SQL' )` 的双引号包单引号形态，
#    塞 `'<字面>'` 进去会让 bash 把 `空值字面` 当成命令名去执行（首跑真报
#    `line 32: empty: No such file or directory` ⇒ 这条"边界对照读数"整个缺失，
#    于是窗口取窄了也看不出来）。回退值改在**壳里**给，不在 SQL 里给。
max_created=$("${PSQL[@]}" 'SELECT max(created_at) FROM tickets;')
echo "  tickets.max(created_at) = ${max_created:-<未读>}"

echo "== 1) 窗口内工单（id | 单号 | 状态 | 租户 | 创建人 | 标题 basename）=="
"${PSQL[@]}" "
SELECT id || ' | ' || ticket_no || ' | ' || status || ' | tid=' || tenant_id || ' | uid=' || created_by || ' | ' || replace(title, '/', '_')
FROM tickets
WHERE created_at >= '$SINCE'
ORDER BY id;"

echo "== 2) 这些工单的产物行（result_path 是否已登记）=="
"${PSQL[@]}" "
SELECT tf.id || ' | t=' || tf.ticket_id || ' | ' || replace(coalesce(tf.file_name,'-'), '/', '_') || ' | result=' ||
  CASE WHEN coalesce(tf.result_path,'')='' THEN '<未登记>' ELSE 'set' END ||
  ' | text_result=' || CASE WHEN coalesce(tf.text_result_path,'')='' THEN '<无>' ELSE 'set' END
FROM ticket_files tf
JOIN tickets t ON t.id = tf.ticket_id
WHERE t.created_at >= '$SINCE'
ORDER BY tf.ticket_id, tf.id;"

echo "== 3) 产物文件实测（basename + 字节；库里那行指向的文件到底在不在盘上）=="
"${PSQL[@]}" "
SELECT tf.result_path
FROM ticket_files tf JOIN tickets t ON t.id = tf.ticket_id
WHERE t.created_at >= '$SINCE' AND coalesce(tf.result_path,'') <> ''
ORDER BY tf.ticket_id;" |
  while IFS= read -r p; do
    [ -z "$p" ] && continue
    if [ -f "$p" ]; then
      echo "  OK   $(basename "$p")  $(stat -c %s "$p") B"
    else
      echo "  MISS $(basename "$p") （库里那行指向的文件不在盘上）"
    fi
  done

echo "== 4) D-01 端点形状判别（回环、不带任何凭证）=="
ok_shape=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "http://127.0.0.1:$PORT/api/tickets/download?id=133")
bad_shape=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "http://127.0.0.1:$PORT/api/tickets/133/download")
echo "  正确形态 /api/tickets/download?id=133  => $ok_shape （判据：401/403＝路由存在只是没凭证；**404 即判死**）"
echo "  错误形态 /api/tickets/133/download     => $bad_shape （读数：404「接口不存在」＝UAT 三条探针撞的那面墙）"
echo "  端到端字节取回 = UNVERIFIED（需要属主凭证，本探针刻意不带；不把它写成已确认）"

echo "== 5) 窗口内测试残骸计数（只数不删；将来清理集合必须与此逐字同条件）=="
"${PSQL[@]}" "
SELECT 'tickets        = ' || count(*) FROM tickets WHERE created_at >= '$SINCE';"
"${PSQL[@]}" "
SELECT 'ticket_files   = ' || count(*) FROM ticket_files tf JOIN tickets t ON t.id=tf.ticket_id WHERE t.created_at >= '$SINCE';"
"${PSQL[@]}" "
SELECT 'ticket_state   = ' || count(*) FROM ticket_state ts JOIN tickets t ON t.id=ts.ticket_id WHERE t.created_at >= '$SINCE';"
"${PSQL[@]}" "
SELECT 'usage_ledger   = ' || count(*) FROM usage_ledger WHERE created_at >= '$SINCE';"
"${PSQL[@]}" "
SELECT 'notifications  = ' || count(*) FROM notifications WHERE created_at >= '$SINCE';"
"${PSQL[@]}" "
SELECT 'chat_convs     = ' || count(*) FROM chat_conversations WHERE created_at >= '$SINCE';"
"${PSQL[@]}" "
SELECT 'chat_msgs      = ' || count(*) FROM chat_messages cm JOIN chat_conversations c ON c.id=cm.conversation_id WHERE c.created_at >= '$SINCE';"
"${PSQL[@]}" "
SELECT 'users_today    = ' || count(*) FROM users WHERE created_at >= '$SINCE' AND username NOT LIKE 'demo\_%';"
"${PSQL[@]}" "
SELECT 'api_keys       = ' || count(*) FROM api_keys WHERE created_at >= '$SINCE';"

echo "== 6) 窗口内新建身份（只出 id/用户名/家租户/is_home；★ 按用户名取身份必须钉 is_home=1，多身份批之后 username 不唯一）=="
"${PSQL[@]}" "
SELECT u.id || ' | ' || u.username || ' | tid=' || coalesce(u.tenant_id,0) || ' | is_home=' || coalesce(u.is_home,0)
FROM users u
WHERE u.created_at >= '$SINCE'
ORDER BY u.id;"
echo "PROBE_DONE=1"
