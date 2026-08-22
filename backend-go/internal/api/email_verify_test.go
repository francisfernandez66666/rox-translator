// ============ 本文件职责中文说明 ============
// 邮箱验证码单元测试：校验通过消费、错误尝试计数与上限作废、过期失效。
package api

import (
	"strings"
	"testing"
	"time"
)

// newTestCode 直接向全局 emailCodes 注入一条验证码（绕过发送，聚焦校验逻辑）。
func newTestCode(email, code string, sentAt time.Time) {
	key := strings.ToLower(email)
	emailCodes.mu.Lock()
	defer emailCodes.mu.Unlock()
	emailCodes.codes[key] = &emailCode{Code: code, ExpiresAt: sentAt.Add(emailCodeTTL), SentAt: sentAt}
}

// TestVerifyEmailCodeConsume 正确验证码：一次通过并消费（第二次失败）。
func TestVerifyEmailCodeConsume(t *testing.T) {
	newTestCode("A@Ex.com", "123456", time.Now())
	if !verifyEmailCode("a@ex.com", "123456") { // 大小写归一化
		t.Fatal("正确验证码应通过")
	}
	if verifyEmailCode("a@ex.com", "123456") {
		t.Fatal("验证码应一次性消费")
	}
}

// TestVerifyEmailCodeAttempts 错误 5 次后即使答对也作废（防爆破）。
func TestVerifyEmailCodeAttempts(t *testing.T) {
	newTestCode("brute@x.com", "654321", time.Now())
	for i := 0; i < emailCodeMaxTries; i++ {
		if verifyEmailCode("brute@x.com", "000000") {
			t.Fatal("错误码不应通过")
		}
	}
	if !verifyEmailCode("brute@x.com", "654321") {
		return // 超限作废 → 正确码也拒绝（预期路径）
	}
	t.Fatal("超过最大错误次数后正确码也应作废")
}

// TestVerifyEmailCodeExpired 过期验证码不通过。
func TestVerifyEmailCodeExpired(t *testing.T) {
	newTestCode("old@x.com", "111111", time.Now().Add(-11*time.Minute))
	if verifyEmailCode("old@x.com", "111111") {
		t.Fatal("过期验证码不应通过")
	}
}

// TestEmailFormat 宽松邮箱格式正则抽查。
func TestEmailFormat(t *testing.T) {
	good := []string{"a@b.co", "user.name+tag@sub.domain.cn"}
	bad := []string{"", "noat", "a@b", "a b@c.com", "@x.com"}
	for _, e := range good {
		if !emailRe.MatchString(e) {
			t.Fatalf("%q 应为合法邮箱", e)
		}
	}
	for _, e := range bad {
		if emailRe.MatchString(e) {
			t.Fatalf("%q 应为非法邮箱", e)
		}
	}
}
