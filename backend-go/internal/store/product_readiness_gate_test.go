// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》批次 2 齐备度现算门禁（§9.1 R1–R5）：
// 六格 × 12 语种全列、缺格读 missing 而非 0、SQL 与现算等值、R5 单产品前缀等值条件。
// ★ R5 是源码级判据：防未来有人把 SQL 改成全租户聚合（慢 SQL 事故要当场量住，不等它来教）。
// ========================================
package store

import (
	"os"
	"strings"
	"testing"
)

// R1/R2：六格齐备度与 SQL 现算等值；无行的格子读 missing（不是 0）。
func TestProductReadinessMatchesStoredRows(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(1, ProductInput{Name: "齐备度产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	cells, err := s.ReadinessFor(1, p.ID)
	if err != nil {
		t.Fatalf("现算齐备度失败: %v", err)
	}
	want := len(ProductAssetTypes) * len(ProductLangs)
	if len(cells) != want {
		t.Fatalf("齐备度必须恒为六格 × 12 语种 = %d 格, got %d", want, len(cells))
	}
	for _, c := range cells {
		if c.Worst != ReadinessMissing || c.Count != 0 {
			t.Fatalf("空产品每格必须读 missing/0, got %+v", c)
		}
	}
	// 落一行 valid 物料 ⇒ 只有对应格变 valid 且 count=1
	if _, err := s.PutAsset(1, ProductAssetInput{
		ProductID: p.ID, AssetType: AssetTypeQuoteSheet, Lang: "en", Body: "quote",
		ReviewStatus: AssetReviewApproved,
	}); err != nil {
		t.Fatalf("写物料失败: %v", err)
	}
	cells, err = s.ReadinessFor(1, p.ID)
	if err != nil {
		t.Fatalf("现算齐备度失败: %v", err)
	}
	hit := 0
	for _, c := range cells {
		if c.AssetType == AssetTypeQuoteSheet && c.Lang == "en" {
			if c.Worst != ReadinessValid || c.Count != 1 {
				t.Fatalf("有 valid 行的格子必须读 valid/1, got %+v", c)
			}
			hit++
		} else if c.Worst != ReadinessMissing {
			t.Fatalf("其余格子仍应 missing, got %+v", c)
		}
	}
	if hit != 1 {
		t.Fatalf("目标格必须恰好出现一次, got %d", hit)
	}
}

// R3：语种维度按 12 全列（不许只数已落行的语种）。
func TestProductReadinessListsAllLangs(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(1, ProductInput{Name: "语种全列产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	// 只落一个语种的物料，仍须列出全部 12 语种
	if _, err := s.PutAsset(1, ProductAssetInput{
		ProductID: p.ID, AssetType: AssetTypeTagline, Lang: "en", Body: "tagline",
	}); err != nil {
		t.Fatalf("写物料失败: %v", err)
	}
	cells, err := s.ReadinessFor(1, p.ID)
	if err != nil {
		t.Fatalf("现算齐备度失败: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range cells {
		if c.AssetType == AssetTypeTagline {
			seen[c.Lang] = true
		}
	}
	if len(seen) != len(ProductLangs) {
		t.Fatalf("语种维度必须按全列 12 项，不许只数已落行的语种, got %d: %v", len(seen), seen)
	}
	for _, lang := range ProductLangs {
		if !seen[lang] {
			t.Fatalf("缺少语种 %s", lang)
		}
	}
	if len(ProductLangs) != 12 {
		t.Fatalf("产品口径语种数必须是 12（词典文件 10 份是另一把尺子）, got %d", len(ProductLangs))
	}
}

// R5（源码级）：ReadinessFor 的 SQL 必带 product_id = ? 前缀等值条件，
// 且入参只允许单产品（不许出现切片入参的第二个 ReadinessFor 形态）。
func TestProductReadinessSQLHasProductPrefix(t *testing.T) {
	raw, err := os.ReadFile("product_readiness.go")
	if err != nil {
		t.Fatalf("读不到 product_readiness.go: %v", err)
	}
	src := string(raw)
	if !strings.Contains(src, "WHERE tenant_id=? AND product_id = ?") {
		t.Fatal("ReadinessFor 的 SQL 必须带 product_id = ? 前缀等值条件（R5），禁止一条 SQL 扫全库")
	}
	if !strings.Contains(src, "func (s *Store) ReadinessFor(tid int64, productID string)") {
		t.Fatal("ReadinessFor 入参只允许单产品，不许改成批量/全租户聚合")
	}
	// 反向对照：SQL 里不许出现「按全租户分组」的形状
	if strings.Contains(src, "GROUP BY tenant_id") {
		t.Fatal("ReadinessFor 不许按全租户分组（R5 反向对照）")
	}
}
