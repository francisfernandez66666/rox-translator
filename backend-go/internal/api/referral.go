// ============ referral.go · 职责说明 ============
// 邀请裂变 HTTP 接口（白皮书 §五）：
//   - GET /api/referral/my      我的邀请码 + 邀请链接 + 邀请记录 + 奖励统计
//   - GET /api/referral/qrcode  邀请链接二维码 PNG（?download=1 触发下载）
//
// 全部按当前登录用户隔离；奖励发放逻辑在存储层（BindReferral/GrantTrialStack/RewardPaidPermanent）。
package api

import (
	"net/http"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// inviteBaseURL 推导邀请链接前缀：优先反代头 X-Forwarded-Proto，其次请求 TLS；路径固定 /register?ref=<个人码>。
func inviteBaseURL(r *http.Request) string {
	scheme := "http"
	if p := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// handleReferralMy 我的邀请主页数据：
// 返回: ref_code=个人邀请码（懒生成）、invite_url=专属注册链接、records=邀请奖励记录、stats=汇总。
func (s *Server) handleReferralMy(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	code := s.Store.EnsureRefCode(u.ID)
	inviteURL := inviteBaseURL(r) + "/register?ref=" + code
	records := s.Store.ListReferrals(u.ID)
	// 汇总：累计邀请人数、体验叠加次数与 token、付费永久奖励 token
	invited := len(records)
	trialCount, trialTokens, paidTokens := 0, int64(0), int64(0)
	for _, rec := range records {
		if rec.Type == "trial_stack" {
			trialCount++
			trialTokens += rec.Tokens
		} else if rec.Type == "paid_perm" {
			paidTokens += rec.Tokens
		}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":      true,
		"ref_code":     code,
		"invite_url":   inviteURL,
		"records":      records,
		"invited":      invited,
		"trial_count":  trialCount,
		"trial_tokens": trialTokens,
		"paid_tokens":  paidTokens,
	})
}

// handleReferralQrcode 邀请链接二维码 PNG（skip2/go-qrcode，容错 Medium，尺寸 512px）。
// 参数 r: ?download=1 时携带 Content-Disposition 附件下载。
func (s *Server) handleReferralQrcode(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	if s.Store == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "平台未初始化"})
		return
	}
	code := s.Store.EnsureRefCode(u.ID)
	url := inviteBaseURL(r) + "/register?ref=" + code
	png, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "二维码生成失败: " + err.Error()})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", "attachment; filename=\"invite-"+code+".png\"")
	}
	_, _ = w.Write(png)
}

// registerReferralRoutes 注册邀请裂变路由。
func (s *Server) registerReferralRoutes() {
	s.mux.HandleFunc("/api/referral/my", s.handleReferralMy)
	s.mux.HandleFunc("/api/referral/qrcode", s.handleReferralQrcode)
}
