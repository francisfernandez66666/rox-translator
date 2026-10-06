#!/usr/bin/env bash
# ============================================================================
# scripts/uat/assist_uat.sh — 主站 AI 助手（assist）自动化 UAT
# 覆盖（2026-09-16 R0 批次 + 既有链路回归 + 2026-09-17 改造 1A 融合项）：
#   C 端：greeting/chips、话术直配(rule)、流程触发与推进(flow)、知识兜底(fallback)、
#         history 回显、features、超长截断、缺参 400、错方法 405
#   R0.1 同义词归一：口语「怎么充钱」命中充值知识（原三层脱靶场景）
#   R0.2 兜底改造：零命中不再空承诺 + 未答问题登记（管理台清单可见）
#   R0.4 管理台：config key 白名单闸、api_key 掩码回显与回写 skip、
#         LLM 配置热加载（llm_mode rule→db）、测试连通端点（不可达可读报错）
#   管理端：401 鉴权、四表 CRUD、sessions 统计载荷
#   ★ 改造 1A（2026-09-17）：内嵌 seed 生效（不依赖外置文件）、内嵌管理页可达、
#         管理台 Token 主库桥接（MAIN_DB → system_config.assist_admin_token）与 env 优先级契约
#   ★ 〇-LK（2026-09-22）G 段：管理 Token 保存即生效（旧值即刻失效）、该键掩码与空值拒绝、
#         同会话重复 greet 不再堆重复欢迎语、换 Token 重启后老访客 tok 仍可用（sess_key 已持久化）
#   ★ 093x（2026-09-30）W 段：真机挂件复问的四条现网漏点（日文正文嵌中文词形／模型自算乘法总额／
#         品牌名翻成「能与」／括号旁白与裸方括号）——用假上游回放逐字原文，
#         从 /api/assist/chat 这条 HTTP 面证明出站四道守卫**接在链上**（不只是单测绿）
#   ★ 0AR 第 4 波（2026-10-06）X 段：canned 出栈闸门／冷语种退避／按钮名本地化／编造承诺守卫／
#         读侧二次闸门／启动期旧代孤儿行清理——同一手法（档位假上游 :8798 + 现读临时库 + /health canned 段），
#         把"界面正常、只是慢/只是中文/只是三个中文按钮"这一族静默形态在 HTTP 面点名
# 依赖：无（自起 assist mock 模式，临时 SQLite，端口默认 8793/8794；W 段另起假上游 8796；
#         X 段再起档位假上游 8798（跟随 ASSIST_UAT_MOCK_PORT）与清理验证实例 8795（ASSIST_UAT_PORT3））
# 用法：bash scripts/uat/assist_uat.sh
# ============================================================================
set -u
cd "$(dirname "$0")/../.." || exit 1

PORT="${ASSIST_UAT_PORT:-8793}"
PORT2="${ASSIST_UAT_PORT2:-8794}"
B="http://127.0.0.1:${PORT}"
B2="http://127.0.0.1:${PORT2}"
J='Content-Type: application/json'
TOK="uat-assist-$(date +%s)"
WORK=$(mktemp -d)
BIN="$WORK/assist-server"
PASS=0; FAIL=0; START=$(date +%s)

ck(){ if echo "$3" | grep -qE "$2"; then PASS=$((PASS+1)); echo "PASS|$1"; else FAIL=$((FAIL+1)); echo "FAIL|$1|want[$2]|got[${3:0:200}]"; fi; }

log(){ echo "[assist_uat] $*"; }

# ---------- 0. 构建（★ 改造 1A：源码已并入主 module，入口改为 cmd/assist-server）----------
log "构建 assist-server（主仓单 module）..."
(cd backend-go && go build -o "$BIN" ./cmd/assist-server) || { echo "构建失败"; exit 1; }

# ---------- 1. 启动（mock 规则模式 + 全新临时库；★ 不配 ASSIST_SEED/ASSIST_WEB）----------
# ★ 改造 1A：显式不传 ASSIST_SEED / ASSIST_WEB —— 验证 seed 与管理页均为二进制内嵌，
#   部署不再需要投放 web/ 与 seed/ 目录；内嵌若失效，下方 A1/A2/E1/E2 会直接红。
log "启动 assist :${PORT}（mock 规则模式，内嵌 seed + 内嵌管理页）..."
ASSIST_MOCK=1 ASSIST_ADMIN_TOKEN="$TOK" ASSIST_ADDR="127.0.0.1:${PORT}" \
  ASSIST_DB="$WORK/assist.db" \
  nohup "$BIN" > "$WORK/assist.log" 2>&1 < /dev/null &
PID=$!
OK=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$B/health" | grep -q '"ok":true'; then OK=1; break; fi
done
[ "${OK:-0}" = "1" ] || { echo "assist 启动失败"; tail -5 "$WORK/assist.log"; kill $PID 2>/dev/null; exit 1; }
log "就绪（${i}s）"

AH="X-Assist-Admin: $TOK"

# ---------- 2. C 端基础链路 ----------
R=$(curl -s "$B/api/assist/greeting?page=/")
ck A1-greeting '"greeting"' "$R"
ck A1-chips '积分怎么收费' "$R"
SID=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
# ★ P0-1（2026-09-18）：greeting 同时下发会话能力令牌 tok，chat/history 必须随带
TOK=$(echo "$R" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
if [ -z "$TOK" ]; then echo "FAIL|A1-tok-missing"; FAIL=$((FAIL+1)); fi

R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID\",\"tok\":\"$TOK\",\"message\":\"怎么收费？价格多少\",\"page\":\"/\"}")
ck A2-chat-rule '"source":"rule"' "$R"

curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID\",\"tok\":\"$TOK\",\"message\":\"我是新手不会用\"}" >/dev/null
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID\",\"tok\":\"$TOK\",\"message\":\"个人版\"}")
ck A3-flow-advance '"source":"flow"' "$R"

R=$(curl -s "$B/api/assist/history?session=$SID&tok=$TOK&limit=20")
ck A4-history 'assistant' "$R"
ck A4-history-user 'user' "$R"
# ★ P0-1 反向断言：无/伪令牌读历史必须 401
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/history?session=$SID")
ck A4-history-no-tok-401 '^401$' "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/history?session=$SID&tok=deadbeef")
ck A4-history-bad-tok-401 '^401$' "$C"

R=$(curl -s "$B/api/assist/features")
ck A5-features '"features"' "$R"

R=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/chat" -H "$J" -d '{"session":"","message":"x"}')
ck A6-chat-400 '^400$' "$R"
R=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$B/api/assist/chat")
ck A6-chat-405 '^405$' "$R"

# ---------- 2b. ★ 2026-09-20 复合意图让位（生产漏接修复：「印度语能翻译吗，一个字多少钱」
# 旧逻辑被价格话术直配抢答，语言侧信息全程不参与；现话术降为素材融合应答）----------
RC=$(curl -s "$B/api/assist/greeting?page=/")
SID_CI=$(echo "$RC" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TOK_CI=$(echo "$RC" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID_CI\",\"tok\":\"$TOK_CI\",\"message\":\"印度语能翻译吗，一个字多少钱\",\"page\":\"/\"}")
ck CI1-not-price-only '"source":"(llm|fallback)"' "$R"
ck CI1-lang-info '印地语|印度语' "$R"
ck CI1-price-info '积分|预充值' "$R"
# 纯价格问句仍走毫秒级话术直配（让位逻辑不得误伤单意图快答）
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID_CI\",\"tok\":\"$TOK_CI\",\"message\":\"多少钱\",\"page\":\"/\"}")
ck CI2-pure-price-rule '"source":"rule"' "$R"

# ---------- 2c. ★ 082x（2026-09-29）非中文访客接线：挂件随请求送 lang，出站必须带语言标记 ----------
# 本段是**契约级**断言（矩阵跑在 mock 规则模式、没有真 LLM），锁的是三件事：
#   ① 带 lang 的 greet/chat 不许打错（新增的按需翻译层在 LLM 缺失时必须软回落，不能 500）；
#   ② /chat 响应里 lang_localized 字段必须在（漏了它，「模型按语言答对了」和「全靠补翻兜着」在界面上长一样）；
#   ③ 取不到译文时**原样出中文**且回复非空——空回复比中文回复更接近事故（见 localize.go 文件头）。
# 真·翻译行为（英文访客拿到英文）由 backend-go/internal/assist/engine 的假上游单测锁：
# reply_lang_check_test.go（含「合格回答零额外调用」的反证腿），矩阵这里不重复起真模型。
RG=$(curl -s "$B/api/assist/greeting?page=/&lang=en")
ck LG1-greet-en-session '"session"' "$RG"
ck LG1-greet-en-not-empty '"greeting":"[^"]' "$RG"
RL=$(echo "$RG" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TL=$(echo "$RG" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$RL\",\"tok\":\"$TL\",\"message\":\"how much does one word cost\",\"lang\":\"en\",\"page\":\"/\"}")
ck LG2-has-lang-field '"lang_localized":(true|false)' "$R"
ck LG2-reply-not-empty '"reply":"[^"]' "$R"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$RL\",\"tok\":\"$TL\",\"message\":\"how much\",\"lang\":\"en\"}")
ck LG3-en-chat-200 '^200$' "$C"

# ---------- 2d. ★ 082x 第九条（2026-09-29 用户实测「中文前台+英文问题，回复的还是中文」）----------
# 用户定稿口径：**打开词按前台语言，后续回复按访客这句话的语言**。
# 本段（mock 规则模式、无真 LLM）锁的是接管**是否发生**——用 source 判，因为它不会被兜底文案骗过：
#   中文话术直配 = 没接管；让位给模型后掉到知识兜底 = 接管发生了。
# 为什么不在这里断言"英文回复"：真翻译行为由 engine 的假上游单测锁
# （input_lang_test.go 的 TestWelcomeStaysOnUiLangWhileReplyFollowsInput 抓的是真发出去的补翻请求体），
# 矩阵没有真模型，硬断言非中文只会得到一条恒红的假判据。
# 三条腿缺一条都不算锁住：只留①的话，把接管写死成「一律非中文」也能绿。
newgreet(){ curl -s "$B/api/assist/greeting?page=/&lang=$1"; }
sidof(){ echo "$1" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])'; }
tokof(){ echo "$1" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])'; }

# ① 中文界面 + 英文提问（句里带话术关键词「价格」）：中文话术必须**让位**（接管前这里是 rule＝缺陷本体）
RZ=$(newgreet zh); SZ=$(sidof "$RZ"); TZ=$(tokof "$RZ")
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SZ\",\"tok\":\"$TZ\",\"message\":\"what is the 价格 like\",\"lang\":\"zh\",\"page\":\"/\"}")
ck LG4-zhui-enq-yields '"source":"fallback"' "$R"
ck LG4-zhui-enq-not-empty '"reply":"[^"]' "$R"
# ② 同一界面 + 纯中文提问：毫秒级话术直配不许被撤（把它一起让位＝把主路径打回慢路，是回退不是修复）
RZ2=$(newgreet zh); SZ2=$(sidof "$RZ2"); TZ2=$(tokof "$RZ2")
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SZ2\",\"tok\":\"$TZ2\",\"message\":\"多少钱\",\"lang\":\"zh\",\"page\":\"/\"}")
ck LG5-zhui-zhq-rule '"source":"rule"' "$R"
# ③ 英文界面 + 中文提问：★ 反向接管（09-29 用户定稿「用什么语言问就用什么语言答」）⇒ 中文话术直出
RZ3=$(newgreet en); SZ3=$(sidof "$RZ3"); TZ3=$(tokof "$RZ3")
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SZ3\",\"tok\":\"$TZ3\",\"message\":\"多少钱\",\"lang\":\"en\",\"page\":\"/\"}")
ck LG6-enui-zhq-rule '"source":"rule"' "$R"
# ④ 打开词那半边：中文界面的 greet 必须还是中文（接管只作用于对话，不许把 greet 也拖进去——
#    它没有"访客输入"可比，且缓存键按界面语言落库，跟着输入走会让运营在管理台改的那句永远读不到）。
HAS_CJK=$(curl -s "$B/api/assist/greeting?page=/&lang=zh" | python3 -c 'import sys,json,re;g=json.load(sys.stdin).get("greeting","");print("CJK" if re.search(r"[一-鿿]",g) else "NONE")')
ck LG7-greet-zh-stays-chinese '^CJK$' "$HAS_CJK"

# ---------- 3. R0.1 同义词归一：怎么充钱 ----------
# ★ 修复（2026-09-18 闸门回归）：B1/B2 各用全新会话，不再复用 $SID——
#   复用会让 B1「怎么充钱」进入的 recharge-guide 流程把 B2 的无意义输入当作流程答案吞掉，
#   兜底话术与未答登记都不再发生（假失败）。tok 是与 sid 绑定的 HMAC 能力令牌，
#   新会话必须重新 greeting 取 sid+tok，不能自造 sid。
RB=$(curl -s "$B/api/assist/greeting?page=/")
SID_SYN=$(echo "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TOK_SYN=$(echo "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID_SYN\",\"tok\":\"$TOK_SYN\",\"message\":\"怎么充钱\",\"page\":\"/\"}")
ck B1-synonym-recharge '充值|余额|套餐|积分' "$R"
ck B1-synonym-not-fallback-empty '"source":"' "$R"
if echo "$R" | grep -qE '这个问题我记下了|这个问题我还没学到'; then
  FAIL=$((FAIL+1)); echo "FAIL|B1-synonym-hit|got fallback text"
else
  PASS=$((PASS+1)); echo "PASS|B1-synonym-hit"
fi

# ---------- 4. R0.2 兜底改造 + 未答登记 ----------
RB=$(curl -s "$B/api/assist/greeting?page=/")
SID_UN=$(echo "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TOK_UN=$(echo "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
R=$(curl -s "$B/api/assist/chat" -H "$J" -d "{\"session\":\"$SID_UN\",\"tok\":\"$TOK_UN\",\"message\":\" xyzzy量子波动速翻布拉布拉 \"}")
ck B2-fallback-guide '先记下来|换个说法|入口' "$R"
ck B2-fallback-actions '"actions":\[{' "$R"
R=$(curl -s "$B/api/assist/admin/sessions" -H "$AH")
ck B2-unanswered 'xyzzy量子波动速翻布拉布拉' "$R"

# ---------- 5. R0.4 管理台：白名单 / 掩码 / 热加载 / 测试连通 ----------
# 5a. 白名单闸：未登记 key 拒绝
C=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"arbitrary_hack","value":"x"}')
ck C1-whitelist-400 '^400$' "$C"
# 5b. LLM 键可写 + 明文不回显（掩码形态）+ 掩码回写 skip
C=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"llm_api_key","value":"sk-abcdef123456"}')
ck C2-llmkey-write '^200$' "$C"
R=$(curl -s "$B/api/assist/admin/config" -H "$AH")
if echo "$R" | grep -q 'sk-abcdef123456'; then
  FAIL=$((FAIL+1)); echo "FAIL|C3-mask-no-leak|明文泄露"
else
  PASS=$((PASS+1)); echo "PASS|C3-mask-no-leak"
fi
ck C4-mask-shape '\*\*\*' "$R"
R=$(curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"llm_api_key","value":"sk-***56"}')
ck C5-masked-write-skip '"skipped":true' "$R"
# 5c. 热加载：写 base_url/model 后 llm_mode 翻转为 db（mock 下 client 由 configs 构建）
curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"llm_base_url","value":"http://127.0.0.1:9/v1"}' >/dev/null
curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d '{"key":"llm_model","value":"uat-model"}' >/dev/null
R=$(curl -s "$B/api/assist/admin/sessions" -H "$AH")
ck C6-hotreload-llmmode '"llm_mode":"db"' "$R"
# 5d. 测试连通：不可达端点返回可读错误而非挂死
R=$(curl -s -m 15 -X POST "$B/api/assist/admin/llm/test" -H "$AH")
ck C7-llmtest 'ok":false|error' "$R"
# 5e. 管理端 401
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/admin/kb")
ck C8-admin-401 '^401$' "$C"

# ---------- 5f. ★ 093x（2026-09-30）真机挂件复问四条漏点：出站守卫链的**HTTP 面接线证明** ----------
# 单测钉的是函数（respond_outbound_test.go / han_residue_reply_test.go / reply_lang_check_test.go），
# 这一段钉的是「从 /api/assist/chat 打进去真的走到那四道守卫」——本仓出过"机制在、入站没接线"
# 那一类形态（082x 第九条：挂件没把 lang 送进来，补翻整段轮空），只有真发一次 HTTP 请求才算锁住。
# 假上游 scripts/uat/mock_assist.py 回放的是 2026-09-30 从现网拿回的**逐字原文**
# （同一形态另钉一份在 engine/han_residue_reply_test.go 的 leakReply093x，两处改动要同步想）。
# 三条纪律：
#   ① 每条负向判据都配正向对照（合法正文必须还在）——否则"整段删空"也能绿灯；
#   ② 上游调用次数从桩的 /uat/stats 读，「生成 1 次＋补翻 1 次」是硬账，多打一次就是反复重写；
#   ③ 段落结束把 llm_base_url 写回不可达端点，别让后面的段落意外依赖这个桩。
MOCK_PORT="${ASSIST_UAT_MOCK_PORT:-8796}"
MOCKB="http://127.0.0.1:${MOCK_PORT}"
# 就绪探针认「本次运行的标识」，不认"有没有人应答"：端口被上一轮残留的假上游占着时，
# 新起的桩 bind 失败即退出，curl 却照样回 200 ——那是上一轮的桩、上一轮的 gen/rewrite 计数，
# W7 那条次数硬账会跟着失真。标识不匹配就是"这不是我起的"，直接点名端口被占。
MOCK_RUN="uat093x-$$-$(date +%s)"
nohup python3 scripts/uat/mock_assist.py "$MOCK_PORT" "$MOCK_RUN" > "$WORK/mockassist.log" 2>&1 < /dev/null &
MOCK_PID=$!
OKM=0
for i in $(seq 1 10); do
  sleep 1
  # 冒号后的空格可有可无（`: *true`）：W0 首跑栽在 Python 默认 json.dumps 回 `"ok": true`
  # 而判据按后端 Go 的字面量写 `"ok":true`，桩明明活着却判"起不来"、整段 W 一条没跑。
  if curl -s -m 2 "$MOCKB/uat/stats" | grep -qE "\"ok\": *true.*\"run\": *\"${MOCK_RUN}\""; then OKM=1; break; fi
done
if [ "${OKM:-0}" != "1" ]; then
  FAIL=$((FAIL+1))
  if grep -qiE "address already in use" "$WORK/mockassist.log" 2>/dev/null; then
    echo "FAIL|W0-mock-llm-start|假上游端口 ${MOCK_PORT} 被上一轮残留的桩占着（kill 掉它或 ASSIST_UAT_MOCK_PORT 换端口）；后面 W 段全部无效"
  else
    echo "FAIL|W0-mock-llm-start|假上游起不来（后面 W 段全部无效）：$(tail -3 "$WORK/mockassist.log" 2>/dev/null | tr '\n' ' ')"
  fi
else
  log "假上游 :${MOCK_PORT} 就绪（${i}s）"
  curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" \
    -d "{\"key\":\"llm_base_url\",\"value\":\"${MOCKB}/v1\"}" >/dev/null
  curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" \
    -d '{"key":"llm_model","value":"uat-assist-model"}' >/dev/null

  RW=$(newgreet ja); SW=$(sidof "$RW"); TW=$(tokof "$RW")
  R=$(curl -s -m 30 "$B/api/assist/chat" -H "$J" \
    -d "{\"session\":\"$SW\",\"tok\":\"$TW\",\"message\":\"ドキュメント翻訳の料金はどのくらいですか\",\"lang\":\"ja\",\"page\":\"/\"}")
  # ② 接线前提：这一条真的是模型答的（rule/flow/fallback 是人写的文案，四道守卫按设计只管 llm 那一路；
  #    落到 rule 就说明请求没进模型，后面所有判据都成了对着人写文案的空扫）
  ck W1-source-llm '"source":"llm"' "$R"
  # ① 正向对照：日文正文里合法的那两句必须原样留着（守卫把正文吃空＝下面所有负向判据假绿）
  ck W2-keep-body '原文を送信いただければ' "$R"
  ck W2b-keep-quant '1,000文字' "$R"
  # 红腿①（判残补翻）：五处中文词形／简体字形／被劈开的假名＋英文，出栈时一个都不许在
  if echo "$R" | grep -qE '选択|系数|扣费|入力语言|プロfessional'; then
    FAIL=$((FAIL+1)); echo "FAIL|W3-no-cn-wordform|现网五处词形漏出出栈正文：${R:0:200}"
  else
    PASS=$((PASS+1)); echo "PASS|W3-no-cn-wordform"
  fi
  # 红腿②（报价守卫）：模型自算的乘法算式与复算不出的总额不许发出去
  if echo "$R" | grep -qE '2000×400|400\+7\.5'; then
    FAIL=$((FAIL+1)); echo "FAIL|W4-no-arithmetic|算式/自算总额漏出出栈正文：${R:0:200}"
  else
    PASS=$((PASS+1)); echo "PASS|W4-no-arithmetic"
  fi
  # 红腿③（品牌归一）：日文轮的品牌错形「能与」不许出栈，正确写法「能言」必须在
  if echo "$R" | grep -q '能与'; then
    FAIL=$((FAIL+1)); echo "FAIL|W5-brand-misform|品牌错形漏出出栈正文：${R:0:200}"
  else
    PASS=$((PASS+1)); echo "PASS|W5-brand-misform"
  fi
  ck W5b-brand-correct '能言' "$R"
  # #21（括号旁白与裸方括号）：交代"我用什么语言答"的那段括号备注与半截 markdown 链接语法都不许留给客户
  if echo "$R" | grep -qE 'で回答するため|\[ pricing'; then
    FAIL=$((FAIL+1)); echo "FAIL|W6-no-aside|旁白括号/裸方括号漏出出栈正文：${R:0:200}"
  else
    PASS=$((PASS+1)); echo "PASS|W6-no-aside"
  fi
  # ② 次数硬账：生成 1 次＋补翻 1 次；canned 那一路另算（欢迎词本地化），不参与本判据
  ST=$(curl -s "$MOCKB/uat/stats")
  GEN=$(echo "$ST" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("gen",0))')
  RW2=$(echo "$ST" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("rewrite",0))')
  if [ "$GEN" = "1" ] && [ "$RW2" = "1" ]; then
    PASS=$((PASS+1)); echo "PASS|W7-upstream-twice(gen=$GEN,rewrite=$RW2)"
  else
    FAIL=$((FAIL+1)); echo "FAIL|W7-upstream-twice|want gen=1 rewrite=1 got $ST"
  fi
  # ★★ 094x（2026-09-30 换件当天现网复问又抓到 3 条红）W9：补翻被拒之后的**确定性正字表**那一腿。
  # 现场不是"判残没抓到"，是"抓到了、补翻却被三条硬判据拒用"，而旧日志没有原因字段，
  # 分不清是上游抖动还是模型改不动。这一条把补翻这条路**真的堵死**：
  # 第二台桩（mode=echo）在补翻那一枪原样吐回上一稿 ⇒ 残片一个没少 ⇒ 必然 reject(not_improved)，
  # 于是出栈正文里那些词形只能由本地正字表改写——**换个说法：这一条红＝二线防线没接上**。
  # 判据两侧同口径（负向必配正向，否则"正文被吃空"也能绿）：
  #   · 表内词形必须已经换成日文正字（選択／係数／費用／なぜ），且数字档「1,000文字」还留着（前置②的实面）；
  #   · 表**外**那种要挑说法的形态必须原样留着（扣费／プロfessional 至少一个还在）——
  #     本地不许猜词，这是 093x 给 jaLatinIntrusions 定下的边界，094x 不许越过去。
  ECHO_PORT=$((MOCK_PORT + 1))
  ECHOB="http://127.0.0.1:${ECHO_PORT}"
  ECHO_RUN="uat094x-$$-$(date +%s)"
  nohup python3 scripts/uat/mock_assist.py "$ECHO_PORT" "$ECHO_RUN" echo > "$WORK/mockassist_echo.log" 2>&1 < /dev/null &
  MOCK_PID2=$!
  OKE=0
  for i in $(seq 1 10); do
    sleep 1
    if curl -s -m 2 "$ECHOB/uat/stats" | grep -qE "\"ok\": *true.*\"run\": *\"${ECHO_RUN}\".*\"mode\": *\"echo\""; then OKE=1; break; fi
  done
  if [ "$OKE" != "1" ]; then
    FAIL=$((FAIL+1)); echo "FAIL|W9-mock-echo-start|补翻必拒桩起不来（:${ECHO_PORT}）：$(tail -3 "$WORK/mockassist_echo.log" 2>/dev/null | tr '\n' ' ')"
  else
    curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" \
      -d "{\"key\":\"llm_base_url\",\"value\":\"${ECHOB}/v1\"}" >/dev/null
    EW=$(newgreet ja); EW_S=$(sidof "$EW"); EW_T=$(tokof "$EW")
    ECHO_R=$(curl -s -m 30 "$B/api/assist/chat" -H "$J" \
      -d "{\"session\":\"$EW_S\",\"tok\":\"$EW_T\",\"message\":\"ドキュメント翻訳の料金はどのくらいですか\",\"lang\":\"ja\",\"page\":\"/\"}")
    ck W9-source-llm '"source":"llm"' "$ECHO_R"
    ck W9a-wordform-fixed '選択' "$ECHO_R"
    ck W9b-wordform-fixed2 '係数' "$ECHO_R"
    # ⚠️ 这一条锚「費用」，而**两个候选锚都是本轮反证真踩出来的**，别改回去：
    #   ① 早先只锚「言語」＝恒绿假锁：脏稿里本就有一句合法日文「…の文字数**と言語**に応じて変動します」，
    #      把出站那条腿整个拆掉后 W9a/b/f 全红、唯独 W9c 照绿（实测读数）。
    #   ② 改成锚「入力言語」又成**永远达不到**的假红：「入力语言」只在 ※旁白括号里出现，
    #      出栈前整段被末道卫生剥掉（那正是 093x W6 的功），HTTP 面根本观测不到这个词。
    #   ⇒ 锚 ECHO_EXTRA 里那两类现网实证词形（括号外、脏稿里没有对应正字），红得动也绿得动。
    ck W9c-wordform-fixed3 '費用' "$ECHO_R"
    ck W9d-keep-quant '1,000文字' "$ECHO_R"
    # 表序那一腿在 HTTP 面的读数：为什么→なぜ；若短词 什么 先动手就打成「为何」（仍不是日文正字）。
    ck W9g-longword-first 'なぜ' "$ECHO_R"
    if echo "$ECHO_R" | grep -qE '为何'; then
      FAIL=$((FAIL+1)); echo "FAIL|W9h-longword-not-broken|表序被改：为什么 被短词拆成 为何 ${ECHO_R:0:200}"
    else
      PASS=$((PASS+1)); echo "PASS|W9h-longword-not-broken"
    fi
    # 正向对照的另一半（表外的形态必须还在）：三条一起 grep 到任意一条即算"本地没越界猜词"
    if echo "$ECHO_R" | grep -qE '扣费|プロfessional'; then
      PASS=$((PASS+1)); echo "PASS|W9e-no-local-guessing"
    else
      FAIL=$((FAIL+1)); echo "FAIL|W9e-no-local-guessing|补翻被拒时正字表把需要挑说法的形态也改了（越界）：${ECHO_R:0:200}"
    fi
    # 反向对照：表内那些词形一个都不许还在（这才是"就地改写真的落了"而不是"补翻顺手修好了"——
    # 这一台的补翻那一枪原样吐回，物理上不可能修好，所以命中只能来自正字表）
    if echo "$ECHO_R" | grep -qE '选択|系数|入力语言|费用|为什么'; then
      FAIL=$((FAIL+1)); echo "FAIL|W9f-fixup-not-applied|补翻被拒且正字表没生效，中文词形原样出栈：${ECHO_R:0:200}"
    else
      PASS=$((PASS+1)); echo "PASS|W9f-fixup-not-applied"
    fi
  fi

  # 反向对照（假绿的另一半）：把 llm_base_url 指回不可达端点后，同一条问句必须**还能出非空回复**
  # ——证明 W3~W6 那几条绿不是"上游打不通所以正文空"顶出来的
  curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" \
    -d '{"key":"llm_base_url","value":"http://127.0.0.1:9/v1"}' >/dev/null
  RV=$(newgreet ja); SV=$(sidof "$RV"); TV=$(tokof "$RV")
  R2=$(curl -s -m 30 "$B/api/assist/chat" -H "$J" \
    -d "{\"session\":\"$SV\",\"tok\":\"$TV\",\"message\":\"ドキュメント翻訳の料金は\",\"lang\":\"ja\",\"page\":\"/\"}")
  ck W8-offline-still-replies '"reply":"[^"]' "$R2"
fi
# 桩的收尾放在 if 外面：只在成功分支里 kill，那么 W0 失败的那一次会把孤儿留在端口上，
# 下一次运行就以"端口被占"的形式红一次（本次实跑真留下了一个持着 8796 的假上游，已按 pid 点名清掉）。
# ★ 094x 起这里有两台桩（seq 默认档＋echo 补翻必拒档），**两台都要在这条外面收**——
# 同一族坑不会因为多了一台就自己少踩一次；MOCK_PID2 在 W0 红的那条路径下根本没赋值，
# 所以用 ${MOCK_PID2:-} 兜空，kill 收到空参数只是报个错、不会把脚本带停。
{ kill $MOCK_PID 2>/dev/null; wait $MOCK_PID 2>/dev/null; } 2>/dev/null || true
{ kill ${MOCK_PID2:-} 2>/dev/null; wait ${MOCK_PID2:-} 2>/dev/null; } 2>/dev/null || true

# ---------- 5g. ★ 0AR 第 4 波（2026-10-06）X 段：canned 出栈那道闸的**HTTP 面接线证明**
# 单测钉的是判据本身（canned_guard_test.go 逐档钉字面量），这一段钉的是另一件事：
# **闸门拦下的那一稿真的没发给访客、真的没写进缓存，而且拦下来说得出为什么**。
# 这一族缺陷的现网形态不是报错，是"界面正常、首屏是中文／是坏字节"（现网 ar 那一行第 5 个 `---`
# 就是既用不上、又永不重翻、又不出声地住了十几天的），所以只有 HTTP 面＋库里那一行**同时**读出结论才算锁住。
# 三条纪律（与 W 段同源，这里各多一条）：
#   ① 每条负向判据配正向对照——X1 那条 clean 档存在的唯一理由就是"缓存写这条路本来是通的"，
#      没有它，后面六条"库里没这一行"会在「上游压根没接通」那种坏实现下一起绿（假绿）；
#   ② 档位由桩的 /uat/set 控制口点名，且**每条腿先回读桩此刻在哪一档**（控制口没接上时
#      所有档位腿会一起绿，而那正是"闸门全开"的形态）；
#   ③ 段落结束把 llm_base_url 写回不可达端点，别让后面的段落意外依赖这个桩；
#   ④ ★ 段首把库里 i18n:% 全部清掉——前面那些段落（W 段／LG 段）已经在同一个库里写过译文行，
#      不清就是"读的是上一段的缓存、判的是这一段的判据"，第一条档位腿会莫名其妙命中旧字节。
XPORT=$((MOCK_PORT + 2))
XB="http://127.0.0.1:${XPORT}"
XRUN="uat0ARx-$$-$(date +%s)"
# 端口预检（同 093x 那条端口抢占教训：X 段有三台桩的历史，8798 上坐着谁的实例没人知道）
if curl -s -m 1 "$XB/uat/stats" | grep -q '"ok"'; then
  FAIL=$((FAIL+1)); echo "FAIL|X0-port-busy|假上游端口 ${XPORT} 已被占用（kill 掉它或 ASSIST_UAT_MOCK_PORT 换端口），X 段全部无效"
else
  nohup python3 scripts/uat/mock_assist.py "$XPORT" "$XRUN" > "$WORK/mockassist_x.log" 2>&1 < /dev/null &
  MOCK_PID3=$!
  OKX=0
  for i in $(seq 1 10); do
    sleep 1
    # 就绪判据里带 `"set"`：这是 0AR 第 4 波才加的字段，能同时证明「应答的是本次这台桩」
    # 与「桩带得上新的控制口」（旧桩残留会回 200 但没有 set 那一格）。
    if curl -s -m 2 "$XB/uat/stats" | grep -qE "\"ok\": *true.*\"run\": *\"${XRUN}\".*\"set\""; then OKX=1; break; fi
  done
  if [ "$OKX" != "1" ]; then
    FAIL=$((FAIL+1)); echo "FAIL|X0-mock-start|档位桩起不来（:${XPORT}），X 段全部无效：$(tail -3 "$WORK/mockassist_x.log" 2>/dev/null | tr '\n' ' ')"
  else
    log "档位假上游 :${XPORT} 就绪（${i}s）"
    # 段首清库：只清本模块写的那一段键（禁止全表 dump／禁止动别的键，见 canned_purge.go 文件头同条纪律）
    python3 -c 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1],timeout=10);n=c.execute("DELETE FROM configs WHERE key LIKE '"'"'i18n:%'"'"'").rowcount;c.commit();c.close();print("cleared",n)' "$WORK/assist.db" >/dev/null
    curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" \
      -d "{\"key\":\"llm_base_url\",\"value\":\"${XB}/v1\"}" >/dev/null
    curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" \
      -d '{"key":"llm_model","value":"uat-assist-model"}' >/dev/null

    xset(){ curl -s -X POST "$XB/uat/set" -H "$J" -d "$1" >/dev/null; }
    # 桩此刻在哪一档（控制口的自证腿，见上面纪律 ②）
    xband(){ curl -s "$XB/uat/stats" | python3 -c "import sys,json;print(json.load(sys.stdin).get('set',{}).get('$1',''))"; }
    # 切档并**当场回读档位名**：控制口是一次 POST，没人规定它一定听（桩若是旧版／路径改了／JSON 拼错，
    # 它照样回 200 但档位根本没变）。不点名的话，后面三条"被拒"判据演的是**上一档**的戏——
    # 而上一档恰好也全被拒时那三条一路绿灯（AGENTS §三 那条"断言自己会撒谎"的第一族形态）。
    xswitch(){ # $1=侧（canned|gen） $2=档位名
      xset "{\"$1\":\"$2\"}"
      local G; G=$(xband "$1")
      [ "$G" = "$2" ] && { PASS=$((PASS+1)); echo "PASS|X-switch-$1-$2"; } \
        || { FAIL=$((FAIL+1)); echo "FAIL|X-switch-$1-$2|控制口没把桩切到这一档（实读 ${G:-空}），后续判据无效"; }
    }
    # 桩侧读数（canned 那一枪的模型名／次数）
    xstat(){ curl -s "$XB/uat/stats" | python3 -c "import sys,json;print(json.load(sys.stdin).get('$1',0))"; }
    # 库里那一行：只问本模块那一段键，值原样回（换行转成可见标记，免得 python 打印劈行）
    dbcount(){ python3 -c 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1],timeout=10);print(c.execute(sys.argv[2]).fetchone()[0]);c.close()' "$WORK/assist.db" "$1"; }
    dbval(){ python3 -c 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1],timeout=10);r=c.execute(sys.argv[2]).fetchone();print("" if not r or not r[0] else str(r[0]).replace("\n","\\n"));c.close()' "$WORK/assist.db" "$1"; }
    # /health 的 canned 段（状态词＋按 reason 计数）：这一档是运维面**唯一**的读数，别用日志行数替代它
    hstatus(){ curl -s -m 5 "$B/health" | python3 -c "import sys,json;print(json.load(sys.stdin).get('canned',{}).get('status',''))" 2>/dev/null; }
    hreason(){ curl -s -m 5 "$B/health" | python3 -c "import sys,json;print(json.load(sys.stdin).get('canned',{}).get('by_reason',{}).get('$1',0))" 2>/dev/null; }
    # 出栈的那**一个字段**，不拿整段响应体做子串匹配：欢迎词与 chips 同住一个 JSON，
    # 整段匹配会把"chips 那句出现在 greeting 里"读成两半都通过。
    # 本轮 X1 首跑真踩到（桩按整段提示词数行数 ⇒ welcome 里落了一排 chip 句，
    # 而 X1c 整段命中 'How does it work here' 跟着假绿）——字段级读法才分得开"谁是谁"，
    # 与 §一·3 那条「同一个接口的两个字段可以来自两个不同时刻，判据必须点名那一个字段」同一条纪律。
    greetof(){ echo "$1" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("greeting",""))' 2>/dev/null; }
    chips_of(){ echo "$1" | python3 -c 'import sys,json;print("\n".join(json.load(sys.stdin).get("chips") or []))' 2>/dev/null; }
    # 拒绝档的三条通用读数：访客拿到的是中文原文／库里一行都没写／闸门报得出档位名
    rej(){ # $1=语种 $2=reason $3=kind(welcome|chips) $4=锚定中文原文的子串
      local L="$1" R="$2" K="$3" A="$4" GR
      GR=$(newgreet "$L")
      # 只问被拒的那**一个字段**（chips 腿不许被 greeting 顶掉，反之亦然）
      FIELD=""
      if [ "$K" = "chips" ]; then FIELD=$(chips_of "$GR"); else FIELD=$(greetof "$GR"); fi
      ck "X-$K-$L-chinese-served" "$A" "$FIELD"
      local N; N=$(dbcount "select count(*) from configs where key='i18n:$K:$L'")
      [ "$N" = "0" ] && { PASS=$((PASS+1)); echo "PASS|X-$K-$L-no-cache-row"; } \
        || { FAIL=$((FAIL+1)); echo "FAIL|X-$K-$L-no-cache-row|库里写了 $N 行（被拒的那一稿一行都不许留）"; }
      local HN; HN=$(hreason "$R")
      [ "${HN:-0}" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|X-$K-$L-health($R=$HN)"; } \
        || { FAIL=$((FAIL+1)); echo "FAIL|X-$K-$L-health|/health 的 canned.by_reason 里没有 $R（读数=$HN）"; }
    }

    # ---- X1 正向对照：clean 档必须**放行并写缓存**（这一段所有负向判据的底座）----
    xswitch canned clean; xswitch gen dirty
    RX1=$(newgreet en)
    ck X1b-greet-translated 'Welcome from LangCross' "$(greetof "$RX1")"
    ck X1c-chips-translated 'How does it work here' "$(chips_of "$RX1")"
    # chips 的**条数**也在出栈面核一次：闸门放行的是"四行对四行"，字段级读到四条才算这条链闭合
    [ "$(chips_of "$RX1" | grep -c . )" = "4" ] \
      && { PASS=$((PASS+1)); echo "PASS|X1d-chips-four-lines"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X1d-chips-four-lines|界面拿到 $(chips_of "$RX1" | grep -c .) 条（库里四条、界面一条是另一族缺陷：拆条腿没接上）"; }
    [ "$(dbcount "select count(*) from configs where key='i18n:welcome:en'")" = "1" ] \
      && { PASS=$((PASS+1)); echo "PASS|X1e-welcome-row-written"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X1e-welcome-row-written|闸门放行时缓存没写＝X 段所有负向判据失去底座"; }
    [ "$(dbcount "select count(*) from configs where key='i18n:chips:en'")" = "1" ] \
      && { PASS=$((PASS+1)); echo "PASS|X1f-chips-row-written"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X1f-chips-row-written|chips 四条一行翻完却没写缓存（条数契约或写库那条腿断了）"; }

    # ---- X2..X6 六道拒绝档里的五条（第六档 canned_empty 在 HTTP 面物理到不了，见段末那条登记）----
    # 每条都按「档名 → reason → 不许写缓存 → /health 出数」同一口径问，五档共用 rej()：
    # 五处各写一遍就是下一次"只有一侧改了判据"的产地（AGENTS §一·13 同条纪律）。
    xswitch canned lines;          rej ar canned_line_count chips '积分怎么收费'
    xswitch canned placeholder;    rej de canned_placeholder_residue welcome '你好，我是能言'
    xswitch canned separator;      rej es canned_separator_residue welcome '你好，我是能言'
    xswitch canned residue;        rej fr canned_han_residue welcome '你好，我是能言'
    # ⚠️ 档位腿的语种只能从**挂件认的那 12 个语种**里挑（reply_lang.go 的 langLabels：
    #    zh/zh_hant/en/ru/fr/ar/es/pt/de/ja/ko/th，**没有 it**）。
    #    本轮首跑用 it 打这一档，三条读数里两条"绿"、第三条恒红，机制是：
    #    localize() 见 langLabel 为空就**原样出中文**（不拨上游、不写缓存、一行日志都不出），
    #    于是 chinese-served 与 no-cache-row 是**结构性必真**（它们根本不是在测闸门），
    #    而 by_reason 那条永远 0。语种挑错不会让这一腿报错，只会让它悄悄变成空转——
    #    与 §一·6「链路型用例必须自带可达探针」同族：判据要有判别力，先要走到那条腿上。
    #    这里改用 ru：现网那四条 chips 各前挂 "LangCross: " 的实证语种就是它（canned_guard.go 注释）。
    xswitch canned brand;          rej ru canned_brand_injected chips '企业术语库怎么建'

    # ---- X7 修正腿（canned_repaired 是一行 INFO，不是失败）----
    # 现网 ko 那一行落库时是 ⟨LangCross⟩、ja 那一路的错形是「能与」：两条都属于
    # "我们已知的形态、且确定性地能改回去"，所以闸门**先就地修再判**，判完必须放行。
    # 三条读数缺一不可：正文是翻好的（不是退回中文）、库里那一行是**清洗后**的字节、
    # 而且 reason 记的是 canned_repaired（"救回来了"这件事必须能被看见，长期靠它救＝该去查上游）。
    xswitch canned repaired
    RX7=$(newgreet ja)
    ck X7a-repaired-served '翻訳のご相談' "$(greetof "$RX7")"
    V7=$(dbval "select value from configs where key='i18n:welcome:ja'")
    ck X7b-cache-has-correct-brand '能言' "$V7"
    if echo "$V7" | grep -qE '能与|⟨|⟩'; then
      FAIL=$((FAIL+1)); echo "FAIL|X7c-cache-not-cleaned|落库的还是清洗前那一串（发出去与存下来的必须同一串字节）：${V7:0:160}"
    else
      PASS=$((PASS+1)); echo "PASS|X7c-cache-not-cleaned"
    fi
    # X7d：**修正腿在"库里已经躺着坏字节"那一路的读数**（现网 ko 那一行的真实形态）。
    # 为什么不能拿同步那一枪判这一档：translateOnce 出栈前本来就做同一族清洗
    #   （cleanTranslated／剥口径复述括号／restoreBrandAfterTranslation 末尾那次
    #    stripBrandDecorBrackets＋stripBrandTokenResidue，见 localize.go 505-560），
    #   所以到闸门手里的 out **已经是干净的**，`gateFinal != out` 那行 INFO 在同步路径上到不了。
    #   本轮实测读数即为此：repaired 档那一枪正文与库里落的是清洗后的字节（X7a/X7b/X7c 全绿），
    #   而 assist.log 里 `"reason":"canned_repaired"` 出现 0 次。
    # ⇒ 这一腿改成先拿 X7 刚落库的那一行**保留指纹头、把正文换回清洗前的坏形态**，
    #   再打一次 greet：命中读侧 ⇒ 修正腿把 final 洗回来 ⇒ 必须回写同一行（同指纹、非新稿）
    #   并出那一行 INFO。不回写的后果就是注释里那句"这条修好永远只活在内存里"，
    #   库里那行坏字节会被任何读点（含历史回放）再投出去——这一腿锁的正是这件事。
    HEAD7=$(dbval "select value from configs where key='i18n:welcome:ja'" | head -c 12)
    python3 -c 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1],timeout=10)
c.execute("UPDATE configs SET value=? WHERE key='"'"'i18n:welcome:ja'"'"'", (sys.argv[2]+"\n⟨LangCross⟩へようこそ。翻訳のご相談は能与まで、いつでもどうぞ。",))
c.commit();c.close()' "$WORK/assist.db" "$HEAD7"
    RX7B=$(newgreet ja)
    ck X7d-served-still-clean '翻訳のご相談' "$(greetof "$RX7B")"
    if [ "$(grep -c '"reason":"canned_repaired"' "$WORK/assist.log")" -ge 1 ]; then
      PASS=$((PASS+1)); echo "PASS|X7e-repaired-rewrite-log"
    else
      FAIL=$((FAIL+1)); echo "FAIL|X7e-repaired-rewrite-log|读侧那次回写没出 canned_repaired（「救回来了」这件事必须能被看见，长期靠这一腿救＝该去查上游）"
    fi
    V7D=$(dbval "select value from configs where key='i18n:welcome:ja'")
    if echo "$V7D" | grep -qE '能与|⟨|⟩'; then
      FAIL=$((FAIL+1)); echo "FAIL|X7f-repaired-row-rewritten|读侧修好了却没回写，库里仍是坏字节：${V7D:0:160}"
    else
      PASS=$((PASS+1)); echo "PASS|X7f-repaired-row-rewritten"
    fi

    # ---- X8／X9 两档"刻意只观测、不拦正文"（拒绝与观测混在一起是这个模块最容易长歪的地方）----
    # 判据取向那句话在这里的实面：**没写够**（品牌名脱落、脚本纯度可疑）不许把访客退回看中文，
    # 所以这两档的断言方向与 X2..X6 **完全相反**——正文照发、缓存照写、只在日志与 /health 里露面。
    xswitch canned brand_drop
    RX8=$(newgreet pt)
    ck X8a-dropped-still-serves 'Welcome' "$(greetof "$RX8")"
    [ "$(dbcount "select count(*) from configs where key='i18n:welcome:pt'")" = "1" ] \
      && { PASS=$((PASS+1)); echo "PASS|X8b-brand-dropped-row-written"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X8b-brand-dropped-row-written|观测档把正文拦回去了＝把英文首屏那种代价搬到了葡语（canned_brand_dropped 只许出声）"; }
    # ⚠️ 判据落在日志而不是 /health：`noteCannedCold` 是 by_reason 那本账的**唯一写方**，
    # 而它只在「上游没拨通／闸门拒绝／退避窗口」这三类**失败**路径上被调；
    # 两档纯观测（品牌名脱落、脚本纯度）走的是 observeCannedSoftTier／observeCannedScriptImpurity，
    # 只出声不计数（本轮实测：/health 里 canned_brand_dropped 恒 0，而 assist.log 那一行明明白白带着 reason）。
    # 把观测档计进失败数会把 canned.status 从 ok 打成 cold，那是对运维撒谎——所以改判据，不改产品。
    [ "$(grep -c '"reason":"canned_brand_dropped"' "$WORK/assist.log")" -ge 1 ] \
      && { PASS=$((PASS+1)); echo "PASS|X8c-brand-dropped-logged"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X8c-brand-dropped-logged|只观测档没出声（现网 ko 那一行就是这么无声无息住在首屏的）"; }
    # X9：泰文界面拿到的是一句纯拉丁（clean 档天然形态）⇒ canned_script_impure 出声、正文照发
    xswitch canned clean
    RX9=$(newgreet th)
    ck X9a-impure-still-serves 'Welcome' "$(greetof "$RX9")"
    [ "$(dbcount "select count(*) from configs where key='i18n:welcome:th'")" = "1" ] \
      && { PASS=$((PASS+1)); echo "PASS|X9b-script-impure-row-written"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X9b-script-impure-row-written|纯度档拦住了正文（阈值未定之前拦住＝拿整段回中文换可疑读数）"; }
    # 同上：纯度档也是**只观测**，读数在日志里（现网那一族将来靠这批 detail 定阈值，
    # 而拿 /health 计数判它＝要求它进失败账，方向与「不许把观测当失败」相反）。
    [ "$(grep -c '"reason":"canned_script_impure"' "$WORK/assist.log")" -ge 1 ] \
      && { PASS=$((PASS+1)); echo "PASS|X9c-script-impure-logged"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X9c-script-impure-logged|纯度读数没出声（下一批就没了定阈值的分布）"; }

    # ⚠️ **X12／X13 这两条对话腿必须排在 X10 之前**（本轮实测的排法事故）：
    # X10 用 http500 档把上游真打死，连续失败会触发 llm provider 自己的**五分钟冷却**
    # （日志行「候选模型都在冷却中（连续失败会冷却 5 分钟，本次未发起请求）」）。
    # 冷却是按 provider 算的、不分语种、不分链路，于是排在 X10 之后的 chat 一律走规则兜底：
    # `"source":"llm"` 恒假、⑲ 那三句压根没进过守卫、⑱ 的按钮名还是库里那句中文——
    # 五条腿一起红，而产品一行没错。这一族红灯的形态是「上游活着但矩阵说它挂了」，
    # 判据修不了，只能修**排程**：把破坏性档位（打死上游、开退避窗口）放到所有需要上游的腿之后。
    # ---- X12 ⑱ 按钮名本地化：过去 feature_links.name 是**原样**塞进回复的 ----
    # 这类"某条腿压根不在清单里"的形态日志一行都看不见（不是翻坏，是没翻），
    # 所以判据只能落在两处：回复里的按钮名不许有汉字，且库里必须真长出 i18n:feature_<key>:<lang> 那几行。
    xswitch canned clean; xswitch gen actions
    RA=$(newgreet en); SA=$(sidof "$RA"); TA=$(tokof "$RA")
    RX12=$(curl -s -m 30 "$B/api/assist/chat" -H "$J" \
      -d "{\"session\":\"$SA\",\"tok\":\"$TA\",\"message\":\"what can you do for my team\",\"lang\":\"en\",\"page\":\"/\"}")
    if [ -n "${ASSIST_UAT_KEEP:-}" ]; then echo "DEBUG|X12-response|$RX12"; fi
    ck X12b-has-actions '"actions":\[' "$RX12"
    CJK_NAMES=$(echo "$RX12" | python3 -c '
import sys,json,re
d=json.load(sys.stdin)
names=[a.get("name","") for a in (d.get("actions") or [])]
print(sum(1 for n in names if re.search(r"[一-鿿]",n)))' 2>/dev/null || echo ERR)
    # 正向对照（按钮真的翻到了）与负向对照（一个汉字都没有）必须同时成立：
    # 只留后者的话，"actions 被整条摘掉"也能绿——那是把功能入口连坐删掉的另一种失败。
    [ "$CJK_NAMES" = "0" ] && { PASS=$((PASS+1)); echo "PASS|X12c-action-names-no-cjk"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X12c-action-names-no-cjk|英文回复里挂着 $CJK_NAMES 个中文按钮名（⑱ 那条腿没接上）"; }
    ck X12d-action-names-translated 'Welcome' "$RX12"
    [ "$(dbcount "select count(*) from configs where key LIKE 'i18n:feature_%:en'")" -ge 2 ] \
      && { PASS=$((PASS+1)); echo "PASS|X12e-feature-rows-written"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X12e-feature-rows-written|按钮名翻完了没写缓存（每一句带按钮的回复都要现翻一遍＝白烧上游）"; }
    # ㊷① 的另一半（与 X11 同一进程、同一时刻）：对话那一枪仍必须打**主模型**
    [ "$(xstat gen_model)" = "uat-assist-model" ] && { PASS=$((PASS+1)); echo "PASS|X12f-chat-still-uses-main-model"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X12f-chat-still-uses-main-model|对话那一枪被 canned 快模型带走了（$(xstat gen_model)）＝把音色档让给了首屏档"; }

    # ---- X13 ⑲ 编造承诺守卫：现网三条实证各一句，外加一句**必须留下**的正当正文 ----
    # 「给您开一个 API sandbox 直接联调」「我们有 327 个标准词」「火花塞 固定译 ignition plug」
    # 这三条报价闸与承诺闸都拦不住——没有一条问"这个能力库里有吗"。
    # ⚠️ 这一腿的问句用**中文**、界面语言用 en：接管后作答语言=中文，
    #    桩回的这份中文草稿才会原样走到 ⑲（拿英文草稿测中文判据＝恒红的假靶子）。
    xswitch gen fabricated
    RF=$(newgreet en); SF=$(sidof "$RF"); TF=$(tokof "$RF")
    RX13=$(curl -s -m 30 "$B/api/assist/chat" -H "$J" \
      -d "{\"session\":\"$SF\",\"tok\":\"$TF\",\"message\":\"我们厂里主要做设备说明书，这块你们一般怎么配合\",\"lang\":\"en\",\"page\":\"/\"}")
    if [ -n "${ASSIST_UAT_KEEP:-}" ]; then echo "DEBUG|X13-response|$RX13"; fi
    ck X13a-source-llm '"source":"llm"' "$RX13"
    for bad in '327' 'sandbox' '火花塞'; do
      if echo "$RX13" | grep -q "$bad"; then
        FAIL=$((FAIL+1)); echo "FAIL|X13b-no-fabrication|编造句里的「$bad」还是发出去了：${RX13:0:200}"
      else
        PASS=$((PASS+1)); echo "PASS|X13b-no-fabrication-$bad"
      fi
    done
    ck X13c-keep-legal-sentence '您可以先在编辑器里试一段' "$RX13"
    for r in fabr_count_claim fabr_unknown_deliverable fabr_term_example; do
      [ "$(grep -c "\"reason\":\"$r\"" "$WORK/assist.log")" -ge 1 ] \
        && { PASS=$((PASS+1)); echo "PASS|X13d-log-$r"; } \
        || { FAIL=$((FAIL+1)); echo "FAIL|X13d-log-$r|三档 reason 里这一档没出声（分档名是对外排障契约，静默删句＝运维不知道是该补知识还是该改判据）"; }
    done

    # ---- X10 ㊷ 退避窗口：这一档要证明的是**下一位访客不再白等也不再白拨** ----
    # 现网 th 那两天的形态是「同步必超时 → 后台补一枪 → 那一枪又被闸拒 → 缓存写不上」，
    # 于是每一位访客从零重拨一次、每次都烧满同步预算——界面看着"只是慢"，日志只有 WARN。
    # 桩这侧用 http500 把上游真的打死：后台腿必然失败 ⇒ 开窗口；窗口内那一条腿**连同步那一枪也不许打**。
    xswitch canned http500
    C0=$(xstat canned)
    RX0A=$(newgreet ko)
    ck X10a-first-greet-chinese '你好，我是能言' "$(greetof "$RX0A")"
    sleep 3   # 后台腿在请求链之外，给它一次收尾时间（窗口只能由后台腿那次失败开）
    C1=$(xstat canned)
    RX0B=$(newgreet ko)
    C2=$(xstat canned)
    ck X10b-second-greet-chinese '你好，我是能言' "$(greetof "$RX0B")"
    # 前后两次 greet 之间**没有新增上游调用**＝窗口内两条腿都没打（这一条是退避的本体：
    # 只押后台腿的话访客那 8 秒白等照旧，台账 ㊷ 的验收判据写的正是"首屏不再白等"）
    [ "$C1" -gt "$C0" ] && { PASS=$((PASS+1)); echo "PASS|X10c-first-round-dialed($C0→$C1)"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X10c-first-round-dialed|第一次 greet 压根没拨上游（$C0→$C1），后面那条'不再拨'就成了空判"; }
    [ "$C2" = "$C1" ] && { PASS=$((PASS+1)); echo "PASS|X10d-window-dials-nothing($C1→$C2)"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X10d-window-dials-nothing|退避窗口内又拨了 $((C2-C1)) 次（$C1→$C2）＝窗口没生效"; }
    [ "$(hstatus)" = "backoff" ] && { PASS=$((PASS+1)); echo "PASS|X10e-health-backoff-word"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X10e-health-backoff-word|/health canned.status 不是 backoff（实读 $(hstatus)）"; }
    [ "$(hreason canned_bg_backoff)" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|X10f-backoff-reason-counted"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X10f-backoff-reason-counted|窗口内那次没记 canned_bg_backoff（'没拨'与'查上游'两种动作分不开＝排障只能猜）"; }

    # ---- X11 ㊷① canned 用独立快模型：欢迎词/chips 是全网一份的短文本，不该拿推理模型买思考 ----
    # 这一档不是管理台键（configKeyWhitelist 里没有 canned_*），所以按现网运维口径直接写库。
    python3 -c 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1],timeout=10);c.execute("INSERT INTO configs(key,value) VALUES('"'"'canned_llm_model'"'"','"'"'uat-fast-model'"'"') ON CONFLICT(key) DO UPDATE SET value=excluded.value");c.commit();c.close()' "$WORK/assist.db"
    xswitch canned clean
    RX11=$(newgreet ru)
    ck X11a-ru-served 'Welcome' "$(greetof "$RX11")"
    [ "$(xstat canned_model)" = "uat-fast-model" ] && { PASS=$((PASS+1)); echo "PASS|X11b-canned-dials-fast-model"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X11b-canned-dials-fast-model|canned 那一枪还在打主模型（实读 $(xstat canned_model)）"; }

    # ---- X14 读侧二次闸门：库里那一行的**字节**比判据旧时，命中也必须被拦在读侧之外 ----
    # 现网 ar／th 那两行的终点不是写侧漏放，是"判据已经能拦，可它永远走不到判据那一步"。
    # 手法：拿 X1 那次**合法落库**的行，保留指纹头、只把正文换成坏形态——
    # 这样 head==fp（读侧按命中走），坏字节只有读侧那道闸看得见。
    # 断言方向：这一轮访客必须拿回中文原文；同一轮里那条被拒的旧行必须作废；
    # 而**重翻后的新行**必须是干净的（否则就成了"每轮各白烧一枪"的死循环）。
    xswitch canned clean
    HEAD14=$(dbval "select value from configs where key='i18n:welcome:en'" | head -c 12)
    python3 -c 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1],timeout=10)
c.execute("UPDATE configs SET value=? WHERE key='"'"'i18n:welcome:en'"'"'", (sys.argv[2]+"\nWelcome! I can translate 文件翻译 for you.\n---",))
c.commit();c.close()' "$WORK/assist.db" "$HEAD14"
    RX14=$(newgreet en)
    # X14a 的期望按**实测行为**写：读侧判不合格 ⇒ 那一行作废 ⇒ 本轮继续往下走同步腿重拨
    #   （localize.go 的 cache_read 分支不作废后**不 return**，日志里那句"下一次重翻"是旧措辞，
    #    真实读数是同一轮里就拿到干净新稿）。所以这一腿判的是**坏字节没投出去**，
    #    而不是"访客这一轮看到中文"——后者会把一条更好的行为判成红。
    #   正向对照（拿到了翻好的正文）与负向对照（坏形态一个字都没有）两条一起成立才算数。
    ck X14a2-bad-cache-row-serves-clean 'Welcome' "$(greetof "$RX14")"
    if echo "$(greetof "$RX14")" | grep -qE '文件翻译|---'; then
      FAIL=$((FAIL+1)); echo "FAIL|X14a-bad-cache-row-not-served|库里那行坏字节仍然投给了访客：$(greetof "$RX14" | head -c 160)"
    else
      PASS=$((PASS+1)); echo "PASS|X14a-bad-cache-row-not-served"
    fi
    [ "$(grep -c '"stage":"cache_read"' "$WORK/assist.log")" -ge 1 ] \
      && { PASS=$((PASS+1)); echo "PASS|X14b-read-side-stage-logged"; } \
      || { FAIL=$((FAIL+1)); echo "FAIL|X14b-read-side-stage-logged|读侧那次拦截没记 stage=cache_read（写侧与读侧的账必须分得开）"; }
    V14=$(dbval "select value from configs where key='i18n:welcome:en'")
    ck X14c-refilled-clean 'Welcome from LangCross' "$V14"
    if echo "$V14" | grep -qE '文件翻译|^---'; then
      FAIL=$((FAIL+1)); echo "FAIL|X14d-refill-not-gated|重翻那一行又写回了坏形态：${V14:0:160}"
    else
      PASS=$((PASS+1)); echo "PASS|X14d-refill-not-gated"
    fi

    # ---- X15 ③ 启动期旧代孤儿行清理：只删过期指纹，人工档与不认识的键一行都不许碰 ----
    # 抬 rev 让**每一行**非人工档指纹当场失效，而运行期自愈的前提是**有人来**：
    # 一个语种十天没访客，那一行旧字节就在库里躺十天，而它是"客户屏幕上正在投什么"的持久事实。
    # 判据必须**逐行现算指纹**（按前缀 LIKE 一条都抓不到——指纹头里根本没有 rev 字样），
    # 所以这里用**独立库的第四台实例**：清库动作只在启动期发生一次，进程内造不出第二次。
    PORT3="${ASSIST_UAT_PORT3:-8795}"
    B3="http://127.0.0.1:${PORT3}"
    if curl -s -m 1 "$B3/health" | grep -q '"ok":true'; then
      FAIL=$((FAIL+1)); echo "FAIL|X15-port-busy|清理实例端口 ${PORT3} 已被占用，这一腿无效"
    else
      log "启动清理验证实例 :${PORT3}（独立临时库，先灌两行孤儿行再重启）..."
      ASSIST_MOCK=1 ASSIST_ADMIN_TOKEN="uat-purge-tok" ASSIST_ADDR="127.0.0.1:${PORT3}" \
        ASSIST_DB="$WORK/purge.db" nohup "$BIN" > "$WORK/purge1.log" 2>&1 < /dev/null &
      PID4=$!
      OKP=0
      for i in $(seq 1 10); do
        sleep 1
        if curl -s -m 2 "$B3/health" | grep -q '"ok":true'; then OKP=1; break; fi
      done
      if [ "$OKP" != "1" ]; then
        FAIL=$((FAIL+1)); echo "FAIL|X15-purge-instance-start|清理验证实例起不来：$(tail -3 "$WORK/purge1.log" | tr '\n' ' ')"
      else
        { kill $PID4 2>/dev/null; wait $PID4 2>/dev/null; } 2>/dev/null || true
        sleep 1
        # 三行靶子：过期指纹（该删）／人工档（永不删）／不认识的 kind（不是本模块写的，不碰）
        python3 -c 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1],timeout=10)
rows=[("i18n:welcome:it","0123456789ab\nVecchia generazione: welcome stale"),
      ("i18n:welcome:ru","!manual\nBenvenuto scritto a mano"),
      ("i18n:somethingelse:xx","0123456789ab\nNot written by this module")]
c.executemany("INSERT INTO configs(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",rows)
c.commit();c.close()' "$WORK/purge.db"
        ASSIST_MOCK=1 ASSIST_ADMIN_TOKEN="uat-purge-tok" ASSIST_ADDR="127.0.0.1:${PORT3}" \
          ASSIST_DB="$WORK/purge.db" nohup "$BIN" > "$WORK/purge2.log" 2>&1 < /dev/null &
        PID5=$!
        OKP2=0
        for i in $(seq 1 10); do
          sleep 1
          if curl -s -m 2 "$B3/health" | grep -q '"ok":true'; then OKP2=1; break; fi
        done
        if [ "$OKP2" != "1" ]; then
          FAIL=$((FAIL+1)); echo "FAIL|X15-purge-instance-restart|清理实例重启失败：$(tail -3 "$WORK/purge2.log" | tr '\n' ' ')"
        else
          pc(){ python3 -c 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1],timeout=10);print(c.execute("SELECT COUNT(*) FROM configs WHERE key=?", (sys.argv[2],)).fetchone()[0]);c.close()' "$WORK/purge.db" "$1"; }
          STALE=$(pc "i18n:welcome:it"); MAN=$(( $(pc "i18n:welcome:ru") )); UNK=$(pc "i18n:somethingelse:xx")
          [ "$STALE" = "0" ] && { PASS=$((PASS+1)); echo "PASS|X15a-stale-row-deleted"; } \
            || { FAIL=$((FAIL+1)); echo "FAIL|X15a-stale-row-deleted|过期指纹那一行还在（现算判据没跑或算错了）"; }
          [ "$MAN" = "1" ] && { PASS=$((PASS+1)); echo "PASS|X15b-manual-row-kept"; } \
            || { FAIL=$((FAIL+1)); echo "FAIL|X15b-manual-row-kept|!manual 人工档被删了＝换代把运营手写的那一行洗掉（宁可漏删不可误删）"; }
          [ "$UNK" = "1" ] && { PASS=$((PASS+1)); echo "PASS|X15c-unknown-kind-kept"; } \
            || { FAIL=$((FAIL+1)); echo "FAIL|X15c-unknown-kind-kept|不认识的 kind 也被删了＝替别人清库"; }
          # 逐行 INFO＋收尾总数：这一腿是**一次性**的，没有这两行就没有人能证明它跑过
          [ "$(grep -c 'canned_purge_stale' "$WORK/purge2.log")" = "1" ] \
            && { PASS=$((PASS+1)); echo "PASS|X15d-purge-per-line-log"; } \
            || { FAIL=$((FAIL+1)); echo "FAIL|X15d-purge-per-line-log|逐行清理读数不是 1 行（库里只种了一行过期靶子；只报总数就分不清'清过'与'没扫到'）"; }
          ck X15e-purge-done-log 'canned_purge_done' "$(cat "$WORK/purge2.log")"
          { kill $PID5 2>/dev/null; wait $PID5 2>/dev/null; } 2>/dev/null || true
        fi
      fi
    fi

    # 纪律 ③：段末把上游指回不可达端点，并 gen 复位（后面 D／E／F／G 段不许意外依赖这个桩）
    xswitch gen dirty
    curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" \
      -d '{"key":"llm_base_url","value":"http://127.0.0.1:9/v1"}' >/dev/null
  fi
fi
# ★ X 段有第三台桩：kill 仍旧放在 if 外面（只在成功分支里收，W0/X0 红的那一次会把孤儿留在端口上，
# 下一次运行以"端口被占"的形式红一次——同一族坑不会因为多了一台就自己少踩一次）。
{ kill ${MOCK_PID3:-} 2>/dev/null; wait ${MOCK_PID3:-} 2>/dev/null; } 2>/dev/null || true

# ---------- 6. 管理端 CRUD 回归 ----------
NID=$(curl -s -X POST "$B/api/assist/admin/scripts" -H "$AH" -H "$J" \
  -d "{\"key\":\"uat$(date +%s)\",\"stype\":\"keyword\",\"title\":\"UAT\",\"keywords\":\"uattest魔法词\",\"content\":\"UAT话术命中\",\"priority\":9,\"enabled\":1}" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id","0"))' 2>/dev/null)
[ -n "$NID" ] && [ "$NID" != "0" ] && { PASS=$((PASS+1)); echo "PASS|D1-crud-create($NID)"; } || { FAIL=$((FAIL+1)); echo "FAIL|D1-crud-create"; }
R=$(curl -s -X PUT "$B/api/assist/admin/scripts?id=$NID" -H "$AH" -H "$J" -d '{"content":"UAT改后"}')
ck D2-crud-update '"ok":true' "$R"
R=$(curl -s -X DELETE "$B/api/assist/admin/scripts?id=$NID" -H "$AH")
ck D3-crud-delete '"ok":true' "$R"

# ---------- 7. 管理页托管 ----------
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/assist/admin")
ck E1-admin-page '^200$' "$C"
# ★ 改造 1A：管理页为二进制内嵌（未配 ASSIST_WEB）——内容必须是真实页面而非 404 占位
R=$(curl -s "$B/assist/admin")
ck E2-admin-embedded 'AI 助手管理台' "$R"

# ---------- 8. ★ 改造 1A：管理台 Token 主库桥接 + env 优先级 ----------
# 构造最小主库（仅 system_config 表）：验证 assist 以只读方式读到该 Token。
# 注：写入值为明文——DecryptSecret 对无 enc:v1: 前缀的历史明文原样返回（兼容路径），
#     主后台经 /api/assist 写入时是 enc:v1: 密文，两条路径共用同一读取函数。
MAINDB="$WORK/main.db"
DBTOK="uat-dbtoken-$(date +%s)"
python3 - "$MAINDB" "$DBTOK" <<'PY'
import sqlite3, sys
conn = sqlite3.connect(sys.argv[1])
conn.execute("CREATE TABLE system_config(key TEXT PRIMARY KEY, value TEXT, updated_at TEXT)")
conn.execute("INSERT INTO system_config(key,value,updated_at) VALUES('assist_admin_token',?,datetime('now'))", (sys.argv[2],))
conn.commit(); conn.close()
PY

log "启动 assist :${PORT2}（无 ASSIST_ADMIN_TOKEN，改由主库桥接取 Token）..."
ASSIST_MOCK=1 ASSIST_ADDR="127.0.0.1:${PORT2}" ASSIST_DB="$WORK/assist2.db" \
  MAIN_DB="$MAINDB" \
  nohup "$BIN" > "$WORK/assist2.log" 2>&1 < /dev/null &
PID2=$!
OK2=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$B2/health" | grep -q '"ok":true'; then OK2=1; break; fi
done
[ "${OK2:-0}" = "1" ] || { echo "assist2 启动失败"; tail -5 "$WORK/assist2.log"; kill $PID2 2>/dev/null; exit 1; }

# 8a. 主库 Token 生效（env 未配 → 回落到 DB）
C=$(curl -s -o /dev/null -w '%{http_code}' "$B2/api/assist/admin/kb" -H "X-Assist-Admin: $DBTOK")
ck F1-dbtoken-works '^200$' "$C"
# 8b. 非法 Token 仍被拒（桥接不放松鉴权）
C=$(curl -s -o /dev/null -w '%{http_code}' "$B2/api/assist/admin/kb" -H "X-Assist-Admin: wrong-token")
ck F2-dbtoken-reject-wrong '^401$' "$C"
{ kill $PID2 2>/dev/null; wait $PID2 2>/dev/null; } 2>/dev/null || true

# 8c. env 优先级高于主库（同库同 Token，env 显式配置应压过 DB 值）
ENVTOK="uat-envtoken-$(date +%s)"
ASSIST_MOCK=1 ASSIST_ADMIN_TOKEN="$ENVTOK" ASSIST_ADDR="127.0.0.1:${PORT2}" \
  ASSIST_DB="$WORK/assist3.db" MAIN_DB="$MAINDB" \
  nohup "$BIN" > "$WORK/assist3.log" 2>&1 < /dev/null &
PID3=$!
OK3=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$B2/health" | grep -q '"ok":true'; then OK3=1; break; fi
done
[ "${OK3:-0}" = "1" ] || { echo "assist3 启动失败"; tail -5 "$WORK/assist3.log"; kill $PID3 2>/dev/null; exit 1; }
C=$(curl -s -o /dev/null -w '%{http_code}' "$B2/api/assist/admin/kb" -H "X-Assist-Admin: $ENVTOK")
ck F3-env-priority-works '^200$' "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B2/api/assist/admin/kb" -H "X-Assist-Admin: $DBTOK")
ck F4-env-priority-overrides-db '^401$' "$C"
{ kill $PID3 2>/dev/null; wait $PID3 2>/dev/null; } 2>/dev/null || true

# ---------- 9. ★ 〇-LK（2026-09-22）：Token 热生效 / 访客密钥解耦 / 欢迎语去重 ----------
# 这一段专门锁用户反馈的两件事：「ai 助手要带缓存，不然刷新一次页面就没了」与
# 「配置请参考我其他 llm 配置的方式重新做」。三件都是改坏就直接影响使用：
#   G1-G3 管理 Token 保存即生效（configs.admin_token 优先于启动快照，且不留双凭据窗口）；
#   G4-G5 该键的读取掩码 / 空值拒绝（清除只能走主后台显式 clear）；
#   G6 同一会话重复 greet 不再堆重复欢迎语（台账与 history 恢复都受影响）；
#   G7 换管理 Token 并重启后，老访客的会话令牌仍然有效（sess_key 已持久化、与 Token 解耦）。
# 放在最后：轮换会让前面用的 $AH 失效，temp 库随即销毁，不影响其它断言。
OLDTOK="${AH#X-Assist-Admin: }"
NEWTOK="uat-rotated-$(date +%s)"
R=$(curl -s -X PUT "$B/api/assist/admin/config" -H "$AH" -H "$J" -d "{\"key\":\"admin_token\",\"value\":\"$NEWTOK\"}")
ck G1-rotate-write '"ok":true' "$R"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/admin/kb" -H "X-Assist-Admin: $NEWTOK")
ck G2-new-token-live '^200$' "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/admin/kb" -H "X-Assist-Admin: $OLDTOK")
ck G3-old-token-dead '^401$' "$C"
# G4：管理面列配置时 admin_token 只能以掩码出现（明文既不进响应也不进日志）
R=$(curl -s "$B/api/assist/admin/config" -H "X-Assist-Admin: $NEWTOK")
if echo "$R" | grep -q "$NEWTOK"; then
  FAIL=$((FAIL+1)); echo "FAIL|G4-admin-token-masked|明文泄露"
else
  PASS=$((PASS+1)); echo "PASS|G4-admin-token-masked"
fi
ck G4b-token-listed 'admin_token' "$R"
ck G4c-token-masked '\*{3,}' "$R"# G5：空值写入拒绝（空串清除会把管理面一把关掉，属显式 clear 流程的职责）
C=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$B/api/assist/admin/config" -H "X-Assist-Admin: $NEWTOK" -H "$J" -d '{"key":"admin_token","value":""}')
ck G5-empty-token-400 '^400$' "$C"

# G6：欢迎语去重（同一 sid 连续 greet 三次，台账里只留一条开场白）
RG=$(curl -s "$B/api/assist/greeting?page=/")
SID_G=$(echo "$RG" | python3 -c 'import sys,json;print(json.load(sys.stdin)["session"])')
TOK_G=$(echo "$RG" | python3 -c 'import sys,json;print(json.load(sys.stdin)["tok"])')
for i in 1 2 3; do curl -s "$B/api/assist/greeting?session=$SID_G&tok=$TOK_G&page=/" >/dev/null; done
NA=$(curl -s "$B/api/assist/history?session=$SID_G&tok=$TOK_G&limit=50" \
  | python3 -c 'import sys,json;print(sum(1 for m in json.load(sys.stdin).get("messages",[]) if m.get("role")=="assistant"))')
if [ "$NA" = "1" ]; then PASS=$((PASS+1)); echo "PASS|G6-greet-dedup"; else FAIL=$((FAIL+1)); echo "FAIL|G6-greet-dedup|want 1 got $NA"; fi

# G7：sess_key 与 Token 解耦——换 Token 重启后，老访客的 tok 仍可续用（刷新页面不再丢对话）
{ kill $PID 2>/dev/null; wait $PID 2>/dev/null; } 2>/dev/null || true
log "重启 assist :${PORT}（新 ASSIST_ADMIN_TOKEN，同一 ASSIST_DB）验证会话密钥不随 Token 变化..."
ASSIST_MOCK=1 ASSIST_ADMIN_TOKEN="uat-restarted-$(date +%s)" ASSIST_ADDR="127.0.0.1:${PORT}" \
  ASSIST_DB="$WORK/assist.db" \
  nohup "$BIN" > "$WORK/assist-restart.log" 2>&1 < /dev/null &
PID=$!
OKR=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$B/health" | grep -q '"ok":true'; then OKR=1; break; fi
done
[ "${OKR:-0}" = "1" ] || { echo "assist 重启失败"; tail -5 "$WORK/assist-restart.log"; exit 1; }
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/history?session=$SID_G&tok=$TOK_G&limit=50")
ck G7-sess-key-survives-restart '^200$' "$C"
# 反向：老会话的 tok 不是任何人都能续——伪令牌仍须 401（否则 G7 就成了「不设防」）
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/assist/history?session=$SID_G&tok=deadbeef")
ck G8-fake-tok-still-401 '^401$' "$C"

# ---------- 汇总 ----------
DUR=$(( $(date +%s) - START ))
log "=============================="
log "assist UAT：PASS=$PASS FAIL=$FAIL DUR=${DUR}s"
log "日志目录：$WORK"
log "=============================="
{ kill $PID 2>/dev/null; wait $PID 2>/dev/null; } 2>/dev/null || true
# ★ 排障口子（默认关，行为与以前逐字一致）：`ASSIST_UAT_KEEP=1 bash scripts/uat/assist_uat.sh`
#   保留那一个临时目录（库里那些 i18n:% 行＋assist.log 都在里面）。
#   为什么值得留这一格：X 段判的是"闸门拦下时**有没有出声**"，而 by_reason 与日志行是两条不同的腿——
#   只看矩阵那三行 PASS/FAIL 分不开"拒了但记成别的档"与"压根没拒"（本轮 X-chips-it-health 就是这么卡了一轮）。
#   目录在 mktemp 下，留着不外泄；排完自己 rm -rf。
if [ -n "${ASSIST_UAT_KEEP:-}" ]; then
  log "ASSIST_UAT_KEEP=1：临时目录不清理（库与日志留在 $WORK）"
else
  rm -rf "$WORK"
fi
[ "$FAIL" = "0" ] || exit 1
exit 0
