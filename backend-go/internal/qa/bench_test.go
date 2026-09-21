// ============ bench_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-8「全仓零 Benchmark」）：确定性质检规则的性能基线。
// QA 六规则（空译/同文/数字集合/占位符/长度/标点）在每个工单每种语言上都要跑，
// 且数字/占位符规则内含正则——是翻译链路里最容易随语料变长而劣化的一段。
// 这里固定语料与长度，给出可对比的 ns/op 与 allocs 基线；后续如引入更重的规则集，
// 用 `go test -bench=. -benchmem -count=5` 与本文件基线对比即可判定是否引入性能回归。
// （正则本身已在包级预编译，基准反映的是「按语料规模线性增长」的真实成本。）
// =============================================
package qa

import (
	"strings"
	"testing"
)

// benchSource 约 500 汉字的业务段落（含数字、千分位、占位符与 HTML 标签，覆盖全部规则分支）。
// 用 var + 包级初始化：const 不允许函数调用，而这里需要拼出足够长度的稳定语料。
var benchSource = func() string {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		b.WriteString("第 2,048 台设备的标定值为 3.14，请在 {days} 天内完成复核，详见 <a href=\"#\">说明</a>。")
		b.WriteString("本季度产能提升至 85%，返修率控制在 1,200 ppm 以下，客户满意度 96.5 分。\n")
	}
	return b.String()
}()

// BenchmarkCheckOneLang 单语言质检（工单最常见的路径）。
func BenchmarkCheckOneLang(b *testing.B) {
	tr := map[string]string{"en": strings.Repeat("The calibrated value is 3.14 for unit 2,048. ", 40)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Check(benchSource, tr)
	}
}

// BenchmarkCheckMultiLang 6 语种并发批处理后的逐语言质检规模（多语种工单的实际负载）。
func BenchmarkCheckMultiLang(b *testing.B) {
	tr := map[string]string{}
	for _, lc := range []string{"en", "ja", "ko", "de", "fr", "ru"} {
		tr[lc] = strings.Repeat("Der kalibrierte Wert betraegt 3,14 fuer Einheit 2.048. ", 40)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Check(benchSource, tr)
	}
}

// BenchmarkCheckDetectsIssues 错误路径基线（数字丢失 + 占位符丢失）：
// 失败分支会走 diff/集合比对与明细拼装，成本与通过分支不同，需单独有基线。
func BenchmarkCheckDetectsIssues(b *testing.B) {
	tr := map[string]string{"en": "The calibrated value is for unit . Complete within days. <a>说明</a>"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := Check(benchSource, tr)
		if r.Pass {
			b.Fatal("基准语料应检出问题，否则本基准测的是通过分支")
		}
	}
}
