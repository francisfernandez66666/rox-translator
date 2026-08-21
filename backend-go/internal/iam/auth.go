// ============ 本文件职责中文说明 ============
// 认证与鉴权：JWT（HS256）签发与校验、当前用户的 context 存取、
// 四级角色等级换算与权限断言（IsSuperAdmin/IsTenantAdmin/IsDeptAdmin/RequireRole）、
// 密码哈希（bcrypt，兼容历史 SHA-256/HMAC 格式并支持登录时自动升级）。
// 密钥来源：环境变量 JWT_SECRET（生产必须设置），未设置时使用内置默认值（仅限本地开发）。
// =============================================
package iam

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
)

var jwtSecret = "trans-platform-jwt-secret-2026"

func init() {
	if v := os.Getenv("JWT_SECRET"); v != "" {
		jwtSecret = v
	}
}

func RandomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return jwtSecret
	}
	return hex.EncodeToString(b)
}

type Claims struct {
	UserID   int64  `json:"uid"`
	TenantID int64  `json:"tid"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"`
}

type ctxUserKey struct{}

func UserFromContext(ctx context.Context) *User {
	v, _ := ctx.Value(ctxUserKey{}).(*User)
	return v
}

func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxUserKey{}, u)
}

func b64Encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func b64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func Sign(u *User, ttl time.Duration) (string, error) {
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
	sig := signHS256(signingInput, jwtSecret)
	return signingInput + "." + b64Encode(sig), nil
}

func signHS256(input, secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(input))
	return m.Sum(nil)
}

func Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("token 格式错误")
	}
	expected := signHS256(parts[0]+"."+parts[1], jwtSecret)
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

// ============ 角色等级 ============

func RoleLevel(role string) int {
	switch role {
	case RoleSuperAdmin, RoleAdmin:
		return 4
	case RoleTenantAdmin, RoleApprover:
		return 3
	case RoleDeptAdmin:
		return 2
	default:
		return 1
	}
}

func IsSuperAdmin(u *User) bool {
	return u != nil && RoleLevel(u.Role) >= 4
}

func IsTenantAdmin(u *User) bool {
	return u != nil && RoleLevel(u.Role) >= 3
}

func IsDeptAdmin(u *User) bool {
	return u != nil && RoleLevel(u.Role) >= 2
}

func RequireRole(u *User, required int) error {
	if u == nil {
		return errors.New("未登录")
	}
	if RoleLevel(u.Role) < required {
		return fmt.Errorf("权限不足：需要 %s 角色", roleName(required))
	}
	return nil
}

func roleName(level int) string {
	switch level {
	case 4:
		return "超级管理员"
	case 3:
		return "租户管理员"
	case 2:
		return "部门管理员"
	default:
		return "普通用户"
	}
}

// ============ 密码 ============

func PasswordHash(password string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "$sha256$" + legacyHash(password)
	}
	return string(h)
}

func CheckPassword(hash, password string) bool {
	switch {
	case strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2c$"):
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	case strings.HasPrefix(hash, "$sha256$"):
		return legacyHash(password) == strings.TrimPrefix(hash, "$sha256$")
	default:
		return PasswordHashLegacy(password) == hash
	}
}

func NeedMigrateHash(hash string) bool {
	return !(strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2c$"))
}

func legacyHash(password string) string {
	sum := sha256.Sum256([]byte(password))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func PasswordHashLegacy(password string) string {
	s := "trans-salt:" + password
	m := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m[:])
}

func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}