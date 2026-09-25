#!/usr/bin/env bash
# ============================================================================
# build_sdk.sh — 官方 SDK 本地交付物打包与站点托管同步（★ F-27，2026-09-25）
# 背景（为什么要建这条链）：后台 SDK 页此前展示的是 `pip install langcross-translator`
#   / `npm install @langcross/translator-sdk` 这类**假命令**——包从未发布到 PyPI / npm
#   公共仓库，用户照抄必然 404。真发公共仓库属运营动作（发布日前不做），故本批采账本
#   选项②：把仓库里现成的交付物（sdk/python/dist 的 whl/sdist + sdk/typescript 的
#   npm pack tgz）收口为「本站托管、本地安装」，与扩展 zip 同一条静态直出链
#   （`/sdk/*` 走 spa.go 静态直出，只换 /opt/translator/web 即生效，不动后端二进制）。
#
# 用法：
#   scripts/build_sdk.sh                 # 按当前双版本刷新 public/sdk/
#   scripts/build_sdk.sh 1.1.0           # 先把 pyproject.toml 与 package.json 的 version
#                                        # 同步 bump 到 1.1.0（两语种 SDK 版本同步是仓内
#                                        # 既定口径，见 pyproject 注释），再打包
#   scripts/build_sdk.sh --check         # 只校验：托管产物是否与源码一致（漂移闸门，不改文件）
#
# 口径完全仿 scripts/build_extension.sh：
#   - 版本号唯一事实源 = sdk/python/pyproject.toml 与 sdk/typescript/package.json；
#     产物文件名带版本号，latest 别名仅作为外部留存链路的稳定下载名；
#   - .sha256 记的是「源码内容指纹」而非产物字节哈希——指纹算法与 build_extension.sh
#     同口径（固定顺序拼「相对路径 + NUL + 文件字节 + NUL」再 sha256，缺文件记 <missing>）。
#     之所以比源码而不是产物字节：npm pack 的 tgz 条目带 mtime，同样内容两次打包字节
#     不同，比字节的锁会假红；whl/sdist 虽是现成文件不重打包，但两语种统一走源码指纹，
#     「改了 SDK 源码忘了同步托管产物」这一类漂移才能被 --check 拦住。
#   - 额外一层字节对照：若 sdk/python/dist 的源产物在盘（dist/ 不进 git，服务器上多半没有），
#     --check 还会比对源与托管副本的字节 sha256，抓住「本地重打包了 whl 但没跑本脚本」。
#   - public/sdk/manifest.json 由本脚本生成，前端 SdkP.tsx 运行时 fetch 它取真实文件名，
#     组件源码里不落任何版本号（改版本只需重跑脚本，不用改前端）。
# ============================================================================
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PY_SRC="$ROOT/sdk/python"
TS_SRC="$ROOT/sdk/typescript"
OUT_DIR="$ROOT/frontend-react/public/sdk"

# 参与源码指纹的文件白名单（新增源文件必须在这里登记，否则不会进指纹——与
# build_extension.sh 的 FILES 同一显式口径，避免把 .DS_Store / 调试残留算进来）
PY_FILES=(pyproject.toml translator_sdk.py README.md)
TS_FILES=(package.json tsconfig.json README.md src/index.ts)

die() { echo "❌ $1" >&2; exit 1; }
[ -f "$PY_SRC/pyproject.toml" ] || die "找不到 $PY_SRC/pyproject.toml"
[ -f "$TS_SRC/package.json" ] || die "找不到 $TS_SRC/package.json"

# 读版本号：pyproject 取首个 `version = "x.y.z"` 行；package.json 取 version 字段
read_py_ver() {
  python3 - "$PY_SRC/pyproject.toml" <<'PY'
import re, sys
src = open(sys.argv[1], encoding='utf-8').read()
m = re.search(r'(?m)^version\s*=\s*"([^"]+)"', src)
if not m:
    sys.exit('未找到 version 行')
print(m.group(1))
PY
}
read_ts_ver() {
  python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$TS_SRC/package.json"
}
PY_VER="$(read_py_ver)" || die "读取 pyproject.toml version 失败"
TS_VER="$(read_ts_ver)" || die "读取 package.json version 失败"

MODE="build"
BUMP=""
for a in "$@"; do
  case "$a" in
    --check) MODE="check" ;;
    [0-9]*)  BUMP="$a" ;;
    *)       die "未知参数：$a（用法见文件头）" ;;
  esac
done

# 版本 bump：两个版本源一起写（SDK 双语种版本同步是既定口径，拆开 bump 会制造版本漂移）。
# pyproject 用逐行正则保留行内注释；package.json 走 json  round-trip（2 空格缩进，与 npm 默认一致）。
if [ -n "$BUMP" ]; then
  MODE="build"
  echo "==> 版本 bump：python $PY_VER / typescript $TS_VER → $BUMP（写回两份版本源）"
  python3 - "$PY_SRC/pyproject.toml" "$BUMP" <<'PY' || die "写回 pyproject.toml 版本失败"
import re, sys
p, v = sys.argv[1], sys.argv[2]
src = open(p, encoding='utf-8').read()
new, n = re.subn(r'(?m)^(version\s*=\s*")[^"]+(")', r'\g<1>' + v + r'\g<2>', src, count=1)
if n != 1:
    sys.exit('version 行未命中')
open(p, 'w', encoding='utf-8').write(new)
PY
  python3 - "$TS_SRC/package.json" "$BUMP" <<'PY' || die "写回 package.json 版本失败"
import json, sys
p, v = sys.argv[1], sys.argv[2]
d = json.load(open(p, encoding='utf-8'))
d['version'] = v
open(p, 'w', encoding='utf-8').write(json.dumps(d, ensure_ascii=False, indent=2) + '\n')
PY
  PY_VER="$BUMP"; TS_VER="$BUMP"
fi

# 内容指纹：白名单文件按固定顺序拼「相对路径 + NUL + 字节 + NUL」再 sha256。
# 与 build_extension.sh 的 digest() 逐字节同口径，前端锁定测试可复刻。
digest() {
  local base="$1"; shift
  python3 - "$base" "$@" <<'PY'
import hashlib, os, sys
base = sys.argv[1]
h = hashlib.sha256()
for name in sys.argv[2:]:
    p = os.path.join(base, name)
    h.update(name.encode() + b'\0')
    if os.path.isfile(p):
        h.update(open(p, 'rb').read())
    else:
        h.update(b'<missing>')
    h.update(b'\0')
print(h.hexdigest())
PY
}
PY_DIGEST="$(digest "$PY_SRC" "${PY_FILES[@]}")" || die "计算 Python SDK 源码指纹失败"
TS_DIGEST="$(digest "$TS_SRC" "${TS_FILES[@]}")" || die "计算 TypeScript SDK 源码指纹失败"

# 托管产物命名（带版本号 = 页面安装命令里的真实文件名；latest 别名 = 外部留存稳定链）
WHL="langcross_translator-$PY_VER-py3-none-any.whl"
SDIST="langcross_translator-$PY_VER.tar.gz"
TGZ="langcross-translator-sdk-$TS_VER.tgz"
WHL_LATEST="langcross_translator-latest-py3-none-any.whl"
SDIST_LATEST="langcross_translator-latest.tar.gz"
TGZ_LATEST="langcross-translator-sdk-latest.tgz"
MANIFEST="manifest.json"
# 每个托管文件对应的源码指纹（latest 别名与带版本号副本同 lane 同指纹）
lane_digest() {
  case "$1" in
    "$WHL"|"$SDIST"|"$WHL_LATEST"|"$SDIST_LATEST") printf '%s\n' "$PY_DIGEST" ;;
    "$TGZ"|"$TGZ_LATEST") printf '%s\n' "$TS_DIGEST" ;;
    *) die "未知托管文件：$1" ;;
  esac
}

mkdir -p "$OUT_DIR" || die "无法创建 $OUT_DIR"

# --check：漂移闸门。产物与 .sha256 必须成对在位、记录的指纹与当前源码一致、
# manifest 里的文件名跟当前版本对齐；源产物在盘时再加一层字节对照。
if [ "$MODE" = "check" ]; then
  for f in "$WHL" "$SDIST" "$TGZ" "$WHL_LATEST" "$SDIST_LATEST" "$TGZ_LATEST" "$MANIFEST"; do
    [ -f "$OUT_DIR/$f" ] || die "托管产物缺失：public/sdk/$f（请先跑 scripts/build_sdk.sh）"
    [ -s "$OUT_DIR/$f" ] || die "托管产物为空文件：public/sdk/$f"
  done
  for f in "$WHL" "$SDIST" "$TGZ" "$WHL_LATEST" "$SDIST_LATEST" "$TGZ_LATEST"; do
    fp="$OUT_DIR/$f.sha256"
    [ -f "$fp" ] || die "指纹文件缺失：public/sdk/$f.sha256（产物与指纹必须成对提交）"
    OLD="$(tr -d '[:space:]' < "$fp")"
    WANT="$(lane_digest "$f")"
    [ "$OLD" = "$WANT" ] || die "交付漂移：$f.sha256 记 $OLD，当前源码指纹 $WANT —— 请重跑 scripts/build_sdk.sh 并一并提交产物"
  done
  # manifest 是前端取文件名的唯一入口，落后于版本就等于页面挂死链
  python3 - "$OUT_DIR/$MANIFEST" "$PY_VER" "$TS_VER" "$WHL" "$TGZ" <<'PY' || die "manifest.json 与当前版本不一致（重跑 scripts/build_sdk.sh）"
import json, sys
p, pv, tv, whl, tgz = sys.argv[1:6]
d = json.load(open(p, encoding='utf-8'))
assert d['python']['version'] == pv and d['python']['wheel'] == whl, 'python 段落后于 pyproject 版本'
assert d['typescript']['version'] == tv and d['typescript']['tarball'] == tgz, 'typescript 段落后于 package.json 版本'
PY
  # 源产物在盘（本地开发态）时比对字节：抓「只重打包了 sdk/python/dist、忘了跑本脚本同步托管」
  byte_sha() { python3 -c 'import hashlib,sys;print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"; }
  for pair in "$PY_SRC/dist/$WHL|$WHL" "$PY_SRC/dist/$SDIST|$SDIST" "$TS_SRC/$TGZ|$TGZ"; do
    src="${pair%%|*}"; dst="${pair##*|}"
    if [ -f "$src" ]; then
      A="$(byte_sha "$src")"; B="$(byte_sha "$OUT_DIR/$dst")"
      [ "$A" = "$B" ] || die "字节漂移：$dst 与源产物 $src 不一致 —— 源已重打包但托管副本未刷新，请重跑 scripts/build_sdk.sh"
    fi
  done
  echo "✅ public/sdk/ 托管产物与 SDK 源码一致（python $PY_VER / typescript $TS_VER）"
  exit 0
fi

# build：whl/sdist 必须是现成构建产物（构建属 SDK 自身发布动作，本脚本只做收口搬运）
[ -f "$PY_SRC/dist/$WHL" ] || die "Python 产物缺失：sdk/python/dist/$WHL —— 请先在 sdk/python 跑 python -m build（或确认 pyproject 版本）"
[ -f "$PY_SRC/dist/$SDIST" ] || die "Python 产物缺失：sdk/python/dist/$SDIST —— 请先在 sdk/python 跑 python -m build"

# TS 产物：优先消费 sdk/typescript 下已存在的同名 tgz；没有才 npm pack
# （纯本地目录打包，无网络依赖；npm 不可用时如实报错，--check 形态不依赖 npm 仍可用）
TGZ_PACKED_HERE=0   # 记录 tgz 是否本次 npm pack 生成——只清理自己造的中间产物，不动别人预置的
if [ ! -f "$TS_SRC/$TGZ" ]; then
  command -v npm >/dev/null || die "npm 不可用且缺少现成产物 sdk/typescript/$TGZ —— 请在有 npm 的环境生成后重试"
  echo "==> npm pack 生成 $TGZ"
  PACKED="$(cd "$TS_SRC" && npm pack --silent 2>/dev/null | tail -n1)" || die "npm pack 失败（如实报错，不静默跳过）"
  [ -f "$TS_SRC/$PACKED" ] || die "npm pack 输出异常：找不到 $TS_SRC/$PACKED"
  [ "$PACKED" = "$TGZ" ] || die "npm pack 产物名 $PACKED 与期望 $TGZ 不符（package.json name/version 变了？同步更新本脚本命名）"
  TGZ_PACKED_HERE=1
fi

echo "==> 刷新 public/sdk/（python $PY_VER / typescript $TS_VER）"
rm -f "$OUT_DIR/$WHL" "$OUT_DIR/$SDIST" "$OUT_DIR/$TGZ"
cp -f "$PY_SRC/dist/$WHL" "$OUT_DIR/$WHL" || die "拷入 whl 失败"
cp -f "$PY_SRC/dist/$SDIST" "$OUT_DIR/$SDIST" || die "拷入 sdist 失败"
cp -f "$TS_SRC/$TGZ" "$OUT_DIR/$TGZ" || die "拷入 tgz 失败"
# latest 别名 = 同源字节拷贝（外部书签/文档里的稳定链，页面自身用的是 manifest 里的带版本名）
cp -f "$OUT_DIR/$WHL" "$OUT_DIR/$WHL_LATEST"
cp -f "$OUT_DIR/$SDIST" "$OUT_DIR/$SDIST_LATEST"
cp -f "$OUT_DIR/$TGZ" "$OUT_DIR/$TGZ_LATEST"
# 本次 npm pack 生成的中间 tgz 不留在源码树里（已进 public/sdk，git 里出现只会添乱）；
# 若是运行方预置的现成产物则原样保留。
if [ "$TGZ_PACKED_HERE" = "1" ]; then
  rm -f "$TS_SRC/$TGZ"
fi

# manifest.json：前端 SdkP.tsx 运行时 fetch 的唯一文件名来源（组件源码不落版本号）
python3 - "$OUT_DIR/$MANIFEST" "$PY_VER" "$TS_VER" "$WHL" "$SDIST" "$TGZ" \
  "$WHL_LATEST" "$SDIST_LATEST" "$TGZ_LATEST" <<'PY' || die "写 manifest.json 失败"
import json, sys
p, pv, tv, whl, sdist, tgz, wl, sl, tl = sys.argv[1:10]
d = {
    '_comment': '由 scripts/build_sdk.sh 生成，是后台 SDK 页取交付物文件名的唯一事实源；改版本请重跑脚本，勿手改。',
    'python': {'version': pv, 'wheel': whl, 'sdist': sdist, 'wheelLatest': wl, 'sdistLatest': sl},
    'typescript': {'version': tv, 'tarball': tgz, 'tarballLatest': tl},
}
open(p, 'w', encoding='utf-8').write(json.dumps(d, ensure_ascii=False, indent=2) + '\n')
PY

for f in "$WHL" "$SDIST" "$TGZ" "$WHL_LATEST" "$SDIST_LATEST" "$TGZ_LATEST"; do
  lane_digest "$f" > "$OUT_DIR/$f.sha256" || die "写指纹文件失败：$f.sha256"
done

echo "✅ 产物已刷新："
for p in "$WHL" "$SDIST" "$TGZ" "$WHL_LATEST" "$SDIST_LATEST" "$TGZ_LATEST"; do
  printf '   public/sdk/%s (%s 字节)\n' "$p" "$(wc -c < "$OUT_DIR/$p" | tr -d ' ')"
done
echo "   站点下载路径：/sdk/$WHL、/sdk/$SDIST、/sdk/$TGZ（另有 -latest 别名）"
echo "⚠️ 生效条件：产物落在 public/ 下，属于前端构建产物——只换 /opt/translator/web 即生效，不需要换后端二进制。"
