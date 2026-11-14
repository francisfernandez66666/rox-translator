// ============ product_readiness.go · 职责说明 ============
// store 包「齐备度现算」（product_assets 派生读数），一期《确定的多语言能力》批次 2。
//
// ★ 为什么现算不落表（裁定 2）：建物化表＝多一个裸写点、必须逐处钩失效，
//
//	且它天然是「第二份事实」；现算永远与 product_assets 同一把尺子。
//
// ★ 代价当场量住（断言 R5）：SQL 必带 `product_id = ?` 前缀等值条件，入参只允许单产品；
//
//	列表页齐备度由调用侧逐产品单查，**禁止**一条 SQL 扫全租户/全库。
//
// ★ missing 不落行：缺由「该格无 valid 行」推得；落行会让「缺」与「有但空」同形。
//
//	返回集恒为六格 × ProductLangs 全列（R3），空产品也出满格 missing，不出 null。
//
// 所有 SQL 一律走 db.Query ＋ db.CurrentDialect()。
// ================================================
package store

import (
	"translator/internal/db"
)

// ProductLangs 对外内容的产品口径 12 语种（与 frontend-react/src/seo/route_meta.json
// 的 languages 逐项等值；★ 词典文件 10 份是另一把尺子，别混）。
var ProductLangs = []string{
	"zh", "zh_hant", "en", "ru", "fr", "ar", "es", "pt", "de", "ja", "ko", "th",
}

// 齐备度三态（worst 的取值；缺与过期不同形，前端文案/类名不许共用）。
const (
	ReadinessValid   = "valid"
	ReadinessExpired = "expired"
	ReadinessMissing = "missing"
)

// AssetReadiness 六格 × 语种矩阵里的一个格。
type AssetReadiness struct {
	AssetType string `json:"asset_type"`
	Lang      string `json:"lang"`
	Count     int    `json:"count"`
	Worst     string `json:"worst"`
}

// readinessRow SQL 聚合的一行（按 asset_type + lang 分组）。
type readinessRow struct {
	assetType string
	lang      string
	count     int
	valid     int
	expired   int
}

// ReadinessFor 现算某产品的齐备度：恒返回六格 × 12 语种的完整矩阵。
// worst 取「该格还有没有可用的」这一把尺子：有 valid 行＝valid；无 valid 但有 expired＝expired；
// 一行都没有＝missing（R2：缺读 missing，不是读 0）。
// 参数：tid=租户 ID，productID=产品主键（只允许单产品）；返回矩阵与错误。
func (s *Store) ReadinessFor(tid int64, productID string) ([]AssetReadiness, error) {
	if kc := s.kcCutover(); kc != nil {
		// ★ R5：仍只允许单产品；服务端返回该租户全部物料，这里按 product_id 前缀等值过滤后现算。
		items, o, err := kc.AssetsList(kcCtx(nil), tid, "")
		if err != nil {
			return nil, err
		}
		if e := kcOutcomeErr(o); e != nil {
			return nil, e
		}
		type agg struct{ count, valid, expired int }
		byKey := map[string]agg{}
		for _, it := range items {
			if rawStr(it, "product_id") != productID {
				continue
			}
			key := rawStr(it, "asset_type") + "|" + rawStr(it, "lang")
			a := byKey[key]
			a.count++
			switch rawStr(it, "validity") {
			case AssetValidityValid:
				a.valid++
			case AssetValidityExpired:
				a.expired++
			}
			byKey[key] = a
		}
		out := make([]AssetReadiness, 0, len(ProductAssetTypes)*len(ProductLangs))
		for _, at := range ProductAssetTypes {
			for _, lang := range ProductLangs {
				cell := AssetReadiness{AssetType: at, Lang: lang, Worst: ReadinessMissing}
				if r, ok := byKey[at+"|"+lang]; ok {
					cell.Count = r.count
					switch {
					case r.valid > 0:
						cell.Worst = ReadinessValid
					case r.expired > 0:
						cell.Worst = ReadinessExpired
					default:
						cell.Worst = ReadinessMissing
					}
				}
				out = append(out, cell)
			}
		}
		return out, nil
	}
	// ★ R5：product_id = ? 是源码级判据的字面锚点，不许改成全租户聚合。
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT asset_type, lang, COUNT(*),
			SUM(CASE WHEN validity=? THEN 1 ELSE 0 END),
			SUM(CASE WHEN validity=? THEN 1 ELSE 0 END)
		 FROM product_assets
		 WHERE tenant_id=? AND product_id = ?
		 GROUP BY asset_type, lang`,
		AssetValidityValid, AssetValidityExpired, tid, productID)
	if err != nil {
		return nil, err
	}
	byKey := map[string]readinessRow{}
	for rows.Next() {
		var r readinessRow
		if e := rows.Scan(&r.assetType, &r.lang, &r.count, &r.valid, &r.expired); e != nil {
			_ = rows.Close()
			return nil, e
		}
		byKey[r.assetType+"|"+r.lang] = r
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	out := make([]AssetReadiness, 0, len(ProductAssetTypes)*len(ProductLangs))
	for _, at := range ProductAssetTypes {
		for _, lang := range ProductLangs {
			cell := AssetReadiness{AssetType: at, Lang: lang, Worst: ReadinessMissing}
			if r, ok := byKey[at+"|"+lang]; ok {
				cell.Count = r.count
				switch {
				case r.valid > 0:
					cell.Worst = ReadinessValid
				case r.expired > 0:
					cell.Worst = ReadinessExpired
				default:
					cell.Worst = ReadinessMissing
				}
			}
			out = append(out, cell)
		}
	}
	return out, nil
}
