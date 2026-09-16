#!/usr/bin/env bash
# ============================================================================
# release-sdk.sh — SDK 三端统一发布管线（npm / PyPI /（可选）Maven）
# ★ P2（2026-09-15，见《P0P2待办核实报告_20260915.md》P2-3）：
#   现状缺口：TS/Python/Java SDK 只有源码与裸 package.json，无版本同步、
#   无构建产物归档、发布全靠手敲命令——极易发错版本或发布未构建的旧 dist。
#
# 用法：
#   scripts/release-sdk.sh                     # 校验三端版本一致 + 构建（缺省不发布）
#   scripts/release-sdk.sh 1.1.0               # bump 三端到 1.1.0 后构建校验
#   scripts/release-sdk.sh 1.1.0 --publish     # 构建后真实发布（需登录态，见下）
#
# 发布前置（一次性）：
#   npm login（或 NPM_TOKEN + //registry.npmjs.org/:_authToken= 于 .npmrc）
#   pip install twine && ~/.pypirc 配置 PyPI token（TWINE_USERNAME/__token__）
#   Maven 中心仓发布涉及 GPG/sonatype 账号，脚本仅做版本 bump，发由 CI 或人工执行
# ============================================================================
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TS_DIR="$ROOT/sdk/typescript"
PY_DIR="$ROOT/sdk/python"
JAVA_POM="$ROOT/sdk/java/pom.xml"

VERSION="${1:-}"
PUBLISH="0"
[[ "${2:-}" == "--publish" || "${1:-}" == "--publish" ]] && PUBLISH=1
[[ "$VERSION" == "--publish" ]] && VERSION=""

say(){ echo "==> $*"; }
die(){ echo "✗ $*" >&2; exit 1; }

# ---------- 版本读取与 bump ----------
ts_ver=$(python3 -c "import json;print(json.load(open('$TS_DIR/package.json'))['version'])")
# pyproject 版本用 tomllib 权威解析（sed 会把行尾注释一并带出——2026-09-15 试跑发现）
py_ver=$(python3 - "$PY_DIR/pyproject.toml" <<'EOF'
import sys
try:
    import tomllib
except ImportError:  # py<3.11 兜底：行内注释感知的正则
    import re
    for line in open(sys.argv[1], encoding='utf-8'):
        m = re.match(r'^version = "([^"]+)"', line)
        if m:
            print(m.group(1)); break
    sys.exit(0)
with open(sys.argv[1], 'rb') as f:
    print(tomllib.load(f)['project']['version'])
EOF
)
mod_ver=$(sed -nE 's/^__version__ = "([^"]+)"/\1/p' "$PY_DIR/translator_sdk.py" | head -1)
java_ver=$(python3 - "$JAVA_POM" <<'EOF'
import sys, xml.etree.ElementTree as ET
ns={'m':'http://maven.apache.org/POM/4.0.0'}
r=ET.parse(sys.argv[1]).getroot()
print(r.find('m:version',ns).text)
EOF
)
say "当前版本：ts=$ts_ver py(pyproject)=$py_ver py(module)=$mod_ver java=$java_ver"

if [[ -n "$VERSION" ]]; then
  say "bump 三端 → $VERSION"
  ( cd "$TS_DIR" && npm version "$VERSION" --no-git-tag-version --allow-same-version >/dev/null )
  sed -i.bak -E "s/^version = \"[^\"]+\"/version = \"$VERSION\"/" "$PY_DIR/pyproject.toml" && rm -f "$PY_DIR/pyproject.toml.bak"
  sed -i.bak -E "s/^__version__ = \"[^\"]+\"/__version__ = \"$VERSION\"/" "$PY_DIR/translator_sdk.py" && rm -f "$PY_DIR/translator_sdk.py.bak"
  # java：只改工程版本（第一个 <version>），依赖版本不动——BSD/GNU sed 均可移植的 python 实现
  python3 - "$JAVA_POM" "$VERSION" <<'EOF'
import sys, re
p, v = sys.argv[1], sys.argv[2]
s = open(p).read()
s2 = re.sub(r'(<artifactId>[^<]*</artifactId>\s*<version>)[^<]+(</version>)', r'\g<1>' + v + r'\g<2>', s, count=1)
if s2 == s:
    sys.exit('java pom 工程版本行未匹配，请手工核对')
open(p, 'w').write(s2)
EOF
  ts_ver=$VERSION; py_ver=$VERSION; mod_ver=$VERSION; java_ver=$VERSION
fi
[[ "$ts_ver" == "$py_ver" && "$py_ver" == "$mod_ver" && "$mod_ver" == "$java_ver" ]] \
  || die "三端版本不一致（${ts_ver}/${py_ver}/${mod_ver}/${java_ver}）——先执行 bump 再发布"

# ---------- 行为级测试门禁（2026-09-16 测试盲区补全：TS node:test + Python unittest）----------
say "运行 TS SDK 行为级测试（node:test）"
( cd "$TS_DIR" && npm install --no-audit --no-fund >/dev/null 2>&1 )
( cd "$TS_DIR" && npm test --silent ) || die "TS SDK 测试失败——先修绿再发布"
say "运行 Python SDK 行为级测试（unittest）"
( cd "$PY_DIR" && python3 -m unittest test_translator_sdk ) || die "Python SDK 测试失败——先修绿再发布"

# ---------- 构建与自检 ----------
say "构建 TypeScript SDK（tsc → dist/）"
[[ -d "$TS_DIR/node_modules/typescript" ]] || ( cd "$TS_DIR" && npm install --no-audit --no-fund >/dev/null ) \
  || die "npm install 失败（发布机需可访问 registry 或预装 node_modules）"
( cd "$TS_DIR" && npm run build --silent )
[[ -f "$TS_DIR/dist/index.js" && -f "$TS_DIR/dist/index.d.ts" ]] || die "ts 构建产物缺失（dist/index.js|.d.ts）"
node -e "const m=require('$TS_DIR/dist/index.js'); if(!m.TranslatorClient||!m.TranslatorError) throw new Error('导出面缺失')" \
  || die "npm 包导出面自检失败"

say "构建 Python 包（sdist + wheel）"
if ! python3 -m build --help >/dev/null 2>&1; then
  die "缺 build 模块：pip install build（一次性）"
fi
rm -rf "$PY_DIR/dist"
( cd "$PY_DIR" && python3 -m build >/dev/null )
ls "$PY_DIR"/dist/*.whl "$PY_DIR"/dist/*.tar.gz >/dev/null || die "py 构建产物缺失"
# 冒烟：wheel 装入临时 venv 并 import 版本一致（离线环境 pip 走本地 dist 索引即可）
TMPV=$(mktemp -d)
python3 -m venv "$TMPV/venv" >/dev/null
"$TMPV/venv/bin/pip" install --quiet --no-index --find-links "$PY_DIR/dist" langcross-translator \
  || die "wheel 安装冒烟失败"
"$TMPV/venv/bin/python" -c "import translator_sdk,sys;sys.exit(0 if translator_sdk.__version__=='$py_ver' else 1)" \
  || die "wheel 版本与声明不一致"
say "npm pack 预演（校验 files 白名单/钩子）"
( cd "$TS_DIR" && npm pack --dry-run >/dev/null )
rm -rf "$TMPV"

if [[ "$PUBLISH" != "1" ]]; then
  say "完成（未发布）。正式发布：scripts/release-sdk.sh $ts_ver --publish"
  exit 0
fi

# ---------- 发布（显式 --publish 才走到这里） ----------
say "发布 npm @langcross/translator-sdk@$ts_ver"
( cd "$TS_DIR" && npm publish --access public )
say "发布 PyPI langcross-translator@$py_ver"
python3 -m twine upload --repository pypi "$PY_DIR"/dist/* \
  || die "twine 发布失败（检查 ~/.pypirc / TWINE_PASSWORD）"
say "✓ 三端发布完成：$ts_ver（Maven 如需要请另行 mvn deploy）"
say "提醒：在 sdk/CHANGELOG.md 追加 $ts_ver 条目（若尚未写）"
