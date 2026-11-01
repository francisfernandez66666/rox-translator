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
	"net/url"
	"strings"

	apierrors "translator/internal/errors"
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
	Company  string `json:"company"`       // 公司/团队名称（必填）
	Email    string `json:"email"`         // 联系邮箱（必填）
	Langs    string `json:"langs"`         // 意向语言（逗号分隔，可空）
	Message  string `json:"message"`       // 补充留言（可空）
	Source   string `json:"source"`        // 来源页：landing | pricing | footer | static_fallback（可空，默认 landing）
	Captcha  string `json:"captcha_token"` // Turnstile token（后台开启人机验证时必填）
	Site     string `json:"site"`          // ★ 蜜罐：真人恒为空
	DeviceID string `json:"device_id"`     // ★ DEF-P0-03：浏览器设备号（与 lib/trialDevice.ts 同一份事实）；缺失/格式不合不返 400
}

// handleLeadCreate POST /api/lead：匿名留资提交。
// 校验顺序：方法 → 解析 → 蜜罐（假成功）→ 必填与格式 → 限流 → 人机验证 → 落库 → 超管通知。
func (s *Server) handleLeadCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	req, formMode, err := s.leadDecode(r)
	if err != nil {
		s.leadFail(w, r, formMode, apierrors.ErrValidation, "请求格式错误")
		return
	}
	// 蜜罐命中：按成功返回但不落库不打扰超管（不给 bot 反馈信号）
	if strings.TrimSpace(req.Site) != "" {
		s.leadOK(w, r, formMode)
		return
	}
	company := truncateRunes(strings.TrimSpace(req.Company), leadMaxCompany)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	langs := truncateRunes(strings.TrimSpace(req.Langs), leadMaxLangs)
	message := truncateRunes(strings.TrimSpace(req.Message), leadMaxMessage)
	if company == "" {
		s.leadFail(w, r, formMode, apierrors.ErrValidation, "请填写公司/团队名称")
		return
	}
	if !validLeadEmail(email) {
		s.leadFail(w, r, formMode, apierrors.ErrValidation, "请填写有效的联系邮箱")
		return
	}
	src := leadSourceOrDefault(req.Source)
	// 设备号：格式不合＝当没上报（不返 400，AGENTS §一·10「新增防护不拿可用性交押金」）
	device := ""
	if id := strings.TrimSpace(req.DeviceID); trialDeviceIDRe.MatchString(id) {
		device = id
	}
	// IP 限流：日配额 + 最小间隔（registerGuard 持久化窗口，重启/多副本共享）
	ip := clientIP(r)
	if ok, wait := s.regGuard.allow("lead:"+ip, leadGuardDaily, leadGuardInterval); !ok {
		s.leadFail(w, r, formMode, apierrors.ErrRateLimited, fmt.Sprintf("提交过于频繁，请 %d 秒后再试", wait))
		return
	}
	// 人机验证（与注册同口径：仅在后台配置 turnstile 时强制）
	if err := s.verifyCaptcha(r, req.Captcha); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 落库：复用 feedbacks 通道（target_type='lead'），content 为结构化摘要行
	content := truncateRunes("【销售线索】"+leadSummaryLine(company, email, langs, src, message), leadContentRuneCap)
	f := &store.Feedback{TenantID: 0, UserID: 0, TargetType: "lead", Content: content, TargetLangs: langs}
	if err := s.Store.CreateFeedback(f); err != nil {
		s.leadFail(w, r, formMode, apierrors.ErrInternal, "提交失败，请稍后再试")
		return
	}
	s.regGuard.record("lead:" + ip) // 成功才计数（与 forgot/reset 同款 allow/record 语义）
	// ★ DEF-P0-03 S1 的账本在这一侧也记：JS 表单带着设备号与 uid，正是"同一出口上换了第二个账号"
	//   的观察面；只在这里记、免验证那一条腿就永远等不到升级信号。
	s.recordLeadIdentities(r, ip, device)
	// 超管触达：站内信（反馈 BBS 同款）+ 告警（复用邮件/群机器人链路，与 feedback 同级 warning）
	// ★ #46：这一支收敛前按角色字面量圈人——销售线索是**唯一客户入口**，
	//   而只有 super_admin 账号的部署里它一条站内信都发不出去（邮件仍走 alert_email，所以现网难发现）。
	s.notifySuperAdmins("收到新的销售线索",
		fmt.Sprintf("%s（%s）", company, email),
		store.NotifyRefFeedback, f.ID)
	s.Store.CreateAlert(0, "warning", "lead",
		fmt.Sprintf("新销售线索：%s｜%s｜意向语言：%s", company, email, orNone(langs)))
	s.leadOK(w, r, formMode)
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

// ============ 表单/JSON 两形态共用的一层（★ DEF-P0-03，2026-11-01）============
//
// 为什么要这一层：`index.html` 直出的零 JS 静态表单只能用原生 `<form method=post>`，
// 它发的是 `application/x-www-form-urlencoded`，而旧 `handleLeadCreate` 只 `json.Decode`
// ⇒ 那种提交**当场 400**（表单看起来"点了没反应"）。分流与出路都收在下面这几个函数里，
// 两个端点共用一份，禁止在 handler 里各写一遍（两把尺子迟早分家）。

// leadFormCT 表单编码提交的 Content-Type 前缀。
const leadFormCT = "application/x-www-form-urlencoded"

// leadIsForm 是否表单形态提交（urlencoded 与 multipart 都算——原生 <form> 只有这两种 enctype）。
func leadIsForm(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	return strings.HasPrefix(ct, leadFormCT) || strings.HasPrefix(ct, "multipart/")
}

// leadDecode 按 Content-Type 分流解码，返回 (请求体, 是否表单档, 解码错误)。
//
// 四条讲究（每条都对应一种坏法）：
//
//	① **只在对应那一条腿上读 r.Body**：表单档走 ParseForm/ParseMultipartForm，JSON 档走
//	   json.NewDecoder(r.Body)。反过来"先 io.ReadAll 再 NewDecoder"会让第二条腿读到空流
//	  （恒 400，属判据自伤里最难查的一族——屏幕上只有一句"请求格式错误"）；
//	② 解码失败 ≠ 必填缺失：本函数只报"看不懂 body"，必填/格式由调用方分档回文案（§E-2 同一条纪律）；
//	③ 取值一律用 r.PostFormValue（**不是** r.FormValue）：后者会把 URL query 一起算进来，
//	   于是 `?company=x` 能替用户填表——留资字段虽然不敏感，但"查询参数能改 POST 体"这个形状
//	   迟早被人拿去试别的接口；
//	④ multipart 给 512KB 内存上限：留资字段本来就小，超了当场判解码失败，而不是把内存吃满。
func (s *Server) leadDecode(r *http.Request) (leadReq, bool, error) {
	var req leadReq
	if !leadIsForm(r) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return req, false, err
		}
		return req, false, nil
	}
	var err error
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/") {
		err = r.ParseMultipartForm(512 << 10)
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		return req, true, err
	}
	req.Company = r.PostFormValue("company")
	req.Email = r.PostFormValue("email")
	req.Langs = r.PostFormValue("langs")
	req.Message = r.PostFormValue("message")
	req.Source = r.PostFormValue("source")
	req.Captcha = r.PostFormValue("captcha_token")
	req.Site = r.PostFormValue("site")
	req.DeviceID = r.PostFormValue("device_id")
	return req, true, nil
}

// leadSourceOrDefault 来源页白名单（三个既有面 ＋ 免验证通道那一档）。
// 未知值一律折回 landing，**不返 400**：来源只是运营看的归因标签，不该为它拒一条真线索。
func leadSourceOrDefault(v string) string {
	switch src := strings.TrimSpace(v); src {
	case "landing", "pricing", "footer", leadPublicSource:
		return src
	default:
		return "landing"
	}
}

// leadSummaryLine 摘要行主体（两个端点共用同一份拼装）。
// ⚠️ 打标（【销售线索】／【销售线索·免验证通道】）**刻意留在调用方**：那一串前缀是
// 两类线索可信度分得开的唯一凭据，收进本函数就等于把它们混成一样。
func leadSummaryLine(company, email, langs, src, message string) string {
	return fmt.Sprintf("公司：%s｜邮箱：%s｜意向语言：%s｜来源：%s｜留言：%s",
		company, email, orNone(langs), src, orNone(message))
}

// leadOK 成功出路：表单档 303 → /?lead=ok（原生表单提交后必须落回一个 HTML 页，
// 否则用户看到一屏 JSON＝"提交了但什么都没发生"）；JSON 档回既有信封，老客户端形状一字不改。
func (s *Server) leadOK(w http.ResponseWriter, r *http.Request, formMode bool) {
	if formMode {
		http.Redirect(w, r, "/?lead=ok", http.StatusSeeOther)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已收到，我们会尽快联系您"})
}

// leadFail 失败出路：表单档 303 → /?lead=err&t=<稳定码>（**只带码，不带原文**——把文案塞进
// query 等于开一个"服务端文案可被 URL 反射"的面，前端也按不了）；JSON 档走 s.writeError（§一·8）。
func (s *Server) leadFail(w http.ResponseWriter, r *http.Request, formMode bool, code apierrors.ErrorCode, msg string) {
	if !formMode {
		s.writeError(w, r, apierrors.New(code, msg))
		return
	}
	q := url.Values{"lead": {"err"}, "t": {string(code)}}
	http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
}
