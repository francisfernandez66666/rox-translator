// ============ cmd/knowledge-migrate · 职责说明 ============
// 四期 W3-3 正向搬迁 CLI：主库 → 独立知识库（四张知识表 products/product_facts/
// product_assets/product_fact_i18n）。
//
//   - 复用 internal/knowledge.Migrate：显式 id 幂等 upsert + PG 序列复位 + 双向指纹校验，
//     指纹不等即报错退出，绝不静默宣称成功。
//   - KN12：启动期 AssertDistinct 硬断言双 DSN 不等，相等即拒绝（不许「同库不同进程」）。
//   - 知识库四表由 knowledgeStore.Migrate() 先建（幂等），再 copyFourTables 灌数据。
//
// 用法（部署机，env 与知识服务同形）：
//
//	KNOWLEDGE_DB_DSN=postgres://.../langcross_knowledge \
//	DB_DSN=postgres://.../langcross \
//	KNOWLEDGE_DB_DRIVER=postgres DB_DRIVER=postgres \
//	./knowledge-migrate
//
// 判绿：末行日志 matched=true；指纹不等或任一表失败 ⇒ 退出码 1。
// ================================================
package main

import (
	"context"
	"log/slog"
	"os"

	"translator/internal/db"
	"translator/internal/knowledge"
	knowledgeStore "translator/internal/knowledge/store"
	"translator/internal/observability"

	_ "github.com/lib/pq"  // PG 驱动（生产 KNOWLEDGE_DB_DRIVER=postgres）
	_ "modernc.org/sqlite" // SQLite 驱动（本地/测试）
)

func main() {
	slog.SetDefault(observability.NewLogger())
	cfg := knowledge.LoadConfig()

	// KN12：双 DSN 不等硬断言（相等/空即拒绝，不许同库不同进程）。
	if err := knowledge.StartupGuard(cfg); err != nil {
		slog.Error("迁移拒绝启动（双 DSN 断言 KN12）", "err", err)
		os.Exit(1)
	}

	// 主库连接（知识四表的源）。
	mainDB, err := db.Open(db.Config{Driver: cfg.MainDriver, DSN: cfg.MainDSN})
	if err != nil {
		slog.Error("主库打开失败", "err", err)
		os.Exit(1)
	}
	defer mainDB.Close()

	// 独立知识库连接（目标）。先建四表（幂等），再灌数据。
	ks, err := knowledgeStore.Open(knowledgeStore.Config{Driver: cfg.KnowledgeDriver, DSN: cfg.KnowledgeDSN})
	if err != nil {
		slog.Error("知识库打开失败", "err", err)
		os.Exit(1)
	}
	defer ks.Close()
	if err := ks.Migrate(); err != nil {
		slog.Error("知识库四表建表失败", "err", err)
		os.Exit(1)
	}

	mainD := db.DialectSQLite
	if cfg.MainDriver == db.DriverPostgres {
		mainD = db.DialectPostgres
	}

	rep, err := knowledge.Migrate(context.Background(), mainDB, mainD, ks.DB(), ks.Dialect)
	if err != nil {
		slog.Error("知识四表迁移失败", "err", err)
		os.Exit(1)
	}
	slog.Info("知识四表迁移完成", "matched", rep.Matched, "counts", rep.Counts, "hashes", rep.Hashes)
	if !rep.Matched {
		slog.Error("知识四表迁移指纹不一致（拒绝宣称成功）")
		os.Exit(1)
	}
	slog.Info("KNOWLEDGE_MIGRATE_OK=1")
}
