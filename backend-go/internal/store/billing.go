// ============ billing.go · 职责说明 ============
// store 包计费域数据访问层。
// 租户余额账本（balance_accounts）、用量明细（usage_ledger）、
// 单价表（rate_card）、充值订单（orders/payments）与发票（invoices）。
// 核心业务逻辑：充值/扣减余额（不足抛 ErrInsufficientBalance）、按单价计量并扣费、
// 订单支付确认、退款、开具发票等。
// =============================================
package store

import (
	cryptorand "crypto/rand"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"translator/internal/db"
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
	Provider  string `json:"provider"`   // LLM 供应商（多供应商成本核算维度）
	Model     string `json:"model"`      // 使用的模型名
	Quantity  int64  `json:"quantity"`   // 用量单位数（字符数或句数）
	UnitPrice int64  `json:"unit_price"` // 每单位价格（token）
	Cost      int64  `json:"cost"`       // 本笔总费用（扣减 token 数）
	BizKind   string `json:"biz_kind"`   // 业务形态：text=文本翻译 / file=文件翻译（空=历史数据）
	BizMode   string `json:"biz_mode"`   // 翻译模式：fast=快速 / pro=专业校对（空=历史数据）
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
//
// ★ 并发安全（2026-08-26 P0-8 止血）：由原「先查后插」两步改为单条
//
//	INSERT OR IGNORE——依赖 BalanceAccountMigrate 建立的 tenant_id 唯一索引，
//	并发首次访问不会产生重复账户行（旧行为会插入两行，导致展示与实扣不一致）。
func (s *Store) EnsureBalance(tid int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tid, time.Now().Format(time.RFC3339))
	return err
}

// BalanceAccountMigrate 余额账户表迁移（幂等，Store.New 迁移链调用，2026-08-26 P0-8 止血）：
//
//	① 先去重历史重复账户行（保留最小 id 一行）；
//	② 再建 tenant_id 唯一索引——此后 EnsureBalance 的 INSERT OR IGNORE 才有约束兜底。
//	   注意顺序不能颠倒：存量已有重复行时建唯一索引会失败。
func (s *Store) BalanceAccountMigrate() {
	// 去重：同租户多行时仅保留 id 最小的一行（历史余额取首行口径）
	db.Exec(s.db, db.CurrentDialect(), `DELETE FROM balance_accounts WHERE id NOT IN
		(SELECT MIN(id) FROM balance_accounts GROUP BY tenant_id)`)
	// 唯一索引：并发 INSERT OR IGNORE 的正确性前提
	db.Exec(s.db, db.CurrentDialect(), `CREATE UNIQUE INDEX IF NOT EXISTS idx_balance_tid ON balance_accounts(tenant_id)`)
}

// GetBalance 查询租户余额（内部先确保账户存在）。
// 参数：tid=租户 ID；返回租户余额结构体。
func (s *Store) GetBalance(tid int64) (*Balance, error) {
	if err := s.EnsureBalance(tid); err != nil {
		return nil, err
	}
	var b Balance
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id, balance, currency, COALESCE(updated_at,'') FROM balance_accounts WHERE tenant_id=?", tid).
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
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
		tokens, time.Now().Format(time.RFC3339), tid)
	return err
}

// Deduct 扣减余额；余额不足时返回 ErrInsufficientBalance。
// 参数：tid=租户 ID，tokens=待扣减 token 数；返回错误。
var ErrInsufficientBalance = &errTxt{"余额不足"}

// Deduct 扣减租户余额：余额不足时返回 ErrInsufficientBalance。
//
// Deprecated: 新代码一律使用 DeductWithGrants（双桶顺序扣减）。
//
// ★ 并发安全（2026-08-26 全仓评审 B4）：改为单条条件更新（balance>=? 守卫 +
// RowsAffected 判定）——旧实现「先 SELECT 再无条件 UPDATE」在并发下可双双通过
// 检查把余额扣成负数，原注释「单机 SQLite 未用事务亦可接受」不成立。
// 参数 tid: 租户 ID；tokens: 待扣减 token 数。返回 nil 表示扣减成功。
func (s *Store) Deduct(tid int64, tokens int64) error {
	if err := s.EnsureBalance(tid); err != nil {
		return err
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
		tokens, time.Now().Format(time.RFC3339), tid, tokens)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInsufficientBalance // 余额不足（或并发抢占失败），拒绝扣减
	}
	return nil
}

// ============ 用量 ============

// RecordUsage 计量一条用量（并扣减余额）。provider/model 用于多供应商成本核算。
// bizKind=text|file（业务形态）；bizMode=fast|pro（翻译模式）——2026-08-26 用量看板标注需求新增，
// 历史数据两列为空串，前端按「—」展示。
// 参数：tid/userID=租户与用户，taskType=任务类型，provider/model=供应商与模型，quantity=用量数。
// 返回：新写入 usage_ledger 记录 ID；余额不足时返回 ErrInsufficientBalance。
//
// ★ 整改 B4：扣减与台账落账合并同一 IMMEDIATE 事务——此前 DeductWithGrants 独立提交后
// ledger INSERT 失败即产生「扣了钱无流水」的对账单向缺口。单价预读仍在事务外完成。
func (s *Store) RecordUsage(tid, userID int64, taskType, provider, model string, quantity int64, bizKind, bizMode string) (int64, error) {
	price, mult := s.unitPrice(taskType, provider) // 事务外预读定价
	cost := int64(float64(quantity*price) * mult)  // 费用 = 用量 × 单价 × 语种倍率
	if cost < 0 {
		cost = 0 // 兜底：费用不可能为负
	}
	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ BEGIN IMMEDIATE
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := deductWithGrantsTx(tx, tid, cost); err != nil {
		return 0, err // 扣减失败（含余额不足）→ 整体回滚，不落半条
	}
	id, err := db.InsertID(tx, db.CurrentDialect(), "id",
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		tid, userID, taskType, provider, model, quantity, price, cost, bizKind, bizMode, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err // 落账失败 → 扣减一并回滚（修复「扣钱无流水」）
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.incrementDailyUsage(tid, cost) // ★ 性能优化 B6：同步累加日计数器
	return id, nil
}

// UsageBatchRow 批量计量单行输入（性能优化 B2）。
type UsageBatchRow struct {
	UserID   int64
	TaskType string
	Provider string
	Model    string
	Quantity int64
	BizKind  string
	BizMode  string
}

// RecordUsageBatch 单事务批量扣减+多行落账（性能优化 B2 核心）：把数十~上千次逐 LLM 调用的
// 写事务合并为「每租户每刷新周期一次写事务」，彻底消除并发翻译下的 SQLITE_BUSY。
// 先按各行单价/倍率汇总 cost，单次 deductWithGrantsTx 扣减全部，再循环多行 INSERT ledger；
// 余额不足整体回滚。返回首个插入行 id。
func (s *Store) RecordUsageBatch(tid int64, rows []UsageBatchRow) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	type priced struct {
		UsageBatchRow
		price int64
		cost  int64
	}
	pricedRows := make([]priced, 0, len(rows))
	var sumCost int64
	for _, r := range rows {
		price, _ := s.unitPrice(r.TaskType, r.Provider)
		// ★ P1-3：计费量以传入 quantity（已在 api 层 markupMultiplier 折算）为唯一口径，
		// 不再于此处二次乘 rate_card 倍率，杜绝「price × 1.5 × mult」的静默双算。
		// price 仅作为 unit_price 列留档，不参与扣减。
		cost := r.Quantity
		if cost < 0 {
			cost = 0
		}
		sumCost += cost
		pricedRows = append(pricedRows, priced{r, price, cost})
	}
	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ BEGIN IMMEDIATE
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := deductWithGrantsTx(tx, tid, sumCost); err != nil {
		return 0, err // 余额不足整体回滚
	}
	const insertSQL = "INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)"
	var firstID int64
	for i, pr := range pricedRows {
		id, e := db.InsertID(tx, db.CurrentDialect(), "id", insertSQL,
			tid, pr.UserID, pr.TaskType, pr.Provider, pr.Model, pr.Quantity, pr.price, pr.cost, pr.BizKind, pr.BizMode, time.Now().Format(time.RFC3339))
		if e != nil {
			return 0, e
		}
		if i == 0 {
			firstID = id
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.incrementDailyUsage(tid, sumCost) // ★ 性能优化 B6：同步累加日计数器
	return firstID, nil
}

// LogUsageBatch 仅记录用量、不扣余额的批量版（billing 未强制计费时用于留痕计量）。
// 单事务多行 INSERT。
func (s *Store) LogUsageBatch(tid int64, rows []UsageBatchRow) error {
	if len(rows) == 0 {
		return nil
	}
	const insertSQL = "INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)"
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sumCost int64
	for _, r := range rows {
		price, _ := s.unitPrice(r.TaskType, r.Provider)
		// ★ P1-3：与 RecordUsageBatch 同口径，quantity 即计费量，price 仅留档。
		cost := r.Quantity
		if cost < 0 {
			cost = 0
		}
		if _, e := db.Exec(tx, db.CurrentDialect(), insertSQL,
			tid, r.UserID, r.TaskType, r.Provider, r.Model, r.Quantity, price, cost, r.BizKind, r.BizMode, time.Now().Format(time.RFC3339)); e != nil {
			return e
		}
		sumCost += cost
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.incrementDailyUsage(tid, sumCost) // ★ 性能优化 B6：同步累加日计数器
	return nil
}

// LogUsage 只记录用量、不扣余额（billing 未强制计费时用于留痕计量）。
// 参数：同 RecordUsage；仅返回错误。
func (s *Store) LogUsage(tid, userID int64, taskType, provider, model string, quantity int64, bizKind, bizMode string) error {
	price, mult := s.unitPrice(taskType, provider)
	cost := int64(float64(quantity*price) * mult) // 费用 = 用量 × 单价 × 语种倍率
	if cost < 0 {
		cost = 0 // 兜底：费用不可能为负
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		tid, userID, taskType, provider, model, quantity, price, cost, bizKind, bizMode, time.Now().Format(time.RFC3339))
	if err == nil {
		s.incrementDailyUsage(tid, cost) // ★ 性能优化 B6：同步累加日计数器
	}
	return err
}

// unitPrice 读取单价：优先供应商专属价，未配置回退全局 '*'。
// 参数：taskType=任务类型，provider=供应商；返回单价与倍率（无配置默认 1 / 1.0）。
func (s *Store) unitPrice(taskType, provider string) (int64, float64) {
	var price int64
	var mult float64
	// 第一次查询：任务+供应商专属价格
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang='*' AND provider=?", taskType, provider).
		Scan(&price, &mult)
	if err != nil {
		// 回退查询：任务全局价格（provider='*'）
		err = db.QueryRow(s.db, db.CurrentDialect(), "SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang='*' AND provider='*'", taskType).
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
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT id, task_type, lang, provider, unit_price, multiplier, COALESCE(updated_at,'') FROM rate_card ORDER BY task_type, provider, lang")
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
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT task_type, COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? GROUP BY task_type", tid)
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
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
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
	rows, err := db.Query(s.db, db.CurrentDialect(),
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
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, COALESCE(biz_kind,''), COALESCE(biz_mode,''), created_at FROM usage_ledger WHERE tenant_id=? ORDER BY id DESC LIMIT ? OFFSET ?",
		tid, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UsageLedger
	for rows.Next() {
		var u UsageLedger
		if err := rows.Scan(&u.ID, &u.TenantID, &u.UserID, &u.TaskType, &u.Provider, &u.Model, &u.Quantity, &u.UnitPrice, &u.Cost, &u.BizKind, &u.BizMode, &u.CreatedAt); err != nil {
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
	// ★ 性能优化 B6：优先读日计数器表（O(1) 命中主键），避免每次翻译请求都对 usage_ledger
	//   做 created_at LIKE 全表扫描（gateUsage→CheckDailyQuota 每请求一次）。
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(SUM(total),0) FROM usage_daily WHERE tenant_id=? AND day=?", tid, day).Scan(&cost); err == nil {
		return cost, nil
	}
	// 兜底（表缺失/无当日行）：回退 ledger 当日 LIKE 扫描
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND created_at LIKE ?", tid, day+"%").Scan(&cost)
	return cost, err
}

// incrementDailyUsage 落账时增量更新日计数器（性能优化 B6）。
func (s *Store) incrementDailyUsage(tid, amount int64) {
	if amount <= 0 {
		return
	}
	day := time.Now().Format("2006-01-02")
	_, _ = db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO usage_daily (tenant_id, day, total) VALUES (?,?,?)
		 ON CONFLICT(tenant_id, day) DO UPDATE SET total=usage_daily.total+?`,
		tid, day, amount, amount)
}

// UsageByUser 个人用量汇总（普通用户个人级看板）：按用户统计区间/累计费用与句数。
// 参数：tid=租户 ID，userID=用户 ID，from/to=日期区间（YYYY-MM-DD；均空=累计+当日口径，
// from 缺省=to、to 缺省=from；from==to 即单日查询）。
// 返回累计费用、当日费用、记录条数（区间查询时 total 与 today 均为区间值）。
func (s *Store) UsageByUser(tid, userID int64, from, to string) (int64, int64, int64, error) {
	var total, today, cnt int64
	// 区间口径（from/to 任一非空）：total=today=区间值
	if pred, args := usageDatePred(from, to); args != nil {
		// 按租户+用户+created_at 区间聚合费用与笔数
		err := db.QueryRow(s.db, db.CurrentDialect(),
			"SELECT COALESCE(SUM(cost),0), COUNT(*) FROM usage_ledger WHERE tenant_id=? AND user_id=? AND created_at "+pred,
			append([]interface{}{tid, userID}, args...)...).Scan(&total, &cnt)
		if err != nil {
			return 0, 0, 0, err
		}
		return total, total, cnt, nil
	}
	// 全部时间口径：total=全量，today=当日
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(SUM(cost),0), COUNT(*) FROM usage_ledger WHERE tenant_id=? AND user_id=?", tid, userID).
		Scan(&total, &cnt)
	if err != nil {
		return 0, 0, 0, err
	}
	// 当日费用：created_at 前缀匹配今天
	_ = db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND user_id=? AND created_at LIKE ?",
		tid, userID, time.Now().Format("2006-01-02")+"%").Scan(&today)
	return total, today, cnt, nil
}

// usageDatePred 生成 usage_ledger.created_at（RFC3339 text）的区间过滤谓词与参数。
// 参数 from/to: YYYY-MM-DD；均空返回 nil（=不过滤全部时间）；仅给一个视同单日/单边。
// 返回谓词（不含列名，如 `>= ? AND created_at < ?`…注意由调用方拼到 created_at 之后）与参数。
func usageDatePred(from, to string) (string, []interface{}) {
	// 仅给一端时补齐：from 缺省=to、to 缺省=from（视同单日）
	if from == "" {
		from = to
	}
	if to == "" {
		to = from
	}
	// 两端均空：不过滤全部时间
	if from == "" {
		return "", nil
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		// 非法日期回退单日前缀匹配
		return "LIKE ?", []interface{}{to + "%"}
	}
	// 时间上界=to 次日零点；RFC3339 文本字典序天然可比（前 10 位即日期）
	endEx := end.AddDate(0, 0, 1).Format("2006-01-02")
	return ">= ? AND created_at < ?", []interface{}{from, endEx}
}

// UsageByOrg 组织用量汇总：统计指定组织及其子孙组织下全部用户的用量（组织→子组织→用户下钻）。
// 参数：tid=租户 ID，orgIDs=组织及子孙组织 ID 集合（空=租户全部用户），from/to=日期区间
// （YYYY-MM-DD；均空=全部时间，仅给一个视同单日/单边）。
// 返回：map[用户ID]=费用；并携带 users 明细（在 API 层组装，此处仅聚合费用）。
// ★ 2026-09-05 修复：不再过滤 l.user_id>0——系统/未登录任务（user_id=0）的用量也归入区间口径，
//   否则仅含后台任务的日期（如全站批量 LLM 调用）按日查询恒为 0。user_id=0 由 API 层单独归一行。
func (s *Store) UsageByOrg(tid int64, orgIDs []int64, from, to string) (map[int64]int64, error) {
	out := map[int64]int64{}
	// 基础 FROM：仅 usage_ledger 自身
	selectFrom := "FROM usage_ledger l"
	// 条件积累：租户恒等过滤为第一项
	whereClauses := []string{"l.tenant_id=?"}
	args := []interface{}{tid}
	// 组织过滤占位符：org_id IN (...)
	filters := []string{}
	for _, oID := range orgIDs {
		filters = append(filters, "?")
		args = append(args, oID)
	}
	if len(filters) > 0 {
		// 限定组织内用户：join users 表按 org_id 过滤
		selectFrom = "FROM usage_ledger l JOIN users u ON l.user_id=u.id"
		whereClauses = append(whereClauses, "u.org_id IN ("+strings.Join(filters, ",")+")")
	}
	// 指定日期区间：追加 created_at 区间谓词（空=全部时间，含 user_id=0 系统任务）
	if pred, cargs := usageDatePred(from, to); cargs != nil {
		whereClauses = append(whereClauses, "l.created_at "+pred)
		args = append(args, cargs...)
	}
	q := "SELECT l.user_id, COALESCE(SUM(l.cost),0) " + selectFrom + " WHERE " +
		strings.Join(whereClauses, " AND ") + " GROUP BY l.user_id"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
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
	rows, err := db.Query(s.db, db.CurrentDialect(),
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
	orderNo := fmt.Sprintf("T%d-RO%s%s", tid, time.Now().Format("20060102150405"), randSuffix(4)) // 生成唯一订单号
	payMethod := "offline"
	if channel != "offline" {
		payMethod = "online"
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, qr_content, created_by, created_at) VALUES (?,?,?,?, 'pending', ?, ?, ?, ?, ?)",
		tid, orderNo, tokens, money, payMethod, channel, qrContent, createdBy, time.Now().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	return s.GetOrderByOrderNo(orderNo, tid)
}

// CreatePackageOrder 创建商业包订阅订单（付费包/增量包）：金额取包售价，关联 package_id。
// 费用一律以 token 口径计：下单时把「句数」按折算率一次性折算为 token 数写入 amount_tokens，
// 此后（支付发放 / 退款核算）一律以该 token 数为唯一事实源，句数仅作业务展示不计入计算。
// 参数：tid=租户 ID，pkg=商业包对象，createdBy=创建者 ID，channel=支付渠道（mock/manual/wechat/alipay）。
// 返回：新订单对象（初始状态 pending，待支付）。
func (s *Store) CreatePackageOrder(tid int64, pkg *Package, createdBy int64, channel string) (*Order, error) {
	orderNo := fmt.Sprintf("T%d-RO%s%s", tid, time.Now().Format("20060102150405"), randSuffix(4)) // 生成唯一订单号
	// ★ 句→token 仅在此折算一次，并统一乘以后台可配的成本均摊系数（billing_markup_multiplier），
	// 与扣费侧（用量实时计量的 billed = total × markup）共用同一 token 单位，保证「1 入账 token = 1 扣费 token」。
	tokenAmt := int64(float64(pkg.Sentences*s.TokenSentenceRate()) * s.MarkupMultiplier())
	if tokenAmt < 0 {
		tokenAmt = 0
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, qr_content, package_id, created_by, created_at) VALUES (?,?,?,?, 'pending', 'online', ?, '', ?, ?, ?)",
		tid, orderNo, tokenAmt, pkg.PriceMoney, channel, pkg.ID, createdBy, time.Now().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	return s.GetOrderByOrderNo(orderNo, tid)
}

// GetOrderByOrderNo 按订单号查询订单（回调对账用，租户隔离校验）。
// 参数：orderNo=订单号，tid=租户 ID；返回订单对象。
func (s *Store) GetOrderByOrderNo(orderNo string, tid int64) (*Order, error) {
	var o Order
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+orderCols+" FROM orders WHERE order_no=? AND tenant_id=?", orderNo, tid).
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
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+orderCols+" FROM orders WHERE id=? AND tenant_id=?", id, tid).
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
		rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+orderCols+" FROM orders ORDER BY id DESC")
		return scanOrders(rows, err)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+orderCols+" FROM orders WHERE tenant_id=? ORDER BY id DESC", tid)
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

// PriceFenPerToken 充值定价换算（分/token）：system_config price_fen_per_token。
// 默认 10 = 现网实际执行口径（回调核对/payments 流水历史均按 tokens×10 分），
// 面板可调；下单回填、回调金额核对、支付流水的单一事实源（评审整改 B1）。
func (s *Store) PriceFenPerToken() int64 {
	if v, _ := s.GetConfig("price_fen_per_token"); v != "" {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil && n > 0 {
			return n
		}
	}
	return 10
}

// UpdateOrderMoney 回填订单应收金额（元）——下单时按定价换算落库，
// 作为回调核对、payments 流水与发票开具的统一取数来源（评审整改 B1）。
func (s *Store) UpdateOrderMoney(orderNo string, money float64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE orders SET amount_money=? WHERE order_no=?", money, orderNo)
	return err
}

// orderMoneyBackfill 存量 pending 充值单应收回填（幂等，Store.New 迁移链调用，评审整改 B1）：
// 历史在线充值单 amount_money 恒 0，导致回调核对兜底与开票金额失真；
// 仅补 pending（paid 单以已发生的流水为准，不改历史）。
func (s *Store) orderMoneyBackfill() {
	rate := s.PriceFenPerToken()
	db.Exec(s.db, db.CurrentDialect(), `UPDATE orders SET amount_money=ROUND(amount_tokens * ? / 100.0, 2)
		WHERE status='pending' AND package_id=0 AND COALESCE(amount_money,0)=0 AND amount_tokens>0`,
		float64(rate))
}

// PackageOrderTokenBackfill 存量商业包订单 token 口径回填（幂等，Store.New 迁移链调用）：
// 历史 CreatePackageOrder 将 amount_tokens 存为 0，实际 token 数需由 pkg.Sentences×折算率得出；
// 为贯彻「费用一律 token 口径、句数不参与运行期计算」，此处把存量包订单的 amount_tokens 一次性补全，
// 此后支付发放（MarkOrderPaid）与退款核算（RefundOrder）均直接采用该 token 数，不再以句数折算。
func (s *Store) PackageOrderTokenBackfill() {
	rate := s.TokenSentenceRate()
	if rate <= 0 {
		rate = 500
	}
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT id, package_id FROM orders WHERE package_id>0 AND amount_tokens=0")
	if err != nil {
		return
	}
	defer rows.Close()
	type rec struct{ id, pkg int64 }
	var pending []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.pkg); err == nil {
			pending = append(pending, r)
		}
	}
	for _, r := range pending {
		p, e := s.GetPackage(r.pkg)
		if e != nil {
			continue
		}
		tok := p.Sentences * rate
		db.Exec(s.db, db.CurrentDialect(), "UPDATE orders SET amount_tokens=? WHERE id=?", tok, r.id)
	}
}

// ===== 订单确认事务内原语（★ 整改 B2）=====
// 约束：以下 *_Tx 函数只能接收 MarkOrderPaid 持有的 IMMEDIATE 事务连接；
// 严禁在事务内调用任何 s.db 直连方法（SQLite 单写者下第二连接写入必撞 busy_timeout，
// UAT-2 已有事故先例）。定价/套餐等只读预读一律在 Begin 之前完成。

// ensureBalanceTx 确保余额账户行存在（tx 版 EnsureBalance，INSERT OR IGNORE 依赖唯一索引）。
func ensureBalanceTx(tx *sql.Tx, tid int64) error {
	_, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tid, time.Now().Format(time.RFC3339))
	return err
}

// chargePermanentTx 永久余额入账（tx 版 Charge）。
func chargePermanentTx(tx *sql.Tx, tid int64, tokens int64) error {
	if tokens <= 0 {
		return nil
	}
	if err := ensureBalanceTx(tx, tid); err != nil {
		return err
	}
	_, err := db.Exec(tx, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
		tokens, time.Now().Format(time.RFC3339), tid)
	return err
}

// createQuotaGrantTx 台账发放（tx 版 CreateQuotaGrant）。
func createQuotaGrantTx(tx *sql.Tx, tid int64, kind string, total int64, expires time.Time, source string, refID int64) error {
	_, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT INTO quota_grants (tenant_id, kind, total, \"left\", expires_at, source, ref_id, created_at) VALUES (?,?,?,?,?,?,?,?)",
		tid, kind, total, total, expires.UTC().Format(time.RFC3339), source, refID, time.Now().Format(time.RFC3339))
	return err
}

// applyIncrementMirrorTx 增量包句数镜像追加（tx 版 ApplyIncrementMirror，json_set 原子自增）。
func applyIncrementMirrorTx(tx *sql.Tx, tid int64, sentences int64) error {
	if sentences <= 0 {
		return nil
	}
	_, err := db.Exec(tx, db.CurrentDialect(),
		"UPDATE tenants SET permissions=json_set(COALESCE(permissions,'{}'), "+
			"'$.sentence_balance', COALESCE(json_extract(permissions,'$.sentence_balance'),0)+?), updated_at=? WHERE id=?",
		sentences, time.Now().Format(time.RFC3339), tid)
	return err
}

// MarkOrderPaid 订单支付确认（线下转账 admin 手动确认 / 静态码人工确认）：置 paid、记支付流水并发放权益。
// 参数：orderID=订单主键 ID，tid=租户 ID；返回错误。
//
// ★ 整改 B2（2026-08-26）：全链路单 IMMEDIATE 事务化——此前「条件抢占→流水→发放」
// 三段各自自动提交，任一中途失败即产生不可重试的半完成态：
//   - 置 paid 后 GetPackage 失败 ⇒ 收钱零发放且 MarkOrderPaidByOrderNo 幂等早退无法补发；
//   - 台账失败兜底 Charge 的错误被 `_` 丢弃 ⇒ 静默零到账；
//   - payments 先写、权益后发 ⇒ 「有流水无权益」。
//
// 现在：① 定价/套餐等只读在事务外预读（预读失败时订单仍 pending 可重试）；
// ② 抢占/流水/身份/台账/余额全部同一事务，全有或全无；③ 邀请付费奖励移至提交后执行。
//
// 并发幂等语义不变：带 status='pending' 条件的单条 UPDATE 以 RowsAffected 判定抢占权，
// 并发双请求仅一笔生效。
func (s *Store) MarkOrderPaid(orderID, tid int64) error {
	nowStr := time.Now().Format(time.RFC3339)
	// ① 事务外预读（失败不产生任何写副作用）
	var tokens, pkgID, createdBy int64
	var money float64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT amount_tokens, package_id, COALESCE(created_by,0), COALESCE(amount_money,0) FROM orders WHERE id=? AND tenant_id=?",
		orderID, tid).Scan(&tokens, &pkgID, &createdBy, &money); err != nil {
		return &errTxt{"订单不存在"}
	}
	sentenceRate := s.TokenSentenceRate()
	priceFen := s.PriceFenPerToken()
	pkgTokens := int64(0)
	pType := ""
	pkgSentences := int64(0)
	pkgCode := ""
	pkgDays := 0
	if pkgID > 0 {
		p, err := s.GetPackage(pkgID)
		if err != nil {
			return fmt.Errorf("套餐缺失(order=%d pkg=%d)：%w；订单保持待支付可重试", orderID, pkgID, err)
		}
		pType = p.PType
		pkgSentences = p.Sentences
		pkgCode = p.Code
		pkgDays = p.DurationDays
		// ★ token 口径：实际入账 token 数一律以订单 amount_tokens（下单时由句数一次性折算/迁移回填）为准，
		// 不再以 pkgSentences*sentenceRate 在发放路径二次折算。句数仅用于 SentenceBalance 展示镜像。
		pkgTokens = tokens
		if pkgTokens == 0 {
			// 兜底：存量未回填订单，按句数×折算率×均摊系数（与新建订单一致口径）发放
			pkgTokens = int64(float64(pkgSentences*s.TokenSentenceRate()) * s.MarkupMultiplier())
		}
	}
	// 应收金额转分：套餐单取包售价，纯充值单按 tokens×定价兜底
	payFen := int64(money*100 + 0.5)
	if payFen <= 0 {
		payFen = tokens * priceFen
	}
	// ② 单事务：确认权抢占 → 支付流水 → 权益发放（全有或全无）
	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ BEGIN IMMEDIATE
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := db.Exec(tx, db.CurrentDialect(),
		"UPDATE orders SET status='paid', paid_at=?, manual_confirm=0 WHERE id=? AND tenant_id=? AND status='pending'",
		nowStr, orderID, tid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &errTxt{"订单不存在或已处理"} // 幂等：重复调用/并发双回调仅一笔生效
	}
	// 支付流水与权益同事务：消除「有流水无权益」中间态（整改 B2）
	if _, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT INTO payments (order_id, tenant_id, amount_tokens, amount_money, status, created_at) VALUES (?,?,?,?, 'paid', ?)",
		orderID, tid, tokens, payFen, nowStr); err != nil {
		return err
	}
	// 按订单类型分流发放权益：纯充值 / 订阅付费 / 增量包 / 其他（免费体验等）
	switch {
	case pkgID == 0:
		// 纯充值单：token 入永久余额
		if err := chargePermanentTx(tx, tid, tokens); err != nil {
			return err
		}
	case pType == "paid":
		// ★ 订阅付费包（白皮书 §4.1）：订阅身份+句数镜像照常落租户权限（不含 token 入余额），
		//   token 走 t+30 天滚动台账；台账行 ref_id 关联本订单供退款精确作废
		perms, gerr := getTenantPermsTx(tx, tid)
		if gerr != nil {
			return gerr
		}
		perms.PackageCode = pkgCode
		perms.SubscribedAt = nowStr
		perms.SentenceBalance += pkgSentences
		if pkgDays > 0 {
			perms.PackageExpires = time.Now().AddDate(0, 0, pkgDays).Format(time.RFC3339)
		} else {
			perms.PackageExpires = ""
		}
		perms.NotifiedExp7 = false
		perms.NotifiedExp1 = false
		if serr := saveTenantPermsTx(tx, tid, perms); serr != nil {
			return serr
		}
		if cerr := createQuotaGrantTx(tx, tid, "plan", pkgTokens, time.Now().Add(30*24*time.Hour), "order", orderID); cerr != nil {
			return cerr
		}
	case pType == "increment":
		// 充值包：句数镜像追加（json_set 原子）+ token 入永久余额（买断无到期）
		if merr := applyIncrementMirrorTx(tx, tid, pkgSentences); merr != nil {
			return merr
		}
		if cerr := chargePermanentTx(tx, tid, pkgTokens); cerr != nil {
			return cerr
		}
	default:
		// 免费体验包等其他类型：与 GrantPackageSentences 同语义——句数镜像 + 订阅身份 + 折算入账
		perms, gerr := getTenantPermsTx(tx, tid)
		if gerr != nil {
			return gerr
		}
		perms.PackageCode = pkgCode
		perms.SubscribedAt = nowStr
		perms.SentenceBalance += pkgSentences
		if pkgDays > 0 {
			perms.PackageExpires = time.Now().AddDate(0, 0, pkgDays).Format(time.RFC3339)
		} else {
			perms.PackageExpires = ""
		}
		perms.NotifiedExp7 = false
		perms.NotifiedExp1 = false
		if serr := saveTenantPermsTx(tx, tid, perms); serr != nil {
			return serr
		}
		if cerr := chargePermanentTx(tx, tid, pkgSentences*sentenceRate); cerr != nil {
			return cerr
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// ③ 提交后副作用：邀请裂变「受邀人首笔付费→邀请者永久 token」（幂等按对去重；
	//    失败仅损失一次奖励，不影响本单权益到账，故置于事务外）
	if createdBy > 0 && pType == "paid" {
		s.ReferralPaidReward(createdBy)
	}
	return nil
}

// MarkOrderManualConfirm 静态码支付人工确认：用户扫码付款后点「我已付费」，置 manual_confirm=1（待超管审核）。
// 参数：orderID=订单主键 ID，tid=租户 ID；返回错误（仅允许 manual 渠道且未支付的订单）。
//
// ★ 原子化（2026-08-26 P0-7 止血）：UPDATE 携带 manual_confirm=0 AND status='pending'
//
//	条件并以 RowsAffected 判定，消除「查询+更新」两步间被并发重复确认的窗口。
func (s *Store) MarkOrderManualConfirm(orderID, tid int64) error {
	// 条件更新：仅 manual 渠道、未确认、未支付的订单可被标记
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE orders SET manual_confirm=1 WHERE id=? AND tenant_id=? AND channel='manual' AND manual_confirm=0 AND status='pending'",
		orderID, tid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &errTxt{"订单不存在或已处理"}
	}
	return nil
}

// ListManualConfirmOrders 列出待人工确认的订单（超管 Billing 面板）：manual 渠道 + manual_confirm=1 + pending。
// 参数：无（全平台）；返回订单列表（按 ID 倒序）。
func (s *Store) ListManualConfirmOrders() ([]*Order, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+orderCols+" FROM orders WHERE channel='manual' AND manual_confirm=1 AND status='pending' ORDER BY id DESC")
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
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+orderCols+" FROM orders WHERE order_no=? LIMIT 1", orderNo).
		Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// UpdateOrderPrepay 更新订单支付凭证（prepay_id / 二维码），供下单后回填。
// 参数：orderNo=订单号，prepayID=渠道预支付 ID，qrContent=二维码内容。
func (s *Store) UpdateOrderPrepay(orderNo, prepayID, qrContent string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE orders SET prepay_id=?, qr_content=? WHERE order_no=?", prepayID, qrContent, orderNo)
	return err
}

// MarkOrderPaidByOrderNo 支付回调确认到账：按订单号置 paid 并充值（幂等）。
// 参数：orderNo=订单号；返回错误。已支付订单重复回调不重复充值。
// 幂等实现说明（2026-08-26 P0-5）：不再依赖此处 status 早退，统一由
// MarkOrderPaid 内部「条件更新 + RowsAffected」保证——并发双回调仅一笔生效。
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

// RefundOrder 退款（★ 2026-08-26 口径定稿·决策人拍板）：仅退未消耗部分，按消耗比例折算。
//
//	剩余率 r = 剩余token / 发放总量（clamp [0,1]）
//	  ① 订阅单(package ptype=paid)：剩余 = SUM(quota_grants.left WHERE source='order'
//	     AND ref_id=本订单 AND kind='plan')——台账天然携带 per-order 剩余，精确；
//	  ② 非订阅单(纯充值/increment)：剩余 = 发放总量 − 该租户自订单 paid_at 起
//	     usage_ledger.quantity 合计。⚠️ 近似口径：usage_ledger 为租户级流水，跨订单混池，
//	     折算结果供商业折让使用；paid_at 缺失的历史单按未消耗处理。
//	应退金额 = ROUND(amount_money × r, 2)（r≤0 仅作废权益不退款）；
//	权益收回：
//	  订阅单 → 作废本单全部剩余台账行（left=0，即收回「剩余」）；
//	  非订阅单 → 从永久余额守卫式扣回 min(剩余, 当前余额)；余额不足的差额不再整体拒绝
//	  退款（修复旧实现与「已消耗不追讨」注释的矛盾），差额 CreateAlert 转人工核对。
//	orders.refund_money 列记录实退金额（审计/对账）；订阅身份清理由 API 层在退款成功后执行。
//	费用一律以 token 口径计：granted 直接取订单入账 token 数（amount_tokens），
//	句数折算已在下单时一次性完成，退款核算不再以句数参与计算。
func (s *Store) RefundOrder(orderID, tid int64) error {
	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ BEGIN IMMEDIATE
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 读取订单（仅已支付可退）：包类型/发放总量/应收金额/支付时间
	var pkgID, tokens int64
	var money float64
	var paidAt string
	if err := db.QueryRow(tx, db.CurrentDialect(),
		"SELECT package_id, amount_tokens, COALESCE(amount_money,0), COALESCE(paid_at,'') FROM orders WHERE id=? AND tenant_id=? AND status='paid'",
		orderID, tid).Scan(&pkgID, &tokens, &money, &paidAt); err != nil {
		if err == sql.ErrNoRows {
			return &errTxt{"订单不存在或状态不允许退款"}
		}
		return err
	}
	// 反推发放总量与剩余量（均为 token 口径；granted 直接取订单入账 token 数）
	granted := tokens
	remain := tokens // 非订阅单默认全额未耗（paid_at 缺失时保守按全退）
	isSub := false
	if pkgID > 0 {
		var ptype string
		if err := db.QueryRow(tx, db.CurrentDialect(), "SELECT ptype FROM packages WHERE id=?", pkgID).Scan(&ptype); err != nil {
			return err
		}
		// granted 已由 tokens（=订单入账 token 数）给出；句数不参与计算
		if ptype == "paid" {
			isSub = true
			// 订阅单：台账剩余精确可查
			if err := db.QueryRow(tx, db.CurrentDialect(),
				"SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=? AND source='order' AND ref_id=? AND kind='plan' AND \"left\">0",
				tid, orderID).Scan(&remain); err != nil {
				return err
			}
		} else if paidAt != "" {
			// 充值/increment：自支付时刻起的租户级消耗近似折算
			var consumed int64
			if err := db.QueryRow(tx, db.CurrentDialect(),
				"SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=? AND created_at>=?",
				tid, paidAt).Scan(&consumed); err != nil {
				return err
			}
			remain = granted - consumed
		}
	}
	if remain < 0 {
		remain = 0
	}
	if remain > granted {
		remain = granted
	}
	// 按剩余率折算应退金额（分），r≤0 时仅作废权益不退款
	ratio := 1.0
	if granted > 0 {
		ratio = float64(remain) / float64(granted)
	}
	moneyFen := int64(money*100 + 0.5)
	refundFen := int64(float64(moneyFen)*ratio + 0.5)
	if ratio <= 0 || refundFen < 0 {
		refundFen = 0
	}
	// 权益收回
	clawed := int64(0)
	clawDiff := int64(0)
	if isSub {
		// 订阅单：作废本单全部剩余台账行（即收回「剩余」，已消耗部分无从收回）
		if _, err := db.Exec(tx, db.CurrentDialect(),
			"UPDATE quota_grants SET \"left\"=0 WHERE tenant_id=? AND source='order' AND ref_id=? AND kind='plan' AND \"left\">0",
			tid, orderID); err != nil {
			return err
		}
		clawed = remain
	} else if remain > 0 {
		// 非订阅单：从永久余额守卫式扣回 min(剩余, 当前余额)——不足部分转人工，不阻塞退款
		var bal int64
		if err := db.QueryRow(tx, db.CurrentDialect(), "SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=?", tid).Scan(&bal); err != nil && err != sql.ErrNoRows {
			return err
		}
		clawed = remain
		if bal < clawed {
			clawDiff = clawed - bal
			clawed = bal
		}
		if clawed > 0 {
			if _, err := db.Exec(tx, db.CurrentDialect(),
				"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
				clawed, time.Now().Format(time.RFC3339), tid, clawed); err != nil {
				return err
			}
		}
	}
	// 条件置 refunded + 记录实退金额（并发双退款只有一个能成功 → 整体回滚，不会双扣）
	res2, err := db.Exec(tx, db.CurrentDialect(),
		"UPDATE orders SET status='refunded', refund_money=? WHERE id=? AND tenant_id=? AND status='paid'",
		float64(refundFen)/100.0, orderID, tid)
	if err != nil {
		return err
	}
	if n, _ := res2.RowsAffected(); n == 0 {
		return &errTxt{"订单不存在或已退款"}
	}
	// 提交后再写告警（历史教训：事务内经独立连接写库撞 busy_timeout 被吞，UAT-2 实测）
	if err := tx.Commit(); err != nil {
		return err
	}
	summary := fmt.Sprintf("订单 %d 退款完成（比例折算）：剩余率 %.1f%%，应退 %d 分，权益收回 %d token",
		orderID, ratio*100, refundFen, clawed)
	if isSub && remain > 0 {
		summary += fmt.Sprintf("，作废订阅台账 %d token", remain)
	}
	if clawDiff > 0 {
		summary += fmt.Sprintf("；⚠️ 余额不足以收回全部剩余权益，缺口 %d token 请人工核对", clawDiff)
		s.CreateAlert(tid, "warning", "refund_revoke", summary)
	} else {
		s.CreateAlert(tid, "info", "refund_revoke", summary)
	}
	return nil
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
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT amount_money FROM orders WHERE id=? AND tenant_id=? AND status='paid'", orderID, tid).Scan(&money)
	if err != nil {
		return nil, err
	}
	no := "INV" + time.Now().Format("20060102150405") + randSuffix(4) // 生成唯一发票号
	now := time.Now().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO invoices (tenant_id, order_id, invoice_no, amount_money, title, tax_no, status, created_at) VALUES (?,?,?,?,?,?,'issued',?)",
		tid, orderID, no, money, title, taxNo, now)
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(id, tid)
}

// GetInvoice 按 ID+租户查询发票（租户隔离校验）。
// 参数：id=发票主键 ID，tid=租户 ID；返回发票对象。
func (s *Store) GetInvoice(id, tid int64) (*Invoice, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(),
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
	rows, err := db.Query(s.db, db.CurrentDialect(),
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

// BillingIndexMigrate 计费域关键索引（幂等；整改 B5）：
//   - orders.order_no 唯一索引：此前无约束+弱随机订单号，回调对账 FindOrderByOrderNo
//     LIMIT 1 撞重复号可能入错账。存量若有重复号，唯一索引创建失败 → 降级普通索引并告警，
//     由运维按日志核对后人工清理再重建唯一索引。
//   - api_keys.key_hash 普通索引：GetAPIKeyByHash 是 OpenAPI 每次调用的热路径，
//     此前全表扫描。
func (s *Store) BillingIndexMigrate() {
	db.Exec(s.db, db.CurrentDialect(), `DROP INDEX IF EXISTS idx_orders_no`)
	if _, err := db.Exec(s.db, db.CurrentDialect(), `CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_no ON orders(tenant_id, order_no) WHERE order_no<>''`); err != nil {
		log.Printf("[migrate] orders.order_no 唯一索引创建失败（疑存量重复单号，请人工核对）: %v", err)
		if _, err2 := db.Exec(s.db, db.CurrentDialect(), `CREATE INDEX IF NOT EXISTS idx_orders_no ON orders(tenant_id, order_no) WHERE order_no<>''`); err2 != nil {
			log.Printf("[migrate] orders.order_no 普通索引亦创建失败: %v", err2)
		}
	}
	db.Exec(s.db, db.CurrentDialect(), `CREATE INDEX IF NOT EXISTS idx_apikeys_hash ON api_keys(key_hash)`)
}

// randSuffix 生成 n 位由大写字母和数字组成的随机后缀（用于订单号/发票号/API Key 唯一性）。
// ★ 整改 B5：换 crypto/rand——此前 UnixNano 种子的 LCG 可预测且高并发下易碰撞；
// 订单号承担支付回调对账主键职责，必须不可预测。
func randSuffix(n int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const max = 252 // 36 的最大整数倍 ≤256，拒绝采样消除取模偏置
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := cryptorand.Read(buf); err != nil {
			// 随机源不可用：拒绝生成而非降级弱随机（订单号可预测=资金风险）
			log.Printf("[rand] crypto/rand 失败: %v", err)
			return ""
		}
		for _, v := range buf {
			if int(v) < max {
				out = append(out, letters[int(v)%len(letters)])
				if len(out) == n {
					break
				}
			}
		}
	}
	return string(out)
}

// UsageAllByUser 跨租户聚合每用户用量（超管平台视角）。
// 参数：from/to=指定日期区间（YYYY-MM-DD，均空=全部时间，仅给一个视同单日/单边）。
// 返回：用户 ID → 消耗量。
func (s *Store) UsageAllByUser(from, to string) (map[int64]int64, error) {
	// 基础聚合：按用户汇总 quantity（平台视角以用量为口径）
	q := "SELECT user_id, COALESCE(SUM(quantity),0) FROM usage_ledger"
	args := []interface{}{}
	// 指定日期区间：追加 created_at 区间谓词（空=全部时间）
	if pred, cargs := usageDatePred(from, to); cargs != nil {
		q += " WHERE created_at " + pred
		args = cargs
	}
	q += " GROUP BY user_id"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
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
		{"price_fen_per_token", "10"},            // ★ 充值定价（分/token，评审整改 B1 单一事实源）
		// ★ KB 上传奖励（任务2.3）：每条约额 + 单租户日封顶（防刷）
		{"kb_upload_reward_tokens_per_entry", "200"},
		{"kb_upload_reward_daily_cap", "50000"},
	}
	for _, kv := range defaults {
		db.Exec(s.db, db.CurrentDialect(), "INSERT INTO system_config (key,value) SELECT ?,? WHERE NOT EXISTS (SELECT 1 FROM system_config WHERE key=?)", kv[0], kv[1], kv[0])
	}
}

// CloseStalePendingOrders 关闭超时未支付订单：pending 超过 order_pending_timeout_min 自动 cancelled。
//
// ★ 人工确认豁免（2026-08-26 P0-6 止血）：channel='manual' 且 manual_confirm=1 的订单
//
//	表示用户已扫码付款并点了「我已付费」，正在等待超管核实——这类单不受 15 分钟限制，
//	否则会出现「用户钱付了、订单被自动取消、超管确认失败」的资损事故。
func (s *Store) CloseStalePendingOrders() int64 {
	minutes := int64(15)
	if v, _ := s.GetConfig("order_pending_timeout_min"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			minutes = x
		}
	}
	cut := time.Now().Add(-time.Duration(minutes) * time.Minute).Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(), `UPDATE orders SET status='cancelled'
		WHERE status='pending' AND created_at < ?
		  AND NOT (channel='manual' AND manual_confirm=1)`, cut)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// TenantLowBalanceAlerts 低额提醒：租户剩余合计（台账+永久）低于阈值时告警（24h 去重）。
//
// ★ 修复记录（2026-08-26 P1-a）：去重查询列名由 type 更正为 alerts 表实际列名 kind
//
//	（旧代码 Scan 错误被吞、cnt 恒 0，24h 去重完全失效）。
//	语义说明：灰度期（billing_enforced=0）同样提醒——提前触达优于突然停服，
//	「只提醒不拦截」正是灰度设计的本意。
func (s *Store) TenantLowBalanceAlerts(threshold int64) {
	rows, err := db.Query(s.db, db.CurrentDialect(), `SELECT ba.tenant_id,
		COALESCE(ba.balance,0) + COALESCE((SELECT SUM(g."left") FROM quota_grants g
			WHERE g.tenant_id=ba.tenant_id AND g."left">0 AND g.expires_at > ?),0) AS remain
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
		db.QueryRow(s.db, db.CurrentDialect(), `SELECT COUNT(*) FROM alerts WHERE tenant_id=? AND kind='low_balance'
			AND created_at>?`, r.tid, dayAgo).Scan(&cnt)
		if cnt == 0 {
			s.CreateAlert(r.tid, "warning", "low_balance",
				fmt.Sprintf("额度即将耗尽：当前剩余 %d token，请及时充值或续订套餐", r.remain))
		}
	}
}
