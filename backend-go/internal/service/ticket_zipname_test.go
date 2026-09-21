// ============ ticket_zipname_test.go · 职责说明 ============
// 工单多语言产物**压缩包命名**回归（2026-09-22，接 RC-4 的漏改点）：
// 旧实现直接用中文原件名拼 `_translated.zip`，导致英文交付物顶着中文包名。
// 现在包名取自译件名（RC-4 起已按目标语翻译），并剥掉单一语言标记
// —— 断言「不残留中文」「不挂某一种语言的尾巴」「异常输入不产生空文件名」。
// =============================================
package service

import (
	"strings"
	"testing"
)

func TestZipDeliveryNameUsesTranslatedArtifactBase(t *testing.T) {
	files := []string{"/data/uploads/1/translated/Product Proposal_v5.1_en.md", "/data/uploads/1/translated/Product Proposal_v5.1_fr.md"}
	got := zipDeliveryName(files, []string{"en", "fr"}, "/data/uploads/1/产品方案书_v5.1-2.md")
	if want := "Product Proposal_v5.1_translated.zip"; got != want {
		t.Fatalf("包名 = %q, 期望 %q", got, want)
	}
	if strings.Contains(got, "产品方案书") {
		t.Fatalf("包名仍带中文原件名:\n%s", got)
	}
	// 纯文案旁路产物 `_{lang}_text.md` 也要剥干净，否则包名变成 "..._en_text_translated.zip"
	textOnly := []string{"/x/translated/方案书_zh_hant_text.md"}
	if g := zipDeliveryName(textOnly, []string{"zh_hant"}, "/x/方案书.md"); g != "方案书_translated.zip" {
		t.Fatalf("_text 后缀未剥离: %q", g)
	}
}

// 兜底路径：无产物 / base 异常时绝不产出 "_translated.zip" 这类空文档名。
func TestZipDeliveryNameFallbacks(t *testing.T) {
	if g := zipDeliveryName(nil, []string{"en"}, "/x/report.md"); g != "report_translated.zip" {
		t.Fatalf("无产物应回落原件名，实得 %q", g)
	}
	if g := zipDeliveryName([]string{"/x/y/_en.md"}, []string{"en"}, "/x/y/中文名.md"); g != "_en_translated.zip" {
		t.Fatalf("剥空后应回落原 base 而不是产出空文档名，实得 %q", g)
	}
	if g := zipDeliveryName([]string{"/x/y/Report_en.docx"}, nil, "/x/y/报告.docx"); g != "Report_en_translated.zip" {
		t.Fatalf("langs 为空时保留译件名，实得 %q", g)
	}
}
