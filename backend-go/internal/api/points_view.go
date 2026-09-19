// ============ points_view.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 对外展示口径折算助手（★ 积分口径全面上线，2026-09-19）：
//   token 仅作内部记账单位（usage_ledger / balance_accounts / system_config 配置键），
//   任何 /api/* 与 /openapi/v1/* 响应一律不出 token 裸值，也不下发积分↔token 汇率。
//   本文件提供三类边界折算助手：
//   - orderViewJSON/ordersViewJSON：充值订单出参 amount_tokens → amount_points
//   - pointsMapJSON：map[标签]=token 合计 → 同键积分合计（用量/趋势看板）
//   - ledgerRowsPoints：用量流水行 cost（token）就地折积分
// =============================================

import (
	"encoding/json"

	"translator/internal/store"
)

// orderViewJSON 订单出参视图：完整序列化 store.Order 后把 amount_tokens 折成 amount_points。
// 参数 o: 订单（nil 返回空对象）。新增列自动随 json tag 透出，无需维护字段清单。
func (s *Server) orderViewJSON(o *store.Order) map[string]interface{} {
	if o == nil {
		return map[string]interface{}{}
	}
	b, err := json.Marshal(o)
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]interface{}{}
	}
	if at, ok := m["amount_tokens"].(float64); ok {
		m["amount_points"] = s.Store.PointsFromTokens(int64(at))
	}
	delete(m, "amount_tokens")
	return m
}

// ordersViewJSON 订单列表出参视图（逐行折积分）。
func (s *Server) ordersViewJSON(list []*store.Order) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(list))
	for _, o := range list {
		out = append(out, s.orderViewJSON(o))
	}
	return out
}

// pointsMapJSON token 合计映射 → 积分合计映射（用量按类型/按趋势分桶通用）。
// 参数 src: map[标签]=token 合计；nil 原样返回 nil。
func (s *Server) pointsMapJSON(src map[string]int64) map[string]int64 {
	if src == nil {
		return nil
	}
	out := make(map[string]int64, len(src))
	for k, v := range src {
		out[k] = s.Store.PointsFromTokens(v)
	}
	return out
}

// referralRecordsJSON 邀请奖励记录出参视图（tokens → reward_points，其余字段同名透传）。
func (s *Server) referralRecordsJSON(list []*store.ReferralRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(list))
	for _, r := range list {
		out = append(out, map[string]interface{}{
			"invitee_uid":   r.InviteeUID,
			"invitee_name":  r.InviteeName,
			"invitee_email": r.InviteeEmail,
			"type":          r.Type,
			"reward_points": s.Store.PointsFromTokens(r.Tokens),
			"days":          r.Days,
			"paid":          r.Paid,
			"created_at":    r.CreatedAt,
		})
	}
	return out
}

// ledgerRowsJSON 用量流水行出参视图：cost（token）折成 cost_points，unit_price 内部单价不再外发。
func (s *Server) ledgerRowsJSON(list []*store.UsageLedger) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(list))
	for _, u := range list {
		out = append(out, map[string]interface{}{
			"id": u.ID, "tenant_id": u.TenantID, "user_id": u.UserID,
			"task_type": u.TaskType, "provider": u.Provider, "model": u.Model,
			"quantity": u.Quantity, "cost_points": s.Store.PointsFromTokens(u.Cost),
			"biz_kind": u.BizKind, "biz_mode": u.BizMode, "created_at": u.CreatedAt,
		})
	}
	return out
}

// ticketJSON 工单出参视图：tokens_billed（内部计费 token）折成 points_billed，裸值不再外发。
// 参数 t: 工单（nil 返回空对象）。其余列随 json tag 自动透出。
func (s *Server) ticketJSON(t *store.Ticket) map[string]interface{} {
	if t == nil {
		return map[string]interface{}{}
	}
	b, err := json.Marshal(t)
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]interface{}{}
	}
	delete(m, "tokens_billed")
	m["points_billed"] = s.Store.PointsFromTokens(t.TokensBilled)
	return m
}

// ticketsViewJSON 工单列表出参视图（逐条折积分）。
func (s *Server) ticketsViewJSON(list []*store.Ticket) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(list))
	for _, t := range list {
		out = append(out, s.ticketJSON(t))
	}
	return out
}
