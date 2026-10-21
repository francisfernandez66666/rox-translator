// ============ users_migrate_test.go · 用户名去唯一迁移 ============
// 覆盖存量 SQLite 库的 12-step 重建：表级 UNIQUE(username,tenant_id) 必须真被摘掉，
// 同时列、行、索引、触发器与自增序列一个都不能少（少一列就是整列数据丢失）。
// =================================================================

package store

import (
	"database/sql"
	"strings"
	"testing"

	"translator/internal/config"
)

func TestUsersMigrateSQLiteDropsUsernameUniquePreservingData(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	schema := []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			username TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'user',
			status TEXT NOT NULL DEFAULT 'active',
			created_by INTEGER NOT NULL DEFAULT 0,
			last_login_at TEXT NOT NULL DEFAULT '',
			org_id INTEGER NOT NULL DEFAULT 0,
			email TEXT NOT NULL DEFAULT '',
			agreed_at TEXT NOT NULL DEFAULT '',
			must_change_pwd INTEGER NOT NULL DEFAULT 0,
			token_version INTEGER NOT NULL DEFAULT 0,
			job_role TEXT NOT NULL DEFAULT '',
			preferred_lang TEXT NOT NULL DEFAULT '',
			ref_code TEXT DEFAULT '',
			referred_by INTEGER DEFAULT 0,
			scim_external_id TEXT NOT NULL DEFAULT '',
			deactivate_at TEXT NOT NULL DEFAULT '',
			profile_setup_required INTEGER NOT NULL DEFAULT 0,
			created_at TEXT,
			updated_at TEXT,
			UNIQUE("username", "tenant_id")
		)`,
		`CREATE INDEX idx_users_keep ON users(display_name)`,
		`CREATE TABLE user_audit (id INTEGER PRIMARY KEY AUTOINCREMENT, uid INTEGER NOT NULL)`,
		`CREATE TRIGGER users_after_insert AFTER INSERT ON users
		 BEGIN INSERT INTO user_audit(uid) VALUES (new.id); END`,
	}
	for _, q := range schema {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("建存量夹具失败: %v\nSQL: %s", err, q)
		}
	}
	if _, err := raw.Exec(`INSERT INTO users (id,tenant_id,username,password_hash,email,display_name,profile_setup_required)
		VALUES (1,2,'alice','h1','alice@example.com','爱丽丝',1)`); err != nil {
		t.Fatalf("插入存量行失败: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO users (id,tenant_id,username,password_hash,email,display_name)
		VALUES (7,2,'bob','h2','bob@example.com','鲍勃')`); err != nil {
		t.Fatalf("插入存量行失败: %v", err)
	}
	// 先把自增序列人为推高，验证重建后不复用已删除的 id。
	if _, err := raw.Exec(`UPDATE sqlite_sequence SET seq=99 WHERE name='users'`); err != nil {
		t.Fatalf("推高序列失败: %v", err)
	}

	s := &Store{db: raw}
	if err := s.migrateUsernameNotUnique(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	var ddl string
	if err := raw.QueryRow(`SELECT COALESCE(sql,'') FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&ddl); err != nil {
		t.Fatalf("读新表定义失败: %v", err)
	}
	if strings.Contains(strings.ToLower(ddl), "unique(username") {
		t.Fatalf("重建后仍带用户名唯一约束：%s", ddl)
	}
	// 同租户同名必须能再插一行（这就是用户令：用户名不是登录标识）。
	res, err := raw.Exec(`INSERT INTO users (tenant_id,username,password_hash,email,display_name)
		VALUES (2,'alice','h3','alice2@example.com','另一个爱丽丝')`)
	if err != nil {
		t.Fatalf("同租户重名应合法，实际失败: %v", err)
	}
	newID, _ := res.LastInsertId()
	if newID <= 99 {
		t.Fatalf("重建后自增序列被回退：新 id=%d，应 >99", newID)
	}

	var display, email string
	var setup int
	if err := raw.QueryRow(`SELECT display_name,email,profile_setup_required FROM users WHERE id=1`).Scan(&display, &email, &setup); err != nil {
		t.Fatalf("存量行丢失: %v", err)
	}
	if display != "爱丽丝" || email != "alice@example.com" || setup != 1 {
		t.Fatalf("存量列值未原样保留：display=%q email=%q setup=%d", display, email, setup)
	}
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("行数不对：n=%d err=%v", n, err)
	}
	// 普通索引与触发器必须还在：依赖对象不是"重建完就算了"的附属品。
	var keep int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_users_keep'`).Scan(&keep); err != nil || keep != 1 {
		t.Fatalf("普通索引 idx_users_keep 未被恢复：keep=%d err=%v", keep, err)
	}
	var audited int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM user_audit WHERE uid=?`, newID).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("触发器 users_after_insert 未被恢复：audited=%d err=%v", audited, err)
	}
	// 非唯一检索索引必须补上。
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_users_tenant_username'`).Scan(&keep); err != nil || keep != 1 {
		t.Fatalf("idx_users_tenant_username 未建立：keep=%d err=%v", keep, err)
	}

	// 幂等：第二次不得再重建、更不能动数据。
	if err := s.migrateUsernameNotUnique(); err != nil {
		t.Fatalf("第二次迁移应幂等：%v", err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("幂等迁移改动了行数：n=%d err=%v", n, err)
	}
}

func TestUsersMigrateNewSchemaHasNoUsernameUnique(t *testing.T) {
	s := newTestStore(t)
	var ddl string
	if err := s.DB().QueryRow(`SELECT COALESCE(sql,'') FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&ddl); err != nil {
		t.Fatalf("读 users 定义失败: %v", err)
	}
	if strings.Contains(strings.ToLower(ddl), "unique(username") {
		t.Fatalf("新库 users 仍带用户名唯一约束：%s", ddl)
	}
	for _, col := range []string{"email", "profile_setup_required", "preferred_lang", "scim_external_id", "deactivate_at"} {
		if !strings.Contains(strings.ToLower(ddl), col) {
			t.Fatalf("新库 users 缺列 %s：%s", col, ddl)
		}
	}
}

func TestUsersMigrateSQLiteDropsCustomUsernameUniqueIndex(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	for _, q := range []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			username TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE UNIQUE INDEX uq_users_name ON users(username, tenant_id)`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("建夹具失败: %v", err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO users (tenant_id,username,password_hash) VALUES (4,'carol','h')`); err != nil {
		t.Fatalf("插入夹具失败: %v", err)
	}
	s := &Store{db: raw}
	if err := s.migrateUsernameNotUnique(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO users (tenant_id,username,password_hash) VALUES (4,'carol','h2')`); err != nil {
		t.Fatalf("手工唯一索引未被摘掉: %v", err)
	}
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM users WHERE username='carol'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("重名行数不对: n=%d err=%v", n, err)
	}
}

func TestOneidMigrateNormalizesEmailBeforeUniqueIndex(t *testing.T) {
	s := newTestStore(t)
	// 模拟旧库：先摘掉新表达式索引，否则带空白的重复行插入不进去（正是这份索引要治的形态）。
	if _, err := s.DB().Exec(`DROP INDEX IF EXISTS idx_users_email_lower_active`); err != nil {
		t.Fatalf("摘除新索引失败: %v", err)
	}
	res, err := s.DB().Exec(`INSERT INTO users (tenant_id,username,password_hash,status,email)
		VALUES (1,'norm_a','h','active','  Alice@Example.COM '),
		       (1,'norm_b','h','active','alice@example.com'),
		       (1,'norm_off','h','disabled',' ALICE@example.com ')`)
	if err != nil {
		t.Fatalf("插入归一化夹具失败: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 3 {
		t.Fatalf("夹具行数不对: %d", n)
	}
	s.OneidMigrate()

	var held, emptied int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE email='alice@example.com' AND status='active'`).Scan(&held); err != nil {
		t.Fatalf("读持有行失败: %v", err)
	}
	if held != 1 {
		t.Fatalf("归一化后应恰好 1 个 active 账号持有该邮箱，实得 %d", held)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE status='active' AND email=''`).Scan(&emptied); err != nil {
		t.Fatalf("读解绑行失败: %v", err)
	}
	if emptied != 1 {
		t.Fatalf("重复占用应解绑较晚的一行，实得 %d 行被解绑", emptied)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_users_email_lower_active'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("归一化唯一索引缺失: n=%d err=%v", n, err)
	}
}
