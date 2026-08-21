package api

// ============ 本文件职责中文说明 ============
// KB 包管理（行业包）与条目、安全句维护（handleKBPackages / handleKBEntries / handleSafetyPhrases 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ========================================

import (
	"context"
	"fmt"
	"time"
	"encoding/json"
	"net/http"
	"strconv"
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
func (s *Server) kbTenant(r *http.Request, u *store.User) int64 {
	tid := s.kbTenant(r, u)
	if auth.IsSuperAdmin(u) && tid <= 0 {
		return 1
	}
	return tid
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
		writeJSON(w, 200, map[string]interface{}{"success": true, "packages": pkgs})
		return
	}
	pkgs, err := s.Store.ListKBPackages(tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "packages": pkgs})
}

// handleKBPackageCreate 创建包（行业包/企业包/部门包）
func (s *Server) handleKBPackageCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Code     string `json:"code"`      // 包编码（唯一标识，必填）
		Name     string `json:"name"`      // 包名称（必填）
		PackType string `json:"pack_type"` // 包类型（industry 行业包，默认）
		Role     string `json:"role"`      // 包角色（source 源语言包，默认）
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
	s.Store.LogAudit(tid, u.ID, "kb_package_create", "kb_packages", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "package": p})
}

// handleKBPackageUpdate 更新包
func (s *Server) handleKBPackageUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID   int64  `json:"id"`   // 目标包 ID
		Name string `json:"name"` // 新包名称
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
	s.Store.LogAudit(tid, u.ID, "kb_package_update", "kb_packages", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBPackageDelete 删除包
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

// handleKBEntryAdd 新增条目
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
	s.rebuildIndexAsync()
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleKBEntryDelete 删除条目
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
		PackageID int64  `json:"package_id"` // 所属包 ID
		Lang      string `json:"lang"`       // 安全句语言代码（默认 en）
		Phrase    string `json:"phrase"`     // 安全句内容（必填）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Phrase == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "phrase 不能为空"})
		return
	}
	if req.Lang == "" {
		req.Lang = "en"
	}
	id, err := s.Store.SaveSafetyPhrase(s.kbTenant(r, u), req.PackageID, req.Lang, req.Phrase)
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
