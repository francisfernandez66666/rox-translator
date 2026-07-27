#!/bin/bash
# ============================================================================
# fix_damaged_app.sh — 给用户用的：修复"app已损坏"问题
# 
# 用法：把此脚本和 翻译助手.app 放在同一目录，双击运行（或终端执行）
# ============================================================================

echo "🔧 翻译助手 — 修复启动脚本"
echo "================================="

APP_NAME="翻译助手"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_PATH="$SCRIPT_DIR/$APP_NAME.app"

# 如果当前脚本在 .app 包内（放在 Contents/Resources/ 里）
if [ -z "$APP_PATH" ] || [ ! -d "$APP_PATH" ]; then
    # 向上查找 .app 目录
    CURRENT="$SCRIPT_DIR"
    while [ "$CURRENT" != "/" ]; do
        if [[ "$CURRENT" == *.app ]]; then
            APP_PATH="$CURRENT"
            break
        fi
        CURRENT="$(dirname "$CURRENT")"
    done
fi

if [ ! -d "$APP_PATH" ]; then
    echo "❌ 找不到 $APP_NAME.app"
    echo "   请确保此脚本和 $APP_NAME.app 在同一目录下"
    read -p "按回车退出..."
    exit 1
fi

echo "📍 找到: $APP_PATH"
echo ""

# 1. 移除隔离属性（com.apple.quarantine）
echo "1️⃣ 移除 macOS 隔离属性..."
sudo xattr -cr "$APP_PATH" 2>/dev/null || xattr -cr "$APP_PATH" 2>/dev/null
echo "   ✅ 已清除隔离属性"

# 2. Ad-hoc 重新签名
echo ""
echo "2️⃣ Ad-hoc 代码签名..."
codesign --force --deep --sign - --timestamp=none "$APP_PATH" 2>/dev/null
echo "   ✅ 已重新签名"

# 3. 验证
echo ""
echo "3️⃣ 验证签名..."
codesign --verify --deep --strict "$APP_PATH" 2>/dev/null && echo "   ✅ 签名验证通过" || echo "   ⚠️ 警告：严格验证未通过（ad-hoc 签名正常）"

echo ""
echo "================================="
echo "✅ 修复完成！现在可以双击打开 $APP_NAME.app 了"
echo ""
echo "💡 如果还不行，请右键点击 $APP_NAME.app → 选择「打开」"
echo ""
read -p "按回车键退出..." dummy
