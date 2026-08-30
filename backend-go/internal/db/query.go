// ============ query.go · 职责说明 ============
// 数据库查询工具层：提供跨方言的 SQL 执行与查询接口，包括占位符改写、
// DDL 翻译、通用 Exec/Query/Prepare 方法，以及 INSERT 返回主键的 InsertID 方法。
// =============================================
package db

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Execer 可执行写操作的连接或事务（*sql.DB / *sql.Tx 均满足）。
type Execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// ContextExecer 可执行带上下文写操作的连接或事务（*sql.DB / *sql.Tx 均满足）。
type ContextExecer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// Querier 可查询的连接或事务（*sql.DB / *sql.Tx 均满足）。
type Querier interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// Preparer 可预备语句的连接（*sql.DB / *sql.Tx 均满足）。
type Preparer interface {
	Prepare(query string) (*sql.Stmt, error)
}

// RewritePlaceholders 将 SQLite 风格的位置占位符 ? 改写为 PostgreSQL 风格 $1/$2/...。
// 仅改写不在单引号字符串字面量内的 ?（字面量内的 ? 保持原样，例如 LIKE 模式或默认值）。
// 调用方传入的 query 为 SQLite 方言（唯一真源），PostgreSQL 下经此改写后即可复用同一段 SQL。
// 参数：query=SQLite 方言 SQL；返回改写后的 PostgreSQL 方言 SQL。
func RewritePlaceholders(query string) string {
	var b strings.Builder
	n := 0
	inLiteral := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			// 处理 '' 转义的连续单引号
			if inLiteral && i+1 < len(query) && query[i+1] == '\'' {
				b.WriteByte(c)
				b.WriteByte(c)
				i++
				continue
			}
			inLiteral = !inLiteral
			b.WriteByte(c)
		case c == '?' && !inLiteral:
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// pgTranslate 对 PostgreSQL 方言做完整翻译：先转 DDL 语义（AUTOINCREMENT/BLOB/REAL），
// 再改写占位符 ? -> $n。所有经本包执行的 SQL 均以 SQLite 方言为唯一真源。
// 参数：query=SQLite 方言 SQL；返回 PostgreSQL 方言 SQL。
func pgTranslate(query string) string {
	return RewritePlaceholders(ToDialect(query, DialectPostgres))
}

// Exec 按方言执行写操作，连接或事务通用。PostgreSQL 下自动翻译 DDL 并改写占位符。
// 参数：e=执行器（连接或事务）；d=方言；query=SQL 语句；args=参数；返回执行结果及错误。
func Exec(e Execer, d Dialect, query string, args ...interface{}) (sql.Result, error) {
	if d == DialectPostgres {
		query = pgTranslate(query)
	}
	return e.Exec(query, args...)
}

// ExecContext 按方言执行带上下文的写操作，连接或事务通用。PostgreSQL 下自动改写占位符。
// 参数：ctx=上下文；e=执行器；d=方言；query=SQL 语句；args=参数；返回执行结果及错误。
func ExecContext(ctx context.Context, e ContextExecer, d Dialect, query string, args ...interface{}) (sql.Result, error) {
	if d == DialectPostgres {
		query = pgTranslate(query)
	}
	return e.ExecContext(ctx, query, args...)
}

// Query 按方言执行查询，连接或事务通用。PostgreSQL 下自动翻译 DDL 并改写占位符。
// 参数：qr=查询器；d=方言；query=SQL 语句；args=参数；返回结果集及错误。
func Query(qr Querier, d Dialect, query string, args ...interface{}) (*sql.Rows, error) {
	if d == DialectPostgres {
		query = pgTranslate(query)
	}
	return qr.Query(query, args...)
}

// QueryRow 按方言执行单行查询，连接或事务通用。PostgreSQL 下自动翻译 DDL 并改写占位符。
// 参数：qr=查询器；d=方言；query=SQL 语句；args=参数；返回单行结果。
func QueryRow(qr Querier, d Dialect, query string, args ...interface{}) *sql.Row {
	if d == DialectPostgres {
		query = pgTranslate(query)
	}
	return qr.QueryRow(query, args...)
}

// Prepare 按方言预备语句。PostgreSQL 下自动翻译 DDL 并改写占位符（保证后续 stmt.Exec 可用 $n）。
// 参数：p=预备语句执行器；d=方言；query=SQL 语句；返回预备语句及错误。
func Prepare(p Preparer, d Dialect, query string) (*sql.Stmt, error) {
	if d == DialectPostgres {
		query = pgTranslate(query)
	}
	return p.Prepare(query)
}

// insertExecer 兼具 Exec 与 QueryRow 的连接或事务，供 InsertID 在 PostgreSQL 下经 RETURNING 取回主键。
type insertExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// InsertID 执行 INSERT 并返回自增主键，跨方言统一。
//   - SQLite：沿用 res.LastInsertId()。
//   - PostgreSQL：追加 RETURNING <pkCol> 并以 QueryRow 取回（lib/pq 不支持 LastInsertId）。
//     对 INSERT OR IGNORE 等"可能不插入"的语义，冲突时无行返回，按 SQLite 行为返回 0。
//
// 调用方传入的 query 为 SQLite 方言（INSERT ... VALUES(?...)），PostgreSQL 下自动改写。
// 参数：e=执行器；d=方言；pkCol=主键列名；query=INSERT 语句；args=参数；返回自增主键及错误。
func InsertID(e insertExecer, d Dialect, pkCol string, query string, args ...interface{}) (int64, error) {
	if d != DialectPostgres {
		// SQLite 路径：直接使用 LastInsertId
		res, err := e.Exec(query, args...)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	// PostgreSQL 路径：追加 RETURNING 子句并以 QueryRow 取回
	q := pgTranslate(strings.TrimRight(query, " ;")) + " RETURNING " + pkCol
	var id int64
	err := e.QueryRow(q, args...).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil // 等价于 SQLite 的 INSERT OR IGNORE 命中冲突：无新行，id 视为 0
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}
