// ============ 本文件职责中文说明 ============
// USDT 收款 HTTP 层（改造方案 2026-09-15）：
//   - handlePayCreateUSDT：/api/pay/create（channel=usdt）分支——校验开关与链，
//     按订单人民币应收 × 汇率快照折算 USDT 应得额（含随机尾数），落收款要素快照
//     usdt_orders，出参 usdt_pay（地址/精确金额/24h 窗口/链浏览器与确认阈值）。
//     本分支不产生任何"已收款"状态——钱是否到账只有两条路：
//     M1 人工核销（manual-confirm 声明 txid + 后台确认）或 M2 reconciler 链上对账。
//   - usdtPayPayloadFor：/api/pay/status 轮询回显收款要素与进度（pending 期间前端展示）。
//
// =============================================
package api

import (
	"net/http"
	"strings"

	"translator/internal/payment"
	"translator/internal/store"
)

// usdtPayPayload USDT 收款要素出参（下单响应与状态轮询共用）。
type usdtPayPayload struct {
	Chain          string `json:"chain"`
	Address        string `json:"address"`
	Amount         string `json:"amount"` // 精确应得（6 位小数字符串，含尾数）
	AmountMicro    int64  `json:"amount_micro"`
	Tail           string `json:"tail"`              // 尾数（唯一对单标识）
	RateFen        int64  `json:"rate_fen_per_usdt"` // 下单时汇率快照（分/USDT）
	ExpiresAt      string `json:"expires_at"`        // 支付窗口截止（24h）
	PayURI         string `json:"pay_uri"`           // tron:地址?amount=X（钱包扫码）
	Confirmations  int64  `json:"confirmations"`     // 所需确认数
	DeclaredTxHash string `json:"declared_tx_hash"`  // 客户声明哈希（线索）
	SettledTxHash  string `json:"settled_tx_hash"`   // 已结算链上凭证（paid 后）
	ExplorerTxURL  string `json:"explorer_tx_url"`   // 声明哈希的浏览器链接（可空）
	AutoSettleOn   bool   `json:"-"`
}

// attachUSDTMeta 为 pending 订单挂 USDT 收款要素（充值单与订阅单共用）。
// 返回 payload 与可读错误（空=成功）。不写响应，调用方决定错误出口与后续流程。
func (s *Server) attachUSDTMeta(u *store.User, tid int64, o *store.Order, money float64, chainRaw string) (*usdtPayPayload, string) {
	cfg := s.Store.GetUSDTCfg()
	if !cfg.Enabled {
		return nil, "USDT 收款未开放"
	}
	chain := payment.NormalizeChain(chainRaw)
	if strings.TrimSpace(chainRaw) == "" && chain == "" && len(cfg.Chains) > 0 {
		chain = cfg.Chains[0] // 未指定链才落到配置首链；显式非法链名不得静默改链
	}
	ok := false
	for _, c := range cfg.Chains {
		if c == chain {
			ok = true
		}
	}
	if !ok {
		return nil, "该链未开放或收款地址未配置"
	}
	addr := cfg.Addrs[chain]
	fen := int64(money*100 + 0.5)
	baseMicro, err := payment.CalcBaseMicro(fen, cfg.RateFen)
	if err != nil {
		return nil, err.Error()
	}
	meta, err := s.Store.CreateUSDTOrderMeta(o.ID, tid, chain, addr, baseMicro, cfg.RateFen, cfg.TailEnabled)
	if err != nil {
		return nil, "创建收款要素失败: " + store.DebriefDBError(err)
	}
	payload := s.usdtPayPayload(meta, cfg)
	_ = s.Store.UpdateOrderPrepay(o.OrderNo, "usdt:"+chain, meta.ToAddr)
	if u != nil {
		s.Store.LogAudit(tid, u.ID, "pay_create", "orders", o.OrderNo+" channel=usdt chain="+chain+" amount="+payload.Amount)
	}
	return &payload, ""
}

// handlePayCreateUSDT channel=usdt 下单分支。参数：tid=生效租户，o=已创建的 pending 订单，
// money=人民币应收（元），chainRaw=请求指定链（可空）。
func (s *Server) handlePayCreateUSDT(w http.ResponseWriter, r *http.Request, u *store.User, tid int64, o *store.Order, money float64, chainRaw string) {
	payload, errMsg := s.attachUSDTMeta(u, tid, o, money, chainRaw)
	if errMsg != "" {
		code := 200
		if payload == nil && strings.Contains(errMsg, "未开放") {
			code = 403
		}
		writeJSON(w, code, map[string]interface{}{"success": false, "message": errMsg, "order": o})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "order": o, "channel": "usdt", "usdt_pay": payload})
}

// usdtPayPayload 由订单快照 + 运行配置组装出参。
func (s *Server) usdtPayPayload(meta *store.USDTOrderMeta, cfg *store.USDTSettings) usdtPayPayload {
	conf := int64(19)
	if c, ok := cfg.Confirm[meta.Chain]; ok {
		conf = c
	}
	p := usdtPayPayload{
		Chain:          meta.Chain,
		Address:        meta.ToAddr,
		Amount:         payment.FormatUSDTMicro(meta.AmountMicro),
		AmountMicro:    meta.AmountMicro,
		Tail:           payment.FormatUSDTMicro(meta.TailMicro),
		RateFen:        meta.RateFen,
		ExpiresAt:      meta.ExpiresAt,
		Confirmations:  conf,
		DeclaredTxHash: meta.ClientTxHash,
		SettledTxHash:  meta.MatchedTxHash,
		AutoSettleOn:   cfg.AutoSettle,
	}
	p.PayURI = "tron:" + meta.ToAddr + "?amount=" + p.Amount
	if meta.Chain != "trc20" {
		p.PayURI = "ethereum:" + meta.ToAddr + "?amount=" + p.Amount // erc20/bep20 钱包通用 URI 族
	}
	if meta.ClientTxHash != "" {
		p.ExplorerTxURL = payment.ExplorerURL(meta.Chain, meta.ClientTxHash)
	}
	return p
}

// usdtPayForOrder 状态轮询回显（非 usdt 单返回 nil）。
func (s *Server) usdtPayForOrder(o *store.Order) *usdtPayPayload {
	if o == nil || o.Channel != "usdt" {
		return nil
	}
	meta, err := s.Store.GetUSDTOrderMeta(o.ID)
	if err != nil {
		return nil
	}
	p := s.usdtPayPayload(meta, s.Store.GetUSDTCfg())
	return &p
}
