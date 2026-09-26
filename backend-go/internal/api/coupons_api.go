// ============ coupons_api.go · 职责说明 ============
// 优惠券/促销码 HTTP 层（★ #41 商业洞三，2026-09-21 实装）。
//
//	POST /api/coupon/preview            租户管理员：试算券对本单的折让（不落库、不建单）
//	GET  /api/admin/coupons             超管：券列表（含实时核销汇总）
//	POST /api/admin/coupons/save        超管：新建（id=0）/ 更新（id>0）
//	POST /api/admin/coupons/delete      超管：删券模板（核销流水保留）
//	GET  /api/admin/coupons/redemptions 超管：核销流水（活动复盘与对账）
//
// 券在下单链路里的位置（与 pay.go / plans_api.go 共用本文件的两个小工具）：
//
//	下单前 couponOrderAmount 服务端重算折前应付 → 建 pending 单 → store.ApplyCouponToOrder 核销并改写
//	amount_money → 渠道按折后金额出码。这样「应收单一事实源」仍是 orders.amount_money，
//	支付回调核对、退款、开票三条链路零改动即可对齐折扣后的实付。
//
// ★ 错误口径（#37 脱敏）：store.CouponError 是设计给用户看的提示，原样透出；
// 其余（DB 故障、SQL 细节）一律走 publicErrMessage 脱敏 + 日志留原文。
// ★ 错误响应口径（F-64① 批 I-7）：失败一律走 s.writeError + apierrors 统一出口，
// 不再用 200 承载失败，也不再内联 writeJSON(4xx, ...)。
// ==========================================
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
)

// couponJSON 券的出参视图（附实时核销汇总，超管列表页一屏看全）。
func couponJSON(c *store.Coupon) map[string]interface{} {
	st := map[string]interface{}{
		"id":               c.ID,
		"code":             c.Code,
		"name":             c.Name,
		"kind":             c.Kind,
		"discount_type":    c.DiscountType,
		"discount_value":   c.DiscountValue,
		"max_discount":     c.MaxDiscount,
		"min_amount":       c.MinAmount,
		"max_uses":         c.MaxUses,
		"used_count":       c.UsedCount,
		"per_tenant_limit": c.PerTenantLimit,
		"valid_from":       c.ValidFrom,
		"valid_until":      c.ValidUntil,
		"enabled":          c.Enabled,
		"note":             c.Note,
		"created_by":       c.CreatedBy,
		"created_at":       c.CreatedAt,
		"updated_at":       c.UpdatedAt,
		"remaining":        remainingUses(c),
		"total_discount":   0.0,
		"paid_total":       0.0,
	}
	return st
}

// remainingUses 剩余可用次数（-1=不限）。
func remainingUses(c *store.Coupon) int64 {
	if c.MaxUses <= 0 {
		return -1
	}
	if r := c.MaxUses - c.UsedCount; r > 0 {
		return r
	}
	return 0
}

// couponFromReq 解析并归一券入参（超管表单 → store.Coupon）。
func couponFromReq(r *http.Request) (*store.Coupon, error) {
	var req struct {
		ID             int64   `json:"id"`
		Code           string  `json:"code"`
		Name           string  `json:"name"`
		Kind           string  `json:"kind"`
		DiscountType   string  `json:"discount_type"`
		DiscountValue  float64 `json:"discount_value"`
		MaxDiscount    float64 `json:"max_discount"`
		MinAmount      float64 `json:"min_amount"`
		MaxUses        int64   `json:"max_uses"`
		PerTenantLimit int64   `json:"per_tenant_limit"`
		ValidFrom      string  `json:"valid_from"`
		ValidUntil     string  `json:"valid_until"`
		Enabled        *int    `json:"enabled"`
		Note           string  `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, errors.New("参数格式错误")
	}
	c := &store.Coupon{
		ID: req.ID, Code: store.NormalizeCouponCode(req.Code), Name: strings.TrimSpace(req.Name),
		Kind: strings.TrimSpace(req.Kind), DiscountType: strings.TrimSpace(req.DiscountType),
		DiscountValue: req.DiscountValue, MaxDiscount: req.MaxDiscount, MinAmount: req.MinAmount,
		MaxUses: req.MaxUses, PerTenantLimit: req.PerTenantLimit,
		ValidFrom: strings.TrimSpace(req.ValidFrom), ValidUntil: strings.TrimSpace(req.ValidUntil),
		Enabled: 1, Note: strings.TrimSpace(req.Note),
	}
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	// 默认口径补全：新建表单常留空，按「最宽松且最不易误解」的方向落默认值
	if c.Kind == "" {
		c.Kind = store.CouponKindAny
	}
	if c.DiscountType == "" {
		c.DiscountType = store.CouponTypePercent
	}
	if c.Name == "" {
		c.Name = c.Code
	}
	return c, nil
}

// handleAdminCoupons 超管：券列表。
func (s *Server) handleAdminCoupons(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireSuperAdmin(w, r); err != nil {
		return
	}
	list := s.Store.ListCoupons()
	out := make([]map[string]interface{}, 0, len(list))
	for _, c := range list {
		j := couponJSON(c)
		st := s.Store.GetCouponStats(c.ID)
		j["total_discount"] = st.TotalDiscount
		j["paid_total"] = st.PaidTotal
		out = append(out, j)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "coupons": out})
}

// handleAdminCouponSave 超管：新建/更新券（id=0 走新建）。
func (s *Server) handleAdminCouponSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdmin(w, r)
	if err != nil {
		return
	}
	if r.Method != http.MethodPost {
		// ★ F-64①（批 I-7）：内联 405 → 统一出口（405 ErrMethodNotAllowed），状态码不变、错误体补齐 code/trace_id
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	c, perr := couponFromReq(r)
	if perr != nil {
		// ★ F-64①（批 I-7）：内联 400 → 统一出口（400 ErrValidation）
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, perr.Error()))
		return
	}
	if c.ID <= 0 {
		created, cerr := s.Store.CreateCoupon(c)
		if cerr != nil {
			// ★ F-64①（批 I-7）：原 200 承载失败 → 500：建券失败是存储写入故障
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), cerr)))
			return
		}
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "coupon_create", "coupons", created.Code)
		writeJSON(w, 200, map[string]interface{}{"success": true, "coupon": couponJSON(created)})
		return
	}
	if uerr := s.Store.UpdateCoupon(c); uerr != nil {
		// ★ F-64①（批 I-7）：原 200 承载失败 → 500：改券失败是存储写入故障
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), uerr)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "coupon_update", "coupons", strconv.FormatInt(c.ID, 10))
	got, gerr := s.Store.GetCouponByID(c.ID)
	if gerr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": true})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "coupon": couponJSON(got)})
}

// handleAdminCouponDelete 超管：删券模板（核销流水保留，历史对账不受影响）。
func (s *Server) handleAdminCouponDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdmin(w, r)
	if err != nil {
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if derr := json.NewDecoder(r.Body).Decode(&req); derr != nil || req.ID <= 0 {
		// ★ F-64①（批 I-7）：内联 400 → 统一出口（400 ErrValidation）
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少券 ID"))
		return
	}
	c, gerr := s.Store.GetCouponByID(req.ID)
	if gerr != nil {
		// ★ F-64①（批 I-7）：原 200 承载失败 → 404：券找不到是可自证的缺失
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "券不存在"))
		return
	}
	if derr := s.Store.DeleteCoupon(req.ID); derr != nil {
		// ★ F-64①（批 I-7）：原 200 承载失败 → 500：删券失败是存储写入故障
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), derr)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "coupon_delete", "coupons", c.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAdminCouponRedemptions 超管：核销流水（?coupon_id= 可选过滤，limit 默认 200、上限 500）。
func (s *Server) handleAdminCouponRedemptions(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireSuperAdmin(w, r); err != nil {
		return
	}
	cid, _ := strconv.ParseInt(r.URL.Query().Get("coupon_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, 200, map[string]interface{}{
		"success":     true,
		"redemptions": s.Store.ListCouponRedemptions(cid, limit),
	})
}

// handleCouponPreview 租户管理员：试算券对本单的折让。
// 入参二选一：points（充值单，与 /api/pay/create 同口径）或 package_code（订阅单）。
// ★ 金额一律服务端按同一算法重算，绝不采信前端传来的数字——否则改一下 body 就能白拿折扣。
func (s *Server) handleCouponPreview(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// ★ F-64①（批 I-7）：内联 403 → 统一出口（403 ErrForbidden），状态码不变、错误体补齐 code/trace_id
		s.writeAuthzError(w, r, err) // ★ F-64①：未登录→401、等级不足→403（见 server.go writeAuthzError）
		return
	}
	tid := s.effTenant(r, u)
	if tid <= 0 {
		// ★ F-64①（批 I-7）：内联 400 → 统一出口（400 ErrValidation）：平台上下文本来就不能试算券，属请求发错了上下文
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "平台上下文无订单概念"))
		return
	}
	var req struct {
		Code        string `json:"code"`
		Points      int64  `json:"points"`
		PackageCode string `json:"package_code"`
	}
	if derr := json.NewDecoder(r.Body).Decode(&req); derr != nil {
		// ★ F-64①（批 I-7）：内联 400 → 统一出口（400 ErrValidation）
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "参数格式错误"))
		return
	}
	kind, origin, oerr := s.couponOrderAmount(tid, req.Points, strings.TrimSpace(req.PackageCode))
	if oerr != nil {
		// ★ F-64①（批 I-7）：原 200 承载失败 → 400：couponOrderAmount 的返回文案按其注释自证
		//   「均为可直接回给用户的经营提示」（套餐不存在/免费包不适用券/未指定积分…），
		//   客户改一下入参即可纠正，故给 ErrValidation 而不是 500
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, oerr.Error()))
		return
	}
	c, discount, perr := s.Store.PreviewCouponDiscount(req.Code, kind, origin, tid)
	if perr != nil {
		// ★ F-64①（批 I-7）：原 200 承载失败 → 400：试算失败绝大多数是券本身的问题（码错/过期/已抢完），
		//   客户换券即可自改；coupon_error 布尔进 details 保留，前端仍按它决定「券区红字」还是「整单错误」
		msg := couponHint(perr)
		if msg == "" {
			msg = publicErrMessage(r.Context(), perr) // 非业务券错误：走统一脱敏文案
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, msg).
			WithDetails(map[string]interface{}{"coupon_error": couponIsUserHint(perr)}))
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":        true,
		"kind":           kind,
		"origin_money":   origin,
		"discount_money": discount,
		"pay_money":      round2(origin - discount),
		"coupon":         couponJSON(c),
	})
}

// couponOrderAmount 按下单口径算出本单折前应付（元）与券适用类型。
// 参数 points=充值积分（>0 时优先），packageCode=订阅包编码；
// 返回 (kind, 应付元, 错误)——错误文案均为可直接回给用户的经营提示。
func (s *Server) couponOrderAmount(tid, points int64, packageCode string) (string, float64, error) {
	if packageCode != "" {
		pkg, err := s.Store.GetPackageByCode(tid, packageCode)
		if err != nil || pkg == nil {
			return "", 0, errors.New("套餐不存在")
		}
		if pkg.PType == store.PackageFree {
			return "", 0, errors.New("免费体验包无需支付，不适用优惠券")
		}
		if pkg.PriceMoney <= 0 {
			return "", 0, errors.New("该套餐未设置价格，不适用优惠券")
		}
		// PackageOrderPrice 含「新客首月半价」——与建单同一函数，预览与实付不会两张皮
		return store.CouponKindSubscribe, round2(s.Store.PackageOrderPrice(pkg, tid)), nil
	}
	if points > 0 {
		return store.CouponKindRecharge, round2(float64(s.Store.TokensToFen(s.Store.TokensFromPoints(points))) / 100), nil
	}
	return "", 0, errors.New("请指定充值积分数或套餐编码")
}

// couponApply 建单后核销券并回传折后应付（pay/create、subscribe、upgrade 三处共用）。
// 参数 code 为空视为「不用券」，直接回原应付（不报错，让调用点少一层分支）。
func (s *Server) couponApply(orderID, tid int64, code, kind string, originMoney float64) (float64, float64, error) {
	if store.NormalizeCouponCode(code) == "" {
		return originMoney, 0, nil
	}
	_, discount, paid, err := s.Store.ApplyCouponToOrder(orderID, tid, code, kind)
	if err != nil {
		return originMoney, 0, err
	}
	return paid, discount, nil
}

// couponIsUserHint 判定错误是否属于「用户可纠正」的券业务错误（可直接回显文案）。
func couponIsUserHint(err error) bool {
	var ce *store.CouponError
	return errors.As(err, &ce) || errors.Is(err, store.ErrCouponInvalid)
}

// replyCouponFailure 下单链路核销失败的统一回包（pay/create、subscribe、upgrade 共用）。
// 业务错误（券码不存在/不适用/已抢完…）原样回显，用户照着改就能下单；
// 其余（DB 故障等）走 publicErrMessage 脱敏（#37），原文只进日志。
// 无论如何都带上 order_no：pending 单仍在，超时由 order_pending_timeout_min 自动收敛。
func (s *Server) replyCouponFailure(w http.ResponseWriter, r *http.Request, tid, userID int64, orderNo, code string, err error) {
	observability.Error(r.Context(), "优惠券核销失败", "tid", strconv.FormatInt(tid, 10),
		"uid", strconv.FormatInt(userID, 10), "order", orderNo, "code", code, "err", err.Error())
	msg := couponHint(err)
	if msg == "" {
		msg = publicErrMessage(r.Context(), err)
	}
	// ★ F-64①（批 I-7）：原 200 承载失败 → 按「是不是客户能自己改对的券错误」分流给码：
	//   业务券错误（券码不存在/已停用/不适用本单/已抢完…）→ 400 ErrValidation（换券码即可自改）；
	//   其余（DB 故障、SQL 细节等）→ 500 ErrInternal（本进程侧故障，客户无从纠正）。
	//   判据沿用本文件已有的非导出 couponIsUserHint（store 包只暴露 ErrCouponInvalid 哨兵与
	//   CouponError 类型，没有导出的 IsUserHint 判定；同包直接复用，避免两套判据漂移）。
	//   变量名用 errCode 而不是 code：code 已是本函数的券码入参，同作用域重声明会编译不过。
	errCode := apierrors.ErrInternal
	if couponIsUserHint(err) {
		errCode = apierrors.ErrValidation
	}
	s.writeError(w, r, apierrors.New(errCode, msg).
		WithDetails(map[string]interface{}{"order_no": orderNo}))
}

// couponHint 券错误的对外文案：业务错误原样给（用户照着改就能用），其余交调用方兜底。
func couponHint(err error) string {
	var ce *store.CouponError
	if errors.As(err, &ce) {
		return ce.Message
	}
	return ""
}

// round2 金额保留两位小数（与订单金额口径一致，防浮点尾差进对账）。
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
