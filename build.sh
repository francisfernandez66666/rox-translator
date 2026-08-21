#!/bin/bash
# ============================================================================
# build.sh — 一键构建免安装版 翻译助手.app
#
# 用法:  ./build.sh
# 产物:  dist/翻译助手.app/  +  dist/翻译助手.zip（可直接发给别人）
#
# 流程:  前端构建 → PyInstaller 打包 → 封装 .app → 签名 → 压缩
# ============================================================================
set -e

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
DIST_DIR="$PROJECT_DIR/dist"
APP_NAME="翻译助手"

echo "============================================"
echo "  构建 $APP_NAME.app（免安装版）"
echo "============================================"

# ---- 1. 检查环境 ----
echo ""
echo "[1/5] 检查环境..."
if ! command -v node &>/dev/null; then
    echo "❌ 未找到 Node.js，请先安装"
    exit 1
fi
if ! command -v python3 &>/dev/null; then
    echo "❌ 未找到 Python3，请先安装"
    exit 1
fi
if ! python3 -c "import PyInstaller" 2>/dev/null; then
    echo "❌ 未安装 PyInstaller，请执行: pip3 install pyinstaller"
    exit 1
fi
echo "   ✅ 环境就绪"

# ---- 2. 构建前端 ----
echo ""
echo "[2/5] 构建前端..."
cd "$PROJECT_DIR/frontend"
if [ ! -d "node_modules" ]; then
    npm install --silent
fi
npm run build
echo "   ✅ 前端构建完成"

# ---- 3. PyInstaller 打包后端 ----
echo ""
echo "[3/5] 打包后端（PyInstaller）..."
rm -rf "$DIST_DIR/$APP_NAME"
rm -rf "$DIST_DIR/$APP_NAME.app"
cd "$PROJECT_DIR"
pyinstaller --clean 翻译助手.spec 2>&1 | tail -5
echo "   ✅ 后端打包完成"

# ---- 4. 封装 .app ----
echo ""
echo "[4/5] 封装 .app..."
BUILD_DIR="$DIST_DIR/$APP_NAME"
APP_BUNDLE="$DIST_DIR/$APP_NAME.app"
mkdir -p "$APP_BUNDLE/Contents/MacOS"
mkdir -p "$APP_BUNDLE/Contents/Resources"

cp -a "$BUILD_DIR/_internal" "$APP_BUNDLE/Contents/Resources/"
cp "$BUILD_DIR/翻译助手" "$APP_BUNDLE/Contents/Resources/翻译助手"

cat > "$APP_BUNDLE/Contents/MacOS/launcher" << 'LAUNCHER'
#!/bin/bash

LOGFILE="$HOME/Desktop/翻译助手启动日志.txt"

echo "===== 翻译助手启动日志 $(date) =====" > "$LOGFILE"

# 1. 杀死遗留的旧翻译助手进程（占着 8000 端口的老进程）
OLD_PIDS=$(lsof -ti tcp:8000 2>/dev/null)
if [ -n "$OLD_PIDS" ]; then
    echo "发现旧进程: $OLD_PIDS，正在杀死..." >> "$LOGFILE"
    kill -9 $OLD_PIDS 2>/dev/null || true
    sleep 1
fi

# 2. 清除 macOS 隔离属性（来自旧版下载/压缩残留）
MY_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_ROOT="$MY_DIR/../.."
echo "APP_ROOT=$APP_ROOT" >> "$LOGFILE"
xattr -cr "$APP_ROOT" 2>/dev/null || true
echo "隔离属性已清除" >> "$LOGFILE"

# 3. 清理 PyInstaller 上次解压的临时文件（如果之前异常退出）
rm -rf /tmp/_MEI* 2>/dev/null || true
echo "临时文件已清理" >> "$LOGFILE"

# 4. 检查二进制是否存在
BIN="$MY_DIR/../Resources/翻译助手"
if [ ! -f "$BIN" ]; then
    echo "错误: 找不到 $BIN" >> "$LOGFILE"
    exit 1
fi
echo "二进制存在: $BIN" >> "$LOGFILE"

# 5. 启动翻译助手，输出追加到日志
cd "$MY_DIR/../Resources"
echo "启动中..." >> "$LOGFILE"
./翻译助手 >> "$LOGFILE" 2>&1
EXIT_CODE=$?
echo "进程退出，退出码=$EXIT_CODE" >> "$LOGFILE"
LAUNCHER
chmod +x "$APP_BUNDLE/Contents/MacOS/launcher"

echo "APPL????" > "$APP_BUNDLE/Contents/PkgInfo"

cat > "$APP_BUNDLE/Contents/Info.plist" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>zh_CN</string>
    <key>CFBundleDisplayName</key>
    <string>翻译助手</string>
    <key>CFBundleExecutable</key>
    <string>launcher</string>
    <key>CFBundleIdentifier</key>
    <string>com.rox.translator</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundleName</key>
    <string>翻译助手</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>2.0.0</string>
    <key>CFBundleVersion</key>
    <string>1</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.15</string>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>NSPrincipalClass</key>
    <string>NSApplication</string>
</dict>
</plist>
PLIST

find "$APP_BUNDLE" -name ".DS_Store" -delete 2>/dev/null || true
xattr -cr "$APP_BUNDLE" 2>/dev/null || true
echo "   ✅ .app 封装完成"

# ---- 5. Ad-hoc 代码签名 ----
echo ""
echo "[5/5] 代码签名..."
# ★ 关键：若项目位于 iCloud 同步目录（如 ~/Desktop 桌面同步），codesign 处理
#   .app 时会被 fileprovider 实时打上 com.apple.FinderInfo 属性，导致签名永远失败。
#   解决办法：复制到非同步临时目录签名，成功后再移回。
TMP_SIGN_DIR="$(mktemp -d "/tmp/翻译助手_sign_XXXXXX")"
TMP_APP="$TMP_SIGN_DIR/$APP_NAME.app"

cp -a "$APP_BUNDLE" "$TMP_APP"
find "$TMP_APP" -name ".DS_Store" -delete 2>/dev/null || true
find "$TMP_APP" -name "._*" -delete 2>/dev/null || true

# ★ 强力清除所有扩展属性（FinderInfo / fileprovider 等，xattr -cr 清不干净）
TMP_APP_PATH="$TMP_APP" python3 - <<'PYEOF'
import os, subprocess
root = os.environ["TMP_APP_PATH"]
for dp, dn, fn in os.walk(root):
    for n in fn + dn:
        p = os.path.join(dp, n)
        for a in subprocess.run(["xattr", p], capture_output=True, text=True).stdout.split():
            subprocess.run(["xattr", "-d", a, p], capture_output=True)
PYEOF

# 签名顺序很重要：先签最内层，再逐层向外，最后签整个 .app
SIG_LOG="$TMP_SIGN_DIR/sign_err.log"
# 1. 所有 .dylib / .so 动态库
find "$TMP_APP/Contents/Resources" -type f \( -name "*.dylib" -o -name "*.so" \) -print0 2>/dev/null | while IFS= read -r -d '' f; do
    codesign --force --deep --sign - --timestamp=none "$f" 2>>"$SIG_LOG" || true
done
# 2. Python.framework 内部二进制 + framework 本体（★ 漏签这里会被 Gatekeeper 拦截闪退）
PY_FW="$TMP_APP/Contents/Resources/_internal/Python.framework"
if [ -d "$PY_FW" ]; then
    for v in "$PY_FW"/Versions/*/Python; do
        codesign --force --sign - --timestamp=none "$v" 2>>"$SIG_LOG" || true
    done
    codesign --force --deep --sign - --timestamp=none "$PY_FW" 2>>"$SIG_LOG" || true
fi
# 3. 主可执行文件（Resources/翻译助手）
codesign --force --sign - --timestamp=none "$TMP_APP/Contents/Resources/翻译助手" 2>>"$SIG_LOG" || true
# 4. .app 启动脚本 launcher（CFBundleExecutable，必须签名）
codesign --force --sign - --timestamp=none "$TMP_APP/Contents/MacOS/launcher" 2>>"$SIG_LOG" || true
# 5. 整个 .app（★ 必须用 --deep 递归签所有子宿主）
codesign --force --deep --sign - --timestamp=none "$TMP_APP" 2>>"$SIG_LOG" || true

# 6. 验证签名；失败则中止构建（避免发出无法运行的包）
if codesign --verify --deep --strict "$TMP_APP" 2>/dev/null; then
    echo "   ✅ 签名完成，验证通过"
    # 在临时目录直接打 zip（★ 避免移回 dist 后 iCloud 重新打 FinderInfo 属性污染包）
    cd "$TMP_SIGN_DIR"
    rm -f "$APP_NAME.zip"
    zip -ry "$APP_NAME.zip" "$APP_NAME.app" -x "*.DS_Store" "._*" 2>/dev/null
    # 把 zip 和 app 移回 dist
    cd "$DIST_DIR"
    rm -rf "$APP_NAME.app"
    rm -f "$APP_NAME.zip"
    mv "$TMP_SIGN_DIR/$APP_NAME.app" "$DIST_DIR/"
    mv "$TMP_SIGN_DIR/$APP_NAME.zip" "$DIST_DIR/"
    rm -rf "$TMP_SIGN_DIR"
else
    echo "   ❌ 签名验证失败，中止构建"
    echo "--- 签名过程错误日志 ---"
    cat "$SIG_LOG" 2>/dev/null | head -10
    echo "--- 验证详情 ---"
    codesign --verify --deep --strict --verbose=2 "$TMP_APP" 2>&1 | tail -3
    rm -rf "$TMP_SIGN_DIR"
    exit 1
fi

echo "   ✅ 打包完成"

echo ""
echo "============================================"
echo "  ✅ 构建完成！"
echo ""
echo "  📱 App:      $APP_BUNDLE"
echo "  📦 安装包:   $DIST_DIR/$APP_NAME.zip"
echo ""
echo "  使用方式："
echo "    1. 解压 $APP_NAME.zip"
echo "    2. 右键点击 翻译助手.app → 选择「打开」"
echo "    3. 浏览器自动打开，即可使用"
echo ""
echo "  ⚠️ 首次打开如提示"无法验证开发者"，"
echo "     请右键点击 → 选择「打开」即可"
echo "============================================"
