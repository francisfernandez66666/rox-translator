// ============ 本文件职责中文说明 ============
// 订阅续费宽限期（★ #74，2026-09-23）的调度与触达层。
//
// 补的是 #41 自动续费最后一段缺口：旧行为是「到期即刻摘掉付费身份」，客户晚付一天就得
// 重新选包、老客直接流失。现在开启自动续费的租户到期后先进入 N 天宽限期：
//
//	① 进入宽限期（到期时刻）：订阅身份与额度**原样保留**（package_code / package_expires_at
//	   一行不动，故鉴权与计费链路无需任何放行分支），落 grace_expires_at 并发一条站内通知；
//	② 宽限期内：每日扫描继续按续费重试阶梯补建续费单（见 pay_renew.go 的
//	   maybeCreateRenewalOrder + store/renewal_grace.go 的同日唯一键抢占）；
//	③ 宽限期结束仍未到账：走原有 ExpirePackage 摘除路径，并改发「宽限期结束」文案。
//
// 配置优先序（AGENTS.md §3：环境变量 > 数据库配置 > 代码默认）：
//   - 宽限天数：env SUBSCRIPTION_GRACE_DAYS > system_config.subscription_grace_days > 3
//     （配 0 = 关闭宽限期，行为回退到「到期即刻摘除」）
//   - 续费提前量（首个阶梯日 T-N）：env RENEWAL_LEAD_DAYS > system_config.renewal_lead_days > 3
//
// 只有开了自动续费的租户进宽限期：没续费意愿的客户延长收费窗口没有意义，
// 且会让「到期就该停服」的运营口径变得不可解释。
//
// 多实例安全：本文件的逻辑全部跑在 watchdog 的 runSubscriptionScan 内，
// 该任务整体已被 s.runExclusive("subscription-scan", …) 单跑者化（Redis 启用时跨实例抢占，
// 抖动时降级本地执行）；同日建单去重另有 renewal_attempts 唯一键在数据库层兜底，
// 两层口径与 multi_instance_e2e.sh 的红线一致。
//
// 上下文口径：宽限期状态与通知都是「必须落库」的后台写路径，一律用 context.Background()
// （沿用 watchdog.go 既有样板），绝不接请求侧可取消的 ctx——请求超时不该让续费状态半路停摆。
// ==========================================
package api

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"translator/internal/observability"
	"translator/internal/tenant"
)

// 宽限期与续费阶梯的默认天数（可被 system_config / 环境变量覆盖，见文件头优先序）。
// 阶梯首个提前量沿用 #41 的 T-3 常量（pay_renew.go 的 autoRenewLeadDays），
// 与 S7 续费三封的 T-3 档同窗，避免同一窗口内两套不同步的提醒口径。
const defaultSubscriptionGraceDays = 3

// renewalAttemptClock 今日格子的日期口径（2006-01-02，本地时区）。
// 声明成变量而非直接调 time.Now：单测要构造「同一阶梯日两次扫描」「跨日后重试」两种时序，
// 真时钟无法稳定复现，见 internal/api/renewal_grace_test.go。
var renewalAttemptClock = func() time.Time { return time.Now() }

// subscriptionGraceDays 宽限天数：环境变量 SUBSCRIPTION_GRACE_DAYS > 库配置 subscription_grace_days > 3。
// 返回 0 表示关闭宽限期（到期即刻摘除）；负数按 0 处理，非法值一律回落默认，
// 绝不让一个写错的配置值把「收费窗口」变成无限延长。
func (s *Server) subscriptionGraceDays() int {
	if v := strings.TrimSpace(os.Getenv("SUBSCRIPTION_GRACE_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return clampGraceDays(n, defaultSubscriptionGraceDays)
		}
	}
	if s.Store != nil {
		if v, _ := s.Store.GetConfig("subscription_grace_days"); strings.TrimSpace(v) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return clampGraceDays(n, defaultSubscriptionGraceDays)
			}
		}
	}
	return defaultSubscriptionGraceDays
}

// renewalLeadDays 续费重试阶梯的首个提前天数（T-N 起，每日一次直到宽限期结束）。
// 环境变量 RENEWAL_LEAD_DAYS > 库配置 renewal_lead_days > autoRenewLeadDays（#41 原 T-3 口径）。
func (s *Server) renewalLeadDays() int {
	if v := strings.TrimSpace(os.Getenv("RENEWAL_LEAD_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if s.Store != nil {
		if v, _ := s.Store.GetConfig("renewal_lead_days"); strings.TrimSpace(v) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				return n
			}
		}
	}
	return autoRenewLeadDays
}

// clampGraceDays 配置值收敛：合法区间 [0, 90]，非法（解析失败/超界）回落默认。
// 上限 90 天是防呆——有人把宽限期写成 365 就等于免费送一年。
func clampGraceDays(n, def int) int {
	if n < 0 || n > 90 {
		return def
	}
	return n
}

// graceDeadline 解析并校验本期宽限期截止时刻。
// 参数：perms=权限快照，exp=本期订阅到期时刻。
// 返回 (graceEnd, valid)：valid=false 表示尚无有效宽限期（需按配置重新起算）。
//
// WHY 要拿 graceEnd 与 exp 比：续费到账走 billing.MarkOrderPaid（store 冻结文件，不改），
// 它只改写订阅两键、不会来清宽限期键，故库里可能留着上一期的旧值。
// 以「晚于本期到期时刻」为准，旧值天然失效并重新起算，无需在扣款链路上加清理逻辑。
func graceDeadline(perms *tenant.Perms, exp time.Time) (time.Time, bool) {
	if perms == nil {
		return time.Time{}, false
	}
	v := strings.TrimSpace(perms.GraceExpiresAt)
	if v == "" {
		return time.Time{}, false
	}
	g, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	if !g.After(exp) {
		return time.Time{}, false // 上一期遗留 / 无法解析：按未进入宽限期处理
	}
	return g, true
}

// handleExpiredSubscription 已到期订阅的宽限期裁决。
// 参数：tid=租户 ID，perms=权限快照，exp=到期时刻，now=本轮扫描时刻。
// 返回 true=本轮保留付费身份（宽限期内，调用方不得摘除）；false=按原逻辑摘除。
//
// 三个触达节点中的前两个在本函数发出：进入宽限期、宽限期结束摘除；
// 第三个（续费单待支付）由 pay_renew.go 的建单逻辑发出。
func (s *Server) handleExpiredSubscription(tid int64, perms *tenant.Perms, exp, now time.Time) bool {
	if s.Store == nil || perms == nil {
		return false
	}
	// 关闭自动续费的租户不进宽限期：无续费意愿还延长收费窗口没有业务依据
	if !perms.AutoRenew {
		return false
	}
	graceDays := s.subscriptionGraceDays()
	if graceDays <= 0 {
		return false // 配置为 0：显式关闭宽限期，回到「到期即刻摘除」的旧行为
	}
	graceEnd, valid := graceDeadline(perms, exp)
	if !valid {
		graceEnd = exp.AddDate(0, 0, graceDays)
		// 先落宽限期截止时刻、再发通知：落库失败下轮重试（幂等），反序则崩溃后会重复轰炸
		if err := s.Store.SetSubscriptionGrace(tid, graceEnd); err != nil {
			observability.Error(context.Background(), "宽限期截止时刻落库失败",
				"tid", strconv.FormatInt(tid, 10), "err", err.Error())
			return false // 状态没落住就不保留身份，避免「无凭证的长期免费」——宁可少给不可漏记
		}
	}
	if !graceEnd.After(now) {
		return false // 宽限期已过：调用方摘除身份
	}
	// 进入宽限期通知（每轮到期只发一次；标记位随 ExpirePackage / 新一期订阅清零）
	if !perms.NotifiedGrace {
		if err := s.Store.MarkGraceNotified(tid); err != nil {
			observability.Error(context.Background(), "宽限期通知去重标记置位失败",
				"tid", strconv.FormatInt(tid, 10), "err", err.Error())
		} else {
			s.notifyTenantAdmins(tid, "订阅已到期，宽限期至 "+graceEnd.Format("2006-01-02"),
				"您的订阅「"+perms.PackageCode+"」已于 "+exp.Format("2006-01-02")+" 到期。"+
					"因您开启了自动续费，系统为您提供 "+strconv.Itoa(graceDays)+" 天宽限期：宽限期至 "+
					graceEnd.Format("2006-01-02")+"，期间订阅身份与剩余额度均正常使用。"+
					"续费单会自动生成，请在「管理后台 → 套餐与账单 → 收银台」完成支付即可无缝续期；"+
					"宽限期结束仍未到账的，订阅身份将自动移除（已发放额度不受影响）。")
			s.notifyBots("订阅进入宽限期",
				"租户 #"+strconv.FormatInt(tid, 10)+" 订阅到期进入宽限期（至 "+graceEnd.Format("2006-01-02")+
					"，包 "+perms.PackageCode+"），身份暂予保留，待续费到账。")
		}
	}
	return true
}

// subscriptionGraceState 订阅当前是否处于宽限期（/api/me/package 出参判定）。
// 返回 (inGrace, graceExpires)：不在宽限期时 graceExpires 为空串，前端零判断。
//
// 与扫描侧同一口径（graceDeadline：宽限期截止时刻必须晚于本期到期时刻才有效），
// 两处共用一个函数，避免「界面说在宽限期、后台却已摘身份」的口径分裂。
func subscriptionGraceState(perms *tenant.Perms) (bool, string) {
	if perms == nil || perms.PackageCode == "" || perms.PackageCode == "trial" ||
		perms.PackageExpires == "" || perms.GraceExpiresAt == "" {
		return false, ""
	}
	exp, err := time.Parse(time.RFC3339, perms.PackageExpires)
	if err != nil {
		return false, ""
	}
	g, ok := graceDeadline(perms, exp)
	if !ok {
		return false, ""
	}
	now := time.Now()
	if now.Before(exp) || !now.Before(g) {
		return false, "" // 未到期 / 宽限期已过（下一轮扫描会摘身份）
	}
	return true, g.Format(time.RFC3339)
}

// graceReminderBody 宽限期内的补充提示（续费单通知尾部）；零值时刻（未到期/未开宽限期）返回空串。
func graceReminderBody(graceEnd time.Time) string {
	if graceEnd.IsZero() {
		return ""
	}
	return "宽限期至 " + graceEnd.Format("2006-01-02") + "，结束仍未到账将自动移除订阅身份。"
}
