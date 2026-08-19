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
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Quantity  int64  `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
	Cost      int64  `json:"cost"`
	CreatedAt string `json:"created_at"`
}

// UsageLedger 用量明细记录（usage_ledger 表）
type UsageLedger struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	UserID    int64  `json:"user_id"`
	TaskType  string `json:"task_type"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
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

// RecordUsage 计量一条用量（并扣减余额）。provider/model 用于多供应商成本核算。
func (s *Store) RecordUsage(tid, userID int64, taskType, provider, model string, quantity int64) (int64, error) {
	price, mult := s.unitPrice(taskType, provider)
	cost := int64(float64(quantity*price) * mult)
	if cost < 0 {
		cost = 0
	}
	if err := s.Deduct(tid, cost); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, created_at) VALUES (?,?,?,?,?,?,?,?,?)",
		tid, userID, taskType, provider, model, quantity, price, cost, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LogUsage 只记录用量、不扣余额（billing 未强制计费时用于留痕计量）
func (s *Store) LogUsage(tid, userID int64, taskType, provider, model string, quantity int64) error {
	price, mult := s.unitPrice(taskType, provider)
	cost := int64(float64(quantity*price) * mult)
	if cost < 0 {
		cost = 0
	}
	_, err := s.db.Exec(
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, created_at) VALUES (?,?,?,?,?,?,?,?,?)",
		tid, userID, taskType, provider, model, quantity, price, cost, time.Now().Format(time.RFC3339))
	return err
}

// unitPrice 读取单价：优先供应商专属价，未配置回退全局 '*'
func (s *Store) unitPrice(taskType, provider string) (int64, float64) {
	var price int64
	var mult float64
	err := s.db.QueryRow("SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang='*' AND provider=?", taskType, provider).
		Scan(&price, &mult)
	if err != nil {
		err = s.db.QueryRow("SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang='*' AND provider='*'", taskType).
			Scan(&price, &mult)
	}
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

// UsageStatsByProvider 用量按供应商/模型拆分（多供应商成本核算）。tid<=0 时统计全平台。
func (s *Store) UsageStatsByProvider(tid int64) (map[string]int64, error) {
	q := "SELECT COALESCE(NULLIF(provider,''),'global') || ' / ' || COALESCE(NULLIF(model,''),'?'), COALESCE(SUM(cost),0) FROM usage_ledger WHERE provider!=''"
	args := []interface{}{}
	if tid > 0 {
		q += " AND tenant_id=?"
		args = append(args, tid)
	}
	q += " GROUP BY provider, model"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var cost int64
		if err := rows.Scan(&k, &cost); err != nil {
			continue
		}
		out[k] = cost
	}
	return out, nil
}

// UsageTrend 租户按日用量趋势（最近 N 天）
func (s *Store) UsageTrend(tid int64, days int) (map[string]int64, error) {
	if days <= 0 || days > 90 {
		days = 7
	}
	start := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.Query(
		"SELECT substr(created_at,1,10) AS day, COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND created_at>=? GROUP BY day ORDER BY day",
		tid, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var day string
		var cost int64
		if err := rows.Scan(&day, &cost); err != nil {
			continue
		}
		out[day] = cost
	}
	return out, nil
}

// UsageLedgerList 用量明细（租户隔离，分页）
func (s *Store) UsageLedgerList(tid int64, limit, offset int) ([]*UsageLedger, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(
		"SELECT id, tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, created_at FROM usage_ledger WHERE tenant_id=? ORDER BY id DESC LIMIT ? OFFSET ?",
		tid, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UsageLedger
	for rows.Next() {
		var u UsageLedger
		if err := rows.Scan(&u.ID, &u.TenantID, &u.UserID, &u.TaskType, &u.Provider, &u.Model, &u.Quantity, &u.UnitPrice, &u.Cost, &u.CreatedAt); err != nil {
			continue
		}
		out = append(out, &u)
	}
	return out, nil
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

// ============ 发票 ============

// Invoice 发票
type Invoice struct {
	ID         int64   `json:"id"`
	TenantID   int64   `json:"tenant_id"`
	OrderID    int64   `json:"order_id"`
	InvoiceNo  string  `json:"invoice_no"`
	AmountMoney float64 `json:"amount_money"`
	Title      string  `json:"title"`
	TaxNo      string  `json:"tax_no"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
}

// CreateInvoice 为已支付订单开具发票
func (s *Store) CreateInvoice(tid, orderID int64, title, taxNo string) (*Invoice, error) {
	var money float64
	err := s.db.QueryRow("SELECT amount_money FROM orders WHERE id=? AND tenant_id=? AND status='paid'", orderID, tid).Scan(&money)
	if err != nil {
		return nil, err
	}
	no := "INV" + time.Now().Format("20060102150405") + randSuffix(4)
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO invoices (tenant_id, order_id, invoice_no, amount_money, title, tax_no, status, created_at) VALUES (?,?,?,?,?,?,'issued',?)",
		tid, orderID, no, money, title, taxNo, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetInvoice(id, tid)
}

// GetInvoice 查询发票
func (s *Store) GetInvoice(id, tid int64) (*Invoice, error) {
	row := s.db.QueryRow(
		"SELECT id, tenant_id, order_id, invoice_no, amount_money, COALESCE(title,''), COALESCE(tax_no,''), status, COALESCE(created_at,'') FROM invoices WHERE id=? AND tenant_id=?", id, tid)
	var inv Invoice
	err := row.Scan(&inv.ID, &inv.TenantID, &inv.OrderID, &inv.InvoiceNo, &inv.AmountMoney, &inv.Title, &inv.TaxNo, &inv.Status, &inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// ListInvoices 租户发票列表
func (s *Store) ListInvoices(tid int64) ([]*Invoice, error) {
	rows, err := s.db.Query(
		"SELECT id, tenant_id, order_id, invoice_no, amount_money, COALESCE(title,''), COALESCE(tax_no,''), status, COALESCE(created_at,'') FROM invoices WHERE tenant_id=? ORDER BY id DESC", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.ID, &inv.TenantID, &inv.OrderID, &inv.InvoiceNo, &inv.AmountMoney, &inv.Title, &inv.TaxNo, &inv.Status, &inv.CreatedAt); err != nil {
			continue
		}
		out = append(out, &inv)
	}
	return out, nil
}

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