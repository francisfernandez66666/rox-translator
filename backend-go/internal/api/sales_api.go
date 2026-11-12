// ============ sales_api.go · 职责说明 ============
// 二期 S2 销售引擎的 API 面：按能言姿势单文件平铺，鉴权在**函数体内**取人
// （route_auth_gate_test.go 按路由全集判，漏写即红；本文件不改那条闸，直接享它）。
// /api/sales/capability 只出三态状态词 off／half／on：
// ★ 二期 §8.1 断言 B2 的读数出口——只开一把闸时必须能读出「半套配置」，
//
//	不许呈现为「已生效」；公开面不许带内部计量裸值。
//
// 草稿 inbox 数据源（§9.4 前端契约的后端半边）：列表 /api/sales/drafts、
// 详情 /api/sales/draft?id=、唯一写动作 /api/sales/draft/approve（转正留审计
// sales_draft_approve）。★ 没有删除／批量／自动生效口——草稿转正只经
// store.ApproveSalesDraft 这一个函数（M1），产物恒为草稿态起步（M2）。
// ================================================================
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// 销售能力面的两把闸配置键（引擎总开关与强制计费闸，二期开启时必须同批开）。
const (
	// salesEngineSwitchKey 二期销售引擎总开关（默认关）。
	salesEngineSwitchKey = "sales_engine_enabled"
	// billingEnforcedKey 强制计费闸（现网既有键，与上一把必须同批开）。
	billingEnforcedKey = "billing_enforced"
)

// salesSwitchStatus 两把闸的三态读数：都关=off、只开一把=half、都开=on。
// 参数：engineOn=销售引擎开关；billingOn=强制计费开关；返回状态词。
func salesSwitchStatus(engineOn, billingOn bool) string {
	switch {
	case engineOn && billingOn:
		return "on"
	case engineOn || billingOn:
		return "half"
	default:
		return "off"
	}
}

// routesSales 注册销售引擎路由（由 routes() 调用）。
// 参数：无；返回：无。
func (s *Server) routesSales() {
	s.mux.HandleFunc("/api/sales/capability", s.handleSalesCapability)
	s.mux.HandleFunc("/api/sales/drafts", s.handleSalesDrafts)
	s.mux.HandleFunc("/api/sales/draft", s.handleSalesDraftDetail)
	s.mux.HandleFunc("/api/sales/draft/approve", s.handleSalesDraftApprove)
	// ★ 二期「能前端配置的都交给前端配置」：租户管理员的销售配置面（模型／流程引擎／策略／词表）。
	s.routesSalesConfig()
	// ★ 三期 W2 六包接线：归因与收件箱的只读管理面（两个子注册函数此前只定义未调用＝死代码，
	// 旧版本注释写着「由 routesSales 调用」但这里从未调——§25-c「闸门绿着而那条腿从没被走到」形态，本行补上）。
	s.routesSalesAttribution()
	s.routesSalesInbox()
	// ★ 三期 W2 六包接线（本批）：报价窄管理面（AcceptQuote 唯一转正写口）＋流程状态机只读排障面。
	s.routesSalesQuote()
	s.routesSalesFlow()
	// ★ 三期 W3：角色权限矩阵与维护开关管理面（读 ≥3，写 ≥3；A10 白名单见 route_auth_gate_test.go）。
	s.routesSalesRole()
	// ★ 三期 W5：外联合规管理面 + 匿名退订端点（退订带 HMAC token，公开理由见白名单）。
	s.routesSalesOutbound()
	// ★ 三期 W6：CRM 管理面（线索/收件箱/转人工/漏斗扩段，统一 ≥3）。
	s.routesSalesCrm()
}

// handleSalesCapability 返回当前租户两把闸的三态状态词（★ W3/A09 收紧：权限走 salesRoleGate 单一真值，
// 状态词读类 ≥2；普通 user 不再有合法读口——半套配置对无权限者不外露）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回：无。
func (s *Server) handleSalesCapability(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesRoleGate(w, r)
	if !ok {
		return
	}
	engineRaw, _ := s.Store.GetConfig(salesEngineSwitchKey)
	billingRaw, _ := s.Store.GetConfig(billingEnforcedKey)
	engineOn := engineRaw == "1"
	billingOn := billingRaw == "1"
	writeJSON(w, 200, map[string]interface{}{
		"success":          true,
		"tenant_id":        tid,
		"status":           salesSwitchStatus(engineOn, billingOn),
		"engine_enabled":   engineOn,
		"billing_enforced": billingOn,
	})
}

// handleSalesDrafts 列本租户的说法草稿（只出草稿与已转正，不自动生效）。
// ★ W3/A09 收紧：读类 ≥2（PermForEndpoint 现推），权限判定单一真值。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（可用 ?status= 过滤）。返回：无。
func (s *Server) handleSalesDrafts(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesRoleGate(w, r)
	if !ok {
		return
	}
	list, err := s.Store.ListSalesDrafts(tid, r.URL.Query().Get("status"))
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "drafts": list, "status": "drafts"})
}

// handleSalesDraftDetail 按 (?id=) 读本租户一条草稿（跨租户/不存在一律 404，不泄露存在性）。
// ★ W3/A09 收紧：读类 ≥2（PermForEndpoint 现推），权限判定单一真值。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回：无。
func (s *Server) handleSalesDraftDetail(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesRoleGate(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少草稿 id"))
		return
	}
	d, err := s.Store.GetSalesDraft(tid, id)
	if err != nil {
		if errors.Is(err, store.ErrSalesDraftNotFound) {
			s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "草稿不存在"))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "draft": d})
}

// handleSalesDraftApprove 人审转正（唯一写动作，留审计动作 sales_draft_approve）。
// ★ W3/A09 收紧：转正审批 perm ≥3（tenant_admin 档）——普通 user 不许再碰写动作（修复复核 §4 的安全缺口）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。返回：无。
func (s *Server) handleSalesDraftApprove(w http.ResponseWriter, r *http.Request) {
	u, tid, ok := s.salesRoleGate(w, r)
	if !ok {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少草稿 id"))
		return
	}
	if err := s.Store.ApproveSalesDraft(tid, req.ID); err != nil {
		// 只有「真不存在／状态不允许」才 404；库故障不许伪装成 not-found（§一·8）
		if errors.Is(err, store.ErrSalesDraftNotFound) {
			s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "草稿不存在或状态不允许转正"))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "sales_draft_approve", "sales_draft", req.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": req.ID, "status": "approved"})
}
