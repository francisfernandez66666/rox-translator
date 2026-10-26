#!/usr/bin/env bash
# ============================================================================
# deploy/cleanup_uat_20261025.sh — 清理 2026-10-25 云端 UAT 测试数据（默认只读）
#
# 范围（用户 2026-10-26 授权“清理测试数据”）：
#   - 工单 123（文本三段）/ 124（文件 uat_sample_20261025.txt）及其
#     ticket_files / ticket_segments / ticket_state / notifications(ref=ticket) /
#     output_artifacts / 上传与产物文件；
#   - 可选聊天会话（CHAT_CONV_IDS=...，默认不删；先只读盘点再确认 id）；
#   - **不删 usage_ledger 财务流水**、不退款、不删任务奖励；保留 UAT 证据目录。
#
# 用法（在服务器上以 root 执行）：
#   bash deploy/cleanup_uat_20261025.sh                 # 只读盘点
#   APPLY=1 bash deploy/cleanup_uat_20261025.sh         # 备份 + 删除工单数据
#   APPLY=1 CHAT_CONV_IDS="id1,id2" bash deploy/cleanup_uat_20261025.sh
#
# 备份：schema fix_bak_20261025_uat（各表 *_bak）；回滚按 id 恢复。
# ============================================================================
set -uo pipefail
APPLY="${APPLY:-0}"
DB="${DB:-langcross}"
TICKET_IDS="${TICKET_IDS:-123,124}"
CHAT_CONV_IDS="${CHAT_CONV_IDS:-}"
Q() { sudo -u postgres psql -d "$DB" -Atc "$1"; }

echo "===== 1) 盘点（tickets=${TICKET_IDS}） ====="
Q "SELECT 'tickets='||count(*) FROM tickets WHERE id IN ($TICKET_IDS);"
Q "SELECT 'ticket_files='||count(*) FROM ticket_files WHERE ticket_id IN ($TICKET_IDS);"
Q "SELECT 'ticket_segments='||count(*) FROM ticket_segments WHERE ticket_id IN ($TICKET_IDS);"
Q "SELECT 'ticket_state='||count(*) FROM ticket_state WHERE ticket_id IN ($TICKET_IDS);"
Q "SELECT 'notifications='||count(*) FROM notifications WHERE ref_type='ticket' AND ref_id IN ($TICKET_IDS);"
Q "SELECT 'output_artifacts='||count(*) FROM output_artifacts WHERE ticket_id IN ($TICKET_IDS);"
Q "SELECT 'usage_ledger(保留)='||count(*) FROM usage_ledger WHERE tenant_id=1 AND created_at>='2026-10-25T15:00' AND created_at<='2026-10-26T01:00';"
if [ -n "$CHAT_CONV_IDS" ]; then
  Q "SELECT 'chat_messages='||count(*) FROM chat_messages WHERE conversation_id IN ('$(echo "$CHAT_CONV_IDS" | sed "s/,/','/g")');"
fi

if [ "$APPLY" != "1" ]; then
  echo "MODE=audit（未写库/未删文件）；确认后 APPLY=1 执行"
  exit 0
fi

echo "===== 2) 备份（schema fix_bak_20261025_uat） ====="
Q "CREATE SCHEMA IF NOT EXISTS fix_bak_20261025_uat;
DROP TABLE IF EXISTS fix_bak_20261025_uat.tickets_bak, fix_bak_20261025_uat.ticket_files_bak,
  fix_bak_20261025_uat.ticket_segments_bak, fix_bak_20261025_uat.ticket_state_bak,
  fix_bak_20261025_uat.notifications_bak, fix_bak_20261025_uat.output_artifacts_bak;
CREATE TABLE fix_bak_20261025_uat.tickets_bak AS SELECT * FROM tickets WHERE id IN ($TICKET_IDS);
CREATE TABLE fix_bak_20261025_uat.ticket_files_bak AS SELECT * FROM ticket_files WHERE ticket_id IN ($TICKET_IDS);
CREATE TABLE fix_bak_20261025_uat.ticket_segments_bak AS SELECT * FROM ticket_segments WHERE ticket_id IN ($TICKET_IDS);
CREATE TABLE fix_bak_20261025_uat.ticket_state_bak AS SELECT * FROM ticket_state WHERE ticket_id IN ($TICKET_IDS);
CREATE TABLE fix_bak_20261025_uat.notifications_bak AS SELECT * FROM notifications WHERE ref_type='ticket' AND ref_id IN ($TICKET_IDS);
CREATE TABLE fix_bak_20261025_uat.output_artifacts_bak AS SELECT * FROM output_artifacts WHERE ticket_id IN ($TICKET_IDS);" \
  || { echo "ABORT=2 备份失败，未执行任何删除"; exit 3; }

echo "===== 3) 产物/上传文件清单（删前落临时清单） ====="
Q "SELECT path FROM output_artifacts WHERE ticket_id IN ($TICKET_IDS)
   UNION SELECT file_path FROM ticket_files WHERE ticket_id IN ($TICKET_IDS)
   UNION SELECT result_path FROM ticket_files WHERE ticket_id IN ($TICKET_IDS) AND COALESCE(result_path,'')<>''
   UNION SELECT text_result_path FROM ticket_files WHERE ticket_id IN ($TICKET_IDS) AND COALESCE(text_result_path,'')<>'';" > /tmp/uat_cleanup_20261025_files.txt || true
wc -l /tmp/uat_cleanup_20261025_files.txt | sed 's/^/  files=/'

echo "===== 4) 删除（只按 ticket_id / 显式会话 id） ====="
Q "BEGIN;
DELETE FROM notifications WHERE ref_type='ticket' AND ref_id IN ($TICKET_IDS);
DELETE FROM ticket_segments WHERE ticket_id IN ($TICKET_IDS);
DELETE FROM ticket_state WHERE ticket_id IN ($TICKET_IDS);
DELETE FROM output_artifacts WHERE ticket_id IN ($TICKET_IDS);
DELETE FROM ticket_files WHERE ticket_id IN ($TICKET_IDS);
DELETE FROM tickets WHERE id IN ($TICKET_IDS);
COMMIT;" || { echo "ABORT=3 删除事务失败（回滚）"; exit 4; }

if [ -n "$CHAT_CONV_IDS" ]; then
  IDS=$(echo "$CHAT_CONV_IDS" | sed "s/,/','/g")
  Q "BEGIN; DELETE FROM chat_messages WHERE conversation_id IN ('$IDS'); DELETE FROM chat_conversations WHERE id IN ('$IDS'); COMMIT;" || { echo "ABORT=4 聊天删除失败"; exit 5; }
fi

echo "===== 5) 文件删除（仅删清单内文件；保留证据目录） ====="
if [ -s /tmp/uat_cleanup_20261025_files.txt ]; then
  while IFS= read -r p; do
    [ -n "$p" ] || continue
    case "$p" in
      /opt/translator/data/*) rm -f -- "$p" ;;
      *) echo "  SKIP 非 data 路径: $p" ;;
    esac
  done < /tmp/uat_cleanup_20261025_files.txt
fi

echo "===== 6) 回读 ====="
Q "SELECT 'tickets_remain='||count(*) FROM tickets WHERE id IN ($TICKET_IDS);"
Q "SELECT 'ledger_kept='||count(*) FROM usage_ledger WHERE tenant_id=1 AND created_at>='2026-10-25T15:00' AND created_at<='2026-10-26T01:00';"
echo "DONE=1 回滚：从 fix_bak_20261025_uat.*_bak 按 id 恢复"
