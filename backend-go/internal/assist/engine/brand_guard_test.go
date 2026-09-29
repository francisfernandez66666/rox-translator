// brand_guard_test.go — ★ 092x 红腿三（2026-09-29 现网复问第四条：日文轮把品牌写成「能与」）。
//
// 这条链要锁的是**保证**那一半：提示词里早就写了「一律写作『能言』」，线上照样翻车，
// 所以断言不能问"提示词有没有那句话"（那是请求），必须问"错形送进来，出站时还在不在"。
// 反向对照同样重要：中文正文里的「能与」是正常词（「能与你」），把它改掉就是误改客户正文——
// 那一腿不钉住，这道守卫就是一个随时会吃掉答案的开关。
package engine

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizeBrandFormsByLocale(t *testing.T) {
	cases := []struct {
		lang string
		in   string
		want string
		n    int
	}{
		// 拼音错形：任何语种档都改（拼音在哪一档都不是词），且改成本档写法
		{"ja", "Nengyan へようこそ", "能言 へようこそ", 1},
		{"en", "Welcome to NengYan", "Welcome to LangCross", 1},
		{"zh", "欢迎使用 nengyan", "欢迎使用 能言", 1},
		{"ru", "NENGYAN", "LangCross", 1},
		// 日文轮的字形近错（现网实证形态）
		{"ja", "能与のAIアシスタントです", "能言のAIアシスタントです", 1},
		// ★ 反向锁：中文档绝不改「能与」——「能与你」是正常中文，改掉就是误伤客户正文
		{"zh", "这个功能能与您现有系统对接", "这个功能能与您现有系统对接", 0},
		{"zh_hant", "該功能能與現有系統對接", "該功能能與現有系統對接", 0},
		// 英文档里出现「能与」：那是访客贴的中文材料或语种判错，不在本表射程（宁可不改）
		{"en", "能与 vs 能言", "能与 vs 能言", 0},
		// 正确写法一律原样
		{"ja", "能言のAIアシスタントです", "能言のAIアシスタントです", 0},
		{"en", "This is LangCross.", "This is LangCross.", 0},
	}
	for _, c := range cases {
		got, n := normalizeBrandForms(c.in, c.lang)
		if got != c.want || n != c.n {
			t.Errorf("normalizeBrandForms(%q, %q) = (%q, %d)，期望 (%q, %d)", c.in, c.lang, got, n, c.want, c.n)
		}
	}
	// 一处都不该改的正文必须**逐字节原样**返回（守卫不许顺手重写、不许收空白）
	plain := "文件支持 PDF、Word，最大 40MB。"
	if got, n := normalizeBrandForms(plain, "ja"); got != plain || n != 0 {
		t.Fatalf("普通正文被改动：%q → %q（%d）", plain, got, n)
	}
}

func TestProtectAndRestoreBrandAcrossTranslation(t *testing.T) {
	// ① 送翻前：中文与英文两种写法都要换成占位符，源文里不许留下任何一种品牌字面
	src := "你好，我是能言（LangCross）AI 助手"
	got, hit := protectBrandForTranslation(src)
	if !hit || strings.Contains(got, "能言") || strings.Contains(got, "LangCross") {
		t.Fatalf("品牌名没被占位符护住：%q", got)
	}
	if strings.Count(got, brandToken) != 2 {
		t.Fatalf("两处品牌写法应各自换成一个占位符，实际：%q", got)
	}
	// ② 占位符规则句只在源文真含品牌时才出现
	if brandTokenRuleLine(false) != "" {
		t.Fatal("白话源文的提示词里不该冒出品牌占位符规则")
	}
	if !strings.Contains(brandTokenRuleLine(true), brandToken) {
		t.Fatal("规则句没写出占位符本身，模型无从保留它")
	}
	// ③ 出栈还原：占位符在 → 按语种档还原
	if r, ok := restoreBrandAfterTranslation("こんにちは、⟦BRAND⟧のAIアシスタントです", "ja"); !ok || !strings.Contains(r, "能言") {
		t.Fatalf("日文档没还原成「能言」：%q", r)
	}
	if r, ok := restoreBrandAfterTranslation("Hello, ⟦BRAND⟧ here", "en"); !ok || !strings.Contains(r, "LangCross") {
		t.Fatalf("英文档没还原成 LangCross：%q", r)
	}
	// ④ 模型没留占位符、但把名字照抄下来了 → 仍算成功，并按语种归一
	if r, ok := restoreBrandAfterTranslation("能与 です", "ja"); !ok || !strings.Contains(r, "能言") {
		t.Fatalf("无占位符时应按品牌形态归一，实际：%q ok=%v", r, ok)
	}
	// ⑤ 整块吃掉：占位符与任何品牌痕迹都不在 ⇒ 仍出译文、只记 WARN（判失败会让访客退回看中文，
	// 那是拿更重的「语言保证」去换一个字面上的自称缺失，见函数注释）
	if r, ok := restoreBrandAfterTranslation("こんにちは、アシスタントです", "ja"); ok || r != "こんにちは、アシスタントです" {
		t.Fatalf("无品牌痕迹时应原样出译文并回 ok=false，实际：%q ok=%v", r, ok)
	}
	// ⑤b 模型把占位符改坏成半角方括号：残渣必须清掉，客户屏幕上不许出现 [BRAND]
	if got := stripBrandTokenResidue("こんにちは、[BRAND]です"); got != "こんにちは、です" {
		t.Fatalf("占位符残渣没剥净：%q", got)
	}
	if got := stripBrandTokenResidue("Please brand your file"); got != "Please brand your file" {
		t.Fatalf("stripBrandTokenResidue 误伤了正文里的英文单词：%q", got)
	}
	// ⑥ 还原之后不许再有占位符漏到客户屏幕上
	for _, lang := range []string{"ja", "en", "zh", "ru", "th"} {
		r, _ := restoreBrandAfterTranslation("line with "+brandToken+" inside", lang)
		if strings.Contains(r, "⟦") || strings.Contains(r, "BRAND") {
			t.Fatalf("%s 档还原后仍留占位符残渣：%q", lang, r)
		}
	}
}

// TestTranslateOnceBrandSurvivesMangling 端到端一条：上游把品牌名翻坏成「能与」，
// 出站必须仍是「能言」——这条才是"保证"，提示词那一半只是请求。
func TestTranslateOnceBrandSurvivesMangling(t *testing.T) {
	ctx := context.Background()
	src := "你好，我是能言 AI 助手"

	// 假上游看到的是占位符，回来的译文却把它意译成了「能与」
	st := newSeqStub(t, "こんにちは、能与のAIアシスタントです")
	e := st.engine(t)
	if got := e.LocalizeGreeting(ctx, src, "ja"); !strings.Contains(got, "能言") || strings.Contains(got, "能与") {
		t.Fatalf("品牌名被模型翻坏却原样送出了：%q", got)
	}
	// 送出去的提示词里不许有「能言」两个字（护住了才有还原的依据）
	if p := st.body(0); strings.Contains(p, "能言 AI") || !strings.Contains(p, brandToken) {
		t.Fatalf("翻译提示词没把品牌名换成占位符：\n%s", p)
	}

	// 上游把占位符整块吃掉 ⇒ **不判失败**：访客仍拿到自己语言的那条回答，
	// 只是自称缺失，由 WARN 记账（判失败＝英文访客退回看中文，比少个品牌名严重得多）
	st2 := newSeqStub(t, "こんにちは、AIアシスタントです")
	e2 := st2.engine(t)
	got2 := e2.LocalizeGreeting(ctx, src, "ja")
	if got2 != "こんにちは、AIアシスタントです" {
		t.Fatalf("品牌名整块丢失时应仍出译文（只记 WARN），实际：%q", got2)
	}
	if strings.Contains(got2, "BRAND") || strings.Contains(got2, "⟦") {
		t.Fatalf("占位符残渣漏到客户屏幕上：%q", got2)
	}
	// 反过来：占位符被模型换成半角形态时也不许漏
	st3 := newSeqStub(t, "こんにちは、[BRAND]のAIアシスタントです")
	e3 := st3.engine(t)
	if got3 := e3.LocalizeGreeting(ctx, src, "ja"); strings.Contains(got3, "BRAND") {
		t.Fatalf("改坏的占位符没被剥掉：%q", got3)
	}
}

// TestGuardReplyBrandOnEveryReply 出站咽喉那条腿：与是否经过翻译无关，canned／兜底同样过。
func TestGuardReplyBrandOnEveryReply(t *testing.T) {
	e := &Engine{}
	ctx := context.Background()

	for _, src := range []string{"canned", "fallback", "llm", "flow", ""} {
		rep := &Reply{Content: "Nengyanのご案内です", Source: src}
		got := e.guardReplyBrand(ctx, "ja", rep).Content
		if strings.Contains(got, "Nengyan") {
			t.Errorf("%s 出处的回复没归一品牌名：%q", src, got)
		}
	}
	// 未改动时正文逐字节不变（守卫不许顺手改空白或标点）
	keep := "能与ではなく能言と書いてください"
	rep := &Reply{Content: keep, Source: "script"}
	if got := e.guardReplyBrand(ctx, "ja", rep).Content; got != "能言ではなく能言と書いてください" {
		t.Fatalf("日文轮应把两处品牌形态都归一，实际：%q", got)
	}
	// 中文轮原样不动
	zh := "这套流程能与您现有系统对接"
	if got := e.guardReplyBrand(ctx, "zh", &Reply{Content: zh}).Content; got != zh {
		t.Fatalf("中文轮误改了「能与」：%q", got)
	}
}
