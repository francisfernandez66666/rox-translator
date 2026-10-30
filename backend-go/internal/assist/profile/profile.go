// ============ profile.go · 职责说明 ============
// 登录态访客画像的**唯一数据结构 + 签名/验签实现**（★ 2026-10-30 三事合一批 组7 §8.2，路 A′）。
//
// 为什么单独成包（而不是在 api / assist 两侧各写一份）：
//
//	主服务负责「组装 + 签名」，assist 负责「验签 + 拼段」。两侧对 JSON 字段名、签名原文
//	（base64(JSON)+"."+ts）、时间窗三个口径必须逐字一致——各写一份的那一天，症状不是报错，
//	而是「画像静默不生效」：验签那边永远算不出同一个 HMAC，只能回落匿名。
//	所以三件事只有一处：字段表、Canonical 拼法、Verify 判据。
//
// 口径（与 即时翻译免费_需求理解与修改面_20261030.md §6.4 P-1…P-8 同一份）：
//   - 只放**客户面已经在用**的读数（余额/已用/套餐），本包不做任何计算，只承载结构；
//   - 邮箱在组装侧已经脱敏（MaskEmail），本包不接触明文；
//   - 画像只进 system 前缀，**不进 canned 缓存、不进会话消息、不进日志**（P-3）——
//     本包因此也没有任何打印 Visitor 的辅助函数（防下一位顺手 log 一整行）。
//
// =============================================
package profile

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 三个内部头。P-7：主服务对入站同名头**先删再自赋**，Caddy 也会在残留直转路径上
// header_up 删除它们；assist 只认「回环对端 + 正确签名」，缺任一头按匿名处理。
const (
	HeaderProfile = "X-Assist-Profile"
	HeaderTs      = "X-Assist-Profile-Ts"
	HeaderSig     = "X-Assist-Profile-Sig"
)

// MaxClockSkew 签名时间窗（±60s）。取值理由：主服务与 assist 同机部署、共用系统时钟，
// 60 秒足够覆盖一次慢请求，同时把「截获一个旧头无限重放」的窗口压到分钟级。
const MaxClockSkew = 60 * time.Second

// TaskReward 任务奖励进度（只出「今天/本周领了几次」这类可核对的事实，不含奖励 token 数）。
type TaskReward struct {
	Title string `json:"title"`
	Today int64  `json:"today"`
	Week  int64  `json:"week"`
	Total int64  `json:"total"`
}

// Visitor 登录态访客画像。字段名即跨进程契约（P-1 表），改名前先核两侧消费方。
type Visitor struct {
	UID           int64  `json:"uid"`
	AccountID     string `json:"account_id,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	JobRole       string `json:"job_role,omitempty"`
	PreferredLang string `json:"preferred_lang,omitempty"`
	Status        string `json:"status,omitempty"`
	// Email 已是脱敏形态（z***@yy.com）；明文只在运营显式打开 assist_profile_full_email 时才出现。
	Email string `json:"email,omitempty"`

	TenantName string `json:"tenant_name,omitempty"`
	Industry   string `json:"industry,omitempty"` // 行业包的**展示名**，不是 code（auto 这类不能念给客户）
	IsPersonal bool   `json:"is_personal"`

	BillingEnforced bool   `json:"billing_enforced"`
	PackageCode     string `json:"package_code,omitempty"`
	PackageExpires  string `json:"package_expires,omitempty"`
	AutoRenew       bool   `json:"auto_renew"`
	InGrace         bool   `json:"in_grace"`
	GraceExpires    string `json:"grace_expires,omitempty"`

	// 积分口径（与 /api/me/package 同源，PointsFromTokens 已折算，禁止在此另立公式）。
	PointsBalance   int64 `json:"points_balance"`
	PointsUsedToday int64 `json:"points_used_today"`
	PointsUsedMonth int64 `json:"points_used_month"`

	// HitWall 只出布尔：今天撞过免费额度墙（rate_limits scope=free_wall）。
	// 不给任何额度数字——暗额度九键不进提示词是 I-3 的硬口径。
	HitWall bool `json:"hit_wall"`

	// RechargedRecently 24 小时内有过一笔已支付订单（充值意图抑制第一档的输入）。
	RechargedRecently bool `json:"recharged_recently"`

	TaskRewards []TaskReward `json:"task_rewards,omitempty"`
}

// Canonical 签名原文：base64(JSON) + "." + Unix 秒。
// 单独成函数而不是两处各拼一次：分隔符或时间单位任一侧写错，验签只会恒失败（静默回落匿名）。
func Canonical(payloadB64 string, ts int64) string {
	return payloadB64 + "." + strconv.FormatInt(ts, 10)
}

// Sign 用共享密钥对画像负载签名（HMAC-SHA256，十六进制小写）。
func Sign(key []byte, payloadB64 string, ts int64) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(Canonical(payloadB64, ts)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Encode 把 Visitor 序列化成 base64(JSON)（与 Sign 的入参同一形态）。
func Encode(v *Visitor) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

var (
	// ErrNoKey 密钥未配置（两侧都没配时画像不启用，不是错误路径）。
	ErrNoKey = errors.New("profile key not configured")
	// ErrBadTimestamp 时间头缺失/不可解析/超出窗口。
	ErrBadTimestamp = errors.New("profile timestamp invalid")
	// ErrBadSignature 签名不等值（含负载被篡改）。
	ErrBadSignature = errors.New("profile signature mismatch")
	// ErrBadPayload 负载不是合法 base64/JSON，或 uid 无效。
	ErrBadPayload = errors.New("profile payload invalid")
)

// Verify 验签并解出 Visitor；任一档不过都返回 error，调用方据此回落匿名（P-5：绝不返 4xx）。
func Verify(key []byte, payloadB64, tsStr, sig string, now time.Time) (*Visitor, error) {
	if len(key) == 0 {
		return nil, ErrNoKey
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
	if err != nil {
		return nil, ErrBadTimestamp
	}
	skew := now.Unix() - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(MaxClockSkew/time.Second) {
		return nil, ErrBadTimestamp
	}
	want := Sign(key, payloadB64, ts)
	// hmac.Equal 常量时间比较：这里比的是公开可猜的签名，但同一实现也服务其它凭据场景，
	// 保持「不写 == 比签名」的仓内一贯口径。
	if !hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(sig))), []byte(want)) {
		return nil, ErrBadSignature
	}
	raw, err := base64.StdEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, ErrBadPayload
	}
	var v Visitor
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, ErrBadPayload
	}
	if v.UID <= 0 {
		return nil, ErrBadPayload
	}
	return &v, nil
}

// MaskEmail 邮箱脱敏：本地部分只留首字符 + "***"，域名原样（z***@yy.com）。
// 非法/空值回空串——宁可画像里没有邮箱，也不把半截脏值送进上游模型。
func MaskEmail(email string) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	local, domain := email[:at], email[at+1:]
	r := []rune(local)
	if len(r) == 0 {
		return ""
	}
	return string(r[0]) + "***@" + domain
}
