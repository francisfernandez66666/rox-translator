// b10_legacy_password_test.go · ★ B10 历史口令格式回归：两种 legacy 可校验、
// 均触发透明升级标记，bcrypt 不触发。
package iam

import "testing"

func TestLegacyPasswordFormats(t *testing.T) {
	if !CheckPassword(PasswordHashLegacy("p@ss"), "p@ss") {
		t.Fatal("legacy salt 格式应可校验")
	}
	if CheckPassword(PasswordHashLegacy("p@ss"), "wrong") {
		t.Fatal("错误密码不得通过 legacy 校验")
	}
	if !NeedMigrateHash(PasswordHashLegacy("x")) {
		t.Fatal("legacy salt 需升级")
	}
	if !NeedMigrateHash("$sha256$" + legacyHash("x")) {
		t.Fatal("无盐 sha256 需升级")
	}
	b := PasswordHash("x")
	if NeedMigrateHash(b) {
		t.Fatal("bcrypt 不应触发升级")
	}
	if !CheckPassword(b, "x") {
		t.Fatal("bcrypt 校验失败")
	}
}
