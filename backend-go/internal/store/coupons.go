// ============ coupons.go · 职责说明 ============
// store 包「优惠券/促销码」（★ #41 商业洞三，2026-09-21 实装）数据层。
//
// 为什么需要它：#41 评估报告指出「首月半价」是写死在 PackageOrderPrice 里的硬编码促销，
// 既不能按活动期投放、也不能定向发放、更没有核销流水。本文件把促销能力做成通用资产：
//   - coupons             券模板（码 / 适用单类 / 折扣口径 / 门槛 / 配额 / 有效期）
//   - coupon_redemptions  核销流水（一单一券，含折前、优惠额与折后，活动复盘与对账的唯一依据）
//
// 折扣落点（关键口径）：只在**订单落库后、渠道出码前**改写 orders.amount_money。
//   - 应收单一事实源不变：支付回调仍按 amount_money×100 核对（handlePayNotify 的 B1 口径），
//     退款与开票同样读该字段，各处不必再判断「有没有用券」；
//   - 内部计量（amount_tokens，决定发放额度）绝不受折扣影响——券减的是钱不是货，
//     否则「用钱打折」会变成「客户买到的积分也打折」的双重让利。
//
// 并发与配额：核销走单事务，PG 下先 FOR UPDATE 锁券行，再用带守卫的
//
//	UPDATE ... WHERE max_uses=0 OR used_count<max_uses 递增，RowsAffected=0 即判「券已抢完」，
//	与 quota_grants 扣减同款双保险，杜绝「两人同时用掉同一张限量券」。
//
// 挂单释放：pending 单超时关单后券不该被永久吃掉——ReleaseStaleCouponRedemptions 把
//
//	已取消订单的核销流水退回（used_count 一并减回），由后台扫描任务周期调用。
//
// =============================================
package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
)

// 券适用单类与折扣口径常量（禁止散落字符串，api 层共用）。
const (
	CouponKindRecharge  = "recharge"  // 充值单（/api/pay/create）
	CouponKindSubscribe = "subscribe" // 订阅/升级单（含 package_id）
	CouponKindAny       = "any"       // 两类都可用

	CouponTypePercent = "percent" // 按比例折让（value=折让百分比，20 表示减 20%）
	CouponTypeAmount  = "amount"  // 立减（value=立减金额，元）
)

// minOrderMoneyFen 折后应付下限（分）：宁可少减也不出 0 元单——0 元单无法走微信/支付宝出码，
// 还会让回调金额核对退化成 expect=0 的恒真判断，属支付安全面红线。
const minOrderMoneyFen = 1

// CouponError 券业务错误：Code 是稳定错误码（前端可按码做差异化交互），Message 是给用户看的
// 中文提示——二者都不含内部细节，符合 #37 脱敏口径（系统故障与业务提示分流，不再拼裸 err）。
type CouponError struct {
	Code    string // 错误码，见下方 CouponErr* 常量
	Message string // 用户可读提示
}

// Error 实现 error 接口（日志侧带码，便于按码检索）。
func (e *CouponError) Error() string { return e.Code + ": " + e.Message }

// Unwrap 让 errors.Is(err, ErrCouponInvalid) 恒成立：
// 调用方只需判「是不是券不可用」这一件事，无需列举每个码。
func (e *CouponError) Unwrap() error { return ErrCouponInvalid }

// ErrCouponInvalid 券不可用的统一哨兵（api 层据此区分「用户可纠正」的业务提示与系统故障）。
var ErrCouponInvalid = errors.New("coupon_invalid")

// 券错误码（稳定契约，新增只增不改语义）
const (
	CouponErrNotFound    = "coupon_not_found"     // 券码不存在
	CouponErrDisabled    = "coupon_disabled"      // 已停用
	CouponErrNotStarted  = "coupon_not_started"   // 未到生效时间
	CouponErrExpired     = "coupon_expired"       // 已过期
	CouponErrKind        = "coupon_kind_mismatch" // 适用单类不符
	CouponErrMinAmount   = "coupon_min_amount"    // 未达门槛
	CouponErrNoDiscount  = "coupon_no_discount"   // 本单无可优惠金额
	CouponErrTenantLimit = "coupon_tenant_limit"  // 本租户次数用尽
	CouponErrSoldOut     = "coupon_sold_out"      // 总量抢完
)

// Coupon 券模板。金额字段一律以「元」为口径（与 orders.amount_money 同单位），
// 时间字段存 RFC3339（与全站一致，空串=不限）。
type Coupon struct {
	ID             int64   `json:"id"`
	Code           string  `json:"code"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`             // recharge / subscribe / any
	DiscountType   string  `json:"discount_type"`    // percent / amount
	DiscountValue  float64 `json:"discount_value"`   // 折让百分比 或 立减金额（元）
	MaxDiscount    float64 `json:"max_discount"`     // 折让上限（元，0=不限，仅 percent 有意义）
	MinAmount      float64 `json:"min_amount"`       // 订单原价门槛（元，0=无门槛）
	MaxUses        int64   `json:"max_uses"`         // 总核销次数上限（0=不限）
	UsedCount      int64   `json:"used_count"`       // 已核销次数（挂单占用，关单后自动回退）
	PerTenantLimit int64   `json:"per_tenant_limit"` // 每租户可用次数（0=不限）
	ValidFrom      string  `json:"valid_from"`       // 生效时间（RFC3339，空=立即）
	ValidUntil     string  `json:"valid_until"`      // 失效时间（RFC3339，空=不限）
	Enabled        int     `json:"enabled"`          // 1=启用
	Note           string  `json:"note"`             // 备注（活动名/投放渠道）
	CreatedBy      int64   `json:"created_by"`       // 建券超管
	CreatedAt      string  `json:"created_at"`       // 创建时间
	UpdatedAt      string  `json:"updated_at"`       // 更新时间
}

// CouponRedemption 核销流水（一单一券，uniq_cr_order 唯一索引兜底）。
type CouponRedemption struct {
	ID            int64   `json:"id"`
	CouponID      int64   `json:"coupon_id"`
	Code          string  `json:"code"`
	TenantID      int64   `json:"tenant_id"`
	OrderID       int64   `json:"order_id"`
	OrderNo       string  `json:"order_no"`
	OriginMoney   float64 `json:"origin_money"`   // 折前应付（元）
	DiscountMoney float64 `json:"discount_money"` // 实减金额（元）
	PaidMoney     float64 `json:"paid_money"`     // 折后应付（元）
	CreatedAt     string  `json:"created_at"`
}

// NormalizeCouponCode 券码归一：去空白 + 转大写（用户输入与库内比对同一口径）。
func NormalizeCouponCode(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// couponColumns 券查询列（扫描函数与 SELECT 共用一份列序，禁止两处各写一遍）。
const couponColumns = `id,code,name,kind,discount_type,discount_value,max_discount,min_amount,
	max_uses,used_count,per_tenant_limit,valid_from,valid_until,enabled,note,created_by,created_at,updated_at`

// CouponMigrate 幂等建表 + 老库补列（随 Store.New 调用）。
// orders 新增 coupon_code / discount_money：订单列表与收银台要展示「用了哪张券、省了多少」，
// 退款时须能区分「实付」与「挂牌价」，不依赖 join 核销表（历史流水可能因清理查不到）。
func (s *Store) CouponMigrate() {
	d := db.CurrentDialect()
	if _, err := db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS coupons (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT 'any',
		discount_type TEXT NOT NULL DEFAULT 'percent',
		discount_value REAL NOT NULL DEFAULT 0,
		max_discount REAL NOT NULL DEFAULT 0,
		min_amount REAL NOT NULL DEFAULT 0,
		max_uses INTEGER NOT NULL DEFAULT 0,
		used_count INTEGER NOT NULL DEFAULT 0,
		per_tenant_limit INTEGER NOT NULL DEFAULT 0,
		valid_from TEXT NOT NULL DEFAULT '',
		valid_until TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		note TEXT NOT NULL DEFAULT '',
		created_by INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT '')`); err != nil {
		observability.Error(context.Background(), "优惠券表建表失败", "err", err.Error())
	}
	// 券码唯一（大小写已由 NormalizeCouponCode 归一）；存量重复码会让建索引失败，出声不阻断启动
	if _, err := db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS uniq_coupons_code ON coupons(code)`); err != nil {
		observability.Warn(context.Background(), "优惠券码唯一索引建立失败（存量重复码需人工清理）", "err", err.Error())
	}
	if _, err := db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS coupon_redemptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		coupon_id INTEGER NOT NULL DEFAULT 0,
		code TEXT NOT NULL DEFAULT '',
		tenant_id INTEGER NOT NULL DEFAULT 0,
		order_id INTEGER NOT NULL DEFAULT 0,
		order_no TEXT NOT NULL DEFAULT '',
		origin_money REAL NOT NULL DEFAULT 0,
		discount_money REAL NOT NULL DEFAULT 0,
		paid_money REAL NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT '')`); err != nil {
		observability.Error(context.Background(), "优惠券核销表建表失败", "err", err.Error())
	}
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS uniq_cr_order ON coupon_redemptions(order_id)`)
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_cr_coupon_tenant ON coupon_redemptions(coupon_id,tenant_id)`)
	if err := db.EnsureColumns(s.db, d, "orders", map[string]string{
		"coupon_code":    "TEXT NOT NULL DEFAULT ''",
		"discount_money": "REAL NOT NULL DEFAULT 0",
	}); err != nil {
		observability.Error(context.Background(), "orders 优惠券列补齐失败", "err", err.Error())
	}
}

// CreateCoupon 新建券（券码归一后查重，重复即拒绝）；返回落库后的完整行。
func (s *Store) CreateCoupon(c *Coupon) (*Coupon, error) {
	if c == nil {
		return nil, errors.New("券信息为空")
	}
	c.Code = NormalizeCouponCode(c.Code)
	if c.Code == "" {
		return nil, errors.New("券码不能为空")
	}
	// 适用单类留空按「两类都可用」落库（与建表 DEFAULT 'any' 同口径；折扣类型不给默认，
	// 因为 percent/amount 语义差别太大，必须由上层显式选定，防「20」被当成 20 元或 20% 的误配）
	if c.Kind == "" {
		c.Kind = CouponKindAny
	}
	if verr := couponRuleError(c); verr != nil {
		return nil, verr
	}
	if _, err := s.GetCouponByCode(c.Code); err == nil {
		return nil, errors.New("该券码已存在")
	}
	d := db.CurrentDialect()
	now := time.Now().UTC().Format(time.RFC3339)
	// 列清单不含 id（自增）与 used_count（只能由核销/回退链路变动），与占位符一一对应
	// ★ PG 方言：lib/pq 不支持 LastInsertId（恒返 0），自增 ID 必须走 db.InsertID（RETURNING）
	id, err := db.InsertID(s.db, d, "id", `INSERT INTO coupons
		(code,name,kind,discount_type,discount_value,max_discount,min_amount,max_uses,
		 per_tenant_limit,valid_from,valid_until,enabled,note,created_by,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Code, c.Name, c.Kind, c.DiscountType, c.DiscountValue, c.MaxDiscount, c.MinAmount, c.MaxUses,
		c.PerTenantLimit, c.ValidFrom, c.ValidUntil, c.Enabled, c.Note, c.CreatedBy, now, now)
	if err != nil {
		return nil, err
	}
	return s.GetCouponByID(id)
}

// UpdateCoupon 更新券规则。券码与 used_count 不在更新列内：
// 前者是核销流水的冗余快照（改码会让历史对账错位），后者只能由核销/回退链路变动。
func (s *Store) UpdateCoupon(c *Coupon) error {
	if c == nil || c.ID <= 0 {
		return errors.New("缺少券 ID")
	}
	if verr := couponRuleError(c); verr != nil {
		return verr
	}
	res, err := db.Exec(s.db, db.CurrentDialect(), `UPDATE coupons SET name=?,kind=?,discount_type=?,discount_value=?,
		max_discount=?,min_amount=?,max_uses=?,per_tenant_limit=?,valid_from=?,valid_until=?,enabled=?,note=?,updated_at=?
		WHERE id=?`,
		c.Name, c.Kind, c.DiscountType, c.DiscountValue, c.MaxDiscount, c.MinAmount, c.MaxUses, c.PerTenantLimit,
		c.ValidFrom, c.ValidUntil, c.Enabled, c.Note, time.Now().UTC().Format(time.RFC3339), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("券不存在或已被删除")
	}
	return nil
}

// DeleteCoupon 删券模板（核销流水保留，活动复盘仍可查历史发放）。
// 已核销过的券更建议 enabled=0 停用而非删除，便于逐条核对。
func (s *Store) DeleteCoupon(id int64) error {
	res, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM coupons WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("券不存在或已被删除")
	}
	return nil
}

// ListCoupons 券列表（含停用）。used_count 以流水实时回补——
// 计数列与流水表理论上恒等，但任何异常中断都可能漂移，展示口径取「事实源」流水。
func (s *Store) ListCoupons() []*Coupon {
	d := db.CurrentDialect()
	rows, err := db.Query(s.db, d, "SELECT "+couponColumns+" FROM coupons ORDER BY id DESC")
	if err != nil {
		observability.Error(context.Background(), "优惠券列表查询失败", "err", err.Error())
		return nil
	}
	defer rows.Close()
	out := []*Coupon{}
	for rows.Next() {
		c := &Coupon{}
		if err := c.scanFrom(rows); err != nil {
			continue
		}
		out = append(out, c)
	}
	for _, c := range out {
		var real int64
		_ = db.QueryRow(s.db, d, "SELECT COUNT(1) FROM coupon_redemptions WHERE coupon_id=?", c.ID).Scan(&real)
		c.UsedCount = real
	}
	return out
}

// GetCouponByCode 按归一化券码取券（不存在返回 sql.ErrNoRows）。
func (s *Store) GetCouponByCode(code string) (*Coupon, error) {
	return s.scanCoupon(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+couponColumns+" FROM coupons WHERE code=?", NormalizeCouponCode(code)))
}

// GetCouponByID 按主键取券。
func (s *Store) GetCouponByID(id int64) (*Coupon, error) {
	return s.scanCoupon(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+couponColumns+" FROM coupons WHERE id=?", id))
}

// rowScanner 最小扫描接口（*sql.Row 与 *sql.Rows 共用同一列序扫描，避免两份实现漂移）。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanCoupon 单行扫描 → Coupon。
func (s *Store) scanCoupon(row rowScanner) (*Coupon, error) {
	c := &Coupon{}
	if err := c.scanFrom(row); err != nil {
		return nil, err
	}
	return c, nil
}

// scanFrom 列序见 couponColumns（新增列必须三处同步：建表 DDL / couponColumns / 本函数）。
func (c *Coupon) scanFrom(row rowScanner) error {
	return row.Scan(&c.ID, &c.Code, &c.Name, &c.Kind, &c.DiscountType, &c.DiscountValue, &c.MaxDiscount,
		&c.MinAmount, &c.MaxUses, &c.UsedCount, &c.PerTenantLimit, &c.ValidFrom, &c.ValidUntil, &c.Enabled,
		&c.Note, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
}

// couponRuleError 券规则校验（新建与更新同一口径；返回文案可直接回给超管）。
func couponRuleError(c *Coupon) error {
	if c.DiscountType != CouponTypePercent && c.DiscountType != CouponTypeAmount {
		return errors.New("折扣类型仅支持 percent / amount")
	}
	if c.Kind != CouponKindAny && c.Kind != CouponKindRecharge && c.Kind != CouponKindSubscribe {
		return errors.New("适用单类仅支持 recharge / subscribe / any")
	}
	if c.DiscountValue <= 0 {
		return errors.New("折扣值必须大于 0")
	}
	if c.DiscountType == CouponTypePercent && c.DiscountValue > 100 {
		return errors.New("比例折扣不能超过 100%")
	}
	if c.MaxDiscount < 0 || c.MinAmount < 0 || c.MaxUses < 0 || c.PerTenantLimit < 0 {
		return errors.New("上限类字段不能为负")
	}
	for _, v := range []string{c.ValidFrom, c.ValidUntil} {
		if v == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return errors.New("有效期格式必须为 RFC3339")
		}
	}
	return nil
}

// PreviewCouponDiscount 试算券对本单的折让（不落库；收银台预览与核销共用同一算法）。
// 参数 code=券码，kind=本单类型（recharge/subscribe），originMoney=折前应付（元），
// tid=当前租户（>0 时顺带判「本租户次数上限」，0=不判，如公开定价页试算）。
// 返回 (券, 折让额, 错误)：错误带 ErrCouponInvalid 包装，api 层可原样回给用户。
// ★ 预览必须与核销同口径：否则收银台显示「可用」、下单却报错，是促销最常见的差评来源。
func (s *Store) PreviewCouponDiscount(code, kind string, originMoney float64, tid int64) (*Coupon, float64, error) {
	c, err := s.GetCouponByCode(code)
	if err != nil {
		return nil, 0, &CouponError{Code: CouponErrNotFound, Message: "券码不存在"}
	}
	if uerr := couponUsable(c, kind, originMoney); uerr != nil {
		return nil, 0, uerr
	}
	if terr := s.couponTenantErr(c, tid); terr != nil {
		return nil, 0, terr
	}
	return c, couponDiscountOf(c, originMoney), nil
}

// couponTenantErr 本租户次数上限判定（tid<=0 时跳过）。核销事务里还会在行锁下再判一次，
// 这里只是让预览与实付同口径，不构成配额保证。
func (s *Store) couponTenantErr(c *Coupon, tid int64) error {
	if c.PerTenantLimit <= 0 || tid <= 0 {
		return nil
	}
	var used int64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(1) FROM coupon_redemptions WHERE coupon_id=? AND tenant_id=?", c.ID, tid).Scan(&used); err != nil {
		return nil // 计数查询失败不拦预览（核销链路会真判），避免预览面被 DB 抖动放大成活动故障
	}
	if used >= c.PerTenantLimit {
		return &CouponError{Code: CouponErrTenantLimit, Message: "该券在本企业的可用次数已用完"}
	}
	return nil
}

// couponUsable 静态可用性判定（不含配额竞争——配额在核销事务里带守卫再判一次）。
func couponUsable(c *Coupon, kind string, originMoney float64) error {
	if c.Enabled != 1 {
		return &CouponError{Code: CouponErrDisabled, Message: "该券已停用"}
	}
	// 总配额快照判定（真正确认在核销事务的守卫 UPDATE 里）：让「已抢完」在收银台就报出来
	if c.MaxUses > 0 && c.UsedCount >= c.MaxUses {
		return &CouponError{Code: CouponErrSoldOut, Message: "该券已抢完"}
	}
	now := time.Now().UTC()
	if c.ValidFrom != "" {
		if vt, err := time.Parse(time.RFC3339, c.ValidFrom); err == nil && now.Before(vt) {
			return &CouponError{Code: CouponErrNotStarted, Message: "该券尚未生效"}
		}
	}
	if c.ValidUntil != "" {
		if vt, err := time.Parse(time.RFC3339, c.ValidUntil); err == nil && !now.Before(vt) {
			return &CouponError{Code: CouponErrExpired, Message: "该券已过期"}
		}
	}
	if c.Kind != CouponKindAny && c.Kind != kind {
		return &CouponError{Code: CouponErrKind, Message: "该券不适用于本单类型"}
	}
	if c.MinAmount > 0 && originMoney+1e-9 < c.MinAmount {
		return &CouponError{Code: CouponErrMinAmount, Message: fmt.Sprintf("订单需满 %.2f 元才可用", c.MinAmount)}
	}
	if couponDiscountOf(c, originMoney) <= 0 {
		return &CouponError{Code: CouponErrNoDiscount, Message: "本单无可优惠金额"}
	}
	return nil
}

// couponDiscountOf 折让额（元）：比例券受 max_discount 封顶、立减券不超过券面额，
// 并且任何情况下都至少留下 minOrderMoneyFen 的实付。向下取整到分（多减一分钱就可能压到 0 元）。
func couponDiscountOf(c *Coupon, originMoney float64) float64 {
	if originMoney <= 0 {
		return 0
	}
	raw := originMoney
	if c.DiscountType == CouponTypePercent {
		raw = originMoney * c.DiscountValue / 100
		if c.MaxDiscount > 0 && raw > c.MaxDiscount {
			raw = c.MaxDiscount
		}
	} else if raw > c.DiscountValue {
		raw = c.DiscountValue
	}
	fen := int64(raw*100 + 1e-6)
	if maxFen := int64(originMoney*100+0.5) - minOrderMoneyFen; fen > maxFen {
		fen = maxFen
	}
	if fen < 0 {
		fen = 0
	}
	return float64(fen) / 100
}

// ApplyCouponToOrder 核销券并改写订单应付（★ 单事务，一单一券）。
// 参数 orderID=订单 ID，tid=租户 ID（必须与订单归属一致，防跨租户套用别人的券），
// code=券码，kind=订单类型；返回 (折前应付, 折让额, 折后应付, 错误)。
// 任何一步失败整体回滚：券不计数、订单金额不变。
func (s *Store) ApplyCouponToOrder(orderID, tid int64, code, kind string) (float64, float64, float64, error) {
	d := db.CurrentDialect()
	code = NormalizeCouponCode(code)
	if code == "" {
		return 0, 0, 0, errors.New("券码不能为空")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()

	var originMoney float64
	var oTID int64
	var oStatus, hadCoupon, orderNo string
	// ★ 事务内一律走 db.QueryRow(tx, d, ...)：PG 占位符需 $n，裸 tx.QueryRow 会把「?」原样发给 lib/pq
	if serr := db.QueryRow(tx, d, "SELECT amount_money, tenant_id, status, coupon_code, order_no FROM orders WHERE id=?", orderID).
		Scan(&originMoney, &oTID, &oStatus, &hadCoupon, &orderNo); serr != nil {
		return 0, 0, 0, errors.New("订单不存在")
	}
	if oTID != tid {
		return 0, 0, 0, errors.New("订单归属校验失败")
	}
	if oStatus != "pending" {
		return 0, 0, 0, errors.New("仅待支付订单可使用优惠券")
	}
	if hadCoupon != "" {
		return 0, 0, 0, errors.New("该订单已使用过优惠券")
	}
	// 锁券行（PG 行锁；SQLite 由 DSN _txlock=immediate 在事务级串行化）
	lock := ""
	if d.IsPostgres() {
		lock = " FOR UPDATE"
	}
	c := &Coupon{}
	if serr := c.scanFrom(db.QueryRow(tx, d, "SELECT "+couponColumns+" FROM coupons WHERE code=?"+lock, code)); serr != nil {
		return 0, 0, 0, &CouponError{Code: CouponErrNotFound, Message: "券码不存在"}
	}
	if uerr := couponUsable(c, kind, originMoney); uerr != nil {
		return 0, 0, 0, uerr
	}
	if c.PerTenantLimit > 0 {
		var used int64
		if qerr := db.QueryRow(tx, d, "SELECT COUNT(1) FROM coupon_redemptions WHERE coupon_id=? AND tenant_id=?", c.ID, tid).Scan(&used); qerr != nil {
			return 0, 0, 0, qerr
		}
		if used >= c.PerTenantLimit {
			return 0, 0, 0, &CouponError{Code: CouponErrTenantLimit, Message: "该券在本企业的可用次数已用完"}
		}
	}
	discount := couponDiscountOf(c, originMoney)
	if discount <= 0 {
		return 0, 0, 0, &CouponError{Code: CouponErrNoDiscount, Message: "本单无可优惠金额"}
	}
	// 总配额：带守卫递增，RowsAffected=0 即已被抢完（不信任上面读到的 used_count 快照）
	res, uerr := db.Exec(tx, d, `UPDATE coupons SET used_count=used_count+1, updated_at=?
		WHERE id=? AND (max_uses=0 OR used_count<max_uses)`,
		time.Now().UTC().Format(time.RFC3339), c.ID)
	if uerr != nil {
		return 0, 0, 0, uerr
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, 0, 0, &CouponError{Code: CouponErrSoldOut, Message: "该券已抢完"}
	}
	paid := roundMoney(originMoney - discount)
	// 改写订单应付（同一守卫：仍 pending 且未用过券，防并发改单）
	ares, aerr := db.Exec(tx, d, `UPDATE orders SET amount_money=?, coupon_code=?, discount_money=?
		WHERE id=? AND status='pending' AND coupon_code=''`, paid, c.Code, discount, orderID)
	if aerr != nil {
		return 0, 0, 0, aerr
	}
	if n, _ := ares.RowsAffected(); n == 0 {
		return 0, 0, 0, errors.New("订单状态已变化，请刷新后重试")
	}
	if _, ierr := db.Exec(tx, d, `INSERT INTO coupon_redemptions
		(coupon_id,code,tenant_id,order_id,order_no,origin_money,discount_money,paid_money,created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Code, tid, orderID, orderNo, originMoney, discount, paid, time.Now().UTC().Format(time.RFC3339)); ierr != nil {
		return 0, 0, 0, ierr
	}
	if cerr := tx.Commit(); cerr != nil {
		return 0, 0, 0, cerr
	}
	return originMoney, discount, paid, nil
}

// roundMoney 金额四舍五入到分（全站订单金额同一口径，防浮点尾差进对账链）。
func roundMoney(v float64) float64 {
	if v < 0 {
		v = 0
	}
	return float64(int64(v*100+0.5)) / 100
}

// ReleaseStaleCouponRedemptions 回退「订单已取消/关闭但券仍被占用」的核销（扫描任务调用）。
// 返回回退条数；paid 单永不回退（那是真实成交的券）。
func (s *Store) ReleaseStaleCouponRedemptions() int64 {
	rows, err := db.Query(s.db, db.CurrentDialect(), `SELECT r.id, r.coupon_id FROM coupon_redemptions r
		JOIN orders o ON o.id=r.order_id WHERE o.status='cancelled'`)
	if err != nil {
		return 0
	}
	type ref struct{ rid, cid int64 }
	var list []ref
	for rows.Next() {
		var it ref
		if serr := rows.Scan(&it.rid, &it.cid); serr == nil {
			list = append(list, it)
		}
	}
	rows.Close()
	var n int64
	for _, it := range list {
		if err := s.releaseCouponRedemption(it.rid, it.cid); err != nil {
			observability.Warn(context.Background(), "优惠券核销回退失败",
				"redemption_id", strconv.FormatInt(it.rid, 10), "err", err.Error())
			continue
		}
		n++
	}
	return n
}

// releaseCouponRedemption 单条回退：删流水 + 计数减回（守卫 used_count>0，防减成负数）。
func (s *Store) releaseCouponRedemption(rid, cid int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d := db.CurrentDialect()
	res, err := db.Exec(tx, d, "DELETE FROM coupon_redemptions WHERE id=?", rid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // 已被并发回退，视为成功
	}
	if _, uerr := db.Exec(tx, d, "UPDATE coupons SET used_count=used_count-1 WHERE id=? AND used_count>0", cid); uerr != nil {
		return uerr
	}
	return tx.Commit()
}

// ListCouponRedemptions 核销流水（超管活动复盘；couponID=0 表示全部，limit 上限 500）。
func (s *Store) ListCouponRedemptions(couponID int64, limit int) []CouponRedemption {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	d := db.CurrentDialect()
	q := `SELECT id,coupon_id,code,tenant_id,order_id,order_no,origin_money,discount_money,paid_money,created_at
		FROM coupon_redemptions`
	args := []any{}
	if couponID > 0 {
		q += " WHERE coupon_id=?"
		args = append(args, couponID)
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.Query(s.db, d, q, args...)
	if err != nil {
		observability.Error(context.Background(), "优惠券核销流水查询失败", "err", err.Error())
		return nil
	}
	defer rows.Close()
	out := []CouponRedemption{}
	for rows.Next() {
		var r CouponRedemption
		if serr := rows.Scan(&r.ID, &r.CouponID, &r.Code, &r.TenantID, &r.OrderID, &r.OrderNo,
			&r.OriginMoney, &r.DiscountMoney, &r.PaidMoney, &r.CreatedAt); serr != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// CouponStats 券核销汇总（列表页展示与活动复盘）。
type CouponStats struct {
	TotalDiscount float64 `json:"total_discount"` // 累计折让（元）
	PaidTotal     float64 `json:"paid_total"`     // 累计实付（元）
	Used          int64   `json:"used"`           // 核销张数
}

// GetCouponStats 单券汇总（无流水返回零值）。
func (s *Store) GetCouponStats(couponID int64) CouponStats {
	var st CouponStats
	_ = db.QueryRow(s.db, db.CurrentDialect(), `SELECT COALESCE(SUM(discount_money),0), COALESCE(SUM(paid_money),0), COUNT(1)
		FROM coupon_redemptions WHERE coupon_id=?`, couponID).Scan(&st.TotalDiscount, &st.PaidTotal, &st.Used)
	return st
}
