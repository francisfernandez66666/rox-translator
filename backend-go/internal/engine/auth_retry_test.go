// ============================================================================
// auth_retry_test.go — ★ 修法 E 的 engine 侧断言（A6 后半，2026-10-04 〇-AR 第 2 波）。
//
// 钉住的历史缺陷：translateLangsConcurrent 的轮次化重试队列对**所有**失败一律重试到
// maxAttempts（默认 3）。对 429/超时/网络抖动这是对的（退避一轮可能就过去了），
// 但对 401/403 是纯粹的浪费：键不会自己变对。
// 现网实证就是 R-1——占位 Key 期间一次 Pro 对话的三语种被打成 3×3=9 次恒 401 的出站调用，
// P95 因此多拖约 6.6s，客户仍然只看到「没有译文」。
//
// 本文件钉三条：
//  1. 认证类失败**只拨一轮**（每语种一次，不是三次）；
//  2. 失败仍然入账（该语种在 out 里是空串，由上层漏译硬闸／⑭ 的出口收敛负责报失败），
//     止损不等于吞掉；
//  3. 日志级别抬到 ERROR（≥WARN），且带得上「这是键的问题」的可读信息＋原样的 401 字面量。
//
// 反证：删掉 authBail 那一档（恢复"什么都重试三次"）⇒ 条 1 与条 3 红；
// 把 llm 侧 401 改回裸 fmt.Errorf ⇒ IsAuthError 恒假，同样回到重试三次 ⇒ 红；
// 把短路写宽成"什么都只拨一次"⇒ TestNonAuthFailuresStillRetry 红。
//
// 方言口径（AGENTS §一·4）：本测试族固定内存 SQLite。
// ============================================================================
package engine

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/kb"
	"translator/internal/llm"
)

// captureSlog 把包级默认 slog 换到内存缓冲并返回该缓冲（用例结束自动恢复原 logger）。
// 用途：A6 要求「认证类失败日志级别 ≥WARN」，而本仓日志口径是 observability（slog JSON），
// 判据只能从 handler 里读 level 字段——拿 stdout 抓不算断言。
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// authRetryTestEngine 构造一台「单语腿恒回指定错误并计数」的引擎。
// 参数 t: 测试句柄；codes: 每次调用要返回的 HTTP 状态码（用于造 401／429／500 三族）。
// 返回: 引擎、按语种统计的调用次数读取函数（并发安全）。
func authRetryTestEngine(t *testing.T, mkErr func(lang string) error) (*Engine, func(lang string) int, func() int) {
	t.Helper()
	st := newTestStore(t)
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	old := config.C
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	e := NewEngine(cfg, nil, nil, nil)
	e.St = st
	// 轮次与退避在测试里钉死：默认「3 轮 / 2 秒」会让用例白等 4 秒，
	// 而"到底重试了几轮"本身就是本用例要测的事实，不能被环境默认值牵着走。
	e.retryMaxAttempts = 3
	e.retrySleep = 5 * time.Millisecond

	var mu sync.Mutex
	calls := map[string]int{}
	e.singleLangFn = func(_ context.Context, _ string, lang string, _ []*kb.Row, _ string, _ string) (string, error) {
		mu.Lock()
		calls[lang]++
		mu.Unlock()
		return "", mkErr(lang)
	}
	perLang := func(lang string) int {
		mu.Lock()
		defer mu.Unlock()
		return calls[lang]
	}
	all := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, c := range calls {
			n += c
		}
		return n
	}
	return e, perLang, all
}

// TestAuthErrorIsTypedAndNotRetried = 断言 A6（engine 侧）：401 只拨一轮、失败仍入账、日志抬 ERROR。
func TestAuthErrorIsTypedAndNotRetried(t *testing.T) {
	buf := captureSlog(t)
	langs := []string{"en", "ja", "th"}
	e, perLang, all := authRetryTestEngine(t, func(_ string) error {
		// 与生产形态一致：类型化＋Auth 标记（R-1 期间那张随机占位 Key 正是这一族）。
		return &llm.StatusError{Code: 401, Auth: true, Body: `{"error":{"message":"invalid api key"}}`}
	})

	out := map[string]string{}
	e.translateLangsConcurrent(context.Background(), "本公司专注智能硬件", langs, nil, out, "zh", config.StageAIInitial)

	if n := all(); n != len(langs) {
		t.Fatalf("认证类失败应只拨一轮（每语种 1 次，共 %d 次），实得 %d 次；旧形态是 %d 次",
			len(langs), n, len(langs)*3)
	}
	for _, lc := range langs {
		if c := perLang(lc); c != 1 {
			t.Fatalf("语种 %s 被调用 %d 次，期望 1 次", lc, c)
		}
		// 止损不吞账：该语种必须留在结果里且为空，上层（漏译硬闸／⑭ 出口收敛）才知道"这一单缺译文"。
		v, ok := out[lc]
		if !ok || strings.TrimSpace(v) != "" {
			t.Fatalf("止损后语种 %s 应记为空串（ok=%v v=%q），否则漏译判据扫不到", lc, ok, v)
		}
	}

	// 日志级别 ≥WARN（这里是 ERROR），且这句要能让人一眼看出"要换键"，不是无从下手的"翻译失败"。
	logs := buf.String()
	if !strings.Contains(logs, `"level":"ERROR"`) {
		t.Fatalf("认证类失败日志未抬到 ERROR（A6③），实得日志：\n%s", logs)
	}
	if !strings.Contains(logs, "鉴权失败") {
		t.Fatalf("日志缺少可读的鉴权失败说明，实得：\n%s", logs)
	}
	// 反向：不许把 401 文案改写成别的——A9 门禁（deploy/check_upstream_401.sh）按这句 grep 现网日志窗口，
	// 文案一丢，门禁就永远绿灯地扫不到真 401（假绿形态）。
	if !strings.Contains(logs, "api key 无效 (401)") {
		t.Fatalf("日志里 401 字面量丢失，发版验收门禁会静默失灵：\n%s", logs)
	}
}

// TestNonAuthFailuresStillRetry 正向对照：429／5xx **必须**继续重试满三轮。
// 没有这一条，"认证类短路"就会被写宽成"什么都只拨一次"——偶发限流直接变成缺语种，
// 而那正是 2026-09-09 可靠性改造要修回去的形态。
func TestNonAuthFailuresStillRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   func() error
	}{
		{"429 限流", func() error { return &llm.StatusError{Code: 429} }},
		{"500 上游故障", func() error { return &llm.StatusError{Code: 500, Body: "boom"} }},
		{"网络错误（非状态类）", func() error { return errors.New("dial tcp: connection refused") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureSlog(t)
			e, _, all := authRetryTestEngine(t, func(_ string) error { return tc.mk() })
			out := map[string]string{}
			e.translateLangsConcurrent(context.Background(), "文本", []string{"en"}, nil, out, "zh", config.StageAIInitial)
			if n := all(); n != 3 {
				t.Fatalf("%s 应重试满 3 轮（退避重试是它的正解），实得 %d 次", tc.name, n)
			}
			// 非认证类失败仍走原来的 log.Printf 口径，不许抬成 ERROR 告警（限流是常态噪音，抬了会把值班淹掉）。
			if strings.Contains(buf.String(), `"level":"ERROR"`) {
				t.Fatalf("%s 不该产生 ERROR 级日志（那是鉴权失败专用档），实得：\n%s", tc.name, buf.String())
			}
		})
	}
}

// TestIsServerErrorNowSeesAuth401 ★ P1-6 的另一半：401 类型化之后，
// 供应商降级/熔断的判定必须真的能"看见"它——这正是 llm 侧那一改想换来的行为。
// 旧形态下这条判据恒假，P1-6 注释里那句「5xx/401 一并纳入降级与熔断」从未在 401 上成立过。
func TestIsServerErrorNowSeesAuth401(t *testing.T) {
	auth401 := &llm.StatusError{Code: 401, Auth: true}
	if !isServerError(auth401) {
		t.Fatal("isServerError 必须认类型化 401，否则备用供应商降级在鉴权失败上仍是死腿")
	}
	if isRateLimited(auth401) {
		t.Fatal("401 不得被判成限流（限流要 sleep 后退避，鉴权失败要立刻收手）")
	}
	if !isServerError(&llm.StatusError{Code: 503}) {
		t.Fatal("5xx 仍须在降级射程内（P1-6 既有行为不许缩）")
	}
	// 反向对照：普通错误（非 HTTP 状态类）不进降级射程。
	if isServerError(errors.New("解析响应: unexpected end of JSON input")) {
		t.Fatal("非 HTTP 状态类错误不该被算成供应商故障")
	}
}
