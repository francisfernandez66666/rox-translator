// ============ 本文件职责中文说明 ============
// 翻译引擎核心：复刻 Python lib.py 的 translate_one 主流程，负责
// 多租户翻译的完整调度链路——知识库(KB)四段匹配（精确命中 / CJK标点无关精确 /
// 模糊子串 / 语义高相似+例句参考）、模型路由（按权重选主模型并按权重降序降级）、
// 主模型熔断降级与冷却自动恢复、429/网络错误降级重试、并发逐语言翻译、
// 批量翻译（文件用，<sN> 标记解析）、审校/驳回重译、截断自修复、
// 基于 ctx 的进度回调（替代单例字段，防并发串台）、goroutine panic 兜底恢复、
// 以及请求级实际用量（供应商/模型）计量记录与错误率监控。
// ========================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/culture"
	"translator/internal/db"
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

	cjkCache         map[string]int64            // 兼容旧字段（保留）：默认租户 CJK→rowID 缓存
	cjkCacheByTenant map[string]map[string]int64 // ★ 2026-08-26 继承链改造：键=「租户|组织链指纹|跨部门开关」→ CJK字符 → row id
	cjkMu            sync.Mutex                  // 保护 CJK 缓存的并发读写锁

	// ★ 整改 D2：原 OnPhase 全局回调字段已移除——Engine 为进程级单例，多请求并发
	//   时对共享字段无锁读改写（进度事件串台/恢复错乱）。阶段回调改经 ctx 传递：
	//   WithProgressCallback / progressCallbackFrom。

	// ★ 主模型熔断恢复：Hunyuan 连续失败超阈值 → 直接降级；冷却后自动恢复
	breaker *Breaker

	// ★ 错误率监控：记录最近 N 次 LLM 调用成败（看门狗/告警用）
	errMu      sync.Mutex
	errRing    []bool // true=成功 false=失败
	errRingCap int

	// ★ 知识库向量索引重建
	NPZPath    string      // npz 文件路径（重建后写回）
	indexMu    sync.Mutex  // 保护 Index 指针热替换与重建互斥
	rebuilding atomic.Bool // 防并发重建

	// ★ 语言文化规范缓存（Gate L1）：key= tid|lang，TTL 60s
	cultureMu      sync.Mutex
	cultureCache   map[string]cultureEntry
	cultureEntries map[string][]cultureRule // 同 key 的结构化条目（L2 用）
}

// cultureRule 单条语言文化规则（来自语言文化包安全句，approved 才生效）。
type cultureRule struct {
	Kind        string // style/forbidden/replace
	Phrase      string
	Replacement string
}

// cultureEntry 缓存条目。
type cultureEntry struct {
	Text      string        // 渲染后的提示词块（L1 注入用）
	Rules     []cultureRule // 结构化条目（L2 硬过滤用）
	ExpiresAt time.Time
}

// ★ 性能优化（不换库 Phase A4，1G 机器）：进程级缓存封顶，防止长运行无限增长。
const (
	cjkCacheScopeMax = 128              // CJK 分片数上限（超过整代清空，惰性重建）
	cultureCacheMax  = 4096             // 文化闸门缓存条目上限（超过整代清空）
	cultureCacheTTL  = 60 * time.Second // 单条有效期（原有语义保留）
)

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

// maxLenKey 缩翻最长字符限制 context 键（0=未启用缩翻）。
type maxLenKey struct{}

// WithMaxLength 把缩翻最长字符限制写入 context（0=未启用；>0=译文总长不得超过该值）。
// 用途：文本/文件翻译与工单初翻共用，提示模型按上限精简输出。
func WithMaxLength(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, maxLenKey{}, n)
}

// maxLengthFromCtx 从 context 读取缩翻最长字符限制；未设置返回 0（未启用）。
func maxLengthFromCtx(ctx context.Context) int {
	if v, ok := ctx.Value(maxLenKey{}).(int); ok && v > 0 {
		return v
	}
	return 0
}

// maxLengthOption 从 options map 读取缩翻最长字符限制（兼容 JSON number/string 形态）。
// 返回：>0=启用缩翻并指定上限；<=0=未启用。
func maxLengthOption(options map[string]interface{}) int {
	if options == nil {
		return 0
	}
	switch v := options["max_length"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return 0
}

// usageCtxKey 请求级"实际使用的 LLM 供应商/模型"记录键（供计量成本核算）
type usageCtxKey struct{}

// usageRecord 可变的请求级记录（context 存指针，跨调用共享）。
//
// ★ 并发安全（2026-08-26 全仓评审 B7）：translateLangsConcurrent 的多语言 goroutine
//
//	会在各自 singleLang 成功路径并发调用 NoteUsageModel 写本结构，字段读写必须持锁。
type usageRecord struct {
	mu       sync.Mutex // 保护以下字段的并发读写
	provider string     // 实际使用的供应商标识（如 siliconflow/volcengine）
	model    string     // 实际使用的模型名
	used     bool       // 是否已记录过实际用量（避免回退默认）
}

// WithUsageRecorder 向 ctx 注入用量记录器（API 层在进入翻译前调用）。
// 同时注入 llm.UsageCollector：全链路（初翻/校对/Judge/文化闸门/embedding）
// 的真实 token 用量自动归集，供按实际费用计费。
func (e *Engine) WithUsageRecorder(ctx context.Context) context.Context {
	// ★ 余额不足中止：创建可取消 ctx 并注入中止函数，实时计费钩子在余额耗尽时调用，
	// 立即中止整次翻译任务（含全部并发段），杜绝其余段被供应商免费翻译的白嫖漏洞。
	ctx, cancel := context.WithCancel(ctx)
	ctx = llm.WithUsageCollector(ctx, &llm.UsageCollector{})
	ctx = llm.WithAbort(ctx, cancel)
	return context.WithValue(ctx, usageCtxKey{}, &usageRecord{})
}

// UsageTokens 返回本次请求累计的真实 token 用量（输入, 输出）；
// 未注入收集器时返回 (0,0)。
func (e *Engine) UsageTokens(ctx context.Context) (int64, int64) {
	if uc := llm.CollectorFrom(ctx); uc != nil {
		return uc.Totals()
	}
	return 0, 0
}

// NoteUsageModel 记录本次实际使用的供应商与模型（单语翻译成功路径调用）
func (e *Engine) NoteUsageModel(ctx context.Context, provider, model string) {
	if rec, ok := ctx.Value(usageCtxKey{}).(*usageRecord); ok {
		rec.mu.Lock()
		rec.provider = provider
		rec.model = model
		rec.used = true
		rec.mu.Unlock()
	}
}

// UsageModel 返回本次请求实际使用的供应商与模型；未记录时回退全局路由/全局默认
// （★ 2026-08-26 BYOK 移除：不再出现 "tenant" 供应商标识，成本核算口径统一）
func (e *Engine) UsageModel(ctx context.Context) (provider, model string) {
	if rec, ok := ctx.Value(usageCtxKey{}).(*usageRecord); ok {
		rec.mu.Lock()
		p, m, used := rec.provider, rec.model, rec.used
		rec.mu.Unlock()
		if used {
			return p, m
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

// getIndex 加锁快照当前语义索引（整改 R4：与 RebuildKBIndex 的 indexMu 写操作互斥，
// 消除「读 e.Index 同时另一请求重建热替换」的 data race）。返回指针副本，调用方勿长期持有。
func (e *Engine) getIndex() *kb.Index {
	e.indexMu.Lock()
	defer e.indexMu.Unlock()
	return e.Index
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

	// GateViolations L2 硬闸门违规：目标语言 → 命中的禁用词列表（replace 已自动替换不在此列）
	GateViolations map[string][]string `json:"gate_violations,omitempty"`
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
		if langs, err := db.AllRowLangs(-1); err == nil {
			idx.IDLangs = langs
		}
		if rows, err := db.AllRowsWithTenant(); err == nil {
			idx.IDTenants = map[int64]int64{}
			idx.IDPacks = map[int64]int64{}
			for _, r := range rows {
				idx.IDTenants[r.ID] = r.TenantID
				idx.IDPacks[r.ID] = r.PackID
			}
		}
	}
	return &Engine{
		Cfg:              cfg,
		LLM:              llm.NewClient(cfg),
		DB:               db,
		Index:            idx,
		Ten:              ts,
		cjkCacheByTenant: map[string]map[string]int64{},
		breaker:          NewBreaker(threshold, coolDown),
	}
}

// getCJKCache 构建指定作用域的 CJK → rowID 缓存（按「租户|组织链|开关」懒加载）。
// ★ 2026-08-26 继承链改造：缓存内容随可见范围变化——同一租户不同组织链/开关各自独立，
// 键含链指纹与开关态，组织移动或开关切换自动走新键（旧键惰性留存，InvalidateKBCaches 可全清）。
func (e *Engine) getCJKCache(scope *kb.PackScope) map[string]int64 {
	key := cjkCacheScopeKey(scope)
	e.cjkMu.Lock()
	defer e.cjkMu.Unlock()
	if e.cjkCache == nil {
		e.cjkCache = map[string]int64{}
	}
	if m, ok := e.cjkCacheByTenant[key]; ok {
		return m
	}
	m := map[string]int64{}
	if e.DB != nil {
		if rows, err := e.DB.GetAllRows(scope.TenantID); err == nil {
			for _, r := range rows {
				// 只缓存当前 scope 可见的行（隔离语义在缓存层同样成立）
				if !scopeVisibleID(scope, r.PackID, r.TenantID) {
					continue
				}
				cjk := ExtractCJK(r.Zh)
				if cjk != "" {
					m[cjk] = r.ID
				}
			}
		}
	}
	// ★ 性能优化（不换库 Phase A4）：分片数封顶，超限整代清空后仅保留本次新建分片，
	//   防止组织移动/开关切换累积的分片无限增长挤占内存。
	if len(e.cjkCacheByTenant) >= cjkCacheScopeMax {
		e.cjkCacheByTenant = map[string]map[string]int64{key: m}
	} else {
		e.cjkCacheByTenant[key] = m
	}
	return m
}

// scopeVisibleID 判断行是否落在 scope 直接采用域内（CJK 缓存过滤用；
// 跨部门回退层不进 CJK 缓存——它只在精确段二按需查询）。
func scopeVisibleID(scope *kb.PackScope, packID, rowTenant int64) bool {
	if scope == nil {
		return true
	}
	if _, chainOK := scope.ChainPacks[packID]; chainOK {
		return true // 链内部门包
	}
	if packID == 0 {
		return rowTenant == scope.TenantID // 历史无主行按企业层（限本租户）
	}
	if scope.TenantPackIDs[packID] {
		return true // 企业包
	}
	return scope.SharedPackIDs[packID] || scope.UniversalPackIDs[packID] // 行业包 / 通用语言习惯包
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

// TranslateOne 单条文本翻译入口，复刻 lib.translate_one 四段匹配流程：
// 精确命中 → CJK 标点无关精确 → 模糊子串 → 语义高相似+例句参考，未命中则模型兜底。
// 参数：zhText=源文本；targetLangs=目标语言列表；langOnly=true 时只用知识库
//
//	（文件翻译第一遍用），false 时知识库未覆盖语言由模型补齐；
//	stage=流程阶段标识（默认 config.StageKBMatch）。
//
// 返回单条翻译结果与错误；返回前已应用 L2 语言文化硬闸门
// （replace 规则自动替换、forbidden 规则记录违规并在强制模式下拦截）。
func (e *Engine) TranslateOne(ctx context.Context, zhText string, targetLangs []string, langOnly bool, stage string) (*TranslateResult, error) {
	res, err := e.translateOneInner(ctx, zhText, targetLangs, langOnly, stage)
	if res == nil || len(res.Translations) == 0 {
		return res, err
	}
	tid := tenant.FromContext(ctx)
	if tid <= 0 {
		return res, err // 平台上下文无文化规则
	}
	enforced := false
	if e.St != nil {
		if v, _ := e.St.GetConfig("locale_gate_enforced"); v == "1" {
			enforced = true
		}
	}
	for lang, text := range res.Translations {
		fixed, violations := e.applyCultureGate(ctx, tid, lang, text)
		if fixed != text {
			res.Translations[lang] = fixed
		}
		if len(violations) > 0 {
			if res.GateViolations == nil {
				res.GateViolations = map[string][]string{}
			}
			res.GateViolations[lang] = violations
			if enforced {
				// 拦截模式：命中禁用词的语言回退为待模型重译（由上层决定是否重试）
				delete(res.Translations, lang)
			}
		}
	}
	return res, err
}

// translateOneInner 翻译单条核心实现（原 TranslateOne 主体）。
func (e *Engine) translateOneInner(ctx context.Context, zhText string, targetLangs []string, langOnly bool, stage string) (*TranslateResult, error) {
	res := &TranslateResult{
		Translations: map[string]string{},
		TargetLangs:  targetLangs,
	}
	// ★ 平台上下文隔离：直接读 context 原始租户（不经兜底）。
	// 超管等平台级账号（tenant_id=0 且未显式切换租户）context 无租户 → tid=0，
	// 只归属平台根组织、不挂任何租户知识库——跳过精确/模糊/向量全部命中，纯模型翻译，
	// 杜绝超管误用具体租户（如 ROX极石汽车）的知识库数据。
	tid := tenant.FromContext(ctx)
	srcLang := DetectSourceLang(zhText) // 检测实际源语言（zh/en），供指令与 KB 匹配使用
	if stage == "" {
		stage = config.StageKBMatch
	}
	if tid <= 0 {
		res.Mode = "平台直翻（无知识库）"
		res.NeedModel = append(res.NeedModel, targetLangs...)
		e.translateLangsConcurrent(ctx, zhText, targetLangs, res.Examples, res.Translations, srcLang, stage)
		return res, nil
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

	// ★ 组装本次请求的知识库可见范围（2026-08-26 继承链改造）：
	// 链内部门包(就近覆盖) > 企业包/历史行 > 共享行业/文化包；跨部门回退层由两段式按需触发。
	scope := e.userScope(ctx, tid)
	// 跨部门精确命中的来源包名（打标用；空=未发生跨部门命中）
	crossFrom := ""

	// 1. 精确命中（两段式：链内优先，零命中且开关开时才查跨部门共享包）
	var matched *kb.Row
	if e.DB != nil {
		if r, src, err := e.DB.FindExactScoped(zhText, tid, scope); err == nil {
			matched = r
			if src == "cross" {
				crossFrom = scope.CrossName(r.PackID) // 记录来源部门包名用于打标
			}
		}
		// CJK 标点无关精确（缓存键含组织链指纹与开关态）
		if matched == nil {
			cjk := ExtractCJK(zhText)
			if id, ok := e.getCJKCache(scope)[cjk]; ok {
				if r, err := e.DB.FetchRowTenant(id, tid, scope); err == nil {
					matched = r
				}
			}
		}
	}
	if matched != nil {
		res.Mode = "精确命中"
		if crossFrom != "" {
			// 保留「精确命中」子串：前台徽标按 includes('精确命中') 判定，追加而非替换
			res.Mode = "精确命中 | 🌐跨部门（来自" + crossFrom + "）"
		}
		res.MatchedZH = matched.Zh
		res.Translations = e.assignKB(matched, targetLangs, &res.NeedModel)
		if len(res.NeedModel) == 0 {
			return res, nil
		}
	}

	// 2. 模糊子串（★ 隔离语义：仅链内命中可整句采用；跨部门命中只降级为例句参考）
	crossExamples := []*kb.Row{} // 跨部门例句池（模糊+语义回退统一收集，最终补位进 res.Examples）
	if matched == nil && e.DB != nil {
		chainHits, crossHits, _ := e.DB.FuzzyHitsScoped(zhText, e.Cfg.TopFuzzy, tid, scope)
		crossExamples = append(crossExamples, crossHits...) // 跨部门模糊 → 例句候选（不采用）
		if len(chainHits) > 0 {
			best := chainHits[0]
			res.Candidates = chainHits
			res.Mode = "模糊匹配"
			res.MatchedZH = best.Zh
			res.Translations = e.assignKB(best, targetLangs, &res.NeedModel)
			// 模糊命中视为高置信，补缺语言走模型兜底
			if len(res.NeedModel) == 0 {
				return res, nil
			}
		}
	}

	// 3. 语义检索（scope 化：链内高相似可整句采用；跨部门与低相似一律降级为例句）
	// ★ 检索源解耦（治本整改）：pgvector（PostgreSQL）不再依赖 npz 索引——
	//   此前整个语义块被 `if idx != nil` 包住，Postgres 部署未加载 npz 时（如演示镜像）
	//   连数据库内向量检索也被一并跳过，知识库术语完全无法命中。现按源独立可用性判定：
	//   任一源可用（pgvector 或 npz）即进入语义检索。
	idx := e.getIndex()
	pgSemOK := db.CurrentDialect() == db.DialectPostgres && e.DB != nil // pgvector 候选源（VectorSearch 内部自检不可用则返回空）
	npzSemOK := idx != nil && len(idx.Vecs) > 0
	if pgSemOK || npzSemOK {
		// 知识库 Embed 阶段覆盖（stage_models.kb_embed）：配置了独立 Embed 模型则随 ctx 透传
		// （整改 R3：改为请求级覆盖，避免全局 SetEmbedOverride 被并发请求串味）
		if eb, ek, em, eok := e.resolveStageModel(ctx, config.StageKBEmbed); eok {
			ctx = llm.WithEmbedOverride(ctx, eb, ek, em)
		}
		// ★ 嵌入三级查找（评审整改 R2）：管线预取向量表 → 进程缓存 → 回源单条调用。
		//   此前「每段每语言」必发一次 Embed HTTP，是文件并翻卡顿的第二大根因。
		var vec []float32
		if lookup := EmbedLookupFrom(ctx); lookup != nil {
			vec = lookup[embedShaKey(zhText)]
		}
		if len(vec) == 0 {
			if v, ok := getCachedEmbed(zhText); ok {
				vec = v
			}
		}
		if len(vec) == 0 {
			if v, err := e.LLM.Embed(ctx, zhText); err == nil && len(v) > 0 {
				vec = v
				putCachedEmbed(zhText, v)
			}
		}
		if len(vec) > 0 {
			highSim, medSim := e.resolvePolicy(ctx)
			// 检索返回 TopK（结果已按 InChain 优先 + 相似度排序；InChain=false=跨部门仅参考）
			// ★ PostgreSQL + pgvector：优先走数据库内向量检索（VectorSearch）；
			//   无 pgvector 结果（非 PG / 扩展缺失 / 无向量）时回退 npz 索引。
			var results []kb.SearchResult
			if pgSemOK {
				if vr, verr := e.DB.VectorSearch(vec, tid, scope, e.Cfg.TopK); verr == nil && len(vr) > 0 {
					results = vr
				}
			}
			if len(results) == 0 && npzSemOK {
				results = idx.ScopedSearchScope(vec, e.Cfg.TopK, targetLangs, tid, scope)
			}
			semAdopted := false
			anyChainExample := false
			for _, sr := range results {
				r, ferr := e.DB.FetchRowTenant(sr.ID, tid, scope)
				if ferr != nil {
					continue // 不可见/不存在：跳过（不泄露）
				}
				if !sr.InChain {
					// ★ 跨部门语义命中：无论相似度多高都只作例句参考（隔离语义硬约束）
					crossExamples = append(crossExamples, r)
					continue
				}
				if !semAdopted && sr.Sim >= highSim && cjkOverlap(zhText, r.Zh) >= e.Cfg.SemHitCharOverlap && matched == nil {
					// ★ 只采用与输入为"同一句"的链内语义命中（CJK 字符重叠足够高）；
					// 语义相近但非同一句不得整句替换
					semAdopted = true
					res.MatchedZH = r.Zh
					res.Mode = "语义命中"
					res.Similarity = sr.Sim
					res.Translations = e.assignKB(r, targetLangs, &res.NeedModel)
					continue
				}
				if sr.Sim >= medSim {
					res.Examples = append(res.Examples, r) // 链内例句（优先填充）
					anyChainExample = true
				}
			}
			if semAdopted && len(res.NeedModel) == 0 {
				return res, nil
			}
			if anyChainExample {
				res.Mode = "段匹配-全不命中-在线模型生成"
			} else if semAdopted {
				res.Mode = "语义命中"
			}
		}
	}

	// ★ 例句池补位：链内候选优先（buildExamplesPrompt 截取前 5 条），
	//   名额不满时以跨部门模糊/语义候选垫底——参考价值保留、污染风险最低。
	if len(crossExamples) > 0 {
		res.Examples = append(res.Examples, crossExamples...)
		if res.Mode == "模型翻译（无知识库）" || res.Mode == "" {
			res.Mode = "段匹配-全不命中-在线模型生成"
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
	// 还有语言需要模型生成 → 通知"AI生成中"（★ 整改 D2：回调经 ctx 传递，替代单例字段）
	if cb := progressCallbackFrom(ctx); cb != nil {
		need := make([]string, 0, len(res.NeedModel))
		for _, lc := range res.NeedModel {
			if v, ok := res.Translations[lc]; ok && strings.TrimSpace(v) != "" {
				continue
			}
			need = append(need, lc)
		}
		if len(need) > 0 {
			cb("ai_generating")
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
			defer recoverPipeline("translate_lang:" + lang) // 整改 D4
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
// examples: 知识库命中例句（可为 nil，注入初翻 prompt 作术语参考）；
// out/sources 为译文与来源映射。
func (e *Engine) TranslateLangsInto(ctx context.Context, zhText string, langs []string, examples []*kb.Row, out map[string]string, sources map[string]string, sourceLang, stage string) {
	if stage == "" {
		stage = config.StageAIInitial
	}
	e.translateLangsConcurrent(ctx, zhText, langs, examples, out, sourceLang, stage)
	for _, lc := range langs {
		if sources != nil && out[lc] != "" {
			sources[lc] = "model"
		}
	}
}

// TranslateWithFeedback 带驳回意见重译（初翻 Agent 修正，用于被驳回工单重跑）。
// 参数 stage: 流程阶段（默认 config.StageAIInitial）。
func (e *Engine) TranslateWithFeedback(ctx context.Context, zhText, targetLang, feedback, stage string) string {
	return e.translateWithFeedbackEx(ctx, zhText, targetLang, feedback, stage, nil)
}

// TranslateWithFeedbackEx 带驳回意见与知识库参考重译（硬闸打回自动重译用）。
// 参数 stage: 流程阶段（默认 config.StageAIInitial）；examples：知识库命中行（提供标准术语译法）。
func (e *Engine) TranslateWithFeedbackEx(ctx context.Context, zhText, targetLang, feedback, stage string, examples []*kb.Row) string {
	return e.translateWithFeedbackEx(ctx, zhText, targetLang, feedback, stage, examples)
}

// translateWithFeedbackEx 带驳回意见（可选知识库参考）重译核心实现。
// 参数 stage: 流程阶段（默认 config.StageAIInitial）；examples：知识库命中行（可为 nil）。
func (e *Engine) translateWithFeedbackEx(ctx context.Context, zhText, targetLang, feedback, stage string, examples []*kb.Row) string {
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	culture := ""
	if tid := tenant.FromContext(ctx); tid > 0 {
		culture, _ = e.cultureRules(ctx, tid, targetLang)
	}
	ref := buildExamplesPrompt(zhText, targetLang, examples) // 知识库术语参考（无命中则为空串）
	var prompt string
	if ref != "" {
		prompt = fmt.Sprintf("你是资深%s翻译。前一次翻译被驳回，驳回意见如下：%s。请严格按照意见修正重译。\n%s\n%s\n\n【原文】%s\n\n只输出修正后的%s译文：",
			langName, feedback, ref, culture, zhText, langName)
	} else {
		prompt = fmt.Sprintf("你是资深%s翻译。前一次翻译被审校驳回，驳回意见如下：%s。请严格按照意见修正重译。%s\n\n【原文】%s\n\n只输出修正后的%s译文：",
			langName, feedback, culture, zhText, langName)
	}
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
	culture := ""
	if tid := tenant.FromContext(ctx); tid > 0 {
		culture, _ = e.cultureRules(ctx, tid, targetLang)
	}
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	prompt := fmt.Sprintf(
		"你是资深翻译审校。请审校以下%s译文，仅修正术语准确性和语法错误，保持原意与风格，不要改写结构。%s\n\n【原文】%s\n【待审校译文】%s\n\n只输出审校后的译文：",
		langName, culture, source, translation)
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
			if strings.Contains(cn, "哈萨克") {
				return "Translate the following text into Kazakh, the state language of Kazakhstan (Qazaq tili). Use ONLY the Cyrillic alphabet (Қазақ тілі), NOT the Arabic-based script used by the Kazakh ethnic minority in China. Output only the Kazakh translation in Cyrillic, with no original text, no explanations, no placeholders like 【】 or brackets."
			}
			return "Translate the following text into " + cn + ". Output only the translation, without reproducing the original text, without extra explanations, and without placeholder symbols like 【原文】 or 【】."
		}
	}
	// 中文界面 → 中文提示词
	switch target {
	case "zh":
		return fmt.Sprintf("把下面的%s翻译为简体中文，只输出简体中文结果", src)
	case "zh_hant":
		return fmt.Sprintf("把下面的%s转换为繁体中文，只输出繁体中文结果，不要复述原文，不要输出类似【原文】【待審校譯文】的标记", src)
	case "ja":
		return fmt.Sprintf("把下面的%s翻译为日语，必须使用规范的日语汉字+假名混合书写，不要只用假名", src)
	case "ko":
		return fmt.Sprintf("把下面的%s翻译为韩语（한국어），必须使用韩语谚文书写，禁止输出日语", src)
	default:
		cn := config.LangNames[target]
		if cn == "" {
			cn = target
		}
		if strings.Contains(cn, "哈萨克") {
			return fmt.Sprintf("把下面的%s翻译为哈萨克语（哈萨克斯坦国家的官方语言，Qazaq tili）。只使用西里尔字母书写（Қазақ тілі），禁止使用中国哈萨克族使用的阿拉伯字母写法。只输出西里尔哈萨克语译文，不要复述原文，不要任何解释，不要输出【】等占位符号", src)
		}
		return fmt.Sprintf("把下面的%s翻译为%s，只输出译文本身，不要复述原文，不要输出【原文】【待審校譯文】等任何标记，不要额外解释", src, cn)
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

// resolveModel 解析当前请求使用的模型配置（平台统一网关，2026-08-26 BYOK 移除）。
// 优先级：全局 ModelRoutes（按权重选主路由）→ 全局默认 Online* 配置。
// ★ 商业口径（决策人定稿）：所有 LLM 调用一律经平台网关出站，租户侧无任何模型配置入口，
//
//	token 定价权 100% 归平台——原「租户 BYOK 路由 → 租户单模型」两级读取链已删除。
func (e *Engine) resolveModel(ctx context.Context) (base, key, model string) {
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
	// ★ 库内密文解密（评审整改 D3）：stage_models 的 api_key 以 enc:v1: 落库
	sm.APIKey = store.DecryptSecret(sm.APIKey)
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
	// ★ 缩翻（任务7）：启用时向指令追加最长字符限制，提示模型精简输出
	if ml := maxLengthFromCtx(ctx); ml > 0 {
		instruction += fmt.Sprintf(" 译文总长度（含标点）不得超过 %d 个字符。请在保留原意与关键信息的前提下尽量精简，不要额外解释，只输出译文。", ml)
	}
	ref := buildExamplesPrompt(zhText, targetLang, examples)

	// 术语参考放入 system 消息（模型不会复述 system 内容），user 只含指令+待翻译文本
	// ★ Gate L1：语言文化规范注入（approved 安全句按目标语言，60s 缓存）
	cultureBlock := ""
	if tid := tenant.FromContext(ctx); tid > 0 {
		cultureBlock, _ = e.cultureRules(ctx, tid, targetLang)
	}
	sysNote := "翻译时请沿用以上参考中的专有词/术语译法，但只输出待翻译文本的翻译结果，不得输出或复述参考内容。"
	if cultureBlock != "" {
		sysNote += cultureBlock
	}
	var messages []map[string]string
	if ref != "" {
		langName := config.LangNames[targetLang]
		if langName == "" {
			langName = targetLang
		}
		messages = []map[string]string{
			{"role": "system", "content": ref + "\n" + sysNote},
			{"role": "user", "content": instruction + "\n\n" + zhText},
		}
	} else if cultureBlock != "" {
		messages = []map[string]string{
			{"role": "system", "content": strings.TrimPrefix(sysNote, "翻译时请沿用以上参考中的专有词/术语译法，但只输出待翻译文本的翻译结果，不得输出或复述参考内容。\n")},
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

	// 模型路由策略：配置了全局多供应商路由时，主模型失败按权重降序逐一降级。
	// 阶段模型（stageActive）同样参与多供应商降级：以阶段模型为主，路由链其余供应商为降级候选，
	// 避免「配置了阶段模型就失去 failover」的单点风险（整改 R-M5）。
	// ★ 2026-08-26 BYOK 移除：原「租户 BYOK 路由优先」分支删除——该分支还有两处
	//   缺陷（降级链含主路由自身导致双倍请求放大；租户端点误配 Hunyuan 兜底模型必 404），随分支一并消除。
	var routeFallbacks []config.ProviderConfig
	if len(e.Cfg.ModelRoutes) > 0 {
		if stageActive {
			routeFallbacks = e.resolveRouteFallbacks(config.ProviderConfig{APIBase: base, APIKey: key, Model: model})
		} else {
			primary := e.pickPrimaryRoute()
			routeFallbacks = e.resolveRouteFallbacks(primary)
		}
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
		// 路由链全部失败或无路由配置 → 单 fallback（用主供应商基址/密钥，避免沿用已失败阶段模型所在供应商）
		if err != nil {
			fbase, fkey, _ := e.resolveModel(ctx)
			fallback := cfg.HunyuanFallbackModel
			if isRateLimited(err) {
				time.Sleep(2 * time.Second)
			}
			content, finishReason, err = e.LLM.CallChat(ctx, fbase, fkey, fallback, messages, maxTokens, false, cfg.FallbackTemp)
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
		// ★ 2026-09-03 长文本丢内容修复：上限由 8192 提到 16384——
		//   长文一次调用 maxTokens 可能已到 8192，翻倍被旧上限钳制无法扩容，译文被截断在中段。
		if maxTokens*2 <= 16384 {
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

	// ★ 缩翻硬闸（任务7）：译文总长超过上限则附提示重译（≤2 次），仍超长返回最近一次结果
	if ml := maxLengthFromCtx(ctx); ml > 0 {
		for retry := 0; retry < 2 && len([]rune(content)) > ml; retry++ {
			fb := fmt.Sprintf("译文长度为 %d 字符，超过最长限制 %d 字符。请压缩精简至不超过 %d 字符（含标点），保留原意与关键信息，只输出压缩后的译文。",
				len([]rune(content)), ml, ml)
			rev := e.translateWithFeedbackEx(ctx, zhText, targetLang, fb, stage, examples)
			if rev == "" {
				break
			}
			content = rev
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
	// 中段截断（长文本）检测：按句子终结符数量核对——译文终结符明显少于原文
	// （如长文被 max_tokens 截断在中段，80% 长度比可能恰好通过），判定不完整。
	if zhLen > 200 && countSentenceEnds(result) < countSentenceEnds(zhText) {
		return true
	}
	if !isEndPunct(result) {
		return true
	}
	return false
}

// countSentenceEnds 统计文本中的句子终结符数量（。！？；．.!?; 等）。
// 用于长文本截断检测：译文终结符数若明显少于原文，说明被截断在中段。
func countSentenceEnds(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case '。', '！', '？', '；', '.', '!', '?', ';':
			n++
		}
	}
	return n
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
	// 全量重翻（★ 2026-09-03 长文本丢内容修复：上限 8192→16384，避免超长源文仍被截断）
	instruction := translateInstruction(sourceLang, targetLang, uiLangFromCtx(ctx))
	full := fmt.Sprintf("%s。必须完整翻译，不要省略任何内容，不要被截断：\n\n%s", instruction, zhText)
	messages = []map[string]string{{"role": "user", "content": full}}
	content, _, err = e.LLM.CallChat(ctx, base, key, model, messages, 16384, false, e.Cfg.FallbackTemp)
	if err == nil && content != "" {
		return PostProcessTranslation(content, targetLang)
	}
	return ""
}

// mergeContinuation 词级重叠去重拼接（★ 2026-09-03 长文本丢内容修复）：
// 中日韩等无空格语言经 strings.Fields 会被整段当作一个「词」，旧实现退化为纯拼接，
// 造成续翻处重复/缺字。现在 CJK 占比高时按字符级重叠去重拼接。
func mergeContinuation(original, continuation string) string {
	if strings.TrimSpace(original) == "" || strings.TrimSpace(continuation) == "" {
		return strings.TrimSpace(original) + " " + strings.TrimSpace(continuation)
	}
	// CJK 字符占比高 → 字符级拼接
	cjk := 0
	total := 0
	for _, r := range original {
		total++
		if r >= '\u4e00' && r <= '\u9fff' || r >= '\u3040' && r <= '\u30ff' || r >= '\uac00' && r <= '\ud7af' {
			cjk++
		}
	}
	if total > 0 && cjk*2 >= total {
		orig := []rune(original)
		cont := []rune(continuation)
		maxOverlap := 20
		if len(orig) < maxOverlap {
			maxOverlap = len(orig)
		}
		for n := maxOverlap; n >= 1; n-- {
			tail := orig[len(orig)-n:]
			if len(cont) >= n && equalRunes(tail, cont[:n]) {
				return string(orig) + string(cont[n:])
			}
		}
		return string(orig) + string(cont)
	}
	origWords := strings.Fields(original)
	contWords := strings.Fields(continuation)
	if len(origWords) == 0 || len(contWords) == 0 {
		return original + " " + continuation
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

// equalRunes 逐字符比较两个 rune 切片是否完全相等（CJK 续翻拼接重叠检测用）。
func equalRunes(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

	log.Printf("[llm-batch] start lang=%s segs=%d", targetLang, len(texts))
	defer func(t0 time.Time) {
		log.Printf("[llm-batch] done  lang=%s segs=%d took=%s", targetLang, len(texts), time.Since(t0).Round(time.Millisecond))
	}(time.Now())

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

	// ★ 统一网关接入（2026-08-26 全仓评审 B6）：批量主路与单语路径共用同一套
	//   「全局路由（按权重选主）→ 全局默认」解析与 ai_initial 阶段模型覆盖——
	//   此前直用 cfg.Online* 常量，使文件管线（主力负载）完全绕过
	//   多供应商路由/降级链，与 BYOK 移除后「平台统一调度」的商业口径冲突。
	base, key, model := e.resolveModel(ctx)
	if b2, k2, m2, ok := e.resolveStageModel(ctx, config.StageAIInitial); ok {
		base, key, model = b2, k2, m2
	}
	hunyuan := strings.HasPrefix(model, "tencent/Hunyuan-MT")
	if hunyuan && !cfg.HunyuanMTLangCode[targetLang] {
		model = cfg.HunyuanFallbackModel
		hunyuan = false
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 2) // 初始 2 批并发

	// runChunk 翻译一个段块并按 <sN> 标记解析写回 result[start:]；返回命中数。
	runChunk := func(start int, chunk []string) int {
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

		content, _, err := e.LLM.CallChat(ctx, base, key, model, messages, maxTokens, hunyuan, cfg.FallbackTemp)
		// 429/网络错误 → 429 先 sleep 5s 避峰，再用 fallback 模型重试一次
		//（base/key 沿用解析结果，不再回退 cfg.Online* 常量）
		if err != nil && (isRateLimited(err) || isNetworkError(err)) {
			if isRateLimited(err) {
				time.Sleep(5 * time.Second)
			}
			content, _, err = e.LLM.CallChat(ctx, base, key, cfg.HunyuanFallbackModel, messages, maxTokens, false, cfg.FallbackTemp)
		}
		if err == nil {
			// 记录批量路径实际使用的供应商与模型（usageRecord 已加锁，多 chunk 并发安全）
			e.NoteUsageModel(ctx, usageProvider(base), model)
		}

		parsed := parseBatchOutput(content, len(chunk))
		hit := 0
		mu.Lock()
		for i, tr := range parsed {
			if i < len(chunk) && tr != "" {
				result[start+i] = PostProcessTranslation(tr, targetLang)
				hit++
			}
		}
		mu.Unlock()
		return hit
	}

	// 第一遍：动态批大小分块并发；解析率 <90% 的块记录待二次收编
	type poorRange struct{ start, end int }
	var poor []poorRange
	var poorMu sync.Mutex
	for start := 0; start < len(texts); start += bs {
		end := start + bs
		if end > len(texts) {
			end = len(texts)
		}
		chunk := texts[start:end]
		wg.Add(1)
		go func(start int, chunk []string) {
			defer wg.Done()
			defer recoverPipeline("batch_chunk") // 整改 D4
			sem <- struct{}{}
			defer func() { <-sem }()
			hit := runChunk(start, chunk)
			if hit < len(chunk)*9/10 && len(chunk) > 5 {
				poorMu.Lock()
				poor = append(poor, poorRange{start: start, end: start + len(chunk)})
				poorMu.Unlock()
			}
			if onBatchDone != nil {
				onBatchDone(start+len(chunk), len(texts))
			}
		}(start, chunk)
	}
	wg.Wait()

	// ★ 二次收编（评审整改 R5）：低解析率块改用更小块(bs=5)重发一次，
	//   把「批量格式漂移」就地修复，避免大段退化到逐段兜底。
	for _, pr := range poor {
		const sub = 5
		for s0 := pr.start; s0 < pr.end; s0 += sub {
			e0 := s0 + sub
			if e0 > pr.end {
				e0 = pr.end
			}
			allSet := true
			for i := s0; i < e0; i++ {
				if strings.TrimSpace(result[i]) == "" {
					allSet = false
					break
				}
			}
			if allSet {
				continue
			}
			chunk := texts[s0:e0]
			wg.Add(1)
			go func(start int, chunk []string) {
				defer wg.Done()
				defer recoverPipeline("batch_retry_chunk") // 整改 D4
				sem <- struct{}{}
				defer func() { <-sem }()
				runChunk(start, chunk)
			}(s0, chunk)
		}
	}
	wg.Wait()

	// 兜底：仍未译出的段——★ 有界并行逐段翻译（评审整改 R5，原实现纯串行，
	// 一段百句文档会退化为上百次串行往返）
	fbSem := make(chan struct{}, 3)
	for i, tr := range result {
		if strings.TrimSpace(tr) != "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer recoverPipeline("batch_fallback") // 整改 D4
			fbSem <- struct{}{}
			defer func() { <-fbSem }()
			s2, err := e.singleLang(ctx, texts[i], targetLang, nil, "zh", config.StageAIInitial, 0)
			out := "[翻译失败]"
			if err == nil && strings.TrimSpace(s2) != "" {
				out = s2
			}
			mu.Lock()
			result[i] = out
			mu.Unlock()
		}(i)
	}
	wg.Wait()
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

// 编译期引用占位：保留 encoding/json 导入（条件编译路径使用）。
var _ = json.Marshal

// RebuildKBIndex 全量重建向量索引：读 tm_segments 全部中文原文 → bge-m3 嵌入 → 写回 npz 并热替换。
// 返回成功嵌入的行数。并发调用安全（同一时刻仅一个重建）。
func (e *Engine) RebuildKBIndex(ctx context.Context) (int, error) {
	if e == nil || e.DB == nil || e.Index == nil || e.NPZPath == "" {
		return 0, fmt.Errorf("向量索引未初始化")
	}
	// ★ Embed 成本计量（评审整改 E3）：全量重建的 embedding 用量进入 usage_ledger——
	//   按知识库包类型分摊到各租户；行业/语言文化等全局包免费，租户/部门包按字符比例计费。
	ctx = e.WithUsageRecorder(ctx)

	// 预加载所有知识库包类型，用于判断当前行是否属于全局包。
	packType := map[int64]string{}
	if e.St != nil {
		rows, err := e.St.DB().Query("SELECT id, pack_type FROM kb_packages")
		if err == nil {
			for rows.Next() {
				var id int64
				var pt string
				if err := rows.Scan(&id, &pt); err == nil {
					packType[id] = pt
				}
			}
			rows.Close()
		}
	}
	isGlobalPack := func(pid int64) bool {
		pt := packType[pid]
		return pt == store.PackIndustry || pt == store.PackLocale
	}

	if !e.rebuilding.CompareAndSwap(false, true) {
		return 0, fmt.Errorf("重建正在进行中")
	}
	defer e.rebuilding.Store(false)

	// 知识库 Embed 阶段配置优先（stage_models.kb_embed）；随 ctx 透传（整改 R3）
	if eb, ek, em, ok := e.resolveStageModel(ctx, config.StageKBEmbed); ok {
		ctx = llm.WithEmbedOverride(ctx, eb, ek, em)
	}

	rows, err := e.DB.AllRowsWithTenant()
	if err != nil {
		return 0, err
	}
	idLangs, _ := e.DB.AllRowLangs(-1)

	newIdx := &kb.Index{IDs: make([]int64, 0, len(rows)), Vecs: make([][]float32, 0, len(rows)),
		IDLangs: idLangs, IDTenants: map[int64]int64{}, IDPacks: map[int64]int64{}}
	const batch = 32
	done := 0
	usageByTenant := map[int64]int64{} // 各租户累计 embedding token
	for i := 0; i < len(rows); i += batch {
		end := i + batch
		if end > len(rows) {
			end = len(rows)
		}
		texts := make([]string, 0, end-i)
		for _, r := range rows[i:end] {
			texts = append(texts, r.Zh)
		}
		// 按调用前后用量差计算本批次实际消耗的 token。
		beforePrompt, beforeComp := e.UsageTokens(ctx)
		vecs, err := e.LLM.EmbedBatch(ctx, texts)
		if err != nil {
			return done, fmt.Errorf("第 %d-%d 批嵌入失败: %w", i, end, err)
		}
		afterPrompt, afterComp := e.UsageTokens(ctx)
		deltaTotal := (afterPrompt + afterComp) - (beforePrompt + beforeComp)

		// 按租户累计字符数；全局包或平台行免费，不计入分摊。
		charsByTenant := map[int64]int64{}
		var totalChars int64
		for _, r := range rows[i:end] {
			if isGlobalPack(r.PackID) || r.TenantID <= 0 {
				continue
			}
			chars := int64(len(strings.TrimSpace(r.Zh)))
			if chars <= 0 {
				continue
			}
			charsByTenant[r.TenantID] += chars
			totalChars += chars
		}
		// 将本批次 token 按字符比例分摊给各租户。
		if deltaTotal > 0 && totalChars > 0 {
			for tid, chars := range charsByTenant {
				share := deltaTotal * chars / totalChars
				if share > 0 {
					usageByTenant[tid] += share
				}
			}
		}

		for j, r := range rows[i:end] {
			if j >= len(vecs) || len(vecs[j]) == 0 {
				continue
			}
			newIdx.IDs = append(newIdx.IDs, r.ID)
			newIdx.Vecs = append(newIdx.Vecs, vecs[j])
			newIdx.IDTenants[r.ID] = r.TenantID
			newIdx.IDPacks[r.ID] = r.PackID
			done++
		}
	}
	if done == 0 {
		return 0, fmt.Errorf("无有效向量生成")
	}
	// 写盘 + 热替换
	if err := kb.SaveNPZ(e.NPZPath, newIdx.IDs, newIdx.Vecs); err != nil {
		return done, err
	}
	e.indexMu.Lock()
	e.Index = newIdx
	e.indexMu.Unlock()

	// pgvector 双写：将重建得到的向量同步写入 tm_segments.embedding（仅 PostgreSQL + 已安装 pgvector）。
	// 与 npz 并存；pgvector 不可用（未安装扩展/列缺失）时最佳努力跳过，不阻断主流程。
	if db.CurrentDialect() == db.DialectPostgres {
		for n := 0; n < len(newIdx.IDs); n++ {
			_ = e.DB.UpsertEmbedding(newIdx.IDs[n], newIdx.Vecs[n])
		}
	}

	// 统一计费（P1-3）：经全局 sink 落库，与翻译用量共用同一扣减入口与批量写事务，
	// 消除「重建直写库」造成的第二计费源与 SQLITE_BUSY 回归；是否扣余额由 sink 按
	// billing_enforced 自行决定（此处不再分支）。
	if e.St != nil {
		provider, model := "bigmodel", "embedding-rebuild"
		for tid, tokens := range usageByTenant {
			if tokens <= 0 || tid <= 0 {
				continue
			}
			billing.RecordUsage(tid, 0, "kb_embed", provider, model, tokens, "kb", "index", nil)
		}
	}

	return done, nil
}

// Rebuilding 是否正在重建向量索引。
func (e *Engine) Rebuilding() bool { return e.rebuilding.Load() }

// cultureRules 加载租户可应用语言文化包中指定目标语言的已审核规则（60s 缓存）。
// 返回渲染后的提示词块（L1）与结构化规则（L2 硬过滤）；无规则返回空。
func (e *Engine) cultureRules(ctx context.Context, tid int64, targetLang string) (string, []cultureRule) {
	if e.St == nil || tid <= 0 || targetLang == "" {
		return "", nil
	}
	key := fmt.Sprintf("%d|%s", tid, targetLang)
	e.cultureMu.Lock()
	defer e.cultureMu.Unlock()
	if e.cultureCache == nil {
		e.cultureCache = map[string]cultureEntry{}
		e.cultureEntries = map[string][]cultureRule{}
	}
	if ce, ok := e.cultureCache[key]; ok && time.Now().Before(ce.ExpiresAt) {
		return ce.Text, ce.Rules
	}
	rows, err := e.St.DB().Query(`
		SELECT sp.phrase, COALESCE(sp.kind,'style'), COALESCE(sp.replacement,'')
		FROM kb_safety_phrases sp
		JOIN kb_packages pkg ON pkg.id = sp.package_id
		WHERE COALESCE(sp.status,'approved')='approved' AND sp.lang=?
AND COALESCE(pkg.enabled,1)=1 AND pkg.pack_type='locale'
	  AND pkg.tenant_id IN (?, 0)`, targetLang, tid)
	if err != nil {
		return "", nil
	}
	defer rows.Close()
	var rules []cultureRule
	for rows.Next() {
		var r cultureRule
		if err := rows.Scan(&r.Phrase, &r.Kind, &r.Replacement); err == nil && r.Phrase != "" {
			rules = append(rules, r)
		}
	}
	rows.Close()
	var sb strings.Builder
	if len(rules) > 0 {
		sb.WriteString("\n【语言文化规范（必须遵守，违反将被拒绝）】\n")
		for _, r := range rules {
			switch r.Kind {
			case "style":
				sb.WriteString("· 写作规范：" + r.Phrase + "\n")
			case "forbidden":
				// ★ 2026-09-03：中性常见词（control/hot 等）不作为 LLM 禁令指令，避免误导模型避开正常词
				if culture.IsNeutralForbidden(targetLang, r.Phrase) {
					continue
				}
				sb.WriteString("· 严禁在译文中出现：" + r.Phrase + "\n")
			case "replace":
				sb.WriteString("· 用词替换：译文中的「" + r.Phrase + "」必须写作「" + r.Replacement + "」\n")
			}
		}
	}
	text := sb.String()
	// ★ 性能优化（不换库 Phase A4）：条目数封顶，超限整代清空后仅保留本次写入，
	//   防止不同语言/租户组合随时间无限累积（TTL 仅控制命中、不回收内存）。
	if len(e.cultureCache) >= cultureCacheMax {
		e.cultureCache = map[string]cultureEntry{}
		e.cultureEntries = map[string][]cultureRule{}
	}
	e.cultureCache[key] = cultureEntry{Text: text, Rules: rules, ExpiresAt: time.Now().Add(cultureCacheTTL)}
	e.cultureEntries[key] = rules
	return text, rules
}

// applyCultureGate L2 硬闸门：replace 对自动替换；forbidden 命中记录违规。
// 返回处理后的文本与违规列表。违规始终记录；是否拦截由调用方按 locale_gate_enforced 决定。
func (e *Engine) applyCultureGate(ctx context.Context, tid int64, targetLang, text string) (string, []string) {
	_, rules := e.cultureRules(ctx, tid, targetLang)
	var violations []string
	for _, r := range rules {
		switch r.Kind {
		case "replace":
			if r.Replacement != "" && strings.Contains(text, r.Phrase) {
				text = strings.ReplaceAll(text, r.Phrase, r.Replacement)
			}
		case "forbidden":
			// 中性常见词豁免 + 整词边界匹配，避免 control/hot 等普通词汇误拦正常译文
			if culture.ForbiddenHit(targetLang, text, r.Phrase) {
				violations = append(violations, r.Phrase)
			}
		}
	}
	return text, violations
}

// ReviewTranslationBatch 批量审校：一次 LLM 调用审校同一目标语言的多段译文。
// 编号契约：输入按 1..N 编号列出【原文】/【译文】，要求模型严格按「编号. 审校后译文」逐行输出；
// 仅当编号可解析时应用修正，解析失败或行数不符的段落原样保留（宁缺毋滥）。
// 参数 ctx=上下文（含租户/用量收集器），sources=源文列表，translations=待审校译文列表，
// targetLang=目标语言代码，stage=阶段模型标识（review）。
// 返回: 与 translations 等长的审校结果数组（未修正项保持原值）。
func (e *Engine) ReviewTranslationBatch(ctx context.Context, sources, translations []string, targetLang, stage string) []string {
	out := make([]string, len(translations))
	copy(out, translations) // 缺省回退原值
	if len(sources) == 0 || len(sources) != len(translations) {
		return out
	}
	culture := ""
	if tid := tenant.FromContext(ctx); tid > 0 {
		culture, _ = e.cultureRules(ctx, tid, targetLang)
	}
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	// 拼接编号清单（单段截断 800 字符，防提示词爆炸）
	var list strings.Builder
	for i := range sources {
		fmt.Fprintf(&list, "%d.\n【原文】%s\n【译文】%s\n", i+1,
			truncateStr(sources[i], 800), truncateStr(translations[i], 800))
	}
	prompt := fmt.Sprintf(
		"你是资深翻译审校。下面是 %d 条%s译文（已编号），请逐条审校：仅修正术语准确性和语法错误，保持原意与风格，不改写结构。%s\n\n%s\n严格按以下格式输出，共 %d 行，每行一条，不要输出任何其他内容：\n编号. 审校后的完整译文",
		len(sources), langName, culture, list.String(), len(sources))
	messages := []map[string]string{{"role": "user", "content": prompt}}
	base, key, model := e.resolveModel(ctx)
	if b2, k2, m2, ok := e.resolveStageModel(ctx, stage); ok {
		base, key, model = b2, k2, m2
	}
	content, _, err := e.LLM.CallChat(ctx, base, key, model, messages, 4096, false, e.Cfg.FallbackTemp)
	if err != nil {
		return out // 审校失败：整体回退原值
	}
	applied := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		dot := strings.Index(line, ".")
		if dot <= 0 {
			continue
		}
		idx, perr := strconv.Atoi(strings.TrimSpace(line[:dot]))
		if perr != nil || idx < 1 || idx > len(out) {
			continue
		}
		if revised := strings.TrimSpace(line[dot+1:]); revised != "" {
			out[idx-1] = PostProcessTranslation(revised, targetLang)
			applied++
		}
	}
	if applied == 0 {
		return out // 输出格式不符：全部回退原值（不冒险应用）
	}
	return out
}

// truncateStr 按 rune 截断字符串（超长追加省略号）。
func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
