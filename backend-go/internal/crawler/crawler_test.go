// ============ crawler_test.go · 职责说明 ============
// crawler 包单元测试：表格解析、robots 规则、去重 hash、断点续传、加载探针。
// =============================================
package crawler

import (
	"strings"
	"testing"
)

// TestParseTermTableRows 验证 HTML 术语表行解析（源/目标两列）。
func TestParseTermTableRows(t *testing.T) {
	htmlSrc := `<html><body><table>
		<tr><th>中文</th><th>English</th></tr>
		<tr><td>汽车</td><td>Automobile</td></tr>
		<tr><td>刹车</td><td>Brake</td></tr>
		<tr><td>只有一列</td></tr>
	</table></body></html>`
	rows := parseTermTableRows(htmlSrc)
	if len(rows) != 3 { // 表头行也会被收集（两列），此处按实现返回 3 行（含表头）
		t.Fatalf("期望 3 行，实际 %d", len(rows))
	}
	if rows[1][0] != "汽车" || rows[1][1] != "Automobile" {
		t.Fatalf("行解析错误: %#v", rows[1])
	}
	// 只有一列的行应被 collectRow 丢弃（<2 列）
	for _, r := range rows {
		if len(r) < 2 {
			t.Fatalf("存在不足两列的行: %#v", r)
		}
	}
}

// TestRobotsDisallows 验证 robots.txt 规则解析。
func TestRobotsDisallows(t *testing.T) {
	body := "User-agent: *\nDisallow: /private/\nDisallow: /admin\n"
	if !robotsDisallows(body, "/private/terms") {
		t.Fatal("应禁止 /private/ 前缀")
	}
	if robotsDisallows(body, "/terms") {
		t.Fatal("不应禁止 /terms")
	}
	if !robotsDisallows(body, "/admin") {
		t.Fatal("应禁止 /admin 前缀")
	}
}

// TestExtractJSON 验证 LLM 输出 JSON 提取（容忍 Markdown 代码块）。
func TestExtractJSON(t *testing.T) {
	in := "```json\n{\"entries\":[{\"src\":\"汽车\",\"tgt\":\"Automobile\"}]}\n```"
	out := extractJSON(in)
	if !strings.HasPrefix(out, "{") || !strings.HasSuffix(out, "}") {
		t.Fatalf("JSON 提取失败: %q", out)
	}
	if !strings.Contains(out, "Automobile") {
		t.Fatalf("JSON 内容缺失: %q", out)
	}
}

// TestProducerIdle 验证负载探针逻辑（探针 nil=空闲）。
func TestProducerIdle(t *testing.T) {
	c := New(nil)
	if !c.Idle() {
		t.Fatal("探针为 nil 时应视为空闲")
	}
	// 契约：Probe 返回 true=低占用（可采集，Idle=true）；false=高占用（应暂停，Idle=false）
	low := false
	c.Probe = func() bool { return low }
	if c.Idle() {
		t.Fatal("Probe=false=高占用，Idle 应为 false")
	}
	low = true
	if !c.Idle() {
		t.Fatal("Probe=true=低占用，Idle 应为 true")
	}
}
