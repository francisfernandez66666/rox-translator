// ============ 本文件职责中文说明 ============
// #38（2026-09-21 评审缺陷）上传扩展名白名单分口单测：
// 复现并钉死「TMX 导入被知识库表格白名单挡死」这一缺陷——
//
//	① .tmx/.xml 在 KB 白名单下必须被拒（证明旧行为，防回退成同一个白名单）；
//	② .tmx/.xml 在 TMX 白名单下必须通过；
//	③ xlsx/csv 等表格件仍走 KB 白名单（互不影响）；
//	④ 拒绝原因必须原文可回显（旧实现吞成「文件上传失败」，用户无从判断类型还是体积）。
//
// 纯 parseUpload 层断言，不依赖数据库/HTTP 服务，双方言无关。
package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// multipartFileReq 造一条只有一个 "file" 字段的 multipart 请求。
func multipartFileReq(t *testing.T, filename, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 失败: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("写入文件内容失败: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/translation/import-tmx", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

// TestUploadWhitelistSplitsTMXAndKB TMX 与 KB 表格各用各的白名单。
func TestUploadWhitelistSplitsTMXAndKB(t *testing.T) {
	const tmxDoc = `<?xml version="1.0" encoding="UTF-8"?>` + "\n<TMX version=\"1.4\"><body><tu><tuv xml:lang=\"zh\"><seg>你好</seg></tuv><tuv xml:lang=\"en\"><seg>Hello</seg></tuv></tu></body></TMX>"

	// ① KB 白名单必须继续拒绝 .tmx（口径未被顺手放宽）
	if err := parseUpload(multipartFileReq(t, "mem.tmx", tmxDoc), kbUploadMax, kbExtWhitelist); err == nil {
		t.Fatalf("KB 白名单不应接受 .tmx 文件")
	} else if !strings.Contains(err.Error(), "不支持的文件类型") {
		t.Fatalf("拒绝原因应为类型白名单，实际: %v", err)
	}
	// ② TMX 白名单接受 .tmx / .xml
	for _, name := range []string{"mem.tmx", "MEM.TMX", "mem.xml"} {
		if err := parseUpload(multipartFileReq(t, name, tmxDoc), kbUploadMax, tmxExtWhitelist); err != nil {
			t.Fatalf("TMX 白名单应接受 %s，实际拒绝: %v", name, err)
		}
	}
	// ③ TMX 白名单不接受表格件（两口径不互相敞开）
	for _, name := range []string{"rows.csv", "book.xlsx"} {
		if err := parseUpload(multipartFileReq(t, name, "a,b\n1,2"), kbUploadMax, tmxExtWhitelist); err == nil {
			t.Fatalf("TMX 白名单不应接受 %s", name)
		}
	}
	// ④ 拒绝文案带出允许列表（可直接回显给前端）
	err := parseUpload(multipartFileReq(t, "evil.exe", "MZ"), kbUploadMax, tmxExtWhitelist)
	if err == nil || !strings.Contains(err.Error(), ".tmx") {
		t.Fatalf("拒绝文案应包含允许列表，实际: %v", err)
	}
}
