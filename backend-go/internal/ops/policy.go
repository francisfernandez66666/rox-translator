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
	"fmt"
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

// ModeRulesPatch 模式定价因子集：按模式名（如 fast/pro）索引的各模式覆盖项
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
// ★ C32（2026-09-12）：数值字段指针化——旧「0=未设置」语义导致运营无法
// 显式把限额/奖励清零（填 0 等于没填，改不回去）。指针 + omitempty：
// 缺省=继承上层，显式 0=真实清零。
type PackagePatch struct {
	TrialTokens         *int64 `json:"trial_tokens,omitempty"`
	TrialDays           *int   `json:"trial_days,omitempty"`
	MonthlyResetEnabled *bool  `json:"monthly_reset_enabled,omitempty"`
	MonthlyResetLimit   *int   `json:"monthly_reset_limit,omitempty"`
}

// InvitePatch 邀请奖励因子
type InvitePatch struct {
	Enabled          *bool  `json:"enabled,omitempty"`
	RewardTokens     *int64 `json:"reward_tokens,omitempty"` // ★ C32 指针化（显式 0 可清零）
	RewardDays       *int   `json:"reward_days,omitempty"`
	PaidRewardTokens *int64 `json:"paid_reward_tokens,omitempty"`
	PaidRewardDays   *int   `json:"paid_reward_days,omitempty"`
	MaxDailyRewards  *int   `json:"max_daily_rewards,omitempty"`
}

// RegistrationPatch 注册因子
type RegistrationPatch struct {
	Enabled            *bool `json:"enabled,omitempty"`
	IPMinIntervalSec   *int  `json:"ip_min_interval_sec,omitempty"` // ★ C32 指针化
	IPDailyLimit       *int  `json:"ip_daily_limit,omitempty"`
	EmailVerifyEnabled *bool `json:"email_verify_enabled,omitempty"`
}

// LimitsPatch 限额因子
type LimitsPatch struct {
	MaxQPS                *int   `json:"max_qps,omitempty"` // ★ C32 指针化
	MaxConcurrent         *int   `json:"max_concurrent,omitempty"`
	DefaultMaxDailyChars  *int64 `json:"default_max_daily_chars,omitempty"`
	DefaultMaxDailyTokens *int64 `json:"default_max_daily_tokens,omitempty"`
}

// PaymentPatch 支付因子（平台级）
type PaymentPatch struct {
	Mode       string `json:"mode,omitempty"`
	AutoCharge *bool  `json:"auto_charge,omitempty"`
}

// ContentPatch 内容/翻译因子
type ContentPatch struct {
	CondenseEnabled *bool `json:"condense_enabled,omitempty"`
	FileMaxMB       *int  `json:"file_max_mb,omitempty"` // ★ C32 指针化
}

// TaskPatch 任务中心因子：奖励发放总开关（前台任务中心具体任务项仍在其页面维护）。
type TaskPatch struct {
	Enabled *bool `json:"enabled,omitempty"`
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
	Task         TaskPatch         `json:"task,omitempty"`
}

// ============================== 保存校验（★ B6，2026-09-12） ==============================

// ValidateWindowOverrides 校验推广时间窗的覆盖因子。
// 平台专属开关（计费强制、支付方式）不允许经时间窗夹带：窗口是自动生效/失效的，
// 绕过显式开关审批流即可临时打开收款/计费通道，构成运营后门。
// 参数 w: 待校验窗口；返回首个违规定级错误（合规返回 nil）。
func ValidateWindowOverrides(w PromoWindow) error {
	if strings.TrimSpace(w.ID) == "" {
		return fmt.Errorf("时间窗 id 不能为空")
	}
	if w.Overrides.Billing.Enforced != nil {
		return fmt.Errorf("时间窗覆盖禁止设置 billing.enforced（请使用计费开关显式操作）")
	}
	if w.Overrides.Payment.Mode != "" || w.Overrides.Payment.AutoCharge != nil {
		return fmt.Errorf("时间窗覆盖禁止设置 payment.mode/auto_charge（请使用支付设置显式操作）")
	}
	if len(w.Overrides.PromoWindows) > 0 {
		return fmt.Errorf("时间窗覆盖不允许嵌套子窗口")
	}
	return nil
}

// ValidatePolicyWindows 校验策略内全部时间窗：id 非空、全局唯一、逐项通过覆盖禁项检查。
// 参数 p: 待保存策略。返回首个违规定级错误（合规返回 nil）。
func ValidatePolicyWindows(p OperationsPolicy) error {
	seen := map[string]bool{}
	for _, w := range p.PromoWindows {
		if err := ValidateWindowOverrides(w); err != nil {
			return err
		}
		if seen[w.ID] {
			return fmt.Errorf("时间窗 id 重复: %s", w.ID)
		}
		seen[w.ID] = true
	}
	return nil
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
	Task         TaskEffective         `json:"task"`
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

// TaskEffective 解析后的任务中心因子：奖励发放总开关。
type TaskEffective struct {
	Enabled bool `json:"enabled"`
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
		Task:         TaskEffective{Enabled: true},
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

// ============ ★ C6（2026-09-12）默认值单一来源访问器 ============
// 全系统兜底默认值以 DefaultEffective 内建表为唯一事实源；
// 任何「读 config 失败回退默认」的代码必须引用本组访问器，禁止再写字面量。

// DefaultTrialTokens 新租户体验 token 数默认值。
func DefaultTrialTokens() int64 { return DefaultEffective().Package.TrialTokens }

// DefaultTrialDays 体验有效期（天）默认值。
func DefaultTrialDays() int { return DefaultEffective().Package.TrialDays }

// DefaultMarkupMultiplier 成本均摊系数默认值。
func DefaultMarkupMultiplier() float64 { return DefaultEffective().MarkupMultiplier }

// DefaultTokensPerSentence 句↔token 换算率默认值（500）。
// 注：该因子暂未纳入 EffectivePolicy 结构（历史原因），常数在此收口，
// store.DefaultTokensPerSentence 与本值保持一致由单测锁定。
const DefaultTokensPerSentence int64 = 500

// DefaultInviteMaxDailyRewards 单邀请人日发放上限默认值。
func DefaultInviteMaxDailyRewards() int64 { return int64(DefaultEffective().Invite.MaxDailyRewards) }

// Merge 以 base（上层解析结果）为底，用 patch 的非零/非空字段覆盖叠加。
func Merge(base EffectivePolicy, patch OperationsPolicy) EffectivePolicy {
	out := base
	// ★ C8（2026-09-12）：结构体浅拷贝仍共享 ModeRules map——旧实现在此直接
	//   `out.ModeRules[k]=r` 会改写 base（平台策略缓存/上一级合并结果），
	//   租户补丁污染平台层、时间窗补丁污染租户层。合并前先克隆。
	if base.ModeRules != nil {
		rm := make(map[string]ModeRule, len(base.ModeRules))
		for k, v := range base.ModeRules {
			rm[k] = v
		}
		out.ModeRules = rm
	}
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
	if patch.Package.TrialTokens != nil {
		out.Package.TrialTokens = *patch.Package.TrialTokens // ★ C32：显式 0 可清零
	}
	if patch.Package.TrialDays != nil {
		out.Package.TrialDays = *patch.Package.TrialDays
	}
	if patch.Package.MonthlyResetEnabled != nil {
		out.Package.MonthlyResetEnabled = *patch.Package.MonthlyResetEnabled
	}
	if patch.Package.MonthlyResetLimit != nil {
		out.Package.MonthlyResetLimit = *patch.Package.MonthlyResetLimit
	}
	if patch.Invite.Enabled != nil {
		out.Invite.Enabled = *patch.Invite.Enabled
	}
	if patch.Invite.RewardTokens != nil {
		out.Invite.RewardTokens = *patch.Invite.RewardTokens
	}
	if patch.Invite.RewardDays != nil {
		out.Invite.RewardDays = *patch.Invite.RewardDays
	}
	if patch.Invite.PaidRewardTokens != nil {
		out.Invite.PaidRewardTokens = *patch.Invite.PaidRewardTokens
	}
	if patch.Invite.PaidRewardDays != nil {
		out.Invite.PaidRewardDays = *patch.Invite.PaidRewardDays
	}
	if patch.Invite.MaxDailyRewards != nil {
		out.Invite.MaxDailyRewards = *patch.Invite.MaxDailyRewards
	}
	if patch.Registration.Enabled != nil {
		out.Registration.Enabled = *patch.Registration.Enabled
	}
	if patch.Registration.IPMinIntervalSec != nil {
		out.Registration.IPMinIntervalSec = *patch.Registration.IPMinIntervalSec
	}
	if patch.Registration.IPDailyLimit != nil {
		out.Registration.IPDailyLimit = *patch.Registration.IPDailyLimit
	}
	if patch.Registration.EmailVerifyEnabled != nil {
		out.Registration.EmailVerifyEnabled = *patch.Registration.EmailVerifyEnabled
	}
	if patch.Limits.MaxQPS != nil {
		out.Limits.MaxQPS = *patch.Limits.MaxQPS
	}
	if patch.Limits.MaxConcurrent != nil {
		out.Limits.MaxConcurrent = *patch.Limits.MaxConcurrent
	}
	if patch.Limits.DefaultMaxDailyChars != nil {
		out.Limits.DefaultMaxDailyChars = *patch.Limits.DefaultMaxDailyChars
	}
	if patch.Limits.DefaultMaxDailyTokens != nil {
		out.Limits.DefaultMaxDailyTokens = *patch.Limits.DefaultMaxDailyTokens
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
	if patch.Content.FileMaxMB != nil {
		out.Content.FileMaxMB = *patch.Content.FileMaxMB
	}
	if patch.Task.Enabled != nil {
		out.Task.Enabled = *patch.Task.Enabled
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

// windowActive 判断 now 是否落在促销窗口 [start,end) 内（时区取窗口自带 TZ，空则 defaultTZ；解析失败视为未激活）。
// 参数：w=窗口定义；now=判定时刻；defaultTZ=全局默认时区名。
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

// parseWindowTime 按指定时区解析窗口时间字符串（RFC3339 或 "2006-01-02 15:04" 形态）。
// 参数：s=时间文本；tz=时区名（非法回退 UTC）；返回时刻与是否解析成功。
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
