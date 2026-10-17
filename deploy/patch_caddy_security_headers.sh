#!/usr/bin/env bash
# patch_caddy_security_headers.sh — 把 2026-10-17 bug3/4/5 的三条安全头口径**原地补**到线上 Caddy 配置。
#
# 背景（详见 PROGRESS §三十二 / 部署指南 §七 安全头三条）：
#   · Permissions-Policy 把 microphone 从 () 放开为 (self)：() = 禁用所有来源，即时翻译录音必失败；
#   · CSP script-src 增 'wasm-unsafe-eval'：端侧 sherpa-onnx WASM 实例化被 CSP 拦；
#   · CSP 增 media-src 'self' blob:：译文朗读的 blob URL 音频被 default-src 兜住而拒播。
#   （演示站 conf 无 CSP，只需第一条。）
#
# ★ 2026-10-17 第二批（用户报障：登录前页面 CSP 报错 / Cloudflare 挑战平台被拦）——再补三条：
#   · CSP script-src 增 'unsafe-eval'：CF 挑战平台/Turnstile 的托管脚本用字符串求值，官方口径＝
#     受保护页面必须放行，否则控制台出「blocks the use of eval」、托管挑战还可能白屏（cloudflare-docs#17786）；
#   · CSP 增 worker-src 'self' blob:：挑战平台起 worker 被默认 default-src 兜住；
#   · CSP connect-src 增 data:：挑战平台的 attestation 走 fetch('data:…') 被 connect-src 拦。
#   三条都只加在**严格 CSP**（行内含 challenges.cloudflare.com 那条）上，/assist-api/* 挂件那条不动。
#
# 三份**线上原地逐条 patch**、不整份覆盖（线上各自带域名/证书/令牌等本机值，覆盖＝事故）：
#   /etc/caddy/translator.conf（主站）、/etc/caddy/translator-tenant.conf（租户品牌域）、
#   /etc/caddy/translator-demo.conf（演示站）。每份写前留 `.bak.<TS>`；改完 `caddy validate` ＋ `systemctl reload caddy`。
#   ⚠️ 只 validate+reload；**禁止** caddy stop/start/restart/run（打的是同一个 127.0.0.1:2019 管理面，会停全站）。
#
# 幂等：三条判据逐条 presence-check，已存在即跳过该条；三份都无改动时不 reload。
#
# 用法：bash deploy/patch_caddy_security_headers.sh            # 干跑：只打印将做哪些改动
#       APPLY=1 bash deploy/patch_caddy_security_headers.sh    # 真改：备份 → patch → validate → reload
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$SCRIPT_DIR/deploy_to_production.sh"
APPLY="${APPLY:-0}"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

PATCHER="$(mktemp -t caddy_sechdr_XXXX).py"
cat >"$PATCHER" <<'PYEOF'
#!/usr/bin/env python3
# 原地补三条安全头口径。参数：--apply（默认干跑）。返回码：0=无改动，10=有改动（已应用或待应用），非零其它=错误。
import sys, os, time, re

APPLY = "--apply" in sys.argv
TS = time.strftime("%Y%m%d_%H%M%S")
FILES = [
    "/etc/caddy/translator.conf",
    "/etc/caddy/translator-tenant.conf",
    "/etc/caddy/translator-demo.conf",
]

changed_total = 0
for path in FILES:
    if not os.path.exists(path):
        print(f"  SKIP {path}（不存在）")
        continue
    with open(path, "r", encoding="utf-8") as fh:
        lines = fh.readlines()
    out = []
    hits = []
    for i, ln in enumerate(lines):
        orig = ln
        line_hits = []
        # ① Permissions-Policy：microphone=() → microphone=(self)（仅该 header 行，幂等）
        if "Permissions-Policy" in ln and "microphone=()" in ln:
            ln = ln.replace("microphone=()", "microphone=(self)")
            line_hits.append("microphone=(self)")
        # ② CSP 严格版（用 challenges.cloudflare.com 判别，避开 /assist-api/* 那条宽松 CSP）：
        #    2a script-src 增 'wasm-unsafe-eval'；2b 增 media-src 'self' blob:;
        if ln.lstrip().startswith("header ") and "Content-Security-Policy" in ln and "challenges.cloudflare.com" in ln:
            if "'wasm-unsafe-eval'" not in ln:
                # 落在 script-src 段的 'unsafe-inline' 之后
                ln2 = ln.replace("script-src 'self' 'unsafe-inline'", "script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'", 1)
                if ln2 != ln:
                    ln = ln2
                    line_hits.append("script-src+'wasm-unsafe-eval'")
            if "media-src " not in ln:
                # 落在 frame-src 之前（media-src 与 frame-src 相邻，保持指令顺序可读）
                ln2 = ln.replace(" frame-src ", " media-src 'self' blob:; frame-src ", 1)
                if ln2 != ln:
                    ln = ln2
                    line_hits.append("media-src+'self' blob:")
            # ②c 为 CF 挑战/Turnstile 放行 'unsafe-eval'（落在 script-src 段的 'wasm-unsafe-eval' 之前）
            if "'unsafe-eval'" not in ln:
                ln2 = ln.replace("script-src 'self' 'unsafe-inline'", "script-src 'self' 'unsafe-inline' 'unsafe-eval'", 1)
                if ln2 != ln:
                    ln = ln2
                    line_hits.append("script-src+'unsafe-eval'")
            # ②d worker-src 'self' blob:（落在 media-src 之后、frame-src 之前，保持指令顺序可读）
            if "worker-src " not in ln:
                ln2 = ln.replace(" frame-src ", " worker-src 'self' blob:; frame-src ", 1)
                if ln2 != ln:
                    ln = ln2
                    line_hits.append("worker-src+'self' blob:")
            # ②e connect-src 增 data:（挑战平台 attestation 走 fetch('data:…')）
            if "connect-src 'self' blob: https://challenges.cloudflare.com" in ln:
                ln2 = ln.replace("connect-src 'self' blob: https://challenges.cloudflare.com",
                                 "connect-src 'self' blob: data: https://challenges.cloudflare.com", 1)
                if ln2 != ln:
                    ln = ln2
                    line_hits.append("connect-src+data:")
        for h in line_hits:
            hits.append(f"L{i+1}:{h}")
        out.append(ln)
    if not hits:
        print(f"  OK   {path}（安全头六条已在位，无需改动）")
        continue
    changed_total += 1
    print(f"  ---- {path}")
    for h in hits:
        print(f"       · {h}")
    if APPLY:
        bak = f"{path}.bak.{TS}"
        with open(bak, "w", encoding="utf-8") as fh:
            fh.writelines(lines)
        tmp = f"{path}.new.{TS}"
        with open(tmp, "w", encoding="utf-8") as fh:
            fh.writelines(out)
        os.chmod(tmp, os.stat(path).st_mode & 0o7777)
        os.replace(tmp, path)
        print(f"       → 已写（备份 {bak}）")

sys.exit(10 if changed_total else 0)
PYEOF

if [ "$APPLY" != "1" ]; then
  scp -q -P "$PORT" "$PATCHER" "root@$HOST:/tmp/caddy_sechdr_patch.py" || { echo "ABORT=2 scp 失败"; rm -f "$PATCHER"; exit 2; }
  echo "===== 干跑：线上将做的改动（不写盘）====="
  ssh -p "$PORT" "root@$HOST" "python3 /tmp/caddy_sechdr_patch.py"
  echo "（干跑结束。确认后 APPLY=1 真改；改完脚本会自动 validate + reload。）"
  rm -f "$PATCHER"
  exit 0
fi

scp -q -P "$PORT" "$PATCHER" "root@$HOST:/tmp/caddy_sechdr_patch.py" || { echo "ABORT=2 scp 失败"; rm -f "$PATCHER"; exit 2; }
rm -f "$PATCHER"

echo "===== P1 应用补丁 ====="
ssh -p "$PORT" "root@$HOST" "python3 /tmp/caddy_sechdr_patch.py --apply"
rc=$?
if [ "$rc" = "0" ]; then
  echo "P1 无改动（安全头六条已在位）⇒ 不 reload，跳过 P2/P3。"
  exit 0
fi
[ "$rc" = "10" ] || { echo "ABORT=3 补丁脚本退码 ${rc}（非 0/10）"; exit 3; }

echo "===== P2 validate + reload（只 validate+reload，禁止 stop/start/restart）====="
ssh -p "$PORT" "root@$HOST" \
  "caddy validate --config /etc/caddy/Caddyfile && systemctl reload caddy && systemctl is-active caddy" || {
  echo "ABORT=4 validate/reload 失败 —— 现场：/etc/caddy/*.bak.* 是改前原文，可 cp 回退后 reload"; exit 4; }

echo "===== P3 读回三域响应头（判据＝三条口径真出现在线上）====="
ssh -p "$PORT" "root@$HOST" bash -s <<'REMOTE'
for u in "https://langcross.lexicorn.cn/" "https://rox-test.lexicorn.cn/" "https://rox.lexicorn.cn/"; do
  h=$(curl -sI --max-time 20 "$u" | tr -d '\r')
  pp=$(printf '%s\n' "$h" | grep -i '^permissions-policy:' | head -1)
  csp=$(printf '%s\n' "$h" | grep -i '^content-security-policy:' | head -1)
  echo "--- $u"
  echo "    ${pp:-<无 Permissions-Policy>}"
  echo "    media-src: $(printf '%s' "$csp" | grep -o "media-src[^;]*" || echo '<无>')"
  echo "    wasm:      $(printf '%s' "$csp" | grep -o "wasm-unsafe-eval" || echo '<无>')"
  echo "    eval:      $(printf '%s' "$csp" | grep -o "'unsafe-eval'" || echo '<无>')"
  echo "    worker:    $(printf '%s' "$csp" | grep -o "worker-src[^;]*" || echo '<无>')"
  echo "    conn:      $(printf '%s' "$csp" | grep -o "connect-src[^;]*" || echo '<无>')"
done
REMOTE
echo "PATCH_HEADERS_DONE=1"
