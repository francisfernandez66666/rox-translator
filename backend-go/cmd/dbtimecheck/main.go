// ============================================================================
// dbtimecheck — ★ C22 时间基准审计/清洗演练工具（2026-09-12）。
// 用途：扫描 TEXT 型时间列的存储格式分布（UTC 'Z' / 带 '+offset' / 无时区），
//
//	输出报告；-fix 模式把带偏移的存量值改写为等价 UTC 'Z' 串（按瞬时值解析
//	重排，不改变语义）。用于「存储统一 UTC RFC3339」迁移的事前盘点与事后核验。
//
// 用法：
//
//	go run ./cmd/dbtimecheck -driver sqlite -dsn ./dev.db [-fix] tbl.col ...
//	go run ./cmd/dbtimecheck -driver postgres -dsn "$DB_DSN" -fix quota_grants.created_at
//
// ============================================================================
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq" // PG 驱动注册（与 cmd/server 一致）

	"translator/internal/db"
)

// main 审计/修复工具入口：检查时间戳列时区偏移，-fix 将存量偏移值改写为 UTC。
func main() {
	dsn := flag.String("dsn", os.Getenv("DB_DSN"), "数据库 DSN（缺省读 DB_DSN）")
	driver := flag.String("driver", os.Getenv("DB_DRIVER"), "sqlite|postgres（缺省读 DB_DRIVER）")
	fix := flag.Bool("fix", false, "把带偏移的存量值改写为 UTC（缺省仅审计）")
	flag.Parse()
	cols := flag.Args()
	if len(cols) == 0 {
		log.Fatal("用法: dbtimecheck [-driver D] [-dsn DSN] [-fix] tbl.col ...")
	}
	conn, err := db.Open(db.Config{Driver: *driver, DSN: *dsn})
	if err != nil {
		log.Fatal("打开数据库失败: ", err)
	}
	defer conn.Close()
	// 方言显式取自 -driver（config.C 未初始化，不能依赖 db.CurrentDialect）
	d := db.DialectSQLite
	if *driver == "postgres" {
		d = db.DialectPostgres
	}
	for _, tc := range cols {
		parts := strings.SplitN(tc, ".", 2)
		if len(parts) != 2 || !safeIdent(parts[0]) || !safeIdent(parts[1]) {
			log.Printf("跳过非法参数 %q（应为 tbl.col）", tc)
			continue
		}
		tbl, col := parts[0], parts[1]
		rows, err := db.Query(conn, d, "SELECT id, "+col+" FROM "+tbl+" WHERE "+col+" <> ''")
		if err != nil {
			log.Printf("[%s] 读取失败: %v", tc, err)
			continue
		}
		type pending struct {
			id   int64
			conv string
		}
		var utcCnt, offCnt, naiveCnt, badCnt int
		var toFix []pending
		for rows.Next() {
			var id int64
			var v string
			if err := rows.Scan(&id, &v); err != nil {
				badCnt++
				continue
			}
			switch {
			case strings.HasSuffix(v, "Z"):
				utcCnt++
			default:
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					// RFC3339 可解析但不以 Z 结尾 = 带偏移形态（本地时区写入）
					offCnt++
					toFix = append(toFix, pending{id, t.UTC().Format(time.RFC3339)})
				} else if _, err := time.Parse("2006-01-02T15:04:05", v); err == nil {
					naiveCnt++ // 无时区形态：语义不确定，改写有风险，仅报告
				} else {
					badCnt++
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Printf("[%s] 遍历出错: %v", tc, err)
		}
		fmt.Printf("%-36s UTC=%-9d 带偏移=%-9d 无时区=%-9d 解析失败=%d\n", tc, utcCnt, offCnt, naiveCnt, badCnt)
		if *fix && len(toFix) > 0 {
			tx, err := conn.Begin()
			if err != nil {
				log.Printf("[%s] 开启事务失败: %v", tc, err)
				continue
			}
			ok := true
			for _, pf := range toFix {
				if _, err := db.Exec(tx, d, "UPDATE "+tbl+" SET "+col+"=? WHERE id=?", pf.conv, pf.id); err != nil {
					tx.Rollback()
					log.Printf("[%s] 改写失败回滚: %v", tc, err)
					ok = false
					break
				}
			}
			if ok {
				if err := tx.Commit(); err == nil {
					fmt.Printf("  → 已改写 %d 行为 UTC\n", len(toFix))
				} else {
					log.Printf("[%s] 提交失败: %v", tc, err)
				}
			}
		}
	}
}

// safeIdent 表/列名白名单校验（防注入——参数来自运维命令行而非用户输入，仍做加固）。
func safeIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
