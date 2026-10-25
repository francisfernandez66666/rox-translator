#!/usr/bin/env bash
# ============================================================================
# deploy/audit_culture_en_20261024.sh — N-02 英语避雷词收缩（2026-10-24 用户令）
#
# 口径：正常沟通交流的词不需要避雷；自动采集的单字泛词只观测，人工确认后才拦截。
# 本脚本默认**只读审计**；APPLY=1 才降级（status='pending'，不删除、可回滚）。
#
# 用法：
#   bash deploy/audit_culture_en_20261024.sh           # 只读盘点
#   APPLY=1 bash deploy/audit_culture_en_20261024.sh   # 备份 + 降级自动采集单字
#
# 回滚：备份在 schema fix_bak_20261024_culture；按 id 恢复 status/source。
# ============================================================================
set -uo pipefail

APPLY="${APPLY:-0}"
DB="${DB:-langcross}"
PSQL=(sudo -u postgres psql -d "$DB" -Atc)

echo "===== 1) 英语 approved 规则盘点 ====="
"${PSQL[@]}" "SELECT kind||'|'||COALESCE(source,'')||'|'||count(*)||'|单字='||sum(CASE WHEN position(' ' in phrase)=0 THEN 1 ELSE 0 END) FROM kb_safety_phrases WHERE lang='en' AND COALESCE(status,'approved')='approved' GROUP BY kind, COALESCE(source,'') ORDER BY kind, COALESCE(source,'');" 2>/dev/null || { echo "ABORT=1 读库失败"; exit 2; }

echo "===== 1b) 英语 forbidden 全量（人工复核用） ====="
"${PSQL[@]}" "SELECT id||'|'||phrase||'|'||COALESCE(source,'')||'|'||COALESCE(status,'') FROM kb_safety_phrases WHERE lang='en' AND COALESCE(kind,'')='forbidden' AND COALESCE(status,'approved')='approved' ORDER BY id;" 2>/dev/null || true

echo "===== 2) 将被降级的候选（自动采集 + 单字） ====="
"${PSQL[@]}" "SELECT id||'|'||phrase||'|'||COALESCE(kind,'')||'|'||COALESCE(source,'') FROM kb_safety_phrases WHERE lang='en' AND COALESCE(kind,'')='forbidden' AND COALESCE(source,'llm') <> 'manual' AND position(' ' in phrase)=0 ORDER BY id LIMIT 50;" 2>/dev/null || true

if [ "$APPLY" != "1" ]; then
  echo "MODE=audit（未写库）；确认后 APPLY=1 执行备份+降级"
  exit 0
fi

echo "===== 3) 备份（schema fix_bak_20261024_culture） ====="
"${PSQL[@]}" "CREATE SCHEMA IF NOT EXISTS fix_bak_20261024_culture; DROP TABLE IF EXISTS fix_bak_20261024_culture.safety_en; CREATE TABLE fix_bak_20261024_culture.safety_en AS SELECT * FROM kb_safety_phrases WHERE lang='en';" 2>/dev/null || { echo "ABORT=2 备份失败，未写任何降级"; exit 3; }

echo "===== 4) 降级（status=pending，不删除） ====="
# 4a 自动采集单字（当前生产无 llm 行，保留这条防未来漂移）
"${PSQL[@]}" "UPDATE kb_safety_phrases SET status='pending' WHERE lang='en' AND COALESCE(kind,'')='forbidden' AND COALESCE(source,'llm') <> 'manual' AND position(' ' in phrase)=0;" 2>/dev/null || { echo "ABORT=3 降级失败（备份仍在）"; exit 4; }
# 4b 人工确认但属于日常沟通/社会议题描述的 forbidden（2026-10-24 用户令：正常沟通不避雷）。
#    保留 white power（仇恨口号）与「蕃夷」（历史贬称）这两个真正的敏感词。
"${PSQL[@]}" "UPDATE kb_safety_phrases SET status='pending'
WHERE lang='en' AND COALESCE(kind,'')='forbidden' AND COALESCE(status,'approved')='approved'
  AND (
    lower(trim(phrase)) IN (
      'blackmail','native language','discriminate against','stereotype','discriminate',
      'blasphemy','racist','obnoxious','stupid','black friday','christmas tree',
      'racial discrimination','sexism','black sheep','politically correct','divorce',
      'cheater','colorism','racial profiling','culture clash','exotic','n/a','white guy'
    )
    OR phrase LIKE '营销文案避免%' OR phrase LIKE '避免带有%' OR phrase LIKE '避免使用明显%'
  );" 2>/dev/null || { echo "ABORT=3 日常词降级失败（备份仍在）"; exit 4; }

echo "===== 5) 回读 ====="
"${PSQL[@]}" "SELECT 'remaining_forbidden='||count(*) FROM kb_safety_phrases WHERE lang='en' AND COALESCE(kind,'')='forbidden' AND COALESCE(status,'approved')='approved';" 2>/dev/null || true
echo "DONE=1 回滚：从 fix_bak_20261024_culture.safety_en 按 id 恢复 status/source"
