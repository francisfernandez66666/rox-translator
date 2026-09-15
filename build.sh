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
# 分发提示：ad-hoc 签名仅限本机运行；对外分发设 APPLE_DEVELOPER_ID（及公证所需的
# APPLE_ID/APPLE_APP_PASSWORD/APPLE_TEAM_ID）即自动走 Developer ID 签名+notarytool 公证+staple。
# ============================================================================

set -euo pipefail
cd "$(dirname "$0")"

APP_NAME="翻译助手"
APP="dist/$APP_NAME.app"
CONTENTS="$APP/Contents"

echo "==> [1/4] 构建前端 ..."
(cd frontend-react && npm install --silent && npm run build)

# ★ G2 方言护栏（2026-09-12 存储统一 S 批）：业务代码禁止内联 SQLite JSON1 专属函数
#   （json_set/json_extract/json('...')），一律经 internal/db/jsonops.go 双方言助手，
#   否则 PG 生产（唯一受支持方言）下整条 SQL 报错且多为静默失败链路。
echo "==> [1.5/4] 方言护栏扫描（JSON1 裸用检测）..."
offenders=$(grep -rn --include='*.go' -E "json_set\(|json_extract\(|json\('" backend-go/internal backend-go/cmd \
  | grep -v '_test.go' | grep -v 'internal/db/jsonops.go' | grep -v 'internal/db/rewrite.go' \
  | grep -v '//' || true)
if [ -n "$offenders" ]; then
  echo "❌ 发现业务层内联 SQLite JSON1 语法（PG 生产必坏），请改用 db.JSON* 助手："
  echo "$offenders"
  exit 1
fi
echo "    方言护栏通过"

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

echo "==> [4/4] 签名/公证 ..."
# ★ P2（2026-09-15）：可选 Developer ID 签名 + 公证流水线——
#   缺省（未设环境变量）仍走 ad-hoc 本地签名（内部试用零门槛）；
#   对外分发时设置：
#     APPLE_DEVELOPER_ID="Your Name (TEAMID)"   # codesign 身份后缀（钥匙串需含 Developer ID Application 私钥）
#     APPLE_ID="you@example.com"                # Apple ID（公证账号）
#     APPLE_APP_PASSWORD="xxxx-xxxx-xxxx-xxxx"   # App 专用密码（appleid.apple.com 生成）
#     APPLE_TEAM_ID="TEAMID"                    # 团队 ID
#   四者齐备即自动完成：Developer ID 签名（hardened runtime）→ notarytool 公证 → staple 装订。
#   公证失败绝不静默出未签名包：直接报错退出，防分发「下载能过、Gatekeeper 拦死」的哑包。
if [ -n "${APPLE_DEVELOPER_ID:-}" ]; then
  ENT="dist/.app-entitlements.plist"  # ★ 必须放包外：签名后删包内文件会破坏 seal
  cat > "$ENT" <<'ENT_PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>com.apple.security.cs.allow-unsigned-executable-memory</key><true/>
  <key>com.apple.security.network.client</key><true/>
  <key>com.apple.security.network.server</key><true/>
</dict>
</plist>
ENT_PLIST
  echo "    · Developer ID 签名：$APPLE_DEVELOPER_ID"
  codesign --force --deep --options runtime --timestamp \
    --entitlements "$ENT" \
    --sign "Developer ID Application: $APPLE_DEVELOPER_ID" "$APP"
  if [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ]; then
    echo "    · 打包公证暂存 zip（notarytool 不接受 .app 直传）"
    NOTARY_ZIP="dist/$APP_NAME.notary.zip"
    ditto -c -k --keepParent "$APP" "$NOTARY_ZIP"
    echo "    · notarytool 提交公证（异步等待苹果回执，通常 1-5 分钟）..."
    xcrun notarytool submit "$NOTARY_ZIP" \
      --apple-id "$APPLE_ID" --password "$APPLE_APP_PASSWORD" --team-id "$APPLE_TEAM_ID" \
      --wait
    rm -f "$NOTARY_ZIP"
    echo "    · stapler 装订票据（离线首启也免联网验签）"
    xcrun stapler staple "$APP"
    xcrun stapler validate "$APP"
  else
    echo "    ! 已 Developer ID 签名，但 APPLE_ID/APPLE_APP_PASSWORD/APPLE_TEAM_ID 不齐，跳过公证"
    echo "      （对外分发必须公证，否则 Gatekeeper 直接拦截下载版）"
  fi
  rm -f "$ENT"
else
  codesign --force --deep --sign - "$APP" 2>/dev/null || echo "（签名跳过）"
fi

echo ""
echo "✅ 完成：$APP"
echo "   双击打开即用；数据目录：~/Library/Application Support/翻译助手/"
echo "   日志：~/Library/Logs/translator/app.log"
