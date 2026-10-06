// ============ canned_guard_test.go · 职责说明 ============
// 锁 096x-1 那两条首屏防线（机制与现网实证见 canned_guard.go 文件头）：
//
//	① 口径按源文投（translateContract 的两档）；
//	② 落库之前的出栈闸（cannedOutboundReject：残片／凭空多出的品牌名一律不发给访客、不写缓存）。
//
// 每条都配了**反向对照**（该出现的要在，不该出现的没有），因为这个模块的失败形态从来不是报错，
// 是"看起来一切正常、客户屏幕上却是坏文本"。
//
// ★ 用例里的源文与坏形态**全部取自 2026-10-01 现网 12 语种逐个复问的真读数**
//
//	（留档：《发布前E2E_UAT_20260926/证据/1001_08AF_真机往返/现网复问_run2_095x.out》），
//	不是编出来的对抗样本。编样本会锁住一个现网根本没有的形态，白锁。
//
// =============================================
package engine

import (
	"context"
	"strings"
	"testing"
)

// 现网真读数（configs.welcome / configs.quick_chips 的中文原文，逐字抄）
const (
	liveWelcome = "你好，我是能言的 AI 助手 👋 文件翻译、对话翻译、企业术语库、积分充值……有什么想了解的？"
	liveChips   = "怎么上传文件翻译？\n积分怎么收费？\n企业术语库怎么建？\n支持哪些格式？"
)

// TestCannedContractFollowsSource ① 口径按源文投：三档各自可反证。
// 判据问的是**真发出去的那段提示词**，不是函数返回值——本批事故的形态正是"代码改了、口径没变"。
func TestCannedContractFollowsSource(t *testing.T) {
	ctx := context.Background()

	t.Run("原文没提品牌名：给的是禁止句，不是写法邀请", func(t *testing.T) {
		st := newSeqStub(t, "How do I upload a file for translation?\nHow are credits charged?\nHow do I build a corporate terminology library?\nWhich formats are supported?")
		e := st.engine(t)
		_ = e.LocalizeChips(ctx, strings.ReplaceAll(liveChips, "\n", ","), "en")
		p := st.body(0)
		if p == "" {
			t.Fatal("上游一次都没被打，提示词无从核对")
		}
		if strings.Contains(p, "品牌名一律写作") {
			t.Fatalf("原文四条里没有一条提品牌名，却仍给了模型「一律写作 X」那句邀请——这正是现网 ja 每条尾粘「能言」、ru 每条前挂 LangCross 的根因：\n%s", firstRunes(p, 400))
		}
		if !strings.Contains(p, "不许出现") {
			t.Fatalf("没提品牌名时缺了那句**反向禁令**（沉默不等于把许可撤回）：\n%s", firstRunes(p, 400))
		}
		// 占位符规则句也不该出现：源文里没有可换的东西，提它只会让模型自己补一个占位符
		if strings.Contains(p, brandToken) {
			t.Fatalf("源文没有品牌名，提示词里却冒出占位符规则：%s", firstRunes(p, 400))
		}
	})

	t.Run("原文提了品牌名：写法口径与占位符规则都在（092x 那条不许退化）", func(t *testing.T) {
		st := newSeqStub(t, "Hello, I'm the AI assistant of LangCross 👋")
		e := st.engine(t)
		_ = e.LocalizeGreeting(ctx, liveWelcome, "en")
		p := st.body(0)
		if !strings.Contains(p, "品牌名一律写作「LangCross」") {
			t.Fatalf("带品牌名的源文丢了写法口径（现网 ja 把「能言」翻成「能与」就是缺这条时的形态）：%s", firstRunes(p, 400))
		}
		if !strings.Contains(p, brandToken) || !strings.Contains(p, "原样保留") {
			t.Fatalf("品牌名没走过桥占位符：%s", firstRunes(p, 400))
		}
		// 计费单位：这句源文里有「积分充值」，口径必须在
		if !strings.Contains(p, "计费单位口径") {
			t.Fatalf("源文提到「积分」却没给术语口径（现网英文把积分写成 integral 就是这一腿缺位）：%s", firstRunes(p, 400))
		}
	})

	t.Run("原文没提积分：不拼计费单位那句（少一句就够，不需要反向禁令）", func(t *testing.T) {
		st := newSeqStub(t, "Which formats are supported?")
		e := st.engine(t)
		_ = e.localize(ctx, "welcome", "支持哪些格式？", "en", 0)
		if p := st.body(0); strings.Contains(p, "计费单位口径") {
			t.Fatalf("源文没提计费单位却仍拼那句，等于邀请模型补一个 credits 进来：%s", firstRunes(p, 400))
		}
	})

	// ④ 两档口径必须算出**不同指纹**：否则换了口径而旧缓存照命中＝换件没修（082x 那条老账的第四次）。
	brandFp := localizeContract("en", srcTopicsOf(liveWelcome))
	plainFp := localizeContract("en", srcTopicsOf("支持哪些格式？"))
	if brandFp == plainFp {
		t.Fatal("两档口径算出同一个指纹：srcTopicsOf 没进指纹，缓存会跨档误命中")
	}
	if got := srcTopicsOf(liveWelcome); !got.brand || !got.points {
		t.Fatalf("现网欢迎词的 topic 判错（brand=%v points=%v），两条口径都该在", got.brand, got.points)
	}
	if got := srcTopicsOf(liveChips); got.brand {
		t.Fatal("现网 chips 被判成「提到品牌名」——它四条里没有一条有品牌名，这一判错就是注入的入口")
	}
}

// TestCannedGateBlocksResidue ② 残片稿不发给访客、不落缓存（现网 en 首屏的 `credits充值`）。
func TestCannedGateBlocksResidue(t *testing.T) {
	ctx := context.Background()
	st := newSeqStub(t,
		"Hello, I'm LangCross 👋 file translation, credits充值 anytime",
		"Hello, I'm LangCross 👋 file translation, credits充值 still") // 补翻那一枪也没救回来（残片仍在）
	e := st.engine(t)
	got := e.LocalizeGreeting(ctx, liveWelcome, "en")
	if got != liveWelcome {
		t.Fatalf("带残片的译文被发给访客了：%q", got)
	}
	if st.count() != 2 {
		t.Fatalf("应当先补翻一次再判不合格（上游次数 %d）", st.count())
	}
	if cached := e.db.GetConfig("i18n:welcome:en", ""); cached != "" {
		t.Fatalf("被闸挡下的译文还是落了库（下次 greet 直接命中它＝常驻首屏）：%q", firstLine(cached))
	}
}

// TestCannedGateBlocksBrandInjection ② 原文没提品牌名、译文里却有 → 挡（现网 ja chips 尾粘「能言」／ru 前挂 LangCross）。
func TestCannedGateBlocksBrandInjection(t *testing.T) {
	ctx := context.Background()
	st := newSeqStub(t,
		"ファイルアップロード方法は何ですか？能言\nポイントの課金は？能言\n企業用語データベースの作成方法は？能言\nサポートしているフォーマットは？能言")
	e := st.engine(t)
	got := e.LocalizeChips(ctx, strings.ReplaceAll(liveChips, "\n", ","), "ja")
	if got != strings.ReplaceAll(liveChips, "\n", ",") {
		t.Fatalf("每条尾粘品牌名的译文被发给访客了：%q", got)
	}
	if cached := e.db.GetConfig("i18n:chips:ja", ""); cached != "" {
		t.Fatalf("注入稿落了库：%q", firstLine(cached))
	}
	// 反向对照：同一句源文、译文里没有品牌名时**必须放行**，否则闸门是单向的（只会拒绝、不会放行）
	st2 := newSeqStub(t, "ファイルアップロード方法は何ですか？\nポイントの課金はどれくらいですか？\n企業用語データベースの作成方法は？\nサポートしているフォーマットは？")
	e2 := st2.engine(t)
	got2 := e2.LocalizeChips(ctx, strings.ReplaceAll(liveChips, "\n", ","), "ja")
	if !strings.Contains(got2, "ファイル") {
		t.Fatalf("干净译文被闸门误杀（访客退回看中文）：%q", got2)
	}
	if cached := e2.db.GetConfig("i18n:chips:ja", ""); !strings.Contains(cached, "ファイル") {
		t.Fatalf("干净译文没落缓存（每次 greet 白打一次上游）：%q", cached)
	}
	// 第二次必须命中缓存、一次上游都不再打
	if _ = e2.LocalizeChips(ctx, strings.ReplaceAll(liveChips, "\n", ","), "ja"); st2.count() != 1 {
		t.Fatalf("好译文没被缓存复用（上游次数 %d）", st2.count())
	}
}

// TestCannedRepairKeepsPreviousDraft 补翻层单独锁：残片没严格变少就不采用新稿（与出栈层分开）。
//
// 为什么出栈闸上线后还要留这一条：闸门把"带残片的稿子"整条挡成中文原文，端到端就**看不见**
// 补翻层选了哪一稿了（localize_test.go 那两条子用例的期望值正是因此改写）。而"拿一份没验过的新稿
// 覆盖旧稿"这条风险还在（现网把业务数字改坏过一次的就是它），所以必须在层内直接钉住。
func TestCannedRepairKeepsPreviousDraft(t *testing.T) {
	ctx := context.Background()
	src := "你好，我是能言 AI 助手\n积分充值随时开通"
	draft := "Hello, this is LangCross\ncredits充值 anytime"
	// 新稿把「充值」翻对了，却把「积分」原样抄回来 ⇒ 残片数 1→1，不算改善
	st := newSeqStub(t, "Hello, this is LangCross\n积分 recharge anytime")
	e := st.engine(t)
	fixed, ok, reason := e.repairHanResidue(ctx, e.ensureLLM(ctx), "en", src, draft,
		hanResidueRuns("en", src, draft), localizeMaxTokens)
	if ok || fixed != "" {
		t.Fatalf("残片数没减少却采用了新稿：ok=%v fixed=%q", ok, fixed)
	}
	if reason != rejectNotImproved {
		t.Fatalf("拒绝原因应回 %q（日志分档是对外排障契约），实际 %q", rejectNotImproved, reason)
	}
}

// TestCannedGateRejectReasonsAreContract 拒绝分档名是**对外排障契约**，逐字钉（同 094x 那七个 reject*）。
//
// ★ 0AR 第 4 波把这张脸从三档补到六档拒绝＋三档观测，**一条都不许改字面**：
// 现网排障是先 grep 日志里的 reason 再决定动哪一档配置（改模型／抬闸门／调退避窗口），
// 名字一变，那批"照日志行写好的排障动作"就全部指错地方。
func TestCannedGateRejectReasonsAreContract(t *testing.T) {
	want := map[string]string{
		"cannedRejectEmpty":       "canned_empty",
		"cannedRejectResidue":     "canned_han_residue",
		"cannedRejectBrandAdded":  "canned_brand_injected",
		"cannedRejectLineCount":   "canned_line_count",
		"cannedRejectPlaceholder": "canned_placeholder_residue",
		"cannedRejectSeparator":   "canned_separator_residue",
	}
	got := map[string]string{
		"cannedRejectEmpty":       cannedRejectEmpty,
		"cannedRejectResidue":     cannedRejectResidue,
		"cannedRejectBrandAdded":  cannedRejectBrandAdded,
		"cannedRejectLineCount":   cannedRejectLineCount,
		"cannedRejectPlaceholder": cannedRejectPlaceholder,
		"cannedRejectSeparator":   cannedRejectSeparator,
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("出栈闸的拒绝分档名被改了（日志与排障文档会一起失效）：%s 应为 %q，实际 %q", k, v, got[k])
		}
	}
	// 观测档同理逐字钉：它们**不改正文**，所以唯一的露面机会就是这一行名字
	if cannedBrandDropped != "canned_brand_dropped" || cannedScriptImpure != "canned_script_impure" ||
		cannedRepaired != "canned_repaired" {
		t.Fatalf("纯观测/修正档的分档名被改了：%s / %s / %s", cannedBrandDropped, cannedScriptImpure, cannedRepaired)
	}
	// 通过档必须真的回空串＋false（不然是"恒判不合格"那种把首屏永久打回中文的空转闸门）。
	// 第四个返回值 final 也要钉：合格译文经修正腿后**必须逐字等于**入稿——
	// 修正腿对干净稿子应当完全不动手，否则它就成了第二条没人看的改写链。
	const clean = "Which formats are supported?"
	if final, r, d, bad := cannedOutboundReject("en", "支持哪些格式？", clean, srcTopics{}, 0); bad || r != "" || d != "" || final != clean {
		t.Fatalf("合格译文被判拒或被改写：reason=%q detail=%q bad=%v final=%q", r, d, bad, final)
	}
	// 判"凭空多出品牌名"只认在**任何语种都不可能是普通词**的写法：日文档的「能与」不在这张脸上
	// （中文源文里「能够与之」完全正常，收进来就是把好译文判成不合格）
	if f := strayBrandForm("能与、そして"); f != "" {
		t.Fatalf("strayBrandForm 误收了「能与」（中文正文里的正常说法）：%q", f)
	}
	if f := strayBrandForm("Nengyan is great"); f == "" {
		t.Fatal("strayBrandForm 漏了拼音错形")
	}
}

// TestBrandDecorBracketsStripped 现网韩文首屏实证：占位符被模型连名带括号改写成 ⟨LangCross⟩。
func TestBrandDecorBracketsStripped(t *testing.T) {
	ctx := context.Background()
	st := newSeqStub(t, "안녕하세요, 저는 ⟨LangCross⟩의 AI 어시스턴트입니다 👋")
	e := st.engine(t)
	got := e.LocalizeGreeting(ctx, liveWelcome, "ko")
	if strings.ContainsAny(got, "⟨⟩〈〉《》") {
		t.Fatalf("品牌名两侧的装饰括号没摘净（客户会以为尖括号是名字的一部分）：%q", got)
	}
	if !strings.Contains(got, "LangCross") {
		t.Fatalf("摘括号时把品牌名一起吃掉了：%q", got)
	}
	if cached := e.db.GetConfig("i18n:welcome:ko", ""); !strings.Contains(cached, "LangCross") || strings.Contains(cached, "⟨") {
		t.Fatalf("落库的那一份仍带坏形态：%q", cached)
	}
	// 反向对照：括号里除了品牌名还有别的字时**不许动**（那是客户要看的补充说明）
	if got := stripBrandDecorBrackets("详见（LangCross 官网）"); got != "详见（LangCross 官网）" {
		t.Fatalf("误删了带补充说明的括号：%q", got)
	}
}

// TestCannedFingerprintTracksPromptSent 指纹里的口径段＝真发出去的口径段（两条腿不许分叉）。
// 这条是 srcTopicsOf 在 localize 与 translateOnce 各调一次仍能同值的**唯一**机械保证。
func TestCannedFingerprintTracksPromptSent(t *testing.T) {
	ctx := context.Background()
	st := newSeqStub(t, "Hello, I'm LangCross 👋")
	e := st.engine(t)
	_ = e.LocalizeGreeting(ctx, liveWelcome, "en")
	want := srcFingerprint(liveWelcome + "\x00" + localizeContract("en", srcTopicsOf(liveWelcome)))
	cached := e.db.GetConfig("i18n:welcome:en", "")
	if !strings.HasPrefix(cached, want+"\n") {
		t.Fatalf("缓存指纹与本轮真用的口径不是同一份：\n want=%s\n head=%s", want, firstLine(cached))
	}
	// 同一句原文换一档口径（改术语表）＝旧译文作废：这是 082x 那条"换件没修"的正身
	old := pointsTermByLang["en"]
	t.Cleanup(func() { pointsTermByLang["en"] = old })
	pointsTermByLang["en"] = "creditsRenewed096x"
	_ = e.LocalizeGreeting(ctx, liveWelcome, "en")
	if st.count() != 2 {
		t.Fatalf("术语口径变了却没重翻（上游次数 %d）——现网英文首屏念 integral 就是这个形态", st.count())
	}
}
