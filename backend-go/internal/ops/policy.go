// ============================================================================
// ops/policy.go — 运营策略引擎：计费/模式/套餐/时间窗/邀请/注册/限额/支付因子配置
//
// 设计见《改造方案_计费流程引擎因子配置.md》：
//   - OperationsPolicy 为可配置（可覆盖）模型：布尔用指针以支持「显式设为 false」，
//     数值以「0=未设置」表示不覆盖；顶层 PromoWindows 承载运营时间窗因子。
//   - EffectivePolicy 为解析后的最终策略：业务代码只读此模型。
//   - 解析顺序：代码内置默认 → 平台 ops_policy → 租户 ops_policy → 命中的活跃时间窗 overrides。
//
// ============================================================================
package ops

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// ============================== 可配置模型（patch） ==============================

// ModeRulePatch 模式定价因子（指针布尔=可显式设为 false）
type ModeRulePatch struct {
	Enabled    *bool    `json:"enabled,omitempty"`
	Charge     *bool    `json:"charge,omitempty"`
	Markup     *float64 `json:"markup,omitempty"` // 成本系数；<=0 沿用全局系数
	LimitChars *int64   `json:"limit_chars,omitempty"`
}

type ModeRulesPatch map[string]ModeRulePatch

// BillingPatch 计费基础因子
type BillingPatch struct {
	Enforced         *bool          `json:"enforced,omitempty"`
	MarkupMultiplier *float64       `json:"markup_multiplier,omitempty"`
	ModeRules        ModeRulesPatch `json:"mode_rules,omitempty"`
}

// PromoWindow 运营时间窗（推广期）因子
type PromoWindow struct {
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Start     string           `json:"start"` // RFC3339 或 YYYY-MM-DD
	End       string           `json:"end"`
	TZ        string           `json:"tz,omitempty"`        // IANA；缺省用平台 tz
	Priority  int              `json:"priority,omitempty"`  // 大者优先
	Overrides OperationsPolicy `json:"overrides,omitempty"` // 窗口内覆盖的因子（同构，走同一 Merge）
}

// PackagePatch 套餐因子
type PackagePatch struct {
	TrialTokens         int64 `json:"trial_tokens,omitempty"`
	TrialDays           int   `json:"trial_days,omitempty"`
	MonthlyResetEnabled *bool `json:"monthly_reset_enabled,omitempty"`
	MonthlyResetLimit   int   `json:"monthly_reset_limit,omitempty"`
}

// InvitePatch 邀请奖励因子
type InvitePatch struct {
	Enabled          *bool `json:"enabled,omitempty"`
	RewardTokens     int64 `json:"reward_tokens,omitempty"`
	RewardDays       int   `json:"reward_days,omitempty"`
	PaidRewardTokens int64 `json:"paid_reward_tokens,omitempty"`
	PaidRewardDays   int   `json:"paid_reward_days,omitempty"`
	MaxDailyRewards  int   `json:"max_daily_rewards,omitempty"`
}

// RegistrationPatch 注册因子
type RegistrationPatch struct {
	Enabled            *bool `json:"enabled,omitempty"`
	IPMinIntervalSec   int   `json:"ip_min_interval_sec,omitempty"`
	IPDailyLimit       int   `json:"ip_daily_limit,omitempty"`
	EmailVerifyEnabled *bool `json:"email_verify_enabled,omitempty"`
}

// LimitsPatch 限额因子
type LimitsPatch struct {
	MaxQPS                int   `json:"max_qps,omitempty"`
	MaxConcurrent         int   `json:"max_concurrent,omitempty"`
	DefaultMaxDailyChars  int64 `json:"default_max_daily_chars,omitempty"`
	DefaultMaxDailyTokens int64 `json:"default_max_daily_tokens,omitempty"`
}

// PaymentPatch 支付因子（平台级）
type PaymentPatch struct {
	Mode       string `json:"mode,omitempty"`
	AutoCharge *bool  `json:"auto_charge,omitempty"`
}

// ContentPatch 内容/翻译因子
type ContentPatch struct {
	CondenseEnabled *bool `json:"condense_enabled,omitempty"`
	FileMaxMB       int   `json:"file_max_mb,omitempty"`
}

// OperationsPolicy 运营策略（可覆盖模型）
type OperationsPolicy struct {
	Version      int               `json:"version,omitempty"`
	TZ           string            `json:"tz,omitempty"`
	Billing      BillingPatch      `json:"billing,omitempty"`
	PromoWindows []PromoWindow     `json:"promo_windows,omitempty"`
	Package      PackagePatch      `json:"package,omitempty"`
	Invite       InvitePatch       `json:"invite,omitempty"`
	Registration RegistrationPatch `json:"registration,omitempty"`
	Limits       LimitsPatch       `json:"limits,omitempty"`
	Payment      PaymentPatch      `json:"payment,omitempty"`
	Content      ContentPatch      `json:"content,omitempty"`
}

// ============================== 最终策略（effective） ==============================

// ModeRule 解析后的模式定价因子
type ModeRule struct {
	Enabled    bool    `json:"enabled"`
	Charge     bool    `json:"charge"`
	Markup     float64 `json:"markup"`
	LimitChars int64   `json:"limit_chars"`
}

// EffectivePolicy 解析后的运营策略（业务只读）
type EffectivePolicy struct {
	TZ               string              `json:"tz"`
	ModeRules        map[string]ModeRule `json:"mode_rules"`
	Enforced         bool                `json:"enforced"`
	MarkupMultiplier float64             `json:"markup_multiplier"`

	Package      PackageEffective      `json:"package"`
	Invite       InviteEffective       `json:"invite"`
	Registration RegistrationEffective `json:"registration"`
	Limits       LimitsEffective       `json:"limits"`
	Payment      PaymentEffective      `json:"payment"`
	Content      ContentEffective      `json:"content"`
}

// PackageEffective 解析后的套餐因子：体验 token/天数、月度用量重置开关与次数上限。
type PackageEffective struct {
	TrialTokens         int64 `json:"trial_tokens"`
	TrialDays           int   `json:"trial_days"`
	MonthlyResetEnabled bool  `json:"monthly_reset_enabled"`
	MonthlyResetLimit   int   `json:"monthly_reset_limit"`
}

// InviteEffective 解析后的邀请奖励因子：总开关、注册/付费奖励 token 与天数、日发奖上限。
type InviteEffective struct {
	Enabled          bool  `json:"enabled"`
	RewardTokens     int64 `json:"reward_tokens"`
	RewardDays       int   `json:"reward_days"`
	PaidRewardTokens int64 `json:"paid_reward_tokens"`
	PaidRewardDays   int   `json:"paid_reward_days"`
	MaxDailyRewards  int   `json:"max_daily_rewards"`
}

// RegistrationEffective 解析后的注册因子：注册开关、同 IP 间隔/日上限、邮箱验证强制。
type RegistrationEffective struct {
	Enabled            bool `json:"enabled"`
	IPMinIntervalSec   int  `json:"ip_min_interval_sec"`
	IPDailyLimit       int  `json:"ip_daily_limit"`
	EmailVerifyEnabled bool `json:"email_verify_enabled"`
}

// LimitsEffective 解析后的限额因子：全局 QPS/并发上限、新租户默认日字符/token 上限。
type LimitsEffective struct {
	MaxQPS                int   `json:"max_qps"`
	MaxConcurrent         int   `json:"max_concurrent"`
	DefaultMaxDailyChars  int64 `json:"default_max_daily_chars"`
	DefaultMaxDailyTokens int64 `json:"default_max_daily_tokens"`
}

// PaymentEffective 解析后的支付因子（平台级）：支付渠道模式与下单即到账开关。
type PaymentEffective struct {
	Mode       string `json:"mode"`
	AutoCharge bool   `json:"auto_charge"`
}

// ContentEffective 解析后的内容/翻译因子：文件翻译上限（MB）。
type ContentEffective struct {
	CondenseEnabled bool `json:"condense_enabled"`
	FileMaxMB       int  `json:"file_max_mb"`
}

// Mode 取指定翻译模式因子；未配置（或空模式）回落 pro 语义并返回 false。
// 说明：空模式视为专业模式（历史口径 "" 与 pro 等同）。
func (p EffectivePolicy) Mode(m string) (ModeRule, bool) {
	if p.ModeRules == nil {
		return ModeRule{Enabled: true, Charge: true}, false
	}
	r, ok := p.ModeRules[m]
	if !ok && m == "" {
		r, ok = p.ModeRules["pro"]
	}
	return r, ok
}

// DefaultEffective 代码内置默认策略（与既有散键行为一致，零感知兼容）
func DefaultEffective() EffectivePolicy {
	return EffectivePolicy{
		TZ:               "Asia/Shanghai",
		Enforced:         true,
		MarkupMultiplier: 1.5,
		ModeRules: map[string]ModeRule{
			"fast": {Enabled: true, Charge: true, Markup: 0, LimitChars: 0},
			"pro":  {Enabled: true, Charge: true, Markup: 0, LimitChars: 0},
		},
		Package:      PackageEffective{TrialTokens: 300000, TrialDays: 14, MonthlyResetEnabled: false, MonthlyResetLimit: 1},
		Invite:       InviteEffective{Enabled: true, RewardTokens: 300000, RewardDays: 14, PaidRewardTokens: 0, PaidRewardDays: 0, MaxDailyRewards: 50},
		Registration: RegistrationEffective{Enabled: true, IPMinIntervalSec: 60, IPDailyLimit: 3, EmailVerifyEnabled: false},
		Limits:       LimitsEffective{MaxQPS: 100, MaxConcurrent: 50, DefaultMaxDailyChars: 20000, DefaultMaxDailyTokens: 20000},
		Payment:      PaymentEffective{Mode: "mock", AutoCharge: false},
		Content:      ContentEffective{CondenseEnabled: true, FileMaxMB: 40},
	}
}

// ============================== 解析与合并 ==============================

// ParseOps 解析策略 JSON；空/非法返回零值策略（可继续 Merge）。
func ParseOps(raw string) OperationsPolicy {
	var p OperationsPolicy
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return p
	}
	_ = json.Unmarshal([]byte(raw), &p)
	return p
}

// Merge 以 base（上层解析结果）为底，用 patch 的非零/非空字段覆盖叠加。
func Merge(base EffectivePolicy, patch OperationsPolicy) EffectivePolicy {
	out := base
	if patch.TZ != "" {
		out.TZ = patch.TZ
	}
	if patch.Billing.Enforced != nil {
		out.Enforced = *patch.Billing.Enforced
	}
	if patch.Billing.MarkupMultiplier != nil && *patch.Billing.MarkupMultiplier > 0 {
		out.MarkupMultiplier = *patch.Billing.MarkupMultiplier
	}
	for k, v := range patch.Billing.ModeRules {
		r, ok := out.ModeRules[k]
		if !ok {
			r = ModeRule{Enabled: true, Charge: true}
		}
		if v.Enabled != nil {
			r.Enabled = *v.Enabled
		}
		if v.Charge != nil {
			r.Charge = *v.Charge
		}
		if v.Markup != nil && *v.Markup > 0 {
			r.Markup = *v.Markup
		}
		if v.LimitChars != nil && *v.LimitChars > 0 {
			r.LimitChars = *v.LimitChars
		}
		out.ModeRules[k] = r
	}
	if patch.Package.TrialTokens != 0 {
		out.Package.TrialTokens = patch.Package.TrialTokens
	}
	if patch.Package.TrialDays != 0 {
		out.Package.TrialDays = patch.Package.TrialDays
	}
	if patch.Package.MonthlyResetEnabled != nil {
		out.Package.MonthlyResetEnabled = *patch.Package.MonthlyResetEnabled
	}
	if patch.Package.MonthlyResetLimit != 0 {
		out.Package.MonthlyResetLimit = patch.Package.MonthlyResetLimit
	}
	if patch.Invite.Enabled != nil {
		out.Invite.Enabled = *patch.Invite.Enabled
	}
	if patch.Invite.RewardTokens != 0 {
		out.Invite.RewardTokens = patch.Invite.RewardTokens
	}
	if patch.Invite.RewardDays != 0 {
		out.Invite.RewardDays = patch.Invite.RewardDays
	}
	if patch.Invite.PaidRewardTokens != 0 {
		out.Invite.PaidRewardTokens = patch.Invite.PaidRewardTokens
	}
	if patch.Invite.PaidRewardDays != 0 {
		out.Invite.PaidRewardDays = patch.Invite.PaidRewardDays
	}
	if patch.Invite.MaxDailyRewards != 0 {
		out.Invite.MaxDailyRewards = patch.Invite.MaxDailyRewards
	}
	if patch.Registration.Enabled != nil {
		out.Registration.Enabled = *patch.Registration.Enabled
	}
	if patch.Registration.IPMinIntervalSec != 0 {
		out.Registration.IPMinIntervalSec = patch.Registration.IPMinIntervalSec
	}
	if patch.Registration.IPDailyLimit != 0 {
		out.Registration.IPDailyLimit = patch.Registration.IPDailyLimit
	}
	if patch.Registration.EmailVerifyEnabled != nil {
		out.Registration.EmailVerifyEnabled = *patch.Registration.EmailVerifyEnabled
	}
	if patch.Limits.MaxQPS != 0 {
		out.Limits.MaxQPS = patch.Limits.MaxQPS
	}
	if patch.Limits.MaxConcurrent != 0 {
		out.Limits.MaxConcurrent = patch.Limits.MaxConcurrent
	}
	if patch.Limits.DefaultMaxDailyChars != 0 {
		out.Limits.DefaultMaxDailyChars = patch.Limits.DefaultMaxDailyChars
	}
	if patch.Limits.DefaultMaxDailyTokens != 0 {
		out.Limits.DefaultMaxDailyTokens = patch.Limits.DefaultMaxDailyTokens
	}
	if patch.Payment.Mode != "" {
		out.Payment.Mode = patch.Payment.Mode
	}
	if patch.Payment.AutoCharge != nil {
		out.Payment.AutoCharge = *patch.Payment.AutoCharge
	}
	if patch.Content.CondenseEnabled != nil {
		out.Content.CondenseEnabled = *patch.Content.CondenseEnabled
	}
	if patch.Content.FileMaxMB != 0 {
		out.Content.FileMaxMB = patch.Content.FileMaxMB
	}
	return out
}

// ============================== 时间窗 ==============================

// ActiveWindows 返回 now 时刻命中的窗口，按 priority 降序（同优先级按数组序）。
func ActiveWindows(ws []PromoWindow, now time.Time, defaultTZ string) []PromoWindow {
	var hit []PromoWindow
	for _, w := range ws {
		if windowActive(w, now, defaultTZ) {
			hit = append(hit, w)
		}
	}
	sort.SliceStable(hit, func(i, j int) bool { return hit[i].Priority > hit[j].Priority })
	return hit
}

// WindowActiveAt 判断单窗口在 now 时刻是否命中（供 API 出参展示 active 状态）。
func WindowActiveAt(w PromoWindow, now time.Time, defaultTZ string) bool {
	return windowActive(w, now, defaultTZ)
}

// WindowTimesValid 校验窗口起止可解析且 start < end。
func WindowTimesValid(w PromoWindow, defaultTZ string) bool {
	loc := defaultTZ
	if w.TZ != "" {
		loc = w.TZ
	}
	start, ok1 := parseWindowTime(w.Start, loc)
	end, ok2 := parseWindowTime(w.End, loc)
	return ok1 && ok2 && start.Before(end)
}

func windowActive(w PromoWindow, now time.Time, defaultTZ string) bool {
	loc := defaultTZ
	if w.TZ != "" {
		loc = w.TZ
	}
	start, ok1 := parseWindowTime(w.Start, loc)
	end, ok2 := parseWindowTime(w.End, loc)
	if !ok1 || !ok2 {
		return false
	}
	locTZ, err := time.LoadLocation(loc)
	if err != nil {
		locTZ = time.UTC
	}
	n := now.In(locTZ)
	return !n.Before(start) && n.Before(end)
}

func parseWindowTime(s, tz string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	// RFC3339（含带时区偏移）
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	// YYYY-MM-DD：当日 00:00:00 起
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, true
	}
	// YYYY-MM-DD HH:MM
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, loc); err == nil {
		return t, true
	}
	return time.Time{}, false
}
