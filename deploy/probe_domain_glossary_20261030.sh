#!/usr/bin/env bash
# ============================================================================
# deploy/probe_domain_glossary_20261030.sh —— 领域词表「上线前手拨一次」的执行体
#
# 为什么必须有这一支（《修改方案·第十二轮》§3.1 那条 ⚠️ 的落地）：
#   判域这一腿用的是**全局主模型**，而现网主模型实测是 `tencent/Hunyuan-MT-7B`——一个纯翻译模型。
#   它很可能答不出那份 JSON ⇒ 判域恒失败 ⇒ 词表恒空。而这条链是 **fail-open** 的：
#   界面照常翻译、零报错、日志里只有一行 reason。也就是说"开关开了、功能其实没生效"
#   这件事在现网**没有任何症状**（AGENTS §一·5「有自动降级的链路，产物能打开不算验收」）。
#   ⇒ 唯一诚实的做法是：开闸之后立刻拿真实样本拨一次，把日志里那一行读数拿出来看。
#
# 本脚本做四件事，全部可回收：
#   ① 用 opskey 给目标租户签一把**一次性** API Key（明文绝不打印、绝不落日志、跑完即 disabled）；
#   ② 打 `/openapi/v1/translate`（同步短文，走 text 链 ⇒ 会跑判域）两次：
#      第一次金融样本（应当判出 domain=finance 并注入词表），第二次通用样本（低置信 ⇒ 不注入，负向对照）；
#   ③ 从应用日志里按**字节偏移**只取本次新增的行，摘出 domain_glossary 读数；
#   ④ 回收 Key（disabled）并核对库里没有残留 active 的探针 Key。
#
# 用法（在部署机上跑；本机用 deploy/run_probe_domain_glossary_20261030.sh 送上去）：
#   TID=1 UID_=10001 bash deploy/probe_domain_glossary_20261030.sh
# 判绿：末行 PROBE_DONE=1（注意：判域**失败也算跑完**，那一档的读数是 reason=…，
#       要不要配 domain_detect_model 由人看读数决定，本脚本不替人做这个决定）。
# ============================================================================
set -uo pipefail

TID="${TID:-1}"
UID_="${UID_:-10001}"
B="${B:-http://127.0.0.1:8787}"
LOG="${LOG:-/opt/translator/log/translator.log}"
OPSKEY="${OPSKEY:-/opt/translator/bin/opskey}"
SECRETS="${SECRETS:-/etc/translator/secrets.env}"
DB="${DB:-langcross}"
KEY_NAME="probe-20261030"

[ -x "$OPSKEY" ] || { echo "BAD=找不到可执行的 ${OPSKEY}（先 scp 一份带过来的 linux 件）"; exit 1; }
[ -f "$SECRETS" ] || { echo "BAD=读不到 ${SECRETS}（opskey 依赖 DB_*/JWT_SECRET）"; exit 1; }
# opskey 只吃环境变量：secrets.env 里的 DB_DSN 等必须先进进程环境
set -a; . "$SECRETS"; set +a

q() { sudo -u postgres psql -d "$DB" -Atc "$1" 2>/dev/null; }

FIN_SAMPLE='本账户今日总盈亏 -11312.00 元，已实现 0.00 元，浮动盈亏 -11312.00 元，成交笔数 7 笔，0胜/0负。买入额 4716 元，卖出额 0 元，笔数按战法统计：manual 6 笔、动量 1 笔。形态战法（内置）含龙头战法、双响炮、N形超短、龙回头（中线），因子战法含波动突破战法；全局仓位留空或 0 表示使用全局单票金额，元/次按信号计费。关闭的战法信号不进入实盘链路，模拟盘不受影响；因子与形态类战法需先在自动研究页审批应用后才会出现在此处。'
GENERIC_SAMPLE='今天天气不错，我们下午一起去公园散步，顺便买一些水果和饮料回来。'

# ---- 0. 日志基线（按字节偏移取增量，不靠"近 N 分钟"凑）----
SZ0=$(stat -c %s "$LOG" 2>/dev/null || echo 0)

# ---- 1. 一次性 Key ----
KEY=$("$OPSKEY" -create -tid "$TID" -user "$UID_" -name "$KEY_NAME" -perms translate 2>/dev/null | tail -1)
if [ -z "${KEY:-}" ]; then echo "BAD=签发探针 Key 失败（opskey 没输出明文）"; exit 2; fi
KID=$(q "SELECT id FROM api_keys WHERE tenant_id=$TID AND name='$KEY_NAME' ORDER BY id DESC LIMIT 1;")
echo "OK   已签发一次性探针 Key（id=${KID:-?}，长度 ${#KEY} 位；明文不落屏、不落库外）"

call() { # <样本> <标签>
  local sample="$1" tag="$2" body cfg resp
  # 请求体与"带密钥的头"都走 0700 临时件：命令行 argv 里不出现密钥（ps 看得见），日志里也不出现
  body=$(mktemp); cfg=$(mktemp); chmod 700 "$body" "$cfg"
  python3 - "$sample" > "$body" <<'PY'
import json,sys
print(json.dumps({"text":sys.argv[1],"target_langs":["en"],"mode":"pro"},ensure_ascii=False))
PY
  {
    printf 'header = "Content-Type: application/json"\n'
    printf 'header = "Authorization: Bearer %s"\n' "$KEY"
    printf 'url = "%s/openapi/v1/translate"\n' "$B"
    printf 'data-binary = @%s\n' "$body"
    printf 'max-time = 120\n'
    printf 'silent\nshow-error\n'
  } > "$cfg"
  resp=$(curl -K "$cfg" 2>&1)
  rm -f "$body" "$cfg"
  echo "----- ${tag} 响应（截 300 字节，译文本身要看得见才叫验收）-----"
  printf '%s\n' "$resp" | head -c 300; echo
}

call "$FIN_SAMPLE" "A 金融样本（应判出域并注入词表）"
call "$GENERIC_SAMPLE" "B 通用样本（负向对照：低置信 ⇒ 不注入）"

# ---- 3. 只取本次新增日志里的 domain_glossary 行 ----
echo "----- 本次新增日志里的 domain_glossary 读数 -----"
tail -c +"$((SZ0 + 1))" "$LOG" 2>/dev/null | grep -a 'domain_glossary' | tail -12 | sed 's/^/  /'
N=$(tail -c +"$((SZ0 + 1))" "$LOG" 2>/dev/null | grep -ac 'domain_glossary' || true)
echo "READ domain_glossary 行数=$N"
if [ "${N:-0}" = "0" ]; then
  echo "NOTE 零行＝这一腿**根本没被走到**（三种可能：开关没生效 / 样本太短不判 / 该链不接词表）。"
  echo "     先核 system_config 里 domain_glossary_enabled.${TID}，再核样本长度（<40 rune 不判）。"
fi
echo "----- 词表落库读数（跨进程缓存里有没有真东西）-----"
q "SELECT 'rows='||COUNT(*)||' | 最近='||COALESCE(MAX(created_at),'-') FROM domain_profiles;"
q "SELECT '  样本: tid='||tenant_id||' | code='||code||' | conf='||confidence||' | 过期='||expires_at FROM domain_profiles ORDER BY created_at DESC LIMIT 3;"

# ---- 4. 回收 ----
if [ -n "${KID:-}" ]; then
  "$OPSKEY" -revoke "$KID" -tid "$TID" >/dev/null 2>&1 && echo "OK   探针 Key 已 disabled（id=${KID}）" \
    || echo "BAD  回收失败 ⇒ 请手工 opskey -revoke $KID -tid $TID"
fi
LEFT=$(q "SELECT COUNT(*) FROM api_keys WHERE name='$KEY_NAME' AND status='active';")
echo "READ 残留 active 探针 Key=${LEFT}（应为 0）"
KEY=""; unset KEY
echo "PROBE_DONE=1"
