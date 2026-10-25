// ============ 本文件职责中文说明 ============
// login_identity_backfill_test.go · 「补建个人身份之后，按用户名登录必须还认得同一个人」。
//
// 缺陷本体（2026-10-25 本地 UAT A 段实测，SQLite 稳定复现）：
// R4 的登录补建（ensurePersonalIdentityOnLogin）会为纯企业账户补一条个人身份行，
// 而那一行**照抄家行的 username 与 email**、且按 D4 **不带口令哈希**。于是库里出现
// 两行同名同邮箱、account_id 相同的记录，按用户名取回的候选集从 1 条变成 2 条，
// 登录口那条既有分支「多命中 ⇒ 只认平台超管行，否则不猜人」把这同一个人的两个身份
// 读成了"两个不相干的人撞了名"，直接回 404「账号或密码错误」。
// 现网表现＝**第一次登录成功（就是它把第二行建出来的），第二次起再也登不进去**，
// 且界面给的是一句"账号或密码错误"——用户只会以为自己记错了口令。
//
// 修法与判据的分工（本文件两条用例各自钉一头，缺一头都会放行另一半缺陷）：
//
//	① 取人层按**账户**归一（iam/account.go collapseToAccounts）⇒ 同一人多身份 = 一条候选；
//	② 调用方「不同账户撞名不猜人」那条闸**一字未放宽** ⇒ 必须仍然挡住（负向对照）。
//
// ⚠️ 为什么既有测试抓不到这一条：identity_test.go 的 join() 夹具给成员行的用户名是
//
//	`home.Username+"-co"`（加了后缀），结构上永远不会撞上同名——夹具把缺陷形态绕开了。
//	所以本文件的成员行**照抄**用户名与邮箱（与产品那一行同源），这才是要锁的形态。
//
// 判据取向：全部走内存 SQLite ＋ 真实 /api/auth/login handler（同 identity_test.go 夹具），
// 并**由登录响应自己读出落到哪家租户**（AGENTS §三「判『生效』必须由那一个消费方自己读出值」），
// 只看"有没有 token"判不出"登进去的是家行还是空的成员行"。
// ★ 单测自钉 sqlite 方言（AGENTS.md §一·4），避免 run_uat 的 PG 模式泄漏给同包内存库用例。
// ==========================================
package api

import (
	"net/http"
	"testing"

	"translator/internal/auth"
	"translator/internal/store"
)

// ① 正向：**产品自己补建**的同名成员行落地之后，用户名登录与邮箱登录都仍必须 200，
//
//	且会话落在家行那一家。
//
// 刻意不手工造第二行——让第一枪走真实的 R4 补建（ensurePersonalIdentityOnLogin），
// 这样"成员行照抄用户名与邮箱"这一形态由产品代码自己写出来；夹具造的那一行只能证明
// "我想象中的形态"，而那正是本缺陷逃过既有测试的原因（join() 夹具给用户名加了后缀）。
func TestLoginBackfill_UsernameStillResolvesAfterSameNameIdentityRow(t *testing.T) {
	f := newIdentFixture(t)
	home, err := f.st.CreateUserWithEmail(f.cTID, "carlos", auth.PasswordHash(identPassword), "Carlos", store.RoleTenantAdmin, 0, 0, "carlos@example.test", "", 0)
	if err != nil {
		t.Fatalf("创建企业家行失败: %v", err)
	}

	// 第一枪：纯企业账户登录 ⇒ 产品在成功之后补建个人身份行（同名、同邮箱、无口令）。
	rec := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"username": "carlos", "password": identPassword}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("补建前的用户名登录应 200，实得 %d %s", rec.Code, rec.Body.String())
	}
	rows, err := f.st.ListIdentitiesByAccount(home.AccountID)
	if err != nil {
		t.Fatalf("读取账户身份失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("夹具前提失效：R4 应补建出第二条身份行（实得 %d 行）——没有同名行，本用例就没在测缺陷形态", len(rows))
	}
	var member *store.User
	for _, r := range rows {
		if r.ID != home.ID {
			member = r
		}
	}
	if member == nil || member.Username != home.Username || member.PasswordHash != "" {
		t.Fatalf("补建出来的成员行必须是同名、同邮箱、无口令（D4），实得 username=%v hash 非空=%v",
			member.Username, member.PasswordHash != "")
	}

	// ★ 缺陷复现点：修复前这一枪回 404「账号或密码错误」（两行同名 ⇒ 候选 2 条 ⇒ 不猜人分支）。
	rec2 := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"username": "carlos", "password": identPassword}, "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("补建同名成员行后用户名登录仍应 200，实得 %d %s（同一个人的两个身份被读成两个人撞名）",
			rec2.Code, rec2.Body.String())
	}
	body := decode(t, rec2)
	userObj, ok := body["user"].(map[string]interface{})
	if !ok {
		t.Fatalf("登录响应缺少 user 对象: %v", body)
	}
	// 归并取的是**凭证行**：会话必须落在企业家行那一家（成员行没有口令，落到它等于验不出人）。
	if got := num(userObj, "tenant_id"); got != f.cTID {
		t.Fatalf("多身份同名归并后必须登家行（tid=%d），实得 tenant_id=%v", f.cTID, got)
	}
	if got := num(userObj, "id"); got != home.ID {
		t.Fatalf("登录命中的行 id 应为家行 %d，实得 %v", home.ID, got)
	}
	if len(identArr(t, body)) != 2 {
		t.Fatalf("登录响应应带出两个身份（企业＋补建的个人行），实得 %d", len(identArr(t, body)))
	}

	// 同邮箱的两行同样不能被读成两个人：按邮箱登录那三条腿（lower 查＋家行在前）也要 200。
	rec3 := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": "carlos@example.test", "password": identPassword}, "")
	if rec3.Code != http.StatusOK {
		t.Fatalf("补建同名成员行后邮箱登录仍应 200，实得 %d %s", rec3.Code, rec3.Body.String())
	}
	if u3, ok := decode(t, rec3)["user"].(map[string]interface{}); !ok || num(u3, "tenant_id") != f.cTID {
		t.Fatalf("邮箱腿也必须落家行，实得 %v", decode(t, rec3)["user"])
	}
}

// ② 负向对照：修法只归并**同一账户**的多行；不同账户撞名那条"不猜人"的闸一字未放宽。
// 没有这一条，任何人把 collapseToAccounts 写成"同名一律取第一行"都能让 ① 变绿，
// 而那会把"两个人共用一个用户名"变成"谁都能登进排在第一个的那个人"。
func TestLoginBackfill_DifferentAccountsSameUsernameStillRejected(t *testing.T) {
	f := newIdentFixture(t)
	// 两个**各自成家**的账户（account_id 必然不同）落在两个租户、共用同一个用户名。
	a, err := f.st.CreateUserWithEmail(f.cTID, "dupman", auth.PasswordHash(identPassword), "A", store.RoleUser, 0, 0, "dup_a@example.test", "", 0)
	if err != nil {
		t.Fatalf("创建账户 A 失败: %v", err)
	}
	b, err := f.st.CreateUserWithEmail(f.pTID, "dupman", auth.PasswordHash(identPassword), "B", store.RoleUser, 0, 0, "dup_b@example.test", "", 0)
	if err != nil {
		t.Fatalf("创建账户 B 失败: %v", err)
	}
	if a.AccountID == b.AccountID {
		t.Fatalf("夹具前提失效：两个独立账户不得共享账户号（A=%s B=%s）", a.AccountID, b.AccountID)
	}
	rec := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"username": "dupman", "password": identPassword}, "")
	if rec.Code == http.StatusOK {
		t.Fatalf("不同账户撞名必须仍判歧义（不猜人），实得 200：%s", rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不同账户撞名应回 404「账号或密码错误」，实得 %d %s", rec.Code, rec.Body.String())
	}
	// 各自的邮箱腿不受影响：同名不等于同一个人，但按邮箱仍能各自登进去。
	if r := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": "dup_a@example.test", "password": identPassword}, ""); r.Code != http.StatusOK {
		t.Fatalf("账户 A 的邮箱腿应 200，实得 %d %s", r.Code, r.Body.String())
	}
	if r := f.call(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": "dup_b@example.test", "password": identPassword}, ""); r.Code != http.StatusOK {
		t.Fatalf("账户 B 的邮箱腿应 200，实得 %d %s", r.Code, r.Body.String())
	}
}
