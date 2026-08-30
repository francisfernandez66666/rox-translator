// Package db 提供数据库驱动抽象层，统一收敛"打开连接 + 驱动专属初始化"的入口，
// 为 P0-3（SQLite→PostgreSQL 迁移）提供可切换的基石。
//
// 设计约束与现状：
//   - 当前默认后端仍是 SQLite，行为与旧实现（kb.Open 内联 DSN）完全一致；
//     因此本改动对现有部署零影响，可作为 P0-3 的安全起点。
//   - 业务 SQL 仍为 SQLite 方言（AUTOINCREMENT / PRAGMA / INSERT OR IGNORE 等），
//     全量方言迁移是后续数周的工作，不在本文件范围内。
//   - 连接器本身不引入任何第三方驱动依赖；驱动由调用方通过 blank import 注册：
//       sqlite：   _ "modernc.org/sqlite"   （store/kb 已导入，自动注册）
//       postgres： _ "github.com/lib/pq"    或  _ "github.com/jackc/pgx/v5/stdlib"
//     待引入 PG 驱动并配置 DB_DRIVER=postgres / DB_DSN 后，仅改配置即可切换后端。
package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// 驱动标识
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// Config 描述一次数据库连接。
type Config struct {
	// Driver 数据库/sql 驱动名："sqlite"（默认）或 "postgres"。
	Driver string
	// DSN：
	//   - sqlite：文件路径（或 ":memory:"），连接器自动补齐加固参数。
	//   - postgres：完整连接串，如 postgres://user:pass@host:5432/db?sslmode=disable。
	DSN string
	// 连接池参数（仅 postgres 生效；sqlite 单写者模型忽略）。
	MaxOpenConns           int
	MaxIdleConns           int
	ConnMaxLifetimeMinutes int
	// EnablePgvector 连接时是否创建 vector 扩展（KB 向量检索，P0-2 依赖）。
	EnablePgvector bool
}

// Open 按配置打开 *sql.DB。
// 调用方需确保对应驱动已注册（见包文档的 blank import 说明）。
func Open(cfg Config) (*sql.DB, error) {
	switch cfg.Driver {
	case DriverSQLite, "":
		return openSQLite(cfg.DSN)
	case DriverPostgres, "pgx":
		return openPostgres(cfg)
	default:
		return nil, fmt.Errorf("db: 不支持的数据库驱动 %q", cfg.Driver)
	}
}

// SQLiteDSN 由文件路径构造 modernc/sqlite 的加固 DSN。
// 参数：dbPath=SQLite 文件路径（或 ":memory:"）；返回含 busy_timeout/WAL/
//   synchronous/_txlock 的完整 DSN。
func SQLiteDSN(dbPath string) string {
	return "file:" + dbPath +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
}

func openSQLite(dsn string) (*sql.DB, error) {
	if dsn == ":memory:" {
		dsn = "file::memory:?cache=shared&_txlock=immediate"
	} else if !strings.Contains(dsn, "?") {
		dsn = SQLiteDSN(dsn)
	}
	conn, err := sql.Open(DriverSQLite, dsn)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func openPostgres(cfg Config) (*sql.DB, error) {
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 20
	}
	conn, err := sql.Open(DriverPostgres, cfg.DSN)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(maxOpen)
	if cfg.MaxIdleConns > 0 {
		conn.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetimeMinutes > 0 {
		conn.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeMinutes) * time.Minute)
	}
	if cfg.EnablePgvector {
		// pgvector 仅 KB 向量检索依赖；缺失不影响其余功能（如未安装扩展）。
		if _, err := conn.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
			log.Printf("db: 创建 vector 扩展跳过（不影响非向量功能）：%v", err)
		}
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("db: postgres 连接探活失败: %w", err)
	}
	return conn, nil
}
