#!/usr/bin/env bash
# ============================================================================
# deploy/check_assist_profile_face.sh —— 挂件访客画像的**现网只读探针**（★ 2026-10-31 补 G32）
#
# 它回答的问题只有一个：**画像那一跳在线上真走了主服务吗**。
# 为什么不能只看仓库（方案一 §8.2 的 G32 本体）：这条链最坏的形态是**闸门全绿、现网空转**——
#   仓库侧单测判的是「签名与验签等值」；Caddy 的 `/assist-api/*` 到底落 8787 还是 8790、
#   两侧 ASSIST_PROFILE_KEY 是否同值，**任何仓库判据都看不见**。而画像坏掉的形状是静默的：
#   验不过就按匿名处理（P-5 刻意不返 4xx），挂件照常回答、界面零症状。
#
# 判据（全只读：不建单、不改配置、不重启单元、不碰 caddy；只按挂件正常路径落会话消息）：
#   P1 登录（env 传的探针账号）拿 token —— 失败即 BAD
#   P2 现读 `/api/me/package` 的 `points_balance` 当**唯一基准 B**
#      （期望值现读现推，禁止写死数字——同一句"余额对得上"必须比的是那一刻的库，AGENTS §一·3）
#   P4 **正腿**：登录态经 `/assist-api` 问「我的余额还剩多少积分」，回答必须含 B
#      —— 只有画像真随这一跳进了提示词，模型才可能知道这个数
#   P5 反向对照一：**匿名**走同一条 `/assist-api` 问同一句，回答不许含 B
#   P6 反向对照二：**直连 assist 端口**（带 token）问同一句，回答不许含 B
#      —— 这一路物理上没有画像头。P4 红 + P6 绿 ＝「只改代码没改 Caddy」那个靶子
#   P7 assist 日志窗口内「访客画像未通过校验」WARN 计数：带头但验不过＝两侧密钥不一致或时钟漂移
#      （它比"回答里没数字"更准，能一句话指到成因）
#
# 三态（同仓内其它 face 脚本口径）：exit 0 全绿／1 有红项／**2 有 N 项没测到**（⏳）。
# 拿到 2 **既不是通过也不是产品缺陷**：模型答案受上游影响，复跑一次再判。
#
# 用法（在部署机上跑；凭据**只经 env**，本文件与任何文档都不许出现账号密码）：
#   PROBE_USER=<探针账号> PROBE_PASS=<密码> bash check_assist_profile_face.sh
#   可选 env：MAIN_BASE（默认 http://127.0.0.1:8787）／ASSIST_BASE（默认 http://127.0.0.1:8790）
#             JOURNAL_SINCE（默认 "5 min ago"，P7 的窗口起点）／CURL_MAX_TIME（默认 60s）
# 判绿只认末行 `ASSIST_PROFILE_FACE_ALL_OK=1`。
# ============================================================================
set -uo pipefail

MAIN_BASE=${MAIN_BASE:-http://127.0.0.1:8787}
ASSIST_BASE=${ASSIST_BASE:-http://127.0.0.1:8790}
CURL_MAX_TIME=${CURL_MAX_TIME:-60}
JOURNAL_SINCE=${JOURNAL_SINCE:-5 min ago}
PROBE_USER=${PROBE_USER:-}
PROBE_PASS=${PROBE_PASS:-}

FAIL=0
UNMEASURED=0
ok()   { echo "OK   $*"; }
bad()  { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
skip() { echo "⏳   $*"; UNMEASURED=$((UNMEASURED + 1)); }

# ---------------------------------------------------------------- --selftest
# 判据射程自证（本仓纪律：一条 face 判据必须能"装坏一次就被自己抓到"，否则它替旧口径说话）。
# 在 127.0.0.1 起一次**临时**挂件假上游（不落任何真实凭据、不碰现网），跑三种形态：
#   S1 画像在位   ⇒ FAIL 必须为 0（P4 绿、两条反向对照绿）
#   S2 画像没跟上 ⇒ P4 必须红（这就是"闸门绿、现网空转"那一档的机器可读形态）
#   S3 匿名也答得出余额 ⇒ P5 必须红（画像泄漏比不生效更坏，判据要分得开这两件事）
# 判绿只认末行 `PROFILE_FACE_SELFTEST_ALL_OK=1`。
write_fixture() {
  cat >"$1" <<'PY'
# 挂件画像探针的假上游（--selftest 专用；只监听 127.0.0.1 的临时端口，进程随自测结束即回收）
import json, os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

MODE = os.environ.get('FIX_MODE', 'profiled')   # profiled / no_profile / leak
BAL = '3210'                                     # 与 /api/me/package 的 points_balance 同值


class H(BaseHTTPRequestHandler):
    def _send(self, obj, code=200):
        b = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        if self.path.startswith('/api/me/package'):
            return self._send({'success': True, 'points_balance': int(BAL)})
        if 'greeting' in self.path:
            return self._send({'session': 's1', 'tok': 't1', 'greeting': '你好', 'chips': []})
        return self._send({'error': 'nf'}, 404)

    def do_POST(self):
        n = int(self.headers.get('Content-Length') or 0)
        self.rfile.read(n)
        auth = (self.headers.get('Authorization') or '').startswith('Bearer ')
        if self.path.startswith('/api/auth/login'):
            return self._send({'success': True, 'token': 'tk-selftest'})
        if 'chat' in self.path:
            via_main = self.path.startswith('/assist-api')
            knows = (MODE == 'leak') or (MODE == 'profiled' and via_main and auth)
            reply = ('您的余额是 ' + BAL + ' 积分') if knows else '这边看不到您的账户余额'
            return self._send({'reply': reply, 'source': 'llm'})
        return self._send({'error': 'nf'}, 404)

    def log_message(self, *a):
        pass


HTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
PY
}

run_selftest() {
  local dir script port out rc
  dir=$(mktemp -d) || { echo "BAD 自测建临时目录失败"; return 1; }
  script=$(cd "$(dirname "$0")" && pwd)/$(basename "$0")
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
  write_fixture "${dir}/fixture.py"

  local modes=() expect=() names=()
  modes=(profiled no_profile leak)
  expect=(pass P4 P5)
  names=(S1_画像在位 S2_画像没跟上 S3_匿名也答得出余额)
  local i all_ok=1
  for i in 0 1 2; do
    FIX_MODE="${modes[$i]}" python3 "${dir}/fixture.py" "${port}" >/dev/null 2>&1 &
    local pid=$!
    # 等就绪（最多 5s）：不等第一枪会撞 connection refused，然后被读成"这一跳没走完"
    local tries=0
    while [ "${tries}" -lt 25 ] && ! curl -s --max-time 1 -o /dev/null "http://127.0.0.1:${port}/api/assist/greeting?session=x"; do
      tries=$((tries + 1)); sleep 0.2
    done
    out=$(PROBE_USER=selftest PROBE_PASS=selftest \
          MAIN_BASE="http://127.0.0.1:${port}" ASSIST_BASE="http://127.0.0.1:${port}" \
          JOURNAL_SINCE="1 second ago" bash "${script}" 2>&1)
    rc=$?
    kill "${pid}" 2>/dev/null; wait "${pid}" 2>/dev/null
    case "${expect[$i]}" in
      pass)
        if [ "${rc}" = "1" ] || printf '%s' "${out}" | grep -q '^BAD'; then
          echo "BAD  ${names[$i]}：画像在位那一档仍报红（判据过敏）"; printf '%s\n' "${out}" | grep '^BAD' | head -3; all_ok=0
        else echo "OK   ${names[$i]}：零红项"; fi ;;
      *)
        if ! printf '%s' "${out}" | grep -q "^BAD.*${expect[$i]}"; then
          echo "BAD  ${names[$i]}：把画像弄坏/弄漏之后 ${expect[$i]} 没有判红 ⇒ 这条判据是空转的"; all_ok=0
        else echo "OK   ${names[$i]}：${expect[$i]} 判红（射程在位）"; fi ;;
    esac
  done
  rm -rf "${dir}"
  [ "${all_ok}" = "1" ] && { echo "PROFILE_FACE_SELFTEST_ALL_OK=1"; return 0; }
  echo "PROFILE_FACE_SELFTEST_ALL_OK=0"; return 1
}

if [ "${1:-}" = "--selftest" ]; then
  command -v python3 >/dev/null 2>&1 || { echo "P0 缺 python3 ⇒ 自测跑不了"; echo "PROFILE_FACE_SELFTEST_ALL_OK=0"; exit 2; }
  command -v curl >/dev/null 2>&1 || { echo "P0 缺 curl ⇒ 自测跑不了"; echo "PROFILE_FACE_SELFTEST_ALL_OK=0"; exit 2; }
  run_selftest; exit $?
fi

# ------------------------------------------------------------------ P0 前置
# 凭据缺失 ⇒ 停在最前面说明"为什么没跑"，而不是拿一条匿名请求把后面几条判成红。
if [ -z "${PROBE_USER}" ] || [ -z "${PROBE_PASS}" ]; then
  echo "P0 需要探针账号：PROBE_USER/PROBE_PASS 经 env 传入（本脚本不落凭据、不读配置文件、不打印 token）"
  echo "ASSIST_PROFILE_FACE_UNMEASURED=1"
  exit 2
fi
if ! command -v curl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  echo "P0 本机缺 curl 或 python3 ⇒ 一条都测不到"
  echo "ASSIST_PROFILE_FACE_UNMEASURED=1"
  exit 2
fi

FACE_RC=""; FACE_CODE=""; FACE_TEXT=""; FACE_RETRIED=0
# face_curl <方法> <url> [curl 头/参数...]：一次请求，把 rc／http_code／body(单行) 落到三个全局。
# 完整性判据（AGENTS §一·5 第⑤条）：rc≠0 或 code=000 ＝「这一跳没走完」⇒ 换 --http1.1 再拨一次；
# 4xx/5xx 与"内容不对"**一律不重拨**——把红摇成绿就是把缺陷藏起来。
face_curl() {
  local method="$1" url="$2"; shift 2
  local tmp code rc
  tmp=$(mktemp) || { FACE_RC=1; FACE_CODE=""; FACE_TEXT=""; return; }
  code=$(curl -sS -o "${tmp}" -w '%{http_code}' -X "${method}" --max-time "${CURL_MAX_TIME}" "$@" "${url}" 2>/dev/null)
  rc=$?
  FACE_RETRIED=0
  if [ "${rc}" -ne 0 ] || [ "${code}" = "000" ]; then
    code=$(curl --http1.1 -sS -o "${tmp}" -w '%{http_code}' -X "${method}" --max-time "${CURL_MAX_TIME}" "$@" "${url}" 2>/dev/null)
    rc=$?
    FACE_RETRIED=1
  fi
  FACE_RC="${rc}"
  FACE_CODE="${code}"
  FACE_TEXT=$(tr '\n' ' ' < "${tmp}")
  rm -f "${tmp}"
}

# jget <json> <key>：取一个顶层字段（键不存在退 3、整体解析失败退 2）。
# 判"取不到"必须比判"取到空值"更严：解析失败常被读成"这个字段没配"（同族假红见 AGENTS §一·5 ⑤）。
jget() {
  printf '%s' "$1" | python3 -c '
import json, sys
raw = sys.stdin.read()
key = sys.argv[1]
try:
    d = json.loads(raw)
except Exception:
    sys.exit(2)
if not isinstance(d, dict) or key not in d:
    sys.exit(3)
v = d[key]
print("" if v is None else v)
' "$2"
}

# ask <基址> <问题> [额外 curl 参数...]：按挂件正常路径 greet → chat，回答复文本（取不到回空串）
ask() {
  local base="$1" msg="$2"; shift 2
  local sid tok body
  face_curl GET "${base}/api/assist/greeting?page=%2F&lang=zh" "$@"
  if [ "${FACE_RC}" != "0" ] || [ "${FACE_CODE}" != "200" ]; then echo ""; return; fi
  sid=$(jget "${FACE_TEXT}" session 2>/dev/null) || { echo ""; return; }
  tok=$(jget "${FACE_TEXT}" tok 2>/dev/null) || { echo ""; return; }
  if [ -z "${sid}" ] || [ -z "${tok}" ]; then echo ""; return; fi
  body=$(python3 -c '
import json, sys
print(json.dumps({"session": sys.argv[1], "tok": sys.argv[2], "message": sys.argv[3],
                  "page": "/", "lang": "zh"}, ensure_ascii=False))
' "${sid}" "${tok}" "${msg}")
  face_curl POST "${base}/api/assist/chat" -H 'Content-Type: application/json' -d "${body}" "$@"
  if [ "${FACE_RC}" != "0" ] || [ "${FACE_CODE}" != "200" ]; then echo ""; return; fi
  jget "${FACE_TEXT}" reply 2>/dev/null || true
}

# has_b <文本>：回答里是否出现基准数字（剥掉千分位逗号与空格再比）
has_b() {
  local text="$1"
  printf '%s' "${text}" | tr -d ',， 　' | grep -qF -- "${B}"
}

ASK_MSG='我的余额还剩多少积分？只回一个数字，不要解释。'

# ------------------------------------------------------------------ P1 登录
TOKEN=""
LOGIN_BODY=$(python3 -c '
import json, sys
print(json.dumps({"username": sys.argv[1], "password": sys.argv[2]}))
' "${PROBE_USER}" "${PROBE_PASS}")
face_curl POST "${MAIN_BASE}/api/auth/login" -H 'Content-Type: application/json' -d "${LOGIN_BODY}"
if [ "${FACE_RC}" != "0" ]; then
  # 「这一跳没走完」两态都不许落：它既不是通过，也不是产品缺陷 ⇒ 按**没测到**交人复跑
  # （重拨已在 face_curl 里做过一次，第二枪锁了 --http1.1）。
  skip "P1 登录这一跳没走完（rc=${FACE_RC} code=${FACE_CODE:-<空>}）⇒ 链路问题，不是产品缺陷"
  echo "ASSIST_PROFILE_FACE_FAIL=${FAIL} UNMEASURED=${UNMEASURED}"
  echo "ASSIST_PROFILE_FACE_UNMEASURED=1"; exit 2
fi
if TOKEN=$(jget "${FACE_TEXT}" token 2>/dev/null) && [ -n "${TOKEN}" ]; then
  ok "P1 登录成功（token 只在本进程内使用，不落屏、不落文件）"
else
  bad "P1 登录响应体解析不出 token（code=${FACE_CODE}）：$(printf '%.160s' "${FACE_TEXT}")"
  TOKEN=""
fi

# ------------------------------------------------------------------ P2 基准 B
B=""
if [ -n "${TOKEN}" ]; then
  face_curl GET "${MAIN_BASE}/api/me/package" -H "Authorization: Bearer ${TOKEN}"
  if [ "${FACE_RC}" = "0" ] && [ "${FACE_CODE}" = "200" ] && B=$(jget "${FACE_TEXT}" points_balance 2>/dev/null) && [ -n "${B:-}" ]; then
    ok "P2 基准 B=${B}（现读现推，不写死期望值）"
  else
    B=""
    bad "P2 读不到 /api/me/package 的 points_balance（rc=${FACE_RC} code=${FACE_CODE:-<空>}）⇒ 基准缺失，后面三条判据都没有射程"
  fi
fi
# 余额为 0 没有判别力：「还剩 0 积分」与任何一句里出现的 0 在字节上无法区分，
# 反向对照更容易撞车。这种账号请换有余额的探针账号（本探针不代扣费、不充值）。
B_OK=0
if [ -n "${B}" ] && [ "${B}" != "0" ]; then
  B_OK=1
elif [ -n "${B}" ]; then
  skip "P2 余额读数为 0 ⇒ 正腿与两条反向对照都失去判别力，请换有余额的探针账号"
fi

AUTH_ARGS=()
if [ -n "${TOKEN}" ]; then AUTH_ARGS=(-H "Authorization: Bearer ${TOKEN}"); fi

# ------------------------------------------------------------------ P4 正腿
if [ "${B_OK}" = "1" ]; then
  REPLY=$(ask "${MAIN_BASE}/assist-api" "${ASK_MSG}" "${AUTH_ARGS[@]}")
  if [ -z "${REPLY}" ]; then
    skip "P4 经 /assist-api 没问到回答（链路或上游模型不可达）⇒ 没测到，不判产品缺陷"
  elif has_b "${REPLY}"; then
    ok "P4 登录态问出余额 ${B} ⇒ 画像确实随这一跳进了提示词"
  else
    # 一条判据只算一次红（屏幕上分行写排查顺序，但不重复计数——FAIL 数是要被人当结论读的）
    bad "P4 登录态问不出余额（回答：$(printf '%.120s' "${REPLY}")）⇒ 现网这一跳没带上画像。
     排查顺序：① Caddy 的 /assist-api/* 是否落 8787（不是 8790、也没有 strip_prefix）；
     ② 三份 secrets.env 的 ASSIST_PROFILE_KEY 是否同值（deploy/set_assist_profile_key.sh --status）；③ 机器时钟。看 P7。"
  fi
else
  skip "P4 缺登录态或基准，未测"
fi

# ------------------------------------------------------------------ P5 反向对照一（匿名同一条路径）
if [ "${B_OK}" = "1" ]; then
  ANON=$(ask "${MAIN_BASE}/assist-api" "${ASK_MSG}")
  if [ -z "${ANON}" ]; then
    skip "P5 匿名腿没问到回答（链路/上游不可达）⇒ 没测到"
  elif has_b "${ANON}"; then
    bad "P5 匿名请求也答出了 B=${B} ⇒ 画像从别处漏出来了（跨会话缓存／sid 复用），比「不生效」更坏"
  else
    ok "P5 匿名回答不含 B（回落匿名成立）"
  fi
else
  skip "P5 缺基准，未测"
fi

# ------------------------------------------------------------------ P6 反向对照二（直连 assist 端口）
if [ "${B_OK}" = "1" ] && [ -n "${TOKEN}" ]; then
  DIRECT=$(ask "${ASSIST_BASE}" "${ASK_MSG}" "${AUTH_ARGS[@]}")
  if [ -z "${DIRECT}" ]; then
    skip "P6 直连 ${ASSIST_BASE} 没问到回答（该端口只监听回环 ⇒ 本脚本必须在部署机上跑）"
  elif has_b "${DIRECT}"; then
    bad "P6 直连 assist 端口问出余额 B=${B} ⇒ 这一路不该有画像：要么端口对外暴露了，要么 assist 自己查了主库"
  else
    ok "P6 直连 assist 端口不含 B（画像只能来自主服务那一跳，G32 的反向对照成立）"
  fi
else
  skip "P6 缺登录态或基准，未测"
fi

# ------------------------------------------------------------------ P7 验签 WARN 读数
WARN_CNT=""
if command -v journalctl >/dev/null 2>&1; then
  WARN_CNT=$(journalctl -u ai-assist --since "${JOURNAL_SINCE}" --no-pager 2>/dev/null | grep -c "访客画像未通过校验" || true)
fi
if [ -z "${WARN_CNT}" ]; then
  skip "P7 读不到 journalctl（权限不足或没有 ai-assist 单元）⇒ 这一条没测到"
elif [ "${WARN_CNT}" = "0" ]; then
  ok "P7 窗口内 assist 没有「访客画像未通过校验」WARN（带头那一次验签没失败）"
else
  bad "P7 窗口内出现 ${WARN_CNT} 行「访客画像未通过校验」WARN ⇒ 带头但验不过：两侧密钥不一致或时钟漂移"
fi

echo "ASSIST_PROFILE_FACE_FAIL=${FAIL} UNMEASURED=${UNMEASURED}"
if [ "${FAIL}" -gt 0 ]; then echo "ASSIST_PROFILE_FACE_ALL_OK=0"; exit 1; fi
if [ "${UNMEASURED}" -gt 0 ]; then echo "ASSIST_PROFILE_FACE_UNMEASURED=1"; exit 2; fi
echo "ASSIST_PROFILE_FACE_ALL_OK=1"
