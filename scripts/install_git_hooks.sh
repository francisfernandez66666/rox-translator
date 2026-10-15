#!/usr/bin/env bash
# ============================================================================
# install_git_hooks.sh — 把本仓自带的钩子挂进 .git/hooks（★ 2026-10-15 新建）
#
# 为什么需要它：`.git/hooks/` **不进版本控制**。所以「仓里有这道守卫」和「这台机器上跑了这道守卫」
# 是两件事——按 AGENTS 那条「有脚本无调度要问到第二层」，只把钩子脚本提交进仓＝又一次
# 「有脚本、没装上、一次都没响过」。本脚本就是把「装上」这一步也变成一条命令。
#
# 用法：
#   bash scripts/install_git_hooks.sh            # 安装/更新（只动我们自己那一个钩子文件）
#   bash scripts/install_git_hooks.sh --status   # 只读：这台装没装、装的内容与仓内是否一致
#   bash scripts/install_git_hooks.sh --uninstall# 摘除（只摘我们自己那枚，不碰别人的 pre-push）
#
# 纪律三条：
#   ① 目标已存在时按**签名**分派，不按"内容是否相同"分派：
#      是本仓那份的旧版本（含 `pre_push_docs_guard.sh` 签名行）⇒ 更新；
#      内容不同且不带签名 ⇒ 那是别人的钩子，一律停手报告、绝不覆盖
#      （覆盖了就把别人的守卫换成我们的，而且没人会知道）。
#      ——这条判别是安装器第二跑自证逼出来的：钩子改一版再装，旧判据会把自己的过期件当外人拒掉。
#   ② 装完必须**回读**（cmp 内容 ＋ 可执行位）才说"装上了"——写完不读是"以为装好了"的标准形态。
#   ③ 判绿只认末行 `HOOKINSTALL_ALL_OK=1`，中间读数不算结论。
# ============================================================================
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

SRC="scripts/git_hooks/pre_push_docs_guard.sh"
HOOKS_DIR="$(git rev-parse --git-path hooks)"
DST="$HOOKS_DIR/pre-push"
MODE="${1:-install}"

fail() { echo "HOOKINSTALL FAIL=$*"; exit 1; }
[ -f "$SRC" ] || fail "仓内钩子源文件不在位：$SRC"

# 只读状态档：装没装／是否与仓内一致／有没有可执行位，三项各出一行现读值。
if [ "$MODE" = "--status" ]; then
  if [ -f "$DST" ]; then
    if cmp -s "$SRC" "$DST"; then echo "状态=已安装且与仓内一致"; else echo "状态=已安装但内容与仓内不一致（可能有人改过目标或源）"; fi
    [ -x "$DST" ] && echo "可执行位=有" || echo "可执行位=**缺**（缺了钩子根本不会被 git 调用）"
  else
    echo "状态=**未安装**（这台机器上裸 push 不会被拦）"
  fi
  echo "HOOKINSTALL_STATUS_DONE=1"
  exit 0
fi

if [ "$MODE" = "--uninstall" ]; then
  if [ -f "$DST" ] && cmp -s "$SRC" "$DST"; then
    rm -f "$DST" && echo "已摘除 $DST（只摘本仓这一份；内容不同的一律不碰）"
  else
    echo "未摘除：目标不在位或内容与仓内不同，绝不替别人删钩子"
  fi
  echo "HOOKINSTALL_UNINSTALL_DONE=1"
  exit 0
fi

# 安装档
# 「目标已存在且不同」要分两种，判据只能是**那枚钩子是谁写的**，不是"内容一不一样"：
#   - 内容是本仓这一份 ⇒ 幂等，什么都不用做；
#   - 内容不同但带着本仓钩子的签名行（我们自己那份的标题）⇒ 是**过期版本的自己**，更新它；
#   - 两者都不是 ⇒ 那是别人的钩子，绝不覆盖（覆盖了就把别人的守卫换成我们的，且没人会知道）。
OWN_SIGNATURE='pre_push_docs_guard.sh'
if [ -e "$DST" ] && ! cmp -s "$SRC" "$DST"; then
  if grep -q "$OWN_SIGNATURE" "$DST" 2>/dev/null; then
    echo "目标是本仓钩子的旧版本 ⇒ 更新到当前仓内版本"
  else
    fail "$DST 已存在且是**别人的**钩子（内容不同、也不带本仓签名行）——不覆盖。要共存请在你那份末尾加一行：
     bash \"\$(git rev-parse --show-toplevel)/$SRC\""
  fi
fi
cp "$SRC" "$DST" || fail "复制失败"
chmod +x "$DST" || fail "chmod 失败"

# ② 回读：装没装成**只认磁盘上那一刻的读数**
cmp -s "$SRC" "$DST" || fail "写入内容与源不一致（回读等值判红）"
[ -x "$DST" ] || fail "可执行位缺失，钩子不会被 git 调用"

# 顺带把仓内唯一口径的在位性也验一次：钩子运行时读不到它会判红，那比不装更吵，
# 所以这里提前出声。
[ -f "scripts/doc_pathspec.excl" ] || fail "口径文件 scripts/doc_pathspec.excl 不在位（钩子会拒绝放行每一次 push）"

echo "已安装：$DST（可执行位已给，内容与仓内逐字节一致）"
echo "下一步自证：bash scripts/pre_push_docs_guard_selftest.sh"
echo "HOOKINSTALL_ALL_OK=1"
