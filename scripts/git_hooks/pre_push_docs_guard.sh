#!/usr/bin/env bash
# ============================================================================
# pre_push_docs_guard.sh — pre-push 钩子：任何一次 push 离开本机前，按同一把尺子
#                          复查「推送区间里有没有新增/修改文档与取证材料」，命中即拒。
#
# 存在理由（★ 2026-10-15，现网第二次击穿换来的）：
#   「文档不外推」原来只有 scripts/push_code_only.sh 一条出路——**它管得住"记得用脚本的人"，
#   管不住裸 `git push` 的人**。2026-10-15 另一个并行 agent 在同一个工作树里直接 push，
#   公开仓库一次性多出 96 个取证件（34 份 .md、15 张后台界面截图、36 份闸门日志/远端读数）。
#   钩子把判据从「流程纪律」翻成「不配合也拦得住」：不需要人记得任何命令。
#
# 判据口径（三处刻意选择，改之前必读）：
#   ① **只看推送区间的「新增/修改」（--diff-filter=AM），不看整棵树**。
#      远端历史上本来就躺着早期批次推上去的文档（实测本地跟踪 .md 137 份），
#      按整棵树判会当场把每一次正常发版都拦死——那种钩子的归宿是被人 `--no-verify` 摘掉，
#      比没有钩子更坏。删除（D）永远是往干净方向走，一律放行。
#   ② **口径只有一份**：所有 pathspec 从 `scripts/doc_pathspec.excl` 现读。
#      禁止在这里再抄一份排除清单——两份尺子迟早打架（同 AGENTS「单一事实源」那一族）。
#   ③ **命中即列清单并退非零**，文案里必须写「怎么继续」的出路（走 push_code_only.sh
#      或把文档提交挪到 docs-local），而不是只说"不行"。
#
# 兜不住的两族，如实说明（别把钩子当成全部）：
#   - `git push --no-verify`、`git push --force` 带 `--no-verify`、直接在服务器上操作仓库——
#     钩子在客户端最后一里，绕过它需要**主动**违反约定；
#   - 已经把文档推上去的历史：本钩子只防**下一次**，清理已上线内容另需改写历史＋force-push（人工明令）。
#
# 安装：bash scripts/install_git_hooks.sh（把本文件挂到 .git/hooks/pre-push，可执行位一并给上）
# 自证：bash scripts/pre_push_docs_guard_selftest.sh（在 /tmp 假树里真推一次，
#        判绿只认末行 DOCGUARD_SELFTEST_ALL_OK=1）
# ============================================================================
set -uo pipefail

# 非 ASCII 路径必须关掉引号，否则 `:(exclude)产品手册/` 这类 pathspec 匹配不到任何东西，
# 整条清单会被读空＝钩子失明（这个坑在 push_code_only.sh 头部注释里有原始记录）。
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.quotePath GIT_CONFIG_VALUE_0=false

ZERO="0000000000000000000000000000000000000000"
LIST_NAME="scripts/doc_pathspec.excl"

# ---- 读那份唯一口径 -------------------------------------------------------
# 钩子可能在任意工作目录被 git 拉起，所以先拿到仓库根；
# 文件读不到 / 解析出 0 条 pathspec 时**必须判红而不是放行**：
# "尺子丢了"和"这次推送很干净"在两族故障里长得一模一样，前者更坏。
ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[ -n "$ROOT" ] || { echo "DOCGUARD ABORT=拿不到仓库根（git rev-parse --show-toplevel 失败）"; exit 1; }
LIST="$ROOT/$LIST_NAME"
[ -f "$LIST" ] || { echo "DOCGUARD ABORT=口径文件不在位：$LIST（钩子拒绝在'尺子丢了'的情况下放行）"; exit 1; }

PATHSPECS=()
while IFS= read -r line; do
  # 注释与空行跳过；其余整行当 pathspec
  case "$line" in
    ''|\#*) continue ;;
  esac
  PATHSPECS+=("$line")
done < "$LIST"
if [ "${#PATHSPECS[@]}" -lt 2 ]; then
  echo "DOCGUARD ABORT=口径文件解析出的 pathspec 少于 2 条（实得 ${#PATHSPECS[@]}），判红不判绿"
  exit 1
fi
# 正项必须在：少了 `.` 这条正项，git 会把 exclude 之外的参数区读空（＝假绿）。
HAS_POSITIVE=0
for p in "${PATHSPECS[@]}"; do
  [ "$p" = "." ] && HAS_POSITIVE=1
  case "$p" in
    *' '*) echo "DOCGUARD ABORT=口径第 N 条含空格（$p），会让 git 把它当两个参数读，判红不判绿"; exit 1 ;;
  esac
done
[ "$HAS_POSITIVE" = 1 ] || { echo "DOCGUARD ABORT=口径里没有 '.' 这条正项 ⇒ 清单必被读空，判红不判绿"; exit 1; }

# ---- 逐条引用（一次 push 可以带多条 ref）---------------------------------
BLOCKED_TOTAL=0
while read -r local_ref local_oid remote_ref remote_oid; do
  [ -z "${local_oid:-}" ] && continue
  # 删除远端分支（local_oid 全 0）不看——那是在减内容，不是加内容。
  case "$local_oid" in
    "$ZERO") continue ;;
  esac
  # 新分支（remote_oid 全 0）没有上游可比：只判"这次要送出去的那一棵树里，
  # 有没有本地历史上从没上过远端的文档"。用仓库根的空树比会误伤早期遗留，
  # 因此这里退一档：拿远端默认分支比，比不动就只报数不拦（文案里说清）。
  if [ "$remote_oid" = "$ZERO" ]; then
    base="$(git merge-base origin/HEAD "$local_oid" 2>/dev/null || true)"
    [ -n "$base" ] || base=""
  else
    base="$remote_oid"
  fi
  [ -n "$base" ] || { echo "DOCGUARD SKIP=新分支 $remote_ref 找不到比较基准（无法判定，未拦）"; continue; }
# 判据的**真正形态**（★ 夹具首跑抓到的设计错误，2026-10-15）：
#   曾经写成「拿带排除项的那一次 diff 的输出当命中清单」——那是把尺子读反了：
#   `git diff -- . ':(exclude)*.md'` 输出的是**扣掉文档之后的代码清单**，
#   于是每一次纯代码推送都被判成"要推文档"，钩子第一次自证就红在 T1/T6 两条上。
#   正确解法是**做差集**：
#     命中 = 全部变更 − 代码清单（两份都由同一次 range、同一组 pathspec 派生）
#   这样口径依然只有一份文件，而"文档类"的定义天然等于"排除项吃掉的那一部分"，
#   不可能与 push_code_only.sh 的清单打架。
  ALL="$(git diff --name-only --diff-filter=AM "$base" "$local_oid" 2>/dev/null || true)"
  CODE="$(git diff --name-only --diff-filter=AM "$base" "$local_oid" -- "${PATHSPECS[@]}" 2>/dev/null || true)"
  # 行式差集；文件名里带换行的极端形态不在本钩子射程（真出现会让计数偏小），
  # 那类文件本仓不存在，也不该由推送钩子来守——记录在此，别把它读成"已处理"。
  hits="$(comm -23 <(printf '%s\n' "$ALL" | sed '/^$/d' | LC_ALL=C sort -u) \
                   <(printf '%s\n' "$CODE" | sed '/^$/d' | LC_ALL=C sort -u))"
  n=0
  [ -n "$hits" ] && n=$(printf '%s\n' "$hits" | grep -c . || true)
  if [ "$n" -gt 0 ]; then
    BLOCKED_TOTAL=$((BLOCKED_TOTAL + n))
    echo "DOCGUARD REJECT=$remote_ref：推送区间里有 $n 个文档/取证类文件将被新增或修改"
    printf '%s\n' "$hits" | head -20 | sed 's/^/    /'
    [ "$n" -gt 20 ] && echo "    …另有 $((n - 20)) 条（完整清单：git diff --name-only --diff-filter=AM $base $local_oid -- $(printf '%s ' "${PATHSPECS[@]}")）"
  fi
done

if [ "$BLOCKED_TOTAL" -gt 0 ]; then
  cat <<'EOF'
DOCGUARD 结论：本次 push 已拦下（AGENTS「文档不外推」的机械背兜）。
出路二选一：
  A. 走纯代码链：bash scripts/push_code_only.sh        # 先干跑看清单
                 bash scripts/push_code_only.sh --apply # 复核后再真推
     （它构造的那个提交直接从 origin/<分支> 长出来，与本地历史里夹没夹文档提交无关。）
  B. 把文档提交挪到永不推送的 docs-local 分支，autosales 上只留代码提交。
判据本身要改（比如确实要把某个 .md 当交付物推上去）＝改 scripts/doc_pathspec.excl，
不要在这里加例外、也不要 --no-verify 绕过（绕过的账最后由公开仓库还）。
EOF
  exit 1
fi
exit 0
