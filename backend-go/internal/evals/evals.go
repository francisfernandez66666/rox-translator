// Package evals 提供 LLM-as-Judge 5 维评估器。
// ① 术语准确性 30% ② 语法正确性 20% ③ 语义保真度 30% ④ 数字/单位保持 10% ⑤ 风格与长度 10%
package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"translator/internal/config"
	"translator/internal/llm"
	"translator/internal/store"
)

// Evaluator 评估器
type Evaluator struct {
	LLM   *llm.Client
	Cfg   *config.Config
	Store *store.Store

	// 抽样率：0.1=10% / 0.5=50% / 1=100%（默认 1）
	SampleRate float64
	// 是否启用（无 Judge Key 时自动跳过）
	Enabled bool
}

// New 创建评估器。无 Judge Key → Enabled=false（记录 skipped 并跳过）
func New(cfg *config.Config, client *llm.Client, st *store.Store, judgeKey string) *Evaluator {
	e := &Evaluator{LLM: client, Cfg: cfg, Store: st, SampleRate: 1.0, Enabled: true}
	if judgeKey == "" {
		e.Enabled = false
	}
	return e
}

// ShouldSample 是否采样本次（抽样率控制，销售场景成本敏感）
func (e *Evaluator) ShouldSample() bool {
	if !e.Enabled {
		return false
	}
	return e.SampleRate >= 1.0
}

// Evaluate LLM-as-Judge 5 维评分；返回总分（0-100）与各维分数
func (e *Evaluator) Evaluate(ctx context.Context, source, translation, targetLang, taskType string) (float64, map[string]float64, error) {
	if !e.Enabled {
		return 0, nil, fmt.Errorf("evals 未启用（无 Judge Key）")
	}
	prompt := fmt.Sprintf(`你是翻译质量评估员。请对下面译文按 5 维评分（每维 0-100 分）：

【原文】(中文)
%s

【译文】(%s)
%s

评分维度与权重：
① 术语准确性 (30%%): 专业术语是否准确、统一
② 语法正确性 (20%%): 语法、拼写是否正确
③ 语义保真度 (30%%): 是否完整保留原意、无漏译错译
④ 数字/单位保持 (10%%): 数字、单位、符号是否与原文一致
⑤ 风格与长度 (10%%): 语气风格与原文匹配，长度合理

只输出 JSON，格式: {"term":0,"grammar":0,"semantic":0,"numunit":0,"style":0,"total":0}`,
		source, targetLang, translation)

	messages := []map[string]string{{"role": "user", "content": prompt}}
	content, _, err := e.LLM.CallChat(ctx, e.Cfg.OnlineAPIBase, e.Cfg.OnlineAPIKey, e.Cfg.OnlineModel, messages, 200, false, 0.0)
	if err != nil {
		return 0, nil, err
	}
	return parseScores(content)
}

func parseScores(content string) (float64, map[string]float64, error) {
	// 提取 JSON（容忍模型加代码块/说明）
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return 0, nil, fmt.Errorf("Judge 输出无 JSON")
	}
	var m map[string]float64
	if err := json.Unmarshal([]byte(content[start:end+1]), &m); err != nil {
		return 0, nil, err
	}
	term := m["term"]
	grammar := m["grammar"]
	semantic := m["semantic"]
	numunit := m["numunit"]
	style := m["style"]
	total := 0.3*term + 0.2*grammar + 0.3*semantic + 0.1*numunit + 0.1*style
	if t, ok := m["total"]; ok && t > 0 {
		total = t
	}
	if total > 100 {
		total = 100
	}
	return total, m, nil
}

// SaveRecord 保存评估记录
func (e *Evaluator) SaveRecord(ctx context.Context, tid, userID, ticketID int64, taskType, model, input, output string, scores map[string]float64, total float64, status string) (int64, error) {
	if e.Store == nil {
		return 0, nil
	}
	sb, _ := json.Marshal(scores)
	return e.Store.SaveEvalRecord(&store.EvalRecord{
		TenantID: tid, UserID: userID, TicketID: ticketID, TaskType: taskType,
		Model: model, InputText: input, OutputText: output, Scores: string(sb), Total: total, Status: status,
	})
}