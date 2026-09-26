// ============ lead.go · 职责说明 ============
// ★ P1-3 营销线索捕获（留资）公开接口：POST /api/lead（无需登录）。
// 背景：Landing/定价页此前只有「联系我们」演示文案、无任何捕获路径（全仓 grep 零命中），
// 流量无法沉淀为销售线索。本接口把留资表单落进 feedbacks 通道
// （target_type='lead'，复用超管反馈 BBS 面板的查看/回复/闭环链路，零新增表零新增后台 UI）。
// 防滥用三件套：IP 限流（复用 registerGuard 日配额+最小间隔）+ Turnstile 人机验证
// （仅 captcha_provider=turnstile 时强制，与注册同口径）+ 蜜罐字段（bot 填了即假成功丢弃）。
// =============================================
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"translator/internal/store"
)

// leadGuardDaily 每 IP 每日留资上限；leadGuardInterval 同 IP 两次提交最小间隔（秒）。
// 上限给销售场景留足余量（同一 NAT 出口多人填表），间隔挡住脚本刷写。
const (
	leadGuardDaily     = 10
	leadGuardInterval  = 5
	leadMaxCompany     = 100 // 公司名 rune 上限
	leadMaxEmail       = 120 // 邮箱长度上限（RFC 实用值）
	leadMaxLangs       = 200 // 意向语言列表上限
	leadMaxMessage     = 500 // 补充留言上限
	leadContentRuneCap = 900 // 组装后落 feedbacks.content 的总长上限（表无约束，入口自保）
)

// leadReq 留资表单请求体。Site 为蜜罐字段：人眼不可见，bot 全会填。
type leadReq struct {
	Company string `json:"company"`       // 公司/团队名称（必填）
	Email   string `json:"email"`         // 联系邮箱（必填）
	Langs   string `json:"langs"`         // 意向语言（逗号分隔，可空）
	Message string `json:"message"`       // 补充留言（可空）
	Source  string `json:"source"`        // 来源页：landing | pricing | footer（可空，默认 landing）
	Captcha string `json:"captcha_token"` // Turnstile token（后台开启人机验证时必填）
	Site    string `json:"site"`          // ★ 蜜罐：真人恒为空
}

// handleLeadCreate POST /api/lead：匿名留资提交。
// 校验顺序：方法 → 解析 → 蜜罐（假成功）→ 必填与格式 → 限流 → 人机验证 → 落库 → 超管通知。
func (s *Server) handleLeadCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]interface{}{"success": false, "message": "仅支持 POST"})
		return
	}
	var req leadReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 蜜罐命中：按成功返回但不落库不打扰超管（不给 bot 反馈信号）
	if strings.TrimSpace(req.Site) != "" {
		writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已收到，我们会尽快联系您"})
		return
	}
	company := truncateRunes(strings.TrimSpace(req.Company), leadMaxCompany)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	langs := truncateRunes(strings.TrimSpace(req.Langs), leadMaxLangs)
	message := truncateRunes(strings.TrimSpace(req.Message), leadMaxMessage)
	if company == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请填写公司/团队名称"})
		return
	}
	if !validLeadEmail(email) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请填写有效的联系邮箱"})
		return
	}
	src := strings.TrimSpace(req.Source)
	if src != "landing" && src != "pricing" && src != "footer" {
		src = "landing"
	}
	// IP 限流：日配额 + 最小间隔（registerGuard 持久化窗口，重启/多副本共享）
	ip := clientIP(r)
	if ok, wait := s.regGuard.allow("lead:"+ip, leadGuardDaily, leadGuardInterval); !ok {
		writeJSON(w, 429, map[string]interface{}{"success": false, "message": fmt.Sprintf("提交过于频繁，请 %d 秒后再试", wait)})
		return
	}
	// 人机验证（与注册同口径：仅在后台配置 turnstile 时强制）
	if err := s.verifyCaptcha(r, req.Captcha); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 落库：复用 feedbacks 通道（target_type='lead'），content 为结构化摘要行
	content := truncateRunes(fmt.Sprintf("【销售线索】公司：%s｜邮箱：%s｜意向语言：%s｜来源：%s｜留言：%s",
		company, email, orNone(langs), src, orNone(message)), leadContentRuneCap)
	f := &store.Feedback{TenantID: 0, UserID: 0, TargetType: "lead", Content: content, TargetLangs: langs}
	if err := s.Store.CreateFeedback(f); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "提交失败，请稍后再试"})
		return
	}
	s.regGuard.record("lead:" + ip) // 成功才计数（与 forgot/reset 同款 allow/record 语义）
	// 超管触达：站内信（反馈 BBS 同款）+ 告警（复用邮件/群机器人链路，与 feedback 同级 warning）
	for _, sa := range s.Store.ListUsersByRole(0, "admin") {
		_ = s.Store.CreateNotification(sa.ID, "收到新的销售线索",
			fmt.Sprintf("%s（%s）", company, email), "feedback", f.ID)
	}
	s.Store.CreateAlert(0, "warning", "lead",
		fmt.Sprintf("新销售线索：%s｜%s｜意向语言：%s", company, email, orNone(langs)))
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已收到，我们会尽快联系您"})
}

// validLeadEmail 邮箱格式校验（宽松实用：本地@域.后缀，无引号/注释形态）。
func validLeadEmail(e string) bool {
	if e == "" || len(e) > leadMaxEmail {
		return false
	}
	at := strings.LastIndex(e, "@")
	dot := strings.LastIndex(e, ".")
	return at > 0 && dot > at+1 && dot < len(e)-1 && !strings.ContainsAny(e, " \t\n（）()，,；;")
}

// orNone 空值占位（意向语言/留言允许缺省，摘要行以「无」保持列对齐）。
func orNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "无"
	}
	return v
}
