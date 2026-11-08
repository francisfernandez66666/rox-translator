// ============ 本文件职责中文说明 ============
// 二期 §9.4 草稿 inbox 数据源的 API 面判据：
//
//	三个口（列表/详情/转正）未登录一律 401；租户作用域按 effTenant 现算，
//	跨租户读与转正一律 404（不泄露存在性）；转正必须留审计 sales_draft_approve
//	（动作/资源/草稿 id/操作者四件齐）；重复转正 404；空集出 []（不是 null）。
//
// 复用同包 adminGateServer 的全量路由表（故意不手挑清单，新路由漏鉴权由既有
// TestRouteAuthGate 与本文件一起兜）。转正唯一入口的源码级判据在
// store/sales_draft_gate_test.go 的 M1，本文件只判 HTTP 面的行为与留痕。
// ========================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSalesDraftInboxIsTenantScopedAndAudited 草稿 inbox 三口行为＋审计留痕。
func TestSalesDraftInboxIsTenantScopedAndAudited(t *testing.T) {
	srv, tok := adminGateServer(t)

	// 本租户两条草稿＋别的租户一条（跨租户靶子）。
	mine1, err := srv.Store.CreateSalesDraft(1, "price-anchor", "原话一", "说法一", "attr-1")
	if err != nil {
		t.Fatalf("建草稿失败: %v", err)
	}
	mine2, err := srv.Store.CreateSalesDraft(1, "spec-anchor", "原话二", "说法二", "attr-2")
	if err != nil {
		t.Fatalf("建草稿失败: %v", err)
	}
	other, err := srv.Store.CreateSalesDraft(2, "x", "别家原话", "别家说法", "attr-x")
	if err != nil {
		t.Fatalf("建跨租户靶子失败: %v", err)
	}

	call := func(method, path, token, body string) (int, map[string]interface{}, string) {
		t.Helper()
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, r)
		var m map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m, w.Body.String()
	}

	// 未登录三口一律 401。
	for _, probe := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/sales/drafts", ""},
		{http.MethodGet, "/api/sales/draft?id=" + mine1.ID, ""},
		{http.MethodPost, "/api/sales/draft/approve", `{"id":"` + mine1.ID + `"}`},
	} {
		if code, _, _ := call(probe.method, probe.path, "", probe.body); code != http.StatusUnauthorized {
			t.Fatalf("未登录 %s 必须 401，实得 %d", probe.path, code)
		}
	}

	// 列表只出本租户；空过滤集出 []（不是 null）。
	code, body, raw := call(http.MethodGet, "/api/sales/drafts", tok, "")
	if code != http.StatusOK {
		t.Fatalf("列草稿应 200，实得 %d body=%s", code, raw)
	}
	drafts, _ := body["drafts"].([]interface{})
	if len(drafts) != 2 {
		t.Fatalf("租户作用域：只应看到本租户 2 条，实得 %d ⇒ 跨租户串了", len(drafts))
	}
	if _, _, rawEmpty := call(http.MethodGet, "/api/sales/drafts?status=rejected", tok, ""); !strings.Contains(rawEmpty, `"drafts":[]`) {
		t.Fatalf("空集必须出 []（不是 null）: %s", rawEmpty)
	}

	// 详情：本租户 200；跨租户与缺参各回其档。
	code, body, _ = call(http.MethodGet, "/api/sales/draft?id="+mine1.ID, tok, "")
	if code != http.StatusOK {
		t.Fatalf("详情应 200，实得 %d", code)
	}
	if d, _ := body["draft"].(map[string]interface{}); d["id"] != mine1.ID || d["status"] != "draft" {
		t.Fatalf("详情读数不对: %v", body)
	}
	if code, _, _ = call(http.MethodGet, "/api/sales/draft?id="+other.ID, tok, ""); code != http.StatusNotFound {
		t.Fatalf("跨租户详情必须 404（不泄露存在性），实得 %d", code)
	}
	if code, _, _ = call(http.MethodGet, "/api/sales/draft", tok, ""); code != http.StatusBadRequest {
		t.Fatalf("缺 id 必须 400，实得 %d", code)
	}

	// 转正：成功＋审计四件齐（动作/资源/草稿 id/操作者）。
	code, body, raw = call(http.MethodPost, "/api/sales/draft/approve", tok, `{"id":"`+mine1.ID+`"}`)
	if code != http.StatusOK || body["status"] != "approved" {
		t.Fatalf("转正应 200 且 approved，实得 %d %s", code, raw)
	}
	logs, err := srv.Store.ListAuditFilter(1, "sales_draft_approve", "sales_draft", 0, "", "", 10)
	if err != nil || len(logs) != 1 {
		t.Fatalf("转正必须恰好留一条审计，实得 %d 条 err=%v ⇒ 接口说成功不算验收，留痕才算", len(logs), err)
	}
	if logs[0].Detail != mine1.ID || logs[0].UserID <= 0 {
		t.Fatalf("审计必须点名草稿 id 与操作者: %+v", logs[0])
	}

	// 重复转正与跨租户转正一律 404；缺 id 400。
	if code, _, _ = call(http.MethodPost, "/api/sales/draft/approve", tok, `{"id":"`+mine1.ID+`"}`); code != http.StatusNotFound {
		t.Fatalf("重复转正必须 404，实得 %d", code)
	}
	if code, _, _ = call(http.MethodPost, "/api/sales/draft/approve", tok, `{"id":"`+other.ID+`"}`); code != http.StatusNotFound {
		t.Fatalf("跨租户转正必须 404，实得 %d", code)
	}
	if code, _, _ = call(http.MethodPost, "/api/sales/draft/approve", tok, `{}`); code != http.StatusBadRequest {
		t.Fatalf("缺 id 必须 400，实得 %d", code)
	}

	// 转正后列表状态过滤跟着动（读侧与写侧同一份事实）。
	_, body, _ = call(http.MethodGet, "/api/sales/drafts?status=approved", tok, "")
	if list, _ := body["drafts"].([]interface{}); len(list) != 1 {
		t.Fatalf("approved 过滤应恰好 1 条，实得 %v", body)
	}
	_ = mine2 // 第二条草稿留在 draft 态，作为「无人审不生效」的活对照
}
