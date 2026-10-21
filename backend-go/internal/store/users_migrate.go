// ============ users_migrate.go · 用户标识约束迁移 ============
// 本文件只放 users 表的「标识语义迁移」，不承接业务读写：
//   - 用户名去唯一：登录标识收敛到邮箱后，同租户重名必须合法；
//   - 邮箱按 lower(trim(email)) 唯一：只有仍占用的 active/deactivating 行互斥；
//   - 新库与存量库走同一份列清单/索引清单，禁止一边建表干净、另一边靠补列长出来。
// ★ 2026-10-21 IAM 重做新增。放在独立文件是为了守住 AGENTS §一·1
// 「store.go 不承接业务方法」与「billing/kbpackages 只减不增」两条冻结规则。
// ================================================================

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"translator/internal/db"
)

// migrateUsernameNotUnique 摘掉历史用户名唯一约束，并补一条非唯一检索索引（幂等）。
//
// PostgreSQL：表级 UNIQUE(username, tenant_id) 在库里的约束名由 PG 自动生成，
// 兼容早期手工命名，逐一 DROP CONSTRAINT IF EXISTS；再 DROP 旧唯一索引。
// SQLite：存量文件库的表级 UNIQUE 只能重建表才去掉。这里用「动态列清单 + 索引/触发器快照」
// 做 12-step 重建：只摘掉与 username 相关的唯一索引，其余列、行、索引、触发器原样搬过去，
// 避免拿一份写死的列清单去赌用户库的版本（少一列就是整列数据丢失）。
func (s *Store) migrateUsernameNotUnique() error {
	d := db.CurrentDialect()
	if d.IsPostgres() {
		for _, q := range []string{
			`ALTER TABLE users DROP CONSTRAINT IF EXISTS users_username_tenant_id_key`,
			`ALTER TABLE users DROP CONSTRAINT IF EXISTS users_tenant_id_username_key`,
			`DROP INDEX IF EXISTS idx_users_tid_username`,
		} {
			if _, err := db.Exec(s.db, d, q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		if err := s.pgDropUsernameUniqueVariants(); err != nil {
			return err
		}
		// 摘掉唯一语义后补回「同租户按用户名查找」的普通索引：唯一约束同时承担了查询加速，
		// 只删不补会让后台按租户列用户退化成顺序扫描。
		if _, err := db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_users_tenant_username ON users(tenant_id, username)`); err != nil {
			return err
		}
	} else {
		if _, err := db.Exec(s.db, d, `DROP INDEX IF EXISTS idx_users_tid_username`); err != nil {
			return err
		}
		needRebuild, err := s.sqliteUsersNeedRebuild()
		if err != nil {
			return err
		}
		if needRebuild {
			if rerr := s.rebuildUsersTableSQLite(); rerr != nil {
				return fmt.Errorf("重建 users 表以去掉用户名唯一约束失败: %w", rerr)
			}
		}
		// 重建后补 column-additions 阶段建过的联合索引（重建会连索引一起换掉）。
		// 极小旧库/手工夹具可能还没有 org_id：缺列时跳过，不把"可选索引没建成"升级成迁移失败。
		if s.sqliteHasColumn("users", "org_id") {
			if _, err := db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_users_tenant_org ON users(tenant_id, org_id)`); err != nil {
				return err
			}
		}
	}
	// 用户名仍是后台/邀请/SCIM 的检索面，但**不唯一**：普通索引足够，不能再建 UNIQUE。
	_, err := db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_users_tenant_username ON users(tenant_id, username)`)
	return err
}

// hasUsernameUnique 是迁移判据的唯一形状检查：表级 UNIQUE 在 SQLite 的建表 SQL 里
// 既可能写成 `UNIQUE(username, tenant_id)` 也可能带空格，故统一去空白后小写比对。
func hasUsernameUnique(ddl string) bool {
	repl := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", `"`, "", "`", "", "[", "", "]", "")
	compact := repl.Replace(strings.ToLower(ddl))
	if strings.Contains(compact, "unique(username,tenant_id)") ||
		strings.Contains(compact, "unique(tenant_id,username)") {
		return true
	}
	// 独立唯一索引的形态是 `CREATE UNIQUE INDEX … ON users(username, tenant_id)`：
	// UNIQUE 与列清单之间隔着索引名/ON users，不能要求两者相邻。
	return strings.Contains(compact, "uniqueindex") &&
		(strings.Contains(compact, "(username,tenant_id)") || strings.Contains(compact, "(tenant_id,username)"))
}

// sqliteUsersNeedRebuild 同时问两个面：表级 UNIQUE 与历史手工唯一索引。
// 只看建表 SQL 会漏掉「约束不在表里、而在另建 UNIQUE INDEX」的旧库形态。
func (s *Store) sqliteUsersNeedRebuild() (bool, error) {
	var ddl string
	err := db.QueryRow(s.db, db.DialectSQLite,
		`SELECT COALESCE(sql,'') FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&ddl)
	if err != nil {
		return false, err
	}
	if hasUsernameUnique(ddl) {
		return true, nil
	}
	rows, err := db.Query(s.db, db.DialectSQLite,
		`SELECT COALESCE(sql,'') FROM sqlite_master
		 WHERE tbl_name='users' AND type='index' AND sql IS NOT NULL`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var idxSQL string
		if err := rows.Scan(&idxSQL); err != nil {
			return false, err
		}
		if hasUsernameUnique(idxSQL) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// sqliteUserColumn 是 PRAGMA table_info(users) 的一行。
// 迁移只搬运现有列定义，不新增/删除列——列增删仍由 columnAdditions 负责。
type sqliteUserColumn struct {
	name       string
	typ        string
	notNull    int
	defaultVal sql.NullString
	pk         int
}

// sqliteUsersDependentSQL 一条索引/触发器的原始建法。
type sqliteUsersDependentSQL struct {
	kind string
	name string
	sql  string
}

// rebuildUsersTableSQLite 按 SQLite 官方 12-step 重建 users：
// 备份列定义与依赖对象 → 建无 UNIQUE 的新表 → 原列原样拷贝 → 换名 → 恢复依赖对象。
// 全程用同一条 *sql.Conn，确保 PRAGMA foreign_keys 与事务落在同一连接上。
func (s *Store) rebuildUsersTableSQLite() (err error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	var fkOn int
	if qerr := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fkOn); qerr == nil && fkOn == 1 {
		if _, qerr := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); qerr != nil {
			return qerr
		}
		defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`) }()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	cols, err := sqliteUsersColumns(ctx, tx)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return fmt.Errorf("users 表不存在或无列")
	}
	var seq int64
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(seq,0) FROM sqlite_sequence WHERE name='users'`).Scan(&seq)
	deps, err := sqliteUsersDependents(ctx, tx)
	if err != nil {
		return err
	}

	const tmp = "users__iam_rebuild"
	if _, err = tx.ExecContext(ctx, `DROP TABLE IF EXISTS "`+tmp+`"`); err != nil {
		return err
	}
	defs := make([]string, 0, len(cols))
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		defs = append(defs, sqliteColumnDDL(c))
		names = append(names, quoteSQLiteIdent(c.name))
	}
	create := `CREATE TABLE "` + tmp + `" (` + strings.Join(defs, ", ") + `)`
	if _, err = tx.ExecContext(ctx, create); err != nil {
		return err
	}
	quoted := strings.Join(names, ", ")
	insert := `INSERT INTO "` + tmp + `" (` + quoted + `) SELECT ` + quoted + ` FROM "users"`
	if _, err = tx.ExecContext(ctx, insert); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DROP TABLE "users"`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE "`+tmp+`" RENAME TO "users"`); err != nil {
		return err
	}
	for _, dep := range deps {
		if _, derr := tx.ExecContext(ctx, dep.sql); derr != nil {
			return fmt.Errorf("恢复 users 依赖对象 %s(%s) 失败: %w", dep.kind, dep.name, derr)
		}
	}
	if seq > 0 {
		// 显式 id 拷贝不会把旧序列推到"已删除过的最大 id"之后；恢复它避免 id 复用。
		_, _ = tx.ExecContext(ctx, `UPDATE sqlite_sequence SET seq=? WHERE name='users'`, seq)
	}
	return tx.Commit()
}

// sqliteUsersColumns 读 PRAGMA table_info(users)，保持原列顺序。
func sqliteUsersColumns(ctx context.Context, tx *sql.Tx) ([]sqliteUserColumn, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(users)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqliteUserColumn
	for rows.Next() {
		var c sqliteUserColumn
		var cid int
		if err := rows.Scan(&cid, &c.name, &c.typ, &c.notNull, &c.defaultVal, &c.pk); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// sqliteUsersDependents 快照 users 上的显式索引与触发器（autoindex 的 sql 为 NULL，不在此列）。
// 刻意跳过三类：
//   - 旧用户名唯一索引（本次迁移的目标）；
//   - 旧原始 email 唯一索引（由 OneidMigrate 重建为 lower(trim(email))+active 部分唯一索引）；
//   - 任何同时含 UNIQUE 与 username 的索引（历史手工命名变体）。
func sqliteUsersDependents(ctx context.Context, tx *sql.Tx) ([]sqliteUsersDependentSQL, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT type, name, COALESCE(sql,'') FROM sqlite_master
		 WHERE tbl_name='users' AND type IN ('index','trigger') AND sql IS NOT NULL
		 ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqliteUsersDependentSQL
	for rows.Next() {
		var d sqliteUsersDependentSQL
		if err := rows.Scan(&d.kind, &d.name, &d.sql); err != nil {
			return nil, err
		}
		if d.sql == "" || !keepUsersDependent(d) {
			continue
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// keepUsersDependent 判定一条依赖对象能否原样恢复。
func keepUsersDependent(d sqliteUsersDependentSQL) bool {
	switch d.name {
	case "idx_users_tid_username", "idx_users_email":
		return false
	}
	compact := strings.ToLower(strings.Join(strings.Fields(d.sql), ""))
	if strings.Contains(compact, "unique") && strings.Contains(compact, "username") {
		return false
	}
	return true
}

// sqliteColumnDDL 把 PRAGMA 列信息还原成建表片段。
// id 固定写回 INTEGER PRIMARY KEY AUTOINCREMENT：SQLite 的 rowid 自增语义不能靠
// PRAGMA 还原（它只回 pk=1，不回 AUTOINCREMENT），漏掉会让新行复用已删除的 id。
func sqliteColumnDDL(c sqliteUserColumn) string {
	if strings.EqualFold(c.name, "id") && c.pk > 0 {
		return `"id" INTEGER PRIMARY KEY AUTOINCREMENT`
	}
	var b strings.Builder
	b.WriteString(quoteSQLiteIdent(c.name))
	if c.typ != "" {
		b.WriteString(" ")
		b.WriteString(c.typ)
	}
	if c.pk > 0 {
		b.WriteString(" PRIMARY KEY")
	}
	if c.notNull == 1 && c.pk == 0 {
		b.WriteString(" NOT NULL")
	}
	if c.defaultVal.Valid {
		b.WriteString(" DEFAULT ")
		b.WriteString(c.defaultVal.String)
	}
	return b.String()
}

// quoteSQLiteIdent 只用于本文件动态拼出的标识符（列名/固定表名），
// 转义双引号，避免历史脏列名把重建 SQL 拼断。
func quoteSQLiteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// sqliteHasColumn 是重建后补建可选索引前的列存在检查（只服务 SQLite 迁移路径）。
func (s *Store) sqliteHasColumn(table, column string) bool {
	rows, err := db.Query(s.db, db.DialectSQLite, `SELECT 1 FROM pragma_table_info(?) WHERE name=?`, table, column)
	if err != nil {
		return false
	}
	defer rows.Close()
	return rows.Next()
}

// pgDropUsernameUniqueVariants 兜住「约束名不是 PG 默认生成名」的历史库：
// 固定名字那三条之后，再按**列清单**从系统目录里找，避免换了命名习惯就让唯一语义留下来。
func (s *Store) pgDropUsernameUniqueVariants() error {
	d := db.DialectPostgres
	rows, err := db.Query(s.db, d,
		`SELECT conname FROM pg_constraint
		 WHERE conrelid='users'::regclass AND contype='u'
		   AND pg_get_constraintdef(oid) ~ 'UNIQUE\s*\(\s*(username\s*,\s*tenant_id|tenant_id\s*,\s*username)\s*\)'`)
	if err != nil {
		return err
	}
	var cons []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		cons = append(cons, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range cons {
		if _, err := db.Exec(s.db, d, `ALTER TABLE users DROP CONSTRAINT `+pgIdent(name)); err != nil {
			return err
		}
	}
	idxRows, err := db.Query(s.db, d,
		`SELECT indexname FROM pg_indexes
		 WHERE schemaname=current_schema() AND tablename='users'
		   AND indexdef LIKE 'CREATE UNIQUE INDEX%'
		   AND indexdef ~ '\((username\s*,\s*tenant_id|tenant_id\s*,\s*username)\)'`)
	if err != nil {
		return err
	}
	var idxs []string
	for idxRows.Next() {
		var name string
		if err := idxRows.Scan(&name); err != nil {
			idxRows.Close()
			return err
		}
		idxs = append(idxs, name)
	}
	idxRows.Close()
	if err := idxRows.Err(); err != nil {
		return err
	}
	for _, name := range idxs {
		if _, err := db.Exec(s.db, d, `DROP INDEX `+pgIdent(name)); err != nil {
			return err
		}
	}
	return nil
}

// pgIdent 用双引号包住系统目录里读出的标识符；内部双引号按 PG 规则再翻倍。
func pgIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
