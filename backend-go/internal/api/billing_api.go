package api

// ============ 本文件职责中文说明 ============
// 本文件实现计费计量 / 配额限流的接入层与计费配置、租户配额管理接口：
//   - gateUsage：翻译/文件/工单执行前的配额与余额闸门（租户 QPS+并发滑动窗口、每日字符上限、强制计费余额校验）
//   - meterUsage：成功路径的用量计量（系统指标 + 计费流水，按供应商/模型成本核算）
//   - usageModel：解析本次请求实际使用的 LLM 供应商与模型
//   - 强制计费开关（super_admin）：handleBillingConfig / handleBillingConfigSave
//   - 租户配额（tenant_admin+）：handleTenantQuota / handleTenantQuotaSave，含 QPS/并发/每日字符上限
// 所有保存操作写入审计 diff。

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 计费计量 / 配额限流 接入 ============

// gateUsage 翻译/文件/工单执行前的配额与余额闸门：
//   - 租户 QPS + 并发（滑动窗口）
//   - 每日字符上限（租户 permissions.max_daily_chars，0=不限）
//   - 强制计费模式（billing_enforced=1）下校验余额，不足则拒绝
//   - 商业包句数模式（sentence_enforced=1）下校验剩余句数，用尽则提示购买
//
// 返回 (租户ID, 释放函数, 错误)。释放函数必须 defer 调用归还并发名额。
// 参数 r: HTTP 请求（用于解析当前租户）。返回 tid: 生效租户 ID；release: 归还并发名额的函数；错误: 闸门校验失败原因。
func (s *Server) gateUsage(r *http.Request) (int64, func(), error) {
	tid := s.currentTenant(r)
	// 计费服务未初始化时不限流（降级为放行）
	if s.Bill == nil {
		return tid, func() {}, nil
	}
	// 租户 QPS 限流（滑动窗口，超限拒绝）
	if !s.Bill.TryQPS(tid) {
		return tid, func() {}, &apiErr{"请求过于频繁，请稍后再试"}
	}
	// 租户并发名额限流（获取并发名额，需在结束时归还）
	if !s.Bill.TryAcquire(tid) {
		return tid, func() {}, &apiErr{"并发请求过多，请稍后再试"}
	}
	// 并发名额释放函数：翻译流程结束后必须调用归还
	release := func() { s.Bill.Release(tid) }

	// 每日字符上限校验：读取租户 permissions 中的 max_daily_chars（0=不限）
	maxDaily := int64(0)
	if s.Ten != nil {
		if t, err := s.Ten.GetByID(tid); err == nil {
			maxDaily = tenant.ParsePerms(t.Permissions).MaxDailyChars
		}
	}
	if err := s.Bill.CheckDailyQuota(tid, maxDaily); err != nil {
		return tid, release, err
	}
	// 强制计费模式（billing_enforced=1）下校验余额，不足则拒绝
	if s.Bill.Enabled() {
		if err := s.Bill.CheckBalance(tid); err != nil {
			return tid, release, err
		}
	}
	// 商业包句数模式（sentence_enforced=1）下校验剩余句数：用尽则拒绝并提示购买
	if v, _ := s.Store.GetConfig("sentence_enforced"); v == "1" {
		if bal, err := s.Store.GetSentenceBalance(tid); err == nil && bal <= 0 {
			return tid, release, store.ErrSentenceExhausted
		}
	}
	return tid, release, nil
}

// countSentences 统计文本的源句数（按换行与中英文句末标点切分，空段忽略）。
// 参数 text: 源文本；返回句子数量（至少 1，保证计量非零）。
func countSentences(text string) int64 {
	// 用换行与句末标点统一替换为分隔符，再按分隔符切分
	repl := strings.NewReplacer("\n", "。", "\r", "。", "。", "。", "！", "。", "？", "。",
		".", "。", "!", "。", "?", "。", ";", "。", "；", "。")
	segs := strings.Split(repl.Replace(text), "。")
	n := int64(0)
	for _, seg := range segs {
		if strings.TrimSpace(seg) != "" {
			n++
		}
	}
	if n < 1 {
		n = 1 // 空文本兜底为 1 句
	}
	return n
}

// targetLangCount 解析请求目标语言数量（options.target_langs 数组长度；缺省按 1 计）。
// 参数 options: 引擎请求选项；返回目标语言个数（至少 1）。
func targetLangCount(options map[string]interface{}) int64 {
	if options == nil {
		return 1
	}
	if langs, ok := options["target_langs"].([]interface{}); ok {
		if len(langs) > 0 {
			return int64(len(langs))
		}
	}
	return 1
}

// meterSentences 计量句数：按「源句数 × 目标语言数」从租户句数余额扣减。
// 失败仅记录日志不阻断（与 token 计量一致）。
// 参数 r: HTTP 请求；tid: 生效租户 ID；sourceText: 源文本；options: 请求选项。
func (s *Server) meterSentences(r *http.Request, tid int64, sourceText string, options map[string]interface{}) {
	if s.Store == nil || sourceText == "" {
		return
	}
	// 强制计费未开启：不扣减、不校验（余额仅在商业包句数模式下有意义）
	if v, _ := s.Store.GetConfig("sentence_enforced"); v != "1" {
		return
	}
	if tid <= 0 {
		return // 平台上下文无计费
	}
	sents := countSentences(sourceText) * targetLangCount(options)
	if sents <= 0 {
		return
	}
	if _, err := s.Store.DeductSentences(tid, sents); err != nil {
		// 句数不足：仅记录（已在前置闸门拦截，此处为并发兜底）
		return
	}
}

// meterFileSentences 计量文件翻译句数：按「文件提取段数 × 目标语言数」扣减句数余额。
// 参数 r: HTTP 请求；tid: 生效租户 ID；segments: 文件提取的文本段数；options: 请求选项。
func (s *Server) meterFileSentences(r *http.Request, tid int64, segments int, options map[string]interface{}) {
	if s.Store == nil || segments <= 0 {
		return
	}
	// 强制计费未开启：不扣减（同 meterSentences）
	if v, _ := s.Store.GetConfig("sentence_enforced"); v != "1" {
		return
	}
	if tid <= 0 {
		return
	}
	sents := int64(segments) * targetLangCount(options)
	if sents <= 0 {
		return
	}
	if _, err := s.Store.DeductSentences(tid, sents); err != nil {
		return // 句数不足：仅记录
	}
}

// meterUsage 计量一次用量（成功路径调用；失败仅记日志不阻断）。
// 参数 r: HTTP 请求（提取用户 ID）；tid: 生效租户 ID；taskType: 任务类型；quantity: 用量数量。
// 无返回；计量失败不阻断主流程。
func (s *Server) meterUsage(r *http.Request, tid int64, taskType string, quantity int64) {
	if s.Bill == nil || quantity <= 0 {
		return
	}
	// 提取操作用户 ID（未登录为 0）
	userID := int64(0)
	if u := s.authUser(r); u != nil {
		userID = u.ID
	}
	// 解析实际使用的供应商与模型（多供应商成本核算）
	provider, model := s.usageModel(r, tid)
	// 系统级指标：累计计量 token
	s.metrics.addUsage(quantity)
	// 写入计费流水（强制计费模式下会扣余额）；失败仅忽略，由审计兜底
	_ = s.Bill.MeterDeferred(tid, userID, taskType, provider, model, quantity)
}

// usageModel 解析本次请求实际使用的 LLM 供应商与模型（多供应商成本核算）。
// 优先取引擎请求级记录（单语翻译成功路径记录的真实模型），否则回退租户配置/全局默认。
// 参数 r: HTTP 请求；tid: 租户 ID。返回 provider: 供应商标识(global/tenant 或路由名)；model: 模型名。
func (s *Server) usageModel(r *http.Request, tid int64) (provider, model string) {
	// 优先取引擎请求级记录：单语翻译成功路径会记录真实使用的模型
	if s.Engine != nil {
		if p, m := s.Engine.UsageModel(r.Context()); p != "" {
			return p, m
		}
	}
	// 回退：先全局默认，再取租户模型配置
	provider, model = "global", s.Cfg.OnlineModel
	if s.Ten != nil {
		if mc, err := s.Ten.GetModelConfig(tid); err == nil && mc.Model != "" {
			provider, model = "tenant", mc.Model
		}
	}
	return
}

// ============ 强制计费开关（super_admin） ============

// handleBillingConfig 读取计费配置接口（是否强制计费 + 各租户 QPS/并发）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回 success 与 billing_enforced 布尔值。
func (s *Server) handleBillingConfig(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 强制计费状态：由计费服务 Enabled() 判定
	enforced := s.Bill != nil && s.Bill.Enabled()
	writeJSON(w, 200, map[string]interface{}{
		"success":          true,
		"billing_enforced": enforced,
	})
}

// handleBillingConfigSave 保存计费配置接口：billing_enforced=1 开启强制计费（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 billing_enforced 布尔值）。
// 返回: success=true 表示保存成功；配置写入 system_config。
func (s *Server) handleBillingConfigSave(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 super_admin 权限
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		BillingEnforced bool `json:"billing_enforced"` // 是否开启强制计费
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 布尔转字符串配置值（"1"/"0"）
	val := "0"
	if req.BillingEnforced {
		val = "1"
	}
	if err := s.Store.SetConfig("billing_enforced", val); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 审计：记录开关变更前后值（on/off）
	u := s.authUser(r)
	before := "off"
	if v, err := s.Store.GetConfig("billing_enforced"); err == nil && v == "1" {
		before = "on"
	}
	s.Store.LogAuditDiff(s.effTenant(r, u), u.ID, "billing_config_save", "system", val, `{"billing_enforced":"`+before+`"}`, `{"billing_enforced":"`+val+`"}`)
	writeJSON(w, 200, map[string]interface{}{"success": true, "billing_enforced": req.BillingEnforced})
}

// handleTenantQuota 读取租户配额接口（QPS/并发/每日字符上限）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（当前租户）。返回 tenant_id/qps/concurrent/max_daily_chars。
func (s *Server) handleTenantQuota(w http.ResponseWriter, r *http.Request) {
	tid := s.currentTenant(r)
	writeJSON(w, 200, map[string]interface{}{
		"success":         true,
		"tenant_id":       tid,
		"qps":             s.quotaQPS(tid),
		"concurrent":      s.quotaConcurrent(tid),
		"max_daily_chars": s.quotaDaily(tid),
	})
}

// handleTenantQuotaSave 保存租户配额接口（tenant_admin 及以上；QPS/并发写入内存 Quota，每日上限写入租户 permissions）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 qps/concurrent/max_daily_chars）。
// 返回: success=true 表示保存成功；带审计 diff。
func (s *Server) handleTenantQuotaSave(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需 tenant_admin 及以上权限
	if _, err := s.requireTenantAdmin(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		QPS           int   `json:"qps"`             // 每秒请求数上限
		Concurrent    int   `json:"concurrent"`      // 并发请求数上限
		MaxDailyChars int64 `json:"max_daily_chars"` // 每日翻译字符上限（0=不限）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.currentTenant(r)
	// QPS/并发写入内存计费服务（限流器热生效）
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
	// 审计：记录配额变更前后值
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

// quotaQPS 读取租户 QPS（内存 Quota 默认 10）。
// 参数 tid: 租户 ID。返回: 每秒请求数上限。
func (s *Server) quotaQPS(tid int64) int {
	if s.Bill == nil {
		return 10
	}
	return s.Bill.QPS(tid)
}

// quotaConcurrent 读取租户并发（内存 Quota 默认 3）。
// 参数 tid: 租户 ID。返回: 并发请求数上限。
func (s *Server) quotaConcurrent(tid int64) int {
	if s.Bill == nil {
		return 3
	}
	return s.Bill.Concurrent(tid)
}

// quotaDaily 读取租户每日字符上限。
// 参数 tid: 租户 ID。返回: 每日字符上限（0=不限）。
func (s *Server) quotaDaily(tid int64) int64 {
	if s.Ten == nil {
		return 0
	}
	if t, err := s.Ten.GetByID(tid); err == nil {
		return tenant.ParsePerms(t.Permissions).MaxDailyChars
	}
	return 0
}

// ============ 分级用量看板（Req 4） ============

// handleUsageMe 个人用量看板（普通用户个人级）：当前登录用户的累计/当日费用与剩余句数。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 total（累计费用）/today（当日费用）/count（笔数）/sentence_balance。
func (s *Server) handleUsageMe(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	total, today, cnt, err := s.Store.UsageByUser(u.TenantID, u.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	bal, _ := s.Store.GetSentenceBalance(u.TenantID)
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "total": total, "today": today, "count": cnt, "sentence_balance": bal,
	})
}

// handleUsageOrg 组织用量看板（租户管理员）：组织→子组织→用户下钻。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（query: org_id；0/缺省=根组织全部用户）。
// 返回: success=true 时携带 users 数组（各用户用量，含 org_name）与 org_id。
func (s *Server) handleUsageOrg(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	// 超管平台视角（tid<=0）：跨租户聚合全部用户用量
	if auth.IsSuperAdmin(u) && tid <= 0 {
		users, uerr := s.Store.ListAllUsers()
		if uerr != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": uerr.Error()})
			return
		}
		costByUser, cerr := s.Store.UsageAllByUser()
		if cerr != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": cerr.Error()})
			return
		}
		nameMap, _ := s.Store.OrgNameMap()
		type orgUsage struct {
			*store.User
			OrgName string `json:"org_name"`
			Cost    int64  `json:"cost"`
		}
		out := make([]orgUsage, 0, len(users))
		var total int64
		for _, usr := range users {
			c := costByUser[usr.ID]
			total += c
			on := nameMap[usr.OrgID]
			if on == "" {
				on = "平台"
			}
			out = append(out, orgUsage{User: usr, OrgName: on, Cost: c})
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "users": out, "org_id": 0, "total": total})
		return
	}
	orgID := int64(0)
	if v := r.URL.Query().Get("org_id"); v != "" {
		if oid, perr := parseInt64(v); perr == nil && oid > 0 {
			orgID = oid
		}
	}
	// 计算组织及其子孙 ID（0=根组织=租户全部用户）
	orgIDs := []int64{}
	if orgID > 0 {
		if err := s.validateOrg(tid, orgID); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		if ids, derr := s.Store.OrgDescendantIDs(tid, orgID); derr == nil {
			orgIDs = ids
		}
	}
	costByUser, err := s.Store.UsageByOrg(tid, orgIDs)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 组装用户明细（含组织名与用量）
	users, err := s.Store.ListUsersByOrg(tid, orgIDs)
	if err != nil {
		users = []*store.User{}
	}
	orgs, _ := s.Store.ListOrgs(tid)
	orgName := map[int64]string{}
	for _, o := range orgs {
		orgName[o.ID] = o.Name
	}
	type orgUsage struct {
		*store.User
		OrgName string `json:"org_name"`
		Cost    int64  `json:"cost"`
	}
	out := make([]orgUsage, 0, len(users))
	var orgTotal int64
	for _, usr := range users {
		c := costByUser[usr.ID]
		orgTotal += c
		out = append(out, orgUsage{User: usr, OrgName: orgName[usr.OrgID], Cost: c})
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "users": out, "org_id": orgID, "total": orgTotal})
}

// handleUsageCost 全平台模型成本核算看板（超级管理员）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 costs（map[provider/model]=cost）与 quants（map[provider/model]=quantity）。
func (s *Server) handleUsageCost(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	costs, quants, err := s.Store.CostByModel()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "costs": costs, "quants": quants})
}

var _ = strings.TrimSpace
