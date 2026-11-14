// ============ assist_session_migrate_gate_test.go · 职责说明 ============
// 四期 §4.5.3 **A2**：会话恢复——sid+tok 迁移后可读历史；
// 兼做步骤 2「造旧库跑两遍」的幂等自证（行数 + 内容哈希等值）。
//
// 反证（M0299）：只迁 sessions_base 不迁 messages_base ⇒ 主库消息表 0 行、
// history 读不到旧消息 ⇒ 红。变异体把 chatMigrateSpecs 截成 [:1] 即触发。
//
// ★ 与 A5（internal/assist/store/migrate_gate_test.go）各判一处搬家，
//
//	不许合并：那边是四张 KB 表 → 知识库，这边是会话/消息 → 主库。
//
// ================================================
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	assistapi "translator/internal/assist/api"
	assistengine "translator/internal/assist/engine"
	assistllm "translator/internal/assist/llm"
	assiststore "translator/internal/assist/store"
	"translator/internal/db"
)

// a2OpenSQLite 造一个独立 SQLite 文件库（单写者，与 assist/store 同口径）。
func a2OpenSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("A2 打开 %s 失败: %v", path, err)
	}
	conn.SetMaxOpenConns(1)
	return conn
}

// a2MustExec 执行建表/插行，失败即判红（造旧库不许有半截形态）。
func a2MustExec(t *testing.T, conn *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(q, args...); err != nil {
		t.Fatalf("A2 造旧库 SQL 失败: %v\nSQL=%s", err, q)
	}
}

// a2BuildLegacyAssistDB 造一个「存量 assist 库」：两张现名基表 + 显式数据。
func a2BuildLegacyAssistDB(t *testing.T, conn *sql.DB) {
	t.Helper()
	a2MustExec(t, conn, `CREATE TABLE sessions_base(
		id TEXT PRIMARY KEY, page_url TEXT DEFAULT '', in_flow TEXT DEFAULT '',
		flow_step INTEGER DEFAULT 0, msg_count INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP, last_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		tenant_id INTEGER, user_id INTEGER, anonym_hash TEXT DEFAULT '', expires_at DATETIME)`)
	a2MustExec(t, conn, `CREATE TABLE messages_base(
		id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
		role TEXT NOT NULL, content TEXT NOT NULL, actions TEXT DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP, tenant_id INTEGER,
		user_id INTEGER, expires_at DATETIME, anonym_hash TEXT DEFAULT '')`)
	const sid = "w5-legacy-session-1"
	a2MustExec(t, conn, `INSERT INTO sessions_base
		(id,page_url,in_flow,flow_step,msg_count,created_at,last_at,tenant_id,user_id,anonym_hash,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		sid, "/p/tc/pc", "", 0, 2, "2026-11-01 10:00:00", "2026-11-01 10:05:00", 7, 9, "", nil)
	a2MustExec(t, conn, `INSERT INTO sessions_base
		(id,page_url,in_flow,flow_step,msg_count,created_at,last_at,tenant_id,user_id,anonym_hash,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"w5-legacy-session-2", "/", "", 0, 0, "2026-11-02 09:00:00", "2026-11-02 09:00:00", 0, 0, "anonhash", nil)
	for _, m := range []struct {
		id      int
		role    string
		content string
	}{
		{11, "user", "旧库用户消息-问价格"},
		{12, "assistant", "旧库助手消息-答价格"},
		{13, "user", "旧库用户消息-问格式"},
		{14, "assistant", "旧库助手消息-答格式"},
	} {
		a2MustExec(t, conn, `INSERT INTO messages_base
			(id,session_id,role,content,actions,created_at,tenant_id,user_id,expires_at,anonym_hash)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			m.id, sid, m.role, m.content, "[]", "2026-11-01 10:0"+string(rune('0'+m.id%10))+":00", 7, 9, nil, "")
	}
}

// a2Fingerprint 行数 + 逐行逐列内容哈希（同一把尺子量源库与主库）。
func a2Fingerprint(t *testing.T, conn *sql.DB, table string) (int, string) {
	t.Helper()
	rows, err := conn.Query("SELECT * FROM " + table + " ORDER BY id")
	if err != nil {
		t.Fatalf("A2 读 %s 失败: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("A2 取 %s 列失败: %v", table, err)
	}
	h := sha256.New()
	n := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("A2 扫描 %s 行失败: %v", table, err)
		}
		b, _ := json.Marshal(vals)
		h.Write(b)
		h.Write([]byte("\n"))
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("A2 遍历 %s 失败: %v", table, err)
	}
	return n, hex.EncodeToString(h.Sum(nil))
}

// TestA2AssistSessionMigrateAndHistory 造旧库跑两遍 + sid+tok 读历史。
func TestA2AssistSessionMigrateAndHistory(t *testing.T) {
	dir := t.TempDir()
	legacy := a2OpenSQLite(t, filepath.Join(dir, "legacy-assist.db"))
	defer legacy.Close()
	a2BuildLegacyAssistDB(t, legacy)
	mainDB := a2OpenSQLite(t, filepath.Join(dir, "main.db"))
	defer mainDB.Close()

	ctx := context.Background()
	// 第一遍：建表 + 显式 id 幂等 upsert。
	if err := MigrateAssistSessionsToMain(ctx, mainDB, legacy, db.DialectSQLite); err != nil {
		t.Fatalf("A2 第一遍迁移失败: %v", err)
	}
	n1s, h1s := a2Fingerprint(t, legacy, "sessions_base")
	n1m, h1m := a2Fingerprint(t, legacy, "messages_base")
	g1s, g1sH := a2Fingerprint(t, mainDB, "sessions_base")
	g1m, g1mH := a2Fingerprint(t, mainDB, "messages_base")
	if n1s != g1s || h1s != g1sH {
		t.Fatalf("A2 sessions_base 第一遍不等值：源(%d,%s) 主库(%d,%s)", n1s, h1s, g1s, g1sH)
	}
	if n1m != g1m || h1m != g1mH {
		t.Fatalf("A2 messages_base 第一遍不等值：源(%d,%s) 主库(%d,%s)", n1m, h1m, g1m, g1mH)
	}
	if n1m == 0 {
		t.Fatal("A2 反证：源库消息表本来就是空的，这条锁没有判别力")
	}

	// 第二遍：行数 + 内容哈希必须一字不变（幂等自证）。
	if err := MigrateAssistSessionsToMain(ctx, mainDB, legacy, db.DialectSQLite); err != nil {
		t.Fatalf("A2 第二遍迁移失败: %v", err)
	}
	g2s, g2sH := a2Fingerprint(t, mainDB, "sessions_base")
	g2m, g2mH := a2Fingerprint(t, mainDB, "messages_base")
	if g2s != g1s || g2sH != g1sH || g2m != g1m || g2mH != g1mH {
		t.Fatalf("A2 跑两遍不幂等：一遍(%d/%d,%s/%s) 二遍(%d/%d,%s/%s)",
			g1s, g1m, g1sH, g1mH, g2s, g2m, g2sH, g2mH)
	}
	// 显式 id 落位正确（不是重新分配自增 id）。
	var content string
	if err := mainDB.QueryRow("SELECT content FROM messages_base WHERE id=11").Scan(&content); err != nil {
		t.Fatalf("A2 显式 id=11 没落位: %v", err)
	}
	if content != "旧库用户消息-问价格" {
		t.Fatalf("A2 显式 id=11 内容错: %q", content)
	}

	// sid+tok 读历史：用本地装配的 assist handler 打 /api/assist/history。
	mainPath := filepath.Join(dir, "main.db")
	adb, err := assiststore.Open(mainPath)
	if err != nil {
		t.Fatalf("A2 以主库打开 assist store 失败: %v", err)
	}
	defer adb.Close()
	eng := assistengine.New(adb, assistllm.New(nil, 45))
	srv := assistapi.NewServer(adb, eng, "w5-a2-admin", "")
	keyHex := adb.GetConfig("sess_key", "")
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil || len(keyBytes) != 32 {
		t.Fatalf("A2 sess_key 形态不对: %q err=%v", keyHex, err)
	}
	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte("w5-legacy-session-1"))
	tok := hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodGet,
		"/api/assist/history?session="+url.QueryEscape("w5-legacy-session-1")+"&tok="+tok, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("A2 sid+tok 读历史失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "旧库用户消息-问价格") ||
		!strings.Contains(rec.Body.String(), "旧库助手消息-答格式") {
		t.Fatalf("A2 迁移后读不到旧消息（M0299 只迁会话不迁消息必红）: %s", rec.Body.String())
	}
	// 反证：错 tok 必须 401（证明上面读到的是「校验通过的历史」而不是任意兜底）。
	badReq := httptest.NewRequest(http.MethodGet,
		"/api/assist/history?session="+url.QueryEscape("w5-legacy-session-1")+"&tok=wrong", nil)
	badRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("A2 反证失明：错 tok 竟返回 %d，history 的归属校验没生效", badRec.Code)
	}
}
