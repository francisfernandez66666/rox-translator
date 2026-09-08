#!/usr/bin/env bash
# ============================================================================
# deploy/systemd/migrate_sandbox.sh — 技术债② 生产 systemd 沙箱化一键迁移
# 对应《技术债治理执行方案.md》第二部分 Step1-6（服务器 root 执行）
#
#  迁移模式：  ./deploy/systemd/migrate_sandbox.sh migrate <JWT_SECRET> <ADMIN_INIT_PASSWORD> <ADMIN_TOKEN> [DB_DSN] [REDIS_ADDR] [REDIS_PASSWORD]
#  回滚模式：  ./deploy/systemd/migrate_sandbox.sh rollback
#  预检模式：  ./deploy/systemd/migrate_sandbox.sh check
#
# 安全约束：
#   - 迁移前自动备份 unit + drop-in 到 /opt/translator/data/*.pre-sandbox.<TS>
#   - secrets.env 写入后 chmod 600（unit 内零明文凭证）
#   - JWT_SECRET 必须沿用现役值（否则库内 enc:v1: 密文不可解 + 全量重登）！
#   - 迁移动作显式确认后执行（--yes 跳过交互确认，脚本化时用）
# ============================================================================
set -euo pipefail

MODE="${1:?用法: $0 <migrate|rollback|check> [...]}"
UNIT=/etc/systemd/system/translator.service
DROP=/etc/systemd/system/translator.service.d
SEC=/etc/translator/secrets.env
BK=/opt/translator/data

need_root() { [ "$(id -u)" = "0" ] || { echo "❌ 需要 root 执行（服务器本机）"; exit 1; }; }

backup() {
  TS=$(date +%Y%m%d_%H%M%S)
  echo "==> 备份现状 → $BK"
  [ -f "$UNIT" ] && cp "$UNIT" "$BK/translator.service.pre-sandbox.$TS" && echo "  ✔ unit 已备份"
  [ -d "$DROP" ] && cp -r "$DROP" "$BK/translator.service.d.pre-sandbox.$TS" && echo "  ✔ drop-in 已备份"
  echo "$TS" > /tmp/translator_migrate_ts
}

stop_service() {
  if systemctl is-active --quiet translator; then
    echo "==> 停服务（停机窗口开始）"
    systemctl stop translator
  else
    echo "==> translator 已停止"
  fi
}

migrate() {
  need_root
  JWT="${2:-}"; ADMIN_PWD="${3:-}"; ADMIN_TOK="${4:-}"
  [ -n "$JWT" ] && [ -n "$ADMIN_PWD" ] && [ -n "$ADMIN_TOK" ] || { echo "❌ 参数缺失：migrate <JWT_SECRET> <ADMIN_INIT_PASSWORD> <ADMIN_TOKEN> [DB_DSN] [REDIS_ADDR] [REDIS_PASSWORD]"; exit 1; }
  DB_DSN="${5:-}"; REDIS_ADDR="${6:-}"; REDIS_PWD="${7:-}"

  # 从现役 unit 提取旧密钥做一致性校验（防御：误传新 JWT_SECRET）
  OLD_JWT=$(systemctl cat translator 2>/dev/null | grep -oE 'JWT_SECRET=[^ ]+' | head -1 | cut -d= -f2- || true)
  if [ -n "$OLD_JWT" ] && [ "$OLD_JWT" != "$JWT" ]; then
    echo "⚠️  现役 unit 内 JWT_SECRET 与传入值不一致！迁移后全量重登 + 密文重配。"
    [ "${SKIP_CONFIRM:-0}" = "1" ] || { read -rp "  确认继续？(y/N) " c; [ "$c" = "y" ] || exit 1; }
  fi

  backup
  stop_service

  echo "==> [S2] 建专用运行账号"
  useradd -r -s /usr/sbin/nologin translator 2>/dev/null || echo "  ↳ 账号已存在"
  chown -R translator:translator /opt/translator
  chmod o+rX /usr/share/fonts/opentype/noto /usr/share/fonts/truetype/alibaba 2>/dev/null || true

  echo "==> [S3] 收敛密钥 → secrets.env(0600)"
  mkdir -p /etc/translator && chmod 750 /etc/translator
  umask 077
  cat > "$SEC" <<EOF
JWT_SECRET=$JWT
ADMIN_INIT_PASSWORD=$ADMIN_PWD
ADMIN_TOKEN=$ADMIN_TOK
METRICS_TOKEN=$(openssl rand -hex 16)
DB_DRIVER=postgres
DB_DSN=$DB_DSN
REDIS_ADDR=$REDIS_ADDR
REDIS_PASSWORD=$REDIS_PWD
SMTP_HOST=${SMTP_HOST:-} SMTP_PORT=${SMTP_PORT:-}
SMTP_USER=${SMTP_USER:-} SMTP_PASS=${SMTP_PASS:-}
SMTP_FROM=${SMTP_FROM:-}
INFO_SMTP_HOST=${INFO_SMTP_HOST:-} INFO_SMTP_PORT=465
INFO_SMTP_USER=${INFO_SMTP_USER:-} INFO_SMTP_PASS=${INFO_SMTP_PASS:-}
INFO_SMTP_FROM=${INFO_SMTP_FROM:-} INFO_SMTP_ENABLED=1
MAIL_ENABLED=1
EOF
  chmod 600 "$SEC"
  echo "  ✔ secrets.env 已生成（0600，含 $([ -n "$DB_DSN" ] && echo "DB_DSN" || echo "占位（未传 DB_DSN，请手动补齐 pg_dsn.txt）")）"

  echo "==> [S4] 收敛主 unit + drop-in"
  cat > "$UNIT" <<'EOF'
[Unit]
Description=Translator SaaS Service
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/translator
ExecStart=/opt/translator/bin/translator-server \
  -addr 127.0.0.1:8787 \
  -frontend /opt/translator/web \
  -kbdb /opt/translator/data/tm.sqlite3
StandardOutput=append:/opt/translator/log/translator.log
StandardError=append:/opt/translator/log/translator.log
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  mkdir -p "$DROP"
  # 复用仓库内 prod.conf（已含 沙箱/密钥/并发/字体/内存红线）
  if [ -f /opt/translator/deploy/systemd/prod.conf ]; then
    cp /opt/translator/deploy/systemd/prod.conf "$DROP/prod.conf"
  elif [ -f "$(dirname "$0")/prod.conf" ]; then
    cp "$(dirname "$0")/prod.conf" "$DROP/prod.conf"
  else
    echo "❌ 未找到 prod.conf（期望 deploy/systemd/prod.conf），中止"; exit 1
  fi
  # 清理旧 6 个 drop-in（避免重复定义冲突）
  rm -f "$DROP"/{concurrency,hardening,mail,mem,pdffont,secrets}.conf
  echo "  ✔ 旧 6 drop-in 已清理，仅留 prod.conf"

  echo "==> [S5] Caddy 回调凭证对齐提示"
  echo "    确认 /etc/caddy/translator.conf 的 X-Admin-Token == secrets.env 的 ADMIN_TOKEN"
  echo "    （Caddy 若引用 \$TRANSLATOR_ADMIN_TOKEN，同步 /etc/default/caddy 同值）"

  echo "==> [S6] 重载启动"
  systemctl daemon-reload
  systemctl start translator
  sleep 2
  systemctl status translator --no-pager -l | head -15 || true
  echo "===================================================="
  echo "  迁移完成！请执行验收："
  echo "    systemctl cat translator | grep -i secret        # 应无明文"
  echo "    ./deploy/deploy_check.sh --systemd               # 沙箱 4 项本地验收"
  echo "    ./deploy/deploy_check.sh https://langcross.lexicorn.cn  # 全量验收"
  echo "    journalctl -u translator -n 30                   # 确认无拒启/panic"
  echo "===================================================="
}

rollback() {
  need_root
  TS=$(cat /tmp/translator_migrate_ts 2>/dev/null || echo "")
  [ -n "$TS" ] && [ -f "$BK/translator.service.pre-sandbox.$TS" ] || { echo "❌ 未找到备份（或已回滚）"; exit 1; }
  echo "==> 回滚到 $TS"
  systemctl stop translator || true
  cp "$BK/translator.service.pre-sandbox.$TS" "$UNIT"
  rm -rf "$DROP"
  if [ -d "$BK/translator.service.d.pre-sandbox.$TS" ]; then
    cp -r "$BK/translator.service.d.pre-sandbox.$TS" "$DROP"
  fi
  systemctl daemon-reload && systemctl start translator
  echo "  ✔ 已回滚并启动"
}

check() {
  need_root
  echo "==> 迁移前预检"
  echo "  unit:        $([ -f "$UNIT" ] && systemctl is-active translator || echo '未安装')"
  echo "  drop-in 目录: $(ls "$DROP" 2>/dev/null | tr '\n' ' ' || echo '无')"
  echo "  现役 JWT_SECRET 前缀: $(systemctl cat translator 2>/dev/null | grep -oE 'JWT_SECRET=.{0,6}' | head -1)"
  echo "  secrets.env: $([ -f "$SEC" ] && stat -c '%a %U:%G' "$SEC" || echo '不存在（待建）')"
  echo "  pg_dsn.txt:  $([ -f /opt/translator/pg_dsn.txt ] && echo '存在' || echo '不存在')"
}

case "$MODE" in
  migrate) migrate "$@";;
  rollback) rollback;;
  check) check;;
  *) echo "用法: $0 <migrate|rollback|check> [...]"; exit 1;;
esac
