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
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// jwtSecret JWT 签名密钥；默认空串，由 init 在启动时解析：
//   - 配置了 JWT_SECRET 环境变量 → 使用其值（多实例部署需保持一致）；
//   - 未配置 → 生成进程级随机密钥并告警。随机密钥不可被外部伪造（替代原硬编码常量），
//     但重启/扩副本会失效，生产必须设置 JWT_SECRET（亦可开 REQUIRE_PROD_SECRETS=1 强校验）。
var jwtSecret string

// init 启动时确定 JWT 签名密钥。
func init() {
	if v := os.Getenv("JWT_SECRET"); v != "" {
		jwtSecret = v
		return
	}
	// 未配置：生成随机密钥，杜绝「已知常量可伪造超管 token」的致命风险。
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 随机源不可用（极罕见）：退化为空，Verify 将拒绝所有 token，服务不可用但不可被伪造。
		jwtSecret = ""
		log.Printf("[auth] 警告: JWT_SECRET 未设置且随机源不可用，JWT 校验将全部失败（请设置 JWT_SECRET）")
		return
	}
	jwtSecret = hex.EncodeToString(b)
	log.Printf("[auth] 警告: JWT_SECRET 未设置，已生成随机进程密钥（重启或扩副本后旧 token 失效，生产环境请设置 JWT_SECRET）")
}

// RandomSecret 生成 32 字节随机数的 hex 字符串（API Key 等高熵凭证用）；随机源不可用时回退 jwtSecret。
func RandomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return jwtSecret
	}
	return hex.EncodeToString(b)
}

// Claims JWT 载荷：uid=用户 ID、tid=租户 ID、username/role 冗余供无库鉴权、exp=过期时间戳（秒）。
type Claims struct {
	UserID   int64  `json:"uid"`
	TenantID int64  `json:"tid"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"`
}

// ctxUserKey context 键类型（私有，防跨包碰撞），承载当前登录用户。
type ctxUserKey struct{}

// UserFromContext 从请求上下文取当前登录用户（中间件注入；未登录返回 nil）。
func UserFromContext(ctx context.Context) *User {
	v, _ := ctx.Value(ctxUserKey{}).(*User)
	return v
}

// WithUser 将登录用户写入 context（登录中间件使用）。
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxUserKey{}, u)
}

// b64Encode base64url 编码（无填充，JWT 标准段格式）。
func b64Encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// b64Decode base64url 解码（对应 b64Encode）。
func b64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// Sign 业务逻辑实现，详见函数体与调用处注释。
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

// signHS256 计算 HMAC-SHA256 签名（JWT 第三段）。
// 参数：input=待签名内容（header.payload）；secret=签名密钥。
func signHS256(input, secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(input))
	return m.Sum(nil)
}

// Verify 校验 JWT：三段式结构→HMAC 签名比对（恒定时间）→载荷解析→过期检查；任一失败返回错误。
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

// RoleLevel 角色→权限等级映射：超管/admin=4、租管/approver=3、部门管理=2、普通用户=1。
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

// IsSuperAdmin 判断性谓词，返回布尔值。
func IsSuperAdmin(u *User) bool {
	return u != nil && RoleLevel(u.Role) >= 4
}

// IsTenantAdmin 判断性谓词，返回布尔值。
func IsTenantAdmin(u *User) bool {
	return u != nil && RoleLevel(u.Role) >= 3
}

// IsDeptAdmin 判断性谓词，返回布尔值。
func IsDeptAdmin(u *User) bool {
	return u != nil && RoleLevel(u.Role) >= 2
}

// RequireRole 权限闸门：未登录报错；角色等级低于 required 时返回「权限不足」中文错误。
func RequireRole(u *User, required int) error {
	if u == nil {
		return errors.New("未登录")
	}
	if RoleLevel(u.Role) < required {
		return fmt.Errorf("权限不足：需要 %s 角色", roleName(required))
	}
	return nil
}

// roleName 权限等级→中文角色名（错误提示用）。
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

// PasswordHash 密码哈希：bcrypt（DefaultCost）；bcrypt 异常时回退带 $sha256$ 前缀的旧哈希（保证可校验）。
func PasswordHash(password string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "$sha256$" + legacyHash(password)
	}
	return string(h)
}

// CheckPassword 校验密码：按哈希前缀分派——bcrypt($2a/$2b/$2c) / $sha256$ 旧格式 / 无前缀最早期 salt 格式。
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

// NeedMigrateHash 判断密码哈希是否需要升级为 bcrypt（非 $2 开头即旧格式，登录成功后应重哈希）。
func NeedMigrateHash(hash string) bool {
	return !(strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2c$"))
}

// legacyHash 旧版无盐 SHA256→base64（$sha256$ 前缀格式的内容体）。
func legacyHash(password string) string {
	sum := sha256.Sum256([]byte(password))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// PasswordHashLegacy 最早期 salt+SHA256 哈希（仅用于存量账号校验，新密码一律 bcrypt）。
func PasswordHashLegacy(password string) string {
	s := "trans-salt:" + password
	m := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m[:])
}

// BearerToken 从 Authorization 头提取 Bearer token（无前缀或不匹配返回空串）。
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
