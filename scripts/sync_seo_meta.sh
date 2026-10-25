#!/usr/bin/env bash
# ============================================================================
# scripts/sync_seo_meta.sh — SEO 路由 meta 单一事实源同步（2026-10-24）
# 源：frontend-react/src/seo/route_meta.json
# 目标：backend-go/internal/api/seo_meta.json（//go:embed 只认包内文件）
# 用法：bash scripts/sync_seo_meta.sh [--check]
#   --check：只比对 sha，不一致退出 2（CI/发版闸门用）
# ============================================================================
set -uo pipefail
sha() { if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; else sha256sum "$1" | awk '{print $1}'; fi; }
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/frontend-react/src/seo/route_meta.json"
DST="$ROOT/backend-go/internal/api/seo_meta.json"
[ -f "$SRC" ] || { echo "ABORT=1 源文件缺失: $SRC"; exit 2; }
if [ "${1:-}" = "--check" ]; then
  [ -f "$DST" ] || { echo "ABORT=2 目标文件缺失: $DST"; exit 2; }
  s=$(sha "$SRC")
  d=$(sha "$DST")
  [ "$s" = "$d" ] || { echo "ABORT=3 SEO meta 不同步: src=$s dst=$d"; exit 2; }
  echo "SEO_META_SYNC_OK=1 sha=$s"
  exit 0
fi
cp "$SRC" "$DST"
echo "SEO_META_SYNCED=1 sha=$(sha "$DST")"
