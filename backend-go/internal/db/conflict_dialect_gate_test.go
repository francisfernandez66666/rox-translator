// ============ 本文件职责中文说明 ============
// 二期 S1《六处冲突判定方言化》门禁（施工图 §4 的 D1／D2／D3）：
// D1 两方言各插入两次均判 true（SQLite 真驱动必跑；PG 真连接可用则连，
// 不可用仍用真 *pq.Error 类型覆盖结构化分支，绝不 skip 成空转）；
// 旧判据三支文本逐条等值对照（收敛不改现网八个调用点的幂等行为）；
// 其他 SQLSTATE／普通错误不误判；
// D3 全仓剥注释后扫三支冲突字面量，除 internal/db/conflict.go 外必须为 0，
// 并配"往别处塞一处字面量必红"的反证；D3b store 侧只许薄委托。
// 复用包内 Open／Exec 与真实 modernc SQLite 驱动；自钉方言防 PG 模式泄漏给同包后续用例。
// ========================================
package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lib/pq"
	_ "modernc.org/sqlite"
	"translator/internal/config"
)

// uniqueViolationLiterals 是"调用方自己判唯一冲突"会用到的三支字面量；
// 除判据本体所在文件外，任何 .go 代码里出现其一即为第二把尺子。
var uniqueViolationLiterals = []string{
	pgUniqueViolationCode,
	sqliteUniqueViolationPrefix,
	pgDuplicateText,
}

// pinSQLiteDialectForConflictTest 自钉方言，避免 config.Default() 读到的
// DB_DRIVER=postgres 泄漏给同包后续内存 SQLite 用例（AGENTS §一·4）。
func pinSQLiteDialectForConflictTest(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = string(DialectSQLite)
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// D1：同一张表同一唯一键，两种方言各插入两次，判据必须都为 true。
func TestUniqueViolationBothDialectsOnRealDrivers(t *testing.T) {
	pinSQLiteDialectForConflictTest(t)

	// ① SQLite：真 modernc 驱动，第二次插入必须真的被唯一索引拦下。
	conn, err := Open(Config{Driver: DriverSQLite, DSN: ":memory:"})
	if err != nil {
		t.Fatalf("SQLite 建连失败: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE TABLE conflict_demo (code TEXT UNIQUE)"); err != nil {
		t.Fatalf("D1 SQLite 建表失败: %v", err)
	}
	if _, err := Exec(conn, DialectSQLite, "INSERT INTO conflict_demo (code) VALUES (?)", "dup"); err != nil {
		t.Fatalf("D1 SQLite 首次插入失败: %v", err)
	}
	_, dupErr := Exec(conn, DialectSQLite, "INSERT INTO conflict_demo (code) VALUES (?)", "dup")
	if dupErr == nil {
		t.Fatal("D1 前提失效：SQLite 唯一索引没拦住第二次插入，本条断言已失明")
	}
	if !IsUniqueViolation(dupErr) {
		t.Fatalf("D1 SQLite 真驱动的冲突必须判 true，实得 false（err=%v）", dupErr)
	}

	// ② PG 真连接：有实例就连真库再插一次；没有实例也不跳过整条断言。
	if pgConn, pgOpenErr := Open(Config{Driver: DriverPostgres, DSN: pgTestDSN()}); pgOpenErr == nil {
		defer pgConn.Close()
		if _, err := pgConn.Exec("DROP TABLE IF EXISTS conflict_demo_pg"); err != nil {
			t.Fatalf("D1 PG 清表失败: %v", err)
		}
		if _, err := pgConn.Exec("CREATE TABLE conflict_demo_pg (code TEXT UNIQUE)"); err != nil {
			t.Fatalf("D1 PG 建表失败: %v", err)
		}
		if _, err := Exec(pgConn, DialectPostgres, "INSERT INTO conflict_demo_pg (code) VALUES (?)", "dup"); err != nil {
			t.Fatalf("D1 PG 首次插入失败: %v", err)
		}
		_, pgDupErr := Exec(pgConn, DialectPostgres, "INSERT INTO conflict_demo_pg (code) VALUES (?)", "dup")
		if pgDupErr == nil {
			t.Fatal("D1 前提失效：PG 唯一索引没拦住第二次插入")
		}
		if !IsUniqueViolation(pgDupErr) {
			t.Fatalf("D1 PG 真驱动的冲突必须判 true，实得 false（err=%v）", pgDupErr)
		}
		t.Log("D1 PG 真驱动连线已跑")
	} else {
		t.Logf("D1 PG 真连线不可用（%v）；结构化分支仍由真 *pq.Error 类型覆盖，真连线由 PG 主矩阵补", pgOpenErr)
	}

	// ③ 结构化驱动类型（不依赖 PG 实例，保证这条腿永不空转）。
	structured := fmt.Errorf("insert inbox: %w", &pq.Error{Code: pq.ErrorCode(pgUniqueViolationCode)})
	if !IsUniqueViolation(structured) {
		t.Fatal("D1 PG 结构化唯一冲突必须判 true（errors.As 未穿透包装）")
	}
}

// 旧判据三支文本逐条等值：收敛到 db 包不许改掉现网已生效的幂等行为。
func TestLegacyUniqueViolationBranchesStayEquivalent(t *testing.T) {
	cases := map[string]string{
		"SQLite 前缀（消息带表名列名）":   "constraint failed: " + sqliteUniqueViolationPrefix + ": users.username (2067)",
		"PG 文本化 duplicate key": "pq: " + pgDuplicateText + " \"users_username_tenant_id_key\"",
		"PG 文本带 SQLSTATE 括注":   "创建失败: pq: " + pgDuplicateText + " \"x\" (" + pgUniqueViolationCode + ")",
		"日志文本化 SQLSTATE":       "write failed: SQLSTATE " + pgUniqueViolationCode,
	}
	for name, msg := range cases {
		if !IsUniqueViolation(errors.New(msg)) {
			t.Fatalf("旧判据分支被改坏 ⇒ 现网幂等回落会失效：%s（%s）", name, msg)
		}
	}
}

// 负向对照：非唯一冲突一律 false，调用方必须原样上抛而不许吞成成功。
func TestUniqueViolationRejectsOtherErrors(t *testing.T) {
	cases := map[string]error{
		"nil 错误":     nil,
		"普通错误":       errors.New("connection refused"),
		"PG 外键冲突":    &pq.Error{Code: pq.ErrorCode("23503")},
		"SQLite 语法错": errors.New("SQL logic error: near \"LIMIT\": syntax error (1)"),
		"PG 非空约束":    &pq.Error{Code: pq.ErrorCode("23502")},
		"被包装的普通错误":   fmt.Errorf("insert: %w", errors.New("disk full")),
	}
	for name, err := range cases {
		if IsUniqueViolation(err) {
			t.Fatalf("负向对照被误判成唯一冲突：%s（%v）", name, err)
		}
	}
}

// stripGoLineComment 剥掉行尾 // 注释；历史说明性注释里带 SQLSTATE 不算第二把尺子。
// 参数：line=单行源码；返回剥注释后的代码部分。
func stripGoLineComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

// scanUniqueViolationLiterals 扫描 root 下所有 .go，剥注释后找三支冲突字面量；
// 唯一判据本体 internal/db/conflict.go 自身跳过。
// 参数：root=扫描根目录；返回命中清单（相对路径:行号）。
func scanUniqueViolationLiterals(root string) ([]string, error) {
	var hits []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		if rel == filepath.Join("internal", "db", "conflict.go") {
			return nil
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		for i, line := range strings.Split(string(raw), "\n") {
			code := stripGoLineComment(line)
			for _, lit := range uniqueViolationLiterals {
				if strings.Contains(code, lit) {
					hits = append(hits, fmt.Sprintf("%s:%d", rel, i+1))
					break
				}
			}
		}
		return nil
	})
	return hits, err
}

// D3：全仓除判据本体外，代码里不许再出现任何一支冲突字面量。
func TestNoRawUniqueViolationLiteralOutsideHelper(t *testing.T) {
	root := findRepoGoRoot(t)
	hits, err := scanUniqueViolationLiterals(root)
	if err != nil {
		t.Fatalf("D3 扫描失败: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("D3：调用方仍在自己判唯一冲突（SQLite 下会静默恒假）：%v", hits)
	}
}

// D3 反证：扫描器必须真的看得见一处违规，防"扫描集为空＝恒绿"。
func TestUniqueViolationLiteralScanHasTeeth(t *testing.T) {
	dir := t.TempDir()
	bad := "package fake\n\nimport \"strings\"\n\nfunc f(err error) bool {\n\treturn strings.Contains(err.Error(), \"" + pgUniqueViolationCode + "\")\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("反证夹具写入失败: %v", err)
	}
	hits, err := scanUniqueViolationLiterals(dir)
	if err != nil {
		t.Fatalf("反证扫描失败: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("D3 反证必须命中 1 处，实得 %d ⇒ 扫描器失明，D3 是空转锁", len(hits))
	}
}

// D3b：store 侧只能薄委托，不许把字符串判定搬回去。
func TestStoreUniqueViolationIsThinDelegate(t *testing.T) {
	root := findRepoGoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "internal", "store", "dberr.go"))
	if err != nil {
		t.Fatalf("读 store/dberr.go 失败: %v", err)
	}
	var code strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		code.WriteString(stripGoLineComment(line))
		code.WriteString("\n")
	}
	if !strings.Contains(code.String(), "return db.IsUniqueViolation(err)") {
		t.Fatal("D3b：store.IsUniqueViolation 必须是 db 包的薄委托，禁止在此重建第二把尺子")
	}
}
