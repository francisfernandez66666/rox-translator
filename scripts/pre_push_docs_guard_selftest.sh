#!/usr/bin/env bash
# ============================================================================
# pre_push_docs_guard_selftest.sh — pre-push 文档守卫的**射程自证**（★ 2026-10-15）
#
# 为什么必须有它：守卫脚本本身「写了」和「拦得住」是两件事。AGENTS 那条
# 「只有默认档才会走到的腿，必须由夹具实跑一次」在这里格外致命——这道钩子最常见的
# 失效形态（pathspec 读空、`--` 混进参数区、可执行位没给）全都是**静默放行**，
# 读代码看不出来，屏幕上还是一片绿。
#
# 本机跑、只在 $DOCGUARD_TEST_HOME（默认 /tmp/docguard_selftest）下的假树里动作，
# **绝不碰本仓工作区、绝不碰真实远端**。判绿只认末行 `DOCGUARD_SELFTEST_ALL_OK=1`。
#
# 七段设计（正测＋反测成对，缺一条就不知道该信哪一半）：
#   T1 纯代码推送 ⇒ 必须**放行**（否则每次发版都被拦＝钩子被人 --no-verify 摘掉）
#   T2 根目录 .md 新增 ⇒ 必须**拦下**
#   T3 嵌套 .md（backend-go/.../README.md）⇒ 必须**拦下**（验 `*.md` 真能跨目录）
#   T4 目录名单类：UI清晰度改进_20990101/shots/*.png ＋ 发布前E2E_UAT_20990101/证据/*.log
#      ⇒ 必须**拦下**（这两族是 10-11 与 10-15 两次真事故的形状）
#   T5 删除文档（清理方向）⇒ 必须**放行**（验 --diff-filter=AM 那一档不是"见 md 就杀"）
#   T6 远端上早就躺着的遗留文档，本次只推代码 ⇒ 必须**放行**
#      （验判据问的是"推送区间"而不是"整棵树"——否则本仓永远推不动）
#   T7 反证：把口径文件里的正项 `.` 摘掉 ⇒ 必须**判红（ABORT）而不是放行**
#      （"尺子丢了"不许冒充"这次很干净"）
# ============================================================================
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOME_TEST="${DOCGUARD_TEST_HOME:-/tmp/docguard_selftest}"
SRC_HOOK="$ROOT/scripts/git_hooks/pre_push_docs_guard.sh"
SRC_LIST="$ROOT/scripts/doc_pathspec.excl"
[ -f "$SRC_HOOK" ] || { echo "DOCGUARD_SELFTEST FAIL=源钩子不在位 $SRC_HOOK"; exit 1; }
[ -f "$SRC_LIST" ] || { echo "DOCGUARD_SELFTEST FAIL=口径文件不在位 $SRC_LIST"; exit 1; }

FAILS=0
note() { echo "  · $*"; }
expect() { # expect <用例名> <期望 ok|reject|abort> <实际退出码> <输出>
  local name="$1" want="$2" rc="$3" out="$4" got
  case "$rc" in
    0) got=ok ;;
    1) got=reject ;;
    *) got=abort ;;
  esac
  if [ "$want" = abort ]; then
    # 判红档允许 1 或非 0，但**必须**带 ABORT 字样（那是"尺子丢了"那一族的专用出口）
    if [ "$rc" != 0 ] && printf '%s' "$out" | grep -q 'DOCGUARD ABORT'; then got=abort; fi
  fi
  if [ "$got" = "$want" ]; then
    echo "PASS $name（want=$want rc=$rc）"
  else
    echo "FAIL $name want=$want got=$got rc=$rc"
    printf '%s\n' "$out" | sed 's/^/      │ /' | head -8
    FAILS=$((FAILS + 1))
  fi
}

# ---- 搭一棵假树 -----------------------------------------------------------
rm -rf "$HOME_TEST" 2>/dev/null
mkdir -p "$HOME_TEST" || { echo "DOCGUARD_SELFTEST FAIL=建不了 $HOME_TEST"; exit 1; }
cd "$HOME_TEST" || exit 1
git init -q --bare remote.git
git init -q work
# 钩子要能在一个「没有 .git/hooks 之外的东西」的环境里跑，所以照真实安装形态：
# 复制进 work/.git/hooks/pre-push，并给可执行位（这一步本身也是被测对象的一部分）。
mkdir -p work/scripts/git_hooks
cp "$SRC_LIST" work/scripts/doc_pathspec.excl
cp "$SRC_HOOK" work/scripts/git_hooks/pre_push_docs_guard.sh
mkdir -p work/.git/hooks
cp "$SRC_HOOK" work/.git/hooks/pre-push
chmod +x work/.git/hooks/pre-push
[ -x work/.git/hooks/pre-push ] || { echo "DOCGUARD_SELFTEST FAIL=夹具自己没装上可执行位"; exit 1; }

cd work || exit 1
git config user.email t@example.com
git config user.name docguard-test
git config commit.gpgsign false
git remote add origin "$HOME_TEST/remote.git"

commit_push() { # commit_push <消息> ⇒ 返回 push 退出码，输出进全局 OUT
  git add -A >/dev/null 2>&1
  git commit -q -m "$1" >/dev/null 2>&1
  OUT="$(git push -q origin HEAD:refs/heads/main 2>&1)"; RC=$?
}

# 先落一个代码基线（第一次推送时远端还没有 main ⇒ 走"新分支"那一档，因此这里刻意先把
# 基准建起来，后面所有用例都在"有上游可比"的形态上测，判据更干净）。
mkdir -p src
echo 'package main' > src/base.go
commit_push "夹具基线：代码"
# 新分支那一档允许"找不到比较基准 ⇒ 只报数不拦"，所以基线可能因 origin/HEAD 缺失而放行；
# 这里把远端默认 HEAD 指到 main，让后续用例都有基准。
git -C "$HOME_TEST/remote.git" symbolic-ref HEAD refs/heads/main
git remote set-head origin main >/dev/null 2>&1
git fetch -q origin

# T1 纯代码推送 ⇒ 放行
echo 'func Extra() {} // 第二行' >> src/base.go
expect_t1() { commit_push "T1 代码"; expect "T1 纯代码放行" ok "$RC" "$OUT"; }
expect_t1

# T2 根目录 .md ⇒ 拦下
echo "# 一次性方案稿" > 方案_T2.md
commit_push "T2 根目录文档"
expect "T2 根 .md 拦下" reject "$RC" "$OUT"
git reset -q --hard HEAD~1 2>/dev/null; rm -f 方案_T2.md

# T3 嵌套 .md ⇒ 拦下（验 *.md 跨目录真的生效）
mkdir -p backend-go/internal/demo
echo "嵌套 README" > backend-go/internal/demo/README.md
commit_push "T3 嵌套文档"
expect "T3 嵌套 .md 拦下" reject "$RC" "$OUT"
git reset -q --hard HEAD~1 2>/dev/null; rm -rf backend-go

# T4 目录名单两族：截图 ＋ 证据日志 ⇒ 各拦一次
mkdir -p "UI清晰度改进_20990101/shots" "发布前E2E_UAT_20990101/证据"
printf 'PNGFAKE' > "UI清晰度改进_20990101/shots/01_工作台_并排.png"
echo "gate log 读数" > "发布前E2E_UAT_20990101/证据/gate_T4.log"
commit_push "T4 取证目录"
expect "T4 截图+证据日志拦下" reject "$RC" "$OUT"
git reset -q --hard HEAD~1 2>/dev/null
rm -rf "UI清晰度改进_20990101" "发布前E2E_UAT_20990101"

# T5 删除文档 ⇒ 放行（清理方向不许被拦）
echo "# 早期遗留文档" > 遗留_T5.md
git add -A >/dev/null 2>&1; git commit -q -m "T5 前置：先造一个已跟踪的文档" >/dev/null 2>&1
# 夹具**故意**把一份 .md 送上远端，好造出"远端躺着遗留文档"那一形态。
# 这一步必须 --no-verify：这是夹具搭建而不是被测行为，被测的是"之后只推代码时别被遗留件拖红"。
git push -q --no-verify origin HEAD:refs/heads/main >/dev/null 2>&1
rm -f 遗留_T5.md
commit_push "T5 删除文档"
expect "T5 删除文档放行" ok "$RC" "$OUT"

# T6 远端已有遗留文档、本次只推代码 ⇒ 放行（判据是"区间"不是"整棵树"）
echo 'func More() {}' >> src/base.go
commit_push "T6 只推代码，树里仍躺着别的遗留文档"
expect "T6 遗留不动只推代码放行" ok "$RC" "$OUT"

# T7 反证：摘掉口径文件里的正项 `.` ⇒ 必须 ABORT（尺子丢了不许冒充干净）
python3 - <<'PY'
import re,io
p="scripts/doc_pathspec.excl"
s=open(p,encoding="utf-8").read().splitlines()
out=[l for l in s if l.strip()!="."]
open(p,"w",encoding="utf-8").write("\n".join(out)+"\n")
PY
git add -A >/dev/null 2>&1; git commit -q -m "T7 反证：摘掉正项" >/dev/null 2>&1
echo 'func T7() {}' >> src/base.go
commit_push "T7 反证推送"
expect "T7 口径缺正项必须判红" abort "$RC" "$OUT"

# 汇总
cd "$ROOT" || exit 1
echo "FAILS=$FAILS"
if [ "$FAILS" = 0 ]; then
  echo "DOCGUARD_SELFTEST_ALL_OK=1"
  exit 0
fi
echo "DOCGUARD_SELFTEST_ALL_OK=0"
exit 1
