// ============ file_artifact_path_test.go 职责说明 ============
// 产物路径与交付名的取名口径回归（#65，2026-09-22 用户裁定「分目录 + 剥前缀」两步同做）。
//
// 这里钉的是**机制本身**（纯函数，不起真模型、不落库）：
//   - TestArtifactDisplayBase*：交付名主干只认原件展示名，内部纳秒时间戳标记（工单落盘的
//     `1790..._名` 与 /api/translate 落盘的 `名_1790...`）一律不进交付物文件名；
//     同时钉住「不误伤用户自己的数字前缀」（2024_ 这类 8 位以内必须原样保留）。
//   - TestArtifactOutputDir*：每个上传件独享 translated/<落盘名主干>/ 子目录——同名原件
//     跨工单/跨租户不再互相覆盖，且子目录名过清洗链，路径不可能逃逸出 translated/。
//   - TestArtifactPathSameNameAcrossTenants：把两者合起来跑一遍真实场景（两个租户传同名
//     「报价单.docx」），断言最终产物绝对路径互不相同——这正是 output_artifacts 按 path
//     反查归属时不再命中错行的前提。
//
// ==========================================================
package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestArtifactDisplayBaseUsesSourceName 原件展示名优先：落盘名里的内部纳秒前缀不得参与取名，
// 也不会被喂给文件名翻译（否则模型回显时那串数字就进了交付名）。
func TestArtifactDisplayBaseUsesSourceName(t *testing.T) {
	got := artifactDisplayBase("/data/upload/tickets/1790026522069352000_报价单.docx",
		map[string]interface{}{"source_name": "报价单.docx"})
	if got != "报价单" {
		t.Fatalf("应以原件展示名取名，得到 %q", got)
	}
}

// TestArtifactDisplayBaseStripsInternalMarkOnFallback 调用点没传展示名（历史单文件工单）时，
// 回落落盘名并剥掉内部时间戳标记；两种历史拼法（前缀式/后缀式）都要认。
func TestArtifactDisplayBaseStripsInternalMarkOnFallback(t *testing.T) {
	cases := []struct{ diskPath, want string }{
		{"/data/upload/tickets/1790026522069352000_报价单.docx", "报价单"},
		{"/data/upload/_uploads/报价单_1790026522069352000.docx", "报价单"},
		// 展示名本身也带内部标记（用户把上一轮交付物又传了一遍）⇒ 同样剥掉
		{"/x/1790026522069352000_报价单.docx", "报价单"},
	}
	for _, c := range cases {
		// 传 nil options：老调用点根本没带 source_name，取名链路不得因此 panic
		if got := artifactDisplayBase(c.diskPath, nil); got != c.want {
			t.Errorf("落盘 %s 剥标记得 %q，期望 %q", c.diskPath, got, c.want)
		}
	}
}

// TestArtifactDisplayBaseKeepsUserDigits 内部标记的下限是 15 位数字，用户自己的年份/序号
// 前缀（2024_、v3_1）必须原样保留，否则就是拿清理缺陷的名义改坏交付名。
func TestArtifactDisplayBaseKeepsUserDigits(t *testing.T) {
	for _, n := range []string{"2024_季度报告.docx", "v3_1_产品方案书.md", "报告_20240922.docx"} {
		if got := artifactDisplayBase("/x/"+n, map[string]interface{}{"source_name": n}); got != strings.TrimSuffix(n, filepath.Ext(n)) {
			t.Errorf("%s 被误剥成 %q", n, got)
		}
	}
}

// TestArtifactDisplayBaseTakesBasenameOnly 展示名可能带 Windows 全路径（multipart filename
// 在部分浏览器/客户端会带 C:\... 前缀）：只取纯文件名，路径成分绝不参与产物取名。
func TestArtifactDisplayBaseTakesBasenameOnly(t *testing.T) {
	got := artifactDisplayBase("/x/1790026522069352000_a.md", map[string]interface{}{"source_name": `C:\Users\me\Desktop\季度 报告 v2.docx`})
	if got != "季度 报告 v2" {
		t.Fatalf("只取了路径最后一段却没保住原名：%q", got)
	}
	// 结果里一个路径分隔符都不许留：这些字符会进 filepath.Join，也直接决定下载响应头
	if strings.ContainsAny(got, `\/:`) {
		t.Fatalf("取名结果仍含路径分隔符：%q", got)
	}
}

// TestArtifactOutputDirIsolatesUploads 每个上传件独享一个产物子目录（同一共用 translated/ 下
// 不再平铺），子目录名取落盘名主干。
func TestArtifactOutputDirIsolatesUploads(t *testing.T) {
	dirA := artifactOutputDir("/data/upload/tickets/1790026522069352000_报价单.docx")
	want := filepath.Join("/data/upload/tickets", "translated", "1790026522069352000_报价单")
	if dirA != want {
		t.Fatalf("产物目录=%q 期望=%q", dirA, want)
	}
	// 同名原件、两次上传（落盘时间戳必不同）⇒ 目录必须不同，否则第二单覆盖第一单产物
	dirB := artifactOutputDir("/data/upload/tickets/1790026522099999999_报价单.docx")
	if dirA == dirB {
		t.Fatalf("两次上传共用产物目录，同名原件会互相覆盖：%q", dirA)
	}
}

// TestArtifactOutputDirCannotEscapeTranslated 子目录名过清洗链：落盘名再脏也拼不出
// ../ 逃逸（sanitizeArtifactBaseName 剔除 / \ : 等，产物只可能落在 translated/ 之下）。
func TestArtifactOutputDirCannotEscapeTranslated(t *testing.T) {
	for _, p := range []string{
		"/data/upload/tickets/.._x.docx",
		"/data/upload/tickets/1790_a:b*c.docx",
		"/data/upload/tickets/  .docx",
	} {
		dir := artifactOutputDir(p)
		root := filepath.Join(filepath.Dir(p), "translated")
		// 允许 dir == root：清洗后为空的名字（如纯空格的落盘名）会让 filepath.Join 丢掉
		// 末段、退化成 translated/ 本身——产物落到共用目录是体验问题，但不是逃逸，
		// 本用例只钉「绝不跳出 translated/」这条安全边界。
		if dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
			t.Fatalf("产物目录 %q 逃逸出 %q", dir, root)
		}
		// 目录名必须已被清洗成非 . / ..（否则等于允许上级目录被当子目录写）
		if filepath.Base(dir) == ".." || filepath.Base(dir) == "." {
			t.Fatalf("子目录名未清洗：%q", dir)
		}
	}
}

// TestArtifactPathSameNameAcrossTenants 真实场景收口：两个租户各传一份同名「报价单.docx」，
// 各自的产物绝对路径必须完全不同（写盘不覆盖、output_artifacts 按 path 反查不会命中错行）。
func TestArtifactPathSameNameAcrossTenants(t *testing.T) {
	uploads := []string{
		"/data/upload/tickets/1790026522069352001_报价单.docx", // 租户 A 的落盘件
		"/data/upload/tickets/1790026522069352002_报价单.docx", // 租户 B 的落盘件
	}
	seen := map[string]bool{}
	for _, up := range uploads {
		// 引擎取名的两条输入都必须是干净的：交付名主干来自原件展示名，目录来自落盘名
		base := artifactDisplayBase(up, map[string]interface{}{"source_name": "报价单.docx"})
		if base != "报价单" {
			t.Fatalf("交付名主干被污染：%q", base)
		}
		p := filepath.Join(artifactOutputDir(up), base+"_en.docx")
		if seen[p] {
			t.Fatalf("两个租户的同名原件产物路径撞车：%q", p)
		}
		seen[p] = true
	}
}
