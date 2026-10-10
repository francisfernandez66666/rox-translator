// ============================================================================
// paren_echo_test.go — ★ 14.3 全角括号原词复述剥离的三档判据锁（2026-10-10）。
//
// StripParenEcho 的两个档名（paren_echo_stripped / paren_strip_failed）是对外排障
// 契约（observability.Warn 的 reason 维度、/metrics 的 purity 序列），字面量在这里
// 钉死——改名＝破坏按串检索的排障链路。判据是**整括号逐字复述**而非词级匹配：
// 词级会把 bootGate/API 这类正常圆括号标识符误杀，这是本判据的红线。
// ============================================================================
package engine

import "testing"

// TestStripParenEchoVerbatimStripped 档一：整括号内容逐字出现在原文 ⇒ 剥掉并出
// paren_echo_stripped。
func TestStripParenEchoVerbatimStripped(t *testing.T) {
	out, reason := StripParenEcho("请按下按钮（button）以继续。", "button")
	if reason != "paren_echo_stripped" {
		t.Fatalf("档名字面量契约被改动：实得 %q（对外排障按串检索，不许改名）", reason)
	}
	if out != "请按下按钮以继续。" {
		t.Fatalf("复述括号应被剥掉，实得 %q", out)
	}
}

// TestStripParenEchoKeepsIdentifiers 档二：括号内容不是原文逐字复述（标识符注解、
// 编者注）⇒ 原样保留。这是「不许误杀 bootGate/API 类圆括号」的红线锁。
func TestStripParenEchoKeepsIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name, tr, src string
	}{
		{"英文标识符注解", "调用 bootGate（启动闸门服务）前先校验。", "调用 bootGate 前先校验"},
		{"API 缩写注解", "接口（API）已就绪。", "接口已就绪"},
	} {
		out, reason := StripParenEcho(tc.tr, tc.src)
		if reason != "" {
			t.Fatalf("%s：非逐字复述不许剥（实得 reason=%q out=%q）", tc.name, reason, out)
		}
		if out != tc.tr {
			t.Fatalf("%s：译文不许被改动，实得 %q", tc.name, out)
		}
	}
}

// TestStripParenEchoEmptyKeepsOriginal 档三：整个译文只是原词复述、剥完全空 ⇒
// 宁保留原文也不交空串，出 paren_strip_failed。
func TestStripParenEchoEmptyKeepsOriginal(t *testing.T) {
	const in = "（button）"
	out, reason := StripParenEcho(in, "button")
	if reason != "paren_strip_failed" {
		t.Fatalf("剥空保底的档名字面量被改动：实得 %q", reason)
	}
	if out != in {
		t.Fatalf("剥完全空必须返回原文，实得 %q", out)
	}
}

// TestStripParenEchoDigitGuard 数字护栏：括号里带编号（剥掉会改变数字序列）⇒
// 不剥，防误杀「步骤（1）（2）」类真注释。
func TestStripParenEchoDigitGuard(t *testing.T) {
	const tr = "执行步骤（1）之后重启。"
	const src = "步骤 1 重启"
	out, reason := StripParenEcho(tr, src)
	if reason != "" || out != tr {
		t.Fatalf("带编号括号不许剥：reason=%q out=%q", reason, out)
	}
}
