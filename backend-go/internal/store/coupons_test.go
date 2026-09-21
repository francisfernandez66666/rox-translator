// ============ coupons_test.go · 职责说明 ============
// store 层「优惠券/促销码」（★ #41 商业洞三，2026-09-21 实装）自动化断言。
// 钉死五条产品口径（任一条被后续改动悄悄破坏即红灯）：
//
//	① 折扣只改钱不改货：orders.amount_money 改写为折后实付，amount_tokens（发放额度）不变；
//	② 折让算法边界：比例券受 max_discount 封顶、立减券不超面额、向下取整到分、实付恒 ≥1 分；
//	③ 适用性校验：券码/停用/未生效/已过期/单类不符/未达门槛/本单无可优惠金额；
//	④ 配额：总配额抢完、每租户上限、一单一券（uniq_cr_order）、跨租户套用被拒；
//	⑤ 挂单回收：cancelled 单的核销流水与 used_count 一并回退，paid 单永不回退。
//
// 方言：本文件固定 SQLite 内存库并显式钉死 config.C（AGENTS.md §4——run_uat 的 PG 模式
// 会把 env DB_DRIVER=postgres 的方言泄漏给同包内存库用例）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run Coupon
// =============================================
package store

import (
	"math"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/db"
)

// couponEnv 建券测试环境：钉方言 + 租户 1 + 一个下单用户。
func couponEnv(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	st := newTestStoreWithTenants(t)
	if _, err := st.CreateUser(1, "coupon_user", "hash", "券测试员", RoleUser, 0, 0); err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	return st
}

// mustCreateCoupon 建券（失败即 t.Fatal，省去每个用例重复三段）。
func mustCreateCoupon(t *testing.T, st *Store, c *Coupon) *Coupon {
	t.Helper()
	got, err := st.CreateCoupon(c)
	if err != nil {
		t.Fatalf("建券 %s 失败: %v", c.Code, err)
	}
	return got
}

// mustCouponCode 建券并回归一化券码（用例里只想引用码时用，避免拿着 *Coupon 不用）。
func mustCouponCode(t *testing.T, st *Store, c *Coupon) string {
	t.Helper()
	return mustCreateCoupon(t, st, c).Code
}

// newRechargeOrder 造一张充值 pending 单（money=折前应付元）。
func newRechargeOrder(t *testing.T, st *Store, tid int64, money float64) *Order {
	t.Helper()
	o, err := st.CreateOrderChannel(tid, st.TokensFromPoints(1000), money, 1, "mock", "")
	if err != nil {
		t.Fatalf("建充值单失败: %v", err)
	}
	return o
}

// couponErrCode 取 CouponError 的错误码（非券业务错误返回空串）。
func couponErrCode(t *testing.T, err error) string {
	t.Helper()
	var ce *CouponError
	if !asCouponError(err, &ce) {
		return ""
	}
	return ce.Code
}

// asCouponError 手写 errors.As 包装（测试里避免再引 errors 包的一处空行）。
func asCouponError(err error, target **CouponError) bool {
	for err != nil {
		if ce, ok := err.(*CouponError); ok {
			*target = ce
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestCouponMigrateIdempotent 建表幂等 + orders 券列随迁移补齐。
func TestCouponMigrateIdempotent(t *testing.T) {
	st := couponEnv(t)
	st.CouponMigrate() // New() 已调过，再调一次必须无害
	var n int
	if err := db.QueryRow(st.db, db.CurrentDialect(), "SELECT COUNT(1) FROM coupons").Scan(&n); err != nil {
		t.Fatalf("coupons 表不可用: %v", err)
	}
	if _, err := st.CreateCoupon(&Coupon{Code: "M1", DiscountType: CouponTypePercent, DiscountValue: 10, Kind: CouponKindAny, Enabled: 1}); err != nil {
		t.Fatalf("二次迁移后建券失败: %v", err)
	}
	// orders 补列必须存在（收银台/对账读这两列）
	o := newRechargeOrder(t, st, 1, 99)
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		"UPDATE orders SET coupon_code=?, discount_money=? WHERE id=?", "M1", 9.9, o.ID); err != nil {
		t.Fatalf("orders 券列缺失: %v", err)
	}
}

// TestCouponCreateValidation 建券校验：空码/重复码/非法折扣类型与值/负数上限。
func TestCouponCreateValidation(t *testing.T) {
	st := couponEnv(t)
	if _, err := st.CreateCoupon(&Coupon{Code: "  ", DiscountType: CouponTypePercent, DiscountValue: 10, Enabled: 1}); err == nil {
		t.Error("空白券码应被拒绝")
	}
	first := mustCreateCoupon(t, st, &Coupon{Code: "promo20", Name: "首月促销", DiscountType: CouponTypePercent, DiscountValue: 20, Kind: CouponKindAny, Enabled: 1})
	if first.Code != "PROMO20" {
		t.Errorf("券码应归一为大写，实际 %s", first.Code)
	}
	if _, err := st.CreateCoupon(&Coupon{Code: " promo20 ", DiscountType: CouponTypePercent, DiscountValue: 5, Enabled: 1}); err == nil {
		t.Error("重复券码应被拒绝")
	}
	for i, bad := range []*Coupon{
		{Code: "B1", DiscountType: "half", DiscountValue: 10},
		{Code: "B2", DiscountType: CouponTypePercent, DiscountValue: 0},
		{Code: "B3", DiscountType: CouponTypePercent, DiscountValue: 120},
		{Code: "B4", DiscountType: CouponTypeAmount, DiscountValue: 10, MaxUses: -1},
		{Code: "B5", DiscountType: CouponTypeAmount, DiscountValue: 10, Kind: "both"},
		{Code: "B6", DiscountType: CouponTypeAmount, DiscountValue: 10, ValidUntil: "2026-09-21"},
	} {
		if _, err := st.CreateCoupon(bad); err == nil {
			t.Errorf("非法券 %d(%+v) 应被拒绝", i, bad)
		}
	}
	// 更新不得改写券码与 used_count
	if err := st.UpdateCoupon(&Coupon{ID: first.ID, Name: "改名", Kind: CouponKindAny,
		DiscountType: CouponTypePercent, DiscountValue: 30, Enabled: 1}); err != nil {
		t.Fatalf("更新券失败: %v", err)
	}
	got, err := st.GetCouponByID(first.ID)
	if err != nil {
		t.Fatalf("取券失败: %v", err)
	}
	if got.Code != "PROMO20" || got.Name != "改名" || got.DiscountValue != 30 {
		t.Errorf("更新结果不符: %+v", got)
	}
	if err := st.UpdateCoupon(&Coupon{ID: 999999, DiscountType: CouponTypePercent, DiscountValue: 10, Enabled: 1}); err == nil {
		t.Error("更新不存在的券应报错")
	}
}

// TestCouponDiscountBoundary 折让边界：比例封顶、立减不超面额、实付恒 ≥1 分、预览与核销同算法。
func TestCouponDiscountBoundary(t *testing.T) {
	st := couponEnv(t)
	cases := []struct {
		name     string
		coupon   *Coupon
		origin   float64
		wantDisc float64
		wantPaid float64
	}{
		{"比例 20%", &Coupon{Code: "D1", DiscountType: CouponTypePercent, DiscountValue: 20, Enabled: 1}, 100, 20, 80},
		{"比例 20% 封顶 10 元", &Coupon{Code: "D2", DiscountType: CouponTypePercent, DiscountValue: 20, MaxDiscount: 10, Enabled: 1}, 100, 10, 90},
		{"立减 30 元", &Coupon{Code: "D3", DiscountType: CouponTypeAmount, DiscountValue: 30, Enabled: 1}, 100, 30, 70},
		{"立减不超面额", &Coupon{Code: "D4", DiscountType: CouponTypeAmount, DiscountValue: 5, Enabled: 1}, 3.33, 3.32, 0.01},
		{"全额立减留 1 分", &Coupon{Code: "D5", DiscountType: CouponTypeAmount, DiscountValue: 999, Enabled: 1}, 50, 49.99, 0.01},
		{"分位向下取整", &Coupon{Code: "D6", DiscountType: CouponTypePercent, DiscountValue: 15, Enabled: 1}, 99.99, 14.99, 85},
	}
	for _, tc := range cases {
		cp := mustCreateCoupon(t, st, tc.coupon)
		_, disc, err := st.PreviewCouponDiscount(cp.Code, cp.Kind, tc.origin, 1)
		if err != nil {
			t.Errorf("%s：预览失败 %v", tc.name, err)
			continue
		}
		if math.Abs(disc-tc.wantDisc) > 1e-9 {
			t.Errorf("%s：折让期望 %.2f 实际 %.2f", tc.name, tc.wantDisc, disc)
		}
		o := newRechargeOrder(t, st, 1, tc.origin)
		_, _, paid, aerr := st.ApplyCouponToOrder(o.ID, 1, cp.Code, CouponKindRecharge)
		if aerr != nil {
			t.Errorf("%s：核销失败 %v", tc.name, aerr)
			continue
		}
		if math.Abs(paid-tc.wantPaid) > 1e-9 {
			t.Errorf("%s：实付期望 %.2f 实际 %.2f", tc.name, tc.wantPaid, paid)
		}
	}
}

// TestCouponApplyRewritesMoneyOnly 核心口径：券只改钱不改货 + 流水落库 + 计数递增。
func TestCouponApplyRewritesMoneyOnly(t *testing.T) {
	st := couponEnv(t)
	cp := mustCreateCoupon(t, st, &Coupon{Code: "APPLY1", DiscountType: CouponTypePercent,
		DiscountValue: 25, Kind: CouponKindAny, Enabled: 1})
	o := newRechargeOrder(t, st, 1, 200)
	before := o.AmountTokens
	origin, disc, paid, err := st.ApplyCouponToOrder(o.ID, 1, "apply1", CouponKindRecharge)
	if err != nil {
		t.Fatalf("核销失败: %v", err)
	}
	if origin != 200 || disc != 50 || paid != 150 {
		t.Errorf("折前/折让/实付不符: %v %v %v", origin, disc, paid)
	}
	got, gerr := st.GetOrder(o.ID, 1)
	if gerr != nil {
		t.Fatalf("取单失败: %v", gerr)
	}
	if got.AmountMoney != 150 {
		t.Errorf("amount_money 应改写为折后实付 150，实际 %.2f", got.AmountMoney)
	}
	if got.AmountTokens != before {
		t.Errorf("amount_tokens 必须不变（券减钱不减货）：期望 %d 实际 %d", before, got.AmountTokens)
	}
	list := st.ListCouponRedemptions(cp.ID, 0)
	if len(list) != 1 {
		t.Fatalf("核销流水应 1 条，实际 %d", len(list))
	}
	r := list[0]
	if r.OrderNo != o.OrderNo || r.TenantID != 1 || r.DiscountMoney != 50 || r.PaidMoney != 150 || r.OriginMoney != 200 {
		t.Errorf("流水内容不符: %+v", r)
	}
	after, _ := st.GetCouponByID(cp.ID)
	if after.UsedCount != 1 {
		t.Errorf("used_count 应为 1，实际 %d", after.UsedCount)
	}
	// 列表口径以流水为准（漂移时展示事实源）
	if l := st.ListCoupons(); len(l) > 0 {
		for _, c := range l {
			if c.ID == cp.ID && c.UsedCount != 1 {
				t.Errorf("ListCoupons used_count 应以流水为准，实际 %d", c.UsedCount)
			}
		}
	}
}

// TestCouponApplyRejected 核销拒绝面：不存在/停用/未生效/已过期/单类不符/门槛不足/重复用券/非 pending/跨租户。
func TestCouponApplyRejected(t *testing.T) {
	st := couponEnv(t)
	now := time.Now().UTC()
	rfc := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }

	disabled := mustCouponCode(t, st, &Coupon{Code: "R-DIS", DiscountType: CouponTypeAmount, DiscountValue: 10, Enabled: 0})
	future := mustCouponCode(t, st, &Coupon{Code: "R-FUT", DiscountType: CouponTypeAmount, DiscountValue: 10, ValidFrom: rfc(24 * time.Hour), Enabled: 1})
	expired := mustCouponCode(t, st, &Coupon{Code: "R-EXP", DiscountType: CouponTypeAmount, DiscountValue: 10, ValidUntil: rfc(-24 * time.Hour), Enabled: 1})
	onlySub := mustCouponCode(t, st, &Coupon{Code: "R-SUB", DiscountType: CouponTypeAmount, DiscountValue: 10, Kind: CouponKindSubscribe, Enabled: 1})
	minHigh := mustCouponCode(t, st, &Coupon{Code: "R-MIN", DiscountType: CouponTypeAmount, DiscountValue: 10, MinAmount: 500, Enabled: 1})

	wants := []struct {
		name string
		code string
		kind string
		want string
	}{
		{"券码不存在", "NOPE", CouponKindRecharge, CouponErrNotFound},
		{"已停用", disabled, CouponKindRecharge, CouponErrDisabled},
		{"未到生效时间", future, CouponKindRecharge, CouponErrNotStarted},
		{"已过期", expired, CouponKindRecharge, CouponErrExpired},
		{"适用单类不符", onlySub, CouponKindRecharge, CouponErrKind},
		{"未达门槛", minHigh, CouponKindRecharge, CouponErrMinAmount},
	}
	for i, tc := range wants {
		o := newRechargeOrder(t, st, 1, 100)
		_, _, _, err := st.ApplyCouponToOrder(o.ID, 1, tc.code, tc.kind)
		if err == nil {
			t.Errorf("%s（第 %d 例）应被拒绝", tc.name, i)
			continue
		}
		if code := couponErrCode(t, err); code != tc.want {
			t.Errorf("%s：错误码期望 %s 实际 %s", tc.name, tc.want, code)
		}
		// 失败必须零副作用：订单金额与券计数都不动
		if got, _ := st.GetOrder(o.ID, 1); got.AmountMoney != 100 {
			t.Errorf("%s：失败后订单金额被改写为 %.2f", tc.name, got.AmountMoney)
		}
	}
	// 一单一券
	ok := mustCouponCode(t, st, &Coupon{Code: "R-OK", DiscountType: CouponTypeAmount, DiscountValue: 10, Enabled: 1})
	o := newRechargeOrder(t, st, 1, 100)
	if _, _, _, err := st.ApplyCouponToOrder(o.ID, 1, ok, CouponKindRecharge); err != nil {
		t.Fatalf("首次核销应成功: %v", err)
	}
	if _, _, _, err := st.ApplyCouponToOrder(o.ID, 1, ok, CouponKindRecharge); err == nil {
		t.Error("同一订单重复用券应被拒绝")
	}
	// 已支付单不可再用券
	o2 := newRechargeOrder(t, st, 1, 100)
	if err := st.UpdateOrderMoney(o2.OrderNo, 100); err != nil {
		t.Fatalf("回填金额失败: %v", err)
	}
	if merr := st.MarkOrderPaid(o2.ID, 1); merr != nil {
		t.Fatalf("模拟入账失败: %v", merr)
	}
	if _, _, _, err := st.ApplyCouponToOrder(o2.ID, 1, ok, CouponKindRecharge); err == nil {
		t.Error("非 pending 单用券应被拒绝")
	}
	// 跨租户套用（订单属租户 1，请求方 tid=2）
	o3 := newRechargeOrder(t, st, 1, 100)
	if _, _, _, err := st.ApplyCouponToOrder(o3.ID, 2, ok, CouponKindRecharge); err == nil {
		t.Error("跨租户套用订单应被拒绝")
	}
	if got, _ := st.GetOrder(o3.ID, 1); got.AmountMoney != 100 {
		t.Errorf("跨租户尝试后订单金额被改写: %.2f", got.AmountMoney)
	}
	// 空券码/不存在的券不污染流水
	if _, _, _, err := st.ApplyCouponToOrder(o3.ID, 1, "   ", CouponKindRecharge); err == nil {
		t.Error("空券码应直接报错")
	}
}

// TestCouponQuotaLimits 配额：每租户上限 + 总配额抢完（并发同一张限量券由守卫 UPDATE 保证不超发）。
func TestCouponQuotaLimits(t *testing.T) {
	st := couponEnv(t)
	perT := mustCouponCode(t, st, &Coupon{Code: "Q-PER", DiscountType: CouponTypeAmount,
		DiscountValue: 10, PerTenantLimit: 2, Enabled: 1})
	for i := 0; i < 2; i++ {
		o := newRechargeOrder(t, st, 1, 100)
		if _, _, _, err := st.ApplyCouponToOrder(o.ID, 1, perT, CouponKindRecharge); err != nil {
			t.Fatalf("第 %d 次核销应成功: %v", i+1, err)
		}
	}
	o := newRechargeOrder(t, st, 1, 100)
	_, _, _, err := st.ApplyCouponToOrder(o.ID, 1, perT, CouponKindRecharge)
	if code := couponErrCode(t, err); code != CouponErrTenantLimit {
		t.Errorf("超出每租户上限应报 %s，实际 err=%v", CouponErrTenantLimit, err)
	}
	// 其他租户不受该租户已达上限影响
	if _, _, _, err = st.ApplyCouponToOrder(newRechargeOrder(t, st, 2, 100).ID, 2, perT, CouponKindRecharge); err != nil {
		t.Errorf("租户 2 首次核销应成功: %v", err)
	}

	total := mustCreateCoupon(t, st, &Coupon{Code: "Q-TOT", DiscountType: CouponTypeAmount,
		DiscountValue: 10, MaxUses: 1, Enabled: 1})
	if _, _, _, err = st.ApplyCouponToOrder(newRechargeOrder(t, st, 3, 100).ID, 3, "Q-TOT", CouponKindRecharge); err != nil {
		t.Fatalf("总量 1 的首次核销应成功: %v", err)
	}
	_, _, _, err = st.ApplyCouponToOrder(newRechargeOrder(t, st, 4, 100).ID, 4, "Q-TOT", CouponKindRecharge)
	if code := couponErrCode(t, err); code != CouponErrSoldOut {
		t.Errorf("抢完应报 %s，实际 err=%v", CouponErrSoldOut, err)
	}
	if got, _ := st.GetCouponByID(total.ID); got.UsedCount != 1 {
		t.Errorf("抢完后 used_count 仍应为 1（失败不计数），实际 %d", got.UsedCount)
	}
	// 预览同样把「抢完」算进去：不能让用户在收银台看到可用、下单却失败
	if _, _, perr := st.PreviewCouponDiscount("Q-TOT", CouponKindRecharge, 100, 5); perr == nil {
		t.Error("已抢完的券预览应报错")
	}
}

// TestCouponReleaseStaleRedemptions 挂单回收：cancelled 单退额度、paid 单不动、pending 单保留占用。
func TestCouponReleaseStaleRedemptions(t *testing.T) {
	st := couponEnv(t)
	cp := mustCreateCoupon(t, st, &Coupon{Code: "REL1", DiscountType: CouponTypeAmount, DiscountValue: 10, Enabled: 1})

	pending := newRechargeOrder(t, st, 1, 100)
	if _, _, _, err := st.ApplyCouponToOrder(pending.ID, 1, "REL1", CouponKindRecharge); err != nil {
		t.Fatalf("pending 单核销失败: %v", err)
	}
	canceled := newRechargeOrder(t, st, 1, 100)
	if _, _, _, err := st.ApplyCouponToOrder(canceled.ID, 1, "REL1", CouponKindRecharge); err != nil {
		t.Fatalf("待取消单核销失败: %v", err)
	}
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE orders SET status='cancelled' WHERE id=?", canceled.ID); err != nil {
		t.Fatalf("置 cancelled 失败: %v", err)
	}
	paid := newRechargeOrder(t, st, 1, 100)
	if _, _, _, err := st.ApplyCouponToOrder(paid.ID, 1, "REL1", CouponKindRecharge); err != nil {
		t.Fatalf("待支付单核销失败: %v", err)
	}
	if err := st.MarkOrderPaid(paid.ID, 1); err != nil {
		t.Fatalf("模拟入账失败: %v", err)
	}

	if n := st.ReleaseStaleCouponRedemptions(); n != 1 {
		t.Errorf("应回退 1 条 cancelled 单核销，实际 %d", n)
	}
	got, _ := st.GetCouponByID(cp.ID)
	if got.UsedCount != 2 {
		t.Errorf("回退后 used_count 期望 2（pending+paid），实际 %d", got.UsedCount)
	}
	if len(st.ListCouponRedemptions(cp.ID, 0)) != 2 {
		t.Errorf("流水应剩 2 条（pending+paid）")
	}
	// 幂等：再扫一次不得重复回退
	if n := st.ReleaseStaleCouponRedemptions(); n != 0 {
		t.Errorf("二次回收应为 0，实际 %d", n)
	}
	// 回收后该单可再次用券的场景由 pending 校验把关：cancelled 单永远不可再核销
	if _, _, _, err := st.ApplyCouponToOrder(canceled.ID, 1, "REL1", CouponKindRecharge); err == nil {
		t.Error("已取消单不得再核销")
	}
	// 删模板不影响历史流水
	if err := st.DeleteCoupon(cp.ID); err != nil {
		t.Fatalf("删券失败: %v", err)
	}
	if len(st.ListCouponRedemptions(cp.ID, 0)) != 2 {
		t.Error("删模板后历史核销流水应保留（对账依据）")
	}
}

// TestCouponPackageOrderSubscribeKind 订阅单用券：只折钱、不折发放计量（句数/token 台账不动）。
func TestCouponPackageOrderSubscribeKind(t *testing.T) {
	st := couponEnv(t)
	pkg, err := st.CreatePackage(&Package{Code: "cp_pack", Name: "券测试包", PType: PackagePaid,
		Sentences: 1000, DurationDays: 30, PriceMoney: 300, Enabled: 1})
	if err != nil {
		t.Fatalf("建包失败: %v", err)
	}
	cp := mustCreateCoupon(t, st, &Coupon{Code: "SUB1", DiscountType: CouponTypePercent,
		DiscountValue: 50, Kind: CouponKindSubscribe, Enabled: 1})
	o, err := st.CreatePackageOrder(1, pkg, 1, "mock")
	if err != nil {
		t.Fatalf("订阅下单失败: %v", err)
	}
	origin, tokensBefore := o.AmountMoney, o.AmountTokens
	if origin <= 0 {
		t.Fatalf("订阅单应收应大于 0，实际 %.2f", origin)
	}
	if _, _, _, err = st.ApplyCouponToOrder(o.ID, 1, cp.Code, CouponKindRecharge); err == nil {
		t.Error("订阅专用券用在充值口径应被拒绝")
	}
	_, disc, paid, err := st.ApplyCouponToOrder(o.ID, 1, cp.Code, CouponKindSubscribe)
	if err != nil {
		t.Fatalf("订阅单核销失败: %v", err)
	}
	if paid != roundMoney(origin-disc) || disc <= 0 {
		t.Errorf("订阅单折让不符：origin=%.2f disc=%.2f paid=%.2f", origin, disc, paid)
	}
	got, gerr := st.GetOrder(o.ID, 1)
	if gerr != nil {
		t.Fatalf("取订阅单失败: %v", gerr)
	}
	if got.AmountTokens != tokensBefore {
		t.Errorf("订阅单发放计量必须不变（券不打折句数）：期望 %d 实际 %d", tokensBefore, got.AmountTokens)
	}
	if got.AmountMoney != paid {
		t.Errorf("订阅单应收应为折后 %.2f，实际 %.2f", paid, got.AmountMoney)
	}
}
