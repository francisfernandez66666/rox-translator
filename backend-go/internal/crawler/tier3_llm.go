// ============ tier3_llm.go · 职责说明 ============
// crawler 包 tier-3 LLM 批量生成适配器（可信度最低，超管审批把关）。
// 按 行业×语言 提示词批量生成：语言文化安全句（forbidden/替换/风格规范）
// 与行业术语对（zh→目标语言）。
// 游标 = 批次序号（每批 N 组，跨批去重由 store hash 兜底）。
// =============================================
package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"translator/internal/config"
	"translator/internal/store"
)

// llmProducer tier-3 LLM 生成器。
type llmProducer struct {
	st  *store.Store
	src *store.KBScrapeSource
	llm LLMCaller
}

// llmBatchSize 每批生成的条目组数。
const llmBatchSize = 10

// llmGenOutput LLM 输出的结构化结果（要求模型严格按此 JSON 数组返回）。
type llmGenOutput struct {
	Entries []llmEntry `json:"entries"` // 行业/语言文化条目
	Phrases []llmPhrase `json:"phrases"` // 语言文化安全句（locale 包）
}

// llmEntry 单条行业术语候选。
type llmEntry struct {
	SrcText string `json:"src"`
	TgtText string `json:"tgt"`
}

// llmPhrase 单条语言文化安全句候选。
type llmPhrase struct {
	Kind        string `json:"kind"` // style / forbidden / replace
	Phrase      string `json:"phrase"`
	Replacement string `json:"replacement,omitempty"`
}

// Next 生成下一批。每批用一次 LLM 调用产出 llmBatchSize 组候选。
func (p *llmProducer) Next(ctx context.Context, deps *SourceDeps, cursor string, offset int) (
	[]*store.KBStagedEntry, []*store.KBStagedPhrase, string, bool, error) {
	batch := 0
	if cursor != "" {
		if n, e := atoiSafe(cursor); e == nil {
			batch = n
		}
	}
	// 批次上限护栏：单源每轮最多生成 20 批（避免成本失控）
	const maxBatches = 20
	if batch >= maxBatches {
		return nil, nil, fmt.Sprintf("%d", batch), true, nil
	}
	out, err := p.generateBatch(ctx, deps, batch)
	if err != nil {
		return nil, nil, cursor, false, err
	}
	// 转换 LLM 原始输出为 store 待审条目/安全句（过滤空值，填充包 ID/语言/来源）
	tgtLang := p.src.Lang
	if tgtLang == "" {
		tgtLang = "en"
	}
	entries := make([]*store.KBStagedEntry, 0, len(out.Entries))
	for _, e := range out.Entries {
		if e.SrcText == "" || e.TgtText == "" {
			continue
		}
		entries = append(entries, &store.KBStagedEntry{
			TargetPackID: deps.TargetPackID,
			PackType:     p.src.PackType,
			Tier:         3,
			Layer:        1,
			SrcLang:      "zh",
			SrcText:      e.SrcText,
			TgtLang:      tgtLang,
			TgtText:      e.TgtText,
			SourceURL:    "llm_gen:" + p.src.Name,
		})
	}
	phrases := make([]*store.KBStagedPhrase, 0, len(out.Phrases))
	for _, ph := range out.Phrases {
		if ph.Phrase == "" {
			continue
		}
		kind := ph.Kind
		if kind != "style" && kind != "forbidden" && kind != "replace" {
			kind = "style"
		}
		phrases = append(phrases, &store.KBStagedPhrase{
			PackageID:   deps.TargetPackID,
			Lang:        tgtLang,
			Phrase:      ph.Phrase,
			Kind:        kind,
			Replacement: ph.Replacement,
			Tier:        3,
		})
	}
	next := fmt.Sprintf("%d", batch+1)
	done := batch+1 >= maxBatches
	return entries, phrases, next, done, nil
}

// generateBatch 调用 LLM 生成一批候选。
func (p *llmProducer) generateBatch(ctx context.Context, deps *SourceDeps, batch int) (*llmGenOutput, error) {
	prompt := p.buildPrompt(deps, batch)
	messages := []map[string]string{{"role": "user", "content": prompt}}
	base, key, model := p.llmRoute()
	content, _, err := p.llm.CallChat(ctx, base, key, model, messages, 4096, false, 0.7)
	if err != nil {
		return nil, err
	}
	var out llmGenOutput
	if err := json.Unmarshal([]byte(extractJSON(content)), &out); err != nil {
		return nil, fmt.Errorf("LLM 输出解析失败: %v", err)
	}
	// 规范化：填充目标包/语言/来源
	now := time.Now().Format(time.RFC3339)
	_ = now
	for _, e := range out.Entries {
		e.SrcText = strings.TrimSpace(e.SrcText)
		e.TgtText = strings.TrimSpace(e.TgtText)
	}
	return &out, nil
}

// llmRoute 选择 LLM 路由：优先 stage_models.ai_initial，否则全局默认。
func (p *llmProducer) llmRoute() (base, key, model string) {
	cfg := config.C
	if cfg != nil && cfg.OnlineAPIBase != "" && cfg.OnlineModel != "" {
		return cfg.OnlineAPIBase, cfg.OnlineAPIKey, cfg.OnlineModel
	}
	return "", "", ""
}

// buildPrompt 构造 LLM 生成提示词（按源类型区分：locale=安全句，industry=术语）。
func (p *llmProducer) buildPrompt(deps *SourceDeps, batch int) string {
	tgtLang := p.src.Lang
	if tgtLang == "" {
		tgtLang = "en"
	}
	langName := config.LangNames[tgtLang]
	if langName == "" {
		langName = tgtLang
	}
	var sb strings.Builder
	if p.src.PackType == "locale" {
		sb.WriteString(fmt.Sprintf(
			"你是跨文化语言专家。请针对目标语言「%s」（%s）输出第 %d 批「语言文化习惯与避雷规范」。\n"+
				"严格只输出 JSON，格式：{\"phrases\":[{\"kind\":\"forbidden|replace|style\",\"phrase\":\"...\",\"replacement\":\"...\"}]}\n"+
				"内容要求（每条真实、通用、可直接用于翻译质检）：\n"+
				"1. forbidden：该语言中政治/文化/种族/辱骂/不雅/色情/敏感等应避免出现的词或表达\n"+
				"2. replace：常见不恰当用词 → 应替换为的地道用词\n"+
				"3. style：该语言写作习惯规范（格式、语气、度量衡、礼貌用语）\n"+
				"每批生成 %d 条，各 kind 都要有，共输出一次 JSON，不要任何多余文字。",
			langName, tgtLang, batch+1, llmBatchSize))
		return sb.String()
	}
	// industry 包
	industry := p.src.Industry
	if industry == "" {
		industry = "通用行业"
	}
	sb.WriteString(fmt.Sprintf(
		"你是资深行业翻译专家。请针对行业「%s」与目标语言「%s」（%s）输出第 %d 批「行业常用术语 zh→%s 对照」。\n"+
			"严格只输出 JSON，格式：{\"entries\":[{\"src\":\"中文\",\"tgt\":\"%s\"}]}\n"+
			"每批 %d 条术语（高频、地道、避免直译腔），共输出一次 JSON，不要任何多余文字。",
		industry, langName, tgtLang, batch+1, tgtLang, langName, llmBatchSize))
	return sb.String()
}

// extractJSON 从 LLM 输出中提取 JSON 对象/数组（容忍 Markdown 代码块包裹）。
// extractJSON 从模型输出中稳健提取最外层 JSON 对象字符串。
// 特性：①字符串感知的括号平衡（引号内 { } 不计深度，正确处理 \" 转义）②遍历所有
// 候选起点，返回第一个能通过 json.Valid 校验的片段（规避模型输出前后缀/截断导致的错位）。
func extractJSON(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "\ufeff")) // 去 BOM
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		depth := 0
		inStr := false
		esc := false
		j := i
		for ; j < len(s); j++ {
			ch := s[j]
			if inStr {
				if esc {
					esc = false
					continue
				}
				if ch == '\\' {
					esc = true
					continue
				}
				if ch == '"' {
					inStr = false
				}
				continue
			}
			switch ch {
			case '"':
				inStr = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					cand := s[i : j+1]
					if json.Valid([]byte(cand)) {
						return cand
					}
					break // 当前候选非法，尝试下一个 '{'
				}
			}
		}
	}
	return s
}
