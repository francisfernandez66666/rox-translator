// ============ h1_terms_scope_test.go · 职责说明 ============
// T-B（2026-10-29 第十一轮）的接线锁：术语闸与提示词术语层必须吃「当前用户的 KB 可见域」
// （企业/组织链/行业/角色/语言文化），而不是「本租户」或「tenant 0 全量」。
//
// 五条腿：
//  1. 本行业术语进闸；2. 非本行业（medical）不进闸；3. 语言文化包进闸；
//  4. 空 job_role 不得命中角色包，带 frontend 才命中；
//  5. 提示词术语层的缓存键必须含 scope（先空角色、后 frontend ⇒ 第二次必须重算）。
//
// ============================================
package engine

import (
	"context"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/gate"
	"translator/internal/store"
	"translator/internal/tenant"
)

// termScopeFixture 建最小拓扑：租户1（注册行业 auto）+ 企业包 + 行业包(auto/medical)
// + 角色包(frontend) + 语言文化包，各放一条 layer=1 术语。
func termScopeFixture(t *testing.T) *store.Store {
	t.Helper()
	st := newTestStore(t)
	// store.New 不建 tenants 表（生产由 tenant.Store 负责）；本测只需 industry 一列。
	if _, err := st.DB().Exec(`CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY, code TEXT DEFAULT '', name TEXT DEFAULT '',
		status TEXT DEFAULT 'active', industry TEXT DEFAULT '')`); err != nil {
		t.Fatalf("建 tenants 表失败: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO tenants (id, code, name, status, industry) VALUES (1,'t1','测试租户','active','auto')`); err != nil {
		t.Fatalf("种子租户失败: %v", err)
	}
	mk := func(tid int64, code, name, ptype string) int64 {
		p, err := st.CreateKBPackage(tid, 0, code, name, ptype, store.PackRoleSource)
		if err != nil {
			t.Fatalf("建包 %s 失败: %v", code, err)
		}
		return p.ID
	}
	entP := mk(1, "ent1", "企业包", store.PackTenant)
	autoP := mk(store.SharedHostTenant, "auto", "汽车行业", store.PackIndustry)
	medP := mk(store.SharedHostTenant, "medical", "医疗行业", store.PackIndustry)
	feP := mk(store.SharedHostTenant, "frontend", "前端", store.PackPersona)
	locP := mk(store.SharedHostTenant, "loc", "文化", store.PackLocale)
	save := func(tid, pkg int64, zh, en string) {
		if _, err := st.SaveEntry(tid, pkg, 1, "zh", zh, "en", en, "term"); err != nil {
			t.Fatalf("写术语 %s 失败: %v", zh, err)
		}
	}
	save(1, entP, "租户术语", "TenantTerm")
	save(store.SharedHostTenant, autoP, "行业术语", "IndustryTerm")
	save(store.SharedHostTenant, medP, "医疗术语", "MedicalTerm")
	save(store.SharedHostTenant, feP, "前端术语", "FrontendTerm")
	save(store.SharedHostTenant, locP, "文化术语", "LocaleTerm")
	// 同 source 跨包（企业 + 行业，译法不同）：termReqs 必须只留企业那条（P5）。
	save(1, entP, "重叠词", "TenantTarget")
	save(store.SharedHostTenant, autoP, "重叠词", "IndustryTarget")
	return st
}

// TestTermReqsUseKBScope 术语闸按可见域取词（行业/角色/locale），跨行业与跨角色不串。
func TestTermReqsUseKBScope(t *testing.T) {
	st := termScopeFixture(t)
	e := &Engine{St: st, Cfg: config.Default()}
	ctx := tenant.WithTenant(context.Background(), 1)

	g := &gateCtx{e: e, tid: 1, termCache: map[string][]gate.TermRequirement{}}
	terms := g.termReqs(ctx, "行业术语 与 租户术语 与 文化术语 与 医疗术语", "en")
	got := map[string]string{}
	for _, tm := range terms {
		got[tm.Source] = tm.Target
	}
	if got["行业术语"] != "IndustryTerm" || got["租户术语"] != "TenantTerm" || got["文化术语"] != "LocaleTerm" {
		t.Fatalf("行业/租户/文化术语都要进闸，实得 %+v", got)
	}
	if _, ok := got["医疗术语"]; ok {
		t.Fatalf("非本行业术语不得进闸：%+v", got)
	}
	// 角色隔离：空 job_role 不得命中角色包术语。
	g2 := &gateCtx{e: e, tid: 1, termCache: map[string][]gate.TermRequirement{}}
	if terms2 := g2.termReqs(ctx, "前端术语", "en"); len(terms2) != 0 {
		t.Fatalf("空角色不应命中角色包术语，实得 %+v", terms2)
	}
	ctxRole := WithUserJobRole(ctx, "frontend")
	g3 := &gateCtx{e: e, tid: 1, termCache: map[string][]gate.TermRequirement{}}
	if terms3 := g3.termReqs(ctxRole, "前端术语", "en"); len(terms3) != 1 || terms3[0].Target != "FrontendTerm" {
		t.Fatalf("job_role=frontend 应命中 FrontendTerm，实得 %+v", terms3)
	}
	// P5：同 source 跨包（企业 + 行业）只留企业那条，不许出现"永远过不了闸"的两条要求。
	g5 := &gateCtx{e: e, tid: 1, termCache: map[string][]gate.TermRequirement{}}
	dup := g5.termReqs(ctx, "重叠词", "en")
	if len(dup) != 1 || dup[0].Target != "TenantTarget" {
		t.Fatalf("同 source 应只留企业包那条（TenantTarget），实得 %+v", dup)
	}
	// 平台上下文：tid=0 仍短路（没有租户可见域）。
	g4 := &gateCtx{e: e, tid: 0, termCache: map[string][]gate.TermRequirement{}}
	if terms4 := g4.termReqs(ctx, "行业术语", "en"); len(terms4) != 0 {
		t.Fatalf("tid=0 必须短路，实得 %+v", terms4)
	}
}

// TestBatchTermLayerUsesKBScope 提示词术语层按可见域取词，且缓存键含 scope（换角色必须重算）。
func TestBatchTermLayerUsesKBScope(t *testing.T) {
	st := termScopeFixture(t)
	e := &Engine{St: st, Cfg: config.Default()}
	ctx := tenant.WithTenant(context.Background(), 1)

	layer := e.batchTermLayer(ctx, 1, "en")
	if !strings.Contains(layer, "IndustryTerm") {
		t.Fatalf("本行业术语必须进提示词层：%q", layer)
	}
	if strings.Contains(layer, "MedicalTerm") {
		t.Fatalf("非本行业术语不得进提示词层：%q", layer)
	}
	if strings.Contains(layer, "FrontendTerm") {
		t.Fatalf("空 job_role 不该有角色术语：%q", layer)
	}
	// 同一 Engine 再带角色调用：缓存键若不含 scope，这里会直接命中上一份（不含角色术语）。
	ctxRole := WithUserJobRole(ctx, "frontend")
	layer2 := e.batchTermLayer(ctxRole, 1, "en")
	if !strings.Contains(layer2, "FrontendTerm") {
		t.Fatalf("job_role=frontend 应带角色术语（缓存键必须含 scope 指纹）：%q", layer2)
	}
}

// TestBatchTermLayerKeepsGateRoleLocale 语言文化包是 role='gate'，它的 layer=1 条目必须仍进提示词词表。
//
// 与术语硬闸是两条不同的射程：硬闸只认 role='source'（FindTermsBySubstring 的既有口径），
// 而 batchTermLayer 历来不筛 role —— T-B 改造时差点顺手把这一档关掉（本锁钉住）。
func TestBatchTermLayerKeepsGateRoleLocale(t *testing.T) {
	st := newTestStore(t)
	pkg, err := st.CreateKBPackage(store.SharedHostTenant, 0, "loc", "文化包", store.PackLocale, store.PackRoleGate)
	if err != nil {
		t.Fatalf("建文化包失败: %v", err)
	}
	if _, err := st.SaveEntry(store.SharedHostTenant, pkg.ID, 1, "zh", "文化术语", "en", "LocaleTerm", "term"); err != nil {
		t.Fatalf("写文化术语失败: %v", err)
	}
	e := &Engine{St: st, Cfg: config.Default()}
	layer := e.batchTermLayer(tenant.WithTenant(context.Background(), 1), 1, "en")
	if !strings.Contains(layer, "LocaleTerm") {
		t.Fatalf("role='gate' 的语言文化包条目必须进提示词词表：%q", layer)
	}
}
