// ============ reconcile.go · 职责说明 ============
// store 包三表勾稽数据源（★ F9 对账视图）：orders ↔ payments ↔（余额/退款流水）。
// 只做数据装载与内存勾稽——规则演进不动 SQL；数据量为「窗口内订单」级（可控）。
// =============================================
package store

import (
	"strings"
	"time"

	"translator/internal/db"
)

// PaymentRow 支付/退款流水行（payments 表投影视角）。
type PaymentRow struct {
	ID           int64  `json:"id"`
	OrderID      int64  `json:"order_id"`
	TenantID     int64  `json:"tenant_id"`
	AmountTokens int64  `json:"amount_tokens"`
	AmountFen    int64  `json:"amount_fen"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

// ReconcileLoad 装载窗口内订单与流水（days 日；按日期前缀过滤，UTC/本地偏移容忍 ±1 天）。
func (s *Store) ReconcileLoad(days int) ([]*Order, []PaymentRow, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	orders, err := s.ListOrders(0) // 全平台（超管对账视角）
	if err != nil {
		return nil, nil, err
	}
	cut := dateNDaysAgo(days)
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, order_id, tenant_id, amount_tokens, COALESCE(amount_fen,0), status, COALESCE(created_at,'') FROM payments WHERE substr(created_at,1,10)>=? ORDER BY id DESC LIMIT 20000", cut)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var pays []PaymentRow
	for rows.Next() {
		var p PaymentRow
		if rows.Scan(&p.ID, &p.OrderID, &p.TenantID, &p.AmountTokens, &p.AmountFen, &p.Status, &p.CreatedAt) != nil {
			continue
		}
		pays = append(pays, p)
	}
	// 订单按窗口裁剪（created_at 前缀比较，避免时区字符串坑）
	var oO []*Order
	for _, o := range orders {
		if len(o.CreatedAt) >= 10 && strings.Compare(o.CreatedAt[:10], cut) >= 0 {
			oO = append(oO, o)
		}
	}
	return oO, pays, nil
}

// dateNDaysAgo N 天前的日期串（YYYY-MM-DD，UTC 口径）。
func dateNDaysAgo(days int) string {
	return time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
}
