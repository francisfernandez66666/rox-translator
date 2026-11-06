// ============ product_fact.go · 职责说明 ============
// store 包「产品事实项」（product_facts 表）窄域文件，一期《确定的多语言能力》批次 2。
//
// ★ 三条硬口径（都是写侧硬拒，不靠数据库约束兜）：
//
//	① 只有 status='confirmed' 的事实可被对外内容引用；本文件的 UpsertFact **不接受 status 入参**，
//	   事实一改就退回 unverified，确认只能走 ConfirmFact（否则「改了值还挂着已确认」＝发错认证）。
//	② 合规类 kind='cert' 永不允许 source!='manual' 落成 confirmed（断言 P4）；
//	   非人工来源（ocr/external_ref）一律不可确认（§5.1 confirm 仅 source='manual'）。
//	③ 事实值变更当场作废引用它的物料（A3 的唯一写点）：UpsertFact 内部调
//	   ExpireAssetsByFactChange，避免 API 层漏调导致「事实改了、旧报价单还在被下载」。
//
// ★ 公开检索语料只有 ListPublicCorpus 一个出口，SQL 硬带 visibility = 'public'（V2/V3）；
//
//	受控行照样进内部术语闸与译文记忆，只有公开出栈这一维被拦。
//
// 所有 SQL 一律走 db.Exec/Query/QueryRow/InsertID ＋ db.CurrentDialect()（db/guard_test.go 裸 SQL 闸）。
// ================================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
)

// 事实项状态字面三值（禁第四值；unverified 为建档缺省）。
const (
	FactStatusUnverified = "unverified"
	FactStatusConfirmed  = "confirmed"
	FactStatusWithdrawn  = "withdrawn"
)

// 事实项来源字面三值（external_ref＝竞品页引用，界面必须标「引用，非本店声明」）。
const (
	FactSourceManual      = "manual"
	FactSourceOCR         = "ocr"
	FactSourceExternalRef = "external_ref"
)

// 可见性两值（fact 与 asset 共用一把尺子；默认 gated＝漏配一行只朝保守）。
const (
	VisibilityPublic = "public"
	VisibilityGated  = "gated"
)

// FactKindCert 认证／合规声明类事实：受控档，且只允许人工来源确认。
const FactKindCert = "cert"

// ProductFactKinds 事实项 kind 的对外契约九值（12 语种要翻译，新增走同族棘轮锁）。
var ProductFactKinds = []string{
	"spec", "material", "price", "moq", "cert", "capacity", "warranty", "usecase", "other",
}

// ProductFact 产品事实项行。
type ProductFact struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	ProductID  string `json:"product_id"`
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Status     string `json:"status"`
	Visibility string `json:"visibility"`
	Source     string `json:"source"`
	Locked     int    `json:"locked"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// ProductFactInput 事实项写入入参。★ 刻意不含 Status：确认只能经 ConfirmFact，撤回只能经 WithdrawFact。
type ProductFactInput struct {
	ProductID  string
	Kind       string
	Key        string
	Value      string
	Visibility string
	Source     string
}

// PublicCorpusItem 公开检索语料的一条（事实或物料）。受控字段永不进这个结构。
type PublicCorpusItem struct {
	ID     int64  `json:"id"`
	Source string `json:"source"` // fact | asset
	Kind   string `json:"kind"`   // fact.kind 或 asset.asset_type
	Key    string `json:"key"`
	Lang   string `json:"lang"`
	Text   string `json:"text"`
}

// factCols 事实项列全集；读侧不各写一份。
const factCols = `id, tenant_id, product_id, kind, key, value, status, visibility, source,
locked, created_at, updated_at`

// scanFact 按 factCols 列序逐位扫描；新增列一律「productCols 与 Scan 同批改」，只改一处会让字段错位一格且不报错。
// 参数：row=行扫描器；返回事实项与错误。
func scanFact(row productScanner) (ProductFact, error) {
	var f ProductFact
	err := row.Scan(&f.ID, &f.TenantID, &f.ProductID, &f.Kind, &f.Key, &f.Value,
		&f.Status, &f.Visibility, &f.Source, &f.Locked, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

// validFactKind 判事实项类型是否在 ProductFactKinds 名单内——名单是唯一真源，写侧拒非法值而不是静默存下（脏值会让 12 语种取词落空）。
// 参数：kind=待判类型；返回是否合法。
func validFactKind(kind string) bool {
	for _, k := range ProductFactKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// validFactSource 判事实来源三值（人工/OCR 抽取/外部引用）；认证类只许 manual 才能落 confirmed，判据入口在此，别在调用方各写一遍。
// 参数：src=来源标记；返回是否合法。
func validFactSource(src string) bool {
	return src == FactSourceManual || src == FactSourceOCR || src == FactSourceExternalRef
}

// validVisibility 判可见性两值（public/gated）；缺省必须是 gated，公开是逐条显式动作——方向反了会让漏配行把报价公开出去。
// 参数：v=可见性；返回是否合法。
func validVisibility(v string) bool {
	return v == VisibilityPublic || v == VisibilityGated
}

// factByIDScoped 读侧与写侧共用的作用域助手：查不到一律 errProductNotFound（不返 403、不泄露他租户存在性）。
// 参数：tid=租户 ID，id=事实项主键；返回事实项与错误。
func (s *Store) factByIDScoped(tid, id int64) (ProductFact, error) {
	f, err := scanFact(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+factCols+" FROM product_facts WHERE tenant_id=? AND id=?", tid, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProductFact{}, errProductNotFound
	}
	return f, err
}

// UpsertFact 按 (product_id, kind, key) 幂等写入一条事实；值变更时退回 unverified 并当场作废引用物料。
// 参数：tid=租户 ID，in=事实内容；返回落库后的行与错误。
func (s *Store) UpsertFact(tid int64, in ProductFactInput) (ProductFact, error) {
	if err := s.productExistsScoped(tid, in.ProductID); err != nil {
		return ProductFact{}, err
	}
	kind := strings.TrimSpace(in.Kind)
	if !validFactKind(kind) {
		return ProductFact{}, fmt.Errorf("非法的 fact kind: %s", kind)
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return ProductFact{}, errors.New("事实项 key 不能为空")
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = FactSourceManual
	}
	if !validFactSource(source) {
		return ProductFact{}, fmt.Errorf("非法的 fact source: %s", source)
	}
	vis := strings.TrimSpace(in.Visibility)
	if vis == "" {
		vis = VisibilityGated
	}
	// ★ V1：cert 归受控档。调用方没显式放开时一律 gated；显式放开也只由 API 层的白名单裁决。
	if vis == VisibilityPublic && kind == FactKindCert {
		vis = VisibilityGated
	}
	if !validVisibility(vis) {
		return ProductFact{}, fmt.Errorf("非法的 visibility: %s", vis)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	existing, err := scanFact(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+factCols+" FROM product_facts WHERE tenant_id=? AND product_id=? AND kind=? AND key=?",
		tid, in.ProductID, kind, key))
	if errors.Is(err, sql.ErrNoRows) {
		id, ierr := db.InsertID(s.db, db.CurrentDialect(), "id",
			`INSERT INTO product_facts
			(tenant_id, product_id, kind, key, value, status, visibility, source, locked, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,0,?,?)`,
			tid, in.ProductID, kind, key, in.Value, FactStatusUnverified, vis, source, now, now)
		if ierr != nil {
			return ProductFact{}, fmt.Errorf("事实项写入失败: %w", ierr)
		}
		return s.factByIDScoped(tid, id)
	}
	if err != nil {
		return ProductFact{}, err
	}
	changed := existing.Value != in.Value
	status := existing.Status
	if changed {
		status = FactStatusUnverified
	}
	// ★ 更新时**空值＝"没表态"，不是"改成缺省档"**（V1 的安全缺省只管新建那一支）：
	//   旧形态把省略的 visibility 一律写回 gated ⇒ 卖家只是改个数，那一行就静默从对外页消失；
	//   source 同理——省略就写 manual 等于把"模型/采集来的行"升级成"人工确认过的行"（confirm 的资格由此白拿）。
	//   两条都回落成"沿用库里现值"，要改档必须显式说。
	if strings.TrimSpace(in.Visibility) == "" {
		vis = existing.Visibility
	}
	if strings.TrimSpace(in.Source) == "" {
		source = existing.Source
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`UPDATE product_facts SET value=?, status=?, visibility=?, source=?, updated_at=?
		 WHERE tenant_id=? AND id=?`,
		in.Value, status, vis, source, now, tid, existing.ID)
	if err != nil {
		return ProductFact{}, fmt.Errorf("事实项更新失败: %w", err)
	}
	// ★ A3 唯一写点：事实值变更 ⇒ 引用它的物料翻 expired（不是靠 API 层记得调）。
	if changed {
		if _, err := s.ExpireAssetsByFactChange(tid, existing.ID, in.ProductID); err != nil {
			return ProductFact{}, fmt.Errorf("事实变更作废物料失败: %w", err)
		}
	}
	return s.factByIDScoped(tid, existing.ID)
}

// ConfirmFact 确认一条事实。★ 非人工来源一律拒；cert ＋ 非人工来源单独点名（断言 P4）。
// 参数：tid=租户 ID，id=事实项主键，userID=操作人（审计用，当前不落列）；返回错误。
func (s *Store) ConfirmFact(tid, id, userID int64) error {
	f, err := s.factByIDScoped(tid, id)
	if err != nil {
		return err
	}
	if f.Kind == FactKindCert && f.Source != FactSourceManual {
		return fmt.Errorf("认证声明只允许人工录入的事实确认（kind=cert source=%s）", f.Source)
	}
	if f.Source != FactSourceManual {
		return fmt.Errorf("只有人工录入的事实可确认（source=%s）", f.Source)
	}
	if userID <= 0 {
		return errors.New("确认事实必须带有效操作人")
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		"UPDATE product_facts SET status=?, locked=1, updated_at=? WHERE tenant_id=? AND id=?",
		FactStatusConfirmed, time.Now().UTC().Format(time.RFC3339), tid, id)
	return err
}

// WithdrawFact 撤回一条事实（撤回态不可被对外内容引用）。
// 参数：tid=租户 ID，id=事实项主键，userID=操作人；返回错误。
func (s *Store) WithdrawFact(tid, id, userID int64) error {
	if _, err := s.factByIDScoped(tid, id); err != nil {
		return err
	}
	if userID <= 0 {
		return errors.New("撤回事实必须带有效操作人")
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE product_facts SET status=?, updated_at=? WHERE tenant_id=? AND id=?",
		FactStatusWithdrawn, time.Now().UTC().Format(time.RFC3339), tid, id)
	return err
}

// FactsByIDSet 按 id 集合取事实项原态（带 Status）。★ 只读原态、不过滤 confirmed：
// 「只有 confirmed 可被引用」的判据放在 engine 一处（§3.1），这里不造第二把尺子。
// 参数：tid=租户 ID，ids=事实项主键集合；返回 id→事实项 的映射与错误。
func (s *Store) FactsByIDSet(tid int64, ids []int64) (map[int64]ProductFact, error) {
	out := map[int64]ProductFact{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, tid)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+factCols+" FROM product_facts WHERE tenant_id=? AND id IN ("+placeholders(len(ids))+")",
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		f, e := scanFact(rows)
		if e != nil {
			return nil, e
		}
		out[f.ID] = f
	}
	return out, rows.Err()
}

// ListFacts 按产品列全部事实项；空集返回非 nil 空切片（出栈 [] 而非 null）。
// 参数：tid=租户 ID，productID=产品主键；返回事实项列表与错误。
func (s *Store) ListFacts(tid int64, productID string) ([]ProductFact, error) {
	if err := s.productExistsScoped(tid, productID); err != nil {
		return []ProductFact{}, err
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+factCols+" FROM product_facts WHERE tenant_id=? AND product_id=? ORDER BY kind, id",
		tid, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProductFact{}
	for rows.Next() {
		f, e := scanFact(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListPublicCorpus 公开检索语料的**唯一出口**：事实项与物料各一条查询，SQL 硬带
// visibility = 'public'（受控行由 SQL 条件挡掉，不是靠应用层 if ⇒ 断言 V3）。
// 事实项还带 status='confirmed'（§2.2：只有 confirmed 可被对外内容引用）。
// 参数：tid=租户 ID，productID=产品主键；返回公开语料条目与错误。
func (s *Store) ListPublicCorpus(tid int64, productID string) ([]PublicCorpusItem, error) {
	out := []PublicCorpusItem{}
	if err := s.productExistsScoped(tid, productID); err != nil {
		return out, err
	}
	frows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id, kind, key, '' , value FROM product_facts
		 WHERE tenant_id=? AND product_id=? AND visibility = 'public' AND status=?
		 ORDER BY id`, tid, productID, FactStatusConfirmed)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var it PublicCorpusItem
		it.Source = "fact"
		if e := frows.Scan(&it.ID, &it.Kind, &it.Key, &it.Lang, &it.Text); e != nil {
			_ = frows.Close()
			return nil, e
		}
		out = append(out, it)
	}
	if err := frows.Err(); err != nil {
		_ = frows.Close()
		return nil, err
	}
	_ = frows.Close()
	arows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id, asset_type, lang, body FROM product_assets
		 WHERE tenant_id=? AND product_id=? AND visibility = 'public' AND validity=?
		 ORDER BY id`, tid, productID, AssetValidityValid)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var it PublicCorpusItem
		it.Source = "asset"
		if e := arows.Scan(&it.ID, &it.Kind, &it.Lang, &it.Text); e != nil {
			return nil, e
		}
		out = append(out, it)
	}
	return out, arows.Err()
}

// CountGatedFacts 某产品里受控（visibility='gated'）事实项的**条数**。
//
// ★ 只出计数、绝不出值：对外页要摆"这部分需登录或留言获取"的替身文案（§5.6），
//
//	而替身文案要成立就得知道"有没有"——把这句话写成"把值取出来再数一遍"就是 V4 的反面。
//
// 参数：tid=租户 ID，productID=产品主键；返回条数与错误。
func (s *Store) CountGatedFacts(tid int64, productID string) (int, error) {
	if err := s.productExistsScoped(tid, productID); err != nil {
		return 0, err
	}
	var n int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(1) FROM product_facts WHERE tenant_id=? AND product_id=? AND visibility=?",
		tid, productID, VisibilityGated).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}
