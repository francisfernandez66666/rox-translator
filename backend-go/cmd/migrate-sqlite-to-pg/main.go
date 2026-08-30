// ============ 本文件职责中文说明 ============
// 一次性数据迁移工具：将存量 SQLite（业务+KB 共用库）的数据拷贝到已建好 schema 的 PostgreSQL。
//
// 适用场景：从 SQLite 单文件切换到托管 RDS PG（见《系统优化方案.md》§〇 工作流 A）。
// 前置条件：
//   1) 已用 DB_DRIVER=postgres 启动过一次服务端，使 PG 侧表结构（含 pgvector 列）创建完毕；
//   2) pgvector 扩展已在目标库安装（EnablePgvector 会自动 CREATE EXTENSION）；
//   3) 本工具仅拷贝"数据"，不拷贝向量列（embedding），切换后跑一次 RebuildKBIndex 回填。
//
// 幂等：所有 INSERT 使用 ON CONFLICT DO NOTHING，可重复执行；运行前请备份 SQLite 与 pg_dump。
// 用法：
//   go run ./cmd/migrate-sqlite-to-pg -sqlite /path/tm.sqlite3 -dsn "postgres://user:pass@host:5432/db?sslmode=disable"
// =============================================
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"translator/internal/db"
)

// skipTables 跳过瞬态/重启自重建的表（jobs 异步账本、ticket_state 进度轨迹）
var skipTables = map[string]bool{
	"jobs":         true,
	"ticket_state": true,
	"sqlite_sequence": true,
}

// preferredOrder 父表优先，规避外键插入顺序问题；未列出者排在末尾。
var preferredOrder = []string{
	"tenants", "orgs", "users", "kb_packages", "packages", "invoices",
	"orders", "payments", "quota_grants", "rate_card", "balance_accounts",
	"api_keys", "invite_codes", "referral_rewards", "rate_limits",
	"kb_entries", "kb_safety_phrases", "tm_segments", "tm_hit_count", "tm_review",
	"tickets", "ticket_files", "output_artifacts", "usage_daily", "usage_ledger",
	"webhooks", "notifications", "alerts", "audit_logs", "feedbacks",
	"eval_records", "system_config",
}

func main() {
	sqlitePath := flag.String("sqlite", "", "源 SQLite 文件路径（必填）")
	pgDSN := flag.String("dsn", "", "目标 PostgreSQL 连接串（必填）")
	onlyTable := flag.String("table", "", "仅迁移指定表（调试用）")
	flag.Parse()

	if *sqlitePath == "" || *pgDSN == "" {
		log.Fatal("用法: migrate-sqlite-to-pg -sqlite <path> -dsn <pg_dsn>")
	}

	src, err := sql.Open("sqlite", db.SQLiteDSN(*sqlitePath))
	if err != nil {
		log.Fatalf("打开 SQLite 失败: %v", err)
	}
	defer src.Close()
	if err := src.Ping(); err != nil {
		log.Fatalf("SQLite 探活失败: %v", err)
	}

	dst, err := sql.Open("postgres", *pgDSN)
	if err != nil {
		log.Fatalf("打开 PostgreSQL 失败: %v", err)
	}
	defer dst.Close()
	if err := dst.Ping(); err != nil {
		log.Fatalf("PostgreSQL 探活失败: %v", err)
	}

	tables, err := listTables(src)
	if err != nil {
		log.Fatalf("列举源表失败: %v", err)
	}
	sort.Slice(tables, func(i, j int) bool {
		return orderIdx(tables[i]) < orderIdx(tables[j])
	})

	totalCopied := 0
	for _, t := range tables {
		if skipTables[t] {
			log.Printf("[skip] %s（瞬态表，跳过）", t)
			continue
		}
		if *onlyTable != "" && t != *onlyTable {
			continue
		}
		n, err := copyTable(src, dst, t)
		if err != nil {
			log.Printf("[fail] %s: %v", t, err)
			continue
		}
		totalCopied += n
		log.Printf("[ok] %s: %d 行", t, n)
	}
	log.Printf("迁移完成，共拷贝 %d 行（不含跳过表）。请核对行数并执行 RebuildKBIndex 回填向量。", totalCopied)
}

// listTables 返回 sqlite_master 中的用户表。
func listTables(src *sql.DB) ([]string, error) {
	rows, err := src.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// orderIdx 返回表在 preferredOrder 中的序号，未列出者排末尾。
func orderIdx(t string) int {
	for i, v := range preferredOrder {
		if v == t {
			return i
		}
	}
	return len(preferredOrder) + 1
}

// copyTable 将单表数据从 src 拷贝到 dst（列对齐 + 类型适配 + 幂等插入）。
func copyTable(src, dst *sql.DB, table string) (int, error) {
	srcCols, err := sqliteColumns(src, table)
	if err != nil {
		return 0, fmt.Errorf("读源列: %w", err)
	}
	pgCols, pgTypes, err := pgColumns(dst, table)
	if err != nil {
		return 0, fmt.Errorf("读目标列(表可能不存在): %w", err)
	}

	// 取交集；跳过 PG 侧 vector 类型列（embedding 由 RebuildKBIndex 回填）
	var cols []string
	for _, c := range srcCols {
		pgType, ok := pgCols[c]
		if !ok {
			continue // 目标无此列则忽略源列
		}
		if strings.Contains(pgType, "vector") {
			continue
		}
		cols = append(cols, c)
	}
	if len(cols) == 0 {
		return 0, nil
	}

	// 构造参数占位符（$1..$n 为 PostgreSQL 风格）
	ph := make([]string, len(cols))
	for i := range cols {
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING",
		table, strings.Join(cols, ","), strings.Join(ph, ","))

	rows, err := src.Query(fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ","), table))
	if err != nil {
		return 0, fmt.Errorf("查询源: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		// 动态构造扫描目标与绑定值
		scanPtrs := make([]interface{}, len(cols))
		vals := make([]interface{}, len(cols))
		for i := range cols {
			scanPtrs[i] = &vals[i]
		}
		if err := rows.Scan(scanPtrs...); err != nil {
			return count, fmt.Errorf("扫描源行: %w", err)
		}
		// 布尔列适配：SQLite 以 0/1 整型存储，转 bool
		args := make([]interface{}, len(cols))
		for i, c := range cols {
			v := vals[i]
			if strings.Contains(pgTypes[c], "boolean") {
				args[i] = toBool(v)
			} else {
				args[i] = v
			}
		}
		if _, err := dst.Exec(insertSQL, args...); err != nil {
			// 单行失败不影响整体；记录后继续
			log.Printf("[row-fail] %s: %v", table, err)
			continue
		}
		count++
	}
	return count, rows.Err()
}

// sqliteColumns 返回表列名（PRAGMA table_info）。
func sqliteColumns(src *sql.DB, table string) ([]string, error) {
	rows, err := src.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt valueScanner
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// pgColumns 返回 PostgreSQL 表的列名→数据类型映射（data_type）。
func pgColumns(dst *sql.DB, table string) (map[string]string, map[string]string, error) {
	rows, err := dst.Query(
		`SELECT column_name, data_type FROM information_schema.columns WHERE table_schema='public' AND table_name=$1`,
		table)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	names := map[string]string{}
	types := map[string]string{}
	for rows.Next() {
		var name, dtype string
		if err := rows.Scan(&name, &dtype); err != nil {
			return nil, nil, err
		}
		names[name] = dtype
		types[name] = dtype
	}
	return names, types, rows.Err()
}

// valueScanner 用于吸收 PRAGMA 的默认值列（可能为 NULL）。
type valueScanner struct{ v sql.NullString }

func (s *valueScanner) Scan(src interface{}) error {
	if src == nil {
		s.v = sql.NullString{Valid: false}
		return nil
	}
	switch v := src.(type) {
	case string:
		s.v = sql.NullString{String: v, Valid: true}
	case []byte:
		s.v = sql.NullString{String: string(v), Valid: true}
	default:
		s.v = sql.NullString{String: fmt.Sprintf("%v", v), Valid: true}
	}
	return nil
}

// toBool 将 SQLite 的整型/字符串 0/1 转为 Go bool（供 PG boolean 绑定）。
func toBool(v interface{}) bool {
	switch x := v.(type) {
	case int64:
		return x != 0
	case float64:
		return x != 0
	case bool:
		return x
	case string:
		return x == "1" || x == "true" || x == "t"
	case nil:
		return false
	default:
		return false
	}
}
