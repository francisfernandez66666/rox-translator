// ============================================================================
// lang_middleware_test.go — 后端语言识别中间件回归（★ 2026-09-24 〇-S #12）
// 锁定 langWriter 二态行为：
//   - 无头/zh 头：响应字节与改造前一致（中文 message 原样、状态码/Content-Type 不动）；
//   - en 头：JSON 响应里 "message" 值翻英，其它键与字段顺序不动，Content-Length 按新体积重算；
//   - 非 JSON（SSE text/event-stream）：直通不缓冲；
//   - 超 1MB 的 JSON：冲刷后直通（不抠内存也不误翻）。
//
// ============================================================================
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"translator/internal/i18n"
)

// langProbe 一个只依赖 writeJSON 的最小 handler：返回带中文 message 的 JSON。
func langProbe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": false,
		"message": "验证码错误或已过期",
		"data":    map[string]string{"note": "非 message 字段不翻"},
	})
}

func TestWithLangZhDefaultUntouched(t *testing.T) {
	s := &Server{}
	h := s.withLang(http.HandlerFunc(langProbe))
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "验证码错误或已过期") {
		t.Fatalf("无头请求应保持中文（UAT 兼容），实际 %s", rec.Body.String())
	}
}

func TestWithLangEnTranslatesMessage(t *testing.T) {
	s := &Server{}
	h := s.withLang(http.HandlerFunc(langProbe))
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set(i18n.HeaderLang, "en")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应 200，实际 %d", rec.Code)
	}
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(rec.Body.Len()) {
		t.Fatalf("Content-Length 应等于最终体积，实际 %q 体积 %d", cl, rec.Body.Len())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应应仍是合法 JSON: %v (%s)", err, rec.Body.String())
	}
	// 等值锁：译文逐字取词条表值，防止后续改表悄悄改变响应
	if body["message"] != "Verification code invalid or expired" {
		t.Fatalf("en 语境 message 应为词条表译文，实际 %v", body["message"])
	}
	// 非 message 字段不许被误伤
	if body["data"].(map[string]interface{})["note"] != "非 message 字段不翻" {
		t.Fatalf("非 message 字段被改写: %v", body["data"])
	}
}

func TestWithLangConcatAndPattern(t *testing.T) {
	s := &Server{}
	probe := func(msg string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"message": msg})
		}
	}
	cases := []struct{ zh, wantPrefix string }{
		{"保存失败: db is locked", "Save failed:"},
		{"注册过于频繁，请 60 秒后再试", "Registrations too frequent; retry in 60"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req.Header.Set(i18n.HeaderLang, "en")
		rec := httptest.NewRecorder()
		s.withLang(probe(c.zh)).ServeHTTP(rec, req)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if !strings.HasPrefix(body["message"], c.wantPrefix) {
			t.Fatalf("%q 应翻成 %q 开头，实际 %q", c.zh, c.wantPrefix, body["message"])
		}
	}
}

func TestWithLangSSEPassThrough(t *testing.T) {
	s := &Server{}
	sse := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"message\":\"进度 50%\"}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil)
	req.Header.Set(i18n.HeaderLang, "en")
	rec := httptest.NewRecorder()
	s.withLang(sse).ServeHTTP(rec, req)
	// SSE 属非 JSON 直通：中文字节必须原样（message 字段也不翻——它不是响应契约里的提示语）
	if got := rec.Body.String(); got != "data: {\"message\":\"进度 50%\"}\n\n" {
		t.Fatalf("SSE 直通被改写: %q", got)
	}
}

func TestWithLangBigJSONFallsBackToPassThrough(t *testing.T) {
	s := &Server{}
	big := strings.Repeat("啊", 600_000) // 1.8MB UTF-8，远超 1MB 缓冲上限
	h := s.withLang(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"message": big})
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set(i18n.HeaderLang, "en")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), big) {
		t.Fatalf("超大 JSON 应直通且不丢字节，实际长度 %d", rec.Body.Len())
	}
}

func TestWithLangEmptyBodyPreservesStatus(t *testing.T) {
	s := &Server{}
	h := s.withLang(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set(i18n.HeaderLang, "en")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("空响应状态码应保持 204，实际 %d", rec.Code)
	}
}

// TestLangWiredIntoHandlerChain 静态锁：Handler() 顶层必须挂 withLang（漏接线=整套机制空转）。
func TestLangWiredIntoHandlerChain(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读取 server.go 失败: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "s.withLang(s.withAPIVersion(") {
		t.Fatal("Handler() 未把 withLang 接到中间件链最外层")
	}
	if !strings.Contains(s, "X-App-Lang") {
		t.Fatal("CORS Allow-Headers 未放行 X-App-Lang")
	}
}

// TestAPICnMessageLiteralsCovered 词条覆盖棘轮（★ 〇-S #12 补漏批）：
// internal/api 非测试源码里两类写死中文提示——
//
//	① writeJSON 的 `"message": "中文"` 字面量；
//	② apierrors.New(code, "中文") 的第一段消息字面量——
//
// 必须逐条命中 i18n 词条表（Msg 能翻出不同译文）。新增写死中文不进 catalog_en.go 即红灯，
// 堵住「首轮只盘点 ① 漏掉 ② 整类 36 条」的同款事故。
func TestAPICnMessageLiteralsCovered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	ctxEn := i18n.WithLang(context.Background(), "en")
	msgLit := regexp.MustCompile(`"message":\s*("(?:[^"\\]|\\.)*")`)
	errNew := regexp.MustCompile(`apierrors\.New\([^,]+,\s*("(?:[^"\\]|\\.)*")\s*[,)]`)
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var lits []string
		for _, re := range []*regexp.Regexp{msgLit, errNew} {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				s, err := strconv.Unquote(m[1])
				if err != nil {
					continue
				}
				lits = append(lits, s)
			}
		}
		for _, s := range lits {
			if !containsHan(s) {
				continue // 纯英文/占位提示不进词典
			}
			checked++
			if i18n.Msg(ctxEn, s) == s {
				t.Errorf("%s: 写死中文 %q 未进词条表（跑 scripts/gen_i18n_catalog.py 补录后重新生成 catalog）", f, s)
			}
		}
	}
	if checked < 300 {
		t.Fatalf("扫描命中中文提示仅 %d 条（<300），正则口径疑似退化", checked)
	}
}

// containsHan 判断字符串是否含 CJK 基本区汉字。
func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}
