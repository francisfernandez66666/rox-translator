// ============ public_ui_test.go · 职责说明 ============
// api 包测试文件。
// =============================================

// ============ 本文件职责中文说明 ============
// 后端直出 HTML 页面的视觉真值闸门（/docs/terms|sla|privacy、/openapi/docs、/office/taskpane.html）。
//
// 为什么要这条锁：这些页由后端直出内嵌 HTML，不在前端构建产物里，
// 所以前端那套 UI 真值闸门（readability.test.ts / pixel_uat.spec.ts）扫不到它们。
// 2026-09-22 全站按 UI 还原时正是这个盲区让「TDesign 蓝靛浅底主题」活了很久：
// 浅色底 + #2b3ee8 主色与交付的 X/Grok 单色纯黑体系完全相反。
// 逐个点名之外还有一条全量扫描（TestAllServedHtmlPagesMonochrome），
// 因为这个盲区已被发现三次（/docs/*、/openapi/docs 的 Google 蓝、office 任务窗格的浅底蓝）。
// 本文件把「不得复活旧主题」与「必须等于 §3.1 真值」两侧都钉住。
//
// 口径来源：《前端及UI相关/UI-ANNOTATIONS.md》§1.1（色令牌）、§1.3（描边）、
// §3.1-05（公开页骨架：导航品牌 Bold/#FFFFFF、面板 #121417、页脚面 #050607 文字 #536471）。
// ★ 〇-N（2026-09-23 用户后令「字号变大、线框变粗、不改颜色」）：描边档由交付原值 1.2px 抬到 2px，
// 字阶整体 +2px（品牌 15→17、导航项 12→14），颜色与字重仍按 §1.1/§3.1 字面值。
// ★ 〇-O（2026-09-23 用户再后令「框线纯白 + 背景主色黑 + 深灰分层」）：描边档整体翻白 #FFFFFF，
// 面改三级台阶（#000 底 / #0A0B0D 内嵌 / #121417 面板 / #1A1D21 浮面）；文字色逐字未动。
// ========================================
package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// legacyLightThemeHexes 历史浅底蓝靛主题的自造色值，任何一个复活都属跑偏。
var legacyLightThemeHexes = []string{
	"#2b3ee8", "#4a5cf0", "#1c2bd0", "#e7ebff", "#1a2233", "#5a6478", "#e3e6ef", "#f4f6fa", "#2b3145",
}

// TestPublicDocPageMonochromeTruth 三篇公开文档页必须按 §1.1/§3.1-05 单色真值直出。
func TestPublicDocPageMonochromeTruth(t *testing.T) {
	pages := map[string]string{
		"terms":   publicDocPage("用户协议 / User Agreement", "<p>正文</p>", "terms"),
		"sla":     publicDocPageLang("服务等级协议 / SLA", "<p>中</p>", "<p>en</p>", "sla"),
		"privacy": publicDocPage("隐私协议 / Privacy Policy", "<p>正文</p>", "privacy"),
	}
	for name, html := range pages {
		t.Run(name, func(t *testing.T) {
			// ① 旧主题清零：蓝靛主色与浅底一档都不许再出现在直出 HTML 里
			for _, hex := range legacyLightThemeHexes {
				if strings.Contains(html, hex) {
					t.Errorf("旧蓝靛浅底主题色 %s 复活（2026-09-22 已废止，见 UI-ANNOTATIONS §1.1）", hex)
				}
			}
			// ② 面/文字/描边三族令牌逐字相等（缺一个就说明有人改写了令牌声明）
			//    ★ 〇-O（2026-09-23 用户后令「框线全部纯白 + 背景黑 + 深灰分层」）：面走三级台阶、
			//    描边三档全部 #FFFFFF。前端等价锁见 readability.test.ts 的 〇-O 段。
			for _, want := range []string{
				"--lc-bg:#000000", "--lc-panel:#121417", "--lc-surface:#1A1D21", "--lc-foot:#050607",
				"--lc-text:#E7E9EA", "--lc-text-2:#9AA0AA", "--lc-text-4:#536471",
				"--lc-line:#FFFFFF", "--lc-card-line:#FFFFFF", "--lc-white:#FFFFFF",
			} {
				if !strings.Contains(html, want) {
					t.Errorf("缺少 §1.1 真值令牌声明 %s", want)
				}
			}
			// ②b 旧灰描边档与旧面档**按声明整体**负向清零（只比对 `--x:#hex` 这种成对写法，
			//     不去 substring 扫十六进制——源码里的「旧档作废」说明注释同样会被扫到，那是假红）。
			for _, banned := range []string{
				"--lc-line:#464C58", "--lc-pill:#424956", "--lc-card-line:#3A404C",
				"--lc-panel:#0E1014", "--lc-surface:#16181C",
			} {
				if strings.Contains(html, banned) {
					t.Errorf("〇-O 已作废的旧档 %s 复活（框线应纯白、面应走三级台阶）", banned)
				}
			}
			// ③ 主按钮=纯白底黑字（交付真值 .lc-btn--primary），不许回落到文字档灰 #E7E9EA
			if !strings.Contains(html, ".header .btn{background:var(--lc-white);color:#000000") {
				t.Error("管理后台主按钮未按 §3.1-05 走白底黑字")
			}
			if strings.Contains(html, "background:var(--lc-text)") || strings.Contains(html, "background:#E7E9EA") {
				t.Error("实心白件用了文字档灰 #E7E9EA 做底（会显脏偏蓝），应取 --lc-white")
			}
			// ④ 描边框统一 2px（★ 〇-N 2026-09-23 用户后令「线框加粗」，交付原档 §1.3 是 1.2px），
			//    并负向清掉旧的 1.2px / 1px 细档——同一批字号 +2px 也覆盖 §3.1-05 的 15/12 字阶，
			//    真值表以 UI-ANNOTATIONS 的「〇-N 后档」为准（前端侧等价锁见 readability.test.ts I 段）。
			if !strings.Contains(html, ".card{background:var(--lc-panel)") ||
				!strings.Contains(html, "border:2px solid var(--lc-card-line)") {
				t.Error("内容面板未按 〇-O 后档走 #121417 + 2px 纯白")
			}
			if strings.Contains(html, "1.2px") || strings.Contains(html, "border:1px ") {
				t.Error("直出页仍有 〇-N 前的细描边（1.2px / 1px），抬档未覆盖本渲染面")
			}
			// ⑤ 导航四项齐全且当前页点亮 .on（活跃 #FFFFFF，其余 #9AA0AA）
			for _, label := range []string{"定价 Pricing", "用户协议 Terms", "SLA", "隐私协议 Privacy"} {
				if !strings.Contains(html, label) {
					t.Errorf("导航缺项 %s", label)
				}
			}
		})
	}
}

// TestPublicDocPageActiveNav 当前页导航项必须挂 .on，其余不挂（点亮口径逐页正确）。
func TestPublicDocPageActiveNav(t *testing.T) {
	terms := publicDocPage("用户协议", "<p>x</p>", "terms")
	if !strings.Contains(terms, `<a class="on" href="/docs/terms">`) {
		t.Error("/docs/terms 页未点亮「用户协议」导航项")
	}
	if strings.Contains(terms, `<a class="on" href="/docs/sla">`) {
		t.Error("/docs/terms 页错误点亮了「SLA」导航项")
	}
	sla := publicDocPageLang("SLA", "中", "en", "sla")
	if !strings.Contains(sla, `<a class="on" href="/docs/sla">`) {
		t.Error("/docs/sla 页未点亮「SLA」导航项")
	}
	// active 传空（或未知 key）时不得 panic，也不得误点亮
	none := publicDocPage("标题", "<p>x</p>", "")
	if strings.Contains(none, `class="on"`) {
		t.Error("active 为空时不应有任何导航项被点亮")
	}
}

// ---------- 其余两处后端直出页（/openapi/docs、/office/taskpane.html）----------

// TestOpenAPIDocsMonochromeTruth 开放 API 文档页（Markdown 渲染壳）必须走 §1.1 纯黑令牌。
func TestOpenAPIDocsMonochromeTruth(t *testing.T) {
	page := renderDocsHTML("# 标题\n\n正文 `code` 与链接 [x](/y)")
	if !strings.Contains(page, "<h1") {
		t.Fatal("goldmark 未渲染出正文，闸门本身失效")
	}
	assertMonochromeShell(t, page, "openapi/docs")
	// 语言切换按钮：活跃档＝白底黑字实心件，次档＝深底描边（不得回蓝底白字）
	if !strings.Contains(page, ".lang-btn.on{background:var(--lc-white);color:#000000") {
		t.Error("语言切换活跃档未按交付真值走白底黑字")
	}
	// 链接不得取蓝色（单色体系里链接＝主文字 + hover 下划线）
	if !strings.Contains(page, "a{color:var(--lc-text);text-decoration:none}") {
		t.Error("链接色不是 §1.1 主文字档")
	}
}

// TestOfficeTaskPaneMonochromeTruth Word 任务窗格页必须走 §1.1 纯黑令牌、无 Google 蓝。
func TestOfficeTaskPaneMonochromeTruth(t *testing.T) {
	assertMonochromeShell(t, officeTaskPaneHTML, "office/taskpane")
	// 主按钮白底黑字、次按钮深底描边（.sec 不得再是 #e8f0fe 浅蓝底）
	if !strings.Contains(officeTaskPaneHTML, "button{width:100%;padding:8px;border:none;border-radius:8px;background:var(--lc-white);color:#000000") {
		t.Error("任务窗格主按钮未按交付真值走白底黑字")
	}
	if !strings.Contains(officeTaskPaneHTML, "button.sec{background:var(--lc-panel)") {
		t.Error("次按钮未走深底描边档")
	}
}

// assertMonochromeShell 对一段后端直出 HTML 跑公共的三族令牌 + 旧主题清零断言。
func assertMonochromeShell(t *testing.T, page, name string) {
	t.Helper()
	if len(page) < 200 {
		t.Fatalf("%s 页面内容异常（%d 字节），闸门本身失效", name, len(page))
	}
	code := stripCodeComments(page)
	for _, hex := range legacyLightThemeHexes {
		if strings.Contains(code, hex) {
			t.Errorf("%s 旧主题色 %s 复活（全站浅底/蓝靛主题 2026-09-22 已废止）", name, hex)
		}
	}
	for _, want := range []string{"--lc-bg:#000000", "--lc-text:#E7E9EA", "--lc-white:#FFFFFF"} {
		if !strings.Contains(code, want) {
			t.Errorf("%s 缺少 §1.1 令牌声明 %s", name, want)
		}
	}
	// 文字档灰 #E7E9EA 只用于文字与活跃指示，任何实心件取它做底都会「显脏偏蓝」
	if strings.Contains(code, "background:var(--lc-text)") || strings.Contains(code, "background:#E7E9EA") {
		t.Errorf("%s 有实心件用文字档灰 #E7E9EA 做底，应取 --lc-white", name)
	}
}

// ---------- 全量扫描：任何后端直出 HTML 都不许带旧浅底/蓝绿主题 ----------

// legacyServedHtmlHexes 后端直出页历史上用过的自造色值全集：
// TDesign 蓝靛族（public.go 旧壳）+ Google 蓝 / indigo 族（openapi 与 office 旧壳）+ 浅底族。
// 只要还有一个出现在渲染出的 HTML 里，就说明某处又引入了独立配色而不是取令牌。
var legacyServedHtmlHexes = []string{
	// 旧 public.go：TDesign 蓝靛 + 浅底
	"#2b3ee8", "#4a5cf0", "#1c2bd0", "#e7ebff", "#1a2233", "#5a6478", "#e3e6ef", "#f4f6fa", "#2b3145",
	// 旧 openapi/docs + office 任务窗格：Google 蓝 / indigo / 浅灰底 / 语义绿红
	"#1a73e8", "#1a237e", "#e8f0fe", "#f5f9ff", "#f6f8fa", "#fafbfd", "#e8eaf6", "#c62828", "#2e7d32",
	"#dadce0", "#e0e0e0", "#f0f0f0", "#c6c6c6", "#202124",
}

// TestAllServedHtmlPagesMonochrome 扫源码里所有后端直出 HTML，逐个跑旧主题清零。
//
// 为什么要全量扫而不是逐页点名：这一类页面的共同特征是「不进前端构建产物、前端令牌闸门看不到」，
// 已经第三次在这里发现漏网的蓝底页（/docs/*、/openapi/docs、/office/taskpane）。
// 新增此类页面时不必记得改本测试——只要它含 <!DOCTYPE html 就自动进射程。
func TestAllServedHtmlPagesMonochrome(t *testing.T) {
	type src struct{ path, body string }
	// 覆盖两个渲染源：本包 Go 源码 + assist 内嵌单文件页（go:embed 随二进制发布）
	sources := []src{{"internal/api/public.go", readSrcForUITest(t, "public.go")},
		{"internal/api/admin_openapi.go", readSrcForUITest(t, "admin_openapi.go")},
		{"internal/api/office.go", readSrcForUITest(t, "office.go")},
		{"internal/assist/web/admin.html", string(assistAdminHTMLForUITest(t))}}
	for _, s := range sources {
		if !strings.Contains(strings.ToLower(s.body), "<!doctype html") {
			t.Errorf("%s 不再含 <!DOCTYPE html，请同步本锁", s.path)
			continue
		}
		code := stripCodeComments(s.body)
		for _, hex := range legacyServedHtmlHexes {
			if strings.Contains(code, hex) {
				t.Errorf("%s 出现旧主题色 %s：后端直出页必须取 §1.1 令牌（全站无蓝无绿、浅底已废止）", s.path, hex)
			}
		}
		// ★ 〇-O（2026-09-23）：旧灰描边档与旧面档一律不得复活。这里比的是**成对声明**
		// （`--x:#hex`）而不是裸十六进制——源码注释里大量「旧档 #464C58 作废」的说明文字
		// 会命中裸值，那是假红；剥注释 + 锁声明形态才是有效负向。
		for _, banned := range retiredRamp00O {
			if strings.Contains(code, banned) {
				t.Errorf("%s 复活 〇-O 已作废的旧档 %s（框线应纯白、面应走 #000/#0A0B0D/#121417/#1A1D21 台阶）", s.path, banned)
			}
		}
	}
}

// retiredRamp00O 〇-O（2026-09-23「框线纯白 + 背景黑 + 深灰分层」）作废的旧令牌声明。
// 覆盖两套命名：后端直出页的 --lc-* 与 assist 内嵌页的简名 --line/--panel/…。
var retiredRamp00O = []string{
	"--lc-line:#464C58", "--lc-pill:#424956", "--lc-card-line:#3A404C",
	"--lc-panel:#0E1014", "--lc-surface:#16181C",
	"--line:#464C58", "--pill:#424956", "--card-line:#3A404C",
	"--input-line:#5A6270", "--done:#6E7683",
	"--panel:#0E1014", "--surface:#16181C",
}

// readSrcForUITest 读本包源文件原文（全量扫描用；文件缺失即红灯，防闸门静默失效）。
func readSrcForUITest(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败（改名/删除会让本闸门失效）：%v", name, err)
	}
	return string(b)
}

// assistAdminHTMLForUITest 取 assist 内嵌管理台页面（该页与主站分属不同包，只能引内嵌变量）。
func assistAdminHTMLForUITest(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../assist/web/admin.html")
	if err != nil {
		t.Fatalf("读取 assist/web/admin.html 失败：%v", err)
	}
	return b
}

// goLineCommentRE 剥行注释：`//` 前不是 `:`（放过 https:// 这类协议头）。
var goLineCommentRE = regexp.MustCompile(`(^|[^:])//.*$`)

// stripCodeComments 剥掉 Go/HTML/CSS 注释，只留代码本体。
// 本仓大量「旧值 → 真值」说明注释里就带着 #1a73e8 这类字样，不剥注释负向锁会命中注释自己。
func stripCodeComments(s string) string {
	s = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = goLineCommentRE.ReplaceAllString(ln, "$1")
	}
	return strings.Join(lines, "\n")
}
