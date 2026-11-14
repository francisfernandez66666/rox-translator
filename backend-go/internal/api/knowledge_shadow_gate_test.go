package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/db"
	knowledgeAPI "translator/internal/knowledge/api"
	"translator/internal/knowledge/contract"
	knowledgeStore "translator/internal/knowledge/store"

	_ "modernc.org/sqlite"
)

func w3San(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			out = append(out, r)
		}
	}
	return string(out)
}

// w3KnOpenDB 造一个带四表的独立内存知识库（api 包各 gate test 共用）。
func w3KnOpenDB(t *testing.T, tag string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:w3api_%s_%s?mode=memory&cache=shared&_txlock=immediate", tag, w3San(t.Name()))
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	if err := knowledgeStore.KnowledgeMigrate(conn, db.DialectSQLite); err != nil {
		conn.Close()
		t.Fatalf("建知识四表失败: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// w3SeedFacts 插 ≥20 组事实夹具（多语 / 受控 / 公开 / 跨租户），逐字段比对用。
func w3SeedFacts(t *testing.T, conn *sql.DB) {
	t.Helper()
	langs := []string{"zh", "en", "ru"}
	vis := []string{contract.VisibilityPublic, contract.VisibilityGated}
	for i := 0; i < 24; i++ {
		q := `INSERT INTO product_facts(id,tenant_id,product_id,kind,key,value,status,visibility,lang,priority,created_at,updated_at)
		      VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`
		args := []interface{}{
			i + 1, int64(1), "p1", "spec", fmt.Sprintf("k%02d", i), fmt.Sprintf("v%d", i),
			contract.FactStatusConfirmed, vis[i%2], langs[i%3], i, "t", "t",
		}
		if _, err := db.Exec(conn, db.DialectSQLite, q, args...); err != nil {
			t.Fatalf("夹具插行失败: %v", err)
		}
	}
	// 跨租户 3 行（tid=2）：验证影子读不会把别租户的行混进来。
	for i := 0; i < 3; i++ {
		q := `INSERT INTO product_facts(id,tenant_id,product_id,kind,key,value,created_at,updated_at)
		      VALUES(?,?,?,?,?,?,?,?)`
		if _, err := db.Exec(conn, db.DialectSQLite, q,
			100+i, int64(2), "p2", "spec", fmt.Sprintf("x%d", i), "v", "t", "t"); err != nil {
			t.Fatalf("跨租户夹具插行失败: %v", err)
		}
	}
}

// KN2 影子读逐字段等值（≥20 组夹具，含空集 / 多语 / 受控 / 跨租户）。
// ★ 服务少返回受控键（M0284）⇒ 逐字段比对在此红；两侧都来自同一份真 SQL，差异必须 0。
func TestKN2ShadowReadFieldParity(t *testing.T) {
	conn := w3KnOpenDB(t, "shadow")
	w3SeedFacts(t, conn)
	ks := knowledgeStore.OpenDB(conn, db.DialectSQLite)
	ts := httptest.NewServer(knowledgeAPI.NewServer(ks, "w3-token").Handler())
	defer ts.Close()

	check := func(tid int64, wantMin int) {
		// 直读（服务端真 SQL 同一出口）
		direct, err := knowledgeStore.QueryScoped(conn, db.DialectSQLite, contract.TableProductFacts, tid, "")
		if err != nil {
			t.Fatalf("直读失败: %v", err)
		}
		// 服务读（HTTP 契约端点）
		req, _ := http.NewRequest(http.MethodPost, ts.URL+contract.PathFactsList,
			strings.NewReader(fmt.Sprintf(`{"tenant_id":%d}`, tid)))
		req.Header.Set("X-Knowledge-Token", "w3-token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("服务读失败: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("服务读非 200: %d", resp.StatusCode)
		}
		var env struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&env)
		resp.Body.Close()
		if decErr != nil {
			t.Fatalf("服务读解码失败: %v", decErr)
		}
		if len(env.Items) != len(direct) {
			t.Fatalf("KN2 行数不等: direct=%d service=%d", len(direct), len(env.Items))
		}
		if len(direct) < wantMin {
			t.Fatalf("KN2 夹具不足: %d < %d", len(direct), wantMin)
		}
		for i := range direct {
			for k, v := range direct[i] {
				got, ok := env.Items[i][k]
				if !ok {
					t.Fatalf("KN2 服务少返回受控键 %s（M0284 在此红）", k)
				}
				if fmt.Sprint(got) != fmt.Sprint(v) {
					t.Fatalf("KN2 字段 %s 不等: 服务=%v 直读=%v", k, got, v)
				}
			}
		}
	}

	check(1, 24)  // 24 组夹具（多语 / 受控 / 公开）
	check(999, 0) // 空集
	check(2, 3)   // 跨租户：只回 tid=2 的 3 行，不含 tid=1
}
