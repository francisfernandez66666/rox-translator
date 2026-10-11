// Package db 提供数据库驱动抽象层，统一收敛"打开连接 + 驱动专属初始化"的入口，
// 为 P0-3（SQLite→PostgreSQL 迁移）提供可切换的基石。
//
// 设计约束与现状：
//   - 当前默认后端仍是 SQLite，行为与旧实现（kb.Open 内联 DSN）完全一致；
//     因此本改动对现有部署零影响，可作为 P0-3 的安全起点。
//   - 业务 SQL 仍为 SQLite 方言（AUTOINCREMENT / PRAGMA / INSERT OR IGNORE 等），
//     全量方言迁移是后续数周的工作，不在本文件范围内。
//   - 连接器本身不引入任何第三方驱动依赖；驱动由调用方通过 blank import 注册：
//     sqlite：   _ "modernc.org/sqlite"   （store/kb 已导入，自动注册）
//     postgres： _ "github.com/lib/pq"    或  _ "github.com/jackc/pgx/v5/stdlib"
//     待引入 PG 驱动并配置 DB_DRIVER=postgres / DB_DSN 后，仅改配置即可切换后端。
package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// 驱动标识：用于 Config.Driver 取值，决定 Open 走哪条连接分支。
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
//
//	synchronous/_txlock 的完整 DSN。
func SQLiteDSN(dbPath string) string {
	return "file:" + dbPath +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
}

// openSQLite 打开 SQLite 数据库连接。
// 参数：dsn 为原始 DSN：若为 ":memory:" 则改用共享缓存的内存库；
//
//	若未带查询参数则通过 SQLiteDSN 补齐加固参数（busy_timeout/WAL 等）；已带参数则原样使用。
//
// 返回打开的 *sql.DB，失败返回错误。注意不设置连接池（SQLite 单写者模型由锁与 WAL 保证）。
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

// openPostgres 打开 PostgreSQL 数据库连接并应用连接池与可选扩展初始化。
// 参数：cfg 提供 DSN、连接池参数及是否启用 pgvector 扩展。
//   - 连接池：MaxOpenConns 默认 20；MaxIdleConns 缺省兜底 10（不越过 MaxOpen）、
//     ConnMaxLifetime 缺省兜底 30 分钟（★ 2026-10-11 慢 SQL 批 P1③，仅 postgres，见函数体注释）；
//   - EnablePgvector 为 true 时执行 CREATE EXTENSION IF NOT EXISTS vector，失败仅告警不影响其余功能；
//   - 最后执行 Ping 探活，失败返回带上下文的错误。
//
// 返回打开且已验证的 *sql.DB，失败返回错误。
func openPostgres(cfg Config) (*sql.DB, error) {
	conn, err := sql.Open(DriverPostgres, cfg.DSN)
	if err != nil {
		return nil, err
	}
	applyPoolDefaults(conn, cfg)
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

// ★★ 连接池兜底默认（2026-10-11 慢 SQL 批 P1③）——**只此一把尺子**：
//
//	旧形态 env 不配就"什么都不设"，实际落进 Go 的出厂档（MaxIdleConns=2、ConnMaxLifetime=无限）。
//	现网取证的形态与之吻合：突发流量后连接反复"建-拆-建"（2 个 idle 撑不住 20 的开），
//	而长寿连接永不回收，中间层（NAT/防火墙）静默掐掉的半死连接要等下一次取用才现形。
//	≤0 一律按"没配"走代码默认（env > 数据库配置 > 代码默认的三级优先级不变，
//	调用方传进来的 cfg 已含前两级）；显式配了值的语义一字不变。
//	**只动 postgres**——SQLite（含 :memory: 共享缓存）的连接寿命会连带销毁内存库本体，
//	openSQLite 绝不许引用这三档（等值锁见 pool_defaults_test.go 的反证段）。
const (
	poolDefaultMaxOpenConns = 20
	poolDefaultMaxIdleConns = 10
	poolDefaultConnLifeMin  = 30
)

// poolSettings 解析出最终三档（maxIdle 永不越过 maxOpen——超出没有行为收益，
// 但会让"配了 100 idle / 20 open"这种运维读数在排障时骗人）。
func poolSettings(cfg Config) (maxOpen, maxIdle int, lifeMin int) {
	maxOpen = cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = poolDefaultMaxOpenConns
	}
	maxIdle = cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = poolDefaultMaxIdleConns
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	lifeMin = cfg.ConnMaxLifetimeMinutes
	if lifeMin <= 0 {
		lifeMin = poolDefaultConnLifeMin
	}
	return maxOpen, maxIdle, lifeMin
}

// poolApplier 是 *sql.DB 的最小 setter 面：单测用假 setter 记参，**守卫真跑的这七个字**
// （只测 poolSettings 属于"判据级代替不了守卫级"，AGENTS §三 那条形态）。
type poolApplier interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
}

// applyPoolDefaults 把 poolSettings 的三项档位真正施加到连接池（openPostgres 唯一调用点）。
// 参数 ap=连接池 setter（测试注假 setter 记参），cfg=当前配置；无返回。
func applyPoolDefaults(ap poolApplier, cfg Config) {
	maxOpen, maxIdle, lifeMin := poolSettings(cfg)
	ap.SetMaxOpenConns(maxOpen)
	ap.SetMaxIdleConns(maxIdle)
	ap.SetConnMaxLifetime(time.Duration(lifeMin) * time.Minute)
}
