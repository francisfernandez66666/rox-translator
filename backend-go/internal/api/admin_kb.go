// ============ admin_kb.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// KB 包管理（行业包）与条目、安全句维护（handleKBPackages / handleKBEntries / handleSafetyPhrases 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ========================================

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// canManagePackType 判断用户是否有权管理指定包类型的知识库包。
// 超管可管理全部类型（tenant/industry/locale/department/cross_dept）；
// 租户管理员可管理企业包、部门包、跨部门包；
// 部门管理员仅可管理部门包与跨部门包。
func canManagePackType(u *store.User, packType string) bool {
	if u != nil && auth.IsSuperAdmin(u) {
		return true
	}
	if auth.RoleLevel(u.Role) == 2 {
		// 部门管理员：仅部门包 / 跨部门包
		return packType == store.PackDepartment || packType == store.PackCrossDept
	}
	// 租户管理员及以上：企业包/部门包/跨部门包
	return packType == store.PackTenant || packType == store.PackDepartment || packType == store.PackCrossDept
}

// sharedPackNeedsApproval 判断是否为平台共享包（行业包/语言文化包）——用户上传内容须先进待审池。
// ★ 2026-09-02 功能①：共享包直接落正式库会影响所有租户译文，必须超管审批通过后热加载生效。
func sharedPackNeedsApproval(packType string) bool {
	return packType == store.PackIndustry || packType == store.PackLocale
}

// deptKBScope 校验部门管理员对指定 KB 包是否有权（部门包须归属本部门及子部门；
// 跨部门包须归属其涵盖部门集合中的本部门及子部门，或全公司包仅超管/租管可维护）。
// 非部门管理员直接放行（超管/租户管理员）。返回 nil 表示有权。
// 参数：u=当前用户，tid=生效租户，pkg=目标包。
func (s *Server) deptKBScope(u *store.User, tid int64, pkg *store.KBPackage) error {
	if auth.RoleLevel(u.Role) != 2 {
		return nil // 非部门管理员无需部门范围校验（超管/租户管理员放行）
	}
	if u.OrgID <= 0 {
		return &apiErr{"无权操作：未绑定部门"}
	}
	// 跨部门包：维护人 = 涵盖部门集合内的部门管理员（全公司包仅超管/租管维护）
	if pkg.PackType == store.PackCrossDept {
		if pkg.CrossAll {
			return &apiErr{"全公司跨部门包仅超管/租管可维护"}
		}
		for _, o := range pkg.CrossOrgs {
			if o == u.OrgID {
				return nil
			}
			if in, _ := s.Store.IsOrgInSubtree(tid, u.OrgID, o); in {
				return nil
			}
		}
		return &apiErr{"无权维护非本部门的跨部门包"}
	}
	if pkg.OrgID <= 0 {
		return &apiErr{"无权操作非本部门的包"}
	}
	inTree, err := s.Store.IsOrgInSubtree(tid, u.OrgID, pkg.OrgID)
	if err != nil || !inTree {
		return &apiErr{"无权操作非本部门的包"}
	}
	return nil
}

// ============ KB 包管理（行业包） ============

// kbTenant 知识库生效租户：
// ★ 2026-09-04 权限澄清后：行业包/语言文化包宿主为租户0（平台上下文 SharedHostTenant）。
//
//	超管未显式切换企业租户（tid≤0）时返回 0（管理平台共享包）；已切换到企业租户（tid>0）
//	则返回该企业租户（管理该企业的企业包/跨部门包/部门包）。普通用户按自身租户。
//
// ⚠️ 历史事故（2026-08-21~23）：本函数曾误写为调用自身导致无限递归栈溢出，
// 任何打开知识库面板的请求都会击穿进程（fatal error: stack overflow），已修复。
func (s *Server) kbTenant(r *http.Request, u *store.User) int64 {
	tid := s.effTenant(r, u) // ★ 修复点：原误写 s.kbTenant 自递归
	// ★ 2026-09-04 权限澄清：行业包/语言文化包宿主为租户0（平台上下文）。
	//   超管未显式切换企业租户（tid<=0）时直接以租户0管理平台共享包；
	//   已切换到企业租户（tid>0）则管理该企业的企业包/跨部门包/部门包。
	if auth.IsSuperAdmin(u) && tid <= 0 {
		return 0
	}
	return tid
}

// invKB 失效引擎 CJK 精确缓存的统一入口（KB 内容/结构变更后必须调用，
// 否则同句翻译在缓存存活期内看不到新术语；Engine 未装配时静默跳过）。
func (s *Server) invKB() {
	if s.Engine != nil {
		s.Engine.InvalidateKBCaches()
	}
}

// handleKBPackages 列出知识库包（部门管理员仅见本部门及子部门部门包）
func (s *Server) handleKBPackages(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	tid := s.kbTenant(r, u)
	// 部门管理员：仅本部门及子部门下属部门包
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 200, map[string]interface{}{"success": true, "packages": []*store.KBPackage{}})
			return
		}
		orgIDs, err := s.Store.OrgDescendantIDs(tid, u.OrgID)
		if err != nil {
			// F-64②（批 I-10）：原 200 承载失败 → 500。部门子树 ID 取的是本进程存储层，
			// 失败既不是入参问题也不是权限问题（权限在 requireDeptAdmin 已判过），
			// 旧写法回 200 会让知识库面板把「查不动」渲染成「本部门没有包」。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		pkgs, err := s.Store.ListDeptPackages(tid, orgIDs)
		if err != nil {
			// F-64②（批 I-10）：原 200 承载失败 → 500：部门包列表查询失败是存储层故障，
			// 与上面的子树取数同类（成功链路才回 200 + packages）。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		s.attachEntryCounts(tid, pkgs)
		s.decoratePackages(tid, pkgs)
		writeJSON(w, 200, map[string]interface{}{"success": true, "packages": pkgs})
		return
	}
	// 租管/超管路径：直接列本租户全部包（部门包/企业包等），附条目数后统一装饰返回
	pkgs, err := s.Store.ListKBPackages(tid)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：租管/超管路径的包列表同样是存储层读取故障，
		// 空列表与查不动必须分得开（旧写法下管理台两处都渲染成「暂无知识包」）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.attachEntryCounts(tid, pkgs)
	// ★ 2026-09-04 权限澄清：行业包/语言文化包宿主为租户0（平台上下文）。
	//   - 平台上下文（tid=0，超管）：列出平台全部行业包/语言文化包，统一管理；
	//   - 企业租户上下文（tid>0，租管/超管已切换）：仅列出本企业的 企业包/跨部门包/部门包——
	//     行业包/语言文化包已迁移至租户0，不在本租户的 kb_packages 中，天然不出现。
	//     旧逻辑在此对非超管滤 locale、并对行业包按租户行业过滤，已随宿主迁移不再需要。
	s.decoratePackages(tid, pkgs)
	writeJSON(w, 200, map[string]interface{}{"success": true, "packages": pkgs})
}

// decoratePackages 为包列表补充展示用名称：归属部门名（org_name）与所属企业名（tenant_name）。
// 部门包/企业包均归属租户，统一带出企业名称；部门包额外带出部门名称。
func (s *Server) decoratePackages(tid int64, pkgs []*store.KBPackage) {
	orgNames, _ := s.Store.OrgNameMap()
	tenantName := ""
	if tn, e := s.Ten.Name(tid); e == nil {
		tenantName = tn
	}
	for _, p := range pkgs {
		p.TenantName = tenantName
		if p.OrgID > 0 {
			p.OrgName = orgNames[p.OrgID]
		}
	}
}

// attachEntryCounts 为包列表一次性附带每条包条目总数（GROUP BY 单查询），
// 消除前端逐包 COUNT 的 N+1 请求（2026-09-03 知识库面板卡顿根因）。
func (s *Server) attachEntryCounts(tid int64, pkgs []*store.KBPackage) {
	counts, err := s.Store.CountEntriesByPackages(tid)
	if err != nil {
		return // 计数失败不阻断列表，角标留 0
	}
	for _, p := range pkgs {
		p.EntryCount = counts[p.ID]
	}
}

// handleKBPackageCreate 创建包（行业包/企业包/部门包）
func (s *Server) handleKBPackageCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Code           string  `json:"code"`             // 包编码（唯一标识，必填）
		Name           string  `json:"name"`             // 包名称（必填，跨部门包由创建人自定义）
		PackType       string  `json:"pack_type"`        // 包类型（industry 行业包，默认）
		Role           string  `json:"role"`             // 包角色（source 源语言包，默认）
		ShareCrossDept *int    `json:"share_cross_dept"` // ★ 可选：部门包跨部门共享初始态（1=共享默认 / 0=退出）；nil=不设置
		CrossAll       *bool   `json:"cross_all"`        // 跨部门包：true=全公司（涵盖租户内全部部门）
		CrossOrgs      []int64 `json:"cross_orgs"`       // 跨部门包：涵盖部门（使用/维护范围）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" || req.Name == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "code/name 不能为空"})
		return
	}
	if req.PackType == "" {
		req.PackType = store.PackIndustry
	}
	if req.Role == "" {
		req.Role = store.PackRoleSource
	}
	// 包类型权限校验：租户管理员仅可建企业/部门包；部门管理员仅可建部门包；超管可建全部类型
	if !canManagePackType(u, req.PackType) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权创建该类型的知识库包"})
		return
	}
	tid := s.kbTenant(r, u)
	var p *store.KBPackage
	// 部门管理员创建部门包：挂到本部门
	if auth.RoleLevel(u.Role) == 2 {
		if u.OrgID <= 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "部门管理员未绑定部门，无法创建部门包"})
			return
		}
		p, err = s.Store.CreateKBPackageForOrg(tid, 0, req.Code, req.Name, req.PackType, req.Role, u.OrgID)
	} else {
		p, err = s.Store.CreateKBPackage(tid, 0, req.Code, req.Name, req.PackType, req.Role)
	}
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500。kb_packages 建包是一条裸 INSERT
		// （表上无唯一索引，重复 code 不会在这里报错），失败只可能是存储写入故障，
		// 入参与权限问题都在上面若干分支拦掉了 ⇒ 不能给 400/403。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ 部门包创建时携带跨部门共享初始态（可选；仅 department 类型有意义）
	if req.ShareCrossDept != nil && p.PackType == store.PackDepartment {
		if serr := s.Store.SetKBPackageCrossDeptShare(p.ID, tid, *req.ShareCrossDept); serr == nil {
			p.ShareCrossDept = *req.ShareCrossDept
		}
	}
	// ★ 跨部门包：落库跨部门范围（全公司 或 涵盖部门集合）；部门管理员创建时至少包含本人部门以便维护
	if p.PackType == store.PackCrossDept {
		all := false
		if req.CrossAll != nil {
			all = *req.CrossAll
		}
		orgs := req.CrossOrgs
		// 非全公司时：部门管理员创建须并入本部门（保证自身可维护），再落库并回填响应字段
		if auth.RoleLevel(u.Role) == 2 && !all {
			has := false
			for _, o := range orgs {
				if o == u.OrgID {
					has = true
					break
				}
			}
			if !has {
				orgs = append(orgs, u.OrgID)
			}
		}
		if serr := s.Store.SetKBPackageCrossScope(p.ID, tid, all, orgs); serr == nil {
			p.CrossAll = all
			p.CrossOrgs = orgs
		}
	}
	s.Store.LogAudit(tid, u.ID, "kb_package_create", "kb_packages", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "package": p})
}

// handleKBPackageUpdate 更新知识库包元信息（名称 / 描述 / 跨部门共享范围等）。参数 w/r：body 含 id 与待更新字段；鉴权：部门管理员及以上；副作用：更新 kb_packages 并写审计。
func (s *Server) handleKBPackageUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		ID             int64   `json:"id"`               // 目标包 ID
		Name           string  `json:"name"`             // 新包名称
		ShareCrossDept *int    `json:"share_cross_dept"` // ★ 可选：同步调整跨部门共享开关（nil=不修改）
		CrossAll       *bool   `json:"cross_all"`        // 跨部门包：全公司范围（nil=不修改）
		CrossOrgs      []int64 `json:"cross_orgs"`       // 跨部门包：涵盖部门（nil=不修改）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.kbTenant(r, u)
	pkg, gErr := s.Store.GetKBPackage(req.ID, tid)
	if gErr != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "包不存在或无权操作"})
		return
	}
	// ★ H3：持有该包 manage 授权的普通成员跳过类型+部门范围校验
	if !s.kbPackGrantedSkip(r, u, pkg, "manage") {
		if auth.RoleLevel(u.Role) < 2 {
			// ★ H3 硬闸（UAT T30 捕获）：非部门管理员成员必须持该包 manage 级授权，
			//   旧版仅 skip 类型/范围校验、无正向准入检查，read 授权可越权写入
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：需要该知识库包的" + needLabel("manage") + "授权"})
			return
		}
		if !canManagePackType(u, pkg.PackType) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权更新该类型的知识库包"})
			return
		}
		if err := s.deptKBScope(u, tid, pkg); err != nil {
			// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
			s.writeAuthzError(w, r, err)
			return
		}
	}
	if err := s.Store.UpdateKBPackage(req.ID, tid, req.Name); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：包已在上方按租户隔离读到、权限也已校验，
		// 改名这条 UPDATE 失败只能是存储写入故障（不存在/无权都不会走到这里）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ 可选：同请求内调整跨部门共享开关（复用独立 setter，权限已在上方校验）
	if req.ShareCrossDept != nil {
		if serr := s.Store.SetKBPackageCrossDeptShare(req.ID, tid, *req.ShareCrossDept); serr != nil {
			// F-64②（批 I-10）：原 200 承载失败 → 按 store 侧两条真实话术分流（文案仍逐字用 serr.Error()）：
			//   目标包不是 department ⇒ 这个开关对该包本就不适用（store 原话「仅部门包支持跨部门共享设置」）
			//     → 409 状态冲突（请求与资源状态对不上，换包/换类型才行，不是入参格式错）；
			//   包已在本租户下读到（上方 GetKBPackage），故 setter 里的「包不存在」只剩并发删除一种可能
			//     → 与 UPDATE 失败同档，500 存储故障。判据用已取到的 pkg.PackType，不再多查一次、也不猜文案。
			if pkg.PackType != store.PackDepartment {
				s.writeError(w, r, apierrors.New(apierrors.ErrConflict, serr.Error()))
				return
			}
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, serr.Error()))
			return
		}
		s.invKB() // 共享集合变化：一致性起见同刷缓存
	}
	// ★ 可选：跨部门包调整跨部门范围（全公司 / 涵盖部门），权限已在上方校验
	if pkg.PackType == store.PackCrossDept && (req.CrossAll != nil || req.CrossOrgs != nil) {
		all := pkg.CrossAll
		if req.CrossAll != nil {
			all = *req.CrossAll
		}
		orgs := pkg.CrossOrgs
		if req.CrossOrgs != nil {
			orgs = req.CrossOrgs
		}
		if serr := s.Store.SetKBPackageCrossScope(req.ID, tid, all, orgs); serr != nil {
			// F-64②（批 I-10）：原 200 承载失败 → 500：跨部门范围是一条纯 UPDATE（setter 内无业务校验分支），
			// 失败即存储写入故障；此处 message 沿用原样 serr.Error()，不做文案改写。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, serr.Error()))
			return
		}
		s.invKB() // 范围变化：同刷缓存
	}
	// ★ F-63（2026-09-26 批 I-3）：detail 原为空串——改名动作无从回查「改了哪个包、改成什么名」
	s.Store.LogAudit(tid, u.ID, "kb_package_update", "kb_packages",
		fmt.Sprintf("术语包 #%d｜名称 %s", req.ID, truncateRunes(req.Name, 40)))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBPackageDelete 删除指定知识库包（按租户 / 部门隔离）。参数 w/r：body 含 id；鉴权：部门管理员及以上；副作用：删除包及其条目并写审计。
func (s *Server) handleKBPackageDelete(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除包 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 包类型权限校验（删除同样校验）
	pkg, gErr := s.Store.GetKBPackage(req.ID, s.kbTenant(r, u))
	if gErr != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "包不存在或无权操作"})
		return
	}
	if !canManagePackType(u, pkg.PackType) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权删除该类型的知识库包"})
		return
	}
	// 维护权限：跨部门包须涵盖本部门（含子树/全公司仅超管租管）；部门包须归属本部门
	if err := s.deptKBScope(u, s.kbTenant(r, u), pkg); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	if err := s.Store.DeleteKBPackage(req.ID, s.kbTenant(r, u)); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：包已在上方读到、类型与部门范围也都校验过，
		// 三条 DELETE（条目/安全句/包）失败只能是存储故障。旧写法 HTTP 层恒 200，
		// 按状态码分支的调用方（SDK/网关/监控）会把「没删掉」读成「删除成功」，
		// 只有逐字检查 success 字段的 React 面板才碰巧拦得住。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ F-63（批 I-3）：删除类统一口径「被删对象标识＋数量」（pkg 在上方的权限校验里已经读到，零额外查询）
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "kb_package_delete", "kb_packages",
		auditDelete("术语包", truncateRunes(pkg.Name, 40), req.ID, 1))
	s.invKB() // ★ 删除包及条目：失效 CJK 缓存
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBEntries 列出包内条目（按 package_id 必填；支持 layer/target_lang/q 过滤与 page/page_size 分页；count=1 仅返回总数）。
func (s *Server) handleKBEntries(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	qp := r.URL.Query()
	pkgID, _ := strconv.ParseInt(qp.Get("package_id"), 10, 64)
	// ★ H3：普通成员需该包只读授权方可浏览条目
	if auth.RoleLevel(u.Role) < 2 && !s.kbPackAllowed(r, u, "read", pkgID) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：需要该知识库包的只读授权"})
		return
	}
	// 解析过滤与分页参数；count=1 时只回包条目总数，否则按 layer/target_lang/q 分页列出
	layer, _ := strconv.Atoi(qp.Get("layer"))
	targetLang := qp.Get("target_lang")
	keyword := qp.Get("q")
	page, _ := strconv.Atoi(qp.Get("page"))
	pageSize, _ := strconv.Atoi(qp.Get("page_size"))
	countOnly := qp.Get("count") == "1"
	tid := s.kbTenant(r, u)
	if countOnly {
		total, err := s.Store.CountEntries(tid, pkgID)
		if err != nil {
			// F-64②（批 I-10）：原 200 承载失败 → 500：count=1 只要一个总数，
			// COUNT 查询失败是本进程存储故障（包 ID 非法也 COUNT 得 0，不会报错），
			// 旧写法让角标统计把「查不动」显示成 0 条。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "total": total})
		return
	}
	entries, total, err := s.Store.ListEntriesPage(tid, pkgID, layer, targetLang, keyword, page, pageSize)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：条目分页查询失败属存储层故障；
		// 空结果与查询失败必须分档（前者 200 + entries 为空，调用方无从区分旧写法下的两种情况）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "entries": entries, "total": total})
}

// handleBrandTerms 品牌术语查询接口（2026-09-10 需求：品牌名设置前端可见 + 知识库单独可配）：
// 列出指定知识库包内的品牌术语（module=brand AND layer=1，如 极石→ROX），
// 供前端「品牌名」配置面板展示与校验。package_id 必填；鉴权：部门管理员及以上。
func (s *Server) handleBrandTerms(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	pkgID, _ := strconv.ParseInt(r.URL.Query().Get("package_id"), 10, 64)
	if auth.RoleLevel(u.Role) < 2 && !s.kbPackAllowed(r, u, "read", pkgID) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：需要该知识库包的只读授权"})
		return
	}
	tid := s.kbTenant(r, u)
	terms, err := s.Store.ListBrandTerms(tid, pkgID)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：品牌术语查询失败是存储层故障，
		// 「品牌名面板空白」和「查不动」在旧写法下界面完全同形（前端按 success 兜底渲染空列表）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "terms": terms, "total": len(terms)})
}

// handleKBEntryAdd 向指定知识库包新增翻译记忆条目（源 / 目标文本等）。参数 w/r：body 含 package_id 与条目内容；鉴权：部门管理员及以上；副作用：写入 kb_entries 并写审计。
func (s *Server) handleKBEntryAdd(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		PackageID  int64  `json:"package_id"`  // 所属包 ID
		Layer      int    `json:"layer"`       // 层级（0 时默认 TM 术语层）
		SourceText string `json:"source_text"` // 源文本（中文，必填）
		TargetLang string `json:"target_lang"` // 目标语言代码（默认 en）
		TargetText string `json:"target_text"` // 目标译文
		Module     string `json:"module"`      // 所属模块
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SourceText == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "source_text 不能为空"})
		return
	}
	tid := s.kbTenant(r, u)
	// 包类型与维护权限校验（跨部门包须涵盖本部门；部门包须归属本部门）
	pkg, gErr := s.Store.GetKBPackage(req.PackageID, tid)
	if gErr != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "包不存在或无权操作"})
		return
	}
	// ★ H3：持有该包 write 授权的普通成员跳过类型+部门范围校验
	if !s.kbPackGrantedSkip(r, u, pkg, "write") {
		if auth.RoleLevel(u.Role) < 2 {
			// ★ H3 硬闸（UAT T30 捕获）：非部门管理员成员必须持该包 write 级授权，
			//   旧版仅 skip 类型/范围校验、无正向准入检查，read 授权可越权写入
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：需要该知识库包的" + needLabel("write") + "授权"})
			return
		}
		if !canManagePackType(u, pkg.PackType) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权向该类型的知识库包写入"})
			return
		}
		if err := s.deptKBScope(u, tid, pkg); err != nil {
			// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
			s.writeAuthzError(w, r, err)
			return
		}
	}
	if req.Layer == 0 {
		req.Layer = store.LayerTM
	}
	if req.TargetLang == "" {
		req.TargetLang = "en"
	}
	id, err := s.Store.SaveEntry(tid, req.PackageID, req.Layer, "zh", req.SourceText, req.TargetLang, req.TargetText, req.Module)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 按失败性质分流，判据直接用本包 errmsg.go 的
		//   hasInternalLeak（与 publicErrMessage 同一把尺子，避免两套口径漂移）：
		//   ① store 的两条入参校验——「不支持的目标语言: x」（tgtLang 会拼进 tm_segments 列名，
		//     受固定语言列白名单约束）与「非法的条目层: n（合法范围 1-4）」（四层契约）——
		//     是人话文案、无内部特征 ⇒ 客户改请求就能过 → 400 ErrValidation；
		//   ② 其余（SQL 报错等）已被 publicErrMessage 脱敏成统一「服务异常」句，客户无从纠正
		//     ⇒ 本进程存储写入故障 → 500。回 200 时这两类在 HTTP 层无从区分，正是 F-64 的病根。
		if hasInternalLeak(err.Error()) {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "kb_entry_add", "kb_entries", req.SourceText)
	s.invKB() // ★ 条目写通 tm_segments：失效 CJK 缓存
	s.rebuildIndexAsync()
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleKBEntryDelete 删除指定知识库条目（按租户 / 部门隔离）。参数 w/r：body 含 id；鉴权：部门管理员及以上；副作用：删除记录并写审计。
func (s *Server) handleKBEntryDelete(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除条目 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// ★ H3：普通成员需条目所属包的编辑授权（先定位包再放行）
	if auth.RoleLevel(u.Role) < 2 {
		rows, e := s.Store.GetEntryForUpdate(s.kbTenant(r, u), req.ID)
		if e != nil || len(rows) == 0 {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "条目不存在或无权操作"})
			return
		}
		if perr := s.requireKBPackPerm(r, u, "write", rows[0].PackageID); perr != nil {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": perr.Error()})
			return
		}
	}
	// ★ F-63（批 I-3）：删除前尽力读一次条目原文供审计（读不到不阻断删除，detail 回落 id 标识）。
	//   旧实现 detail 为空串：术语条目删除后既看不到删的是哪句话，也无法与提交/更新轨迹对齐。
	entryLabel := ""
	if rows, e := s.Store.GetEntryForUpdate(s.kbTenant(r, u), req.ID); e == nil && len(rows) > 0 {
		entryLabel = truncateRunes(rows[0].SourceText, 40)
	}
	if err := s.Store.DeleteEntry(req.ID, s.kbTenant(r, u)); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：DeleteEntry 是一条参数化 DELETE
		// （命中 0 行也回 nil，所以「条目不存在」不会走到这里），失败只能是存储故障。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "kb_entry_delete", "kb_entries",
		auditDelete("术语条目", entryLabel, req.ID, 1))
	s.invKB() // ★ 摘除条目：失效 CJK 缓存
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBEntryUpdate 更新指定知识库条目内容（层/源文本/目标语言/译文/模块；不可改包归属）。
// 参数 w/r：body 含 id 与可编辑字段；鉴权：部门管理员及以上；副作用：更新记录并写审计 + 失效 CJK 缓存。
func (s *Server) handleKBEntryUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	// 解析请求体：id/source_text 必填，target_lang 缺省补 en（layer=0 时保留原层，见下）
	var req struct {
		ID         int64  `json:"id"`          // 待更新条目 ID（必填）
		Layer      int    `json:"layer"`       // 层级（0 时保留原层，由 store 侧校验）
		SourceText string `json:"source_text"` // 源文本（中文，必填）
		TargetLang string `json:"target_lang"` // 目标语言代码（默认 en）
		TargetText string `json:"target_text"` // 目标译文
		Module     string `json:"module"`      // 所属模块
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.SourceText == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "source_text 不能为空"})
		return
	}
	if req.TargetLang == "" {
		req.TargetLang = "en"
	}
	tid := s.kbTenant(r, u)
	// 取原条目，校验包归属与维护权限（与新增同源）
	var cur *store.KBEntry
	if rows, e := s.Store.GetEntryForUpdate(tid, req.ID); e == nil && len(rows) > 0 {
		cur = rows[0]
	} else {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "条目不存在或无权操作"})
		return
	}
	pkg, gErr := s.Store.GetKBPackage(cur.PackageID, tid)
	if gErr != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "包不存在或无权操作"})
		return
	}
	// ★ H3：持有该包 write 授权的普通成员跳过类型+部门范围校验
	if !s.kbPackGrantedSkip(r, u, pkg, "write") {
		if auth.RoleLevel(u.Role) < 2 {
			// ★ H3 硬闸（UAT T30 捕获）：非部门管理员成员必须持该包 write 级授权，
			//   旧版仅 skip 类型/范围校验、无正向准入检查，read 授权可越权写入
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：需要该知识库包的" + needLabel("write") + "授权"})
			return
		}
		if !canManagePackType(u, pkg.PackType) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权向该类型的知识库包写入"})
			return
		}
		if err := s.deptKBScope(u, tid, pkg); err != nil {
			// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
			s.writeAuthzError(w, r, err)
			return
		}
	}
	if req.Layer == 0 {
		req.Layer = cur.Layer
	}
	if err := s.Store.UpdateEntry(req.ID, tid, req.Layer, req.SourceText, req.TargetLang, req.TargetText, req.Module); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：UpdateEntry 是带租户隔离的纯 UPDATE
		// （setter 内无入参校验分支，命中 0 行也回 nil），条目与包的归属/权限已在上方校验，
		// 失败只剩存储写入故障这一种可能。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "kb_entry_update", "kb_entries", req.SourceText)
	s.invKB() // ★ 改条目：失效 CJK 缓存
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleSafetyPhrases 安全句列表（支持按包/语言/类型/状态过滤与 q 搜索 + 服务端分页）
func (s *Server) handleSafetyPhrases(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	qp := r.URL.Query()
	pkgID, _ := strconv.ParseInt(qp.Get("package_id"), 10, 64)
	page, _ := strconv.Atoi(qp.Get("page"))
	pageSize, _ := strconv.Atoi(qp.Get("page_size"))
	phrases, total, err := s.Store.ListSafetyPhrasesPage(s.kbTenant(r, u), pkgID,
		qp.Get("lang"), qp.Get("kind"), qp.Get("status"), qp.Get("q"), page, pageSize)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：安全句分页（过滤 + COUNT）失败是存储层故障，
		// 与「过滤后没有命中」必须分开（后者 200 + phrases 空）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "phrases": phrases, "total": total})
}

// handleSafetyPhraseAdd 新增安全句
func (s *Server) handleSafetyPhraseAdd(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 解析请求体：phrase 必填；lang/kind 缺省补 en/style；replacement 仅 replace 类型有义
	var req struct {
		PackageID   int64  `json:"package_id"`  // 所属包 ID（语言文化包）
		Lang        string `json:"lang"`        // 目标语言代码（默认 en）
		Phrase      string `json:"phrase"`      // 规则内容（必填）
		Kind        string `json:"kind"`        // 类型：style(默认)/forbidden/replace
		Replacement string `json:"replacement"` // 替换词（仅 replace 类型）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Phrase == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "phrase 不能为空"})
		return
	}
	if req.Lang == "" {
		req.Lang = "en"
	}
	if req.Kind == "" {
		req.Kind = "style"
	}
	id, err := s.Store.SaveSafetyPhraseEx(s.kbTenant(r, u), req.PackageID, req.Lang, req.Phrase, req.Kind, req.Replacement)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：安全句落库是一条 INSERT…RETURNING
		// （kind 空值由 store 兜成 style，无入参校验分支），失败只能是存储写入故障；
		// phrase 重复也不报错（表无唯一约束），故这里不存在 409 语义。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "safety_add", "kb_safety_phrases", req.Phrase)
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleSafetyPhraseDelete 删除安全句
func (s *Server) handleSafetyPhraseDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除安全句 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// ★ F-63（批 I-3）：安全句没有单条 getter，删除前扫一次本租户清单取原文（量级小：安全句按租户几十条）
	phraseLabel := ""
	if ps, e := s.Store.ListSafetyPhrases(s.kbTenant(r, u)); e == nil {
		for _, p := range ps {
			if p != nil && p.ID == req.ID {
				phraseLabel = truncateRunes(p.Phrase, 40)
				break
			}
		}
	}
	if err := s.Store.DeleteSafetyPhrase(req.ID, s.kbTenant(r, u)); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：带租户隔离的 DELETE，命中 0 行也回 nil，
		// 所以失败只可能是存储故障（不存在/他租记录都表现为删除成功，本批不改这个既有行为）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ F-63（批 I-3）：detail 原为空串，删除动作只剩「有人删过一条」——补「被删对象标识＋数量」
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "safety_delete", "kb_safety_phrases",
		auditDelete("安全话术", phraseLabel, req.ID, 1))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBPackageStatus 启用/停用知识库包（租户管理员及以上，部门管理员限本部门子树）。
// 停用：从翻译检索层（tm_segments）摘除该包条目（kb_entries 保留）；启用：按优先级重新写回。
func (s *Server) handleKBPackageStatus(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		ID      int64 `json:"id"`
		Enabled int   `json:"enabled"` // 1=启用 0=停用
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || (req.Enabled != 0 && req.Enabled != 1) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.kbTenant(r, u)
	pkg, gErr := s.Store.GetKBPackage(req.ID, tid)
	if gErr != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "包不存在或无权操作"})
		return
	}
	// ★ H3：持有该包 manage 授权的普通成员跳过类型+部门范围校验
	if !s.kbPackGrantedSkip(r, u, pkg, "manage") {
		if auth.RoleLevel(u.Role) < 2 {
			// ★ H3 硬闸（UAT T30 捕获）：非部门管理员成员必须持该包 manage 级授权，
			//   旧版仅 skip 类型/范围校验、无正向准入检查，read 授权可越权写入
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：需要该知识库包的" + needLabel("manage") + "授权"})
			return
		}
		if !canManagePackType(u, pkg.PackType) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作该类型的知识库包"})
			return
		}
		if err := s.deptKBScope(u, tid, pkg); err != nil {
			// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
			s.writeAuthzError(w, r, err)
			return
		}
	}
	if err := s.Store.SetKBPackageEnabled(req.ID, req.Enabled); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：启停要联动改写检索层 tm_segments
		// （摘除/回写），包已在上方按租户隔离读到、权限也过，失败只能是这批改写的存储故障。
		// 旧写法 HTTP 层恒 200：停用没生效，按状态码分支的调用方（SDK/监控）却读成成功，
		// 该包的条目会继续被当成在用检索层参与翻译——状态与事实相反。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "kb_package_status", "kb_packages", fmt.Sprintf("pkg=%d enabled=%d", req.ID, req.Enabled))
	s.invKB() // ★ 启停重写检索层：失效 CJK 缓存
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBPackageShare 部门包跨部门共享开关（包级 opt-out，2026-08-26 KB继承链改造）。
// 语义：share=1（默认）该部门包可参与其他部门的「跨部门降级检索」；
//
//	share=0 本包仅限归属链内用户可见。校验口径与启停接口完全一致。
func (s *Server) handleKBPackageShare(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		// 匿名＝401（★ F-64③ 批 I-10：旧写法回 403，前端只在 401 走重登录链路）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		ID    int64 `json:"id"`
		Share int   `json:"share"` // 1=共享给跨部门检索 0=仅限归属链
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || (req.Share != 0 && req.Share != 1) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.kbTenant(r, u)
	pkg, gErr := s.Store.GetKBPackage(req.ID, tid)
	if gErr != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "包不存在或无权操作"})
		return
	}
	// ★ H3：持有该包 manage 授权的普通成员跳过类型+部门范围校验
	if !s.kbPackGrantedSkip(r, u, pkg, "manage") {
		if auth.RoleLevel(u.Role) < 2 {
			// ★ H3 硬闸（UAT T30 捕获）：非部门管理员成员必须持该包 manage 级授权，
			//   旧版仅 skip 类型/范围校验、无正向准入检查，read 授权可越权写入
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：需要该知识库包的" + needLabel("manage") + "授权"})
			return
		}
		if !canManagePackType(u, pkg.PackType) {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作该类型的知识库包"})
			return
		}
		if err := s.deptKBScope(u, tid, pkg); err != nil {
			// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
			s.writeAuthzError(w, r, err)
			return
		}
	}
	if err := s.Store.SetKBPackageCrossDeptShare(req.ID, tid, req.Share); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 按真实语义分流（文案仍逐字走 publicErrMessage）：
		//   非 department 包（企业包/行业包/跨部门包）本就没有这个开关，store 原话
		//   「仅部门包支持跨部门共享设置」⇒ 请求与该资源的状态对不上 → 409 ErrConflict
		//   （不是入参格式错，也不是权限问题：权限在上方已放行）；
		//   包已在上方按同一 tid 读到，setter 里的「包不存在或无权操作」只剩并发删除 → 500。
		//   判据用已取到的 pkg.PackType，不额外回查、也不靠猜文案。
		if pkg.PackType != store.PackDepartment {
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, publicErrMessage(r.Context(), err)))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "kb_package_share", "kb_packages", fmt.Sprintf("pkg=%d share=%d", req.ID, req.Share))
	s.invKB() // ★ 共享状态变更：一致性起见同刷缓存（幂等廉价）
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBIndexRebuild 手动触发向量索引全量重建（超管）。
// 使用知识库 Embed 阶段模型（stage_models.kb_embed，建议 BAAI/bge-m3）嵌入全部中文原文。
func (s *Server) handleKBIndexRebuild(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	_ = u
	if s.Engine == nil || s.Engine.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "向量索引未初始化"})
		return
	}
	if s.Engine.Rebuilding() {
		// F-64②（批 I-10）：原 200 承载失败 → 409：同一个索引同时只允许一次全量重建，
		// 这是「资源正被上一次操作占用」的状态冲突（与「重复提交」同档），
		// 客户等上一次跑完再发即可；旧写法回 200 会让脚本以为已经排上队并继续狂点。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "重建正在进行中，请稍候"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	n, err := s.Engine.RebuildKBIndex(ctx)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：重建失败的可达原因有三类
		//   （读全表向量源数据失败、逐批 Embed 模型调用失败、无有效向量生成/落盘失败），
		//   没有哪一档上游错误码能同时诚实覆盖，故取兜底 500；
		//   进度（已嵌入行数）与原始错误按原文案逐字保留，排查靠它而不是靠状态码细分。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, fmt.Sprintf("重建失败（已嵌入 %d 行）: %v", n, err)))
		return
	}
	s.Store.LogAudit(1, u.ID, "kb_index_rebuild", "kb", fmt.Sprintf("%d 行向量已重建", n))
	writeJSON(w, 200, map[string]interface{}{"success": true, "embedded": n})
}

// rebuildIndexAsync 知识库变更后异步重建向量索引（导入条目/文件后自动触发；进行中则跳过）。
func (s *Server) rebuildIndexAsync() {
	if s.Engine == nil || s.Engine.DB == nil || s.Engine.NPZPath == "" || s.Engine.Rebuilding() {
		return
	}
	// 后台协程：带 10 分钟超时的上下文重建索引，避免阻塞当前请求
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, _ = s.Engine.RebuildKBIndex(ctx)
	}()
}

// handleSafetyPhraseBulkImport LLM 投喂批量导入安全句（租管及以上）。
// body: {package_id, items:[{lang, phrase, kind, replacement}]}；统一落 pending+llm，人工审核后生效。
func (s *Server) handleSafetyPhraseBulkImport(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		PackageID int64                   `json:"package_id"`
		Items     []*store.KBSafetyPhrase `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Items) == 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "items 不能为空"})
		return
	}
	tid := s.kbTenant(r, u)
	added, err := s.Store.BulkImportSafetyPhrases(tid, req.PackageID, req.Items)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：批量投喂逐条 INSERT，重复项由 store 内部跳过
		// （不是错误），失败只能是中途的存储写入故障。已写入条数不回填是本批保留的历史行为。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "safety_bulk_import", "kb_safety_phrases", fmt.Sprintf("imported=%d pending_review", added))
	writeJSON(w, 200, map[string]interface{}{"success": true, "added": added})
}

// handleSafetyPhraseStatus 审核安全句（通过/驳回/回退待审）。
func (s *Server) handleSafetyPhraseStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID     int64  `json:"id"`
		Status string `json:"status"` // pending/approved/rejected
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.SetSafetyPhraseStatus(req.ID, s.kbTenant(r, u), req.Status); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 按真实语义分流（文案仍逐字走 publicErrMessage，
		//   成功链路不多查一次，只在失败路径回读判定；同 ①档 handleOrderRefund 手法）：
		//   status 不在 pending/approved/rejected ⇒ 客户改请求就能过 → 400 ErrValidation（store 原话「非法状态: x」）；
		//   本租户清单里查不到这条 ⇒ 404 ErrNotFound——store 刻意把「不存在」与「他租记录」合并成
		//     同一句「记录不存在」以免泄露跨租户存在性，404 正是这个口径（既不是 500，也不是 403 泄权限图）；
		//   记录在、status 也合法仍失败 ⇒ 500 存储写入故障。
		msg := publicErrMessage(r.Context(), err)
		if req.Status != "pending" && req.Status != "approved" && req.Status != "rejected" {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, msg))
			return
		}
		phrases, lerr := s.Store.ListSafetyPhrases(s.kbTenant(r, u))
		if lerr == nil {
			found := false
			for _, p := range phrases {
				if p != nil && p.ID == req.ID {
					found = true
					break
				}
			}
			if !found {
				s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, msg))
				return
			}
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, msg))
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "safety_status", "kb_safety_phrases", req.Status)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 行业字典管理（超管可创建/维护行业，2026-09-10） ============
// 行业以 kb_packages 中 pack_type=industry 的包为承载，宿主恒为平台租户0（SharedHostTenant）。
// 超管在「行业管理」面板创建/维护行业，前端各行业下拉（注册/租户表单/数据采集/语料导入）
// 一律动态拉取 GET /api/admin/industries。行业 CRUD 仅允许超管（平台上下文）。

// handleIndustries 列出平台全部行业字典（超管/租户管理员以上可见；前端下拉与面板共用）。
// 响应：{ success, industries: [{id,code,name,enabled,entry_count}] }
func (s *Server) handleIndustries(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireDeptAdmin(r); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	inds, err := s.Store.ListIndustries()
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：行业字典是注册页/租户表单/数据采集下拉的唯一数据源，
		// 查不动与「平台没有行业」在旧写法下界面同形（下拉只剩空列表，用户以为字典没配）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// 附带条目计数（面板角标展示语料量）
	counts, _ := s.Store.CountEntriesByPackages(store.SharedHostTenant)
	for _, p := range inds {
		p.EntryCount = counts[p.ID]
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "industries": inds})
}

// handleIndustryCreate 新建行业（仅超管）：创建平台行业包（pack_type=industry, 租户0）。
// body：{ code, name }——code 为行业编码（全局唯一，如 auto），name 为显示名。
func (s *Server) handleIndustryCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可创建行业"})
		return
	}
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "code 与 name 不能为空"})
		return
	}
	// code 规范化：小写字母/数字/下划线，防注入与展示异常
	code := strings.ToLower(strings.TrimSpace(req.Code))
	ok := true
	for _, c := range code {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			ok = false
			break
		}
	}
	if !ok {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "行业 code 仅允许小写字母/数字/下划线"})
		return
	}
	// code 查重通过后创建平台行业包（宿主固定为租户 0，全局共享）
	exists, _ := s.Store.IndustryCodeExists(code)
	if exists {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "行业 code 已存在"})
		return
	}
	p, err := s.Store.CreateKBPackage(store.SharedHostTenant, 0, code, strings.TrimSpace(req.Name), store.PackIndustry, store.PackRoleSource)
	if err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：code 重复已在上方 IndustryCodeExists 拦掉（内联 400），
		// 建包这条 INSERT 失败只剩存储写入故障（表上无唯一索引，不会在这里抛冲突）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(store.SharedHostTenant, u.ID, "industry_create", "kb_packages", code)
	s.invKB()
	writeJSON(w, 200, map[string]interface{}{"success": true, "industry": p})
}

// handleIndustryUpdate 编辑行业显示名（仅超管）。
// body：{ id, name }
func (s *Server) handleIndustryUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可编辑行业"})
		return
	}
	// 解析并校验 body：id>0、name 去空白后非空；通过后更新显示名并审计+失效缓存
	var req struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.UpdateIndustry(req.ID, strings.TrimSpace(req.Name)); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：改名是限定租户 0 + pack_type=industry 的纯 UPDATE
		// （命中 0 行也回 nil，即「行业不存在」走不到这条分支，那里表现为更新成功），
		// 失败只剩存储写入故障；入参与权限已在上面的 400/403 分支拦掉。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(store.SharedHostTenant, u.ID, "industry_update", "kb_packages", fmt.Sprintf("id=%d name=%s", req.ID, req.Name))
	s.invKB()
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleIndustryStatus 启用/停用行业（仅超管）。
// body：{ id, enabled }——enabled=1 启用 / 0 停用。
func (s *Server) handleIndustryStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可启停行业"})
		return
	}
	// 解析并校验 body：id>0、enabled 仅允许 0/1；启停行业包并审计+失效缓存
	var req struct {
		ID      int64 `json:"id"`
		Enabled int   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || (req.Enabled != 0 && req.Enabled != 1) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.ToggleIndustry(req.ID, req.Enabled); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：启停行业直接决定注册页/翻译命中链路用不用该行业，
		// 失败必须是服务端出错（UPDATE 命中 0 行也回 nil，所以这里不存在「行业不存在」的 404 语义）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(store.SharedHostTenant, u.ID, "industry_status", "kb_packages", fmt.Sprintf("id=%d enabled=%d", req.ID, req.Enabled))
	s.invKB()
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleIndustryDelete 删除行业（仅超管）：删除平台行业包及其下条目/安全句。
// 删除前校验行业未被企业租户引用（tenants.industry 指向该 code 时拒绝，避免注册回落失效）。
// body：{ id }
func (s *Server) handleIndustryDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可删除行业"})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	pkg, gErr := s.Store.GetKBPackage(req.ID, store.SharedHostTenant)
	if gErr != nil || pkg == nil || pkg.PackType != store.PackIndustry {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "行业不存在"})
		return
	}
	// 引用校验：任何租户以该行业 code 注册/配置时禁止删除（注册回落与行业包载入会失效）
	if used, uErr := s.Store.IndustryReferenced(pkg.Code); uErr == nil && used {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "该行业已被企业租户使用，无法删除（可停用替代）"})
		return
	}
	if err := s.Store.DeleteIndustry(req.ID); err != nil {
		// F-64②（批 I-10）：原 200 承载失败 → 500：行业是否存在、类型是否 industry、是否被租户引用
		// 都已在上三道分支拦掉（403「行业不存在」/400「已被企业租户使用」），
		// 走到这里只剩「条目/安全句/包」三条 DELETE 的存储故障——删一半失败的脏状态必须报服务端错误。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(store.SharedHostTenant, u.ID, "industry_delete", "kb_packages", pkg.Code)
	s.invKB()
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
