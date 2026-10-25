// ============ register_identity.go · 职责说明 ============
// 多身份账号方案 B3 §6.1「注册口汇流」：企业凭据（管理台手输邀请码 / 专属邀请链接 / 专属域名码）
// 在注册这一口的**解析、分流、建号**三件事收在本文件，register.go 只在咽喉处调三次函数。
//
// 为什么单独立一个文件而不是往 handleRegister 里加：handleRegister 已 670 行、七道闸串在一条链上，
// 而 §6.1 的分流是**六种邮箱命中情形 × 三种凭据形态**的矩阵。写在同一处，下一个改注册的人
// 必须先读完整个矩阵才敢动 IP 档——而矩阵本身也迟早和 IP 档漂开（本仓「同一件事写两遍迟早打架」那一族）。
//
// 四条硬口径（改这一腿前必读）：
//
//	① **只读解析 / 只读分流 / 烧码单点**：resolveEnterpriseCredential 与 planEnterpriseJoin
//	   都不写库；MarkInviteCodeUsed 唯一发生点留在 register.go，位置在「防薅占格成功 + 分流判定通过」之后。
//	   两条理由：AGENTS §一·10 明写「防薅判定必须发生在 MarkInviteCodeUsed 之前，拒绝不许烧掉客户自己发的邀请码」；
//	   §6.1 的三条拒绝分支（已在该企业／注销宽限期／他人占用）每一条都发生在烧码之前。
//	② **状态判定只有一把尺子**：拒绝理由直接取 store.inviteStatusOf 派生出的 InviteCode.Status
//	   （revoked > used > expired > active），**禁止**在 API 层再比一次 used/revoked/expires_at。
//	   旧形态三处各判一次（:359、:368、:376），G9 加撤销位时就得跟着改三处——那是迟早漂的写法。
//	③ **防枚举的先后**（P0-9 同族）：先证明邮箱归你（本次注册验码通过／本人空白号 JWT），
//	   才告诉你它被占用；没证明时一律回同一句 409「该邮箱已被其他账号绑定」，
//	   与挪位那一批的对外口径逐字一致——新文案不得泄露"这个邮箱在我们哪一家企业里"。
//	④ **§6.3 删除静默降级**：只要请求带了企业凭据串，就不许再有任何"降级为个人用户"或
//	   "忽略邀请码、另建一家企业"的第三条路；每张无效码各给一句可行动文案，
//	   而不是一句「无效」把人反复推回同一个坏链接（byd 案例那族语义混淆就是模糊文案养出来的）。
//
// ★ 方言：本文件不建表、不写裸 SQL，读写全经 store/iam 的 db.CurrentDialect() 分支，SQLite/PG 天然等价。
// ==========================================================
package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
)

// entCredential 一枚企业凭据在注册口的解析结果。
// ★ 只读数——本结构由 resolveEnterpriseCredential 产出，任何写库动作都不发生在它身上。
type entCredential struct {
	Code       string // 规范化后的凭据串（烧码与审计 detail 用它，不用 req.Invite 原文）
	RowID      int64  // invite_codes 主键（MarkInviteCodeUsed 的入参）
	FromLink   bool   // true=专属邀请链接（kind='link'）/ false=管理台手输码
	TenantID   int64  // 目标企业租户（恒 >0：tenant_id=0 的老语义在解析期就被拒）
	OrgID      int64  // 预绑部门（0=未分配；落库前还要过 validateOrg）
	Role       string // 预绑角色（落库前还要过 joinableRole）
	BoundEmail string // 链接锁定的邮箱（非空 ⇒ 注册邮箱必须与它等值）
	Source     string // 审计来源标记：invite_code / invite_link
}

// resolveEnterpriseCredential 注册口的凭据解析（§6.1 汇流点 + §6.3 显式报错）。
//
// 三种返回值形态，调用方据此分派：
//   - (nil, nil)   ⇒ 这次注册与企业凭据**无关**（无凭据串，或 role_choice=admin 表明"我要新建企业"），
//     走既有个人/建企业分支；
//   - (cred, nil)  ⇒ 走 §6.1 分流建企业身份；
//   - (nil, err)   ⇒ 显式拒绝（★ 这里是 §6.3「删除静默降级」的落点：旧形态把无效码悄悄改成
//     "降级为个人用户"，把"没带 role_choice 的码"悄悄当成没带——用户以为自己进了企业，实际凭空多一家公司。
//     注释与代码相反比代码本身错更危险：下一个人照注释做客户答复，就会在线上架空一道闸（F-77 那条教训））。
//
// 参数 email 用于链接锁邮箱校验（§6.2「预绑邮箱是最强绑定」）；dedicatedTid 为品牌子域解析结果。
func (s *Server) resolveEnterpriseCredential(ctx context.Context, invite, reqType, roleChoice, email string, dedicatedTid int64) (*entCredential, *apierrors.APIError) {
	code := strings.TrimSpace(invite)
	if dedicatedTid > 0 {
		if code == "" {
			// F-77 的 A 腿钉着这条：文案必须点名「邀请码」（否则客户不知道去哪儿要码）。
			return nil, apierrors.New(apierrors.ErrValidation, "请填写企业邀请码后再注册")
		}
		cred, apiErr := s.loadEnterpriseCredential(ctx, code)
		if apiErr != nil {
			return nil, apiErr
		}
		// 品牌子域＝品牌方自己的门：码必须出自**这一家**（跨租户投毒＝外人混进企业数据面）。
		// 句子逐字沿用旧文案——register_dedicated_invite_test.go 的 B 腿按「该企业」断言，
		// 而它同时也是给客户看的那句"这个码不是你们公司的"。
		if cred.TenantID != dedicatedTid {
			return nil, apierrors.New(apierrors.ErrValidation, "邀请码无效、已使用或不属于该企业")
		}
		return s.lockBoundEmail(cred, email)
	}
	if code == "" {
		// role_choice=member 却没带码：旧形态在这里**悄悄把用户改成个人用户**（§6.3 禁的第二条静默路）。
		// 前端 Login.tsx:456 本来就把这种提交挡在表单里（auth.inviteRequired），所以这一句 400
		// 打的是脚本与旧缓存包，不是真人路径；放行的只有 admin 那一档（下面 role_choice=admin 分支）。
		if strings.EqualFold(strings.TrimSpace(roleChoice), "member") {
			return nil, apierrors.New(apierrors.ErrValidation, "请填写企业邀请码后再注册")
		}
		return nil, nil
	}
	// type=personal 却带企业凭据：旧形态静默清掉 req.Invite 当没看见（§6.3 禁的正是这个）。
	// 现网前端不会发出这种组合（Login.tsx 的企业邀请码字段只在企业分支出现，AiRegisterFlow 只在 staff/补全分支带码），
	// 所以这里翻成 400 打的是脚本与手工请求，不是真人路径。
	if strings.EqualFold(strings.TrimSpace(reqType), "personal") {
		return nil, apierrors.New(apierrors.ErrValidation, "个人注册不能使用企业邀请码")
	}
	// role_choice=admin＝「我是管理员，要新建企业」：这是"凭据与本次意图无关"的唯一合法组合，
	// 沿用旧行为忽略它（★ 不烧码：烧掉一张客户本要用的码＝客户资产损失），建企业分支照走。
	if strings.EqualFold(strings.TrimSpace(roleChoice), "admin") {
		return nil, nil
	}
	cred, apiErr := s.loadEnterpriseCredential(ctx, code)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.lockBoundEmail(cred, email)
}

// loadEnterpriseCredential 读一枚凭据并给出**可行动**的拒绝文案（只读，不烧码）。
//
// 两次读是有意的：GetInviteLinkByCode 已经把「kind 必须是 link」收在数据层，
// 在 API 层再比一次 row.Kind=="link" 字面量就是第二把尺子（invites_link.go:139 一改，这里迟早不跟）。
// 注册不是热路径，多一次查询换判据单点，值。
func (s *Server) loadEnterpriseCredential(ctx context.Context, code string) (*entCredential, *apierrors.APIError) {
	row, err := s.Store.GetInviteLinkByCode(code)
	if err != nil {
		observability.Warn(ctx, "注册口读取邀请链接失败", "err", err.Error())
		return nil, apierrors.New(apierrors.ErrInternal, "注册校验失败，请稍后重试")
	}
	fromLink := row != nil
	if !fromLink {
		row, err = s.Store.GetInviteCodeByCode(code)
		if err != nil {
			// 「码不存在」不是故障：外人可以拿任意串来打这一口，ErrNoRows 必须走文案而不是 500。
			if errors.Is(err, sql.ErrNoRows) {
				return nil, apierrors.New(apierrors.ErrValidation, credentialRejectText("invalid", false))
			}
			observability.Warn(ctx, "注册口读取邀请码失败", "err", err.Error())
			return nil, apierrors.New(apierrors.ErrInternal, "注册校验失败，请稍后重试")
		}
	}
	// 五态判定只读这一把尺子（见文件头②）：Status 由 store.scanInviteCode 统一派生。
	if row.Status != "active" {
		return nil, apierrors.New(apierrors.ErrValidation, credentialRejectText(row.Status, fromLink))
	}
	if row.TenantID <= 0 {
		// tenant_id=0 是历史「凭码新建独立租户」那一档，它不指向任何一家企业。
		// 拿它进门＝凭空进一间不存在的公司；这里显式拒绝，绝不静默退化成"给你新建一家"。
		return nil, apierrors.New(apierrors.ErrValidation, "该邀请码未绑定企业，无法用于加入")
	}
	source := "invite_code"
	if fromLink {
		source = "invite_link"
	}
	return &entCredential{Code: code, RowID: row.ID, FromLink: fromLink, TenantID: row.TenantID,
		OrgID: row.OrgID, Role: row.Role, BoundEmail: row.BoundEmail, Source: source}, nil
}

// lockBoundEmail 链接预绑邮箱校验（§6.2：填了 bound_email 就是最强绑定）。
// 转发出去的门禁串不该让另一个人领走一个已指名员工的身份。
func (s *Server) lockBoundEmail(cred *entCredential, email string) (*entCredential, *apierrors.APIError) {
	if be := strings.TrimSpace(cred.BoundEmail); be != "" && !strings.EqualFold(be, strings.TrimSpace(email)) {
		return nil, apierrors.New(apierrors.ErrValidation, "该邀请链接只能由指定邮箱注册，请更换邮箱或向管理员重新获取")
	}
	return cred, nil
}

// credentialRejectText 凭据无效原因 → 中文可行动文案（★ 每档点名"下一步做什么"）。
// fromLink 直接复用 inviteLinkReasonText 那四档既有句子：管理台链接落地页已在用同一把尺子，
// 再造第二份"链接已过期"的措辞＝同一个坏链接在两个面上给出两种提示（迟早有人把它们改到不同口径）。
func credentialRejectText(reason string, fromLink bool) string {
	if fromLink {
		return inviteLinkReasonText(reason)
	}
	switch reason {
	case "used":
		return "该邀请码已被使用，请向企业管理员重新获取"
	case "revoked":
		return "该邀请码已被管理员撤销，请向企业管理员重新获取"
	case "expired":
		return "该邀请码已过期，请向企业管理员重新获取"
	default:
		// 「码不存在」与旧文案合并（历史契约：register.go 一直用这句，UAT 与 SDK 断言都指向它）。
		return "邀请码无效或已使用"
	}
}

// joinAction §6.1 分流的三种落位形态。
type joinAction string

// 三档各对应一条注册链的落位形态；判定本身纯读数（不写库），写侧只按 Action 分派一次。
const (
	joinNewHome      joinAction = "new_home"      // 纯企业用户：企业行就是家行（is_home=1、口令自设）
	joinCompleteHome joinAction = "complete_home" // §七 升级通道：家行还是空白号 ⇒ 顺带补全 + 建企业行
	joinMemberRow    joinAction = "member_row"    // 本人已有已补全的 home 行 ⇒ 只建企业成员行（家不动）
)

// joinPlan 分流判定结果（★ 纯读数，不含写库副作用）。
type joinPlan struct {
	Action    joinAction
	AccountID string      // 挂靠的账户号（joinNewHome 为空：这一行自己就是新账户）
	Home      *store.User // 命中的家行（joinNewHome 为 nil）
}

// planEnterpriseJoin §6.1 邮箱命中分流。
//
// 六条分支按方案表格逐条落地，判据次序本身是防枚举的一部分：
//
//	① 目标企业里这个邮箱已被占用（按租户那把尺子 D3）：
//	   deactivating ⇒ 「账号注销宽限期内，暂不能加入企业」；active ⇒ 「你已在该企业中，可直接切换身份」
//	   （退出过的行是 disabled，两个查询都看不见 ⇒ 自然落到"建新行"，即 R3「重新加入建新行」）；
//	② 别处一家都没有这个邮箱 ⇒ joinNewHome（建企业行并把它当首页，D2 一人一个家）；
//	③ 任一行处于注销宽限期 ⇒ 与 ① 同判（这个人正在退场，此时加入等于给他续一段新身份）；
//	④ 命中家行但账户号为空 ⇒ 回 409（MultiIdentityMigrate 没覆盖到这一行；没有账户号就无法挂成员行，
//	   硬建会造出一行无主身份——与改造前的全局邮箱唯一腿给出的判决逐字等价，不是新行为）；
//	⑤ 家行是空白号（无口令 + 待补全标记） ⇒ joinCompleteHome（§七：不停用、不释放邮箱）；
//	⑥ 其余 ⇒ joinMemberRow（家行留着，口令只存家行 D4）。
//
// 参数 emailProven=本次注册验码通过；caller=带 JWT 时的登录人（升级通道的等价凭据）。
func (s *Server) planEnterpriseJoin(ctx context.Context, email string, tid int64, caller *store.User, emailProven bool) (*joinPlan, *apierrors.APIError) {
	// 「本人」的两条等价凭据（§七 判定扩展）：验码通过，或本人 JWT 且邮箱等值（登录 purpose 已证明收件箱）。
	proven := emailProven || (caller != nil && strings.EqualFold(caller.Email, email))
	if row, err := s.Store.GetUserByEmailInTenant(email, tid); err == nil && row != nil {
		if row.Status == store.UserDeactivating {
			return nil, joinOccupiedError(proven, "账号注销宽限期内，暂不能加入企业")
		}
		return nil, joinOccupiedError(proven, "你已在该企业中，可直接切换身份")
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		observability.Warn(ctx, "注册口按租户查邮箱占用失败", "tid", tid, "err", err.Error())
		return nil, apierrors.New(apierrors.ErrInternal, "注册校验失败，请稍后重试")
	}
	rows, err := s.Store.ListAccountsByEmail(email)
	if err != nil {
		observability.Warn(ctx, "注册口列账户失败", "err", err.Error())
		return nil, apierrors.New(apierrors.ErrInternal, "注册校验失败，请稍后重试")
	}
	if len(rows) == 0 {
		return &joinPlan{Action: joinNewHome}, nil
	}
	for _, r := range rows {
		if r.Status == store.UserDeactivating {
			return nil, joinOccupiedError(proven, "账号注销宽限期内，暂不能加入企业")
		}
	}
	home := rows[0] // ListAccountsByEmail 已按「家行在前」排序（与 GetUserByEmail 同一条 ORDER BY）
	if !proven {
		// 未证明邮箱归你 ⇒ 与改造前同一句 409，不透露"是谁/在哪家"（P0-9 防枚举口径不倒退）。
		return nil, apierrors.New(apierrors.ErrConflict, "该邮箱已被其他账号绑定")
	}
	if strings.TrimSpace(home.AccountID) == "" {
		observability.Warn(ctx, "注册分流：命中的行没有账户号（MultiIdentityMigrate 未覆盖）", "uid", home.ID, "tid", home.TenantID)
		return nil, apierrors.New(apierrors.ErrConflict, "该邮箱已被其他账号绑定")
	}
	if home.PasswordHash == "" && home.ProfileSetupRequired == 1 {
		return &joinPlan{Action: joinCompleteHome, AccountID: home.AccountID, Home: home}, nil
	}
	return &joinPlan{Action: joinMemberRow, AccountID: home.AccountID, Home: home}, nil
}

// joinOccupiedError 同一条占用事实的两种说法（★ 分档只看"邮箱是不是你的"，不看占用者是谁）。
// 本人 ⇒ 400 + 点名下一步（去切换身份／等宽限期结束）；他人或未证明 ⇒ 409 那一句历史文案。
func joinOccupiedError(proven bool, ownMsg string) *apierrors.APIError {
	if !proven {
		return apierrors.New(apierrors.ErrConflict, "该邮箱已被其他账号绑定")
	}
	return apierrors.New(apierrors.ErrValidation, ownMsg)
}

// joinableRole 凭据预绑角色 → 落位角色（★ 加入门白名单，与邀请链接创建端那六档不同）。
//
// 只放 user / dept_admin / tenant_admin：super_admin、admin、approver 是平台级或审批位，
// 一枚企业邀请码就能把自己升上去＝提权路径。创建端（invite_link.go）已经拦过一遍，
// 但"库里那一列现在躺着什么"不由那一次写入决定——手输码、历史存量行、直改库都绕得开创建端，
// 所以落位侧必须再按名单收敛一次（读侧与写侧同一把尺子，AGENTS §一·8①）。
// 名单外一律降为普通成员并出声：**降档不拒绝**——人已站在这门口，档位坏的是管理员的配置，
// 代价不该由还没进门的那个人付（打回 400 只会让人反复试同一张码）。
func joinableRole(ctx context.Context, raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case store.RoleUser, store.RoleDeptAdmin, store.RoleTenantAdmin:
		return v
	case "":
		return store.RoleUser
	}
	observability.Warn(ctx, "企业邀请凭据预绑了加入门白名单外的角色，降为普通成员", "role", v)
	return store.RoleUser
}

// createEnterpriseJoin 按分流判定把身份行落库（★ 本函数是 §6.1 唯一的写库点）。
//
// 三种形态各自的凭证口径（方案 D2/D4）：
//   - joinNewHome     ⇒ CreateUserWithEmail：口令自设、is_home=1（这一行就是这个新账户的家）；
//   - joinMemberRow   ⇒ CreateIdentityRow：口令留空（成员行不含凭证），家行原样不动；
//   - joinCompleteHome ⇒ ① SetPasswordForAccount 补全**家行**口令（顺带清 must_change_pwd/待补全、
//     家行 token_version +1＝旧会话作废，而本次注册紧接着签一枚新会话给客户，不构成登出事故）
//     ② 用户名顺带更新（用户名不是登录标识，失败只出声不阻断）③ 再建企业成员行。
//
// ⚠️ 与改造前的关键差别：升级通道**不再停用空白号**（旧形态停号＋建新号会释放邮箱、
// 留下一个 disabled 的孤儿账户，而 §七 明令「家不动、邮箱不释放、无停用」），
// 因此 account_upgrade 这一审计动作在本路径退役，改由 membership_create + profile_setup 两条如实记录。
func (s *Server) createEnterpriseJoin(ctx context.Context, cred *entCredential, plan *joinPlan, username, password, email, agreedAt string) (*store.User, *apierrors.APIError) {
	role := joinableRole(ctx, cred.Role)
	orgID := cred.OrgID
	if orgID > 0 {
		// 组织可能已被管理员删掉（凭据是更早发出去的）：清零按未分配落位，别把入职打死。
		if verr := s.validateOrg(cred.TenantID, orgID); verr != nil {
			observability.Warn(ctx, "企业邀请凭据里的部门已失效，受邀人按未分配落位", "org_id", orgID, "tid", cred.TenantID, "err", verr.Error())
			orgID = 0
		}
	}
	// 部门管理员必须绑定部门（与 auth.go 那条既有判据同口径）；凭据没带部门或部门已失效时降为普通成员。
	if role == store.RoleDeptAdmin && orgID <= 0 {
		observability.Warn(ctx, "邀请凭据预绑部门管理员但没有可用部门，降为普通成员", "code", cred.Code)
		role = store.RoleUser
	}
	if plan.Action == joinNewHome {
		nu, err := s.Store.CreateUserWithEmail(cred.TenantID, username, auth.PasswordHash(password), username, role, 0, orgID,
			strings.ToLower(strings.TrimSpace(email)), agreedAt, 0)
		if err != nil {
			return nil, apierrors.New(apierrors.ErrValidation, "用户创建失败: "+err.Error())
		}
		return nu, nil
	}
	if plan.Action == joinCompleteHome {
		// ① 家行补全口令：0 行＝这个账户没有家行（迁移没跑或数据被并坏），必须当失败，不许回 success。
		n, err := s.Store.SetPasswordForAccount(plan.AccountID, auth.PasswordHash(password))
		if err != nil {
			observability.Warn(ctx, "注册口补全家行口令失败", "account", plan.AccountID, "err", err.Error())
			return nil, apierrors.New(apierrors.ErrInternal, "账号升级失败，请稍后重试")
		}
		if n != 1 {
			observability.Warn(ctx, "注册口补全家行口令影响行数不是 1（该账户没有家行？）", "account", plan.AccountID, "rows", n)
			return nil, apierrors.New(apierrors.ErrInternal, "账号升级失败，请稍后重试")
		}
		s.Store.LogAudit(plan.Home.TenantID, plan.Home.ID, "profile_setup", "user", "注册口升级通道：凭企业邀请凭据补全家行口令（旧空白号不停用）")
		// ② 用户名以本次请求为准（空白号自动建号时用的是邮箱前缀，客户在补全面板里改过名字）。
		if username != "" && username != plan.Home.Username {
			if serr := s.Store.SetUsername(plan.Home.ID, plan.Home.TenantID, username); serr != nil {
				observability.Warn(ctx, "注册口更新家行用户名失败（不阻断加入）", "uid", plan.Home.ID, "err", serr.Error())
			}
		}
	}
	// ③ 企业成员行（joinCompleteHome 与 joinMemberRow 共用：口令空、is_home=0）。
	nu, err := s.Store.CreateIdentityRow(plan.AccountID, cred.TenantID, username, username, role, 0, orgID, email)
	if err != nil {
		return nil, apierrors.New(apierrors.ErrValidation, "用户创建失败: "+err.Error())
	}
	return nu, nil
}
