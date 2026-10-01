// ja_residue_fixup_test.go — ★ 094x（2026-09-30，093x 换件**当天**的现网复问又抓到 3 条红）：
// 三条红全部是「判残抓到了、补翻被拒」，而那一行 WARN 里没有原因字段，
// 于是"上游抖了一下"（同窗口实测 10 条 provider 调用失败）和"模型改不动那几处"
// 在日志里长得一模一样。本文件钉两件事：
//
//	① 补翻底座的第三个返回值＝拒绝原因，五个出口各有自己的名字，日志能分档；
//	② 日文对话正文在补翻被拒后过一道**确定性正字表**（applyJaResidueFixups），
//	   四道前置任一不过就整体作废回原稿。
//
// ⚠️ 这个文件里的"表值必须过判残"那条是**正向对照**，不是走过场：
// 表值自己要是还会被判残（比如误把 数据 写成 数値），替换完等于原地打转，
// 而判据④（残片严格变少）会把它整段作废——那时这条腿静默失效，界面上什么都看不出来。
//
// ★ 10-01（〇-AF 收尾）本文件多两件事，都是"把一句话结论变成代码"：
//
//	① 现网实证必须在表里那份清单补上「文件」——它是 10-01 那条 ja 正文五个残片里唯一被表兜住的；
//	② 新增 TestJaResidueFixupsTenOfOneFormsStayOutOfTable：把另外四个形态（料金额／术语库／
//	   定价页面／プロfessional）**按收录门槛判为表外**这件事钉成负向锁，
//	   并当场演示"为什么连子形态（金额→金額、页面→ページ）都不许塞"——
//	   那会把可见残留换成不可见残留，四道前置一道都挡不住，只能靠这一条锁。
//	   逐条判词的正文在 han_residue.go 的 jaResidueFixups 表头。
package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/assist/llm"
)

// stubClient 把 seqStub 的地址包成一个 llm.Client（补翻底座只吃 client，不吃引擎）。
//
// 为什么不复用 seqStub.engine(t)：那个连带起一份 newTestEngine（临时库＋种子数据），
// 而这一批的判据全在底座自己的分支上——把引擎拖进来只是让用例更慢、失败更难归属。
func stubClient(s *seqStub) *llm.Client {
	return llm.New([]llm.Provider{{Name: "main", BaseURL: s.url, APIKey: "k", Model: "m"}}, 5)
}

// TestJaResidueFixupTableIsSelfConsistent 正字表自身的三条不变量（逐条都能反证）。
func TestJaResidueFixupTableIsSelfConsistent(t *testing.T) {
	seen := map[string]string{}
	for _, fp := range jaResidueFixups {
		if fp.from == "" || fp.to == "" {
			t.Fatalf("正字表有空项：%+v", fp)
		}
		if fp.from == fp.to {
			t.Fatalf("正字表改了自己（%q）：等于白替换一次还占掉一条前置", fp.from)
		}
		if prev, dup := seen[fp.from]; dup {
			t.Fatalf("同一个键配了两个值：%q→%q 与 %q→%q", fp.from, prev, fp.from, fp.to)
		}
		seen[fp.from] = fp.to
		// ① 键必须**会被判残点名**：抓不到的键放进表里就是让正常日文冒被改的风险
		if got := replyHanResidueRuns("ja", fp.from); len(got) == 0 {
			t.Errorf("正字表键 %q 判残抓不到，却配了替换值 %q——这条键改不到残留，只会误伤正文", fp.from, fp.to)
		}
		// ② 值必须**干净**（过判残）：值再被判残就是原地打转
		if got := replyHanResidueRuns("ja", fp.to); len(got) != 0 {
			t.Errorf("正字表值 %q（%q 改成它）自己还被判残：%v——替换后照样是残留，前置④会把它整段作废",
				fp.to, fp.from, got)
		}
		// ③ 值不许带数字：前置②（数字序列逐字不变）会把它挡死，配进表里等于一条永远失效的项
		if digitSeqOf(fp.to) != "" {
			t.Errorf("正字表值 %q 带数字，替换必然撞上『数字一字不动』前置：%q→%q 是死项", fp.to, fp.from, fp.to)
		}
		// ④ 值不许带换行：同理撞前置③
		if strings.ContainsAny(fp.to, "\n\r") {
			t.Errorf("正字表值 %q 带换行，会改掉行数：%q→%q", fp.to, fp.from, fp.to)
		}
	}
	// 长词必须排在它的子串之前：替换按表序走，「什么」先跑就把「为什么」拆成「なぜ」＋余字。
	idx := map[string]int{}
	for i, fp := range jaResidueFixups {
		idx[fp.from] = i
	}
	for _, fp := range jaResidueFixups {
		for _, other := range jaResidueFixups {
			if other.from != fp.from && strings.Contains(other.from, fp.from) && idx[fp.from] < idx[other.from] {
				t.Fatalf("表序不对：%q 排在 %q 前面，长词永远轮不到替换（替换按表序走）", fp.from, other.from)
			}
		}
	}
	// 现网实证的残片必须在表里（这条是"删除清单只收现网逐字实证形态"口径的反面：
	// 已经实证漏出的词，任何一批把它们从表里抹掉都要当场红）。
	//   费用／什么／系数 ← 093x/094x（2026-09-30）那三条红；
	//   文件 ← ★ 10-01 新增：现网那条 ja 正文的五个残片里**唯一被表兜住的一个**
	//          （日志实读 fixed=文件、before=5 after=4；覆盖率读数见
	//           scripts/assist_residue_report.py 与本文件下方 TestJaResidueFixupsTenOfOneFormsStayOutOfTable）。
	for _, k := range []string{"费用", "什么", "系数", "文件"} {
		if _, ok := seen[k]; !ok {
			t.Fatalf("现网实证的残片 %q 不在正字表里——那几条红会原样复发", k)
		}
	}
	// 「积分」的正字档必须与作答语种那张术语表同值（两处口径分叉＝一个客户屏幕上两种说法）
	if seen["积分"] != pointsTermByLang["ja"] {
		t.Fatalf("正字表把积分写成 %q，而术语表（pointsTermByLang）是 %q——两处必须同值",
			seen["积分"], pointsTermByLang["ja"])
	}
}

// TestApplyJaResidueFixupsGuards 四道前置逐条压：只有"该改的改、不该动的一个字不动"。
func TestApplyJaResidueFixupsGuards(t *testing.T) {
	t.Run("只动被判残点名的词形且数字一字不动", func(t *testing.T) {
		text := "1,000文字なら150ポイントです。実際の费用と系数を確認してください。"
		leaks := replyHanResidueRuns("ja", text)
		if len(leaks) == 0 {
			t.Fatalf("夹具本身没被判残：%q", text)
		}
		got, applied := applyJaResidueFixups("ja", text, leaks)
		if !strings.Contains(got, "費用") || !strings.Contains(got, "係数") {
			t.Fatalf("补翻被拒时正字表没生效：%q（applied=%v）", got, applied)
		}
		if digitSeqOf(got) != digitSeqOf(text) {
			t.Fatalf("正字替换动了数字：%q → %q", text, got)
		}
		if len(replyHanResidueRuns("ja", got)) >= len(leaks) {
			t.Fatalf("替换后残片没减少：%v → %v", leaks, replyHanResidueRuns("ja", got))
		}
	})
	t.Run("正常日文正文一个键都不许碰", func(t *testing.T) {
		// 表值（情報／検索／データ）本身在正文里是**正常日文**：判残为空＝前置①直接不成立
		text := "データは検索され、情報は暗号化されます。200ポイントです。"
		if leaks := replyHanResidueRuns("ja", text); len(leaks) != 0 {
			t.Fatalf("夹具本该判残为空，实际 %v", leaks)
		}
		got, applied := applyJaResidueFixups("ja", text, nil)
		if len(applied) != 0 || got != text {
			t.Fatalf("判残为空却被改写：%q → %q（applied=%v）", text, got, applied)
		}
	})
	t.Run("假名嵌拉丁一律不就地替换", func(t *testing.T) {
		// 「プロfessional」要在 専門 / プロフェッショナル 里**挑一个说法**，本地猜不了：
		// 这条是 093x 定下的口径（见 jaLatinIntrusionPat 的注释），094x 不许把它破掉。
		text := "プロfessionalモードがあります。"
		leaks := replyHanResidueRuns("ja", text)
		if len(leaks) != 1 || leaks[0] != "プロfessional" {
			t.Fatalf("夹具判残读数不对：%v", leaks)
		}
		got, applied := applyJaResidueFixups("ja", text, leaks)
		if len(applied) != 0 || got != text {
			t.Fatalf("本地替换越界改了需要挑说法的形态：%q → %q（applied=%v）", text, got, applied)
		}
	})
	t.Run("非日文档整体不进射程", func(t *testing.T) {
		text := "費用と系数は 150 です" // 拉丁语种里任何汉字段都是残留，但没有"正字表"这回事
		got, applied := applyJaResidueFixups("en", text, replyHanResidueRuns("en", text))
		if len(applied) != 0 || got != text {
			t.Fatalf("正字表只管日文，en 档被改写：%q → %q（applied=%v）", text, got, applied)
		}
	})
	t.Run("残片没严格变少就整体作废（前置④独立成立）", func(t *testing.T) {
		// 少报一个残片：正文里的「扣费」不在表内、本地修不掉，而传进来的清单只有「选択」，
		// ⇒ 替换后剩余残片数（1）并不**少于**清单长度（1），整段作废回原稿。
		// 这一条专门钉前置④那句 `>= len(leaks)`：把那句删掉，本用例立刻红（反证脚本实跑过）。
		text := "选択が必要です。実際の扣费も確認ください。"
		got, applied := applyJaResidueFixups("ja", text, []string{"选択"})
		if len(applied) != 0 || got != text {
			t.Fatalf("残片数没严格变少却仍被改写（前置④失守）：%q → %q（applied=%v）", text, got, applied)
		}
		// 对照支：按真实判残清单（两个残片都点名）调用，同一段就必须改成功——
		// 少了这条对照，上面那个"作废"可能只是整条腿根本没跑起来（恒空也算通过）。
		full := replyHanResidueRuns("ja", text)
		if patched, ap := applyJaResidueFixups("ja", text, full); len(ap) == 0 || !strings.Contains(patched, "選択") {
			t.Fatalf("按实际判残清单调用却没生效（对照支）：%q（leaks=%v ap=%v）", patched, full, ap)
		}
	})
	t.Run("无键可改时原样返回", func(t *testing.T) {
		// 残片清单里没有表内键（前置①不成立）⇒ applied 必须为空，调用方据此走"保留原稿"那一支
		text := "150ポイントです。"
		got, applied := applyJaResidueFixups("ja", text, []string{"根本不在表里的片段"})
		if len(applied) != 0 || got != text {
			t.Fatalf("无键可改却返回了改动：%q → %q（applied=%v）", text, got, applied)
		}
		// 前置①的独立支：正文里**有**表内键（选択），但调用方给的清单里没点名它。
		// 少了这一条判据，"清单与正文不匹配"时也会照改——那是把射程从"判残点名的那几段"
		// 悄悄放大成"整段正文"，正常日文冒被误伤的风险就是这么长出来的。
		other := "选択が必要です。"
		got2, applied2 := applyJaResidueFixups("ja", other, []string{"清单里没点名的东西"})
		if len(applied2) != 0 || got2 != other {
			t.Fatalf("未被判残点名的词形被就地改写（前置①失守）：%q → %q（applied=%v）", other, got2, applied2)
		}
	})
	t.Run("长词整词替换：为什么→なぜ 而不是被 什么 拆成何", func(t *testing.T) {
		text := "为什么費用が変動しますか。"
		got, _ := applyJaResidueFixups("ja", text, replyHanResidueRuns("ja", text))
		if !strings.Contains(got, "なぜ") {
			t.Fatalf("「为什么」没按整词换成 なぜ（表序被打乱就会先跑 什么 拆成长词）：%q", got)
		}
	})
}

// TestRepairReplyHanResidueRejectReasonAndFixup 出站那条腿：补翻被拒时先看原因分档、再走正字表。
func TestRepairReplyHanResidueRejectReasonAndFixup(t *testing.T) {
	ctx := context.Background()

	t.Run("补翻没改善时仍走正字表并只打一次上游", func(t *testing.T) {
		// 第二稿＝原样吐回第一稿（残片一个没少 ⇒ 前置④必然拒用），这是现网那条红的形态
		dirty := "実際の扣费と费用、系数を確認ください。入力语言は日本語です。"
		st := newSeqStub(t, dirty, dirty)
		e := st.engine(t)
		rep := &Reply{Content: dirty, Source: "llm"}
		got := e.repairReplyHanResidue(ctx, "ja", rep).Content
		if !strings.Contains(got, "費用") || !strings.Contains(got, "係数") || !strings.Contains(got, "言語") {
			t.Fatalf("补翻被拒后正字表没接上：%q", got)
		}
		if !strings.Contains(got, "扣费") {
			t.Fatalf("正字表越界改了需要挑说法的形态（扣费→課金/お支払い 二选一，本地猜不了，必须原样留给补翻）：%q", got)
		}
		if st.count() != 1 {
			t.Fatalf("补翻仍只打一次上游（正字表是本地腿，不该多打），实际 %d 次", st.count())
		}
	})

	t.Run("根本没有可用上游时也有正字表兜底", func(t *testing.T) {
		// e.llm=nil 且 configs 空 ⇒ ensureLLM 回 nil ⇒ reason=no_upstream。
		// 现网 09-30 那一窗是 upstream_error（10 条 provider 调用失败），两者在日志里靠档名分开，
		// 但**本地这条二线防线对两种都要生效**——上游通不通不该决定客户看到中文词形还是日文正字。
		e := newTestEngine(t)
		e.llm = nil
		dirty := "実際の费用を確認ください。"
		rep := &Reply{Content: dirty, Source: "fallback"}
		got := e.repairReplyHanResidue(ctx, "ja", rep).Content
		if !strings.Contains(got, "費用") {
			t.Fatalf("没有上游时正字表没兜住：%q", got)
		}
	})

	t.Run("补翻成功时不许再叠一次正字表（两把刀不同时落）", func(t *testing.T) {
		// 桩回一份**已经改好**的新稿 ⇒ 三条硬判据通过 ⇒ 直接采用；
		// 若这时还去跑本地替换，就是把"模型已经写对的句子"再按表改一遍（表是补翻的替补，不是叠加层）。
		e := newTestEngine(t)
		dirty := "実際の费用を確認ください。"
		e.llm = stubClient(newSeqStub(t, "費用の確認方法をご案内します。"))
		rep := &Reply{Content: dirty, Source: "llm"}
		got := e.repairReplyHanResidue(ctx, "ja", rep).Content
		if got != "費用の確認方法をご案内します。" {
			t.Fatalf("补翻已采用却又被本地表改写（不该叠加）：%q", got)
		}
	})

	t.Run("上游被 max_tokens 截断时回 truncated", func(t *testing.T) {
		// 单独一个桩：finish_reason=length ⇒ usage.Truncated ⇒ rejectTruncated。
		// 这一档在现网是"回答太长"的形态（对话正文用 replyLocalizeMaxTokens=1200，仍可能撞），
		// 没有它就得等客户看到半截回答才知道。
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"費用のご案内です"},` +
				`"finish_reason":"length"}],"usage":{"completion_tokens":1200}}`))
		}))
		defer srv.Close()
		c := llm.New([]llm.Provider{{Name: "main", BaseURL: srv.URL, APIKey: "k", Model: "m"}}, 5)
		e := newTestEngine(t)
		dirty := "実際の费用を確認ください。"
		if _, ok, reason := e.repairHanResidueBase(context.Background(), c, "ja", replyRepairTopics(), "", dirty,
			replyHanResidueRuns("ja", dirty), 1200,
			func(out string) []string { return replyHanResidueRuns("ja", out) }); ok || reason != rejectTruncated {
			t.Fatalf("截断那档应回 %q，实际 ok=%v reason=%q", rejectTruncated, ok, reason)
		}
	})

	// 日志里那七个**字面量**是运维读档的口径（Go 常量名互换在单测里是查不出来的——
	// 断言拿的是常量，值跟着一起换），所以必须逐字钉住字面量本身，反证 Y7 就红在这一条上。
	t.Run("拒绝原因的字面量逐字钉住（日志档名是对外契约）", func(t *testing.T) {
		want := map[string]string{
			"rejectNoUpstream":    "no_upstream",
			"rejectNoLeaks":       "no_leaks",
			"rejectUpstreamError": "upstream_error",
			"rejectTruncated":     "truncated",
			"rejectEmptyOutput":   "empty_output",
			"rejectLineCount":     "line_count",
			"rejectNotImproved":   "not_improved",
		}
		got := map[string]string{
			"rejectNoUpstream":    rejectNoUpstream,
			"rejectNoLeaks":       rejectNoLeaks,
			"rejectUpstreamError": rejectUpstreamError,
			"rejectTruncated":     rejectTruncated,
			"rejectEmptyOutput":   rejectEmptyOutput,
			"rejectLineCount":     rejectLineCount,
			"rejectNotImproved":   rejectNotImproved,
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("%s 的日志档名应是 %q，实际 %q——档名换了等于把现网排障的读法改掉（互换成另一档更糟：直接指错方向）",
					k, v, got[k])
			}
		}
	})

	t.Run("拒绝原因分档可辨（每个出口各有各的名字）", func(t *testing.T) {
		e := newTestEngine(t)
		dirty := "実際の费用を確認ください。"
		leaks := replyHanResidueRuns("ja", dirty)
		if len(leaks) == 0 {
			t.Fatalf("夹具本身没被判残：%q", dirty)
		}
		after := func(out string) []string { return replyHanResidueRuns("ja", out) }

		// ① 没有可用上游（env 未接、configs 空 ⇒ ensureLLM 回 nil）
		e.llm = nil
		if _, ok, reason := e.repairHanResidueBase(ctx, nil, "ja", replyRepairTopics(), "", dirty, leaks, 1200, after); ok ||
			reason != rejectNoUpstream {
			t.Fatalf("上游缺失那档应回 %q，实际 ok=%v reason=%q", rejectNoUpstream, ok, reason)
		}
		stub := newSeqStub(t, dirty)
		e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: stub.url, APIKey: "k", Model: "m"}}, 5)

		// ② 残片清单为空（调用方判据与本函数分叉才会走到这里，出现即点名）
		if _, ok, reason := e.repairHanResidueBase(ctx, e.llm, "ja", replyRepairTopics(), "", dirty, nil, 1200, after); ok ||
			reason != rejectNoLeaks {
			t.Fatalf("残片为空那档应回 %q，实际 ok=%v reason=%q", rejectNoLeaks, ok, reason)
		}
		// ③ 残片没减少（桩原样吐回＝现网那条红的形态）
		if _, ok, reason := e.repairHanResidueBase(ctx, e.llm, "ja", replyRepairTopics(), "", dirty, leaks, 1200, after); ok ||
			reason != rejectNotImproved {
			t.Fatalf("未改善那档应回 %q，实际 ok=%v reason=%q", rejectNotImproved, ok, reason)
		}
		// ④ 行数变了（桩回两行；残片确实变少也照样不许采用——行数这条管的是"内容被增删没"）
		stub2 := newSeqStub(t, "費用です\n追加行了")
		if _, ok, reason := e.repairHanResidueBase(ctx, stubClient(stub2), "ja", replyRepairTopics(), "", dirty, leaks, 1200, after); ok ||
			reason != rejectLineCount {
			t.Fatalf("行数那档应回 %q，实际 ok=%v reason=%q", rejectLineCount, ok, reason)
		}
		// ④b 回空正文
		if _, ok, reason := e.repairHanResidueBase(ctx, stubClient(newSeqStub(t, "   ")), "ja", replyRepairTopics(), "", dirty, leaks, 1200, after); ok ||
			reason != rejectEmptyOutput {
			t.Fatalf("空正文那档应回 %q，实际 ok=%v reason=%q", rejectEmptyOutput, ok, reason)
		}
		// ⑤ 上游报错（服务已关掉的地址）
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		deadURL := dead.URL
		dead.Close()
		deadClient := llm.New([]llm.Provider{{Name: "main", BaseURL: deadURL, APIKey: "k", Model: "m"}}, 5)
		if _, ok, reason := e.repairHanResidueBase(ctx, deadClient, "ja", replyRepairTopics(), "", dirty, leaks, 1200, after); ok ||
			reason != rejectUpstreamError {
			t.Fatalf("上游报错那档应回 %q，实际 ok=%v reason=%q", rejectUpstreamError, ok, reason)
		}
		// ⑥ 采用成功时原因必须回空串（别把"没拒绝"也写成一个档名）
		stub3 := newSeqStub(t, "費用のご案内をいたします。")
		if got, ok, reason := e.repairHanResidueBase(ctx, stubClient(stub3), "ja", replyRepairTopics(), "", dirty, leaks, 1200, after); !ok ||
			reason != "" || !strings.Contains(got, "費用") {
			t.Fatalf("采用档应回空原因，实际 ok=%v reason=%q out=%q", ok, reason, got)
		}
	})
}

// jaTenOfOneForms 现网 2026-10-01 那条被拒的 ja 正文里的五个残片，
// **顺序与日志 leaks 字段逐字一致**（实读那行：reason=line_count、before=5、after=4、fixed=文件）：
//
//	"leaks":"料金额,文件,术语库,定价页面,プロfessional"
var jaTenOfOneForms = []string{"料金额", "文件", "术语库", "定价页面", "プロfessional"}

// jaTenOfOneOutOfTable 其中判词为**表外**的四个（文件 已在表内，由上面那条正向锁守住）。
// 四个的逐条判词见 han_residue.go 的 jaResidueFixups 表头（★ 10-01 批），一句话版：
//
//	料金额   ＝ 料金／金額 二选一的半截形态，本地猜不了；
//	术语库   ＝ 用語集／用語ベース／用語データベース 三种说法在自家语料里并存；
//	定价页面 ＝ 価格ページ／料金ページ 两种说法也在自家 ja 文案里并存；
//	プロfessional ＝ 假名嵌拉丁那一族按 093x 口径一律只送补翻。
var jaTenOfOneOutOfTable = []string{"料金额", "术语库", "定价页面", "プロfessional"}

// jaTableKeysHitting 返回表内**哪几条键**会是 frag 的子串。
//
// 为什么"子串"就是判据：applyJaResidueFixups 的落刀方式是 strings.ReplaceAll(out, fp.from, fp.to)，
// 只要某个表内键出现在被点名的片段里，那一段就会被就地改写（前置①问的正是 strings.Contains(leak, from)）。
// 所以"这四个形态表外"真正要锁的东西是：**表里没有任何一条键命中它们**——
// 只锁"当前替换结果"是不够的，将来有人往表里加一条恰好是它们子串的新键（例如 金额→金額），
// 替换结果才会变质，而那种变质是**减一行日志**、不是加一行错误，界面上什么都不会发生。
func jaTableKeysHitting(frag string) []string {
	var hits []string
	for _, fp := range jaResidueFixups {
		if strings.Contains(frag, fp.from) {
			hits = append(hits, fp.from+"→"+fp.to)
		}
	}
	return hits
}

// TestJaResidueFixupsTenOfOneFormsStayOutOfTable ★ 10-01 那五个残片里"表外四个"的负向锁。
//
// 这一条存在的理由不是"它们今天没被改"（那是 applyJaResidueFixups 现行为的副产品），
// 而是**"不许有人把它们改上去"**：这批的判词全部落在"要挑说法"这一档，
// 而挑说法的形态一旦被塞进表（哪怕是以子形态的名义），症状是"日志变干净、屏幕上还是半中半日"，
// 那比原样留残片难发现一个量级（同 AGENTS §一·12 那句"产物能打开不算验收"）。
// 因此本用例的四条断言按"改上去会发生什么"倒着排：先证明现象（判残失明），再锁死入口（键不许命中）。
func TestJaResidueFixupsTenOfOneFormsStayOutOfTable(t *testing.T) {
	// 夹具：五个形态用读点隔开，保证汉字段切分与现网那一行**逐字对齐**（不多不少五个残片）。
	text := "料金额、文件、术语库、定价页面、プロfessionalモードについて"

	leaks := replyHanResidueRuns("ja", text)
	if strings.Join(leaks, ",") != strings.Join(jaTenOfOneForms, ",") {
		t.Fatalf("夹具没能复现现网那一行的五个残片：want=%v got=%v", jaTenOfOneForms, leaks)
	}

	t.Run("只有已在表内的那一个被改掉", func(t *testing.T) {
		got, applied := applyJaResidueFixups("ja", text, leaks)
		if len(applied) != 1 || applied[0] != "文件" {
			t.Fatalf("正字表改动清单应与现网那行 fixed=文件 逐字一致，实际 applied=%v（text=%q）", applied, got)
		}
		if !strings.Contains(got, "ファイル") {
			t.Fatalf("表内的「文件」这一条没生效：%q", got)
		}
	})

	t.Run("四个表外形态原样留着且仍被补翻点名", func(t *testing.T) {
		got, _ := applyJaResidueFixups("ja", text, leaks)
		for _, f := range jaTenOfOneOutOfTable {
			// ③' 原样留着＝这一处只能靠补翻，服务端不替客户写作文
			if !strings.Contains(got, f) {
				t.Fatalf("表外形态 %q 被就地改写了（应原样留给补翻）：%q", f, got)
			}
		}
		// ④' 而且替换后必须**仍然被判残点名**——"表外"不等于"判残漏"：
		// 一旦某处改动让形态离开判残尺子，下一轮补翻就不会再点它的名，坏形态从此静默常驻。
		after := replyHanResidueRuns("ja", got)
		if len(after) != len(jaTenOfOneOutOfTable) {
			t.Fatalf("替换后残片清单长度不对（应恰好剩四个表外形态）：%v", after)
		}
		for _, f := range jaTenOfOneOutOfTable {
			found := false
			for _, a := range after {
				if a == f {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("表外形态 %q 不在替换后的判残清单里 %v——它已经逃出补翻射程", f, after)
			}
		}
	})

	t.Run("负向锁主体：表内任何键都不许是这四个形态的子串", func(t *testing.T) {
		for _, f := range jaTenOfOneOutOfTable {
			if hits := jaTableKeysHitting(f); len(hits) > 0 {
				t.Fatalf("正字表里出现了 %q 的子串键 %v——这条会把需要挑说法的形态做半截替换，"+
					"产物是「第三种谁也没写过的形态」，且判残尺子会因此看不见它（本批 10-01 判词：表外）", f, hits)
			}
		}
	})

	t.Run("反证甲：假想子形态 金额→金額 会把可见残留换成不可见残留", func(t *testing.T) {
		// 这一支就是上面那条负向锁的**反证**（在内存里演一遍，绝不改源、也不往表里塞东西）：
		// 假设有人觉得「额→額 是纯字形之别，收进去总没错」，往表里加了 {金额, 金額}——
		// 手工做同一次替换，看判残尺子还剩什么。
		partial := strings.ReplaceAll(text, "金额", "金額")
		if strings.Contains(partial, "料金额") {
			t.Fatalf("假想替换本身没生效，反证无效：%q", partial)
		}
		still := replyHanResidueRuns("ja", partial)
		for _, a := range still {
			if strings.Contains(a, "料金額") {
				t.Fatalf("夹具假设不成立：「料金額」本该逃过判残，实际仍被点名 %v", still)
			}
		}
		if len(still) >= len(leaks) {
			t.Fatalf("假想子形态理应让残片数下降（否则前置④会拦住，本反证就不必存在），实际 %v vs %v", still, leaks)
		}
		// ⇒ 结论：**四道前置一道都挡不住这条子形态**（残片数确实严格变少了），
		//   挡住它的只能是上面那条"键不许命中"的收录门槛锁。这就是本用例存在的理由。
	})

	t.Run("反证乙：命中判据不是恒假（表内形态必须命中）", func(t *testing.T) {
		// 少了这一支，上面那条负向锁可能是"helper 永远返回空"的空转判据。
		// 「文件」是表内键，jaTableKeysHitting 必须报出它；「费用」同理。
		for _, in := range []string{"文件翻訳", "実際の费用"} {
			if len(jaTableKeysHitting(in)) == 0 {
				t.Fatalf("命中判据对表内形态 %q 报空——那条负向锁是恒真的空锁", in)
			}
		}
		if len(jaTableKeysHitting("定价页面")) != 0 {
			t.Fatalf("表外形态被命中，主判据与逐条判词不一致")
		}
	})
}
