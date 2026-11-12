// ============ sales_outbound_api.go · 职责说明 ============
// 三期 W5：外联合规的 API 面 + sendOne 单一发送出口 + 看门狗拨轮。
//
// ★ 三条硬口径（施工图 §3 W5 / §4.2 A16–A19 / §0.0 A2 裁定）：
//  1. **sendOne 单一出口**：全部外发（含序列每节点）只能经 sendOne；顺序＝合规四件套
//     （opt-in → 抑制名单 → 频限 freq_cap_per_day → SMTP 在位）→ **先占位再发送**
//     （ux_outbound_once 唯一键保证多实例只有一个赢家）→ 结果回写终态。
//     发送失败落 failed（带原因供排障），绝不把失败写成「已发送」。
//  2. **频限独立**：外联频限键是 freq_cap_per_day（经 store.GuardOutboundConfigKey 单一真值），
//     与 AI 每日免费额度（salesflow.Config 的同名字段）语义无关，绝不复用（A2/M0247 钉住）。
//  3. **退订匿名可达但带签名**：/api/public/marketing/unsubscribe 的 token=
//     HMAC-SHA256(退订密钥, "tid|email") hex 小写；未配密钥端点 503（fail-closed）；
//     匿名理由进 route_auth_gate_test.go 的 publicRouteAllowlist（人工评审口径）。
//
// 看门狗拨轮 runOutboundSequenceOnce：缺省关（outbound_enabled 显式 "1" 才跑），
// kill-switch 判据先于任何发送调用；不做 @startup 启动即跑（与 A05 同形状）。
// ================================================
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/mail"
	"translator/internal/observability"
	"translator/internal/store"
)

// outboundConfigKeys 允许经维护页写的外联配置键（白名单外的键一律拒写——W3 同族闸）。
var outboundConfigKeys = map[string]bool{
	"outbound_enabled":    true,
	"freq_cap_per_day":    true,
	"outbound_batch_size": true,
}

// routesSalesOutbound 注册外联管理面与匿名退订端点（由 routesSales 调用）。
func (s *Server) routesSalesOutbound() {
	s.mux.HandleFunc("/api/admin/sales/outbound/config", s.handleOutboundConfig)
	s.mux.HandleFunc("/api/admin/sales/outbound/sends", s.handleOutboundSends)
	s.mux.HandleFunc("/api/admin/sales/outbound/consent", s.handleOutboundConsent)
	s.mux.HandleFunc("/api/admin/sales/outbound/suppression", s.handleOutboundSuppression)
	s.mux.HandleFunc("/api/admin/sales/outbound/sequence", s.handleOutboundSequence)
	// GDPR 删除请求权（§8 C4）：管理员执行客户外联留痕擦除（≥3；只擦发送/订阅，抑制名单保留）。
	s.mux.HandleFunc("/api/admin/sales/outbound/purge", s.handleOutboundPurge)
	// 匿名端点：token 校验在 handler 内（公开理由见 route_auth_gate_test.go 白名单）。
	s.mux.HandleFunc("/api/public/marketing/unsubscribe", s.handlePublicUnsubscribe)
}

// handleOutboundConfig 读/写外联开关与频限（≥3；写键过白名单，频限值必须是正整数）。
func (s *Server) handleOutboundConfig(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		out := map[string]string{}
		for k := range outboundConfigKeys {
			v, _ := s.Store.GetConfig(k)
			out[k] = v
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "config": out})
	case http.MethodPost:
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
			return
		}
		for k, v := range body {
			if !outboundConfigKeys[k] {
				s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "未登记的维护开关键"))
				return
			}
			if k == "freq_cap_per_day" || k == "outbound_batch_size" {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil || n < 1 || n > 1000 {
					s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "频限必须是 1-1000 的整数"))
					return
				}
			} else if v != "0" && v != "1" {
				s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "开关值只能是 0 或 1"))
				return
			}
		}
		for k, v := range body {
			if err := s.Store.SetConfig(k, v); err != nil {
				s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
				return
			}
		}
		writeJSON(w, 200, map[string]interface{}{"success": true})
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
	}
}

// handleOutboundSends 发送记录分页（≥3，最新在前，排障用）。
func (s *Server) handleOutboundSends(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	limit, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("limit")), 10, 64)
	rows, err := s.Store.ListOutboundSends(tid, limit)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "rows": rows})
}

// handleOutboundConsent 登记订阅状态（≥3；body: email/status/source）。
func (s *Server) handleOutboundConsent(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body struct {
		Email  string `json:"email"`
		Status string `json:"status"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
		return
	}
	if err := s.Store.SetConsentStatus(tid, body.Email, body.Status, body.Source); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOutboundSuppression 加入抑制名单（≥3；body: value/kind/reason）。
func (s *Server) handleOutboundSuppression(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body struct {
		Value  string `json:"value"`
		Kind   string `json:"kind"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
		return
	}
	if err := s.Store.AddSuppression(tid, body.Value, body.Kind, body.Reason); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleOutboundSequence 序列节点读/写（≥3；GET 列表 / POST 单节点保存）。
func (s *Server) handleOutboundSequence(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		nodes, err := s.Store.ListSequenceNodes(tid)
		if err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "nodes": nodes})
	case http.MethodPost:
		var n store.OutboundSequenceNode
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
			return
		}
		if err := s.Store.SaveSequenceNode(tid, n); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true})
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
	}
}

// outboundUnsubscribeKey 退订签名密钥（库内配置 marketing_unsubscribe_key；未配置＝fail-closed）。
func (s *Server) outboundUnsubscribeKey() []byte {
	v, _ := s.Store.GetConfig("marketing_unsubscribe_key")
	return []byte(v)
}

// outboundToken 计算退订 token：HMAC-SHA256(key, "tid|email") hex 小写。
func outboundToken(key []byte, tid int64, email string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(strconv.FormatInt(tid, 10) + "|" + strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(mac.Sum(nil))
}

// handlePublicUnsubscribe 匿名退订（带 HMAC token；成功把订阅置 opted_out 并入抑制名单）。
// 未配密钥/坏 token 一律 403（不区分两种失败，不给枚举反馈）。
func (s *Server) handlePublicUnsubscribe(w http.ResponseWriter, r *http.Request) {
	key := s.outboundUnsubscribeKey()
	if len(key) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrServiceUnavailable, "退订通道未启用"))
		return
	}
	q := r.URL.Query()
	tid, err := strconv.ParseInt(strings.TrimSpace(q.Get("tid")), 10, 64)
	email := strings.ToLower(strings.TrimSpace(q.Get("email")))
	tok := strings.ToLower(strings.TrimSpace(q.Get("token")))
	if err != nil || tid <= 0 || email == "" || tok == "" ||
		!hmac.Equal([]byte(tok), []byte(outboundToken(key, tid, email))) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "退订链接无效"))
		return
	}
	// 幂等：重复退订＝再次置 opted_out + 名单幂等加入，两个口都天然幂等。
	if err := s.Store.SetConsentStatus(tid, email, store.ConsentOptedOut, "unsubscribe_link"); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	_ = s.Store.AddSuppression(tid, email, "email", "unsubscribe_link")
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "已退订，后续不再向您发送营销邮件"})
}

// handleOutboundPurge GDPR 删除请求权执行口（≥3；body: email）。
// 只擦发送留痕与订阅行，抑制名单保留（「别再联系此人」的证据不许被删除请求洗掉）。
func (s *Server) handleOutboundPurge(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求体必须是 JSON"))
		return
	}
	purged, err := s.Store.PurgeCustomerOutboundTrail(tid, body.Email)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, 0, "marketing_purge", "outbound_trail", strings.ToLower(strings.TrimSpace(body.Email)))
	writeJSON(w, 200, map[string]interface{}{"success": true, "purged": purged})
}

// smtpLive 判断真实 SMTP 是否在位（MAIL_ENABLED=1 且 SMTP_HOST 非空）。
// 未在位时 sendOne 安全跳过并留痕——测试环境零外发（A16 的判据之一）。
func smtpLive() bool {
	return os.Getenv("MAIL_ENABLED") == "1" && os.Getenv("SMTP_HOST") != ""
}

// sendOne 外发的**唯一出口**（A17）：合规四件套 → 先占位 → SMTP → 终态回写。
// 返回 (result, reason)：result ∈ sent / failed / skipped。
// ★ 顺序硬口径：占位（ux_outbound_once）成功后才允许碰 SMTP；合规不过也必须占位（skip 终态），
//   保证该 (客户,节点) 不会被下一轮重复尝试。
func (s *Server) sendOne(ctx context.Context, tid int64, email, nodeKey, subject, body string) (string, string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || nodeKey == "" {
		return "skipped", "bad_params"
	}
	// ① opt-in：无记录/已退订一律不发。
	status, found, err := s.Store.ConsentStatus(tid, email)
	if err != nil {
		return "skipped", "consent_check_error"
	}
	if !found || status != store.ConsentOptedIn {
		return "skipped", "no_opt_in"
	}
	// ② 抑制名单（邮箱或域名命中即跳过）。
	sup, err := s.Store.InSuppression(tid, email)
	if err != nil {
		return "skipped", "suppression_check_error"
	}
	if sup {
		return "skipped", "suppressed"
	}
	// ③ 频限 freq_cap_per_day（独立配置键，A2/M0247）：当日已发+已占位数达上限即跳过。
	cap := int64(1)
	if raw, _ := s.Store.GetConfig(store.GuardOutboundConfigKey()); raw != "" {
		if n, e := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); e == nil && n > 0 {
			cap = n
		}
	}
	cnt, err := s.Store.DailySendCount(tid, email)
	if err != nil {
		return "skipped", "freq_check_error"
	}
	if cnt >= cap {
		return "skipped", "freq_capped"
	}
	// ④ SMTP 在位性先判：未配置真实邮件时安全跳过（测试环境零外发），同样占位防重试。
	if !smtpLive() {
		if rowID, first, cerr := s.Store.ClaimOutboundSend(tid, email, nodeKey); cerr == nil && first && rowID > 0 {
			_ = s.Store.MarkOutboundSkipped(tid, rowID, "smtp_disabled")
		}
		return "skipped", "smtp_disabled"
	}
	// ⑤ 先占位再发送：多实例并发只有一个赢家（ux_outbound_once）。
	rowID, first, err := s.Store.ClaimOutboundSend(tid, email, nodeKey)
	if err != nil {
		return "skipped", "claim_error"
	}
	if !first {
		return "skipped", "already_sent"
	}
	err = s.mailer().Send(&mail.Message{To: email, Subject: subject, Body: body})
	if err != nil {
		_ = s.Store.MarkOutboundFailed(tid, rowID, err.Error())
		observability.Warn(ctx, "外联邮件发送失败（已留可重试状态）", "tenant_id", tid, "node", nodeKey, "err", err)
		return "failed", "smtp_error"
	}
	if err := s.Store.MarkOutboundSent(tid, rowID); err != nil {
		observability.Warn(ctx, "外联发送成功但终态回写失败", "tenant_id", tid, "send_id", rowID, "err", err)
	}
	return "sent", ""
}

// runOutboundSequenceOnce 外联序列拨轮（watchdog「outbound」槽每小时拨一次；不做 @startup）。
// kill-switch＝outbound_enabled 显式 "1"；缺省关是 A18/M0249 的判据一半（翻缺省必红）。
// 每租户每节点对 opted-in 候选集逐个 sendOne；合规不过的都在 sendOne 内留 skip 终态。
func (s *Server) runOutboundSequenceOnce() {
	if s.Store == nil || s.Ten == nil {
		return
	}
	// kill-switch 先问，任何发送调用都不得排在它前面（A18 静态锁锚定本行）。
	if v, _ := s.Store.GetConfig("outbound_enabled"); v != "1" {
		return
	}
	tenants, err := s.Ten.List()
	if err != nil {
		observability.Warn(context.Background(), "外联拨轮读租户清单失败", "err", err)
		return
	}
	batch := int64(200)
	if bs, _ := s.Store.GetConfig("outbound_batch_size"); bs != "" {
		if n, e := strconv.ParseInt(strings.TrimSpace(bs), 10, 64); e == nil && n > 0 {
			batch = n
		}
	}
	ctx := context.Background()
	for _, t := range tenants {
		nodes, err := s.Store.ListSequenceNodes(t.ID)
		if err != nil || len(nodes) == 0 {
			continue
		}
		emails, err := s.Store.ListOptedInEmails(t.ID, batch)
		if err != nil {
			continue
		}
		for _, node := range nodes {
			if node.Enabled != 1 {
				continue
			}
			for _, email := range emails {
				s.sendOne(ctx, t.ID, email, node.NodeKey, node.Subject, node.Body)
			}
		}
	}
}
