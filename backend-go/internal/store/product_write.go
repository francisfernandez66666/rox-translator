// ============ product_write.go · 职责说明 ============
// store 包「产品档案写侧补充」（一期批次 4），product.go 之外的窄域文件：
// 档案删除、资料更新、SEO 索引开关、产品详情读、sitemap 列表、留资产品上下文。
//
// ★ 两条硬口径：
//
//	① 所有写路径先过 productExistsScoped（查不到 ⇒ errProductNotFound，**不返 403**），
//	   且 SQL 恒带 tenant_id=?（SC1/SC2 源码级判据要求作用域助手只有一把）。
//	② tenant_code / page_code / slug 在任何 UPDATE 里都不出现（H2），
//	   seo_indexable 只由本文件的 SetProductSeoIndexable 显式写（S4）。
//
// 所有 SQL 一律走 db.Exec/Query/QueryRow ＋ db.CurrentDialect()。
// ================================================
package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"translator/internal/db"
)

// GetProductScoped 按主键取产品（读侧与写侧共用 productExistsScoped 同一把尺子）。
// 参数：tid=租户 ID，id=产品主键；返回产品、是否命中与错误。
func (s *Store) GetProductScoped(tid int64, id string) (Product, bool, error) {
	if kc := s.kcCutover(); kc != nil {
		items, o, err := kc.ProductGet(kcCtx(nil), tid, id)
		if err != nil {
			return Product{}, false, err
		}
		if e := kcOutcomeErr(o); e != nil {
			return Product{}, false, e
		}
		p, ok := gatherProduct(items)
		return p, ok, nil
	}
	p, err := scanProduct(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+productCols+" FROM products WHERE tenant_id=? AND id=?", tid, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Product{}, false, nil
	}
	if err != nil {
		return Product{}, false, err
	}
	return p, true, nil
}

// DeleteProduct 删除产品档案（一期只删档案行；事实/物料由同租户后续清理，不跨租户蒸发）。
// 参数：tid=租户 ID，id=产品主键；返回错误。
func (s *Store) DeleteProduct(tid int64, id string) error {
	if kc := s.kcCutover(); kc != nil {
		if err := s.productExistsScoped(tid, id); err != nil {
			return err
		}
		_, o, err := kc.ProductDelete(kcCtx(nil), tid, id)
		if err != nil {
			return err
		}
		return kcOutcomeErr(o)
	}
	if err := s.productExistsScoped(tid, id); err != nil {
		return err
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"DELETE FROM products WHERE tenant_id=? AND id=?", tid, id)
	return err
}

// UpdateProductProfile 更新产品资料（不含 name/slug/两段短码/seo_indexable）。
// 参数：tid=租户 ID，id=产品主键，in=资料；返回错误。
func (s *Store) UpdateProductProfile(tid int64, id string, in ProductInput) error {
	if kc := s.kcCutover(); kc != nil {
		if err := s.productExistsScoped(tid, id); err != nil {
			return err
		}
		// ★ 只发 8 个资料列 + updated_at（target_langs 仅显式表态才带），绝不发 name/status/slug 等——
		//   部分列 upsert，服务端不会触碰未传列，沿用库里现值（防"改简介⇒名字被清空"）。
		row := map[string]any{
			"id":            id,
			"code":          in.Code,
			"tagline":       in.Tagline,
			"industry_code": in.IndustryCode,
			"audience":      in.Audience,
			"use_case":      in.UseCase,
			"style_notes":   in.StyleNotes,
			"banned_notes":  in.BannedNotes,
			"updated_at":    deadlineNow(),
		}
		if in.TargetLangs != nil {
			row["target_langs"] = joinLangs(in.TargetLangs)
		}
		_, o, err := kc.ProductUpsert(kcCtx(nil), row)
		if err != nil {
			return err
		}
		return kcOutcomeErr(o)
	}
	if err := s.productExistsScoped(tid, id); err != nil {
		return err
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE products SET code=?, tagline=?, industry_code=?, audience=?, use_case=?,
		 style_notes=?, banned_notes=?, updated_at=? WHERE tenant_id=? AND id=?`,
		in.Code, in.Tagline, in.IndustryCode, in.Audience, in.UseCase,
		in.StyleNotes, in.BannedNotes, time.Now().UTC().Format(time.RFC3339), tid, id)
	if err != nil {
		return err
	}
	// ★ O 组（2026-11-06）：目标语种单独一条 UPDATE，且**只在显式表态时才动**。
	//   并进上面那条就会退化成"改个名字 ⇒ 语种列表被空值覆盖"——那正是 §29
	//   「update 省略字段＝不表态」在产品面上第二次长出来。
	if in.TargetLangs != nil {
		_, err = db.Exec(s.db, db.CurrentDialect(),
			`UPDATE products SET target_langs=?, updated_at=? WHERE tenant_id=? AND id=?`,
			joinLangs(in.TargetLangs), time.Now().UTC().Format(time.RFC3339), tid, id)
	}
	return err
}

// SetProductSeoIndexable 单产品索引开关（S4：关掉后不因改名/live 翻转被重新打开）。
// ★ 这是 seo_indexable 的唯一写点；UPDATE 语句里出现该列的地方只许这一处。
// 参数：tid=租户 ID，id=产品主键，on=是否允许被搜索引擎收录；返回错误。
func (s *Store) SetProductSeoIndexable(tid int64, id string, on bool) error {
	if kc := s.kcCutover(); kc != nil {
		if err := s.productExistsScoped(tid, id); err != nil {
			return err
		}
		v := 0
		if on {
			v = 1
		}
		row := map[string]any{"id": id, "seo_indexable": v, "updated_at": deadlineNow()}
		_, o, err := kc.ProductUpsert(kcCtx(nil), row)
		if err != nil {
			return err
		}
		return kcOutcomeErr(o)
	}
	if err := s.productExistsScoped(tid, id); err != nil {
		return err
	}
	v := 0
	if on {
		v = 1
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE products SET seo_indexable=?, updated_at=? WHERE tenant_id=? AND id=?",
		v, time.Now().UTC().Format(time.RFC3339), tid, id)
	return err
}

// SitemapProduct sitemap 收录的一行（只出两段短码与更新时间，零受控字段）。
type SitemapProduct struct {
	TenantCode string `json:"tenant_code"`
	PageCode   string `json:"page_code"`
	UpdatedAt  string `json:"updated_at"`
}

// ListSitemapProducts 列全部可收录的产品页（跨租户，供公开 sitemap）。
// ★ SQL 硬带 seo_indexable=1 且 status='live'（§5.8 S1），受控字段一个都不出栈。
// 参数：无；返回 URL 片段列表与错误。
func (s *Store) ListSitemapProducts() ([]SitemapProduct, error) {
	if kc := s.kcCutover(); kc != nil {
		// ★ 服务端 SitemapList 返回该租户全部产品行，过滤（seo_indexable=1 AND status=live
		//   且两段短码非空）在 Go 侧复刻主库 WHERE 条件；不泄露受控字段。
		items, o, err := kc.SitemapList(kcCtx(nil))
		if err != nil {
			return nil, err
		}
		if e := kcOutcomeErr(o); e != nil {
			return nil, e
		}
		out := []SitemapProduct{}
		for _, it := range items {
			if rawInt(it, "seo_indexable") != 1 {
				continue
			}
			if rawStr(it, "status") != ProductStatusLive {
				continue
			}
			tc := rawStr(it, "tenant_code")
			pc := rawStr(it, "page_code")
			if tc == "" || pc == "" {
				continue
			}
			out = append(out, SitemapProduct{TenantCode: tc, PageCode: pc, UpdatedAt: rawStr(it, "updated_at")})
		}
		return out, nil
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT tenant_code, page_code, updated_at FROM products
		 WHERE seo_indexable=1 AND status=? AND tenant_code<>'' AND page_code<>''
		 ORDER BY tenant_code, page_code`, ProductStatusLive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SitemapProduct{}
	for rows.Next() {
		var p SitemapProduct
		if e := rows.Scan(&p.TenantCode, &p.PageCode, &p.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetFactVisibility 显式放开/收起一条事实项的公开可见性（白名单式，默认 gated）。
// 参数：tid=租户 ID，id=事实项主键，v=VisibilityPublic/VisibilityGated；返回错误。
func (s *Store) SetFactVisibility(tid, id int64, v string) error {
	if !validVisibility(v) {
		return errors.New("非法的 visibility")
	}
	f, err := s.factByIDScoped(tid, id)
	if err != nil {
		return err
	}
	// cert 归受控档：永不允许显式放开（V1）。
	if f.Kind == FactKindCert && v == VisibilityPublic {
		return errors.New("认证声明只能受控展示")
	}
	if kc := s.kcCutover(); kc != nil {
		row := map[string]any{"id": id, "visibility": v, "updated_at": deadlineNow()}
		_, o, err := kc.FactUpsert(kcCtx(nil), row)
		if err != nil {
			return err
		}
		return kcOutcomeErr(o)
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		"UPDATE product_facts SET visibility=?, updated_at=? WHERE tenant_id=? AND id=?",
		v, time.Now().UTC().Format(time.RFC3339), tid, id)
	return err
}

// SetAssetVisibility 显式放开/收起一条物料的公开可见性。
// 参数：tid=租户 ID，id=物料主键，v=VisibilityPublic/VisibilityGated；返回错误。
func (s *Store) SetAssetVisibility(tid, id int64, v string) error {
	if !validVisibility(v) {
		return errors.New("非法的 visibility")
	}
	if _, err := s.assetByIDScoped(tid, id); err != nil {
		return err
	}
	if kc := s.kcCutover(); kc != nil {
		row := map[string]any{"id": id, "visibility": v, "updated_at": deadlineNow()}
		_, o, err := kc.AssetUpsert(kcCtx(nil), row)
		if err != nil {
			return err
		}
		return kcOutcomeErr(o)
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE product_assets SET visibility=?, updated_at=? WHERE tenant_id=? AND id=?",
		v, time.Now().UTC().Format(time.RFC3339), tid, id)
	return err
}

// ProductLeadContext 一条匿名留资的产品上下文（V3/V5：handle → 产品 → 租户 三段解析同一租户）。
type ProductLeadContext struct {
	Product    Product
	TenantCode string
	PageCode   string
	LeadSource string
}

// ResolveProductLeadContext 由两段短码解析留资归属；★ 租户归属只取自产品行，
// 绝不采信请求里的 tenant_id 参数（V6：伪造 ?tenant_id 不改落库归属）。
// 参数：tenantCode/pageCode=对外页两段短码，source=来源标记；返回上下文与错误。
func (s *Store) ResolveProductLeadContext(tenantCode, pageCode, source string) (ProductLeadContext, error) {
	p, ok, err := s.GetProductByCodes(tenantCode, pageCode)
	if err != nil {
		return ProductLeadContext{}, err
	}
	if !ok {
		return ProductLeadContext{}, errProductNotFound
	}
	return ProductLeadContext{
		Product:    p,
		TenantCode: p.TenantCode,
		PageCode:   p.PageCode,
		LeadSource: strings.TrimSpace(source),
	}, nil
}

// CreateProductLead 落一条带产品上下文的匿名留资（V3 四列复用 feedbacks，不建新表）。
// ★ 与 CreateFeedback 的区别只在这四列；内容摘要不带任何受控字段字面量。
// 参数：ctx=产品上下文，content=留言正文，langs=意向语种；返回新行 ID 与错误。
func (s *Store) CreateProductLead(ctx ProductLeadContext, content, langs string) (int64, error) {
	if ctx.Product.ID == "" {
		return 0, errProductNotFound
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		`INSERT INTO feedbacks
		(tenant_id, user_id, target_type, ticket_id, source_text, translations, target_langs,
		 mode, content, with_context, status, created_at,
		 product_id, tenant_code, page_code, lead_source)
		VALUES (?,0,'lead',0,'','{}',?,'',?,0,'open',?,?,?,?,?)`,
		ctx.Product.TenantID, langs, content, now,
		ctx.Product.ID, ctx.TenantCode, ctx.PageCode, ctx.LeadSource)
	if err != nil {
		return 0, err
	}
	return id, nil
}
