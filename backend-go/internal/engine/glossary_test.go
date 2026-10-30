// ============ glossary_test.go · 职责说明 ============
// 文档级领域词表的接线锁（2026-10-29 第十二轮）：
//
//	空词表逐字等价 / 记忆键折词表指纹 / allowance 从真注记现量 / 开关三档 /
//	DB 缓存命中（不拨模型）/ 关思考与异步预热的接线（静态：这两条没法在单测里真拨模型）。
//
// ============================================
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/domain"
	"translator/internal/store"
	"translator/internal/tenant"
)

func TestGlossaryNoteEmptyAndStable(t *testing.T) {
	if got := glossaryNote("量化交易", nil); got != "" {
		t.Fatalf("空词表必须返回空串（prompt 与改造前逐字相同），实得 %q", got)
	}
	terms := []domain.Term{{Src: "动量", Tgt: "Momentum"}, {Src: "因子", Tgt: "Factor"}}
	a := glossaryNote("量化交易", terms)
	if !strings.Contains(a, "动量 → Momentum") || !strings.Contains(a, "因子 → Factor") {
		t.Fatalf("注记没把词对渲染出来：%q", a)
	}
	if a != glossaryNote("量化交易", terms) {
		t.Fatalf("同一输入两次渲染必须一致")
	}
	if !strings.Contains(a, "ignore any entry that does not fit the context") {
		t.Fatalf("缺「不适合就忽略」这句软闸：%q", a)
	}
}

func TestTMContextHashIncludesGlossary(t *testing.T) {
	if tmContextHash(BatchOptions{}) != "" {
		t.Fatalf("两个开关都关时记忆键必须是空串（否则上线当天全站历史命中作废）")
	}
	base := tmContextHash(BatchOptions{HeaderContext: "表头"})
	gl := tmContextHash(BatchOptions{HeaderContext: "表头", Glossary: []domain.Term{{Src: "动量", Tgt: "Momentum"}}})
	if gl == base || gl == "" {
		t.Fatalf("词表必须进记忆键：base=%q gl=%q", base, gl)
	}
	swapped := tmContextHash(BatchOptions{HeaderContext: "表头", Glossary: []domain.Term{{Src: "因子", Tgt: "Factor"}, {Src: "动量", Tgt: "Momentum"}}})
	same := tmContextHash(BatchOptions{HeaderContext: "表头", Glossary: []domain.Term{{Src: "动量", Tgt: "Momentum"}, {Src: "因子", Tgt: "Factor"}}})
	if swapped != same {
		t.Fatalf("词表顺序不该影响记忆键：%q vs %q", swapped, same)
	}
}

func TestGlossaryAllowanceDerivesFromRealNote(t *testing.T) {
	const n = 3
	sample := make([]domain.Term, 0, n)
	for i := 0; i < n; i++ {
		sample = append(sample, domain.Term{Src: "术语", Tgt: "term"})
	}
	want := int64(2) * int64(3) * 6 * ScriptTokens(glossaryNote("domain", sample))
	if got := GlossaryAllowanceTokens(2, 3, n); got != want {
		t.Fatalf("allowance 必须从真注记现量：got=%d want=%d", got, want)
	}
	if GlossaryAllowanceTokens(0, 3, n) != 0 {
		t.Fatalf("没有图片件时 allowance 应为 0")
	}
}

func TestDomainGlossaryEnabled(t *testing.T) {
	st := newTestStore(t)
	e := &Engine{St: st}
	if e.domainGlossaryEnabled(1) {
		t.Fatalf("默认必须是关")
	}
	if err := st.SetConfig("domain_glossary_enabled", "1"); err != nil {
		t.Fatalf("写配置失败：%v", err)
	}
	if !e.domainGlossaryEnabled(1) {
		t.Fatalf("全局开应生效")
	}
	if err := st.SetConfig("domain_glossary_enabled.1", "0"); err != nil {
		t.Fatalf("写租户配置失败：%v", err)
	}
	if e.domainGlossaryEnabled(1) {
		t.Fatalf("租户覆盖关应优先于全局")
	}
	if !e.domainGlossaryEnabled(2) {
		t.Fatalf("其它租户仍应按全局开")
	}
	t.Setenv("DOMAIN_GLOSSARY", "off")
	if e.domainGlossaryEnabled(2) {
		t.Fatalf("env kill-switch 应压过配置")
	}
}

func TestGlossaryProfileUsesDBCache(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetConfig("domain_glossary_enabled", "1"); err != nil {
		t.Fatalf("写配置失败：%v", err)
	}
	sample := strings.Repeat("量化交易与因子模型。", 30)
	fp := domain.Fingerprint(1, sample)
	raw, _ := json.Marshal(&domain.Profile{
		Code: "quant", Label: "量化交易", Confidence: 0.9,
		Terms: []domain.TermTargets{{Src: "量化交易", Targets: map[string]string{"en": "Quant trading"}}},
	})
	if err := st.PutDomainProfile(&store.DomainProfileRow{
		Fingerprint: fp, TenantID: 1, Code: "quant", Label: "量化交易", Confidence: 0.9,
		TermsJSON: string(raw), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写缓存行失败：%v", err)
	}
	// LLM 为 nil：任何真拨模型都会失败 ⇒ 能拿到 profile 只可能走 DB 缓存。
	e := &Engine{St: st, Cfg: config.Default()}
	p := e.glossaryProfile(context.Background(), 1, "file", sample, []string{"en"}, domain.FileLimits)
	if p == nil || p.Code != "quant" {
		t.Fatalf("应命中 DB 缓存，实得 %+v", p)
	}
	if got := p.TermsFor("en", domain.FileLimits.MaxTerms); len(got) != 1 || got[0].Tgt != "Quant trading" {
		t.Fatalf("缓存词表不对：%+v", got)
	}
	// 关掉开关后必须不读缓存也不调模型。
	if err := st.SetConfig("domain_glossary_enabled", "0"); err != nil {
		t.Fatalf("写配置失败：%v", err)
	}
	e2 := &Engine{St: st, Cfg: config.Default()}
	if p2 := e2.glossaryProfile(context.Background(), 1, "file", sample, []string{"en"}, domain.FileLimits); p2 != nil {
		t.Fatalf("关着开关不许返回词表，实得 %+v", p2)
	}
}

// readEngineSrc 读 engine 包内源文件（静态接线锁用）。
func readEngineSrc(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", name, err)
	}
	return string(raw)
}

// TestDomainLLMNoThinkingWired 判域调用必须写死关思考，且不许有"复开"env。
func TestDomainLLMNoThinkingWired(t *testing.T) {
	src := readEngineSrc(t, "glossary.go")
	if !strings.Contains(src, "llm.WithChatExtraParams(") || !strings.Contains(src, "noThinkingExtra()") {
		t.Fatalf("判域调用没挂 noThinkingExtra（推理模型会把预算吃光）")
	}
	if regexp.MustCompile(`os\.Getenv\("[A-Z_]*THINK[A-Z_]*"\)`).MatchString(src) {
		t.Fatalf("出现了「复开思考」的 env 开关：这条必须写死（ocr_fix 那族的老坑）")
	}
	// 判域调用必须是平台承担（客户没有直接下单这一步）。
	if !strings.Contains(src, "llm.WithPlatformCost(") || !strings.Contains(src, "llm.PlatformDomainDetect") {
		t.Fatalf("判域调用没标平台承担（会扣到客户账上）")
	}
}

// TestInstantWarmWired 即时链超时必须起后台预热（否则长段即时翻译永远吃不到词表），
// 且**预热腿的两条分支各自都要出声**。
//
// 第二条是 2026-10-30 现网换来的：旧写法两条分支都静默 return，而即时链同步那一枪在
// 600ms 预算下**恒** `reason=timeout`（设计如此），真正干活的预热腿一声不吭 ⇒
// 现场只剩 `domain_profiles rows=0` 一个孤数，分不清是"模型答不出 JSON"「上游 400」
// 「wiki 全查不到」还是"样本短于该链门槛、压根没判"。本批连拨三次探针才把后两件事分开，
// 那种归因成本本该由一行日志付掉（AGENTS §三「判'某件事成功过'要问成功路径真会写的东西」）。
func TestInstantWarmWired(t *testing.T) {
	src := readEngineSrc(t, "glossary.go")
	if !strings.Contains(src, "go e.glossaryWarm(ctx, tid, fp, sample, langs, lim)") {
		t.Fatalf("即时链超时后没有后台预热（go e.glossaryWarm(ctx, …)）")
	}
	at := strings.Index(src, "func (e *Engine) glossaryWarm(")
	if at < 0 {
		t.Fatalf("读不到 glossaryWarm 函数体 ⇒ 本锁已失明（不是「预热被删了」）")
	}
	rest := src[at:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("glossaryWarm 函数体切不出来 ⇒ 判据形态变了，本锁要跟着改而不是跳过")
	}
	body := rest[:end+3]
	if n := strings.Count(body, "observability.Info("); n < 2 {
		t.Fatalf("预热腿成功/失败分支各自出声的要求是 ≥2 处，现读 %d 处 ⇒ 零行日志分不开「没拨」与「拨了却失败」", n)
	}
	for _, want := range []string{`"cache", "warm"`, `"reason", reason`} {
		if !strings.Contains(body, want) {
			t.Fatalf("预热腿缺少读数片段 %s（分档名与 cache 档是对外排障契约）", want)
		}
	}
}

// TestGlossaryInjectedIntoDynNotesOnly 文本/即时链只进 user 尾（不碰稳定 system 前缀），
// 文件链进 sysBatch。
func TestGlossaryInjectedIntoDynNotesOnly(t *testing.T) {
	src := readEngineSrc(t, "engine.go")
	// 先判负向（被塞进稳定前缀）——顺序反了的话，M0048（挪到 instrCore）会先撞上"dynNotes 不见了"
	// 那条，变异体读起来像"没接线"，而不是"接错位置"。
	if strings.Contains(src, "instrCore += glossaryNote") || strings.Contains(src, "instrCore = instrCore + glossaryNote") {
		t.Fatalf("词表被塞进 instrCore（稳定 system 前缀会被 per-request 词表打碎）")
	}
	if !strings.Contains(src, "dynNotes.WriteString(glossaryNote(") {
		t.Fatalf("文本/即时链没把词表写进 dynNotes")
	}
	if !strings.Contains(src, "glossaryNote(opts.GlossaryLabel, opts.Glossary)") {
		t.Fatalf("文件链的 sysBatch 没注入词表")
	}
}

// TestInstantTimeoutWarmsInBackground 即时链 600ms 超时 ⇒ 先返回、后台把同一 fingerprint 算热（G10）。
//
// 为什么必须有这条行为腿：只测"预热的函数存在"（静态）会漏掉"预热拿的是发起请求那把被取消的 ctx"
// 这一形态——那会让后台每次都立刻失败，表现与"没预热"完全一样。
func TestInstantTimeoutWarmsInBackground(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetConfig("domain_glossary_enabled", "1"); err != nil {
		t.Fatalf("写配置失败：%v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"query":{"pages":{"1":{"langlinks":[{"lang":"en","*":"EN"}]}}}}`)
	}))
	defer srv.Close()
	w := domain.NewWiki()
	w.Base = srv.URL
	w.HTTP = srv.Client()
	slow := func(ctx context.Context, system, user string) (string, error) {
		select {
		case <-time.After(2 * time.Second):
			return `{"domain":"quant","label":"量化交易","confidence":0.9,"terms":["量化交易","因子模型","回测","动量"]}`, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	e := &Engine{St: st, Cfg: config.Default(), glossaryCall: slow}
	e.glossaryWiki = &wikiClient{w: w}
	sample := strings.Repeat("量化交易与因子模型与回测与动量。", 20)

	start := time.Now()
	p := e.glossaryProfile(context.Background(), 1, "instant", sample, []string{"en"}, e.glossaryLimits("instant"))
	if p != nil {
		t.Fatalf("超时不该返回词表：%+v", p)
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("即时链超时预算失效（应 ≤600ms 返回）：%v", d)
	}
	fp := domain.Fingerprint(1, sample)
	deadline := time.Now().Add(6 * time.Second)
	for {
		e.glossaryMu.Lock()
		ent, ok := e.glossaryCache[fp]
		e.glossaryMu.Unlock()
		if ok && ent.Profile != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("后台预热 6s 内没把缓存写热（预热很可能用了被取消的 ctx）")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestGlossaryBlockedTermsNotSentToWiki W3/W4：品牌术语与已有术语层不得进词表，也不得作为 wiki 查询词。
func TestGlossaryBlockedTermsNotSentToWiki(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetConfig("domain_glossary_enabled", "1"); err != nil {
		t.Fatalf("写配置失败：%v", err)
	}
	pkg, err := st.CreateKBPackage(1, 0, "terms", "术语包", store.PackTenant, store.PackRoleSource)
	if err != nil {
		t.Fatalf("建包失败：%v", err)
	}
	if _, err := st.SaveEntry(1, pkg.ID, 1, "zh", "品牌词", "en", "Brand", "brand"); err != nil {
		t.Fatalf("写品牌术语失败：%v", err)
	}
	if _, err := st.SaveEntry(1, pkg.ID, 1, "zh", "已有术语", "en", "Existing", "term"); err != nil {
		t.Fatalf("写术语失败：%v", err)
	}
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query().Get("titles"))
		mu.Unlock()
		fmt.Fprint(w, `{"query":{"pages":{"1":{"langlinks":[{"lang":"en","*":"EN"}]}}}}`)
	}))
	defer srv.Close()
	w := domain.NewWiki()
	w.Base = srv.URL
	w.HTTP = srv.Client()
	e := &Engine{St: st, Cfg: config.Default(), glossaryCall: func(ctx context.Context, system, user string) (string, error) {
		return `{"domain":"quant","label":"量化交易","confidence":0.9,"terms":["品牌词","已有术语","动量","因子"]}`, nil
	}}
	e.glossaryWiki = &wikiClient{w: w}
	sample := strings.Repeat("品牌词与已有术语与动量与因子。", 20)
	ctx := tenant.WithTenant(context.Background(), 1)
	lim := domain.Limits{MinSampleRunes: 20, MinConfidence: 0.1, MinTerms: 2, MinResolved: 2, MaxTerms: 10}
	p := e.glossaryProfile(ctx, 1, "text", sample, []string{"en"}, lim)
	if p == nil {
		t.Fatalf("应产出词表")
	}
	if len(p.Terms) != 2 {
		t.Fatalf("Blocked 词必须被剔掉，应只剩动量/因子：%+v", p.Terms)
	}
	for _, term := range p.Terms {
		if term.Src == "品牌词" || term.Src == "已有术语" {
			t.Fatalf("Blocked 词进了词表：%+v", p.Terms)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, q := range seen {
		if q == "品牌词" || q == "已有术语" {
			t.Fatalf("Blocked 词被发去维基查询（隐私）：%v", seen)
		}
	}
}

// TestGlossaryProfileFailOpenOnLLMError 判域上游报错 ⇒ 空词表（fail-open），不把工单打回。
func TestGlossaryProfileFailOpenOnLLMError(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetConfig("domain_glossary_enabled", "1"); err != nil {
		t.Fatalf("写配置失败：%v", err)
	}
	e := &Engine{St: st, Cfg: config.Default(), glossaryCall: func(ctx context.Context, system, user string) (string, error) {
		return "", errors.New("boom")
	}}
	sample := strings.Repeat("量化交易与因子模型。", 30)
	p := e.glossaryProfile(tenant.WithTenant(context.Background(), 1), 1, "file", sample, []string{"en"}, domain.FileLimits)
	if p != nil {
		t.Fatalf("上游报错应返回空词表，实得 %+v", p)
	}
}

// TestPrepareTicketGlossaryFailOpen 文本工单链的词表入口必须 fail-open：
// 每一档"不做"都回 (nil, 0)，绝不返回错误、绝不 panic——判不出就照常翻，
// 不许因为一个可选增强把客户整单拖住（同 §19 族处置的总口径）。
func TestPrepareTicketGlossaryFailOpen(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), 1)
	var nilEng *Engine
	if p, n := nilEng.PrepareTicketGlossary(ctx, strings.Repeat("样本内容", 60), []string{"en"}); p != nil || n != 0 {
		t.Fatalf("nil engine 必须回 (nil,0)，实得 %v/%d", p, n)
	}
	e := &Engine{}
	if p, n := e.PrepareTicketGlossary(ctx, strings.Repeat("样本内容", 60), nil); p != nil || n != 0 {
		t.Fatalf("没有目标语种必须回 (nil,0)：本腿判不出任何语种的词表，判了也是白花一次调用")
	}
	// St 为 nil ⇒ 开关读不到 ⇒ 默认关（与 domainGlossaryEnabled 同一条口径）。
	if p, n := e.PrepareTicketGlossary(ctx, strings.Repeat("样本内容", 60), []string{"en"}); p != nil || n != 0 {
		t.Fatalf("开关取不到时必须回 (nil,0)，实得 %v/%d", p, n)
	}
	// 开关开着、但样本短于 TextLimits.MinSampleRunes(120) ⇒ 不做（低置信度判域只会污染提示词）。
	st := newTestStore(t)
	if err := st.SetConfig("domain_glossary_enabled.1", "1"); err != nil {
		t.Fatalf("写开关失败: %v", err)
	}
	e2 := &Engine{St: st}
	if p, n := e2.PrepareTicketGlossary(ctx, "太短的一句话", []string{"en"}); p != nil || n != 0 {
		t.Fatalf("样本不足门槛时必须回 (nil,0)，实得 %v/%d", p, n)
	}
}

// TestPrepareTicketGlossaryPositive 文本工单链真的能拿到词表（含每语种上限）。
// 假判域回 6 个候选词、假维基全部解析成 en ⇒ TextLimits 要求 ≥6 词、每语种 ≥3 命中，应恰好过线。
func TestPrepareTicketGlossaryPositive(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetConfig("domain_glossary_enabled.1", "1"); err != nil {
		t.Fatalf("写开关失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 每个词都回一条 en 标题（词条名带前缀，便于断言确实取到了 wiki 那一路）。
		title := strings.TrimSpace(r.URL.Query().Get("titles"))
		fmt.Fprintf(w, `{"query":{"pages":{"1":{"langlinks":[{"lang":"en","*":"EN-%s"}]}}}}`, title)
	}))
	defer srv.Close()

	e := &Engine{St: st}
	e.glossaryCall = func(ctx context.Context, system, user string) (string, error) {
		return `{"domain":"finance","label":"金融","confidence":0.9,` +
			`"terms":["市盈率","市值","融资融券","换手率","涨停板","集合竞价"]}`, nil
	}
	wk := domain.NewWiki()
	wk.Base, wk.HTTP = srv.URL, srv.Client()
	e.glossaryWiki = &wikiClient{w: wk}

	// 样本必须 ≥ TextLimits.MinSampleRunes(120)：判域对短样本一律不做，这条长度就是那道门槛。
	sample := "今日市盈率与市值同步走高，融资融券余额抬升，换手率放大，涨停板与集合竞价出现异动。" +
		"请核对资产负债表、现金流量表与已实现盈亏的口径，并按全局单票金额复核仓位留空规则。" +
		"若账户出现浮亏，请先确认止损位与保证金比例，再决定是否加仓；数据以交易流水为准。"
	prof, maxTerms := e.PrepareTicketGlossary(tenant.WithTenant(context.Background(), 1), sample, []string{"en"})
	if prof == nil {
		t.Fatalf("判域应成功（假模型回 6 词、假维基全命中），实得 nil")
	}
	if maxTerms != domain.TextLimits.MaxTerms {
		t.Fatalf("每语种上限应取 text 档 %d，实得 %d", domain.TextLimits.MaxTerms, maxTerms)
	}
	terms := prof.TermsFor("en", maxTerms)
	if len(terms) == 0 {
		t.Fatalf("en 词表为空（profile=%v）", prof)
	}
	if !strings.HasPrefix(terms[0].Tgt, "EN-") {
		t.Fatalf("词表应来自 wiki 那一路（EN- 前缀），实得 %q", terms[0].Tgt)
	}
	if prof.DisplayLabel() != "金融" {
		t.Fatalf("标签应透传模型的 label，实得 %q", prof.DisplayLabel())
	}
}

// TestApplyCtxGlossaryFallback 批量腿的词表兜底：ctx 有词表就按**本语种**切片补进 opts，
// opts 显式给的**永远优先**（文件链按语种切好显式传，不能被 ctx 覆盖）。
//
// 为什么这条要用函数级锁：文本工单链把词表挂在 ctx 上（workflow.go 每单判一次），
// 而批量腿过去只认 opts.Glossary ⇒ 逐行腿拿不到。判据三档：
// 空 ctx＝零值（提示词与没有这个特性时逐字相同）／ctx 有词表＝按语种切片填上／opts 优先。
func TestApplyCtxGlossaryFallback(t *testing.T) {
	base := BatchOptions{}
	if got := applyCtxGlossary(context.Background(), "en", base); len(got.Glossary) != 0 {
		t.Fatalf("ctx 没词表时不该填任何东西，实得 %v", got.Glossary)
	}
	prof := &domain.Profile{Code: "finance", Label: "金融", Confidence: 0.9, Terms: []domain.TermTargets{
		{Src: "市盈率", Targets: map[string]string{"en": "P/E ratio", "ja": "PER"}},
		{Src: "市值", Targets: map[string]string{"en": "market cap", "ja": "時価総額"}},
	}}
	ctx := WithGlossary(context.Background(), prof, 5)
	en := applyCtxGlossary(ctx, "en", base)
	if len(en.Glossary) != 2 || en.Glossary[0].Tgt != "P/E ratio" {
		t.Fatalf("应填上 en 两条词表，实得 %v", en.Glossary)
	}
	if en.GlossaryLabel != "金融" {
		t.Fatalf("标签应透传 profile 的中文名，实得 %q", en.GlossaryLabel)
	}
	// 目标语种不在 profile 里 ⇒ 不填（绝不能把别的语种的词表塞进这个语种）。
	if ru := applyCtxGlossary(ctx, "ru", base); len(ru.Glossary) != 0 {
		t.Fatalf("ru 没有对应词表时不该填，实得 %v", ru.Glossary)
	}
	// opts 显式给的赢：文件链显式传的切片不能被 ctx 覆盖。
	explicit := BatchOptions{Glossary: []domain.Term{{Src: "市值", Tgt: "EXPLICIT"}}, GlossaryLabel: "文件链"}
	if got := applyCtxGlossary(ctx, "en", explicit); len(got.Glossary) != 1 || got.Glossary[0].Tgt != "EXPLICIT" || got.GlossaryLabel != "文件链" {
		t.Fatalf("opts 显式给的必须优先，实得 %v/%q", got.Glossary, got.GlossaryLabel)
	}
}

// TestBatchAppliesCtxGlossaryWiring 批量腿必须真调 ctx 兜底，且排在 sysBatch 组装之前。
// 判据是**接线**不是判据：`applyCtxGlossary` 自身有行为锁，但"没人调它"照样全绿（本仓 §三 那条）。
// 顺序也要判：词表既进提示词、也进译文记忆键（tmContextHash），排在组装之后就只进了一处。
func TestBatchAppliesCtxGlossaryWiring(t *testing.T) {
	src := readEngineSrc(t, "engine.go")
	call := strings.Index(src, "opts = applyCtxGlossary(ctx, targetLang, opts)")
	if call < 0 {
		t.Fatalf("批量腿没调 applyCtxGlossary：文本工单的逐行腿会拿不到词表（静默，不报错）")
	}
	batch := strings.Index(src, "sysBatch := translateInstruction(")
	if batch < 0 || call > batch {
		t.Fatalf("ctx 兜底必须排在 sysBatch 组装之前（call=%d sysBatch=%d）", call, batch)
	}
}
