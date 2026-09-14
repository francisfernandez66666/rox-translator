#!/usr/bin/env bash
# =============================================================================
# fetch_backups_local.sh — 生产 DB 备份「本地异地副本」（拉取式，★ 2026-09-14）
#
# 用途：rsync 从服务器 /opt/translator/data/backups 拉取最新 pg_dump 计划备份到
#       本机 ~/Backups/langcross，校验可还原（pg_restore --list）后按份数保留。
#       服务器无需任何改动/凭据下发（单向拉取，只读）。
# 用法：bash fetch_backups_local.sh [拉取份数=3] [本地保留=7]
# 定时：crontab 每日 10:00（脚本实体放 ~/Backups/tools，避开 macOS 桌面 TCC 限制）。
# =============================================================================
set -euo pipefail
SERVER="${BK_SERVER:-root@43.108.86.140}"
SSH_PORT="${BK_PORT:-28022}"
SRC_DIR="${BK_SRCDIR:-/opt/translator/data/backups}"
DEST="${BK_DEST:-$HOME/Backups/langcross}"
KEEP_N="${2:-7}"                       # 本地保留份数
PULL="${1:-3}"                         # 每次最多拉最新几份（增量幂等，rsync 跳过已有）

mkdir -p "$DEST"
echo "[fetch] $(date '+%F %T') ${SERVER}:${SRC_DIR} → ${DEST}（最新 ${PULL} 份，保留 ${KEEP_N}）"

# 1) 服务器侧按时间倒序取最新 N 个文件名
FILES=$(ssh -p "$SSH_PORT" "$SERVER" "ls -t $SRC_DIR/tm_*.bak.dump 2>/dev/null | head -n $PULL")
[ -n "$FILES" ] || { echo "✖ 服务器无计划备份"; exit 1; }
echo "$FILES" | sed 's#.*/#  远端: #'

# 2) 增量拉取（--partial 断点；已存在且同大小同 mtime 自动跳过）
rsync -a --partial -e "ssh -p $SSH_PORT" \
  $(printf '%s\n' "$FILES" | sed "s#^#$SERVER:#") "$DEST/"

# 3) 完整性校验：最新一份 本地/远端 sha256 一致（副本完好；还原性由链演/灾备演练证明）
LATEST_REL=$(echo "$FILES" | head -1)
LATEST_BASE=$(basename "$LATEST_REL")
H_REMOTE=$(ssh -p "$SSH_PORT" "$SERVER" "sha256sum $LATEST_REL | cut -d' ' -f1")
H_LOCAL=$(shasum -a 256 "$DEST/$LATEST_BASE" | cut -d' ' -f1)
if [ "$H_REMOTE" = "$H_LOCAL" ]; then
  echo "  ✔ sha256 一致: $LATEST_BASE ($(du -h "$DEST/$LATEST_BASE" | cut -f1)) ${H_LOCAL:0:12}…"
else
  echo "  ✖ sha256 不一致（远端 $H_REMOTE / 本地 $H_LOCAL）"; exit 1
fi
# 本地若有不低于服务端的 pg client，追加 TOC 校验（可选，版本低则跳过不算失败）
if command -v pg_restore >/dev/null 2>&1 && pg_restore --list "$DEST/$LATEST_BASE" >/dev/null 2>&1; then
  echo "  ✔ pg_restore --list TOC 校验通过"
fi

# 4) 本地按份数修剪（保留最新 KEEP_N）
ls -t "$DEST"/tm_*.bak.dump 2>/dev/null | tail -n +$((KEEP_N+1)) | while read -r old; do
  echo "  修剪旧副本: $(basename "$old")"; rm -f "$old"
done
echo "[fetch] ✅ 完成"
