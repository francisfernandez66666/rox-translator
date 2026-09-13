// ============ 本文件职责说明 ============
// tier2 术语表行解析测试（表头过滤等）。
// =============================================
package crawler

import "testing"

// TestD6HeaderRowSkipped 表头行（th）不得作为术语对采集。
func TestD6HeaderRowSkipped(t *testing.T) {
	rows := parseTermTableRows(`<table><tr><th>术语</th><th>Term</th></tr><tr><td>服务器</td><td>server</td></tr></table>`)
	for _, r := range rows {
		if len(r) >= 2 && r[0] == "术语" {
			t.Fatalf("表头行混入采集: %v", rows)
		}
	}
	if len(rows) != 1 || rows[0][0] != "服务器" {
		t.Fatalf("数据行应保留: %v", rows)
	}
}
