// ============ 本文件职责中文说明 ============
// 开放 API 文档在线维护单元测试：Markdown 渲染（GFM 表格/代码块/原始 HTML 转义）、
// 内容回退链（config 空 → 内置默认）。
// =============================================
package api

import (
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
