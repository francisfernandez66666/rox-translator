// S8 词包扫描器单测：加载/热更新/大小写/注释/回显上限。
package sensitive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写词包失败: %v", err)
	}
}

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
