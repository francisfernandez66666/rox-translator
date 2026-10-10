// ============ number_test.go · 职责说明 ============
// 「数字保持」归一化比对的单测（★ 2026-10-10 批次 ⑫ 配套断言）：
//
//	① 千分位两方向（源"1,000"译文"1000"、源"1000"译文"1,000"）必过；
//	② 数量词换算（"1.5 million"→"150万"、"2亿"→"200 million"）必过；
//	③ 中文数字单向认领（"3 steps"→"三步"）必过；
//	④ 真丢数字必死且 Detail 含「缺失: 2026」；
//	⑤ 译文纯丢数字且无中文数字写法必死（含边界：读法不得命中更长中文数字的片段）；
//	⑥ 归一函数边界（空串/多小数点/纯符号/超大数）不 panic。
//
// 纯函数测试，不触 DB，无需钉方言。
// ========================================
package gate

import (
	"strings"
	"testing"
)

// numCheck 从 GateResult 中取「数字保持」单项结果（其余检查项与本断言无关）。
func numCheck(t *testing.T, g *GateResult) Check {
	t.Helper()
	for _, c := range g.Checks {
		if c.Name == CheckNumberKeep {
			return c
		}
	}
	t.Fatalf("GateResult 中没有「数字保持」检查项: %+v", g.Checks)
	return Check{}
}

// TestNumberKeepNormalization 归一化比对的放行侧：合法换算一律不得判死。
func TestNumberKeepNormalization(t *testing.T) {
	cases := []struct {
		name string
		src  string
		tr   string
	}{
		{"千分位_源有译文无", "共 1,000 件商品", "A total of 1000 items."},
		{"千分位_源无译文有", "共 1000 件商品", "A total of 1,000 items."},
		{"千分位多组", "营收 1,500,000 元", "Revenue of 1500000 yuan."},
		{"数量词_英文到中文万", "公司有 1.5 million 用户", "The company has 150万 users."},
		{"数量词_中文亿到英文", "用户达 2亿", "Users reached 200 million."},
		{"数量词_千分位对万", "销售额 1,500,000 美元", "Sales of 1.5 million dollars."},
		{"小数零尾", "重试 3 次，超时 1000.0 秒", "Retry 3 times, timeout 1000 seconds."},
		{"k词缀", "频率 5K 次", "Frequency 5000 times."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := Run(c.src, "en", c.tr)
			ch := numCheck(t, g)
			if !ch.Pass {
				t.Fatalf("合法换算应通过「数字保持」，实得失败 Detail=%q（checks=%v）", ch.Detail, g.Checks)
			}
			if ch.Detail != "" {
				t.Fatalf("通过时 Detail 必须为空串，实得 %q", ch.Detail)
			}
		})
	}
}

// TestNumberKeepChineseNumeralClaim 中文数字单向认领：数字还在、写法变了 → 放行。
func TestNumberKeepChineseNumeralClaim(t *testing.T) {
	cases := []struct {
		name string
		src  string
		tr   string
	}{
		{"个位数", "请执行 3 steps 操作", "请执行三步操作"},
		{"十位组合", "流程分 23 步", "流程分二十三步"},
		{"独立十", "等待 15 分钟", "等待十五分钟"},
		{"含零位", "计划覆盖 2026 年", "计划覆盖二千零二十六年"},
		{"小数点读法", "系数为 1.5", "系数为一点五"},
		{"两变体", "备份保留 200 份", "备份保留两百份"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := numCheck(t, Run(c.src, "en", c.tr))
			if !ch.Pass {
				t.Fatalf("中文数字写法应单向认领放行，实得失败 Detail=%q", ch.Detail)
			}
		})
	}
}

// TestNumberKeepTrulyMissing 真丢数字必死，且 Detail 列出缺失清单。
func TestNumberKeepTrulyMissing(t *testing.T) {
	g := Run("报告覆盖 2026 年计划", "en", "Report covers the plan")
	ch := numCheck(t, g)
	if ch.Pass {
		t.Fatal("译文真丢了 2026，必须判死")
	}
	if !strings.Contains(ch.Detail, "缺失: 2026") {
		t.Fatalf("Detail 应含「缺失: 2026」，实得 %q", ch.Detail)
	}
	if g.Pass {
		t.Fatal("数字保持失败必须拉低整体 Pass")
	}
	// 多个缺失数字按源文序列出
	g2 := Run("需要 1.5 吨与 2026 份", "en", "Need tons and copies")
	ch2 := numCheck(t, g2)
	if ch2.Pass || !strings.Contains(ch2.Detail, "缺失: 1.5, 2026") {
		t.Fatalf("多缺失应列全清单，实得 pass=%v Detail=%q", ch2.Pass, ch2.Detail)
	}
}

// TestNumberKeepNoChineseNumeralEscape 数字没有就是没有：
// 译文既无阿拉伯数字、也无对应中文数字写法时必须判死；
// 中文数字读法不得命中更长数字的片段（「三」≠「十三」）。
func TestNumberKeepNoChineseNumeralEscape(t *testing.T) {
	cases := []struct {
		name string
		src  string
		tr   string
	}{
		{"纯丢数字", "需要 3 天", "It takes days"},
		{"读法命中片段_十三", "需要 3 天", "需要第十三天"},
		{"读法命中片段_三百", "预算 3 万元", "预算三百万元"},
		{"部分读法不完整", "编号 23", "编号二十"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := numCheck(t, Run(c.src, "en", c.tr))
			if ch.Pass {
				t.Fatalf("译文没有该数字（写法也只是别的数字的片段）应判死，实得通过 Detail=%q", ch.Detail)
			}
		})
	}
}

// TestStripNumSeparatorsEdge 归一函数边界：空串/多小数点/纯符号/超大数不 panic。
// 同时钉住「不误剥」：并列数字（"3 5"）与日期（"2026,10,10"）不得被当成千分位合并。
func TestStripNumSeparatorsEdge(t *testing.T) {
	// 不 panic 且返回值可用的边界输入
	edges := []string{"", "   ", ",,,", "...", "1.2.3", "999999999999999999999999", "-", "abc"}
	for _, e := range edges {
		got := stripNumSeparators(e)
		if got == "\x00PANIC" {
			t.Fatal("不可能分支")
		}
		_ = extractNumValues(got) // 抽取链路同样不得 panic
		_ = missingNumbers(e, "译文")
		_ = missingNumbers("源 1,000 测试", e)
	}
	// 恰好 3 位才剥：并列数字与日期不合并
	if got := stripNumSeparators("3 5"); got != "3 5" {
		t.Fatalf("并列数字不得被千分位合并，实得 %q", got)
	}
	if got := stripNumSeparators("2026,10,10"); got != "2026,10,10" {
		t.Fatalf("日期式短组不得被千分位合并，实得 %q", got)
	}
	// 多小数点：按正则口径拆出 "1.2" 与 "3"，不 panic 即可达意（畸形字面量本就不参与误放行）
	if vals := extractNumValues("1.2.3"); len(vals) != 2 {
		t.Fatalf("畸形小数应按正则自然切分，实得 %v", vals)
	}
}

// TestSuffixMultiplierGuard 数量词词缀的词边界保护：
// km/MB/Millions 这类「词缀只是更长单词前缀」的形态不得被乘上基数。
func TestSuffixMultiplierGuard(t *testing.T) {
	cases := []struct {
		rest string
		want float64
	}{
		{"", 1},
		{" 万", 1e4},
		{"亿", 1e8},
		{" million", 1e6},
		{"k", 1e3},
		{"M", 1e6},
		{" km", 1},       // 千米不是 1e3 基数
		{" MB", 1},       // 兆字节不是 1e6 基数
		{" Millions", 1}, // 复数词不认领（词缀后紧跟字母）
		{" steps", 1},    // 普通单词
	}
	for _, c := range cases {
		if got := suffixMultiplier(c.rest); got != c.want {
			t.Fatalf("suffixMultiplier(%q) = %v, want %v", c.rest, got, c.want)
		}
	}
}
