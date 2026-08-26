// ============ 本文件职责中文说明 ============
// API Key 明文的静态加密（AES-256-GCM）：
//   - 签发时把明文加密落库（key_enc 列），使「任意时刻可复制」成为可能；
//   - 加密密钥由 JWT_SECRET 派生（SHA-256 → 32 字节）；未配置时回退固定开发密钥
//     （仅本地/测试用，生产必须设置 JWT_SECRET）；
//   - Reveal 接口仅在租户管理员鉴权 + 租户隔离下解密返回。
//
// =============================================
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"os"
	"strings"
)

// deriveAEADKey 由 JWT_SECRET 派生 32 字节 AES 密钥。
func deriveAEADKey() []byte {
	sum := sha256.Sum256([]byte(os.Getenv("JWT_SECRET") + "|rox-apikey-enc"))
	return sum[:]
}

// EncryptPlain AES-256-GCM 加密：返回 base64(nonce||ciphertext)。
func EncryptPlain(plain string) string {
	key := deriveAEADKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return ""
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct)
}

// DecryptPlain 解密 base64(nonce||ciphertext)；失败返回空串。
func DecryptPlain(enc string) string {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return ""
	}
	key := deriveAEADKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return ""
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return string("") // 认证失败：静默返回空（调用方应告警）
	}
	return string(plain)
}

// SecretEncPrefix 静态加密密文前缀：区分「已加密」与「历史明文」，支持平滑迁移。
const SecretEncPrefix = secretEncPrefix

// secretEncPrefix 前缀常量本体。
const secretEncPrefix = "enc:v1:"

// EncryptSecret 带前缀的通用静态加密（评审整改 D3）：用于 model_routes 等配置内的供应商 Key。
func EncryptSecret(plain string) string {
	if plain == "" {
		return ""
	}
	return secretEncPrefix + EncryptPlain(plain)
}

// DecryptSecret 与 EncryptSecret 配对；无前缀的输入按历史明文原样返回（兼容旧库）。
// 解密失败返回空串——调用方应打告警并跳过该条目（典型原因：JWT_SECRET 轮换未同步重存）。
func DecryptSecret(stored string) string {
	if stored == "" {
		return ""
	}
	if !strings.HasPrefix(stored, secretEncPrefix) {
		return stored // 历史明文
	}
	return DecryptPlain(strings.TrimPrefix(stored, secretEncPrefix))
}

// IsSecretMasked 判断是否为前端掩码串（sk-**** 形态）——保存链路据此回填旧值。
func IsSecretMasked(s string) bool {
	return strings.Contains(s, "****")
}
