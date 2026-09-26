// ============ tmreview_tenant.go · 职责说明 ============
// ★ F-62（2026-09-26 批 I-8）TM 待审池的**租户侧只读视图**。
//
// 背景：TM 自闭环（D7）把 bitext/TMX 导入与命中阈值候选一律送进 tm_review，
// 前台文案已如实告知「已提交，待平台审核」（F-24 修的是文案腿），
// 但审核进度只对超管可见（路由只有 /api/admin/tm-review/*，且 ListTmReviews 无租户过滤）。
// ⇒ 客户被告知「待审」却永远看不到去向，属静默降级。
//
// 本文件只做**读**：按 token 内 tenant_id 裁剪、状态白名单三态（pending/approved/rejected）、
// 不提供任何写路径（审批仍是超管独占）。为什么单独建文件：AGENTS.md §一·1 store 冻结规则，
// 新能力必须按域新建文件，不往既有大文件追加方法。
//
// 安全口径（§一·3 与 F-55 的教训）：过滤条件只能来自**已鉴权用户的 TenantID**，
// 禁止吃 X-Tenant-ID 请求头——所以这里把 tid 作为显式入参，由 api 层从 token 用户取值。
// =============================================
package store

import (
	"strconv"
	"strings"

	"translator/internal/db"
)

// TmReviewTenantRow 租户侧候选出参视图（**租户可见字段**的白名单投影）。
// 与 store.TmReview 的差异刻意为之：不外发 tenant_id（本租户隐含）、
// 不外发 reviewer/ref_type/ref_id（平台内部人员与工单/反馈主键，属平台侧溯源信息）。
// ★ hit_count 保留：它向租户解释「为什么这句会进审核池」，是审核时效之外的第二项诚实信息。
type TmReviewTenantRow struct {
	ID         int64  `json:"id"`
	Zh         string `json:"zh"`
	Lang       string `json:"lang"`
	Trans      string `json:"trans"`
	Source     string `json:"source"` // bitext | tmx | hit_threshold | feedback
	Status     string `json:"status"` // pending | approved | rejected
	HitCount   int64  `json:"hit_count"`
	CreatedAt  string `json:"created_at"`
	ReviewedAt string `json:"reviewed_at"`
}

// tmReviewTenantCols 租户侧列清单（Scan 顺序契约；只取投影需要的列）。
const tmReviewTenantCols = "id, zh, lang, trans, COALESCE(source,''), status, COALESCE(hit_count,0), created_at, COALESCE(reviewed_at,'')"

// TmReviewStatusWhitelist 租户侧可见的三态。
// ★ 不放空串：空串在超管侧语义是「全部」，租户侧的「全部」由 handler 显式表达，
//
//	避免有人把任意字符串直接拼进 status=?（脏值只会查不到，但白名单能让口径写死在代码里）。
var TmReviewStatusWhitelist = map[string]bool{"pending": true, "approved": true, "rejected": true}

// NormTmReviewStatus 状态参数归一：返回 (白名单值或空串, 是否为合法取值)。
// 合法 = 三态之一，或空串/全部（大小写与空格容错后仍为 all/*）。
// 非法（如 pending,approved 这种拼接、或历史脏值）→ ok=false，由 api 层报参数错误，
// 绝不静默当成「全部」返回——那会让「只看已通过」的过滤条件形同虚设。
func NormTmReviewStatus(raw string) (status string, all bool, ok bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == "all" || v == "*" {
		return "", true, true
	}
	if TmReviewStatusWhitelist[v] {
		return v, false, true
	}
	return "", false, false
}

// ListTmReviewsForTenant 租户侧候选列表（只读、按本租户裁剪、状态三态过滤）。
// 参数 tid: 租户 ID（**必须**来自已鉴权用户，tid<=0 直接返回空集，不退化成全表）；
// status: 归一后的状态（all=true 时忽略）。
func (s *Store) ListTmReviewsForTenant(tid int64, status string, all bool, limit int) ([]*TmReviewTenantRow, error) {
	if tid <= 0 {
		return []*TmReviewTenantRow{}, nil // 平台/无租户账号：本视图无内容，绝不放宽成跨租户查询
	}
	if limit <= 0 || limit > 200 {
		limit = 200 // 与超管侧同档上限（租户侧更不该一次拉全池）
	}
	q := "SELECT " + tmReviewTenantCols + " FROM tm_review WHERE tenant_id=?"
	args := []interface{}{tid}
	if !all && status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	q += " ORDER BY id DESC LIMIT " + strconv.Itoa(limit) // 上限由代码控制（≤200）、非用户输入；拼字面量而非绑参，免得 SQLite/PG 占位符改写层在 LIMIT 位踩坑
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TmReviewTenantRow{}
	for rows.Next() {
		var r TmReviewTenantRow
		if err := rows.Scan(&r.ID, &r.Zh, &r.Lang, &r.Trans, &r.Source,
			&r.Status, &r.HitCount, &r.CreatedAt, &r.ReviewedAt); err != nil {
			continue
		}
		out = append(out, &r)
	}
	return out, nil
}

// TmReviewTenantSummary 租户侧三态计数（给「待审 X / 已通过 Y / 已驳回 Z」摘要行用）。
// 为什么单独算：列表有 200 条上限，前端数出来的长度不是真计数，
// 拿截断结果当总数报给客户就是又一处「界面说一套、库里做一套」。
type TmReviewTenantSummary struct {
	Pending  int64 `json:"pending"`
	Approved int64 `json:"approved"`
	Rejected int64 `json:"rejected"`
	Total    int64 `json:"total"`
}

// SummarizeTmReviewsForTenant 本租户候选按状态计数。tid<=0 返回全零（同上，不跨租户）。
func (s *Store) SummarizeTmReviewsForTenant(tid int64) (*TmReviewTenantSummary, error) {
	sum := &TmReviewTenantSummary{}
	if tid <= 0 {
		return sum, nil
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT status, COUNT(*) FROM tm_review WHERE tenant_id=? GROUP BY status", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			continue
		}
		switch st {
		case "pending":
			sum.Pending += n
		case "approved":
			sum.Approved += n
		case "rejected":
			sum.Rejected += n
		default: // 历史脏状态只进 total，不进三态，免得摘要出现「三态相加≠总数」的假账
		}
		sum.Total += n
	}
	return sum, nil
}
