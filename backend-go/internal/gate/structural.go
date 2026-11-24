// ============ structural.go · 职责说明 ============
// Tier3 候选与重译产物的结构化质量闸唯一实现：源译同形、目标书写系统、
// 拉丁转写、全角标点四项确定性检查。crawler、qualityjudge 与历史审批工具共用。
// ================================================================
package gate

import "strings"

// StructuralReject 返回非空原因表示候选应拒绝；空串表示通过。
func StructuralReject(srcLang, srcText, tgtLang, tgtText string) string {
	if strings.TrimSpace(srcText) == strings.TrimSpace(tgtText) {
		return "源译同形（疑似原样吐回）"
	}
	if !TargetTextMatchesLang(tgtLang, tgtText) {
		return "目标书写系统与语种不符"
	}
	if viols := LatinTransliterationViolations(tgtLang, tgtText, nil); len(viols) > 0 {
		return "拉丁转写：" + strings.Join(viols, ",")
	}
	if viols := CJKPunctViolations(tgtLang, tgtText, nil); len(viols) > 0 {
		return "全角标点残留：" + strings.Join(viols, ",")
	}
	return ""
}
