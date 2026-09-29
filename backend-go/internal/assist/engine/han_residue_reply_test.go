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

	t.Run("没改善保留原稿", func(t *testing.T) {
		st := newSeqStub(t, "邮件翻訳のご案内です")
		e := st.engine(t)
		rep := &Reply{Content: "文件翻訳のご案内です", Source: "llm"}
		if got := e.repairReplyHanResidue(ctx, "ja", rep).Content; !strings.Contains(got, "文件") {
			t.Fatalf("残片数量没减少时不许换稿（那是拿未验过的新稿覆盖客户正文）：%q", got)
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
