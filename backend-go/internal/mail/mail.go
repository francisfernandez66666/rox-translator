// ============ 本文件职责中文说明 ============
// 邮件通知抽象层：定义 Sender 发送接口，并提供两种实现：
//   - NoopSender：默认实现（未配置 SMTP 时使用），不发真实邮件，
//     仅在服务日志打印消息内容，前端提示「验证码/重置链接已生成（测试模式）」。
//   - SMTPSender：标准 SMTP 实现（net/smtp），配置 SMTP_HOST/PORT/USER/PASS 后启用，
//     用于发送验证码 / 密码重置链接等真实邮件。
//
// 通过环境变量 MAIL_ENABLED=1 + SMTP_* 系列变量启用真实邮件。
// =============================================
// Package mail 提供邮件发送抽象层：定义 Sender 接口与 NoopSender（测试占位）、
// SMTPSender（SMTP 真实发送）两种实现，用于验证码与密码重置链接等邮件通知。
package mail

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log"
	"net/smtp"
	"strings"
)

// Message 邮件消息结构。
type Message struct {
	To         string       // 收件人邮箱
	CC         string       // 抄送邮箱（可选；SMTP 发送时作为 Cc 收件人）
	Subject    string       // 邮件主题
	Body       string       // 邮件正文（纯文本）
	Attachments []Attachment // 附件（可选；存在时以 multipart/mixed 发送）
}

// Attachment 邮件附件（如产品手册 PDF）。
type Attachment struct {
	Name string // 附件文件名（如 产品手册.pdf）
	Data []byte // 附件二进制内容
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
	log.Printf("[MAIL-NOOP] To=%s CC=%s Subject=%s\n%s", m.To, m.CC, m.Subject, m.Body)
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
	log.Printf("[smtp] 开始发送 to=%s from=%s subject=%s", m.To, s.from, m.Subject)
	addr := s.host
	if s.port != "" {
		addr = s.host + ":" + s.port
	}
	// 构造邮件（含 From/To/Cc/Subject 头）
	// 主题做 RFC2047 编码（中文主题直接发送会被部分 SMTP 服务端拒收）；
	// 正文用 base64 传输编码，规避 8bit 非 ASCII 字符问题。
	// 存在附件时改用 multipart/mixed，文本与附件均为 base64 段。
	var msg string
	if len(m.Attachments) == 0 {
		msg = "From: " + s.from + "\r\n" +
			"To: " + m.To + "\r\n"
		if m.CC != "" {
			msg += "Cc: " + m.CC + "\r\n"
		}
		msg += "Subject: " + encodeMIMEHeader(m.Subject) + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"Content-Transfer-Encoding: base64\r\n" +
			"\r\n" + base64.StdEncoding.EncodeToString([]byte(m.Body))
	} else {
		const boundary = "MIME_boundary_9f3c2a7b"
		var sb strings.Builder
		sb.WriteString("From: " + s.from + "\r\n")
		sb.WriteString("To: " + m.To + "\r\n")
		if m.CC != "" {
			sb.WriteString("Cc: " + m.CC + "\r\n")
		}
		sb.WriteString("Subject: " + encodeMIMEHeader(m.Subject) + "\r\n")
		sb.WriteString("MIME-Version: 1.0\r\n")
		sb.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n")
		// 正文段
		sb.WriteString("--" + boundary + "\r\n")
		sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		sb.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		sb.WriteString(base64.StdEncoding.EncodeToString([]byte(m.Body)) + "\r\n")
		// 附件段
		for _, a := range m.Attachments {
			sb.WriteString("--" + boundary + "\r\n")
			sb.WriteString("Content-Type: application/pdf\r\n")
			sb.WriteString("Content-Transfer-Encoding: base64\r\n")
			sb.WriteString("Content-Disposition: attachment; filename=\"" + a.Name + "\"\r\n\r\n")
			sb.WriteString(base64.StdEncoding.EncodeToString(a.Data) + "\r\n")
		}
		sb.WriteString("--" + boundary + "--\r\n")
		msg = sb.String()
	}
	// ★ 发送通道：465 用隐式 TLS 直连（crypto/tls）；587 用 smtp.SendMail（STARTTLS 自动协商）
	auth := smtp.PlainAuth("", s.user, s.pass, s.host)
	if s.port == "465" {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: s.host})
		if err != nil {
			log.Printf("[smtp] TLS 连接失败 to=%s err=%v", m.To, err)
			return fmt.Errorf("SMTP TLS 连接失败: %w", err)
		}
		defer conn.Close()
		c, err := smtp.NewClient(conn, s.host)
		if err != nil {
			log.Printf("[smtp] 客户端创建失败 to=%s err=%v", m.To, err)
			return fmt.Errorf("SMTP 客户端创建失败: %w", err)
		}
		defer c.Close()
		if aerr := c.Auth(auth); aerr != nil {
			log.Printf("[smtp] 认证失败 to=%s err=%v", m.To, aerr)
			return fmt.Errorf("SMTP 认证失败: %w", aerr)
		}
		if aerr := c.Mail(s.from); aerr != nil {
			log.Printf("[smtp] 设置发件人失败 to=%s err=%v", m.To, aerr)
			return fmt.Errorf("设置发件人失败: %w", aerr)
		}
		if aerr := c.Rcpt(m.To); aerr != nil {
			log.Printf("[smtp] 设置收件人失败 to=%s err=%v", m.To, aerr)
			return fmt.Errorf("设置收件人失败: %w", aerr)
		}
		if m.CC != "" {
			if aerr := c.Rcpt(m.CC); aerr != nil {
				log.Printf("[smtp] 设置抄送失败 to=%s cc=%s err=%v", m.To, m.CC, aerr)
				return fmt.Errorf("设置抄送收件人失败: %w", aerr)
			}
		}
		w, werr := c.Data()
		if werr != nil {
			log.Printf("[smtp] 打开数据通道失败 to=%s err=%v", m.To, werr)
			return fmt.Errorf("打开数据通道失败: %w", werr)
		}
		if _, werr = w.Write([]byte(msg)); werr != nil {
			w.Close()
			log.Printf("[smtp] 写入内容失败 to=%s err=%v", m.To, werr)
			return fmt.Errorf("写入邮件内容失败: %w", werr)
		}
		if werr = w.Close(); werr != nil {
			log.Printf("[smtp] 提交邮件失败 to=%s err=%v", m.To, werr)
			return fmt.Errorf("提交邮件失败: %w", werr)
		}
		log.Printf("[smtp] 发送成功 to=%s", m.To)
		return c.Quit()
	}
	rcpts := []string{m.To}
	if m.CC != "" {
		rcpts = append(rcpts, m.CC)
	}
	if err := smtp.SendMail(addr, auth, s.from, rcpts, []byte(msg)); err != nil {
		log.Printf("[smtp] 发送失败 to=%s err=%v", m.To, err)
		return err
	}
	log.Printf("[smtp] 发送成功 to=%s", m.To)
	return nil
}

// encodeMIMEHeader 对邮件头字段（如 Subject）做 RFC2047 编码：含非 ASCII 字符时
// 编码为 =?UTF-8?B?...?=，纯 ASCII 原样返回（避免中文主题被部分 SMTP 服务端拒收）。
func encodeMIMEHeader(s string) string {
	if isASCII(s) {
		return s
	}
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
}

// isASCII 判断字符串是否仅含 ASCII 字符。
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// BuildVerificationBody 生成密码重置验证码邮件正文（通用）。
func BuildVerificationBody(code string) string {
	return fmt.Sprintf("您好，\n\n您的密码重置验证码是：%s\n\n该验证码 10 分钟内有效，请勿泄露给他人。\n\n—— 能言", code)
}

// BuildResetLinkBody 生成带重置链接的邮件正文（SMTP 模式使用，链接由调用方拼接）。
func BuildResetLinkBody(link string) string {
	return fmt.Sprintf("您好，\n\n请点击以下链接重置您的密码（10 分钟内有效）：\n\n%s\n\n如果非本人操作，请忽略本邮件。\n\n—— 能言", link)
}

// 小工具：确保 strings 包被引用（便于未来扩展 HTML 邮件）。
var _ = strings.TrimSpace
