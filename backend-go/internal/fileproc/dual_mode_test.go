// ============ dual_mode_test.go · 职责说明 ============
// 工单双模式（2026-09-13）fileproc 侧纯函数单测：
// WriteTranslationMd（纯文案产物拼装）与 anydoc 错误话术映射。
// =============================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTranslationMd(t *testing.T) {
	texts := []string{"第一段", "第二段", "未译段"}
	tr := map[string]string{"第一段": "Para one", "第二段": ""} // 第二段译文为空=未译出
	out := filepath.Join(t.TempDir(), "sub", "doc_en_text.md")
	if err := WriteTranslationMd(out, texts, tr); err != nil {
		t.Fatalf("WriteTranslationMd: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "Para one\n\n") {
		t.Errorf("命中段应输出译文:\n%s", got)
	}
	if !strings.Contains(got, "第二段") || !strings.Contains(got, "未译段") {
		t.Errorf("空译文/未命中段应保留原文:\n%s", got)
	}
	if strings.Contains(got, "Para one\n\nPara one") {
		t.Errorf("不应重复输出")
	}
}

func TestFriendlyAnydocError(t *testing.T) {
	cases := []struct {
		stderr string
		want   string
	}{
		{"ERR:EncryptedError:file is password protected", "文件已加密或带密码，无法提取文案，请解除密码后重试"},
		{"Traceback (most recent call last):\nERR:NeedsOcrError:pages=[3,4]", "扫描版/图片型文档：服务器不提供文字识别（OCR），无法提取文案，请转存为 Word 后重试"},
		{"ERR:MalformedError:bad zip member", "文件结构损坏：未能提取到有效内容"},
		{"ERR:MissingPartError:word/document.xml", "文件结构损坏：未能提取到有效内容"},
		{"ERR:Import:anydoc 库未安装（pip install firecrawl-anydoc）", "纯文案模式依赖未就绪：服务器未安装 firecrawl-anydoc"},
		{"some random stderr without marker", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := friendlyAnydocError(c.stderr); got != c.want {
			t.Errorf("friendlyAnydocError(%q)=%q, want %q", c.stderr, got, c.want)
		}
	}
}

func TestAnydocFormatsWhitelist(t *testing.T) {
	for _, e := range []string{".doc", ".xls", ".ppt", ".odt", ".ods", ".odp", ".rtf", ".epub", ".docm", ".xlsb", ".ppsx"} {
		if !AnydocFormats[e] {
			t.Errorf("anydoc 独占格式缺失: %s", e)
		}
	}
	// Go 原生已支持格式不应列入（走既有管线）；pdf 单独由引擎 anydocSourceExt 判定
	for _, e := range []string{".docx", ".xlsx", ".pptx", ".md", ".txt"} {
		if AnydocFormats[e] {
			t.Errorf("原生支持格式不应在 anydoc 独占表: %s", e)
		}
	}
}
