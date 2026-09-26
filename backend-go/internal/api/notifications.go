// ============ notifications.go · 职责说明 ============
// api 包内部实现文件。
// =============================================

// ============ 本文件职责中文说明 ============
// 站内信（通知中心）HTTP 接口：
//   - GET  /api/notifications          列表（最新在前，最多 100 条）
//   - GET  /api/notifications/unread    未读数量
//   - POST /api/notifications/read      单条已读 {id}
//   - POST /api/notifications/read-all  全部已读
//
// 全部按当前登录用户隔离。
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	apierrors "translator/internal/errors"
)

// handleNotifications 列出当前用户的站内信。
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	// 用户鉴权
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 查询当前用户的站内信列表（最新在前，最多 100 条）
	list, err := s.Store.ListNotifications(u.ID)
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：站内信列表读的是本进程存储层，
		//   DB 失败属服务端故障。只读列表没有「记录不存在」一说，故不涉及 404；
		//   旧写法让铃铛把故障渲染成「暂无通知」，用户以为没人找过他。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：未读数是 COUNT 查询，失败即本进程存储层故障。
		//   这个接口是铃铛轮询（前端拿不到结果就保留上一次读数、不弹提示），
		//   报 500 而不是伪装成功，前端与排障都能按状态码认出「这次轮询真没成」。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500（存储写入故障）。三类失败的分界写清楚：
		//   ① 参数缺失/非法（id ≤ 0、body 不是 JSON）→ 上面那支 400，载荷本身写错了，属客户端可修；
		//   ② id 合法但记录不存在或不属本人 → store.MarkNotificationRead 是带 user_id 条件的 UPDATE，
		//      影响 0 行**不回 err**，故这一支落不到 404（现状＝静默成功，已列为挂账：
		//      需 store 侧回 sql.ErrNoRows 才能诚实报「站内信不存在」，本批不得动 store）；
		//   ③ 真正写失败（DB 故障）→ 就是这里，重试同一份载荷有意义，伪装成成功会吞掉故障。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500（存储写入故障）。
		//   「一条都没更新到」（本来就没有未读）在 store 侧是 0 行影响且不报错，仍走成功分支——
		//   这是幂等批量的正常语义，不是部分失败；能落到这里的只有数据库写失败。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// 占位引用：保留 strconv 导入供后续通知格式化扩展使用（编译期防 unused 撤销）
var _ = strconv.Itoa // 占位引用（strconv 预留）
