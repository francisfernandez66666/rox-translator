#!/usr/bin/env bash
# ============ swap_web_and_binary.sh · 职责说明 ============
# 服务器侧换源与换件的**唯一常设执行体**（在服务器本机跑）：两站 web 根各自独立换源并各自复验，
# 两个后端二进制各自备份后替换，最后 restart 后端单元并自证 is-active。全程不碰 caddy。
#
# 用法（本机先把三样东西发上去，sha 一律现读现比）：
#   scp frontend-dist.tar.gz <服务器>:/tmp/ && scp translator-server-linux <服务器>:/tmp/translator-server-new
#   scp deploy/swap_web_and_binary.sh <服务器>:/tmp/
#   ssh <服务器> 'bash /tmp/swap_web_and_binary.sh /tmp/frontend-dist.tar.gz <tar 的 sha256> /tmp/translator-server-new <二进制的 sha256>'
#
# ★ 建立经过（2026-10-13，真踩）：那一批手工写的换源脚本把**部署根**（/opt/translator）当成了 **web 根**传进去，
#   于是 `mv $root $root.old.$TS` 把 bin／data／log／.venv／web 整棵挪走、放上去的只是 dist 内容，
#   两个单元进入崩溃重启循环（主站每枪退 209/STDOUT＝日志目录没了、演示站每枪退 203/EXEC＝可执行文件不在），
#   各失败 23 次，两站停 **1 分 24 秒**（07:44:19→07:45:43，两端点 journal 实读；靠事后按原路搬回才恢复）。
#   所以这一版把那条教训变成**机械守卫**：
#   ① 根路径必须以 `/web` 结尾，且同级必须有 `bin/`，否则整站拒绝执行；
#   ② 校验（tar／二进制 sha、入口名形状、首页引用的每个 asset 真落盘、语种 chunk 份数）全部**在换名之前**；
#   ③ **两段式**：先「备料」（解到 `web.new` 并四道校验，两站都要过），全绿才进「落位」（两步 mv ＋ 换二进制 ＋ 重启）。
#      备料段任一 BAD ⇒ 清掉 `.new`、整段停手＝现网还是完整上一代；
#      旧形态是「本站拒了但别的站照换、二进制照换、单元照重启」，会留下 **web 旧代＋后端新代**的混合半态；
#   ④ 二进制走 `cp 到临时名 + mv rename`（`cp` 直接覆盖运行中文件退 ETXTBSY 会**静默不生效**）；
#   ⑤ 任一条 BAD ⇒ 退出码抬高，`SWAP_ALL_OK` 不出现在屏上（AGENTS §三「验证链自己不算账」：exit 必须一路 exit 到最外层）。
#   ⑥ ★ 2026-10-28 慢链缓解批新增 **CF 边缘 purge 腿**：SPA 六个入口页现在带 Edge TTL 2h 的边缘缓存
#      （国内访客被 Cloudflare 调度到 LAX、源站在首尔，每次 DYNAMIC 回源都要跨太平洋——缓存是免费版能做的缓解）。
#      缓解带来一个新窗口：换源后边缘残留的**旧 HTML** 会引用已被换掉的旧入口 JS ⇒ 访客白屏到 TTL 过期。
#      purge 位置**钉死在「备料全绿之后、落位之前」**：purge 失败 ⇒ 整段 ABORT，此刻边缘已清、源站仍是完整上一代
#      （访客拿到回源旧代，慢而不坏）；反过来「先落位后 purge」会留下「边缘旧 HTML＋源站新代」的白屏窗口，
#      那正是这一腿要消灭的形态。三态 ok／skipped／off 见 `cf_purge` 函数头。
#
# ⚠️ 三条不改的东西：不 reload/restart caddy（它和 `caddy stop` 共用 127.0.0.1:2019 管理面，会停全站）；
#    不删归档以外的任何目录（只把 web.old.* 按**文件名时间戳**裁到 4 份——`ls -t` 会被 cp -a 继承的 mtime 骗）；
#    不写死任何哈希文件名（入口名从新 index.html 现读，只判**形状**）。
# ==========================================================
set -uo pipefail

TAR=${1:?用法: bash swap_web_and_binary.sh <tar 路径> <tar 的 sha256> <待换二进制> <二进制的 sha256>}
TAR_SHA=${2:?缺 tar 的 sha256（本机现读，禁止照抄上一批那份）}
BIN_NEW=${3:?缺待换二进制路径}
BIN_SHA=${4:?缺待换二进制的 sha256}
# 部署根集合可用 env 覆盖：本地拿假树自证守卫时用，生产不设即走现网两棵
DEPLOY_DIRS=${DEPLOY_DIRS:-/opt/translator /opt/translator-demo}
WEB_OWNER=${WEB_OWNER:-root:caddy}
UNITS=${UNITS:-translator translator-demo}
KEEP_ARCHIVES=${KEEP_ARCHIVES:-4}
# 回环端口集合（探针与后面的 /livez 复验**共用这一份**，别再在别处第二次默认，
# 否则两处各写一遍就迟早长出"探针打 A 端口、复验打 B 端口"的裂缝）。
CHECK_PORTS=${CHECK_PORTS:-"8787 8789"}
TS=$(date +%Y%m%d_%H%M%S)
FAIL=0

bad() { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
ok()  { echo "OK   $*"; }

# ---- 0. 上传件完整性：两份 sha 先对上，对不上整段不做（一个字节都不动）----
got=$(sha256sum "$TAR" | cut -d' ' -f1)
[ "$got" = "$TAR_SHA" ] && ok "tar sha 与本机一致（${got}）" || bad "tar sha 不符：期望 $TAR_SHA 实得 $got"
gotb=$(sha256sum "$BIN_NEW" | cut -d' ' -f1)
[ "$gotb" = "$BIN_SHA" ] && ok "二进制 sha 与本机一致（${gotb}）" || bad "二进制 sha 不符：期望 $BIN_SHA 实得 $gotb"
[ "$FAIL" -eq 0 ] || { echo "SWAP_ABORT=上传件校验没过，什么都没动"; exit 1; }

# ---- 1a. 备料段：解到 web.new → 四道校验（**一个字节都不动现有目录**）----
# root 参数必须是 **web 根**（/opt/translator/web 这种形态），不是部署根。
stage_site() {
  local root="$1" name="$2"
  # ★ 守卫 ①：形状。传成部署根＝把 bin/data/log/.venv 一起搬走＝停站，所以这一条比判据都靠前。
  case "$root" in
    */web) : ;;
    *) bad "$name 根路径不以 /web 结尾（实得 ${root}）——**拒绝执行**，见本函数头注释与文件头「建立经过」"; return ;;
  esac
  [ -d "$(dirname "$root")/bin" ] || { bad "$name 部署根 $(dirname "$root") 下没有 bin/，形态不认识，停手"; return; }

  rm -rf "$root.new"
  mkdir -p "$root.new"
  tar xzf "$TAR" -C "$root.new" --strip-components=1 2>/dev/null || { bad "$name 解包失败"; return; }
  rm -f "$root.new/.DS_Store"; find "$root.new" -name '._*' -delete 2>/dev/null

  # 守卫 ②：入口名从**新** index.html 现读，且形状认得出（无哈希名会让 immutable 长缓存与存在性匹配器双双落空）
  local entry
  entry=$(grep -oE 'assets/index-[A-Za-z0-9_-]*\.js' "$root.new/index.html" | head -1)
  [ -n "$entry" ] || { bad "$name 新 index.html 里认不出入口名"; return; }
  case "$entry" in
    assets/index-????????*.js) : ;;
    *) bad "$name 入口名形状不合（${entry}）"; return ;;
  esac
  [ -f "$root.new/$entry" ] || { bad "$name 首页指向 $entry 但磁盘上没有它"; return; }
  ok "$name 入口 ${entry}（raw $(stat -c%s "$root.new/$entry") B；侧车 br=$([ -f "$root.new/$entry.br" ] && echo 在 || echo 缺) gz=$([ -f "$root.new/$entry.gz" ] && echo 在 || echo 缺)）"

  # 守卫 ③：首页引用的**每个** asset 都要真落盘（缺一个是换源后 404 的直接来源）
  local missing=0 total=0 a
  for a in $(grep -oE 'assets/[A-Za-z0-9._-]*\.(js|css)' "$root.new/index.html" | sort -u); do
    total=$((total + 1))
    [ -f "$root.new/$a" ] || { missing=$((missing + 1)); bad "$name 首页引用的 $a 未落盘"; }
  done
  [ "$missing" -eq 0 ] && ok "$name 首页引用 asset $total 个，缺失 0 个" || return

  # 守卫 ④：语种 chunk 份数（懒加载① 的落地证据——改回静态 import 在这一档当场现形）
  # 现网 dist 实测这十份的文件名：ar／de／es／fr／ja／ko／pt／ru／th／zh-hant（均 `<语种>-<8 位哈希>.js`），
  # 所以这一条**不是**只有夹具能过的形状；zh／en 刻意留在入口包里，本来就不该出现在这里。
  local lc
  lc=$(cd "$root.new/assets" 2>/dev/null && ls | grep -cE '^[a-z]{2}(-[a-zA-Z]+)?-[A-Za-z0-9_-]{8}\.js$')
  if [ "${lc:-0}" -ge 10 ]; then ok "$name 语种 chunk $lc 份"; else bad "$name 语种 chunk 只有 ${lc:-0} 份（应 ≥10）"; return; fi

  ok "$name 备料通过（web.new 已就位，现有目录尚未动）"
}

# ---- 1b. 落位段：两步 mv → 权限 → dotfile 复验 → 裁归档（只在**全部**备料通过后执行）----
commit_site() {
  local root="$1" name="$2"
  mv "$root" "$root.old.$TS" && mv "$root.new" "$root" || { bad "$name 两步 mv 失败（此刻目录名可能已换一半，看上一条 BAD 归因）"; return; }
  chown -R "$WEB_OWNER" "$root" 2>/dev/null
  chmod -R o+rX "$root"
  local dirty
  dirty=$(find "$root" -name '.DS_Store' -o -name '._*' | wc -l)
  [ "$dirty" -eq 0 ] && ok "$name 脏文件复验 0" || bad "$name 脏文件复验 $dirty"
  # 归档按**文件名时间戳**裁（不是 ls -t）
  ls -d "$root".old.* 2>/dev/null | sort -r | tail -n "+$((KEEP_ARCHIVES + 1))" | while read -r d; do rm -rf "$d"; done
  ok "$name 换源完成，web 归档现有 $(ls -d "$root".old.* 2>/dev/null | wc -l) 份（保留上限 ${KEEP_ARCHIVES}）"
}

# ---- 1a5. CF 边缘缓存 purge（★ 2026-10-28 慢链缓解批；位置理由见文件头第 ⑥ 条）----
# 三态：ok（真清成功）／skipped（未配 token，出一行可见 NOTICE 后照常发版）／off（CF_PURGE_OFF=1 显式关）。
# ⚠️ token 只经 env `CLOUDFLARE_API_TOKEN` 传入，**绝不写进脚本/文档/git**；zone id 现查不写死。
CF_ZONE_NAME=${CF_ZONE_NAME:-lexicorn.cn}

# cf_api <URL> <METHOD> <payload 可空> <响应落盘文件> [额外 curl 参数]：统一构造并回吐 http_code；
# curl 自身失败（连接被拒／等满 max-time）回 000，由调用方按「这一跳没走完」重拨一次。
cf_api() {
  local url="$1" method="$2" payload="$3" out="$4" extra="${5:-}"
  local args=(-sS -o "$out" -w '%{http_code}' --max-time 60 -X "$method"
              -H "Authorization: Bearer ${CLOUDFLARE_API_TOKEN}")
  [ -n "$payload" ] && args+=(-H "Content-Type: application/json" -d "$payload")
  [ -n "$extra" ] && args+=("$extra")
  curl "${args[@]}" "$url" 2>/dev/null || echo 000
}

cf_purge() {
  if [ "${CF_PURGE_OFF:-0}" = "1" ]; then
    echo "CF_PURGE=off（CF_PURGE_OFF=1 显式下令 ⇒ 边缘可能残留旧 HTML ≤2h，白屏窗口自负）"
    return 0
  fi
  if [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; then
    echo "CF_PURGE=skipped（未配 CLOUDFLARE_API_TOKEN ⇒ 边缘可能残留旧 HTML ≤2h；要消窗口就带 token 重跑）"
    return 0
  fi
  local tmp code zone
  tmp=$(mktemp)
  # ① zone id 现查：写死 zone id＝换 zone 后静默打错地方（屏幕上什么都看不出）。
  code=$(cf_api "https://api.cloudflare.com/client/v4/zones?name=${CF_ZONE_NAME}" GET "" "$tmp")
  [ "$code" = "000" ] && code=$(cf_api "https://api.cloudflare.com/client/v4/zones?name=${CF_ZONE_NAME}" GET "" "$tmp" --http1.1)
  zone=$(grep -oE '"id":"[a-f0-9]{32}"' "$tmp" 2>/dev/null | head -1 | cut -d'"' -f4)
  if [ -z "$zone" ]; then
    rm -f "$tmp"
    bad "CF purge 取不到 zone id（HTTP=${code}，zone=${CF_ZONE_NAME}）⇒ 边缘缓存没清，落位不会开始"
    return 1
  fi
  # ② purge_everything：全 zone 一次清完（主站／演示站／租户品牌域共用这一个 zone）。
  code=$(cf_api "https://api.cloudflare.com/client/v4/zones/${zone}/purge_cache" POST '{"purge_everything":true}' "$tmp")
  [ "$code" = "000" ] && code=$(cf_api "https://api.cloudflare.com/client/v4/zones/${zone}/purge_cache" POST '{"purge_everything":true}' "$tmp" --http1.1)
  if grep -q '"success":true' "$tmp" 2>/dev/null; then
    ok "CF purge 已受理（zone=${zone}，HTTP=${code}）⇒ 边缘 HTML 缓存已清"
    rm -f "$tmp"
    return 0
  fi
  rm -f "$tmp"
  # 重拨只对「这一跳没走完」；4xx/5xx 与 success:false 一律不重拨——摇绿灯就是把白屏窗口藏起来。
  bad "CF purge 未成功（HTTP=${code}）⇒ 边缘缓存没清，落位不会开始（此刻源站仍是完整上一代）"
  return 1
}

for d in $DEPLOY_DIRS; do stage_site "$d/web" "$(basename "$d")"; done
# ★ 两段式（2026-10-13 自证时补的修法）：备料段任一 BAD ⇒ 把 .new 收干净、**整段停在这里**，
#   不去碰二进制也不重启单元。旧形态是「本站拒了但后面的站照换、二进制照换、闹钟照响」＝
#   web 是旧代、后端是新代那种**混合半态**（同 §一·9 那批「部分失败＝混合半态、单一 buildCommit 看不见」）。
if [ "$FAIL" -gt 0 ]; then
  for d in $DEPLOY_DIRS; do rm -rf "$d/web.new"; done
  echo "SWAP_ABORT=备料段有 $FAIL 条 BAD，web／二进制／单元一律未动（现网仍是上一代）"
  exit "$FAIL"
fi
# ★ purge 在**落位之前**（文件头第 ⑥ 条）：失败即停手＝源站仍是完整上一代、边缘已清，
#   访客回源拿旧代（慢而不坏）；先落位后 purge 才会留下「边缘旧 HTML＋源站新代」的白屏窗口。
cf_purge
if [ "$FAIL" -gt 0 ]; then
  for d in $DEPLOY_DIRS; do rm -rf "$d/web.new"; done
  echo "SWAP_ABORT=CF purge 未成功，web／二进制／单元一律未动（现网仍是上一代；边缘缓存已清或未被清都不影响正确性）"
  exit "$FAIL"
fi
for d in $DEPLOY_DIRS; do commit_site "$d/web" "$(basename "$d")"; done

# ---- 2. 二进制：备份 → 落到临时名 → mv rename → 现读 sha 比对 ----
for d in $DEPLOY_DIRS; do
  bin="$d/bin/translator-server"
  [ -f "$bin" ] || { bad "$(basename "$d") 没有 ${bin}（部署根形态不对？）"; continue; }
  cp -a "$bin" "$bin.bak.$TS" || { bad "$(basename "$d") 备份失败"; continue; }
  cp -a "$BIN_NEW" "$bin.tmp" && mv "$bin.tmp" "$bin" || { bad "$(basename "$d") 替换失败"; continue; }
  chmod +x "$bin"
  news=$(sha256sum "$bin" | cut -d' ' -f1)
  [ "$news" = "$BIN_SHA" ] && ok "$(basename "$d") 二进制已到位（${news}）" || bad "$(basename "$d") 二进制 sha 不符：$news"
done

# ---- 3. 重启后端单元并自证（演示单元启动要约 3.4 秒才 bind，先等再读；探活与重启**分开**）----
# ============ #41 S4 · 零停机窗口探针（默认开；关掉要显式 WINDOW_PROBE_OFF=1）============
# 为什么必须有这一段：方案 §四·S4 的原话是「现在的 `is-active` 判据对零停机**完全无感**
# ——它只看进程起没起，不看窗口里有没有 502」。socket activation 做对了没有，只有
# 「在重启那一刻持续打请求、数非 2xx」这一种读法能证明；只看 is-active=active 属于
# 「跑一个脚本看它报不报错」那一族假绿（AGENTS §三「验证链自己不算账」①）。
# 三条口径：
#  ① **默认开，要关必须显式下令**（★ 2026-10-14 从"默认不跑"翻转，翻转理由就写在这里）：
#     旧形态把 URL 交给"下令的人现配"，于是**"这次没配"和"这条锁不存在"在屏幕上同形**，
#     下一次发版照旧没有窗口读数——正是 §三「有脚本无调度要一直问到第二层」那一族。
#     现在按 `CHECK_PORTS` 自动生成回环 `/livez` URL（与复验共用同一份端口集合）；
#     只有显式 `WINDOW_PROBE_OFF=1` 才关（本机假树自证走这一条，那里没有真服务）。
#  ② **URL 只打回环直连后端**（`127.0.0.1:<port>/livez` 这类）——
#     测的是"进程重启那一跳"，不能把跨境 CDN 抖动混进来（那是另一条链的病，见慢链路批）。
#     打 `/livez`（零依赖）不打 `/readyz`／`/api/health`：探依赖的面在启动瞬间**允许** 503，
#     拿它数非 2xx 会把"依赖还没探完"读成"零停机失败"。
#  ③ **采样 0 条＝判红**，不是判跳过：既然明确配了 URL 却没拿到一个读数，
#     那就是这条锁空转（同「有脚本无调度」那一族），把空转写成通过最坏。
PROBE_LOG=""
PROBE_STOP=""
PROBE_PID=""

# probe_status 出四个读数：样本数／非 2xx 数／其中 000（连接失败或超时）数／P99 秒
#   （awk 一次算完，避免 shell 里做浮点）。
# ★ 为什么要单独数 000（2026-10-14 修）：000 与 5xx 的**归因完全不同**——
#   5xx＝Caddy 拿到连接但后端给了错误（socket activation 没生效）；
#   000＝这一跳压根没走完（连接被拒，或排队时间超过了 curl 的 max-time）。
#   混成一串就把"启动慢"读成"零停机失败"，而后者才是这一腿真正要抓的东西。
probe_status() {
  awk 'NF>=2 {n++; if ($2 !~ /^2/) bad++; if ($2 == "000") zero++; t[n]=$3+0}
       END {
         if (n==0) { print "0 0 0 none"; exit }
         for (i=1;i<=n;i++) for (j=i+1;j<=n;j++) if (t[j]<t[i]) { x=t[i]; t[i]=t[j]; t[j]=x }
         idx=int(n*0.99); if (idx<1) idx=1; if (idx>n) idx=n
         printf "%d %d %d %.3f\n", n, bad, zero, t[idx]
       }' "$PROBE_LOG" 2>/dev/null
}

# probe_start 起后台采样腿（每 0.2s 一轮；两轮之间靠 stop 文件收摊，另有硬上限防孤儿）。
# ⚠️ 可移植性：mac 的 BSD `date` 没有 %3N，所以**耗时不靠 shell 做时间差**，
#    直接取 curl 自己的 `%{time_total}`（Linux/mac 同口径，夹具桩也只需回两列）。
# ★ max-time 默认 15s（旧形态是 2s，那是个假红发生器）：socket activation 下"连接进 backlog 排队"
#    是**预期行为**，2s 上限会把"排了 3 秒但成功"计成 000 非 2xx，于是延迟问题被读成 502 问题。
#    真被拒的连接 curl **立刻**就退（不会等满 15s），所以放宽上限不损失"5xx 归零"这一腿的判别力，
#    代价只有一个：窗口里最坏情况每轮多等一些，而探针本来就是后台腿、不参与落位。
probe_start() {
  [ -n "${WINDOW_PROBE_URLS:-}" ] || return 1
  PROBE_MAX_TIME="${WINDOW_PROBE_MAX_TIME:-15}"
  PROBE_LOG=$(mktemp)
  PROBE_STOP=$(mktemp) && rm -f "$PROBE_STOP" # 只要这个文件不存在就继续；由 probe_stop 落它
  : > "$PROBE_LOG"
  (
    i=0
    while [ ! -f "$PROBE_STOP" ] && [ "$i" -lt "${WINDOW_PROBE_MAX_ROUNDS:-600}" ]; do
      i=$((i + 1))
      for u in $WINDOW_PROBE_URLS; do
        # curl 失败时 -w 照样出 `000 <真实耗时>`，所以只在**什么都没输出**时才补桩值，
        # 不许拿固定的 "000 2.000" 覆盖真实读数（那会把"0.01 秒被拒"与"等满上限"读成同一种失败）。
        r=$(curl -s -o /dev/null -w '%{http_code} %{time_total}' --max-time "$PROBE_MAX_TIME" "$u" 2>/dev/null || true)
        [ -n "$r" ] || r="000 0.000"
        echo "$u $r" >> "$PROBE_LOG"
      done
      sleep 0.2
    done
  ) &
  PROBE_PID=$!
  return 0
}

# probe_finish 收摊并判定：非 2xx 必须为 0，P99 必须低于阈值（默认 1s＝方案的"亚秒"口径）。
# 判定结果**抬进 FAIL**：探针是发版判据的一部分，不是给人看着玩的读数。
probe_finish() {
  [ -n "$PROBE_PID" ] || return 0
  : > "$PROBE_STOP"
  wait "$PROBE_PID" 2>/dev/null || true
  local stats n non2 zero p99
  stats=$(probe_status)
  n=$(echo "$stats" | cut -d' ' -f1)
  non2=$(echo "$stats" | cut -d' ' -f2)
  zero=$(echo "$stats" | cut -d' ' -f3)
  p99=$(echo "$stats" | cut -d' ' -f4)
  if [ -z "$n" ] || [ "$n" = "0" ]; then
    bad "窗口探针配了 URL 却**一个样本都没采到**（⇒ 这条锁空转，别把「没测到」读成「通过」）"
    return
  fi
  echo "WINDOW_PROBE samples=$n non2xx=$non2 timeout000=$zero p99_s=$p99 urls=$WINDOW_PROBE_URLS"
  if [ "${non2:-1}" = "0" ]; then
    ok "重启窗口内非 2xx＝0（$n 个样本）"
  else
    # 红字按 000 占比分档，别把两种病因说成同一种
    local real5x
    real5x=$(awk -v b="${non2:-0}" -v z="${zero:-0}" 'BEGIN{d=b-z; print (d<0?0:d)}')
    if [ "${real5x:-0}" != "0" ]; then
      bad "重启窗口内有 $real5x/$n 个**真 5xx／4xx** ⇒ socket activation 没生效（502 仍在），按 §九 L0 回滚口径处置"
    fi
    if [ "${zero:-0}" != "0" ]; then
      bad "重启窗口内有 $zero/$n 个 000（连接被拒或等满 ${PROBE_MAX_TIME:-15}s 上限）⇒ 这一跳没走完：被拒＝接管没生效，等满＝启动太慢；先看 P99 那条读数与 journal 分归因"
    fi
  fi
  # P99 用 awk 比浮点（shell 不做浮点算术）；读数缺失一律判红，不默认合格
  local late
  late=$(awk -v v="${p99:-99}" -v w="${WINDOW_PROBE_P99_SEC:-1}" 'BEGIN{print (v+0>w+0)?"1":"0"}')
  [ "$late" = "1" ] && bad "重启窗口 P99=${p99}s ≥ ${WINDOW_PROBE_P99_SEC:-1}s ⇒ 5xx 虽归零但延迟没进亚秒（S3 那一步的活儿还没完）" \
    || ok "重启窗口 P99=${p99}s < ${WINDOW_PROBE_P99_SEC:-1}s（亚秒达标）"
  rm -f "$PROBE_LOG" "$PROBE_STOP" 2>/dev/null
  PROBE_PID=""
}
# ==============================================================================

# ---- 默认开：URL 没显式配就按 CHECK_PORTS 生成回环 /livez（见上面口径①的翻转理由）----
if [ -z "${WINDOW_PROBE_URLS:-}" ] && [ "${WINDOW_PROBE_OFF:-0}" != "1" ]; then
  # ★ 必须先赋空串再拼接：本脚本 `set -u`，而这一段正是**只有默认档才会走到**的那条腿
  #   （显式配 URL 时整个 if 跳过）。首跑夹具 T7e 抓到它在 247 行 `WINDOW_PROBE_URLS: unbound variable`
  #   当场退出——位置在「已落位 mv 之后、systemctl restart 之前」，真上线就是**两站停在未重启状态**。
  #   ⇒ 这条默认开的腿必须由夹具实跑一次，光读代码看不出（同 §三「有脚本无调度要问到第二层」）。
  WINDOW_PROBE_URLS=""
  for p in $CHECK_PORTS; do WINDOW_PROBE_URLS="$WINDOW_PROBE_URLS http://127.0.0.1:$p/livez"; done
  WINDOW_PROBE_URLS=$(echo "$WINDOW_PROBE_URLS" | sed 's/^ *//')
  export WINDOW_PROBE_URLS
  echo "WINDOW_PROBE=defaulted（未显式配 URL，按 CHECK_PORTS 生成：${WINDOW_PROBE_URLS}）"
fi
if [ "${WINDOW_PROBE_OFF:-0}" = "1" ]; then
  # 显式关也要出一行**读数缺失**的说明：屏幕上"什么都没出"与"关掉了"必须不同形，
  # 否则下一个人分不清"锁没跑"是人为关的还是压根没接线（同 selftest 那条默认关的形状判据）。
  echo "WINDOW_PROBE=off（WINDOW_PROBE_OFF=1 显式下令 ⇒ 本次窗口内 5xx 与 P99 **无读数**，别把没测写成通过）"
else
  probe_start && echo "WINDOW_PROBE=started" || echo "WINDOW_PROBE=off（URL 为空＝端口没解析出来，本次无读数）"
fi
systemctl restart $UNITS
sleep 8
for u in $UNITS; do
  st=$(systemctl is-active "$u" 2>&1)
  [ "$st" = active ] && ok "$u is-active=active" || bad "$u is-active=$st"
done
# 窗口判定放在 is-active 之后：采样腿必须跨过上面那 8 秒等待，否则"窗口"只剩 restart 那一瞬，
# 新进程还没 bind 完的那一段就没进读数（那正是方案要治的延迟）。
probe_finish
# 端口集合在文件头随 env 一起定（探针与这里共用一份，见头注那条纪律）。
for p in $CHECK_PORTS; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$p/livez" 2>/dev/null)
  [ "$code" = 200 ] && ok "回环 :$p /livez=200" || bad "回环 :$p /livez=${code}（这一条不判发版失败，起得慢也会 000）"
done
echo "caddy（本脚本不碰它）=$(systemctl is-active caddy 2>&1)"

# ---- 4. 两站同代复验：现读入口名必须一致且磁盘上真有 ----
read_entry() { grep -oE 'assets/index-[A-Za-z0-9_-]*\.js' "$1/index.html" 2>/dev/null | head -1; }
e1=$(read_entry "$(echo "$DEPLOY_DIRS" | cut -d' ' -f1)/web")
e2=$(read_entry "$(echo "$DEPLOY_DIRS" | cut -d' ' -f2)/web")
echo "SWAP_ENTRY first=$e1 second=$e2 TS=$TS"
[ -n "$e1" ] || bad "第一站现读入口名为空"
[ "$e1" = "$e2" ] || bad "两站不同代：$e1 vs $e2"

if [ "$FAIL" -eq 0 ]; then echo "SWAP_ALL_OK=1"; else echo "SWAP_BAD=$FAIL"; fi
exit "$FAIL"
