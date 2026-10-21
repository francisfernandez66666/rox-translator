// ============ 本文件职责中文说明 ============
// 开放 API 文档在线维护单元测试：Markdown 渲染（GFM 表格/代码块/原始 HTML 转义）、
// 内容回退链（config 空 → 内置默认）。
// =============================================
package api

import (
	"regexp"
	"strings"
	"testing"
)

// TestRenderDocsEscapesRawHTML 原始 HTML 必须不得进入输出（goldmark 安全模式：
// 非 Unsafe 时原始 HTML 块被丢弃/转义），杜绝公开页脚本注入。
func TestRenderDocsEscapesRawHTML(t *testing.T) {
	html := renderDocsHTML("# 标题\n\n<script>alert(1)</script>\n\n正文")
	if strings.Contains(html, "<script>") {
		t.Fatalf("原始 <script> 出现在输出中，存在注入风险")
	}
	if !strings.Contains(html, "正文") {
		t.Fatalf("相邻正常内容不应受影响")
	}
}

// TestRenderDocsGFMTable GFM 表格扩展渲染为 HTML 表格（API 文档端点清单刚需）。
func TestRenderDocsGFMTable(t *testing.T) {
	md := "| 方法 | 路径 |\n|------|------|\n| POST | /openapi/v1/tasks |"
	html := renderDocsHTML(md)
	if !strings.Contains(html, "<table>") || !strings.Contains(html, "/openapi/v1/tasks") {
		t.Fatalf("GFM 表格未正确渲染")
	}
}

// TestRenderDocsHeadingAndCode 标题与围栏代码块基础渲染。
func TestRenderDocsHeadingAndCode(t *testing.T) {
	html := renderDocsHTML("# API\n\n~~~\ncurl -X POST\n~~~")
	if !strings.Contains(html, "<h1>") || !strings.Contains(html, "<pre>") {
		t.Fatalf("标题/代码块渲染异常")
	}
}

// TestGetDocsMDFallback 双语回退链：对应语言 config 为空时回退该语言内置默认；
// 保存后读取自定义值；互不影响。
func TestGetDocsMDFallback(t *testing.T) {
	s := newCaptchaTestServer(t)
	if got := s.getDocsMD("zh"); got != defaultDocsMDZh {
		t.Fatalf("zh 空配置应回退内置默认")
	}
	if got := s.getDocsMD("en"); got != defaultDocsMDEn {
		t.Fatalf("en 空配置应回退内置默认")
	}
	if err := s.Store.SetConfig(openAPIDocsConfigKeyZh, "# 自定义中文"); err != nil {
		t.Fatal(err)
	}
	if got := s.getDocsMD("zh"); got != "# 自定义中文" {
		t.Fatalf("应读到自定义中文，实得 %q", got)
	}
	if got := s.getDocsMD("en"); got != defaultDocsMDEn {
		t.Fatalf("en 不应受影响")
	}
	_ = s.Store.SetConfig(openAPIDocsConfigKeyZh, "")
	if got := s.getDocsMD("zh"); got != defaultDocsMDZh {
		t.Fatalf("清空后应再次回退内置默认")
	}
}

// TestDefaultDocsRotateCountResetNote F-70 撤案尾账（批 I-8 补断言，2026-09-27）：
// 轮换＝删旧建新，「当日调用计数随之归零」是设计行为，但必须写进对外文档与前端确认文案，
// 否则客户会把计数清零当成被盗用/异常。本锁钉内置默认文档（中英两份）的接口一览行，
// 谁把这句抹掉即红灯——文档可被管理台在线覆写，锁只保证**出厂默认**在场。
func TestDefaultDocsRotateCountResetNote(t *testing.T) {
	if !strings.Contains(defaultDocsMDZh, "当日调用计数随之归零") {
		t.Fatal(`中文默认文档缺「当日调用计数随之归零」（F-70 对外文案账）`)
	}
	if !strings.Contains(defaultDocsMDEn, "daily call count resets to zero") {
		t.Fatal(`英文默认文档缺 "daily call count resets to zero"（F-70 对外文案账）`)
	}
}

// docBareFileSample 判定默认文档里有没有「裸 @文件名」的 curl 示例。
// 返回第一个违规片段（空串＝全部合规）。
//
// ★ 2026-10-21 UAT 收口 M4：形如 `files=@manual.docx` 的片段会被 Cloudflare 的
// 邮箱混淆当成邮箱地址改写（实测三域响应体各带不同的 data-cfemail，第 536/751/757 行
// 字节互不相等），而这三域本来承诺逐字节等值。判据刻意只认「@ 后面不是 / 开头」：
// 示例必须写成 `files=@/path/manual.docx` 这种绝对路径形态——它既不会被邮箱正则命中，
// 也正是客户真实用法。写死路径名而不是"文件里有没有 @"，是因为 `--data @-` 属合法用法。
func docBareFileSample(md string) string {
	re := regexp.MustCompile(`files=@([^\s'"]+)`)
	for _, m := range re.FindAllStringSubmatch(md, -1) {
		if !strings.HasPrefix(m[1], "/") {
			return m[0]
		}
	}
	return ""
}

// TestDefaultDocsFileSamplesUseAbsolutePaths 中英两份内置文档的 files=@ 示例一律走绝对路径。
// 反证：把任一处改回 `files=@manual.docx` 必须让 docBareFileSample 返回非空（见下方用例子）。
func TestDefaultDocsFileSamplesUseAbsolutePaths(t *testing.T) {
	for name, md := range map[string]string{
		"zh": defaultDocsMDZh,
		"en": defaultDocsMDEn,
	} {
		if bad := docBareFileSample(md); bad != "" {
			t.Fatalf("%s 默认文档出现裸「%s」：CDN 邮箱混淆会把它当邮箱改写，三域字节等值判据随之失效（M4）", name, bad)
		}
	}
	// 射程自证：这份锁必须真能抓到违规形态（否则上面两条恒绿＝空转）。
	if docBareFileSample("curl -F 'files=@manual.docx'") == "" {
		t.Fatal("判据自身失明：喂裸 @文件名 竟判合规")
	}
}

// TestOpenAPIDocsPageDisablesEmailObfuscation ★ M4（2026-10-21 UAT 收口）：
// 对外文档整页正文必须包在 <!--email_off--> 里，否则 Cloudflare 会把示例里的
// `files=@xxx` 当邮箱改写成 data-cfemail（客户复制到的 cURL 直接不可用），
// 且三域响应体字节不再等值。内置默认改成 /path/ 只覆盖默认，管理台旧抄本仍靠这道标记兜住。
func TestOpenAPIDocsPageDisablesEmailObfuscation(t *testing.T) {
	page := openAPIDocsPage("中文正文", "english body")
	const start, end = "<!--email_off-->", "<!--/email_off-->"
	if strings.Count(page, start) != 1 || strings.Count(page, end) != 1 {
		t.Fatalf("email_off 标记必须恰好成对出现，实得 start=%d end=%d", strings.Count(page, start), strings.Count(page, end))
	}
	i, j := strings.Index(page, start), strings.Index(page, end)
	zh, en := strings.Index(page, "中文正文"), strings.Index(page, "english body")
	if !(i < zh && zh < j && i < en && en < j) {
		t.Fatalf("中英正文都必须落在 email_off 区间内：start=%d zh=%d en=%d end=%d", i, zh, en, j)
	}
}
