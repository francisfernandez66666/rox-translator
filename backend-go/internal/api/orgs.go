package api

// ============ 本文件职责中文说明 ============
// 组织层级管理 HTTP 接口（管理结构展示层，根组织=租户）：
//   - 组织列表（超管按生效租户，租户管理员限本租户）
//   - 创建子组织/部门（handleOrgCreate）
//   - 重命名组织（handleOrgRename）
//   - 删除组织（handleOrgDelete，子孙上移、用户回收）
//   - 组织下用户视图（handleOrgUsers，含子孙组织归集，供超管/租户管理员按组织下钻）
// 权限：租户管理员及以上；租户隔离（组织必须属于生效租户）。
// =============================================

import (
	"encoding/json"
	"net/http"
	"strings"

	"translator/internal/store"
)

// routesOrgs 注册组织管理路由。
func (s *Server) routesOrgs() {
	s.mux.HandleFunc("/api/admin/orgs", s.handleOrgList)
	s.mux.HandleFunc("/api/admin/orgs/create", s.handleOrgCreate)
	s.mux.HandleFunc("/api/admin/orgs/rename", s.handleOrgRename)
	s.mux.HandleFunc("/api/admin/orgs/move", s.handleOrgMove)
	s.mux.HandleFunc("/api/admin/orgs/delete", s.handleOrgDelete)
	s.mux.HandleFunc("/api/admin/orgs/users", s.handleOrgUsers)
}

// handleOrgList 组织列表接口（扁平列表，前端组装树）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（需租户管理员及以上）。
// 返回: success=true 时携带 orgs 数组、root（根组织行，名称可自定义）与 tenant_id。
func (s *Server) handleOrgList(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	// 确保根组织行存在（首次或迁移后），名称默认租户名
	root, err := s.Store.GetRootOrg(tid)
	if err != nil {
		rootName := ""
		if s.Ten != nil {
			if tn, e := s.Ten.GetByID(tid); e == nil {
				rootName = tn.Name
			}
		}
		if rootName == "" {
			rootName = "组织"
		}
		if r, e := s.Store.EnsureRootOrg(tid, rootName); e == nil {
			root = r
		}
	}
	orgs, err := s.Store.ListOrgs(tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "orgs": orgs, "root": root, "tenant_id": tid})
}

// handleOrgCreate 创建组织/部门接口。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 name/parent_id/type）。
// 返回: success=true 时携带新建组织对象。
// 类型规则：支持任意深度层级——组织(org)下可再建组织(org)或部门(dept)；
// parent_id=0 表示挂在根组织下；type 空则按 parent 推断（根下=组织，否则=部门）。
func (s *Server) handleOrgCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Name     string `json:"name"`      // 组织/部门名称（必填）
		ParentID int64  `json:"parent_id"` // 父节点 ID（0=根组织下）
		Type     string `json:"type"`      // 类型：org(组织)/dept(部门)，空则按 parent 推断
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "组织名称不能为空"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	tid := s.effTenant(r, u)
	// 父节点归属校验（非根时父节点必须属于本租户）
	if req.ParentID > 0 {
		if err := s.validateOrg(tid, req.ParentID); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	// 类型推断与校验
	orgType := req.Type
	if orgType == "" {
		if req.ParentID == 0 {
			orgType = store.OrgTypeOrg
		} else {
			orgType = store.OrgTypeDept
		}
	}
	if orgType != store.OrgTypeOrg && orgType != store.OrgTypeDept {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "组织类型仅支持 org(组织)/dept(部门)"})
		return
	}
	org, err := s.Store.CreateOrg(tid, req.ParentID, req.Name, orgType)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "org_create", "orgs", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "org": org})
}

// handleOrgRename 重命名组织接口。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/name）。
// 返回: success=true 表示重命名成功。
func (s *Server) handleOrgRename(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID   int64  `json:"id"`   // 组织 ID
		Name string `json:"name"` // 新名称
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "参数错误"})
		return
	}
	tid := s.effTenant(r, u)
	if err := s.validateOrg(tid, req.ID); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if err := s.Store.RenameOrg(req.ID, strings.TrimSpace(req.Name)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "org_rename", "orgs", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOrgMove 调整组织/部门层级接口（拖拽改父级）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/parent_id）。
// 返回: success=true 表示移动成功（含成环/根组织/租户归属校验）。
func (s *Server) handleOrgMove(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID       int64 `json:"id"`        // 被移动组织 ID
		ParentID int64 `json:"parent_id"` // 目标父节点 ID（0=根组织下）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "参数错误"})
		return
	}
	tid := s.effTenant(r, u)
	if err := s.Store.MoveOrg(tid, req.ID, req.ParentID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "org_move", "orgs", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOrgDelete 删除组织接口（子孙组织上移、用户回收至根组织）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 表示删除成功。
func (s *Server) handleOrgDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 组织 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "参数错误"})
		return
	}
	tid := s.effTenant(r, u)
	if err := s.validateOrg(tid, req.ID); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if err := s.Store.DeleteOrg(req.ID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "org_delete", "orgs", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOrgUsers 组织下用户视图接口：按组织及其子孙组织归集用户。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（query: org_id；0/缺省=根组织全部用户）。
// 返回: success=true 时携带 users 数组（含所属组织名 org_name）。
func (s *Server) handleOrgUsers(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	// 解析 org_id：缺省/0 表示根组织（租户全部用户）
	orgID := int64(0)
	if v := r.URL.Query().Get("org_id"); v != "" {
		oid, perr := parseInt64(v)
		if perr == nil && oid > 0 {
			orgID = oid
		}
	}
	// 计算组织及其子孙 ID 集合
	orgIDs := []int64{}
	if orgID > 0 {
		if err := s.validateOrg(tid, orgID); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		orgIDs, err = s.Store.OrgDescendantIDs(tid, orgID)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	users, err := s.Store.ListUsersByOrg(tid, orgIDs)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 补充组织名（前端展示树形归属）
	orgs, _ := s.Store.ListOrgs(tid)
	orgName := map[int64]string{}
	for _, o := range orgs {
		orgName[o.ID] = o.Name
	}
	type orgUser struct {
		*store.User
		OrgName string `json:"org_name"`
	}
	out := make([]orgUser, 0, len(users))
	for _, usr := range users {
		out = append(out, orgUser{User: usr, OrgName: orgName[usr.OrgID]})
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "users": out, "org_id": orgID})
}
