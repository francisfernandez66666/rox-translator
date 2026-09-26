#!/usr/bin/env bash
# ============================================================================
# deploy/smoke_brand_homepage.sh — 品牌首屏冒烟（★ F-46，2026-09-26 批 I-9）
#
# 为什么需要这条公网冒烟（Go 单测已经绿了还不够）：
#   F-46 的实测形态是「演示站首页 2,080,056 B」——品牌图以 base64 dataURI 整串存库，
#   spa.go 每次首页请求把整包品牌 JSON 注进 </head> 前，**HTML 体积 = 图片体积**。
#   这有两层只有线上才看得出来：
#     ① 存量库里那行 2 MB 的值不会因为代码修好就自己变小，只有**首屏真回出 URL**
#        才算修好（读侧惰性落件这条路径，本地临时库证不了生产存量形态）；
#     ② 换没换 translator-server 二进制、Caddy/Cloudflare 那层有没有把老壳缓存住，
#        也只有打公网域名才知道（AGENTS §一·5：后端直出面改动必须换二进制）。
#   故本脚本按「字节 + 内容形状」双判据打线上，而不是只判 200。
#
# 判据（与 AGENTS §一·6 托管物口径同族：状态码诚实还不够，内容必须真是那个东西）：
#   A. 首页 HTML 体积 < MAX_KB（默认 30 KB；无品牌外壳实测 ~2.6 KB，演示站修好后 13.8 KB）
#   B. 注入的 window.__BRANDING__ 里 brand_logo / brand_home_bg 若存在 ⇒ 必须是
#      http(s) 或根相对路径，且**不得**是 dataURI
#   C. 整个 HTML 里搜不到 data:image（B 是结构化判据，C 是全文兜底判据：
#      防止有人把品牌图挪到别的注入点绕过 B）
#   D. 品牌件地址本身可达：200 + Content-Type=image/* + 体首是图片魔数 + 非 HTML 兜底 + 体积达标
#      （落件后必须真取得到字节，否则前台只是「碎图 + 看不出错」）
#   E. 不存在的品牌件路径 ⇒ 如实 4xx JSON，**不得**回 200 整页 HTML
#      （spa.go 的 "/" 兜底会把缺件吃成 200 壳，正是本轮 UAT 抓出的托管物陷阱）
#
# 用法：
#   bash deploy/smoke_brand_homepage.sh                       # 默认打主站（无品牌 ⇒ B/C 走空值分支）
#   BASE=https://rox-test.lexicorn.cn bash deploy/smoke_brand_homepage.sh   # 打演示站（含品牌图 ⇒ D 也跑）
#   EXPECT_BRAND_IMAGES=2 bash deploy/smoke_brand_homepage.sh # 钉住「品牌图正好 2 张」
#   MAX_KB=8 bash deploy/smoke_brand_homepage.sh              # 收紧体积上限
#   RUN_NEG=0 bash ...                                        # 目标不是本后端时跳过负向探针
#   bash deploy/smoke_brand_homepage.sh --selftest            # ★ 自检：绿态必须 0、红态必须非 0
#     （自检只在**本机**造绿/红各态（静态夹具 + 临时库实例），唯一的外部只读请求是
#      打一次演示站取"部署前现状基线"——那一条是有意保留的：它同时验判据抓得住真缺陷。
#      首轮要编译后端，约 1~2 分钟；离线也能跑，把 SELFTEST_SKIP_DEMO=1 挂上即跳过外部请求）
#
# 退出码：0=全绿；1=有红项（红项逐条点名，不静默）。
# 只读脚本：对目标站点全程纯 GET，不写库、不改任何线上配置
#   （--selftest 会自建临时库/临时目录起本机实例，与生产无关，跑完即删）。
# ============================================================================
set -euo pipefail

BASE="${BASE:-https://langcross.lexicorn.cn}"
MAX_KB="${MAX_KB:-30}"                                 # 首页 HTML 上限（KB）
EXPECT_BRAND_IMAGES="${EXPECT_BRAND_IMAGES:-}"         # 可选：钉住品牌图张数
RUN_NEG="${RUN_NEG:-1}"                                # 负向探针开关
SKIP_HOMEPAGE_PROBE="${SKIP_HOMEPAGE_PROBE:-0}"        # ★ 仅自检用：不请求首页，只按 FORCE_URLS 验判据 D
FORCE_URLS="${FORCE_URLS:-}"                           # ★ 仅自检用：每行一个品牌件地址（模拟已注入的地址）
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fails=0
note() { printf '%s\n' "$*"; }
red()  { fails=$((fails + 1)); printf '  ✗ %s\n' "$*"; }
ok()   { printf '  ✓ %s\n' "$*"; }

# ===========================================================================
# --selftest：验「判据本身有效」——绿态必须 0 退出、红态必须非 0 退出。
#   为什么必须有这一段（AGENTS §三「修复要同步补断言」+ 本轮 UAT 的假绿教训）：
#   冒烟脚本自己就是闸门，而闸门最容易坏在"解析不到就静默通过"——
#   例如注入正则失配时若无 BAD 分支，A/C 照样绿、B/D 整段跳过，脚本会永远全绿。
#   红态用**手造的修复前直出页**（等价于 HEAD~ 的 spa.go 逐字注入 dataURI），
#   不依赖旧二进制；绿态与 D/E 的红态用真服务端（起临时库实例，跑完即关）。
# ===========================================================================
if [ "${1:-}" = "--selftest" ]; then
  REPO="$(cd "$(dirname "$0")/.." && pwd)"
  st_fail=0
  st_case() { # $1=用例名 $2=期望 green|red $3..=环境变量赋值
    local name="$1" want="$2"; shift 2
    local out rc=0
    out="$(env "$@" bash "$REPO/deploy/smoke_brand_homepage.sh" 2>&1)" || rc=$?
    LAST_OUT="$out"
    if [ "$want" = green ] && [ "$rc" -eq 0 ]; then
      printf '  ✅ 自检 %s：绿态 exit 0\n' "$name"
    elif [ "$want" = red ] && [ "$rc" -ne 0 ]; then
      printf '  ✅ 自检 %s：红态 exit %s（预期非 0）\n' "$name" "$rc"
    else
      printf '  ❌ 自检 %s：期望 %s，实际 exit %s\n' "$name" "$want" "$rc"
      printf '%s\n' "$out" | sed 's/^/      | /'
      st_fail=$((st_fail + 1))
    fi
  }

  # —— 用例 1（绿）：真品牌件 + 正确 Content-Type（静态夹具只能验 A/B/C/D；
  #             E 的"缺件必须如实 4xx"是本后端特有语义，放用例 4 用真服务端验）。——
  python3 - "$TMP/static" <<'PY'
import base64, os, re, sys
d = sys.argv[1]
os.makedirs(d + '/brand', exist_ok=True)
png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg==')
open(d + '/brand/logo.png', 'wb').write(png * 12)        # 放大到 >128 B，过体积判据
open(d + '/brand/bg.png', 'wb').write(png * 20)
inj = ('<script id="__branding__">window.__BRANDING__={"success":true,"tenant_id":0,'
       '"brand_name":"能言 LangCross","brand_logo":"/brand/logo.png","brand_home_bg":"/brand/bg.png"};</script>')
shell = open(d + '/index.html').read() if os.path.exists(d + '/index.html') else ''
html = ('<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"/><title>LangCross</title>'
        + '<!--' + '首屏外壳填充' * 60 + '-->' + inj + '</head><body><div id="root"></div></body></html>')
open(d + '/index.html', 'w', encoding='utf-8').write(html)
assert 'data:image' not in html
PY
  ( cd "$TMP/static" && exec python3 -m http.server 8788 --bind 127.0.0.1 >/dev/null 2>&1 ) &
  ST_PID=$!
  sleep 1
  st_case "品牌件齐备（A/B/C/D 全绿；E 的 4xx 语义不属静态器，交由用例 4 的真服务端验）" green \
    "BASE=http://127.0.0.1:8788" "EXPECT_BRAND_IMAGES=2" "RUN_NEG=0"
  kill "$ST_PID" 2>/dev/null || true; wait "$ST_PID" 2>/dev/null || true

  # —— 用例 2（红）：修复前的直出页——品牌 dataURI 整串进 HTML，并带 SPA 兜底壳 ——
  mkdir -p "$TMP/legacy"
  python3 - "$TMP/legacy" <<'PY'
import base64, os, sys
d = sys.argv[1]
blob = base64.b64encode(b'\x89PNG\r\n\x1a\n' + b'LEGACY-DATAURI-' * 30000).decode()  # ≈0.6 MB
html = ('<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"/><title>LangCross</title>'
        + '<script id="__branding__">window.__BRANDING__={"success":true,"brand_logo":"data:image/png;base64,'
        + blob + '","brand_home_bg":"data:image/png;base64,' + blob + '"};</script>'
        + '</head><body><div id="root"></div></body></html>')
open(d + '/index.html', 'w', encoding='utf-8').write(html)
os.makedirs(d + '/brand', exist_ok=True)   # 模拟 spa.go 的 "/" 兜底：任何路径都回整页 HTML
open(d + '/brand/404.html', 'w', encoding='utf-8').write(html)
PY
  # 兜底页要在"任意 /brand/*"上生效，http.server 做不到；这里用 404.html 作为 D 的抓取目标之外
  # 的形态——本用例只需 A/B/C 三条红，故把品牌地址改成 dataURI 后 D 天然不参与（无 url 行）。
  ( cd "$TMP/legacy" && exec python3 -m http.server 8788 --bind 127.0.0.1 >/dev/null 2>&1 ) &
  ST_PID=$!
  sleep 1
  st_case "dataURI 漏进首屏（A/B/C 必须红）" red \
    "BASE=http://127.0.0.1:8788" "EXPECT_BRAND_IMAGES=" "RUN_NEG=0"
  kill "$ST_PID" 2>/dev/null || true; wait "$ST_PID" 2>/dev/null || true

  # —— 用例 3（绿）：无品牌站＝首页没有 __branding__ 注入，必须判绿。
  #    线上主站实测就是这个形态（直出裸 dist 外壳 2,227 B、无注入），所以"未注入"
  #    绝不能判红，否则这条冒烟天天红、钝化成噪声（同 F-69 那轮学到的"闸门要能区分缺陷与常态"）。
  #    ★ 本例刻意用**本地静态夹具**而不是真打主站：自检要可离线重复跑，
  #       公网链路（Cloudflare 偶发 curl 28/16）会把"判据是否有效"混成"网络是否抖"。
  mkdir -p "$TMP/nobrand"
  printf '<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"/><title>LangCross</title>' \
    >"$TMP/nobrand/index.html"
  printf '<!--%s--></head><body><div id="root"></div></body></html>\n' "$(printf '外壳填充%.0s' $(seq 1 200))" \
    >>"$TMP/nobrand/index.html"
  ( cd "$TMP/nobrand" && exec python3 -m http.server 8788 --bind 127.0.0.1 >/dev/null 2>&1 ) &
  ST_PID=$!
  sleep 1
  st_case "无品牌站（未注入＝正常态）" green \
    "BASE=http://127.0.0.1:8788" "EXPECT_BRAND_IMAGES=" "RUN_NEG=0"
  kill "$ST_PID" 2>/dev/null || true; wait "$ST_PID" 2>/dev/null || true

  if [ "${SELFTEST_SKIP_DEMO:-0}" = "1" ]; then
    note "  （SELFTEST_SKIP_DEMO=1：跳过打演示站的外部只读基线例）"
  else
  # —— 用例 3b（现状基线）：演示站品牌图仍是库里的 dataURI ⇒ 必须红（判据抓得住真缺陷）。
  #    这一例是"部署前红、部署后绿"的对照：换 translator-server 后重跑
  #    `BASE=https://rox-test.lexicorn.cn EXPECT_BRAND_IMAGES=1 bash deploy/smoke_brand_homepage.sh`
  #    应当转绿（brand_home_bg 已按用户指令清空、brand_logo 收敛成 /brand/ 地址）。
  st_case "演示站存量 dataURI（部署前应红）" red \
    "BASE=https://rox-test.lexicorn.cn" "EXPECT_BRAND_IMAGES=" "RUN_NEG=0"
  fi

  # —— 用例 4（绿 + 红）：真服务端（临时库实例）——存量 dataURI 经读侧收敛应全绿；
  #             把落好的品牌件从盘上删掉 ⇒ 判据 D 必须红（"库里是地址"不等于"件取得到"）。
  BIN="$TMP/translator-selftest"
  ( cd "$REPO/backend-go" && go build -o "$BIN" ./cmd/server ) || true
  if [ -x "$BIN" ]; then
    UDD="$TMP/udata"; mkdir -p "$UDD"
    PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
    # 后台起服**不用子壳 + exec**：子壳会吞掉真实 pid（$! 拿到的是子壳），
    # 这里直接 env 前缀 + & ⇒ $! 就是服务进程本身；日志落文件，起来失败时能点名原因。
    ( cd "$REPO/backend-go" && USER_DATA_DIR="$UDD" DB_DRIVER=sqlite "$BIN" \
        -addr "127.0.0.1:$PORT" -frontend "$REPO/frontend-react/dist" ) >"$TMP/server.log" 2>&1 &
    SRV_PID=$!
    up=0
    for _ in $(seq 1 160); do
      if curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1; then up=1; break; fi
      sleep 0.5
    done
    if [ "$up" != 1 ]; then
      printf '  ❌ 自检 用例 4：临时实例未起来（port=%s pid=%s）\n' "$PORT" "$SRV_PID"
      tail -12 "$TMP/server.log" | sed 's/^/      | /'
      st_fail=$((st_fail + 1))
    else
      DBF="$(python3 - "$UDD" <<'PY'
import os, sqlite3, sys
# 业务库口径：<UserDataDir>/tm.sqlite3（config.DBPath）；为防实现变更，退回逐个扫根目录。
def has_sc(p):
    try:
        c = sqlite3.connect(p)
        n = c.execute("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='system_config'").fetchone()[0]
        c.close()
        return bool(n)
    except Exception:
        return False
cands = [os.path.join(sys.argv[1], 'tm.sqlite3')]
for f in sorted(os.listdir(sys.argv[1])):
    if (f.endswith('.db') or f.endswith('.sqlite3')) and not f.startswith('tm_'):
        cands.append(os.path.join(sys.argv[1], f))
print(next((p for p in cands if os.path.exists(p) and has_sc(p)), ''))
PY
)"
      if [ -z "$DBF" ]; then
        printf '  ❌ 自检 用例 4：找不到含 system_config 的库文件（UDD=%s）\n' "$UDD"; st_fail=$((st_fail + 1))
      else
        python3 - "$DBF" <<'PY'
import base64, json, sqlite3, sys
# 列名以**实际 schema** 为准：system_config(key, value, updated_at)。
# （这里曾写过 (k,v) 直接 OperationalError——列名不能凭印象，必须查库确认。）
png = b'\x89PNG\r\n\x1a\n' + b'LC-BRAND-SELFTEST-' * 3000        # ≈54 KB 合成图，dataURI 后约 72 KB
uri = 'data:image/png;base64,' + base64.b64encode(png).decode()
assert base64.b64decode(uri.split(',', 1)[1]) == png             # 保证是合法 base64（否则读侧回落空＝假绿）
val = json.dumps({'brand_name': '能言 LangCross', 'brand_logo': uri, 'brand_home_bg': uri})
c = sqlite3.connect(sys.argv[1])
c.execute('INSERT OR REPLACE INTO system_config(key, value, updated_at) VALUES(?,?,?)',
          ('platform_branding', val, 'selftest'))
c.commit(); c.close()
PY
        st_case "存量 dataURI 经读侧收敛后仍全绿（真服务端；若这里红＝收敛没生效）" green \
          "BASE=http://127.0.0.1:$PORT" "EXPECT_BRAND_IMAGES=2" "RUN_NEG=1"
        # 红态造法分两步（都走 SKIP_HOMEPAGE_PROBE 短路：**不请求首页**）：
        #   (a) 把件**改写成同字节数的非图片内容** ⇒ 直取地址：200、Content-Type 也对，但魔数不符 ⇒ D 必须红。
        #       （先踩过一条弯路：走"完整首页 + 拉件"这条路造这个态是造不出来的——
        #        请求首页时读侧发现坏件就**当场重写自愈**了，D 再拉自然是绿的。那次踩空
        #        反而暴露并修掉了一个真缺陷：复用前只比 os.Stat 尺寸、不比内容，
        #        坏件会被判"已就位"直接发出去 ⇒ 已补 brandHeadMagicOK 魔数门，
        #        并落进 Go 闸门 brand_assets_test.go 锁⑧。）
        #   (b) 把件**挪走** ⇒ 直取地址：404 ⇒ D 必须红。
        #   两步都不碰首页，才能确保测的是"D 判据本身"，而不是"读侧自愈特性"（后者由 (c) 验）。
        served="$(printf '%s\n' "$LAST_OUT" | grep -E '✓ D ' | grep -o '/brand/[A-Za-z0-9._-]*' | sort -u || true)"
        if [ -z "$served" ]; then
          printf '  ❌ 自检 用例 4：绿态里一个品牌件地址都没抓到，无法造 D 的红态\n'; st_fail=$((st_fail + 1))
        else
          n_served="$(printf '%s\n' "$served" | grep -c . || true)"
          [ "$n_served" = "2" ] || printf '  ⚠ 自检 用例 4：绿态只抓到 %s 个件地址（期望 2 个）\n' "$n_served"
          first="$(printf '%s\n' "$served" | head -1)"
          printf '%s\n' "$first" >"$TMP/one_url"
          cp "$UDD${first}" "$TMP/keep_aside"
          # 同字节数改写：专门绕开"尺寸相符即可复用"这一弱判据（坏件伪装成完好件的能力就在这儿）
          python3 - "$UDD${first}" <<'PY'
import sys
p = sys.argv[1]
n = __import__('os').path.getsize(p)
open(p, 'wb').write(b'x' * n)
PY
          st_case "盘上件被同尺寸写坏（200 但魔数不符，D 必须红）" red \
            "BASE=http://127.0.0.1:$PORT" "SKIP_HOMEPAGE_PROBE=1" "FORCE_URLS=$TMP/one_url" "EXPECT_BRAND_IMAGES=" "RUN_NEG=0"
          cp "$TMP/keep_aside" "$UDD${first}"                 # 复原：下一步只测"缺件"这一件事
          while IFS= read -r f; do
            [ -n "$f" ] || continue
            mv "$UDD$f" "$TMP/moved_aside_$(basename "$f")" 2>/dev/null || true
          done <<EOF
$served
EOF
          printf '%s\n' "$served" >"$TMP/forced_urls"
          st_case "品牌件已从盘上消失且不触发自愈（直取地址必须判红）" red \
            "BASE=http://127.0.0.1:$PORT" "SKIP_HOMEPAGE_PROBE=1" "FORCE_URLS=$TMP/forced_urls" "EXPECT_BRAND_IMAGES=" "RUN_NEG=0"
          # (c) 自愈特性反向确认：件被挪走后**再请求一次首页**，读侧必须重新落件并全绿。
          st_case "件缺失后再请求首页 ⇒ 读侧重新落件（自愈生效，必须转绿）" green \
            "BASE=http://127.0.0.1:$PORT" "EXPECT_BRAND_IMAGES=2" "RUN_NEG=0"
        fi
      fi
    fi
    kill "$SRV_PID" 2>/dev/null || true; wait "$SRV_PID" 2>/dev/null || true
  else
    printf '  ❌ 自检 用例 4：后端编译失败\n'; st_fail=$((st_fail + 1))
  fi

  # —— 用例 5（红）：地址形状合法但**内容不是图**（HTML 壳 / 体积过小）⇒ 判据 D 必须红。
  #    这一例专门堵「D 只判 200」的假绿：地址是对的，件却是壳。
  python3 - "$TMP/badasset" <<'PY'
import os, sys
d = sys.argv[1]
os.makedirs(d + '/brand', exist_ok=True)
shell = ('<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"/><title>LangCross</title>'
         '<script id="__branding__">window.__BRANDING__={"success":true,'
         '"brand_logo":"/brand/as_html.png","brand_home_bg":"/brand/too_small.png"};</script>'
         '</head><body><div id="root"></div></body></html>')
open(d + '/index.html', 'w', encoding='utf-8').write(shell)
# 一个是"200 但内容是 HTML 壳"，一个是"魔数对但只有 64 B"（两条红分支都要被走到）
open(d + '/brand/as_html.png', 'w', encoding='utf-8').write(shell * 3)
open(d + '/brand/too_small.png', 'wb').write(b'\x89PNG\r\n\x1a\n' + b'\x00' * 40)
PY
  ( cd "$TMP/badasset" && exec python3 -m http.server 8788 --bind 127.0.0.1 >/dev/null 2>&1 ) &
  ST_PID=$!
  sleep 1
  st_case "地址合法但件是 HTML 壳/过小（D 必须红）" red \
    "BASE=http://127.0.0.1:8788" "EXPECT_BRAND_IMAGES=2" "RUN_NEG=0"
  kill "$ST_PID" 2>/dev/null || true; wait "$ST_PID" 2>/dev/null || true

  if [ "$st_fail" -ne 0 ]; then
    note "★ 自检失败 $st_fail 项 ⇒ 判据本身不可信，先修脚本再谈线上冒烟"
    exit 1
  fi
  note "自检全绿：9 例（绿 4：品牌件齐备 / 未注入常态 / 存量 dataURI 经读侧收敛 / 缺件后自愈转绿；
#       红 5：dataURI 漏进首屏 A+B+C、演示站现状基线、件被同尺寸写坏 D、件缺失 D、件是 HTML 壳或过小 D）都判对了。"
  exit 0
fi

# ---------------------------------------------------------------------------
# 0) 自检专用短路：只验判据 D（件可达性），不碰首页注入解析那几条
#    —— 为什么要有这条：读侧带"缺件自愈"，一旦再请求首页，被挪走的件会立刻重新落盘，
#       红态就造不出来了。所以这里直接用已知地址打件，把 D 的判据单独钉死。
# ---------------------------------------------------------------------------
if [ "$SKIP_HOMEPAGE_PROBE" = "1" ]; then
  note "品牌件可达性单独验（SKIP_HOMEPAGE_PROBE=1，自检专用）：BASE=$BASE"
  if [ ! -s "$FORCE_URLS" ]; then
    red "0 没给 FORCE_URLS 或文件为空 ⇒ 无地址可验，自检本身失真";
  else
    while IFS= read -r full; do
      [ -n "$full" ] || continue
      b2="$TMP/f.asset"; h2="$TMP/f.hdr"
      code2="$(curl -sS --http1.1 -SL -m 30 -o "$b2" -D "$h2" -w '%{http_code}' "${BASE%/}$full" || echo 000)"
      n2=$(wc -c < "$b2" | tr -d ' ')
      ct2="$(grep -i -E '^content-type:' "$h2" | tail -1 | tr -d '\r' | sed 's/^[^:]*: *//')"
      magic="$(head -c 12 "$b2" 2>/dev/null | od -An -tx1 | tr -d ' \n' || true)"
      fmt=''
      case "$magic" in
        89504e47*) fmt=png ;; ffd8ff*) fmt=jpg ;; 47494638*) fmt=gif ;; 52494646*) fmt=webp ;;
      esac
      if [ "$code2" != "200" ]; then
        red "D ${full} 件 HTTP $code2（期望 200）→ ${BASE%/}$full"
      elif [ -z "$fmt" ]; then
        red "D ${full} 体首字节 ${magic:-<空>} 不是图片魔数（Content-Type=${ct2:-<缺失>}，${n2} B）"
      elif [ "$n2" -lt 128 ]; then
        red "D ${full} 体积 ${n2} B < 128 B ⇒ 疑似占位/截断件"
      else
        ok "D ${full} 件可达：200 + $fmt + $ct2 + ${n2} B"
      fi
    done < "$FORCE_URLS"
  fi
  if [ "$fails" -ne 0 ]; then note "★ 件可达性验红 $fails 项"; exit 1; fi
  note "件可达性全绿（自检短路路径）。"
  exit 0
fi

# ---------------------------------------------------------------------------
# 1) 取首页，跑判据 A / C
# ---------------------------------------------------------------------------
url="$BASE/"
body="$TMP/index.html"
hdr="$TMP/index.hdr"
# 体积按**未解码**字节算（curl 不带 --compressed）：SPA 直出的 HTML 本就不大，
# 若链路上上了 gzip 只会读得更小，不会造成假红；真会造成假红的方向是"变大"。
# 重试 3 次并锁 HTTP/1.1：公网这条链（Cloudflare→Caddy）偶发 `curl: (16) Error in the HTTP2
# framing layer`（自检跑到真站点时实测撞上过）。这种传输层抖动**不许**冒成"缺陷红"——
# 红项必须能指向内容形状，而不是指向某一次握手。
http_code="000"
for _try in 1 2 3; do
  http_code="$(curl -sS --http1.1 -SL -m 30 -o "$body" -D "$hdr" -w '%{http_code}' "$url" || echo 000)"
  [ "$http_code" != "000" ] && break
  sleep 1
done
bytes=$(wc -c < "$body" 2>/dev/null | tr -d ' ' || true); bytes="${bytes:-0}"
note "品牌首屏冒烟：BASE=$BASE"
note "判据：A 首屏体积<${MAX_KB}KB / B 品牌字段是地址不是 dataURI / C 全文无 data:image / D 品牌件真取得到字节 / E 缺件如实 4xx"
note "首页 HTTP $http_code，body ${bytes} B（上限 $((MAX_KB * 1024)) B）"

if [ "$http_code" != "200" ]; then
  red "首页 HTTP ${http_code}（期望 200）→ $url；后续判据无从跑起"
  note "★ 冒烟失败 $fails 项"; exit 1
fi

if [ "$bytes" -ge "$((MAX_KB * 1024))" ]; then
  red "A 首屏 HTML ${bytes} B ≥ 上限 $((MAX_KB * 1024)) B ⇒ 品牌图又跟着 HTML 走了（F-46 复发）"
else
  ok "A 首屏 HTML ${bytes} B < 上限 $((MAX_KB * 1024)) B"
fi

if grep -q -a -i -E 'data:image' "$body"; then
  red "C 首页 HTML 全文里出现 data:image ⇒ dataURI 仍被注进首屏（F-46 复发的字节级证据）"
else
  ok "C 首页 HTML 全文无 data:image"
fi

# ---------------------------------------------------------------------------
# 2) 解析注入的品牌 JSON（判据 B），并把非空图片地址交给判据 D
#    产出两份文件：
#      $TMP/fields —— 每行 "<key>\t<shape>\t<字符数>"，shape ∈ empty|url|datauri|other|BAD
#      $TMP/urls   —— 每行 "<key>\t<完整地址>"（判据 D 要真拉的那批）
# ---------------------------------------------------------------------------
python3 - "$body" "$TMP/fields" "$TMP/urls" "$TMP/inj" <<'PY'
import json, re, sys
html = open(sys.argv[1], encoding='utf-8', errors='replace').read()
m = re.search(r'<script id="__branding__">\s*window\.__BRANDING__\s*=\s*(\{.*?\})\s*;?\s*</script>', html, re.S)
f = open(sys.argv[2], 'w', encoding='utf-8')
u = open(sys.argv[3], 'w', encoding='utf-8')
open(sys.argv[4], 'w', encoding='utf-8').write('1' if m else '0')
if not m:
    # 没注入＝这个域名压根没配品牌（线上主站实测如此：直出裸 dist 外壳 2,227 B、无 __branding__）。
    # 这不是缺陷，故不判红；"该站点到底配没配品牌"由 EXPECT_BRAND_IMAGES 显式钉，钉了没见到才算红。
    sys.exit(0)
try:
    d = json.loads(m.group(1))
except Exception:  # noqa: BLE001 注入体坏了要如实报，但不能把异常对象写进文件（可能带库里的值）
    f.write('BAD\tparse_fail\t0\n'); sys.exit(0)
for k in ('brand_logo', 'brand_home_bg'):
    v = str(d.get(k) or '')
    if v == '':
        f.write(f'{k}\tempty\t0\n')
        continue
    low = v.lower()
    if low.startswith('data:'):
        f.write(f'{k}\tdatauri\t{len(v)}\n'); continue
    if low.startswith('http://') or low.startswith('https://') or (v.startswith('/') and not v.startswith('//')):
        f.write(f'{k}\turl\t{len(v)}\n'); u.write(f'{k}\t{v}\n'); continue
    # 协议相对 //host 与裸字符串都不放行：前者会把浏览器送到外站，后者根本不是地址
    f.write(f'{k}\tother\t{len(v)}\n')
PY

if [ ! -s "$TMP/fields" ] && [ "$(cat "$TMP/inj" 2>/dev/null || echo 0)" = "0" ]; then
  note "B 首页没有 __branding__ 注入（该域名未配品牌 ⇒ 前端用默认外观，属正常态）"
elif grep -q -E '^BAD' "$TMP/fields"; then
  red "B 注入的品牌 JSON 解析不了（$(head -1 "$TMP/fields")）⇒ 注入面形状变了，判据得跟着改"
else
  n_url=0
  while IFS=$'\t' read -r key shape vlen; do
    [ -n "${key:-}" ] || continue
    case "$shape" in
      empty) ok "B ${key} 未配置（空 ⇒ 前端回落默认背景，正常态）" ;;
      url)   n_url=$((n_url + 1)); ok "B ${key} 是图片地址（${vlen} 字符，不是 dataURI）" ;;
      datauri) red "B ${key} 仍是 dataURI（${vlen} 字符）⇒ 库里没收敛或读侧回落失效（F-46 未修好）" ;;
      other) red "B ${key} 既不是 http(s)/根相对地址也不是空（${vlen} 字符）⇒ 注入值形状不受控" ;;
      *) red "B ${key} 出现未知判据形状 ${shape}（脚本自身要修）" ;;
    esac
  done < "$TMP/fields"
  if [ -n "$EXPECT_BRAND_IMAGES" ]; then
    if [ "$n_url" != "$EXPECT_BRAND_IMAGES" ]; then
      red "B 品牌图地址张数 ${n_url} ≠ 期望 ${EXPECT_BRAND_IMAGES}（'站点没注入品牌'也算不符）"
    else
      ok "B 品牌图地址张数 = ${n_url}（符合期望）"
    fi
  fi
fi

# ---------------------------------------------------------------------------
# 3) 判据 D：把地址真拉一次
# ---------------------------------------------------------------------------
if [ -s "$TMP/urls" ]; then
  while IFS=$'\t' read -r key full; do
    [ -n "${full:-}" ] || continue
    case "$full" in
      http://*|https://*) target="$full" ;;
      /*) target="${BASE%/}$full" ;;
      *) continue ;;
    esac
    b2="$TMP/asset.bin"; h2="$TMP/asset.hdr"
    code2="$(curl -sSL -m 30 -o "$b2" -D "$h2" -w '%{http_code}' "$target" || echo 000)"
    n2=$(wc -c < "$b2" | tr -d ' ')
    ct2="$(grep -i -E '^content-type:' "$h2" | tail -1 | tr -d '\r' | sed 's/^[^:]*: *//')"
    magic="$(head -c 12 "$b2" 2>/dev/null | od -An -tx1 | tr -d ' \n' || true)"
    fmt=''
    case "$magic" in
      89504e47*) fmt=png ;; ffd8ff*) fmt=jpg ;; 47494638*) fmt=gif ;; 52494646*) fmt=webp ;;
    esac
    if [ "$code2" != "200" ]; then
      red "D ${key} 指向的件 HTTP $code2（期望 200）→ $target"
    elif head -c 2048 "$b2" | grep -q -i -E '<!doctype html|<html'; then
      red "D ${key} 拿到的是 HTML 壳 ⇒ SPA 兜底吃掉了品牌件（与 F-69 同一陷阱）"
    elif [ -z "$fmt" ]; then
      red "D ${key} 体首字节 ${magic:-<空>} 不是 png/jpg/gif/webp 任一魔数 → $target"
    elif ! printf '%s' "$ct2" | grep -q -i -E '^image/'; then
      red "D ${key} Content-Type=${ct2:-<缺失>}（期望 image/*）→ $target"
    elif [ "$n2" -lt 128 ]; then
      red "D ${key} 体积 ${n2} B < 128 B ⇒ 疑似占位/截断件"
    else
      ok "D ${key} 件可达：200 + ${fmt} + ${ct2} + ${n2} B → ${full}"
    fi
  done < "$TMP/urls"
else
  note "D 跳过（本次注入里没有可拉取的品牌图地址——主站本就没配品牌图）"
fi

# ---------------------------------------------------------------------------
# 4) 判据 E：负向探针（只判 4xx 不够，还得确认不是 HTML 壳）
# ---------------------------------------------------------------------------
neg() {
  local path="$1" b c
  b="$TMP/neg.bin"
  c="$(curl -sS -m 30 -o "$b" -w '%{http_code}' "$BASE$path" || echo 000)"
  case "$c" in
    400|403|404)
      if grep -q -i -E '<!doctype html' "$b"; then
        red "E 负向 $path → $c 但响应体是 HTML（兜底没堵住）"
      else
        ok "E 负向 $path → $c 且非 HTML 兜底"
      fi ;;
    *) red "E 负向 $path → HTTP $c（期望 4xx 且非 HTML 壳）；体首=$(head -c 60 "$b" | tr -d '\n')" ;;
  esac
}
if [ "$RUN_NEG" != "1" ]; then
  note "负向探针已跳过（RUN_NEG=0：目标不是本后端时 4xx 语义不适用）"
else
  neg "/brand/does-not-exist-deadbeef.png"   # 内容寻址名不存在 ⇒ 404 JSON
  neg "/brand/%2e%2e%2fserver.go"            # 编码穿越段 ⇒ 4xx（单层文件名判据）
  neg "/brand/"                              # 目录本身 ⇒ 4xx（无索引可给）
fi

if [ "$fails" -ne 0 ]; then
  note "★ 品牌首屏冒烟失败 $fails 项（判据 A/B/C/D/E 见文件头）"
  note "  排查顺序：① translator-server 是否已换新二进制（品牌面属后端直出）② UserDataDir/brand 目录是否可写"
  note "  ③ 库里品牌字段是否仍是 dataURI（读侧会惰性落件，落件失败才回落空 ⇒ 看日志 brand_image_* 告警）"
  exit 1
fi
note "品牌首屏冒烟全绿：首屏体积受控、品牌字段是地址、件真取得到、缺件如实报错。"
