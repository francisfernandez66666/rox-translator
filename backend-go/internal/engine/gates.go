// ============ 本文件职责中文说明 ============
// 输出质量闸门：主翻译路径（文本 HandleText / 文件 HandleFile）在翻译完成后对译文
// 施加的约束与文化校验层（整改 R1）。
//   - gateCtx：主翻译路径的质量闸门上下文，自带调用方租户的安全句（避雷词/风格/替换对），
//     避免每条译文逐次查库；newGateCtx 负责加载调用方租户及其共享宿主（租户1）的安全句。
//   - check：对单条译文执行约束闸门 gate.Run（数字/金额/单位/专有名词一致性等硬校验）
//     与语言文化闸门 culture.Run（避雷词/格式/语气），retry=true 时约束项首轮不过带反馈重翻一次。
//   - applyOutputGates / applySegmentGates：分别面向文本主路径与文件主路径的批量闸门校验，
//     返回仅以警告形式透出的违规提示，绝不静默丢弃译文内容。
// =============================================
package engine

import (
	"context"
	"fmt"
	"strings"

	"translator/internal/config"
	"translator/internal/culture"
	"translator/internal/gate"
	"translator/internal/store"
	"translator/internal/tenant"
)

// gateCtx 主翻译路径（整改 R1）输出质量闸门上下文：
// 自带调用方租户的安全句（避雷词 / 风格 / 替换对），避免每次逐条查库。
type gateCtx struct {
	safety []*store.KBSafetyPhrase
}

// newGateCtx 构造输出闸门上下文，加载调用方租户及共享宿主（租户1）的安全句。
func (e *Engine) newGateCtx(ctx context.Context) gateCtx {
	gc := gateCtx{}
	if e.St == nil {
		return gc
	}
	if tid := tenant.FromContext(ctx); tid > 0 {
		if ph, err := e.St.ListSafetyPhrases(tid); err == nil {
			gc.safety = ph
		}
		// 共享宿主（租户1）投放的全局安全句对本租户同样生效
		if ph, err := e.St.ListSafetyPhrases(1); err == nil {
			gc.safety = append(gc.safety, ph...)
		}
	}
	return gc
}

// check 对单条译文执行约束闸门（gate.Run 8 项硬校验）与语言文化闸门（culture.Run 避雷词/格式/语气）。
// retry=true 时若约束项首轮不过，则带反馈让模型重翻一次（仅文本主路径使用，文件主路径为成本考虑不重翻）。
// 返回（修正后译文，警告列表）；任何情况下均不静默丢弃译文内容（仅以警告形式透出）。
func (g gateCtx) check(source, lang, translated string, retry bool, e *Engine, ctx context.Context) (string, []string) {
	var warnings []string
	tr := translated
	if strings.TrimSpace(tr) == "" {
		return tr, warnings
	}
	// 1) 约束闸门：数字/金额/单位/专有名词一致性等硬校验
	gr := gate.Run(source, lang, tr)
	if !gr.Pass {
		if retry {
			msg := "请修正以下质量问题后重新翻译：" + firstGateFail(gr.Checks)
			if fixed := e.TranslateWithFeedback(ctx, source, lang, msg, config.StageAIInitial); strings.TrimSpace(fixed) != "" && gate.Run(source, lang, fixed).Pass {
				tr = fixed
			}
		}
		if !gate.Run(source, lang, tr).Pass {
			warnings = append(warnings, fmt.Sprintf("%s 译文未通过质量校验(%s)", langLabel(lang), firstGateFail(gate.Run(source, lang, tr).Checks)))
		}
	}
	// 2) 语言文化闸门：输出侧避雷词 / 格式 / 语气校验（仅警告透出，不在主路径静默丢弃）
	cr := culture.Run(lang, tr, g.safety)
	if !cr.Pass {
		warnings = append(warnings, fmt.Sprintf("%s 语言文化提示：%s", langLabel(lang), strings.Join(cr.Reasons, ";")))
	}
	return tr, warnings
}

// applyOutputGates 文本主路径（HandleText）：对全部目标语言译文做一次性质量闸门校验。
func (e *Engine) applyOutputGates(ctx context.Context, source string, translations map[string]string, retry bool) []string {
	if len(translations) == 0 {
		return nil
	}
	gc := e.newGateCtx(ctx)
	var warnings []string
	for lc, tr := range translations {
		fixed, ws := gc.check(source, lc, tr, retry, e, ctx)
		if fixed != tr {
			translations[lc] = fixed
		}
		warnings = append(warnings, ws...)
	}
	return capWarnings(warnings)
}

// applySegmentGates 文件主路径（HandleFile）：对流式分段译文按语言做质量闸门校验。
// retry=true 时约束闸门首轮不过则带反馈重翻一次（文件交付物如成本表必须保证数字/格式正确，
// 整改：原「文件不重翻」会导致硬闸失效、错误直接落入成品文件）。
func (e *Engine) applySegmentGates(ctx context.Context, langTranslations map[string]map[string]string, retry bool) []string {
	if len(langTranslations) == 0 {
		return nil
	}
	gc := e.newGateCtx(ctx)
	var warnings []string
	for lc, m := range langTranslations {
		for orig, tr := range m {
			fixed, ws := gc.check(orig, lc, tr, retry, e, ctx)
			if fixed != tr {
				m[orig] = fixed
			}
			warnings = append(warnings, ws...)
		}
	}
	return capWarnings(warnings)
}

// firstGateFail 取首个未通过约束项的描述，便于在警告/重翻反馈中提示。
// Detail 存在时一并附带（如哈萨克语必须使用西里尔字母），让重翻反馈对模型可执行。
func firstGateFail(checks []gate.Check) string {
	for _, c := range checks {
		if !c.Pass {
			if c.Detail != "" {
				return c.Name + "（" + c.Detail + "）"
			}
			return c.Name
		}
	}
	return ""
}

// langLabel 语言代码转可读标签。
func langLabel(lc string) string {
	if n, ok := config.LangNames[lc]; ok && n != "" {
		return n
	}
	return lc
}

// capWarnings 限制警告条数，避免大文件产生海量提示。
func capWarnings(ws []string) []string {
	if len(ws) > 100 {
		return ws[:100]
	}
	return ws
}
