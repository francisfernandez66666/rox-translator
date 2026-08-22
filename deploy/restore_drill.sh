#!/bin/bash
# ============================================================================
# restore_drill.sh — 备份恢复演练脚本（容灾验证）
#
# 用途：取最新一份 VACUUM INTO 备份，在临时目录做完整性检查与行数抽样，
#       验证备份真实可恢复（同机备份≠容灾，演练通过才算数）。
# 用法：./deploy/restore_drill.sh [备份目录] [库文件名]
#   备份目录 默认 /tmp/translator_backup（或部署时配置的 backup_dir）
#   库文件名 默认 translator.db
# 退出码：0=演练通过；1=无备份/损坏
# ============================================================================
set -uo pipefail

BACKUP_DIR="${1:-/tmp/translator_backup}"
DB_NAME="${2:-translator.db}"

echo "==> 恢复演练开始：$BACKUP_DIR/$DB_NAME"

LATEST=$(ls -t "$BACKUP_DIR"/${DB_NAME}.bak_* "$BACKUP_DIR"/${DB_NAME}_*.db \
         "$BACKUP_DIR"/${DB_NAME} 2>/dev/null | head -1)
if [ -z "$LATEST" ]; then
  echo "✖ 未找到备份文件于 $BACKUP_DIR"; exit 1
fi
echo "==> 最新备份: $LATEST ($(du -h "$LATEST" | cut -f1))"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cp "$LATEST" "$WORK/restored.db"

# 1. SQLite 完整性检查（快速 + 完整）
INTEGRITY=$(sqlite3 "$WORK/restored.db" "PRAGMA integrity_check;" 2>&1)
if [ "$INTEGRITY" != "ok" ]; then
  echo "✖ 完整性检查失败: $INTEGRITY"; exit 1
fi
echo "✔ 完整性检查通过 (integrity_check=ok)"

# 2. 关键表行数抽样（表不存在视为旧版本结构，仅提示）
for T in tenants users tickets orders usage_ledger audit_logs kb_entries jobs notifications; do
  CNT=$(sqlite3 "$WORK/restored.db" "SELECT COUNT(*) FROM $T;" 2>/dev/null || echo "N/A")
  printf "    %-14s %s 行\n" "$T" "$CNT"
done

# 3. 可写性冒烟：临时副本上执行一次写入并回滚
sqlite3 "$WORK/restored.db" "BEGIN; CREATE TABLE IF NOT EXISTS _drill(x INT); INSERT INTO _drill VALUES(1); ROLLBACK;" \
  && echo "✔ 恢复副本可正常读写" || { echo "✖ 恢复副本不可写"; exit 1; }

echo "==> ✅ 恢复演练通过：备份可恢复。建议每季度执行一次并异地保存输出。"
