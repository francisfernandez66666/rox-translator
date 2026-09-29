// TestCanonicalizeBracketlessGoMarkers ★ 082x 第六条（2026-09-29 换件后现网第二批取证）：
// 动作标记还有**整个不带括号**的形态，现网两条读数——
//
//	消息 167（英文访客·价格轮）正文末尾：「…This requires precise calculation. gone:key editor,billing」
//	消息 158（英文访客·格式轮）正文末尾：「…[go editor,packages]」
//
// 158 那类带括号的上一批已经收口；167 这一支的漏口在**补翻之前**：
// 中文那轮模型写的就是不带括号的「go:editor,billing」，括号扫描找不到开括号就原样放行，
// 控制序列于是进了正文，再被出站补翻把动词 "go" 顺手变成 "gone"——形态在出站那一刻已经不存在，
// 换件也追不回来。所以无括号形态必须在同一次归一里收成规范形态（normalizeBracketlessGoMarker），
// 剥离、出按钮、历史回放清洗三条语义继续只有 extractGoMarkers 一份实现。
//
// 判据两侧都要钉：正向（该收的收干净）＋负向（正常人话逐字节不变）。
// 无括号形态没有「被括起来」这条天然边界，误伤风险全靠判定补回来，
// 负向那组少一条都可能让闸门绿着把用户的句子吃掉半句。
package engine

import (
	"strings"
	"testing"

	"translator/internal/assist/llm"
	"translator/internal/assist/store"
)

// 夹具功能卡 key：tickets / pricing / billing（见 engine_test.go newTestEngine）。
func TestCanonicalizeBracketlessGoMarkers(t *testing.T) {
	e := newTestEngine(t)

	// ① 正向：该收的必须收干净，且开括号之前的正文一个字都不许少
	positive := []struct {
		in      string
		keep    string // 必须留下的正文
		buttons int    // 归一后能变出的按钮数（走全链路 postProcess 数）
	}{
		{"This requires precise calculation. go:editor,billing", "precise calculation", 1},
		{"This requires precise calculation. go:key editor,billing", "precise calculation", 1},
		{"需要体验的话可以看看 go：pricing", "需要体验的话可以看看", 1},
		{"go:billing", "", 1}, // 整条正文就只有标记：剥完是空串，由上层兜底
		{"先说结论。\n go: tickets,pricing", "先说结论。", 2},
		{"末尾带个引号 go:pricing\" ", "末尾带个引号", 1}, // 残片后面只剩标点／引号／空白，仍算"落在末尾"
	}
	for _, c := range positive {
		got := e.canonicalizeGoMarkers(c.in)
		// 归一这一层的正确产物**就是**规范形态「【go:…」，所以这里只查「别长出空括号」；
		// 「控制序列有没有真被剥掉」归 postProcess 那一层查（在下面那条全链路腿里）。
		if strings.Contains(got, "【】") || strings.Contains(got, "【【") {
			t.Fatalf("归一产出畸形形态（双重归一）：\n 输入 %q\n 输出 %q", c.in, got)
		}
		if c.keep != "" && !strings.Contains(got, c.keep) {
			t.Fatalf("标记之前的正文被删了：\n 输入 %q\n 输出 %q", c.in, got)
		}
		rep := e.postProcess(c.in, "m")
		if strings.Contains(rep.Content, "go:") || strings.Contains(rep.Content, "【") {
			t.Fatalf("全链路（postProcess）仍漏控制序列：%q", rep.Content)
		}
		if len(rep.Actions) != c.buttons {
			t.Fatalf("%q 应出 %d 个按钮，实际 %d（%+v）", c.in, c.buttons, len(rep.Actions), rep.Actions)
		}
	}

	// ② 负向：正常人话必须**逐字节不变**。每条都对应一种误伤形态，缺一条就是把删除权交出去。
	safe := []string{
		"cargo: the price is fine, billing too",                 // "go" 前是字母：cargo 里的 go 不是标记头
		"logo:LangCross and pricing",                            // 同上（logo）
		"1go: billing",                                          // "go" 前是数字
		"You can go: to the store, please buy tickets",          // 每段三四个词，不是键名长相
		"you can go: billing and then we upload file",           // 一段里五个词，即便含 billing 也不算
		"先说结论 go: billing 然后再看别的",                               // 句中：这一段后面还有人话（位置判据）
		"你可以 go: 我帮你看看怎么弄",                                      // 冒号后第一个字符就不是键名
		"go: billing, pricing, tickets, editor, more, extras",   // 6 段（且卡在 48 字节边界上）：段数上限先拦掉
		"go: billing,pricing,tickets,editor,extra",              // 5 段：超过 4 段上限
		"go: pricing, and the rest of your sentence here",       // 首段是真 key、后段是一句话：靠 allGoMarkerKeyish 拦（反证④已证：拆掉它这条会被吃掉半句）
		"Note you can go: billing, to the store and get things", // 同上，且落在句末（位置判据也放行，全靠键名长相判据）
		"Good pricing here, nothing to go.",                     // 连冒号都没有
		"Visit go: https://example.com/a?b=c",                   // 冒号后是 URL，字符集先就不合（且后面还有正文）
	}
	for _, s := range safe {
		if got := e.canonicalizeGoMarkers(s); got != s {
			t.Fatalf("正常人话被吃了：\n 输入 %q\n 输出 %q", s, got)
		}
	}

	// ③ 与括号那一遍的分工：紧挨着开／闭括号的 go: 归括号那一遍管，这里必须让路。
	// 不让路的后果（本文件 ① 组首跑就是这个）：规范形态【go:tickets】被归成【【go:tickets】】，
	// extractGoMarkers 剥掉内层，正文里剩一个「【】」挂在用户屏幕上。
	for _, s := range []string{
		"先说结论。\n【go:tickets】中间还有一句。\n【go:pricing】",
		"[go:billing] 方括号形态",
	} {
		if got := e.canonicalizeGoMarkers(s); strings.Contains(got, "【】") || strings.Contains(got, "[]") {
			t.Fatalf("括号形态被双重归一，正文里留下空括号：%q", got)
		}
	}

	// ④ 取不到功能卡表（库里没配／DB 故障）时一律不动：歧义判定必须问得上一张真表。
	{
		db2, err := store.Open(t.TempDir() + "/empty_keys.db")
		if err != nil {
			t.Fatalf("open empty: %v", err)
		}
		t.Cleanup(func() { _ = db2.Close() })
		e2 := New(db2, llm.New(nil, 5))
		for _, s := range []string{"看看 go:billing", "看看 go editor"} {
			if got := e2.canonicalizeGoMarkers(s); got != s {
				t.Fatalf("功能卡表为空时仍做了无括号歧义归一：%q → %q", s, got)
			}
		}
		// 对照腿：带**括号**的规范意图不依赖卡表，空卡表下照样剥净（否则真控制序列会漏给客户）
		if got := e2.postProcess("可以看看【go:whatever】", "m"); strings.Contains(got.Content, "go:") {
			t.Fatalf("带冒号形态在空卡表下没剥净: %q", got.Content)
		}
	}
}
