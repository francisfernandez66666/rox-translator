#!/bin/bash
# ============================================================================
# build_app.sh — 从 PyInstaller COLLECT 输出构建可签名的 .app
# ============================================================================
set -e

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
DIST_DIR="$PROJECT_DIR/dist/翻译助手"
APP_NAME="翻译助手"
APP_BUNDLE="$PROJECT_DIR/dist/$APP_NAME.app"

echo "============================================"
echo "🔨 构建 $APP_NAME.app"
echo "============================================"

# 清理旧 app
rm -rf "$APP_BUNDLE"

# 创建 .app 目录结构
mkdir -p "$APP_BUNDLE/Contents/MacOS"
mkdir -p "$APP_BUNDLE/Contents/Resources"

# 复制 COLLECT 产出的全部文件到 Resources
echo "📦 复制文件到 .app 包..."
cp -a "$DIST_DIR/_internal"       "$APP_BUNDLE/Contents/Resources/"
cp -a "$DIST_DIR/翻译助手"         "$APP_BUNDLE/Contents/MacOS/$APP_NAME"
chmod +x "$APP_BUNDLE/Contents/MacOS/$APP_NAME"

# 创建可执行包装脚本（确保 working directory 正确）
cat > "$APP_BUNDLE/Contents/MacOS/launcher.sh" << 'LAUNCHER_EOF'
#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
RESOURCES="$DIR/../Resources"
cd "$RESOURCES"
exec "$DIR/翻译助手"
LAUNCHER_EOF
chmod +x "$APP_BUNDLE/Contents/MacOS/launcher.sh"

# PkgInfo
echo "APPL????" > "$APP_BUNDLE/Contents/PkgInfo"

# Info.plist
cat > "$APP_BUNDLE/Contents/Info.plist" << 'PLIST_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>zh_CN</string>
	<key>CFBundleDisplayName</key>
	<string>翻译助手</string>
	<key>CFBundleExecutable</key>
	<string>翻译助手</string>
	<key>CFBundleIdentifier</key>
	<string>com.rox.translator</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>翻译助手</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0.0</string>
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
PLIST_EOF

echo "✅ .app 目录结构已创建"

# ---- 清理 macOS 垃圾文件（资源分支、.DS_Store 等会导致签名失败）----
echo ""
echo "🧹 清理 .DS_Store 和扩展属性..."
find "$APP_BUNDLE" -name ".DS_Store" -delete 2>/dev/null
find "$APP_BUNDLE" -name "._*" -delete 2>/dev/null
xattr -cr "$APP_BUNDLE" 2>/dev/null || true

# ---- 对 .app 内所有二进制文件做 ad-hoc 签名 ----
echo ""
echo "🔐 Ad-hoc 代码签名..."

# 1. 先签名 _internal 里的所有 .dylib 和 framework
find "$APP_BUNDLE/Contents/Resources/_internal" -type f \( -name "*.dylib" -o -name "*.so" \) -print0 2>/dev/null | while IFS= read -r -d '' f; do
    codesign --force --deep --sign - --timestamp=none "$f" 2>/dev/null || true
done

# 2. 签名 Python.framework（如果存在）
PY_FW="$APP_BUNDLE/Contents/Resources/_internal/Python.framework"
if [ -d "$PY_FW" ]; then
    echo "  签名 Python.framework..."
    codesign --force --deep --sign - --timestamp=none "$PY_FW/Versions/Current/Python" 2>/dev/null || true
    codesign --force --deep --sign - --timestamp=none "$PY_FW/Versions/Current/Resources/Python.app" 2>/dev/null || true
    codesign --force --deep --sign - --timestamp=none "$PY_FW" 2>/dev/null || true
fi

# 3. 签名主可执行文件
echo "  签名主可执行文件..."
codesign --force --deep --sign - --timestamp=none "$APP_BUNDLE/Contents/MacOS/$APP_NAME"

# 4. 签名整个 .app
echo "  签名 .app 包..."
codesign --force --deep --sign - --timestamp=none "$APP_BUNDLE"

# 5. 验证签名
echo ""
echo "🔍 验证签名..."
codesign --verify --deep --strict --verbose=2 "$APP_BUNDLE" 2>&1 || echo "  ⚠️ 验证有警告（ad-hoc 签名正常）"

echo ""
echo "============================================"
echo "✅ 完成！"
echo ""
echo "📱 App 位置: $APP_BUNDLE"
echo ""
echo "📦 分发方式："
echo "   zip 打包:  zip -ry 翻译助手.zip \"$APP_NAME.app\""
echo "   DMG 打包:  create-dmg \"$APP_NAME.app\""
echo ""
echo "⚠️ 用户收到后如果仍提示损坏，请右键点击 → 打开，"
echo "   或运行: xattr -cr \"$APP_NAME.app\""
echo "============================================"
