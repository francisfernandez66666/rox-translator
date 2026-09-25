// ============================================================================
// store/user_lang_test.go — F-17 批E（2026-09-25）邮件语种落库自动化断言
// 钉三层：①UserLangMigrate 幂等（store.New 已挂一次，重复执行不报错、列可用）；
// ②Set/GetPreferredLang 读写回路（含跨租户守卫：tid 不符不得改动）；
// ③清除语义（空串=未选语种，回中文链路）。
// 方言：newKBEnv 自带内存 SQLite + 自钉（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run TestUATBatchE
// ============================================================================
package store

import (
	"testing"
)

// TestUATBatchE_UserLangMigrateIdempotent 补列迁移幂等：store.New 挂载一次后再跑一遍不得报错，
// 且列保持可用（以 GetPreferredLang 成功执行为准——缺列会直接 SQL 错误）。
func TestUATBatchE_UserLangMigrateIdempotent(t *testing.T) {
	st, _ := newKBEnv(t)
	u, err := st.CreateUser(9, "f17_lang0", "hash", "语种探针", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	// store.New 已挂 UserLangMigrate；再执行两次模拟重启（幂等：不报错、不丢数据）
	if err := st.SetPreferredLang(u.ID, 9, "ja"); err != nil {
		t.Fatalf("SetPreferredLang 失败: %v", err)
	}
	st.UserLangMigrate()
	st.UserLangMigrate()
	got, err := st.GetPreferredLang(u.ID)
	if err != nil {
		t.Fatalf("重复迁移后 GetPreferredLang 失败（列被破坏？）: %v", err)
	}
	if got != "ja" {
		t.Fatalf("重复迁移不得清数据，期望 ja，实际 %q", got)
	}
}

// TestUATBatchE_PreferredLangRoundTrip 读写回路 + 跨租户守卫 + 清除语义。
func TestUATBatchE_PreferredLangRoundTrip(t *testing.T) {
	st, _ := newKBEnv(t)
	u, err := st.CreateUser(11, "f17_lang1", "hash", "语种回路", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	// 出厂空串（未选语种=中文链路）
	if got, err := st.GetPreferredLang(u.ID); err != nil || got != "" {
		t.Fatalf("新用户 preferred_lang 应为空，实际 %q err=%v", got, err)
	}
	// 正常写入回读
	if err := st.SetPreferredLang(u.ID, 11, "de"); err != nil {
		t.Fatalf("SetPreferredLang 失败: %v", err)
	}
	if got, err := st.GetPreferredLang(u.ID); err != nil || got != "de" {
		t.Fatalf("回读应为 de，实际 %q err=%v", got, err)
	}
	// 跨租户守卫：tid 不符不得改动（与 SetJobRole 同款 WHERE id AND tenant_id）
	if err := st.SetPreferredLang(u.ID, 12, "th"); err != nil {
		t.Fatalf("跨租户 SetPreferredLang 不应报错（0 行更新语义）: %v", err)
	}
	if got, _ := st.GetPreferredLang(u.ID); got != "de" {
		t.Fatalf("跨租户写入不得生效，期望仍为 de，实际 %q", got)
	}
	// 清除语义：空串=未选（回中文链路）
	if err := st.SetPreferredLang(u.ID, 11, ""); err != nil {
		t.Fatalf("清除 preferred_lang 失败: %v", err)
	}
	if got, _ := st.GetPreferredLang(u.ID); got != "" {
		t.Fatalf("清除后应为空串，实际 %q", got)
	}
	// 查无用户：GetPreferredLang 报错（调用方按空串降级），不得静默回假值
	if _, err := st.GetPreferredLang(999999); err == nil {
		t.Fatal("查无用户应返回错误，供调用方区分「未选」与「查无」")
	}
}
