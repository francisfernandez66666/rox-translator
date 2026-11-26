#!/usr/bin/env bash
# ============ run_kb_hardgate_clean.sh · layer=1 硬闸词库全语种自动清洗 ============
# 顺序：en → ru → ar → he → 其余语种按码升序；每语种先 REPORT 出证据/裁决，再 APPLY。
# 前置：DB_DSN / JWT_SECRET 由环境注入（脚本不打印）；KB_CLEAN_BIN 指向已编译的 kb-clean。
# 低占用：jobs 表有 ticket_run queued/running 时最多等 20 轮（30s/轮），超时即中止本轮。
# 任一语种失败即停止，不继续后面的语种；已成功语种的备份留在 OUT_ROOT/<lang>/。
# ============================================================
set -euo pipefail

ROOT="${ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
BIN="${KB_CLEAN_BIN:-/tmp/kb-clean-hard}"
OUT_ROOT="${OUT_ROOT:-/var/lib/langcross/kbclean/$(date +%Y%m%d%H%M%S)}"
PRIORITY="en ru ar he"

if [[ -z "${DB_DSN:-}" ]]; then
  echo "缺少 DB_DSN（不要写进脚本，由环境注入）" >&2
  exit 2
fi
if [[ ! -x "$BIN" ]]; then
  echo "缺少可执行文件 ${BIN}；请先 go build -o $BIN ./cmd/kb-clean" >&2
  exit 2
fi
mkdir -p "$OUT_ROOT"

# G-S0 基线门禁：全新跑批才校验；带 checkpoint 的续跑允许 layer=1 数量已下降。
if ! find "$OUT_ROOT" -name checkpoint.txt -print -quit | grep -q .; then
  base_total=$(psql "$DB_DSN" -At -c "SELECT count(*) FROM kb_entries WHERE layer=1 AND source_lang='zh' AND module LIKE 'scrape:%' AND length(trim(target_text))>0")
  base_langs=$(psql "$DB_DSN" -At -c "SELECT count(DISTINCT target_lang) FROM kb_entries WHERE layer=1 AND source_lang='zh' AND module LIKE 'scrape:%' AND length(trim(target_text))>0")
  base_srcs=$(psql "$DB_DSN" -At -c "SELECT count(DISTINCT source_text) FROM kb_entries WHERE layer=1 AND source_lang='zh' AND module LIKE 'scrape:%' AND length(trim(target_text))>0")
  if [[ "$base_total" != "43248" || "$base_langs" != "34" || "$base_srcs" != "8311" ]]; then
    echo "[kb-hardgate] 基线不符 total=$base_total langs=$base_langs srcs=${base_srcs}，期望 43248/34/8311" >&2
    exit 2
  fi
  for sid in 1 5 16 18 20; do
    en=$(psql "$DB_DSN" -At -c "SELECT enabled FROM kb_pack_sources WHERE id=$sid")
    if [[ "$en" != "0" ]]; then
      echo "[kb-hardgate] 来源 $sid 未停用 enabled=$en" >&2
      exit 2
    fi
  done
  echo "[kb-hardgate] 基线校验通过 43248/34/8311"
else
  echo "[kb-hardgate] 断点续跑模式，跳过初始基线校验"
fi

all_langs=$(psql "$DB_DSN" -At -c "SELECT DISTINCT target_lang FROM kb_entries WHERE layer=1 AND source_lang='zh' AND module LIKE 'scrape:%' AND length(trim(target_text))>0 ORDER BY 1")
ordered=""
for l in $PRIORITY; do
  if printf '%s\n' "$all_langs" | grep -qx "$l"; then
    ordered="$ordered $l"
  fi
done
for l in $all_langs; do
  skip=0
  for p in $PRIORITY; do [[ "$l" == "$p" ]] && skip=1 && break; done
  [[ "$skip" -eq 0 ]] && ordered="$ordered $l"
done

echo "[kb-hardgate] OUT_ROOT=$OUT_ROOT"
echo "[kb-hardgate] 语种顺序:$ordered"

wait_idle() {
  local n=0
  while :; do
    local busy
    busy=$(psql "$DB_DSN" -At -c "SELECT count(*) FROM jobs WHERE type='ticket_run' AND status IN ('queued','running')")
    [[ "$busy" == "0" ]] && return 0
    n=$((n+1))
    if [[ "$n" -ge 20 ]]; then
      echo "[kb-hardgate] 翻译任务持续占用，放弃本轮" >&2
      return 3
    fi
    sleep 30
  done
}

for lang in $ordered; do
  out="$OUT_ROOT/$lang"
  mkdir -p "$out"
  wait_idle

  if [[ -f "$out/checkpoint.txt" ]]; then
    echo "[kb-hardgate] RESUME lang=${lang}（已有 checkpoint，跳过 REPORT，保留全量证据/裁决）"
  else
    echo "[kb-hardgate] REPORT lang=$lang"
    DB_DRIVER=postgres DB_DSN="$DB_DSN" JWT_SECRET="${JWT_SECRET:-}" \
      "$BIN" -scope hardgate -lang "$lang" -report -out "$out"
    if [[ ! -s "$out/decision.jsonl" || ! -s "$out/evidence.jsonl" ]]; then
      echo "[kb-hardgate] lang=$lang 证据/裁决文件为空，中止" >&2
      exit 4
    fi
    bad=$(python3 -c 'import json,sys
bad=0
for line in open(sys.argv[1],encoding="utf-8"):
    d=json.loads(line)
    if d.get("action") not in ("KEEP","REPLACE","DEMOTE_LAYER2"): bad+=1
    if d.get("action")=="REPLACE" and not d.get("new_text"): bad+=1
print(bad)' "$out/decision.jsonl")
    if [[ "$bad" != "0" ]]; then
      echo "[kb-hardgate] lang=$lang 裁决文件异常 $bad 行，中止" >&2
      exit 5
    fi
  fi

  echo "[kb-hardgate] APPLY lang=$lang"
  DB_DRIVER=postgres DB_DSN="$DB_DSN" JWT_SECRET="${JWT_SECRET:-}" \
    "$BIN" -scope hardgate -lang "$lang" -apply -out "$out"

  # S6 验收断言（SQL 侧）：冲突收敛 / 旧错词清零 / canonical 落位 / 负对照未误伤
  conflict=$(psql "$DB_DSN" -At -c "SELECT count(*) FROM (SELECT source_text FROM kb_entries WHERE layer=1 AND source_lang='zh' AND module LIKE 'scrape:%' AND target_lang='$lang' GROUP BY source_text HAVING count(DISTINCT target_text)>1) x")
  pending=$(psql "$DB_DSN" -At -c "SELECT count(*) FROM kb_entries WHERE layer=1 AND source_lang='zh' AND module LIKE 'scrape:%' AND target_lang='$lang' AND target_text IN ('Retail shop','Магазин','Distributor') AND source_text='经销商'")
  echo "[kb-hardgate] lang=$lang conflict_groups=$conflict old_wrong=$pending"
  if [[ "$conflict" != "0" || "$pending" != "0" ]]; then
    echo "[kb-hardgate] lang=$lang S6 验收失败，停止后续语种" >&2
    exit 6
  fi
  if [[ "$lang" == "en" ]]; then
    canonical=$(psql "$DB_DSN" -At -c "SELECT count(*) FROM kb_entries WHERE id IN (29161,79689) AND layer=1 AND target_text='dealer'")
    demoted=$(psql "$DB_DSN" -At -c "SELECT count(*) FROM kb_entries WHERE id=29234 AND layer=2")
    supplier=$(psql "$DB_DSN" -At -c "SELECT count(*) FROM kb_entries WHERE source_text='供应商' AND target_lang='en' AND layer=1 AND target_text='supplier'")
    echo "[kb-hardgate] en canonical=$canonical demoted=$demoted supplier_kept=$supplier"
    if [[ "$canonical" != "2" || "$demoted" != "1" || "$supplier" == "0" ]]; then
      echo "[kb-hardgate] en canonical/负对照断言失败" >&2
      exit 7
    fi
  fi
  if [[ "$lang" == "ru" ]]; then
    canonical=$(psql "$DB_DSN" -At -c "SELECT count(*) FROM kb_entries WHERE id IN (29498,79699) AND layer=1 AND target_text='дилер'")
    echo "[kb-hardgate] ru canonical=$canonical"
    if [[ "$canonical" != "2" ]]; then
      echo "[kb-hardgate] ru canonical 断言失败" >&2
      exit 7
    fi
  fi
done

echo "[kb-hardgate] ALL_DONE out=$OUT_ROOT"
