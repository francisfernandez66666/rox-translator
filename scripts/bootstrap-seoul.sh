#!/usr/bin/env bash
# =============================================================================
# bootstrap-seoul.sh · 首尔 2G/双核 服务器「PostgreSQL + pgvector」一键初始化
# -----------------------------------------------------------------------------
# 适用：Debian / Ubuntu 系 VPS（首尔机房）。在其他发行版（CentOS/Alma/Rocky）需
#       自行替换包管理器与包名；脚本会在无法识别时退出并给出提示。
# 作用：
#   1) 安装 PostgreSQL（系统仓库版本）+ pgvector 扩展
#   2) 创建专用于翻译助手的 数据库 / 角色 / 密码，并启用 vector 扩展
#   3) 按 2G 内存 / 双核 CPU 写入保守性能参数（避免 OOM、保证稳定）
#   4) 打印后续 cutover-to-pg.sh 需要用的 DB_DSN
# 设计原则：幂等（重复执行安全），不触碰既有数据；仅做「最小可用」调优。
# 注意：本脚本只负责「数据库底座」，应用切流请用同仓 scripts/cutover-to-pg.sh。
# =============================================================================
set -euo pipefail

# ----------------------------- 可配置项（环境变量覆盖） -----------------------------
PG_DB="${PG_DB:-langcross}"
PG_USER="${PG_USER:-langcross}"
PG_PASS="${PG_PASS:-$(tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 24)}"
PG_PORT="${PG_PORT:-5432}"
# 2G 机器保守调优（shared_buffers 取物理内存 ~25%）
SHARED_BUFFERS="${SHARED_BUFFERS:-512MB}"
EFFECTIVE_CACHE="${EFFECTIVE_CACHE_SIZE:-1GB}"
MAX_CONN="${MAX_CONNECTIONS:-40}"
WORK_MEM="${WORK_MEM:-16MB}"
MAINT_MEM="${MAINTENANCE_WORK_MEM:-128MB}"

echo "==> [0/5] 运行环境检测"
if [ "$(id -u)" -ne 0 ]; then echo "❌ 请使用 root 或 sudo 运行"; exit 1; fi
if command -v apt-get >/dev/null 2>&1; then PKG=apt; else
  echo "❌ 未识别到 apt（仅支持 Debian/Ubuntu）。CentOS/Alma 请改用 dnf 安装 postgresql-server + pgvector 后手工执行第 2~4 步"; exit 2
fi

echo "==> [1/5] 安装 PostgreSQL + pgvector"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
# 优先装服务端；pgvector 包名随大版本变化，逐个尝试
apt-get install -y postgresql postgresql-contrib
PV_OK=0
for pv in $(apt-cache search '^postgresql-.*-pgvector$' 2>/dev/null | awk '{print $1}'); do
  apt-get install -y "$pv" && PV_OK=1 && echo "已安装 $pv" && break
done
if [ "$PV_OK" -eq 0 ]; then
  # 兜底：尝试与已装 PG 同大版本的 pgvector
  PGVER=$(pg_config --version 2>/dev/null | grep -oE '[0-9]+' | head -1 || echo 15)
  apt-get install -y "postgresql-${PGVER}-pgvector" || {
    echo "⚠️ 仓库无 pgvector 包；可改用 pgxn 安装："; echo "    apt-get install -y build-essential postgresql-server-dev-all; sudo -u postgres pgxn install vector"; }
fi

echo "==> [2/5] 启动并设置开机自启 PostgreSQL"
PGVER=$(ls /etc/postgresql | head -1)
PGDATA="/etc/postgresql/${PGVER}/main"
service postgresql start || systemctl enable --now postgresql

# ----------------------------- 创建角色 / 库 / 扩展 -----------------------------
echo "==> [3/5] 创建角色 ${PG_USER} / 库 ${PG_DB} / 扩展 vector"
# 用 postgres 超级用户执行；密码回显到控制台（仅此一次，请记录）
sudo -u postgres psql -v ON_ERROR_STOP=1 <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='${PG_USER}') THEN
    CREATE ROLE ${PG_USER} LOGIN PASSWORD '${PG_PASS}';
  ELSE
    ALTER ROLE ${PG_USER} WITH PASSWORD '${PG_PASS}';
  END IF;
END\$\$;
SQL

if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='${PG_DB}'" | grep -q 1; then
  sudo -u postgres createdb -O "${PG_USER}" "${PG_DB}"
fi
sudo -u postgres psql -d "${PG_DB}" -c "CREATE EXTENSION IF NOT EXISTS vector;"
echo "vector 扩展版本: $(sudo -u postgres psql -d "${PG_DB}" -tAc 'SELECT extversion FROM pg_extension WHERE extname=\$\$vector\$\$')"

# ----------------------------- 2G 调优 -----------------------------
echo "==> [4/5] 写入 2G/双核 性能参数 -> ${PGDATA}/conf.d/02-seoul-2g.conf"
mkdir -p "${PGDATA}/conf.d"
cat > "${PGDATA}/conf.d/02-seoul-2g.conf" <<CNF
# 翻译助手 · 首尔 2G/双核 保守调优（避免 OOM，优先稳定）
shared_buffers = ${SHARED_BUFFERS}
effective_cache_size = ${EFFECTIVE_CACHE}
max_connections = ${MAX_CONN}
work_mem = ${WORK_MEM}
maintenance_work_mem = ${MAINT_MEM}
random_page_cost = 1.1
checkpoint_completion_target = 0.9
wal_buffers = 16MB
default_statistics_target = 100
# pgvector 建 IVFFlat 索引时需要较大 maintenance_work_mem，已含于上方
CNF
# 允许密码登录（scram）；如仅需本机可保持 peer/本地 trust，这里放开 md5/scram
sed -i "s/^#*host\(.*\)all\(.*\)all\(.*\)/host    all             all             127.0.0.1\/32            scram-sha-256/" "${PGDATA}/pg_hba.conf" 2>/dev/null || true
service postgresql restart || systemctl restart postgresql
sleep 2
sudo -u postgres psql -tAc "SELECT version();" | head -1

# ----------------------------- 输出 -----------------------------
echo "==> [5/5] 生成 DB_DSN（供 cutover-to-pg.sh / 应用 env 使用）"
DSN="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/${PG_DB}?sslmode=disable"
echo "--------------------------------------------------------------"
echo "PG_DB    = ${PG_DB}"
echo "PG_USER  = ${PG_USER}"
echo "PG_PASS  = ${PG_PASS}   <-- 请妥善保存"
echo "DB_DSN   = ${DSN}"
echo "--------------------------------------------------------------"
echo "下一步（在本仓根目录执行切流脚本）："
echo "  export DB_DRIVER=postgres"
echo "  export DB_DSN='${DSN}'"
echo "  bash scripts/cutover-to-pg.sh"
echo "--------------------------------------------------------------"
