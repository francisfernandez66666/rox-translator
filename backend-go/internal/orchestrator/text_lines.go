// ============ 本文件职责中文说明 ============
// DEF-02（2026-10-24 云端 UAT）：文本工单逐行翻译与「只补缺失/错误行」。
//
// 现网形态：源文两段（第二行本身是「请翻译成英文并保留段落结构」这类元指令），
// 整篇一次调用时模型把它当指令吞掉，translations.en 只有第一段；qa 的 length 规则
// 因为译文总长不短而放行。本文件把文本工单改为逐行批量翻译：
//   - 源文按 \n 切成行（空行保留占位），非空行才送模型；
//   - 每行独立过 TextTranslationUsable（与文件链路同一把尺子，含指令回显）；
//   - 重发时先读回旧译文：正确行直接复用，只有缺失/错误行才重新调用模型。
//
// ============================================
package orchestrator

import (
	"context"
	"strings"

	"translator/internal/observability"
)

// textPerLineEnabled 文本工单是否启用逐行翻译（system_config 默认 1，0=回退整篇旧行为）。
func (w *Workflow) textPerLineEnabled() bool {
	if w == nil || w.Store == nil {
		return false
	}
	// 0＝回退整篇是方案里写明的应急开关，必须走 ConfigIntAllowZero：
	// ConfigInt 的「≤0 视为未配置」会把这一档读回默认 1，开关按不下去。
	return w.Store.ConfigIntAllowZero("text_ticket_per_line", 1) != 0
}

// composeLineOutput 把逐行译文拼回整篇（行数与源文一致；行内换行压成空格，防一段变多段）。
func composeLineOutput(lines []string) string {
	out := make([]string, len(lines))
	for i, v := range lines {
		out[i] = strings.ReplaceAll(strings.TrimSpace(v), "\n", " ")
	}
	return strings.Join(out, "\n")
}

// splitLines 按 \n 切行并保留空行占位（末尾空行也保留，保证行数与源文一致）。
func splitLines(s string) []string { return strings.Split(s, "\n") }

// nonEmptyLines 统计非空行数（与 qa.nonEmptyLineCount 同口径；这里不 import qa 只为不把
// 「是否多段」的判定与 QA 规则耦合，阈值只有「≥2」这一条）。
func nonEmptyLines(s string) int {
	n := 0
	for _, l := range splitLines(s) {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// translateTextPerLine 文本工单逐行翻译/增量补译。
// 返回 true 表示本函数已接管（调用方保存 payload 后返回）；false 表示回退旧整篇路径。
func (w *Workflow) translateTextPerLine(ctx context.Context, p *ticketPayload) bool {
	if w == nil || w.Engine == nil || p == nil || !w.textPerLineEnabled() {
		return false
	}
	srcLines := splitLines(p.SourceText)
	if nonEmptyLines(p.SourceText) < 2 {
		return false // 单段不折腾（与旧路径等价，少两次模型调用）
	}
	// 载荷里的 Translations 可能是 nil（FinalResult 是一段没带 translations 键的 JSON，
	// loadPayload 只保证反序列化成功）。与下面 Sources 的守卫同一形态——缺这一句会在写入点
	// panic「assignment to entry in nil map」，把整条流水线打挂。
	if p.Translations == nil {
		p.Translations = map[string]string{}
	}
	reused, retranslated := 0, 0
	for _, lc := range p.TargetLangs {
		existing := splitLines(p.Translations[lc])
		outLines := make([]string, len(srcLines))
		var needSrc []string
		var needIdx []int
		for i, src := range srcLines {
			if strings.TrimSpace(src) == "" {
				outLines[i] = ""
				continue
			}
			if i < len(existing) && strings.TrimSpace(existing[i]) != "" &&
				w.Engine.TextTranslationUsable(ctx, src, existing[i], lc) {
				outLines[i] = existing[i]
				reused++
				continue
			}
			needSrc = append(needSrc, src)
			needIdx = append(needIdx, i)
		}
		if len(needSrc) > 0 {
			batch := w.Engine.BatchTranslate(ctx, needSrc, lc, 15, nil)
			for j, idx := range needIdx {
				var out string
				if j < len(batch) {
					out = batch[j]
				}
				if !w.Engine.TextTranslationUsable(ctx, needSrc[j], out, lc) {
					// 批量失败/回显 → 极简指令逐行补；仍不可用则留空，由结构 QA 暴露
					if v, ok := w.Engine.TranslateMinimalLine(ctx, needSrc[j], lc); ok &&
						w.Engine.TextTranslationUsable(ctx, needSrc[j], v, lc) {
						out = v
					} else {
						out = ""
					}
				}
				outLines[idx] = out
				retranslated++
			}
		}
		p.Translations[lc] = composeLineOutput(outLines)
		if p.Sources == nil {
			p.Sources = map[string]string{}
		}
		p.Sources[lc] = "model"
	}
	observability.Info(ctx, "文本工单逐行翻译/增量补译完成",
		"reused_lines", reused, "retranslated_lines", retranslated, "langs", len(p.TargetLangs))
	return true
}

// reviewTextPerLine 文本工单逐行审校：保留行数，避免整篇审校把某一段合并/吞掉。
// 返回 true 表示已接管；false 走旧整篇 review。
func (w *Workflow) reviewTextPerLine(ctx context.Context, p *ticketPayload) bool {
	if w == nil || w.Engine == nil || p == nil || !w.textPerLineEnabled() {
		return false
	}
	if nonEmptyLines(p.SourceText) < 2 {
		return false
	}
	srcLines := splitLines(p.SourceText)
	for lc, tr := range p.Translations {
		trLines := splitLines(tr)
		if len(trLines) != len(srcLines) {
			continue // 行数不符：不冒险逐行审校，交给结构 QA/重发
		}
		var srcs, tgts []string
		var idxs []int
		for i := range srcLines {
			if strings.TrimSpace(srcLines[i]) == "" {
				continue
			}
			srcs = append(srcs, srcLines[i])
			tgts = append(tgts, trLines[i])
			idxs = append(idxs, i)
		}
		if len(srcs) == 0 {
			continue
		}
		rev := w.Engine.ReviewTranslationBatch(ctx, srcs, tgts, lc, "review")
		for j, i := range idxs {
			if j < len(rev) && strings.TrimSpace(rev[j]) != "" &&
				w.Engine.TextTranslationUsable(ctx, srcLines[i], rev[j], lc) {
				trLines[i] = strings.ReplaceAll(strings.TrimSpace(rev[j]), "\n", " ")
			}
		}
		p.Translations[lc] = composeLineOutput(trLines)
	}
	return true
}
