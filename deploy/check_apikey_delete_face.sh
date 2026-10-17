#!/usr/bin/env bash
# check_apikey_delete_face.sh — 「API Key 后台删掉了没有」的只读面检（2026-10-16 修「删不掉」的配套闸门）
#
# 为什么要有这一份：那条缺陷的现场形态是**接口回 success、库里那行原封不动**，
# 所以「管理台点了删除、页面刷新后那行不见了」并不足以收尾——必须看**库侧事实**与**新代码路径的留痕**。
#
# 用法：
#   bash deploy/check_apikey_delete_face.sh 56        # 复验 id=56 是否真删掉（删前跑一次＝1、删后跑一次＝0）
#   bash deploy/check_apikey_delete_face.sh --list    # 列出全部 Key（id/租户/名称/状态/前缀）
#
# 判据（全只读，不写库、不动服务）：
#   D1 目标行存在性（打印 count，期望：删除前 1、删除后 0）
#   D2 审计留痕 audit_logs.action='apikey_delete' 且 detail=<id>（★ 这条只有**新代码**会写：
#      旧 handler 根本不写审计——所以 D2 命中＝"新件真的被走到"，比看版本号可靠）
#   D3 平台视角总量与按租户分布（超管列表就是这一份，用来解释"列表字节数一字不变"那种症状）
#   D4 缺陷机制对照：同一个 id 在 `AND tenant_id=0` 过滤下必然 0 行（旧件就是拿它当删除条件）
set -uo pipefail

HOST="${SSH_HOST:-root@43.108.86.140}"
PORT="${SSH_PORT:-28022}"
MODE="${1:-}"

if [ -z "$MODE" ]; then
  echo "用法: bash deploy/check_apikey_delete_face.sh <key_id> | --list"
  exit 2
fi

ssh -o BatchMode=yes -o ConnectTimeout=15 -p "$PORT" "$HOST" bash -s -- "$MODE" <<'REMOTE_EOF'
set -uo pipefail
MODE="$1"
Q() { sudo -u postgres psql -d langcross -Atc "$1" 2>/dev/null; }

if [ "$MODE" = "--list" ]; then
  echo "----- 全部 API Key（平台视角＝超管列表就是这一份）-----"
  Q "select id||' | tid='||tenant_id||' | '||name||' | '||status||' | '||key_prefix||' | last_used='||COALESCE(NULLIF(last_used_at,''),'<从未>') from api_keys order by id;"
  echo "总数=$(Q 'select count(*) from api_keys;')"
  exit 0
fi

ID="$MODE"
case "$ID" in ''|*[!0-9]*) echo "ID 必须是数字"; exit 2 ;; esac

FAIL=0
echo "===== D1 目标行存在性（id=${ID}）====="
n=$(Q "select count(*) from api_keys where id=$ID;")
echo "  count(id=$ID)=$n"
if [ "$n" = "0" ]; then echo "  OK 该行已不在库里（删除确实落库了）"; else echo "  READOUT 该行仍在库里（删前跑＝正常基线；删后跑＝没删掉）"; fi

echo "===== D2 新代码路径留痕（审计 apikey_delete）====="
a=$(Q "select count(*) from audit_logs where action='apikey_delete' and detail='$ID';")
echo "  audit_logs(action=apikey_delete, detail=$ID)=$a"
if [ "${a:-0}" -ge 1 ] 2>/dev/null; then
  echo "  OK 命中 ⇒ 新件的 handler 真被走到过（旧件不写这条审计）"
  Q "select '    最近一条: '||COALESCE(NULLIF(created_at,''),'<无>')||' tid='||tenant_id||' uid='||user_id from audit_logs where action='apikey_delete' and detail='$ID' order by id desc limit 1;"
else
  echo "  NOTE 未命中 ⇒ 要么还没删过，要么走的仍是旧件（先看 /api/health 与件内特征串）"
fi

echo "===== D3 平台视角总量与租户分布 ====="
echo "  总数=$(Q 'select count(*) from api_keys;')"
Q "select '  tid='||tenant_id||' 共 '||count(*)||' 把' from api_keys group by tenant_id order by tenant_id;"

echo "===== D4 缺陷机制对照（旧件的删除条件）====="
echo "  count(id=$ID AND tenant_id=0)=$(Q "select count(*) from api_keys where id=$ID and tenant_id=0;")（旧件拿它当 WHERE：恒 0 ⇒ 恒「删不掉」却回 success）"

[ "$FAIL" -eq 0 ] || echo "FAIL=$FAIL"
REMOTE_EOF
