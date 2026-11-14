#!/usr/bin/env bash
# ============================================================================
# deploy/install_knowledge_service.sh — 四期 W3 知识服务独立进程装配入口
# （AGENTS §一·16「装配四档一条命令」形态；施工图 §8.3 / §4.3.8）
#
# 为什么必须是一条命令：知识服务「独立进程 + 独立库」若只把 unit 落在仓库里、
# 安装步骤写在文件头注释里，现网就会一直是「仓库里有、systemd 里没有」——同 10-10
# 那只闹钟的第二层教训。⇒ 装配/回滚都要真执行体，不是文档。
#
# ★ 写 unit 前再判一次双 DSN 不等（KN12，与 internal/knowledge/config.go AssertDistinct 同口径）：
#   空 / 相等 / 归一化后相等（去尾 `/`、大小写、localhost↔127.0.0.1、去 :5432）一律拒绝写 unit。
#   ⚠️ localhost 与 127.0.0.1 必须判成「同一个库」——看着不同实际同库正是这条闸要拦的东西。
#
# 四档（默认干跑，任何写动作都要显式下令）：
#   bash deploy/install_knowledge_service.sh              # 干跑：打印将做的动作 + unit 全文，不写一个字节
#   bash deploy/install_knowledge_service.sh --status     # 只读：unit 在不在 / is-active / health / 双 DSN 判定
#   bash deploy/install_knowledge_service.sh --apply      # 落位：写 unit → daemon-reload → enable --now → restart → health 验收
#   bash deploy/install_knowledge_service.sh --rollback  # 回滚：disable --now → 删 unit → daemon-reload（真删，不靠回退代码）
#
# 判绿只认末行 `KNOWLEDGE_APPLY_OK=1` / `KNOWLEDGE_ROLLBACK_OK=1` / `KNOWLEDGE_DRYRUN_OK=1`。
# 退出码：0＝该档做完且判据成立；1＝干跑有待办/验收不成立；2＝硬前置不满足（参数/env 形状）。
#
# 可覆盖 env（便于本机干跑演练，不碰 /etc）：
#   KNOWLEDGE_UNIT / KNOWLEDGE_BIN / KNOWLEDGE_BASE_URL / KNOWLEDGE_DB_DRIVER /
#   KNOWLEDGE_DB_DSN / DB_DSN / KNOWLEDGE_TOKEN / KNOWLEDGE_SERVICE_READ
# ============================================================================
set -uo pipefail

UNIT=${KNOWLEDGE_UNIT:-/etc/systemd/system/translator-knowledge.service}
SERVICE=${KNOWLEDGE_SERVICE:-translator-knowledge.service}
BIN=${KNOWLEDGE_BIN:-/opt/translator/bin/knowledge-server}
BASE_URL=${KNOWLEDGE_BASE_URL:-http://127.0.0.1:8791}

MODE=dryrun
while [ $# -gt 0 ]; do
  case "$1" in
    --status)   MODE=status;   shift ;;
    --apply)    MODE=apply;    shift ;;
    --rollback) MODE=rollback; shift ;;
    --dry-run)  MODE=dryrun;   shift ;;
    *) echo "ABORT=2 未知参数 $1"; exit 2 ;;
  esac
done

# 由 DB_DSN 派生知识库 DSN（库名 → langcross_knowledge，保留用户名/主机/参数）：
# ★ 只用「最后一个 / 后的库名」正则，避免误伤 //user 段（原 do_install.sh 的
#   sed "s#/langcross#/langcross_knowledge#" 会先命中 //langcross 用户名段，
#   把 postgres://langcross 变成 postgres:/langcross_knowledge，导致认证失败 28P01）。
# 显式传入 KNOWLEDGE_DB_DSN 时本段跳过（以调用方为准）。
if [ -z "${KNOWLEDGE_DB_DSN:-}" ] && [ -n "${DB_DSN:-}" ]; then
  KNOWLEDGE_DB_DSN=$(printf '%s' "$DB_DSN" | sed -E 's#/([^/?]+)(\?.*)?$#/langcross_knowledge\2#')
  echo "DERIVED KNOWLEDGE_DB_DSN from DB_DSN (库名 → langcross_knowledge)"
fi

# 归一化 DSN（可移植写法：macOS 自带 bash 3.2 无 ${s,,}，用 tr）。
norm_dsn() {
  local s
  s=$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')
  s=${s%/}
  s=${s//localhost/127.0.0.1}
  s=${s//:5432/}
  printf '%s' "$s"
}

# KN12：双 DSN 不等硬断言。相等/空即返回 1（写 unit 前的唯一闸门）。
assert_distinct() {
  local main=${DB_DSN:-} know=${KNOWLEDGE_DB_DSN:-}
  if [ -z "$main" ] || [ -z "$know" ]; then
    echo "BAD 双 DSN 断言：DB_DSN / KNOWLEDGE_DB_DSN 不得为空（KN12）"
    return 1
  fi
  if [ "$(norm_dsn "$main")" = "$(norm_dsn "$know")" ]; then
    echo "BAD 双 DSN 断言：两个 DSN 指向同一个库（KN12），拒绝写 unit"
    return 1
  fi
  echo "OK  双 DSN 不等（KN12）"
  return 0
}

# unit 值形状守卫：含双引号/换行会把 Environment= 拆开（静默装出错 unit）。
guard_env() {
  case "${KNOWLEDGE_DB_DSN:-}${DB_DSN:-}${KNOWLEDGE_TOKEN:-}" in
    *'"'*) echo "BAD env 值含双引号，拒绝写 unit"; return 1 ;;
  esac
  return 0
}

render_unit() {
  cat <<EOF
[Unit]
Description=LangCross Knowledge Service (四期 W3, 127.0.0.1:8791)
After=network.target

[Service]
Type=simple
Environment=KNOWLEDGE_DB_DRIVER=${KNOWLEDGE_DB_DRIVER:-sqlite}
Environment=KNOWLEDGE_DB_DSN=${KNOWLEDGE_DB_DSN:-}
Environment=DB_DSN=${DB_DSN:-}
Environment=KNOWLEDGE_TOKEN=${KNOWLEDGE_TOKEN:-}
Environment=KNOWLEDGE_BASE_URL=${BASE_URL}
Environment=KNOWLEDGE_SERVICE_READ=${KNOWLEDGE_SERVICE_READ:-off}
Environment=KNOWLEDGE_ADDR=127.0.0.1:8791
ExecStart=${BIN}
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF
}

do_status() {
  echo "UNIT=$UNIT"
  if [ -f "$UNIT" ]; then echo "UNIT_PRESENT=1"; else echo "UNIT_PRESENT=0"; fi
  if command -v systemctl >/dev/null 2>&1; then
    echo "ACTIVE=$(systemctl is-active "$SERVICE" 2>/dev/null || true)"
  else
    echo "ACTIVE=unknown_no_systemctl"
  fi
  assert_distinct || true
  if health=$(curl -sS -m 5 "$BASE_URL/internal/health" 2>/dev/null); then
    echo "HEALTH=$health"
  else
    echo "HEALTH=unavailable"
  fi
}

do_dryrun() {
  echo "== 干跑：以下动作将执行，本档不写任何东西 =="
  assert_distinct || true
  [ -n "${KNOWLEDGE_TOKEN:-}" ] || echo "BAD KNOWLEDGE_TOKEN 未配置（--apply 会拒）"
  echo "-- unit 将写到 ${UNIT}："
  render_unit
  echo "-- 随后：systemctl daemon-reload && systemctl enable --now $SERVICE && systemctl restart $SERVICE"
  echo "KNOWLEDGE_DRYRUN_OK=1"
}

do_apply() {
  assert_distinct || return 1
  [ -n "${KNOWLEDGE_TOKEN:-}" ] || { echo "BAD KNOWLEDGE_TOKEN 未配置"; return 1; }
  guard_env || return 1
  if ! command -v systemctl >/dev/null 2>&1; then
    echo "BAD systemctl 不存在（本脚本只在部署机跑）"; return 1
  fi
  if ! render_unit > "$UNIT"; then echo "BAD 写 unit 失败 $UNIT"; return 1; fi
  systemctl daemon-reload || return 1
  systemctl enable --now "$SERVICE" || return 1
  systemctl restart "$SERVICE" || return 1
  local h
  if ! h=$(curl -sS -m 10 "$BASE_URL/internal/health" 2>/dev/null); then
    echo "BAD apply 后 /internal/health 不可达"; return 1
  fi
  if ! printf '%s' "$h" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="ok" and d.get("contract_version"), "health shape"' 2>/dev/null; then
    echo "BAD health 形状不对（缺 status/contract_version）"; return 1
  fi
  echo "OK  apply 完成 + health 验收"
  echo "KNOWLEDGE_APPLY_OK=1"
  return 0
}

# 真回滚：停服务、删 unit、重载。DDL 不可由代码回滚，删表后的数据腿走
# cmd/knowledge-reverse-migrate（本脚本只负责「服务/unit」这一层，适用范围写在文件头）。
do_rollback() {
  if ! command -v systemctl >/dev/null 2>&1; then
    echo "BAD systemctl 不存在（本脚本只在部署机跑）"; return 1
  fi
  systemctl disable --now "$SERVICE" 2>/dev/null || true
  if ! rm -f "$UNIT"; then echo "BAD 删 unit 失败 $UNIT"; return 1; fi
  systemctl daemon-reload || return 1
  echo "OK  rollback 完成（unit 已删、服务已停）"
  echo "KNOWLEDGE_ROLLBACK_OK=1"
  return 0
}

case "$MODE" in
  status)   do_status ;;
  dryrun)   do_dryrun ;;
  apply)    do_apply    || exit 1 ;;
  rollback) do_rollback || exit 1 ;;
esac
