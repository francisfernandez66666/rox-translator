// ============================================================================
// ★ H3 成员写入硬闸回归（UAT T30 捕获的越权漏洞）：read 授权成员不得写条目；
//
//	write 授权成员可写；无授权成员列表读取也被拒。
//
// ============================================================================
package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/store"
)

func h3gSetup(t *testing.T) (*Server, int64, int64) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, e := db.Exec(`INSERT INTO kb_packages (id, tenant_id, code, name, pack_type, role, enabled, org_id, share_cross_dept, created_at, updated_at) VALUES (77,1,'h3g','H3G包','tenant','source',1,0,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); e != nil {
		t.Fatal(e)
	}
	m, e := st.CreateUser(1, "h3g-member", auth.PasswordHash("pw123456"), "成员", "user", 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	return &Server{Store: st}, m.ID, 77
}

func h3gAdd(t *testing.T, s *Server, uid int64) *httptest.ResponseRecorder {
	t.Helper()
	u, err := s.Store.GetUser(uid, 1)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/kb-entries/add",
		strings.NewReader(`{"package_id":77,"layer":1,"source_lang":"zh","source_text":"术语甲","target_lang":"en","target_text":"term-a"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+tk)
	w := httptest.NewRecorder()
	s.handleKBEntryAdd(w, r)
	return w
}

func TestH3MemberWriteGate(t *testing.T) {
	s, uid, pack := h3gSetup(t)
	if w := h3gAdd(t, s, uid); !strings.Contains(w.Body.String(), "只读") && w.Code == 200 {
		t.Fatalf("无授权成员写入应 403: %d %s", w.Code, w.Body.String())
	}
	if err := s.Store.GrantKBPack(1, pack, uid, "read"); err != nil {
		t.Fatal(err)
	}
	w := h3gAdd(t, s, uid)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "编辑") {
		t.Fatalf("read 授权成员写入必须被拒（越权回归）: %d %s", w.Code, w.Body.String())
	}
	if err := s.Store.GrantKBPack(1, pack, uid, "write"); err != nil {
		t.Fatal(err)
	}
	w = h3gAdd(t, s, uid)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("write 授权成员应可写入: %d %s", w.Code, w.Body.String())
	}
	n := 0
	s.Store.DB().QueryRow("SELECT COUNT(*) FROM kb_entries WHERE package_id=77").Scan(&n)
	if n != 1 {
		t.Fatalf("条目数应为 1: %d", n)
	}
}
