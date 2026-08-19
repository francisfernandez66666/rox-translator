package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/billing"
	"translator/internal/tenant"
)

// ============ 计费计量 / 配额限流 接入 ============

// gateUsage 翻译/文件/工单执行前的配额与余额闸门：
//   - 租户 QPS + 并发（滑动窗口）
//   - 每日字符上限（租户 permissions.max_daily_chars，0=不限）
//   - 强制计费模式（billing_enforced=1）下校验余额，不足则拒绝
//
// 返回 (租户ID, 释放函数, 错误)。释放函数必须 defer 调用归还并发名额。
func (s *Server) gateUsage(r *http.Request) (int64, func(), error) {
	tid := s.currentTenant(r)
	if s.Bill == nil {
		return tid, func() {}, nil
	}
	if !s.Bill.TryQPS(tid) {
		return tid, func() {}, &apiErr{"请求过于频繁，请稍后再试"}
	}
	if !s.Bill.TryAcquire(tid) {
		return tid, func() {}, &apiErr{"并发请求过多，请稍后再试"}
	}
	release := func() { s.Bill.Release(tid) }

	// 每日字符上限
	maxDaily := int64(0)
	if s.Ten != nil {
		if t, err := s.Ten.GetByID(tid); err == nil {
			maxDaily = tenant.ParsePerms(t.Permissions).MaxDailyChars
		}
	}
	if err := s.Bill.CheckDailyQuota(tid, maxDaily); err != nil {
		return tid, release, err
	}
	// 强制计费时校验余额
	if s.Bill.Enabled() {
		if err := s.Bill.CheckBalance(tid); err != nil {
			return tid, release, err
		}
	}
	return tid, release, nil
}

// meterUsage 计量一次用量（成功路径调用；失败仅记日志不阻断）
func (s *Server) meterUsage(r *http.Request, tid int64, taskType string, quantity int64) {
	if s.Bill == nil || quantity <= 0 {
		return
	}
	userID := int64(0)
	if u := s.authUser(r); u != nil {
		userID = u.ID
	}
	provider, model := s.usageModel(r, tid)
	// 强制计费模式下计量失败返回错误，交给调用方决定是否提示
	_ = s.Bill.MeterDeferred(tid, userID, taskType, provider, model, quantity)
}

// usageModel 解析本次请求实际使用的 LLM 供应商与模型（多供应商成本核算）。
// 优先取引擎请求级记录（单语翻译成功路径记录的真实模型），否则回退租户配置/全局默认。
func (s *Server) usageModel(r *http.Request, tid int64) (provider, model string) {
	if s.Engine != nil {
		if p, m := s.Engine.UsageModel(r.Context()); p != "" {
			return p, m
		}
	}
	provider, model = "global", s.Cfg.OnlineModel
	if s.Ten != nil {
		if mc, err := s.Ten.GetModelConfig(tid); err == nil && mc.Model != "" {
			provider, model = "tenant", mc.Model
		}
	}
	return
}

// ============ 强制计费开关（super_admin） ============

// handleBillingConfig 读取计费配置（是否强制计费 + 各租户 QPS/并发）
func (s *Server) handleBillingConfig(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	enforced := s.Bill != nil && s.Bill.Enabled()
	writeJSON(w, 200, map[string]interface{}{
		"success":          true,
		"billing_enforced": enforced,
	})
}

// handleBillingConfigSave 保存计费配置：billing_enforced=1 开启强制计费（super_admin）
func (s *Server) handleBillingConfigSave(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		BillingEnforced bool `json:"billing_enforced"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	val := "0"
	if req.BillingEnforced {
		val = "1"
	}
	if err := s.Store.SetConfig("billing_enforced", val); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	u := s.authUser(r)
	before := "off"
	if v, err := s.Store.GetConfig("billing_enforced"); err == nil && v == "1" {
		before = "on"
	}
	s.Store.LogAuditDiff(s.effTenant(r, u), u.ID, "billing_config_save", "system", val, `{"billing_enforced":"`+before+`"}`, `{"billing_enforced":"`+val+`"}`)
	writeJSON(w, 200, map[string]interface{}{"success": true, "billing_enforced": req.BillingEnforced})
}

// handleTenantQuota 读取租户配额（QPS/并发/每日上限）
func (s *Server) handleTenantQuota(w http.ResponseWriter, r *http.Request) {
	tid := s.currentTenant(r)
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"tenant_id": tid,
		"qps":      s.quotaQPS(tid),
		"concurrent": s.quotaConcurrent(tid),
		"max_daily_chars": s.quotaDaily(tid),
	})
}

// handleTenantQuotaSave 保存租户配额（tenant_admin 及以上；QPS/并发用逗号分隔）
func (s *Server) handleTenantQuotaSave(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireTenantAdmin(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		QPS           int `json:"qps"`
		Concurrent    int `json:"concurrent"`
		MaxDailyChars int64 `json:"max_daily_chars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.currentTenant(r)
	if s.Bill != nil {
		billing.SetQPS(tid, req.QPS)
		billing.SetConcurrent(tid, req.Concurrent)
	}
	// 每日字符上限写入租户 permissions（与 tenant 配置共用）
	if s.Ten != nil {
		if t, err := s.Ten.GetByID(tid); err == nil {
			perms := tenant.ParsePerms(t.Permissions)
			perms.MaxDailyChars = req.MaxDailyChars
			if req.MaxDailyChars == 0 {
				perms.MaxDailyChars = 0
			}
			b, _ := json.Marshal(perms)
			_ = s.Ten.Update(t.ID, t.Name, t.ExpiresAt, string(b))
		}
	}
	u := s.authUser(r)
	beforeJSON, _ := json.Marshal(map[string]interface{}{
		"qps": s.quotaQPS(tid), "concurrent": s.quotaConcurrent(tid), "max_daily_chars": s.quotaDaily(tid),
	})
	afterJSON, _ := json.Marshal(map[string]interface{}{
		"qps": req.QPS, "concurrent": req.Concurrent, "max_daily_chars": req.MaxDailyChars,
	})
	s.Store.LogAuditDiff(s.effTenant(r, u), u.ID, "tenant_quota_save", "tenant", strconv.FormatInt(tid, 10), string(beforeJSON), string(afterJSON))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// quotaQPS 读取租户 QPS（内存 Quota 默认 10）
func (s *Server) quotaQPS(tid int64) int {
	if s.Bill == nil {
		return 10
	}
	return s.Bill.QPS(tid)
}

// quotaConcurrent 读取租户并发（内存 Quota 默认 3）
func (s *Server) quotaConcurrent(tid int64) int {
	if s.Bill == nil {
		return 3
	}
	return s.Bill.Concurrent(tid)
}

// quotaDaily 读取租户每日字符上限
func (s *Server) quotaDaily(tid int64) int64 {
	if s.Ten == nil {
		return 0
	}
	if t, err := s.Ten.GetByID(tid); err == nil {
		return tenant.ParsePerms(t.Permissions).MaxDailyChars
	}
	return 0
}

var _ = strings.TrimSpace