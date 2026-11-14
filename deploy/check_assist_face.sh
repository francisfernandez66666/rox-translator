#!/usr/bin/env bash
# ============================================================================
# deploy/check_assist_face.sh —— 四期 W5 面检（§4.5.3 A7 / §8.2）
#
# 它回答一个只有停机之后才问得准的问题：**停掉 translator-assist 之后，
# 主站接待还可用吗**（逻辑收敛真的生效，而不是本地链路悄悄回落了旧代理）。
#
# 三态（同仓内其它 face 脚本口径）：exit 0 全绿／1 有红项／**2 有 N 项没测到**。
# 判绿只认末行 `ASSIST_FACE_ALL_OK=1`。
#
# ★ A7 的核心判据（反证 M0304）：**只判 200 不判 sid ⇒ 红**。
#   挂件是缓存包，主服务直出 dist 的形态下 /assist-api 一旦落 SPA 兜底，
#   也会回一个 200 的 index.html——那正是「助手暂时联系不上」的老病灶。
#   所以 greet 必须拿到非空 session + tok，再用它们真打一次 chat 拿到 reply。
#   --selftest 会喂「200 但无 sid」的坏样本，脚本必须判红；把 sid 判据改成
#   `if false` 之后 selftest 立即红（证明这条判据不是空转的）。
#
# 只读：本脚本不改配置、不停服务。unit 状态只**读**，停机由
# deploy/retire_assist_unit.sh --apply 执行（本面检不越权做危险动作）。
# ============================================================================
set -uo pipefail

FAIL=0
SKIP=0
ok()   { echo "OK   $*"; }
bad()  { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
skip() { echo "SKIP $*"; SKIP=$((SKIP + 1)); }

ASSIST_UNIT_NAME="${ASSIST_FACE_UNIT:-ai-assist.service}"
FACE_BASE="${ASSIST_FACE_BASE:-http://127.0.0.1:8787}"
CURL_MAX_TIME="${ASSIST_FACE_CURL_TIME:-8}"

# jget <key>：从 stdin 的 JSON 里取一个字符串字段，取不到回空串（不吐异常）。
jget() {
  python3 -c 'import sys,json;d=json.load(sys.stdin);v=d.get(sys.argv[1],"");print(v if isinstance(v,str) else "")' "$1" 2>/dev/null || true
}

# face_curl <url> [curl 参数...]：把 rc/http_code/body 落到三个全局。
FACE_CODE=""
FACE_BODY=""
face_curl() {
  local url="$1"; shift
  local tmp
  tmp=$(mktemp) || { FACE_CODE="000"; FACE_BODY=""; return 1; }
  FACE_CODE=$(curl -sS -o "$tmp" -w '%{http_code}' --max-time "$CURL_MAX_TIME" "$@" "$url" 2>/dev/null) || FACE_CODE="000"
  FACE_BODY=$(cat "$tmp" 2>/dev/null)
  rm -f "$tmp"
  [ -n "$FACE_CODE" ] || FACE_CODE="000"
}

# run_checks <base> <check_unit:0/1>：一次完整面检，输出 OK/BAD/SKIP 行，
# 末尾一行 `RESULT FAIL=n SKIP=n` 供 --selftest 捕获判定（不直接 exit）。
run_checks() {
  local base="$1" check_unit="$2"
  FAIL=0
  SKIP=0

  # P1 停机前提：unit 已停用，才谈得上「停进程后接待可用」。
  if [ "$check_unit" = "1" ]; then
    if command -v systemctl >/dev/null 2>&1; then
      local st
      st=$(systemctl is-active "$ASSIST_UNIT_NAME" 2>/dev/null || true)
      case "$st" in
        inactive|unknown|failed) ok "P1 translator-assist 已停用（${st}）" ;;
        active) bad "P1 translator-assist 仍在运行：本次面检没有验到「停进程后主站接待可用」" ;;
        *) skip "P1 unit 状态读不到（${st}），这一项没测到" ;;
      esac
    else
      skip "P1 无 systemctl，unit 状态没测到"
    fi
  fi

  # P2 greet：200 + 非空 sid + 非空 tok（A7 判据本体）。
  local sid tok
  face_curl "$base/assist-api/api/assist/greeting?page=%2F&lang=zh"
  if [ "$FACE_CODE" != "200" ]; then
    bad "P2 greet HTTP ${FACE_CODE}（不是 200）：主站接待没接住 /assist-api"
    sid=""; tok=""
  else
    sid=$(printf '%s' "$FACE_BODY" | jget session)
    tok=$(printf '%s' "$FACE_BODY" | jget tok)
    # ★ 只判 200 不判 sid ⇒ 红（M0304）：SPA 兜底也是 200。
    if [ -z "$sid" ] || [ -z "$tok" ]; then
      bad "P2 greet 回 200 但没有有效 sid/tok：缓存包把 SPA 兜底当成助手可用（sid 判据）"
    else
      ok "P2 greet 拿到 sid+tok"
    fi
  fi

  # P3 chat：用 P2 的 sid+tok 真打一次，必须拿到非空 reply。
  if [ -n "$sid" ] && [ -n "$tok" ]; then
    local chat_body reply
    chat_body=$(printf '{"session":"%s","tok":"%s","message":"你好","page":"/","lang":"zh"}' "$sid" "$tok")
    face_curl "$base/assist-api/api/assist/chat" -X POST -H 'Content-Type: application/json' -d "$chat_body"
    reply=$(printf '%s' "$FACE_BODY" | jget reply)
    if [ "$FACE_CODE" != "200" ] || [ -z "$reply" ]; then
      bad "P3 chat HTTP $FACE_CODE / reply 为空：sid+tok 链路没真通"
    else
      ok "P3 chat 拿到 reply"
    fi
  else
    skip "P3 没拿到 sid+tok，chat 这一项没测到"
  fi

  # P4 health：主站仍活着（只判 200，不消费 assist 字段——那是 health 锁的射程）。
  face_curl "$base/api/health"
  if [ "$FACE_CODE" != "200" ]; then
    bad "P4 /api/health HTTP ${FACE_CODE}：主站本身不可用"
  else
    ok "P4 /api/health 200"
  fi

  echo "RESULT FAIL=$FAIL SKIP=$SKIP"
}

# ------------------------------------------------------------------ selftest
# 三种假主站：good（正常）/ no_sid（200 但无 sid）/ dead（greet 502）。
# good 必须零红项；no_sid 必须红在「sid 判据」上；dead 必须红。
run_selftest() {
  local dir all_ok=1 pid port_file port mode want_bad out has_bad
  command -v python3 >/dev/null 2>&1 || { echo "BAD selftest 缺 python3"; echo "ASSIST_FACE_SELFTEST_ALL_OK=0"; return 2; }
  command -v curl >/dev/null 2>&1 || { echo "BAD selftest 缺 curl"; echo "ASSIST_FACE_SELFTEST_ALL_OK=0"; return 2; }
  dir=$(mktemp -d) || { echo "ASSIST_FACE_SELFTEST_ALL_OK=0"; return 2; }
  cat >"$dir/fixture.py" <<'PYEOF'
import http.server, json, os, sys
mode = os.environ.get("FIX_MODE", "good")
port_file = sys.argv[1]

class H(http.server.BaseHTTPRequestHandler):
    def _json(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        if self.path.startswith("/assist-api/api/assist/greeting"):
            if mode == "dead":
                self.send_response(502); self.end_headers(); self.wfile.write(b"upstream down"); return
            payload = {"session": "sid-fixed", "tok": "tok-fixed", "greeting": "hi"}
            if mode == "no_sid":
                payload.pop("session")
            self._json(200, payload)
        elif self.path.startswith("/api/health"):
            self._json(200, {"status": "ok"})
        else:
            self.send_response(404); self.end_headers()

    def do_POST(self):
        if self.path.startswith("/assist-api/api/assist/chat"):
            self._json(200, {"reply": "固定答复"})
        else:
            self.send_response(404); self.end_headers()

    def log_message(self, *a):
        pass

srv = http.server.HTTPServer(("127.0.0.1", 0), H)
with open(port_file, "w") as f:
    f.write(str(srv.server_address[1]))
srv.serve_forever()
PYEOF
  for pair in "good:0" "no_sid:1" "dead:1"; do
    mode="${pair%%:*}"
    want_bad="${pair##*:}"
    port_file="$dir/port.$mode"
    : >"$port_file"
    FIX_MODE="$mode" python3 "$dir/fixture.py" "$port_file" >/dev/null 2>&1 &
    pid=$!
    for _ in $(seq 1 60); do [ -s "$port_file" ] && break; sleep 0.05; done
    port=$(cat "$port_file" 2>/dev/null)
    if [ -z "$port" ]; then
      kill "$pid" 2>/dev/null || true
      echo "BAD selftest $mode 假主站没起来"
      all_ok=0
      continue
    fi
    out=$(run_checks "http://127.0.0.1:$port" 0)
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    has_bad=0
    printf '%s' "$out" | grep -q '^BAD' && has_bad=1
    if [ "$want_bad" = "0" ]; then
      if [ "$has_bad" = "1" ]; then
        echo "BAD  selftest good：正常态仍报红（判据过敏）"
        printf '%s\n' "$out" | grep '^BAD' | head -3
        all_ok=0
      else
        echo "OK   selftest good：零红项"
      fi
    else
      if [ "$has_bad" != "1" ]; then
        echo "BAD  selftest ${mode}：坏样本没判红 ⇒ 判据空转"
        all_ok=0
      elif [ "$mode" = "no_sid" ] && ! printf '%s' "$out" | grep -q '^BAD.*sid'; then
        echo "BAD  selftest no_sid：没红在 sid 判据上（M0304 射程失明）"
        all_ok=0
      else
        echo "OK   selftest ${mode}：判红（射程在位）"
      fi
    fi
  done
  rm -rf "$dir"
  if [ "$all_ok" = "1" ]; then
    echo "ASSIST_FACE_SELFTEST_ALL_OK=1"
    return 0
  fi
  echo "ASSIST_FACE_SELFTEST_ALL_OK=0"
  return 1
}

if [ "${1:-}" = "--selftest" ]; then
  run_selftest
  exit $?
fi
if [ "${1:-}" != "" ] && [ "${1:-}" != "--run" ]; then
  echo "未知参数：${1}（--selftest／--run）" >&2
  exit 2
fi

run_checks "$FACE_BASE" 1
if [ "$FAIL" -gt 0 ]; then
  echo "ASSIST_FACE_ALL_OK=0"
  exit 1
fi
if [ "$SKIP" -gt 0 ]; then
  echo "ASSIST_FACE_ALL_OK=0 (有 $SKIP 项没测到)"
  exit 2
fi
echo "ASSIST_FACE_ALL_OK=1"
exit 0
