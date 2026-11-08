// ============ deal_quote.go · 职责说明 ============
// 二期 S1：deal 域「报价」窄域文件（二期 §2 金额启用前置）。
// 一张商机同一时刻只允许一张在途报价（draft／sent），由部分唯一索引
// ux_quote_one_open_per_deal 兜底；撞键一律走 internal/db.IsUniqueViolation
// 判据（S1 第一刀方言化的真实业务落点），SQLite／PG 不许有两套形态。
// ★ 金额一律 int64「分」：与 payments.amount_fen 同量纲；orders.amount_money 是「元」，
//
//	两者换算必须显式 ×100，禁止在本域出现第二套金额量纲（C2 同名不同单位的旧账）。
//
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

// 报价状态七值（二期 §2）：只有 draft／sent 算「在途」，参与部分唯一索引。
const (
	QuoteStatusDraft      = "draft"
	QuoteStatusSent       = "sent"
	QuoteStatusAccepted   = "accepted"
	QuoteStatusDeclined   = "declined"
	QuoteStatusSuperseded = "superseded"
	QuoteStatusVoid       = "void"
	QuoteStatusExpired    = "expired"
)

// ErrQuoteOneOpenPerDeal 同一商机已有一张在途报价；撞部分唯一索引时返回，
// 调用方据此给用户可辨文案，不许按数据库原文分支（两方言的报错文案不同）。
var ErrQuoteOneOpenPerDeal = errors.New("该商机已有一张在途报价")

// ErrQuoteNotFound 报价不存在、跨租户或状态已不允许流转（一律 not-found，不泄露存在性）。
var ErrQuoteNotFound = errors.New("报价不存在或状态不允许该操作")

// Quote 一行报价。TotalCents 单位为分，禁止用浮点表示金额。
type Quote struct {
	ID         string `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	DealID     string `json:"deal_id"`
	TotalCents int64  `json:"total_cents"`
	Currency   string `json:"currency"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// DealMigrate 建 deal 域的部分唯一索引（幂等）。
// 调用位置：store.go New() 的域补丁链，必须排在 migrate() 之后（表要先存在）。
// 参数：无；返回迁移错误，失败直接中断启动，不静默降级。
func (s *Store) DealMigrate() error {
	d := db.CurrentDialect()
	if err := db.ExecDDL(s.db, d,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_quote_one_open_per_deal
		 ON deal_quotes(tenant_id, deal_id) WHERE status IN ('draft','sent')`); err != nil {
		return fmt.Errorf("建 ux_quote_one_open_per_deal 部分唯一索引失败: %w", err)
	}
	return nil
}

// CreateQuote 建一张报价（默认 draft，金额单位为分）。
// 参数：tid=租户 ID；dealID=商机标识；totalCents=金额（分，不许为负）；currency=币种，空则 CNY。
// 返回：报价行与错误；撞在途唯一索引时返回 ErrQuoteOneOpenPerDeal。
func (s *Store) CreateQuote(tid int64, dealID string, totalCents int64, currency string) (Quote, error) {
	if tid <= 0 {
		return Quote{}, errors.New("报价必须带有效租户")
	}
	dealID = strings.TrimSpace(dealID)
	if dealID == "" {
		return Quote{}, errors.New("商机标识不能为空")
	}
	if totalCents < 0 {
		return Quote{}, errors.New("报价金额不能为负")
	}
	if strings.TrimSpace(currency) == "" {
		currency = "CNY"
	}
	id, err := idgen.NewCode("qt_")
	if err != nil {
		return Quote{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	q := Quote{
		ID: id, TenantID: tid, DealID: dealID, TotalCents: totalCents,
		Currency: currency, Status: QuoteStatusDraft, CreatedAt: now, UpdatedAt: now,
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO deal_quotes
		 (id, tenant_id, deal_id, total_cents, currency, status, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		q.ID, q.TenantID, q.DealID, q.TotalCents, q.Currency, q.Status, q.CreatedAt, q.UpdatedAt)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return Quote{}, ErrQuoteOneOpenPerDeal
		}
		return Quote{}, fmt.Errorf("报价建档失败: %w", err)
	}
	return q, nil
}

// AcceptQuote 把一张在途报价流转为 accepted（只允许 draft／sent 起跳）。
// 参数：tid=租户 ID；quoteID=报价 ID；返回错误（跨租户／已流转一律 ErrQuoteNotFound）。
func (s *Store) AcceptQuote(tid int64, quoteID string) error {
	if tid <= 0 || strings.TrimSpace(quoteID) == "" {
		return ErrQuoteNotFound
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE deal_quotes SET status=?, updated_at=?
		 WHERE id=? AND tenant_id=? AND status IN ('draft','sent')`,
		QuoteStatusAccepted, now, quoteID, tid)
	if err != nil {
		return fmt.Errorf("报价流转失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("报价流转读数失败: %w", err)
	}
	if n == 0 {
		return ErrQuoteNotFound
	}
	return nil
}

// GetQuote 按 (租户, 报价 ID) 读一行报价。
// 参数：tid=租户 ID；quoteID=报价 ID；返回报价行与错误。
func (s *Store) GetQuote(tid int64, quoteID string) (Quote, error) {
	var q Quote
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT id, tenant_id, deal_id, total_cents, currency, status, created_at, updated_at
		 FROM deal_quotes WHERE id=? AND tenant_id=?`,
		quoteID, tid).Scan(&q.ID, &q.TenantID, &q.DealID, &q.TotalCents,
		&q.Currency, &q.Status, &q.CreatedAt, &q.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Quote{}, ErrQuoteNotFound
	}
	if err != nil {
		return Quote{}, fmt.Errorf("读取报价失败: %w", err)
	}
	return q, nil
}
