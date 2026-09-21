// ============ 本文件职责说明 ============
// D5 回归（2026-09-12）：txt/csv/md 写回必须按原文件行对齐——空行/重复行/
// 代码围栏/分隔线不丢失，md 结构前缀与链接 URL 保留。
//
// ★ 2026-09-22 结构层重做时同步改写了围栏断言（工单 T20260921075004EF8 / 缺陷记录 RC-3）：
//
//	旧断言 `if !strings.Contains(res, "```go\n你好\n```") { t.Fatalf("代码围栏内容被误翻译") }`
//	把「围栏整块不译」固化成了正确行为——**这条固化本身是缺陷能长期存在的直接原因**：
//	任何修复都会让 CI 变红、被当成回归回滚。用户已裁定「整块不译是缺陷」。
//	新口径（三类分派）：真源码块内**代码语句逐字节保留、注释翻译**；
//	本用例同时锁住这两半，原意（空行/重复行/前缀/URL 不丢）全部保留。
//
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
	if got := string(mustRead(t, out)); got != "Hello\n\nHello\nBye\n" {
		t.Fatalf("txt 行对齐失败: %q", got)
	}

	// md：go 块内「代码语句逐字节不动 + 注释被译」，结构前缀粘回，链接 URL 保住，未译行原样留着
	srcM := dir + "/b.md"
	md := "# 标题\n\n```go\nfunc main() { /* 初始化 */ }\nx := 1 // 计算成本\n```\n\n- 列表项\n[链接文字](https://x.dev)\n未译行\n"
	if err := os.WriteFile(srcM, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	outM := dir + "/b_en.md"
	// ★ 键必须是「提取侧真实产出的片段」：链接的文字部分含方括号（URL 是骨架洞，不入库），
	//   这与旧口径「剥掉全部标记后剩裸文字」不同——正是这一点保证标记不会被顺手删掉。
	tr := map[string]string{
		"标题":     "Title",
		"初始化":    "init",
		"计算成本":   "compute cost",
		"列表项":    "Item",
		"[链接文字]": "[Link text]",
	}
	if err := ApplyAlignedText(".md", srcM, outM, tr); err != nil {
		t.Fatalf("md 写回失败: %v", err)
	}
	res := string(mustRead(t, outM))

	// 围栏骨架与代码语句：逐字节保留（真代码翻了就是坏代码）
	if !strings.Contains(res, "func main() {") {
		t.Fatalf("go 块内代码语句被改写: %q", res)
	}
	if !strings.Contains(res, "x := 1 //") {
		t.Fatalf("行尾注释的代码前缀被改写: %q", res)
	}
	if !strings.Contains(res, "```go\n") || !strings.Contains(res, "\nx := 1") || strings.Count(res, "```") != 2 {
		t.Fatalf("围栏标记/行序被破坏: %q", res)
	}
	// 注释：必须被翻译（用户裁定「真代码不译、注释译」）
	if !strings.Contains(res, "// compute cost") {
		t.Fatalf("go 块内注释未翻译（旧口径整块不译的残留）: %q", res)
	}
	if !strings.Contains(res, "/* init */") {
		t.Fatalf("块注释正文未翻译: %q", res)
	}
	// 结构前缀与 URL
	if !strings.Contains(res, "# Title") {
		t.Fatalf("标题前缀未粘回: %q", res)
	}
	if !strings.Contains(res, "- Item") {
		t.Fatalf("列表前缀未保留: %q", res)
	}
	if !strings.Contains(res, "[Link text](https://x.dev)") {
		t.Fatalf("链接 URL 丢失: %q", res)
	}
	if !strings.Contains(res, "\n未译行\n") {
		t.Fatalf("未命中行被改写: %q", res)
	}
	if g, e := len(strings.Split(res, "\n")), len(strings.Split(md, "\n")); g != e {
		t.Fatalf("产物行数 %d ≠ 原文行数 %d", g, e)
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
