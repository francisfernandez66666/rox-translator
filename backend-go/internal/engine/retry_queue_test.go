// translateLangsConcurrent 轮次化重试队列测试（2026-09-09 可靠性改造回归）：
//   - 失败语言排队尾重试、最多尝试 maxAttempts 次
//   - 成功语言只调用一次
//   - 3 次仍失败则输出置空（由上层漏译率硬闸兜底）
//   - 上下文取消立即退出、不再重试
package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/kb"
)

// retryTestEngine 构造带 fake 单语翻译器与短退避的 Engine（避免测试耗时）。
func retryTestEngine(fn func(lang string, attempt int) (string, error)) (*Engine, *sync.Mutex, map[string]int) {
	calls := map[string]int{}
	var mu sync.Mutex
	e := &Engine{
		singleLangFn: func(_ context.Context, _ string, targetLang string, _ []*kb.Row, _, _ string) (string, error) {
			mu.Lock()
			calls[targetLang]++
			attempt := calls[targetLang]
			mu.Unlock()
			return fn(targetLang, attempt)
		},
		retryMaxAttempts: 3,
		retrySleep:       time.Millisecond, // 测试用：轮间退避压到 1ms
	}
	return e, &mu, calls
}

// TestTranslateRetryQueueFailedLangRetries 失败语言排队尾重试至第 3 次
func TestTranslateRetryQueueFailedLangRetries(t *testing.T) {
	e, mu, calls := retryTestEngine(func(lang string, attempt int) (string, error) {
		return "", errors.New("boom") // 永远失败
	})
	out := map[string]string{}
	e.translateLangsConcurrent(context.Background(), "你好", []string{"ar", "ru"}, nil, out, "zh", "ai_initial")
	mu.Lock()
	defer mu.Unlock()
	// ar/ru 各应尝试 3 次后放弃，输出为空
	if calls["ar"] != 3 || calls["ru"] != 3 {
		t.Fatalf("失败语言应各尝试 3 次，实际 ar=%d ru=%d", calls["ar"], calls["ru"])
	}
	if out["ar"] != "" || out["ru"] != "" {
		t.Fatalf("3 次失败后输出应为空，实际 ar=%q ru=%q", out["ar"], out["ru"])
	}
}

// TestTranslateRetryQueueSuccessStops 语言成功则不再尝试
func TestTranslateRetryQueueSuccessStops(t *testing.T) {
	e, mu, calls := retryTestEngine(func(lang string, attempt int) (string, error) {
		if lang == "ar" {
			return "", errors.New("boom") // ar 永远失败
		}
		return "translated-" + lang, nil // ru 一次成功
	})
	out := map[string]string{}
	e.translateLangsConcurrent(context.Background(), "你好", []string{"ru", "ar"}, nil, out, "zh", "ai_initial")
	mu.Lock()
	defer mu.Unlock()
	if calls["ru"] != 1 {
		t.Fatalf("成功语言应只调用 1 次，实际 %d", calls["ru"])
	}
	if calls["ar"] != 3 {
		t.Fatalf("失败语言应重试至 3 次，实际 %d", calls["ar"])
	}
	if out["ru"] != "translated-ru" {
		t.Fatalf("成功语言输出应写入，实际 %q", out["ru"])
	}
	if out["ar"] != "" {
		t.Fatalf("失败语言输出应为空，实际 %q", out["ar"])
	}
}

// TestTranslateRetryQueueRecoversOnLaterAttempt 第 2 轮成功则恢复
func TestTranslateRetryQueueRecoversOnLaterAttempt(t *testing.T) {
	e, mu, calls := retryTestEngine(func(lang string, attempt int) (string, error) {
		if attempt == 1 {
			return "", errors.New("transient") // 首轮失败
		}
		return "recovered-" + lang, nil // 第 2 轮成功
	})
	out := map[string]string{}
	e.translateLangsConcurrent(context.Background(), "你好", []string{"ar"}, nil, out, "zh", "ai_initial")
	mu.Lock()
	defer mu.Unlock()
	if calls["ar"] != 2 {
		t.Fatalf("应在第 2 次尝试时成功，实际调用 %d 次", calls["ar"])
	}
	if !strings.HasPrefix(out["ar"], "recovered-") {
		t.Fatalf("恢复后输出应写入，实际 %q", out["ar"])
	}
}

// TestTranslateRetryQueueCancelExitsImmediately 上下文取消立即退出
func TestTranslateRetryQueueCancelExitsImmediately(t *testing.T) {
	e, _, _ := retryTestEngine(func(lang string, attempt int) (string, error) {
		return "", errors.New("boom")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	out := map[string]string{}
	start := time.Now()
	e.translateLangsConcurrent(ctx, "你好", []string{"ar", "ru"}, nil, out, "zh", "ai_initial")
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("取消后应立即返回，实际耗时 %v", time.Since(start))
	}
}

// TestTranslateRetryQueueSkipsExistingOut 已翻译语言跳过
func TestTranslateRetryQueueSkipsExistingOut(t *testing.T) {
	e, mu, calls := retryTestEngine(func(lang string, attempt int) (string, error) {
		return "x-" + lang, nil
	})
	out := map[string]string{"ru": "已有译文"}
	e.translateLangsConcurrent(context.Background(), "你好", []string{"ru", "ar"}, nil, out, "zh", "ai_initial")
	mu.Lock()
	defer mu.Unlock()
	if calls["ru"] != 0 {
		t.Fatalf("已翻译语言不应再调用，实际 %d", calls["ru"])
	}
	if out["ru"] != "已有译文" {
		t.Fatalf("已翻译语言输出不应被覆盖，实际 %q", out["ru"])
	}
}
