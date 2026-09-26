// ============================================================================
// internal/api/docs_manual_test.go — 手册 PDF 公网下载面收口（★ F-69，2026-09-26 批 I-8）
//
// 五条锁（全部等值/负向，判据按 AGENTS §一·6「托管物四件套」写：
//
//	200 + 非 HTML 兜底 + %PDF 魔数 + 体积下限；只判 200 视为无效断言）：
//	① 12 语种逐码探针：每码回 application/pdf、体首 %PDF-、体积达下限、且体里**不含**
//	   SPA 壳标记（<!DOCTYPE / index- 字样 0 命中）；zh-hant 连字符 URL 命中下划线文件名；
//	② 路由优先级：同一个 mux 里既挂 /docs/manual/ 也挂 "/"（SPA 兜底桩），
//	   手册请求必须落在 PDF 处理方——**兜底壳不得吃掉托管物**（F-69 的病根就是这条）；
//	③ 回落链如实：无专稿语种回落到 en 时 200，但 Content-Disposition 文件名**等值** en.pdf
//	   （不假装是请求语种——F-64「状态码诚实、内容说谎」同族的正向对照）；
//	④ 三条负向路径逐个带码：白名单外语种 400 / 未铺库 404 / 目录里放非 PDF 500，
//	   且三者的响应体都必须是 JSON 错误体、**不得**是 200 整页 HTML；
//	   路径穿越（../../etc/passwd.pdf 等）直调处理方也只能拿到 400/404，永不落到文件系统；
//	⑤ 方法收口：POST 回 405；HEAD 只回头（Content-Length 等值 GET 体长）。
//
// 反证（2026-09-26 建锁时实跑，见文件末记录）：把路由摘掉只留 "/" 兜底 ⇒ 锁①②全红
// （这正是线上实测的 2,591 B 壳）；把魔数判据去掉 ⇒ 锁④第三条红。
// 运行：go test ./internal/api/ -run TestManualPDF
// ============================================================================
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manualUILangs 12 语种码（与 normalizeMailLang 的白名单同源口径，URL 用连字符写法）。
var manualUILangs = []string{"zh", "zh-hant", "en", "ru", "fr", "ar", "es", "pt", "de", "ja", "ko", "th"}

// manualPDFFloor 体积下限（字节）：真手册是十几页 PDF，测试假件也按这个量级铺，
// 目的是把「2 KB 的 SPA 兜底壳当手册发出去」这类形态直接判死（实测旧缺陷就是 2,591 B）。
const manualPDFFloor = 4096

// manualPDFBody 造一份「够大且带魔数」的假 PDF，并把语种码埋成可唯一检索的标记
// （标记带方括号定界，避免 "de" 这种两字母码在正文里被别的单词顺带命中的假绿）。
func manualPDFBody(code string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n% LC-MANUAL[" + code + "]\n")
	for b.Len() < manualPDFFloor+128 {
		b.WriteString("% filler filler filler filler filler filler filler filler\n")
	}
	b.WriteString("%%EOF\n")
	return b.Bytes()
}

// manualMark 某语种件的可检索标记（入参用 URL 侧写法：zh-hant 等连字符码）。
func manualMark(code string) string { return "LC-MANUAL[" + code + "]" }

// manualEnv 铺一个手册目录（12 语种齐件）并配好 manual_pdf_dir。
// 钉死两条会影响回落链的环境：MANUAL_PDF_PATH 置空（避免旧单文件链被宿主环境污染）、
// 并确认包目录里没有遗留 manual.pdf（否则「未铺库」那条负向锁会被它顶绿）。
func manualEnv(t *testing.T, withDir bool) *Server {
	t.Helper()
	s := f41StoreServer(t)
	t.Setenv("MANUAL_PDF_PATH", "")
	if _, err := os.Stat("manual.pdf"); err == nil {
		t.Fatal("包目录存在 manual.pdf，会让「未铺库」负向锁失效，请先清掉")
	}
	if err := s.Store.SetConfig("manual_pdf_path", ""); err != nil {
		t.Fatalf("清 manual_pdf_path 失败: %v", err)
	}
	if !withDir {
		return s
	}
	dir := t.TempDir()
	for _, code := range manualUILangs {
		name := strings.ReplaceAll(code, "-", "_") + ".pdf" // 齐件统一铺下划线名（zh_hant.pdf）
		if err := os.WriteFile(filepath.Join(dir, name), manualPDFBody(code), 0o644); err != nil {
			t.Fatalf("写假 PDF %s 失败: %v", name, err)
		}
	}
	if err := s.Store.SetConfig("manual_pdf_dir", dir); err != nil {
		t.Fatalf("SetConfig manual_pdf_dir 失败: %v", err)
	}
	return s
}

// manualMux 把 PDF 路由与 SPA 兜底桩挂在同一个 mux 上（锁②：前缀更具体者必须赢）。
// 兜底桩刻意模仿线上形态：200 + 整页 index.html（含 index- 资源引用）。
func manualMux(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/docs/manual/", s.handleManualPDFDownload)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!DOCTYPE html><html><head><script src=\"/assets/index-BOIA4YDV.js\"></script>"))
	})
	return mux
}

// assertManualPDF 托管物四件套：状态 200 + 非 HTML + %PDF 魔数 + 体积下限。
// 参数 body 为响应体、ct 为 Content-Type，wantCode 是这份件里埋的语种标记。
func assertManualPDF(t *testing.T, path string, code int, header http.Header, body []byte, wantMark string) {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("%s 期望 200，实际 %d（体：%s）", path, code, truncateForMsg(body))
	}
	if ct := header.Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("%s Content-Type 期望 application/pdf，实际 %q", path, ct)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("%s ★ 体首不是 %%PDF- 魔数（前 32 字节：%q）", path, string(body[:min(32, len(body))]))
	}
	if len(body) < manualPDFFloor {
		t.Fatalf("%s 体积 %d B 低于下限 %d B（疑似兜底壳冒充手册）", path, len(body), manualPDFFloor)
	}
	// 负向：SPA 兜底壳的两条特征字面必须 0 命中（F-69 实测就是栽在这里）
	for _, bad := range []string{"<!DOCTYPE", "index-BOIA4YDV", "window.__BRANDING__"} {
		if bytes.Contains(body, []byte(bad)) {
			t.Fatalf("%s 响应体含 SPA 兜底壳标记 %q：托管物被前端路由吃掉了", path, bad)
		}
	}
	if wantMark != "" && !bytes.Contains(body, []byte(wantMark)) {
		t.Fatalf("%s 应取到标记为 %q 的那份手册，实际体首 %q", path, wantMark, string(body[:min(48, len(body))]))
	}
}

func truncateForMsg(b []byte) string {
	s := string(b)
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// TestManualPDFDownloadTwelveLangs 锁①②：12 语种逐码走完整 mux，每码都必须是真 PDF。
func TestManualPDFDownloadTwelveLangs(t *testing.T) {
	s := manualEnv(t, true)
	h := manualMux(s)
	for _, code := range manualUILangs {
		path := "/docs/manual/" + code + ".pdf"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		res := rec.Result()
		body := rec.Body.Bytes()
		// wantMark：这份件写盘时埋的语种串（zh-hant 的件埋的是 "zh-hant"）
		assertManualPDF(t, path, res.StatusCode, res.Header, body, manualMark(code))
	}
	// 大写与带空格的语种码也应归一命中（与 normalizeMailLang 同一判据，不另立规矩）
	for _, raw := range []string{"JA", "DE", "Zh-Hant"} {
		path := "/docs/manual/" + raw + ".pdf"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assertManualPDF(t, path, rec.Result().StatusCode, rec.Result().Header, rec.Body.Bytes(), manualMark(strings.ToLower(raw)))
	}
	// 连字符 URL 命中下划线文件名（交付包命名口径），且文件名如实回显命中的那份
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/manual/zh-hant.pdf", nil))
	if cd := rec.Result().Header.Get("Content-Disposition"); !strings.Contains(cd, "zh_hant.pdf") {
		t.Fatalf("zh-hant 应命中下划线名 zh_hant.pdf，实际 Content-Disposition=%q", cd)
	}
}

// TestManualPDFHitsPDFNotSPA 锁②：路由必须**排在 SPA 兜底之前**（同一 mux 内前缀更具体者赢）。
// 反证：把 /docs/manual/ 这条注册删掉，本用例即红（请求落到 "/" 兜底、回 200 整页 HTML）。
func TestManualPDFHitsPDFNotSPA(t *testing.T) {
	s := manualEnv(t, true)
	h := manualMux(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/manual/ru.pdf", nil))
	assertManualPDF(t, "/docs/manual/ru.pdf", rec.Result().StatusCode, rec.Result().Header, rec.Body.Bytes(), manualMark("ru"))
	// 对照面：同一条 mux 上的未知路径仍回兜底壳（证明判据能区分两者，而不是恒真）
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/some-spa-route", nil))
	if !bytes.Contains(rec2.Body.Bytes(), []byte("<!DOCTYPE")) {
		t.Fatal("对照组失效：SPA 兜底桩没回 HTML，说明本用例的负向判据是恒真的假锁")
	}
}

// TestManualPDFFallbackDisclosesHitLang 锁③：无专稿语种回落 en 时 200，但文件名等值 en.pdf。
func TestManualPDFFallbackDisclosesHitLang(t *testing.T) {
	s := f41StoreServer(t)
	t.Setenv("MANUAL_PDF_PATH", "")
	if err := s.Store.SetConfig("manual_pdf_path", ""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// 只铺 zh + en 两份（模拟「先出中英、其余语种待补」的过渡期）
	for _, c := range []string{"zh", "en"} {
		if err := os.WriteFile(filepath.Join(dir, c+".pdf"), manualPDFBody(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.SetConfig("manual_pdf_dir", dir); err != nil {
		t.Fatal(err)
	}
	h := manualMux(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/manual/th.pdf", nil))
	assertManualPDF(t, "/docs/manual/th.pdf", rec.Result().StatusCode, rec.Result().Header, rec.Body.Bytes(), manualMark("en"))
	cd := rec.Result().Header.Get("Content-Disposition")
	// 等值锁：回落链走了 en.pdf，文件名就必须写 en.pdf（写 th.pdf 是「内容说谎」）
	if !strings.Contains(cd, `filename="en.pdf"`) {
		t.Fatalf("回落命中应如实回显 en.pdf，实际 %q", cd)
	}
	if strings.Contains(cd, "th.pdf") {
		t.Fatalf("Content-Disposition 不得冒充请求语种：实际 %q", cd)
	}
}

// assertManualErrJSON 负向路径统一判据：状态码 + 错误码 + **必须是 JSON 错误体不是 HTML 壳**。
func assertManualErrJSON(t *testing.T, code int, body []byte, wantStatus int, wantErrCode string) {
	t.Helper()
	if code != wantStatus {
		t.Fatalf("期望 %d，实际 %d（体：%s）", wantStatus, code, truncateForMsg(body))
	}
	if bytes.Contains(body, []byte("<!DOCTYPE")) || bytes.Contains(body, []byte("index-BOIA4YDV")) {
		t.Fatalf("错误路径回出了 SPA 兜底壳（F-69 病根）：%s", truncateForMsg(body))
	}
	var v struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("错误体不是 JSON（%v）：%s", err, truncateForMsg(body))
	}
	if v.Success {
		t.Fatalf("错误路径 success 必须为 false，实际 %s", truncateForMsg(body))
	}
	if v.Code != wantErrCode {
		t.Fatalf("错误码期望 %s，实际 %q（%s）", wantErrCode, v.Code, truncateForMsg(body))
	}
}

// TestManualPDFNegativePaths 锁④：白名单外 400、未铺库 404、坏文件 500、穿越串不落文件系统、目录索引 404。
func TestManualPDFNegativePaths(t *testing.T) {
	// ① 白名单外语种：400 VALIDATION_ERROR（绝不静默回落到中文）
	s := manualEnv(t, true)
	h := manualMux(s)
	for _, bad := range []string{"xx", "zh_hans", "cn", "chinese", "zh.pk", "zh.pdf.pdf"} {
		path := "/docs/manual/" + bad + ".pdf"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assertManualErrJSON(t, rec.Result().StatusCode, rec.Body.Bytes(), http.StatusBadRequest, "VALIDATION_ERROR")
	}
	// ② 目录索引（无文件名）：404 NOT_FOUND，形状提示写在 message 里
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/manual/", nil))
	assertManualErrJSON(t, rec.Result().StatusCode, rec.Body.Bytes(), http.StatusNotFound, "NOT_FOUND")

	// ③ 未铺库（manual_pdf_dir 未配、旧链也没有）：404，且**不得**回 200 HTML
	s2 := manualEnv(t, false)
	rec2 := httptest.NewRecorder()
	manualMux(s2).ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/docs/manual/ja.pdf", nil))
	assertManualErrJSON(t, rec2.Result().StatusCode, rec2.Body.Bytes(), http.StatusNotFound, "NOT_FOUND")

	// ④ 目录里的件不是 PDF（HTML 改名 .pdf）：500 INTERNAL_ERROR，宁红不假绿
	s3 := manualEnv(t, false)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ja.pdf"), []byte("<!DOCTYPE html><html>x</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s3.Store.SetConfig("manual_pdf_dir", dir); err != nil {
		t.Fatal(err)
	}
	rec3 := httptest.NewRecorder()
	manualMux(s3).ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/docs/manual/ja.pdf", nil))
	assertManualErrJSON(t, rec3.Result().StatusCode, rec3.Body.Bytes(), http.StatusInternalServerError, "INTERNAL_ERROR")

	// ⑤ 路径穿越：绕过 mux 直调处理方（ServeMux 自己会先净化一遍路径，这里要证的是**处理方本身**
	//    不吃穿越串）。命中白名单外的码即被拒，任何情况下都不得把目录外的文件发出去。
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.pdf")
	if err := os.WriteFile(secret, manualPDFBody("SECRET-LEAK"), 0o644); err != nil {
		t.Fatal(err)
	}
	s4 := manualEnv(t, true)
	for _, trav := range []string{
		"../../etc/passwd.pdf", "..%2F..%2Fetc%2Fpasswd.pdf", "zh/zh.pdf",
		"../" + filepath.Base(secret), fmt.Sprintf("../../../..%s", secret),
	} {
		r := httptest.NewRequest(http.MethodGet, "/docs/manual/"+trav, nil)
		r.URL.Path = manualPDFPrefix + trav // 强行把原始串塞进 Path，绕开 mux 的路径净化
		rec := httptest.NewRecorder()
		s4.handleManualPDFDownload(rec, r)
		res := rec.Result()
		if bytes.Contains(rec.Body.Bytes(), []byte("SECRET-LEAK")) || bytes.Contains(rec.Body.Bytes(), []byte("root:")) {
			t.Fatalf("★ 路径穿越把目录外的文件发出去了：%s → %s", trav, truncateForMsg(rec.Body.Bytes()))
		}
		if res.StatusCode == http.StatusOK {
			t.Fatalf("穿越串 %s 竟回 200：%s", trav, truncateForMsg(rec.Body.Bytes()))
		}
		if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusNotFound {
			t.Fatalf("穿越串 %s 期望 400/404，实际 %d", trav, res.StatusCode)
		}
	}
}

// TestManualPDFMethodAndHead 锁⑤：POST 405；HEAD 只回头且 Content-Length 等值 GET 体长。
func TestManualPDFMethodAndHead(t *testing.T) {
	s := manualEnv(t, true)
	h := manualMux(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/docs/manual/zh.pdf", nil))
	assertManualErrJSON(t, rec.Result().StatusCode, rec.Body.Bytes(), http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")

	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/docs/manual/zh.pdf", nil))
	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/docs/manual/zh.pdf", nil))
	if head.Result().StatusCode != http.StatusOK {
		t.Fatalf("HEAD 期望 200，实际 %d", head.Result().StatusCode)
	}
	if head.Result().Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("HEAD 的 Content-Type 期望 application/pdf，实际 %q", head.Result().Header.Get("Content-Type"))
	}
	// 等值锁：HEAD 报的体长必须等于 GET 的真实体长（冒烟脚本靠它判体积下限）
	if got, want := head.Result().ContentLength, int64(len(get.Body.Bytes())); got != want {
		t.Fatalf("HEAD Content-Length 期望 %d，实际 %d", want, got)
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD 不得带响应体，实际 %d B", head.Body.Len())
	}
}

// TestSanitizeManualFilename 响应头净化单测：引号/换行/非 ASCII 都不得进 Content-Disposition
// （老单文件链的文件名来自运维配置，不可控）。
func TestSanitizeManualFilename(t *testing.T) {
	cases := map[string]string{
		"ja.pdf":          "ja.pdf",
		"zh_hant.pdf":     "zh_hant.pdf",
		"manual.pdf":      "manual.pdf",
		`evil".pdf`:       "evil-.pdf",           // 引号会提前终结 filename 参数
		"a\nb.pdf":        "a-b.pdf",             // 换行可用于响应头注入
		"用户手册.pdf":        "pdf.pdf",             // 纯非 ASCII 名折成占位（真名走 filename*）
		"":                "manual.pdf",          // 空名回落
		"..":              "manual.pdf",          // 点串不得成为文件名
		"LangCross-Guide": "LangCross-Guide.pdf", // 缺后缀补上
		"/etc/passwd.pdf": "etc-passwd.pdf",      // filepath.Base 之后仍可能带斜杠的兜底
	}
	for in, want := range cases {
		if got := sanitizeManualFilename(in); got != want {
			t.Fatalf("sanitizeManualFilename(%q) 期望 %q，实际 %q", in, want, got)
		}
	}
	// 等值锁：任何输入的结果里都不得出现引号、换行、斜杠
	for in := range cases {
		got := sanitizeManualFilename(in)
		if strings.ContainsAny(got, "\"\n\r\\/") {
			t.Fatalf("净化后的文件名仍含危险字符：%q → %q", in, got)
		}
	}
}

// TestManualPDFRegisteredOnRealRoutes 锁⑥（真接线）：**生产 routes() 注册的 mux** 必须把
// /docs/manual/zh.pdf 送到 PDF 处理方，而不是 spa.go 的 "/" 兜底。
// 为什么单独立一条：前面几条用的是测试自建的 mux（把 PDF 路由与兜底桩挂一起），
// 那条「注册点被删」的事故只有走真 routes() 才抓得住——F-69 的成因正是「没人注册」。
func TestManualPDFRegisteredOnRealRoutes(t *testing.T) {
	s := manualEnv(t, true)
	s.mux = newRouteMux()
	s.routes()
	hits := map[string]int{"pdf": 0, "spa": 0}
	// 探针：包一层计数中间件，按响应形态归类（application/pdf vs 整页 HTML 壳）
	probe := func(path, wantMark string) {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		res := rec.Result()
		switch {
		case res.StatusCode == http.StatusOK && res.Header.Get("Content-Type") == "application/pdf" &&
			bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")):
			hits["pdf"]++
			assertManualPDF(t, path, res.StatusCode, res.Header, rec.Body.Bytes(), wantMark)
		default:
			hits["spa"]++
			t.Logf("探针 %s 未落在 PDF 处理方：status=%d ct=%q 前 80B=%q", path, res.StatusCode,
				res.Header.Get("Content-Type"), truncateForMsg(rec.Body.Bytes()))
		}
	}
	probe("/docs/manual/zh.pdf", manualMark("zh"))
	probe("/docs/manual/en.pdf", manualMark("en"))
	probe("/docs/manual/ja.pdf", manualMark("ja"))
	probe("/docs/manual/xx.pdf", "") // 白名单外：真 mux 里也必须是 400 JSON，不得漏给兜底
	probe("/docs/manual/", "")       // 目录索引：404 JSON
	if hits["pdf"] != 3 || hits["spa"] != 2 {
		t.Fatalf("真 routes() 接线判据不符：PDF 命中期望 3、非 PDF（400/404）期望 2，实际 %+v", hits)
	}
}

// ============================================================================
// 反证实跑记录（2026-09-26 建锁时逐条改坏再复跑，改完即还原）：
//   · 删掉 server.go 的 `s.mux.HandleFunc("/docs/manual/", …)` 注册 ⇒ 锁⑥红
//     （三条手册探针全落 "/" 兜底、PDF 命中数 0）——这条即线上 2,591 B 壳的复现面；
//   · 只留 "/" 兜底、把测试自建 mux 里的注册删掉 ⇒ 锁①②全红（同上形态）；
//   · 删掉 %PDF 魔数判据 ⇒ 锁④「坏文件 500」红（HTML 改名 .pdf 被当手册发出）；
//   · 把 normalizeMailLang 的空码分支改成「回落 zh」⇒ 锁④白名单外 400 红（静默给中文）；
//   · 把 Content-Disposition 改成请求语种名（th.pdf）⇒ 锁③等值红；
//   · 去掉 sanitizeManualFilename 的引号替换 ⇒ 锁净化的「无引号/换行/斜杠」等值红。
// ============================================================================
