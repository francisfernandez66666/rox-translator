#!/usr/bin/env bash
# ============================================================================
# scripts/check_frontend_tar.sh —— 前端交付包**换源前**的自检（★ 2026-10-12 慢链路批）
#
# 为什么要有这一个文件（而不是把判据写在 deploy_to_production.sh 里）：
#   本批三件事里有两件半是「**静默失败**」形态——预压缩侧车不在时 Caddy 照发原文、界面照常、服务照常起，
#   懒加载退化成静态打进入口时照跑无误，两件事在现网都**没有任何症状**。
#   唯一能在把包送上去之前抓住它们的时刻就是"包本身"，而现网换源历史上既走过
#   `deploy_to_production.sh`，也按《部署指南》§五手工 tar→scp→解到 web.new→两步 mv 走过多次
#   （演示站、租户块根本不在那个脚本的射程里）。把判据锁进一条路径＝另一条路径上判据不存在，
#   所以判据落成这一个**两条路径都能调**的独立文件。
#
# 用法：
#   bash scripts/check_frontend_tar.sh [tar 路径]        # 默认 /tmp/frontend-dist.tar.gz
#   bash scripts/check_frontend_tar.sh --dist <dist 目录> # 直接验磁盘上的 dist（打包前用）
#   退 0＝五档全过；退 1＝有 BAD（不许换源）；退 2＝参数/文件本身不成立（没东西可验）
#
# 五档判据（都只问"这批改动真送出去了吗"，不重复单测已证明的语义）：
#   T1 brotli/gzip 侧车在位且数量对得上——省字节这件事的物理载体就是这两批文件；
#   T2 macOS 脏文件为零（.DS_Store 与 ._ AppleDouble）——历史上两次公网可下载都源自 scp -r／tar 未排除；
#   T3 入口 asset 原文体积 ≤ 上限——懒加载退化（词典被改回静态 import）在这里当场越界；
#   T4 语种 chunk 份数达标——12 语种懒加载的产物就是这些独立 chunk，一份都没有＝①没生效。
#   T5 入口文件名带内容哈希且在 assets/ 下——长缓存与「文件真在才发」那条 Caddy 条件化的前提；
#      2026-10-12 的 CACHE_BUST 逃生阀写法（`entryFileNames: undefined`）会把产物退成固定名 /index.js。
#      判据形状见下方 entry_shape_ok，`FRONTEND_TAR_SELFTEST=1` 会把五种名字喂给同一个函数自证射程。
# ============================================================================
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="$REPO_ROOT/frontend-react/dist"
TAR_PATH="/tmp/frontend-dist.tar.gz"
MODE="tar"

if [ "${1:-}" = "--dist" ]; then
  MODE="dist"
  [ -n "${2:-}" ] || { echo "ABORT=2 --dist 后面要跟目录路径"; exit 2; }
  DIST_DIR="$2"
else
  [ -n "${1:-}" ] && TAR_PATH="$1"
fi

# 上限是**退化哨兵**，不是性能承诺：本批实测入口 raw 605,065 B；改回静态词典会跳回 ~2,633,000 B。
# 1,200,000 给正常增量留了一倍余量（新组件仍会进入口），而"词典又被静态打进入口"一定越过它。
ENTRY_SIZE_MAX="${FRONTEND_ENTRY_SIZE_MAX:-1200000}"
# 语种 chunk 下限：懒加载拆出的独立词典 chunk（zh/en 刻意留在入口，见 src/i18n/index.ts）。
LOCALE_MIN="${FRONTEND_LOCALE_CHUNK_MIN:-10}"

BR=0; GZ=0; DIRTY=0; LOCALE=0; ENTRY=""; ENTRY_SIZE=0; WORK=""
cleanup() { [ -n "$WORK" ] && rm -rf "$WORK"; }
trap cleanup EXIT

if [ "$MODE" = "tar" ]; then
  [ -f "$TAR_PATH" ] || { echo "ABORT=2 找不到交付包：$TAR_PATH（先 npx vite build && node scripts/precompress_dist.mjs 再打包）"; exit 2; }
  WORK="$(mktemp -d)"
  # 只解清单需要的东西代价太大（要读 index.html 与逐个 asset 尺寸），这里整包解到临时目录后按**包内**计数；
  # tar 解出的是一比一的磁盘形态，判据问的就是"落盘后长什么样"。
  tar xzf "$TAR_PATH" -C "$WORK" || { echo "ABORT=2 tar 解包失败（包本身不完整）"; exit 2; }
  # 包内顶层前缀按《部署指南》§五 的打包口径是 `dist/`（服务器侧解包用 --strip-components=1），
  # 手工打包时也可能直接 `tar czf x.tar -C dist .`（无前缀）。两种形态都认：
  # **按 index.html 真在哪一层来定 ROOT**，而不是写死前缀——写死会让"换了打包命令"表现为 ABORT，
  # 而 ABORT 会被读成"包没问题、只是工具没配好"，正是本文件要防的那一族假绿。
  ROOT="$WORK"
  if [ ! -f "$ROOT/index.html" ]; then
    found=$(find "$WORK" -maxdepth 3 -name index.html -type f 2>/dev/null | head -1)
    [ -n "$found" ] && ROOT="$(dirname "$found")"
  fi
  list() { find "$ROOT" -type f 2>/dev/null; }
  echo "=== 前端交付包自检（tar 模式：$TAR_PATH）==="
else
  [ -d "$DIST_DIR" ] || { echo "ABORT=2 找不到 dist 目录：$DIST_DIR（先 npx vite build）"; exit 2; }
  ROOT="$DIST_DIR"
  list() { find "$ROOT" -type f 2>/dev/null; }
  echo "=== 前端交付包自检（磁盘模式：$DIST_DIR）==="
fi

[ -f "$ROOT/index.html" ] || { echo "ABORT=2 $ROOT 里没有 index.html，这不是一个前端包"; exit 2; }

BR=$(list | grep -cE '\.br$' || true)
GZ=$(list | grep -cE '\.gz$' || true)
DIRTY=$(list | grep -cE '(\.DS_Store|/\._[^/]*|^\./\._[^/]*)$' || true)
LOCALE=$(list | grep -cE '/(zh-hant|ja|ko|th|ru|ar|fr|es|de|pt|id|vi|it|nl|pl|tr)-[A-Za-z0-9_-]+\.js$' || true)
ENTRY=$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$ROOT/index.html" 2>/dev/null | head -1)

if [ -n "$ENTRY" ] && [ -f "$ROOT/$ENTRY" ]; then
  ENTRY_SIZE=$(wc -c <"$ROOT/$ENTRY" | tr -d ' ')
else
  ENTRY_SIZE=""
fi

RC=0
echo "  侧车：br=$BR gz=$GZ ｜ 语种 chunk=$LOCALE ｜ 入口=${ENTRY:-<取不到>} raw=${ENTRY_SIZE:-<无>} B ｜ 脏文件=$DIRTY"

# T5 的判据单独成函数：自检模式（文件末尾）与真包判据**共用同一个函数**，
# 否则「自测通过」只证明了一份副本判据，真包走的还是另一份（本仓两份判据打架的老形态）。
entry_shape_ok() { # $1＝从 index.html 认出来的入口相对路径；返回 0＝形态合格
  case "${1:-}" in
    assets/index-*.js) ;;
    *) return 1 ;;
  esac
  local rest last
  rest="${1%.js}"
  rest="${rest#assets/index-}"
  last="${rest##*-}" # 最后一段＝内容哈希（CACHE_BUST 档在它前面另加一段标记）
  [ "${#last}" -ge 8 ] || return 1
  case "$last" in
    *[A-Za-z0-9]*) return 0 ;;
    *) return 1 ;;
  esac
}

# T1 侧车：入口那一份必须**两档都在**（br→gzip→identity 是一条回落链，少一档就是半条链）
if [ "$BR" -ge 1 ] && [ "$GZ" -ge 1 ] && [ -n "$ENTRY" ] \
   && [ -f "$ROOT/$ENTRY.br" ] && [ -f "$ROOT/$ENTRY.gz" ]; then
  echo "  OK   T1 brotli＋gzip 侧车在位（共 $BR/$GZ 份），入口两份侧车齐全"
else
  echo "  BAD  T1 侧车不齐（br=$BR gz=$GZ，入口 .br/.gz $([ -f "$ROOT/${ENTRY}.br" ] && echo 有 || echo 无)/$([ -f "$ROOT/${ENTRY}.gz" ] && echo 有 || echo 无)）" \
    && RC=1
  echo "       ⇒ 换源后静态资源只能发原文；先跑 node scripts/precompress_dist.mjs，再 node scripts/precompress_dist.mjs --check"
fi

# T2 脏文件：必须为 0（Caddy／spa.go 对磁盘上真存在的 dotfile 不拦，落进 web 根即公网可下载）
if [ "$DIRTY" = "0" ]; then
  echo "  OK   T2 macOS 脏文件为 0"
else
  echo "  BAD  T2 包内有 $DIRTY 个 .DS_Store/._* ⇒ 落进 web 根即公网可下载（历史上清过两次，都因换源方式带回）"; RC=1
  echo "       ⇒ 打包一律 COPYFILE_DISABLE=1 tar czf … --exclude='._*' --exclude='.DS_Store'，禁止 scp -r dist"
fi

# T3 入口体积：懒加载退化哨兵（体积判据比"抓一段泰文来 grep"射程稳：文件名带 hash、入口里本就可能有零星非 ASCII 文案）
if [ -z "$ENTRY_SIZE" ]; then
  echo "  BAD  T3 从 index.html 里认不出入口 asset（换源后首页只会拿兜底页）"; RC=1
elif [ "$ENTRY_SIZE" -le "$ENTRY_SIZE_MAX" ]; then
  echo "  OK   T3 入口 raw=${ENTRY_SIZE} B ≤ ${ENTRY_SIZE_MAX} B（懒加载形态）"
else
  echo "  BAD  T3 入口 raw=${ENTRY_SIZE} B 越过 ${ENTRY_SIZE_MAX} B ⇒ 像被打回静态词典（本批实测 605,065 B，改回静态是 2,633 kB 量级）"; RC=1
fi

# T4 语种 chunk：①懒加载的产物本体；一份都没有＝动态 import 没被 Vite 拆出来
if [ "$LOCALE" -ge "$LOCALE_MIN" ]; then
  echo "  OK   T4 语种 chunk=$LOCALE 份 ≥ ${LOCALE_MIN}（词典按语种切开了）"
else
  echo "  BAD  T4 语种 chunk=$LOCALE < ${LOCALE_MIN} ⇒ 词典没有独立 chunk，多半是 import 又写成了静态"; RC=1
fi

# T5 入口名形态：必须落在 assets/ 下、且最后一段是 ≥8 字符的内容哈希。
# 这一档治的是「名字本身不再随内容变」——固定名的代价是三件事一起落空：
#   ① immutable 长缓存没有意义（同名换内容＝老字节被钉一年，反过来新字节拿不到）；
#   ② Caddy 那条「文件真在才发长缓存」的条件化写在 `handle /assets/*` 里，固定名若落在根路径直接不在射程内；
#   ③ 换源后按入口名逐 asset 校验落盘的步骤会找空（《部署指南》§五）。
# 现网来路（2026-10-12 真踩）：CACHE_BUST 逃生阀写成 `output: { entryFileNames: undefined }`，
# 键存在但值为 undefined ⇒ Vite 不再补自己的默认 ⇒ Rollup 用默认 `[name].js` ⇒ 产物是根目录 `index.js`。
if entry_shape_ok "$ENTRY"; then
  echo "  OK   T5 入口名带内容哈希且在 assets/ 下（$ENTRY）"
else
  echo "  BAD  T5 入口名形态不合（=${ENTRY:-<取不到>}）⇒ 名字不随内容变，长缓存与按名校验都落空"; RC=1
  echo "       ⇒ 核 vite.config.ts 的 build：CACHE_BUST 未设时**整段 rollupOptions 都不许出现**（不是把值写成 undefined）"
fi

# T5b 判据自证：把五种名字喂给**同一个**函数——两正三负，负向腿必须真能判负。
# 没有这一段，「T5 通过」可能只是判据恒真（本仓负向锁的老形态：射程为空比判红更难发现）。
if entry_shape_ok "assets/index-BBMceqr3.js" && entry_shape_ok "assets/index-20261012a-BRVjfTbB.js" \
   && ! entry_shape_ok "" && ! entry_shape_ok "index.js" && ! entry_shape_ok "assets/index.js" \
   && ! entry_shape_ok "assets/index-1234567.js"; then
  echo "  OK   T5b 入口名判据自证（2 例合格放行／固定名＋短哈希名 4 例判负）"
else
  echo "  BAD  T5b 入口名判据自证没过 ⇒ T5 这一档的射程不可信"; RC=1
fi

echo
if [ "$RC" = "0" ]; then echo "FRONTEND_TAR_ALL_OK=1"; else echo "FRONTEND_TAR_FAIL=1（上面有 BAD 行 ⇒ 不许换源）"; fi
echo CHECK_FRONTEND_TAR_EXIT=$RC
exit "$RC"
