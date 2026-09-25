#!/usr/bin/env bash
# ============================================================================
# scripts/uat/api_uat.sh — 后端全链路 UAT 断言套件（API 层，含前端所调全部后端面）
# 覆盖：健康/公开接口 / 认证注册 / 防刷协议 / 模型路由 / 估价 / 翻译扣费 / 余额硬闸
#       充值支付(下单-模拟-幂等-发票) / 邀请裂变 / 知识库 / OpenAPI / 权限租户隔离
#       套餐订阅 / 通知反馈 / 文件翻译(docx)
# 依赖：mock_llm.py 已启动、uat 服务已启动且为全新库（run_uat.sh 负责编排）
# 环境变量：
#   BASE_URL    服务地址（默认 http://127.0.0.1:8899）
#   UAT_DB      SQLite 库路径（默认 /tmp/uat/dev.db）
#   ADMIN_USER  超管用户名（默认 admin）
#   ADMIN_PASS  超管密码（默认 Admin@1234）
# ============================================================================
# 只 set -u、刻意不 set -e：本套件是「跑完全部断言再汇总」，任何一步中断都会吞掉后面的覆盖面，
# 失败以 FAIL 计数体现在 PASS=/FAIL= 汇总行里，交给 run_uat.sh 判总闸。
set -u
B="${BASE_URL:-http://127.0.0.1:8899}"
U="${UAT_DB:-/tmp/uat/dev.db}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Admin@1234}"
J='Content-Type: application/json'
source "$(dirname "$0")/dblib.sh"   # 双方言断言层（sqlite/PG）
# 落库类断言一律走 dbq/dbjson，禁止在本文件直写 sqlite3 / psql：
# 直写等于把 PG 方言永久排除在 UAT 之外（is_personal bool、JSON1 缺失这类专属缺陷会全部漏检）。
# 计数只累加不中断（见上）；报告每行一个 PASS|名称 / FAIL|名称|期望，供 run_uat.sh 抓取汇总。
# 惯例：纯准备类调用（建包、存策略、造受邀号）一律 >/dev/null 且不开断言名额——
# 它们是否正确由后面的行为断言反证，准备步骤失败时行为断言必红，不必为其重复计一条。
PASS=0; FAIL=0; START=$(date +%s)

# ck <名称> <期望正则> <实际文本> — 套件唯一的断言入口，命中即 PASS。
# ★ 必须 grep -qE（ERE），禁止写成 BRE 的 'a\|b'：`\|` 是 GNU 扩展，BSD grep 与精简容器里
#   自带的 grep 会把它当字面量、静默返回 0 命中——于是「或」类期望值永远判失败（或反过来
#   恒判通过），闸门失效却不报错，是全仓最难发现的一类 bug（见 AGENTS.md 第七节）。
# 第三参截断到 220 字符再打印：整页 HTML / zip 字节进报告会刷屏，够定位即可。
ck(){ if echo "$3" | grep -qE "$2"; then PASS=$((PASS+1)); echo "PASS|$1"; else FAIL=$((FAIL+1)); echo "FAIL|$1|want[$2]|got[${3:0:220}]"; fi; }
# reg — 走公开注册接口建号（带 agreed:true，协议是硬闸；extra 供追加 ref/job_role 等字段）
# tok — 登录取 token；整条套件只认这一个取 token 的口径
# pv  — 从 stdin 的 JSON 里按 python 表达式取值（后端返回形态多包一层，比 grep 提字段可靠）
reg(){ local extra="${6:-}"; curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"$1\",\"password\":\"$2\",\"code\":\"$3\",\"name\":\"$4\",\"email\":\"$5\",\"agreed\":true${extra:+,$extra}}"; }
tok(){ curl -s $B/api/auth/login -H "$J" -d "{\"username\":\"$1\",\"password\":\"$2\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))'; }
pv(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(d$1)"; }

echo "=== A 阶段：公开接口 / 认证 / 计费 / 翻译 / 交易 ==="
# ---------- A1 健康与公开接口 ----------
# 这一组全部匿名可访：它们是注册页/落地页的数据源，一旦需要登录就把首屏整条链路打死，
# 所以放在套件最前面当「服务起没起、公开面漏没漏鉴权」的地基断言。
ck A1-status '"ok":true' "$(curl -s $B/status)"
ck A1-plans '"success":true' "$(curl -s $B/api/plans)"
# ---------- A1q ★ #42 探针拆分（P2 技术债：存活/就绪语义必须分开）----------
# /livez 恒 200：依赖故障时重启进程救不回来，反而把可降级自愈的实例反复杀掉；
# /readyz 真探依赖：库不可达必须 503 并点名失败方，否则上游摘不掉这个实例（扣费会各副本各算）。
ck A1q-livez '"status":"ok"' "$(curl -s $B/livez)"
ck A1q-readyz '"status":"ready"' "$(curl -s $B/readyz)"
# 就绪判定只回粗粒度状态词：探针匿名可达，DB 报错原文（主机/端口/文件路径）不得外泄
# 下面几处「反向断言」都写成 if grep … 而不用 ck：ck 的语义是「命中即绿」，
# 反锁要的正是「命中即红」，只能手工累加 PASS/FAIL（交替一律 grep -E，理由见文件头 ck 注释）。
READYZ_BODY=$(curl -s $B/readyz)
if echo "$READYZ_BODY" | grep -qE 'postgres://|127\.0\.0\.1:5432|no such file|dial tcp'; then
  FAIL=$((FAIL+1)); echo "FAIL|A1q-readyz-no-topology-leak|readyz 出参含内网拓扑原文"
else PASS=$((PASS+1)); echo "PASS|A1q-readyz-no-topology-leak"; fi
# 反向断言：/readyz 不能是永远 200 的假探针（停掉依赖后必须能翻红）——
# 这里用「store 字段真实存在且取值为词」证明它确实做了判定，而不是硬编码 status。
ck A1q-readyz-probes-store '"store":"(ok|unreachable|not_initialized)"' "$READYZ_BODY"
# ★ P2-6（2026-09-18）/pricing 归一断言：Go 服务端渲染版已删除，直连应回退 SPA 壳（含 id="root"）；
#   并反向断言旧内嵌页（<title>定价 - 能言</title> 直出 HTML）不再出现，防止双实现回归。
PRICING_HTML=$(curl -s $B/pricing)
ck A1-pricing-spa-shell 'id="root"' "$PRICING_HTML"
if echo "$PRICING_HTML" | grep -qE '定价 - 能言'; then FAIL=$((FAIL+1)); echo "FAIL|A1-pricing-go-page-gone|Go版pricing未清除"; else PASS=$((PASS+1)); echo "PASS|A1-pricing-go-page-gone"; fi
ck A1-register-config '"success":true' "$(curl -s $B/api/auth/register-config)"
ck A1-langs 'kb_langs' "$(curl -s $B/api/translation/langs)"

# ---------- A1b 销售留资（★ P1-3：匿名 POST /api/lead，落 feedbacks lead 通道） ----------
# 先清本 IP 的留资限流窗口：rate_limits 持久化跨运行，重跑套件时不能被上一轮的 5s 间隔卡住
dbq "DELETE FROM rate_limits WHERE scope LIKE 'guard_%' AND key LIKE 'lead:%'" >/dev/null 2>&1
dbq "DELETE FROM feedbacks WHERE target_type='lead' AND content LIKE '%UATLead%'" >/dev/null 2>&1
ck A1b-lead-method '仅支持 POST' "$(curl -s $B/api/lead)"
ck A1b-lead-ok '"success":true' "$(curl -s $B/api/lead -H "$J" -d '{"company":"UATLead公司","email":"Sales@UAT-Lead.com","langs":"English,日本語","message":"请提供企业报价","source":"pricing"}')"
# 落库口径：公司原样、邮箱小写归一、来源走白名单（pricing 保留）——三处任一不符即断言红
ck A1b-lead-db '^1$' "$(dbq "SELECT COUNT(*) FROM feedbacks WHERE target_type='lead' AND content LIKE '%UATLead公司%' AND content LIKE '%sales@uat-lead.com%' AND content LIKE '%来源：pricing%'")"
ck A1b-lead-bademail '有效的联系邮箱' "$(curl -s $B/api/lead -H "$J" -d '{"company":"UATLead公司","email":"not-an-email"}')"
ck A1b-lead-nocompany '公司/团队名称' "$(curl -s $B/api/lead -H "$J" -d '{"company":"   ","email":"a@b.co"}')"
# 蜜罐命中：假成功但不落库（给 bot 成功信号、不给运营留垃圾）
ck A1b-lead-honeypot-fake '"success":true' "$(curl -s $B/api/lead -H "$J" -d '{"company":"Bot公司","email":"bot@spam.xyz","site":"http://seo-spam"}')"
ck A1b-lead-honeypot-nodb '^0$' "$(dbq "SELECT COUNT(*) FROM feedbacks WHERE target_type='lead' AND content LIKE '%Bot公司%'")"
# 同 IP 二次有效提交：5s 最小间隔限流（校验失败的请求不计数，故此处必是上一条触发）
ck A1b-lead-ratelimit '提交过于频繁' "$(curl -s $B/api/lead -H "$J" -d '{"company":"UATLead公司2","email":"x2@y.co"}')"

# ---------- A2 管理员登录 ----------
# 长度 >30 而不是解析 JWT：套件后面所有超管接口都要这个头，只要拿到「像样的 token」即可，
# 去 jwt 库验签会把断言绑死在签名算法上，收益不抵维护成本。
AT=$(tok $ADMIN_USER $ADMIN_PASS)
[ ${#AT} -gt 30 ] && { PASS=$((PASS+1)); echo "PASS|A2-admin-login(${#AT}b)"; } || { FAIL=$((FAIL+1)); echo "FAIL|A2-admin-login|AT=[$AT]"; }
AH="Authorization: Bearer $AT"
# /me 要能看出管理员身份：前端后台入口（roleLevelSafe(user.role)）与 e2e 的超管用例都按这个
# 字段判权，登录返回里掉了 role 就等于整条后台链路对前端不可达。
ck A2-admin-me 'admin|super' "$(curl -s $B/api/auth/me -H "$AH")"

# ---------- A3 注册 / 重名 / 错误密码 / 协议 ----------
ck A3-register-A '"success":true' "$(reg uatuser_a uatpass123 uatcorpA UAT公司A uat_a@test.com)"
T1=$(tok uatuser_a uatpass123); H1="Authorization: Bearer $T1"
ck A3-register-B '"success":true' "$(reg uatuser_b uatpass123 uatcorpB UAT公司B uat_b@test.com)"
T2=$(tok uatuser_b uatpass123); H2="Authorization: Bearer $T2"
# 唯一性分两条测：注册接口这条带重复的「用户名 + 租户码」，期望词把两种拦截形态都兜住
# （租户创建失败 / UNIQUE / 已存在）；只想单看「用户名占用」这一条约束，必须绕开注册流程
# 从管理端直插同一个租户（A3-dup-username），否则结果由先到那道校验决定、指不到具体列。
# AID 现查自库：租户 id 取决于建号顺序，写死会在种子数据变化后打到错的租户上（那就成了插新号）。
ck A3-dup-tenantcode '租户创建失败|UNIQUE|已存在' "$(reg uatuser_a uatpass123 uatcorpA 重复 uat_dup@test.com)"
AID=$(dbq "SELECT id FROM users WHERE username='uatuser_a' LIMIT 1" | tr -d '[:space:]')
DUP_PAYLOAD="{\"username\":\"uatuser_a\",\"password\":\"uatpass123\",\"display_name\":\"重复\",\"role\":\"user\",\"tenant_id\":$AID}"
ck A3-dup-username '已存在|占用|exists' "$(curl -s $B/api/admin/users/create -H "$AH" -H "$J" -d "$DUP_PAYLOAD")"
ck A3-bad-login '密码|失败|incorrect|invalid|UNAUTHORIZED' "$(curl -s $B/api/auth/login -H "$J" -d '{"username":"uatuser_a","password":"wrongpass"}')"
ck A3-no-agree '同意|协议' "$(curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_x","password":"uatpass123","code":"uatcorpD","name":"X","email":"uat_x@test.com"}')"

# ---------- A3lang 后端语言识别（★ 2026-09-24 〇-S #12） ----------
# 语种判定与 JSON 提示改写的实现口径在 internal/i18n + lang_middleware.go，
# 本段是「真实接口面」级闸门：withLang 接线被挪位、词条键被改坏，这里都会红。
# 刻意避开登录端点——失败计数限流（5 次/5 分钟）会把后面整条套件的 tok() 连带锁死；
# lead 校验失败不计提交限流、404 兜底无状态，是两条零副作用的探针。
# 无头请求必须保持中文：这是本套件全部存量断言的兼容红线（翻成英文则全线连坐）。
LEAD_BAD='{"company":"   ","email":"a@b.co"}'
ck A3lang-zh-default '"请填写公司/团队名称"' "$(curl -s $B/api/lead -H "$J" -d "$LEAD_BAD")"
ck A3lang-en '"Company/team name is required"' "$(curl -s $B/api/lead -H "$J" -H 'X-App-Lang: en' -d "$LEAD_BAD")"
ck A3lang-ja-to-en 'Company/team name is required' "$(curl -s $B/api/lead -H "$J" -H 'X-App-Lang: ja' -d "$LEAD_BAD")"
ck A3lang-zhhant-stays-zh '"请填写公司/团队名称"' "$(curl -s $B/api/lead -H "$J" -H 'X-App-Lang: zh_hant' -d "$LEAD_BAD")"
# ③ 类内联 error 键（spa 404 兜底写法）同样随语种改写；英文码值不在词条表必须原样
ck A3lang-error-field-en '"error":"API endpoint not found"' "$(curl -s -H 'X-App-Lang: en' $B/api/uat-lang-probe-missing)"
ck A3lang-error-field-zh '"error":"接口不存在"' "$(curl -s $B/api/uat-lang-probe-missing)"

# ---------- A3p 职业角色（2026-09-19：job_role 绑用户不绑企业 + persona 角色包） ----------
# ① 出厂字典：PersonaMigrate 种入 8 角色，公开接口与注册配置必须能读到（注册下拉数据源）
ck A3p-personas-public '"success":true' "$(curl -s $B/api/register/personas)"
ck A3p-personas-seeded 'sales' "$(curl -s $B/api/register/personas)"
ck A3p-config-personas '"personas"' "$(curl -s $B/api/auth/register-config)"
# ② 个人注册带 job_role=sales → 落库 users.job_role，/api/auth/me 回读（个人分支同样生效）
ck A3p-register-role '"success":true' "$(curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_rp","password":"uatpass123","type":"personal","name":"角色测试","email":"uat_rp@test.com","agreed":true,"job_role":"sales"}')"
ck A3p-role-db 'sales' "$(dbq "SELECT job_role FROM users WHERE username='uatuser_rp' LIMIT 1")"
RP_T=$(tok uatuser_rp uatpass123); RP_H="Authorization: Bearer $RP_T"
ck A3p-role-me '"job_role":"sales"' "$(curl -s $B/api/auth/me -H "$RP_H")"
# ③ 转岗：本人可改（POST /api/me/job-role）；未知角色拒绝——后端按启用中 persona 字典校验
ck A3p-switch-role '"success":true' "$(curl -s $B/api/me/job-role -H "$RP_H" -H "$J" -d '{"job_role":"pm"}')"
ck A3p-switch-db 'pm' "$(dbq "SELECT job_role FROM users WHERE username='uatuser_rp' LIMIT 1")"
ck A3p-bad-role-reject '不存在|已停用' "$(curl -s $B/api/me/job-role -H "$RP_H" -H "$J" -d '{"job_role":"no_such_role"}')"
# ④ 注册带非法角色：静默忽略（建号成功、角色不落库），不阻断注册主链路
ck A3p-bad-reg-role-ignored '"success":true' "$(curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_rq","password":"uatpass123","type":"personal","name":"忽略测试","email":"uat_rq@test.com","agreed":true,"job_role":"no_such_role"}')"
RJ=$(dbq "SELECT COALESCE(job_role,'') FROM users WHERE username='uatuser_rq' LIMIT 1" | tr -d '[:space:]')
[ "$RJ" != "no_such_role" ] && { PASS=$((PASS+1)); echo "PASS|A3p-bad-reg-role-empty"; } || { FAIL=$((FAIL+1)); echo "FAIL|A3p-bad-reg-role-empty|got[$RJ]"; }

# ---------- A3L ★ F-17（2026-09-25 批E）注册界面语种落库 users.preferred_lang ----------
# 邮件语种跟随的前置地基：注册载荷 app_lang 优先、X-App-Lang 头兜底，12 码白名单外
# 一律落空串（=中文链路，与 job_role「非法值静默忽略不阻断注册」同口径）。
# 三条都过 dbq 直读库判等值：接口回 success 不代表语种真的存下来了。
ck A3L-register-lang-en '"success":true' "$(curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_len","password":"uatpass123","type":"personal","name":"语种EN","email":"uat_len@test.com","agreed":true,"app_lang":"en"}')"
PL_EN=$(dbq "SELECT COALESCE(preferred_lang,'') FROM users WHERE username='uatuser_len' LIMIT 1" | tr -d '[:space:]')
[ "$PL_EN" = "en" ] && { PASS=$((PASS+1)); echo "PASS|A3L-lang-payload(en)"; } || { FAIL=$((FAIL+1)); echo "FAIL|A3L-lang-payload(want=en|got=$PL_EN)"; }
# 头兜底：载荷不带 app_lang，只有 X-App-Lang: ja 头 → 同样落 ja（前端 authHeaders 恒带此头）
ck A3L-register-lang-hdr '"success":true' "$(curl -s $B/api/auth/register -H "$J" -H 'X-App-Lang: ja' -d '{"username":"uatuser_lja","password":"uatpass123","type":"personal","name":"语种JA","email":"uat_lja@test.com","agreed":true}')"
PL_JA=$(dbq "SELECT COALESCE(preferred_lang,'') FROM users WHERE username='uatuser_lja' LIMIT 1" | tr -d '[:space:]')
[ "$PL_JA" = "ja" ] && { PASS=$((PASS+1)); echo "PASS|A3L-lang-header(ja)"; } || { FAIL=$((FAIL+1)); echo "FAIL|A3L-lang-header(want=ja|got=$PL_JA)"; }
# 白名单外语种（zz）：建号必须照常成功，且语种落空串——脏值不得入库变成永久错语种
ck A3L-register-lang-bad '"success":true' "$(curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_lzz","password":"uatpass123","type":"personal","name":"语种ZZ","email":"uat_lzz@test.com","agreed":true,"app_lang":"zz"}')"
PL_ZZ=$(dbq "SELECT COALESCE(preferred_lang,'') FROM users WHERE username='uatuser_lzz' LIMIT 1" | tr -d '[:space:]')
[ -z "$PL_ZZ" ] && { PASS=$((PASS+1)); echo "PASS|A3L-lang-invalid-empty"; } || { FAIL=$((FAIL+1)); echo "FAIL|A3L-lang-invalid-empty|got=$PL_ZZ"; }

# ---------- A4 注册赠送余额 ----------
BAL1=$(curl -s "$B/api/billing/balance" -H "$H1")
ck A4-balance-shape 'points_available' "$BAL1"
echo "INFO|balance-userA|$BAL1"

# ---------- A5 模型路由指向 mock LLM ----------
# 本条是整套件的前置：不指向 mock_llm，后面所有翻译/扣费断言都会打到真实供应商。
# embed_* 一并指过去：知识库检索类接口在没有嵌入端点时是另一条失败路径，容易误判成产品缺陷。
MS=$(curl -s $B/api/admin/models/save -H "$AH" -H "$J" -d "{\"api_base\":\"${MOCK_LLM_URL:-http://127.0.0.1:8901}/v1\",\"api_key\":\"sk-mock-uat\",\"model\":\"mock-mt\",\"embed_api_base\":\"${MOCK_LLM_URL:-http://127.0.0.1:8901}/v1\",\"embed_api_key\":\"sk-mock-embed\"}")
ck A5-models-save '"success":true' "$MS"
ck A5-models-set '"set":true' "$(curl -s $B/api/admin/models -H "$AH")"
MG=$(curl -s $B/api/admin/models -H "$AH")
# ★ 密钥掩码回显（工程约定第三节）：管理台读模型配置时 api_key 必须以 **** 形式回显，
#   真密钥出现在这里就是「GET 接口泄明文」级缺陷，比翻译挂掉更严重，所以单独开一条锁。
#   期望值写成 {4} 而不是完整掩码串：掩码长度随实现变（前 4 后 4 等），只有「含 ****」不变。
echo "$MG" | grep -qE '\*\*\*\*' && { PASS=$((PASS+1)); echo "PASS|A5-mask"; } || { FAIL=$((FAIL+1)); echo "FAIL|A5-mask|$MG"; }

# ---------- A6 估价 ----------
ck A6-estimate '"success":true' "$(curl -s $B/api/translation/estimate -H "$H1" -H "$J" -d '{"text":"早上好，欢迎使用翻译助手平台进行文本翻译测试。","target_langs":["en"],"mode":"fast"}')"

# ---------- A7 聊天翻译 + 计量入账（usage_ledger 行增） ----------
# ★ 2026-09-21（#33 任务系统）：口径改为「净消耗」= 窗口前容量 + 窗口内新发放 − 窗口后容量。
# 原因有二：① 任务临时积分（有效期 3/7 天）到期早于体验台账，按「先到期先消耗」必然先被扣，
#         只看 trial 台账会把「消耗发生在任务台账上」误判成没扣费；
#       ② 本次 /api/chat 同时触发「每周发起翻译 +100 积分」自动发放，纯容量差值会被这笔
#         增量吃掉甚至翻正（实测 330000→359693）。任务发放台账的 total 单调不减，
#         用它补回增量即可还原真实消耗量（扣费本体仍由 B 阶段流水对账锁死）。
TID7=$(dbq "SELECT tenant_id FROM users WHERE username='uatuser_a'")
cap7(){ NOW7=$(date -u +%Y-%m-%dT%H:%M:%SZ); dbq "SELECT (SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$TID7 AND expires_at>'$NOW7') + (SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=$TID7)"; }
gtot7(){ dbq "SELECT COALESCE(SUM(total),0) FROM quota_grants WHERE tenant_id=$TID7 AND kind='task'"; }
Q1=$(cap7); G1=$(gtot7)
CH=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"早上好，欢迎使用翻译助手平台。","options":{"target_langs":["en"]}}')
ck A7-chat-ok 'TranslatedEN' "$CH"
sleep 5
Q2=$(cap7); G2=$(gtot7)
NET7=$(( Q1 + G2 - G1 - Q2 ))
[ "$NET7" -gt 0 ] && { PASS=$((PASS+1)); echo "PASS|A7-metering(净消耗$NET7|$Q1->$Q2 发放+$((G2-G1)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|A7-metering(净消耗$NET7|$Q1->$Q2 发放+$((G2-G1)))"; }
ck A7-usage-me '"success":true' "$(curl -s "$B/api/billing/usage/me" -H "$H1")"

# ---------- A7s ★ 即时翻译 SSE 收尾必须已落库（2026-09-22 E2E TF2 抓到的缺陷锁） ----------
# 为什么单独一条：/api/chat（非流式）响应前会 billing.Flush()，但界面走的是
#   /api/chat/stream —— 该路径曾全程不冲刷，计量留在内存缓冲等 2s ticker 落库，
#   而前端在收到 done 帧后只刷新一次余额条，于是稳定读到旧值（用户表现为
#   「翻译完余额/今日已耗不动，再操作一次才跳」，E2E TF2 直接翻红）。
# 断言口径：done 帧之后**不 sleep**、立刻读 /api/me/package 的 points_used_today 必须已增加
#   —— 一旦把 Flush 挪回 handler 末尾（done 帧之后），这里就会重新变红。
#   用「今日已耗」而非「余额」判定：发起翻译会同步发放每周任务积分，余额可能被补成正增量。
sleep 2
USED1=$(curl -s $B/api/me/package -H "$H1" | pv '.get("points_used_today", -1)')
SS=$(curl -s -N $B/api/chat/stream -H "$H1" -H "$J" --max-time 90 \
  -d "{\"message\":\"A7s 流式计量落库回归断言 $$-$RANDOM-$RANDOM，设备需在傍晚前送达。\",\"skill\":\"translation\",\"options\":{\"target_langs\":[\"en\"]}}")
ck A7s-stream-done '"type":"done"' "$SS"
USED2=$(curl -s $B/api/me/package -H "$H1" | pv '.get("points_used_today", -1)')
[ -n "$USED1" ] && [ -n "$USED2" ] && [ "$USED2" -gt "$USED1" ] \
  && { PASS=$((PASS+1)); echo "PASS|A7s-stream-metering-sync(今日已耗 $USED1->$USED2)"; } \
  || { FAIL=$((FAIL+1)); echo "FAIL|A7s-stream-metering-sync(今日已耗 $USED1->$USED2，done 帧后计量未即时可见)"; }

# ---------- A7t ★ F-29 后端半（2026-09-25 批D）超长对话文本必须在 SSE 头前拒 ----------
# 缺陷形态：上万字符的文本直接喂对话管线，LLM 上下游 90s+ 不返回，前端挂着流干等、
#   积分照扣。修复口径：handleChatStream 在写 sseHeaders **之前**按 []rune 判
#   chat_max_chars（运营策略键，默认 5000），超限走 writeError → 400 JSON
#   code=chat_text_too_long、文案引导改用翻译工单。
# 「头前拒」必须用 Content-Type 判：一旦先发过 SSE 头，错误体就只可能混在 text/event-stream
#   帧里——那种形态下浏览器根本不会按 JSON 处理，前端拿不到稳定错误码。
LT_MSG=$(python3 -c 'print("测"*6000)')
LT_CODE=$(curl -s -o /tmp/uat_lt_body.json -w '%{http_code}' $B/api/chat/stream -H "$H1" -H "$J" --max-time 30 \
  -d "{\"message\":\"$LT_MSG\",\"skill\":\"translation\",\"options\":{\"target_langs\":[\"en\"]}}")
[ "$LT_CODE" = "400" ] && { PASS=$((PASS+1)); echo "PASS|A7t-too-long-400"; } || { FAIL=$((FAIL+1)); echo "FAIL|A7t-too-long-400(want=400|got=$LT_CODE)"; }
ck A7t-too-long-code '"code":"chat_text_too_long"' "$(cat /tmp/uat_lt_body.json)"
ck A7t-too-long-msg '工单' "$(cat /tmp/uat_lt_body.json)"
LT_CT=$(curl -s -o /dev/null -D - $B/api/chat/stream -H "$H1" -H "$J" --max-time 30 \
  -d "{\"message\":\"$LT_MSG\",\"skill\":\"translation\",\"options\":{\"target_langs\":[\"en\"]}}" | grep -i '^content-type:' || true)
echo "$LT_CT" | grep -qiE 'application/json' \
  && { PASS=$((PASS+1)); echo "PASS|A7t-too-long-not-sse"; } \
  || { FAIL=$((FAIL+1)); echo "FAIL|A7t-too-long-not-sse|got[$LT_CT]（拒绝必须先于 SSE 头，见批D纪律）"; }
rm -f /tmp/uat_lt_body.json
# 反向对照：限内文本（<5000 字符）必须照常走 SSE 出 done 帧——否则上面三条是「全拒」假绿
CH_OK=$(curl -s -N $B/api/chat/stream -H "$H1" -H "$J" --max-time 90 \
  -d '{"message":"限内文本对照断言，设备需在傍晚前送达。","skill":"translation","options":{"target_langs":["en"]}}')
ck A7t-within-limit-done '"type":"done"' "$CH_OK"

# ---------- A8 余额不足硬闸（清零 userB 双台账，billing_enforced=1） ----------
# 两张台账都要清零：容量判定走的是「quota_grants 未过期余额 + balance_accounts 合计」，
# 只清一张仍会被另一张放过，硬闸就测不到了（这条正是历史上最常见的假绿成因）。
# 用 userB 而不是 userA：A 还要留给后面的扣费/支付断言，这里必须找个「陪葬」租户。
BID=$(dbq "SELECT tenant_id FROM users WHERE username='uatuser_b'")
dbq "UPDATE balance_accounts SET balance=0 WHERE tenant_id=$BID; UPDATE quota_grants SET \"left\"=0 WHERE tenant_id=$BID;"
CH2=$(curl -s $B/api/chat -H "$H2" -H "$J" --max-time 30 -d '{"message":"这是一条余额不足应当被拦截的翻译请求文本内容","options":{"target_langs":["en"]}}')
ck A8-insufficient-block '耗尽|不足|insufficient' "$CH2"
echo "INFO|A8-reply|${CH2:0:200}"

# ---------- A9 支付链路：下单→模拟支付→幂等→发票 ----------
# 顺序即业务不变量：下单拿 id → 模拟回调 → 查单 → 再拿这个 id 开票，四步共用同一个 OID。
# 金额取 3333 这种非整档值：整千数额在按档/取整实现里容易「巧合对得上」，掩盖换算偏差。
ORD=$(curl -s $B/api/pay/create -H "$H1" -H "$J" -d '{"points":3333,"channel":"mock"}')
ck A9-pay-create '"success":true' "$ORD"
OID=$(echo "$ORD" | pv '.get("order_id") or d.get("order",{}).get("id") or 0')
echo "INFO|order-id|$OID"
SIM_PAYLOAD="{\"order_id\":$OID}"
ck A9-pay-simulate '"success":true' "$(curl -s $B/api/pay/simulate -H "$H1" -H "$J" -d "$SIM_PAYLOAD")"
ck A9-pay-status-paid 'paid|success' "$(curl -s "$B/api/pay/status?order_id=$OID" -H "$H1")"
# 幂等锁：同一订单第二次 simulate 后，余额必须与第一次**完全相等**（不是「不再增加」）。
# 回调重复投递是支付侧最常见事故，只看增量会把「二次全额到账」漏成绿。
G1=$(dbq "SELECT balance FROM balance_accounts WHERE tenant_id=(SELECT tenant_id FROM users WHERE username='uatuser_a')")
curl -s $B/api/pay/simulate -H "$H1" -H "$J" -d "{\"order_id\":$OID}" >/dev/null
G2=$(dbq "SELECT balance FROM balance_accounts WHERE tenant_id=(SELECT tenant_id FROM users WHERE username='uatuser_a')")
[ "$G1" = "$G2" ] && { PASS=$((PASS+1)); echo "PASS|A9-pay-idempotent($G1==$G2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|A9-pay-idempotent($G1->$G2)"; }
ck A9-orders-list '"success":true' "$(curl -s "$B/api/billing/orders" -H "$H1")"
INV_PAYLOAD="{\"order_id\":$OID,\"title\":\"UAT测试发票\",\"tax_no\":\"TAX123456\"}"
ck A9-invoice '"success"' "$(curl -s $B/api/billing/invoices/create -H "$H1" -H "$J" -d "$INV_PAYLOAD")"

# ---------- A10 邀请裂变（个人注册 + ref 码） ----------
REF=$(curl -s "$B/api/referral/my" -H "$H1")
ck A10-referral-my '"success":true' "$REF"
CODE=$(echo "$REF" | pv '.get("ref_code","")')
echo "INFO|ref-code|$CODE"
# 重试 3 次、每次退避 2s：注册接口按 IP 有最小间隔限流（A1b 段同一套 guard），
# 套件跑到这里往往还在窗口内。成功即 break，最终判定交给下面那条 ck——
# 循环只吸收节流抖动，真失败（ref 码校验不过）会跑满 3 次然后照旧 FAIL，不会被掩盖。
for i in 1 2 3; do
  R3=$(curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"uatuser_c\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"C受邀\",\"email\":\"uat_c2@test.com\",\"agreed\":true,\"ref\":\"$CODE\"}")
  echo "$R3" | grep -q '"success":true' && break
  sleep 2
done
ck A10-reg-invite '"success":true' "$R3"
RE2=$(curl -s "$B/api/referral/my" -H "$H1")
ck A10-referral-ok '"success":true' "$RE2"
echo "INFO|ref-after|$RE2"

# ---------- A11 知识库：企业包 + 条目 + 检索 ----------
# 建包 → 加条目 → 查统计是链式的：PKGID 取自上一步响应并留 `or 0` 兜底，
# 取不到就带着 id=0 去加条目、A11-kb-add 当场红，报告把失败停在真正断掉的那一步。
KBP=$(curl -s $B/api/admin/kb-packages/create -H "$H1" -H "$J" -d '{"code":"uat_kb_pkg","name":"UAT企业知识包","pack_type":"tenant"}')
ck A11-kb-package-create '"success":true' "$KBP"
PKGID=$(echo "$KBP" | pv '.get("package",{}).get("id") or d.get("data",{}).get("id") or 0')
KB=$(curl -s $B/api/admin/kb-entries/add -H "$H1" -H "$J" -d "{\"package_id\":$PKGID,\"source_text\":\"登录\",\"target_lang\":\"en\",\"target_text\":\"Log In\",\"module\":\"uat\",\"remark\":\"UAT术语\"}")
ck A11-kb-add '"success":true' "$KB"
ck A11-kb-stats '"success":true' "$(curl -s "$B/api/translation/kb-stats" -H "$H1")"
# ★ P0-2（2026-09-18）反向断言：匿名不得读租户 KB 统计（旧实现落默认租户 1 泄漏）
C=$(curl -s -o /dev/null -w '%{http_code}' "$B/api/translation/kb-stats")
ck A11-kb-stats-anon-401 '^401$' "$C"

# ---------- A12 OpenAPI：密钥 + 同步翻译 + 错误密钥 ----------
# 这一段走的是对外开放面（Bearer <api_key>，不是会话 token），前端接第三方客户时就靠它，
# 鉴权口径与会话体系完全独立，所以必须单独成组断言。
AK=$(curl -s $B/api/apikeys/create -H "$H1" -H "$J" -d '{"name":"uat-key"}')
ck A12-apikey-create '"success":true' "$AK"
# KEY 明文只在创建响应里返回这一次（后端 note 亦声明「仅显示一次」），后续任何读接口都拿不到，
# 所以必须当场抓进变量、供本段三条开放接口断言复用。
KEY=$(echo "$AK" | pv '.get("api_key","")')
OT=$(curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer $KEY" --max-time 90 -d '{"text":"开放接口同步翻译测试","target_lang":"en"}')
ck A12-openapi-translate '"success":true' "$OT"
echo "INFO|openapi-translate|$OT"
ck A12-openapi-balance '"success":true' "$(curl -s "$B/openapi/v1/balance" -H "Authorization: Bearer $KEY")"
# 错误密钥这条是安全锁：伪造 key 必须被拒且给出可辨识的失败原因（invalid/无效），
# 不能退化成 200 + 空结果——那会把鉴权失效伪装成「翻译没内容」。
ck A12-openapi-badkey 'invalid|无效' "$(curl -s $B/openapi/v1/translate -H "$J" -H "Authorization: Bearer sk-bogus-key" -d '{"text":"x","target_lang":"en"}')"
# 规格文档只取前 400 字节：整份 openapi.json 体积大且会随接口增删而变，
# 这里要锁的是「对外文档端点活着且是 OpenAPI 格式」，不是文档内容。
ck A12-openapi-spec '"openapi"' "$(curl -s $B/openapi/v1.json | head -c 400)"

# ---------- A13 权限与租户隔离 ----------
# 本组两条 python 判定用的是「集合级不变量」：返回的每一行 tenant_id 都必须与首行一致。
# 用 grep 做不到（只能证明某处出现过本租户），也就漏掉了「混进别租户那一行」这种真实事故形态。
UADM=$(curl -s "$B/api/admin/users" -H "$H1")
echo "$UADM" | python3 -c 'import sys,json;d=json.load(sys.stdin);us=d.get("users",[]);print("OK" if us and all(u.get("tenant_id")==us[0].get("tenant_id") for u in us) else "BAD")' | grep -q '^OK' && { PASS=$((PASS+1)); echo "PASS|A13-users-scoped"; } || { FAIL=$((FAIL+1)); echo "FAIL|A13-users-scoped|$UADM"; }
# 无 token 必须被挡（401/未登录）：与 A1 那组「匿名必须可读」配成一对，
# 两边同时绿才说明鉴权边界落在正确的位置上，而不是整体放开或整体锁死。
ck A13-no-token-401 'success.*false|401|未登录' "$(curl -s $B/api/billing/balance)"
# 跨租户读单：用 B 的会话去查 A 的订单。期望词同时允许 403/404/「不存在」/「无权」——
# 具体回哪种不是契约，只要不是「200 且拿到别人的单」；钉死单一状态码会把方言级实现差异当回归。
ck A13-cross-tenant-order 'success":false|404|不存在|无权' "$(curl -s "$B/api/pay/status?order_id=$OID" -H "$H2")"
AUD=$(curl -s "$B/api/system/audit" -H "$H1")
echo "$AUD" | python3 -c 'import sys,json;d=json.load(sys.stdin);ls=d.get("logs",[]);print("OK" if ls and all(l.get("tenant_id")==ls[0].get("tenant_id") for l in ls) else "BAD")' | grep -q '^OK' && { PASS=$((PASS+1)); echo "PASS|A13-audit-scoped"; } || { FAIL=$((FAIL+1)); echo "FAIL|A13-audit-scoped|$AUD"; }

# ---------- A14 套餐订阅（包绑定用户A租户，mock 即付即到账） ----------
TAID=$(dbq "SELECT tenant_id FROM users WHERE username='uatuser_a' LIMIT 1")
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d "{\"tenant_id\":$TAID,\"code\":\"uat_paid_100k\",\"name\":\"UAT包月10万句\",\"ptype\":\"paid\",\"sentences\":100000,\"price_money\":99,\"duration_days\":30}" >/dev/null
SUB=$(curl -s $B/api/package/subscribe -H "$H1" -H "$J" -d '{"code":"uat_paid_100k"}')
ck A14-subscribe '"success"' "$SUB"
echo "INFO|subscribe|$SUB"
ck A14-me-package '"success":true' "$(curl -s "$B/api/me/package" -H "$H1")"

# ---------- A15 通知 / 反馈 ----------
ck A15-notifications '"success":true' "$(curl -s "$B/api/notifications" -H "$H1")"
ck A15-feedback '"success":true' "$(curl -s $B/api/feedback -H "$H1" -H "$J" -d '{"content":"UAT反馈：界面很赞","contact":"uat@a.com"}')"
ck A15-feedback-list-admin '"success":true' "$(curl -s "$B/api/feedback/list" -H "$AH")"

# ---------- A16 文件翻译（docx 对照表交付） ----------
# docx 用 python 现场拼最小包（三份必需部件：Content_Types / _rels / word/document.xml）：
# 不在仓库里塞二进制夹具，套件自解释、跨机器可复跑，也不会被 git-lfs/换行符改动破坏。
TMPD=$(mktemp -d)
python3 - "$TMPD/hello.docx" <<'EOF'
import sys, zipfile
zf = zipfile.ZipFile(sys.argv[1],'w')
zf.writestr('[Content_Types].xml','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>''')
zf.writestr('_rels/.rels','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>''')
zf.writestr('word/document.xml','''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>今天天气晴朗，适合外出散步。</w:t></w:r></w:p>
<w:p><w:r><w:t>智能翻译助手的文件翻译功能非常强大。</w:t></w:r></w:p>
<w:sectPr/></w:body></w:document>''')
zf.close()
EOF
FT=$(curl -s $B/api/translate -H "$H1" -F "file=@$TMPD/hello.docx" -F "target_langs=en" --max-time 120)
ck A16-file-translate '文件翻译完成|points_used' "$FT"
echo "INFO|file-result|${FT:0:260}"
# ---------- A16b/A16c ★ #65（2026-09-22 两步同做）：交付名清理 + 产物按上传件分目录 ----------
# A16b 交付文件名（对外可见的那一段）不得含内部落盘标记：15 位以上时间戳曾被当成文件名主干
#      一路带进 zip 条目与下载名（`1790..._e2e_m1_en.txt`）。断言只看 basename，不看目录成分
#      ——目录那一层**故意**保留落盘名（它才是「每上传件独享子目录」的唯一性来源）。
# A16c 产物必须落在 translated/<落盘名>/ 之下（父目录名不得就是 translated），否则同名原件
#      会跨工单/跨租户互相覆盖产物，output_artifacts 按 path 反查归属也会命中错行。
# 下面写成 if/elif/else 手工累加而不用 ck：本条有三态（没产物 / 名字脏 / 名字干净），
# 而 ck 只有「命中即绿」一态——最要紧的「A16 压根没产出 files」必须单独报出来，
# 否则空输入会被反向 grep 判成「干净」而假绿（这是反向断言最容易踩的坑）。
# 匹配内部时间戳用 grep -qE '[0-9]{15}'（ERE 量词），理由见文件头 ck 注释。
FILE_NAMES=$(printf '%s' "$FT" | python3 -c 'import sys,json,os;print("\n".join(os.path.basename(f) for f in (json.load(sys.stdin).get("files") or [])))' 2>/dev/null)
if [ -z "$FILE_NAMES" ]; then
  FAIL=$((FAIL+1)); echo "FAIL|A16b-deliverable-name-clean|结果里没有 files 列表（A16 未产出产物？）"
elif printf '%s' "$FILE_NAMES" | grep -qE '[0-9]{15}'; then
  FAIL=$((FAIL+1)); echo "FAIL|A16b-deliverable-name-clean|交付名含内部时间戳标记: $(printf '%s' "$FILE_NAMES" | tr '\n' ' ')"
else
  PASS=$((PASS+1)); echo "PASS|A16b-deliverable-name-clean($(printf '%s' "$FILE_NAMES" | tr '\n' ' '))"
fi
# 判据由 python 侧折算成一个词（subdir=ok / subdir=bad），ck 只认 ok；
# 顺带的好处是：JSON 形态再变、解析抛错，输出为空同样判红，不会退化成假绿。
ck A16c-artifact-per-upload-dir 'subdir=ok' "$(printf '%s' "$FT" | python3 -c '
import sys,json,os
ps=[p for p in (json.load(sys.stdin).get("files") or []) if p]
print("subdir=ok" if ps and all(os.path.basename(os.path.dirname(p))!="translated" for p in ps) else "subdir=bad")' 2>/dev/null)"
rm -rf "$TMPD"

echo "==A-PASS=$PASS FAIL=$FAIL=="

# ============================================================================
# B 阶段：运营策略引擎（计费因子流程引擎配置）+ P1/P2 回归（2026-09-05）
# 覆盖：ops policy 读取/保存、fast 免费、limit_chars 闸门、平台包订阅(P2)、
#       套餐月度重置、邀请奖励因子、企业成员邀请加入(P1)、推广时间窗覆盖
# ============================================================================
echo "=== B 阶段：运营策略引擎 / P1 / P2 回归 ==="
# 显式重查一次 TAID（A14 也查过同名变量）：B 段每条都按租户维度落库断言，
# 取值口径集中在段首，不靠上一段残留的变量往下传——中途增删 A 段用例不会悄悄改变 B 段的租户。
TAID=$(dbq "SELECT tenant_id FROM users WHERE username='uatuser_a' LIMIT 1")

# ---------- B1 运营策略读取/保存（平台级，超管） ----------
ck B1-ops-policy-get '"success":true' "$(curl -s $B/api/admin/ops/policy -H "$AH")"
# 这里的三档设置是为后面 B2/B3/B5/B6 预埋的夹具，不是产品默认值：
#   fast.charge=false → B2 的「免费仍计量不扣费」；fast.limit_chars=10 → B3 用短文本就能撞闸；
#   monthly_reset_limit=1 → B5 的「第二次重置必须被上限拦」；invite.reward_tokens=900000 → B6 的入账因子。
# 改动本段等于改后面四条锁的判据，务必一起复核。
# heredoc 用带引号的 <<'JSON'：策略串里一旦出现 $ 或反引号，非引号版会被 shell 展开，
# 送出去的就是残缺 JSON——红的是本段的「保存失败」，看不出策略本身有没有问题。
OPS_PAYLOAD=$(cat <<'JSON'
{"scope":"platform","policy":{"billing":{"mode_rules":{"fast":{"enabled":true,"charge":false,"limit_chars":10},"pro":{"enabled":true,"charge":true,"limit_chars":0}}},"package":{"monthly_reset_enabled":true,"monthly_reset_limit":1},"invite":{"enabled":true,"reward_tokens":900000}}}
JSON
)
ck B1-ops-policy-save '"success":true' "$(curl -s $B/api/admin/ops/policy/save -H "$AH" -H "$J" -d "$OPS_PAYLOAD")"

# ---------- B2 fast 免费（charge=false）：翻译成功、台账 biz_mode=fast、双桶余额不变 ----------
# 三条缺一不可：只查余额不变会把「压根没翻译成功」当成免费通过；
# 所以必须同时钉「结果回来了（TranslatedEN 是 mock 的固定出参）」+「台账按 fast 记了一行」。
B2B1=$(curl -s "$B/api/billing/balance" -H "$H1" | pv '.get("points_available") or 0')
CHF=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"测试文本。","options":{"target_langs":["en"],"mode":"fast"}}')
ck B2-fast-chat-free 'TranslatedEN' "$CHF"
B2B2=$(curl -s "$B/api/billing/balance" -H "$H1" | pv '.get("points_available") or 0')
[ -n "$B2B1" ] && [ "$B2B1" = "$B2B2" ] && { PASS=$((PASS+1)); echo "PASS|B2-free-no-deduct($B2B1==$B2B2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B2-free-no-deduct($B2B1->$B2B2)"; }
B2LED=$(dbq "SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$TAID AND biz_mode='fast'")
[ "$B2LED" -ge 1 ] && { PASS=$((PASS+1)); echo "PASS|B2-fast-ledger(biz_mode=fast rows=$B2LED)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B2-fast-ledger|rows=$B2LED"; }
# 对照腿：同一时刻 pro 仍要正常翻译成功。缺这条就分不清「fast 免费生效」与「整套计费都掉了」。
CHP=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"专业模式扣费测试。","options":{"target_langs":["en"],"mode":"pro"}}')
ck B2-pro-still-charge 'TranslatedEN' "$CHP"

# ---------- B3 fast.limit_chars 闸门：超长被拒（MODE_LIMIT_CHARS），pro 不受限 ----------
# 10 个字符这个门槛来自 B1 刚存进去的策略，不是产品默认值 ⇒ B1/B3 顺序强耦合，
# 文本长度也刻意压在 10 以上、几十以内：再长会先去撞别的输入上限，测不到这条闸门。
LONG="这是一段远远超过十个字符的快速模式超长测试文本内容"
CHF2=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 30 -d "{\"message\":\"$LONG\",\"options\":{\"target_langs\":[\"en\"],\"mode\":\"fast\"}}")
ck B3-fast-limit-block '输入上限' "$CHF2"
CHP2=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d "{\"message\":\"$LONG\",\"options\":{\"target_langs\":[\"en\"],\"mode\":\"pro\"}}")
ck B3-pro-unlimited 'TranslatedEN' "$CHP2"

# ---------- B4 P2 回归：平台包（tenant_id=0）可被任意租户订阅 ----------
# tenant_id=0 是「平台自营包」的哨兵值；回归点是别的租户（这里用 B）也能订。
# 旧缺陷是把租户过滤一律套上，导致平台包谁都订不到。
# 刻意复用 B（A8 刚被清零的那个小租户）：这里要的是「与 A 无关的另一个租户」，
# 顺带排掉「靠 A 段那笔充值才订得上」的巧合解释。
curl -s $B/api/admin/packages/create -H "$AH" -H "$J" -d '{"tenant_id":0,"code":"uat_plat_pkg","name":"UAT平台套餐","ptype":"paid","sentences":50000,"price_money":50,"duration_days":30}' >/dev/null
SUB2=$(curl -s $B/api/package/subscribe -H "$H2" -H "$J" -d '{"code":"uat_plat_pkg"}')
ck B4-platform-subscribe '"success"' "$SUB2"
echo "INFO|B4-platform-subscribe|$SUB2"

# ---------- B5 套餐月度重置：扣减→重置恢复→二次重置被上限拦截 ----------
# 重置没有可观测的「自动触发」时点，所以先把 grant 手工扣薄（-50000 且只动 >50000 的行，
# 保证仍有余额可减、也不会把别的 kind 带进来），再调管理端重置接口模拟「跨月」。
dbq "UPDATE quota_grants SET \"left\"=\"left\"-50000 WHERE tenant_id=$TAID AND kind='plan' AND \"left\">50000"
RST=$(curl -s $B/api/admin/billing/package/reset -H "$H1" -H "$J" -d '{}')
ck B5-reset-ok '"success":true' "$RST"
echo "INFO|B5-reset|$RST"
# 第二次必须被拒：上限就来自 B1 的 monthly_reset_limit=1。少这条反向锁，
# 「随时可重置」这种能把套餐刷爆的实现照样全绿。
RST2=$(curl -s $B/api/admin/billing/package/reset -H "$H1" -H "$J" -d '{}')
ck B5-reset-limit '上限|已达' "$RST2"

# ---------- B6 邀请奖励因子：invite.reward_tokens=900000 按新值入账 ----------
# 先造邀请人 E（个人号，企业号没有裂变入口）→ 取其 ref 码 → 造受邀人 D → 比对 E 的 trial 台账合计。
# 判据是「增量 ≥900000」而不是「恰好等于」：注册本身还会发新手体验额度，增量里必然叠加。
curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_e","password":"uatpass123","type":"personal","name":"E邀请人","email":"uat_e@test.com","agreed":true}' >/dev/null
TE=$(tok uatuser_e uatpass123); HE="Authorization: Bearer $TE"
ECODE=$(curl -s "$B/api/referral/my" -H "$HE" | pv '.get("ref_code","")')
ETID=$(dbq "SELECT tenant_id FROM users WHERE username='uatuser_e'")
E1=$(dbq "SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$ETID AND kind='trial'")
curl -s $B/api/auth/register -H "$J" -d "{\"username\":\"uatuser_d\",\"password\":\"uatpass123\",\"type\":\"personal\",\"name\":\"D受邀\",\"email\":\"uat_d@test.com\",\"agreed\":true,\"ref\":\"$ECODE\"}" >/dev/null
E2=$(dbq "SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=$ETID AND kind='trial'")
[ -n "$E1" ] && [ -n "$E2" ] && [ $((E2 - E1)) -ge 900000 ] && { PASS=$((PASS+1)); echo "PASS|B6-invite-factor(+$((E2-E1)))"; } || { FAIL=$((FAIL+1)); echo "FAIL|B6-invite-factor($E1->$E2)"; }

# ---------- B7 P1 回归：企业成员凭有效邀请码加入既有租户（role=user） ----------
# 三条落库断言（进了哪个租户 / 落成什么角色 / 码有没有被消费）都在库侧核，不看接口脸色：
# 「返回 success 但其实建了个新租户」或「成员落成 admin」这类缺陷只有查库才看得见。
curl -s $B/api/admin/invite-codes/create -H "$H1" -H "$J" -d '{"code":"JOINUAT001"}' >/dev/null
RF=$(curl -s $B/api/auth/register -H "$J" -d '{"username":"uatuser_f","password":"uatpass123","type":"enterprise","role_choice":"member","invite":"JOINUAT001","name":"F加入","email":"uat_f@test.com","agreed":true}')
ck B7-invite-join '"success":true' "$RF"
# 三个取值都过一遍 tr -d '[:space:]'：dbq 的输出末尾带换行（PG 侧还可能有回车），
# 不剥干净的话下面的字符串比较恒不等，红得完全看不出原因。
FTID=$(dbq "SELECT tenant_id FROM users WHERE username='uatuser_f'" | tr -d '[:space:]')
FROLE=$(dbq "SELECT role FROM users WHERE username='uatuser_f'" | tr -d '[:space:]')
FUSED=$(dbq "SELECT used FROM invite_codes WHERE code='JOINUAT001'" | tr -d '[:space:]')
[ "$FTID" = "$TAID" ] && { PASS=$((PASS+1)); echo "PASS|B7-joined-tenant($FTID==$TAID)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B7-joined-tenant(f=$FTID a=$TAID)"; }
[ "$FROLE" = "user" ] && { PASS=$((PASS+1)); echo "PASS|B7-joined-role(user)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B7-joined-role($FROLE)"; }
[ "$FUSED" = "1" ] && { PASS=$((PASS+1)); echo "PASS|B7-invite-marked-used"; } || { FAIL=$((FAIL+1)); echo "FAIL|B7-invite-marked-used($FUSED)"; }

# ---------- B8 运营时间窗：base fast 收费 + 窗口覆盖 fast 免费 → 窗口生效 ----------
# 先把 base 策略翻回「fast 收费」（覆盖 B1 设的免费），再压一个覆盖窗口把 fast 改回免费：
# 只有「base 收费 + 窗口免费」这个反向组合，才能证明余额不变是窗口带来的，而不是策略还没生效。
WPAYLOAD=$(cat <<'JSON'
{"scope":"platform","policy":{"billing":{"mode_rules":{"fast":{"enabled":true,"charge":true,"limit_chars":0},"pro":{"enabled":true,"charge":true,"limit_chars":0}}}}}
JSON
)
curl -s $B/api/admin/ops/policy/save -H "$AH" -H "$J" -d "$WPAYLOAD" >/dev/null
# 窗口取「Asia/Shanghai 的昨天 00:00 ~ 明天 23:59」，而不是写死日期：
# ① 跑套件的时刻必然落在窗内，任何时候都不会因跨零点/跨月而假绿；
# ② 窗口时刻按 ops/policy 的默认时区（Asia/Shanghai）判定，测试取同区日期才能对上；
#    在 UTC 机器上取本地日期会在午夜前后差一天，表现为「窗口没命中」的假红。
TODAY=$(python3 -c 'from datetime import datetime,timedelta;from zoneinfo import ZoneInfo;n=datetime.now(ZoneInfo("Asia/Shanghai"));print((n-timedelta(days=1)).strftime("%Y-%m-%d"),(n+timedelta(days=1)).strftime("%Y-%m-%d"))')
WS=$(echo "$TODAY" | cut -d' ' -f1); WE=$(echo "$TODAY" | cut -d' ' -f2)
WINDOW_PAYLOAD=$(printf '{"window":{"id":"uat_promo","name":"UAT推广期","start":"%s 00:00","end":"%s 23:59","priority":10,"overrides":{"billing":{"mode_rules":{"fast":{"charge":false}}}}}}' "$WS" "$WE")
curl -s $B/api/admin/ops/policy/window/save -H "$AH" -H "$J" -d "$WINDOW_PAYLOAD" >/dev/null
# 窗口的 overrides 只写 {"charge":false} 一项（不带 enabled / limit_chars）：
# 万一实现把缺省字段当成「关掉/清零」，紧接着那条 fast 翻译就拿不到 TranslatedEN，本段先红——
# 「窗口只做局部覆盖、其余沿用 base」正是这里要钉住的语义。
B8B1=$(curl -s "$B/api/billing/balance" -H "$H1" | pv '.get("points_available") or 0')
CHF3=$(curl -s $B/api/chat -H "$H1" -H "$J" --max-time 90 -d '{"message":"窗口覆盖测试。","options":{"target_langs":["en"],"mode":"fast"}}')
ck B8-window-fast-free 'TranslatedEN' "$CHF3"
B8B2=$(curl -s "$B/api/billing/balance" -H "$H1" | pv '.get("points_available") or 0')
[ -n "$B8B1" ] && [ "$B8B1" = "$B8B2" ] && { PASS=$((PASS+1)); echo "PASS|B8-window-override-no-deduct($B8B1==$B8B2)"; } || { FAIL=$((FAIL+1)); echo "FAIL|B8-window-override($B8B1->$B8B2)"; }

echo "==B-PASS=$PASS FAIL=$FAIL=="
echo "==TOTAL-PASS=$PASS FAIL=$FAIL=="
