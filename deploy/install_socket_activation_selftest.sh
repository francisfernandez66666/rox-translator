#!/usr/bin/env bash
# ============================================================================
# deploy/install_socket_activation_selftest.sh — 装配入口 `install_socket_activation.sh`
# 的**落位段＋V4 解析段**射程自证（★ 2026-10-14，#41 S1/S4）
#
# 为什么要有这一份（当天真踩换来的）：
#   同族的发版执行体 `swap_web_and_binary.sh` 在把窗口探针从"opt-in"翻成"默认开"之后，
#   第一跑夹具就在 `set -u` 下当场退在 `WINDOW_PROBE_URLS: unbound variable`——
#   而那一行的位置是**「已经 mv 落位、还没 systemctl restart」**，真上线就是两站停在未重启状态。
#   这条腿之所以肉眼看不出来，是因为"只有默认档才会走到"（显式配 URL 时整个 if 跳过），
#   写它的人（我）一直用显式 URL 试。⇒ 结论：**新增的默认腿必须由夹具真跑一次**。
#   而 `install_socket_activation.sh --apply` 是全仓写入面最宽的一条命令
#   （往 /etc/systemd/system 落文件 ＋ enable --now ＋ restart 两个 service），
#   它比 swap 脚本更不该靠"读一遍代码"当成"跑过一遍"。
#
# 本夹具**只测抽出来的两段**，不碰 ssh／不碰现网：
#   A 段＝落位段（`APPLY_OUT=$($SSH "bash -s" <<REMOTE … REMOTE)` 那一份远端脚本）。
#     抽取方式＝按锚点把**原文**摘出来，塞进一份同样用 `<<REMOTE` 未引号定界符的本地壳里执行，
#     于是「本地展开一次、远端再展开一次」这两层引号与原脚本**完全同形**（这是本夹具存在的理由：
#     bugs 就长在这一层，改成引号定界符或预先展开都等于把被测对象换掉了）。
#     远端脚本由桩 `ssh` 就地执行（它把 stdin 交给 `bash -s`），
#     绝对路径三处（/etc/systemd/system、/tmp/socket_units.tgz、/tmp/socket_units_pre）
#     在摘出后**按字面**换成夹具目录 ⇒ 一个字节都不写进真的 /etc。
#   P 段＝V4 解析段（`V4=$(echo "$APPLY_OUT"` 起、到文件末尾 `exit 0`）——
#     喂四份合成读数，验三条裁决腿各自会红、并且 **P99 那条不许抬 exit**。
#
# 判绿只认末行 `SOCKETTEST_ALL_OK=1`＋exit 0；任何一条期望不符即点名列出并退 1。
#
# 七条断言（A1–A5／P1–P2）：
#   A1 落位段真跑完：`APPLY=done` ＋ 两份 unit 各一行 `LANDED=… byte_equal`
#   A2 V4 确实采到样：`V4_STATS` 首格 ≥1、非 2xx＝0（桩 curl 恒回 200 10ms）
#   A3 三条 systemctl 命令的**顺序**：daemon-reload → enable --now → restart
#      （顺序错了就是"还没 reload 就 enable"或"没 enable 就 restart"，现网表现是端口没人听）
#   A4 落进夹具目录的那两份与仓库**逐字节等值**（不信任段里那句 cmp，另数一次）
#   A5 反证·写不进去就不许谎报成功：夹具 etc 目录 chmod 000 ⇒ 段必须 `APPLY=2`、
#      **不得**出现 `APPLY=done`，且 systemctl 桩一条命令都没收到（停在写动作之前才是对的取向）
#   P1 反证·V4 三条红腿各点名：无统计行／零样本／非 2xx≠0 ⇒ 三种 `SOCKET_APPLY_OK=0` 文案互不相同
#      （混成一串就把"锁空转"和"502 没归零"读成同一件事）
#   P2 正证·P99 超阈**只出名不裁决**：非 2xx＝0 且样本足 ⇒ `SOCKET_APPLY_OK=1` 且退出码 0，
#      同时必须出现 `V4_DELAY_NOTE`（把延迟写成硬失败＝让脚本替人做还没有读数的决定）
#
# ⚠️ 本夹具**不覆盖**的部分（诚实交账，别把绿灯读成"整个 --apply 验过了"）：
#   · ssh/scp 链路本身、G1 二进制标记预检、G2 逐文件 sha 等值、remote_status 的属主读数、
#     V1／V2／V3 三段验收、`--rollback` 档 —— 这些要么依赖现网读数，要么已在
#     `deploy/install_socket_activation.sh --status`（只读档位）里可随时复看。
#   · 采样器与真实服务的并发关系（桩 curl 是瞬回的，真实现场是"进程重启中"）。
#     它测的是**接线**：这段代码在真机上会跑、会写对的字节、会把统计交给对的裁决腿。
#
# 用法：bash deploy/install_socket_activation_selftest.sh
# 夹具根可用 SOCKETTEST_HOME 覆盖（默认 /tmp/socket_activation_selftest）；全程只在本机假树里动作。
# ============================================================================
set -uo pipefail

SRC_REL="deploy/install_socket_activation.sh"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$REPO/$SRC_REL"
[ -f "$SRC" ] || { echo "ABORT 读不到执行体 $SRC"; exit 1; }

H="${SOCKETTEST_HOME:-/tmp/socket_activation_selftest}"
rm -rf "$H"
mkdir -p "$H/bin" "$H/etc/systemd/system" "$H/stage"
chmod 755 "$H/etc" "$H/etc/systemd" "$H/etc/systemd/system"

# 安全断言：夹具根必须在 /tmp 下（本夹具会把抽取出的段**就地执行**，路径错了就是往真系统里写）
case "$H" in
  /tmp/*) : ;;
  *) echo "ABORT SOCKETTEST_HOME=$H 不在 /tmp 下——拒绝执行抽取出来的落位段"; exit 1 ;;
esac

ECHO_BAD=0
expect() { # expect <名字> <实得> <期望>
  if [ "$2" = "$3" ]; then echo "EXPECT_OK   $1 = $2"; else echo "EXPECT_BAD  $1 期望 $3 实得 $2"; ECHO_BAD=$((ECHO_BAD + 1)); fi
}
expect_nonzero() {
  if [ "$2" != "0" ]; then echo "EXPECT_OK   $1 退非 0（$2）"; else echo "EXPECT_BAD  $1 应退非 0，实退 0"; ECHO_BAD=$((ECHO_BAD + 1)); fi
}
expect_zero() {
  if [ "$2" = "0" ]; then echo "EXPECT_OK   $1 退 0"; else echo "EXPECT_BAD  $1 应退 0，实退 $2"; ECHO_BAD=$((ECHO_BAD + 1)); fi
}

# ---------- 桩：把"远端"就地演成"本机夹具" ----------
cat > "$H/bin/ssh" <<'EOF'
#!/usr/bin/env bash
# 桩 ssh：丢掉 -o/-p/<user@host> 这些选项，剩下的照原样执行。
# 只支持执行体用到的两种形态：`ssh … bash -s`（脚本走 stdin）与 `ssh … '<整段命令>'`（单参数）。
# ★ 这里**故意不模拟网络失败**：本夹具要测的是"展开两层引号之后那段代码对不对"，
#   链路与凭据不属它射程（那部分靠 --status 只读档在现网复看）。
args=()
while [ $# -gt 0 ]; do
  case "$1" in
    -o|-p|-i|-l) shift 2 ;;
    -*) shift ;;
    *@*) shift ;;
    *) args+=("$1"); shift ;;
  esac
done
if [ "${args[0]:-}" = "bash" ] && [ "${args[1]:-}" = "-s" ]; then exec bash -s; fi
exec bash -c "${args[0]:-}"
EOF
cat > "$H/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
# 桩 systemctl：只记一行到 SYSD_LOG，全部成功返回。
# A3 那条顺序锁数的就是这份日志——它证明段里**真的按 daemon-reload → enable --now → restart 的次序**下令。
echo "systemctl $*" >> "${SYSD_LOG:-/dev/null}"
exit 0
EOF
cat > "$H/bin/curl" <<'EOF'
#!/usr/bin/env bash
# 桩 curl：窗口探针用的两列格式一律回 `200 0.010`（正测档）；
# STUB_CURL_CODE／STUB_CURL_EMPTY 留给"要造非 2xx／要造一个字节都不输出"的合成腿。
[ -n "${STUB_CURL_EMPTY:-}" ] && exit 0
echo "${STUB_CURL_CODE:-200} ${STUB_CURL_TIME:-0.010}"
EOF
cat > "$H/bin/sleep" <<'EOF'
#!/usr/bin/env bash
# 夹具专用：把段里那句"等窗口过去"的 8 秒压到 2 秒（真跑三遍会白等半分钟），
# 采样器自己的 `sleep 0.2` 一字不动——窗口长度只影响**样本数**，而断言问的是"≥1 且非 2xx＝0"。
v="${1:-0}"
case "$v" in
  0.1|0.2|0.5|1) exec /bin/sleep "$v" ;;
  *) exec /bin/sleep 2 ;;
esac
EOF
chmod +x "$H/bin"/*
export PATH="$H/bin:$PATH"
export SYSD_LOG="$H/systemctl.log"; : > "$SYSD_LOG"

# ---------- 1) 按锚点抽出两段原文 ----------
python3 - "$SRC" "$H" "$REPO" <<'PY'
import io, sys, os
src, h, repo = sys.argv[1], sys.argv[2], sys.argv[3]
s = io.open(src, encoding="utf-8").read()

# A 段：`APPLY_OUT=$($SSH "bash -s" <<REMOTE` 到独立一行 `REMOTE`
head = 'APPLY_OUT=$($SSH "bash -s" <<REMOTE\n'
assert s.count(head) == 1, "锚点 1（落位段起始）没命中或不止一处 ⇒ 执行体已改形态，本夹具需同步重钉"
i = s.index(head) + len(head)
j = s.index("\nREMOTE\n", i) + 1
apply_seg = s[i:j]
assert "APPLY=done" in apply_seg and "V4_STATS=" in apply_seg and "sampler.sh" in apply_seg, \
    "落位段抽出来不含该有的三样（APPLY=done／V4_STATS／sampler.sh）⇒ 抽取错位"

# 三处绝对路径按**字面**换成夹具目录（段里除此之外没有别的写盘路径）
n1 = apply_seg.count("/etc/systemd/system/")
n2 = apply_seg.count("/tmp/socket_units.tgz")
n3 = apply_seg.count("/tmp/socket_units_pre")
assert n1 >= 1 and n2 >= 1 and n3 >= 1, "落位段里那三处绝对路径有一处没找到 ⇒ 段改形态了，夹具必须同步"
apply_seg = (apply_seg.replace("/etc/systemd/system/", f"{h}/etc/systemd/system/")
                       .replace("/tmp/socket_units.tgz", f"{h}/socket_units.tgz")
                       .replace("/tmp/socket_units_pre", f"{h}/socket_units_pre"))
print("EXTRACT apply_lines=%d rewritten=%d/%d/%d" % (apply_seg.count("\n"), n1, n2, n3))

# P 段：`V4=$(echo "$APPLY_OUT"` 起，到文件里最后一个 `exit 0` 行止
k = s.index('V4=$(echo "$APPLY_OUT"')
tail = s[k:]
last = tail.rindex("exit 0")
tail = tail[:last + len("exit 0")]
assert "SOCKET_APPLY_OK=0" in tail and "SOCKET_APPLY_OK=1" in tail and "V4_DELAY_NOTE" in tail, \
    "V4 解析段抽少了（三条结论文案不全）"
io.open(f"{h}/apply_seg.sh", "w", encoding="utf-8").write(apply_seg)
io.open(f"{h}/parser_seg.sh", "w", encoding="utf-8").write(tail)

# 仓库里那两份 unit 必须存在（落位段的 cmp/回验靠它们）
for f in ("deploy/systemd/translator.socket", "deploy/systemd/translator-demo.socket"):
    assert os.path.isfile(os.path.join(repo, f)), f"仓库缺 {f}"
PY
[ -f "$H/apply_seg.sh" ] && [ -f "$H/parser_seg.sh" ] || { echo "ABORT 两段没抽出来（上面的 assert 已点名）"; exit 1; }

# ---------- 2) 造"远端"上该有的那份上传件与解包目录 ----------
# 形状照抄执行体：tar 里是 deploy/systemd/*.socket，解到 socket_units_pre/ 后 cd 进 deploy/systemd
tar --exclude='._*' --exclude='.DS_Store' -czf "$H/socket_units.tgz" -C "$REPO" \
  deploy/systemd/translator.socket deploy/systemd/translator-demo.socket \
  || { echo "ABORT 打包失败"; exit 1; }
mkdir -p "$H/socket_units_pre"
tar -xzf "$H/socket_units.tgz" -C "$H/socket_units_pre"

# 执行体在本地展开那两层变量是 TS／PROBE_URLS／PROBE_P99——壳里必须给同名同档的它们
cat > "$H/run_apply.sh" <<'EOF'
#!/usr/bin/env bash
set -uo pipefail
TS=$(date +%Y%m%d_%H%M%S)
PROBE_URLS="${__PROBE_URLS:-http://127.0.0.1:8787/livez http://127.0.0.1:8789/livez}"
PROBE_P99="${__PROBE_P99:-1}"
# ★ 退出码必须一路 exit 到最外层（AGENTS 那条「跑一个脚本看它报不报错」的复查脚本教训）：
#   只 echo 的话，段里 `exit 2` 会被末尾那条 echo 的退 0 盖掉，A5 那枚反证就恒绿。
APPLY_OUT=$(bash -s <<REMOTE
@@APPLY@@
REMOTE
)
RC=$?
echo "$APPLY_OUT"
exit "$RC"
EOF
python3 - "$H" <<'PY'
import io, sys
h = sys.argv[1]
p = f"{h}/run_apply.sh"
s = io.open(p, encoding="utf-8").read()
seg = io.open(f"{h}/apply_seg.sh", encoding="utf-8").read().rstrip("\n")
assert "@@APPLY@@" in s
io.open(p, "w", encoding="utf-8").write(s.replace("@@APPLY@@", seg))
PY

# 解析段的壳：BAD／APPLY_OUT／PROBE_URLS／PROBE_P99 由调用方经环境传入
cat > "$H/run_parser.sh" <<'EOF'
#!/usr/bin/env bash
set -uo pipefail
BAD="${__BAD:-}"
APPLY_OUT="${__APPLY_OUT:-}"
PROBE_URLS="${__PROBE_URLS:-http://127.0.0.1:8787/livez}"
PROBE_P99="${__PROBE_P99:-1}"
@@PARSER@@
EOF
python3 - "$H" <<'PY'
import io, sys
h = sys.argv[1]
p = f"{h}/run_parser.sh"
s = io.open(p, encoding="utf-8").read()
seg = io.open(f"{h}/parser_seg.sh", encoding="utf-8").read().rstrip("\n")
assert "@@PARSER@@" in s
io.open(p, "w", encoding="utf-8").write(s.replace("@@PARSER@@", seg))
PY

# 夹具专用收摊：把**上一段留下的孤儿采样器**先落 stop 再删目录。
# ⚠️ 为什么需要（A6 首跑的 flaky 就是这么来的，属夹具错、不是执行体废）：
#   段里的采样目录名是 `/tmp/window_probe.$TS`，而 TS 只到**秒**——A5 那段在落位循环里 exit 2，
#   既没走到 `rm -rf "$PDIR"` 也没落 stop，它的采样器还在按**真 URL**往同一个目录写；
#   A6 若落在同一秒里，就会读到 12 条"别人家的样本"，于是"空 URL ⇒ 0 0 0 none"那枚判据假失败。
#   ⇒ 每一段跑之前先清场；同一条纪律在生产侧不成立（不会有人同一秒跑两次 --apply），
#     但这条读数值得记下来：**目录名按秒聚合的采样腿，并发或重试时会互相污染**。
reap_probes() {
  local d
  for d in /tmp/window_probe.*; do
    [ -d "$d" ] || continue
    : > "$d/stop" 2>/dev/null || true
    rm -rf "$d" 2>/dev/null || true
  done
}

# ---------- A 段：正测 ----------
echo "############ A1–A4 落位段真跑（夹具 etc／桩 systemctl／桩 curl） ############"
reap_probes
APPLY_LOG="$H/apply.log"
bash "$H/run_apply.sh" > "$APPLY_LOG" 2>&1
RA=$?
expect_zero "A0 落位段退出码" "$RA" "0"
expect "A1_段跑到底" "$(grep -c 'APPLY=done' "$APPLY_LOG")" "1"
expect "A1_两份 unit 逐字节落位" "$(grep -c 'byte_equal' "$APPLY_LOG")" "2"
expect "A2_V4 出了统计行" "$(grep -c '^V4_STATS=' "$APPLY_LOG")" "1"
N=$(sed -n 's/^V4_STATS=\([0-9]*\) .*/\1/p' "$APPLY_LOG" | head -1)
NON2=$(sed -n 's/^V4_STATS=[0-9]* \([0-9]*\) .*/\1/p' "$APPLY_LOG" | head -1)
[ "${N:-0}" -ge 1 ] && expect "A2_样本数下界" "yes" "yes" || expect "A2_样本数下界(实得 ${N:-<空>})" "NO" "yes"
expect "A2_窗口内非2xx=0" "${NON2:-<空>}" "0"

# A3 顺序锁：daemon-reload 必须早于 enable --now，enable --now 必须早于 restart
# （只判"三条都在"不够——三条都在但次序错了，现网表现就是"socket 还没开始听，服务已经重启完了"）
ORDER=$(awk '
 /daemon-reload/ && !d {d=NR}
 /enable --now/ && d && !e {e=NR}
 /restart[[:space:]]/ && e && !r {r=NR}
 END { print (d && e && r) ? "yes" : "NO" }' "$SYSD_LOG")
expect "A3_daemon-reload→enable→restart 次序" "${ORDER:-NO}" "yes"
expect "A3_enable --now 含两份 socket" "$(grep -c 'enable --now translator.socket translator-demo.socket' "$SYSD_LOG")" "1"
expect "A3_restart 只点两个 service" "$(grep -c '^systemctl restart translator translator-demo$' "$SYSD_LOG")" "1"

# A4 落盘内容与仓库逐字节等值（不信任段里那句 cmp，另数一次）
for u in translator.socket translator-demo.socket; do
  cmp -s "$REPO/deploy/systemd/$u" "$H/etc/systemd/system/$u" \
    && expect "A4_$u 与仓库等值" "yes" "yes" \
    || expect "A4_$u 与仓库等值" "NO" "yes"
done

echo
echo "############ A5 反证：夹具 etc 写成不可入 ⇒ 必须停在写动作之前，且不谎报成功 ############"
: > "$SYSD_LOG"
reap_probes
chmod 000 "$H/etc/systemd/system"
A5_LOG="$H/apply_no_write.log"
bash "$H/run_apply.sh" > "$A5_LOG" 2>&1
RA5=$?
chmod 755 "$H/etc/systemd/system"
# 段在落位循环里 exit 2 ⇒ 它**没走到**那句 `rm -rf "$PDIR"`，也没落 stop 文件
# ⇒ 采样器成了孤儿，会自己跑满 400 轮才收摊。这里按它自己的停手协议落一个 stop（不 kill 进程，
#   避免"按名字杀"误伤别的会话），孤儿下一轮就自己退出。
for d in /tmp/window_probe.*; do [ -d "$d" ] && : > "$d/stop"; done
expect_nonzero "A5 写不进去即退" "$RA5" "0"
expect "A5_点名落位失败" "$(grep -c 'APPLY=2 落位失败' "$A5_LOG")" "1"
expect "A5_不得出现 APPLY=done" "$(grep -c 'APPLY=done' "$A5_LOG")" "0"
expect "A5_一条 systemctl 都没下令" "$(wc -l < "$SYSD_LOG" | tr -d ' ')" "0"

echo
echo "############ A6 反证：URL 给了空档 ⇒ 段自己必须把「一个样本都没采到」报出来 ############"
# P1b 那枚测的是**解析腿**对合成串的反应；这一枚测的是**采样腿本身**：
# 真把 URL 掏空跑一遍，段里那条 awk 必须回 `0 0 0 none`（而不是回一串看着像"0 个坏样本"的数）。
# 少了这一枚，"采样器压根没跑"与"跑了但窗口很干净"在读数上就同形了。
: > "$SYSD_LOG"
reap_probes
sleep 1   # 给刚落 stop 的采样器一轮时间自己退出（它每轮先看 stop）
A6_LOG="$H/apply_empty_urls.log"
__PROBE_URLS=" " bash "$H/run_apply.sh" > "$A6_LOG" 2>&1
expect_zero "A6 空 URL 仍跑完落位" "$?" "0"
expect "A6_统计行照样出" "$(grep -c '^V4_STATS=' "$A6_LOG")" "1"
expect "A6_回吐 0 0 0 none（不是漏样本就是空转）" "$(sed -n 's/^V4_STATS=//p' "$A6_LOG" | head -1)" "0 0 0 none"

echo
echo "############ P1 反证：V4 三条红腿各点名（无读数／零样本／非2xx≠0） ############"
# 喂三份合成 APPLY_OUT。这一步测的是**裁决接线**：段里那条 awk 出的统计串，
# 到了主脚本必须被拆成"样本数／非2xx／000／P99"四格并各自有归属——
# 拆错一格（比如把非 2xx 当 000）就把"接管没生效"读成"这一跳没走完"，处置方向整个反过来。
p_run() { # p_run <名字> <合成 APPLY_OUT> <期望 SOCKET_APPLY_OK>
  local out rc
  out=$( __APPLY_OUT="$2" __BAD="" bash "$H/run_parser.sh" 2>&1 ); rc=$?
  echo "---- $1（退 $rc）"
  echo "$out" | sed 's/^/    /'
  if [ "$3" = "1" ]; then
    expect "$1_判绿" "$(echo "$out" | grep -c 'SOCKET_APPLY_OK=1')" "1"
    expect_zero "$1_退出码" "$rc" "0"
  else
    expect "$1_判红" "$(echo "$out" | grep -c 'SOCKET_APPLY_OK=0')" "1"
    expect_nonzero "$1_退出码" "$rc" "0"
  fi
  P1_OUT="$out"
}
p_run "P1a 落位段没出统计" "APPLY=done" "0"
expect "P1a_点名无读数" "$(echo "$P1_OUT" | grep -c 'V4:<无读数（落位段没出统计）>')" "1"
p_run "P1b 零样本" "V4_STATS=0 0 0 none
APPLY=done" "0"
expect "P1b_点名零样本" "$(echo "$P1_OUT" | grep -c 'V4:零样本')" "1"
expect "P1b_不许误报成无读数" "$(echo "$P1_OUT" | grep -c 'V4:<无读数')" "0"
p_run "P1c 窗口内有非 2xx" "V4_STATS=37 3 0 0.012
APPLY=done" "0"
expect "P1c_点名非2xx" "$(echo "$P1_OUT" | grep -c 'V4:窗口内非2xx=3')" "1"
expect "P1c_不许误报成零样本" "$(echo "$P1_OUT" | grep -c 'V4:零样本')" "0"

echo
echo "############ P2 正证：P99 超亚秒只出名、不改裁决 ############"
p_run "P2 P99=3.4s 但非2xx=0" "V4_STATS=41 0 0 3.400
APPLY=done" "1"
# 贴到**那一行自己的形状**上问，别跨行凑数（首跑写成 `grep -c 'V4_DELAY_NOTE'` 实得 2——
# 成功文案最后那句"V4_DELAY_NOTE 那条 P99 读数就是它的窗口侧对照"里也含这个词，那是注释不是读数）。
expect "P2_DELAY_NOTE 那行在位" "$(echo "$P1_OUT" | grep -c 'V4_DELAY_NOTE P99=3.400s')" "1"
expect "P2_写明不参与裁决" "$(echo "$P1_OUT" | grep -c '不参与裁决')" "1"
expect "P2_窗口采样行也出读数" "$(echo "$P1_OUT" | grep -c 'V4_window samples=41')" "1"
# 反证：把"只出名"写成"也裁决"的话，P2 这一腿会红——所以再加一枚对照：
# 非 2xx＝0 且 P99 达标 ⇒ 同样判绿（两档都绿才说明这条腿的射程真的只在 P99 那一维）
p_run "P2b P99 达标" "V4_STATS=41 0 0 0.090
APPLY=done" "1"
expect "P2b_亚秒达标行在位" "$(echo "$P1_OUT" | grep -c '亚秒达标')" "1"

echo
if [ "$ECHO_BAD" -eq 0 ]; then
  echo "SOCKETTEST_ALL_OK=1 装配入口两段（A 落位 5 项＋P 解析 5 份合成读数）全部命中期望；夹具树在 $H"
  echo "  未覆盖：ssh/scp 链路、G1 标记预检、G2 逐文件 sha、remote_status 属主读数、V1／V2／V3、--rollback（见文件头）"
  exit 0
fi
echo "SOCKETTEST_ABORT=期望不符 $ECHO_BAD 条（上面 EXPECT_BAD 行逐条点名；先判是夹具锚漂了还是段真废了）"
exit 1
