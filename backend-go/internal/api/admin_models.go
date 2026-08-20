package api

// ============ 本文件职责中文说明 ============
// 模型配置 / 模型路由策略 / 策略参数（handleModels / handleModelRoutes / handlePolicy 系列）
// 安全要点：所有写操作均记录审计日志（LogAudit）；API Key 密钥仅明文返回一次，前端立即保存。
// ========================================

import (
	"encoding/json"
	"fmt"
	"net/http"
	"translator/internal/config"
	"translator/internal/tenant"
)

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
