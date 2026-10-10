// ============ 本文件职责中文说明 ============
// 文本翻译主流程（复刻 skill.py _handle_text_translate）：面向对话/文本翻译入口。
// 从 options 与用户 prompt 解析目标语言（KB 语言 + 其他语言），
// 知识库语言走 TranslateOne 四段匹配（命中标注 kb 来源），其他语言走纯模型并发翻译，
// 支持 fast/pro 双模式与基于 ctx 的进度回调，最终合并所有语言译文、
// 经 AI 校对后构建展示 reply 与结构化 TextTranslateData 返回。
// ========================================
package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"translator/internal/config"
	"translator/internal/gate"
	"translator/internal/observability"
	"translator/internal/tenant"
)

// Progress 进度回调（step=阶段描述，done/total=当前/总步数）
type Progress func(step string, done, total int)

// TextTranslateResult 文本翻译结果（ChatResponse 契约）
type TextTranslateResult struct {
	Skill string            `json:"skill"` // 能力标识（固定 "translation"）
	Reply string            `json:"reply"` // 面向用户的完成话术（含译文与模式说明）
	Data  TextTranslateData `json:"data"`  // 结构化翻译数据
	Files []string          `json:"files"` // 关联文件（文本翻译通常为空）
	Error string            `json:"error"` // 失败原因（成功时为空）
	// ★ 2026-09-03 需求：每次翻译结果携带实际用量（全链路真实用量）。
	// 2026-09-19 积分口径：token 裸值仅内部计量（不外发），对外只出 points_used。
	TokensUsed int64 `json:"-"`
	PointsUsed int64 `json:"points_used"`
}

// TextTranslateData 文本翻译结构化数据
type TextTranslateData struct {
	Translations       map[string]string `json:"translations"`        // 目标语言 → 译文
	TranslationsSource map[string]string `json:"translations_source"` // 目标语言 → 来源（kb/model）
	LangNames          map[string]string `json:"lang_names"`          // 目标语言代码 → 中文名
	KBLangs            []string          `json:"kb_langs"`            // 走知识库翻译的目标语言
	OtherLangs         []string          `json:"other_langs"`         // 走纯模型翻译的其他语言
	SourceText         string            `json:"source_text"`         // 实际被翻译的原文（剥离指令后）
	Mode               string            `json:"mode"`                // 命中模式描述（精确命中/纯模型等）
	GateWarnings       []string          `json:"gate_warnings"`       // 整改 R1：主路径输出质量/文化闸门警告
	Similarity         *float64          `json:"similarity"`          // 语义命中的相似度（未命中为 nil）
	MatchedZH          string            `json:"matched_zh"`          // 命中的知识库中文原文
	TargetLangs        []string          `json:"target_langs"`        // 全部目标语言（KB + 其他）
}

// TargetLangsFromOptions 从 options 提取语言列表
func TargetLangsFromOptions(options map[string]interface{}) []string {
	if options == nil {
		return nil
	}
	v, ok := options["target_langs"]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		parts := strings.Split(t, ",")
		var out []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

// ModeFromOptions 从 options 提取翻译模式："fast"=快速模式；""/"pro"=专业校对模式。
func ModeFromOptions(options map[string]interface{}) string {
	if options == nil {
		return ""
	}
	m, _ := options["mode"].(string)
	return strings.ToLower(strings.TrimSpace(m))
}

// ModeBadgeLabel 模式徽标后缀（拼在结果「📊 模式：…」与 OpenAPI mode 出参尾部，一处定死）。
// 参数 fast: true=快速模式，false=专业校对模式。返回: 以 " | " 开头的后缀串。
// ★ F-50②（〇-U 批 I-4，2026-09-26 UAT）：旧写法在同一行里就地拼字面量，且描述自相矛盾——
// 快速模式写着「AI初翻+校对」（＝和 pro 一样含校对，区别无从解释），而 text.go:343 的注释
// 与 orchestrator/flow.go:282 的开关逻辑说的是第三套（fast＝初翻+校对+质检，关掉知识库直配、
// 质量评估、文化闸、自迭代）。现在文案按**流水线真实差异**写，并收敛成唯一常量：
// 要改口径只有一个地方可改，也不会再出现「三处说法」。
func ModeBadgeLabel(fast bool) string {
	if fast {
		return " | ⚡快速模式（初翻+校对+质检，不走知识库直配与质量评估）"
	}
	return " | 🎓专业校对模式（全流水线：知识库直配+初翻+校对+质量评估+文化闸）"
}

// SplitOptions 分离 KB / other / directOther
func SplitOptions(langs []string) (kbTarget, directOther []string, hasOther bool) {
	for _, lc := range langs {
		if lc == "other" {
			hasOther = true
		} else if IsKBLang(lc) {
			kbTarget = append(kbTarget, lc)
		} else {
			directOther = append(directOther, lc)
		}
	}
	return
}

// normalizeBrandTerms 品牌术语归一化（对话路径品牌统一输出，2026-09-10 需求）：
// 源文命中 KB module=brand 术语（layer=1，如 极石→ROX）时，对每个目标语言译文执行
// gate.NormalizeBrandTerm 剥离「品牌名+车辆类后缀」自创组合（ROX vehicles/motor/автомобиль 等），
// 使品牌名一律等于术语规定译法。源文无术语/未启用平台存储/无 hits 时原样返回。
// 返回命中的品牌术语数量（>0 说明做过归一化扫描）。
func (e *Engine) normalizeBrandTerms(ctx context.Context, srcText string, langTranslations map[string]string, srcLang string) int {
	if e.St == nil || strings.TrimSpace(srcText) == "" || len(langTranslations) == 0 {
		return 0
	}
	tid := tenant.FromContext(ctx)
	var orgID int64
	if uid := tenant.UserFromContext(ctx); uid > 0 {
		if u, uerr := e.St.GetUser(uid, tid); uerr == nil && u != nil {
			orgID = u.OrgID
		}
	}
	if tid <= 0 {
		tid = 1 // 品牌主站根租户发型场景（与 KB 查询口径对齐：ticket 侧用有效租户）
	}
	ents, err := e.St.FindTermsBySubstring(tid, orgID, srcLang, srcText)
	if err != nil || len(ents) == 0 {
		return 0
	}
	// 仅保留 brand 模块 + layer=1 的品牌术语
	brandByLang := map[string]string{} // target_lang → 规定译法（品牌名）
	for _, ent := range ents {
		if ent == nil || ent.Module != "brand" || ent.Layer != 1 {
			continue
		}
		if ent.TargetLang == "" || strings.TrimSpace(ent.TargetText) == "" {
			continue
		}
		if _, ok := brandByLang[ent.TargetLang]; !ok {
			brandByLang[ent.TargetLang] = strings.TrimSpace(ent.TargetText)
		}
	}
	if len(brandByLang) == 0 {
		return 0
	}
	hit := 0
	for lc, tr := range langTranslations {
		brand, ok := brandByLang[lc]
		if !ok || strings.TrimSpace(tr) == "" {
			continue
		}
		if norm := gate.NormalizeBrandTerm(tr, brand); norm != tr {
			langTranslations[lc] = norm
			hit++
		}
	}
	return hit
}

// HandleText 文本翻译主流程（复刻 skill.py _handle_text_translate）
// HandleText 文本/对话翻译统一入口（★ S8：敏感词双向兑底闸包一层，核心流程在 handleTextCore）。
func (e *Engine) HandleText(ctx context.Context, text string, options map[string]interface{}, prog Progress) *TextTranslateResult {
	// 输入侧：整段命中直接拒译（不进模型、不扣费）
	// ★ F-53（批 I-8）：Error 用导出常量 CodeSensitiveBlocked（值不变，仍是 "sensitive_blocked"），
	//   消费方要按码分支，不能再拿裸字面量比对。人类文案在 Reply 里，由调用方按码取用。
	if msg := e.sensitiveTextGuardInput(ctx, text); msg != "" {
		return &TextTranslateResult{Skill: "translation", Reply: msg, Error: CodeSensitiveBlocked}
	}
	res := e.handleTextCore(ctx, text, options, prog)
	// ★ 修法 F（2026-10-04 〇-AR 第 2 波 · 缺陷 ⑭）：出口先收敛「空壳成功」——
	//   模型/知识库全腿没产出一条可用译文时，core 只会「跳过空译文不拼接」，Error 恒为空，
	//   于是对话发 done 空壳、OpenAPI 回 success:true。判据与错误码见 upstream_failure.go。
	//   与下面那道输出侧敏感词闸**互斥**（那道闸的进入条件写着 len(Translations)>0，
	//   而本收敛的前提是「一条非空译文都没有」），所以两条的先后不改变行为；
	//   写在这里只为了读代码的人一眼看到「失败诚实性」这一层在合规闸之前就已经完成。
	//   反证＝把这一行摘掉：A7 与 api 侧的 ⑭ 回归锁一起红（回到空壳报成功）。
	e.convergeEmptyResult(ctx, res)
	// 输出侧兑底：模型自产敏感内容整单拒付（文本通道为单块交付，不做段级替换）
	if res != nil && res.Error == "" && len(res.Data.Translations) > 0 {
		if msg := e.sensitiveTextGuardOutput(ctx, res.Data.Translations); msg != "" {
			res.Data.Translations = nil
			res.Reply = msg
			res.Error = CodeSensitiveBlocked
		}
	}
	return res
}

// handleTextCore 文本/对话翻译核心流程（模型调用、用量记账、记忆与 TM 缓存）。
// 合规闸与熔断在 HandleText 外层已处理，此处只做业务。
func (e *Engine) handleTextCore(ctx context.Context, text string, options map[string]interface{}, prog Progress) *TextTranslateResult {
	// 注入请求级用量记录器（供计量成本核算）
	ctx = e.WithUsageRecorder(ctx)
	// 界面语言（提示词语言跟随用户界面语言）：options["lang"] 缺省按中文
	if l, ok := options["lang"].(string); ok && l != "" {
		ctx = WithUILang(ctx, l)
	}
	// ★ 缩翻（任务7）：options["max_length"]>0 时启用最长字符限制
	if n := maxLengthOption(options); n > 0 {
		ctx = WithMaxLength(ctx, n)
	}
	if prog == nil {
		prog = func(string, int, int) {}
	}
	if strings.TrimSpace(text) == "" {
		return &TextTranslateResult{Skill: "translation", Reply: "请输入要翻译的文本"}
	}
	// ★ 租户可用性校验
	if err := e.tenantOK(ctx); err != nil {
		return &TextTranslateResult{Skill: "translation", Reply: "❌ " + err.Error()}
	}

	langs := TargetLangsFromOptions(options)
	kbTarget, directOther, hasOther := SplitOptions(langs)
	cleanText := text
	// 实际源语言：优先用户显式指定（source_lang），否则自动检测
	srcLang := DetectSourceLang(cleanText)
	if sl, ok := options["source_lang"].(string); ok && sl != "" {
		srcLang = sl
	}

	// 未指定语言 → 从 prompt 解析
	if len(kbTarget) == 0 && len(directOther) == 0 {
		clean, parsed := StripLangInstruction(text)
		if len(parsed) > 0 {
			kbTarget, directOther, hasOther = SplitOptions(parsed)
			cleanText = clean
		} else {
			kbTarget = []string{"en"}
		}
	}
	// 选了 other 但无法解析 → 从 prompt 解析其他语言
	if hasOther && len(directOther) == 0 {
		parsed, cleaned := e.parseOtherLangsFromPrompt(ctx, cleanText)
		if len(parsed) == 0 {
			return &TextTranslateResult{
				Skill: "translation",
				Reply: "你选择了「其他语言」，但没告诉我翻译成什么语言。请输入类似「翻译成泰语：xxx」",
			}
		}
		directOther = parsed
		cleanText = cleaned
	}

	// ★ 双模式：fast 快速模式跳过知识库匹配——全部目标语言并入纯模型直翻
	fast := ModeFromOptions(options) == "fast"
	if fast && len(kbTarget) > 0 {
		directOther = append(directOther, kbTarget...)
		kbTarget = nil
	}

	// ★ 翻译前品牌保护（2026-09-11）：与文件路径对齐，在翻译前把源文品牌名替换为规定译法，
	//   避免 LLM 音译（如 极石→جيشي），仅对 KB 语言（有品牌术语的语言）生效。
	brandTermsAll := e.fetchBrandTerms(ctx, cleanText)
	protectedTexts := map[string]string{} // lang → protected source text
	if len(brandTermsAll) > 0 {
		for lc, terms := range brandTermsAll {
			if prot, changed := protectSourceByLang([]string{cleanText}, terms); changed {
				protectedTexts[lc] = prot[0]
			}
		}
	}

	// KB 翻译
	kbResult := &TranslateResult{Translations: map[string]string{}, Mode: "模型翻译（无知识库）"}
	kbSrc := map[string]string{}
	if len(kbTarget) > 0 {
		prog("知识库匹配中...", 1, 4)
		// ★ 整改 D2：阶段回调经 ctx 传递（替代 Engine.OnPhase 单例字段——并发请求
		//   共享字段读改写会竞争且回调串台，go test -race 必报）
		kbCtx := WithProgressCallback(ctx, func(phase string) {
			if phase == "ai_generating" {
				prog("AI生成中...", 2, 4)
			}
		})
		// ★ 品牌保护：如果有保护后的源文，使用第一个 KB 语言的保护版本
		kbText := cleanText
		for _, lc := range kbTarget {
			if prot, ok := protectedTexts[lc]; ok {
				kbText = prot
				break
			}
		}
		kbResult, _ = e.TranslateOne(kbCtx, kbText, kbTarget, false, config.StageKBMatch)
		for lc := range kbResult.Translations {
			src := "model"
			if kbResult.MatchedZH != "" {
				src = "kb"
			}
			kbSrc[lc] = src
		}
	}

	// 其他语言 → 纯模型（并发）
	otherTr := map[string]string{}
	otherNames := map[string]string{}
	for i, code := range directOther {
		name := config.LangNames[code]
		if name == "" {
			name = code
		}
		otherNames[code] = name
		prog(fmt.Sprintf("AI翻译%s...", name), 2+i, 4+len(directOther))
	}
	if len(directOther) > 0 {
		// 并发限制（信号量）：最多 3 路并发调用其他语言模型翻译，避免打爆 LLM API
		const maxConcurrent = 3
		sem := make(chan struct{}, maxConcurrent)
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, code := range directOther {
			wg.Add(1)
			go func(lc string) {
				defer wg.Done()
				defer recoverPipeline("chat_other:" + lc) // 整改 D4
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				// ★ 品牌保护：使用保护后的源文（如有）
				srcText := cleanText
				if prot, ok := protectedTexts[lc]; ok {
					srcText = prot
				}
				tr, _ := e.TranslateOtherLang(ctx, srcText, lc, srcLang, config.StageAIInitial)
				mu.Lock()
				otherTr[lc] = tr
				mu.Unlock()
			}(code)
		}
		wg.Wait()
	}

	prog("翻译完成", 4, 4)

	// 合并
	allTr := map[string]string{}
	allSrc := map[string]string{}
	for lc, v := range kbResult.Translations {
		allTr[lc] = v
		allSrc[lc] = kbSrc[lc]
	}
	for lc, v := range otherTr {
		allTr[lc] = v
		allSrc[lc] = "model"
	}

	// ★ 校对 Agent：初翻结果逐语言审校修正（fast/pro 均含校对环节；3 路并发限流）
	if len(allTr) > 0 {
		prog("AI 校对中...", 3, 4)
		sem := make(chan struct{}, 3)
		var mu sync.Mutex
		var wg sync.WaitGroup
		// ★ 〇-Z（2026-09-28）：先把待校对的语言**快照成切片**再并发，range 期间不得有
		//   goroutine 往 allTr 里写。旧写法是 `for lc, tr := range allTr { go func(){ allTr[lc]=… } }`：
		//   mu 只挡住了校对线程彼此，挡不住「主线程还在 range、已放出去的线程在写同一张 map」，
		//   而 map 的「边遍历边写」在 Go 里是 **runtime fatal error（concurrent map iteration and
		//   map write）**——不是 panic，recoverPipeline 兜不住，整个进程直接挂。
		//   线上没炸是因为真模型调用要几百毫秒到几秒，range 早就跑完了；
		//   单测/UAT 的本地假上游瞬间返回，每次都撞在窗口里（-race 稳定复现，见
		//   internal/api/trial_test.go 与 internal/engine/review_snapshot_test.go）。
		type reviewJob struct {
			lang string
			text string
		}
		jobs := make([]reviewJob, 0, len(allTr))
		for lc, tr := range allTr {
			if strings.TrimSpace(tr) == "" {
				continue
			}
			jobs = append(jobs, reviewJob{lang: lc, text: tr})
		}
		for _, j := range jobs {
			wg.Add(1)
			go func(lc, tr string) {
				defer wg.Done()
				defer recoverPipeline("chat_review:" + lc) // 整改 D4
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				if revised := e.ReviewTranslation(ctx, cleanText, tr, lc, config.StageReview); strings.TrimSpace(revised) != "" {
					mu.Lock()
					allTr[lc] = revised
					mu.Unlock()
				}
			}(j.lang, j.text)
		}
		wg.Wait()
	}

	// ★ 品牌术语归一化收尾（2026-09-10 需求，放在 AI 校对与重翻之后）：
	//   源文命中 KB 品牌术语（极石/极石汽车→ROX）时，对每个目标语言译文剥离
	//   「ROX vehicles/motor/автомобиль」等自创后缀，统一为纯品牌名。
	//   必须在校对之后做最终约束——校对 agent 可能把前半程归一化结果改回带后缀，
	//   故此处为硬收尾，确保交给约束闸门与用户的是品牌名统一的译文。
	if hit := e.normalizeBrandTerms(ctx, cleanText, allTr, srcLang); hit > 0 {
		log.Printf("[brandterm] 对话路径品牌术语归一化 %d 个语言译文", hit)
	}

	// 整改 R1：文本主翻译路径统一走约束闸门 + 语言文化闸门（pro 模式可带反馈重翻）
	gateWarnings := e.applyOutputGates(ctx, cleanText, allTr, !fast)

	// ★ 14.3 输出纯度收尾（2026-10-10）：剥离译文中「全角括号原词复述」——提示词层
	//   已加约束（translateInstruction / ReviewTranslation），此处是兜底腿。判据见
	//   StripParenEcho（整括号逐字复述 + 数字序列不变，词级匹配会误杀标识符）。
	//   日志走 observability.Warn 而非 log.Printf（棘轮闸门：internal 基线只减不增）。
	for lc, tr := range allTr {
		if strings.TrimSpace(tr) == "" {
			continue
		}
		clean, reason := StripParenEcho(tr, cleanText)
		if reason == "" {
			continue
		}
		allTr[lc] = clean
		recordPurityAction(lc, reason)
		observability.Warn(ctx, "译文全角括号复述原词，已按纯度判据处理",
			"lang", lc, "reason", reason, "sample", clipRunes(tr, 80))
	}

	langNames := map[string]string{}
	for lc := range allTr {
		langNames[lc] = config.LangNames[lc]
	}
	for lc, n := range otherNames {
		langNames[lc] = n
	}

	// 构建 reply（★ 14.3/14.4 输出净化，2026-10-10）：回复体只保留译文本体——
	//   「📝 「原文」翻译结果：」「📊 模式：…」「⚡ …积分」三段全部撤出内容。
	//   原文走结构化字段 Data.SourceText（前端「查看原文」消费），模式走 Data.Mode
	//   （前端徽标渲染），积分走 PointsUsed / points_used 报文（前端气泡底部元信息行）。
	//   口径锁：upstream_failure_test.go 仍断言 Reply 不含「翻译结果：」，方向一致。
	order := kbTarget
	order = append(order, directOther...)
	var bodyLines []string
	for _, lc := range order {
		if v, ok := allTr[lc]; ok && strings.TrimSpace(v) != "" {
			if len(order) == 1 {
				// 单目标语：只给译文本体，不带语言名前缀
				bodyLines = append(bodyLines, v)
			} else {
				// 多目标语：每行「语言名：译文」，不再带 🤖 尾缀
				bodyLines = append(bodyLines, fmt.Sprintf("%s：%s", langNames[lc], v))
			}
		}
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(bodyLines, "\n"))

	modelCount := 0
	for _, src := range allSrc {
		if src == "model" {
			modelCount++
		}
	}
	mode := kbResult.Mode
	if mode == "" {
		mode = "模型翻译（无知识库）"
	}
	if modelCount > 0 {
		mode = "纯模型翻译"
	}
	kbHitName := ""
	for lc, src := range allSrc {
		if src == "kb" {
			kbHitName = config.LangNames[lc]
			break
		}
	}
	if kbHitName != "" {
		mode = mode + " | " + kbHitName + " 命中知识库"
	}
	// ★ 模式标注（前台徽标与 OpenAPI 出参用）——文案收敛到 ModeBadgeLabel 一处常量（F-50②）
	mode += ModeBadgeLabel(fast)

	// ★ 2026-09-19 积分口径 / ★ F-50①（〇-U 批 I-4）：积分不再拼进 Reply 文案
	//   （14.4 复制净化：复制按钮只取结构化 translations），仅保留结构化出参——
	//   取**实收**口径（扣费现场累计，F-49①），与报文 points_used 同值。
	tp, tc := e.UsageTokens(ctx)
	rawTokens := tp + tc // 仅供内部计量字段（json:"-"），不进任何对外文案
	billed := e.UsageDisplayTokens(ctx)

	if len(gateWarnings) > 0 {
		sb.WriteString("\n\n⚠️ 质量校验提示：\n" + strings.Join(gateWarnings, "\n"))
	}

	// ★ 2026-09-10 改进1：实时计费余额不足中止时，向用户明确提示（不再静默缺失语言）。
	// 此前 abort() 直接取消 ctx，对话回复只见成功语言、缺失语言无任何说明，
	// 用户误以为翻译故障。现把中止原因（store.ErrInsufficientBalance 的面向用户文案）
	// 追加到回复尾部，并追加到 gateWarnings 供前端结构化展示。
	if reason := abortReasonFrom(ctx); reason != "" {
		hint := fmt.Sprintf("⚠️ %s：本次翻译可能不完整，请充值后重新发送。", reason)
		sb.WriteString("\n\n" + hint)
		gateWarnings = append(gateWarnings, hint)
	}

	var sim *float64
	if kbResult.Similarity > 0 {
		s := kbResult.Similarity
		sim = &s
	}

	return &TextTranslateResult{
		Skill: "translation",
		Reply: sb.String(),
		Data: TextTranslateData{
			Translations:       allTr,
			TranslationsSource: allSrc,
			LangNames:          langNames,
			KBLangs:            kbTarget,
			OtherLangs:         directOther,
			SourceText:         cleanText,
			Mode:               mode,
			Similarity:         sim,
			MatchedZH:          kbResult.MatchedZH,
			TargetLangs:        append(append([]string{}, kbTarget...), directOther...),
			GateWarnings:       gateWarnings,
		},
		TokensUsed: rawTokens,
		// ★ F-49①：对外积分一律按**实收**口径（与 points_used 出参、台账扣费同源），
		// 不再用裸真实用量折算——后者比实收少一个 markup，客户按报文折算必然对不上。
		PointsUsed: e.PointsOfTokens(billed),
	}
}
