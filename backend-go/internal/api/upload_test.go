// ============ 本文件职责中文说明 ============
// 上传校验单元测试：扩展名白名单判定（大小写/隐藏文件）与 parseUpload 大小/类型校验。
// ========================================
package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"translator/internal/fileproc"
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
//
// ★ 2026-10-15 重钉为**派生等值锁**（原形态钉死 12 个，OCR/ASR 批把准入清单扩到 27 项后恒红）：
// 写死数字这类锁的坏处不是"会过时"而是"过时了还在替旧口径说话"——旧断言看似在守白名单，
// 实际守的是"扩项之前那一代清单的长度"，谁扩一次格式就得先猜一次它想数什么。
// 现在只问一件真有判别力的事：api 层这把闸**是否逐项等于 fileproc 那一份唯一事实源**
// （AGENTS §一·14「准入清单只有一份」在 api 侧的接线腿；§一·14 的机械锁在
// upload_formats_gate_test.go 守清单本身，这一条守的是调用方有没有拿到同一份）。
// 两侧都判：少一项＝api 收了别人不收的（客户传上来后端才拒），多一项＝准入被 api 层私加。
func TestWhitelistKeys(t *testing.T) {
	keys := whitelistKeys(translateExtWhitelist)
	if len(keys) == 0 {
		t.Fatal("白名单为空：错误提示将一句格式都不列")
	}
	got := make(map[string]bool, len(keys))
	for _, k := range keys {
		got[k] = true
	}
	if missing := whitelistMissing(got); len(missing) > 0 {
		t.Fatalf("准入清单有而 api 白名单没有：%v（fileproc 收了、上传闸却拒＝两把尺子打架）", missing)
	}
	if extra := whitelistExtra(got); len(extra) > 0 {
		t.Fatalf("api 白名单多出清单外的项：%v（＝这一层私加准入）", extra)
	}

	// ---- 反证两枚：证明上面两条腿真的会抓，而不是"恒空的形状检查" ----
	// R1 少了 .docx ⇒ whitelistMissing 必须点名它（把判据摘成恒空的那类破坏就在这里现形）
	truncated := make(map[string]bool, len(got))
	for k := range got {
		if k != ".docx" {
			truncated[k] = true
		}
	}
	if m := whitelistMissing(truncated); !containsString(m, ".docx") {
		t.Fatalf("反证 R1 失效：白名单少了 .docx 却没被抓到（实得 %v）", m)
	}
	// R2 混进一个清单外的 .exe ⇒ whitelistExtra 必须点名它
	polluted := make(map[string]bool, len(got)+1)
	for k := range got {
		polluted[k] = true
	}
	polluted[".exe"] = true
	if e := whitelistExtra(polluted); !containsString(e, ".exe") {
		t.Fatalf("反证 R2 失效：白名单私加 .exe 却没被抓到（实得 %v）", e)
	}
}

// whitelistMissing 列出唯一事实源有、而待检白名单没有的扩展名（升序返回便于判据与报错稳定）。
func whitelistMissing(got map[string]bool) []string {
	var out []string
	for _, want := range fileproc.TranslateFormatOrder {
		if !got[want] {
			out = append(out, want)
		}
	}
	sort.Strings(out)
	return out
}

// whitelistExtra 列出待检白名单有、而唯一事实源不认的扩展名（升序同上）。
func whitelistExtra(got map[string]bool) []string {
	var out []string
	for k := range got {
		if !fileproc.IsTranslateFormat(k, false) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
