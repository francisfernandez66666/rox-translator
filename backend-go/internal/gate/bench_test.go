// ============ bench_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-8「全仓零 Benchmark」）：ConstraintGate 8 项硬校验的性能基线。
// 闸门对「每段 × 每语言」都要跑（含汉字/乱码/数字三张正则 + 书写系统字符集扫描），
// 是翻译编排里调用次数最多的纯函数之一；语料变长或新增一项正则都可能静默劣化整条流水线吞吐。
// 三个基准分别钉住：全通过路径、失败路径（含 Detail 拼装）、带 KB 术语要求的第 9 项。
// =============================================
package gate

import (
	"strings"
	"testing"
)

// benchSource 约 400 字混合语料（数字 + 术语 + 占位风格），覆盖各校验项的输入面。
// 用 var + 包级初始化：const 不允许函数调用，而这里需要拼出足够长度的稳定语料。
var benchSource = func() string {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("极石设备的额定功率为 3.5 kW，连续工作 1,200 小时后需复检，合格率达 99.6%。")
		b.WriteString("请按 {qty} 的数量下单，并在 7 个工作日内完成交付。\n")
	}
	return b.String()
}()

// benchTranslation 与源文对应的「应通过」译文（无汉字残留、数字齐全、长度合理）。
var benchTranslation = strings.Repeat(
	"The rated power of the ROX device is 3.5 kW; after 1,200 hours of continuous operation a re-inspection is required, with a pass rate of 99.6%. Place the order in the quantity of {qty} and deliver within 7 working days. ", 10)

// BenchmarkRunPass 全通过路径（闸门绝大多数请求走这条）。
func BenchmarkRunPass(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !Run(benchSource, "en", benchTranslation).Pass {
			b.Fatal("基准译文应通过闸门，否则测的是失败路径")
		}
	}
}

// BenchmarkRunFail 失败路径（源语言残留 + 数字丢失，触发 Detail 拼装与多项不通过）。
func BenchmarkRunFail(b *testing.B) {
	bad := "极石设备的额定功率为 kW，请按时下单。" + strings.Repeat("中文残留内容", 30)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if Run(benchSource, "en", bad).Pass {
			b.Fatal("坏译文不应通过闸门")
		}
	}
}

// BenchmarkRunWithTerms 第 9 项 KB 术语硬闸（命中术语多时随术语表规模线性增长）。
func BenchmarkRunWithTerms(b *testing.B) {
	terms := make([]TermRequirement, 0, 12)
	for i := 0; i < 12; i++ {
		terms = append(terms, TermRequirement{Source: "极石", Target: "ROX"})
		terms = append(terms, TermRequirement{Source: "额定功率", Target: "rated power"})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !RunWithTerms(benchSource, "en", benchTranslation, terms).Pass {
			b.Fatal("术语已体现，应通过")
		}
	}
}

// BenchmarkZhTargetVariant 目标语为 zh/zh_hant 时的分支（跳过源语言残留检查，字符集校验更重）。
// 语料刻意用「逐句不同」的英中句对：闸门第 6 项会识别复读片段，
// 若简单用 strings.Repeat 复制同句会先命中复读检查，基准就跑不到目标语言字符集分支了。
func BenchmarkZhTargetVariant(b *testing.B) {
	src, tr := benchZhCorpus()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !Run(src, "zh", tr).Pass {
			b.Fatal("英译中基准译文应通过闸门")
		}
		if !Run(src, "zh_hant", tr).Pass {
			b.Fatal("繁体分支基准译文应通过闸门")
		}
	}
}

// benchZhPairs 英译中句对（数字在两侧一致，避免踩到「数字保持」项）。
var benchZhPairs = [][2]string{
	{"The rated power of the device is 3.5 kW.", "该设备的额定功率为 3.5 千瓦，出厂前已逐台复核。"},
	{"Continuous operation lasts 1200 hours.", "可连续运行 1200 小时，无需中途停机保养。"},
	{"A pass rate of 99.6 percent was recorded.", "本批次抽样合格率为 99.6%，高于既定质量目标。"},
	{"Please deliver the goods within 7 working days.", "请在 7 个工作日内完成交付，并同步物流单据。"},
	{"Batch 42 requires 8 re-inspections.", "第 42 批次需安排 8 次复检，由质检组签字确认后放行。"},
}

// benchZhCorpus 拼装英译中基准语料（源文 / 译文）。
func benchZhCorpus() (string, string) {
	var s, t []string
	for _, p := range benchZhPairs {
		s = append(s, p[0])
		t = append(t, p[1])
	}
	return strings.Join(s, " "), strings.Join(t, "")
}
