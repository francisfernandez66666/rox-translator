#!/usr/bin/env bash
# ============================================================================
# start.sh — 本地开发环境一键启动（2026-08-26 重写，Go 单栈）
# 旧脚本说明：原版本启动 Python 后端（backend/main.py @ :8000），该栈已按
#   《旧Python后端下线与构建链收敛方案》整体退役；前端 vite 代理指向 Go :8787。
#
# 用法：
#   ./start.sh            # 编译并启动 Go 后端（127.0.0.1:8787）
#   ./start.sh -f         # 额外前台启动 vite dev server（:5173，API 代理到 8787）
#   ./start.sh -b         # 启动前先执行一次前端构建（frontend/dist 不存在时也会自动构建）
# ============================================================================

set -euo pipefail
cd "$(dirname "$0")"

FRONTEND_DIST="frontend/dist"
NEED_FRONTEND_BUILD=0

# ---------- 参数解析 ----------
RUN_VITE=0
for arg in "$@"; do
  case "$arg" in
    -f) RUN_VITE=1 ;;          # -f：前台起 vite dev（热更新开发模式）
    -b) NEED_FRONTEND_BUILD=1 ;; # -b：强制重建前端 dist
  esac
done

# ---------- 前端构建（缺失或强制时） ----------
if [ "$NEED_FRONTEND_BUILD" = "1" ] || [ ! -f "$FRONTEND_DIST/index.html" ]; then
  echo "==> 构建前端 (npm run build) ..."
  (cd frontend && npm install --silent && npm run build)
fi

# ---------- 编译 Go 后端 ----------
echo "==> 编译后端 (go build) ..."
(cd backend-go && go build -o /tmp/translator-server-dbg ./cmd/server)

# ---------- 启动参数说明 ----------
#   -addr     仅监听回环地址（本地开发不暴露公网）
#   -frontend 前端静态资源目录（Go 内嵌托管，浏览器直接访问 http://127.0.0.1:8787）
#   -kb       知识库向量文件（data/tm_embeddings.npz；缺失则以无向量模式运行）
#   -kbdb     知识库 SQLite 路径（首次运行自动建库 + 初始化超管 admin/admin123）
echo "==> 启动服务 http://127.0.0.1:8787 （默认账号 admin / admin123）"
(cd backend-go && /tmp/translator-server-dbg \
  -addr 127.0.0.1:8787 \
  -frontend "../$FRONTEND_DIST" \
  -kb ../data/tm_embeddings.npz \
  -kbdb data/dev.db) &
SERVER_PID=$!
trap 'kill $SERVER_PID 2>/dev/null || true' EXIT

# ---------- 可选：vite dev（热更新） ----------
if [ "$RUN_VITE" = "1" ]; then
  echo "==> 启动 vite dev http://localhost:5173 （API 已代理至 :8787）"
  (cd frontend && npx vite)
else
  echo "==> 就绪。打开 http://127.0.0.1:8787 ；Ctrl+C 退出"
  wait $SERVER_PID
fi
