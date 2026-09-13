// ============ dual_mode_test.go · 职责说明 ============
// 工单双模式（2026-09-13）api 侧纯函数单测：交付方式归一化 + 健康探测出参。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeTaskDelivery(t *testing.T) {
	cases := map[string]string{
		"":        "restore", // 历史工单/未显式选择
		"restore": "restore",
		"text":    "text",
		"TEXT":    "text",
		" text ":  "text",
		"garbage": "restore", // 非法值一律归默认
	}
	for in, want := range cases {
		if got := normalizeTaskDelivery(in); got != want {
			t.Errorf("normalizeTaskDelivery(%q)=%q, want %q", in, got, want)
		}
	}
}

// TestHandleHealthAnydocReady ★ 2026-09-14 部署期修复回归：/api/health 必须暴露
// anydoc_ready 布尔字段（纯文案模式提取层就绪状态），供部署验收与运维探测使用。
// 空 Server（各子系统未初始化）也应正常出参——就绪位全 false，不因未初始化而缺字段。
func TestHandleHealthAnydocReady(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	(&Server{}).handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleHealth 应 200，实际 %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health 出参非法 JSON: %v", err)
	}
	if _, ok := body["anydoc_ready"].(bool); !ok {
		t.Errorf("anydoc_ready 应为布尔字段，出参: %s", rec.Body.String())
	}
	if body["store_ready"] != false { // 空 Server 各子系统未就绪的口径核对
		t.Errorf("空 Server 下 store_ready 应为 false，出参: %s", rec.Body.String())
	}
}
