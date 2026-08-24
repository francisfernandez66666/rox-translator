// ============ admin_models.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 模型配置 / 模型路由策略 / 策略参数（handleModels / handleModelRoutes / handlePolicy 系列）
// 安全要点：全部接口仅超管可访问（requireAdminUser）；模型与策略属平台级配置，
// 租户管理员无权读写。所有写操作均记录审计日志（LogAudit）。
// ========================================

import (
	"encoding/json"
	"fmt"
	"net/http"
	"translator/internal/config"
	"translator/internal/tenant"
)

// ============ 模型配置（仅超管） ============

// handleModels 读取模型配置（仅超管）：
// 读取全局配置（全局默认单模型 + system_config.model_routes 全局路由）。
//
// 返回 model 单模型 + routes 多供应商路由。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 读全局配置
	base := s.Cfg.OnlineAPIBase
	key := s.Cfg.OnlineAPIKey
	model := s.Cfg.OnlineModel
	var routes []tenant.Route
	if s.Store != nil {
		if v, e := s.Store.GetConfig("model_routes"); e == nil && v != "" {
			var rs []config.ProviderConfig
			if json.Unmarshal([]byte(v), &rs) == nil {
				for _, rt := range rs {
					routes = append(routes, tenant.Route{Provider: rt.Provider, APIBase: rt.APIBase, APIKey: rt.APIKey, Model: rt.Model, Weight: rt.Weight})
				}
			}
		}
	}
	if routes == nil {
		routes = []tenant.Route{}
	}
	maskedRoutes := make([]tenant.Route, 0, len(routes))
	for _, rt := range routes {
		rt.APIKey = maskKey(rt.APIKey)
		maskedRoutes = append(maskedRoutes, rt)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true,
		"model":  map[string]interface{}{"api_base": base, "api_key": maskKey(key), "model": model},
		"routes": maskedRoutes})
}

// handleModelsSave 保存模型配置（仅超管）：
// 保存全局配置——单模型字段（api_base+model）作为主路由合并写入 model_routes，
// 并热更新运行配置；routes 全量覆盖全局路由。
//
// 支持多供应商路由（ChatGPT/Gemini 等 OpenAI 兼容端点）。
func (s *Server) handleModelsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		APIBase string         `json:"api_base"` // 模型 API 基础地址（可为空=不修改）
		APIKey  string         `json:"api_key"`  // 模型 API Key（掩码值不覆盖原密钥）
		Model   string         `json:"model"`    // 模型名称
		Routes  []tenant.Route `json:"routes"`   // 多供应商路由（可为空=清空；ChatGPT/Gemini 等）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 保存全局配置
	if s.Store == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 读取现有全局路由（保留掩码密钥）
	oldRoutes := []config.ProviderConfig{}
	if v, e := s.Store.GetConfig("model_routes"); e == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &oldRoutes)
	}
	// 构建新路由列表：单模型字段优先作为主路由（api_base+model 非空），其余来自 routes
	merged := make([]config.ProviderConfig, 0, len(req.Routes)+1)
	if req.APIBase != "" && req.Model != "" {
		merged = append(merged, config.ProviderConfig{
			Provider: "global", APIBase: req.APIBase, APIKey: req.APIKey, Model: req.Model, Weight: 100,
		})
	}
	for _, rt := range req.Routes {
		merged = append(merged, config.ProviderConfig{Provider: rt.Provider, APIBase: rt.APIBase, APIKey: rt.APIKey, Model: rt.Model, Weight: rt.Weight})
	}
	// 掩码密钥保留原值
	for i := range merged {
		if hasMask(merged[i].APIKey) {
			for _, o := range oldRoutes {
				if o.APIBase == merged[i].APIBase && o.Model == merged[i].Model {
					merged[i].APIKey = o.APIKey
					break
				}
			}
		}
	}
	b, _ := json.Marshal(merged)
	if err := s.Store.SetConfig("model_routes", string(b)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 热同步到运行配置（引擎即时生效）
	s.Cfg.ModelRoutes = merged
	// 若单模型字段非空，同时更新全局默认单模型（引擎回退链的最终兜底）
	if req.APIBase != "" {
		s.Cfg.OnlineAPIBase = req.APIBase
	}
	if req.APIKey != "" && !hasMask(req.APIKey) {
		s.Cfg.OnlineAPIKey = req.APIKey
	}
	if req.Model != "" {
		s.Cfg.OnlineModel = req.Model
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "model_save", "system", fmt.Sprintf("%d 条全局路由", len(merged)))
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

// handleModelRoutesSave 保存模型路由策略（仅超管）
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

// ============ 各流程阶段模型配置（仅超管） ============

// handleStageModels 读取各流程阶段模型配置（仅超管）。
// 返回 4 个阶段（kb_match/ai_initial/evals/review）的模型配置；未配置的返回空项以便前端渲染。
func (s *Server) handleStageModels(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	stages := config.StageModels{}
	if s.Store != nil {
		if v, err := s.Store.GetConfig("stage_models"); err == nil && v != "" {
			_ = json.Unmarshal([]byte(v), &stages)
		}
	}
	// 掩码所有 API Key 再返回（密钥仅保存后返回一次）；输出业务五阶段
	out := config.StageModels{}
	for _, k := range []string{config.StageAIInitial, config.StageKBEmbed, config.StageInitialEvals, config.StageReview, config.StageReviewEvals} {
		sm := stages[k]
		out[k] = config.StageModel{
			Provider: sm.Provider,
			APIBase:  sm.APIBase,
			APIKey:   maskKey(sm.APIKey),
			Model:    sm.Model,
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "stages": out})
}

// handleStageModelsSave 保存各流程阶段模型配置（仅超管）。
// 覆盖式保存：全量提交；某项 api_base/model 为空表示清空该阶段独立模型（回退全局/路由）。
func (s *Server) handleStageModelsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Stages config.StageModels `json:"stages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Store == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 读取旧配置，掩码密钥保留原值
	old := config.StageModels{}
	if v, err := s.Store.GetConfig("stage_models"); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &old)
	}
	for k := range req.Stages {
		sm := req.Stages[k]
		if sm.APIBase == "" && sm.Model == "" {
			// 清空该阶段 → 删除键
			delete(req.Stages, k)
			continue
		}
		if sm.APIBase == "" || sm.Model == "" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": fmt.Sprintf("阶段 %s 缺少 api_base 或 model", k)})
			return
		}
		if sm.Provider == "" {
			req.Stages[k] = config.StageModel{Provider: "stage_" + k, APIBase: sm.APIBase, APIKey: sm.APIKey, Model: sm.Model}
			sm = req.Stages[k]
		}
		if hasMask(sm.APIKey) {
			if o, ok := old[k]; ok {
				req.Stages[k] = config.StageModel{Provider: sm.Provider, APIBase: sm.APIBase, APIKey: o.APIKey, Model: sm.Model}
			} else {
				req.Stages[k] = config.StageModel{Provider: sm.Provider, APIBase: sm.APIBase, APIKey: "", Model: sm.Model}
			}
		}
	}
	b, _ := json.Marshal(req.Stages)
	if err := s.Store.SetConfig("stage_models", string(b)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "stage_models_save", "system", fmt.Sprintf("%d 阶段", len(req.Stages)))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 策略参数（仅超管） ============

// handlePolicy 读取翻译策略参数（仅超管；经 X-Tenant-ID 切换生效租户，未配置回退全局默认）
func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
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

// handlePolicySave 保存翻译策略参数（仅超管）
func (s *Server) handlePolicySave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
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
