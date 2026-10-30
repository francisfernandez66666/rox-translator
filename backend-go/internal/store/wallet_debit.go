// ============ wallet_debit.go · 职责说明 ============
// 扣档与拆扣的唯一实现（★ 2026-10-30 三事合一批 组3，W3／W4／W5／W6／W12／W22 的执行体）。
//
// 这一条链上"静默扣错钱"的形态从来不是某条腿写反，而是**同一笔钱被几把尺子各量一次**
// （AGENTS §一·11 那批 bug 的根因），所以本文件把三件事收在一起：
//
//	① **次序**只能来自 `TierOrderPred()`（wallet_tier.go），这里不再排一次；
//	② **归属**只有一条 SQL：个人档 ∪ 租户共享档在**同一次查询**里取回（W5）——
//	   拆成"先查个人、不足再查租户"两条腿，迟早有一条读到旧数据，而且第二条永远看不出漏了谁；
//	③ **每一档扣了多少**必须逐行落 `usage_debit_lines`（W3）：退款按原档回补（W13）、
//	   账单分档展示、`SUM(debit)=SUM(实扣)` 对账（W23）三件事都只有这一份事实源能回答。
//	   只写一行合计＝把"从哪几档扣的"这个问题永久问不出来。
//
// 三条不许放宽的边界：
//
//	· `user_id>0` 的行**只该成员可核销**（W6）：把别人的注册赠送/任务奖励扣进我的账＝数据事故级；
//	  `user_id=0` 是租户共享池（套餐），任何成员可核销，但仍受"用户可用池"那道独立判据约束（§4.5，组3 后半）。
//	· **不足才欠费**（W12）：`ErrInsufficientBalance` 的触发条件是"全部档位合计不够"，
//	  第一档不够而后面够，属正常拆扣，绝不许报错——旧实现按到期日单维排序时不会出这个错，
//	  分档之后如果判错，表现是"客户明明有余额却被告知欠费"。
//	· A1 守卫冲突→重读→重试那三段语义**一字不动**（同一事务内条件 UPDATE 冲突属瞬态，
//	  重取该行真实余额继续核销而非误报欠费；SQLite IMMEDIATE 下守卫恒真，PG 多实例才是它的战场）。
//
// ⚠️ 写点纪律（W22）：除本文件与 `wallet_refund.go` 之外，任何地方直改 `quota_grants."left"` 都算第二把尺子，
// 由 `wallet_writepoint_gate_test.go` 按写点全集扫（AGENTS §一·13 缓存写点闸同一手法）。
// =============================================
package store

import (
	"database/sql"
	"fmt"
	"time"
	"translator/internal/db"
)

// debitLine 一次拆扣中「从哪一档、哪一行台账、扣掉多少」的一条明细。
type debitLine struct {
	Tier     string
	GrantID  int64 // TierLegacy（永久余额兜底）为 0
	OwnerUID int64 // 这一档台账行的归属（0＝租户共享池）
	Tokens   int64
}

// deductWalletTx 按六档次序核销 tokens，返回逐档明细（调用方落账后必须原样写进 usage_debit_lines）。
// 参数 tx: 调用方已 Begin 的事务（DSN 带 _txlock=immediate，SQLite 侧即 BEGIN IMMEDIATE）；
// tid: 租户；uid: **本次消费的实际用户**（0＝不知道是谁/平台任务，只能吃共享池）；tokens: 应扣量；
// skipSharedPlan: 该用户本月是否已不能用租户共享的 plan_* 档（D-9，由 `UserSharedPlanBlocked` 现算，
// 在**开事务之前**判——它要读部门子树用量，与扣减不是同一把锁，属软判据，见 wallet_user_pool.go）。
// 返回 error 时调用方必须整体回滚——扣了台账却没落成流水，就是"扣钱无流水"那个对账缺口。
func deductWalletTx(tx *sql.Tx, tid, uid, tokens int64, skipSharedPlan bool) ([]debitLine, error) {
	if tokens <= 0 {
		return nil, nil
	}
	d := db.CurrentDialect()
	// 确保账户行存在（等价 EnsureBalance 的 tx 内联版）
	if _, err := db.Exec(tx, d,
		"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tid, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return nil, err
	}
	lock := ""
	if d.IsPostgres() {
		lock = " FOR UPDATE" // 行锁串行化：后到事务在首行等待前者提交，再读到最新余额
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// ② 个人档 ∪ 共享档**同一次**取回（W5 的正靶：这里只许有一条 SELECT）。
	//    次序整段只来自 TierOrderPred()（三桶 + 桶内先到期 + 档位号 + id 兜底，见 wallet_tier.go 文件头），
	//    调用侧**不再补** expires_at/id——补一次就是长出第五把尺子。
	rows, err := db.Query(tx, d,
		fmt.Sprintf(`SELECT id, user_id, tier, "left" FROM quota_grants
		   WHERE tenant_id=? AND "left">0 AND expires_at>? AND (user_id=? OR user_id=0)
		   ORDER BY %s`, TierOrderPred())+lock,
		tid, now, uid)
	if err != nil {
		return nil, err
	}
	type grantRow struct {
		id, owner, left int64
		tier            string
	}
	var grants []grantRow
	for rows.Next() {
		var g grantRow
		var tier sql.NullString
		if err := rows.Scan(&g.id, &g.owner, &tier, &g.left); err == nil {
			g.tier = tier.String
			grants = append(grants, g)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	need := tokens
	var lines []debitLine
	for _, g := range grants {
		if need <= 0 {
			break
		}
		// D-9（wallet_user_pool.go）：部门月度预算已满 ⇒ **跳过租户共享的 plan 档**，
		// 个人档与每日免费照吃。只跳不报错——"第一档不够但后面够"属正常拆扣（W12 同族）。
		if skipSharedPlan && sharedPlanTierOf(tierOrDefault(g.tier), g.owner) {
			continue
		}
		// ★ A1 守卫冲突→重读→重试（语义一字不动）
		left := g.left
		for attempt := 0; attempt < 3 && need > 0 && left > 0; attempt++ {
			use := left
			if use > need {
				use = need
			}
			res, err := db.Exec(tx, d, `UPDATE quota_grants SET "left"="left"-? WHERE id=? AND "left">=?`, use, g.id, use)
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				need -= use
				lines = append(lines, debitLine{Tier: tierOrDefault(g.tier), GrantID: g.id, OwnerUID: g.owner, Tokens: use})
				break
			}
			if err := db.QueryRow(tx, d, `SELECT "left" FROM quota_grants WHERE id=?`, g.id).Scan(&left); err != nil {
				return nil, err
			}
		}
	}
	if need > 0 {
		// 兜底桶：历史永久余额（balance_accounts）。它也是"一档"，必须留明细——
		// 退款时这一档要回补到永久余额而不是某个台账行（W13 的第三段）。
		res, err := db.Exec(tx, d, "UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
			need, time.Now().UTC().Format(time.RFC3339), tid, need)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrInsufficientBalance
		}
		lines = append(lines, debitLine{Tier: TierLegacy, GrantID: 0, OwnerUID: 0, Tokens: need})
		need = 0
	}
	// W12 的反向哨兵：走到这里 need 必须为 0。非 0 只可能是上面某条腿把扣减量吞了
	// （比如守卫重试三圈耗尽），那种形态**绝不能当成扣成功**——宁可报欠费，也不给一个不存在的余额背书。
	if need > 0 {
		return nil, ErrInsufficientBalance
	}
	return lines, nil
}

// tierOrDefault 把台账行上空的档位读数折成 legacy。
// 空串只可能是"这台库还没跑过回填"或旁路 SQL 插入的行；扣减顺序上它排最后（TierRank 的 ELSE 那一支），
// 但**出栈与明细里不许出现空串**——前端按 tier 取词，空串会渲染成一个没有名字的档，
// 对账按 tier 聚合也会多出一组无法解释的行。
func tierOrDefault(tier string) string {
	if tier == "" {
		return TierLegacy
	}
	return tier
}

// recordDebitLines 把一次拆扣的明细落 usage_debit_lines。
// 参数 ledgerID: 本次用量流水 id（调用方**先落 ledger 再写明细**，同一事务内，
// 所以 ledger 插入失败时明细跟着一起回滚；反序就会留下"有明细无流水"的孤儿行）。
// ⚠️ lines 为空（本次没真扣任何东西）时静默返回——这不是异常：0 用量的调用方本来就不该有明细。
func recordDebitLines(tx *sql.Tx, tid, ledgerID int64, lines []debitLine) error {
	if len(lines) == 0 {
		return nil
	}
	d := db.CurrentDialect()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, l := range lines {
		if l.Tokens <= 0 {
			continue
		}
		if _, err := db.Exec(tx, d,
			"INSERT INTO usage_debit_lines (ledger_id, tenant_id, user_id, tier, grant_id, tokens, created_at) VALUES (?,?,?,?,?,?,?)",
			ledgerID, tid, l.OwnerUID, l.Tier, l.GrantID, l.Tokens, now); err != nil {
			return fmt.Errorf("写扣档明细失败: %w", err)
		}
	}
	return nil
}

// debitLinesOfLedger 读某条流水的逐档明细（退款与对账的唯一入口）。
// 返回空切片有两种含义，调用侧必须分得开：**这笔没扣过钱**（免费/留痕行），
// 或者**这笔是拆扣上线前的旧账**（旧账没有明细行）——后者退款只能走旧口径，不许伪造明细。
func (s *Store) debitLinesOfLedger(ledgerID int64) ([]debitLine, error) {
	if ledgerID <= 0 {
		return nil, nil
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT tier, grant_id, user_id, tokens FROM usage_debit_lines WHERE ledger_id=? ORDER BY id ASC`, ledgerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []debitLine
	for rows.Next() {
		var l debitLine
		if err := rows.Scan(&l.Tier, &l.GrantID, &l.OwnerUID, &l.Tokens); err == nil {
			out = append(out, l)
		}
	}
	return out, rows.Err()
}
