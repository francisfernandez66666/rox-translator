// ============ no_knowledge_ddl_gate_test.go · KN11 验收闸门 ============
// 四期 W3 / KN11，2026-11-15 收口重写。
//
// ★ 原来的形态只有一条腿：「主库 New() 后产品四表必须不存在」。
//
//	它在本轮审计里被判为**结构上不可成立的空转锁**——
//	四期摘掉了主库的四条 CREATE TABLE，而 KNOWLEDGE_SERVICE_READ 默认 off，
//	32 处 kcCutover() 在 off 档回落主库 SQL ⇒ **off 档的主库里也没有表**，
//	于是「四表不存在」这条判据在「切读态正确」与「两边都没建、产品域全 500」两种现场下
//	给出同一个绿灯。判据形态必须按**开关两档各判一次**：
//
//	- 切读态（KNOWLEDGE_SERVICE_READ=on）⇒ 主库四表**全不存在**（KN11 终态）；
//	- 影子期（off，默认档）              ⇒ 主库四表**全存在且可用**（回滚路径的唯一依靠）。
//
// ★ 只测一条会给两类坏法放行：
//
//	① 「两边都不建」⇒ 只测切读态那条仍是绿的；
//	② 「不管开关都建」⇒ 只测 off 档那条仍是绿的。
//
// 反证：M0353（摘掉切读守卫，无条件建表）红在切读态那条；
//
//	M0354（把兜底改成恒 nil）红在影子期那条。
//
// ============================================================
package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
	knowledgeStore "translator/internal/knowledge/store"
)

// productTableNames 产品四表。
var productTableNames = []string{
	"products",
	"product_facts",
	"product_assets",
	"product_fact_i18n",
}

// pinKN11SQLite 固定方言并清掉切读开关（每个用例自己显式设，不继承上一条的 env）。
func pinKN11SQLite(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	t.Setenv("KNOWLEDGE_SERVICE_READ", "off")
}

// openKN11Store 打开一个文件库并跑 store.New()（生产迁移路径）。
func openKN11Store(t *testing.T, name string) *sql.DB {
	t.Helper()
	raw, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), name)})
	if err != nil {
		t.Fatalf("打开主库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := New(raw); err != nil {
		t.Fatalf("主库 New 失败: %v", err)
	}
	return raw
}

// TestKN11MainStoreHasNoProductTablesWhenCutover 切读态：主库不得存在任何产品表（KN11 终态）。
func TestKN11MainStoreHasNoProductTablesWhenCutover(t *testing.T) {
	pinKN11SQLite(t)
	t.Setenv("KNOWLEDGE_SERVICE_READ", "on")

	raw := openKN11Store(t, "main_cutover_no_product.db")
	d := db.CurrentDialect()
	for _, tbl := range productTableNames {
		exists, err := tableExists(raw, d, tbl)
		if err != nil {
			t.Fatalf("查表 %s 失败: %v", tbl, err)
		}
		if exists {
			t.Fatalf("KN11 违例：切读态主库 New() 后仍建了产品表 %s（产品表归知识服务，主库必须零表）", tbl)
		}
	}
}

// TestKN11MainStoreCreatesProductTablesWhenFallback 影子期（off，默认档）：主库必须建齐四表且可用。
//
// 这条是「新库产品域 500」那条 P0 的直接回归锁：off 档表不在 ⇒ 第一条产品查询就炸，
// 而启动链不报错。判据不能只问「表在不在」，还要真写读一行（PG/SQLite 双方言都能跑）。
func TestKN11MainStoreCreatesProductTablesWhenFallback(t *testing.T) {
	pinKN11SQLite(t) // env 已在 off
	raw := openKN11Store(t, "main_fallback_product.db")
	d := db.CurrentDialect()
	for _, tbl := range productTableNames {
		exists, err := tableExists(raw, d, tbl)
		if err != nil {
			t.Fatalf("查表 %s 失败: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("P0 回归：影子期（KNOWLEDGE_SERVICE_READ=off）主库没有产品表 %s —— 产品域第一条查询会 500，而启动链不报错", tbl)
		}
	}
	// 真写一行 + 真读回来：表在但列缺（例如 target_langs）同样会让产品域在运行时才炸。
	if _, err := db.Exec(raw, d,
		`INSERT INTO products (id, tenant_id, name, slug, tenant_code, page_code, created_at, updated_at)
		 VALUES ('kn11_p1', 1, 'KN11 兜底产品', 'kn11-p1', 'tc', 'pc', '2026-11-15T00:00:00Z', '2026-11-15T00:00:00Z')`); err != nil {
		t.Fatalf("影子期主库 products 写入失败（表在但结构不对）: %v", err)
	}
	var name, targetLangs string
	if err := db.QueryRow(raw, d,
		"SELECT name, target_langs FROM products WHERE id='kn11_p1'").Scan(&name, &targetLangs); err != nil {
		t.Fatalf("影子期主库 products 读回失败（target_langs 缺列即此处炸）: %v", err)
	}
	if name != "KN11 兜底产品" || targetLangs != "" {
		t.Fatalf("影子期主库 products 读回内容不对: name=%q target_langs=%q", name, targetLangs)
	}
}

// TestKN11KnowledgeStoreCreatesProductTables 知识服务迁移须在独立库上建齐四表（正判据）。
func TestKN11KnowledgeStoreCreatesProductTables(t *testing.T) {
	pinKN11SQLite(t)
	raw, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "knowledge_product.db")})
	if err != nil {
		t.Fatalf("打开知识库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	d := db.CurrentDialect()
	if err := knowledgeStore.KnowledgeMigrate(raw, d); err != nil {
		t.Fatalf("知识库迁移失败: %v", err)
	}
	for _, tbl := range productTableNames {
		exists, err := tableExists(raw, d, tbl)
		if err != nil {
			t.Fatalf("查知识表 %s 失败: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("KN11 违例：知识服务未建产品表 %s（应由 KnowledgeMigrate 负责）", tbl)
		}
	}
}
