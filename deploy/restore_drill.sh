#!/bin/bash
# ============================================================================
# restore_drill.sh — 备份恢复演练脚本（容灾验证，★ 双方言版 2026-09-15）
#
# 用途：取最新一份备份，在隔离环境做「真实还原 → 完整性检查 → 行数抽样」，
#       验证备份可用（同机备份≠容灾，演练通过才算数）。
# 方言自动判定（P1-2 核实修正 + P2 补齐）：
#   - 备份目录存在 *.bak.dump → PostgreSQL 演练：建临时库 translator_drill_<ts>，
#     pg_restore 全量还原 → 行数抽样 → 删临时库（生产 PG 唯一方言的主路径）；
#     旧版仅支持 SQLite VACUUM INTO，生产 PG 部署从未被演练覆盖——本分支补齐。
#   - 否则按 SQLite：拷贝副本 → PRAGMA integrity_check → 行数抽样（原行为不变）。
# 用法：
#   ./deploy/restore_drill.sh [备份目录] [库名前缀|translator.db]
#   PG 演练需环境变量：DRILL_PG_ADMIN_DSN（具备建库权限的管理连接串，
#     如 postgres://user@127.0.0.1:5432/postgres?sslmode=disable）
# 失败告警（P2：restore_drill 失败进告警收口，2026-09-15）：
#   环境变量 ALERT_INTAKE_URL（如 http://127.0.0.1:8787/api/alerts/alertmanager）
#   + ALERT_TOKEN（平台 ADMIN_TOKEN）时，任何非零退出自动 POST firing 告警
#   （Alertmanager 兼容载荷，经 S9 收口落平台告警中心并推运营群）。
#   systemd timer 场景配合 OnFailure= 亦可，但脚本自带告警保证「手工跑也不漏报」。
# 退出码：0=演练通过；1=无备份/还原失败/校验失败
# 定时：deploy/systemd/translator-restore-drill.timer（每周日 03:30）
# ============================================================================
set -uo pipefail

BACKUP_DIR="${1:-/tmp/translator_backup}"
DB_NAME="${2:-translator.db}"

# alert_intake 失败告警上报（尽力而为：告警通道故障不影响脚本退出码语义）。
alert_intake() {
  local summary="$1"
  [ -z "${ALERT_INTAKE_URL:-}" ] && return 0
  local tok="${ALERT_TOKEN:-${ADMIN_TOKEN:-}}"
  [ -z "$tok" ] && { echo "（跳过告警上报：未配置 ALERT_TOKEN/ADMIN_TOKEN）"; return 0; }
  local now
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  # Alertmanager webhook 兼容载荷：labels.alertname=RestoreDrillFailure（S9 收口规则按 prom: 前缀落库）
  local body
  body=$(printf '{"alerts":[{"status":"firing","labels":{"alertname":"RestoreDrillFailure","severity":"critical"},"annotations":{"summary":"备份恢复演练失败","description":"%s"},"startsAt":"%s"}]}' "$summary" "$now")
  curl -s -m 5 -XPOST "$ALERT_INTAKE_URL" -H "Content-Type: application/json" -H "X-Admin-Token: $tok" -d "$body" >/dev/null 2>&1 \
    && echo "==> 已上报演练失败告警（S9 收口）" \
    || echo "==> ⚠️ 演练失败告警上报未成功（请人工跟进）"
}
# fail 统一出口：打印 → 上报 → 退出
fail() { echo "✖ $*"; alert_intake "$*"; exit 1; }

echo "==> 恢复演练开始：$BACKUP_DIR/$DB_NAME"

# ---------- 方言判定：优先找 PG 备份（*.bak.dump），回退 SQLite ----------
LATEST_PG=$(ls -t "$BACKUP_DIR"/*.bak.dump 2>/dev/null | head -1)
if [ -n "${DRILL_PG_ADMIN_DSN:-}" ] && [ -n "$LATEST_PG" ]; then
  # ==================== PostgreSQL 演练分支 ====================
  echo "==> 方言：PostgreSQL（最新备份 ${LATEST_PG}，$(du -h "$LATEST_PG" | cut -f1)）"
  TS=$(date +%Y%m%d_%H%M%S)
  SCRATCH="translator_drill_${TS}"
  ADMIN_DSN="$DRILL_PG_ADMIN_DSN"
  # 临时库连接串：仅替换库名段（保留 user/host/query），避免 %/* 误伤协议双斜杠
  SCRATCH_DSN=$(echo "$ADMIN_DSN" | sed -E "s|/[^/?]+(\?|\$)|/$SCRATCH\1|")
  # ① 建临时库（模板库拷贝，秒级）
  psql "$ADMIN_DSN" -q -c "CREATE DATABASE $SCRATCH" || fail "PG 建临时演练库失败（检查 DRILL_PG_ADMIN_DSN 建库权限）"
  # ② 全量还原：pg_restore 真实执行全部 DDL/DATA（失败=备份不可用）
  #    -e 遇错即停；--no-owner 与备份口径一致；扩展缺失（vector）容忍为已知环境差异仅告警。
  RESTORE_OUT=$(pg_restore -d "$SCRATCH_DSN" --no-owner -e "$LATEST_PG" 2>&1)
  RESTORE_RC=$?
  if [ $RESTORE_RC -ne 0 ]; then
    psql "$ADMIN_DSN" -q -c "DROP DATABASE IF EXISTS $SCRATCH WITH (FORCE)" >/dev/null 2>&1
    echo "$RESTORE_OUT" | tail -5
    fail "pg_restore 还原失败（exit=$RESTORE_RC）：备份不完整或与目标库版本不兼容"
  fi
  echo "✔ pg_restore 全量还原成功（DDL+DATA 无致命错误）"
  # ③ 关键表行数抽样（0 行且业务应有数据的表给出提示；表缺失视为还原不完整直接判失败）
  MISS=0
  for T in tenants users tickets orders usage_ledger audit_logs kb_entries jobs notifications; do
    CNT=$(psql "$SCRATCH_DSN" -t -A -c "SELECT COUNT(*) FROM $T" 2>/dev/null) || { echo "    $T 表不存在"; MISS=1; continue; }
    printf "    %-14s %s 行\n" "$T" "$CNT"
  done
  # ④ 可写性冒烟：临时库上执行一次写入事务并回滚
  psql "$SCRATCH_DSN" -q -c "BEGIN; CREATE TABLE IF NOT EXISTS _drill(x INT); INSERT INTO _drill VALUES(1); ROLLBACK;" >/dev/null 2>&1 \
    || { psql "$ADMIN_DSN" -q -c "DROP DATABASE IF EXISTS $SCRATCH WITH (FORCE)" >/dev/null 2>&1; fail "恢复副本不可读写"; }
  echo "✔ 恢复副本可正常读写"
  # ⑤ 清理临时库（无论成败都尽力删除，防磁盘堆积）
  psql "$ADMIN_DSN" -q -c "DROP DATABASE IF EXISTS $SCRATCH WITH (FORCE)" >/dev/null 2>&1
  [ $MISS -eq 1 ] && fail "pg_restore 后关键表有缺失，备份不可信"
  echo "==> ✅ 恢复演练通过（PG）：备份可全量还原。输出已异地保存则最佳。"
  exit 0
fi

# ==================== SQLite 演练分支（原行为） ====================
LATEST=$(ls -t "$BACKUP_DIR"/${DB_NAME}.bak_* "$BACKUP_DIR"/${DB_NAME}_*.db \
         "$BACKUP_DIR"/${DB_NAME} 2>/dev/null | head -1)
[ -n "$LATEST" ] || fail "未找到备份文件于 $BACKUP_DIR（SQLite 模式；PG 部署请配 DRILL_PG_ADMIN_DSN）"
echo "==> 方言：SQLite（最新备份 $LATEST，$(du -h "$LATEST" | cut -f1)）"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cp "$LATEST" "$WORK/restored.db"

# 1. SQLite 完整性检查（快速 + 完整）
INTEGRITY=$(sqlite3 "$WORK/restored.db" "PRAGMA integrity_check;" 2>&1)
[ "$INTEGRITY" = "ok" ] || fail "完整性检查失败: $INTEGRITY"
echo "✔ 完整性检查通过 (integrity_check=ok)"

# 2. 关键表行数抽样（表不存在视为旧版本结构，仅提示）
for T in tenants users tickets orders usage_ledger audit_logs kb_entries jobs notifications; do
  CNT=$(sqlite3 "$WORK/restored.db" "SELECT COUNT(*) FROM $T;" 2>/dev/null || echo "N/A")
  printf "    %-14s %s 行\n" "$T" "$CNT"
done

# 3. 可写性冒烟：临时副本上执行一次写入并回滚
sqlite3 "$WORK/restored.db" "BEGIN; CREATE TABLE IF NOT EXISTS _drill(x INT); INSERT INTO _drill VALUES(1); ROLLBACK;" \
  && echo "✔ 恢复副本可正常读写" || fail "恢复副本不可写"

echo "==> ✅ 恢复演练通过（SQLite）：备份可恢复。建议每季度执行一次并异地保存输出。"
