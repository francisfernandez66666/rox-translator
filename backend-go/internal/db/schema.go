package db

// RegisteredMigrations 返回平台核心表的双方言迁移定义。
//
// 这是 P0-3「SQLite→PostgreSQL 表结构迁移」的基线模板：后续按相同模式
// 逐表补齐其余约 37 张表，并在 legacy store.migrate() 移除对应建表后由本
// Runner 接管。当前仅作为模板与单元测试使用，尚未在 main 中激活
// （避免与 legacy 建表重复；两者均用 IF NOT EXISTS，即使同时执行也幂等）。
//
// 编写约定：
//   - SQLite 端布尔/时间用 INTEGER/TEXT，默认值沿用原 store.go；
//   - PostgreSQL 端自增主键用 BIGSERIAL，时间列用 TIMESTAMPTZ，布尔列用 BOOLEAN；
//   - 凡涉及 INSERT 的迁移，占位符与冲突子句统一经 Dialect 助手生成（见 dialect.go）。
func RegisteredMigrations() []Migration {
	return []Migration{
		{
			ID: "0001_users",
			SQLiteUp: `CREATE TABLE IF NOT EXISTS users (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				tenant_id INTEGER NOT NULL DEFAULT 1,
				username TEXT NOT NULL,
				password_hash TEXT NOT NULL,
				display_name TEXT NOT NULL DEFAULT '',
				role TEXT NOT NULL DEFAULT 'user',
				status TEXT NOT NULL DEFAULT 'active',
				created_by INTEGER NOT NULL DEFAULT 0,
				last_login_at TEXT NOT NULL DEFAULT '',
				created_at TEXT,
				updated_at TEXT,
				UNIQUE(username, tenant_id)
			)`,
			PostgresUp: `CREATE TABLE IF NOT EXISTS users (
				id BIGSERIAL PRIMARY KEY,
				tenant_id INTEGER NOT NULL DEFAULT 1,
				username TEXT NOT NULL,
				password_hash TEXT NOT NULL,
				display_name TEXT NOT NULL DEFAULT '',
				role TEXT NOT NULL DEFAULT 'user',
				status TEXT NOT NULL DEFAULT 'active',
				created_by INTEGER NOT NULL DEFAULT 0,
				last_login_at TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ,
				updated_at TIMESTAMPTZ,
				UNIQUE(username, tenant_id)
			)`,
		},
		{
			ID: "0002_tickets",
			SQLiteUp: `CREATE TABLE IF NOT EXISTS tickets (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				tenant_id INTEGER NOT NULL DEFAULT 1,
				ticket_no TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'draft',
				source_text TEXT NOT NULL DEFAULT '',
				file_path TEXT NOT NULL DEFAULT '',
				target_langs TEXT NOT NULL DEFAULT '',
				created_by INTEGER NOT NULL DEFAULT 0,
				approver_id INTEGER NOT NULL DEFAULT 0,
				reviewer_id INTEGER NOT NULL DEFAULT 0,
				reject_reason TEXT NOT NULL DEFAULT '',
				final_result TEXT NOT NULL DEFAULT '',
				created_at TEXT,
				updated_at TEXT
			)`,
			PostgresUp: `CREATE TABLE IF NOT EXISTS tickets (
				id BIGSERIAL PRIMARY KEY,
				tenant_id INTEGER NOT NULL DEFAULT 1,
				ticket_no TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'draft',
				source_text TEXT NOT NULL DEFAULT '',
				file_path TEXT NOT NULL DEFAULT '',
				target_langs TEXT NOT NULL DEFAULT '',
				created_by INTEGER NOT NULL DEFAULT 0,
				approver_id INTEGER NOT NULL DEFAULT 0,
				reviewer_id INTEGER NOT NULL DEFAULT 0,
				reject_reason TEXT NOT NULL DEFAULT '',
				final_result TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ,
				updated_at TIMESTAMPTZ
			)`,
		},
	}
}
