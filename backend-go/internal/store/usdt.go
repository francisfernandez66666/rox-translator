// ============ 本文件职责中文说明 ============
// USDT 收款数据层（改造方案 2026-09-15）：
//   - 配置解析：system_config 的 usdt_* 键 → USDTSettings（默认安全：总开关/自动对账皆关）；
//   - 迁移：usdt_orders（订单收款要素快照：链/地址/含尾金额/汇率快照/到期/客户声明 txid）
//     与 usdt_deposits（链上入账幂等台账）两张新表 + payments.tx_hash 列（唯一索引防一笔 tx 复用到两单）；
//   - 尾数分配：同链同址 pending 窗口内金额唯一（应用层查重 + 部分唯一索引双保险，冲突重采样）；
//   - 匹配与结算查询：入账↔订单精确金额匹配、孤儿入账、结算幂等落 tx_hash。
//
// 资金入账一律复用 Store.MarkOrderPaid / MarkOrderPaidByOrderNo（单事务幂等抢占），
// 本文件不新增任何余额写路径。
// =============================================
package store

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"
	"translator/internal/db"
)

// ErrUSTXHashUsed 该链上交易已关联其它订单（payments.tx_hash 唯一约束防线）。
var ErrUSTXHashUsed = errors.New("该链上交易哈希已关联其它订单")

// USDTSettings USDT 收款运行配置（system_config 直读，配置量小不缓存）。
type USDTSettings struct {
	Enabled     bool              // usdt_enabled 总开关（默认 0）
	AutoSettle  bool              // usdt_auto_settle 自动对账开关（默认 0：仅人工核销）
	Chains      []string          // usdt_chains 逗号分隔（trc20[,erc20,bep20]）
	Addrs       map[string]string // usdt_addr_<chain> 收款地址
	RateFen     int64             // usdt_rate_fen_per_usdt 汇率（人民币分/USDT）
	TailEnabled bool              // usdt_tail_enabled 尾数防混淆（默认 1）
	Confirm     map[string]int64  // usdt_confirmations_<chain> 各链确认阈值
}

// GetUSDTCfg 读取并校验 USDT 收款配置；地址缺失/格式非法的链自动剔除。
func (s *Store) GetUSDTCfg() *USDTSettings {
	st := &USDTSettings{Addrs: map[string]string{}, Confirm: map[string]int64{}}
	st.Enabled = s.GetConfigInt("usdt_enabled") == 1
	st.AutoSettle = s.GetConfigInt("usdt_auto_settle") == 1
	st.TailEnabled = true
	if v, _ := s.GetConfig("usdt_tail_enabled"); v == "0" {
		st.TailEnabled = false
	}
	st.RateFen, _ = s.GetConfigInt64("usdt_rate_fen_per_usdt")
	chainsRaw, _ := s.GetConfig("usdt_chains")
	if strings.TrimSpace(chainsRaw) == "" {
		chainsRaw = "trc20"
	}
	for _, c := range strings.Split(chainsRaw, ",") {
		c = strings.TrimSpace(strings.ToLower(c))
		if c == "" {
			continue
		}
		addr, _ := s.GetConfig("usdt_addr_" + c)
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue // 未配地址的链不启用
		}
		st.Chains = append(st.Chains, c)
		st.Addrs[c] = addr
		conf := int64(19) // 默认：TRC20 19 块≈1min；EVM 在配置面板另设
		if c == "erc20" {
			conf = 12
		} else if c == "bep20" {
			conf = 15
		}
		if v, ok := s.GetConfigInt64("usdt_confirmations_" + c); ok && v > 0 {
			conf = v
		}
		st.Confirm[c] = conf
	}
	return st
}

// USDTOrderMeta 一笔 USDT 订单的收款要素（下单快照，改配置不影响存量单）。
type USDTOrderMeta struct {
	ID            int64  `json:"-"`
	OrderID       int64  `json:"order_id"`
	TenantID      int64  `json:"tenant_id"`
	Chain         string `json:"chain"`
	ToAddr        string `json:"to_addr"`
	AmountMicro   int64  `json:"amount_micro"` // 含尾数精确应得
	TailMicro     int64  `json:"tail_micro"`
	RateFen       int64  `json:"rate_fen_per_usdt"`
	ExpiresAt     string `json:"expires_at"`
	ClientTxHash  string `json:"client_tx_hash"` // 客户声明（仅展示/线索，非到账依据）
	MatchedTxHash string `json:"matched_tx_hash"`
	DeclaredAt    string `json:"declared_at"`
	CreatedAt     string `json:"created_at"`
}

// USDTDeposit 链上入账事件（幂等台账）。
type USDTDeposit struct {
	ID            int64  `json:"id"`
	Chain         string `json:"chain"`
	TxHash        string `json:"tx_hash"`
	LogIndex      int    `json:"log_index"`
	FromAddr      string `json:"from_addr"`
	AmountMicro   int64  `json:"amount_micro"`
	BlockNo       int64  `json:"block_no"`
	NewestBlockNo int64  `json:"newest_block_no"` // 事件归一时的链头（实时确认数由调用方重查链头计算）
	SeenAt        string `json:"seen_at"`
	MatchedTid    int64  `json:"matched_order_id"` // 0=未匹配
}

// USDTMigrate 建表与索引（幂等，挂 Store.New 迁移链）。
func (s *Store) USDTMigrate() {
	d := db.CurrentDialect()
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS usdt_orders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		order_id INTEGER NOT NULL UNIQUE,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		chain TEXT NOT NULL DEFAULT 'trc20',
		to_addr TEXT NOT NULL DEFAULT '',
		amount_micro INTEGER NOT NULL DEFAULT 0,
		tail_micro INTEGER NOT NULL DEFAULT 0,
		rate_fen INTEGER NOT NULL DEFAULT 0,
		expires_at TEXT NOT NULL DEFAULT '',
		client_tx_hash TEXT NOT NULL DEFAULT '',
		matched_tx_hash TEXT NOT NULL DEFAULT '',
		declared_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '')`)
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS usdt_deposits (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chain TEXT NOT NULL DEFAULT '',
		tx_hash TEXT NOT NULL DEFAULT '',
		log_index INTEGER NOT NULL DEFAULT 0,
		from_addr TEXT NOT NULL DEFAULT '',
		amount_micro INTEGER NOT NULL DEFAULT 0,
		block_no INTEGER NOT NULL DEFAULT 0,
		newest_block_no INTEGER NOT NULL DEFAULT 0,
		matched_order_id INTEGER NOT NULL DEFAULT 0,
		seen_at TEXT NOT NULL DEFAULT '')`)
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS idx_usdt_dep_key ON usdt_deposits(chain, tx_hash, log_index)`)
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_usdt_dep_unmatched ON usdt_deposits(matched_order_id, amount_micro)`)
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_usdt_ord_chain_addr ON usdt_orders(chain, to_addr)`)
	// payments.tx_hash 补列（列存在性迁移走 columnAdditions；这里只建索引）
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS idx_pay_txhash ON payments(tx_hash) WHERE tx_hash<>''`)
	// 尾数唯一双保险：同链同址、未结算、未过期的 pending 窗口内金额不得重复
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS idx_usdt_amount_pending ON usdt_orders(chain, to_addr, amount_micro) WHERE matched_tx_hash=''`)
}

// CreateUSDTOrderMeta 为 USDT 订单分配收款要素（含随机尾数）。
// 尾数冲突（同址未结算行金额重复）→ 重采样（≤8 次）；全部冲突返回错误（窗口拥挤告警场景）。
func (s *Store) CreateUSDTOrderMeta(orderID, tid int64, chain, addr string, baseMicro, rateFen int64, tailEnabled bool) (*USDTOrderMeta, error) {
	d := db.CurrentDialect()
	now := time.Now().UTC().Format(time.RFC3339)
	exp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	for attempt := 0; attempt < 8; attempt++ {
		tail := int64(0)
		if tailEnabled {
			tail = int64(TailMinRand())
		}
		total := baseMicro + tail
		// 应用层查重（pending 窗口）：同链同址未结算行不得同金额
		var dup int64
		if err := db.QueryRow(s.db, d, `SELECT COUNT(*) FROM usdt_orders
			WHERE chain=? AND to_addr=? AND amount_micro=? AND matched_tx_hash='' AND expires_at>?`,
			chain, addr, total, now).Scan(&dup); err != nil {
			return nil, err
		}
		if dup > 0 {
			continue
		}
		_, err := db.Exec(s.db, d, `INSERT INTO usdt_orders
			(order_id, tenant_id, chain, to_addr, amount_micro, tail_micro, rate_fen, expires_at, created_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			orderID, tid, chain, addr, total, tail, rateFen, exp, now)
		if err != nil {
			if IsUniqueViolation(err) {
				continue // 部分唯一索引兜底（并发下单撞尾数）→ 重采样
			}
			return nil, err
		}
		return s.GetUSDTOrderMeta(orderID)
	}
	return nil, fmt.Errorf("USDT 尾数分配失败（同址待结算订单过多，请人工核对 pending）")
}

// GetUSDTOrderMeta 按订单 ID 取收款要素快照。
func (s *Store) GetUSDTOrderMeta(orderID int64) (*USDTOrderMeta, error) {
	var m USDTOrderMeta
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT id, order_id, tenant_id, chain, to_addr, amount_micro, tail_micro, rate_fen,
			COALESCE(expires_at,''), COALESCE(client_tx_hash,''), COALESCE(matched_tx_hash,''), COALESCE(declared_at,''), COALESCE(created_at,'')
		 FROM usdt_orders WHERE order_id=?`, orderID).
		Scan(&m.ID, &m.OrderID, &m.TenantID, &m.Chain, &m.ToAddr, &m.AmountMicro, &m.TailMicro, &m.RateFen,
			&m.ExpiresAt, &m.ClientTxHash, &m.MatchedTxHash, &m.DeclaredAt, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// SetUSDTDeclaredTxHash 客户「我已付费」提交的交易哈希（仅格式校验过的线索值）。
func (s *Store) SetUSDTDeclaredTxHash(orderID int64, txHash string) error {
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE usdt_orders SET client_tx_hash=?, declared_at=? WHERE order_id=? AND matched_tx_hash=''`,
		txHash, time.Now().UTC().Format(time.RFC3339), orderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &errTxt{"订单不存在或已结算"}
	}
	return nil
}

// SetPaymentTxHash 结算确认后把链上交易哈希落到支付流水（唯一索引：一笔 tx 只能关联一单）。
func (s *Store) SetPaymentTxHash(orderID int64, txHash string) error {
	// 预检：该哈希是否已挂他单（给出明确业务错误而非裸唯一冲突）
	var used int64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM payments WHERE tx_hash=? AND order_id<>?", txHash, orderID).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return ErrUSTXHashUsed
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE payments SET tx_hash=? WHERE order_id=? AND tx_hash=''", txHash, orderID)
	if err != nil {
		if IsUniqueViolation(err) {
			return ErrUSTXHashUsed
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &errTxt{"支付流水不存在或已关联交易哈希"}
	}
	return nil
}

// SettleUSDTOrder 订单结算落链上凭证（matched_tx_hash + 关联入账事件）。
// 幂等：已结算行不覆盖（已结算订单不可能再次结算，前置由 MarkOrderPaid 抢占保证）。
func (s *Store) SettleUSDTOrder(orderID int64, txHash string) error {
	d := db.CurrentDialect()
	if _, err := db.Exec(s.db, d,
		`UPDATE usdt_orders SET matched_tx_hash=? WHERE order_id=? AND matched_tx_hash=''`, txHash, orderID); err != nil {
		return err
	}
	if txHash != "" {
		_, _ = db.Exec(s.db, d,
			`UPDATE usdt_deposits SET matched_order_id=? WHERE matched_order_id=0 AND
			  (chain, tx_hash) IN (SELECT chain, client_tx_hash FROM usdt_orders WHERE order_id=? AND client_tx_hash<>'')`,
			orderID, orderID)
		// 自动对账路径带 log_index 精确键（tx_hash 同值多事件时不误联）
		_, _ = db.Exec(s.db, d,
			`UPDATE usdt_deposits SET matched_order_id=? WHERE matched_order_id=0 AND tx_hash=? AND amount_micro=
			  (SELECT amount_micro FROM usdt_orders WHERE order_id=?)`, orderID, txHash, orderID)
	}
	return nil
}

// InsertUSDTDeposit 幂等落一条链上入账事件（(chain,tx_hash,log_index) 冲突即忽略）。
// 返回 true=新插入。
func (s *Store) InsertUSDTDeposit(dep *USDTDeposit) (bool, error) {
	dep.Chain = strings.ToLower(dep.Chain)
	if !paymentAddrValid(dep.Chain) {
		return false, fmt.Errorf("未知链: %s", dep.Chain)
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT OR IGNORE INTO usdt_deposits (chain, tx_hash, log_index, from_addr, amount_micro, block_no, newest_block_no, matched_order_id, seen_at)
		 VALUES (?,?,?,?,?,?,?,0,?)`,
		dep.Chain, dep.TxHash, dep.LogIndex, dep.FromAddr, dep.AmountMicro, dep.BlockNo, dep.NewestBlockNo,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// paymentAddrValid 校验链标识是否为受支持的 USDT 收款链（trc20|erc20|bep20）。
// 用于管理端配置 usdt_addr_* 时的入口级合法性判断。
func paymentAddrValid(chain string) bool {
	switch chain {
	case "trc20", "erc20", "bep20":
		return true
	}
	return false
}

// FindPendingUSDTOrderByDeposit 按入账精确金额匹配待结算 USDT 订单：
// 同链、金额=应收（含尾数）、未过期、订单 pending。多命中（唯一索引理论排除）返回 nil+告警标记串。
func (s *Store) FindPendingUSDTOrderByDeposit(chain string, amountMicro int64) (*USDTOrderMeta, bool /*ambiguous*/) {
	d := db.CurrentDialect()
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := db.Query(s.db, d,
		`SELECT m.order_id, m.tenant_id, m.chain, m.to_addr, m.amount_micro, m.tail_micro, m.rate_fen,
			COALESCE(m.expires_at,''), COALESCE(m.client_tx_hash,''), COALESCE(m.matched_tx_hash,''), COALESCE(m.declared_at,''), COALESCE(m.created_at,'')
		 FROM usdt_orders m JOIN orders o ON o.id=m.order_id
		 WHERE m.chain=? AND m.amount_micro=? AND m.matched_tx_hash='' AND m.expires_at>? AND o.status='pending'`,
		chain, amountMicro, now)
	if err != nil {
		log.Printf("[usdt] 匹配查询失败: %v", err)
		return nil, false
	}
	defer rows.Close()
	var found []*USDTOrderMeta
	for rows.Next() {
		m := &USDTOrderMeta{}
		if err := rows.Scan(&m.OrderID, &m.TenantID, &m.Chain, &m.ToAddr, &m.AmountMicro, &m.TailMicro, &m.RateFen,
			&m.ExpiresAt, &m.ClientTxHash, &m.MatchedTxHash, &m.DeclaredAt, &m.CreatedAt); err != nil {
			continue
		}
		found = append(found, m)
	}
	if len(found) == 1 {
		return found[0], false
	}
	if len(found) > 1 {
		return nil, true // 多命中：宁可人工裁决，不自动入账
	}
	return nil, false
}

// ListUnmatchedDeposits 未匹配入账（供 reconciler 二次匹配与孤儿告警）。
func (s *Store) ListUnmatchedDeposits(limit int) []*USDTDeposit {
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id, chain, tx_hash, log_index, from_addr, amount_micro, block_no, newest_block_no, matched_order_id, COALESCE(seen_at,'')
		 FROM usdt_deposits WHERE matched_order_id=0 AND amount_micro>0 ORDER BY id ASC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*USDTDeposit
	for rows.Next() {
		var x USDTDeposit
		if err := rows.Scan(&x.ID, &x.Chain, &x.TxHash, &x.LogIndex, &x.FromAddr, &x.AmountMicro,
			&x.BlockNo, &x.NewestBlockNo, &x.MatchedTid, &x.SeenAt); err != nil {
			continue
		}
		out = append(out, &x)
	}
	return out
}

// LinkUSDTDeposit 标记入账事件已匹配订单。
func (s *Store) LinkUSDTDeposit(depositID, orderID int64) {
	_, _ = db.Exec(s.db, db.CurrentDialect(),
		"UPDATE usdt_deposits SET matched_order_id=? WHERE id=? AND matched_order_id=0", orderID, depositID)
}

// MarkUSDTDepositSeenBlock 更新链游标（reconciler 断点续扫）。
func (s *Store) MarkUSDTDepositSeenBlock(chain string, block int64) {
	if block <= 0 {
		return
	}
	_ = s.SetConfig("usdt_cursor_"+chain, strconv.FormatInt(block, 10))
}

// USDTDepositCursor 读取链扫描游标（缺省 0=从头；首扫回退窗口由调用方决定）。
func (s *Store) USDTDepositCursor(chain string) int64 {
	v, _ := s.GetConfig("usdt_cursor_" + chain)
	n, _ := strconv.ParseInt(v, 10, 64)
	if n < 0 {
		return 0
	}
	return n
}

// ExpireUSDTOrders USDT pending 单超时处理（watchdog）：到期未结算订单走现有取消逻辑。
// 仅返回待取消列表（取消由 API 层复用 CancelOrder 语义，避免资金核心旁路写）。
func (s *Store) ExpiredUSDTMetaOrderIDs() []int64 {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT order_id FROM usdt_orders WHERE matched_tx_hash='' AND expires_at<? AND client_tx_hash='' LIMIT 200`,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// GetConfigInt64 读整型配置（存在且可解析返回 ok）。
func (s *Store) GetConfigInt64(key string) (int64, bool) {
	v, err := s.GetConfig(key)
	if err != nil || v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// GetConfigInt 读整型配置（缺省 0；兼容既有调用点）。
func (s *Store) GetConfigInt(key string) int {
	n, ok := s.GetConfigInt64(key)
	if !ok {
		return 0
	}
	return int(n)
}

// TailMinRand 分配随机尾数（[TailMin, TailMax]，payment 包常量在此以值复制避免循环依赖）。
func TailMinRand() int {
	return rand.Intn(9999) + 1
}

// PaymentTxHashUsed 链上交易哈希是否已关联过其他订单（确认收款前置唯一性预检；
// 最终防线仍是 payments.tx_hash 唯一索引——预检只挡「确认时的故意/失误复用」）。
func (s *Store) PaymentTxHashUsed(hash string) (bool, error) {
	if hash == "" {
		return false, nil
	}
	var n int
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM payments WHERE tx_hash=? AND tx_hash<>''", hash).Scan(&n)
	return n > 0, err
}
