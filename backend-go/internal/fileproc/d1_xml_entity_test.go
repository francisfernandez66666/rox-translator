// ============ 本文件职责说明 ============
// D1 回归（2026-09-12）：docx 段落写回对含 XML 预定义实体字符（& < > " '）
// 的译文必须产出合法 XML 且文本无损——旧游标按转义前长度截断产生残缺实体。
// =============================================
package fileproc

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestD1EscapeCursorParity(t *testing.T) {
	para := `<w:p><w:r><w:t>R&amp;D 方案</w:t></w:r><w:r><w:t> 附录</w:t></w:r></w:p>`
	out := translateDocxParagraph(para, map[string]string{"R&D 方案 附录": `Q&A <tag> "引" '单'`})
	if out == para {
		t.Fatal("译文未命中（提取键异常）")
	}
	// 1) XML 合法性：任何残缺实体都会在此报错
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		_, e := dec.Token()
		if e != nil {
			if e.Error() == "EOF" {
				break
			}
			t.Fatalf("写回结果非法 XML（残缺实体）: %v\n%s", e, out)
		}
	}
	// 2) 文本 round-trip：再提取应与注入译文一致（次 run 已清空）
	got, _ := paragraphRunText(out)
	if got != `Q&A <tag> "引" '单'` {
		t.Fatalf("写回后提取文本失真: %q", got)
	}
}
