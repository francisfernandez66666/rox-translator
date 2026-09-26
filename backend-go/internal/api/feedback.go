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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/observability"

	"translator/internal/store"
)

// feedbackDailyLimit 同用户每日反馈条数上限（防滥用）
const feedbackDailyLimit = 64

// feedbackMaxLen 单条反馈意见最大长度
const feedbackMaxLen = 1000

// emptyTranslationsJSON 「没有译文上下文」的唯一落库形态（★ F-45）。
const emptyTranslationsJSON = "{}"

// translationsJSONForFeedback 把反馈附带的译文映射归一为可安全回读的 JSON 串。
// 参数：m=语种→译文（可为 nil / 空）。返回：JSON 串，空映射恒为 "{}"。
// ★ F-45（〇-U 批，2026-09-26 UAT）：旧写法直接 json.Marshal(nil map) 得到字面量 "null" 落库，
// 而 "null" 是**合法 JSON**——前端 JSON.parse 不抛错却拿到 null，
// 下一步 Object.entries(null) 抛 TypeError，超管点进这条反馈整块详情白屏（读侧无 try/catch 也救不了）。
// 因此「没有上下文」必须写成 "{}"，由本函数统一口径，禁止再在各分支里手写 marshal。
func translationsJSONForFeedback(m map[string]string) string {
	if len(m) == 0 {
		return emptyTranslationsJSON
	}
	b, err := json.Marshal(m)
	if err != nil || string(b) == "null" {
		// marshal 对 map[string]string 实际不会失败；这里连同 "null" 一起兜底，
		// 是为了让「任何路径都不得把 null 送进这列」成为函数自身的性质，而不是调用点的自觉。
		return emptyTranslationsJSON
	}
	return string(b)
}

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
			// ★ F-45（〇-U 批）：这里旧写法 `_ = json.Unmarshal(...)` 把错误整个丢掉，
			// 空/脏 FinalResult 与「确实没有译文」被压成同一条路径，且落库成字面量 "null"。
			// 现在：空串不去解析（避免每条工单反馈都记一条无意义的告警），
			// 真有内容却解析失败才留痕，译文上下文统一按「无」降级。
			if strings.TrimSpace(t.FinalResult) != "" {
				if jerr := json.Unmarshal([]byte(t.FinalResult), &payload); jerr != nil {
					observability.Warn(r.Context(), "反馈上下文解析失败，译文上下文按空处理",
						"ticket_id", t.ID, "err", jerr.Error())
				}
			}
			f.Translations = translationsJSONForFeedback(payload.Translations)
			f.TargetLangs = t.TargetLangs
			f.Mode = normalizeTaskMode(t.Mode)
		} else {
			f.TicketID = t.ID // 仅关联工单号，不带内容上下文
			f.SourceText = ""
			f.Translations = emptyTranslationsJSON
		}
	} else if req.WithContext {
		// 文本类型：上下文由前端随附（气泡内的源文/译文）
		f.SourceText = req.SourceText
		f.Translations = translationsJSONForFeedback(req.Translations)
	} else {
		f.SourceText = ""
		f.Translations = emptyTranslationsJSON
	}
	// 创建反馈记录
	if err := s.Store.CreateFeedback(f); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
		// ★ F-64②（批 I-10）：原 200 承载失败 → 按真实语义分流（store.ResolveFeedback 自己区分了这两类事实：
		//   找不到这条反馈时它显式回 sql.ErrNoRows，写坏时才回 DB 错误）：
		//   ① sql.ErrNoRows → 404「反馈不存在」（文案原样保留），结案对象确实不在；
		//   ② 其余 err → 500 存储写入故障——旧写法把它也说成「反馈不存在」，超管会反复点结案，
		//      而真实原因是数据库没写进去。
		if errors.Is(err, sql.ErrNoRows) {
			s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "反馈不存在"))
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
	// 数据范围分叉：超管看平台全部反馈，其他登录用户仅看本人提交
	if auth.IsSuperAdmin(u) {
		list, err = s.Store.ListFeedbacks(status)
	} else {
		list, err = s.Store.ListFeedbacksByUser(u.ID, status)
	}
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：反馈列表（超管全量 / 本人分页）读的是本进程存储层。
		//   只读列表没有「记录不存在」一说，故不涉及 404；旧写法把 DB 故障画成「暂无反馈」，
		//   超管侧尤其危险——看不到待处理反馈会以为用户这周都没提问题。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	if list == nil {
		list = []*store.Feedback{}
	}
	// 预收集提交者集合，按 (user,tenant) 去重查显示名（避免逐条重复查询）
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
	// 组装响应行：WithContext=false 时脱敏 source_text/translations，仅回反馈正文与线程
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
			"translations_json": func() string {
				if f.WithContext {
					return f.Translations
				}
				return ""
			}(),
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
	// 载入反馈并做回复闸口：仅超管或提交者本人可复；resolved 后线程封存
	f, err := s.Store.GetFeedback(req.ID)
	if err != nil || f == nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404「反馈不存在」（文案原样）。
		//   回复要有对象，对象读不到就是不存在——这与「反馈已完成不可回复」(409)、「非提交者非超管」(403)
		//   是三种不同事实，旧写法三者全是 200＋一句文案，前端只能靠字符串区分。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "反馈不存在"))
		return
	}
	if !auth.IsSuperAdmin(u) && f.UserID != u.ID {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管或提交者可回复"})
		return
	}
	if f.Status == "resolved" {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：记录在、权限也对，只是**当前状态不允许**再追加回复
		//   （结案即线程封存）。这正是 409 的标准语义，不是参数错（400）也不是本层出错（500）。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "反馈已完成，不可再回复"))
		return
	}
	thread, aerr := s.appendReply(f, u, content)
	if aerr != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：appendReply 的 err 只可能是
		//   store.AppendFeedbackReply 的写失败（读改写里的读侧已在上游 404 拦下），属服务端故障；
		//   文案仍逐字透出 aerr.Error()（口径不变，仅状态码变诚实）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, aerr.Error()))
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
//
// ★ F-64② 收尾定夺（2026-09-26 批 I-10）：这条详情接口同时是**存在性探针**——
//
//	旧口径「别人的反馈回 403『无权查看』、不存在的回 404『反馈不存在』」让任何登录用户
//	都能拿 id 逐一试：403 说明「这条存在」、404 说明「不存在」，把只该超管知道的
//	反馈总量与提交节奏（连号 id 的密度）泄漏成了免费计数接口。
//	现按业界口径把两种情况并成同一个 404 同一句文案：归属不符也不承认存在。
//	代价是本人拿错链接时看到「反馈不存在」而不是「无权查看」——这句本来也不告诉他任何
//	可自助的信息，而列表接口（超管视图）本就带完整归属字段，运营侧不受影响。
func (s *Server) handleFeedbackGet(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	f, err := s.Store.GetFeedback(id)
	if err != nil || f == nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404「反馈不存在」（文案原样）。
		//   id 缺失/非数字时 ParseInt 回 0，落到这里同样按「这条反馈不存在」处理。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "反馈不存在"))
		return
	}
	if !auth.IsSuperAdmin(u) && f.UserID != u.ID {
		// 与上面那支**同码同文案**（见文件头：分开写就等于把「存在但不可看」送出去）。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "反馈不存在"))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "feedback": f})
}
