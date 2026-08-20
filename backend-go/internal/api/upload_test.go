// ============ 本文件职责中文说明 ============
// 上传校验单元测试：扩展名白名单判定（大小写/隐藏文件）与 parseUpload 大小/类型校验。
// ========================================
package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// validateUploadExt：白名单内/外、大小写、无扩展名。
func TestValidateUploadExt(t *testing.T) {
	cases := []struct {
		filename string
		want     bool
	}{
		{"a.docx", true},
		{"a.DOCX", true}, // 大写应归一化放行
		{"a.pdf", true},
		{"a.xlsx", true},
		{"a.pptx", true},
		{"a.exe", false}, // 危险类型拒绝
		{"a.doc", false}, // 不在白名单
		{"a", false},     // 无扩展名
		{"a.", false},    // 空扩展名
		{"archive.tar.gz", false},
	}
	for _, c := range cases {
		if got := validateUploadExt(c.filename, translateExtWhitelist); got != c.want {
			t.Errorf("validateUploadExt(%q)=%v want %v", c.filename, got, c.want)
		}
	}
	// KB 白名单（表格类）
	if !validateUploadExt("a.xls", kbExtWhitelist) || !validateUploadExt("a.csv", kbExtWhitelist) {
		t.Fatal("KB 白名单应含 xls/csv")
	}
	if validateUploadExt("a.docx", kbExtWhitelist) {
		t.Fatal("KB 白名单不应含 docx")
	}
}

// buildUploadRequest 构造带文件的 multipart 请求。
func buildUploadRequest(t *testing.T, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("创建表单文件失败: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("写入文件内容失败: %v", err)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/translate", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// parseUpload：类型校验拒绝非白名单文件。
func TestParseUploadRejectExt(t *testing.T) {
	req := buildUploadRequest(t, "evil.exe", []byte("MZ..."))
	err := parseUpload(req, translateUploadMax, translateExtWhitelist)
	if err == nil {
		t.Fatal(".exe 应被拒绝")
	}
	if !strings.Contains(err.Error(), "不支持的文件类型") {
		t.Fatalf("错误信息应提示类型不支持: %v", err)
	}
}

// parseUpload：正常文件通过。
func TestParseUploadAccept(t *testing.T) {
	req := buildUploadRequest(t, "report.docx", []byte("%PDF-ish"))
	if err := parseUpload(req, translateUploadMax, translateExtWhitelist); err != nil {
		t.Fatalf(".docx 应通过: %v", err)
	}
}

// parseUpload：超过大小上限应拒绝。
func TestParseUploadTooBig(t *testing.T) {
	req := buildUploadRequest(t, "big.pdf", bytes.Repeat([]byte("x"), 1024))
	if err := parseUpload(req, 512, translateExtWhitelist); err == nil {
		t.Fatal("超过上限应拒绝")
	}
}

// whitelistKeys：返回非空扩展名列表（错误提示用）。
func TestWhitelistKeys(t *testing.T) {
	keys := whitelistKeys(translateExtWhitelist)
	if len(keys) != 4 {
		t.Fatalf("翻译白名单应有 4 个扩展名, got %d", len(keys))
	}
}
