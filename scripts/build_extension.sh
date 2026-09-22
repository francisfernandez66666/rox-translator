#!/usr/bin/env bash
# ============================================================================
# build_extension.sh — 浏览器划词插件（extension/）打包与托管产物同步
# ★ 建立于 2026-09-23（磁盘清理批顺带补的交付缺口）：
#   此前 `extension/` 是一个**没有任何交付渠道**的孤儿目录——没有打包脚本、
#   站点上没有托管物、manifest 版本自初始就停在 1.0.0。改了 popup/content.css
#   （如 〇-LI 的白色两档还原）也无法到达用户手里，更不能声称「已上线」。
#   本脚本把这条链补齐：版本号单一来源 = `extension/manifest.json`，
#   产物落 `frontend-react/public/extensions/`，随前端 dist 一起换源即上线，
#   无需动后端二进制（`/extensions/*.zip` 走 spa.go 的静态直出）。
#
# 用法：
#   scripts/build_extension.sh                 # 按 manifest 当前版本打包并刷新 latest
#   scripts/build_extension.sh 1.2.0           # 先把 manifest.version 改成 1.2.0，再打包
#   scripts/build_extension.sh --check         # 只校验：托管 zip 是否与源码一致（漂移闸门，不改文件）
#
# 为什么 zip 进仓库（而不是只在 CI 里生成）：
#   站点是「Go 二进制 + dist 静态目录」的自托管形态，没有对象存储也没有 CI 产物仓，
#   zip 不进仓库就等于没有下载入口。体积约 10 KB，可接受。
#   代价是必须防「改了源码忘了重打包」——所以有 `--check`，并被
#   `frontend-react/src/extensionPackage.test.ts` 的 vitest 锁钉进发布闸门。
#
# 版本口径：manifest.json 里的 `version` 是唯一事实源；zip 文件名带该版本号，
#   latest 副本仅作为页面上的稳定下载链接（避免改一次版本就要改一次前端）。
# ============================================================================
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EXT="$ROOT/extension"
OUT_DIR="$ROOT/frontend-react/public/extensions"
MANIFEST="$EXT/manifest.json"

# 打包进 zip 的文件清单（新增文件必须在这里登记，否则不会进包——显式白名单
# 优于 `zip -r .`，避免把 .DS_Store / 调试残留塞进用户下载的包里）
FILES=(manifest.json background.js content.js content.css popup.html popup.js INSTALL.txt)

die() { echo "❌ $1" >&2; exit 1; }
[ -f "$MANIFEST" ] || die "找不到 $MANIFEST"

VER="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$MANIFEST")" \
  || die "读取 manifest.version 失败"

MODE="build"
BUMP=""
for a in "$@"; do
  case "$a" in
    --check) MODE="check" ;;
    [0-9]*)  BUMP="$a" ;;
    *)       die "未知参数：$a（用法见文件头）" ;;
  esac
done

if [ -n "$BUMP" ]; then
  MODE="build"
  echo "==> 版本 bump：$VER → $BUMP（写回 manifest.json）"
  python3 - "$MANIFEST" "$BUMP" <<'PY' || die "写回版本失败"
import json, sys
p, v = sys.argv[1], sys.argv[2]
raw = open(p, encoding='utf-8').read()
d = json.loads(raw)
d['version'] = v
open(p, 'w', encoding='utf-8').write(json.dumps(d, ensure_ascii=False, indent=2) + '\n')
PY
  VER="$BUMP"
fi

for f in "${FILES[@]}"; do
  case "$f" in
    INSTALL.txt) [ -f "$EXT/$f" ] || continue ;;  # 安装说明可选，缺了不阻断打包
    *) [ -f "$EXT/$f" ] || die "打包清单里的文件不存在：extension/$f" ;;
  esac
done

# 内容指纹：把打包清单按固定顺序拼接后取 sha256。
# 不用 zip 自身字节比对——zip 条目带 mtime，同样内容两次打包字节不一致，
# 拿它当漂移闸门会出现「重跑就红」的假警。
digest() {
  python3 - "$EXT" "${FILES[@]}" <<'PY'
import hashlib, os, sys
ext = sys.argv[1]
h = hashlib.sha256()
for name in sys.argv[2:]:
    p = os.path.join(ext, name)
    h.update(name.encode() + b'\0')
    if os.path.isfile(p):
        h.update(open(p, 'rb').read())
    else:
        h.update(b'<missing>')
    h.update(b'\0')
print(h.hexdigest())
PY
}
SRC_DIGEST="$(digest)" || die "计算源码指纹失败"
# 指纹文件与 zip 同目录同名（.sha256 后缀）：--check 只比这一个文件，不必解包（服务器上没装 unzip）
FP_FILE="$OUT_DIR/langcross-extension-$VER.sha256"
LATEST_FP_FILE="$OUT_DIR/langcross-extension-latest.sha256"
ZIP_FILE="$OUT_DIR/langcross-extension-$VER.zip"
LATEST_ZIP="$OUT_DIR/langcross-extension-latest.zip"

mkdir -p "$OUT_DIR" || die "无法创建 $OUT_DIR"

if [ "$MODE" = "check" ]; then
  [ -f "$ZIP_FILE" ] || die "托管产物缺失：$ZIP_FILE（请先跑 scripts/build_extension.sh）"
  for fp in "$FP_FILE" "$LATEST_FP_FILE"; do
    [ -f "$fp" ] || die "指纹文件缺失：${fp#$ROOT/}（zip 与指纹必须成对提交）"
    OLD="$(tr -d '[:space:]' < "$fp")"
    [ "$OLD" = "$SRC_DIGEST" ] || die "交付漂移：$fp 记录 $OLD，当前源码指纹 $SRC_DIGEST —— 请重跑 scripts/build_extension.sh 并一并提交 zip"
  done
  echo "✅ 托管 zip 与 extension/ 源码一致（版本 $VER，指纹 ${SRC_DIGEST:0:12}…）"
  exit 0
fi

echo "==> 打包 extension/（版本 $VER）"
rm -f "$ZIP_FILE"
# -X 去多余字段、-q 静默；显式列文件而非 -r 整目录（同上：防塞进垃圾文件）
(cd "$EXT" && zip -qX "$ZIP_FILE" "${FILES[@]}") || die "zip 失败"
cp -f "$ZIP_FILE" "$LATEST_ZIP" || die "刷新 latest 失败"
printf '%s\n' "$SRC_DIGEST" > "$FP_FILE"
printf '%s\n' "$SRC_DIGEST" > "$LATEST_FP_FILE"

echo "✅ 产物已刷新："
for p in "$ZIP_FILE" "$LATEST_ZIP"; do
  printf '   %s (%s 字节)  sha256 %s\n' "${p#$ROOT/}" "$(wc -c < "$p" | tr -d ' ')" "$(shasum -a 256 "$p" | cut -c1-12)"
done
echo "   站点下载路径：/extensions/langcross-extension-$VER.zip 与 /extensions/langcross-extension-latest.zip"
echo "⚠️ 生效条件：zip 落在 public/ 下，属于前端构建产物——只换 /opt/translator/web 即生效，不需要换后端二进制。"
