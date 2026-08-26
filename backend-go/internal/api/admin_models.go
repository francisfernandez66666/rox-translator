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
	"log"
	"net/http"
	"strings"

	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 密钥静态加密辅助（评审整改 D3） ============
//
// 库内 system_config.model_routes / stage_models 的 api_key 一律以 enc:v1: AES-GCM
// 密文落库（密钥派生自 JWT_SECRET）；内存/热同步链路使用明文；前端只见过掩码。

// loadRoutesDecrypted 读取 model_routes 并解密为明文副本。
func (s *Server) loadRoutesDecrypted() []config.ProviderConfig {
	rs := []config.ProviderConfig{}
	if s.Store == nil {
		return rs
	}
	if v, e := s.Store.GetConfig("model_routes"); e == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &rs)
		for i := range rs {
			rs[i].APIKey = store.DecryptSecret(rs[i].APIKey)
			if rs[i].APIKey == "" && strings.HasPrefix(v, "enc:") {
				log.Printf("[models] 路由 %s(%s) 密钥解密失败（疑 JWT_SECRET 轮换未同步），已跳过", rs[i].Provider, rs[i].Model)
			}
		}
	}
	return rs
}

// encryptRoutes 入库前加密副本的 api_key（不改原切片）。
func encryptRoutes(rs []config.ProviderConfig) []config.ProviderConfig {
	out := make([]config.ProviderConfig, len(rs))
	copy(out, rs)
	for i := range out {
		out[i].APIKey = store.EncryptSecret(out[i].APIKey)
	}
	return out
}

// llmKeyState 返回某加密配置键是否已设置及其掩码展示（解密后脱敏）。
func (s *Server) llmKeyState(key string) (bool, string) {
	v, err := s.Store.GetConfig(key)
	if err != nil || v == "" {
		return false, ""
	}
	dec := store.DecryptSecret(v)
	if dec == "" {
		return false, ""
	}
	return true, maskKey(dec)
}

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
	routes := s.loadRoutesDecrypted()
	if routes == nil {
		routes = []config.ProviderConfig{}
	}
	maskedRoutes := make([]config.ProviderConfig, 0, len(routes))
	for _, rt := range routes {
		rt.APIKey = maskKey(rt.APIKey)
		maskedRoutes = append(maskedRoutes, rt)
	}
	embSet, embMask := s.llmKeyState("embed_api_key")
	// 翻译密钥是否已真实配置（占位随机 Key 视为未配置）
	transSet := key != "" && !s.Cfg.OnlineAPIKeyIsPlaceholder
	writeJSON(w, 200, map[string]interface{}{"success": true,
		"model": map[string]interface{}{"api_base": base, "api_key": maskKey(key), "model": model, "set": transSet},
		"embedding": map[string]interface{}{"set": embSet, "masked": embMask, "api_base": s.Cfg.EmbedAPIBase},
		"routes":   maskedRoutes})
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
		APIBase      string                  `json:"api_base"`      // 模型 API 基础地址（可为空=不修改）
		APIKey       string                  `json:"api_key"`       // 模型 API Key（掩码值不覆盖原密钥）
		Model        string                  `json:"model"`         // 模型名称
		Routes       []config.ProviderConfig `json:"routes"`        // 多供应商路由（可为空=清空；平台统一网关多供应商调度）
		EmbedAPIKey  string                  `json:"embed_api_key"` // ★ KB 向量重建 Embedding Key（掩码值不覆盖）
		EmbedAPIBase string                  `json:"embed_api_base"`
		ClearKeys    []string                `json:"clear_keys"` // ★ 清空指定密钥作用域：["translation","embedding"]
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
	// 读取现有全局路由并解密（库内为 enc:v1: 密文或历史明文，回填需明文）
	oldRoutes := s.loadRoutesDecrypted()
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
	// 掩码密钥回填（★ 2026-08-26 修复脆弱匹配）：
	//   旧逻辑按「api_base+model 双字段相等」找旧路由——管理员只改 model 名即匹配失败，
	//   掩码串（如 sk-a****xyz）会被当真实 Key 入库，路由静默坏死。
	//   新规则：掩码 = 未修改 ⇒ 按位置对齐回填。合并列表结构与库内一致
	//   （[0]=单模型主路由(可省)，其后为 routes 全量），同一下标即同一条路由，
	//   前端整表回传时顺序天然保持。
	for i := range merged {
		if hasMask(merged[i].APIKey) && i < len(oldRoutes) {
			merged[i].APIKey = oldRoutes[i].APIKey // 回填明文旧值
		}
	}
	// ★ 热同步用明文；入库前整体加密（评审整改 D3：库内不再存任何明文供应商 Key）
	s.Cfg.ModelRoutes = merged
	b, _ := json.Marshal(encryptRoutes(merged))
	if err := s.Store.SetConfig("model_routes", string(b)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 若单模型字段非空，同时更新全局默认单模型（引擎回退链的最终兜底）
	if req.APIBase != "" {
		s.Cfg.OnlineAPIBase = req.APIBase
	}
	if req.APIKey != "" && !hasMask(req.APIKey) {
		s.Cfg.OnlineAPIKey = req.APIKey
		s.Cfg.OnlineAPIKeyIsPlaceholder = false
	}
	if req.Model != "" {
		s.Cfg.OnlineModel = req.Model
	}
	// ★ 后台可配 LLM Key：覆盖环境变量、重启后水合生效（需求：翻译/工单任务 + KB 向量重建）
	// 清空作用域优先（clear_keys 指定的密钥置回占位/删除）
	for _, sc := range req.ClearKeys {
		if sc == "translation" {
			_ = s.Store.SetConfig("online_api_key", "")
			_ = s.Store.SetConfig("online_api_base", "")
			_ = s.Store.SetConfig("online_model", "")
			s.Cfg.OnlineAPIKey = ""
			s.Cfg.OnlineAPIKeyIsPlaceholder = true
		}
		if sc == "embedding" {
			_ = s.Store.SetConfig("embed_api_key", "")
			_ = s.Store.SetConfig("embed_api_base", "")
			s.Cfg.EmbedAPIKey = ""
		}
	}
	// 翻译 Key 持久化（仅在非空且非掩码时写入；掩码=未修改保留原值）
	if req.APIKey != "" && !hasMask(req.APIKey) {
		_ = s.Store.SetConfig("online_api_key", store.EncryptSecret(req.APIKey))
	}
	if req.APIBase != "" {
		_ = s.Store.SetConfig("online_api_base", req.APIBase)
	}
	if req.Model != "" {
		_ = s.Store.SetConfig("online_model", req.Model)
	}
	// Embedding Key 持久化（KB 向量重建）
	if req.EmbedAPIKey != "" && !hasMask(req.EmbedAPIKey) {
		s.Cfg.EmbedAPIKey = req.EmbedAPIKey
		_ = s.Store.SetConfig("embed_api_key", store.EncryptSecret(req.EmbedAPIKey))
	}
	if req.EmbedAPIBase != "" {
		s.Cfg.EmbedAPIBase = req.EmbedAPIBase
		_ = s.Store.SetConfig("embed_api_base", req.EmbedAPIBase)
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "model_save", "system", fmt.Sprintf("%d 条全局路由", len(merged)))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleModelRoutes 读取模型路由策略（super_admin）。★ 输出掩码（评审整改 D3）
func (s *Server) handleModelRoutes(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	routes := s.loadRoutesDecrypted()
	masked := make([]config.ProviderConfig, len(routes))
	for i, rt := range routes {
		rt.APIKey = maskKey(rt.APIKey)
		masked[i] = rt
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "routes": masked})
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
	// 掩码密钥不覆盖：保留原值（旧库读出后先解密再回填）
	if len(req.Routes) > 0 {
		old := s.loadRoutesDecrypted()
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
	// ★ 热同步明文；入库加密（评审整改 D3）
	s.Cfg.ModelRoutes = req.Routes
	b, _ := json.Marshal(encryptRoutes(req.Routes))
	if err := s.Store.SetConfig("model_routes", string(b)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "model_routes_save", "system", fmt.Sprintf("%d 条", len(req.Routes)))
	writeJSON(w, 200, map[string]interface{}{"success": true})
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
			// ★ 库内密文 → 明文后再掩码输出（评审整改 D3）
			for k := range stages {
				sm := stages[k]
				sm.APIKey = store.DecryptSecret(sm.APIKey)
				stages[k] = sm
			}
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
	// 读取旧配置并解密，掩码密钥保留原值
	old := config.StageModels{}
	if v, err := s.Store.GetConfig("stage_models"); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &old)
		for k := range old {
			sm := old[k]
			sm.APIKey = store.DecryptSecret(sm.APIKey)
			old[k] = sm
		}
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
	// ★ 入库前整体加密（评审整改 D3）；引擎读取侧 resolveStageModel 做对应解密
	stored := config.StageModels{}
	for k, sm := range req.Stages {
		sm.APIKey = store.EncryptSecret(sm.APIKey)
		stored[k] = sm
	}
	b, _ := json.Marshal(stored)
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
	// ★ 跨部门降级检索开关（2026-08-26 KB继承链）：nil=默认开；输出解析后的布尔供前端渲染
	cross := true
	if pc.CrossDeptFallback != nil {
		cross = *pc.CrossDeptFallback == 1
	}
	// ★ 数据回流开关（评审整改 D7）：默认参与共建
	feedbackOut := pc.DataFeedbackOptOut != nil && *pc.DataFeedbackOptOut == 1
	writeJSON(w, 200, map[string]interface{}{"success": true, "policy": map[string]interface{}{
		"high_sim":             high,
		"med_sim":              med,
		"evals_pass_threshold": evals,
		"cross_dept_fallback":  cross,
		"data_feedback_opt_out": feedbackOut,
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
		Policy             map[string]float64 `json:"policy"`
		CrossDeptFallback  *bool              `json:"cross_dept_fallback"`  // 跨部门降级检索（nil=不修改）
		DataFeedbackOptOut *bool              `json:"data_feedback_opt_out"` // ★ 数据回流关闭开关（D7；nil=不修改）
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
	// ★ 跨部门开关：显式传入才修改（bool→*int 三态存储）
	if req.CrossDeptFallback != nil {
		v := 0
		if *req.CrossDeptFallback {
			v = 1
		}
		pc.CrossDeptFallback = &v
	}
	if req.DataFeedbackOptOut != nil {
		v := 0
		if *req.DataFeedbackOptOut {
			v = 1
		}
		pc.DataFeedbackOptOut = &v
	}
	if err := s.Ten.SetPolicyConfig(s.effTenant(r, u), pc); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "policy_save", "tenants", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
