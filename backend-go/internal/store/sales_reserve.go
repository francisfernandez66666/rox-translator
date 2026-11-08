// ============ sales_reserve.go · 职责说明 ============
// 二期 §4 断言 D4 的原子写原语：替代 SCRM `api/strategy.go:156` 那种
// 「先 Count 预检再 409」的非原子形态（并发下两个请求同时过预检 ⇒ 后写覆盖）。
// ★ 一条语句完成判据与写入：`INSERT … SELECT … WHERE NOT EXISTS`（照现网
//
//	`billing.go:2368` 补键那族形态，双方言同一条 SQL）；唯一索引兜 PG 竞态，
//	败方撞索引经 db.IsUniqueViolation 判成「没抢到」而不是错误上抛。
//
// ================================================
package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
)

// TryReserve 原子占位：同一 (租户, 类型, 编码) 全局只允许一个成功者。
// 参数：tid=租户 ID；kind=占位类型；code=业务编码；返回是否抢到与错误。
func (s *Store) TryReserve(tid int64, kind, code string) (bool, error) {
	if tid <= 0 || strings.TrimSpace(kind) == "" || strings.TrimSpace(code) == "" {
		return false, errors.New("原子占位必须带有效租户、类型与编码")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_reservations (tenant_id, kind, code, created_at)
		 SELECT ?,?,?,? WHERE NOT EXISTS (
			SELECT 1 FROM sales_reservations WHERE tenant_id=? AND kind=? AND code=?)`,
		tid, kind, code, now, tid, kind, code)
	if err != nil {
		if reservationConflict(err) {
			return false, nil // 并发败方：撞唯一索引不是故障
		}
		return false, fmt.Errorf("原子占位失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("原子占位读数失败: %w", err)
	}
	return n == 1, nil
}

// reservationConflict 判并发败方撞唯一索引（PG 竞态兜底）。
// SQLite 下条件 INSERT 通常直接给出 RowsAffected=0、不产生错误，故这一支必须由
// 独立的合成 PG 错误用例覆盖，否则「摘掉这行」在本机全绿、生产却把败方写成 500。
// 参数：err=写操作错误；返回是否属于唯一键冲突。
func reservationConflict(err error) bool {
	return db.IsUniqueViolation(err)
}
