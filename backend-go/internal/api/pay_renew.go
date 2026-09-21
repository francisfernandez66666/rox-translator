// ============ 本文件职责中文说明 ============
// 订阅自动续费（★ #41 商业洞二，2026-09-21）：
//   - handleAutoRenew（POST /api/package/auto-renew）：租户管理员开/关本租户自动续费；
//     GET 同路径回读当前开关态（订阅页开关初值）。
//   - maybeCreateRenewalOrder：到期前 T-N 天由订阅扫描任务调用，按当前订阅包自动生成续费订单
//     （去重：同包已有 pending 单则跳过），并站内信通知管理员付款。
//
// ★ 为什么不是「免密代扣」：微信「委托代扣 / PAP」、支付宝「周期扣款协议」都需要与渠道
//
//	单独签约并申请扣款模板，属于商户资质之外的第二道资质门槛。协议未接入前本函数只建单
//	不扣款（fail-closed：绝不调用任何未经签约的扣款接口，也不伪造「已续费」状态）。
//	签约后在此处补 `Provider.ChargeAgreedOrder`（协议号来自 #41 后续批次），
//	建单逻辑保持不变即可平滑升级为自动扣款。
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

	"translator/internal/observability"
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	tid := s.effTenant(r, u)
	if tid <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "平台上下文无订阅概念"})
		return
	}
	perms, perr := s.Store.GetTenantPerms(tid)
	if perr != nil || perms == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "订阅信息读取失败"})
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
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "参数格式错误"})
		return
	}
	// 开启前置校验：必须已有付费订阅（试用/无包租户自动续费无意义，且会对不存在的包建单）
	if req.Enabled && (perms.PackageCode == "" || perms.PackageCode == "trial" || perms.PackageExpires == "") {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "当前无到期订阅，暂不需要自动续费"})
		return
	}
	// 开启时确认该包仍上架：包下架后扫描任务无法建单，不如当场提示管理员换包
	if req.Enabled {
		if pkg, gerr := s.Store.GetPackageByCode(tid, perms.PackageCode); gerr != nil || pkg == nil || pkg.Enabled != 1 {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "当前订阅套餐已下架，请先选择其他套餐"})
			return
		}
	}
	if serr := s.Store.SetTenantAutoRenew(tid, req.Enabled); serr != nil {
		observability.Error(context.Background(), "自动续费开关置位失败", "tid", strconv.FormatInt(tid, 10), "err", serr.Error())
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "设置保存失败"})
		return
	}
	s.Store.LogAudit(tid, u.ID, "auto_renew_set", "tenant", strconv.FormatBool(req.Enabled))
	writeJSON(w, 200, map[string]interface{}{"success": true, "auto_renew": req.Enabled})
}

// maybeCreateRenewalOrder 自动续费建单（订阅扫描任务调用）。
// 参数 tid=租户 ID，perms=该租户权限快照（须含 package_code），daysLeft=剩余天数（可为负=已到期）。
// 返回 true 表示本轮已生成续费单（调用方据此留痕）。
//
// 去重与幂等：同包已有 pending 订单直接跳过；渠道不可用（资质未配 / 静态码）时订单仍建为
// pending 并照常通知——续费入口本身就在收银台，不因渠道差异漏提醒。
func (s *Server) maybeCreateRenewalOrder(tid int64, perms *tenant.Perms, daysLeft int) bool {
	if s.Store == nil || perms == nil || !perms.AutoRenew || perms.PackageCode == "" || perms.PackageCode == "trial" {
		return false
	}
	if daysLeft > autoRenewLeadDays {
		return false // 未到续费窗口
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
	channel := s.renewalChannel(tid)
	o, cerr := s.Store.CreatePackageOrder(tid, pkg, 0, channel) // createdBy=0：系统自动建单
	if cerr != nil {
		observability.Error(context.Background(), "自动续费订单创建失败",
			"tid", strconv.FormatInt(tid, 10), "code", pkg.Code, "err", cerr.Error())
		return false
	}
	expDate := ""
	if exp, perr := time.Parse(time.RFC3339, perms.PackageExpires); perr == nil {
		expDate = exp.Format("2006-01-02")
	} else {
		expDate = perms.PackageExpires
	}
	s.notifyTenantAdmins(tid, "续费订单已自动生成",
		fmt.Sprintf("订阅「%s」将于 %s 到期，系统已生成续费单 %s（应收 %.2f 元）。"+
			"请在「管理后台 → 套餐与账单 → 收银台」完成支付，到账后订阅自动顺延；订单 15 分钟未支付会自动关闭并可重新生成。",
			pkg.Name, expDate, o.OrderNo, o.AmountMoney))
	s.Store.LogAudit(tid, 0, "auto_renew_order", "orders", o.OrderNo+" pkg="+pkg.Code)
	_ = s.Store.CreateAlert(0, "warning", "auto_renew",
		"租户 #"+strconv.FormatInt(tid, 10)+" 自动续费单 "+o.OrderNo+" 待支付（套餐 "+pkg.Code+"，"+expDate+" 到期）")
	return true
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
