// ============ 本文件职责中文说明 ============
// testdb_test.go · iam 包测试用的 users 表夹具（唯一构造口）。
//
// 为什么要有这一个文件：iam 的测试夹具历史上是**手抄一份列清单**建表，
// 而生产侧的列清单长在两个地方（store.go 的建表 DDL + columnAdditions 补列），
// 每加一列就得记得回来改夹具——2026-10-24 多身份 B1 加 account_id/is_home 时
// 这两份抄本又漂了一次（表现＝「table users has no column named account_id」）。
//
// 修法按 AGENTS「同一把尺子」：夹具不再手抄，**列清单直接从查询侧常量 userCols 现算**，
// userCols 里加一列，夹具自动就有这一列；iam 另读另写的列（preferred_lang）
// 显式列在 extraTestColumns 里并注明「谁在用」，除此之外不许再加第三把尺子。
//
// ⚠️ 这里刻意**不**跑 store 的迁移：iam 是被 internal/store 依赖的底层包
//   （反向 import 会成环），夹具只负责「形状够用」，
//   迁移本身的正确性由 internal/store 的迁移测试承担。
// ========================================

package iam

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// extraTestColumns 不在 userCols 里、但 iam 的 SQL 会读写的列（逐列点名用途）。
var extraTestColumns = map[string]string{
	"preferred_lang": "SetPreferredLang/GetPreferredLang（邮件与界面语种偏好，F-17）",
}

// testColumnTypes 列名 → 建表片段（类型与默认值）。
//
// 为什么这张表必须存在而不能"一律建成可空裸列"：userCols 里的裸列（不带 COALESCE 的那些）
// 直接 Scan 进 Go string/int64，读到 NULL 会退
// 「converting NULL to string is unsupported」——生产 DDL 靠 NOT NULL DEFAULT ” 保证这一档，
// 夹具必须同源。**列名清单由 userCols 现算，这里只登记「新列怎么建」**：
// 往 userCols 加一列而没在这里登记，下面 newUsersTestDB 直接点名判红，不会漂成 no-column。
var testColumnTypes = map[string]string{
	"tenant_id":              "INTEGER NOT NULL DEFAULT 1",
	"username":               "TEXT NOT NULL",
	"password_hash":          "TEXT NOT NULL",
	"display_name":           "TEXT NOT NULL DEFAULT ''",
	"role":                   "TEXT NOT NULL DEFAULT 'user'",
	"status":                 "TEXT NOT NULL DEFAULT 'active'",
	"created_by":             "INTEGER NOT NULL DEFAULT 0",
	"last_login_at":          "TEXT NOT NULL DEFAULT ''",
	"org_id":                 "INTEGER NOT NULL DEFAULT 0",
	"email":                  "TEXT NOT NULL DEFAULT ''",
	"agreed_at":              "TEXT NOT NULL DEFAULT ''",
	"must_change_pwd":        "INTEGER NOT NULL DEFAULT 0",
	"token_version":          "INTEGER NOT NULL DEFAULT 0",
	"job_role":               "TEXT NOT NULL DEFAULT ''",
	"preferred_lang":         "TEXT NOT NULL DEFAULT ''",
	"deactivate_at":          "TEXT NOT NULL DEFAULT ''",
	"profile_setup_required": "INTEGER NOT NULL DEFAULT 0",
	"account_id":             "TEXT NOT NULL DEFAULT ''",   // ★ 多身份 D1（人键）
	"is_home":                "INTEGER NOT NULL DEFAULT 0", // ★ 多身份 D2（家行标记）
	"created_at":             "TEXT",
	"updated_at":             "TEXT",
}

// newUsersTestDB 建一个临时文件库，返回其中形状与生产同源的 users 表连接。
// 列清单＝userCols 现算 + extraTestColumns，逐列按 testColumnTypes 登记的类型建。
func newUsersTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "iam_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// 只建主键：其余列一律由下面的派生清单补出，保证「夹具形状」与「查询列清单」同源。
	if _, err := conn.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatal(err)
	}
	cols := append(userColsColumnNames(), extraColumnNames()...)
	var untyped []string
	for _, name := range cols {
		if name == "id" {
			continue // 主键已在建表语句里
		}
		typ, ok := testColumnTypes[name]
		if !ok {
			untyped = append(untyped, name)
			continue
		}
		if _, err := conn.Exec("ALTER TABLE users ADD COLUMN " + name + " " + typ); err != nil {
			t.Fatalf("夹具补列 %s %s 失败: %v", name, typ, err)
		}
	}
	// 派生清单里有列没登记类型 ⇒ 必须点名（不点名就是"加了列、夹具默默不给"，下一批以 no-column 假红）。
	if len(untyped) > 0 {
		t.Fatalf("users 列 %v 在 testColumnTypes 里没有登记类型 ⇒ 往 userCols 加列时要同时在这里登记（照生产 DDL 的 NOT NULL DEFAULT 抄）", untyped)
	}
	// 构造后自证：派生清单里的每一列必须真在表上（否则「同源」这句只是注释）。
	if missing := missingTestColumns(t, conn, cols); len(missing) > 0 {
		t.Fatalf("夹具 users 表缺列 %v ⇒ 派生清单没落到表上，后面的用例会以「no column」假红", missing)
	}
	return conn
}

// newIAMTestStore 临时文件库 iam.Store（users 表形状见本文件头）。
func newIAMTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(newUsersTestDB(t))
}

// userColsColumnNames 从查询侧常量 userCols 现算列名清单：
// 每个片段要么是裸列名，要么是 COALESCE(col,兜底)——取括号里第一个逗号前的那个词。
// 判据只认 userCols 这一个事实源，禁止在本文件再抄一份列名。
//
// ⚠️ 切片段必须**带括号/引号深度**切：`COALESCE(email,”)` 里自带逗号，
// 直接 strings.Split(userCols, ",") 会把它切成「COALESCE(email」+「”)」两段垃圾（本函数首跑真踩）。
func userColsColumnNames() []string {
	out := make([]string, 0, 32)
	depth, inQuote := 0, false
	start := 0
	flush := func(end int) {
		f := strings.TrimSpace(userCols[start:end])
		if f == "" {
			return
		}
		if strings.HasPrefix(strings.ToUpper(f), "COALESCE(") {
			inner := f[len("COALESCE("):]
			if i := strings.Index(inner, ","); i >= 0 {
				inner = inner[:i]
			}
			f = strings.TrimSuffix(strings.TrimSpace(inner), ")")
		}
		out = append(out, f)
	}
	for i := 0; i < len(userCols); i++ {
		switch c := userCols[i]; {
		case c == '\'':
			inQuote = !inQuote
		case inQuote:
			// 引号内的括号/逗号一律不参与切分
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			flush(i)
			start = i + 1
		}
	}
	flush(len(userCols))
	return out
}

// extraColumnNames extraTestColumns 的列名清单。
func extraColumnNames() []string {
	names := make([]string, 0, len(extraTestColumns))
	for k := range extraTestColumns {
		names = append(names, k)
	}
	return names
}

// missingTestColumns 现读表上的真实列，返回派生清单里不在其上的那些。
func missingTestColumns(t *testing.T, conn *sql.DB, want []string) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT name FROM pragma_table_info('users')`)
	if err != nil {
		t.Fatalf("读夹具表列失败: %v", err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil {
			have[n] = true
		}
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// TestFixtureColumnsCoverUserCols 守卫级锁（不是判据级）：
// 夹具建出来的表必须覆盖 userCols 的每一列——将来往 userCols 加列而夹具「够不着」时当场红，
// 而不是让某个用例以「no such column」的形式把「列清单变了」报成「代码坏了」。
func TestFixtureColumnsCoverUserCols(t *testing.T) {
	conn := newUsersTestDB(t)
	cols := append(userColsColumnNames(), extraColumnNames()...)
	if missing := missingTestColumns(t, conn, cols); len(missing) > 0 {
		t.Fatalf("夹具列覆盖不全：%v（userCols 现算共 %d 列）", missing, len(userColsColumnNames()))
	}
	// 反向腿：testColumnTypes 里不许躺"userCols 已经没有的列"——
	// 删列（或从查询清单摘掉一列）时必须一起清登记，否则下一位以为表上还有它。
	inCols := map[string]bool{}
	for _, c := range cols {
		inCols[c] = true
	}
	inQuery := map[string]bool{}
	for _, c := range userColsColumnNames() {
		inQuery[c] = true
	}
	for name := range testColumnTypes {
		if !inCols[name] {
			t.Fatalf("testColumnTypes 登记了 %s，但 userCols/extraTestColumns 都没有它 ⇒ 登记清单比查询清单旧，删列要一起清", name)
		}
		if !inQuery[name] && extraTestColumns[name] == "" {
			t.Fatalf("%s 不在 userCols 里，就必须在 extraTestColumns 里写明「谁在用」（否则看不出这列还要不要）", name)
		}
	}
}
