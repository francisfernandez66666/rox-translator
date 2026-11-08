// ============ conflict.go · 职责说明 ============
// 唯一约束冲突的跨方言唯一判据：PostgreSQL 认 SQLSTATE 23505，SQLite 认
// "UNIQUE constraint failed" 前缀。二期 S1 把判据先收敛到本文件，S2 迁入 SCRM
// 引擎时所有幂等回落（队列去重／回读既有单／重抽签／草稿去重）一律调用它，
// 禁止调用方再自行判 23505 字符串——SQLite 下那种写法恒假且不报错。
// 判定顺序：先 errors.As 到驱动的结构化错误类型，再退化到文本；
// 文本分支保留原 store/dberr.go 的三支，确保收敛不改现网已生效的幂等行为。
// =============================================
package db

import (
	"errors"
	"strings"

	"github.com/lib/pq"
)

// 两方言的冲突锚点：PG 认 SQLSTATE，SQLite 认文本前缀（旧三支逐字保留，防现网幂等行为变）。
const (
	// pgUniqueViolationCode 是 PostgreSQL 唯一约束冲突的 SQLSTATE。
	pgUniqueViolationCode = "23505"
	// sqliteUniqueViolationPrefix 是 SQLite 唯一约束失败消息前缀；消息后带表名列名，故只作前缀匹配。
	sqliteUniqueViolationPrefix = "UNIQUE constraint failed"
	// pgDuplicateText 是 lib/pq 文本化后的唯一冲突消息（原判据第二支，保留以兼容现网）。
	pgDuplicateText = "duplicate key value violates unique constraint"
)

// IsUniqueViolation 判定 err 是否为唯一约束／唯一索引冲突。
// 参数：err=数据库写操作返回的错误，允许为 nil 或经 fmt.Errorf("%w") 包装。
// 返回：true 表示应走幂等回落（回读既有行／重抽签／计 SkippedExisting）；
// false 表示其他错误，调用方必须原样上抛，不许吞成成功。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pq.Error
	if errors.As(err, &pgErr) {
		return string(pgErr.Code) == pgUniqueViolationCode
	}
	msg := err.Error()
	if strings.Contains(msg, sqliteUniqueViolationPrefix) {
		return true
	}
	if strings.Contains(msg, pgDuplicateText) {
		return true
	}
	return strings.Contains(msg, "("+pgUniqueViolationCode+")") ||
		strings.Contains(msg, "SQLSTATE "+pgUniqueViolationCode)
}
