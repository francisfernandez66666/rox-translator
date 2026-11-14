// ============ product.go · 职责说明 ============
// store 包「产品档案」（products 表）窄域文件，一期《确定的多语言能力》第一批。
// 承载建档、列表、改名、两段短码解析、状态流转；所有 SQL 一律走 db.Exec/db.Query/db.QueryRow
// 并显式传 db.CurrentDialect()（db/guard_test.go 的裸 SQL 闸要求）。
//
// ★ 两段短码（tenant_code / page_code）创建即定、永不改写：已印在名片、二维码、
//
//	被搜索引擎收录的链接不能因改名失效。所有 UPDATE 语句都不许出现这两列（断言 H2）。
//
// ★ tenant_code 按租户稳定：同租户第一个产品签发后，后续产品复用同一段，保证
//
//	/p/{tenantCode}/ 是同一个入口。★ 2026-11-07 二期开工前置核实结案：
//	tenantCodeFor 用 idgen.NewCode("") 独立签发不可枚举随机短码，全程不读 tenants.code，
//	故「tenants.code 是否可枚举」与本 URL 无关，不再是实施前置；短码只由 products 表持有。
//
// ★ 解析口两段都做等值查询；ux_product_page(page_code) 全局唯一，因此
//
//	「A 的租户段 ＋ B 的页面段」拼不出任何行 ⇒ not-found，不返 403、不泄露他租户存在性。
//
// ================================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/idgen"
)

// 产品状态字面三值（禁第四值，product_gate_test.go 的 P1/P2 判）。
const (
	ProductStatusDraft    = "draft"
	ProductStatusLive     = "live"
	ProductStatusArchived = "archived"
)

// 产品列表分页档位（api 层只做同档镜像，出栈 limit 必须真生效）。
const (
	ProductPageDefault = 20
	ProductPageMax     = 100
)

// errProductNotFound 产品不存在或不属于该租户；写侧与读侧共用，租户隔离下不返 403。
var errProductNotFound = errors.New("产品不存在")

// Product 产品档案行。
type Product struct {
	ID           string `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	UserID       int64  `json:"user_id"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	Tagline      string `json:"tagline"`
	IndustryCode string `json:"industry_code"`
	Audience     string `json:"audience"`
	UseCase      string `json:"use_case"`
	StyleNotes   string `json:"style_notes"`
	BannedNotes  string `json:"banned_notes"`
	Status       string `json:"status"`
	Slug         string `json:"slug"`
	TenantCode   string `json:"tenant_code"`
	PageCode     string `json:"page_code"`
	SeoIndexable int    `json:"seo_indexable"`
	// TargetLangs 是这款产品**要对外说的语言**（逗号分隔的语种码）。
	// ★ 2026-11-06 O 组裁定：这一维**只存产品行**，不建用户级/租户级默认档——
	//   好处是 hreflang 集合、物料语种、引导第 3 步勾的那一份从此有同一个事实源；
	//   代价是多产品要各勾一次（用户已接受）。禁止再在调用方拼"界面语种＝目标语种"的猜测。
	// 出栈形态只有这一个：数组（空＝[]，不是 null、不是 ""）。
	// ★ 2026-11-06 e2e 抓到过一版"两副面孔"：/api/product/list 直接把这个 CSV 字符串吐出去，
	//   而 /api/product/public 吐的是数组 ⇒ 同一个字段两种形状，消费方（前端、SDK、手写脚本）
	//   要各判一次；更坏的是字符串 "en" 对上 .includes('n') 这种**看起来能用**的错用。
	//   ⇒ 库内那一列仍按 CSV 存（迁移与既有读点不动），但**序列化只走 TargetLangsList**。
	TargetLangs string `json:"-"`
	// TargetLangsList 是 target_langs 的**唯一出栈形态**（语种码数组，顺序＝卖家提交顺序）。
	// 由 fillLangsList 在扫描/构造后填；手工构造 Product 时若忘了填，出栈就是 []（不是脏值）。
	TargetLangsList []string `json:"target_langs"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

// ProductInput 建档入参。Slug 为空时写侧生成兜底值（DDL 为 NOT NULL 无默认，绝不让空串落库）；
// TenantCode 为空时按租户复用／签发，调用方不许传自增数字租户 id。
type ProductInput struct {
	Code         string
	Name         string
	Tagline      string
	IndustryCode string
	Audience     string
	UseCase      string
	StyleNotes   string
	BannedNotes  string
	Slug         string
	UserID       int64
	TenantCode   string
	// TargetLangs 用指针是为了把"没表态"和"清空"分开：
	//   nil ＝ 沿用库里现值（AGENTS §29「update 省略字段＝不表态」）；
	//   &[]string{} ＝ 显式清空；非空 ＝ 整份替换。
	// 值语义在这里没有第三条出路：写空串既可能是"没填"也可能是"要清空"，两种现场同形。
	TargetLangs *[]string
}

// productCols 产品列全集；新增列只改这一处，读侧不会各写一份。
const productCols = `id, tenant_id, user_id, code, name, tagline, industry_code, audience,
use_case, style_notes, banned_notes, status, slug, tenant_code, page_code,
seo_indexable, target_langs, created_at, updated_at`

// scanProduct 把一行产品扫描成结构；row 可为 *sql.Row 或 *sql.Rows。
type productScanner interface {
	Scan(dest ...interface{}) error
}

// scanProduct 按 productCols 的**列序逐位**扫描；顺序改动必须与此处同步，否则一格值会静默落到另一格（不报错）。
// 参数：row=可为 *sql.Row 或 *sql.Rows；返回产品结构与扫描错误（不吞 ErrNoRows，交由调用方判 not-found）。
func scanProduct(row productScanner) (Product, error) {
	var p Product
	err := row.Scan(
		&p.ID, &p.TenantID, &p.UserID, &p.Code, &p.Name, &p.Tagline, &p.IndustryCode,
		&p.Audience, &p.UseCase, &p.StyleNotes, &p.BannedNotes, &p.Status, &p.Slug,
		&p.TenantCode, &p.PageCode, &p.SeoIndexable, &p.TargetLangs, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return p, err
	}
	// 出栈形态在这里派生一次（见 TargetLangsList 的注释）：扫描口只有一个，所以数组不会漏填
	p.fillLangsList()
	return p, err
}

// fillLangsList 把库内 CSV 派生成出栈数组（**包内唯一写点**，扫描与构造两支各调一次）。
// 参数：无；返回：无（就地改）。
func (p *Product) fillLangsList() { p.TargetLangsList = splitLangs(p.TargetLangs) }

// joinLangs 把语种切片拼成库内 CSV：去空白、去空项、去重、保持提交顺序（顺序给 hreflang 当主次用，不许排成字典序）。
// 参数：langs=nil 视作空串；返回归一后的串。
func joinLangs(langs *[]string) string {
	if langs == nil {
		return ""
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(*langs))
	for _, raw := range *langs {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return strings.Join(out, ",")
}

// splitLangs 把库内 CSV 拆回切片（空串回空切片，不回 nil——出栈形状对消费方是契约，见 §一·5 空集归一那条）。
// 参数：csv=库内值；返回语种码切片。
func splitLangs(csv string) []string {
	out := []string{}
	for _, v := range strings.Split(csv, ",") {
		if t := strings.TrimSpace(v); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// TargetLangList 出这个产品声明过的对外语言（未声明回空切片，**不回 nil**）。
// 参数：无；返回语种码切片（顺序＝卖家提交顺序）。
func (p Product) TargetLangList() []string {
	if p.TargetLangsList != nil {
		return p.TargetLangsList
	}
	return splitLangs(p.TargetLangs)
}

// SplitTargetLangs 给 api 层用的同一把拆分尺子（读库外CSV形态时也必须走这里，不许各处 strings.Split）。
// 参数：csv=库内 target_langs 值；返回语种码切片。
func SplitTargetLangs(csv string) []string { return splitLangs(csv) }

// tenantCodeFor 取该租户已签发的租户段；没有则新签一枚不可枚举短码。
// 参数：tid=租户 ID；返回租户段与错误。
func (s *Store) tenantCodeFor(tid int64) (string, error) {
	if kc := s.kcCutover(); kc != nil {
		items, _, o, err := kc.ProductsList(kcCtx(nil), tid, 0, 1)
		if err != nil {
			return "", err
		}
		if e := kcOutcomeErr(o); e != nil {
			return "", e
		}
		for _, it := range items {
			if tc := rawStr(it, "tenant_code"); tc != "" {
				return tc, nil
			}
		}
		return idgen.NewCode("")
	}
	var code string
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT tenant_code FROM products WHERE tenant_id=? LIMIT 1", tid).Scan(&code)
	if err == nil && code != "" {
		return code, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("读取租户段失败: %w", err)
	}
	return idgen.NewCode("")
}

// CreateProduct 建一个产品档案，并签发两段不可枚举短码。
// 参数：tid=租户 ID，in=建档内容；返回带 TenantCode/PageCode 的产品行与错误。
func (s *Store) CreateProduct(tid int64, in ProductInput) (Product, error) {
	if tid <= 0 {
		return Product{}, errors.New("产品建档必须带有效租户")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Product{}, errors.New("产品名不能为空")
	}
	tenantCode := strings.TrimSpace(in.TenantCode)
	if tenantCode == "" {
		var err error
		tenantCode, err = s.tenantCodeFor(tid)
		if err != nil {
			return Product{}, err
		}
	}
	pageCode, err := idgen.NewCode("")
	if err != nil {
		return Product{}, err
	}
	id, err := idgen.NewCode("prd_")
	if err != nil {
		return Product{}, err
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		// 不落空串：DDL 三列 NOT NULL 无默认，唯一索引会把空串也当一个值（第二行必撞）。
		slug = "p-" + pageCode[:12]
	}
	now := time.Now().UTC().Format(time.RFC3339)
	out := Product{
		ID: id, TenantID: tid, UserID: in.UserID, Code: in.Code, Name: name,
		Tagline: in.Tagline, IndustryCode: in.IndustryCode, Audience: in.Audience,
		UseCase: in.UseCase, StyleNotes: in.StyleNotes, BannedNotes: in.BannedNotes,
		Status: ProductStatusDraft, Slug: slug, TenantCode: tenantCode, PageCode: pageCode,
		SeoIndexable: 1, TargetLangs: joinLangs(in.TargetLangs), CreatedAt: now, UpdatedAt: now,
	}
	// 建档这一支没走 scanProduct，数组必须就地派生（否则前端刚建完读回的是 []）
	out.fillLangsList()
	if kc := s.kcCutover(); kc != nil {
		row := productToRow(out, nil)
		_, o, werr := kc.ProductUpsert(kcCtx(nil), row)
		if werr != nil {
			return Product{}, werr
		}
		if e := kcOutcomeErr(o); e != nil {
			return Product{}, e
		}
		return out, nil
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO products
		(id, tenant_id, user_id, code, name, tagline, industry_code, audience,
		 use_case, style_notes, banned_notes, status, slug, tenant_code, page_code,
		 seo_indexable, target_langs, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, tid, in.UserID, in.Code, name, in.Tagline, in.IndustryCode, in.Audience,
		in.UseCase, in.StyleNotes, in.BannedNotes, ProductStatusDraft, slug, tenantCode,
		pageCode, 1, joinLangs(in.TargetLangs), now, now)
	if err != nil {
		return Product{}, fmt.Errorf("产品建档失败: %w", err)
	}
	return out, nil
}

// ListProducts 按租户分页列产品；脏参折回档位，空集返回非 nil 空切片（出栈 [] 而非 null）。
// 参数：tid=租户 ID，page=页码（从 1 起），pageSize=每页条数；返回列表、总数与错误。
func (s *Store) ListProducts(tid int64, page, pageSize int) ([]Product, int, error) {
	if pageSize <= 0 || pageSize > ProductPageMax {
		pageSize = ProductPageDefault
	}
	if page <= 0 {
		page = 1
	}
	if kc := s.kcCutover(); kc != nil {
		items, _, o, err := kc.ProductsList(kcCtx(nil), tid, 0, 0)
		if err != nil {
			return nil, 0, err
		}
		if e := kcOutcomeErr(o); e != nil {
			return nil, 0, e
		}
		all := kcProducts(items)
		// 排序口径与主库一致：created_at DESC, id DESC（RFC3339 串字典序即时间序）。
		sort.SliceStable(all, func(i, j int) bool {
			if all[i].CreatedAt != all[j].CreatedAt {
				return all[i].CreatedAt > all[j].CreatedAt
			}
			return all[i].ID > all[j].ID
		})
		total := len(all)
		start := (page - 1) * pageSize
		if start >= total {
			return []Product{}, total, nil
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		return all[start:end], total, nil
	}
	var total int
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM products WHERE tenant_id=?", tid).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+productCols+" FROM products WHERE tenant_id=? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?",
		tid, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// GetProductByCodes 对外页解析口：两段短码各做等值查询，跨租户拼接取不到行。
// 参数：tenantCode=租户段，pageCode=页面段；返回产品、是否命中与错误。
func (s *Store) GetProductByCodes(tenantCode, pageCode string) (Product, bool, error) {
	if strings.TrimSpace(tenantCode) == "" || strings.TrimSpace(pageCode) == "" {
		return Product{}, false, nil
	}
	if kc := s.kcCutover(); kc != nil {
		items, o, err := kc.ProductResolveByCodes(kcCtx(nil), tenantCode, pageCode)
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
		"SELECT "+productCols+" FROM products WHERE tenant_code=? AND page_code=?",
		tenantCode, pageCode))
	if errors.Is(err, sql.ErrNoRows) {
		return Product{}, false, nil
	}
	if err != nil {
		return Product{}, false, err
	}
	return p, true, nil
}

// productExistsScoped 读侧与写侧共用的作用域助手：查不到一律同一个错误，不泄露他租户存在性。
// 参数：tid=租户 ID，id=产品主键；返回错误。
func (s *Store) productExistsScoped(tid int64, id string) error {
	if kc := s.kcCutover(); kc != nil {
		items, o, err := kc.ProductGet(kcCtx(nil), tid, id)
		if err != nil {
			return err
		}
		if e := kcOutcomeErr(o); e != nil {
			return e
		}
		if len(items) == 0 {
			return errProductNotFound
		}
		return nil
	}
	var one int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT 1 FROM products WHERE tenant_id=? AND id=?", tid, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return errProductNotFound
	}
	return err
}

// UpdateProductName 改产品名；★ 只碰 name/updated_at，两段短码与 slug 永不改写（断言 H1/H2）。
// 参数：tid=租户 ID，id=产品主键，name=新名称；返回错误。
func (s *Store) UpdateProductName(tid int64, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("产品名不能为空")
	}
	if kc := s.kcCutover(); kc != nil {
		if err := s.productExistsScoped(tid, id); err != nil {
			return err
		}
		row := map[string]any{"id": id, "name": name, "updated_at": time.Now().UTC().Format(time.RFC3339)}
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
		"UPDATE products SET name=?, updated_at=? WHERE tenant_id=? AND id=?",
		name, time.Now().UTC().Format(time.RFC3339), tid, id)
	return err
}

// SetProductStatus 流转产品状态；非法值在写侧硬拒（禁第四值）。
// 参数：tid=租户 ID，id=产品主键，status=目标状态；返回错误。
func (s *Store) SetProductStatus(tid int64, id, status string) error {
	switch status {
	case ProductStatusDraft, ProductStatusLive, ProductStatusArchived:
	default:
		return fmt.Errorf("非法的产品状态: %s", status)
	}
	if kc := s.kcCutover(); kc != nil {
		if err := s.productExistsScoped(tid, id); err != nil {
			return err
		}
		row := map[string]any{"id": id, "status": status, "updated_at": time.Now().UTC().Format(time.RFC3339)}
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
		"UPDATE products SET status=?, updated_at=? WHERE tenant_id=? AND id=?",
		status, time.Now().UTC().Format(time.RFC3339), tid, id)
	return err
}
