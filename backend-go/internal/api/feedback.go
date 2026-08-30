// ============ feedback.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 前台用户反馈接口：
//   - POST /api/feedback                登录用户提交反馈（文本气泡/工单详情入口）
//   - GET  /api/admin/feedbacks         超管查看反馈列表（status 过滤）
//   - POST /api/admin/feedbacks/resolve 超管标记已处理
// 安全要点：ticket 类型后端校验本人工单并自动读取 FinalResult 上下文；
// 同用户每天限 20 条；content ≤1000 字；新反馈写 critical/warning 告警触达超管。
// ========================================

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"

	"translator/internal/store"
)

// feedbackDailyLimit 同用户每日反馈条数上限（防滥用）
const feedbackDailyLimit = 64

// feedbackMaxLen 单条反馈意见最大长度
const feedbackMaxLen = 1000

// handleFeedbackCreate 用户提交翻译反馈。
func (s *Server) handleFeedbackCreate(w http.ResponseWriter, r *http.Request) {
	// 用户鉴权
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 解析请求参数
	var req struct {
		TargetType   string            `json:"target_type"`  // text | ticket
		TicketID     int64             `json:"ticket_id"`    // 工单 ID（ticket 类型必填）
		Content      string            `json:"content"`      // 反馈意见（必填）
		WithContext  bool              `json:"with_context"` // 是否同意附带上下文
		SourceText   string            `json:"source_text"`  // 文本场景源文（前端随附）
		Translations map[string]string `json:"translations"` // 文本场景译文映射（前端随附）
		TargetLangs  string            `json:"target_langs"` // 目标语言列表
		Mode         string            `json:"mode"`         // fast | pro
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 校验反馈内容
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请填写反馈意见"})
		return
	}
	if len([]rune(content)) > feedbackMaxLen {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": fmt.Sprintf("反馈意见最多 %d 字", feedbackMaxLen)})
		return
	}
	// 每日限流（防滥用）
	if n := s.Store.CountFeedbacksToday(u.ID); n >= feedbackDailyLimit {
		writeJSON(w, 429, map[string]interface{}{"success": false, "message": "今日反馈已达上限，请明天再试"})
		return
	}
	// 默认反馈类型为文本
	targetType := req.TargetType
	if targetType != "text" && targetType != "ticket" {
		targetType = "text"
	}

	// 构建反馈记录
	f := &store.Feedback{
		TenantID:    u.TenantID,
		UserID:      u.ID,
		TargetType:  targetType,
		TicketID:    req.TicketID,
		TargetLangs: strings.TrimSpace(req.TargetLangs),
		Mode:        normalizeTaskMode(req.Mode),
		Content:     content,
		WithContext: req.WithContext,
	}
	// 工单类型：校验本人工单，勾选同意时后端自动读取工单上下文（更可信）
	if targetType == "ticket" {
		t, err := s.Store.GetTicket(req.TicketID, u.TenantID)
		if err != nil || t.CreatedBy != u.ID {
			writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权反馈该工单"})
			return
		}
		f.TicketID = t.ID
		if req.WithContext {
			// 读取工单上下文：源文和译文
			f.SourceText = t.SourceText
			var payload struct {
				Translations map[string]string `json:"translations"`
			}
			_ = json.Unmarshal([]byte(t.FinalResult), &payload)
			if b, jerr := json.Marshal(payload.Translations); jerr == nil {
				f.Translations = string(b)
			}
			f.TargetLangs = t.TargetLangs
			f.Mode = normalizeTaskMode(t.Mode)
		} else {
			f.TicketID = t.ID // 仅关联工单号，不带内容上下文
			f.SourceText = ""
			f.Translations = ""
		}
	} else if req.WithContext {
		// 文本类型：上下文由前端随附（气泡内的源文/译文）
		f.SourceText = req.SourceText
		if b, jerr := json.Marshal(req.Translations); jerr == nil {
			f.Translations = string(b)
		}
	} else {
		f.SourceText = ""
		f.Translations = ""
	}
	// 创建反馈记录
	if err := s.Store.CreateFeedback(f); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 告警触达（复用告警面板/邮件/群机器人链路）
	s.Store.CreateAlert(f.TenantID, "warning", "feedback",
		fmt.Sprintf("收到用户 #%d 的%s反馈：%s", u.ID, map[string]string{"text": "文本翻译", "ticket": "工单"}[targetType], truncateRunes(content, 80)))
	// ★ 站内通知超管（问题反馈入口）：通知 platform 超管（tenant_id=0, role=admin）
	for _, sa := range s.Store.ListUsersByRole(0, "admin") {
		_ = s.Store.CreateNotification(sa.ID, "收到新的问题反馈",
			fmt.Sprintf("%s：%s", u.DisplayName, truncateRunes(content, 60)), "feedback", f.ID)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": f.ID})
}

// truncateRunes 按 rune 截断字符串并加省略号。
func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}


// handleAdminFeedbackResolve 超管标记反馈已处理。
func (s *Server) handleAdminFeedbackResolve(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID   int64  `json:"id"`   // 反馈 ID
		Note string `json:"note"` // 处理备注（可选）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "无效的反馈 ID"})
		return
	}
	if err := s.Store.ResolveFeedback(req.ID, strings.TrimSpace(req.Note)); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "反馈不存在"})
		return
	}
	s.Store.LogAudit(0, u.ID, "feedback_resolve", "feedbacks", fmt.Sprintf("%d", req.ID))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// feedbackReplyItem BBS 回复线程元素
type feedbackReplyItem struct {
	U       int64  `json:"u"`       // 回复人用户 ID
	Name    string `json:"name"`    // 显示名
	Role    string `json:"role"`    // admin | tenant_admin | dept_admin | user
	Content string `json:"content"` // 回复内容
	At      string `json:"at"`      // 时间 RFC3339
}

// appendReply 读取→追加→写回回复线程（当前量级下读改写可接受）。
func (s *Server) appendReply(f *store.Feedback, u *store.User, content string) (string, error) {
	items := []feedbackReplyItem{}
	if f.Replies != "" {
		_ = json.Unmarshal([]byte(f.Replies), &items)
	}
	items = append(items, feedbackReplyItem{
		U: u.ID, Name: u.DisplayName, Role: u.Role,
		Content: content, At: time.Now().Format(time.RFC3339),
	})
	b, _ := json.Marshal(items)
	return string(b), s.Store.AppendFeedbackReply(f.ID, string(b))
}

// handleFeedbackList 反馈列表（角色化）：超管=平台全部；其他登录用户=本人提交。
// 查询参数 status=open|resolved|空。附带提交者显示名。
func (s *Server) handleFeedbackList(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	status := r.URL.Query().Get("status")
	var (
		list []*store.Feedback
		err  error
	)
	if auth.IsSuperAdmin(u) {
		list, err = s.Store.ListFeedbacks(status)
	} else {
		list, err = s.Store.ListFeedbacksByUser(u.ID, status)
	}
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if list == nil {
		list = []*store.Feedback{}
	}
	nameOf := map[int64]string{}
	tidOf := map[int64]int64{}
	for _, f := range list {
		nameOf[f.UserID] = ""
		tidOf[f.UserID] = f.TenantID
	}
	for uid, tid := range tidOf {
		if usr, e := s.Store.GetUser(uid, tid); e == nil && usr != nil {
			nameOf[uid] = usr.DisplayName
		}
	}
	out := make([]map[string]interface{}, 0, len(list))
	for _, f := range list {
		srcCtx := ""
		if f.WithContext {
			srcCtx = f.SourceText
		}
		out = append(out, map[string]interface{}{
			"id": f.ID, "user_id": f.UserID, "user_name": nameOf[f.UserID],
			"target_type": f.TargetType, "ticket_id": f.TicketID,
			"content": f.Content, "target_langs": f.TargetLangs, "mode": f.Mode,
			"with_context": f.WithContext, "source_text": srcCtx,
			"translations_json": func() string { if f.WithContext { return f.Translations }; return "" }(),
			"status": f.Status, "replies": json.RawMessage(f.Replies),
			"created_at": f.CreatedAt, "handled_at": f.HandledAt,
		})
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "feedbacks": out})
}

// handleFeedbackReply 回复反馈（BBS 模式）：超管或提交者本人；已完成禁止回复。
func (s *Server) handleFeedbackReply(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		ID      int64  `json:"id"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请填写回复内容"})
		return
	}
	f, err := s.Store.GetFeedback(req.ID)
	if err != nil || f == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "反馈不存在"})
		return
	}
	if !auth.IsSuperAdmin(u) && f.UserID != u.ID {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管或提交者可回复"})
		return
	}
	if f.Status == "resolved" {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "反馈已完成，不可再回复"})
		return
	}
	thread, aerr := s.appendReply(f, u, content)
	if aerr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": aerr.Error()})
		return
	}
	// ★ 回复提醒对方：超管回复→通知提交者；提交者补充→再通知超管
	if auth.IsSuperAdmin(u) {
		_ = s.Store.CreateNotification(f.UserID, "你的问题反馈有新回复",
			truncateRunes(content, 60), "feedback", f.ID)
	} else {
		for _, sa := range s.Store.ListUsersByRole(0, "admin") {
			_ = s.Store.CreateNotification(sa.ID, "问题反馈有新回复",
				fmt.Sprintf("%s：%s", u.DisplayName, truncateRunes(content, 60)), "feedback", f.ID)
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "replies": json.RawMessage(thread)})
}

// handleFeedbackGet 单条反馈详情（超管或提交者）。
func (s *Server) handleFeedbackGet(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	f, err := s.Store.GetFeedback(id)
	if err != nil || f == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "反馈不存在"})
		return
	}
	if !auth.IsSuperAdmin(u) && f.UserID != u.ID {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权查看"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "feedback": f})
}
