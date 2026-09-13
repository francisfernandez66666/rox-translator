// ============================================================================
// ★ H12 buildTMX 纯函数测试：转义/语言标签/排序稳定/空行与无目标语过滤。
// ============================================================================
package api

import (
	"strings"
	"testing"

	"translator/internal/kb"
)

func TestH12BuildTMX(t *testing.T) {
	rows := []*kb.Row{
		{Zh: "A & B <x>", Langs: map[string]string{"en": "A & B <x> EN", "de": "DE"}},
		{Zh: "纯中文", Langs: map[string]string{"en": ""}},
		{Zh: "", Langs: map[string]string{"en": "orphan"}},
		{Zh: "模块B", Module: "imported", Langs: map[string]string{"zh_hant": "漢"}},
	}
	doc := string(buildTMX(rows, ""))
	if !strings.Contains(doc, `<tmx version="1.4">`) {
		t.Fatal("缺 TMX 头")
	}
	if !strings.Contains(doc, "A &amp; B &lt;x&gt; EN") {
		t.Fatal("XML 转义缺失")
	}
	if strings.Contains(doc, "纯中文") {
		t.Fatal("无目标语的行不应导出")
	}
	if strings.Contains(doc, "orphan") {
		t.Fatal("空源文行不应导出")
	}
	if !strings.Contains(doc, `xml:lang="en-US"`) || !strings.Contains(doc, `xml:lang="de"`) || !strings.Contains(doc, `xml:lang="zh-TW"`) {
		t.Fatalf("语言标签映射错误:\n%s", doc)
	}
	// module 过滤
	doc2 := string(buildTMX(rows, "imported"))
	if !strings.Contains(doc2, "模块B") || strings.Contains(doc2, "DE") {
		t.Fatal("module 过滤失效")
	}
	// 稳定输出（同输入两次一致）
	if buildTMX(rows, "")[10] != buildTMX(rows, "")[10] {
		t.Fatal("输出不稳定")
	}
}
