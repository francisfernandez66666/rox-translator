// Package auth 认证薄委托包：向后兼容的旧导入路径，全部实现委托给 internal/iam。
// （IAM 子系统拆分后保留本包避免历史调用方大面积改 import。）
// JWT 签名密钥唯一事实源在 iam.jwtSecret（JWT_SECRET 环境变量注入）。
// =============================================
package auth

import (
	"context"
	"net/http"
	"time"

	"translator/internal/iam"
	"translator/internal/store"
)

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

// ---- ★ 2026-10-21 IAM 重做 P0-1：账号归一化与口令强度的透传口 ----
// 判据本体只在 internal/iam/account.go 一份；这一层透传只为让 api 调用方
// 不必新增 import（本包已经是 api 的既有鉴权入口）。**禁止**在这里再造第二套判据。

// PasswordMinLen 口令最小长度（委托 iam，唯一尺子）。
const PasswordMinLen = iam.PasswordMinLen

// PasswordTooShortMsg 口令不合格的对外文案（委托 iam，四写口逐字同一句）。
const PasswordTooShortMsg = iam.PasswordTooShortMsg

// ValidatePassword 口令强度唯一判据（委托 iam）。
func ValidatePassword(p string) bool { return iam.ValidatePassword(p) }

// LooksLikeEmail 判输入是不是邮箱形态（委托 iam，只用于选查询腿）。
func LooksLikeEmail(s string) bool { return iam.LooksLikeEmail(s) }

// NormalizeAccount 账号归一化唯一入口（委托 iam）：只 trim，邮箱额外小写；口令永远不经这里。
func NormalizeAccount(s string) string { return iam.NormalizeAccount(s) }

// NormalizeEmail 邮箱归一化（委托 iam）。
func NormalizeEmail(s string) string { return iam.NormalizeEmail(s) }

// EffectiveUserStatus 计算用户生效状态（注销宽限期次日起等效停用；委托 iam）。
func EffectiveUserStatus(status, deactivatedAt string) string {
	return iam.EffectiveUserStatus(status, deactivatedAt)
}

// PasswordHashLegacy 历史 SHA-256+固定盐 哈希（仅校验存量账号用，委托 iam）。
func PasswordHashLegacy(password string) string { return iam.PasswordHashLegacy(password) }

// BearerToken 从 Authorization 头提取 Bearer 令牌（委托 iam）。
func BearerToken(r *http.Request) string { return iam.BearerToken(r) }

// ClaimsOf 提取并校验请求的 JWT 载荷（委托 iam）。
// ★ IAM P1（2026-10-21）：登出等「只要 claims 不要用户行」的 handler 统一走这一条，
// 避免每个调用点各自拼一遍 BearerToken + Verify。
func ClaimsOf(r *http.Request) (*Claims, error) { return iam.ClaimsOf(r) }
