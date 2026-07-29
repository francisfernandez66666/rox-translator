#!/bin/bash
# ============================================================================
# start.sh — 一键启动翻译助手（开发模式）
# 同时启动：
#   后端 FastAPI (uvicorn) → http://127.0.0.1:8000
#   前端 Vite 开发服务器  → http://127.0.0.1:5173（自动代理 /api 到后端）
# ============================================================================
set -e

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
BACKEND_DIR="$PROJECT_DIR/backend"
FRONTEND_DIR="$PROJECT_DIR/frontend"

echo "============================================"
echo "  翻译助手 — 一键启动"
echo "============================================"

# ---- 检查 Python ----
PYTHON=""
for cmd in python3 python; do
    if command -v "$cmd" &>/dev/null; then
        PYTHON="$cmd"
        break
    fi
done
if [ -z "$PYTHON" ]; then
    echo "[错误] 未找到 Python3，请先安装"
    exit 1
fi

# ---- 检查 Node.js ----
if ! command -v node &>/dev/null; then
    echo "[错误] 未找到 Node.js，请先安装"
    exit 1
fi

# ---- 安装后端依赖 ----
if [ ! -f "$BACKEND_DIR/.deps_installed" ]; then
    echo "[安装] 后端依赖..."
    "$PYTHON" -m pip install -r "$BACKEND_DIR/requirements.txt" -q
    touch "$BACKEND_DIR/.deps_installed"
fi

# ---- 安装前端依赖 ----
if [ ! -d "$FRONTEND_DIR/node_modules" ]; then
    echo "[安装] 前端依赖..."
    cd "$FRONTEND_DIR" && npm install --silent
fi

# ---- 启动后端 ----
echo "[启动] 后端 FastAPI (http://127.0.0.1:8000)"
cd "$BACKEND_DIR"
"$PYTHON" main.py &
BACKEND_PID=$!
echo "   PID: $BACKEND_PID"

# ---- 启动前端 ----
echo "[启动] 前端 Vite (http://127.0.0.1:5173)"
cd "$FRONTEND_DIR"
npx vite --host 127.0.0.1 &
FRONTEND_PID=$!
echo "   PID: $FRONTEND_PID"

echo ""
echo "============================================"
echo "  后端: http://127.0.0.1:8000"
echo "  前端: http://127.0.0.1:5173"
echo "  按 Ctrl+C 停止所有服务"
echo "============================================"

# ---- 等待后端就绪后自动打开浏览器 ----
echo "[等待] 后端启动中..."
for i in $(seq 1 30); do
    if curl -s http://127.0.0.1:8000/api/health >/dev/null 2>&1; then
        echo "[就绪] 后端已启动"
        break
    fi
    sleep 1
done

echo "[打开] 浏览器 http://127.0.0.1:5173"
if command -v open &>/dev/null; then
    open http://127.0.0.1:5173
fi

cleanup() {
    echo ""
    echo "[停止] 正在关闭服务..."
    kill "$BACKEND_PID" 2>/dev/null
    kill "$FRONTEND_PID" 2>/dev/null
    wait "$BACKEND_PID" 2>/dev/null
    wait "$FRONTEND_PID" 2>/dev/null
    echo "[停止] 已退出"
}

trap cleanup EXIT INT TERM

wait
