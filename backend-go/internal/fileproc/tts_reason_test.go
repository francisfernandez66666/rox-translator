// ============ tts_reason_test.go · 职责说明 ============
// M8（2026-10-21 UAT 收口）：朗读失败的**分档名**是对外排障契约，前端按 reason 取 12 语种词典。
//
// 本文件锁三件事：
//
//	R1 六个档位字面量逐字固定（改名式破坏必须当场红）；
//	R2 deadline（子进程或 ctx 任一）⇒ timeout，客户端取消 ⇒ failed；
//	R3 TtsReasonOf 能从包裹后的错误取回档位（调用方不再做字符串匹配）。
//
// 反证：把 ttsSubprocessError 的 deadline 分支摘掉，R2 立即红。
// =============================================
package fileproc

import (
	"context"
	"errors"
	"testing"
)

func TestTtsReasonNamesArePinned(t *testing.T) {
	want := map[string]string{
		TtsReasonEmpty:             "empty",
		TtsReasonTooLong:           "too_long",
		TtsReasonCapabilityMissing: "capability_missing",
		TtsReasonUnsupportedLang:   "unsupported_lang",
		TtsReasonFailed:            "failed",
		TtsReasonTimeout:           "timeout",
	}
	for got, exp := range want {
		if got != exp {
			t.Fatalf("TTS 分档名漂移：实得 %q，应为 %q（前端词典与排障口径都按字面量取词）", got, exp)
		}
	}
}

func TestTtsSubprocessErrorTimeoutIsDistinct(t *testing.T) {
	te := ttsSubprocessError(context.Background(), context.DeadlineExceeded, "")
	if te.Reason != TtsReasonTimeout {
		t.Fatalf("子进程 deadline 必须归 timeout，实得 %#v", te)
	}
	// ctx 自己过期（err 被 runSubprocess 包成别的形态）也必须判超时。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if te := ttsSubprocessError(ctx, errors.New("signal: killed"), ""); te.Reason != TtsReasonFailed {
		t.Fatalf("context.Canceled 属于「客户端放弃」，仍应归 failed；实得 %q", te.Reason)
	}
	dctx, dcancel := context.WithTimeout(context.Background(), -1)
	defer dcancel()
	if te := ttsSubprocessError(dctx, errors.New("signal: killed"), ""); te.Reason != TtsReasonTimeout {
		t.Fatalf("ctx 已过期应归 timeout，实得 %q", te.Reason)
	}
}

func TestTtsReasonOfUnwrapsTimeout(t *testing.T) {
	base := &TTLError{Reason: TtsReasonTimeout, Message: "TTS 合成超时，请稍后重试", Err: context.DeadlineExceeded}
	wrapped := errors.Join(errors.New("outer"), base)
	if got := TtsReasonOf(wrapped); got != TtsReasonTimeout {
		t.Fatalf("包裹后的超时错误应取回 timeout，实得 %q", got)
	}
	if got := TtsReasonOf(nil); got != "" {
		t.Fatalf("nil 应回空串，实得 %q", got)
	}
}
