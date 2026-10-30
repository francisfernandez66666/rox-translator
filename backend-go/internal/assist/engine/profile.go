// ============ profile.go · 职责说明 ============
// 登录态访客画像的**系统提示词拼段**（★ 2026-10-30 三事合一批 组7 §8.2 P-6）。
//
// 口径：
//   - 画像由主服务在转发那一跳组装并签名，assist 验签后**只放进本轮请求的 ctx**；
//     本文件是它进入提示词的**唯一拼接点**（`visitorProfileBlock`），别处出现第二处即红。
//   - 匿名/无画像时整段不出现（不是「画像：无」——那会让模型以为访客已登录）。
//   - 只陈述事实，不写任何「你应该催他充值」之类的指令：是否推充值入口由 recharge_intent.go
//     的单一路径判定（R-4 的负向锁靠这条边界成立）。
//   - **绝不进 canned 缓存、不进会话消息、不进日志**（P-3）：本文件只读 ctx，不写任何存储；
//     欢迎词/chips 的拼装路径根本不经过 buildSystemPrompt，所以它们天然看不到画像。
//
// =============================================
package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"translator/internal/assist/profile"
)

// visitorProfileKey 画像在 ctx 里的私有键（不导出，防止别的包塞第二个来源）。
type visitorProfileKey struct{}

// WithVisitorProfile 把已验证的画像挂进本轮请求 ctx（只应由 assist/api 的 handleChat 调用）。
func WithVisitorProfile(ctx context.Context, v *profile.Visitor) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, visitorProfileKey{}, v)
}

// VisitorProfileFromCtx 取本轮画像；匿名返回 nil。
func VisitorProfileFromCtx(ctx context.Context) *profile.Visitor {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(visitorProfileKey{}).(*profile.Visitor)
	if v == nil || v.UID <= 0 {
		return nil
	}
	return v
}

// visitorProfileBlock 渲染【访客画像】整段；无画像返回空串（调用方整段不拼）。
func visitorProfileBlock(ctx context.Context) string {
	v := VisitorProfileFromCtx(ctx)
	if v == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("【访客画像（登录用户，服务端现读）】\n")
	sb.WriteString("下面是这位登录用户此刻的真实情况。用它判断他现在的处境，但**不要把这些字段逐条念出来**；\n")
	sb.WriteString("也不要向他索要密码、验证码、证件或银行卡号——这些数据平台不需要、画像里也没有。\n")
	line := func(k, val string) {
		val = strings.TrimSpace(val)
		if val == "" {
			return
		}
		sb.WriteString("- " + k + "：" + val + "\n")
	}
	line("称呼", v.DisplayName)
	line("职业", v.JobRole)
	line("界面语种偏好", v.PreferredLang)
	line("账号状态", profileStatusZh(v.Status))
	line("邮箱（已脱敏）", v.Email)
	tenant := strings.TrimSpace(v.TenantName)
	if tenant != "" {
		kind := "企业版"
		if v.IsPersonal {
			kind = "个人版"
		}
		line("所在租户", tenant+"（"+kind+"）")
	}
	line("行业", v.Industry)

	// 付费与套餐：只报事实（开/关、包名、到期），不报价格。
	pay := "当前处于免费期（未强制按积分计费）"
	if v.BillingEnforced {
		pay = "已开启按积分计费"
	}
	if strings.TrimSpace(v.PackageCode) != "" {
		pay += "；订阅套餐 " + strings.TrimSpace(v.PackageCode)
		if exp := strings.TrimSpace(v.PackageExpires); exp != "" {
			pay += "，到期 " + exp
		}
		renew := "关"
		if v.AutoRenew {
			renew = "开"
		}
		pay += "（自动续费：" + renew + "）"
		if v.InGrace {
			pay += "；已到期、处于宽限期"
			if g := strings.TrimSpace(v.GraceExpires); g != "" {
				pay += "（至 " + g + "）"
			}
		}
	} else {
		pay += "；当前没有订阅套餐"
	}
	line("付费状态", pay)

	// 积分：与 /api/me/package 同一把尺子（PointsFromTokens 折好的值）。
	line("积分余额", strconv.FormatInt(v.PointsBalance, 10)+" 分")
	line("今日已用", strconv.FormatInt(v.PointsUsedToday, 10)+" 分")
	line("本月已用", strconv.FormatInt(v.PointsUsedMonth, 10)+" 分")
	if v.HitWall {
		line("今日免费额度", "撞过免费档上限（不要报任何剩余额度数字，额度本身是隐藏的）")
	}
	if v.RechargedRecently {
		line("最近充值", "24 小时内刚完成过一笔充值")
	}
	if len(v.TaskRewards) > 0 {
		var parts []string
		for _, t := range v.TaskRewards {
			title := strings.TrimSpace(t.Title)
			if title == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s（今日 %d 次 / 本周 %d 次）", title, t.Today, t.Week))
		}
		if len(parts) > 0 {
			line("任务奖励进度", strings.Join(parts, "；"))
		}
	}
	return sb.String()
}

// profileStatusZh 账号状态英→中（提示词全中文，留英文枚举会让模型照念 "active"）。
func profileStatusZh(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return "正常"
	case "disabled":
		return "已停用"
	case "deactivating":
		return "注销中（当日仍可用）"
	case "":
		return ""
	default:
		return s
	}
}
