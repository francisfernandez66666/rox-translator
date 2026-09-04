// ============ 本文件职责中文说明 ============
// KB 包默认结构单元测试：EnsureDefaultPackages 应幂等创建企业/行业/语言文化/部门四类默认包；
// 以及 2026-09-04 行业包/语言文化包宿主迁移到租户0 的回归锁定。
// =============================================
package store

import (
	"testing"

	"translator/internal/kb"
)

// TestMigrateSharedHostToZero 行业包/语言文化包宿主迁移回归（2026-09-04）。
// 旧设计把这些包硬编码宿主在租户1（ROX），本用例锁定：启动迁移 MigrateSharedHostToZero
// 把租户1的行业包/语言文化包及其条目/安全句/tm_segments 行统一搬到平台宿主租户0（SharedHostTenant），
// 企业包/部门包不受影响，且迁移幂等（重复调用不报错、不改动结果）。
func TestMigrateSharedHostToZero(t *testing.T) {
	env := newScopeEnv(t)
	// 造一个旧模型下的行业包壳：宿主在租户1（模拟存量，区别于 newScopeEnv 默认已建在租户0的）
	indOld, err := env.st.CreateKBPackage(1, 0, "legacy-ind", "旧宿主行业包", PackIndustry, PackRoleSource)
	if err != nil {
		t.Fatalf("建旧宿主行业包失败: %v", err)
	}
	// ★ 直接插旧态行（不经 SaveEntry——新代码会把行业包条目写进 tm_segments 租户0，会与旧态并存干扰断言）
	legacyHash := kb.MD5Hex("旧行业句")
	if _, err := env.st.db.Exec(
		"INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		1, indOld.ID, 2, "zh", "旧行业句", "en", "Legacy ind", "test", "2024-01-01T00:00:00", "2024-01-01T00:00:00"); err != nil {
		t.Fatalf("插入旧态 kb_entries 行失败: %v", err)
	}
	if _, err := env.st.db.Exec(
		"INSERT OR REPLACE INTO tm_segments (zh_hash, zh, tenant_id, priority, pack_id, en, updated_at) VALUES (?,?,?,?,?,?,?)",
		legacyHash, "旧行业句", 1, 2, indOld.ID, "Legacy ind legacy row", "2024-01-01T00:00:00"); err != nil {
		t.Fatalf("插入旧态 tm_segments 行失败: %v", err)
	}
	if _, err := env.st.SaveSafetyPhraseEx(1, indOld.ID, "en", "旧文化规则", "style", ""); err != nil {
		t.Fatalf("写入旧宿主安全句失败: %v", err)
	}
	// 迁移前：包/条目/检索层/安全句在租户1
	var pkgTid, entTid, segTid, spTid int64
	_ = env.st.db.QueryRow("SELECT tenant_id FROM kb_packages WHERE id=?", indOld.ID).Scan(&pkgTid)
	_ = env.st.db.QueryRow("SELECT tenant_id FROM kb_entries WHERE package_id=?", indOld.ID).Scan(&entTid)
	_ = env.st.db.QueryRow("SELECT tenant_id FROM tm_segments WHERE pack_id=? AND zh=?", indOld.ID, "旧行业句").Scan(&segTid)
	_ = env.st.db.QueryRow("SELECT tenant_id FROM kb_safety_phrases WHERE package_id=?", indOld.ID).Scan(&spTid)
	if pkgTid != 1 || entTid != 1 || segTid != 1 || spTid != 1 {
		t.Fatalf("前置断言失败：期望迁移前租户1, 实得 pkg=%d ent=%d seg=%d sp=%d", pkgTid, entTid, segTid, spTid)
	}
	// 执行迁移
	env.st.MigrateSharedHostToZero()
	// 迁移后：全部搬到租户0
	_ = env.st.db.QueryRow("SELECT tenant_id FROM kb_packages WHERE id=?", indOld.ID).Scan(&pkgTid)
	_ = env.st.db.QueryRow("SELECT tenant_id FROM kb_entries WHERE package_id=?", indOld.ID).Scan(&entTid)
	_ = env.st.db.QueryRow("SELECT tenant_id FROM tm_segments WHERE pack_id=? AND zh=?", indOld.ID, "旧行业句").Scan(&segTid)
	_ = env.st.db.QueryRow("SELECT tenant_id FROM kb_safety_phrases WHERE package_id=?", indOld.ID).Scan(&spTid)
	if pkgTid != 0 || entTid != 0 || segTid != 0 || spTid != 0 {
		t.Fatalf("迁移失败：期望宿主租户0, 实得 pkg=%d ent=%d seg=%d sp=%d", pkgTid, entTid, segTid, spTid)
	}
	// 企业包不受影响（租户2的企业包仍在租户2）
	var tenantPkgTid int64
	_ = env.st.db.QueryRow("SELECT tenant_id FROM kb_packages WHERE id=?", env.entP.ID).Scan(&tenantPkgTid)
	if tenantPkgTid != 2 {
		t.Fatalf("企业包被误迁移：期望仍在租户2, 实得 %d", tenantPkgTid)
	}
	// 幂等：重复调用不报错、不改变结果
	env.st.MigrateSharedHostToZero()
	_ = env.st.db.QueryRow("SELECT tenant_id FROM kb_packages WHERE id=?", indOld.ID).Scan(&pkgTid)
	if pkgTid != 0 {
		t.Fatalf("迁移不幂等：第二次调用后租户=%d, 期望0", pkgTid)
	}
}

// TestEnsureDefaultPackagesIncludesDepartment 验证 EnsureDefaultPackages 幂等创建企业/行业/语言文化/部门四类默认包。
func TestEnsureDefaultPackagesIncludesDepartment(t *testing.T) {
	s := newTestStore(t)
	// 初始无包，首次调用应创建 4 类默认包
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 失败: %v", err)
	}
	pkgs, err := s.ListKBPackages(1)
	if err != nil {
		t.Fatalf("ListKBPackages 失败: %v", err)
	}
	hasDepartment := false
	for _, p := range pkgs {
		if p.PackType == PackDepartment {
			hasDepartment = true
		}
	}
	if !hasDepartment {
		t.Fatalf("默认包中缺少部门包(department)，实际包类型: %+v", pkgs)
	}
	// 幂等：再次调用不应重复创建
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 幂等失败: %v", err)
	}
	pkgs2, _ := s.ListKBPackages(1)
	if len(pkgs2) != len(pkgs) {
		t.Fatalf("幂等失败：调用前后包数不一致 %d -> %d", len(pkgs), len(pkgs2))
	}
}
