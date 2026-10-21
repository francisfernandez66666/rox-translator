// ============ account_ambiguity_test.go · 用户名重名不得静默选中一个 ============
// 新方案允许同租户重名，用户名不再是登录标识。存量兼容的「无 email 字段登录」若只取
// 一行就会登进任意账号（审计/会话都记到错的人头上），因此必须把全部同名候选交回调用方。
// =================================================================================

package iam

import "testing"

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
