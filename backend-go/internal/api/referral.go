// ============ referral.go · 职责说明 ============
// 邀请裂变 HTTP 接口（白皮书 §五）：
//   - GET  /api/referral/my            我的邀请码 + 邀请链接 + 邀请记录 + 奖励统计
//   - GET  /api/referral/qrcode        邀请链接二维码 PNG（?download=1 触发下载）
//   - GET/POST /api/admin/referral/config  邀请裂变运营参数（仅超管：启用开关 / 体验奖励 token / 付费永久奖励 token）
//
// 全部按当前登录用户隔离；奖励发放逻辑在存储层（BindReferral/GrantTrialStack/RewardPaidPermanent）。
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"translator/internal/auth"
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
	// 用户鉴权
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 确保用户有邀请码（懒生成）
	code := s.Store.EnsureRefCode(u.ID)
	// 构造专属注册链接
	inviteURL := inviteBaseURL(r) + "/register?ref=" + code
	// 查询邀请奖励记录
	records := s.Store.ListReferrals(u.ID)
	// 汇总统计数据：累计邀请人数、体验叠加次数与 token、付费永久奖励 token
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

// sysReferralCfgInt64 读取整型配置（>0 有效，否则回退默认值）。
// 参数 get: 配置读取函数；key: 配置键；def: 默认值。
// 返回: 解析后的正整数；解析失败或值 ≤0 时返回 def。
func sysReferralCfgInt64(get func(string) (string, error), key string, def int64) int64 {
	if v, err := get(key); err == nil && v != "" {
		if x, perr := strconv.ParseInt(v, 10, 64); perr == nil && x > 0 {
			return x
		}
	}
	return def
}

// handleAdminReferralConfig 邀请裂变运营参数（仅超管；2026-08-26 U3 需求）：
//   - GET  /api/admin/referral/config → {enabled, reward_tokens, paid_reward_tokens}
//   - POST 同路径，body 三个可选字段增量更新（enabled/reward_tokens/paid_reward_tokens）
//
// 配置键：referral_enabled（"0"=关闭，缺省开启）/ invite_reward_tokens /
// inviter_paid_reward_tokens。token 入参负值归零。
func (s *Server) handleAdminReferralConfig(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil || !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超级管理员可配置邀请参数"})
		return
	}
	const (
		kEnabled    = "referral_enabled"
		kReward     = "invite_reward_tokens"
		kPaid       = "inviter_paid_reward_tokens"
		kRewardDays = "invite_extend_days" // 注册邀请奖励有效期（天）；register.go 读取，此处暴露给超管
		kPaidDays   = "inviter_paid_reward_days" // 付费邀请奖励有效期（天）；0=永久（默认）
	)
	get := func(key string) (string, error) { return s.Store.GetConfig(key) }
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]interface{}{
			"success":            true,
			"enabled":            s.Store.ReferralEnabled(),
			"reward_tokens":      sysReferralCfgInt64(get, kReward, 300000),
			"paid_reward_tokens": sysReferralCfgInt64(get, kPaid, 500000),
			"reward_days":        sysReferralCfgInt64(get, kRewardDays, 14),
			"paid_reward_days":   sysReferralCfgInt64(get, kPaidDays, 0),
		})
	case http.MethodPost:
		var req struct {
			Enabled      *bool  `json:"enabled"`
			RewardTokens *int64 `json:"reward_tokens"`
			PaidTokens   *int64 `json:"paid_reward_tokens"`
			RewardDays   *int64 `json:"reward_days"`
			PaidDays     *int64 `json:"paid_reward_days"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
			return
		}
		if req.Enabled != nil {
			v := "1"
			if !*req.Enabled {
				v = "0"
			}
			_ = s.Store.SetConfig(kEnabled, v)
		}
		if req.RewardTokens != nil {
			if *req.RewardTokens < 0 {
				*req.RewardTokens = 0
			}
			_ = s.Store.SetConfig(kReward, strconv.FormatInt(*req.RewardTokens, 10))
		}
		if req.PaidTokens != nil {
			if *req.PaidTokens < 0 {
				*req.PaidTokens = 0
			}
			_ = s.Store.SetConfig(kPaid, strconv.FormatInt(*req.PaidTokens, 10))
		}
		// 注册邀请奖励有效期（天）：至少 1 天，避免 0 天立刻过期
		if req.RewardDays != nil {
			d := *req.RewardDays
			if d < 1 {
				d = 1
			}
			_ = s.Store.SetConfig(kRewardDays, strconv.FormatInt(d, 10))
		}
		// 付费邀请奖励有效期（天）：0=永久（默认），>0=限时
		if req.PaidDays != nil {
			d := *req.PaidDays
			if d < 0 {
				d = 0
			}
			_ = s.Store.SetConfig(kPaidDays, strconv.FormatInt(d, 10))
		}
		s.Store.LogAudit(0, u.ID, "referral_config", "system_config", "邀请裂变运营参数更新")
		writeJSON(w, 200, map[string]interface{}{"success": true})
	default:
		writeJSON(w, 405, map[string]interface{}{"success": false, "message": "方法不允许"})
	}
}

// registerReferralRoutes 注册邀请裂变路由。
func (s *Server) registerReferralRoutes() {
	s.mux.HandleFunc("/api/referral/my", s.handleReferralMy)
	s.mux.HandleFunc("/api/referral/qrcode", s.handleReferralQrcode)
	// ★ 邀请运营参数（仅超管，2026-08-26 U3）
	s.mux.HandleFunc("/api/admin/referral/config", s.handleAdminReferralConfig)
}
