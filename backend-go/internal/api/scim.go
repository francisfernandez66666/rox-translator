// ============================================================================
// ★ H10 SCIM 2.0 组织同步——API 层
//
//	/api/scim/v2/{ServiceProviderConfig,Schemas,Users,Groups}
//	鉴权：租户自助生成的 Bearer 令牌（scim_config.token），与会话体系完全隔离；
//	停用（enabled=false）或删除配置即切断同步。
//	映射：User↔users（userName/emails/externalId/active→status），
//	      Group↔orgs（name；members=用户 ID 集合，写回 users.org_id）。
//	幂等：externalId 优先匹配，其次 userName；命中即更新不重复建号。
//
// ============================================================================
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"translator/internal/auth"
	"translator/internal/store"
)

const scimCT = "application/scim+json"

// scimUser SCIM User 资源（最小完备子集）。
type scimUser struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id,omitempty"`
	ExternalID  string   `json:"externalId,omitempty"`
	UserName    string   `json:"userName"`
	DisplayName string   `json:"displayName,omitempty"`
	Name        *struct {
		Formatted string `json:"formatted,omitempty"`
		GivenName string `json:"givenName,omitempty"`
	} `json:"name,omitempty"`
	Emails []struct {
		Value string `json:"value"`
	} `json:"emails,omitempty"`
	Active   *bool                  `json:"active,omitempty"`
	Meta     map[string]interface{} `json:"meta,omitempty"`
	Password string                 `json:"password,omitempty"`
}

// scimGroup SCIM Group 资源。
type scimGroup struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id,omitempty"`
	DisplayName string   `json:"displayName"`
	Members     []struct {
		Value string `json:"value"`
	} `json:"members,omitempty"`
	Meta map[string]interface{} `json:"meta,omitempty"`
}

// scimWrite 输出带 SCIM Content-Type 的 JSON 响应。
func scimWrite(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", scimCT)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// scimErr 输出 SCIM 标准错误响应体（schemas/status/detail）。
func scimErr(w http.ResponseWriter, code int, detail string) {
	scimWrite(w, code, map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
		"status":  code, "detail": detail,
	})
}

// scimGuard Bearer 令牌 → 租户配置；401/403 已写出时返回 nil。
func (s *Server) scimGuard(w http.ResponseWriter, r *http.Request) *store.SCIMConfig {
	tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer"))
	cfg, err := s.Store.GetSCIMConfigByToken(tok)
	if err != nil || cfg == nil {
		scimErr(w, 401, "invalid SCIM bearer token")
		return nil
	}
	if !cfg.Enabled {
		scimErr(w, 403, "SCIM provisioning disabled for this tenant")
		return nil
	}
	return cfg
}

// scimUserOf 把 store.User 转换为 SCIM User 资源（ID=数字用户 ID 字符串）。
func scimUserOf(u *store.User, ext string) scimUser {
	su := scimUser{
		Schemas:     []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		ID:          strconv.FormatInt(u.ID, 10),
		ExternalID:  ext,
		UserName:    u.Username,
		DisplayName: u.DisplayName,
		Active:      boolP(u.Status != "disabled"),
		Meta:        map[string]interface{}{"resourceType": "User"},
	}
	if u.Email != "" {
		su.Emails = []struct {
			Value string `json:"value"`
		}{{Value: u.Email}}
	}
	return su
}

// boolP 返回 bool 的指针（SCIM 可选布尔字段用）。
func boolP(b bool) *bool { return &b }

// handleSCIMUsers /api/scim/v2/Users[/{id}] —— SCIM 用户端点：列表/单个/创建/PUT 全量改/PATCH 改/删除。
func (s *Server) handleSCIMUsers(w http.ResponseWriter, r *http.Request) {
	cfg := s.scimGuard(w, r)
	if cfg == nil {
		return
	}
	tid := cfg.TenantID
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/scim/v2/Users"), "/")
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			s.scimUserList(w, r, tid)
		case http.MethodPost:
			s.scimUserCreate(w, r, tid, cfg)
		default:
			scimErr(w, 405, "method not allowed")
		}
		return
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		scimErr(w, 404, "user not found")
		return
	}
	u, _ := s.Store.GetUser(id, tid)
	if u == nil {
		scimErr(w, 404, "user not found")
		return
	}
	ext := s.Store.SCIMExternalID(id)
	switch r.Method {
	case http.MethodGet:
		scimWrite(w, 200, scimUserOf(u, ext))
	case http.MethodPut:
		var su scimUser
		if json.NewDecoder(r.Body).Decode(&su) != nil {
			scimErr(w, 400, "bad request body")
			return
		}
		s.scimUserApply(tid, u, &su, cfg)
		got, _ := s.Store.GetUser(id, tid)
		scimWrite(w, 200, scimUserOf(got, s.Store.SCIMExternalID(id)))
	case http.MethodPatch:
		var patch struct {
			Operations []struct {
				Op    string          `json:"op"`
				Path  string          `json:"path"`
				Value json.RawMessage `json:"value"`
			} `json:"Operations"`
		}
		if json.NewDecoder(r.Body).Decode(&patch) != nil {
			scimErr(w, 400, "bad patch")
			return
		}
		for _, op := range patch.Operations {
			if strings.EqualFold(op.Path, "active") {
				var v bool
				_ = json.Unmarshal(op.Value, &v)
				status := "active"
				if !v {
					status = "disabled"
				}
				_ = s.Store.UpdateUser(u.ID, tid, u.DisplayName, u.Role, status, u.OrgID)
			}
		}
		got, _ := s.Store.GetUser(id, tid)
		scimWrite(w, 200, scimUserOf(got, s.Store.SCIMExternalID(id)))
	case http.MethodDelete:
		// SCIM delete → 软删（禁用）；令牌通道做硬删风险过高
		_ = s.Store.UpdateUser(u.ID, tid, u.DisplayName, u.Role, "disabled", u.OrgID)
		s.Store.LogAudit(tid, 0, "scim_user_disable", "users", u.Username)
		w.WriteHeader(204)
	default:
		scimErr(w, 405, "method not allowed")
	}
}

// scimUserList 分页输出用户列表（startIndex/count，配合 filter 过滤）。
func (s *Server) scimUserList(w http.ResponseWriter, r *http.Request, tid int64) {
	start, _ := strconv.Atoi(r.URL.Query().Get("startIndex"))
	count, _ := strconv.Atoi(r.URL.Query().Get("count"))
	if start <= 0 {
		start = 1
	}
	if count <= 0 || count > 100 {
		count = 100
	}
	users, _ := s.Store.ListUsers(tid)
	filter := r.URL.Query().Get("filter")
	var items []scimUser
	for _, u := range users {
		su := scimUserOf(u, s.Store.SCIMExternalID(u.ID))
		if filter != "" && !scimMatchFilter(filter, su) {
			continue
		}
		items = append(items, su)
	}
	total := len(items)
	lo, hi := start-1, start-1+count
	if lo > total {
		lo = total
	}
	if hi > total {
		hi = total
	}
	scimWrite(w, 200, map[string]interface{}{
		"schemas":      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
		"totalResults": total, "startIndex": start, "itemsPerPage": hi - lo,
		"Resources": items[lo:hi],
	})
}

var scimEqRe = regexp.MustCompile(`(?i)(userName|externalId)\s+eq\s+"([^"]*)"`)

// scimMatchFilter 仅支持 eq 单条件（IdP 全量同步主用路径）。
func scimMatchFilter(filter string, su scimUser) bool {
	for _, m := range scimEqRe.FindAllStringSubmatch(filter, -1) {
		val := su.UserName
		if strings.EqualFold(m[1], "externalId") {
			val = su.ExternalID
		}
		if val != m[2] {
			return false
		}
	}
	return true
}

// scimUserCreate 创建 SCIM 用户：username 必填，默认状态 disabled，按配置映射组织归属。
func (s *Server) scimUserCreate(w http.ResponseWriter, r *http.Request, tid int64, cfg *store.SCIMConfig) {
	var su scimUser
	if json.NewDecoder(r.Body).Decode(&su) != nil || strings.TrimSpace(su.UserName) == "" {
		scimErr(w, 400, "userName is required")
		return
	}
	// 幂等：externalId 命中 → 更新；userName 命中 → 绑定并更新
	if u := s.Store.SCIMFindUserByExternalId(tid, su.ExternalID); u != nil {
		s.scimUserApply(tid, u, &su, cfg)
		scimWrite(w, 200, scimUserOf(u, s.Store.SCIMExternalID(u.ID)))
		return
	}
	ex, _ := s.Store.GetUserByUsername(tid, su.UserName)
	if ex != nil {
		_ = s.Store.SCIMSetUserExternalId(ex.ID, tid, su.ExternalID)
		s.scimUserApply(tid, ex, &su, cfg)
		scimWrite(w, 200, scimUserOf(ex, su.ExternalID))
		return
	}
	pass := make([]byte, 12)
	_, _ = rand.Read(pass)
	hash := auth.PasswordHash(hex.EncodeToString(pass))
	active := true
	if su.Active != nil {
		active = *su.Active
	}
	email := ""
	if len(su.Emails) > 0 {
		email = strings.ToLower(strings.TrimSpace(su.Emails[0].Value))
	}
	display := su.DisplayName
	if display == "" && su.Name != nil {
		display = strings.TrimSpace(su.Name.Formatted + " " + su.Name.GivenName)
	}
	u, err := s.Store.CreateUser(tid, su.UserName, hash, display, "user", 0, cfg.RootOrgID)
	if err != nil {
		scimErr(w, 409, "create user failed: "+err.Error())
		return
	}
	if email != "" {
		_ = s.Store.SetUserEmail(u.ID, tid, email)
	}
	_ = s.Store.SCIMSetUserExternalId(u.ID, tid, su.ExternalID)
	s.Store.LogAudit(tid, 0, "scim_user_create", "users", u.Username)
	u.Email = email
	if !active {
		_ = s.Store.UpdateUser(u.ID, tid, u.DisplayName, u.Role, "disabled", u.OrgID)
		u.Status = "disabled"
	}
	scimWrite(w, 201, scimUserOf(u, su.ExternalID))
}

// scimUserApply PUT/幂等更新：显示名/邮箱/active/externalId。
func (s *Server) scimUserApply(tid int64, u *store.User, su *scimUser, cfg *store.SCIMConfig) {
	if su.ExternalID != "" {
		_ = s.Store.SCIMSetUserExternalId(u.ID, tid, su.ExternalID)
	}
	display := u.DisplayName
	if su.DisplayName != "" {
		display = su.DisplayName
	}
	status := u.Status
	if su.Active != nil {
		status = "disabled"
		if *su.Active {
			status = "active"
		}
	}
	orgID := u.OrgID
	if orgID == 0 {
		orgID = cfg.RootOrgID
	}
	_ = s.Store.UpdateUser(u.ID, tid, display, u.Role, status, orgID)
	if len(su.Emails) > 0 && !strings.EqualFold(strings.TrimSpace(su.Emails[0].Value), u.Email) {
		_ = s.Store.SetUserEmail(u.ID, tid, strings.ToLower(strings.TrimSpace(su.Emails[0].Value)))
	}
}

// handleSCIMGroups /api/scim/v2/Groups[/{id}] —— Group↔org 同步。
func (s *Server) handleSCIMGroups(w http.ResponseWriter, r *http.Request) {
	cfg := s.scimGuard(w, r)
	if cfg == nil {
		return
	}
	tid := cfg.TenantID
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/scim/v2/Groups"), "/")
	switch r.Method {
	case http.MethodGet:
		if rest != "" {
			s.scimGroupOne(w, tid, rest)
			return
		}
		orgs, err := s.Store.ListOrgs(tid)
		if err != nil {
			scimErr(w, 500, err.Error())
			return
		}
		var items []scimGroup
		for _, o := range orgs {
			items = append(items, s.scimGroupOf(tid, o.ID, o.Name))
		}
		scimWrite(w, 200, map[string]interface{}{
			"schemas":      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
			"totalResults": len(items), "Resources": items,
		})
	case http.MethodPost:
		var g scimGroup
		if json.NewDecoder(r.Body).Decode(&g) != nil || strings.TrimSpace(g.DisplayName) == "" {
			scimErr(w, 400, "displayName is required")
			return
		}
		// 幂等：同名同级组织直接返回
		if exist := s.scimFindOrgByName(tid, cfg.RootOrgID, g.DisplayName); exist > 0 {
			scimWrite(w, 200, s.scimGroupOf(tid, exist, g.DisplayName))
			return
		}
		org, err := s.Store.CreateOrg(tid, cfg.RootOrgID, g.DisplayName, "scim")
		if err != nil {
			scimErr(w, 409, "create group failed: "+err.Error())
			return
		}
		s.applyGroupMembers(tid, org.ID, g.Members)
		s.Store.LogAudit(tid, 0, "scim_group_create", "orgs", org.Name)
		scimWrite(w, 201, s.scimGroupOf(tid, org.ID, org.Name))
	case http.MethodPut, http.MethodPatch:
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || id <= 0 {
			scimErr(w, 404, "group not found")
			return
		}
		var g scimGroup
		if json.NewDecoder(r.Body).Decode(&g) != nil {
			scimErr(w, 400, "bad body")
			return
		}
		if len(g.Members) > 0 || r.Method == http.MethodPut {
			s.applyGroupMembers(tid, id, g.Members)
		}
		name := g.DisplayName
		if name == "" {
			if o := s.scimOrgName(tid, id); o != "" {
				name = o
			} else {
				scimErr(w, 404, "group not found")
				return
			}
		}
		scimWrite(w, 200, s.scimGroupOf(tid, id, name))
	case http.MethodDelete:
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || id <= 0 {
			scimErr(w, 404, "group not found")
			return
		}
		if err := s.Store.DeleteOrg(id); err != nil {
			scimErr(w, 400, err.Error()) // 有成员/子组织时拒绝，符合 IdP 重放安全
			return
		}
		s.Store.LogAudit(tid, 0, "scim_group_delete", "orgs", strconv.FormatInt(id, 10))
		w.WriteHeader(204)
	default:
		scimErr(w, 405, "method not allowed")
	}
}

// scimGroupOne 处理单个群组请求（ID 即部门 ID，仅支持查询）。
func (s *Server) scimGroupOne(w http.ResponseWriter, tid int64, rest string) {
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		scimErr(w, 404, "group not found")
		return
	}
	name := s.scimOrgName(tid, id)
	if name == "" {
		scimErr(w, 404, "group not found")
		return
	}
	scimWrite(w, 200, s.scimGroupOf(tid, id, name))
}

// scimOrgName 返回部门名称（未找到返回空串）。
func (s *Server) scimOrgName(tid, orgID int64) string {
	orgs, _ := s.Store.ListOrgs(tid)
	for _, o := range orgs {
		if o.ID == orgID {
			return o.Name
		}
	}
	return ""
}

// scimFindOrgByName 按名称查找部门（可限定父部门），返回部门 ID 或 0。
func (s *Server) scimFindOrgByName(tid, parentID int64, name string) int64 {
	orgs, _ := s.Store.ListOrgs(tid)
	for _, o := range orgs {
		if o.Name == name && (parentID == 0 || o.ParentID == parentID) {
			return o.ID
		}
	}
	return 0
}

// scimGroupOf 构造 SCIM Group 资源（成员取自部门用户列表）。
func (s *Server) scimGroupOf(tid, orgID int64, name string) scimGroup {
	members := s.Store.UserIDsByOrg(tid, orgID)
	g := scimGroup{
		Schemas:     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		ID:          strconv.FormatInt(orgID, 10),
		DisplayName: name,
		Meta:        map[string]interface{}{"resourceType": "Group"},
	}
	for _, m := range members {
		g.Members = append(g.Members, struct {
			Value string `json:"value"`
		}{Value: strconv.FormatInt(m, 10)})
	}
	return g
}

// applyGroupMembers 全量替换组成员（SCIM members 语义为集合）。
func (s *Server) applyGroupMembers(tid, orgID int64, members []struct {
	Value string `json:"value"`
}) {
	if orgID <= 0 {
		return
	}
	set := map[int64]bool{}
	for _, m := range members {
		if uid, err := strconv.ParseInt(m.Value, 10, 64); err == nil {
			if u, _ := s.Store.GetUser(uid, tid); u != nil {
				set[uid] = true
			}
		}
	}
	old := s.Store.UserIDsByOrg(tid, orgID)
	for _, uid := range old {
		if !set[uid] {
			_ = s.Store.SetUserOrg(uid, tid, 0) // 移出本组（未同步入其他组的用户回落未分配）
		}
	}
	for uid := range set {
		_ = s.Store.SetUserOrg(uid, tid, orgID)
	}
}

// handleSCIMMeta ServiceProviderConfig / Schemas 静态元数据。
func (s *Server) handleSCIMMeta(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "ServiceProviderConfig"):
		scimWrite(w, 200, map[string]interface{}{
			"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
			"patch":   map[string]interface{}{"supported": true},
			"bulk":    map[string]interface{}{"supported": false},
			"filter":  map[string]interface{}{"supported": true, "maxResults": 100},
			"authenticationSchemes": []map[string]interface{}{{
				"type": "oauthbearertoken", "name": "OAuth Bearer Token",
				"specURI": "https://datatracker.ietf.org/doc/html/rfc6749",
			}},
		})
	case strings.HasSuffix(r.URL.Path, "Schemas"):
		scimWrite(w, 200, map[string]interface{}{
			"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
			"Resources": []map[string]interface{}{
				{"id": "urn:ietf:params:scim:schemas:core:2.0:User"},
				{"id": "urn:ietf:params:scim:schemas:core:2.0:Group"},
			},
		})
	default:
		scimErr(w, 404, "not found")
	}
}

// ============ 租户自助配置（会话鉴权，org_admin+） ============

// handleTenantSCIM GET 查看配置 / POST {enabled, rotate, root_org_id} 自助开通。
func (s *Server) handleTenantSCIM(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	if r.Method == http.MethodGet {
		cfg, _ := s.Store.GetSCIMConfigByTenant(tid)
		if cfg == nil {
			cfg = &store.SCIMConfig{TenantID: tid}
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "config": cfg,
			"endpoint": scimBaseURL(r) + "/api/scim/v2"})
		return
	}
	var req struct {
		Enabled   *bool  `json:"enabled"`
		Rotate    bool   `json:"rotate"`
		RootOrgID *int64 `json:"root_org_id"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	cfg, _ := s.Store.GetSCIMConfigByTenant(tid)
	if cfg == nil {
		cfg = &store.SCIMConfig{TenantID: tid, Token: store.NewSCIMToken()}
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.Rotate {
		cfg.Token = store.NewSCIMToken()
	}
	if req.RootOrgID != nil {
		cfg.RootOrgID = *req.RootOrgID
	}
	if err := s.Store.UpsertSCIMConfig(cfg); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "scim_config", "scim", mapBoolStr(cfg.Enabled))
	writeJSON(w, 200, map[string]interface{}{"success": true, "config": cfg,
		"endpoint": scimBaseURL(r) + "/api/scim/v2"})
}

// mapBoolStr 把 active 状态映射为用户 status 字段值 enabled/disabled。
func mapBoolStr(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

// scimBaseURL 从请求推断 SCIM 服务对外基础 URL（识别反向代理 https 头）。
func scimBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// registerSCIMRoutes H10 路由注册（server.go 启动调用）。
func (s *Server) registerSCIMRoutes() {
	s.mux.HandleFunc("/api/scim/v2/Users", s.handleSCIMUsers)
	s.mux.HandleFunc("/api/scim/v2/Users/", s.handleSCIMUsers)
	s.mux.HandleFunc("/api/scim/v2/Groups", s.handleSCIMGroups)
	s.mux.HandleFunc("/api/scim/v2/Groups/", s.handleSCIMGroups)
	s.mux.HandleFunc("/api/scim/v2/ServiceProviderConfig", s.handleSCIMMeta)
	s.mux.HandleFunc("/api/scim/v2/Schemas", s.handleSCIMMeta)
	s.mux.HandleFunc("/api/tenant/scim", s.handleTenantSCIM)
}
