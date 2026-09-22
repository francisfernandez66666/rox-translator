// ============ admin_assist.go · 职责说明 ============
// 主后台 ↔ AI 助手（assist）管理台鉴权贯通：管理 Token 的托管接口（★ 改造 1A 建立，
// ★ 〇-LK 2026-09-22 按「模型配置（ModelsP）」范式重做读取与保存语义）。
//
// 背景：ai-assist 融合进主仓前是独立服务，管理台要求用户手工粘贴 ASSIST_ADMIN_TOKEN
// （提示语「首次使用需在右上角填入管理 Token」），与主后台 SSO 体验断层。
// 现由主后台统一托管该 Token：密文落 system_config.assist_admin_token（enc:v1:，
// 复用 store.EncryptSecret），仅超管可读写；#34 之后浏览器连掩码之外的任何值都不再需要。
//
// ★ 为什么不回明文（〇-LK 修的正是这一点）：旧 GET 直接把明文 Token 写进响应体，
// 注释理由是「前端注入同源 iframe」，但 iframe 形态在 #34 已被原生面板取代、
// 前端取明文的那条路（adminAssistToken 的 token 字段）已经没人用了——
// 留着就等于凭据仍然会出现在浏览器响应、代理日志与 Playwright trace 里。
// 现在口径与 llm_api_key / online_api_key 完全一致：
//   - 读取只回 set + 掩码（maskKey 前4****后4）+ 生效来源；
//   - 保存「留空=不修改」「掩码=未改动」（IsSecretMasked 命中即跳过写库，绝不把掩码写回）；
//   - 清除必须走显式 clear=true，避免把空串误当清空；
//   - 保存成功后尽力推给 assist 侧（见 pushAssistAdminToken），做到改完即用、不必重启。
//
// 优先级口径（与 assist-server 侧一致，避免两处读到不同值）：
//  1. env ASSIST_ADMIN_TOKEN —— 部署侧显式配置，优先级最高（保底，运维可绕过库配置）；
//  2. system_config.assist_admin_token —— 库内密文，管理台可轮换。
//
// env 存在时库内值不会生效，因此响应额外回 env_overridden，前端据此置灰并解释，
// 而不是让用户以为「我保存了怎么还是未配置」（同类陷阱见 pay_channels.go 的 env_overridden）。
//
// 安全要点：仅 requireAdminUser（super_admin）可访问；读取与轮换均记审计。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"translator/internal/observability"
	"translator/internal/store"
)

// assistAdminTokenKey system_config 中存放 AI 助手管理台 Token 的键名。
const assistAdminTokenKey = "assist_admin_token"

// assistTokenState AI 助手管理 Token 的状态出参（**永不含明文**）。
//
//	Source:  当前生效来源 env / db / none
//	Set:     是否已生效配置（Source != none 的便捷位）
//	Masked:  生效 Token 的掩码（供面板显示「sk-a****wxyz」）
//	DBMasked: 库内密文解密后的掩码（env 覆盖时仍能看到库里存的是哪一个）
//	EnvOverridden: true = 环境变量占用了生效位，库内值要等 env 移除后才生效
type assistTokenState struct {
	Source        string `json:"source"`
	Set           bool   `json:"set"`
	Masked        string `json:"masked"`
	DBMasked      string `json:"db_masked"`
	EnvOverridden bool   `json:"env_overridden"`
}

// assistTokenStateOf 组装 Token 状态。参数 effective/source：生效值与其来源（env/db/none）。
// 库内掩码单独解一次密——env 生效时前端仍要看见库里存着哪个值，否则「保存了但没生效」无从判断。
func (s *Server) assistTokenStateOf(effective, source string) assistTokenState {
	_, dbMask := s.llmKeyState(assistAdminTokenKey) // llmKeyState 已是「解密 + maskKey」的通用工具
	return assistTokenState{
		Source:        source,
		Set:           effective != "",
		Masked:        maskKey(effective), // 空串经 maskKey 得 "****"，前端按 set 判定显示，不显示掩码
		DBMasked:      dbMask,
		EnvOverridden: strings.TrimSpace(os.Getenv("ASSIST_ADMIN_TOKEN")) != "",
	}
}

// handleAdminAssistToken AI 助手管理台 Token 的读取/保存（仅超管）。
//
//	GET  → 掩码态（set/masked/source/env_overridden）；明文一律不外发（见文件头口径）
//	POST → body {"token":"...","clear":false}
//	        · token 留空或仍是掩码 = 不修改（changed:false），与 ModelsP 保存 Key 同口径；
//	        · clear=true = 清除库内配置（回落 env）；必须显式传，避免空串误删；
//	        · 写库成功后尽力同步给 assist 服务（pushAssistAdminToken），实现改完即用。
//
// 返回：success + 状态字段 + changed（本次是否真的写入了库）+ pushed（是否已同步到助手服务）。
func (s *Server) handleAdminAssistToken(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	switch r.Method {
	case http.MethodGet:
		tok, src := s.effectiveAssistToken()
		// ★ 审计：读取虽只回掩码，仍留痕（谁在何时看过管理凭据状态）
		s.Store.LogAudit(0, u.ID, "assist_token_read", "system_config", assistAdminTokenKey)
		st := s.assistTokenStateOf(tok, src)
		writeJSON(w, 200, map[string]interface{}{"success": true,
			"source": st.Source, "set": st.Set, "masked": st.Masked,
			"db_masked": st.DBMasked, "env_overridden": st.EnvOverridden})
	case http.MethodPost:
		var req struct {
			Token string `json:"token"`
			Clear bool   `json:"clear"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求体格式错误"})
			return
		}
		tok := strings.TrimSpace(req.Token)
		// 保存前先把「当前生效值」留在手上：推送给 assist 时要用它做凭据（新值对方还不认）
		oldTok, _ := s.effectiveAssistToken()
		if req.Clear {
			if err := s.Store.SetConfig(assistAdminTokenKey, ""); err != nil {
				writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
				return
			}
			// 清除只作用于主后台库内那份配置：assist 侧若已收到过推送值（configs.admin_token），
			// 它仍按自己的配置放行——要彻底停用管理面得同时处理部署侧 env。面板文案按此口径写。
			s.Store.LogAudit(0, u.ID, "assist_token_clear", "system_config", assistAdminTokenKey)
			eff, src := s.effectiveAssistToken()
			st := s.assistTokenStateOf(eff, src)
			writeJSON(w, 200, map[string]interface{}{"success": true, "changed": true, "pushed": false,
				"source": st.Source, "set": st.Set, "masked": st.Masked,
				"db_masked": st.DBMasked, "env_overridden": st.EnvOverridden})
			return
		}
		// 留空 / 掩码回写 = 未修改：既不是清除也不写库（掩码写回库会把凭据静默改坏，历史踩坑）
		changed := tok != "" && !store.IsSecretMasked(tok)
		if changed {
			if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret(tok)); err != nil {
				writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
				return
			}
			s.Store.LogAudit(0, u.ID, "assist_token_rotate", "system_config", assistAdminTokenKey)
		}
		eff, src := s.effectiveAssistToken()
		st := s.assistTokenStateOf(eff, src)
		// 尽力同步到 assist 服务：旧值还能用时用旧值鉴权，否则拿新值试一次
		// （典型场景：运维把 assist 的 ASSIST_ADMIN_TOKEN 原样填进面板，让两边对齐）。
		// 只在「本次保存的值确实是生效值」时推送：env 占着生效位时推过去会让
		// 助手侧认新值、主后台仍发 env 旧值，反而把管理面推成 401。
		pushed := false
		if changed && src == "db" {
			cred := oldTok
			if cred == "" {
				cred = tok
			}
			pushed = s.pushAssistAdminToken(r, cred, tok)
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "changed": changed, "pushed": pushed,
			"source": st.Source, "set": st.Set, "masked": st.Masked,
			"db_masked": st.DBMasked, "env_overridden": st.EnvOverridden})
	default:
		writeJSON(w, 405, map[string]interface{}{"success": false, "message": "方法不支持"})
	}
}

// pushAssistAdminToken 把库内新 Token 推到 assist 服务的 configs.admin_token（★ 〇-LK）。
//
// 为什么要推：assist 的 guard 改成「env ∪ 自身 configs」双候选后（见 internal/assist/api），
// 主后台保存即等同助手侧即时生效，不必再「改一次 Token 重启一次服务」；
// 而重启会连带把访客会话密钥换掉——这正是用户反馈「刷新一次页面对话就没了」的根因之一。
//
// 失败不阻断保存：主库已经是事实源，推不过去只是「需要重启 assist」，
// 因此只记 slog + 回 pushed:false 给前端提示，绝不回滚也不报错。
// 参数：cred=assist 当前认的 Token（旧生效值或新值本身）；next=要写入的新 Token。
func (s *Server) pushAssistAdminToken(r *http.Request, cred, next string) bool {
	if cred == "" || next == "" || s.Store == nil {
		return false
	}
	payload, _ := json.Marshal(map[string]string{"key": "admin_token", "value": next})
	ok, err := s.assistAdminPutConfig(r.Context(), cred, string(payload))
	if err != nil {
		observability.Warn(r.Context(), "assist Token 同步失败（主库已保存，助手侧需重启或改环境变量）", "err", err.Error())
	}
	return ok
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
