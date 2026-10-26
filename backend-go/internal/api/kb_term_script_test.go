// ============ kb_term_script_test.go · 职责说明 ============
// P2-03（2026-10-26）入库口锁：目标语与目标文本书写系统明显不符的术语不得入库。
// 现网形态＝target_lang=en、target_text=「英语」（中文），它会命中任何中文正文，
// 让正常译文被判「术语未体现」并触发无意义重译。
// 品牌术语（module=brand）豁免：品牌名可能刻意保留汉字。
// ============================================
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
)

// kbTermAdd 以有 write 授权的成员身份提交一条术语，返回响应与库内条目数。
func kbTermAdd(t *testing.T, s *Server, uid, pack int64, body string) (*httptest.ResponseRecorder, int) {
	t.Helper()
	u, err := s.Store.GetUser(uid, 1)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/kb-entries/add", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+tk)
	w := httptest.NewRecorder()
	s.handleKBEntryAdd(w, r)
	n := 0
	_ = s.Store.DB().QueryRow("SELECT COUNT(*) FROM kb_entries WHERE package_id=?", pack).Scan(&n)
	return w, n
}

// TestKBAddRejectsTargetScriptMismatch 脏术语被拒且不落库；品牌术语豁免。
func TestKBAddRejectsTargetScriptMismatch(t *testing.T) {
	s, uid, pack := h3gSetup(t)
	if err := s.Store.GrantKBPack(1, pack, uid, "write"); err != nil {
		t.Fatal(err)
	}
	w, n := kbTermAdd(t, s, uid, pack,
		`{"package_id":77,"layer":1,"source_lang":"zh","source_text":"中文","target_lang":"en","target_text":"英语"}`)
	if n != 0 {
		t.Fatalf("脏术语不得落库，实际条目数 %d（响应 %d %s）", n, w.Code, w.Body.String())
	}
	if w.Code == 200 && strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("脏术语应被拒绝，实际 200 成功：%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "书写系统") {
		t.Fatalf("拒绝原因应点明书写系统不符：%d %s", w.Code, w.Body.String())
	}

	// 品牌术语豁免：品牌名可以保留汉字（中文品牌在英文文案里仍写中文）。
	w2, n2 := kbTermAdd(t, s, uid, pack,
		`{"package_id":77,"layer":1,"source_lang":"zh","source_text":"极石","target_lang":"en","target_text":"极石","module":"brand"}`)
	if n2 != 1 || w2.Code != 200 || !strings.Contains(w2.Body.String(), `"success":true`) {
		t.Fatalf("品牌术语应被放行：%d %s（条目数 %d）", w2.Code, w2.Body.String(), n2)
	}
}
