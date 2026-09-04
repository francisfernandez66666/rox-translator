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
	"fmt"
	"net"
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
		Code        string `json:"code"`          // 租户唯一编码（必填）
		Name        string `json:"name"`          // 租户名称
		ExpiresAt   string `json:"expires_at"`    // 到期时间
		Permissions string `json:"permissions"`   // 权限 JSON（含每日字符上限等）
		AdminUser   string `json:"admin_user"`    // 初始租户管理员用户名（可选）
		AdminPass   string `json:"admin_pass"`    // 初始租户管理员密码（可选）
		Industry    string `json:"industry"`      // 注册行业编码（功能②：决定共享行业包载入范围）
		BrandName   string `json:"brand_name"`    // 品牌中文名（种入企业知识库固定用法）
		BrandNameEn string `json:"brand_name_en"` // 品牌英文名（覆盖所有非 zh/zh_hant 目标语固定用法）
		BrandNames  string `json:"brand_names"`   // 品牌多语言名 JSON（可选）
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
	// 功能②：初始化租户默认包 + 行业（缺选回退通用行业兜底）
	_ = s.Store.EnsureDefaultPackages(t.ID)
	// ★ 品牌固定用法种入企业知识库（2026-09-04）：防止品牌名翻译漂移（极石→ROX 而非 jixi）
	s.seedTenantBrandTerms(t.ID, req.BrandName, req.BrandNameEn, req.BrandNames)
	industryCode := req.Industry
	if industryCode == "" {
		industryCode = store.GeneralIndustryCode
	}
	_ = s.Ten.SetIndustry(t.ID, industryCode)
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
		ID            int64  `json:"id"`             // 目标租户 ID
		Name          string `json:"name"`           // 租户名称
		ExpiresAt     string `json:"expires_at"`     // 到期时间
		Permissions   string `json:"permissions"`    // 权限 JSON
		BrandName     string `json:"brand_name"`     // 自定义品牌名
		BrandLogo     string `json:"brand_logo"`     // 自定义品牌 Logo
		Domain        string `json:"domain"`         // 自定义域名
		BrandLinks    string `json:"brand_links"`    // 自定义页脚链接 JSON
		Industry      string `json:"industry"`       // 注册行业编码（空=不清除；功能②超管可修正租户行业）
		InviteEnabled *bool  `json:"invite_enabled"` // 邀请好友功能开关（nil=不改动；非 nil=按值设置）
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
	// 功能②：更新租户行业编码（仅当显式传入且非空时；空值不改动，避免误清）。
	//   行业决定共享行业包的载入范围（与注册口径一致）。
	if req.Industry != "" {
		if err := s.Ten.SetIndustry(req.ID, req.Industry); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "行业保存失败: " + err.Error()})
			return
		}
	}
	// 更新品牌定制（含自定义域名/Logo/页脚链接）：仅当请求显式携带品牌字段时才写，
	// 避免「邀请好友」开关、编辑租户名等只改部分字段的请求把已有品牌清空（品牌有独立保存接口）。
	if req.BrandName != "" || req.BrandLogo != "" || req.Domain != "" || req.BrandLinks != "" {
		if err := s.Ten.SetBranding(req.ID, req.BrandName, req.BrandLogo, req.Domain, req.BrandLinks); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "品牌保存失败: " + err.Error()})
			return
		}
	}
	// 更新「邀请好友」开关（仅当显式传入时）
	if req.InviteEnabled != nil {
		if err := s.Ten.SetInviteEnabled(req.ID, *req.InviteEnabled); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邀请开关保存失败: " + err.Error()})
			return
		}
	}
	t, _ := s.Ten.GetByID(req.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "tenant": t})
}

// handleTenantInviteEnabledGet 读取当前生效租户的「邀请好友」功能开关（tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 当前生效租户：超管随 X-Tenant-ID 切换；租户管理员取自身租户。
// 返回: success=true 时携带 invite_enabled（bool，true=开通）。
func (s *Server) handleTenantInviteEnabledGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 解析当前生效租户：请求上下文注入了 X-Tenant-ID 则用其值，否则用当前用户自身租户
	var tid int64
	if id := tenant.FromContext(r.Context()); id > 0 {
		tid = id
	} else {
		tid = u.TenantID
	}
	enabled := true
	isPersonal := false
	if tid > 0 {
		if ten, e := s.Ten.GetByID(tid); e == nil && ten != nil {
			enabled = ten.InviteEnabled
			isPersonal = ten.IsPersonal
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "invite_enabled": enabled, "is_personal": isPersonal})
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

// resolveDedicatedTenant 从访问 Host 解析「专属域名自助注册」目标租户：
// 仅当子域前缀命中某租户的品牌子域（domain 列），且该子域不是主站前缀、且非默认平台租户（code≠rox、id≠1）时，
// 返回该租户 ID；否则返回 0。命中后注册将自动归入该租户，并强制为普通成员（禁止建企业/升管理员）。
// 说明：主站（如 langcross.lexicorn.cn）始终保留「创建企业」能力，不受专属域名逻辑影响。
func resolveDedicatedTenant(s *Server, r *http.Request) int64 {
	if s.Ten == nil {
		return 0
	}
	base := brandingBaseDomain(s)
	primary := "langcross.lexicorn.cn"
	if s.Store != nil {
		if v, e := s.Store.GetConfig("primary_host"); e == nil && v != "" {
			primary = v
		}
	}
	host := r.Host
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == base || host == primary {
		return 0
	}
	prefix := ""
	if strings.HasSuffix(host, "."+base) {
		prefix = strings.TrimSuffix(host, "."+base)
	}
	if prefix == "" {
		return 0
	}
	// 主站前缀（如 langcross）不视为专属域名
	primaryPrefix := ""
	if strings.HasSuffix(primary, "."+base) {
		primaryPrefix = strings.TrimSuffix(primary, "."+base)
	}
	if prefix == primaryPrefix {
		return 0
	}
	// 解析目标租户：优先按品牌子域（domain 列）匹配；兜底按租户编码匹配（如默认租户 rox → rox.lexicorn.cn）
	t, err := s.Ten.GetByDomain(prefix)
	if err != nil || t == nil {
		t, err = s.Ten.GetByCode(prefix)
	}
	if err != nil || t == nil {
		return 0
	}
	if t.Status != tenant.StatusActive {
		return 0
	}
	return t.ID
}

// isDedicatedRegisterHost 判断当前访问是否处于「专属域名自助注册」场景（供前端展示提示与后端注册分支复用）。
func isDedicatedRegisterHost(s *Server, r *http.Request) bool {
	return resolveDedicatedTenant(s, r) > 0
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

// tenantBrandingUnlocked 综合判定：满足以下任一即可解锁品牌定制编辑：
//  1. 租户根（企业租户，is_personal=false）——企业默认开放品牌定制；
//  2. 持有有效付费套餐（套餐付费租户）；
//  3. 超管显式指定开通（超管指定租户）。
func tenantBrandingUnlocked(s *Server, tid int64) bool {
	if t, _ := s.Ten.GetByID(tid); t != nil && !t.IsPersonal {
		return true // 租户根（企业租户）默认可定制品牌
	}
	return tenantBrandingPackagePaid(s, tid) || tenantBrandingGranted(s, tid)
}

// platformBrandingKey 存储「平台主站（租户根 tenant_id=0）」品牌定制，JSON 字符串。
const platformBrandingKey = "platform_branding"

// getPlatformBranding 读取平台主站（tenant_id=0）品牌定制字段。
func (s *Server) getPlatformBranding() map[string]string {
	m := map[string]string{}
	if s.Store == nil {
		return m
	}
	raw, err := s.Store.GetConfig(platformBrandingKey)
	if err != nil || raw == "" {
		return m
	}
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

// setPlatformBranding 保存平台主站（tenant_id=0）品牌定制字段。
func (s *Server) setPlatformBranding(m map[string]string) error {
	if s.Store == nil {
		return fmt.Errorf("平台存储未初始化")
	}
	b, _ := json.Marshal(m)
	return s.Store.SetConfig(platformBrandingKey, string(b))
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
// 解析优先级：?tenant_id=（超管预览指定租户）> Host 子域名前缀（前缀.基础域名）。
// 关键：品牌只由「访问域名」决定——全局根域名（主站/apex）永远返回平台级品牌（能言 LangCross），
// 不跟随登录用户所属租户，避免根域名误显示某租户（如 rox）的品牌；租户品牌仅在该租户专属子域下生效。
func (s *Server) handleTenantBrandingGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.brandingPayload(r))
}

// brandingPayload 解析当前访问域名的租户品牌定制（供接口与 SPA 首屏注入复用）。
// 解析优先级：显式 ?tenant_id= > 按访问子域前缀；主站前缀（如 langcross）按全局根处理，不套用任何租户品牌。
// 返回结构与历史 /api/tenant/branding 响应一致（前端 BrandingProvider 直接消费）。
func (s *Server) brandingPayload(r *http.Request) map[string]interface{} {
	q := r.URL.Query()
	var tid int64
	if v := q.Get("tenant_id"); v != "" {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil {
			tid = n
		}
	}
	// 按访问子域前缀解析专属租户品牌；主站前缀按全局根处理，不套用任何租户品牌
	if tid <= 0 {
		if prefix := tenantPrefixFromHost(s, r.Host); prefix != "" {
			primary := "langcross.lexicorn.cn"
			if s.Store != nil {
				if v, e := s.Store.GetConfig("primary_host"); e == nil && v != "" {
					primary = v
				}
			}
			base := brandingBaseDomain(s)
			primaryPrefix := ""
			if strings.HasSuffix(primary, "."+base) {
				primaryPrefix = strings.TrimSuffix(primary, "."+base)
			}
			if prefix != primaryPrefix && s.Ten != nil {
				if t, err := s.Ten.GetByDomain(prefix); err == nil && t != nil {
					tid = t.ID
				}
			}
		}
	}
	// 全局根域名（主站 langcross / apex）：返回平台级默认品牌；平台主站品牌定制存于 system_config
	if tid <= 0 {
		// 平台主站（租户根 tenant_id=0）：读取 system_config 中存储的平台品牌定制
		m := s.getPlatformBranding()
		return map[string]interface{}{
			"success":              true,
			"tenant_id":            0,
			"name":                 "",
			"code":                 "",
			"industry":             "",
			"industry_name":        "",
			"brand_name":           m["brand_name"],
			"brand_logo":           m["brand_logo"],
			"domain":               m["domain"],
			"brand_home_bg":        m["brand_home_bg"],
			"brand_home_bg_style":  m["brand_home_bg_style"],
			"brand_login_card_pos": m["brand_login_card_pos"],
			"brand_login_layout":   m["brand_login_layout"],
			"brand_paid":           false,
			"brand_granted":        false,
			"brand_root":           false,
			"dedicated_register":   false,
		}
	}
	var t *tenant.Tenant
	if tid > 0 {
		t, _ = s.Ten.GetByID(tid)
	}
	if t == nil {
		return map[string]interface{}{"success": true, "tenant_id": 0}
	}
	// 行业名称解析（注册页「专属域名自动带入企业信息」展示用）
	industryName := ""
	if t.Industry != "" {
		if ip, ierr := s.Store.FindIndustryByCode(t.Industry); ierr == nil && ip != nil {
			industryName = ip.Name
		}
	}
	return map[string]interface{}{
		"success":              true,
		"tenant_id":            t.ID,
		"name":                 t.Name,
		"code":                 t.Code,
		"industry":             t.Industry,
		"industry_name":        industryName,
		"brand_name":           t.BrandName,
		"brand_names":          t.BrandNames,
		"brand_name_en":        t.BrandNameEn,
		"brand_logo":           t.BrandLogo,
		"domain":               t.Domain,
		"brand_home_bg":        t.BrandHomeBg,
		"brand_home_bg_style":  t.BrandHomeBgStyle,
		"brand_login_card_pos": t.BrandLoginCardPos,
		"brand_login_layout":   t.BrandLoginLayout,
		"brand_paid":           tenantBrandingPackagePaid(s, t.ID),
		"brand_granted":        tenantBrandingGranted(s, t.ID),
		"brand_root":           !t.IsPersonal,
		"dedicated_register":   isDedicatedRegisterHost(s, r),
	}
}

// handleCaddyOnDemandAsk 供 Caddy 的 on_demand_tls「ask 权限模块」调用：
// Caddy 在为每个未知子域名签发 Let's Encrypt 证书前，会 GET 本接口 ?domain=<host>，
// 仅当返回 HTTP 200 才允许签发。放行范围：
//  1. 基础域名本身（apex，如 lexicorn.cn）；
//  2. 主站点（primary_host，缺省 langcross.lexicorn.cn，可在 system_config 配置）；
//  3. 已在「品牌定制」中登记的租户子域前缀（GetByDomain 命中）。
//
// 其余子域一律 403，既满足 Caddy 防滥用要求，又避免任意子域耗尽 Let's Encrypt 配额。
// 租户在后台设置子域前缀后即时生效，无需手动申请证书（配合 DNS 通配符 A 记录 *.lexicorn.cn → 服务器 IP）。
func (s *Server) handleCaddyOnDemandAsk(w http.ResponseWriter, r *http.Request) {
	// ★ 整改 R-M6：本接口仅供 Caddy 的 on_demand_tls ask 模块调用，返回 200/403 即会暴露
	//   哪些租户子域已登记（子域名枚举 oracle）。因此强制来源白名单：仅本机回环（Caddy 同机）
	//   或 system_config「caddy_ask_allowed」配置的 CIDR（Caddy 与后端不同机时）可调用，
	//   其余来源一律 403，杜绝外部探测。
	if !s.caddyAskAuthorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
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

// caddyAskAuthorized 判断 Caddy ask 请求是否来自受信来源：本机回环优先；
// 若 Caddy 与后端不同机，可在 system_config「caddy_ask_allowed」配置逗号分隔的 CIDR 白名单。
func (s *Server) caddyAskAuthorized(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	if s.Store != nil {
		if v, e := s.Store.GetConfig("caddy_ask_allowed"); e == nil && v != "" {
			for _, cidr := range strings.Split(v, ",") {
				cidr = strings.TrimSpace(cidr)
				if _, netC, cerr := net.ParseCIDR(cidr); cerr == nil && ip != nil && netC.Contains(ip) {
					return true
				}
			}
		}
	}
	return false
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
		ID                int64  `json:"id"`
		BrandName         string `json:"brand_name"`
		BrandNameEn       string `json:"brand_name_en"`
		BrandNames        string `json:"brand_names"`
		BrandLogo         string `json:"brand_logo"`
		Domain            string `json:"domain"`
		BrandHomeBg       string `json:"brand_home_bg"`
		BrandHomeBgStyle  string `json:"brand_home_bg_style"`
		BrandLoginCardPos string `json:"brand_login_card_pos"`
		BrandLoginLayout  string `json:"brand_login_layout"`
		BrandLinks        string `json:"brand_links"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := req.ID
	if tid <= 0 {
		// 超管以 id=0 显式表示「平台主站（租户根）」；其余角色回落到本租户
		if auth.RoleLevel(u.Role) >= 4 {
			tid = 0
		} else {
			tid = s.effTenant(r, u)
		}
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
	// 平台主站（tenant_id=0）品牌定制：超管专属，存入 system_config
	if tid == 0 {
		m := s.getPlatformBranding()
		m["brand_name"] = req.BrandName
		m["brand_logo"] = req.BrandLogo
		m["domain"] = req.Domain
		m["brand_home_bg"] = req.BrandHomeBg
		m["brand_home_bg_style"] = req.BrandHomeBgStyle
		m["brand_login_card_pos"] = req.BrandLoginCardPos
		m["brand_login_layout"] = req.BrandLoginLayout
		m["brand_links"] = req.BrandLinks
		if err := s.setPlatformBranding(m); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()})
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "tenant_id": 0})
		return
	}
	if err := s.Ten.SetBranding(tid, req.BrandName, req.BrandLogo, req.Domain, req.BrandLinks); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()})
		return
	}
	if err := s.Ten.SetBrandHomeBg(tid, req.BrandHomeBg, req.BrandHomeBgStyle); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "首页背景保存失败: " + err.Error()})
		return
	}
	if err := s.Ten.SetBrandLoginCardPos(tid, req.BrandLoginCardPos); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "登录卡片位置保存失败: " + err.Error()})
		return
	}
	if err := s.Ten.SetBrandLoginLayout(tid, req.BrandLoginLayout); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "登录页布局保存失败: " + err.Error()})
		return
	}
	// ★ 品牌固定用法补种（2026-09-04）：后台品牌定制保存时同步 brand_names/brand_name_en
	//   并幂等重写企业包 L1 术语，使换品牌名后新翻译不再漂移；未提供英文名则不种入。
	s.seedTenantBrandTerms(tid, req.BrandName, req.BrandNameEn, req.BrandNames)
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
