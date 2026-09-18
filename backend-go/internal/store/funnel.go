// ============================================================================
// store/funnel.go — S4 增长数据层（★ 2026-09-14 试运营拍板）
// 五环节漏斗（按 utm_source 分组）：注册 → 激活(首笔真实用量) → 额度耗尽 →
// 首购(首笔已付订单) → 续费(≥2 笔已付订单)。数据源=registration_attribution
// + usage_ledger + orders（租户维度）；SQLite/PG 双兼容纯 SQL。
// ============================================================================
package store

import (
	"time"

	"translator/internal/db"
)

// RegAttribution 注册归因快照（UTM 五参 + 邀请码 + 访问域 + UA）。
type RegAttribution struct {
	UserID      int64  `json:"user_id"`
	TenantID    int64  `json:"tenant_id"`
	UTMSource   string `json:"utm_source"`
	UTMMedium   string `json:"utm_medium"`
	UTMCampaign string `json:"utm_campaign"`
	UTMTerm     string `json:"utm_term"`
	UTMContent  string `json:"utm_content"`
	RefCode     string `json:"ref_code"`
	Host        string `json:"host"`
	LandingPath string `json:"landing_path"`
	UserAgent   string `json:"user_agent"`
}

// InsertRegAttribution 注册成功后落归因（失败仅日志级：注册主链路绝不因归因受阻）。
func (s *Store) InsertRegAttribution(a *RegAttribution) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO registration_attribution (user_id, tenant_id, utm_source, utm_medium, utm_campaign, utm_term, utm_content, ref_code, host, landing_path, user_agent, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.UserID, a.TenantID, a.UTMSource, a.UTMMedium, a.UTMCampaign, a.UTMTerm, a.UTMContent,
		a.RefCode, a.Host, a.LandingPath, a.UserAgent, time.Now().Format(time.RFC3339))
	return err
}

// FunnelRow 漏斗单行（按渠道）。
type FunnelRow struct {
	Source     string `json:"source"`   // utm_source（空=direct 自然量）
	Referral   string `json:"referral"` // ref_code（裂变码前 8 位聚合：ref:<code>）
	Registered int64  `json:"registered"`
	Activated  int64  `json:"activated"` // 激活：用户名下有真实用量流水
	Exhausted  int64  `json:"exhausted"` // 耗尽：租户曾发放额度且当前双桶可用=0
	FirstPay   int64  `json:"first_pay"` // 首购：租户 ≥1 笔已付订单
	Renewed    int64  `json:"renewed"`   // 续费：租户 ≥2 笔已付订单
}

// FunnelStats 时间窗内注册 cohort 的五环节漏斗（按 utm_source 聚合）。
// since=统计起点（注册时间的下限）；含 direct 与 ref 归并行。
func (s *Store) FunnelStats(since time.Time) ([]*FunnelRow, error) {
	sinceStr := since.Format(time.RFC3339)
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT
		   COALESCE(NULLIF(a.utm_source,''),'direct') AS src,
		   COUNT(DISTINCT a.user_id) AS registered,
		   COUNT(DISTINCT CASE WHEN EXISTS (SELECT 1 FROM usage_ledger u WHERE u.user_id=a.user_id AND u.quantity>0) THEN a.user_id END) AS activated,
		   COUNT(DISTINCT CASE WHEN (
		         SELECT COALESCE(b.balance,0) + (SELECT COALESCE(SUM(g."left"),0) FROM quota_grants g
		                  WHERE g.tenant_id=a.tenant_id AND g."left">0 AND g.expires_at>?)
		         FROM balance_accounts b WHERE b.tenant_id=a.tenant_id) <= 0
		         AND EXISTS (SELECT 1 FROM quota_grants g2 WHERE g2.tenant_id=a.tenant_id)
		         THEN a.user_id END) AS exhausted,
		   COUNT(DISTINCT CASE WHEN (SELECT COUNT(*) FROM orders o WHERE o.tenant_id=a.tenant_id AND o.status='paid')>=1 THEN a.user_id END) AS first_pay,
		   COUNT(DISTINCT CASE WHEN (SELECT COUNT(*) FROM orders o WHERE o.tenant_id=a.tenant_id AND o.status='paid')>=2 THEN a.user_id END) AS renewed
		 FROM registration_attribution a
		 WHERE a.created_at>=?
		 GROUP BY 1
		 ORDER BY registered DESC`, sinceStr, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*FunnelRow
	for rows.Next() {
		r := &FunnelRow{}
		if err := rows.Scan(&r.Source, &r.Registered, &r.Activated, &r.Exhausted, &r.FirstPay, &r.Renewed); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	// 裂变渠道（有邀请码的注册，单列聚合便于看 KOL 效果）
	refRows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT 'ref:'||SUBSTR(a.ref_code,1,8) AS src,
		   COUNT(DISTINCT a.user_id),
		   COUNT(DISTINCT CASE WHEN EXISTS (SELECT 1 FROM usage_ledger u WHERE u.user_id=a.user_id AND u.quantity>0) THEN a.user_id END),
		   0,
		   COUNT(DISTINCT CASE WHEN (SELECT COUNT(*) FROM orders o WHERE o.tenant_id=a.tenant_id AND o.status='paid')>=1 THEN a.user_id END),
		   COUNT(DISTINCT CASE WHEN (SELECT COUNT(*) FROM orders o WHERE o.tenant_id=a.tenant_id AND o.status='paid')>=2 THEN a.user_id END)
		 FROM registration_attribution a
		 WHERE a.created_at>=? AND a.ref_code<>''
		 GROUP BY 1 ORDER BY 2 DESC LIMIT 15`, sinceStr)
	if err == nil {
		defer refRows.Close()
		for refRows.Next() {
			r := &FunnelRow{}
			if e := refRows.Scan(&r.Referral, &r.Registered, &r.Activated, &r.Exhausted, &r.FirstPay, &r.Renewed); e == nil {
				r.Source = r.Referral
				out = append(out, r)
			}
		}
	}
	return out, nil
}
