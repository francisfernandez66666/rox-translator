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

// TestAPICnMessageLiteralsCovered 词条覆盖棘轮（★ 〇-S #12 补漏批 ×2）：
// internal/api 非测试源码里三类写死中文提示——
//
//	① writeJSON 的 `"message": "中文"` 字面量；
//	② apierrors.New(code, "中文") 的第一段消息字面量；
//	③ writeJSON 的 `"error": "中文"` 字面量（metrics/spa/stream 早期内联写法）——
//
// 必须逐条命中 i18n 词条表（Msg 能翻出不同译文）。新增写死中文不进 catalog_en.go 即红灯，
// 堵住「首轮只盘点 ① 漏掉 ②③ 整类」的同款事故。
func TestAPICnMessageLiteralsCovered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	ctxEn := i18n.WithLang(context.Background(), "en")
	msgLit := regexp.MustCompile(`"message":\s*("(?:[^"\\]|\\.)*")`)
	errLit := regexp.MustCompile(`"error":\s*("(?:[^"\\]|\\.)*")`)
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
		for _, re := range []*regexp.Regexp{msgLit, errLit, errNew} {
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

// hanMsgIdents 找出 src 里「被赋过中文**字面量**的变量名」。
// 只认整行单行字面量赋值（`x := "中文"` / `x = "中文"`），与词条覆盖棘轮同一种粗粒度口径。
func hanMsgIdents(src string) map[string]bool {
	assign := regexp.MustCompile(`(?m)^\s*(\w+)\s*(?::=|=)\s*("(?:[^"\\]|\\.)*")\s*$`)
	out := map[string]bool{}
	for _, m := range assign.FindAllStringSubmatch(src, -1) {
		s, err := strconv.Unquote(m[2])
		if err == nil && containsHan(s) {
			out[m[1]] = true
		}
	}
	return out
}

// TestAPICnMessageEscapePattern 词条覆盖的**盲区锁**（★ 2026-09-28 〇-Z）：
//
//	上一条棘轮只认 `"message": "中文"` / `"error": "中文"` / `apierrors.New(code, "中文")`
//	三种**字面量在位**的写法。一旦写成 `msg := "中文"; New(code, msg)`，字面量就落在正则射程外，
//	闸门对着一个"消息来自变量"的调用无从判起，于是**静默放行**——英文访客看到整句中文，
//	而所有 i18n 闸门全绿。本批实跑就撞上两次：trial.go 的三条「试用已用完」与
//	tickets.go 批 I-10 加的「系统繁忙，工单取消未成功，请稍候重试」（后者漏词条至今没人发现）。
//
//	本锁与上一条**配对**才闭环：这条负责「凡中文经变量传给 New 一律点名」，上一条负责
//	「在位字面量必须翻得出」。两句合起来才等于「面向人的中文提示没有第三条逃路」。
//
// 判据三条（缺一条就是空锁）：
//
//	① 反证：合成源里那种写法必须被点名（抓不到＝锁空转，改判据时必须先跑这一条）；
//	② 反向对照：合成源里**合规**写法（字面量在位）不得被点名，防止把上一条棘轮的正确形态判红；
//	③ 实扫 internal/api 全部非测试源码必须 0 处（现存两处已随批改成在位字面量并补词条）。
func TestAPICnMessageEscapePattern(t *testing.T) {
	// ① 反证——中文先赋变量、再当消息传给 New，必须抓到
	bad := "func h() {\n\tmsg := \"操作太快了，请稍后再试\"\n\tapierrors.New(apierrors.ErrRateLimited, msg)\n}\n"
	if got := len(apiEscapeHitsIn(bad)); got != 1 {
		t.Fatalf("盲区锁反证失败：合成源应点名 1 处，实际 %d 处（判据退化＝恒空假绿）", got)
	}
	// ② 反向对照——字面量在位（上一条棘轮的射程）不该被本锁点名
	good := "func h() {\n\tapierrors.New(apierrors.ErrRateLimited, \"操作太快了，请稍后再试\")\n}\n"
	if got := apiEscapeHitsIn(good); len(got) != 0 {
		t.Fatalf("盲区锁误伤合规写法：%v（在位字面量应交给上一条棘轮判词条）", got)
	}
	// ②b 反向对照——消息来自 error 文本（非中文字面量赋值）也不该点名
	dyn := "func h() {\n\tmsg := err.Error()\n\tapierrors.New(apierrors.ErrInternal, msg)\n}\n"
	if got := apiEscapeHitsIn(dyn); len(got) != 0 {
		t.Fatalf("盲区锁误伤动态消息：%v（底层原文透出属预期形态）", got)
	}
	// ③ 实扫全包
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range apiEscapeHitsIn(string(src)) {
			total++
			t.Errorf("%s: 中文提示经变量 %s 传给 apierrors.New，绕开了词条覆盖棘轮 —— 请把字面量直接写在实参位并跑 scripts/gen_i18n_catalog.py 补录", f, hit)
		}
	}
	if total > 0 {
		t.Fatalf("共 %d 处「变量传参逃词条」待收口", total)
	}
}

// apiEscapeHitsIn 返回单个源文件里「被赋过中文字面量的变量」又被用作 apierrors.New 消息的标识符。
// 抽成纯函数是 ①② 反证能离线跑起来的前提（不必往仓库里塞一个故意写错的文件）。
func apiEscapeHitsIn(src string) []string {
	hids := hanMsgIdents(src)
	if len(hids) == 0 {
		return nil
	}
	newID := regexp.MustCompile(`apierrors\.New\([^,()]+,\s*(\w+)\s*\)`)
	var hits []string
	for _, m := range newID.FindAllStringSubmatch(src, -1) {
		if hids[m[1]] {
			hits = append(hits, m[1])
		}
	}
	return hits
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

// TestWithLangErrorFieldTranslates ③ 类收口：内联 {"error":"中文"} 也随语种翻；
// 英文错误码值（非词条）必须原样不动——防止把机器可读 code 翻坏。
func TestWithLangErrorFieldTranslates(t *testing.T) {
	s := &Server{}
	h := s.withLang(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "接口不存在"})
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/nothing", nil)
	req.Header.Set(i18n.HeaderLang, "en")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "API endpoint not found" {
		t.Fatalf("error 字段应翻为词条表译文，实际 %q", body["error"])
	}
	// 反证：英文码值不在词条表，原样透传
	h2 := s.withLang(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_api_key"})
	}))
	req2 := httptest.NewRequest(http.MethodGet, "/openapi/x", nil)
	req2.Header.Set(i18n.HeaderLang, "en")
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req2)
	if !strings.Contains(rec2.Body.String(), `"invalid_api_key"`) {
		t.Fatalf("英文错误码被误伤: %s", rec2.Body.String())
	}
}
