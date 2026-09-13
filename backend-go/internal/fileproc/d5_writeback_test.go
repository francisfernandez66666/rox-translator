// ============ 本文件职责说明 ============
// D5 回归（2026-09-12）：txt/csv/md 写回必须按原文件行对齐——空行/重复行/
// 代码围栏/分隔线不丢失，md 结构前缀与单链接 URL 保留。
// =============================================
package fileproc

import (
	"os"
	"strings"
	"testing"
)

func TestD5AlignedWriteback(t *testing.T) {
	dir := t.TempDir()

	// txt：空行与重复行必须原样保留（旧实现由去重表重建全文，全部丢失）
	src := dir + "/a.txt"
	content := "你好\n\n你好\n再见\n"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	out := dir + "/a_en.txt"
	if err := ApplyAlignedText(".txt", src, out, map[string]string{"你好": "Hello", "再见": "Bye"}); err != nil {
		t.Fatalf("写回失败: %v", err)
	}
	got := string(mustRead(t, out))
	if got != "Hello\n\nHello\nBye\n" {
		t.Fatalf("txt 行对齐失败: %q", got)
	}

	// md：围栏内不翻译、前缀保留、整行单链接 URL 保住
	srcM := dir + "/b.md"
	md := "# 标题\n\n```go\n你好\n```\n\n- 列表项\n[链接文字](https://x.dev)\n未译行\n"
	if err := os.WriteFile(srcM, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	outM := dir + "/b_en.md"
	tr := map[string]string{"标题": "Title", "你好": "Hello", "列表项": "Item", "链接文字": "Link"}
	if err := ApplyAlignedText(".md", srcM, outM, tr); err != nil {
		t.Fatalf("md 写回失败: %v", err)
	}
	res := string(mustRead(t, outM))
	if !strings.Contains(res, "```go\n你好\n```") {
		t.Fatalf("代码围栏内容被误翻译: %q", res)
	}
	if !strings.Contains(res, "# Title") {
		t.Fatalf("标题前缀未粘回: %q", res)
	}
	if !strings.Contains(res, "- Item") {
		t.Fatalf("列表前缀未保留: %q", res)
	}
	if !strings.Contains(res, "[Link](https://x.dev)") {
		t.Fatalf("单链接 URL 丢失: %q", res)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
