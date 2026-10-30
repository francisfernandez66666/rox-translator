// ============ billing_refund_wallet.go · 职责说明 ============
// 按档退款（★ 2026-10-30 三事合一批 组3 #8，W13／W14／W15 的执行体）。
//
// 一句话：**退一笔消耗时，钱要退回它当初被扣走的那几档**，不是统统打回永久余额。
//
// 为什么必须有这个文件（B7 的原话是"没有它退款必然回补错桶"）：
//
//	拆扣上线前，一笔扣款的来源只有一个桶（永久余额），"退款"因此可以粗糙地理解成
//	"往 balance_accounts 加一笔"。拆扣之后同一笔钱可能来自注册赠送／任务奖励／月包／年包，
//	把它们全部打回 balance_accounts ＝ **把有时效的免费额度洗成永久积分**：
//	客户白赚一段无期限余额，而那一档原本该在到期日作废——账面上合计还对得上，
//	只有下一次对账算到期分布时才会发现总量对不上，而那时已经无法还原"哪笔退错了"。
//
// 三条不许放宽的边界（逐条对应一条断言）：
//
//	① **回补不得超过原始扣减量**（W14）：`tokens - refunded_tokens` 才是这一次还能回补的量，
//	   台账侧再加一道 `"left"+? <= total` 守卫——两道闸各堵一种刷法：
//	   前者堵"同一笔退两次"，后者堵"客户退完又花掉、再退一次把余额顶穿 total"。
//	② **已过期的原档不回补**（W15）：回补进一个已作废的桶＝凭空造出额度。
//	   本批口径是"不回补 ＋ WARN ＋ 读数点名"（U-2 未决项），**绝不静默**——
//	   静默的形态是"客户说退的钱没到账"，而日志里什么都没有。
//	③ **台账行已被删掉的那一条不回补**：那说明这一档已经被人整体作废（例如任务奖励回收），
//	   照着 grant_id 重新 INSERT 一条行就是复活一笔已收回的额度。只记读数。
//
// 射程与调用面（写在这里防下一位误读）：
//
//	本函数按**用量流水**（usage_ledger.id）退款，一次只退一条流水的全部拆扣明细。
//	当前产品口径仍是「已消耗不退还」（billing_api.go 的注释），所以**没有自动调用点**；
//	它是订单退款/商务折让/可用性补偿要按档回补时的**唯一出口**（U-2 落地时接这里，
//	禁止在调用方各自往 balance_accounts 加钱）。订单自身"收回未消耗份额"那一条腿
//	走的是 RefundOrder（billing.go），本次同批改成"有台账行就按台账收回"，见那里的注释。
//
// 观测口径：过期跳过的量与条数由 `WalletRefundResult` 交回调用方，本文件另打一行 WARN
// （含 tenant/tier/tokens，**不含**用户邮箱等 PII）。`SUM(tokens)` 与 `SUM(refunded_tokens)`
// 就是 W13 那条对账等式，不需要另建退款流水表。
// =============================================
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
)

// WalletRefundResult 一次按档回补的读数（调用方据此出对账与告警，不要只看 Refunded）。
type WalletRefundResult struct {
	Refunded       int64 // 实际回补成功的总量
	ExpiredSkipped int64 // 原档已过期而未回补的量（U-2：不回补，只点名）
	MissingSkipped int64 // 台账行已不存在而未回补的量
	CappedSkipped  int64 // 被 `left+?<=total` 守卫挡下的量（客户退完又花掉那一段）
	Lines          int   // 处理过的明细条数（含 0 回补的）
}

// RefundUsageByLedger 按某条用量流水的拆扣明细逐档回补（幂等：同一 ledgerID 退两次，第二次全 0）。
// 参数 ledgerID: usage_ledger.id；<=0 或没有明细（免费留痕行／拆扣上线前的旧账）时返回零值且不报错——
// 那两类的正确处置都是"没有可退的档"，不是"退到永久余额"（旧账回补口径属 U-2，不许在这里猜）。
func (s *Store) RefundUsageByLedger(ledgerID int64) (WalletRefundResult, error) {
	var out WalletRefundResult
	if s == nil || s.db == nil || ledgerID <= 0 {
		return out, nil
	}
	tx, err := s.db.Begin() // _txlock=immediate：与扣减同一把锁，退款与扣减不会互相踩
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	d := db.CurrentDialect()
	rows, err := db.Query(tx, d,
		`SELECT id, tier, grant_id, tenant_id, tokens, COALESCE(refunded_tokens,0)
		   FROM usage_debit_lines WHERE ledger_id=? ORDER BY id ASC`, ledgerID)
	if err != nil {
		return out, err
	}
	type line struct {
		id, grantID, tid, tokens, refunded int64
		tier                               string
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.id, &l.tier, &l.grantID, &l.tid, &l.tokens, &l.refunded); err == nil {
			lines = append(lines, l)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	now := time.Now().UTC()
	for _, l := range lines {
		out.Lines++
		alloc := l.tokens - l.refunded
		if alloc <= 0 {
			continue // 这一条已经退过（W14 的幂等腿）
		}
		var got int64
		if l.grantID == 0 {
			// TierLegacy：这一档本来就住在 balance_accounts（历史永久余额），原路回补
			if _, err := db.Exec(tx, d,
				"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
				l.tid, now.Format(time.RFC3339)); err != nil {
				return out, err
			}
			if _, err := db.Exec(tx, d,
				"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
				alloc, now.Format(time.RFC3339), l.tid); err != nil {
				return out, err
			}
			got = alloc
		} else {
			var total, left int64
			var expires string
			err := db.QueryRow(tx, d, `SELECT total, "left", expires_at FROM quota_grants WHERE id=? AND tenant_id=?`,
				l.grantID, l.tid).Scan(&total, &left, &expires)
			if err == sql.ErrNoRows {
				out.MissingSkipped += alloc // ③ 行已被整体作废：不复活
				continue
			}
			if err != nil {
				return out, err
			}
			// ② 已过期不回补（字符串字典序比较与全仓 expires_at 口径一致：均为 UTC RFC3339）
			if expires <= now.Format(time.RFC3339) {
				out.ExpiredSkipped += alloc
				observability.Warn(context.Background(), "按档退款跳过已过期档（不回补，只点名）",
					"ledger_id", ledgerID, "tenant_id", l.tid, "grant_id", l.grantID, "tier", tierOrDefault(l.tier), "tokens", alloc)
				continue
			}
			// ① 两个上限取小：本次还能退的量 ∧ 该行还剩的容量（客户退完又花掉时只补回容量）
			if room := total - left; room < alloc {
				out.CappedSkipped += alloc - room
				alloc = room
			}
			if alloc > 0 {
				res, err := db.Exec(tx, d,
					`UPDATE quota_grants SET "left"="left"+? WHERE id=? AND tenant_id=? AND "left"+?<=total`,
					alloc, l.grantID, l.tid, alloc)
				if err != nil {
					return out, err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					out.CappedSkipped += alloc
					continue
				}
				got = alloc
			}
		}
		if got <= 0 {
			continue
		}
		// 幂等标记：带 `refunded_tokens=?` 乐观守卫，并发双退只有一方写进去
		res, err := db.Exec(tx, d,
			"UPDATE usage_debit_lines SET refunded_tokens=refunded_tokens+? WHERE id=? AND refunded_tokens=?",
			got, l.id, l.refunded)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return out, fmt.Errorf("按档退款并发冲突（ledger=%d line=%d）：另一个退款已改过这一行，本次整体回滚", ledgerID, l.id)
		}
		out.Refunded += got
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	if out.Refunded > 0 {
		// 余额行变了 ⇒ 通知影子失效（与扣减同一条多实例闭环，漏通知由影子 TTL 兜底）
		var tid int64
		if len(lines) > 0 {
			tid = lines[0].tid
		}
		if tid > 0 {
			notifyTenantBalanceChanged(tid)
		}
	}
	return out, nil
}
