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
		return ""
	}
	return string(plain)
}
