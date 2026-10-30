// ============ wallet_query.go · 职责说明 ============
// 钱包的**出栈读腿**（★ 2026-10-30 三事合一批 组3 #10，D-12／W20／W21）。
//
// 一句话：客户面看到的是「合计优先、可展开逐档」，而**每日免费那一档不出数字**。
//
// 三条口径（每一条都对应一条断言或一条红线）：
//
//	① **聚合口径与 `points_grants_left` 完全同一把尺子**（W21）：同一时刻
//	   `SUM(wallet[].tokens) == TenantRemainTotal 的 grants`。两处各算一次是刻意的——
//	   等价由测试钉住，而不是让面板去"求和凑总数"（凑出来的总数会让 `points_balance` 这一列
//	   与它的来源脱钩，正是 §一·11 那族）。谓词固定为 `"left">0 AND expires_at>now`，
//	   与 TenantRemainTotal 逐字相同。
//	② **`free` 档永不出栈**（W20）：每日免费是 UTC 日计数器（ratelimit_day.go），不落台账，
//	   把它的余量当钱显示出来，等于把"今天还能白嫖多少"直接写在界面上（G3/G18/W-4）。
//	   它也不在这个表的查询里——`tier='free'` 的行在库中根本不存在；本函数额外再排一次，
//	   防的是有人日后把免费额度也落成台账行。
//	③ **未知档位并入 `legacy`**：与 `tierOrDefault` 同一取向（前端按 tier 取词，
//	   空串会渲染成一个没有名字的档；对账按 tier 聚合也会多出无法解释的一组）。
//
// 出栈字段：tier（对外契约字面）／tokens（**token 口径**，api 层再折积分）／
// expires_at（该档最早到期时刻；长期档是 9999 哨兵，前端按年份判"永久"）／grants（行数，排障用）。
// =============================================
package store

import (
	"sort"
	"time"

	"translator/internal/db"
)

// WalletTierRow 一档钱包的汇总行。
type WalletTierRow struct {
	Tier      string `json:"tier"`
	Tokens    int64  `json:"tokens"`
	ExpiresAt string `json:"expires_at"`
	Grants    int64  `json:"grants"`
}

// WalletTiers 按档位聚合该租户**未过期**的台账余额，按扣减次序（TierNamesInOrder）出栈。
// 参数 tid: 租户。返回空切片而不是 nil（HTTP 层要出 `[]`，同 §一·5 那条"空页归一成 []"）。
func (s *Store) WalletTiers(tid int64) ([]WalletTierRow, error) {
	out := []WalletTierRow{}
	if s == nil || s.db == nil || tid <= 0 {
		return out, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT COALESCE(NULLIF(tier,''),?), COALESCE(SUM("left"),0), COALESCE(MIN(expires_at),''), COUNT(*)
		   FROM quota_grants
		  WHERE tenant_id=? AND "left">0 AND expires_at>? AND COALESCE(tier,'')<>?
		  GROUP BY COALESCE(NULLIF(tier,''),?)`,
		TierLegacy, tid, now, TierFree, TierLegacy)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	byTier := map[string]*WalletTierRow{}
	for rows.Next() {
		var r WalletTierRow
		if err := rows.Scan(&r.Tier, &r.Tokens, &r.ExpiresAt, &r.Grants); err != nil {
			continue
		}
		byTier[r.Tier] = &r
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, tier := range TierNamesInOrder() {
		if tier == TierFree {
			continue // ② 免费档永不出栈
		}
		if r, ok := byTier[tier]; ok {
			out = append(out, *r)
		}
	}
	// 名单之外的档位（将来新增却还没登记）排最后，仍出栈——**不许吞掉**：
	// 吞掉就是"客户账上有余额，面板上找不到"，对账时比多一行更难查。
	var extra []WalletTierRow
	for tier, r := range byTier {
		if TierRank(tier) == tierRankUnknown {
			extra = append(extra, *r)
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Tier < extra[j].Tier })
	return append(out, extra...), nil
}
