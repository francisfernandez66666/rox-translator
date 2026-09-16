// ============ mail_test.go · 邮件层单测（2026-09-16 测试盲区补全） ============
// 覆盖：
//   - encodeMIMEHeader：ASCII 直通 / 中文 RFC2047 base64 编码可逆
//   - buildMailMessage：纯文本报文字节级结构（CRLF 行尾 / base64 正文可解码）
//   - buildMailMessage 附件形态：multipart boundary 闭环 / 附件段可解码 / Cc 头
//   - NoopSender：nil 消息报错 / 正常消息不报错（不外发）
//
// 此前 mail 包零测试：SMTP 报文构造仅靠线上验证，主题编码/boundary 拼装回归无防护。
package mail

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestEncodeMIMEHeaderASCII(t *testing.T) {
	const s = "Reset Your Password - LangCross"
	if got := encodeMIMEHeader(s); got != s {
		t.Fatalf("纯 ASCII 主题应原样直通，got %q", got)
	}
}

func TestEncodeMIMEHeaderChineseRFC2047(t *testing.T) {
	const s = "密码重置验证码"
	got := encodeMIMEHeader(s)
	if !strings.HasPrefix(got, "=?UTF-8?B?") || !strings.HasSuffix(got, "?=") {
		t.Fatalf("中文主题应编码为 RFC2047 base64 词，got %q", got)
	}
	// 解码还原校验（=?UTF-8?B?<b64>?= → 原文）
	payload := strings.TrimSuffix(strings.TrimPrefix(got, "=?UTF-8?B?"), "?=")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || string(raw) != s {
		t.Fatalf("RFC2047 编码不可逆：decoded=%q err=%v", string(raw), err)
	}
}

func TestBuildMailMessagePlainText(t *testing.T) {
	msg, err := buildMailMessage("noreply@langcross.test", &Message{
		To: "user@example.com", Subject: "Subject X", Body: "正文内容 hello",
	})
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	for _, want := range []string{
		"From: noreply@langcross.test\r\n",
		"To: user@example.com\r\n",
		"Subject: Subject X\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
		"Content-Transfer-Encoding: base64\r\n",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("报文缺少头 %q；报文=%q", want, msg)
		}
	}
	// 头与体之间必须是空行（\r\n\r\n），行尾必须 CRLF（裸 \n 视为违规）
	if !strings.Contains(msg, "\r\n\r\n") {
		t.Fatalf("头体之间缺少 CRLF 空行")
	}
	if strings.Count(msg, "\n") != strings.Count(msg, "\r\n") {
		t.Fatalf("报文存在裸 LF 行尾（SMTP 违规）：%q", msg)
	}
	// 正文 base64 可解码且还原原文
	body := msg[strings.Index(msg, "\r\n\r\n")+4:]
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil || string(raw) != "正文内容 hello" {
		t.Fatalf("正文 base64 解码不符：decoded=%q err=%v", string(raw), err)
	}
}

func TestBuildMailMessageWithAttachment(t *testing.T) {
	pdf := []byte("%PDF-1.4 fake-bytes")
	msg, err := buildMailMessage("noreply@langcross.test", &Message{
		To: "a@b.com", CC: "c@d.com", Subject: "手册", Body: "见附件",
		Attachments: []Attachment{{Name: "manual.pdf", Data: pdf}},
	})
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	if !strings.Contains(msg, `Content-Type: multipart/mixed; boundary="MIME_boundary_9f3c2a7b"`) {
		t.Fatalf("附件报文缺少 multipart/mixed 头")
	}
	if !strings.Contains(msg, "Cc: c@d.com\r\n") {
		t.Fatalf("附件报文缺少 Cc 头")
	}
	if !strings.Contains(msg, `Content-Disposition: attachment; filename="manual.pdf"`) {
		t.Fatalf("附件段缺少 Content-Disposition")
	}
	// boundary 闭环：每段 --boundary 都有闭合 --boundary--
	const b = "--MIME_boundary_9f3c2a7b"
	if strings.Count(msg, b) < 3 || !strings.Contains(msg, b+"--\r\n") {
		t.Fatalf("multipart boundary 未闭环")
	}
	// 附件字节可解码还原
	if !strings.Contains(msg, base64.StdEncoding.EncodeToString(pdf)) {
		t.Fatalf("附件 base64 编码与原始字节不符")
	}
	// 行尾全 CRLF
	if strings.Count(msg, "\n") != strings.Count(msg, "\r\n") {
		t.Fatalf("附件报文存在裸 LF 行尾")
	}
}

func TestBuildMailMessageNilAndEmptyTo(t *testing.T) {
	if _, err := buildMailMessage("f@x.com", nil); err == nil {
		t.Fatalf("nil 消息应报错")
	}
	if _, err := buildMailMessage("f@x.com", &Message{Subject: "s"}); err == nil {
		t.Fatalf("收件人为空应报错")
	}
}

func TestNoopSender(t *testing.T) {
	s := &NoopSender{}
	if err := s.Send(nil); err == nil {
		t.Fatalf("NoopSender(nil) 应报错")
	}
	if err := s.Send(&Message{To: "a@b.com", Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("NoopSender 正常消息不应报错: %v", err)
	}
}

func TestNewSenderFallback(t *testing.T) {
	// 未配置 SMTP → NoopSender；配置齐全 → SMTPSender（不外发，仅验证类型路由）
	if _, ok := NewSender(nil).(*NoopSender); !ok {
		t.Fatalf("nil 配置应回退 NoopSender")
	}
	if _, ok := NewSender(&Config{Enabled: false}).(*NoopSender); !ok {
		t.Fatalf("MAIL_ENABLED=0 应回退 NoopSender")
	}
	if _, ok := NewSender(&Config{Enabled: true, Host: "smtp.x.com", User: "u"}).(*SMTPSender); !ok {
		t.Fatalf("配置齐全应返回 SMTPSender")
	}
}
