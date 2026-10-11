#!/usr/bin/env bash
# deploy/postdeploy_20261012.sh —— ★ 2026-10-12「慢链路批」（① 12 语种词典懒加载 ② 静态资源 brotli 预压缩 ③ 通知列表 20 条＋分页）
# 两站换源 + translator-server 换件之后的**现网接线复查**（全程只读：不重启、不写库、不落位任何文件、不改 Caddy）。
#
# 这一批改了三件事，所以这一份就只验这三件事在现网**真的接上了**，一条都不重复单测已经证明过的语义：
#
#   ① 懒加载：入口 chunk 从「静态背十份词典」变成「只背 zh/en＋按需 dynamic import」。
#      现网判据＝**入口 asset 的原文字节数**与**语种 chunk 的文件数**，两站磁盘都要和本机 dist 逐字节等值。
#      为什么用体积而不是"抓一段泰文来 grep"：词典 chunk 的文件名带 hash，且入口里本来也可能有零星非 ASCII 文案，
#      拿字符类当判据是一条**射程不稳**的锁；而"有人把 import 改回静态"这件事在体积上是从 605 KB 跳回 2.6 MB，
#      一眼就是结构性变化 ⇒ 体积等值 + 上限双判据既能抓退化又不会误伤。
#   ② brotli 侧车：`.br`／`.gz` 随 dist 落进 /opt/translator*/web/assets，Caddy `file_server { precompressed br gzip }` 直出。
#      ★ 这一条**必须由公网域名来判**，本机 curl 127.0.0.1 走的不是 Cloudflare 那一跳。
#      现网最坏形态是「侧车在磁盘上、Caddy 配置也对，但 CDN 把 origin 的 content-encoding 剥了/缓存了不带 br 的那一份」——
#      界面照常、构建照常绿、一个字节都没省，而**没有任何症状**（同 §一·12「有自动降级的链路，产物能打开不算验收」）。
#      所以 F 段判三档：`br, gzip` ⇒ br；只 `gzip` ⇒ gzip；不带 Accept-Encoding ⇒ 原文（负向对照，证明不是"永远发 br"）。
#      判据取**下载体的 sha256 等值于本机 .br 文件**，不是只看 content-encoding 这个头——
#      头是谁写的都行，字节对不上就是没发到。
#      ★ 但**只有 br 这一档能在公网做强等值**：Cloudflare 在边缘会把 gzip 重新压缩（10-12 实测
#        rox 的 gzip 档体 194,652 B／sha 18884853…，本机 .gz 侧车 196,179 B／sha 2f15d651…），
#        所以公网档二只判「enc=gzip 且 0<size<raw＝回落链活着」，.gz 的字节等值挪到**源站段 F-2**
#        （带 SNI 打 127.0.0.1，绕开 CDN）。把强等值留在公网那一档＝结构性恒红，
#        而恒红和真断了在屏幕上一模一样，这是本批第一次跑挂在 F 段时读到的形态。
#   ④ ★ 本批改在现网**新长出来的两类锁**（都是当天真踩出来的，见 PROGRESS §十一）：
#      · `/assets/*` 的 `Cache-Control: public, max-age=31536000, immutable` 原来是**无条件**发的，
#        404 也带着它 ⇒ Cloudflare 会把一次探针打出来的 404 按"不可变"钉住**一年**
#        （现网实证：旧入口名 404 至今 `cf-cache-status: HIT`、age 7000 秒仍回 404，
#         而源站 `?cb=` 那条腿 200／605,065 B 完好——坏的是边缘，不是磁盘）。
#        修法两半（三份 conf 各一份，仓库与线上同源）：`@asset file {http.request.uri.path}` 让长缓存
#        只在磁盘真有该文件时发 ＋ `handle_errors` 里给 `/assets/*` 的错误响应显式 `no-store`
#        （⚠️ 少了第二条腿还不行：源站对 404 不带任何 cache-control 时，CF 仍按自己的默认档钉 **4 小时**，
#         实测 `cache-control: max-age=14400` ＋ HIT；带上 no-store 才翻成 `cf-cache-status: BYPASS`）。
#        ⚠️ v2.9.1 **没有** `http.matchers.status`，`status 200` 那一档在 adapt 期就报
#           module not registered ⇒ 只能走 file＋handle_errors 这两档（隔离实例 :8099 逐条现读过）。
#      · 入口文件名多带一段 `CACHE_BUST` 前缀（`vite.config.ts` 的 escape valve）：
#        URL 一旦被边缘钉住，改内容不改名是救不回来的，改名是唯一不依赖 CF 工单/令牌的出路。
#        日常发版**不许**设这个 env（名字照旧只带 hash）；它只在"某个 URL 被缓存毒死"时用一次。
#   ③ 通知分页：路由与鉴权接线（匿名 ⇒ 401 且响应体是 JSON 不是 SPA 兜底 HTML）。
#      ⚠️ 分页语义（limit 夹到 20、has_more、越界 offset）**不在这里判**——那需要真实登录态，
#      在生产造一次登录＝往现网写数据。它的账在 Go 侧两条单测＋UAT 矩阵，这里只确认"这条腿没接错线"。
#
# ★ 三条现成口径（前一波踩过，本份照用，别再写回去）：
#   · 多点判据**分格各取一次**：`curl -w '%{http_code}|%{content_type}|%{size_download}'` 用 | 分隔再 awk 取字段，
#     因为 content_type 本身含空格（`text/html; charset=utf-8`），拿裸词序读会把 size 读成字符串；
#   · `grep -c` 在 `set -e`/管道里零命中退 1 ⇒ 计数一律 `|| true` 收口再自己判数；
#   · systemd 读数按**单元名现读现算**、缺单元既打印又退非零 ⇒ 统一走 sysd() 包装，回退值写成一眼是「没读到」的字面。
#
# ★ 退出码接线（AGENTS §三「验证链自己不算账」①）：远端段末尾 `exit "$RC"` ⇒ 本机 `SSH_RC=$?` ⇒ 末尾 `exit "$SSH_RC"`。
#   少任何一环，屏幕上十几行 FAIL 也会退 0；255 必须读成「链路没通」，绝不当成「现网没问题」。
#   这一条由 backend-go/internal/fileproc/postdeploy_exit_gate_test.go 按 deploy/postdeploy_*.sh **glob 派生**逐文件判。
#
# 用法：bash deploy/postdeploy_20261012.sh
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$REPO/deploy/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$CONF" | head -1)"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$CONF" | head -1)"
[ -n "$HOST" ] || { echo "ABORT=1 主机没解析出来（读 $CONF）"; exit 2; }
[ -n "$PORT" ] || PORT=22
SSH="ssh -p $PORT -o ConnectTimeout=20 -o BatchMode=yes root@$HOST"

MAIN_URL="https://langcross.lexicorn.cn"     # 主站块 translator.conf → 8787，root /opt/translator/web
DEMO_URL="https://rox-test.lexicorn.cn"      # 演示块 translator-demo.conf → 8789，root /opt/translator-demo/web
TENANT_URL="https://rox.lexicorn.cn"         # 租户通配块 translator-tenant.conf → 8787，/assets/* 与主站**同一份磁盘**
# ★ 三个域都要过公网那一段，一个都不能省：本批**动了三份 conf**（主站／演示／租户通配），
#   而这三段的 `file_server { precompressed br gzip }` 是三处各写一遍的独立站点块——
#   主站测通了不代表通配块也接上了（Caddy 的站点块按 Host 精确优先，通配块那条腿只有打租户域名才走到）。
#   10-12 基线读数（发版前现网）：三域 `Accept-Encoding: br, gzip` 一律回 `content-encoding: gzip`
#   （站点里只有 `encode gzip`，磁盘上没有侧车），所以本批上线后"回 br"才是**新事实**，不是"本来就在发"。

# ── 本机侧事实（远端与公网都拿它做等值对照；**现读现算，不写死任何数字**）──
DIST="$REPO/frontend-react/dist"
[ -d "$DIST" ] || { echo "ABORT=1 本机没有 dist（$DIST）——先 npx vite build && node scripts/precompress_dist.mjs"; exit 2; }
ENTRY=$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$DIST/index.html" 2>/dev/null | head -1)
[ -n "$ENTRY" ] || { echo "ABORT=1 dist/index.html 里取不到入口 asset 名"; exit 2; }
[ -f "$DIST/$ENTRY" ] || { echo "ABORT=1 dist 里没有 $ENTRY"; exit 2; }
L_SHA=$(shasum -a 256 "$DIST/$ENTRY" | awk '{print $1}')
L_SIZE=$(wc -c <"$DIST/$ENTRY" | tr -d ' ')
# 侧车：入口那一份的 sha 与字节数是公网抓回的对照基准
if [ ! -f "$DIST/$ENTRY.br" ]; then
  echo "ABORT=1 入口 asset 没有 .br 侧车（$DIST/$ENTRY.br）⇒ 先跑 node scripts/precompress_dist.mjs"
  exit 2
fi
L_BR_SHA=$(shasum -a 256 "$DIST/$ENTRY.br" | awk '{print $1}')
L_BR_SIZE=$(wc -c <"$DIST/$ENTRY.br" | tr -d ' ')
if [ ! -f "$DIST/$ENTRY.gz" ]; then
  echo "ABORT=1 入口 asset 没有 .gz 侧车 ⇒ 预压缩只出了一档，Caddy 的 br→gzip→identity 回落链断在半路"
  exit 2
fi
L_GZ_SHA=$(shasum -a 256 "$DIST/$ENTRY.gz" | awk '{print $1}')
L_GZ_SIZE=$(wc -c <"$DIST/$ENTRY.gz" | tr -d ' ')
# 侧车总数与语种 chunk 总数：远端落盘数必须与这两个数**各自相等**
L_BR_TOTAL=$(find "$DIST/assets" -name '*.br' 2>/dev/null | wc -l | tr -d ' ')
L_GZ_TOTAL=$(find "$DIST/assets" -name '*.gz' 2>/dev/null | wc -l | tr -d ' ')
L_LOCALE_CHUNKS=$(find "$DIST/assets" -maxdepth 1 -name '*.js' 2>/dev/null | grep -cE '/(zh-hant|ja|ko|th|ru|ar|fr|es|de|pt|id|vi|it|nl|pl|tr)-[A-Za-z0-9_-]+\.js$' || true)
[ "${L_BR_TOTAL}" -ge 1 ] || { echo "ABORT=1 本机 dist/assets 里一个 .br 都没有"; exit 2; }
[ "${L_LOCALE_CHUNKS}" -ge 10 ] || { echo "ABORT=1 本机语种 chunk 只有 $L_LOCALE_CHUNKS 份（预期 ≥10）⇒ 词典没被拆出去"; exit 2; }

echo "=== 本机 dist 事实（唯一基准）==="
echo "  入口 asset = $ENTRY  raw=${L_SIZE} B  sha=${L_SHA:0:16}…"
echo "  入口 .br   = ${L_BR_SIZE} B  sha=${L_BR_SHA:0:16}…   侧车总数 br=$L_BR_TOTAL gz=$L_GZ_TOTAL  语种 chunk=$L_LOCALE_CHUNKS"
echo

# ══════════════════════════ 远端段（只读）══════════════════════════
$SSH bash -s -- "$ENTRY" "$L_SHA" "$L_SIZE" "$L_BR_SHA" "$L_BR_SIZE" "$L_BR_TOTAL" "$L_GZ_TOTAL" "$L_LOCALE_CHUNKS" "$L_GZ_SHA" "$L_GZ_SIZE" <<'REMOTE_EOF'
set -uo pipefail
ENTRY="$1"; WANT_SHA="$2"; WANT_SIZE="$3"; WANT_BR_SHA="$4"; WANT_BR_SIZE="$5"; WANT_BR_TOTAL="$6"; WANT_GZ_TOTAL="$7"; WANT_LOCALE="$8"; WANT_GZ_SHA="$9"; WANT_GZ_SIZE="${10}"
RC=0
ok()  { echo "  OK   $*"; }
bad() { echo "  BAD  $*"; RC=1; }
# sysd()：systemd 多点读数统一包装。`is-enabled`／`is-active` 缺单元时**既打印 not-found 又退非零**，
# 写成 $(cmd || echo x) 会把两行一起收进变量 ⇒ 判据永远对不上；这里按"一次调用、取第一行"收口。
sysd() { "$@" 2>/dev/null | head -1; }

echo "----- A 四个单元与健康面（换件/换源后进程是不是那一台）-----"
for u in translator translator-demo ai-assist caddy; do
  st=$(sysd systemctl is-active "$u")
  if [ "$st" = "active" ]; then ok "unit $u = active"; else bad "unit $u = ${st:-<未读>}（必须 active；<未读>＝单元名不存在，先核名字再判）"; fi
done
for p in 8787 8789; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://127.0.0.1:$p/api/health" || echo 000)
  [ "$code" = "200" ] && ok "127.0.0.1:$p/api/health = 200" || bad "127.0.0.1:$p/api/health = $code（8787=主站 8789=演示单元）"
done
# 挂件健康面是 /health，不是 /api/health（这两个 unit 的日志也不落 journal，一律落文件）
AC=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 http://127.0.0.1:8790/health || echo 000)
[ "$AC" = "200" ] && ok "挂件 127.0.0.1:8790/health = 200" || bad "挂件 /health = $AC"

echo
echo "----- B brotli/gzip 侧车落盘对账（两站磁盘 vs 本机 dist）-----"
for w in /opt/translator/web /opt/translator-demo/web; do
  [ -d "$w/assets" ] || { bad "$w/assets 不存在"; continue; }
  n=$(find "$w/assets" -maxdepth 1 -name '*.br' 2>/dev/null | wc -l | tr -d ' ')
  g=$(find "$w/assets" -maxdepth 1 -name '*.gz' 2>/dev/null | wc -l | tr -d ' ')
  if [ "$n" = "$WANT_BR_TOTAL" ] && [ "$g" = "$WANT_GZ_TOTAL" ]; then
    ok "$w 侧车落盘 br=$n gz=$g（与本机 $WANT_BR_TOTAL/$WANT_GZ_TOTAL 一致）"
  else
    bad "$w 侧车落盘 br=$n/$WANT_BR_TOTAL gz=$g/$WANT_GZ_TOTAL 不等 ⇒ 该站没收到本批侧车（tar 前没跑预压缩，或换源只换了半站）"
  fi
  bs="$w/$ENTRY.br"
  if [ -f "$bs" ]; then
    bsha=$(sha256sum "$bs" | cut -d' ' -f1); bsz=$(wc -c <"$bs" | tr -d ' ')
    if [ "$bsha" = "$WANT_BR_SHA" ] && [ "$bsz" = "$WANT_BR_SIZE" ]; then
      ok "$w 入口 .br 逐字节等于本机（${bsz} B）"
    else
      bad "$w 入口 .br sha=${bsha:0:16} size=$bsz ≠ 本机（${WANT_BR_SIZE} B ${WANT_BR_SHA:0:16}）⇒ 磁盘上躺着上一代侧车"
    fi
  else
    bad "$w 没有 $ENTRY.br"
  fi
done

echo
echo "----- C 懒加载落盘对账（入口原文体积 + 语种 chunk 文件数）-----"
# 上限是**退化哨兵**，不是性能承诺：本批实测入口 raw 605,065 B；改回静态 import 会跳回 ~2,633,000 B。
# 写 1,200,000 留一倍余量给正常增量（新增组件仍进入口），同时任何"词典又被静态打进入口"的形态一定越过它。
ENTRY_SIZE_MAX=1200000
for w in /opt/translator/web /opt/translator-demo/web; do
  en=$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$w/index.html" 2>/dev/null | head -1)
  if [ "$en" != "$ENTRY" ]; then bad "$w 入口名=$en ≠ 本机 $ENTRY ⇒ index.html 落后一代"; continue; fi
  sz=$(wc -c <"$w/$en" 2>/dev/null | tr -d ' '); sha=$(sha256sum "$w/$en" 2>/dev/null | cut -d' ' -f1)
  if [ "$sha" = "$WANT_SHA" ] && [ "$sz" = "$WANT_SIZE" ]; then ok "$w 入口 asset 逐字节等于本机（${sz} B）"; else bad "$w 入口 sha=${sha:0:16} size=$sz ≠ 本机（${WANT_SIZE} B ${WANT_SHA:0:16}）"; fi
  [ "${sz:-0}" -le "$ENTRY_SIZE_MAX" ] && ok "$w 入口 raw=${sz} B ≤ ${ENTRY_SIZE_MAX} B（懒加载形态）" \
    || bad "$w 入口 raw=${sz} B > ${ENTRY_SIZE_MAX} B ⇒ 像被打回静态词典（本批实测 ${WANT_SIZE} B）"
  lc=$(find "$w/assets" -maxdepth 1 -name '*.js' 2>/dev/null | grep -cE '/(zh-hant|ja|ko|th|ru|ar|fr|es|de|pt|id|vi|it|nl|pl|tr)-[A-Za-z0-9_-]+\.js$' || true)
  [ "$lc" = "$WANT_LOCALE" ] && ok "$w 语种 chunk=$lc 份（与本机一致）" \
    || bad "$w 语种 chunk=$lc ≠ 本机 $WANT_LOCALE ⇒ 词典没拆成独立 chunk，或该站收到了别的构建"
done

echo
echo "----- D 通知分页的路由与鉴权接线（只判接线，不判分页语义）-----"
for p in 8787 8789; do
  r=$(curl -s -o /tmp/_pd_notif -w '%{http_code}|%{content_type}' --max-time 10 "http://127.0.0.1:$p/api/notifications?limit=20&offset=0" || echo CURL_FAIL)
  code=$(echo "$r" | awk -F'|' '{print $1}'); ct=$(echo "$r" | awk -F'|' '{print $2}')
  head=$(head -c 120 /tmp/_pd_notif 2>/dev/null | tr -d '\n'); rm -f /tmp/_pd_notif
  if [ "$code" = "401" ] && [ "${ct%%;*}" = "application/json" ]; then
    ok "127.0.0.1:$p /api/notifications 匿名=$code 且 application/json（路由在、鉴权在、没落进 SPA 兜底）"
  elif echo "$head" | grep -q '^<!DOCTYPE\|^<html'; then
    bad "127.0.0.1:$p /api/notifications 回的是 HTML（code=$code ct=$ct）⇒ 这条路由压根没被后端接住"
  else
    bad "127.0.0.1:$p /api/notifications 匿名=$code ct=$ct（期望 401 application/json）"
  fi
done

echo
echo "----- E 磁盘脏文件与 dotfile 负向（换源链的旧坑）-----"
for w in /opt/translator/web /opt/translator-demo/web; do
  d=$(find "$w" \( -name .DS_Store -o -name '._*' \) 2>/dev/null | wc -l | tr -d ' ')
  [ "$d" = "0" ] && ok "$w dotfile=0" || bad "$w dotfile=$d（必须 0：scp -r 会把 .DS_Store 带进 web 根，Caddy 对磁盘上真存在的 dotfile 不拦）"
done
echo "  两站 assets 文件数=$(find /opt/translator/web/assets -maxdepth 1 -type f 2>/dev/null | wc -l | tr -d '') / $(find /opt/translator-demo/web/assets -maxdepth 1 -type f 2>/dev/null | wc -l | tr -d '')"

echo
echo "----- F 源站直读（绕开 CDN）：三档编码字节等值 ＋ /assets 错误响应必须 no-store -----"
# ★ 为什么这条必须在**源站**测，不能只靠公网那一段：Cloudflare 会在边缘把 gzip **重新压缩**，
#   于是公网抓回的 .gz 体 sha 天生不等于本机 .gz 侧车（10-12 实测 rox 回
#   enc=gzip size=194652 sha=18884853…，本机 .gz 是 196,179 B sha=2f15d651…）。
#   拿"公网 .gz 体 sha 等值"当判据＝**结构性恒红**；要证明「.gz 侧车真在发、回落链没断」，
#   唯一能做强等值的尺子就是带 SNI 打 127.0.0.1 的源站直读（--resolve，公网域名只用于 Host/SNI，不打 CDN）。
# ★ 第二条腿是本批**现网事故**的锁：`/assets/*` 的 immutable 原来无条件发，404 也带着它进边缘缓存，
#   于是一次探针就把不存在的哈希名钉住一整年（实测旧 URL `cf-cache-status: HIT`＋age 7000 秒仍在 404，
#   而源站磁盘上那个文件本来存在过、换源后改名才 404）。修法两半：
#   ① `@asset file {http.request.uri.path}` ⇒ 只有磁盘上真有这个文件才发长缓存；
#   ② `handle_errors { @asseterr path /assets/*; header … "no-store" }` ⇒ 错误页一律不可缓存。
#   ⚠️ v2.9.1 **没有** `http.matchers.status`（`status 200` 在 adapt 期就报 module not registered），
#      所以只能走 file＋handle_errors 这两档；两档都在隔离实例（:8099，`admin off`）现读过：
#      加段前后 `/`、`/assets/<存在件>`、死上游 502 三条响应一字不差，只有 `/assets/<不存在件>` 多出 no-store。
GZPROBE="index-POSTDEPLOYNOTHERE.js"
for h in langcross.lexicorn.cn rox-test.lexicorn.cn rox.lexicorn.cn; do
  RES="--resolve $h:443:127.0.0.1"
  # F-1 br 档：源站必须发侧车字节
  curl -sk $RES -H 'Accept-Encoding: br' -o /tmp/_pd_src -D /tmp/_pd_srch --max-time 20 "https://$h/$ENTRY" || echo CURL_FAIL >/dev/null
  se=$(tr -d '\r' </tmp/_pd_srch 2>/dev/null | awk 'tolower($1)=="content-encoding:"{print tolower($2)}' | head -1)
  ss=$(wc -c </tmp/_pd_src 2>/dev/null | tr -d ' '); sh=$(sha256sum /tmp/_pd_src 2>/dev/null | cut -d' ' -f1)
  if [ "$se" = "br" ] && [ "$sh" = "$WANT_BR_SHA" ]; then ok "$h 源站 br＝本机 .br（${ss} B）"; else bad "$h 源站 br 档 enc=${se:-<无>} sha=${sh:0:16}/$ss ≠ .br（${WANT_BR_SHA:0:16}/${WANT_BR_SIZE}）"; fi
  # F-2 gzip 档：源站必须发 .gz 侧车字节（这条的强等值只在源站成立，公网档见下面 F 段档二）
  curl -sk $RES -H 'Accept-Encoding: gzip' -o /tmp/_pd_src -D /tmp/_pd_srch --max-time 20 "https://$h/$ENTRY" || true
  se=$(tr -d '\r' </tmp/_pd_srch 2>/dev/null | awk 'tolower($1)=="content-encoding:"{print tolower($2)}' | head -1)
  ss=$(wc -c </tmp/_pd_src 2>/dev/null | tr -d ' '); sh=$(sha256sum /tmp/_pd_src 2>/dev/null | cut -d' ' -f1)
  if [ "$se" = "gzip" ] && [ "$sh" = "$WANT_GZ_SHA" ]; then ok "$h 源站 gzip＝本机 .gz（${ss} B，回落链完整）"; else bad "$h 源站 gzip 档 enc=${se:-<无>} sha=${sh:0:16}/$ss ≠ .gz（${WANT_GZ_SHA:0:16}/${WANT_GZ_SIZE}）⇒ 侧车缺或被运行时重压"; fi
  # F-3 错误腿：/assets 下的 404 必须带 no-store，且绝不许带 immutable/一年档
  curl -sk $RES -o /dev/null -D /tmp/_pd_src4 --max-time 20 "https://$h/assets/$GZPROBE" || true
  c4=$(tr -d '\r' </tmp/_pd_src4 2>/dev/null | awk 'tolower($1)=="cache-control:"{sub(/^[^:]*:[ \t]*/,"");print}' | head -1)
  st4=$(tr -d '\r' </tmp/_pd_src4 2>/dev/null | awk 'tolower($1)=="http/2"{print $2; exit}' | head -1)
  if [ "${st4:-0}" = "404" ] && [ "$c4" = "no-store" ]; then
    ok "$h 源站 /assets 404 cache-control=no-store（边缘拿不到可缓存的错误响应）"
  elif echo "$c4" | grep -q 'immutable\|31536000'; then
    bad "$h 源站 /assets 404 仍带长缓存头（cache-control=${c4:-<无>} code=${st4:-<无>}）⇒ 修法①/② 被写回，一次探针就能把 404 钉一年"
  else
    bad "$h 源站 /assets 404 cache-control=${c4:-<无>}（期望 no-store）code=${st4:-<无>}（期望 404）⇒ ② 那半没生效"
  fi
  rm -f /tmp/_pd_src /tmp/_pd_srch /tmp/_pd_src4
done

echo
if [ "$RC" = "0" ]; then echo "REMOTE_ALL_OK=1"; else echo "REMOTE_FAIL=1（上面有 BAD 行）"; fi
echo POSTDEPLOY_REMOTE_EXIT=$RC
exit "$RC"
REMOTE_EOF
SSH_RC=$?
echo "POSTDEPLOY_LOCAL_EXIT=$SSH_RC"

# ══════════════════════════ 公网段（本机发起，走 Cloudflare 那一跳）══════════════════════════
# ★ 这一段的三条档是一个整体：**正向（br）＋回落对照（gzip）＋负向对照（identity）**。
#   只留第一条会有两种假绿：① Caddy 其实一直在发 br 而侧车不存在（运行时 encode 也会写 content-encoding: br），
#   ② origin 的 br 被 CDN 换成 gzip 或剥掉而脚本看不见。字节 sha 等值才是"这份 .br 真发到了客户手上"。
echo
echo "----- F 公网 br/gzip/identity 三档（Cloudflare 透传取证）-----"
PUB_RC=0
TMPB="/tmp/_pd_body.$$"; TMPH="/tmp/_pd_hdr.$$"
# pd_fetch：一次请求同时收 body／响应头／状态码。旧写法每档拨两次（一次拿头、一次拿体），
# 慢链路下这一段自己就成了发版验收里最慢的一条腿，而且两次请求之间 CDN 缓存可能换档 ⇒ 头和体不同源。
# 现在 CODE/SIZE/SHA/ENC 是**同一次请求**的四个读数，判据才谈得上"这份 .br 真发到了客户手上"。
pd_fetch() {
  BODY_OUT="$1"; HDR_OUT="$2"; URL_IN="$3"; shift 3
  rm -f "$BODY_OUT" "$HDR_OUT"
  CODE=$(curl -s -o "$BODY_OUT" -D "$HDR_OUT" -w '%{http_code}' --max-time 60 "$@" "$URL_IN") || CODE=000
  [ -f "$BODY_OUT" ] || : > "$BODY_OUT"
  SIZE=$(wc -c <"$BODY_OUT" | tr -d ' ')
  SHA=$(shasum -a 256 "$BODY_OUT" 2>/dev/null | awk '{print $1}')
  ENC=$(tr -d '\r' <"$HDR_OUT" 2>/dev/null | awk 'tolower($1)=="content-encoding:"{print tolower($2)}' | head -1)
}
for u in "$MAIN_URL" "$DEMO_URL" "$TENANT_URL"; do
  # 档一：br, gzip ⇒ 必须 br，且下载体逐字节等于本机 .br
  pd_fetch "$TMPB" "$TMPH" "$u/$ENTRY" -H 'Accept-Encoding: br, gzip'
  if [ "$ENC" = "br" ] && [ "$SHA" = "$L_BR_SHA" ]; then
    echo "  OK   $u/$ENTRY content-encoding=br 且下载体 sha＝本机 .br（${SIZE} B code=$CODE）"
  else
    echo "  BAD  $u/$ENTRY enc=${ENC:-<无>} sha=${SHA:0:16} size=$SIZE code=$CODE —— 期望 br/${L_BR_SHA:0:16}（${L_BR_SIZE} B）⇒ 侧车没发出／CDN 剥了 br／磁盘是旧代"
    PUB_RC=1
  fi

  # 档二：只 gzip ⇒ 必须回 gzip。**这一档刻意不做字节等值**（★ 10-12 现网实测覆盖旧判据）：
  #   Cloudflare 在边缘会把 gzip 响应重新压缩，抓回体的 sha 天生不等于本机 .gz 侧车
  #   （实测 rox enc=gzip size=194652 sha=18884853… vs 本机 196,179 B sha=2f15d651…），
  #   旧判据写成等值就是**结构性恒红**——而"恒红"和"真断了"在屏幕上长得一样，会把人引去查侧车。
  #   公网这一档只证明**回落链活着**（enc=gzip 且体积明显小于原文）；字节等值由上面源站段 F-2 强判。
  pd_fetch "$TMPB" "$TMPH" "$u/$ENTRY" -H 'Accept-Encoding: gzip'
  if [ "$ENC" = "gzip" ] && [ "${SIZE:-0}" -gt 0 ] && [ "${SIZE:-0}" -lt "$L_SIZE" ]; then
    echo "  OK   $u/$ENTRY 只给 gzip 时回压缩档（size=$SIZE < raw=$L_SIZE；体的 sha=${SHA:0:16} 是 CF 边缘重压后的字节，不与本机 .gz 等值——等值看源站段 F-2）"
  else
    echo "  BAD  $u/$ENTRY enc=${ENC:-<无>} size=$SIZE/$L_SIZE code=$CODE —— 期望 enc=gzip 且 0<size<raw ⇒ 回落链那一半没接上（侧车缺／CDN 把档换成原文）"
    PUB_RC=1
  fi

  # 档三：负向对照——不带 Accept-Encoding 的客户端必须拿原文（证明 br 不是"对谁都说 br"）
  pd_fetch "$TMPB" "$TMPH" "$u/$ENTRY"
  if [ -z "$ENC" ] && [ "$SIZE" = "$L_SIZE" ]; then
    echo "  OK   $u/$ENTRY 无 Accept-Encoding 时发原文（${SIZE} B＝本机 raw，负向对照成立）"
  else
    echo "  BAD  $u/$ENTRY 无 Accept-Encoding 却 enc=${ENC:-<无>} size=$SIZE/$L_SIZE ⇒ 负向对照不成立（客户端协商这一层判据不可信）"
    PUB_RC=1
  fi

  # 首页品牌注入（§一·5 的三域判据，本批换了源、动过 Caddy，照旧复验一次）
  b=$(curl -s --max-time 25 "$u/" | grep -c '__BRANDING__' || true)
  if [ "$b" = "1" ]; then echo "  OK   $u/ 首页 __BRANDING__ 命中=1"; else echo "  BAD  $u/ __BRANDING__ 命中=$b（应为 1）"; PUB_RC=1; fi
  # ★ content_type 含空格 ⇒ 先整段取出值、再按 ; 切第一格，不拿 awk 的裸词序读
  dsct=$(curl -s -o /dev/null -D - --max-time 25 "$u/.DS_Store" 2>/dev/null | tr -d '\r' \
    | grep -im1 '^content-type:' | sed 's/^[^:]*:[[:space:]]*//' | cut -d';' -f1)
  if [ "$dsct" = "text/html" ]; then
    echo "  OK   $u/.DS_Store 仍是 SPA 兜底 HTML（content-type=text/html）"
  else
    echo "  BAD  $u/.DS_Store content-type=${dsct:-<无>}（不是 text/html ⇒ 磁盘上可能还有真文件）"; PUB_RC=1
  fi

  # ★ 边缘那一跳的「404 不许可缓存」判据（本批现网事故的公网腿锁）：
  #   探针名带 404PROBE 字样，**永不与 vite 生成的哈希名同名**，所以就算这条真把一次 404 送进边缘缓存，
  #   也没有任何页面引用它 ⇒ 不伤访客；而它要抓的是"immutable 被写回无条件档"这个退化形态。
  pd_fetch "$TMPB" "$TMPH" "$u/assets/index-404PROBE-NEVERREAL.js"
  cc=$(tr -d '\r' <"$HDR_OUT" 2>/dev/null | awk 'tolower($1)=="cache-control:"{sub(/^[^:]*:[ \t]*/,"");print}' | head -1)
  cfs=$(tr -d '\r' <"$HDR_OUT" 2>/dev/null | awk 'tolower($1)=="cf-cache-status:"{print toupper($2)}' | head -1)
  if [ "$CODE" = "404" ] && ! echo "$cc" | grep -q 'immutable\|31536000'; then
    echo "  OK   $u 的 /assets 404 不带长缓存头（cache-control=${cc:-<无>} cf=$cfs code=$CODE）"
  else
    echo "  BAD  $u 的 /assets 404 cache-control=${cc:-<无>}（含 immutable/31536000 ⇒ 一次探针就能把 404 在边缘钉一年，本批事故就是这么发生的）code=$CODE cf=$cfs"
    PUB_RC=1
  fi
done

rm -f "$TMPB" "$TMPH"

echo
echo "----- G 体积对账读数（不参与判定，只留这一批的现场数字）-----"
echo "  入口 raw=${L_SIZE} B → br=${L_BR_SIZE} B（省 $(( L_SIZE - L_BR_SIZE )) B）；侧车总数 br=$L_BR_TOTAL gz=$L_GZ_TOTAL；语种 chunk=$L_LOCALE_CHUNKS 份"
echo "  ⚠️ 本批前的入口是 2,633 kB 量级（词典静态打进入口），改懒加载后 raw 见上；两个数是**不同尺度**的对账，别把 2633 当基准写进判据。"

echo
# ★ 两段失败计数**必须合到同一个退出码**：远端全绿而公网段红（CDN 剥掉 br 就是这个形态）时，
#   旧写法 `exit "$SSH_RC"` 会退 0 ⇒ 本批点名要防的那一族（屏幕上写着 FAIL、退出码是绿的）在**本机层**复活。
#   合并规则只在"远端那半本来是 0"时抬成 1 ⇒ SSH_RC=255（链路没通）与 SSH_RC=1（远端有 BAD 行）都原样保留，
#   调度方按码位仍能分辨"没拨通"和"拨通了但现网不对"。
REMOTE_RC="$SSH_RC"   # 合并前先把远端那半的原始退码留住：255（链路没通）／1（远端有 BAD 行）／0 是三种不同的事，
                      # 合并成最终码之后屏幕上必须还能分辨是哪一种，否则排障时又得回到"只知道不绿"。
if [ "$PUB_RC" != "0" ] && [ "$SSH_RC" = "0" ]; then SSH_RC=1; fi
if [ "$SSH_RC" = "0" ]; then echo "POSTDEPLOY_ALL_OK=1"; else echo "POSTDEPLOY_FAIL=1（远端段=$REMOTE_RC 公网段=$PUB_RC 最终=$SSH_RC）"; fi
echo POSTDEPLOY_EXIT=$SSH_RC
# ★ 255＝链路没通，必须照样红（绝不被读成"现网没问题"）
exit "$SSH_RC"
