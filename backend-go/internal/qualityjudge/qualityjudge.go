// ============ qualityjudge.go · 职责说明（2026-11-23）============
// 术语翻译语义判据的**唯一实现**：HY 回译 + evals 打分。
//
// 为什么独立成包：这条判据现在有两个调用方——
//
//	① 实时采集语义闸（internal/api/scrape_quality.go，注入 crawler.QualityJudge）；
//	② 云端历史知识库清洗 job（cmd/kb-clean）。
//
// 两处必须共用同一套回译Prompt/打分Prompt/阈值口径/Key解密，否则就长成
// "同一条判据在两个时刻两个结果"的静默腿（项目反复踩过的形态）。
//
// 判据形态（用户 2026-11-23 定稿，实测见方案 §九）：
//   - leg① 回译：把目标语译文用初翻模型（默认 Hunyuan-MT-7B）译回中文，作为语义证据。
//     ★ 必须 system 放指令 + user 放纯译文 + hunyuan=true（翻译引擎同形态），否则 HY 原样吐回。
//   - leg② 打分：源词+译文+回译证据+行业语境 → evals 模型（stage_models.initial_evals,
//     默认 GLM-Z1-9B）打 term/semantic，任一维 < 阈值即判错译。
//   - Key 自解密：evals.Evaluator.resolveJudge（evals.go:110）在 stage key 非空时返回
//     enc:v1 密文不解密 ⇒ 恒 401；这里自己解密，绕开那个 bug。
//   - 任一腿失败 ⇒ 返回 err，调用方决定 fail-open（实时闸放行）还是 fail-closed（清洗跳过）。
//
// 平台成本标记 llm.PlatformEvals：不扣客户账。
// ============================================================
package qualityjudge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"translator/internal/config"
	"translator/internal/llm"
	"translator/internal/store"
)

const (
	// DefaultBacktranslateModel 回译腿默认模型：现网初翻模型，中文↔多语翻译专精。
	DefaultBacktranslateModel = "tencent/Hunyuan-MT-7B"
	// ConfigKeyBacktranslateModel 超管可配的回译模型键（管理台 scrape-backtranslate 端点读写）。
	ConfigKeyBacktranslateModel = "scrape_tier3_backtranslate_model"
	// ConfigKeyMinScore 语义闸阈值键（0=关闭）。
	ConfigKeyMinScore = "scrape_tier3_quality_min"
	// DefaultMinScore 默认 term/semantic 双维最低分。
	DefaultMinScore = 70.0
)

// Judge 语义判据：回译腿（可配模型）+ 打分腿（evals 模型），key 自解密。
type Judge struct {
	client    *llm.Client
	st        *store.Store
	backModel string
	minScore  float64
}

// New 构造判据。回译模型从 system_config 读（可配），缺省 Hunyuan-MT-7B；
// minScore 从 system_config 读（0=关闭语义门），缺省 70。
// cfg/client/store 任一缺失 ⇒ 返回 nil（调用方按"无判据"降级）。
func New(cfg *config.Config, client *llm.Client, st *store.Store) *Judge {
	if cfg == nil || client == nil || st == nil {
		return nil
	}
	j := &Judge{client: client, st: st, backModel: DefaultBacktranslateModel, minScore: DefaultMinScore}
	if v, err := st.GetConfig(ConfigKeyBacktranslateModel); err == nil && strings.TrimSpace(v) != "" {
		j.backModel = strings.TrimSpace(v)
	}
	if v := st.ConfigIntAllowZero(ConfigKeyMinScore, int(DefaultMinScore)); v >= 0 {
		j.minScore = float64(v)
	}
	return j
}

// SetMinScore 覆盖判据阈值（job 的 -min-score 用；0=不改）。
func (j *Judge) SetMinScore(v float64) {
	if j != nil && v > 0 {
		j.minScore = v
	}
}

// resolveStageKey 读 stage_models[stage] 并**解密** api_key；"online" 伪档走全局 online_*。
func (j *Judge) resolveStageKey(stage string) (base, key, model string, err error) {
	if stage == "online" {
		base, _ = j.st.GetConfig("online_api_base")
		encKey, _ := j.st.GetConfig("online_api_key")
		model, _ = j.st.GetConfig("online_model")
		if base == "" || encKey == "" {
			return "", "", "", fmt.Errorf("online_* 配置不完整")
		}
		key = store.DecryptSecret(encKey)
		if key == "" {
			return "", "", "", fmt.Errorf("online_api_key 解密失败")
		}
		return base, key, model, nil
	}
	raw, gerr := j.st.GetConfig("stage_models")
	if gerr != nil || raw == "" {
		return "", "", "", fmt.Errorf("stage_models 未配置")
	}
	var m config.StageModels
	if json.Unmarshal([]byte(raw), &m) != nil {
		return "", "", "", fmt.Errorf("stage_models 解析失败")
	}
	sm, ok := m[stage]
	if !ok || sm.APIBase == "" || sm.Model == "" {
		return "", "", "", fmt.Errorf("模型档位 %s 未配置", stage)
	}
	key = sm.APIKey
	if key != "" {
		key = store.DecryptSecret(key)
		if key == "" {
			return "", "", "", fmt.Errorf("模型档位 %s 的 API Key 解密失败", stage)
		}
	}
	if key == "" {
		encKey, _ := j.st.GetConfig("online_api_key")
		key = store.DecryptSecret(encKey)
	}
	return sm.APIBase, key, sm.Model, nil
}

func judgeCtx(ctx context.Context) context.Context {
	return llm.WithPlatformCost(ctx, llm.PlatformEvals)
}

// backInstruction 按目标语生成回译指令（与引擎 translateInstructionCore 的 zh 分支同口径）。
func backInstruction(targetLang string) string {
	name := config.LangNames[targetLang]
	if name == "" {
		name = targetLang
	}
	return fmt.Sprintf("把下面的%s翻译为简体中文，只输出简体中文结果，不得复述原文，也不得在译文后面附加解释或备注", name)
}

// BackTranslate leg①：把目标语译文用初翻模型译回中文，作为语义证据。
func (j *Judge) BackTranslate(ctx context.Context, tgt, targetLang string) (string, error) {
	base, key, _, err := j.resolveStageKey("online")
	if err != nil {
		return "", err
	}
	model := j.backModel
	hunyuan := strings.HasPrefix(model, "tencent/Hunyuan-MT")
	content, _, err := j.client.CallChat(judgeCtx(ctx), base, key, model,
		[]map[string]string{
			{"role": "system", "content": backInstruction(targetLang)},
			{"role": "user", "content": tgt},
		}, 96, hunyuan, 0.0)
	if err != nil {
		return "", err
	}
	back := strings.TrimSpace(content)
	back = strings.Trim(back, "\"'`。.：: ")
	if len([]rune(back)) > 60 {
		back = string([]rune(back)[:60])
	}
	return back, nil
}

// Result 判定结果。
type Result struct {
	Pass     bool    // 是否达标
	Back     string  // 回译证据
	Term     float64 // 术语分
	Semantic float64 // 语义分
	Reason   string  // 说明/失败原因
}

// Evaluate leg①+leg②：回译证据 + evals 打分 → term/semantic 任一维低于阈值即判不达标。
// err 非 nil 表示判据本身没跑起来（网络/配置/解析），调用方按需 fail-open 或跳过。
func (j *Judge) Evaluate(ctx context.Context, source, translation, targetLang string) (Result, error) {
	var r Result
	if j.client == nil || j.st == nil {
		return r, fmt.Errorf("quality judge 未初始化")
	}
	back, err := j.BackTranslate(ctx, translation, targetLang)
	if err != nil {
		return r, fmt.Errorf("回译腿失败: %w", err)
	}
	r.Back = back
	base, key, model, err := j.resolveStageKey("initial_evals")
	if err != nil {
		return r, fmt.Errorf("evals 打分档失败: %w", err)
	}
	sysPrompt := "你是翻译质量评估员。请评估译文是否准确表达了原文含义（0-100分）。只输出 JSON: {\"term\":0,\"semantic\":0}"
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	user := fmt.Sprintf(`原文术语(中文): %s
目标语译文(%s): %s
该译文被独立回译成中文是: %s

请评估这个译文是否准确表达了原文术语的含义。注意行业语境：营销词在通用词典里可能有别的含义（如 ליד 在营销语境是"线索"而非"旁边"），品牌名音译可能对应普通词（如 צ'רי 是"奇瑞"而非"樱桃"），不要只按通用字面判断。只输出 JSON。`, source, langName, translation, back)
	content, _, err := j.client.CallChat(judgeCtx(ctx), base, key, model,
		[]map[string]string{
			{"role": "system", "content": sysPrompt},
			{"role": "user", "content": user},
		}, 64, false, 0.0)
	if err != nil {
		return r, fmt.Errorf("evals 打分腿失败: %w", err)
	}
	var scores struct {
		Term     float64 `json:"term"`
		Semantic float64 `json:"semantic"`
	}
	if json.Unmarshal([]byte(ExtractJSON(content)), &scores) != nil {
		return r, fmt.Errorf("evals 打分输出非 JSON: %.60s", content)
	}
	r.Term, r.Semantic = scores.Term, scores.Semantic
	r.Pass = !(scores.Term < j.minScore || scores.Semantic < j.minScore)
	r.Reason = fmt.Sprintf("回译=%q，打分 term=%.0f semantic=%.0f", back, scores.Term, scores.Semantic)
	return r, nil
}

// Retranslate 正向重译：把中文源词用初翻模型翻译成目标语，用于替换被判错译的词条。
// ★ 与回译腿同模型同调用形态（system 指令 + user 纯文本 + hunyuan=true），只是方向反过来。
func (j *Judge) Retranslate(ctx context.Context, sourceZh, targetLang string) (string, error) {
	base, key, _, err := j.resolveStageKey("online")
	if err != nil {
		return "", err
	}
	model := j.backModel
	hunyuan := strings.HasPrefix(model, "tencent/Hunyuan-MT")
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	instr := fmt.Sprintf("把下面的中文翻译为%s，只输出%s译文本身，不得复述中文原文，不得附加解释或回译。", langName, langName)
	content, _, err := j.client.CallChat(judgeCtx(ctx), base, key, model,
		[]map[string]string{
			{"role": "system", "content": instr},
			{"role": "user", "content": sourceZh},
		}, 96, hunyuan, 0.0)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(content)
	out = strings.Trim(out, "\"'`。.：: ")
	if len([]rune(out)) > 120 {
		out = string([]rune(out)[:120])
	}
	return out, nil
}

// Repair 修正一条被判错译的词条：中文源词经 HY 正向重译 → 新译文再过回译+打分验证。
// 返回 (新译文, true, nil) 表示可回写；验证不过或任一步失败 ⇒ ("", false, err)。
// ★ 不返回"没验证过"的重译结果——那等于把错译换成另一个错译。
func (j *Judge) Repair(ctx context.Context, sourceZh, targetLang string) (string, bool, error) {
	fixed, err := j.Retranslate(ctx, sourceZh, targetLang)
	if err != nil {
		return "", false, fmt.Errorf("正向重译失败: %w", err)
	}
	if strings.TrimSpace(fixed) == "" || strings.TrimSpace(fixed) == strings.TrimSpace(sourceZh) {
		return "", false, fmt.Errorf("重译结果为空或与中文源同形")
	}
	// 结构化兜底：新译文必须匹配目标语书写系统，否则不回写
	if !targetScriptOK(targetLang, fixed) {
		return "", false, fmt.Errorf("重译结果书写系统不符: %q", fixed)
	}
	// 同一条判据验证新译文（回译 + 打分）
	r, err := j.Evaluate(ctx, sourceZh, fixed, targetLang)
	if err != nil {
		return "", false, fmt.Errorf("重译验证失败: %w", err)
	}
	if !r.Pass {
		return "", false, fmt.Errorf("重译后仍未达标: %s", r.Reason)
	}
	return fixed, true, nil
}

// targetScriptOK 目标语书写系统快检（与 gate.TargetTextMatchesLang 同口径的最小内联，
// 避免 qualityjudge 包 import gate 形成依赖；job/实时闸都在写库前已跑过 gate 的结构化闸）。
func targetScriptOK(targetLang, text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	if targetLang == "zh" || targetLang == "zh_hant" {
		for _, r := range t {
			if r >= 0x4e00 && r <= 0x9fff {
				return true
			}
		}
		return false
	}
	// 非中文目标语：至少有一个非 ASCII 字母（拉丁/西里尔/希伯来/阿拉伯等）
	for _, r := range t {
		if r > 0x7f {
			return true
		}
	}
	return false
}

// ExtractJSON 从可能带前后缀的模型输出里抠出第一个 {...} JSON 对象（纯函数，便于单测）。
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}
