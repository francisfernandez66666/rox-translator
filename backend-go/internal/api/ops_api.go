// ============ ops_api.go · 职责说明 ============
// 运营策略引擎（Operations Policy Engine）HTTP 层：
//   - 有效策略组装：代码默认 → 存量散键兜底 → 平台 ops_policy → 租户覆盖 → 活跃时间窗
//   - GET  /api/admin/ops/policy         查询策略（平台/租户/基础/最终/窗口命中态）
//   - POST /api/admin/ops/policy/save     保存策略（scope=platform|tenant，租户白名单裁剪）
//   - POST /api/admin/ops/policy/window/save 保存单个推广时间窗（超管）
//   - POST /api/admin/billing/package/reset  重置当前套餐月度用量（租户管理员+）
//
// 设计见《改造方案_计费流程引擎因子配置.md》。
// =============================================
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	"translator/internal/ops"
)

// ============ 有效策略组装 ============

// opsPlatformPolicy 平台级策略（system_config.ops_policy，含推广时间窗）。
func (s *Server) opsPlatformPolicy() ops.OperationsPolicy {
	raw, _ := s.Store.GetConfig("ops_policy")
	return ops.ParseOps(raw)
}

// opsTenantPolicy 租户级策略覆盖（tenants.policy_config.ops_policy）。
func (s *Server) opsTenantPolicy(tid int64) ops.OperationsPolicy {
	if tid <= 0 || s.Ten == nil {
		return ops.OperationsPolicy{}
	}
	if pc, err := s.Ten.GetPolicyConfig(tid); err == nil && strings.TrimSpace(pc.OpsPolicy) != "" {
		return ops.ParseOps(pc.OpsPolicy)
	}
	return ops.OperationsPolicy{}
}

// applyLegacyConfig 存量散键兜底（零感知兼容）：策略未配置时回落既有 system_config 行为。
func (s *Server) applyLegacyConfig(eff *ops.EffectivePolicy) {
	if v, _ := s.Store.GetConfig("billing_enforced"); v == "0" {
		eff.Enforced = false
	}
	if v, _ := s.Store.GetConfig("billing_markup_multiplier"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil && f >= 1.0 {
			eff.MarkupMultiplier = f
		}
	}
	if v, _ := s.Store.GetConfig("free_trial_tokens"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Package.TrialTokens = x
		}
	}
	if v, _ := s.Store.GetConfig("free_trial_days"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Package.TrialDays = int(x)
		}
	}
	if v, _ := s.Store.GetConfig("referral_enabled"); v == "0" {
		eff.Invite.Enabled = false
	}
	if v, _ := s.Store.GetConfig("invite_reward_tokens"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Invite.RewardTokens = x
		}
	}
	if v, _ := s.Store.GetConfig("invite_extend_days"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Invite.RewardDays = int(x)
		}
	}
	if v, _ := s.Store.GetConfig("referral_max_daily_rewards"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Invite.MaxDailyRewards = int(x)
		}
	}
	// 付费邀请奖励（多邀多得）：邀请人首笔付费套餐后的 token/有效期（0=永久）——随
	// 2026-09 邀请奖励合并进运营策略引擎，此处把存量散键兜底进最终策略供面板回显。
	if v, _ := s.Store.GetConfig("inviter_paid_reward_tokens"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Invite.PaidRewardTokens = x
		}
	}
	if v, _ := s.Store.GetConfig("inviter_paid_reward_days"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Invite.PaidRewardDays = int(x)
		}
	}
	if v, _ := s.Store.GetConfig("registration_enabled"); v == "0" {
		eff.Registration.Enabled = false
	}
	if v, _ := s.Store.GetConfig("email_verify_enabled"); v == "1" {
		eff.Registration.EmailVerifyEnabled = true
	}
	if v, _ := s.Store.GetConfig("quota_max_qps"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Limits.MaxQPS = int(x)
		}
	}
	if v, _ := s.Store.GetConfig("quota_max_concurrent"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			eff.Limits.MaxConcurrent = int(x)
		}
	}
	if v, _ := s.Store.GetConfig("pay_mode"); v != "" {
		eff.Payment.Mode = v
	}
	if v, _ := s.Store.GetConfig("auto_charge"); v == "1" {
		eff.Payment.AutoCharge = true
	}
}

// opsBaseEffective 基础有效策略 = 代码默认 → 存量散键 → 平台 ops_policy → 租户覆盖（不含时间窗）。
func (s *Server) opsBaseEffective(tid int64) ops.EffectivePolicy {
	eff := ops.DefaultEffective()
	s.applyLegacyConfig(&eff)
	eff = ops.Merge(eff, s.opsPlatformPolicy())
	eff = ops.Merge(eff, s.opsTenantPolicy(tid))
	return eff
}

// effectivePolicy 最终有效策略 = 基础策略 + 命中的活跃时间窗 overrides。
// 叠加顺序：ActiveWindows 返回 priority 降序，需倒序应用使最高优先级窗口最后生效（赢）。
func (s *Server) effectivePolicy(tid int64) ops.EffectivePolicy {
	eff := s.opsBaseEffective(tid)
	plat := s.opsPlatformPolicy()
	active := ops.ActiveWindows(plat.PromoWindows, time.Now(), eff.TZ)
	for i := len(active) - 1; i >= 0; i-- {
		eff = ops.Merge(eff, active[i].Overrides)
	}
	return eff
}

// effPayMode 当前生效支付模式（运营策略 payment.mode，落空默认 mock）。
// 供支付下单/模拟/订阅等读点使用，替代散读 system_config pay_mode：
// 存量 pay_mode 经 applyLegacyConfig 已并入最终策略，双入口收敛为单一事实源。
func (s *Server) effPayMode(tid int64) string {
	m := s.effectivePolicy(tid).Payment.Mode
	if m == "" {
		m = "mock"
	}
	return m
}

// ============ 查询 ============

// handleOpsPolicy 查询运营策略（超管/租户管理员）。
func (s *Server) handleOpsPolicy(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	super := auth.IsSuperAdmin(u)
	tid := s.effTenant(r, u)
	plat := s.opsPlatformPolicy()
	ten := s.opsTenantPolicy(tid)
	base := s.opsBaseEffective(tid)
	eff := s.effectivePolicy(tid)
	now := time.Now()
	windows := make([]map[string]interface{}, 0, len(plat.PromoWindows))
	for _, w := range plat.PromoWindows {
		windows = append(windows, map[string]interface{}{
			"id": w.ID, "name": w.Name, "start": w.Start, "end": w.End,
			"tz": w.TZ, "priority": w.Priority,
			"active": ops.WindowActiveAt(w, now, eff.TZ),
		})
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "scope": "platform", "super": super,
		"tenant_id": tid, "now": now.Format(time.RFC3339),
		"platform": plat, "tenant": ten,
		"base": base, "effective": eff,
		"windows": windows,
	})
}

// ============ 保存策略 ============

// handleOpsPolicySave 保存运营策略。
// ★ 2026-09 权限收紧：运营策略为平台级中台配置，仅超管可写（scope 恒为 platform）；
//   租户管理员仅可读（GET），不再开放租户级覆盖，避免租户自行改奖励开关绕过运营管控。
// 请求体：{"policy":{...OperationsPolicy...}}；scope 兼容字段，忽略租户级。
func (s *Server) handleOpsPolicySave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "运营策略仅超级管理员可设置"})
		return
	}
	var req struct {
		Scope  string               `json:"scope"`
		Policy ops.OperationsPolicy `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "策略格式错误"})
		return
	}
	// 落库前安全裁剪：租户覆盖组与平台专属组一律以平台为准，禁止写 promo_windows/tz 之外的
	// 非授权字段；平台全量策略直接整体写入。
	if err := s.Store.SetConfig("ops_policy", marshalOps(req.Policy)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()})
		return
	}
	s.Store.LogAudit(0, u.ID, "ops_policy_save", "ops", "scope=platform")
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "运营策略已保存", "scope": "platform"})
}

// ============ 推广时间窗 ============

// handleOpsWindowSave 保存单个推广时间窗（超管）：id 已存在=更新，否则新增。
func (s *Server) handleOpsWindowSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可管理推广时间窗"})
		return
	}
	var req struct {
		Window ops.PromoWindow `json:"window"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Window.Start == "" || req.Window.End == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "窗口起止时间必填"})
		return
	}
	// 校验 start < end
	if !ops.WindowTimesValid(req.Window, s.opsPlatformPolicy().TZ) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "时间窗格式非法或 start≥end"})
		return
	}
	pol := s.opsPlatformPolicy()
	replaced := false
	for i := range pol.PromoWindows {
		if pol.PromoWindows[i].ID == req.Window.ID {
			pol.PromoWindows[i] = req.Window
			replaced = true
			break
		}
	}
	if !replaced {
		pol.PromoWindows = append(pol.PromoWindows, req.Window)
	}
	if err := s.Store.SetConfig("ops_policy", marshalOps(pol)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()})
		return
	}
	s.Store.LogAudit(0, u.ID, "ops_window_save", "ops", req.Window.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "推广时间窗已保存", "id": req.Window.ID})
}

// ============ 套餐月度重置 ============

// handlePackageReset 重置当前套餐月度用量（GPT 式重制）：
// 闸门：package.monthly_reset_enabled 开启 且 当月重置次数 < monthly_reset_limit（>0 时生效）。
func (s *Server) handlePackageReset(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	if tid <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请先切换到目标租户"})
		return
	}
	// 超管代操作：body 可带 tenant_id（仅超管生效）
	if auth.IsSuperAdmin(u) {
		var req struct {
			TenantID int64 `json:"tenant_id"`
		}
		if body, rerr := io.ReadAll(io.LimitReader(r.Body, 4096)); rerr == nil && len(body) > 0 {
			_ = json.Unmarshal(body, &req)
			if req.TenantID > 0 {
				tid = req.TenantID
			}
		}
	}
	eff := s.effectivePolicy(tid)
	if !eff.Package.MonthlyResetEnabled {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "未开放套餐重置"})
		return
	}
	if eff.Package.MonthlyResetLimit > 0 {
		if n := s.tenantResetCount(tid); n >= eff.Package.MonthlyResetLimit {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "本月重置次数已达上限"})
			return
		}
	}
	// 执行重置：当前套餐期（kind='plan' 未过期、left<total）恢复满额
	cnt, err := s.Store.ResetCurrentPackageGrants(tid)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "重置失败: " + err.Error()})
		return
	}
	s.bumpTenantResetCount(tid)
	s.Store.LogAudit(tid, u.ID, "billing_package_reset", "packages", "")
	g, p, _ := s.Store.TenantRemainTotal(tid)
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "message": "本月套餐用量已重置", "reset_count": cnt, "remaining": g + p,
	})
}

// tenantResetCount 当月套餐重置次数。
func (s *Server) tenantResetCount(tid int64) int {
	if s.Ten == nil {
		return 0
	}
	pc, _ := s.Ten.GetPolicyConfig(tid)
	month := time.Now().Format("2006-01")
	var rc struct {
		Month string `json:"month"`
		Count int    `json:"count"`
	}
	_ = json.Unmarshal([]byte(pc.OpsResets), &rc)
	if rc.Month != month {
		return 0
	}
	return rc.Count
}

// bumpTenantResetCount 累加当月重置次数（跨月自动归零重计）。
func (s *Server) bumpTenantResetCount(tid int64) {
	if s.Ten == nil {
		return
	}
	month := time.Now().Format("2006-01")
	pc, _ := s.Ten.GetPolicyConfig(tid)
	var rc struct {
		Month string `json:"month"`
		Count int    `json:"count"`
	}
	_ = json.Unmarshal([]byte(pc.OpsResets), &rc)
	if rc.Month != month {
		rc.Month = month
		rc.Count = 0
	}
	rc.Count++
	pc.OpsResets = marshalOps(rc)
	_ = s.Ten.SetPolicyConfig(tid, pc)
}

// marshalOps 通用 JSON 序列化（nil-safe）。
func marshalOps(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
