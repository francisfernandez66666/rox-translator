// ============ sales_draft.go · 职责说明 ============
// 二期 §7 归因 → 挖掘 → 人审闭环的「草稿池」：挖掘产物一律 status=draft，
// 只有经 ApproveSalesDraft 这一个函数才能转成 approved（转正动作可追、可审计）。
// ★ 自学习边界：草稿永不自动进一期 kb_entries 的「已批准说法」，
// 「越用越准」与「越用越像幻觉」的分界就在这一道人审闸。
// ================================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/idgen"
)

// 草稿状态三值（第四值写侧硬拒，不靠数据库约束兜）。
const (
	SalesDraftStatusDraft    = "draft"
	SalesDraftStatusApproved = "approved"
	SalesDraftStatusRejected = "rejected"
)

// ErrSalesDraftNotFound 草稿不存在、跨租户或状态已不允许转正（一律 not-found）。
var ErrSalesDraftNotFound = errors.New("草稿不存在或状态不允许转正")

// SalesDraft 一条待审说法草稿。
type SalesDraft struct {
	ID             string `json:"id"`
	TenantID       int64  `json:"tenant_id"`
	Anchor         string `json:"anchor"`
	SourceText     string `json:"source_text"`
	SuggestedText  string `json:"suggested_text"`
	AttributionRef string `json:"attribution_ref"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// CreateSalesDraft 落一条**草稿**（状态恒为 draft，调用方不许指定）。
// 参数：tid=租户；anchor=锚位；sourceText=原文；suggestedText=建议说法；attributionRef=归因引用。
// 返回：草稿行与错误。
func (s *Store) CreateSalesDraft(tid int64, anchor, sourceText, suggestedText, attributionRef string) (SalesDraft, error) {
	if tid <= 0 || strings.TrimSpace(suggestedText) == "" {
		return SalesDraft{}, errors.New("草稿必须带有效租户与非空建议说法")
	}
	id, err := idgen.NewCode("sdr_")
	if err != nil {
		return SalesDraft{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	d := SalesDraft{
		ID: id, TenantID: tid, Anchor: anchor, SourceText: sourceText,
		SuggestedText: suggestedText, AttributionRef: attributionRef,
		Status: SalesDraftStatusDraft, CreatedAt: now, UpdatedAt: now,
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_drafts
		 (id, tenant_id, anchor, source_text, suggested_text, attribution_ref, status, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		d.ID, d.TenantID, d.Anchor, d.SourceText, d.SuggestedText, d.AttributionRef,
		d.Status, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return SalesDraft{}, fmt.Errorf("草稿落库失败: %w", err)
	}
	return d, nil
}

// ListSalesDrafts 按租户与状态列草稿；status 为空时列全部。
// 参数：tid=租户；status=状态过滤（可空）；返回草稿切片（空集出 []，不出 null）。
func (s *Store) ListSalesDrafts(tid int64, status string) ([]SalesDraft, error) {
	out := make([]SalesDraft, 0)
	if tid <= 0 {
		return out, nil
	}
	q := `SELECT id, tenant_id, anchor, source_text, suggested_text, attribution_ref, status, created_at, updated_at
	      FROM sales_drafts WHERE tenant_id=?`
	args := []interface{}{tid}
	if strings.TrimSpace(status) != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY id ASC`
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return out, fmt.Errorf("列草稿失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d SalesDraft
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Anchor, &d.SourceText, &d.SuggestedText,
			&d.AttributionRef, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return out, fmt.Errorf("扫草稿行失败: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetSalesDraft 按 (租户, 草稿 ID) 读一行。
// 参数：tid=租户；id=草稿 ID；返回草稿行与错误。
func (s *Store) GetSalesDraft(tid int64, id string) (SalesDraft, error) {
	var d SalesDraft
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT id, tenant_id, anchor, source_text, suggested_text, attribution_ref, status, created_at, updated_at
		 FROM sales_drafts WHERE id=? AND tenant_id=?`, id, tid).
		Scan(&d.ID, &d.TenantID, &d.Anchor, &d.SourceText, &d.SuggestedText,
			&d.AttributionRef, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SalesDraft{}, ErrSalesDraftNotFound
	}
	if err != nil {
		return SalesDraft{}, fmt.Errorf("读取草稿失败: %w", err)
	}
	return d, nil
}

// ApproveSalesDraft **唯一转正入口**：draft → approved，重复转正/跨租户一律 not-found。
// 参数：tid=租户；id=草稿 ID；返回错误。
func (s *Store) ApproveSalesDraft(tid int64, id string) error {
	if tid <= 0 || strings.TrimSpace(id) == "" {
		return ErrSalesDraftNotFound
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE sales_drafts SET status=?, updated_at=? WHERE id=? AND tenant_id=? AND status=?`,
		SalesDraftStatusApproved, time.Now().UTC().Format(time.RFC3339), id, tid, SalesDraftStatusDraft)
	if err != nil {
		return fmt.Errorf("转正失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("转正读数失败: %w", err)
	}
	if n == 0 {
		return ErrSalesDraftNotFound
	}
	return nil
}
