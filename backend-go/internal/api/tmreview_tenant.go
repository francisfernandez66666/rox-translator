// ============ tmreview_tenant.go · 职责说明 ============
// ★ F-62（2026-09-26 批 I-8）TM 待审池的**租户侧只读接口**（补「去向腿」）。
//
//	G GET /api/me/tm-review/list?status=pending|approved|rejected|''(全部)
//
// 出参 = 本租户候选的白名单投影（rows）+ 三态真计数（summary）。
// 鉴权与越权口径（§一·3 与 F-55/F-62 的教训）：
//   - 过滤用的 tid **只取自已鉴权用户 u.TenantID**（JWT→DB 查回的真实归属），
//     不走 effTenant()、不读 X-Tenant-ID——那正是本轮 UAT 抓出的「读写不同源 / 头能换租户」一族；
//   - 平台超管（tenant_id=0）在本接口没有租户归属，直接 403，让其走超管审核台，
//     绝不退化成跨租户全表读；
//   - 只读：本文件不注册任何写路径，审批仍是 /api/admin/tm-review/* 超管独占。
//
// 错误一律 s.writeError + apierrors（AGENTS §一·8，本文件内零内联 4xx/5xx）。
// =============================================
package api

import (
	"net/http"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// tmReviewTenantLimit 租户侧一次最多回看条数（与 store 侧上限同档；超出部分由 summary 报真数）。
const tmReviewTenantLimit = 200

// myTmReviewJSON 租户侧候选出参（字段口径见 store.TmReviewTenantRow：不含 reviewer/内部主键）。
type myTmReviewJSON struct {
	ID         int64  `json:"id"`
	Zh         string `json:"zh"`
	Lang       string `json:"lang"`
	Trans      string `json:"trans"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	HitCount   int64  `json:"hit_count"`
	CreatedAt  string `json:"created_at"`
	ReviewedAt string `json:"reviewed_at"`
}

// handleMyTmReviewList 本租户 TM 候选进度（只读）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（status 查询参数 = 三态白名单或空=全部）。
func (s *Server) handleMyTmReviewList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "该接口仅支持 GET"))
		return
	}
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	if s.Store == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "平台存储未初始化"))
		return
	}
	// ★ 租户归属只认 token 里的用户（u.TenantID 由 DB 查回），X-Tenant-ID 在这里没有任何话语权。
	tid := u.TenantID
	if tid <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "此接口为租户自助视图，平台账号请使用超管 TM 审核台"))
		return
	}
	status, all, ok := store.NormTmReviewStatus(r.URL.Query().Get("status"))
	if !ok {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "status 取值仅支持 pending / approved / rejected（留空为全部）"))
		return
	}
	rows, err := s.Store.ListTmReviewsForTenant(tid, status, all, tmReviewTenantLimit)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	sum, err := s.Store.SummarizeTmReviewsForTenant(tid)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	out := make([]myTmReviewJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, myTmReviewJSON{
			ID: row.ID, Zh: row.Zh, Lang: row.Lang, Trans: row.Trans, Source: row.Source,
			Status: row.Status, HitCount: row.HitCount, CreatedAt: row.CreatedAt, ReviewedAt: row.ReviewedAt,
		})
	}
	// truncated：列表被 200 条上限截断时如实出声，前端摘要按 summary 的真计数显示，
	// 不拿 rows.length 冒充总数（「界面说一套、库里做一套」是本轮 UAT 的高频缺陷形）。
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "candidates": out, "summary": sum,
		"truncated": len(out) >= tmReviewTenantLimit && sum.Total > int64(len(out)),
	})
}
