// ============ 本文件职责中文说明 ============
// 公开「算价/比价」页的数据面：GET /api/pricing/meta（无需登录）。
//
// 为什么单独开一个口，而不是塞进 /api/plans：
//   - /api/plans 的契约是「套餐清单」，被定价页、e2e 与 quote_currency_test 三方依赖，
//     往里加算价系数等于让套餐接口的语义变浑（F-12 那类「一处口径改动炸三处展示」的病根）。
//   - 算价系数（F-72 两段式的 K/F）此前**只有超管口** `/api/admin/config/est-tokens`，
//     公开页要公示公式就必须有一条匿名可达、且**只出积分口径**的新接口。
//
// ★ 出参铁律（AGENTS §一·5「公开接口零 token 裸值」）：
//
//	本口一律把内部 token 量**折算成积分**后再外露——K(每字符 token)、F(固定 token)、
//	points_tokens_rate、price_fen_per_million_tokens 这四类裸值一个都不发。
//	外露的是三件客户本来就该看得见的东西：每千字·单语种的积分档、每次建单的固定积分、
//	每积分对应的人民币单价。
//
// ★ 与建单预检必须同一把尺（改这里先看 tickets.go）：
//
//	预检算的是 est_tokens = F + 字符数 × 语种数 × K，再 PointsFromTokens 折成积分。
//	本口把同一式子改写成积分域系数（per1k = K×1000÷汇率、fixed = F÷汇率），
//	所以前端用浮点跑同一式子、末尾四舍五入取整，结果与后端预检一致；
//	pricing_meta_test.go 的等式锁就是钉这条一致性的（页面公式 ≠ 扣费公式 会让官网变成虚假宣传）。
//
// ★ 只读、只回生效值：系数脏了走读侧同一回退链（estConfigFloat → 代码缺省），
//
//	本口不提供任何写入口；调参仍然只有超管表单那条路。
//
// ==========================================
package api

import (
	"net/http"

	apierrors "translator/internal/errors"
)

// pricingMode 公开算价页的一个模式档（积分口径，不含任何 token 裸值）。
type pricingMode struct {
	Code string `json:"code"`
	// PointsPer1kChars 每「1000 源字符 × 1 个目标语种」的线性积分档（＝K×1000÷积分汇率）。
	PointsPer1kChars float64 `json:"points_per_1k_chars"`
	// PointsFixed 每次建单的一次性固定积分档（＝F÷积分汇率；出厂缺省 pro=7.5 / fast=3）。
	// 带小数是刻意的：前端要用它跑与后端同一的浮点式子，这里先取整就等于把误差塞进客户账单。
	PointsFixed float64 `json:"points_fixed"`
}

// handlePricingMeta GET /api/pricing/meta —— 公开算价元数据（匿名可达、只读）。
// 返回: success=true 时携带 modes（两档积分系数）与 points_price_money（每积分人民币单价，元）。
// 文案（公式怎么读、浮动说明、人工价基准与出处）一律在前端 12 语种词典里，本口只给数。
func (s *Server) handlePricingMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "算价元数据接口只支持 GET"))
		return
	}
	rate := s.Store.PointsTokensRate()
	ruler := s.Store.PriceFenPerMillionTokens()
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"modes": []pricingMode{
			{
				Code:             "fast",
				PointsPer1kChars: pointsFromTokensFloat(s.estTokensPerChar("fast")*1000, rate),
				PointsFixed:      pointsFromTokensFloat(s.estTokensFixed("fast"), rate),
			},
			{
				Code:             "pro",
				PointsPer1kChars: pointsFromTokensFloat(s.estTokensPerChar("pro")*1000, rate),
				PointsFixed:      pointsFromTokensFloat(s.estTokensFixed("pro"), rate),
			},
		},
		// 每积分对应人民币（元）＝积分汇率（token/积分）× 尺子（分/百万 token）÷ 1e8。
		// 与「3,000 积分 = ¥299」这条套餐面值同源同联动（F-78：汇率与尺子是一对，只动一头本页跟着一起动）。
		"points_price_money": moneyPerPoint(rate, ruler),
		"unit":               "points",
	})
}

// pointsFromTokensFloat 积分域版本的 PointsFromTokens：保留小数、不取整。
// 为什么要留小数：前端算价页要跑与后端预检**同一条**浮点式子，末了才四舍五入取整；
// 若这里先按 store 的整数口径把 7.5 取成 8，页面显示的系数就和实际扣费对不上，
// 公示公式等于公示了一句假话。
// tokens<=0（固定项被显式配 0）与汇率脏（≤0）一律回 0——脏配置宁可让页面显示「暂不报价」，
// 也不能把除零产生的 ±Inf 写进 JSON（NaN/Inf 序列化会整包 500）。
func pointsFromTokensFloat(tokens float64, rate int64) float64 {
	if rate <= 0 || tokens <= 0 {
		return 0
	}
	return round6(tokens / float64(rate))
}

// moneyPerPoint 每积分对应的人民币单价（元）＝汇率 × 尺子 ÷ 1e8。
// 两个读数必须成对取（F-78 口径）：只换一头就会把「每积分单价」推离套餐面值，
// 而面值推离正是 F-12 那次「定价页/收银台/管理台三口径打架」的复发形态。
func moneyPerPoint(rate int64, ruler int64) float64 {
	if rate <= 0 || ruler <= 0 {
		return 0
	}
	return round6(float64(rate) * float64(ruler) / 1e8)
}

// round6 六位小数归一（只为 JSON 里没有 0.09966800000000001 这类无业务含义的浮点尾巴）。
func round6(v float64) float64 {
	return float64(int64(v*1e6+0.5)) / 1e6
}
