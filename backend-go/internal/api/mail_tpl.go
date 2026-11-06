// ============ mail_tpl.go · 职责说明 ============
// 邮件模板（按用途区分的多个模板，支持前端后台配置，仅超管可改）：
//   - 注册验证码 / 找回密码验证码 / 企业注册成功提醒 / 租户管理员通知 / 系统告警
//   - 模板存于 system_config.mail_templates（JSON：{code:{subject,body,cc}}），未配置字段回退内置默认
//   - ★ F-17（2026-09-25 批E）语种跟随：同一 JSON 里以点分键存语种稿（如 "register_code.en"），
//     解析序 {code}.{lang} → {code}.en（非中文系）→ {code}（无后缀=中文，老配置零迁移）→ 内置默认
//     （验证码类内置中英双稿，其余语种回落英文稿）；管理台 UI 本批不加语种维度，超管直配 system_config
//   - 支持 {var} 占位符替换（如 {code}/{name}/{username}/{email}/{brand}/{title}/{content}/{level}）
//   - GET  /api/admin/mail-templates 仅超管：返回全部模板当前生效内容 + 用途/变量说明
//   - PUT  /api/admin/mail-templates 仅超管：保存（覆盖）指定模板的 subject/body/cc
//
// =============================================
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/mail"
)

// defaultBrand 平台默认品牌名（占位符 {brand} 的回退值）。
const defaultBrand = "能言 LangCross"

// ============ F-17（批E）邮件语种：12 语种白名单与归一化 ============

// mailUILangNames 界面语言 12 码白名单（★ 与前端 i18n Lang 联合类型同口径：
// zh/zh_hant/en/ru/fr/ar/es/pt/de/ja/ko/th）。非法值落空串=中文链路（与 job_role 静默忽略同口径）。
var mailUILangNames = map[string]bool{
	"zh": true, "zh_hant": true, "en": true, "ru": true, "fr": true, "ar": true,
	"es": true, "pt": true, "de": true, "ja": true, "ko": true, "th": true,
}

// normalizeMailLang 归一化原始语种输入：去空白/转小写/连字符转下划线后过 12 码白名单；
// 白名单外（含空串）一律返回 ""（= 未选语种，走中文链路，不做 Accept-Language 双判据）。
func normalizeMailLang(raw string) string {
	l := strings.ToLower(strings.TrimSpace(raw))
	l = strings.ReplaceAll(l, "-", "_")
	if mailUILangNames[l] {
		return l
	}
	return ""
}

// isChineseMailLang 中文系（简/繁）与空串均按中文链路处理（app_lang 空留 zh 口径）。
func isChineseMailLang(l string) bool { return l == "" || l == "zh" || l == "zh_hant" }

// requestMailLang 从请求提取界面语种：优先载荷 app_lang（注册/发码表单随 getLang() 上报），
// 其次 X-App-Lang 头（〇-S #12 起 authHeaders 对全部带认证头请求自动附带）。
func requestMailLang(r *http.Request, bodyLang string) string {
	if l := normalizeMailLang(bodyLang); l != "" {
		return l
	}
	return normalizeMailLang(r.Header.Get("X-App-Lang"))
}

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
	Default MailTpl  `json:"default"` // 内置默认内容（未配置时生效，中文）
	// DefaultEn 内置英文稿（★ F-17 批E：仅验证码类提供——非中文语种且超管未配置时回落英文，
	// 判定依据「误翻风险大于收益」：验证码文案短、机核无歧义，可放心内置；正文类通知仍回中文母稿）。
	// Subject 为空视为「未提供英文稿」。
	DefaultEn MailTpl `json:"default_en"`
}

// mailTplMetas 系统内置的邮件模板清单（不同用处，支持多个）。
var mailTplMetas = []MailTplMeta{
	// —— 验证码类：注册/找回密码（占位符 code/brand，10 分钟有效口径写在正文）——
	// ★ F-17 批E：内置中英双稿——非中文语种且超管未配置语种稿时回落英文（其余十语共用英文稿）。
	{
		Code: "register_code", Name: "注册验证码",
		Desc: "用户自助注册时发送的邮箱验证码",
		Vars: []string{"code", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】注册验证码",
			Body:    "您好，\n\n您的注册验证码是：{code}\n\n该验证码 10 分钟内有效，请勿泄露给他人。\n\n—— {brand}",
		},
		DefaultEn: MailTpl{
			Subject: "[{brand}] Your verification code",
			Body:    "Hello,\n\nYour verification code is: {code}\n\nThis code expires in 10 minutes. Please keep it confidential.\n\n—— {brand}",
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
		DefaultEn: MailTpl{
			Subject: "[{brand}] Your password reset code",
			Body:    "Hello,\n\nYour password reset code is: {code}\n\nThis code expires in 10 minutes. Please keep it confidential.\n\n—— {brand}",
		},
	},
	// —— 通知类模板：以下按 企业注册提醒/租户通知/企业邀请链接/系统告警/欢迎手册 顺序 ——
	{
		Code: "enterprise_reg", Name: "企业注册成功提醒",
		Desc: "企业用户注册成功后，发送给注册人并抄送运营（建议建联）",
		Vars: []string{"name", "username", "email", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】企业注册成功提醒",
			Body:    "欢迎加入 {brand}！\n\n企业名称：{name}\n管理员账号：{username}\n联系邮箱：{email}\n\n我们已收到您的企业注册信息，将尽快与您建联。",
			CC:      opsNotifyEmail(), // ★ B11：运营抄送取自 OPS_NOTIFY_EMAIL（空=不抄送）
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
		Code: "invite_link", Name: "企业邀请链接通知",
		Desc: "管理台建号/批量导入后，向受邀人发送一次性专属邀请链接（由本人自设邮箱与密码，★ 2026-10-24 R9 取代旧的初始密码邮件）",
		Vars: []string{"username", "invite_url", "expires_at", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】邀请您加入企业",
			Body:    "您好，\n\n管理员邀请您加入 {brand} 企业：\n邀请账号：{username}\n加入链接：{invite_url}\n链接有效期：{expires_at}\n\n请点开链接自行设置邮箱与密码；链接仅可使用一次，过期或已使用后请联系管理员重新生成。\n\n—— {brand}",
		},
	},
	// —— 运营触达类：告警通知与注册欢迎手册（manual 正文见 manualDefaultBody 常量）——
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
		// ★ 2026-11-06 E 组：这封信**不再挂 PDF 附件**，改为给站内手册链接（理由见 manualGreeting 头部三条）。
		//   这里同时如实改掉运营看到的说明——旧说明写着"此正文即 PDF 内容"，
		//   而代码从来只用本模板的**主题**，正文由 manualGreeting 按语种生成；留着它＝运营改完以为生效了。
		Desc: "注册成功后发给新用户的《产品手册》欢迎信（随邮件给出站内手册链接，不再附 PDF）。本模板当前只决定**主题**；正文按语种由系统生成",
		Vars: []string{"username", "brand"},
		Default: MailTpl{
			Subject: "【{brand}】欢迎使用 · 产品手册",
			Body:    manualDefaultBody,
		},
	},
}

// manualDefaultBody 产品手册默认正文（★ 注意：注册欢迎信的正文由 manualGreeting 按语种生成，
// 这份长文是**手册文字稿**的历史载体，改它不等于改用户收到的那封信——见 manual 模板 Desc 那条说明）。
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
4. 想多拿额度：在「邀请好友」里把你的邀请码发给朋友，朋友注册后你可得试用积分。
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

// getMailTpl 返回某模板对某语种当前生效内容（★ F-17 批E 语种跟随）。
// 解析优先级（低→高逐字段覆盖，空字段=该级未配置继续回落）：
// 内置中文母稿 → 自定义 {code}（无后缀=中文母稿，老配置零迁移）
// → 内置英文稿（仅验证码类有；非中文语种时压过中文自定义=「其余回落 en」条款）
// → 自定义 {code}.en（非中文语种）→ 自定义 {code}.{lang} 精确语种键（zh_hant 可单独配）。
// 中文系（zh/zh_hant/空）不套任何英文级，保证「app_lang 空留 zh」口径。
func (s *Server) getMailTpl(code, lang string) MailTpl {
	def := defaultBrandTpl(code)
	custom := s.loadCustomMailTpls()
	overlay := func(t MailTpl) {
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
	// 第 2 级：无后缀键=超管中文母稿（历史唯一形态；现仅中文链路与「无英文稿」模板沿用）
	if t, ok := custom[code]; ok {
		overlay(t)
	}
	if !isChineseMailLang(lang) {
		// 第 3 级：验证码类内置英文稿（超管只配了中文稿时，非中文语种回落英文而非误发中文）
		if m, ok := mailTplMeta(code); ok && m.DefaultEn.Subject != "" {
			overlay(m.DefaultEn)
		}
		// 第 4 级：自定义英文稿（lang=en 时并入第 5 级精确键，一次覆盖）
		if lang != "en" {
			if t, ok := custom[code+".en"]; ok {
				overlay(t)
			}
		}
		// 第 5 级：精确语种键（en / 其余十语）
		if t, ok := custom[code+"."+lang]; ok {
			overlay(t)
		}
	} else if lang == "zh_hant" {
		// 中文系唯一可有独立键的语种（繁体专配）
		if t, ok := custom[code+".zh_hant"]; ok {
			overlay(t)
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
// 改为异步：投递到任务队列由 worker 发送（失败自动重试/死信），避免 SMTP 阻塞注册/重置流程。
// 参数 lang：收件人界面语种（12 码白名单，空串=中文链路；★ F-17 批E 语种跟随）。
// 返回错误仅在「入队与同步降级均失败」时出现（极少见）。
func (s *Server) sendTemplatedMail(to, code, lang string, data map[string]string) error {
	// 渲染邮件模板：替换占位符
	msg := renderMailTpl(s.getMailTpl(code, lang), data)
	msg.To = to
	// 异步投递邮件（优先入队，队列不可用时降级同步发送）
	return s.enqueueMail(msg, false)
}

// manualGreeting 手册邮件正文问候语（中英两稿：中文系=原中文稿，其余回落英文——
// 与决策项 ③「其余回落 en」同口径，避免机翻误翻风险；超管可经 manual 模板语种键覆盖主题/正文级配置）。
// ★ 2026-11-06 E 组改形：**邮件不再带 PDF 附件，只给站内链接**。三个理由缺一不可：
//
//	① 附件逼近 20 MB 会被退信，那是**结构性**失败，压图只是推迟它；
//	② 带图 PDF 一旦流出去就收不回（邮件会被转发、被爬到），不发附件＝这一族风险清零；
//	③ 站内链接永远指向库里那份当前稿，而附件永远是被反复转发的旧稿。
//
// 参数 lang=归一化后的语种（可空）；username=收件人用户名；link=手册绝对链接（空＝不写那一行）。
func manualGreeting(lang, username, link string) string {
	// 链接拿不到时不许放一个空链接／假链接过去：一封写着"手册在这里："而后继空白的邮件，
	// 比一封不提手册的邮件更让客户以为站点坏了（AGENTS「读数不许为失败背书」同族）。
	line := ""
	if strings.TrimSpace(link) != "" {
		if isChineseMailLang(lang) {
			line = "\n《产品手册》（站内常年更新版）：" + link + "\n"
		} else {
			line = "\nUser guide (the current version, always): " + link + "\n"
		}
	}
	if isChineseMailLang(lang) {
		return fmt.Sprintf("亲爱的 %s：\n\n欢迎使用能言 LangCross！手册里有个人与企业用户的上手步骤，建议先花 5 分钟阅读。%s\n—— 能言 LangCross 团队", username, line)
	}
	return fmt.Sprintf("Dear %s,\n\nWelcome to LangCross! Our guide walks you through the first steps for both individual and enterprise users — a 5-minute read is worth it.%s\n—— The LangCross Team", username, line)
}

// buildManualMailMsg 组装手册邮件（纯函数，供单测断言主题渲染/正文语种/**链接形态**——
// sendManualEmail 的组消息部分抽出让「链接与模板语种」可离线锁定）。
//
// ★ 本函数**永不再挂附件**：判据里那条「Attachments 必须为空」是正向防线，
//
//	它挡的是"过一阵又有人觉得带附件更方便"——那时这条锁先红，而不是等一封退信才发现。
//
// 参数 to/username/lang：收件人；tpl：已按语种解析的 manual 模板；link：手册绝对链接（空＝不写那一行）。
func buildManualMailMsg(to, username, lang string, tpl MailTpl, link string) *mail.Message {
	// 主题走模板渲染（修复历史直挂 tpl.Subject 未替换 {brand} 的存量口径）
	msg := renderMailTpl(tpl, map[string]string{"username": username, "brand": defaultBrand})
	msg.Body = manualGreeting(lang, username, link)
	msg.To = to
	msg.Attachments = nil
	return msg
}

// manualLinkFor 拼手册链接的**唯一形状**：`/docs/manual/<语种码>.pdf`（与注册串那一个常量同源）。
//
// ★ 这里**不读 PDF 字节**，也**不重复实现回落链**：
//
//	回落（{lang} → en → zh）是 docs_manual.go 那一个路由的事实，邮件只递"请求语种"过去；
//	在这里再排一遍优先级＝第二把尺子，两处对"这份稿子在不在"的判断迟早相反（邮件说有、点开 404）。
//	所以只问一次 `manualPDFPresent`：**它在才给链接，不在就不写那一行**（见 manualGreeting 的纪律）。
//
// 参数 base=站点基址（scheme://host，与邀请链接同一个 inviteBaseURL），lang=注册界面语种；返回空串＝不给链接。
func (s *Server) manualLinkFor(base, lang string) string {
	code := normalizeMailLang(lang)
	if code == "" {
		code = "zh" // 空语种=中文链路（app_lang 空留 zh 口径），与 loadManualPDF 的缺省一致
	}
	if strings.TrimSpace(base) == "" || !s.manualPDFPresent(code) {
		return ""
	}
	return strings.TrimRight(base, "/") + manualPDFPrefix + code + manualPDFSuffix
}

// sendManualEmail 注册成功后发送《产品手册》**链接**邮件（个人/企业用户均发；用 info 专用邮箱）。
// 正文模板可在后台「邮件模板」中配置（manual 模板）。
// ★ F-17（2026-09-25 批E）语种跟随：主题与链接语种都随注册界面语言（可空=中文链路）。
// ★ E 组（2026-11-06）：**不再挂 PDF 附件**——退信是结构性的、发出去的附件收不回、
//
//	而站内链接永远指向当前稿（三条理由见 manualGreeting 头部）。
//
// 参数 base 由调用点在请求还在时算好传入（异步 goroutine 里不能读 r）。
func (s *Server) sendManualEmail(to, username, lang, base string) error {
	tpl := s.getMailTpl("manual", lang)
	link := s.manualLinkFor(base, lang)
	if link == "" {
		// 手册没铺库／基址取不到：如实点名，仍然发正文邮件，只是不写那一行链接
		log.Printf("[mail] 手册链接取不到 to=%s lang=%s base=%q（本封只发正文，不给链接）", to, lang, base)
	}
	msg := buildManualMailMsg(to, username, lang, tpl, link)
	if err := s.enqueueMail(msg, true); err != nil {
		return err
	}
	if link != "" {
		log.Printf("[mail] 手册邮件已入队 to=%s 链接=%s（附件已退役，不读 PDF 字节）", to, link)
	} else {
		log.Printf("[mail] 手册邮件已入队 to=%s（无链接、无附件）", to)
	}
	return nil
}

// enqueueMail 异步投递邮件：优先入队由 worker 发送；队列不可用时降级为同步发送，
// 确保注册/重置等关键流程在极端情况下仍可发出邮件（不静默丢失）。
func (s *Server) enqueueMail(msg *mail.Message, useInfo bool) error {
	if s.TicketSvc != nil && s.TicketSvc.Queue != nil {
		if _, err := s.TicketSvc.EnqueueMail(context.Background(), msg, useInfo); err != nil {
			log.Printf("[mail] 入队失败（降级同步发送） to=%s err=%v", msg.To, err)
			return s.syncSendMail(msg, useInfo)
		}
		log.Printf("[mail] 已入队 to=%s useInfo=%v", msg.To, useInfo)
		return nil
	}
	log.Printf("[mail] 队列不可用，同步发送 to=%s", msg.To)
	return s.syncSendMail(msg, useInfo)
}

// syncSendMail 同步发送邮件（enqueueMail 的降级路径）。
func (s *Server) syncSendMail(msg *mail.Message, useInfo bool) error {
	if useInfo {
		log.Printf("[mail] 同步发送(info) to=%s", msg.To)
		return s.infoMailer().Send(msg)
	}
	log.Printf("[mail] 同步发送 to=%s", msg.To)
	return s.mailer().Send(msg)
}

// loadManualPDF 按优先级读取产品手册 PDF 文件，返回内容、命中路径。
// ★ F-17（2026-09-25 批E）语种序：配置键 manual_pdf_dir 目录下 {lang}.pdf → en.pdf → zh.pdf
// （下划线码另试连字符变体文件名，兼容交付包 LangCross-User-Guide-zh-hant.pdf 一类命名）；
// 目录未配置或未命中时回落旧单文件链（manual_pdf_path > env MANUAL_PDF_PATH >
// /opt/translator/data/manual.pdf > manual.pdf）——老部署零改动仍可发中文手册。
// manualPDFCandidates 手册文件的**唯一一份**候选顺序（精确语种 → en → zh → 旧单文件链）。
// 读字节（loadManualPDF）与只判存在（manualPDFPresent）都必须走这里，
// 否则"邮件说有、点开 404"这种两处判断相反的情况就会长出来。
// 参数 lang=归一化后的语种码（可空）；返回按优先级排列的路径列表。
func (s *Server) manualPDFCandidates(lang string) []string {
	cands := []string{}
	// 1) 多语种目录：按「精确语种 → en → zh」次序找 {code}.pdf（去重）
	if dir, _ := s.Store.GetConfig("manual_pdf_dir"); strings.TrimSpace(dir) != "" {
		dir = strings.TrimSpace(dir)
		if lang == "" {
			lang = "zh" // 空语种=中文链路（app_lang 空留 zh 口径），优先命中 zh.pdf 而非 en.pdf
		}
		order := []string{}
		for _, c := range []string{lang, "en", "zh"} {
			if c == "" {
				continue
			}
			dup := false
			for _, seen := range order {
				if seen == c {
					dup = true
					break
				}
			}
			if !dup {
				order = append(order, c)
			}
		}
		for _, code := range order {
			// 文件名两种写法：下划线码（zh_hant.pdf）与连字符码（zh-hant.pdf）
			for _, c := range []string{code, strings.ReplaceAll(code, "_", "-")} {
				cands = append(cands, filepath.Join(dir, c+".pdf"))
			}
		}
	}
	// 2) 旧单文件链兜底（兼容既有部署：配置键/环境变量/默认路径/相对路径）
	candidates := []string{}
	if p, e := s.Store.GetConfig("manual_pdf_path"); e == nil && p != "" {
		candidates = append(candidates, p)
	}
	if p := os.Getenv("MANUAL_PDF_PATH"); p != "" {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, "/opt/translator/data/manual.pdf", "manual.pdf")
	cands = append(cands, candidates...)
	return cands
}

// manualPDFPresent 只判"这份稿子在不在"（os.Stat，**不读字节**），供邮件决定给不给链接。
// 参数 code=已归一的语种码；返回是否有可读的手册文件。
func (s *Server) manualPDFPresent(code string) bool {
	for _, p := range s.manualPDFCandidates(code) {
		if fi, e := os.Stat(p); e == nil && !fi.IsDir() && fi.Size() > 0 {
			return true
		}
	}
	return false
}

// loadManualPDF 按 manualPDFCandidates 的唯一顺序读取 PDF 字节，返回内容、命中路径。
// 参数 lang=归一化后的语种码（可空）；找不到时报错并点名该配哪个配置键。
func (s *Server) loadManualPDF(lang string) (data []byte, path string, err error) {
	for _, p := range s.manualPDFCandidates(lang) {
		if d, e := os.ReadFile(p); e == nil && len(d) > 0 {
			return d, p, nil
		}
	}
	return nil, "", fmt.Errorf("未找到产品手册PDF（请配置 system_config.manual_pdf_dir（多语种目录，内含 {lang}.pdf）或 manual_pdf_path，或将文件放到 /opt/translator/data/manual.pdf）")
}

// handleAdminMailTemplates 邮件模板管理入口（GET=读取 / PUT=保存），均仅超管。
func (s *Server) handleAdminMailTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAdminMailTemplatesGet(w, r)
	case http.MethodPut, http.MethodPost:
		s.handleAdminMailTemplatesPut(w, r)
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不允许"))
	}
}

// handleAdminMailTemplatesGet 仅超管：返回全部邮件模板当前生效内容 + 用途/变量说明。
func (s *Server) handleAdminMailTemplatesGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireSuperJSON(w, r); !ok {
		return
	}
	custom := s.loadCustomMailTpls()
	// 以内置默认为底、按字段套用自定义值合并出当前生效内容，is_modified 标记供前端展示「已改」
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
		// 输出结构 = 模板元信息（用途/占位符/默认值）+ 当前生效内容 + is_modified 标记
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
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	// 仅允许已知模板 code，且只保留合法字段
	merged := s.loadCustomMailTpls()
	for code, tpl := range req.Templates {
		if _, ok := mailTplMeta(code); !ok {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "未知模板类型: "+code))
			return
		}
		merged[code] = MailTpl{Subject: tpl.Subject, Body: tpl.Body, CC: tpl.CC}
	}
	raw, _ := json.Marshal(merged)
	if err := s.Store.SetConfig("mail_templates", string(raw)); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存失败: "+err.Error()))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "邮件模板已保存"})
}
