// ============ tenant.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现 SaaS 租户管理接口（管理后台，JWT admin 认证）：
//   - 租户列表/创建/更新/状态切换/删除（handleTenantList / handleTenantCreate / handleTenantUpdate / handleTenantStatus / handleTenantDelete）
//   - 租户数据导出（handleTenantExport）：数据主权，super_admin 导出租户全部业务数据为 JSON 下载
//   - 租户数据清除（handleTenantErase）：GDPR 删除权，super_admin 清除租户全部业务数据
// 安全与约束：
//   - 全部接口 requireAdminUser 鉴权（super_admin）
//   - 默认租户（ID=1）不可删除/不可清除
//   - 创建租户时可同时创建初始租户管理员账号
//   - 清除操作前建议先导出备份（代码注释约定）

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ SaaS 租户管理（管理后台，JWT admin 认证） ============

// handleTenantList 租户列表接口（super_admin）：返回平台全部租户。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回: success=true 时携带 tenants 数组。
func (s *Server) handleTenantList(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	// 租户存储未初始化时拒绝
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	list, err := s.Ten.List()
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenants": list})
}

// handleTenantCreate 创建租户接口（super_admin）：创建租户并可选同时创建初始租户管理员账号。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 code/name/expires_at/permissions/admin_user/admin_pass）。
// 返回: success=true 时携带新租户对象。
func (s *Server) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		Code        string `json:"code"`        // 租户唯一编码（必填）
		Name        string `json:"name"`        // 租户名称
		ExpiresAt   string `json:"expires_at"`  // 到期时间
		Permissions string `json:"permissions"` // 权限 JSON（含每日字符上限等）
		AdminUser   string `json:"admin_user"`  // 初始租户管理员用户名（可选）
		AdminPass   string `json:"admin_pass"`  // 初始租户管理员密码（可选）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 创建租户
	t, err := s.Ten.Create(req.Code, req.Name, req.ExpiresAt, req.Permissions)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
		return
	}
	// 创建该租户的初始租户管理员账号（挂到新租户下；仅当提供了用户名/密码时）
	if s.Store != nil && req.AdminUser != "" && req.AdminPass != "" {
		if _, err := s.Store.CreateUser(t.ID, req.AdminUser, auth.PasswordHash(req.AdminPass), req.AdminUser+" 管理员", store.RoleTenantAdmin, u.ID, 0); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户已创建，但管理员账号创建失败: " + err.Error()})
			return
		}
	}
	// ★ 默认分配一个开放 API Key（translate 权限、不限量；明文仅此一次随响应返回）
	defaultKey := s.issueDefaultAPIKey(t.ID, "默认 Key")
	resp := map[string]interface{}{"success": true, "tenant": t}
	if defaultKey != "" {
		resp["api_key"] = defaultKey
		resp["message"] = "创建成功（已附带默认 API Key，请立即保存，仅显示一次）"
	}
	writeJSON(w, 200, resp)
}

// handleTenantUpdate 更新租户接口（super_admin）：更新名称/到期时间/权限。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/name/expires_at/permissions）。
// 返回: success=true 时携带更新后的租户对象。
func (s *Server) handleTenantUpdate(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		ID          int64  `json:"id"`          // 目标租户 ID
		Name        string `json:"name"`        // 租户名称
		ExpiresAt   string `json:"expires_at"`  // 到期时间
		Permissions string `json:"permissions"` // 权限 JSON
		BrandName   string `json:"brand_name"`  // 自定义品牌名
		BrandLogo   string `json:"brand_logo"`  // 自定义品牌 Logo
		Domain      string `json:"domain"`      // 自定义域名
		BrandLinks  string `json:"brand_links"` // 自定义页脚链接 JSON
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 更新租户
	if err := s.Ten.Update(req.ID, req.Name, req.ExpiresAt, req.Permissions); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "更新失败: " + err.Error()})
		return
	}
	// 更新品牌定制（含自定义域名/Logo/页脚链接）
	if err := s.Ten.SetBranding(req.ID, req.BrandName, req.BrandLogo, req.Domain, req.BrandLinks); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "品牌保存失败: " + err.Error()})
		return
	}
	t, _ := s.Ten.GetByID(req.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenant": t})
}

// handleTenantStatus 切换租户状态接口（super_admin）：启用/禁用租户。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/status，status 仅支持 active/disabled）。
// 返回: success=true 时携带更新后的租户对象。
func (s *Server) handleTenantStatus(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		ID     int64  `json:"id"`     // 目标租户 ID
		Status string `json:"status"` // 目标状态：active（启用）/ disabled（禁用）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 状态白名单校验
	if req.Status != tenant.StatusActive && req.Status != tenant.StatusDisabled {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "状态仅支持 active/disabled"})
		return
	}
	if err := s.Ten.SetStatus(req.ID, req.Status); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "操作失败: " + err.Error()})
		return
	}
	t, _ := s.Ten.GetByID(req.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenant": t})
}

// handleTenantDelete 删除租户接口（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 表示删除成功；默认租户（ID=1）不可删除。
func (s *Server) handleTenantDelete(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = u
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 目标租户 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 安全约束：不允许删除默认租户（平台根基）
	if req.ID == 1 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "默认租户不可删除"})
		return
	}
	if err := s.Ten.Delete(req.ID); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "删除失败: " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 租户级品牌定制（按域名/租户解析，超管与租户管理员可设置） ============

// handleTenantBranding 品牌接口路由：GET 公开按域名/租户解析品牌；POST 保存（超管或租户管理员）。
func (s *Server) handleTenantBranding(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleTenantBrandingGet(w, r)
		return
	}
	s.handleTenantBrandingSet(w, r)
}

// brandingBaseDomain 品牌基础域名（子域名前缀拼接到此后缀）；可由 system_config(base_domain) 覆盖，缺省 lexicorn.cn。
func brandingBaseDomain(s *Server) string {
	if s.Store != nil {
		if v, e := s.Store.GetConfig("base_domain"); e == nil && v != "" {
			return v
		}
	}
	return "lexicorn.cn"
}

// tenantPrefixFromHost 从访问 Host 解析租户子域名前缀（如 rox.lexicorn.cn → rox）。
func tenantPrefixFromHost(s *Server, host string) string {
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	base := brandingBaseDomain(s)
	if host == base {
		return ""
	}
	if strings.HasSuffix(host, "."+base) {
		return strings.TrimSuffix(host, "."+base)
	}
	return ""
}

// tenantBrandingPackagePaid 判断某租户是否持有有效付费套餐（非试用/非免费且未过期）。
// 仅影响「品牌定制」编辑权限判定之一；与 tenantBrandingGranted（超管授权）取或。
func tenantBrandingPackagePaid(s *Server, tid int64) bool {
	if tid <= 0 || s.Store == nil {
		return false
	}
	perms, err := s.Store.GetTenantPerms(tid)
	if err != nil || perms == nil {
		return false
	}
	code := perms.PackageCode
	if code == "" || code == "trial" {
		return false
	}
	if perms.PackageExpires != "" {
		if exp, e := time.Parse(time.RFC3339, perms.PackageExpires); e == nil && exp.Before(time.Now()) {
			return false
		}
	}
	// 包定义存在时，仅付费/增量包视为已付费；免费包(free)不算
	if pkg, perr := s.Store.GetPackageByCode(tid, code); perr == nil && pkg != nil {
		return pkg.PType != store.PackageFree
	}
	// 包定义缺失（被删除）但订阅身份由支付发放：视为已付费，避免误锁已付费租户
	return true
}

// brandGrantsKey 存储「超管为指定租户开通品牌定制」的 system_config 键（JSON: {"租户ID":true}）
const brandGrantsKey = "brand_grants"

// tenantBrandingGranted 判断某租户是否被超管显式开通品牌定制（免套餐）。
func tenantBrandingGranted(s *Server, tid int64) bool {
	if tid <= 0 || s.Store == nil {
		return false
	}
	raw, err := s.Store.GetConfig(brandGrantsKey)
	if err != nil || raw == "" {
		return false
	}
	var m map[string]bool
	if json.Unmarshal([]byte(raw), &m) != nil {
		return false
	}
	return m[strconv.FormatInt(tid, 10)]
}

// tenantBrandingUnlocked 综合判定：持有有效付费套餐 或 超管已授权，二者任一即可解锁编辑。
func tenantBrandingUnlocked(s *Server, tid int64) bool {
	return tenantBrandingPackagePaid(s, tid) || tenantBrandingGranted(s, tid)
}

// handleAdminBrandGrant 超管为指定租户开通/撤销「品牌定制」权限（免套餐）。
// 仅超管(roleLevel>=4)可调用；开通后该租户（含其租户管理员）即可编辑品牌，无需付费套餐。
func (s *Server) handleAdminBrandGrant(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		TenantID int64 `json:"tenant_id"`
		Enabled  bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TenantID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Store == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "存储未初始化"})
		return
	}
	raw, _ := s.Store.GetConfig(brandGrantsKey)
	var m map[string]bool
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	if m == nil {
		m = map[string]bool{}
	}
	key := strconv.FormatInt(req.TenantID, 10)
	if req.Enabled {
		m[key] = true
	} else {
		delete(m, key)
	}
	b, err := json.Marshal(m)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "序列化失败"})
		return
	}
	if err := s.Store.SetConfig(brandGrantsKey, string(b)); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}


// handleTenantBrandingGet 公开接口：按域名/租户解析品牌展示信息（登录页与前台使用，无需鉴权）。
// 解析优先级：?tenant_id= > Host 子域名前缀（前缀.基础域名）→ 默认租户 rox。
func (s *Server) handleTenantBrandingGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var tid int64
	if v := q.Get("tenant_id"); v != "" {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil {
			tid = n
		}
	}
	if tid <= 0 {
		// 已登录用户优先解析为自身租户（使租户管理员看到本租户品牌，而非默认 rox）
		if u := s.authUser(r); u != nil {
			tid = s.effTenant(r, u)
		}
		if tid <= 0 {
			if prefix := tenantPrefixFromHost(s, r.Host); prefix != "" {
				if t, err := s.Ten.GetByDomain(prefix); err == nil && t != nil {
					tid = t.ID
				}
			}
		}
	}
	var t *tenant.Tenant
	if tid > 0 {
		t, _ = s.Ten.GetByID(tid)
	}
	if t == nil {
		t, _ = s.Ten.GetByCode("rox")
	}
	if t == nil {
		writeJSON(w, 200, map[string]interface{}{"success": true, "tenant_id": 0})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":       true,
		"tenant_id":     t.ID,
		"name":          t.Name,
		"brand_name":    t.BrandName,
		"brand_logo":    t.BrandLogo,
		"domain":        t.Domain,
		"brand_paid":    tenantBrandingPackagePaid(s, t.ID),
		"brand_granted": tenantBrandingGranted(s, t.ID),
	})
}

// handleCaddyOnDemandAsk 供 Caddy 的 on_demand_tls「ask 权限模块」调用：
// Caddy 在为每个未知子域名签发 Let's Encrypt 证书前，会 GET 本接口 ?domain=<host>，
// 仅当返回 HTTP 200 才允许签发。放行范围：
//   1) 基础域名本身（apex，如 lexicorn.cn）；
//   2) 主站点（primary_host，缺省 langcross.lexicorn.cn，可在 system_config 配置）；
//   3) 已在「品牌定制」中登记的租户子域前缀（GetByDomain 命中）。
// 其余子域一律 403，既满足 Caddy 防滥用要求，又避免任意子域耗尽 Let's Encrypt 配额。
// 租户在后台设置子域前缀后即时生效，无需手动申请证书（配合 DNS 通配符 A 记录 *.lexicorn.cn → 服务器 IP）。
func (s *Server) handleCaddyOnDemandAsk(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if i := strings.Index(domain, ":"); i >= 0 {
		domain = domain[:i]
	}
	if domain == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	base := brandingBaseDomain(s)
	if domain == base {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	primary := "langcross.lexicorn.cn"
	if s.Store != nil {
		if v, e := s.Store.GetConfig("primary_host"); e == nil && v != "" {
			primary = v
		}
	}
	if domain == primary {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	if strings.HasSuffix(domain, "."+base) {
		if prefix := strings.TrimSuffix(domain, "."+base); prefix != "" {
			if _, err := s.Ten.GetByDomain(prefix); err == nil {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
				return
			}
		}
	}
	http.Error(w, "forbidden", http.StatusForbidden)
}


// handleFooterLinksGet 公开接口：返回平台级页脚链接（超管设置，与租户无关）。
func (s *Server) handleFooterLinksGet(w http.ResponseWriter, r *http.Request) {
	raw, _ := s.Store.GetConfig("footer_links")
	var links []tenant.BrandLink
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &links)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "links": links})
}

// handleFooterLinksSet 保存平台级页脚链接（仅超管）。links 为 JSON 数组字符串 [{label,label_en,url}]。
func (s *Server) handleFooterLinksSet(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if s.Store == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "存储未初始化"})
		return
	}
	var req struct {
		Links string `json:"links"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.Links != "" {
		var arr []interface{}
		if err := json.Unmarshal([]byte(req.Links), &arr); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "links 需为合法 JSON 数组"})
			return
		}
	}
	if err := s.Store.SetConfig("footer_links", req.Links); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleTenantBrandingSet 保存租户品牌（超管：任意租户；租户管理员：仅本租户）。
func (s *Server) handleTenantBrandingSet(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	var req struct {
		ID         int64  `json:"id"`
		BrandName  string `json:"brand_name"`
		BrandLogo  string `json:"brand_logo"`
		Domain     string `json:"domain"`
		BrandLinks string `json:"brand_links"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := req.ID
	if tid <= 0 {
		tid = s.effTenant(r, u)
	}
	if auth.RoleLevel(u.Role) < 4 && tid != u.TenantID {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "只能设置本租户品牌"})
		return
	}
	// ★ 品牌定制鉴权：付费套餐且在有效期内才能编辑（超管拥有覆盖权）
	if auth.RoleLevel(u.Role) < 4 && !tenantBrandingUnlocked(s, tid) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "品牌定制为付费套餐功能，请先订阅有效套餐后解锁"})
		return
	}
	if err := s.Ten.SetBranding(tid, req.BrandName, req.BrandLogo, req.Domain, req.BrandLinks); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()})
		return
	}
	t, _ := s.Ten.GetByID(tid)
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenant": t})
}

// handleTenantExport 导出租户全部数据接口（数据主权，super_admin）。
func (s *Server) handleTenantExport(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	_, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 目标租户 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Store == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 导出该租户全部业务数据
	data, err := s.Store.ExportTenantData(req.ID)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 格式化 JSON 并作为附件下载
	b, _ := json.MarshalIndent(data, "", "  ")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename=tenant_`+strconv.FormatInt(req.ID, 10)+`_export.json`)
	_, _ = w.Write(b)
}

// handleTenantErase 清除租户全部业务数据接口（GDPR 删除权，super_admin；删除前建议先导出备份）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 表示租户业务数据已清除；默认租户（ID=1）不可清除。
func (s *Server) handleTenantErase(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 目标租户 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 安全约束：默认租户不可清除
	if req.ID == 1 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "默认租户不可清除"})
		return
	}
	if s.Store == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 清除租户全部业务数据（GDPR 删除权；★ C5：含磁盘产物清理的完整版入口）
	if err := s.Store.EraseTenantDataFull(req.ID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 清除操作审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "tenant_erase", "tenants", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "租户业务数据已清除"})
}
