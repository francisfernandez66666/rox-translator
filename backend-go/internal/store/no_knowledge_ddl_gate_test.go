// ============ no_knowledge_ddl_gate_test.go · KN11 验收闸门 ============
// ★ 终态硬判据：产品域已整体迁至知识服务（独立 DSN）。
//
//   - 主库 New() 启动迁移后，产品四表（products / product_facts / product_assets /
//     product_fact_i18n）必须**全部不存在**——它们由 knowledge/store.KnowledgeMigrate
//     在独立知识库上建，主库启动链只摘不建（store.go migrate() 已删三表 CREATE，
//     ProductFactI18nMigrate 已脱离 New() 启动链）。
//   - 对应正判据：知识服务 KnowledgeMigrate 必须在独立库上建齐四表。
//
// 注：本文件测的是「生产 migrate 路径」，与产品行为单测里用 migrateProductTablesTest
// 在测试库重建四表（测主库回滚路径）互不干扰——那条 helper 只作用于测试 Store，不影响
// 此处的主库 New() 真值。
// ============================================================
package store

import (
	"path/filepath"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
	knowledgeStore "translator/internal/knowledge/store"
)

// productTableNames 产品四表（已迁知识服务）。
var productTableNames = []string{
	"products",
	"product_facts",
	"product_assets",
	"product_fact_i18n",
}

// TestMainStoreHasNoProductTables 主库 New() 后不得存在任何产品表（KN11 终态）。
func TestMainStoreHasNoProductTables(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "main_no_product.db")})
	if err != nil {
		t.Fatalf("打开主库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := New(raw); err != nil {
		t.Fatalf("主库 New 失败: %v", err)
	}

	d := db.CurrentDialect()
	for _, tbl := range productTableNames {
		exists, err := tableExists(raw, d, tbl)
		if err != nil {
			t.Fatalf("查表 %s 失败: %v", tbl, err)
		}
		if exists {
			t.Fatalf("KN11 违例：主库 New() 后仍建了产品表 %s（应已整体迁知识服务）", tbl)
		}
	}
}

// TestKnowledgeStoreCreatesProductTables 知识服务迁移须在独立库上建齐四表（正判据）。
func TestKnowledgeStoreCreatesProductTables(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

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
