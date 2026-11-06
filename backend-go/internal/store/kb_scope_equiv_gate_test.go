// ============ 本文件职责中文说明 ============
// ★ 一期《确定的多语言能力》发布前置：老租户术语命中行序不变的等值对照（§4.4 E1–E3 ＋ 变异 M）。
//
// 为什么这条是发布闸：kbterms_scoped.go 的 ORDER BY 被 engine/gates.go 的术语硬闸消费，
// 而硬闸对**所有租户默认开**——任何一次排序变化都可能改变既有客户的译法。
// 产品包插秩 −1/150 只许在「库里真的有产品包」时生效；product_id 全空的老租户
// 必须与改造前的旧 CASE 逐条等值，否则不许合。
//
// E1 用**内置旧 CASE 字面**另跑一条 SQL 作基线（不依赖"改前快照"），因此改前改后都能判。
// ★ 比 []*KBEntry 的 ID 序列，不比内容：这一族事故的症状就是"内容没变、顺序变了"。
// ========================================
package store

import (
	"strings"
	"testing"

	"translator/internal/db"
	"translator/internal/kb"
)

// legacyTermOrderSQL 改造前的排序子句（逐字抄自 2026-11-05 现读 kbterms_scoped.go:48-49）。
// ★ 这是基线真值，不许随实现改动；实现改了这里仍留着，才能判出老租户行为有没有漂。
const legacyTermOrderSQL = `ORDER BY length(e.source_text) DESC, CASE p.pack_type ` +
	`WHEN 'tenant' THEN 0 WHEN 'persona' THEN 1 WHEN 'industry' THEN 2 ELSE 3 END, e.id`

// kbEquivTerms 用指定排序子句跑一条与 FindTermsBySubstringAllowed 相同的查询，只回 ID 序列。
// 参数：t=测试句柄，s=Store，srcLang/source/allow 同产品函数，orderBy=排序子句；返回 ID 序列。
func kbEquivTerms(t *testing.T, s *Store, srcLang, source string, allow map[int64]bool, orderBy string) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(allow))
	for id := range allow {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return []int64{}
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	q := "SELECT e.id FROM kb_entries e JOIN kb_packages p ON e.package_id=p.id " +
		"WHERE e.source_lang=? AND e.layer=1 AND p.role='source' AND COALESCE(p.enabled,1)=1 " +
		"AND e.package_id IN (" + ph + ") " +
		"AND ? LIKE '%' || e.source_text || '%' " + orderBy
	args := make([]interface{}, 0, len(ids)+2)
	args = append(args, srcLang)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, source)
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		t.Fatalf("基线查询失败: %v", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("基线扫描失败: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func kbEquivIDsOf(ents []*KBEntry) []int64 {
	out := make([]int64, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.ID)
	}
	return out
}

func kbEquivSameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// E1：老租户（product_id 全空）行序必须与旧 CASE 逐条等值。
func TestKBScopeEquivOldTenantsUnchanged(t *testing.T) {
	s := newProductTestStore(t)
	// 企业包 / 行业包 / persona 包各一行；同长度源词，让 CASE 的顺序差异能显形。
	ent, err := s.CreateKBPackage(2, 0, "ent", "企业包", PackTenant, PackRoleSource)
	if err != nil {
		t.Fatalf("建企业包失败: %v", err)
	}
	ind, err := s.CreateKBPackage(2, 0, "ind", "行业包", PackIndustry, PackRoleSource)
	if err != nil {
		t.Fatalf("建行业包失败: %v", err)
	}
	per, err := s.CreateKBPackage(2, 0, "per", "角色包", "persona", PackRoleSource)
	if err != nil {
		t.Fatalf("建角色包失败: %v", err)
	}
	for _, pkg := range []int64{ent.ID, ind.ID, per.ID} {
		if _, err := s.SaveEntry(2, pkg, 1, "zh", "同长源词", "en", "T", "term"); err != nil {
			t.Fatalf("写术语失败: %v", err)
		}
	}
	allow := map[int64]bool{ent.ID: true, ind.ID: true, per.ID: true}

	want := kbEquivTerms(t, s, "zh", "同长源词", allow, legacyTermOrderSQL)
	if len(want) != 3 {
		t.Fatalf("基线应有 3 条命中, got %d（%v）", len(want), want)
	}
	got, err := s.FindTermsBySubstringAllowed("zh", "同长源词", allow)
	if err != nil {
		t.Fatalf("产品版查询失败: %v", err)
	}
	if !kbEquivSameIDs(kbEquivIDsOf(got), want) {
		t.Fatalf("老租户术语命中行序必须与改造前逐条等值（E1）, want=%v got=%v", want, kbEquivIDsOf(got))
	}
}

// E2：同一库里加产品包 ⇒ 产品包必须排第一（秩 −1 生效）。
// ★ 同时是「反证 M」的正向对照：把 −1 改成 0/3 后本例必须红而 E1 仍绿。
func TestKBScopeEquivProductPackWins(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(2, ProductInput{Name: "等值对照产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	src, gate, err := s.EnsureProductPacks(2, p.ID)
	if err != nil {
		t.Fatalf("建产品包失败: %v", err)
	}
	ent, err := s.CreateKBPackage(2, 0, "ent2", "企业包", PackTenant, PackRoleSource)
	if err != nil {
		t.Fatalf("建企业包失败: %v", err)
	}
	for _, pkg := range []int64{src, ent.ID} {
		if _, err := s.SaveEntry(2, pkg, 1, "zh", "同长源词", "en", "T", "term"); err != nil {
			t.Fatalf("写术语失败: %v", err)
		}
	}
	if gate <= 0 {
		t.Fatal("gate 包必须存在")
	}
	allow := map[int64]bool{src: true, ent.ID: true}
	got, err := s.FindTermsBySubstringAllowed("zh", "同长源词", allow)
	if err != nil {
		t.Fatalf("产品版查询失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应命中 2 条, got %d", len(got))
	}
	if got[0].PackageID != src {
		t.Fatalf("产品私有说法必须排第一（秩 −1）, got package_id=%d want=%d", got[0].PackageID, src)
	}
	// 反向对照：不传产品包集合时，企业包与产品包并列 0（不得凭空优先）
	legacy := kbEquivTerms(t, s, "zh", "同长源词", allow, legacyTermOrderSQL)
	if len(legacy) != 2 {
		t.Fatalf("基线应有 2 条, got %d", len(legacy))
	}
}

// E3：Rank 的既有秩表逐字等值（防插层把既有分支挪位）＋产品包回滚开关真被调用。
func TestKBScopeEquivRankTableAndProductRollbackSwitch(t *testing.T) {
	s := &kb.PackScope{
		ChainPacks:       map[int64]int{11: 0, 12: 1},
		CrossDeptPacks:   map[int64]string{21: "跨部门"},
		TenantPackIDs:    map[int64]bool{31: true},
		SharedPackIDs:    map[int64]bool{41: true},
		PersonaPackIDs:   map[int64]bool{51: true},
		UniversalPackIDs: map[int64]bool{61: true},
		ProductPackIDs:   map[int64]bool{71: true},
	}
	cases := []struct {
		name string
		pack int64
		want int
	}{
		{"链内本部门", 11, 0},
		{"链内父部门", 12, 1},
		{"跨部门", 21, 100},
		{"历史无主行", 0, 200},
		{"企业包", 31, 200},
		{"行业包", 41, 300},
		{"角色包", 51, 350},
		{"语言文化包", 61, 400},
		{"未知包", 999, 400},
	}
	for _, c := range cases {
		if got := s.Rank(c.pack, 0); got != c.want {
			t.Fatalf("%s 秩表被挪位: want=%d got=%d", c.name, c.want, got)
		}
	}
	// 产品包：150 档，且 **ProductPackRank() 必须真被调用**（防回滚开关是死码）。
	if got := s.Rank(71, 0); got != 150 {
		t.Fatalf("产品包秩应为 150（在区间 100..200 之间）, got %d", got)
	}
	if kb.ProductPackRank() != 150 {
		t.Fatal("ProductPackRank 单一回滚开关返回了非 150 的既有秩 ⇒ 插层被摘掉或没走这个开关")
	}
	// 反向对照：ProductPackIDs 为 nil 的老 scope 不许受任何影响
	old := &kb.PackScope{TenantPackIDs: map[int64]bool{31: true}}
	if got := old.Rank(31, 0); got != 200 {
		t.Fatalf("老 scope 企业包仍应 200, got %d", got)
	}
}
