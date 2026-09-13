// ============ 本文件职责说明 ============
// D3 回归（2026-09-12）：kb_packages.enabled=0（停用）后，其 kb_entries 术语
// 不得再经 FindTermsBySubstring / FindEntriesBySource* 注入翻译 prompt。
// =============================================
package store

import "testing"

func TestD3DisabledPackTermsExcluded(t *testing.T) {
	st := newTestStoreWithTenants(t)
	pk, err := st.CreateKBPackage(1, 0, "d3org", "D3企业包", "tenant", "source")
	if err != nil || pk == nil {
		t.Fatalf("建包失败: %v", err)
	}
	if _, err := st.db.Exec("INSERT INTO kb_entries (tenant_id,package_id,layer,source_lang,source_text,target_lang,target_text,module,created_at,updated_at) VALUES (1,?,1,'zh','极石','ru','РОКС','brand','x','x')", pk.ID); err != nil {
		t.Fatalf("插术语失败: %v", err)
	}
	var hits []*KBEntry
	hits, err = st.FindTermsBySubstring(1, 0, "zh", "我有一辆极石汽车")
	if err != nil || len(hits) == 0 {
		t.Fatalf("启用态应命中: n=%d err=%v", len(hits), err)
	}
	if e := st.SetKBPackageEnabled(pk.ID, 0); e != nil {
		if _, e2 := st.db.Exec("UPDATE kb_packages SET enabled=0 WHERE id=?", pk.ID); e2 != nil {
			t.Fatalf("停用失败: %v / %v", e, e2)
		}
	}
	if hits, err = st.FindTermsBySubstring(1, 0, "zh", "我有一辆极石汽车"); err != nil || len(hits) != 0 {
		t.Fatalf("停用包不得命中: n=%d err=%v", len(hits), err)
	}
	if hs, err := st.FindEntriesBySource(1, "zh", "极石"); err != nil || len(hs) != 0 {
		t.Fatalf("停用包精确查询不得命中: n=%d err=%v", len(hs), err)
	}
	if hs, err := st.FindEntriesBySourceScoped(1, 0, "zh", "极石"); err != nil || len(hs) != 0 {
		t.Fatalf("停用包 scoped 查询不得命中: n=%d err=%v", len(hs), err)
	}
}
