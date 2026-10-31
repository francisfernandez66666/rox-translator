#!/usr/bin/env bash
# ============================================================================
# deploy/smoke_tenant_domain.sh — 租户品牌域名链路冒烟（★ F-73，2026-09-27 〇-W）
#
# 为什么单独立一条脚本（后端单测 + run_uat 都绿了还不够）：
#   F-73 的缺陷形态是「后端半边全通、Caddy 那半边没有租户域名的站点块」——
#   客户打开自己的品牌域得到 525 或 200 空体，本地任何 Go/vitest 闸门都看不见这一层，
#   因为它断在**反向代理的路由表**上，不在代码里。修复＝生产 Caddy 加 `*.lexicorn.cn`
#   通配块（deploy/caddy/translator-tenant.conf），而通配块一旦被下次发版/别人改配置
#   抹掉，症状会和今天一模一样。所以这条链必须有可复跑的发版验收，而不是记一次成功。
#
# 判据（延续 AGENTS §一·6「只判 200 一律视为无效断言」与 §一·5 直出面口径）：
#   T1 已登记租户域首页：200 + `window.__BRANDING__` 命中恰好 1 + 注入体里的
#      tenant_id / brand_name 与钉住的期望值等值 —— 证明首页是**后端直出**（通配块兜底段
#      必须 reverse_proxy，写成 file_server 就会退化成 F-74 的「静态壳无注入」）。
#   T2 品牌件可达：/brand/*.webp|png 200 + image/* + 图片魔数（webp=RIFF / png=89504e47）
#      + 体积下限 —— 库里存 URL 不等于浏览器取得到字节。
#   T3 未登记子域负向：首页照样 200（通配块接住），但 /api/tenant/branding 必须
#      tenant_id=0 且**不得拿到任何租户的品牌名**（空或＝平台品牌名都可，平台品牌现读主站）
#      —— 防「猜个子域就看到别人的品牌」的串号。
#      ⚠️ 旧口径写的是「brand_name 必须为空」，那在「平台品牌名已配」的机器上结构性恒红
#      （未登记子域按设计回落平台默认品牌），2026-10-28 按上口径修正。
#   T4 接口链诚实：/api/me/tasks 匿名必须 401（不是 200+success:false 的旧信封；
#      裸 /api/me 在这套路由里本来就是 404，别拿它当探针——主站与租户域同形），
#      /api/health 200 且体首是 JSON —— 证明 API 反代真的落到后端而不是 SPA 兜底。
#   T5 托管物：/extensions/langcross-extension-latest.zip 200 + PK + 体积下限、
#      /sdk/manifest.json 200 + JSON（不得落 HTML 兜底）。
#   T6 安全头基线：HSTS / nosniff / Referrer-Policy / Permissions-Policy 四项必须在
#      租户域上同样存在（租户域不得弱于主站）。
#   T7 HTTP ⇒ HTTPS：http:// 必须 301/308 跳 https。
#   T8 反向不串号：主站首页不得出现租户 brand_name（主站被租户域「污染」是另一类事故）。
#
# ★ 2026-10-31 用户令：`rox.lexicorn.cn` 已退役（Caddy 对该 Host 直接 `respond 410`）。
#   它**不再是**「已登记租户域」的验收靶子；默认 `TENANT_BASE` 留空，T1/T2/T4/T5/T6 在没有
#   现役租户域时**跳过（NOT_APPLICABLE，不计数）**，退役行为由 **T0 反向锁**钉住（必须 410 且
#   不返回站点内容）。将来有新的现役租户域时，显式传 TENANT_BASE/EXPECT_TENANT_ID 即可恢复那五段。
#
# 用法（全部只读，纯 GET，不写库不改配置）：
#   bash deploy/smoke_tenant_domain.sh                                   # 现网口径：T0/T3/T7/T8 全跑
#   TENANT_BASE=https://<现役租户域> EXPECT_TENANT_ID=<id> EXPECT_BRAND_NAME=<名> \
#     UNREGISTERED_BASE=https://zzz-nodata-0w.lexicorn.cn bash deploy/smoke_tenant_domain.sh
#   SKIP_ASSETS=1 bash deploy/smoke_tenant_domain.sh                     # 只跑路由/品牌判据（离线快检）
#
# ⚠️ 期望值为什么从接口现读而不是写死：品牌名/图片哈希会随客户运营改动，写死必然变成
#    落后于现实的假绿源；本脚本只钉「结构性不变量」（命中数、tenant_id 归属、空值负向、
#    魔数、状态码语义），EXPECT_BRAND_NAME 允许留空＝只判「非空且与未登记子域不同」。
# 退出码：0=全绿；1=有红项（红项逐条点名）；2=**有 N 项没测到**（链路抖动，既不算通过也不算缺陷）。
#   ★ 发版判据＝`FAIL=0` **且** `UNMEASURED=0`；拿到 2 就复跑一次，别把"没测到"当"通过"。
# ============================================================================
set -uo pipefail

# 现役租户域（★ 2026-10-31 起默认空：默认租户的 rox 已退役；没有靶子就跳过 T1/T2/T4/T5/T6）
TENANT_BASE="${TENANT_BASE:-}"
# 退役域：源站必须对它回 410（T0 反向锁；退役不是"删 DNS 就完事"，源站行为要可判）
RETIRED_BASE="${RETIRED_BASE:-https://rox.lexicorn.cn}"
UNREGISTERED_BASE="${UNREGISTERED_BASE:-https://zzz-nodata-0w.lexicorn.cn}"
MAIN_BASE="${MAIN_BASE:-https://langcross.lexicorn.cn}"
EXPECT_TENANT_ID="${EXPECT_TENANT_ID:-1}"
EXPECT_BRAND_NAME="${EXPECT_BRAND_NAME:-}"             # 留空＝只判「非空」
MIN_BRAND_BYTES="${MIN_BRAND_BYTES:-2048}"
MIN_ZIP_BYTES="${MIN_ZIP_BYTES:-5000}"
SKIP_ASSETS="${SKIP_ASSETS:-0}"

PASS=0
FAIL=0
UNMEASURED=0
ok()  { echo "  ✔ $1"; PASS=$((PASS + 1)); }
bad() { echo "  ✖ $1"; FAIL=$((FAIL + 1)); }
# ★ 2026-10-28：第三态。跨境这一跳（Cloudflare→Caddy）实测会偶发半截体／空回复，
#   那既不是"通过"也不是"产品缺陷"——按 AGENTS §一·5 ⑤ 的口径必须**如实报"没测到"**，
#   单独计数并给退出码 2（发版判据＝FAIL=0 且 UNMEASURED=0；出现 2 就复跑，不许当通过）。
inc() { echo "  ⏳ $1"; UNMEASURED=$((UNMEASURED + 1)); }

# host_of 剥掉协议与端口，得到探针要用的域名
host_of() { echo "$1" | sed -E 's#^[a-z]+://##; s#[:/].*$##'; }
TENANT_HOST=$(host_of "$TENANT_BASE")
RETIRED_HOST=$(host_of "$RETIRED_BASE")
UNREG_HOST=$(host_of "$UNREGISTERED_BASE")
MAIN_HOST=$(host_of "$MAIN_BASE")
# B_NAME1/T1 读到的租户品牌名：没有现役租户域时它保持空（T3/T8 据此判定参照物是否存在）。
B_NAME1=""

# ★ 2026-10-28：公网这一跳（跨境 Cloudflare→Caddy）实测会偶发 `curl: (52) Empty reply`／`(28)`，
#   而它和"配置坏了"在脚本里长得一模一样（都是 code=000）。按 AGENTS §一·5 那条口径分两档：
#   ① 重拨**只针对"这一跳没走完"**（curl_rc≠0、code=000、或收了半截体），第二枪起锁 `--http1.1`
#      ——本仓实测同一分钟同一 URL h2 三连败、换 1.1 立刻 200；
#   ② 判据本身不合格（4xx/5xx、字节不等、品牌空）**一律不重拨**——摇绿灯就是把缺陷藏起来。
#   ⚠️ 「重拨三次仍没走完」必须**如实报成"这一跳没走完"**，不许把半截体交下去判：
#   实测同一份 1.5 MB 品牌图连三次都被截断（768,024／999,397／858 B），而 code 一直是 200、
#   魔数 `RIFF` 也在——旧写法会把"只收到 220 KB"判成"品牌件可达"（判据给半截体发了绿灯）。
CURL_RETRY="${CURL_RETRY:-3}"
CURL_MAX_TIME="${CURL_MAX_TIME:-180}"   # 单次预算；1.5 MB 品牌图跨境实测 ≈23 KB/s，60s 不够

# fetch <url> ⇒ 输出 "code|size|ctype|magic|complete"，正文落 $BODY
#   complete=1 表示这一跳真收完整了（判据可以开始判）；=0 表示重拨三次仍没走完
#   （此时 code 可能仍是 200，但那不是"发到了"，见上面那条实测）。
BODY=$(mktemp)
HDR=$(mktemp)
trap 'rm -f "$BODY" "$HDR"' EXIT

# _complete <code> <size> ⇒ 0＝收完整了；1＝没走完（该重拨／该如实报）
#   有 content-length 就逐字节比对（取**最后一条**：`-L` 跟重定向时标题块有多段，
#   取第一条会把 302 的长度当成终点的）；没有 content-length（分块传输）只能靠 curl_rc 判。
_complete() {
  local code="$1" size="$2" clen
  [ "$code" = "000" ] && return 1
  clen=$(grep -i '^content-length:' "$HDR" 2>/dev/null | tail -1 | tr -dc '0-9')
  [ -n "$clen" ] && [ "$clen" != "$size" ] && return 1
  return 0
}

fetch() {
  local url="$1" out rc code size done=0 i
  for i in $(seq 1 "$CURL_RETRY"); do
    if [ "$i" = "1" ]; then
      out=$(curl -s -L -o "$BODY" -D "$HDR" --max-time "$CURL_MAX_TIME" \
        -w '%{http_code}|%{size_download}|%{content_type}' "$url" 2>/dev/null); rc=$?
    else
      out=$(curl -s -L --http1.1 -o "$BODY" -D "$HDR" --max-time "$CURL_MAX_TIME" \
        -w '%{http_code}|%{size_download}|%{content_type}' "$url" 2>/dev/null); rc=$?
    fi
    out=$(printf '%s' "$out" | tr -d '\r')
    code=$(printf '%s' "$out" | cut -d'|' -f1)
    size=$(printf '%s' "$out" | cut -d'|' -f2)
    if [ "$rc" = "0" ] && _complete "$code" "$size"; then done=1; break; fi
  done
  echo "${out}|$(head -c 4 "$BODY" | od -An -tx1 | tr -d ' \n')|${done}"
}

# get_code <url> ⇒ 回 "code|complete"（正文落 $BODY）。重拨口径同 fetch（只管传输、不管判据）。
get_code() {
  local url="$1" out rc code size done=0 i
  for i in $(seq 1 "$CURL_RETRY"); do
    if [ "$i" = "1" ]; then
      out=$(curl -s -L -o "$BODY" -D "$HDR" --max-time "$CURL_MAX_TIME" \
        -w '%{http_code}|%{size_download}' "$url" 2>/dev/null); rc=$?
    else
      out=$(curl -s -L --http1.1 -o "$BODY" -D "$HDR" --max-time "$CURL_MAX_TIME" \
        -w '%{http_code}|%{size_download}' "$url" 2>/dev/null); rc=$?
    fi
    out=$(printf '%s' "$out" | tr -d '\r')
    code=$(printf '%s' "$out" | cut -d'|' -f1)
    size=$(printf '%s' "$out" | cut -d'|' -f2)
    if [ "$rc" = "0" ] && _complete "$code" "$size"; then done=1; break; fi
  done
  echo "${code}|${done}"
}

# brand_field <json文件> <键> ⇒ 取字符串/数字值（无 jq 依赖，纯 python）
brand_field() {
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    print("__parse_fail__")
    raise SystemExit
v = d.get(sys.argv[2])
print("" if v is None else v)
PY
}

echo "==> 租户品牌域名冒烟：现役域=${TENANT_HOST:-<未配置>}（退役域 ${RETIRED_HOST} ／ 对照 $UNREG_HOST ／ ${MAIN_HOST}）"

# ---- T0 退役域：必须 410 且不返回站点内容（★ 2026-10-31 用户令）----------------
# 为什么要有这一腿：rox.lexicorn.cn 已按用户令退役（Caddy 精确块 `respond ... 410`）。
# 退役的判据不是"配置里有那一行"，而是**公网真的拿不到站点内容**——所以这里对公网真拨一次：
#   ① 状态码必须是 410（200＝又有人把退役块删了/写回通配块；404/000 是另一种形态，都要红）；
#   ② 正文必须点名 retired（防止 410 被别的错误页复用，把"谁挂了"读成"已退役"）。
if [ -n "$RETIRED_BASE" ]; then
  R0=$(fetch "$RETIRED_BASE/")
  code0=$(echo "$R0" | cut -d'|' -f1); ok0=$(echo "$R0" | cut -d'|' -f5)
  if [ "$ok0" != "1" ]; then
    inc "T0 退役域这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=${code0}）⇒ 稍后复跑"
  elif [ "$code0" = "410" ] && grep -qi 'retired' "$BODY"; then
    ok "T0 退役域 $RETIRED_HOST ⇒ 410 且正文点名 retired（不再返回站点内容）"
  else
    bad "T0 退役域 $RETIRED_HOST 异常：code=$code0 body=$(head -c 80 "$BODY" | tr '\n' ' ')（期望 410 + retired）⇒ 退役块被删/被通配块抢走"
  fi
fi

# ---- T1 租户域首页：后端直出 + 注入体归属正确 --------------------------------
if [ -n "$TENANT_BASE" ]; then
R1=$(get_code "$TENANT_BASE/"); CODE1=$(echo "$R1" | cut -d'|' -f1); COK1=$(echo "$R1" | cut -d'|' -f2)
HITS1=$(grep -c '__BRANDING__' "$BODY" || true)
B_TID1=$(python3 - "$BODY" <<'PY'
import json, re, sys
s = open(sys.argv[1], encoding="utf-8", errors="ignore").read()
m = re.search(r"window\.__BRANDING__\s*=\s*(\{.*?\})\s*;?\s*<", s, re.S)
if not m:
    print("__no_injection__")
    raise SystemExit
try:
    d = json.loads(m.group(1))
except Exception:
    print("__bad_json__")
    raise SystemExit
print(d.get("tenant_id", "__missing__"))
PY
)
B_NAME1=$(python3 - "$BODY" <<'PY'
import json, re, sys
s = open(sys.argv[1], encoding="utf-8", errors="ignore").read()
m = re.search(r"window\.__BRANDING__\s*=\s*(\{.*?\})\s*;?\s*<", s, re.S)
if not m:
    print("")
    raise SystemExit
try:
    print(json.loads(m.group(1)).get("brand_name", "") or "")
except Exception:
    print("")
PY
)
if [ "$CODE1" = "200" ] && [ "$HITS1" = "1" ] && [ "$B_TID1" = "$EXPECT_TENANT_ID" ]; then
  ok "T1 租户域首页 ${CODE1}／__BRANDING__ 命中 ${HITS1}／tenant_id=${B_TID1}（=期望 ${EXPECT_TENANT_ID}）"
elif [ "$COK1" != "1" ]; then
  inc "T1 首页这一跳没走完（重拨 ${CURL_RETRY} 次仍半截／超时，末次 code=${CODE1} size=$(wc -c < "$BODY" | tr -d ' ')）⇒ 网络抖动，不是配置问题；稍后复跑一次再判"
else
  bad "T1 租户域首页异常：code=$CODE1 hits=$HITS1 tenant_id=${B_TID1}（期望 200／1／${EXPECT_TENANT_ID}）⇒ 通配块缺失、或兜底段写成 file_server（F-74 形态）、或后端按 Host 没解析出租户"
fi
if [ -n "$EXPECT_BRAND_NAME" ]; then
  [ "$B_NAME1" = "$EXPECT_BRAND_NAME" ] && ok "T1b 注入体 brand_name=${B_NAME1}（等值）" \
    || bad "T1b brand_name 不符：实取「${B_NAME1}」期望「${EXPECT_BRAND_NAME}」"
else
  [ -n "$B_NAME1" ] && ok "T1b 注入体 brand_name 非空（${B_NAME1}）" \
    || bad "T1b brand_name 为空 ⇒ 该域名没解析到租户品牌（检查 tenants.domain 是否等于子域前缀 ${TENANT_HOST}）"
fi

# ---- T2 品牌件可达（图片魔数，防「库里存了 URL 但取不到字节」）-----------------
if [ "$SKIP_ASSETS" = "1" ]; then
  echo "  ⏭ T2 跳过（SKIP_ASSETS=1）"
else
  if [ "$COK1" != "1" ]; then
    # T1 那一跳没收全 ⇒ 注入体是半截的，解出来的品牌地址不可信（空值会被下面读成"没配"）
    inc "T2 跳过取词（T1 首页这一跳没走完，注入体是半截的）⇒ 稍后复跑"
    BG=""
  else
  BG=$(python3 - "$BODY" <<'PY'
import json, re, sys
s = open(sys.argv[1], encoding="utf-8", errors="ignore").read()
m = re.search(r"window\.__BRANDING__\s*=\s*(\{.*?\})\s*;?\s*<", s, re.S)
if not m:
    print("")
    raise SystemExit
try:
    print(json.loads(m.group(1)).get("brand_home_bg", "") or "")
except Exception:
    print("")
PY
)
  fi
  T1JSON=$(cat "$BODY")
  : > "$BODY"
  # ★ 2026-10-28：这一腿原来用裸 `curl | python3` 读一次 payload——跨境这一跳一旦收不全，
  #   Python 解 JSON 失败 ⇒ `LOGO=""` ⇒ 落到下面「非 /brand/ 路径」那支，**把链路抖动读成
  #   "这个租户没配 logo"**（判据静默降级，正是 AGENTS §三 那族）。现在走 fetch（重拨＋完整性），
  #   取不到就如实报红，不再和"未配置"同形。
  RLOGO=$(fetch "$TENANT_BASE/api/tenant/branding")
  codel=$(echo "$RLOGO" | cut -d'|' -f1); okel=$(echo "$RLOGO" | cut -d'|' -f5)
  if [ "$codel" != "200" ] || [ "$okel" != "1" ]; then
    inc "T2 品牌 payload 这一跳没走完（code=$codel complete=${okel}）⇒ 无法判定品牌件，稍后复跑"
    LOGO=""
  else
    LOGO=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1],encoding="utf-8")).get("brand_logo","") or "")' "$BODY" 2>/dev/null || echo "")
  fi
  got=0
  for pair in "brand_home_bg:$BG" "brand_logo:$LOGO"; do
    p="${pair#*:}"
    case "$p" in
      /brand/*)
        R=$(fetch "$TENANT_BASE$p")
        code=$(echo "$R" | cut -d'|' -f1); size=$(echo "$R" | cut -d'|' -f2)
        ctype=$(echo "$R" | cut -d'|' -f3); magic=$(echo "$R" | cut -d'|' -f4)
        cok=$(echo "$R" | cut -d'|' -f5)
        case "$p" in
          *.webp) want=52494646 ;;
          *) want=89504e47 ;;
        esac
        if [ "$cok" != "1" ]; then
          # 半截体不许判绿：实测同一份 1.5 MB 图连收三次都是 200＋魔数命中，但只有 220 KB
          inc "T2 $pair 这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=$code 只收到 ${size}B／远端声明更长）⇒ 网络抖动，不是静态件没落盘；稍后复跑一次"
        elif [ "$code" = "200" ] && [ "${size:-0}" -ge "$MIN_BRAND_BYTES" ] && [ "$magic" = "$want" ] \
           && echo "$ctype" | grep -qi '^image/'; then
          ok "T2 $pair 可达（${size}B／magic=${magic}／${ctype}）"; got=$((got + 1))
        else
          bad "T2 $pair 取不到字节：code=$code size=$size ctype=$ctype magic=${magic}（期望 200／≥${MIN_BRAND_BYTES}B／image\/*／魔数 ${want}）⇒ SPA 兜底吃成 HTML 或静态件没落盘"
        fi
        ;;
      *) echo "  · $pair 非 /brand/ 路径（空值或外链），T2 该件不计判" ;;
    esac
  done
  [ "$got" -ge 1 ] || echo "  ℹ️  T2 本轮没有可验的品牌件（该租户两张图都未配置）——属正常空值分支，不算红"
fi

else
  echo "  — T1/T1b/T2 跳过：未配置现役租户域（默认租户域 rox 已退役；退役行为见 T0）"
fi

# ---- T3 未登记子域负向：不得串到任何租户品牌 ----------------------------------
# ★ 2026-10-28 判据修正（原口径「brand_name 必须为空」自 10-23 起**结构性恒红**）：
#   平台主站把 brand_name 写进 `system_config.platform_branding` 之后（现网 2026-10-23 21:27 写入
#   「能言 LangCross」），未登记子域按设计回落到**平台默认品牌**（`tenant.go` 的 `brandingPayload`
#   在 tid≤0 时返回平台那一支），于是"名字非空"是正确形态；"名字必须空"只在"这台机器从没配过平台品牌"
#   时成立——那是**对实现形态做结构假设**的判据（AGENTS §三 那一族），会把正常回落读成串号。
#   这条判据真正要抓的是**串号**：猜个子域就看到**别人租户**的 brand_name。⇒ 三腿，期望值全部现读：
#     ① tenant_id=0（结构性：Host 没解析到任何租户）；
#     ② brand_name ≠ 该租户品牌名（T1 现读到的 name）；
#     ③ brand_name 要么空、要么等于**平台**品牌名（主站现读）——不许是第三个来路。
R3=$(fetch "$UNREGISTERED_BASE/api/tenant/branding"); code3=$(echo "$R3" | cut -d'|' -f1); cok3=$(echo "$R3" | cut -d'|' -f5)
TID3=""; NAME3=""
# 半截体不参与取词：解析失败会得到 __parse_fail__ 这种假读数，会被下面读成「串号」
[ "$cok3" = "1" ] && { TID3=$(brand_field "$BODY" "tenant_id"); NAME3=$(brand_field "$BODY" "brand_name"); }
# 平台品牌名现读主站接口（不写死：运营改平台品牌名不该让这条判据变红）
RP=$(fetch "$MAIN_BASE/api/tenant/branding"); cokp=$(echo "$RP" | cut -d'|' -f5)
PLAT_NAME=""
[ "$cokp" = "1" ] && PLAT_NAME=$(brand_field "$BODY" "brand_name")
# T1 那条注入体读到的是租户品牌名（可能因 T1 红而为空/占位，故先判可用性再比）
TEN_NAME="$B_NAME1"
case "$TEN_NAME" in __*) TEN_NAME="" ;; esac
case "$PLAT_NAME" in __*) PLAT_NAME="" ;; esac
T3_OK=0
T3_INC=0
T3_WHY=""
if [ "$cok3" != "1" ]; then
  T3_INC=1
  T3_WHY="这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=${code3}）"
elif [ "$cokp" != "1" ]; then
  # 平台对照取不到 ⇒ 第三腿无参照物（不许把「没取到参照物」写成「串号」）
  T3_INC=1
  T3_WHY="平台品牌名这一跳没走完（末次 code=$(echo "$RP" | cut -d'|' -f1)），无法判定「${NAME3}」是否平台默认"
elif [ "$code3" = "200" ] && [ "$TID3" = "0" ]; then
  # ② 串号：拿到的名字必须不是这个租户的品牌名（租户名为空时这一腿无从判，靠 ③ 兜）
  if [ -n "$TEN_NAME" ] && [ "$NAME3" = "$TEN_NAME" ]; then
    T3_OK=0
    T3_WHY="拿到的是本租户品牌名「${NAME3}」"
  elif [ -z "$NAME3" ] || { [ -n "$PLAT_NAME" ] && [ "$NAME3" = "$PLAT_NAME" ]; }; then
    T3_OK=1
  else
    T3_WHY="brand_name「${NAME3}」既不是空、也不是平台品牌「${PLAT_NAME}」"
  fi
else
  T3_WHY="code=${code3} tenant_id=${TID3}（期望 200／0）"
fi
if [ "$T3_OK" = "1" ]; then
  ok "T3 未登记子域 $UNREG_HOST ⇒ tenant_id=0／brand_name=${NAME3:-<空>}（空或=平台品牌「${PLAT_NAME}」，无串号）"
elif [ "$T3_INC" = "1" ]; then
  # 半截体／参照物取不到 ⇒ 这一跳没测到（既不是通过，也不许写成串号）
  inc "T3 未登记子域这一跳没走完：${T3_WHY} ⇒ 稍后复跑"
else
  bad "T3 未登记子域拿到品牌：${T3_WHY} ⇒ 品牌解析被 Host 之外的东西（Cookie/默认租户）带偏"
fi

# ---- T4 接口链诚实：401 语义 + JSON 形状（证明 API 反代落到后端）--------------
if [ -n "$TENANT_BASE" ]; then
R4a=$(get_code "$TENANT_BASE/api/me/tasks"); C4=$(echo "$R4a" | cut -d'|' -f1); ok4a=$(echo "$R4a" | cut -d'|' -f2)
if [ "$ok4a" != "1" ]; then
  inc "T4a /api/me/tasks 这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=${C4}）⇒ 稍后复跑"
elif [ "$C4" = "401" ]; then
  ok "T4a /api/me/tasks 匿名 ⇒ 401（F-64 诚实状态码，说明请求真到了后端而不是 SPA 兜底）"
else
  bad "T4a /api/me/tasks 匿名返回 ${C4}（期望 401；200＝落 SPA 兜底，404＝API 段没反代到后端）"
fi
R4=$(fetch "$TENANT_BASE/api/health"); code4=$(echo "$R4" | cut -d'|' -f1); magic4=$(echo "$R4" | cut -d'|' -f4); ok4=$(echo "$R4" | cut -d'|' -f5)
if [ "$ok4" != "1" ]; then
  inc "T4b /api/health 这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=${code4}）⇒ 稍后复跑"
elif [ "$code4" = "200" ] && [ "${magic4:0:2}" = "7b" ]; then
  ok "T4b /api/health 200 且体首是 JSON（magic=${magic4}）"
else
  bad "T4b /api/health code=$code4 magic=${magic4}（期望 200／7b…）⇒ 后端没接住"
fi

# ---- T5 托管物：扩展 zip 与 SDK manifest 不得被 SPA 兜底吃掉 -------------------
if [ "$SKIP_ASSETS" = "1" ]; then
  echo "  ⏭ T5 跳过（SKIP_ASSETS=1）"
else
  R5=$(fetch "$TENANT_BASE/extensions/langcross-extension-latest.zip")
  code5=$(echo "$R5" | cut -d'|' -f1); size5=$(echo "$R5" | cut -d'|' -f2); magic5=$(echo "$R5" | cut -d'|' -f4); ok5=$(echo "$R5" | cut -d'|' -f5)
  if [ "$ok5" != "1" ]; then
    # ★ 半截包不许判绿：实测同一份 12,153 B 的包收到 10,459 B 仍报 200 且魔数 PK 也在
    inc "T5a 扩展包这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=$code5 只收到 ${size5}B）⇒ 拿到的可能是半截包，稍后复跑"
  elif [ "$code5" = "200" ] && [ "$magic5" = "504b0304" ] && [ "${size5:-0}" -ge "$MIN_ZIP_BYTES" ]; then
    ok "T5a 扩展包 ${size5}B／magic=${magic5}（不是 HTML 兜底）"
  else
    bad "T5a 扩展包异常 code=$code5 size=$size5 magic=${magic5}（期望 200／504b0304／≥${MIN_ZIP_BYTES}B）"
  fi
  R5b=$(fetch "$TENANT_BASE/sdk/manifest.json"); code5b=$(echo "$R5b" | cut -d'|' -f1); magic5b=$(echo "$R5b" | cut -d'|' -f4); ok5b=$(echo "$R5b" | cut -d'|' -f5)
  if [ "$ok5b" != "1" ]; then
    inc "T5b SDK manifest 这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=${code5b}）⇒ 稍后复跑"
  elif [ "$code5b" = "200" ] && [ "${magic5b:0:2}" = "7b" ]; then
    ok "T5b SDK manifest 200 且是 JSON（magic=${magic5b}）"
  else
    bad "T5b SDK manifest code=$code5b magic=${magic5b}（期望 200／7b…）⇒ 落 HTML 兜底"
  fi
fi

# ---- T6 安全头基线（租户域不得弱于主站）--------------------------------------
R6=$(get_code "$TENANT_BASE/"); code6=$(echo "$R6" | cut -d'|' -f1); ok6=$(echo "$R6" | cut -d'|' -f2)
tr -d '\r' < "$HDR" > /tmp/.std_h.$$
if [ "$ok6" != "1" ]; then
  inc "T6 首页这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=${code6}）⇒ 安全头无从判定，稍后复跑"
else
  for h in strict-transport-security x-content-type-options referrer-policy permissions-policy; do
    grep -qi "^$h:" /tmp/.std_h.$$ && ok "T6 安全头 $h 在位" || bad "T6 安全头 $h 缺失 ⇒ 通配块 header 基线被改动（租户域安全不得弱于主站）"
  done
fi
rm -f /tmp/.std_h.$$

else
  echo "  — T4/T5/T6 跳过：未配置现役租户域（默认租户域 rox 已退役）"
fi

# ---- T7 HTTP ⇒ HTTPS ---------------------------------------------------------
# 注意：这一腿**不能用 -L**（跟随后就看不到 308 了），所以自带走一条只认传输失败的重拨。
T7_HOST="${TENANT_HOST:-$RETIRED_HOST}"
C7=000
for i in $(seq 1 "$CURL_RETRY"); do
  if [ "$i" = "1" ]; then
    C7=$(curl -s -o /dev/null -w '%{http_code}' --max-time "$CURL_MAX_TIME" "http://$T7_HOST/"); rc7=$?
  else
    C7=$(curl -s -o /dev/null --http1.1 -w '%{http_code}' --max-time "$CURL_MAX_TIME" "http://$T7_HOST/"); rc7=$?
  fi
  [ "$rc7" = "0" ] && [ "$C7" != "000" ] && break
done
if [ "$rc7" != "0" ] && [ "$C7" = "000" ]; then
  inc "T7 http:// 这一跳没走完（重拨 ${CURL_RETRY} 次）⇒ 稍后复跑"
else
  case "$C7" in 301 | 308) ok "T7 http:// ⇒ $C7 跳 https" ;; *) bad "T7 http:// 返回 ${C7}（期望 301/308）" ;; esac
fi

# ---- T8 反向不串号：主站首页不得出现该租户品牌名 ------------------------------
if [ -z "$B_NAME1" ]; then
  echo "  — T8 跳过：无现役租户品牌名可比对（T1 未跑或无品牌名）"
else
R8=$(fetch "$MAIN_BASE/"); code8=$(echo "$R8" | cut -d'|' -f1); ok8=$(echo "$R8" | cut -d'|' -f5)
if [ "$ok8" != "1" ]; then
  # 半截体里"没出现租户品牌"会读成"不串号"——这条负向锁不能吃半截体
  inc "T8 主站首页这一跳没走完（重拨 ${CURL_RETRY} 次，末次 code=${code8}）⇒ 负向判据不吃半截体，稍后复跑"
elif [ -n "$B_NAME1" ] && grep -qF "$B_NAME1" "$BODY"; then
  bad "T8 主站首页出现租户品牌「${B_NAME1}」⇒ 平台根域名被套上了租户品牌（应永远回平台默认）"
else
  ok "T8 主站首页不含租户品牌「${B_NAME1}」（反向不串号）"
fi
fi

echo ""
echo "============================================================"
echo "smoke_tenant_domain｜现役域 ${TENANT_HOST:-<无>}／退役域 ${RETIRED_HOST}｜PASS=$PASS FAIL=$FAIL UNMEASURED=$UNMEASURED"
echo "============================================================"
# ★ 2026-10-28 三态退出码：红项优先（1），其次"没测到"（2），全绿才 0。
#   发版判据＝FAIL=0 **且** UNMEASURED=0；拿到 2 就是这一跳没走完，复跑一次再判，不许当通过。
if [ "$FAIL" != "0" ]; then
  exit 1
elif [ "$UNMEASURED" != "0" ]; then
  exit 2
fi
exit 0
