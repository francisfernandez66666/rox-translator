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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	var req struct {
		TenantID     int64   `json:"tenant_id"`     // 租户 ID（可选，默认 0=平台）
		Code         string  `json:"code"`          // 包编码（唯一，必填）
		Name         string  `json:"name"`          // 包名称（必填）
		PType        string  `json:"ptype"`         // 包类型：free/paid/increment（默认 paid）
		Sentences    int64   `json:"sentences"`     // 包内含翻译句数（必填 >0）
		PriceMoney   float64 `json:"price_money"`   // 售价（元）
		DurationDays int     `json:"duration_days"` // 有效期（天，默认 30）
		SortOrder    int     `json:"sort_order"`    // 展示排序
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" || req.Name == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "code/name 不能为空"})
		return
	}
	if req.PType == "" {
		req.PType = store.PackagePaid
	}
	if req.PType != store.PackageFree && req.PType != store.PackagePaid && req.PType != store.PackageIncrement {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "ptype 仅支持 free/paid/increment"})
		return
	}
	if req.Sentences <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "sentences 必须大于 0"})
		return
	}
	if req.DurationDays <= 0 {
		req.DurationDays = 30
	}
	p, err := s.Store.CreatePackage(&store.Package{
		TenantID: req.TenantID, Code: req.Code, Name: req.Name, PType: req.PType, Sentences: req.Sentences,
		PriceMoney: req.PriceMoney, DurationDays: req.DurationDays, Enabled: 1, SortOrder: req.SortOrder,
	})
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "创建失败: " + err.Error()})
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
		ID           int64   `json:"id"`            // 目标包 ID（必填）
		TenantID     *int64  `json:"tenant_id"`     // 新租户 ID（nil=不修改）
		Name         string  `json:"name"`          // 新名称（可为空=不修改）
		PType        string  `json:"ptype"`         // 新类型（可为空=不修改）
		Sentences    int64   `json:"sentences"`     // 新句数（<=0=不修改）
		PriceMoney   float64 `json:"price_money"`   // 新售价（<0=不修改）
		DurationDays int     `json:"duration_days"` // 新有效期（<=0=不修改）
		Enabled      *int    `json:"enabled"`       // 启停（0/1，nil=不修改）
		SortOrder    *int    `json:"sort_order"`    // 排序（nil=不修改）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	cur, err := s.Store.GetPackage(req.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "包不存在"})
		return
	}
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
	if req.PriceMoney >= 0 {
		cur.PriceMoney = req.PriceMoney
	}
	if req.DurationDays > 0 {
		cur.DurationDays = req.DurationDays
	}
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if req.SortOrder != nil {
		cur.SortOrder = *req.SortOrder
	}
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
	freeTokens := int64(300000)
	if v, _ := s.Store.GetConfig("free_trial_tokens"); v != "" {
		if n, e := parseInt64(v); e == nil && n > 0 {
			freeTokens = n
		}
	}
	freeDays := 14
	if v, _ := s.Store.GetConfig("free_trial_days"); v != "" {
		if n, e := parseInt64(v); e == nil && n > 0 {
			freeDays = int(n)
		}
	}
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
	// ★ Token 实费参数：均摊系数（默认 1.5）与句↔token 换算率（默认 500）
	markup := 1.5
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
		// ★ Token 实费参数（四期）：均摊系数与句↔token 换算率
		"billing_markup_multiplier":    markup,
		"estimate_tokens_per_sentence": tokenRate,
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
		PayMode           *string  `json:"pay_mode"`                     // mock / sdk / static_qr
		StaticQRImage     *string  `json:"static_qr_image"`              // 静态收款码图片 URL 或 base64
		// 三期注册与触达配置（均可选，传了才更新；secret_key 只写不回显）
		EmailVerifyEnabled *string `json:"email_verify_enabled"` // "1"=注册需邮箱验证码
		EmailNotifyEnabled *string `json:"email_notify_enabled"` // "1"=站内通知同步邮件触达租户管理员
		CaptchaProvider    *string `json:"captcha_provider"`     // 空/none=关闭；turnstile
		CaptchaSiteKey     *string `json:"captcha_site_key"`     // Turnstile 站点 key（公开下发）
		CaptchaSecretKey   *string `json:"captcha_secret_key"`   // Turnstile 服务端密钥（只写）
		WecomWebhookURL    *string `json:"wecom_webhook_url"`    // 企业微信群机器人地址
		DingtalkWebhookURL *string `json:"dingtalk_webhook_url"` // 钉钉群机器人地址
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.BillingEnforced != nil {
		if err := s.Store.SetConfig("billing_enforced", *req.BillingEnforced); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	// ★ 任务2.2：体验额度唯一口径 free_trial_tokens / free_trial_days
	if req.FreeTrialTokens != nil && *req.FreeTrialTokens > 0 {
		if err := s.Store.SetConfig("free_trial_tokens", strconv.FormatInt(*req.FreeTrialTokens, 10)); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	if req.FreeTrialDays != nil && *req.FreeTrialDays > 0 {
		if err := s.Store.SetConfig("free_trial_days", strconv.FormatInt(*req.FreeTrialDays, 10)); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	if req.PayMode != nil {
		if *req.PayMode != "mock" && *req.PayMode != "sdk" && *req.PayMode != "static_qr" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "pay_mode 仅支持 mock/sdk/static_qr"})
			return
		}
		if err := s.Store.SetConfig("pay_mode", *req.PayMode); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	if req.StaticQRImage != nil {
		if err := s.Store.SetConfig("static_qr_image", *req.StaticQRImage); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
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
		{"dingtalk_webhook_url", req.DingtalkWebhookURL},
	}
	for _, kv := range cfgKeys {
		if kv.val == nil {
			continue
		}
		if err := s.Store.SetConfig(kv.key, *kv.val); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_settings_save", "system", "")

	// ★ 计费参数（Token 实费体系）：均摊系数与换算率，超管可调
	if req.MarkupMultiplier != nil {
		if *req.MarkupMultiplier < 1.0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "均摊系数不能小于 1.0"})
			return
		}
		if err := s.Store.SetConfig("billing_markup_multiplier", strconv.FormatFloat(*req.MarkupMultiplier, 'f', 2, 64)); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	if req.TokensPerSentence != nil {
		if *req.TokensPerSentence <= 0 {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "换算率必须大于 0"})
			return
		}
		if err := s.Store.SetConfig("estimate_tokens_per_sentence", strconv.FormatInt(*req.TokensPerSentence, 10)); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
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
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_delete", "packages", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
