// ============ secret_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-6「安全敏感包零测试」）：internal/secret 的回归断言。
// 本包是全仓静态加密的唯一真源（模型路由 Key、API Key key_enc、webhook secret、SCIM token 都走它），
// 136 行代码此前**零测试**——改坏一次 GCM nonce 拼接或前缀判断，全库密文会静默不可解密。
// 钉住的行为：
//
//	① EncryptSecret/DecryptSecret 往返一致，且密文带 enc:v1: 前缀、同明文两次加密结果不同（随机 nonce）；
//	② 空串原样返回空（保持「未配置」语义，不产出空密文）；
//	③ 历史明文（无前缀）原样返回，保证旧库升级可读；
//	④ 密文被篡改 / base64 非法 / 密钥不一致 ⇒ 一律返回空串（GCM 认证失败必须可被调用方识别，绝不返回脏值）；
//	⑤ 掩码判定 IsSecretMasked 与保存链路「掩码不写回库」的约定一致。
//
// 另附 BenchmarkEncryptSecret/BenchmarkDecryptSecret（报告 §4.1-8：全仓零 Benchmark）。
// =============================================
package secret

import (
	"encoding/base64"
	"strings"
	"testing"
)

// fixedSecret 本测试固定 JWT_SECRET：让密钥派生确定，且能构造「密钥不一致」用例。
const fixedSecret = "unit-test-jwt-secret"

// TestEncryptDecryptRoundTrip 往返一致 + 前缀 + 非确定性密文（随机 nonce）。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Setenv("JWT_SECRET", fixedSecret)
	const plain = "sk-proj-非常敏感的供应商密钥-123456"

	c1 := EncryptSecret(plain)
	if c1 == plain {
		t.Fatal("★ 回归：加密结果与明文相同（等于没加密）")
	}
	if !strings.HasPrefix(c1, SecretEncPrefix) {
		t.Fatalf("密文必须以 %s 开头以区分历史明文，实得 %q", SecretEncPrefix, c1)
	}
	if DecryptSecret(c1) != plain {
		t.Fatalf("解密结果不符，实得 %q", DecryptSecret(c1))
	}
	// AES-GCM 每次随机 nonce：同一明文两次密文必须不同（否则可被模式分析识别）
	if c2 := EncryptSecret(plain); c2 == c1 {
		t.Fatal("两次加密得到相同密文，nonce 未随机化")
	} else if DecryptSecret(c2) != plain {
		t.Fatal("第二个密文解密失败")
	}
	// 空串语义：表示「未配置」，不得产出 enc:v1: 空密文（否则读侧无法区分未配置与解密失败）
	if got := EncryptSecret(""); got != "" {
		t.Fatalf("空串应保持空串，实得 %q", got)
	}
	if got := DecryptSecret(""); got != "" {
		t.Fatalf("解密空串应为空，实得 %q", got)
	}
}

// TestDecryptSecretLegacyPlaintext 无前缀的历史明文原样返回（旧库平滑升级的关键分支）。
func TestDecryptSecretLegacyPlaintext(t *testing.T) {
	t.Setenv("JWT_SECRET", fixedSecret)
	for _, legacy := range []string{"sk-plain-old-value", "not-prefixed:enc:v1", "  "} {
		if got := DecryptSecret(legacy); got != legacy {
			t.Fatalf("历史明文应原样返回，输入 %q 实得 %q", legacy, got)
		}
	}
	// 只有完整前缀才算密文：前缀被截断的脏数据按明文返回，不能当密文解（会误判为「解密失败」）
	if got := DecryptSecret("enc:v"); got != "enc:v" {
		t.Fatalf("非完整前缀应按明文处理，实得 %q", got)
	}
}

// TestDecryptSecretRejectsTampered 篡改/非法密文一律返回空串（认证加密的核心保证）。
func TestDecryptSecretRejectsTampered(t *testing.T) {
	t.Setenv("JWT_SECRET", fixedSecret)
	valid := EncryptSecret("top-secret-value")
	body := strings.TrimPrefix(valid, SecretEncPrefix)
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("测试前置失败，密文非法 base64: %v", err)
	}
	// ① 翻转最后一字节（篡改密文标签/内容）⇒ GCM 认证必须失败
	tampered := append([]byte{}, raw...)
	tampered[len(tampered)-1] ^= 0xFF
	if got := DecryptSecret(SecretEncPrefix + base64.StdEncoding.EncodeToString(tampered)); got != "" {
		t.Fatalf("篡改密文应返回空串，实得 %q", got)
	}
	// ② 截断到不足 nonce 长度
	if got := DecryptSecret(SecretEncPrefix + base64.StdEncoding.EncodeToString(raw[:4])); got != "" {
		t.Fatalf("过短密文应返回空串，实得 %q", got)
	}
	// ③ 非法 base64
	if got := DecryptSecret(SecretEncPrefix + "这不是base64!!"); got != "" {
		t.Fatalf("非法 base64 应返回空串，实得 %q", got)
	}
	// ④ 合法密文但密钥不一致（JWT_SECRET 轮换未重存）⇒ 空串，调用方据此告警
	t.Setenv("JWT_SECRET", "another-secret")
	if got := DecryptSecret(valid); got != "" {
		t.Fatalf("密钥不一致时应返回空串，实得 %q", got)
	}
}

// TestEncryptPlainPair EncryptPlain/DecryptPlain 裸接口（无前缀，assist 等外部协议用）同样往返成立。
func TestEncryptPlainPair(t *testing.T) {
	t.Setenv("JWT_SECRET", fixedSecret)
	if got := DecryptPlain(EncryptPlain("abc")); got != "abc" {
		t.Fatalf("裸接口往返失败，实得 %q", got)
	}
	if DecryptPlain("garbage!!") != "" {
		t.Fatal("裸接口非法输入应返回空串")
	}
	if DecryptPlain("") != "" {
		t.Fatal("裸接口空输入应返回空串")
	}
}

// TestIsSecretMasked 掩码判定：管理台回显 sk-**** 形态，保存链路据此回填旧值不覆盖真密钥。
func TestIsSecretMasked(t *testing.T) {
	cases := map[string]bool{
		"sk-****abcd":   true,  // 前端掩码回显
		"****":          true,  // 纯掩码
		"sk-1234567890": false, // 真密钥
		"":              false, // 未配置
		"sk-not-masked": false,
		"中间含 **** 的串":   true, // 中文上下文里的掩码同样要识别（避免把掩码写回库）
	}
	for in, want := range cases {
		if got := IsSecretMasked(in); got != want {
			t.Errorf("IsSecretMasked(%q)=%v，期望 %v", in, got, want)
		}
	}
}

// BenchmarkEncryptSecret 静态加密吞吐（配置保存链路：模型路由 Key、SCIM token 等）。
func BenchmarkEncryptSecret(b *testing.B) {
	b.Setenv("JWT_SECRET", fixedSecret)
	payload := strings.Repeat("benchmark-key-material-", 4) // ~100 字节典型供应商 Key
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EncryptSecret(payload)
	}
}

// BenchmarkDecryptSecret 静态解密吞吐（热路径：每次读取密文配置都会走这里）。
func BenchmarkDecryptSecret(b *testing.B) {
	b.Setenv("JWT_SECRET", fixedSecret)
	stored := EncryptSecret(strings.Repeat("benchmark-key-material-", 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DecryptSecret(stored)
	}
}
