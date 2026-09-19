// ============================================================================
// store/persona_test.go — 角色功能（job_role + persona 包）自动化断言（2026-09-19）
// 覆盖：①PersonaMigrate 幂等（重复执行不重复种入）；②users.job_role 读写回路 +
// ListUsers 行式 Scan 列契约（2026-08-26 静默吞行事故的防复刻断言）；
// ③BuildUserPackScope 按 job_role 装配（350 优先级档、链内采用域、停用包不装配）；
// ④scoped 检索可见性（角色包条目对匹配用户可命中、对其他用户不可见）。
// ============================================================================
package store

import (
	"testing"
)

// personaCount 统计宿主租户0 的角色包数量。
func personaCount(t *testing.T, st *Store) int {
	t.Helper()
	pkgs, err := st.ListPersonas()
	if err != nil {
		t.Fatalf("ListPersonas 失败: %v", err)
	}
	return len(pkgs)
}

// TestPersonaMigrateIdempotent 出厂角色字典幂等：环境已建一次，再跑一遍不得翻倍。
func TestPersonaMigrateIdempotent(t *testing.T) {
	st, _ := newKBEnv(t)
	before := personaCount(t, st)
	if before < 8 {
		t.Fatalf("store.New 应种入 8 个出厂角色包，实际 %d", before)
	}
	st.PersonaMigrate()
	if after := personaCount(t, st); after != before {
		t.Fatalf("PersonaMigrate 重复执行导致角色包翻倍: %d → %d", before, after)
	}
}

// TestJobRoleRoundTrip users.job_role 写入/读出 + 行式 Scan 列契约（job_role 必须在列表结果中）。
func TestJobRoleRoundTrip(t *testing.T) {
	st, _ := newKBEnv(t)
	u, err := st.CreateUser(2, "persona_user", "hash", "角色测试员", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	if u.JobRole != "" {
		t.Fatalf("新建用户 job_role 应为空，实际 %q", u.JobRole)
	}
	if err := st.SetJobRole(u.ID, 2, "frontend"); err != nil {
		t.Fatalf("SetJobRole 失败: %v", err)
	}
	got, err := st.GetUser(u.ID, 2) // scanUser 单行路径
	if err != nil || got.JobRole != "frontend" {
		t.Fatalf("GetUser job_role 回读失败: %v %q", err, got.JobRole)
	}
	users, err := st.ListUsers(2) // 行式 Scan 路径（列数不匹配会静默吞行 → 空列表）
	if err != nil {
		t.Fatalf("ListUsers 失败: %v", err)
	}
	var found bool
	for _, x := range users {
		if x.ID == u.ID {
			found = true
			if x.JobRole != "frontend" {
				t.Fatalf("ListUsers 行式 Scan 未带出 job_role: %q", x.JobRole)
			}
		}
	}
	if !found {
		t.Fatal("ListUsers 丢失用户行（userCols 与 Scan 列契约被破坏）")
	}
	// 清除角色（空串=未选）
	if err := st.SetJobRole(u.ID, 2, ""); err != nil {
		t.Fatalf("清除 job_role 失败: %v", err)
	}
	if got, _ := st.GetUser(u.ID, 2); got.JobRole != "" {
		t.Fatalf("清除后 job_role 应为空，实际 %q", got.JobRole)
	}
}

// TestBuildUserPackScopePersona 检索装配：job_role 命中启用角色包（Rank 350/采用域）；
// 空角色不装配；停用包不装配；未知角色 code 不装配。
func TestBuildUserPackScopePersona(t *testing.T) {
	st, kdb := newKBEnv(t)
	p, err := st.FindEnabledPersonaByCode("frontend")
	if err != nil {
		t.Fatalf("出厂角色 frontend 应存在且启用: %v", err)
	}
	// 角色包条目（宿主租户0 检索行）
	if _, err := st.SaveEntry(SharedHostTenant, p.ID, 2, "zh", "组件复用", "en", "Component reuse [PERSONA]", "test"); err != nil {
		t.Fatalf("写入角色条目失败: %v", err)
	}

	scope, err := st.BuildUserPackScope(2, []int64{}, false, "frontend")
	if err != nil {
		t.Fatalf("BuildUserPackScope 失败: %v", err)
	}
	if !scope.PersonaPackIDs[p.ID] {
		t.Fatal("job_role=frontend 未装配角色包进 PersonaPackIDs")
	}
	if scope.JobRole != "frontend" {
		t.Fatalf("scope.JobRole 应为 frontend，实际 %q", scope.JobRole)
	}
	if r := scope.Rank(p.ID, SharedHostTenant); r != 350 {
		t.Fatalf("角色包优先级应为 350 档，实际 %d", r)
	}
	if !scope.InChain(p.ID) {
		t.Fatal("角色包应属直接采用域（InChain=true）")
	}
	// scoped 检索：匹配用户可命中
	if _, _, err := kdb.FindExactScoped("组件复用", 2, scope); err != nil {
		t.Fatalf("frontend 用户应命中角色包条目: %v", err)
	}
	// 空角色：不装配、不可见
	scopeNone, _ := st.BuildUserPackScope(2, []int64{}, false, "")
	if len(scopeNone.PersonaPackIDs) != 0 {
		t.Fatal("空 job_role 不应装配任何角色包")
	}
	if _, _, err := kdb.FindExactScoped("组件复用", 2, scopeNone); err == nil {
		t.Fatal("未选角色用户不应命中角色包条目（跨角色泄漏）")
	}
	// 其它角色：不可见
	scopeBack, _ := st.BuildUserPackScope(2, []int64{}, false, "backend")
	if scopeBack.PersonaPackIDs[p.ID] {
		t.Fatal("backend 用户不应装配 frontend 角色包")
	}
	// 停用后：FindEnabledPersonaByCode 失败、装配集不含金色包
	if err := st.TogglePersona(p.ID, 0); err != nil {
		t.Fatalf("停用角色包失败: %v", err)
	}
	if _, err := st.FindEnabledPersonaByCode("frontend"); err == nil {
		t.Fatal("停用后 FindEnabledPersonaByCode 应返回未找到")
	}
	scopeOff, _ := st.BuildUserPackScope(2, []int64{}, false, "frontend")
	if scopeOff.PersonaPackIDs[p.ID] {
		t.Fatal("停用角色包不得进入检索装配")
	}
	// 未知 code 安全回落（不装配、不报错）
	scopeUnknown, err := st.BuildUserPackScope(2, []int64{}, false, "no_such_role")
	if err != nil || len(scopeUnknown.PersonaPackIDs) != 0 {
		t.Fatalf("未知角色应安全回落空集: %v %v", err, scopeUnknown.PersonaPackIDs)
	}
}

// TestPersonaReferenced 删除前置校验：角色被用户引用时禁止删除。
func TestPersonaReferenced(t *testing.T) {
	st, _ := newKBEnv(t)
	u, err := st.CreateUser(2, "ref_user", "hash", "引用测试", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	if err := st.SetJobRole(u.ID, 2, "sales"); err != nil {
		t.Fatalf("SetJobRole 失败: %v", err)
	}
	used, err := st.PersonaReferenced("sales")
	if err != nil || !used {
		t.Fatalf("sales 应被用户引用: %v %v", used, err)
	}
	if err := st.SetJobRole(u.ID, 2, ""); err != nil {
		t.Fatalf("清除 job_role 失败: %v", err)
	}
	if used, _ = st.PersonaReferenced("sales"); used {
		t.Fatal("清除引用后 PersonaReferenced 应为 false")
	}
}
