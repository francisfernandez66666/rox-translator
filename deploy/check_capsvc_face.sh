#!/usr/bin/env bash
# ============================================================================
# deploy/check_capsvc_face.sh —— 能力常驻服务（capsvc）的**现网只读探针**（2026-10-30 组 6）
#
# 它回答的问题只有一个：**常驻腿到底有没有在干活**。
# 为什么不能省（方案二 A6 是这一组的真判据）：
#   capsvc 坏掉的形态是**静默**的——上游发现 socket 不在/连不上就回落子进程，
#   工单照常完成、产物照常可打开，界面上零症状。只有把「单元活着 / socket 在 /
#   控制面答得出计数 / 模型只加载一次」这几条读数摆出来，才分得清
#   「常驻生效」与「一直在回落、只是没人报」。
#
# 判据（全只读：不建单、不写库、不重启任何单元、不碰 caddy）：
#   F1 单元状态词（active / inactive / failed / not-found）
#   F2 socket 在位且是 socket 文件（权限不看，两站共用组由部署侧定）
#   F3 控制面往返：`svc status` 必须回得出来，且负载里**不含路径/主机名**（同 dispatch 纪律）
#   F4 /api/health.capsvc：state ∈ {ok,off,down}；**单元在跑却说 off** 属读数与事实相反 ⇒ 判红
#   F5 冷启动不变量：model_loads 必须等于 cold_count（同一事件两处记），且 >0 才算"真跑过"；
#      0 ⇒ ⏳ 没测到（这台还没跑过任何一次 OCR），不许读成"没加载模型"
#   F6 fallback_1h 读数（只打印；>0 不判红——服务没部署时它本来就是每单 +1）
#
# 用法（在部署机上跑）：
#   bash check_capsvc_face.sh
#   判绿只认末行 `CAPSVC_FACE_ALL_OK=1`；三态：0 全绿／1 有红项／2 有项**没测到**（复跑再判）。
# ============================================================================
set -uo pipefail

SVC=${CAPSVC_SERVICE_NAME:-translator-capsvc}
SOCK=${CAPSVC_SOCKET:-/run/langcross/capsvc.sock}
HEALTH=${HEALTH_URL:-http://127.0.0.1:8787/api/health}
PY=${FILEPROC_PYTHON_BIN:-python3}
CURL_MAX_TIME=${CURL_MAX_TIME:-20}

FAIL=0
UNMEASURED=0
ok()   { echo "OK   $*"; }
bad()  { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
skip() { echo "⏳   $*"; UNMEASURED=$((UNMEASURED + 1)); }

# ---------------------------------------------------------------- F1 单元状态
STATE=""
if command -v systemctl >/dev/null 2>&1; then
  # is-active 对不存在的单元既打印 not-found 又退非零 ⇒ 只取第一行，别把两行收进变量。
  STATE=$(systemctl is-active "${SVC}.service" 2>/dev/null | head -1)
  echo "F1 unit_state=${STATE:-<未读>}"
  case "$STATE" in
    active) ok "F1 单元在跑" ;;
    "")     skip "F1 systemd 读不到状态（没有 systemctl 或权限不足）" ;;
    *)      skip "F1 单元未运行（state=${STATE}）⇒ 未部署时上游回落子进程，属正常态；接着看 F2 判断是不是半套" ;;
  esac
else
  skip "F1 本机没有 systemctl ⇒ 读不到单元状态（别把没读到读成没部署）"
fi

# ---------------------------------------------------------------- F2 socket
if [ -S "$SOCK" ]; then
  ok "F2 socket 在位：${SOCK}"
  SOCK_OK=1
else
  SOCK_OK=0
  if [ "$STATE" = "active" ]; then
    bad "F2 单元 active 但 socket 不在（${SOCK}）⇒ 启动自检没过或路径漂了，去 journalctl -u ${SVC}"
  else
    skip "F2 socket 不在（${SOCK}）⇒ state=off，正常态"
  fi
fi

# ---------------------------------------------------------------- F3 控制面往返
STATUS_JSON=""
if [ "$SOCK_OK" = "1" ]; then
  STATUS_JSON=$("$PY" - "$SOCK" <<'PY' 2>/dev/null
import json, socket, struct, sys
path = sys.argv[1]
try:
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(3.0)
    s.connect(path)
    body = json.dumps({"id": "face-1", "cap": "svc", "cmd": "status",
                       "args": [], "stdin_b64": "", "timeout_ms": 3000}).encode()
    s.sendall(struct.pack(">I", len(body)) + body)
    hdr = b""
    while len(hdr) < 4:
        b = s.recv(4 - len(hdr))
        if not b:
            raise SystemExit(1)
        hdr += b
    (n,) = struct.unpack(">I", hdr)
    buf = b""
    while len(buf) < n:
        b = s.recv(n - len(buf))
        if not b:
            raise SystemExit(1)
        buf += b
    print(buf.decode())
    s.close()
except Exception:
    raise SystemExit(1)
PY
)
  if [ -n "$STATUS_JSON" ]; then
    ok "F3 控制面往返成功"
    # 路径/主机名泄漏负向腿：socket 路径与部署根都不许出现在负载里。
    case "$STATUS_JSON" in
      *"/run/"*|*"/opt/"*) bad "F3 控制面负载出现内部路径（同 dispatch 纪律：只许状态词与计数）" ;;
      *) ok "F3 负载无内部路径（负向腿）" ;;
    esac
    # ★ 2026-10-31 修正（判据自伤）：协议帧的**外层**只有 ok/stdout/stderr/…，
    #   计数在 `stdout` 那个 JSON 字符串里（capsvc.py 的 _svc_status 输出）。旧写法直接
    #   对外层取 model_loads ⇒ 恒得 -1、caps 恒 0，于是"服务好好的"被读成"注册表为空 +
    #   冷启动读数不可信"（两条 BAD 全是判据自己的错）。
    INNER=$(printf '%s' "$STATUS_JSON" | "$PY" -c 'import json,sys;print(json.load(sys.stdin).get("stdout",""))' 2>/dev/null || echo "")
    MODEL_LOADS=$(printf '%s' "$INNER" | "$PY" -c 'import json,sys;print(int(json.load(sys.stdin).get("model_loads",-1)))' 2>/dev/null || echo -1)
    COLD_COUNT=$(printf '%s' "$INNER" | "$PY" -c 'import json,sys;print(int(json.load(sys.stdin).get("cold_count",-1)))' 2>/dev/null || echo -1)
    CAPS_N=$(printf '%s' "$INNER" | "$PY" -c 'import json,sys;print(len(json.load(sys.stdin).get("caps") or {}))' 2>/dev/null || echo -1)
    echo "F3 model_loads=${MODEL_LOADS} cold_count=${COLD_COUNT} caps=${CAPS_N}"
    if [ "$CAPS_N" -ge 1 ] 2>/dev/null; then ok "F3 能力注册表非空"; else bad "F3 能力注册表为空（caps=${CAPS_N}）"; fi
    # F5 冷启动不变量
    if [ "$MODEL_LOADS" -eq "$COLD_COUNT" ] 2>/dev/null && [ "$MODEL_LOADS" -gt 0 ] 2>/dev/null; then
      ok "F5 model_loads==cold_count==${MODEL_LOADS}（每次加载都记了一次冷启动，且模型只加载这么多次）"
    elif [ "$MODEL_LOADS" -eq 0 ] 2>/dev/null; then
      skip "F5 model_loads=0 ⇒ 这台还没跑过任何一次 OCR（连打三次再判 A6，别把 0 读成没加载）"
    else
      bad "F5 model_loads(${MODEL_LOADS}) 与 cold_count(${COLD_COUNT}) 不等 ⇒ 冷启动读数本身不可信"
    fi
  else
    if [ "$STATE" = "active" ]; then
      bad "F3 控制面往返失败（单元 active）⇒ 协议/权限有问题，去看 journalctl -u ${SVC}"
    else
      skip "F3 控制面往返失败（单元未运行，属正常态）"
    fi
  fi
else
  skip "F3 socket 不在 ⇒ 没测到控制面（不是通过）"
fi

# ---------------------------------------------------------------- F4 /api/health 段
HEALTH_JSON=$(curl -fsS --max-time "$CURL_MAX_TIME" "$HEALTH" 2>/dev/null || true)
if [ -z "$HEALTH_JSON" ]; then
  skip "F4 /api/health 没取到（${HEALTH}）⇒ 链路没走完，复跑再判"
else
  SEG=$(printf '%s' "$HEALTH_JSON" | "$PY" -c 'import json,sys;d=json.load(sys.stdin);print(json.dumps(d.get("capsvc",{}),ensure_ascii=False))' 2>/dev/null || echo "")
  if [ -z "$SEG" ] || [ "$SEG" = "{}" ]; then
    bad "F4 /api/health 没有 capsvc 段（旧件或接线没生效）"
  else
    echo "F4 segment=${SEG}"
    HSTATE=$(printf '%s' "$SEG" | "$PY" -c 'import json,sys;print(json.load(sys.stdin).get("state",""))' 2>/dev/null || echo "")
    case "$HSTATE" in
      ok)  ok "F4 health state=ok" ;;
      off) if [ "$STATE" = "active" ]; then
             bad "F4 单元 active 而 health 说 off ⇒ 读数与事实相反（socket 路径/权限不一致）"
           else
             skip "F4 health state=off（未部署，正常态）"
           fi ;;
      down) bad "F4 health state=down ⇒ 部署了但连不上，去看 journalctl -u ${SVC}" ;;
      *)    bad "F4 health state 取值不在 {ok,off,down} 内：'${HSTATE}'" ;;
    esac
    case "$SEG" in
      *"/run/"*|*"/opt/"*) bad "F4 capsvc 段出现内部路径（同 #42 口径：只许状态词与计数）" ;;
      *) ok "F4 段无内部路径（负向腿）" ;;
    esac
    FB=$(printf '%s' "$SEG" | "$PY" -c 'import json,sys;print(int(json.load(sys.stdin).get("fallback_1h",0)))' 2>/dev/null || echo 0)
    echo "F6 fallback_1h=${FB}（回落次数；>0 不判红——未部署时它本来就每单 +1）"
  fi
fi

echo "CAPSVC_FACE_FAIL=$FAIL UNMEASURED=$UNMEASURED"
if [ "$FAIL" -gt 0 ]; then echo "CAPSVC_FACE_EXIT=1"; exit 1; fi
if [ "$UNMEASURED" -gt 0 ]; then echo "CAPSVC_FACE_EXIT=2"; exit 2; fi
echo "CAPSVC_FACE_ALL_OK=1"; exit 0
