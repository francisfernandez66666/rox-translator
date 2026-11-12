// ============ sales_quote_api.go · 职责说明 ============
// 三期 W2 六包接线：报价域的**窄管理面**（/api/admin/sales/quote*）。
// 能力本体早就在 store/deal_quote.go（CreateQuote/AcceptQuote/GetQuote），
// 本文件只做接线——零新业务逻辑（W2 定位「先接线再建能力」）。
//
// ★ 三条硬口径（施工图 W2 + A06）：
//  1. **AcceptQuote 是唯一转正写口**：全仓扫调用点必须恰为 1（A06/M0213）——
//     转正（draft/sent → accepted）不许出现第二个 UPDATE 路径，否则「一单两转正」
//     没有任何闸门看得见。前端页面 W3/W6 才挂，本批不画空壳页。
//  2. **金额只有「分」一个量纲**（TotalCents int64，与 payments.amount_fen 同量纲）；
//     handler 不做分↔元换算——C2 旧账「同名不同单位」的禁令，A06/M0215 钉住。
//  3. **撞在途唯一索引走 db.IsUniqueViolation 分支**（store 已折成
//     ErrQuoteOneOpenPerDeal）：两方言报错文案不同，禁止按 DB 原文分支。
//
// 鉴权（A07，route_auth_gate_test 自动扫）：读 ≥2（requireDeptAdmin，部门管理员
// 合法读口，先例 sales_inbox_api.go）；写 ≥3（salesConfigAdmin → requireTenantAdmin，
// 唯一强闸入口，不许新开第二套）。ErrQuoteNotFound 一律 not-found——不泄露存在性。
// ================================================
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// routesSalesQuote 注册报价管理路由（由 routesSales() 调用）。
func (s *Server) routesSalesQuote() {
	s.mux.HandleFunc("/api/admin/sales/quote", s.handleSalesQuoteGet)
	s.mux.HandleFunc("/api/admin/sales/quote/create", s.handleSalesQuoteCreate)
	s.mux.HandleFunc("/api/admin/sales/quote/accept", s.handleSalesQuoteAccept)
}

// handleSalesQuoteGet 读单条报价（?quote_id=；不存在回 404，跨租户同样 404——
// store 层已把两种情况折成同一个 ErrQuoteNotFound，本层不区分，不泄露存在性）。
func (s *Server) handleSalesQuoteGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	quoteID := strings.TrimSpace(r.URL.Query().Get("quote_id"))
	if quoteID == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 quote_id"))
		return
	}
	q, err := s.Store.GetQuote(s.effTenant(r, u), quoteID)
	if err != nil {
		s.writeQuoteStoreError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "quote": q})
}

// handleSalesQuoteCreate 建一张报价（默认 draft；金额单位为分）。
func (s *Server) handleSalesQuoteCreate(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body struct {
		DealID     string `json:"deal_id"`
		TotalCents *int64 `json:"total_cents"`
		Currency   string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
		return
	}
	// ★ 量纲闸在 handler 侧立住：金额必须是「分」，负数拒收（store 也有同判据，
	// 这里先挡是为了给可辨文案而不是落库报错）。
	if body.TotalCents == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 total_cents"))
		return
	}
	if *body.TotalCents < 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "报价金额不能为负"))
		return
	}
	if strings.TrimSpace(body.DealID) == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 deal_id"))
		return
	}
	q, err := s.Store.CreateQuote(tid, body.DealID, *body.TotalCents, body.Currency)
	if err != nil {
		s.writeQuoteStoreError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "quote": q})
}

// handleSalesQuoteAccept 把一张在途报价流转为 accepted（唯一转正写口，A06）。
func (s *Server) handleSalesQuoteAccept(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body struct {
		QuoteID string `json:"quote_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
		return
	}
	if strings.TrimSpace(body.QuoteID) == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 quote_id"))
		return
	}
	if err := s.Store.AcceptQuote(tid, body.QuoteID); err != nil {
		s.writeQuoteStoreError(w, r, err)
		return
	}
	q, err := s.Store.GetQuote(tid, body.QuoteID)
	if err != nil {
		s.writeQuoteStoreError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "quote": q})
}

// writeQuoteStoreError 把 store 层报价错误折成对外语义（唯一出口，禁止各 handler 各写一份）：
// 在途唯一索引冲突 → 409 Conflict；查无/跨租户/状态不允许 → 404（不泄露存在性）；
// 其余 → 500 走 publicErrMessage 脱敏。
func (s *Server) writeQuoteStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrQuoteOneOpenPerDeal):
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, publicErrMessage(r.Context(), err)))
	case errors.Is(err, store.ErrQuoteNotFound):
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, publicErrMessage(r.Context(), err)))
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
	}
}
