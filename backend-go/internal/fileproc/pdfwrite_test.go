// ============ 本文件职责中文说明 ============
// PDF 排版回写单元测试：内置字体解析、译文 PDF 生成（%PDF 头 + 非空体积）、无字体环境降级路径。
package fileproc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolvePDFFontBundled 内置字体应可被解析到且为可用 TTF。
func TestResolvePDFFontBundled(t *testing.T) {
	p := ResolvePDFFont()
	if p == "" {
		t.Fatal("内置字体解析失败")
	}
	if !isUsableTTF(p) {
		t.Fatalf("解析到的字体不可用: %s", p)
	}
}

// TestWriteTranslatedPDF 生成含中文译文的 PDF：文件存在、%PDF 头、体积合理。
func TestWriteTranslatedPDF(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.pdf")
	srcs := []string{
		"车辆用户手册",
		"激活蓝牙钥匙后，车辆将自动解锁并同步座椅记忆设置。请确保手机蓝牙已开启。",
		"本章节描述了紧急呼叫功能的触发条件与操作步骤。",
	}
	tr := map[string]string{
		"车辆用户手册": "Vehicle User Manual",
		"激活蓝牙钥匙后，车辆将自动解锁并同步座椅记忆设置。请确保手机蓝牙已开启。": "After the Bluetooth key is activated, the vehicle will unlock automatically and sync seat memory settings.",
	}
	if err := WriteTranslatedPDF(context.Background(), out, srcs, tr); err != nil {
		t.Fatalf("WriteTranslatedPDF: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 1000 || !strings.HasPrefix(string(b[:8]), "%PDF") {
		t.Fatalf("产物异常: size=%d head=%q", len(b), b[:min(8, len(b))])
	}
}

// min 两整数取小（测试内使用）。
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestWriteTranslatedPDFNoFont 无字体时返回错误（供调用方降级 xlsx）。
func TestWriteTranslatedPDFNoFont(t *testing.T) {
	t.Setenv("PDF_FONT_PATH", "/nonexistent/font.ttf")
	savedCandidates := systemFontCandidates
	savedProbe := bundledFontProbe
	systemFontCandidates = nil
	bundledFontProbe = "skip"
	defer func() {
		systemFontCandidates = savedCandidates
		bundledFontProbe = savedProbe
	}()
	err := WriteTranslatedPDF(context.Background(), filepath.Join(t.TempDir(), "o.pdf"), []string{"测试"}, nil)
	if err == nil {
		t.Fatal("无字体时应返回错误")
	}
}
