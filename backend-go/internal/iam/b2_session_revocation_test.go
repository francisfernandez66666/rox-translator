// ============ 本文件职责中文说明 ============
// b2_session_revocation_test.go · B2 会话撤销回归（2026-09-12）：
//
//	① Sign 把用户 TokenVersion 写入 JWT claims（Verify 原样还原）；
//	② ResetPassword 递增 token_version（改密/重置后旧 token 与库内版本不一致 → authUser 拒绝）；
//	③ 旧版无 tv 字段的 token 解码为 0，兼容升级前签发的存量会话。
//
// ========================================
package iam

import (
	"path/filepath"
	"testing"

	"database/sql"
	_ "modernc.org/sqlite"
)

// newIAMTestStore 临时文件库 iam.Store。
func newIAMTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "b2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER, username TEXT, password_hash TEXT,
		display_name TEXT, role TEXT, status TEXT, created_by INTEGER, last_login_at TEXT NOT NULL DEFAULT '', org_id INTEGER,
		email TEXT DEFAULT '', created_at TEXT, updated_at TEXT, deactivate_at TEXT DEFAULT '',
		agreed_at TEXT DEFAULT '', must_change_pwd INTEGER DEFAULT 0, token_version INTEGER DEFAULT 0,
		job_role TEXT DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	return NewStore(conn)
}

func TestSignCarriesTokenVersion(t *testing.T) {
	u := &User{ID: 9, TenantID: 3, Username: "b2", Role: "user", TokenVersion: 7}
	tok, err := Sign(u, 3600000000000) // 1h in ns
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.TokenVersion != 7 {
		t.Fatalf("claims 应携带 tv=7，实得 %d", c.TokenVersion)
	}
}

func TestResetPasswordBumpsTokenVersion(t *testing.T) {
	st := newIAMTestStore(t)
	u, err := st.CreateUser(5, "b2user", "hash", "B2", "user", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if u.TokenVersion != 0 {
		t.Fatalf("新用户 tv 应为 0，实得 %d", u.TokenVersion)
	}
	tok, _ := Sign(u, 3600000000000)
	if c, err := Verify(tok); err != nil || c.TokenVersion != 0 {
		t.Fatalf("旧 token tv 应为 0: %+v %v", c, err)
	}
	// 改密：库内版本递增；重签 token 携带新版本，旧 token 与库比对必然失配
	if err := st.ResetPassword(u.ID, 5, "newhash"); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetUser(u.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenVersion != 1 {
		t.Fatalf("ResetPassword 后 tv 应为 1，实得 %d", got.TokenVersion)
	}
	oldClaims, _ := Verify(tok)
	if oldClaims.TokenVersion == got.TokenVersion {
		t.Fatal("旧 token 版本不应与新库值一致（会话应已撤销）")
	}
}
