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
	"strings"

	"translator/internal/store"
)

// feedbackDailyLimit 同用户每日反馈条数上限（防滥用）
const feedbackDailyLimit = 64

// feedbackMaxLen 单条反馈意见最大长度
const feedbackMaxLen = 1000

// handleFeedbackCreate 用户提交翻译反馈。
func (s *Server) handleFeedbackCreate(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
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
	targetType := req.TargetType
	if targetType != "text" && targetType != "ticket" {
		targetType = "text"
	}

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
	if err := s.Store.CreateFeedback(f); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 告警触达超管：复用告警面板/邮件/群机器人链路
	s.Store.CreateAlert(f.TenantID, "warning", "feedback",
		fmt.Sprintf("收到用户 #%d 的%s反馈：%s", u.ID, map[string]string{"text": "文本翻译", "ticket": "工单"}[targetType], truncateRunes(content, 80)))
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

// handleAdminFeedbacks 超管查看反馈列表（?status=open|resolved|空=全部）。
func (s *Server) handleAdminFeedbacks(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	list, err := s.Store.ListFeedbacks(r.URL.Query().Get("status"))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "feedbacks": list})
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
