#!/usr/bin/env bash
# ============ translation_quality_regression.sh · 职责说明 ============
# 翻译系统质量提升（2026-11-20 P0–P5）的**定向回归脚本**：把当天新增/改造的
# 全部测试聚合成一条命令，供改翻译链代码后快速自证，不必跑全量 go test（分钟级、且全量会
# 撑大沙盒 profile）。全部通过时末行输出 TRANS_QUALITY_REGRESSION_ALL_OK=1。
#
# 覆盖范围（对应方案《施工方案_翻译系统语言污染与结构保真_20261120.md》§11）：
#   P0 骨架判据     — textscript/target_consistency + engine/translation_guard_lang
#   P1 Markdown保真 — fileproc/md_marker_conserve + md_structure(Md/RoundTrip)
#   P2 字符污染     — gate/script_consistency + engine/foreign_script_strip
#   P3 高频词/复核  — engine/highfreq + gate/term_consistency + engine/delivery_notes(物理行号)
#   P5 门禁         — gateguard 登记 + engine/file_fidelity 指纹
#
# 用法：bash scripts/translation_quality_regression.sh
# 环境：只方言钉 sqlite，避免 UAT 的 PG env 泄漏进内存 SQLite 单测。
# ============================================
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/.." && pwd)"
cd "$REPO_ROOT/backend-go" || exit 1

PASS=0
FAILN=0
run() {
  local label="$1"; shift
  echo "--- $label ---"
  # 反空转：捕获完整输出后再判。go test 对「-run 模式命中 0 个测试」仍返回 0 且
  # 只打 testing: warning: no tests to run —— 那是假绿（本批 TestFileWritePoints 真踩过）。
  # 这里命中该警告即判失败，杜绝「脚本全绿但守卫从没跑过」。
  local out
  out=$(env DB_DRIVER=sqlite DB_DSN= go test -count=1 "$@" 2>&1)
  local rc=$?
  printf '%s\n' "$out" | tail -5
  if [ "$rc" -ne 0 ] || printf '%s' "$out" | grep -q 'no tests to run'; then
    echo "FAIL:$label（非零退出或 -run 命中 0 测试）"
    FAILN=$((FAILN+1))
  else
    PASS=$((PASS+1))
  fi
}

# P0 判据层
run "P0:骨架判据(textscript)" ./internal/textscript/ -run 'TestTargetConsistency|TestLangToScript'
run "P0:骨架判据(engine)"    ./internal/engine/ -run 'TestIsSkeletonForLang|TestCJKEcho'

# P1 结构层
run "P1:Markdown标记守恒"    ./internal/fileproc/ -run 'TestMdMarkerConserve|TestMdRestoreMarkers|TestMdRealDocRoundTrip|TestMdInlineMarkers|TestMdLinkUrlProtected|TestMdTableCellColumnsConserved'
run "P1:结构指纹"            ./internal/engine/ -run 'Fidelity'

# P2 脚本层
run "P2:脚本一致性(gate)"    ./internal/gate/ -run 'TestScriptConsistency|TestTargetChars|TestRunWithTerms|TestRunNoFalsePositive'
run "P2:外来脚本清洗"        ./internal/engine/ -run 'TestStripForeignScripts|TestPostProcessTranslationNoFalseStrip|TestPostProcess|PostProcess'

# P3 高频词/复核层
run "P3:高频词抽取"          ./internal/engine/ -run 'TestHighfreq|TestStripCodeFence'
run "P3:术语一致性(gate)"    ./internal/gate/ -run 'TestCheckTermConsistency|TestTermRequirementAliases'
# ★ 2026-11-20 修正：原 -run 模式 TestFileWritePoints 命中 0 个函数（真实名为
#   TestFileTranslationWritePointsAllGuarded / TestFileWritePointScannerDetectsUnguardedWrite），
#   该腿此前是「假绿」——写点守卫没被回归脚本真正跑到。改为两个真实名，杜绝空转。
run "P3:写点/未译清单"       ./internal/engine/ -run 'TestApplySegmentGates|TestDeliveryNotes|TestUntranslatedPhysicalLineMap|TestFileTranslationWritePointsAllGuarded|TestFileWritePointScannerDetectsUnguardedWrite'

# P5 门禁登记
run "P5:gateguard登记"       ./internal/gateguard/

echo "============================================================"
echo "PASS=$PASS FAIL=$FAILN"
if [ "$FAILN" -eq 0 ]; then
  echo "TRANS_QUALITY_REGRESSION_ALL_OK=1"
  exit 0
else
  echo "TRANS_QUALITY_REGRESSION_ALL_OK=0"
  exit 1
fi
