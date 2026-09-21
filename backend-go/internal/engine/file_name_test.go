// ============ file_name_test.go 职责说明 ============
// 产物文件名翻译（RC-4）的纯函数断言：全部离线，不打真模型。
//   - sanitizeArtifactBaseName：危险字符/控制字符清理、首尾空白与点剥离、
//     为后缀预留字节后按 UTF-8 截断且**不切断多字节字符**；
//   - resolveTranslatedBaseName：调用失败/空返回/同文回显/指令残留一律回落原名，
//     正常返回才采用译文名（扩展名不参与）；
//   - fileNameNeedsTranslation：非 CJK 目标语下已是拉丁名的 base 不再送模型。
//
// ========================================
package engine

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSanitizeArtifactBaseNameStripsDangerous 危险字符与控制字符必须被剔除，
// 尤其是路径分隔符（可让产物逃逸出 translated/ 目录）与换行（污染下载响应头）。
func TestSanitizeArtifactBaseNameStripsDangerous(t *testing.T) {
	got := sanitizeArtifactBaseName(`../../etc/passwd: 产品*方案?"书"<v5.1>|结尾  `, "")
	if strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Fatalf("危险字符未清理： %q", got)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") || strings.HasSuffix(got, ".") {
		t.Fatalf("首尾空白/结尾点未剥离： %q", got)
	}
	if !strings.Contains(got, "产品") || !strings.Contains(got, "方案") {
		t.Fatalf("正常内容被误删： %q", got)
	}
	if strings.Contains(sanitizeArtifactBaseName("第一行\n第二行\t制表", ""), "\n") ||
		strings.Contains(sanitizeArtifactBaseName("a\x00b", ""), "\x00") {
		t.Fatal("换行/ NUL 等控制字符必须剔除")
	}
}

// TestSanitizeArtifactBaseNameTruncatesByBytesWithoutSplittingRune 255 字节上限：
// 按字节截断（文件系统限制的是字节），且不得把 3 字节汉字切一半（否则名字变乱码）。
func TestSanitizeArtifactBaseNameTruncatesByBytesWithoutSplittingRune(t *testing.T) {
	long := strings.Repeat("翻译助手", 300) // 1200 字节
	got := sanitizeArtifactBaseName(long, "")
	if len(got) > artifactBaseMaxBytes {
		t.Fatalf("未截断：%d 字节 > %d", len(got), artifactBaseMaxBytes)
	}
	if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("截断切到了多字节字符中间：%q", got)
	}
	// 截断结果必须是原名前缀（保证不产生「凭空多出内容」的名字）
	if !strings.HasPrefix(long, got) {
		t.Fatal("截断结果不是原名前缀")
	}
	if utf8.RuneCountInString(got)*3 != len(got) {
		t.Fatalf("截断后汉字被切半（字节数非 3 的整倍数）：%d", len(got))
	}
}

// TestSanitizeArtifactBaseNameReservesSuffixBytes 后缀（"_en_text.md"）占的字节必须预留，
// 否则 base 顶满 255 时整个文件名仍会超限被静默截断。
func TestSanitizeArtifactBaseNameReservesSuffixBytes(t *testing.T) {
	suffix := "_en_text.md"
	long := strings.Repeat("文案", 300)
	got := sanitizeArtifactBaseName(long, suffix)
	if len(got)+len(suffix) > artifactBaseMaxBytes {
		t.Fatalf("整体超限：%d + %d > %d", len(got), len(suffix), artifactBaseMaxBytes)
	}
}

// TestResolveTranslatedBaseNameFallbacks 文件名翻译只是体验项：任何异常都必须回落原名，
// 绝不能让工单失败（断言覆盖调用失败/空返回/同文回显/指令残留/清洗后为空）。
func TestResolveTranslatedBaseNameFallbacks(t *testing.T) {
	const orig = "产品方案书_v5.1"
	cases := []struct {
		name     string
		out      string
		callErr  error
		wantSame bool
		want     string
	}{
		{"调用失败回落原名", "Product Proposal_v5.1", errors.New("llm down"), true, orig},
		{"空返回回落原名", "", nil, true, orig},
		{"同文回显回落原名", orig, nil, true, orig},
		{"指令残留回落原名", "<Only output the final translated text, enclosed entirely within  and nothing else>", nil, true, orig},
		{"正常译文名采用", "Product Proposal_v5.1", nil, false, "Product Proposal_v5.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveTranslatedBaseName(orig, c.out, c.callErr, "en")
			if c.wantSame && got != orig {
				t.Fatalf("应回落原名，got %q", got)
			}
			if !c.wantSame && got != c.want {
				t.Fatalf("应采用译文名，got %q want %q", got, c.want)
			}
		})
	}
}

// TestResolveTranslatedBaseNameKeepsExtensionOut 扩展名不参与：模型只可能看到 base，
// 返回名里出现危险字符也被清洗，但版本号里的点必须保留。
func TestResolveTranslatedBaseNameKeepsExtensionOut(t *testing.T) {
	got := resolveTranslatedBaseName("产品方案书_v5.1", "Product/Proposal: v5.1", nil, "en")
	if strings.ContainsAny(got, `/\:`) {
		t.Fatalf("清洗不彻底：%q", got)
	}
	if !strings.Contains(got, "v5.1") {
		t.Fatalf("版本号的点被误删：%q", got)
	}
}

// TestResolveTranslatedBaseNameBlocksPromptResidue ★ 2026-09-22 全量 UAT（E2E M1）实测缺陷：
// mock LLM 对「文件名主干」这条调用把整条 prompt 逐行复述回来（指令行 + 原名），
// 旧口径下 sanitizeArtifactBaseName 会把换行当控制字符删掉再拼串，于是交付物文件名变成
// `TranslatedEN(把下面的英语翻译为日语，必须使用规范的日语汉字+假名混合书写)TranslatedEN(产品方案书_v5.1)_ja.md`
// ——内部提示词泄漏进客户交付物（与 P0「表格分隔行泄漏」同类，只是载体换成名字）。
// 现在两条结构判据都要拦住它并回落原名；同时反向钉住「正常译名不得被误杀」。
func TestResolveTranslatedBaseNameBlocksPromptResidue(t *testing.T) {
	cases := []struct {
		name string
		orig string
		out  string
	}{
		{"多行=复述整条prompt", "1790026522069352000_e2e_m1",
			"TranslatedEN(把下面的英语翻译为日语，必须使用规范的日语汉字+假名混合书写)\nTranslatedEN(1790026522069352000_e2e_m1)"},
		{"单行但原样含整个原名", "产品方案书_v5.1", "TranslatedEN(产品方案书_v5.1)"},
		{"原样含原名且有尾随说明", "产品方案书_v5.1", "产品方案书_v5.1 （如需改请联系管理员）"},
		{"回车符同样拦", "报告Q3", "a\r\nb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveTranslatedBaseName(c.orig, c.out, nil, "ja"); got != c.orig {
				t.Fatalf("回显/提示词残留必须回落原名，实得 %q", got)
			}
		})
	}
	// 反向：正常译名不能被这两条判据误杀（短原名 "AI" 被包含在合理译名里属正常保留）
	if got := resolveTranslatedBaseName("产品方案书_v5.1", "製品計画書_v5.1", nil, "ja"); got != "製品計画書_v5.1" {
		t.Fatalf("正常日文译名被误杀，实得 %q", got)
	}
	if got := resolveTranslatedBaseName("AI", "AI技術", nil, "ja"); got != "AI技術" {
		t.Fatalf("短原名（<3 字符）不参与包含判据，实得 %q", got)
	}
}

// TestFileNameNeedsTranslation 非 CJK 目标语下纯拉丁名不再白调一次模型；
// 含中文的名字、以及 CJK 目标语（简翻繁/日文）仍需送模型。
func TestFileNameNeedsTranslation(t *testing.T) {
	if fileNameNeedsTranslation("Q3-report_v2", "en") {
		t.Fatal("已是英文的名无需再翻（只会拿到同文回显）")
	}
	if !fileNameNeedsTranslation("产品方案书_v5.1", "en") {
		t.Fatal("含中文必须送翻")
	}
	if !fileNameNeedsTranslation("Q3-report_v2", "ja") {
		t.Fatal("CJK 目标语无法按字符判定，一律送翻")
	}
}
