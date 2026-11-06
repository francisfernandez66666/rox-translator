// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》批次 2 物料货架门禁（§9.1 A1–A5）：
// 事实改动触发作废、未改动不误伤、下载口拒 expired、空 fact_refs 不误判、missing 不落行。
// ★ A1/A2 必须成对：只测触发＝空锁（把作废腿摘掉也可能绿）；只测不误伤＝另一半空转。
// 复用包内 newProductTestStore（自钉 SQLite 方言，防 PG 模式泄漏）。
// ========================================
package store

import (
	"errors"
	"strings"
	"testing"

	"translator/internal/db"
)

// putFactAndAsset 夹具：建产品 → 写一条人工事实并确认 → 写一条引用它的已审物料。
// 参数：t=测试句柄，s=Store，body=物料正文，refs=引用的事实 ID 集合；返回产品、事实、物料。
func putFactAndAsset(t *testing.T, s *Store, body string, refs []int64) (Product, ProductFact, ProductAsset) {
	t.Helper()
	p, err := s.CreateProduct(1, ProductInput{Name: "物料测试产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	f, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "price", Key: "unit", Value: "¥100", Source: FactSourceManual,
	})
	if err != nil {
		t.Fatalf("写事实失败: %v", err)
	}
	if err := s.ConfirmFact(1, f.ID, 7); err != nil {
		t.Fatalf("确认事实失败: %v", err)
	}
	useRefs := refs
	if useRefs == nil {
		useRefs = []int64{f.ID}
	}
	a, err := s.PutAsset(1, ProductAssetInput{
		ProductID: p.ID, AssetType: AssetTypeQuoteSheet, Lang: "en", Body: body,
		FactRefs: useRefs, ReviewStatus: AssetReviewApproved,
	})
	if err != nil {
		t.Fatalf("写物料失败: %v", err)
	}
	return p, f, a
}

// A1：事实值变更 ⇒ 引用它的物料翻 expired，且 UpsertFact 是唯一触发写点。
func TestProductAssetFactChangeExpiresAsset(t *testing.T) {
	s := newProductTestStore(t)
	p, f, a := putFactAndAsset(t, s, "quote-100", nil)
	if a.Validity != AssetValidityValid {
		t.Fatalf("新物料应为 valid, got %s", a.Validity)
	}
	if _, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "price", Key: "unit", Value: "¥120", Source: FactSourceManual,
	}); err != nil {
		t.Fatalf("改事实失败: %v", err)
	}
	got, err := s.assetByIDScoped(1, a.ID)
	if err != nil {
		t.Fatalf("回读物料失败: %v", err)
	}
	if got.Validity != AssetValidityExpired {
		t.Fatalf("事实变更后引用它的物料必须翻 expired, got %s", got.Validity)
	}
	_ = f
}

// A2：同值重写（未改动）与「别的事实变更」都不许误伤引用物料。
func TestProductAssetNoChangeNeverExpires(t *testing.T) {
	s := newProductTestStore(t)
	p, f, a := putFactAndAsset(t, s, "quote-keep", nil)
	// ① 同值重写
	if _, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "price", Key: "unit", Value: "¥100", Source: FactSourceManual,
	}); err != nil {
		t.Fatalf("同值重写失败: %v", err)
	}
	// ② 新增并改动另一条事实（引用集合不含它）
	other, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "moq", Key: "min", Value: "10", Source: FactSourceManual,
	})
	if err != nil {
		t.Fatalf("写另一事实失败: %v", err)
	}
	if _, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "moq", Key: "min", Value: "20", Source: FactSourceManual,
	}); err != nil {
		t.Fatalf("改另一事实失败: %v", err)
	}
	got, err := s.assetByIDScoped(1, a.ID)
	if err != nil {
		t.Fatalf("回读物料失败: %v", err)
	}
	if got.Validity != AssetValidityValid {
		t.Fatalf("未改动不得误伤引用物料（A2 是 A1 的反向对照）, got %s", got.Validity)
	}
	_ = f
	_ = other
}

// A3：下载口拒 expired，且返回「可行动文案」而不是静默出空。
func TestProductAssetDownloadBlocksExpired(t *testing.T) {
	s := newProductTestStore(t)
	p, _, a := putFactAndAsset(t, s, "quote-block", nil)
	// 正向对照：未过期时下载口放通（否则下面的负向锁恒真＝空转）
	if got, err := s.AssetForDownload(1, a.ID); err != nil || got.ID != a.ID {
		t.Fatalf("valid 物料下载口必须放通, got id=%d err=%v", got.ID, err)
	}
	if _, err := s.UpsertFact(1, ProductFactInput{
		ProductID: p.ID, Kind: "price", Key: "unit", Value: "¥999", Source: FactSourceManual,
	}); err != nil {
		t.Fatalf("改事实失败: %v", err)
	}
	_, err := s.AssetForDownload(1, a.ID)
	if !errors.Is(err, ErrAssetExpired) {
		t.Fatalf("expired 物料下载口必须拒, got %v", err)
	}
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		t.Fatal("下载口拒绝必须带可行动文案，不许静默出空")
	}
}

// A4：fact_refs 为空数组（或非法 JSON）的物料**永不**被判 expired。
func TestProductAssetEmptyFactRefsNeverExpired(t *testing.T) {
	s := newProductTestStore(t)
	p, f, a := putFactAndAsset(t, s, "quote-plain", []int64{})
	if len(a.FactRefs) != 0 {
		t.Fatalf("空 fact_refs 应解析为空集, got %v", a.FactRefs)
	}
	n, err := s.ExpireAssetsByFactChange(1, f.ID, p.ID)
	if err != nil {
		t.Fatalf("作废腿报错: %v", err)
	}
	if n != 0 {
		t.Fatalf("空 fact_refs 的物料不得被作废, got n=%d", n)
	}
	got, err := s.assetByIDScoped(1, a.ID)
	if err != nil || got.Validity != AssetValidityValid {
		t.Fatalf("空 fact_refs 物料必须仍 valid, got %s err=%v", got.Validity, err)
	}
	// 正向对照：带引用的物料确实会被同一条腿作废（证明上面不是「腿没跑」）
	linked, err := s.PutAsset(1, ProductAssetInput{
		ProductID: p.ID, AssetType: AssetTypeOnepager, Lang: "en", Body: "linked",
		FactRefs: []int64{f.ID}, ReviewStatus: AssetReviewApproved,
	})
	if err != nil {
		t.Fatalf("写带引用物料失败: %v", err)
	}
	n, err = s.ExpireAssetsByFactChange(1, f.ID, p.ID)
	if err != nil || n != 1 {
		t.Fatalf("带引用物料必须被作废 1 条, got n=%d err=%v", n, err)
	}
	got, err = s.assetByIDScoped(1, linked.ID)
	if err != nil || got.Validity != AssetValidityExpired {
		t.Fatalf("带引用物料必须翻 expired, got %s err=%v", got.Validity, err)
	}
}

// A5：validity='missing' 永不落行（缺由 ReadinessFor 现算给出）。
func TestProductAssetMissingNeverStored(t *testing.T) {
	s := newProductTestStore(t)
	p, _, _ := putFactAndAsset(t, s, "quote-missing-check", nil)
	// 写侧硬拒：PutAsset 不接受 missing，来源三值也不许造第四值。
	if _, err := s.PutAsset(1, ProductAssetInput{
		ProductID: p.ID, AssetType: AssetTypeTagline, Lang: "en", Body: "x",
	}); err != nil {
		t.Fatalf("普通物料写入失败: %v", err)
	}
	var n int
	if err := db.QueryRow(s.DB(), db.CurrentDialect(),
		"SELECT COUNT(*) FROM product_assets WHERE validity=?", AssetValidityMissing).Scan(&n); err != nil {
		t.Fatalf("查 missing 行数失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("missing 永不落行, got %d 行", n)
	}
	cells, err := s.ReadinessFor(1, p.ID)
	if err != nil {
		t.Fatalf("现算齐备度失败: %v", err)
	}
	missing := 0
	for _, c := range cells {
		if c.Worst == ReadinessMissing {
			missing++
		}
	}
	if missing == 0 {
		t.Fatal("正向对照失效：矩阵里必须有由现算推得的 missing 格")
	}
}
