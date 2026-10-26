#!/usr/bin/env bash
# ============================================================================
# deploy/audit_kb_terms_20261026.sh — P2-03 术语脏数据盘点与清理（2026-10-26）
#
# 背景：2026-10-25 云端 UAT 发现 quality gate 报
#   「知识库术语「中文」应译作 英语，译文未体现」
# 根因是 kb_entries 里有一条 target_lang=en、target_text=英语（中文）的脏术语；
# 源词「中文」会命中几乎任何中文正文，而英文译文当然不含中文「英语」二字。
#
# 口径：
#   - 默认**只读盘点**，不写库；
#   - APPLY=1 才备份 + 删除「target_lang=en 且 target_text 全是中文」的术语行；
#   - 刻意**不删** source_text='服务' 的正常术语（「服务→Service」是大小写匹配缺陷，
#     代码侧已修，删它会伤术语一致性）；
#   - 备份在 schema fix_bak_20261026_terms；按 id 可回滚。
#
# 用法：
#   bash deploy/audit_kb_terms_20261026.sh           # 只读盘点
#   APPLY=1 bash deploy/audit_kb_terms_20261026.sh   # 备份 + 删除确认的脏行
#
# 说明：本脚本在部署机上以 postgres 用户跑（与 audit_culture_en_20261024.sh 同口径）。
# ============================================================================
set -uo pipefail

APPLY="${APPLY:-0}"
DB="${DB:-langcross}"
PSQL=(sudo -u postgres psql -d "$DB" -Atc)
# 脏行判据（保守）：L1 术语 + 目标语言 en + 目标文本含汉字 + 源词是**语言名**。
# 不按“target_lang=en 且目标是中文”一刀切：品牌名等正常条目可能刻意保留汉字；
# 运行期 termReqs 另有“非 brand 且书写系统不符就跳过”的第二道防线。
WHERE="layer=1 AND target_lang='en' AND target_text ~ '[一-鿿]' AND source_text IN ('中文','英文','英语','日语','韩语','法语','德语','俄语','西班牙语','葡萄牙语','阿拉伯语','泰语')"

echo "===== 1) 脏术语候选（target_lang=en 但 target_text 含中文） ====="
"${PSQL[@]}" "SELECT id||'|'||tenant_id||'|'||package_id||'|'||source_lang||'|'||source_text||'|'||target_lang||'|'||target_text FROM kb_entries WHERE ${WHERE} ORDER BY id;" 2>/dev/null || { echo "ABORT=1 读库失败"; exit 2; }

echo "===== 2) 正常的「服务→Service」术语（只读，不删） ====="
"${PSQL[@]}" "SELECT id||'|'||tenant_id||'|'||package_id||'|'||source_text||'|'||target_lang||'|'||target_text FROM kb_entries WHERE layer=1 AND source_text='服务' ORDER BY id LIMIT 20;" 2>/dev/null || true

echo "===== 3) 目标书写系统统计（排障用） ====="
"${PSQL[@]}" "SELECT target_lang||'|'||count(*) FROM kb_entries WHERE layer=1 GROUP BY target_lang ORDER BY target_lang;" 2>/dev/null || true

if [ "$APPLY" != "1" ]; then
  echo "MODE=audit（未写库）；确认候选后 APPLY=1 执行备份+删除"
  exit 0
fi

echo "===== 4) 备份（schema fix_bak_20261026_terms） ====="
"${PSQL[@]}" "CREATE SCHEMA IF NOT EXISTS fix_bak_20261026_terms; DROP TABLE IF EXISTS fix_bak_20261026_terms.kb_entries; CREATE TABLE fix_bak_20261026_terms.kb_entries AS SELECT * FROM kb_entries WHERE ${WHERE};" 2>/dev/null || { echo "ABORT=2 备份失败，未删除任何行"; exit 3; }

echo "===== 5) 删除确认的脏行（只删备份里那批 id） ====="
"${PSQL[@]}" "DELETE FROM kb_entries WHERE id IN (SELECT id FROM fix_bak_20261026_terms.kb_entries);" 2>/dev/null || { echo "ABORT=3 删除失败（备份仍在，可回滚）"; exit 4; }

echo "===== 6) 回读 ====="
"${PSQL[@]}" "SELECT 'remaining_bad='||count(*) FROM kb_entries WHERE ${WHERE};" 2>/dev/null || true
"${PSQL[@]}" "SELECT 'backup_rows='||count(*) FROM fix_bak_20261026_terms.kb_entries;" 2>/dev/null || true
echo "DONE=1 回滚：INSERT INTO kb_entries SELECT * FROM fix_bak_20261026_terms.kb_entries 中对应 id 的行"
