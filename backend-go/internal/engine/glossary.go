// ============ glossary.go · 职责说明 ============
// 文档级领域词表（2026-10-29 第十二轮 B 方案）的接线层：
//   - 一次/单判定 → 缓存（进程内 + domain_profiles 表）→ 按语种取词表 → 注入提示词。
//   - 词表**只进本单提示词**（用户拍板）：不做硬闸、不写平台包。
//   - 任何失败（开关关/低置信/查不到/wiki 超时）一律 fail-open：翻译照常，prompt 与改造前逐字相同。
//
// 两条注入位置（别抄错）：
//   - 文件/图片：BatchOptions.Glossary → sysBatch 的 system 前缀（编号契约之前）；
//   - 文本/即时：ctx 携带 → singleLangRaw 的 dynNotes（user 尾，不碰稳定 system 前缀）。
//
// ============================================
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"translator/internal/domain"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/store"
)

// wikiClient 薄包装（让 engine.go 的字段类型不必 import domain）。
type wikiClient struct{ w *domain.Wiki }

// glossaryEntry 进程内缓存一行（Profile=nil 是负缓存，表示"这单不做"）。
type glossaryEntry struct {
	Profile   *domain.Profile
	ExpiresAt time.Time
}

// glossaryKey ctx 存取键（私有类型防碰撞，与 llm 包的几个 key 同族）。
type glossaryKey struct{}

// glossaryState ctx 里携带的那一份：本单词表 + 每语种上限。
type glossaryState struct {
	Profile  *domain.Profile
	MaxTerms int
}

// WithGlossary 把本单词表挂到 ctx（文本/即时链的 singleLangRaw 从 user 尾读它）。
func WithGlossary(ctx context.Context, p *domain.Profile, maxTerms int) context.Context {
	if p == nil || len(p.Terms) == 0 {
		return ctx
	}
	return context.WithValue(ctx, glossaryKey{}, &glossaryState{Profile: p, MaxTerms: maxTerms})
}

// glossaryFromCtx 取本单词表与每语种上限；没有返回 (nil, 0)。
func glossaryFromCtx(ctx context.Context) (*domain.Profile, int) {
	if v, ok := ctx.Value(glossaryKey{}).(*glossaryState); ok && v != nil {
		return v.Profile, v.MaxTerms
	}
	return nil, 0
}

// glossaryNote 把词表渲染成提示词里的一句话；空词表返回 ""（关档与"没判出来"逐字等价）。
//
// "ignore any entry that does not fit the context" 是误判的最后一道软闸：
// wiki 标题可能带百科语义（`因子→Factor`），模型可以不用，但不能被它带偏。
func glossaryNote(label string, terms []domain.Term) string {
	if len(terms) == 0 {
		return ""
	}
	if strings.TrimSpace(label) == "" {
		label = "domain-specific"
	}
	var b strings.Builder
	b.WriteString(" These items come from a " + label + " document. A reference glossary was cross-checked")
	b.WriteString(" against public reference sources; prefer these equivalents when the source term appears,")
	b.WriteString(" and ignore any entry that does not fit the context:")
	for _, t := range terms {
		b.WriteString("\n  " + t.Src + " → " + t.Tgt)
	}
	b.WriteString("\n")
	return b.String()
}

// tmGlossaryHash 词表进记忆键的指纹（空词表 ⇒ ""，非空 ⇒ 与顺序无关的稳定标记）。
func tmGlossaryHash(terms []domain.Term) string {
	if len(terms) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(terms))
	for _, t := range terms {
		pairs = append(pairs, t.Src+"\x00"+t.Tgt)
	}
	sort.Strings(pairs)
	sum := sha256.Sum256([]byte(strings.Join(pairs, "|")))
	return "|gl:" + hex.EncodeToString(sum[:8])
}

// GlossaryAllowanceTokens 图片件「本单词表」进 system 前缀的 token 预估（客户承担的那部分）。
//
// 口径与 LiteralOCRAllowanceTokens 同族：每份图片件按 6 个批块估，从**真渲染出的那句话**现量
// （改 glossaryNote 必须同步改预估，等值锁 TestGlossaryAllowanceDerivesFromRealNote 钉住）。
func GlossaryAllowanceTokens(imageFiles, langs, termsPerLang int) int64 {
	if imageFiles <= 0 || langs <= 0 || termsPerLang <= 0 {
		return 0
	}
	const chunksPerImageFile = 6
	sample := make([]domain.Term, 0, termsPerLang)
	for i := 0; i < termsPerLang; i++ {
		sample = append(sample, domain.Term{Src: "术语", Tgt: "term"})
	}
	return int64(imageFiles) * int64(langs) * chunksPerImageFile * ScriptTokens(glossaryNote("domain", sample))
}

// domainGlossaryEnabled 开关：env kill-switch > 租户覆盖 > 全局 > 默认关。
func (e *Engine) domainGlossaryEnabled(tid int64) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DOMAIN_GLOSSARY"))) {
	case "off", "0", "false":
		return false
	}
	if e == nil || e.St == nil || tid <= 0 {
		return false
	}
	if v, _ := e.St.GetConfig(fmt.Sprintf("domain_glossary_enabled.%d", tid)); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v) == "1"
	}
	if v, _ := e.St.GetConfig("domain_glossary_enabled"); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v) == "1"
	}
	return false
}

// DomainGlossaryEnabled 开关的导出读点（预估腿与判定腿共用同一把尺子，避免"收钱不收钱"各判一次）。
func DomainGlossaryEnabled(st *store.Store, tid int64) bool {
	if st == nil {
		return false
	}
	e := &Engine{St: st}
	return e.domainGlossaryEnabled(tid)
}

// GlossaryMaxTerms 预估腿用：某条链每语种词表上限（配置优先，默认见 domain.*Limits）。
func GlossaryMaxTerms(st *store.Store, chain string) int {
	e := &Engine{St: st}
	return e.glossaryLimits(chain).MaxTerms
}

// glossaryConfigInt 读一个正整数配置；缺省/非法/<=0 一律回默认。
func (e *Engine) glossaryConfigInt(name string, def int) int {
	if e == nil || e.St == nil {
		return def
	}
	v, _ := e.St.GetConfig(name)
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return n
	}
	return def
}

// glossaryLimits 某条链的门槛（运营可在 system_config 覆盖；缺省见 domain.*Limits）。
func (e *Engine) glossaryLimits(chain string) domain.Limits {
	lim := domain.FileLimits
	switch chain {
	case "text":
		lim = domain.TextLimits
	case "instant":
		lim = domain.InstantLimits
	}
	lim.MinConfidence = float64(e.glossaryConfigInt("domain_detect_min_conf", int(lim.MinConfidence*100))) / 100
	lim.MinTerms = e.glossaryConfigInt("domain_detect_min_terms", lim.MinTerms)
	lim.MinResolved = e.glossaryConfigInt("domain_detect_min_resolved", lim.MinResolved)
	switch chain {
	case "text":
		lim.MaxTerms = e.glossaryConfigInt("domain_glossary_text_terms", lim.MaxTerms)
	case "instant":
		lim.MaxTerms = e.glossaryConfigInt("domain_glossary_instant_terms", lim.MaxTerms)
	default:
		lim.MaxTerms = e.glossaryConfigInt("domain_glossary_max_terms", lim.MaxTerms)
	}
	return lim
}

// glossaryTTL 高置信 profile 的缓存时长（默认 168h）。
func (e *Engine) glossaryTTL() time.Duration {
	return time.Duration(e.glossaryConfigInt("domain_glossary_ttl_hours", 168)) * time.Hour
}

// glossaryCapOK 每租户每日判定次数上限（默认 500；in-process 计数，重启归零可接受）。
func (e *Engine) glossaryCapOK(tid int64) bool {
	capN := e.glossaryConfigInt("domain_glossary_daily_cap", 500)
	day := time.Now().Format("2006-01-02")
	e.glossaryMu.Lock()
	defer e.glossaryMu.Unlock()
	if e.glossaryDay == nil || e.glossaryDayKey != day {
		e.glossaryDay = map[int64]int{}
		e.glossaryDayKey = day
	}
	return e.glossaryDay[tid] < capN
}

// glossaryCapInc 记一次真实判定（缓存命中不计）。
func (e *Engine) glossaryCapInc(tid int64) {
	day := time.Now().Format("2006-01-02")
	e.glossaryMu.Lock()
	defer e.glossaryMu.Unlock()
	if e.glossaryDay == nil || e.glossaryDayKey != day {
		e.glossaryDay = map[int64]int{}
		e.glossaryDayKey = day
	}
	e.glossaryDay[tid]++
}

// glossaryBlocked 组装「不入词表」的源词（W3/W4）：租户品牌术语 + 可见域里已有的术语层。
//
// 必须在 Detector 查 wiki **之前**生效：品牌名/内部代号不能作为查询词发给维基。
func (e *Engine) glossaryBlocked(ctx context.Context, tid int64, sample string) map[string]bool {
	out := map[string]bool{}
	if e.St == nil {
		return out
	}
	for _, perLang := range e.fetchBrandTerms(ctx, sample) {
		for src := range perLang {
			if v := strings.ToLower(strings.TrimSpace(src)); v != "" {
				out[v] = true
			}
		}
	}
	if srcs, err := e.St.TermSourcesAllowed(packAllowSet(e.userScope(ctx, tid))); err == nil {
		for s := range srcs {
			out[s] = true
		}
	}
	return out
}

// glossaryBudget 每条链的总预算（域判定 + wiki 共享）。
//
// `domain_detect_timeout_ms`（默认 2500）是**判定腿**的预算；总预算按链派生：
// 文件 = 判定×2（约 5s）、文本 = 判定＋1.2s（约 3.7s）、即时固定 600ms（首字延迟敏感）。
func (e *Engine) glossaryBudget(chain string) time.Duration {
	d := time.Duration(e.glossaryConfigInt("domain_detect_timeout_ms", 2500)) * time.Millisecond
	switch chain {
	case "instant":
		return 600 * time.Millisecond
	case "text":
		return d + 1200*time.Millisecond
	default:
		return d * 2
	}
}

// wikiClient 懒建的维基客户端（进程内共享一份词条缓存）。
func (e *Engine) wikiClient() *wikiClient {
	e.glossaryMu.Lock()
	defer e.glossaryMu.Unlock()
	if e.glossaryWiki == nil {
		w := domain.NewWiki()
		if n := e.glossaryConfigInt("domain_wiki_timeout_ms", 0); n >= 100 && n <= 10000 {
			w.HTTP.Timeout = time.Duration(n) * time.Millisecond
		}
		e.glossaryWiki = &wikiClient{w: w}
	}
	return e.glossaryWiki
}

// glossaryRemember 写进程内缓存；p=nil 是负缓存（这单不做，别重复烧判定）。
func (e *Engine) glossaryRemember(fp string, p *domain.Profile, exp time.Time) {
	e.glossaryMu.Lock()
	defer e.glossaryMu.Unlock()
	if e.glossaryCache == nil {
		e.glossaryCache = map[string]glossaryEntry{}
	}
	if len(e.glossaryCache) >= 4096 {
		e.glossaryCache = map[string]glossaryEntry{}
	}
	e.glossaryCache[fp] = glossaryEntry{Profile: p, ExpiresAt: exp}
}

// glossaryLLMCall 判定调用的本地签名（与 domain.LLMCall 同形；不为此让 engine.go import domain）。
type glossaryLLMCall func(ctx context.Context, system, user string) (string, error)

// domainLLMCall 判定调用的闭包：全局主模型 + 写死关思考 + 平台承担。
//
// 为什么写死关思考：主模型可能是推理系（GLM-Z1 那一族），先吐思维链再给 JSON 会把
// 600/2500ms 预算与 max_tokens 一起吃光（ocr_fix 六次运行全是 tokens:0/timeout 就是这条）。
func (e *Engine) domainLLMCall() domain.LLMCall {
	if e.glossaryCall != nil { // 测试注入口（生产恒 nil）
		return domain.LLMCall(e.glossaryCall)
	}
	return func(ctx context.Context, system, user string) (string, error) {
		if e.LLM == nil {
			return "", errors.New("no_llm")
		}
		base, key, model := e.resolveModel(ctx)
		if e.St != nil {
			if m, _ := e.St.GetConfig("domain_detect_model"); strings.TrimSpace(m) != "" {
				model = strings.TrimSpace(m)
			}
		}
		if strings.TrimSpace(base) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(model) == "" {
			return "", errors.New("no_model")
		}
		cctx := llm.WithChatExtraParams(
			llm.WithPlatformCost(llm.WithInteractive(llm.WithoutFileMode(ctx)), llm.PlatformDomainDetect),
			noThinkingExtra())
		content, finish, err := e.LLM.CallChat(cctx, base, key, model,
			[]map[string]string{
				{"role": "system", "content": system},
				{"role": "user", "content": user},
			}, 600, false, 0)
		if err != nil {
			return "", err
		}
		if finish == "length" {
			return "", errors.New("truncated")
		}
		return content, nil
	}
}

// glossaryProfile 取（或算）本单的域词表；任何失败返回 nil（fail-open）。
// chain: "file" / "text" / "instant"（只影响预算与日志）。
func (e *Engine) glossaryProfile(ctx context.Context, tid int64, chain, sample string, langs []string, lim domain.Limits) *domain.Profile {
	if !e.domainGlossaryEnabled(tid) || len(langs) == 0 {
		return nil
	}
	sample = strings.TrimSpace(sample)
	if len([]rune(domain.Normalize(sample))) < lim.MinSampleRunes {
		return nil
	}
	fp := domain.Fingerprint(tid, sample)
	now := time.Now()
	e.glossaryMu.Lock()
	if ent, ok := e.glossaryCache[fp]; ok && now.Before(ent.ExpiresAt) {
		p := ent.Profile
		e.glossaryMu.Unlock()
		return p
	}
	e.glossaryMu.Unlock()
	// 跨进程缓存（DB）：内容哈希为键，没有主动失效。
	if row, err := e.St.GetDomainProfile(fp, now); err == nil && row != nil {
		var p domain.Profile
		if json.Unmarshal([]byte(row.TermsJSON), &p) == nil && len(p.Terms) > 0 {
			e.glossaryRemember(fp, &p, now.Add(24*time.Hour))
			observability.Info(ctx, "domain_glossary",
				"chain", chain, "cache", "hit", "code", p.Code, "terms", len(p.Terms))
			return &p
		}
	}

	// ★ 每租户每日上限（默认 500）：缓存命中不计；超限写 1h 负缓存，别让每单都白探一次。
	if !e.glossaryCapOK(tid) {
		e.glossaryRemember(fp, nil, now.Add(time.Hour))
		observability.Info(ctx, "domain_glossary", "chain", chain, "reason", "daily_cap")
		return nil
	}
	e.glossaryCapInc(tid)

	started := time.Now()
	bctx, cancel := context.WithTimeout(ctx, e.glossaryBudget(chain))
	defer cancel()
	d := &domain.Detector{
		Call:    e.domainLLMCall(),
		Wiki:    e.wikiClient().w,
		Blocked: e.glossaryBlocked(ctx, tid, sample), // W3/W4：品牌与术语层在查 wiki 之前就剔掉
	}
	p, err := d.Profile(bctx, sample, lim, langs)
	if err != nil {
		reason := err.Error()
		if bctx.Err() != nil {
			reason = "timeout"
		}
		if chain == "instant" && reason == "timeout" {
			// 即时链 600ms 内大概率查不完 wiki：**不写长时间负缓存**，后台把同一 fingerprint 算完写热，
			// 下一次请求直接命中（0 延迟）。写负缓存会把预热结果挡住。
			// 30s 占位：防同一句话在预热期间被并发请求反复触发（预热最多 5s，占位先到期也没关系）。
			e.glossaryRemember(fp, nil, now.Add(30*time.Second))
			go e.glossaryWarm(ctx, tid, fp, sample, langs, lim)
		} else {
			e.glossaryRemember(fp, nil, now.Add(10*time.Minute))
		}
		observability.Info(ctx, "domain_glossary",
			"chain", chain, "reason", reason, "sample_runes", len([]rune(sample)),
			"latency_ms", time.Since(started).Milliseconds())
		return nil
	}
	raw, _ := json.Marshal(p)
	ttl := e.glossaryTTL()
	_ = e.St.PutDomainProfile(&store.DomainProfileRow{
		Fingerprint: fp,
		TenantID:    tid,
		Code:        p.Code,
		Label:       p.Label,
		Confidence:  p.Confidence,
		TermsJSON:   string(raw),
		ExpiresAt:   now.Add(ttl).UTC().Format(time.RFC3339),
	})
	e.glossaryRemember(fp, p, now.Add(ttl))
	pairs := 0
	for _, t := range p.Terms {
		pairs += len(t.Targets)
	}
	observability.Info(ctx, "domain_glossary",
		"chain", chain, "code", p.Code, "conf", p.Confidence,
		"terms", len(p.Terms), "kept", len(p.Terms), "resolved", pairs, "langs", len(langs),
		"latency_ms", time.Since(started).Milliseconds(), "cache", "miss")
	return p
}

// sampleTexts 取样本：前 maxRunes 个字符（段间换行）。判域只看开头这一截，不把整篇送给模型。
func sampleTexts(texts []string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, t := range texts {
		if n >= maxRunes {
			break
		}
		if b.Len() > 0 {
			b.WriteString("\n")
			n++
		}
		r := []rune(t)
		if n+len(r) > maxRunes {
			r = r[:maxRunes-n]
		}
		b.WriteString(string(r))
		n += len(r)
	}
	return b.String()
}

// glossaryWarm 即时链超时后的后台预热（独立 ctx、5s；结果写进程内 + DB 缓存）。
//
// ★★ 这一腿**两条分支都必须出声**（2026-10-30 现网换来的口径）：旧写法两条分支都静默 return，
//
//	于是"换了 detect_model 到底有没有效"这件事在日志上**完全读不出来**——
//	即时链同步那一枪恒 `reason=timeout`（600ms 预算下这是设计），真正干活的就是这一腿，
//	而它一声不吭 ⇒ 现场只剩 `domain_profiles rows=0` 一个孤零零的数，
//	分不清是"模型答不出 JSON""上游 400""wiki 全查不到""样本太短压根没判"哪一种
//	（本批就是靠连拨三次探针才把"样本 162 rune < 即时链门槛 200 ⇒ 静默跳过"
//	和"判了但预热失败"这两件事分开——那种归因成本本该由一行日志付掉）。
//	预算有限、不 flooding：这一腿只在即时链超时后各拨一次，且按指纹去重（同一句话 10 分钟内不再重拨）。
func (e *Engine) glossaryWarm(ctx0 context.Context, tid int64, fp, sample string, langs []string, lim domain.Limits) {
	warmStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d := &domain.Detector{
		Call:    e.domainLLMCall(),
		Wiki:    e.wikiClient().w,
		Blocked: e.glossaryBlocked(ctx, tid, sample),
	}
	p, err := d.Profile(ctx, sample, lim, langs)
	if err != nil || p == nil {
		// 预热失败写一条 10 分钟负缓存：否则同一句话会被反复重试（每请求一次后台调用）。
		e.glossaryRemember(fp, nil, time.Now().Add(10*time.Minute))
		reason := "empty_profile"
		if err != nil {
			reason = err.Error()
		}
		if ctx.Err() != nil {
			reason = "timeout" // 预热预算（5s）到点，与上游慢/排队同形，故一并出 latency
		}
		observability.Info(ctx0, "domain_glossary",
			"chain", "instant", "cache", "warm", "reason", reason,
			"tenant", tid, "sample_runes", len([]rune(sample)),
			"latency_ms", time.Since(warmStart).Milliseconds())
		return
	}
	raw, _ := json.Marshal(p)
	ttl := e.glossaryTTL()
	_ = e.St.PutDomainProfile(&store.DomainProfileRow{
		Fingerprint: fp,
		TenantID:    tid,
		Code:        p.Code,
		Label:       p.Label,
		Confidence:  p.Confidence,
		TermsJSON:   string(raw),
		ExpiresAt:   time.Now().Add(ttl).UTC().Format(time.RFC3339),
	})
	e.glossaryRemember(fp, p, time.Now().Add(ttl))
	pairs := 0
	for _, t := range p.Terms {
		pairs += len(t.Targets)
	}
	observability.Info(ctx0, "domain_glossary",
		"chain", "instant", "cache", "warm", "code", p.Code, "conf", p.Confidence,
		"terms", len(p.Terms), "resolved", pairs, "langs", len(langs), "tenant", tid,
		"latency_ms", time.Since(warmStart).Milliseconds())
}
