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
//   - leg② 打分：源词+译文+回译证据+行业语境 → evals 模型（stage_models.initial_evals）
//     打 term/semantic，任一维 < 阈值即判错译；具体模型以后台现值为准。
//   - Key 自解密：evals.Evaluator.resolveJudge 在 stage key 非空时返回
//     enc:v1 密文不解密 ⇒ 恒 401；这里自己解密，绕开那个 bug。
//   - 全局 LLM 三件套只走 llmsource.Current：回译与 stage 缺 Key 时的兜底
//     都遵守 env > 数据库 > 主路由，禁止再直接读 online_* 绕过热加载。
//   - 任一腿失败 ⇒ 返回 err；实时采集 fail-closed 丢弃，清洗 job 跳过且不落断点。
//
// 平台成本标记 llm.PlatformEvals：不扣客户账。
// ============================================================
package qualityjudge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"translator/internal/config"
	"translator/internal/gate"
	"translator/internal/llm"
	"translator/internal/llmsource"
	"translator/internal/observability"
	"translator/internal/secret"
	"translator/internal/store"
)

// 语义闸默认模型、配置键、阈值与复核参数。
const (
	// DefaultBacktranslateModel 回译腿默认模型：现网初翻模型，中文↔多语翻译专精。
	DefaultBacktranslateModel = "tencent/Hunyuan-MT-7B"
	// ConfigKeyBacktranslateModel 超管可配的回译模型键（管理台 scrape-backtranslate 端点读写）。
	ConfigKeyBacktranslateModel = "scrape_tier3_backtranslate_model"
	// ConfigKeyMinScore 语义闸阈值键（0=关闭）。
	ConfigKeyMinScore = "scrape_tier3_quality_min"
	// DefaultMinScore 默认 term/semantic 双维最低分。
	DefaultMinScore = 70.0
	// reviewStage 复核兜底腿的档位键：复用引擎校对腿那条 stage_models.review。
	// ★ 不新造档位、不加管理台卡片：这一腿要问的是"同一个判定问题，换一个模型再问一遍"，
	// 运营要换模型时本就该与校对腿一起换（两处语义同源）；多开一个档位只会多一处没人配的面板。
	reviewStage = "review"
	// reviewMaxTokens 复核腿的 max_tokens：与主判据同档（都只出两维 JSON）。
	// 不是越宽越好——推理系模型可能先写解释再给 JSON，预算越宽越容易截断正文。
	reviewMaxTokens = 64
)

// judgeSysPrompt / judgeUserPromptFmt 是打分腿的提示词口径（2026-11-24 定稿）。
//
// 为什么换掉旧口径：旧 sys 只有一句"评估译文是否准确表达原文含义"，user 里塞了
// 两枚希伯来语举例（ליד / צ'רי）当"行业语境"示范——对马来语、瑞典语、波兰语样本
// 是纯噪音，且缺"回译腿自己可能取错义项"的免责口径。同一模型（GLM-4-9B）同一参数
// 下固定样本集对照：旧口径 real 组应过 6/9，本口径 9/9（prod 5/5、ctrl 4/4）。
// ⇒ 改动这两段必须同步复跑 cmd/evals_probe（clean 臂），否则"换对了"无法归因。
const (
	judgeSysPrompt = "你是术语翻译审核员。给定中文术语、目标语译文与机器回译，判断译文是否准确传达该术语。\n" +
		"评分两维，各 0-100 分：\n" +
		"- term：术语对应是否准确（行业词、专名与多义词按行业语境判断）\n" +
		"- semantic：回译所表达的含义与中文术语是否一致\n" +
		"评分锚点：\n" +
		"90-100 = 含义一致（允许同义表达、上下位词）；\n" +
		"70-89 = 基本一致，只是措辞不同；\n" +
		"40-69 = 部分相关但有明显偏差；\n" +
		"0-39 = 含义错误或与术语无关。\n" +
		"注意：回译由另一个模型生成，可能取错义项或表达生硬；当译文与回译明显不符时以译文本身为准，不要仅因回译异常就压分。\n" +
		"只输出 JSON：{\"term\":0,\"semantic\":0}"

	judgeUserPromptFmt = "中文术语：%s\n目标语译文（%s）：%s\n机器回译：%s\n请打分，只输出 JSON。"
)

// JudgeSystemPrompt 返回产品正在使用的两维评分 system 提示词，供探针同源复用。
func JudgeSystemPrompt() string { return judgeSysPrompt }

// JudgeUserPrompt 按产品口径生成 user 提示词，避免探针另抄一份后与产品漂移。
func JudgeUserPrompt(source, langName, translation, back string) string {
	return fmt.Sprintf(judgeUserPromptFmt, source, langName, translation, back)
}

// Judge 语义判据：回译腿（可配模型）+ 打分腿（evals 模型），key 自解密。
type Judge struct {
	client    *llm.Client
	st        *store.Store
	cfg       *config.Config
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
	j := &Judge{client: client, st: st, cfg: cfg, backModel: DefaultBacktranslateModel, minScore: DefaultMinScore}
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

// resolveStageKey 读 stage_models[stage] 并**解密** api_key；"online" 伪档走 llmsource 当前快照。
func (j *Judge) resolveStageKey(ctx context.Context, stage string) (base, key, model string, err error) {
	if stage == "online" {
		snap := llmsource.Current(ctx, j.st, j.cfg)
		if snap.Placeholder || snap.OnlineAPIKey == "" || secret.IsSecretMasked(snap.OnlineAPIKey) {
			return "", "", "", fmt.Errorf("online_* 配置不完整或密钥不可用")
		}
		if snap.From == llmsource.FromRoute {
			best := -1
			for i := range snap.Routes {
				rt := &snap.Routes[i]
				if rt.Model == j.backModel && rt.APIBase != "" && rt.APIKey != "" && !secret.IsSecretMasked(rt.APIKey) &&
					(best < 0 || rt.Weight > snap.Routes[best].Weight) {
					best = i
				}
			}
			if best >= 0 {
				rt := snap.Routes[best]
				return rt.APIBase, rt.APIKey, j.backModel, nil
			}
			return "", "", "", fmt.Errorf("主路由中未找到回译模型 %s", j.backModel)
		}
		if snap.OnlineAPIBase == "" {
			return "", "", "", fmt.Errorf("online_* 端点未配置")
		}
		return snap.OnlineAPIBase, snap.OnlineAPIKey, j.backModel, nil
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
	if key == "" || secret.IsSecretMasked(key) {
		snap := llmsource.Current(ctx, j.st, j.cfg)
		key = snap.OnlineAPIKey
		if snap.Placeholder || key == "" || secret.IsSecretMasked(key) {
			return "", "", "", fmt.Errorf("模型档位 %s 未配 Key，全局 LLM 密钥也不可用", stage)
		}
	}
	return sm.APIBase, key, sm.Model, nil
}

// judgeCtx 标记平台承担的评估成本，避免记到客户额度。
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
	base, key, _, err := j.resolveStageKey(ctx, "online")
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
	// RescuedByReview 复核兜底腿把"主判据不达标"改判为达标（2026-11-24）。
	RescuedByReview bool
	// ReviewNote 复核腿读数，三档不同形：改判通过 / 维持不通过 / 不可用及原因。
	// ★ 它只用于留痕与排障，**不参与** Pass 判定——判定口径仍只有阈值那一条。
	ReviewNote string
	// Conclusive 表示本轮拿到了有效评分（主判或复核）。false 表示无法判定，
	// 清洗端必须按报错跳过，绝不能把坏 JSON/截断/两腿不可用当成有效低分删除。
	Conclusive bool
}

// Evaluate leg①+leg②：回译证据 + evals 打分 → term/semantic 任一维低于阈值即判不达标。
// err 非 nil 表示判据本身没跑起来（网络/配置/解析）；实时采集必须丢弃，清洗必须跳过不删。
func (j *Judge) Evaluate(ctx context.Context, source, translation, targetLang string) (Result, error) {
	var r Result
	if j.client == nil || j.st == nil {
		return r, fmt.Errorf("quality judge 未初始化")
	}
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	reviewOrError := func(cause error) (Result, error) {
		r.Reason = "主判据不可用: " + cause.Error() + "，转复核腿"
		j.rescueByReview(ctx, &r, source, translation, langName, r.Back)
		if r.Conclusive {
			return r, nil
		}
		return r, cause
	}
	back, err := j.BackTranslate(ctx, translation, targetLang)
	if err != nil {
		return reviewOrError(fmt.Errorf("回译腿失败: %w", err))
	}
	r.Back = back
	base, key, model, err := j.resolveStageKey(ctx, "initial_evals")
	if err != nil {
		return reviewOrError(fmt.Errorf("evals 打分档失败: %w", err))
	}
	user := JudgeUserPrompt(source, langName, translation, back)
	content, finish, err := j.client.CallChat(judgeCtx(ctx), base, key, model,
		[]map[string]string{
			{"role": "system", "content": JudgeSystemPrompt()},
			{"role": "user", "content": user},
		}, 64, false, 0.0)
	if err != nil {
		return reviewOrError(fmt.Errorf("evals 打分腿失败: %w", err))
	}
	term, semantic, parseErr := ParseJudgeScores(content)
	if parseErr != nil || strings.EqualFold(strings.TrimSpace(finish), "length") {
		if strings.EqualFold(strings.TrimSpace(finish), "length") {
			r.Reason = "主判据输出被截断（finish=length），转复核腿"
		} else {
			r.Reason = fmt.Sprintf("主判据输出无效: %v，转复核腿", parseErr)
		}
		j.rescueByReview(ctx, &r, source, translation, langName, back)
		if r.Conclusive {
			return r, nil
		}
		if strings.EqualFold(strings.TrimSpace(finish), "length") {
			return r, fmt.Errorf("主判据输出被截断且复核未给出有效裁决")
		}
		return r, fmt.Errorf("主判据输出无效且复核未给出有效裁决: %w", parseErr)
	}
	r.Term, r.Semantic = term, semantic
	r.Conclusive = true
	r.Pass = !(term < j.minScore || semantic < j.minScore)
	r.Reason = fmt.Sprintf("回译=%q，打分 term=%.0f semantic=%.0f", back, term, semantic)
	if !r.Pass {
		j.rescueByReview(ctx, &r, source, translation, langName, back)
	}
	return r, nil
}

// rescueByReview 复核兜底腿（★ 2026-11-24 用户令「人工不兜底，靠关闭思考的复核模型兜底」）。
//
// 三条口径，改这一腿前必读（对照读数由 cmd/evals_probe 固定样本集复跑）：
//
//		① **只捞不杀**：只在主判据已判"不达标"或回执无效时拨。因此它永远不会把已通过的条目改判为不通过。
//		   历史回放里复核模型对部分主判已通过的正确译文也给过低分，做成"两票都通过才通过"会放大误杀；
//		   本腿只负责救回主判误杀，不承担反向否决。
//		② **同一把尺子**：同一个 judgeSysPrompt / judgeUserPromptFmt、同一个 minScore，
//		   只换模型档。换提示词就等于换判据，那时候"复核"与"重判"是两个事实，无法归因。
//		③ **关思考是必需的**：推理系模型在 64 tokens 下可能先写思维链再给 JSON，
//	  正文只剩半截。关思考走 llm.NoThinkingExtra（全仓唯一一份 map）。
//
// 失败语义：档位没配 / 429 / 超时 / 截断 / 无效 JSON ⇒ **保留原判不通过**，只写 ReviewNote，
// 不返错也不翻转。一次网络抖动不该让"这条译文不合格"变成"合格"（同 AGENTS §一·3
// 「库读不到就沿用上一份快照」的取向：拿不到第二意见时，第一意见仍然是唯一的证据）。
func (j *Judge) rescueByReview(ctx context.Context, r *Result, source, translation, langName, back string) {
	base, key, model, err := j.resolveStageKey(ctx, reviewStage)
	if err != nil {
		r.ReviewNote = "复核档不可用: " + err.Error()
		return
	}
	user := JudgeUserPrompt(source, langName, translation, back)
	callCtx := llm.WithChatExtraParams(judgeCtx(ctx), llm.NoThinkingExtra())
	content, finish, err := j.client.CallChat(callCtx, base, key, model,
		[]map[string]string{
			{"role": "system", "content": JudgeSystemPrompt()},
			{"role": "user", "content": user},
		}, reviewMaxTokens, false, 0.0)
	if err != nil {
		r.ReviewNote = "复核腿调用失败: " + err.Error()
		return
	}
	if strings.EqualFold(strings.TrimSpace(finish), "length") {
		r.ReviewNote = "复核腿输出被截断（finish=length），保留原判"
		return
	}
	term, semantic, parseErr := ParseJudgeScores(content)
	if parseErr != nil {
		r.ReviewNote = fmt.Sprintf("复核腿输出无效: %v", parseErr)
		return
	}
	r.Conclusive = true
	if term < j.minScore || semantic < j.minScore {
		r.ReviewNote = fmt.Sprintf("复核维持不通过（term=%.0f semantic=%.0f）", term, semantic)
		return
	}
	primaryTerm, primarySemantic := r.Term, r.Semantic
	r.Term, r.Semantic = term, semantic
	r.Pass = true
	r.RescuedByReview = true
	r.ReviewNote = fmt.Sprintf("复核改判通过（term=%.0f semantic=%.0f）", term, semantic)
	r.Reason += "；" + r.ReviewNote
	// 捞回一条就是一处"主判据误杀"的实证：现网靠这行读数估算本腿价值与复核模型质量。
	observability.Warn(ctx, "tier3 语义闸主判不达标，复核腿改判通过",
		"src", source, "tgt", translation, "lang", langName,
		"term", primaryTerm, "semantic", primarySemantic,
		"review_term", term, "review_semantic", semantic)
}

// Retranslate 正向重译：把中文源词用初翻模型翻译成目标语，用于替换被判错译的词条。
// ★ 与回译腿同模型同调用形态（system 指令 + user 纯文本 + hunyuan=true），只是方向反过来。
func (j *Judge) Retranslate(ctx context.Context, sourceZh, targetLang string) (string, error) {
	base, key, _, err := j.resolveStageKey(ctx, "online")
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
	if !containsHan(sourceZh) {
		return "", false, fmt.Errorf("仅中文源可进入正向重译，拒绝非中文源")
	}
	fixed, err := j.Retranslate(ctx, sourceZh, targetLang)
	if err != nil {
		return "", false, fmt.Errorf("正向重译失败: %w", err)
	}
	if strings.TrimSpace(fixed) == "" || strings.TrimSpace(fixed) == strings.TrimSpace(sourceZh) {
		return "", false, fmt.Errorf("重译结果为空或与中文源同形")
	}
	// 与实时 crawler 同一把结构化闸：修复产物也必须过书写系统/转写/标点/同形检查。
	if reason := gate.StructuralReject("zh", sourceZh, targetLang, fixed); reason != "" {
		return "", false, fmt.Errorf("重译结果未过结构化闸: %s", reason)
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

// containsHan 判源文是否含汉字；重译提示词固定为中文源，非中文源必须 fail-closed。
func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
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

// ParseJudgeScores 严格解析两维评分：字段必须存在且都落在 0-100。
// 指针字段用于区分"模型真的给了 0 分"与"模型根本没给这个字段"。
func ParseJudgeScores(content string) (term, semantic float64, err error) {
	var scores struct {
		Term     *float64 `json:"term"`
		Semantic *float64 `json:"semantic"`
	}
	raw := ExtractJSON(content)
	if err := json.Unmarshal([]byte(raw), &scores); err != nil {
		return 0, 0, fmt.Errorf("非 JSON: %.80s", raw)
	}
	if scores.Term == nil || scores.Semantic == nil {
		return 0, 0, fmt.Errorf("缺少 term/semantic 字段: %.80s", raw)
	}
	if *scores.Term < 0 || *scores.Term > 100 || *scores.Semantic < 0 || *scores.Semantic > 100 {
		return 0, 0, fmt.Errorf("评分越界（应为 0-100）: %.80s", raw)
	}
	return *scores.Term, *scores.Semantic, nil
}
