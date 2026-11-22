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

// TestRejectReasonNamesSkeleton B 腿：真实成因必须**点名到具体判据**而不是空串，
// 让诊断日志能区分"骨架拒"与"回显拒"——这正是加日志要解决的归因问题。
// 实测两族：`65–120 万<br>`+纯数字 → IsSkeletonForLang；
// `45–80 万`+纯数字 → isProseInstructionEcho（数字全同被当回显）。两者都属"纯数字格"形态。
func TestRejectReasonNamesSkeleton(t *testing.T) {
	e := &Engine{}
	cases := []struct {
		src, out, want string
	}{
		{"65–120 万<br>", "650,000–1,200,000<br>", "IsSkeletonForLang"},
		{"45–80 万", "450,000–800,000", "isProseInstructionEcho"},
	}
	for _, c := range cases {
		got := e.fileTranslationRejectReason(context.Background(), c.src, c.out, "he")
		if got != c.want {
			t.Errorf("src=%q 纯数字译文应报 %q，实得 %q", c.src, c.want, got)
		}
	}
	// 反向对照：带目标语单位词的形态必须放行（原因空串）。
	if got := e.fileTranslationRejectReason(context.Background(), "45–80 万", "0.45–0.8 מיליון", "he"); got != "" {
		t.Errorf("带单位词的正确译文不该被拒，实得 %q", got)
	}
}
