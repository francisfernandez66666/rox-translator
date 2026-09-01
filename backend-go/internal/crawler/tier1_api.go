// ============ tier1_api.go · 职责说明 ============
// crawler 包 tier-1 官方开放 API 适配器（可信度最高）。
// 当前实现：维基百科/维基词典 MediaWiki `action=query&prop=langlinks` 翻译查询——
// 给定种子词（源语言），取指定目标语言的词条标题作为译文候选（官方开放 API，合规风险低）。
// 种子词来源：数据源 base_url 指向的纯文本词表（每行一个词），
//   若未配置词表 URL，则使用内置行业关键词种子（按 src.Industry 匹配）。
// 逐词查询、游标=已处理词数（断点续传基于词序号）。
// =============================================
package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"translator/internal/store"
)

// wiktionaryProducer tier-1 官方 API 抓取器。
type wiktionaryProducer struct {
	st  *store.Store
	src *store.KBScrapeSource
}

// langlinksResp MediaWiki langlinks 响应结构（仅解析所需字段）。
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

// seedTerms 获取待翻译种子词：优先数据源 base_url（纯文本词表，每行一词），
// 未配置时返回内置行业/通用种子词。
func (p *wiktionaryProducer) seedTerms(ctx context.Context, f *fetchBase) ([]string, error) {
	if p.src.BaseURL != "" {
		body, err := f.get(ctx, p.src.BaseURL)
		if err == nil {
			var out []string
			for _, line := range strings.Split(string(body), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					out = append(out, line)
				}
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}
	// 内置种子词（按行业匹配 + 通用兜底）
	if seeds, ok := builtinIndustrySeeds[p.src.Industry]; ok && len(seeds) > 0 {
		return seeds, nil
	}
	return builtinGeneralSeeds, nil
}

// Next 抓取下一批：按游标（已处理词序号）继续，逐词查询目标语言词条。
func (p *wiktionaryProducer) Next(ctx context.Context, deps *SourceDeps, cursor string, offset int) (
	[]*store.KBStagedEntry, []*store.KBStagedPhrase, string, bool, error) {
	f := newFetchBase()
	terms, err := p.seedTerms(ctx, f)
	if err != nil {
		return nil, nil, cursor, true, err
	}
	start := 0
	if cursor != "" {
		if n, e := atoiSafe(cursor); e == nil {
			start = n
		}
	}
	if start >= len(terms) {
		return nil, nil, fmt.Sprintf("%d", len(terms)), true, nil
	}
	srcLang := "zh" // 种子词为中文，源语言固定 zh
	tgtLang := p.src.Lang
	if tgtLang == "" {
		tgtLang = "en"
	}
	entries := make([]*store.KBStagedEntry, 0, 20)
	processed := start
	const pageSize = 20
	limit := start + pageSize
	if limit > len(terms) {
		limit = len(terms)
	}
	for i := start; i < limit; i++ {
		if ctx.Err() != nil {
			return entries, nil, fmt.Sprintf("%d", processed), false, ctx.Err()
		}
		term := strings.TrimSpace(terms[i])
		processed = i + 1
		if term == "" {
			continue
		}
		trans := p.lookupTranslation(ctx, f, term, tgtLang)
		if trans == "" {
			continue
		}
		entries = append(entries, &store.KBStagedEntry{
			TargetPackID: deps.TargetPackID,
			PackType:     p.src.PackType,
			Tier:         1,
			Layer:        1, // L1 术语
			SrcLang:      srcLang,
			SrcText:      term,
			TgtLang:      tgtLang,
			TgtText:      trans,
			SourceURL:    "wiktionary langlinks",
		})
	}
	done := limit >= len(terms)
	return entries, nil, fmt.Sprintf("%d", processed), done, nil
}

// lookupTranslation 查询词条在目标语言维基的词条标题（近似译文候选）。
// 使用 zh.wikipedia.org（中文词条覆盖广）；目标语言 langlinks 标题即该语言的对应词条名。
func (p *wiktionaryProducer) lookupTranslation(ctx context.Context, f *fetchBase, term, tgtLang string) string {
	api := "https://zh.wikipedia.org/w/api.php?" + url.Values{
		"action":    {"query"},
		"prop":      {"langlinks"},
		"titles":    {term},
		"lllang":    {tgtLang},
		"lllimit":   {"1"},
		"format":    {"json"},
		"redirects": {"1"},
	}.Encode()
	body, err := f.getJSON(ctx, api)
	if err != nil {
		return ""
	}
	var resp langlinksResp
	if json.Unmarshal(body, &resp) != nil {
		return ""
	}
	for _, pg := range resp.Query.Pages {
		for _, ll := range pg.Langlinks {
			if ll.Lang == tgtLang && strings.TrimSpace(ll.Star) != "" {
				return strings.TrimSpace(ll.Star)
			}
		}
	}
	return ""
}

// builtinIndustrySeeds 内置行业关键词种子（按 src.Industry 匹配；后续可由超管词表 URL 替代）。
var builtinIndustrySeeds = map[string][]string{
	"auto":      {"汽车", "发动机", "刹车", "续航", "充电", "经销商", "保修", "自动挡", "混合动力", "驾驶辅助"},
	"realestate": {"房产", "装修", "首付", "按揭", "户型", "样板间", "物业", "交房", "精装", "毛坯"},
	"b2b":       {"解决方案", "报价", "合同", "售后", "服务商", "供应链", "签约", "续约", "白皮书", "案例"},
	"education": {"留学", "申请", "签证", "奖学金", "课程", "学位", "预科", "语言成绩", "文书", "面试"},
	"ecommerce": {"独立站", "转化率", "客单价", "复购", "物流", "退货", "优惠券", "广告投放", "落地页", "加购"},
	"wedding":   {"婚庆", "跟妆", "司仪", "摄影", "场地", "婚纱", "婚宴", "礼金", "请柬", "蜜月"},
}

// builtinGeneralSeeds 通用语言文化种子词（无行业匹配时兜底）。
var builtinGeneralSeeds = []string{
	"欢迎", "您好", "谢谢", "对不起", "请问", "价格", "发货", "退款", "服务", "客服",
	"您好吗", "再见", "请", "不客气", "没关系", "加油", "合作", "订单", "支付", "发票",
}
