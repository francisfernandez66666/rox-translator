package auth

import (
	"context"
	"net/http"
	"time"

	"translator/internal/iam"
	"translator/internal/store"
)

var Secret = "trans-platform-jwt-secret-2026"

type Claims = iam.Claims

func UserFromContext(ctx context.Context) *store.User {
	u := iam.UserFromContext(ctx)
	return (*store.User)(u)
}

func WithUser(ctx context.Context, u *store.User) context.Context {
	return iam.WithUser(ctx, (*iam.User)(u))
}

func RandomSecret() string { return iam.RandomSecret() }
func Sign(u *store.User, ttl time.Duration) (string, error) { return iam.Sign((*iam.User)(u), ttl) }
func Verify(token string) (*Claims, error) { return iam.Verify(token) }
func RoleLevel(role string) int { return iam.RoleLevel(role) }
func IsSuperAdmin(u *store.User) bool { return iam.IsSuperAdmin((*iam.User)(u)) }
func IsTenantAdmin(u *store.User) bool { return iam.IsTenantAdmin((*iam.User)(u)) }
func IsDeptAdmin(u *store.User) bool { return iam.IsDeptAdmin((*iam.User)(u)) }
func RequireRole(u *store.User, required int) error { return iam.RequireRole((*iam.User)(u), required) }
func PasswordHash(password string) string { return iam.PasswordHash(password) }
func CheckPassword(hash, password string) bool { return iam.CheckPassword(hash, password) }
func NeedMigrateHash(hash string) bool { return iam.NeedMigrateHash(hash) }
func PasswordHashLegacy(password string) string { return iam.PasswordHashLegacy(password) }
func BearerToken(r *http.Request) string { return iam.BearerToken(r) }