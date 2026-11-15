// ============ schema_test.go · 职责说明 ============
// internal/knowledge/schema 的行为锁（四期收口 §1.4）。
//
// 三条腿：
//
//	① DropOrder 必须子表在前、父表在后（PG 有外键时顺序错了会炸）；
//	② 反证：把顺序颠倒喂**同一个校验函数**必须判红（否则 ① 是空转锁）；
//	③ 真 SQL：建四表 → DropProductTablesForRollback → 四表全不存在（SQLite 实测）。
//
// ★ 方言自钉：本文件全程显式传 db.DialectSQLite，不读 config.Default()（避免
//
//	AGENTS §一·4 那条「config.Default() 写全局 config.C 泄漏方言」的坑）。
//
// ================================================
package schema

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"translator/internal/db"
	"translator/internal/knowledge/contract"

	_ "modernc.org/sqlite" // db.Open 走 sql.Open("sqlite")，测试进程需自行注册驱动
)

// newSchemaDB 开一个文件型 SQLite 库（t.TempDir 自动回收）。
func newSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "schema.db")})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// tablePresent 真查 sqlite_master（不用 db.CurrentDialect，测试自钉方言）。
func tablePresent(t *testing.T, conn *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := conn.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n); err != nil {
		t.Fatalf("查表 %s 失败: %v", name, err)
	}
	return n > 0
}

// orderedChildBeforeParent 是**产品侧**判据（被 DropProductTablesForRollback 复用），
// 正反两条用例喂同一个函数，避免「测试里自己写一遍顺序」那种空转。
func orderedChildBeforeParent(order []string) error {
	return validateDropOrder(order)
}

func TestDropOrderChildBeforeParent(t *testing.T) {
	want := []string{
		contract.TableProductFactI18n,
		contract.TableProductAssets,
		contract.TableProductFacts,
		contract.TableProducts,
	}
	if !reflect.DeepEqual(DropOrder, want) {
		t.Fatalf("drop 顺序不对：got=%v want=%v", DropOrder, want)
	}
	// 本包 TableNames 是「DDL 归属」口径、contract.KnowledgeTableNames 是契约口径，
	// 两者必须逐项等值（否则健康面/闸门与真实 DDL 会读到两张不同的表清单）。
	if !reflect.DeepEqual(TableNames, contract.KnowledgeTableNames) {
		t.Fatalf("表清单漂移：schema.TableNames=%v contract.KnowledgeTableNames=%v",
			TableNames, contract.KnowledgeTableNames)
	}
	if err := orderedChildBeforeParent(DropOrder); err != nil {
		t.Fatalf("正常顺序被判错: %v", err)
	}
	// 反证①：整个颠倒 ⇒ 父表 products 排在最前 ⇒ 必红。
	reversed := append([]string(nil), DropOrder...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	if err := orderedChildBeforeParent(reversed); err == nil {
		t.Fatal("反证失明：颠倒 drop 顺序仍判通过 ⇒ 这条锁测不到 PG 外键炸表那一族坏法")
	}
	// 反证②：少一张表也必须红（防「只 drop 三张也放行」）。
	if err := orderedChildBeforeParent(DropOrder[:3]); err == nil {
		t.Fatal("反证失明：少一张表仍判通过 ⇒ 主库会留下一张孤儿产品表")
	}
}

func TestEnsureThenDropRemovesAllFourTables(t *testing.T) {
	conn := newSchemaDB(t)
	if err := EnsureProductTables(conn, db.DialectSQLite); err != nil {
		t.Fatalf("建四表失败: %v", err)
	}
	for _, name := range DropOrder {
		if !tablePresent(t, conn, name) {
			t.Fatalf("建表后 %s 不存在（DDL 被架空）", name)
		}
	}
	if err := DropProductTablesForRollback(conn, db.DialectSQLite); err != nil {
		t.Fatalf("drop 四表失败: %v", err)
	}
	for _, name := range DropOrder {
		if tablePresent(t, conn, name) {
			t.Fatalf("drop 后 %s 仍在（回滚/收尾没删干净）", name)
		}
	}
	// 幂等：再 drop 一次不得报错（运维重跑是常态）。
	if err := DropProductTablesForRollback(conn, db.DialectSQLite); err != nil {
		t.Fatalf("重复 drop 失败（IF EXISTS 被摘）: %v", err)
	}
}
