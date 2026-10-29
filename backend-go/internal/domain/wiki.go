// ============ wiki.go · 职责说明 ============
// 维基 langlinks 查询（2026-10-29 第十二轮）：**每个词一次请求取回全部语种标题**。
//
// 为什么不按语种各查一次：v1 要支持多目标语，12 语种 × 12 词 = 144 次请求；
// 不传 `lllang` 时一次就能拿全（`lllimit=max`），12 词 = 12 次请求，与语种数无关。
//
// 只读公开数据、只发词条（不发文档）；失败返回 error，调用方按「查不到就丢」处理。
// ============================================
package domain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// langlinksResp MediaWiki `prop=langlinks` 响应（只解析需要的字段）。
type langlinksResp struct {
	Query struct {
		Pages map[string]struct {
			Langlinks []struct {
				Lang string `json:"lang"`
				Star string `json:"*"`
			} `json:"langlinks"`
		} `json:"pages"`
	} `json:"query"`
}

// wikiEntry 一条词条缓存（全语种标题 + 写入时刻）。
type wikiEntry struct {
	targets map[string]string
	at      time.Time
}

// Wiki 维基查询客户端（进程内缓存 term → map[lang]title）。
type Wiki struct {
	// Base 接口地址（测试用 httptest 覆盖；生产恒为默认值）。
	Base  string
	HTTP  *http.Client
	ttl   time.Duration
	mu    sync.Mutex
	cache map[string]wikiEntry
}

// NewWiki 默认 800ms 单词超时、30 天缓存。
func NewWiki() *Wiki {
	return &Wiki{
		Base:  "https://zh.wikipedia.org/w/api.php",
		HTTP:  &http.Client{Timeout: 800 * time.Millisecond},
		ttl:   30 * 24 * time.Hour,
		cache: map[string]wikiEntry{},
	}
}

// LookupAll 取该词条的全部目标语标题（lang→title）；查不到返回空 map（不是错误）。
func (w *Wiki) LookupAll(ctx context.Context, term string) (map[string]string, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, nil
	}
	w.mu.Lock()
	if e, ok := w.cache[term]; ok && time.Since(e.at) < w.ttl {
		m := e.targets
		w.mu.Unlock()
		return m, nil
	}
	w.mu.Unlock()

	u := w.Base + "?" + url.Values{
		"action":    {"query"},
		"prop":      {"langlinks"},
		"titles":    {term},
		"lllimit":   {"max"},
		"format":    {"json"},
		"redirects": {"1"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body langlinksResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, pg := range body.Query.Pages {
		for _, ll := range pg.Langlinks {
			if t := cleanTitle(ll.Star); t != "" && ll.Lang != "" {
				out[ll.Lang] = t
			}
		}
	}
	w.mu.Lock()
	if len(w.cache) >= 2048 {
		w.cache = map[string]wikiEntry{}
	}
	w.cache[term] = wikiEntry{targets: out, at: time.Now()}
	w.mu.Unlock()
	return out, nil
}

// cleanTitle 清洗词条标题：
//   - 空/含 disambiguation ⇒ 丢（消歧义页不是译法）；
//   - `Factor (mathematics)` ⇒ 取基名 `Factor`（保留括号会把上下文钉死）。
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(strings.ToLower(s), "disambiguation") {
		return ""
	}
	if i := strings.Index(s, " ("); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}
