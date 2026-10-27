// ============ estimate.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 翻译前报价预览接口（estimate）：按 Token 实费口径预估本次翻译的消耗区间，
// 只读不扣减。前端在工作台/建单页提交前展示「预计消耗 X~Y 积分 ≈ A 句 · 余额 N 积分」。
//
// ★ 2026-10-13 用户令⑦（#31）：估算公式整体换成 engine.EstimateChatTokens 那一份
//
//	「按真实链路结构、常数派生自真实提示词」的模型。旧公式是
//	「源句数 × 语种数 × 每句 500 token × 2 轮」，现网读数：180 个拉丁字符、单语种、
//	专业档 → 横幅「≈30~75 积分（约 60 句）」，而同一气泡实收「1 积分」。
//	错因不是系数调错，是**结构**错两处（详见 engine/costmodel.go 文件头）：
//	即时翻译整篇一次调用、句数根本不进成本；而 countSentences 把 "e.g." 与
//	"600912.SH" 各切一刀，把一段话数成 8 句。
//
// ★ 与实扣「同一把尺子」指的是两件事（不是拿预估当账单）：
//	① markup 只走 modeMarkup(eff, mode) 这一个解析口（与 ChargeUsageRealtime 同源，含模式因子）；
//	② token→积分只走 Store.PointsFromTokens（与出参 points_used、台账扣费同一个汇率与取整方向）。
//
// 出参字段一字未改（前端零改动）；cost_sentences_approx 仍是「上限 ÷ 每句汇率」，
// 只是分子不再是句数×系数，所以那一档读数从「约 60 句」回到「约 1~2 句」这个量级。
// =============================================

import (
	"encoding/json"
	"net/http"

	"translator/internal/engine"
	apierrors "translator/internal/errors"
)

// estimateReq 预估接口入参（提成具名类型：单测要直接构造它，匿名结构体在包外/跨函数不可复用）。
type estimateReq struct {
	Text         string   `json:"text"`          // 文本内容（与 segment_count 二选一）
	SegmentCount int64    `json:"segment_count"` // 文件提取段数（文件场景直传）
	TargetLangs  []string `json:"target_langs"`  // 目标语言列表
	Mode         string   `json:"mode"`          // fast | pro（默认 pro）
	UILang       string   `json:"ui_lang"`       // 界面语言（决定中/英文提示词那一套；缺省按中文）
	// ImageFiles 本次工单含图片/扫描件的件数：>0 时计入 OCR 理解/纠错的预估 allowance。
	// 缺省 0 ⇒ 老客户端行为完全不变（不新增任何必填字段）。
	ImageFiles int `json:"image_files"`
}

// handleTranslationEstimate 报价预估接口（登录用户，纯只读）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 见 estimateReq）。
// 返回: success=true 时携带：
//
//	points_min/points_max=预估消耗区间（积分口径，已含均摊系数）；cost_sentences_approx≈句数换算；
//	points_balance=当前积分余额；balance_sentences_approx=余额≈句数；
//	activated=租户是否已开通；hint=未开通提示。
//	★ 2026-09-19 积分口径：token 裸值出参（tokens_min/tokens_max/balance_tokens）全部下线。
func (s *Server) handleTranslationEstimate(w http.ResponseWriter, r *http.Request) {
	// 用户鉴权
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req estimateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	// mode 归一与扣费腿同一个函数（fast 之外一律 pro，别让两侧对「未知档」的理解不一样）
	mode := normalizeTaskMode(req.Mode)
	tid := s.effTenant(r, u)
	minTokens, maxTokens := s.estimateTokens(req, mode)
	// markup 与扣费腿同一个解析口（租户策略全局值 ＋ 该模式因子，模式因子优先）
	markup := modeMarkup(s.effectivePolicyCached(tid), mode)
	minTokens = applyMarkup(minTokens, markup)
	maxTokens = applyMarkup(maxTokens, markup)
	// 句数读数仍按「上限 ÷ 每句汇率」给（这一档是**换算展示**，不是成本来源）
	rate := s.Store.TokenSentenceRate()
	if rate <= 0 {
		rate = 1
	}
	// 获取当前租户 token 余额（双桶可用总额）
	_, _, tokens, approxBal := s.balancePayload(tid)
	// 检查租户是否已开通（有套餐或余额）
	activated := true
	hint := ""
	if tid > 0 {
		perms, err := s.Store.GetTenantPerms(tid)
		if err == nil && perms.PackageCode == "" && tokens <= 0 {
			activated = false
			// ★ 任务2.5：耗尽引导——购买月租套餐或充值永久 token
			hint = "额度已用尽，请购买订阅套餐或充值积分"
		}
	}
	// 返回预估结果（★ 积分口径唯一出参，token 折算仅发生在内部）
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		// sentences 字段保留为「句/段数读数」：出参形状不变，老客户端零改动；
		// 它**不再参与**积分计算（参与计算就是本次要修的缺陷本体）。
		"sentences": estimateUnitCount(req),
		// ★ 假成功判据补强（§一·3「成功类判据要同时问内容体非空」同口径）：
		//   points_min 恒 0 的报文和「没算」长得一样，所以这里保底 1 积分——
		//   任何一次真实翻译都不可能是零消耗（至少一次模型调用），报 0 就是算错了。
		"points_min":               atLeastOne(s.Store.PointsFromTokens(minTokens)),
		"points_max":               atLeastOne(s.Store.PointsFromTokens(maxTokens)),
		"cost_sentences_approx":    maxTokens / rate, // 上限≈句数（保守展示）
		"points_balance":           s.Store.PointsFromTokens(tokens),
		"balance_sentences_approx": approxBal,
		"sufficient":               !s.Bill.Enabled() || tokens > 0,
		"activated":                activated,
		"hint":                     hint,
		// ★ 决策⑪①（2026-10-10）：LLM 排队深度代理值（饱和代理，口径见 llmQueueDepth 注释）。
		//   前端 slowEstimate 用它参与「字符数 × 模式 × 排队深度」三档预检；字段缺失时前端按保守档兜底。
		"llm_queue_depth": s.llmQueueDepth(),
	})
}

// estimateTokens 按请求形态选一条预估腿（有正文＝即时翻译形态，只有段数＝文件/工单形态）。
// 参数 req: 已解析入参；mode: 归一后的模式档。返回: (真实 token 下限, 上限)，两者都 ≥1。
func (s *Server) estimateTokens(req estimateReq, mode string) (int64, int64) {
	var minTokens, maxTokens int64
	if req.Text != "" {
		minTokens, maxTokens = engine.EstimateChatTokens(req.Text, req.TargetLangs, mode, "", req.UILang)
	} else {
		minTokens, maxTokens = engine.EstimateSegmentTokens(req.SegmentCount, req.TargetLangs, mode, "", req.UILang)
	}
	// ★ 2026-10-28 主方案 §8.5：图片/扫描件工单会多一条 ③（OCR 判定补偿）的模型调用，
	//   预估必须与实扣同一把尺子（AGENTS §一·5④）。上限口径见 engine.OcrFixAllowanceTokens。
	if allow := engine.OcrFixAllowanceTokens(req.ImageFiles); allow > 0 {
		minTokens += allow
		maxTokens += allow
	}
	return minTokens, maxTokens
}

// applyMarkup 把真实 token 口径的预估折算成实收口径（与扣费腿同一乘方、同一兜底方向）。
// 参数 tokens: 预估真实 token；markup: modeMarkup 解析出的生效系数。
// 返回: 预估实收 token（不低于原值——系数异常时的兜底方向与 ChargeUsageRealtime 里
// 「billed<total 就按 total 计」那条一致，宁可多估也不让客户备钱备少）。
func applyMarkup(tokens int64, markup float64) int64 {
	if tokens <= 0 {
		return 0
	}
	billed := int64(float64(tokens) * markup)
	if billed < tokens {
		billed = tokens
	}
	return billed
}

// atLeastOne 对外积分读数的下限保底。
// 参数 n: 折算出的积分。返回: n<1 时回 1，否则原值。
//
// 为什么保底而不是报 0：预估报 0 与「这条腿压根没算」在报文上同形（F-49 那一族就是
// 恒零报文被客户当成免费），而真实链路不可能零消耗——至少一次模型调用。
func atLeastOne(n int64) int64 {
	if n < 1 {
		return 1
	}
	return n
}

// estimateUnitCount 出参 sentences 的读数（纯展示，不进任何金额/积分算式）。
// 参数 req: 已解析入参。返回: 句数（有正文时）或段数（文件场景），至少 1。
//
// ⚠️ 这里**仍然**用 countSentences，但只当读数：预估的积分一律走 engine.EstimateChatTokens，
// 句数不进成本公式（本函数只是出栈给客户看的「大约几句」）。
// ⚠️ 归因订正（2026-10-13 实读代码核正）：旧注释写着"它还服务「余额≈句数」换算与免费额度发放，
// 所以本批不动它的判据"——那两个读点其实**都不读这里**（余额句数走 balancePayload 的 rate 除法，
// 试用额度按请求计次，套餐额度取 packages.sentences 固定值）。所以切分判据已在 sentence_count.go 重做，
// 不必再为那两处把这一维锁死；改动只在展示读数这一维生效。
func estimateUnitCount(req estimateReq) int64 {
	n := req.SegmentCount
	if req.Text != "" {
		n = countSentences(req.Text)
	}
	if n < 1 {
		n = 1
	}
	return n
}
