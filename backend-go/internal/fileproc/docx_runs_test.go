// ============ docx_runs_test.go · 职责说明 ============
// ★ D21 多 run 段落写回格式保留单测（比例分配 / 首尾节点 / 转义安全）。
// =============================================
package fileproc

import (
	"strings"
	"testing"
)

func TestTranslateDocxParagraphMultiRunDistribution(t *testing.T) {
	para := `<w:p><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">你好</w:t></w:r><w:r><w:rPr><w:color w:val="FF0000"/></w:rPr><w:t>世界</w:t></w:r></w:p>`
	tr := map[string]string{"你好世界": "Hello my dear world"}
	out := string(translateDocxXML([]byte(para), tr))
	if !strings.Contains(out, "<w:b/>") || !strings.Contains(out, `w:val="FF0000"`) {
		t.Fatalf("run 级格式标记丢失: %s", out)
	}
	// 两个 w:t 都应有内容（按比例 2:2 → 各一半 rune 边界），且拼接覆盖全文
	if strings.Count(out, "</w:t>") != 2 {
		t.Fatalf("节点数变化: %s", out)
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "world") {
		t.Fatalf("译文未分布到两 run: %s", out)
	}
	var joined strings.Builder
	i := 0
	for {
		st := strings.Index(out[i:], "<w:t")
		if st < 0 {
			break
		}
		cs := strings.Index(out[i+st:], ">") + i + st + 1
		ce := strings.Index(out[cs:], "</w:t>") + cs
		joined.WriteString(out[cs:ce])
		i = ce + 6
	}
	if strings.ReplaceAll(joined.String(), " ", "") != "Hellomydearworld" {
		t.Fatalf("分配拼接后文本不等于译文: %q", joined.String())
	}
}

func TestTranslateDocxParagraphAmpersandSafe(t *testing.T) {
	para := `<w:p><w:r><w:t>AB</w:t></w:r><w:r><w:t>CD</w:t></w:r></w:p>`
	tr := map[string]string{"ABCD": "R&D 50% <x>"}
	out := string(translateDocxXML([]byte(para), tr))
	if strings.Contains(out, "R&D") || strings.Contains(out, "<x>") {
		t.Fatalf("转义未生效: %s", out)
	}
	if !strings.Contains(out, "R&amp;D") || !strings.Contains(out, "&lt;x&gt;") {
		t.Fatalf("转义实体缺失: %s", out)
	}
}

func TestDistributeByWeight(t *testing.T) {
	ps := distributeByWeight("0123456789", []int{3, 7})
	if ps[0] != "012" || ps[1] != "3456789" {
		t.Fatalf("比例切分错误: %q", ps)
	}
	if got := distributeByWeight("abc", []int{0, 0}); got[0] != "abc" || got[1] != "" {
		t.Fatalf("零权重应全归首段: %q", got)
	}
}
