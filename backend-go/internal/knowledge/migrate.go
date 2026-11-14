// ============ migrate.go · 职责说明 ============
// 四期 W3-3：知识四表从主库搬到独立知识库（§4.3.2 / §4.3.7 / KN8）。
//
//   - 真 SQL：显式 id 插入、按主键 ON CONFLICT DO UPDATE 幂等 upsert（跑两遍行数不变）。
//   - PG：搬完 setval(pg_get_serial_sequence(...)) 复位序列，否则下一个 INSERT 撞唯一键。
//     SQLite AUTOINCREMENT 自己按 max(id) 续，不复位。
//   - 双向指纹：行数 + 每表「COALESCE 拼接哈希」（全量列，独立于搬迁列清单）。
//     指纹不等 ⇒ 返回 error，绝不静默宣称成功。
//   - 列清单唯一真值＝ contract.TableColumns；少搬一列会让目标库该列落默认值 ⇒ 指纹不等 ⇒ 红。
// ================================================
package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
)

// tableSpec 一张表的搬迁规格。
type tableSpec struct {
	Table   string
	Columns []string // 本次 SELECT/INSERT 的列（显式 id 必含）
	PK      string
	AutoInc bool // PG 侧需要 setval 复位
}

func isAutoInc(t string) bool {
	for _, a := range contract.AutoIncrementTables {
		if a == t {
			return true
		}
	}
	return false
}

// MigrationTables 正向搬迁（主库 → 知识库）的列规格（从 contract 全量清单取拷贝）。
func MigrationTables() []tableSpec {
	out := make([]tableSpec, 0, len(contract.KnowledgeTableNames))
	for _, t := range contract.KnowledgeTableNames {
		out = append(out, tableSpec{
			Table:   t,
			Columns: contract.ColumnsFor(t),
			PK:      contract.PrimaryKey(t),
			AutoInc: isAutoInc(t),
		})
	}
	return out
}

// Report 搬迁报告：目标库各表行数 + 内容哈希 + 双向是否等值。
type Report struct {
	Counts  map[string]int
	Hashes  map[string]string
	Matched bool
}

// Migrate 主库 → 知识库（W3-3 正向搬迁）。
// 参数：main/mainD=主库连接与方言；knowledge/knowD=独立知识库连接与方言。
func Migrate(ctx context.Context, main *sql.DB, mainD db.Dialect, knowledge *sql.DB, knowD db.Dialect) (Report, error) {
	return copyFourTables(ctx, main, mainD, knowledge, knowD, MigrationTables())
}

// copyFourTables 真 SQL 搬迁内核（正反两向共用；方向由 src/dst 参数决定）。
func copyFourTables(ctx context.Context, src *sql.DB, srcD db.Dialect, dst *sql.DB, dstD db.Dialect, specs []tableSpec) (Report, error) {
	if src == nil || dst == nil {
		return Report{}, fmt.Errorf("knowledge: 搬迁收到 nil 连接（主库/知识库都必须真连上）")
	}
	for _, sp := range specs {
		if err := copyTable(ctx, src, srcD, dst, dstD, sp); err != nil {
			return Report{}, fmt.Errorf("knowledge: 表 %s 搬迁失败: %w", sp.Table, err)
		}
	}
	// PG 序列复位：显式 id 插进去后必须把序列推到 max(id)，否则下一次 INSERT 撞唯一键。
	if dstD == db.DialectPostgres {
		for _, sp := range specs {
			if !sp.AutoInc {
				continue
			}
			if err := resetSequence(ctx, dst, sp.Table); err != nil {
				return Report{}, fmt.Errorf("knowledge: 表 %s 序列复位失败: %w", sp.Table, err)
			}
		}
	}
	rep, err := fingerprintBoth(src, srcD, dst, dstD)
	if err != nil {
		return Report{}, err
	}
	if !rep.Matched {
		return rep, fmt.Errorf("knowledge: 双向指纹不一致（行数/逐表内容哈希），拒绝宣称搬迁成功")
	}
	return rep, nil
}

// copyTable 单表显式 id 幂等 upsert（真 SQL，无桩、无静默吞错）。
func copyTable(ctx context.Context, src *sql.DB, srcD db.Dialect, dst *sql.DB, dstD db.Dialect, sp tableSpec) error {
	cols := sp.Columns
	if len(cols) == 0 {
		return fmt.Errorf("表 %s 无列清单", sp.Table)
	}
	q := "SELECT " + strings.Join(cols, ",") + " FROM " + sp.Table + " ORDER BY " + sp.PK
	rows, err := db.Query(src, srcD, q)
	if err != nil {
		return fmt.Errorf("读源表失败: %w", err)
	}
	defer rows.Close()

	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	setClauses := make([]string, 0, len(cols))
	for _, c := range cols {
		if c != sp.PK {
			setClauses = append(setClauses, c+"=excluded."+c)
		}
	}
	if len(setClauses) == 0 {
		return fmt.Errorf("表 %s 除主键外无列可更新", sp.Table)
	}
	upsert := "INSERT INTO " + sp.Table + " (" + strings.Join(cols, ",") + ") VALUES (" +
		strings.Join(placeholders, ",") + ") ON CONFLICT(" + sp.PK + ") DO UPDATE SET " +
		strings.Join(setClauses, ",")

	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("源表行扫描失败: %w", err)
		}
		// ★ 显式 id 落在 vals[0]（列清单主键优先）；按主键 upsert ⇒ 跑两遍行数不变。
		if _, err := db.Exec(dst, dstD, upsert, vals...); err != nil {
			return fmt.Errorf("目标表幂等 upsert 失败: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("源表行迭代失败: %w", err)
	}
	return nil
}

// resetSequence PG 序列复位（setval(pg_get_serial_sequence(...))）。
// 第三参 is_called：表有行 ⇒ true（next=max+1）；空表 ⇒ false（next=1）。
func resetSequence(ctx context.Context, conn *sql.DB, table string) error {
	q := "SELECT setval(pg_get_serial_sequence('" + table + "','id'), " +
		"GREATEST(COALESCE((SELECT MAX(id) FROM " + table + "),0),1), " +
		"(SELECT COUNT(*)>0 FROM " + table + "))"
	if _, err := db.Exec(conn, db.DialectPostgres, q); err != nil {
		return err
	}
	return nil
}

// fingerprintBoth 主库/知识库双向指纹（行数 + 每表内容哈希）。
func fingerprintBoth(src *sql.DB, srcD db.Dialect, dst *sql.DB, dstD db.Dialect) (Report, error) {
	rep := Report{Counts: map[string]int{}, Hashes: map[string]string{}, Matched: true}
	for _, t := range contract.KnowledgeTableNames {
		c1, h1, err := fingerprint(src, srcD, t)
		if err != nil {
			return rep, fmt.Errorf("knowledge: 源库 %s 指纹失败: %w", t, err)
		}
		c2, h2, err := fingerprint(dst, dstD, t)
		if err != nil {
			return rep, fmt.Errorf("knowledge: 目标库 %s 指纹失败: %w", t, err)
		}
		if c1 != c2 || h1 != h2 {
			rep.Matched = false
		}
		rep.Counts[t] = c2
		rep.Hashes[t] = h2
	}
	return rep, nil
}

// fingerprint 单表指纹：行数 + 「COALESCE 拼接哈希」。
// ★ 用 contract 全量列清单（独立于搬迁时的列切片），按主键排序逐行拼接，
//   nil 按 "" 处理（COALESCE 语义），不依赖 SQL 的 ||（PG 下 int||text 会报类型错）。
func fingerprint(conn *sql.DB, d db.Dialect, table string) (int, string, error) {
	cols := contract.ColumnsFor(table)
	q := "SELECT " + strings.Join(cols, ",") + " FROM " + table + " ORDER BY " + contract.PrimaryKey(table)
	rows, err := db.Query(conn, d, q)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	h := sha256.New()
	n := 0
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return 0, "", err
		}
		var b strings.Builder
		for i, v := range vals {
			if i > 0 {
				b.WriteByte(0x1f) // 列分隔
			}
			b.WriteString(scalarKey(v))
		}
		h.Write([]byte(b.String()))
		h.Write([]byte{0x1e}) // 行分隔
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// scalarKey 标量归一（双方言一致：PG/SQLite 的 TEXT/INTEGER 都映到同一字符串）。
func scalarKey(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return "" // COALESCE
	case []byte:
		return string(x)
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(v)
	}
}
