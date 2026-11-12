// ============ sales_role_api.go · 职责说明 ============
// 三期 W3 销售角色设置 / 维护页的**管理面**（/api/admin/sales/roles*、/api/admin/sales/ops*）。
//
// ★ 与 sales_api.go 的分工（硬口径 3）：本文件**不碰** sales_api.go 四端点，
//   而是把 A09 收紧实现为「store 侧鉴权真值 + 本文件导出可调用的鉴权助手 salesRoleGate」，
//   由主 agent 在 sales_api.go 四端点函数体开头各补一行调用（函数签名与插入位置见报告）。
//
// 鉴权口径：
//  - 管理面四条路由一律 ≥3（salesConfigAdmin → requireTenantAdmin，唯一强闸入口，
//    与 A07 的 4 层 BFS 同源，不新开第二套）；
//  - salesRoleGate 的权限项从 store.PermForEndpoint(r.URL.Path) **现推**，api 侧零角色/权限字面，
//    杜绝散落多份 if；未登记路径 fail-closed 拒绝。
//
// 错误一律 apierrors.New；日志只用 observability.Info / Warn；中文错误串原文已登记（见报告）。
// ================================================
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
)

// salesOpsConfigKeys 维护页开关绑定的后端**真实配置键**全集（A12 双向比对的后端半边）。
// ★ W5 收口校准（改方案留痕）：键名必须与各能力**实际读的键**逐字一致，不许两套名字——
//   W4 守卫读 sales_facts_injection_enabled / sales_claim_guard_enabled（assist_sales_facts.go:99），
//   W5 外联读 outbound_enabled（sales_outbound_api.go）；freq_cap_per_day 是数值型，
//   走 /api/admin/sales/outbound/config 的专用白名单（本面只收 0/1 布尔键）。
//   既有两把闸 sales_engine_enabled / billing_enforced 沿用二期。
var salesOpsConfigKeys = []string{
	"sales_engine_enabled", "billing_enforced",
	"sales_facts_injection_enabled", "sales_claim_guard_enabled",
	"outbound_enabled",
}

// isSalesOpsConfigKey 判断配置键是否在维护页白名单内（写口只认登记键，防越权写全局配置）。
// 参数：key=待判键名；返回：是否登记。
func isSalesOpsConfigKey(key string) bool {
	for _, k := range salesOpsConfigKeys {
		if k == key {
			return true
		}
	}
	return false
}

// routesSalesRole 注册 W3 管理面路由（★ 主 agent 在 sales_api.go 的 routesSales() 内补一行调用）。
// 参数：无；返回：无。
func (s *Server) routesSalesRole() {
	s.mux.HandleFunc("/api/admin/sales/roles", s.handleSalesRolesGet)
	s.mux.HandleFunc("/api/admin/sales/roles/save", s.handleSalesRolesSave)
	s.mux.HandleFunc("/api/admin/sales/ops", s.handleSalesOpsGet)
	s.mux.HandleFunc("/api/admin/sales/ops/save", s.handleSalesOpsSave)
}

// salesRoleGate 销售域端点鉴权助手（★ A09 收紧的唯一入口；主 agent 在四个端点各补一行调用）。
// 权限项从 store.PermForEndpoint(r.URL.Path) 现推：读类 ≥2、转正审批 ≥3，部门管理员合法读口不关。
// 参数：w=响应；r=请求；返回：当前用户、生效租户、是否继续（false 表示已写出 401/403）。
func (s *Server) salesRoleGate(w http.ResponseWriter, r *http.Request) (*store.User, int64, bool) {
	u := s.authUser(r)
	if u == nil {
		s.writeAuthzError(w, r, errNotLogin)
		return nil, 0, false
	}
	permKey := store.PermForEndpoint(r.URL.Path)
	tid := s.effTenant(r, u)
	if permKey == "" || !s.Store.SalesRoleAllowed(tid, auth.RoleLevel(u.Role), permKey) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "权限不足"))
		return nil, 0, false
	}
	return u, tid, true
}

// handleSalesRolesGet 回读行式权限矩阵 + 权限项全集 + 等级枚举 + 端点全集（≥3 管理面）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回：无。
func (s *Server) handleSalesRolesGet(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	rows, err := s.Store.ListSalesRolePerms(tid)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// 回传**完整** 4×10 行式矩阵：落库行优先，未落库项由出厂缺省补齐。
	// 前端只显示后端真值，不许自己抄第二份缺省清单（单一真值口径）。
	stored := make(map[store.SalesRolePermRow]bool, len(rows))
	for _, row := range rows {
		stored[store.SalesRolePermRow{RoleLevel: row.RoleLevel, PermKey: row.PermKey}] = row.Allowed
	}
	matrix := make([]store.SalesRolePermRow, 0, 4*len(store.AllSalesPermKeys()))
	for _, lvl := range []int{1, 2, 3, 4} {
		for _, perm := range store.AllSalesPermKeys() {
			row := store.SalesRolePermRow{RoleLevel: lvl, PermKey: perm}
			allowed, ok := stored[row]
			if !ok {
				allowed = store.DefaultSalesRoleAllowed(lvl, perm)
			}
			row.Allowed = allowed
			matrix = append(matrix, row)
		}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":   true,
		"perm_keys": store.AllSalesPermKeys(),
		"levels":    []int{1, 2, 3, 4},
		"rows":      rows,
		"matrix":    matrix,
		"endpoints": store.SalesEndpointPaths(),
	})
}

// handleSalesRolesSave 逐项保存一档 RoleLevel × perm 的 allowed（唯一写口，留审计）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回：无。
func (s *Server) handleSalesRolesSave(w http.ResponseWriter, r *http.Request) {
	u, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body struct {
		RoleLevel int    `json:"role_level"`
		PermKey   string `json:"perm_key"`
		Allowed   bool   `json:"allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
		return
	}
	if !store.SalesPermExists(body.PermKey) || body.RoleLevel < 1 || body.RoleLevel > 4 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "权限项或角色等级不合法"))
		return
	}
	if err := s.Store.SaveSalesRolePerm(tid, body.RoleLevel, body.PermKey, body.Allowed); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "sales_role_perm_save", "sales_role_perm", body.PermKey)
	observability.Info(r.Context(), "销售角色权限已保存",
		"tenant_id", tid, "level", body.RoleLevel, "perm", body.PermKey, "allowed", body.Allowed)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleSalesOpsGet 读维护页开关的后端真实配置值（新键取不到出空串，前端按未接线显示 half）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回：无。
func (s *Server) handleSalesOpsGet(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	values := make(map[string]string, len(salesOpsConfigKeys))
	for _, k := range salesOpsConfigKeys {
		v, err := s.Store.GetConfig(k)
		if err != nil {
			v = ""
		}
		values[k] = v
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"keys":    salesOpsConfigKeys,
		"values":  values,
	})
}

// handleSalesOpsSave 保存单个开关（只认白名单键与 0/1 值，留审计）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回：无。
func (s *Server) handleSalesOpsSave(w http.ResponseWriter, r *http.Request) {
	u, _, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
		return
	}
	body.Key = strings.TrimSpace(body.Key)
	if !isSalesOpsConfigKey(body.Key) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "未登记的维护开关键"))
		return
	}
	if body.Value != "0" && body.Value != "1" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "开关值只能是 0 或 1"))
		return
	}
	if err := s.Store.SetConfig(body.Key, body.Value); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "sales_ops_switch_save", "sales_ops", body.Key)
	observability.Info(r.Context(), "销售维护开关已保存", "key", body.Key, "value", body.Value)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
