// ============ invite_link.go · 职责说明 ============
// 多身份方案 B3（2026-10-24）门二「管理台生成专属邀请链接建号」的 HTTP 面
// （《多身份账号方案_20261023.md》§6.2、§十一 锁 12）。
//
// 产品语义（R9 拍板）：租户/部门管理员建账号时**只录用户名和部门/角色（可选）**，
// 不录口令、甚至不录邮箱；系统生成**一次性专属邀请链接**；用户点链接进注册页
// **自己填邮箱+口令**，注册成功链接即失效。管理员全程不接触任何口令。
//
// 本文件承载四个端点：
//   - POST /api/admin/invite-links         创建链接（requireDeptAdmin，部门管理员限本部门树）；
//   - GET  /api/admin/invite-links         列本租户链接（管理台邀请列表四档视图，逐行带服务端算好的 url）；
//   - GET  /api/admin/invite-links/qrcode  按链接 id 出该链接的二维码 PNG（★ B4：只出可用链接、只出自己有作用域的）；
//   - GET  /api/auth/invite-link/{code}    公开校验（注册页预填用，五态各给明确原因）。
//
// ★★ B3-c（2026-10-24）起，本文件的 issueInviteLink 是「发一枚企业邀请链接」这一动作的
//
//	**唯一**守卫链 + 写库点，三个调用面共用：邀请链接面板、管理台建号（R9 后不录口令）、
//	Excel 批量导入逐行。新增任何一个"给企业添人"的入口都必须走它——
//	角色白名单／权限梯／部门作用域／随机码失败口径各写一份，迟早漂成「一个面拒了另一个面放行」。
//
// ★ 三条设计纪律（改这一族前必读）：
//
//	① **不建 users 行**：pending 状态完全活在 invite_codes 里，users 状态机零改动
//	   （登录/取人/列表/权限全部不受影响）。任何"顺手先把空号建出来"的改动都会让
//	   未激活成员出现在用户列表与用量报表里。
//	② **code 即密钥**：`inv_<32hex>` 高熵、不可枚举；链接被转发＝凭证转移，兜底是「可撤销 + 使用留痕」，
//	   不是阅后即焚（与 API Key 的差别见 §6.5——它必须可转发、可重复查看、可长期有效）。
//	③ 公开校验端点的五种无效原因**必须分得开**（无效/已用/已撤销/已过期/正常）：
//	   管理员转发错链接、用户点到过期链接，看到的都该是「这条能修的那句话」，而不是含糊的「链接无效」。
//	   这一条不违反防枚举口径——防枚举保护的是**邮箱归属**（登录/注册面），而邀请码本来就是管理员
//	   主动发给当事人的门禁串，它的五种状态对当事人都是他本来就知道的事实。
//
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/store"
	"translator/internal/tenant"
)

// inviteLinkDefaultDays 专属邀请链接默认有效期（天）——§6.2 拍板值 7。
// 档位仍走 configIntTier 三级（env > system_config > 代码默认，AGENTS §一·10），
// 运营可配；代码默认值只在这里出现一份，禁止在调用方再抄一次 7。
const inviteLinkDefaultDays = 7

// inviteLinkMaxDays 有效期上限（365 天）。
// 存在的理由不是"防手滑填了 99999"，而是：链接＝企业门禁，一条永久有效的链接在员工离职、
// 截图外传、群聊转发之后仍然可用；给它一个可运营的上限，是把"忘记撤销"的代价从"永久"压成"最多一年"。
const inviteLinkMaxDays = 365

// inviteLinkHostEnvKey / inviteLinkHostCfgKey 链接落地站点主机的配置档（品牌域/主站自适应）。
// 为什么要有这一档：管理员在管理台（可能是品牌子域、也可能是主站后台）点"生成链接"，
// 生成的 URL 却写死了当前请求 Host——超管用 X-Tenant-ID 在平台后台给某租户建链接时，
// 发出去的是**平台域名**，受邀人落地页显示的就是错的品牌。
const (
	inviteLinkHostEnvKey = "INVITE_LINK_HOST"
	inviteLinkHostCfgKey = "invite_link_host"
)

// inviteLinkCreateResp 创建链接的回执结构（管理台要拿 url 去复制/发信/出二维码）。
type inviteLinkCreateResp struct {
	Success    bool   `json:"success"`
	ID         int64  `json:"id"`
	Code       string `json:"code"`
	URL        string `json:"url"`
	TenantID   int64  `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	ExpiresAt  string `json:"expires_at"` // 空=不过期（当前配置下不会出现，保留形态给将来）
	Status     string `json:"status"`
}

// inviteLinkPublicResp 公开校验端点的出栈结构（注册页据此预填）。
type inviteLinkPublicResp struct {
	Success      bool   `json:"success"`
	Valid        bool   `json:"valid"`
	Reason       string `json:"reason,omitempty"`  // invalid/used/revoked/expired（无效时给明确文案）
	Message      string `json:"message,omitempty"` // 面向当事人的可行动中文文案（注册页直接展示）
	TenantName   string `json:"tenant_name"`       // 「你将加入 X 企业」
	OrgName      string `json:"org_name"`          // 「Y 部门」（未绑为空）
	Username     string `json:"username"`          // 预填用户名
	BoundEmail   string `json:"bound_email"`       // 非空 ⇒ 注册页邮箱只读
	EmailLocked  bool   `json:"email_locked"`      // 上面那条的显式形态（前端不必再判空串）
	Role         string `json:"role"`              // 预绑角色（空=默认 user）
	ExpiresAt    string `json:"expires_at"`        // 展示"此链接 X 前有效"
	IsPersonal   bool   `json:"is_personal"`       // 企业链接恒 false（防御性读数）
	TenantStatus string `json:"tenant_status"`     // 租户被停用/到期时公开端点直接判无效
}

// inviteLinkExpiryDays 读取链接有效期档位（天）并按 [1, inviteLinkMaxDays] 夹住。
// 入参 reqDays：请求显式指定的天数（<=0 表示没填，取配置档）。
// 夹档而不是报错：管理台表单填了 5000 天，判红让管理员摸不着头脑；
// 压到上限＋回执里带真实到期日，他看到的就是落库的那一句话。
func (s *Server) inviteLinkExpiryDays(reqDays int) int {
	d := reqDays
	if d <= 0 {
		d = configIntTier(s.Store, "INVITE_LINK_EXPIRE_DAYS", "invite_link_expire_days", inviteLinkDefaultDays, false)
	}
	if d > inviteLinkMaxDays {
		d = inviteLinkMaxDays
	}
	if d <= 0 {
		d = inviteLinkDefaultDays
	}
	return d
}

// inviteLinkHostOverride 链接落地主机的显式配置档（env > system_config > 空=未配置）。
// 与 configIntTier 同一优先序，只是这一档是字符串；单独一个函数是为了让 inviteLinkHostFor
// 的四级优先序读起来是"配置 → 品牌域 → 主站 → 请求 Host"，而不是夹一段取配置的样板。
func (s *Server) inviteLinkHostOverride() string {
	if v := strings.TrimSpace(os.Getenv(inviteLinkHostEnvKey)); v != "" {
		return v
	}
	if s.Store != nil {
		if v, err := s.Store.GetConfig(inviteLinkHostCfgKey); err == nil {
			if t := strings.TrimSpace(v); t != "" {
				return t
			}
		}
	}
	return ""
}

// inviteLinkHostFor 解析邀请链接落地主机（品牌域/主站自适应）。
// 优先序（★ 与 §一·3「env > system_config > 代码默认」同口径）：
//  1. env INVITE_LINK_HOST / system_config invite_link_host（运营显式指定，唯一硬覆盖口）；
//  2. 目标租户自己的品牌域（t.Domain 非空 ⇒ <domain>.<基础域>）——受邀人看到的必须是他要加入的那家企业的门面；
//  3. 主站（primaryHost）；
//  4. 当前请求 Host（本地开发/未配品牌时的兜底）。
//
// 为什么第 2 档在第 3 档之前：一个配了专属域的企业，它的员工点开的链接如果落在主站域名上，
// 落地页显示的是平台品牌而不是自己企业的名字——那正是 §6.5 第 4 条「受邀落地页告知」要防的误解。
func (s *Server) inviteLinkHostFor(tid int64, r *http.Request) string {
	if v := s.inviteLinkHostOverride(); v != "" {
		return v
	}
	if s.Ten != nil && tid > 0 {
		if t, err := s.Ten.GetByID(tid); err == nil && t != nil && strings.TrimSpace(t.Domain) != "" {
			return strings.TrimSpace(t.Domain) + "." + brandingBaseDomain(s)
		}
	}
	if h := s.primaryHost(); h != "" {
		return h
	}
	if r != nil && strings.TrimSpace(r.Host) != "" {
		return r.Host
	}
	return brandPrimaryHost()
}

// inviteLinkURL 拼出完整邀请链接（`https://<host>/?invite=<code>`，§6.2 管理员侧第 3 步）。
// 刻意用查询参数而不是路径：注册页已经在 /register 上，路径形态要么新增一条公开路由
// 要么让前端猜重定向；`?invite=` 是方案 §6.3 点名的参数名，前后端只认这一个写法。
func inviteLinkURL(host, code string) string {
	h := strings.TrimSpace(host)
	if h == "" {
		h = "localhost"
	}
	return "https://" + h + "/?invite=" + strings.TrimSpace(code)
}

// inviteLinkSpec 一枚专属邀请链接的创建意图（★ 多身份方案 B3-c，2026-10-24）。
//
// 为什么要有这一份：R9 拍板「管理台建号不录口令」之后，「给企业添一个成员」在三张脸上
// 是同一件事——邀请链接面板、管理台建号、Excel 批量导入逐行。三个调用面若不共用一份
// 守卫链与落库口径，就会长出三把尺子：同一种越权在一个面上被拒、在另一个面上发出去
// （本仓「同一件事写两遍迟早打架」那一族，AGENTS §一·5 报价单一事实源同理）。
type inviteLinkSpec struct {
	Username    string // 预填给受邀人的用户名（必填）
	Role        string // 预绑角色（空=普通用户；白名单与权限梯见 inviteGrantGuard）
	OrgID       int64  // 预绑部门（0=未分配；dept_admin 必须 >0）
	Email       string // 预绑邮箱（非空 ⇒ 注册邮箱必须等于它）
	ExpiresDays int    // 显式有效期天数（<=0=取配置档，见 inviteLinkExpiryDays）
}

// inviteGrantGuard 角色缺省 + 白名单 + 权限梯（★ 三个调用面共用的一把尺子）。
//
// 权限梯在这里比在建号口更要紧：链接的 role 是**注册时才落地**的，
// 部门管理员发一枚 tenant_admin 的链接出去，等于把管理权挂在一个可转发的 URL 上。
func inviteGrantGuard(actor *store.User, rawRole string) (string, *apierrors.APIError) {
	role := strings.TrimSpace(rawRole)
	if role == "" {
		role = store.RoleUser
	}
	switch role {
	case store.RoleUser, store.RoleDeptAdmin, store.RoleTenantAdmin, store.RoleSuperAdmin, store.RoleApprover, store.RoleAdmin:
	default:
		return "", apierrors.New(apierrors.ErrValidation, "角色无效")
	}
	if auth.RoleLevel(role) >= 4 && !auth.IsSuperAdmin(actor) {
		return "", apierrors.New(apierrors.ErrForbidden, "权限不足：仅超级管理员可分配该角色")
	}
	if auth.RoleLevel(role) >= 3 && !auth.IsTenantAdmin(actor) {
		return "", apierrors.New(apierrors.ErrForbidden, "权限不足：仅租户管理员及以上可分配该角色")
	}
	return role, nil
}

// issueInviteLink 走完守卫链、落一枚 active 链接并返回完整 URL（★ B3-c 本族唯一的写库点）。
//
// 参数 tid 由调用方解析（各面口径不同：建号口超管可平台级、链接与导入必须企业级），
// tid<=0 在这里判死——pending 成员的用量最终要落在某家企业的预算墙上，
// 没有归属的链接注册出来是一个进不了任何租户的孤儿身份。
//
// 守卫次序（与旧 handleInviteLinkCreate 逐条同尺，只是收敛成一个函数）：
//  1. 用户名必填；
//  2. 角色白名单 + 权限梯（inviteGrantGuard）；
//  3. 归属企业必须解析得出；
//  4. 部门管理员作用域：本人必须已绑部门、目标部门必须落在自己子树内；
//  5. 部门归属校验（validateOrg，org_id=0 恒合法）；
//  6. dept_admin 必须绑定部门（F-31 闸 1：org_id=0 的部门管理员是死角色，源头卡死）；
//  7. 随机码生成失败 ⇒ 500，**绝不**退化成时间戳/用户名派生的可猜测串。
//
// ★ 本函数**不建 users 行**（设计纪律 ①：pending 状态完全活在 invite_codes 里）。
func (s *Server) issueInviteLink(r *http.Request, actor *store.User, tid int64, spec inviteLinkSpec) (*store.InviteCode, string, *apierrors.APIError) {
	username := strings.TrimSpace(spec.Username)
	if username == "" {
		return nil, "", apierrors.New(apierrors.ErrValidation, "用户名不能为空")
	}
	role, gerr := inviteGrantGuard(actor, spec.Role)
	if gerr != nil {
		return nil, "", gerr
	}
	if tid <= 0 {
		return nil, "", apierrors.New(apierrors.ErrValidation, "无法确定链接归属的企业")
	}
	// 部门管理员范围：目标部门必须落在自己的部门子树内（否则拒绝发出门禁串）。
	if auth.RoleLevel(actor.Role) == 2 {
		if actor.OrgID <= 0 {
			return nil, "", apierrors.New(apierrors.ErrForbidden, "部门管理员未绑定部门，无法生成邀请链接")
		}
		if spec.OrgID > 0 {
			inTree, e := s.Store.IsOrgInSubtree(tid, actor.OrgID, spec.OrgID)
			if e != nil || !inTree {
				return nil, "", apierrors.New(apierrors.ErrForbidden, "无权在非本部门下生成邀请链接")
			}
		}
	}
	if err := s.validateOrg(tid, spec.OrgID); err != nil {
		return nil, "", apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err))
	}
	if role == store.RoleDeptAdmin && spec.OrgID <= 0 {
		return nil, "", apierrors.New(apierrors.ErrValidation, "部门管理员必须绑定部门：请先在组织列表中选择所属部门")
	}
	// 预绑邮箱规范化（与库里 email 列同口径：trim + 小写），空串表示"纯链接、不锁邮箱"。
	boundEmail := strings.ToLower(strings.TrimSpace(spec.Email))
	code, err := store.NewInviteLinkCode()
	if err != nil {
		// 随机源失败＝生成不出不可枚举的门禁串。这里**绝不**兜底成时间戳/用户名派生串：
		// 那种"还是生成了一个链接"的修法把不可枚举性抹掉了，而管理台完全看不出区别。
		return nil, "", apierrors.New(apierrors.ErrInternal, "生成邀请链接失败，请稍后重试")
	}
	days := s.inviteLinkExpiryDays(spec.ExpiresDays)
	expiresAt := time.Now().UTC().AddDate(0, 0, days).Format(time.RFC3339)
	link, err := s.Store.CreateInviteLink(code, tid, spec.OrgID, role, username, boundEmail, expiresAt)
	if err != nil {
		if store.IsUniqueViolation(err) {
			// 撞码在 128 bit 随机串上物理上不会发生；真撞了说明库里有人手输过同形串。
			// 回 409 而不是重试：静默重试会把"有人伪造了一枚 inv_ 形状的码"这件事藏起来。
			return nil, "", apierrors.New(apierrors.ErrConflict, "邀请链接创建冲突，请重试或联系管理员")
		}
		return nil, "", apierrors.New(apierrors.ErrInternal, "创建邀请链接失败: "+store.DebriefDBError(err))
	}
	s.Store.LogAudit(tid, actor.ID, "invite_link_create", "invite", code)
	return link, inviteLinkURL(s.inviteLinkHostFor(tid, r), link.Code), nil
}

// handleInviteLinksRoute /api/admin/invite-links 的方法分派（★ 方案 §6.2 点名的端点形态）。
// GET=列本租户链接，POST=创建一枚链接；其余方法 405。
// 两条子处理各自带 requireDeptAdmin（不是把鉴权收在这里再往下传）：
// 路由闸门按 handler 的调用闭包找鉴权动作，收在一层"分发器"里会让闭包搜索多绕一跳，
// 而将来新增第三条方法（PATCH 撤销之类）时最容易忘记的就是这一跳。
func (s *Server) handleInviteLinksRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleInviteLinks(w, r)
	case http.MethodPost:
		s.handleInviteLinkCreate(w, r)
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "请使用 GET 或 POST 请求"))
	}
}

// handleInviteLinkCreate 创建专属邀请链接（POST /api/admin/invite-links，★ §6.2 管理员侧）。
//
// 守卫链与落库全部收敛在 issueInviteLink（★ B3-c：管理台建号与批量导入走同一个函数，
// 三个面一把尺子），本 handler 只做「解析入参 → 解析归属租户 → 出栈形态」三件事。
//
// ★ 本方法**不建 users 行**（设计纪律 ①）。
func (s *Server) handleInviteLinkCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Username    string `json:"username"`     // 必填：预填给受邀人的用户名
		DisplayName string `json:"display_name"` // 选填：显示名（与 username 同值时前端不必再填）
		OrgID       int64  `json:"org_id"`       // 选填：预绑部门
		Role        string `json:"role"`         // 选填：预绑角色（默认 user）
		Email       string `json:"email"`        // 选填：预绑邮箱（非空 ⇒ 注册邮箱必须等于它）
		TenantID    int64  `json:"tenant_id"`    // 仅超管可用：给指定租户建链接
		ExpiresDays int    `json:"expires_days"` // 选填：有效期（默认 7 天、上限 365）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	_ = req.DisplayName // 显示名由受邀人注册时自己填；链接只带 username/bound_email/role/org（不替当事人取名）
	// 归属租户：非超管限本租户（忽略入参），超管可按 tenant_id / 组织归属 / 生效租户解析。
	tid := s.effTenant(r, u)
	if auth.IsSuperAdmin(u) {
		if req.OrgID > 0 {
			if org, e := s.Store.GetOrgByID(req.OrgID); e == nil && org != nil && org.TenantID > 0 {
				tid = org.TenantID
			}
		} else if req.TenantID > 0 {
			tid = req.TenantID
		}
	} else {
		tid = u.TenantID
	}
	link, url, apiErr := s.issueInviteLink(r, u, tid, inviteLinkSpec{
		Username: req.Username, Role: req.Role, OrgID: req.OrgID, Email: req.Email, ExpiresDays: req.ExpiresDays,
	})
	if apiErr != nil {
		s.writeError(w, r, apiErr)
		return
	}
	name, _ := s.tenantBriefFor(tid, nil)
	writeJSON(w, 200, inviteLinkCreateResp{
		Success: true, ID: link.ID, Code: link.Code, URL: url,
		TenantID: tid, TenantName: name.name, ExpiresAt: link.ExpiresAt, Status: link.Status,
	})
}

// inviteLinkRow 列表出栈的一行＝store 行 ＋ 服务端算好的完整链接。
//
// 为什么 url 必须由服务端出栈（★ B4，2026-10-24）：链接的 host 不是「当前后台域名」——
// inviteLinkHostFor 依次问 `invite_link_host` 配置、租户品牌域、主站域，最后才回落到请求 Host。
// 前端若自己拼 `https://<window.location.host>/?invite=<code>`，超管在平台后台给品牌域租户
// 生成的那一档就会拼错域名：受邀人点开的是主站落地页、显示的是错的品牌。
// 创建回执（inviteLinkCreateResp.URL）与列表读数必须同源同尺，否则管理员复制到的和他发出去的
// 是两个地址，而两个地址都"看着能用"。
type inviteLinkRow struct {
	*store.InviteCode
	URL string `json:"url"`
}

// handleInviteLinks 邀请链接列表（GET /api/admin/invite-links，管理台四档视图用）。
// 非超管只看本租户；超管不带 tenant_id 时看全平台（与 handleInviteCodes 的过滤口径一致）。
func (s *Server) handleInviteLinks(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	tid := u.TenantID
	if auth.IsSuperAdmin(u) {
		if v := strings.TrimSpace(r.URL.Query().Get("tenant_id")); v != "" {
			if x, e := strconv.ParseInt(v, 10, 64); e == nil {
				tid = x
			}
		} else {
			tid = s.effTenant(r, u)
		}
	}
	links, err := s.Store.ListInviteLinks(tid)
	if err != nil {
		// 读失败诚实回 500：200 壳会让面板渲染成「一条链接都没有」，
		// 与"确实没发过链接"在界面上完全同形（同 handleInviteCodes 那条 F-64② 判据）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	if links == nil {
		links = []*store.InviteCode{} // 空值归一成 []（AGENTS §一·5 分页/空值口径：Go 的 nil slice 出栈是 null）
	}
	rows := make([]inviteLinkRow, 0, len(links))
	for _, l := range links {
		rows = append(rows, inviteLinkRow{InviteCode: l, URL: inviteLinkURL(s.inviteLinkHostFor(l.TenantID, r), l.Code)})
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "links": rows})
}

// handleInviteLinkPublic 公开校验邀请链接（GET /api/auth/invite-link/{code}，★ §6.2 用户侧第 1 步）。
//
// 匿名可达是前提（人还没注册呢），防护由**码本身的不可枚举性**承担，不靠会话。
// 出栈只给预填所需的读数（企业名/部门名/用户名/是否锁邮箱/到期时间）：
// 零成员列表、零用量、零 Key —— 校验一枚门禁串不该顺带把门后的东西透出来。
//
// 状态码口径：链接本身无效**也回 200**，用 valid=false + reason 表达。
// 理由同 F-79「公开首屏不返 403」那一族——注册页拿到 4xx 会走错误位，
// 用户看到的是一个报错页而不是一句"这个链接已经过期了，请找管理员重新生成"的可行动文案。
func (s *Server) handleInviteLinkPublic(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/auth/invite-link/"))
	out := inviteLinkPublicResp{Success: true}
	if code == "" {
		out.Reason = "invalid"
		out.Message = inviteLinkReasonText("invalid")
		writeJSON(w, 200, out)
		return
	}
	link, err := s.Store.GetInviteLinkByCode(code)
	if err != nil {
		// 存储故障与"码不存在"必须分得开：前者让人以为链接被撤销了，会去找管理员重新生成（白忙）；
		// 后者才是真无效。故障这一档回 500，让落地页出"暂时校验不了，请稍后再试"。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	if link == nil {
		out.Reason = "invalid"
		out.Message = inviteLinkReasonText("invalid")
		writeJSON(w, 200, out)
		return
	}
	// 五态判定顺序：revoked > used > expired（与 store.inviteStatusOf 那把尺子逐字一致——
	// 人已动过手的事实优先于时间到期；两处各判一次迟早打架）。
	if link.Revoked == 1 {
		out.Reason = "revoked"
	} else if link.Used == 1 {
		out.Reason = "used"
	} else if link.IsExpired() {
		out.Reason = "expired"
	}
	// 租户维度：企业被停用/到期时链接一律判无效（门禁指向一间已经关门的办公室）。
	tid := link.TenantID
	brief, ok := s.tenantBriefFor(tid, nil)
	if !ok || (brief.status != "" && brief.status != tenant.StatusActive) {
		out.Reason = "invalid"
	}
	if out.Reason != "" {
		out.Message = inviteLinkReasonText(out.Reason)
		writeJSON(w, 200, out)
		return
	}
	out.Valid = true
	out.TenantName = brief.name
	out.IsPersonal = brief.isPersonal
	out.TenantStatus = brief.status
	out.ExpiresAt = link.ExpiresAt
	out.Role = link.Role
	out.Username = link.BoundUsername
	out.BoundEmail = link.BoundEmail
	out.EmailLocked = link.BoundEmail != ""
	if link.OrgID > 0 {
		if o, e := s.Store.GetOrgByID(link.OrgID); e == nil && o != nil {
			out.OrgName = o.Name
		}
	}
	writeJSON(w, 200, out)
}

// inviteLinkReasonText 链接无效原因 → 中文可行动文案（注册页直接展示）。
// 四档文案各自点名"下一步做什么"，因为这一面是当事人唯一的报错入口；
// 拿一句「链接无效」覆盖四种情况，用户只会反复点同一个坏链接。
func inviteLinkReasonText(reason string) string {
	switch reason {
	case "used":
		return "该邀请链接已被使用（一枚链接只能开通一个账号），请向企业管理员重新获取"
	case "revoked":
		return "该邀请链接已被管理员撤销，请向企业管理员重新获取"
	case "expired":
		return "该邀请链接已过期，请向企业管理员重新获取"
	default:
		return "邀请链接无效，请检查链接是否完整或向企业管理员重新获取"
	}
}

// handleInviteLinkQrcode 邀请链接二维码 PNG（GET /api/admin/invite-links/qrcode?id=<id>[&download=1]，★ 方案 §6.2「复制/发信/出二维码」第三件）。
//
// 为什么按 id 出图、而不是收一个 ?text= 参数：一枚任意文本的二维码生成器是**任何**已登录用户
// 都能拿来的钓鱼工具（把企业门禁串包进自己的二维码发到群里，来源不可溯）。
// 这里只允许出「库里已经存在的、且属于调用方作用域的」那一枚链接的图，
// 作用域判据与列表完全同一把尺子（非超管限本租户；越权与不存在回同一句 404，不泄露他租户链接的存在性，AGENTS §一·8②）。
//
// 死链（已用／已撤销／已过期）不出图：二维码是"把门禁发出去"的动作，
// 对一条已经作废的凭据出图＝让管理员把一张扫不开的图发给员工，还不如当场告诉他去重新生成。
func (s *Server) handleInviteLinkQrcode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "请使用 GET 请求获取二维码"))
		return
	}
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	id, perr := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	const notFound = "邀请链接不存在"
	if perr != nil || id <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, notFound))
		return
	}
	link, lerr := s.Store.GetInviteCode(id)
	// 三种"取不到可用链接"（行不存在／不是链接形态／不属于我的作用域）折叠成同一句 404：
	// 分开回等于给逐号枚举别人租户的链接 ID 提供一条免费探针。
	if lerr != nil || link == nil || link.Kind != "link" {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, notFound))
		return
	}
	if !auth.IsSuperAdmin(u) && link.TenantID != u.TenantID {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, notFound))
		return
	}
	if link.Used == 1 || link.Revoked == 1 || link.IsExpired() {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "该邀请链接已不可用（已使用／已撤销／已过期），请重新生成后再发二维码"))
		return
	}
	url := inviteLinkURL(s.inviteLinkHostFor(link.TenantID, r), link.Code)
	png, eerr := qrcode.Encode(url, qrcode.Medium, 512)
	if eerr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "二维码生成失败"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", contentDisposition("invite-"+link.Code+".png"))
	}
	_, _ = w.Write(png)
}
