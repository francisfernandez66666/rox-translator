// ============ admin_packages.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 商业包管理接口（super_admin）：付费包 / 增量包 / 免费体验包的 CRUD 与启停。
//   - handleAdminPackages（GET /api/admin/packages）：列出全部商业包（含下架）
//   - handleAdminPackageCreate（POST /api/admin/packages/create）：创建商业包
//   - handleAdminPackageUpdate（POST /api/admin/packages/update）：更新商业包（含启停/调价/改句数）
//   - handleAdminPackageDelete（POST /api/admin/packages/delete）：删除商业包
// 安全要点：全部 requireAdminUser（super_admin）；写操作记录审计日志。
// =============================================

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"translator/internal/ops"
	"translator/internal/payment"
	"translator/internal/secret"

	qrcode "github.com/skip2/go-qrcode"

	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
)

// ============ 群机器人 Webhook 掩码（★ 批 I-6 2026-09-26，本轮核实新发现）============
// webhookMaskKeys 必须掩码回显的键清单（GET /api/admin/packages/settings）。
// 这四条存的是群机器人 Incoming Webhook 完整地址，URL 里就带着机器人凭证
// （企微/钉钉的 ?key=、Slack 的 /services/T…/B…/token、Teams 的 ? webhook 路径），
// 拿到即可向企业群任意发消息——等价于密钥，不是「配置项可见」的范畴。
// 对照面（同仓别处都已掩码，说明这是本 handler 漏做而非全站口径）：
//
//	SMTP 密码（auth.go）、LLM key（admin.go maskKey）、支付渠道密钥（billing_payconfig.go
//	IsSecretMasked 回填）、assist token（admin_assist.go）。
var webhookMaskKeys = map[string]bool{
	"wecom_webhook_url":    true,
	"dingtalk_webhook_url": true,
	"slack_webhook_url":    true,
	"teams_webhook_url":    true,
}

// maskWebhookValue 读侧掩码：空值原样回空（前端据此判断「未配置」，若把空串也打码成 ****
// 就会让未配置的通道看起来已配置，且下一次保存被当成「用户没改」而永远写不进去）。
// 非空一律 maskKey（首4＋****＋尾4）。
func maskWebhookValue(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return maskKey(v)
}

// webhookSaveSkipped 写侧守卫：收到含 **** 的回显值＝前端把掩码串原样 POST 回来（用户只是打开面板
// 点了保存，并没改这一项），此时**绝不能**把 "abcd****wxyz" 写回库覆盖真地址——否则机器人当场失效。
// 范式照抄 billing_payconfig.go 的「收到掩码就跳过」。空串不在射程内（空串=显式清除配置，照常落库）。
func webhookSaveSkipped(key, val string) bool {
	return webhookMaskKeys[key] && val != "" && secret.IsSecretMasked(val)
}

// qrImageWhitelist 套餐中心静态收款码图片支持的扩展名白名单。
var qrImageWhitelist = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
}

// qrImageUploadMax 收款码图片上传大小上限（5MB，收款码 PNG/JPEG 通常远小于此）。
const qrImageUploadMax = 5 << 20

// handleAdminPackages 列出全部商业包（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 packages 数组（含下架包）。
func (s *Server) handleAdminPackages(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	pkgs, err := s.Store.ListCommercialPackages()
	if err != nil {
		// F-64②：包清单读的是本进程存储层，失败＝服务端出错（500）；
		// 旧写法回 200 会让超管面板把「查库失败」当成「一个商业包都没配」，据此误建/误删包。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "packages": pkgs})
}

// handleAdminPackageCreate 创建商业包（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 code/name/ptype/sentences/price_money/duration_days）。
// 返回: success=true 时携带新包对象。
func (s *Server) handleAdminPackageCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 解析 body 并做入参校验：code/name 必填、ptype 白名单（缺省 paid）、sentences/points 至少一项为正
	var req struct {
		TenantID     int64   `json:"tenant_id"`     // 租户 ID（可选，默认 0=平台）
		Code         string  `json:"code"`          // 包编码（唯一，必填）
		Name         string  `json:"name"`          // 包名称（必填）
		PType        string  `json:"ptype"`         // 包类型：free/paid/increment（默认 paid）
		Sentences    int64   `json:"sentences"`     // 包内含翻译句数（历史字段；与 points 二选一必填）
		Points       int64   `json:"points"`        // ★ S1 积分面值（对外售卖单位；>0 时按积分口径发放）
		PriceMoney   float64 `json:"price_money"`   // 售价（元）
		DurationDays int     `json:"duration_days"` // 有效期（天，默认 30）
		SortOrder    int     `json:"sort_order"`    // 展示排序
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" || req.Name == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "code/name 不能为空"})
		return
	}
	// ptype 缺省补 paid，随后白名单校验（仅 free/paid/increment）
	if req.PType == "" {
		req.PType = store.PackagePaid
	}
	if req.PType != store.PackageFree && req.PType != store.PackagePaid && req.PType != store.PackageIncrement {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "ptype 仅支持 free/paid/increment"})
		return
	}
	// sentences/points 双口径：至少一项为正（★S1 起新包主推 points 积分面值）
	if req.Sentences <= 0 && req.Points <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "sentences 与 points 至少一项大于 0（积分包填 points）"})
		return
	}
	// ★ C14（2026-09-12）：负价拒绝（旧实现直落库，退款/统计口径被打穿）
	if req.PriceMoney < 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "price_money 不能为负"})
		return
	}
	if req.DurationDays <= 0 {
		req.DurationDays = 30 // 缺省 30 天（显式不限期请传 -1，见下）
	}
	if req.DurationDays == -1 {
		req.DurationDays = 0 // ★ C14：-1 = 显式「不限期」（区别于缺省 0→30）
	}
	p, err := s.Store.CreatePackage(&store.Package{
		TenantID: req.TenantID, Code: req.Code, Name: req.Name, PType: req.PType, Sentences: req.Sentences,
		Points: req.Points, PriceMoney: req.PriceMoney, DurationDays: req.DurationDays, Enabled: 1, SortOrder: req.SortOrder,
	})
	if err != nil {
		// ★ 脱敏（2026-09-12）：驱动错误不透吐
		if store.IsUniqueViolation(err) {
			// F-64②：(tenant_id, code) 复合唯一命中＝包编码重复，改名/改租户后重发即可 ⇒ 409 状态冲突
			//（旧写法回 200，接入方与重试器一律当成功）。
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "创建失败：套餐编码已存在"))
			return
		}
		log.Printf("[packages] 创建套餐失败 code=%s: %v", req.Code, err)
		// F-64②：非唯一冲突的插入失败＝数据库写入故障（500）；DebriefDBError 的脱敏文案原样透出。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "创建失败: "+store.DebriefDBError(err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_create", "packages", req.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true, "package": p})
}

// handleAdminPackageUpdate 更新商业包（super_admin）：支持改名/调价/改句数/启停。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id 及可选字段）。
// 返回: success=true 表示更新成功。
func (s *Server) handleAdminPackageUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID           int64    `json:"id"`            // 目标包 ID（必填）
		TenantID     *int64   `json:"tenant_id"`     // 新租户 ID（nil=不修改）
		Name         string   `json:"name"`          // 新名称（可为空=不修改）
		PType        string   `json:"ptype"`         // 新类型（可为空=不修改）
		Sentences    int64    `json:"sentences"`     // 新句数（<=0=不修改）
		Points       *int64   `json:"points"`        // ★ S1 新积分面值（nil=不修改，0=清零回退句数口径）
		PriceMoney   *float64 `json:"price_money"`   // ★ C14：指针——nil=不修改，0=0 元价
		DurationDays *int     `json:"duration_days"` // ★ C14：指针——nil=不修改，0=改回不限期
		Enabled      *int     `json:"enabled"`       // 启停（0/1，nil=不修改）
		SortOrder    *int     `json:"sort_order"`    // 排序（nil=不修改）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 读当前包全量字段做基底，逐字段增量覆盖（指针字段 nil=不修改），最后整行写回
	cur, err := s.Store.GetPackage(req.ID)
	if err != nil {
		// F-64②：先按 id 回读整行做增量基底，读不到＝该包不存在（文案「包不存在」原样保留）⇒ 404。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "包不存在"))
		return
	}
	// 增量覆盖：仅修改显式传入的字段；ptype 重新白名单校验、points 拒绝负值（nil=不动）
	if req.TenantID != nil {
		cur.TenantID = *req.TenantID
	}
	if req.Name != "" {
		cur.Name = req.Name
	}
	if req.PType != "" {
		if req.PType != store.PackageFree && req.PType != store.PackagePaid && req.PType != store.PackageIncrement {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "ptype 仅支持 free/paid/increment"})
			return
		}
		cur.PType = req.PType
	}
	if req.Sentences > 0 {
		cur.Sentences = req.Sentences
	}
	if req.Points != nil {
		if *req.Points < 0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "points 不能为负"})
			return
		}
		cur.Points = *req.Points
	}
	// ★ C14：价格/期限指针化后，「不修改」与「显式设为 0」可区分
	//   （旧实现 -1/缺省混淆：0 元价改不了、限期改不回不限、负价直接落库）。
	if req.PriceMoney != nil {
		if *req.PriceMoney < 0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "price_money 不能为负"})
			return
		}
		cur.PriceMoney = *req.PriceMoney
	}
	if req.DurationDays != nil {
		if *req.DurationDays < 0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "duration_days 不能为负"})
			return
		}
		cur.DurationDays = *req.DurationDays
	}
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if req.SortOrder != nil {
		cur.SortOrder = *req.SortOrder
	}
	// 合并完成后整行落库并记 package_update 审计（cur 为原记录+增量字段的合成值）
	if err := s.Store.UpdatePackage(cur); err != nil {
		// F-64②：整行 UPDATE 落库失败按服务端写入故障回（500）。
		// ⚠️ 本支还混着一条可达的 409 语义——改 tenant_id 时撞 packages 的 UNIQUE(tenant_id, code)
		//（把包迁到一个已有同 code 的租户）；store 未给结构化错误，本层按 IsUniqueViolation 分流
		// 需要新增判定分支，超出「只换壳」边界，已作为不确定项上报主代理定夺。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_update", "packages", cur.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAdminPackageSettings 读取商业包全局设置（super_admin）：强制计费开关 / 体验额度 / 支付模式 / 静态码配置。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 billing_enforced / free_trial_points / free_trial_days / pay_mode / static_qr_image。
// ★ 2026-09-19 积分口径：体验额度以积分回显；句↔token 换算率与积分汇率不再经本接口透出。
func (s *Server) handleAdminPackageSettings(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// ★ 任务2.2：体验额度唯一口径 free_trial_tokens / free_trial_days（旧键 trial_sentences 已下线）
	// ★ C6（2026-09-12）：默认值单一来源，读取点收敛到 trialConfig
	freeTokens, freeDays := s.trialConfig()
	enforced := "0"
	if v, _ := s.Store.GetConfig("billing_enforced"); v != "" {
		enforced = v
	}
	payMode := "mock"
	if v, _ := s.Store.GetConfig("pay_mode"); v != "" {
		payMode = v
	}
	staticQR := ""
	if v, _ := s.Store.GetConfig("static_qr_image"); v != "" {
		staticQR = v
	}
	// ★ Token 实费参数：均摊系数（换算率与积分汇率属内部记账口径，★ 2026-09-19 起不在本接口回显/保存，
	//   仅可经运营配置 system_config 直改）
	markup := ops.DefaultMarkupMultiplier()
	if v, _ := s.Store.GetConfig("billing_markup_multiplier"); v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil && f >= 1.0 {
			markup = f
		}
	}
	// 三期注册与触达配置：邮箱验证 / 人机验证 / 群机器人（secret_key 只写不回显）
	// ★ 批 I-6：四个群机器人 webhook 掩码回显（URL 即凭证，见文件头 webhookMaskKeys 说明）——
	//   写侧配套 webhookSaveSkipped，前端把掩码串原样存回来时不覆盖真值。
	getCfg := func(k string) string { v, _ := s.Store.GetConfig(k); return v }
	maskedCfg := func(k string) string { return maskWebhookValue(getCfg(k)) }
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "billing_enforced": enforced,
		// ★ 2026-09-19 积分口径：体验额度以积分回显（内部仍按 token 记账）
		"free_trial_points": s.Store.PointsFromTokens(freeTokens), "free_trial_days": freeDays,
		"pay_mode": payMode, "static_qr_image": staticQR,
		"email_verify_enabled": getCfg("email_verify_enabled"),
		"email_notify_enabled": getCfg("email_notify_enabled"),
		"captcha_provider":     getCfg("captcha_provider"),
		"captcha_site_key":     getCfg("captcha_site_key"),
		"wecom_webhook_url":    maskedCfg("wecom_webhook_url"),
		"dingtalk_webhook_url": maskedCfg("dingtalk_webhook_url"),
		// ★ P2 国际化渠道（2026-09-15）：Slack / Teams 群机器人（bot.go 统一消费）
		// ★ 批 I-6：同企微/钉钉一并掩码——这四个键旧实现整串明文回显（含机器人凭证）
		"slack_webhook_url": maskedCfg("slack_webhook_url"),
		"teams_webhook_url": maskedCfg("teams_webhook_url"),
		// ★ Token 实费参数（四期）：成本均摊系数（无量纲，保留）
		"billing_markup_multiplier": markup,
		// ★ S3 防薅：一次性邮箱域黑名单（运营增补部分；内置表不随出参重复）
		"disposable_email_domains": func() string {
			v, _ := s.Store.GetConfig("disposable_email_domains")
			return v
		}(),
		// ★ S8 敏感词兑底闸开关（"0"=临时停用；词包空文件=天然关闭）
		"sensitive_gate_enabled": func() string {
			if v, _ := s.Store.GetConfig("sensitive_gate_enabled"); v == "0" {
				return "0"
			}
			return "1"
		}(),
		// ★ USDT 收款（2026-09-15）：开关/链/地址/汇率/确认数/自动对账（RPC 凭证走环境变量不在此露出）
		"usdt_enabled":             getCfg("usdt_enabled"),
		"usdt_auto_settle":         getCfg("usdt_auto_settle"),
		"usdt_tail_enabled":        getCfg("usdt_tail_enabled"),
		"usdt_chains":              getCfg("usdt_chains"),
		"usdt_addr_trc20":          getCfg("usdt_addr_trc20"),
		"usdt_addr_erc20":          getCfg("usdt_addr_erc20"),
		"usdt_addr_bep20":          getCfg("usdt_addr_bep20"),
		"usdt_rate_fen_per_usdt":   getCfg("usdt_rate_fen_per_usdt"),
		"usdt_confirmations_trc20": getCfg("usdt_confirmations_trc20"),
		"usdt_confirmations_erc20": getCfg("usdt_confirmations_erc20"),
		"usdt_confirmations_bep20": getCfg("usdt_confirmations_bep20"),
	})
}

// handleAdminPackageSettingsSave 保存商业包全局设置（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 billing_enforced/free_trial_points/free_trial_days/pay_mode/static_qr_image 可选字段）。
// 返回: success=true 表示保存成功。
func (s *Server) handleAdminPackageSettingsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		BillingEnforced  *string  `json:"billing_enforced"`          // 强制计费开关："1"/"0"
		FreeTrialPoints  *int64   `json:"free_trial_points"`         // ★ 积分口径：新租户体验积分数（内部折 token 落库）
		FreeTrialDays    *int64   `json:"free_trial_days"`           // 体验有效期（天）
		MarkupMultiplier *float64 `json:"billing_markup_multiplier"` // 成本均摊系数（≥1.0）
		SensitiveGate    *string  `json:"sensitive_gate_enabled"`    // ★ S8 敏感词兑底闸："1"/"0"
		PayMode          *string  `json:"pay_mode"`                  // mock / sdk / static_qr
		StaticQRImage    *string  `json:"static_qr_image"`           // 静态收款码图片 URL 或 base64
		// ★ USDT 收款（2026-09-15）：开关/链/地址/汇率/确认数（RPC 凭证走环境变量）
		USDTEnabled      *string `json:"usdt_enabled"`           // "1"/"0" 总开关（开启需地址+汇率就绪）
		USDTAutoSettle   *string `json:"usdt_auto_settle"`       // "1"/"0" 自动对账（默认关：仅人工核销）
		USDTCtailEnabled *string `json:"usdt_tail_enabled"`      // "1"/"0" 金额尾数防混淆（默认开）
		USDTChains       *string `json:"usdt_chains"`            // 逗号分隔：trc20,erc20,bep20
		USDTAddrTRC20    *string `json:"usdt_addr_trc20"`        // TRC20 收款地址
		USDTAddrERC20    *string `json:"usdt_addr_erc20"`        // ERC20 收款地址
		USDTAddrBEP20    *string `json:"usdt_addr_bep20"`        // BEP20 收款地址
		USDFtRateFen     *int64  `json:"usdt_rate_fen_per_usdt"` // 汇率：人民币分/USDT（¥7.2→720）
		USDTConfTRC20    *int64  `json:"usdt_confirmations_trc20"`
		USDTConfERC20    *int64  `json:"usdt_confirmations_erc20"`
		USDTConfBEP20    *int64  `json:"usdt_confirmations_bep20"`
		// 三期注册与触达配置（均可选，传了才更新；secret_key 只写不回显）
		EmailVerifyEnabled     *string `json:"email_verify_enabled"`     // "1"=注册需邮箱验证码
		EmailNotifyEnabled     *string `json:"email_notify_enabled"`     // "1"=站内通知同步邮件触达租户管理员
		CaptchaProvider        *string `json:"captcha_provider"`         // 空/none=关闭；turnstile
		CaptchaSiteKey         *string `json:"captcha_site_key"`         // Turnstile 站点 key（公开下发）
		CaptchaSecretKey       *string `json:"captcha_secret_key"`       // Turnstile 服务端密钥（只写）
		WecomWebhookURL        *string `json:"wecom_webhook_url"`        // 企业微信群机器人地址
		SlackWebhookURL        *string `json:"slack_webhook_url"`        // ★ P2：Slack Incoming Webhook 地址（hooks.slack.com/services/…）
		TeamsWebhookURL        *string `json:"teams_webhook_url"`        // ★ P2：Teams/M365 连接器 Incoming Webhook 地址
		DisposableEmailDomains *string `json:"disposable_email_domains"` // ★ S3 防薅：一次性邮箱域增补黑名单（逗号分隔，叠加内置表）
		DingtalkWebhookURL     *string `json:"dingtalk_webhook_url"`     // 钉钉群机器人地址
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// ★ C28（2026-09-12）重构：旧实现「边校验边写入、审计在校验前落笔」——
	//   后置字段校验失败时，前面字段已生效但响应 400，且审计留下"已保存"假轨迹。
	//   现在：① 全量校验 → ② 收集变更 → ③ 统一落库 → ④ 成功才写审计（带变更键明细）。
	var pending []struct{ key, val string }
	add := func(k, v string) { pending = append(pending, struct{ key, val string }{k, v}) }
	if req.BillingEnforced != nil {
		if *req.BillingEnforced != "0" && *req.BillingEnforced != "1" { // 值域白名单（C28）
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": `billing_enforced 仅支持 "0"/"1"`})
			return
		}
		add("billing_enforced", *req.BillingEnforced)
	}
	// ★ 任务2.2：体验额度唯一口径 free_trial_tokens / free_trial_days（积分入参折 token 落库）
	if req.FreeTrialPoints != nil && *req.FreeTrialPoints > 0 {
		add("free_trial_tokens", strconv.FormatInt(s.Store.TokensFromPoints(*req.FreeTrialPoints), 10))
	}
	if req.FreeTrialDays != nil && *req.FreeTrialDays > 0 {
		add("free_trial_days", strconv.FormatInt(*req.FreeTrialDays, 10))
	}
	if req.PayMode != nil {
		if *req.PayMode != "mock" && *req.PayMode != "sdk" && *req.PayMode != "static_qr" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "pay_mode 仅支持 mock/sdk/static_qr"})
			return
		}
		add("pay_mode", *req.PayMode)
	}
	if req.StaticQRImage != nil {
		add("static_qr_image", *req.StaticQRImage)
	}
	// ★ USDT 收款（2026-09-15）：开关键 + 收款要素（地址/汇率按链校验后才可开启总闸）
	if req.USDTEnabled != nil {
		if *req.USDTEnabled != "0" && *req.USDTEnabled != "1" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": `usdt_enabled 仅支持 "0"/"1"`})
			return
		}
		if *req.USDTEnabled == "1" {
			// 开启前强校验（允许与地址/汇率同批保存：优先取本请求值，回落库值）
			rateOK := s.Store.GetConfigInt("usdt_rate_fen_per_usdt") > 0
			if req.USDFtRateFen != nil {
				rateOK = *req.USDFtRateFen > 0
			}
			if !rateOK {
				writeJSON(w, 400, map[string]interface{}{"success": false, "message": "USDT 开启失败：请先配置汇率（usdt_rate_fen_per_usdt，人民币分/USDT）"})
				return
			}
			addrOf := func(chain string, reqVal *string) string {
				if reqVal != nil {
					return strings.TrimSpace(*reqVal)
				}
				v, _ := s.Store.GetConfig("usdt_addr_" + chain)
				return strings.TrimSpace(v)
			}
			found := false
			for _, c := range []string{"trc20", "erc20", "bep20"} {
				var rv *string
				switch c {
				case "trc20":
					rv = req.USDTAddrTRC20
				case "erc20":
					rv = req.USDTAddrERC20
				case "bep20":
					rv = req.USDTAddrBEP20
				}
				if a := addrOf(c, rv); a != "" && payment.ValidUSDTAddress(c, a) {
					found = true
				}
			}
			if !found {
				writeJSON(w, 400, map[string]interface{}{"success": false, "message": "USDT 开启失败：请先配置至少一条链的合法收款地址"})
				return
			}
		}
		add("usdt_enabled", *req.USDTEnabled)
	}
	if req.USDTAutoSettle != nil {
		if *req.USDTAutoSettle != "0" && *req.USDTAutoSettle != "1" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": `usdt_auto_settle 仅支持 "0"/"1"`})
			return
		}
		add("usdt_auto_settle", *req.USDTAutoSettle)
	}
	if req.USDTCtailEnabled != nil {
		if *req.USDTCtailEnabled != "0" && *req.USDTCtailEnabled != "1" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": `usdt_tail_enabled 仅支持 "0"/"1"`})
			return
		}
		add("usdt_tail_enabled", *req.USDTCtailEnabled)
	}
	if req.USDTChains != nil {
		chains := []string{}
		for _, c := range strings.Split(*req.USDTChains, ",") {
			c = payment.NormalizeChain(c)
			if c == "" {
				writeJSON(w, 400, map[string]interface{}{"success": false, "message": "usdt_chains 含未知链（可用：trc20/erc20/bep20）"})
				return
			}
			chains = append(chains, c)
		}
		if len(chains) == 0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "usdt_chains 不能为空"})
			return
		}
		add("usdt_chains", strings.Join(chains, ","))
	}
	if req.USDTAddrTRC20 != nil {
		v := strings.TrimSpace(*req.USDTAddrTRC20)
		if v != "" && !payment.ValidUSDTAddress("trc20", v) {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "TRC20 收款地址格式非法（T 开头 34 位 base58）"})
			return
		}
		add("usdt_addr_trc20", v)
	}
	if req.USDTAddrERC20 != nil {
		v := strings.TrimSpace(*req.USDTAddrERC20)
		if v != "" && !payment.ValidUSDTAddress("erc20", v) {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "ERC20 收款地址格式非法（0x+40 位十六进制）"})
			return
		}
		add("usdt_addr_erc20", v)
	}
	if req.USDTAddrBEP20 != nil {
		v := strings.TrimSpace(*req.USDTAddrBEP20)
		if v != "" && !payment.ValidUSDTAddress("bep20", v) {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "BEP20 收款地址格式非法（0x+40 位十六进制）"})
			return
		}
		add("usdt_addr_bep20", v)
	}
	if req.USDFtRateFen != nil {
		if *req.USDFtRateFen <= 0 || *req.USDFtRateFen > 100_000_000 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "usdt_rate_fen_per_usdt 非法（分/USDT，如 ¥7.2 → 720）"})
			return
		}
		add("usdt_rate_fen_per_usdt", strconv.FormatInt(*req.USDFtRateFen, 10))
	}
	for chain, ptr := range map[string]*int64{"trc20": req.USDTConfTRC20, "erc20": req.USDTConfERC20, "bep20": req.USDTConfBEP20} {
		if ptr == nil {
			continue
		}
		if *ptr <= 0 || *ptr > 2000 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "usdt_confirmations 非法（1–2000）"})
			return
		}
		add("usdt_confirmations_"+chain, strconv.FormatInt(*ptr, 10))
	}
	// 三期注册与触达配置保存（键名白名单直传；空串=清除配置）
	cfgKeys := []struct {
		key string
		val *string
	}{
		{"email_verify_enabled", req.EmailVerifyEnabled},
		{"email_notify_enabled", req.EmailNotifyEnabled},
		{"captcha_provider", req.CaptchaProvider},
		{"captcha_site_key", req.CaptchaSiteKey},
		{"captcha_secret_key", req.CaptchaSecretKey},
		{"wecom_webhook_url", req.WecomWebhookURL},
		{"disposable_email_domains", req.DisposableEmailDomains},
		{"dingtalk_webhook_url", req.DingtalkWebhookURL},
		{"slack_webhook_url", req.SlackWebhookURL},
		{"teams_webhook_url", req.TeamsWebhookURL},
	}
	for _, kv := range cfgKeys {
		if kv.val == nil {
			continue
		}
		// ★ 批 I-6 配套守卫：webhook 四键若收到掩码回显（含 ****），视为「用户没改这一项」直接跳过，
		//   绝不把 abcd****wxyz 写回库（否则超管「打开面板直接点保存」就让机器人集体失效）。
		//   读侧已改掩码回显，本守卫必须同批在位——只做一半是事故，不是修复。
		if webhookSaveSkipped(kv.key, *kv.val) {
			continue
		}
		add(kv.key, *kv.val)
	}
	// ★ 计费参数（Token 实费体系）：均摊系数与换算率，超管可调
	// 四个参数各自范围校验后以字符串值进 pending 暂存区，循环外统一落库（见下）
	if req.MarkupMultiplier != nil {
		if *req.MarkupMultiplier < 1.0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "均摊系数不能小于 1.0"})
			return
		}
		add("billing_markup_multiplier", strconv.FormatFloat(*req.MarkupMultiplier, 'f', 2, 64))
	}
	// 敏感词闸门开关：仅接受 "0"/"1" 字符串（前端开关态直传）
	if req.SensitiveGate != nil {
		if *req.SensitiveGate != "0" && *req.SensitiveGate != "1" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": `sensitive_gate_enabled 仅支持 "0"/"1"`})
			return
		}
		add("sensitive_gate_enabled", *req.SensitiveGate)
	}
	for _, kv := range pending {
		if err := s.Store.SetConfig(kv.key, kv.val); err != nil {
			// ★ F-64② 收尾（2026-09-26 批 I-10）：原回 500 + err.Error() 裸拼——写 system_config
			//   失败是本进程存储层故障（500 语义不变），但驱动原文会把表名/约束名/连接信息
			//   送给任意登录用户（口径见 errmsg.go 文件头）。现按统一出口：对外只留「保存失败（键名）」，
			//   原文进 slog（带 trace_id），排障走日志不走响应体。
			observability.Error(r.Context(), "套餐中心配置保存失败", "key", kv.key, "err", err)
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存失败（"+kv.key+"）"))
			return
		}
	}
	if len(pending) > 0 {
		s.invalidatePolicyCache() // ★ C31：pay_mode/billing_enforced 等经 applyLegacyConfig 影响有效策略
		var keys []string
		payModeMock := false
		for _, kv := range pending {
			keys = append(keys, kv.key)
			if kv.key == "pay_mode" && kv.val == "mock" {
				payModeMock = true
			}
		}
		// ★ O-1（2026-09-26 批 I-10 定夺）：支付模式切到 mock 时在审计里显式写下**新值**。
		//   背景：超管侧「支付模式」单选一直含「模拟支付（测试）」项（F-09 把租户侧射程管住了，
		//   超管侧保留是研发自助验收的必需入口，不能删）；真正的风险是**发布后误切**——
		//   一旦切到 mock，全站租户收银台立刻出现「模拟支付」，任何人都能零成本开通套餐。
		//   这里不拦（拦了 UAT 就跑不了支付链路），改为把「谁在何时把它切成了 mock」
		//   留成一条可回查的硬账：出事故时按审计一眼定位切换时刻与操作人。
		//   其余配置项**只记键名不记值**：static_qr_image 可能是整张 base64 图片、
		//   模型密钥类同族配置也走这条保存链，把值写进审计等于把大对象/敏感料送进日志。
		//   配套的发布红线见《部署指南》§十 验收清单（两站 pay_mode 必须非 mock）。
		detail := strings.Join(keys, ",")
		if payModeMock {
			detail += "｜pay_mode=mock ⚠模拟支付已开启：租户收银台会出现「模拟支付」，生产误切即全站零成本开通"
		}
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_settings_save", "system", detail)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAdminQRUpload 上传套餐中心静态收款码图片（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart 表单，字段名 file，图片文件）。
// 返回: success=true 时携带 qr_url（静态码图片的相对 URL，前端存入 static_qr_image 配置）。
// 说明：图片保存到上传目录 _qr 子目录，文件名用 uniqueName 生成（纳秒级唯一 + Base 清洗防路径穿越）；
// 通过 /api/qr-image/<文件名> 公开访问（支付二维码需被买家扫码，属公开资源）。
func (s *Server) handleAdminQRUpload(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 大小/扩展名校验（复用 parseUpload，仅允许图片白名单）
	if err := parseUpload(r, qrImageUploadMax, qrImageWhitelist); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少文件"})
		return
	}
	defer file.Close()
	// 保存到上传目录 _qr 子目录（独立命名空间，避免与翻译上传文件混放）
	dir := filepath.Join(s.Cfg.UploadDir, "_qr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "无法创建上传目录"})
		return
	}
	savePath := filepath.Join(dir, uniqueName(header.Filename))
	f, err := os.Create(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "无法保存文件"})
		return
	}
	defer f.Close()
	if _, err := io.Copy(f, file); err != nil {
		os.Remove(savePath)
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "写入失败"})
		return
	}
	qrURL := "/api/qr-image/" + filepath.Base(savePath)
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_qr_upload", "packages", qrURL)
	writeJSON(w, 200, map[string]interface{}{"success": true, "qr_url": qrURL})
}

// handleQRImage 公开访问静态收款码图片（/api/qr-image/<文件名>，无需登录）。
// 支付二维码需被买家扫码，属公开资源；文件名经 uniqueName 生成（纳秒级随机），
// 且仅允许 _qr 子目录内的图片文件，防止路径穿越访问其他上传文件。
func (s *Server) handleQRImage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/qr-image/")
	if name == "" || strings.ContainsAny(name, "/\\") {
		writeJSON(w, 404, map[string]interface{}{"success": false, "message": "图片不存在"})
		return
	}
	dir := filepath.Join(s.Cfg.UploadDir, "_qr")
	full := filepath.Join(dir, filepath.Base(name))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		writeJSON(w, 404, map[string]interface{}{"success": false, "message": "图片不存在"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, full)
}

// handleQRRender 按文本渲染二维码 PNG（/api/qr/render?text=...，需登录）。
// ★ 2026-09 debug：mock/wechat/alipay 渠道返回的 qr_content 是字符串（mockpay://…、
//
//	weixin://…、alipay://…），收银台需展示真实二维码图片供扫码，故复用 go-qrcode
//	将任意支付串渲染为 PNG（与邀请二维码同款渲染）。仅限登录用户，text 最长 512 字符。
func (s *Server) handleQRRender(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	text := strings.TrimSpace(r.URL.Query().Get("text"))
	if text == "" || len(text) > 512 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "text 参数必填且不超过 512 字符"})
		return
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "二维码生成失败: " + err.Error()})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// handleAdminPackageDelete 删除商业包（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 表示删除成功。
func (s *Server) handleAdminPackageDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除包 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.DeletePackage(req.ID); err != nil {
		// F-64②：删除失败按设计只有一条业务拒绝——C15 引用闸门（pending 订单 / 仍有余量的活跃台账），
		// store 回 errPkgRef「套餐被引用无法删除：…，请改用停用」⇒ 409 状态冲突
		//（载荷没错、错在资源当前状态，改下架才是正解；重试同一份删除请求永远不会成功）。
		// 两支 COUNT(*) 查询本身的数据库错误同走此文案（本层无结构化错误可分，属既有偏差）。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_delete", "packages", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
