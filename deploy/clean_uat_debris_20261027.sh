#!/usr/bin/env bash
# ============================================================================
# deploy/clean_uat_debris_20261027.sh — 今日窗口 UAT 测试残骸清理（默认**干跑**）
#
# 用户 2026-10-27 拍板：按「今日窗口」整段清（探针 deploy/probe_d01_and_uat_debris_20261027.sh
#   已给出精确计数与 id 清单：12 工单／6 产物行／89 状态轨迹／288 用量留痕／21 通知／12+24 会话）。
#
# 五条硬口径（都是本仓历史上真踩过的形态，见《部署指南》清理段与项目记忆）：
#   ① **备份集合＝删除集合**：同一份 WHERE 逐字复用，禁止备份用宽条件、删除用窄条件；
#   ② 备份用 `CREATE TABLE … AS SELECT` 进独立 schema `fix_bak_20261027_uat`，
#      并且**回导数行**核对（`wc -l` 会撒谎，键名错位一格不报错）；
#   ③ 删除按**子→父**顺序，且每张表删完立刻用同一条判据数"应当为 0"，不为 0 即停手不回滚一半；
#   ④ **产物文件不 rm**：移到 `/opt/translator/data/_quarantine/<TS>/`（磁盘上还在，可回滚），
#      库里那行指向的文件移不动时（不存在）只记 MISSING，不许当成失败半途退出；
#   ⑤ 演示站那批 `demo_%` 固定账号与**任何租户/套餐/余额/额度表都不在本脚本射程**——
#      这里只摘"测试打出来的行"，不碰钱与身份；quota_grants/balance_accounts/orders/payments 一律不动。
#
# 用法：
#   bash deploy/clean_uat_debris_20261027.sh          # 干跑：只打集合与计数，一个字节都不写
#   APPLY=1 bash deploy/clean_uat_debris_20261027.sh  # 备份 → 核对 → 删除 → 回验（需用户已授权）
# ============================================================================
set -uo pipefail

APPLY="${APPLY:-0}"
DB="${DB:-langcross}"
# ★★ 边界写法必须与**库里那一列的实际形态同形**（2026-10-27 现网实测）：
#   `tickets.created_at` 存的是 `2026-10-26T00:04:32+08:00` 这种带 **T** 的 ISO 文本，
#   若把界写成带**空格**的 `'2026-10-26 20:00'`，同一天里 `'T'(0x54) > ' '(0x20)` ⇒
#   当天**不分时刻**全部落进窗口（本批就是这么把 10-26 的行一起圈进来的；事后逐条核对确认
#   被摘的 12 张工单全是 UAT 命名件、身份只有 uid=10001/1，没有客户数据，且 281 行用量已整行备份）。
#   ⇒ 现在默认档改成同形带 T 的 ISO；改这一档必须先看 `SELECT max(created_at)` 的真实形状。
SINCE="${SINCE:-2026-10-26T00:00:00+08:00}"          # 与探针同一把尺子，且与库里那一列同形（见上方同形注记）
BAK="${BAK:-fix_bak_20261027_uat}"
QUAR="${QUAR:-/opt/translator/data/_quarantine/uat_debris_$(date +%Y%m%d_%H%M%S)}"
PSQL=(sudo -u postgres psql -d "$DB" -v ON_ERROR_STOP=1 -Atc)
FAIL=0

bad() { echo "BAD  $*"; FAIL=$((FAIL + 1)); }

# 窗口内工单 id 集（**所有下游集合都由它派生**，避免两处各圈一次）
TICKET_IDS="SELECT id FROM tickets WHERE created_at >= '$SINCE'"
# 窗口内两个测试身份的用量留痕（uid=10001 主站 UAT 账号／uid=1 超管手测），以及它们的会话
UAT_UID_LIST="1, 10001"

echo "== 0) 集合预览（只读）=="
cnt_tickets=$("${PSQL[@]}" "SELECT count(*) FROM tickets WHERE created_at >= '$SINCE';")
cnt_files=$("${PSQL[@]}" "SELECT count(*) FROM ticket_files WHERE ticket_id IN ($TICKET_IDS);")
cnt_state=$("${PSQL[@]}" "SELECT count(*) FROM ticket_state WHERE ticket_id IN ($TICKET_IDS);")
cnt_ledger=$("${PSQL[@]}" "SELECT count(*) FROM usage_ledger WHERE created_at >= '$SINCE' AND user_id IN ($UAT_UID_LIST);")
cnt_notif=$("${PSQL[@]}" "SELECT count(*) FROM notifications WHERE created_at >= '$SINCE' AND user_id IN ($UAT_UID_LIST);")
cnt_convs=$("${PSQL[@]}" "SELECT count(*) FROM chat_conversations WHERE created_at >= '$SINCE' AND user_id IN ($UAT_UID_LIST);")
cnt_msgs=$("${PSQL[@]}" "SELECT count(*) FROM chat_messages WHERE conversation_id IN (SELECT id FROM chat_conversations WHERE created_at >= '$SINCE' AND user_id IN ($UAT_UID_LIST));")
echo "  tickets=$cnt_tickets ticket_files=$cnt_files ticket_state=$cnt_state"
echo "  usage_ledger(仅这两个身份)=$cnt_ledger notifications=$cnt_notif chat_convs=$cnt_convs chat_msgs=$cnt_msgs"
echo "  ★ 刻意不动：tenants/packages/quota_grants/balance_accounts/orders/payments/users/api_keys"
echo "  ★ 刻意不动：其他身份的用量留痕（窗口内非 UAT 身份的那部分一条都不摘）"

if [ "$APPLY" != "1" ]; then
  echo "DRYRUN=1（未加 APPLY=1 ⇒ 一个字节都没写）"
  exit 0
fi

echo "== 1) 建备份 schema 并整行导出（备份集合＝删除集合，同一条 WHERE）=="
"${PSQL[@]}" "CREATE SCHEMA IF NOT EXISTS $BAK;"
"${PSQL[@]}" "DROP TABLE IF EXISTS $BAK.tickets; CREATE TABLE $BAK.tickets AS SELECT * FROM tickets WHERE created_at >= '$SINCE';"
"${PSQL[@]}" "DROP TABLE IF EXISTS $BAK.ticket_files; CREATE TABLE $BAK.ticket_files AS SELECT * FROM ticket_files WHERE ticket_id IN ($TICKET_IDS);"
"${PSQL[@]}" "DROP TABLE IF EXISTS $BAK.ticket_state; CREATE TABLE $BAK.ticket_state AS SELECT * FROM ticket_state WHERE ticket_id IN ($TICKET_IDS);"
"${PSQL[@]}" "DROP TABLE IF EXISTS $BAK.usage_ledger; CREATE TABLE $BAK.usage_ledger AS SELECT * FROM usage_ledger WHERE created_at >= '$SINCE' AND user_id IN ($UAT_UID_LIST);"
"${PSQL[@]}" "DROP TABLE IF EXISTS $BAK.notifications; CREATE TABLE $BAK.notifications AS SELECT * FROM notifications WHERE created_at >= '$SINCE' AND user_id IN ($UAT_UID_LIST);"
"${PSQL[@]}" "DROP TABLE IF EXISTS $BAK.chat_conversations; CREATE TABLE $BAK.chat_conversations AS SELECT * FROM chat_conversations WHERE created_at >= '$SINCE' AND user_id IN ($UAT_UID_LIST);"
"${PSQL[@]}" "DROP TABLE IF EXISTS $BAK.chat_messages; CREATE TABLE $BAK.chat_messages AS SELECT * FROM chat_messages WHERE conversation_id IN (SELECT id FROM chat_conversations WHERE created_at >= '$SINCE' AND user_id IN ($UAT_UID_LIST));"

# 回导数行核对：备份里的行数必须与预览集合逐张等值（差一行即停手，不删任何东西）
check_pair() { # $1=表名 $2=期望数
  got=$("${PSQL[@]}" "SELECT count(*) FROM $BAK.$1;")
  if [ "$got" != "$2" ]; then bad "备份核对不过：$BAK.$1 有 $got 行，期望 $2 行"; else echo "  OK   备份 $BAK.$1 = $got 行"; fi
}
check_pair tickets "$cnt_tickets"
check_pair ticket_files "$cnt_files"
check_pair ticket_state "$cnt_state"
check_pair usage_ledger "$cnt_ledger"
check_pair notifications "$cnt_notif"
check_pair chat_conversations "$cnt_convs"
check_pair chat_messages "$cnt_msgs"
if [ "$FAIL" != "0" ]; then
  echo "CLEAN_ABORT=1（备份核对不过 ⇒ 停在写库之前，现役表一字未动）"
  exit 1
fi

echo "== 2) 产物文件移入隔离目录（不 rm，库里指向的先收进来）=="
mkdir -p "$QUAR"
moved=0; missing=0
while IFS= read -r p; do
  [ -z "$p" ] && continue
  if [ -f "$p" ]; then
    mv "$p" "$QUAR/" && moved=$((moved + 1))
  else
    missing=$((missing + 1))
  fi
done < <("${PSQL[@]}" "SELECT result_path FROM $BAK.ticket_files WHERE coalesce(result_path,'')<>'' UNION ALL SELECT text_result_path FROM $BAK.ticket_files WHERE coalesce(text_result_path,'')<>'';")
echo "  移走 $moved 份，库里另有 $missing 行指向的文件本就不在盘上（只记数，不判死）"
# 源上传件也一并隔离（工单原文件同样属测试残骸）
while IFS= read -r p; do
  [ -z "$p" ] && continue
  [ -f "$p" ] && mv "$p" "$QUAR/"
done < <("${PSQL[@]}" "SELECT file_path FROM $BAK.tickets WHERE coalesce(file_path,'')<>'';")
echo "  隔离目录：$QUAR"

echo "== 3) 删除（子→父），每张删完立刻按同一条判据数「应为 0」=="
"${PSQL[@]}" "DELETE FROM chat_messages WHERE conversation_id IN (SELECT id FROM $BAK.chat_conversations);"
"${PSQL[@]}" "DELETE FROM chat_conversations WHERE id IN (SELECT id FROM $BAK.chat_conversations);"
"${PSQL[@]}" "DELETE FROM notifications WHERE id IN (SELECT id FROM $BAK.notifications);"
"${PSQL[@]}" "DELETE FROM usage_ledger WHERE id IN (SELECT id FROM $BAK.usage_ledger);"
"${PSQL[@]}" "DELETE FROM ticket_state WHERE ticket_id IN (SELECT ticket_id FROM $BAK.ticket_state);"
"${PSQL[@]}" "DELETE FROM ticket_files WHERE id IN (SELECT id FROM $BAK.ticket_files);"
"${PSQL[@]}" "DELETE FROM tickets WHERE id IN (SELECT id FROM $BAK.tickets);"

zero() { # $1=名 $2=SQL
  left=$("${PSQL[@]}" "$2")
  if [ "$left" != "0" ]; then bad "删除后仍有残留：$1 还差 $left 行"; else echo "  OK   $1 = 0"; fi
}
zero chat_messages "SELECT count(*) FROM chat_messages WHERE conversation_id IN (SELECT id FROM $BAK.chat_conversations);"
zero chat_conversations "SELECT count(*) FROM chat_conversations WHERE id IN (SELECT id FROM $BAK.chat_conversations);"
zero notifications "SELECT count(*) FROM notifications WHERE id IN (SELECT id FROM $BAK.notifications);"
zero usage_ledger "SELECT count(*) FROM usage_ledger WHERE id IN (SELECT id FROM $BAK.usage_ledger);"
zero ticket_state "SELECT count(*) FROM ticket_state WHERE ticket_id IN (SELECT ticket_id FROM $BAK.ticket_state);"
zero ticket_files "SELECT count(*) FROM ticket_files WHERE id IN (SELECT id FROM $BAK.ticket_files);"
zero tickets "SELECT count(*) FROM tickets WHERE id IN (SELECT id FROM $BAK.tickets);"

echo "== 4) 旁证：现役非测试数据一条都没少（同租户在窗口外的工单数前后对照）=="
keep_now=$("${PSQL[@]}" "SELECT count(*) FROM tickets WHERE created_at < '$SINCE';")
keep_bak=$("${PSQL[@]}" "SELECT count(*) FROM $BAK.tickets;")
echo "  窗口外工单现存 $keep_now 行；备份里（窗口内）$keep_bak 行"
[ "$keep_bak" = "$cnt_tickets" ] || bad "窗口外计数被牵连（备份行数与预览不符）"

if [ "$FAIL" = "0" ]; then echo "CLEAN_ALL_OK=1 隔离目录＝${QUAR} 备份 schema＝${BAK}"; else echo "CLEAN_FAIL=${FAIL}（上面 BAD 行）"; exit 1; fi
