// ============ domain_test.go · 职责说明 ============
// 文档级判域的判据锁（2026-10-29 第十二轮）：
//
//	语种码映射 / 回执解析 / 词护栏（W1+W3）/ 门槛（样本长度、置信度、每语种命中数）/ wiki 取证与缓存。
//
// ============================================
package domain

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWikiLangCode(t *testing.T) {
	// 用有序切片（不是 map）：map 迭代顺序随机，M0051 的失败信息会时而落在 zh_hant、
	// 时而落在 zh-hant 上，变异闸按固定字面判"红在锁自己的消息上"会假红。
	cases := []struct{ in, want string }{
		{"en", "en"}, {"ja", "ja"}, {"id_lang", "id"}, {"ID_LANG", "id"},
		{"zh_hant", ""}, {"zh-hant", ""}, {"  fr  ", "fr"},
	}
	for _, c := range cases {
		if got := wikiLangCode(c.in); got != c.want {
			t.Fatalf("wikiLangCode(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestParseReply(t *testing.T) {
	if rep, err := parseReply("```json\n{\"domain\":\"quant\",\"label\":\"量化\",\"confidence\":0.9,\"terms\":[\"动量\"]}\n```"); err != nil || rep.Domain != "quant" {
		t.Fatalf("合法回执解析失败：%+v err=%v", rep, err)
	}
	if _, err := parseReply(`{"domain":""}`); !errors.Is(err, ErrSchema) {
		t.Fatalf("空 domain 应判 schema，实得 %v", err)
	}
	if _, err := parseReply("not json"); !errors.Is(err, ErrSchema) {
		t.Fatalf("非 JSON 应判 schema，实得 %v", err)
	}
	rep, err := parseReply(`{"domain":"x","confidence":2}`)
	if err != nil || rep.Confidence != 1 {
		t.Fatalf("置信度应夹到 1，实得 %v/%v", rep.Confidence, err)
	}
}

func TestCleanTermsGuards(t *testing.T) {
	sample := "量化交易 与 因子模型"
	got := cleanTerms([]string{"量化交易", "量化交易", "不存在词", "ABC", "a1", "太"}, sample, nil)
	if len(got) != 1 || got[0] != "量化交易" {
		t.Fatalf("词护栏失效（样本里没有/纯拉丁/数字/单字/重复都要丢）：%v", got)
	}
	// W3/W4：品牌术语/已有术语层（Blocked）必须在查 wiki 之前就丢掉。
	got2 := cleanTerms([]string{"动量", "因子"}, "动量与因子", map[string]bool{"动量": true})
	if len(got2) != 1 || got2[0] != "因子" {
		t.Fatalf("Blocked 词没被过滤（会作为查询词发给维基）：%v", got2)
	}
}

func TestProfileTooShort(t *testing.T) {
	d := &Detector{Call: func(ctx context.Context, s, u string) (string, error) {
		t.Fatalf("样本太短不该拨模型")
		return "", nil
	}}
	lim := Limits{MinSampleRunes: 200, MinConfidence: 0.5, MinTerms: 2, MinResolved: 1}
	if _, err := d.Profile(context.Background(), "太短", lim, []string{"en"}); !errors.Is(err, ErrTooShort) {
		t.Fatalf("短样本应判 too_short，实得 %v", err)
	}
}

func TestProfileLowConf(t *testing.T) {
	d := &Detector{Call: func(ctx context.Context, s, u string) (string, error) {
		return `{"domain":"x","label":"x","confidence":0.2,"terms":["量化交易"]}`, nil
	}}
	sample := strings.Repeat("量化交易与因子模型。", 30)
	lim := Limits{MinSampleRunes: 20, MinConfidence: 0.7, MinTerms: 1, MinResolved: 1, MaxTerms: 5}
	if _, err := d.Profile(context.Background(), sample, lim, []string{"en"}); !errors.Is(err, ErrLowConf) {
		t.Fatalf("低置信应判 low_conf，实得 %v", err)
	}
}

func TestProfileMinTerms(t *testing.T) {
	d := &Detector{Call: func(ctx context.Context, s, u string) (string, error) {
		return `{"domain":"x","label":"x","confidence":0.9,"terms":["量化交易","因子模型"]}`, nil
	}}
	sample := strings.Repeat("量化交易与因子模型。", 30)
	lim := Limits{MinSampleRunes: 20, MinConfidence: 0.7, MinTerms: 3, MinResolved: 1, MaxTerms: 5}
	if _, err := d.Profile(context.Background(), sample, lim, []string{"en"}); !errors.Is(err, ErrNoTerms) {
		t.Fatalf("词数不足应判 no_terms，实得 %v", err)
	}
}

func TestProfileNoWiki(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"query":{"pages":{"-1":{"missing":""}}}}`)
	}))
	defer srv.Close()
	wk := &Wiki{Base: srv.URL, HTTP: srv.Client(), ttl: time.Hour, cache: map[string]wikiEntry{}}
	d := &Detector{
		Call: func(ctx context.Context, s, u string) (string, error) {
			return `{"domain":"x","label":"x","confidence":0.9,"terms":["量化交易","因子模型"]}`, nil
		},
		Wiki: wk,
	}
	sample := strings.Repeat("量化交易与因子模型。", 30)
	lim := Limits{MinSampleRunes: 20, MinConfidence: 0.7, MinTerms: 2, MinResolved: 1, MaxTerms: 5}
	if _, err := d.Profile(context.Background(), sample, lim, []string{"en"}); !errors.Is(err, ErrNoWiki) {
		t.Fatalf("维基查不到应判 no_wiki，实得 %v", err)
	}
}

func TestProfilePerLanguage(t *testing.T) {
	// wiki：en 全给；ja 只给「量化交易」一条 ⇒ MinResolved=2 时 ja 整份丢掉。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		term := r.URL.Query().Get("titles")
		ll := `{"lang":"en","*":"EN"}`
		if term == "量化交易" {
			ll += `,{"lang":"ja","*":"JA"}`
		}
		fmt.Fprintf(w, `{"query":{"pages":{"1":{"langlinks":[%s]}}}}`, ll)
	}))
	defer srv.Close()
	wk := &Wiki{Base: srv.URL, HTTP: srv.Client(), ttl: time.Hour, cache: map[string]wikiEntry{}}
	d := &Detector{
		Call: func(ctx context.Context, s, u string) (string, error) {
			return `{"domain":"quant","label":"量化交易","confidence":0.9,"terms":["量化交易","因子模型","回测"]}`, nil
		},
		Wiki: wk,
	}
	sample := strings.Repeat("量化交易与因子模型与回测。", 20)
	lim := Limits{MinSampleRunes: 20, MinConfidence: 0.7, MinTerms: 2, MinResolved: 2, MaxTerms: 5}
	p, err := d.Profile(context.Background(), sample, lim, []string{"en", "ja"})
	if err != nil {
		t.Fatalf("判域失败：%v", err)
	}
	if len(p.TermsFor("en", 5)) != 3 {
		t.Fatalf("en 应 3 条，实得 %v", p.TermsFor("en", 5))
	}
	if len(p.TermsFor("ja", 5)) != 0 {
		t.Fatalf("ja 命中不足应整份丢掉，实得 %v", p.TermsFor("ja", 5))
	}
	// 站内码映射：zh_hant 没有 langlink ⇒ 该语种不参与（即使 wiki 返回 zh 也不冒充）。
	if len(p.TermsFor("zh_hant", 5)) != 0 {
		t.Fatalf("zh_hant 不该有词表")
	}
}

func TestWikiLookupAllAndCache(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		// 一次返回全部语种；含括注标题、消歧义页（后者必须丢）。
		fmt.Fprint(w, `{"query":{"pages":{"1":{"langlinks":[
			{"lang":"en","*":"Momentum (physics)"},
			{"lang":"ja","*":"運動量"},
			{"lang":"zh","*":"动量"},
			{"lang":"de","*":"Disambiguation"}
		]}}}}`)
	}))
	defer srv.Close()
	wk := &Wiki{Base: srv.URL, HTTP: srv.Client(), ttl: time.Hour, cache: map[string]wikiEntry{}}
	m, err := wk.LookupAll(context.Background(), "动量")
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if m["en"] != "Momentum" {
		t.Fatalf("括注标题应取基名，实得 %q", m["en"])
	}
	if m["ja"] != "運動量" || m["zh"] != "动量" {
		t.Fatalf("多语种标题没取全：%v", m)
	}
	if _, ok := m["de"]; ok {
		t.Fatalf("消歧义页不许进词表：%v", m)
	}
	if _, err := wk.LookupAll(context.Background(), "动量"); err != nil {
		t.Fatalf("第二次查询失败：%v", err)
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("同词第二次应命中缓存（0 次 HTTP），实读 %d 次", got)
	}
}
