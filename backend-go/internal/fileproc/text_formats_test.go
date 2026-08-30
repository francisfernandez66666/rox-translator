// ============ 本文件职责中文说明 ============
// 纯文本类格式提取器与 xlsx 对照表单元测试：
// srt 字幕块、md 围栏/链接剥离、json 字符串值、yaml 标量、WriteComparisonXlsx 产物。
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeTemp 写临时文件并返回路径。
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestExtractSrt 字幕提取：跳过序号/时间轴，块内行合并为一条。
func TestExtractSrt(t *testing.T) {
	p := writeTemp(t, "a.srt", "1\n00:00:01,000 --> 00:00:02,000\nHello world\nsecond line\n\n2\n00:00:03,000 --> 00:00:04,000\nGoodbye\n")
	texts, err := ExtractTexts(p)
	if err != nil {
		t.Fatalf("ExtractTexts 失败: %v", err)
	}
	if len(texts) != 2 || texts[0] != "Hello world second line" || texts[1] != "Goodbye" {
		t.Fatalf("srt 提取结果异常: %#v", texts)
	}
}

// TestExtractMd Markdown：代码块剔除、图片/链接剥离、正文保留。
func TestExtractMd(t *testing.T) {
	md := "# 标题\n\n看这张 ![图](https://x/a.png) 和 [链接](https://y)\n\n```go\ncode_should_skip()\n```\n\n**加粗**正文行\n---\n"
	texts, err := ExtractTexts(writeTemp(t, "a.md", md))
	if err != nil {
		t.Fatalf("ExtractTexts 失败: %v", err)
	}
	joined := strings.Join(texts, "|")
	if strings.Contains(joined, "code_should_skip") || strings.Contains(joined, "https://") || joined == "" {
		t.Fatalf("md 应剔除代码块与 URL: %#v", texts)
	}
	if !strings.Contains(joined, "标题") || !strings.Contains(joined, "加粗") {
		t.Fatalf("md 正文应保留: %#v", texts)
	}
}

// TestExtractJson JSON：仅字符串值入库（含嵌套数组），键名不入库。
func TestExtractJson(t *testing.T) {
	js := `{"a":"第一句","b":{"c":["第二句","第三句"]},"num":42}`
	texts, err := ExtractTexts(writeTemp(t, "a.json", js))
	if err != nil {
		t.Fatalf("ExtractTexts 失败: %v", err)
	}
	if len(texts) != 3 {
		t.Fatalf("应提取 3 条字符串值: %#v", texts)
	}
	for _, want := range []string{"第一句", "第二句", "第三句"} {
		found := false
		for _, tx := range texts {
			if tx == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺少 %q: %#v", want, texts)
		}
	}
}

// TestExtractYaml YAML：注释/键丢弃、列表与键值取标量。
func TestExtractYaml(t *testing.T) {
	yml := "# 注释行\nname: 张三\nitems:\n  - 苹果\n  - 香蕉\nempty:\n"
	texts, err := ExtractTexts(writeTemp(t, "a.yaml", yml))
	if err != nil {
		t.Fatalf("ExtractTexts 失败: %v", err)
	}
	if len(texts) != 3 || texts[0] != "张三" {
		t.Fatalf("yaml 提取异常: %#v", texts)
	}
}

// TestWriteComparisonXlsx 对照表：两列表头+数据回读一致。
func TestWriteComparisonXlsx(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.xlsx")
	srcs := []string{"你好", "世界"}
	tr := map[string]string{"你好": "hello"}
	if err := WriteComparisonXlsx(out, srcs, tr); err != nil {
		t.Fatalf("WriteComparisonXlsx 失败: %v", err)
	}
	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatalf("产物无法打开: %v", err)
	}
	defer f.Close()
	v1, _ := f.GetCellValue("Sheet1", "A2")
	v2, _ := f.GetCellValue("Sheet1", "B2")
	if v1 != "你好" || v2 != "hello" {
		t.Fatalf("对照表内容异常: A2=%q B2=%q", v1, v2)
	}
}
