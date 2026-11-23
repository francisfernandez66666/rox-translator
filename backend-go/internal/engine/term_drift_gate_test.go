// ============ term_drift_gate_test.go · 职责说明（2026-11-23 用户令三项升级之一）============
// 术语漂移硬闸锁：漂移段必须产出 WARN（不静默交付）；无漂移段零告警、译文逐字节不动。
// 重译腿（retry=true）依赖模型，不在此测——采纳门槛由 RunWithTerms/CheckTermConsistency
// 两把既有尺子锁，本文件只锁"漂移必出声、不漂不动"。
// ================================================
package engine

import (
	"context"
	"strings"
	"testing"

	"translator/internal/gate"
)

func TestTermDriftHardGate(t *testing.T) {
	gc := &gateCtx{e: &Engine{}}
	snap := []gate.TermRequirement{
		{Source: "线索", Target: "ליד", FromHighfreq: true},
	}
	ctx := context.Background()

	// 正向：漂移（把线索译成 הקלטות）必须出声
	tr, warnings := gc.enforceTermDrift(ctx, gc.e, "线索靠客户主动留资", "he", "הקלטות מתבססות על לקיחת פעולה", false, nil, snap)
	if tr != "הקלטות מתבססות על לקיחת פעולה" {
		t.Fatalf("retry=false 时译文必须原样保留（重译交给上层），实得 %q", tr)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "术语一致性") {
		t.Fatalf("漂移段必须产出术语一致性 WARN：%+v", warnings)
	}

	// 反向：已含规定译法 ⇒ 无告警、译文不动
	tr2, w2 := gc.enforceTermDrift(ctx, gc.e, "线索靠客户主动留资", "he", "הליד נשען על לקוח", false, nil, snap)
	if tr2 != "הליד נשען על לקוח" || len(w2) != 0 {
		t.Fatalf("无漂移段不该有告警：%q %+v", tr2, w2)
	}

	// 反向：源文不含术语 ⇒ 不射程
	tr3, w3 := gc.enforceTermDrift(ctx, gc.e, "第二行原文", "he", "שורה שנייה", false, nil, snap)
	if tr3 != "שורה שנייה" || len(w3) != 0 {
		t.Fatalf("源文无术语不该有告警：%q %+v", tr3, w3)
	}
}
