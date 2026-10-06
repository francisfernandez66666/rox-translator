#!/usr/bin/env bash
# deploy/housekeeping_20261006.sh —— 10-06 发版后的归档归一与裁剪（写操作，只碰归档目录/备份件）
#
# 为什么要动这一处：本仓的换源归档历来是 `web_old.<TS>`（deploy_to_production.sh:57 就是这个名），
# 而 10-06 那批新写的 release_20261006.sh 用的是 `web.old.<TS>`（点），于是同一目录长出**两族命名**，
# 而"裁到 4 份"那条口径按族各数各的——旧的 `web_old.*` 族从来没人裁（主站实测已堆 5 份），
# 新族也只裁自己。这正是 AGENTS §一·11「五条腿各拿一把尺子」在归档层的同形问题。
# 处置：① 先把点族改回下划线族（等价重命名，不丢任何一份历史）；② 再按**文件名时间戳**统一裁到 4 份；
#       ③ bin 目录的 `.bak.<TS>` 同族问题一并裁到 4 份（实测 translator=7 demo=6 assist=13 个条目）。
# 判据与释放纪律：只动 `web_old.*` / `*.bak.*` 这两种形态，现役 `web` 目录与现役二进制一律不碰；
# 每删一条先打印一条，删完复数一次剩几份，末尾出 `HEALTHY=1` 才算跑成。
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$REPO/deploy/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$CONF" | head -1)"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$CONF" | head -1)"
[ -n "$HOST" ] || { echo "ABORT=1 主机没解析出来"; exit 2; }
[ -n "$PORT" ] || PORT=22

KEEP=${KEEP:-4}
APPLY=${APPLY:-0}
echo "===== 归档归一与裁剪 KEEP=$KEEP APPLY=$APPLY 本机时刻=$(date -u +%FT%TZ) ====="
ssh -p "$PORT" -o ConnectTimeout=20 -o BatchMode=yes "root@$HOST" KEEP="$KEEP" APPLY="$APPLY" bash -s <<'REMOTE_EOF'
set -uo pipefail
KEEP=${KEEP:-4}
APPLY=${APPLY:-0}
MODE="DRY（只报清单，一个都不删）"
[ "$APPLY" = "1" ] && MODE="APPLY"
echo "mode=$MODE KEEP=$KEEP"

echo "----- 1) web 根：现役目录先自证没被牵连 -----"
for w in /opt/translator/web /opt/translator-demo/web; do
  echo "  $w 入口=$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$w/index.html" 2>/dev/null | head -1) dotfile=$(find "$w" -name '.DS_Store' -o -name '._*' | wc -l)"
done

echo
echo "----- 2) 点族归档改回下划线族（等价重命名，先列再动）-----"
for w in /opt/translator/web /opt/translator-demo/web; do
  for d in "$w".old.*; do
    [ -d "$d" ] || continue
    tgt=$(echo "$d" | sed 's/\.old\./_old./')
    # 重命名前必须问目标在不在：`mv 目录 已存在目录` 不会报错，而是把源目录**塞进**目标里，
    # 于是那一份归档凭空消失（内容还在，但换名后没人认得它、回滚也找不到）。
    if [ -e "$tgt" ]; then
      echo "  COLLISION $tgt 已存在 ⇒ 跳过重命名（改加 .dot 后缀另存）"
      if [ "$APPLY" = "1" ]; then mv "$d" "${tgt}.dot" && echo "    RENAMED->${tgt}.dot" || echo "    FAIL rename"; fi
      continue
    fi
    echo "  rename $d -> $tgt"
    if [ "$APPLY" = "1" ]; then mv "$d" "$tgt" && echo "    RENAMED" || echo "    FAIL rename"; fi
  done
done

echo
echo "----- 3) 下划线族统一裁到 KEEP 份（按**文件名时间戳**排，ls -t 会被 cp -a 继承的 mtime 骗到）-----"
for w in /opt/translator/web /opt/translator-demo/web; do
  echo "  ### $w"
  ls -1d "${w}"_old.* 2>/dev/null | sort -r > /tmp/_archive_list
  total=$(wc -l < /tmp/_archive_list)
  echo "    现存 $total 份，最新 $KEEP 份保留："
  head -n "$KEEP" /tmp/_archive_list | sed 's/^/      keep /'
  if [ "$total" -gt "$KEEP" ]; then
    tail -n +$((KEEP + 1)) /tmp/_archive_list | while read -r O; do
      sz=$(du -sk "$O" 2>/dev/null | cut -f1)
      echo "    待删 $O（${sz}K）"
      if [ "$APPLY" = "1" ]; then rm -rf "$O" && echo "      DELETED" || echo "      FAIL delete"; fi
    done
  fi
done

echo
echo "----- 4) bin 的 .bak.<TS> 同族问题：现役件先自证，再裁到 KEEP 份 -----"
for b in /opt/translator/bin/translator-server /opt/translator-demo/bin/translator-server /opt/ai-assist/bin/translator-assist; do
  echo "  ### $b 现役 sha16=$(sha256sum "$b" 2>/dev/null | cut -c1-16)"
  ls -1d "${b}".bak.* 2>/dev/null | sort -r > /tmp/_bak_list
  total=$(wc -l < /tmp/_bak_list)
  echo "    现存备份 $total 份"
  if [ "$total" -gt "$KEEP" ]; then
    tail -n +$((KEEP + 1)) /tmp/_bak_list | while read -r O; do
      echo "    待删 $O（$(du -h "$O" 2>/dev/null | cut -f1)）"
      if [ "$APPLY" = "1" ]; then rm -f "$O" && echo "      DELETED" || echo "      FAIL delete"; fi
    done
  fi
done

echo
echo "----- 5) 复数与磁盘水位 -----"
for w in /opt/translator/web /opt/translator-demo/web; do
  echo "  $w 点族=$(ls -1d "$w".old.* 2>/dev/null | wc -l) 下划线族=$(ls -1d "${w}"_old.* 2>/dev/null | wc -l)"
done
df -h / | tail -1 | awk '{print "  磁盘 used="$5" free="$4}'
echo HEALTHY=1
REMOTE_EOF
echo "HOUSEKEEPING_EXIT=$?"
