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
#   退 0＝全部判据过；退 1＝有 BAD（不许换源）；退 2＝参数/文件本身不成立（没东西可验）
#
# 判据清单（都只问"这批改动真送出去了吗／送出去的东西能用吗"，不重复单测已证明的语义）：
#   T1 brotli/gzip 侧车在位且数量对得上——省字节这件事的物理载体就是这两批文件；
#   T2 macOS 脏文件为零（.DS_Store 与 ._ AppleDouble）——历史上两次公网可下载都源自 scp -r／tar 未排除；
#   T3 入口 asset 原文体积 ≤ 上限——懒加载退化（词典被改回静态 import）在这里当场越界；
#   T4 语种 chunk 份数达标——12 语种懒加载的产物就是这些独立 chunk，一份都没有＝①没生效。
#   T5 入口文件名带内容哈希且在 assets/ 下——长缓存与「文件真在才发」那条 Caddy 条件化的前提；
#      2026-10-12 的 CACHE_BUST 逃生阀写法（`entryFileNames: undefined`）会把产物退成固定名 /index.js。
#      判据形状见下方 entry_shape_ok，`FRONTEND_TAR_SELFTEST=1` 会把五种名字喂给同一个函数自证射程。
#   T6 每一对侧车必须能解回原文（★ 2026-10-14 白屏事故新增，见下方那段的因果链）——
#      T1 判"在不在"，T6 判"解不解得开"，两档不可替代。
#      T5b／T6b 是两条判据自证腿：把合格与不合格的样本喂给**同一个**函数，两态读数都要有。
#   T7 部署期补拉的大模型资产（`sherpa-onnx-wasm-main-asr.data`，199 MB）**不得进包**——
#      它 gitignore、由部署机 `scripts/fetch_sherpa_assets.sh` 就地拉取；本机 public/ 落过盘时
#      vite build 会把它拷进 dist ⇒ tar 从 ~6.6 MB 变 ~215 MB，而界面照常、无任何症状（★ 2026-10-15 WASM 批）。
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
  [ -f "$TAR_PATH" ] || { echo "ABORT=2 找不到交付包：${TAR_PATH}（先 npx vite build && node scripts/precompress_dist.mjs 再打包）"; exit 2; }
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
  echo "=== 前端交付包自检（tar 模式：${TAR_PATH}）==="
else
  [ -d "$DIST_DIR" ] || { echo "ABORT=2 找不到 dist 目录：${DIST_DIR}（先 npx vite build）"; exit 2; }
  ROOT="$DIST_DIR"
  list() { find "$ROOT" -type f 2>/dev/null; }
  echo "=== 前端交付包自检（磁盘模式：${DIST_DIR}）==="
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
entry_shape_ok() { # ${1}＝从 index.html 认出来的入口相对路径；返回 0＝形态合格
  case "${1:-}" in
    assets/index-*.js) ;;
    *) return 1 ;;
  esac
  local rest
  rest="${1%.js}"
  rest="${rest#assets/index-}"
  # ★ 内容哈希＝`index-` 之后到 `.js` 之前的**整段**（2026-10-16 修）：Vite 的 [hash] 字母表
  #   是 `[A-Za-z0-9_-]`，**里面就有 `-`**——旧写法取"最后一个 `-` 之后那一段"当哈希，
  #   碰上 `index-1A-0R5X_.js` 这种哈希自带 `-` 的名字会算成 5 字符 ⇒ 判成"名字不随内容变"的
  #   假红（本批换源前实测到，实现是对的、判据错了）。
  #   CACHE_BUST 档在哈希前另加一段标记（`index-20261012a-<hash>`），同样落进这整段里。
  case "$rest" in
    *[!A-Za-z0-9_-]*) return 1 ;; # 只许哈希字母表（挡住 `index-中文.js` 这类）
  esac
  [ "${#rest}" -ge 8 ] || return 1
  case "$rest" in
    *[A-Za-z0-9]*) return 0 ;;    # 还要至少一个字母数字（挡住 `index-________.js`）
    *) return 1 ;;
  esac
}

# T1 侧车：入口那一份必须**两档都在**（br→gzip→identity 是一条回落链，少一档就是半条链）
if [ "$BR" -ge 1 ] && [ "$GZ" -ge 1 ] && [ -n "$ENTRY" ] \
   && [ -f "$ROOT/$ENTRY.br" ] && [ -f "$ROOT/$ENTRY.gz" ]; then
  echo "  OK   T1 brotli＋gzip 侧车在位（共 $BR/$GZ 份），入口两份侧车齐全"
else
  echo "  BAD  T1 侧车不齐（br=$BR gz=${GZ}，入口 .br/.gz $([ -f "$ROOT/${ENTRY}.br" ] && echo 有 || echo 无)/$([ -f "$ROOT/${ENTRY}.gz" ] && echo 有 || echo 无)）" \
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
  echo "  OK   T5 入口名带内容哈希且在 assets/ 下（${ENTRY}）"
else
  echo "  BAD  T5 入口名形态不合（=${ENTRY:-<取不到>}）⇒ 名字不随内容变，长缓存与按名校验都落空"; RC=1
  echo "       ⇒ 核 vite.config.ts 的 build：CACHE_BUST 未设时**整段 rollupOptions 都不许出现**（不是把值写成 undefined）"
fi

# T5b 判据自证：把七种名字喂给**同一个**函数——三正四负，负向腿必须真能判负。
# 没有这一段，「T5 通过」可能只是判据恒真（本仓负向锁的老形态：射程为空比判红更难发现）。
# ★ 第三枚正向（`index-1A-0R5X_.js`）是 2026-10-16 本批实测出来的**哈希自带 `-`** 形态——
#   它正是旧判据的盲区（取"最后一个 `-` 之后那一段"⇒ 算成 5 字符 ⇒ 假红），锁住它才不会改回去。
if entry_shape_ok "assets/index-BBMceqr3.js" && entry_shape_ok "assets/index-20261012a-BRVjfTbB.js" \
   && entry_shape_ok "assets/index-1A-0R5X_.js" \
   && ! entry_shape_ok "" && ! entry_shape_ok "index.js" && ! entry_shape_ok "assets/index.js" \
   && ! entry_shape_ok "assets/index-1234567.js" && ! entry_shape_ok "assets/index-________.js"; then
  echo "  OK   T5b 入口名判据自证（3 例合格放行／固定名＋短哈希名＋全下划线 5 例判负）"
else
  echo "  BAD  T5b 入口名判据自证没过 ⇒ T5 这一档的射程不可信"; RC=1
fi

# T6 侧车必须**解得回原文**（★ 2026-10-14 现网白屏事故立档）：
#   上一代的侧车是用 `BROTLI_PARAM_LARGE_WINDOW=1` 压的，产物头字节里写了 WBITS>24，
#   浏览器解码器窗口上限 16 MiB ⇒ Edge/Chrome 判 `ERR_CONTENT_DECODING_FAILED`
#   ⇒ `<script type=module>` 载入失败 ⇒ 整页纯黑（标签能显示、界面一片黑、零提示）。
#   而 T1 只问"侧车在不在、入口两份齐不齐"，对"能不能解开"完全无感；那一代坏侧车甚至比原文小得多，
#   gzip／原文两档又都正常，于是 vite build／前端单测／T1–T5 全绿而现网白屏——
#   本文件唯一能在换源前抓住它的一档就是这个解码往返。
#   判据对**包里的每一对**侧车都跑（不抽查入口）：坏侧车由压缩参数造成，一坏就是全坏。
T6_JS='
const fs = require("fs"), path = require("path"), z = require("zlib");
const root = process.argv[1];
let checked = 0; const bad = [];
(function walk(d) {
  for (const e of fs.readdirSync(d, { withFileTypes: true })) {
    const p = path.join(d, e.name);
    if (e.isDirectory()) { walk(p); continue; }
    if (!/\.(br|gz)$/.test(e.name)) continue;
    const orig = p.replace(/\.(br|gz)$/, "");
    if (!fs.existsSync(orig)) continue;              // 不是侧车（真 .gz 资产没有同名原文）⇒ 不在射程
    checked++;
    const raw = fs.readFileSync(orig), enc = fs.readFileSync(p);
    try {
      const back = /\.br$/.test(e.name) ? z.brotliDecompressSync(enc) : z.gunzipSync(enc);
      if (Buffer.compare(back, raw) !== 0) bad.push(p + " 解回后与原文不等（" + back.length + " B ≠ " + raw.length + " B）");
    } catch (err) { bad.push(p + " 解不开（浏览器会判解码失败）：" + err.message); }
  }
})(root);
console.log("  T6 明细：checked=" + checked + " bad=" + bad.length);
for (const b of bad) console.log("       SIDECAR_BAD " + b);
process.exit(checked > 0 && bad.length === 0 ? 0 : 1);
'
if ! command -v node >/dev/null 2>&1; then
  echo "  BAD  T6 本机没有 node，侧车解码这一档**一行都没跑**（不许读成通过）"; RC=1
else
  node -e "$T6_JS" "$ROOT"; T6_RC=$?
  if [ "$T6_RC" = "0" ]; then
    echo "  OK   T6 包内每一对 br/gz 侧车都能解回原文"
  else
    echo "  BAD  T6 侧车解码往返没过（有解不开/不等的一份，或一份都没判到＝这一档没有射程）"; RC=1
    echo "       ⇒ 重跑 node scripts/precompress_dist.mjs；核压缩参数里不许出现 BROTLI_PARAM_LARGE_WINDOW"
  fi

  # T6b 射程自证：现攒一份"解不开"的假侧车喂给**同一个**判据函数，必须先放行再判死。
  # 只验"坏的能判死"不够——若整段压根没跑到（比如 root 传错），坏的也判不死；
  # 所以两态都要有读数（同记忆里那条"新读数腿要双态自证"）。
  T6_SELF=$(mktemp -d)
  printf 'hello sidecar self test\nhello sidecar self test\n' > "$T6_SELF/a.txt"
  node -e 'const z=require("zlib"),fs=require("fs");fs.writeFileSync(process.argv[1],z.brotliCompressSync(fs.readFileSync(process.argv[2])))' "$T6_SELF/a.txt.br" "$T6_SELF/a.txt" >/dev/null 2>&1
  node -e "$T6_JS" "$T6_SELF" >/dev/null 2>&1; T6_GOOD=$?
  # 再把它截断成浏览器解不开的那一类（魔数与"比原文小"都还在，只毁内容——正是 T1 抓不到的形态）
  node -e 'const fs=require("fs");const b=fs.readFileSync(process.argv[1]);fs.writeFileSync(process.argv[1],b.subarray(0,Math.max(4,Math.floor(b.length/2))))' "$T6_SELF/a.txt.br" >/dev/null 2>&1
  node -e "$T6_JS" "$T6_SELF" >/dev/null 2>&1; T6_BAD=$?
  rm -rf "$T6_SELF"
  if [ "$T6_GOOD" = "0" ] && [ "$T6_BAD" != "0" ]; then
    echo "  OK   T6b 解码判据自证（好侧车放行／截断侧车判死）"
  else
    echo "  BAD  T6b 解码判据自证没过（good=$T6_GOOD bad=${T6_BAD}）⇒ T6 这一档的射程不可信"; RC=1
  fi
fi

# T7 部署期补拉的大模型资产不得进包（★ 2026-10-15 端侧 WASM 批立档）：
#   `sherpa-onnx-wasm-main-asr.data`（199 MB）超 GitHub 单文件 100MB 硬限 ⇒ gitignore ＋ 部署机
#   `scripts/fetch_sherpa_assets.sh` 就地拉取。它在本机 `public/` 落过盘时，`vite build` 会把它拷进 dist，
#   于是这条链上**没有任何一处会报错**——只是 tar 从 ~6.6 MB 变 ~215 MB、跨境上传拖慢整条换源。
#   判据问包内**有没有这份模型**（按路径），再配一条"单文件体积上限"兜住同族未来资产。
T7_DATA=$(list | grep -cE '/wasm/sherpa-onnx/[^/]*\.data$' || true)
T7_MAX_BYTES="${FRONTEND_SINGLE_FILE_MAX_BYTES:-104857600}" # 100 MiB
T7_BIG=$(find "$ROOT" -type f -size +"${T7_MAX_BYTES}"c 2>/dev/null | wc -l | tr -d ' ')
if [ "${T7_DATA:-0}" = "0" ] && [ "${T7_BIG:-0}" = "0" ]; then
  echo "  OK   T7 包内无部署期补拉的模型 .data（无 >$((T7_MAX_BYTES / 1048576)) MiB 的单文件）"
else
  echo "  BAD  T7 包内有部署期补拉的模型资产（*.data=$T7_DATA 份；>${T7_MAX_BYTES} B 的单文件 $T7_BIG 份）⇒ tar 会被撑到 ~215 MB" && RC=1
  echo "       ⇒ 打包加 --exclude='dist/wasm/sherpa-onnx/*.data'；模型由部署机跑 scripts/fetch_sherpa_assets.sh 就地拉取"
fi

# T7b 射程自证：同一条判据对"含 .data 的路径"必须命中、对普通 asset 必须不命中。
if printf 'dist/wasm/sherpa-onnx/sherpa-onnx-wasm-main-asr.data\n' | grep -qE '/wasm/sherpa-onnx/[^/]*\.data$' \
   && ! printf 'dist/assets/index-abc12345.js\n' | grep -qE '/wasm/sherpa-onnx/[^/]*\.data$'; then
  echo "  OK   T7b 大模型资产判据自证（命中 .data／放行普通 asset）"
else
  echo "  BAD  T7b 大模型资产判据自证没过 ⇒ T7 这一档的射程不可信"; RC=1
fi

echo
if [ "$RC" = "0" ]; then echo "FRONTEND_TAR_ALL_OK=1"; else echo "FRONTEND_TAR_FAIL=1（上面有 BAD 行 ⇒ 不许换源）"; fi
echo CHECK_FRONTEND_TAR_EXIT=$RC
exit "$RC"
