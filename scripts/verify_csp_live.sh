#!/usr/bin/env bash
# scripts/verify_csp_live.sh — W0：两站完整 CSP 响应头抓取 + 四档判据（2026-11-10）
#
# 背景：2026-11-10 批次 P16.hdr 只有 39 B 的 "HTTP/1.1 200 Connection Established"
#       ——那是 HTTP 代理的 CONNECT 隧道建立行，不是站点响应头。本脚本显式清空代理并
#       直连，同时把"只抓到隧道行必须判红"做成负向对照，防止再次把抓包缺陷读成部署漂移。
#
# 用法：bash scripts/verify_csp_live.sh [证据目录]
# 判绿：输出 CSP_LIVE_ALL_OK=1（退出码 0）；任一档 FAIL 输出 0（退出码 1）
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAMP="$(date +%Y%m%d_%H%M%S)"
LOG="${1:-$ROOT/UAT_上线审计_20261105/logs/CSP_LIVE_$STAMP}"
mkdir -p "$LOG" || exit 2

MAIN_URL="https://langcross.lexicorn.cn/"
DEMO_URL="https://demo.lexicorn.cn/"
BASELINE="$ROOT/UAT_上线审计_20261105/logs/CLEAN-PROD/P16.hdr"

FAIL=0
note() { printf '%s\n' "$*" | tee -a "$LOG/verdict.txt"; }
ok()   { note "PASS $*"; }
bad()  { note "FAIL $*"; FAIL=$((FAIL + 1)); }

# ★ 清代理：不清就会被 127.0.0.1:55519 截成 CONNECT 隧道行
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy
CURL_OPTS=(--noproxy '*' -sSL --connect-timeout 8 --max-time 20)

count_line() {
  local n
  n=$(grep -ciE "$2" "$1" 2>/dev/null)
  [ -z "$n" ] && n=0
  printf '%s' "$n"
}

# 判据 ①：必须有真实最终状态行（排除 CONNECT 隧道行）且头非空
valid_hdr() {
  local f="$1" bytes real
  [ -f "$f" ] || return 1
  bytes=$(wc -c < "$f" | tr -d ' ')
  real=$(grep -E '^HTTP/[0-9.]+ [0-9]{3}' "$f" 2>/dev/null | grep -vc 'Connection Established')
  [ -z "$real" ] && real=0
  [ "$bytes" -ge 500 ] && [ "$real" -ge 1 ]
}

# 响应头在收到头那一刻即落盘；body 传输慢/超时（curl rc=28）不使头失效。
# 判据 ① 只看落盘头本身，不拿 curl 退出码当抓包失败——这正是上次误判的机制。
# ★ 直连偶发 SSL 握手超时，单发一次不能定论：每站最多 3 次，逐次留证，取第一份有效头。
grab() {
  local name="$1" url="$2"
  local hdr="$LOG/$name.hdr" m rc i tryhdr tb
  : > "$LOG/$name.tries.txt"
  for i in 1 2 3; do
    tryhdr="$LOG/$name.try$i.hdr"
    m=$(curl "${CURL_OPTS[@]}" -D "$tryhdr" -o /dev/null \
        -w '%{http_code} %{size_download} %{content_type} %{url_effective} %{http_version}' \
        "$url" 2> "$LOG/$name.try$i.err")
    rc=$?
    tb=$(wc -c < "$tryhdr" 2>/dev/null | tr -d ' ')
    [ -z "$tb" ] && tb=0
    printf 'try=%s curl_rc=%s hdr_bytes=%s meta=%s\n' "$i" "$rc" "$tb" "${m}" \
      >> "$LOG/$name.tries.txt"
    if valid_hdr "$tryhdr"; then
      cat "$tryhdr" > "$hdr"
      printf 'url=%s\nts=%s\n%s\nhdr_bytes=%s\ncurl_rc=%s\nwinning_try=%s\nproxy=disabled\nbody=discarded\n' \
        "$url" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${m}" "$tb" "$rc" "$i" > "$LOG/$name.meta"
      note "GRAB $name winning_try=$i code_size_url=${m} hdr_bytes=$tb curl_rc=$rc"
      return 0
    fi
    note "RETRY $name try=$i 无效头（hdr_bytes=${tb} curl_rc=${rc}）"
  done
  note "GRAB $name 三次均未取得有效响应头 ⇒ 判据 ① 判红，不得据此推断站点缺 CSP"
  return 1
}

csp_line() { grep -i '^content-security-policy:' "$1" 2>/dev/null | tr -d '\r' | head -1; }

note "=== W0 CSP 完整头抓取 $(date -u +%Y-%m-%dT%H:%M:%SZ) ==="
note "LOG=$LOG"
note "base_main=$MAIN_URL"
note "base_demo=$DEMO_URL"

grab MAIN "$MAIN_URL"
grab DEMO "$DEMO_URL"

# ---- 判据 ① 抓包有效性 -------------------------------------------------
for s in MAIN DEMO; do
  f="$LOG/$s.hdr"
  if valid_hdr "$f"; then
    ok "G1_$s 完整响应头（$(wc -c < "$f" | tr -d ' ') B，含真实状态行）"
  else
    bad "G1_$s 只有隧道行或头为空（$(wc -c < "$f" 2>/dev/null | tr -d ' ') B）⇒ 抓包无效，禁止据此判部署漂移"
  fi
done

# 负向对照：造一份 39 B 隧道样本，判据必须判红（证明判据有齿，不是恒绿）
TUNNEL="$LOG/TUNNEL_ONLY.hdr"
printf 'HTTP/1.1 200 Connection Established\r\n\r\n' > "$TUNNEL"
if valid_hdr "$TUNNEL"; then
  bad "G1_counterproof 隧道样本被判有效 ⇒ 判据失去区分度"
else
  ok "G1_counterproof 隧道样本（$(wc -c < "$TUNNEL" | tr -d ' ') B）被正确判红"
fi

# ---- 判据 ② 两站各自 CSP 有无 -----------------------------------------
for s in MAIN DEMO; do
  f="$LOG/$s.hdr"
  n=$(count_line "$f" '^content-security-policy:')
  if [ "$n" -ge 1 ]; then
    ok "G2_$s CSP 在位（$(csp_line "$f" | wc -c | tr -d ' ') B）"
  else
    bad "G2_$s 缺 content-security-policy"
  fi
done

# ---- 判据 ③ 与 CLEAN-PROD 存档的 CSP 值逐字节等值 -----------------------
if [ -f "$BASELINE" ]; then
  csp_line "$BASELINE" > "$LOG/CLEAN-PROD.csp"
  csp_line "$LOG/MAIN.hdr" > "$LOG/MAIN.csp"
  if cmp -s "$LOG/CLEAN-PROD.csp" "$LOG/MAIN.csp" && [ -s "$LOG/MAIN.csp" ]; then
    ok "G3_baseline 主站 CSP 与 CLEAN-PROD 存档逐字节等值"
  else
    bad "G3_baseline 主站 CSP 与 CLEAN-PROD 存档不一致（或存档无 CSP）"
  fi
else
  bad "G3_baseline 基线文件不存在：$BASELINE"
fi

# ---- 判据 ④ 其余安全头（缺 CSP 时尤其要看）-----------------------------
for s in MAIN DEMO; do
  f="$LOG/$s.hdr"
  for h in strict-transport-security x-frame-options x-content-type-options permissions-policy referrer-policy; do
    n=$(count_line "$f" "^$h:")
    if [ "$n" -ge 1 ]; then ok "G4_${s}_$h 在位"; else bad "G4_${s}_$h 缺失"; fi
  done
done

# ---- 附证：带代理复跑一次，记录隧道形态（不参与判绿）--------------------
PROXY_VAR="${HTTPS_PROXY_PRESET:-http://127.0.0.1:55519}"
curl -sSL --max-time 15 -x "$PROXY_VAR" -D "$LOG/PROXY_SAMPLE.hdr" \
     -o /dev/null "$MAIN_URL" >/dev/null 2>&1 || true
pb=$(wc -c < "$LOG/PROXY_SAMPLE.hdr" 2>/dev/null | tr -d ' ')
[ -z "$pb" ] && pb=0
if [ "$pb" -gt 0 ] && [ "$pb" -le 100 ] && grep -q 'Connection Established' "$LOG/PROXY_SAMPLE.hdr"; then
  ok "G5_repro 代理样本仅 ${pb} B 的 CONNECT 隧道行 ⇒ 与 11-10 批次 39 B 同形，根因坐实"
elif [ "$pb" -gt 100 ]; then
  ok "G5_repro 代理样本 ${pb} B 含隧道行+完整响应头；本次直连判据不受影响"
else
  note "INFO G5_repro 代理样本不可得；根因仍由「清代理后即得完整头 + 历史 39 B 恰为 CONNECT 行」支撑"
fi

note "----------------------------------------"
if [ "$FAIL" -eq 0 ]; then
  note "CSP_LIVE_ALL_OK=1"
  exit 0
else
  note "CSP_LIVE_ALL_OK=0 FAIL=$FAIL"
  exit 1
fi
