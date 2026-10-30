// ============ assist_profile.go · 职责说明 ============
// 挂件访客画像的**主服务侧**：按已校验的会话组装画像 → HMAC 签名 → 放进三个内部头，
// 只为 `/assist-api/*` 这一跳服务（★ 2026-10-30 三事合一批 组7 §8.2，路 A′／D-14）。
//
// 为什么放在转发层而不是 assist 侧查库：
//
//	assist 是独立部署单元，它没有主站会话、也不该持有主库凭据（见 system_values.go 文件头第 1 条）。
//	主服务这一跳手里已经有 `authUser(r)` 校验过的用户，正好在同一跳里把「这个人是谁」变成
//	一份**签名事实**交出去；assist 只验签、不查库。
//
// 四条必须遵守的口径（逐条落成断言）：
//
//	P-1 单一事实源：余额/已用/套餐/宽限一律调客户面在用的同一函数
//	    （balancePayload／GetTenantPerms／subscriptionGraceState／PlatformTaskTypeExclPred＋日/月边界／
//	    RateDayLoad），**不许**在画像里另算一份；
//	P-2 PII 最小化：邮箱默认脱敏，不带 token/密码/手机/地址/证件/API Key；
//	P-4 每轮现组装：本文件不设任何缓存，uid 绑定字段更不许跨请求留存；
//	P-7 头不可伪造：`handleAssistOpenProxy` 进来第一件事就把入站同名头删掉（见 stripAssistProfileHeaders），
//	    本文件只写**出站**请求头。
//
// 未配置密钥（env 与库都空）⇒ 不发画像头，回落匿名，不报错：画像是一个增强，不是可用性前提。
// =============================================
package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"translator/internal/assist/profile"
	"translator/internal/db"
	"translator/internal/secret"
	"translator/internal/store"
)

// assistProfileKeyEnv 环境变量名（env > 库，与全仓配置优先序一致）。
const assistProfileKeyEnv = "ASSIST_PROFILE_KEY"

// assistProfileKeyConfig 库内键名。值按 `internal/secret` 密文存（enc:v1: 前缀），
// DecryptSecret 兼容历史明文；管理台回显走掩码时不得把掩码写回（写侧不在本文件）。
const assistProfileKeyConfig = "assist_profile_key"

// assistProfileFullEmailConfig 邮箱全量开关（默认关）。打开它会把完整邮箱送进第三方模型提示词，
// 只有运营明确接受这个风险时才允许；默认永远走脱敏。
const assistProfileFullEmailConfig = "assist_profile_full_email"

// stripAssistProfileHeaders 删除入站请求上的三个画像头（P-7）。
// 客户端自带一份伪造头时，这一步保证它到不了上游；随后的 attachAssistProfile 只按**本进程**的
// 会话结论自赋——「先删再自赋」是这条链的唯一形态。
func stripAssistProfileHeaders(h http.Header) {
	if h == nil {
		return
	}
	h.Del(profile.HeaderProfile)
	h.Del(profile.HeaderTs)
	h.Del(profile.HeaderSig)
}

// assistProfileKey 解析共享签名密钥（env > system_config，库里是密文）。
// 返回 nil 表示未配置——调用方据此不发画像头。
func (s *Server) assistProfileKey() []byte {
	if v := strings.TrimSpace(os.Getenv(assistProfileKeyEnv)); v != "" {
		return []byte(v)
	}
	if s.Store == nil {
		return nil
	}
	v, err := s.Store.GetConfig(assistProfileKeyConfig)
	if err != nil {
		return nil
	}
	v = strings.TrimSpace(secret.DecryptSecret(strings.TrimSpace(v)))
	if v == "" || secret.IsSecretMasked(v) {
		// 掩码串是**管理台回显形态**，绝不能当密钥用：拿它签出来的头验签必然失败，
		// 而症状是"画像静默不生效"——比直接不发更难查。
		return nil
	}
	return []byte(v)
}

// attachAssistProfile 若本次请求是登录用户且密钥已配置，则把画像签名头写到出站请求上。
// 任何一环取不到（未登录/未配密钥/序列化失败）都**静默不发**：匿名是合法回落，不是错误。
func (s *Server) attachAssistProfile(dst *http.Request, src *http.Request) {
	if dst == nil || src == nil {
		return
	}
	key := s.assistProfileKey()
	if len(key) == 0 {
		return
	}
	u := s.authUser(src)
	if u == nil || u.ID <= 0 {
		return
	}
	v := s.buildVisitorProfile(src, u)
	if v == nil {
		return
	}
	payload, err := profile.Encode(v)
	if err != nil {
		return
	}
	ts := time.Now().Unix()
	dst.Header.Set(profile.HeaderProfile, payload)
	dst.Header.Set(profile.HeaderTs, strconv.FormatInt(ts, 10))
	dst.Header.Set(profile.HeaderSig, profile.Sign(key, payload, ts))
}

// buildVisitorProfile 按 P-1 表现组装一份画像。所有数值均来自客户面同一读点，本函数只做投影，
// 不做任何金额/日历计算（那是这些读点自己的职责）。
func (s *Server) buildVisitorProfile(r *http.Request, u *store.User) *profile.Visitor {
	if s == nil || s.Store == nil || u == nil || u.ID <= 0 {
		return nil
	}
	tid := s.effTenant(r, u)
	v := &profile.Visitor{
		UID:           u.ID,
		AccountID:     u.AccountID,
		DisplayName:   strings.TrimSpace(u.DisplayName),
		PreferredLang: "",
		Status:        u.Status,
		IsPersonal:    false,
	}
	// 职业：编码 → 角色包展示名（编码如 fullstack 不能念给客户）。取不到就回落编码本身。
	v.JobRole = strings.TrimSpace(u.JobRole)
	if v.JobRole != "" {
		if p, err := s.Store.FindEnabledPersonaByCode(v.JobRole); err == nil && p != nil && strings.TrimSpace(p.Name) != "" {
			v.JobRole = strings.TrimSpace(p.Name)
		}
	}
	if pl, err := s.Store.GetPreferredLang(u.ID); err == nil {
		v.PreferredLang = strings.TrimSpace(pl)
	}
	// 邮箱：默认脱敏；只有运营显式打开开关时才出全量（P-2）。
	if full, _ := s.Store.GetConfig(assistProfileFullEmailConfig); isTruthyConfig(full) {
		v.Email = strings.TrimSpace(u.Email)
	} else {
		v.Email = profile.MaskEmail(u.Email)
	}

	// 租户与行业：行业只出**展示名**（FindIndustryByCode 返回的包名），不把 code 念给客户。
	if tid > 0 && s.Ten != nil {
		if t, err := s.Ten.GetByID(tid); err == nil && t != nil {
			v.TenantName = strings.TrimSpace(t.Name)
			v.IsPersonal = t.IsPersonal
			if code := strings.TrimSpace(t.Industry); code != "" {
				v.Industry = code
				if pkg, ierr := s.Store.FindIndustryByCode(code); ierr == nil && pkg != nil && strings.TrimSpace(pkg.Name) != "" {
					v.Industry = strings.TrimSpace(pkg.Name)
				}
			}
		}
	}
	if tid <= 0 {
		return v // 平台上下文（超管）没有余额/套餐语义，画像到此为止
	}

	// 付费状态与套餐：与收银台/订阅页同一读点。
	v.BillingEnforced = s.Bill != nil && s.Bill.Enabled()
	if perms, err := s.Store.GetTenantPerms(tid); err == nil && perms != nil {
		v.PackageCode = perms.PackageCode
		v.PackageExpires = perms.PackageExpires
		v.AutoRenew = perms.AutoRenew
		v.InGrace, v.GraceExpires = subscriptionGraceState(perms)
	}
	// 余额：balancePayload 的 total 再经 PointsFromTokens 折算——与 /api/me/package 的
	// points_balance 是同一把尺子（G29 的等值腿）。
	_, _, tokens, _ := s.balancePayload(tid)
	v.PointsBalance = s.Store.PointsFromTokens(tokens)

	// 今日/本月已用：谓词与日历边界都引用 store 里的常量/出口，api 层不自己算（AGENTS §一·11）。
	var usedToday, usedMonth int64
	if s.Store.DB() != nil {
		_ = db.QueryRow(s.Store.DB(), db.CurrentDialect(),
			"SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=? AND created_at>=? AND "+store.PlatformTaskTypeExclPred,
			tid, store.MonthStartBound()).Scan(&usedMonth)
		_ = db.QueryRow(s.Store.DB(), db.CurrentDialect(),
			"SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=? AND user_id=? AND created_at>=? AND "+store.PlatformTaskTypeExclPred,
			tid, u.ID, store.DayStartBound()).Scan(&usedToday)
	}
	v.PointsUsedToday = s.Store.PointsFromTokens(usedToday)
	v.PointsUsedMonth = s.Store.PointsFromTokens(usedMonth)

	// 撞墙脚印（§4.1 A-5 的 free_wall 桶）：只出布尔，绝不带任何额度数字。
	if st, err := s.Store.RateDayLoad(store.RateScopeFreeWall, strconv.FormatInt(u.ID, 10)); err == nil {
		v.HitWall = st.Used > 0
	}

	// 24 小时内有已支付订单 ⇒ 充值意图抑制第一档的输入（R-3）。
	v.RechargedRecently = s.rechargedWithin24h(tid, u.ID)

	// 任务奖励进度：只出「今天/本周领了几次」，不含奖励 token 数（P-2 / I-3 同族）。
	for _, tv := range s.Store.ListUserTaskViews(u.ID) {
		if tv == nil || tv.Reward == nil {
			continue
		}
		v.TaskRewards = append(v.TaskRewards, profile.TaskReward{
			Title: strings.TrimSpace(tv.Title),
			Today: tv.Reward.TodayCount,
			Week:  tv.Reward.WeekCount,
			Total: tv.Reward.TotalCount,
		})
		if len(v.TaskRewards) >= 5 {
			break
		}
	}
	return v
}

// rechargedWithin24h 读最近 24h 内本人已支付订单是否存在。
// 走 orders 表而不是 payments：orders 有 created_by（下单人）与 paid_at（到账时刻），
// 而 payments 只有租户维度，分不出「同租户里是谁刚充的」。
func (s *Server) rechargedWithin24h(tid, uid int64) bool {
	if tid <= 0 || uid <= 0 || s.Store == nil || s.Store.DB() == nil {
		return false
	}
	since := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	var n int64
	_ = db.QueryRow(s.Store.DB(), db.CurrentDialect(),
		"SELECT COUNT(*) FROM orders WHERE tenant_id=? AND created_by=? AND status='paid' AND paid_at>=?",
		tid, uid, since).Scan(&n)
	return n > 0
}

// isTruthyConfig 配置开关键判定（与仓内其它 bool 配置同一口径：1/true/on/yes）。
func isTruthyConfig(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}
