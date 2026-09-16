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
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
	"translator/internal/db"
	"translator/internal/ops"
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
	// ChargeKind 计费语义（''=实扣旧数据 / charge=实扣 / free=策略免扣 / settle=欠费结算调整留痕）
	// ★ P1-2 修复（2026-09-14）台账已有列；此处仅列表结构补映射，供报表导出区分「真实消耗 vs 调整」。
	ChargeKind string `json:"charge_kind,omitempty"`
	CreatedAt  string `json:"created_at"` // 用量发生时间（RFC3339 字符串）
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
	// ★ 套餐升级（2026-09-09）：升级来源订单 ID（0=非升级单）与旧包抵扣金额（元，冲抵新包应付）
	UpgradeFromOrder int64   `json:"upgrade_from_order"` // 升级来源订单 ID（0=非升级单）
	CreditMoney      float64 `json:"credit_money"`       // 旧包按剩余价值折算的抵扣金额（元）
	// ★ A3（2026-09-12）：退款实退金额（元）。此前 refund_money 有列但不在查询清单，API/前端读不到。
	RefundMoney float64 `json:"refund_money"`
}

// orderCols 订单表查询列清单（统一使用，避免遗漏新增列）
const orderCols = "id, tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, prepay_id, qr_content, package_id, manual_confirm, created_by, created_at, COALESCE(paid_at,''), upgrade_from_order, COALESCE(credit_money,0), COALESCE(refund_money,0)"

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
		tid, time.Now().UTC().Format(time.RFC3339))
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
		tokens, time.Now().UTC().Format(time.RFC3339), tid)
	if err == nil {
		// ★ P1 多实例闭环：充值入账后通知影子余额失效（本进程清缓存 + Redis 广播他实例）
		notifyTenantBalanceChanged(tid)
	}
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
// deduct 扣减永久余额（★ C21：原导出 Deduct 已废弃转私有，仅存量回归测试使用；
// 业务代码一律走 DeductWithGrants 双桶口径）。
func (s *Store) deduct(tid int64, tokens int64) error {
	if err := s.EnsureBalance(tid); err != nil {
		return err
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
		tokens, time.Now().UTC().Format(time.RFC3339), tid, tokens)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInsufficientBalance // 余额不足（或并发抢占失败），拒绝扣减
	}
	return nil
}

// ErrSettleNotNeeded 欠费复核不成立：事务内权威复核发现余额实际足够（此前复核与清零
// 之间的并发消费已改变事实），调用方应将批次回插重试而非结算清零。
var ErrSettleNotNeeded = errors.New("欠费复核不成立：余额足够覆盖本批应扣")

// SettleExhausted 欠费停用结算（2026-09-12 决策「扣到归零」；★ P0-1 修复 2026-09-14）。
// 修复前缺陷：两条 UPDATE 无差别清零全部 quota_grants（含付费 kind='plan'）与永久余额，
// 且「复核通过 → 清零」跨事务存在 TOCTOU，并发扣减可使复核说够、实际已空被误清零。
// 修复后语义：
//
//	① 事务内权威复核（与清零同事务，SQLite _txlock=immediate / PG 行锁天然串行）：
//	   双桶可用量 ≥ owed → 返回 ErrSettleNotNeeded（瞬态误报，调用方回插重试）；
//	② 有界清零：消耗总量不超过 owed（正常路径 avail<owed → 全清亦不超欠），逐行取走
//	   并按「试用/临期优先 → 付费 plan → 永久余额兜底」的顺序消费；
//	③ 调整流水：实际消耗量落 usage_ledger（charge_kind='settle'），清零可审计可追偿，
//	   不再是「无痕归零」。
//
// 返回：实际消耗 token 量与错误。
func (s *Store) SettleExhausted(tid int64, owed int64) (int64, error) {
	if owed < 0 {
		owed = 0
	}
	d := db.CurrentDialect()
	tx, err := s.db.Begin() // sqlite 下 _txlock=immediate；PG 下逐行锁
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// ① 事务内权威复核：复核与清零同一事务，杜绝「复核说够、清零时空」竞态（A1 收口）
	var grantAvail, balAvail int64
	if err := db.QueryRow(tx, d,
		`SELECT COALESCE(SUM("left"),0) FROM quota_grants WHERE tenant_id=? AND "left">0`, tid).Scan(&grantAvail); err != nil {
		return 0, err
	}
	if err := db.QueryRow(tx, d,
		"SELECT COALESCE(SUM(balance),0) FROM balance_accounts WHERE tenant_id=?", tid).Scan(&balAvail); err != nil {
		return 0, err
	}
	if grantAvail+balAvail >= owed {
		return 0, ErrSettleNotNeeded
	}
	if grantAvail+balAvail <= 0 {
		// 幂等：本已归零（重复调用/零余额租户），直接提交空事务
		return 0, tx.Commit()
	}
	// ② 有界消费：台账行先于永久余额；台账内试用/临期先于付费 plan（与正常扣减次序同构）
	now := time.Now().UTC().Format(time.RFC3339)
	consumed := int64(0)
	rows, qerr := db.Query(tx, d,
		`SELECT id, "left", kind FROM quota_grants WHERE tenant_id=? AND "left">0
		 ORDER BY CASE WHEN kind='trial' THEN 0 ELSE 1 END ASC, expires_at ASC`, tid)
	if qerr != nil {
		return 0, qerr
	}
	type grantRow struct {
		id   int64
		left int64
	}
	var grants []grantRow
	for rows.Next() {
		var g grantRow
		var kind string
		if err := rows.Scan(&g.id, &g.left, &kind); err != nil {
			rows.Close()
			return 0, err
		}
		grants = append(grants, g)
	}
	rows.Close()
	for _, g := range grants {
		if consumed >= owed {
			break
		}
		take := g.left
		if take > owed-consumed {
			take = owed - consumed
		}
		if _, err := db.Exec(tx, d, `UPDATE quota_grants SET "left"=? WHERE id=?`,
			g.left-take, g.id); err != nil {
			return 0, err
		}
		consumed += take
	}
	// 永久余额兜底（仅当台账取完仍未覆盖 owed）
	if consumed < owed {
		var bid, bleft int64
		if err := db.QueryRow(tx, d,
			"SELECT id, balance FROM balance_accounts WHERE tenant_id=? AND balance>0", tid).Scan(&bid, &bleft); err == nil {
			take := bleft
			if take > owed-consumed {
				take = owed - consumed
			}
			if _, err := db.Exec(tx, d,
				"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE id=?",
				take, now, bid); err != nil {
				return 0, err
			}
			consumed += take
		} else if !errors.Is(qerr, sql.ErrNoRows) && qerr != nil {
			// 多行余额账户理论上不存在（单行表）；保守处理首行即可
			return 0, qerr
		}
	}
	// ③ 调整流水：清零量落 ledger（charge_kind='settle'），杜绝「无痕归零」无法追偿
	if consumed > 0 {
		if _, err := db.Exec(tx, d,
			"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at) VALUES (?,?,?,?,?,?,0,?, 'settle','', 'settle', ?)",
			tid, 0, "settle_exhausted", "settle", "欠费结算调整", consumed, consumed, now); err != nil {
			return 0, err
		}
	}
	// ★ P1 多实例闭环：欠费清零成功后通知影子失效（影子随后回读到近零真值，
	//   避免他实例影子停留在清零前的旧余额继续放行消费）
	if cerr := tx.Commit(); cerr != nil {
		return consumed, cerr
	}
	notifyTenantBalanceChanged(tid)
	return consumed, nil
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
func (s *Store) RecordUsage(tid, userID int64, taskType, provider, model, lang string, quantity int64, bizKind, bizMode string) (int64, error) {
	price, cost := s.pricingCost(taskType, provider, lang, quantity) // ★ C1：统一 cost=qty×price×mult
	tx, err := s.db.Begin()                                          // DSN _txlock=immediate ⇒ BEGIN IMMEDIATE
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := deductWithGrantsTx(tx, tid, cost); err != nil {
		return 0, err // 扣减失败（含余额不足）→ 整体回滚，不落半条
	}
	id, err := db.InsertID(tx, db.CurrentDialect(), "id",
		// ★ P1-2（2026-09-14）：charge_kind 区分实扣('charge')/留痕('log')/结算('settle')，退款与对账只认实扣
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,'charge',?)",
		tid, userID, taskType, provider, model, quantity, price, cost, bizKind, bizMode, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err // 落账失败 → 扣减一并回滚（修复「扣钱无流水」）
	}
	if err := incrementDailyUsageTx(tx, tid, cost); err != nil { // ★ C5：事务内累加，失败整体回滚
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// ★ 性能优化 B6：同步累加日计数器
	return id, nil
}

// UsageBatchRow 批量计量单行输入（性能优化 B2）。
type UsageBatchRow struct {
	UserID   int64
	TaskType string
	Provider string
	Model    string
	Lang     string // ★ C4：目标语种（''=通配定价）
	Quantity int64
	BizKind  string
	BizMode  string
	// OccurredAt ★ B2 口径修正（2026-09-12）：用量实际发生时间（RFC3339，入队时刻）。
	//   旧实现批量落账写 flush 时刻，导致「退款窗口消耗核算」把迟flush的历史用量
	//   误计入本单支付后消耗。空串=调用方未提供，退回当前时间。
	OccurredAt string
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
		// ★ C1（2026-09-12）：批量与单条统一 cost=qty×price×mult。translate 的 rate_card 为
		//   (1,1.0)，api 层 markup 折算不受影响（无双算）；evals/gate 等任务类型恢复 rate_card
		//   定价语义（旧批量忽略 price/mult，导致这些行多扣或漏扣）。
		price, cost := s.pricingCost(r.TaskType, r.Provider, r.Lang, r.Quantity)
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
	const insertSQL = "INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,'charge',?)"
	var firstID int64
	for i, pr := range pricedRows {
		ts := pr.OccurredAt
		if ts == "" {
			ts = time.Now().UTC().Format(time.RFC3339)
		}
		id, e := db.InsertID(tx, db.CurrentDialect(), "id", insertSQL,
			tid, pr.UserID, pr.TaskType, pr.Provider, pr.Model, pr.Quantity, pr.price, pr.cost, pr.BizKind, pr.BizMode, ts)
		if e != nil {
			return 0, e
		}
		if i == 0 {
			firstID = id
		}
	}
	// ★ C5：日计数器累加并入本事务（旧实现 commit 后独立写、吞错 → usage_daily 漂移）
	if err := incrementDailyUsageTx(tx, tid, sumCost); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return firstID, nil
}

// LogUsageBatch 仅记录用量、不扣余额的批量版（billing 未强制计费时用于留痕计量）。
// 单事务多行 INSERT。charge_kind='log'：留痕行不参与退款消耗核算与实扣对账（P1-2）。
func (s *Store) LogUsageBatch(tid int64, rows []UsageBatchRow) error {
	if len(rows) == 0 {
		return nil
	}
	const insertSQL = "INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,'log',?)"
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sumCost int64
	for _, r := range rows {
		ts := r.OccurredAt
		if ts == "" {
			ts = time.Now().UTC().Format(time.RFC3339)
		}
		// ★ C1：与 RecordUsageBatch 同口径 cost=qty×price×mult
		price, cost := s.pricingCost(r.TaskType, r.Provider, r.Lang, r.Quantity)
		if _, e := db.Exec(tx, db.CurrentDialect(), insertSQL,
			tid, r.UserID, r.TaskType, r.Provider, r.Model, r.Quantity, price, cost, r.BizKind, r.BizMode, ts); e != nil {
			return e
		}
		sumCost += cost
	}
	// ★ C5：同 RecordUsageBatch，累加入事务
	if err := incrementDailyUsageTx(tx, tid, sumCost); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// LogUsage 只记录用量、不扣余额（billing 未强制计费时用于留痕计量）。
// 参数：同 RecordUsage；仅返回错误。
func (s *Store) LogUsage(tid, userID int64, taskType, provider, model, lang string, quantity int64, bizKind, bizMode string) error {
	// ★ C1（2026-09-12）：与 RecordUsage/批量路径同一 cost 公式，SUM(cost) 可对账
	price, cost := s.pricingCost(taskType, provider, lang, quantity)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		// charge_kind='log'：留痕行不参与退款消耗核算（P1-2）
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,'log',?)",
		tid, userID, taskType, provider, model, quantity, price, cost, bizKind, bizMode, time.Now().UTC().Format(time.RFC3339))
	if err == nil {
		s.incrementDailyUsage(tid, cost) // ★ 性能优化 B6：同步累加日计数器
	}
	return err
}

// unitPrice 读取单价：优先供应商专属价，未配置回退全局 '*'。
// backfillUsageCostC1 ★ C1（2026-09-12）：历史 usage_ledger.cost 按统一公式
// （cost=qty×price×mult）重算回填。旧数据混用两套公式（单条乘、批量原样），
// SUM(cost) 勾稽失真。按当前 rate_card 定价近似重算，仅在值不同才写；
// system_config 标志位保证进程生命周期/重启只跑一次。
func (s *Store) backfillUsageCostC1() {
	if v, _ := s.GetConfig("cost_formula_backfill_c1"); v != "" {
		return
	}
	const chunk = 2000
	var lastID int64
	changed := 0
	for {
		rows, err := db.Query(s.db, db.CurrentDialect(),
			"SELECT id, quantity, task_type, provider, cost FROM usage_ledger WHERE id>? ORDER BY id LIMIT "+strconv.Itoa(chunk), lastID)
		if err != nil {
			log.Printf("[C1] 用量 cost 回填读取失败（跳过）: %v", err)
			return
		}
		type row struct {
			id             int64
			qty, cost      int64
			taskType, prov string
		}
		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.qty, &r.taskType, &r.prov, &r.cost); err != nil {
				continue
			}
			batch = append(batch, r)
			if r.id > lastID {
				lastID = r.id
			}
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			_, want := s.pricingCost(r.taskType, r.prov, "", r.qty) // 历史行无语种维度，按通配口径
			if want == r.cost {
				continue
			}
			if _, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE usage_ledger SET cost=? WHERE id=?", want, r.id); err == nil {
				changed++
			}
		}
		if len(batch) < chunk {
			break
		}
	}
	_ = s.SetConfig("cost_formula_backfill_c1", fmt.Sprintf("done:%d", changed))
	if changed > 0 {
		log.Printf("[C1] usage_ledger.cost 回填完成，更新 %d 行", changed)
	}
}

// pricingCost ★ C1 统一计费公式（唯一入口）：cost = quantity × unit_price × multiplier，
// 向下取整后负值兜 0。quantity 为计费量（translate 路径已在 api 层完成运营 markup 折算，
// rate_card.translate=(1,1.0) 保证不双重计费）。返回 (留档单价, 费用)。
func (s *Store) pricingCost(taskType, provider, lang string, quantity int64) (price, cost int64) {
	price, mult := s.unitPrice(taskType, provider, lang)
	// ★ C11（2026-09-12）：int64() 向下截断使 0.6 元成本记 0（小额免费化漂移），
	//   统一四舍五入（与 toFen 整改同口径）。
	cost = int64(math.Round(float64(quantity*price) * mult))
	if cost < 0 {
		cost = 0
	}
	return
}

// unitPrice 查询任务单价；返回单价与倍率（无配置默认 1 / 1.0）。
// 参数：taskType=任务类型，provider=供应商；返回单价与倍率（无配置默认 1 / 1.0）。
func (s *Store) unitPrice(taskType, provider, lang string) (int64, float64) {
	var price int64
	var mult float64
	// ★ C4（2026-09-12）：补「目标语种」定价维度。就近回退顺序：
	//   ① task+provider+lang → ② task+'*'provider+lang → ③ task+provider+'*' → ④ task+'*'+​'*'
	var err error
	if lang != "" {
		err = db.QueryRow(s.db, db.CurrentDialect(), "SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang=? AND provider=?", taskType, lang, provider).
			Scan(&price, &mult)
		if err != nil {
			err = db.QueryRow(s.db, db.CurrentDialect(), "SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang=? AND provider='*'", taskType, lang).
				Scan(&price, &mult)
		}
	}
	if lang == "" || err != nil {
		// 第一次回退：任务+供应商专属价格（lang='*'）
		// ★ G4 修复：lang=="" 时 err 保持 nil 会连回退一起跳过，落到 price=0 免费洞
		//   （kb_embed / 无语种调用全部静默免扣），必须显式进入回退链。
		err = db.QueryRow(s.db, db.CurrentDialect(), "SELECT unit_price, multiplier FROM rate_card WHERE task_type=? AND lang='*' AND provider=?", taskType, provider).
			Scan(&price, &mult)
	}
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

// UsageLedgerForExport 用量明细导出查询（★ P2 报表导出 2026-09-15）：
// 按时间范围（含边界，空串=不限）过滤，供 CSV 报表使用；单查询最大 limit 行（上限 10 万，
// 超出部分按 id 倒序截断最新段——报表面向「近期用量导出」场景，历史全量走 GDPR 导出）。
// 时间口径：usage_ledger.created_at 统一 UTC RFC3339（★ C22 写点约定），
// 故 from/to 按前缀字典序比较即可（from=YYYY-MM-DD 补 00:00:00，to 补 23:59:59）。
// 参数：tid=租户；from/to=日期字符串（可空）；limit=最大行数。
func (s *Store) UsageLedgerForExport(tid int64, from, to string, limit int) ([]*UsageLedger, error) {
	if limit <= 0 || limit > 100000 {
		limit = 100000 // 导出行数硬上限（防拖库式全量拉取放大内存/IO）
	}
	q := "SELECT id, tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, COALESCE(biz_kind,''), COALESCE(biz_mode,''), COALESCE(charge_kind,''), created_at FROM usage_ledger WHERE tenant_id=?"
	args := []interface{}{tid}
	if from != "" {
		q += " AND created_at>=?"
		args = append(args, from+"T00:00:00")
	}
	if to != "" {
		q += " AND created_at<=?"
		args = append(args, to+"T23:59:59Z")
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UsageLedger
	for rows.Next() {
		var u UsageLedger
		if err := rows.Scan(&u.ID, &u.TenantID, &u.UserID, &u.TaskType, &u.Provider, &u.Model, &u.Quantity, &u.UnitPrice, &u.Cost, &u.BizKind, &u.BizMode, &u.ChargeKind, &u.CreatedAt); err != nil {
			continue // 单行解析失败跳过（与列表查询同口径）
		}
		out = append(out, &u)
	}
	return out, nil
}

// DailyUsage 租户当日用量总费用。
// 参数：tid=租户 ID；返回今天累计扣费 token 数。
func (s *Store) DailyUsage(tid int64) (int64, error) {
	day := time.Now().UTC().Format("2006-01-02")
	var cost int64
	// ★ 性能优化 B6：优先读日计数器表（O(1) 命中主键），避免每次翻译请求都对 usage_ledger
	//   做 created_at LIKE 全表扫描（gateUsage→CheckDailyQuota 每请求一次）。
	// ★ C5：旧实现 SUM 无行也返回 (0,nil)，兜底成死代码。改「存在性」判定：
	//   当日有行即信任计数器（O(1)）；无行才回退 ledger 聚合（首日/迁移前遗留/极端缺失）。
	var cnt int
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*), COALESCE(SUM(total),0) FROM usage_daily WHERE tenant_id=? AND day=?", tid, day).Scan(&cnt, &cost); err == nil && cnt > 0 {
		return cost, nil
	}
	// 兜底（表缺失/无当日行）：回退 ledger 当日 LIKE 扫描
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND created_at LIKE ?", tid, day+"%").Scan(&cost)
	return cost, err
}

// incrementDailyUsageTx ★ C5（2026-09-12）：日计数器累加并入扣费事务。
// 旧实现事务外独立写 + 吞错——commit 后进程崩溃/写失败即产生 usage_daily 与 ledger
// 永久漂移（日限额少计，形同虚设）。返回错误供调用方随主事务回滚。
func incrementDailyUsageTx(tx *sql.Tx, tid, amount int64) error {
	if amount <= 0 {
		return nil
	}
	day := time.Now().UTC().Format("2006-01-02")
	_, err := db.Exec(tx, db.CurrentDialect(),
		`INSERT INTO usage_daily (tenant_id, day, total) VALUES (?,?,?)
		 ON CONFLICT(tenant_id, day) DO UPDATE SET total=usage_daily.total+?`,
		tid, day, amount, amount)
	return err
}

// incrementDailyUsage 落账时增量更新日计数器（性能优化 B6；无事务上下文兜底版）。
func (s *Store) incrementDailyUsage(tid, amount int64) {
	if amount <= 0 {
		return
	}
	day := time.Now().UTC().Format("2006-01-02")
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
		tid, userID, time.Now().UTC().Format("2006-01-02")+"%").Scan(&today)
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
//
//	否则仅含后台任务的日期（如全站批量 LLM 调用）按日查询恒为 0。user_id=0 由 API 层单独归一行。
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
		tid, orderNo, tokens, money, payMethod, channel, qrContent, createdBy, time.Now().UTC().Format(time.RFC3339))
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
	// ★ S1 积分制（2026-09-14）：包配置积分面值（points>0）时按「积分×points_tokens_rate」
	//   折算内部计量 token（新口径）；未配置走历史句数折算（sentences×500×markup，兼容存量包）。
	//   内部账本仍是 token（usage_ledger/成本核算不变），积分只是对外计量马甲，防成本反推。
	tokenAmt := s.PackageTokenAmount(pkg)
	// ★ 试运营首月半价（2026-09-14 拍板）：注册 30 天内订阅包订单一律挂牌价五折（含换档升级单）；
	//   充值包（increment，价格尺子）不打折。
	money := s.PackageOrderPrice(pkg, tid)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, qr_content, package_id, created_by, created_at) VALUES (?,?,?,?, 'pending', 'online', ?, '', ?, ?, ?)",
		tid, orderNo, tokenAmt, money, channel, pkg.ID, createdBy, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	return s.GetOrderByOrderNo(orderNo, tid)
}

// PackageTokenAmount 包面值 → 内部计量 token 数（积分包：积分×points_tokens_rate；
// 存量句数包：句数×500×markup 兼容口径）。
func (s *Store) PackageTokenAmount(pkg *Package) int64 {
	if pkg.Points > 0 {
		return pkg.Points * s.PointsTokensRate()
	}
	return int64(float64(pkg.Sentences*s.TokenSentenceRate()) * s.MarkupMultiplier())
}

// PackageOrderPrice 计算本单实收金额（元）：订阅包（paid）且租户注册未满 30 天 → 挂牌价五折。
// 参数：pkg=商业包，tid=租户 ID；返回实收金额（分为最小颗粒，四舍五入）。
func (s *Store) PackageOrderPrice(pkg *Package, tid int64) float64 {
	if pkg.PType != PackagePaid {
		return pkg.PriceMoney
	}
	var regAt string
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(created_at,'') FROM tenants WHERE id=?", tid).Scan(&regAt); err != nil || regAt == "" {
		return pkg.PriceMoney
	}
	t, e := time.Parse(time.RFC3339, regAt)
	if e != nil || time.Since(t) > 30*24*time.Hour {
		return pkg.PriceMoney
	}
	return float64(int64(pkg.PriceMoney*100*0.5+0.5)) / 100
}

// UpgradeCredit 套餐升级抵扣结算结果：旧包剩余价值折算的抵扣金额与旧包剩余台账 token。
type UpgradeCredit struct {
	OldOrderID   int64   // 升级来源订单 ID
	CreditMoney  float64 // 抵扣金额（元）= 旧订单实付 × 旧包剩余率
	RemainTokens int64   // 旧包剩余台账 token（作废后等价转入新包）
}

// ComputeUpgradeCredit 计算套餐升级抵扣：找租户当前生效付费订阅的最近一笔已支付订单，
// 按该订单剩余台账（quota_grants kind='plan'）折算剩余率 → 抵扣金额与剩余 token。
// 仅支持付费包（paid）升级：订阅单的剩余台账能精确核算（source='order' 关联订单）。
// ★ 校验：目标包售价必须高于当前包售价（更高才叫升级；持平/更低应走普通订阅）。
// 参数：tid=租户 ID，newPkg=目标升级包；返回抵扣结算（无有效订阅/非升级关系则返回错误）。
// ★ 2026-09-09 套餐升级：原包剩余价值按 token 台账比例退、新包即时生效、剩余额度等价转入新包。
func (s *Store) ComputeUpgradeCredit(tid int64, newPkg *Package) (*UpgradeCredit, error) {
	// ① 当前生效付费订阅（PackageCode 非空，最近一笔已支付订单）
	perms, err := s.GetTenantPerms(tid)
	if err != nil {
		return nil, err
	}
	if perms.PackageCode == "" {
		return nil, &errTxt{"当前无生效套餐，无法升级"}
	}
	// 目标不能是自己（换个包升级才有意义）
	if perms.PackageCode == newPkg.Code {
		return nil, &errTxt{"目标套餐与当前套餐相同，无需升级"}
	}
	curPkg, err := s.GetPackageByCode(tid, perms.PackageCode)
	if err != nil {
		return nil, &errTxt{"当前套餐不存在"}
	}
	if curPkg.PType != PackagePaid {
		return nil, &errTxt{"仅付费包支持升级"}
	}
	// ★ 新包售价必须高于当前包（否则不是升级）
	if newPkg.PriceMoney <= curPkg.PriceMoney {
		return nil, &errTxt{"目标套餐价格应高于当前套餐才能升级"}
	}
	// ② 找最近一笔该包已支付订单
	var oldID int64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id FROM orders WHERE tenant_id=? AND package_id=? AND status='paid' ORDER BY id DESC LIMIT 1",
		tid, curPkg.ID).Scan(&oldID); err != nil {
		return nil, &errTxt{"未找到当前套餐的已支付订单"}
	}
	oldOrder, err := s.GetOrder(oldID, tid)
	if err != nil {
		return nil, err
	}
	// ③ 剩余台账 token（精确）
	var remain int64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=? AND source='order' AND ref_id=? AND kind='plan' AND \"left\">0",
		tid, oldID).Scan(&remain); err != nil {
		return nil, err
	}
	granted := oldOrder.AmountTokens
	if granted <= 0 {
		return nil, &errTxt{"当前套餐订单 token 口径异常，无法核算抵扣"}
	}
	// ④ 剩余率 → 抵扣金额
	ratio := float64(remain) / float64(granted)
	if ratio > 1 {
		ratio = 1
	}
	if ratio <= 0 {
		return nil, &errTxt{"当前套餐已无剩余价值，无法抵扣升级"}
	}
	credit := float64(int(oldOrder.AmountMoney*ratio*100+0.5)) / 100.0
	return &UpgradeCredit{OldOrderID: oldID, CreditMoney: credit, RemainTokens: remain}, nil
}

// CreateUpgradeOrder 创建套餐升级订单（付费包→更高价付费包）：
// 金额 = 新包售价 − 旧包抵扣（≥0），记录 upgrade_from_order / credit_money，
// 支付确认时由 MarkOrderPaid 作废旧包剩余台账并等价转入新包台账。
// 参数：tid=租户 ID，pkg=目标升级包，credit=抵扣结算，createdBy=创建者 ID，channel=支付渠道。
// 返回：新订单对象（初始 pending）。
func (s *Store) CreateUpgradeOrder(tid int64, pkg *Package, credit *UpgradeCredit, createdBy int64, channel string) (*Order, error) {
	orderNo := fmt.Sprintf("T%d-RO%s%s", tid, time.Now().Format("20060102150405"), randSuffix(4))
	tokenAmt := s.PackageTokenAmount(pkg)
	if tokenAmt < 0 {
		tokenAmt = 0
	}
	// ★ S1：挂牌价走同一 PackageOrderPrice（注册 30 天内升级单同样享受首月半价），再减旧包抵扣
	pay := s.PackageOrderPrice(pkg, tid) - credit.CreditMoney
	if pay < 0 {
		pay = 0
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, qr_content, package_id, created_by, created_at, upgrade_from_order, credit_money) VALUES (?,?,?,?, 'pending', 'online', ?, '', ?, ?, ?, ?, ?)",
		tid, orderNo, tokenAmt, pay, channel, pkg.ID, createdBy, time.Now().UTC().Format(time.RFC3339), credit.OldOrderID, credit.CreditMoney)
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
		Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt, &o.UpgradeFromOrder, &o.CreditMoney, &o.RefundMoney)
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
		Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt, &o.UpgradeFromOrder, &o.CreditMoney, &o.RefundMoney)
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
		if err := rows.Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt, &o.UpgradeFromOrder, &o.CreditMoney, &o.RefundMoney); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &o)
	}
	return out, nil
}

// PriceFenPerMillionTokens 充值定价基准（分/百万 token）：system_config price_fen_per_million_tokens。
// ★ S1 账目修复（2026-09-14）：旧键 price_fen_per_token 名义「分/token」、实值 10，
//
//	等价 100 元/千 token、百万 token=10 万元——「按次计费」时代遗留，token 迁移后未换算。
//	新口径按试运营价目表尺子价锚定：¥299/百万 token → 29900 分。旧键不再读取（保留仅供历史对账）。
//	注意：本价仅用于「无套餐裸充值单」的金额兜底；正式售卖一律走套餐 amount_money（packages.price_money）。
func (s *Store) PriceFenPerMillionTokens() int64 {
	if v, _ := s.GetConfig("price_fen_per_million_tokens"); v != "" {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil && n > 0 {
			return n
		}
	}
	return 29900
}

// TokensToFen token 数→应收金额（分），四舍五入。
func (s *Store) TokensToFen(tokens int64) int64 {
	r := s.PriceFenPerMillionTokens()
	return (tokens*r + 500000) / 1000000
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
	rate := float64(s.PriceFenPerMillionTokens()) // 分/百万 token
	db.Exec(s.db, db.CurrentDialect(), `UPDATE orders SET amount_money=ROUND(amount_tokens * ? / 100000000.0, 2)
		WHERE status='pending' AND package_id=0 AND COALESCE(amount_money,0)=0 AND amount_tokens>0`,
		rate)
}

// PackageOrderTokenBackfill 存量商业包订单 token 口径回填（幂等，Store.New 迁移链调用）：
// 历史 CreatePackageOrder 将 amount_tokens 存为 0，实际 token 数需由 pkg.Sentences×折算率得出；
// 为贯彻「费用一律 token 口径、句数不参与运行期计算」，此处把存量包订单的 amount_tokens 一次性补全，
// 此后支付发放（MarkOrderPaid）与退款核算（RefundOrder）均直接采用该 token 数，不再以句数折算。
func (s *Store) PackageOrderTokenBackfill() {
	rate := s.TokenSentenceRate()
	if rate <= 0 {
		rate = ops.DefaultTokensPerSentence // ★ C6
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
	// ★ C10（2026-09-12）：回填口径必须与 MarkOrderPaid 未回填兜底
	//   （sentences×rate×MarkupMultiplier）一致。旧回填漏乘 markup：
	//   已按兜底口径发放的订单，升级抵扣 ratio=remain/amount_tokens 分子分母
	//   不同源（发放带系数、台账记不带），抵扣额失真。
	mult := s.MarkupMultiplier()
	for _, r := range pending {
		p, e := s.GetPackage(r.pkg)
		if e != nil {
			continue
		}
		tok := int64(float64(p.Sentences*rate) * mult)
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
		tid, time.Now().UTC().Format(time.RFC3339))
	return err
}

// chargePermanentTx 永久余额入账（tx 版 Charge）。
// planGrantExpiry ★ C3（2026-09-12）：订阅台账到期 = DurationDays（0=不限期，
//
//	以 9999 哨兵表达，字典序比较天然恒真）。旧实现硬编码 t+30 天，无视包配置时长。
func planGrantExpiry(pkgDays int) time.Time {
	if pkgDays <= 0 {
		return time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	return time.Now().AddDate(0, 0, pkgDays)
}

// chargePermanentTx 事务内扣减永久余额（tokens<=0 直接放行）。
func chargePermanentTx(tx *sql.Tx, tid int64, tokens int64) error {
	if tokens <= 0 {
		return nil
	}
	if err := ensureBalanceTx(tx, tid); err != nil {
		return err
	}
	_, err := db.Exec(tx, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
		tokens, time.Now().UTC().Format(time.RFC3339), tid)
	if err == nil {
		// ★ P1 多实例闭环：永久余额入账通知影子失效（事务回滚时多通知一次仅多一次
		//   DB 回读，无正确性风险；漏通知由影子 TTL(5s) 自愈兜底，见 balancehook.go）
		notifyTenantBalanceChanged(tid)
	}
	return err
}

// createQuotaGrantTx 台账发放（CreateQuotaGrant 的 tx/DB 双形态版，★ C23 供占用+发放同事务复用）。
func createQuotaGrantTx(tx db.Execer, tid int64, kind string, total int64, expires time.Time, source string, refID int64) error {
	_, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT INTO quota_grants (tenant_id, kind, total, \"left\", expires_at, source, ref_id, created_at) VALUES (?,?,?,?,?,?,?,?)",
		// ★ C22：created_at 同样 UTC（与 CreateQuotaGrant 一致）
		tid, kind, total, total, expires.UTC().Format(time.RFC3339), source, refID, time.Now().UTC().Format(time.RFC3339))
	if err == nil {
		// ★ P1 多实例闭环：台账发放（trial/plan/增量）通知影子失效
		notifyTenantBalanceChanged(tid)
	}
	return err
}

// applyIncrementMirrorTx 增量包句数镜像追加（tx 版 ApplyIncrementMirror，单语句原子自增）。
// ★ 2026-09-12 PG 方言修复：原内联 json_set/json_extract 为 SQLite JSON1 专属，
// PG 下整条 UPDATE 报「function json_extract does not exist」→ 增量包订单结算必失败、
// 订单永挂 pending。改经 db.JSONNumAdd 双方言助手。
func applyIncrementMirrorTx(tx *sql.Tx, tid int64, sentences int64) error {
	if sentences <= 0 {
		return nil
	}
	d := db.CurrentDialect()
	_, err := db.Exec(tx, d,
		"UPDATE tenants SET "+db.JSONNumAdd(d, "permissions", "sentence_balance")+", updated_at=? WHERE id=?",
		sentences, time.Now().UTC().Format(time.RFC3339), tid)
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
	nowStr := time.Now().UTC().Format(time.RFC3339)
	// ① 事务外预读（失败不产生任何写副作用）
	var tokens, pkgID, createdBy, upgradeFrom int64
	var money float64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT amount_tokens, package_id, COALESCE(created_by,0), COALESCE(amount_money,0), COALESCE(upgrade_from_order,0) FROM orders WHERE id=? AND tenant_id=?",
		orderID, tid).Scan(&tokens, &pkgID, &createdBy, &money, &upgradeFrom); err != nil {
		return &errTxt{"订单不存在"}
	}
	sentenceRate := s.TokenSentenceRate()
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
	// 应收金额转分：套餐单取包售价，纯充值单按尺子价（分/百万 token）兜底
	payFen := int64(money*100 + 0.5)
	if payFen <= 0 {
		payFen = s.TokensToFen(tokens)
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
		// ★ C2：金额投语义列 amount_fen（分）；amount_money 停止写入（历史兼容列）
		"INSERT INTO payments (order_id, tenant_id, amount_tokens, amount_fen, status, created_at) VALUES (?,?,?,?, 'paid', ?)",
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
	case pType == "paid" && upgradeFrom > 0:
		// ★ 套餐升级（2026-09-09）：旧付费包作废 + 新付费包即时生效。
		//   ① 作废旧包剩余台账（source='order' AND ref_id=旧订单 → left=0）；
		//   ② 旧包剩余 token 等价转入新台账（新台账 total = 新包 token + 旧包剩余）；
		//   ③ 订阅身份覆盖为新包（PackageCode/SubscribedAt/PackageExpires 从今天重算）。
		//   旧包按剩余价值的抵扣已在创建订单时冲抵新包应付（credit_money），此处仅作废权益转移额度。
		oldRemain := int64(0)
		if err := db.QueryRow(tx, db.CurrentDialect(),
			"SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=? AND source='order' AND ref_id=? AND kind='plan' AND \"left\">0",
			tid, upgradeFrom).Scan(&oldRemain); err != nil {
			return err
		}
		// ★ C3（2026-09-12）：升级转入逐行保留**原到期时间**（旧实现并入新包统一窗口，
		//   短包升长包变相续命、长包升短包反被提前收割）。转入行 source='order_carry'
		//   挂本单 ref，退款收回（source='order' 过滤）自然排除转入份额。
		carryRows, cerr := db.Query(tx, db.CurrentDialect(),
			"SELECT \"left\", expires_at FROM quota_grants WHERE tenant_id=? AND source='order' AND ref_id=? AND kind='plan' AND \"left\">0",
			tid, upgradeFrom)
		if cerr != nil {
			return cerr
		}
		type carryItem struct {
			left    int64
			expires string
		}
		var carries []carryItem
		for carryRows.Next() {
			var ci carryItem
			if err := carryRows.Scan(&ci.left, &ci.expires); err == nil && ci.left > 0 {
				carries = append(carries, ci)
			}
		}
		carryRows.Close()
		if err := carryRows.Err(); err != nil {
			return err
		}
		// 作废旧包剩余台账（并发安全：同一事务内条件置零）
		if _, err := db.Exec(tx, db.CurrentDialect(),
			"UPDATE quota_grants SET \"left\"=0 WHERE tenant_id=? AND source='order' AND ref_id=? AND kind='plan' AND \"left\">0",
			tid, upgradeFrom); err != nil {
			return err
		}
		perms, gerr := getTenantPermsTx(tx, tid)
		if gerr != nil {
			return gerr
		}
		perms.PackageCode = pkgCode
		perms.SubscribedAt = nowStr
		perms.SentenceBalance += pkgSentences
		if pkgDays > 0 {
			perms.PackageExpires = time.Now().UTC().AddDate(0, 0, pkgDays).Format(time.RFC3339)
		} else {
			perms.PackageExpires = ""
		}
		perms.NotifiedExp7 = false
		perms.NotifiedExp1 = false
		perms.NotifiedRenew3 = false
		if serr := saveTenantPermsTx(tx, tid, perms); serr != nil {
			return serr
		}
		// 新台账：仅新包 token，到期按 DurationDays（★ C3）
		if gerr2 := createQuotaGrantTx(tx, tid, "plan", pkgTokens, planGrantExpiry(pkgDays), "order", orderID); gerr2 != nil {
			return gerr2
		}
		for _, ci := range carries {
			cexp := time.Now()
			if t, err := time.Parse(time.RFC3339, ci.expires); err == nil {
				cexp = t
			}
			if gerr2 := createQuotaGrantTx(tx, tid, "plan", ci.left, cexp, "order_carry", orderID); gerr2 != nil {
				return gerr2
			}
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
			perms.PackageExpires = time.Now().UTC().AddDate(0, 0, pkgDays).Format(time.RFC3339)
		} else {
			perms.PackageExpires = ""
		}
		perms.NotifiedExp7 = false
		perms.NotifiedExp1 = false
		perms.NotifiedRenew3 = false
		if serr := saveTenantPermsTx(tx, tid, perms); serr != nil {
			return serr
		}
		// ★ C3：到期跟随 DurationDays（0=永久哨兵），不再硬编码 t+30
		if cerr := createQuotaGrantTx(tx, tid, "plan", pkgTokens, planGrantExpiry(pkgDays), "order", orderID); cerr != nil {
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
			perms.PackageExpires = time.Now().UTC().AddDate(0, 0, pkgDays).Format(time.RFC3339)
		} else {
			perms.PackageExpires = ""
		}
		perms.NotifiedExp7 = false
		perms.NotifiedExp1 = false
		perms.NotifiedRenew3 = false
		if serr := saveTenantPermsTx(tx, tid, perms); serr != nil {
			return serr
		}
		// ★ C9（2026-09-12）：入账一律取订单 amount_tokens（下单为唯一事实源，
		//   与「token 口径」整改一致）；旧实现 pkgSentences×rate 二次折算，
		//   无 markup、与前端展示的下单量脱钩，造成账实不符。
		cashTokens := pkgTokens
		if cashTokens == 0 {
			cashTokens = pkgSentences * sentenceRate // 存量未回填订单兜底（与订阅分支同口径）
		}
		if cerr := chargePermanentTx(tx, tid, cashTokens); cerr != nil {
			return cerr
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// ③ 提交后副作用：邀请裂变「受邀人首笔付费→邀请者永久 token」（幂等按对去重；
	//    失败仅损失一次奖励，不影响本单权益到账，故置于事务外）
	if createdBy > 0 && pType == "paid" {
		// ★ C23：占用+到账同事务，失败可整体回滚重触发；此处不再丢弃错误，留痕便于补发
		if rerr := s.ReferralPaidReward(createdBy); rerr != nil {
			log.Printf("[billing] 邀请付费奖励发放失败（可重新触发）invitee=%d: %v", createdBy, rerr)
		}
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
	// 条件更新：仅 manual/usdt 渠道、未确认、未支付的订单可被标记
	// （usdt=★ 2026-09-15 静态收款「我已付费+txid」声明进人工/对账队列）
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE orders SET manual_confirm=1 WHERE id=? AND tenant_id=? AND channel IN ('manual','usdt') AND manual_confirm=0 AND status='pending'",
		orderID, tid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &errTxt{"订单不存在或已处理"}
	}
	return nil
}

// ReopenManualOrder ★ C19（2026-09-12）：manual 静态码单被超时自动取消后，
// 用户点「我已付费」的补单通道——按原单复制金额/套餐重建一笔待审核单
// （status=pending + manual_confirm=1），直接进入超管人工确认队列。
// 原 cancelled 单保留不动（对账痕迹）；转账凭证由用户在弹窗外线下提供。
// 参数：orderID=原订单 ID，tid=租户 ID；返回新建订单。
func (s *Store) ReopenManualOrder(orderID, tid int64) (*Order, error) {
	o, err := s.GetOrder(orderID, tid)
	if err != nil {
		return nil, err
	}
	if o.Channel != "manual" || o.Status != "cancelled" {
		return nil, &errTxt{"仅超时取消的静态码订单支持补单重建"}
	}
	no := fmt.Sprintf("T%d-RO%s%sR", tid, time.Now().UTC().Format("20060102150405"), randSuffix(4))
	_, err = db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, pay_method, channel, qr_content, package_id, manual_confirm, created_by, created_at) VALUES (?,?,?,?, 'pending', 'offline', 'manual', '', ?, 1, ?, ?)",
		tid, no, o.AmountTokens, o.AmountMoney, o.PackageID, o.CreatedBy, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	return s.GetOrderByOrderNo(no, tid)
}

// ListManualConfirmOrders 列出待人工确认的订单（超管 Billing 面板）：manual 渠道 + manual_confirm=1 + pending。
// 参数：无（全平台）；返回订单列表（按 ID 倒序）。
func (s *Store) ListManualConfirmOrders() ([]*Order, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+orderCols+" FROM orders WHERE channel IN ('manual','usdt') AND manual_confirm=1 AND status='pending' ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt, &o.UpgradeFromOrder, &o.CreditMoney, &o.RefundMoney); err != nil {
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
		Scan(&o.ID, &o.TenantID, &o.OrderNo, &o.AmountTokens, &o.AmountMoney, &o.Status, &o.PayMethod, &o.Channel, &o.PrepayID, &o.QRContent, &o.PackageID, &o.ManualConfirm, &o.CreatedBy, &o.CreatedAt, &o.PaidAt, &o.UpgradeFromOrder, &o.CreditMoney, &o.RefundMoney)
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
	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ BEGIN IMMEDIATE（PG 下订单行条件更新兜底幂等）
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d := db.CurrentDialect()
	// 读取订单（仅已支付可退）：包/发放量/应收/支付时间/升级来源/买家
	var pkgID, tokens, upgradeFrom, buyerUID int64
	var money float64
	var paidAt string
	if err := db.QueryRow(tx, d,
		"SELECT package_id, amount_tokens, COALESCE(amount_money,0), COALESCE(paid_at,''), COALESCE(upgrade_from_order,0), created_by FROM orders WHERE id=? AND tenant_id=? AND status='paid'",
		orderID, tid).Scan(&pkgID, &tokens, &money, &paidAt, &upgradeFrom, &buyerUID); err != nil {
		if err == sql.ErrNoRows {
			return &errTxt{"订单不存在或状态不允许退款"}
		}
		return err
	}
	granted := tokens
	isSub := false
	var pkgSentences int64
	// ① 包信息：ptype 决定收回路径；sentences 用于句数镜像回冲
	var ptype string
	if pkgID > 0 {
		if err := db.QueryRow(tx, d,
			"SELECT COALESCE(ptype,''), sentences FROM packages WHERE id=?", pkgID).Scan(&ptype, &pkgSentences); err != nil {
			return err
		}
	}
	remain := granted
	consumedApprox := false
	if pkgID > 0 && ptype == "paid" {
		isSub = true
		// 订阅单：台账剩余精确可查。★ A3 升级单口径修正：台账行 total=新单入账+旧包转入（oldRemain），
		//   旧包价值已在升级下单时抵扣（credit_money），退款只收回「本单剩余份额」，转入份额保留。
		var rowTotal, rowLeft int64
		if err := db.QueryRow(tx, d,
			`SELECT COALESCE(SUM(total),0), COALESCE(SUM("left"),0) FROM quota_grants
			 WHERE tenant_id=? AND source='order' AND ref_id=? AND kind='plan'`,
			tid, orderID).Scan(&rowTotal, &rowLeft); err != nil {
			return err
		}
		oldCarry := rowTotal - granted // 升级转入的旧包剩余（非升级单恒 0）
		if oldCarry < 0 {
			oldCarry = 0
		}
		remain = rowLeft - oldCarry
		if remain < 0 {
			remain = 0
		}
		if remain > granted {
			remain = granted
		}
	} else if paidAt != "" {
		// ★ A3 口径统一（含纯充值单——旧实现 pkgID==0 直接跳过消耗核算、无条件全额退）：
		//   凡非订阅单，剩余 = 发放 − 自支付时刻起的租户级计量合计。quantity 与入账同为「计费 token」
		//   （含 markup），单位一致；跨订单混池属近似折算，供商务折让使用。
		// ★ P1-2 修复（2026-09-14）：只认实扣行 charge_kind IN ('','charge')——
		//   旧实现把「非强制计费期留痕」与「欠费结算留痕（未扣费）」也计入消耗，
		//   导致 consumed 虚高、应退金额被低估，直接损害退款用户。
		var consumed int64
		if err := db.QueryRow(tx, d,
			"SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=? AND created_at>=? AND charge_kind IN ('','charge')",
			tid, paidAt).Scan(&consumed); err != nil {
			return err
		}
		remain = granted - consumed
		consumedApprox = true
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
		// 订阅单：核销「本单剩余份额」（升级单保留旧包转入部分，见上）
		if remain > 0 {
			if _, err := db.Exec(tx, d,
				`UPDATE quota_grants SET "left" = (CASE WHEN "left">=? THEN "left"-? ELSE 0 END)
				 WHERE tenant_id=? AND source='order' AND ref_id=? AND kind='plan' AND "left">0`,
				remain, remain, tid, orderID); err != nil {
				return err
			}
			clawed = remain
		}
	} else if remain > 0 {
		// 非订阅单：从永久余额守卫式扣回 min(剩余, 当前余额)——不足部分转人工，不阻塞退款
		var bal int64
		if err := db.QueryRow(tx, d, "SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=?", tid).Scan(&bal); err != nil && err != sql.ErrNoRows {
			return err
		}
		clawed = remain
		if bal < clawed {
			clawDiff = clawed - bal
			clawed = bal
		}
		if clawed > 0 {
			if _, err := db.Exec(tx, d,
				"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
				clawed, time.Now().UTC().Format(time.RFC3339), tid, clawed); err != nil {
				return err
			}
		}
	}
	// ★ A3 句数镜像回冲：购包时入账的 sentence_balance（发放流水口径）同步扣回，下限钳 0；
	//   纯充值单不入句数镜像（pkgSentences=0 自然跳过）。
	if pkgSentences > 0 {
		if _, err := db.Exec(tx, d,
			"UPDATE tenants SET "+db.JSONNumAddFloor0(d, "permissions", "sentence_balance")+", updated_at=? WHERE id=?",
			-pkgSentences, time.Now().UTC().Format(time.RFC3339), tid); err != nil {
			return err
		}
	}
	// 条件置 refunded + 记录实退金额（并发双退款只有一个能成功 → 整体回滚，不会双扣）
	res2, err := db.Exec(tx, d,
		"UPDATE orders SET status='refunded', refund_money=? WHERE id=? AND tenant_id=? AND status='paid'",
		float64(refundFen)/100.0, orderID, tid)
	if err != nil {
		return err
	}
	if n, _ := res2.RowsAffected(); n == 0 {
		return &errTxt{"订单不存在或已退款"}
	}
	// ★ A3 退款冲正流水：payments 与支付流水同表登记（负向金额，分口径与入账一致），
	//   orders↔payments 自此可勾稽对账。
	if _, err := db.Exec(tx, d,
		"INSERT INTO payments (order_id, tenant_id, amount_tokens, amount_fen, status, created_at) VALUES (?,?,?,?, 'refunded', ?)",
		orderID, tid, -clawed, -refundFen, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	// 提交后再写告警（历史教训：事务内经独立连接写库撞 busy_timeout 被吞，UAT-2 实测）
	if err := tx.Commit(); err != nil {
		return err
	}
	summary := fmt.Sprintf("订单 %d 退款完成（比例折算）：剩余率 %.1f%%，应退 %d 分，权益收回 %d token",
		orderID, ratio*100, refundFen, clawed)
	if isSub && remain > 0 {
		summary += fmt.Sprintf("，作废订阅台账 %d token", remain)
		if upgradeFrom > 0 {
			summary += "（升级单：旧包转入份额已保留，不在收回范围）"
		}
	} else if consumedApprox {
		summary += "，消耗按支付后计量流水近似折算"
	}
	// ★ A3 裂变付费奖励回收：被邀人首笔付费是奖励唯一触发源——其 paid 订单已全部退还时，
	//   删除 paid_perm 流水并扣回邀请人奖励（余额不足部分转人工核对告警）。
	if claw := s.revokePaidReferralIfAllRefunded(buyerUID); claw > 0 {
		summary += fmt.Sprintf("；已回收邀请人付费奖励 %d token", claw)
	}
	// ★ A3 发票冲红提示：本单存在已开具发票时告警留痕（税务冲红对接属资质遗留项）
	var invCount int64
	_ = db.QueryRow(s.db, d, "SELECT COUNT(*) FROM invoices WHERE order_id=? AND status='issued'", orderID).Scan(&invCount)
	if invCount > 0 {
		summary += fmt.Sprintf("；⚠️ 该订单存在 %d 张已开具发票，需线下冲红", invCount)
	}
	if clawDiff > 0 {
		summary += fmt.Sprintf("；⚠️ 余额不足以收回全部剩余权益，缺口 %d token 请人工核对", clawDiff)
		s.CreateAlert(tid, "warning", "refund_revoke", summary)
	} else {
		s.CreateAlert(tid, "info", "refund_revoke", summary)
	}
	// ★ P1 多实例闭环：退款收回权益后通知影子失效（本租户 + 买家的双桶均已变动；
	//   邀请人奖励回收部分若遗漏通知，由其影子 TTL(5s) 自愈兜底）
	notifyTenantBalanceChanged(tid)
	return nil
}

// revokePaidReferralIfAllRefunded ★ A3（2026-09-12）：退款后回收「受邀付费→邀请者永久余额」奖励。
// 条件：被邀人（buyerUID）名下已无任何 paid 订单（首笔付费语义不再成立）且存在 paid_perm 流水。
// 动作：先删占用流水行（防并发双退），再从邀请人永久余额守卫式扣回，缺口告警。
// 返回实际扣回 token（0=无奖励可回收/未满足条件）。
func (s *Store) revokePaidReferralIfAllRefunded(buyerUID int64) int64 {
	d := db.CurrentDialect()
	if buyerUID <= 0 {
		return 0
	}
	var rowID, inviterUID, inviterTID, tokens int64
	err := db.QueryRow(s.db, d,
		"SELECT id, inviter_uid, inviter_tid, tokens FROM referral_rewards WHERE invitee_uid=? AND type='paid_perm' LIMIT 1",
		buyerUID).Scan(&rowID, &inviterUID, &inviterTID, &tokens)
	if err != nil || rowID == 0 || tokens <= 0 {
		return 0
	}
	// 被邀人仍有其它已支付订单 → 付费事实成立，奖励保留
	var stillPaid int64
	_ = db.QueryRow(s.db, d, "SELECT COUNT(*) FROM orders WHERE created_by=? AND status='paid'", buyerUID).Scan(&stillPaid)
	if stillPaid > 0 {
		return 0
	}
	// 条件删除（并发双退时仅一方命中，天然幂等）
	res, err := db.Exec(s.db, d, "DELETE FROM referral_rewards WHERE id=? AND type='paid_perm'", rowID)
	if err != nil {
		log.Printf("[refund] 裂变奖励流水删除失败 invitee=%d: %v", buyerUID, err)
		return 0
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0
	}
	// 邀请人永久余额守卫式扣回，不足扣到 0 并告警缺口
	_ = s.EnsureBalance(inviterTID)
	var bal int64
	_ = db.QueryRow(s.db, d, "SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=?", inviterTID).Scan(&bal)
	revoked := tokens
	if bal < revoked {
		revoked = bal
	}
	if revoked > 0 {
		if _, err := db.Exec(s.db, d,
			"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
			revoked, time.Now().UTC().Format(time.RFC3339), inviterTID, revoked); err != nil {
			log.Printf("[refund] 裂变奖励扣回失败 inviter_tid=%d: %v", inviterTID, err)
			return 0
		}
	}
	if gap := tokens - revoked; gap > 0 {
		_ = s.CreateAlert(inviterTID, "warning", "refund_reward_revoke",
			fmt.Sprintf("受邀人订单全部退款，邀请付费奖励回收缺口 %d token（余额不足），请人工核对", gap))
	} else {
		_ = s.CreateAlert(inviterTID, "info", "refund_reward_revoke",
			fmt.Sprintf("受邀人订单全部退款，已回收邀请付费奖励 %d token", revoked))
	}
	return revoked
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
	// ★ C16/A3.4：refunded 订单禁止开票（需先走冲红重开流程的由线下税务处理）
	var ostatus string
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT status FROM orders WHERE id=? AND tenant_id=?", orderID, tid).Scan(&ostatus); err != nil {
		return nil, err
	}
	if ostatus == "refunded" {
		return nil, &errTxt{"已退款订单不可开具发票"}
	}
	// 仅允许对已支付订单开票，金额取订单金额
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT amount_money FROM orders WHERE id=? AND tenant_id=? AND status='paid'", orderID, tid).Scan(&money)
	if err != nil {
		return nil, err
	}
	// ★ C16（2026-09-12）：同单重复开票前置检查（并发下由 partial unique 索引兜底）。
	var actives int
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM invoices WHERE order_id=? AND status <> 'void'", orderID).Scan(&actives); err != nil {
		return nil, err
	}
	if actives > 0 {
		return nil, &errTxt{"该订单已有有效发票，如需重开请先作废（冲红）"}
	}
	no := "INV" + time.Now().Format("20060102150405") + randSuffix(4) // 生成唯一发票号
	now := time.Now().UTC().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO invoices (tenant_id, order_id, invoice_no, amount_money, title, tax_no, status, created_at) VALUES (?,?,?,?,?,?,'issued',?)",
		tid, orderID, no, money, title, taxNo, now)
	if err != nil {
		// 并发双开：唯一索引冲突转可读错误（C16）
		if strings.Contains(err.Error(), "idx_invoices_order_active") || strings.Contains(err.Error(), "duplicate key") {
			return nil, &errTxt{"该订单已有有效发票（并发提交被唯一约束拦截）"}
		}
		return nil, err
	}
	return s.GetInvoice(id, tid)
}

// VoidInvoice ★ C16：发票作废（数据层冲红标记）。仅 issued/cancelled → void；
// void 为逻辑作废（保留行可审计），作废后该订单可重新开票。
// 参数：id=发票 ID，tid=租户 ID；返回错误（不存在/状态不允许）。
func (s *Store) VoidInvoice(id, tid int64) error {
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE invoices SET status='void' WHERE id=? AND tenant_id=? AND status<>'void'", id, tid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &errTxt{"发票不存在或已作废"}
	}
	return nil
}

// InvoiceNeedsRedMark ★ C16/A3 联动：退款单上的有效发票计数（冲红提醒看板用）。
func (s *Store) InvoiceNeedsRedMark(tid int64) (int64, error) {
	var n int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT COUNT(*) FROM invoices i JOIN orders o ON o.id=i.order_id
		 WHERE i.status <> 'void' AND o.status='refunded' AND (?<=0 OR o.tenant_id=?)`,
		tid, tid).Scan(&n)
	return n, err
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
	// ★ C27（2026-09-12）：order_no 升格**全局唯一**（回调 FindOrderByOrderNo /
	//   UpdateOrderPrepay 都是全局按号匹配，租户级唯一索引挡不住跨租户同号错账）。
	//   存量重复 → 创建失败时点名告警并降级保留租户级索引，人工改号后重启自动升级。
	if _, err := db.Exec(s.db, db.CurrentDialect(), `CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_no_global ON orders(order_no) WHERE order_no<>''`); err != nil {
		log.Printf("[migrate] order_no 全局唯一索引创建失败（存量重复单号，存在回调错账风险，需人工改号）: %v", err)
		dups, e := db.Query(s.db, db.CurrentDialect(),
			`SELECT order_no, COUNT(*) FROM orders WHERE order_no<>'' GROUP BY order_no HAVING COUNT(*)>1 LIMIT 20`)
		if e == nil {
			for dups.Next() {
				var no string
				var c int
				if dups.Scan(&no, &c) == nil {
					log.Printf("[migrate] 重复单号: %s ×%d", no, c)
				}
			}
			dups.Close()
		}
	}
	// ★ C16（2026-09-12）：一张有效发票/订单——partial unique（status<>'void' 才算占用），
	//   void 流转后可重开；存量同单多发票 → 降级普通索引 + 告警人工合并。
	if _, err := db.Exec(s.db, db.CurrentDialect(), `CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_order_active ON invoices(order_id) WHERE status <> 'void'`); err != nil {
		log.Printf("[migrate] invoices(order_id) 活跃唯一索引创建失败（存量同单多发票需人工置 void）: %v", err)
		db.Exec(s.db, db.CurrentDialect(), `CREATE INDEX IF NOT EXISTS idx_invoices_order ON invoices(order_id)`)
	}
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
		{"invite_reward_tokens", "300000"},        // 每邀 1 人·邀请者体验增量
		{"invite_extend_days", "14"},              // 每邀 1 人·邀请者时长叠加天数
		{"inviter_paid_reward_tokens", "500000"},  // 受邀者首笔付费套餐→邀请者永久 token
		{"price_fen_per_million_tokens", "29900"}, // ★ 充值尺子价（分/百万 token＝¥299/百万，S1 口径修复；旧键 price_fen_per_token 已废弃）
		{"points_tokens_rate", "300"},             // ★ S1 积分制：1 积分 = 300 内部计量 token（对外只露积分，防成本反推）
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
	cut := time.Now().UTC().Add(-time.Duration(minutes) * time.Minute).Format(time.RFC3339)
	// ★ USDT 收款（2026-09-15）：链上转账确认以分钟计，15 分钟窗口不适用——
	//   usdt 单按 usdt_orders.expires_at（下单快照 24h）到期关单；已点「我已付费」的
	//   同样豁免（待人工/自动核实，防「钱付了单没了」资损）；无 meta 的异常单按普通窗口兜底。
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(), `UPDATE orders SET status='cancelled'
		WHERE status='pending'
		  AND NOT ((channel='manual' OR channel='usdt') AND manual_confirm=1)
		  AND (
			(channel<>'usdt' AND created_at < ?)
			OR (channel='usdt' AND EXISTS (SELECT 1 FROM usdt_orders m WHERE m.order_id=orders.id AND m.expires_at<=?))
			OR (channel='usdt' AND NOT EXISTS (SELECT 1 FROM usdt_orders m WHERE m.order_id=orders.id) AND created_at < ?)
		  )`, cut, nowUTC, cut)
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
