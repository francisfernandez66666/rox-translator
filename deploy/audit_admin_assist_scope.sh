#!/usr/bin/env bash
# ============================================================================
# deploy/audit_admin_assist_scope.sh — Q9／D-13 旧面收严前的**只读盘点**（2026-10-30 三事合一批 组7 §8.4）
#
# 背景：旧面 `/api/admin/assist/*` 的鉴权已按用户口径收严到 `auth.IsSuperAdmin`
# （等级≥4 **且** tenant_id=0）。这是一次**功能收窄**：被打掉的是「role_level≥4 但 tenant_id≠0」
# 的账号。收严前必须先盘点这类账号有几个、近 90 天有没有真的用过旧面；盘点结果由用户拍板，
# 本脚本**只读不写、不改任何判据**。
#
# 用法（部署机，与 audit_culture_en_20261024.sh 同口径）：
#   bash deploy/audit_admin_assist_scope.sh
#   DB=langcross bash deploy/audit_admin_assist_scope.sh
#   DB_DRIVER=sqlite SQLITE_DB=/opt/translator/data/app.db bash deploy/audit_admin_assist_scope.sh
#   PSQL='psql -h 127.0.0.1 -U translator -d langcross -Atc' bash deploy/audit_admin_assist_scope.sh
#
# 三态退出码（与仓内其他盘点脚本同一口径，别把它读成两态）：
#   0 = 干净（两类命中都为 0）
#   1 = 有命中（清单已打印，交用户决定收严还是保留）
#   2 = 没测到（库连不上／查询失败／读到的不是数字）——**不是通过，也不是产品缺陷**
#
# 判据刻意「现读现推」：
#   - 等级口径不写成 role_level 列（库里没有这一列，它由 auth.RoleLevel 现算），
#     这里按同一张映射表用 role 枚举表达：super_admin/admin ⇒ 4。
#   - 审计动作名从代码抄：internal/api/admin_assist_proxy.go 写的是 `assist_admin_write`。
#     动作名改名时这里必须同步（否则第二段会恒 0，把「没人用过」读成「不需要收严」）。
# ============================================================================
set -uo pipefail

DB="${DB:-langcross}"
DB_DRIVER="${DB_DRIVER:-postgres}"
PSQL_CMD="${PSQL:-sudo -u postgres psql -d $DB -Atc}"

# run_sql 执行一条只读 SQL，stdout 即结果。失败返回非零（调用方据此判 exit 2）。
run_sql() {
  if [ "$DB_DRIVER" = "sqlite" ]; then
    local path="${SQLITE_DB:-/opt/translator/data/app.db}"
    if [ ! -r "$path" ]; then
      echo "sqlite db not readable: $path" >&2
      return 1
    fi
    sqlite3 -readonly "$path" "$1"
    return $?
  fi
  $PSQL_CMD "$1"
}

# 90 天前的 RFC3339 边界：两种方言各一条（created_at 是 TEXT/RFC3339 字典序比较）。
if [ "$DB_DRIVER" = "sqlite" ]; then
  SINCE_SQL="strftime('%Y-%m-%dT%H:%M:%S', datetime('now','-90 days'))"
else
  SINCE_SQL="to_char(now() - interval '90 days', 'YYYY-MM-DD\"T\"HH24:MI:SS')"
fi

echo "===== 1) 等级≥4 且 tenant_id≠0 的账号（收严会当场摘掉这些人的旧面入口） ====="
# 等级映射与 auth.RoleLevel 同源（super_admin/admin=4）。租户管理员 tenant_admin 是 3，不在本清单。
N_ACCOUNTS="$(run_sql "SELECT COUNT(*) FROM users WHERE role IN ('super_admin','admin') AND tenant_id<>0;" 2>/dev/null)" || {
  echo "ABORT=1 查询失败（账号清单）"; echo "MEASURED=0"; exit 2; }
case "$N_ACCOUNTS" in
  ''|*[!0-9]*) echo "ABORT=2 读数不是数字：[$N_ACCOUNTS]"; echo "MEASURED=0"; exit 2 ;;
esac
if [ "$N_ACCOUNTS" -gt 0 ]; then
  run_sql "SELECT id||'|'||tenant_id||'|'||username||'|'||COALESCE(display_name,'')||'|'||role||'|'||COALESCE(status,'') FROM users WHERE role IN ('super_admin','admin') AND tenant_id<>0 ORDER BY id;" 2>/dev/null || {
    echo "ABORT=3 清单读取失败"; echo "MEASURED=0"; exit 2; }
fi

echo "===== 2) 近 90 天旧面写操作审计（action=assist_admin_write） ====="
# 只统计**写**：GET 不进审计（见 admin_assist_proxy.go 的 LogAudit 位置），
# 所以这里出的是"真的动过挂件话术/知识库"的人，比"点开过面板"更有判别力。
N_AUDIT="$(run_sql "SELECT COUNT(*) FROM audit_logs WHERE action='assist_admin_write' AND created_at >= ${SINCE_SQL};" 2>/dev/null)" || {
  echo "ABORT=4 查询失败（审计）"; echo "MEASURED=0"; exit 2; }
case "$N_AUDIT" in
  ''|*[!0-9]*) echo "ABORT=5 读数不是数字：[$N_AUDIT]"; echo "MEASURED=0"; exit 2 ;;
esac
if [ "$N_AUDIT" -gt 0 ]; then
  run_sql "SELECT a.user_id||'|'||COALESCE(u.tenant_id,-1)||'|'||COALESCE(u.role,'')||'|'||COUNT(*)||'|'||MAX(a.created_at) FROM audit_logs a LEFT JOIN users u ON u.id=a.user_id WHERE a.action='assist_admin_write' AND a.created_at >= ${SINCE_SQL} GROUP BY a.user_id, u.tenant_id, u.role ORDER BY COUNT(*) DESC;" 2>/dev/null || {
    echo "ABORT=6 明细读取失败"; echo "MEASURED=0"; exit 2; }
fi

echo "----- 汇总 -----"
echo "accounts_level4_nonzero_tenant=$N_ACCOUNTS"
echo "assist_admin_writes_90d=$N_AUDIT"
echo "MEASURED=1"
if [ "$N_ACCOUNTS" -eq 0 ] && [ "$N_AUDIT" -eq 0 ]; then
  echo "VERDICT=clean（可把旧面判据收严到 IsSuperAdmin：没有账号会被摘掉）"
  exit 0
fi
echo "VERDICT=has_findings（先交用户拍板：收严＝摘掉上面这些账号的入口，属功能回退不是修 bug）"
exit 1
