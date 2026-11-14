// ============ assist_session_migrate.go · 职责说明 ============
// 四期 W5（§4.5.2 步骤 2）：把 assist 库的 sessions_base / messages_base
// **跨库搬进主库**（与步骤 6 的四张 KB 表搬知识库方向不同，别混成一步）。
//
// ★ 不是「把 DSN 指过去」：assist 是独立 SQLite 单文件库、主库可能是 SQLite 或 PG，
//
//	两处各持一条 *sql.DB，本文件从 src 逐行 SELECT、向 dst 显式 id 幂等 upsert。
//
// 三条硬口径：
//  1. DDL 与补列只经 db.ExecDDL / db.EnsureColumns（DDL 唯一入口，AGENTS §一·1），
//     双方言：SQLite 语句为唯一真源，PG 下由 db.ToDialect 翻译 AUTOINCREMENT→BIGSERIAL，
//     DATETIME→TIMESTAMP 在本文件按方言补齐（db.ToDialect 不管 DATETIME）。
//  2. 显式 id 幂等 upsert：按主键冲突更新全部非主键列，重跑行数与内容不变；
//     「造旧库跑两遍」的行数 + 内容哈希自证在 api/assist_session_migrate_gate_test.go（A2）。
//  3. PG 侧 setval(pg_get_serial_sequence(...)) 复位 messages_base 序列——显式 id
//     插进去但不复位，下一行 INSERT 会撞唯一键，而那个错误在搬迁当天不出现、
//     在第一个新消息落库时才出现（§8.1 第 7 条）。SQLite AUTOINCREMENT 自己按 max(id) 续。
//
// 失败一律返回 error，绝不静默降级（半搬完的会话表比不搬更坏）。
// ================================================
package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"translator/internal/db"
)

// chatTableSpec 一张会话/消息表的迁移规格。cols 的顺序既是 SELECT 列序，
// 也是 INSERT 列序——逐列等值靠「同一份清单」保证，不靠两处手写对齐。
type chatTableSpec struct {
	name       string
	createDDL  string
	indexDDL   []string
	ensureCols map[string]string
	cols       []string
	serial     bool // 是否有自增主键（messages_base 需要 PG setval 复位）
}

// chatMigrateSpecs 与 internal/assist/store/store.go:107-129 的现名逐列对齐，
// 补列（expires_at / anonym_hash）直接写进 CREATE TABLE，同时仍走 EnsureColumns
// 供搬迁过来的老库幂等补列（AGENTS §一·1 规则 4）。
var chatMigrateSpecs = []chatTableSpec{
	{
		name: "sessions_base",
		createDDL: `CREATE TABLE IF NOT EXISTS sessions_base(
			id TEXT PRIMARY KEY,
			page_url TEXT DEFAULT '',
			in_flow TEXT DEFAULT '',
			flow_step INTEGER DEFAULT 0,
			msg_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			tenant_id INTEGER DEFAULT NULL,
			user_id INTEGER DEFAULT NULL,
			anonym_hash TEXT DEFAULT '',
			expires_at DATETIME DEFAULT NULL
		)`,
		indexDDL: []string{
			"CREATE INDEX IF NOT EXISTS idx_sessions_anon_expires ON sessions_base(anonym_hash, expires_at)",
		},
		ensureCols: map[string]string{
			"expires_at":  "DATETIME DEFAULT NULL",
			"anonym_hash": "TEXT DEFAULT ''",
		},
		cols: []string{
			"id", "page_url", "in_flow", "flow_step", "msg_count",
			"created_at", "last_at", "tenant_id", "user_id", "anonym_hash", "expires_at",
		},
		serial: false, // id 是 TEXT 主键，无序列
	},
	{
		name: "messages_base",
		createDDL: `CREATE TABLE IF NOT EXISTS messages_base(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			actions TEXT DEFAULT '[]',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			tenant_id INTEGER DEFAULT NULL,
			user_id INTEGER DEFAULT NULL,
			expires_at DATETIME DEFAULT NULL,
			anonym_hash TEXT DEFAULT ''
		)`,
		indexDDL: []string{
			"CREATE INDEX IF NOT EXISTS idx_messages_session ON messages_base(session_id, id)",
			"CREATE INDEX IF NOT EXISTS idx_msgs_auth_expire ON messages_base(tenant_id, user_id, expires_at)",
		},
		ensureCols: map[string]string{
			"expires_at":  "DATETIME DEFAULT NULL",
			"anonym_hash": "TEXT DEFAULT ''",
		},
		cols: []string{
			"id", "session_id", "role", "content", "actions",
			"created_at", "tenant_id", "user_id", "expires_at", "anonym_hash",
		},
		serial: true,
	},
}

// pgSafeDDL 把 SQLite 真源里的 DATETIME 翻成 PG 的 TIMESTAMP。
// db.ToDialect 只覆盖 AUTOINCREMENT/BLOB/REAL/INSERT OR IGNORE，不认 DATETIME；
// 不补这一刀，PG 侧 CREATE TABLE 会在搬迁第一张表就报 "type datetime does not exist"。
func pgSafeDDL(sqlText string, d db.Dialect) string {
	if d.IsPostgres() {
		return strings.ReplaceAll(sqlText, "DATETIME", "TIMESTAMP")
	}
	return sqlText
}

// MigrateAssistSessionsToMain 把 assist 库的两张会话/消息表搬进主库。
// mainDB=主库句柄（s.Store.DB()）；assistDB=assist 独立库句柄；d=目标库方言。
// 可重复调用（幂等）；任何一步失败返回 error，已提交的事务不回滚（下一次重跑补齐）。
func MigrateAssistSessionsToMain(ctx context.Context, mainDB, assistDB *sql.DB, d db.Dialect) error {
	if mainDB == nil || assistDB == nil {
		return fmt.Errorf("assist 会话迁移：主库或 assist 库句柄为空")
	}
	for _, spec := range chatMigrateSpecs {
		if err := db.ExecDDL(mainDB, d, pgSafeDDL(spec.createDDL, d)); err != nil {
			return fmt.Errorf("assist 会话迁移：建 %s 失败: %w", spec.name, err)
		}
		if err := db.EnsureColumns(mainDB, d, spec.name, pgSafeCols(spec.ensureCols, d)); err != nil {
			return fmt.Errorf("assist 会话迁移：补 %s 列失败: %w", spec.name, err)
		}
		for _, idx := range spec.indexDDL {
			if err := db.ExecDDL(mainDB, d, idx); err != nil {
				return fmt.Errorf("assist 会话迁移：建 %s 索引失败: %w", spec.name, err)
			}
		}
		if err := copyChatRows(ctx, mainDB, assistDB, d, spec); err != nil {
			return err
		}
		if spec.serial && d.IsPostgres() {
			if err := resetChatSerial(ctx, mainDB, d, spec.name); err != nil {
				return err
			}
		}
	}
	return nil
}

// pgSafeCols 补列定义同样按方言翻 DATETIME（见 pgSafeDDL）。
func pgSafeCols(in map[string]string, d db.Dialect) map[string]string {
	if !d.IsPostgres() {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = strings.ReplaceAll(v, "DATETIME", "TIMESTAMP")
	}
	return out
}

// copyChatRows 从 assist 库逐行读出、向主库显式 id 幂等 upsert。
//
// ★ 先把行全部读进内存、关掉游标，再对目标库开事务：单连接池（SQLite）下
//
//	「边 range rows 边 Begin」会当场死锁（见 assist/store/store.go 的
//	migrateSessionsToBase 头注释，现网 600s 超时实证）。
func copyChatRows(ctx context.Context, dst, src *sql.DB, d db.Dialect, spec chatTableSpec) error {
	selectSQL := "SELECT " + strings.Join(spec.cols, ",") + " FROM " + spec.name
	rows, err := db.Query(src, db.DialectSQLite, selectSQL)
	if err != nil {
		return fmt.Errorf("assist 会话迁移：读 %s 失败: %w", spec.name, err)
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
			return fmt.Errorf("assist 会话迁移：扫描 %s 行失败: %w", spec.name, serr)
		}
		pending = append(pending, vals)
	}
	if rerr := rows.Err(); rerr != nil {
		_ = rows.Close()
		return fmt.Errorf("assist 会话迁移：遍历 %s 失败: %w", spec.name, rerr)
	}
	_ = rows.Close() // 释放源库唯一一条连接，下面才允许对目标库开事务

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
		return fmt.Errorf("assist 会话迁移：%s 开事务失败: %w", spec.name, err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := db.Prepare(tx, d, insertSQL)
	if err != nil {
		return fmt.Errorf("assist 会话迁移：%s 预备 upsert 失败: %w", spec.name, err)
	}
	defer stmt.Close()
	for _, vals := range pending {
		if _, ierr := stmt.Exec(vals...); ierr != nil {
			return fmt.Errorf("assist 会话迁移：%s 写入 id=%v 失败: %w", spec.name, vals[0], ierr)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("assist 会话迁移：%s 提交失败: %w", spec.name, err)
	}
	return nil
}

// resetChatSerial PG 侧复位 messages_base 的 id 序列（显式 id 插入后的必需一步）。
// 目标表 id 不是 serial（例如更老的库形态）时 pg_get_serial_sequence 返回 NULL，
// 此时无序列可复位，按「无需动作」返回 nil，不把一次正常搬迁判成失败。
func resetChatSerial(ctx context.Context, conn *sql.DB, d db.Dialect, table string) error {
	var seqName sql.NullString
	if err := db.QueryRow(conn, d,
		"SELECT pg_get_serial_sequence('"+table+"','id')").Scan(&seqName); err != nil {
		return fmt.Errorf("assist 会话迁移：查 %s 序列失败: %w", table, err)
	}
	if !seqName.Valid || strings.TrimSpace(seqName.String) == "" {
		return nil
	}
	// GREATEST(...,1) 保证空表也能把序列落到 1，下一行 INSERT 不撞主键。
	q := "SELECT setval(?, GREATEST(COALESCE((SELECT MAX(id) FROM " + table + "),0),1), true)"
	if _, err := db.Exec(conn, d, q, seqName.String); err != nil {
		return fmt.Errorf("assist 会话迁移：%s setval 复位失败: %w", table, err)
	}
	return nil
}
