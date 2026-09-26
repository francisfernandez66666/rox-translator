// ============ 本文件职责中文说明 ============
// S4 增长漏斗 API（★ 2026-09-14）：超管看板「注册→激活→耗尽→首购→续费」
// 按渠道（utm_source / 裂变码）聚合，数据源 registration_attribution。
// GET /api/admin/funnel?days=30（默认 30 天，上限 365）。
package api

import (
	"net/http"
	"strconv"
	"time"
	"translator/internal/auth"
	apierrors "translator/internal/errors"
)

// handleAdminFunnel GET /api/admin/funnel：注册 cohort 五环节增长漏斗（仅超管，S4）。
func (s *Server) handleAdminFunnel(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法把两件事挤进同一个条件一律回 403，
		//   于是 token 过期的超管看到的是一句「仅超级管理员可见」——他差的只是重新登录，
		//   而前端只在 401 才走重登录链路，结果是人被留在本页反复撞闸）。
		s.writeAuthzError(w, r, err)
		return
	}
	if !auth.IsSuperAdmin(u) {
		// 角色专属文案原样保留（这句是「你是谁」之外的「该不该你做」，仍 403）
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "仅超级管理员可见"))
		return
	}
	days := 30
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 365 {
		days = d
	}
	rows, err := s.Store.FunnelStats(time.Now().AddDate(0, 0, -days))
	if err != nil {
		// F-64②：漏斗聚合是一次真实库查询，失败属服务端故障 → 500；
		//   旧写法回 200 + success:false，超管看板会把它渲染成「零注册零转化」的空漏斗，
		//   看起来像增长停了而不是查询挂了——这类误判的代价是整渠道投放决策。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "days": days, "rows": rows})
}
