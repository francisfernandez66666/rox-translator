// Package auth 提供 JWT 签发/校验 + RBAC 角色校验 + 登录/改密。
package auth

// ============ 本文件职责中文说明 ============
// 认证与鉴权：JWT（HS256）的签发（Sign）与校验（Verify）、从 context 存取当前用户、
// 基于角色的权限等级校验（user < tenant_admin < super_admin，含旧值兼容）、
// 密码哈希（HMAC-SHA256 + salt）与校验、Authorization Bearer Token 提取。
// ========================================

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"translator/internal/store"
)

// Secret JWT 签名密钥：优先读取环境变量 JWT_SECRET（生产必须设置），
// 未设置则使用内置默认值（本地开发用；生产务必通过环境变量覆盖，避免密钥泄露被伪造 JWT）
var Secret = "trans-platform-jwt-secret-2026"

// init 包初始化：从环境变量 JWT_SECRET 读取签名密钥（生产必须设置）。
// 无返回值；若环境变量为空则沿用内置默认值（仅限本地开发，生产需显式覆盖）。
func init() {
	if v := os.Getenv("JWT_SECRET"); v != "" {
		Secret = v
	}
}

// RandomSecret 生成随机 JWT 密钥（供初始化工具使用；长度 32 字节 → 64 位 hex）
func RandomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Secret
	}
	return hex.EncodeToString(b)
}

// Claims JWT 载荷
type Claims struct {
	UserID   int64  `json:"uid"`      // 用户 ID
	TenantID int64  `json:"tid"`      // 所属租户 ID
	Username string `json:"username"` // 用户名
	Role     string `json:"role"`     // 角色（user/tenant_admin/super_admin 等）
	Exp      int64  `json:"exp"`      // 过期时间（Unix 秒）
}

// ctxUserKey context 存取当前用户的键类型
type ctxUserKey struct{}

// UserFromContext 从 context 取当前用户
func UserFromContext(ctx context.Context) *store.User {
	v, _ := ctx.Value(ctxUserKey{}).(*store.User)
	return v
}

// WithUser 将用户注入 context
func WithUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, ctxUserKey{}, u)
}

// b64 编码/解码
// b64Encode 将字节切片编码为 URL 安全的 Base64 字符串（无填充）。
func b64Encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// b64Decode 将 URL 安全的 Base64 字符串解码为原始字节。
// 参数 s: 待解码字符串；返回: (解码结果, 错误)。非法输入返回错误。
func b64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// Sign 生成 JWT
func Sign(u *store.User, ttl time.Duration) (string, error) {
	claims := Claims{
		UserID:   u.ID,
		TenantID: u.TenantID,
		Username: u.Username,
		Role:     u.Role,
		Exp:      time.Now().Add(ttl).Unix(),
	}
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	hb, _ := json.Marshal(header)
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64Encode(hb) + "." + b64Encode(cb)
	sig := signHS256(signingInput, Secret)
	return signingInput + "." + b64Encode(sig), nil
}

// signHS256 用 HMAC-SHA256 对指定输入计算签名（JWT 第三段签名部分的底层实现）
func signHS256(input, secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(input))
	return m.Sum(nil)
}

// Verify 校验 JWT 并返回 Claims
func Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("token 格式错误")
	}
	expected := signHS256(parts[0]+"."+parts[1], Secret)
	got, err := b64Decode(parts[2])
	if err != nil || !hmac.Equal(expected, got) {
		return nil, errors.New("签名无效")
	}
	cb, err := b64Decode(parts[1])
	if err != nil {
		return nil, errors.New("载荷无效")
	}
	var c Claims
	if err := json.Unmarshal(cb, &c); err != nil {
		return nil, err
	}
	if time.Now().Unix() > c.Exp {
		return nil, errors.New("token 已过期")
	}
	return &c, nil
}

// RoleLevel 角色等级（user < tenant_admin < super_admin）
// 兼容旧值：approver 视为 tenant_admin，admin 视为 super_admin
func RoleLevel(role string) int {
	switch role {
	case store.RoleSuperAdmin, store.RoleAdmin:
		return 3
	case store.RoleTenantAdmin, store.RoleApprover:
		return 2
	default:
		return 1
	}
}

// IsSuperAdmin 是否超级管理员
func IsSuperAdmin(u *store.User) bool {
	return u != nil && RoleLevel(u.Role) >= 3
}

// IsTenantAdmin 是否租户管理员及以上
func IsTenantAdmin(u *store.User) bool {
	return u != nil && RoleLevel(u.Role) >= 2
}

// RequireRole 校验角色等级不低于 required（super_admin=3/tenant_admin=2/user=1）
func RequireRole(u *store.User, required int) error {
	if u == nil {
		return errors.New("未登录")
	}
	if RoleLevel(u.Role) < required {
		return fmt.Errorf("权限不足：需要 %s 角色", roleName(required))
	}
	return nil
}

// roleName 角色等级 → 中文角色名（用于权限不足错误提示）
func roleName(level int) string {
	switch level {
	case 3:
		return "超级管理员"
	case 2:
		return "租户管理员"
	default:
		return "普通用户"
	}
}

// PasswordHash 密码哈希（bcrypt，成本因子默认 10）。
// 说明：历史版本使用 HMAC-SHA256（固定盐），CheckPassword 对两种格式均兼容，
// 登录成功后通过 MaybeMigrateHash 自动升级为 bcrypt，保证存量账号平滑迁移。
func PasswordHash(password string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		// bcrypt 仅对 >72 字节超长密码可能失败，兜底用 SHA-256 格式
		return "$sha256$" + legacyHash(password)
	}
	return string(h)
}

// CheckPassword 校验密码：兼容 bcrypt（$2a/$2b/$2c）与历史 SHA-256（$sha256$ 前缀）两种格式。
// 参数 hash: 存储的密码哈希；password: 明文密码。返回: 是否匹配。
func CheckPassword(hash, password string) bool {
	switch {
	case strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2c$"):
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	case strings.HasPrefix(hash, "$sha256$"):
		return legacyHash(password) == strings.TrimPrefix(hash, "$sha256$")
	default:
		// 极早期无前缀格式（base64 SHA-256）
		return PasswordHashLegacy(password) == hash
	}
}

// NeedMigrateHash 判断哈希是否需要升级为 bcrypt（返回 true 表示旧格式，登录成功后应重写）
func NeedMigrateHash(hash string) bool {
	return !(strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2c$"))
}

// legacyHash 历史 SHA-256 摘要（供 $sha256$ 前缀格式与迁移兜底使用）
func legacyHash(password string) string {
	sum := sha256.Sum256([]byte(password))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// PasswordHashLegacy 兼容函数：历史版本使用的 HMAC-SHA256 + 固定盐哈希（仅校验旧数据用）
func PasswordHashLegacy(password string) string {
	s := "trans-salt:" + password
	m := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m[:])
}

// BearerToken 从请求头提取 token
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
