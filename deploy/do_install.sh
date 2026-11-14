#!/usr/bin/env bash
# ============================================================================
# deploy/do_install.sh — 四期 W3 知识服务部署编排（部署机上跑，一条命令）
#
# 为什么需要这一层：知识服务「独立进程 + 独立库」的部署有**一处必须协调的隐藏依赖**——
# 知识服务端 authOK 要求 token 非空（config.go:52，空 token 全拒），主服务端
# knowledge.Client 只在 Token 非空时才发 X-Knowledge-Token（client.go:113）。
# ⇒ translator.service 与知识服务 unit 的 KNOWLEDGE_TOKEN **必须相同**，否则 401。
# 本脚本把这条协调做成幂等自动化，避免手工漏配。
#
# 顺序（施工图 §8.3）：
#   ① 取主库 DSN / driver（secrets.env）
#   ② 协调 KNOWLEDGE_TOKEN（secrets.env 优先 → 主服务 unit → 生成并写回）
#   ③ install_knowledge_service.sh --apply（派生 KNOWLEDGE_DB_DSN + 写 unit + 起服务）
#   ④ knowledge-migrate（主库四表 → 知识库，幂等 upsert + 双向指纹校验）
#   ⑤ check_knowledge_face.sh（只读面检三态）
#
# 判绿：末行 KNOWLEDGE_FACE_ALL_OK=1。退出码 0＝全绿。
# 可覆盖 env：SECRETS / UNIT_MAIN / UNIT_KNOWLEDGE / KNOWLEDGE_BIN / KNOWLEDGE_BASE_URL
# ============================================================================
set -uo pipefail

SECRETS=${SECRETS:-/etc/translator/secrets.env}
UNIT_MAIN=${UNIT_MAIN:-/etc/systemd/system/translator.service}
UNIT_KNOWLEDGE=${UNIT_KNOWLEDGE:-/etc/systemd/system/translator-knowledge.service}
KNOWLEDGE_BIN=${KNOWLEDGE_BIN:-/opt/translator/bin/knowledge-server}
BASE_URL=${KNOWLEDGE_BASE_URL:-http://127.0.0.1:8791}
HERE="$(cd "$(dirname "$0")" && pwd)"

# ---- ① 主库 DSN / driver ----
[ -f "$SECRETS" ] || { echo "BAD secrets.env 不在：$SECRETS"; exit 2; }
DB_DSN=$(grep '^DB_DSN=' "$SECRETS" | head -1 | cut -d= -f2-)
[ -n "$DB_DSN" ] || { echo "BAD 读不到 DB_DSN"; exit 2; }
DB_DRIVER=$(grep '^DB_DRIVER=' "$SECRETS" | head -1 | cut -d= -f2-)
DB_DRIVER=${DB_DRIVER:-postgres}

# ---- 派生 KNOWLEDGE_DB_DSN（库名 → langcross_knowledge，保留用户名/主机/参数）----
# ★ 只用「最后一个 / 后的库名」正则，避免误伤 //user 段（原 do_install.sh 的
#   sed "s#/langcross#/langcross_knowledge#" 会先命中 //langcross 用户名段 → 28P01）。
KNOWLEDGE_DB_DSN=$(printf '%s' "$DB_DSN" | sed -E 's#/([^/?]+)(\?.*)?$#/langcross_knowledge\2#')
KNOWLEDGE_DB_DRIVER=postgres

# ---- ② 协调 KNOWLEDGE_TOKEN（两服务必须一致）----
# 单一事实源：secrets.env。主服务若用 EnvironmentFile 指向它，写回即自动生效。
EXISTING=$(grep '^KNOWLEDGE_TOKEN=' "$SECRETS" | head -1 | cut -d= -f2-)
if [ -n "$EXISTING" ]; then
  KNOWLEDGE_TOKEN="$EXISTING"
  echo "REUSE KNOWLEDGE_TOKEN from secrets.env"
else
  # 看主服务 unit 是否已有（极少数情况 token 只写在 unit 没进 secrets.env）
  UNIT_TOK=$(grep -oE 'Environment=KNOWLEDGE_TOKEN=[^[:space:]]+' "$UNIT_MAIN" 2>/dev/null | head -1 | cut -d= -f3-)
  if [ -n "$UNIT_TOK" ]; then
    KNOWLEDGE_TOKEN="$UNIT_TOK"
    printf 'KNOWLEDGE_TOKEN=%s\n' "$KNOWLEDGE_TOKEN" >> "$SECRETS"
    echo "REUSE KNOWLEDGE_TOKEN from $UNIT_MAIN，已回写 secrets.env"
  else
    KNOWLEDGE_TOKEN=$(openssl rand -hex 32)
    sed -i.bak '/^KNOWLEDGE_TOKEN=/d' "$SECRETS"
    printf 'KNOWLEDGE_TOKEN=%s\n' "$KNOWLEDGE_TOKEN" >> "$SECRETS"
    echo "GENERATED new KNOWLEDGE_TOKEN，已写回 secrets.env"
  fi
fi

# 主服务 unit 若没吃到 secrets.env（没有 EnvironmentFile），显式补 Environment= 并重启
if grep -q 'EnvironmentFile=.*secrets.env' "$UNIT_MAIN" 2>/dev/null; then
  echo "OK  $UNIT_MAIN 已用 EnvironmentFile 指向 secrets.env，token 自动生效（重启后）"
else
  if ! grep -q 'Environment=KNOWLEDGE_TOKEN=' "$UNIT_MAIN" 2>/dev/null; then
    echo "PATCH $UNIT_MAIN 无 EnvironmentFile：补 Environment=KNOWLEDGE_TOKEN 并重启"
    sed -i.bak '/^\[Service\]/a Environment=KNOWLEDGE_TOKEN='"$KNOWLEDGE_TOKEN" "$UNIT_MAIN"
    systemctl daemon-reload
    systemctl restart translator.service
  else
    echo "OK  $UNIT_MAIN 已有 KNOWLEDGE_TOKEN Environment"
  fi
fi

# ---- ③ 安装知识服务 unit（派生已在脚本内做，这里显式传 KNOWLEDGE_DB_DSN 保证一致）----
KNOWLEDGE_DB_DRIVER=$KNOWLEDGE_DB_DRIVER \
DB_DSN="$DB_DSN" \
KNOWLEDGE_DB_DSN="$KNOWLEDGE_DB_DSN" \
KNOWLEDGE_TOKEN="$KNOWLEDGE_TOKEN" \
KNOWLEDGE_SERVICE_READ=off \
KNOWLEDGE_BIN="$KNOWLEDGE_BIN" \
bash "$HERE/install_knowledge_service.sh" --apply || exit 1

# ---- ④ 迁四表（读上面导出的主库/知识库 DSN）----
DB_DRIVER="$DB_DRIVER" \
DB_DSN="$DB_DSN" \
KNOWLEDGE_DB_DRIVER=$KNOWLEDGE_DB_DRIVER \
KNOWLEDGE_DB_DSN="$KNOWLEDGE_DB_DSN" \
"$KNOWLEDGE_BIN"-migrate || exit 1

# ---- ⑤ 只读面检三态 ----
KNOWLEDGE_TOKEN="$KNOWLEDGE_TOKEN" \
KNOWLEDGE_BASE_URL="$BASE_URL" \
bash "$HERE/check_knowledge_face.sh" || exit 1

echo "DEPLOY_KNOWLEDGE_ALL_OK=1"
