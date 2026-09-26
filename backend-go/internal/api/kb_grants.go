// ============ kb_grants.go · 职责说明 ============
// api 包内部实现文件：KB 包级读/写/管理授权（H3 权限矩阵）。
// =============================================
// ============================================================================
// H3 KB 包级权限矩阵 API：读/写/管理三级授权的鉴权辅助 + 授权管理端点 +
// 当前用户授权查询（前端导航门控）。
// 规则：部门管理员及以上对租户内包天然具备 manage（维持存量行为）；
// 普通成员仅当持有目标包 ≥ 所需级别的授权时放行对应操作。
// ============================================================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// kbPackAllowed 判定 u 对包 pkgID 是否具备 need 级别（read/write/manage）。
func (s *Server) kbPackAllowed(r *http.Request, u *store.User, need string, pkgID int64) bool {
	if u == nil {
		return false
	}
	if auth.RoleLevel(u.Role) >= 2 {
		return true // 部门管理员及以上：全租户包 manage（现状）
	}
	if pkgID <= 0 {
		return false // 未定位到具体包：无包级授权可言
	}
	got := s.Store.KBPackRoleOf(s.kbTenant(r, u), pkgID, u.ID)
	return store.KBRoleRank(got) >= store.KBRoleRank(need)
}

// requireKBPackPerm 普通成员的包级校验（部门管理员请在调用方先行短路）。
// 返回 nil 表示放行；错误消息可直接回 403。
func (s *Server) requireKBPackPerm(r *http.Request, u *store.User, need string, pkgID int64) error {
	if s.kbPackAllowed(r, u, need, pkgID) {
		return nil
	}
	return &apiErr{"权限不足：需要该知识库包的" + needLabel(need) + "授权"}
}

// needLabel 级别中文化（错误提示用）。
func needLabel(need string) string {
	switch need {
	case "read":
		return "只读"
	case "write":
		return "编辑"
	case "manage":
		return "管理"
	}
	return need
}

// kbPackGrantedSkip ★ H3：普通成员（role<2）若持有目标包 ≥need 级授权，
// 跳过「包类型 + 部门范围」双重校验（受托维护语义）；部门管理员及以上
// 返回 false 走原有校验链，行为完全不变。
func (s *Server) kbPackGrantedSkip(r *http.Request, u *store.User, pkg *store.KBPackage, need string) bool {
	if u == nil || pkg == nil {
		return false
	}
	if auth.RoleLevel(u.Role) >= 2 {
		return false
	}
	return s.kbPackAllowed(r, u, need, pkg.ID)
}

// handleKBPackGrants GET /api/admin/kb-packages/grants?pack_id=N 列授权；
// POST {pack_id, user_id, role: read|write|manage|”(撤销)} 设置授权。
// 鉴权：部门管理员及以上，或对该包持 manage 授权者（被授权人不能越级再授权）。
func (s *Server) handleKBPackGrants(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		pkgID, _ := strconv.ParseInt(r.URL.Query().Get("pack_id"), 10, 64)
		u := s.authUser(r)
		if u == nil {
			writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
			return
		}
		if err := s.requireKBPackPerm(r, u, "manage", pkgID); err != nil {
			// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
			s.writeAuthzError(w, r, err)
			return
		}
		list, err := s.Store.ListKBPackGrants(s.kbTenant(r, u), pkgID)
		if err != nil {
			// F-64②：授权清单读取失败是本进程/存储侧故障（500）。旧写法回 200＋success:false，
			// 包管理弹窗会把它当成「这个包还没人授权」渲染成空列表，管理员以为配置丢了。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "grants": list})
		return
	}
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// POST 授权/撤销：解析 body 后依次过 manage 闸口、包存在性、role 白名单（空串=撤销）
	var req struct {
		PackID int64  `json:"pack_id"`
		UserID int64  `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackID <= 0 || req.UserID <= 0 {
		// F-64②：载荷不合法（body 解不开或 pack_id/user_id 缺）＝参数错（400）。
		// 不用 401：本 handler 的未登录在上面的 authUser 分支已单独处理，
		// 401 会触发前端 handleUnauthorized 清登录态，把已登录的管理员踢回登录页。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "参数错误：pack_id/user_id 必填"))
		return
	}
	if err := s.requireKBPackPerm(r, u, "manage", req.PackID); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	tid := s.kbTenant(r, u)
	pkg, gErr := s.Store.GetKBPackage(req.PackID, tid)
	if gErr != nil || pkg == nil {
		// F-64②：包在本租户下取不到＝记录不存在（404，知识库包专用码 ErrKBNotFound）。
		// 为什么不是 403：租户隔离口径下「别人的包」与「没有这个包」必须给同一个 404，
		// 回 403 等于承认该包 ID 存在，会泄露跨租户资源清单。
		s.writeError(w, r, apierrors.New(apierrors.ErrKBNotFound, "知识库包不存在"))
		return
	}
	role := strings.TrimSpace(strings.ToLower(req.Role))
	if role != "" && store.KBRoleRank(role) == 0 {
		// F-64②：role 不在 read/write/manage 白名单里（空串是合法的「撤销」，已在上面短路）
		// 是纯载荷取值错误（400）——改对 role 重发即可，与权限、记录存在性都无关。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "role 仅支持 read/write/manage（空串=撤销）"))
		return
	}
	// 目标用户须属本租户（超管宿主租户 tid<=0 时按用户自身租户兜底）
	tu, uErr := s.Store.GetUser(req.UserID, tid)
	if uErr != nil || tu == nil {
		// F-64②：被授权人查不到（含「存在但属别的租户」）＝记录不存在（404），
		// 与上一条同口径：租户隔离下不许用 403 泄露「这个用户属于别的租户」。
		// 真正的授权写入失败在下面的 500 分支，两者语义不能混。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "目标用户不存在或不属于当前租户"))
		return
	}
	var err error
	if role == "" {
		err = s.Store.RevokeKBPack(tid, req.PackID, req.UserID)
	} else {
		err = s.Store.GrantKBPack(tid, req.PackID, req.UserID, role)
	}
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	s.Store.LogAudit(tid, u.ID, "kb_pack_grant", "kb_packages",
		strings.TrimSpace(pkg.Name)+":"+req.Role+":"+tu.Username)
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "授权已更新"})
}

// handleKBPackMine GET /api/admin/kb-packages/mine：当前用户的包级授权清单
// （前端据此对 KB 管理导航做门控；部门管理员及以上返回 role_level 由前端短路）。
func (s *Server) handleKBPackMine(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	list, err := s.Store.ListKBPackGrantsByUser(s.effTenant(r, u), u.ID)
	if err != nil {
		// F-64②：读取当前用户包级授权失败＝存储侧故障（500）。旧写法回 200 会让前端导航
		// 门控把「读挂了」当成「一条授权都没有」，普通成员的入口被整片藏掉且无任何提示。
		// 不用 401：未登录已在上面的 authUser 分支单独处理，这里回 401 会清掉在线用户的登录态。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	if list == nil {
		list = []*store.KBPackGrant{}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "grants": list, "role_level": auth.RoleLevel(u.Role),
	})
}
