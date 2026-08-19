package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 自助注册 + 试用额度 ============

// handleRegister 自助注册：
//   - 不携带邀请码：创建独立租户（试用额度）→ 创建该租户 tenant_admin 账号
//   - 携带邀请码且邀请码绑定租户：加入已有租户（创建 user 账号）
//   - 试用额度默认发放（system_config trial_tokens，默认 50000），并在 permissions 记录 max_daily_chars
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil || s.Ten == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "平台未初始化"})
		return
	}
	// 注册开关：默认开放（system_config registration_enabled=0 时关闭）
	if v, _ := s.Store.GetConfig("registration_enabled"); v == "0" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "注册已关闭，请联系管理员开通"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"` // 租户编码（无邀请码时必填）
		Name     string `json:"name"` // 租户名称
		Invite   string `json:"invite"` // 邀请码（可选）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)
	req.Code = strings.TrimSpace(req.Code)
	req.Name = strings.TrimSpace(req.Name)
	if req.Username == "" || req.Password == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "用户名和密码不能为空"})
		return
	}
	if len(req.Password) < 6 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "密码至少 6 位"})
		return
	}

	// 试用额度（可配置）
	trialTokens := int64(50000)
	if v, _ := s.Store.GetConfig("trial_tokens"); v != "" {
		if tv, err := strconv.ParseInt(v, 10, 64); err == nil && tv > 0 {
			trialTokens = tv
		}
	}

	// 1. 处理邀请码（可选）
	inviteTenantID := int64(0)
	if req.Invite != "" {
		inv, err := s.Store.GetInviteCodeByCode(strings.TrimSpace(req.Invite))
		if err != nil || inv.Used == 1 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邀请码无效或已使用"})
			return
		}
		inviteTenantID = inv.TenantID
		// 绑定租户：校验用户是否已存在
		if inviteTenantID > 0 {
			if _, err := s.Store.GetUserByUsername(inviteTenantID, req.Username); err == nil {
				writeJSON(w, 400, map[string]interface{}{"success": false, "message": "该租户下用户名已存在"})
				return
			}
		}
		_ = s.Store.MarkInviteCodeUsed(inv.ID, req.Username)
	}

	// 2. 创建租户（无绑定租户时新建独立试用租户）
	if inviteTenantID == 0 {
		if req.Code == "" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供租户编码"})
			return
		}
		if req.Name == "" {
			req.Name = req.Code
		}
		perms := &tenant.Perms{MaxDailyChars: 20000} // 试用每日上限 2 万字符
		pb, _ := json.Marshal(perms)
		t, err := s.Ten.Create(req.Code, req.Name, "", string(pb))
		if err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "租户创建失败: " + err.Error()})
			return
		}
		inviteTenantID = t.ID
		// 发放试用余额
		_ = s.Store.EnsureBalance(inviteTenantID)
		if trialTokens > 0 {
			_ = s.Store.Charge(inviteTenantID, trialTokens)
		}
	}

	// 3. 创建账号：独立租户 → tenant_admin；受邀加入 → user
	role := store.RoleTenantAdmin
	if inviteTenantID > 0 && s.wasInviteBind(req.Invite) {
		role = store.RoleUser
	}
	nu, err := s.Store.CreateUser(inviteTenantID, req.Username, auth.PasswordHash(req.Password), req.Username, role, 0)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "用户创建失败: " + err.Error()})
		return
	}
	nu.PasswordHash = ""
	s.Store.LogAudit(inviteTenantID, nu.ID, "register", "auth", "自助注册")
	writeJSON(w, 200, map[string]interface{}{
		"success":   true,
		"message":   "注册成功，试用额度已发放",
		"user":      nu,
		"tenant_id": inviteTenantID,
	})
}

// wasInviteBind 判断是否通过绑定租户的邀请码加入（用于角色判定）
func (s *Server) wasInviteBind(invite string) bool {
	if invite == "" {
		return false
	}
	inv, err := s.Store.GetInviteCodeByCode(strings.TrimSpace(invite))
	if err != nil {
		return false
	}
	return inv.TenantID > 0
}

// handleInviteCodes 邀请码列表（super_admin）
func (s *Server) handleInviteCodes(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	codes, err := s.Store.ListInviteCodes()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "codes": codes})
}

// handleInviteCodeCreate 创建邀请码（super_admin）
func (s *Server) handleInviteCodeCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Code     string `json:"code"`
		TenantID int64  `json:"tenant_id"` // 0=新建租户
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "邀请码不能为空"})
		return
	}
	c, err := s.Store.CreateInviteCode(strings.TrimSpace(req.Code), req.TenantID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "invite_create", "invite", req.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true, "code": c})
}