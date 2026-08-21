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
	"net/http"
	"strconv"

	"translator/internal/store"
)

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
		Code: req.Code, Name: req.Name, PType: req.PType, Sentences: req.Sentences,
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

// handleAdminPackageSettings 读取商业包全局设置（super_admin）：句数强制开关 / 试用句数 / 支付模式 / 静态码配置。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 sentence_enforced / trial_sentences / pay_mode / static_qr_image。
func (s *Server) handleAdminPackageSettings(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	trial := int64(500)
	if v, _ := s.Store.GetConfig("trial_sentences"); v != "" {
		if n, e := parseInt64(v); e == nil && n > 0 {
			trial = n
		}
	}
	enforced := "0"
	if v, _ := s.Store.GetConfig("sentence_enforced"); v != "" {
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
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "sentence_enforced": enforced, "trial_sentences": trial,
		"pay_mode": payMode, "static_qr_image": staticQR,
	})
}

// handleAdminPackageSettingsSave 保存商业包全局设置（super_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 sentence_enforced/trial_sentences/pay_mode/static_qr_image 可选字段）。
// 返回: success=true 表示保存成功。
func (s *Server) handleAdminPackageSettingsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		SentenceEnforced *string `json:"sentence_enforced"` // "1"/"0"
		TrialSentences   *int64  `json:"trial_sentences"`   // 试用句数
		PayMode          *string `json:"pay_mode"`          // mock / sdk / static_qr
		StaticQRImage    *string `json:"static_qr_image"`   // 静态收款码图片 URL 或 base64
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.SentenceEnforced != nil {
		if err := s.Store.SetConfig("sentence_enforced", *req.SentenceEnforced); err != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
	}
	if req.TrialSentences != nil && *req.TrialSentences > 0 {
		if err := s.Store.SetConfig("trial_sentences", strconv.FormatInt(*req.TrialSentences, 10)); err != nil {
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
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "package_settings_save", "system", "")
	writeJSON(w, 200, map[string]interface{}{"success": true})
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
