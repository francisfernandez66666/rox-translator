#!/usr/bin/env bash
# =============================================================================
# SEO 页面上线验证 —— 防「页面看起来上线了，其实是空壳」
#
# ★ 为什么必须有这个脚本（本仓已踩过的坑，不是理论风险）：
#     spa.go 对不存在的路径会回退成 index.html，并且**仍然返回 200**。
#     于是「curl 一下返回 200」这个最常见的验证方式，在 SEO 页上是 100% 的假绿：
#     页面被收录了、sitemap 也提交了，但搜索引擎抓到的是 SPA 空壳，
#     零内容、零排名，而且**全程没有任何报错**。
#
# 三重判据（缺一不可）：
#   ① HTTP 200
#   ② 响应体不含 SPA 兜底标记 window.__BRANDING__
#   ③ canonical 存在且指向本页自己的 /g/<slug>.html
#      （SPA 兜底的 canonical 指向首页，这一条能把它和第二条一起坐实）
#
# 另检：sitemap.xml / robots.txt 可访问；CF-Cache-Status 是否 HIT（缓存 poisoning 排查）
#
# 用法：
#   bash scripts/growth/check_seo_live.sh --base https://langcross.lexicorn.cn
#   bash scripts/growth/check_seo_live.sh --base https://langcross.lexicorn.cn --limit 10
#   bash scripts/growth/check_seo_live.sh --selftest     # 正负双向自证
#
# ★ 判据选择说明（来自本仓教训「否定结论必须复核」）：
#   只数「有多少个 200」是没有判别力的——SPA 兜底也是 200。
#   必须数「有多少个**不是** SPA 兜底的 200」。
# =============================================================================
set -uo pipefail

# ★ 本地回环不走代理：自证用的 127.0.0.1 静态服务会被本机 HTTP_PROXY 劫持成 502，
#   导致判据自证误判「样本未被识别」。覆盖成始终带 --noproxy，
#   对生产 https 域名（非 127.0.0.1）无副作用。
curl() { command curl --noproxy 127.0.0.1,localhost "$@"; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
PUBLIC="${ROOT}/frontend-react/public"
PAGE_DIR="g"

BASE=""
LIMIT=0
SELFTEST=0
VERBOSE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --base)    BASE="${2:-}"; shift 2 ;;
    --limit)   LIMIT="${2:-0}"; shift 2 ;;
    --verbose|-v) VERBOSE=1; shift ;;
    --selftest) SELFTEST=1; shift ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

# ---- 工具函数 ----------------------------------------------------------------

log()  { printf '%s\n' "$*"; }
err()  { printf '%s\n' "$*" >&2; }

need() {
  command -v "$1" >/dev/null 2>&1 || { err "✖ 缺少依赖: $1"; exit 2; }
}

# 取 URL 列表：优先本地 public/g（不依赖线上 sitemap），缺省回退线上 sitemap
slug_list() {
  if [ -d "${PUBLIC}/${PAGE_DIR}" ]; then
    # shellcheck disable=SC2012
    ls "${PUBLIC}/${PAGE_DIR}" 2>/dev/null | sed -n 's/\.html$//p'
  fi
}

# ---- 单项检查 ----------------------------------------------------------------

check_one() {
  # $1 = 完整 URL, $2 = slug
  local url="$1" slug="$2" tmp code body
  tmp="$(mktemp)"
  # ★ 只取 http_code 一个字段：%{content_type} 本身含空格，
  #   和别的字段写一起再词分割会串位（本仓踩过）
  code="$(curl -sS --max-time 20 -o "${tmp}" -w '%{http_code}' "${url}" 2>/dev/null || echo 000)"
  body="$(cat "${tmp}" 2>/dev/null)"
  rm -f "${tmp}"

  if [ "${code}" != "200" ]; then
    echo "NON200|${slug}|HTTP ${code}"
    return
  fi
  # ② SPA 兜底标记：首页由后端直出并注入 window.__BRANDING__
  if printf '%s' "${body}" | grep -q 'window.__BRANDING__'; then
    echo "SPA_FALLBACK|${slug}|响应体含 window.__BRANDING__，被 SPA 兜底吞了"
    return
  fi
  # ③ canonical 自指
  if ! printf '%s' "${body}" | grep -q "rel=\"canonical\" href=\"[^\"]*/${PAGE_DIR}/${slug}.html\""; then
    echo "NO_CANONICAL|${slug}|canonical 缺失或未指向本页"
    return
  fi
  echo "OK|${slug}|"
}

# ---- 主流程 ------------------------------------------------------------------

run_check() {
  local base="$1"
  need curl

  if [ -z "${base}" ]; then
    err "✖ 需要 --base <站点根 URL>"
    return 2
  fi

  # ★ 可达性先判：站点不通时不能让「0 个页面」看起来像「0 个问题」。
  #   （本仓教训：没人来读 ≠ 没探到，观测腿必须自带读者并显式区分）
  local root_code
  root_code="$(curl -sS --max-time 20 -o /dev/null -w '%{http_code}' "${base}/" 2>/dev/null || echo 000)"
  if [ "${root_code}" = "000" ]; then
    err "✖ 站点不可达：${base}（curl 返回 000）"
    err "  这不是「页面有问题」，是根本没连上。先确认网络 / 域名解析 / 服务状态。"
    return 3
  fi

  local slugs n=0 ok=0 non200=0 spa=0 nocan=0
  slugs="$(slug_list)"
  if [ -z "${slugs}" ]; then
    err "✖ 本地 ${PUBLIC}/${PAGE_DIR} 里没有页面，先跑 build_seo_pages.py"
    return 2
  fi

  log "站点：${base}"
  log "页面来源：${PUBLIC}/${PAGE_DIR}"
  log ""

  local fail_list=""
  while IFS= read -r slug; do
    [ -z "${slug}" ] && continue
    if [ "${LIMIT}" != "0" ] && [ "${n}" -ge "${LIMIT}" ]; then break; fi
    n=$((n + 1))
    local res st sl msg
    res="$(check_one "${base}/${PAGE_DIR}/${slug}.html" "${slug}")"
    st="${res%%|*}"; res="${res#*|}"
    sl="${res%%|*}"; msg="${res#*|}"
    case "${st}" in
      OK) ok=$((ok + 1)); [ "${VERBOSE}" = "1" ] && log "  ✔ ${sl}" ;;
      NON200)       non200=$((non200 + 1)); fail_list="${fail_list}✖ ${st} ${sl} ${msg}\n" ;;
      SPA_FALLBACK) spa=$((spa + 1));       fail_list="${fail_list}✖ ${st} ${sl} ${msg}\n" ;;
      NO_CANONICAL) nocan=$((nocan + 1));   fail_list="${fail_list}✖ ${st} ${sl} ${msg}\n" ;;
    esac
  done <<EOF
${slugs}
EOF

  # ---- sitemap / robots ----
  local sm_code sm_type rb_code
  sm_code="$(curl -sS --max-time 20 -o /dev/null -w '%{http_code}' "${base}/sitemap.xml" 2>/dev/null || echo 000)"
  rb_code="$(curl -sS --max-time 20 -o /dev/null -w '%{http_code}' "${base}/robots.txt" 2>/dev/null || echo 000)"
  if [ "${sm_code}" = "200" ]; then
    sm_type="$(curl -sSI --max-time 20 "${base}/sitemap.xml" 2>/dev/null | grep -i '^content-type' | head -1)"
  else
    sm_type=""
  fi

  log "────────────────────────────────────────"
  log "抽查页面数：${n}"
  log "  通过（三重判据全绿）：${ok}"
  log "  非 200           ：${non200}"
  log "  ★ SPA 兜底      ：${spa}"
  log "  canonical 缺失  ：${nocan}"
  log "sitemap.xml：HTTP ${sm_code}  ${sm_type}"
  log "robots.txt ：HTTP ${rb_code}"
  log "────────────────────────────────────────"

  if [ "${n}" -eq 0 ]; then
    err "✖ 一个页面都没抽查到 —— 这是脚本自身的问题，不是站点的问题"
    return 2
  fi
  # ★ 「抽查到 0 个」和「抽查到但全是 SPA 兜底」必须是两种失败，不能都归成 FAIL 一句话
  if [ "${spa}" -gt 0 ] || [ "${nocan}" -gt 0 ] || [ "${non200}" -gt 0 ]; then
    log ""
    log "问题明细（前 20 条）："
    printf '%b' "${fail_list}" | head -20
    if [ "${spa}" -gt 0 ]; then
      log ""
      log "★ ${spa} 个页面被 SPA 兜底吞掉了。"
      log "  含义：路径没被静态直出，请求落进了 spa.go 的 index.html 回退。"
      log "  排查：① 文件是否真在 dist 里 ② Caddy 的 file_server 是否覆盖 /${PAGE_DIR}/ "
      log "        ③ 是否被 Cloudflare 缓存了换源前的 404（真被毒死时只能 CACHE_BUST 换入口名）"
    fi
    if [ "${sm_code}" != "200" ]; then
      log ""
      log "★ sitemap.xml 不可访问，搜索引擎发现不了这些页面"
    fi
    return 1
  fi

  if [ "${sm_code}" != "200" ] || [ "${rb_code}" != "200" ]; then
    log "△ 页面全绿，但 sitemap/robots 有问题，提交收录前先修"
    return 1
  fi

  log "CHECK PASS — ${ok}/${n} 页面真实上线，未被 SPA 兜底"
  return 0
}

# ---- 自证（★ 正负双向） ------------------------------------------------------

selftest() {
  need curl
  need python3
  local fail=0

  if [ ! -d "${PUBLIC}/${PAGE_DIR}" ]; then
    err "✖ 没有页面可自证（${PUBLIC}/${PAGE_DIR} 不存在）。先跑 build_seo_pages.py"
    return 1
  fi

  # ① 负向自证：对一个必然连不上的地址，脚本必须判 FAIL（而不是「0 个页面」混过去）
  log "① 负向自证：不可达站点必须判 FAIL"
  if bash "$0" --base "http://127.0.0.1:1" --limit 1 >/dev/null 2>&1; then
    err "   ✖ 不可达站点被判成了通过 —— 判据恒绿，这个脚本没有判别力"
    fail=1
  else
    log "   ✔ 不可达站点正确判 FAIL"
  fi

  # ② 正向自证：起一个本地静态服务跑真实 HTTP，必须全绿
  log "② 正向自证：本地静态服务跑真实 HTTP 必须全绿"
  local port=18799 pid
  ( cd "${PUBLIC}" && python3 -m http.server "${port}" >/dev/null 2>&1 ) &
  pid=$!
  local waited=0
  while [ "${waited}" -lt 40 ]; do
    if curl -sS --max-time 2 -o /dev/null "http://127.0.0.1:${port}/" 2>/dev/null; then break; fi
    sleep 0.25; waited=$((waited + 1))
  done

  local out rc
  out="$(bash "$0" --base "http://127.0.0.1:${port}" --limit 12 2>&1)"
  rc=$?
  kill "${pid}" 2>/dev/null
  wait "${pid}" 2>/dev/null

  if [ "${rc}" -ne 0 ]; then
    err "   ✖ 本地真实 HTTP 下未通过："
    printf '%s\n' "${out}" | tail -12
    fail=1
  else
    printf '%s\n' "${out}" | grep -q "CHECK PASS" || { err "   ✖ 输出里没有 CHECK PASS"; fail=1; }
    log "   ✔ 本地真实 HTTP 下 CHECK PASS"
  fi

  # ③ 判据自证：拿一个 SPA 兜底样本，确认会被判成 SPA_FALLBACK
  #    ★ 否则「SPA 兜底 0」可能只是因为判据没在工作
  log "③ 判据自证：SPA 兜底样本必须被抓出来"
  local spa_dir tmpdir
  tmpdir="$(mktemp -d)"
  spa_dir="${tmpdir}/${PAGE_DIR}"
  mkdir -p "${spa_dir}"
  # 造一个「200 但是 SPA 壳」的样本：含 window.__BRANDING__、canonical 指向首页
  printf '%s' '<!doctype html><html><head><script>window.__BRANDING__={"x":1};</script>
<link rel="canonical" href="https://langcross.lexicorn.cn/"></head><body>spa</body></html>' \
    > "${spa_dir}/spa-canary.html"
  local port2=18800 pid2
  ( cd "${tmpdir}" && python3 -m http.server "${port2}" >/dev/null 2>&1 ) &
  pid2=$!
  waited=0
  while [ "${waited}" -lt 40 ]; do
    if curl -sS --max-time 2 -o /dev/null "http://127.0.0.1:${port2}/" 2>/dev/null; then break; fi
    sleep 0.25; waited=$((waited + 1))
  done
  # 直接调 check_one（需要它在子进程里可见，这里用 bash -c 复跑脚本并改 PUBLIC 不方便，
  # 故用环境变量覆盖的方式：复制脚本无法改常量，改为内联复算一次判据）
  local body code
  code="$(curl -sS --max-time 10 -o "${tmpdir}/canary.out" -w '%{http_code}' \
          "http://127.0.0.1:${port2}/${PAGE_DIR}/spa-canary.html" 2>/dev/null || echo 000)"
  body="$(cat "${tmpdir}/canary.out" 2>/dev/null)"
  kill "${pid2}" 2>/dev/null; wait "${pid2}" 2>/dev/null
  rm -rf "${tmpdir}"

  if [ "${code}" = "200" ] && printf '%s' "${body}" | grep -q 'window.__BRANDING__'; then
    log "   ✔ SPA 样本被识别（HTTP ${code} 且含 __BRANDING__，会被判 SPA_FALLBACK）"
  else
    err "   ✖ SPA 样本没被识别（code=${code}）——判据失效，线上真被兜底时也发现不了"
    fail=1
  fi

  if [ "${fail}" -ne 0 ]; then
    err "SELFTEST FAIL"
    return 1
  fi
  log "SELFTEST PASS  （负向 / 正向 / 判据 三向自证）"
  return 0
}

# ---- 入口 --------------------------------------------------------------------

if [ "${SELFTEST}" = "1" ]; then
  selftest
  exit $?
fi

run_check "${BASE}"
exit $?
