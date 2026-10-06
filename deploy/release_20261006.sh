#!/usr/bin/env bash
# release_20261006.sh — 〇-AR 第 3＋4 波换件＋两站换源（前后端同批）
#
# 本批为什么三件都要动（按 AGENTS §一·5「五类渲染面」点名，不靠"dist 绿＝全站绿"）：
#   · `internal/api/pay_usdt*`／`stream.go`／`server.go` 变了 ⇒ **translator-server 两台都要换**；
#   · `internal/assist/*`（engine 六道闸／退避／启动期清理／按钮名／编造守卫）变了 ⇒ **translator-assist 要换**；
#   · `PlansP.tsx`＋`usdtPromise.ts`＋十份 locale 变了 ⇒ **两站前端换源**。
#   没动 `extension/`、`sdk/`、`internal/fileproc/*.py` ⇒ 扩展/SDK 与派发资产本轮不重打包（漂移闸今日已 --check 双绿）。
#
# 三道中止线（都在**动手之前**，不是事后补救）：
#   ① 上传件在服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，此时磁盘上还没落位任何新件；
#   ② 换源前 `web.new/index.html` 的入口 asset 名与本机 dist 现读值不等 ⇒ 不换名（防"忘了重新 build"推上线）；
#   ③ 入口引用的每个 asset 必须真落盘 ⇒ 缺一个就不换名。
# 换名走「解到 web.new → 校验 → 两步 mv」，`cp` 覆盖运行中二进制那条 ETXTBSY 老坑一律规避
# （落位一律 `cp 到新名` ＋ 同目录 `mv -f` rename）。
#
# 发版判据按用户既定口径：**只验接线，不跑线上像素校验**。
# 用法：bash deploy/release_20261006.sh            # 干跑到换件前（默认 DRY 态什么都不写）
#       APPLY=1 bash deploy/release_20261006.sh    # 真换
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"

SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux}"
ASSIST_BIN="${ASSIST_BIN:-/tmp/translator-assist-linux}"
DIST_DIR="$REPO/frontend-react/dist"
TARBALL="${TARBALL:-/tmp/frontend-dist-20261006.tar.gz}"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 本机产物自检 ====="
for f in "$SERVER_BIN" "$ASSIST_BIN"; do
  [ -f "$f" ] || { echo "ABORT=1 缺产物 $f（先按 §四 交叉编译）"; exit 2; }
  echo "$f size=$(wc -c <"$f" | tr -d ' ') sha=$(sha_local "$f")"
done
[ -d "$DIST_DIR" ] || { echo "ABORT=1 缺 $DIST_DIR"; exit 2; }
DIRTY=$(find "$DIST_DIR" \( -name '.DS_Store' -o -name '._*' \) 2>/dev/null | wc -l | tr -d ' ')
if [ "$DIRTY" != "0" ]; then
  echo "ABORT=1 dist 里有 $DIRTY 个 macOS 脏件（.DS_Store/._*）——打包前必须清 0，见 AGENTS §三"
  exit 2
fi
ENTRY=$(grep -oE 'assets/[A-Za-z0-9._-]+' "$DIST_DIR/index.html" | sort -u)
echo "dist 入口引用：$(printf '%s ' $ENTRY)"
echo "dist/index.html bytes=$(wc -c <"$DIST_DIR/index.html" | tr -d ' ')"

echo "===== P0-B 打 tar（COPYFILE_DISABLE＋exclude，本机现算 sha）====="
rm -f "$TARBALL"
(cd "$REPO/frontend-react" && COPYFILE_DISABLE=1 tar czf "$TARBALL" --exclude='._*' --exclude='.DS_Store' dist) || { echo "ABORT=1 打包失败"; exit 2; }
echo "$TARBALL size=$(wc -c <"$TARBALL" | tr -d ' ') sha=$(sha_local "$TARBALL")"
DIRTY_AFTER=$(find "$DIST_DIR" \( -name '.DS_Store' -o -name '._*' \) 2>/dev/null | wc -l | tr -d ' ')
echo "打包后 dist 脏件复数=$DIRTY_AFTER（必须仍为 0）"
[ "$DIRTY_AFTER" = "0" ] || { echo "ABORT=1 打包过程又生出脏件"; exit 2; }

SERVER_SHA=$(sha_local "$SERVER_BIN")
ASSIST_SHA=$(sha_local "$ASSIST_BIN")
TAR_SHA=$(sha_local "$TARBALL")
ENTRY_JS=$(grep -oE 'index-[A-Za-z0-9_-]+\.js' "$DIST_DIR/index.html" | head -1)

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里为止全是本机只读/临时目录动作；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
  exit 0
fi

echo "===== P1 上传到 /tmp（此时仍未落位）====="
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-20261006" || { echo "ABORT=2 scp 后端失败"; exit 3; }
scp -P "$PORT" "$ASSIST_BIN" "root@$HOST:/tmp/tas-new-20261006" || { echo "ABORT=2 scp 挂件失败"; exit 3; }
scp -P "$PORT" "$TARBALL" "root@$HOST:/tmp/frontend-dist-20261006.tar.gz" || { echo "ABORT=2 scp 前端失败"; exit 3; }
echo "上传完成。"

echo "===== P2 服务器侧 sha 等值（中止线①）＋落位＋换源＋重启 ====="
ssh -p "$PORT" "root@$HOST" bash -s -- "$SERVER_SHA" "$ASSIST_SHA" "$TAR_SHA" "$ENTRY_JS" <<'REMOTE_EOF'
set -uo pipefail
EXPECT_TS="$1"; EXPECT_TAS="$2"; EXPECT_TAR="$3"; EXPECT_ENTRY="$4"
TS=$(date +%Y%m%d_%H%M%S)
echo "远端时刻=$(date -u +%FT%TZ) TS=$TS"

echo "----- P2 校验上传件（不等即中止，此时一件未落位）-----"
a=$(sha256sum /tmp/ts-new-20261006 | cut -d' ' -f1)
b=$(sha256sum /tmp/tas-new-20261006 | cut -d' ' -f1)
c=$(sha256sum /tmp/frontend-dist-20261006.tar.gz | cut -d' ' -f1)
echo "server_sha=$a"; echo "assist_sha=$b"; echo "tar_sha=$c"
[ "$a" = "$EXPECT_TS" ]  || { echo "ABORT=3 后端上传件 sha 不等，未落位即停"; exit 3; }
[ "$b" = "$EXPECT_TAS" ] || { echo "ABORT=3 挂件上传件 sha 不等，未落位即停"; exit 3; }
[ "$c" = "$EXPECT_TAR" ] || { echo "ABORT=3 前端包 sha 不等，未落位即停"; exit 3; }

echo "----- P3 换件：备份→cp 到新名→同目录 mv -f rename（三件同法）-----"
place() { # $1 源 $2 目标目录 $3 目标文件名 $4 模式
  local src="$1" dir="$2" name="$3" mode="$4"
  if [ ! -f "$dir/$name" ]; then echo "SKIP $dir/$name <不存在>"; return 1; fi
  cp -a "$dir/$name" "$dir/$name.bak.$TS" || { echo "FAIL 备份 $dir/$name"; return 1; }
  cp "$src" "$dir/$name.new.$TS" || { echo "FAIL 复制到新名 $dir/$name.new.$TS"; return 1; }
  chmod "$mode" "$dir/$name.new.$TS"
  chown root:root "$dir/$name.new.$TS"
  mv -f "$dir/$name.new.$TS" "$dir/$name" || { echo "FAIL rename 落位 $dir/$name"; return 1; }
  local got=$(sha256sum "$dir/$name" | cut -d' ' -f1)
  echo "PLACED $dir/$name size=$(stat -c %s "$dir/$name") sha=$got"
}
place /tmp/ts-new-20261006  /opt/translator/bin      translator-server 755 || exit 4
place /tmp/ts-new-20261006  /opt/translator-demo/bin translator-server 755 || exit 4
place /tmp/tas-new-20261006 /opt/ai-assist/bin       translator-assist 755 || exit 4

echo "----- P4 换源：解到 web.new→校验入口与每个 asset→两步 mv→属主权限→裁归档-----"
assets_in() { grep -oE 'assets/[A-Za-z0-9._-]+' "$1" | sort -u; }
rehost() { # $1 web 根
  local root="$1" newdir="$1.new"
  rm -rf "$newdir"; mkdir -p "$newdir" || { echo "FAIL mkdir $newdir"; return 1; }
  tar xzf /tmp/frontend-dist-20261006.tar.gz -C "$newdir" --strip-components=1 || { echo "FAIL 解包到 $newdir"; return 1; }
  local e=$(grep -oE 'index-[A-Za-z0-9_-]+\.js' "$newdir/index.html" | head -1)
  echo "$root 解包入口=$e（期望 $EXPECT_ENTRY）"
  [ "$e" = "$EXPECT_ENTRY" ] || { echo "FAIL 入口 asset 不等：可能忘了重新 build，不换名"; return 1; }
  local miss=0
  for A in $(assets_in "$newdir/index.html"); do
    [ -f "$newdir/$A" ] || { echo "FAIL 缺 $A（$root）"; miss=$((miss+1)); }
  done
  [ "$miss" = "0" ] || { echo "FAIL 有 $miss 个引用未落盘，不换名"; return 1; }
  local d=$(find "$newdir" \( -name .DS_Store -o -name '._*' \) 2>/dev/null | wc -l)
  echo "$root web.new 脏件=$d"
  [ "$d" = "0" ] || { echo "FAIL 包里有 macOS 脏件，不换名"; return 1; }
  # ★ 归档名一律用**下划线族** `${root}_old.$TS`（与 deploy_to_production.sh:57 同一族）。
  #   本脚本第一版写的是点族 `web.old.$TS`，于是同一目录长出两族命名、
  #   "裁到 4 份"按族各数各的——旧的 web_old.* 五份从来没人裁（10-06 复查现形，已由
  #   deploy/housekeeping_20261006.sh 归一并裁到 4）。别再造第三族。
  local archive="$root"_old.$TS
  if [ -e "$archive" ]; then archive="$archive.dup"; echo "WARN 归档同名 $archive 已存在 ⇒ 另存为 $archive" >&2; fi
  mv "$root" "$archive" || { echo "FAIL 旧目录换名 $root"; return 1; }
  mv "$newdir" "$root" || { echo "FAIL 新目录上位 $root"; return 1; }
  chown -R root:caddy "$root" 2>/dev/null || chown -R root "$root"
  chmod -R o+rX "$root"
  echo "$root 换源完成 entry=$(grep -oE 'index-[A-Za-z0-9_-]+\.js' "$root/index.html" | head -1) index_bytes=$(stat -c %s "$root/index.html") dirty=$(find "$root" \( -name .DS_Store -o -name '._*' \) 2>/dev/null | wc -l)"
  # 归档裁剪：按**文件名时间戳**排（ls -t 会被 cp -a 继承的 mtime 骗到，本仓踩过），留最近 4 份。
  # ⚠️ 只裁下划线族，**禁止**把点族并进同一次排序——`.`(0x2E) 比 `_`(0x5F) 小，`sort -r` 会把
  #   点族整族排到最后，于是"最新的一份点族归档"会被当成最旧的删掉（本机实测：
  #   app_old.20260101…20260105 ＋ app.old.20260106 混排时，待删那两条恰是 0106 与 0101）。
  #   点族是 10-06 之前的历史遗留，已由 housekeeping 归一；这里只负责"发现又长出来了就报警"。
  ls -1d "$root"_old.* 2>/dev/null | sort -r | tail -n +5 | while read -r O; do [ -n "$O" ] && rm -rf "$O" && echo "裁剪 $O"; done
  echo "$root 现存归档=$(ls -1d "$root"_old.* 2>/dev/null | grep -c .) 份"
  dotleft=$(ls -1d "$root".old.* 2>/dev/null | grep -c .)
  [ "$dotleft" = "0" ] || echo "WARN $root 又长出点族归档 $dotleft 份（不在本脚本的裁剪射程内，跑 deploy/housekeeping_20261006.sh 归一）"
}
rehost /opt/translator/web || exit 5
rehost /opt/translator-demo/web || exit 5

echo "----- P5 重启三单元并等健康 -----"
systemctl restart translator translator-demo ai-assist
echo "restart_rc=$?"
for p in 8787 8789 8790; do
  ok=0
  for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
    code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$p/api/health" 2>/dev/null || true)
    [ "$p" = "8790" ] && code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$p/health" 2>/dev/null || true)
    if [ "$code" = "200" ]; then ok=1; break; fi
    sleep 2
  done
  echo "port=$p healthy=$ok"
done
systemctl is-active translator translator-demo ai-assist
for u in translator translator-demo ai-assist; do
  echo "unit=$u since=$(systemctl show -p ActiveEnterTimestamp --value "$u") pid=$(systemctl show -p MainPID --value "$u")"
done

echo "----- P6 换件后读数（哪一件在跑只看 /proc/<pid>/exe＋特征串）-----"
for u in translator translator-demo ai-assist; do
  pid=$(systemctl show -p MainPID --value "$u")
  exe=$(readlink "/proc/$pid/exe" 2>/dev/null || true)
  if [ -n "$exe" ] && [ -f "$exe" ]; then
    echo "unit=$u exe=$exe size=$(stat -c %s "$exe") sha16=$(sha256sum "$exe" | cut -c1-16) usdt_watch_dead=$(grep -ac usdt_watch_dead "$exe" 2>/dev/null || true) fabr_count_claim=$(grep -ac fabr_count_claim "$exe" 2>/dev/null || true) canned_feature_key_unsafe=$(grep -ac canned_feature_key_unsafe "$exe" 2>/dev/null || true)"
  else
    echo "unit=$u exe=<取不到>"
  fi
done

echo "----- P6-B 挂件启动腿：旧表搬迁＋canned 代际清理（这两条只在启动那一刻跑一次）-----"
DB=/opt/ai-assist/data/assist.db
if command -v sqlite3 >/dev/null 2>&1; then
  echo "换件后 canned_rows=$(sqlite3 -readonly "$DB" "select count(*) from configs where key like 'i18n:%';" 2>/dev/null || echo '<失败>')"
  sqlite3 -readonly "$DB" "select key, substr(value,1,12) from configs where key like 'i18n:%' order by key;" 2>/dev/null || true
  for t in sessions_base messages_base sessions messages; do
    echo "table=$t rows=$(sqlite3 -readonly "$DB" "select count(*) from $t;" 2>/dev/null || echo '<无此表>')"
  done
fi
LOG=/opt/ai-assist/data/assist.log
echo "assist.log size=$(stat -c %s "$LOG" 2>/dev/null || echo '<无>')"
echo "清理腿读数行=$(grep -ac 'canned' "$LOG" 2>/dev/null || true) 条；迁移腿读数=$(grep -acE '旧表|迁移|legacy' "$LOG" 2>/dev/null || true) 条"
echo "----- P6-C 最近 40 行日志里的 level 计数（不贴正文）-----"
tail -80 "$LOG" 2>/dev/null | grep -oE '"level":"[A-Z]+"' | sort | uniq -c || true
echo "----- P6-D /api/health 状态词（主/演示）＋assist health-----"
curl -s -m 10 http://127.0.0.1:8787/api/health | head -c 900; echo
curl -s -m 10 http://127.0.0.1:8789/api/health | head -c 900; echo
curl -s -m 10 http://127.0.0.1:8790/health | head -c 300; echo
echo "----- P6-E 三域首页直出与 __BRANDING__（磁盘 index 与公网字节数是两个口径）-----"
for base in https://langcross.lexicorn.cn https://demo.lexicorn.cn https://rox-test.lexicorn.cn; do
  n=$(curl -s -m 15 "$base/" | grep -c '__BRANDING__')
  b=$(curl -s -m 15 "$base/" | wc -c | tr -d ' ')
  echo "$base branding=$n bytes=$b"
done
echo "RELEASE_DONE=1 TS=$TS"
REMOTE_EOF
echo "RELEASE_EXIT=$?"
