#!/usr/bin/env bash
# tier3_postdeploy_20261125.sh — 两站 tier3 上线后开关与采集源启用。
#
# 先校验本批二进制、健康面与三档模型，再写销售双开关和 313/314；
# 所有写操作幂等，失败立即停手，成功后逐库回读。默认干跑，APPLY=1 才写。
set -euo pipefail

HOST="${DEPLOY_HOST:-43.108.86.140}"
PORT="${DEPLOY_PORT:-28022}"
APPLY="${APPLY:-0}"
EXPECT_BIN_SHA="${EXPECT_BIN_SHA:-c941355a567aa04028093176f87cd02c651bf9e1f1e7a944c61d8aec2d413c8e}"

ssh -p "$PORT" -o ConnectTimeout=20 -o BatchMode=yes "root@$HOST" 'bash -s' -- "$APPLY" "$EXPECT_BIN_SHA" <<'REMOTE'
set -euo pipefail
APPLY="$1"
EXPECT_BIN_SHA="$2"

for root in /opt/translator /opt/translator-demo; do
  test -x "$root/bin/translator-server"
  got=$(sha256sum "$root/bin/translator-server" | cut -d' ' -f1)
  [ "$got" = "$EXPECT_BIN_SHA" ] || { echo "BAD binary $root got=$got"; exit 2; }
  entry=$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$root/web/index.html" | head -1)
  [ "$entry" = "assets/index-CMiT0hq0.js" ] || { echo "BAD entry $root got=$entry"; exit 2; }
done

for port in 8787 8789; do
  curl -fsS --max-time 15 "http://127.0.0.1:$port/api/health" | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d.get("status")=="ok"
assert d.get("assist",{}).get("mode")=="local"
assert d.get("assist",{}).get("ready") is True
assert d.get("knowledge",{}).get("reachable") is True
' || { echo "BAD health port=$port"; exit 2; }
done

for db in langcross langcross_demo; do
  models=$(sudo -u postgres psql -X -A -t -d "$db" -c "SELECT ((value::jsonb)->'initial_evals'->>'model')||'|'||((value::jsonb)->'review_evals'->>'model')||'|'||((value::jsonb)->'review'->>'model') FROM system_config WHERE key='stage_models';")
  [ "$models" = "THUDM/GLM-4-9B-0414|THUDM/GLM-4-9B-0414|deepseek-v4.1-flash-1017" ] || { echo "BAD models $db got=$models"; exit 2; }
  count=$(sudo -u postgres psql -X -A -t -d "$db" -c "SELECT COUNT(*) FROM kb_pack_sources WHERE id IN (313,314);")
  [ "$count" = "2" ] || { echo "BAD sources $db count=$count"; exit 2; }
done

for f in /opt/translator/data/assist.db /opt/translator-demo/data/assist.db; do
  test -f "$f"
  sqlite3 "$f" 'SELECT 1;' >/dev/null
done

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 前置校验全绿；未写库。加 APPLY=1 才启用。"
  exit 0
fi

for db in langcross langcross_demo; do
  sudo -u postgres psql -X -v ON_ERROR_STOP=1 -d "$db" -c "UPDATE kb_pack_sources SET enabled=1 WHERE id IN (313,314);"
done
for f in /opt/translator/data/assist.db /opt/translator-demo/data/assist.db; do
  sqlite3 "$f" "BEGIN; INSERT INTO configs(key,value) VALUES('sales_facts_injection_enabled','1'),('sales_claim_guard_enabled','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value; COMMIT;"
done

for db in langcross langcross_demo; do
  state=$(sudo -u postgres psql -X -A -t -d "$db" -c "SELECT string_agg(id||':'||enabled,',' ORDER BY id) FROM kb_pack_sources WHERE id IN (313,314);")
  [ "$state" = "313:1,314:1" ] || { echo "BAD source readback $db got=$state"; exit 3; }
  echo "SOURCES $db $state"
done
for f in /opt/translator/data/assist.db /opt/translator-demo/data/assist.db; do
  state=$(sqlite3 "$f" "SELECT key||'='||value FROM configs WHERE key IN ('sales_facts_injection_enabled','sales_claim_guard_enabled') ORDER BY key;" | paste -sd, -)
  [ "$state" = "sales_claim_guard_enabled=1,sales_facts_injection_enabled=1" ] || { echo "BAD assist readback $f got=$state"; exit 3; }
  echo "ASSIST $f $state"
done
echo "TIER3_POSTDEPLOY_ALL_OK=1"
REMOTE
