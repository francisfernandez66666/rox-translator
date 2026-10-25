// ============ account_ambiguity_test.go · 用户名重名不得静默选中一个 ============
// 新方案允许同租户重名，用户名不再是登录标识。存量兼容的「无 email 字段登录」若只取
// 一行就会登进任意账号（审计/会话都记到错的人头上），因此必须把全部同名候选交回调用方。
//
// ★ 2026-10-25 多身份补腿：交回的粒度是**账户**，不是**行**。
// 同一个人有多条身份行（家行＋成员行，同名同邮箱、口令只在家行）时必须归并成一条候选，
// 否则调用方那条"重名不猜人"的闸会把同一个人的两个身份读成两个人 ⇒
// 现网形态＝**第一次登录成功（正是它补建出第二行的），第二次起再也登不进去**。
// 两条用例各钉一头：归并过头（同名一律取第一行）会被下面的"不同账户仍交回 2 条"判红。
// =================================================================================

package iam

import (
	"testing"
)

func TestFindAccountForLoginReturnsAllDuplicateUsernames(t *testing.T) {
	st := newIAMTestStore(t)
	for _, name := range []string{"dup", "dup"} {
		if _, err := st.CreateUser(7, name, "hash", name, "user", 0, 0); err != nil {
			t.Fatalf("建同名账号失败: %v", err)
		}
	}
	got, kind, err := st.FindAccountForLogin(7, "dup")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("同租户重名应把 2 个候选都交回（由调用方拒绝歧义），实得 %d（kind=%s）", len(got), kind)
	}
	if kind != AccountByExact {
		t.Fatalf("kind 应为 %s，实得 %s", AccountByExact, kind)
	}
}

// 同一账户的多条身份行（R4 补建／受邀加入企业的形态）必须归并成一条候选，且取的是**凭证行**。
func TestFindAccountForLoginCollapsesSameAccountIdentityRows(t *testing.T) {
	st := newIAMTestStore(t)
	home, err := st.CreateUser(7, "nina", "hash-home", "Nina", "user", 0, 0)
	if err != nil {
		t.Fatalf("建家行失败: %v", err)
	}
	if !home.IsHomeRow() {
		t.Fatalf("夹具前提失效：CreateUser 的第一行应是家行")
	}
	// 成员行：照抄用户名、不带口令（D4）——这正是缺陷的触发形态。
	member, err := st.CreateIdentityRow(home.AccountID, 8, "nina", "Nina", "tenant_admin", 0, 0, "")
	if err != nil {
		t.Fatalf("建成员行失败: %v", err)
	}
	if member.PasswordHash != "" {
		t.Fatalf("夹具前提失效：成员行不得带口令哈希")
	}
	got, kind, err := st.FindAccountForLogin(0, "nina")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if kind != AccountByExact {
		t.Fatalf("kind 应为 %s，实得 %s", AccountByExact, kind)
	}
	if len(got) != 1 {
		t.Fatalf("同一账户的多身份行必须归并成 1 条候选（否则调用方按重名拒登），实得 %d", len(got))
	}
	if got[0].ID != home.ID {
		t.Fatalf("归并必须取凭证行（家行）：期望 uid=%d，实得 %d", home.ID, got[0].ID)
	}
	// 大小写腿同样要归并：这个人用「NINA」登（腿 1 精确等值取不到 ⇒ 走 CI 多命中），
	// 归并前同样是"两条候选 ⇒ 调用方按重名拒登"。
	got2, kind2, err := st.FindAccountForLogin(0, "NINA")
	if err != nil {
		t.Fatalf("大小写腿查询失败: %v", err)
	}
	if kind2 != AccountByCI {
		t.Fatalf("期望命中腿 %s，实得 %s", AccountByCI, kind2)
	}
	if len(got2) != 1 || got2[0].ID != home.ID {
		t.Fatalf("大小写腿也必须按账户归并到家行，实得 %v", idList(got2))
	}
}

// 组内没有家行时（数据异常/半态迁移）取**带口令**那一行，而不是一律取首行——
// 取首行会把"无口令的成员行"当成凭证行交给调用方，表现为"口令是对的却登不进"。
func TestCollapseToAccountsFallsBackToCredentialRow(t *testing.T) {
	st := newIAMTestStore(t)
	home, err := st.CreateUser(7, "otto", "hash-home", "Otto", "user", 0, 0)
	if err != nil {
		t.Fatalf("建家行失败: %v", err)
	}
	// 成员行落在**更小租户号**上：GetUserByUsernameGlobal 是 ORDER BY tenant_id，
	// 于是无口令那一行排在前面——回落腿才有射程（否则首行本来就是家行，判据空转）。
	member, err := st.CreateIdentityRow(home.AccountID, 1, "otto", "Otto", "tenant_admin", 0, 0, "")
	if err != nil {
		t.Fatalf("建成员行失败: %v", err)
	}
	// 人为制造"家行标记丢失"的半态（ORDER BY tenant_id 会让无口令的成员行排在前面）。
	if _, err := st.db.Exec("UPDATE users SET is_home=0 WHERE id=?", home.ID); err != nil {
		t.Fatalf("构造半态失败: %v", err)
	}
	rows, err := st.GetUserByUsernameGlobal("otto")
	if err != nil || len(rows) != 2 {
		t.Fatalf("夹具前提失效：应有 2 行同名，实得 %d 行（err=%v）", len(rows), err)
	}
	if rows[0].ID != member.ID {
		t.Fatalf("夹具前提失效：排序首行应是无口令的成员行（否则本用例没在测回落腿）")
	}
	got := collapseToAccounts(rows)
	if len(got) != 1 || got[0].ID != home.ID {
		t.Fatalf("无家行时必须回落到带口令那一行（uid=%d），实得 %v", home.ID, idList(got))
	}
}

// idList 把候选集摊成 uid 串，失败信息里一眼看出取到哪几行（而不是只报一个长度）。
func idList(us []*User) []int64 {
	out := make([]int64, 0, len(us))
	for _, u := range us {
		out = append(out, u.ID)
	}
	return out
}
