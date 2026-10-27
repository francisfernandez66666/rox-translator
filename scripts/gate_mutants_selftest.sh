#!/usr/bin/env bash
# ============ gate_mutants_selftest.sh · 职责说明 ============
# meta 闸门执行器的**自举反证**：在一个假仓库里造几种"坏登记表"，要求执行器给出
# 与设计一致的退出码与 FAIL/ABORT 分类。判绿只认末行 `GATEMUT_SELFTEST_ALL_OK=1`。
#
# 为什么必须有它：执行器本身也是代码。没有这层，"变异闸门全绿"可能只是执行器压根没跑（或永远退 0）。
#
# 五个场景（对应主方案《闸门射程与反证登记》§4 的 T1/T2/T3/T4/T5）：
#   T1 正确变异 → exit 0
#   T2 锚点 0 命中 → ABORT（exit 2）
#   T3 锚点 2 命中 → ABORT（exit 2）
#   T4 变异没改坏（锁仍绿）→ FAIL（exit 1）
#   T5 锁红了但消息不匹配 → FAIL（exit 1）
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/.." && pwd)"
WORK="${GATEMUT_HOME:-/tmp/gatemut_selftest}"
rm -rf "$WORK"; mkdir -p "$WORK"
PASS=0; FAILN=0
ok()  { echo "OK:$*"; PASS=$((PASS+1)); }
bad() { echo "BAD:$*"; FAILN=$((FAILN+1)); }
b64() { python3 -c 'import base64,sys;sys.stdout.write(base64.b64encode(sys.argv[1].encode()).decode())' "$1"; }

# 造一个最小假仓：一条 shell 锁 + 一个目标文件 + 三张表。
mk_repo() {
  local dir="$1"
  rm -rf "$dir"; mkdir -p "$dir/scripts"
  ( cd "$dir" && git init -q && git config user.email t@t && git config user.name t )
  printf 'KEEP\n' > "$dir/target.txt"
  cat > "$dir/scripts/lock.sh" <<'EOF'
#!/usr/bin/env bash
grep -q KEEP target.txt || { echo "锁：目标被改坏"; exit 1; }
exit 0
EOF
  chmod +x "$dir/scripts/lock.sh"
  printf 'id\tlock\tkind\trun\ttarget\tfind_b64\treplace_b64\texpect_msg_b64\towner\tsince\tnote\n' > "$dir/scripts/gate_mutants.tsv"
  printf 'id\tlock\tkind\tanchor\twhere\texpect\tnote\n' > "$dir/scripts/gate_anchors.tsv"
  printf 'id\tlock\tinject_kind\tinject_target\tinject_b64\texpect_msg_b64\towner\tsince\tnote\n' > "$dir/scripts/gate_scope.tsv"
  ( cd "$dir" && git add -A && git commit -qm init )
}

add_row() { # dir find replace expect
  printf 'M0001\tscripts/lock.sh\tshell\t-\ttarget.txt\t%s\t%s\t%s\tt\t2026-10-28\tselftest\n' \
    "$(b64 "$2")" "$(b64 "$3")" "$(b64 "$4")" >> "$1/scripts/gate_mutants.tsv"
}

run_case() { # dir expect_rc
  local dir="$1" want="$2" out rc
  out="$( GATEMUT_REPO="$dir" bash "$REPO_ROOT/scripts/gate_mutants.sh" --check 2>&1 )"
  rc=$?
  if [ "$rc" -ne "$want" ]; then
    bad "退出码 ${rc}，期望 ${want}；输出：$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-300)"
    return 1
  fi
  return 0
}

# T1 正确变异：锁必须红在锁自己的消息上。
D="$WORK/t1"; mk_repo "$D"; add_row "$D" "KEEP" "BROKEN" "锁：目标被改坏"
if run_case "$D" 0 && grep -q 'GATEMUT_ALL_OK=1' <<<"$( GATEMUT_REPO="$D" bash "$REPO_ROOT/scripts/gate_mutants.sh" --check 2>&1 )"; then
  ok "T1 正确变异 → 全绿"
else bad "T1 未按预期通过"; fi

# T2 锚点 0 命中 → ABORT。
D="$WORK/t2"; mk_repo "$D"; add_row "$D" "NO_SUCH_ANCHOR" "X" "锁：目标被改坏"
if run_case "$D" 2; then ok "T2 锚点缺失 → ABORT"; else bad "T2 未 ABORT"; fi

# T3 锚点 2 命中 → ABORT。
D="$WORK/t3"; mk_repo "$D"; printf 'KEEP KEEP\n' > "$D/target.txt"; add_row "$D" "KEEP" "BROKEN" "锁：目标被改坏"
( cd "$D" && git add -A && git commit -qm dup )
if run_case "$D" 2; then ok "T3 锚点不唯一 → ABORT"; else bad "T3 未 ABORT"; fi

# T4 变异没改坏（KEEP→KEEP）→ 锁仍绿 → FAIL。
D="$WORK/t4"; mk_repo "$D"; add_row "$D" "KEEP" "KEEP" "锁：目标被改坏"
if run_case "$D" 1 && grep -q 'lock-stayed-green' <<<"$( GATEMUT_REPO="$D" bash "$REPO_ROOT/scripts/gate_mutants.sh" --check 2>&1 )"; then
  ok "T4 锁仍绿 → FAIL"
else bad "T4 未判 lock-stayed-green"; fi

# T5 锁红了但消息不匹配 → FAIL wrong-reason。
D="$WORK/t5"; mk_repo "$D"; add_row "$D" "KEEP" "BROKEN" "这条消息根本不存在"
if run_case "$D" 1 && grep -q 'wrong-reason' <<<"$( GATEMUT_REPO="$D" bash "$REPO_ROOT/scripts/gate_mutants.sh" --check 2>&1 )"; then
  ok "T5 消息不匹配 → FAIL wrong-reason"
else bad "T5 未判 wrong-reason"; fi

echo "PASS=$PASS FAIL=$FAILN"
if [ "$FAILN" -ne 0 ]; then exit 1; fi
echo "GATEMUT_SELFTEST_ALL_OK=1"
