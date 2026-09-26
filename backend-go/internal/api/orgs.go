// ============ orgs.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
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
	"log"
	"net/http"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// routesOrgs 注册组织管理路由。
func (s *Server) routesOrgs() {
	s.mux.HandleFunc("/api/admin/orgs", s.handleOrgList)
	s.mux.HandleFunc("/api/admin/orgs/create", s.handleOrgCreate)
	s.mux.HandleFunc("/api/admin/orgs/rename", s.handleOrgRename)
	s.mux.HandleFunc("/api/admin/orgs/move", s.handleOrgMove)
	s.mux.HandleFunc("/api/admin/orgs/token-limit", s.handleOrgTokenLimit) // ★ 部门预算分配（四期）
	s.mux.HandleFunc("/api/admin/org-budget", s.handleOrgBudgetSummary)    // ★ 预算总览（∑部门预算=总预算）
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
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
			// F-64②：本租户根组织行读取失败是存储侧故障（500）。旧写法回 200＋success:false，
			// 部门管理员的组织面板会把它当「加载成功但没数据」渲染成空树，运维也查不到 trace_id。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		orgIDs, err := s.Store.OrgDescendantIDs(tid, u.OrgID)
		if err != nil {
			// F-64②：子孙组织集合要遍历 orgs 表，读失败同样是存储侧故障（500）；
			// 这里不能退化成「返回空集合继续渲染」，那会让部门管理员看到一棵假的空树。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		all, err := s.Store.ListOrgs(tid)
		if err != nil {
			// F-64②：祖先链补全依赖的全量组织列表读取失败（500）——同上，失败必须是失败。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
	// 超级管理员：按生效租户切分为「平台视图」或「该租户视图」
	if auth.IsSuperAdmin(u) {
		tid := s.effTenant(r, u)
		if tid > 0 {
			// 指定租户→展示该租户的组织树（同租管视图）
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
				// F-64②：超管切到指定租户看组织树，列表读取失败是存储侧故障（500）；
				// 回 200 会让管理台把「读挂了」显示成「该租户没有部门」，误导跨租户排障。
				s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
				return
			}
			writeJSON(w, 200, map[string]interface{}{"success": true, "orgs": orgs, "root": root, "tenant_id": tid, "platform": false})
			return
		}
		// 平台上下文（tid=0）→ 展示平台组织树（所有租户）
		root, err := s.Store.EnsurePlatformRootOrg("能言")
		if err != nil {
			// F-64②：Ensure 是「查不到就建」，失败只可能是读写平台根组织行出错（500），
			// 不是权限问题——能走到这里说明已过 requireDeptAdmin 与超管判定。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
			// F-64②：平台全量组织视图（INNER JOIN tenants）读取失败＝存储侧故障（500）。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
		// F-64②：租户管理员兜底视图（非超管、非部门管理员）的组织列表读取失败＝存储侧故障（500）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
	// 层级设置权限：仅超管（任意租户）与租户管理员（本租户）；部门管理员不动结构
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
		// ★ 脱敏（2026-09-12）：驱动错误不透吐
		if store.IsUniqueViolation(err) {
			// F-64②：同级重名是「资源状态冲突」（orgs 上有 idx_orgs_sibling_unique
			// 唯一索引）——载荷本身没错，改个名字重发即可，故 409 而非 200 也不是 400：
			// 前端据此把焦点留在名称输入框，而不是当成服务端故障去重试同一个名字。
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "创建失败：同级下已存在同名组织"))
			return
		}
		log.Printf("[orgs] 创建组织失败 tid=%d name=%s: %v", tid, req.Name, err)
		// F-64②：非重名的建组织失败（驱动/连接级）才是真·服务端出错（500），
		// 原文已按下面的 log.Printf 落日志，响应体只回 DebriefDBError 的安全文案。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "创建失败: "+store.DebriefDBError(err)))
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
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 解析 id/name 并做组织归属（orgTenant）+ 层级合法性（validateOrg）双闸口后改名；根组织同步租户名见下
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
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	if err := s.validateOrg(tid, req.ID); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	if err := s.Store.RenameOrg(req.ID, strings.TrimSpace(req.Name)); err != nil {
		// F-64②：改名同样受 idx_orgs_sibling_unique 约束——撞到「同级重名」是状态冲突（409），
		// 用户换个名字就好；其余（驱动/连接级）才是服务端出错（500），两者旧写法都是 200。
		if store.IsUniqueViolation(err) {
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, publicErrMessage(r.Context(), err)))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// 根组织改名同步租户名（保持组织树与租户列表一致）
	if org, e := s.Store.GetOrgByID(req.ID); e == nil && org.Type == store.OrgTypeRoot && org.TenantID > 0 && s.Ten != nil {
		if tn, e2 := s.Ten.GetByID(org.TenantID); e2 == nil && tn != nil {
			_ = s.Ten.Update(org.TenantID, strings.TrimSpace(req.Name), tn.ExpiresAt, tn.Permissions)
		}
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
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 解析 id/parent_id 并确认组织归属当前租户；防成环等层级校验由 store 侧 MoveOrg 完成
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
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	if err := s.Store.MoveOrg(tid, req.ID, req.ParentID); err != nil {
		// F-64②：移动失败的语义很杂（跨租户／目标父节点不存在／成环／根组织不可动），
		// 一律回 200 会让前端无法区分「拖错了地方」与「服务挂了」，这里按语义分档。
		s.writeError(w, r, orgMutationError(r, err))
		return
	}
	// ★ F-63（2026-09-26 批 I-3）：detail 原为空串——组织树结构调整（谁挂到谁下面）正是
	//   「部门额度/权限范围为何变了」的回查依据，必须落两个 id。
	s.Store.LogAudit(tid, u.ID, "org_move", "orgs",
		fmt.Sprintf("组织 #%d 移至父节点 #%d", req.ID, req.ParentID))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOrgDelete 删除组织接口（子孙组织上移、用户回收至根组织）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 表示删除成功。
func (s *Server) handleOrgDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 解析 id 并过组织归属 + validateOrg 双闸口后删除；子组织上移/成员回收由 store 侧 DeleteOrg 处理
	var req struct {
		ID int64 `json:"id"` // 组织 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "参数错误"})
		return
	}
	tid, err := s.orgTenant(r, u, req.ID)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	if err := s.validateOrg(tid, req.ID); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// ★ F-63（2026-09-26 批 I-3）：删除前先取节点名与直属子节点数——删除后这些信息就没了，
	//   旧实现 detail 为空串，事后既不知道删的是哪个部门，也不知道多少子节点被上移。
	delName, childCount := "", 0
	if o, e := s.Store.GetOrgByID(req.ID); e == nil && o != nil {
		delName = o.Name
	}
	if all, e := s.Store.ListOrgs(tid); e == nil {
		for _, o := range all {
			if o != nil && o.ParentID == req.ID {
				childCount++
			}
		}
	}
	if err := s.Store.DeleteOrg(req.ID); err != nil {
		// F-64②：删除失败沿用与移动同一套语义分档（跨租户 403／成环守卫 400／
		// 根组织不可删 409／存储写入故障 500），旧写法全部混成 200。
		s.writeError(w, r, orgMutationError(r, err))
		return
	}
	s.Store.LogAudit(tid, u.ID, "org_delete", "orgs",
		auditDelete("组织节点", truncateRunes(delName, 40), req.ID, 1)+
			fmt.Sprintf("｜直属子节点上移 %d 个", childCount))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOrgUsers 组织下用户视图接口：按组织及其子孙组织归集用户。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（query: org_id；0/缺省=根组织全部用户）。
// 返回: success=true 时携带 users 数组（含所属组织名 org_name）。
func (s *Server) handleOrgUsers(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
			// F-64②：超管平台视图跨租户列账号，读失败是存储侧故障（500）。
			// 这里不是「没登录」——上面 requireDeptAdmin 已经放行，绝不能回 401
			// （前端 core.ts 的 handleUnauthorized 只挂 401，会把在线用户踢回登录页）。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
			return
		}
		orgIDs, err = s.Store.OrgDescendantIDs(tid, orgID)
		if err != nil {
			// F-64②：子孙集合靠 orgs 全量查询算，读失败＝存储侧故障（500）。
			// 注意此处不能借 401/403 表达——权限与归属在上面的 validateOrg 已判过，走到这里只可能是库坏了。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
	}
	users, err := s.Store.ListUsersByOrg(tid, orgIDs)
	if err != nil {
		// F-64②：按组织子树列成员失败＝存储侧故障（500），旧写法回 200 会被面板渲染成「该部门没有成员」。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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

// orgMutationError 组织结构性写操作（移动/删除）失败时的错误语义分档。
// 为什么必须分档而不是一律 500：MoveOrg/DeleteOrg 的失败绝大多数不是「服务端出错」，
// 而是本层挡得住的业务事实——跨租户访问（403）、目标节点不存在（404）、
// 新父节点在被移节点子树内＝父节点不合法（400）、根组织不可移/不可删＝资源状态冲突（409）；
// 全塞进 500 会让前端与运维照着 trace_id 去查一个根本没坏的进程。
// 为什么判据取 publicErrMessage 的对外文案而不是 err.Error()：
// iam 侧这些业务错误是中文话术、经脱敏后原样透出，两者同源；
// 驱动级错误（含 sql: no rows）脱敏成「服务处理出现异常」，落到兜底 500，
// 于是「状态码」与「用户看到的那句话」永远一致，不会出现「404 + 系统繁忙」的自相矛盾应答。
// 本文件这些接口都是已登录的管理台入口，绝不用 401（401 会触发前端清登录态）。
func orgMutationError(r *http.Request, err error) *apierrors.APIError {
	msg := publicErrMessage(r.Context(), err)
	switch {
	case strings.Contains(msg, "不属于当前租户"):
		// 组织或目标父节点归属别的租户：越权语义（403），改请求参数无法绕过
		return apierrors.New(apierrors.ErrForbidden, msg)
	case strings.Contains(msg, "不存在"):
		// 明确写了「不存在」的业务文案：目标节点查不到（404）
		return apierrors.New(apierrors.ErrNotFound, msg)
	case strings.Contains(msg, "自身或其子组织"):
		// 成环：新父节点落在被移动节点自己的子树里，属父节点不合法（400）
		return apierrors.New(apierrors.ErrValidation, msg)
	case strings.Contains(msg, "根组织不可"):
		// 根组织（=租户本身）不可移动/不可删除：载荷没错，错在对象当前状态（409）
		return apierrors.New(apierrors.ErrConflict, msg)
	default:
		// 剩下才是真·服务端出错（500）：驱动/事务/连接级失败
		return apierrors.New(apierrors.ErrInternal, msg)
	}
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

// handleOrgTokenLimit 设置部门月度积分预算（租户管理员及以上；0=关闭该部门的部门墙）。
// 约束：∑部门预算 = 租户总预算由面板语义保证（每次调整即重排构成），后端仅做非负校验。
// 入参为积分（内部 token 记账，2026-09-19 起换算不外露）。
func (s *Server) handleOrgTokenLimit(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		OrgID       int64 `json:"org_id"`       // 组织 ID
		LimitPoints int64 `json:"limit_points"` // 月度积分预算（≥0）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgID <= 0 || req.LimitPoints < 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	o, oerr := s.Store.GetOrgByID(req.OrgID)
	if oerr != nil || o.TenantID != s.effTenant(r, u) {
		writeJSON(w, 404, map[string]interface{}{"success": false, "message": "组织不存在"})
		return
	}
	limit := s.Store.TokensFromPoints(req.LimitPoints)
	if err := s.Store.SetOrgTokenLimit(req.OrgID, limit); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "org_token_limit", "orgs",
		fmt.Sprintf("%d limit=%d", req.OrgID, limit))
	// 同步预算总览给前端（积分口径；总预算=∑部门预算、全租户本月已用）
	sum, _ := s.Store.GetOrgBudgetSummary(s.effTenant(r, u))
	writeJSON(w, 200, map[string]interface{}{"success": true, "summary": s.orgBudgetViewJSON(sum)})
}

// orgBudgetViewJSON 部门预算总览折积分出参（内部 store 结构保持 token）。
func (s *Server) orgBudgetViewJSON(sum *store.OrgBudgetSummary) map[string]interface{} {
	out := map[string]interface{}{"total_limit_points": int64(0), "used_this_month_points": int64(0), "depts": []map[string]interface{}{}}
	if sum == nil {
		return out
	}
	out["total_limit_points"] = s.Store.PointsFromTokens(sum.TotalLimit)
	out["used_this_month_points"] = s.Store.PointsFromTokens(sum.UsedThisMonth)
	depts := []map[string]interface{}{}
	for _, d := range sum.Depts {
		depts = append(depts, map[string]interface{}{
			"org_id": d.OrgID, "name": d.Name,
			"limit_points": s.Store.PointsFromTokens(d.TokenLimit),
			"used_points":  s.Store.PointsFromTokens(d.UsedThisMonth),
		})
	}
	out["depts"] = depts
	return out
}

// handleOrgBudgetSummary 部门预算总览接口（租管面板展示：各部门预算/已用 + 总预算；积分口径）。
func (s *Server) handleOrgBudgetSummary(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	sum, err := s.Store.GetOrgBudgetSummary(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "summary": s.orgBudgetViewJSON(sum)})
}
