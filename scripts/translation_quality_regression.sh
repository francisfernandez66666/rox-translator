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
    echo "FAIL:${label}（非零退出或 -run 命中 0 测试）"
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

# ============================================================================
# ★ 2026-11-22 今日开发内容（真值 tv8–tv10 暴露的缺陷 + 全量红灯收口），
#   按「新增一条锁就进回归脚本」的纪律逐条登记，防止只跑全量时漏掉定向自证。
# 7 处译文缺陷的四类根因（提交 c922ccb2/13328fbe）
run "11-22:数字尾巴/量级口径" ./internal/engine/ -run 'TestDeliveryTailGarbage|TestMagnitudeInstruction|TestQuantity'
run "11-22:标题编号骨架"      ./internal/fileproc/ -run 'TestHeadingNumberBlock|TestMdHeadingNumber'
run "11-22:外来组合符/泰国"   ./internal/engine/ -run 'TestForeignScript|TestStripForeignScriptsInNonTarget'
# 数字换算判据与上下文重译（提交 1452092b/a6af7581）
run "11-22:数字期望判据"      ./internal/gate/ -run 'TestNumberExpectations|TestNumberExpectationsAgreeWithJudge|TestScriptConsistencyNeutralCell'
run "11-22:上下文重译分档"    ./internal/engine/ -run 'TestFileCtxRetryTargetsTierOrder|TestFileCtxRetryNumberExpectationsInPrompt|TestFileCtxRetryMissingSegmentGetsNumberExpectations|TestFileCtxRetryCandidatesOrderAndLimit'
run "11-22:救援段复核evals"   ./internal/engine/ -run 'TestRescueReviewEvalDegradesQuietly|TestReviewScope'
# ★ 2026-11-22 诊断日志：救援段被拒原因名必须与采纳布尔同源（单一判据）
run "11-22:被拒诊断同源"      ./internal/engine/ -run 'TestRejectReason'
# ★ 2026-11-22 tv12：数量表达豁免（正确纯数字译文不被骨架/回显误杀，错量级仍拒）
run "11-22:数量交付豁免"      ./internal/engine/ -run 'TestQuantityDelivery|TestRejectReasonNamesSkeleton'
# 全量 -race 四处红灯修复（提交 d4edb4bf）：SQL 方言包装 + D3 字面量门禁 + 守卫反证
run "11-22:SQL方言包装守卫"   ./internal/db/ -run 'TestNoRawSQLOutsideDialectWrapper|TestRawSQLPatternHasTeeth|TestNoRawUniqueViolationLiteralOutsideHelper|TestUniqueViolationLiteralScanHasTeeth|TestNoUncoveredDialectConstructs|TestDialectScanPatternsActuallyFire'
run "11-22:方言裸SQL修复回归" ./internal/store/ -run 'TestOutboundNoAntiPatterns'
# ★ 2026-11-22 tv13 终轮：静默垃圾格、金额格确定性换算、标注节按行去重
run "11-22:金额格确定性交付" ./internal/engine/ -run 'TestDeterministicNumericDelivery|TestNumericCellShortLetterGarbage|TestDeliveryTranslationsNoShortLetterGarbage'
run "11-22:标注节按行去重" ./internal/engine/ -run 'TestUntranslatedNotesKeepEverySegment|TestUntranslatedNotesHeaderCountsUniqueLines|TestUntranslatedNotesNoMapKeepsSegments'
run "11-22:工单时限放宽" ./internal/service/ -run 'TestJobExecTimeout|TestMediaTimeoutForFiles|TestLongTaskBudgetsRaised'
run "11-22:多Key均衡调用" ./internal/engine/ -run 'TestPickPrimaryRotatesKeysInSamePool|TestPickPrimaryDoesNotMixDifferentPools|TestRouteQuotaFieldsOptional'
run "11-23:交付数字守恒" ./internal/engine/ -run 'TestVerifyDeliveredNumbers'
run "11-23:相邻加粗骨架" ./internal/fileproc/ -run 'TestRestoreAdjacentBoldSkeleton'

# ★ 2026-11-22 tv13：判读脚本「区间共享量级词」假红锁。
#   v1 把正确译文 `1.5–2.5 מיליון` 读成 [1.5, 2.5e6] ⇒ L160 假红；v3 修后
#   必须同时保证 65–120 מיליון / 300 מיליון+ 这类真量级错仍判红（反向对照在自锁里）。
if python3 "$REPO_ROOT/output/tv_acceptance_20261121/tv_compare_selftest.py" >/tmp/tv_compare_selftest.log 2>&1; then
  PASS=$((PASS+1))
  printf '%s\n' "OK:11-22:判读脚本量级词假红锁"
else
  echo "FAIL:11-22:判读脚本量级词假红锁"
  tail -12 /tmp/tv_compare_selftest.log
  FAILN=$((FAILN+1))
fi

echo "============================================================"
echo "PASS=$PASS FAIL=$FAILN"
if [ "$FAILN" -eq 0 ]; then
  echo "TRANS_QUALITY_REGRESSION_ALL_OK=1"
  exit 0
else
  echo "TRANS_QUALITY_REGRESSION_ALL_OK=0"
  exit 1
fi
