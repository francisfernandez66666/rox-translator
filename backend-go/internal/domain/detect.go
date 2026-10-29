// ============ detect.go · 职责说明 ============
// 文档级判域（2026-10-29 第十二轮 B 方案）：
//
//	一次 LLM 调用 → 域码/标签/置信度/种子词 → 词必须逐字出现在样本里 → 逐词查 wiki →
//	按语种过门槛（低置信/查不到/命中不足 ⇒ 不做）。
//
// 五道护栏（缺一不可）：
//
//	W1 词必须在样本里逐字出现（防 LLM 幻觉）；W2 只收 wiki 查得到的；W3 去数字/纯拉丁（含品牌风险）；
//	W4 与租户术语层去重由 engine 侧做；W5 门槛（样本长度/置信度/词数/每语种命中数）。
//
// 多语种：wiki 一次取全语种（langlinks 不传 lllang），但**站内语种码与维基码只在 wikiLangCode 一处映射**
// （`id_lang→id`；`zh_hant` 没有对应 langlink ⇒ 该语种跳过，不拿简体冒充繁体）。
// ============================================
package domain

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// 判定失败的档位（日志 reason 契约；不区分会让排障只能靠猜）。
var (
	ErrTooShort = errors.New("too_short")
	ErrLowConf  = errors.New("low_conf")
	ErrNoTerms  = errors.New("no_terms")
	ErrNoWiki   = errors.New("no_wiki")
	ErrLLM      = errors.New("llm_error")
	ErrSchema   = errors.New("schema")
)

// detectPrompt 判定提示词（英文：MT 模型会把中文指令当待译内容，同 literalOCRNote 的理由）。
const detectPrompt = `You classify the domain of a short document sample and list domain terms that appear VERBATIM in it.
Return ONE JSON object only:
{"domain":"<snake_case code>","label":"<Chinese label>","confidence":<0..1>,"terms":["<Chinese term>", ...]}
Rules:
- terms must be copied verbatim from the sample, 2 to 12 characters each, 4 to 20 items, no duplicates;
- no numbers, no pure Latin words, no product names;
- if the domain is unclear, set confidence below 0.5 and return an empty terms array;
- do not translate anything in this step.`

// LLMCall 判定调用（engine 注入：解析模型 + CallChat + 关思考）。
type LLMCall func(ctx context.Context, system, user string) (string, error)

// Detector 判域器：LLM 出候选，wiki 取证。
type Detector struct {
	Call LLMCall
	Wiki *Wiki
	// Blocked 不入词表的源词（小写）——W3/W4：租户品牌术语 + 可见域里已有的术语层。
	// 必须在**查 wiki 之前**过滤：品牌名不能作为查询词发给维基（隐私）。
	Blocked map[string]bool
}

// detectReply 判定回执（与 detectPrompt 里的 JSON 契约一一对应）。
type detectReply struct {
	Domain     string   `json:"domain"`
	Label      string   `json:"label"`
	Confidence float64  `json:"confidence"`
	Terms      []string `json:"terms"`
}

// wikiLangCode 站内语种码 → 维基 langlinks 语种码；返回 "" 表示该语种不查。
//
// ★ 这张表只有一处：写错/漏一项＝某语种永远拿不到词表且不报错。
// `zh_hant` 刻意跳过：维基中文只有 `zh`，拿简体冒充繁体是错译。
func wikiLangCode(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	switch l {
	case "id_lang":
		return "id"
	case "zh_hant", "zh-hant":
		return ""
	}
	return l
}

// Profile 跑一次文档级判域。任何一步不达标都返回错误（调用方 fail-open）。
func (d *Detector) Profile(ctx context.Context, sample string, lim Limits, langs []string) (*Profile, error) {
	sample = strings.TrimSpace(sample)
	if len([]rune(Normalize(sample))) < lim.MinSampleRunes {
		return nil, ErrTooShort
	}
	if d == nil || d.Call == nil {
		return nil, ErrLLM
	}
	raw, err := d.Call(ctx, detectPrompt, sample)
	if err != nil {
		return nil, err // 保留原始错误（engine 用 context.DeadlineExceeded 分"超时"与"上游坏"）
	}
	rep, err := parseReply(raw)
	if err != nil {
		return nil, err
	}
	if rep.Confidence < lim.MinConfidence {
		return nil, ErrLowConf
	}
	terms := cleanTerms(rep.Terms, sample, d.Blocked)
	if len(terms) < lim.MinTerms {
		return nil, ErrNoTerms
	}
	// 站内码 → 维基码（zh_hant 等没有 langlink 的语种在这里被摘掉）。
	codeOf := map[string]string{} // our → wiki
	for _, l := range langs {
		if c := wikiLangCode(l); c != "" {
			codeOf[l] = c
		}
	}
	if len(codeOf) == 0 {
		return nil, ErrNoWiki
	}
	targets := d.lookupAll(ctx, terms, codeOf)
	if len(targets) == 0 {
		return nil, ErrNoWiki
	}
	// 每语种命中数门槛：某语种不够 ⇒ 该语种整份不给（其它语种不受影响）。
	resolved := map[string]int{}
	for _, t := range targets {
		for l, c := range codeOf {
			if strings.TrimSpace(t.Targets[c]) != "" {
				resolved[l]++
			}
		}
	}
	drop := map[string]bool{}
	any := false
	for l := range codeOf {
		if resolved[l] < lim.MinResolved {
			drop[l] = true
		} else {
			any = true
		}
	}
	if !any {
		return nil, ErrNoWiki
	}
	out := make([]TermTargets, 0, len(targets))
	for _, t := range targets {
		m := map[string]string{}
		for l, c := range codeOf {
			if drop[l] {
				continue
			}
			if v := strings.TrimSpace(t.Targets[c]); v != "" {
				m[l] = v // 交付键用**站内语种码**（TermsFor 直接按它取）
			}
		}
		if len(m) == 0 {
			continue
		}
		out = append(out, TermTargets{Src: t.Src, Targets: m})
	}
	if len(out) == 0 {
		return nil, ErrNoWiki
	}
	return &Profile{Code: rep.Domain, Label: rep.Label, Confidence: rep.Confidence, Terms: out}, nil
}

// parseReply 解析模型回执（容忍 ``` 包裹；domain 空视为 schema 失败；置信度夹到 0..1）。
func parseReply(raw string) (detectReply, error) {
	var rep detectReply
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	if err := json.Unmarshal([]byte(s), &rep); err != nil {
		return rep, ErrSchema
	}
	rep.Domain = strings.TrimSpace(rep.Domain)
	rep.Label = strings.TrimSpace(rep.Label)
	if rep.Domain == "" {
		return rep, ErrSchema
	}
	if rep.Confidence < 0 {
		rep.Confidence = 0
	}
	if rep.Confidence > 1 {
		rep.Confidence = 1
	}
	return rep, nil
}

// cleanTerms W1+W3+W4：去空白/去重/长度 2~12/必须含汉字且不含数字/必须逐字出现在样本里/
// 不在 Blocked（品牌与术语层）里；上限 20。
func cleanTerms(in []string, sample string, blocked map[string]bool) []string {
	s := Normalize(sample)
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		n := len([]rune(t))
		if n < 2 || n > 12 {
			continue
		}
		if !validTerm(t) {
			continue
		}
		if !strings.Contains(s, t) {
			continue
		}
		if blocked[strings.ToLower(t)] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= 20 {
			break
		}
	}
	return out
}

// validTerm 只收「含汉字、无数字」的短词：纯拉丁/编号/产品名不进（隐私与误伤两头）。
func validTerm(t string) bool {
	han := false
	for _, r := range t {
		if unicode.IsDigit(r) {
			return false
		}
		if unicode.Is(unicode.Han, r) {
			han = true
		}
	}
	return han
}

// lookupAll 并发查 wiki（4 路），每个词一次请求取回全部语种；只保留本单要的维基码；失败的词直接丢（W2）。
// 结果按输入词序排（并发 append 顺序不定，prompt 必须稳定才能复用记忆键）。
func (d *Detector) lookupAll(ctx context.Context, terms []string, codeOf map[string]string) []TermTargets {
	if d.Wiki == nil || len(terms) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, c := range codeOf {
		want[c] = true
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	var out []TermTargets
	for _, term := range terms {
		term := term
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := d.Wiki.LookupAll(ctx, term)
			if err != nil || len(m) == 0 {
				return
			}
			keep := map[string]string{}
			for c := range want {
				if v := strings.TrimSpace(m[c]); v != "" {
					keep[c] = v
				}
			}
			if len(keep) == 0 {
				return
			}
			mu.Lock()
			out = append(out, TermTargets{Src: term, Targets: keep})
			mu.Unlock()
		}()
	}
	wg.Wait()
	idx := map[string]int{}
	for i, t := range terms {
		idx[t] = i
	}
	sort.Slice(out, func(i, j int) bool { return idx[out[i].Src] < idx[out[j].Src] })
	return out
}
