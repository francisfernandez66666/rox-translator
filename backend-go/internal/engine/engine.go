// ============ 本文件职责中文说明 ============
// 翻译引擎核心：复刻 Python lib.py 的 translate_one 主流程，负责
// 多租户翻译的完整调度链路——知识库(KB)四段匹配（精确命中 / CJK标点无关精确 /
// 模糊子串 / 语义高相似+例句参考）、模型路由（按权重选主模型并按权重降序降级）、
// 主模型熔断降级与冷却自动恢复、429/网络错误降级重试、并发逐语言翻译、
// 批量翻译（文件用，<sN> 标记解析）、审校/驳回重译、截断自修复、
// 以及请求级实际用量（供应商/模型）计量记录与错误率监控。
// ========================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"translator/internal/config"
	"translator/internal/evals"
	"translator/internal/kb"
	"translator/internal/llm"
	"translator/internal/store"
	"translator/internal/tenant"
)

// Engine 翻译引擎（复刻 lib.py translate_one 核心流程）
type Engine struct {
	Cfg   *config.Config   // 全局配置（模型/路由/相似度阈值/目录等）
	LLM   *llm.Client      // LLM 客户端（Chat 对话 / Embed 向量调用）
	DB    *kb.KBDatabase   // 知识库数据库（精确/模糊/译文对/多租户行查询）
	Index *kb.Index        // 语义检索索引（向量 + 目标语言过滤 + 租户隔离）
	Ten   *tenant.Store    // 租户存储（查询租户级模型配置与策略阈值）
	St    *store.Store     // 平台存储（读取 system_config：模型路由/阶段模型，可选）
	Evals *evals.Evaluator // 评估器（质量评估用，可选）

	cjkCache         map[string]int64           // 兼容旧字段（保留）：默认租户 CJK→rowID 缓存
	cjkCacheByTenant map[int64]map[string]int64 // 租户 → CJK 字符串 → row id（按租户懒加载）
	cjkMu            sync.Mutex                 // 保护 CJK 缓存的并发读写锁

	// OnPhase 阶段回调（可选）：KB 匹配完成 → 进入 AI 生成前触发 "ai_generating"
	OnPhase func(phase string)

	// ★ 主模型熔断恢复：Hunyuan 连续失败超阈值 → 直接降级；冷却后自动恢复
	breaker *Breaker

	// ★ 错误率监控：记录最近 N 次 LLM 调用成败（看门狗/告警用）
	errMu      sync.Mutex
	errRing    []bool // true=成功 false=失败
	errRingCap int
}

// tenantID 从 ctx 取租户 id；未指定时回退默认租户 rox（id=1）
func (e *Engine) tenantID(ctx context.Context) int64 {
	if id := tenant.FromContext(ctx); id > 0 {
		return id
	}
	return 1
}

// uiLangKey 界面语言 context 键（提示词语言跟随用户界面语言）
type uiLangKey struct{}

// WithUILang 把界面语言写入 context（"zh"=中文提示词，其他=英文提示词）。
func WithUILang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, uiLangKey{}, lang)
}

// uiLangFromCtx 从 context 读取界面语言；未设置时返回 "zh"（默认中文提示词）。
func uiLangFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(uiLangKey{}).(string); ok && v != "" {
		return v
	}
	return "zh"
}

// usageCtxKey 请求级"实际使用的 LLM 供应商/模型"记录键（供计量成本核算）
type usageCtxKey struct{}

// usageRecord 可变的请求级记录（context 存指针，跨调用共享）
type usageRecord struct {
	provider string // 实际使用的供应商标识（如 siliconflow/volcengine）
	model    string // 实际使用的模型名
	used     bool   // 是否已记录过实际用量（避免回退默认）
}

// WithUsageRecorder 向 ctx 注入用量记录器（API 层在进入翻译前调用）
func (e *Engine) WithUsageRecorder(ctx context.Context) context.Context {
	return context.WithValue(ctx, usageCtxKey{}, &usageRecord{})
}

// NoteUsageModel 记录本次实际使用的供应商与模型（单语翻译成功路径调用）
func (e *Engine) NoteUsageModel(ctx context.Context, provider, model string) {
	if rec, ok := ctx.Value(usageCtxKey{}).(*usageRecord); ok {
		rec.provider = provider
		rec.model = model
		rec.used = true
	}
}

// UsageModel 返回本次请求实际使用的供应商与模型；未记录时回退租户配置/全局默认
func (e *Engine) UsageModel(ctx context.Context) (provider, model string) {
	if rec, ok := ctx.Value(usageCtxKey{}).(*usageRecord); ok && rec.used {
		return rec.provider, rec.model
	}
	// 未记录：租户配置优先，其次全局（路由策略主模型）
	if e.Ten != nil {
		if mc, err := e.Ten.GetModelConfig(e.tenantID(ctx)); err == nil && mc.Model != "" {
			return "tenant", mc.Model
		}
	}
	if len(e.Cfg.ModelRoutes) > 0 {
		p := e.pickPrimaryRoute()
		return p.Provider, p.Model
	}
	return "global", e.Cfg.OnlineModel
}

// BreakerOpen 主模型是否处于熔断状态（监控告警用）
func (e *Engine) BreakerOpen() bool {
	return e.breaker.IsOpen()
}

// NoteLLMResult 记录一次 LLM 调用结果（成功/失败），用于错误率监控
func (e *Engine) NoteLLMResult(ok bool) {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	if e.errRingCap == 0 {
		e.errRingCap = 100
	}
	e.errRing = append(e.errRing, ok)
	if len(e.errRing) > e.errRingCap {
		e.errRing = e.errRing[len(e.errRing)-e.errRingCap:]
	}
}

// ErrorRate 返回最近窗口内 LLM 调用错误率（0.0~1.0）；无样本返回 0
func (e *Engine) ErrorRate() float64 {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	if len(e.errRing) == 0 {
		return 0
	}
	fails := 0
	for _, ok := range e.errRing {
		if !ok {
			fails++
		}
	}
	return float64(fails) / float64(len(e.errRing))
}

// tenantOK 校验租户是否可用（存在且未禁用/未过期）。返回错误信息；nil 表示可用。
func (e *Engine) tenantOK(ctx context.Context) error {
	if e.Ten == nil {
		return nil
	}
	t, err := e.Ten.GetByID(e.tenantID(ctx))
	if err != nil {
		return fmt.Errorf("租户不存在")
	}
	if t.Status == tenant.StatusDisabled {
		return fmt.Errorf("租户已停用")
	}
	if t.Status == tenant.StatusExpired {
		return fmt.Errorf("租户已过期")
	}
	return nil
}

// Breaker 主模型熔断器（全局共享，跨请求生效）
type Breaker struct {
	mu         sync.Mutex    // 保护熔断状态的互斥锁
	failures   int           // 当前连续失败计数
	open       bool          // 是否处于熔断状态
	openedAt   time.Time     // 熔断开启时刻（用于计算冷却剩余时间）
	threshold  int           // 触发熔断的连续失败阈值
	coolDown   time.Duration // 熔断后冷却时长，冷却结束后进入半开试探
	tripReason string        // 触发熔断时的原因（便于排查）
}

// NewBreaker 创建熔断器。threshold=触发熔断的连续失败数；coolDown=熔断后冷却时间
func NewBreaker(threshold int, coolDown time.Duration) *Breaker {
	return &Breaker{threshold: threshold, coolDown: coolDown}
}

// Fail 记录一次主模型失败。达到阈值则熔断，返回是否触发熔断
func (b *Breaker) Fail(reason string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open {
		return false
	}
	b.failures++
	if b.failures >= b.threshold {
		b.open = true
		b.openedAt = time.Now()
		b.tripReason = reason
		return true
	}
	return false
}

// Reset 主模型成功时清零失败计数（若已熔断则恢复）
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	if b.open {
		b.open = false
		b.tripReason = ""
	}
}

// IsOpen 是否处于熔断状态（熔断中主模型直接跳过）
func (b *Breaker) IsOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open && time.Since(b.openedAt) >= b.coolDown {
		// 冷却结束：半开状态，允许试探主模型
		b.open = false
		b.failures = 0
		b.tripReason = ""
		return false
	}
	return b.open
}

// Stats 熔断状态摘要（调试用）
func (b *Breaker) Stats() (open bool, failures, threshold int, reason string, left time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	left = 0
	if b.open {
		left = b.coolDown - time.Since(b.openedAt)
		if left < 0 {
			left = 0
		}
	}
	return b.open, b.failures, b.threshold, b.tripReason, left
}

// TranslateResult 单条翻译结果
type TranslateResult struct {
	Mode         string            // 本次命中模式（精确命中/模糊匹配/语义命中/纯模型翻译等）
	MatchedZH    string            // 命中的知识库中文原文（未命中时为空）
	Similarity   float64           // 语义命中的相似度得分（未命中为 0）
	Translations map[string]string // 各目标语言 → 译文（KB 命中部分 + 模型生成部分）
	Candidates   []*kb.Row         // 模糊/语义检索候选行（供展示或例句参考）
	TargetLangs  []string          // 请求的目标语言列表
	NeedModel    []string          // 仍需要模型生成的目标语言（KB 未覆盖的部分）
	Examples     []*kb.Row         // 参考例句（语义相近但非同一句时收集）
}

// NewEngine 创建引擎
func NewEngine(cfg *config.Config, db *kb.KBDatabase, idx *kb.Index, ts *tenant.Store) *Engine {
	threshold := 5
	coolDown := 30 * time.Minute
	if cfg.BreakerThreshold > 0 {
		threshold = cfg.BreakerThreshold
	}
	if cfg.BreakerCoolDownSec > 0 {
		coolDown = time.Duration(cfg.BreakerCoolDownSec) * time.Second
	}
	// 构建语义检索的语言过滤映射（rowID → 已有语言）+ 租户映射（rowID → 租户）
	if idx != nil && db != nil {
		if langs, err := db.AllRowLangs(0); err == nil {
			idx.IDLangs = langs
		}
		if rows, err := db.AllRowsWithTenant(); err == nil {
			idx.IDTenants = map[int64]int64{}
			for _, r := range rows {
				idx.IDTenants[r.ID] = r.TenantID
			}
		}
	}
	return &Engine{
		Cfg:              cfg,
		LLM:              llm.NewClient(cfg),
		DB:               db,
		Index:            idx,
		Ten:              ts,
		cjkCacheByTenant: map[int64]map[string]int64{},
		breaker:          NewBreaker(threshold, coolDown),
	}
}

// getCJKCache 构建指定租户的 CJK → rowID 缓存（按租户懒加载）
func (e *Engine) getCJKCache(tenantID int64) map[string]int64 {
	e.cjkMu.Lock()
	defer e.cjkMu.Unlock()
	if e.cjkCache == nil {
		e.cjkCache = map[string]int64{}
	}
	if _, ok := e.cjkCacheByTenant[tenantID]; ok {
		return e.cjkCacheByTenant[tenantID]
	}
	m := map[string]int64{}
	if e.DB != nil {
		if rows, err := e.DB.GetAllRows(tenantID); err == nil {
			for _, r := range rows {
				cjk := ExtractCJK(r.Zh)
				if cjk != "" {
					m[cjk] = r.ID
				}
			}
		}
	}
	e.cjkCacheByTenant[tenantID] = m
	return m
}

// cjkOverlap 计算两条中文的 CJK 字符 Jaccard 重叠率（0~1）
// 用于区分"同一句的不同表述/标点变体"与"语义相近但非同一句"
func cjkOverlap(a, b string) float64 {
	ca := []rune(ExtractCJK(a))
	cb := []rune(ExtractCJK(b))
	if len(ca) == 0 || len(cb) == 0 {
		return 0
	}
	sa := map[rune]struct{}{}
	sb := map[rune]struct{}{}
	for _, r := range ca {
		sa[r] = struct{}{}
	}
	for _, r := range cb {
		sb[r] = struct{}{}
	}
	inter, union := 0, len(sa)+len(sb)
	for r := range sa {
		if _, ok := sb[r]; ok {
			inter++
		}
	}
	union -= inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// TranslateOne 翻译单条文本（四段匹配），等价 lib.translate_one
// langOnly=true 时只用 KB（translate_file 第一遍用）；否则 KB+模型兜底。
// 参数 stage: 流程阶段（KB 兜底翻译默认 config.StageKBMatch）。
func (e *Engine) TranslateOne(ctx context.Context, zhText string, targetLangs []string, langOnly bool, stage string) (*TranslateResult, error) {
	res := &TranslateResult{
		Translations: map[string]string{},
		TargetLangs:  targetLangs,
	}
	tid := e.tenantID(ctx)
	srcLang := DetectSourceLang(zhText) // 检测实际源语言（zh/en），供指令与 KB 匹配使用
	if stage == "" {
		stage = config.StageKBMatch
	}

	// 非中文源 → 逐语言模型直翻
	if srcLang != "zh" {
		res.Mode = "纯模型翻译"
		res.NeedModel = targetLangs
		if langOnly {
			return res, nil
		}
		e.translateLangsConcurrent(ctx, zhText, targetLangs, res.Examples, res.Translations, srcLang, stage)
		return res, nil
	}

	// 1. 精确命中
	var matched *kb.Row
	if e.DB != nil {
		if r, err := e.DB.FindExact(zhText, tid); err == nil {
			matched = r
		}
		// CJK 标点无关精确
		if matched == nil {
			cjk := ExtractCJK(zhText)
			if id, ok := e.getCJKCache(tid)[cjk]; ok {
				if r, err := e.DB.FetchRowTenant(id, tid); err == nil {
					matched = r
				}
			}
		}
	}
	if matched != nil {
		res.Mode = "精确命中"
		res.MatchedZH = matched.Zh
		res.Translations = e.assignKB(matched, targetLangs, &res.NeedModel)
		if len(res.NeedModel) == 0 {
			return res, nil
		}
	}

	// 2. 模糊子串
	if matched == nil && e.DB != nil {
		hits, _ := e.DB.FuzzyHits(zhText, e.Cfg.TopFuzzy, tid)
		if len(hits) > 0 {
			best := hits[0]
			res.Candidates = hits
			res.Mode = "模糊匹配"
			res.MatchedZH = best.Zh
			res.Translations = e.assignKB(best, targetLangs, &res.NeedModel)
			// 模糊命中视为高置信，补缺语言走模型兜底
			if len(res.NeedModel) == 0 {
				return res, nil
			}
		}
	}

	// 3. 语义高相似（≥0.90）——按目标语言过滤，只检索含目标语言的行
	if e.Index != nil && len(e.Index.Vecs) > 0 {
		// 先把中文转成嵌入向量，再在向量索引中做余弦相似检索
		vec, err := e.LLM.Embed(ctx, zhText)
		if err == nil && len(vec) > 0 {
			highSim, _ := e.resolvePolicy(ctx)
			// 检索返回 TopK 个语义相似行（按目标语言 + 租户双重过滤）
			results := e.Index.Search(vec, e.Cfg.TopK, targetLangs, tid)
			if len(results) > 0 && results[0].Sim >= highSim {
				res.Similarity = results[0].Sim
				if r, err := e.DB.FetchRowTenant(results[0].ID, tid); err == nil {
					// ★ 只采用与输入为"同一句"的语义命中（CJK 字符重叠足够高）；
					// 语义相近但非同一句（如带编号的流程条目）不得整句替换，降级走模型
					overlap := cjkOverlap(zhText, r.Zh)
					if overlap >= e.Cfg.SemHitCharOverlap {
						res.MatchedZH = r.Zh
						res.Mode = "语义命中"
						res.Translations = e.assignKB(r, targetLangs, &res.NeedModel)
						if len(res.NeedModel) == 0 {
							return res, nil
						}
					} else {
						res.Examples = append(res.Examples, r)
						res.Mode = "语义相近（非同一句）-在线模型生成"
					}
				}
			}
			// 4. 参考例句（≥MED_SIM 且未命中时收集）
			_, medSim := e.resolvePolicy(ctx)
			if matched == nil && len(results) > 0 && results[0].Sim >= medSim {
				for _, s := range results {
					if r, err := e.DB.FetchRowTenant(s.ID, tid); err == nil {
						res.Examples = append(res.Examples, r)
					}
				}
				res.Mode = "段匹配-全不命中-在线模型生成"
			}
		}
	}

	if langOnly {
		if res.Mode == "" {
			res.Mode = "模型翻译（无知识库）"
		}
		return res, nil
	}

	// 5. 模型兜底：逐语言翻译（并发）
	if res.Mode == "" {
		res.Mode = "模型翻译（无知识库）"
	}
	res.NeedModel = targetLangs
	// 还有语言需要模型生成 → 通知"AI生成中"
	if e.OnPhase != nil {
		need := make([]string, 0, len(res.NeedModel))
		for _, lc := range res.NeedModel {
			if v, ok := res.Translations[lc]; ok && strings.TrimSpace(v) != "" {
				continue
			}
			need = append(need, lc)
		}
		if len(need) > 0 {
			e.OnPhase("ai_generating")
		}
	}
	e.translateLangsConcurrent(ctx, zhText, targetLangs, res.Examples, res.Translations, srcLang, stage)
	res.NeedModel = nil
	return res, nil
}

// translateLangsConcurrent 并发翻译多个目标语言（跳过已翻译的）
// 用信号量限制并发，避免打爆 LLM API；结果写入 out（map 并发写由内部加锁保护）。
// 参数 sourceLang: 实际源语言代码（默认 "zh"）；stage: 流程阶段，透传至单语翻译指令与模型解析。
func (e *Engine) translateLangsConcurrent(ctx context.Context, zhText string, langs []string, examples []*kb.Row, out map[string]string, sourceLang, stage string) {
	if len(langs) == 0 {
		return
	}
	if sourceLang == "" {
		sourceLang = "zh"
	}
	// 需要翻译的语言（跳过已有值）
	need := make([]string, 0, len(langs))
	for _, lc := range langs {
		if v, ok := out[lc]; ok && strings.TrimSpace(v) != "" {
			continue
		}
		need = append(need, lc)
	}
	if len(need) == 0 {
		return
	}

	const maxConcurrent = 3 // 服务器 2 核，3 路并发平衡
	sem := make(chan struct{}, maxConcurrent)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, lc := range need {
		wg.Add(1)
		go func(lang string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			tr, err := e.SingleLangTranslate(ctx, zhText, lang, examples, sourceLang, stage)
			if err != nil {
				tr = ""
			}
			tr = PostProcessTranslation(tr, lang)
			mu.Lock()
			out[lang] = tr
			mu.Unlock()
		}(lc)
	}
	wg.Wait()
}

// assignKB 从知识库行分配翻译，返回缺失语言
func (e *Engine) assignKB(row *kb.Row, targetLangs []string, needModel *[]string) map[string]string {
	out := map[string]string{}
	var need []string
	for _, lc := range targetLangs {
		if v, ok := row.Langs[lc]; ok && strings.TrimSpace(v) != "" {
			out[lc] = v
		} else {
			need = append(need, lc)
		}
	}
	*needModel = need
	return out
}

// TranslateLangsInto 并发翻译缺失语言，写入 out/sources（供 workflow 初翻用）。
// 参数 sourceLang: 实际源语言代码（默认 "zh"）；stage: 流程阶段（初翻默认 config.StageAIInitial）；
// out/sources 为译文与来源映射。
func (e *Engine) TranslateLangsInto(ctx context.Context, zhText string, langs []string, out map[string]string, sources map[string]string, sourceLang, stage string) {
	if stage == "" {
		stage = config.StageAIInitial
	}
	e.translateLangsConcurrent(ctx, zhText, langs, nil, out, sourceLang, stage)
	for _, lc := range langs {
		if sources != nil && out[lc] != "" {
			sources[lc] = "model"
		}
	}
}

// TranslateWithFeedback 带驳回意见重译（初翻 Agent 修正，用于被驳回工单重跑）。
// 参数 stage: 流程阶段（默认 config.StageAIInitial）。
func (e *Engine) TranslateWithFeedback(ctx context.Context, zhText, targetLang, feedback, stage string) string {
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	prompt := fmt.Sprintf(
		"你是资深%s翻译。前一次翻译被审校驳回，驳回意见如下：%s。请严格按照意见修正重译。\n\n【原文】%s\n\n只输出修正后的%s译文：",
		langName, feedback, zhText, langName)
	messages := []map[string]string{{"role": "user", "content": prompt}}
	base, key, model := e.resolveModel(ctx)
	if b2, k2, m2, ok := e.resolveStageModel(ctx, stage); ok {
		base, key, model = b2, k2, m2
	}
	content, _, err := e.LLM.CallChat(ctx, base, key, model, messages, 2048, false, e.Cfg.FallbackTemp)
	if err != nil || strings.TrimSpace(content) == "" {
		return ""
	}
	return PostProcessTranslation(content, targetLang)
}

// ReviewTranslation 审校译文（LLM 修正术语/语法/表达，返回修正后译文；失败返回原文）。
// 参数 stage: 流程阶段（默认 config.StageReview）。
func (e *Engine) ReviewTranslation(ctx context.Context, source, translation, targetLang, stage string) string {
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	prompt := fmt.Sprintf(
		"你是资深翻译审校。请审校以下%s译文，仅修正术语准确性和语法错误，保持原意与风格，不要改写结构。\n\n【原文】%s\n【待审校译文】%s\n\n只输出审校后的译文：",
		langName, source, translation)
	messages := []map[string]string{{"role": "user", "content": prompt}}
	base, key, model := e.resolveModel(ctx)
	if b2, k2, m2, ok := e.resolveStageModel(ctx, stage); ok {
		base, key, model = b2, k2, m2
	}
	content, _, err := e.LLM.CallChat(ctx, base, key, model, messages, 2048, false, e.Cfg.FallbackTemp)
	if err != nil || strings.TrimSpace(content) == "" {
		return ""
	}
	return PostProcessTranslation(content, targetLang)
}

// ============ 单语翻译 ============

// srcName 源语言代码 → 提示用语言名（未知语言回退 "文本"）。
// 参数 code: 语言代码（如 zh/en/fr）；返回可读语言名（中文/英语/法语…）。
func srcName(code string) string {
	switch code {
	case "zh":
		return "中文"
	case "en":
		return "英语"
	case "zh_hant":
		return "繁体中文"
	}
	if code != "" {
		if n := config.LangNames[code]; n != "" {
			return n
		}
	}
	return "文本"
}

// translateInstruction 按源语言+目标语言+界面语言定制翻译指令（支持任意方向互译与中英文提示词）。
// 参数 source: 源语言代码；target: 目标语言代码；uiLang: 界面语言（"zh"=中文提示词，其他/空=英文提示词）。
// 返回: 模型翻译指令（不臆断源语言名称，未知源回退 "文本"）。
func translateInstruction(source, target, uiLang string) string {
	src := srcName(source)
	// 英文界面 → 英文提示词（模型自动识别源语言，不显式引用源语言名避免中英混排）
	if uiLang != "" && uiLang != "zh" && uiLang != "zh_hant" {
		switch target {
		case "zh":
			return "Translate the following text into Simplified Chinese. Output only the Simplified Chinese translation."
		case "zh_hant":
			return "Convert the following text into Traditional Chinese. Output only the Traditional Chinese translation."
		case "ja":
			return "Translate the following text into Japanese, using proper Kanji+Kana mixed writing (not only Kana). Output only the Japanese translation."
		case "ko":
			return "Translate the following text into Korean (한국어), using Hangul. Do not output Japanese. Output only the Korean translation."
		default:
			cn := config.LangNames[target]
			if cn == "" {
				cn = target
			}
			return "Translate the following text into " + cn + ". Output only the translation, without extra explanation."
		}
	}
	// 中文界面 → 中文提示词
	switch target {
	case "zh":
		return fmt.Sprintf("把下面的%s翻译为简体中文，只输出简体中文结果", src)
	case "zh_hant":
		return fmt.Sprintf("把下面的%s转换为繁体中文，只输出繁体中文结果", src)
	case "ja":
		return fmt.Sprintf("把下面的%s翻译为日语，必须使用规范的日语汉字+假名混合书写，不要只用假名", src)
	case "ko":
		return fmt.Sprintf("把下面的%s翻译为韩语（한국어），必须使用韩语谚文书写，禁止输出日语", src)
	default:
		cn := config.LangNames[target]
		if cn == "" {
			cn = target
		}
		return fmt.Sprintf("把下面的%s翻译为%s，不要额外解释", src, cn)
	}
}

// buildExamplesPrompt 从知识库命中行构造术语/句对参考，注入翻译 prompt
// 目的是让模型兜底翻译时沿用知识库里的标准专有词与术语译法（如 蓝牙钥匙→Bluetooth-Schlüssel）
// 只注入短句（中文 ≤30 字）作为术语参考，长流程句跳过，避免模型把参考块当翻译目标复述
func buildExamplesPrompt(zhText, targetLang string, examples []*kb.Row) string {
	if len(examples) == 0 {
		return ""
	}
	used := map[string]bool{}
	var sb strings.Builder
	count := 0
	for _, r := range examples {
		if r == nil || strings.TrimSpace(r.Zh) == "" {
			continue
		}
		if used[r.Zh] {
			continue
		}
		tr, ok := r.Langs[targetLang]
		if !ok || strings.TrimSpace(tr) == "" {
			continue
		}
		if len([]rune(r.Zh)) > 30 {
			continue
		}
		used[r.Zh] = true
		if count == 0 {
			sb.WriteString("知识库术语参考（仅用于沿用其专有词/术语译法）：\n")
		}
		count++
		sb.WriteString(fmt.Sprintf("%d. %s → %s\n", count, r.Zh, tr))
		if count >= 5 {
			break
		}
	}
	if count == 0 {
		return ""
	}
	return sb.String()
}

// resolveModel 解析当前租户的模型配置；未配置租户级时回退全局默认。
// 若配置了 ModelRoutes 路由策略，则按权重选取主模型，失败后调用方按 resolveRouteFallbacks 降级。
func (e *Engine) resolveModel(ctx context.Context) (base, key, model string) {
	if e.Ten != nil {
		if mc, err := e.Ten.GetModelConfig(e.tenantID(ctx)); err == nil && mc.Model != "" && mc.APIBase != "" {
			return mc.APIBase, mc.APIKey, mc.Model
		}
	}
	if len(e.Cfg.ModelRoutes) > 0 {
		p := e.pickPrimaryRoute()
		return p.APIBase, p.APIKey, p.Model
	}
	return e.Cfg.OnlineAPIBase, e.Cfg.OnlineAPIKey, e.Cfg.OnlineModel
}

// resolveStageModel 解析指定流程阶段的独立模型配置（system_config.stage_models，超管维护）。
// 参数 stage: 流程阶段标识（config.StageKBMatch / StageAIInitial / StageEvals / StageReview）。
// 返回 base/key/model 与是否命中；未配置该阶段或缺 model/api_base 时 ok=false（调用方回退 resolveModel）。
// APIKey 为空时继承全局默认密钥（租户/路由/全局），便于阶段模型复用同一供应商密钥。
func (e *Engine) resolveStageModel(ctx context.Context, stage string) (base, key, model string, ok bool) {
	if e.St == nil {
		return "", "", "", false
	}
	raw, err := e.St.GetConfig("stage_models")
	if err != nil || raw == "" {
		return "", "", "", false
	}
	var m config.StageModels
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", "", "", false
	}
	sm, exists := m[stage]
	if !exists || sm.APIBase == "" || sm.Model == "" {
		return "", "", "", false
	}
	if sm.APIKey != "" {
		return sm.APIBase, sm.APIKey, sm.Model, true
	}
	_, gKey, _ := e.resolveModel(ctx) // 阶段模型未填密钥 → 继承全局默认密钥
	return sm.APIBase, gKey, sm.Model, true
}

// pickPrimaryRoute 按权重选取主路由（权重最高者；全为 0 时取第一个）
func (e *Engine) pickPrimaryRoute() config.ProviderConfig {
	rs := e.Cfg.ModelRoutes
	if len(rs) == 0 {
		return config.ProviderConfig{}
	}
	best, bestW := rs[0], rs[0].Weight
	// 线性扫描取权重最高者（权重相等时优先前面的配置）
	for _, r := range rs[1:] {
		if r.Weight > bestW {
			best, bestW = r, r.Weight
		}
	}
	return best
}

// resolveRouteFallbacks 返回按权重降序的备用路由（排除主路由），用于主模型失败时降级
func (e *Engine) resolveRouteFallbacks(primary config.ProviderConfig) []config.ProviderConfig {
	out := []config.ProviderConfig{}
	for _, r := range e.Cfg.ModelRoutes {
		if r.APIBase == primary.APIBase && r.Model == primary.Model {
			continue
		}
		out = append(out, r)
	}
	// 按权重降序（插入排序：将高权重路由排到前面，供降级链按优先级使用）
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Weight > out[j-1].Weight; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// resolvePolicy 解析当前租户策略参数（high_sim/med_sim），未配置回退全局默认
func (e *Engine) resolvePolicy(ctx context.Context) (high, med float64) {
	high = e.Cfg.HighSim
	med = e.Cfg.MedSim
	if e.Ten != nil {
		if pc, err := e.Ten.GetPolicyConfig(e.tenantID(ctx)); err == nil {
			if pc.HighSim > 0 {
				high = pc.HighSim
			}
			if pc.MedSim > 0 {
				med = pc.MedSim
			}
		}
	}
	return
}

// SingleLangTranslate 单语翻译（含 429 降级、finish_reason=length 重试、截断自修复）。
// 参数 sourceLang: 实际源语言代码（默认 "zh"）；stage: 流程阶段（config.Stage*，默认 ai_initial）。
func (e *Engine) SingleLangTranslate(ctx context.Context, zhText, targetLang string, examples []*kb.Row, sourceLang, stage string) (string, error) {
	if sourceLang == "" {
		sourceLang = "zh"
	}
	if stage == "" {
		stage = config.StageAIInitial
	}
	return e.singleLang(ctx, zhText, targetLang, examples, sourceLang, stage, 0)
}

// singleLang 单语翻译核心实现（SingleLangTranslate 的实际逻辑）。
// 流程：组装指令+术语参考 → 解析主模型（阶段模型 → 租户级/路由/全局）→ 检测 Hunyuan 语言支持度
// → 计算 max_tokens → 熔断检查 → 主模型短超时调用 → 失败时记录熔断并沿路由链降级 /
// 单 fallback 兜底（429 前 sleep 2s）→ 记录实际用量 → 成功则重置熔断
// → finish_reason=length 或空内容翻倍 max_tokens 递归重试（最多 2 次）
// → 后处理 + 截断自修复。参数：sourceLang 源语言（默认 "zh"），stage 流程阶段，attempt 当前重试次数。
// 返回译文内容与错误（失败时返回 ""）。
func (e *Engine) singleLang(ctx context.Context, zhText, targetLang string, examples []*kb.Row, sourceLang, stage string, attempt int) (string, error) {
	cfg := e.Cfg
	instruction := translateInstruction(sourceLang, targetLang, uiLangFromCtx(ctx))
	ref := buildExamplesPrompt(zhText, targetLang, examples)

	// 术语参考放入 system 消息（模型不会复述 system 内容），user 只含指令+待翻译文本
	var messages []map[string]string
	if ref != "" {
		langName := config.LangNames[targetLang]
		if langName == "" {
			langName = targetLang
		}
		messages = []map[string]string{
			{"role": "system", "content": ref +
				"\n翻译时请沿用以上参考中的专有词/术语译法，但只输出待翻译文本的翻译结果，不得输出或复述参考内容。"},
			{"role": "user", "content": instruction + "\n\n" + zhText},
		}
	} else {
		messages = []map[string]string{{"role": "user", "content": instruction + "\n\n" + zhText}}
	}

	base, key, model := e.resolveModel(ctx)
	// 阶段独立模型：配置了该阶段（stage_models）则优先使用；未配置回退 resolveModel
	stageActive := false
	if b2, k2, m2, ok := e.resolveStageModel(ctx, stage); ok {
		base, key, model = b2, k2, m2
		stageActive = true
	}

	// 模型路由策略：配置了多供应商时，主模型失败按权重降序逐一降级（阶段模型独立时不走路由链）
	var routeFallbacks []config.ProviderConfig
	if !stageActive && len(e.Cfg.ModelRoutes) > 0 {
		primary := e.pickPrimaryRoute()
		routeFallbacks = e.resolveRouteFallbacks(primary)
	}

	// Hunyuan-MT 不支持的语种 → 降级模型
	hunyuan := strings.HasPrefix(model, "tencent/Hunyuan-MT")
	if hunyuan && !cfg.HunyuanMTLangCode[targetLang] {
		model = cfg.HunyuanFallbackModel
		hunyuan = false
	}

	lineCount := strings.Count(zhText, "\n")
	chars := len([]rune(zhText))
	maxTokens := 2048 + lineCount*384
	if chars*4 > maxTokens {
		maxTokens = chars * 4
	}
	if maxTokens < 2048 {
		maxTokens = 2048
	}

	// ★ 主模型熔断：Hunyuan 连续失败超阈值后直接走 fallback，冷却后自动恢复
	mainOpen := e.breaker.IsOpen()

	// Hunyuan 主模型首次调用用短超时（熔断开启时不尝试主模型）
	content, finishReason, err := e.tryMainModel(ctx, cfg, base, key, model, messages, maxTokens, hunyuan, mainOpen)
	e.NoteLLMResult(err == nil)

	// 主模型失败 → 记录熔断计数并降级到 fallback 模型
	if err != nil && (isRateLimited(err) || isNetworkError(err)) {
		if hunyuan && !mainOpen {
			e.breaker.Fail(err.Error())
		}
		// 优先尝试多供应商路由降级链
		if len(routeFallbacks) > 0 {
			for _, r := range routeFallbacks {
				if isRateLimited(err) {
					time.Sleep(2 * time.Second)
				}
				content, finishReason, err = e.LLM.CallChat(ctx, r.APIBase, r.APIKey, r.Model, messages, maxTokens, false, cfg.FallbackTemp)
				if err == nil {
					model = r.Model
					base = r.APIBase
					break
				}
			}
		}
		// 路由链全部失败或无路由配置 → 原有单 fallback
		if err != nil {
			fallback := cfg.HunyuanFallbackModel
			if isRateLimited(err) {
				time.Sleep(2 * time.Second)
			}
			content, finishReason, err = e.LLM.CallChat(ctx, base, key, fallback, messages, maxTokens, false, cfg.FallbackTemp)
			e.NoteLLMResult(err == nil)
		}
	}
	if err != nil {
		return "", err
	}
	// 记录本次实际使用的供应商与模型（用于计量成本核算）
	e.NoteUsageModel(ctx, usageProvider(base), model)
	// 主模型成功 → 重置熔断计数
	if hunyuan && !mainOpen {
		e.breaker.Reset()
	}

	// finish_reason=length 或空内容 → 翻倍 max_tokens 重试
	if (finishReason == "length" || content == "") && attempt < 2 {
		if maxTokens*2 <= 8192 {
			maxTokens *= 2
		}
		return e.singleLang(ctx, zhText, targetLang, examples, sourceLang, stage, attempt+1)
	}

	content = PostProcessTranslation(content, targetLang)

	// 截断自修复
	if e.isTranslationIncomplete(content, zhText, targetLang) {
		completed := e.autoCompleteTranslation(ctx, zhText, targetLang, content, base, key, model, sourceLang)
		if completed != "" {
			content = completed
		}
	}
	return content, nil
}

// tryMainModel 尝试主模型；熔断开启时直接改用 fallback 模型（不等待）
func (e *Engine) tryMainModel(ctx context.Context, cfg *config.Config, base, key, model string,
	messages []map[string]string, maxTokens int, hunyuan, breakerOpen bool) (string, string, error) {

	// 熔断中：跳过主模型，直接走 fallback
	if breakerOpen {
		return e.LLM.CallChat(ctx, base, key, cfg.HunyuanFallbackModel, messages, maxTokens, false, cfg.FallbackTemp)
	}

	// 主模型首次调用用短超时：Hunyuan 慢/挂起时快速降级，避免等满兜底超时
	firstCtx := ctx
	var firstCancel context.CancelFunc
	if hunyuan && cfg.HunyuanFirstTimeoutSec > 0 {
		firstCtx, firstCancel = context.WithTimeout(ctx, time.Duration(cfg.HunyuanFirstTimeoutSec)*time.Second)
		defer firstCancel()
	}
	return e.LLM.CallChat(firstCtx, base, key, model, messages, maxTokens, hunyuan, cfg.FallbackTemp)
}

// usageProvider 根据 base URL 推断供应商标识（成本核算分组用）
func usageProvider(base string) string {
	switch {
	case strings.Contains(base, "siliconflow"):
		return "siliconflow"
	case strings.Contains(base, "bigmodel"):
		return "bigmodel"
	case strings.Contains(base, "openai"):
		return "openai"
	case strings.Contains(base, "volces"):
		return "volcengine"
	case strings.Contains(base, "aliyun") || strings.Contains(base, "dashscope"):
		return "aliyun"
	}
	return "global"
}

// isTranslationIncomplete 判断翻译是否截断
func (e *Engine) isTranslationIncomplete(result, zhText, targetLang string) bool {
	if targetLang == "zh_hant" || len([]rune(zhText)) <= 20 {
		return false
	}
	zhLen := len([]rune(zhText))
	resLen := len([]rune(result))
	minRatio := 0.8
	if targetLang == "ja" || targetLang == "ko" {
		minRatio = 0.6
	}
	if resLen < zhLen*int(minRatio*10)/10 {
		return true
	}
	if resLen < zhLen*6/5 && !strings.HasSuffix(strings.TrimSpace(result), ".") {
		return true
	}
	if resLen > 100 && !isEndPunct(result) {
		return true
	}
	return false
}

// isEndPunct 判断文本末尾是否以中文/西文结束标点收尾
// （用于判断长文本是否被截断：正常译文应带结束标点）
func isEndPunct(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	last := t[len(t)-1:]
	return strings.Contains("。！？.!?:：;；…", last)
}

// autoCompleteTranslation 续翻 + 全量重翻（sourceLang 透传保证互译方向正确）
func (e *Engine) autoCompleteTranslation(ctx context.Context, zhText, targetLang, partial, base, key, model, sourceLang string) string {
	// 续翻
	cont := fmt.Sprintf("你之前的翻译被截断了，请从断点继续，不要重复已经翻译的内容：\n%s\n\n继续翻译：", partial)
	messages := []map[string]string{{"role": "user", "content": cont}}
	content, _, err := e.LLM.CallChat(ctx, base, key, model, messages, 4096, false, e.Cfg.FallbackTemp)
	if err == nil && content != "" {
		merged := mergeContinuation(partial, content)
		if !e.isTranslationIncomplete(merged, zhText, targetLang) {
			return PostProcessTranslation(merged, targetLang)
		}
	}
	// 全量重翻
	instruction := translateInstruction(sourceLang, targetLang, uiLangFromCtx(ctx))
	full := fmt.Sprintf("%s。必须完整翻译，不要省略任何内容，不要被截断：\n\n%s", instruction, zhText)
	messages = []map[string]string{{"role": "user", "content": full}}
	content, _, err = e.LLM.CallChat(ctx, base, key, model, messages, 8192, false, e.Cfg.FallbackTemp)
	if err == nil && content != "" {
		return PostProcessTranslation(content, targetLang)
	}
	return ""
}

// mergeContinuation 词级重叠去重拼接
func mergeContinuation(original, continuation string) string {
	origWords := strings.Fields(original)
	contWords := strings.Fields(continuation)
	if len(origWords) == 0 || len(contWords) == 0 {
		return original + continuation
	}
	maxOverlap := 20
	if len(origWords) < maxOverlap {
		maxOverlap = len(origWords)
	}
	for n := maxOverlap; n >= 1; n-- {
		tail := origWords[len(origWords)-n:]
		if len(contWords) >= n && equalWords(tail, contWords[:n]) {
			return original + " " + strings.Join(contWords[n:], " ")
		}
	}
	return original + " " + continuation
}

// equalWords 逐词比较两个字符串切片是否完全相等（用于续翻拼接时的重叠检测）
func equalWords(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isRateLimited 判断错误是否为 429 限流（限流时应 sleep 后重试而非直接判定失败）
func isRateLimited(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "429")
}

// isNetworkError 判断是否为超时/网络/连接错误（这类错误值得降级重试）
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline") ||
		strings.Contains(msg, "context deadline") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "eof")
}

// ============ 批量翻译（文件翻译用） ============

// BatchTranslate 批量单语翻译，等价 call_online_llm_single_lang_batch
// 返回与输入等长的译文列表，失败填 "[翻译失败]"
func (e *Engine) BatchTranslate(ctx context.Context, texts []string, targetLang string, batchSize int, onBatchDone func(done, total int)) []string {
	cfg := e.Cfg
	result := make([]string, len(texts))
	if len(texts) == 0 {
		return result
	}

	// 动态批大小
	avgLen := 0
	for _, t := range texts {
		avgLen += len([]rune(t))
	}
	avgLen /= len(texts)
	bs := 15
	if avgLen > 150 {
		bs = 5
	} else if avgLen > 80 {
		bs = 8
	}
	if batchSize > 0 {
		bs = batchSize
	}

	model := cfg.OnlineModel
	hunyuan := strings.HasPrefix(model, "tencent/Hunyuan-MT")
	if hunyuan && !cfg.HunyuanMTLangCode[targetLang] {
		model = cfg.HunyuanFallbackModel
		hunyuan = false
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 2) // 初始 2 批并发

	for start := 0; start < len(texts); start += bs {
		end := start + bs
		if end > len(texts) {
			end = len(texts)
		}
		chunk := texts[start:end]
		wg.Add(1)
		go func(start int, chunk []string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// 构造 <sN> 标记
			var sb strings.Builder
			for i, t := range chunk {
				sb.WriteString(fmt.Sprintf("<s%d>%s</s%d>\n", i+1, t, i+1))
			}
			instruction := translateInstruction("zh", targetLang, uiLangFromCtx(ctx))
			userPrompt := instruction + "\n\n请按编号逐条翻译，用 <sN>...</sN> 包裹每条翻译结果：\n\n" + sb.String()
			messages := []map[string]string{{"role": "user", "content": userPrompt}}

			maxTokens := 2048
			totalChars := 0
			for _, t := range chunk {
				totalChars += len([]rune(t))
			}
			if totalChars*4 > maxTokens {
				maxTokens = totalChars * 4
			}
			if maxTokens > 8192 {
				maxTokens = 8192
			}

			content, _, err := e.LLM.CallChat(ctx, cfg.OnlineAPIBase, cfg.OnlineAPIKey, model, messages, maxTokens, hunyuan, cfg.FallbackTemp)
			// 429/网络错误 → 429 先 sleep 5s 避峰，再用 fallback 模型重试一次
			if err != nil && (isRateLimited(err) || isNetworkError(err)) {
				if isRateLimited(err) {
					time.Sleep(5 * time.Second)
				}
				content, _, err = e.LLM.CallChat(ctx, cfg.OnlineAPIBase, cfg.OnlineAPIKey, cfg.HunyuanFallbackModel, messages, maxTokens, false, cfg.FallbackTemp)
			}

			parsed := parseBatchOutput(content, len(chunk))
			mu.Lock()
			for i, tr := range parsed {
				if i < len(chunk) {
					result[start+i] = PostProcessTranslation(tr, targetLang)
				}
			}
			mu.Unlock()
			if onBatchDone != nil {
				onBatchDone(end, len(texts))
			}
		}(start, chunk)
	}
	wg.Wait()

	// 兜底：未译出的逐条翻译
	for i, tr := range result {
		if strings.TrimSpace(tr) == "" {
			s, err := e.singleLang(ctx, texts[i], targetLang, nil, "zh", config.StageAIInitial, 0)
			if err != nil || s == "" {
				result[i] = "[翻译失败]"
			} else {
				result[i] = s
			}
		}
	}
	return result
}

// parseBatchOutput 解析批量翻译结果。返回与 n 等长的译文切片。
// 方式1：手工配对 <sN>...</sN> 标记（Go 正则不支持反向引用，故手工扫描）；
// 命中率 ≥ 9/10 即采用。否则方式2：按行解析 "1." / "[1]" / "1：" 等编号前缀。
func parseBatchOutput(content string, n int) []string {
	out := make([]string, n)
	// 方式1：<sN>...</sN>（手工配对，规避 Go 正则不支持反向引用）
	content = strings.Replace(content, "\r\n", "\n", -1)
	pos := 0
	hit := 0
	for pos < len(content) {
		openStart := strings.Index(content[pos:], "<s")
		if openStart < 0 {
			break
		}
		openStart += pos
		openEnd := strings.Index(content[openStart:], ">")
		if openEnd < 0 {
			break
		}
		openEnd += openStart
		body := content[openStart+1 : openEnd]
		if !strings.HasPrefix(body, "s") {
			pos = openEnd + 1
			continue
		}
		idx := 0
		if _, err := fmt.Sscanf(body[1:], "%d", &idx); err != nil || idx < 1 || idx > n {
			pos = openEnd + 1
			continue
		}
		closingEnd := strings.Index(content[openEnd+1:], "</s"+body[1:]+">")
		if closingEnd < 0 {
			pos = openEnd + 1
			continue
		}
		closingEnd += openEnd + 1
		text := strings.TrimSpace(content[openEnd+1 : closingEnd])
		if text != "" {
			out[idx-1] = text
			hit++
		}
		pos = closingEnd + 1
	}
	if hit >= n*9/10 {
		return out
	}
	// 方式2：按行 "1. xxx" / "[1] xxx" / "1：xxx"
	lines := strings.Split(content, "\n")
	lineHit := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		for i := 0; i < n; i++ {
			prefixes := []string{fmt.Sprintf("%d.", i+1), fmt.Sprintf("%d、", i+1), fmt.Sprintf("[%d]", i+1), fmt.Sprintf("%d：", i+1), fmt.Sprintf("%d:", i+1)}
			for _, p := range prefixes {
				if strings.HasPrefix(line, p) && out[i] == "" {
					out[i] = strings.TrimSpace(line[len(p):])
					lineHit++
					break
				}
			}
		}
	}
	if lineHit >= n*9/10 {
		return out
	}
	return out
}

// ============ 其他语言翻译（前端子选单直接传非 KB 语言） ============

// TranslateOtherLang 纯模型翻译单条（"其他语言"，支持任意源语言互译）。
// 参数 zhText: 源文本；langCode: 目标语言代码；sourceLang: 实际源语言代码（默认 "zh"）；
// stage: 流程阶段（默认 config.StageAIInitial）。
func (e *Engine) TranslateOtherLang(ctx context.Context, zhText, langCode, sourceLang, stage string) (string, error) {
	if sourceLang == "" {
		sourceLang = "zh"
	}
	if stage == "" {
		stage = config.StageAIInitial
	}
	tr, err := e.singleLang(ctx, zhText, langCode, nil, sourceLang, stage, 0)
	if err != nil {
		return "", err
	}
	return PostProcessTranslation(tr, langCode), nil
}

// SummarizeContext LLM 总结文件内容（1-2 句）
func (e *Engine) SummarizeContext(ctx context.Context, texts []string) string {
	preview := strings.Join(texts[:min(50, len(texts))], "\n")
	if len([]rune(preview)) > 2000 {
		preview = string([]rune(preview)[:2000])
	}
	prompt := "请用1-2句中文总结以下文档的主要内容：\n\n" + preview
	messages := []map[string]string{{"role": "user", "content": prompt}}
	content, _, err := e.LLM.CallChat(ctx, e.Cfg.OnlineAPIBase, e.Cfg.OnlineAPIKey, e.Cfg.OnlineModel, messages, 256, false, e.Cfg.FallbackTemp)
	if err != nil {
		return ""
	}
	return content
}

// LLMParseLang LLM 识别语言，返回 ISO 639-1 代码或空
func (e *Engine) LLMParseLang(ctx context.Context, hint string) string {
	sys := "你是一个语言识别助手。用户给出一个语言名称，只返回该语言的ISO 639-1两字母代码（如 th, vi, ja）。无法识别返回 unknown"
	messages := []map[string]string{
		{"role": "system", "content": sys},
		{"role": "user", "content": hint},
	}
	content, _, err := e.LLM.CallChat(ctx, e.Cfg.OnlineAPIBase, e.Cfg.OnlineAPIKey, e.Cfg.OnlineModel, messages, 10, false, 0.0)
	if err != nil {
		return ""
	}
	content = strings.TrimSpace(strings.ToLower(content))
	if content == "unknown" || len(content) != 2 {
		return ""
	}
	return content
}

var _ = json.Marshal
