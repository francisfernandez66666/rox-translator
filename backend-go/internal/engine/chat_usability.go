// ============ 本文件职责中文说明 ============
// 即时翻译（chat/OpenAPI/试用/图片分腿）的「译文可用性写入门」。
//
// 缺陷背景（2026-10-25 云端 UAT · P0-01）：
//   - 源文含「第N段」这类结构标签时，tencent/Hunyuan-MT-7B 会退化，整段只回一个 "and"；
//   - chat 路径此前把模型结果直接合入 allTr，applyOutputGates 只透出警告，
//     convergeEmptyResult 也只收敛「所有语种都为空」⇒ "and" 非空，照常交付并扣费；
//   - 文件链早有「可用性判据 + 极简指令兜底」的成熟范式（file.go 的 translateSegmentMinimal
//   - fileTranslationUsable），而 chat 路径没有接上这一层。
//
// 本文件把 chat 的可用性判据、修复腿、最终断言收在一处：
//
//	① chatUsabilityIssue：空串 / 同文（源目标语言不同时）/ IsTranslationUsable / 长文爆炸 / 指令回显；
//	② repairChatTranslations：review 之后、品牌归一化与质量闸门之前调用。不可用 → 极简指令兜底
//	   → 强制 fallback 模型兜底 → 清空并出声，绝不把 "and" 这类退化串交给客户；
//	③ finalizeChatTranslations：闸门/纯度之后再做一次「只判不改」的最终断言，
//	   防止闸门反馈重译或纯度清洗把译文弄坏后仍然交付；
//	④ forcedFallback 开关：给上述第 2 次兜底用，singleLangRaw 解析模型后据此切到
//	   cfg.HunyuanFallbackModel，避开正在退化的纯翻译模型。
//
// 设计约束（不要改回）：
//   - 不直接套 fileTranslationUsable：文件链的 IsTranslationUsable 允许「源文无任何非 ASCII 字母时
//     同文即可用」，这对 zh→en 没问题，但 en→zh 会把原样回吐英文判成可用；chat 已知 srcLang，
//     必须显式判「源/目标语言不同则同文不可用」（数字/符号/代码这类无需翻译的段除外）；
//   - 修复腿必须在闸门之前，救回的译文仍要过品牌归一化、术语/文化闸门、纯度清洗；
//   - 日志只出 lang/reason，不打印客户原文（与文件链 file.go 的日志口径一致）；
//   - 兜底有界：极简一次 → fallback 一次 → 清空，正常译文零额外调用。
//
// ============================================
package engine

import (
	"context"
	"fmt"
	"strings"

	"translator/internal/observability"
)

// chatUsabilityIssue 返回 chat 译文不可用的原因；空串表示可用。
//
// 判据顺序刻意从「最确定的结构问题」到「更宽的形态问题」：
//  1. 空串：没有内容可交付；
//  2. 同文：源/目标语言不同却逐字相同（en→zh 原样回吐英文是典型误判点）；
//     数字/符号/代码这类「本来不需要翻译」的段由 segmentNeedsTranslation 兜住，
//     原样交付才是正确形态，不判不可用；源/目标语言相同则原样就是正确交付；
//  3. IsTranslationUsable：覆盖 and/回绝/指令回显/短格爆炸/同文（非 ASCII 源）等既有九类判据；
//  4. hasLongTextExpansion：长源文的长度爆炸兜底（语言无关）；
//  5. isProseInstructionEcho：与文件链同一把「指令正文回显」尺子。
//     这一条在 chat 上误杀风险高于文件段，必须由 chat_usability_test.go 的正常样本锁住；
//     若将来发现误杀，优先收紧判据而不是直接摘掉（摘掉会让整句提示词回显重新进交付）。
func (e *Engine) chatUsabilityIssue(ctx context.Context, src, srcLang, targetLang, out string) string {
	if strings.TrimSpace(out) == "" {
		return "empty"
	}
	if srcLang != "" && targetLang != "" && normalizeForCompare(src) == normalizeForCompare(out) {
		if srcLang == targetLang {
			return "" // 同语种原样返回是正确交付形态
		}
		if !segmentNeedsTranslation(src) {
			return "" // 纯数字/符号/代码段：原样交付即可，不强求模型改字
		}
		return "identity" // 源/目标语言不同却原样回吐：不是翻译
	}
	if !IsTranslationUsable(src, out) {
		return "unusable"
	}
	if hasLongTextExpansion(src, out) {
		return "expansion"
	}
	if isProseInstructionEcho(src, out, targetLang, uiLangFromCtx(ctx)) {
		return "instruction_echo"
	}
	return ""
}

// repairChatTranslations 在 review 之后、品牌归一化/质量闸门之前调用。
//
// 修复链（有界）：
//  1. 极简指令（复用文件链 translateSegmentMinimal：去掉币种/纯度中文尾注，
//     只留指令骨架+输出契约；2026-10-19 工单 107 实测 12/12 正确）；
//  2. 强制 fallback 模型再试一次（仅当配置了 cfg.HunyuanFallbackModel）；
//  3. 仍不可用：清空该语种并写入 GateWarnings，绝不交付退化串。
//
// 参数 warnings 指向调用方稍后会并入 Data.GateWarnings 的切片；
// 这里只追加「哪个语种、什么原因」，不打印客户原文。
func (e *Engine) repairChatTranslations(ctx context.Context, src, srcLang string, allTr map[string]string, warnings *[]string) {
	for lc, tr := range allTr {
		reason := e.chatUsabilityIssue(ctx, src, srcLang, lc, tr)
		if reason == "" {
			continue
		}
		// 1) 极简指令兜底：与文件链同一个实现，不在这里重复拼 TranslateOne 参数。
		if v, ok := e.translateSegmentMinimal(ctx, src, lc); ok {
			if e.chatUsabilityIssue(ctx, src, srcLang, lc, v) == "" {
				allTr[lc] = v
				observability.Warn(ctx, "即时翻译首翻不可用，极简指令兜底成功", "lang", lc, "reason", reason)
				continue
			}
		}
		// 2) 强制 fallback 模型：避开正在退化的纯翻译模型；未配置则跳过。
		if e.Cfg != nil && strings.TrimSpace(e.Cfg.HunyuanFallbackModel) != "" {
			if v, ok := e.translateSegmentMinimal(withForcedFallbackModel(ctx), src, lc); ok {
				if e.chatUsabilityIssue(ctx, src, srcLang, lc, v) == "" {
					allTr[lc] = v
					observability.Warn(ctx, "即时翻译极简腿仍不可用，fallback 模型兜底成功", "lang", lc, "reason", reason)
					continue
				}
			}
		}
		// 3) 仍不可用：清空并出声。清空后若所有语种都为空，HandleText 的
		//    convergeEmptyResult 会把结果收敛成 upstream_failed，客户看到的是诚实失败。
		allTr[lc] = ""
		if warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("%s 译文不可用，已丢弃（%s），请稍后重试或改用翻译工单", langLabel(lc), reason))
		}
		observability.Warn(ctx, "即时翻译译文不可用，已丢弃", "lang", lc, "reason", reason)
	}
}

// finalizeChatTranslations 在品牌归一化、质量闸门、纯度清洗全部完成之后调用。
//
// 这一层只判不改、不再调模型：闸门里的反馈重译/术语强制替换仍可能产出不可用串
// （例如把源文回显回来），最后一道断言负责把它们清掉并出声。
// 单语种被清空后由 convergeEmptyResult 收敛成失败；多语种只丢一个时，原因进 GateWarnings。
func (e *Engine) finalizeChatTranslations(ctx context.Context, src, srcLang string, allTr map[string]string, warnings *[]string) {
	for lc, tr := range allTr {
		if strings.TrimSpace(tr) == "" {
			continue
		}
		reason := e.chatUsabilityIssue(ctx, src, srcLang, lc, tr)
		if reason == "" {
			continue
		}
		allTr[lc] = ""
		if warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("%s 译文不可用，已丢弃（%s）", langLabel(lc), reason))
		}
		observability.Warn(ctx, "即时翻译最终断言不可用，已丢弃", "lang", lc, "reason", reason)
	}
}

// forcedFallbackKey 质量兜底专用的 context 键（true=这一趟强制走 fallback 模型）。
type forcedFallbackKey struct{}

// withForcedFallbackModel 标记「本趟翻译强制使用 cfg.HunyuanFallbackModel」。
// 只给 repairChatTranslations 的第二档用；正常翻译/失败降级链不受影响。
func withForcedFallbackModel(ctx context.Context) context.Context {
	return context.WithValue(ctx, forcedFallbackKey{}, true)
}

// forcedFallbackFromCtx 读取强制 fallback 开关；未设置返回 false（正常档）。
func forcedFallbackFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(forcedFallbackKey{}).(bool)
	return v
}
