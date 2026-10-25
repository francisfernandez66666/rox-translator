// ============ 本文件职责中文说明 ============
// auth_identity_r4_test.go · 多身份账号方案 §四／§十三 R4 的机械锁：
// **纯企业账户（家行在企业租户、账户名下没有任何个人身份）登录时自动补建一条个人身份行**。
//
// 这一条是方案对账（2026-10-25）里唯一"整条无代码痕迹"的功能项，所以断言按方案点名的
// §十一 case 10 写，并补齐四条反证：
//
//	① 登录出口真的补建：响应 identities 多出一条 is_personal=true，且**会话仍落企业行**
//	   （补建是顺手做的第一件事，不许把人从自己刚登录进去的那一家踢出来）；
//	② 幂等：重复登录只长**一家**个人租户、只留一行个人身份（编码＝pa_<账户号>，
//	   由 tenants.code 唯一索引当原子守卫；时间戳编码做不到这一条）；
//	③ 钱的口径 A：账户已经领过**真实**补贴（tokens>0）⇒ 补建的个人行只落 tokens=0 占位账，
//	   绝不出第二份 quota_grants（自助注册的企业管理员注册时已领一份，是本条的现网形态）；
//	④ 钱的口径 B：新行继承家行 created_by ⇒ 批量导入的历史员工（created_by>0）补建出的
//	   个人身份同样拿不到补贴（trial_auto.go「created_by≠0 不发放」那条规则不许被补建绕过）；
//	⑤ 六道守卫逐一判：成员行会话、家行已在个人租户、未迁移行（account_id 空）一律什么都不做。
//
// 判据取向与 identity_test.go 同源：内存 SQLite ＋真实 handler（打 /api/auth/login），
// 因为这一族缺陷在**接线**上——补建挂在 writeLoginResponse 唯一出口才同时覆盖口令腿与
// 邮箱验证码腿，挂在任一 handler 里都会留一条"那条腿不补"的空洞。
// ★ 单测自钉 sqlite 方言（AGENTS.md §一·4）。
// ⚠️ 修改此处请同步更新本说明。
// ==========================================
package api

import (
	"context"
	"net/http"
	"testing"

	"translator/internal/auth"
	"translator/internal/store"
)

// r4Count 现读一个计数（本文件所有"补了几家/发了几份"的判据都走它，避免各处手抄 SQL）。
func (f *identFixture) r4Count(t *testing.T, query string, args ...interface{}) int64 {
	t.Helper()
	var n int64
	if err := f.raw.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("计数失败 [%s]: %v", query, err)
	}
	return n
}

// r4EnterpriseHome 造一个"纯企业账户"的家行：家行落在企业租户、带口令与邮箱，
// 并补上注册链路那一条 R5 标记（企业身份不送体验额度，tokens=0 占位）。
// ⚠️ 必须一起标：现网经邀请链接建出来的企业身份**都带着这一行**，
// 不标就等于造了一个生产里不存在的形态，③④ 两条钱的判据会跟着失真。
func (f *identFixture) r4EnterpriseHome(t *testing.T, username, email string) *store.User {
	t.Helper()
	home, err := f.st.CreateUserWithEmail(f.cTID, username, auth.PasswordHash(identPassword),
		username, store.RoleTenantAdmin, 0, 0, email, "", 0)
	if err != nil {
		t.Fatalf("创建企业家行失败: %v", err)
	}
	f.srv.markTrialNotGranted(context.Background(), home)
	return home
}

// r4CreatorUID 测试里当作"导入操作人"的用户 ID（created_by>0 即"非自助注册"形态）。
const r4CreatorUID = 4242

// r4Personal 在身份清单里找出个人版那一条，返回 (tid, uid)；找不到回 (0,0) 由调用方判红。
func r4Personal(t *testing.T, list []interface{}) (int64, int64) {
	t.Helper()
	for _, it := range list {
		if m, ok := it.(map[string]interface{}); ok && m["is_personal"] == true {
			return num(m, "tid"), num(m, "uid")
		}
	}
	return 0, 0
}

// ---- ① 登录出口真的补建，且会话仍落企业行 ----

func TestIdentityR401PureEnterpriseLoginBuildsPersonalIdentity(t *testing.T) {
	f := newIdentFixture(t)
	home := f.r4EnterpriseHome(t, "carla", "carla@example.test")

	rec := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": "carla@example.test", "password": identPassword}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)

	// 会话仍在企业那一行：补建是"多给一个身份"，不是"把人换到另一个身份上"。
	// 前端拿 user.tenant_id 定位当前上下文，这里若跟着新行走，用户会被踢回登录页。
	if userObj, ok := body["user"].(map[string]interface{}); !ok || num(userObj, "tenant_id") != f.cTID {
		t.Fatalf("登录响应 user 必须仍落企业租户 %d，实得 %v", f.cTID, body["user"])
	}

	// 身份清单里必须已经带上补建那一行（identitiesFor 现读库 ⇒ 本次响应就该看见）。
	list := identArr(t, body)
	if len(list) != 2 {
		t.Fatalf("补建后登录响应应带 2 条身份（企业＋个人），实得 %d：%v", len(list), list)
	}
	pTID, pUID := r4Personal(t, list)
	if pTID <= 0 || pUID <= 0 {
		t.Fatalf("身份清单里没有 is_personal=true 那一条（补建没走到出栈）：%v", list)
	}
	// 家仍留在企业那一行（D4：口令只在家行；切换后才进个人行）。
	if got := f.mustGet(t, home.ID, f.cTID); got.IsHome != 1 {
		t.Fatalf("补建不许动家行归属，企业行 is_home 应为 1，实得 %d", got.IsHome)
	}
	prow := f.mustGet(t, pUID, pTID)
	if prow.IsHome != 0 {
		t.Fatalf("补建出来的个人身份必须是成员行（is_home=0），实得 %d", prow.IsHome)
	}
	if prow.PasswordHash != "" {
		t.Fatalf("成员行不许带口令哈希（方案 D4），实得长度 %d", len(prow.PasswordHash))
	}
	if prow.Role != store.RoleTenantAdmin {
		t.Fatalf("个人租户里他自己是管理员（与自助注册同形），实得 %q", prow.Role)
	}
	// 租户编码＝账户号派生，且打了个人标记。
	pt, err := f.ten.GetByID(pTID)
	if err != nil || pt == nil {
		t.Fatalf("补建的个人租户读不到: %v", err)
	}
	if pt.Code != personalTenantCode(home.AccountID) {
		t.Fatalf("个人租户编码必须由账户号派生（原子守卫），期望 %q 实得 %q", personalTenantCode(home.AccountID), pt.Code)
	}
	if !pt.IsPersonal {
		t.Fatalf("补建的个人租户必须标 is_personal（决定额度语义与切换器名字）")
	}
	// 补贴：企业行那一笔是 tokens=0 占位 ⇒ 这个账户还没领过真补贴 ⇒ 个人行领到一份。
	if n := f.r4Count(t, `SELECT COUNT(*) FROM user_trial_grants WHERE user_id=? AND tokens>0`, pUID); n != 1 {
		t.Fatalf("补建的个人身份应拿到一份真实体验补贴，实得 %d 行", n)
	}
	if n := f.r4Count(t, `SELECT COUNT(*) FROM quota_grants WHERE ref_id=?`, pUID); n != 1 {
		t.Fatalf("体验补贴必须落一条额度台账，实得 %d 行", n)
	}
	// 默认 API Key 挂在新身份行上（自助注册的个人用户拿到的是"登录即可用"的一家）。
	if n := f.r4Count(t, `SELECT COUNT(*) FROM api_keys WHERE tenant_id=? AND user_id=?`, pTID, pUID); n != 1 {
		t.Fatalf("补建的个人身份应有默认 API Key（挂在个人行，不是企业行），实得 %d 行", n)
	}
}

// ---- ② 幂等：重复登录只长一家 ----

func TestIdentityR402RepeatLoginBuildsOnlyOne(t *testing.T) {
	f := newIdentFixture(t)
	home := f.r4EnterpriseHome(t, "dora", "dora@example.test")

	for i := 0; i < 3; i++ {
		rec := f.call(t, http.MethodPost, "/api/auth/login",
			map[string]string{"email": "dora@example.test", "password": identPassword}, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次登录应 200，实得 %d %s", i+1, rec.Code, rec.Body.String())
		}
		if n := len(identArr(t, decode(t, rec))); n != 2 {
			t.Fatalf("第 %d 次登录的身份清单应恒为 2 条（幂等），实得 %d", i+1, n)
		}
	}
	// 编码唯一 ⇒ 全库只有一个 pa_<账户号>；这一条是"并发/重复登录不长出两家空租户"的唯一判据。
	if n := f.r4Count(t, `SELECT COUNT(*) FROM tenants WHERE code=?`, personalTenantCode(home.AccountID)); n != 1 {
		t.Fatalf("重复登录只该有一家个人租户，实得 %d", n)
	}
	if n := f.r4Count(t, `SELECT COUNT(*) FROM users WHERE account_id=?`, home.AccountID); n != 2 {
		t.Fatalf("重复登录只该长出 1 行（企业＋个人＝2 行），实得 %d", n)
	}
	// 补贴同样只有一份：三次登录不该把"一个账号仅一次"打成三次。
	if n := f.r4Count(t, `SELECT COUNT(*) FROM quota_grants g JOIN user_trial_grants t ON t.user_id=g.ref_id AND t.tokens>0 WHERE g.ref_id IN (SELECT id FROM users WHERE account_id=?)`, home.AccountID); n != 1 {
		t.Fatalf("同一账户的体验补贴只发一次，实得 %d 行", n)
	}
}

// ---- ③ 钱口径 A：账户已领过真实补贴 ⇒ 个人行不再领第二份 ----

func TestIdentityR403NoSecondSubsidy(t *testing.T) {
	f := newIdentFixture(t)
	home := f.r4EnterpriseHome(t, "erin", "erin@example.test")
	// 造出"这个账户已经拿到过真实补贴"的既有形态（自助注册的企业管理员就是这一形）。
	// ⚠️ 用 UPDATE 而不是 GrantUserTrialOnce：上面 r4EnterpriseHome 已经给这一行落了
	//   tokens=0 的占位账，发放函数按 user_id 主键判"已发过"直接回 false——
	//   那是发放腿的幂等，不是夹具要的"领过一份真补贴"。把那一行的 tokens 抬成真实读数，
	//   才与生产里"注册时发过补贴"的行在同一形态上（判据读的是 tokens>0）。
	f.rawExec(t, `UPDATE user_trial_grants SET tokens=5000, source='register' WHERE user_id=?`, home.ID)
	// 台账去重判据必须认得"tokens>0 才算领过"，tokens=0 的占位（backfill／membership_no_trial）不算。
	if has, herr := f.st.AccountHasRealTrialGrant(home.AccountID); herr != nil || !has {
		t.Fatalf("AccountHasRealTrialGrant 应读出该账户已领过真实补贴，got=%v err=%v", has, herr)
	}
	if has, herr := f.st.AccountHasRealTrialGrant(""); herr != nil || has {
		t.Fatalf("空账户号不许被判成「同一个账户」，got=%v err=%v", has, herr)
	}

	rec := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": "erin@example.test", "password": identPassword}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	list := identArr(t, decode(t, rec))
	if len(list) != 2 {
		t.Fatalf("应补建出个人身份，实得 %d 条：%v", len(list), list)
	}
	_, pUID := r4Personal(t, list)
	if pUID <= 0 {
		t.Fatalf("身份清单里没有个人那一条：%v", list)
	}
	// 个人行只落占位账（tokens=0），且**不出**额度台账——一人一份补贴的尺子守住。
	if n := f.r4Count(t, `SELECT COUNT(*) FROM user_trial_grants WHERE user_id=?`, pUID); n != 1 {
		t.Fatalf("已领过补贴的账户，补建行应落一行占位标记，实得 %d", n)
	}
	if n := f.r4Count(t, `SELECT COUNT(*) FROM user_trial_grants WHERE user_id=? AND tokens>0`, pUID); n != 0 {
		t.Fatalf("补建不许给已领过补贴的账户发第二份，实得 %d 行 tokens>0", n)
	}
	if n := f.r4Count(t, `SELECT COUNT(*) FROM quota_grants WHERE ref_id=?`, pUID); n != 0 {
		t.Fatalf("补建的第二份补贴不该出现额度台账，实得 %d 行", n)
	}
}

// ---- ④ 钱口径 B：批量导入（created_by>0）补建出的个人身份同样不领补贴 ----

func TestIdentityR404ImportedStaffGetsNoTrial(t *testing.T) {
	f := newIdentFixture(t)
	// 管理台批量导入的员工形态：created_by>0（体验额度规则「非自助注册不发放」）。
	home, err := f.st.CreateUserWithEmail(f.cTID, "fern", auth.PasswordHash(identPassword),
		"Fern", store.RoleUser, r4CreatorUID, 0, "fern@example.test", "", 0)
	if err != nil {
		t.Fatalf("创建导入员工家行失败: %v", err)
	}
	f.srv.markTrialNotGranted(context.Background(), home)

	rec := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": "fern@example.test", "password": identPassword}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	list := identArr(t, decode(t, rec))
	if len(list) != 2 {
		t.Fatalf("导入员工同样应补建个人身份（身份形态与企业员工一致），实得 %d 条", len(list))
	}
	pTID, pUID := r4Personal(t, list)
	prow := f.mustGet(t, pUID, pTID)
	if prow.CreatedBy != r4CreatorUID {
		t.Fatalf("补建行必须继承家行 created_by（否则批量导入的号靠补建绕过了发放规则），实得 %d", prow.CreatedBy)
	}
	if n := f.r4Count(t, `SELECT COUNT(*) FROM quota_grants WHERE ref_id=?`, pUID); n != 0 {
		t.Fatalf("继承 created_by 的补建行不该拿到补贴，实得 %d 行额度台账", n)
	}
}

// ---- ⑤ 六道守卫：不属于「纯企业账户首登」的形态一律什么都不做 ----

func TestIdentityR405GuardsSkipBuild(t *testing.T) {
	f := newIdentFixture(t)

	// (a) 家行已经在个人版租户（99% 的登录）：一次租户读取就该判完，不许长出第二家。
	alice := f.newAccount(t, "alice", "alice@example.test")
	f.srv.ensurePersonalIdentityOnLogin(context.Background(), alice)
	if n := f.r4Count(t, `SELECT COUNT(*) FROM users WHERE account_id=?`, alice.AccountID); n != 1 {
		t.Fatalf("个人版家行不该被补建（守卫 3），实得 %d 行", n)
	}

	// (b) 成员行会话（切换后的那一行）：不许凭空长出一家新租户。
	coHome, err := f.st.CreateUserWithEmail(f.cTID, "bob", auth.PasswordHash(identPassword),
		"Bob", store.RoleTenantAdmin, 0, 0, "bob@example.test", "", 0)
	if err != nil {
		t.Fatalf("创建企业家行失败: %v", err)
	}
	member := f.join(t, coHome, f.pTID, store.RoleUser) // 已有一条个人身份
	before := f.r4Count(t, `SELECT COUNT(*) FROM tenants`)
	f.srv.ensurePersonalIdentityOnLogin(context.Background(), member)
	if n := f.r4Count(t, `SELECT COUNT(*) FROM tenants`); n != before {
		t.Fatalf("成员行会话不许补建（守卫 1），租户数从 %d 变到 %d", before, n)
	}
	// (b2) 同一账户名下已有个人身份 ⇒ 幂等腿（守卫 4）也不许再建。
	f.srv.ensurePersonalIdentityOnLogin(context.Background(), coHome)
	if n := f.r4Count(t, `SELECT COUNT(*) FROM tenants`); n != before {
		t.Fatalf("账户已有个人身份时不许再补一家（守卫 4），租户数从 %d 变到 %d", before, n)
	}

	// (c) 未迁移行（account_id 为空）：保持改造前的单身份形态，不动数据。
	// 建号函数（CreateUser/CreateUserWithEmail）在 B1 起就会钉上 account_id，
	// 所以"未迁移"这一形态只能用裸 INSERT 造——它测的正是守卫 2 那条出厂判据。
	f.rawExec(t, `INSERT INTO users (tenant_id, username, password_hash, display_name, role, status, created_at, updated_at)
		VALUES (?, 'zoe', 'h', 'Zoe', 'tenant_admin', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, f.cTID)
	var legacyID int64
	if e := f.raw.QueryRow(`SELECT id FROM users WHERE username='zoe' AND tenant_id=?`, f.cTID).Scan(&legacyID); e != nil {
		t.Fatalf("取回未迁移行 ID 失败: %v", e)
	}
	lg := f.mustGet(t, legacyID, f.cTID)
	if lg.AccountID != "" {
		t.Fatalf("夹具失效：这一行应当是未迁移形态（account_id 空）")
	}
	tenBefore := f.r4Count(t, `SELECT COUNT(*) FROM tenants`)
	f.srv.ensurePersonalIdentityOnLogin(context.Background(), lg)
	if n := f.r4Count(t, `SELECT COUNT(*) FROM tenants`); n != tenBefore {
		t.Fatalf("未迁移行不许补建（守卫 2／B1 逐字节等价判据），租户数从 %d 变到 %d", tenBefore, n)
	}
	if n := f.r4Count(t, `SELECT COUNT(*) FROM users WHERE account_id IS NULL OR account_id=''`); n < 1 {
		t.Fatalf("未迁移行不该被动过，实得空账户号行数 %d", n)
	}

	// (d) 平台级账号（tid<=0）：不许长出租户。
	// 守卫 1 在碰任何存储之前就看 TenantID，所以这里喂一个手搓的 User 结构即可——
	// 它测的是"这一档压根不往下走"，不是"往下走但被某道读失败挡住"。
	plat := &store.User{ID: 900001, AccountID: "acc-root0", TenantID: 0, IsHome: 1, Role: store.RoleSuperAdmin}
	tenBefore2 := f.r4Count(t, `SELECT COUNT(*) FROM tenants`)
	f.srv.ensurePersonalIdentityOnLogin(context.Background(), plat)
	if n := f.r4Count(t, `SELECT COUNT(*) FROM tenants`); n != tenBefore2 {
		t.Fatalf("平台级账号不许补建个人租户（守卫 1），租户数从 %d 变到 %d", tenBefore2, n)
	}
}
