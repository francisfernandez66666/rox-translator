// ============ kb_to_knowledge_migrate.go · 职责说明 ============
// 四期 W5（§4.5.2 步骤 6）：把四张 KB 表（kb_entries / scripts / flows /
// feature_links）从 **assist 库跨库拷贝**进知识库。
//
// ★ 这一步不是「把 DSN 指过去」：assist 是独立 SQLite 单文件库、knowledge 是
//
//	独立 DSN（§4.3.1 第 1 条，从 W3 第一天就是），两者各持一条 *sql.DB。
//	写成「改连接串」会让四张表在现网根本没搬，而 entries/query 回落空集
//	（KN4 腿④）看上去只是「知识库里没这条」。本文件是真的 SELECT→INSERT。
//
// ★ 与步骤 2（会话/消息 → 主库）方向不同、别混成一步：那边归
//
//	api/assist_session_migrate.go，那边拥有 sessions_base/messages_base；
//	本文件只碰四张 KB 表，逐列等值、显式 id、按主键幂等 upsert。
//
// 契约引用（按名字，不硬 import 尚未落地的包）：
//
//	目标库由 W3 的 knowledge.KnowledgeMigrate() 契约建好四张表后的独立 DSN 提供，
//	组合根把它作为参数传进 (d *DB).MigrateKBToKnowledge（接线见 /tmp/w5_wiring.md）。
//	本包刻意不对 translator/internal/knowledge 产生编译期依赖——那个包由另一个
//	agent 并行施工，本文件只按契约名引用，避免两边同时落地时互相卡住编译。
//	常量 KnowledgeMigrateContract 就是这个契约名在搬迁侧的唯一留痕。
//
// 回滚口径（§4.5.2 步骤 6 末 / §8.3）：知识库里这四张表**不删**，回退 assist 旧件
// 即照常工作。因此本文件只做「拷进去」，删源表 / entries/query 四条真件 /
// 切读切写属于组合根与面检在验收后的动作，不在这里一笔做完。
//
// DDL 唯一入口 db.ExecDDL / db.EnsureColumns（AGENTS §一·1）；双方言；
// 失败一律返回 error，绝不 TODO、绝不静默降级。
// ================================================
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"translator/internal/db"
)

// KnowledgeMigrateContract W3 知识服务的迁移契约名（按名字引用，不创建 knowledge 包文件）。
// 组合根：knowledge.KnowledgeMigrate() 建表 → 取它的独立 DSN 句柄 → 传给本文件。
const KnowledgeMigrateContract = "knowledge.KnowledgeMigrate"

// Conn 导出本库底层句柄，供组合根做跨库搬迁 / 双 DSN 不等断言（KN12）。
// 只回句柄，不新增任何业务读写方法（AGENTS §一·1 store 冻结口径）。
func (d *DB) Conn() *sql.DB { return d.sql }

// kbTableSpec 一张 KB 表的搬迁规格。cols 顺序同时是 SELECT 与 INSERT 列序，
// 逐列等值靠「同一份清单」保证，不靠两处手写对齐。
type kbTableSpec struct {
	name       string
	createDDL  string
	ensureCols map[string]string
	cols       []string
}

// kbMigrateSpecs 与 store.go:60-104 的四条 CREATE TABLE 逐列对齐，
// lang 列直接写进 CREATE TABLE（§4.3.3 口径：直接带 lang，同时保留 EnsureColumns
// 供搬过来的旧库用）；feature_links 无 updated_at / 无 lang，照原形态不补。
var kbMigrateSpecs = []kbTableSpec{
	{
		name: "kb_entries",
		createDDL: `CREATE TABLE IF NOT EXISTS kb_entries(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE,
			category TEXT DEFAULT 'usage',
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			keywords TEXT DEFAULT '',
			link_keys TEXT DEFAULT '',
			priority INTEGER DEFAULT 5,
			enabled INTEGER DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			lang TEXT NOT NULL DEFAULT 'zh'
		)`,
		ensureCols: map[string]string{"lang": "TEXT NOT NULL DEFAULT 'zh'"},
		cols: []string{
			"id", "key", "category", "title", "content", "keywords",
			"link_keys", "priority", "enabled", "updated_at", "lang",
		},
	},
	{
		name: "scripts",
		createDDL: `CREATE TABLE IF NOT EXISTS scripts(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE,
			stype TEXT DEFAULT 'keyword',
			title TEXT DEFAULT '',
			keywords TEXT DEFAULT '',
			link_keys TEXT DEFAULT '',
			content TEXT NOT NULL,
			priority INTEGER DEFAULT 5,
			enabled INTEGER DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			lang TEXT NOT NULL DEFAULT 'zh'
		)`,
		ensureCols: map[string]string{"lang": "TEXT NOT NULL DEFAULT 'zh'"},
		cols: []string{
			"id", "key", "stype", "title", "keywords", "link_keys",
			"content", "priority", "enabled", "updated_at", "lang",
		},
	},
	{
		name: "flows",
		createDDL: `CREATE TABLE IF NOT EXISTS flows(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			trigger_keywords TEXT DEFAULT '',
			steps_json TEXT DEFAULT '[]',
			enabled INTEGER DEFAULT 1,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			lang TEXT NOT NULL DEFAULT 'zh'
		)`,
		ensureCols: map[string]string{"lang": "TEXT NOT NULL DEFAULT 'zh'"},
		cols: []string{
			"id", "key", "name", "description", "trigger_keywords",
			"steps_json", "enabled", "updated_at", "lang",
		},
	},
	{
		name: "feature_links",
		createDDL: `CREATE TABLE IF NOT EXISTS feature_links(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			url TEXT NOT NULL,
			ftype TEXT DEFAULT 'route',
			icon TEXT DEFAULT '',
			sort INTEGER DEFAULT 50,
			enabled INTEGER DEFAULT 1
		)`,
		ensureCols: map[string]string{},
		cols: []string{
			"id", "key", "name", "description", "url", "ftype", "icon", "sort", "enabled",
		},
	},
}

// kbPgSafeDDL 把 SQLite 真源的 DATETIME 翻成 PG 的 TIMESTAMP（db.ToDialect 不认 DATETIME）。
func kbPgSafeDDL(sqlText string, d db.Dialect) string {
	if d.IsPostgres() {
		return strings.ReplaceAll(sqlText, "DATETIME", "TIMESTAMP")
	}
	return sqlText
}

// MigrateKBToKnowledge 把四张 KB 表从 assist 库跨库拷贝进知识库。
// knowledgeDB=knowledge.KnowledgeMigrate() 建表后的独立 DSN 句柄；d=知识库方言。
// 可重复调用（幂等）：行数、内容、显式 id 落位跑两遍不变（A5 锁自证）。
// 任何一步失败返回 error；不删源表（回滚口径见文件头）。
func (d *DB) MigrateKBToKnowledge(ctx context.Context, knowledgeDB *sql.DB, dialect db.Dialect) error {
	if d == nil || d.sql == nil || knowledgeDB == nil {
		return fmt.Errorf("KB 搬迁：assist 库或知识库句柄为空")
	}
	for _, spec := range kbMigrateSpecs {
		if err := db.ExecDDL(knowledgeDB, dialect, kbPgSafeDDL(spec.createDDL, dialect)); err != nil {
			return fmt.Errorf("KB 搬迁：建 %s 失败: %w", spec.name, err)
		}
		if len(spec.ensureCols) > 0 {
			cols := spec.ensureCols
			if dialect.IsPostgres() {
				cols = map[string]string{}
				for k, v := range spec.ensureCols {
					cols[k] = strings.ReplaceAll(v, "DATETIME", "TIMESTAMP")
				}
			}
			if err := db.EnsureColumns(knowledgeDB, dialect, spec.name, cols); err != nil {
				return fmt.Errorf("KB 搬迁：补 %s 列失败: %w", spec.name, err)
			}
		}
		if err := copyKBRows(ctx, knowledgeDB, d.sql, dialect, spec); err != nil {
			return err
		}
		if dialect.IsPostgres() {
			if err := resetKBSerial(ctx, knowledgeDB, dialect, spec.name); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyKBRows 从 assist 库逐行读出、向知识库显式 id 幂等 upsert。
// 先收进内存再关游标，再对目标库开事务——单连接池下边 range 边 Begin 会死锁
// （同 store.go 的 migrateSessionsToBase 头注释）。
func copyKBRows(ctx context.Context, dst, src *sql.DB, d db.Dialect, spec kbTableSpec) error {
	selectSQL := "SELECT " + strings.Join(spec.cols, ",") + " FROM " + spec.name
	rows, err := db.Query(src, db.DialectSQLite, selectSQL)
	if err != nil {
		return fmt.Errorf("KB 搬迁：读 %s 失败: %w", spec.name, err)
	}
	pending := make([][]any, 0, 16)
	for rows.Next() {
		vals := make([]any, len(spec.cols))
		ptrs := make([]any, len(spec.cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if serr := rows.Scan(ptrs...); serr != nil {
			_ = rows.Close()
			return fmt.Errorf("KB 搬迁：扫描 %s 行失败: %w", spec.name, serr)
		}
		pending = append(pending, vals)
	}
	if rerr := rows.Err(); rerr != nil {
		_ = rows.Close()
		return fmt.Errorf("KB 搬迁：遍历 %s 失败: %w", spec.name, rerr)
	}
	_ = rows.Close()

	ph := make([]string, len(spec.cols))
	for i := range ph {
		ph[i] = "?"
	}
	colList := strings.Join(spec.cols, ",")
	valList := strings.Join(ph, ",")
	insertSQL := "INSERT INTO " + spec.name + "(" + colList + ") VALUES(" + valList + ")"
	if d.IsPostgres() {
		insertSQL += " " + d.UpsertUpdate([]string{"id"}, spec.cols[1:])
	} else {
		insertSQL = "INSERT OR REPLACE INTO " + spec.name + "(" + colList + ") VALUES(" + valList + ")"
	}

	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("KB 搬迁：%s 开事务失败: %w", spec.name, err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := db.Prepare(tx, d, insertSQL)
	if err != nil {
		return fmt.Errorf("KB 搬迁：%s 预备 upsert 失败: %w", spec.name, err)
	}
	defer stmt.Close()
	for _, vals := range pending {
		if _, ierr := stmt.Exec(vals...); ierr != nil {
			return fmt.Errorf("KB 搬迁：%s 写入 id=%v 失败: %w", spec.name, vals[0], ierr)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("KB 搬迁：%s 提交失败: %w", spec.name, err)
	}
	return nil
}

// resetKBSerial PG 侧复位四张表的 id 序列（显式 id 插入后的必需一步，§8.1 第 7 条）。
// 非 serial 形态（pg_get_serial_sequence 返回 NULL）按「无需动作」返回 nil。
func resetKBSerial(ctx context.Context, conn *sql.DB, d db.Dialect, table string) error {
	var seqName sql.NullString
	if err := db.QueryRow(conn, d,
		"SELECT pg_get_serial_sequence('"+table+"','id')").Scan(&seqName); err != nil {
		return fmt.Errorf("KB 搬迁：查 %s 序列失败: %w", table, err)
	}
	if !seqName.Valid || strings.TrimSpace(seqName.String) == "" {
		return nil
	}
	q := "SELECT setval(?, GREATEST(COALESCE((SELECT MAX(id) FROM " + table + "),0),1), true)"
	if _, err := db.Exec(conn, d, q, seqName.String); err != nil {
		return fmt.Errorf("KB 搬迁：%s setval 复位失败: %w", table, err)
	}
	return nil
}
