#!/usr/bin/env bash
# ============================================================================
# push_code_only.sh — 「只推代码、不推文档」的机械保证（★ 2026-09-23 建立）
#
# 背景（这条脚本存在的全部理由）：本仓约定「文档提交仅本地、GitHub 上只放代码」。
# 但 2026-09-22 〇-LJ 出过一次事故：`autosales` 是线性单分支，排在纯代码提交前面的
# 两份「仅本地」文档提交成了它的祖先，`git push` 按祖先链打包，文档被一起推上远端——
# **「文档不外推」被历史结构击穿**，而不是被某条命令写错。
# 事后靠改写已推送历史补救代价极高（force-push 需用户明令，且会砸掉别人的克隆）。
#
# 根治办法有两半，本脚本是第二半：
#  ① 流程上：文档提交放到永不推送的 `docs-local` 分支（见 AGENTS.md §一·9）。
#     正常批次里 `autosales` 只有代码提交，push 天然干净。
#  ② 机制上（本脚本）：**推出去的那个提交永远直接从 `origin/<分支>` 长出来**，
#     内容 = 本地代码文件的目标状态，与本地历史里有没有夹着文档提交无关。
#     于是即便①被忘记、或历史已经像现在这样夹了文档提交，泄漏也不会发生。
#
# 用法：
#   scripts/push_code_only.sh               # 预览（干跑：只打印将要推的文件清单与判定）
#   scripts/push_code_only.sh --apply       # 真推：建纯代码提交 → 校验零 .md → push → 并轨回本地
#
# 排除口径（宁多勿漏）：
#   - 任意层级的 `*.md`（README/PROGRESS/部署指南/各类方案与报告都算文档）
#   - `前端及UI相关/`（UI 交付包与流程图目录，含 svg/png 之类大文件）
#   - 根目录 `*.png` / `*.svg` / `*.drawio` / `*.zip` 之外的流程图产物一律按目录排；
#     注：`frontend-react/public/extensions/*.zip` 是**代码交付物**（插件安装包），不排除。
#
# 安全：绝不做 force-push、绝不改写已推送历史；并轨用普通 merge，冲突即停手交人工。
# ============================================================================
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

BR="$(git rev-parse --abbrev-ref HEAD)"
REMOTE="${PUSH_REMOTE:-origin}"
TARGET="${PUSH_TARGET:-$BR}"
BASE_REF="$REMOTE/$TARGET"

APPLY=0
[ "${1:-}" = "--apply" ] && APPLY=1

# 文档/流程图的排除 pathspec。git 的 `*.md` 通配默认可跨 `/`（fnmatch 未开 PATHNAME），
# 所以一条 ':(exclude)*.md' 就能把嵌套目录里的 .md 一并排掉——不写错就是省事，写错会漏推。
EXCL=(
  -- ':(exclude)*.md'
  -- ':(exclude)前端及UI相关/'
)

git fetch --quiet "$REMOTE" "$TARGET" 2>/dev/null || echo "⚠️ fetch 失败，用本地已有的 $BASE_REF 继续（请自查网络）"
git rev-parse --verify -q "$BASE_REF" >/dev/null || { echo "❌ 找不到 $BASE_REF（首次推送请人工确认目标分支）" >&2; exit 1; }
BASE=$(git rev-parse "$BASE_REF")

# 本地待推的代码文件改动清单（BASE..HEAD 的差异，剔除文档）
CHANGED=$(git diff --name-only "$BASE" HEAD -- ':(exclude)*.md' ':(exclude)前端及UI相关/')
DOC_IN_HISTORY=$(git diff --name-only "$BASE" HEAD -- '*.md' ':(exclude)前端及UI相关/*' | wc -l | tr -d ' ')

echo "==> 当前分支 $BR；基点 $BASE_REF=${BASE:0:9}"
echo "==> 本地领先提交里含文档文件 $DOC_IN_HISTORY 个（这些**不会**被推出去）"
if [ -z "$CHANGED" ]; then
  echo "✅ 没有代码改动需要推送（$BASE_REF 已包含全部代码）。"
  exit 0
fi
echo "==> 将推送的代码文件（$(printf '%s\n' "$CHANGED" | wc -l | tr -d ' ') 个）："
printf '%s\n' "$CHANGED" | sed 's/^/   /' | head -60
if printf '%s\n' "$CHANGED" | grep -qE '\.md$|^前端及UI相关/'; then
  echo "❌ 清单里混进了文档路径，排除口径失效，停手。" >&2; exit 1
fi
if [ "$APPLY" = "0" ]; then
  echo ""
  echo "（干跑结束。确认无误后加 --apply 真推。）"
  exit 0
fi

TS=$(date +%Y%m%d_%H%M%S)
TMP="push-code-only-$TS"
echo "==> 在 $BASE_REF 之上另建纯代码分支 $TMP"
git checkout -q -b "$TMP" "$BASE" || { echo "❌ 建分支失败" >&2; git checkout -q "$BR"; exit 1; }
# 用「目标状态」而非逐个 cherry-pick：把 BASE..HEAD 的代码改动整体落到临时分支，
# 这样本地历史里夹没夹文档提交都无所谓（这正是①失效时的兜底）。
printf '%s\n' "$CHANGED" | while IFS= read -r f; do
  # ★ 判定「该取还是该删」必须问 git 对象库（cat-file -e "$BR:$f"），不能问工作区（[ -e ]）：
  #   上一步 `checkout -b $TMP $BASE` 会把「$BR 有、BASE 没有」的文件（= 本批新增文件）
  #   从磁盘物理删掉，此时 `-e` 恒为假，新增文件就被当成删除处理，
  #   纯代码提交静默少掉全部新文件（2026-09-23 首跑实测：25 个只推上去 13 个）。
  if git cat-file -e "$BR:$f" 2>/dev/null; then
    git checkout "$BR" -- "$f"
  elif git cat-file -e "$BASE:$f" 2>/dev/null; then
    git rm -q -f -- "$f" 2>/dev/null || true   # 本地确实删了：远端也要删
  else
    echo "⚠️ 跳过既不在 $BR 也不在 $BASE 的路径：$f" >&2
  fi
done
git diff --cached --quiet && { echo "✅ 暂存为空，无内容可推"; git checkout -q "$BR"; git branch -q -D "$TMP"; exit 0; }
git commit -q -m "chore: 纯代码推送 $(git rev-parse --short "$BR")（由 scripts/push_code_only.sh 生成：零 .md、零 UI 交付包；本地文档提交留在 $BR 与 docs-local 侧）" || {
  echo "❌ 临时分支提交失败" >&2; git checkout -q "$BR"; exit 1; }

# ★ 三次校验（比「零 .md」更硬）：推出去的树必须与本地代码状态逐文件相等。
#   只查 .md 会漏掉「新文件被静默丢弃」，只查文件名会漏掉「内容没取全」——两类都要堵。
MISSING=$(git diff --name-only "$BR" HEAD -- ':(exclude)*.md' ':(exclude)前端及UI相关/')
if [ -n "$MISSING" ]; then
  echo "❌ 三次校验：纯代码提交与本地代码状态仍有差异，已停在本地未推：" >&2
  printf '%s\n' "$MISSING" | sed 's/^/   /' | head -20 >&2
  git checkout -q "$BR"; git branch -q -D "$TMP" 2>/dev/null; exit 1
fi

PUSHED_FILES=$(git show --name-only --pretty=format: HEAD | sed '/^$/d')
if printf '%s\n' "$PUSHED_FILES" | grep -qiE '\.md$|^前端及UI相关/'; then
  echo "❌ 二次校验：纯代码提交里仍有文档路径，已停在本地未推。" >&2
  printf '%s\n' "$PUSHED_FILES" | grep -iE '\.md$|^前端及UI相关/' | head
  git checkout -q "$BR"; exit 1
fi
echo "==> 二次校验通过：提交含 $(printf '%s\n' "$PUSHED_FILES" | wc -l | tr -d ' ') 个文件，零 .md"

echo "==> push $TMP:$TARGET"
git push -q "$REMOTE" "HEAD:refs/heads/$TARGET" || { echo "❌ push 失败，未并轨" >&2; git checkout -q "$BR"; exit 1; }

echo "==> 并轨：把远端 $TARGET 合回本地 $BR"
git checkout -q "$BR"
git merge -q --no-edit "$BASE_REF" 2>/dev/null || git merge -q --no-edit "$TMP" || {
  echo "⚠️ 并轨冲突：请手工解决（远端已更新，本地 $BR 未动）" >&2; git branch -q -D "$TMP" 2>/dev/null; exit 1; }
git branch -q -D "$TMP"
echo "✅ 已推送 $(git rev-parse --short "$BASE_REF" 2>/dev/null || echo '?')；本地 $BR 与远端并轨完成"
git log --oneline -2 | cat
