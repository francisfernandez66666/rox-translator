// 改进1回归测试：实时计费余额不足中止原因（abortReasonFrom）
//   - WithUsageRecorder 注入的 abort 需记录 store.ErrInsufficientBalance 原因
//   - 未中止/普通取消/超时返回 ""
//   - 对话翻译据此在回复中提示「余额不足」，不再静默缺失语言
package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"translator/internal/llm"
	"translator/internal/store"
)

// TestAbortReasonFromCancelled 普通取消/超时不算余额中止
func TestAbortReasonFromCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := abortReasonFrom(ctx); got != "" {
		t.Fatalf("普通取消应返回空，实际 %q", got)
	}
	deadline, df := context.WithTimeout(context.Background(), time.Nanosecond)
	defer df()
	<-deadline.Done()
	if got := abortReasonFrom(deadline); got != "" {
		t.Fatalf("超时应返回空，实际 %q", got)
	}
	if got := abortReasonFrom(nil); got != "" {
		t.Fatalf("nil ctx 应返回空，实际 %q", got)
	}
}

// TestAbortReasonFromInsufficientBalance 余额不足中止应返回面向用户的文案
func TestAbortReasonFromInsufficientBalance(t *testing.T) {
	e := &Engine{}
	ctx := e.WithUsageRecorder(context.Background())
	if abort := llm.AbortFromCtx(ctx); abort != nil {
		abort()
	}
	if got := abortReasonFrom(ctx); got == "" {
		t.Fatalf("余额不足中止应返回提示，实际为空")
	} else if !errors.Is(context.Cause(ctx), store.ErrInsufficientBalance) {
		t.Fatalf("中止原因应为 ErrInsufficientBalance，实际 %v", context.Cause(ctx))
	}
}
