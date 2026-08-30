// ============ rewrite.go · 职责说明 ============
// SQL 方言改写层：将 SQLite 方言的 DDL/DML 语句翻译为目标方言（PostgreSQL），
// 包括占位符改写、类型映射、UPSERT 语法转换等，以 SQLite 为唯一真源避免 Schema 漂移。
// =============================================
package db

import (
	"database/sql"
	"regexp"
	"strings"

	"translator/internal/config"
)

// ToDialect 将 SQLite 方言的 DDL/DML 语句翻译为目标方言。
//
// 设计原则：以现有 store/tenant/kb 中的 SQLite DDL 作为唯一真源，运行时按需翻译，
// 避免维护两份易漂移的 Schema。当前覆盖 P0-3 迁移所需的机械转换：
//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
//   - 独立 AUTOINCREMENT 关键字移除
//   - BLOB → BYTEA
//   - REAL → DOUBLE PRECISION
//   - 其余（TEXT / INTEGER / 默认值 / UNIQUE / 复合主键 / CREATE INDEX IF NOT EXISTS）保持
//
// 注：PRAGMA / pragma_table_info 等 SQLite 专属元数据查询不在本函数处理范围，
// 调用方需在 PostgreSQL 下跳过对应逻辑（见 store.migrate / tenant.ensureTable 的分支）。
// 参数：sqlText=SQLite 方言 SQL；d=目标方言；返回翻译后的 SQL。
func ToDialect(sqlText string, d Dialect) string {
	if d != DialectPostgres {
		return sqlText
	}
	out := sqlText
	// 顺序敏感：先处理带 AUTOINCREMENT 的主键，再清理残留关键字
	out = sqliteAutoIncPK.ReplaceAllString(out, "BIGSERIAL PRIMARY KEY")
	out = sqliteAutoInc.ReplaceAllString(out, "")
	out = sqliteBlob.ReplaceAllString(out, "BYTEA")
	out = sqliteReal.ReplaceAllString(out, "DOUBLE PRECISION")
	// INSERT OR IGNORE -> INSERT ... ON CONFLICT DO NOTHING（与 SQLite 忽略任意唯一冲突的语义一致）
	if sqliteInsertOrIgnore.MatchString(out) {
		out = sqliteInsertOrIgnore.ReplaceAllString(out, "INSERT INTO")
		out = sqliteTrailingSemi.ReplaceAllString(out, " ON CONFLICT DO NOTHING$1")
	}
	return out
}

// 正则均不区分大小写；\b 保证整词匹配，避免误伤子串。
var (
	sqliteAutoIncPK      = regexp.MustCompile(`(?i)INTEGER\s+PRIMARY\s+KEY\s+AUTOINCREMENT`) // 自增主键模式
	sqliteAutoInc        = regexp.MustCompile(`(?i)\s*AUTOINCREMENT`)                        // 独立 AUTOINCREMENT 关键字
	sqliteBlob           = regexp.MustCompile(`(?i)\bBLOB\b`)                                // BLOB 类型
	sqliteReal           = regexp.MustCompile(`(?i)\bREAL\b`)                                // REAL 类型
	sqliteInsertOrIgnore = regexp.MustCompile(`(?i)INSERT\s+OR\s+IGNORE\s+INTO`)             // INSERT OR IGNORE 模式
	sqliteTrailingSemi   = regexp.MustCompile(`(;?\s*)$`)                                    // 尾部分号与空白
)

// RewriteStmts 将多条语句整体翻译为目标方言（保留分号分隔，去除空语句）。
// 参数：sqlText=多语句 SQL 文本；d=目标方言；返回翻译后的 SQL 文本。
func RewriteStmts(sqlText string, d Dialect) string {
	if d != DialectPostgres {
		return sqlText
	}
	var b strings.Builder
	for _, s := range splitStmts(sqlText) {
		b.WriteString(ToDialect(s, d))
		b.WriteString(";\n")
	}
	return b.String()
}

// ExecDDL 按当前方言翻译并执行单条 DDL 语句（CREATE/INDEX 等）。
// 调用方传入的 sqlText 应为 SQLite 方言（作为唯一真源），PostgreSQL 下自动改写。
// 参数：conn=数据库连接；d=方言；sqlText=DDL 语句；返回执行错误。
func ExecDDL(conn *sql.DB, d Dialect, sqlText string) error {
	_, err := conn.Exec(ToDialect(sqlText, d))
	return err
}

// reAddColumn 匹配 ADD COLUMN 关键字（不区分大小写）
var reAddColumn = regexp.MustCompile(`(?i)ADD COLUMN`)

// RewriteAlter 将 ALTER 补列语句改写为目标方言版本：PostgreSQL 下自动补上
// IF NOT EXISTS 并做类型翻译，使其幂等可重复执行；SQLite 下原样返回。
// 参数：alterSQL=ALTER 语句；d=目标方言；返回改写后的 SQL。
func RewriteAlter(alterSQL string, d Dialect) string {
	sqlText := alterSQL
	if d == DialectPostgres {
		// PostgreSQL 下补上 IF NOT EXISTS 使补列幂等
		sqlText = reAddColumn.ReplaceAllString(sqlText, "ADD COLUMN IF NOT EXISTS")
		sqlText = ToDialect(sqlText, d)
	}
	return sqlText
}

// RunAlter 执行 ALTER 语句（目前仅用于 ADD COLUMN 补列）。
// PostgreSQL 下自动补上 IF NOT EXISTS 并做类型翻译，使其幂等可重复执行；
// SQLite 下原样执行（补列幂等性由调用方的 PRAGMA 列存在性检查保证）。
// 参数：conn=数据库连接；d=方言；alterSQL=ALTER 语句；返回执行错误。
func RunAlter(conn *sql.DB, d Dialect, alterSQL string) error {
	_, err := conn.Exec(RewriteAlter(alterSQL, d))
	return err
}

// CurrentDialect 依据全局配置返回当前数据库方言。
// 配置尚未初始化时（如早期启动）安全回退为 SQLite。
// 返回：当前数据库方言。
func CurrentDialect() Dialect {
	if config.C != nil && config.C.DatabaseDriver == string(DialectPostgres) {
		return DialectPostgres
	}
	return DialectSQLite
}
