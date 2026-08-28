#!/usr/bin/env bash
# ============================================================================
# build.sh — 构建 macOS 桌面版「翻译助手.app」（2026-08-26 重写，Go 单栈）
# 旧链路说明：原脚本经 PyInstaller 打包 Python 后端（backend/main.py），该栈已退役。
#   新形态：Go 单二进制 + 内嵌前端 dist，无 Python 依赖、无 UPX、无 xattr/sudo 补丁链。
#
# 产物结构：
#   dist/翻译助手.app/Contents/MacOS/translator      # Go 服务（监听 127.0.0.1 随机端口）
#   dist/翻译助手.app/Contents/MacOS/launcher        # 启动器：起服务 → 打开浏览器
#   dist/翻译助手.app/Contents/Resources/frontend/   # 前端静态资源（-frontend 指向）
#   dist/翻译助手.app/Contents/Info.plist
#
# 分发提示：ad-hoc 签名仅限本机运行；对外分发需 Apple Developer ID 签名 + 公证
# （notarytool），届时替换下方 codesign 参数即可，其余流程不变。
# ============================================================================

set -euo pipefail
cd "$(dirname "$0")"

APP_NAME="翻译助手"
APP="dist/$APP_NAME.app"
CONTENTS="$APP/Contents"

echo "==> [1/4] 构建前端 ..."
(cd frontend-react && npm install --silent && npm run build)

echo "==> [2/4] 编译 Go 后端 (darwin 单架构本机二进制：按当前主机架构产出，非 arm64+amd64 通用 fat 包) ..."
(cd backend-go && go build -ldflags "-s -w" -o /tmp/translator-server-mac ./cmd/server)

echo "==> [3/4] 组装 $APP ..."
rm -rf "$APP"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources/frontend"

# Go 服务二进制
cp /tmp/translator-server-mac "$CONTENTS/MacOS/translator"
chmod +x "$CONTENTS/MacOS/translator"

# 前端静态资源
cp -R frontend-react/dist "$CONTENTS/Resources/frontend/dist"

# Info.plist：应用元信息（可执行文件名 / 标识符 / 版本）
cat > "$CONTENTS/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>          <string>launcher</string>
    <key>CFBundleIdentifier</key>          <string>cn.lexicorn.translator</string>
    <key>CFBundleName</key>                <string>$APP_NAME</string>
    <key>CFBundlePackageType</key>         <string>APPL</string>
    <key>CFBundleShortVersionString</key>  <string>4.0.0</string>
    <key>CFBundleVersion</key>             <string>4</string>
    <key>LSMinimumSystemVersion</key>      <string>11.0</string>
    <key>NSHighResolutionCapable</key>     <true/>
</dict>
</plist>
PLIST

# launcher 启动器：选空闲端口 → 起服务 → 打开浏览器；退出时回收服务进程
cat > "$CONTENTS/MacOS/launcher" <<'LAUNCH'
#!/usr/bin/env bash
DIR="$(cd "$(dirname "$0")" && pwd)"
PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
LOG_DIR="$HOME/Library/Logs/translator"; mkdir -p "$LOG_DIR"
"$DIR/translator" -addr "127.0.0.1:$PORT" \
  -frontend "$DIR/../Resources/frontend/dist" \
  -kb "$HOME/Library/Application Support/翻译助手/tm_embeddings.npz" \
  >> "$LOG_DIR/app.log" 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null || true' EXIT
open "http://127.0.0.1:$PORT"
wait $SRV
LAUNCH
chmod +x "$CONTENTS/MacOS/launcher"

echo "==> [4/4] ad-hoc 签名 ..."
codesign --force --deep --sign - "$APP" 2>/dev/null || echo "（签名跳过）"

echo ""
echo "✅ 完成：$APP"
echo "   双击打开即用；数据目录：~/Library/Application Support/翻译助手/"
echo "   日志：~/Library/Logs/translator/app.log"
