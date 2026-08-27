// ============ mail_tpl.go · 职责说明 ============
// 邮件模板（按用途区分的多个模板，支持前端后台配置，仅超管可改）：
//   - 注册验证码 / 找回密码验证码 / 企业注册成功提醒 / 租户管理员通知 / 系统告警
//   - 模板存于 system_config.mail_templates（JSON：{code:{subject,body,cc}}），未配置字段回退内置默认
//   - 支持 {var} 占位符替换（如 {code}/{name}/{username}/{email}/{brand}/{title}/{content}/{level}）
//   - GET  /api/admin/mail-templates 仅超管：返回全部模板当前生效内容 + 用途/变量说明
//   - PUT  /api/admin/mail-templates 仅超管：保存（覆盖）指定模板的 subject/body/cc
// =============================================
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"translator/internal/mail"
)

// defaultBrand 平台默认品牌名（占位符 {brand} 的回退值）。
const defaultBrand = "能言 LangCross"

// MailTpl 单一邮件模板的可配置内容。
type MailTpl struct {
	Subject string `json:"subject"` // 邮件主题（支持 {var}）
	Body    string `json:"body"`    // 邮件正文（纯文本，支持 {var}）
	CC      string `json:"cc"`      // 抄送邮箱（可选；空=不抄送）
}

// MailTplMeta 模板元信息：用途说明、默认内容、可用占位符。
type MailTplMeta struct {
	Code    string   `json:"code"`    // 模板标识（用途）
	Name    string   `json:"name"`    // 中文用途名
	Desc    string   `json:"desc"`    // 用途说明
	Vars    []string `json:"vars"`    // 可用占位符（如 code/name）
	Default MailTpl  `json:"default"` // 内置默认内容（未配置时生效）
}

// mailTplMetas 系统内置的邮件模板清单（不同用处，支持多个）。
var mailTplMetas = []MailTplMeta{
	{
		Code: "register_code", Name: "注册验证码",
		Desc: "用户自助注册时发送的邮箱验证码",
		Vars: []string{"code", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】注册验证码",
			Body:    "您好，\n\n您的注册验证码是：{code}\n\n该验证码 10 分钟内有效，请勿泄露给他人。\n\n—— {brand}",
		},
	},
	{
		Code: "reset_code", Name: "找回密码验证码",
		Desc: "用户找回密码时发送的验证码",
		Vars: []string{"code", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】密码重置验证码",
			Body:    "您好，\n\n您的密码重置验证码是：{code}\n\n该验证码 10 分钟内有效，请勿泄露给他人。\n\n—— {brand}",
		},
	},
	{
		Code: "enterprise_reg", Name: "企业注册成功提醒",
		Desc: "企业用户注册成功后，发送给注册人并抄送运营（建议建联）",
		Vars: []string{"name", "username", "email", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】企业注册成功提醒",
			Body:    "欢迎加入 {brand}！\n\n企业名称：{name}\n管理员账号：{username}\n联系邮箱：{email}\n\n我们已收到您的企业注册信息，将尽快与您建联。",
			CC:      "575160894@qq.com",
		},
	},
	{
		Code: "tenant_notify", Name: "租户管理员通知",
		Desc: "系统向租户管理员推送的站内/邮件通知",
		Vars: []string{"title", "body", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】{title}",
			Body:    "{body}\n\n—— {brand}",
		},
	},
	{
		Code: "alert", Name: "系统告警通知",
		Desc: "系统监控/阈值告警通知（如邀请奖励触顶、余额不足等）",
		Vars: []string{"title", "content", "level", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】系统告警：{title}",
			Body:    "告警级别：{level}\n\n{content}\n\n—— {brand} 监控系统",
		},
	},
	{
		Code: "manual", Name: "产品手册(注册欢迎)",
		Desc: "注册成功后随邮件发给新用户的《产品手册》PDF（此正文即 PDF 内容，支持 #/##/- 排版标记，超管可改）",
		Vars: []string{"username", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】欢迎使用 · 产品手册",
			Body: manualDefaultBody,
		},
	},
}

// manualDefaultBody 产品手册默认正文（注册成功邮件附件 PDF 的内容源）。
// 面向小白：先讲价值与卖点，再分别给「个人用户」与「企业用户」一步一步的上手步骤。
const manualDefaultBody = `# 能言 LangCross 产品手册（新手一步到位）

欢迎使用能言 LangCross！本手册用最通俗的话，带你从零用起来。无论你是个人译者，还是企业团队，都能在 10 分钟内跑通第一条翻译。

## 一、它能帮你解决什么（核心价值）
- 翻译不再是「每次从零翻」：历史译文自动沉淀为「翻译记忆」，重复句子自动复用，越用越省、越用越一致。
- 品牌词不乱翻：内置「术语库」，保证全公司对外文案「同一个词一个译法」。
- 机器翻译 + 人工质控：多模型翻译叠加安全短语与术语一致性校验，避免机翻味与错译。
- 个人轻量上手、企业独立空间：个人拿试用额度即用；企业建组织、发邀请、管成员、可扩展套餐。

## 二、注册后第一步（必做）
- 打开注册时填写的邮箱，查收「验证码邮件」，回到注册页粘贴验证码完成激活。
- 若未收到：检查垃圾箱；仍未收到可点「重新发送」。
- 登录后先到「设置 → 个人资料」确认邮箱，便于后续找回密码与接收通知。

## 三、个人用户怎么用（一步一步）
1. 登录后进入主工作台，在输入框粘贴或输入原文，选择目标语言，点击「翻译」。
2. 对结果不满意：在译文上直接修改，系统会记住这次修正（翻译记忆）。
3. 建立你的「个人术语库」：把常翻的品牌名、专有名词加入术语库，以后自动套用。
4. 想多拿额度：在「邀请好友」里把你的邀请码发给朋友，朋友注册后你可得试用 token。
5. 需要批量：使用「文档翻译 / 批量」功能，上传文件一次性翻译。

## 四、企业用户怎么用（一步一步）
1. 注册时选择「企业用户」，填写企业编码、企业名称、行业，你即为该企业管理员。
2. 在「企业管理」里邀请同事：生成邀请码或链接，同事凭此加入你的企业空间。
3. 配置「企业术语库 / 行业包」：统一全公司的译法标准。
4. 在「品牌定制」里上传企业 Logo、设置登录页配色与子域名，给客户专属体验。
5. 管理额度与套餐：在「用量 / 套餐」查看消耗，按需扩容；可开启「邀请好友」让员工裂变获额度。
6. 管理员可在「审计 / 告警」查看成员操作与系统告警。

## 五、常见问题
- 忘记密码：登录页点「忘记密码」，凭绑定邮箱验证码重置。
- 翻译不对：直接改译文即可沉淀记忆；检查术语库是否覆盖该词。
- 收不到邮件：确认邮箱正确且在垃圾箱；联系管理员核对邮件配置。
- 想升级：企业管理员在「套餐」中扩容，或个人在「邀请好友」获取更多试用额度。

祝你翻译愉快，越翻越聪明！—— 能言 LangCross 团队`

// mailTplMeta 按 code 取模板元信息（含默认内容）。
func mailTplMeta(code string) (MailTplMeta, bool) {
	for _, m := range mailTplMetas {
		if m.Code == code {
			return m, true
		}
	}
	return MailTplMeta{}, false
}

// loadCustomMailTpls 读取超管自定义模板（system_config.mail_templates）。
func (s *Server) loadCustomMailTpls() map[string]MailTpl {
	out := map[string]MailTpl{}
	raw, err := s.Store.GetConfig("mail_templates")
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// getMailTpl 返回某模板当前生效内容（用户覆盖优先，未配置字段回退默认）。
func (s *Server) getMailTpl(code string) MailTpl {
	def := defaultBrandTpl(code)
	custom := s.loadCustomMailTpls()
	if t, ok := custom[code]; ok {
		if t.Subject != "" {
			def.Subject = t.Subject
		}
		if t.Body != "" {
			def.Body = t.Body
		}
		if t.CC != "" {
			def.CC = t.CC
		}
	}
	return def
}

// defaultBrandTpl 返回某模板的内置默认内容（找不到 code 时退化为以 code 作主题）。
func defaultBrandTpl(code string) MailTpl {
	if m, ok := mailTplMeta(code); ok {
		return m.Default
	}
	return MailTpl{Subject: code, Body: ""}
}

// replaceVars 用 data 替换字符串中的 {key} 占位符。
func replaceVars(s string, data map[string]string) string {
	for k, v := range data {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// renderMailTpl 渲染模板为可发送邮件（替换占位符）。
func renderMailTpl(tpl MailTpl, data map[string]string) *mail.Message {
	if _, ok := data["brand"]; !ok {
		data["brand"] = defaultBrand
	}
	return &mail.Message{
		Subject: replaceVars(tpl.Subject, data),
		Body:    replaceVars(tpl.Body, data),
		CC:      replaceVars(tpl.CC, data),
	}
}

// sendTemplatedMail 按模板发送邮件（自动套用当前生效模板并渲染占位符）。
// 参数 to=收件人；code=模板标识；data=占位符数据。返回发送错误（Noop 模式不报错）。
func (s *Server) sendTemplatedMail(to, code string, data map[string]string) error {
	msg := renderMailTpl(s.getMailTpl(code), data)
	msg.To = to
	if err := s.mailer().Send(msg); err != nil {
		log.Printf("[mail] 发送失败 to=%s tpl=%s err=%v", to, code, err)
		return err
	}
	return nil
}

// sendManualEmail 注册成功后给新用户发送《产品手册》PDF 邮件（附件为你提供的产品手册 PDF 文件）。
// 使用 info@lexicorn.cn 专用邮箱发送；邮件正文模板内容可在后台「邮件模板」中配置（manual 模板）。
// PDF 附件来源（按优先级）：system_config.manual_pdf_path > 环境变量 MANUAL_PDF_PATH >
// 默认路径 /opt/translator/data/manual.pdf。找不到时仍发送正文邮件（仅不含附件），并在日志提示。
func (s *Server) sendManualEmail(to, username string) error {
	tpl := s.getMailTpl("manual")
	body := fmt.Sprintf("亲爱的 %s：\n\n欢迎使用能言 LangCross！附件为《产品手册》PDF，包含个人与企业用户的上手步骤，建议先花 5 分钟阅读。\n\n—— 能言 LangCross 团队", username)
	msg := &mail.Message{
		Subject: tpl.Subject,
		Body:    body,
	}
	pdf, path, err := s.loadManualPDF()
	if err != nil {
		log.Printf("[mail] 手册PDF未找到 to=%s err=%v（将仅发送正文邮件）", to, err)
	} else {
		msg.Attachments = []mail.Attachment{{Name: "产品手册.pdf", Data: pdf}}
	}
	msg.To = to
	if err := s.infoMailer().Send(msg); err != nil {
		log.Printf("[mail] 手册邮件发送失败 to=%s err=%v", to, err)
		return err
	}
	if path != "" {
		log.Printf("[mail] 手册邮件已发送 to=%s (附件PDF <- %s, %d字节)", to, path, len(pdf))
	} else {
		log.Printf("[mail] 手册邮件已发送 to=%s (无附件PDF)", to)
	}
	return nil
}

// loadManualPDF 按优先级读取产品手册 PDF 文件，返回内容、命中路径。
func (s *Server) loadManualPDF() (data []byte, path string, err error) {
	candidates := []string{}
	if p, e := s.Store.GetConfig("manual_pdf_path"); e == nil && p != "" {
		candidates = append(candidates, p)
	}
	if p := os.Getenv("MANUAL_PDF_PATH"); p != "" {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, "/opt/translator/data/manual.pdf", "manual.pdf")
	for _, c := range candidates {
		if d, e := os.ReadFile(c); e == nil && len(d) > 0 {
			return d, c, nil
		}
	}
	return nil, "", fmt.Errorf("未找到产品手册PDF（请配置 system_config.manual_pdf_path 或环境变量 MANUAL_PDF_PATH，或将文件放到 /opt/translator/data/manual.pdf）")
}

// handleAdminMailTemplates 邮件模板管理入口（GET=读取 / PUT=保存），均仅超管。
func (s *Server) handleAdminMailTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAdminMailTemplatesGet(w, r)
	case http.MethodPut, http.MethodPost:
		s.handleAdminMailTemplatesPut(w, r)
	default:
		writeJSON(w, 405, map[string]interface{}{"success": false, "message": "方法不允许"})
	}
}

// handleAdminMailTemplatesGet 仅超管：返回全部邮件模板当前生效内容 + 用途/变量说明。
func (s *Server) handleAdminMailTemplatesGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireSuperJSON(w, r); !ok {
		return
	}
	custom := s.loadCustomMailTpls()
	list := make([]map[string]interface{}, 0, len(mailTplMetas))
	for _, m := range mailTplMetas {
		t := m.Default
		if c, ok := custom[m.Code]; ok {
			if c.Subject != "" {
				t.Subject = c.Subject
			}
			if c.Body != "" {
				t.Body = c.Body
			}
			if c.CC != "" {
				t.CC = c.CC
			}
		}
		list = append(list, map[string]interface{}{
			"code":        m.Code,
			"name":        m.Name,
			"desc":        m.Desc,
			"vars":        m.Vars,
			"subject":     t.Subject,
			"body":        t.Body,
			"cc":          t.CC,
			"is_modified": custom[m.Code].Subject != "" || custom[m.Code].Body != "" || custom[m.Code].CC != "",
		})
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "templates": list})
}

// handleAdminMailTemplatesPut 仅超管：保存（覆盖）指定模板的 subject/body/cc。
// body: {"templates": {"register_code": {"subject": "...", "body": "...", "cc": "..."}, ...}}
func (s *Server) handleAdminMailTemplatesPut(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireSuperJSON(w, r); !ok {
		return
	}
	var req struct {
		Templates map[string]MailTpl `json:"templates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 仅允许已知模板 code，且只保留合法字段
	merged := s.loadCustomMailTpls()
	for code, tpl := range req.Templates {
		if _, ok := mailTplMeta(code); !ok {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "未知模板类型: " + code})
			return
		}
		merged[code] = MailTpl{Subject: tpl.Subject, Body: tpl.Body, CC: tpl.CC}
	}
	raw, _ := json.Marshal(merged)
	if err := s.Store.SetConfig("mail_templates", string(raw)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "邮件模板已保存"})
}
