#!/usr/bin/env bash
# ============================================================================
# deploy/release_20261102_cftoken.sh —— 2026-11-02 CF 凭据管理面批（两站发版驱动）
#
# 本批改动面：后端 `internal/api/admin_cf_cache.go`（新文件，636 行）＋ `server.go` 两条路由
#           ＋ `crawler/fetch.go` 的联系方式 UA（单源自证）＋ `i18n/catalog_en.go`（DYN 13 条）
#           ＋ 前端 `OpsP` 的「边缘缓存（Cloudflare）」卡片 ＋ 12 语种文案
#           ＋ `deploy/swap_web_and_binary.sh` 的凭据解析口（env > 0600 投影文件，四态读数）。
#           没有 .py 资产改动 ⇒ 不跑 sync_fileproc_assets.sh（仍逐文件比一次 sha 证明没漂）。
#
# ★★ 本批与上一批的关键差别：**入口名变了**（CfEyZeWk → 新构建那份），而官网六个 SPA 入口页
#   在 Cloudflare 有 Edge TTL 2h 的 Cache Rule（《部署指南》§三十）⇒ 换源后边缘残留的旧 HTML
#   会引用**已被换掉的旧入口 JS**（白屏窗口）。
#   本批**刻意不搬上一代 assets**：AGENTS §25-b 那条实证说过，手搬 `web.old.*/assets/*` 会把
#   postdeploy 的「侧车份数／语种 chunk 份数／逐文件 sha」三条判据一起打脏（读数指向的病因
#   与真因完全两回事），而且手工动作不进 sha 对账。⇒ 正确出路只有一条：
#   执行体的 `cf_purge` 腿（**位置钉死在备料全绿之后、落位之前**）。
#   ⚠️ 本机**没有** Cloudflare API Token，所以这一批会如实打 `CF_PURGE=skipped`；
#      用户随后在「运营策略 → 边缘缓存」里粘贴 token 并点「立即清理边缘缓存」即关闭该窗口
#      （这正是本批要交付的能力本身），下一次发版起执行体就会读 0600 投影文件自动拍。
#
# 判绿：末行 RELEASE_CF_ALL_OK=1。任何一段 BAD 即停手并如实报停在第几段。
# 用法：bash deploy/release_20261102_cftoken.sh
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

HOST="${HOST:-seoul}"
PORT="${PORT:-28022}"
TAR="${TAR:-/tmp/frontend-dist.tar.gz}"
BIN="${BIN:-/tmp/translator-server-20261102}"
FAIL=0
log(){ echo "[rel-cf] $*"; }
bad(){ echo "BAD   $*"; FAIL=$((FAIL+1)); }
ok(){  echo "OK    $*"; }

[ -f "$TAR" ] || { echo "BAD 缺 $TAR"; exit 1; }
[ -f "$BIN" ] || { echo "BAD 缺 $BIN"; exit 1; }
TAR_SHA=$(shasum -a 256 "$TAR" | awk '{print $1}')
BIN_SHA=$(shasum -a 256 "$BIN" | awk '{print $1}')
log "tar  $(wc -c < "$TAR" | tr -d ' ') B  sha=$TAR_SHA"
log "bin  $(wc -c < "$BIN" | tr -d ' ') B  sha=$BIN_SHA"

# ---- 0. 前端包自检（T0–T9b，与 deploy_to_production.sh／《部署指南》§五 共用同一把尺子）----
log "0/6 前端包自检"
bash scripts/check_frontend_tar.sh "$TAR" > /tmp/rel_cf_tarcheck.log 2>&1 \
  || { tail -8 /tmp/rel_cf_tarcheck.log; bad "前端包自检未过 ⇒ 禁止上传"; }
grep -q 'FRONTEND_TAR_ALL_OK=1' /tmp/rel_cf_tarcheck.log && ok "FRONTEND_TAR_ALL_OK=1"
[ "$FAIL" = 0 ] || { echo "RELEASE_CF_ABORT=包自检"; exit 1; }

# ---- 0b. 「这个二进制真是这棵树编出来的」要用**运行期会被引用的字符串字面量**当证据 ----
#   ★ AGENTS §三 那条纪律：`-ldflags="-s -w"` 会内联掉函数名，拿函数名判会**假红**；
#   而字符串字面量是数据、一定在只读段里。本批三枚：配置键、留痕键、审计动作名。
for s in cf_api_token cf_purge_last cf_cache_save; do
  n=$(LC_ALL=C grep -c -- "$s" "$BIN" 2>/dev/null || true)
  if [ "${n:-0}" -ge 1 ]; then ok "二进制含运行期特征串 ${s}（${n} 处）"; else bad "二进制缺特征串 ${s} ⇒ 不是这棵树编的"; fi
done
[ "$FAIL" = 0 ] || { echo "RELEASE_CF_ABORT=特征串"; exit 1; }

# ---- 1. 上传三件 ----
log "1/6 上传"
scp -P "$PORT" -q "$TAR" "$HOST:/tmp/frontend-dist-20261102.tar.gz" || bad "scp tar 失败"
scp -P "$PORT" -q "$BIN" "$HOST:/tmp/translator-server-20261102" || bad "scp bin 失败"
scp -P "$PORT" -q deploy/swap_web_and_binary.sh "$HOST:/tmp/" || bad "scp 执行体失败"
[ "$FAIL" = 0 ] || { echo "RELEASE_CF_ABORT=上传"; exit 1; }

# ---- 2. 换源换件（执行体；**不关 purge 腿** —— 让它如实报 skipped/ok/rejected）----
#   ★ 幂等守卫：本脚本可能被「上一段崩了、修完重跑」再次调用。现网已经是目标代时不再重启一次服务。
log "2/6 换源换件（两站）"
NOW_BIN=$(ssh -p "$PORT" "$HOST" 'sha256sum /opt/translator/bin/translator-server | cut -d" " -f1' </dev/null)
NOW_ENTRY=$(ssh -p "$PORT" "$HOST" 'grep -oE "assets/index-[A-Za-z0-9_-]{8,}\.js" /opt/translator/web/index.html | head -1' </dev/null)
NEW_ENTRY_WANT=$(tar -xzOf "$TAR" dist/index.html | grep -oE 'assets/index-[A-Za-z0-9_-]{8,}\.js' | head -1)
OLD_ENTRY=$NOW_ENTRY
log "  现网 bin=${NOW_BIN:0:12}… entry=${NOW_ENTRY} ⇒ 目标 entry=${NEW_ENTRY_WANT}"
if [ "${NOW_BIN:-}" = "$BIN_SHA" ] && [ "${NOW_ENTRY:-}" = "$NEW_ENTRY_WANT" ]; then
  log "SKIP 换源换件：现网已是目标代 ⇒ 不重复重启"
  NEW_ENTRY="$NOW_ENTRY"
else
  ssh -p "$PORT" "$HOST" \
    "bash /tmp/swap_web_and_binary.sh /tmp/frontend-dist-20261102.tar.gz $TAR_SHA /tmp/translator-server-20261102 $BIN_SHA" \
    > /tmp/rel_cf_swap.log 2>&1
  tail -8 /tmp/rel_cf_swap.log
  grep -q 'SWAP_ALL_OK=1' /tmp/rel_cf_swap.log && ok "SWAP_ALL_OK=1" || bad "换源未到 SWAP_ALL_OK（见 /tmp/rel_cf_swap.log）"
  NEW_ENTRY=$(grep -oE 'SWAP_ENTRY first=[A-Za-z0-9/_.-]+' /tmp/rel_cf_swap.log | head -1 | sed 's/.*first=//')
  [ -n "$NEW_ENTRY" ] || bad "换源日志里读不到新入口名"
  # CF 腿的四态：ok／skipped／off／rejected。**四种必须不同形**，且 ok 要带 from= 说明凭据来源。
  if grep -q 'CF_PURGE=rejected' /tmp/rel_cf_swap.log; then
    bad "CF_PURGE=rejected（凭据文件在但权限/属主不合格）⇒ 看病因，不要改成'purge 失败也继续'"
  elif grep -q 'CF_PURGE=ok' /tmp/rel_cf_swap.log; then
    ok "CF_PURGE=ok $(grep -oE 'from=[A-Za-z0-9:/_.-]+' /tmp/rel_cf_swap.log | head -1)"
  elif grep -q 'CF_PURGE=skipped' /tmp/rel_cf_swap.log; then
    log "CF_PURGE=skipped（本机与部署机都没有 token ⇒ 边缘可能残留旧 HTML ≤2h，来源见《部署指南》§三十三）"
  else
    log "CF_PURGE 段没有点名读数 ⇒ 检查执行体是否被旧件覆盖"
  fi
  [ "$FAIL" = 0 ] || { echo "RELEASE_CF_ABORT=换源换件（现网停在完整上一代）"; exit 1; }
fi

# ---- 3. 新入口与**旧入口**都要在源站能解析（旧入口不在＝边缘仍缓存旧 HTML 时会白屏）----
log "3/6 源站直读两代入口"
for e in "$NEW_ENTRY" "$OLD_ENTRY"; do
  [ -n "$e" ] || continue
  code=$(ssh -p "$PORT" "$HOST" \
    "curl -s -o /dev/null --max-time 25 --http1.1 --resolve langcross.lexicorn.cn:443:127.0.0.1 -w '%{http_code} %{size_download}' https://langcross.lexicorn.cn/$e" </dev/null)
  case "$code" in
    200\ *) ok "源站 $e → $code";;
    *) if [ "$e" = "$OLD_ENTRY" ]; then
         log "旧入口 $e → ${code:-<空>}（已被换掉：边缘若仍缓存旧 HTML，访客在 TTL 内白屏 ⇒ 必须在后台点一次「立即清理边缘缓存」）"
       else
         bad "源站 $NEW_ENTRY → ${code:-<空>}（新入口落不了盘 ⇒ 这一代本身坏了）"
       fi;;
  esac
done

# ---- 4. 新路由真的在这台跑着（匿名必须 401，不是 404 也不是 500）----
log "4/6 新路由活体判据（匿名 401）"
for p in /api/admin/ops/cf-cache /api/admin/ops/cf-cache/purge; do
  code=$(ssh -p "$PORT" "$HOST" \
    "curl -s -o /dev/null --max-time 20 --http1.1 --resolve langcross.lexicorn.cn:443:127.0.0.1 -w '%{http_code}' https://langcross.lexicorn.cn$p" </dev/null)
  case "$code" in
    401) ok "匿名 GET $p → 401（鉴权排在方法闸之前，符合 F-64⑨）";;
    404) bad "匿名 GET $p → 404（路由没注册 ⇒ 换的不是这棵树编的件）";;
    *)   bad "匿名 GET $p → ${code:-<空>}（期望 401）";;
  esac
done
# 反向对照：这两条腿必须在**换件之后**问，问的是"这一台真跑着新路由"，
# 不是"仓里有这个文件"。凭据面本身不可匿名访问，所以 401 就是可达性的唯一诚实读数。

# ---- 5. 发版后接线复查（离线的公网段可能因跨境抖动判红，按三态口径复跑再判）----
log "5/6 发版后复查（只读）"
bash deploy/postdeploy_20261012.sh > /tmp/rel_cf_postdeploy.log 2>&1
RC=$?
grep -E 'REMOTE_ALL_OK|POSTDEPLOY_ALL_OK|POSTDEPLOY_FAIL|POSTDEPLOY_EXIT' /tmp/rel_cf_postdeploy.log | tail -4
if ! grep -q 'POSTDEPLOY_ALL_OK=1' /tmp/rel_cf_postdeploy.log; then
  grep -E '^  BAD|⏳' /tmp/rel_cf_postdeploy.log | head -8
  if grep -q 'REMOTE_ALL_OK=1' /tmp/rel_cf_postdeploy.log; then
    bad "发版后复查**公网段**判红（exit=${RC}）⇒ 先分「这一跳没走完」还是「现网不对」，复跑一次再判"
  else
    bad "发版后复查**远端段**判红 ⇒ 现网接线有问题，按 §31.4 回滚"
  fi
else
  ok "POSTDEPLOY_ALL_OK=1"
fi

# ---- 6. 交付面读数（不是判据，是给下一位的坐标）----
log "6/6 交付面读数"
ssh -p "$PORT" "$HOST" 'echo "  bin  sha=$(sha256sum /opt/translator/bin/translator-server | cut -d" " -f1 | cut -c1-16)"
  echo "  entry=$(grep -oE "assets/index-[A-Za-z0-9_-]{8,}\.js" /opt/translator/web/index.html | head -1)"
  echo "  sidecar br=$(ls /opt/translator/web/assets/*.br 2>/dev/null | wc -l | tr -d " ")"
  echo "  cf_token_file=$(ls -l /etc/translator/cf_token 2>/dev/null || echo <未配置>)"
  for u in translator translator-demo; do echo "  $u=$(systemctl is-active $u 2>/dev/null)"; done' </dev/null 2>&1 | tail -8

echo
if [ "$FAIL" = 0 ]; then
  echo "RELEASE_CF_ALL_OK=1"
  echo "新入口=${NEW_ENTRY}"
  echo "下一步（人工）：平台超管登录后台 → 运营策略 → 边缘缓存（Cloudflare）→ 粘贴 token 并保存，"
  echo "              确认投影徽标「在场」后点「立即清理边缘缓存」；这一步同时关闭边缘旧 HTML 的 ≤2h 窗口。"
  exit 0
fi
echo "RELEASE_CF_FAIL=$FAIL"
exit 1
