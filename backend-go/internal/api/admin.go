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

// requireAdminUser 超管鉴权（super_admin）
func (s *Server) requireAdminUser(r *http.Request) (*store.User, error) {
	u := s.authUser(r)
	if u == nil {
		return nil, errNotLogin
	}
	if err := auth.RequireRole(u, 3); err != nil {
		return nil, err
	}
	return u, nil
}

// requireTenantAdmin 租户管理员鉴权（tenant_admin 及以上）
func (s *Server) requireTenantAdmin(r *http.Request) (*store.User, error) {
	u := s.authUser(r)
	if u == nil {
		return nil, errNotLogin
	}
	if err := auth.RequireRole(u, 2); err != nil {
		return nil, err
	}
	return u, nil
}

var errNotLogin = &apiErr{"未登录"}

type apiErr struct{ s string }

func (e *apiErr) Error() string { return e.s }

// ============ 流程引擎设置 ============

// handleFlowConfig 读取当前租户流程步骤配置（未配置回退默认定义）
func (s *Server) handleFlowConfig(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	steps := flowStepsForTenant(s.Ten, s.effTenant(r, u))
	writeJSON(w, 200, map[string]interface{}{"success": true, "steps": steps})
}

// flowStepsForTenant 组装租户流程步骤：租户 flow_config 启停 × 默认定义
func flowStepsForTenant(ts *tenant.Store, tid int64) []store.FlowStep {
	cfg := tenant.FlowConfig{}
	if ts != nil {
		cfg, _ = ts.GetFlowConfig(tid)
	}
	out := make([]store.FlowStep, 0, len(store.DefaultFlowSteps))
	for _, d := range store.DefaultFlowSteps {
		enable := d.Enable
		if on, ok := cfg.Steps[d.Key]; ok {
			enable = on
		}
		out = append(out, store.FlowStep{Key: d.Key, Name: d.Name, Enable: enable})
	}
	return out
}

// handleFlowSave 保存当前租户流程步骤启停
func (s *Server) handleFlowSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Steps []store.FlowStep `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Ten == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	cfg := tenant.FlowConfig{Steps: map[string]bool{}}
	for _, st := range req.Steps {
		cfg.Steps[st.Key] = st.Enable
	}
	if err := s.Ten.SetFlowConfig(s.effTenant(r, u), cfg); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "flow_save", "tenants", "流程步骤配置更新")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleFlowRunTicket 直接对指定工单执行流程（admin 触发）
func (s *Server) handleFlowRunTicket(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	wf := s.workflow()
	if wf == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工作流未初始化"})
		return
	}
	if err := wf.Executor.Execute(r.Context(), t, nil); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error(), "ticket": t})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "flow_run", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// ============ 模型配置 ============

// handleModels 读取当前租户的模型配置（未配置时回退全局默认）
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	base := s.Cfg.OnlineAPIBase
	key := s.Cfg.OnlineAPIKey
	model := s.Cfg.OnlineModel
	if s.Ten != nil {
		if mc, err := s.Ten.GetModelConfig(s.effTenant(r, u)); err == nil && mc.Model != "" {
			if mc.APIBase != "" {
				base = mc.APIBase
			}
			if mc.APIKey != "" {
				key = mc.APIKey
			}
			model = mc.Model
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true,
		"model": map[string]string{"api_base": base, "api_key": maskKey(key), "model": model}})
}

// maskKey 掩码显示
func maskKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

// handleModelsSave 保存当前租户的模型配置
func (s *Server) handleModelsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		APIBase string `json:"api_base"`
		APIKey  string `json:"api_key"`
		Model   string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "model 不能为空"})
		return
	}
	if s.Ten == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	// 保留现有配置中未修改的字段（APIKey 掩码时不覆盖）
	cur, _ := s.Ten.GetModelConfig(s.effTenant(r, u))
	mc := cur
	if req.APIBase != "" {
		mc.APIBase = req.APIBase
	}
	if req.APIKey != "" && !hasMask(req.APIKey) {
		mc.APIKey = req.APIKey
	}
	mc.Model = req.Model
	if err := s.Ten.SetModelConfig(s.effTenant(r, u), mc); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "model_save", "tenants", req.Model)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

func hasMask(k string) bool { return strings.Contains(k, "****") }

// ============ 策略参数 ============

// handlePolicy 读取当前租户策略参数（未配置回退全局默认）
func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	pc := tenant.PolicyConfig{}
	if s.Ten != nil {
		pc, _ = s.Ten.GetPolicyConfig(s.effTenant(r, u))
	}
	high := pc.HighSim
	med := pc.MedSim
	evals := pc.EvalsPassThreshold
	if high <= 0 {
		high = s.Cfg.HighSim
	}
	if med <= 0 {
		med = s.Cfg.MedSim
	}
	if evals <= 0 {
		evals = 75
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "policy": map[string]float64{
		"high_sim": high,
		"med_sim":  med,
		"evals_pass_threshold": evals,
	}})
}

// handlePolicySave 保存当前租户策略参数
func (s *Server) handlePolicySave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Policy map[string]float64 `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Ten == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	cur, _ := s.Ten.GetPolicyConfig(s.effTenant(r, u))
	pc := cur
	if v, ok := req.Policy["high_sim"]; ok && v > 0 {
		pc.HighSim = v
	}
	if v, ok := req.Policy["med_sim"]; ok && v > 0 {
		pc.MedSim = v
	}
	if v, ok := req.Policy["evals_pass_threshold"]; ok && v > 0 {
		pc.EvalsPassThreshold = v
	}
	if err := s.Ten.SetPolicyConfig(s.effTenant(r, u), pc); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "policy_save", "tenants", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ KB 包管理（行业包） ============

// handleKBPackages 列出知识库包
func (s *Server) handleKBPackages(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	pkgs, err := s.Store.ListKBPackages(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "packages": pkgs})
}

// handleKBPackageCreate 创建包（行业包）
func (s *Server) handleKBPackageCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		PackType string `json:"pack_type"`
		Role     string `json:"role"`
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
	p, err := s.Store.CreateKBPackage(s.effTenant(r, u), 0, req.Code, req.Name, req.PackType, req.Role)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "kb_package_create", "kb_packages", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "package": p})
}

// handleKBPackageUpdate 更新包
func (s *Server) handleKBPackageUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.UpdateKBPackage(req.ID, s.effTenant(r, u), req.Name); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "kb_package_update", "kb_packages", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBPackageDelete 删除包
func (s *Server) handleKBPackageDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeleteKBPackage(req.ID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "kb_package_delete", "kb_packages", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBEntries 列出包内条目
func (s *Server) handleKBEntries(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	pkgID, _ := strconv.ParseInt(r.URL.Query().Get("package_id"), 10, 64)
	entries, err := s.Store.ListEntries(s.effTenant(r, u), pkgID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "entries": entries})
}

// handleKBEntryAdd 新增条目
func (s *Server) handleKBEntryAdd(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		PackageID int64  `json:"package_id"`
		Layer     int    `json:"layer"`
		SourceText string `json:"source_text"`
		TargetLang string `json:"target_lang"`
		TargetText string `json:"target_text"`
		Module     string `json:"module"`
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
	id, err := s.Store.SaveEntry(s.effTenant(r, u), req.PackageID, req.Layer, "zh", req.SourceText, req.TargetLang, req.TargetText, req.Module)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "kb_entry_add", "kb_entries", req.SourceText)
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleKBEntryDelete 删除条目
func (s *Server) handleKBEntryDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeleteEntry(req.ID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "kb_entry_delete", "kb_entries", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleSafetyPhrases 安全句列表
func (s *Server) handleSafetyPhrases(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	phrases, err := s.Store.ListSafetyPhrases(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "phrases": phrases})
}

// handleSafetyPhraseAdd 新增安全句
func (s *Server) handleSafetyPhraseAdd(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		PackageID int64  `json:"package_id"`
		Lang      string `json:"lang"`
		Phrase    string `json:"phrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Phrase == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "phrase 不能为空"})
		return
	}
	if req.Lang == "" {
		req.Lang = "en"
	}
	id, err := s.Store.SaveSafetyPhrase(s.effTenant(r, u), req.PackageID, req.Lang, req.Phrase)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "safety_add", "kb_safety_phrases", req.Phrase)
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleSafetyPhraseDelete 删除安全句
func (s *Server) handleSafetyPhraseDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeleteSafetyPhrase(req.ID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "safety_delete", "kb_safety_phrases", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ evals 看板 ============

// handleEvalsList 评估记录列表
func (s *Server) handleEvalsList(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	records, err := s.Store.ListEvalRecords(s.effTenant(r, u), 100)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "records": records})
}

// ============ 系统健康 ============

// handleSystemHealth 系统健康看板
func (s *Server) handleSystemHealth(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	total, perLang, _, _ := s.DB.Stats(s.effTenant(r, u))
	balance, _ := s.Store.GetBalance(s.effTenant(r, u))
	usage, _, _ := s.Store.UsageStats(s.effTenant(r, u))
	steps := flowStepsForTenant(s.Ten, s.effTenant(r, u))
	enabled := 0
	for _, st := range steps {
		if st.Enable {
			enabled++
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "health": map[string]interface{}{
		"version":            "2.0.0-go",
		"kb_entries":         total,
		"kb_lang_stats":      perLang,
		"balance":            balance,
		"usage":              usage,
		"flow_steps_enabled": enabled,
		"flow_steps_total":   len(steps),
	}})
}

// handleSystemAudit 审计日志
func (s *Server) handleSystemAudit(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	logs, err := s.Store.ListAudit(s.effTenant(r, u), 100)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "logs": logs})
}

// ============ 计费/充值/用量 ============

// handleBalance 余额查询
func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	b, err := s.Store.GetBalance(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "balance": b})
}

// handleUsage 用量统计
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	usage, total, err := s.Store.UsageStats(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "usage": usage, "total": total})
}

// handleOrders 订单列表
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	orders, err := s.Store.ListOrders(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "orders": orders})
}

// handleOrderCreate 创建充值订单（super_admin）
func (s *Server) handleOrderCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		TenantID int64   `json:"tenant_id"`
		Tokens   int64   `json:"tokens"`
		Money    float64 `json:"money"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tokens <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "tokens 必须大于 0"})
		return
	}
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	o, err := s.Store.CreateOrder(req.TenantID, req.Tokens, req.Money, u.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_create", "orders", o.OrderNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "order": o})
}

// handleOrderPay 确认支付（super_admin，线下转账）
func (s *Server) handleOrderPay(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID       int64 `json:"id"`
		TenantID int64 `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	if err := s.Store.MarkOrderPaid(req.ID, req.TenantID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_pay", "orders", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOrderRefund 退款（super_admin）
func (s *Server) handleOrderRefund(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID       int64 `json:"id"`
		TenantID int64 `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	if err := s.Store.RefundOrder(req.ID, req.TenantID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "order_refund", "orders", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 开放 API Key ============

// handleAPIKeys 列表
func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	keys, err := s.Store.ListAPIKeys(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "keys": keys})
}

// handleAPIKeyCreate 签发
func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Name  string `json:"name"`
		Perms string `json:"perms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "name 不能为空"})
		return
	}
	plain, err := s.Store.CreateAPIKey(s.effTenant(r, u), req.Name, req.Perms)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "apikey_create", "api_keys", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain, "note": "请立即保存，仅显示一次"})
}

// handleAPIKeyStatus 启停
func (s *Server) handleAPIKeyStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.SetAPIKeyStatus(req.ID, s.effTenant(r, u), req.Status); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAPIKeyDelete 删除
func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeleteAPIKey(req.ID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 开放 API（API Key 鉴权） ============

// handleOpenAPITranslate 开放翻译接口
func (s *Server) handleOpenAPITranslate(w http.ResponseWriter, r *http.Request) {
	// 用 openapi 中间件鉴权（需挂载在独立 mux）
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "translate" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "API Key 无翻译权限"})
		return
	}
	var req struct {
		Text        string   `json:"text"`
		TargetLangs []string `json:"target_langs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "text 不能为空"})
		return
	}
	if len(req.TargetLangs) == 0 {
		req.TargetLangs = []string{"en"}
	}
	res := s.Engine.HandleText(r.Context(), req.Text, map[string]interface{}{"target_langs": req.TargetLangs}, nil)
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"translations": res.Data.Translations,
		"sources":      res.Data.TranslationsSource,
		"mode":         res.Data.Mode,
		"reply":        res.Reply,
	})
}

// authenticateAPIKey 从 Authorization 头解析 API Key
func (s *Server) authenticateAPIKey(r *http.Request) (*store.APIKey, bool) {
	if s.Store == nil {
		return nil, false
	}
	h := r.Header.Get("Authorization")
	if len(h) < 8 || h[:7] != "Bearer " {
		return nil, false
	}
	key := h[7:]
	ak, err := s.Store.GetAPIKeyByHash(store.HashAPIKey(key))
	if err != nil || ak.Status != "active" {
		return nil, false
	}
	s.Store.TouchAPIKey(ak.ID)
	return ak, true
}