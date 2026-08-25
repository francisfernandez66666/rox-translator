// ============ 本文件职责中文说明 ============
// 认证薄委托包：向后兼容的旧导入路径，全部实现委托给 internal/iam。
// （IAM 子系统拆分后保留本包避免历史调用方大面积改 import。）
// =============================================
package auth

import (
	"context"
	"net/http"
	"time"

	"translator/internal/iam"
	"translator/internal/store"
)

// Secret JWT 签名密钥（部署侧可用 JWT_SECRET 环境变量覆盖；与 iam 包共享同一来源）。
var Secret = "trans-platform-jwt-secret-2026"

// Claims JWT 载荷结构（别名 iam.Claims）。
type Claims = iam.Claims

// UserFromContext 从 context 取当前登录用户（委托 iam）。
func UserFromContext(ctx context.Context) *store.User {
	u := iam.UserFromContext(ctx)
	return (*store.User)(u)
}

// WithUser 将当前用户注入 context（鉴权中间件使用；委托 iam）。
func WithUser(ctx context.Context, u *store.User) context.Context {
	return iam.WithUser(ctx, (*iam.User)(u))
}

// RandomSecret 生成随机密钥（委托 iam）。
func RandomSecret() string { return iam.RandomSecret() }

// Sign 签发 JWT（委托 iam）。参数 u=用户，ttl=有效期。
func Sign(u *store.User, ttl time.Duration) (string, error) { return iam.Sign((*iam.User)(u), ttl) }

// Verify 校验 JWT 并返回 Claims（委托 iam）。
func Verify(token string) (*Claims, error) { return iam.Verify(token) }

// RoleLevel 角色等级换算（1~4，委托 iam）。
func RoleLevel(role string) int { return iam.RoleLevel(role) }

// IsSuperAdmin 是否超级管理员（等级≥4，委托 iam）。
func IsSuperAdmin(u *store.User) bool { return iam.IsSuperAdmin((*iam.User)(u)) }

// IsTenantAdmin 是否租户管理员及以上（等级≥3，委托 iam）。
func IsTenantAdmin(u *store.User) bool { return iam.IsTenantAdmin((*iam.User)(u)) }

// IsDeptAdmin 是否部门管理员及以上（等级≥2，委托 iam）。
func IsDeptAdmin(u *store.User) bool { return iam.IsDeptAdmin((*iam.User)(u)) }

// RequireRole 断言角色等级达标，不足返回错误（委托 iam）。
func RequireRole(u *store.User, required int) error { return iam.RequireRole((*iam.User)(u), required) }

// PasswordHash 生成 bcrypt 密码哈希（兼容历史格式，委托 iam）。
func PasswordHash(password string) string { return iam.PasswordHash(password) }

// CheckPassword 校验密码与哈希匹配（支持 bcrypt/legacy，委托 iam）。
func CheckPassword(hash, password string) bool { return iam.CheckPassword(hash, password) }

// NeedMigrateHash 判断哈希是否需透明升级为 bcrypt（委托 iam）。
func NeedMigrateHash(hash string) bool { return iam.NeedMigrateHash(hash) }

// PasswordHashLegacy 历史 SHA-256+固定盐 哈希（仅校验存量账号用，委托 iam）。
func PasswordHashLegacy(password string) string { return iam.PasswordHashLegacy(password) }

// BearerToken 从 Authorization 头提取 Bearer 令牌（委托 iam）。
func BearerToken(r *http.Request) string { return iam.BearerToken(r) }
