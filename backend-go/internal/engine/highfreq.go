// ============ highfreq.go · 高频词统一译法表（2026-11-20 P3） ============
//
// 用户三轮澄清的落点之一：从原文抽高频词 → 剔除 KB 已覆盖词（KB 强制不变）
// → 剩余非 KB 高频词送模型判定"是不是术语/专名 + 该语种统一译法"
// → 生成统一译法表，供复核提示词（软统一）与交付前一致性后检（硬后检）两处消费。
//
// ★ 最高边界（用户拍板）：不是"词频代替 KB"。KB 词一行不改、不进本表、不参与判定。
// ========================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"translator/internal/config"
	"translator/internal/gate"
	"translator/internal/observability"
)

// highfreqStopWords 中文虚词/高频非术语（不值得统一译法，统一反而添乱）。
var highfreqStopWords = map[string]bool{
	"我们": true, "你们": true, "他们": true, "这些": true, "那些": true,
	"公司": true, "产品": true, "服务": true, "系统": true, "平台": true,
	"可以": true, "应该": true, "需要": true, "因为": true, "所以": true,
	"但是": true, "而且": true, "以及": true, "或者": true, "一个": true,
	"没有": true, "已经": true, "正在": true, "通过": true, "使用": true,
	"进行": true, "实现": true, "提供": true, "包括": true, "相关": true,
	"如下": true, "以上": true, "以下": true, "其中": true, "对于": true,
}

// highfreqMinCount / highfreqMaxTerms 抽取门槛与上限（env 可配见 highfreqMaxTermsFromEnv）。
const (
	highfreqMinCount = 2  // 至少出现 2 次才算"高频"
	highfreqMinRunes = 2  // 中文词最短 2 字
	highfreqMaxRunes = 6  // 中文词最长 6 字
)

var latinWordReHF = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_-]{1,}`)

// highfreqCandidates 从源文片段集合抽高频词候选（纯逻辑，无模型调用）。
// 中文：2-6 字滑窗统计；拉丁：整词统计。过滤停用词/纯数字。返回按频次降序的候选。
func highfreqCandidates(sourceTexts []string) []string {
	freq := map[string]int{}
	for _, text := range sourceTexts {
		runes := []rune(text)
		// 拉丁整词
		for _, w := range latinWordReHF.FindAllString(text, -1) {
			lw := strings.ToLower(w)
			if len(lw) >= 2 {
				freq[lw]++
			}
		}
		// 中文滑窗 2-6 字
		for i := 0; i < len(runes); i++ {
			if !unicode.Is(unicode.Han, runes[i]) {
				continue
			}
			for n := highfreqMinRunes; n <= highfreqMaxRunes && i+n <= len(runes); n++ {
				seg := string(runes[i : i+n])
				allHan := true
				for _, r := range seg {
					if !unicode.Is(unicode.Han, r) {
						allHan = false
						break
					}
				}
				if allHan && !highfreqStopWords[seg] {
					freq[seg]++
				}
			}
		}
	}
	type kv struct {
		w string
		n int
	}
	var arr []kv
	for w, n := range freq {
		if n >= highfreqMinCount {
			arr = append(arr, kv{w, n})
		}
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].n != arr[j].n {
			return arr[i].n > arr[j].n
		}
		return len(arr[i].w) > len(arr[j].w) // 同频取长词（术语多为多字词）
	})
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		out = append(out, it.w)
	}
	return out
}

// HighfreqTermTable 高频词统一译法表（只含非 KB 词）。
// 与 KB 表物理隔离：本表每条 FromHighfreq=true，KB 硬闸会跳过它们。
type HighfreqTermTable struct {
	terms []gate.TermRequirement
}

// highfreqCtxKey 通过 ctx 把本批高频词表透传给复核链（避免给 ReviewTranslation/
// ReviewTranslationBatch 加参数波及 5+ 调用点）。每批翻译前注入一次，逐段复用。
type highfreqCtxKey struct{}

// withHighfreqTerms 把本批高频词表注入 ctx（参数 ctx=父上下文、t=统一译法表；返回注入后的新 ctx）。
// 每批翻译前调一次，逐段复核共享同一份表，避免给 5+ 个复核调用点加参数。
func withHighfreqTerms(ctx context.Context, t HighfreqTermTable) context.Context {
	return context.WithValue(ctx, highfreqCtxKey{}, t)
}

// highfreqTermsFromCtx 从 ctx 取本批高频词表；未注入/类型不符时返回空表（fail-open，不 panic）。
// 参数 ctx=翻译上下文；返回可安全调用的 HighfreqTermTable（其 PromptBlock/AsSnapshot 对空表友好）。
func highfreqTermsFromCtx(ctx context.Context) HighfreqTermTable {
	if t, ok := ctx.Value(highfreqCtxKey{}).(HighfreqTermTable); ok {
		return t
	}
	return HighfreqTermTable{}
}

// AsSnapshot 返回给一致性后检用的快照。
func (t HighfreqTermTable) AsSnapshot() []gate.TermRequirement { return t.terms }

// PromptBlock 返回注入复核提示词的文本段（"建议"措辞，区别于 KB 的"必须"）。
func (t HighfreqTermTable) PromptBlock() string {
	if len(t.terms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("以下高频词建议在本批中统一译法（保持前后一致）：\n")
	for _, tm := range t.terms {
		if len(tm.Aliases) > 0 {
			b.WriteString(fmt.Sprintf("- %s → %s（或 %s）\n", tm.Source, tm.Target, strings.Join(tm.Aliases, " / ")))
		} else {
			b.WriteString(fmt.Sprintf("- %s → %s\n", tm.Source, tm.Target))
		}
	}
	return strings.TrimSpace(b.String())
}

// BuildHighfreqTermTable 构建高频词统一译法表：
//  1. 抽候选 → 2. 剔除 KB 覆盖词（KB 强制不变）→ 3. 剩余送模型判定术语与译法 → 4. fail-open。
//
// 参数 sourceTexts 全篇源文片段；lang 目标语言；gc 提供 KB 查询（可为 nil，测试时不查 KB）。
// ctx 仅用于取租户与取消；模型调用走 e.LLM。
func (e *Engine) BuildHighfreqTermTable(ctx context.Context, sourceTexts []string, lang string, gc *gateCtx) HighfreqTermTable {
	candidates := highfreqCandidates(sourceTexts)
	if len(candidates) == 0 {
		return HighfreqTermTable{}
	}
	// ★ KB 剔除：KB 覆盖的 source 一律从候选里删掉（用户最高边界：KB 词强制不变）。
	kbCovered := map[string]bool{}
	if gc != nil && gc.e != nil && gc.e.St != nil && gc.tid > 0 {
		allow := gc.kbAllow(ctx)
		for _, cand := range candidates {
			ents, err := gc.e.St.FindTermsBySubstringAllowed("zh", cand, allow)
			if err == nil {
				for _, ent := range ents {
					if ent != nil && ent.Layer == 1 && ent.TargetLang == lang &&
						strings.Contains(cand, strings.TrimSpace(ent.SourceText)) {
						kbCovered[cand] = true
					}
				}
			}
		}
	}
	nonKB := make([]string, 0, len(candidates))
	for _, cand := range candidates {
		if !kbCovered[cand] {
			nonKB = append(nonKB, cand)
		}
	}
	if len(nonKB) == 0 {
		return HighfreqTermTable{}
	}
	// ★ 模型判定：哪些是术语/专名 + 该语种统一译法。fail-open：任何失败返回空表。
	table := e.judgeHighfreqTerms(ctx, nonKB, lang)
	return table
}

// judgeHighfreqTerms 用模型判定高频候选词的统一译法（纯输出解析 + 一次模型调用）。
func (e *Engine) judgeHighfreqTerms(ctx context.Context, candidates []string, lang string) HighfreqTermTable {
	langName := config.LangNames[lang]
	if langName == "" {
		langName = lang
	}
	var sb strings.Builder
	for _, c := range candidates {
		sb.WriteString("- ")
		sb.WriteString(c)
		sb.WriteString("\n")
	}
	prompt := fmt.Sprintf("以下是从一篇中文文档里统计出的高频词。请判断哪些是专业术语或专有名词（需要在%s译文中保持译法一致），并为每个给出统一的%s译法。普通词汇、虚词、泛用词不要列入。\n只输出 JSON 数组，格式：[{\"source\":\"词\",\"target\":\"统一译法\"}]，不要输出其他文字。\n\n高频词：\n%s",
		langName, langName, sb.String())
	messages := []map[string]string{{"role": "user", "content": prompt}}
	base, key, model := e.resolveModel(ctx)
	if b2, k2, m2, ok := e.resolveStageModel(ctx, config.StageReview); ok {
		base, key, model = b2, k2, m2 // 复用复核模型做语义判定
	}
	content, _, err := e.LLM.CallChat(ctx, base, key, model, messages, 2048, false, e.Cfg.FallbackTemp)
	if err != nil || strings.TrimSpace(content) == "" {
		observability.Warn(ctx, "高频词判定模型调用失败，跳过高频词统一", "lang", lang, "err", fmt.Sprint(err))
		return HighfreqTermTable{}
	}
	// 解析 JSON（容忍模型包在 ```json 里）
	clean := stripCodeFence(content)
	var parsed []struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	if jerr := json.Unmarshal([]byte(clean), &parsed); jerr != nil {
		observability.Warn(ctx, "高频词判定输出解析失败，跳过高频词统一", "lang", lang, "err", jerr.Error())
		return HighfreqTermTable{}
	}
	// 只接受候选池里的词（防模型编造），标 FromHighfreq=true。
	candSet := map[string]bool{}
	for _, c := range candidates {
		candSet[c] = true
	}
	var terms []gate.TermRequirement
	for _, p := range parsed {
		s, t := strings.TrimSpace(p.Source), strings.TrimSpace(p.Target)
		if s != "" && t != "" && candSet[s] {
			terms = append(terms, gate.TermRequirement{Source: s, Target: t, FromHighfreq: true})
		}
	}
	return HighfreqTermTable{terms: terms}
}

// stripCodeFence 去掉模型输出外层的 ```json ... ``` 包裹。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}
