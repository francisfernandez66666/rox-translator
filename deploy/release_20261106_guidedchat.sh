#!/usr/bin/env bash
# ============================================================================
# deploy/release_20261106_guidedchat.sh —— 2026-11-06 H 批（引导只有一副脸＋模型只出候选＋去掉横幅）两站发版驱动
#
# 本批改动面（决定"要换哪几件"）：
#   后端  internal/api/product_onboard_draft.go（新口 POST /api/product/onboard/draft ＋ 每人每日封顶）
#         internal/engine/onboard_draft.go（模型腿：没配第⑨张卡一次都不拨）
#         internal/engine/costmodel.go（第 7 支 allowance 恒 0）＋ api/estimate.go／api/pricing_meta.go（points_onboard_draft:0）
#         internal/config/config.go（StageOnboardDraft 进 AllStages）＋ llm/platform_cost.go／store/billing_platform_cost.go（平台标签名单）
#         internal/api/server.go（/api/health 出 onboard_draft 状态词）＋ internal/store/product.go（语种形状）
#         internal/i18n/catalog_en.go（第⑨张卡词条）
#   前端  components/guidedchat/*（对话式外壳收成一份）＋ Onboarding.tsx／OnboardingGate.tsx（横幅退役）
#         AiRegisterFlow.tsx（改为消费同一份外壳）＋ admin/ModelsP.tsx（第⑨张卡）＋ 12 语种词典
#   ⇒ 换源（dist）＋换件（translator-server）两站各一次；**assist 二进制本批未动**（internal/assist 零改动）。
#   ⇒ 手册 12 份 PDF 是 17:04 那一代（H1 对话卡的新脸），而现网铺的是 15:45 那一代 ⇒ **必须铺库**（段 3）：
#     它既不在 dist 也不在二进制里，只换源换件不会带上它（AGENTS §一·5 手册批那条）。
#
# ★ 边缘缓存窗口：本批入口 JS 的名字必变，而六个 SPA 入口页在 Cloudflare 带 Edge TTL 2h
#   ⇒ 换源一律走执行体的 cf_purge 腿（位置钉死在「备料全绿之后、落位之前」，§一·5 那条纪律）。
#
# 判绿：末行 RELEASE_H_ALL_OK=1（FAIL=0 且 UNMEASURED=0）。拿到 exit 2 语义（有项没测到）就复跑再判。
# 用法：bash deploy/release_20261106_guidedchat.sh
#   只读预演：DRY=1 bash deploy/release_20261106_guidedchat.sh（只跑到包自检，不上传不落位）
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

HOST="${HOST:-seoul}"
PORT="${PORT:-28022}"
TAR="${TAR:-/tmp/frontend-dist-20261106h.tar.gz}"
BIN="${BIN:-/tmp/translator-server-20261106h}"
PDFTAR="${PDFTAR:-/tmp/manual-pdf-20261106h.tar.gz}"
PDF_LIST="${PDF_LIST:-/tmp/manual-pdf-shas-20261106h.txt}"
DRY="${DRY:-0}"
BASE_MAIN="${BASE_MAIN:-https://langcross.lexicorn.cn}"
BASE_DEMO="${BASE_DEMO:-https://rox-test.lexicorn.cn}"
LANGS="zh zh-hant en ru fr ar es pt de ja ko th"
FAIL=0
UNMEASURED=0
log(){ echo "[rel-h] $*"; }
bad(){ echo "BAD   $*"; FAIL=$((FAIL+1)); }
ok(){  echo "OK    $*"; }

# ---- 0. 三件交付物必须在位；sha 现读现比（禁止写死数字） ----
[ -f "$TAR" ] || { echo "BAD 缺前端包 $TAR（顺序只有一种：vite build → precompress → tar → 本脚本）"; exit 1; }
[ -f "$BIN" ] || { echo "BAD 缺后端件 $BIN（GOOS=linux go build -o …）"; exit 1; }
[ -d 产品手册/pdf ] || { echo "BAD 缺 产品手册/pdf 目录"; exit 1; }
TAR_SHA=$(shasum -a 256 "$TAR" | awk '{print $1}')
BIN_SHA=$(shasum -a 256 "$BIN" | awk '{print $1}')
log "tar  $(wc -c < "$TAR" | tr -d ' ') B  sha=$TAR_SHA"
log "bin  $(wc -c < "$BIN" | tr -d ' ') B  sha=$BIN_SHA"

# ---- 0a. 手册 PDF 清单：12 份逐份 sha（铺库后按这一份清单逐字节核对，而不是"能下载"） ----
: > "$PDF_LIST"
for code in $LANGS; do
  f="产品手册/pdf/LangCross-User-Guide-${code}.pdf"
  if [ ! -f "$f" ]; then bad "缺手册件 $f"; continue; fi
  printf '%s  %s\n' "$(shasum -a 256 "$f" | awk '{print $1}')" "$code" >> "$PDF_LIST"
done
PDF_N=$(wc -l < "$PDF_LIST" | tr -d ' ')
[ "$PDF_N" = "12" ] || bad "PDF 清单只有 ${PDF_N} 份（应 12）"
[ "$FAIL" = 0 ] || { echo "RELEASE_H_ABORT=交付物缺失"; exit 1; }

# ---- 0b. 前端包自检（T0–T9b，与 deploy_to_production.sh／《部署指南》§五 共用同一把尺子） ----
log "0b/6 前端包自检"
if bash scripts/check_frontend_tar.sh "$TAR" > /tmp/rel_h_tarcheck.log 2>&1 \
   && grep -q 'FRONTEND_TAR_ALL_OK=1' /tmp/rel_h_tarcheck.log; then
  ok "FRONTEND_TAR_ALL_OK=1"
else
  tail -10 /tmp/rel_h_tarcheck.log
  bad "前端包自检未过 ⇒ 禁止上传"
fi

# ---- 0c. 「这个二进制真是这棵树编出来的」＝运行期会被引用的字符串字面量（AGENTS §三） ----
#   函数名在 -ldflags="-s -w" 下会被内联掉、不进名字表，拿函数名判会**假红**；字符串是数据，一定在只读段里。
#   本批三枚：新路由路径（注册时引用）／防刷档位键（configIntTier 现读）／模型提示词里那句固定指令（真发出去的那句）。
for s in '/api/product/onboard/draft' 'onboard_draft_daily_limit' '可选语种清单（只能从这里挑）'; do
  n=$(LC_ALL=C grep -c -- "$s" "$BIN" 2>/dev/null || true)
  if [ "${n:-0}" -ge 1 ]; then ok "二进制含运行期特征串 ${s}（${n} 处）"; else bad "二进制缺特征串 ${s} ⇒ 不是这棵树编的"; fi
done
[ "$FAIL" = 0 ] || { echo "RELEASE_H_ABORT=包或件不合格"; exit 1; }

if [ "$DRY" = "1" ]; then
  log "DRY=1 ⇒ 只跑到包自检，不上传不落位"
  echo "RELEASE_H_DRY_ONLY=1"
  exit 0
fi

# ---- 1. 上传五件（tar／二进制／执行体／PDF 包／PDF 清单） ----
log "1/6 上传"
# shellcheck disable=SC2086
tar -czf "$PDFTAR" -C 产品手册/pdf $(for c in $LANGS; do printf 'LangCross-User-Guide-%s.pdf ' "$c"; done) \
  || bad "打 PDF 包失败"
PDFTAR_SHA=$(shasum -a 256 "$PDFTAR" | awk '{print $1}')
scp -P "$PORT" -q "$TAR" "$HOST:/tmp/frontend-dist-20261106h.tar.gz" || bad "scp tar 失败"
scp -P "$PORT" -q "$BIN" "$HOST:/tmp/translator-server-20261106h" || bad "scp bin 失败"
scp -P "$PORT" -q deploy/swap_web_and_binary.sh "$HOST:/tmp/" || bad "scp 执行体失败"
scp -P "$PORT" -q "$PDFTAR" "$HOST:/tmp/manual-pdf-20261106h.tar.gz" || bad "scp PDF 包失败"
scp -P "$PORT" -q "$PDF_LIST" "$HOST:/tmp/manual-pdf-shas-20261106h.txt" || bad "scp PDF 清单失败"
[ "$FAIL" = 0 ] || { echo "RELEASE_H_ABORT=上传"; exit 1; }
log "PDF 包 $(wc -c < "$PDFTAR" | tr -d ' ') B sha=$PDFTAR_SHA"

# ---- 2. 换源换件（常设执行体；purge 腿不许关） ----
#   ★ SKIP_SWAP=1 的用途是**可重入**：执行体已经落位、但后面的段（铺库/判据/冒烟/postdeploy）要单独复跑时，
#     不许为了跑那四段再付一遍两站重启窗口＋边缘缓存窗口（AGENTS §三 那条「证等价而不是再跑一遍发版清单」同族）。
#     它**不跳过**上传（铺库段要用服务器上的 PDF 包与清单），只跳过落位与重启。
log "2/6 换源换件（两站 web＋二进制，含 CF purge 腿）"
if [ "${SKIP_SWAP:-0}" = "1" ]; then
  ok "SKIP_SWAP=1 ⇒ 本段跳过（现网保持在已落位的那一代，不重启、不再清边缘缓存）"
  SWAP_OUT="SWAP_ALL_OK=1（skipped）"
  SWAP_RC=0
else
  SWAP_OUT=$(ssh -p "$PORT" "$HOST" \
    "bash /tmp/swap_web_and_binary.sh /tmp/frontend-dist-20261106h.tar.gz $TAR_SHA /tmp/translator-server-20261106h $BIN_SHA" 2>&1)
  SWAP_RC=$?
  echo "$SWAP_OUT" | tail -25
fi
if [ "$SWAP_RC" != 0 ] || ! printf '%s\n' "$SWAP_OUT" | grep -q 'SWAP_ALL_OK=1'; then
  # ★ 这条消息必须**分得开两种停手位置**（旧写法一律写"停在完整上一代"，而实际踩到的是
  #   「落位已完成、只有重启窗口探针判红」那一档 ⇒ 屏幕上的处置建议与现场完全两回事，
  #   下一位会按"现网还是旧代"去重跑换源，白付一遍两站重启窗口）
  if printf '%s\n' "$SWAP_OUT" | grep -q 'SWAP_ENTRY'; then
    echo "RELEASE_H_ABORT=换源换件（rc=$SWAP_RC；**落位已完成**（见上面 SWAP_ENTRY 行），红的是重启窗口/复验那一条腿 ⇒ 现网已是新代，后续段用 SKIP_SWAP=1 复跑，别再跑一遍落位）"
  else
    echo "RELEASE_H_ABORT=换源换件（rc=$SWAP_RC；备料段拒了＝现网停在完整上一代）"
  fi
  exit 1
fi
ok "SWAP_ALL_OK=1"
if [ "${SKIP_SWAP:-0}" = "1" ]; then
  skip "CF_PURGE 读数本段没跑（SKIP_SWAP=1）；上一次落位那一跑的读数已记在《部署指南》§三十七"
elif printf '%s\n' "$SWAP_OUT" | grep -Eq '^CF_PURGE='; then
  ok "执行体出了 CF_PURGE 那一行（$(printf '%s\n' "$SWAP_OUT" | grep -E '^CF_PURGE=' | head -1)）"
else
  bad "执行体没出 CF_PURGE 读数（skipped／off／ok／rejected 四态之一都不出＝purge 腿不在位）"
fi

# ---- 3. 手册 PDF 铺库（两站共用一个目录 ⇒ 只铺一次；两站的下载面各验一次在段 5） ----
#   形态照《部署指南》§三十六：临时名就位 → 备份上一代 → mv 落位 → root:root 644 → 落盘后**再算一次 sha 与清单等值**。
log "3/6 手册 PDF 铺库（/opt/translator/data/manual）"
INSTALL_PDF=$(ssh -p "$PORT" "$HOST" 'bash -s' <<'REMOTE_PDF'
set -uo pipefail
DIR=/opt/translator/data/manual
NEW=/tmp/manual_new_20261106h
TS=$(date +%Y%m%d%H%M%S)
mkdir -p "$NEW" "$DIR"
tar -xzf /tmp/manual-pdf-20261106h.tar.gz -C "$NEW" || { echo "BAD 解包失败"; exit 1; }
FAIL=0
OKN=0
while read -r want code; do
  [ -n "${code:-}" ] || continue
  src="$NEW/LangCross-User-Guide-${code}.pdf"
  dst="$DIR/${code}.pdf"
  if [ ! -f "$src" ]; then echo "BAD 包内缺 ${code}"; FAIL=$((FAIL+1)); continue; fi
  got=$(sha256sum "$src" | awk '{print $1}')
  if [ "$got" != "$want" ]; then echo "BAD ${code} 包内件与清单不等值"; FAIL=$((FAIL+1)); continue; fi
  if ! cp "$src" "$dst.new"; then echo "BAD ${code} 临时名写入失败"; FAIL=$((FAIL+1)); continue; fi
  if [ -f "$dst" ] && ! cp -a "$dst" "$dst.bak.$TS"; then
    echo "BAD ${code} 上一代备份失败 ⇒ 不覆盖"; rm -f "$dst.new"; FAIL=$((FAIL+1)); continue
  fi
  if ! mv "$dst.new" "$dst"; then echo "BAD ${code} 落位失败"; rm -f "$dst.new"; FAIL=$((FAIL+1)); continue; fi
  chown root:root "$dst" 2>/dev/null || true
  chmod 644 "$dst"
  now=$(sha256sum "$dst" | awk '{print $1}')
  if [ "$now" != "$want" ]; then echo "BAD ${code} 落盘后 sha 变了"; FAIL=$((FAIL+1)); continue; fi
  echo "OK  ${code} $(wc -c < "$dst" | tr -d ' ') B"
  OKN=$((OKN+1))
done < /tmp/manual-pdf-shas-20261106h.txt
rm -rf "$NEW"
if [ "$FAIL" != 0 ] || [ "$OKN" != 12 ]; then echo "PDF_INSTALL_FAIL=$FAIL OKN=$OKN"; exit 1; fi
echo "PDF_INSTALL_ALL_OK=1"
REMOTE_PDF
)
PDF_RC=$?
echo "$INSTALL_PDF" | tail -16
if [ "$PDF_RC" != 0 ] || ! printf '%s\n' "$INSTALL_PDF" | grep -q 'PDF_INSTALL_ALL_OK=1'; then
  echo "RELEASE_H_ABORT=手册铺库（前端后端已换，但库里还是旧稿 ⇒ 这一批不能算发版完成）"
  exit 1
fi
ok "12 份手册逐字节等值落库"

# ---- 4. 现网只读判据：四条腿各问"这一台真跑着新代码"，而不是看版本号 ----
#   ★ 「接口说成功不算验收」的同一条：这三档读数（health 的状态词、meta 的 0、新口的 401）只有新代码会出；
#   ★ 「这一跳没走完」单独记 UNMEASURED，既不判绿也不判成产品缺陷（§一·5 §6.2 那条口径）。
log "4/6 现网只读判据"
FACE=$(ssh -p "$PORT" "$HOST" 'bash -s' <<'REMOTE_FACE'
set -uo pipefail
SITES="https://langcross.lexicorn.cn https://rox-test.lexicorn.cn"
FAIL=0
UNMEAS=0
red(){ printf 'BAD %s\n' "$*"; FAIL=$((FAIL+1)); }
good(){ printf 'OK  %s\n' "$*"; }
unk(){ printf 'UNMEASURED %s\n' "$*"; UNMEAS=$((UNMEAS+1)); }

for B in $SITES; do
  # 4.1 健康面必须出 onboard_draft 状态词（ok＝第⑨张卡配好了；degraded＝没配，动作是**去配**而不是等）
  H=$(curl -sS -m 60 "$B/api/health" 2>/dev/null || echo '')
  if printf '%s' "$H" | grep -q '"onboard_draft"'; then
    good "health 出 onboard_draft $(printf '%s' "$H" | grep -o '"onboard_draft":"[^"]*"' | head -1) [$B]"
  elif [ -z "$H" ]; then
    unk "health 这一跳没走完 [$B]"
  else
    red "health 缺 onboard_draft 字段 ⇒ 后端还是旧代 [$B]"
  fi

  # 4.2 新路由：匿名 POST 必须 401（404＝路由没注册；500＝注册了但装配坏了；200＝鉴权漏了）
  C=$(curl -sS -o /dev/null -w '%{http_code}' -m 60 -X POST "$B/api/product/onboard/draft" \
        -H 'Content-Type: application/json' -d '{"text":"探针"}' 2>/dev/null || echo 000)
  if [ "$C" = 401 ]; then
    good "匿名打新口 401 [$B]"
  elif [ "$C" = 000 ]; then
    unk "新口这一跳没走完 [$B]"
  else
    red "匿名打新口应 401，实得 $C [$B]"
  fi

  # 4.3 对外算价面必须明出 points_onboard_draft:0（不带 omitempty：缺席会被读成"字段没实现"）
  M=$(curl -sS -m 60 "$B/api/pricing/meta" 2>/dev/null || echo '')
  if printf '%s' "$M" | grep -q '"points_onboard_draft":0'; then
    good "pricing/meta 出 points_onboard_draft:0 [$B]"
  elif [ -z "$M" ]; then
    unk "pricing/meta 这一跳没走完 [$B]"
  else
    red "pricing/meta 缺 points_onboard_draft:0 ⇒ 后端旧代 [$B]"
  fi

  # 4.4 引导页深链必须 200（AI 顾问那颗按钮跳的就是它；404＝SPA 白名单没登记）
  D=$(curl -sS -o /dev/null -w '%{http_code}' -m 60 "$B/onboarding" 2>/dev/null || echo 000)
  if [ "$D" = 200 ]; then
    good "/onboarding 深链 200 [$B]"
  elif [ "$D" = 000 ]; then
    unk "/onboarding 这一跳没走完 [$B]"
  else
    red "/onboarding 应 200，实得 $D [$B]"
  fi
done
# 4.5 assist 本批未换件 ⇒ 只登记**这台自己的读数**（unit 状态），不许打一个不存在的路径
#     ⚠️ 两个坑都踩过：① 旧写法猜 /assist-api/health 与 /api/assist/health，两把都回 NOT_FOUND JSON
#        ⇒ 屏幕上像"挂件坏了"，而那只是探针拼错 URL；② unit 名**不是** translator-assist（那是 /opt/translator/bin 下
#        那个二进制名），现役单元是 **ai-assist.service**——拿不存在的单元名问 is-active 会回"inactive"，
#        把"这台没读到"读成"挂件停了"（§三 那条 `systemctl is-enabled` 缺单元既打 not-found 又退非零的同族）。
A=$(systemctl is-active ai-assist 2>/dev/null || echo '<未读到>')
printf 'NOTE assist 单元状态（本批未动 assist，只登记）：%s\n' "$A"

if [ "$FAIL" != 0 ]; then echo "FACE_FAIL=$FAIL"; exit 1; fi
echo "FACE_UNMEASURED=$UNMEAS"
echo "FACE_ALL_OK=1"
REMOTE_FACE
)
FACE_RC=$?
echo "$FACE" | tail -22
if [ "$FACE_RC" = 0 ] && printf '%s\n' "$FACE" | grep -q 'FACE_ALL_OK=1'; then
  ok "现网只读判据零红项"
else
  bad "现网只读判据有红项（见上面 BAD 行）"
fi
FACE_UN=$(printf '%s\n' "$FACE" | grep -c '^UNMEASURED' || true)
if [ "${FACE_UN:-0}" != 0 ]; then
  log "有 ${FACE_UN} 项没测到（跨境抖动）⇒ 复跑段 4 再判，既不读成通过也不读成缺陷"
  UNMEASURED=$((UNMEASURED+FACE_UN))
fi

# ---- 5. 手册下载面冒烟（两站各跑一次；四件套判据＋收完整，**三态**） ----
#   退出码 0＝全绿／1＝有红项（内容不对，该有人去修）／2＝有 N 项没测到（跨境抖动）
#   ⇒ 发版判据要求 0；拿到 2 复跑再判，把 2 读成"通过"或读成"产品缺陷"都是误判（AGENTS §一·5 §6.2 ⑤）。
log "5/6 手册 PDF 公网下载面（两站各一次）"
for B in "$BASE_MAIN" "$BASE_DEMO"; do
  tag=$(printf '%s' "$B" | sed 's|https://||; s|[^a-z0-9]*|-|g')
  BASE="$B" bash deploy/smoke_manual_pdf.sh > "/tmp/rel_h_pdf_${tag}.log" 2>&1
  SM_RC=$?
  SM_UN=$(grep -c '^  ⏳' "/tmp/rel_h_pdf_${tag}.log" || true)
  if [ "$SM_RC" = 0 ]; then
    ok "smoke_manual_pdf 全绿：$B"
  elif [ "$SM_RC" = 2 ]; then
    log "smoke_manual_pdf 有 ${SM_UN:-?} 项没测到（exit 2）：$B ⇒ 复跑段 5，不判绿也不判成缺陷（详见 /tmp/rel_h_pdf_${tag}.log）"
    UNMEASURED=$((UNMEASURED + ${SM_UN:-1}))
  else
    tail -12 "/tmp/rel_h_pdf_${tag}.log"
    bad "smoke_manual_pdf 判红：$B（详见 /tmp/rel_h_pdf_${tag}.log）"
  fi
done

# ---- 6. 发版后复验（**本地跑**；三态：FAIL=0 且 UNMEASURED=0 才叫过） ----
log "6/6 postdeploy 复验"
PD_OUT=$(bash deploy/postdeploy_20261012.sh 2>&1); PD_RC=$?
echo "$PD_OUT" | tail -25
PD_FAIL=$(printf '%s\n' "$PD_OUT" | grep -c '^FAIL' || true)
PD_UN=$(printf '%s\n' "$PD_OUT" | grep -Ec '^⏳|UNMEASURED' || true)
log "postdeploy rc=$PD_RC FAIL=${PD_FAIL:-0} UNMEASURED=${PD_UN:-0}"
if [ "$PD_RC" != 0 ] || [ "${PD_FAIL:-0}" != 0 ]; then bad "postdeploy 有红项或没跑完（rc=$PD_RC）"; fi
if [ "${PD_UN:-0}" != 0 ]; then
  log "postdeploy 有 ${PD_UN} 项没测到 ⇒ 复跑一次再判（2 既不是通过也不是产品缺陷）"
  UNMEASURED=$((UNMEASURED+PD_UN))
fi

echo "-----------------------------------------"
if [ "$FAIL" = 0 ] && [ "${UNMEASURED:-0}" = 0 ]; then
  echo "RELEASE_H_ALL_OK=1"
  exit 0
fi
echo "RELEASE_H_DONE_WITH_ISSUES FAIL=$FAIL UNMEASURED=${UNMEASURED:-0}"
exit 1
