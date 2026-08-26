// ============ admin_llmkey.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 后台「LLM Key 可配」：让翻译/工单任务（在线翻译 Online Key）与 KB 向量重建
// （Embedding Key）的密钥可由超级管理员在后台随时设置，而非仅依赖启动环境变量。
//   - GET  /api/admin/llm-key       读取当前配置（密钥仅返回掩码 + 是否已设置）
//   - POST /api/admin/llm-key/save  设置密钥/网关/模型（明文入库前加密；掩码值不覆盖原值）
//   - POST /api/admin/llm-key/clear 清除指定作用域密钥（恢复占位，翻译将不可用直至重设）
// 安全：仅 super_admin（requireAdminUser）；密钥以 enc:v1: 密文落库（评审整改 D3）；
//       写操作记审计。设置的密钥热同步到运行配置（s.Cfg），即时生效并随 system_config 持久化、重启后水合。
// =============================================

import (
	"encoding/json"
	"net/http"

	"translator/internal/store"
)

// handleAdminLLMKeyGet 读取当前 LLM Key 配置（仅超管；密钥脱敏）。
func (s *Server) handleAdminLLMKeyGet(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	transSet, transMask := s.llmKeyState("online_api_key")
	embedSet, embedMask := s.llmKeyState("embed_api_key")
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"translation": map[string]interface{}{
			"set":      transSet,
			"masked":   transMask,
			"api_base": s.Cfg.OnlineAPIBase,
			"model":    s.Cfg.OnlineModel,
		},
		"embedding": map[string]interface{}{
			"set":      embedSet,
			"masked":   embedMask,
			"api_base": s.Cfg.EmbedAPIBase,
		},
	})
}

// llmKeyState 返回某加密配置键是否已设置及其掩码展示。
// 参数 key: system_config 键名（密文）；返回 (是否已设置, 掩码串)。
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

// handleAdminLLMKeySave 设置 LLM Key（仅超管；即时生效 + 持久化）。
// body: {api_key, api_base, model, embed_api_key, embed_api_base}
// 任一字段为空则不改；掩码值（含 ****）视为「未修改」保留原值。
func (s *Server) handleAdminLLMKeySave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		APIKey       string `json:"api_key"`       // 在线翻译 Key（翻译/工单任务）
		APIBase      string `json:"api_base"`      // 在线翻译网关（空=不改）
		Model        string `json:"model"`         // 在线翻译模型（空=不改）
		EmbedAPIKey  string `json:"embed_api_key"` // Embedding Key（KB 向量重建）
		EmbedAPIBase string `json:"embed_api_base"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 翻译 Key
	if req.APIKey != "" && !hasMask(req.APIKey) {
		s.Cfg.OnlineAPIKey = req.APIKey
		s.Cfg.OnlineAPIKeyIsPlaceholder = false
		if err := s.Store.SetConfig("online_api_key", store.EncryptSecret(req.APIKey)); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": "保存翻译 Key 失败: " + err.Error()})
			return
		}
	}
	if req.APIBase != "" {
		s.Cfg.OnlineAPIBase = req.APIBase
		_ = s.Store.SetConfig("online_api_base", req.APIBase)
	}
	if req.Model != "" {
		s.Cfg.OnlineModel = req.Model
		_ = s.Store.SetConfig("online_model", req.Model)
	}
	// Embedding Key
	if req.EmbedAPIKey != "" && !hasMask(req.EmbedAPIKey) {
		s.Cfg.EmbedAPIKey = req.EmbedAPIKey
		if err := s.Store.SetConfig("embed_api_key", store.EncryptSecret(req.EmbedAPIKey)); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": "保存 Embedding Key 失败: " + err.Error()})
			return
		}
	}
	if req.EmbedAPIBase != "" {
		s.Cfg.EmbedAPIBase = req.EmbedAPIBase
		_ = s.Store.SetConfig("embed_api_base", req.EmbedAPIBase)
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "llm_key_save", "system", "后台设置 LLM Key")
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "LLM Key 已保存并即时生效"})
}

// handleAdminLLMKeyClear 清除密钥（仅超管）。body: {scope: "translation"|"embedding"|"all"}（默认 all）。
func (s *Server) handleAdminLLMKeyClear(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Scope string `json:"scope"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	scope := req.Scope
	if scope == "" {
		scope = "all"
	}
	if scope == "all" || scope == "translation" {
		_ = s.Store.SetConfig("online_api_key", "")
		_ = s.Store.SetConfig("online_api_base", "")
		_ = s.Store.SetConfig("online_model", "")
		s.Cfg.OnlineAPIKey = ""
		s.Cfg.OnlineAPIKeyIsPlaceholder = true
	}
	if scope == "all" || scope == "embedding" {
		_ = s.Store.SetConfig("embed_api_key", "")
		_ = s.Store.SetConfig("embed_api_base", "")
		s.Cfg.EmbedAPIKey = ""
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "llm_key_clear", "system", "清除 LLM Key scope="+scope)
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已清除 " + scope + " 密钥"})
}
