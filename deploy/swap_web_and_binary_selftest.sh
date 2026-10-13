#!/usr/bin/env bash
# ============ swap_web_and_binary_selftest.sh · 职责说明 ============
# 给 `deploy/swap_web_and_binary.sh` 那两道**形状守卫**与**两段式闸门**做机械自证的夹具（在**本机**跑，不进现网）。
#
# 为什么需要它：守卫的绿灯有两种来源——「守卫真拦住了」和「恰好没走到守卫」。只看正例分不清这两者，
# 所以这里六段里五段是**反证**：把守卫摘掉必须**真出事**，绿灯才算来自守卫本身（AGENTS §三「给读路径装缓存」
# 那条族的通用写法：凡负向腿恒零命中，必须补一枚"往那侧塞假目标"的反证）。
#
# 用法：
#   bash deploy/swap_web_and_binary_selftest.sh          # 全跑（默认）
#   SWAPTEST_HOME=/tmp/swaptest_selftest bash deploy/swap_web_and_binary_selftest.sh
# 判绿只认末行 `SWAPTEST_ALL_OK=1`＋exit 0；任何一段的期望读数不对就点名列出并退非 0。
#
# ★ 三条安全口径（都是 2026-10-13 首跑真踩出来的）：
#   ① 全程只在 `$SWAPTEST_HOME` 里动作，且**每次都显式带 `DEPLOY_DIRS`** 指向夹具树——
#      同一条命令里曾把脚本跑两遍、第一遍忘带 env，于是打在 mac 的真 `/opt` 路径上（判据没错，接线漏了一层）；
#      本脚本在入口就把「`DEPLOY_DIRS` 必须在夹具根下」钉成断言，不设第二道防线。
#   ② 夹具的**语种 chunk 文件名必须按现网 dist 的真实形态造**：正则
#      `^[a-z]{2}(-[a-zA-Z]+)?-[A-Za-z0-9_-]{8}\.js$` 命中十份恰为 `ar/de/es/fr/ja/ko/pt/ru/th/zh-hant`；
#      写成 `zh_hant`（下划线）或哈希段给 9 字符，夹具自己会判红——那是**夹具错**，不是守卫错。
#   ③ mac/Linux 工具口径差：`stat -c%s`／`sha256sum` 在 mac 上不存在，桩目录把它们统一成 Linux 形态
#      （mac 侧 `sha256sum` 桩回落到 `shasum -a 256`，Linux 侧回落 `/usr/bin/sha256sum`，输出格式一致）。
#
# ⚠️ 本夹具**不碰现网、不碰 caddy、不需要 ssh**；它只证明守卫的射程，不证明现网换源成功
#    （现网那一跳的验收仍按《部署指南》§五／§十：只认 `SWAP_ALL_OK=1`＋`deploy/postdeploy_*.sh`）。
# ================================================================
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SRC=${SRC:-$HERE/swap_web_and_binary.sh}
H=${SWAPTEST_HOME:-/tmp/swaptest_selftest}
[ -f "$SRC" ] || { echo "BAD_SRC 找不到待自证的执行体：$SRC"; exit 1; }

rm -rf "$H"; mkdir -p "$H"

# ---------- 期望计数（每条 expect 一次判定，末行汇总）----------
BAD_EXPECT=0
# expect <标签> <实际> <期望>：不等即点名，并抬 BAD_EXPECT
expect() {
  local label="$1" got="$2" want="$3"
  if [ "$got" = "$want" ]; then
    echo "EXPECT_OK   $label = $got"
  else
    echo "EXPECT_BAD  $label 期望 $want 实得 $got"
    BAD_EXPECT=$((BAD_EXPECT + 1))
  fi
}
# expect nonzero/zero：退出码只问"是不是 0"，不写死具体 BAD 条数（条数随判据增长会变）
expect_zero()    { if [ "${1:-1}" -eq 0 ];   then echo "EXPECT_OK   $2 退 0";   else echo "EXPECT_BAD  $2 应退 0，实退 $1"; BAD_EXPECT=$((BAD_EXPECT + 1)); fi }
expect_nonzero() { if [ "${1:-0}" -ne 0 ];   then echo "EXPECT_OK   $2 退非 0（$1）"; else echo "EXPECT_BAD  $2 本该拒绝却退 0"; BAD_EXPECT=$((BAD_EXPECT + 1)); fi }

# ---------- 桩目录（PATH 前置，把 Linux 口径搬到 mac）----------
mkdir -p "$H/bin"
cat > "$H/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
# 桩：restart 只记一行到 STUB_LOG；is-active 恒回 active（本夹具不测服务真起没起）
[ "${1:-}" = restart ] && echo "STUB systemctl $*" >> "${STUB_LOG:-/dev/null}"
[ "${1:-}" = is-active ] && echo active
exit 0
EOF
cat > "$H/bin/curl" <<'EOF'
#!/usr/bin/env bash
# 桩：回环 /livez 一律 200（守卫段与 livez 无关，别让本机没有 curl 干扰）
echo 200
EOF
cat > "$H/bin/stat" <<'EOF'
#!/usr/bin/env bash
# mac 的 stat 没有 -c%s；把 GNU 那一档换成 wc -c，其余原样透传
if [ "${1:-}" = "-c%s" ]; then wc -c < "$2" | tr -d ' '; else /usr/bin/stat "$@"; fi
EOF
cat > "$H/bin/sha256sum" <<'EOF'
#!/usr/bin/env bash
# 两侧输出格式统一为 "<hash>  <path>"（cut -d' ' -f1 取首格），且必须避开自递归
if command -v shasum >/dev/null 2>&1; then exec shasum -a 256 "$@"; fi
exec /usr/bin/sha256sum "$@"
EOF
chmod +x "$H/bin"/*
export PATH="$H/bin:$PATH"
export STUB_LOG="$H/stub.log"; : > "$STUB_LOG"

# ---------- 假 dist：入口名＋语种 chunk 十份，全部按现网真实形态 ----------
mkdir -p "$H/src/dist/assets"
python3 - "$H/src/dist" <<'PY'
import sys
d = sys.argv[1]
entry = "assets/index-A1B2C3D4.js"          # index- + 8 位哈希（形状守卫要求 ≥8）
langs = ["ar", "de", "es", "fr", "ja", "ko", "pt", "ru", "th", "zh-hant"]  # = 现网那十份
for i, lg in enumerate(langs):
    open(f"{d}/assets/{lg}-0123456{i}.js", "w").write("x" * 40)
open(f"{d}/" + entry, "w").write("y" * 512)
open(f"{d}/" + entry + ".br", "w").write("b" * 120)   # 预压缩侧车也在位（T1 会点读数）
open(f"{d}/" + entry + ".gz", "w").write("g" * 140)
open(f"{d}/assets/index-A1B2C3D4.css", "w").write("c" * 60)
open(f"{d}/index.html", "w").write(
    f'<html><head><script type="module" src="/{entry}"></script>'
    f'<link rel="stylesheet" href="/assets/index-A1B2C3D4.css"></head><body><div id="root"></div></body></html>\n')
PY
(cd "$H/src" && COPYFILE_DISABLE=1 tar czf "$H/dist.tar.gz" --exclude='._*' --exclude='.DS_Store' dist)
TAR_SHA=$(shasum -a 256 "$H/dist.tar.gz" 2>/dev/null | cut -d' ' -f1 || sha256sum "$H/dist.tar.gz" | cut -d' ' -f1)
head -c 300 /dev/urandom > "$H/fakebin"
BIN_SHA=$(shasum -a 256 "$H/fakebin" 2>/dev/null | cut -d' ' -f1 || sha256sum "$H/fakebin" | cut -d' ' -f1)

# ---------- 假部署树：两棵「部署根/{bin,data,log,web}」，web 里放老一代 ----------
make_tree() {
  local r="$1"
  rm -rf "$r"
  mkdir -p "$r/bin" "$r/data" "$r/log" "$r/web/assets"
  echo "old-entry" > "$r/web/index.html"
  echo "old-asset" > "$r/web/assets/index-OLDOLD00.js"
  printf '#!/bin/sh\necho old\n' > "$r/bin/translator-server"
  chmod +x "$r/bin/translator-server"
  echo row > "$r/data/app.db"; echo venv > "$r/data/.venv_marker"
}
build_trees() { make_tree "$H/opt/translator"; make_tree "$H/opt/translator-demo"; }
build_trees
tree_fp() { find "$H/opt" | sort | shasum -a 256 2>/dev/null | cut -d' ' -f1 || find "$H/opt" | sort | sha256sum | cut -d' ' -f1; }
DD="$H/opt/translator $H/opt/translator-demo"

# ★ 安全断言：待自证的执行体在任何一段里都只准看见夹具树（写在夹具根之外一律停手）
case "$DD" in
  "$H"/opt/*) : ;;
  *) echo "BAD_HOME DEPLOY_DIRS 不在夹具根 $H 下（实得 $DD）——拒绝执行"; exit 1 ;;
esac

echo "############ T1 正测（根＝<部署根>/web，应整段成功） ############"
DEPLOY_DIRS="$DD" bash "$SRC" "$H/dist.tar.gz" "$TAR_SHA" "$H/fakebin" "$BIN_SHA"
T1=$?
expect_zero "$T1" "T1 正测"
expect "T1_web入口" "$(grep -oE 'assets/[^"]*\.js' "$H/opt/translator/web/index.html" | head -1)" "assets/index-A1B2C3D4.js"
expect "T1_二进制已是新代" "$([ "$(sha256sum < "$H/opt/translator/bin/translator-server" | cut -d' ' -f1)" = "$BIN_SHA" ] && echo yes || echo NO)" "yes"
expect "T1_归档份数" "$(ls -d "$H/opt/translator/web".old.* 2>/dev/null | wc -l | tr -d ' ')" "1"
expect "T1_无web.new残留" "$(find "$H/opt" -maxdepth 2 -name 'web.new' | wc -l | tr -d ' ')" "0"
expect "T1_两站入口同代" "$(cmp -s "$H/opt/translator/web/index.html" "$H/opt/translator-demo/web/index.html" && echo yes || echo NO)" "yes"

echo
echo "############ T2 反证 A：把**部署根**当根传＝07:44 那次错接线（应整段拒绝、树一个字节不动） ############"
build_trees; FP0=$(tree_fp)
sed 's#stage_site "$d/web"#stage_site "$d"#' "$SRC" > "$H/deploy_root.sh"
DEPLOY_DIRS="$DD" bash "$H/deploy_root.sh" "$H/dist.tar.gz" "$TAR_SHA" "$H/fakebin" "$BIN_SHA"
T2=$?
expect_nonzero "$T2" "T2 传部署根"
expect "T2_树一个字节未动" "$([ "$(tree_fp)" = "$FP0" ] && echo yes || echo NO)" "yes"
expect "T2_data还在原位" "$([ -f "$H/opt/translator/data/app.db" ] && echo yes || echo NO)" "yes"
expect "T2_二进制未被换" "$([ "$(sha256sum < "$H/opt/translator/bin/translator-server" | cut -d' ' -f1)" = "$BIN_SHA" ] && echo NO || echo yes)" "yes"
expect "T2_无.new或.old残留" "$(find "$H/opt" -maxdepth 2 \( -name 'web.new' -o -name '*.old.*' \) | wc -l | tr -d ' ')" "0"

echo
echo "############ T3 反证 B：tar sha 给错（应在任何动作之前 abort） ############"
FP1=$(tree_fp)
DEPLOY_DIRS="$DD" bash "$SRC" "$H/dist.tar.gz" "0000000000000000000000000000000000000000000000000000000000000000" "$H/fakebin" "$BIN_SHA"
T3=$?
expect_nonzero "$T3" "T3 sha 给错"
expect "T3_树一个字节未动" "$([ "$(tree_fp)" = "$FP1" ] && echo yes || echo NO)" "yes"

echo
echo "############ T4 反证 C：首页引用不存在的 asset（应判死且不换名、不换二进制） ############"
build_trees
python3 - "$H/src/dist" <<'PY'
import sys
d = sys.argv[1]
open(f"{d}/index.html", "w").write(
    '<html><head><script type="module" src="/assets/index-A1B2C3D4.js"></script>'
    '<link rel="stylesheet" href="/assets/GONE-9999.css"></head><body></body></html>\n')
PY
(cd "$H/src" && COPYFILE_DISABLE=1 tar czf "$H/dist2.tar.gz" --exclude='._*' --exclude='.DS_Store' dist)
DEPLOY_DIRS="$DD" bash "$SRC" "$H/dist2.tar.gz" "$(sha256sum "$H/dist2.tar.gz" | cut -d' ' -f1)" "$H/fakebin" "$BIN_SHA"
T4=$?
expect_nonzero "$T4" "T4 首页缺件"
expect "T4_web仍为老一代" "$([ "$(cat "$H/opt/translator/web/index.html")" = "old-entry" ] && echo yes || echo NO)" "yes"
expect "T4_二进制未被换" "$([ "$(sha256sum < "$H/opt/translator/bin/translator-server" | cut -d' ' -f1)" = "$BIN_SHA" ] && echo NO || echo yes)" "yes"
expect "T4_无web.new残留" "$(find "$H/opt" -maxdepth 2 -name 'web.new' | wc -l | tr -d ' ')" "0"

echo
echo "############ T5 反证 D：只摘**第一道**守卫 ⇒ 第二道（同级必须有 bin/）必须还拦得住 ############"
build_trees
python3 - "$SRC" "$H/one_leg_off.sh" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
s = open(src, encoding="utf-8").read()
old = '    *) bad "$name 根路径不以 /web 结尾（实得 $root）——**拒绝执行**，见本函数头注释与文件头「建立经过」"; return ;;'
assert old in s, "锚点没命中（第一道守卫），反证脚本没造出来——那说明执行体已改形态，本夹具需同步重钉"
s = s.replace(old, '    *) : ;;  # 反证 D：第一道形状守卫摘掉（夹具专用，不是交付形态）')
s = s.replace('stage_site "$d/web"', 'stage_site "$d"')
s = s.replace('commit_site "$d/web"', 'commit_site "$d"')
open(dst, "w", encoding="utf-8").write(s)
PY
DEPLOY_DIRS="$DD" bash "$H/one_leg_off.sh" "$H/dist.tar.gz" "$TAR_SHA" "$H/fakebin" "$BIN_SHA" >/dev/null 2>&1
T5=$?
expect_nonzero "$T5" "T5 只摘第一道"
expect "T5_data还在原位" "$([ -f "$H/opt/translator/data/app.db" ] && echo yes || echo NO)" "yes"

echo
echo "############ T6 反证 E：**两道守卫全摘** ⇒ 必须真出事（证明 T2/T5 的绿灯来自守卫本身） ############"
build_trees
python3 - "$SRC" "$H/no_guard.sh" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
s = open(src, encoding="utf-8").read()
a = '    *) bad "$name 根路径不以 /web 结尾（实得 $root）——**拒绝执行**，见本函数头注释与文件头「建立经过」"; return ;;'
b = '  [ -d "$(dirname "$root")/bin" ] || { bad "$name 部署根 $(dirname "$root") 下没有 bin/，形态不认识，停手"; return; }'
assert a in s and b in s, "锚点没命中（两道守卫），反证脚本没造出来"
s = s.replace(a, '    *) : ;;  # 反证 E：第一道守卫摘掉（夹具专用）').replace(b, '  # 反证 E：第二道守卫摘掉（夹具专用）')
s = s.replace('stage_site "$d/web"', 'stage_site "$d"')
s = s.replace('commit_site "$d/web"', 'commit_site "$d"')
open(dst, "w", encoding="utf-8").write(s)
PY
DEPLOY_DIRS="$DD" bash "$H/no_guard.sh" "$H/dist.tar.gz" "$TAR_SHA" "$H/fakebin" "$BIN_SHA" >/dev/null 2>&1
expect "T6_data被一起搬走（真复现 07:44）" "$([ -f "$H/opt/translator/data/app.db" ] && echo yes || echo NO)" "NO"
expect "T6_bin被一起搬走" "$([ -f "$H/opt/translator/bin/translator-server" ] && echo yes || echo NO)" "NO"
expect "T6_部署根改名份数下界" "$([ "$(ls -d "$H/opt/translator".old.* 2>/dev/null | wc -l | tr -d ' ')" -ge 1 ] && echo yes || echo NO)" "yes"

echo
if [ "$BAD_EXPECT" -eq 0 ]; then
  echo "SWAPTEST_ALL_OK=1 六段（T1 正测＋T2–T6 五枚反证）全部命中期望；夹具树在 $H"
  exit 0
fi
echo "SWAPTEST_ABORT=期望不符 $BAD_EXPECT 条（上面 EXPECT_BAD 行逐条点名；先判是夹具漂了还是守卫真废了）"
exit 1
