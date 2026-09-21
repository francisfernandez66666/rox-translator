// ============ md_anchor_test.go · 职责说明 ============
// RC-6 回归（2026-09-22）：Markdown 目录锚点必须随标题同步换语。
// 链接目标在 span 写回里是「骨架洞」（防模型改 URL），因此译文会出现
// 「链接文字已翻、`#中文锚点` 未翻」的残缺目录 —— 本文件钉住重算逻辑：
// 中文锚点换成译文锚点、真实 URL 与外部链接一字不动、围栏内假标题不参与。
// =============================================
package fileproc

import (
	"os"
	"strings"
	"testing"
)

// TestMdAnchorRemapViaWriteback 端到端（走 ApplyAlignedText，而不是只测纯函数）：
// 目录行链接文字 + 标题一起翻，锚点必须跟着变，否则译文目录点不动。
func TestMdAnchorRemapViaWriteback(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/toc.md"
	md := "# 产品方案书\n\n" +
		"- [一、项目背景与目标](#一项目背景与目标)\n" +
		"- [二、方案](#二方案)\n" +
		"- [重复节](#重复节)\n" +
		"- [重复节](#重复节-1)\n" +
		"- [官网](https://example.com/老目录#中文锚)\n\n" +
		"## 一、项目背景与目标\n\n正文\n\n## 二、方案\n\n内容\n\n## 重复节\n\nA\n\n## 重复节\n\nB\n"
	if err := os.WriteFile(src, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	// ★ 键形制以 md_structure 的 span 提取为准：链接文字的键**含方括号**（`[文字]`），
	//   标题/单元格的键不含。用无括号键会导致链接文字翻不出来，锚点重算就失去了前提。
	tr := map[string]string{
		"产品方案书":       "Product Proposal",
		"一、项目背景与目标":   "1. Background & Goals",
		"二、方案":        "2. Solution",
		"重复节":         "Repeat",
		"[一、项目背景与目标]": "[1. Background & Goals]",
		"[二、方案]":      "[2. Solution]",
		"[重复节]":       "[Repeat]",
	}
	out := dir + "/toc_en.md"
	if err := ApplyAlignedText(".md", src, out, tr); err != nil {
		t.Fatalf("写回失败: %v", err)
	}
	res := string(mustRead(t, out))

	// 1) 标题确实翻掉了（否则锚点重算无意义）
	if !strings.Contains(res, "## 1. Background & Goals") {
		t.Fatalf("标题未译，锚点用例失去前提:\n%s", res)
	}
	// 2) 目录锚点跟着换成译文 slug（GitHub 规则：标点剔除、空格转 -）
	if !strings.Contains(res, "[1. Background & Goals](#1-background--goals)") {
		t.Fatalf("RC-6 未修：目录锚点仍指向中文 slug:\n%s", res)
	}
	if strings.Contains(res, "](#一项目背景与目标)") {
		t.Fatalf("残留中文锚点:\n%s", res)
	}
	// 3) 真实 URL 与其 fragment 一个字都不许动（只重算 `(#…)` 形态的内部锚点）
	if !strings.Contains(res, "[官网](https://example.com/老目录#中文锚)") {
		t.Fatalf("外部链接被改写:\n%s", res)
	}
	// 4) 同名标题的 -1 后缀两侧对齐（第二个 Repeat 必须落 repeat-1）
	if !strings.Contains(res, "[Repeat](#repeat)") || !strings.Contains(res, "[Repeat](#repeat-1)") {
		t.Fatalf("重复标题锚点未按 -1 规则重算:\n%s", res)
	}
	// 5) 行数 1:1（锚点重写不得增删行）
	if strings.Count(res, "\n") != strings.Count(md, "\n") {
		t.Fatalf("锚点重写破坏了行数守恒:\n%s", res)
	}
}

// TestMdAnchorIgnoresFencePseudoHeading 代码围栏里的 `# 注释` 不是标题：
// 把它算进映射表会造出「译文里根本不存在的锚点目标」，污染目录跳转。
func TestMdAnchorIgnoresFencePseudoHeading(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/fence.md"
	md := "# 标题\n\n- [标题](#标题)\n\n```\n# 标题\n```\n"
	if err := os.WriteFile(src, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	out := dir + "/fence_en.md"
	if err := ApplyAlignedText(".md", src, out, map[string]string{"标题": "Title", "[标题]": "[Title]"}); err != nil {
		t.Fatalf("写回失败: %v", err)
	}
	res := string(mustRead(t, out))
	if !strings.Contains(res, "[Title](#title)") {
		t.Fatalf("真标题的锚点未重算:\n%s", res)
	}
	if !strings.Contains(res, "```\n# Title\n```") && !strings.Contains(res, "```\n# 标题\n```") {
		t.Fatalf("围栏内容形态异常:\n%s", res)
	}
	// 围栏内那行 `# 标题` 参与映射的话，第二个 fence 行会被当成「另一处同名标题」，
	// 使外部锚点落到 #title-1 —— 断言它没发生。
	if strings.Contains(res, "[Title](#title-1)") {
		t.Fatalf("围栏内假标题参与了锚点映射:\n%s", res)
	}
}

// TestMdSlugGitHubFlavor slug 口径钉死：与 GitHub 生成的 anchor 一致，否则重算后照样跳不动。
func TestMdSlugGitHubFlavor(t *testing.T) {
	cases := map[string]string{
		"一、项目背景与目标":             "一项目背景与目标",
		"1. Background & Goals": "1-background--goals",
		"API 与 SDK":             "api-与-sdk",
		"**加粗**标题":              "加粗标题",
		"snake_case 保留":         "snake_case-保留",
		"[链接标题](https://x.dev)": "链接标题",
	}
	for in, want := range cases {
		if got := mdSlug(mdAnchorPlainText(in)); got != want {
			t.Errorf("mdSlug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := mdSlug("  "); got != "" {
		t.Errorf("空标题应得空 slug，实得 %q", got)
	}
}
