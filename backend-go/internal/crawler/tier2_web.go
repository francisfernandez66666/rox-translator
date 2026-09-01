// ============ tier2_web.go · 职责说明 ============
// crawler 包 tier-2 受限网页抓取适配器（行业垂直站术语表页）。
// 抓取 base_url 指向的术语表 HTML 页，解析表格行：取「源语言列 + 目标语言列」，
// 产出 L1 术语候选写入待审池。
// 合规：经 fetchBase 统一尊重 robots.txt、限频、UA 标识；仅取表格类结构化内容。
// 游标 = 已处理行序号（断点续传基于行序号）。
// =============================================
package crawler

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"translator/internal/store"
)

// htmlTableProducer tier-2 受限网页表格抓取器。
type htmlTableProducer struct {
	st  *store.Store
	src *store.KBScrapeSource
}

// Next 抓取术语表页并逐行产出术语对。
// 行解析：表格每行取前两列文本（去除空白），任一列为空跳过；去重由 store hash 兜底。
func (p *htmlTableProducer) Next(ctx context.Context, deps *SourceDeps, cursor string, offset int) (
	[]*store.KBStagedEntry, []*store.KBStagedPhrase, string, bool, error) {
	if p.src.BaseURL == "" {
		return nil, nil, cursor, true, fmt.Errorf("limited_web 源必须配置 base_url")
	}
	f := newFetchBase()
	body, err := f.get(ctx, p.src.BaseURL)
	if err != nil {
		return nil, nil, cursor, true, err
	}
	rows := parseTermTableRows(string(body))
	start := 0
	if cursor != "" {
		if n, e := atoiSafe(cursor); e == nil {
			start = n
		}
	}
	if start >= len(rows) {
		return nil, nil, fmt.Sprintf("%d", len(rows)), true, nil
	}
	// 目标语言：src.Lang 为采集语言（如 en）；源语言默认 zh
	tgtLang := p.src.Lang
	if tgtLang == "" {
		tgtLang = "en"
	}
	entries := make([]*store.KBStagedEntry, 0, 20)
	processed := start
	const pageSize = 50
	limit := start + pageSize
	if limit > len(rows) {
		limit = len(rows)
	}
	for i := start; i < limit; i++ {
		processed = i + 1
		srcTxt, tgtTxt := rows[i][0], rows[i][1]
		srcTxt = strings.TrimSpace(srcTxt)
		tgtTxt = strings.TrimSpace(tgtTxt)
		if srcTxt == "" || tgtTxt == "" || srcTxt == tgtTxt {
			continue
		}
		entries = append(entries, &store.KBStagedEntry{
			TargetPackID: deps.TargetPackID,
			PackType:     p.src.PackType,
			Tier:         2,
			Layer:        1, // L1 术语
			SrcLang:      "zh",
			SrcText:      srcTxt,
			TgtLang:      tgtLang,
			TgtText:      tgtTxt,
			SourceURL:    p.src.BaseURL,
		})
	}
	done := limit >= len(rows)
	return entries, nil, fmt.Sprintf("%d", processed), done, nil
}

// parseTermTableRows 解析 HTML 中的术语表行：收集所有 <table> 内每行的前两列文本。
// 返回二维切片：每行 [列0, 列1]（缺失列用空串占位）。
func parseTermTableRows(htmlSrc string) [][]string {
	doc, err := html.Parse(strings.NewReader(htmlSrc))
	if err != nil {
		return nil
	}
	var out [][]string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Table {
			out = append(out, extractTableRows(n)...)
			// 不递归进入已处理的 table（避免嵌套重复）
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// extractTableRows 提取单个 <table> 的 [源, 目标] 行集合。
func extractTableRows(tbl *html.Node) [][]string {
	var rows [][]string
	var walkRow func(n *html.Node, cur *[]string)
	// 收集一个 <tr> 内的前两列文本
	collectRow := func(tr *html.Node) []string {
		var cells []string
		var walkCells func(n *html.Node)
		walkCells = func(n *html.Node) {
			if len(cells) >= 2 {
				return
			}
			if n.Type == html.ElementNode && (n.DataAtom == atom.Td || n.DataAtom == atom.Th) {
				cells = append(cells, nodeText(n))
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkCells(c)
			}
		}
		walkCells(tr)
		if len(cells) < 2 {
			return nil
		}
		return []string{cells[0], cells[1]}
	}
	walkRow = func(n *html.Node, cur *[]string) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Tr {
			if r := collectRow(n); r != nil {
				rows = append(rows, r)
			}
			return // 不递归进入已处理的 tr
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkRow(c, cur)
		}
	}
	walkRow(tbl, nil)
	return rows
}

// nodeText 提取节点内文本（去空白，压缩连续空白）。
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}
