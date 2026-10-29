// ============ kbterms_scoped_test.go · 职责说明 ============
// 术语硬闸「可见域版」查询的射程锁（2026-10-29 第十一轮 T-B）。
// 五条：本行业可见 / 非本行业不可见 / 语言文化可见 / 链外部门不可见 / 空 allow 不放宽。
// 另加一条同 source 的优先级（企业包 > 行业包），它是 termReqs 去重取第一条的依据。
// ============================================
package store

import "testing"

func TestFindTermsBySubstringAllowed(t *testing.T) {
	env := newScopeEnv(t)
	var indAuto int64
	if err := env.st.db.QueryRow("SELECT id FROM kb_packages WHERE tenant_id=? AND code='auto'", SharedHostTenant).Scan(&indAuto); err != nil {
		t.Fatalf("取 auto 行业包失败: %v", err)
	}
	saveTerm := func(tid, pkg int64, zh, en string) {
		t.Helper()
		if _, err := env.st.SaveEntry(tid, pkg, 1, "zh", zh, "en", en, "term"); err != nil {
			t.Fatalf("写入术语 %s 失败: %v", zh, err)
		}
	}
	saveTerm(2, env.entP.ID, "重叠术语", "Ent term")
	saveTerm(SharedHostTenant, indAuto, "重叠术语", "Industry term")
	saveTerm(SharedHostTenant, indAuto, "行业专属", "Industry only")
	saveTerm(SharedHostTenant, env.indMed, "医疗专属", "Medical only")
	saveTerm(SharedHostTenant, env.locP.ID, "文化专属", "Locale only")
	saveTerm(2, env.dP.ID, "兄弟部门术语", "Sibling only")

	scope := env.scopeOf(t, env.orgC, false)
	allow := map[int64]bool{}
	for id := range scope.TenantPackIDs {
		allow[id] = true
	}
	for id := range scope.ChainPacks {
		allow[id] = true
	}
	for id := range scope.SharedPackIDs {
		allow[id] = true
	}
	for id := range scope.UniversalPackIDs {
		allow[id] = true
	}

	if got, err := env.st.FindTermsBySubstringAllowed("zh", "行业专属", allow); err != nil || len(got) != 1 {
		t.Fatalf("本行业术语应命中 1 条，实得 %d（err=%v）", len(got), err)
	}
	if got, _ := env.st.FindTermsBySubstringAllowed("zh", "医疗专属", allow); len(got) != 0 {
		t.Fatalf("非本行业术语不得命中，实得 %d", len(got))
	}
	if got, _ := env.st.FindTermsBySubstringAllowed("zh", "文化专属", allow); len(got) != 1 {
		t.Fatalf("语言文化包术语应命中 1 条，实得 %d", len(got))
	}
	if got, _ := env.st.FindTermsBySubstringAllowed("zh", "兄弟部门术语", allow); len(got) != 0 {
		t.Fatalf("链外部门术语不得命中，实得 %d", len(got))
	}
	got, _ := env.st.FindTermsBySubstringAllowed("zh", "前缀重叠术语后缀", allow)
	if len(got) != 2 || got[0].PackageID != env.entP.ID {
		t.Fatalf("同 source 应两条且企业包排第一（termReqs 去重取第一条），实得 %+v", got)
	}
	if got, err := env.st.FindTermsBySubstringAllowed("zh", "行业专属", nil); err != nil || got != nil {
		t.Fatalf("空 allow 必须返回空（不放宽成查全部），实得 %v err=%v", got, err)
	}
}

// TestTermSourcesAllowed W4 的去重口：只回可见域里的 layer=1 源词（小写），空 allow 回空。
func TestTermSourcesAllowed(t *testing.T) {
	env := newScopeEnv(t)
	var indAuto int64
	if err := env.st.db.QueryRow("SELECT id FROM kb_packages WHERE tenant_id=? AND code='auto'", SharedHostTenant).Scan(&indAuto); err != nil {
		t.Fatalf("取 auto 行业包失败: %v", err)
	}
	if _, err := env.st.SaveEntry(2, env.entP.ID, 1, "zh", "企业词", "en", "Ent", "term"); err != nil {
		t.Fatalf("写企业术语失败: %v", err)
	}
	if _, err := env.st.SaveEntry(SharedHostTenant, indAuto, 1, "zh", "行业词", "en", "Ind", "term"); err != nil {
		t.Fatalf("写行业术语失败: %v", err)
	}
	if _, err := env.st.SaveEntry(SharedHostTenant, env.indMed, 1, "zh", "医疗词", "en", "Med", "term"); err != nil {
		t.Fatalf("写医疗术语失败: %v", err)
	}
	scope := env.scopeOf(t, env.orgC, false)
	allow := map[int64]bool{}
	for id := range scope.TenantPackIDs {
		allow[id] = true
	}
	for id := range scope.ChainPacks {
		allow[id] = true
	}
	for id := range scope.SharedPackIDs {
		allow[id] = true
	}
	for id := range scope.UniversalPackIDs {
		allow[id] = true
	}
	got, err := env.st.TermSourcesAllowed(allow)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if !got["行业词"] {
		t.Fatalf("本行业术语应可见：%v", got)
	}
	if got["医疗词"] {
		t.Fatalf("跨行业术语不得可见：%v", got)
	}
	if empty, err := env.st.TermSourcesAllowed(nil); err != nil || len(empty) != 0 {
		t.Fatalf("空 allow 必须回空，实得 %v err=%v", empty, err)
	}
}
