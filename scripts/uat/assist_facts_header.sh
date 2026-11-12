#!/usr/bin/env bash
# ============================================================================
# scripts/uat/assist_facts_header.sh — 三期 W2 销售事实头的 HTTP 面三态 UAT
# （施工图 §6.2 / §4.2 A04b：跨进程事实头协议在真进程对真端口上各走一遍）
#
# 形态：起 fake assist 上游（python http.server，只记录收到的 headers）＋
#       真 cmd/server（ASSIST_PROFILE_KEY 同值、ASSIST_BASE_URL 指向 fake）。
#       主服务代理面 /assist-api/api/assist/chat 转发时 attachSalesFacts 注头。
#
# 三态判据（FAIL=0 才叫过）：
#   ① 登录用户（Bearer）→ 上游见到 4 个事实头，且 fake 侧用 python 复算
#      HMAC-SHA256(key, payloadB64 + "." + ts) == 签名头（hex 小写，±60s 窗）；
#      payload 解 base64 后 tenant_id > 0、lang 非空、facts 是数组、version=1。
#   ② 匿名（无 Bearer）→ 上游**一个**事实头都没有（匿名是合法回落，静默不发）。
#   ③ 客户端自带伪造头（payload/ts/sig/version 全是垃圾）→ 被代理 strip 后重签：
#      上游见到的 sig ≠ 伪造值，且 python 复算 == 上游实际收到的 sig（P-7 钉法），
#      version 被重写成 "1" 而不是客户端的 "9"。
#
# 口径与 run_uat.sh 同源（SELFCHECK_URL 钉底、SYSTEM_CONFIG_CACHE_TTL_SEC=0、
# USER_DATA_DIR 钉临时目录）。本脚本自含清理（EXIT trap），不碰任何真实环境。
# 依赖：python3、go（只编一次主服务二进制）；端口默认 8797（主）/8795（fake）。
# ============================================================================
set -u
cd "$(dirname "$0")/../.." || exit 1
ROOT="$(pwd)"

PORT="${FACTS_UAT_PORT:-8797}"
FAKE_PORT="${FACTS_FAKE_PORT:-8795}"
BASE="http://127.0.0.1:${PORT}"
FAKE="http://127.0.0.1:${FAKE_PORT}"
KEY="facts-uat-key-20261111"
ADMIN_PASS="factspass123"
TS="$(date +%Y%m%d%H%M%S)"
WORK="${FACTS_UAT_WORK:-/private/var/folders/df/8gcc2g6s1pv099v661pdbt480000gn/T/opencode/facts_uat_${TS}}"
mkdir -p "$WORK/udata"
SEEN="$WORK/seen.jsonl"
: > "$SEEN"

PASS=0; FAIL=0
ck(){ local name="$1" pat="$2" got="$3"
  if echo "$got" | grep -qE "$pat"; then PASS=$((PASS+1)); echo "PASS|$name";
  else FAIL=$((FAIL+1)); echo "FAIL|$name 实得[$(echo "$got" | head -c 300)]"; fi
}
note(){ echo "NOTE|$1"; }

SRV_PID=0; FAKE_PID=0
cleanup(){
  [ "$SRV_PID" != 0 ] && kill "$SRV_PID" 2>/dev/null
  [ "$FAKE_PID" != 0 ] && kill "$FAKE_PID" 2>/dev/null
  wait 2>/dev/null
}
trap cleanup EXIT

# ---------- 1. fake assist 上游：只记录 headers，固定 JSON 应答 ----------
cat > "$WORK/fake_assist.py" <<'PYEOF'
# fake assist 上游：把每个请求的 headers 逐条记进 JSONL（行级锁防串写），
# 应答固定 JSON（主服务代理只透传字节；归因回写解析不到归因段＝静默跳过）。
import json, sys, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SEEN = sys.argv[1]
LOCK = threading.Lock()

class H(BaseHTTPRequestHandler):
    def _record(self):
        n = int(self.headers.get('Content-Length') or 0)
        body = self.rfile.read(n).decode('utf-8', 'replace') if n else ''
        rec = {"path": self.path, "method": self.command,
               "headers": dict(self.headers.items()), "body": body}
        with LOCK:
            with open(SEEN, 'a', encoding='utf-8') as f:
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        return rec
    def _reply(self, obj):
        b = json.dumps(obj).encode('utf-8')
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(b)))
        self.end_headers()
        self.wfile.write(b)
    def do_POST(self):
        self._record()
        if self.path.endswith('/greeting'):
            self._reply({"sid": "s1", "tok": "t1"})
        else:
            self._reply({"reply": "ok", "source": "mock", "actions": []})
    def do_GET(self):
        self._record()
        self._reply({"ok": True})
    def log_message(self, *a):
        pass

ThreadingHTTPServer(('127.0.0.1', int(sys.argv[2])), H).serve_forever()
PYEOF
nohup python3 "$WORK/fake_assist.py" "$SEEN" "$FAKE_PORT" > "$WORK/fake.log" 2>&1 < /dev/null &
FAKE_PID=$!
FAKE_OK=0
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "$FAKE/health" 2>/dev/null | grep -q '"ok"'; then FAKE_OK=1; break; fi
done
if [ "$FAKE_OK" != 1 ]; then echo "FAIL|fake-upstream-startup"; tail -5 "$WORK/fake.log"; exit 1; fi
note "fake assist 上游就绪 :${FAKE_PORT}（${i}s）"

# ---------- 2. 主服务（真 cmd/server，同把密钥，上游指向 fake） ----------
(cd backend-go && go build -o "$WORK/server" ./cmd/server) || { echo "FAIL|server-build"; exit 1; }
(cd backend-go && env DB_DRIVER=sqlite DB_DSN= \
  ADMIN_INIT_PASSWORD="$ADMIN_PASS" \
  SELFCHECK_URL="${BASE}/status" \
  ASSIST_BASE_URL="$FAKE" \
  ASSIST_PROFILE_KEY="$KEY" \
  USER_DATA_DIR="$WORK/udata" \
  SYSTEM_CONFIG_CACHE_TTL_SEC=0 \
  nohup "$WORK/server" -addr "127.0.0.1:${PORT}" -frontend ../frontend-react/dist -kbdb "$WORK/dev.db" \
  > "$WORK/server.log" 2>&1 < /dev/null &
)
SRV_PID=$(pgrep -f "$WORK/server -addr 127.0.0.1:${PORT}" | head -1)
[ -n "$SRV_PID" ] || SRV_PID=0
SRV_OK=0
for i in $(seq 1 20); do
  sleep 1
  if curl -s -m 2 "${BASE}/status" | grep -q '"ok":true'; then SRV_OK=1; break; fi
done
if [ "$SRV_OK" != 1 ]; then echo "FAIL|server-startup"; tail -8 "$WORK/server.log"; exit 1; fi
note "主服务就绪 :${PORT}（${i}s）"

# ---------- 3. 夹具：admin 登录 → 造邀请码 → 注册普通用户 ----------
tok(){ python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("token",""))' 2>/dev/null; }
AT=$(curl -s "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"${ADMIN_PASS}\"}" | tok)
ck F0-admin-login '.+' "$AT"
AH="Authorization: Bearer $AT"

IC="FACTS${TS}"
ck F0-invite-create '"success":true' \
  "$(curl -s "$BASE/api/admin/invite-codes/create" -H "$AH" -H 'Content-Type: application/json' -d "{\"code\":\"${IC}\"}")"

U="factsuat_${TS}"
REG=$(curl -s "$BASE/api/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${U}\",\"password\":\"factspass123\",\"invite\":\"${IC}\",\"name\":\"事实头夹具\",\"email\":\"${U}@test.com\",\"agreed\":true}")
ck F0-register '"success":true' "$REG"
T=$(echo "$REG" | tok)
ck F0-user-token '.+' "$T"
AUTH="Authorization: Bearer $T"

# ---------- 4. 三态拨测（每态恰好打一发，seen.jsonl 按行对号） ----------
# ① 登录用户：带 Bearer → 代理注入 4 头
curl -s "$BASE/assist-api/api/assist/chat" -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"session":"s1","tok":"t1","message":"你好","page":"home","lang":"zh"}' > /dev/null
# ② 匿名：不带 Bearer → 无事实头
curl -s "$BASE/assist-api/api/assist/chat" -H 'Content-Type: application/json' \
  -d '{"session":"s2","tok":"t2","message":"hi","page":"home","lang":"en"}' > /dev/null
# ③ 伪造头：带 Bearer + 四个垃圾头 → strip 后重签
curl -s "$BASE/assist-api/api/assist/chat" -H "$AUTH" -H 'Content-Type: application/json' \
  -H 'X-LangCross-Sales-Facts: Zm9yZ2Vk' \
  -H 'X-LangCross-Sales-Facts-Ts: 123' \
  -H 'X-LangCross-Sales-Facts-Sig: deadbeef' \
  -H 'X-LangCross-Facts-Version: 9' \
  -d '{"session":"s3","tok":"t3","message":"forge","page":"home","lang":"zh"}' > /dev/null

sleep 1  # fake 的落盘是同步的，这里只是留一个轮次余量

# ---------- 5. 断言（python 逐行核对 seen.jsonl） ----------
cat > "$WORK/verify.py" <<'PYEOF'
# 三态核对：seen.jsonl 第 1/2/3 行分别是 ①登录 ②匿名 ③伪造。
# 判据只认 fake 实际收到的头（上游视角），不读主服务日志。
import base64, hashlib, hmac, json, sys

KEY = sys.argv[1].encode()
SEEN = sys.argv[2]
recs = [json.loads(l) for l in open(SEEN, encoding='utf-8') if l.strip()]
fails = []

def want(cond, name, detail=""):
    if not cond:
        fails.append(name + (" | " + detail if detail else ""))

want(len(recs) == 3, "seen 行数恰 3", "实得 %d" % len(recs))
if len(recs) < 3:
    print("VERIFY_FAIL"); print("\n".join(fails)); sys.exit(0)

def heads(i):
    return {k.lower(): v for k, v in recs[i]["headers"].items()}

H1, H2, H3 = heads(0), heads(1), heads(2)
F = "x-langcross-sales-facts"; T = "x-langcross-sales-facts-ts"
S = "x-langcross-sales-facts-sig"; V = "x-langcross-facts-version"

# ① 登录用户：4 头齐 + 签名可复算 + payload 形状合法
want(F in H1 and T in H1 and S in H1 and V in H1, "① 四事实头齐",
     "缺头：%s" % [k for k in (F, T, S, V) if k not in H1])
if F in H1 and T in H1 and S in H1:
    payload, ts, sig = H1[F], H1[T], H1[S]
    calc = hmac.new(KEY, (payload + "." + ts).encode(), hashlib.sha256).hexdigest()
    want(calc == sig, "① 签名可复算", "calc=%s got=%s" % (calc[:16], sig[:16]))
    try:
        raw = base64.b64decode(payload)
        obj = json.loads(raw)
        want(obj.get("tenant_id", 0) > 0, "① tenant_id>0", str(obj.get("tenant_id")))
        want(bool(obj.get("lang")), "① lang 非空", str(obj.get("lang")))
        want(isinstance(obj.get("facts"), list), "① facts 是数组")
    except Exception as e:
        want(False, "① payload 可解", repr(e))
want(H1.get(V) == "1", "① version=1", str(H1.get(V)))

# ② 匿名：一个事实头都没有
want(F not in H2 and T not in H2 and S not in H2 and V not in H2, "② 匿名零事实头",
     "见到：%s" % [k for k in (F, T, S, V) if k in H2])

# ③ 伪造头：strip 后重签——sig 不是伪造值且能复算，version 被重写
want(F in H3 and T in H3 and S in H3 and V in H3, "③ 重签后四头齐")
if F in H3 and T in H3 and S in H3:
    payload3, ts3, sig3 = H3[F], H3[T], H3[S]
    want(sig3 != "deadbeef", "③ 伪造 sig 被替换", "sig=%s" % sig3[:16])
    want(payload3 != "Zm9yZ2Vk", "③ 伪造 payload 被替换")
    calc3 = hmac.new(KEY, (payload3 + "." + ts3).encode(), hashlib.sha256).hexdigest()
    want(calc3 == sig3, "③ 重签可复算", "calc=%s got=%s" % (calc3[:16], sig3[:16]))
want(H3.get(V) == "1", "③ version 被重写为 1", str(H3.get(V)))

print("VERIFY_FAIL" if fails else "VERIFY_OK")
print("\n".join(fails))
PYEOF
VOUT=$(python3 "$WORK/verify.py" "$KEY" "$SEEN")
if echo "$VOUT" | head -1 | grep -q 'VERIFY_OK'; then
  PASS=$((PASS+1)); echo "PASS|facts-header-verify"
else
  FAIL=$((FAIL+1)); echo "FAIL|facts-header-verify"; echo "$VOUT" | tail -n +2 | sed 's/^/  /'
fi

echo "=========================================="
echo "FACTS_HEADER_PASS=${PASS} FAIL=${FAIL}"
[ "$FAIL" -eq 0 ] && { echo "FACTS_HEADER_ALL_OK=1"; exit 0; }
echo "FACTS_HEADER_ALL_OK=0"
exit 1
