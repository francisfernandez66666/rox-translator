// ============================================================================
// api/mail_f17_test.go — F-17 批E（2026-09-25）邮件语种跟随自动化断言
// 钉四组（修复文档「批 E｜F-17」断言清单后端侧三件）：
//
//	①normalizeMailLang/requestMailLang——12 码白名单、连字符归一、载荷优先于 X-App-Lang 头；
//	②getMailTpl 五级解析链——老配置零迁移（无后缀=中文）、验证码「其余回落 en」、
//	  自定义 .en/精确语种键逐级压过、中文系不套任何英文级；
//	③loadManualPDF——t.TempDir 假 PDF 铺目录，锁命中序 {lang}→en→zh 与旧单文件终兜底；
//	④buildManualMailMsg——附件名/正文问候语/主题渲染按语种。
//
// 方言：f41StoreServer 自钉内存 SQLite（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestUATBatchE
// ============================================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUATBatchE_NormalizeMailLang 12 码白名单归一：大小写/连字符/空白收口，白名单外一律空串。
func TestUATBatchE_NormalizeMailLang(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ja", "ja"}, {" EN ", "en"}, {"zh-hant", "zh_hant"}, {"ZH_HANT", "zh_hant"},
		{"", ""}, {"xx", ""}, {"pt-BR", ""}, {"zh-CN", ""}, {"en-GB", ""},
	}
	for _, c := range cases {
		if got := normalizeMailLang(c.in); got != c.want {
			t.Fatalf("normalizeMailLang(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
	// 中文系判据：空/zh/zh_hant 走中文链路，其余（含 en）走外文的回落链
	for _, zh := range []string{"", "zh", "zh_hant"} {
		if !isChineseMailLang(zh) {
			t.Fatalf("isChineseMailLang(%q) 应为 true（app_lang 空留 zh 口径）", zh)
		}
	}
	for _, nonzh := range []string{"en", "ja", "th"} {
		if isChineseMailLang(nonzh) {
			t.Fatalf("isChineseMailLang(%q) 应为 false", nonzh)
		}
	}
}

// TestUATBatchE_RequestMailLang 提取序：载荷 app_lang 优先，X-App-Lang 头兜底，双非法落空。
func TestUATBatchE_RequestMailLang(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/register", nil)
	r.Header.Set("X-App-Lang", "ja")
	if got := requestMailLang(r, "de"); got != "de" {
		t.Fatalf("载荷 app_lang 应压过请求头，实际 %q", got)
	}
	if got := requestMailLang(r, ""); got != "ja" {
		t.Fatalf("载荷为空应回落 X-App-Lang 头，实际 %q", got)
	}
	if got := requestMailLang(r, "xx"); got != "ja" {
		t.Fatalf("载荷非法应回落请求头，实际 %q", got)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/x", nil)
	if got := requestMailLang(r2, "nope"); got != "" {
		t.Fatalf("双非法应落空串（中文链路），实际 %q", got)
	}
}

// setMailTpls 把语种化模板写入 system_config.mail_templates（模拟超管直配）。
func setMailTpls(t *testing.T, s *Server, m map[string]MailTpl) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("序列化模板失败: %v", err)
	}
	if err := s.Store.SetConfig("mail_templates", string(raw)); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}
}

// TestUATBatchE_GetMailTplFallbackChain 五级解析链等值锁（修复文档「其余回落 en + 老配置零迁移」）。
func TestUATBatchE_GetMailTplFallbackChain(t *testing.T) {
	s := f41StoreServer(t)

	// —— 无任何自定义配置（出厂态）——
	// 验证码类：中文/空→中文稿；非中文（含 en）→内置英文稿（「先 zh/en 两份 + 其余回落 en」）
	if got := s.getMailTpl("register_code", ""); !strings.Contains(got.Body, "注册验证码") {
		t.Fatalf("空语种应吃中文注册验证码稿，实际 %q", got.Subject+"/"+got.Body)
	}
	if got := s.getMailTpl("register_code", "zh_hant"); !strings.Contains(got.Body, "注册验证码") {
		t.Fatalf("繁体属中文系，仍吃中文稿，实际 %q", got.Body)
	}
	for _, l := range []string{"en", "th", "de", "ar"} {
		if got := s.getMailTpl("register_code", l); !strings.Contains(got.Body, "verification code") {
			t.Fatalf("语种 %s 应回落内置英文验证码稿，实际 %q", l, got.Body)
		}
	}
	// 正文类（manual 无内置英文稿）：任何语种都吃中文母稿（老配置零迁移口径）
	if got := s.getMailTpl("manual", "th"); !strings.Contains(got.Subject, "产品手册") {
		t.Fatalf("manual 无英文稿应吃中文母稿，实际 %q", got.Subject)
	}

	// —— 超管只配中文母稿（历史形态）：老键零迁移仍生效；验证码类非中文回落内置英文 ——
	setMailTpls(t, s, map[string]MailTpl{
		"register_code": {Subject: "中文定制主题"},
		"manual":        {Subject: "定制手册主题"},
	})
	if got := s.getMailTpl("register_code", "zh").Subject; got != "中文定制主题" {
		t.Fatalf("中文应吃自定义母稿主题，实际 %q", got)
	}
	// ★ 语义锁定：验证码类超管只配中文稿时，非中文语种吃**整套内置英文稿**（主题也被英文稿覆盖）——
	//   「中文定制主题 + 英文正文」的混血邮件就是误翻风险本体，不许出现。
	if got := s.getMailTpl("register_code", "th"); !strings.Contains(got.Body, "verification code") || strings.Contains(got.Subject, "中文") {
		t.Fatalf("th 应整套回落英文稿（主题不得残留中文定制），实际 sub=%q body=%q", got.Subject, got.Body)
	}
	if got := s.getMailTpl("manual", "de").Subject; got != "定制手册主题" {
		t.Fatalf("manual 无英文稿，de 也应吃中文定制母稿，实际 %q", got)
	}

	// —— 语种化配置全开：{code}.en 与 {code}.{lang} 逐级压过 ——
	setMailTpls(t, s, map[string]MailTpl{
		"manual":           {Subject: "ZH base"},
		"manual.en":        {Subject: "EN sub", Body: "EN body"},
		"manual.th":        {Subject: "TH sub"},
		"manual.zh_hant":   {Subject: "HANT sub"},
		"register_code":    {Subject: "RC zh sub"},
		"register_code.en": {Subject: "RC en sub"},
	})
	if got := s.getMailTpl("manual", "").Subject; got != "ZH base" {
		t.Fatalf("空语种=中文链路，期望 ZH base，实际 %q", got)
	}
	if got := s.getMailTpl("manual", "zh_hant").Subject; got != "HANT sub" {
		t.Fatalf("zh_hant 应命中繁体专配键，期望 HANT sub，实际 %q", got)
	}
	if got := s.getMailTpl("manual", "en"); got.Subject != "EN sub" || got.Body != "EN body" {
		t.Fatalf("en 应命中 .en 键两字段，实际 %+v", got)
	}
	if got := s.getMailTpl("manual", "th"); got.Subject != "TH sub" || got.Body != "EN body" {
		t.Fatalf("th：主题吃精确键、正文（.th 未配）回落 .en——等值锁，实际 %+v", got)
	}
	if got := s.getMailTpl("manual", "de").Subject; got != "EN sub" {
		t.Fatalf("de 无精确键应回落 .en，期望 EN sub，实际 %q", got)
	}
	if got := s.getMailTpl("register_code", "th").Subject; got != "RC en sub" {
		t.Fatalf("验证码 th 回落自定义 .en（自定义英文稿压过内置英文稿），期望 RC en sub，实际 %q", got)
	}
}

// TestUATBatchE_LoadManualPDFLangChain t.TempDir 假 PDF 锁命中/回落序（修复文档断言③）。
func TestUATBatchE_LoadManualPDFLangChain(t *testing.T) {
	s := f41StoreServer(t)
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("%PDF-"+name), 0o644); err != nil {
			t.Fatalf("写假 PDF 失败: %v", err)
		}
	}
	write("zh.pdf")
	write("en.pdf")
	write("zh-hant.pdf") // 连字符变体命名（对齐交付包 LangCross-User-Guide-zh-hant.pdf 口径）
	write("ja.pdf")
	if err := s.Store.SetConfig("manual_pdf_dir", dir); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}

	// 精确命中
	d, p, err := s.loadManualPDF("ja")
	if err != nil || filepath.Base(p) != "ja.pdf" || string(d) != "%PDF-ja.pdf" {
		t.Fatalf("ja 应命中 ja.pdf，实际 path=%q err=%v", p, err)
	}
	// 连字符变体文件名命中（码是 zh_hant，盘上是 zh-hant.pdf）
	if _, p, err := s.loadManualPDF("zh_hant"); err != nil || filepath.Base(p) != "zh-hant.pdf" {
		t.Fatalf("zh_hant 应命中连字符变体 zh-hant.pdf，实际 path=%q err=%v", p, err)
	}
	// 回落 en：th 无专稿
	if _, p, err := s.loadManualPDF("th"); err != nil || filepath.Base(p) != "en.pdf" {
		t.Fatalf("th 应回落 en.pdf，实际 path=%q err=%v", p, err)
	}
	// 空语种=中文链路，优先 zh.pdf（不得先吃 en.pdf）
	if _, p, err := s.loadManualPDF(""); err != nil || filepath.Base(p) != "zh.pdf" {
		t.Fatalf("空语种应命中 zh.pdf，实际 path=%q err=%v", p, err)
	}
	// 目录缺文件时回落旧单文件链：manual_pdf_path 顶上
	legacy := filepath.Join(dir, "old-manual.pdf")
	if err := os.WriteFile(legacy, []byte("%PDF-legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir() // 空目录：语种链全落空
	if err := s.Store.SetConfig("manual_pdf_dir", empty); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetConfig("manual_pdf_path", legacy); err != nil {
		t.Fatal(err)
	}
	d, p, err = s.loadManualPDF("de")
	if err != nil || p != legacy || string(d) != "%PDF-legacy" {
		t.Fatalf("目录未命中应回落旧单文件 manual_pdf_path，实际 path=%q err=%v", p, err)
	}
	// 全链落空：报错文案指向新配置键（排障线索）
	if err := s.Store.SetConfig("manual_pdf_dir", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetConfig("manual_pdf_path", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.loadManualPDF("ja"); err == nil || !strings.Contains(err.Error(), "manual_pdf_dir") {
		t.Fatalf("全链未命中应报错并提示 manual_pdf_dir，实际 err=%v", err)
	}
}

// TestUATBatchE_BuildManualMailMsgLang mock 级锁：主题渲染/正文语种/附件名随语种（修复文档断言④前端链路除外）。
func TestUATBatchE_BuildManualMailMsgLang(t *testing.T) {
	s := f41StoreServer(t)
	pdf := []byte("%PDF-fake")
	// 中文链路：主题渲染掉 {brand} 占位（修复历史直挂未渲染主题），附件中文名、正文中文问候
	tpl := s.getMailTpl("manual", "zh")
	msg := buildManualMailMsg("a@b.c", "alice", "zh", tpl, pdf, manualPDFName("zh"))
	if strings.Contains(msg.Subject, "{brand}") {
		t.Fatalf("主题应完成 {brand} 占位渲染，实际 %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "亲爱的 alice") {
		t.Fatalf("zh 正文应为中文问候，实际 %q", msg.Body)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Name != "产品手册.pdf" {
		t.Fatalf("zh 附件名应为 产品手册.pdf，实际 %+v", msg.Attachments)
	}
	// 非中文：英文问候 + 语种化附件名（泰/德两例代表「其余回落 en 正文、附件名精确语种」）
	for lang, wantName := range map[string]string{"th": "คู่มือผู้ใช้.pdf", "de": "Benutzerhandbuch.pdf"} {
		t2 := s.getMailTpl("manual", lang)
		m2 := buildManualMailMsg("x@y.z", "bob", lang, t2, pdf, manualPDFName(lang))
		if !strings.Contains(m2.Body, "Dear bob") {
			t.Fatalf("语种 %s 正文应回落英文问候，实际 %q", lang, m2.Body)
		}
		if m2.Attachments[0].Name != wantName {
			t.Fatalf("语种 %s 附件名应为 %q，实际 %q", lang, wantName, m2.Attachments[0].Name)
		}
	}
	// 繁体：中文系走中文问候（不误翻），但附件名吃繁体专档名
	mh := buildManualMailMsg("t@u.v", "carol", "zh_hant", s.getMailTpl("manual", "zh_hant"), pdf, manualPDFName("zh_hant"))
	if !strings.Contains(mh.Body, "亲爱的 carol") || mh.Attachments[0].Name != "產品手冊.pdf" {
		t.Fatalf("zh_hant 应为中文问候+繁体附件名，实际 body=%q name=%q", mh.Body, mh.Attachments[0].Name)
	}
	// 无 PDF：仅正文邮件，不带空附件
	m3 := buildManualMailMsg("x@y.z", "bob", "en", s.getMailTpl("manual", "en"), nil, manualPDFName("en"))
	if len(m3.Attachments) != 0 {
		t.Fatalf("pdf 为空不得挂空附件，实际 %+v", m3.Attachments)
	}
}
