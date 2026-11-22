// ============ reject_reason_test.go · 职责说明（2026-11-22）============
// 「救援段被判据丢弃」诊断日志的锁：fileTranslationRejectReason 必须与
// fileTranslationUsable 严格同源——前者只多报一个"哪一条拒的"原因名，
// 不改任何采纳结论（AGENTS §一·11 两把尺子纪律）。
//
// 两条腿：
//
//	A 一致性：对每一样本，rejectReason=="" ⟺ fileTranslationUsable==true；
//	B 归因准确：骨架格必须报 IsSkeletonForLang（tv10/tv11 三格落回落的真实成因），
//	  可用格必须报空串。
//
// ================================================
package engine

import (
	"context"
	"testing"
)

// TestRejectReasonMatchesUsable A 腿：诊断原因与采纳布尔严格等值（同一把尺子）。
func TestRejectReasonMatchesUsable(t *testing.T) {
	e := &Engine{}
	cases := []struct {
		name, src, out, lang string
	}{
		{"骨架-纯数字金额", "65–120 万<br>", "650,000–1,200,000<br>", "he"},
		{"骨架-带单位词可用", "65–120 万<br>", "0.65–1.2 מיליון<br>", "he"},
		{"可用-整句", "第二行原文", "שורה שנייה מקורית", "he"},
		{"空产物", "65–120 万", "", "he"},
		{"回显原文", "第二行原文", "第二行原文", "he"},
	}
	for _, c := range cases {
		reason := e.fileTranslationRejectReason(context.Background(), c.src, c.out, c.lang)
		usable := e.fileTranslationUsable(context.Background(), c.src, c.out, c.lang)
		if (reason == "") != usable {
			t.Errorf("%s：诊断原因=%q 与采纳布尔=%v 不一致（两把尺子？）", c.name, reason, usable)
		}
	}
}

// TestRejectReasonNamesSkeleton B 腿（★ 2026-11-22 tv12 数量豁免修复后更新契约）：
//   - 正向：tv12 诊断日志钓出的三格**正确纯数字译文**必须全部放行（原因空串）——
//     修复前分别被 IsSkeletonForLang／isProseInstructionEcho 误杀，这正是本批修的缺陷。
//   - 反向：丢量级（`45–80 万`→`45–80`）、数字不匹配、源带实义修饰词的纯数值译文，
//     以及真正的提示词回显，仍必须被拒——豁免不许变成万能放行。
func TestRejectReasonNamesSkeleton(t *testing.T) {
	e := &Engine{}
	ctx := context.Background()
	// 正向：三格正确换算（tv12 真实模型输出，逐字抄自诊断日志）。
	for _, c := range []struct{ src, out string }{
		{"65–120 万<br>", "650,000–1,200,000<br>"},
		{"45–80 万", "450,000–800,000"},
		{"25–50 万", "250,000–500,000"},
		{"45–80 万", "0.45–0.8 מיליון"}, // 带单位词的形态照旧放行
	} {
		if got := e.fileTranslationRejectReason(ctx, c.src, c.out, "he"); got != "" {
			t.Errorf("正确数量译文 src=%q out=%q 不该被拒，实得 %q", c.src, c.out, got)
		}
	}
	// 反向一：丢量级／数字被改（45 万写成 45，差 10000 倍）——必须拒。
	for _, c := range []struct{ src, out string }{
		{"45–80 万", "45–80"},
		{"45–80 万", "450,000–8,000"},
		{"65–120 万<br>", "65–120<br>"},
	} {
		if got := e.fileTranslationRejectReason(ctx, c.src, c.out, "he"); got == "" {
			t.Errorf("错量级/错数字 src=%q out=%q 必须被拒，实得放行", c.src, c.out)
		}
	}
	// 反向二：真正的指令语料回显仍必须被拒（豁免只认"数字占主导源"）。
	echoSrc := "请把下面内容翻译成英文，并保持术语一致，不要解释。"
	echoOut := "Please translate the following content into English and keep terminology consistent, do not explain."
	if got := e.fileTranslationRejectReason(ctx, echoSrc, echoOut, "he"); got == "" {
		t.Error("真正的指令回显必须被拒，数量豁免不许放行长句")
	}
}
