package api

// ============ 本文件职责中文说明 ============
// 本文件实现管理后台（Admin Dashboard）的配置与运营类接口：
//   - 权限鉴权助手：requireAdminUser（super_admin 超管）/ requireTenantAdmin（租户管理员及以上）
//   - 流程引擎设置：读取/保存租户流程步骤启停（handleFlowConfig / handleFlowSave）、直接触发工单流程（handleFlowRunTicket）
//   - 模型配置：读取/保存租户模型（handleModels / handleModelsSave）、全局模型路由策略（handleModelRoutes / handleModelRoutesSave，super_admin）
//   - 策略参数：读取/保存租户相似度阈值与评估通过线（handlePolicy / handlePolicySave）
//   - KB 包管理（行业包）：包与条目的增删改查、安全句维护（handleKBPackages / handleKBEntries / handleSafetyPhrases 系列）
//   - evals 看板：评估记录列表（handleEvalsList）
//   - 系统健康：健康看板 / 审计日志（含 CSV 导出）/ 告警管理（handleSystemHealth / handleSystemAudit / handleAlerts 系列）
//   - 计费/充值/用量：余额查询、用量统计、订单（充值/支付/退款）、发票开具
//   - 开放 API Key：签发 / 启停 / 轮换 / 删除（handleAPIKeys 系列）
//   - 开放 API（API Key 鉴权）：翻译 / KB 统计 / 用量查询 / Key 轮换 / 文档（handleOpenAPI 系列）
// 安全要点：
//   - 所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存
//   - 模型路由与策略配置读取时回退全局默认；API Key 掩码显示/掩码提交不覆盖原密钥

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/engine"
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

type apiErr struct{ s string } // s: 错误描述信息（用于返回给前端的错误消息）

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
		Steps []store.FlowStep `json:"steps"` // 流程步骤启停配置数组（含 key/name/enable）
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
		ID int64 `json:"id"` // 待执行流程的工单 ID
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
		APIBase string `json:"api_base"` // 模型 API 基础地址（可为空=不修改）
		APIKey  string `json:"api_key"`  // 模型 API Key（掩码值不覆盖原密钥）
		Model   string `json:"model"`    // 模型名称（必填）
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

// handleModelRoutes 读取模型路由策略（super_admin）
func (s *Server) handleModelRoutes(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var routes []config.ProviderConfig
	if s.Store != nil {
		if v, err := s.Store.GetConfig("model_routes"); err == nil && v != "" {
			_ = json.Unmarshal([]byte(v), &routes)
		}
	}
	if routes == nil {
		routes = []config.ProviderConfig{}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "routes": routes})
}

// handleModelRoutesSave 保存模型路由策略（super_admin）
// 覆盖式保存：全量提交，空数组表示清空路由回退单供应商 Online* 配置。
func (s *Server) handleModelRoutesSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Routes []config.ProviderConfig `json:"routes"` // 模型路由全量配置（空数组=清空路由回退单供应商）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 校验：非空时必须每条含 api_base/model/api_key
	for i, rt := range req.Routes {
		if rt.APIBase == "" || rt.Model == "" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": fmt.Sprintf("第 %d 条路由缺少 api_base/model", i+1)})
			return
		}
		if rt.Provider == "" {
			req.Routes[i].Provider = "global"
		}
	}
	// 掩码密钥不覆盖：保留原值
	if len(req.Routes) > 0 {
		if v, err := s.Store.GetConfig("model_routes"); err == nil && v != "" {
			var old []config.ProviderConfig
			if json.Unmarshal([]byte(v), &old) == nil {
				for i := range req.Routes {
					if hasMask(req.Routes[i].APIKey) {
						for _, o := range old {
							if o.APIBase == req.Routes[i].APIBase && o.Model == req.Routes[i].Model {
								req.Routes[i].APIKey = o.APIKey
								break
							}
						}
					}
				}
			}
		}
	}
	b, _ := json.Marshal(req.Routes)
	if err := s.Store.SetConfig("model_routes", string(b)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 同步到运行配置（引擎热生效）
	s.Cfg.ModelRoutes = req.Routes
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "model_routes_save", "system", fmt.Sprintf("%d 条", len(req.Routes)))
	writeJSON(w, 200, map[string]interface{}{"success": true, "routes": req.Routes})
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
		"high_sim":             high,
		"med_sim":              med,
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
		Policy map[string]float64 `json:"policy"` // 策略参数映射（high_sim/med_sim/evals_pass_threshold）
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
		ID   int64  `json:"id"`   // 目标包 ID
		Name string `json:"name"` // 新包名称
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
		ID int64 `json:"id"` // 待删除包 ID
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
		ID int64 `json:"id"` // 待删除条目 ID
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
		ID int64 `json:"id"` // 待删除安全句 ID
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
		"breaker_open":       s.Engine != nil && s.Engine.BreakerOpen(),
		"llm_error_rate":     llmErrorRateStr(s.Engine),
	}})
}

// llmErrorRateStr 生成 LLM 错误率展示字符串（无样本显示 0.0%）
func llmErrorRateStr(eng *engine.Engine) string {
	if eng == nil {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", eng.ErrorRate()*100)
}

// handleSystemAudit 审计日志（支持 action/resource/user/time 过滤；export=csv 导出）
func (s *Server) handleSystemAudit(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	q := r.URL.Query()
	logs, err := s.Store.ListAuditFilter(
		s.effTenant(r, u),
		q.Get("action"),
		q.Get("resource"),
		atol(q.Get("user_id")),
		q.Get("from"),
		q.Get("to"),
		atoiDef(q.Get("limit"), 100),
	)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// CSV 导出（ISO 17100 审计留痕）
	if q.Get("export") == "csv" {
		s.exportAuditCSV(w, logs)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "logs": logs})
}

// handleAlerts 告警列表（租户管理员看本租户，超管看全平台）
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	if u.Role == "super_admin" || u.Role == "admin" {
		tid = 0 // 超管看全平台
	}
	q := r.URL.Query()
	status := q.Get("status")
	alerts, err := s.Store.ListAlerts(tid, status, atoiDef(q.Get("limit"), 100))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "alerts": alerts})
}

// handleAlertResolve 关闭告警
func (s *Server) handleAlertResolve(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待关闭告警 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.ResolveAlert(req.ID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "alert_resolve", "alerts", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// exportAuditCSV 导出审计日志为 CSV
func (s *Server) exportAuditCSV(w http.ResponseWriter, logs []*store.AuditLog) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=audit.csv")
	var sb strings.Builder
	sb.WriteString("\xEF\xBB\xBF") // UTF-8 BOM（Excel 兼容）
	sb.WriteString("id,tenant_id,user_id,action,resource,detail,before_val,after_val,created_at\n")
	for _, a := range logs {
		sb.WriteString(fmt.Sprintf("%d,%d,%d,%s,%s,%s,%s,%s,%s\n",
			a.ID, a.TenantID, a.UserID, csvEscape(a.Action), csvEscape(a.Resource), csvEscape(a.Detail),
			csvEscape(a.BeforeVal), csvEscape(a.AfterVal), csvEscape(a.CreatedAt)))
	}
	_, _ = w.Write([]byte(sb.String()))
}

// csvEscape CSV 字段转义（逗号/引号/换行）
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// atol 解析 int64（非法返回 0）
func atol(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// atoiDef 解析 int，非法返回默认值
func atoiDef(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
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
	// 多供应商成本核算：按 provider 拆分用量（平台超管可查看全平台汇总）
	providerUsage, err := s.Store.UsageStatsByProvider(s.effTenant(r, u))
	if err != nil {
		providerUsage = map[string]int64{}
	}
	// 用量趋势（最近 7 天）
	trend, err := s.Store.UsageTrend(s.effTenant(r, u), 7)
	if err != nil {
		trend = map[string]int64{}
	}
	// 用量明细（分页）
	ledger, err := s.Store.UsageLedgerList(s.effTenant(r, u), atoiDef(r.URL.Query().Get("limit"), 50), int(atol(r.URL.Query().Get("offset"))))
	if err != nil {
		ledger = []*store.UsageLedger{}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "usage": usage, "total": total,
		"provider_usage": providerUsage, "trend": trend, "ledger": ledger,
	})
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

// handleOrderCreate 创建充值订单（super_admin 为任意租户 / tenant_admin 为本租户自助充值）
func (s *Server) handleOrderCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		TenantID int64   `json:"tenant_id"` // 充值目标租户（0=当前生效租户）
		Tokens   int64   `json:"tokens"`    // 充值 token 数量（必填，>0）
		Money    float64 `json:"money"`     // 充值金额（元，可选记录）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tokens <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "tokens 必须大于 0"})
		return
	}
	if req.TenantID <= 0 {
		req.TenantID = s.effTenant(r, u)
	}
	// 租户管理员只能为自己租户提交充值申请（super_admin 可代任意租户）
	if !auth.IsSuperAdmin(u) && req.TenantID != s.effTenant(r, u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "权限不足：只能为本租户充值"})
		return
	}
	o, err := s.Store.CreateOrder(req.TenantID, req.Tokens, req.Money, u.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 自助充值即时到账模式：system_config auto_charge=1 时创建订单即确认到账（内网/测试模式）
	if v, _ := s.Store.GetConfig("auto_charge"); v == "1" {
		if err := s.Store.MarkOrderPaid(o.ID, req.TenantID); err == nil {
			o.Status = "paid"
		}
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
		ID       int64 `json:"id"`        // 待确认支付订单 ID
		TenantID int64 `json:"tenant_id"` // 订单归属租户（0=当前生效租户）
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
		ID       int64 `json:"id"`        // 待退款订单 ID
		TenantID int64 `json:"tenant_id"` // 订单归属租户（0=当前生效租户）
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

// ============ 发票 ============

// handleInvoices 发票列表（租户管理员）
func (s *Server) handleInvoices(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	inv, err := s.Store.ListInvoices(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "invoices": inv})
}

// handleInvoiceCreate 为已支付订单开具发票（租户管理员，限本租户已支付订单）
func (s *Server) handleInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		OrderID int64  `json:"order_id"` // 已支付订单 ID（仅限本租户）
		Title   string `json:"title"`    // 发票抬头
		TaxNo   string `json:"tax_no"`   // 税号
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrderID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供订单 id"})
		return
	}
	inv, err := s.Store.CreateInvoice(s.effTenant(r, u), req.OrderID, req.Title, req.TaxNo)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "开票失败（需订单已支付）: " + err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "invoice_create", "billing", inv.InvoiceNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "invoice": inv})
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
		Name  string `json:"name"`  // Key 名称（便于管理识别）
		Perms string `json:"perms"` // 权限范围（all/translate/kb/billing）
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
		ID     int64  `json:"id"`     // 目标 API Key ID
		Status string `json:"status"` // 目标状态：active（启用）/ disabled（停用）
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

// handleAPIKeyRotate 轮换 API Key（本租户，旧 Key 立即失效）
func (s *Server) handleAPIKeyRotate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待轮换 API Key ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	tid := s.effTenant(r, u)
	old, err := s.Store.GetAPIKey(req.ID, tid)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "Key 不存在"})
		return
	}
	if err := s.Store.DeleteAPIKey(req.ID, tid); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	plain, err := s.Store.CreateAPIKey(tid, old.Name, old.Perms)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(tid, u.ID, "apikey_rotate", "api_keys", old.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "api_key": plain, "note": "旧 Key 已失效，新 Key 仅显示一次"})
}

// handleAPIKeyDelete 删除
func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除 API Key ID
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

// handleOpenAPIDocs 开放 API 文档（静态 HTML）
func (s *Server) handleOpenAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8">
<title>翻译平台开放 API 文档</title>
<style>body{font-family:-apple-system,Segoe UI,sans-serif;max-width:900px;margin:30px auto;padding:0 20px;color:#222}
h1{border-bottom:2px solid #1a237e;padding-bottom:8px}code{background:#f0f0f0;padding:2px 6px;border-radius:4px}
pre{background:#f6f8fa;padding:12px;border-radius:8px;overflow:auto}
table{border-collapse:collapse;width:100%;margin:10px 0}th,td{border:1px solid #ddd;padding:8px;text-align:left;font-size:14px}th{background:#fafbfd}
.badge{display:inline-block;background:#e8eaf6;color:#1a237e;border-radius:4px;padding:2px 8px;font-size:12px}</style></head><body>
<h1>翻译平台开放 API</h1>
<p>所有接口使用 <code>Authorization: Bearer &lt;API_KEY&gt;</code> 认证，API Key 在管理后台「API Key」面板签发。</p>
<table><tr><th>方法</th><th>路径</th><th>权限</th><th>说明</th></tr>
<tr><td>POST</td><td>/openapi/v1/translate</td><td class="badge">translate/all</td><td>文本翻译</td></tr>
<tr><td>GET</td><td>/openapi/v1/kb/stats</td><td class="badge">kb/all</td><td>知识库条目统计</td></tr>
<tr><td>GET</td><td>/openapi/v1/billing/usage</td><td class="badge">billing/all</td><td>用量与余额</td></tr>
<tr><td>POST</td><td>/openapi/v1/apikey/rotate</td><td class="badge">all</td><td>轮换 API Key（旧 Key 立即失效）</td></tr>
</table>
<h2>翻译请求示例</h2>
<pre>curl -X POST https://<span>域名</span>/openapi/v1/translate \
  -H "Authorization: Bearer &lt;API_KEY&gt;" \
  -H "Content-Type: application/json" \
  -d '{"text":"请检查制动系统","target_langs":["en","de"]}'</pre>
<p>响应：<code>translations</code> 为目标语言→译文映射，<code>sources</code> 标记来源（kb/ai）。</p>
</body></html>`))
}

// handleOpenAPITranslate 开放翻译接口
func (s *Server) handleOpenAPITranslate(w http.ResponseWriter, r *http.Request) {
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
		Text        string   `json:"text"`         // 待翻译源文本（必填）
		TargetLangs []string `json:"target_langs"` // 目标语言列表（默认 ["en"]）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "text 不能为空"})
		return
	}
	if len(req.TargetLangs) == 0 {
		req.TargetLangs = []string{"en"}
	}
	// ★ 配额闸门（openapi 请求租户来自 API Key）
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": gateErr.Error()})
		return
	}
	res := s.Engine.HandleText(r.Context(), req.Text, map[string]interface{}{"target_langs": req.TargetLangs}, nil)
	if res.Error == "" {
		// ★ 计量：开放 API 翻译按源文本字符数计量
		s.meterUsage(r, tid, "translate", int64(len([]rune(req.Text))))
		s.metrics.countTranslate("openapi", true)
	} else {
		s.metrics.countTranslate("openapi", false)
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":      true,
		"translations": res.Data.Translations,
		"sources":      res.Data.TranslationsSource,
		"mode":         res.Data.Mode,
		"reply":        res.Reply,
	})
}

// handleOpenAPIKBStats 开放接口：查询本租户知识库统计（需要 kb/all 权限）
func (s *Server) handleOpenAPIKBStats(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "kb" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "API Key 无知识库权限"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":    true,
		"tenant_id":  ak.TenantID,
		"kb_entries": s.kbStats(ak.TenantID),
	})
}

// kbStats 统计租户知识库条目数（安全封装：DB 为 nil 时返回 0）
func (s *Server) kbStats(tid int64) int64 {
	if s.DB == nil {
		return 0
	}
	total, _, _, _ := s.DB.Stats(tid)
	return total
}

// handleOpenAPIUsage 开放接口：查询本租户用量与余额（需要 billing/all 权限）
func (s *Server) handleOpenAPIUsage(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "billing" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "API Key 无计费权限"})
		return
	}
	usage, total, _ := s.Store.UsageStats(ak.TenantID)
	balance, _ := s.Store.GetBalance(ak.TenantID)
	writeJSON(w, 200, map[string]interface{}{
		"success":   true,
		"tenant_id": ak.TenantID,
		"usage":     usage,
		"total":     total,
		"balance":   balance,
	})
}

// handleOpenAPIKeyRotate 开放接口：轮换本租户 API Key（传入旧 key 换取新 key）
func (s *Server) handleOpenAPIKeyRotate(w http.ResponseWriter, r *http.Request) {
	ak, ok := s.authenticateAPIKey(r)
	if !ok {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "API Key 无效"})
		return
	}
	// 轮换：删除旧 key 并签发同权限新 key
	if err := s.Store.DeleteAPIKey(ak.ID, ak.TenantID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	newKey, err := s.Store.CreateAPIKey(ak.TenantID, ak.Name, ak.Perms)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "message": "API Key 已轮换，旧 Key 立即失效",
		"api_key": newKey, "name": ak.Name, "perms": ak.Perms,
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
