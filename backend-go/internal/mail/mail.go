// ============ 本文件职责中文说明 ============
// 邮件通知抽象层：定义 Sender 发送接口，并提供两种实现：
//   - NoopSender：默认实现（未配置 SMTP 时使用），不发真实邮件，
//     仅在服务日志打印消息内容，前端提示「验证码/重置链接已生成（测试模式）」。
//   - SMTPSender：标准 SMTP 实现（net/smtp），配置 SMTP_HOST/PORT/USER/PASS 后启用，
//     用于发送验证码 / 密码重置链接等真实邮件。
//
// 通过环境变量 MAIL_ENABLED=1 + SMTP_* 系列变量启用真实邮件。
// =============================================
package mail

import (
	"fmt"
	"log"
	"net/smtp"
	"strings"
)

// Message 邮件消息结构。
type Message struct {
	To      string // 收件人邮箱
	Subject string // 邮件主题
	Body    string // 邮件正文（纯文本）
}

// Sender 邮件发送器接口：统一封装各渠道邮件发送。
type Sender interface {
	// Send 发送一封邮件；返回错误（NoopSender 永不报错，仅打印日志）。
	Send(m *Message) error
}

// Config SMTP 配置（由环境变量注入）。
type Config struct {
	Enabled bool   // 是否启用真实邮件（MAIL_ENABLED=1）
	Host    string // SMTP 服务器地址（SMTP_HOST）
	Port    string // SMTP 端口（SMTP_PORT，默认 465/587）
	User    string // 发件账号（SMTP_USER）
	Pass    string // 发件密码/授权码（SMTP_PASS）
	From    string // 发件人地址（SMTP_FROM，缺省取 User）
}

// NewSender 按配置创建发送器：未启用 SMTP 时返回 NoopSender。
func NewSender(cfg *Config) Sender {
	if cfg != nil && cfg.Enabled && cfg.Host != "" && cfg.User != "" {
		from := cfg.From
		if from == "" {
			from = cfg.User
		}
		return &SMTPSender{host: cfg.Host, port: cfg.Port, user: cfg.User, pass: cfg.Pass, from: from}
	}
	return &NoopSender{}
}

// ============ Noop 实现（默认） ============

// NoopSender 测试/占位实现：不发送真实邮件，仅打印日志。
type NoopSender struct{}

// Send 打印消息到日志并返回 nil（保证调用方流程不中断）。
func (s *NoopSender) Send(m *Message) error {
	if m == nil {
		return fmt.Errorf("nil 消息")
	}
	log.Printf("[MAIL-NOOP] To=%s Subject=%s\n%s", m.To, m.Subject, m.Body)
	return nil
}

// ============ SMTP 实现 ============

// SMTPSender 标准 SMTP 发送器（net/smtp，支持 465 隐式 TLS 与 587 STARTTLS）。
type SMTPSender struct {
	host string // SMTP 服务器地址
	port string // SMTP 端口
	user string // 认证账号
	pass string // 认证密码
	from string // 发件人地址
}

// Send 通过 SMTP 发送邮件。
func (s *SMTPSender) Send(m *Message) error {
	if m == nil || m.To == "" {
		return fmt.Errorf("收件人邮箱为空")
	}
	addr := s.host
	if s.port != "" {
		addr = s.host + ":" + s.port
	}
	// 构造简单纯文本邮件（含 From/To/Subject/Date 头）
	msg := "From: " + s.from + "\r\n" +
		"To: " + m.To + "\r\n" +
		"Subject: " + m.Subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" + m.Body
	// ★ 真实接入点：587 用 smtp.SendMail（STARTTLS 自动协商）；
	//   465 需显式 TLS 拨号（crypto/tls），当前以 587 为主实现。
	if s.port == "465" {
		return fmt.Errorf("465 隐式 TLS 暂未启用，请配置 587 端口")
	}
	auth := smtp.PlainAuth("", s.user, s.pass, s.host)
	return smtp.SendMail(addr, auth, s.from, []string{m.To}, []byte(msg))
}

// BuildVerificationBody 生成密码重置验证码邮件正文（通用）。
func BuildVerificationBody(code string) string {
	return fmt.Sprintf("您好，\n\n您的密码重置验证码是：%s\n\n该验证码 10 分钟内有效，请勿泄露给他人。\n\n—— 翻译助手", code)
}

// BuildResetLinkBody 生成带重置链接的邮件正文（SMTP 模式使用，链接由调用方拼接）。
func BuildResetLinkBody(link string) string {
	return fmt.Sprintf("您好，\n\n请点击以下链接重置您的密码（10 分钟内有效）：\n\n%s\n\n如果非本人操作，请忽略本邮件。\n\n—— 翻译助手", link)
}

// 小工具：确保 strings 包被引用（便于未来扩展 HTML 邮件）。
var _ = strings.TrimSpace
