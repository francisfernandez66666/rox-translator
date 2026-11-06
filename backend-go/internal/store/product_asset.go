// ============ product_asset.go · 职责说明 ============
// store 包「物料货架」（product_assets 表）窄域文件，一期《确定的多语言能力》批次 2。
//
// ★ 效期反查为什么在 Go 侧做：fact_refs 在 PG/SQLite 都按 TEXT 存 JSON 数组，
//
//	用 jsonb 算子会让 SQLite 侧直接挂（AGENTS §一·4 双方言硬约定）；因此
//	ExpireAssetsByFactChange 先按「租户 + 产品」前缀取 valid 行，再在应用侧解析数组。
//	★ 绝不能改成 `fact_refs LIKE '%id%'`——那会把 id=1 误命中到 id=11/21（A4 的坏法）。
//
// ★ validity='missing' 永不落行：缺由 ReadinessFor 现算给出，落行会让「缺」与「有但空」同形。
// ★ 下载口是 A3 的最后一道闸：expired 行必须被拒且返回可行动文案，不许静默出空。
//
// 所有 SQL 一律走 db.Exec/Query/QueryRow/InsertID ＋ db.CurrentDialect()。
// ================================================
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
)

// 物料六格（＝齐备度的六个格子；新增值须同步 ProductAssetTypes）。
const (
	AssetTypeTagline     = "tagline"
	AssetTypeOnepager    = "onepager"
	AssetTypeQuoteSheet  = "quote_sheet"
	AssetTypeImageCard   = "image_card"
	AssetTypeVoiceScript = "voice_script"
	AssetTypeQAPack      = "qa_pack"
)

// ProductAssetTypes 物料类型对外契约六值（前端六格、齐备度现算共用这一份清单）。
var ProductAssetTypes = []string{
	AssetTypeTagline, AssetTypeOnepager, AssetTypeQuoteSheet,
	AssetTypeImageCard, AssetTypeVoiceScript, AssetTypeQAPack,
}

// 审核状态三值。
const (
	AssetReviewUnreviewed = "unreviewed"
	AssetReviewApproved   = "approved"
	AssetReviewRejected   = "rejected"
)

// 效期三值。★ AssetValidityMissing 只作为读数枚举，**永不落行**。
const (
	AssetValidityValid   = "valid"
	AssetValidityExpired = "expired"
	AssetValidityMissing = "missing"
)

// ErrAssetExpired 下载口拒发过期物料；消息即「可行动文案」，不静默、不返空串。
var ErrAssetExpired = errors.New("该物料引用的产品事实已变更，请重新生成并审核后再下载")

// ProductAsset 物料行。FactRefs 出栈为数组（库内为 JSON TEXT）。
type ProductAsset struct {
	ID           int64   `json:"id"`
	TenantID     int64   `json:"tenant_id"`
	ProductID    string  `json:"product_id"`
	AssetType    string  `json:"asset_type"`
	Lang         string  `json:"lang"`
	Body         string  `json:"body"`
	ArtifactRef  string  `json:"artifact_ref"`
	ReviewStatus string  `json:"review_status"`
	Validity     string  `json:"validity"`
	Visibility   string  `json:"visibility"`
	FactRefs     []int64 `json:"fact_refs"`
	SourceKind   string  `json:"source_kind"`
	Version      int     `json:"version"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// ProductAssetInput 物料写入入参。FactRefs 为空时落 "[]"（不是空串、不是 NULL）。
type ProductAssetInput struct {
	ProductID    string
	AssetType    string
	Lang         string
	Body         string
	ArtifactRef  string
	ReviewStatus string
	Visibility   string
	FactRefs     []int64
	SourceKind   string
}

// assetCols 物料列全集；读写两侧共用这一份，禁止在 SELECT 里各写一遍（两处清单迟早漂）。
const assetCols = `id, tenant_id, product_id, asset_type, lang, body, artifact_ref,
review_status, validity, visibility, fact_refs, source_kind, version, created_at, updated_at`

// scanAsset 按 assetCols 列序逐位扫描；fact_refs 先按字符串取回再交 parseFactRefs，让两种方言的 TEXT 存法走同一条路。
// 参数：row=行扫描器；返回物料与错误。
func scanAsset(row productScanner) (ProductAsset, error) {
	var a ProductAsset
	var refs string
	err := row.Scan(&a.ID, &a.TenantID, &a.ProductID, &a.AssetType, &a.Lang, &a.Body,
		&a.ArtifactRef, &a.ReviewStatus, &a.Validity, &a.Visibility, &refs,
		&a.SourceKind, &a.Version, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	a.FactRefs = parseFactRefs(refs)
	return a, nil
}

// parseFactRefs 解析库内 JSON 数组；空/非法一律当空集（不让脏行把别的物料误判 expired）。
func parseFactRefs(s string) []int64 {
	out := []int64{}
	if strings.TrimSpace(s) == "" {
		return out
	}
	var ids []int64
	if err := json.Unmarshal([]byte(s), &ids); err != nil || ids == nil {
		return out
	}
	return ids
}

// encodeFactRefs 把引用集列成 JSON 数组；空集写 "[]" 而不是空串——空串会让「没有引用」与「脏数据」同形，作废反查就分不清该不该拦。
// 参数：ids=事实项 ID 集；返回入库字符串。
func encodeFactRefs(ids []int64) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// validAssetType 判物料类型是否在六格货架名单内；这六格就是齐备度的形状，加类型必须同时改 ReadinessFor 与前端渲染，别只改这里。
// 参数：t=类型；返回是否合法。
func validAssetType(t string) bool {
	for _, v := range ProductAssetTypes {
		if v == t {
			return true
		}
	}
	return false
}

// validReviewStatus 判审核态三值；只有 approved 才进「备好的」那一档，未过审与被打回都必须能被界面点名（不许合成一个"不可用"态掩盖原因）。
// 参数：s=审核态；返回是否合法。
func validReviewStatus(s string) bool {
	return s == AssetReviewUnreviewed || s == AssetReviewApproved || s == AssetReviewRejected
}

// assetByIDScoped 读侧与写侧共用的作用域助手；查不到一律 errProductNotFound（不返 403）。
// 参数：tid=租户 ID，id=物料主键；返回物料与错误。
func (s *Store) assetByIDScoped(tid, id int64) (ProductAsset, error) {
	a, err := scanAsset(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+assetCols+" FROM product_assets WHERE tenant_id=? AND id=?", tid, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProductAsset{}, errProductNotFound
	}
	return a, err
}

// PutAsset 写入一条物料（每次写入都是新版本行，不覆盖既有行，保留效期历史）。
// 参数：tid=租户 ID，in=物料内容；返回落库后的行与错误。
func (s *Store) PutAsset(tid int64, in ProductAssetInput) (ProductAsset, error) {
	if err := s.productExistsScoped(tid, in.ProductID); err != nil {
		return ProductAsset{}, err
	}
	if !validAssetType(in.AssetType) {
		return ProductAsset{}, fmt.Errorf("非法的物料类型: %s", in.AssetType)
	}
	if strings.TrimSpace(in.Lang) == "" {
		return ProductAsset{}, errors.New("物料语种不能为空")
	}
	review := in.ReviewStatus
	if review == "" {
		review = AssetReviewUnreviewed
	}
	if !validReviewStatus(review) {
		return ProductAsset{}, fmt.Errorf("非法的审核状态: %s", review)
	}
	vis := in.Visibility
	if vis == "" {
		vis = VisibilityGated
	}
	if !validVisibility(vis) {
		return ProductAsset{}, fmt.Errorf("非法的 visibility: %s", vis)
	}
	srcKind := in.SourceKind
	if srcKind == "" {
		srcKind = TMSourceModel
	}
	if srcKind != TMSourceModel && srcKind != TMSourceHuman && srcKind != TMSourceApproved {
		return ProductAsset{}, fmt.Errorf("非法的物料来源: %s", srcKind)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		`INSERT INTO product_assets
		(tenant_id, product_id, asset_type, lang, body, artifact_ref, review_status,
		 validity, visibility, fact_refs, source_kind, version, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		tid, in.ProductID, in.AssetType, in.Lang, in.Body, in.ArtifactRef, review,
		AssetValidityValid, vis, encodeFactRefs(in.FactRefs), srcKind, 1, now, now)
	if err != nil {
		return ProductAsset{}, fmt.Errorf("物料写入失败: %w", err)
	}
	return s.assetByIDScoped(tid, id)
}

// MarkAssetReview 审核一条物料；审核状态三值写侧硬拒（禁第四值）。
// 参数：tid=租户 ID，id=物料主键，status=目标审核状态；返回错误。
func (s *Store) MarkAssetReview(tid, id int64, status string) error {
	if !validReviewStatus(status) {
		return fmt.Errorf("非法的审核状态: %s", status)
	}
	if _, err := s.assetByIDScoped(tid, id); err != nil {
		return err
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE product_assets SET review_status=?, updated_at=? WHERE tenant_id=? AND id=?",
		status, time.Now().UTC().Format(time.RFC3339), tid, id)
	return err
}

// ExpireAssetsByFactChange 事实变更后作废所有引用它的 valid 物料，返回实际作废行数。
// ★ A4：fact_refs 为空数组（或非法 JSON）的行**永不**被命中；按 Go 侧整数等值比对，不做字符串包含。
// ★ A2：只按传入的 factID 命中，未变更的事实不触发 ⇒ 不会误伤同产品下引用别的事实项的物料。
// 参数：tid=租户 ID，factID=被改动的事实项主键，productID=产品主键；返回作废行数与错误。
func (s *Store) ExpireAssetsByFactChange(tid int64, factID int64, productID string) (int, error) {
	if err := s.productExistsScoped(tid, productID); err != nil {
		return 0, err
	}
	// ★ R5 同族：SQL 必带「租户 + 产品」前缀等值条件，禁止一条 SQL 扫全库。
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id, fact_refs FROM product_assets
		 WHERE tenant_id=? AND product_id=? AND validity=?`,
		tid, productID, AssetValidityValid)
	if err != nil {
		return 0, err
	}
	type hit struct{ id int64 }
	var hits []hit
	for rows.Next() {
		var id int64
		var refs string
		if e := rows.Scan(&id, &refs); e != nil {
			_ = rows.Close()
			return 0, e
		}
		for _, fid := range parseFactRefs(refs) {
			if fid == factID {
				hits = append(hits, hit{id: id})
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	expired := 0
	for _, h := range hits {
		res, err := db.Exec(s.db, db.CurrentDialect(),
			`UPDATE product_assets SET validity=?, updated_at=?
			 WHERE tenant_id=? AND id=? AND validity=?`,
			AssetValidityExpired, now, tid, h.id, AssetValidityValid)
		if err != nil {
			return expired, err
		}
		if n, e := res.RowsAffected(); e == nil {
			expired += int(n)
		}
	}
	return expired, nil
}

// ListAssets 按产品列全部物料（含 expired，供齐备度与货架展示）；空集出 []。
// 参数：tid=租户 ID，productID=产品主键；返回物料列表与错误。
func (s *Store) ListAssets(tid int64, productID string) ([]ProductAsset, error) {
	if err := s.productExistsScoped(tid, productID); err != nil {
		return []ProductAsset{}, err
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+assetCols+" FROM product_assets WHERE tenant_id=? AND product_id=? ORDER BY asset_type, lang, id",
		tid, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProductAsset{}
	for rows.Next() {
		a, e := scanAsset(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAssetsForAnswer 知识层兜底问答的「已备物料原文」：只取 valid 且已审核通过的行，
// 语种为空表示不限语种。★ 零 LLM 路径只读这里，不碰模型（§7.3 N1 的判据落点）。
// 参数：tid=租户 ID，productID=产品主键，lang=目标语种（可空）；返回物料列表与错误。
func (s *Store) ListAssetsForAnswer(tid int64, productID, lang string) ([]ProductAsset, error) {
	if err := s.productExistsScoped(tid, productID); err != nil {
		return []ProductAsset{}, err
	}
	q := `SELECT ` + assetCols + ` FROM product_assets
		WHERE tenant_id=? AND product_id=? AND validity=? AND review_status=?`
	args := []interface{}{tid, productID, AssetValidityValid, AssetReviewApproved}
	if strings.TrimSpace(lang) != "" {
		q += " AND lang=?"
		args = append(args, lang)
	}
	q += " ORDER BY asset_type, id"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProductAsset{}
	for rows.Next() {
		a, e := scanAsset(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AssetForDownload 下载口唯一入口：expired 行直接拒（A3），返回可行动文案。
// 参数：tid=租户 ID，id=物料主键；返回可下载物料与错误。
func (s *Store) AssetForDownload(tid, id int64) (ProductAsset, error) {
	a, err := s.assetByIDScoped(tid, id)
	if err != nil {
		return ProductAsset{}, err
	}
	if a.Validity == AssetValidityExpired {
		return ProductAsset{}, ErrAssetExpired
	}
	return a, nil
}
