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
    # ★ 2026-09-16 修复：.timeout 5000 等 WAL 写锁——后端 reconciler/巡检在套件运行期
    #   仍持续写库，sqlite3 CLI 默认 busy timeout=0，撞锁瞬间报错输出空串，导致
    #   断言层取值为空、下游请求体拼出 {"id":,...} 这类非法 JSON（T43 曾因此误红）。
    sqlite3 -cmd '.timeout 5000' "${UAT_DB:?dbq: sqlite 需 UAT_DB}" "$1"
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

# dbjsonstr <表> <ID列值> <JSON列> <键> — 取 JSON 列中字符串键原值（缺失/空输出空串）。
dbjsonstr(){
  local tbl=$1 id=$2 col=$3 key=$4
  if [ "${DB_DRIVER:-sqlite}" = "postgres" ]; then
    dbq "SELECT COALESCE(NULLIF($col,'')::jsonb->>'$key','') FROM $tbl WHERE id=$id"
  else
    dbq "SELECT COALESCE(json_extract(NULLIF($col,''),'\$.$key'),'') FROM $tbl WHERE id=$id"
  fi
}

# dbjsonset <表> <ID列值> <JSON列> <键> <值> — 顶层键覆写（断言层专用注入）。
dbjsonset(){
  local tbl=$1 id=$2 col=$3 key=$4 val=$5
  if [ "${DB_DRIVER:-sqlite}" = "postgres" ]; then
    dbq "UPDATE $tbl SET $col=(COALESCE(NULLIF($col,''),'{}')::jsonb || '{\"$key\":\"$val\"}')::text WHERE id=$id" >/dev/null
  else
    dbq "UPDATE $tbl SET $col=json_set($col,'\$.$key','$val') WHERE id=$id" >/dev/null
  fi
}

# dbcfg <key> <value> — 幂等写 system_config（等价超管控制台配置）
# 失败即中止（exit 1）：配置写不进去整套件就在错误前提下跑——本次实测「防刷没放宽、
# 注册整片被限流」级联 22 条假红。静默继续比立刻红更有害，禁止改回「只打印不退出」。
dbcfg(){
  if [ "${DB_DRIVER:-sqlite}" = "postgres" ]; then
    psql "${DB_DSN:?dbcfg: 需 DB_DSN}" -q -c \
      "INSERT INTO system_config (key,value,updated_at) VALUES ('$1','$2',now()::text) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()::text" >/dev/null \
      || { echo "FATAL|dbcfg $1=$2 写入失败（postgres）"; exit 1; }
  else
    # ★ 2026-09-25（批G 步骤6 并入时踩出）：与 dbq 同口径带 .timeout 5000——
    #   dbcfg 在服务实例运行期写 system_config，撞 reconciler/巡检的 WAL 写锁时
    #   sqlite3 CLI 默认 busy timeout=0 直接「database is locked」静默失败，
    #   防刷放宽没落库 → 后续注册断言整片「注册过于频繁」级联假红（本次实测）。
    sqlite3 -cmd '.timeout 5000' "${UAT_DB:?dbcfg: 需 UAT_DB}" \
      "INSERT OR REPLACE INTO system_config (key,value,updated_at) VALUES ('$1','$2',datetime('now'));" \
      || { echo "FATAL|dbcfg $1=$2 写入失败（sqlite）"; exit 1; }
  fi
}
