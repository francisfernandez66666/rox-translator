// ============ onboard_draft.go · 职责说明 ============
// 一期引导页「自由描述 → 候选字段」的**模型腿**（★ 2026-11-07）：把卖家贴进来的自由描述
// 交给一个**显式配置了 onboard_draft 阶段模型**的指令档，取回一个四键 JSON 对象
// （name / tagline / use_case / target_langs），供 api/product_onboard_draft.go 组装成候选给卖家确认。
//
// ★ 为什么这一条只认显式配置、**不走** resolveModelForStage 那条「退回初翻」的咽喉：
//
//	AGENTS §21 的 ocr_fix 现网实证——回落链在没有阶段模型时会退回 ai_initial／全局主模型，
//	而现网全局是一个**翻译模型**（tencent/Hunyuan-MT）；让它按 JSON 判定二十行文字，
//	六次运行全是 `calls:3, tokens:0, latency_ms:44998`＝每张图白等满预算、零收益、零日志线索。
//	本腿的活（抽取＋判定）与 ③ 同形，所以同一条纪律照搬：**没配这一档就一次都不拨**，
//	直接回零值候选（fail-open），由界面让卖家手打。⇒ 它也**不许**接进 ai_initial／review 的降级链，
//	健康面出 `degraded`（动作＝去配第 ⑨ 张卡）而不是 `ok`。
//
// ★ 三条出栈纪律：
//
//	① 任何失败（没配模型／超时／上游错／回执被 max_tokens 截断／不是合法 JSON／空回执）
//	   一律回**零值**并把病因收进 reason 档（对外排障契约，同 §13 那批分档名）；
//	   调用方据此出「空草稿 + success:true」——模型抖动绝不许写成卖家的错误（§一·8 的方向）。
//	② 零成本：token 全部平台承担（llm.WithPlatformCost + llm.PlatformOnboardDraft ⇒
//	   留痕行照写、余额一分不动，§一·11）；预估侧走 engine.OnboardDraftAllowanceTokens 恒 0。
//	③ 本文件**没有任何客户可见文案**（向导的文案全在调用方与 12 语种词典里），
//	   提示词里也刻意不出现「准确／保证／一定能识别」这类对外承诺（§19「不写可用性担保」同族）。
//
// ⚠️ 提示词只有一份（onboardDraftPrompt 那一个包级常量），调用点必须引用它本身；
//
//	等值与"没有第二份抄本"由 engine/onboard_draft_gate_test.go 与 api 侧的源码级腿各判一次。
//
// ======================================================
package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"translator/internal/config"
	"translator/internal/llm"
	"translator/internal/observability"
)

// onboardDraftPrompt 唯一的系统指令：四键 JSON 契约＋「没有就不写」这条主轴。
//
// 为什么反复强调空串而不是占位符：模型最常见的偷懒形态是"抽不出来就写一句『未提供』"，
// 而那一串会直接被预填进卖家的建档表单（再由 /api/product/save 落库成产品对外页的正文）。
// 出栈侧还有一道同样窄的清洗（api/product_onboard_draft.go 的 onboardDraftLine），
// 两条腿各治一头：这里管"别说"，那里管"没说出口的别当真"。
const onboardDraftPrompt = `你是产品建档助手。卖家会贴进来一段关于自己产品的自由描述，你的活是从这段描述里抽取四个字段，用来预填卖家自己的建档表单。

只输出一个 JSON 对象，键固定为 name / tagline / use_case / target_langs；不要输出代码块标记、注释、解释或 JSON 之外的任何字符。

四条规则，一条都不许越：
1. 只写描述里已经存在的事实。描述里没有的信息，对应键给空字符串 ""；绝不写"未提供""暂无""未知"这类占位文字，也不凭常识补全或猜测。
2. name＝产品或服务的名称，照描述里出现过的叫法写；描述里没有明确名称就给 ""，不自造品牌名。
3. tagline＝一句话定位，不超过 60 个字符；use_case＝典型用途或使用场景，不超过 400 个字符。两者都不加"最""第一""百分百"这类没有依据的承诺词。
4. target_langs＝只能从用户消息给出的语种清单里挑；清单里没有的代码一律不许出现；描述里没提到任何买家语言就给空数组 []；卖家自己界面所用的语言不算目标语言。

name / tagline / use_case 一律沿用描述本身所用的语言，不做翻译。`

// onboardDraftMaxTokens 回执预算：四个短字段＋一个小数组，600 足够。
// 为什么宁可给小：`max_tokens` 是这一腿的**成本封顶**——判定题一旦开始发散，
// 小预算让它早点以 finish_reason=length 收场（那一档本函数照样 fail-open），
// 而不是把整份预算换成一句没用的长文（§21 那条白等满预算的同一族）。
const onboardDraftMaxTokens = 600

// onboardDraftBudget 单次判定的等待上限（与 ③ 同档 15s）。
// 到点即回 fail-open：向导手打这条路永远开着，所以"多等一会儿"不换来任何客户可见的东西。
const onboardDraftBudget = 15 * time.Second

// OnboardDraftCandidate 抽取结果（★ 语种**未经站点码表过滤**——码表在调用方那侧，引擎不造第二份清单）。
type OnboardDraftCandidate struct {
	Name        string
	Tagline     string
	UseCase     string
	TargetLangs []string
}

// ExtractOnboardDraft 跑一次「自由描述 → 候选字段」判定；任何失败都回零值候选（fail-open）。
// 参数：text=卖家贴进来的自由描述（调用方已判非空与长度上限）；uiLang=卖家界面语种码；
// langCodes=站点支持的对外语种清单（**由调用方传入**，唯一事实源是 store.ProductLangs）。
// 返回候选与 reason 档：""=正常取回；其余依次是
// no_text / no_langs / no_stage_model / upstream / timeout / cancelled / truncated / empty_output / unparsable
// （档名是对外排障契约，与 §13 那批 reason 同样逐字钉在本包单测里，改名要同步改锁与 T97）。
func (e *Engine) ExtractOnboardDraft(ctx context.Context, text, uiLang string, langCodes []string) (OnboardDraftCandidate, string) {
	// ★ 判空排在最前，且**不碰 e 的任何字段**：nil 引擎（未装配/启动早期）也要能安全返回。
	if strings.TrimSpace(text) == "" {
		return OnboardDraftCandidate{}, "no_text"
	}
	if len(langCodes) == 0 {
		// 没有清单就等于没有"可选项"：让模型自由发挥语种码，产出的脏值会直接被存进对外页的 hreflang。
		return OnboardDraftCandidate{}, "no_langs"
	}
	if e == nil || e.LLM == nil {
		return OnboardDraftCandidate{}, "no_stage_model"
	}
	base, key, model, configured := e.onboardDraftStageModel(ctx)
	if !configured {
		// 一次都不拨（本函数上面那条 ocr_fix 教训的落地位置）。这里**不记零产出熔断**：
		// §21 那条口径——压根没拨＝没资格记账，否则一次上层取消就能把这条腿押进冷却。
		observability.Warn(ctx, "onboard_draft 未配置阶段模型，本单不拨判定腿",
			"reason", "no_stage_model", "ui_lang", uiLang, "chars", len([]rune(text)))
		return OnboardDraftCandidate{}, "no_stage_model"
	}

	// 平台承担 + 交互通道 + 写死关思考：三件都在同一处挂，缺一个都会长成静默的错账——
	//   · WithPlatformCost ⇒ 留痕不扣客户（§一·11）；
	//   · WithInteractive ⇒ 排在交互通道，不跟文件工单抢 fileChatSem（§21 那条「timeout 先问排队」）；
	//   · noThinkingExtra ⇒ 判定题不需要思维链，而推理系会先把 600 token 预算吃光再给 JSON。
	//     这一档**不留 env 开关**（同 §21：留了下一位遇到某家 400 就顺手打开，症状原样复发）。
	cctx, cancel := context.WithTimeout(
		llm.WithChatExtraParams(
			llm.WithInteractive(llm.WithPlatformCost(ctx, llm.PlatformOnboardDraft)),
			noThinkingExtra()),
		onboardDraftBudget)
	defer cancel()

	messages := []map[string]string{
		{"role": "system", "content": onboardDraftPrompt},
		{"role": "user", "content": onboardDraftUserMsg(uiLang, langCodes, text)},
	}
	// 温度写死 0：候选要能稳定复现（同一份描述两次识别出不同名字，卖家会以为这一腿在猜）。
	content, finishReason, err := e.LLM.CallChat(cctx, base, key, model, messages, onboardDraftMaxTokens, false, 0)
	if err != nil {
		reason := ocrFixCallReason(err) // 同一个病因分类器（timeout/cancelled/upstream），不另造第二把尺子
		observability.Warn(ctx, "onboard_draft 判定腿未取回回执（按空草稿出栈）",
			"reason", reason, "chars", len([]rune(text)), "ui_lang", uiLang)
		return OnboardDraftCandidate{}, reason
	}
	// ★ finish_reason 必须读：截断的 JSON 既解不出也不许当成"这一单没提到语种"（那是给失败背书）。
	if finishReason == "length" {
		observability.Warn(ctx, "onboard_draft 回执被 max_tokens 截断（按空草稿出栈）",
			"reason", "truncated", "finish_reason", finishReason)
		return OnboardDraftCandidate{}, "truncated"
	}
	if strings.TrimSpace(content) == "" {
		observability.Warn(ctx, "onboard_draft 上游回了空正文（按空草稿出栈）", "reason", "empty_output")
		return OnboardDraftCandidate{}, "empty_output"
	}
	cand, ok := parseOnboardDraftJSON(content)
	if !ok {
		// 只报正文长度：卖家的描述里带着公司名与数字，回执正文一律不进日志。
		observability.Warn(ctx, "onboard_draft 回执不是合法 JSON 对象（按空草稿出栈）",
			"reason", "unparsable", "bytes", len(content))
		return OnboardDraftCandidate{}, "unparsable"
	}
	return cand, ""
}

// onboardDraftUserMsg 拼 user 消息：清单＋界面语种＋描述本体。
// 语种清单**由调用方传进来**（唯一事实源在 store），引擎这里只做一次 strings.Join——
// 在引擎里再抄一份码表就是 §一·5 那一族「两份名单迟早打架」。
func onboardDraftUserMsg(uiLang string, langCodes []string, text string) string {
	own := strings.TrimSpace(uiLang)
	if own == "" {
		own = "（未提供）"
	}
	var sb strings.Builder
	sb.WriteString("可选语种清单（只能从这里挑）：")
	sb.WriteString(strings.Join(langCodes, ","))
	sb.WriteString("\n卖家界面语言：")
	sb.WriteString(own)
	sb.WriteString("\n以下是卖家的自由描述：\n")
	sb.WriteString(strings.TrimSpace(text))
	return sb.String()
}

// parseOnboardDraftJSON 防御式解析：剥代码围栏 → 取第一个 { 到最后一个 } → 解对象。
// 任何一步不成立即回 (零值,false)——**不许**把半截 JSON 里读出来的那一两个键当成品交出去：
// 截断处最可能停在 target_langs 中间，那种"残缺的完整"会被卖家当成"识别结果就这么点"。
// 参数 content: 上游正文（可能带 ```json 围栏或前后寒暄）。返回: 候选与是否解析成功。
func parseOnboardDraftJSON(content string) (OnboardDraftCandidate, bool) {
	body := strings.TrimSpace(content)
	if i := strings.IndexByte(body, '{'); i >= 0 {
		if j := strings.LastIndexByte(body, '}'); j > i {
			body = body[i : j+1]
		} else {
			// 只有开头没有闭合＝被截断/被围栏吃掉，直接判失败（上面的注释说明为什么不许"将就用"）。
			return OnboardDraftCandidate{}, false
		}
	} else {
		return OnboardDraftCandidate{}, false
	}
	// target_langs 收 json.RawMessage：模型常把语种写成 [1,2] 或 "en,ja"（单个字符串）。
	// 整包解一次就失败太脆——**三个文本字段照收**，只有数组那一维单独容错（少一处误伤，不多信一个值）。
	var raw struct {
		Name      string          `json:"name"`
		Tagline   string          `json:"tagline"`
		UseCase   string          `json:"use_case"`
		LangsJSON json.RawMessage `json:"target_langs"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return OnboardDraftCandidate{}, false
	}
	return OnboardDraftCandidate{
		Name:        raw.Name,
		Tagline:     raw.Tagline,
		UseCase:     raw.UseCase,
		TargetLangs: parseOnboardDraftLangs(raw.LangsJSON),
	}, true
}

// parseOnboardDraftLangs 把 target_langs 那一维收成字符串切片；解不出即空切片（不是 nil 也不是报错）。
// 三条容错各对一个真实形态：["en","ja"]（正常）／["en",1,"ja"]（混进数字）／"en,ja"（整段写成一个串）。
// ⚠️ 这里**不做**任何"这个码支不支持"的判断——码表在调用方那侧（同 onboardDraftUserMsg 那条分工）。
func parseOnboardDraftLangs(raw json.RawMessage) []string {
	out := make([]string, 0, 4)
	if len(raw) == 0 {
		return out
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, v := range arr {
			if s := strings.TrimSpace(v); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	var any []interface{}
	if err := json.Unmarshal(raw, &any); err == nil {
		for _, v := range any {
			if s, ok := v.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
		}
		return out
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		for _, part := range strings.Split(one, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// onboardDraftStageModel 这一档模型配置的**唯一读点**（照 ocrFixStageModel 的同形写法）。
// ⚠️ 只调 resolveStageModel，**禁止**改成 resolveModelForStage——后者在本档没配时会退回
// ai_initial／全局主模型并把 stageActive 一起报成 true，于是「读数说配好了、实际在拿翻译模型干判定题」，
// 那正是 §21 里「健康面恒 ok、这条腿恒超时」的形态。
func (e *Engine) onboardDraftStageModel(ctx context.Context) (base, key, model string, configured bool) {
	return e.resolveStageModel(ctx, config.StageOnboardDraft)
}

// OnboardDraftHealthWord 健康面的**状态词**（只出词，绝不出 base/key/model／供应商域名）。
// 两档动作不同，所以不许合成一个词（同 §17 那条 `backoff` vs `degraded` 的取向）：
//
//	ok        ＝ 第 ⑨ 张卡配好了且取得到端点，这一腿会真拨；
//	degraded  ＝ 没配/端点不全 ⇒ 一次都不拨，界面按手打走（动作＝**去配**，不是等）。
func (e *Engine) OnboardDraftHealthWord(ctx context.Context) string {
	if e == nil || e.Cfg == nil || e.LLM == nil {
		return "degraded"
	}
	base, key, model, configured := e.onboardDraftStageModel(ctx)
	if !configured {
		return "degraded"
	}
	if strings.TrimSpace(base) != "" && strings.TrimSpace(model) != "" && strings.TrimSpace(key) != "" {
		return "ok"
	}
	return "degraded"
}
