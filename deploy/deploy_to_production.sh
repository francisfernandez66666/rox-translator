#!/bin/bash
# ============================================================================
# 上生产部署脚本（LangCross 2026-10-01 聊天对话持久化批次）
# 用法：bash deploy_to_production.sh [SSH配置]
#   SSH配置格式: user@host[:port]
#   默认: root@43.108.86.140:28022 (需要 ssh_config 支持)
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
REMOTE_USER="${DEPLOY_USER:-root}"
REMOTE_HOST="${DEPLOY_HOST:-43.108.86.140}"
REMOTE_PORT="${DEPLOY_PORT:-28022}"

# --- 前置检查 ---
echo "=== [1/5] 检查构建产物 ==="
if [ ! -f /tmp/translator-server-linux ]; then
  echo "ERROR: Linux binary not found at /tmp/translator-server-linux"
  exit 1
fi
if [ ! -f /tmp/frontend-dist.tar.gz ]; then
  echo "ERROR: Frontend dist tarball not found at /tmp/frontend-dist.tar.gz"
  exit 1
fi

BACKEND_SIZE=$(wc -c < /tmp/translator-server-linux | tr -d ' ')
FRONTEND_SIZE=$(wc -c < /tmp/frontend-dist.tar.gz | tr -d ' ')
echo "  Backend: ${BACKEND_SIZE} bytes"
echo "  Frontend: ${FRONTEND_SIZE} bytes"

# --- 上传 ---
echo ""
echo "=== [2/5] 上传到服务器 ==="
scp -P "$REMOTE_PORT" /tmp/translator-server-linux "${REMOTE_USER}@${REMOTE_HOST}:/tmp/translator-server-new"
scp -P "$REMOTE_PORT" /tmp/frontend-dist.tar.gz "${REMOTE_USER}@${REMOTE_HOST}:/tmp/"
echo "  Upload complete."

# --- 远程替换 + 重启 ---
echo ""
echo "=== [3/5] 远程替换二进制和前端 ==="
ssh -p "$REMOTE_PORT" "${REMOTE_USER}@${REMOTE_HOST}" "
  TS='$TIMESTAMP'
  
  # 1. 备份旧版本
  echo '  [Backup] Backing up current binary...'
  cp /opt/translator/bin/translator-server /opt/translator/bin/translator-server.bak.\$TS
  
  # 2. 替换后端二进制
  echo '  [Backend] Replacing binary...'
  mv /tmp/translator-server-new /opt/translator/bin/translator-server && chmod +x /opt/translator/bin/translator-server
  
  # 3. 替换前端静态文件
  echo '  [Frontend] Replacing web files...'
  mv /opt/translator/web /opt/translator/web_old.\$TS 2>/dev/null || true
  mkdir -p /opt/translator/web
  tar xzf /tmp/frontend-dist.tar.gz -C /opt/translator/web --strip-components=1
  
  # 4. 重启服务
  echo '  [Restart] Restarting translator service...'
  systemctl restart translator
  
  # 5. 检查状态
  sleep 2
  STATUS=\$(systemctl is-active translator)
  echo \"  Status: \$STATUS\"
  if [ \"\$STATUS\" = \"active\" ]; then
    echo '  SUCCESS: Service is running.'
  else
    echo '  WARNING: Service is NOT active. Check logs:'
    echo '    journalctl -u translator -n 50 --no-pager'
    echo '    tail -50 /opt/translator/log/translator.log'
    exit 1
  fi
"

# --- 验证 ---
echo ""
echo "=== [4/5] 生产验证 ==="
ssh -p "$REMOTE_PORT" "${REMOTE_USER}@${REMOTE_HOST}" "
  echo '  Health check:'
  curl -sf https://langcross.lexicorn.cn/api/health || echo '  HEALTH CHECK FAILED'
  
  echo ''
  echo '  Panic check (last 100 lines):'
  grep -c panic /opt/translator/log/translator.log || echo '  0 panics found'
  
  echo ''
  echo '  Recent log entries:'
  tail -20 /opt/translator/log/translator.log
"

# --- 清理 ---
echo ""
echo "=== [5/5] 清理临时文件 ==="
rm -f /tmp/translator-server-new
echo "  Done."

echo ""
echo "============================================================================"
echo " 部署完成! 时间戳: $TIMESTAMP"
echo " 如需回滚，备份文件: /opt/translator/bin/translator-server.bak.$TIMESTAMP"
echo "============================================================================"
