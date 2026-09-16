// ============ placeholder.go · 职责说明 ============
// ★ D19（2026-09-12）：占位符「掩码-回填」硬保护。
// 现状缺口：占位符（{var}/%s/<tag> 等，与 qa.phRe 同源）仅靠 prompt 约束 + QA 事后
// 告警，模型破坏（丢一个 {name}）无法自动修复。
// 闭环：翻译前掩码为 ⟦Pn⟧ → 翻后回填；发现令牌丢失自动重翻一次（带更强约束注记）；
// 仍缺失时兜底把遗漏令牌追加句尾（宁多勿丢，QA placeholder 规则自此可过）。
// =============================================
package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"translator/internal/kb"
	"translator/internal/qa"
)

// phTokenRe 保护令牌匹配（宽松容错：模型可能把 ⟦ ⟧ 换成 [ ] 【 】 或加空格）。
var phTokenRe = regexp.MustCompile(`[⟦\[【]\s*P(\d+)\s*[⟧\]】]`)

// phGuardKey ctx 标记：本次调用启用了占位符掩码（singleLangRaw 据此注入约束注记）。
type phGuardKeyT struct{}

// phGuardKey / streamSinkKey / streamSinkInnerKey 各 ctx 键的单例实例（空结构体零开销）。
var phGuardKey phGuardKeyT

// withPHGuard 在 ctx 标记占位符保护已开启（防止逐层重复包装）。
func withPHGuard(ctx context.Context) context.Context {
	return context.WithValue(ctx, phGuardKey, true)
}

// phGuardFromCtx 读取 ctx 中的占位符保护开关。
func phGuardFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(phGuardKey).(bool)
	return v
}

// maskPlaceholders 把源文中的占位符替换为 ⟦Pn⟧ 保护令牌（保持出现顺序与去重下标）。
// 返回: 掩码文本 + 令牌原词表（下标即编号）。无占位符时原样返回（toks=nil）。
func maskPlaceholders(text string) (string, []string) {
	idx := map[string]int{}
	var toks []string
	out := qa.PlaceholderRe.ReplaceAllStringFunc(text, func(ph string) string {
		if id, ok := idx[ph]; ok {
			return fmt.Sprintf("⟦P%d⟧", id)
		}
		idx[ph] = len(toks)
		toks = append(toks, ph)
		return fmt.Sprintf("⟦P%d⟧", len(toks)-1)
	})
	if len(toks) == 0 {
		return text, nil
	}
	return out, toks
}

// unmaskPlaceholders 回填保护令牌。
// 返回: 回填文本 + 丢失的令牌下标（模型删改令牌所致，按原序）。
func unmaskPlaceholders(out string, toks []string) (string, []int) {
	seen := map[int]bool{}
	s := phTokenRe.ReplaceAllStringFunc(out, func(m string) string {
		sub := phTokenRe.FindStringSubmatch(m)
		var n int
		fmt.Sscanf(sub[1], "%d", &n)
		if n >= 0 && n < len(toks) {
			seen[n] = true
			return toks[n]
		}
		return m // 越界令牌原样保留（不属本次掩码，不动）
	})
	var missing []int
	for i := range toks {
		if !seen[i] {
			missing = append(missing, i)
		}
	}
	return s, missing
}

// ★ D20：流式增量 sink（lang 标签版，api 层注入 ctx；singleLangRaw 调用前按目标语言套壳）
type streamSinkKeyT struct{}

// streamSinkKey ctx 键单例：外层双参增量 sink（lang 标签版）。
var streamSinkKey streamSinkKeyT

// WithStreamSink 注入 token 级增量回调（func(targetLang, delta)）。
func WithStreamSink(ctx context.Context, sink func(string, string)) context.Context {
	return context.WithValue(ctx, streamSinkKey, sink)
}

// streamSinkFromCtx 取出外层流式回调（参数为增量文本与目标语言）。
func streamSinkFromCtx(ctx context.Context) func(string, string) {
	v, _ := ctx.Value(streamSinkKey).(func(string, string))
	return v
}

// 内层单参 sink（按目标语言套壳后供 tryMainModel 消费；与外层不同键防类型混叠）
type streamSinkInnerKeyT struct{}

// streamSinkInnerKey ctx 键单例：内层单参增量 sink。
var streamSinkInnerKey streamSinkInnerKeyT

// withStreamSinkInner 挂内层单参流式回调（已按目标语言套壳）。
func withStreamSinkInner(ctx context.Context, sink func(string)) context.Context {
	return context.WithValue(ctx, streamSinkInnerKey, sink)
}

// streamSinkInnerFromCtx 取出内层单参流式回调。
func streamSinkInnerFromCtx(ctx context.Context) func(string) {
	v, _ := ctx.Value(streamSinkInnerKey).(func(string))
	return v
}

// singleLang 占位符保护包装（★ D19）：mask → singleLangRaw → unmask；
// 令牌丢失自动重翻一次，仍丢失则遗漏令牌兜底追加句尾。
func (e *Engine) singleLang(ctx context.Context, zhText, targetLang string, examples []*kb.Row, sourceLang, stage string, attempt int) (string, error) {
	// ★ D22：超长对话文本先做句级切分（CJK 感知），逐段翻译后按原序拼接；
	//   顶层 attempt>0（来自 singleLangRaw 截断重试等）不再拆分，防递归嵌套。
	if attempt == 0 {
		if segs := splitForChatTranslate(zhText); len(segs) > 1 {
			var joined strings.Builder
			for _, seg := range segs {
				one, err := e.singleLangProtected(ctx, seg, targetLang, examples, sourceLang, stage, 0)
				if err != nil {
					return "", err
				}
				joined.WriteString(one)
			}
			return joined.String(), nil
		}
	}
	return e.singleLangProtected(ctx, zhText, targetLang, examples, sourceLang, stage, attempt)
}

// singleLangProtected D19 掩码-回填闭环（可重翻一次 + 遗漏兜底追加）。
func (e *Engine) singleLangProtected(ctx context.Context, zhText, targetLang string, examples []*kb.Row, sourceLang, stage string, attempt int) (string, error) {
	masked, toks := maskPlaceholders(zhText)
	if toks == nil {
		return e.singleLangRaw(ctx, zhText, targetLang, examples, sourceLang, stage, attempt)
	}
	out, err := e.singleLangRaw(withPHGuard(ctx), masked, targetLang, examples, sourceLang, stage, attempt)
	if err != nil {
		return "", err
	}
	final, missing := unmaskPlaceholders(out, toks)
	if len(missing) > 0 && attempt == 0 {
		// 违规自动重翻（一次为限，防循环）
		out2, err2 := e.singleLangRaw(withPHGuard(ctx), masked, targetLang, examples, sourceLang, stage, attempt+1)
		if err2 == nil {
			if f2, m2 := unmaskPlaceholders(out2, toks); len(m2) < len(missing) {
				final, missing = f2, m2
			}
		}
	}
	if len(missing) > 0 {
		// 兜底：宁多勿丢——遗漏令牌追加句尾（保证 QA 占位符一致性可过、原文信息不蒸发）
		var sb strings.Builder
		sb.WriteString(final)
		for _, i := range missing {
			sb.WriteString(" " + toks[i])
		}
		final = sb.String()
	}
	return final, nil
}
