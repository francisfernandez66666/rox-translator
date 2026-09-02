// ============ kb_screen.go · 职责说明 ============
// engine 包：企业包上传「双轨行业化筛选」（功能①）。
// 租户上传到自有企业包仍立即生效（自服务），同时用 LLM 逐条分析
// 判定哪些条目是「可行业化」的通用术语（行业关键词作为提示词上下文），
// 命中者进共享行业包待审池（超管审批 + 通过后热加载与奖励）。
// 设计约束：整个链路失败必须静默降级（返回空筛选），不得阻塞/影响正常导入。
// =============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ScreenCandidate 待行业化筛选的上传条目。
type ScreenCandidate struct {
	SrcText string // 源文本（中文）
	TgtText string // 目标译文
	TgtLang string // 目标语言
}

// ScreenIndustryEntries 判定一条上传条目是否为「行业通用、可行业化共享」内容。
// 参数 industryName: 行业中文名（用于提示词）；seeds: 行业关键词种子（提示词上下文，
//
//	用户口径：关键词匹配做提示词——让模型对照行业种子判断通用性）；
//	entries: 待判条目列表。
//
// 返回与 entries 等长的布尔切片（true=可行业化）；LLM 不可用/调用失败时返回 nil
//
//	（上层按「无可行业化候选」静默处理，不阻塞导入）。
func (e *Engine) ScreenIndustryEntries(ctx context.Context, industryName string, seeds []string, entries []ScreenCandidate) []bool {
	if e == nil || e.LLM == nil || len(entries) == 0 {
		return nil
	}
	if industryName == "" {
		industryName = "通用行业"
	}
	if len(seeds) > 8 {
		seeds = seeds[:8] // 提示词上下文收敛，避免超长
	}
	seedHint := "（参考关键词：" + strings.Join(seeds, "、") + "）"
	out := make([]bool, len(entries))
	// 分批调用（单批上限 50 条，防止单次提示超长）
	const batch = 50
	for start := 0; start < len(entries); start += batch {
		end := start + batch
		if end > len(entries) {
			end = len(entries)
		}
		part := entries[start:end]
		res, ok := e.screenEntryBatch(ctx, industryName, seedHint, part)
		if !ok {
			return nil // 任一失败即整批放弃（默认不误伤任何条目为可行业化）
		}
		for i, v := range res {
			out[start+i] = v
		}
	}
	return out
}

// screenEntryBatch 单批 LLM 筛选：输出 JSON 布尔数组。
// 返回给定批次的判定结果与成功标志。
func (e *Engine) screenEntryBatch(ctx context.Context, industryName, seedHint string, entries []ScreenCandidate) ([]bool, bool) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "你是知识库运营。下方是上传到某企业客户的知识库条目。请判断每条是否为「%s」行业的通用术语/常见商务用法，可共享给该行业所有租户复用（不含企业专有信息、人名、内部代号）。%s\n\n逐条输出 JSON 布尔数组，如 {\"suitable\":[true,false,...]}，条数与输入一致，不要多余文字：\n", industryName, seedHint)
	for i, ev := range entries {
		fmt.Fprintf(&sb, "%d. %s → [%s] %s\n", i+1, ev.SrcText, ev.TgtLang, ev.TgtText)
	}
	messages := []map[string]string{{"role": "user", "content": sb.String()}}
	base, key, model := e.resolveModel(ctx)
	if b2, k2, m2, ok := e.resolveStageModel(ctx, "kb_screen"); ok {
		base, key, model = b2, k2, m2
	}
	content, _, err := e.LLM.CallChat(ctx, base, key, model, messages, 600, false, 0.0)
	if err != nil {
		return nil, false
	}
	content = strings.TrimSpace(content)
	if len(content) > 3 && content[0] != '{' {
		if i := strings.Index(content, "{"); i >= 0 {
			content = content[i:]
		}
	}
	var parsed struct {
		Suitable []bool `json:"suitable"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil || len(parsed.Suitable) != len(entries) {
		// 解析兜底：尝试仅截取首对象
		if i := strings.LastIndex(content, "}"); i > 0 {
			if err2 := json.Unmarshal([]byte(content[:i+1]), &parsed); err2 == nil && len(parsed.Suitable) == len(entries) {
				return parsed.Suitable, true
			}
		}
		return nil, false
	}
	return parsed.Suitable, true
}
