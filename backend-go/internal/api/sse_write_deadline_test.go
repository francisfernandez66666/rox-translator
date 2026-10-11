// 本文件锁 P1④ 的**豁免腿**（2026-10-11 慢 SQL 批）：SSE 入口（sseHeaders）必须把本请求的
// 写超时清成无限，而且必须**穿过真实中间件包装链**够到底层 response。
// 三层包装（真实 → statusWriter → langWriter）逐层测，是因为 Unwrap 少配任何一层，
// http.NewResponseController 都只回 ErrNotSupported、界面与日志零痕迹——
// 那正是「WriteTimeout 上线当天把翻译流掐了」的隐藏形态，只有功能腿抓得住。
package api

import (
	"net/http"
	"testing"
	"time"
)

// deadlineSpy 是"真实 response"的替身：记录 SetWriteDeadline 的每一次调用参数。
type deadlineSpy struct {
	h        http.Header
	calls    int
	last     time.Time
	supports bool // false＝模拟 ResponseController 够不到底层（Unwrap 链断掉时的真实形态）
}

func (d *deadlineSpy) Header() http.Header {
	if d.h == nil {
		d.h = http.Header{}
	}
	return d.h
}
func (d *deadlineSpy) Write(b []byte) (int, error) { return len(b), nil }
func (d *deadlineSpy) WriteHeader(code int)        {}
func (d *deadlineSpy) SetWriteDeadline(t time.Time) error {
	// 先记账再表态：supports=false 模拟的是"够到底层但底层不支持"，
	// 若把记账放在分支后，"没走到"与"走到了但不支持"就分不开（第二条腿正是来区分它们的）。
	d.calls++
	d.last = t
	if !d.supports {
		return http.ErrNotSupported
	}
	return nil
}

func TestSSEHeadersClearsWriteDeadline(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(spy *deadlineSpy) http.ResponseWriter
	}{
		{"zh 直通链（statusWriter 一层）", func(s *deadlineSpy) http.ResponseWriter {
			return &statusWriter{ResponseWriter: s, status: 200}
		}},
		{"en 包装链（statusWriter+langWriter 两层）", func(s *deadlineSpy) http.ResponseWriter {
			sw := &statusWriter{ResponseWriter: s, status: 200}
			return &langWriter{real: sw}
		}},
	} {
		spy := &deadlineSpy{supports: true}
		sseHeaders(tc.wrap(spy))
		if spy.calls != 1 || !spy.last.IsZero() {
			t.Fatalf("%s: 期望恰好一次 SetWriteDeadline(零值=无限) 实际 calls=%d last=%v ⇒ Unwrap 链或豁免腿断了", tc.name, spy.calls, spy.last)
		}
		if got := spy.Header().Get("Content-Type"); got != "text/event-stream" {
			t.Fatalf("%s: SSE 头被豁免腿改动: %q", tc.name, got)
		}
	}
}

// TestSSEHeadersUnwrapChainReachesBottom 接线判据：langWriter.Unwrap 必须给回 statusWriter、
// statusWriter.Unwrap 必须给回底层 spy——链断在任何一层，豁免腿就只在 ErrNotSupported 里静默死掉。
// 另附负向对照：底层"不支持"时 sseHeaders 不许 panic、不许把错误当成功（调用仍须穿过两层到它头上）。
func TestSSEHeadersUnwrapChainReachesBottom(t *testing.T) {
	spy := &deadlineSpy{supports: false}
	sw := &statusWriter{ResponseWriter: spy, status: 200}
	lw := &langWriter{real: sw}
	if got := lw.Unwrap(); got == nil {
		t.Fatal("langWriter.Unwrap 返回 nil ⇒ 豁免腿到不了内层")
	} else if _, ok := got.(*statusWriter); !ok {
		t.Fatalf("langWriter.Unwrap 没回 statusWriter 而是 %T ⇒ 包装链接错", got)
	}
	if got := sw.Unwrap(); got == nil {
		t.Fatal("statusWriter.Unwrap 返回 nil ⇒ 豁免腿到不了真实 response")
	} else if got != http.ResponseWriter(spy) {
		t.Fatalf("statusWriter.Unwrap 没回底层 spy（%T）", got)
	}
	sseHeaders(lw) // spy 不支持时返回 ErrNotSupported（被吞），但调用必须穿过两层到它头上
	if spy.calls != 1 {
		t.Fatalf("底层被调次数期望 1 实际 %d ⇒ Unwrap 链没走完", spy.calls)
	}
}
