package api

// ============ 本文件职责中文说明 ============
// 商业包公开接口与租户订阅接口：
//   - handlePlans（GET /api/plans）：公开定价页，列出上架中的商业包（免费体验/付费包/增量包）
//   - handleMyPackage（GET /api/me/package）：当前登录租户的包订阅信息与剩余句数
//   - handlePackageSubscribe（POST /api/package/subscribe）：订阅/兑换商业包（生成待支付订单）
//   - handleRegisterIndustries（GET /api/register/industries）：注册行业列表（来自 tenant 1 的行业包）
// 句数计量口径：每源句 × 每个目标语言 = 消耗句数（与 usage_ledger 逐语言计量一致）。
// 订阅流程：用户选择付费包/增量包 → 创建订单（含 package_id）→ 走支付（SDK/静态码/线下）
//   → 超管确认到账 → GrantPackageSentences 发放句数。
// =============================================

import (
	"encoding/json"
	"net/http"
	"time"

	"translator/internal/store"
)

// handlePlans 公开定价页接口（无需登录）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 plans 数组（仅上架包）与 trial_sentences（默认试用句数）。
func (s *Server) handlePlans(w http.ResponseWriter, r *http.Request) {
	pkgs, err := s.Store.ListEnabledCommercialPackages()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	trial := int64(100)
	if v, _ := s.Store.GetConfig("trial_sentences"); v != "" {
		if n, e := parseInt64(v); e == nil && n > 0 {
			trial = n
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "plans": pkgs, "trial_sentences": trial})
}

// handleMyPackage 当前租户包信息接口（登录用户）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 balance_tokens（token 余额，主单位）/balance_sentences_approx（≈句数）/
//
//	package_code/subscribed_at/package_expires/pay_mode；旧字段 sentence_balance 兼容保留。
func (s *Server) handleMyPackage(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	tid := s.effTenant(r, u)
	var pkgCode, subAt, pkgExpires string
	tokens, approx := int64(0), int64(0)
	sentenceMirror := int64(0)
	// 平台上下文（tid<=0）无计费概念：余额返回 0
	if tid > 0 {
		tokens, approx = s.balancePayload(tid)
		if perms, err := s.Store.GetTenantPerms(tid); err == nil {
			pkgCode = perms.PackageCode
			subAt = perms.SubscribedAt
			pkgExpires = perms.PackageExpires
			sentenceMirror = perms.SentenceBalance
		}
	}
	payMode := "mock"
	if v, _ := s.Store.GetConfig("pay_mode"); v != "" {
		payMode = v
	}
	// ★ 四期体验增强：无论是否强制计费，均透出实际消耗（今日/本月）与部门预算进度
	usedToday, usedMonth := int64(0), int64(0)
	if tid > 0 {
		ms := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Local).Format(time.RFC3339)
		_ = s.Store.DB().QueryRow(
			"SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=? AND created_at>=?", tid, ms).Scan(&usedMonth)
		_ = s.Store.DB().QueryRow(
			"SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=? AND user_id=? AND created_at>=?",
			tid, u.ID, ms).Scan(&usedToday)
	}
	resp := map[string]interface{}{
		"success":                  true,
		"tokens_used_today":        usedToday,
		"tokens_used_month":        usedMonth,
		"balance_tokens":           tokens,
		"balance_sentences_approx": approx,
		"sentence_balance":         sentenceMirror, // 兼容字段：历史句数镜像
		"package_code":             pkgCode,
		"subscribed_at":            subAt,
		"package_expires":          pkgExpires,
		"pay_mode":                 payMode,
	}
	// ★ 部门预算进度（四期增强；前台「🏢 部门预算 used/limit」徽标数据源）：
	// 仅当用户归属的部门启用了预算（token_limit>0）时返回 org_budget 与租户总预算
	if tid > 0 && u.OrgID > 0 {
		if sum, err := s.Store.GetOrgBudgetSummary(tid); err == nil {
			for _, d := range sum.Depts {
				if d.OrgID == u.OrgID {
					resp["org_budget"] = map[string]interface{}{
						"org_id": d.OrgID, "name": d.Name,
						"limit": d.TokenLimit, "used_this_month": d.UsedThisMonth,
					}
				}
			}
			resp["tenant_budget_total"] = sum.TotalLimit
		}
	}
	writeJSON(w, 200, resp)
}

// handlePackageSubscribe 订阅/兑换商业包（登录用户）：创建待支付订单并走支付流程。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 code=商业包编码）。
// 返回: success=true 时携带 order（含金额）与 qr_content（收款码，static_qr/mock 模式）。
func (s *Server) handlePackageSubscribe(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Code string `json:"code"` // 商业包编码（必填）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "code 不能为空"})
		return
	}
	pkg, err := s.Store.GetPackageByCode(req.Code)
	if err != nil || pkg.Enabled != 1 {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "套餐不存在或已下架"})
		return
	}
	// 免费体验包不走支付：直接发放
	if pkg.PType == store.PackageFree {
		if _, err := s.Store.GrantPackageSentences(s.effTenant(r, u), pkg); err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_free_claim", "packages", pkg.Code)
		writeJSON(w, 200, map[string]interface{}{"success": true, "message": "免费体验句数已发放"})
		return
	}
	// 付费包/增量包：创建订单（含 package_id 与售价），按支付模式走不同渠道
	tid := s.effTenant(r, u)
	// 支付模式：sdk / static_qr / mock（默认 mock）
	payMode := "mock"
	if v, _ := s.Store.GetConfig("pay_mode"); v != "" {
		payMode = v
	}
	channel := "manual"
	if payMode == "sdk" {
		channel = "wechat"
	} else if payMode == "mock" {
		channel = "mock"
	}
	o, err := s.Store.CreatePackageOrder(tid, pkg, u.ID, channel)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// mock 模式：模拟支付自动到账并发放句数（测试/演示）
	if channel == "mock" {
		if err := s.Store.MarkOrderPaid(o.ID, tid); err == nil {
			o.Status = "paid"
		}
	} else if channel == "manual" {
		// 静态码模式：回填收款码图片
		if v, _ := s.Store.GetConfig("static_qr_image"); v != "" {
			_ = s.Store.UpdateOrderPrepay(o.OrderNo, "", v)
			o.QRContent = v
		}
	}
	s.Store.LogAudit(tid, u.ID, "package_subscribe", "packages", pkg.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true, "order": o})
}

// handleRegisterIndustries 注册行业列表接口（无需登录）。
// 说明：行业来源 = 默认租户（tenant 1）的 industry 类型 KB 包；超管在默认租户下创建行业包即成为注册选项。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 industries 数组（code/name）。
func (s *Server) handleRegisterIndustries(w http.ResponseWriter, r *http.Request) {
	pkgs, err := s.Store.ListKBPackages(1)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	industries := make([]map[string]string, 0, len(pkgs))
	for _, p := range pkgs {
		// 仅 industry 类型包作为注册行业选项
		if p.PackType == store.PackIndustry {
			industries = append(industries, map[string]string{"code": p.Code, "name": p.Name})
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "industries": industries})
}
