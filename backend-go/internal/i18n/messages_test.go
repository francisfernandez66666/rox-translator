// ============================================================================
// messages_test.go — 提示语翻译三级匹配回归（★ 2026-09-24 〇-S #12）
// 锁定 Msg() 口径：精确 → 最长前缀（≥6 rune，覆盖「前缀 + err」拼接）→ 模式词条
// （%d/%s/%v 按序回填）→ 原样透传；非 en 语境零改动。词条全部取自真实 catalog。
// ============================================================================
package i18n

import (
	"context"
	"strings"
	"testing"
)

func enCtx() context.Context { return WithLang(context.Background(), "en") }

func TestMsgExact(t *testing.T) {
	got := Msg(enCtx(), "验证码错误或已过期")
	if want := exactEN["验证码错误或已过期"]; got != want {
		t.Fatalf("精确词条应命中 catalog 译文 %q，实际 %q", want, got)
	}
}

func TestMsgPrefixConcat(t *testing.T) {
	// "保存失败: " 是带尾空格的前缀键，运行时拼接 err 后走最长前缀
	got := Msg(enCtx(), "保存失败: connection refused")
	if !strings.HasPrefix(got, "Save failed:") || !strings.HasSuffix(got, "connection refused") {
		t.Fatalf("前缀词条应译头留尾，实际 %q", got)
	}
}

func TestMsgPatterns(t *testing.T) {
	cases := []struct{ zh, want string }{
		{"注册过于频繁，请 60 秒后再试", "Registrations too frequent; retry in 60 seconds"},
		{"回调金额不符：期望 100 分，实收 90 分", "Callback amount mismatch: expected 100 fen, received 90 fen"},
		{"反馈意见最多 500 字", "Feedback is limited to 500 characters"},
		{"同步翻译单次上限 2000 字符（当前 3000），长文本请使用 POST /openapi/v1/tasks 异步任务",
			"Sync translation limit is 2000 characters (current 3000); for long text use POST /openapi/v1/tasks async jobs"},
	}
	for _, c := range cases {
		if got := Msg(enCtx(), c.zh); got != c.want {
			t.Fatalf("模式词条 %q 应得 %q，实际 %q", c.zh, c.want, got)
		}
	}
}

func TestMsgVVerbKeepsRawError(t *testing.T) {
	// %v 槽回填的 err 原文含引号/花括号也必须原样进入译文（按序捕获，不做二次格式化）
	got := Msg(enCtx(), `请求格式错误: invalid character '}' looking for beginning of value`)
	if got != `Malformed request: invalid character '}' looking for beginning of value` {
		t.Fatalf("%%v 回填异常，实际 %q", got)
	}
}

func TestMsgZhContextPassthrough(t *testing.T) {
	zh := WithLang(context.Background(), "zh")
	if got := Msg(zh, "验证码错误或已过期"); got != "验证码错误或已过期" {
		t.Fatalf("中文语境必须原样透传，实际 %q", got)
	}
	if got := Msg(context.Background(), "验证码错误或已过期"); got != "验证码错误或已过期" {
		t.Fatalf("未注入语境的内部调用按 zh 透传，实际 %q", got)
	}
}

func TestMsgUnknownPassthrough(t *testing.T) {
	const s = "某条没进词条表的中文提示"
	if got := Msg(enCtx(), s); got != s {
		t.Fatalf("未命中词条应原样透传，实际 %q", got)
	}
	if got := Msg(enCtx(), ""); got != "" {
		t.Fatalf("空串应返回空串，实际 %q", got)
	}
}

// containsHan 判断字符串是否含 CJK 统一表意文字（基本区）。
// 注意不能用 strings.ContainsAny——它是字符集合判定，不是区间判定。
func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// TestCatalogCoverage 结构性自检：词条表不允许空键/空译文；键必须含中文原文。
func TestCatalogCoverage(t *testing.T) {
	for k, v := range exactEN {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			t.Fatalf("exactEN 存在空键/空译文: %q→%q", k, v)
		}
		if !containsHan(k) {
			t.Errorf("exactEN 键应为中文原文: %q", k)
		}
	}
	for _, p := range patternsEN {
		if !containsHan(p.zhFmt) || p.enFmt == "" {
			t.Errorf("patternsEN 词条异常: %q→%q", p.zhFmt, p.enFmt)
		}
	}
	// 词条规模闸门：343 静态 + 1 条无占位模式并入 + 35 条 apierrors.New 补漏，
	// 低于此数说明 catalog 生成回退
	if len(exactEN) < 375 {
		t.Fatalf("exactEN 词条数 %d < 375，catalog_en.go 可能被截断或未重新生成", len(exactEN))
	}
}
