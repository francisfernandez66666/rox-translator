// ============ register_join_test.go · 职责说明 ============
// 多身份账号方案 B3（2026-10-24）「注册口汇流」的行为锁：打**真实 handleRegister**（f77 夹具 + f77Mux），
// 覆盖《多身份账号方案_20261023.md》§十一 点名给 B3 的第 1／2／6／7／9／11／12／13 条。
//
// 为什么必须是 HTTP 级而不是单测判据函数：§6.1 那六种邮箱情形 × 三种凭据形态的**全部风险都在接线顺序上**——
// 「烧码在分流拒绝之前」（客户资产损失）、「IP 档在凭据解析之前」（R5 豁免失效）、
// 「防枚举在验码之后」（P0-9 白做）、identities 出栈与注册即登录那一次 token 是不是同一个人。
// 这些都不在任何一个纯函数里，把 planEnterpriseJoin 单独调用来调用去只会得到一个"判据函数自己很对"的绿灯。
//
// 八组判据：
//
//	J1（§十一 1＋9＋11）本人已有家行 + 有效码 ⇒ 只建企业成员行（家不动、成员行不带口令）、
//	   注册即登录的 token 指向**企业行**、identities 两条、审计 membership_create 留痕、
//	   该身份落一行 tokens=0 的「不发放」标记（R5）。
//	J2（§十一 2＋9）他人 JWT + 已被占用的邮箱 ⇒ 与改造前**逐字相同**的一句 409，
//	   且**不许烧码**（拒绝发生在 MarkInviteCodeUsed 之前）。
//	J3（§十一 1 反向）本人已是该企业成员 ⇒ 400 点名「可直接切换身份」，同样不烧码。
//	J4（§十一 7＋12）§6.3「删除静默降级」七档：无效码／member 没带码／personal 带码／
//	   已用码／已撤销码／已过期码／tenant_id=0 的老语义码，各回一句可行动文案且**没有**任何
//	   账号或租户被建出来；另钉 role_choice=admin 带码 ⇒ 建自己的企业且**不烧**那张码。
//	J5（§十一 6）本人空白家行 + 有效码 ⇒ 走 joinCompleteHome：家行补全口令、不停用、不释放邮箱，
//	   另挂一条成员行；审计两条（profile_setup + membership_create）且**没有** account_upgrade。
//	J6（§十一 11）个人注册那一行拿到额度，企业身份那一行拿不到；且登录补发腿**打不动**它
//	   （直接调 autoGrantUserTrial 反证 tokens=0 标记是持久事实，不是"这一次没调"）。
//	J7（§十一 12）邀请链接形态：创建端不建 users 行、预绑邮箱是最强绑定、注册即烧码、
//	   二次提交/过期/撤销各回一句链接文案、预绑角色落进落库那一行（不再一律降成 user）。
//	J8（§十一 13）两套邀请彼此隔离：只带个人裂变码 ref ⇒ 奖励归属照落，但**没有**企业身份
//	   （响应里根本没有 identities 键、无 membership_create、落的是个人租户）。
//
// ★ 方言由 f77Fixture 自钉 sqlite（AGENTS §一·4）；IP 档每轮放开（upgRegister 内做），
//
//	email_verify_enabled 保持默认关——proven 一律由**本人 JWT** 提供，这正是 §七 判定扩展的形态。
//
// ⚠️ 修改此处请同步更新本说明。
// ==========================================================
package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// joinPassword 注册表单里的自设口令（过 auth.ValidatePassword 强度尺）。
const joinPassword = "Join-2026-passw0rd"

// joinEnterpriseTenant 建一家普通企业租户（is_personal=0）并返回其 ID。
func joinEnterpriseTenant(t *testing.T, f *renewFixture, code string) int64 {
	t.Helper()
	tt, err := f.srv.Ten.Create(code, code+" 公司", "", "{}")
	if err != nil {
		t.Fatalf("预置企业租户失败: %v", err)
	}
	return tt.ID
}

// joinHomeAccount 造一个**已补全**的个人家行（带口令、无待补全标记、个人租户）。
// 它是 §6.1「本人已有家行」那一档的输入：口令非空 ⇒ upgradeBlankID 不置位，
// 于是同一条请求只能走 joinMemberRow（而不是升级通道），两条路径各测各的。
func joinHomeAccount(t *testing.T, f *renewFixture, username, email string) *store.User {
	t.Helper()
	tt, err := f.srv.Ten.Create("p_"+username, username, "", "{}")
	if err != nil {
		t.Fatalf("建个人租户失败: %v", err)
	}
	u, err := f.srv.Store.CreateUserWithEmail(tt.ID, username, auth.PasswordHash(identPassword), username,
		store.RoleUser, 0, 0, email, time.Now().Format(time.RFC3339), 0)
	if err != nil {
		t.Fatalf("建家行失败: %v", err)
	}
	if err := f.srv.Ten.SetPersonal(tt.ID, true); err != nil {
		t.Fatalf("个人租户标记失败: %v", err)
	}
	return u
}

// joinMakeCode 生成一枚绑定到指定企业的邀请码（kind='code'，管理员手输形态）。
func joinMakeCode(t *testing.T, f *renewFixture, code string, tid int64) *store.InviteCode {
	t.Helper()
	c, err := f.srv.Store.CreateInviteCode(code, tid)
	if err != nil {
		t.Fatalf("预置邀请码失败: %v", err)
	}
	return c
}

// joinMakeLink 生成一枚专属邀请链接（kind='link'）；**只写 invite_codes，不碰 users 表**。
func joinMakeLink(t *testing.T, f *renewFixture, code string, tid int64, orgID int64, role, boundUser, boundEmail, expiresAt string) *store.InviteCode {
	t.Helper()
	c, err := f.srv.Store.CreateInviteLink(code, tid, orgID, role, boundUser, boundEmail, expiresAt)
	if err != nil {
		t.Fatalf("预置邀请链接失败: %v", err)
	}
	return c
}

// joinMemberBody 「凭邀请码/链接加入企业」的注册载荷（role_choice=member 是前端真实形态）。
func joinMemberBody(username, invite string) map[string]any {
	return map[string]any{
		"username": username, "password": joinPassword, "type": "enterprise",
		"role_choice": "member", "invite": invite, "agreed": true,
	}
}

// joinAdminBody 「我是管理员，新建企业」的载荷；invite 为空时不带凭据串。
func joinAdminBody(t *testing.T, username, tenantCode, invite string) map[string]any {
	t.Helper()
	b := map[string]any{
		"username": username, "password": joinPassword, "type": "enterprise",
		"role_choice": "admin", "code": tenantCode, "name": tenantCode + " 公司", "agreed": true,
	}
	if invite != "" {
		b["invite"] = invite
	}
	return b
}

// joinRow 读一行（不存在即判红——断言里"这一行没建出来"和"读挂了"必须分开报）。
func joinRow(t *testing.T, f *renewFixture, uid, tid int64) *store.User {
	t.Helper()
	u, err := f.srv.Store.GetUser(uid, tid)
	if err != nil || u == nil {
		t.Fatalf("读取身份行 uid=%d tid=%d 失败: %v", uid, tid, err)
	}
	return u
}

// joinCount 一个只读计数判据（表名/条件由调用方拼好，本函数只负责"读不到即红"）。
func joinCount(t *testing.T, f *renewFixture, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.srv.Store.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("计数失败 query=%s: %v", query, err)
	}
	return n
}

// joinRespUser 取响应里的新账号（id 用于后续读数）。
func joinRespUser(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	u, ok := m["user"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 user 对象: %v", m)
	}
	return u
}

// joinMsg 取响应 message（文案逐字判据的读数入口）。
func joinMsg(m map[string]any) string {
	s, _ := m["message"].(string)
	return s
}

// joinCodeStillOpen 判这张凭据**没被烧**（used=0）：§6.1 的三条拒绝分支每一条都必须落在这里，
// 拒绝注册却烧掉客户自己发出去的码＝客户资产损失（AGENTS §一·10）。
func joinCodeStillOpen(t *testing.T, f *renewFixture, code string) {
	t.Helper()
	row, err := f.srv.Store.GetInviteCodeByCode(code)
	if err != nil {
		t.Fatalf("读邀请码失败: %v", err)
	}
	if row.Used != 0 {
		t.Fatalf("被拒的注册不许烧掉邀请码（客户资产损失），实得 used=%d used_by=%q", row.Used, row.UsedBy)
	}
}

// ---- J1：本人已有家行 + 有效码 ⇒ 只建企业成员行（§十一 1／9／11）----

func TestJoin01MemberRowKeepsHomeAndSignsEnterpriseSession(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j1")
	home := joinHomeAccount(t, f, "j1_home", "j1-a@ex.com")
	joinMakeCode(t, f, "J1CODE", cTid)

	code, m := upgRegister(t, f, upgToken(t, home), "j1-a@ex.com", joinMemberBody("j1_staff", "J1CODE"))
	if code != http.StatusOK || m["success"] != true {
		t.Fatalf("本人持有效码应放行，实得 code=%d body=%v", code, m)
	}
	// ① 落位到**企业租户**，且出栈文案不再承诺额度（R5）
	if num(m, "tenant_id") != cTid {
		t.Fatalf("tenant_id 应是企业租户 %d，实得 %v", cTid, m["tenant_id"])
	}
	if got := joinMsg(m); got != "注册成功，已加入企业" {
		t.Fatalf("凭据门成功语应点名「已加入企业」，实得 %q", got)
	}
	nu := joinRespUser(t, m)
	uid := int64(nu["id"].(float64))
	if uid == home.ID {
		t.Fatal("加入企业应新建一条身份行，不许复用家行 id")
	}
	// ② 成员行形状：is_home=0、挂同一账户号、**不带口令**（D2／D4）
	row := joinRow(t, f, uid, cTid)
	if row.IsHome != 0 {
		t.Fatalf("成员行不许是家行，实得 is_home=%d", row.IsHome)
	}
	if row.AccountID != home.AccountID {
		t.Fatalf("成员行账户号应与家行同一人，实得 %q vs %q", row.AccountID, home.AccountID)
	}
	if row.PasswordHash != "" {
		t.Fatal("成员行不许残留可用口令（口令只存家行，D4）")
	}
	if row.Role != store.RoleUser {
		t.Fatalf("未预绑角色的凭据应落普通成员，实得 %q", row.Role)
	}
	if row.Email != "j1-a@ex.com" {
		t.Fatalf("成员行邮箱应与注册邮箱一致，实得 %q", row.Email)
	}
	// ③ 家行一字未动（不停用、不释放邮箱、口令还是原来那把）
	after := joinRow(t, f, home.ID, home.TenantID)
	if after.Status != store.UserActive {
		t.Fatalf("家行不该被停用（这是加入，不是升级），实得 %q", after.Status)
	}
	if after.PasswordHash != home.PasswordHash || !auth.CheckPassword(after.PasswordHash, identPassword) {
		t.Fatal("家行口令被改动")
	}
	if after.IsHome != 1 {
		t.Fatalf("家行仍是家行，实得 is_home=%d", after.IsHome)
	}
	// ④ 注册即登录：这一条 token 指向**企业行**（前端拿到就在这个身份里，不必再切一次）
	tok, _ := m["token"].(string)
	if tok == "" {
		t.Fatal("注册成功应直接回会话 token（IAM P0-4）")
	}
	cl, verr := auth.Verify(tok)
	if verr != nil {
		t.Fatalf("解析注册 token 失败: %v", verr)
	}
	if cl.UserID != uid || cl.TenantID != cTid {
		t.Fatalf("token 应指向新建的企业身份，实得 uid=%d tid=%d", cl.UserID, cl.TenantID)
	}
	// ⑤ 身份清单两条、家行在前（§6.5 前端切换器的出栈契约）
	ids := identArr(t, m)
	if len(ids) != 2 {
		t.Fatalf("加入企业后应回两条身份，实得 %d: %+v", len(ids), ids)
	}
	if homeItem, ok := idItem(t, m, home.TenantID); !ok || !homeItem["is_home"].(bool) {
		t.Fatalf("身份清单里家行缺失或 is_home 未置真: %+v", ids)
	}
	if coItem, ok := idItem(t, m, cTid); !ok || coItem["is_home"] == true {
		t.Fatalf("身份清单里企业行缺失或错标成家行: %+v", ids)
	}
	// ⑥ 审计：membership_create 是「一个人多了一份企业身份」的唯一留痕
	if n := joinCount(t, f, "SELECT COUNT(*) FROM audit_logs WHERE action='membership_create' AND user_id=?", uid); n != 1 {
		t.Fatalf("凭据门应记一条 membership_create 审计，实得 %d", n)
	}
	// ⑦ 码已烧、used_by 留痕
	burned, berr := f.srv.Store.GetInviteCodeByCode("J1CODE")
	if berr != nil {
		t.Fatalf("读邀请码失败: %v", berr)
	}
	if burned.Used != 1 || burned.UsedBy != "j1_staff" {
		t.Fatalf("成功加入应烧码并留使用人，实得 used=%d used_by=%q", burned.Used, burned.UsedBy)
	}
	// ⑧ R5：这一行落的是 tokens=0 的「不发放」标记，不是额度
	//   （quota_grants 没有 user_id 列，ref_id 才是那一笔的归因人）
	if n := joinCount(t, f, "SELECT COUNT(*) FROM user_trial_grants WHERE user_id=? AND tokens=0 AND source='membership_no_trial'", uid); n != 1 {
		t.Fatalf("企业身份应落一行 tokens=0 的 no_trial 标记，实得 %d", n)
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM quota_grants WHERE tenant_id=?", cTid); n != 0 {
		t.Fatalf("企业身份不许给公司钱包凭空加一份平台补贴额度，实得 quota_grants=%d", n)
	}
}

// ---- J2：他人 JWT + 被占用邮箱 ⇒ 历史那一句 409，且不烧码（§十一 2＋9）----

func TestJoin02OtherPersonHoldingCodeCannotClaimOccupiedEmail(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j2")
	victim := joinHomeAccount(t, f, "j2_victim", "j2-v@ex.com")
	attacker := joinHomeAccount(t, f, "j2_attacker", "j2-a@ex.com")
	joinMakeCode(t, f, "J2CODE", cTid)

	code, m := upgRegister(t, f, upgToken(t, attacker), "j2-v@ex.com", joinMemberBody("j2_evil", "J2CODE"))
	if code != http.StatusConflict {
		t.Fatalf("他人持码冒领邮箱应 409，实得 code=%d body=%v", code, m)
	}
	// 防枚举：未证明邮箱归你时，出栈的必须是**改造前那一句**，不许泄露"这个邮箱在哪家公司"
	if got := joinMsg(m); got != "该邮箱已被其他账号绑定" {
		t.Fatalf("拒因应是历史那句邮箱被占用，实得 %q", got)
	}
	if got := joinCount(t, f, "SELECT COUNT(*) FROM users WHERE email=? AND tenant_id=?", "j2-v@ex.com", cTid); got != 0 {
		t.Fatalf("被拒请求不该在目标企业建出任何行，实得 %d", got)
	}
	if v := joinRow(t, f, victim.ID, victim.TenantID); v.Status != store.UserActive {
		t.Fatalf("被拒请求不该动受害者账号，实得 %q", v.Status)
	}
	joinCodeStillOpen(t, f, "J2CODE")
}

// ---- J3：本人已是该企业成员 ⇒ 400 点名下一步，且不烧码 ----

func TestJoin03AlreadyMemberToldToSwitchIdentity(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j3")
	home := joinHomeAccount(t, f, "j3_home", "j3-a@ex.com")
	// 第一次真实加入（烧掉第一张码）
	joinMakeCode(t, f, "J3CODE1", cTid)
	c1, m1 := upgRegister(t, f, upgToken(t, home), "j3-a@ex.com", joinMemberBody("j3_staff", "J3CODE1"))
	if c1 != http.StatusOK {
		t.Fatalf("首次加入应放行，实得 code=%d body=%v", c1, m1)
	}
	// 第二张码：同一邮箱再打一次同一企业 ⇒ 按租户那把尺子（D3）命中已在册成员
	joinMakeCode(t, f, "J3CODE2", cTid)
	c2, m2 := upgRegister(t, f, upgToken(t, home), "j3-a@ex.com", joinMemberBody("j3_staff2", "J3CODE2"))
	if c2 != http.StatusBadRequest {
		t.Fatalf("已在该企业应回 400（本人已证明邮箱归属，说得出实话），实得 code=%d body=%v", c2, m2)
	}
	if got := joinMsg(m2); got != "你已在该企业中，可直接切换身份" {
		t.Fatalf("拒因应点名「去切换身份」，实得 %q", got)
	}
	joinCodeStillOpen(t, f, "J3CODE2")
	if n := joinCount(t, f, "SELECT COUNT(*) FROM users WHERE email=? AND tenant_id=?", "j3-a@ex.com", cTid); n != 1 {
		t.Fatalf("重复加入不该在同一企业再建一行，实得 %d", n)
	}
}

// ---- J4：§6.3 删除静默降级——每一档无效凭据各回一句、且什么都不建 ----

func TestJoin04InvalidCredentialRejectedExplicitly(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j4")
	// 基线读数现取（夹具预置了几家租户不由本判据关心）：这一组要问的是"无效凭据有没有多出东西"，
	// 写死绝对数就会在别人加夹具那天变成假红。
	usersBefore := joinCount(t, f, "SELECT COUNT(*) FROM users")
	tenantsBefore := joinCount(t, f, "SELECT COUNT(*) FROM tenants")
	// 一次性邮箱黑名单之外的域名由 upgRegister 传入；本组全用 @ex.com（与既有夹具同族）。
	cases := []struct {
		name       string
		build      func() (body map[string]any, email string)
		wantMsg    string
		wantStatus int
	}{
		{"无效码", func() (map[string]any, string) {
			return joinMemberBody("j4_a", "NOSUCHCODE_42"), "j4-a@ex.com"
		}, "邀请码无效或已使用", http.StatusBadRequest},
		{"member 没带码", func() (map[string]any, string) {
			b := joinMemberBody("j4_b", "")
			b["role_choice"] = "member"
			return b, "j4-b@ex.com"
		}, "请填写企业邀请码后再注册", http.StatusBadRequest},
		{"personal 带企业码", func() (map[string]any, string) {
			joinMakeCode(t, f, "J4C", cTid)
			b := map[string]any{"username": "j4_c", "password": joinPassword, "type": "personal",
				"invite": "J4C", "agreed": true}
			return b, "j4-c@ex.com"
		}, "个人注册不能使用企业邀请码", http.StatusBadRequest},
		{"未绑定企业的老码", func() (map[string]any, string) {
			joinMakeCode(t, f, "J4D", 0)
			return joinMemberBody("j4_d", "J4D"), "j4-d@ex.com"
		}, "该邀请码未绑定企业，无法用于加入", http.StatusBadRequest},
	}
	for _, tc := range cases {
		body, email := tc.build()
		code, m := upgRegister(t, f, "", email, body)
		if code != tc.wantStatus {
			t.Fatalf("%s：应回 %d，实得 code=%d body=%v", tc.name, tc.wantStatus, code, m)
		}
		if got := joinMsg(m); got != tc.wantMsg {
			t.Fatalf("%s：拒因文案不符，期望 %q 实得 %q", tc.name, tc.wantMsg, got)
		}
	}
	// 什么都不许被建出来：这四档既没建用户，也没建租户（静默降级"另建一家企业"正是被删掉的那条路）
	if got := joinCount(t, f, "SELECT COUNT(*) FROM users"); got != usersBefore {
		t.Fatalf("无效凭据一组不该建出任何账号，实得 users=%d（基线 %d）", got, usersBefore)
	}
	if got := joinCount(t, f, "SELECT COUNT(*) FROM tenants"); got != tenantsBefore {
		t.Fatalf("无效凭据一组不该新建租户，实得 tenants=%d（基线 %d）", got, tenantsBefore)
	}
}

// TestJoin05UsedRevokedExpiredCodeEachOwnMessage 三档"曾经有效的码"各回一句可行动文案。
// 语义取向和 §6.3 同一句话：一张坏码给人三种说法，人才知道下一步是问管理员要哪一种。
func TestJoin05UsedRevokedExpiredCodeEachOwnMessage(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j5")

	// used：先把一枚码正常烧掉（本人无家行 ⇒ joinNewHome），再拿同一枚码提交第二次
	joinMakeCode(t, f, "J5USED", cTid)
	c1, m1 := upgRegister(t, f, "", "j5-used@ex.com", joinMemberBody("j5_u", "J5USED"))
	if c1 != http.StatusOK {
		t.Fatalf("首次持有效码应放行，实得 code=%d body=%v", c1, m1)
	}
	c2, m2 := upgRegister(t, f, "", "j5-used2@ex.com", joinMemberBody("j5_u2", "J5USED"))
	if c2 != http.StatusBadRequest || joinMsg(m2) != "该邀请码已被使用，请向企业管理员重新获取" {
		t.Fatalf("二次使用应点名「已被使用」，实得 code=%d msg=%q", c2, joinMsg(m2))
	}

	// revoked：管理员撤销
	revC := joinMakeCode(t, f, "J5REV", cTid)
	if ok, _, err := f.srv.Store.RevokeInviteCode(revC.ID); err != nil || !ok {
		t.Fatalf("预置撤销失败 ok=%v err=%v", ok, err)
	}
	c3, m3 := upgRegister(t, f, "", "j5-rev@ex.com", joinMemberBody("j5_r", "J5REV"))
	if c3 != http.StatusBadRequest || joinMsg(m3) != "该邀请码已被管理员撤销，请向企业管理员重新获取" {
		t.Fatalf("撤销码应点名「已被撤销」，实得 code=%d msg=%q", c3, joinMsg(m3))
	}

	// expired：expires_at 落在过去（存量码 expires_at='' ⇒ 不过期，这里显式喂一个到期时刻）
	expC := joinMakeCode(t, f, "J5EXP", cTid)
	if _, err := f.srv.Store.DB().Exec("UPDATE invite_codes SET expires_at=? WHERE id=?",
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), expC.ID); err != nil {
		t.Fatalf("预置过期时刻失败: %v", err)
	}
	c4, m4 := upgRegister(t, f, "", "j5-exp@ex.com", joinMemberBody("j5_e", "J5EXP"))
	if c4 != http.StatusBadRequest || joinMsg(m4) != "该邀请码已过期，请向企业管理员重新获取" {
		t.Fatalf("过期码应点名「已过期」，实得 code=%d msg=%q", c4, joinMsg(m4))
	}
}

// TestJoin06AdminChoiceIgnoresCodeWithoutBurning role_choice=admin 带码＝"我要新建企业"：
// 沿用旧行为忽略那枚码（不烧），建的是**自己那家**公司。
// 这条腿钉的是"不烧客户资产"——管理员手里那张码本来还要给下一个员工用。
func TestJoin06AdminChoiceIgnoresCodeWithoutBurning(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j6")
	joinMakeCode(t, f, "J6CODE", cTid)

	code, m := upgRegister(t, f, "", "j6-a@ex.com", joinAdminBody(t, "j6_boss", "own_j6", "J6CODE"))
	if code != http.StatusOK {
		t.Fatalf("管理员新建企业应放行，实得 code=%d body=%v", code, m)
	}
	if num(m, "tenant_id") == cTid {
		t.Fatal("role_choice=admin 不该被那枚码带进别人公司")
	}
	if got := joinMsg(m); got != "注册成功，试用额度已发放" {
		t.Fatalf("新建企业的成功语仍是发额度那句，实得 %q", got)
	}
	if _, has := m["identities"]; has {
		t.Fatal("非凭据门注册不该出栈 identities（账户只有一行，回它是无信息量字段）")
	}
	joinCodeStillOpen(t, f, "J6CODE")
}

// ---- J7：本人空白家行 + 有效码 ⇒ 补全家行 + 挂成员行，一条都不停（§十一 6）----

func TestJoin07BlankHomeCompletedNotDisabled(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j7")
	blank := upgBlankAccount(t, f, "j7-a@ex.com") // 主站邮箱登录自动建出来的空白号
	joinMakeCode(t, f, "J7CODE", cTid)

	// 补全面板里改过名字 ⇒ 家行用户名以本次请求为准（旧形态空白号用的是邮箱前缀）
	code, m := upgRegister(t, f, upgToken(t, blank), "j7-a@ex.com", joinMemberBody("j7_chosen", "J7CODE"))
	if code != http.StatusOK {
		t.Fatalf("空白号凭码加入应放行，实得 code=%d body=%v", code, m)
	}
	if num(m, "tenant_id") != cTid {
		t.Fatalf("应落在企业租户，实得 %v", m["tenant_id"])
	}
	// ① 家行还在、仍是 active（★ 与"升级注册先停用旧号"那一条路的关键差别）
	home := joinRow(t, f, blank.ID, blank.TenantID)
	if home.Status != store.UserActive {
		t.Fatalf("补全通道不停用家行，实得 %q", home.Status)
	}
	if home.PasswordHash == "" || !auth.CheckPassword(home.PasswordHash, joinPassword) {
		t.Fatal("家行应补上本次自设口令（口令只存家行，D4）")
	}
	if home.ProfileSetupRequired != 0 {
		t.Fatalf("补全后待补全标记应清零，实得 %d", home.ProfileSetupRequired)
	}
	if home.Username != "j7_chosen" {
		t.Fatalf("家行用户名以本次请求为准，实得 %q", home.Username)
	}
	if home.IsHome != 1 {
		t.Fatalf("家行仍是家行，实得 is_home=%d", home.IsHome)
	}
	// ② 另挂一条成员行（同一账户号、is_home=0、不带口令）
	uid := int64(joinRespUser(t, m)["id"].(float64))
	row := joinRow(t, f, uid, cTid)
	if row.IsHome != 0 || row.AccountID != blank.AccountID || row.PasswordHash != "" {
		t.Fatalf("成员行形状不符 is_home=%d account_id=%q pwd=%d", row.IsHome, row.AccountID, len(row.PasswordHash))
	}
	// ③ 审计两条各说一件事；这条路径**不写** account_upgrade（那是"停用旧号"那一族的留痕）
	if n := joinCount(t, f, "SELECT COUNT(*) FROM audit_logs WHERE action='profile_setup' AND user_id=?", blank.ID); n != 1 {
		t.Fatalf("补全家行应记一条 profile_setup，实得 %d", n)
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM audit_logs WHERE action='membership_create' AND user_id=?", uid); n != 1 {
		t.Fatalf("加入企业应记一条 membership_create，实得 %d", n)
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM audit_logs WHERE action='account_upgrade'"); n != 0 {
		t.Fatalf("凭码加入通道不该写「升级停用」审计，实得 %d", n)
	}
	// ④ R5：新身份不带体验额度（补全的是家行，发的额度也只在个人侧，见 J6 组）
	if n := joinCount(t, f, "SELECT COUNT(*) FROM user_trial_grants WHERE user_id=? AND tokens=0 AND source='membership_no_trial'", uid); n != 1 {
		t.Fatalf("企业身份应落 no_trial 标记，实得 %d", n)
	}
}

// ---- J8：额度两档（个人行发一次 / 企业行永不发，含登录补发腿反证，§十一 11）----

func TestJoin08TrialOnlyForPersonalIdentity(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j8")

	// 个人注册（无凭据）⇒ 台账 tokens>0 且额度真落进 quota_grants（ref_id＝归因人）
	pc, pm := upgRegister(t, f, "", "j8-p@ex.com", map[string]any{
		"username": "j8_person", "password": joinPassword, "type": "personal", "agreed": true})
	if pc != http.StatusOK {
		t.Fatalf("个人注册应放行，实得 code=%d body=%v", pc, pm)
	}
	personUID := int64(joinRespUser(t, pm)["id"].(float64))
	if n := joinCount(t, f, "SELECT COUNT(*) FROM user_trial_grants WHERE user_id=? AND tokens>0", personUID); n != 1 {
		t.Fatalf("个人注册应发放一次体验额度，实得台账 %d 行", n)
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM quota_grants WHERE ref_id=? AND kind='trial'", personUID); n != 1 {
		t.Fatalf("个人注册的额度应真落进 quota_grants，实得 %d", n)
	}

	// 企业身份（凭码加入）⇒ 台账 tokens=0、quota_grants 零行
	home := joinHomeAccount(t, f, "j8_home", "j8-a@ex.com")
	joinMakeCode(t, f, "J8CODE", cTid)
	jc, jm := upgRegister(t, f, upgToken(t, home), "j8-a@ex.com", joinMemberBody("j8_staff", "J8CODE"))
	if jc != http.StatusOK {
		t.Fatalf("凭码加入应放行，实得 code=%d body=%v", jc, jm)
	}
	coUID := int64(joinRespUser(t, jm)["id"].(float64))
	if n := joinCount(t, f, "SELECT COUNT(*) FROM quota_grants WHERE ref_id=?", coUID); n != 0 {
		t.Fatalf("企业身份不该有额度授予行，实得 %d", n)
	}

	// ★ 反证：登录补发腿亲自打一次——tokens=0 那一行是**持久事实**，不是"这一次没调用发放"
	//   （补发腿的判据是「这行从没在 user_trial_grants 里出现过」，只砍注册那条腿救不了它）
	coRow := joinRow(t, f, coUID, cTid)
	f.srv.autoGrantUserTrial(context.Background(), coRow)
	if n := joinCount(t, f, "SELECT COUNT(*) FROM quota_grants WHERE ref_id=?", coUID); n != 0 {
		t.Fatalf("登录补发腿给企业身份补出了额度，实得 %d", n)
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM user_trial_grants WHERE user_id=? AND tokens=0", coUID); n != 1 {
		t.Fatalf("补发腿不许改写那行 no_trial 标记，实得 %d", n)
	}
}

// ---- J9：邀请链接全套（§十一 12）----

func TestJoin09InviteLinkDoor(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j9")

	// ① 创建端**不建 users 行**：pending 成员完全活在邀请表里
	if n := joinCount(t, f, "SELECT COUNT(*) FROM users"); n != 0 {
		t.Fatalf("夹具开局不该有账号行，实得 %d", n)
	}
	linkCode := "inv_" + "0123456789abcdef0123456789abcdef"
	link := joinMakeLink(t, f, linkCode, cTid, 0, store.RoleUser, "bob", "j9-bound@ex.com", "")
	if link.Kind != "link" {
		t.Fatalf("链接凭据 kind 应为 link，实得 %q", link.Kind)
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM users"); n != 0 {
		t.Fatalf("生成邀请链接不该建出 users 行（pending 不落账号表），实得 %d", n)
	}

	// ② 预绑邮箱是最强绑定：换邮箱 ⇒ 400，且码不被烧
	c1, m1 := upgRegister(t, f, "", "j9-other@ex.com", joinMemberBody("j9_x", linkCode))
	if c1 != http.StatusBadRequest || joinMsg(m1) != "该邀请链接只能由指定邮箱注册，请更换邮箱或向管理员重新获取" {
		t.Fatalf("邮箱与预绑不符应 400，实得 code=%d msg=%q", c1, joinMsg(m1))
	}
	joinCodeStillOpen(t, f, linkCode)

	// ③ 指定邮箱提交 ⇒ 建号 + 烧码；纯企业用户 ⇒ 这一行自己就是家行（D2）
	c2, m2 := upgRegister(t, f, "", "j9-bound@ex.com", joinMemberBody("j9_bob", linkCode))
	if c2 != http.StatusOK {
		t.Fatalf("预绑邮箱命中应放行，实得 code=%d body=%v", c2, m2)
	}
	uid := int64(joinRespUser(t, m2)["id"].(float64))
	row := joinRow(t, f, uid, cTid)
	if row.IsHome != 1 {
		t.Fatalf("纯企业用户的首行应是自己那个账户的家行，实得 is_home=%d", row.IsHome)
	}
	if row.AccountID == "" || row.AccountID != strconv.FormatInt(uid, 10) {
		t.Fatalf("新家行账户号应钉成自身 id，实得 %q", row.AccountID)
	}
	if row.PasswordHash == "" || !auth.CheckPassword(row.PasswordHash, joinPassword) {
		t.Fatal("家行应带上用户自设口令")
	}
	if row.Role != store.RoleUser {
		t.Fatalf("预绑角色应落进落库那一行，实得 %q", row.Role)
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM user_trial_grants WHERE user_id=? AND tokens=0 AND source='membership_no_trial'", uid); n != 1 {
		t.Fatalf("链接建出来的企业身份同样不发额度，实得 %d", n)
	}
	burned, berr := f.srv.Store.GetInviteLinkByCode(linkCode)
	if berr != nil || burned == nil || burned.Used != 1 {
		t.Fatalf("链接注册即烧码，实得 %+v err=%v", burned, berr)
	}

	// ④ 一枚链接只开一个账号：第二次提交吃"已被使用"那一句（与落地页同一把尺子）
	c3, m3 := upgRegister(t, f, "", "j9-bound@ex.com", joinMemberBody("j9_bob2", linkCode))
	if c3 != http.StatusBadRequest || joinMsg(m3) != "该邀请链接已被使用（一枚链接只能开通一个账号），请向企业管理员重新获取" {
		t.Fatalf("二次提交应点名「一枚链接只能开通一个账号」，实得 code=%d msg=%q", c3, joinMsg(m3))
	}

	// ⑤ 过期链接 ⇒ 链接那一句（不是码那一句）
	expLink := joinMakeLink(t, f, "inv_"+"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", cTid, 0, "", "", "",
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339))
	c4, m4 := upgRegister(t, f, "", "j9-exp@ex.com", joinMemberBody("j9_e", expLink.Code))
	if c4 != http.StatusBadRequest || joinMsg(m4) != "该邀请链接已过期，请向企业管理员重新获取" {
		t.Fatalf("过期链接应回链接档位文案，实得 code=%d msg=%q", c4, joinMsg(m4))
	}

	// ⑥ 撤销链接 ⇒ 同上，四档文案各归各
	revLink := joinMakeLink(t, f, "inv_"+"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", cTid, 0, "", "", "", "")
	if ok, _, err := f.srv.Store.RevokeInviteCode(revLink.ID); err != nil || !ok {
		t.Fatalf("预置链接撤销失败 ok=%v err=%v", ok, err)
	}
	c5, m5 := upgRegister(t, f, "", "j9-rev@ex.com", joinMemberBody("j9_r", revLink.Code))
	if c5 != http.StatusBadRequest || joinMsg(m5) != "该邀请链接已被管理员撤销，请向企业管理员重新获取" {
		t.Fatalf("撤销链接应回撤销档位文案，实得 code=%d msg=%q", c5, joinMsg(m5))
	}
}

// ---- J10：两套邀请彼此隔离（§十一 13）----

func TestJoin10ReferralStaysSeparateFromEnterpriseDoor(t *testing.T) {
	f := f77Fixture(t)
	cTid := joinEnterpriseTenant(t, f, "co_j10")
	joinMakeCode(t, f, "J10CODE", cTid)

	// 邀请人：一个已存在的个人版账号，带裂变码 ref_code
	inviter := joinHomeAccount(t, f, "j10_inviter", "j10-inv@ex.com")
	if _, err := f.srv.Store.DB().Exec("UPDATE users SET ref_code=? WHERE id=?", "J10REF", inviter.ID); err != nil {
		t.Fatalf("预置裂变码失败: %v", err)
	}

	body := map[string]any{"username": "j10_new", "password": joinPassword, "type": "personal",
		"ref": "J10REF", "agreed": true}
	code, m := upgRegister(t, f, "", "j10-new@ex.com", body)
	if code != http.StatusOK {
		t.Fatalf("只带裂变码的个人注册应放行，实得 code=%d body=%v", code, m)
	}
	// ① 裂变归属照落（这一条腿没被企业门改动碰坏）
	uid := int64(joinRespUser(t, m)["id"].(float64))
	if n := joinCount(t, f, "SELECT COUNT(*) FROM users WHERE id=? AND referred_by=?", uid, inviter.ID); n != 1 {
		t.Fatalf("仅带 ref 应落奖励归属，实得 %d", n)
	}
	// ② 但**没有**企业身份：出栈没有 identities、审计没有 membership_create、
	//    目标企业的码一张没烧、企业租户里一行没建
	if _, has := m["identities"]; has {
		t.Fatalf("个人注册不许出栈 identities（两套邀请各走各的门）: %v", m["identities"])
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM audit_logs WHERE action='membership_create'"); n != 0 {
		t.Fatalf("仅 ref 注册不该记 membership_create，实得 %d", n)
	}
	if n := joinCount(t, f, "SELECT COUNT(*) FROM users WHERE tenant_id=?", cTid); n != 0 {
		t.Fatalf("仅 ref 注册不该在目标企业建出行，实得 %d", n)
	}
	joinCodeStillOpen(t, f, "J10CODE")
	// ③ 个人注册拿的是体验额度（企业门才不发）
	if n := joinCount(t, f, "SELECT COUNT(*) FROM user_trial_grants WHERE user_id=? AND tokens>0", uid); n != 1 {
		t.Fatalf("个人注册应发放体验额度，实得 %d", n)
	}
}
