#!/usr/bin/env bash
# ============================================================================
# deploy/smoke_assist_llm.sh — 官网 AI 助手问答链线上冒烟（★ 2026-09-28 〇-Z #76）
#
# 为什么需要这条公网冒烟（assist_uat.sh 已经绿了还不够）：
#   #76 的现场是**用户截图**：访客在挂件里问「我为啥要买你的服务，不用deepl」，
#   助手回「这个问题我还没学到」。根因是两层缺口叠在一起——
#     ① 生产 assist 的 LLM 四项配置没落（env 注释着、configs 表空 ⇒ client.Enabled()==false），
#        所以所有回答都走 fallbackReply 的规则拼接；
#     ② 知识库压根没有「价值/竞品/比人工」这一族词条，规则拼接无米可炊 ⇒ 零命中 ⇒ 兜底文案。
#   ②已由 seed 补料 + golden_test.go 的 TestValueQuestionsAnsweredWithoutLLM 收口，但那条测试
#   跑在**临时库 + 内嵌 seed** 上，证不了三件只有线上才知道的事：
#     · 存量生产库不会因为 seed 更新而自己长出新词条（seed 只在首启空表灌库，必须
#       `python3 scripts/assist_kb_sync.py --host <服务器>` 推一次——AGENTS §三最后一条）；
#     · Caddy 的 `/assist-api/*` 转发段还在不在（这段被删掉时**界面照样渲染**：请求落进 SPA 兜底
#       拿回整页 index.html、状态码仍是 200，正是 AGENTS §6 点名的托管物/兜底陷阱）；
#     · LLM 到底接没接上（①那半边只能在生产配，配置走管理台/代理 API 热加载、不需要重启，
#       而「配了」与「生效」是两件事——本脚本按回答的 source 字段看实际走的是哪条路）。
#   故本脚本打真域名、真访客会话（greet 拿 sid+tok ⇒ chat），按「链路 + 内容形状」双判据出红绿。
#
# 判据：
#   0. 链路探针：`/assist-api/api/assist/greeting` 必须 200 + **响应体是 JSON**（不是 HTML 壳）
#      + `session`/`tok` 都取得到非空值。任一条不满足即整体红并退出——后面的问答判据在离线态
#      下会一路绿灯（assist_uat.sh 当年就是这么假绿到 W3 才暴露的，见 e2e/assist_widget_cache.spec.ts）。
#   1. 三条产品价值/竞品/比人工问句：回答**不得**是零命中兜底文案（「这个问题我还没学到」），
#      且必须是非空实义文本（≥20 字符）。这是 #76 的验收本体。
#      ⚠️ 字面量核对（`原版式交付`/`0.20–0.30`/`上传→拿成品`）**只在 source=fallback 时判**：
#        LLM 接上后回答是模型按知识改写的话术，钉字面量会把「已经修好」的线上判红——
#        那是判据错，不是产品错。fallback 这一支的字面量覆盖由本脚本 --selftest 的
#        mock 实例（无 LLM ⇒ 必然 fallback）离线钉住，两边都不留空档。
#   2. source 分布播报 + 可选硬门（EXPECT_LLM=1 时要求至少一条 source=llm）。
#      默认只播报：主页助手当前的口径是「有知识就答得出来」，LLM 是体验增强而非必要条件的。
#   3. 反向对照（RUN_NEG=1 才跑，★ 默认关，见下方 COST 注释）：一句无关乱码在 fallback 形态下
#      **必须**仍出兜底文案。
#      这一条堵的是「判据 1 恒绿」——兜底文案被改掉、或问答链路整体返空时，判据 1 的
#      「不含那句文案」会变成一句空话，有了这条反证才说明那句确实会出现在该出现的地方。
#      ⚠️ LLM 模式下这一条**自动降级为只播报**：engine.Respond 在 LLM 可用时把零命中交给模型，
#        不再吐那句文案（见 engine.go 的 `client.Enabled()` 分支），此时硬判会红在常态上。
#      ★ 闸门有效性不靠线上烧钱证明：这一条与判据 1 的字面量支都由 `--selftest` 的本地 mock
#        实例离线覆盖（无 LLM、零成本、可重复）。要看线上反证再手工 RUN_NEG=1 打开。
#
# ★ 副作用声明（别把它当纯只读脚本）：
#   greet 会在线上 assist 自有库建会话行，chat 会落 user/assistant 两条消息 ⇒ 管理台
#   「会话记录」会多出 N 条探针会话（N=问句数(+反向对照 1)）；判据 3 的乱码那句在 fallback
#   形态下还会登记进 `unanswered_questions`（运营补料清单）。这些**脚本一律不删**：
#   台账与补料清单是运营资产，冒烟没有资格 DELETE（同 assist_kb_sync.py 的「绝不 DELETE」口径）。
#   介意清单脏就 `RUN_NEG=0` 少一句；判据 1 那三条是验收本体，删不掉。
#
# ★ 密钥纪律（AGENTS 安全口径）：
#   本脚本**不读、不打印、不落盘**任何密钥。回答正文与错误体在打印前一律过 `redact()`
#   （把 `sk-…` 形态的串截成前 4 位 + [REDACTED]），防的是「上游把配置回显进错误信息」
#   这类二次泄漏；可选的 llm_mode 读数走环境变量 ASSIST_ADMIN_TOKEN 注入，只进请求头，
#   绝不 echo、绝不写文件。
#
# 用法：
#   bash deploy/smoke_assist_llm.sh                        # 默认打主站
#   BASE=https://rox-test.lexicorn.cn bash deploy/smoke_assist_llm.sh    # 打演示站
#   ASSIST_PREFIX= BASE=http://127.0.0.1:8793 bash deploy/smoke_assist_llm.sh   # 直连 assist（无 /assist-api 前缀）
#   EXPECT_LLM=1 bash deploy/smoke_assist_llm.sh           # 钉住「LLM 确实接上了」
#   RUN_NEG=0 bash deploy/smoke_assist_llm.sh              # 跳过乱码反向对照（不污染补料清单）
#   ASSIST_ADMIN_TOKEN=… bash deploy/smoke_assist_llm.sh   # 额外只读拉 llm_mode（只播报，不计分）
#   bash deploy/smoke_assist_llm.sh --selftest             # ★ 自检：绿态必须 0、红态必须非 0
#
# 退出码：0=全绿；1=有红项（红项逐条点名，不静默）。
# ============================================================================
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="${BASE:-https://langcross.lexicorn.cn}"
# ★ 用 `${VAR-default}` 而不是 `${VAR:-default}`：自检要传**空串**表达「直连 assist、没有前缀」，
#   而 `:-` 会把空串也当成未设置 ⇒ 吞回 /assist-api ⇒ 打谁都是 404。
#   09-28 自检首跑实测：用例 1 报「greeting HTTP 404」红在链路上，红的是夹具的传参方式，
#   而用例 2（本该红在"没接 LLM"）也 exit 1 —— 两条一起把"判据有效"演成了假象，
#   所以光看 exit 码不够，见下面 1c 那条「红项归属」核查。
ASSIST_PREFIX="${ASSIST_PREFIX-/assist-api}"      # 生产走 Caddy 前缀；直连 assist 时显式置空
EXPECT_LLM="${EXPECT_LLM:-0}"                    # =1 时要求至少一条回答来自 llm
RUN_NEG="${RUN_NEG:-0}"                          # 反向对照开关：默认关（打生产=白烧一次上游调用，见下方 COST 注释）
MAX_TIME="${MAX_TIME:-60}"                       # 单请求超时（LLM 生成比规则拼接慢得多，别用 15s）
MIN_REPLY="${MIN_REPLY:-20}"                     # 实义回答最短字符数（rune 口径）

# ★ 为什么默认 RUN_NEG=0（与文件头「默认开」相反，这里是本轮实测后改的口径）：
#   打生产 = 真调 LLM。SiliconFlow 那条线一次问答≈一次计费，而反向对照那句乱码**没有产品价值**，
#   纯属验闸门；把它默认打开等于每次冒烟白烧一次调用、还往运营补料清单塞垃圾行。
#   闸门有效性不靠线上烧钱证明：判据 1 的字面量支与判据 3 的兜底支都由 `--selftest` 的本地
#   mock 实例离线覆盖（无 LLM、零成本、可重复）。要看线上反证再手工 `RUN_NEG=1` 打开。
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fails=0
note() { printf '%s\n' "$*"; }
red()  { fails=$((fails + 1)); printf '  ✗ %s\n' "$*"; }
ok()   { printf '  ✓ %s\n' "$*"; }

# redact —— 任何要打印的响应体先过这一道：把 sk- 形态的串截成 4 位 + 标记。
# 防的不是我们自己（本脚本不发密钥），而是上游把配置/Authorization 回显进错误信息。
redact() {
  python3 -c '
import re, sys
t = sys.stdin.read()
sys.stdout.write(re.sub(r"(sk-|key[\"'"'"']?\s*[:=]\s*[\"'"'"']?)([A-Za-z0-9_\-]{4})[A-Za-z0-9_\-]{6,}",
                       r"\1\2[REDACTED]", t))
' 2>/dev/null || cat
}

# jget <文件> <键> —— 从 JSON 响应里取顶层键；非 JSON 回 __NOTJSON__，缺键回空。
jget() {
  python3 - "$1" "$2" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding='utf-8', errors='replace'))
except Exception:
    print('__NOTJSON__'); sys.exit(0)
if not isinstance(d, dict):
    print('__NOTJSON__'); sys.exit(0)
v = d.get(sys.argv[2])
if v is None:
    print('')
elif isinstance(v, (dict, list)):
    print(json.dumps(v, ensure_ascii=False))
else:
    print(v)
PY
}

# ask <请求体> <输出文件> —— 把 chat 响应写进文件，stdout 只回 HTTP 状态码（调用方负责打印前的 redact）
#   ★ 参数位序号写错过一次（`$3` 而调用只给两个参数）：set -u 下 curl 根本没跑，
#     ask 返回空串 ⇒ 三条问句全报「chat HTTP （期望 200）」，看着像链路红、其实是脚本自身。
ask() {
  curl -sS -m "$MAX_TIME" -o "$2" -w '%{http_code}' \
    -X POST "${BASE}${ASSIST_PREFIX}/api/assist/chat" \
    -H 'Content-Type: application/json' \
    --data "$1" || echo 000
}

# ===========================================================================
# --selftest：验「判据本身有效」——绿态必须 0 退出、红态必须非 0 退出。
#   用例 1（绿）：本地 mock assist 实例（内嵌 seed、无 LLM 配置）⇒ 三条价值问句必须答得出、
#                 且走 fallback 拿到字面量核对；反向对照必须出兜底文案。
#   用例 2（红）：同一实例钉 EXPECT_LLM=1 ⇒ 必须红（mock 没有 LLM ⇒ 抓「配了但没生效/压根没配」）。
#   用例 3（红）：BASE 指向静态夹具，/assist-api/* 落进 SPA 兜底回整页 HTML ⇒ 判据 0 必须红
#                 （AGENTS §6 的托管物陷阱：200 不等于链路通）。
# ===========================================================================
if [ "${1:-}" = "--selftest" ]; then
  st_fail=0
  st_case() { # $1=用例名 $2=期望 green|red $3..=环境变量赋值
    local name="$1" want="$2"; shift 2
    local out rc=0
    out="$(env "$@" bash "$REPO_ROOT/deploy/smoke_assist_llm.sh" 2>&1)" || rc=$?
    LAST_OUT="$out"
    if [ "$want" = green ] && [ "$rc" -eq 0 ]; then
      printf '  ✅ 自检 %s：绿态 exit 0\n' "$name"
    elif [ "$want" = red ] && [ "$rc" -ne 0 ]; then
      printf '  ✅ 自检 %s：红态 exit %s（预期非 0）\n' "$name" "$rc"
    else
      printf '  ❌ 自检 %s：期望 %s，实际 exit %s\n' "$name" "$want" "$rc"
      printf '%s\n' "$out" | sed 's/^/      | /' | redact
      st_fail=$((st_fail + 1))
    fi
  }

  # —— 用例 1/2：真 assist 服务（mock 模式 + 临时库 + 内嵌 seed）——
  BIN="$TMP/assist-selftest"
  if ! ( cd "$REPO_ROOT/backend-go" && go build -o "$BIN" ./cmd/assist-server ); then
    printf '  ❌ 自检：assist-server 编译失败 ⇒ 用例 1/2 无从跑起\n'
    st_fail=$((st_fail + 1))
  fi
  if [ -x "$BIN" ]; then
    PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
    # 后台起服**不用子壳 + exec**：子壳会吞掉真实 pid（$! 拿到的是子壳，见 09-27 踩坑）。
    # ASSIST_MOCK=1：无上游密钥时也不许外呼；内嵌 seed 随二进制走（改造 1A），故不投 ASSIST_SEED。
    ASSIST_MOCK=1 ASSIST_ADDR="127.0.0.1:${PORT}" ASSIST_DB="$TMP/assist.db" \
      ASSIST_ADMIN_TOKEN="selftest-only-not-a-real-token" \
      "$BIN" >"$TMP/assist.log" 2>&1 < /dev/null &
    SRV_PID=$!
    up=0
    for _ in $(seq 1 40); do
      if curl -sf -m 2 "http://127.0.0.1:$PORT/health" | grep -q '"ok":true'; then up=1; break; fi
      sleep 0.5
    done
    if [ "$up" != 1 ]; then
      printf '  ❌ 自检 用例 1：assist 实例未起来（port=%s pid=%s）\n' "$PORT" "$SRV_PID"
      tail -12 "$TMP/assist.log" | sed 's/^/      | /' | redact
      st_fail=$((st_fail + 1))
    else
      st_case "本地 mock 实例（内嵌 seed·无 LLM）三条价值问句都答得上＋乱码仍走兜底" green \
        "BASE=http://127.0.0.1:$PORT" "ASSIST_PREFIX=" "RUN_NEG=1" "EXPECT_LLM=0"
      # 字面量这一支必须真的被走到（否则判据 1 在 llm 形态下等于只剩「非空」一条弱锁），
      # 且**三条问句都要走到**：只答对一条也算绿的话，另外两条的词条缺口就永远看不见。
      n_ok1="$(printf '%s\n' "$LAST_OUT" | grep -c '✓ 1 问句' || true)"
      if [ "$n_ok1" = "3" ] && printf '%s\n' "$LAST_OUT" | grep -q '含 seed 实义内容'; then
        printf '  ✅ 自检 用例 1b：三条问句都走了 fallback 字面量核对（3/3）\n'
      else
        printf '  ❌ 自检 用例 1b：字面量核对只跑到 %s/3 条 ⇒ 绿灯可能是空转\n' "$n_ok1"
        st_fail=$((st_fail + 1))
      fi
      st_case "同一实例钉 EXPECT_LLM=1（mock 没接 LLM，必须红）" red \
        "BASE=http://127.0.0.1:$PORT" "ASSIST_PREFIX=" "RUN_NEG=0" "EXPECT_LLM=1"
      # ★ 红项归属核查（09-28 自检首跑踩实）：这一例当时也 exit 1，但红的是"链路 404"而不是
      #   判据 2 —— 只看 exit≠0 会把夹具自身的毛病演成"反证成立"。故钉死：必须恰好 1 处红、
      #   且那句红出自判据 2，同时链路探针（判据 0）必须是绿的。
      llm_reds="$(printf '%s\n' "$LAST_OUT" | grep -c '  ✗ ' || true)"
      if [ "$llm_reds" = "1" ] && printf '%s\n' "$LAST_OUT" | grep -q '✓ 0 链路通' \
         && printf '%s\n' "$LAST_OUT" | grep -q '✗ 2 '; then
        printf '  ✅ 自检 用例 2b：红项恰 1 处、出自判据 2 且链路是通的（不是 404 演出来的红）\n'
      else
        printf '  ❌ 自检 用例 2b：EXPECT_LLM 夹具产出 %s 处红项／链路探针未绿 ⇒ 判据归属存疑\n' "$llm_reds"
        st_fail=$((st_fail + 1))
      fi
    fi
    kill "$SRV_PID" 2>/dev/null || true; wait "$SRV_PID" 2>/dev/null || true
  fi

  # —— 用例 3：/assist-api 落进 SPA 兜底（整页 HTML、状态码仍 200）⇒ 判据 0 必须红 ——
  mkdir -p "$TMP/spa/assist-api/api/assist"
  python3 - "$TMP/spa" <<'PY'
import os, sys
d = sys.argv[1]
shell = ('<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"/><title>LangCross</title>'
         '</head><body><div id="root"></div></body></html>')
# 复刻 spa.go 的兜底形态：**任意路径都回整页 index.html 且状态码 200**。
# 这里直接把 greeting 落成一份 HTML 文件（http.server 对不存在的路径给 404，那是另一种红，
# 抓不到"200 但内容是壳"这一类最阴的形态——所以夹具必须造 200 + HTML）。
open(os.path.join(d, 'index.html'), 'w', encoding='utf-8').write(shell)
p = os.path.join(d, 'assist-api', 'api', 'assist')
os.makedirs(p, exist_ok=True)
open(os.path.join(p, 'greeting'), 'w', encoding='utf-8').write(shell)
PY
  ( cd "$TMP/spa" && exec python3 -m http.server 8799 --bind 127.0.0.1 >/dev/null 2>&1 ) &
  ST_PID=$!
  sleep 1
  st_case "assist 前缀被 SPA 兜底吃掉（回整页 HTML，判据 0 必须红）" red \
    "BASE=http://127.0.0.1:8799" "ASSIST_PREFIX=/assist-api" "RUN_NEG=0"
  kill "$ST_PID" 2>/dev/null || true; wait "$ST_PID" 2>/dev/null || true

  if [ "$st_fail" -ne 0 ]; then
    note "★ 自检失败 $st_fail 项 ⇒ 判据本身不可信，先修脚本再谈线上冒烟"
    exit 1
  fi
  note "自检全绿：3 例（绿 1：mock 实例三条价值问句都答得上、且 fallback 字面量核对确有跑；"
  note "        红 2：mock 实例上钉 EXPECT_LLM=1（抓「LLM 没配/没生效」）、/assist-api 被 SPA 兜底吃成 200+HTML（抓「200 不等于链路通」））都判对了。"
  note "        另有 2 条读数存在性核查（1b：三条问句的字面量核对都走到；2b：EXPECT_LLM 的红恰 1 处、"
  note "        出自判据 2 且链路探针是绿的——防止「链路 404 演出来的红」被当成反证成立）。"
  exit 0
fi

# ---------------------------------------------------------------------------
# 问句集：与 golden_test.go 的 TestValueQuestionsAnsweredWithoutLLM **同一批口径**
#   （问句字面 + 期望实义片段）。两边刻意重复而不共享常量：一边是本地内嵌 seed 的行为锁，
#   一边是线上真实库的接线锁，各自的 seed/词条演进节奏不同，绑死会让线上冒烟替单测背红。
#   格式 `问句|fallback 形态下必须出现的字面片段`
# ---------------------------------------------------------------------------
QUESTIONS="$TMP/questions"
cat > "$QUESTIONS" <<'QD'
我为啥要买你的服务，不用deepl|原版式交付
跟人工翻译比能省多少|0.20–0.30
为什么要用能言|上传→拿成品
QD

FALLBACK_TXT='这个问题我还没学到'
GREET="$TMP/greet.json"
CHAT="$TMP/chat.json"

note "AI 助手问答链冒烟：BASE=$BASE${ASSIST_PREFIX:+（前缀 $ASSIST_PREFIX）}"

# ---------------------------------------------------------------------------
# 0) 链路探针：greet 必须拿到 JSON + sid + tok
#    ⚠️ 只判 200 不算数——SPA 兜底会把「转发段没了」吃成 200 整页 HTML，
#       而挂件对着那种形态会照常渲染离线兜底 UI（AGENTS §6 实测陷阱）。
# ---------------------------------------------------------------------------
gcode="$(curl -sS --http1.1 -m "$MAX_TIME" -o "$GREET" -w '%{http_code}' \
  "${BASE}${ASSIST_PREFIX}/api/assist/greeting?page=/" || echo 000)"
if [ "$gcode" != "200" ]; then
  red "0 链路探针：greeting HTTP $gcode（期望 200）→ ${BASE}${ASSIST_PREFIX}/api/assist/greeting?page=/"
  note "  排查：① Caddy 的 /assist-api/* 转发段在不在 ② ai-assist.service 是否 active ③ 域名解析"
  note "★ 冒烟失败 $fails 项"; exit 1
fi
sid="$(jget "$GREET" session)"
tok="$(jget "$GREET" tok)"
if [ "$sid" = "__NOTJSON__" ] || head -c 400 "$GREET" | grep -q -i -E '<!doctype html|<html'; then
  red "0 链路探针：greeting 回的是 HTML 不是 JSON ⇒ /assist-api 落进了 SPA 兜底（转发段没生效，挂件那侧会显示离线兜底而非报错）"
  note "★ 冒烟失败 $fails 项"; exit 1
fi
if [ -z "$sid" ]; then
  red "0 链路探针：greeting JSON 里没有 session 字段 ⇒ 会话起不来，后面的问答无从跑起（响应体=$(head -c 160 "$GREET" | redact)）"
  note "★ 冒烟失败 $fails 项"; exit 1
fi
if [ -z "$tok" ]; then
  red "0 链路探针：greeting 没回能力令牌 tok（★ P0-1 之后 chat 必须带 tok，缺它每条问句都会 401）"
  note "★ 冒烟失败 $fails 项"; exit 1
fi
ok "0 链路通：greeting 200 + JSON + sid/tok 齐备"

# ---------------------------------------------------------------------------
# 1)+2) 三条价值问句：答得上来（非兜底文案、非空）；source 分布计数供判据 2 用
# ---------------------------------------------------------------------------
n_llm=0; n_fallback=0; n_other=0; n_red=0
while IFS='|' read -r q want; do
  [ -n "${q:-}" ] || continue
  body=$(printf '{"session":"%s","tok":"%s","message":"%s","page":"/"}' "$sid" "$tok" "$q")
  ccode="$(ask "$body" "$CHAT")"
  if [ "$ccode" != "200" ]; then
    red "1 问句「$q」chat HTTP $ccode（期望 200；401=session/tok 失效，5xx=assist 侧崩）"
    n_red=$((n_red + 1)); continue
  fi
  if [ "$(jget "$CHAT" reply)" = "__NOTJSON__" ]; then
    red "1 问句「$q」chat 回的也不是 JSON ⇒ 链路上被兜底/改道了（体首=$(head -c 120 "$CHAT" | redact)）"
    n_red=$((n_red + 1)); continue
  fi
  reply="$(jget "$CHAT" reply)"
  src="$(jget "$CHAT" source)"
  model="$(jget "$CHAT" model)"
  # 长度按 **rune** 数（中文一字一符；用字节数的话 MIN_REPLY=20 会在中文下轻松过关，判据就虚了）
  rlen="$(printf '%s' "$reply" | python3 -c "import sys;print(len(sys.stdin.buffer.read().decode('utf-8','replace')))")"
  case "$src" in
    llm) n_llm=$((n_llm + 1)) ;;
    fallback) n_fallback=$((n_fallback + 1)) ;;
    *) n_other=$((n_other + 1)) ;;
  esac
  # 判据 1a：不得是零命中兜底文案（这就是 #76 的本体：那句文案出现在访客屏幕上＝没答上）
  if printf '%s' "$reply" | grep -q -F "$FALLBACK_TXT"; then
    red "1 问句「$q」仍回零命中兜底 ⇒ 生产库里没有这一族词条（跑 python3 scripts/assist_kb_sync.py --host <服务器> 推 seed），或 LLM 把知识丢了"
    n_red=$((n_red + 1)); continue
  fi
  # 判据 1b：必须有实义长度（「命中了但拼空串」也是一种答不上）
  if [ "$rlen" -lt "$MIN_REPLY" ]; then
    red "1 问句「$q」回答只有 ${rlen} 字符（< ${MIN_REPLY}）⇒ 空/半截回答（内容=$(printf '%s' "$reply" | redact | head -c 120)）"
    n_red=$((n_red + 1)); continue
  fi
  # 判据 1c：fallback 形态下核 seed 实义片段；llm 形态下只播报（措辞由模型改写，钉字面量是判据错）
  if [ "$src" = "fallback" ]; then
    if printf '%s' "$reply" | grep -q -F "$want"; then
      ok "1 问句「$q」答得上（fallback，含 seed 实义内容「$want」，${rlen} 字）"
    else
      red "1 问句「$q」走 fallback 却没带词条实义片段「$want」⇒ 命中的不是这条知识（内容=$(printf '%s' "$reply" | redact | head -c 160)）"
      n_red=$((n_red + 1))
    fi
  else
    ok "1 问句「$q」答得上（source=${src:-?}${model:+, model=$model}，${rlen} 字；llm 形态不钉字面量）"
  fi
done < "$QUESTIONS"

# 判据 2：LLM 生效状态
note "2 回答来源分布：llm=$n_llm fallback=$n_fallback 其他=$n_other（红=$n_red）"
if [ "$EXPECT_LLM" = "1" ]; then
  if [ "$n_llm" -ge 1 ]; then
    ok "2 EXPECT_LLM=1 且确有 llm 来源的回答 ⇒ 上游接入生效"
  else
    red "2 钉了 EXPECT_LLM=1 却没拿到一条 llm 回答 ⇒ LLM 配置没落/没生效（env ASSIST_LLM_* 或管理台四项，配后热加载无需重启）"
  fi
else
  note "  （要硬钉「LLM 确实接上了」跑 EXPECT_LLM=1；默认只播报，规则拼接答得上也算合格）"
fi

# 可选：只读拉 llm_mode 徽标（凭据来自环境变量，只进请求头、绝不打印）
if [ -n "${ASSIST_ADMIN_TOKEN:-}" ]; then
  if curl -sS -m "$MAX_TIME" -o "$TMP/sess.json" "${BASE}${ASSIST_PREFIX}/api/assist/admin/sessions" \
       -H "X-Assist-Admin: ${ASSIST_ADMIN_TOKEN}"; then
    LM="$(jget "$TMP/sess.json" llm_mode)"
    if [ -z "$LM" ] || [ "$LM" = "__NOTJSON__" ]; then
      note "  llm_mode 未取到（目标不是 assist 本体／Token 不对／该面被主站鉴权挡住——本条不计分）"
    else
      note "  llm_mode（管理台徽标，只播报）=$LM"
    fi
  else
    note "  llm_mode 拉取失败（管理面不可达；本条不计分）"
  fi
fi

# ---------------------------------------------------------------------------
# 3) 反向对照：乱码问句在 fallback 形态下必须仍出兜底文案（堵「判据 1 恒绿」）
#    ⚠️ 默认**关**（见文件头 COST 注释）：RUN_NEG=1 手工打开；线上若是 llm 形态则自动降级为只播报。
# ---------------------------------------------------------------------------
if [ "$RUN_NEG" != "1" ]; then
  note "3 反向对照已跳过（RUN_NEG=0 默认：乱码那句无产品价值、打生产就是白烧一次上游调用，还往补料清单塞垃圾行）"
else
  nbody=$(printf '{"session":"%s","tok":"%s","message":"%s","page":"/"}' "$sid" "$tok" "xyzzy量子波动速翻布拉布拉")
  ncode="$(ask "$nbody" "$TMP/neg.json")"
  nreply="$(jget "$TMP/neg.json" reply)"
  nsrc="$(jget "$TMP/neg.json" source)"
  if [ "$ncode" != "200" ]; then
    red "3 反向对照 chat HTTP $ncode（期望 200）"
  elif [ "$nsrc" = "llm" ]; then
    note "3 反向对照只播报：线上走 llm（source=llm），零命中由模型自由回答，兜底文案那条不适用"
    note "  （该问句回答=${nreply:0:80}）"
  elif printf '%s' "$nreply" | grep -q -F "$FALLBACK_TXT"; then
    ok "3 反向对照：无关乱码仍走零命中兜底 ⇒ 判据 1 那句「不得出现兜底文案」是有对照的"
  else
    red "3 反向对照失败：乱码问句也没出兜底文案 ⇒ 问答链整体返空或文案已改名，判据 1 会恒绿（内容=$(printf '%s' "$nreply" | redact | head -c 120)）"
  fi
fi

if [ "$fails" -ne 0 ]; then
  note "★ AI 助手问答链冒烟失败 $fails 项（判据 0/1/2/3 见文件头）"
  note "  排查顺序：① ai-assist.service 是否 active、Caddy /assist-api 段在不在（判据 0 的红都在这两层）"
  note "  ② 生产 assist 库有没有这一族词条——seed 只首启灌库，改 seed 必须跑 scripts/assist_kb_sync.py --host <服务器>"
  note "  ③ LLM 四项配置（env ASSIST_LLM_* 优先且不被管理台覆盖；配置热加载，不用重启）"
  note "  ⚠️ 本脚本不打印任何密钥；排障时也别把 configs 表的原文贴进聊天/文档/git。"
  exit 1
fi
note "AI 助手问答链冒烟全绿：访客问产品价值/竞品/比人工都能答得上，链路是真接口不是兜底。"
