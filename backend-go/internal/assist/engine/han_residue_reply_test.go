// han_residue_reply_test.go — ★ 092x 红腿一（2026-09-29 现网复问第四条）：
// 判残＋补翻此前**只接在 canned 那一路**，而现网那条是模型直接用日文写的回答——
// 主体是合格日文（整段补翻的占比判据抓不到），句子里却嵌着「文件翻訳」「ポイント充費」。
// 这一批把它接到对话正文的出站咽喉上，两条路共用同一个底座（见 han_residue.go 文件头）。
package engine

import (
	"context"
	"strings"
	"testing"
)

// TestReplyHanResidueRunsNeedsNoSource 对话版判残：没有源文也要认得出中文词形与简体字形。
func TestReplyHanResidueRunsNeedsNoSource(t *testing.T) {
	cases := []struct {
		lang string
		in   string
		want []string
	}{
		// 现网形态一：中文词形「文件」嵌在日文里（这两个字日文都单独成立，canned 版靠源文才抓得到）
		{"ja", "ファイルではなく文件翻訳で対応します", []string{"文件翻訳"}},
		// 现网形态二：简体独有字形（费）——日文正字法写「費」，这个字形只能是照抄的中文
		{"ja", "ポイント充费はいつでも可能です", []string{"充费"}},
		// ★ 093x（2026-09-30 真机复问）：字形档原来缺 选／语，词形档缺 系数——现网那条就是这么漏的
		{"ja", "か选択が必要です", []string{"选択"}},
		{"ja", "入力语言が日本語です", []string{"入力语言"}},
		{"ja", "系数で算出されます", []string{"系数"}},
		{"ja", "数据は暗号化されます", []string{"数据"}},
		{"ja", "折扣はいつでも可能です", []string{"折扣"}},
		// 反向对照：データ／割引 是日文侧的正确写法，进了判残清单就是把好译文送去重写
		{"ja", "データは暗号化されます。割引はいつでも可能です", nil},
		// ★ 093x 第三条腿：一个汉字都没有的「假名嵌拉丁」半截词（现网实证形态）
		{"ja", "プロfessionalモードがあります", []string{"プロfessional"}},
		// 反向对照：这些是**正常日文**，判残为空＝不许每条回答白打一次上游
		{"ja", "PDFのファイル形式をそのままに、pricing ページをご確認ください", nil},
		{"ja", "係数で算出されます。選択してください。", nil},
		{"ja", "1,000文字で23.5ポイントです。OKです", nil},
		{"ja", "Word/Excel/PPT/PDF に対応しています", nil},
		{"ja", "12言語に対応し、英語でも使えます", nil},
		// 正常日文回答一个都不许判残：翻訳／文書／ポイント／確認 全是日文正常写法
		{"ja", "文書翻訳・会話翻訳・企業用語ベース・ポイント確認", nil},
		// 其余语种：有汉字就是残留（跟 canned 那一路同口径）
		{"en", "Document 文件 translation is supported", []string{"文件"}},
		{"ru", "Поддержка 积分", []string{"积分"}},
		// 中文系访客：正文有汉字是正常渲染，判残＝每条回答白打一次上游
		{"zh", "文件翻译与积分计费都支持", nil},
		{"zh_hant", "ファイル", nil},
		{"", "文件", nil},
	}
	for _, c := range cases {
		got := replyHanResidueRuns(c.lang, c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("replyHanResidueRuns(%q, %q) = %v，期望 %v", c.lang, c.in, got, c.want)
		}
	}
	// 与 canned 版的差集必须**只有一处**（源文那条判据）：其余判据两份表共享，别在这儿另起一套
	if got := hanResidueRuns("ja", "与源文无关", "文件翻訳"); len(got) != 0 {
		t.Fatalf("canned 版在没有源文时本该判不出（这正是对话版存在的理由），实际：%v", got)
	}
}

// TestRepairReplyHanResidueAdoptsOnlyImprovement 三条硬判据一条不落，且只补翻一次。
func TestRepairReplyHanResidueAdoptsOnlyImprovement(t *testing.T) {
	ctx := context.Background()

	t.Run("改善则采用新稿并只多打一次上游", func(t *testing.T) {
		// 这里的第 1 条响应就是**补翻那一枪**的返回（本用例直接调出站补翻，不经过生成那一轮）
		st := newSeqStub(t, "ファイル翻訳ではなく文書翻訳のご案内です")
		e := st.engine(t)
		rep := &Reply{Content: "ファイル翻訳ではなく文件翻訳のご案内です", Source: "llm"}
		got := e.repairReplyHanResidue(ctx, "ja", rep).Content
		if !strings.Contains(got, "文書翻訳") || strings.Contains(got, "文件") {
			t.Fatalf("补翻生效却没采用新稿：%q", got)
		}
		if st.count() != 1 {
			t.Fatalf("补翻一次就收手（对话正文在访客等待链上），实际打了 %d 次", st.count())
		}
		// 提示词必须点名残片＋禁改数字：不点名的补翻等于重新翻一遍，成本一样而成功率更低
		if p := st.body(0); !strings.Contains(p, "文件翻訳") || !strings.Contains(p, "不许改任何数字") {
			t.Fatalf("补翻提示词没点名残片或没禁改数字：\n%s", p)
		}
	})

	// ★ 094x 口径更新：这一条原来写作「没改善保留原稿」，用的是「文件翻訳」那个夹具——
	// 而 094x 起「文件」在**确定性正字表**里（补翻被拒后就地换成 ファイル），
	// 那条断言会被二线防线合法地改写，不再证明"没换稿"。
	// 于是这里换成**表里没有的形态**（プロfessional 要在 専門／プロフェッショナル 里挑说法，本地猜不了）：
	// 判据回到它的本体——**补翻没改善就不许拿新稿覆盖客户正文**，
	// 正字表那一支的验收在 ja_residue_fixup_test.go，两边不互相顶替。
	t.Run("没改善且正字表兜不住时保留原稿", func(t *testing.T) {
		dirty := "プロfessionalのご案内です"
		st := newSeqStub(t, dirty)
		e := st.engine(t)
		rep := &Reply{Content: dirty, Source: "llm"}
		if got := e.repairReplyHanResidue(ctx, "ja", rep).Content; got != dirty {
			t.Fatalf("残片数量没减少时不许换稿（那是拿未验过的新稿覆盖客户正文）：%q", got)
		}
		if st.count() != 1 {
			t.Fatalf("补翻一次就收手，实际打了 %d 次", st.count())
		}
	})

	t.Run("行数变了保留原稿", func(t *testing.T) {
		st := newSeqStub(t, "第一行 文件翻訳\n第二行のご案内", "合成一行的答案")
		e := st.engine(t)
		rep := &Reply{Content: "第一行 文件翻訳\n第二行のご案内", Source: "llm"}
		if got := e.repairReplyHanResidue(ctx, "ja", rep).Content; !strings.Contains(got, "\n") {
			t.Fatalf("新稿行数变了却仍被采用（列表会被压平）：%q", got)
		}
	})

	t.Run("中文界面一次都不打上游", func(t *testing.T) {
		st := newSeqStub(t, "不该被调用")
		e := st.engine(t)
		rep := &Reply{Content: "文件翻译支持", Source: "llm"}
		if got := e.repairReplyHanResidue(ctx, "zh", rep).Content; got != "文件翻译支持" {
			t.Fatalf("中文轮正文被改动：%q", got)
		}
		if st.count() != 0 {
			t.Fatalf("中文轮一次都不该打上游，实际 %d 次", st.count())
		}
	})

	t.Run("整段回错语言不归这条管（上层那条补翻的射程，不重复打上游）", func(t *testing.T) {
		st := newSeqStub(t, "不该被调用")
		e := st.engine(t)
		whole := "我们按积分计费，专业模式每 1000 源字符 8 积分，建单固定 7.5 积分，具体看实际字符数。"
		rep := &Reply{Content: whole, Source: "llm"}
		if got := e.repairReplyHanResidue(ctx, "en", rep).Content; got != whole {
			t.Fatalf("整段中文该由 enforceReplyLang 处理，本条却动了正文：%q", got)
		}
		if st.count() != 0 {
			t.Fatalf("整段错语言的正文又被补打一枪（%d 次）——两条补翻腿重叠就是把访客等待时间翻倍", st.count())
		}
	})

	t.Run("干净正文不打上游", func(t *testing.T) {
		st := newSeqStub(t, "不该被调用")
		e := st.engine(t)
		clean := "文書翻訳・会話翻訳に対応しています"
		rep := &Reply{Content: clean, Source: "llm"}
		if got := e.repairReplyHanResidue(ctx, "ja", rep).Content; got != clean {
			t.Fatalf("合格日文被改动：%q", got)
		}
		if st.count() != 0 {
			t.Fatalf("判残为空时白打了 %d 次上游（每条日文回答多一次往返就是首响应变慢）", st.count())
		}
	})
}

// leakReply093x ★ 093x（2026-09-30）真机挂件复问打现网拿回的**逐字原文**（日文轮问报价）。
// 上一批的判残尺子对这一段只认得「扣费」一处，其余四处漏到客户屏幕上。
// 本条用例把原文钉在这里当回归基线：尺子变窄（有人删表里的字、删腿）当场红灯。
const leakReply093x = "日本語から中国語への翻訳は、源文字数（源語の文字数）で計算されます。" +
	"例えば1,000文字の日本語を翻訳する場合、**快速モードで150ポイント**か、" +
	"**プロfessionalモードで400ポイント**か选択が必要です。ポイント数は原文の文字数（日本語は全角文字含む）× 系数で算出されますが、" +
	"実際の扣费は原文の文字数と言語に応じて変動します。お手数ですが、原文を送信いただければ正確なポイント数をご案内できます。" +
	"[ pricing ページで詳細を確認]  （※日本語で回答するため、必要に応じて「ポイント」を用い、" +
	"入力语言が日本語であることを考慮して翻訳を実施。）"

// TestReplyHanResidueRunsOnRealProductionReply 现网原文必须**逐处**进判残清单（不再只抓到一处）。
func TestReplyHanResidueRunsOnRealProductionReply(t *testing.T) {
	got := replyHanResidueRuns("ja", leakReply093x)
	joined := strings.Join(got, "|")
	for _, want := range []string{"选択", "系数", "扣费", "プロfessional", "入力语言"} {
		if !strings.Contains(joined, want) {
			t.Errorf("现网漏点 %q 没进判残清单（尺子又变窄了）；实际：%v", want, got)
		}
	}
	// 正常日文词一个都不许进来：翻訳／文字／確認／言語 这些被抓走，补翻提示词就变成"重写整段"，
	// 三条硬判据里"残片严格变少"会被灌水的好条目顶掉，采用率反而下降。
	for _, bad := range []string{"翻訳", "文字数", "確認", "言語", "全角"} {
		if strings.Contains(joined, bad) {
			t.Errorf("正常日文 %q 被判成残留：%v", bad, got)
		}
	}
}

// TestHanResidueRunsCannedPathAlsoSeesLatinIntrusion ★ 093x 第三条腿对 canned 那一路同样生效。
// canned 的汉字腿有**中文源文**可比，而「プロfessional」在源文里一个字都对不上——
// 只接对话正文那一路的话，日文欢迎词里的半截英文照样漏（两条路必须共用同一条尺子）。
func TestHanResidueRunsCannedPathAlsoSeesLatinIntrusion(t *testing.T) {
	got := hanResidueRuns("ja", "专业模式按积分计费，快速模式更省", "プロfessionalモードで400ポイントです")
	if len(got) != 1 || got[0] != "プロfessional" {
		t.Fatalf("canned 路的假名嵌拉丁腿没接上：%v", got)
	}
	// 源文里根本没有这个词形也照样抓到（对照：汉字腿在同样条件下必须为空）
	if extra := jaLeakSubstrings("专业模式按积分计费", hanRunsOf("プロfessionalモードです")); len(extra) != 0 {
		t.Fatalf("汉字腿不该在源文对不上时判残：%v", extra)
	}
	// 正常日文欢迎词零残留：判残为空＝greet 不白打补翻那一枪
	if leaks := hanResidueRuns("ja", "支持文档翻译与积分计费", "ドキュメント翻訳に対応しています。1,000文字で23.5ポイントです。"); len(leaks) != 0 {
		t.Fatalf("正常日文被判残：%v", leaks)
	}
}

// TestRepairPromptForbidsCopyingLeaksBack ★ 093x 第三条根因：提示词原来写"其余措辞尽量照抄上一版"，
// 于是**没进清单的坏词是被要求照抄回来的**——判残尺子修好之前，这一句本身就是漏口。
// 现在必须逐条点名"每一处都要改掉"，并把"不许加括号备注"写进去（旁白也是重写腿会新造的形态）。
func TestRepairPromptForbidsCopyingLeaksBack(t *testing.T) {
	st := newSeqStub(t, "プロフェッショナルモードで400ポイントです。")
	e := st.engine(t)
	rep := &Reply{Content: "プロfessionalモードで400ポイントです", Source: "llm"}
	if got := e.repairReplyHanResidue(context.Background(), "ja", rep).Content; strings.Contains(got, "professional") {
		t.Fatalf("半截英文没被换掉：%q", got)
	}
	p := st.body(0)
	for _, want := range []string{"每一处", "不许加任何括号备注", "不许改任何数字", "プロfessional"} {
		if !strings.Contains(p, want) {
			t.Fatalf("补翻提示词缺 %q：\n%s", want, p)
		}
	}
}
