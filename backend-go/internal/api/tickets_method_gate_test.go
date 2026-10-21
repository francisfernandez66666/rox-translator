// ============ tickets_method_gate_test.go · 职责说明 ============
// M5（2026-10-21 UAT 收口）：GET /api/tickets 是「列出我的工单」，必须只接受 GET。
//
// 缺陷形态：旧 handleTickets 不判方法，POST/DELETE 等打同一路径会一路走完鉴权与查询，
// 回 200 的列表体——按状态码分支的客户端/监控会把它读成"动作成功"，而工单其实一件没动。
// 判据：非 GET 一律 405 + code=METHOD_NOT_ALLOWED + trace_id；GET 必须越过方法闸
// （本用例用空 Server：未登录走 401，只要不是 405 即证明闸门只拦非 GET）。
// =============================================================
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "translator/internal/errors"
)

// TestTicketsListRejectsNonGET 非 GET 方法必须在读任何状态之前以 405 收口。
// 反证：摘掉 handleTickets 开头那段方法闸，本用例立刻红（响应会变成 401 或 200）。
func TestTicketsListRejectsNonGET(t *testing.T) {
	s := &Server{}
	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	} {
		rec := httptest.NewRecorder()
		// trace_id 由中间件注入 ctx；单元测试直调 handler 时手工注一条，
		// 才能验「405 也带得出链路 ID」这条对外排障契约。
		req := httptest.NewRequest(method, "/api/tickets", nil)
		req = req.WithContext(apierrors.WithTraceID(req.Context(), "m5-tickets-gate"))
		s.handleTickets(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/tickets 应回 405，实得 %d（body=%s）", method, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"code":"METHOD_NOT_ALLOWED"`) {
			t.Fatalf("%s 的 405 响应缺统一错误码 METHOD_NOT_ALLOWED：%s", method, body)
		}
		if !strings.Contains(body, `"trace_id"`) {
			t.Fatalf("%s 的 405 响应缺 trace_id：%s", method, body)
		}
	}
}

// TestTicketsListGETPassesMethodGate 正向对照：GET 必须越过方法闸。
// 空 Server 未登录时应回 401（不是 405），证明闸门只按方法分派、没有把正常读路径一起拦掉。
func TestTicketsListGETPassesMethodGate(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
	s.handleTickets(rec, req)
	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/tickets 不应回 405（方法闸把正常读路径拦掉了）")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录 GET 应回 401，实得 %d（body=%s）", rec.Code, rec.Body.String())
	}
}
