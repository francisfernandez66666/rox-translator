// ============ notifications.go · 职责说明 ============
// api 包内部实现文件。
// =============================================

// ============ 本文件职责中文说明 ============
// 站内信（通知中心）HTTP 接口：
//   - GET  /api/notifications          列表（最新在前，?limit=&offset= 分页，默认 20 条／单页上限 100）
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
	"translator/internal/store"
)

// handleNotifications 列出当前用户的站内信（分页）。
//
// ★ 2026-10-11 跨境链路瓶颈批：查询参数 `limit`（默认 20）／`offset`（默认 0），档位与上限都在 store 侧收口；
// 响应新增 `limit`／`offset`／`has_more` 三字段，`notifications` 键名与形态**一字未改**（老客户端照旧能用，
// 只是第一页从 100 条变 20 条）。为什么不做"服务端继续吐 100 条、前端只渲染 20 条"：
// 那省的是渲染，省不了**传输**，而本批要治的恰恰是传输（列表接口在塌到 KB/s 的链路上永远转不完）。
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	// 用户鉴权
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	// 分页参数：脏值一律折回默认档（不报错——见 store 侧那条"新增防护不拿可用性交押金"）
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	// 查询当前用户的站内信列表（最新在前，按页取）
	list, err := s.Store.ListNotifications(u.ID, limit, offset)
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：站内信列表读的是本进程存储层，
		//   DB 失败属服务端故障。只读列表没有「记录不存在」一说，故不涉及 404；
		//   旧写法让铃铛把故障渲染成「暂无通知」，用户以为没人找过他。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// has_more 用「本页取满」近似判断：够一页就还有下一页可能。
	// 为什么不用 COUNT(*)：多一次查询换一个更准的按钮态，在本批这条瓶颈链路上是**反向**收益；
	// 前端多翻一次取到空页即收口，代价只是一次 20 条的小请求。
	hasMore := len(list) >= effectivePageLimit(limit)
	// ★ 空页必须序列化成 `[]` 而不是 `null`（本批新增收口，不是历史形态的顺带修补）：
	//   Go 侧 `var out []*Notification` 在零行时是 nil，json 直接吐 `"notifications":null`。
	//   改造前只有「一条通知都没有的用户」会走到这条形态；分页之后**任何越界/尾页都恒为空页**，
	//   null 从罕见形态变成常规形态——而这一层的契约是「列表键永远是数组」，
	//   非 JS 客户端（SDK／严格类型消费方）对 null 直接解引用崩，前端现在靠 `|| []` 兜着才没出事。
	//   方向是**放宽**不是改形状：原先能处理 null 的消费方处理 `[]` 不需要改一行；
	//   拿「是不是 null」当「有没有通知」判据的写法本来就该问长度。
	if list == nil {
		list = []*store.Notification{}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":       true,
		"notifications": list,
		"limit":         effectivePageLimit(limit),
		"offset":        maxInt(offset, 0),
		"has_more":      hasMore,
	})
}

// effectivePageLimit 把请求档位折回 store 侧同一套钳制口径（响应里回显的 limit 必须与真用的那个一致，
// 否则前端拿"请求值"当翻页步长，脏参会让 offset 算歪、整页跳过或重复）。
func effectivePageLimit(limit int) int {
	if limit <= 0 {
		return store.NotificationPageDefault
	}
	if limit > store.NotificationPageMax {
		return store.NotificationPageMax
	}
	return limit
}

// maxInt 不在本文件重复定义：api 包已有同名工具（register_guard.go），语义一致（取较大值），复用即可。

// handleNotificationsUnread 未读数量。
func (s *Server) handleNotificationsUnread(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
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
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
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
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
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

// 说明：strconv 现被分页参数解析真实使用（2026-10-11 分页批），历史那条 `var _ = strconv.Itoa`
// 占位引用已撤——留着它，将来真把 strconv 用上了也看不出"这一度是死引用"，反而钝化对 unused import 的感知。
