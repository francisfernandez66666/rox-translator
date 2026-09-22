// ============ 本文件职责中文说明 ============
// 多币种报价（★ #75，2026-09-23）的 API 层：超管配置口 + 报价出参 + 下单汇率快照。
//
// 接口（均需平台超管，鉴权/审计/错误出口写法参照 pay_channels.go）：
//   - GET  /api/admin/config/quote-currency   回显生效报价配置（币种、倍率表、白名单、env 接管标注）
//   - POST /api/admin/config/quote-currency   保存 {"currency":"USD","rates":{"USD":7.2,...}}
//     校验先行、整批落库在后：币种不在白名单 / 倍率 ≤0 或 ≥1000 → 整体拒绝并回中文 message。
//     本配置无密文项，不走 internal/secret（别乱加密，加密了反而没法审计比对）。
//
// 出参增强（老字段一个不删、语义不变，前端与 e2e 依赖）：
//   - handlePlans / handleMyPackage 增 quote_currency、fx_rates_snapshot（仅币种码→倍率，
//     不含任何密钥），每个套餐增 price_display（本币金额，2 位）与 price_cny。
//
// 下单快照：各建单点（pay/create、package/subscribe、package/upgrade、自动续费、
// 后台代建充值单）在「金额最终确定后」调 stampOrderQuote，把下单时刻的
// currency + fx_rate + money_cny 落进 orders。快照失败只告警不阻断收款——
// 报价是展示与审计层，资金主链路（amount_money）不依赖它。
//
// ★ fail-closed 红线：本文件不存在任何"以外币实际扣款"的路径。微信/支付宝收单
// 主体恒为 CNY（见 payment 包），外币金额 price_display 仅供客户看，
// PayRequest/渠道下单永远投人民币分。签约外币收单前禁止扩这个口子。
//
// ★ 当前为关闭封存态（2026-09-22 用户决策：收单只有微信/支付宝 CNY 与币安 USDT 两条
// 渠道，外币报价暂无业务落点）。总开关在 store/currency.go 的 quoteFeatureOpen：
// 关闭态下 QuoteCurrencyCfg 恒回 CNY、外币写入一律 400 拒绝、白名单只暴露 CNY，
// 管理台据 supported_currencies 无外币项自动隐藏报价区块。接口与快照列原样保留，
// 具备外币收单能力后翻转开关即重开。
// ==========================================
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
)

// handleAdminQuoteCurrency GET/POST /api/admin/config/quote-currency —— 超管报价配置口。
// GET 回显生效值（环境变量 > 库配置 > 默认，读取即清洗）；POST 整体校验后保存。
func (s *Server) handleAdminQuoteCurrency(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleAdminQuoteCurrencySave(w, r)
		return
	}
	u, err := s.requireQuoteCfgAdmin(w, r)
	if err != nil {
		return
	}
	cfg := s.Store.QuoteCurrencyCfg()
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		// 当前生效配置（含清洗结果：脏键已被 store 层丢弃，这里回显即可信值）
		"currency": cfg.Currency, "rates": cfg.Rates,
		"supported_currencies": store.SupportedQuoteCurrencies(),
		// 报价功能开放态（★ 关闭封存中恒 false）：管理台与自动化闸门据此区分
		// "配置就是 CNY" 与 "功能开着但没配外币"，两种形态的界面语义不同
		"feature_open": store.QuoteFeatureOpen(),
		// env 接管标注：管理员据此知道「改这里不生效」，与 pay_channels 同一坑位提示
		"env_overridden": quoteCfgEnvOverridden(),
		"updated_by":     u.ID,
	})
}

// handleAdminQuoteCurrencySave POST 保存报价币种与汇率倍率。
// 请求 body: {"currency":"USD","rates":{"USD":7.2,"EUR":7.8}}
// 语义：currency 留空=不表态（维持库中现值）；rates 传空对象=清除全部自定义倍率（回落仅 CNY）。
func (s *Server) handleAdminQuoteCurrencySave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireQuoteCfgAdmin(w, r)
	if err != nil {
		return
	}
	var req struct {
		Currency string             `json:"currency"`
		Rates    map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误：需为 {\"currency\":\"USD\",\"rates\":{...}}"))
		return
	}
	// ★ 校验先行、两项都过了才落库（同 pay_channels_save 的"不留半套配置"口径）：
	// 只存了币种没存汇率 = 客户看到外币价却下不了单，比不配更糟。
	if verr := store.ValidateQuoteCurrency(req.Currency); verr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), verr)))
		return
	}
	if verr := store.ValidateFxRates(req.Rates); verr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), verr)))
		return
	}
	// 选了非 CNY 报价就必须能换算：倍率表里得有该币种，否则当场拒绝（防"配了不用"的死配置）。
	// 注：校验对象是本次提交的 rates 整表（管理台表单一次性提交全部倍率）；
	// 若汇率实际由环境变量 FX_RATES 接管，此处按提交值表态即可（生效值另有 env_overridden 标注）。
	code := strings.ToUpper(strings.TrimSpace(req.Currency))
	if code != "" && code != store.QuoteCurrencyCNY {
		if _, ok := req.Rates[code]; !ok {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				"报价币种 "+code+" 缺少汇率倍率（1 "+code+" = ? 人民币），请在 rates 中一并配置"))
			return
		}
	}
	if err := s.Store.SetFxRates(req.Rates); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
		return
	}
	if err := s.Store.SetQuoteCurrency(s.effTenant(r, u), req.Currency); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
		return
	}
	// 审计留痕：改价配置直接影响客户看到的外币数字，谁改的、改成了什么必须可查
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "quote_currency_save", "system_config",
		"currency="+quoteAuditVal(code)+" rates="+quoteAuditRates(req.Rates))
	cfg := s.Store.QuoteCurrencyCfg()
	writeJSON(w, 200, map[string]interface{}{"success": true, "currency": cfg.Currency, "rates": cfg.Rates})
}

// requireQuoteCfgAdmin 报价配置口鉴权：平台超管（L4 且 auth.IsSuperAdmin）。
// 为什么不用现成的 requireSuperAdmin：它的 403 文案写死了「数据采集」，
// 照抄逻辑不抄文案，避免跨域误导（判定链与 requireSuperAdmin 完全一致）。
func (s *Server) requireQuoteCfgAdmin(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, publicErrMessage(r.Context(), err)))
		return nil, err
	}
	if !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "仅平台超管可配置报价币种与汇率"))
		return nil, &apiErr{"非超管"}
	}
	return u, nil
}

// quoteCfgEnvOverridden 标注哪些报价项正由环境变量接管（与 payConfigEnvOverridden 同一目的：
// env 压住库值是设计取舍，但「保存成功却不生效」必须摆到界面上告知）。
func quoteCfgEnvOverridden() map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(os.Getenv(store.EnvQuoteCurrency)) != "" {
		out[store.ConfigQuoteCurrency] = store.EnvQuoteCurrency
	}
	if strings.TrimSpace(os.Getenv(store.EnvFxRates)) != "" {
		out[store.ConfigFxRates] = store.EnvFxRates
	}
	return out
}

// quoteAuditVal 审计串里的币种值（空=未表态，写"不变"而不是空串，避免歧义）。
func quoteAuditVal(code string) string {
	if code == "" {
		return "不变"
	}
	return code
}

// quoteAuditRates 倍率表 → 审计串（键排序保证稳定可比对，只记币种码与倍率，无敏感信息）。
func quoteAuditRates(rates map[string]float64) string {
	if len(rates) == 0 {
		return "清空"
	}
	keys := make([]string, 0, len(rates))
	for k := range rates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, strings.ToUpper(k)+"="+
			strconv.FormatFloat(rates[k], 'f', -1, 64))
	}
	return strings.Join(parts, ",")
}

// quoteSnapshot 报价出参的公共三件套（handlePlans / handleMyPackage 共用一份读口径）。
// 返回：生效币种（已 Resolve 回落）、倍率、可换算判定、倍率快照表。
func (s *Server) quoteSnapshot() (code string, rate float64, rates map[string]float64) {
	cfg := s.Store.QuoteCurrencyCfg()
	code, rate = cfg.Resolve()
	return code, rate, cfg.Rates
}

// stampOrderQuote 建单链路统一落报价快照（★ #75）。
// 参数：ctx 用于日志 trace（HTTP 链路传 r.Context()，watchdog 传 context.Background()）；
// o=金额已最终确定的订单（amount_money 已是实收）。
// 失败不阻断收款主链路（快照是展示/审计层），但必须出声——静默丢快照等于没做留痕。
func (s *Server) stampOrderQuote(ctx context.Context, o *store.Order) {
	if o == nil || o.ID <= 0 {
		return
	}
	code, rate, _ := s.quoteSnapshot()
	if err := s.Store.StampOrderCurrency(o.ID, code, rate); err != nil {
		observability.Warn(ctx, "订单报价快照落库失败（不影响收款主链路）",
			"order", o.OrderNo, "currency", code, "err", err.Error())
	}
}
