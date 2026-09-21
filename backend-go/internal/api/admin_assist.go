// ============ admin_assist.go · 职责说明 ============
// 主后台 ↔ AI 助手（assist）管理台鉴权贯通：Token 下发与轮换（★ 改造 1A，2026-09-17）。
//
// 背景：ai-assist 融合进主仓前是独立服务，管理台要求用户手工粘贴 ASSIST_ADMIN_TOKEN
// （提示语「首次使用需在右上角填入管理 Token」），与主后台 SSO 体验断层。
// 现由主后台统一托管该 Token：密文落 system_config.assist_admin_token（enc:v1:，
// 复用 store.EncryptSecret），仅超管可读取/轮换；前端 AssistP 拉到后注入 iframe，免手填。
//
// 优先级口径（与 assist-server 侧一致，避免两处读到不同值）：
//  1. env ASSIST_ADMIN_TOKEN —— 部署侧显式配置，优先级最高（保底，运维可绕过库配置）；
//  2. system_config.assist_admin_token —— 库内密文，管理台可轮换。
//
// 安全要点：仅 requireAdminUser（super_admin）可访问；读取与轮换均记审计。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"translator/internal/store"
)

// assistAdminTokenKey system_config 中存放 AI 助手管理台 Token 的键名。
const assistAdminTokenKey = "assist_admin_token"

// handleAdminAssistToken AI 助手管理台 Token 的读取/轮换（仅超管）。
//
//	GET  → 返回生效 Token 明文 + 来源（env/db/none），前端 AssistP 注入 iframe 用；
//	POST → 轮换 Token（body {"token":"..."}，空串表示清除库内配置并回落 env）。
//
// 返回：success=true 时携带 token / source / has_token。
func (s *Server) handleAdminAssistToken(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	switch r.Method {
	case http.MethodGet:
		tok, src := s.effectiveAssistToken()
		// ★ 审计：Token 下发虽仅超管可见，仍留痕（谁在何时拉取了管理台凭据）
		s.Store.LogAudit(0, u.ID, "assist_token_read", "system_config", assistAdminTokenKey)
		writeJSON(w, 200, map[string]interface{}{
			"success":   true,
			"token":     tok, // 明文——仅超管会话可见，前端只用于注入同源 iframe
			"source":    src, // env / db / none
			"has_token": tok != "",
		})
	case http.MethodPost:
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求体格式错误"})
			return
		}
		tok := strings.TrimSpace(req.Token)
		// 空串 = 清除库内配置（回落到 env 或默认值），便于排障
		if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret(tok)); err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
			return
		}
		action := "assist_token_rotate"
		if tok == "" {
			action = "assist_token_clear"
		}
		s.Store.LogAudit(0, u.ID, action, "system_config", assistAdminTokenKey)
		_, src := s.effectiveAssistToken()
		writeJSON(w, 200, map[string]interface{}{"success": true, "source": src})
	default:
		writeJSON(w, 405, map[string]interface{}{"success": false, "message": "方法不支持"})
	}
}

// effectiveAssistToken 返回当前生效的 AI 助手管理台 Token 及其来源。
// env 优先（部署侧保底），其次 system_config 密文；均无则返回空串 + "none"。
// 参数：无（读进程环境与库内配置）。返回：Token 明文与来源标识。
func (s *Server) effectiveAssistToken() (string, string) {
	if v := strings.TrimSpace(os.Getenv("ASSIST_ADMIN_TOKEN")); v != "" {
		return v, "env"
	}
	raw, err := s.Store.GetConfig(assistAdminTokenKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return "", "none"
	}
	tok := store.DecryptSecret(strings.TrimSpace(raw))
	if tok == "" {
		// 密文存在但解不开（典型：JWT_SECRET 轮换未同步重存）——明确报 none，不静默给空
		return "", "none"
	}
	return tok, "db"
}
