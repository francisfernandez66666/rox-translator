// ============ user_list_page_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 A6：超管平台视图「跨租户列用户」分页出参的行为闸门。
//
// U1 **缺省档＝不限**，与改造前那条无 LIMIT 的 SELECT 逐字等值（老面板一字不变）；
// U2 显式点档 ⇒ 分页生效、两页不重叠、has_more 按"本页打满档位"判；
// U3 只要 offset 没要 limit ⇒ offset 必须真生效（静默吞掉的表现为"第二页＝第一页"，
//
//	那是分页参数在位却什么都不做的最难归因的一种假绿）；
//
// U4 越界页 ⇒ `users` 出 `[]` 不出 `null`，且 total 仍是真值。
//
// 方言：自钉内存 SQLite（AGENTS §一·4），夹具走 newF44Probe（超管上下文 tid=0）。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"translator/internal/store"
)

// callAdminUsers 直调 GET /api/admin/users?<query>，返回状态码与原始响应体 map。
func callAdminUsers(t *testing.T, s *Server, tok, query string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users"+query, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleAdminUsers(rec, req)
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("用户列表响应不是 JSON（code=%d）: %s", rec.Code, rec.Body.String())
	}
	return rec.Code, out
}

// userNamesFromRaw 摊出 users 的用户名列表；顺带判"空页必须是 [] 不是 null"。
func userNamesFromRaw(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	raw, ok := body["users"]
	if !ok {
		t.Fatalf("响应缺 users 键: %v", body)
	}
	if raw == nil {
		t.Fatal("users 出栈成 null——空页在 HTTP 层必须归一成 []（AGENTS §一·5）")
	}
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("users 不是数组: %T", raw)
	}
	var out []string
	for _, it := range arr {
		m, _ := it.(map[string]interface{})
		out = append(out, m["username"].(string))
	}
	return out
}

// newUserListFixture 起一块内存库：一个超管（newF44Probe 自带）＋ n 个普通成员。
func newUserListFixture(t *testing.T) (*Server, string, int) {
	t.Helper()
	s, _, tok := newF44Probe(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Store.CreateUser(int64(i+1), "a6_user_"+string(rune('a'+i)), "hash", "A6 夹具", store.RoleUser, 0, 0); err != nil {
			t.Fatalf("建夹具用户失败: %v", err)
		}
	}
	return s, tok, 4 // 1 超管 + 3 成员
}

// TestAdminUsersDefaultTierIsUnlimited U1：缺省不限 ⇒ 全量回、total 为真值、has_more=false。
func TestAdminUsersDefaultTierIsUnlimited(t *testing.T) {
	s, tok, want := newUserListFixture(t)
	code, body := callAdminUsers(t, s, tok, "")
	if code != http.StatusOK {
		t.Fatalf("应 200，实得 %d: %v", code, body)
	}
	got := userNamesFromRaw(t, body)
	if len(got) != want {
		t.Fatalf("缺省档必须与改造前一样回全量 %d 个账号，实得 %d（默认档被人调小＝把全量统计算窄）", want, len(got))
	}
	if l, _ := body["limit"].(float64); int(l) != 0 {
		t.Fatalf("缺省 limit 应出 0（0＝不限，这是本接口与工单列表唯一不同的那一档），实得 %v", body["limit"])
	}
	if tot, _ := body["total"].(float64); int(tot) != want {
		t.Fatalf("total 应为 %d，实得 %v", want, body["total"])
	}
	if hm, ok := body["has_more"].(bool); !ok || hm {
		t.Fatalf("不限档一次取完，has_more 必须为 false（实得 %v）", body["has_more"])
	}
}

// TestAdminUsersExplicitPagesDoNotOverlap U2：显式档位分页不重叠、has_more 正确。
func TestAdminUsersExplicitPagesDoNotOverlap(t *testing.T) {
	s, tok, want := newUserListFixture(t)
	_, p0 := callAdminUsers(t, s, tok, "?limit=2&offset=0")
	_, p1 := callAdminUsers(t, s, tok, "?limit=2&offset=2")
	n0 := userNamesFromRaw(t, p0)
	n1 := userNamesFromRaw(t, p1)
	if len(n0) != 2 || len(n1) != 2 {
		t.Fatalf("两页应各 2 个，实得 %d / %d（共 %d 账号）", len(n0), len(n1), want)
	}
	seen := map[string]bool{}
	for _, u := range n0 {
		seen[u] = true
	}
	for _, u := range n1 {
		if seen[u] {
			t.Fatalf("第二页与第一页重叠（%s）——offset 没进 SQL", u)
		}
	}
	if hm, _ := p0["has_more"].(bool); !hm {
		t.Fatal("2/4 只取了一半，has_more 必须为 true")
	}
	if l, _ := p1["limit"].(float64); int(l) != 2 {
		t.Fatalf("出栈 limit 必须回生效档位 2（回显入参也要是生效那一档），实得 %v", p1["limit"])
	}
}

// TestAdminUsersOffsetWithoutLimitIsHonoured U3：只要 offset 也必须真生效。
func TestAdminUsersOffsetWithoutLimitIsHonoured(t *testing.T) {
	s, tok, want := newUserListFixture(t)
	_, all := callAdminUsers(t, s, tok, "")
	allNames := userNamesFromRaw(t, all)
	_, off := callAdminUsers(t, s, tok, "?offset=1")
	got := userNamesFromRaw(t, off)
	if len(got) != want-1 {
		t.Fatalf("offset=1 且不限档应少 1 行，实得 %d（offset 被静默吞掉＝分页在位却什么都不做）", len(got))
	}
	if len(got) > 0 && got[0] == allNames[0] {
		t.Fatalf("offset=1 的第一行还是原来的第一行（%s）——offset 没生效", got[0])
	}
}

// TestAdminUsersOutOfRangePageIsEmptyArray U4：越界页出 [] 而不是 null，total 仍真值。
func TestAdminUsersOutOfRangePageIsEmptyArray(t *testing.T) {
	s, tok, want := newUserListFixture(t)
	code, body := callAdminUsers(t, s, tok, "?limit=2&offset=99")
	if code != http.StatusOK {
		t.Fatalf("越界页应 200（不是 4xx），实得 %d", code)
	}
	if got := userNamesFromRaw(t, body); len(got) != 0 {
		t.Fatalf("越界页应为空数组，实得 %d 条", len(got))
	}
	if tot, _ := body["total"].(float64); int(tot) != want {
		t.Fatalf("空页的 total 仍须是真值，实得 %v", body["total"])
	}
	if hm, _ := body["has_more"].(bool); hm {
		t.Fatal("空页 has_more 必须为 false")
	}
}
