#!/usr/bin/env bash
# ============ dispatch_probe_daily.sh · 职责说明 ============
# 把两条**原本只靠人拨**的派发排障链路接进一只每日闹钟（★ 2026-10-01 本批建立）：
#   ① scripts/dispatch_preflight.sh —— 开闸前的一致性门禁 G1~G5（G5 是唯一的开闸判据）；
#   ② cmd/fpdprobe —— 主站 Go 派发腿（fileproc.TryDispatch）的真机全往返探针。
#
# 为什么必须是**定时器**而不是"记得跑"（现网真事，不是推演）：
#   开闸（10-01）之后，"传输腿今天通不通"这一问的证据只有**人手工拨那一次**留下的读数。
#   而这条链路的失败形态偏偏是**静默降级**：TryDispatch 失败只打一条 WARN 就返回 false，
#   调用方回落到本地链，客户照样拿到能打开的 PDF（AGENTS §一·12：「有自动降级的链路，
#   产物能打开不算验收」）。于是"闸开着但从没真跑过一次"这类缺陷在界面上零症状，
#   只有主动拨一次探针才看得见——而"主动"这件事历史上每次都漏。
#   另外两道闸顶不了它：/api/health 的 dispatch=online 只证明 **probe 腿**（不搬文件），
#   预检 G5 走的是 **bash 自己拼的协议**（bash 绿≠Go 绿，09-29 坏的就是 Go 侧那两条裸 ssh/scp）。
#
# ★ 射程边界（与 translator-dispatch-expiry.timer **不同**，两件事不许合并）：
#   到期那只管"到期那天把闸关掉"（它会写 /etc、重启服务）；
#   **本脚本一行配置都不改、一个服务都不重启、绝不关闸**——它只做三件事：
#   读现值 → 拨两条只读探针 → 把结构化读数记账，红的时候发一条告警。
#   所以本脚本里出现 `systemctl stop/restart/disable`、`dispatch_revert.sh`、写 /etc 任何动作
#   都是**越界**（静态锁＝internal/fileproc/dispatch_probe_timer_test.go）。
#   理由很直接：一只"排障闹钟"如果顺手会关闸，那它每天就都在制造一次不可解释的停机窗口，
#   而运维会因为怕它乱动而把它关掉——闹钟一关，本批要堵的那个洞又开回去了。
#
# 判读口径（三条硬约定，都有对应的行为锁）：
#   1) **就绪判据不许吃零值**：/api/health 取不到、或响应里没有 dispatch 字段 ⇒ 判 red，
#      绝不"读不到就当没事"（AGENTS §一·12 同一口径；零值放行是本仓反复点名的死法）。
#   2) **G5 无样张判红不判跳过**：样张缺失时仍然跑预检（它会自己把 G5 判红），
#      探针那一腿跳过并记 reason=no_sample ⇒ 整体 red。
#      ⚠️ 别为了绿灯把这条放宽——"跳过还能全绿退 0"正是 09-29 那次事故的形状。
#   3) **派发闸关着＝无事可探**：health 回 dispatch=off ⇒ 记 probe_result=closed、退 0、**不发告警**。
#      这不是"静默跳过判据"：闸关着时传输腿本来就不该被拨，天天告警会把真正的回归钝化掉；
#      哪天重新开闸（dispatch_apply.sh），这一腿自动恢复监控，不需要人记得改闹钟。
#
# 退出码：0=探针通过 或 闸关着（closed）；1=任一腿判红（**只告警，不动配置**）。
#   ⚠️ 退 1 会被 systemd 记成 unit failed，这是**故意**的诚实读数：
#      `systemctl is-failed translator-dispatch-probe.service` 是除告警通道外的第二条可见面。
#
# ---------------------------------------------------------------- 安装（主站 root 执行）
#   # 1) 脚本落位（与 dispatch_revert.sh 同一目录口径）
#   install -m 0755 scripts/dispatch_probe_daily.sh /opt/translator/bin/
#   install -m 0755 scripts/dispatch_preflight.sh   /opt/translator/bin/   # 探针要调它；仓库 checkout 在别处也行，用 PREFLIGHT_SCRIPT 指过去
#   # 2) unit 落位
#   cp deploy/systemd/translator-dispatch-probe.{service,timer} /etc/systemd/system/
#   systemctl daemon-reload
#   systemctl enable --now translator-dispatch-probe.timer      # ← 就这一行，别只 enable 不 --now
#   # 3) 样张落位（**没有样张这只闹钟每天判红**，那是设计而非故障）
#   #    档位：≥ FILEPROC_DISPATCH_MIN_MB(20MiB) 且 ≥ FILEPROC_DISPATCH_MIN_PAGES(30)，
#   #    用真件，或用《1001 〇-AF》那批留证的合成件生成法（每页一句中文＋噪声图抬体积，不经模型）：
#   #      /opt/translator/.venv/bin/python3 make_probe_pdf.py /opt/translator/data/_dispatch_probe/probe.pdf 32
#   mkdir -p /opt/translator/data/_dispatch_probe
#   chown -R translator:translator /opt/translator/data/_dispatch_probe   # 探针以 translator 跑，要能读写
#   # 4) 自检（手工跑一次，别等明天 05:20）
#   systemctl start translator-dispatch-probe.service; systemctl status translator-dispatch-probe.service
#
# ---------------------------------------------------------------- 排障（读数怎么看）
#   journalctl -u translator-dispatch-probe.service -n 50      # 每次运行的全部结构化读数
#   journalctl -u translator-dispatch-probe.service --since today | grep -E 'probe_result='
#   读数本体（一行一个键，全部以 @@PROBE 前缀）：
#     dispatch=<off|online|degraded>          来自 /api/health 的**现值**（不是配置文件）
#     probe_result=<ok|red|closed>            最终结论；red 时同时打一条 LEVEL=ERROR 行
#     preflight_rc=… preflight_fail=…         预检退出码与 ❌ 计数（G1~G5 的失败项数）
#     probe_rc=… probe_verdict=…              fpdprobe 退出码与它自己的 verdict
#     reason=<关键字>                          红的具体档：health_unreadable / no_sample /
#                                             probe_binary_missing / runuser_unusable /
#                                             preflight_red / probe_red / probe_output_missing /
#                                             credentials_missing / preflight_script_missing
#   常见三条红各是什么：
#     ① reason=probe_red 且 probe_rc=2 ⇒ **静默降级复发**（产物能打开但 stdout 里没有远端路径）。
#        这是本闹钟存在的理由本身。看 fpdprobe 自己打的 WARN（journalctl -u translator | tail），
#        10-01 那次真机首跑就是这一档，根因是 header.argv[0] 被双写（〇-AF 补丁五）。
#     ② reason=preflight_red 且 preflight_fail≥1 ⇒ 环境一致性漂了（G1 脚本指纹 / G2 库版本 /
#        G3 字体族 / G5 传输腿）。修法在预检自己的报错行里（它会点名该跑 dispatch_sync.sh 还是装字体）。
#     ③ reason=no_sample ⇒ 样张没落位或被人清了。**别把闹钟关掉了事**，放一份够档的件才是正解。
#   告警通道：ALERT_INTAKE_URL（service 里已指到本机 /api/alerts/alertmanager）
#     + ALERT_TOKEN/ADMIN_TOKEN（来自 EnvironmentFile=-/etc/translator/secrets.env，
#     与「人工确认收款」同一管理凭证，**不需要任何新凭据**）。
#     通道不可用（未配 token／curl 不通）时**不改变退出码**，但 ERROR 行一定在日志里：
#     订阅面就按 `grep -E 'DISPATCH_PROBE_RED'` 兜底（journal 或 LOG_FILE）。
#   可改行为的环境变量（都在 unit 里以默认值运行；调试时用 env 覆盖，**不要**写进 unit）：
#     PROBE_SAMPLE / PREFLIGHT_SCRIPT / FPDPROBE_BIN / PROBE_RUN_AS / PROBE_LEG_TIMEOUT /
#     LOG_FILE / ENV_FILE / LOCAL_BASE / PROBE_KEEP_ARTIFACT
#     ⚠️ 这些口同时是**行为测试的靶面**（dispatch_probe_timer_test.go 靠它们在临时目录里
#        真跑成功支与失败支）——删掉任何一个口，对应的行为锁就只能测到恒真分支。
# =============================================================================
set -u
set -o pipefail

# ---------------------------------------------------------------- 可调路径（全部留 env 口）
# 为什么全留口：本脚本的判据是**行为**（红的时候到底告不告警、会不会顺手关闸），
# 静态 grep 锁不住"位置与条件"这类语义——同族教训见 dispatch_revert.sh 头注里那段 ★ 10-01。
# 留了口，Go 侧就能用假 systemctl／假 curl／假预检在 t.TempDir() 里真跑每一条分支。
# 生产 timer 的环境里没有这些变量，走的仍是下面这些现网真值。
ENV_FILE="${ENV_FILE:-/etc/translator/dispatch.env}"
LOCAL_BASE="${LOCAL_BASE:-http://127.0.0.1:8787}"
# LOG_FILE 默认空＝**journald 是唯一事实源**。本仓派发脚本一律只出 stdout（deploy_check.sh 也是拿
# journalctl 读回落计数），自建一套 .log 体系就会和 journal 漂移；运维要持久文件时再显式配这一项。
LOG_FILE="${LOG_FILE:-}"
FPDPROBE_BIN="${FPDPROBE_BIN:-/opt/translator/bin/fpdprobe}"
PROBE_SAMPLE="${PROBE_SAMPLE:-/opt/translator/data/_dispatch_probe/probe.pdf}"
PROBE_LANG="${PROBE_LANG:-en}"
# ★ 必须降到服务账号：私钥/known_hosts 的可读性都长在这个 uid 上，root 跑通了不代表服务跑得通
#   （09-30 开闸首跑"预检全绿、一单不派"就是这两句话之差）。置空是**测试口**，见文件头。
#   ⚠️ 这里刻意用 `${VAR-default}` 而**不是** `${VAR:-default}`：显式给空串是测试口的语义
#   （"就以当前 uid 跑"），用 `:-` 会把空串也顶回 translator，本机行为锁就只能测到 runuser 那一支。
PROBE_RUN_AS="${PROBE_RUN_AS-translator}"
PROBE_LEG_TIMEOUT="${PROBE_LEG_TIMEOUT:-1500}"
PROBE_KEEP_ARTIFACT="${PROBE_KEEP_ARTIFACT:-0}"
ALERT_INTAKE_URL="${ALERT_INTAKE_URL:-}"
ALERT_TOKEN="${ALERT_TOKEN:-${ADMIN_TOKEN:-}}"
ALERT_NAME="${ALERT_NAME:-DispatchProbeFailure}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# 预检脚本的落位依次找：显式指定 ＞ 与本脚本同目录 ＞ /opt/translator/bin。
# 不写"仓库 checkout 路径"是因为现网仓库位置会变，而预检在哪个目录不影响判据、只影响找不找得到靶子。
PREFLIGHT_SCRIPT="${PREFLIGHT_SCRIPT:-}"
if [ -z "$PREFLIGHT_SCRIPT" ]; then
  if [ -f "$SCRIPT_DIR/dispatch_preflight.sh" ]; then
    PREFLIGHT_SCRIPT="$SCRIPT_DIR/dispatch_preflight.sh"
  elif [ -f "/opt/translator/bin/dispatch_preflight.sh" ]; then
    PREFLIGHT_SCRIPT="/opt/translator/bin/dispatch_preflight.sh"
  else
    PREFLIGHT_SCRIPT=""
  fi
fi

# ---------------------------------------------------------------- 记账原语
# 一行一个键、统一 @@PROBE 前缀：现网排障要把读数原样贴进交接文档，
# 自由文案的日志只能靠人重读一遍，键值对才 grep 得动（fpdprobe 自己用 @@FPD 同一形态）。
emit() {
  local line="@@PROBE $*"
  printf '%s\n' "$line"
  if [ -n "$LOG_FILE" ]; then
    printf '%s\n' "$line" >>"$LOG_FILE" 2>/dev/null || true
  fi
}

# emit_raw：把整段腿输出贴进日志（加 "| " 前缀，好与读数行区分）。
# 收尾显式 return 0：本函数常在 set -o pipefail 下被调用，若最后一次 sed 恰好没匹配到任何行，
# 函数返回值就会把"输出为空"伪装成"脚本失败"（§一·7 那条「末句是雷」的同族）。
emit_raw() {
  local f="$1"
  if [ ! -f "$f" ]; then
    return 0
  fi
  sed 's/^/  | /' "$f"
  if [ -n "$LOG_FILE" ]; then
    sed 's/^/  | /' "$f" >>"$LOG_FILE" 2>/dev/null || true
  fi
  return 0
}

# count_mark：按 ERE 数行数。三条纪律都在这里，各有原因：
#   ① grep -E 而不是 BRE 的 `a\|b`（§一·7：精简实现的 grep 把 \| 当字面量，**静默 0 命中**＝恒真假绿）；
#   ② set -o pipefail 下 grep 无匹配退 1 会把整段命令替换炸掉，所以 `|| true` 收口；
#   ③ 取值先 tr -d 空白，别把 wc/grep 的空白当数字（§一·7 数值比对口径）。
count_mark() {
  local f="$1" pat="$2" n
  n="$(grep -cE "$pat" "$f" 2>/dev/null || true)"
  printf '%s' "$(printf '%s' "${n:-0}" | tr -d '[:space:]')"
}

# jget <json 串> <python 表达式（d 已绑定）>：取不到就回空串，调用侧一律按"空＝不可信"判红。
# 绝不在解析失败时抛栈——抛栈会让上层把"解析失败"读成"脚本崩了"，而这里的语义是"读数不合法"。
jget() {
  printf '%s' "$1" | python3 -c 'import sys, json
raw = sys.stdin.read()
try:
    d = json.loads(raw)
except Exception:
    print("")
    sys.exit(0)
try:
    print(eval(sys.argv[1], {"d": d}))
except Exception:
    print("")' "$2" 2>/dev/null || true
}

# fvp <文件> <key>：从 fpdprobe 的 `@@FPD key=value` 读数里取值（取不到回空串）。
fvp() {
  local f="$1" k="$2" line val
  line=""
  if [ -f "$f" ]; then
    line="$(grep -E "^@@FPD ${k}=" "$f" 2>/dev/null | tail -1 || true)"
  fi
  val="${line#*${k}=}"
  # ★ 行尾 CR 必须剥：留着它做等值比较会**恒假**（§一·7 同款踩坑，SSH 回传读数是重灾区）。
  #   顺带把行首的 `@@FPD key=` 骨架也切掉，只剩值本体。
  printf '%s' "$val" | tr -d '\r'
}

run_timeout() {
  local secs="$1"
  shift
  # 没有 timeout 的宿主（精简容器）就直接跑：这一档不影响判据，只是防止闹钟被一次卡死的 ssh 挂住。
  if command -v timeout >/dev/null 2>&1; then
    timeout "$secs" "$@"
  else
    "$@"
  fi
}

# ---------------------------------------------------------------- 告警（仓里既有事实，不新造通道）
# 通道＝deploy/restore_drill.sh 的 alert_intake 同一条收口：
#   POST /api/alerts/alertmanager（仅回环）＋ X-Admin-Token == cfg.AdminToken，
#   S9 侧落平台告警中心并推运营群（internal/api/s9_alerts.go）。
# 为什么是这一层：它是现网**已经在用**的通道（恢复演练每周日的失败就走它），
# 凭证已在 /etc/translator/secrets.env 里，不需要任何新凭据；
# 而邮件/webhook 那一类要新建凭据与订阅关系，本批不引入（引入不了就把"必须新凭据"当成缺口盖住）。
# 尽力而为的语义照抄 restore_drill：**通道故障不改退出码**——告警发不出去是告警的问题，
# 探针的红必须仍然红（否则一次收口 500 就能把这条腿的失败全部伪装成"处理过了"）。
alert_intake() {
  local summary="$1"
  if [ -z "$ALERT_INTAKE_URL" ]; then
    emit "alert_skipped=no_intake_url（订阅面请用 grep -E 'DISPATCH_PROBE_RED'）"
    return 0
  fi
  if [ -z "$ALERT_TOKEN" ]; then
    emit "alert_skipped=no_token（secrets.env 里取不到 ALERT_TOKEN/ADMIN_TOKEN）"
    return 0
  fi
  local now clean body
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  # description 里的引号/反斜杠/换行必须剥：这是**拼进 JSON 字符串**的自由文本，
  # 一份带双引号的读数就能把整条告警打成非法 JSON（非法 JSON＝收口侧 400＝告警静默消失）。
  clean="$(printf '%s' "$summary" | tr -d '\n\r"\\' | cut -c1-300)"
  body="$(printf '{"alerts":[{"status":"firing","labels":{"alertname":"%s","severity":"critical"},"annotations":{"summary":"派发传输腿日探针失败","description":"%s"},"startsAt":"%s"}]}' "$ALERT_NAME" "$clean" "$now")"
  if curl -s -m 5 -XPOST "$ALERT_INTAKE_URL" -H "Content-Type: application/json" -H "X-Admin-Token: $ALERT_TOKEN" -d "$body" >/dev/null 2>&1; then
    emit "alert=intake_posted"
  else
    emit "alert=intake_failed（请人工跟进；日志行仍在）"
  fi
  return 0
}

# red_exit：统一的"红 ⇒ 记 ERROR 行 ⇒ 尽力告警 ⇒ 退 1"出口。
# 为什么收成一个函数而不是在各判红点各写三行：ERROR 关键字行与告警必须**永远同时出现**——
# 漏一处的形态就是"日志看着红了但没人知道"，那正是本批要堵的"只靠人拨/只靠人看"。
red_exit() {
  local reason="$1" summary="$2"
  emit "probe_result=red reason=$reason"
  emit "LEVEL=ERROR DISPATCH_PROBE_RED $summary"
  alert_intake "$summary"
  exit 1
}

REASONS=""
add_reason() {
  # 去重：同一条根因会让两条腿各点一次名（凭据缺失最典型），
  # reason 串里重复一项会被读成"两个独立缺陷"，排障的人就去找第二个不存在的洞。
  case ",$REASONS," in
    *",$1,"*) return 0 ;;
  esac
  if [ -z "$REASONS" ]; then
    REASONS="$1"
  else
    REASONS="$REASONS,$1"
  fi
  return 0
}

# ---------------------------------------------------------------- LOG_FILE 可用性（不可用就退回 journal）
# 为什么要显式判一次：emit 里的写文件是 `|| true`（不能因为日志面挂了就把探针打死），
# 于是"配了 LOG_FILE 却一个字节都没落"在现网**不会有任何症状**——
# 运维以为持久文件在，实际只有 journal。这里出一行 log_written=yes 作为正对照，
# 行为锁（dispatch_probe_timer_test.go 场景 ①）就是盯这一行，缺了它 ⇒ 持久面是空的。
LOG_ACTIVE=0
if [ -n "$LOG_FILE" ]; then
  LOG_DIR="$(dirname "$LOG_FILE")"
  if [ ! -d "$LOG_DIR" ]; then
    mkdir -p "$LOG_DIR" 2>/dev/null || true
  fi
  if ( : >>"$LOG_FILE" ) 2>/dev/null; then
    LOG_ACTIVE=1
  else
    printf '@@PROBE log_written=no（LOG_FILE 不可写，退回 journald；判据不受影响）\n'
    LOG_FILE=""
  fi
fi

# ---------------------------------------------------------------- 凭据现值（env 文件只读，绝不写）
DISPATCH_HOST=""
DISPATCH_KEY=""
ENV_FILE_PRESENT=0
if [ -f "$ENV_FILE" ]; then
  ENV_FILE_PRESENT=1
  # 与 fpdprobe 头注的用法逐字同形：set -a 让读到的键同时进环境，探针子进程才拿得到档位与凭据路径。
  # ⚠️ 这里**只 source 不修改**：写这个文件归 dispatch_apply.sh，本脚本碰它就越界了。
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
  DISPATCH_HOST="${FILEPROC_DISPATCH_HOST:-}"
  DISPATCH_KEY="${FILEPROC_DISPATCH_SSH_KEY:-}"
fi
# 允许显式 env 覆盖（同一件事只有一处兜底：显式值优先，缺了才问 env 文件现值）。
FPD_SSH="${FPD_SSH:-$DISPATCH_HOST}"
FPD_KEY="${FPD_KEY:-$DISPATCH_KEY}"
FPD_KNOWN_HOSTS="${FPD_KNOWN_HOSTS:-${FILEPROC_DISPATCH_KNOWN_HOSTS:-}}"

emit "run_started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if [ "$LOG_ACTIVE" = "1" ]; then
  emit "log_written=yes（结构化读数同时进 journal 与 LOG_FILE）"
fi
# ★ 凭据面只出 yes/no，**不出主机串与路径**：/api/health 那条"状态词不许夹带远端拓扑"的口径
#   同样适用于日志——这份日志会被原样贴进交接文档与聊天，主机名一旦外流就是拓扑外流。
cred_state="yes"
if [ "$ENV_FILE_PRESENT" != "1" ]; then
  cred_state="env_file_missing"
elif [ -z "$FPD_SSH" ] || [ -z "$FPD_KEY" ]; then
  cred_state="host_or_key_missing"
fi
emit "credentials_configured=$cred_state"

# ---------------------------------------------------------------- 第一道：闸的**现值**（不是配置文件）
# 为什么问 /api/health 而不是 grep env 里的 FILEPROC_DISPATCH=1：
#   env 写了 1 而服务没重启／drop-in 被别的 drop-in 覆盖／远端已到期被判 degraded——
#   这三种状态下"文件说的"和"进程真用的"不一样，而探针要盯的是**后者**。
#   revert/apply 两条脚本早就都用同一把尺子（读 health 的 dispatch 词），这里不另立第四把。
if ! command -v python3 >/dev/null 2>&1; then
  red_exit "python3_missing" "reason=python3_missing（health 与预检都要用 python3 解析，缺它无从判读）"
fi

HEALTH_RAW="$(curl -s --max-time 8 "$LOCAL_BASE/api/health" 2>/dev/null || true)"
HEALTH_RAW="$(printf '%s' "$HEALTH_RAW" | tr -d '\r')"
DISPATCH_WORD="$(jget "$HEALTH_RAW" 'd.get("dispatch","")')"
DISPATCH_WORD="$(printf '%s' "$DISPATCH_WORD" | tr -d '[:space:]')"
emit "dispatch=${DISPATCH_WORD:-<empty>}"

if [ -z "$DISPATCH_WORD" ]; then
  # 零值不放行：读不到／字段缺失＝不可信＝红（不许当成"应该没事吧"）。
  red_exit "health_unreadable" "reason=health_unreadable ⇒ $LOCAL_BASE/api/health 取不到 dispatch 读数（服务没起／二进制没接线／字段缺失）"
fi

case "$DISPATCH_WORD" in
  off)
    # 闸关着＝没有传输腿可探。记 closed（不是 ok，也不是 red）：既不制造假绿，也不制造每日噪声。
    emit "probe_result=closed note=dispatch_off_nothing_to_probe"
    emit "本轮未拨任何远端（闸关着）。重新开闸后本读数自动恢复为 ok/red，无需改动闹钟。"
    exit 0
    ;;
  online | degraded)
    ;;
  *)
    red_exit "health_unreadable" "reason=health_unreadable dispatch_word=$DISPATCH_WORD ⇒ dispatch 读数不是 off/online/degraded 三态词之一（二进制侧漂移）"
    ;;
esac

TMP="$(mktemp -d "${TMPDIR:-/tmp}/fpd-probe-daily.XXXXXX")"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

PF_OUT="$TMP/preflight.out"
PR_OUT="$TMP/probe.out"
PF_RC=0
PF_RAN=0
PF_FAIL=0
PF_PASS=0

# ---------------------------------------------------------------- 腿 1：预检 G1~G5（只读）
if [ -z "$PREFLIGHT_SCRIPT" ] || [ ! -f "$PREFLIGHT_SCRIPT" ]; then
  PF_RC=127
  add_reason "preflight_script_missing"
  : >"$PF_OUT"
  echo "找不到 dispatch_preflight.sh（用 PREFLIGHT_SCRIPT 指过去）" >"$PF_OUT"
elif [ "$cred_state" != "yes" ]; then
  # 主机/私钥路径都取不到时**不拨**：预检会在 ssh 之前就退 2，但那是一台都还没确认存在的机器，
  # 记成 credentials_missing 比记成一串 ssh 超时更有用（排障第一步该配哪个变量是明确的）。
  PF_RC=2
  add_reason "credentials_missing"
  : >"$PF_OUT"
  echo "凭据现值取不到（$cred_state），未拨远端" >"$PF_OUT"
else
  # ★ 样张缺不缺都跑：预检的 G5 在缺样张时**判红不判跳过**是既有硬口径（09-29 事故本体），
  #   这里绝不为了"让闹钟绿"把 FPD_SAMPLE 换成一份凑数的文件或不传——不传正是让它自己红。
  rc=0
  run_timeout "$PROBE_LEG_TIMEOUT" env \
    FPD_SSH="$FPD_SSH" FPD_KEY="$FPD_KEY" FPD_KNOWN_HOSTS="$FPD_KNOWN_HOSTS" \
    FPD_SAMPLE="${PROBE_SAMPLE}" bash "$PREFLIGHT_SCRIPT" >"$PF_OUT" 2>&1 || rc=$?
  # 上面必须用 `|| rc=$?` 收码：预检的非 0 是**读数**不是崩溃，pipefail 直接把脚本打死就没了后面的判读。
  PF_RC="$rc"
  PF_RAN=1
fi
PF_FAIL="$(count_mark "$PF_OUT" '❌')"
PF_PASS="$(count_mark "$PF_OUT" '✅')"
emit "preflight_rc=$PF_RC preflight_fail=$PF_FAIL preflight_pass=$PF_PASS"
emit_raw "$PF_OUT"
if [ "$PF_RAN" = "1" ] && { [ "$PF_RC" != "0" ] || [ "$PF_FAIL" != "0" ]; }; then
  # 双条判据而非一条：rc=0 但计数里仍有 ❌（有人把 exit 改成兜底退 0）与 rc≠0 但零 ❌
  # （崩在打印之前）都是"看着绿其实红"，任何一条命中就整体红。
  add_reason "preflight_red"
fi

# ---------------------------------------------------------------- 腿 2：fpdprobe（Go 派发腿真机往返）
PR_RC=0
PR_VERDICT=""
PR_EVIDENCE=""
SKIP_PROBE=""
if [ "$cred_state" != "yes" ]; then
  # 主机/私钥现值取不到 ⇒ 探针**也**拨不出去（它吃的是同一批 FILEPROC_DISPATCH_*）。
  # 与其让真 fpdprobe 报一串"派发未配置"的失败，不如在这一层就点名——理由已由腿 1 记过，
  # 这里刻意**不重复** add_reason：reason 串里出现两次同名会被读成两个独立缺陷。
  PR_RC=2
  SKIP_PROBE="credentials_missing"
elif [ ! -x "$FPDPROBE_BIN" ]; then
  PR_RC=127
  SKIP_PROBE="probe_binary_missing"
elif [ ! -s "$PROBE_SAMPLE" ]; then
  # ★ 判红不判跳过（本文件头约定 2）：探针这一腿确实无从执行，但整体必须红。
  PR_RC=2
  SKIP_PROBE="no_sample"
elif [ -n "$PROBE_RUN_AS" ] && { ! command -v runuser >/dev/null 2>&1 || ! runuser -u "$PROBE_RUN_AS" -- true >/dev/null 2>&1; }; then
  # 判的是"能不能降到服务账号"，不是"runuser 这个文件在不在"：
  # 非 root 宿主上 runuser 明明存在却每条都退非 0，只查存在性就会把探针真跑一次再以
  # "probe_red" 报出来——症状被推给下游，排障的人看不到"这一腿从来没以正确 uid 跑过"。
  # 后果不是"少一条读数"而是**读数全假**：私钥/known_hosts 的可读性都长在那个 uid 上
  # （09-30 开闸首跑"预检全绿、一单不派"就是这么藏的）。
  PR_RC=127
  SKIP_PROBE="runuser_unusable"
fi

if [ -z "$SKIP_PROBE" ]; then
  : >"$PR_OUT"
  # ★ 降级到服务账号跑（口径见上面 PROBE_RUN_AS 的注释）。PROBE_RUN_AS 置空是**测试口**：
  #   行为锁在本机以当前 uid 真跑 wrapper，现网永远走 runuser 那一支。
  rc=0
  if [ -n "$PROBE_RUN_AS" ]; then
    run_timeout "$PROBE_LEG_TIMEOUT" runuser -u "$PROBE_RUN_AS" -- "$FPDPROBE_BIN" "$PROBE_SAMPLE" "$PROBE_LANG" >"$PR_OUT" 2>&1 || rc=$?
  else
    run_timeout "$PROBE_LEG_TIMEOUT" "$FPDPROBE_BIN" "$PROBE_SAMPLE" "$PROBE_LANG" >"$PR_OUT" 2>&1 || rc=$?
  fi
  PR_RC="$rc"
fi
PR_VERDICT="$(fvp "$PR_OUT" 'verdict')"
PR_EVIDENCE="$(fvp "$PR_OUT" 'remote_evidence')"
PR_EXTRACT="$(fvp "$PR_OUT" 'extract_leg')"
PR_APPLY="$(fvp "$PR_OUT" 'apply_leg')"
if [ -n "$SKIP_PROBE" ]; then
  emit "probe_rc=$PR_RC probe_skip=$SKIP_PROBE"
  add_reason "$SKIP_PROBE"
else
  emit "probe_rc=$PR_RC probe_extract_leg=${PR_EXTRACT:-<empty>} probe_apply_leg=${PR_APPLY:-<empty>} probe_remote_evidence=${PR_EVIDENCE:-<empty>} probe_verdict=${PR_VERDICT:-<empty>}"
  emit_raw "$PR_OUT"
  # 三条等值判据，一条比一条难伪造：
  #   ① 退出码 0（探针自己已经把"档位不足／派发失败"收进非 0）；
  #   ② 输出里**必须有** verdict 行——空输出/被截断的输出绝不默认放行（同 §一·12 就绪判据口径）；
  #   ③ remote_evidence=true——产物能打开但没有远端路径＝这一单被降级链兜回本地跑了，
  #      正是本闹钟要抓的缺陷本体（探针自己会退 2，这里再钉一次，防只改一侧就静默）。
  if [ "$PR_RC" != "0" ]; then
    add_reason "probe_red"
  elif [ -z "$PR_VERDICT" ]; then
    add_reason "probe_output_missing"
  elif [ "$PR_EVIDENCE" != "true" ]; then
    add_reason "probe_red"
  fi
  # 探针产物是**主站侧**落在样张同目录的残骸（`<样张>.fpdprobe.pdf`）；每天一次不清就胀盘。
  # 只删这一个后缀、只删这一次运行的产物——不碰任何配置目录（越界关键字静态锁也盯着这条）。
  if [ "$PROBE_KEEP_ARTIFACT" != "1" ]; then
    ARTIFACT="${PROBE_SAMPLE}.fpdprobe.pdf"
    case "$ARTIFACT" in
      *.fpdprobe.pdf)
        if [ -f "$ARTIFACT" ]; then
          rm -f -- "$ARTIFACT" 2>/dev/null || true
        fi
        ;;
    esac
  fi
fi

# ---------------------------------------------------------------- 结论
if [ -n "$REASONS" ]; then
  red_exit "$REASONS" "preflight_rc=$PF_RC preflight_fail=$PF_FAIL probe_rc=$PR_RC dispatch=$DISPATCH_WORD"
fi
emit "probe_result=ok preflight_rc=$PF_RC preflight_fail=0 probe_rc=$PR_RC dispatch=$DISPATCH_WORD"
exit 0
