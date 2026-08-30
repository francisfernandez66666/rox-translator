#!/usr/bin/env bash
# ============ cutover-to-pg.sh · PostgreSQL 生产切流一键编排 ============
# 配套文档：改造方案_解锁PG与Redis及路线图.md（阶段一）
#
# 作用：在「停写维护窗口」内，将存量 SQLite（业务+KB 共用库）切流到托管 RDS PostgreSQL。
#  ① 备份源 SQLite + 目标 PG（pg_dump 空库）
#  ② 用 server --init-db 在 PG 建好 schema（含 pgvector 列）
#  ③ 用 migrate-sqlite-to-pg 拷贝全量数据（幂等，跳过 jobs/ticket_state/embedding）
#  ④ 用 rebuild-kb-index 回填 pgvector 向量，使语义检索生效
#  ⑤ 校验行数 + 提示切换 secrets.env 后重启
#
# 用法：
#   DB_DSN='postgres://user:pass@rds:5432/langcross?sslmode=require' \
#   SQLITE_PATH=/opt/translator/tm.sqlite3 \
#   ./cutover-to-pg.sh
#
# 前置：
#   - 已安装 psql / pg_dump 客户端，且能连通目标 PG
#   - 目标库已执行：CREATE EXTENSION IF NOT EXISTS vector;
#   - 服务二进制已 build（或本机有 go 工具链走 go run）
#   - 维护窗口内，翻译服务已停（避免切流期间写入 SQLite 后丢失）
# =============================================================
set -euo pipefail

: "${DB_DSN:?请设置 DB_DSN（PostgreSQL 连接串）}"
: "${SQLITE_PATH:?请设置 SQLITE_PATH（源 SQLite 文件路径）}"
: "${TRANSLATOR_BIN:=}"   # 可选：预编译二进制；留空则走 go run（需当前目录为 backend-go）

BACKUP_DIR="${BACKUP_DIR:-/opt/translator/backups/$(date +%Y%m%d_%H%M%S)}"
mkdir -p "$BACKUP_DIR"

log() { echo "[$(date '+%F %T')] $*"; }

# ---- 0. 选择运行方式（go run 或预编译二进制） ----
RUN() {
  if [ -n "$TRANSLATOR_BIN" ]; then
    "$TRANSLATOR_BIN" "$@"
  else
    (cd "$(dirname "$0")/../backend-go" && go run ./cmd/server "$@")
  fi
}
RUN_MIGRATE() {
  if [ -n "$TRANSLATOR_BIN" ]; then
    # migrate 工具未打包进 server 二进制；预编译场景需单独提供 migrate-sqlite-to-pg
    "${TRANSLATOR_BIN%.*}-migrate" "$@"
  else
    (cd "$(dirname "$0")/../backend-go" && go run ./cmd/migrate-sqlite-to-pg "$@")
  fi
}
RUN_REBUILD() {
  if [ -n "$TRANSLATOR_BIN" ]; then
    "${TRANSLATOR_BIN%.*}-rebuild" "$@"
  else
    (cd "$(dirname "$0")/../backend-go" && go run ./cmd/rebuild-kb-index "$@")
  fi
}

# ---- 1. 备份 ----
log "① 备份源 SQLite -> $BACKUP_DIR/tm.sqlite3.bak"
cp "$SQLITE_PATH" "$BACKUP_DIR/tm.sqlite3.bak"

PG_DUMP_BASE="${DB_DSN%%\?*}"  # 去掉 query 参数，pg_dump 单独传 sslmode
log "① 备份目标 PG 空库结构 -> $BACKUP_DIR/pg_pre.dump"
PGSSLMODE=require pg_dump "$PG_DUMP_BASE" --schema-only --no-owner > "$BACKUP_DIR/pg_pre.dump" 2>/dev/null || \
  log "   （pg_dump 失败可忽略：目标库可能为空，init-db 将建表）"

# ---- 2. 建 PG schema ----
log "② 在 PG 建 schema（server --init-db）"
DB_DRIVER=postgres DB_DSN="$DB_DSN" RUN --init-db
log "   schema 就绪"

# ---- 3. 迁数据 ----
log "③ 拷贝 SQLite -> PG（migrate-sqlite-to-pg，幂等）"
RUN_MIGRATE -sqlite "$SQLITE_PATH" -dsn "$DB_DSN"
log "   数据拷贝完成"

# ---- 4. 回填向量 ----
log "④ 回填 pgvector 向量（rebuild-kb-index）"
DB_DRIVER=postgres DB_DSN="$DB_DSN" RUN_REBUILD
log "   向量回填完成"

# ---- 5. 提示切换 ----
cat <<EOF

✅ 切流数据就绪。请在执行窗口内完成以下收尾：
  1) 在 /etc/translator/secrets.env 追加：
       DATABASE_DRIVER=postgres
       DATABASE_DSN='$DB_DSN'
  2) 重启服务：systemctl restart translator
  3) 冒烟：curl -s localhost:8787/status | grep dialect   # 应显示 postgres
  4) 提交一条翻译，确认日志出现 pgvector 语义检索命中

⚠️ 回滚：若 PG 异常，置空 DATABASE_DRIVER 重启即回 SQLite（切流前 SQLite 已备份于 $BACKUP_DIR/tm.sqlite3.bak）。
EOF
