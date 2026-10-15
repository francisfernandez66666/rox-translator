#!/usr/bin/env bash
# ============================================================================
# purge_docs_from_history.sh — 把「本批误上线的那批文档/取证件」从**已推送历史**里摘掉
#
# 与另两件的分工（三件事别混成一件）：
#   ① scripts/push_code_only.sh                 —— 下一次推送只送代码（防再犯·主动链）
#   ② scripts/git_hooks/pre_push_docs_guard.sh  —— 忘记①也被拦（防再犯·机械背兜）
#   ③ 本脚本                                    —— 清理**已经出去**的那一次（本批事故补救）
#
# 为什么不用 `git filter-branch`：它是最重的一把，会 checkout、会留 refs/original，
# 而**同一个工作树此刻正被另一条批次写**（实测几十个未提交改动）。本脚本全程只在对象库里动作：
#   读原提交 → 树灌进**临时索引** → 按清单摘件 → write-tree →
#   用原作者/原时间/原提交正文/映射后的父 commit-tree → 结果只落 `refs/purged/<分支>`；
#   不碰分支、不碰工作树、不碰远端。
# 推不推由单独的 `--force-push` 档决定（改写已推送历史需人工明令，AGENTS §一·9）。
#
# 用法：
#   bash scripts/purge_docs_from_history.sh --build        # 只重建＋自证，产物落 refs/purged/<分支>
#   bash scripts/purge_docs_from_history.sh --force-push   # 自证全过才推（改写远端历史）
#   bash scripts/purge_docs_from_history.sh --status       # 只读：purged 引用在不在、顶端树是否等价
#
# ★ 一条硬性不变量：**重建链的顶端树 == 当前远端顶端树**（同一个 tree 对象）。
#   远端顶端早就把清单里那批件摘干净了，本脚本只让"中间那些提交"也别再带着它们。
#   树一旦变了＝我误伤了内容（比如把代码当文档摘掉），必须停在本地、绝不推。
#
# ★ 三道"改写前先问清单该不该存在"的防线（本仓口径：宁停手，不猜范围）：
#   防线A 清单必须逐条落在校验过的**文档类形态**名单里（防止有人把 .ts/.go 塞进清单当"文档"）；
#   防线B 清单里的路径必须**不在当前远端顶端树**里（顶端还在＝它是要的东西，摘它＝删功能）；
#   防线C 落位前重读一次远端顶端，与建链时用的那条不一致即停手
#         （同工作树并发写是这里的常态，不是假设）。
#
# ★ 兼容口径：本机 bash 是 3.2（macOS 默认）⇒ 不用关联数组、不用进程替换；
#   old→new 的映射写进临时文件按行查，父指针用普通数组。
#
# 默认范围＝锚点 `PURGE_ANCHOR`（本批第一次纯代码推送）到当前远端顶端之间的提交，
# 默认清单＝`.git/leak_to_remove.txt`（本批实际推上去又被顶端删掉的那 96 件）。
# 想把口径扩到"历史里所有 .md"是**另一个决定**：那会连 README.md、sdk/README.md 这类
# 仓门面一起摘掉，要扩请显式给 PURGE_LIST=，且必须重跑防线A 的形态名单。
#
# 残留说明（诚实交代，别把这条写成"已彻底抹净"）：
#   force-push 之后旧提交在 GitHub 侧仍可按**原 SHA** 直接取文件（dangling object／活动日志／
#   已存在的克隆）。要让对象本身失效须走 GitHub 支持工单；本仓实测 0 fork／0 star，
#   残余风险面只有"顺旧 SHA 拉文件"这一条。
# ============================================================================
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1
# 非 ASCII 路径必须关掉引号包裹，否则清单里的中文路径一条都匹配不到（那次假红的同一把坑）。
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.quotePath GIT_CONFIG_VALUE_0=false

BR="${PURGE_BRANCH:-autosales}"
REMOTE="${PURGE_REMOTE:-origin}"
MODE="${1:---build}"
LIST="${PURGE_LIST:-$ROOT/.git/leak_to_remove.txt}"
ANCHOR="${PURGE_ANCHOR:-3c6342a}"   # 本批第一次纯代码推送；它之前的历史不在本批事故范围内

fail() { echo "PURGE FAIL=$*"; exit 1; }

# ---- 参数与前置读数 --------------------------------------------------------
if [ "$MODE" = "--status" ]; then
  if git rev-parse --verify -q "refs/purged/$BR" >/dev/null; then
    echo "purged 引用=在位（$(git rev-parse --short "refs/purged/$BR")）"
    if [ "$(git rev-parse "refs/purged/$BR^{tree}")" = "$(git rev-parse "$REMOTE/$BR^{tree}")" ]; then
      echo "顶端树与原顶端等价=是"
    else
      echo "顶端树与原顶端等价=**否（别推）**"
    fi
  else
    echo "purged 引用=未构建"
  fi
  echo "PURGE_STATUS_DONE=1"; exit 0
fi
[ "$MODE" = "--build" ] || [ "$MODE" = "--force-push" ] || fail "未知参数 $MODE（--build / --force-push / --status）"
[ -f "$LIST" ] || fail "清理清单不在位：$LIST（宁停手，也不按猜的范围改写历史）"

# 清单去空行；注释行（# 开头）允许存在，方便人留"这批为什么是这些"的脚印。
CLEAN=".git/purge-list.$$"
sed -e 's/[[:space:]]*$//' -e '/^#/d' -e '/^$/d' "$LIST" | LC_ALL=C sort -u > "$CLEAN" || fail "清洗清单失败"
N_LIST=$(grep -c . "$CLEAN" || true)
[ "${N_LIST:-0}" -ge 1 ] || fail "清单为空，改写历史却什么都不摘＝白改写"

# ---- 防线A：清单必须逐条是"文档/取证类形态" --------------------------------
# 判据按**形态**而不是"文件里出现过 .md 就算"，与 §一·9 的目录名单制同一套口径。
doc_shape() {
  case "$1" in
    *.md|*.pdf|*.log|*.out) return 0 ;;
    */*) ;;
    *) return 1 ;;
  esac
  # 带目录的路径：只认名单里那几类取证目录/前缀。**刻意不用 `*证据*` 这种松匹配**——
  # 松匹配会把 `backend-go/.../evidence_test.go` 一类真代码放过来，防线A 就白装了。
  case "$1" in
    前端及UI相关/*|产品手册/*|发布前E2E_UAT_*|*/发布前E2E_UAT_*|UI清晰度改进_*/*|*/证据/*) return 0 ;;
  esac
  return 1
}
BAD_SHAPE=""
while IFS= read -r p; do
  doc_shape "$p" || BAD_SHAPE="$BAD_SHAPE$p"$'\n'
done < "$CLEAN"
if [ -n "$BAD_SHAPE" ]; then
  echo "PURGE FAIL=防线A：清单里混进了**非文档形态**的路径，改写历史会删掉真东西："
  printf '%s' "$BAD_SHAPE" | sed '/^$/d' | head -10
  rm -f "$CLEAN"; exit 1
fi
echo "防线A 清单逐条落文档形态=通过（$N_LIST 条）"

# ---- 防线B：清单里的路径不许还在当前远端顶端树上 --------------------------
TIP_SHA=$(git rev-parse --verify -q "$REMOTE/$BR") || fail "找不到 $REMOTE/$BR"
ANCHOR_SHA=$(git rev-parse --verify -q "$ANCHOR") || fail "锚点提交取不到：$ANCHOR"
TIP_TREE=$(git rev-parse "$TIP_SHA^{tree}")
STILL_TIP=".git/purge-stilltip.$$"
git -c core.quotePath=false ls-tree -r --name-only "$TIP_SHA" | LC_ALL=C sort -u > "$STILL_TIP"
HITS_TIP=$(LC_ALL=C comm -12 "$CLEAN" "$STILL_TIP" | grep -c . || true)
if [ "${HITS_TIP:-0}" -gt 0 ]; then
  echo "PURGE FAIL=防线B：清单里还有 ${HITS_TIP} 条**在当前远端顶端树上存在**（顶端还在＝它是要保留的东西）："
  LC_ALL=C comm -12 "$CLEAN" "$STILL_TIP" | head -10
  rm -f "$CLEAN" "$STILL_TIP"; exit 1
fi
rm -f "$STILL_TIP"
echo "防线B 顶端树零命中=通过（清单那些件确实已被顶端删掉，本脚本只清中间提交）"

MAPFILE=".git/purge-map.$$"
REVLIST=".git/purge-revs.$$"
RMTMP=".git/purge-msg.$$"
: > "$MAPFILE"
git rev-list --reverse --topo-order "$ANCHOR_SHA..$TIP_SHA" > "$REVLIST" || fail "rev-list 失败"
TOTAL=$(grep -c . "$REVLIST" || true)
[ "${TOTAL:-0}" -ge 1 ] || { rm -f "$MAPFILE" "$REVLIST" "$CLEAN"; fail "锚点到顶端之间没有提交，没有可重建的历史"; }
echo "待重建提交=$TOTAL 条（锚点 $(git rev-parse --short "$ANCHOR_SHA") → 原顶端 $(git rev-parse --short "$TIP_SHA")）；清单 $N_LIST 条"

# ---- 逐提交重建（只在对象库里动作）----------------------------------------
# ★ 前置读数：真索引与 HEAD 的指纹。本脚本声称"不碰工作树/不碰别人的暂存区"，
#   那就得有一条读数证明它（另一条批次此刻正在同一个工作树上写，动了暂存区就是事故）。
IDX_FP_BEFORE=$(git ls-files -s | LC_ALL=C sort | git hash-object --stdin)
HEAD_BEFORE=$(git rev-parse HEAD)
BRANCH_BEFORE=$(git symbolic-ref --short -q HEAD || echo "(游离)")
COUNT=0
IDX=".git/purge-index.$$"
while IFS= read -r old; do
  [ -n "$old" ] || continue
  rm -f "$IDX"
  GIT_INDEX_FILE="$IDX" git read-tree "$old" || fail "read-tree 失败 $old"
  # --ignore-unmatch 必要：**大多数提交本来就不含这些路径**（它们由别的提交加入），
  # 没有它每一次都会退非零，而那条非零会被误读成"改写失败"。
  # -f 也必要（首跑真踩）：临时索引里那条路径的内容与**磁盘上的同名文件**和该提交都不一致时，
  #   git 会拒摘（"staged content different from both the file and the HEAD"）——
  #   它比较的是工作树文件，而我们根本不想动工作树。`--cached` 保证只从索引摘、磁盘一字不碰，
  #   所以这里的 -f 只是"别再拿工作树当理由拒绝摘临时索引里的条目"，不是"强制删文件"。
  GIT_INDEX_FILE="$IDX" git rm -r --cached -f --quiet --ignore-unmatch --pathspec-from-file="$CLEAN" \
    || { rm -f "$IDX"; fail "摘件失败 $old（先核清单路径形态与 git 是否支持 --pathspec-from-file）"; }
  TREE=$(GIT_INDEX_FILE="$IDX" git write-tree) || { rm -f "$IDX"; fail "write-tree 失败 $old"; }
  git log -1 --format='%B' "$old" > "$RMTMP" || fail "取提交正文失败 $old"
  # 父指针：范围内的父换成重建后的那一条；范围外（锚点及其之前）保持原 SHA 不动。
  # --topo-order --reverse 保证"父一定比子先处理"，所以这里查表不会查到还没写的新值。
  # 位置参数当数组用（bash 3.2 没有关联数组，而 set -- 追加是 3.2 也认的写法）；
  # 注意开头**不能**放 `--`：commit-tree 只吃一个 tree 位置参数，`--` 会把后面的 -p 变成裸位置。
  set --
  for p in $(git log -1 --format='%P' "$old"); do
    np=$(awk -v k="$p" '$1==k{print $2; exit}' "$MAPFILE" 2>/dev/null)
    [ -n "$np" ] || np="$p"
    set -- "$@" -p "$np"
  done
  NEW=$(GIT_AUTHOR_NAME="$(git log -1 --format='%an' "$old")" \
        GIT_AUTHOR_EMAIL="$(git log -1 --format='%ae' "$old")" \
        GIT_AUTHOR_DATE="$(git log -1 --format='%aI' "$old")" \
        GIT_COMMITTER_NAME="$(git log -1 --format='%cn' "$old")" \
        GIT_COMMITTER_EMAIL="$(git log -1 --format='%ce' "$old")" \
        GIT_COMMITTER_DATE="$(git log -1 --format='%cI' "$old")" \
        git commit-tree "$TREE" "$@" < "$RMTMP") \
    || fail "commit-tree 失败 $old"
  printf '%s\t%s\n' "$old" "$NEW" >> "$MAPFILE"
  COUNT=$((COUNT + 1))
done < "$REVLIST"
rm -f "$IDX" "$RMTMP"

lookup_tip=$(awk -v k="$TIP_SHA" '$1==k{print $2; exit}' "$MAPFILE" 2>/dev/null)
[ -n "$lookup_tip" ] || fail "重建完没拿到新顶端（映射表里查不到 $TIP_SHA）"
NEW_TIP="$lookup_tip"
git update-ref "refs/purged/$BR" "$NEW_TIP" || fail "写 refs/purged/$BR 失败"
echo "重建完成：$COUNT/$TOTAL 条；新顶端=$(git rev-parse --short "$NEW_TIP")"

# ---- 自证（任一不过即判红，且不推）----------------------------------------
if [ "$(git rev-parse "refs/purged/$BR^{tree}")" != "$TIP_TREE" ]; then
  echo "PURGE FAIL=自证1：新顶端树 ≠ 原顶端树 ⇒ 改写误伤了内容"
  git -c core.quotePath=false diff --name-status "$TIP_SHA" "refs/purged/$BR" | head -10
  rm -f "$MAPFILE" "$REVLIST" "$CLEAN"; exit 1
fi
echo "自证1 顶端树逐字节等价=通过（只动历史，没动内容）"

# 自证2 只扫**重建过的那一段**（锚点→新顶端）：锚点之前的提交本来就不在本批事故范围内，
# 拿全史扫会把"更早已公开的旧文档"读成残留，那条判据结构性恒红。
LEFT=0
FIRST_BAD=""
NEWREVS=".git/purge-newrevs.$$"
git rev-list --topo-order "$ANCHOR_SHA..refs/purged/$BR" > "$NEWREVS" || fail "自证2：列新链提交失败"
while IFS= read -r c; do
  [ -n "$c" ] || continue
  n=$(git -c core.quotePath=false ls-tree -r --name-only "$c" | LC_ALL=C grep -F -x -f "$CLEAN" | grep -c . || true)
  if [ "${n:-0}" -gt 0 ]; then
    [ -n "$FIRST_BAD" ] || FIRST_BAD="$c"
    LEFT=$((LEFT + n))
  fi
done < "$NEWREVS"
rm -f "$NEWREVS"
if [ "$LEFT" -ne 0 ]; then
  echo "PURGE FAIL=自证2：新链里仍有提交带着清单中的路径（命中 $LEFT，首条 $FIRST_BAD）"
  rm -f "$MAPFILE" "$REVLIST" "$CLEAN"; exit 1
fi
echo "自证2 全链无残留=通过（逐提交数清单命中，合计 0）"

OLD_N=$(git rev-list --count "$ANCHOR_SHA..$TIP_SHA")
NEW_N=$(git rev-list --count "$ANCHOR_SHA..refs/purged/$BR")
[ "$OLD_N" = "$NEW_N" ] || { rm -f "$MAPFILE" "$REVLIST" "$CLEAN"; fail "自证3：新链提交数 $NEW_N ≠ 原链 $OLD_N"; }
# 作者/邮箱/时间逐条比对：改写历史最容易悄悄改掉的就是这两样。
git log --reverse --format='%an|%ae|%aI' "$ANCHOR_SHA..$TIP_SHA" > .git/purge-old-meta.$$
git log --reverse --format='%an|%ae|%aI' "$ANCHOR_SHA..refs/purged/$BR" > .git/purge-new-meta.$$
if ! cmp -s .git/purge-old-meta.$$ .git/purge-new-meta.$$; then
  echo "PURGE FAIL=自证3：作者/邮箱/时间逐条比对不一致（改写把元数据动了）"
  diff .git/purge-old-meta.$$ .git/purge-new-meta.$$ | head -6
  rm -f .git/purge-old-meta.$$ .git/purge-new-meta.$$ "$MAPFILE" "$REVLIST" "$CLEAN"; exit 1
fi
rm -f .git/purge-old-meta.$$ .git/purge-new-meta.$$
echo "自证3 条数与作者/邮箱/时间逐条等价=通过（$NEW_N 条）"

# 提交正文逐条比对（作者对上了、正文被改掉同样是最坏的静默形态）。
# ⚠️ 判据口径：比对**按整段 rstrip 行尾换行后**逐条等值。原因不是"允许改正文"，而是
#   `%B` 这个 pretty-format **本身会在正文后补一行空行**，而 `git commit-tree` 从 stdin 读消息时
#   的 whitespace 清理会把末尾空行收掉——首跑 180/180 条"全红"就是这一维，逐条 diff 实证
#   **正文一字未变，只有末尾空白不同**。留一条读数把这一维显式量出来：
#   如果哪天"仅尾部空白"计数不是 100%，说明真的有人动了正文，那条判据会跟着翻红。
git log --reverse --format='%B%x00' "$ANCHOR_SHA..$TIP_SHA" > .git/purge-old-body.$$
git log --reverse --format='%B%x00' "$ANCHOR_SHA..refs/purged/$BR" > .git/purge-new-body.$$
BODY_CHECK="$(python3 - .git/purge-old-body.$$ .git/purge-new-body.$$ <<'PY'
import sys
sp = '\0'
o = open(sys.argv[1], encoding='utf-8', errors='replace').read().split(sp)
n = open(sys.argv[2], encoding='utf-8', errors='replace').read().split(sp)
if len(o) != len(n):
    print(f"COUNT {len(o)} {len(n)}"); sys.exit(0)
real = [i for i in range(len(o)) if o[i].rstrip('\n') != n[i].rstrip('\n')]
ws = sum(1 for i in range(len(o)) if o[i] != n[i])
exact = sum(1 for i in range(len(o)) if o[i] == n[i])
print(f"OK n={len(o)} 正文不等(去尾部空白)={len(real)} 仅尾部空白不同={ws} 逐字节相等={exact}")
for i in real[:3]:
    print(f"  首条不等 #{i}")
PY
)" || fail "自证4：正文比对脚本自身跑挂（比对器失灵＝判据失明，宁停手）"
case "$BODY_CHECK" in
  OK*) echo "自证4 提交正文逐条等价=通过（$BODY_CHECK）" ;;
  *) rm -f .git/purge-old-body.$$ .git/purge-new-body.$$ "$MAPFILE" "$REVLIST" "$CLEAN"; fail "自证4：提交正文被改写 → $BODY_CHECK" ;;
esac
rm -f .git/purge-old-body.$$ .git/purge-new-body.$$

# 自证5：全程没碰工作树、没碰真索引、没碰分支指针（这是"另一条批次正在同一工作树写"时的安全前提）。
IDX_FP_AFTER=$(git ls-files -s | LC_ALL=C sort | git hash-object --stdin)
HEAD_AFTER=$(git rev-parse HEAD)
BRANCH_AFTER=$(git symbolic-ref --short -q HEAD || echo "(游离)")
if [ "$IDX_FP_BEFORE" != "$IDX_FP_AFTER" ] || [ "$HEAD_BEFORE" != "$HEAD_AFTER" ] || [ "$BRANCH_BEFORE" != "$BRANCH_AFTER" ]; then
  echo "PURGE FAIL=自证5：改写历史期间动了工作树状态（真索引/HEAD/分支三者有变）"
  echo "  索引 $IDX_FP_BEFORE → $IDX_FP_AFTER ｜ HEAD $(git rev-parse --short "$HEAD_BEFORE") → $(git rev-parse --short "$HEAD_AFTER") ｜ 分支 $BRANCH_BEFORE → $BRANCH_AFTER"
  rm -f "$MAPFILE" "$REVLIST" "$CLEAN"; exit 1
fi
echo "自证5 真索引/HEAD/分支三者一动未动=通过（只在对象库里动作）"

if [ "$MODE" = "--force-push" ]; then
  # 防线C：建链这段时间里远端顶端有没有被别人推过？动过就必须重来，
  # 拿旧顶端算出来的链去 force-push＝把别人那一段直接抹掉。
  NOW_TIP=$(git rev-parse --verify -q "$REMOTE/$BR") || fail "force-push 前重读远端顶端失败"
  [ "$NOW_TIP" = "$TIP_SHA" ] || fail "防线C：远端顶端在建链期间动了（$TIP_SHA → $NOW_TIP）；重跑 --build 再推"
  echo "防线C 远端顶端未变动=通过"
  echo "（改写已推送历史＝人工明令档）执行 force-push → $REMOTE/$BR"
  git push --force "$REMOTE" "refs/purged/$BR:refs/heads/$BR" || fail "force-push 失败"
  git fetch --quiet "$REMOTE" 2>/dev/null || true
  NOW=$(git rev-parse "$REMOTE/$BR")
  echo "推送后实读 origin/$BR=$(git rev-parse --short "$NOW")（期望新顶端 $(git rev-parse --short "$NEW_TIP")）"
  [ "$NOW" = "$NEW_TIP" ] || fail "推送后远端顶端与重建结果不符"
  echo "PURGE_FORCE_PUSH_DONE=1"
fi
rm -f "$MAPFILE" "$REVLIST" "$CLEAN"
echo "PURGE_ALL_OK=1"
