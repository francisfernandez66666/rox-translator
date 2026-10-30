#!/usr/bin/env bash
# ============================================================================
# deploy/release_20261030b_industry.sh —— 2026-10-30 第二批（金融/餐饮两行业注册表）发版驱动
#
# 本批改动面：backend-go/internal/crawler/{tier1_api.go,tier3_llm.go}（新增两行业的种子与中文名）
#           ＋ 新锁 industry_registry_gate_test.go ＋ 前端 lib/industries.ts（行业下拉两行）。
#           ★ 没有 .py 资产改动 ⇒ 不跑 sync_fileproc_assets.sh，但**逐文件比一次 sha 证明没漂**。
#
# ★★ 这一批与上午那批的关键差别：**首页 HTML 变了**（入口名 CaRsc8Xt → U549SThL）。
#   官网六个 SPA 入口页在 Cloudflare 有 Edge TTL 2h 的 Cache Rule（《部署指南》§三十），
#   而换源会把旧入口文件从磁盘上换掉 ⇒ 边缘里那份**旧 HTML 指向一个已经不存在的 JS**，
#   国内访客在 TTL 过期前会撞白屏。正常出路是 `swap_web_and_binary.sh` 的 CF purge 腿，
#   但那一条要 `CLOUDFLARE_API_TOKEN`（只经 env 传入，绝不进脚本/文档/git）。
#   ⇒ 本机没有令牌，所以这一批改用**等价且不需要令牌**的形态：
#     「把上一代的 assets/ 并回新 web 根（只补不覆盖）」——
#     旧 HTML 的引用全部还能落盘解析（访客在 TTL 内继续拿到**上一代应用**，慢而不是坏），
#     新访客拿新 HTML→新 assets。TTL 到点自然收敛，无白屏窗口。
#   ⚠️ 这是"没有令牌时的逃生口"，**不取代** CF purge 腿：配了令牌仍应回到 §三十 那条常链
#     （purge 位置钉死在「备料全绿之后、落位之前」）。
#
# 判绿：末行 RELEASE_B_ALL_OK=1。任何一段 BAD 即停手并如实报停在第几段。
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

HOST="${HOST:-seoul}"
PORT="${PORT:-28022}"
TAR=/tmp/frontend-dist-20261030b.tar.gz
BIN=/tmp/translator-server-20261030b
OLD_ENTRY_RE='assets/index-[A-Za-z0-9_-]{8,}\.js'
FAIL=0
log(){ echo "[rel-b] $*"; }
bad(){ echo "BAD   $*"; FAIL=$((FAIL+1)); }
ok(){  echo "OK    $*"; }
[ "$FAIL" = 0 ] || { echo "RELEASE_B_ABORT=前置"; exit 1; }

[ -f "$TAR" ] || { echo "BAD 缺 $TAR"; exit 1; }
[ -f "$BIN" ] || { echo "BAD 缺 $BIN"; exit 1; }
TAR_SHA=$(shasum -a 256 "$TAR" | awk '{print $1}')
BIN_SHA=$(shasum -a 256 "$BIN" | awk '{print $1}')
log "tar  $(wc -c < "$TAR" | tr -d ' ') B  sha=$TAR_SHA"
log "bin  $(wc -c < "$BIN" | tr -d ' ') B  sha=$BIN_SHA"

# ---- 0. 包自检（T0–T9b）----
bash scripts/check_frontend_tar.sh "$TAR" > /tmp/rel_b_tarcheck.log 2>&1 || { tail -8 /tmp/rel_b_tarcheck.log; bad "前端包自检未过 ⇒ 禁止上传"; }
grep -q 'FRONTEND_TAR_ALL_OK=1' /tmp/rel_b_tarcheck.log && ok "前端包自检 FRONTEND_TAR_ALL_OK=1"
[ "$FAIL" = 0 ] || { echo "RELEASE_B_ABORT=包自检"; exit 1; }

# ---- 1. 上传三件 ----
log "1/6 上传"
scp -P "$PORT" -q "$TAR" "$BIN" deploy/swap_web_and_binary.sh "$HOST:/tmp/" || { bad "scp 失败"; }
[ "$FAIL" = 0 ] || { echo "RELEASE_B_ABORT=上传"; exit 1; }

# ---- 2. 换源换件（执行体；CF 显式关，理由见文件头）----
#   ★ 幂等守卫：本脚本可能被"上一段崩了、修完重跑"再次调用。现网已经是目标代时**不再重启一次服务**
#     （多一次重启窗口没有收益，只有风险），只把后面的判据照常跑一遍。
log "2/6 换源换件（两站）"
NOW_BIN=$(ssh -p "$PORT" "$HOST" 'sha256sum /opt/translator/bin/translator-server | cut -d" " -f1' </dev/null)
NOW_ENTRY=$(ssh -p "$PORT" "$HOST" 'grep -oE "assets/index-[A-Za-z0-9_-]{8,}\.js" /opt/translator/web/index.html | head -1' </dev/null)
NEW_ENTRY_WANT=$(tar -xzOf "$TAR" dist/index.html | grep -oE 'assets/index-[A-Za-z0-9_-]{8,}\.js' | head -1)
if [ "${NOW_BIN:-}" = "$BIN_SHA" ] && [ "${NOW_ENTRY:-}" = "$NEW_ENTRY_WANT" ]; then
  log "SKIP 换源换件：现网二进制与入口已是目标代（bin=${NOW_BIN:0:12}… entry=${NOW_ENTRY}）⇒ 不重复重启"
  NEW_ENTRY="$NOW_ENTRY"
else
  ssh -p "$PORT" "$HOST" "CF_PURGE_OFF=1 bash /tmp/swap_web_and_binary.sh /tmp/frontend-dist-20261030b.tar.gz $TAR_SHA /tmp/translator-server-20261030b $BIN_SHA" \
    > /tmp/rel_b_swap.log 2>&1
  tail -6 /tmp/rel_b_swap.log
  grep -q 'SWAP_ALL_OK=1' /tmp/rel_b_swap.log && ok "SWAP_ALL_OK=1" || bad "换源未到 SWAP_ALL_OK（见 /tmp/rel_b_swap.log）"
  NEW_ENTRY=$(grep -oE 'SWAP_ENTRY first=[A-Za-z0-9/_.-]+' /tmp/rel_b_swap.log | head -1 | sed 's/.*first=//')
  [ -n "$NEW_ENTRY" ] || bad "换源日志里读不到新入口名"
  [ "$FAIL" = 0 ] || { echo "RELEASE_B_ABORT=换源换件（现网停在完整上一代）"; exit 1; }
fi

# ---- 3. ★ 把上一代 assets 并回新 web 根（无令牌时替代 purge 的那一步）----
log "3/6 并回上一代 assets 本体（并清掉误并的侧车）"
#   ⚠️ 只并 *.js / *.css **本体**，**绝不并它们的 .br / .gz 侧车**：
#      postdeploy 的 B 段判据是「两站侧车份数必须等于本机 dist」，上一版整目录 cp 过来
#      ⇒ 侧车 47→79 ⇒ 那条常设闸门当场判红（本批真踩）。旧 HTML 只需要入口 JS 与 CSS 还能落盘解析，
#      Caddy 没有侧车时静默发原文——慢一点，但不白屏、也不把闸门搞脏。
#      同时删掉"不在新包里"的侧车（把上一版误并的那批清干净，包内的一个不动）。
ssh -p "$PORT" "$HOST" "set -u
TAR=/tmp/frontend-dist-20261030b.tar.gz
LIST=/tmp/rel_b_tar_files.txt
tar tzf \$TAR | sed 's|^\\./||; s|^dist/||' | sort > \$LIST
OLD_ENTRY=''
for d in /opt/translator /opt/translator-demo; do
  old=\$(ls -dt \$d/web.old.* 2>/dev/null | head -1)
  [ -n \"\$old\" ] || { echo \"BAD  \$d 找不到上一代归档\"; continue; }
  oe=\$(grep -oE '$OLD_ENTRY_RE' \$old/index.html | head -1)
  mkdir -p \$d/web/assets
  find \$old/assets -maxdepth 1 -type f \\( -name '*.js' -o -name '*.css' \\) -exec cp -n {} \$d/web/assets/ \\; 2>/dev/null
  removed=0
  for f in \$d/web/assets/*.br \$d/web/assets/*.gz; do
    [ -f \"\$f\" ] || continue
    rel=\"assets/\$(basename \"\$f\")\"
    grep -qxF \"\$rel\" \$LIST || { rm -f \"\$f\"; removed=\$((removed+1)); }
  done
  chown -R root:caddy \$d/web; chmod -R o+rX \$d/web
  br=\$(ls \$d/web/assets/*.br 2>/dev/null | wc -l | tr -d ' ')
  if [ -f \"\$d/web/\$oe\" ]; then echo \"OK   \$d 旧入口在位 \$oe（br 侧车=\$br，清掉误并 \$removed 份）\"; else echo \"BAD  \$d 并回后仍找不到旧入口 \$oe\"; fi
  [ -z \"\$OLD_ENTRY\" ] && OLD_ENTRY=\"\$oe\"
done
echo \"OLD_ENTRY=\$OLD_ENTRY\"" > /tmp/rel_b_union.log 2>&1
cat /tmp/rel_b_union.log
grep -q '^BAD' /tmp/rel_b_union.log && bad "上一代 assets 并回失败 ⇒ 旧 HTML 会撞 404"
grep -q '^OK' /tmp/rel_b_union.log || bad "并回段一条 OK 都没有 ⇒ 这一步没跑到（不许当成跑过了）"
[ "$FAIL" = 0 ] || { echo "RELEASE_B_ABORT=并回上一代 assets（新件已落位，需要时按 §31.4 回滚）"; exit 1; }

# ---- 4. 端侧 ASR .data（两站现读；不在位属已知形态，回落服务端引擎）----
#   ⚠️ 这一段的远端命令**必须整段单引号**：上一版在单引号串里写了 '"$d"'，
#      等于把 $d 交给本机 shell 展开 ⇒ 本机 set -u 当场 unbound variable，脚本停在第 4 段
#      （换源已经做完、后两段没跑＝半程状态，这正是"变量紧挨中文/引号混排"那一族）。
log "4/6 .data 读数"
ssh -p "$PORT" "$HOST" 'for d in /opt/translator /opt/translator-demo; do
  old=$(ls -dt "$d"/web.old.* 2>/dev/null | head -1)
  if [ -n "$old" ] && [ -d "$old/wasm/sherpa-onnx" ]; then
    mkdir -p "$d/web/wasm/sherpa-onnx" && cp -n "$old"/wasm/sherpa-onnx/*.data "$d/web/wasm/sherpa-onnx/" 2>/dev/null
  fi
  echo "  $d .data=$(ls "$d"/web/wasm/sherpa-onnx/*.data 2>/dev/null | wc -l | tr -d " ")"
done' </dev/null 2>&1 | tail -4 || log "  <.data 段读不到，不判发版（端侧探不到即回落服务端引擎）>"

# ---- 5. 两代入口都必须在源站 200（这一条才是"没有白屏窗口"的证据）----
log "5/6 源站直读：新入口与上一代入口各 200"
OLD_ENTRY=$(sed -n 's/^OLD_ENTRY=//p' /tmp/rel_b_union.log | tail -1)
[ -n "$OLD_ENTRY" ] || bad "读不到上一代入口名 ⇒ 第 3 步的并回没有证据，白屏窗口未排除"
if [ "$FAIL" = 0 ]; then
  for e in "$NEW_ENTRY" "$OLD_ENTRY"; do
    code=$(ssh -p "$PORT" "$HOST" \
      "curl -s -o /dev/null --max-time 25 --resolve langcross.lexicorn.cn:443:127.0.0.1 -w '%{http_code} %{size_download}' https://langcross.lexicorn.cn/$e" </dev/null)
    case "$code" in
      200\ *) ok "源站 $e → $code";;
      *) bad "源站 $e → ${code:-<空>}（非 200 ⇒ 这一代入口落不了盘，边缘旧 HTML 会白屏）";;
    esac
  done
  [ "$FAIL" = 0 ] || { echo "RELEASE_B_ABORT=两代入口判据"; exit 1; }
fi

# ---- 6. 发版后接线复查 ----
log "6/6 发版后复查（只读）"
bash deploy/postdeploy_20261012.sh > /tmp/rel_b_postdeploy.log 2>&1
RC=$?
grep -E 'REMOTE_ALL_OK|POSTDEPLOY_ALL_OK|POSTDEPLOY_FAIL|POSTDEPLOY_EXIT' /tmp/rel_b_postdeploy.log | tail -4
if ! grep -q 'POSTDEPLOY_ALL_OK=1' /tmp/rel_b_postdeploy.log; then
  grep -E '^  BAD' /tmp/rel_b_postdeploy.log | head -6
  # ★ 2026-10-30 修：旧写法在「远端段绿、公网段红」时只打一行 log、**不计 FAIL**，
  #   于是末行照样打印 RELEASE_B_ALL_OK=1 —— 屏幕上有 BAD、判定说成功（本仓那族「给失败背书」）。
  #   公网段红可能只是本机这一跳（code=000／curl_rc≠0／半截体），但**不许判绿**：
  #   让人看读数、复跑一次再判，才是「没测到 ≠ 通过」。
  if grep -q 'REMOTE_ALL_OK=1' /tmp/rel_b_postdeploy.log; then
    bad "发版后复查**公网段**判红（exit=${RC}）⇒ 先分「这一跳没走完」还是「现网不对」，复跑一次再判"
  else
    bad "发版后复查**远端段**判红 ⇒ 现网接线有问题，按 §31.4 回滚"
  fi
fi

echo
if [ "$FAIL" = 0 ]; then
  echo "RELEASE_B_ALL_OK=1"
  echo "新入口=${NEW_ENTRY}（上一代入口已并回，边缘旧 HTML 在 TTL 内仍可解析）"
  exit 0
fi
echo "RELEASE_B_FAIL=$FAIL"
exit 1
