package db

// Dialect 标识后端数据库方言。P0-3 目标是在单一抽象下同时支持 SQLite（当前默认）
// 与 PostgreSQL（待引入驱动后启用）。所有方言相关差异收敛到本文件与 migrate.go，
// 业务代码不直接出现 ? / $1 / AUTOINCREMENT 等方言特有关键字。
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// Placeholder 返回第 n（从 1 开始）个参数占位符：sqlite 用 ?，pg 用 $n。
// 调用方构造参数化 SQL 时统一经此函数，避免硬编码方言占位符。
func (d Dialect) Placeholder(n int) string {
	if d == DialectPostgres {
		return "$" + itoa(n)
	}
	return "?"
}

// IsPostgres 是否是 PostgreSQL 后端。
func (d Dialect) IsPostgres() bool { return d == DialectPostgres }

// AutoIncrementPK 返回自增主键列定义：sqlite 用 INTEGER PRIMARY KEY AUTOINCREMENT，
// pg 用 BIGSERIAL PRIMARY KEY。
func (d Dialect) AutoIncrementPK() string {
	if d == DialectPostgres {
		return "BIGSERIAL PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

// BoolLiteral 返回布尔字面量：sqlite 以 1/0 表示，pg 用 TRUE/FALSE。
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
func (d Dialect) NowFn() string {
	if d == DialectPostgres {
		return "NOW()"
	}
	return "CURRENT_TIMESTAMP"
}

// UpsertIgnore 生成"冲突则忽略"的插入后缀。
// cols 为冲突判定列（唯一/主键）；sqlite 用 INSERT OR IGNORE，
// pg 用 ON CONFLICT (cols) DO NOTHING。
func (d Dialect) UpsertIgnore(table string, cols []string) string {
	if d == DialectPostgres {
		return "ON CONFLICT (" + joinCols(cols) + ") DO NOTHING"
	}
	return "ON CONFLICT" // sqlite 语法为 INSERT OR IGNORE，由调用方拼接到 INSERT 头部
}

// UpsertUpdate 生成"冲突则更新"的后缀（UPSERT）。
// 当冲突列冲突时，用 SET 更新 nonConflict 列。
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
