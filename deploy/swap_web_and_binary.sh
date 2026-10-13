#!/usr/bin/env bash
# ============ swap_web_and_binary.sh · 职责说明 ============
# 服务器侧换源与换件的**唯一常设执行体**（在服务器本机跑）：两站 web 根各自独立换源并各自复验，
# 两个后端二进制各自备份后替换，最后 restart 后端单元并自证 is-active。全程不碰 caddy。
#
# 用法（本机先把三样东西发上去，sha 一律现读现比）：
#   scp frontend-dist.tar.gz <服务器>:/tmp/ && scp translator-server-linux <服务器>:/tmp/translator-server-new
#   scp deploy/swap_web_and_binary.sh <服务器>:/tmp/
#   ssh <服务器> 'bash /tmp/swap_web_and_binary.sh /tmp/frontend-dist.tar.gz <tar 的 sha256> /tmp/translator-server-new <二进制的 sha256>'
#
# ★ 建立经过（2026-10-13，真踩）：那一批手工写的换源脚本把**部署根**（/opt/translator）当成了 **web 根**传进去，
#   于是 `mv $root $root.old.$TS` 把 bin／data／log／.venv／web 整棵挪走、放上去的只是 dist 内容，
#   两个单元进入崩溃重启循环（主站每枪退 209/STDOUT＝日志目录没了、演示站每枪退 203/EXEC＝可执行文件不在），
#   各失败 23 次，两站停 **1 分 24 秒**（07:44:19→07:45:43，两端点 journal 实读；靠事后按原路搬回才恢复）。
#   所以这一版把那条教训变成**机械守卫**：
#   ① 根路径必须以 `/web` 结尾，且同级必须有 `bin/`，否则整站拒绝执行；
#   ② 校验（tar／二进制 sha、入口名形状、首页引用的每个 asset 真落盘、语种 chunk 份数）全部**在换名之前**；
#   ③ **两段式**：先「备料」（解到 `web.new` 并四道校验，两站都要过），全绿才进「落位」（两步 mv ＋ 换二进制 ＋ 重启）。
#      备料段任一 BAD ⇒ 清掉 `.new`、整段停手＝现网还是完整上一代；
#      旧形态是「本站拒了但别的站照换、二进制照换、单元照重启」，会留下 **web 旧代＋后端新代**的混合半态；
#   ④ 二进制走 `cp 到临时名 + mv rename`（`cp` 直接覆盖运行中文件退 ETXTBSY 会**静默不生效**）；
#   ⑤ 任一条 BAD ⇒ 退出码抬高，`SWAP_ALL_OK` 不出现在屏上（AGENTS §三「验证链自己不算账」：exit 必须一路 exit 到最外层）。
#
# ⚠️ 三条不改的东西：不 reload/restart caddy（它和 `caddy stop` 共用 127.0.0.1:2019 管理面，会停全站）；
#    不删归档以外的任何目录（只把 web.old.* 按**文件名时间戳**裁到 4 份——`ls -t` 会被 cp -a 继承的 mtime 骗）；
#    不写死任何哈希文件名（入口名从新 index.html 现读，只判**形状**）。
# ==========================================================
set -uo pipefail

TAR=${1:?用法: bash swap_web_and_binary.sh <tar 路径> <tar 的 sha256> <待换二进制> <二进制的 sha256>}
TAR_SHA=${2:?缺 tar 的 sha256（本机现读，禁止照抄上一批那份）}
BIN_NEW=${3:?缺待换二进制路径}
BIN_SHA=${4:?缺待换二进制的 sha256}
# 部署根集合可用 env 覆盖：本地拿假树自证守卫时用，生产不设即走现网两棵
DEPLOY_DIRS=${DEPLOY_DIRS:-/opt/translator /opt/translator-demo}
WEB_OWNER=${WEB_OWNER:-root:caddy}
UNITS=${UNITS:-translator translator-demo}
KEEP_ARCHIVES=${KEEP_ARCHIVES:-4}
TS=$(date +%Y%m%d_%H%M%S)
FAIL=0

bad() { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
ok()  { echo "OK   $*"; }

# ---- 0. 上传件完整性：两份 sha 先对上，对不上整段不做（一个字节都不动）----
got=$(sha256sum "$TAR" | cut -d' ' -f1)
[ "$got" = "$TAR_SHA" ] && ok "tar sha 与本机一致（$got）" || bad "tar sha 不符：期望 $TAR_SHA 实得 $got"
gotb=$(sha256sum "$BIN_NEW" | cut -d' ' -f1)
[ "$gotb" = "$BIN_SHA" ] && ok "二进制 sha 与本机一致（$gotb）" || bad "二进制 sha 不符：期望 $BIN_SHA 实得 $gotb"
[ "$FAIL" -eq 0 ] || { echo "SWAP_ABORT=上传件校验没过，什么都没动"; exit 1; }

# ---- 1a. 备料段：解到 web.new → 四道校验（**一个字节都不动现有目录**）----
# root 参数必须是 **web 根**（/opt/translator/web 这种形态），不是部署根。
stage_site() {
  local root="$1" name="$2"
  # ★ 守卫 ①：形状。传成部署根＝把 bin/data/log/.venv 一起搬走＝停站，所以这一条比判据都靠前。
  case "$root" in
    */web) : ;;
    *) bad "$name 根路径不以 /web 结尾（实得 $root）——**拒绝执行**，见本函数头注释与文件头「建立经过」"; return ;;
  esac
  [ -d "$(dirname "$root")/bin" ] || { bad "$name 部署根 $(dirname "$root") 下没有 bin/，形态不认识，停手"; return; }

  rm -rf "$root.new"
  mkdir -p "$root.new"
  tar xzf "$TAR" -C "$root.new" --strip-components=1 2>/dev/null || { bad "$name 解包失败"; return; }
  rm -f "$root.new/.DS_Store"; find "$root.new" -name '._*' -delete 2>/dev/null

  # 守卫 ②：入口名从**新** index.html 现读，且形状认得出（无哈希名会让 immutable 长缓存与存在性匹配器双双落空）
  local entry
  entry=$(grep -oE 'assets/index-[A-Za-z0-9_-]*\.js' "$root.new/index.html" | head -1)
  [ -n "$entry" ] || { bad "$name 新 index.html 里认不出入口名"; return; }
  case "$entry" in
    assets/index-????????*.js) : ;;
    *) bad "$name 入口名形状不合（$entry）"; return ;;
  esac
  [ -f "$root.new/$entry" ] || { bad "$name 首页指向 $entry 但磁盘上没有它"; return; }
  ok "$name 入口 $entry（raw $(stat -c%s "$root.new/$entry") B；侧车 br=$([ -f "$root.new/$entry.br" ] && echo 在 || echo 缺) gz=$([ -f "$root.new/$entry.gz" ] && echo 在 || echo 缺)）"

  # 守卫 ③：首页引用的**每个** asset 都要真落盘（缺一个是换源后 404 的直接来源）
  local missing=0 total=0 a
  for a in $(grep -oE 'assets/[A-Za-z0-9._-]*\.(js|css)' "$root.new/index.html" | sort -u); do
    total=$((total + 1))
    [ -f "$root.new/$a" ] || { missing=$((missing + 1)); bad "$name 首页引用的 $a 未落盘"; }
  done
  [ "$missing" -eq 0 ] && ok "$name 首页引用 asset $total 个，缺失 0 个" || return

  # 守卫 ④：语种 chunk 份数（懒加载① 的落地证据——改回静态 import 在这一档当场现形）
  # 现网 dist 实测这十份的文件名：ar／de／es／fr／ja／ko／pt／ru／th／zh-hant（均 `<语种>-<8 位哈希>.js`），
  # 所以这一条**不是**只有夹具能过的形状；zh／en 刻意留在入口包里，本来就不该出现在这里。
  local lc
  lc=$(cd "$root.new/assets" 2>/dev/null && ls | grep -cE '^[a-z]{2}(-[a-zA-Z]+)?-[A-Za-z0-9_-]{8}\.js$')
  if [ "${lc:-0}" -ge 10 ]; then ok "$name 语种 chunk $lc 份"; else bad "$name 语种 chunk 只有 ${lc:-0} 份（应 ≥10）"; return; fi

  ok "$name 备料通过（web.new 已就位，现有目录尚未动）"
}

# ---- 1b. 落位段：两步 mv → 权限 → dotfile 复验 → 裁归档（只在**全部**备料通过后执行）----
commit_site() {
  local root="$1" name="$2"
  mv "$root" "$root.old.$TS" && mv "$root.new" "$root" || { bad "$name 两步 mv 失败（此刻目录名可能已换一半，看上一条 BAD 归因）"; return; }
  chown -R "$WEB_OWNER" "$root" 2>/dev/null
  chmod -R o+rX "$root"
  local dirty
  dirty=$(find "$root" -name '.DS_Store' -o -name '._*' | wc -l)
  [ "$dirty" -eq 0 ] && ok "$name 脏文件复验 0" || bad "$name 脏文件复验 $dirty"
  # 归档按**文件名时间戳**裁（不是 ls -t）
  ls -d "$root".old.* 2>/dev/null | sort -r | tail -n "+$((KEEP_ARCHIVES + 1))" | while read -r d; do rm -rf "$d"; done
  ok "$name 换源完成，web 归档现有 $(ls -d "$root".old.* 2>/dev/null | wc -l) 份（保留上限 $KEEP_ARCHIVES）"
}

for d in $DEPLOY_DIRS; do stage_site "$d/web" "$(basename "$d")"; done
# ★ 两段式（2026-10-13 自证时补的修法）：备料段任一 BAD ⇒ 把 .new 收干净、**整段停在这里**，
#   不去碰二进制也不重启单元。旧形态是「本站拒了但后面的站照换、二进制照换、闹钟照响」＝
#   web 是旧代、后端是新代那种**混合半态**（同 §一·9 那批「部分失败＝混合半态、单一 buildCommit 看不见」）。
if [ "$FAIL" -gt 0 ]; then
  for d in $DEPLOY_DIRS; do rm -rf "$d/web.new"; done
  echo "SWAP_ABORT=备料段有 $FAIL 条 BAD，web／二进制／单元一律未动（现网仍是上一代）"
  exit "$FAIL"
fi
for d in $DEPLOY_DIRS; do commit_site "$d/web" "$(basename "$d")"; done

# ---- 2. 二进制：备份 → 落到临时名 → mv rename → 现读 sha 比对 ----
for d in $DEPLOY_DIRS; do
  bin="$d/bin/translator-server"
  [ -f "$bin" ] || { bad "$(basename "$d") 没有 $bin（部署根形态不对？）"; continue; }
  cp -a "$bin" "$bin.bak.$TS" || { bad "$(basename "$d") 备份失败"; continue; }
  cp -a "$BIN_NEW" "$bin.tmp" && mv "$bin.tmp" "$bin" || { bad "$(basename "$d") 替换失败"; continue; }
  chmod +x "$bin"
  news=$(sha256sum "$bin" | cut -d' ' -f1)
  [ "$news" = "$BIN_SHA" ] && ok "$(basename "$d") 二进制已到位（$news）" || bad "$(basename "$d") 二进制 sha 不符：$news"
done

# ---- 3. 重启后端单元并自证（演示单元启动要约 3.4 秒才 bind，先等再读；探活与重启**分开**）----
systemctl restart $UNITS
sleep 8
for u in $UNITS; do
  st=$(systemctl is-active "$u" 2>&1)
  [ "$st" = active ] && ok "$u is-active=active" || bad "$u is-active=$st"
done
[ -n "${CHECK_PORTS:-}" ] || CHECK_PORTS="8787 8789"
for p in $CHECK_PORTS; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$p/livez" 2>/dev/null)
  [ "$code" = 200 ] && ok "回环 :$p /livez=200" || bad "回环 :$p /livez=$code（这一条不判发版失败，起得慢也会 000）"
done
echo "caddy（本脚本不碰它）=$(systemctl is-active caddy 2>&1)"

# ---- 4. 两站同代复验：现读入口名必须一致且磁盘上真有 ----
read_entry() { grep -oE 'assets/index-[A-Za-z0-9_-]*\.js' "$1/index.html" 2>/dev/null | head -1; }
e1=$(read_entry "$(echo "$DEPLOY_DIRS" | cut -d' ' -f1)/web")
e2=$(read_entry "$(echo "$DEPLOY_DIRS" | cut -d' ' -f2)/web")
echo "SWAP_ENTRY first=$e1 second=$e2 TS=$TS"
[ -n "$e1" ] || bad "第一站现读入口名为空"
[ "$e1" = "$e2" ] || bad "两站不同代：$e1 vs $e2"

if [ "$FAIL" -eq 0 ]; then echo "SWAP_ALL_OK=1"; else echo "SWAP_BAD=$FAIL"; fi
exit "$FAIL"
