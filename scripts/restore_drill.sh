#!/usr/bin/env bash
# =============================================================================
# restore_drill.sh · ★ S6 PG 备份恢复演练（生产预案唯一路径）
# 流程：pg_dump 源库 → 还原到一次性 scratch 库 → 关键表行数对账 → 清理。
# 用法：PGSRC=postgres://user@host/db bash scripts/restore_drill.sh
# 退出码非 0 = 演练失败（备份不可用/还原不可用），生产侧建议 cron 定期跑。
# =============================================================================
set -euo pipefail
PGSRC="${PGSRC:-postgres://${USER}@127.0.0.1:5432/translator_uat?sslmode=disable}"
PGADMIN="${PGDST_ADMIN:-postgres://${USER}@127.0.0.1:5432/postgres?sslmode=disable}"
SCRATCH="restore_drill_$(date +%s)"
DST="${PGADMIN%/postgres*}/$SCRATCH"
DST="${PGADMIN%/*}/$SCRATCH"
WORK="$(mktemp -d /tmp/restore-drill.XXXX)"
trap 'rm -rf "$WORK"; psql "$PGADMIN" -qc "DROP DATABASE IF EXISTS $SCRATCH" >/dev/null 2>&1 || true' EXIT

echo "[drill] 1/4 pg_dump $PGSRC"
pg_dump -Fc -d "$PGSRC" -f "$WORK/backup.dump"

echo "[drill] 2/4 创建 scratch 库 $SCRATCH 并 pg_restore"
psql "$PGADMIN" -qc "CREATE DATABASE $SCRATCH"
pg_restore -d "$DST" --no-owner "$WORK/backup.dump" 2>"$WORK/restore.log" || true
[ -s "$WORK/restore.log" ] && sed 's/^/[restore] /' "$WORK/restore.log" | head -5

echo "[drill] 3/4 关键表行数对账"
fail=0
for t in users tenants orders usage_ledger quota_grants tickets kb_packages; do
  src=$(psql "$PGSRC" -Atc "SELECT count(*) FROM $t" 2>/dev/null || echo ERR)
  dst=$(psql "$DST" -Atc "SELECT count(*) FROM $t" 2>/dev/null || echo ERR)
  if [ "$src" != "$dst" ]; then echo "  MISMATCH $t: src=$src dst=$dst"; fail=1; else echo "  ok  $t ($dst)"; fi
done
[ "$fail" = 0 ] || { echo "[drill] 对账失败"; exit 1; }
echo "[drill] 4/4 完成：备份可还原、行数一致（scratch 库与转储已清理）"
