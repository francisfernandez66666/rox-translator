// ============ open.go · 职责说明 ============
// 四期 W3-2：知识服务**独立库**连接（只认 KNOWLEDGE_DB_DRIVER / KNOWLEDGE_DB_DSN）。
//
//   - 主服务进程不持有本连接；只有 cmd/knowledge-server 与迁移工具打开它（§4.3.1 第 2 条）。
//   - 方言不读全局 config.C（db.CurrentDialect 会读主库配置），而是按本连接的 driver 显式钉，
//     这样同一进程里主库/知识库即便方言不同也各走各的包装器（双方言硬约定 AGENTS §一·4）。
// ================================================
package store

import (
	"database/sql"
	"fmt"

	"translator/internal/db"

	_ "modernc.org/sqlite" // 注册 sqlite 驱动（本地/测试知识库）
)

// Config 知识库独立连接参数（只从 KNOWLEDGE_DB_DRIVER / KNOWLEDGE_DB_DSN 来）。
type Config struct {
	Driver string
	DSN    string
}

// KnowledgeStore 知识库连接与方言。
type KnowledgeStore struct {
	conn    *sql.DB
	Dialect db.Dialect
}

func dialectOf(driver string) db.Dialect {
	if driver == db.DriverPostgres || driver == "pgx" {
		return db.DialectPostgres
	}
	return db.DialectSQLite
}

// Open 打开独立知识库连接。DSN 为空直接报错（不许静默用主库顶上，KN12）。
func Open(cfg Config) (*KnowledgeStore, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("knowledge: KNOWLEDGE_DB_DSN 为空，拒绝连接（独立库硬边界 KN12）")
	}
	conn, err := db.Open(db.Config{Driver: cfg.Driver, DSN: cfg.DSN})
	if err != nil {
		return nil, fmt.Errorf("knowledge: 打开独立知识库失败: %w", err)
	}
	return &KnowledgeStore{conn: conn, Dialect: dialectOf(cfg.Driver)}, nil
}

// OpenDB 用已打开的 *sql.DB 构造（搬迁/反向搬迁/测试直接传两库连接，不额外开连接）。
func OpenDB(conn *sql.DB, d db.Dialect) *KnowledgeStore {
	return &KnowledgeStore{conn: conn, Dialect: d}
}

// DB 返回底层连接（供迁移与端点复用）。
func (k *KnowledgeStore) DB() *sql.DB { return k.conn }

// Close 关闭独立知识库连接。
func (k *KnowledgeStore) Close() error {
	if k.conn != nil {
		return k.conn.Close()
	}
	return nil
}

// Migrate 在独立库上建四张知识表（幂等）。
func (k *KnowledgeStore) Migrate() error { return KnowledgeMigrate(k.conn, k.Dialect) }
