package knowledge

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
	knowledgeStore "translator/internal/knowledge/store"

	_ "modernc.org/sqlite"
)

// knSanitize 把测试名压成 DSN 安全字符（避免内存库名带 / 等特殊字符）。
func knSanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			out = append(out, r)
		}
	}
	return string(out)
}

// openKnSQLite 造一个带四表的独立内存知识库（同包各 gate test 共用；tag + t.Name() 唯一化避免共享串库）。
func openKnSQLite(t *testing.T, tag string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:kn_%s_%s?mode=memory&cache=shared&_txlock=immediate", tag, knSanitize(t.Name()))
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

// seedKnFourTables 四表各插一行。
// ★ product_facts.priority=7 是 KN10「反向脚本故意漏一列」的非默认值哨兵：
//   漏列 ⇒ 目标库落 DEFAULT 0 ⇒ 指纹按全量列算不等 ⇒ 反向搬迁返回 error ⇒ 判红。
func seedKnFourTables(t *testing.T, conn *sql.DB, tid int64) {
	t.Helper()
	must := func(q string, args ...interface{}) {
		if _, err := db.Exec(conn, db.DialectSQLite, q, args...); err != nil {
			t.Fatalf("夹具插行失败: %v\nSQL: %s", err, q)
		}
	}
	must(`INSERT INTO products(id,tenant_id,name,slug,tenant_code,page_code,created_at,updated_at)
	      VALUES(?,?,?,?,?,?,?,?)`, "p1", tid, "n", "s", "tc", "pc", "t", "t")
	must(`INSERT INTO product_facts(id,tenant_id,product_id,kind,key,value,priority,created_at,updated_at)
	      VALUES(?,?,?,?,?,?,?,?,?)`, 1, tid, "p1", "spec", "k", "v", 7, "t", "t")
	must(`INSERT INTO product_assets(id,tenant_id,product_id,asset_type,lang,body,created_at,updated_at)
	      VALUES(?,?,?,?,?,?,?,?)`, 1, tid, "p1", "tagline", "zh", "b", "t", "t")
	must(`INSERT INTO product_fact_i18n(id,tenant_id,fact_id,lang,value)
	      VALUES(?,?,?,?,?)`, 1, tid, 1, "zh", "译文")
}

// KN8 四表搬迁：真 SQL、显式 id、跑两遍幂等、双向指纹等值。
func TestKN8MigrateIdempotentAndFingerprint(t *testing.T) {
	main := openKnSQLite(t, "mig_src")
	know := openKnSQLite(t, "mig_dst")
	seedKnFourTables(t, main, 1)

	rep, err := Migrate(context.Background(), main, db.DialectSQLite, know, db.DialectSQLite)
	if err != nil {
		t.Fatalf("KN8 搬迁失败（只建表不搬行 ⇒ M0290 会让指纹不等在此红）: %v", err)
	}
	if !rep.Matched {
		t.Fatalf("KN8 双向指纹不等")
	}
	for _, table := range contract.KnowledgeTableNames {
		if rep.Counts[table] == 0 {
			t.Fatalf("KN8 表 %s 目标库零行（搬迁没真插行）", table)
		}
	}
	var id string
	if err := db.QueryRow(know, db.DialectSQLite,
		"SELECT id FROM products WHERE tenant_id=?", 1).Scan(&id); err != nil {
		t.Fatalf("KN8 显式 id 查询失败: %v", err)
	}
	if id != "p1" {
		t.Fatalf("KN8 显式 id 丢失: got %q want %q", id, "p1")
	}

	rep2, err := Migrate(context.Background(), main, db.DialectSQLite, know, db.DialectSQLite)
	if err != nil {
		t.Fatalf("KN8 第二遍搬迁失败: %v", err)
	}
	for _, table := range contract.KnowledgeTableNames {
		if rep2.Counts[table] != rep.Counts[table] {
			t.Fatalf("KN8 跑两遍行数变了（幂等失效）: %s %d→%d", table, rep.Counts[table], rep2.Counts[table])
		}
	}
}

// 反证判别力：目标库零行时指纹必须判不等——证明「只建表不搬行」会被抓到（不是空转锁）。
func TestKN8FingerprintDiscriminatesMissingRows(t *testing.T) {
	src := openKnSQLite(t, "fp_src")
	dst := openKnSQLite(t, "fp_dst")
	seedKnFourTables(t, src, 1)
	rep, err := fingerprintBoth(src, db.DialectSQLite, dst, db.DialectSQLite)
	if err != nil {
		t.Fatalf("指纹计算失败: %v", err)
	}
	if rep.Matched {
		t.Fatalf("KN8 判据失明：目标库零行却判双向等值（空转锁）")
	}
}
