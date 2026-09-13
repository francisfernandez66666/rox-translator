// ============================================================================
// H3 包级授权存储层测试：upsert 幂等、三级角色序、撤销、按用户汇总。
// ============================================================================
package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func h3Store(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return s
}

func TestH3KBPackGrants(t *testing.T) {
	s := h3Store(t)
	if got := s.KBPackRoleOf(1, 10, 5); got != "" {
		t.Fatalf("初始应无授权: %q", got)
	}
	if err := s.GrantKBPack(1, 10, 5, "read"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if got := s.KBPackRoleOf(1, 10, 5); got != "read" {
		t.Fatalf("角色应为 read: %q", got)
	}
	// upsert：同包同人重复授权覆盖为更高级别
	if err := s.GrantKBPack(1, 10, 5, "manage"); err != nil {
		t.Fatalf("Grant#2: %v", err)
	}
	if got := s.KBPackRoleOf(1, 10, 5); got != "manage" {
		t.Fatalf("应升级为 manage: %q", got)
	}
	list, err := s.ListKBPackGrants(1, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("授权列表应 1 条: %d %v", len(list), err)
	}
	// 角色序：read < write < manage
	if !(KBRoleRank("read") < KBRoleRank("write") && KBRoleRank("write") < KBRoleRank("manage")) {
		t.Fatal("角色序错误")
	}
	// role='' 等价撤销
	if err := s.GrantKBPack(1, 10, 5, ""); err != nil {
		t.Fatalf("Grant(''): %v", err)
	}
	if got := s.KBPackRoleOf(1, 10, 5); got != "" {
		t.Fatalf("'' 应撤销: %q", got)
	}
	// 按用户汇总多包
	_ = s.GrantKBPack(1, 11, 6, "write")
	_ = s.GrantKBPack(1, 12, 6, "read")
	mine, err := s.ListKBPackGrantsByUser(1, 6)
	if err != nil || len(mine) != 2 {
		t.Fatalf("用户应持 2 包授权: %d %v", len(mine), err)
	}
	// 他租户不可见
	if got := s.KBPackRoleOf(2, 11, 6); got != "" {
		t.Fatalf("跨租户不应可见: %q", got)
	}
}
