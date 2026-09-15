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

	qrcode "github.com/skip2/go-qrcode"

	"translator/internal/store"
)

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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	pkgs, err := s.Store.ListCommercialPackages()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "创建失败：套餐编码已存在"})
			return
		}
		log.Printf("[packages] 创建套餐失败 code=%s: %v", req.Code, err)
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "创建失败: " + store.DebriefDBError(err)})
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "包不存在"})
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_update", "packages", cur.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleAdminPackageSettings 读取商业包全局设置（super_admin）：强制计费开关 / 体验额度 / 支付模式 / 静态码配置。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 billing_enforced / free_trial_tokens / free_trial_days / pay_mode / static_qr_image。
func (s *Server) handleAdminPackageSettings(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
	// ★ Token 实费参数：均摊系数与句↔token 换算率（默认值 ★ C6 单一来源）
	markup := ops.DefaultMarkupMultiplier()
	if v, _ := s.Store.GetConfig("billing_markup_multiplier"); v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil && f >= 1.0 {
			markup = f
		}
	}
	tokenRate := s.Store.TokenSentenceRate()
	// 三期注册与触达配置：邮箱验证 / 人机验证 / 群机器人（secret_key 只写不回显）
	getCfg := func(k string) string { v, _ := s.Store.GetConfig(k); return v }
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "billing_enforced": enforced,
		"free_trial_tokens": freeTokens, "free_trial_days": freeDays,
		"pay_mode": payMode, "static_qr_image": staticQR,
		"email_verify_enabled": getCfg("email_verify_enabled"),
		"email_notify_enabled": getCfg("email_notify_enabled"),
		"captcha_provider":     getCfg("captcha_provider"),
		"captcha_site_key":     getCfg("captcha_site_key"),
		"wecom_webhook_url":    getCfg("wecom_webhook_url"),
		"dingtalk_webhook_url": getCfg("dingtalk_webhook_url"),
		// ★ P2 国际化渠道（2026-09-15）：Slack / Teams 群机器人（bot.go 统一消费）
		"slack_webhook_url": getCfg("slack_webhook_url"),
		"teams_webhook_url": getCfg("teams_webhook_url"),
		// ★ Token 实费参数（四期）：均摊系数与句↔token 换算率
		"billing_markup_multiplier":    markup,
		"estimate_tokens_per_sentence": tokenRate,
		// ★ S1 积分制：积分↔内部计量 token 汇率（对外只露积分，超管可见真实口径）
		"points_tokens_rate": s.Store.PointsTokensRate(),
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
	})
}

// handleAdminPackageSettingsSave 保存商业包全局设置（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 billing_enforced/free_trial_tokens/free_trial_days/pay_mode/static_qr_image 可选字段）。
// 返回: success=true 表示保存成功。
func (s *Server) handleAdminPackageSettingsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		BillingEnforced   *string  `json:"billing_enforced"`             // 强制计费开关："1"/"0"
		FreeTrialTokens   *int64   `json:"free_trial_tokens"`            // 新租户体验 token 数
		FreeTrialDays     *int64   `json:"free_trial_days"`              // 体验有效期（天）
		MarkupMultiplier  *float64 `json:"billing_markup_multiplier"`    // 成本均摊系数（≥1.0）
		TokensPerSentence *int64   `json:"estimate_tokens_per_sentence"` // 句↔token 换算率（>0）
		PointsTokensRate  *int64   `json:"points_tokens_rate"`           // ★ S1 积分汇率：1 积分=N 内部 token（>0）
		SensitiveGate     *string  `json:"sensitive_gate_enabled"`       // ★ S8 敏感词兑底闸："1"/"0"
		PayMode           *string  `json:"pay_mode"`                     // mock / sdk / static_qr
		StaticQRImage     *string  `json:"static_qr_image"`              // 静态收款码图片 URL 或 base64
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
	// ★ 任务2.2：体验额度唯一口径 free_trial_tokens / free_trial_days
	if req.FreeTrialTokens != nil && *req.FreeTrialTokens > 0 {
		add("free_trial_tokens", strconv.FormatInt(*req.FreeTrialTokens, 10))
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
		if kv.val != nil {
			add(kv.key, *kv.val)
		}
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
	if req.TokensPerSentence != nil {
		if *req.TokensPerSentence <= 0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "换算率必须大于 0"})
			return
		}
		add("estimate_tokens_per_sentence", strconv.FormatInt(*req.TokensPerSentence, 10))
	}
	if req.PointsTokensRate != nil {
		if *req.PointsTokensRate <= 0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "积分汇率必须大于 0"})
			return
		}
		add("points_tokens_rate", strconv.FormatInt(*req.PointsTokensRate, 10))
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
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": "保存失败（" + kv.key + "）: " + err.Error()})
			return
		}
	}
	if len(pending) > 0 {
		s.invalidatePolicyCache() // ★ C31：pay_mode/billing_enforced 等经 applyLegacyConfig 影响有效策略
		var keys []string
		for _, kv := range pending {
			keys = append(keys, kv.key)
		}
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_settings_save", "system", strings.Join(keys, ","))
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 大小/扩展名校验（复用 parseUpload，仅允许图片白名单）
	if err := parseUpload(r, qrImageUploadMax, qrImageWhitelist); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_delete", "packages", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
