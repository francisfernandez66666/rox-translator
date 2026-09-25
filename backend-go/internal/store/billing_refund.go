// ============ billing_refund.go · 职责说明 ============
// store 包退款域窄化文件（AGENTS §一·1：billing.go 只减不增，退款新增逻辑落此）。
// 当前承载两枚 UAT 修复批新增 helper：
//   - reclaimCarryOfRefundedUpgradesTx ★ F-36：升级结转（order_carry）份额的闭环回收（事务内）；
//   - LatestActivePaidSubscription     ★ F-37：退款后订阅身份改挂「其他在期订阅」（事务外只读查）。
//
// 两函数都跑在 RefundOrder 已开启的事务句柄内，不新起连接（历史教训：
// 事务内经独立连接写库撞 busy_timeout 被吞，见 billing.go 退款尾部告警注释）。
// =============================================
package store

import (
	"database/sql"
	"time"
	"translator/internal/db"
)

// BillingRefundMigrate ★ F-34（2026-09-25 UAT 修复批）幂等迁移：
// orders 补 reopen_from_order 列（超时取消的静态码单 → 补审重建单指回原单），
// 使 ReopenManualOrder 具备「同原单已有在途补审单则复用」的防重判据，
// 并为 F-36 的 upgrade_from_order 反查留同型范式。老行默认 0（无来源）。
// 参数：无（随 Store.New 调用）；补列失败静默容忍口径与 TicketQualityMigrate 一致
// （EnsureColumns 自身幂等，启动期失败由后续调用的 SQL 报错暴露）。
func (s *Store) BillingRefundMigrate() {
	_ = db.EnsureColumns(s.db, db.CurrentDialect(), "orders", map[string]string{
		"reopen_from_order": "INTEGER NOT NULL DEFAULT 0",
	})
}

// reclaimCarryOfRefundedUpgradesTx ★ F-36（2026-09-25 UAT 修复批）：
// 退还订单 X 时，凡「由 X 升级而来且已退款」的子单，其升级结转份额
// （quota_grants source='order_carry'、ref_id=子单）一并作废。
// 背景：升级下单把旧单剩余台账转插为 carry 行（billing.go 升级分支），
// 退子单时 A3 口径有意豁免 carry（其价值对应旧单已付对价）；但旧单自身
// 退款路径此前没有任何按 upgrade_from_order 的反查——「先退新后退旧」可白嫖
// carry 额度（生产实测幽灵 3,000 积分）。本函数封死该闭环。
// 语义边界：子单仍 paid 时 carry 保留（合法：对应旧单折抵的对价仍在期内）。
// 参数：tx=RefundOrder 事务句柄，d=当前方言，tid=租户 ID，orderID=正在退款的（旧）订单 ID。
// 返回：被清零的 carry 份额 token 合计（0=无可回收）。
func (s *Store) reclaimCarryOfRefundedUpgradesTx(tx *sql.Tx, d db.Dialect, tid, orderID int64) (int64, error) {
	var claw int64
	if err := db.QueryRow(tx, d,
		`SELECT COALESCE(SUM("left"),0) FROM quota_grants
		 WHERE tenant_id=? AND source='order_carry' AND "left">0
		   AND ref_id IN (SELECT id FROM orders WHERE upgrade_from_order=? AND status='refunded')`,
		tid, orderID).Scan(&claw); err != nil {
		return 0, err
	}
	if claw <= 0 {
		return 0, nil
	}
	if _, err := db.Exec(tx, d,
		`UPDATE quota_grants SET "left"=0
		 WHERE tenant_id=? AND source='order_carry' AND "left">0
		   AND ref_id IN (SELECT id FROM orders WHERE upgrade_from_order=? AND status='refunded')`,
		tid, orderID); err != nil {
		return 0, err
	}
	return claw, nil
}

// LatestActivePaidSubscription ★ F-37（2026-09-25 UAT 修复批）：
// 查租户当前「仍在有效期内」的订阅付费包（ptype=paid、status=paid），
// 供退款处理器在退掉身份来源单后把订阅身份改挂到剩余最晚支付的一笔订阅，
// 而非一律清空（旧实现无条件 perms.PackageCode=""，双订阅退错一笔即误杀现役身份）。
// 在期判据：paid_at + 包 duration_days 未过期（duration_days<=0 视为不过期，随包配置语义）。
// 参数：tid=租户 ID，excludeOrderID=刚被退款/需排除的订单 ID。
// 返回：包 code、到期时间（RFC3339，永不过期包为 ""）、是否存在（false=已无在期订阅）。
func (s *Store) LatestActivePaidSubscription(tid, excludeOrderID int64) (string, string, bool, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT p.code, COALESCE(o.paid_at,''), p.duration_days
		 FROM orders o JOIN packages p ON p.id=o.package_id
		 WHERE o.tenant_id=? AND o.status='paid' AND COALESCE(p.ptype,'')='paid' AND o.id<>?
		 ORDER BY o.paid_at DESC`,
		tid, excludeOrderID)
	if err != nil {
		return "", "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var code, paidAt string
		var days int64
		if err := rows.Scan(&code, &paidAt, &days); err != nil {
			continue
		}
		if paidAt == "" {
			continue
		}
		// 到期判定在 Go 侧算，避开 SQLite/PG 日期函数方言差（AGENTS §一·4）
		if days > 0 {
			t, perr := time.Parse(time.RFC3339, paidAt)
			if perr != nil {
				continue
			}
			exp := t.AddDate(0, 0, int(days))
			if !exp.After(time.Now()) {
				continue // 已过期不算「在期订阅」
			}
			return code, exp.UTC().Format(time.RFC3339), true, nil
		}
		return code, "", true, nil
	}
	return "", "", false, nil
}
