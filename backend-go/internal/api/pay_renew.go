// ============ 本文件职责中文说明 ============
// 订阅自动续费（★ #41 商业洞二，2026-09-21）：
//   - handleAutoRenew（POST /api/package/auto-renew）：租户管理员开/关本租户自动续费；
//     GET 同路径回读当前开关态（订阅页开关初值）。
//   - maybeCreateRenewalOrder：由订阅扫描任务按「续费重试阶梯」调用（★ #74 起）——
//     到期前 T-N（N=renewal_lead_days，默认 3）起每轮扫描一次，宽限期内继续每日补建，
//     直到订阅到账或宽限期结束。同包已有 pending 单则跳过（不堆单），
//     「同一天不重复建单」由 renewal_attempts 的 (tenant_id, package_id, attempt_date)
//     唯一键在数据库层兜住（多实例/重复触发同一天也只建一张）。
//
// ★ 为什么不是「免密代扣」：微信「委托代扣 / PAP」、支付宝「周期扣款协议」都需要与渠道
//
//	单独签约并申请扣款模板，属于商户资质之外的第二道资质门槛。协议未接入前本函数只建单
//	不扣款（fail-closed：绝不调用任何未经签约的扣款接口，也不伪造「已续费」状态）。
//	签约后在此处补 `Provider.ChargeAgreedOrder`（协议号来自 #41 后续批次），
//	建单逻辑保持不变即可平滑升级为自动扣款。
//
// ★ F-64①（批 I-7）口径：本文件错误响应已统一走 s.writeError，状态码按语义诚实
//
//	（403/400/409/500），不再用 200 承载失败。
//
// ==========================================
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
	"translator/internal/tenant"
)

// autoRenewLeadDays 到期前几天自动生成续费单（与 S7 续费三封的 T-3 档同窗，避免多轮打扰）。
const autoRenewLeadDays = 3

// handleAutoRenew 自动续费开关：GET 回读、POST 置位（租户管理员及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（POST body: {"enabled":true|false}）。
// 返回: success=true 时携带 auto_renew 当前态；无订阅租户可读到 false 但开启会被拒绝。
func (s *Server) handleAutoRenew(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err) // ★ F-64①：未登录→401、等级不足→403（见 server.go writeAuthzError）
		return
	}
	tid := s.effTenant(r, u)
	if tid <= 0 {
		// ★ F-64①（批 I-7）：上下文不匹配属请求侧问题 → 400 校验错误。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "平台上下文无订阅概念"))
		return
	}
	perms, perr := s.Store.GetTenantPerms(tid)
	if perr != nil || perms == nil {
		// ★ F-64①（批 I-7）：读取失败是服务端故障 → 500，不再用 200 壳承载失败。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "订阅信息读取失败"))
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]interface{}{"success": true, "auto_renew": perms.AutoRenew, "package_code": perms.PackageCode})
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if decErr := json.NewDecoder(r.Body).Decode(&req); decErr != nil {
		// ★ F-64①（批 I-7）：body 解析失败 → 400 校验错误。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "参数格式错误"))
		return
	}
	// 开启前置校验：必须已有付费订阅（试用/无包租户自动续费无意义，且会对不存在的包建单）
	if req.Enabled && (perms.PackageCode == "" || perms.PackageCode == "trial" || perms.PackageExpires == "") {
		// ★ F-64①（批 I-7）：请求合法但与订阅当前状态冲突 → 409，不再用 200 壳承载失败。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "当前无到期订阅，暂不需要自动续费"))
		return
	}
	// 开启时确认该包仍上架：包下架后扫描任务无法建单，不如当场提示管理员换包
	if req.Enabled {
		if pkg, gerr := s.Store.GetPackageByCode(tid, perms.PackageCode); gerr != nil || pkg == nil || pkg.Enabled != 1 {
			// ★ F-64①（批 I-7）：包下架是订阅状态不允许该操作 → 409，不再用 200 壳承载失败。
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "当前订阅套餐已下架，请先选择其他套餐"))
			return
		}
	}
	if serr := s.Store.SetTenantAutoRenew(tid, req.Enabled); serr != nil {
		observability.Error(context.Background(), "自动续费开关置位失败", "tid", strconv.FormatInt(tid, 10), "err", serr.Error())
		// ★ F-64①（批 I-7）：保存失败是服务端故障 → 500，不再用 200 壳承载失败。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "设置保存失败"))
		return
	}
	s.Store.LogAudit(tid, u.ID, "auto_renew_set", "tenant", strconv.FormatBool(req.Enabled))
	writeJSON(w, 200, map[string]interface{}{"success": true, "auto_renew": req.Enabled})
}

// maybeCreateRenewalOrder 自动续费建单（订阅扫描任务调用）。
// 参数 tid=租户 ID，perms=该租户权限快照（须含 package_code），daysLeft=剩余天数（可为负=已到期）。
// 返回 true 表示本轮已生成续费单（调用方据此留痕）。
//
// 阶梯（★ #74）：daysLeft ≤ 配置的 T-N 即进入续费窗口，每轮日扫描尝试一次；
// 到期后由 watchdog 的宽限期分支继续按日调用（daysLeft 为负），故默认配置下的实际
// 节奏是 T-3 / T-2 / T-1 / T0 / T+1 / T+2 各一次（宽限期 3 天），而不是到期即停。
//
// 去重分两层（缺一不可）：
//
//	① 同包已有 pending 单 → 直接跳过（既不重复建单，也**不消耗**今日格子，
//	   以便该单当天超时关单后仍有机会补建）；
//	② renewal_attempts 同日唯一键抢占 → 保证「同一租户同一包同一天最多建一张」，
//	   多实例降级本地执行、手工触发扫描接口重复调用都吃在这一格里。
//
// 渠道不可用（资质未配 / 静态码）时订单仍建为 pending 并照常通知——续费入口本身就在收银台，
// 不因渠道差异漏提醒。
func (s *Server) maybeCreateRenewalOrder(tid int64, perms *tenant.Perms, daysLeft int) bool {
	if s.Store == nil || perms == nil || !perms.AutoRenew || perms.PackageCode == "" || perms.PackageCode == "trial" {
		return false
	}
	if daysLeft > s.renewalLeadDays() {
		return false // 未到续费窗口（阶梯首日之后每日一次）
	}
	pkg, err := s.Store.GetPackageByCode(tid, perms.PackageCode)
	if err != nil || pkg == nil || pkg.Enabled != 1 {
		// 包已下架/删除：不静默失败，通知管理员换包（否则到期即摘除身份）
		s.notifyTenantAdmins(tid, "自动续费未能生成订单",
			"当前订阅套餐「"+perms.PackageCode+"」已下架或不可用，系统未自动续费。"+
				"请在到期前前往「管理后台 → 套餐与账单」重新选择套餐。")
		return false
	}
	if has, herr := s.Store.HasPendingPackageOrder(tid, pkg.ID); herr != nil {
		observability.Error(context.Background(), "自动续费单去重查询失败",
			"tid", strconv.FormatInt(tid, 10), "pkg", strconv.FormatInt(pkg.ID, 10), "err", herr.Error())
		return false
	} else if has {
		return false // 本轮已建过（或管理员自己已下单待付）
	}
	// ★ #74 同日唯一键抢占：抢到才继续建单；建单失败也认掉这一格（下一阶梯日再试），
	//   否则同一个必败错误会被刷成一串垃圾 pending 单。
	today := renewalAttemptClock().Format("2006-01-02")
	claimed, cerr := s.Store.ClaimRenewalAttempt(tid, pkg.ID, today)
	if cerr != nil {
		observability.Error(context.Background(), "自动续费建单资格抢占失败",
			"tid", strconv.FormatInt(tid, 10), "pkg", strconv.FormatInt(pkg.ID, 10), "err", cerr.Error())
		return false // 状态层故障不建单：宁可漏一张，不可同日堆两张
	}
	if !claimed {
		observability.Info(context.Background(), "自动续费今日已建过单，跳过",
			"tid", strconv.FormatInt(tid, 10), "date", today)
		return false
	}
	channel := s.renewalChannel(tid)
	o, oerr := s.Store.CreatePackageOrder(tid, pkg, 0, channel) // createdBy=0：系统自动建单
	if oerr != nil {
		observability.Error(context.Background(), "自动续费订单创建失败",
			"tid", strconv.FormatInt(tid, 10), "code", pkg.Code, "err", oerr.Error())
		// 失败也要落台账：运维排查「为什么这个客户没收到续费单」时，这一步是唯一线索
		_ = s.Store.FinishRenewalAttempt(tid, pkg.ID, today, store.RenewalAttemptFailed, 0, "", truncReason(oerr.Error()))
		return false
	}
	_ = s.Store.FinishRenewalAttempt(tid, pkg.ID, today, store.RenewalAttemptCreated, o.ID, o.OrderNo, "")
	// ★ #75（2026-09-23）多币种报价：自动续费单同样落报价快照（金额=PackageOrderPrice 最终值）。
	// 后台链路无请求上下文，快照失败只影响展示留痕，不影响建单结果。
	s.stampOrderQuote(context.Background(), o)
	expDate := ""
	if exp, perr := time.Parse(time.RFC3339, perms.PackageExpires); perr == nil {
		expDate = exp.Format("2006-01-02")
	} else {
		expDate = perms.PackageExpires
	}
	s.notifyTenantAdmins(tid, "续费订单已自动生成",
		fmt.Sprintf("订阅「%s」%s %s 到期，系统已生成续费单 %s（应收 %.2f 元）。"+
			"请在「管理后台 → 套餐与账单 → 收银台」完成支付，到账后订阅自动顺延；订单 15 分钟未支付会自动关闭并可重新生成。%s",
			pkg.Name, renewTense(daysLeft), expDate, o.OrderNo, o.AmountMoney, graceReminderBody(s.graceEndForNotice(perms))))
	s.Store.LogAudit(tid, 0, "auto_renew_order", "orders", o.OrderNo+" pkg="+pkg.Code)
	_ = s.Store.CreateAlert(0, "warning", "auto_renew",
		"租户 #"+strconv.FormatInt(tid, 10)+" 自动续费单 "+o.OrderNo+" 待支付（套餐 "+pkg.Code+"，"+expDate+" 到期）")
	return true
}

// renewTense 到期前/后的文案区分：daysLeft<0 表示已在宽限期内（到期后补建），
// 说「已于 X 到期」比「将于 X 到期」准确，避免客户以为还有三天。
func renewTense(daysLeft int) string {
	if daysLeft < 0 {
		return "已于"
	}
	return "将于"
}

// graceEndForNotice 通知文案里的宽限期截止时刻：仅在实际处于宽限期（到期时刻已过）时给出。
// 未到期或本租户无有效宽限期时返回零值，graceReminderBody 对此输出空串（不打扰）。
func (s *Server) graceEndForNotice(perms *tenant.Perms) time.Time {
	if perms == nil || s.subscriptionGraceDays() <= 0 {
		return time.Time{}
	}
	exp, err := time.Parse(time.RFC3339, perms.PackageExpires)
	if err != nil || time.Now().Before(exp) {
		return time.Time{} // 未到期：不提前吓唬客户
	}
	if g, ok := graceDeadline(perms, exp); ok {
		return g
	}
	return exp.AddDate(0, 0, s.subscriptionGraceDays())
}

// truncReason 失败原因截断（renewal_attempts.reason 供人看，不需要堆栈全文）。
func truncReason(msg string) string {
	if len(msg) > 200 {
		return msg[:200]
	}
	return msg
}

// renewalChannel 续费单支付渠道：与收银台同一口径（运营策略 payment.mode → 渠道），
// USDT 开关开启时不自动走链上（链上需人工核销，自动建单只会堆挂单）。
func (s *Server) renewalChannel(tid int64) string {
	switch s.effPayMode(tid) {
	case "sdk":
		return "wechat"
	case "mock":
		return "mock"
	default:
		return "manual"
	}
}
