// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》第一批产品档案门禁：状态三值、租户隔离、短码稳定与不可空、
// 跨租户拼接 not-found、slug 唯一、分页脏参档位、空集出 []。
// 复用包内 newTestStore（内存 SQLite），并自钉方言防止 PG 模式泄漏给同包后续用例。
// ========================================
package store

import (
	"errors"
	"strings"
	"testing"

	"translator/internal/config"
)

// newProductTestStore 建一个自钉 SQLite 方言的测试 Store。
func newProductTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return newTestStore(t)
}

// P1/P2：状态字面三值可流转，第四值写侧硬拒（不是靠数据库约束兜）。
func TestProductStatusOnlyThreeValues(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(1, ProductInput{Name: "测试产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	for _, st := range []string{ProductStatusDraft, ProductStatusLive, ProductStatusArchived} {
		if err := s.SetProductStatus(1, p.ID, st); err != nil {
			t.Fatalf("合法状态 %s 被拒: %v", st, err)
		}
	}
	if err := s.SetProductStatus(1, p.ID, "deleted"); err == nil {
		t.Fatal("第四个状态值必须在写侧被拒")
	}
}

// P3/H4/H5：跨租户不可读写、跨租户拼接取不到行、合法两段码必须取得到（正向对照）。
func TestProductScopeIsolationAndCodePairing(t *testing.T) {
	s := newProductTestStore(t)
	a, err := s.CreateProduct(1, ProductInput{Name: "A 的产品"})
	if err != nil {
		t.Fatalf("A 建档失败: %v", err)
	}
	b, err := s.CreateProduct(2, ProductInput{Name: "B 的产品"})
	if err != nil {
		t.Fatalf("B 建档失败: %v", err)
	}
	// 负向：B 租户改 A 的产品必须查无此行，且是同一个错误（不返 403、不泄露存在性）
	if err := s.UpdateProductName(2, a.ID, "越权改名"); !errors.Is(err, errProductNotFound) {
		t.Fatalf("跨租户写应得 errProductNotFound, got %v", err)
	}
	if err := s.SetProductStatus(2, a.ID, ProductStatusLive); !errors.Is(err, errProductNotFound) {
		t.Fatalf("跨租户状态流转应得 errProductNotFound, got %v", err)
	}
	// 负向：A 的租户段 ＋ B 的页面段
	if _, ok, err := s.GetProductByCodes(a.TenantCode, b.PageCode); err != nil || ok {
		t.Fatalf("跨租户拼接必须 not-found, got ok=%v err=%v", ok, err)
	}
	// 负向：空段
	if _, ok, err := s.GetProductByCodes("", a.PageCode); err != nil || ok {
		t.Fatalf("空租户段必须 not-found, got ok=%v err=%v", ok, err)
	}
	// 正向对照：合法两段码确实取得到（防上面几条负向锁恒真空转）
	got, ok, err := s.GetProductByCodes(a.TenantCode, a.PageCode)
	if err != nil || !ok || got.ID != a.ID {
		t.Fatalf("合法两段码解析失败, got ok=%v err=%v id=%s", ok, err, got.ID)
	}
	// 列表按租户隔离
	list, total, err := s.ListProducts(1, 1, 20)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("租户 1 列表读数错误: total=%d len=%d err=%v", total, len(list), err)
	}
	_ = b
}

// H1/H2/H6：短码创建即定、改名不改链、三段绝不落空串、同租户租户段复用。
func TestProductShortCodesStableAndNeverEmpty(t *testing.T) {
	s := newProductTestStore(t)
	a, err := s.CreateProduct(1, ProductInput{Name: "第一个产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if a.Slug == "" || a.TenantCode == "" || a.PageCode == "" {
		t.Fatalf("三段不得为空: slug=%q tenant=%q page=%q", a.Slug, a.TenantCode, a.PageCode)
	}
	if a.TenantCode == a.PageCode {
		t.Fatal("租户段与页面段不得同值")
	}
	b, err := s.CreateProduct(1, ProductInput{Name: "第二个产品"})
	if err != nil {
		t.Fatalf("第二个产品建档失败（空串唯一键会在这里撞）: %v", err)
	}
	if b.TenantCode != a.TenantCode {
		t.Fatal("同租户所有产品必须复用同一个租户段")
	}
	if b.PageCode == a.PageCode {
		t.Fatal("页面段必须逐产品唯一")
	}
	before := a
	if err := s.UpdateProductName(1, a.ID, "改过名字"); err != nil {
		t.Fatalf("改名失败: %v", err)
	}
	after, ok, err := s.GetProductByCodes(a.TenantCode, a.PageCode)
	if err != nil || !ok {
		t.Fatalf("改名后两段短码应仍可解析: ok=%v err=%v", ok, err)
	}
	if after.TenantCode != before.TenantCode || after.PageCode != before.PageCode || after.Slug != before.Slug {
		t.Fatal("改名不得改写两段短码与 slug")
	}
	if after.Name != "改过名字" {
		t.Fatalf("改名未生效: %q", after.Name)
	}
}

// P5/H3：同租户 slug 重复被拒，不同租户同名 slug 各自可建。
func TestProductSlugUniquePerTenant(t *testing.T) {
	s := newProductTestStore(t)
	if _, err := s.CreateProduct(1, ProductInput{Name: "产品一", Slug: "same-slug"}); err != nil {
		t.Fatalf("首次建档失败: %v", err)
	}
	if _, err := s.CreateProduct(1, ProductInput{Name: "产品二", Slug: "same-slug"}); err == nil {
		t.Fatal("同租户重复 slug 必须被唯一键拒绝")
	}
	if _, err := s.CreateProduct(2, ProductInput{Name: "别家产品", Slug: "same-slug"}); err != nil {
		t.Fatalf("不同租户同名 slug 应可建: %v", err)
	}
}

// P4：合规类 kind='cert' 永不允许 source!='manual' 落成 confirmed（写侧硬拒）。
func TestProductCertFactNeedsManualSource(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(1, ProductInput{Name: "认证事实产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	// 负向：ocr 来源的认证声明不许确认
	ocr, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: FactKindCert, Key: "ce", Value: "CE", Source: FactSourceOCR,
	})
	if err != nil {
		t.Fatalf("写认证事实失败: %v", err)
	}
	if err := s.ConfirmFact(1, ocr.ID, 7); err == nil {
		t.Fatal("认证声明只允许人工录入的事实确认（source=ocr 必须被拒）")
	}
	got, err := s.factByIDScoped(1, ocr.ID)
	if err != nil || got.Status == FactStatusConfirmed {
		t.Fatalf("被拒后状态不得是 confirmed, got %q err=%v", got.Status, err)
	}
	// 正向对照：手工录入的认证声明可确认（防上面的负向恒真＝空转）
	manual, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: FactKindCert, Key: "fda", Value: "FDA", Source: FactSourceManual,
	})
	if err != nil {
		t.Fatalf("写人工认证事实失败: %v", err)
	}
	if err := s.ConfirmFact(1, manual.ID, 7); err != nil {
		t.Fatalf("人工认证声明必须可确认: %v", err)
	}
	got, err = s.factByIDScoped(1, manual.ID)
	if err != nil || got.Status != FactStatusConfirmed {
		t.Fatalf("人工认证声明应 confirmed, got %q err=%v", got.Status, err)
	}
}

// V1：新建行不写 visibility 时库里落 gated（事实项与物料各验一次，默认朝保守）。
func TestProductVisibilityDefaultsGated(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(1, ProductInput{Name: "默认受控产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	f, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "price", Key: "unit", Value: "¥100",
	})
	if err != nil {
		t.Fatalf("写事实失败: %v", err)
	}
	if f.Visibility != VisibilityGated {
		t.Fatalf("事实项缺省必须 gated, got %q", f.Visibility)
	}
	a, err := s.PutAsset(1, ProductAssetInput{
		ProductID: p.ID, AssetType: AssetTypeTagline, Lang: "en", Body: "tagline",
	})
	if err != nil {
		t.Fatalf("写物料失败: %v", err)
	}
	if a.Visibility != VisibilityGated {
		t.Fatalf("物料缺省必须 gated, got %q", a.Visibility)
	}
	// V3 负向：受控行的值字面量不出现在公开语料（必须被 SQL 条件挡掉，不是应用层 if）
	corpus, err := s.ListPublicCorpus(1, p.ID)
	if err != nil {
		t.Fatalf("读公开语料失败: %v", err)
	}
	for _, it := range corpus {
		if strings.Contains(it.Text, "¥100") || strings.Contains(it.Text, "tagline") {
			t.Fatalf("受控值不得进公开语料, got %+v", it)
		}
	}
	// 正向对照：显式放开的行确实进得去（防上面负向恒真）
	if _, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "usecase", Key: "scene", Value: "公开场景",
		Visibility: VisibilityPublic, Source: FactSourceManual,
	}); err != nil {
		t.Fatalf("写公开事实失败: %v", err)
	}
	// 只有 confirmed 可被引用的口径仍成立：先确认再读
	facts, err := s.ListFacts(1, p.ID)
	if err != nil {
		t.Fatalf("列事实失败: %v", err)
	}
	for _, one := range facts {
		if one.Kind == "usecase" {
			if err := s.ConfirmFact(1, one.ID, 7); err != nil {
				t.Fatalf("确认公开事实失败: %v", err)
			}
		}
	}
	corpus, err = s.ListPublicCorpus(1, p.ID)
	if err != nil {
		t.Fatalf("读公开语料失败: %v", err)
	}
	found := false
	for _, it := range corpus {
		if it.Text == "公开场景" {
			found = true
		}
	}
	if !found {
		t.Fatal("显式放开的 confirmed 事实必须进公开语料（V3 正向对照）")
	}
}

// P6：分页脏参折回档位、空集返回非 nil 空切片（出栈 [] 而非 null）。
func TestProductPageClampAndEmptySlice(t *testing.T) {
	s := newProductTestStore(t)
	list, total, err := s.ListProducts(999, 0, 0)
	if err != nil {
		t.Fatalf("空租户列表报错: %v", err)
	}
	if total != 0 || list == nil || len(list) != 0 {
		t.Fatalf("空集必须是非 nil 空切片: total=%d nil=%v len=%d", total, list == nil, len(list))
	}
	if _, err := s.CreateProduct(3, ProductInput{Name: "单产品"}); err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	for _, size := range []int{0, -1, ProductPageMax + 1} {
		got, total, err := s.ListProducts(3, 1, size)
		if err != nil || total != 1 || len(got) != 1 {
			t.Fatalf("脏 pageSize=%d 未折回档位: total=%d len=%d err=%v", size, total, len(got), err)
		}
	}
}

// V1b：★ **更新**时省略 visibility／source ＝"没表态"，不是"改回缺省档"。
//
// 旧形态把省略一律写成 gated／manual ⇒ 卖家只是改一个数字，那一行就静默从对外页消失，
// 而且"模型采集来的行"被升级成"人工行"（confirm 资格由此白拿）。两条各一枚判据＋
// 一条正向对照（显式说要改档，才真的改）。
func TestFactUpdateOmittedFieldsKeepExistingState(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(1, ProductInput{Name: "改值不改档产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if _, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "spec", Key: "lead", Value: "7 天",
		Visibility: VisibilityPublic, Source: FactSourceExternalRef,
	}); err != nil {
		t.Fatalf("写公开事实失败: %v", err)
	}
	rows, err := s.ListFacts(1, p.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("回读失败: len=%d err=%v", len(rows), err)
	}
	// 只改值：档位与来源都必须沿用
	if _, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "spec", Key: "lead", Value: "10 天",
	}); err != nil {
		t.Fatalf("改值失败: %v", err)
	}
	rows, _ = s.ListFacts(1, p.ID)
	if rows[0].Visibility != VisibilityPublic {
		t.Fatalf("V1b：省略 visibility 不许把公开行洗成受控, got %q", rows[0].Visibility)
	}
	if rows[0].Source != FactSourceExternalRef {
		t.Fatalf("V1b：省略 source 不许把采集行升级成人工行, got %q", rows[0].Source)
	}
	// 值改动本身仍按 §2.2 打回未核实（这条不能因为"沿用现值"而被顺手放宽）
	if rows[0].Status != FactStatusUnverified {
		t.Fatalf("值改动必须打回 unverified, got %q", rows[0].Status)
	}
	// 正向对照：显式说要收回受控档，就必须真改（否则上面两条负向锁是恒真）
	if _, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "spec", Key: "lead", Value: "10 天",
		Visibility: VisibilityGated,
	}); err != nil {
		t.Fatalf("显式改档失败: %v", err)
	}
	rows, _ = s.ListFacts(1, p.ID)
	if rows[0].Visibility != VisibilityGated {
		t.Fatalf("V1b 正向对照失效：显式 gated 必须落地, got %q", rows[0].Visibility)
	}
}

// TestProductTargetLangsThreeStates O 组：目标语种的三态必须各不同形（AGENTS §29「省略＝不表态」的产品面第二例）。
//
//	nill   ＝ 没表态  ⇒ 库里那份原样留着（**这是本条的全部意义**：改个名字不许把语种清空）
//	&[]    ＝ 显式清空 ⇒ 真的清空
//	&[ja]  ＝ 整份替换 ⇒ 覆盖，并按提交顺序归一（去空白／去重／小写）
//
// 反证：把 update 里那句"只在非 nil 才动"改成"总是写" ⇒ 第一档必须红。
func TestProductTargetLangsThreeStates(t *testing.T) {
	st := newProductTestStore(t)

	p, err := st.CreateProduct(1, ProductInput{Name: "整机", UserID: 1, TargetLangs: &[]string{"JA", " en ", "en", ""}})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if p.TargetLangs != "ja,en" {
		t.Fatalf("T1：建档要归一（小写／去空白／去重／保序），实得 %q", p.TargetLangs)
	}
	if got := p.TargetLangList(); len(got) != 2 || got[0] != "ja" || got[1] != "en" {
		t.Fatalf("T1：切片出口顺序错，实得 %v", got)
	}

	// T2：只改一句话定位，不提语种 ⇒ 语种必须原样留着
	if err := st.UpdateProductProfile(1, p.ID, ProductInput{Tagline: "新的定位"}); err != nil {
		t.Fatalf("T2：更新失败: %v", err)
	}
	row, ok, err := st.GetProductScoped(1, p.ID)
	if err != nil || !ok {
		t.Fatalf("T2：读回失败 ok=%v err=%v", ok, err)
	}
	if row.TargetLangs != "ja,en" {
		t.Fatalf("T2：省略字段必须＝没表态（语种被清空＝替客户决定了他不说哪些话），实得 %q", row.TargetLangs)
	}

	// T3：显式空切片＝真的要清空
	if err := st.UpdateProductProfile(1, p.ID, ProductInput{Tagline: "还是这句", TargetLangs: &[]string{}}); err != nil {
		t.Fatalf("T3：清空调用失败: %v", err)
	}
	row, _, _ = st.GetProductScoped(1, p.ID)
	if row.TargetLangs != "" {
		t.Fatalf("T3：显式清空必须落空串，实得 %q", row.TargetLangs)
	}
	if got := row.TargetLangList(); got == nil || len(got) != 0 {
		t.Fatalf("T3：空值出栈要归一成 []（不回 nil，消费方各判一次 null 是老坑），实得 %#v", got)
	}

	// T4：整份替换按新顺序
	if err := st.UpdateProductProfile(1, p.ID, ProductInput{TargetLangs: &[]string{"de", "ja"}}); err != nil {
		t.Fatalf("T4：替换失败: %v", err)
	}
	row, _, _ = st.GetProductScoped(1, p.ID)
	if row.TargetLangs != "de,ja" {
		t.Fatalf("T4：整份替换要按新提交顺序，实得 %q", row.TargetLangs)
	}
}
