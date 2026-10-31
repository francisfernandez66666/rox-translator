#!/usr/bin/env bash
# ============ deploy/set_assist_profile_key.sh · 职责说明 ============
# 给「助手画像」链路配置**同一个** HMAC 共享密钥（★ 2026-10-30 三事合一批 组7 §8.2 P-8）。
#
# 链路：主服务（translator／translator-demo）组装画像并用这把钥匙签 HMAC，
#       挂件服务（ai-assist）验签。三份 secrets.env 里必须是**同一个值**——
#       不一致的症状不是报错，而是「画像永久回落匿名」：验签永远算不出同一个 Mac，
#       界面上挂件照常工作，没有人会发现画像没生效。
#
# 用法（部署机上跑，需要 root）：
#   bash deploy/set_assist_profile_key.sh --status   # 只读：三份文件各有没有这一行、哪些 unit 在跑
#   bash deploy/set_assist_profile_key.sh --apply    # 落位：生成/保留密钥 → 三份文件同步 → 重启三个 unit
#
# 口径：
#   · **绝不打印密钥值**（截断也不行）——本脚本所有输出只有「行数/长度/是否一致」这类读数。
#   · 已有值优先复用（apply 时取三份里第一份非空值；都没有才新生成 32 字节 hex）：
#     避免每次重跑把主服务与挂件的钥匙改成一新一旧。
#   · 写前每份留 `.bak.<TS>`，权限保持 0600、属主 root:root（与既有 secrets.env 同档）。
#   · 回滚：把 `.bak.<TS>` 覆盖回去并重启三个 unit（画像消失、挂件照常，属设计内的降级）。
# =============================================
set -uo pipefail

ENV_FILES=(
  /etc/translator/secrets.env
  /etc/translator-demo/secrets.env
  /etc/ai-assist/secrets.env
)
UNITS=(translator translator-demo ai-assist)
KEY_NAME=ASSIST_PROFILE_KEY
MODE="${1:-}"

case "$MODE" in
  --status|--apply) ;;
  ""|-h|--help) awk '/^set -uo/{exit} {print}' "$0"; exit 0 ;;
  *) echo "未知参数：${MODE}（--status／--apply）" >&2; exit 2 ;;
esac

fail() { echo "BAD  $*" >&2; FAIL=$((FAIL + 1)); }
say()  { printf '%s\n' "$*"; }
FAIL=0

# 读取某份文件的现有值（不打印；只在函数内比较）。
current() {
  awk -F= -v k="$KEY_NAME" '$1==k {sub(/^[^=]*=/, ""); print; exit}' "$1" 2>/dev/null
}

say "===== 助手画像密钥（${KEY_NAME}）====="
rexisting=""
for f in "${ENV_FILES[@]}"; do
  if [ ! -f "$f" ]; then
    if [ "$MODE" = "--status" ]; then
      echo "⏳   $f 不存在（该单元可能未部署）"
    else
      fail "$f 不存在（先确认部署形态，不在本脚本里造 secrets 文件）"
    fi
    continue
  fi
  n=$(grep -c "^${KEY_NAME}=" "$f" 2>/dev/null || true)
  v=$(current "$f")
  if [ -n "$v" ]; then
    [ -z "$rexisting" ] && rexisting="$v"
    echo "OK   ${f}：${KEY_NAME} 行数=${n} 长度=${#v}"
  else
    echo "⏳   ${f}：${KEY_NAME} 未配置（行数=${n}）"
  fi
done

if [ "$MODE" = "--status" ]; then
  for u in "${UNITS[@]}"; do
    printf '     %-18s %s\n' "$u" "$(systemctl is-active "$u" 2>/dev/null || echo unknown)"
  done
  echo "SET_PROFILE_KEY_STATUS_FAIL=$FAIL"
  exit $([ "$FAIL" -eq 0 ] && echo 0 || echo 1)
fi

# ---- --apply ----
key="$rexisting"
if [ -z "$key" ]; then
  key="$(openssl rand -hex 32)"
  echo "OK   三份都没有现值 ⇒ 已生成新密钥（长度=${#key}，不打印内容）"
else
  echo "OK   复用现有密钥（长度=${#key}，不打印内容）"
fi

TS=$(date +%Y%m%d_%H%M%S)
for f in "${ENV_FILES[@]}"; do
  [ -f "$f" ] || continue
  cp -a "$f" "$f.bak.$TS" || { fail "备份 $f 失败"; continue; }
  # 用 python 原地改写：有该键则替换值，没有则追加；保持其余行逐字不动。
  KEY="$key" KEY_NAME="$KEY_NAME" FILE="$f" python3 - <<'PY' || { fail "写入 $f 失败"; continue; }
import os, sys
path = os.environ["FILE"]; name = os.environ["KEY_NAME"]; val = os.environ["KEY"]
lines = open(path, encoding="utf-8").read().splitlines()
out, hit = [], False
for ln in lines:
    if ln.startswith(name + "="):
        out.append(name + "=" + val); hit = True
    else:
        out.append(ln)
if not hit:
    out.append(name + "=" + val)
open(path, "w", encoding="utf-8").write("\n".join(out) + "\n")
PY
  chown root:root "$f" && chmod 600 "$f" || fail "权限修正 $f 失败"
  echo "OK   ${f} 已写入（备份 $(basename "$f").bak.${TS}）"
done

for u in "${UNITS[@]}"; do
  systemctl restart "$u" || fail "重启 $u 失败"
  sleep 1
done

for u in "${UNITS[@]}"; do
  st=$(systemctl is-active "$u" 2>/dev/null || true)
  [ "$st" = "active" ] && echo "OK   $u=$st" || fail "$u=$st"
done

# 一致性自证：三份现值长度与逐字相等（不打印值本身）。
first=""
for f in "${ENV_FILES[@]}"; do
  [ -f "$f" ] || continue
  v=$(current "$f")
  [ -z "$first" ] && first="$v"
  [ "$v" = "$first" ] && echo "OK   $(basename "$f") 与首份一致" || fail "$(basename "$f") 与首份不一致（画像会永久匿名）"
done

echo "SET_PROFILE_KEY_APPLY_FAIL=$FAIL"
exit $([ "$FAIL" -eq 0 ] && echo 0 || echo 1)
