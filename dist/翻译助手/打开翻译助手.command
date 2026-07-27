#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"
xattr -cr "$DIR" 2>/dev/null
chmod +x "$DIR/翻译助手" 2>/dev/null

clear
echo "========================================="
echo "  翻译助手 — 启动中..."
echo "========================================="
echo ""
echo "🔓 已解除安全锁定"
echo "   （以后可直接双击「翻译助手.app」启动）"
echo ""

"./翻译助手" &
APP_PID=$!

for i in $(seq 1 30); do
    if curl -sf http://127.0.0.1:8000/api/health > /dev/null 2>&1; then
        echo "✅ 已就绪，打开浏览器..."
        open "http://localhost:8000"
        echo ""
        echo "========================================="
        echo "  翻译助手运行中  |  http://localhost:8000"
        echo "  关闭此窗口即停止服务"
        echo "========================================="
        break
    fi
    sleep 1
done

if ! kill -0 $APP_PID 2>/dev/null; then
    echo "❌ 启动失败，请检查 8000 端口是否被占用"
    read -p "按回车退出..."
    exit 1
fi

wait $APP_PID 2>/dev/null
echo "翻译助手已停止。"
