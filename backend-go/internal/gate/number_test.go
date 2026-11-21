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

// TestNumberKeepRangeSharedSuffix ★ 2026-11-21 真值验收（tv_job3/tv_job4）暴露的假警：
// 中文量级词在数值区间里**只写在末端**（"45–80 万"＝45万–80万），旧判据只给第二个数乘 1e4，
// 第一个数按 45 去比 ⇒ 正确译文 `450,000–800,000` 被判「缺失: 45, 80」。
//
// 三条腿（缺一条这锁就空转）：
//
//	A 正确译文零缺失（这是本锁的靶心）；
//	B 真丢量级仍判缺失（`45–80` 丢了「万」＝10000 倍的错，必须红）；
//	C 真丢数字仍判缺失（`450,000–800,000` 里少了 80 那一半）。
func TestNumberKeepRangeSharedSuffix(t *testing.T) {
	pass := []struct{ name, src, tr string }{
		{"半角短横", "45–80 万", "450,000–800,000"},
		{"中文至", "45 至 80 万", "450,000–800,000"},
		{"波浪号", "45~80万", "450000-800000"},
		{"带货币", "25–50 万 元", "250,000–500,000 yuan"},
		{"区间后无空格", "1.5–2 亿", "150–200 million"},
	}
	for _, c := range pass {
		t.Run("pass_"+c.name, func(t *testing.T) {
			if got := missingNumbers(c.src, c.tr); len(got) != 0 {
				t.Fatalf("正确译文不得判缺失，实得 %v（src=%q tr=%q）", got, c.src, c.tr)
			}
			if !NumbersPreserved(c.src, c.tr) {
				t.Fatalf("NumbersPreserved 应与 missingNumbers 同口径，实得 false（src=%q tr=%q）", c.src, c.tr)
			}
		})
	}
	fail := []struct{ name, src, tr string }{
		{"丢了量级", "45–80 万", "45–80"},
		{"丢了一半", "45–80 万", "450,000"},
		{"只翻一个", "45–80 万", "450,000–80"},
	}
	for _, c := range fail {
		t.Run("fail_"+c.name, func(t *testing.T) {
			if got := missingNumbers(c.src, c.tr); len(got) == 0 {
				t.Fatalf("真丢数字/量级必须判缺失，实得零缺失（src=%q tr=%q）", c.src, c.tr)
			}
			if NumbersPreserved(c.src, c.tr) {
				t.Fatalf("NumbersPreserved 必须与 missingNumbers 同判：真丢却报 true（src=%q tr=%q）", c.src, c.tr)
			}
		})
	}
}

// TestRangeSuffixMultiplierUnit 区间量级词识别的边界：只在「数字+区间符+数字」形态生效。
func TestRangeSuffixMultiplierUnit(t *testing.T) {
	cases := []struct {
		src  string
		end  int
		want float64
	}{
		{"45–80 万", 2, 1e4}, // 区间：末端量级词回填到第一个数
		{"45 万", 2, 1},      // 不是区间（后面直接是量级词）⇒ 由 suffixMultiplier 负责
		{"45-80", 2, 1},     // 区间但无末端量级词
		{"2026-11", 4, 1},   // 日期形态：右端不是量级词
		{"45–80 万", 6, 1},   // end 落在区间右端数字之后：由 suffixMultiplier 负责
	}
	for _, c := range cases {
		if got := rangeSuffixMultiplier(c.src, c.end); got != c.want {
			t.Errorf("rangeSuffixMultiplier(%q,%d)=%v, want %v", c.src, c.end, got, c.want)
		}
	}
}

// TestSuffixMultiplierMultilingualMagnitudes ★ 2026-11-21 上下文重译批收尾：
// 量级词表原先只认中文「万/亿」与英文 million/billion，而**目标语会用它自己的量级词**。
// 现网实证（演示站 `tvctx` 小件，希伯来语）：源 `150–250 万` 被正确译成 `1.5–2.5 מיליון`，
// 判据却报「数字保持（缺失: 150, 250）」——数字一个没丢，错的是尺子的语种射程。
// 这条假警会进 `gate_warnings` 变成客户看到的「质检存疑」，代价是真漏译被淹掉。
//
// ★ 希伯来/阿拉伯词条一律用 `\u` 转义写：它们是右向文字，直接抄进源码会在编辑器里
//
//	与相邻标点重排，看代码的人根本分不清自己改的是哪个字符（本批就靠码位核对过一次）。
func TestSuffixMultiplierMultilingualMagnitudes(t *testing.T) {
	cases := []struct {
		name string
		rest string
		want float64
	}{
		// 希伯来语：מיליון=百万、מיליארד=十亿（码位：מ=05de י=05d9 ל=05dc ו=05d5 ן=05df א=05d0 ר=05e8 ד=05d3）
		{"希伯来_百万", " \u05de\u05d9\u05dc\u05d9\u05d5\u05df", 1e6},
		{"希伯来_百万复数", " \u05de\u05d9\u05dc\u05d9\u05d5\u05e0\u05d9\u05dd", 1e6},
		{"希伯来_十亿", " \u05de\u05d9\u05dc\u05d9\u05d0\u05e8\u05d3", 1e9},
		// 阿拉伯语
		{"阿拉伯_百万", " \u0645\u0644\u064a\u0648\u0646", 1e6},
		{"阿拉伯_十亿", " \u0645\u0644\u064a\u0627\u0631", 1e9},
		// 俄语
		{"俄语_百万", " \u043c\u0438\u043b\u043b\u0438\u043e\u043d", 1e6},
		{"俄语_十亿", " \u043c\u0438\u043b\u043b\u0438\u0430\u0440\u0434", 1e9},
		{"俄语_千", " \u0442\u044b\u0441\u044f\u0447", 1e3},
		// 韩语/日语（汉字量级词）
		{"韩语_万", " \ub9cc", 1e4},
		{"韩语_亿", " \uc5b5", 1e8},
		{"日语_亿", " \u5104", 1e8},
		{"日语_兆", " \u5146", 1e12},
		// 拉丁语族与其它
		{"意大利_百万", " milioni", 1e6},
		{"意大利_十亿", " miliardi", 1e9},
		{"荷兰_百万", " miljoen", 1e6},
		{"波兰_百万", " milion", 1e6},
		{"西班牙_百万", " millones", 1e6},
		{"葡语_百万", " milh\u00f5es", 1e6},
		{"越南_百万", " tri\u1ec7u", 1e6},
		{"越南_十亿", " t\u1ef7", 1e9},
		{"土耳其_百万", " milyon", 1e6},
		{"土耳其_千", " bin", 1e3},
		{"印尼_百万", " juta", 1e6},
		{"西语_千", " mil", 1e3},
		// 反向：更长单词前缀仍不认领（词边界那条腿不能被这批新词条弄坏）
		{"普通单词_千米", " kilometro", 1},
		{"普通单词_分钟", " minutes", 1},
		{"普通单词_军事", " militar", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := suffixMultiplier(c.rest); got != c.want {
				t.Fatalf("suffixMultiplier(%q) = %v, want %v", c.rest, got, c.want)
			}
		})
	}
}

// TestSuffixMultiplierShadowGuard 表内顺序守卫：短词条排在长词条**前面**且乘子不同时，
// 长词条要么被 ASCII 词边界兜住（尾巴是字母 ⇒ continue），要么**永远匹配不到**。
// 非 ASCII 词条没有词边界保护（中文/希伯来/俄语词之间不加空格是常态），那一档只能靠顺序。
//
// 这条锁不是"形式主义"：它挡的正是 `M` 排在 `Million` 前面那一类——
// 当年英文百万就是这么被单字母截断的（文件头第一条注释），换成俄语/希伯来语时
// 报错形态会变成"数字一个不差却报缺失"，比直接崩更难归因。
func TestSuffixMultiplierShadowGuard(t *testing.T) {
	for i := 0; i < len(numSuffixMults); i++ {
		for j := i + 1; j < len(numSuffixMults); j++ {
			short, long := numSuffixMults[i], numSuffixMults[j]
			if short.mult == long.mult || !strings.HasPrefix(long.suf, short.suf) {
				continue
			}
			tail := long.suf[len(short.suf):]
			// 尾巴是 ASCII 字母 ⇒ 词边界那条腿会 continue 到下一个词条，不算遮蔽。
			if tail != "" && tail[0] < 0x80 && (tail[0] >= 'a' && tail[0] <= 'z' || tail[0] >= 'A' && tail[0] <= 'Z') {
				continue
			}
			t.Fatalf("词条顺序有遮蔽：%q(×%g) 排在 %q(×%g) 之前，且尾巴 %q 不是 ASCII 字母 ⇒ 后者永远匹配不到",
				short.suf, short.mult, long.suf, long.mult, tail)
		}
	}
}

// TestNumberKeepCrossLanguageMagnitude 行为腿：跨语种量级词的正确译文零缺失，
// 而**真丢量级**仍必须判缺失（否则上面那批词条会让闸门变成摆设）。
func TestNumberKeepCrossLanguageMagnitude(t *testing.T) {
	pass := []struct{ name, src, tr string }{
		{"希伯来_区间百万", "150–250 万", "1.5–2.5 \u05de\u05d9\u05dc\u05d9\u05d5\u05df"},
		{"希伯来_区间亿", "2–3 亿", "200–300 \u05de\u05d9\u05dc\u05d9\u05d5\u05df"},
		// ⚠️ 俄语这里刻意用**点号小数**：判据的 reNumbers 只认 `.` 作小数点，
		//    俄/德/法惯用的逗号小数（`1,5`）会被拆成 1 与 5 两个数 ⇒ 报缺失。
		//    那一档是**已知盲区**（逗号同时是英文千分位，改它要连带改 stripNumSeparators 的判据），
		//    本批不碰、也不假装它能过：真出现时是 warning 级（不拦交付）。
		{"俄语_区间百万", "150–250 万", "1.5–2.5 \u043c\u0438\u043b\u043b\u0438\u043e\u043d\u0430"},
		{"韩语_万", "25–50 万", "25\ub9cc\u201350\ub9cc"},
	}
	for _, c := range pass {
		t.Run("pass_"+c.name, func(t *testing.T) {
			if got := missingNumbers(c.src, c.tr); len(got) != 0 {
				t.Fatalf("跨语种量级词的正确译文不得判缺失，实得 %v（src=%q tr=%q）", got, c.src, c.tr)
			}
		})
	}
	fail := []struct{ name, src, tr string }{
		{"希伯来_丢了量级", "150–250 万", "1.5–2.5"},
		{"希伯来_只翻一半", "150–250 万", "1.5 \u05de\u05d9\u05dc\u05d9\u05d5\u05df"},
		{"俄语_丢了量级", "150–250 万", "1,5–2,5"},
	}
	for _, c := range fail {
		t.Run("fail_"+c.name, func(t *testing.T) {
			if got := missingNumbers(c.src, c.tr); len(got) == 0 {
				t.Fatalf("真丢量级必须判缺失，实得零缺失（src=%q tr=%q）", c.src, c.tr)
			}
		})
	}
}
