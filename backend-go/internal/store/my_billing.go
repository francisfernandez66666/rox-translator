// ============================================================================
// store/my_billing.go — 自服务账单数据层（★ F8）
// 用户侧（非管理员亦可）查询自己的用量趋势/台账明细分页。
// 订单/奖励/发票复用既有 List* 小结果集（页内切片在 API 层完成）。
// ============================================================================
package store

import "translator/internal/db"

// DailyUsagePoint 单日用量点（UTC 日期口径，与 C22 台账域一致）。
type DailyUsagePoint struct {
	Date  string `json:"date"`  // YYYY-MM-DD（UTC）
	Cost  int64  `json:"cost"`  // 当日扣费 token
	Count int64  `json:"count"` // 当日流水笔数
}

// MyDailyUsage 近 N 天「当前用户」每日消耗序列（升序返回，缺日无行——前端按日期轴补零）。
// 参数：tid=租户 ID；uid=用户 ID；days=回看天数（默认 30，上限 90）。
// ★ 2026-09-29 〇-AD（补丁三）：只数客户自己的用量（CustomerUsagePred）——自服务账单里的「每日消耗」
//
//	必须与客户实际掉的积分一致，留痕行（平台承担的 embed/采集/免费期）出现在这里
//	就是「账单上有、余额里没有」，客户照着对账永远对不平。
func (s *Store) MyDailyUsage(tid, uid int64, days int) []DailyUsagePoint {
	if days <= 0 || days > 90 {
		days = 30
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT substr(created_at,1,10) d, COALESCE(SUM(cost),0), COUNT(*) FROM usage_ledger
		 WHERE tenant_id=? AND user_id=? AND `+CustomerUsagePred+` GROUP BY d ORDER BY d DESC LIMIT ?`, tid, uid, days)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []DailyUsagePoint
	for rows.Next() {
		var p DailyUsagePoint
		if rows.Scan(&p.Date, &p.Cost, &p.Count) == nil {
			out = append(out, p)
		}
	}
	// DESC 收集后翻转为时间升序（图表友好）
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// MyLedgerPage 当前用户用量台账分页（可选 biz_kind=text/file 过滤）。
// 返回：行（id 倒序）、总笔数。
// ★ 2026-09-29 〇-AD（补丁三）：加客户面谓词 CustomerUsagePred——这是客户能逐条翻的「我的流水」，
//
//	留痕/结算行必须与 MyDailyUsage、usage/me 同进同出，否则同一账户两张面孔
//	（汇总 0 元、明细里躺着几百积分）。总数与行共用同一 where，天然一致。
func (s *Store) MyLedgerPage(tid, uid int64, bizKind string, limit, offset int) ([]*UsageLedger, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	d := db.CurrentDialect()
	where := "tenant_id=? AND user_id=? AND " + CustomerUsagePred
	args := []interface{}{tid, uid}
	if bizKind != "" {
		where += " AND COALESCE(biz_kind,'')=?"
		args = append(args, bizKind)
	}
	var total int64
	_ = db.QueryRow(s.db, d, "SELECT COUNT(*) FROM usage_ledger WHERE "+where, args...).Scan(&total)
	queryArgs := append(append([]interface{}{}, args...), limit, offset)
	rows, err := db.Query(s.db, d,
		"SELECT id, tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, COALESCE(biz_kind,''), COALESCE(biz_mode,''), created_at FROM usage_ledger WHERE "+
			where+" ORDER BY id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*UsageLedger
	for rows.Next() {
		var u UsageLedger
		if err := rows.Scan(&u.ID, &u.TenantID, &u.UserID, &u.TaskType, &u.Provider, &u.Model, &u.Quantity, &u.UnitPrice, &u.Cost, &u.BizKind, &u.BizMode, &u.CreatedAt); err != nil {
			continue
		}
		out = append(out, &u)
	}
	return out, total, nil
}
