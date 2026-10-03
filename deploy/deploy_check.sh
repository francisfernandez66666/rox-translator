#!/usr/bin/env bash
# ============================================================================
# deploy/deploy_check.sh — 部署后验收一键检查（2026-08-26 评审整改配套）
# 用法：./deploy/deploy_check.sh <base_url> [ADMIN_TOKEN] [METRICS_TOKEN]
#        ./deploy/deploy_check.sh --systemd            （服务器 root 本地沙箱验收）
# 退出码 0 = 全部通过；任何一项失败即非 0，便于部署窗口快速定位。
# ============================================================================
set -uo pipefail

# --systemd 模式：技术债② sandbox 迁移验收（方案第二部分·四，须 root 在服务器本机执行）
if [ "${1:-}" = "--systemd" ]; then
  PASS=0; FAIL=0
  ok()  { echo "  ✔ $1"; PASS=$((PASS+1)); }
  bad() { echo "  ✖ $1"; FAIL=$((FAIL+1)); }
  echo "==> [S1] 进程归属（须 translator，不得 root）"
  PID=$(systemctl show -p MainPID --value translator 2>/dev/null)
  if [ -z "$PID" ] || [ "$PID" = "0" ]; then bad "translator 服务未运行"; else
    USER=$(ps -o user= -p "$PID" 2>/dev/null | tr -d ' ')
    [ "$USER" = "translator" ] && ok "进程用户=$USER (PID=$PID)" || bad "进程用户=$USER（期望 translator）"
  fi
  echo "==> [S2] 密钥隔离（unit 无明文密钥 + secrets.env 0600）"
  CAT=$(systemctl cat translator 2>/dev/null)
  echo "$CAT" | grep -Eq 'JWT_SECRET=[^$]|ADMIN_TOKEN=[^$]' && bad "unit/drop-in 含明文密钥" || ok "unit 无明文密钥"
  if [ -f /etc/translator/secrets.env ]; then
    PERM=$(stat -c %a /etc/translator/secrets.env 2>/dev/null)
    [ "$PERM" = "600" ] && ok "secrets.env 权限=$PERM" || bad "secrets.env 权限=$PERM（期望600）"
    echo "$CAT" | grep -q "EnvironmentFile=/etc/translator/secrets.env" && ok "EnvironmentFile 引用正确" || bad "缺少 EnvironmentFile 引用"
  else
    bad "/etc/translator/secrets.env 不存在"
  fi
  echo "==> [S3] 沙箱生效（NoNewPrivileges / ProtectSystem）"
  SHOW=$(systemctl show translator 2>/dev/null)
  echo "$SHOW" | grep -q "NoNewPrivileges=yes" && ok "NoNewPrivileges=yes" || bad "NoNewPrivileges 未启用"
  echo "$SHOW" | grep -q "ProtectSystem=full" && ok "ProtectSystem=full" || bad "ProtectSystem 未启用"
  echo "$SHOW" | grep -q "MemoryMax=" && ok "MemoryMax 已设（$(echo "$SHOW" | grep -o 'MemoryMax=[0-9]*)')" || bad "MemoryMax 未设"
  echo "==> [S4] 旧 drop-in 清理（concurrency/hardening/mail/mem/pdffont/secrets 不得残留）"
  LEFTOVER=$(ls /etc/systemd/system/translator.service.d/ 2>/dev/null | grep -E '^(concurrency|hardening|mail|mem|pdffont|secrets)\.conf$')
  if [ -z "$LEFTOVER" ]; then ok "旧 drop-in 已清理"; else bad "残留 drop-in: $LEFTOVER"; fi
  # ★ A9（R-1 修法 B 的现网负向锁，2026-10-04）：本次启动后「api key 无效 (401)」必须 0 条。
  #   判据全文与"为什么只能放在这里"写在 deploy/check_upstream_401.sh 文件头，本处只做接线与计数归并。
  echo "==> [S5] 本次启动后的上游 401（A9 负向锁）"
  HERE401=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
  if [ ! -f "$HERE401/check_upstream_401.sh" ]; then
    bad "缺少 $HERE401/check_upstream_401.sh ⇒ 本条没跑，不许当成通过"
  else
    OUT401=$(bash "$HERE401/check_upstream_401.sh" 2>&1); RC401=$?
    echo "$OUT401" | sed 's/^/     /'
    [ "$RC401" = "0" ] && ok "上游 401 计数=0" || bad "上游 401 判据未成立（见上面那行 FAIL 的原因）"
  fi
  echo ""
  [ "$FAIL" = "0" ] && echo "✅ systemd 沙箱验收全部通过（$PASS 项）" || { echo "❌ 通过 $PASS 项 / 失败 $FAIL 项"; exit 1; }
  exit 0
fi

BASE="${1:?用法: $0 <base_url> [ADMIN_TOKEN] [METRICS_TOKEN]}"
ADMTOK="${2:-}"
MTRTOK="${3:-}"
# 内网直连地址：默认生产 8787；本地冒烟时自动跟随 base_url 端口
LOCAL_BASE="http://127.0.0.1:8787"
case "$BASE" in
  *127.0.0.1:*|*localhost:*) LOCAL_BASE="$BASE" ;;
esac
PASS=0; FAIL=0
ok()   { echo "  ✔ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✖ $1"; FAIL=$((FAIL+1)); }
check(){ local desc="$1" want="$2" got="$3"; [ "$got" = "$want" ] && ok "$desc ($got)" || bad "$desc 期望$want 实际$got"; }

echo "==> [1/9] 基础探活"
check "/api/health"        200 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$BASE/api/health")"
check "/status"            200 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$BASE/status")"
# ★ #42（2026-09-22）探针拆分验收：/livez 只判进程存活（依赖抖动时也必须 200，否则编排器会去
#   重启本可降级自愈的实例）；/readyz 真探依赖（库不可达必须 503，让上游把这个实例摘掉，
#   否则多副本各算一份扣费）。
#   ★ 口径：两条走**内网直连**（LOCAL_BASE），不在 Caddy 里对公网开 handle——
#   探针虽只回状态词，但公网可达的「依赖健康」信号本身就是可用性情报；与 /metrics 内网抓取同口径。
#   因此从开发机远程跑本脚本时这两项记为跳过（不是失败），必须在服务器本机验收。
case "$BASE" in
  *127.0.0.1:*|*localhost:*) PROBE_ON_SERVER=1 ;;
  *) PROBE_ON_SERVER=0 ;;
esac
if [ "$PROBE_ON_SERVER" = "1" ]; then
  check "/livez(内网)"     200 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$LOCAL_BASE/livez")"
  check "/readyz(内网)"    200 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$LOCAL_BASE/readyz")"
  # 就绪探针只输出粗粒度状态词：DB 报错原文（内网主机/端口/文件路径）不得出现在响应里
  RZBODY=$(curl -s --max-time 8 "$LOCAL_BASE/readyz")
  if echo "$RZBODY" | grep -Eq 'postgres://|dial tcp|no such file|127\.0\.0\.1:5432'; then
    bad "/readyz 响应含内网拓扑原文（探针必须只回状态词）"
  else
    ok "/readyz 无拓扑泄露（$(echo "$RZBODY" | head -c 120)）"
  fi
else
  echo "  ↷ /livez /readyz 跳过（公网 base 不暴露探针，需服务器本机执行：curl 127.0.0.1:8787/readyz）"
fi

echo "==> [2/9] D1 metrics 收敛（公网响应体不得出现指标特征；SPA 兜底页/401 均视为安全）"
body=$(curl -s --max-time 8 "$BASE/metrics" | head -c 2000)
if echo "$body" | grep -q "translator_"; then
  bad "公网 /metrics 泄露指标特征"
else
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$BASE/metrics")
  ok "公网 /metrics 无指标泄露 (http=$code 兜底页或401)"
fi
if [ -n "$MTRTOK" ]; then
  check "/metrics(内网+token)" 200 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 -H "Authorization: Bearer $MTRTOK" "$LOCAL_BASE/metrics")"
fi

echo "==> [3/9] A2 插件 CORS（Origin 反射）"
hdr=$(curl -s -o /dev/null -D - --max-time 8 -H "Origin: https://example.com" "$BASE/openapi/v1/balance" | grep -i "^access-control-allow-origin:" | tr -d '\r' | awk '{print $2}')
[ "$hdr" = "https://example.com" ] && ok "ACAO 反射生效" || bad "ACAO 未反射（got: ${hdr:-空}）"

echo "==> [4/9] P0-2 支付回调三道闸"
pncode=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 -X POST "$BASE/api/pay/notify/mock" -d '{"order_no":"x","amount":1}')
case "$pncode" in
  403) ok "匿名回调 403（直连口径）" ;;
  400) ok "400=订单不存在（生产拓扑：Caddy 已注入凭证，渠道/金额闸门生效）" ;;
  *)   bad "匿名回调 $pncode 异常" ;;
esac

echo "==> [5/9] 注册→双桶余额→OpenAPI（A1 核心口径）"
EV=$(curl -s --max-time 8 "$BASE/api/auth/register-config" | python3 -c "import sys,json;print(json.load(sys.stdin).get('email_verify_enabled',False))" 2>/dev/null)
if [ "$EV" = "True" ] || [ "$EV" = "true" ]; then
  echo "  ↳ 生产已启用注册邮箱验证（防薅生效），第5项改为仅验证双桶出参通道开放性"
  bal=$(curl -s --max-time 8 "$BASE/openapi/v1/balance" -H "Authorization: Bearer invalid-probe")
  echo "$bal" | grep -q "invalid_api_key" && ok "balance 端点鉴权正常（跳过注册实测）" || bad "balance 端点异常"
  SKIP_REGISTER=1
fi
if [ "${SKIP_REGISTER:-0}" != "1" ]; then
IND=$(curl -s --max-time 8 "$BASE/api/register/industries" | python3 -c "import sys,json;print(json.load(sys.stdin)['industries'][0]['code'])" 2>/dev/null)
REG=$(curl -s --max-time 15 -X POST "$BASE/api/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"chk$(date +%s)\",\"password\":\"chk123456\",\"code\":\"chk$(date +%s)\",\"email\":\"chk$(date +%s)@t.com\",\"industry\":\"$IND\",\"role_choice\":\"admin\",\"agreed\":true}")
KEY=$(echo "$REG" | python3 -c "import sys,json;print(json.load(sys.stdin).get('api_key',''))" 2>/dev/null)
if [ -n "$KEY" ]; then
  bal=$(curl -s "$BASE/openapi/v1/balance" -H "Authorization: Bearer $KEY")
  # ★ 2026-09-19 积分口径：balance 出参为 balance_points/points_grants/points_permanent，零 token 裸值
  echo "$bal" | grep -qE '"[a-z_]*tokens"' && bad "balance 出参含 token 裸值（应纯积分口径）" || ok "balance 出参零 token 裸值"
  total=$(echo "$bal" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('balance_points',-1))" 2>/dev/null)
  grants=$(echo "$bal" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('points_grants',-1))" 2>/dev/null)
  perm=$(echo "$bal" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('points_permanent',-1))" 2>/dev/null)
  [ "${total:-0}" -gt 0 ] && [ "${grants:--1}" -ge 0 ] && [ "${perm:--1}" -ge 0 ] \
    && ok "双桶出参(积分) total=$total grants=$grants perm=$perm" || bad "双桶出参异常: $bal"
else
  bad "注册未返回 api_key"
fi
fi

echo "==> [6/9] 自助注销端点存在性（匿名 401 即可）"
check "/api/me/deactivate 匿名" 401 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 -X POST "$BASE/api/me/deactivate")"

echo "==> [7/9] 同步划译端点存在性（匿名 401 即可）"
check "/openapi/v1/translate 匿名" 401 "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 -X POST "$BASE/openapi/v1/translate" -d '{}')"

# == [8/9] G5（改造方案 §10-G5，2026-09-28）：派发状态词 + 最近一次回落计数 ==
# 只读、走内网直连（与 /livez 同口径）：状态词本身是可用性情报，不对公网摊开。
# 判据不钉死状态词取值——派发默认关闭（off）是正确态，开着时 online 才算生效；
# 真正要抓的是两件事：① 字段缺失（新二进制没上/没接线）② 状态词是 degraded（远端白配）。
# ⚠️ 与 /livez 同口径：从开发机远程跑记跳过不记失败，必须在服务器本机验收。
echo "==> [8/9] G5 远程派发状态（内网只读）"
if [ "$PROBE_ON_SERVER" = "1" ]; then
  HBODY=$(curl -s --max-time 8 "$LOCAL_BASE/api/health")
  DISP=$(echo "$HBODY" | python3 -c "import sys,json;print(json.load(sys.stdin).get('dispatch',''))" 2>/dev/null)
  if [ -z "$DISP" ]; then
    bad "/api/health 缺 dispatch 字段（派发未接线或二进制没换）"
  else
    case "$DISP" in
      off)     ok "dispatch=$DISP（默认关闭＝正确态：派发是增益不是依赖）" ;;
      online)  ok "dispatch=$DISP（派发生效中）" ;;
      degraded) bad "dispatch=$DISP（远端不可用，主站内存一分没省 ⇒ 按 §5.1/§5.2 排障，不要继续观察）" ;;
      *)       bad "dispatch=$DISP（非法状态词，只允许 off/online/degraded）" ;;
    esac
  fi
  # ★★ 2026-09-30 追加：派发第二节新增的三个健康面读数（到期日／内存帽／远端自检）一起验收。
  # 判据不是"有值就好"，而是**写读同源的一致性**——三档由 fileproc 的 DispatchStatusWords 翻出，
  # 与上面的状态词必须互相咬合：
  #   · 关着（off）⇒ 三档全空串（空＝"没配"，与"配了但坏了"的 unknown 是两件事，混了就排不动障）；
  #   · 生效中（online）⇒ 自检必须是 pass、内存帽不许 unknown、到期日不许空。
  # 为什么要拿 online 去反推自检：状态词为 online 的前提就是 probe 成功，而 probe 成功的前提
  # 是远端 selftest 返回 0（第二节立的深判据）。**如果健康面出现 online 而 selftest=unknown，
  # 只有一个解释**：出栈那三个键没接上、或接的是另一套默认值——现象正是"闸门看着绿、远端其实在降级"。
  # 键缺失单独报（旧二进制上 .get 会回 None，那会被下面的等值判据吞成"值不对"，指错方向）。
  DX_READ=$(echo "$HBODY" | python3 -c 'import sys,json
try:
    d = json.load(sys.stdin)
except Exception:
    print("__bad__|__bad__|__bad__")
    raise SystemExit(0)
out = []
for k in ("dispatch_expire", "dispatch_mem_cap", "dispatch_selftest"):
    v = d.get(k, "__missing__")
    out.append("" if v is None else str(v))
sys.stdout.write("|".join(out) + "\n")' 2>/dev/null)
  IFS='|' read -r DX_EXPIRE DX_MEMCAP DX_SELFTEST <<<"$DX_READ"
  case "$DX_EXPIRE|$DX_MEMCAP|$DX_SELFTEST" in
    *__missing__*) bad "健康面缺 dispatch_expire/dispatch_mem_cap/dispatch_selftest 三键之一 ⇒ 二进制没换或第二节出栈没接上" ;;
    __bad__*)      bad "健康面不是合法 JSON，派发三档读不出（先看服务是否起着）" ;;
    *)
      if [ "$DISP" = "off" ]; then
        if [ -z "$DX_EXPIRE$DX_MEMCAP$DX_SELFTEST" ]; then
          ok "派发关闭态三档全空（＝没配，与「配了但坏了」可区分）"
        else
          bad "dispatch=off 却带着读数 expire=$DX_EXPIRE mem_cap=$DX_MEMCAP selftest=$DX_SELFTEST ⇒ 三档取值链和状态词不同源（关着时不该拨远端）"
        fi
      else
        DXT_BAD=""
        [ -n "$DX_EXPIRE" ] || DXT_BAD="$DXT_BAD 到期日为空（取不到不等于没有到期）"
        case "$DX_MEMCAP" in on|clamped) ;; *) DXT_BAD="$DXT_BAD 内存帽=$DX_MEMCAP（应为 on/clamped）" ;; esac
        [ "$DX_SELFTEST" = "pass" ] || DXT_BAD="$DXT_BAD 自检=$DX_SELFTEST（状态词已是 $DISP，自检却不是 pass）"
        if [ -z "$DXT_BAD" ]; then
          ok "派发三档一致（expire=$DX_EXPIRE mem_cap=$DX_MEMCAP selftest=$DX_SELFTEST）"
        else
          bad "$DISP 态下派发三档不自洽：$DXT_BAD ⇒ 详见 fileproc_remote.go 的 DispatchStatusWords"
        fi
        if [ "$DX_MEMCAP" = "clamped" ]; then
          echo "  ↳ 内存帽 clamped＝配了值但被系统硬上限压住：要查体验机侧的 rlimit 配置（这一档不判失败，是情报）"
        fi
      fi
      ;;
  esac
  # 状态词只回三态词，不得夹带主机/路径等拓扑情报（同 /readyz 那条口径）
  if echo "$HBODY" | grep -Eq 'FILEPROC_DISPATCH_HOST|fpdispatch|/opt/'; then
    bad "/api/health 的 dispatch 段泄露远端拓扑（主机/路径）"
  else
    ok "dispatch 段无拓扑泄露"
  fi
  # 最近一次回落计数：只读 journal，不写不判阈值（阈值判读是运营口径，见 §5.4：24h 回落率 >30%）
  FALLBACK=$(journalctl -u translator --since '24 hours ago' --no-pager 2>/dev/null | grep -c '\[fpdispatch\] 派发失败 ⇒ 回落本地排队')
  echo "  ↳ 近 24h 派发回落计数 = ${FALLBACK}（>30% 或连续 5 单全回落 ⇒ 视为派发未生效，见 §5.4）"
else
  echo "  ↷ dispatch 项跳过（公网 base 不暴露，需服务器本机执行：curl 127.0.0.1:8787/api/health）"
fi

# == [9/9] PPROF 诊断面不得经反代可达（★ D-8，2026-09-29）==
# 背景：/debug/pprof/* 那条 mux 挂在**独立端口**（PPROF_ADDR，默认 127.0.0.1:18787），
# 二进制侧已有启动期闸门（非回环 + 无 PPROF_TOKEN 直接拒绝启动，见 cmd/server/pprof_guard.go）。
# 但**运维把 Caddy 反代指到那个端口**这一步不经过 Go 判定——单测看不见反代路由表，
# 这正是 F-73/F-74 藏了很久的同一层盲区，所以要在发版验收里补一条只读探针。
# ⚠️ 判据按 §一·6 的「兜底陷阱」写：spa.go 对不存在的路径回 index.html **且状态码仍是 200**，
#    所以只看 http_code 等于恒绿。真判据是响应体里不许出现 pprof 索引特征串。
#    取不到索引特征 ≠ 通道不通（可能只是路径不同），故同时钉两条正向对照（见下 PP_FIXTURE / 本机腿）。
# ★★ 特征串必须按**索引页实际字节**钉，不能凭直觉写（2026-09-29 发版当夜真踩，本机实测）：
#    Go 的 net/http/pprof 索引页里**没有**「goroutine profile」「Heap profile」「/debug/pprof/profile」
#    这些字样——它输出的是 `Types of profiles available:` 表格＋`full goroutine stack dump` 链接
#    （链接是相对路径 `href='profile?debug=1'`，所以带 `/debug/pprof/` 前缀的写法也命中不了）。
#    上一版三条串在真索引页上实测各命中 **0**，于是这条负向锁**永远抓不到东西**：
#    哪怕运维真把 Caddy 反代指到 18787，公网腿照样报「无 pprof 特征」绿灯＝对空气判负。
#    当时正是「对照腿：18787 有应答但无 pprof 索引」那句信息行把它喊出来的——
#    信息行只能提示，所以这里补一条 **fixture 正向对照**：不依赖任何宿主，先把正则本身钉死。
echo "==> [9/9] PPROF 诊断面可达性（公网侧只读）"
# 唯一一条特征串，负向腿与两条正向对照共用同一变量——防止判据与对照各写一份、迟早漂移。
PP_FEAT='Types of profiles available|full goroutine stack dump'
# fixture＝线上索引页的截断实拍（只留两条特征所在的结构，不掺业务内容）
PP_FIXTURE="<html><head><title>/debug/pprof/</title></head><body>/debug/pprof/<br>Types of profiles available:<table><thead><td>Count</td><td>Profile</td></thead><tr><td>27</td><td><a href='goroutine?debug=1'>goroutine</a></td></tr></table><a href=\"goroutine?debug=2\">full goroutine stack dump</a></body></html>"
if echo "$PP_FIXTURE" | grep -Eq "$PP_FEAT"; then
  echo "  ↳ 判据自证：特征串在 pprof 索引实拍样本上命中 ⇒ 下面那条负向不是空转"
else
  bad "pprof 特征串连索引页实拍样本都匹配不了（正则写错/被改坏）⇒ 公网那条负向锁作废，先去核对 Go pprof 索引页真实字节"
fi
# 链路探针先行（§一·6）：base 整个打不通时 curl 回空串，"无特征"会**结构性假绿**——
# 那种绿和"反代确实没摊开诊断面"是两件事，必须分开记账。
PP_LINK=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$BASE/api/health")
if [ "$PP_LINK" = "000" ] || [ -z "$PP_LINK" ]; then
  bad "pprof 腿无效：$BASE/api/health 不可达（$PP_LINK）⇒ 响应体为空当然'没有特征'，这条绿灯不许采信"
else
  PP_PUB=$(curl -s --max-time 8 "$BASE/debug/pprof/")
  if echo "$PP_PUB" | grep -Eq "$PP_FEAT"; then
    bad "公网 $BASE/debug/pprof 回吐了 pprof 索引 ⇒ 反代把诊断端口摊到公网了（进程内存/协程栈可被任何人拉走，profile 还能被打满 CPU）"
  else
    ok "公网 /debug/pprof 无 pprof 特征（链路存活 http=$PP_LINK，回环独占）"
  fi
fi
# 对照腿（仅服务器本机）：确认「没特征」是因为通道不通，而不是判据串写错
if [ "$PROBE_ON_SERVER" = "1" ]; then
  if ! command -v systemctl >/dev/null 2>&1; then
    echo "  ↷ systemctl 不存在（本机不是服务宿主）⇒ unit 读档跳过，**不代表 PPROF_ADDR 未设**"
  else
    # 读**展开后的** unit 现值（systemctl show -p Environment --value），不读 drop-in 原文：
    # 配置可能分散在多个 .conf 或含 ${...} 引用，按文件 grep 会读假空。
    PPROF_ENV=$(systemctl show translator -p Environment --value 2>/dev/null | tr ' ' '\n')
    PP_ADDR=$(echo "$PPROF_ENV" | sed -n 's/^PPROF_ADDR=//p')
    PP_TOKEN=$(echo "$PPROF_ENV" | sed -n 's/^PPROF_TOKEN=//p')
    case "$PP_ADDR" in
      ""|off) echo "  ↳ PPROF_ADDR=${PP_ADDR:-（unit 现值未设，走默认回环 127.0.0.1:18787）}" ;;
      127.0.0.1:*|\[::1\]:*|localhost:*) echo "  ↳ PPROF_ADDR=$PP_ADDR（回环＝正确态，外网打不到）" ;;
      *)
        if [ -z "$PP_TOKEN" ]; then
          bad "PPROF_ADDR=$PP_ADDR 非回环且无 PPROF_TOKEN ⇒ 诊断面对内网摊开（闸门本应拒绝启动，请核 unit 现值）"
        else
          echo "  ↳ PPROF_ADDR=$PP_ADDR（非回环，已配 PPROF_TOKEN＝受 pprofAuth 保护）"
        fi
        ;;
    esac
  fi
  # 对照腿（仅服务器本机）：确认「没特征」是因为通道不通，而不是判据串写错。
  # ⚠️ 端口集合从 **unit 现值**取（主站＋演示单元各自的 PPROF_ADDR），未配则用代码默认 18787；
  #    历史教训＝只探写死的 18787，而演示单元配的是 127.0.0.1:18788，于是对照腿永远在
  #    「有应答但无索引」上打转，把「判据串写错」误读成「该端口属于别的服务」。
  PP_PROBE_LIST="127.0.0.1:18787"
  for _u in translator translator-demo; do
    _a=$(systemctl show "$_u" -p Environment --value 2>/dev/null | tr ' ' '\n' | sed -n 's/^PPROF_ADDR=//p')
    case "$_a" in
      ""|off|*0.0.0.0*|*\[::\]*) : ;;
      127.0.0.1:*|localhost:*|\[::1\]:*) PP_PROBE_LIST="$PP_PROBE_LIST $_a" ;;
    esac
  done
  PP_CONTROL_SEEN=0
  for _pa in $PP_PROBE_LIST; do
    PP_LOCAL=$(curl -s --max-time 4 "http://$_pa/debug/pprof/" 2>/dev/null)
    [ -n "$PP_LOCAL" ] || continue
    PP_CONTROL_SEEN=1
    if echo "$PP_LOCAL" | grep -Eq "$PP_FEAT"; then
      echo "  ↳ 对照腿：本机 $_pa 能取到 pprof 索引 ⇒ 上面那条「无特征」判据测的是反代链，不是空转"
    else
      bad "本机 $_pa（unit 现值认定的诊断端口）有应答却无 pprof 索引 ⇒ 公网那条绿灯不可采信：要么诊断面没起来（排障能力为零），要么端口被别的服务占了"
    fi
  done
  [ "$PP_CONTROL_SEEN" = "1" ] || echo "  ↳ 诊断端口（18787 及 unit 里配的回环档）均未监听 ⇒ 诊断面关闭，公网那条锁无需对照"
else
  echo "  ↷ pprof 对照腿与 unit 读档跳过（需服务器本机执行）"
fi

echo ""
[ "$FAIL" = "0" ] && echo "✅ 验收全部通过（$PASS 项）" || { echo "❌ 通过 $PASS 项 / 失败 $FAIL 项"; exit 1; }