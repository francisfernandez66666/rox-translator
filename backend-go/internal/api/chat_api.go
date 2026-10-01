// Package api 聊天对话历史接口（★ 〇-AM：工作台 SSE 翻译通道的话记录查询）。
// 本文件提供两条 JSON 接口：列出用户最近 N 条对话、获取单条会话消息列表。
package api

import (
	"net/http"
	"strconv"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// ===== 列出对话历史 =====

// handleChatList 列出当前用户最近的对话（GET /api/chat/list?limit=N，默认 20）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回: {success:true, data:[{id,title,updated_at},...]}.
// 说明：必须登录态，匿名返回 401；limit 上限 100 防大查询。
func (s *Server) handleChatList(w http.ResponseWriter, r *http.Request) {
	if s.authUser(r) == nil {
		// ★ 〇-AM 收口（2026-10-02）：原为内联 writeJSON(w,401,{error})，违反 AGENTS §一·8
		//   「错误一律走 s.writeError + apierrors」；改走统一错误码后 401 结构体由 apierrors.WriteError 出，
		//   前端 core.ts 的 bizResp 对 4xx 一律还原成 {success:false} 并在 401 触发重登录，行为不变。
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}

	tid := s.currentTenant(r)

	// 解析 limit 参数（默认 20，上限 100）
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > 100 {
				limit = 100
			}
		}
	}

	convs, err := s.Store.ListChatConversations(s.authUser(r).ID, tid, limit)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"data":    convsToResp(convs),
	})
}

// convsToResp 把 store.ChatConversation 切片转成 JSON-safe 数组。
func convsToResp(convs []store.ChatConversation) []map[string]interface{} {
	out := make([]map[string]interface{}, len(convs))
	for i, c := range convs {
		out[i] = map[string]interface{}{
			"id":         c.ID,
			"title":      c.Title,
			"updated_at": c.UpdatedAt,
		}
	}
	return out
}

// ===== 获取单条会话消息 =====

// handleChatMessages 获取某会话的最近 N 条消息（GET /api/chat/messages?id=xxx&limit=N，默认 100）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回: {success:true, data:[{role,content,model,created_at},...]}.
// 说明：必须登录态；只取自己租户下的会话，跨租户一律 404。
func (s *Server) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	if s.authUser(r) == nil {
		// ★ 〇-AM 收口（2026-10-02）：同上，401 改走 writeError + apierrors（AGENTS §一·8）
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}

	convID := r.URL.Query().Get("id")
	if convID == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 id 参数"))
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > 500 {
				limit = 500
			}
		}
	}

	msgs, err := s.Store.GetChatMessages(convID, limit)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"data":    msgsToResp(msgs),
	})
}

// msgsToResp 把 store.ChatMessage 切片转成 JSON-safe 数组。
func msgsToResp(msgs []store.ChatMessage) []map[string]interface{} {
	out := make([]map[string]interface{}, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]interface{}{
			"role":       m.Role,
			"content":    m.Content,
			"model":      m.Model,
			"created_at": m.CreatedAt,
		}
	}
	return out
}
