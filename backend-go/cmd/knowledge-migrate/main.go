// ============ cmd/knowledge-migrate · 职责说明 ============
// 四期 W3-3 / W5 收口：知识库迁移与切读收尾的**显式命令**。
//
// 用法（向后兼容）：
//
//	./knowledge-migrate                       # 原正向搬迁：主库 → 知识库四表（现网既有调用方式，零改动）
//	./knowledge-migrate --sessions            # W5 会话迁移预检（默认 dry-run，只读）
//	./knowledge-migrate --sessions --apply    # 真搬：assist 库 sessions_base/messages_base → 主库
//	./knowledge-migrate --kb                  # W5 KB 迁移预检（默认 dry-run，只读）
//	./knowledge-migrate --kb --apply          # 真搬：assist 库四张 KB 表 → 知识库
//	./knowledge-migrate --drop-main-product-tables --yes   # 切读收尾：删主库四表（不可逆）
//
// ★ 三条硬口径（收口方案 §3.6 / §1.4）：
//  1. --sessions / --kb **默认 dry-run**：先出「src 行数 vs dst 行数」，显式 --apply 才写；
//  2. 写前自动备份目标库文件（SQLite）；PG 无法文件复制 ⇒ 明确打印 BACKUP_SKIPPED=pg，
//     绝不把「没备份」写成「已备份」；
//  3. 迁移失败（含半搬完）一律 error + 退出码 1，绝不沉默宣称成功。
//
// ★ --drop 必须同时给 --yes，且要求知识库四表在位（唯一真源确认后才允许删主库表）。
//
//	本命令**不进任何启动链**，只由运维在部署机显式拨一次。
//
// ================================================
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"translator/internal/api"
	assistconfig "translator/internal/assist/config"
	assiststore "translator/internal/assist/store"
	"translator/internal/db"
	"translator/internal/knowledge"
	"translator/internal/knowledge/schema"
	knowledgeStore "translator/internal/knowledge/store"
	"translator/internal/observability"

	_ "github.com/lib/pq"  // PG 驱动（生产 KNOWLEDGE_DB_DRIVER=postgres）
	_ "modernc.org/sqlite" // SQLite 驱动（本地/测试）
)

// cliArgs 本命令的全部显式开关（默认全 false ⇒ 保持旧正向搬迁行为）。
type cliArgs struct {
	sessions bool
	kb       bool
	dropMain bool
	yes      bool
	apply    bool
}

// main 解析显式开关并分派到对应迁移动作；未给开关时保持旧正向搬迁，任何失败均非零退出。
func main() {
	slog.SetDefault(observability.NewLogger())
	var a cliArgs
	flag.BoolVar(&a.sessions, "sessions", false, "W5：assist 会话/消息迁主库（默认 dry-run）")
	flag.BoolVar(&a.kb, "kb", false, "W5：assist 四张 KB 表迁知识库（默认 dry-run）")
	flag.BoolVar(&a.dropMain, "drop-main-product-tables", false, "切读收尾：删主库四张产品表（不可逆，须 --yes）")
	flag.BoolVar(&a.yes, "yes", false, "确认执行不可逆的 drop")
	flag.BoolVar(&a.apply, "apply", false, "把 --sessions/--kb 的 dry-run 预检升级为真写")
	flag.Parse()

	cfg := knowledge.LoadConfig()
	switch {
	case a.dropMain:
		exitOn(runDropMain(cfg, a))
	case a.sessions:
		exitOn(runSessions(cfg, a))
	case a.kb:
		exitOn(runKB(cfg, a))
	default:
		exitOn(runForwardMigrate(cfg))
	}
}

// exitOn 统一失败出口：半搬完/预检失败一律非零退出（不许静默宣称成功）。
func exitOn(err error) {
	if err != nil {
		slog.Error("knowledge-migrate 失败", "err", err)
		os.Exit(1)
	}
}

// openMain 打开主库并推导方言。
func openMain(cfg knowledge.Config) (*sql.DB, db.Dialect, error) {
	conn, err := db.Open(db.Config{Driver: cfg.MainDriver, DSN: cfg.MainDSN})
	if err != nil {
		return nil, db.DialectSQLite, fmt.Errorf("主库打开失败: %w", err)
	}
	d := db.DialectSQLite
	if cfg.MainDriver == db.DriverPostgres {
		d = db.DialectPostgres
	}
	return conn, d, nil
}

// openKnowledge 打开独立知识库并建四表（幂等）。
func openKnowledge(cfg knowledge.Config) (*knowledgeStore.KnowledgeStore, error) {
	ks, err := knowledgeStore.Open(knowledgeStore.Config{Driver: cfg.KnowledgeDriver, DSN: cfg.KnowledgeDSN})
	if err != nil {
		return nil, fmt.Errorf("知识库打开失败: %w", err)
	}
	if err := ks.Migrate(); err != nil {
		_ = ks.Close()
		return nil, fmt.Errorf("知识库四表建表失败: %w", err)
	}
	return ks, nil
}

// openAssist 打开 assist 独立 SQLite（路径沿用 cmd/assist-server 的 ASSIST_DB）。
func openAssist() (*assiststore.DB, string, error) {
	path := assistconfig.Load().DBPath
	adb, err := assiststore.Open(path)
	if err != nil {
		return nil, path, fmt.Errorf("assist 库打开失败: %w", err)
	}
	return adb, path, nil
}

// countRows 只读计数；表不存在返回 (0,false)，不把它伪装成「0 行且一切正常」。
func countRows(conn *sql.DB, table string) (int64, bool) {
	var n int64
	if err := conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		return 0, false
	}
	return n, true
}

// printCounts 打印 src/dst 逐表读数（§8.1 预检的第 5/6 条）。
func printCounts(label string, src, dst *sql.DB, tables []string) {
	for _, t := range tables {
		sn, sok := countRows(src, t)
		dn, dok := countRows(dst, t)
		slog.Info(label, "table", t, "src", sn, "src_present", sok, "dst", dn, "dst_present", dok)
	}
}

// needWrite 方案 §3.6 第 1 条：只读计数逐表对账，**行数不等才进写**。
// 目标表不存在（dst_present=false）也算需要写——迁移同时负责建表，
// 不能把「表还没建」误判成「两边都是 0、已经同步」。
// 参数：src=源库；dst=目标库；tables=待对账表名；返回：是否需要真写。
func needWrite(src, dst *sql.DB, tables []string) bool {
	for _, t := range tables {
		sn, _ := countRows(src, t)
		dn, ok := countRows(dst, t)
		if !ok || sn != dn {
			return true
		}
	}
	return false
}

// backupSQLite 写前备份目标库文件（同 assist_kb_sync.py 的 <db>.bak.<TS> 口径）。
// PG 或非文件 DSN 明确返回 skipped，绝不假装备份成功。返回：备份路径与是否跳过。
func backupSQLite(driver, dsn string) (string, bool) {
	if driver == db.DriverPostgres {
		slog.Warn("BACKUP_SKIPPED=pg（PG 需运维自行 pg_dump；本命令不谎报已备份）")
		return "", true
	}
	path := strings.TrimPrefix(strings.TrimSpace(dsn), "file:")
	if path == "" {
		slog.Warn("BACKUP_SKIPPED=empty_dsn", "dsn", dsn)
		return "", true
	}
	if _, err := os.Stat(path); err != nil {
		slog.Warn("BACKUP_SKIPPED=file_absent", "path", path, "err", err)
		return "", true
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("BACKUP_FAILED", "path", path, "err", err)
		return "", true
	}
	dest := fmt.Sprintf("%s.bak.%s", path, time.Now().UTC().Format("20060102_150405"))
	if err := os.WriteFile(dest, raw, 0o600); err != nil {
		slog.Warn("BACKUP_FAILED", "dest", dest, "err", err)
		return "", true
	}
	slog.Info("BACKUP_OK", "dest", dest)
	return dest, false
}

// runForwardMigrate 原正向搬迁（主库 → 知识库四表），行为与本命令改造前逐字等价。
// KN12：启动期 AssertDistinct 硬断言双 DSN 不等，相等即拒绝（不许「同库不同进程」）。
func runForwardMigrate(cfg knowledge.Config) error {
	if err := knowledge.StartupGuard(cfg); err != nil {
		return fmt.Errorf("迁移拒绝启动（双 DSN 断言 KN12）: %w", err)
	}
	mainDB, err := db.Open(db.Config{Driver: cfg.MainDriver, DSN: cfg.MainDSN})
	if err != nil {
		return fmt.Errorf("主库打开失败: %w", err)
	}
	defer mainDB.Close()
	ks, err := openKnowledge(cfg)
	if err != nil {
		return err
	}
	defer ks.Close()
	mainD := db.DialectSQLite
	if cfg.MainDriver == db.DriverPostgres {
		mainD = db.DialectPostgres
	}
	rep, err := knowledge.Migrate(context.Background(), mainDB, mainD, ks.DB(), ks.Dialect)
	if err != nil {
		return fmt.Errorf("知识四表迁移失败: %w", err)
	}
	slog.Info("知识四表迁移完成", "matched", rep.Matched, "counts", rep.Counts, "hashes", rep.Hashes)
	if !rep.Matched {
		return fmt.Errorf("知识四表迁移指纹不一致（拒绝宣称成功）")
	}
	slog.Info("KNOWLEDGE_MIGRATE_OK=1")
	return nil
}

// runSessions W5 会话迁移：只读计数 → --apply 才备份+真搬。
// 不需要知识库连接，故不跑 KN12（本地 SQLite 形态也能预检）。
func runSessions(cfg knowledge.Config, a cliArgs) error {
	mainDB, mainD, err := openMain(cfg)
	if err != nil {
		return err
	}
	defer mainDB.Close()
	adb, path, err := openAssist()
	if err != nil {
		return err
	}
	defer adb.Close()
	tables := api.AssistChatTableNames()
	slog.Info("sessions 预检", "assist_db", path, "tables", len(tables))
	printCounts("sessions 行数", adb.SQL(), mainDB, tables)
	if !a.apply {
		slog.Info("DRY_RUN=1（--sessions 未给 --apply，未写任何一行）")
		return nil
	}
	if !needWrite(adb.SQL(), mainDB, tables) {
		slog.Info("NOOP=1（src/dst 逐表行数一致且目标表均在位，跳过写入；迁移幂等）")
		return nil
	}
	backupSQLite(cfg.MainDriver, cfg.MainDSN)
	if err := api.MigrateAssistSessionsToMain(context.Background(), mainDB, adb.SQL(), mainD); err != nil {
		return fmt.Errorf("assist 会话迁移失败（半搬完必须报错退出）: %w", err)
	}
	printCounts("sessions 迁移后", adb.SQL(), mainDB, tables)
	slog.Info("SESSIONS_MIGRATE_OK=1")
	return nil
}

// runKB W5 KB 迁移：只读计数 → --apply 才备份+真搬。
func runKB(cfg knowledge.Config, a cliArgs) error {
	if err := knowledge.StartupGuard(cfg); err != nil {
		return fmt.Errorf("KB 迁移拒绝启动（双 DSN 断言 KN12）: %w", err)
	}
	adb, path, err := openAssist()
	if err != nil {
		return err
	}
	defer adb.Close()
	ks, err := openKnowledge(cfg)
	if err != nil {
		return err
	}
	defer ks.Close()
	tables := assiststore.KBTableNames()
	slog.Info("kb 预检", "assist_db", path, "tables", len(tables))
	printCounts("kb 行数", adb.SQL(), ks.DB(), tables)
	if !a.apply {
		slog.Info("DRY_RUN=1（--kb 未给 --apply，未写任何一行）")
		return nil
	}
	if !needWrite(adb.SQL(), ks.DB(), tables) {
		slog.Info("NOOP=1（src/dst 逐表行数一致且目标表均在位，跳过写入；迁移幂等）")
		return nil
	}
	backupSQLite(cfg.KnowledgeDriver, cfg.KnowledgeDSN)
	if err := adb.MigrateKBToKnowledge(context.Background(), ks.DB(), ks.Dialect); err != nil {
		return fmt.Errorf("KB 迁移失败（半搬完必须报错退出）: %w", err)
	}
	printCounts("kb 迁移后", adb.SQL(), ks.DB(), tables)
	slog.Info("KB_MIGRATE_OK=1")
	return nil
}

// runDropMain 切读收尾：确认知识库四表在位（唯一真源）后才删主库四表。
func runDropMain(cfg knowledge.Config, a cliArgs) error {
	if !a.yes {
		return fmt.Errorf("拒绝执行：--drop-main-product-tables 是不可逆操作，必须显式给 --yes")
	}
	if err := knowledge.StartupGuard(cfg); err != nil {
		return fmt.Errorf("drop 拒绝启动（双 DSN 断言 KN12）: %w", err)
	}
	mainDB, mainD, err := openMain(cfg)
	if err != nil {
		return err
	}
	defer mainDB.Close()
	ks, err := openKnowledge(cfg)
	if err != nil {
		return err
	}
	defer ks.Close()
	// 前置：知识库四表必须都在（否则删主库表＝把唯一真源删了）。
	for _, t := range schema.DropOrder {
		if _, ok := countRows(ks.DB(), t); !ok {
			return fmt.Errorf("拒绝 drop：知识库表 %s 不存在（唯一真源未就位）", t)
		}
	}
	printCounts("drop 前读数", mainDB, ks.DB(), schema.DropOrder)
	backupSQLite(cfg.MainDriver, cfg.MainDSN)
	if err := schema.DropProductTablesForRollback(mainDB, mainD); err != nil {
		return fmt.Errorf("删主库四表失败: %w", err)
	}
	slog.Info("DROP_MAIN_PRODUCT_TABLES_OK=1（主库四表已删，回滚须走 reverse_migrate）")
	return nil
}
