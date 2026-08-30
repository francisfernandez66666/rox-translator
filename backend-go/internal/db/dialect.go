// ============ dialect.go · 职责说明 ============
// 数据库方言抽象层：定义 Dialect 类型及 SQLite/PostgreSQL 双方言适配方法，
// 包括占位符、自增主键、布尔字面量、时间函数、UPSERT 语法等差异收敛。
// 所有方言差异收敛到本文件与 migrate.go，业务代码不直接出现方言特有关键字。
// =============================================
package db

// Dialect 标识后端数据库方言。P0-3 目标是在单一抽象下同时支持 SQLite（当前默认）
// 与 PostgreSQL（待引入驱动后启用）。所有方言相关差异收敛到本文件与 migrate.go，
// 业务代码不直接出现 ? / $1 / AUTOINCREMENT 等方言特有关键字。
type Dialect string

// 方言常量：定义支持的数据库方言类型。
const (
	DialectSQLite   Dialect = "sqlite"   // SQLite 方言（当前默认）
	DialectPostgres Dialect = "postgres" // PostgreSQL 方言（P0-3 待启用）
)

// Placeholder 返回第 n（从 1 开始）个参数占位符：sqlite 用 ?，pg 用 $n。
// 调用方构造参数化 SQL 时统一经此函数，避免硬编码方言占位符。
// 参数：n=参数序号（从 1 开始）；返回对应方言的占位符字符串。
func (d Dialect) Placeholder(n int) string {
	if d == DialectPostgres {
		return "$" + itoa(n)
	}
	return "?"
}

// IsPostgres 是否是 PostgreSQL 后端。
// 返回：true 表示当前方言为 PostgreSQL。
func (d Dialect) IsPostgres() bool { return d == DialectPostgres }

// AutoIncrementPK 返回自增主键列定义：sqlite 用 INTEGER PRIMARY KEY AUTOINCREMENT，
// pg 用 BIGSERIAL PRIMARY KEY。
// 返回：对应方言的自增主键列定义字符串。
func (d Dialect) AutoIncrementPK() string {
	if d == DialectPostgres {
		return "BIGSERIAL PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

// BoolLiteral 返回布尔字面量：sqlite 以 1/0 表示，pg 用 TRUE/FALSE。
// 参数：b=布尔值；返回对应方言的布尔字面量字符串。
func (d Dialect) BoolLiteral(b bool) string {
	if d == DialectPostgres {
		if b {
			return "TRUE"
		}
		return "FALSE"
	}
	if b {
		return "1"
	}
	return "0"
}

// NowFn 返回取当前时间戳的表达式：sqlite 用 CURRENT_TIMESTAMP（文本），
// pg 用 NOW()（timestamptz）。
// 返回：对应方言的当前时间表达式字符串。
func (d Dialect) NowFn() string {
	if d == DialectPostgres {
		return "NOW()"
	}
	return "CURRENT_TIMESTAMP"
}

// UpsertIgnore 生成"冲突则忽略"的插入后缀。
// cols 为冲突判定列（唯一/主键）；sqlite 用 INSERT OR IGNORE，
// pg 用 ON CONFLICT (cols) DO NOTHING。
// 参数：table=表名；cols=冲突判定列列表；返回对应方言的 UPSERT 后缀。
func (d Dialect) UpsertIgnore(table string, cols []string) string {
	if d == DialectPostgres {
		return "ON CONFLICT (" + joinCols(cols) + ") DO NOTHING"
	}
	return "ON CONFLICT" // sqlite 语法为 INSERT OR IGNORE，由调用方拼接到 INSERT 头部
}

// UpsertUpdate 生成"冲突则更新"的后缀（UPSERT）。
// 当冲突列冲突时，用 SET 更新 nonConflict 列。
// 参数：conflict=冲突判定列列表；updateCols=需要更新的列列表；返回对应方言的 UPSERT 后缀。
func (d Dialect) UpsertUpdate(conflict []string, updateCols []string) string {
	if d == DialectPostgres {
		setExpr := ""
		for i, c := range updateCols {
			if i > 0 {
				setExpr += ", "
			}
			setExpr += c + " = EXCLUDED." + c
		}
		return "ON CONFLICT (" + joinCols(conflict) + ") DO UPDATE SET " + setExpr
	}
	// sqlite 无标准 UPSERT 后缀，需调用方使用 INSERT OR REPLACE / 独立 UPDATE。
	return ""
}

// itoa 整数转字符串（避免引入 strconv 依赖）。
// 参数：n=整数；返回十进制字符串表示。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// joinCols 将列名列表用逗号连接为字符串（签名拼接辅助）。
// 参数：cols=列名列表；返回逗号分隔的列名字符串。
func joinCols(cols []string) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}
