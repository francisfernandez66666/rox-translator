// ============ orgs_f4_test.go · 职责说明 ============
// 2026-10-23 F4：平台组织树默认收敛个人租户。
// 判据三向：
//
//	① 默认档（不带 include_personal）orgs 里不得出现个人租户的任何节点，企业租户节点必须在（正对照）；
//	② personal_count 必须如实给出个人租户数（开关文案的数据源）；
//	③ include_personal=1 时必须展开个人租户根，且节点带 tenant_is_personal=true（前端据此二次过滤）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestOrgListPlatformExcludesPersonal
// ============================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// orgListCall 以超管令牌在平台上下文（不带 X-Tenant-ID）取组织列表。
func orgListCall(t *testing.T, s *Server, token, query string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/orgs"+query, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.handleOrgList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("组织列表应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析组织列表失败: %v (%s)", err, rec.Body.String())
	}
	return m
}

// orgTenantIDs 汇总出参里出现过的 tenant_id 集合。
func orgTenantIDs(m map[string]any) map[int64]bool {
	out := map[int64]bool{}
	list, _ := m["orgs"].([]any)
	for _, it := range list {
		row, _ := it.(map[string]any)
		if v, ok := row["tenant_id"].(float64); ok {
			out[int64(v)] = true
		}
	}
	return out
}

// TestOrgListPlatformExcludesPersonal F4 主判据 + 反证（开关打开后必须出现）。
func TestOrgListPlatformExcludesPersonal(t *testing.T) {
	f := newRenewFixture(t)
	superTK := brandGateSuperToken(t, f)

	ent, err := f.srv.Ten.Create("f4ent", "F4 企业", "", "{}")
	if err != nil {
		t.Fatalf("创建企业租户失败: %v", err)
	}
	per, err := f.srv.Ten.Create("f4per", "F4 个人", "", "{}")
	if err != nil {
		t.Fatalf("创建个人租户失败: %v", err)
	}
	if err := f.srv.Ten.SetPersonal(per.ID, true); err != nil {
		t.Fatalf("置个人租户失败: %v", err)
	}
	if _, err := f.srv.Store.EnsureRootOrg(ent.ID, ent.Name); err != nil {
		t.Fatalf("建企业根组织失败: %v", err)
	}
	if _, err := f.srv.Store.EnsureRootOrg(per.ID, per.Name); err != nil {
		t.Fatalf("建个人根组织失败: %v", err)
	}

	// ① 默认档：个人租户整棵不可见、企业租户可见
	m := orgListCall(t, f.srv, superTK, "")
	ids := orgTenantIDs(m)
	if !ids[ent.ID] {
		t.Fatalf("正对照失败：企业租户 %d 不在平台树里（下面「个人不在」的断言会变假绿）", ent.ID)
	}
	if ids[per.ID] {
		t.Fatalf("F4 未生效：默认档仍返回个人租户 %d 的组织", per.ID)
	}
	if n, _ := m["personal_count"].(float64); int(n) != 1 {
		t.Fatalf("personal_count 应为 1，实际 %v（开关文案会显示错数）", m["personal_count"])
	}

	// ③ 反证：显式打开开关后个人租户根必须出现，并带 tenant_is_personal 标记
	m2 := orgListCall(t, f.srv, superTK, "?include_personal=1")
	ids2 := orgTenantIDs(m2)
	if !ids2[per.ID] {
		t.Fatalf("开关打开后个人租户 %d 仍未出现（收敛被写成了永久隐藏）", per.ID)
	}
	marked := false
	list, _ := m2["orgs"].([]any)
	for _, it := range list {
		row, _ := it.(map[string]any)
		if v, ok := row["tenant_id"].(float64); ok && int64(v) == per.ID {
			if b, _ := row["tenant_is_personal"].(bool); !b {
				t.Fatalf("个人租户节点缺 tenant_is_personal=true：%v", row)
			}
			marked = true
		}
	}
	if !marked {
		t.Fatalf("个人租户节点没有 tenant_id=%d 的行", per.ID)
	}
	if n, _ := m2["personal_count"].(float64); int(n) != 1 {
		t.Fatalf("展开档 personal_count 应仍为 1，实际 %v", m2["personal_count"])
	}
}
