// ============================================================================
// ★ H12 术语检索 SearchTerms 测试：精确优先/反向命中/语言过滤/停用包不可见/
//
//	租户隔离/limit。
//
// ============================================================================
package store

import "testing"

func h12bSeed(t *testing.T) *Store {
	t.Helper()
	s := newTestStoreWithTenants(t)
	mustExec := func(q string, args ...interface{}) {
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mustExec(`INSERT INTO kb_packages (id, tenant_id, code, name, pack_type, role, enabled, org_id, share_cross_dept) VALUES (10,1,'t1','企业包','tenant','source',1,0,1)`)
	mustExec(`INSERT INTO kb_packages (id, tenant_id, code, name, pack_type, role, enabled, org_id, share_cross_dept) VALUES (11,1,'t2','停用包','tenant','source',0,0,1)`)
	mustExec(`INSERT INTO kb_packages (id, tenant_id, code, name, pack_type, role, enabled, org_id, share_cross_dept) VALUES (12,2,'t3','他租户','tenant','source',1,0,1)`)
	ins := func(tid, pkg int64, src, tgtLang, tgt string) {
		mustExec(`INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text) VALUES (?, ?, 1, 'zh', ?, ?, ?)`, tid, pkg, src, tgtLang, tgt)
	}
	ins(1, 10, "服务器", "en", "server")
	ins(1, 10, "云服务器", "en", "cloud server")
	ins(1, 10, "防火墙", "de", "Firewall")
	ins(1, 11, "交换机", "en", "switch") // 停用包
	ins(2, 12, "服务器", "en", "OTHER")  // 他租户：租户 1 检索不可见
	return s
}

func TestH12bSearchTerms(t *testing.T) {
	s := h12bSeed(t)
	// 精确优先：查「服务器」应命中 服务器(exact) + 云服务器(前缀包含)
	hits, err := s.SearchTerms(1, 0, "服务器", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("应命中 2 条: %+v", hits)
	}
	if !hits[0].Exact || hits[0].SourceText != "服务器" {
		t.Fatalf("精确命中应排首位: %+v", hits[0])
	}
	// 反向检索：查英文 target 也能命中
	rev, err := s.SearchTerms(1, 0, "cloud server", "", 20)
	if err != nil || len(rev) != 1 || rev[0].SourceText != "云服务器" {
		t.Fatalf("反向命中失败: %+v %v", rev, err)
	}
	// 语言过滤
	de, err := s.SearchTerms(1, 0, "防火墙", "de", 20)
	if err != nil || len(de) != 1 || de[0].TargetText != "Firewall" {
		t.Fatalf("lang 过滤失败: %+v %v", de, err)
	}
	if en, _ := s.SearchTerms(1, 0, "防火墙", "en", 20); len(en) != 0 {
		t.Fatal("lang=en 不应命中 de 术语")
	}
	// 停用包不可见 + 租户隔离 + LIKE 通配符转义
	for _, q := range []string{"交换机", "OTHER", "%"} {
		if got, _ := s.SearchTerms(1, 0, q, "", 20); len(got) != 0 {
			t.Fatalf("%q 不应命中: %+v", q, got)
		}
	}
	// limit
	lim, _ := s.SearchTerms(1, 0, "服务器", "", 1)
	if len(lim) != 1 {
		t.Fatalf("limit 失效: %d", len(lim))
	}
	if _, err := s.SearchTerms(1, 0, "  ", "", 20); err == nil {
		t.Fatal("空检索词应报错")
	}
}
