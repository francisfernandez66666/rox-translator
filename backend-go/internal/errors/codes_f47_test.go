// ============ codes_f47_test.go · 职责说明 ============
// internal/errors 包内部测试文件。
// =============================================

// ============ 本文件职责中文说明 ============
// ★ F-47（2026-09-26 UAT 修复批 I-7）状态码诚实性的收口闸门。
//
// 缺陷现象（UAT 实测）：同一仓里「被限流」有两套互相矛盾的对外表达——
//
//	① 登录锁定：走统一错误出口 s.writeError，但 ErrRateLimited 在 HTTPStatus()
//	   里落在 400 组，且 JSON 体不含「还要等多久」，响应头也没有 Retry-After。
//	   旧函数名 loginLocked 的注释写着「拒绝登录（429）」，注释与行为长期背离。
//	② 注册 / 找回密码 / 邮箱验证码：内联 writeJSON(w, 429, …) + Retry-After 头，
//	   状态码对了但绕过了统一错误体（前端 request() 对 4xx 归一解析 message，链路仍通，
//	   只是错误码缺失、SDK 无法按 code 分支）。
//
// 修法（本批已实施）：HTTPStatus() 把 ErrRateLimited 从 400 组拆出单独映射 429；
// APIError 增 retry_after 字段（omitempty）；WriteError 在 RetryAfter>0 时落 Retry-After 头；
// ②类内联体全部改走 s.writeError(...ErrRateLimited...).WithRetryAfter(wait)。
//
// 本文件锁住「机制」而非「某一处调用点」，四条判据缺一不可：
//
//	A. 限流映射 429，且与参数错(400)/余额不足(402)/不存在(404) 分家——
//	   通用重试器只对 429 退避，落回 400 就等于告诉客户端「重试无意义」。
//	B. RetryAfter>0 时头与字段同时存在且数值一致（头给通用 HTTP 客户端，字段给浏览器端）。
//	C. 负向：非限流错误**不得**带 Retry-After 头、**不得**出现 retry_after 键
//	   （omitempty 失效会让前端把「参数错」误读成「稍后再试」）。
//	D. WithRetryAfter(0)/负数归零：边界态（冷却刚好走完）不得写成 retry_after:0
//	   让客户端再空转一轮。
//
// 方言说明：本文件纯内存构造 httptest.ResponseRecorder，不碰数据库，无需钉 DB_DRIVER。
// =============================================
package errors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// f47Decode 解析 WriteError 写出的 JSON 体为通用 map（判「键在不在」用，比结构体解码严格）。
func f47Decode(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("错误体不是合法 JSON: %v\n%s", err, body)
	}
	return out
}

// A 段：错误码 → HTTP 状态映射表。
// 表内同时钉住「限流=429」与「参数错=400」的分家事实，
// 并顺带覆盖本批拆组时被移动过的兄弟码（配额/文件过大/文本超长仍为 400）。
func TestF47_RateLimitedMapsTo429(t *testing.T) {
	cases := []struct {
		name string
		code ErrorCode
		want int
	}{
		{"限流→429", ErrRateLimited, http.StatusTooManyRequests},
		{"参数错→400", ErrValidation, http.StatusBadRequest},
		{"配额超→400", ErrQuotaExceeded, http.StatusBadRequest},
		{"文件过大→400", ErrFileTooLarge, http.StatusBadRequest},
		{"聊天文本超长→400", ErrChatTextTooLong, http.StatusBadRequest},
		{"未登录→401", ErrUnauthorized, http.StatusUnauthorized},
		{"无权限→403", ErrForbidden, http.StatusForbidden},
		{"不存在→404", ErrNotFound, http.StatusNotFound},
		{"冲突→409", ErrConflict, http.StatusConflict},
		{"余额不足→402", ErrInsufficientBalance, http.StatusPaymentRequired},
		{"方法不允许→405", ErrMethodNotAllowed, http.StatusMethodNotAllowed},
		{"上游不可达→502", ErrUpstreamUnavailable, http.StatusBadGateway},
		{"内部错→500", ErrInternal, http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := New(c.code, "x").HTTPStatus(); got != c.want {
				t.Errorf("HTTPStatus(%s)=%d want %d", c.code, got, c.want)
			}
		})
	}
	// 分家反向锁：429 与 400 不得再次混在同一个 case 分支里。
	if New(ErrRateLimited, "x").HTTPStatus() == New(ErrValidation, "x").HTTPStatus() {
		t.Error("F-47 已修：限流与参数错状态码不得相同（否则客户端无法区分「稍后再试」与「重试无意义」）")
	}
}

// B 段：RetryAfter>0 ⇒ Retry-After 头 + retry_after 字段同时下发且数值一致。
func TestF47_WriteErrorEmitsRetryAfterHeaderAndField(t *testing.T) {
	for _, sec := range []int{1, 30, 300} {
		w := httptest.NewRecorder()
		WriteError(w, context.Background(), New(ErrRateLimited, "登录尝试过于频繁，请稍后再试").WithRetryAfter(sec))

		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("RetryAfter=%d 时状态码=%d want 429", sec, w.Code)
		}
		hdr := w.Header().Get("Retry-After")
		if hdr == "" {
			t.Fatalf("RetryAfter=%d 时缺 Retry-After 头（通用重试器只能瞎猜退避窗口）", sec)
		}
		hv, err := strconv.Atoi(hdr)
		if err != nil || hv != sec {
			t.Fatalf("Retry-After 头=%q want %d", hdr, sec)
		}
		out := f47Decode(t, w.Body.Bytes())
		fv, ok := out["retry_after"]
		if !ok {
			t.Fatalf("缺 retry_after 字段（浏览器端读不到头时的唯一兜底）：%s", w.Body.String())
		}
		// JSON 数字解为 float64：与头值交叉核对，禁「头 30 / 字段 31」这种两套算式。
		if fn, _ := fv.(float64); int(fn) != hv {
			t.Errorf("retry_after=%v 与 Retry-After 头=%d 不一致", fv, hv)
		}
		if b, _ := out["success"].(bool); b {
			t.Error("success 必须为 false")
		}
		if c, _ := out["code"].(string); c != string(ErrRateLimited) {
			t.Errorf("code=%q want %q", c, ErrRateLimited)
		}
	}
}

// C 段（负向）：非限流错误不得携带任何「稍后再试」信号。
// 同时锁住 omitempty：字段缺席而非 null——null 会让前端 `if (retry_after != null)` 型判定误命中。
func TestF47_NonRateLimitedCarriesNoRetrySignal(t *testing.T) {
	for _, code := range []ErrorCode{ErrValidation, ErrUnauthorized, ErrForbidden, ErrNotFound, ErrConflict, ErrInternal, ErrQuotaExceeded} {
		w := httptest.NewRecorder()
		WriteError(w, context.Background(), New(code, "普通错误"))
		if h := w.Header().Get("Retry-After"); h != "" {
			t.Errorf("%s 却带了 Retry-After: %s（前端会把参数错误读成稍后再试）", code, h)
		}
		if strings.Contains(w.Body.String(), "retry_after") {
			t.Errorf("%s 响应体出现 retry_after 键，omitempty 失效：%s", code, w.Body.String())
		}
		if w.Code == http.StatusTooManyRequests {
			t.Errorf("%s 不得映射 429", code)
		}
	}
}

// D 段：WithRetryAfter 的 0/负数归零语义（边界态＝已经可以重试）。
func TestF47_WithRetryAfterNormalizesNonPositive(t *testing.T) {
	for _, sec := range []int{0, -1, -300} {
		e := New(ErrRateLimited, "登录尝试过于频繁，请稍后再试").WithRetryAfter(sec)
		if e.RetryAfter != 0 {
			t.Errorf("WithRetryAfter(%d) 后 RetryAfter=%d，非正数必须归零", sec, e.RetryAfter)
		}
		w := httptest.NewRecorder()
		WriteError(w, context.Background(), e)
		// 归零后走的是「无时长 429」：状态码仍诚实，只是不给具体等待值。
		if w.Code != http.StatusTooManyRequests {
			t.Errorf("WithRetryAfter(%d) 后状态码=%d want 429", sec, w.Code)
		}
		if h := w.Header().Get("Retry-After"); h != "" {
			t.Errorf("WithRetryAfter(%d) 却下发 Retry-After: %s（0 会被客户端当成立刻重试信号）", sec, h)
		}
		if strings.Contains(w.Body.String(), "retry_after") {
			t.Errorf("WithRetryAfter(%d) 响应体仍含 retry_after：%s", sec, w.Body.String())
		}
	}
	// 正数必须保留（上一段全归零也能过，这里补正向对照）。
	if got := New(ErrRateLimited, "x").WithRetryAfter(12).RetryAfter; got != 12 {
		t.Errorf("正数被吞：WithRetryAfter(12) 后 RetryAfter=%d want 12", got)
	}
}

// E 段：链式返回自身（防有人把 WithRetryAfter 改成值接收者，链式调用点静默丢字段）。
func TestF47_WithRetryAfterReturnsSelf(t *testing.T) {
	e := New(ErrRateLimited, "x")
	if got := e.WithRetryAfter(7); got != e {
		t.Error("WithRetryAfter 必须返回自身以支持链式（与 WithDetails/WithTraceID 同口径）")
	}
	w := httptest.NewRecorder()
	WriteError(w, context.Background(), New(ErrRateLimited, "x").WithRetryAfter(7).WithDetails(map[string]string{"k": "v"}))
	out := f47Decode(t, w.Body.Bytes())
	if _, ok := out["details"]; !ok {
		t.Error("链式调用后 details 丢失")
	}
	if v, _ := out["retry_after"].(float64); int(v) != 7 {
		t.Errorf("链式调用后 retry_after 丢失/漂移：%v", out["retry_after"])
	}
}
