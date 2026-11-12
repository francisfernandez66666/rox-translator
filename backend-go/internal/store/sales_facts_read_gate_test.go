// ============ sales_facts_read_gate_test.go · 职责说明 ============
// A13b：ListSalesFactsForInjection 准入四条件（confirmed/public/locked=0/tenant_id）过滤、
// 受控键清单绝不含 value、缺译文/hash 失配 fail-closed、跨租户不可串、按 priority 排序、
// SQLite/PG 双真驱动各跑一次。
// ================================================
package store

import (
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// insertSalesFact 直插一条 product_facts（绕过 UpsertFact 的确认流程，精确控制三条件）。
func insertSalesFact(t *testing.T, conn *sql.DB, tid int64, productID, kind, key, value, status, visibility string, locked, priority int) int64 {
	t.Helper()
	id, err := db.InsertID(conn, db.CurrentDialect(), "id",
		`INSERT INTO product_facts
		 (tenant_id, product_id, kind, key, value, status, visibility, source, locked, created_at, updated_at, priority)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		tid, productID, kind, key, value, status, visibility, "manual", locked,
		"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", priority)
	if err != nil {
		t.Fatalf("插事实失败: %v", err)
	}
	return id
}

// insertFactI18n 直插一条已落库译文（hash 用写侧同一把 factSourceHash）。
func insertFactI18n(t *testing.T, conn *sql.DB, tid, factID int64, lang, value, hash string) {
	t.Helper()
	if _, err := db.Exec(conn, db.CurrentDialect(),
		`INSERT INTO product_fact_i18n(tenant_id, fact_id, lang, value, source_hash, translated_at, engine)
		 VALUES (?,?,?,?,?,?,?)`,
		tid, factID, lang, value, hash, "2026-01-01T00:00:00Z", "single_lang"); err != nil {
		t.Fatalf("插译文失败: %v", err)
	}
}

// TestSalesFactsAdmissionAndControlled 准入一条带值、四类受控只出 kind+key 且绝无 value。
func TestSalesFactsAdmissionAndControlled(t *testing.T) {
	st, conn := newFactI18nStore(t)
	id := insertSalesFact(t, conn, 7, "P1", "price", "retail_price", "12 元", "confirmed", "public", 0, 5)
	insertFactI18n(t, conn, 7, id, "en", "12 CNY", factSourceHash("12 元"))
	insertSalesFact(t, conn, 7, "P1", "spec", "unverified_key", "SECRET-UNVERIFIED", "unverified", "public", 0, 0)
	insertSalesFact(t, conn, 7, "P1", "cert", "gated_key", "SECRET-GATED", "confirmed", "gated", 0, 0)
	// ★ locked 行也补上译文（M0236 行为腿补强）：否则摘掉 AND locked=0 后该行会因「缺译文」
	//   在 JOIN 层被二次过滤，变异永远抓不到（首跑 lock-stayed-green 实证）。补译文后
	//   locked=0 一旦被摘，locked_key 就会带值进 Facts ⇒ len(facts)!=1 必红。
	lockedID := insertSalesFact(t, conn, 7, "P1", "capacity", "locked_key", "SECRET-LOCKED", "confirmed", "public", 1, 0)
	insertFactI18n(t, conn, 7, lockedID, "en", "SECRET-LOCKED-EN", factSourceHash("SECRET-LOCKED"))
	insertSalesFact(t, conn, 7, "P1", "moq", "withdrawn_key", "SECRET-WITHDRAWN", "withdrawn", "public", 0, 0)

	facts, controlled, err := st.ListSalesFactsForInjection(7, "", "en")
	if err != nil {
		t.Fatalf("准入读口失败: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("只有一条准入事实应带值进集，实际 %d 条", len(facts))
	}
	if facts[0].Value != "12 CNY" || facts[0].Lang != "en" {
		t.Fatalf("应下发 answerLang 译文而非中文原文: %+v", facts[0])
	}
	if len(controlled) != 4 {
		t.Fatalf("四类非准入事实应各出一条受控键，实际 %d 条", len(controlled))
	}
	// ★ 类型层保证：ControlledFactKey 只有 Kind/Key 两个字段，Value 物理上进不来（M0237）。
	typ := reflect.TypeOf(ControlledFactKey{})
	if typ.NumField() != 2 || typ.Field(0).Name != "Kind" || typ.Field(1).Name != "Key" {
		t.Fatalf("ControlledFactKey 必须只有 Kind/Key、无 Value，实际字段数 %d", typ.NumField())
	}
	// ★ SQL 层保证：受控键查询只 SELECT kind,key，一个 value 都不查出来。
	src, err := os.ReadFile("sales_facts_read.go")
	if err != nil {
		t.Fatalf("读不到 sales_facts_read.go: %v", err)
	}
	if !strings.Contains(string(src), "SELECT kind, key FROM product_facts") {
		t.Fatal("受控键查询必须只 SELECT kind, key")
	}
}

// TestSalesFactsMissingTranslationFailClosed 缺译文 / source_hash 失配 ⇒ 该事实不进集。
func TestSalesFactsMissingTranslationFailClosed(t *testing.T) {
	st, conn := newFactI18nStore(t)
	noTrans := insertSalesFact(t, conn, 1, "P", "spec", "no_trans", "甲", "confirmed", "public", 0, 0)
	badHash := insertSalesFact(t, conn, 1, "P", "spec", "bad_hash", "乙", "confirmed", "public", 0, 0)
	insertFactI18n(t, conn, 1, badHash, "en", "B", "deadbeef") // 与当前原文 hash 不符
	good := insertSalesFact(t, conn, 1, "P", "spec", "good_key", "丙", "confirmed", "public", 0, 0)
	insertFactI18n(t, conn, 1, good, "en", "C", factSourceHash("丙"))

	facts, _, err := st.ListSalesFactsForInjection(1, "", "en")
	if err != nil {
		t.Fatalf("准入读口失败: %v", err)
	}
	if len(facts) != 1 || facts[0].Key != "good_key" {
		t.Fatalf("缺译文/hash 失配必须不进集，实际进集: %+v", facts)
	}
	_ = noTrans
}

// TestSalesFactsCrossTenant 跨租户不可串（事实与受控键都按 tid 隔离）。
func TestSalesFactsCrossTenant(t *testing.T) {
	st, conn := newFactI18nStore(t)
	a := insertSalesFact(t, conn, 1, "P", "price", "k1", "甲", "confirmed", "public", 0, 0)
	insertFactI18n(t, conn, 1, a, "en", "A", factSourceHash("甲"))
	insertSalesFact(t, conn, 2, "P", "price", "k2", "乙", "confirmed", "public", 0, 0)
	insertSalesFact(t, conn, 2, "P", "cert", "gated2", "SECRET2", "confirmed", "gated", 0, 0)

	facts, controlled, err := st.ListSalesFactsForInjection(1, "", "en")
	if err != nil {
		t.Fatalf("准入读口失败: %v", err)
	}
	if len(facts) != 1 || facts[0].Key != "k1" {
		t.Fatalf("租户 1 事实被串: %+v", facts)
	}
	if len(controlled) != 0 {
		t.Fatalf("租户 1 不应看到租户 2 的受控键: %+v", controlled)
	}
}

// TestSalesFactsPriorityOrder 准入事实按 priority DESC 排序（超限截断保前丢后）。
func TestSalesFactsPriorityOrder(t *testing.T) {
	st, conn := newFactI18nStore(t)
	for i, pri := range []int{1, 5, 3} {
		key := []string{"low", "high", "mid"}[i]
		id := insertSalesFact(t, conn, 1, "P", "spec", key, key, "confirmed", "public", 0, pri)
		insertFactI18n(t, conn, 1, id, "en", key, factSourceHash(key))
	}
	facts, _, err := st.ListSalesFactsForInjection(1, "", "en")
	if err != nil {
		t.Fatalf("准入读口失败: %v", err)
	}
	want := []string{"high", "mid", "low"}
	if len(facts) != len(want) {
		t.Fatalf("事实数不对: %d", len(facts))
	}
	for i, w := range want {
		if facts[i].Key != w {
			t.Fatalf("priority 排序错误: 第 %d 位 got=%s want=%s", i, facts[i].Key, w)
		}
	}
}

// TestSalesFactsPGDualDriver PG 双真驱动（无 PG_TEST_DSN 时跳过，SQLite 腿已在上面各测覆盖）。
func TestSalesFactsPGDualDriver(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("无 PG_TEST_DSN，跳过 PG 集成腿（SQLite 腿已覆盖）")
	}
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = string(db.DialectPostgres)
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Skipf("无可用 PostgreSQL: %v", err)
	}
	defer conn.Close()
	must := func(q string) {
		if _, err := conn.Exec(q); err != nil {
			t.Fatalf("PG DDL 失败: %s -> %v", q, err)
		}
	}
	must(`DROP TABLE IF EXISTS product_fact_i18n`)
	must(`DROP TABLE IF EXISTS product_facts`)
	must(`CREATE TABLE product_facts (
		id BIGSERIAL PRIMARY KEY, tenant_id BIGINT NOT NULL, product_id TEXT NOT NULL,
		kind TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'unverified', visibility TEXT NOT NULL DEFAULT 'gated',
		source TEXT NOT NULL DEFAULT 'manual', locked INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	st := &Store{db: conn}
	if err := st.ProductFactI18nMigrate(); err != nil {
		t.Fatalf("PG 上 ProductFactI18nMigrate 失败: %v", err)
	}
	id := insertSalesFact(t, conn, 9, "P", "price", "pg_key", "9 元", "confirmed", "public", 0, 0)
	insertFactI18n(t, conn, 9, id, "en", "9 CNY", factSourceHash("9 元"))
	facts, controlled, err := st.ListSalesFactsForInjection(9, "", "en")
	if err != nil {
		t.Fatalf("PG 准入读口失败: %v", err)
	}
	if len(facts) != 1 || facts[0].Value != "9 CNY" || len(controlled) != 0 {
		t.Fatalf("PG 准入结果不符: facts=%+v controlled=%+v", facts, controlled)
	}
}
