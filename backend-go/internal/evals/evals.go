// ============ 本文件职责中文说明 ============
// LLM-as-Judge 5 维翻译质量评估器：
// ① 术语准确性 30% ② 语法正确性 20% ③ 语义保真度 30% ④ 数字/单位保持 10% ⑤ 风格与长度 10%。
// 调用 Judge 模型返回 JSON 五维分数并加权计算总分；支持抽样率控制（成本敏感场景）、
// 无 Judge Key 自动降级跳过、评估记录落库。
// =============================================

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
	LLM   *llm.Client    // LLM 客户端（调用 Judge 模型）
	Cfg   *config.Config // 全局配置（Judge 模型/地址/密钥来源）
	Store *store.Store   // 平台存储（保存评估记录）

	// 抽样率：0.1=10% / 0.5=50% / 1=100%（默认 1）
	SampleRate float64
	// 是否启用（无 Judge Key 时自动跳过）
	Enabled bool
}

// New 创建评估器。无 Judge Key → Enabled=false（记录 skipped 并跳过）。
// 参数：cfg=全局配置，client=LLM 客户端，st=平台存储，judgeKey=Judge 模型 API Key。
// 返回：评估器实例。
func New(cfg *config.Config, client *llm.Client, st *store.Store, judgeKey string) *Evaluator {
	e := &Evaluator{LLM: client, Cfg: cfg, Store: st, SampleRate: 1.0, Enabled: true}
	if judgeKey == "" {
		e.Enabled = false // 无 Judge Key：禁用评估，避免无意义的 API 调用
	}
	return e
}

// ShouldSample 是否采样本次（抽样率控制，销售场景成本敏感）。
// 返回：是否需要执行评估（禁用时恒为 false）。
func (e *Evaluator) ShouldSample() bool {
	if !e.Enabled {
		return false
	}
	return e.SampleRate >= 1.0 // 当前实现：仅 100% 抽样才评估
}

// judgeKeyUsable 判断解析出的 Judge Key 是否可用（非空、非掩码、非启动占位符）。
func (e *Evaluator) judgeKeyUsable(key string) bool {
	if key == "" || strings.Contains(key, "****") {
		return false
	}
	// 启动生成的随机占位符无法调用外部 API，视为不可用
	if e.Cfg != nil && e.Cfg.OnlineAPIKeyIsPlaceholder && key == e.Cfg.OnlineAPIKey {
		return false
	}
	return true
}

// resolveJudge 解析 Judge 模型的 base/key/model。
// 优先级：stage_models[初翻评估/校对评估] → stage_models.evals（旧键兼容）→ ModelRoutes → Online*。
// 参数 taskType: "translate"=初翻评估（initial_evals），"review"=校对评估（review_evals）。
func (e *Evaluator) resolveJudge(taskType string) (base, key, model string) {
	stage := config.StageInitialEvals
	if taskType == "review" {
		stage = config.StageReviewEvals
	}
	if e.Store != nil {
		if raw, err := e.Store.GetConfig("stage_models"); err == nil && raw != "" {
			var m config.StageModels
			if json.Unmarshal([]byte(raw), &m) == nil {
				if sm, ok := m[stage]; ok && sm.APIBase != "" && sm.Model != "" {
					if sm.APIKey != "" {
						return sm.APIBase, sm.APIKey, sm.Model
					}
					return sm.APIBase, e.Cfg.OnlineAPIKey, sm.Model
				}
				// 旧键兜底：未区分初翻/校对评估时共用 evals 阶段配置
				if sm, ok := m[config.StageEvals]; ok && sm.APIBase != "" && sm.Model != "" {
					if sm.APIKey != "" {
						return sm.APIBase, sm.APIKey, sm.Model
					}
					return sm.APIBase, e.Cfg.OnlineAPIKey, sm.Model
				}
			}
		}
	}
	if len(e.Cfg.ModelRoutes) > 0 {
		best := e.Cfg.ModelRoutes[0]
		for _, r := range e.Cfg.ModelRoutes[1:] {
			if r.Weight > best.Weight {
				best = r
			}
		}
		if best.APIBase != "" && best.Model != "" {
			return best.APIBase, best.APIKey, best.Model
		}
	}
	return e.Cfg.OnlineAPIBase, e.Cfg.OnlineAPIKey, e.Cfg.OnlineModel
}

// Evaluate LLM-as-Judge 5 维评分；返回总分（0-100）与各维分数。
// 参数：source=原文，translation=译文，targetLang=目标语言，taskType=任务类型。
// 返回：加权总分、各维分数 map（term/grammar/semantic/numunit/style）与错误。
func (e *Evaluator) Evaluate(ctx context.Context, source, translation, targetLang, taskType string) (float64, map[string]float64, error) {
	if !e.Enabled {
		return 0, nil, fmt.Errorf("evals 未启用（无 Judge Key）")
	}
	// 构造评估提示词：请求 Judge 按 5 维打分并只输出 JSON
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

	base, key, model := e.resolveJudge(taskType)
	// 动态可用性判定：占位/掩码 Key 直接跳过，避免必失败的空转调用
	if !e.judgeKeyUsable(key) {
		return 0, nil, fmt.Errorf("evals 未启用（Judge Key 缺失或为占位符）")
	}
	messages := []map[string]string{{"role": "user", "content": prompt}}
	content, _, err := e.LLM.CallChat(ctx, base, key, model, messages, 200, false, 0.0)
	if err != nil {
		return 0, nil, err
	}
	return parseScores(content)
}

// parseScores 解析 Judge 输出的 JSON 分数并计算加权总分。
// 参数：content=模型输出文本；返回总分与各维分数 map。
func parseScores(content string) (float64, map[string]float64, error) {
	// 提取 JSON（容忍模型加代码块/说明）：取第一个 { 到最后一个 }
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
	// 按权重加权：术语30% + 语法20% + 语义30% + 数字10% + 风格10%
	total := 0.3*term + 0.2*grammar + 0.3*semantic + 0.1*numunit + 0.1*style
	if t, ok := m["total"]; ok && t > 0 {
		total = t // 模型直接给出 total 则优先采用
	}
	if total > 100 {
		total = 100 // 总分上限 100
	}
	return total, m, nil
}

// SaveRecord 保存评估记录。
// 参数：tid/userID/ticketID=租户/用户/工单标识，taskType=任务类型，model=模型/语言，
// input/output=原文与译文，scores=各维分数 map，total=总分，status=评估状态。
// 返回：新记录 ID（Store 为 nil 时返回 0,nil 静默跳过）。
func (e *Evaluator) SaveRecord(ctx context.Context, tid, userID, ticketID int64, taskType, model, input, output string, scores map[string]float64, total float64, status string) (int64, error) {
	if e.Store == nil {
		return 0, nil // 无存储则跳过落库
	}
	sb, _ := json.Marshal(scores) // 分数 map 序列化为 JSON
	return e.Store.SaveEvalRecord(&store.EvalRecord{
		TenantID: tid, UserID: userID, TicketID: ticketID, TaskType: taskType,
		Model: model, InputText: input, OutputText: output, Scores: string(sb), Total: total, Status: status,
	})
}
