// S3 一次性邮箱黑名单单测：内置域/大小写/子域/运营增补域。
package api

import "testing"

// TestIsDisposableEmail 一次性邮箱域名黑名单判定（内置域 + 子域 + 误伤防护）。
func TestIsDisposableEmail(t *testing.T) {
	cases := []struct {
		email string
		want  bool
	}{
		{"foo@mailinator.com", true},
		{"FOO@Mailinator.NET ", true},
		{"a.b@yopmail.com", true},
		{"x@sub.mailinator.com", true}, // 子域后缀命中
		{"boss@corp.example.com", false},
		{"me+tag@gmail.com", false},
		{"no-at-sign", false},
		{"a@b", false},
	}
	for _, c := range cases {
		if got := isDisposableEmail(c.email, ""); got != c.want {
			t.Errorf("isDisposableEmail(%q)=%v 期望 %v", c.email, got, c.want)
		}
	}
	// 运营增补域（config 追加）
	if !isDisposableEmail("x@new-temp.example", "new-temp.example, other.cn") {
		t.Error("运营增补域应命中")
	}
	if isDisposableEmail("x@new-temp.example", "") {
		t.Error("未增补前不应命中")
	}
}
