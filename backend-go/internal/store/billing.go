// ============ 本文件职责中文说明 ============
// 计费域数据访问层：租户余额账本（balance_accounts）、用量明细（usage_ledger）、
// 单价表（rate_card）、充值订单（orders/payments）与发票（invoices）。
// 核心业务逻辑：充值/扣减余额（不足抛 ErrInsufficientBalance）、按单价计量并扣费、
// 订单支付确认、退款、开具发票等。
// =============================================
package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// Balance 租户余额
type Balance struct {
	ID        int64  `json:"id"`         // 余额账户主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户 ID
	Balance   int64  `json:"balance"`    // 当前剩余 token 数
	Currency  string `json:"currency"`   // 货币/计量单位（本系统为 tokens）
	UpdatedAt string `json:"updated_at"` // 最近变更时间（RFC3339 字符串）
}

// UsageRecord 用量明细
type UsageRecord struct {
	ID        int64  `json:"id"`         // 用量记录主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户 ID
	UserID    int64  `json:"user_id"`    // 发起用量的用户 ID
	TaskType  string `json:"task_type"`  // 任务类型：translate/review/evals/gate
	Provider  string `json:"provider"`   // LLM 供应商（多供应商成本核算维度）
	Model     string `json:"model"`      // 使用的模型名
	Quantity  int64  `json:"quantity"`   // 用量单位数（字符数或句数）
	UnitPrice int64  `json:"unit_price"` // 每单位价格（token）
	Cost      int64  `json:"cost"`       // 本笔总费用（扣减 token 数）
	CreatedAt string `json:"created_at"` // 用量发生时间（RFC3339 字符串）
}

// UsageLedger 用量明细记录（usage_ledger 表）
type UsageLedger struct {
	ID        int64  `json:"id"`         // 用量明细主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户 ID
	UserID    int64  `json:"user_id"`    // 发起用量的用户 ID
	TaskType  string `json:"task_type"`  // 任务类型：translate/review/evals/gate
	Provider  string `json:"provider"`   // LLM 供应商（成本核算维度）
	Model     string `json:"model"`      // 使用的模型名
	Quantity  int64  `json:"quantity"`   // 用量单位数（字符数或句数）
	UnitPrice int64  `json:"unit_price"` // 每单位价格（token）
	Cost      int64  `json:"cost"`       // 本笔总费用（扣减 token 数）
	CreatedAt string `json:"created_at"` // 用量发生时间（RFC3339 字符串）
}

// RateCard 单价表
type RateCard struct {
	ID         int64   `json:"id"`         // 单价规则主键 ID
	TaskType   string  `json:"task_type"`  // 任务类型：translate/review/evals/gate
	Lang       string  `json:"lang"`       // 目标语言（* 表示全局通用）
	Provider   string  `json:"provider"`   // 供应商（* 表示全局通用）
	UnitPrice  int64   `json:"unit_price"` // 每单位价格（token）
	Multiplier float64 `json:"multiplier"` // 高膨胀语种倍率（乘以 UnitPrice）
	UpdatedAt  string  `json:"updated_at"` // 最近更新时间（RFC3339 字符串）
}

// Order 充值订单
type Order struct {
	ID            int64   `json:"id"`             // 订单主键 ID
	TenantID      int64   `json:"tenant_id"`      // 所属租户 ID
	OrderNo       string  `json:"order_no"`       // 订单号（RO + 时间戳 + 随机后缀）
	AmountTokens  int64   `json:"amount_tokens"`  // 充值 token 数
	AmountMoney   float64 `json:"amount_money"`   // 充值金额（货币）
	Status        string  `json:"status"`         // 订单状态：pending / paid / refunded / cancelled
	PayMethod     string  `json:"pay_method"`     // 支付方式（offline 线下转账 / online 在线支付）
	Channel       string  `json:"channel"`        // 在线支付渠道（mock / wechat / alipay / manual）
	PrepayID      string  `json:"prepay_id"`      // 渠道预支付 ID（回调对账）
	QRContent     string  `json:"qr_content"`     // 收款二维码内容（在线支付）
	PackageID     int64   `json:"package_id"`     // 关联商业包 ID（订阅付费/增量包时 >0）
	ManualConfirm int     `json:"manual_confirm"` // 静态码支付人工确认标记：1=用户已点「我已付费」，待超管确认
	CreatedBy     int64   `json:"created_by"`     // 创建订单的用户 ID
	CreatedAt     string  `json:"created_at"`     // 创建时间（RFC3339 字符串）
	PaidAt        string  `json:"paid_at"`        // 支付确认时间（空表示未支付）
}

// orderCols 订单表查询列清单（统一使用，避免遗漏新增列）
const orderCols = "id, tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, prepay_id, qr_content, package_id, manual_confirm, created_by, created_at, COALESCE(paid_at,'')"

// ============ 余额 ============

// EnsureBalance 确保租户余额账户存在：不存在则创建初始为 0 的账户（幂等）。
// 参数：tid=租户 ID；返回错误（存在则直接返回 nil）。
func (s *Store) EnsureBalance(tid int64) error {
	var id int64
	err := s.db.QueryRow("SELECT id FROM balance_accounts WHERE tenant_id=?", tid).Scan(&id)
	if err == nil {
		return nil // 账户已存在
	}
	if err != sql.ErrNoRows {
		return err // 查询异常而非"无记录"
	}
	_, err = s.db.Exec("INSERT INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tid, time.Now().Format(time.RFC3339))
	return err
}

// GetBalance 查询租户余额（内部先确保账户存在）。
// 参数：tid=租户 ID；返回租户余额结构体。
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

// Charge 充值：增加租户余额（幂等，按订单触发）。
// 参数：tid=租户 ID，tokens=充值 token 数；返回错误。
func (s *Store) Charge(tid int64, tokens int64) error {
	if err := s.EnsureBalance(tid); err != nil {
		return err
	}
	// 余额累加充值 token 数
	_, err := s.db.Exec(
		"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
		tokens, time.Now().Format(time.RFC3339), tid)
	return err
}

// Deduct 扣减余额；余额不足时返回 ErrInsufficientBalance。
// 参数：tid=租户 ID，tokens=待扣减 token 数；返回错误。
var ErrInsufficientBalance = &errTxt{"余额不足"}

// Deduct 扣减租户余额：先校验余额充足，再原子扣减（单机 SQLite 保证余额不转负）。
// 参数 tid: 租户 ID；tokens: 待扣减 token 数。返回 nil 表示扣减成功，否则返回错误。
func (s *Store) Deduct(tid int64, tokens int64) error {
	if err := s.EnsureBalance(tid); err != nil {
		return err
	}
	// 先读当前余额再扣减（单机 SQLite，未用事务亦可接受，保证余额不转负）
	var bal int64
	if err := s.db.QueryRow("SELECT balance FROM balance_accounts WHERE tenant_id=?", tid).Scan(&bal); err != nil {
		return err
	}
	if bal < tokens {
		return ErrInsufficientBalance // 余额不足，拒绝扣减
	}
	_, err := s.db.Exec(
		"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=?",
		tokens, time.Now().Format(time.RFC3339), tid)
	return err
}

// ============ 用量 ============

// RecordUsage 计量一条用量（并扣减余额）。provider/model 用于多供应商成本核算。
// 参数：tid/userID=租户与用户，taskType=任务类型，provider/model=供应商与模型，quantity=用量数。
// 返回：新写入 usage_ledger 记录 ID；余额不足时返回 ErrInsufficientBalance。
func (s *Store) RecordUsage(tid, userID int64, taskType, provider, model string, quantity int64) (int64, error) {
	price, mult := s.unitPrice(taskType, provider)
	cost := int64(float64(quantity*price) * mult) // 费用 = 用量 × 单价 × 语种倍率
	if cost < 0 {
		cost = 0 // 兜底：费用不可能为负
	}
	if err := s.DeductWithGrants(tid, cost); err != nil { // ★ 双部分顺序扣减（台账→永久）
		return 0, err // 先扣余额，失败则不再落账
	}
	res, err := s.db.Exec(
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, created_at) VALUES (?,?,?,?,?,?,?,?,?)",
		tid, userID, taskType, provider, model, quantity, price, cost, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LogUsage 只记录用量、不扣余额（billing 未强制计费时用于留痕计量）。
// 参数：同 RecordUsage；仅返回错误。
func (s *Store) LogUsage(tid, userID int64, taskType, provider, model string, quantity int64) error {
	price, mult := s.unitPrice(taskType, provider)
	cost := int64(float64(quantity*price) * mult) // 费用 = 用量 × 单价 × 语种倍率
	if cost < 0 {
		cost = 0 // 兜底：费用不可能为负
	}
	_, err := s.db.Exec(
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, created_at) VALUES (?,?,?,?,?,?,?,?,?)",
		tid, userID, taskType, provider, model, quantity, price, cost, time.Now().Format(time.RFC3339))
	return err
}

// unitPrice 读取单价：优先供应商专属价，未配置回退全局 '*'。
// 参数：taskType=任务类型，provider=供应商；返回单价与倍率（无配置默认 1 / 1.0）。
func (s *Store) unitPrice(taskType, provider string) (int64, float64) {
	var price int64
	var mult float64
	// 第一次查询：任务+供应商专属价格
	err := s.db.QueryRow("SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang='*' AND provider=?", taskType, provider).
		Scan(&price, &mult)
	if err != nil {
		// 回退查询：任务全局价格（provider='*'）
		err = s.db.QueryRow("SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang='*' AND provider='*'", taskType).
			Scan(&price, &mult)
	}
	if err != nil {
		return 1, 1.0 // 都未配置则按 1 token/单位 计费
	}
	if mult == 0 {
		mult = 1.0 // 倍率 0 视为 1（防止免费乘数导致费用为 0）
	}
	return price, mult
}

// ListRateCards 返回全部单价配置（公开定价页展示用）。
// 返回: 全部 rate_card 行（按 task_type/provider/lang 排序）。
func (s *Store) ListRateCards() ([]*RateCard, error) {
	rows, err := s.db.Query("SELECT id, task_type, lang, provider, unit_price, multiplier, COALESCE(updated_at,'') FROM rate_card ORDER BY task_type, provider, lang")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RateCard
	for rows.Next() {
		var r RateCard
		if err := rows.Scan(&r.ID, &r.TaskType, &r.Lang, &r.Provider, &r.UnitPrice, &r.Multiplier, &r.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &r)
	}
	return out, nil
}

// UsageStats 租户用量汇总（按任务类型分组统计费用）。
// 参数：tid=租户 ID；返回 map[任务类型]=总费用 与 全部费用合计。
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
		out[tt] = cost // 按任务类型累计
		total += cost  // 全局合计
	}
	return out, total, nil
}

// UsageStatsByProvider 用量按供应商/模型拆分统计（多供应商成本核算）。tid<=0 时统计全平台。
// 参数：tid=租户 ID（<=0 表示全平台）；返回 map["供应商 / 模型"]=总费用。
func (s *Store) UsageStatsByProvider(tid int64) (map[string]int64, error) {
	// 把空供应商归为 global，空模型归为 ?，拼接成展示键
	q := "SELECT COALESCE(NULLIF(provider,''),'global') || ' / ' || COALESCE(NULLIF(model,''),'?'), COALESCE(SUM(cost),0) FROM usage_ledger WHERE provider!=''"
	args := []interface{}{}
	if tid > 0 {
		q += " AND tenant_id=?" // 租户过滤（tid<=0 查全平台）
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

// UsageTrend 租户按日用量趋势（最近 N 天）。
// 参数：tid=租户 ID，days=最近天数（默认 7，最大 90）。
// 返回：map[日期YYYY-MM-DD]=当日总费用。
func (s *Store) UsageTrend(tid int64, days int) (map[string]int64, error) {
	if days <= 0 || days > 90 {
		days = 7 // 非法天数收敛到 7
	}
	// 计算起始日期（不含今天，往前 days-1 天）
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
		out[day] = cost // 按日期累计
	}
	return out, nil
}

// UsageLedgerList 用量明细列表（租户隔离，分页）。
// 参数：tid=租户 ID，limit=每页条数（默认 50，最大 500），offset=偏移量。
// 返回：用量明细列表，按 ID 倒序。
func (s *Store) UsageLedgerList(tid int64, limit, offset int) ([]*UsageLedger, error) {
	if limit <= 0 || limit > 500 {
		limit = 50 // 非法 limit 收敛到 50
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
			continue // 单行解析失败跳过
		}
		out = append(out, &u)
	}
	return out, nil
}

// DailyUsage 租户当日用量总费用。
// 参数：tid=租户 ID；返回今天累计扣费 token 数。
func (s *Store) DailyUsage(tid int64) (int64, error) {
	day := time.Now().Format("2006-01-02")
	var cost int64
	// 用 LIKE 前缀匹配当日所有记录（created_at 以 日期 开头）
	err := s.db.QueryRow("SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND created_at LIKE ?", tid, day+"%").Scan(&cost)
	return cost, err
}

// UsageByUser 个人用量汇总（普通用户个人级看板）：按用户统计当日/累计费用与句数。
// 参数：tid=租户 ID，userID=用户 ID；返回累计费用、当日费用、记录条数。
func (s *Store) UsageByUser(tid, userID int64) (int64, int64, int64, error) {
	day := time.Now().Format("2006-01-02")
	var total, today, cnt int64
	err := s.db.QueryRow(
		"SELECT COALESCE(SUM(cost),0), COUNT(*) FROM usage_ledger WHERE tenant_id=? AND user_id=?", tid, userID).
		Scan(&total, &cnt)
	if err != nil {
		return 0, 0, 0, err
	}
	_ = s.db.QueryRow(
		"SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND user_id=? AND created_at LIKE ?", tid, userID, day+"%").Scan(&today)
	return total, today, cnt, nil
}

// UsageByOrg 组织用量汇总：统计指定组织及其子孙组织下全部用户的用量（组织→子组织→用户下钻）。
// 参数：tid=租户 ID，orgIDs=组织及子孙组织 ID 集合（空=租户全部用户）。
// 返回：map[用户ID]=累计费用；并携带 users 明细（在 API 层组装，此处仅聚合费用）。
func (s *Store) UsageByOrg(tid int64, orgIDs []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	q := "SELECT user_id, COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND user_id>0"
	args := []interface{}{tid}
	if len(orgIDs) > 0 {
		ph := ""
		for i := range orgIDs {
			if i > 0 {
				ph += ","
			}
			ph += "?"
			args = append(args, orgIDs[i])
		}
		// 限定组织内用户：join users 表按 org_id 过滤
		q = "SELECT l.user_id, COALESCE(SUM(l.cost),0) FROM usage_ledger l JOIN users u ON l.user_id=u.id " +
			"WHERE l.tenant_id=? AND u.org_id IN (" + ph + ") AND l.user_id>0"
	}
	q += " GROUP BY user_id"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid, cost int64
		if err := rows.Scan(&uid, &cost); err != nil {
			continue
		}
		out[uid] = cost
	}
	return out, nil
}

// CostByModel 全平台模型成本核算（超管）：按供应商/模型维度汇总成本与用量。
// 参数：无（超管看全平台）；返回 map[provider/model]=cost 与 map[provider/model]=quantity。
func (s *Store) CostByModel() (map[string]int64, map[string]int64, error) {
	costs := map[string]int64{}
	quants := map[string]int64{}
	rows, err := s.db.Query(
		"SELECT COALESCE(NULLIF(provider,''),'global'), COALESCE(NULLIF(model,''),'?'), COALESCE(SUM(cost),0), COALESCE(SUM(quantity),0) FROM usage_ledger GROUP BY provider, model")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var prov, model string
		var cost, q int64
		if err := rows.Scan(&prov, &model, &cost, &q); err != nil {
			continue
		}
		key := prov + " / " + model
		costs[key] = cost
		quants[key] = q
	}
	return costs, quants, nil
}

// ============ 订单 ============

// CreateOrder 创建充值订单（线下转账，初始状态 pending）。
// 参数：tid=租户 ID，tokens=充值 token 数，money=充值金额，createdBy=创建者 ID。
// 返回：新订单对象（含生成的订单号）。
func (s *Store) CreateOrder(tid int64, tokens int64, money float64, createdBy int64) (*Order, error) {
	return s.CreateOrderChannel(tid, tokens, money, createdBy, "offline", "")
}

// CreateOrderChannel 创建充值订单并指定支付渠道。
// 参数：tid=租户 ID，tokens=充值 token 数，money=充值金额，createdBy=创建者 ID，
// channel=支付渠道（offline / mock / wechat / alipay），qrContent=渠道二维码内容。
// 返回：新订单对象。
func (s *Store) CreateOrderChannel(tid int64, tokens int64, money float64, createdBy int64, channel, qrContent string) (*Order, error) {
	orderNo := "RO" + time.Now().Format("20060102150405") + randSuffix(4) // 生成唯一订单号
	payMethod := "offline"
	if channel != "offline" {
		payMethod = "online"
	}
	_, err := s.db.Exec(
		"INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, qr_content, created_by, created_at) VALUES (?,?,?,?, 'pending', ?, ?, ?, ?, ?)",
		tid, orderNo, tokens, money, payMethod, channel, qrContent, createdBy, time.Now().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	return s.GetOrderByOrderNo(orderNo, tid)
}

// CreatePackageOrder 创建商业包订阅订单（付费包/增量包）：金额取包售价，关联 package_id。
// 参数：tid=租户 ID，pkg=商业包对象，createdBy=创建者 ID，channel=支付渠道（mock/manual/wechat/alipay）。
// 返回：新订单对象（初始状态 pending，待支付）。
func (s *Store) CreatePackageOrder(tid int64, pkg *Package, createdBy int64, channel string) (*Order, error) {
	orderNo := "RO" + time.Now().Format("20060102150405") + randSuffix(4) // 生成唯一订单号
	_, err := s.db.Exec(
		"INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, qr_content, package_id, created_by, created_at) VALUES (?,?,0,?, 'pending', 'online', ?, '', ?, ?, ?)",
		tid, orderNo, pkg.PriceMoney, channel, pkg.ID, createdBy, time.Now().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	return s.GetOrderByOrderNo(orderNo, tid)
}

// GetOrderByOrderNo 按订单号查询订单（回调对账用，租户隔离校验）。
// 参数：orderNo=订单号，tid=租户 ID；返回订单对象。
func (s *Store) GetOrderByOrderNo(orderNo string, tid int64) (*Order, error) {
	var o Order
	err := s.db.QueryRow("SELECT "+orderCols+" FROM orders WHERE order_no=? AND tenant_id=?", orderNo, tid).
		Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// GetOrder 按 ID+租户查询订单（租户隔离校验）。
// 参数：id=订单主键 ID，tid=租户 ID；返回订单对象。
func (s *Store) GetOrder(id, tid int64) (*Order, error) {
	var o Order
	err := s.db.QueryRow("SELECT "+orderCols+" FROM orders WHERE id=? AND tenant_id=?", id, tid).
		Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListOrders 列出租户全部订单（按 ID 倒序）。
// 参数：tid=租户 ID；返回订单列表。
func (s *Store) ListOrders(tid int64) ([]*Order, error) {
	// tid<=0：跨租户全量（超管平台视角聚合）
	if tid <= 0 {
		rows, err := s.db.Query("SELECT " + orderCols + " FROM orders ORDER BY id DESC")
		return scanOrders(rows, err)
	}
	rows, err := s.db.Query("SELECT "+orderCols+" FROM orders WHERE tenant_id=? ORDER BY id DESC", tid)
	return scanOrders(rows, err)
}

// scanOrders 扫描订单行集（ListOrders 共用）。
func scanOrders(rows *sql.Rows, err error) ([]*Order, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &o)
	}
	return out, nil
}

// MarkOrderPaid 订单支付确认（线下转账 admin 手动确认 / 静态码人工确认）：置 paid、记支付流水并发放权益。
// 参数：orderID=订单主键 ID，tid=租户 ID；返回错误。
func (s *Store) MarkOrderPaid(orderID, tid int64) error {
	var tokens int64
	var pkgID int64
	var createdBy int64
	// 仅允许 pending 状态的订单被确认（防止重复支付）
	err := s.db.QueryRow("SELECT amount_tokens, package_id, COALESCE(created_by,0) FROM orders WHERE id=? AND tenant_id=? AND status='pending'", orderID, tid).Scan(&tokens, &pkgID, &createdBy)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		"UPDATE orders SET status='paid', paid_at=?, manual_confirm=0 WHERE id=? AND tenant_id=?",
		time.Now().Format(time.RFC3339), orderID, tid); err != nil {
		return err
	}
	// 写入支付流水（payments 表）
	if _, err := s.db.Exec(
		"INSERT INTO payments (order_id, tenant_id, amount_tokens, amount_money, status, created_at) VALUES (?,?,?,?, 'paid', ?)",
		orderID, tid, tokens, 0, time.Now().Format(time.RFC3339)); err != nil {
		return err
	}
	// 商业包订单（订阅付费/增量包）：按包类型分流发放（白皮书 §4.1）
	if pkgID > 0 {
		pkg, err := s.GetPackage(pkgID)
		if err != nil {
			return err
		}
		// ★ 套餐订单 amount_tokens=0，权益额度按「包内句数×换算率」折算 token
		pkgTokens := pkg.Sentences * s.TokenSentenceRate()
		// ★ 按包类型分流（白皮书 §4.1）：
		if pkg.PType == "paid" {
			// 订阅身份与句数镜像照常落租户权限（不含 token）
			if _, err := s.ApplyPaidPackageIdentity(tid, pkg); err != nil {
				return err
			}
			// 付费订阅：token 入台账，t+30 天滚动；台账失败兜底旧通道（永久余额）
			if err := s.CreateQuotaGrant(tid, "plan", pkgTokens, time.Now().Add(30*24*time.Hour), "order", orderID); err != nil {
				_ = s.Charge(tid, pkgTokens)
			}
			// ★ 邀请裂变（白皮书 §5.2）：受邀者首笔付费套餐到账→邀请者永久 token（按对去重，仅首笔；续费/充值包不触发）
			s.ReferralPaidReward(createdBy)
			return nil
		}
		if pkg.PType == "increment" {
			// 充值包：句数镜像追加 + 等值 token 入永久余额（买断无到期）
			if _, err := s.ApplyIncrementMirror(tid, pkg); err != nil {
				return err
			}
			return s.Charge(tid, pkgTokens)
		}
		_, err = s.GrantPackageSentences(tid, pkg)
		return err
	}
	// 到账：给租户充值等额 token
	return s.Charge(tid, tokens)
}

// MarkOrderManualConfirm 静态码支付人工确认：用户扫码付款后点「我已付费」，置 manual_confirm=1（待超管审核）。
// 参数：orderID=订单主键 ID，tid=租户 ID；返回错误（仅允许 manual 渠道且未支付的订单）。
func (s *Store) MarkOrderManualConfirm(orderID, tid int64) error {
	// 校验订单归属、渠道为 manual、状态仍为 pending（防止重复确认）
	var ch string
	var status string
	err := s.db.QueryRow("SELECT channel, status FROM orders WHERE id=? AND tenant_id=?", orderID, tid).Scan(&ch, &status)
	if err != nil || ch != "manual" || status != "pending" {
		return &errTxt{"订单不存在或已处理"}
	}
	_, err = s.db.Exec("UPDATE orders SET manual_confirm=1 WHERE id=? AND tenant_id=?", orderID, tid)
	return err
}

// ListManualConfirmOrders 列出待人工确认的订单（超管 Billing 面板）：manual 渠道 + manual_confirm=1 + pending。
// 参数：无（全平台）；返回订单列表（按 ID 倒序）。
func (s *Store) ListManualConfirmOrders() ([]*Order, error) {
	rows, err := s.db.Query("SELECT " + orderCols + " FROM orders WHERE channel='manual' AND manual_confirm=1 AND status='pending' ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt); err != nil {
			continue
		}
		out = append(out, &o)
	}
	return out, nil
}

// FindOrderByOrderNo 按订单号查找订单（跨租户，支付回调对账用）。
// 参数：orderNo=渠道回调的订单号；返回订单对象。
func (s *Store) FindOrderByOrderNo(orderNo string) (*Order, error) {
	var o Order
	err := s.db.QueryRow("SELECT "+orderCols+" FROM orders WHERE order_no=? LIMIT 1", orderNo).
		Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// UpdateOrderPrepay 更新订单支付凭证（prepay_id / 二维码），供下单后回填。
// 参数：orderNo=订单号，prepayID=渠道预支付 ID，qrContent=二维码内容。
func (s *Store) UpdateOrderPrepay(orderNo, prepayID, qrContent string) error {
	_, err := s.db.Exec("UPDATE orders SET prepay_id=?, qr_content=? WHERE order_no=?", prepayID, qrContent, orderNo)
	return err
}

// MarkOrderPaidByOrderNo 支付回调确认到账：按订单号置 paid 并充值（幂等）。
// 参数：orderNo=订单号；返回错误。已支付订单重复回调不重复充值。
func (s *Store) MarkOrderPaidByOrderNo(orderNo string) error {
	o, err := s.FindOrderByOrderNo(orderNo)
	if err != nil {
		return err
	}
	if o.Status == "paid" {
		return nil // 幂等：已支付直接返回
	}
	return s.MarkOrderPaid(o.ID, o.TenantID)
}

// RefundOrder 退款：订单置 refunded 并从租户余额扣除等额 token。
// 参数：orderID=订单主键 ID，tid=租户 ID；余额不足时返回 ErrInsufficientBalance。
func (s *Store) RefundOrder(orderID, tid int64) error {
	var tokens int64
	// 仅允许已支付订单退款
	err := s.db.QueryRow("SELECT amount_tokens FROM orders WHERE id=? AND tenant_id=? AND status='paid'", orderID, tid).Scan(&tokens)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec("UPDATE orders SET status='refunded' WHERE id=? AND tenant_id=?", orderID, tid); err != nil {
		return err
	}
	return s.Deduct(tid, tokens) // 扣回等额余额完成退款
}

// errTxt 自定义错误类型：仅保存一条错误消息文本。
type errTxt struct{ s string }

// Error 实现 error 接口，返回错误消息文本。
func (e *errTxt) Error() string { return e.s }

// ============ 发票 ============

// Invoice 发票
type Invoice struct {
	ID          int64   `json:"id"`           // 发票主键 ID
	TenantID    int64   `json:"tenant_id"`    // 所属租户 ID
	OrderID     int64   `json:"order_id"`     // 关联订单 ID
	InvoiceNo   string  `json:"invoice_no"`   // 发票号（INV + 时间戳 + 随机后缀）
	AmountMoney float64 `json:"amount_money"` // 开票金额
	Title       string  `json:"title"`        // 发票抬头
	TaxNo       string  `json:"tax_no"`       // 税号
	Status      string  `json:"status"`       // 发票状态：pending / issued / cancelled
	CreatedAt   string  `json:"created_at"`   // 开票时间（RFC3339 字符串）
}

// CreateInvoice 为已支付订单开具发票。
// 参数：tid=租户 ID，orderID=已支付订单 ID，title=抬头，taxNo=税号。
// 返回：新发票对象（金额取自订单 amount_money）。
func (s *Store) CreateInvoice(tid, orderID int64, title, taxNo string) (*Invoice, error) {
	var money float64
	// 仅允许对已支付订单开票，金额取订单金额
	err := s.db.QueryRow("SELECT amount_money FROM orders WHERE id=? AND tenant_id=? AND status='paid'", orderID, tid).Scan(&money)
	if err != nil {
		return nil, err
	}
	no := "INV" + time.Now().Format("20060102150405") + randSuffix(4) // 生成唯一发票号
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

// GetInvoice 按 ID+租户查询发票（租户隔离校验）。
// 参数：id=发票主键 ID，tid=租户 ID；返回发票对象。
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

// ListInvoices 列出租户全部发票（按 ID 倒序）。
// 参数：tid=租户 ID；返回发票列表。
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
			continue // 单行解析失败跳过
		}
		out = append(out, &inv)
	}
	return out, nil
}

// randSuffix 生成 n 位由大写字母和数字组成的随机后缀（用于订单号/发票号/API Key 唯一性）。
// 参数：n=随机字符个数；返回随机字符串（基于线性同余伪随机，无需 crypto/rand）。
func randSuffix(n int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := range b {
		seed = seed*6364136223846793005 + 1442695040888963407 // LCG 线性同余发生器迭代
		b[i] = letters[uint64(seed)%uint64(len(letters))]     // 取模映射到字母表
	}
	return string(b)
}

// UsageAllByUser 跨租户聚合每用户用量（超管平台视角）。
// 返回：用户 ID → 累计消耗。
func (s *Store) UsageAllByUser() (map[int64]int64, error) {
	rows, err := s.db.Query("SELECT user_id, COALESCE(SUM(quantity),0) FROM usage_ledger GROUP BY user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var uid, q int64
		if err := rows.Scan(&uid, &q); err == nil {
			out[uid] = q
		}
	}
	return out, nil
}

// ============ 商业化参数与巡检（Commit B） ============

// EnsureBillingDefaults 商业化参数默认值落库（幂等；后台面板可改）。
func (s *Store) EnsureBillingDefaults() {
	defaults := [][2]string{
		{"free_trial_tokens", "300000"},
		{"free_trial_days", "14"},
		{"order_pending_timeout_min", "15"},
		{"low_balance_alert_tokens", "100000"},
		// ★ 邀请裂变参数（白皮书 §5.2 计奖矩阵，面板可改）
		{"invite_reward_tokens", "300000"},       // 每邀 1 人·邀请者体验增量
		{"invite_extend_days", "14"},             // 每邀 1 人·邀请者时长叠加天数
		{"inviter_paid_reward_tokens", "500000"}, // 受邀者首笔付费套餐→邀请者永久 token
	}
	for _, kv := range defaults {
		s.db.Exec("INSERT INTO system_config (key,value) SELECT ?,? WHERE NOT EXISTS (SELECT 1 FROM system_config WHERE key=?)", kv[0], kv[1], kv[0])
	}
}

// CloseStalePendingOrders 关闭超时未支付订单：pending 超过 order_pending_timeout_min 自动 cancelled。
func (s *Store) CloseStalePendingOrders() int64 {
	minutes := int64(15)
	if v, _ := s.GetConfig("order_pending_timeout_min"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			minutes = x
		}
	}
	cut := time.Now().Add(-time.Duration(minutes) * time.Minute).Format(time.RFC3339)
	res, err := s.db.Exec("UPDATE orders SET status='cancelled' WHERE status='pending' AND created_at < ?", cut)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// TenantLowBalanceAlerts 低额提醒：enforced 租户剩余合计（台账+永久）低于阈值时告警（24h 去重）。
func (s *Store) TenantLowBalanceAlerts(threshold int64) {
	enforced := false
	if v, _ := s.GetConfig("billing_enforced"); v == "1" {
		enforced = true
	}
	rows, err := s.db.Query(`SELECT ba.tenant_id,
		COALESCE(ba.balance,0) + COALESCE((SELECT SUM(g.left) FROM quota_grants g
			WHERE g.tenant_id=ba.tenant_id AND g.left>0 AND g.expires_at > ?),0) AS remain
		FROM balance_accounts ba WHERE ba.tenant_id>0`,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return
	}
	defer rows.Close()
	type row struct {
		tid, remain int64
	}
	dayAgo := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	var list []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.tid, &r.remain) == nil {
			list = append(list, r)
		}
	}
	for _, r := range list {
		if r.remain >= threshold {
			continue
		}
		var cnt int
		s.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE tenant_id=? AND type='low_balance'
			AND created_at>?`, r.tid, dayAgo).Scan(&cnt)
		if cnt == 0 {
			s.CreateAlert(r.tid, "warning", "low_balance",
				fmt.Sprintf("额度即将耗尽：当前剩余 %d token，请及时充值或续订套餐", r.remain))
		}
	}
	_ = enforced // 预留：灰度期同样提醒，仅不停服
}
