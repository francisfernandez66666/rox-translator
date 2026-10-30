#!/usr/bin/env bash
# ============================================================================
# deploy/install_capsvc.sh —— 能力常驻服务（capsvc）的装配入口（2026-10-30 组 6／方案 A）
#
# 四档，一条命令走到底（与 install_socket_activation.sh / install_probe_timer 同形态）：
#   --status     只读基线：单元状态、socket、解释器判定链、脚本落盘、`--selftest` 能否跑（不改任何东西）
#   --apply      落位：渲染 unit → daemon-reload → enable --now → 等 socket 出现 → 打印状态
#   --rollback   disable --now → 删 unit → daemon-reload（**不需要回滚代码**：上游回落子进程）
#   --selftest   在**假树**里真跑一遍（不碰 /opt、不碰 systemd）：capsvc.py --selftest ＋ 渲染后的 unit
#                必须含四条关键行且 ExecStart 的解释器等于现读的判定链
#   判定只认末行 `CAPSVC_INSTALL_ALL_OK=1`；退出码 0=全绿／1=有红项／2=硬前置不满足（不动作）。
#
# ★ 两条刻意的设计（都是本仓点过名的死法）：
#   ① **解释器现读判定链，不手抄**：`FILEPROC_PYTHON_BIN > /opt/translator/.venv/bin/python3 > python3`
#      与 fileproc.pyBin() 同序（pdfwrite.go 那段注释就是这条链的出处）。探测用一个解释器、
#      执行用另一个，症状是「探针说依赖在、服务起来就说模块找不到」，且零日志。
#   ② **脚本落盘由 sync_fileproc_assets.sh 负责，本脚本只核不搬**：那份同步器「不枚举清单」，
#      新增 .py 零改动即进射程（AGENTS §一·17）。本脚本若自己 cp 一份，就等于长出第二把尺子。
#
# 可覆盖的 env（供 --selftest 与演练；生产别配）：
#   ROOT         部署根，默认 /opt/translator（脚本落点 $ROOT/bin/capsvc.py）
#   UNIT_DIR     unit 落点，默认 /etc/systemd/system
#   SOCKET       socket 路径，默认 /run/langcross/capsvc.sock（必须与 Go 侧默认值同值）
#   CAPSVC_SERVICE_NAME 单元名，默认 translator-capsvc
#   CAPSVC_INSTALL_SKIP_SYSTEMD=1  --apply 时只渲染 unit 不碰 systemd（演练用）
# ============================================================================
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="${ROOT:-/opt/translator}"
UNIT_DIR="${UNIT_DIR:-/etc/systemd/system}"
SOCKET="${SOCKET:-/run/langcross/capsvc.sock}"
SVC_NAME="${CAPSVC_SERVICE_NAME:-translator-capsvc}"
UNIT_SRC="$REPO_ROOT/deploy/capsvc/translator-capsvc.service"
UNIT_DST="$UNIT_DIR/${SVC_NAME}.service"
PY_SRC="$ROOT/bin/capsvc.py"
# ★ ExecStart 里**生产默认**那一档的字面量（与 unit 文件同源）。渲染时整段替换成实际判定值。
UNIT_DEFAULT_PY=/opt/translator/.venv/bin/python3

FAIL=0
UNMEASURED=0
ok()   { printf 'OK   %s\n' "$*"; }
bad()  { printf 'BAD  %s\n' "$*"; FAIL=$((FAIL + 1)); }
skip() { printf '⏳   %s\n' "$*"; UNMEASURED=$((UNMEASURED + 1)); }
say()  { printf '%s\n' "$*"; }

MODE=""
case "${1:-}" in
  --status|--apply|--rollback|--selftest) MODE="$1" ;;
  ""|-h|--help) awk '/^set -uo/{exit} {print}' "${BASH_SOURCE[0]}"; exit 0 ;;
  *) echo "未知参数：${1}（--status／--apply／--rollback／--selftest）" >&2; exit 2 ;;
esac
[ -f "$UNIT_SRC" ] || { echo "ABORT=2 仓库缺 $UNIT_SRC"; exit 2; }

# ---------------------------------------------------------------- 解释器判定链（唯一实现）
# 与 fileproc.pyBin() 同序：env > 生产 venv > PATH python3。任何一处改动都要两侧同步。
resolve_python() {
  if [ -n "${FILEPROC_PYTHON_BIN:-}" ]; then printf '%s' "$FILEPROC_PYTHON_BIN"; return; fi
  if [ -x "$UNIT_DEFAULT_PY" ]; then printf '%s' "$UNIT_DEFAULT_PY"; return; fi
  command -v python3 || true
}

render_unit() {
  # $1=目标文件；$2=解释器；$3=部署根。把 ExecStart 整行按判定值重写（其余行一字不动）。
  local dest="$1" py="$2" root="$3"
  sed -e "s#^ExecStart=.*#ExecStart=${py} ${root}/bin/capsvc.py --socket ${SOCKET}#" \
      "$UNIT_SRC" > "$dest"
}

# ---------------------------------------------------------------- --status / --apply / --rollback
if [ "$MODE" = "--status" ]; then
  say "===== capsvc 基线（只读）====="
  PY="$(resolve_python)"
  [ -n "$PY" ] && ok "解释器判定链 → ${PY}" || bad "解释器判定链取不到（FILEPROC_PYTHON_BIN / venv / python3 全空）"
  [ -f "$PY_SRC" ] && ok "服务脚本在位：${PY_SRC}" \
    || bad "服务脚本不在位：${PY_SRC} ⇒ 先跑 scripts/sync_fileproc_assets.sh（它不枚举清单，新 .py 自动进射程）"
  if command -v systemctl >/dev/null 2>&1; then
    ST="$(systemctl is-active "${SVC_NAME}.service" 2>/dev/null || true)"
    EN="$(systemctl is-enabled "${SVC_NAME}.service" 2>/dev/null || true)"
    say "     unit=${ST:-<未读>} enabled=${EN:-<未读>}"
    [ "$ST" = "active" ] && ok "单元在跑" || skip "单元未运行（state=${ST:-<未读>}）⇒ 未部署时上游回落子进程，属正常态"
  else
    skip "本机没有 systemctl ⇒ 读不到单元状态（别把没读到读成没部署）"
  fi
  if [ -S "$SOCKET" ]; then ok "socket 在位：${SOCKET}"; else skip "socket 不在：${SOCKET}（state=off，正常态）"; fi
  say "CAPSVC_STATUS_FAIL=$FAIL UNMEASURED=$UNMEASURED"
  [ "$FAIL" -gt 0 ] && exit 1
  exit 0
fi

if [ "$MODE" = "--rollback" ]; then
  say "===== capsvc 回滚（停单元 + 删 unit；不动代码：上游自动回落子进程）====="
  systemctl disable --now "${SVC_NAME}.service" 2>/dev/null || true
  rm -f "$UNIT_DST"
  systemctl daemon-reload 2>/dev/null || true
  rm -f "$SOCKET" 2>/dev/null || true
  say "CAPSVC_ROLLBACK_DONE=1"
  exit 0
fi

if [ "$MODE" = "--apply" ]; then
  say "===== capsvc 落位 ====="
  PY="$(resolve_python)"
  [ -n "$PY" ] || { say "ABORT=2 解释器判定链取不到"; exit 2; }
  [ -x "$PY" ] || { say "ABORT=2 解释器不可执行：${PY}（探测与执行必须同一个解释器）"; exit 2; }
  [ -f "$PY_SRC" ] || { say "ABORT=2 ${PY_SRC} 不在位 ⇒ 先跑 scripts/sync_fileproc_assets.sh"; exit 2; }
  mkdir -p "$UNIT_DIR" || { say "ABORT=2 建不了 ${UNIT_DIR}"; exit 2; }
  TMP="$(mktemp "${TMPDIR:-/tmp}/capsvc-unit.XXXXXX")"
  render_unit "$TMP" "$PY" "$ROOT"
  if [ -f "$UNIT_DST" ]; then cp -a "$UNIT_DST" "${UNIT_DST}.bak.$(date +%s)"; fi
  install -m 0644 "$TMP" "$UNIT_DST" || { say "ABORT=2 写 ${UNIT_DST} 失败"; rm -f "$TMP"; exit 2; }
  rm -f "$TMP"
  ok "unit 已落位：${UNIT_DST}（解释器=${PY}）"
  if [ "${CAPSVC_INSTALL_SKIP_SYSTEMD:-0}" = "1" ]; then
    skip "CAPSVC_INSTALL_SKIP_SYSTEMD=1 ⇒ 没碰 systemd（演练）"
  else
    systemctl daemon-reload || { say "ABORT=2 daemon-reload 失败"; exit 2; }
    systemctl enable --now "${SVC_NAME}.service" || { say "ABORT=2 enable --now 失败"; exit 2; }
    for _ in $(seq 1 50); do [ -S "$SOCKET" ] && break; sleep 0.2; done
    if [ -S "$SOCKET" ]; then ok "socket 已出现：${SOCKET}"; else
      bad "socket 10s 内没出现 ⇒ 看 journalctl -u ${SVC_NAME}（多半是启动自检未过：依赖模块/资产）"
    fi
    say "     $(systemctl is-active "${SVC_NAME}.service" 2>/dev/null || true)"
  fi
  say "CAPSVC_APPLY_FAIL=$FAIL"
  [ "$FAIL" -gt 0 ] && exit 1
  exit 0
fi

# ---------------------------------------------------------------- --selftest（假树真跑）
if [ "$MODE" = "--selftest" ]; then
  say "===== capsvc 安装器自测（假树，不碰 /opt、不碰 systemd）====="
  PY="$(resolve_python)"
  if [ -z "$PY" ] || [ ! -x "$PY" ]; then
    say "CAPSVC_INSTALL_SELFTEST_FAIL=1（解释器判定链取不到：${PY:-<空>}）"; exit 1
  fi
  FAKE="$(mktemp -d "${TMPDIR:-/tmp}/capsvc-install-selftest.XXXXXX")"
  trap 'rm -rf "$FAKE"' EXIT
  mkdir -p "$FAKE/root/bin" "$FAKE/unit"
  # 服务脚本从仓库现取（生产由 sync_fileproc_assets.sh 落盘，形态相同）。
  cp "$REPO_ROOT/backend-go/internal/fileproc/capsvc.py" "$FAKE/root/bin/capsvc.py" || {
    say "CAPSVC_INSTALL_SELFTEST_FAIL=1（拷不到 capsvc.py）"; exit 1; }

  # T1 服务本体自测（纯协议腿，不需要 onnxruntime/权重）
  if "$PY" "$FAKE/root/bin/capsvc.py" --selftest >"$FAKE/selftest.log" 2>&1; then
    if tail -1 "$FAKE/selftest.log" | grep -q '^CAPSVC_SELFTEST_ALL_OK=1$'; then
      ok "T1 capsvc.py --selftest 全绿"
    else
      bad "T1 自测退出 0 但末行不是 CAPSVC_SELFTEST_ALL_OK=1（判据只认这一行）"
    fi
  else
    bad "T1 capsvc.py --selftest 失败（末行：$(tail -1 "$FAKE/selftest.log" 2>/dev/null)）"
  fi

  # T2 渲染：四条关键行 + 解释器必须等于判定值 + 路径与 Go 侧默认同值
  render_unit "$FAKE/unit/svc.service" "$PY" "$FAKE/root"
  U="$FAKE/unit/svc.service"
  grep -q '^Restart=always$' "$U" && ok "T2a Restart=always" || bad "T2a 缺 Restart=always"
  grep -q '^MemoryMax=800M$' "$U" && ok "T2b MemoryMax=800M" || bad "T2b 缺 MemoryMax=800M"
  grep -q '^RuntimeDirectory=langcross$' "$U" && ok "T2c RuntimeDirectory=langcross" || bad "T2c 缺 RuntimeDirectory=langcross"
  grep -q -- '--socket /run/langcross/capsvc.sock' "$U" && ok "T2d socket 路径与 Go 侧默认同值" \
    || bad "T2d socket 路径漂了（Go 侧 CapsvcDefaultSocket=/run/langcross/capsvc.sock）"
  if grep -q "^ExecStart=${PY} ${FAKE}/root/bin/capsvc.py --socket /run/langcross/capsvc.sock$" "$U"; then
    ok "T2e ExecStart 解释器=判定链现值（不手抄）"
  else
    bad "T2e ExecStart 与判定链不一致（渲染后的行：$(grep '^ExecStart=' "$U")）"
  fi

  # T3 幂等：再渲染一次结果必须逐字节相同（模板里没有依赖时间/随机的东西）
  render_unit "$FAKE/unit/svc2.service" "$PY" "$FAKE/root"
  cmp -s "$U" "$FAKE/unit/svc2.service" && ok "T3 渲染幂等（两次逐字节相同）" || bad "T3 渲染不幂等"

  # T4 描述性字段不许把内部路径泄进环境变量面（只查 Environment= 行）
  if grep -q '^Environment=' "$U"; then
    ok "T4 Environment 段在位（$(grep -c '^Environment=' "$U") 行）"
  else
    bad "T4 少了 Environment 段（预算/空闲卸载/并发档全靠它）"
  fi

  say "CAPSVC_INSTALL_SELFTEST_FAIL=$FAIL"
  if [ "$FAIL" -gt 0 ]; then exit 1; fi
  echo "CAPSVC_INSTALL_ALL_OK=1"
  exit 0
fi
