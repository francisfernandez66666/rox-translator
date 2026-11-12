// ============ kb_lang_migrate_gate_test.go · 职责说明 ============
// A15：assist 库 kb_entries/scripts/flows 三表补 lang 幂等、存量回填 'zh' 不覆盖运营手工档、
// 幂等重跑零动作；与主库 product_facts 挂点分离（不在本测试里碰主库）。
// ================================================
package store

import (
	"path/filepath"
	"testing"

	"translator/internal/db"
)

// newKBLangDB 建临时 assist 库（Open 已建齐三张基表）。
func newKBLangDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("assist 库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// hasLangCol 查某表是否已有 lang 列（assist 固定 SQLite，直接用 DialectSQLite）。
func hasLangCol(t *testing.T, d *DB, table string) bool {
	t.Helper()
	ok, err := db.HasColumn(d.sql, db.DialectSQLite, table, "lang")
	if err != nil {
		t.Fatalf("HasColumn(%s) 失败: %v", table, err)
	}
	return ok
}

// TestKBLangMigrateIdempotent 三表补列，跑两次不报错且列都在。
func TestKBLangMigrateIdempotent(t *testing.T) {
	d := newKBLangDB(t)
	if err := d.KBLangMigrate(); err != nil {
		t.Fatalf("KBLangMigrate 失败: %v", err)
	}
	if err := d.KBLangMigrate(); err != nil {
		t.Fatalf("二次 KBLangMigrate 不幂等: %v", err)
	}
	for _, tbl := range []string{"kb_entries", "scripts", "flows"} {
		if !hasLangCol(t, d, tbl) {
			t.Fatalf("%s 缺 lang 列", tbl)
		}
	}
}

// TestKBLangBackfillZHAndNoOverwrite 存量行回填 'zh'，重跑不覆盖运营改过的 lang。
func TestKBLangBackfillZHAndNoOverwrite(t *testing.T) {
	d := newKBLangDB(t)
	id, err := d.Create("kb_entries", map[string]any{
		"key": "k1", "title": "T", "content": "C",
	})
	if err != nil || id <= 0 {
		t.Fatalf("补列前插 kb 行失败: id=%d err=%v", id, err)
	}
	if err := d.KBLangMigrate(); err != nil {
		t.Fatalf("KBLangMigrate 失败: %v", err)
	}
	var lang string
	if err := d.sql.QueryRow("SELECT lang FROM kb_entries WHERE id=?", id).Scan(&lang); err != nil {
		t.Fatalf("读回填 lang 失败: %v", err)
	}
	if lang != "zh" {
		t.Fatalf("存量行应回填 'zh'，实际 %q", lang)
	}
	// 运营手工改成 en。
	if _, err := d.sql.Exec("UPDATE kb_entries SET lang='en' WHERE id=?", id); err != nil {
		t.Fatalf("改手工档失败: %v", err)
	}
	if err := d.KBLangMigrate(); err != nil {
		t.Fatalf("二次 KBLangMigrate 失败: %v", err)
	}
	if err := d.sql.QueryRow("SELECT lang FROM kb_entries WHERE id=?", id).Scan(&lang); err != nil {
		t.Fatalf("复读 lang 失败: %v", err)
	}
	if lang != "en" {
		t.Fatalf("幂等重跑不得覆盖运营手工档，实际 %q", lang)
	}
}
