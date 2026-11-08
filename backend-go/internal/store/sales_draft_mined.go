// ============ sales_draft_mined.go · 职责说明 ============
// 二期 S2 talkmining 出稿落库口（实现 talkmining.DraftWriter）：把确定性 ID 的草稿幂等写进
// sales_drafts。**状态恒为 draft**——挖掘产物一律先落人审池，绝不自动生效（§7 M2）。
//
// 幂等形状＝「INSERT … SELECT … WHERE NOT EXISTS」一条语句完成判重＋写入（照
// billing.go 条件 INSERT 先例），不用「先 Count 预检再插」那种并发下会双写的形态；
// 撞主键（另一个进程抢先）由 db.IsUniqueViolation 判双方言后视为「已存在」。
// ★ 本文件只 INSERT，不含任何 UPDATE：转正入口全仓仍只有 ApproveSalesDraft 一处（M1）。
// ================================================
package store

import (
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
)

// CreateMinedDraft 幂等写一条挖掘草稿。
// 参数：tid=租户号；id=确定性草稿 ID；anchor=锚位上下文；source=已脱敏代表原文；
// suggested=LLM 归纳出的建议说法；attribution=簇签名（溯源）。
// 返回：created=true 表示本轮真写入；false 表示同 ID 已存在（不报错、不重复出稿）。
func (s *Store) CreateMinedDraft(tid int64, id, anchor, source, suggested, attribution string) (bool, error) {
	if tid <= 0 || strings.TrimSpace(id) == "" || strings.TrimSpace(suggested) == "" {
		return false, fmt.Errorf("挖掘草稿必须带有效租户、ID 与非空建议说法")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_drafts
		 (id, tenant_id, anchor, source_text, suggested_text, attribution_ref, status, created_at, updated_at)
		 SELECT ?,?,?,?,?,?,?,?,?
		 WHERE NOT EXISTS (SELECT 1 FROM sales_drafts WHERE id=? AND tenant_id=?)`,
		id, tid, anchor, source, strings.TrimSpace(suggested), attribution,
		SalesDraftStatusDraft, now, now, id, tid)
	if err != nil {
		// 并发下另一进程抢先插入：唯一键冲突即「已存在」，两种方言同判。
		if db.IsUniqueViolation(err) {
			return false, nil
		}
		return false, fmt.Errorf("挖掘草稿落库失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("挖掘草稿写入读数失败: %w", err)
	}
	return n > 0, nil
}
