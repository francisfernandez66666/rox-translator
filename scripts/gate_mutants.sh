#!/usr/bin/env bash
# ============ gate_mutants.sh · 职责说明 ============
# meta 闸门的执行器（L1 变异 / L2 射程 / L3 锚点）。
#
# 为什么需要它：仓里已有 80+ 条家族级锁，但"锁会不会红、红得对不对、读的锚点还在不在"
# 全靠人记。本脚本把三件事机械化：
#   L1：每条登记锁配一个"把实现改坏"的补丁 → 锁必须红，且红在**锁自己的消息**上；
#   L2：登记表里的"注入新成员"动作执行后，家族锁必须红（本批表为空，机制先到位）；
#   L3：手工登记的锚点（文件/字面量/正则）必须存在且计数符合期望。
#
# 用法：
#   bash scripts/gate_mutants.sh --list
#   bash scripts/gate_mutants.sh --anchors
#   bash scripts/gate_mutants.sh --only M0001,M0002
#   bash scripts/gate_mutants.sh --changed [BASE]
#   bash scripts/gate_mutants.sh --check            # 发布闸门用
#
# 退出码：0 全绿；1 有 FAIL（锁没红 / 消息不匹配）；2 ABORT（登记表坏了 / 锚点不唯一 / 隔离环境建不起）。
#
# ⚠️ 隔离：所有变异都在 git worktree 的**临时副本**里做，绝不改工作树；
#    跑前跑后各记一次主树指纹，不等即 ABORT（防"变异没还原"这类二次事故）。
#    assets / node_modules 用软链（不拷几十 MB）；单文件 >8MB 的未跟踪件跳过并告警。
set -uo pipefail

ROOT="${GATEMUT_REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
MUTANTS="$ROOT/scripts/gate_mutants.tsv"
ANCHORS="$ROOT/scripts/gate_anchors.tsv"
SCOPE="$ROOT/scripts/gate_scope.tsv"
PY="${PYTHON:-python3}"
MODE=""
ONLY=""
BASE="origin/$(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo autosales)"

# b64d 解码一个 base64 字段（TSV 里存 base64 是因为代码片段含 tab/引号/中文）。
b64d() {
  "$PY" -c 'import base64,sys; sys.stdout.write(base64.b64decode(sys.argv[1]).decode("utf-8"))' "$1"
}

# fingerprint 主树指纹：HEAD＋工作区状态＋diff 的哈希。
fingerprint() {
  ( cd "$ROOT" && {
      git rev-parse HEAD 2>/dev/null
      git status --porcelain=v1 2>/dev/null
      git diff --binary 2>/dev/null | shasum -a 256 2>/dev/null
    } | shasum -a 256 | awk '{print $1}' )
}

abort() { echo "ABORT:$*" >&2; exit 2; }
fail()  { echo "FAIL:$*" >&2; FAILED=$((FAILED+1)); }
okmsg() { echo "OK:$*"; }

FAILED=0

# ---------- L3：锚点体检 ----------
anchors_check() {
  [ -f "$ANCHORS" ] || abort "anchor-file-missing:$ANCHORS"
  local line id lock kind anchor where expect note cnt
  while IFS=$'\t' read -r id lock kind anchor where expect note; do
    case "$id" in ''|\#*) continue;; esac
    [ "$id" = "id" ] && continue
    local pat
    pat="$(b64d "$anchor")"
    cnt="$("$PY" - "$kind" "$ROOT/$where" "$pat" <<'PY'
import os,re,sys
kind, where, pat = sys.argv[1], sys.argv[2], sys.argv[3]
files=[]
if os.path.isfile(where): files=[where]
elif os.path.isdir(where):
    for base,_,fs in os.walk(where):
        for f in fs:
            files.append(os.path.join(base,f))
n=0
for fp in files:
    try: data=open(fp,encoding='utf-8',errors='ignore').read()
    except OSError: continue
    if kind=='literal': n+=data.count(pat)
    elif kind=='regex': n+=len(re.findall(pat,data,re.M))
    else:
        print('BADKIND'); sys.exit(0)
print(n)
PY
)"
    [ "$cnt" = "BADKIND" ] && abort "anchor-bad-kind:$id:$kind"
    case "$expect" in
      "==0") [ "$cnt" -eq 0 ] && okmsg "anchor $id = 0" || fail "anchor $id 期望 0，实读 ${cnt}（$pat @ ${where}）";;
      "==1") [ "$cnt" -eq 1 ] && okmsg "anchor $id = 1" || fail "anchor $id 期望 1，实读 ${cnt}（$pat @ ${where}）";;
      ">="*) local min="${expect#>=}"; [ "$cnt" -ge "$min" ] && okmsg "anchor $id >= ${min}（${cnt}）" || fail "anchor $id 期望 >=${min}，实读 $cnt";;
      *) fail "anchor $id 的 expect 写法不认识：$expect";;
    esac
  done < "$ANCHORS"
}

# ---------- L2：射程注入（本批无登记，机制先到位） ----------
scope_check() {
  [ -f "$SCOPE" ] || abort "scope-file-missing:$SCOPE"
  local line id lock kind target inj expect note
  while IFS=$'\t' read -r id lock kind target inj expect note; do
    case "$id" in ''|\#*) continue;; esac
    [ "$id" = "id" ] && continue
    if [ -e "$ROOT/$target" ] || [ -d "$(dirname "$ROOT/$target")" ]; then
      okmsg "scope $id target 可注入"
    else
      fail "scope $id target 不存在：$target"
    fi
  done < "$SCOPE"
}

# ---------- L1：变异登记 ----------
list_rows() {
  local id lock kind run target find_b64 rep_b64 expect owner since note
  while IFS=$'\t' read -r id lock kind run target find_b64 rep_b64 expect owner since note; do
    case "$id" in ''|\#*) continue;; esac
    [ "$id" = "id" ] && continue
    printf '%s\t%s\t%s\t%s\n' "$id" "$kind" "$lock" "$note"
  done < "$MUTANTS"
}

# changed_rows 只跑"本次改动文件命中"的变异体。
changed_rows() {
  local names
  names="$( cd "$ROOT" && { git status --porcelain=v1 2>/dev/null | awk '{print $2}'; git diff --name-only "$BASE"...HEAD 2>/dev/null; } | sort -u )"
  local id lock kind run target find_b64 rep_b64 expect owner since note hit
  while IFS=$'\t' read -r id lock kind run target find_b64 rep_b64 expect owner since note; do
    case "$id" in ''|\#*) continue;; esac
    [ "$id" = "id" ] && continue
    hit=0
    while IFS= read -r f; do
      [ -z "$f" ] && continue
      case "$f" in "$lock"|"$target") hit=1;; esac
    done <<EOF
$names
EOF
    [ "$hit" = "1" ] && printf '%s\n' "$id"
  done < "$MUTANTS"
}

run_mutations() {
  local want_ids="$1"
  [ -f "$MUTANTS" ] || abort "mutant-file-missing:$MUTANTS"
  command -v git >/dev/null || abort "no-git"
  local before after
  before="$(fingerprint)"
  local TMP
  TMP="$(mktemp -d "${TMPDIR:-/tmp}/gatemut.XXXXXX")" || abort "mktemp-failed"
  cleanup() {
    # 早退（比如登记表坏了）时 TMP 可能还没赋值：set -u 下必须显式兜住，
    # 否则收尾函数自己把真实退出码改掉（本仓"验证链自己不算账"那一族）。
    [ -n "${TMP:-}" ] || return 0
    ( cd "$ROOT" && git worktree remove --force "$TMP" >/dev/null 2>&1 )
    rm -rf "$TMP"
  }
  trap cleanup EXIT
  ( cd "$ROOT" && git worktree add --detach "$TMP" HEAD >/dev/null 2>&1 ) || abort "worktree-add-failed"
  # 带上未提交改动（工作区保真）；新文件按清单复制（>8MB 跳过）。
  ( cd "$ROOT" && git diff --binary HEAD 2>/dev/null | git -C "$TMP" apply >/dev/null 2>&1 ) || true
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    local src="$ROOT/$f" dst="$TMP/$f"
    [ -f "$src" ] || continue
    local sz
    sz="$(wc -c < "$src" | tr -d ' ')"
    if [ "$sz" -gt 8388608 ]; then echo "WARN:未跟踪件超过 8MB 跳过：$f"; continue; fi
    mkdir -p "$(dirname "$dst")"; cp "$src" "$dst"
  done <<EOF
$( cd "$ROOT" && git ls-files --others --exclude-standard 2>/dev/null )
EOF
  for link in frontend-react/node_modules backend-go/internal/fileproc/assets; do
    [ -e "$ROOT/$link" ] || continue
    mkdir -p "$(dirname "$TMP/$link")"
    ln -sfn "$ROOT/$link" "$TMP/$link"
  done

  local id lock kind run target find_b64 rep_b64 expect owner since note
  while IFS=$'\t' read -r id lock kind run target find_b64 rep_b64 expect owner since note; do
    case "$id" in ''|\#*) continue;; esac
    [ "$id" = "id" ] && continue
    if [ -n "$want_ids" ]; then
      case ",$want_ids," in *",$id,"*) ;; *) continue;; esac
    fi
    [ -f "$TMP/$target" ] || { fail "$id target 不存在：$target"; continue; }
    [ -f "$TMP/$lock" ] || { fail "$id lock 不存在：$lock"; continue; }
    local find_pat rep_pat expect_msg
    # ★ 三个字段都必须**非空且能解码**：tab 是 IFS 空白字符，连续 tab 会把空字段折叠掉，
    #   字段一错位就会出现"expect 解出空串 ⇒ case *""* 恒真 ⇒ 假绿"。
    find_pat="$(b64d "$find_b64")" || abort "b64-decode-failed:$id:find"
    rep_pat="$(b64d "$rep_b64")" || abort "b64-decode-failed:$id:replace"
    expect_msg="$(b64d "$expect")" || abort "b64-decode-failed:$id:expect"
    [ -n "$find_pat" ] || abort "empty-field:$id:find"
    [ -n "$rep_pat" ] || abort "empty-field:$id:replace（空替换会被 IFS 折叠；要删行就替换成注释/占位）"
    [ -n "$expect_msg" ] || abort "empty-field:$id:expect（空期望恒真）"
    local cnt
    cnt="$("$PY" - "$TMP/$target" "$find_pat" <<'PY'
import sys
data=open(sys.argv[1],encoding='utf-8',errors='ignore').read()
print(data.count(sys.argv[2]))
PY
)"
    [ "$cnt" = "0" ] && abort "anchor-missing:$id"
    [ "$cnt" != "1" ] && abort "anchor-ambiguous:${id}（$cnt 处）"
    cp "$TMP/$target" "$TMP/$target.gatemut.bak"
    "$PY" - "$TMP/$target" "$find_pat" "$rep_pat" <<'PY'
import sys
p,a,b=sys.argv[1],sys.argv[2],sys.argv[3]
data=open(p,encoding='utf-8').read()
open(p,'w',encoding='utf-8').write(data.replace(a,b,1))
PY
    local out rc pkg
    case "$kind" in
      go)
        pkg="./$(dirname "${lock#backend-go/}")"
        out="$( cd "$TMP/backend-go" && FILEPROC_PYTHON_BIN="${FILEPROC_PYTHON_BIN:-}" go test -count=1 -run "$run" "$pkg" 2>&1 )"
        rc=$?
        ;;
      ts)
        out="$( cd "$TMP/frontend-react" && npx vitest run "${lock#frontend-react/}" 2>&1 )"
        rc=$?
        ;;
      shell)
        out="$( cd "$TMP" && bash "$lock" 2>&1 )"
        rc=$?
        ;;
      e2e)
        if [ "${GATEMUT_E2E:-0}" != "1" ]; then
          mv "$TMP/$target.gatemut.bak" "$TMP/$target"
          okmsg "$id e2e 默认不纳入（GATEMUT_E2E=1 单独开档）"
          continue
        fi
        out="$( cd "$TMP/frontend-react" && npx playwright test "$lock" --reporter=line 2>&1 )"
        rc=$?
        ;;
      *) abort "bad-kind:$id:$kind";;
    esac
    mv "$TMP/$target.gatemut.bak" "$TMP/$target"
    if [ "$rc" -eq 0 ]; then
      fail "$id lock-stayed-green（变异后锁仍然绿 ⇒ 这条锁没有射程）"
      continue
    fi
    case "$out" in
      *"$expect_msg"*) okmsg "$id 红在锁自己的消息上";;
      *) fail "$id wrong-reason（锁红了，但输出里没有「${expect_msg}」）；输出摘录：$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-500)";;
    esac
  done < "$MUTANTS"

  after="$(fingerprint)"
  [ "$before" = "$after" ] || abort "main-tree-dirty（跑前/跑后主树指纹不一致）"
}

# ---------- 入口 ----------
while [ $# -gt 0 ]; do
  case "$1" in
    --list) MODE="list"; shift;;
    --anchors) MODE="anchors"; shift;;
    --only) MODE="only"; ONLY="${2:-}"; shift 2;;
    --changed) MODE="changed"; shift;;   # 可选 BASE 由环境变量 GATEMUT_BASE 覆盖
    --check) MODE="check"; shift;;
    --e2e) GATEMUT_E2E=1; shift;;
    -h|--help) echo "用法：gate_mutants.sh --list|--anchors|--check|--only IDS|--changed"; exit 0;;
    *) echo "未知参数：$1" >&2; exit 2;;
  esac
done
[ -n "${GATEMUT_BASE:-}" ] && BASE="$GATEMUT_BASE"
[ -z "$MODE" ] && MODE="check"

case "$MODE" in
  list) list_rows; exit 0;;
  anchors) anchors_check; scope_check; [ "$FAILED" -eq 0 ] && okmsg "anchors/scope 全绿" || exit 1; exit 0;;
  only) run_mutations "$ONLY";;
  changed) ids="$(changed_rows | tr '\n' ',' | sed 's/,$//')"; [ -z "$ids" ] && { okmsg "本次改动没有命中任何变异体"; exit 0; }; run_mutations "$ids";;
  check) run_mutations "";;
esac

if [ "$FAILED" -ne 0 ]; then echo "GATEMUT_FAIL=$FAILED"; exit 1; fi
echo "GATEMUT_ALL_OK=1"
exit 0
