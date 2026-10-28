// ============ sensitive_test.go · 单元测试 ============
// 覆盖 S8 敏感词闸：
//
//	① 词包加载/热更新/注释/回显上限/文件缺失保底（原回归）
//	② 归一化检测口径（★ P0-5 修复 2026-09-14）：大小写折叠、NFKC 全角→半角、
//	   零宽字符剥离、字符间空白剥离；干净文本零误报
//
// =============================================
package sensitive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFile 写临时词包文件。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写词包失败: %v", err)
	}
}

// newTestChecker 用临时词包创建扫描器（绕过文件系统依赖）。
func newTestChecker(t *testing.T, words ...string) *Checker {
	t.Helper()
	p := filepath.Join(t.TempDir(), "words.txt")
	content := ""
	for _, w := range words {
		content += w + "\n"
	}
	writeFile(t, p, content)
	return New(p)
}

// TestCheckerHitsAndReload 词包加载/热更新/注释/回显上限/文件缺失保底（原回归用例）。
func TestCheckerHitsAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "words.txt")
	writeFile(t, path, "# 注释行\n\n违禁词A\nFirearms\n枪支\n")
	c := New(path)
	if n := c.Count(); n != 3 {
		t.Fatalf("词条数应为 3，实际 %d", n)
	}
	// 大小写折叠命中 + 中文命中
	hits := c.Hits("this is about FIREARMS and 枪支")
	if len(hits) != 2 {
		t.Fatalf("应命中 2 词，实际 %v", hits)
	}
	if c.Has("无涉内容 normal business text") {
		t.Fatal("正常文本不应命中")
	}
	// 回显上限 5
	writeFile(t, path, "w1\nw2\nw3\nw4\nw5\nw6\nw7")
	time.Sleep(10 * time.Millisecond)
	if hs := c.Hits("w1 w2 w3 w4 w5 w6 w7"); len(hs) != 5 {
		t.Fatalf("命中回显应截断为 5，实际 %d", len(hs))
	}
	// mtime 热加载：换词包后旧词不再命中
	writeFile(t, path, "only_this")
	time.Sleep(10 * time.Millisecond)
	if c.Has("w1") {
		t.Fatal("热加载后旧词应失效")
	}
	if !c.Has("Only_THIS case folded") {
		t.Fatal("新词大小写命中失效")
	}
	// 文件缺失：保留旧表（宁可用旧词包继续拦）
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !c.Has("only_this") {
		t.Fatal("词包文件缺失时应保留旧表")
	}
}

// TestHitsNormalization Unicode 混淆向量全部必须命中（P0-5 修复前全部绕过）。
func TestHitsNormalization(t *testing.T) {
	c := newTestChecker(t, "firearms", "枪支")
	cases := []string{
		"FireARMS for sale", // 大小写折叠
		"ＦＩＲＥＡＲＭＳ 出售",       // 全角字母（NFKC 归一）
		"枪\u200b支交易",        // 零宽空格插入
		"F i r e a r m s",   // 字符间空格（宽松口径第二遍）
	}
	for _, text := range cases {
		if got := c.Hits(text); len(got) == 0 {
			t.Errorf("混淆文本未被拦截: %q", text)
		}
	}
	// 误报对照：不含敏感词的干净文本
	for _, clean := range []string{"普通中文句子", "hello world", "Fire sale 消防演习"} {
		if got := c.Hits(clean); len(got) > 0 {
			t.Errorf("干净文本误报: %q → %v", clean, got)
		}
	}
}

// TestHitsFullwidthCJK 中文词内全角数字/字母归一（词表与文本同口径）。
func TestHitsFullwidthCJK(t *testing.T) {
	c := newTestChecker(t, "紫火核弹T36")
	if got := c.Hits("紫火核弹Ｔ３６ 出售"); len(got) == 0 {
		t.Errorf("全角数字混淆未被拦截")
	}
	if got := c.Hits("紫\u3000火核弹T36"); len(got) == 0 {
		t.Errorf("全角空格插入未被拦截")
	}
}

// TestNormalize 基础归一化函数行为锚定。
func TestNormalize(t *testing.T) {
	if normalize("ＡＢｃ１２３") != "abc123" {
		t.Errorf("NFKC 全角折叠失效: %q", normalize("ＡＢｃ１２３"))
	}
	if normalize("枪\u200b支") != "枪支" {
		t.Errorf("零宽剥离失效: %q", normalize("枪\u200b支"))
	}
	if normalizeLoose("F i r e") != "fire" {
		t.Errorf("宽松口径空白剥离失效: %q", normalizeLoose("F i r e"))
	}
}

// TestLatinWordsUseWordBoundary 拉丁词条按**词边界**匹配（★ 2026-10-29 现网实证）。
// 旧形态把拉丁词也丢进 `normalizeLoose` 子串：正常英文译文「…calls down…」剥空格后拼成
// 「…callsdown…」，里面恰好有 `lsd` ⇒ 词表里的 `LSD` 把整段正常译文判成敏感词，
// 交付物上那一行变占位符（回落原文＝图上留中文）。这条锁两边都判：
// 不跨词边界误杀；原样/大写/全角/零宽/字符间空格仍要拦住。
func TestLatinWordsUseWordBoundary(t *testing.T) {
	c := newTestChecker(t, "LSD", "枪支")

	for _, benign := range []string{
		"The signals are calls down the list.",
		"Tools designed for trading.",
		"results during the day",
	} {
		if hits := c.Hits(benign); len(hits) > 0 {
			t.Fatalf("正常译文被误判（跨词边界拼出 lsd）：%q 命中 %v", benign, hits)
		}
	}

	for _, bad := range []string{"lsd", "LSD", "ＬＳＤ", "l\u200bsd", "L S D", "是lsd。"} {
		if hits := c.Hits(bad); len(hits) == 0 {
			t.Fatalf("应命中却漏（拉丁词边界口径不能放走混淆写法）：%q", bad)
		}
	}

	// CJK 词条仍走「子串 + 剥空格」口径：字符间插空格是中文侧最常见的绕过写法。
	if hits := c.Hits("枪 支"); len(hits) == 0 {
		t.Fatalf("CJK 词条松散口径失效：「枪 支」没命中")
	}
}
