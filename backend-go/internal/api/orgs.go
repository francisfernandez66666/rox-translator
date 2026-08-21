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
	"fmt"
	"net/http"
	"strings"

	"translator/internal/auth"
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
// 超级管理员：返回平台组织树（平台根 + 各租户根组织 + 各租户内部组织），
//
//	由 ListPlatformOrgs 组装；同时返回平台根组织。
//
// 租户管理员：返回本租户组织树。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（需租户管理员及以上）。
// 返回: success=true 时携带 orgs 数组、root（根组织行）与 tenant_id。
func (s *Server) handleOrgList(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 部门管理员：仅本部门及子部门组织树（只读视图）
	if auth.RoleLevel(u.Role) == 2 {
		tid := s.effTenant(r, u)
		if u.OrgID <= 0 {
			writeJSON(w, 200, map[string]interface{}{"success": true, "orgs": []*store.Org{}, "root": nil, "tenant_id": tid, "platform": false})
			return
		}
		root, err := s.Store.GetRootOrg(tid)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		orgIDs, err := s.Store.OrgDescendantIDs(tid, u.OrgID)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		all, err := s.Store.ListOrgs(tid)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		orgSet := map[int64]bool{}
		for _, id := range orgIDs {
			orgSet[id] = true
		}
		// 包含祖先节点（用于前端展开完整路径）
		ancestors := map[int64]bool{}
		for _, o := range all {
			if orgSet[o.ID] {
				pid := o.ParentID
				for pid > 0 {
					ancestors[pid] = true
					parent := findOrgByID(all, pid)
					if parent == nil {
						break
					}
					pid = parent.ParentID
				}
			}
		}
		var filtered []*store.Org
		for _, o := range all {
			if orgSet[o.ID] || ancestors[o.ID] || o.Type == store.OrgTypeRoot {
				filtered = append(filtered, o)
			}
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "orgs": filtered, "root": root, "tenant_id": tid, "platform": false})
		return
	}
	// 超级管理员：平台组织树（所有租户）
	if auth.IsSuperAdmin(u) {
		// 确保平台根组织存在
		root, err := s.Store.EnsurePlatformRootOrg("翻译助手")
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		// 确保所有租户都有根组织行（首次或迁移后）
		if s.Ten != nil {
			if tenants, e := s.Ten.List(); e == nil {
				for _, t := range tenants {
					if _, e2 := s.Store.GetRootOrg(t.ID); e2 != nil {
						_, _ = s.Store.EnsureRootOrg(t.ID, t.Name)
					}
				}
			}
		}
		orgs, err := s.Store.ListPlatformOrgs(root.ID)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "orgs": orgs, "root": root, "tenant_id": 0, "platform": true})
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
	writeJSON(w, 200, map[string]interface{}{"success": true, "orgs": orgs, "root": root, "tenant_id": tid, "platform": false})
}

// orgTenant 解析组织操作的目标租户：
//   - 超管：从目标组织归属的租户解析（平台视图下可按任意租户操作）
//   - 租户管理员：固定本租户
//
// 参数 r: HTTP 请求；u: 当前用户；orgID: 目标组织 ID（0=根组织，超管时需显式指定租户）。
// 返回: 目标租户 ID 与错误。
func (s *Server) orgTenant(r *http.Request, u *store.User, orgID int64) (int64, error) {
	// 租户管理员固定本租户
	if !auth.IsSuperAdmin(u) {
		return s.effTenant(r, u), nil
	}
	// 超管：若指定了组织，从组织归属租户解析
	if orgID > 0 {
		org, err := s.Store.GetOrgByID(orgID)
		if err != nil {
			return 0, fmt.Errorf("组织不存在")
		}
		return org.TenantID, nil
	}
	// 未指定组织（创建根级组织）：超管需显式指定租户，否则用 X-Tenant-ID
	tid := s.currentTenant(r)
	if tid <= 0 {
		tid = 1
	}
	return tid, nil
}

// handleOrgCreate 创建组织/部门接口。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 name/parent_id/type/tenant_id）。
// 返回: success=true 时携带新建组织对象。
// 类型规则：支持任意深度层级——组织(org)下可再建组织(org)或部门(dept)；
// parent_id=0 表示挂在根组织下；type 空则按 parent 推断（根下=组织，否则=部门）。
// 超管平台视图：parent_id 归属某租户则自动解析该租户；parent_id=0 时用 tenant_id 指定。
func (s *Server) handleOrgCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Name     string `json:"name"`      // 组织/部门名称（必填）
		ParentID int64  `json:"parent_id"` // 父节点 ID（0=根组织下）
		Type     string `json:"type"`      // 类型：org(组织)/dept(部门)，空则按 parent 推断
		TenantID int64  `json:"tenant_id"` // 归属租户（超管创建根级组织时指定）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "组织名称不能为空"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	tid := s.effTenant(r, u)
	// 部门管理员：只能在本部门子树内建子部门/子组织，不能建根级节点
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法创建"})
			return
		}
		if req.ParentID <= 0 {
			req.ParentID = u.OrgID // 缺省挂本部门下
		}
		inTree, e := s.Store.IsOrgInSubtree(tid, u.OrgID, req.ParentID)
		if e != nil || !inTree {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权在非本部门范围下创建子部门"})
			return
		}
	}
	// 超管平台视图：优先按 parent 归属租户解析；parent=0 且指定 tenant_id 时用该租户
	if auth.IsSuperAdmin(u) {
		if req.ParentID > 0 {
			if t, e := s.orgTenant(r, u, req.ParentID); e == nil {
				tid = t
			}
		} else if req.TenantID > 0 {
			tid = req.TenantID
		}
	}
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
	u, err := s.requireDeptAdmin(r)
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
	tid, err := s.orgTenant(r, u, req.ID)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if err := s.validateOrg(tid, req.ID); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 部门管理员：仅可重命名本部门子树内的节点
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法操作"})
			return
		}
		inTree, e := s.Store.IsOrgInSubtree(tid, u.OrgID, req.ID)
		if e != nil || !inTree {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权重命名非本部门的组织"})
			return
		}
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
	tid, err := s.orgTenant(r, u, req.ID)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
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
	tid, err := s.orgTenant(r, u, req.ID)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
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
	u, err := s.requireDeptAdmin(r)
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
	// 部门管理员：强制限定本部门子树（忽略请求中的 org_id 越权）
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 200, map[string]interface{}{"success": true, "users": []interface{}{}, "org_id": 0})
			return
		}
		orgID = u.OrgID
	}
	// 超管平台视图：从目标组织归属租户解析（跨租户下钻）
	if orgID > 0 && auth.IsSuperAdmin(u) {
		if t, e := s.orgTenant(r, u, orgID); e == nil {
			tid = t
		}
	}
	// 超管平台根视图（未选具体组织）：跨租户列出全部账号
	if auth.IsSuperAdmin(u) && tid <= 0 && orgID <= 0 {
		users, err := s.Store.ListAllUsers()
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		nameMap, _ := s.Store.OrgNameMap()
		type orgUser struct {
			*store.User
			OrgName string `json:"org_name"`
		}
		out := make([]orgUser, 0, len(users))
		for _, usr := range users {
			on := nameMap[usr.OrgID]
			if on == "" {
				on = "平台"
			}
			out = append(out, orgUser{User: usr, OrgName: on})
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "users": out, "org_id": 0})
		return
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

// findOrgByID 在组织切片中按 ID 查找（用于 dept_admin 祖先链构建）。
func findOrgByID(orgs []*store.Org, id int64) *store.Org {
	for _, o := range orgs {
		if o.ID == id {
			return o
		}
	}
	return nil
}
