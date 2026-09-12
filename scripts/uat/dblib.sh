#!/usr/bin/env bash
# ============================================================================
# scripts/uat/dblib.sh — UAT 断言层双方言数据库访问（2026-09-12 PG 重研落地）
# 背景：断言层曾硬编码 sqlite3 CLI，导致「生产 PG 方言永远不被 UAT 覆盖」——
#       is_personal bool→INTEGER、JSON1 函数缺失等 PG 专属缺陷全部漏检。
# 用法：source scripts/uat/dblib.sh
# 环境变量：
#   DB_DRIVER  sqlite（默认）| postgres
#   UAT_DB     sqlite 库文件路径（DB_DRIVER=sqlite 时必填）
#   DB_DSN     postgres 连接串（DB_DRIVER=postgres 时必填）
# 约定：所有 SQL 用双引号包裹标识符（"left" 等），sqlite/PG 均兼容；
#       禁止在断言 SQL 中使用 datetime('now')/json_set 等方言专属函数（走 dbcfg 助手）。
# ============================================================================

# dbq <sql> — 执行查询/语句，输出未对齐单列值（sqlite3 / psql -Atc 等价语义）
dbq(){
  if [ "${DB_DRIVER:-sqlite}" = "postgres" ]; then
    psql "${DB_DSN:?dbq: DB_DRIVER=postgres 需 DB_DSN}" -q -Atc "$1"
  else
    sqlite3 "${UAT_DB:?dbq: sqlite 需 UAT_DB}" "$1"
  fi
}

# dbjson <表> <ID列值> <JSON列> <键> — 取某行 JSON 列中数值键（缺失按 0）。
# 断言层专用最小实现，与后端 db.JSONExtractNum 语义对齐（空串/NULL 均按 0）。
dbjson(){
  local tbl=$1 id=$2 col=$3 key=$4
  if [ "${DB_DRIVER:-sqlite}" = "postgres" ]; then
    dbq "SELECT COALESCE(NULLIF($col,'')::jsonb->>'$key','0')::numeric FROM $tbl WHERE id=$id"
  else
    dbq "SELECT COALESCE(json_extract(NULLIF($col,''),'\$.$key'),0) FROM $tbl WHERE id=$id"
  fi
}

# dbcfg <key> <value> — 幂等写 system_config（等价超管控制台配置）
dbcfg(){
  if [ "${DB_DRIVER:-sqlite}" = "postgres" ]; then
    psql "${DB_DSN:?dbcfg: 需 DB_DSN}" -q -c \
      "INSERT INTO system_config (key,value,updated_at) VALUES ('$1','$2',now()::text) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()::text" >/dev/null
  else
    sqlite3 "${UAT_DB:?dbcfg: 需 UAT_DB}" \
      "INSERT OR REPLACE INTO system_config (key,value,updated_at) VALUES ('$1','$2',datetime('now'));"
  fi
}
