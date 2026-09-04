// ============ 本文件职责中文说明 ============
// kbpackages_scope_test.go · KB 组织继承链与部门隔离回归测试
// （2026-08-26《KB组织继承链与部门隔离改造方案》§五 全场景）。
// 覆盖：三级就近覆盖 / 兄弟隔离 / 跨部门精确采用+打标 / 空链守卫 /
//
//	pack_id=0 历史行 / 移动部门重继承 / 包级 opt-out / 模糊分层 / 共享行业码校验。
//
// 环境：临时文件库（kb.Open + store.New 共享连接，DSN 自带 IMMEDIATE/WAL）。
// ========================================
package store

import (
	"path/filepath"
	"testing"

	"translator/internal/kb"
	"translator/internal/tenant"
)

// newKBEnv 建立共享同一 SQLite 文件的 KB + 租户 + Store 测试环境
// （与生产 main.go 装配顺序一致：kb.Open → tenant.NewStore → store.New 共用一条连接）。
func newKBEnv(t *testing.T) (*Store, *kb.KBDatabase) {
	t.Helper()
	kdb, err := kb.Open(filepath.Join(t.TempDir(), "scope.db"))
	if err != nil {
		t.Fatalf("打开 KB 失败: %v", err)
	}
	t.Cleanup(func() { kdb.Close() })
	if err := kdb.EnsureTenantMigration(); err != nil {
		t.Fatalf("tm_segments 迁移失败: %v", err)
	}
	// tenants/orgs 表由 tenant.Store 负责建表（生产同款装配）
	ts, err := tenant.NewStore(kdb.RawDB())
	if err != nil {
		t.Fatalf("创建租户存储失败: %v", err)
	}
	_ = ts
	st, err := New(kdb.RawDB())
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return st, kdb
}

// scopeEnv 标准测试拓扑：
//
//	租户2（注册行业 auto）：组织 A(根) > B > C；A 下另有兄弟分支 D
//	包：企业包 entP；部门包 cP(orgC)/bP(orgB)/dP(orgD,愿共享)/dPOpt(orgD,opt-out)
//	平台租户1：行业包 indAuto(auto)/indMed(medical) + 文化包 locP（共享宿主）
type scopeEnv struct {
	st     *Store
	kdb    *kb.KBDatabase
	entP   *KBPackage
	cP     *KBPackage
	bP     *KBPackage
	dP     *KBPackage
	dPOpt  *KBPackage
	orgA   int64
	orgB   int64
	orgC   int64
	orgD   int64
	indMed int64
	locP   *KBPackage
	crossP *KBPackage
}

// save 条目写通（SaveEntry 自动落 tm_segments 的 priority/host/pack_id）。
func (e *scopeEnv) save(t *testing.T, tid, pkg int64, zh, en string) {
	t.Helper()
	if _, err := e.st.SaveEntry(tid, pkg, 2, "zh", zh, "en", en, "test"); err != nil {
		t.Fatalf("写入条目失败 %s: %v", zh, err)
	}
}

// newScopeEnv 装配标准拓扑与种子条目。
func newScopeEnv(t *testing.T) *scopeEnv {
	t.Helper()
	st, kdb := newKBEnv(t)
	e := &scopeEnv{st: st, kdb: kdb}

	var err error
	// 种子租户行（tenant.NewStore 仅保证表结构；注册行业决定共享行业包可见性）
	if _, err = st.db.Exec("INSERT OR IGNORE INTO tenants (id, code, name, status, industry) VALUES (2,'t2','测试租户B','active','auto')"); err != nil {
		t.Fatalf("种子租户失败: %v", err)
	}
	// 组织树 A > B > C；A > D（兄弟分支）
	if e.orgA, err = st.createOrgID(2, 0, "A", "root"); err != nil {
		t.Fatalf("建 A 失败: %v", err)
	}
	if e.orgB, err = st.createOrgID(2, e.orgA, "B", "org"); err != nil {
		t.Fatalf("建 B 失败: %v", err)
	}
	if e.orgC, err = st.createOrgID(2, e.orgB, "C", "dept"); err != nil {
		t.Fatalf("建 C 失败: %v", err)
	}
	if e.orgD, err = st.createOrgID(2, e.orgA, "D", "dept"); err != nil {
		t.Fatalf("建 D 失败: %v", err)
	}

	// 包（显式错误检查；Go 不允许 f(t, g()) 形式的多返回值部分展开）
	if e.entP, err = st.CreateKBPackage(2, 0, "ent", "企业包", PackTenant, PackRoleSource); err != nil {
		t.Fatalf("建企业包失败: %v", err)
	}
	if e.cP, err = st.CreateKBPackageForOrg(2, 0, "c", "C包", PackDepartment, PackRoleSource, e.orgC); err != nil {
		t.Fatalf("建 C 包失败: %v", err)
	}
	if e.bP, err = st.CreateKBPackageForOrg(2, 0, "b", "B包", PackDepartment, PackRoleSource, e.orgB); err != nil {
		t.Fatalf("建 B 包失败: %v", err)
	}
	if e.dP, err = st.CreateKBPackageForOrg(2, 0, "d", "D包", PackDepartment, PackRoleSource, e.orgD); err != nil {
		t.Fatalf("建 D 包失败: %v", err)
	}
	if e.dPOpt, err = st.CreateKBPackageForOrg(2, 0, "dopt", "D保密包", PackDepartment, PackRoleSource, e.orgD); err != nil {
		t.Fatalf("建 D 保密包失败: %v", err)
	}
	if err = st.SetKBPackageCrossDeptShare(e.dPOpt.ID, 2, 0); err != nil { // 包级 opt-out
		t.Fatalf("设置 opt-out 失败: %v", err)
	}
	var indAuto *KBPackage
	if indAuto, err = st.CreateKBPackage(SharedHostTenant, 0, "auto", "汽车行业包", PackIndustry, PackRoleSource); err != nil {
		t.Fatalf("建汽车行业包失败: %v", err)
	}
	var indMedPkg *KBPackage
	if indMedPkg, err = st.CreateKBPackage(SharedHostTenant, 0, "med-ind", "医疗行业包", PackIndustry, PackRoleSource); err != nil {
		t.Fatalf("建医疗行业包失败: %v", err)
	}
	e.indMed = indMedPkg.ID
	if e.locP, err = st.CreateKBPackage(SharedHostTenant, 0, "loc", "文化包", PackLocale, PackRoleSource); err != nil {
		t.Fatalf("建文化包失败: %v", err)
	}
	if e.crossP, err = st.CreateKBPackageForOrg(2, 0, "cross", "跨部门包", PackCrossDept, PackRoleSource, e.orgC); err != nil {
		t.Fatalf("建跨部门包失败: %v", err)
	}
	// 跨部门包涵盖部门 = {C}（使用/维护范围为 C 部门成员/管理员）
	if err = st.SetKBPackageCrossScope(e.crossP.ID, 2, false, []int64{e.orgC}); err != nil {
		t.Fatalf("设置跨部门包范围失败: %v", err)
	}
	e.crossP.CrossOrgs = []int64{e.orgC}

	// 种子条目
	e.save(t, 2, e.entP.ID, "刹车系统", "Braking system [ENT]")             // 企业层
	e.save(t, 2, e.cP.ID, "刹车系统", "Brake sys [C]")                      // 链内更近 → 应胜出
	e.save(t, 2, e.cP.ID, "C专属术语", "C-only term")                       // 链内独有
	e.save(t, 2, e.bP.ID, "B层术语", "B-level term")                       // 祖先链第 1 层
	e.save(t, 2, e.dP.ID, "跨部门共享话术", "Cross-dept shared script")        // 兄弟·愿共享
	e.save(t, 2, e.dPOpt.ID, "D机密流程", "D secret process")               // 兄弟·已退出共享
	e.save(t, SharedHostTenant, indAuto.ID, "行业通用句", "Industry common") // 匹配行业（auto）
	e.save(t, SharedHostTenant, e.indMed, "医疗专用句", "Medical only")      // 非本行业 → 不可见
	e.save(t, SharedHostTenant, e.locP.ID, "文化习惯句", "Locale habit")     // 文化包全系统共享
	// pack_id=0 历史沉淀行（直插检索层）
	if _, err := st.db.Exec(
		"INSERT INTO tm_segments (zh_hash, zh, tenant_id, pack_id, priority, en) VALUES (?,?,?,?,?,?)",
		kb.MD5Hex("历史沉淀行"), "历史沉淀行", 2, 0, 9, "Legacy row"); err != nil {
		t.Fatalf("插入历史行失败: %v", err)
	}
	return e
}

// createOrgID 建组织并返回 ID。
func (st *Store) createOrgID(tid, parent int64, name, typ string) (int64, error) {
	o, err := st.CreateOrg(tid, parent, name, typ)
	if err != nil {
		return 0, err
	}
	return o.ID, nil
}

// scopeOf 组装指定组织的 scope（allowCross 显式传入便于开关场景测试）。
func (e *scopeEnv) scopeOf(t *testing.T, orgID int64, allowCross bool) *kb.PackScope {
	t.Helper()
	chain, err := e.st.OrgAncestorIDs(2, orgID)
	if err != nil {
		t.Fatalf("祖先链失败: %v", err)
	}
	scope, err := e.st.BuildPackScope(2, chain, allowCross)
	if err != nil {
		t.Fatalf("BuildPackScope 失败: %v", err)
	}
	scope.Chain = chain
	return scope
}

// find 精确命中便捷封装，返回 (译文en, 来源, 是否命中)。
func (e *scopeEnv) find(t *testing.T, scope *kb.PackScope, zh string) (string, string, bool) {
	t.Helper()
	r, src, err := e.kdb.FindExactScoped(zh, 2, scope)
	if err != nil {
		return "", "", false
	}
	return r.Langs["en"], src, true
}

// TestScopeNearestWins 三级就近覆盖：C 用户同名术语应命中 C 包而非企业包；
// 删除 C 包行后回落企业包（模拟：直接摘除该行验证回退）。
func TestScopeNearestWins(t *testing.T) {
	env := newScopeEnv(t)
	scope := env.scopeOf(t, env.orgC, true)

	en, src, ok := env.find(t, scope, "刹车系统")
	if !ok || en != "Brake sys [C]" || src != "chain" {
		t.Fatalf("期望 C 包就近胜出, got ok=%v src=%s en=%q", ok, src, en)
	}
	// 摘除 C 包条目（等价停用包）→ 回落企业层
	if _, err := env.st.db.Exec("DELETE FROM tm_segments WHERE zh=? AND pack_id=?", "刹车系统", env.cP.ID); err != nil {
		t.Fatal(err)
	}
	env.st.db.Exec("DELETE FROM kb_entries WHERE package_id=? AND source_text=?", env.cP.ID, "刹车系统")
	en, src, ok = env.find(t, scope, "刹车系统")
	if !ok || en != "Braking system [ENT]" || src != "chain" {
		t.Fatalf("期望回落企业包, got ok=%v src=%s en=%q", ok, src, en)
	}
}

// TestScopeAncestorInheritance 祖先链第 1 层：B 包术语对 C 用户可见且来源为链内。
func TestScopeAncestorInheritance(t *testing.T) {
	env := newScopeEnv(t)
	scope := env.scopeOf(t, env.orgC, false) // 关闭跨部门也不影响链内

	en, src, ok := env.find(t, scope, "B层术语")
	if !ok || en != "B-level term" || src != "chain" {
		t.Fatalf("祖先链继承失败, got ok=%v src=%s en=%q", ok, src, en)
	}
}

// TestScopeSiblingIsolation 兄弟分支隔离：D 的两个包对 C 用户均不可见
// （opt-out 包彻底不可见；愿共享包走跨部门回退且打标来源）。
func TestScopeSiblingIsolation(t *testing.T) {
	env := newScopeEnv(t)
	scope := env.scopeOf(t, env.orgC, true)

	// opt-out 包：任何路径不可见
	if _, _, ok := env.find(t, scope, "D机密流程"); ok {
		t.Fatal("opt-out 兄弟包不应可见")
	}
	// 愿共享包：链内零命中时经段二回退命中，src=cross
	en, src, ok := env.find(t, scope, "跨部门共享话术")
	if !ok || en != "Cross-dept shared script" || src != "cross" {
		t.Fatalf("跨部门精确回退失败, got ok=%v src=%s en=%q", ok, src, en)
	}
	if nm := scope.CrossName(env.dP.ID); nm == "" {
		t.Fatal("跨部门打标缺少来源包名")
	}
}

// TestScopeSwitchOff 开关关闭：跨部门回退层完全不发生（愿共享包也不可见）。
func TestScopeSwitchOff(t *testing.T) {
	env := newScopeEnv(t)
	scope := env.scopeOf(t, env.orgC, false)

	if _, _, ok := env.find(t, scope, "跨部门共享话术"); ok {
		t.Fatal("开关关闭时兄弟包不应可见")
	}
}

// TestScopeEmptyChain 空链守卫：未挂组织用户不见任何部门包（含愿共享的），只见企业/历史/共享层。
func TestScopeEmptyChain(t *testing.T) {
	env := newScopeEnv(t)
	scope := env.scopeOf(t, 0, true) // org=0

	if _, _, ok := env.find(t, scope, "C专属术语"); ok {
		t.Fatal("无组织用户不应见部门包")
	}
	if _, _, ok := env.find(t, scope, "跨部门共享话术"); ok {
		t.Fatal("无组织用户不应触发跨部门回退")
	}
	if en, src, ok := env.find(t, scope, "刹车系统"); !ok || en != "Braking system [ENT]" || src != "chain" {
		t.Fatalf("无组织用户应见企业包, got ok=%v en=%q src=%s", ok, en, src)
	}
}

// TestScopeLegacyRowAndShared 历史无主行(pack_id=0)+匹配行业+文化包可见；非本行业不可见。
func TestScopeLegacyRowAndShared(t *testing.T) {
	env := newScopeEnv(t)
	scope := env.scopeOf(t, env.orgC, false)

	if en, _, ok := env.find(t, scope, "历史沉淀行"); !ok || en != "Legacy row" {
		t.Fatalf("历史行应可见, got ok=%v en=%q", ok, en)
	}
	if en, _, ok := env.find(t, scope, "行业通用句"); !ok || en != "Industry common" {
		t.Fatalf("匹配行业包应可见, got ok=%v en=%q", ok, en)
	}
	if _, _, ok := env.find(t, scope, "医疗专用句"); ok {
		t.Fatal("非本注册行业的平台行业包不应可见（审计 #9 回归）")
	}
	if en, _, ok := env.find(t, scope, "文化习惯句"); !ok || en != "Locale habit" {
		t.Fatalf("文化包应全系统可见, got ok=%v en=%q", ok, en)
	}
}

// TestScopeHostTenantIndustryFilter 企业租户按注册行业隔离行业包回归（2026-09-04）。
// 背景：行业包/语言文化包为平台级全局资源，宿主在共享宿主租户0（SharedHostTenant）；
// 旧规则 `tid==1 全放行` 使原宿主（如 ROX=汽车）在翻译检索时装配全部无关行业包——已移除。
// 本用例锁定：企业租户（此处以租户1 ROX 为例）同样只装配与本公司注册行业（auto）匹配的
// 行业包，医疗行业包不进入 SharedPackIDs，其术语不可见。
func TestScopeHostTenantIndustryFilter(t *testing.T) {
	env := newScopeEnv(t)
	// 企业租户1注册行业 auto（演示 ROX=汽车）；仅匹配 auto 行业包
	if _, err := env.st.db.Exec("INSERT OR IGNORE INTO tenants (id, code, name, status, industry) VALUES (1,'rox','ROX','active','auto')"); err != nil {
		t.Fatalf("种子租户失败: %v", err)
	}
	chain, err := env.st.OrgAncestorIDs(1, 0) // 企业租户无组织，空链
	if err != nil {
		t.Fatalf("祖先链失败: %v", err)
	}
	scope, err := env.st.BuildPackScope(1, chain, true)
	if err != nil {
		t.Fatalf("BuildPackScope(企业租户1) 失败: %v", err)
	}
	// 匹配行业包（auto）应装配；非本行业的平台行业包（med-ind）不应装配
	findT1 := func(zh string) bool {
		r, _, err := env.kdb.FindExactScoped(zh, 1, scope)
		return err == nil && r != nil
	}
	if !findT1("行业通用句") {
		t.Fatal("宿主租户应见匹配注册行业的行业包（auto）")
	}
	if findT1("医疗专用句") {
		t.Fatal("宿主租户不应见非本注册行业的平台行业包（med-ind）")
	}
}

// TestScopeMoveOrg 移动部门后继承随之变化：
// 把 C 挂到 D 之下 → D 进入 C 的祖先链，其包从「跨部门回退」转为「链内可见」。
func TestScopeMoveOrg(t *testing.T) {
	env := newScopeEnv(t)
	// 移动前基线：D 是 C 的兄弟分支，关闭开关时不可见、开启时仅回退可见
	scopeBefore := env.scopeOf(t, env.orgC, true)
	if _, src, ok := env.find(t, scopeBefore, "跨部门共享话术"); !ok || src != "cross" {
		t.Fatalf("移动前应为跨部门回退可见, got src=%s ok=%v", src, ok)
	}
	// ★ 组织调整：C 从 B 改挂到 D 下（真实场景=上级调整汇报线）
	if err := env.st.MoveOrg(2, env.orgC, env.orgD); err != nil {
		t.Fatalf("MoveOrg 失败: %v", err)
	}
	scope := env.scopeOf(t, env.orgC, false) // 关闭跨部门开关，纯链内判定
	en, src, ok := env.find(t, scope, "跨部门共享话术")
	if !ok || en != "Cross-dept shared script" || src != "chain" {
		t.Fatalf("移动后 D 包应为链内可见, got ok=%v src=%s en=%q", ok, src, en)
	}
}

// TestFuzzyScopedLayering 模糊分层：链内候选进采用域、兄弟候选仅进参考域。
func TestFuzzyScopedLayering(t *testing.T) {
	env := newScopeEnv(t)
	// 追加模糊近邻：C 包「C专属术语」与 D 包「跨部门共享话术」各造一条近似串
	e := env
	e.save(t, 2, e.cP.ID, "C专属术语补充说明版", "C fuzzy ver")
	e.save(t, 2, e.dP.ID, "跨部门共享话术补充说明版", "D fuzzy ver")

	scope := e.scopeOf(t, e.orgC, true)
	chainHits, crossHits, err := e.kdb.FuzzyHitsScoped("C专属术语", 5, 2, scope)
	if err != nil {
		t.Fatalf("FuzzyHitsScoped 失败: %v", err)
	}
	if len(chainHits) == 0 {
		t.Fatal("链内模糊候选缺失")
	}
	for _, r := range chainHits {
		if r.PackID == e.dP.ID || r.PackID == e.dPOpt.ID {
			t.Fatal("兄弟分支包不应出现在链内采用域")
		}
	}
	foundCross := false
	for _, r := range crossHits {
		if r.PackID == e.dP.ID {
			foundCross = true
		}
	}
	if !foundCross {
		t.Log("提示：本次长度差过滤下无跨部门模糊候选（可接受，非必现路径）")
	}
	// opt-out 包在任何域都不可见
	for _, list := range [][]*kb.Row{chainHits, crossHits} {
		for _, r := range list {
			if r.PackID == e.dPOpt.ID {
				t.Fatal("opt-out 包不得出现在任何模糊候选域")
			}
		}
	}
}

// TestVectorScopedSharedVisible 向量检索共享包可见性（审计 #10 回归）：
// 文化包（宿主租户1）在普通租户用户的向量结果中必须可召回。
func TestVectorScopedSharedVisible(t *testing.T) {
	env := newScopeEnv(t)
	scope := env.scopeOf(t, env.orgC, false)
	found := false
	for id := range scope.UniversalPackIDs {
		if id == env.locP.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("通用语言习惯包(locale)未进入 UniversalPackIDs（向量白名单将漏召回）")
	}
	if _, inChain := scope.ChainPacks[env.dP.ID]; inChain {
		t.Fatal("兄弟部门包不应进入链内集合")
	}
}

// TestFindEntriesBySourceScopedIsolation 整改 R2 回归：结构化 KB 条目按组织可见性隔离。
// 部门私有（opt-out, share_cross_dept=0）术语对兄弟部门不可见；共享部门包/企业包/行业包/文化包可见。
func TestFindEntriesBySourceScopedIsolation(t *testing.T) {
	env := newScopeEnv(t)

	// C 部门用户视角
	got, err := env.st.FindEntriesBySourceScoped(2, env.orgC, "zh", "D机密流程")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("兄弟部门 opt-out 私有术语不应跨见, got=%v", got)
	}

	// 批量检索收集命中源文本
	collect := func(zh string) map[string]bool {
		es, e := env.st.FindEntriesBySourceScoped(2, env.orgC, "zh", zh)
		if e != nil {
			t.Fatalf("查询 %s 失败: %v", zh, e)
		}
		m := map[string]bool{}
		for _, x := range es {
			m[x.SourceText] = true
		}
		return m
	}
	if m := collect("C专属术语"); !m["C专属术语"] {
		t.Fatal("本部门术语应可见")
	}
	if m := collect("刹车系统"); !m["刹车系统"] {
		t.Fatal("企业包术语应可见")
	}
	if m := collect("跨部门共享话术"); !m["跨部门共享话术"] {
		t.Fatal("兄弟部门 opt-in 共享术语应可见")
	}
	if m := collect("B层术语"); !m["B层术语"] {
		t.Fatal("兄弟部门默认共享术语应可见")
	}

	// 无组织（org=0）用户：任何部门私有包均不可见，只见企业/行业/文化
	zero := func(zh string) bool {
		es, _ := env.st.FindEntriesBySourceScoped(2, 0, "zh", zh)
		for _, x := range es {
			if x.SourceText == zh {
				return true
			}
		}
		return false
	}
	if zero("D机密流程") {
		t.Fatal("无组织用户不应见 opt-out 私有术语")
	}
	if !zero("刹车系统") {
		t.Fatal("无组织用户应见企业包术语")
	}
}

// TestCrossDeptIndependentType 跨部门包作为独立可见类型的回归：
// ① 开关开时进入 CrossDeptPacks；② 开关关时不纳入；③ 不混进链内/企业/行业/文化集合；
// ④ ScopeVisibility 判定为可见但 inChain=false（仅例句参考，绝不自动替换）。
func TestCrossDeptIndependentType(t *testing.T) {
	env := newScopeEnv(t)

	// ① 开关开：跨部门包进入 CrossDeptPacks
	scopeOn := env.scopeOf(t, env.orgC, true)
	if _, ok := scopeOn.CrossDeptPacks[env.crossP.ID]; !ok {
		t.Fatal("跨部门包(cross_dept)未进入 CrossDeptPacks（开关开）")
	}
	// ③ 不混进其他域
	if _, ok := scopeOn.ChainPacks[env.crossP.ID]; ok {
		t.Fatal("跨部门包不应进入链内集合")
	}
	if scopeOn.TenantPackIDs[env.crossP.ID] {
		t.Fatal("跨部门包不应进入企业集合")
	}
	if scopeOn.SharedPackIDs[env.crossP.ID] {
		t.Fatal("跨部门包不应进入行业共享集合")
	}
	if scopeOn.UniversalPackIDs[env.crossP.ID] {
		t.Fatal("跨部门包不应进入通用语言习惯集合")
	}
	// ④ 可见性判定：可见但非链内（仅参考）
	vis, inChain := kb.ScopeVisibility(2, env.crossP.ID, 2, scopeOn)
	if !vis || inChain {
		t.Fatalf("跨部门包应为可见且 inChain=false, got vis=%v inChain=%v", vis, inChain)
	}

	// ② 开关关：跨部门包不纳入任何跨部门集
	scopeOff := env.scopeOf(t, env.orgC, false)
	if _, ok := scopeOff.CrossDeptPacks[env.crossP.ID]; ok {
		t.Fatal("跨部门包不应在开关关时进入 CrossDeptPacks")
	}
	visOff, _ := kb.ScopeVisibility(2, env.crossP.ID, 2, scopeOff)
	if visOff {
		t.Fatal("开关关时跨部门包应对调用者不可见")
	}

	// ②b 部门隔离（使用权限）：D 部门成员不得见 C 部门的跨部门包
	scopeD := env.scopeOf(t, env.orgD, true)
	if _, ok := scopeD.CrossDeptPacks[env.crossP.ID]; ok {
		t.Fatal("跨部门包仅对归属部门成员可见，D 部门不应见 C 部门的跨部门包")
	}
	visD, _ := kb.ScopeVisibility(2, env.crossP.ID, 2, scopeD)
	if visD {
		t.Fatal("D 部门成员对 C 部门的跨部门包应不可见")
	}
	// 超管/租管（空链）见全部跨部门包
	scopeZero := env.scopeOf(t, 0, true)
	if _, ok := scopeZero.CrossDeptPacks[env.crossP.ID]; !ok {
		t.Fatal("无组织链用户（超管/租管）应见全部跨部门包")
	}

	// 全公司跨部门包：对其他部门可见（使用范围=租户内全部部门）
	allP, err := env.st.CreateKBPackage(2, 0, "crossall", "全公司跨部门包", PackCrossDept, PackRoleSource)
	if err != nil {
		t.Fatalf("建全公司跨部门包失败: %v", err)
	}
	if err := env.st.SetKBPackageCrossScope(allP.ID, 2, true, nil); err != nil {
		t.Fatalf("设置全公司失败: %v", err)
	}
	scopeDAll := env.scopeOf(t, env.orgD, true)
	if _, ok := scopeDAll.CrossDeptPacks[allP.ID]; !ok {
		t.Fatal("全公司跨部门包应对其他部门（D）可见")
	}

	// 跨部门包条目在精确检索中仅作参考（不打入采用域）
	env.save(t, 2, env.crossP.ID, "跨部门专有句", "Cross-dept only")
	en, src, hit := env.find(t, scopeOn, "跨部门专有句")
	if !hit {
		t.Fatal("跨部门包条目应对 C 用户可召回（参考域）")
	}
	if src != "cross" {
		t.Fatalf("跨部门包条目来源应为参考域(cross), got src=%s en=%q", src, en)
	}
}
