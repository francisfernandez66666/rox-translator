// ============ 本文件职责中文说明 ============
// 站内信（通知中心）HTTP 接口：
//   - GET  /api/notifications          列表（最新在前，最多 100 条）
//   - GET  /api/notifications/unread    未读数量
//   - POST /api/notifications/read      单条已读 {id}
//   - POST /api/notifications/read-all  全部已读
// 全部按当前登录用户隔离。
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// handleNotifications 列出当前用户的站内信。
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	list, err := s.Store.ListNotifications(u.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "notifications": list})
}

// handleNotificationsUnread 未读数量。
func (s *Server) handleNotificationsUnread(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	n, err := s.Store.UnreadCount(u.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "unread": n})
}

// handleNotificationsRead 单条已读。
func (s *Server) handleNotificationsRead(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.MarkNotificationRead(req.ID, u.ID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleNotificationsReadAll 全部已读。
func (s *Server) handleNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	if err := s.Store.MarkAllNotificationsRead(u.ID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

var _ = strconv.Itoa // 占位引用（strconv 预留）
