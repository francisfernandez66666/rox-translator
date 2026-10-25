// ============ 本文件职责中文说明 ============
// auth_identity.go · 多身份账号方案（多身份账号方案_20261023.md）B2 批：
// 身份清单 / 身份切换两个端点，登录响应的 identities 数组，口令写点收口，
// 以及管理台「移除成员」的家行提升不变式。
//
// 一句话讲清这件事：以前「一个人 = users 表一行」，所以登录进来是谁就由那一行说了算；
// 现在允许一个人同时是「个人版用户」和「若干企业的成员」（每多一个身份就是多一行），
// 那么必须有一处告诉前端"你一共有几个身份"、有一处让人在同源凭证下换到另一个身份，
// 还要保证换身份这件事不越权（只有同一个账户的行能互相切）。
//
// 三条硬口径（改这一族前必读）：
//  1. **身份列表只有一个构造点** identitiesFor()：登录响应、GET /api/auth/identities、
//     POST /api/auth/switch-identity 三处出栈都走它。以前"同一件事写两遍，字段必然随改动漂移"
//     （loginUserPayload 那条注释的教训）在这里同样成立——三处各拼一份 map 迟早漂成三种字段。
//  2. **切换守卫只看账户归属，不看调用方嘴里的角色**：目标行必须与本行同 account_id，
//     且本行状态可用、目标行状态可用、目标租户未停用/未到期；
//     任何一条不成立一律回同一句 403「无权切换到该身份」。
//     刻意不区分"目标不存在 / 目标属于别人 / 目标租户已停用"——那是防枚举口径的一部分：
//     把三种失败分开回，等于给攻击者一条接口去探"哪些企业号存在"。
//  3. **口令只写家行**（D4/D5），setAccountPassword() 是所有口令写点的唯一收口：
//     profileSetup / 自助改密 / 管理台重置 / 登录 legacy 哈希升级四条腿一律走它。
//     按 (id,tid) 各写一遍会造出「家行新哈希、成员行旧哈希」，而库里留着的那份旧哈希
//     就是一条还能用的凭证（方案 §十三 R1 备选 A 的病根）。
//     ⚠️ 对尚未回填账户号的行（account_id 空／未迁移的老库／测试夹具）它**退回旧的单行写法**，
//     这是 B1 出厂判据「一人一行时行为逐字节等价」的落点，不是兼容壳。
//
// 【不含】邀请链接建号、注册分流、升级通道（B3）；前端切换器（B4）。
// ⚠️ 修改此处请同步更新本说明。
// ========================================
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
	"translator/internal/tenant"
)

// tenantBrief 身份出栈需要的租户侧最小读数（名字 + 是否个人版 + 状态）。
type tenantBrief struct {
	name       string
	isPersonal bool
	status     string
}

// identityPayload 一个可用身份的出栈结构（★ 唯一事实源，字段只在这里定义一次）。
//
// 为什么带 uid：切换端点按 tid 定位目标行，但前端渲染下拉时要给每一项一个稳定 key，
// 且后续动作（移除成员、申请权限）是按行操作的——只出 tid 就得多一次反查。
// 为什么带 tenant_status：个人版恒 active，企业行可能已停用/到期；前端据此把不可用的身份
// 置灰，而不是等人点下去才吃 403（后端守卫照旧必判，置灰只是少一次失败请求）。
type identityPayload struct {
	UID          int64  `json:"uid"`
	TID          int64  `json:"tid"`
	TenantName   string `json:"tenant_name"`
	IsPersonal   bool   `json:"is_personal"`
	Role         string `json:"role"`
	OrgName      string `json:"org_name"`
	IsHome       bool   `json:"is_home"`
	TenantStatus string `json:"tenant_status"`
}

// identitiesFor 列出一个账户的全部身份（家行在前）。
//
// 三种返回形态，各自对应一段真实数据：
//   - account_id 为空（迁移未覆盖的老行／夹具里的单行）⇒ 只回当前这一行，
//     形状与"改造前的单身份用户"逐字节等价，前端因此不会看到一个空身份的切换器；
//   - 正常账户 ⇒ 该账户全部身份行，逐行补齐租户名/是否个人版/部门名；
//   - 身份集合读失败或读到空 ⇒ 回当前这一行。
//     刻意不把读失败升级成登录失败：身份列表是锦上添花的那一半，
//     一次读抖动不该把人挡在登录页外（口令已经验过了）。
func (s *Server) identitiesFor(u *store.User) []identityPayload {
	if u == nil {
		return nil
	}
	if strings.TrimSpace(u.AccountID) == "" {
		return []identityPayload{s.identityPayloadFor(u, nil)}
	}
	rows, err := s.Store.ListIdentitiesByAccount(u.AccountID)
	if err != nil || len(rows) == 0 {
		return []identityPayload{s.identityPayloadFor(u, nil)}
	}
	// 同一次调用里同一租户会被问很多次（十个身份可能同属一家），这里各缓存一份读结果。
	cache := map[int64]tenantBrief{}
	out := make([]identityPayload, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.identityPayloadFor(row, cache))
	}
	return out
}

// identityPayloadFor 把一行 users 记录折成身份出栈结构。
// 参数 cache：租户读数缓存；传 nil 表示单次调用（未迁移行那条回落腿），不写缓存。
func (s *Server) identityPayloadFor(row *store.User, cache map[int64]tenantBrief) identityPayload {
	p := identityPayload{
		UID: row.ID, TID: row.TenantID, Role: row.Role,
		IsHome: row.IsHome == 1, TenantStatus: tenant.StatusActive,
	}
	if b, ok := s.tenantBriefFor(row.TenantID, cache); ok {
		p.TenantName = b.name
		p.IsPersonal = b.isPersonal
		p.TenantStatus = b.status
	}
	// row.TenantID<=0（平台级账号）或租户读不到 ⇒ 三个字段保持缺省：
	// 名字空串、不是个人版、状态 active。平台超管在切换器里是一条"平台身份"，
	// 刻意不借用 is_personal 标记，免得超管被前端读成个人版用户。
	if row.OrgID > 0 {
		if o, e := s.Store.GetOrgByID(row.OrgID); e == nil && o != nil {
			p.OrgName = o.Name
		}
	}
	return p
}

// tenantBriefFor 取一个租户的身份展示读数（带同调用内缓存）。
// 返回 false：该租户读不到（已删除/查询失败）或本就是平台级（tid<=0），调用方按缺省出栈。
func (s *Server) tenantBriefFor(tid int64, cache map[int64]tenantBrief) (tenantBrief, bool) {
	if tid <= 0 || s.Ten == nil {
		return tenantBrief{}, false
	}
	if cache != nil {
		if v, hit := cache[tid]; hit {
			// 空值也算命中（负缓存）：同一次调用里不必对同一台读失败反复重试。
			return v, v != tenantBrief{}
		}
	}
	t, err := s.Ten.GetByID(tid)
	if err != nil || t == nil {
		if cache != nil {
			cache[tid] = tenantBrief{}
		}
		return tenantBrief{}, false
	}
	b := tenantBrief{name: t.Name, isPersonal: t.IsPersonal, status: t.Status}
	if cache != nil {
		cache[tid] = b
	}
	return b, true
}

// handleIdentities 身份清单接口（GET/POST /api/auth/identities，★ 方案 §五）。
// 语义：拿当前会话所属账户的全部身份行；单身份账户也回一条（前端据此不渲染切换入口）。
// 鉴权：s.authUser —— 已登录态动作，未登录一律 401（前端 core.ts 据此走重登录链路）。
func (s *Server) handleIdentities(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "identities": s.identityListOrEmpty(u)})
}

// handleSwitchIdentity 身份切换接口（POST /api/auth/switch-identity，body {tid}，★ 方案 §五）。
//
// 白话：你已经用口令证明过"你是你"了，现在只是把会话从你名下的一个身份换到另一个身份，
// 所以这里**不再要口令**，要的是"目标行确实属于同一个账户"这一条守卫。
//
// 守卫四条，任一不成立回同一句 403（不区分失败原因，见文件头口径 2）：
//  1. 本会话自身状态可用（自助注销已过宽限的人不该还能换身份）；
//  2. 目标行的 account_id 与本行相同（同一人）；
//  3. 目标行状态可用（管理员停用了某企业身份 ⇒ 切不进去）；
//  4. 目标租户没被停用/到期（与 handleLogin 那条 P0-11 同尺：不再出现"能进去但处处失败"）。
//
// 成功：以目标行重签 24h JWT（新 (uid,tid) 对），回 token + user + identities；
// 审计 identity_switch（from_tid→to_tid）。
func (s *Server) handleSwitchIdentity(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		// 目标身份的租户号：个人版也可能有自己的租户号，所以这里不做 >0 前置校验，
		// 由"目标行必须在本账户身份集合里"那一条守卫出判决。
		TenantID *int64 `json:"tid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TenantID == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	const denied = "无权切换到该身份"
	if auth.EffectiveUserStatus(u.Status, u.DeactivatedAt) != store.UserActive {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, denied))
		return
	}
	// 未回填账户号的行只可能有自己这一行 ⇒ 切到本租户是"原地重签"，切到别的 tid 一律 403。
	targets, err := s.accountIdentityRows(u)
	if err != nil {
		// 读不到自己的身份集合是存储故障，不是越权：这里诚实回 500，
		// 不许把它折叠成 403——那会把一次数据库抖动写成"这个人没权限"，运维顺着权限排查一整晚。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	var target *store.User
	for _, row := range targets {
		if row.TenantID == *req.TenantID {
			target = row
			break
		}
	}
	if target == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, denied))
		return
	}
	if auth.EffectiveUserStatus(target.Status, target.DeactivatedAt) != store.UserActive {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, denied))
		return
	}
	if target.TenantID > 0 && s.Ten != nil {
		if t, terr := s.Ten.GetByID(target.TenantID); terr == nil && t != nil {
			switch t.Status {
			case tenant.StatusDisabled, tenant.StatusExpired:
				s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, denied))
				return
			}
		}
	}
	tok, serr := auth.Sign(target, 24*time.Hour)
	if serr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "签发失败"))
		return
	}
	// 审计归属**目标**租户（切进哪家就在哪家留痕），平台级账号归默认租户 1，与登录审计同口径。
	auditTID := target.TenantID
	if auditTID <= 0 {
		auditTID = 1
	}
	s.Store.LogAudit(auditTID, target.ID, "identity_switch", "auth",
		fmt.Sprintf("身份切换 %s→%s", tidLabel(u.TenantID), tidLabel(target.TenantID)))
	writeJSON(w, 200, map[string]interface{}{
		"success":    true,
		"token":      tok,
		"user":       loginUserPayload(target),
		"identities": s.identityListOrEmpty(target),
	})
}

// accountIdentityRows 取本会话账户的全部身份行（切换守卫的数据源）。
// 未回填账户号的行退化成"只有自己这一行"，保证一人一行时切换语义与改造前一致。
func (s *Server) accountIdentityRows(u *store.User) ([]*store.User, error) {
	if strings.TrimSpace(u.AccountID) == "" {
		return []*store.User{u}, nil
	}
	rows, err := s.Store.ListIdentitiesByAccount(u.AccountID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []*store.User{u}, nil
	}
	return rows, nil
}

// identityListOrEmpty 出栈用的身份数组（恒不为 nil ⇒ JSON 里恒是数组，不是 null）。
// 前端对 null 要各判一次（AGENTS §一·5 空页归一那族的同类坑），所以在服务端一次归掉。
func (s *Server) identityListOrEmpty(u *store.User) []identityPayload {
	ids := s.identitiesFor(u)
	if ids == nil {
		return []identityPayload{}
	}
	return ids
}

// personalTenantCode 自动补建（R4）的个人租户编码：一个账户最多一家。
//
// ★ 为什么用账户号派生而不是时间戳（注册链路用的是 p_<纳秒>）：
//
//	这一条挂在**登录出口**上，同一个人双击登录、或两台实例同时收到同一次登录，
//	会各自走到「这个账户还没有个人身份」的判定上。编码由账户号唯一决定 ⇒
//	第二枪在 tenants.code 的唯一索引上撞车、直接复用已存在那一家，
//	这是这条链上**唯一**的原子守卫（同 §一·10「占格必须原子、判据与计数同一条语句」那一族）。
//	时间戳编码做不到这一点：它会安静地长出两家空租户，而体验额度按 user_id 各发一笔＝一人双份补贴。
func personalTenantCode(accountID string) string {
	return "pa_" + strings.TrimSpace(accountID)
}

// ensurePersonalIdentityOnLogin 纯企业账户登录时自动补建个人身份行（★ 方案 §四／§十三 R4 拍板）。
//
// 白话：管理台邀请链接建出来的那种账号，一开始只有企业那一行（企业行就是他的家行）。
// 客户第一次登录进的是企业，但方案拍板「个人身份自此可经切换器选用」，
// 所以登录这一跳要顺手给他补一家个人版——口令仍只在家行（D4），会话仍落企业行。
//
// 六道前置守卫，任一不成立就**什么都不做**（登录本身照常成功）：
//  1. 只在家行上动手：切换后的成员会话、平台级账号（tid<=0）都不该凭空长出一家新租户；
//  2. 账户号必须已回填（未迁移的老行退回改造前的单身份形态，B1 出厂判据）；
//  3. 家行所在租户已是个人版 ⇒ 直接返回（这是 99% 的登录，一次租户读取就判完）；
//  4. 账户名下任何一行落在个人版租户 ⇒ 已补建过，返回（幂等腿）；
//  5. 家行租户读不到 ⇒ 不猜（拿不到事实就不写库，同 §一·13「清单取不到整条腿不判」）；
//  6. 任何一步失败只记 WARN、绝不把登录打成失败：口令已经验过了，
//     补建缺一次下一次登录还会再补（这条链上没有"错过就永远没有"的东西）。
//
// ★ 钱的口径（两条一起成立才算不放大免费额度）：
//   - 新行**继承家行的 created_by**：管理台批量导入的历史员工（created_by>0）本就
//     「不自动发放体验额度」（trial_auto.go 那条规则），补建个人身份不该成为绕过它的后门；
//   - 发放前问一次「这个账户拿到过真实补贴吗」（AccountHasRealTrialGrant）：
//     自助注册的企业管理员在注册那刻已经在自己企业租户上领过一份，
//     再补一家个人版又发一份＝一人双份。占位行（backfill／membership_no_trial，tokens=0）不算拿到过。
func (s *Server) ensurePersonalIdentityOnLogin(ctx context.Context, u *store.User) {
	if s == nil || s.Store == nil || s.Ten == nil || u == nil {
		return
	}
	if u.IsHome != 1 || u.TenantID <= 0 || strings.TrimSpace(u.AccountID) == "" {
		return
	}
	own, oerr := s.Ten.GetByID(u.TenantID)
	if oerr != nil || own == nil {
		return
	}
	if own.IsPersonal {
		return
	}
	rows, rerr := s.Store.ListIdentitiesByAccount(u.AccountID)
	if rerr != nil {
		observability.Warn(ctx, "补建个人身份前读取账户身份失败（本次跳过，下次登录再补）", "account", u.AccountID, "err", rerr.Error())
		return
	}
	cache := map[int64]tenantBrief{}
	for _, row := range rows {
		if b, ok := s.tenantBriefFor(row.TenantID, cache); ok && b.isPersonal {
			return
		}
	}

	// —— 以下才是真的"这个账户一个个人身份都没有"——
	code := personalTenantCode(u.AccountID)
	tid, terr := s.ensurePersonalTenant(ctx, u, code)
	if terr != nil {
		observability.Warn(ctx, "补建个人身份：个人租户没建起来（本次跳过）", "account", u.AccountID, "code", code, "err", terr.Error())
		return
	}
	// 成员行形态（is_home=0）：家仍在那条企业行，口令也只写家行（D4）。
	// role=tenant_admin：这家个人租户是他自己开的，与自助注册的个人用户同形（register.go 那条 else 支）。
	prow, cerr := s.Store.CreateIdentityRow(u.AccountID, tid, u.Username, u.DisplayName, store.RoleTenantAdmin, u.CreatedBy, 0, u.Email)
	if cerr != nil {
		// 撞唯一约束＝另一枪（并发登录／另一台实例）已经把这行建好了：那是幂等成功，不是故障。
		observability.Warn(ctx, "补建个人身份：个人身份行写入未成功（多为并发已建，下次登录自愈）", "account", u.AccountID, "tid", tid, "err", cerr.Error())
		return
	}
	// 默认 API Key：自助注册的个人用户拿到的是"登录即可用"的一家（register.go 的 issueDefaultAPIKeyFor）。
	// 挂在身份行**建成之后**——Key 的归属人是这一行本人，写家行 uid 会把用量记到企业身份上。
	s.issueDefaultAPIKeyFor(tid, prow.ID, "默认 Key")
	if already, aerr := s.Store.AccountHasRealTrialGrant(u.AccountID); aerr == nil && already {
		// 已经领过一份真实补贴 ⇒ 这一行也只落 tokens=0 的占位账，
		// 免得下一次谁把"补发腿"接到个人行上时又按"从没发过"补一份（与 markTrialNotGranted 同一把尺子）。
		s.markTrialNotGranted(ctx, prow)
	} else if aerr != nil {
		observability.Warn(ctx, "补建个人身份：账户既有补贴读数取不到（本次不发放，避免双份）", "account", u.AccountID, "err", aerr.Error())
		s.markTrialNotGranted(ctx, prow)
	} else {
		s.autoGrantUserTrial(ctx, prow)
	}
	observability.Info(ctx, "纯企业账户登录已自动补建个人身份（方案 R4）",
		"account", u.AccountID, "home_uid", u.ID, "home_tid", u.TenantID, "personal_uid", prow.ID, "personal_tid", tid)
}

// ensurePersonalTenant 取（或建）这一账户的个人租户，返回租户 ID。
//
// 建法与自助注册的个人用户**逐项同源**（register.go 那条 creatingPersonal 支）：
// 权限墙读运营策略默认档、句数镜像按同一折算率、默认 KB 包与余额账户照建、
// 行业落通用兜底——缺任何一项，这个补建出来的个人版就和客户自己注册的个人版长得不一样
// （最坏形态：能登录但没有默认术语包，翻译质量比同站其他个人用户差，而日志一行红都没有）。
//
// 并发/残留形态一律走"编码已存在就复用"：tenants.code 的唯一索引就是这条链的原子守卫。
func (s *Server) ensurePersonalTenant(ctx context.Context, u *store.User, code string) (int64, error) {
	if t, err := s.Ten.GetByCode(code); err == nil && t != nil {
		return t.ID, nil
	}
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		name = strings.TrimSpace(u.Username)
	}
	if name == "" {
		name = "个人用户"
	}
	trialTokens, _ := s.trialConfig()
	trialSentences := int64(0)
	if rate := s.Store.TokenSentenceRate(); rate > 0 {
		trialSentences = trialTokens / rate
	}
	lim := s.opsBaseEffective(0).Limits
	perms := &tenant.Perms{MaxDailyChars: lim.DefaultMaxDailyChars, MaxDailyTokens: lim.DefaultMaxDailyTokens}
	perms.SentenceBalance = trialSentences
	perms.PackageCode = "trial"
	pb, _ := json.Marshal(perms)
	t, err := s.Ten.Create(code, name, "", string(pb))
	if err != nil {
		if store.IsUniqueViolation(err) {
			// 另一枪刚建好同一编码：复用它的，别把并发登录打成两家人。
			if exist, e2 := s.Ten.GetByCode(code); e2 == nil && exist != nil {
				return exist.ID, nil
			}
		}
		return 0, err
	}
	// 个人标记是与自助注册同一条硬口径（is_personal 决定邀请奖励、额度语义与切换器上的名字）；
	// 失败必须出声——2026-09-12 那批就是被 `_ =` 吞掉导致个人标记恒失败、奖励全量停发且无痕。
	if perr := s.Ten.SetPersonal(t.ID, true); perr != nil {
		observability.Warn(ctx, "补建个人身份：个人租户标记失败", "tid", t.ID, "err", perr.Error())
	}
	_ = s.Store.EnsureDefaultPackages(t.ID)
	if pkg, ierr := s.Store.FindIndustryByCode(store.GeneralIndustryCode); ierr == nil && pkg != nil {
		_ = s.Ten.SetIndustry(t.ID, pkg.Code)
	}
	_ = s.Store.EnsureBalance(t.ID)
	return t.ID, nil
}

// tidLabel 租户号的可读写法（审计 detail 用；0 写作 platform，别留一个裸 0 让人猜）。
func tidLabel(tid int64) string {
	if tid <= 0 {
		return "platform"
	}
	return fmt.Sprintf("tid=%d", tid)
}

// credentialRowFor 取"这一行该拿哪一份口令哈希去验原密码"（★ 方案 D4：成员行不含凭证）。
//
// 未迁移行（account_id 空）与家行都直接返回自己——旧行为，逐字节等价。
// 成员行返回该账户的家行；家行读不到（数据被并坏）时返回原行，
// 让调用方按"这行没有口令"的既有分支走（回「请先获取邮箱验证码」），
// 而不是把一次读失败冒充成"原密码错误"——那会把运维引去让人改口令，而真因是那账户没有家。
func (s *Server) credentialRowFor(u *store.User) *store.User {
	if u == nil || strings.TrimSpace(u.AccountID) == "" || u.IsHome == 1 {
		return u
	}
	home, err := s.Store.GetHomeRow(u.AccountID)
	if err != nil || home == nil {
		return u
	}
	return home
}

// setAccountPassword 口令写入唯一收口（★ 方案 D4/D5）。
//
// 参数：target=要写入口令的那一行（用它定位账户的家行）、passHash=bcrypt 哈希、
// keepForceChange=是否保留家行**原有**的首登标记（强制改密闸 + 资料补全提醒）。
//   - 自助改密／资料补全传 false：用户亲手设过口令了，这两把闸该当场关
//     （SetPasswordForAccount 本来就会清掉它们，这里什么都不用补）；
//   - legacy 哈希透明升级、管理台代客重置传 true：这两次都**不是**用户亲手换口令，
//     只是把同一句口令换个哈希形态存 / 管理员替成员设一个初始口令。
//     不补这一腿就会把「首登必须自己改密」的闸洗掉——批量导入的初始口令正是靠它兜住的
//     （旧形态 ResetPassword 根本不碰这两个标记，所以这是"收口"带来的新语义，必须显式补回）。
//
// 成员行（is_home=0）不含凭证（D4）：管理员对成员行重置口令时，写的仍然是这个账户的**家行**，
// 因为登录永远家优先验口令——按成员行写会在库里多留一份可用哈希，那正是 R1 备选 A 的病根。
//
// 返回真实受影响行数（0 行必须判失败，AGENTS §一·8②）。
func (s *Server) setAccountPassword(target *store.User, passHash string, keepForceChange bool) (int64, error) {
	if target == nil {
		return 0, fmt.Errorf("待写入口令的账号不存在")
	}
	// 未迁移行（account_id 空）退回旧的按行写法：与改造前逐字节等价（B1 出厂判据）。
	if strings.TrimSpace(target.AccountID) == "" {
		return s.Store.ResetPassword(target.ID, target.TenantID, passHash)
	}
	// 代客重置要保留家行**原有**的标记，所以先读原值再写（写完再读只能读到被清后的 0）。
	var prev *store.User
	if keepForceChange {
		if home, herr := s.Store.GetHomeRow(target.AccountID); herr == nil && home != nil {
			prev = home
		}
	}
	n, err := s.Store.SetPasswordForAccount(target.AccountID, passHash)
	if err != nil || n == 0 || prev == nil {
		return n, err
	}
	// 把两把闸抬回原值（写失败不改判这次口令写入：口令已经真写进库了，标记只是首登提醒）。
	if prev.MustChangePwd > 0 {
		_ = s.Store.SetMustChangePwd(prev.ID, prev.TenantID, 1)
	}
	if prev.ProfileSetupRequired > 0 {
		_ = s.Store.SetProfileSetupRequired(prev.ID, prev.TenantID, 1)
	}
	return n, err
}

// promoteHomeBeforeRemoval 移除成员前的家行提升（★ 方案 §6.2 + R8/R2 拍板）。
//
// 场景很具体：纯企业用户（管理台邀请的新邮箱，企业行就是他的家行）被管理员移除 ⇒
// 停用的正是家行。若直接停用，这个账户就"有可用行却没有家行"，
// 而 D2 不变式规定账户只要有可用行就必须有且仅有一个家行，家优先解析也就无行可取。
// 所以这里先把该账户的另一行（个人行，若存在）提升为家，再停用被移除那一行。
//
// 判据与失败取向：
//   - 被移除的行不是家行 ⇒ 什么都不做（多数情形：个人版是家、企业行是成员行）；
//   - 账户没有别的行 ⇒ 也什么都不做（这个人只有这一行，停用后没有可用身份，本来就不需要家）；
//   - 提升失败 ⇒ 只记 WARN、**不阻断移除操作**：管理员撤身份是明确的管理动作，
//     不该因为一次家标记搬迁失败而被卡住；不变式由 MultiIdentityMigrate 的
//     reconcileHomeRows 在下次启动兜底（数据层已有那条收敛腿）。
func (s *Server) promoteHomeBeforeRemoval(ctx context.Context, row *store.User) {
	if row == nil || row.IsHome != 1 || strings.TrimSpace(row.AccountID) == "" {
		return
	}
	rows, err := s.Store.ListIdentitiesByAccount(row.AccountID)
	if err != nil || len(rows) < 2 {
		return
	}
	// 提升候选＝除本行外第一条**可用**身份（按 ListIdentitiesByAccount 的既有顺序取，
	// 家行已被本行占着，所以第二条起都是成员行；个人行按 D1 回填顺序通常就是它）。
	for _, cand := range rows {
		if cand.ID == row.ID {
			continue
		}
		if auth.EffectiveUserStatus(cand.Status, cand.DeactivatedAt) != store.UserActive {
			continue
		}
		if perr := s.Store.PromoteHomeRow(row.AccountID, cand.ID); perr != nil {
			observability.Warn(ctx, "移除成员前家行提升失败", "account", row.AccountID, "target_row", cand.ID, "err", perr.Error())
			return
		}
		return
	}
}
