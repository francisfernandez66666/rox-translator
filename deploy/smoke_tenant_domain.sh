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
#      tenant_id=0 且 brand_name 为空 —— 防「猜个子域就看到别人的品牌」的串号。
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
# 用法（全部只读，纯 GET，不写库不改配置）：
#   bash deploy/smoke_tenant_domain.sh                                   # 用下面默认值（现网口径）
#   TENANT_BASE=https://rox.lexicorn.cn EXPECT_TENANT_ID=1 EXPECT_BRAND_NAME=极石 \
#     UNREGISTERED_BASE=https://zzz-nodata-0w.lexicorn.cn bash deploy/smoke_tenant_domain.sh
#   SKIP_ASSETS=1 bash deploy/smoke_tenant_domain.sh                     # 只跑路由/品牌判据（离线快检）
#
# ⚠️ 期望值为什么从接口现读而不是写死：品牌名/图片哈希会随客户运营改动，写死必然变成
#    落后于现实的假绿源；本脚本只钉「结构性不变量」（命中数、tenant_id 归属、空值负向、
#    魔数、状态码语义），EXPECT_BRAND_NAME 允许留空＝只判「非空且与未登记子域不同」。
# 退出码：0=全绿；1=有红项（红项逐条点名）。
# ============================================================================
set -uo pipefail

TENANT_BASE="${TENANT_BASE:-https://rox.lexicorn.cn}"
UNREGISTERED_BASE="${UNREGISTERED_BASE:-https://zzz-nodata-0w.lexicorn.cn}"
MAIN_BASE="${MAIN_BASE:-https://langcross.lexicorn.cn}"
EXPECT_TENANT_ID="${EXPECT_TENANT_ID:-1}"
EXPECT_BRAND_NAME="${EXPECT_BRAND_NAME:-}"             # 留空＝只判「非空」
MIN_BRAND_BYTES="${MIN_BRAND_BYTES:-2048}"
MIN_ZIP_BYTES="${MIN_ZIP_BYTES:-5000}"
SKIP_ASSETS="${SKIP_ASSETS:-0}"

PASS=0
FAIL=0
ok()  { echo "  ✔ $1"; PASS=$((PASS + 1)); }
bad() { echo "  ✖ $1"; FAIL=$((FAIL + 1)); }

# host_of 剥掉协议与端口，得到探针要用的域名
host_of() { echo "$1" | sed -E 's#^[a-z]+://##; s#[:/].*$##'; }
TENANT_HOST=$(host_of "$TENANT_BASE")
UNREG_HOST=$(host_of "$UNREGISTERED_BASE")
MAIN_HOST=$(host_of "$MAIN_BASE")

# fetch <url> <期望魔数前缀(可空)> ⇒ 输出 "code|size|ctype|magic"，正文落 $BODY
BODY=$(mktemp)
trap 'rm -f "$BODY"' EXIT
fetch() {
  local url="$1"
  curl -s -L -o "$BODY" -D /tmp/.std_hdr.$$ --max-time 60 \
    -w '%{http_code}|%{size_download}|%{content_type}' "$url" 2>/dev/null | tr -d '\r'
  echo "|$(head -c 4 "$BODY" | od -An -tx1 | tr -d ' \n')"
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

echo "==> 租户品牌域名冒烟：$TENANT_HOST（对照 $UNREG_HOST ／ $MAIN_HOST）"

# ---- T1 租户域首页：后端直出 + 注入体归属正确 --------------------------------
CODE1=$(curl -s -o "$BODY" -w '%{http_code}' --max-time 60 "$TENANT_BASE/")
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
  ok "T1 租户域首页 $CODE1／__BRANDING__ 命中 $HITS1／tenant_id=$B_TID1（=期望 $EXPECT_TENANT_ID）"
else
  bad "T1 租户域首页异常：code=$CODE1 hits=$HITS1 tenant_id=$B_TID1（期望 200／1／$EXPECT_TENANT_ID）⇒ 通配块缺失、或兜底段写成 file_server（F-74 形态）、或后端按 Host 没解析出租户"
fi
if [ -n "$EXPECT_BRAND_NAME" ]; then
  [ "$B_NAME1" = "$EXPECT_BRAND_NAME" ] && ok "T1b 注入体 brand_name=$B_NAME1（等值）" \
    || bad "T1b brand_name 不符：实取「$B_NAME1」期望「$EXPECT_BRAND_NAME」"
else
  [ -n "$B_NAME1" ] && ok "T1b 注入体 brand_name 非空（$B_NAME1）" \
    || bad "T1b brand_name 为空 ⇒ 该域名没解析到租户品牌（检查 tenants.domain 是否等于子域前缀 $TENANT_HOST）"
fi

# ---- T2 品牌件可达（图片魔数，防「库里存了 URL 但取不到字节」）-----------------
if [ "$SKIP_ASSETS" = "1" ]; then
  echo "  ⏭ T2 跳过（SKIP_ASSETS=1）"
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
  T1JSON=$(cat "$BODY")
  : > "$BODY"
  LOGO=$(curl -s --max-time 60 "$TENANT_BASE/api/tenant/branding" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("brand_logo",""))' 2>/dev/null || echo "")
  got=0
  for pair in "brand_home_bg:$BG" "brand_logo:$LOGO"; do
    p="${pair#*:}"
    case "$p" in
      /brand/*)
        R=$(fetch "$TENANT_BASE$p")
        code=$(echo "$R" | cut -d'|' -f1); size=$(echo "$R" | cut -d'|' -f2)
        ctype=$(echo "$R" | cut -d'|' -f3); magic=$(echo "$R" | cut -d'|' -f4)
        case "$p" in
          *.webp) want=52494646 ;;
          *) want=89504e47 ;;
        esac
        if [ "$code" = "200" ] && [ "${size:-0}" -ge "$MIN_BRAND_BYTES" ] && [ "$magic" = "$want" ] \
           && echo "$ctype" | grep -qi '^image/'; then
          ok "T2 $pair 可达（${size}B／magic=$magic／$ctype）"; got=$((got + 1))
        else
          bad "T2 $pair 取不到字节：code=$code size=$size ctype=$ctype magic=$magic（期望 200／≥${MIN_BRAND_BYTES}B／image\/*／魔数 $want）⇒ SPA 兜底吃成 HTML 或静态件没落盘"
        fi
        ;;
      *) echo "  · $pair 非 /brand/ 路径（空值或外链），T2 该件不计判" ;;
    esac
  done
  [ "$got" -ge 1 ] || echo "  ℹ️  T2 本轮没有可验的品牌件（该租户两张图都未配置）——属正常空值分支，不算红"
fi

# ---- T3 未登记子域负向：不得串到任何租户品牌 ----------------------------------
R3=$(fetch "$UNREGISTERED_BASE/api/tenant/branding"); code3=$(echo "$R3" | cut -d'|' -f1)
TID3=$(brand_field "$BODY" "tenant_id"); NAME3=$(brand_field "$BODY" "brand_name")
if [ "$code3" = "200" ] && [ "$TID3" = "0" ] && [ -z "$NAME3" ]; then
  ok "T3 未登记子域 $UNREG_HOST ⇒ tenant_id=0／brand_name 空（无串号）"
else
  bad "T3 未登记子域拿到了品牌：code=$code3 tenant_id=$TID3 brand_name=$NAME3 ⇒ 品牌解析被 Host 之外的东西（Cookie/默认租户）带偏"
fi

# ---- T4 接口链诚实：401 语义 + JSON 形状（证明 API 反代落到后端）--------------
C4=$(curl -s -o "$BODY" -w '%{http_code}' --max-time 60 "$TENANT_BASE/api/me/tasks")
[ "$C4" = "401" ] && ok "T4a /api/me/tasks 匿名 ⇒ 401（F-64 诚实状态码，说明请求真到了后端而不是 SPA 兜底）" \
  || bad "T4a /api/me/tasks 匿名返回 $C4（期望 401；200＝落 SPA 兜底，404＝API 段没反代到后端）"
R4=$(fetch "$TENANT_BASE/api/health"); code4=$(echo "$R4" | cut -d'|' -f1); magic4=$(echo "$R4" | cut -d'|' -f4)
{ [ "$code4" = "200" ] && [ "${magic4:0:2}" = "7b" ]; } && ok "T4b /api/health 200 且体首是 JSON（magic=$magic4）" \
  || bad "T4b /api/health code=$code4 magic=$magic4（期望 200／7b…）⇒ 后端没接住"

# ---- T5 托管物：扩展 zip 与 SDK manifest 不得被 SPA 兜底吃掉 -------------------
if [ "$SKIP_ASSETS" = "1" ]; then
  echo "  ⏭ T5 跳过（SKIP_ASSETS=1）"
else
  R5=$(fetch "$TENANT_BASE/extensions/langcross-extension-latest.zip")
  code5=$(echo "$R5" | cut -d'|' -f1); size5=$(echo "$R5" | cut -d'|' -f2); magic5=$(echo "$R5" | cut -d'|' -f4)
  { [ "$code5" = "200" ] && [ "$magic5" = "504b0304" ] && [ "${size5:-0}" -ge "$MIN_ZIP_BYTES" ]; } \
    && ok "T5a 扩展包 ${size5}B／magic=$magic5（不是 HTML 兜底）" \
    || bad "T5a 扩展包异常 code=$code5 size=$size5 magic=$magic5（期望 200／504b0304／≥${MIN_ZIP_BYTES}B）"
  R5b=$(fetch "$TENANT_BASE/sdk/manifest.json"); code5b=$(echo "$R5b" | cut -d'|' -f1); magic5b=$(echo "$R5b" | cut -d'|' -f4)
  { [ "$code5b" = "200" ] && [ "${magic5b:0:2}" = "7b" ]; } && ok "T5b SDK manifest 200 且是 JSON（magic=$magic5b）" \
    || bad "T5b SDK manifest code=$code5b magic=$magic5b（期望 200／7b…）⇒ 落 HTML 兜底"
fi

# ---- T6 安全头基线（租户域不得弱于主站）--------------------------------------
curl -s -D - -o /dev/null --max-time 60 "$TENANT_BASE/" | tr -d '\r' > /tmp/.std_h.$$
for h in strict-transport-security x-content-type-options referrer-policy permissions-policy; do
  grep -qi "^$h:" /tmp/.std_h.$$ && ok "T6 安全头 $h 在位" || bad "T6 安全头 $h 缺失 ⇒ 通配块 header 基线被改动（租户域安全不得弱于主站）"
done
rm -f /tmp/.std_h.$$

# ---- T7 HTTP ⇒ HTTPS ---------------------------------------------------------
C7=$(curl -s -o /dev/null -w '%{http_code}' --max-time 60 "http://$TENANT_HOST/")
case "$C7" in 301 | 308) ok "T7 http:// ⇒ $C7 跳 https" ;; *) bad "T7 http:// 返回 $C7（期望 301/308）" ;; esac

# ---- T8 反向不串号：主站首页不得出现该租户品牌名 ------------------------------
curl -s -o "$BODY" --max-time 60 "$MAIN_BASE/"
if [ -n "$B_NAME1" ] && grep -qF "$B_NAME1" "$BODY"; then
  bad "T8 主站首页出现租户品牌「$B_NAME1」⇒ 平台根域名被套上了租户品牌（应永远回平台默认）"
else
  ok "T8 主站首页不含租户品牌「${B_NAME1:-（无品牌名可比对）}」（反向不串号）"
fi

echo ""
echo "============================================================"
echo "smoke_tenant_domain｜租户域 $TENANT_HOST｜PASS=$PASS FAIL=$FAIL"
echo "============================================================"
[ "$FAIL" = "0" ] && exit 0 || exit 1
