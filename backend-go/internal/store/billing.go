package store

import (
	"database/sql"
	"time"
)

// Balance 租户余额
type Balance struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	Balance   int64  `json:"balance"`
	Currency  string `json:"currency"`
	UpdatedAt string `json:"updated_at"`
}

// UsageRecord 用量明细
type UsageRecord struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	UserID    int64  `json:"user_id"`
	TaskType  string `json:"task_type"`
	Quantity  int64  `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
	Cost      int64  `json:"cost"`
	CreatedAt string `json:"created_at"`
}

// RateCard 单价表
type RateCard struct {
	ID         int64   `json:"id"`
	TaskType   string  `json:"task_type"`
	Lang       string  `json:"lang"`
	UnitPrice  int64   `json:"unit_price"`
	Multiplier float64 `json:"multiplier"`
	UpdatedAt  string  `json:"updated_at"`
}

// Order 充值订单
type Order struct {
	ID           int64   `json:"id"`
	TenantID     int64   `json:"tenant_id"`
	OrderNo      string  `json:"order_no"`
	AmountTokens int64   `json:"amount_tokens"`
	AmountMoney  float64 `json:"amount_money"`
	Status       string  `json:"status"` // pending/paid/refunded/cancelled
	PayMethod    string  `json:"pay_method"`
	CreatedBy    int64   `json:"created_by"`
	CreatedAt    string  `json:"created_at"`
	PaidAt       string  `json:"paid_at"`
}

// ============ 余额 ============

// EnsureBalance 确保租户余额账户存在
func (s *Store) EnsureBalance(tid int64) error {
	var id int64
	err := s.db.QueryRow("SELECT id FROM balance_accounts WHERE tenant_id=?", tid).Scan(&id)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = s.db.Exec("INSERT INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tid, time.Now().Format(time.RFC3339))
	return err
}

// GetBalance 查询租户余额
func (s *Store) GetBalance(tid int64) (*Balance, error) {
	if err := s.EnsureBalance(tid); err != nil {
		return nil, err
	}
	var b Balance
	err := s.db.QueryRow("SELECT id, tenant_id, balance, currency, COALESCE(updated_at,'') FROM balance_accounts WHERE tenant_id=?", tid).
		Scan(&b.ID, &b.TenantID, &b.Balance, &b.Currency, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// Charge 充值（增加余额，幂等 by 订单）
func (s *Store) Charge(tid int64, tokens int64) error {
	if err := s.EnsureBalance(tid); err != nil {
		return err
	}
	_, err := s.db.Exec(
		"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
		tokens, time.Now().Format(time.RFC3339), tid)
	return err
}

// Deduct 扣减余额（不足时返回 ErrInsufficientBalance）
var ErrInsufficientBalance = &errTxt{"余额不足"}

func (s *Store) Deduct(tid int64, tokens int64) error {
	if err := s.EnsureBalance(tid); err != nil {
		return err
	}
	var bal int64
	if err := s.db.QueryRow("SELECT balance FROM balance_accounts WHERE tenant_id=?", tid).Scan(&bal); err != nil {
		return err
	}
	if bal < tokens {
		return ErrInsufficientBalance
	}
	_, err := s.db.Exec(
		"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=?",
		tokens, time.Now().Format(time.RFC3339), tid)
	return err
}

// ============ 用量 ============

// RecordUsage 计量一条用量（并扣减余额）
func (s *Store) RecordUsage(tid, userID int64, taskType string, quantity int64) (int64, error) {
	price, mult := s.unitPrice(taskType)
	cost := int64(float64(quantity*price) * mult)
	if cost < 0 {
		cost = 0
	}
	if err := s.Deduct(tid, cost); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, quantity, unit_price, cost, created_at) VALUES (?,?,?,?,?,?,?)",
		tid, userID, taskType, quantity, price, cost, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) unitPrice(taskType string) (int64, float64) {
	var price int64
	var mult float64
	err := s.db.QueryRow("SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang='*'", taskType).
		Scan(&price, &mult)
	if err != nil {
		return 1, 1.0
	}
	if mult == 0 {
		mult = 1.0
	}
	return price, mult
}

// UsageStats 租户用量汇总（按任务类型）
func (s *Store) UsageStats(tid int64) (map[string]int64, int64, error) {
	rows, err := s.db.Query("SELECT task_type, COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? GROUP BY task_type", tid)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := map[string]int64{}
	var total int64
	for rows.Next() {
		var tt string
		var cost int64
		if err := rows.Scan(&tt, &cost); err != nil {
			continue
		}
		out[tt] = cost
		total += cost
	}
	return out, total, nil
}

// DailyUsage 租户当日用量
func (s *Store) DailyUsage(tid int64) (int64, error) {
	day := time.Now().Format("2006-01-02")
	var cost int64
	err := s.db.QueryRow("SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND created_at LIKE ?", tid, day+"%").Scan(&cost)
	return cost, err
}

// ============ 订单 ============

// CreateOrder 创建充值订单
func (s *Store) CreateOrder(tid int64, tokens int64, money float64, createdBy int64) (*Order, error) {
	orderNo := "RO" + time.Now().Format("20060102150405") + randSuffix(4)
	res, err := s.db.Exec(
		"INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, pay_method, created_by, created_at) VALUES (?,?,?,?, 'pending', 'offline', ?, ?)",
		tid, orderNo, tokens, money, createdBy, time.Now().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetOrder(id, tid)
}

// GetOrder 查询订单
func (s *Store) GetOrder(id, tid int64) (*Order, error) {
	var o Order
	err := s.db.QueryRow("SELECT id, tenant_id, order_no, amount_tokens, amount_money, status, pay_method, created_by, created_at, COALESCE(paid_at,'') FROM orders WHERE id=? AND tenant_id=?", id, tid).
		Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.CreatedBy, &o.CreatedAt, &o.PaidAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListOrders 租户订单列表
func (s *Store) ListOrders(tid int64) ([]*Order, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, order_no, amount_tokens, amount_money, status, pay_method, created_by, created_at, COALESCE(paid_at,'') FROM orders WHERE tenant_id=? ORDER BY id DESC", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.CreatedBy, &o.CreatedAt, &o.PaidAt); err != nil {
			continue
		}
		out = append(out, &o)
	}
	return out, nil
}

// MarkOrderPaid 订单支付确认（线下转账 admin 手动确认）
func (s *Store) MarkOrderPaid(orderID, tid int64) error {
	var tokens int64
	err := s.db.QueryRow("SELECT amount_tokens FROM orders WHERE id=? AND tenant_id=? AND status='pending'", orderID, tid).Scan(&tokens)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		"UPDATE orders SET status='paid', paid_at=? WHERE id=? AND tenant_id=?",
		time.Now().Format(time.RFC3339), orderID, tid); err != nil {
		return err
	}
	if _, err := s.db.Exec(
		"INSERT INTO payments (order_id, tenant_id, amount_tokens, amount_money, status, created_at) VALUES (?,?,?,?, 'paid', ?)",
		orderID, tid, tokens, 0, time.Now().Format(time.RFC3339)); err != nil {
		return err
	}
	return s.Charge(tid, tokens)
}

// RefundOrder 退款（扣除等额余额）
func (s *Store) RefundOrder(orderID, tid int64) error {
	var tokens int64
	err := s.db.QueryRow("SELECT amount_tokens FROM orders WHERE id=? AND tenant_id=? AND status='paid'", orderID, tid).Scan(&tokens)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec("UPDATE orders SET status='refunded' WHERE id=? AND tenant_id=?", orderID, tid); err != nil {
		return err
	}
	return s.Deduct(tid, tokens)
}

type errTxt struct{ s string }

func (e *errTxt) Error() string { return e.s }

func randSuffix(n int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := range b {
		seed = seed*6364136223846793005 + 1442695040888963407
		b[i] = letters[uint64(seed)%uint64(len(letters))]
	}
	return string(b)
}