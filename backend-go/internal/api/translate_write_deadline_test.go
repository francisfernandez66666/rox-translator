// 本文件锁「同步文件翻译通道（/api/translate）必须清写超时」这条腿（2026-11-21 真值验收 tv6）。
//
// 现场：199 行 / 15.6 KB 的 Markdown 实测耗时 2,150 s，而全站 http.Server 的 WriteTimeout 是
// 30 分钟（cmd/server/main.go）⇒ handler 把成品写完了、响应却写不出去，客户端拿到 curl 000，
// 客户以为失败而文件其实已经在磁盘上。产物能打开、服务端日志全绿，只有回执那一跳是坏的
// （AGENTS §一·12「有自动降级的链路，产物能打开不算验收」的同一形态）。
//
// 判据取向与 sse_write_deadline_test.go 同源，但多两条：
//   - 必须穿过真实中间件包装链（statusWriter / langWriter）够到底层 response（Unwrap 少一层就静默死）；
//   - 位置必须在**鉴权之前**：拒绝路径也要写回 401 响应体，而"谁来调"与"本请求的写截止时刻"无关。
//     ⚠️ 这一条要用匿名请求（Store 为 nil ⇒ authUser 直接回 nil）来判，否则测的是"带对 Token 的请求"。
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTranslateFileClearsWriteDeadline 匿名请求（401 路径）也必须先把写超时清成无限，
// 且这次调用必须穿过两层包装器落到真实 response 上。
func TestTranslateFileClearsWriteDeadline(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wrap      func(spy *deadlineSpy) http.ResponseWriter
		checkBody bool // langWriter 会把 JSON 扣在缓冲里等中间件 finish，直调 handler 时看不到体
	}{
		{"直通", func(s *deadlineSpy) http.ResponseWriter { return s }, true},
		{"statusWriter 一层", func(s *deadlineSpy) http.ResponseWriter {
			return &statusWriter{ResponseWriter: s, status: 200}
		}, true},
		{"statusWriter+langWriter 两层", func(s *deadlineSpy) http.ResponseWriter {
			sw := &statusWriter{ResponseWriter: s, status: 200}
			return &langWriter{real: sw}
		}, false},
	} {
		spy := &deadlineSpy{supports: true}
		// Store 为 nil ⇒ authUser 立即回 nil，走 401 分支；这条路径正是"拒绝也要能写回响应体"的现场。
		srv := &Server{}
		req := httptest.NewRequest(http.MethodPost, "/api/translate", nil)
		srv.handleTranslateFile(tc.wrap(spy), req)

		if spy.calls != 1 || !spy.last.IsZero() {
			t.Fatalf("%s: 期望恰好一次 SetWriteDeadline(零值=无限)，实际 calls=%d last=%v ⇒ 同步长腿没清写超时，"+
				"大文件会在 30 分钟后拿到 curl 000（产物已落盘但客户以为失败）", tc.name, spy.calls, spy.last)
		}
		if tc.checkBody && !strings.Contains(spy.body.String(), "未登录") {
			t.Fatalf("%s: 401 响应体没写出去（实际 %q）⇒ 清超时那一行挡住了拒绝路径", tc.name, spy.body.String())
		}
	}
}

// TestTranslateFileWriteDeadlineClearedBeforeAuth 位置判据：清超时必须在鉴权**之前**。
// 形态判据（不依赖上面的行为腿）：源码里 SetWriteDeadline 的下标必须小于 authUser 的下标。
// 理由：把它放到鉴权之后，匿名/过期 Token 的请求就拿不到豁免——而这类请求同样要写回 401 体，
// 长连接上仍可能撞写超时；更糟的是它会让"这条豁免对谁生效"变成一个隐式条件（AGENTS §三 判据要有射程）。
func TestTranslateFileWriteDeadlineClearedBeforeAuth(t *testing.T) {
	src := readFile(t, "stream.go")
	start := strings.Index(src, "func (s *Server) handleTranslateFile(")
	if start < 0 {
		t.Fatal("找不到 handleTranslateFile ⇒ 锚点漂了，判据已失效（不是产品缺陷）")
	}
	end := strings.Index(src[start:], "\nfunc ")
	if end < 0 {
		end = len(src) - start
	}
	body := src[start : start+end]
	dl := strings.Index(body, "SetWriteDeadline(time.Time{})")
	au := strings.Index(body, "s.authUser(r)")
	if dl < 0 {
		t.Fatal("handleTranslateFile 里没有清写超时的调用 ⇒ tv6 那条 HTTP=000 会原样复发")
	}
	if au < 0 {
		t.Fatal("handleTranslateFile 里找不到 s.authUser(r) ⇒ 锚点漂了，判据已失效")
	}
	if dl > au {
		t.Fatalf("清写超时排在鉴权之后（SetWriteDeadline@%d > authUser@%d）⇒ 拒绝路径没有豁免", dl, au)
	}
}
