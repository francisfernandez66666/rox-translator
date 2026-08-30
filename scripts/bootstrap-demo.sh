#!/usr/bin/env bash
# =============================================================================
# bootstrap-demo.sh · ROX 演示镜像一键部署（rox-test.lexicorn.cn）
# -----------------------------------------------------------------------------
# 目标：把现有首尔服务器上的生产 ROX 租户「拆分」出一个独立演示实例，
#       专门稳定给老板演示，且生产发版（替换 translator-server / restart
#       translator.service）完全不影响演示实例。
#
# 设计（同机独立实例）：
#   - 独立目录   /opt/translator-demo/            （bin / web / data 独立）
#   - 独立端口   127.0.0.1:8789                    （生产为 8787）
#   - 独立库     langcross_demo                    （从生产库 pg_dump 克隆）
#   - 独立服务   translator-demo.service           （生产为 translator.service）
#   - 独立域名   rox-test.lexicorn.cn  → Caddy → 8789
#   - 数据：完整克隆生产库（ROX 租户 + 全部演示数据），JWT_SECRET 复用生产
#     （保证库内 enc:v1: 加密密钥可解密、登录 token 可验证——镜像的本质）。
#
# 幂等：重复执行安全（已存在则更新二进制/前端/配置并重启，库克隆自动跳过）。
# 安全：演示环境不引入新的公网面（仅新增一个 Caddy 站点 + 内网端口）。
# =============================================================================
set -euo pipefail

# ----------------------------- 可配置项（环境变量覆盖） -----------------------------
DEMO_DOMAIN="${DEMO_DOMAIN:-rox-test.lexicorn.cn}"
DEMO_PORT="${DEMO_PORT:-8789}"
DEMO_PPROF="${DEMO_PPROF:-127.0.0.1:18788}"
DEMO_DIR="${DEMO_DIR:-/opt/translator-demo}"
DEMO_SVC="${DEMO_SVC:-translator-demo}"
DEMO_DB="${DEMO_DB:-langcross_demo}"
DEMO_USER_DATA="${DEMO_USER_DATA:-/opt/translator-demo/data}"

# 生产实例路径（脚本自动探测，通常无需覆盖）
PROD_DIR="${PROD_DIR:-/opt/translator}"
PROD_BIN="${PROD_BIN:-/opt/translator/bin/translator-server}"
PROD_WEB="${PROD_WEB:-/opt/translator/web}"
PROD_SVC="${PROD_SVC:-translator}"
PROD_DB="${PROD_DB:-langcross}"
PROD_DSN="${PROD_DSN:-}"       # 可选覆盖；缺省自动从 systemd/prod.conf/secrets 探测
PROD_SECRETS="${PROD_SECRETS:-/etc/translator/secrets.env}"
DEMO_SECRETS="${DEMO_SECRETS:-/etc/translator-demo/secrets.env}"

CADDY_MAIN="${CADDY_MAIN:-/etc/caddy/Caddyfile}"
CADDY_DEMO_CONF="${CADDY_DEMO_CONF:-/etc/caddy/translator-demo.conf}"

# 演示环境支付模式：mock（默认，演示环境无需真实收款，下单即自动到账，老板演示最顺）
DEMO_PAY_MODE="${DEMO_PAY_MODE:-mock}"

log() { echo "[$(date '+%F %T')] $*"; }
die() { echo "❌ $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "请使用 root 运行（需写 /etc/systemd /etc/caddy /opt 与 sudo -u postgres）"

# ----------------------------- 探测生产 DB_DSN -----------------------------
probe_prod_dsn() {
  [ -n "$PROD_DSN" ] && { echo "$PROD_DSN"; return; }
  # ① secrets.env 里的 DATABASE_DSN / DB_DSN
  if [ -f "$PROD_SECRETS" ]; then
    while IFS= read -r line; do
      case "$line" in
        DATABASE_DSN=*) echo "${line#DATABASE_DSN=}"; return ;;
        DB_DSN=*)       echo "${line#DB_DSN=}";       return ;;
      esac
    done < "$PROD_SECRETS"
  fi
  # ② systemd 当前 Environment
  if command -v systemctl >/dev/null 2>&1 && systemctl show "$PROD_SVC" >/dev/null 2>&1; then
    env=$(systemctl show "$PROD_SVC" -p Environment --value 2>/dev/null || true)
    for kv in $env; do
      case "$kv" in
        DATABASE_DSN=*) echo "${kv#DATABASE_DSN=}"; return ;;
        DB_DSN=*)       echo "${kv#DB_DSN=}";       return ;;
      esac
    done
  fi
  # ③ 部署指南：/opt/translator/pg_dsn.txt
  if [ -f /opt/translator/pg_dsn.txt ]; then
    tr -d '\r\n' < /opt/translator/pg_dsn.txt
    return
  fi
  echo ""
}

PROD_DSN="$(probe_prod_dsn)"
[ -n "$PROD_DSN" ] || die "无法探测生产 DB_DSN。请通过环境变量 PROD_DSN 提供，例如：
  PROD_DSN='postgres://langcross:<密码>@127.0.0.1:5432/langcross?sslmode=disable'"

# 从 DSN 提取生产库名（用于 pg_dump 源库）
PROD_DB_NAME="$(printf '%s' "$PROD_DSN" | sed -E 's#^postgres(ql)?://[^/]+/([^?]+).*#\2#' | sed 's/ *$//')"
[ -n "$PROD_DB_NAME" ] || PROD_DB_NAME="$PROD_DB"

# 演示 DSN：同角色、同主机、换库名（锚定库名后缀，避免误伤 host 中的路径成分）
DEMO_DSN="$(printf '%s' "$PROD_DSN" | sed -E "s|/(${PROD_DB_NAME})(\\?.*)?\$|/${DEMO_DB}\\2|")"

echo "========== ROX 演示镜像部署参数 =========="
echo "  域名    : ${DEMO_DOMAIN}"
echo "  端口    : 127.0.0.1:${DEMO_PORT}（生产 8787 不受影响）"
echo "  目录    : ${DEMO_DIR}"
echo "  服务    : ${DEMO_SVC}.service（生产 ${PROD_SVC}.service 不受影响）"
echo "  数据库  : ${DEMO_DB}（源：${PROD_DB_NAME}）"
echo "  生产DSN : ${PROD_DSN}"
echo "  演示DSN : ${DEMO_DSN}"
echo "==========================================="

# ----------------------------- 前置检查 -----------------------------
for c in psql pg_dump systemctl caddy; do
  command -v "$c" >/dev/null 2>&1 || die "缺少命令: $c（演示镜像依赖 PostgreSQL 客户端与 Caddy）"
done
systemctl is-active --quiet "$PROD_SVC" 2>/dev/null || \
  log "⚠️  生产服务 ${PROD_SVC} 未运行——仍将克隆数据库，但请确认生产数据是期望的最新快照"

# ----------------------------- 1. 目录与二进制/前端 -----------------------------
log "==> [1/6] 建立演示目录并复制二进制/前端（快照，发版不影响）"
mkdir -p "$DEMO_DIR/bin" "$DEMO_DIR/web" "$DEMO_USER_DATA"
[ -x "$PROD_BIN" ] || die "生产二进制不存在: $PROD_BIN"
cp -f "$PROD_BIN" "$DEMO_DIR/bin/translator-server"
chmod +x "$DEMO_DIR/bin/translator-server"
if [ -d "$PROD_WEB" ] && [ -f "$PROD_WEB/index.html" ]; then
  rm -rf "$DEMO_DIR/web"
  cp -r "$PROD_WEB" "$DEMO_DIR/web"
else
  log "⚠️  未找到生产前端目录 $PROD_WEB（或缺少 index.html），演示将仅提供 API；可稍后手动补充"
fi
# 运行账号（与生产一致 translator；目录归属一次性授权）
if ! id translator >/dev/null 2>&1; then
  useradd -r -s /usr/sbin/nologin translator || true
fi
chown -R translator:translator "$DEMO_DIR"
chmod -R o-rwx "$DEMO_DIR" 2>/dev/null || true
# ★ Caddy（caddy 用户）需能遍历 $DEMO_DIR 并读静态 web：
#   - bin/data 保持 o-rwx（安全）；仅 $DEMO_DIR 加 o+x 供遍历
#   - web 属主转 root:caddy 并对其他人开放 rX（否则主页/静态资源 403）
chmod o+x "$DEMO_DIR"
chown -R root:caddy "$DEMO_DIR/web" 2>/dev/null || chmod -R o+rX "$DEMO_DIR/web" || true

# ----------------------------- 2. 克隆数据库 -----------------------------
log "==> [2/6] 克隆生产库 ${PROD_DB_NAME} → ${DEMO_DB}（pg_dump + 恢复）"
# 演示库已存在则跳过（幂等）；不删旧库，避免误伤
if sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='${DEMO_DB}'" | grep -q 1; then
  log "   演示库 ${DEMO_DB} 已存在，跳过克隆（如需刷新请先 drop database ${DEMO_DB}）"
else
  sudo -u postgres createdb -O langcross "$DEMO_DB"
  sudo -u postgres psql -d "$DEMO_DB" -c "CREATE EXTENSION IF NOT EXISTS vector;"
  TMPDUMP=$(mktemp /tmp/langcross_demo_XXXX.sql)
  chmod 644 "$TMPDUMP"   # postgres 用户需可读（脚本以 root 运行，dump 由 root 写入）
  log "   pg_dump ${PROD_DB_NAME} ..."
  sudo -u postgres pg_dump "$PROD_DB_NAME" > "$TMPDUMP"
  log "   恢复至 ${DEMO_DB} ..."
  sudo -u postgres psql -v ON_ERROR_STOP=1 -d "$DEMO_DB" -f "$TMPDUMP" >/dev/null
  rm -f "$TMPDUMP"
  log "   克隆完成"
fi

# ----------------------------- 3. 演示库微调 -----------------------------
log "==> [3/6] 演示库配置微调（主域名 / 支付模式）"
# ① 主站域名指向演示域（品牌/Caddy on-demand ask 均按此放行）
sudo -u postgres psql -d "$DEMO_DB" -c "
INSERT INTO system_config (key, value, updated_at) VALUES ('primary_host','${DEMO_DOMAIN}', now())
ON CONFLICT (key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at;"
# ② 支付模式：演示环境默认 mock（下单自动到账，无需真实收款回调）
sudo -u postgres psql -d "$DEMO_DB" -c "
INSERT INTO system_config (key, value, updated_at) VALUES ('pay_mode','${DEMO_PAY_MODE}', now())
ON CONFLICT (key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at;"

# ----------------------------- 4. 演示 secrets -----------------------------
log "==> [4/6] 生成演示环境密钥文件 ${DEMO_SECRETS}（0600）"
mkdir -p "$(dirname "$DEMO_SECRETS")"
chmod 750 "$(dirname "$DEMO_SECRETS")"
# JWT_SECRET / ADMIN_TOKEN 复用生产（保证库内 enc:v1: 密文可解密、Caddy 注入一致）
# ★ ADMIN_INIT_PASSWORD 为启动强校验（REQUIRE_PROD_SECRETS=1）必需项；克隆库中 admin
#   账号已存在，EnsureAdmin 不会重置其密码，此值仅用于满足启动校验（登录仍用克隆账号密码）。
if [ -f "$PROD_SECRETS" ]; then
  JWT_SECRET_VAL="$(sed -n 's/^JWT_SECRET=//p' "$PROD_SECRETS" | head -1)"
  ADMIN_TOKEN_VAL="$(sed -n 's/^ADMIN_TOKEN=//p' "$PROD_SECRETS" | head -1)"
  METRICS_TOKEN_VAL="$(sed -n 's/^METRICS_TOKEN=//p' "$PROD_SECRETS" | head -1)"
  ADMIN_INIT_VAL="$(sed -n 's/^ADMIN_INIT_PASSWORD=//p' "$PROD_SECRETS" | head -1)"
else
  JWT_SECRET_VAL="${JWT_SECRET_VAL:-}"
  ADMIN_TOKEN_VAL="${ADMIN_TOKEN_VAL:-}"
  METRICS_TOKEN_VAL="${METRICS_TOKEN_VAL:-}"
  ADMIN_INIT_VAL="${ADMIN_INIT_VAL:-}"
fi
[ -n "$JWT_SECRET_VAL" ] || JWT_SECRET_VAL="$(openssl rand -hex 32)"
[ -n "$ADMIN_TOKEN_VAL" ] || ADMIN_TOKEN_VAL="$(openssl rand -hex 32)"
[ -n "$METRICS_TOKEN_VAL" ] || METRICS_TOKEN_VAL="$(openssl rand -hex 16)"
[ -n "$ADMIN_INIT_VAL" ] || ADMIN_INIT_VAL="$(openssl rand -hex 16)"

cat > "$DEMO_SECRETS" <<EOF
JWT_SECRET=${JWT_SECRET_VAL}
ADMIN_TOKEN=${ADMIN_TOKEN_VAL}
ADMIN_INIT_PASSWORD=${ADMIN_INIT_VAL}
METRICS_TOKEN=${METRICS_TOKEN_VAL}
EOF
chmod 600 "$DEMO_SECRETS"
chown root:translator "$DEMO_SECRETS" 2>/dev/null || true

# ----------------------------- 5. systemd 演示单元 -----------------------------
log "==> [5/6] 安装演示 systemd 单元 ${DEMO_SVC}.service"
UNIT="/etc/systemd/system/${DEMO_SVC}.service"
cat > "$UNIT" <<UNITEOF
[Unit]
Description=翻译助手 ROX 演示镜像 (${DEMO_DOMAIN})
After=network.target postgresql.service

[Service]
User=translator
Group=translator
WorkingDirectory=${DEMO_DIR}
EnvironmentFile=${DEMO_SECRETS}
Environment=REQUIRE_PROD_SECRETS=1
Environment=TRUST_PROXY_XFF=1
Environment=DB_DRIVER=postgres
Environment=DB_DSN=${DEMO_DSN}
Environment=USER_DATA_DIR=${DEMO_USER_DATA}
Environment=GOMEMLIMIT=450MiB
Environment=LLM_CHAT_CONCURRENT=1
Environment=LLM_EMBED_CONCURRENT=2
Environment=FILEPROC_MAX_CONCURRENT=1
Environment=WORKER_CONCURRENCY=1
Environment=PPROF_ADDR=${DEMO_PPROF}
ExecStart=${DEMO_DIR}/bin/translator-server -addr 127.0.0.1:${DEMO_PORT} -frontend ${DEMO_DIR}/web -kbdb ${DEMO_USER_DATA}/tm.sqlite3
Restart=always
RestartSec=3
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${DEMO_DIR}
MemoryMax=700M

[Install]
WantedBy=multi-user.target
UNITEOF
systemctl daemon-reload
systemctl enable --now "$DEMO_SVC"
systemctl restart "$DEMO_SVC"
log "   服务已启动（${DEMO_SVC}.service）"

# ----------------------------- 6. Caddy 站点 -----------------------------
log "==> [6/6] 配置 Caddy 站点 ${DEMO_DOMAIN} → 127.0.0.1:${DEMO_PORT}"
mkdir -p "$(dirname "$CADDY_DEMO_CONF")"
cat > "$CADDY_DEMO_CONF" <<CADDYEOF
# ============ ${DEMO_DOMAIN}（ROX 演示镜像，独立于生产 langcross） ============
# 由 bootstrap-demo.sh 生成。生产发版只动 translator.conf/translator.service，
# 本站点与 translator-demo.service 完全独立，不受发版影响。
${DEMO_DOMAIN} {
	encode gzip

	header {
		Strict-Transport-Security "max-age=31536000; includeSubDomains"
		X-Content-Type-Options "nosniff"
		X-Frame-Options "DENY"
		Referrer-Policy "strict-origin-when-cross-origin"
		Permissions-Policy "camera=(), microphone=(), geolocation=()"
		-Server
	}

	# 支付回调凭证注入（与生产共用同一 ADMIN_TOKEN，值见 /etc/default/caddy）
	handle /api/pay/notify/* {
		reverse_proxy 127.0.0.1:${DEMO_PORT} {
			header_up X-Admin-Token "{\$TRANSLATOR_ADMIN_TOKEN}"
		}
	}

	handle /api/* {
		reverse_proxy 127.0.0.1:${DEMO_PORT}
	}
	handle /status {
		reverse_proxy 127.0.0.1:${DEMO_PORT}
	}
	handle /openapi/* {
		reverse_proxy 127.0.0.1:${DEMO_PORT}
	}
	handle /office/* {
		reverse_proxy 127.0.0.1:${DEMO_PORT}
	}
	handle /pricing {
		reverse_proxy 127.0.0.1:${DEMO_PORT}
	}
	handle /docs/* {
		reverse_proxy 127.0.0.1:${DEMO_PORT}
	}

	handle /assets/* {
		root * ${DEMO_DIR}/web
		header Cache-Control "public, max-age=31536000, immutable"
		file_server
	}

	handle {
		root * ${DEMO_DIR}/web
		try_files {path} /index.html
		header Cache-Control "no-cache, must-revalidate"
		file_server
	}

	request_body {
		max_size 10MB
	}
}
CADDYEOF
chmod 644 "$CADDY_DEMO_CONF"

# 在主 Caddyfile 引入演示 conf（幂等，去重）
if [ -f "$CADDY_MAIN" ]; then
  if ! grep -q "translator-demo.conf" "$CADDY_MAIN"; then
    cp "$CADDY_MAIN" "${CADDY_MAIN}.bak-$(date +%Y%m%d_%H%M%S)"
    printf '\n# ===== ROX 演示镜像 =====\nimport %s\n' "$CADDY_DEMO_CONF" >> "$CADDY_MAIN"
  fi
else
  log "⚠️  未找到主 Caddyfile ${CADDY_MAIN}——请手动在主 Caddyfile 追加: import ${CADDY_DEMO_CONF}"
fi
# ★ 不要用 root 运行 caddy validate（会以 root 创建 /var/log/caddy/translator.access.log，
#   导致 Caddy 服务因权限拒绝而无法启动）。改以 caddy 用户校验；失败仅告警不阻断。
if command -v sudo >/dev/null 2>&1 && id caddy >/dev/null 2>&1; then
  if sudo -u caddy caddy validate --config "$CADDY_MAIN" >/dev/null 2>&1; then
    systemctl reload caddy || systemctl restart caddy || true
  else
    log "⚠️  Caddy 校验失败，演示域名暂不对外；请检查 ${CADDY_MAIN}"
  fi
else
  systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null || \
    log "⚠️  无法重载 Caddy，请手动执行 systemctl reload caddy"
fi

# ----------------------------- 冒烟验证 -----------------------------
sleep 3
echo ""
echo "========== 冒烟验证 =========="
curl -s -o /dev/null -w "  /api/health(本机)      -> %{http_code}\n" "http://127.0.0.1:${DEMO_PORT}/api/health" || echo "  /api/health 失败"
curl -s "http://127.0.0.1:${DEMO_PORT}/status" | grep -o '"dialect":"[^"]*"' | sed 's/^/  dialect              -> /' || true
curl -s -o /dev/null -w "  https://${DEMO_DOMAIN}/api/health -> %{http_code}\n" --max-time 15 "https://${DEMO_DOMAIN}/api/health" || echo "  ⚠️  公网验证失败（证书签发/解析可能滞后，稍后重试）"
echo "================================="
cat <<EOF

✅ ROX 演示镜像部署完成！
------------------------------------------------------------
  访问地址 : https://${DEMO_DOMAIN}
  演示登录 : 使用生产超管账号 / ROX 租户既有账号（数据已克隆）
  服务管理 : systemctl status ${DEMO_SVC}   /   journalctl -u ${DEMO_SVC}
  数据刷新 : sudo -u postgres dropdb ${DEMO_DB} && 重跑本脚本（会重新克隆）
  DNS 要求 : 确保 ${DEMO_DOMAIN} 的 A 记录指向本机（通配符 *.lexicorn.cn 已指向本机则无需处理）
  发版隔离 : 生产发版仅替换 ${PROD_BIN} 与 restart ${PROD_SVC}，与本演示实例完全无关
------------------------------------------------------------
EOF
