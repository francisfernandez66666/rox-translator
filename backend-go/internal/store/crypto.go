// ============ crypto.go · 职责说明 ============
// store 包静态加密入口（AES-256-GCM）。
//
// ★ 改造 2（2026-09-17）：实现已下沉至 internal/secret，本文件仅保留同名薄委托，
// 使既有 90+ 调用点（store.EncryptSecret / store.DecryptSecret …）零改动，
// 同时让非存储层代码（如 internal/assist）能直接复用加密能力而不反向依赖业务存储层。
//
//   - 签发时把明文加密落库（key_enc 列），使「任意时刻可复制」成为可能；
//   - 加密密钥由 JWT_SECRET 派生（SHA-256 → 32 字节）；未配置时回退随机进程密钥
//     （仅本地/测试用，生产必须设置 JWT_SECRET）；
//   - Reveal 接口仅在租户管理员鉴权 + 租户隔离下解密返回。
//
// =============================================
package store

import "translator/internal/secret"

// SecretEncPrefix 静态加密密文前缀（enc:v1:）——区分「已加密」与「历史明文」。
const SecretEncPrefix = secret.SecretEncPrefix

// EncryptPlain AES-256-GCM 加密：返回 base64(nonce||ciphertext)。
func EncryptPlain(plain string) string { return secret.EncryptPlain(plain) }

// DecryptPlain 解密 base64(nonce||ciphertext)；失败返回空串。
func DecryptPlain(enc string) string { return secret.DecryptPlain(enc) }

// EncryptSecret 带前缀的通用静态加密（评审整改 D3）：用于 model_routes 等配置内的供应商 Key。
func EncryptSecret(plain string) string { return secret.EncryptSecret(plain) }

// DecryptSecret 与 EncryptSecret 配对；无前缀的输入按历史明文原样返回（兼容旧库）。
// 解密失败返回空串——调用方应打告警并跳过该条目（典型原因：JWT_SECRET 轮换未同步重存）。
func DecryptSecret(stored string) string { return secret.DecryptSecret(stored) }

// IsSecretMasked 判断是否为前端掩码串（sk-**** 形态）——保存链路据此回填旧值。
func IsSecretMasked(s string) bool { return secret.IsSecretMasked(s) }
