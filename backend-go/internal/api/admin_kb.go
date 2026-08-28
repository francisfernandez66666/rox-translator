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
	"time"
	"translator/internal/auth"
	"translator/internal/store"
)

// canManagePackType 判断用户是否有权管理指定包类型的知识库包。
// 超管可管理全部类型（tenant/industry/locale/department）；
// 租户管理员可管理企业包与部门包；
// 部门管理员仅可管理部门包（department）。
func canManagePackType(u *store.User, packType string) bool {
	if u != nil && auth.IsSuperAdmin(u) {
		return true
	}
	if auth.RoleLevel(u.Role) == 2 {
		// 部门管理员：仅部门包
		return packType == store.PackDepartment
	}
	// 租户管理员及以上：企业包/部门包
	return packType == store.PackTenant || packType == store.PackDepartment
}

// deptKBScope 校验部门管理员对指定 KB 包是否有权（包必须归属本部门及子部门）。
// 非部门管理员直接放行（超管/租户管理员）。返回 nil 表示有权。
// 参数：u=当前用户，tid=生效租户，pkg=目标包。
func (s *Server) deptKBScope(u *store.User, tid int64, pkg *store.KBPackage) error {
	if auth.RoleLevel(u.Role) != 2 {
		return nil // 非部门管理员无需部门范围校验
	}
	if u.OrgID <= 0 || pkg.OrgID <= 0 {
		return &apiErr{"无权操作非本部门的包"}
	}
	inTree, err := s.Store.IsOrgInSubtree(tid, u.OrgID, pkg.OrgID)
	if err != nil || !inTree {
		return &apiErr{"无权操作非本部门的包"}
	}
	return nil
}

// ============ KB 包管理（行业包） ============

// kbTenant 知识库生效租户：超管平台上下文（tid=0）时行业包宿主为租户 1，其余同 effTenant。
// ⚠️ 历史事故（2026-08-21~23）：本函数曾误写为调用自身导致无限递归栈溢出，
// 任何打开知识库面板的请求都会击穿进程（fatal error: stack overflow），已修复。
func (s *Server) kbTenant(r *http.Request, u *store.User) int64 {
	tid := s.effTenant(r, u) // ★ 修复点：原误写 s.kbTenant 自递归
	if auth.IsSuperAdmin(u) && tid <= 0 {
		return 1
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		pkgs, err := s.Store.ListDeptPackages(tid, orgIDs)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		s.decoratePackages(tid, pkgs)
		writeJSON(w, 200, map[string]interface{}{"success": true, "packages": pkgs})
		return
	}
	pkgs, err := s.Store.ListKBPackages(tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 租户管理员（非超管）仅可见企业包/部门包；行业包与语言文化包仅超管可见
	if auth.RoleLevel(u.Role) < 4 {
		filtered := pkgs[:0]
		for _, p := range pkgs {
			if p.PackType == store.PackIndustry || p.PackType == store.PackLocale {
				continue
			}
			filtered = append(filtered, p)
		}
		pkgs = filtered
	}
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

// handleKBPackageCreate 创建包（行业包/企业包/部门包）
func (s *Server) handleKBPackageCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Code           string `json:"code"`             // 包编码（唯一标识，必填）
		Name           string `json:"name"`             // 包名称（必填）
		PackType       string `json:"pack_type"`        // 包类型（industry 行业包，默认）
		Role           string `json:"role"`             // 包角色（source 源语言包，默认）
		ShareCrossDept *int   `json:"share_cross_dept"` // ★ 可选：部门包跨部门共享初始态（1=共享默认 / 0=退出）；nil=不设置
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// ★ 部门包创建时携带跨部门共享初始态（可选；仅 department 类型有意义）
	if req.ShareCrossDept != nil && p.PackType == store.PackDepartment {
		if serr := s.Store.SetKBPackageCrossDeptShare(p.ID, tid, *req.ShareCrossDept); serr == nil {
			p.ShareCrossDept = *req.ShareCrossDept
		}
	}
	s.Store.LogAudit(tid, u.ID, "kb_package_create", "kb_packages", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "package": p})
}

// handleKBPackageUpdate 更新知识库包元信息（名称 / 描述 / 跨部门共享范围等）。参数 w/r：body 含 id 与待更新字段；鉴权：部门管理员及以上；副作用：更新 kb_packages 并写审计。
func (s *Server) handleKBPackageUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID             int64  `json:"id"`               // 目标包 ID
		Name           string `json:"name"`             // 新包名称
		ShareCrossDept *int   `json:"share_cross_dept"` // ★ 可选：同步调整跨部门共享开关（nil=不修改）
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
	if !canManagePackType(u, pkg.PackType) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权更新该类型的知识库包"})
		return
	}
	if err := s.deptKBScope(u, tid, pkg); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if err := s.Store.UpdateKBPackage(req.ID, tid, req.Name); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// ★ 可选：同请求内调整跨部门共享开关（复用独立 setter，权限已在上方校验）
	if req.ShareCrossDept != nil {
		if serr := s.Store.SetKBPackageCrossDeptShare(req.ID, tid, *req.ShareCrossDept); serr != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": serr.Error()})
			return
		}
		s.invKB() // 共享集合变化：一致性起见同刷缓存
	}
	s.Store.LogAudit(tid, u.ID, "kb_package_update", "kb_packages", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBPackageDelete 删除指定知识库包（按租户 / 部门隔离）。参数 w/r：body 含 id；鉴权：部门管理员及以上；副作用：删除包及其条目并写审计。
func (s *Server) handleKBPackageDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
	if err := s.Store.DeleteKBPackage(req.ID, s.kbTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "kb_package_delete", "kb_packages", "")
	s.invKB() // ★ 删除包及条目：失效 CJK 缓存
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBEntries 列出包内条目
func (s *Server) handleKBEntries(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	pkgID, _ := strconv.ParseInt(r.URL.Query().Get("package_id"), 10, 64)
	entries, err := s.Store.ListEntries(s.kbTenant(r, u), pkgID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "entries": entries})
}

// handleKBEntryAdd 向指定知识库包新增翻译记忆条目（源 / 目标文本等）。参数 w/r：body 含 package_id 与条目内容；鉴权：部门管理员及以上；副作用：写入 kb_entries 并写审计。
func (s *Server) handleKBEntryAdd(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
	if req.Layer == 0 {
		req.Layer = store.LayerTM
	}
	if req.TargetLang == "" {
		req.TargetLang = "en"
	}
	id, err := s.Store.SaveEntry(s.kbTenant(r, u), req.PackageID, req.Layer, "zh", req.SourceText, req.TargetLang, req.TargetText, req.Module)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "kb_entry_add", "kb_entries", req.SourceText)
	s.invKB() // ★ 条目写通 tm_segments：失效 CJK 缓存
	s.rebuildIndexAsync()
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleKBEntryDelete 删除指定知识库条目（按租户 / 部门隔离）。参数 w/r：body 含 id；鉴权：部门管理员及以上；副作用：删除记录并写审计。
func (s *Server) handleKBEntryDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除条目 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeleteEntry(req.ID, s.kbTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "kb_entry_delete", "kb_entries", "")
	s.invKB() // ★ 摘除条目：失效 CJK 缓存
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleSafetyPhrases 安全句列表
func (s *Server) handleSafetyPhrases(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	phrases, err := s.Store.ListSafetyPhrases(s.kbTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "phrases": phrases})
}

// handleSafetyPhraseAdd 新增安全句
func (s *Server) handleSafetyPhraseAdd(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "safety_add", "kb_safety_phrases", req.Phrase)
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleSafetyPhraseDelete 删除安全句
func (s *Server) handleSafetyPhraseDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除安全句 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeleteSafetyPhrase(req.ID, s.kbTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "safety_delete", "kb_safety_phrases", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBPackageStatus 启用/停用知识库包（租户管理员及以上，部门管理员限本部门子树）。
// 停用：从翻译检索层（tm_segments）摘除该包条目（kb_entries 保留）；启用：按优先级重新写回。
func (s *Server) handleKBPackageStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
	if !canManagePackType(u, pkg.PackType) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作该类型的知识库包"})
		return
	}
	if err := s.deptKBScope(u, tid, pkg); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if err := s.Store.SetKBPackageEnabled(req.ID, req.Enabled); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
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
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
	if !canManagePackType(u, pkg.PackType) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作该类型的知识库包"})
		return
	}
	if err := s.deptKBScope(u, tid, pkg); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if err := s.Store.SetKBPackageCrossDeptShare(req.ID, tid, req.Share); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Engine == nil || s.Engine.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "向量索引未初始化"})
		return
	}
	if s.Engine.Rebuilding() {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "重建正在进行中，请稍候"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	n, err := s.Engine.RebuildKBIndex(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": fmt.Sprintf("重建失败（已嵌入 %d 行）: %v", n, err)})
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "safety_bulk_import", "kb_safety_phrases", fmt.Sprintf("imported=%d pending_review", added))
	writeJSON(w, 200, map[string]interface{}{"success": true, "added": added})
}

// handleSafetyPhraseStatus 审核安全句（通过/驳回/回退待审）。
func (s *Server) handleSafetyPhraseStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.kbTenant(r, u), u.ID, "safety_status", "kb_safety_phrases", req.Status)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
