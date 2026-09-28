#!/usr/bin/env bash
# ============================================================================
# deploy/set_assist_admin_token.sh — AI 助手「管理 Token」一次落 env、三进程对齐（★ 2026-09-29）
#
# 要解决的问题：
#   主后台「🤖 AI 助手」面板报「服务在线，但主后台尚未配置管理 Token（当前来源：none）」，
#   于是面板里所有管理调用（含配 LLM 那三项）都无从下手。这把 Token 是**部署时自己定的共享口令**，
#   不是供应商 Key、也不是任何地方能查出来的现成值；内置默认口令早在 P0-2（2026-09-21）删除，
#   三级皆空时助手服务启动期直接拒绝起来。
#
# 本脚本走「零代码改动」那条路（用户 2026-09-29 选定）：
#   把同一把值一次性写进**三个进程**的 EnvironmentFile，此后面板顶部显示
#   「已配置（环境变量）」，超管进面板直接填 LLM 就行，**永远不必在任何输入框里碰 Token**。
#     · ai-assist        → /etc/ai-assist/secrets.env       （助手侧 guard 认它）
#     · translator       → /etc/translator/secrets.env      （主站面板发请求时带它）
#     · translator-demo  → /etc/translator-demo/secrets.env （演示站同上，另一套进程）
#   三条都是**必需**的：主服务读的是自己进程的 env（os.Getenv 在 exec 时固化），
#   所以改完文件必须重启那两个 web 单元才算数 —— 本脚本会重启，并在动手前交底停机。
#
# 为什么必须清掉助手库 configs.admin_token：
#   助手侧生效链是 configs.admin_token ＞ 启动快照（env / 主库 SQLite 桥接），库里那行（若有）
#   优先级**压过** env。只改 env 不清库 = 文件对、运行时错，最难查的一类假成功。
#
# 为什么不再往 system_config.assist_admin_token 存一份：
#   env 优先级压过库值（见 internal/api/admin_assist.go 的 effectiveAssistToken 与 env_overridden），
#   库里再存一份密文只会形成两个事实源，轮换时容易只改一边。走 env 就只留 env 这一份。
#
# 用法：
#   bash deploy/set_assist_admin_token.sh                        # ★ 默认只读预检，不动任何东西
#   bash deploy/set_assist_admin_token.sh --apply                # 三进程全落 + 重启三个服务（数秒停机）
#   bash deploy/set_assist_admin_token.sh --apply --assist-only  # 只动助手（面板仍会问 Token，留给下次）
#   bash deploy/set_assist_admin_token.sh --apply --yes          # 跳过交互确认（无人值守）
#   ASSIST_TOKEN_FILE=/path/to/token bash deploy/set_assist_admin_token.sh   # 换 Token 文件路径
#
# 前置（Token 在**本机**生成，值不进聊天/git/日志）：
#   umask 077; mkdir -p ~/.config/assist-token
#   openssl rand -hex 24 | tr -d '\n' > ~/.config/assist-token/assist_admin_token
#
# 密钥纪律（照 AGENTS §一·3 与《部署指南》P2-8 口径）：
#   · 值只走 scp 的传输通道，**不出现在命令行 argv**（本机与服务器两侧的 ps 都捞不到）、
#     不 echo、不落 git；远端上传件与探针临时件用后即删。
#   · 验收要「两边同值」时比的是 sha256 摘要前 12 位，不是明文。
#   · 一旦这把值在别处（聊天、截图、日志）出现过，重跑本脚本换一把即可（幂等）。
#
# ★ 写这个文件时必须守的两条机械纪律（都是它自己的历史踩坑）：
#   ① 下面两段远端脚本走的是**未加引号的 heredoc**（要本地展开路径变量），因此正文里
#      禁止出现反引号 —— 反引号会被当命令替换**在本机执行**，连注释里都不行；
#      远端自己的 $ 一律写成转义形式。
#   ② 中文标点紧跟变量名时必须用花括号（${VAR}，），bash 3.2 在部分 locale 下会吃尾巴。
# ============================================================================
set -euo pipefail

SERVER="${ASSIST_TOKEN_SERVER:-root@43.108.86.140}"   # 可用 env 覆盖（同 fetch_backups_local.sh 的 BK_SERVER 惯例）
SSH_PORT="${ASSIST_TOKEN_PORT:-28022}"
TOKEN_FILE="${ASSIST_TOKEN_FILE:-${HOME}/.config/assist-token/assist_admin_token}"
REMOTE_TMP="/root/.assist_admin_token.upload"

# 三个进程各自的 unit 名、EnvironmentFile 与本机探针地址（顺序即写入顺序）
ASSIST_UNIT="ai-assist";       ASSIST_SECRETS="/etc/ai-assist/secrets.env";       ASSIST_BASE="http://127.0.0.1:8790"
MAIN_UNIT="translator";        MAIN_SECRETS="/etc/translator/secrets.env";        MAIN_BASE="http://127.0.0.1:8787"
DEMO_UNIT="translator-demo";   DEMO_SECRETS="/etc/translator-demo/secrets.env";   DEMO_BASE="http://127.0.0.1:8789"
ASSIST_DB="/opt/ai-assist/data/assist.db"

APPLY=0; ASSIST_ONLY=0; YES=0
for a in "$@"; do
  case "$a" in
    --apply)       APPLY=1 ;;
    --assist-only) ASSIST_ONLY=1 ;;
    --yes|-y)      YES=1 ;;
    --help|-h)     sed -n '1,58p' "$0"; exit 0 ;;
    *)             ;;
  esac
done

# 掩码输出：只回长度与尾四位，绝不回全值。参数：$1=明文
mask_of() { printf 'len=%d tail=%s' "${#1}" "${1: -4}"; }

[ -f "$TOKEN_FILE" ] || {
  echo "✖ 本机没有 Token 文件：$TOKEN_FILE"
  echo "  先生成（值不进聊天）：umask 077 && mkdir -p ~/.config/assist-token && openssl rand -hex 24 | tr -d '\\n' > $TOKEN_FILE"
  exit 1
}

# 读本地 Token（不带结尾换行；容忍 CRLF）
TOKEN_RAW=$(tr -d '\r\n' < "$TOKEN_FILE")
if ! printf '%s' "$TOKEN_RAW" | grep -Eq '^[A-Za-z0-9_-]{16,64}$'; then
  echo "✖ Token 格式不合助手侧口径（要求 16–64 位 [A-Za-z0-9_-]）：$(mask_of "$TOKEN_RAW")"
  echo "  注：长度 0 通常意味着文件里有 BOM/空行，请重生成。"
  exit 1
fi

SCOPE="三进程（助手＋主站＋演示站）"
RESTART_LIST="ai-assist、translator、translator-demo"
if [ "$ASSIST_ONLY" = 1 ]; then SCOPE="仅助手进程"; RESTART_LIST="ai-assist"; fi
echo "== 本机 Token：$(mask_of "$TOKEN_RAW")（文件 ${TOKEN_FILE}，权限 $(ls -l "$TOKEN_FILE" | awk '{print $1}')）"
if [ "$APPLY" = 1 ]; then
  echo "== 目标：${SERVER}:${SSH_PORT}  范围：${SCOPE}  模式：★ APPLY"
  echo "⚠️  停机交底：会重启 ${RESTART_LIST}。两个 web 单元重启期间官网与后台短暂不可达；"
  echo "    演示单元要先加载知识库向量索引，实测约 3.4 秒才 bind —— 重启后头几次探到 000"
  echo "    属「起得慢」不是「起不来」（《部署指南》§十 已记过这条），脚本按 30 秒重试判。"
  if [ "$YES" != 1 ] && [ -t 0 ]; then
    printf '确认执行？输入 yes 继续：'
    read -r ans
    [ "${ans:-}" = "yes" ] || { echo "已中止（未动服务器任何东西）。"; exit 1; }
  fi
else
  echo "== 目标：${SERVER}:${SSH_PORT}  模式：只读预检（不动任何东西）"
fi

# ----------------------------------------------------------------------------
# 第一步：只读预检。全部只回「计数 / 长度 / 摘要 / 状态词」，一个密钥值都不回。
# ★ 纪律：本段**绝不引用 $TOKEN_RAW** —— 把它拼进 ssh 命令行，值就出现在本机进程表（ps）
#   与远端 shell 的 argv 里，等于「Key 进别处」、按红线得轮换。鉴权探针因此改用一把
#   **故意错的**假值：只问「管理面有没有在鉴权」，不问「认不认我这把」。
# ----------------------------------------------------------------------------
ssh -p "$SSH_PORT" -o ConnectTimeout=15 "$SERVER" "bash -s" <<EOF
set -uo pipefail
F='${ASSIST_SECRETS}'
DB='${ASSIST_DB}'
B='${ASSIST_BASE}'

# 读某进程**真实**环境里 ASSIST_ADMIN_TOKEN 的计数与摘要前 12 位（明文一律不外传）。
# 为什么读 /proc/PID/environ 而不是读文件：文件写了不等于进程吃到了（unit 没重启、
# 改错文件、drop-in 指错 EnvironmentFile 都会「文件对、运行时错」），
# 验收必须钉在运行时真实取值链上。
proc_tok() {
  local unit="\$1" pid n
  pid=\$(systemctl show -p MainPID --value "\$unit" 2>/dev/null || echo 0)
  if [ "\$pid" = "0" ] || [ ! -r "/proc/\$pid/environ" ]; then echo "进程不可读(主PID=\$pid)"; return; fi
  n=\$(tr '\\0' '\\n' < "/proc/\$pid/environ" | grep -c '^ASSIST_ADMIN_TOKEN=' || true)
  if [ "\$n" = "0" ]; then
    echo "未配"
  else
    echo "已配 摘要=\$(tr '\\0' '\\n' < "/proc/\$pid/environ" | sed -n 's/^ASSIST_ADMIN_TOKEN=//p' | tr -d '\\r\\n' | sha256sum | cut -c1-12)"
  fi
}

echo "-- 助手服务状态：\$(systemctl is-active ai-assist 2>&1)"
echo "-- 助手 secrets.env：\$(ls -l "\$F" 2>&1 | awk '{print \$1, \$3":"\$4, \$6, \$7, \$8}')"
echo "   ASSIST_ADMIN_TOKEN 生效行数（未注释才算，0＝env 侧根本没配）：\$(grep -c '^ASSIST_ADMIN_TOKEN=' "\$F" 2>/dev/null || true)"
echo "   ASSIST_ADMIN_TOKEN 注释占位行数：\$(grep -c '^#ASSIST_ADMIN_TOKEN=' "\$F" 2>/dev/null || true)"
echo "   ASSIST_LLM_* 生效行数（六键全配应回 6）：\$(grep -c '^ASSIST_LLM_[A-Z_]*=' "\$F" 2>/dev/null || true)"
if [ -f "\$DB" ]; then
  echo "   助手库 configs.admin_token：行数=\$(sqlite3 "\$DB" "select count(*) from configs where key='admin_token';" 2>/dev/null || echo '?') 长度=\$(sqlite3 "\$DB" "select coalesce(length(value),0) from configs where key='admin_token';" 2>/dev/null || echo '?')"
  echo "   助手库 llm_* 已配键数：\$(sqlite3 "\$DB" "select count(*) from configs where key like 'llm_%';" 2>/dev/null || echo '?')"
else
  echo "   助手库：无 \$DB"
fi
echo "-- 三份 env 文件是否就位："
for f in '${ASSIST_SECRETS}' '${MAIN_SECRETS}' '${DEMO_SECRETS}'; do
  if [ -f "\$f" ]; then
    echo "   \${f}：存在，同名行 \$(grep -c '^ASSIST_ADMIN_TOKEN=' "\$f" 2>/dev/null || true) 行，权限 \$(stat -c '%a %U:%G' "\$f" 2>/dev/null || echo '?')"
  else
    echo "   \${f}：不存在（该 unit 若在用，需先人工确认它的 EnvironmentFile 路径）"
  fi
done
echo "-- 三进程运行时真实取值（摘要一致才算「三边同一把」）："
echo "   ${ASSIST_UNIT}: \$(proc_tok ${ASSIST_UNIT})"
echo "   ${MAIN_UNIT}: \$(proc_tok ${MAIN_UNIT})"
echo "   ${DEMO_UNIT}: \$(proc_tok ${DEMO_UNIT})"
echo "-- 助手健康探针：\$(curl -s -m 5 "\$B/health" 2>/dev/null | head -c 60)"
echo "-- 管理面鉴权探针（故意用错的假值，期望 401；回 200＝管理面裸奔没鉴权，那才是事故）：\$(curl -s -m 5 -o /dev/null -w '%{http_code}' -H 'X-Assist-Admin: deliberately-wrong-probe-token' "\$B/api/assist/admin/config" 2>/dev/null || echo 000)"
EOF

echo "
判据怎么读：
  · configs.admin_token 长度>0 ⇒ 助手库里存着旧值，它会**压过** env —— APPLY 那一步连它一起清掉，属预期动作。
  · 三行「运行时真实取值」现在多半是「未配」或摘要互不相同 —— 那正是本次要修的状态。
    ⚠️ 若三行摘要**已经全部相同**，说明现网本来就配好了，本次 APPLY 等于换一把新的（旧值即作废）。
  · 鉴权探针回 401 = 管理面有鉴权（正常）；回 200 = **管理面裸奔**，先别配任何东西，来找我。
  · ASSIST_LLM_* 生效行数=0 且 助手库 llm_*=0 ⇒ LLM 六键还没落，Token 通了之后去面板「配置」页填。"

[ "$APPLY" = 1 ] || { echo "\n（预检模式，未做任何改动。确认无误后：bash deploy/set_assist_admin_token.sh --apply）"; exit 0; }

# ----------------------------------------------------------------------------
# 第二步：APPLY —— 上传 → 三份 env 落同一把 → 清助手库 configs → 重启 → 运行时闭环验证
# ----------------------------------------------------------------------------
echo "== [1/3] 上传 Token 到服务器临时文件（走 scp 通道，不进 argv）"
scp -P "$SSH_PORT" -q "$TOKEN_FILE" "${SERVER}:${REMOTE_TMP}"

echo "== [2/3] 远端落 env（${SCOPE}）＋清助手库旧行＋重启＋验证"
ssh -p "$SSH_PORT" "$SERVER" "bash -s" <<EOS
set -uo pipefail
TMP='${REMOTE_TMP}'
DB='${ASSIST_DB}'
B='${ASSIST_BASE}'
ONLY='${ASSIST_ONLY}'
UNITS='${ASSIST_UNIT} ${MAIN_UNIT} ${DEMO_UNIT}'
FILES='${ASSIST_SECRETS} ${MAIN_SECRETS} ${DEMO_SECRETS}'
if [ "\$ONLY" = "1" ]; then UNITS='${ASSIST_UNIT}'; FILES='${ASSIST_SECRETS}'; fi

[ -s "\$TMP" ] || { echo "✖ 上传件不存在或为空，中止（不动任何文件）"; exit 1; }
T=\$(tr -d '\\r\\n' < "\$TMP")
# ★ 形状复核：本地已校验过一次，这里再钉一遍，专防「上传件被换错内容 / 带了键名前缀」。
if ! printf '%s' "\$T" | grep -Eq '^[A-Za-z0-9_-]{16,64}\$'; then
  echo "✖ 上传件形状不合（长度 \${#T}，应为 16–64 位 [A-Za-z0-9_-]），中止——只接受裸 Token 值"
  exit 1
fi
TS=\$(date +%Y%m%d_%H%M%S)

# 往一份 EnvironmentFile 里落同一把（幂等：先剥同名行再追加）。
# ★ 这里**不能**写成 grep -v ... || true：grep 的 rc=1（无同名行，正常）与 rc≥2（文件读不了，
#   异常）会被一起吞掉，异常那支让 .new 成为空文件，紧接着的 mv 就把整份 secrets.env 清成
#   只剩一行 Token —— CORS、LLM base、其余密钥全丢，服务重启即挂。故显式收码，rc>1 一律中止。
put_env() {
  local f="\$1" rc=0 old new
  if [ ! -f "\$f" ]; then
    echo "  ⚠ 跳过 \${f}（文件不存在，未新建——新建会盖掉运维自己的路径约定，交人工确认）"
    return 2
  fi
  if ! cp -a "\$f" "\$f.bak.\${TS}"; then
    echo "✖ 备份 \$f 失败，中止（不动原文件）"; exit 1
  fi
  chmod 600 "\$f.bak.\${TS}" 2>/dev/null || true
  echo "  ✔ 备份 → \$f.bak.\${TS}"
  grep -v '^ASSIST_ADMIN_TOKEN=' "\$f" > "\$f.new" || rc=\$?
  if [ "\$rc" -gt 1 ]; then
    echo "✖ 读取 \${f} 失败（grep rc=\${rc}），中止且不覆盖（半成品已删）"; rm -f "\$f.new"; exit 1
  fi
  printf 'ASSIST_ADMIN_TOKEN=%s\\n' "\$T" >> "\$f.new"
  # 守恒不变式：新行数 ≥ 原行数（只删同名行、只加一行；任何「缩水」都说明写坏了）
  old=\$(wc -l < "\$f" | tr -d ' '); new=\$(wc -l < "\$f.new" | tr -d ' ')
  if [ "\$new" -lt "\$old" ]; then
    echo "✖ 行数守恒破了（\$f 原 \${old} 行 → 新 \${new} 行），中止且不覆盖"; rm -f "\$f.new"; exit 1
  fi
  if ! mv "\$f.new" "\$f"; then
    echo "✖ mv 失败，原文件未变（半成品 \$f.new 留在原地待人工看）"; exit 1
  fi
  chmod 600 "\$f"; chown root:root "\$f" 2>/dev/null || true
  echo "  ✔ \$f 已落 ASSIST_ADMIN_TOKEN（\${old}→\${new} 行，0600）"
}
SKIPPED=""
for f in \$FILES; do
  put_env "\$f"; rc=\$?
  # rc=2 是 put_env 的「跳过」信号（文件不存在，故意不新建）；rc=1 已在函数内直接中止整脚本
  if [ "\${rc}" = "2" ]; then SKIPPED="\${SKIPPED} \${f}"; fi
done
if [ -n "\${SKIPPED}" ]; then
  echo "  ⚠ 有 EnvironmentFile 不存在，本批未写入：\${SKIPPED}"
  echo "    ⇒ 对应站点的面板仍会显示「未配置」。先核那个 unit 实际用的是哪个文件："
  echo "      systemctl cat <unit> | grep EnvironmentFile"
  echo "    确认后把路径补进本脚本顶部的 *_SECRETS 变量再跑一次（脚本不新建：猜错路径会把运维自己的约定盖掉）。"
fi

# ★ 清掉助手库里那份（configs 优先级压过 env 启动快照；不清就是「改了 env 线上照旧认旧值」）
if [ -f "\$DB" ]; then
  N=\$(sqlite3 "\$DB" "select count(*) from configs where key='admin_token';" 2>/dev/null || echo 0)
  sqlite3 "\$DB" "delete from configs where key='admin_token';" 2>/dev/null || true
  echo "  ✔ 助手库 configs.admin_token 旧行清除：\${N} 行"
fi

shred -u "\$TMP" 2>/dev/null || rm -f "\$TMP"   # 上传件用后即删

if ! systemctl restart \$UNITS; then
  echo "✖ systemctl restart 失败（env 文件已改，服务仍是旧进程）⇒ 手工复查后再跑一次本脚本"
  exit 1
fi
echo "  ✔ 已重启：\$UNITS"

# 运行时真实取值验收：读 /proc/PID/environ，不读文件——文件对而进程没吃到是最常见的假成功。
# 空值必须显式喊「没吃到」，不能回一把**空串的 sha256**（e3b0c442… 看着像正常摘要，
# 实际是「这个进程根本没有这把变量」，读不出来的验收等于没验收）。
proc_digest() {
  local unit="\$1" pid v
  pid=\$(systemctl show -p MainPID --value "\$unit" 2>/dev/null || echo 0)
  if [ "\$pid" = "0" ] || [ ! -r "/proc/\$pid/environ" ]; then echo "不可读(主PID=\$pid)"; return; fi
  v=\$(tr '\\0' '\\n' < "/proc/\$pid/environ" | sed -n 's/^ASSIST_ADMIN_TOKEN=//p' | tr -d '\\r\\n')
  if [ -z "\$v" ]; then echo "✖ 没吃到（environ 里无此行：unit 未重启或 EnvironmentFile 指错）"; return; fi
  printf '%s' "\$v" | sha256sum | cut -c1-12
}
echo "-- 运行时摘要（各 unit 一行，一致＝同一把）："
for u in \$UNITS; do echo "   \${u}: \$(proc_digest "\$u")"; done

# 健康探针：000 先等再判（演示单元实测约 3.4 秒才 bind，见《部署指南》§十）
wait_live() {
  local url="\$1" name="\$2" i=0 code
  while [ "\$i" -lt 30 ]; do
    code=\$(curl -s -m 3 -o /dev/null -w '%{http_code}' "\$url" 2>/dev/null || echo 000)
    if [ "\$code" = "200" ]; then
      echo "  ✔ \${name} 200（第 \${i} 次探测后，计数从 0 起）"
      return 0
    fi
    i=\$((i + 1)); sleep 1
  done
  echo "  ✖ \${name} 30 秒内没到 200（最后 code=\${code}）⇒ journalctl -u 看原因"
  return 1
}
FAIL=0
wait_live "\$B/health" "助手 /health" || FAIL=1
if [ "\$ONLY" != "1" ]; then
  wait_live '${MAIN_BASE}/livez' "主站 /livez" || FAIL=1
  wait_live '${DEMO_BASE}/livez' "演示站 /livez" || FAIL=1
fi

# ★ 闭环：真拿这把 Token 拨一次助手管理面，期望 200。
#   走 curl 的 --config 文件（0600、用后即删），**不写成 -H 头直传变量** ——
#   后者会把明文送进远端进程 argv，同机任何 ps 都能捞到（本地侧同类问题已一并收口）。
CFG=\$(mktemp /tmp/.assist_probe.XXXXXX); chmod 600 "\$CFG"
printf 'header = "X-Assist-Admin: %s"\\n' "\$T" > "\$CFG"
C=\$(curl -s -m 5 -o /dev/null -w '%{http_code}' -K "\$CFG" "\$B/api/assist/admin/config" 2>/dev/null || echo 000)
shred -u "\$CFG" 2>/dev/null || rm -f "\$CFG"
if [ "\$C" = "200" ]; then
  echo "  ✔ 闭环：用这把 Token 拨助手管理面 → HTTP 200（助手运行时已认它）"
else
  echo "  ✖ 拨助手管理面 → HTTP \${C}（期望 200）⇒ env 没吃到或还有别的优先级，别继续往下配"
  FAIL=1
fi

if [ "\$FAIL" != "0" ]; then echo "✖ 有验收项未过，见上方红字"; exit 1; fi
echo "  ✔ 全部验收项通过"
EOS

# ----------------------------------------------------------------------------
# 第三步：收尾说明（这条路不需要在面板里输入任何东西）
# ----------------------------------------------------------------------------
cat <<EOF

== [3/3] 服务器侧已闭环。你现在只需要刷新管理后台那个页面：
   主后台 → 🤖 AI 助手 → 顶部显示「已配置（环境变量）」，
   Token 输入框会置灰并说明「env 占用了生效位」—— 那是设计行为，不是坏了，不必再填任何东西。
   本机那份 Token 仍在 ${TOKEN_FILE}（0600，仓库外）。留着是为了换机/重建时可复跑；
   不想留就执行：shred -u ${TOKEN_FILE}（之后要再动这份配置得重新生成并 --apply 一次）。

== 接着配大模型（这才是供应商那把 sk-… Key，与上面的 Token 无关）：
   同一面板 → 「配置」→ 🤖 LLM 接入 → 填 llm_base_url / llm_api_key / llm_model，有备用模型再填 llm_model_backup
   ★ llm_base_url 只填**服务根地址**（例 https://api.siliconflow.cn/v1），不要填成 https://api.siliconflow.cn/v1/chat/completions。
     助手侧会在它后面自己拼 /chat/completions（见 internal/assist/llm/llm.go 的 endpointURL）；填成整条接口地址时
     面板会报「连通失败：http 404: Not Found」——2026-09-29 生产首配就踩在这里，改回根地址即通。
   → 🔌测试连通 → 保存（热加载，不必重启）
   ⚠️ 面板上**没有独立的备用 Key 输入框**，这是设计不是缺项：管理端可写白名单只有上面这四项
      （见 backend-go/internal/assist/api/server.go 的 configKeyWhitelist——注释写明「api_key_backup 复用主 Key 故不单列」）。
      备用 Key 想独立成一把，只能走环境变量 ASSIST_LLM_API_KEY_BACKUP（一旦进 env 就压过后台、后台改不动）。
   验收：bash deploy/smoke_assist_llm.sh                # 链路 + 回答形状
         EXPECT_LLM=1 bash deploy/smoke_assist_llm.sh   # 硬判至少一条 source=llm
   ⚠️ 别把 ASSIST_LLM_* 写进 env 又指望管理台改得动：env 优先且不被管理台覆盖，
      env 侧六个变量名（BASE_URL／API_KEY／MODEL／MODEL_BACKUP／API_KEY_BACKUP／TIMEOUT）里
      配了哪几条就锁死哪几条，面板能改的只有前面那四项。LLM 这几键建议**只走面板**，别进 env。

== 轮换（将来想换一把）：重生成 Token 文件 → 再跑一次本脚本 --apply，幂等（旧值即刻作废）。
EOF
