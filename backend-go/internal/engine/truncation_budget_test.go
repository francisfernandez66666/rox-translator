// ============ truncation_budget_test.go 职责中文说明 ============
// 2026-10-15 工作台「译文中间断掉」修复的回归锁。三条缺陷各自一条：
//   A. singleLangRaw 的「截断重试翻倍」是死代码：它在 length 分支里对局部 maxTokens 做乘法，
//      而递归自调会把预算从 zhText 重算一遍 ⇒ 翻倍被抹平，重试仍是同一配额。锁：重试请求的
//      max_tokens 必须真的翻倍（本用例读假上游收到的 max_tokens 字段，不看注释）。
//   B. ReviewTranslation 会**覆盖**客户手上的初翻，却把 max_tokens 写死 2048 且丢掉 finish_reason
//      ⇒ 长译文的审校输出被截断后照样覆盖 ⇒ 客户拿到比初翻更短、中间断掉的译文。
//      锁：finish_reason=length ⇒ 返回 ""（调用方保留初翻）；配正向对照（stop ⇒ 照采）。
//   C. translateWithFeedbackEx（改写腿）同 B：同样覆盖上一版、同样写死 2048。
// 全程 httptest 假上游（零真实 LLM 调用）、自钉 sqlite 方言（AGENTS §一·4）。
// ========================================
package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/config"
)

// truncCall 记一次假上游收到的读数。
type truncCall struct {
	kind      string // 腿名（复用 purityPromptKind）
	maxTokens int    // 请求体里的 max_tokens
	finish    string // 本次回执的 finish_reason
}

// truncUpstream 假上游：按提示词分腿回话，并记录每次的 max_tokens 与 finish_reason。
type truncUpstream struct {
	mu    sync.Mutex
	calls []truncCall
	reply func(prompt string, callIdx int) (content, finish string)
}

// truncResp 把文本与 finish_reason 包成 OpenAI 兼容回执（与 purity 的 chatOpenAIResp 同形，
// 只差 finish_reason 可控——B/C 两条锁问的就是这个字段）。
func truncResp(content, finish string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": finish,
		}},
	})
	return string(b)
}

// truncEngine 造一台「假上游按腿回话 + 可读 max_tokens」的引擎。
func truncEngine(t *testing.T, reply func(prompt string, callIdx int) (string, string)) (*Engine, *truncUpstream) {
	t.Helper()
	up := &truncUpstream{reply: reply}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
			Messages  []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		var joined strings.Builder
		for _, m := range body.Messages {
			joined.WriteString(m.Content)
			joined.WriteString("\n")
		}
		prompt := joined.String()
		kind := purityPromptKind(prompt)

		up.mu.Lock()
		idx := len(up.calls)
		content, finish := up.reply(prompt, idx)
		up.calls = append(up.calls, truncCall{kind: kind, maxTokens: body.MaxTokens, finish: finish})
		up.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, truncResp(content, finish))
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS §一·4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	e := NewEngine(cfg, nil, nil, nil)
	e.breaker = NewBreaker(3, time.Minute)
	return e, up
}

// callsByKind 取某一腿的全部读数（调用方已把 mu 内联读，这里只做筛选拷贝）。
func (u *truncUpstream) callsByKind(kind string) []truncCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []truncCall
	for _, c := range u.calls {
		if c.kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// TestSingleLangTruncatedRetryActuallyDoublesBudget A 锁：initial 腿第一次被截断（length），
// 重试请求的 max_tokens 必须真的翻倍（2048 → 4096），而不是注释里写着翻倍、实到同一配额。
func TestSingleLangTruncatedRetryActuallyDoublesBudget(t *testing.T) {
	const src = "本设备的维护周期与校准规范说明文件" // 17 rune：>12（关掉短格爆炸闸）、≤20（关掉截断自修复）
	const partial = "This device"
	const full = "This device requires periodic maintenance and calibration according to the specification document."

	e, up := truncEngine(t, func(prompt string, idx int) (string, string) {
		if purityPromptKind(prompt) == "initial" && idx == 0 {
			return partial, "length"
		}
		if purityPromptKind(prompt) == "initial" {
			return full, "stop"
		}
		return full, "stop"
	})

	out, err := e.singleLangRaw(context.Background(), src, "en", nil, "zh", "", 0)
	if err != nil {
		t.Fatalf("截断重试后应正常返回，got err=%v", err)
	}
	if !strings.Contains(out, "periodic maintenance") {
		t.Fatalf("重试产物没被采纳：%q", out)
	}
	initial := up.callsByKind("initial")
	if len(initial) != 2 {
		t.Fatalf("截断后应恰好重试一次（共 2 次 initial 调用），实得 %d：%+v", len(initial), initial)
	}
	if initial[0].maxTokens != 2048 {
		t.Fatalf("首译预算应为基础档 2048，实得 %d", initial[0].maxTokens)
	}
	if initial[1].maxTokens != 4096 {
		t.Fatalf("★ 重试预算没有真翻倍（旧死代码形态）：期望 4096，实得 %d —— attempt 没有进预算推导", initial[1].maxTokens)
	}
}

// TestReviewTruncatedOutputKeptFromOverwritingInitial B 锁：审校腿回 finish_reason=length
// ⇒ 必须返回 ""（调用方保留初翻），不得把半截审校产物覆盖上去。
func TestReviewTruncatedOutputKeptFromOverwritingInitial(t *testing.T) {
	const initial = "弊社はスマートハードウェアの研究開発に専念します。"
	const half = "弊社はスマートハードウェアの研究開発に専念"

	// 截断档：finish_reason=length
	e, _ := truncEngine(t, func(prompt string, idx int) (string, string) {
		if purityPromptKind(prompt) == "review" {
			return half, "length"
		}
		return initial, "stop"
	})
	if got := e.ReviewTranslation(context.Background(), "本公司专注于智能硬件的研发与制造", initial, "ja", config.StageReview); got != "" {
		t.Fatalf("★ 被截断的审校产物被当成修正版返回，会覆盖完整初翻：%q", got)
	}

	// 正向对照：同内容 finish_reason=stop ⇒ 必须照采（否则上面那条也可能是「恒拦」的假绿）
	const revised = "弊社はスマートハードウェアの研究開発に専念しております。"
	e2, _ := truncEngine(t, func(prompt string, idx int) (string, string) {
		if purityPromptKind(prompt) == "review" {
			return revised, "stop"
		}
		return initial, "stop"
	})
	if got := e2.ReviewTranslation(context.Background(), "本公司专注于智能硬件的研发", initial, "ja", config.StageReview); got != revised {
		t.Fatalf("合格审校产物没被采纳：%q", got)
	}
}

// TestFeedbackTruncatedOutputKeptFromOverwritingPrevious C 锁：改写腿（同样覆盖上一版）
// 回 finish_reason=length ⇒ 返回 ""，配 stop 档正向对照。
func TestFeedbackTruncatedOutputKeptFromOverwritingPrevious(t *testing.T) {
	const src = "本公司专注于智能硬件的研发与制造"
	const prev = "弊社はスマートハードウェアの研究開発に専念します。"
	const half = "弊社はスマートハードウェアの研究開発に専念"

	e, _ := truncEngine(t, func(prompt string, idx int) (string, string) {
		if purityPromptKind(prompt) == "feedback" {
			return half, "length"
		}
		return prev, "stop"
	})
	if got := e.translateWithFeedbackEx(context.Background(), src, "ja", "请压缩", config.StageReview, nil); got != "" {
		t.Fatalf("★ 被截断的改写产物被当成修正版返回，会覆盖上一版：%q", got)
	}

	const revised = "弊社はスマートハードウェアの研究開発に専念しております。"
	e2, _ := truncEngine(t, func(prompt string, idx int) (string, string) {
		if purityPromptKind(prompt) == "feedback" {
			return revised, "stop"
		}
		return prev, "stop"
	})
	if got := e2.translateWithFeedbackEx(context.Background(), src, "ja", "请压缩", config.StageReview, nil); got != revised {
		t.Fatalf("合格改写产物没被采纳：%q", got)
	}
}

// TestReviewBudgetScalesWithTranslation B 锁的另一半：审校腿预算必须随待审校译文长度放大，
// 而不是恒 2048（旧写死值）。用一段长译文读假上游收到的 max_tokens 直接断言。
func TestReviewBudgetScalesWithTranslation(t *testing.T) {
	longTr := strings.Repeat("弊社はスマートハードウェアの研究開発に専念します。", 200)
	want := translateOutputBudget(longTr)
	if want <= 2048 {
		t.Fatalf("夹具没造出足够长的译文，预算读数 %d 未触发放大", want)
	}
	e, up := truncEngine(t, func(prompt string, idx int) (string, string) {
		if purityPromptKind(prompt) == "review" {
			return longTr, "stop"
		}
		return longTr, "stop"
	})
	_ = e.ReviewTranslation(context.Background(), "原文", longTr, "ja", config.StageReview)
	rev := up.callsByKind("review")
	if len(rev) != 1 {
		t.Fatalf("应恰好一次审校调用，实得 %d", len(rev))
	}
	if rev[0].maxTokens != want {
		t.Fatalf("审校预算没随译文放大：期望 %d，实得 %d（写死 2048 的旧形态）", want, rev[0].maxTokens)
	}
	if rev[0].maxTokens <= 2048 {
		t.Fatalf("审校预算仍停在旧写死档 %d", rev[0].maxTokens)
	}
}
